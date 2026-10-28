package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/example/autostream-control-panel/internal/store"
)

func TestStreamSettingsRevisionHTTPRejectsStaleBasicAndRuntimeWithoutOverwrite(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "settings-operator"}, "correct horse battery", []string{"streams.read", "streams.update"}); err != nil {
		t.Fatal(err)
	}
	streams := store.NewMemoryStreamStore()
	stream, err := streams.CreateStream(t.Context(), "original")
	if err != nil {
		t.Fatal(err)
	}
	handler := NewServer(streams, WithAuthStore(auth), WithAuditStore(auth))
	cookie, csrf := loginForTest(t, handler, "settings-operator", "correct horse battery")
	request := func(method, suffix string, body map[string]any) *httptest.ResponseRecorder {
		t.Helper()
		var raw []byte
		if body != nil {
			raw, err = json.Marshal(body)
			if err != nil {
				t.Fatal(err)
			}
		}
		req := httptest.NewRequest(method, "/streams/"+stream.ID+suffix, bytes.NewReader(raw))
		req.AddCookie(cookie)
		req.Header.Set("X-CSRF-Token", csrf)
		res := httptest.NewRecorder()
		handler.ServeHTTP(res, req)
		return res
	}
	decode := func(response *httptest.ResponseRecorder) store.Stream {
		t.Helper()
		if response.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
		}
		var value store.Stream
		if err := json.Unmarshal(response.Body.Bytes(), &value); err != nil {
			t.Fatal(err)
		}
		return value
	}
	initial := decode(request(http.MethodGet, "", nil))
	if initial.SettingsRevision != store.UnconfiguredStreamSettingsRevision {
		t.Fatalf("GET did not expose legacy settings authority: %q", initial.SettingsRevision)
	}
	first := decode(request(http.MethodPut, "/settings", map[string]any{"name": "first writer", "expected_settings_revision": initial.SettingsRevision}))
	if first.SettingsRevision == initial.SettingsRevision || !store.ValidStreamSettingsRevision(first.SettingsRevision) {
		t.Fatalf("settings did not advance its own revision: %q", first.SettingsRevision)
	}
	assertConflict := func(response *httptest.ResponseRecorder) {
		t.Helper()
		var result struct {
			Code string `json:"code"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		if response.Code != http.StatusConflict || result.Code != "revision_conflict" {
			t.Fatalf("stale write status=%d code=%q", response.Code, result.Code)
		}
	}
	assertConflict(request(http.MethodPut, "/settings", map[string]any{"name": "stale overwrite", "expected_settings_revision": initial.SettingsRevision}))
	current := decode(request(http.MethodGet, "", nil))
	if current.Name != "first writer" || current.SettingsRevision != first.SettingsRevision {
		t.Fatalf("stale basic request changed settings: name=%q revision=%q", current.Name, current.SettingsRevision)
	}
	if _, err := streams.UpdateStreamStatus(t.Context(), stream.ID, "completed"); err != nil {
		t.Fatal(err)
	}
	runtime := decode(request(http.MethodPut, "/runtime-settings", map[string]any{"encoder_audio_gain_db": -3, "overlay_profile_id": "", "expected_settings_revision": first.SettingsRevision}))
	assertConflict(request(http.MethodPut, "/runtime-settings", map[string]any{"encoder_audio_gain_db": 6, "overlay_profile_id": "", "expected_settings_revision": first.SettingsRevision}))
	assertConflict(request(http.MethodPut, "/settings", map[string]any{"name": "late basic overwrite", "expected_settings_revision": first.SettingsRevision}))
	for _, invalid := range []string{"", "latest", "2026-10-28T00:00:00+00:00", "2026-10-28T00:00:00.1234567Z"} {
		for _, suffix := range []string{"/settings", "/runtime-settings"} {
			response := request(http.MethodPut, suffix, map[string]any{"expected_settings_revision": invalid})
			if response.Code != http.StatusBadRequest {
				t.Fatalf("invalid revision accepted: suffix=%s status=%d", suffix, response.Code)
			}
		}
	}
	after := decode(request(http.MethodGet, "", nil))
	if after.Name != "first writer" || after.EncoderAudioGainDB != -3 || after.SettingsRevision != runtime.SettingsRevision {
		t.Fatalf("rejected requests changed data: name=%q gain=%v revision=%q", after.Name, after.EncoderAudioGainDB, after.SettingsRevision)
	}
	// An omitted expectation remains an explicitly supported older caller, not
	// an implicit adoption of a new revision by the current editing UI.
	legacy := decode(request(http.MethodPut, "/settings", map[string]any{"name": "legacy deliberate caller"}))
	if legacy.Name != "legacy deliberate caller" || legacy.SettingsRevision == after.SettingsRevision {
		t.Fatal("optional-field compatibility did not advance settings authority")
	}
}

type concurrentStreamSettingsWriter struct {
	*store.MemoryStreamStore
	beforeUpdate func(context.Context, string) error
}

func (s *concurrentStreamSettingsWriter) UpdateStreamSettings(ctx context.Context, id string, settings store.StreamSettings) (store.Stream, error) {
	if s.beforeUpdate != nil {
		if err := s.beforeUpdate(ctx, id); err != nil {
			return store.Stream{}, err
		}
	}
	return s.MemoryStreamStore.UpdateStreamSettings(ctx, id, settings)
}

func TestStreamSettingsRevisionHTTPDirectArchiveCombinationRejectsBeforeSideEffects(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "archive-settings-operator"}, "correct horse battery", []string{"streams.read", "streams.update"}); err != nil {
		t.Fatal(err)
	}
	streams := store.NewMemoryStreamStore()
	stream, err := streams.CreateStream(t.Context(), "original archive stream")
	if err != nil {
		t.Fatal(err)
	}
	profiles := store.NewMemoryProfileStore()
	profile, err := profiles.CreateProfile(t.Context(), store.ProfileArchive, "original direct archive", map[string]any{"stream_archive_direct": true, "retention_days": 7})
	if err != nil {
		t.Fatal(err)
	}
	initial, err := streams.UpdateStreamSettings(t.Context(), stream.ID, store.StreamSettings{ArchiveProfileID: profile.ID})
	if err != nil {
		t.Fatal(err)
	}
	writes := 0
	wrapped := &concurrentStreamSettingsWriter{MemoryStreamStore: streams, beforeUpdate: func(ctx context.Context, id string) error {
		writes++
		// This separate store writer models a second process passing the HTTP
		// lifecycle mutex after this request's initial revision read.
		_, err := streams.UpdateStreamEncoderRuntimeSettings(ctx, id, -3, "")
		return err
	}}
	handler := NewServer(wrapped, WithAuthStore(auth), WithAuditStore(auth), WithProfileStore(profiles))
	cookie, csrf := loginForTest(t, handler, "archive-settings-operator", "correct horse battery")
	request := func(body map[string]any) *httptest.ResponseRecorder {
		t.Helper()
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		req := httptest.NewRequest(http.MethodPut, "/streams/"+stream.ID+"/settings", bytes.NewReader(raw))
		req.AddCookie(cookie)
		req.Header.Set("X-CSRF-Token", csrf)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, req)
		return response
	}
	response := request(map[string]any{"name": "must not apply", "archive_profile_id": profile.ID, "archive_retention_days": 31, "expected_settings_revision": initial.SettingsRevision})
	var result struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if response.Code != http.StatusBadRequest || result.Code != "stream_settings_archive_revision_unsupported" {
		t.Errorf("guarded legacy direct archive combination: status=%d code=%q", response.Code, result.Code)
	}
	if writes != 0 {
		t.Errorf("unsupported combination reached a settings writer: %d", writes)
	}
	current, err := streams.GetStream(t.Context(), stream.ID)
	if err != nil {
		t.Fatal(err)
	}
	if current.Name != initial.Name || current.SettingsRevision != initial.SettingsRevision {
		t.Error("unsupported request changed stream state")
	}
	currentProfile, err := profiles.GetProfile(t.Context(), store.ProfileArchive, profile.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(currentProfile.Config, profile.Config) || currentProfile.UpdatedAt != profile.UpdatedAt {
		t.Error("rejected request changed an existing direct archive profile")
	}
	allProfiles, err := profiles.ListProfiles(t.Context(), store.ProfileArchive)
	if err != nil || len(allProfiles) != 1 {
		t.Fatalf("rejected request created an orphan archive profile: count=%d error=%v", len(allProfiles), err)
	}

	// The older, unguarded legacy request remains supported. The new editing
	// UI uses managed profile IDs; it does not send these direct fields.
	wrapped.beforeUpdate = nil
	legacy := request(map[string]any{"name": "legacy direct caller", "archive_profile_id": profile.ID, "archive_retention_days": 31})
	if legacy.Code != http.StatusOK {
		t.Fatalf("legacy request compatibility: status=%d", legacy.Code)
	}
	var applied store.Stream
	if err := json.Unmarshal(legacy.Body.Bytes(), &applied); err != nil {
		t.Fatal(err)
	}
	beforeClear, err := profiles.GetProfile(t.Context(), store.ProfileArchive, profile.ID)
	if err != nil {
		t.Fatal(err)
	}
	cleared := request(map[string]any{"name": "explicit clear", "archive_profile_id": profile.ID, "archive_oauth_account_id": "", "archive_folder_id": "", "archive_shared_drive": false, "archive_shared_drive_id": "", "archive_file_name": "", "archive_retention_days": 0, "expected_settings_revision": applied.SettingsRevision})
	if cleared.Code != http.StatusOK {
		t.Fatalf("explicit empty direct fields must still support CAS: status=%d", cleared.Code)
	}
	afterClear, err := profiles.GetProfile(t.Context(), store.ProfileArchive, profile.ID)
	if err != nil || !reflect.DeepEqual(beforeClear.Config, afterClear.Config) || beforeClear.UpdatedAt != afterClear.UpdatedAt {
		t.Fatal("empty clear unexpectedly materialized archive settings")
	}
}

func TestStreamSettingsRevisionHTTPConcurrentWriterRestoresUnchangedAndReplacedAssignments(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "assignment-settings-operator"}, "correct horse battery", []string{"streams.read", "streams.update", "services.assign", "workers.assign"}); err != nil {
		t.Fatal(err)
	}
	streams := store.NewMemoryStreamStore()
	stream, err := streams.CreateStream(t.Context(), "original assignment stream")
	if err != nil {
		t.Fatal(err)
	}
	initial, err := streams.UpdateStreamSettings(t.Context(), stream.ID, store.StreamSettings{})
	if err != nil {
		t.Fatal(err)
	}
	for serviceID, serviceType := range map[string]string{"encoder-retained": "encoder_recorder", "worker-old": "worker", "worker-new": "worker"} {
		registerServiceInstance(t, auth, serviceID, serviceType)
	}
	for _, serviceID := range []string{"encoder-retained", "worker-old"} {
		if _, err := auth.AssignServiceToStreamWithRole(t.Context(), serviceID, stream.ID, "bootstrap", "primary"); err != nil {
			t.Fatal(err)
		}
	}
	writes := 0
	wrapped := &concurrentStreamSettingsWriter{MemoryStreamStore: streams, beforeUpdate: func(ctx context.Context, id string) error {
		writes++
		_, err := streams.UpdateStreamEncoderRuntimeSettings(ctx, id, -3, "")
		return err
	}}
	handler := NewServer(wrapped, WithAuthStore(auth), WithAuditStore(auth), WithServiceRegistryStore(auth))
	cookie, csrf := loginForTest(t, handler, "assignment-settings-operator", "correct horse battery")
	raw, err := json.Marshal(map[string]any{"name": "must not persist", "encoder_service_id": "encoder-retained", "worker_service_id": "worker-new", "expected_settings_revision": initial.SettingsRevision})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPut, "/streams/"+stream.ID+"/settings", bytes.NewReader(raw))
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrf)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, req)
	var result struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if response.Code != http.StatusConflict || result.Code != "revision_conflict" {
		t.Errorf("final CAS rejection did not complete compensation: status=%d code=%q", response.Code, result.Code)
	}
	if writes != 1 {
		t.Errorf("the competing real store writer was not reached exactly once: %d", writes)
	}
	current, err := streams.GetStream(t.Context(), stream.ID)
	if err != nil || current.Name != initial.Name || current.EncoderAudioGainDB != -3 || current.SettingsRevision == initial.SettingsRevision {
		t.Fatal("the competing committed settings were overwritten or not exercised")
	}
	for serviceID, expectedStream := range map[string]string{"encoder-retained": stream.ID, "worker-old": stream.ID, "worker-new": ""} {
		service, err := auth.GetService(t.Context(), serviceID)
		if err != nil || service.CurrentStreamID != expectedStream {
			t.Errorf("assignment was not restored: service=%s error=%v", serviceID, err)
		}
	}
}
