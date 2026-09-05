package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/example/autostream-control-panel/internal/servicecall"
	"github.com/example/autostream-control-panel/internal/store"
)

func TestCreateStreamPreservesActiveRuntimeHeartbeatAndPreview(t *testing.T) {
	heartbeatAt := time.Date(2026, 8, 23, 1, 2, 3, 0, time.UTC)
	auth := store.NewMemoryAuthStore(store.WithMemoryServiceHeartbeatClock(func() time.Time { return heartbeatAt }))
	if err := auth.AddUser(store.User{Username: "operator", Roles: []string{"stream_operator"}}, "correct horse battery", []string{"streams.create", "streams.read", "streams.update", "services.assign", "workers.assign", "service_health.read"}); err != nil {
		t.Fatal(err)
	}
	streams := store.NewMemoryStreamStore()
	profiles := store.NewMemoryProfileStore()
	encoderProfile, err := profiles.CreateProfile(t.Context(), store.ProfileEncoder, "encoder-shared", map[string]any{"video_codec": "h264"})
	if err != nil {
		t.Fatal(err)
	}
	otherEncoderProfile, err := profiles.CreateProfile(t.Context(), store.ProfileEncoder, "encoder-other", map[string]any{"video_codec": "hevc"})
	if err != nil {
		t.Fatal(err)
	}
	archiveProfile, err := profiles.CreateProfile(t.Context(), store.ProfileArchive, "archive-shared", map[string]any{"retention_days": 30})
	if err != nil {
		t.Fatal(err)
	}
	active, err := streams.CreateStream(t.Context(), "active-stream")
	if err != nil {
		t.Fatal(err)
	}
	active, err = streams.UpdateStreamSettings(t.Context(), active.ID, store.StreamSettings{EncoderProfileID: encoderProfile.ID, ArchiveProfileID: archiveProfile.ID})
	if err != nil {
		t.Fatal(err)
	}
	active, err = streams.UpdateStreamStatus(t.Context(), active.ID, "live")
	if err != nil {
		t.Fatal(err)
	}

	encoderToken := registerStreamIsolationService(t, auth, "encoder-active", "encoder_recorder", []string{"service.register", "service.heartbeat", "service.config.read", "encoder.status.write"})
	workerToken := registerStreamIsolationService(t, auth, "worker-active", "worker", []string{"service.register", "service.heartbeat", "service.config.read"})
	if _, err := auth.AssignServiceToStream(t.Context(), "encoder-active", active.ID, "bootstrap"); err != nil {
		t.Fatal(err)
	}
	if _, err := auth.AssignServiceToStream(t.Context(), "worker-active", active.ID, "bootstrap"); err != nil {
		t.Fatal(err)
	}
	if _, err := auth.Heartbeat(t.Context(), encoderToken, store.ServiceHeartbeat{ServiceID: "encoder-active", Status: "online", CurrentStreamID: active.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err := auth.Heartbeat(t.Context(), workerToken, store.ServiceHeartbeat{ServiceID: "worker-active", Status: "online", CurrentStreamID: active.ID}); err != nil {
		t.Fatal(err)
	}

	dispatcher := &previewFakeDispatcher{result: servicecall.PreviewAssetResult{
		StatusCode: http.StatusOK,
		Success:    true,
		Body:       []byte("#EXTM3U\n#EXT-X-VERSION:3\n#EXTINF:2.0,\nsegment-000001.ts\n"),
	}}
	registry := &streamIsolationServiceRegistry{ServiceRegistryStore: auth}
	handler := NewServer(
		streams,
		WithAuthStore(auth),
		WithAuditStore(auth),
		WithServiceRegistryStore(registry),
		WithProfileStore(profiles),
		WithServiceDispatcher(dispatcher),
		WithPreviewSigningKey("stream-create-isolation-preview-key"),
	)
	cookie, csrf := loginForTest(t, handler, "operator", "correct horse battery")

	beforeActive, err := streams.GetStream(t.Context(), active.ID)
	if err != nil {
		t.Fatal(err)
	}
	beforeEncoder, err := auth.GetService(t.Context(), "encoder-active")
	if err != nil {
		t.Fatal(err)
	}
	beforeWorker, err := auth.GetService(t.Context(), "worker-active")
	if err != nil {
		t.Fatal(err)
	}
	beforeRuntime := streamIsolationRuntimeConfig(t, handler, encoderToken, "encoder-active")
	streamIsolationPreview(t, handler, cookie, active.ID, http.StatusOK)
	streamIsolationPreviewLink(t, handler, cookie, csrf, active.ID, http.StatusCreated)

	payload := `{"name":"new-inactive-stream","encoder_profile_id":"` + encoderProfile.ID + `","archive_profile_id":"` + archiveProfile.ID + `"}`
	created := createStreamForIsolation(t, handler, cookie, csrf, payload)

	if created.Status != "created" || created.EncoderProfileID != encoderProfile.ID || created.ArchiveProfileID != archiveProfile.ID {
		t.Fatalf("new stream configuration was not persisted: %#v", created)
	}
	if created.AssignedEncoderID != "" || created.AssignedWorkerID != "" {
		t.Errorf("stream creation must not assign runtime services: encoder=%q worker=%q", created.AssignedEncoderID, created.AssignedWorkerID)
	}
	for _, variant := range []struct {
		name    string
		payload string
	}{
		{name: "unassigned", payload: `{"name":"new-unassigned"}`},
		{name: "same encoder profile", payload: `{"name":"new-same-encoder-profile","encoder_profile_id":"` + encoderProfile.ID + `"}`},
		{name: "same archive profile", payload: `{"name":"new-same-archive-profile","archive_profile_id":"` + archiveProfile.ID + `"}`},
		{name: "different profile", payload: `{"name":"new-different-profile","encoder_profile_id":"` + otherEncoderProfile.ID + `"}`},
	} {
		variant := variant
		t.Run(variant.name, func(t *testing.T) {
			candidate := createStreamForIsolation(t, handler, cookie, csrf, variant.payload)
			assignments, listErr := auth.ListStreamAssignments(t.Context(), candidate.ID)
			if listErr != nil {
				t.Fatal(listErr)
			}
			if candidate.AssignedEncoderID != "" || candidate.AssignedWorkerID != "" || len(assignments) != 0 {
				t.Errorf("config-only create wrote runtime assignment: stream=%#v assignments=%s", candidate, formatSafeHTTPSensitiveDiagnostic(assignments))
			}
		})
	}
	editReq := httptest.NewRequest(http.MethodPut, "/streams/"+created.ID+"/settings", strings.NewReader(`{"name":"edited-inactive-stream","encoder_profile_id":"`+otherEncoderProfile.ID+`"}`))
	editReq.AddCookie(cookie)
	editReq.Header.Set("X-CSRF-Token", csrf)
	editRes := httptest.NewRecorder()
	handler.ServeHTTP(editRes, editReq)
	if editRes.Code != http.StatusOK {
		t.Fatalf("inactive stream edit status=%d body=%s", editRes.Code, editRes.Body.String())
	}
	afterActive, err := streams.GetStream(t.Context(), active.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(afterActive, beforeActive) {
		t.Errorf("active stream changed during unrelated create:\nbefore=%#v\nafter=%#v", beforeActive, afterActive)
	}
	afterEncoder, err := auth.GetService(t.Context(), "encoder-active")
	if err != nil {
		t.Fatal(err)
	}
	afterWorker, err := auth.GetService(t.Context(), "worker-active")
	if err != nil {
		t.Fatal(err)
	}
	assertStreamIsolationServiceUnchanged(t, "encoder", beforeEncoder, afterEncoder)
	assertStreamIsolationServiceUnchanged(t, "worker", beforeWorker, afterWorker)

	activeAssignments, err := auth.ListStreamAssignments(t.Context(), active.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(activeAssignments) != 2 || assignedServiceID(activeAssignments, "encoder_recorder") != "encoder-active" || assignedServiceID(activeAssignments, "worker") != "worker-active" {
		t.Errorf("active assignments changed during create: %s", formatSafeHTTPSensitiveDiagnostic(activeAssignments))
	}
	createdAssignments, err := auth.ListStreamAssignments(t.Context(), created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(createdAssignments) != 0 {
		t.Errorf("create wrote runtime assignments for inactive stream: %s", formatSafeHTTPSensitiveDiagnostic(createdAssignments))
	}
	afterRuntime := streamIsolationRuntimeConfig(t, handler, encoderToken, "encoder-active")
	if !reflect.DeepEqual(afterRuntime, beforeRuntime) {
		t.Errorf("encoder runtime config changed during unrelated create:\nbefore=%#v\nafter=%#v", beforeRuntime, afterRuntime)
	}

	streamIsolationPreview(t, handler, cookie, active.ID, http.StatusOK)
	streamIsolationPreviewLink(t, handler, cookie, csrf, active.ID, http.StatusCreated)
	heartbeatAt = heartbeatAt.Add(10 * time.Second)
	heartbeatReq := httptest.NewRequest(http.MethodPost, "/services/heartbeat", strings.NewReader(`{"service_id":"encoder-active","status":"online","current_stream_id":"`+active.ID+`"}`))
	heartbeatReq.Header.Set("Authorization", "Bearer "+encoderToken.RawToken)
	heartbeatRes := httptest.NewRecorder()
	handler.ServeHTTP(heartbeatRes, heartbeatReq)
	if heartbeatRes.Code != http.StatusAccepted {
		t.Errorf("active encoder heartbeat after create status=%d body=%s", heartbeatRes.Code, heartbeatRes.Body.String())
	} else {
		freshEncoder, getErr := auth.GetService(t.Context(), "encoder-active")
		if getErr != nil {
			t.Fatal(getErr)
		}
		if freshEncoder.LastHeartbeatAt == nil || !freshEncoder.LastHeartbeatAt.Equal(heartbeatAt) {
			t.Errorf("heartbeat freshness did not advance after create: %#v", freshEncoder.LastHeartbeatAt)
		}
		if health, stale, _ := serviceHealthFields(freshEncoder, heartbeatAt); health != "healthy" || stale {
			t.Errorf("fresh service became unhealthy after create: health=%q stale=%v", health, stale)
		}
	}
	if dispatcher.startCalls != 0 || dispatcher.stopCalls != 0 || dispatcher.retryCalls != 0 {
		t.Errorf("stream create emitted lifecycle calls: start=%d stop=%d retry=%d", dispatcher.startCalls, dispatcher.stopCalls, dispatcher.retryCalls)
	}
	assertNoStreamIsolationAssignmentWrites(t, registry)
}

func TestCreateStreamRejectsLegacyAssignmentFieldsWithoutPartialStateOrValues(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "operator"}, "correct horse battery", []string{"streams.create"}); err != nil {
		t.Fatal(err)
	}
	streams := store.NewMemoryStreamStore()
	registry := &streamIsolationServiceRegistry{ServiceRegistryStore: auth}
	handler := NewServer(streams, WithAuthStore(auth), WithAuditStore(auth), WithServiceRegistryStore(registry))
	cookie, csrf := loginForTest(t, handler, "operator", "correct horse battery")
	cases := []struct {
		name      string
		payload   string
		sensitive []string
	}{
		{name: "encoder value", payload: `{"name":"legacy-encoder","encoder_service_id":"encoder-sensitive-legacy-id"}`, sensitive: []string{"encoder-sensitive-legacy-id"}},
		{name: "worker value", payload: `{"name":"legacy-worker","worker_service_id":"worker-sensitive-legacy-id"}`, sensitive: []string{"worker-sensitive-legacy-id"}},
		{name: "both values", payload: `{"name":"legacy-both","encoder_service_id":"encoder-sensitive-both","worker_service_id":"worker-sensitive-both"}`, sensitive: []string{"encoder-sensitive-both", "worker-sensitive-both"}},
		{name: "encoder empty", payload: `{"name":"legacy-empty","encoder_service_id":""}`},
		{name: "worker null", payload: `{"name":"legacy-null","worker_service_id":null}`},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/streams", strings.NewReader(testCase.payload))
			req.AddCookie(cookie)
			req.Header.Set("X-CSRF-Token", csrf)
			res := httptest.NewRecorder()
			handler.ServeHTTP(res, req)
			if res.Code != http.StatusBadRequest || !strings.Contains(res.Body.String(), `"code":"bad_request"`) {
				t.Fatalf("legacy create status=%d body=%s", res.Code, res.Body.String())
			}
			if warning := res.Header().Get("Warning"); warning != "" {
				t.Fatalf("removed create fields must not emit compatibility Warning=%q", warning)
			}
			for _, sensitive := range testCase.sensitive {
				if strings.Contains(res.Body.String(), sensitive) {
					t.Fatalf("legacy create rejection leaked an assignment ID: %s", res.Body.String())
				}
			}
		})
	}
	created, err := streams.ListStreams(t.Context())
	if err != nil || len(created) != 0 {
		t.Fatalf("legacy create rejection persisted a stream: streams=%#v err=%v", created, err)
	}

	for _, event := range auth.AuditEvents() {
		if event.Action == "streams.create" && event.Result == "success" {
			t.Fatalf("legacy create rejection emitted success audit: %#v", event)
		}
	}
	auditJSON, err := json.Marshal(auth.AuditEvents())
	if err != nil {
		t.Fatal(err)
	}
	for _, sensitive := range []string{"encoder-sensitive-legacy-id", "worker-sensitive-legacy-id", "encoder-sensitive-both", "worker-sensitive-both"} {
		if strings.Contains(string(auditJSON), sensitive) {
			t.Fatalf("legacy create failure audit leaked an assignment ID: %s", auditJSON)
		}
	}
	assertNoStreamIsolationAssignmentWrites(t, registry)
}

func TestCreateStreamPreservesArchiveRunAndArtifactReportTarget(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "operator", Roles: []string{"stream_operator"}}, "correct horse battery", []string{"streams.create", "services.assign"}); err != nil {
		t.Fatal(err)
	}
	streams := store.NewMemoryStreamStore()
	profiles := store.NewMemoryProfileStore()
	archiveProfile, err := profiles.CreateProfile(t.Context(), store.ProfileArchive, "archive-shared", map[string]any{"retention_days": 30})
	if err != nil {
		t.Fatal(err)
	}
	archiving, err := streams.CreateStream(t.Context(), "archive-in-progress")
	if err != nil {
		t.Fatal(err)
	}
	archiving, err = streams.UpdateStreamSettings(t.Context(), archiving.ID, store.StreamSettings{ArchiveProfileID: archiveProfile.ID})
	if err != nil {
		t.Fatal(err)
	}
	archiving, err = streams.UpdateStreamStatus(t.Context(), archiving.ID, "completed")
	if err != nil {
		t.Fatal(err)
	}
	archiveStartedAt := time.Date(2026, 8, 23, 0, 30, 0, 0, time.UTC)
	archiving, err = streams.PrepareStreamArchiveRun(t.Context(), archiving.ID, "run-01", archiveStartedAt)
	if err != nil {
		t.Fatal(err)
	}
	encoderToken := registerStreamIsolationService(t, auth, "encoder-archive", "encoder_recorder", []string{"service.register", "encoder.status.write"})
	if _, err := auth.AssignServiceToStream(t.Context(), "encoder-archive", archiving.ID, "bootstrap"); err != nil {
		t.Fatal(err)
	}
	dispatcher := &fakeServiceDispatcher{}
	registry := &streamIsolationServiceRegistry{ServiceRegistryStore: auth}
	handler := NewServer(streams, WithAuthStore(auth), WithAuditStore(auth), WithServiceRegistryStore(registry), WithProfileStore(profiles), WithServiceDispatcher(dispatcher))
	cookie, csrf := loginForTest(t, handler, "operator", "correct horse battery")

	unassigned := createStreamForIsolation(t, handler, cookie, csrf, `{"name":"unassigned-during-archive","archive_profile_id":"`+archiveProfile.ID+`"}`)
	if assignments, listErr := auth.ListStreamAssignments(t.Context(), unassigned.ID); listErr != nil || len(assignments) != 0 {
		t.Fatalf("unassigned create during archive wrote assignments: assignments=%s err=%v", formatSafeHTTPSensitiveDiagnostic(assignments), listErr)
	}
	created := createStreamForIsolation(t, handler, cookie, csrf, `{"name":"new-during-archive","archive_profile_id":"`+archiveProfile.ID+`"}`)
	if created.AssignedEncoderID != "" {
		t.Errorf("new stream stole archive encoder assignment: %q", created.AssignedEncoderID)
	}
	failedReq := httptest.NewRequest(http.MethodPost, "/streams", strings.NewReader(`{"name":"invalid-during-archive","archive_profile_id":"missing-profile"}`))
	failedReq.AddCookie(cookie)
	failedReq.Header.Set("X-CSRF-Token", csrf)
	failedRes := httptest.NewRecorder()
	handler.ServeHTTP(failedRes, failedReq)
	if failedRes.Code != http.StatusBadRequest {
		t.Fatalf("invalid create during archive status=%d body=%s", failedRes.Code, failedRes.Body.String())
	}
	afterCreate, err := streams.GetStream(t.Context(), archiving.ID)
	if err != nil {
		t.Fatal(err)
	}
	if afterCreate.ArchiveRunID != archiving.ArchiveRunID || afterCreate.ArchiveStartedAt == nil || !afterCreate.ArchiveStartedAt.Equal(*archiving.ArchiveStartedAt) || afterCreate.ArchiveReportedAt != nil {
		t.Errorf("archive run changed during create:\nbefore=%#v\nafter=%#v", archiving, afterCreate)
	}
	service, err := auth.GetService(t.Context(), "encoder-archive")
	if err != nil {
		t.Fatal(err)
	}
	if service.CurrentStreamID != archiving.ID {
		t.Errorf("archive report target changed during create: got=%q want=%q", service.CurrentStreamID, archiving.ID)
	}

	reportBody := `{"service_id":"encoder-archive","stream_id":"` + archiving.ID + `","archive_run_id":"run-01","archive_started_at":"` + archiveStartedAt.Format(time.RFC3339) + `","artifacts":[{"kind":"archive","name":"final.mp4","relative_path":"final/` + archiving.ID + `/run-01/final.mp4","size_bytes":123}]}`
	reportReq := httptest.NewRequest(http.MethodPost, "/services/stream-artifacts", strings.NewReader(reportBody))
	reportReq.Header.Set("Authorization", "Bearer "+encoderToken.RawToken)
	reportRes := httptest.NewRecorder()
	handler.ServeHTTP(reportRes, reportReq)
	if reportRes.Code != http.StatusAccepted {
		t.Errorf("archive artifact report after create status=%d body=%s", reportRes.Code, reportRes.Body.String())
	} else {
		artifacts, listErr := streams.ListStreamArtifacts(t.Context(), archiving.ID)
		if listErr != nil {
			t.Fatal(listErr)
		}
		if len(artifacts) != 1 || artifacts[0].StreamID != archiving.ID || artifacts[0].ArchiveRunID != "run-01" || artifacts[0].RelativePath != "final/"+archiving.ID+"/run-01/final.mp4" {
			t.Errorf("archive artifact target changed: %#v", artifacts)
		}
	}
	if dispatcher.startCalls != 0 || dispatcher.stopCalls != 0 || dispatcher.retryCalls != 0 {
		t.Errorf("stream create emitted lifecycle calls during archive: start=%d stop=%d retry=%d", dispatcher.startCalls, dispatcher.stopCalls, dispatcher.retryCalls)
	}
	assertNoStreamIsolationAssignmentWrites(t, registry)
}

