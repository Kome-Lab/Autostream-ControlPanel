package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/example/autostream-control-panel/internal/store"
)

func TestStreamListIncludesPrimaryAssignedNodes(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "operator", Roles: []string{"stream_operator"}}, "correct horse battery", []string{"streams.read"}); err != nil {
		t.Fatal(err)
	}
	streams := store.NewMemoryStreamStore()
	stream, err := streams.CreateStream(t.Context(), "assigned nodes")
	if err != nil {
		t.Fatal(err)
	}
	registerAssignedServices(t, auth, stream.ID, "encoder_recorder", "worker")
	handler := NewServer(streams, WithAuthStore(auth), WithServiceRegistryStore(auth))
	cookie, csrf := loginForTest(t, handler, "operator", "correct horse battery")

	req := httptest.NewRequest(http.MethodGet, "/streams", nil)
	req.AddCookie(cookie)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("list streams status = %d body = %s", res.Code, res.Body.String())
	}
	var items []store.Stream
	if err := json.Unmarshal(res.Body.Bytes(), &items); err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 {
		t.Fatalf("list streams length = %d", len(items))
	}
	if items[0].AssignedWorkerID != "worker-01" || items[0].AssignedEncoderID != "encoder_recorder-01" {
		t.Fatalf("assigned nodes = worker=%q encoder=%q", items[0].AssignedWorkerID, items[0].AssignedEncoderID)
	}

	_ = csrf
}

func TestArchiveProcessingStreamsExposeOnlyUnreportedCompletedRecordings(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "archive-operator", Roles: []string{"stream_operator"}}, "correct horse battery", []string{"archives.read"}); err != nil {
		t.Fatal(err)
	}
	streams := store.NewMemoryStreamStore()
	stream, err := streams.CreateStream(t.Context(), "archive-processing")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := streams.UpdateStreamSettings(t.Context(), stream.ID, store.StreamSettings{ArchiveProfileID: "archive-01"}); err != nil {
		t.Fatal(err)
	}
	if _, err := streams.UpdateStreamStatus(t.Context(), stream.ID, "completed"); err != nil {
		t.Fatal(err)
	}
	handler := NewServer(streams, WithAuthStore(auth), WithServiceRegistryStore(auth))
	cookie, _ := loginForTest(t, handler, "archive-operator", "correct horse battery")

	listProcessing := func() []store.Stream {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, "/archive/processing-streams", nil)
		req.AddCookie(cookie)
		res := httptest.NewRecorder()
		handler.ServeHTTP(res, req)
		if res.Code != http.StatusOK {
			t.Fatalf("archive processing stream list status = %d body = %s", res.Code, res.Body.String())
		}
		var items []store.Stream
		if err := json.Unmarshal(res.Body.Bytes(), &items); err != nil {
			t.Fatal(err)
		}
		return items
	}

	if items := listProcessing(); len(items) != 0 {
		t.Fatalf("never-started stream was treated as processing = %#v", items)
	}
	startedAt := time.Now().UTC()
	if _, err := streams.PrepareStreamArchiveRun(t.Context(), stream.ID, "archive-run-01", startedAt); err != nil {
		t.Fatal(err)
	}
	if items := listProcessing(); len(items) != 1 || items[0].ID != stream.ID {
		t.Fatalf("processing streams before artifact report = %#v", items)
	}
	if err := streams.UpsertStreamArtifacts(t.Context(), stream.ID, []store.StreamArtifact{{
		ArchiveRunID: "archive-run-01", ArchiveStartedAt: &startedAt,
		Kind: "archive", Name: "final.mp4", RelativePath: "final/" + stream.ID + "/archive-run-01/final.mp4", SizeBytes: 10,
	}}); err != nil {
		t.Fatal(err)
	}
	artifacts, err := streams.ListStreamArtifacts(t.Context(), stream.ID)
	if err != nil || len(artifacts) != 1 {
		t.Fatalf("reported artifacts = %#v err=%v", artifacts, err)
	}
	if items := listProcessing(); len(items) != 0 {
		t.Fatalf("processing streams after artifact report = %#v", items)
	}
	if err := streams.DeleteStreamArtifact(t.Context(), stream.ID, artifacts[0].ID); err != nil {
		t.Fatal(err)
	}
	if items := listProcessing(); len(items) != 0 {
		t.Fatalf("deleted reported recording returned to processing = %#v", items)
	}
}

