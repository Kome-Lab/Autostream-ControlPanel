package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/example/autostream-control-panel/internal/store"
)

func TestUI188R9HTTPProfilelessArchiveFinalizationBarrier(t *testing.T) {
	auth, streams := store.NewMemoryAuthStore(), store.NewMemoryStreamStore()
	if err := auth.AddUser(store.User{Username: "r9-operator", Roles: []string{"super_admin"}}, "r9 test passphrase", []string{"*"}); err != nil {
		t.Fatal(err)
	}
	handler := NewServer(streams, WithAuthStore(auth), WithAuditStore(auth), WithServiceRegistryStore(auth))
	cookie, csrf := loginForTest(t, handler, "r9-operator", "r9 test passphrase")
	owner, err := streams.CreateStream(t.Context(), "R9 archive barrier")
	if err != nil {
		t.Fatal(err)
	}
	other, err := streams.CreateStream(t.Context(), "R9 retained other")
	if err != nil {
		t.Fatal(err)
	}
	token := createBoundServiceTokenForTest(t, handler, cookie, csrf, "encoder_recorder", "encoder-r9", []string{"service.register", "encoder.status.write"})
	registerServiceForTest(t, handler, token.RawToken, "encoder-r9", "encoder_recorder")
	request := func(method, path, body, bearer string, authenticated, validCSRF bool) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		if bearer != "" {
			req.Header.Set("Authorization", "Bearer "+bearer)
		}
		if authenticated {
			req.AddCookie(cookie)
		}
		if validCSRF {
			req.Header.Set("X-CSRF-Token", csrf)
		}
		res := httptest.NewRecorder()
		handler.ServeHTTP(res, req)
		return res
	}
	assign := request(http.MethodPost, "/services/encoder-r9/assign", `{"stream_id":"`+owner.ID+`"}`, "", true, true)
	if assign.Code != http.StatusOK {
		t.Fatalf("assign status=%d", assign.Code)
	}
	started := time.Date(2026, 10, 15, 14, 0, 0, 0, time.UTC)
	owner, err = streams.PrepareStreamArchiveRun(t.Context(), owner.ID, "r9-http-run", started)
	if err != nil {
		t.Fatal(err)
	}
	owner, err = streams.UpdateStreamStatus(t.Context(), owner.ID, "completed")
	if err != nil || owner.ArchiveProfileID != "" {
		t.Fatalf("modern no-profile fixture: %v", err)
	}
	before, err := auth.GetService(t.Context(), "encoder-r9")
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range []struct{ method, path, body, code string }{
		{http.MethodDelete, "/streams/" + owner.ID, "", "service_unassign_protected_stream"},
		{http.MethodDelete, "/services/encoder-r9/assignment", "", "service_unassign_protected_stream"},
		{http.MethodPost, "/services/encoder-r9/assign", `{"stream_id":"` + other.ID + `"}`, "service_assignment_protected_stream"},
	} {
		res := request(item.method, item.path, item.body, "", true, true)
		if res.Code != http.StatusConflict || !strings.Contains(res.Body.String(), item.code) {
			t.Fatalf("pending %s %s status=%d code=%s", item.method, item.path, res.Code, item.code)
		}
	}
	for _, authentication := range []struct {
		auth, csrf bool
		status     int
	}{{false, false, http.StatusUnauthorized}, {true, false, http.StatusForbidden}} {
		res := request(http.MethodDelete, "/streams/"+owner.ID, "", "", authentication.auth, authentication.csrf)
		if res.Code != authentication.status {
			t.Fatalf("security precedence=%d", res.Code)
		}
	}
	after, err := auth.GetService(t.Context(), "encoder-r9")
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("rejected mutation changed assignment")
	}
	pending, err := streams.ListArchiveProcessingStreams(t.Context())
	if err != nil || len(pending) != 1 || pending[0].ID != owner.ID {
		t.Fatalf("pending list=%d err=%v", len(pending), err)
	}
	artifacts := []map[string]any{}
	for _, item := range []struct{ kind, name string }{{"archive", "final.mp4"}, {"metadata", "metadata.json"}, {"logs", "logs.jsonl"}} {
		artifacts = append(artifacts, map[string]any{"kind": item.kind, "name": item.name, "relative_path": "final/" + owner.ID + "/r9-http-run/" + item.name, "size_bytes": 123})
	}
	body, err := json.Marshal(map[string]any{"service_id": "encoder-r9", "stream_id": owner.ID, "archive_run_id": owner.ArchiveRunID, "archive_started_at": started.Format(time.RFC3339Nano), "artifacts": artifacts})
	if err != nil {
		t.Fatal(err)
	}
	// Real authenticated report handler; no pre-written report marker or response stub.
	report := request(http.MethodPost, "/services/stream-artifacts", string(body), token.RawToken, false, false)
	if report.Code != http.StatusAccepted {
		t.Fatalf("normal artifact report status=%d body=%s", report.Code, report.Body.String())
	}
	stored, err := streams.ListStreamArtifacts(t.Context(), owner.ID)
	if err != nil || len(stored) != 3 {
		t.Fatalf("normal catalog count=%d err=%v", len(stored), err)
	}
	current, err := streams.GetStream(t.Context(), owner.ID)
	if err != nil || current.ArchiveReportedAt == nil || current.ArchiveRunID != owner.ArchiveRunID {
		t.Fatal("current report marker missing")
	}
	pending, err = streams.ListArchiveProcessingStreams(t.Context())
	if err != nil || len(pending) != 0 {
		t.Fatal("reported run still pending")
	}
	deleted := request(http.MethodDelete, "/streams/"+owner.ID, "", "", true, true)
	if deleted.Code != http.StatusOK || !strings.Contains(deleted.Body.String(), `"status":"deleted"`) {
		t.Fatalf("normal delete=%d body=%s", deleted.Code, deleted.Body.String())
	}
	retained, err := streams.ListStreamArtifacts(t.Context(), owner.ID)
	if err != nil || !reflect.DeepEqual(stored, retained) {
		t.Fatal("recordings changed on delete")
	}
	if _, err := streams.GetStream(t.Context(), other.ID); err != nil {
		t.Fatal("other stream changed")
	}
	late := request(http.MethodPost, "/services/stream-artifacts", string(body), token.RawToken, false, false)
	if late.Code != http.StatusForbidden {
		t.Fatalf("late report status=%d", late.Code)
	}
	if bytes.Contains(late.Body.Bytes(), []byte(token.RawToken)) {
		t.Fatal("secret exposed")
	}
}