func TestCreateStreamPreservesStartingLiveAndStoppingRuntimeOwnership(t *testing.T) {
	for _, status := range []string{"starting", "live", "stopping"} {
		t.Run(status, func(t *testing.T) {
			auth := store.NewMemoryAuthStore()
			if err := auth.AddUser(store.User{Username: "operator", Roles: []string{"stream_operator"}}, "correct horse battery", []string{"streams.create", "services.assign", "workers.assign"}); err != nil {
				t.Fatal(err)
			}
			streams := store.NewMemoryStreamStore()
			active, err := streams.CreateStream(t.Context(), status+"-stream")
			if err != nil {
				t.Fatal(err)
			}
			active, err = streams.UpdateStreamStatus(t.Context(), active.ID, status)
			if err != nil {
				t.Fatal(err)
			}
			registerStreamIsolationService(t, auth, "encoder-"+status, "encoder_recorder", []string{"service.register"})
			registerStreamIsolationService(t, auth, "worker-"+status, "worker", []string{"service.register"})
			if _, err := auth.AssignServiceToStream(t.Context(), "encoder-"+status, active.ID, "bootstrap"); err != nil {
				t.Fatal(err)
			}
			if _, err := auth.AssignServiceToStream(t.Context(), "worker-"+status, active.ID, "bootstrap"); err != nil {
				t.Fatal(err)
			}
			beforeEncoder, err := auth.GetService(t.Context(), "encoder-"+status)
			if err != nil {
				t.Fatal(err)
			}
			beforeWorker, err := auth.GetService(t.Context(), "worker-"+status)
			if err != nil {
				t.Fatal(err)
			}
			dispatcher := &fakeServiceDispatcher{}
			registry := &streamIsolationServiceRegistry{ServiceRegistryStore: auth}
			handler := NewServer(streams, WithAuthStore(auth), WithServiceRegistryStore(registry), WithServiceDispatcher(dispatcher))
			cookie, csrf := loginForTest(t, handler, "operator", "correct horse battery")

			created := createStreamForIsolation(t, handler, cookie, csrf, `{"name":"new-during-`+status+`"}`)
			afterActive, err := streams.GetStream(t.Context(), active.ID)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(afterActive, active) {
				t.Errorf("%s stream changed during unrelated create:\nbefore=%#v\nafter=%#v", status, active, afterActive)
			}
			afterEncoder, err := auth.GetService(t.Context(), "encoder-"+status)
			if err != nil {
				t.Fatal(err)
			}
			afterWorker, err := auth.GetService(t.Context(), "worker-"+status)
			if err != nil {
				t.Fatal(err)
			}
			assertStreamIsolationServiceUnchanged(t, status+" encoder", beforeEncoder, afterEncoder)
			assertStreamIsolationServiceUnchanged(t, status+" worker", beforeWorker, afterWorker)
			activeAssignments, err := auth.ListStreamAssignments(t.Context(), active.ID)
			if err != nil {
				t.Fatal(err)
			}
			createdAssignments, err := auth.ListStreamAssignments(t.Context(), created.ID)
			if err != nil {
				t.Fatal(err)
			}
			if len(activeAssignments) != 2 || len(createdAssignments) != 0 {
				t.Errorf("%s assignment ownership changed: active=%s created=%s", status, formatSafeHTTPSensitiveDiagnostic(activeAssignments), formatSafeHTTPSensitiveDiagnostic(createdAssignments))
			}
			if dispatcher.startCalls != 0 || dispatcher.stopCalls != 0 || dispatcher.retryCalls != 0 {
				t.Errorf("%s create emitted lifecycle calls: start=%d stop=%d retry=%d", status, dispatcher.startCalls, dispatcher.stopCalls, dispatcher.retryCalls)
			}
			assertNoStreamIsolationAssignmentWrites(t, registry)
		})
	}
}