func TestStreamManagementCreateRejectsRequestedPrimaryNodes(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "operator", Roles: []string{"stream_operator"}}, "correct horse battery", []string{"streams.create", "services.assign", "workers.assign"}); err != nil {
		t.Fatal(err)
	}
	registerServiceInstance(t, auth, "encoder_recorder-01", "encoder_recorder")
	registerServiceInstance(t, auth, "worker-01", "worker")
	streams := store.NewMemoryStreamStore()
	handler := NewServer(streams, WithAuthStore(auth), WithServiceRegistryStore(auth))
	cookie, csrf := loginForTest(t, handler, "operator", "correct horse battery")

	req := httptest.NewRequest(http.MethodPost, "/streams", strings.NewReader(`{"name":"created with nodes","encoder_service_id":"encoder_recorder-01","worker_service_id":"worker-01"}`))
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrf)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusBadRequest || !strings.Contains(res.Body.String(), `"code":"bad_request"`) {
		t.Fatalf("create stream status = %d body = %s", res.Code, res.Body.String())
	}
	items, err := streams.ListStreams(t.Context())
	if err != nil || len(items) != 0 {
		t.Fatalf("rejected create persisted stream: streams=%#v err=%v", items, err)
	}
}

func TestDeleteStreamReleasesAssignmentsAndRejectsActiveStream(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "operator", Roles: []string{"stream_operator"}}, "correct horse battery", []string{"streams.delete"}); err != nil {
		t.Fatal(err)
	}
	streams := store.NewMemoryStreamStore()
	stream, err := streams.CreateStream(t.Context(), "deletable")
	if err != nil {
		t.Fatal(err)
	}
	registerAssignedServices(t, auth, stream.ID, "encoder_recorder", "worker")
	handler := NewServer(streams, WithAuthStore(auth), WithServiceRegistryStore(auth))
	cookie, csrf := loginForTest(t, handler, "operator", "correct horse battery")

	deleteReq := httptest.NewRequest(http.MethodDelete, "/streams/"+stream.ID, nil)
	deleteReq.AddCookie(cookie)
	deleteReq.Header.Set("X-CSRF-Token", csrf)
	deleteRes := httptest.NewRecorder()
	handler.ServeHTTP(deleteRes, deleteReq)
	if deleteRes.Code != http.StatusOK {
		t.Fatalf("delete stream status = %d body = %s", deleteRes.Code, deleteRes.Body.String())
	}
	deleted, err := streams.GetStream(t.Context(), stream.ID)
	if err != nil {
		t.Fatalf("retained stream lookup error = %v", err)
	}
	if deleted.DeletedAt == nil || deleted.Status != "completed" {
		t.Fatalf("retained stream lifecycle = status=%q deleted_at=%v", deleted.Status, deleted.DeletedAt)
	}
	operational, err := streams.ListStreams(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(operational) != 0 {
		t.Fatalf("deleted stream remains in operational list = %#v", operational)
	}
	assignments, err := auth.ListStreamAssignments(t.Context(), stream.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(assignments) != 0 {
		t.Fatalf("assignments after delete = %d", len(assignments))
	}

	active, err := streams.CreateStream(t.Context(), "active")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := streams.UpdateStreamStatus(t.Context(), active.ID, "live"); err != nil {
		t.Fatal(err)
	}
	activeReq := httptest.NewRequest(http.MethodDelete, "/streams/"+active.ID, nil)
	activeReq.AddCookie(cookie)
	activeReq.Header.Set("X-CSRF-Token", csrf)
	activeRes := httptest.NewRecorder()
	handler.ServeHTTP(activeRes, activeReq)
	if activeRes.Code != http.StatusConflict {
		t.Fatalf("active delete status = %d body = %s", activeRes.Code, activeRes.Body.String())
	}
	if _, err := streams.GetStream(t.Context(), active.ID); err != nil {
		t.Fatalf("active stream lookup error = %v", err)
	}
}

func TestDeleteStreamRetainsArchiveCatalogAndReleasesAssignments(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "operator", Roles: []string{"stream_operator"}}, "correct horse battery", []string{"streams.delete", "archives.read", "archives.download"}); err != nil {
		t.Fatal(err)
	}
	streams := store.NewMemoryStreamStore()
	stream, err := streams.CreateStream(t.Context(), "archive-protected")
	if err != nil {
		t.Fatal(err)
	}
	registerAssignedServices(t, auth, stream.ID, "encoder_recorder", "worker")
	archiveStartedAt := time.Date(2026, 8, 25, 1, 2, 3, 0, time.UTC)
	archiving, err := streams.PrepareStreamArchiveRun(t.Context(), stream.ID, "run-delete", archiveStartedAt)
	if err != nil {
		t.Fatal(err)
	}
	if err := streams.UpsertStreamArtifacts(t.Context(), stream.ID, []store.StreamArtifact{{
		ArchiveRunID: archiving.ArchiveRunID, ArchiveStartedAt: archiving.ArchiveStartedAt,
		Kind: "archive", Name: "final.mp4", RelativePath: "final/" + stream.ID + "/run-delete/final.mp4", SizeBytes: 123,
	}}); err != nil {
		t.Fatal(err)
	}
	dispatcher := &fakeServiceDispatcher{}
	handler := NewServer(streams, WithAuthStore(auth), WithServiceRegistryStore(auth), WithServiceDispatcher(dispatcher))
	cookie, csrf := loginForTest(t, handler, "operator", "correct horse battery")

	deleteReq := httptest.NewRequest(http.MethodDelete, "/streams/"+stream.ID, nil)
	deleteReq.AddCookie(cookie)
	deleteReq.Header.Set("X-CSRF-Token", csrf)
	deleteRes := httptest.NewRecorder()
	handler.ServeHTTP(deleteRes, deleteReq)
	if deleteRes.Code != http.StatusOK {
		t.Fatalf("delete stream with artifacts status = %d body = %s", deleteRes.Code, deleteRes.Body.String())
	}
	deleted, err := streams.GetStream(t.Context(), stream.ID)
	if err != nil {
		t.Fatalf("retained stream lookup failed: %v", err)
	}
	if deleted.DeletedAt == nil || deleted.Status != "completed" {
		t.Fatalf("retained stream lifecycle = status=%q deleted_at=%v", deleted.Status, deleted.DeletedAt)
	}
	artifacts, err := streams.ListStreamArtifacts(t.Context(), stream.ID)
	if err != nil || len(artifacts) != 1 {
		t.Fatalf("archive catalog was not retained after delete: artifacts=%#v err=%v", artifacts, err)
	}
	if artifacts[0].SourceServiceID != "encoder_recorder-01" {
		t.Fatalf("archive source encoder was not retained: %#v", artifacts[0])
	}
	assignments, err := auth.ListStreamAssignments(t.Context(), stream.ID)
	if err != nil || len(assignments) != 0 {
		t.Fatalf("assignments were not released after delete: assignments=%s err=%v", formatSafeHTTPSensitiveDiagnostic(assignments), err)
	}
	archiveReq := httptest.NewRequest(http.MethodGet, "/archive/streams", nil)
	archiveReq.AddCookie(cookie)
	archiveRes := httptest.NewRecorder()
	handler.ServeHTTP(archiveRes, archiveReq)
	if archiveRes.Code != http.StatusOK {
		t.Fatalf("archive stream list status = %d body = %s", archiveRes.Code, archiveRes.Body.String())
	}
	var archiveStreams []store.Stream
	if err := json.Unmarshal(archiveRes.Body.Bytes(), &archiveStreams); err != nil {
		t.Fatal(err)
	}
	if len(archiveStreams) != 1 || archiveStreams[0].ID != stream.ID || archiveStreams[0].DeletedAt == nil {
		t.Fatalf("archive stream list = %#v", archiveStreams)
	}
	downloadReq := httptest.NewRequest(http.MethodGet, "/streams/"+stream.ID+"/artifacts/"+artifacts[0].ID+"/download", nil)
	downloadReq.AddCookie(cookie)
	downloadRes := httptest.NewRecorder()
	handler.ServeHTTP(downloadRes, downloadReq)
	if downloadRes.Code != http.StatusOK || downloadRes.Body.String() != "archive-bytes" {
		t.Fatalf("archived download status = %d body = %q", downloadRes.Code, downloadRes.Body.String())
	}
	if dispatcher.archiveDownloadCalls != 1 || dispatcher.archiveStream.ID != stream.ID || dispatcher.archiveArtifact.ID != artifacts[0].ID {
		t.Fatalf("archived download dispatch = calls=%d stream=%#v artifact=%#v", dispatcher.archiveDownloadCalls, dispatcher.archiveStream, dispatcher.archiveArtifact)
	}
}

