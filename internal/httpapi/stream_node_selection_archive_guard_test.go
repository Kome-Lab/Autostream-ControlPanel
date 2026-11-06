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

func TestStreamSettingsCannotStealPendingArchiveAndReportStillSucceeds(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "operator"}, "correct horse battery", []string{"streams.update", "services.assign", "services.unassign"}); err != nil {
		t.Fatal(err)
	}
	streams := store.NewMemoryStreamStore()
	owner, err := streams.CreateStream(t.Context(), "archive owner")
	if err != nil {
		t.Fatal(err)
	}
	owner, err = streams.UpdateStreamSettings(t.Context(), owner.ID, store.StreamSettings{Name: owner.Name, ArchiveProfileID: "archive-profile", ArchiveFileName: "recording.mp4"})
	if err != nil {
		t.Fatal(err)
	}
	target, err := streams.CreateStream(t.Context(), "idle target")
	if err != nil {
		t.Fatal(err)
	}
	token := registerStreamIsolationService(t, auth, "encoder-archive-owner", "encoder_recorder", []string{"service.register", "encoder.status.write"})
	if _, err := auth.AssignServiceToStream(t.Context(), "encoder-archive-owner", owner.ID, "bootstrap"); err != nil {
		t.Fatal(err)
	}
	startedAt := time.Date(2026, 8, 23, 1, 30, 0, 123456789, time.UTC)
	owner, err = streams.PrepareStreamArchiveRun(t.Context(), owner.ID, "archive-run-owner", startedAt)
	if err != nil {
		t.Fatal(err)
	}
	owner, err = streams.UpdateStreamStatus(t.Context(), owner.ID, "completed")
	if err != nil {
		t.Fatal(err)
	}
	dispatcher := &fakeServiceDispatcher{}
	handler := NewServer(streams, WithAuthStore(auth), WithAuditStore(auth), WithServiceRegistryStore(auth), WithServiceDispatcher(dispatcher))
	cookie, csrf := loginForTest(t, handler, "operator", "correct horse battery")
	beforeService, err := auth.GetService(t.Context(), "encoder-archive-owner")
	if err != nil {
		t.Fatal(err)
	}
	beforeAssignmentIDs := streamSettingsGuardAssignmentIDsForTest(t, auth)
	beforeAssignments, err := auth.ListStreamAssignments(t.Context(), owner.ID)
	if err != nil {
		t.Fatal(err)
	}
	assertRuntimeOwner := func() {
		t.Helper()
		service, err := auth.GetService(t.Context(), "encoder-archive-owner")
		if err != nil || !reflect.DeepEqual(service, beforeService) {
			t.Fatalf("saving archive preferences changed the reporting Encoder: %v", err)
		}
		assignments, err := auth.ListStreamAssignments(t.Context(), owner.ID)
		if err != nil || !reflect.DeepEqual(assignments, beforeAssignments) ||
			!reflect.DeepEqual(streamSettingsGuardAssignmentIDsForTest(t, auth), beforeAssignmentIDs) {
			t.Fatalf("saving archive preferences changed execution assignment identity: %v", err)
		}
		targetAssignments, err := auth.ListStreamAssignments(t.Context(), target.ID)
		if err != nil || len(targetAssignments) != 0 {
			t.Fatalf("saving archive preferences acquired target execution ownership: %v", err)
		}
	}

	reassignReq := httptest.NewRequest(http.MethodPut, "/streams/"+target.ID+"/settings", bytes.NewBufferString(`{"name":"target","encoder_service_id":"encoder-archive-owner"}`))
	reassignReq.AddCookie(cookie)
	reassignReq.Header.Set("X-CSRF-Token", csrf)
	reassignRes := httptest.NewRecorder()
	handler.ServeHTTP(reassignRes, reassignReq)
	if reassignRes.Code != http.StatusOK {
		t.Fatalf("save shared archive Encoder choice status=%d body=%s", reassignRes.Code, reassignRes.Body.String())
	}
	savedTarget, err := streams.GetStream(t.Context(), target.ID)
	if err != nil || savedTarget.Name != "target" || !savedTarget.NodeSelectionsConfigured ||
		savedTarget.SelectedEncoderID != "encoder-archive-owner" {
		t.Fatalf("archive Encoder preference was not saved: stream=%#v err=%v", savedTarget, err)
	}
	assertRuntimeOwner()

	clearReq := httptest.NewRequest(http.MethodPut, "/streams/"+owner.ID+"/settings", bytes.NewBufferString(`{"name":"archive owner","encoder_service_id":""}`))
	clearReq.AddCookie(cookie)
	clearReq.Header.Set("X-CSRF-Token", csrf)
	clearRes := httptest.NewRecorder()
	handler.ServeHTTP(clearRes, clearReq)
	if clearRes.Code != http.StatusOK {
		t.Fatalf("clear saved archive Encoder choice status=%d body=%s", clearRes.Code, clearRes.Body.String())
	}
	clearedOwner, err := streams.GetStream(t.Context(), owner.ID)
	if err != nil || !clearedOwner.NodeSelectionsConfigured || clearedOwner.SelectedEncoderID != "" ||
		clearedOwner.Status != owner.Status || clearedOwner.ArchiveRunID != owner.ArchiveRunID ||
		clearedOwner.ArchiveStartedAt == nil || !clearedOwner.ArchiveStartedAt.Equal(*owner.ArchiveStartedAt) ||
		clearedOwner.ArchiveReportedAt != nil || clearedOwner.ArchiveFileName != owner.ArchiveFileName {
		t.Fatalf("clearing preferences changed pending archive authority: stream=%#v err=%v", clearedOwner, err)
	}
	processing, err := streams.ListArchiveProcessingStreams(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	foundProcessing := false
	for _, stream := range processing {
		if stream.ID == owner.ID {
			foundProcessing = true
		}
	}
	if !foundProcessing {
		t.Fatal("clearing a saved Encoder preference removed its pending archive")
	}
	unassignReq := httptest.NewRequest(http.MethodDelete, "/services/encoder-archive-owner/assignment", nil)
	unassignReq.AddCookie(cookie)
	unassignReq.Header.Set("X-CSRF-Token", csrf)
	unassignRes := httptest.NewRecorder()
	handler.ServeHTTP(unassignRes, unassignReq)
	if unassignRes.Code != http.StatusConflict || !strings.Contains(unassignRes.Body.String(), `"code":"service_unassign_protected_stream"`) {
		t.Fatalf("actual archive unassign status=%d body=%s", unassignRes.Code, unassignRes.Body.String())
	}
	assertRuntimeOwner()

	relativePath := "final/" + owner.ID + "/" + owner.ArchiveRunID + "/final.mp4"
	reportBody, err := json.Marshal(map[string]any{
		"service_id":         "encoder-archive-owner",
		"stream_id":          owner.ID,
		"archive_run_id":     owner.ArchiveRunID,
		"archive_started_at": owner.ArchiveStartedAt,
		"artifacts":          []map[string]any{{"kind": "archive", "name": "final.mp4", "relative_path": relativePath, "size_bytes": 321}},
	})
	if err != nil {
		t.Fatal(err)
	}
	reportReq := httptest.NewRequest(http.MethodPost, "/services/stream-artifacts", bytes.NewReader(reportBody))
	reportReq.Header.Set("Authorization", "Bearer "+token.RawToken)
	reportRes := httptest.NewRecorder()
	handler.ServeHTTP(reportRes, reportReq)
	if reportRes.Code != http.StatusAccepted {
		t.Fatalf("artifact report after shared/cleared preferences status=%d body=%s", reportRes.Code, reportRes.Body.String())
	}
	afterReport, err := streams.GetStream(t.Context(), owner.ID)
	if err != nil {
		t.Fatal(err)
	}
	if afterReport.ArchiveRunID != owner.ArchiveRunID || afterReport.ArchiveStartedAt == nil || !afterReport.ArchiveStartedAt.Equal(*owner.ArchiveStartedAt) || afterReport.ArchiveReportedAt == nil || afterReport.ArchiveFileName != owner.ArchiveFileName {
		t.Fatalf("archive identity changed after preference edits/report: before=%#v after=%#v", owner, afterReport)
	}
	artifacts, err := streams.ListStreamArtifacts(t.Context(), owner.ID)
	if err != nil || len(artifacts) != 1 || artifacts[0].RelativePath != relativePath ||
		artifacts[0].ArchiveRunID != owner.ArchiveRunID || artifacts[0].ArchiveStartedAt == nil ||
		!artifacts[0].ArchiveStartedAt.Equal(*owner.ArchiveStartedAt) || artifacts[0].SourceServiceID != "encoder-archive-owner" {
		t.Fatalf("artifact report result=%#v err=%v", artifacts, err)
	}

	assertRuntimeOwner()
	processing, err = streams.ListArchiveProcessingStreams(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, stream := range processing {
		if stream.ID == owner.ID {
			t.Fatal("authoritative artifact report did not close archive processing")
		}
	}
	// Saving the same preference after reporting still does not transfer a
	// runtime owner. Explicit runtime reassignment remains a separate operation.
	saveAfterReport := httptest.NewRequest(http.MethodPut, "/streams/"+target.ID+"/settings", bytes.NewBufferString(`{"name":"target","encoder_service_id":"encoder-archive-owner"}`))
	saveAfterReport.AddCookie(cookie)
	saveAfterReport.Header.Set("X-CSRF-Token", csrf)
	saveAfterReportRes := httptest.NewRecorder()
	handler.ServeHTTP(saveAfterReportRes, saveAfterReport)
	if saveAfterReportRes.Code != http.StatusOK {
		t.Fatalf("save shared choice after report status=%d body=%s", saveAfterReportRes.Code, saveAfterReportRes.Body.String())
	}
	assertRuntimeOwner()
	reassignAfterReport := httptest.NewRequest(http.MethodPost, "/services/encoder-archive-owner/assign", bytes.NewBufferString(`{"stream_id":"`+target.ID+`","assignment_role":"primary"}`))
	reassignAfterReport.AddCookie(cookie)
	reassignAfterReport.Header.Set("X-CSRF-Token", csrf)
	reassignAfterReportRes := httptest.NewRecorder()
	handler.ServeHTTP(reassignAfterReportRes, reassignAfterReport)
	if reassignAfterReportRes.Code != http.StatusOK {
		t.Fatalf("idle reassign after report status=%d body=%s", reassignAfterReportRes.Code, reassignAfterReportRes.Body.String())
	}
	service, err := auth.GetService(t.Context(), "encoder-archive-owner")
	if err != nil || service.CurrentStreamID != target.ID {
		t.Fatalf("idle reassign service=%s err=%v", formatSafeHTTPSensitiveDiagnostic(service), err)
	}
	oldAssignments, err := auth.ListStreamAssignments(t.Context(), owner.ID)
	if err != nil || len(oldAssignments) != 0 {
		t.Fatalf("old owner assignments=%s err=%v", formatSafeHTTPSensitiveDiagnostic(oldAssignments), err)
	}
	if dispatcher.startCalls != 0 || dispatcher.stopCalls != 0 || dispatcher.retryCalls != 0 || dispatcher.encoderPreflightCalls != 0 {
		t.Fatal("archive preference edits dispatched downstream")
	}
}