func TestCreateStreamRepeatedAndConcurrentRequestsPreserveAssignments(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "operator", Roles: []string{"stream_operator"}}, "correct horse battery", []string{"streams.create", "services.assign", "workers.assign"}); err != nil {
		t.Fatal(err)
	}
	streams := store.NewMemoryStreamStore()
	active, err := streams.CreateStream(t.Context(), "active-stream")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := streams.UpdateStreamStatus(t.Context(), active.ID, "live"); err != nil {
		t.Fatal(err)
	}
	registerStreamIsolationService(t, auth, "encoder-concurrent", "encoder_recorder", []string{"service.register"})
	registerStreamIsolationService(t, auth, "worker-concurrent", "worker", []string{"service.register"})
	if _, err := auth.AssignServiceToStream(t.Context(), "encoder-concurrent", active.ID, "bootstrap"); err != nil {
		t.Fatal(err)
	}
	if _, err := auth.AssignServiceToStream(t.Context(), "worker-concurrent", active.ID, "bootstrap"); err != nil {
		t.Fatal(err)
	}
	registry := &streamIsolationServiceRegistry{ServiceRegistryStore: auth}
	handler := NewServer(streams, WithAuthStore(auth), WithServiceRegistryStore(registry))
	cookie, csrf := loginForTest(t, handler, "operator", "correct horse battery")

	const requestCount = 8
	responses := make(chan *httptest.ResponseRecorder, requestCount)
	var wg sync.WaitGroup
	for index := 0; index < requestCount; index++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			body := `{"name":"concurrent-` + string(rune('a'+index)) + `","visual_settings":{"expected_revision":0,"discord_target":{"mode":"inherit"}}}`
			req := httptest.NewRequest(http.MethodPost, "/streams", strings.NewReader(body))
			req.AddCookie(cookie)
			req.Header.Set("X-CSRF-Token", csrf)
			res := httptest.NewRecorder()
			handler.ServeHTTP(res, req)
			responses <- res
		}(index)
	}
	wg.Wait()
	close(responses)
	for res := range responses {
		if res.Code != http.StatusCreated {
			t.Fatalf("concurrent create status=%d body=%s", res.Code, res.Body.String())
		}
		var created store.Stream
		if err := json.NewDecoder(res.Body).Decode(&created); err != nil {
			t.Fatal(err)
		}
		assignments, err := auth.ListStreamAssignments(t.Context(), created.ID)
		if err != nil {
			t.Fatal(err)
		}
		if created.AssignedEncoderID != "" || created.AssignedWorkerID != "" || len(assignments) != 0 {
			t.Errorf("concurrent create wrote runtime assignment: stream=%#v assignments=%s", created, formatSafeHTTPSensitiveDiagnostic(assignments))
		}
	}
	activeAssignments, err := auth.ListStreamAssignments(t.Context(), active.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(activeAssignments) != 2 || assignedServiceID(activeAssignments, "encoder_recorder") != "encoder-concurrent" || assignedServiceID(activeAssignments, "worker") != "worker-concurrent" {
		t.Errorf("concurrent creates changed active assignments: %s", formatSafeHTTPSensitiveDiagnostic(activeAssignments))
	}
	assertNoStreamIsolationAssignmentWrites(t, registry)
}