func TestDeletedStreamLeavesArchivePickerAfterLastRecordingArtifactIsDeleted(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "operator", Roles: []string{"stream_operator"}}, "correct horse battery", []string{"streams.delete", "archives.read", "archives.delete"}); err != nil {
		t.Fatal(err)
	}
	streams := store.NewMemoryStreamStore()
	stream, err := streams.CreateStream(t.Context(), "archive-with-sidecars")
	if err != nil {
		t.Fatal(err)
	}
	registerAssignedServices(t, auth, stream.ID, "encoder_recorder")
	archiveStartedAt := time.Date(2026, 8, 26, 1, 2, 3, 0, time.UTC)
	archiving, err := streams.PrepareStreamArchiveRun(t.Context(), stream.ID, "run-sidecars", archiveStartedAt)
	if err != nil {
		t.Fatal(err)
	}
	if err := streams.UpsertStreamArtifacts(t.Context(), stream.ID, []store.StreamArtifact{
		{ArchiveRunID: archiving.ArchiveRunID, ArchiveStartedAt: archiving.ArchiveStartedAt, Kind: "archive", Name: "final.mp4", RelativePath: "final/" + stream.ID + "/run-sidecars/final.mp4", SizeBytes: 10},
		{ArchiveRunID: archiving.ArchiveRunID, ArchiveStartedAt: archiving.ArchiveStartedAt, Kind: "metadata", Name: "metadata.json", RelativePath: "final/" + stream.ID + "/run-sidecars/metadata.json", SizeBytes: 1},
	}); err != nil {
		t.Fatal(err)
	}
	handler := NewServer(streams, WithAuthStore(auth), WithServiceRegistryStore(auth), WithServiceDispatcher(&fakeServiceDispatcher{}))
	cookie, csrf := loginForTest(t, handler, "operator", "correct horse battery")

	deleteStreamReq := httptest.NewRequest(http.MethodDelete, "/streams/"+stream.ID, nil)
	deleteStreamReq.AddCookie(cookie)
	deleteStreamReq.Header.Set("X-CSRF-Token", csrf)
	deleteStreamRes := httptest.NewRecorder()
	handler.ServeHTTP(deleteStreamRes, deleteStreamReq)
	if deleteStreamRes.Code != http.StatusOK {
		t.Fatalf("delete stream status = %d body = %s", deleteStreamRes.Code, deleteStreamRes.Body.String())
	}

	artifacts, err := streams.ListStreamArtifacts(t.Context(), stream.ID)
	if err != nil {
		t.Fatal(err)
	}
	archiveArtifactID := ""
	for _, artifact := range artifacts {
		if artifact.Kind == "archive" {
			archiveArtifactID = artifact.ID
			break
		}
	}
	if archiveArtifactID == "" {
		t.Fatal("recording artifact was not retained after stream deletion")
	}
	deleteArtifactReq := httptest.NewRequest(http.MethodDelete, "/streams/"+stream.ID+"/artifacts/"+archiveArtifactID, nil)
	deleteArtifactReq.AddCookie(cookie)
	deleteArtifactReq.Header.Set("X-CSRF-Token", csrf)
	deleteArtifactRes := httptest.NewRecorder()
	handler.ServeHTTP(deleteArtifactRes, deleteArtifactReq)
	if deleteArtifactRes.Code != http.StatusOK {
		t.Fatalf("delete artifact status = %d body = %s", deleteArtifactRes.Code, deleteArtifactRes.Body.String())
	}

	archiveReq := httptest.NewRequest(http.MethodGet, "/archive/streams", nil)
	archiveReq.AddCookie(cookie)
	archiveRes := httptest.NewRecorder()
	handler.ServeHTTP(archiveRes, archiveReq)
	if archiveRes.Code != http.StatusOK {
		t.Fatalf("archive stream list status = %d body = %s", archiveRes.Code, archiveRes.Body.String())
	}
	var archiveStreams []store.Stream
	if err := json.Unmarshal(archiveRes.Body.Bytes(), &archiveStreams); err != nil {
		t.Fatal(err)
	}
	if len(archiveStreams) != 0 {
		t.Fatalf("archive picker retained deleted stream without a recording: %#v", archiveStreams)
	}
	artifacts, err = streams.ListStreamArtifacts(t.Context(), stream.ID)
	if err != nil || len(artifacts) != 1 || artifacts[0].Kind != "metadata" {
		t.Fatalf("sidecar retention after recording deletion = %#v err=%v", artifacts, err)
	}
}