type streamIsolationServiceRegistry struct {
	store.ServiceRegistryStore
	assignCalls          atomic.Int64
	assignWithRoleCalls  atomic.Int64
	unassignCalls        atomic.Int64
	guardedAssignCalls   atomic.Int64
	guardedUnassignCalls atomic.Int64
}

func (s *streamIsolationServiceRegistry) AssignServiceToStream(ctx context.Context, serviceID, streamID, actorUserID string) (store.RegisteredService, error) {
	s.assignCalls.Add(1)
	return s.ServiceRegistryStore.AssignServiceToStream(ctx, serviceID, streamID, actorUserID)
}

func (s *streamIsolationServiceRegistry) AssignServiceToStreamWithRole(ctx context.Context, serviceID, streamID, actorUserID, assignmentRole string) (store.RegisteredService, error) {
	s.assignWithRoleCalls.Add(1)
	return s.ServiceRegistryStore.AssignServiceToStreamWithRole(ctx, serviceID, streamID, actorUserID, assignmentRole)
}

func (s *streamIsolationServiceRegistry) UnassignServiceFromStream(ctx context.Context, serviceID, actorUserID string) (store.RegisteredService, error) {
	s.unassignCalls.Add(1)
	return s.ServiceRegistryStore.UnassignServiceFromStream(ctx, serviceID, actorUserID)
}

func (s *streamIsolationServiceRegistry) AssignServiceToStreamGuarded(ctx context.Context, mutation store.ServiceAssignmentMutation) (store.RegisteredService, error) {
	s.guardedAssignCalls.Add(1)
	return s.ServiceRegistryStore.AssignServiceToStreamGuarded(ctx, mutation)
}

func (s *streamIsolationServiceRegistry) UnassignServiceFromStreamGuarded(ctx context.Context, mutation store.ServiceUnassignmentMutation) (store.RegisteredService, error) {
	s.guardedUnassignCalls.Add(1)
	return s.ServiceRegistryStore.UnassignServiceFromStreamGuarded(ctx, mutation)
}

func assertNoStreamIsolationAssignmentWrites(t *testing.T, registry *streamIsolationServiceRegistry) {
	t.Helper()
	if assign := registry.assignCalls.Load(); assign != 0 {
		t.Errorf("stream create called AssignServiceToStream %d times", assign)
	}
	if assignWithRole := registry.assignWithRoleCalls.Load(); assignWithRole != 0 {
		t.Errorf("stream create called AssignServiceToStreamWithRole %d times", assignWithRole)
	}
	if unassign := registry.unassignCalls.Load(); unassign != 0 {
		t.Errorf("stream create called UnassignServiceFromStream %d times", unassign)
	}
	if assign := registry.guardedAssignCalls.Load(); assign != 0 {
		t.Errorf("stream create called AssignServiceToStreamGuarded %d times", assign)
	}
	if unassign := registry.guardedUnassignCalls.Load(); unassign != 0 {
		t.Errorf("stream create called UnassignServiceFromStreamGuarded %d times", unassign)
	}
}