func TestForceStopRearmsDiscordVoiceStream(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "operator"}, "correct horse battery", []string{"streams.stop"}); err != nil {
		t.Fatal(err)
	}
	streams := store.NewMemoryStreamStore()
	stream, err := streams.CreateStream(t.Context(), "Dev_1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := streams.UpdateStreamSettings(t.Context(), stream.ID, store.StreamSettings{
		DiscordConfigID: "discord-config-01", AutoStartTrigger: "discord_voice_join", YouTubeOutputID: "youtube-output-01",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := streams.UpdateStreamStatus(t.Context(), stream.ID, "live"); err != nil {
		t.Fatal(err)
	}
	registerAssignedServices(t, auth, stream.ID, "encoder_recorder", "worker", "discord_bot")
	handler := NewServer(streams, WithAuthStore(auth), WithAuditStore(auth), WithServiceRegistryStore(auth), WithServiceDispatcher(&fakeServiceDispatcher{}))
	cookie, csrf := loginForTest(t, handler, "operator", "correct horse battery")
	req := httptest.NewRequest(http.MethodPost, "/streams/"+stream.ID+"/force-stop", nil)
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrf)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("force stop status = %d body = %s", res.Code, res.Body.String())
	}
	updated, err := streams.GetStream(t.Context(), stream.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Status != "ready" {
		t.Fatalf("force stop status = %q, want ready after acknowledged downstream stop", updated.Status)
	}
	items, err := streams.ListStreams(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	var waiting store.Stream
	for _, item := range items {
		if item.ID == stream.ID && item.Status == "ready" && item.AutoStartTrigger == "discord_voice_join" {
			waiting = item
		}
	}
	if waiting.ID == "" || len(items) != 1 || items[0].ID != stream.ID || items[0].Status != "ready" {
		t.Fatalf("same stream was not rearmed: %#v", items)
	}
	assignments, err := auth.ListStreamAssignments(t.Context(), stream.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(assignments) != 3 {
		t.Fatalf("rearmed stream assignments = %d, want 3", len(assignments))
	}
	// Same-row rearm is fully asserted above.
}