func registerStreamIsolationService(t *testing.T, auth *store.MemoryAuthStore, serviceID, serviceType string, scopes []string) store.ServiceToken {
	t.Helper()
	token, err := auth.CreateServiceToken(t.Context(), serviceType, scopes)
	if err != nil {
		t.Fatal(err)
	}
	registerServiceWithTokenForTest(t, auth, token, store.ServiceRegistration{
		ServiceID:   serviceID,
		ServiceType: serviceType,
		ServiceName: serviceID,
		PublicURL:   "https://" + serviceID + ".example.com",
		Version:     "test",
		Capabilities: map[string]any{
			"runtime_config": true,
		},
	})
	return token
}

func createStreamForIsolation(t *testing.T, handler http.Handler, cookie *http.Cookie, csrf, payload string) store.Stream {
	t.Helper()
	var body map[string]any
	if err := json.Unmarshal([]byte(payload), &body); err != nil {
		t.Fatal(err)
	}
	if _, ok := body["visual_settings"]; !ok {
		body["visual_settings"] = map[string]any{
			"expected_revision": 0,
			"discord_target": map[string]any{
				"mode": "inherit",
			},
		}
	}
	encoded, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/streams", bytes.NewReader(encoded))
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrf)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusCreated {
		t.Fatalf("create stream status=%d body=%s", res.Code, res.Body.String())
	}
	var stream store.Stream
	if err := json.NewDecoder(res.Body).Decode(&stream); err != nil {
		t.Fatal(err)
	}
	return stream
}

func streamIsolationRuntimeConfig(t *testing.T, handler http.Handler, token store.ServiceToken, serviceID string) serviceRuntimeConfigResponse {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/services/runtime-config?service_id="+serviceID, nil)
	req.Header.Set("Authorization", "Bearer "+token.RawToken)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("runtime config status=%d body=%s", res.Code, res.Body.String())
	}
	var response serviceRuntimeConfigResponse
	if err := json.NewDecoder(res.Body).Decode(&response); err != nil {
		t.Fatal(err)
	}
	return response
}

func streamIsolationPreview(t *testing.T, handler http.Handler, cookie *http.Cookie, streamID string, wantStatus int) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/streams/"+streamID+"/preview/index.m3u8", nil)
	req.AddCookie(cookie)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != wantStatus {
		t.Errorf("preview status=%d want=%d body=%s", res.Code, wantStatus, res.Body.String())
	}
}

func streamIsolationPreviewLink(t *testing.T, handler http.Handler, cookie *http.Cookie, csrf, streamID string, wantStatus int) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/streams/"+streamID+"/preview-links", nil)
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrf)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != wantStatus {
		t.Errorf("preview link status=%d want=%d body=%s", res.Code, wantStatus, res.Body.String())
	}
}

func assertStreamIsolationServiceUnchanged(t *testing.T, label string, before, after store.RegisteredService) {
	t.Helper()
	if !reflect.DeepEqual(before, after) {
		t.Errorf("%s service runtime changed during create: before=%s after=%s", label, formatSafeHTTPSensitiveDiagnostic(before), formatSafeHTTPSensitiveDiagnostic(after))
	}
}

func assignedServiceID(assignments []store.RegisteredService, serviceType string) string {
	for _, assignment := range assignments {
		if assignment.ServiceType == serviceType && normalizeAssignmentRole(assignment.AssignmentRole) == "primary" {
			return assignment.ServiceID
		}
	}
	return ""
}
