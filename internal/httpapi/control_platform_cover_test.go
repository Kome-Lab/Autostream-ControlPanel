package httpapi

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/example/autostream-control-panel/internal/servicecall"
	"github.com/example/autostream-control-panel/internal/store"
	"github.com/example/autostream-control-panel/internal/streamvisual"
	"github.com/example/autostream-control-panel/internal/videocover"
)

type countingVideoCoverDispatcher struct {
	calls    int
	result   servicecall.VideoCoverDispatchResult
	requests []servicecall.VideoCoverDispatchRequest
}

func (d *countingVideoCoverDispatcher) DispatchVideoCover(_ context.Context, _ store.RegisteredService, request servicecall.VideoCoverDispatchRequest) servicecall.VideoCoverDispatchResult {
	d.calls++
	d.requests = append(d.requests, request)
	return d.result
}

func TestVideoCoverHTTPPermissionCapabilityIdempotencyAndAmbiguousRequestCounts(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{ID: "cover-allowed", Username: "cover-allowed"}, "correct horse battery", []string{"streams.read", "streams.show_cover", "streams.hide_cover"}); err != nil {
		t.Fatal(err)
	}
	if err := auth.AddUser(store.User{ID: "cover-denied", Username: "cover-denied"}, "correct horse battery", []string{"streams.read"}); err != nil {
		t.Fatal(err)
	}
	streams := store.NewMemoryStreamStore()
	stream, err := streams.CreateStream(t.Context(), "Cover stream")
	if err != nil {
		t.Fatal(err)
	}
	token := createRegisteredAssignedService(t, auth, "encoder-cover", "encoder_recorder", stream.ID)
	covers := videocover.NewMemoryRepository()
	if _, err = covers.EnsureGeneration(t.Context(), stream.ID, 1, "variant-cover", false); err != nil {
		t.Fatal(err)
	}
	dispatcher := &countingVideoCoverDispatcher{result: servicecall.VideoCoverDispatchResult{Applied: true}}
	visual := &fixedVisualRepository{
		settings: streamvisual.Settings{StreamID: stream.ID, BackgroundMode: "default", HeaderTitleMode: "default", CoverSource: "upload", CoverAssetID: "asset-cover", CoverVariantID: "variant-cover", Revision: 1},
		assets:   streamvisual.AssetReadiness{CoverVariantReady: true, MediaAssetIntegrity: true},
	}
	handler := NewServer(streams, WithAuthStore(auth), WithServiceRegistryStore(auth), WithStreamVisualRepository(visual), WithVideoCoverRepository(covers), WithVideoCoverDispatcher(dispatcher))
	allowedCookie, allowedCSRF := loginForTest(t, handler, "cover-allowed", "correct horse battery")
	deniedCookie, deniedCSRF := loginForTest(t, handler, "cover-denied", "correct horse battery")
	showBody := `{"active":true,"expected_job_generation":1,"expected_revision":1,"idempotency_key":"show-1"}`
	denied := serveUserJSON(t, handler, http.MethodPut, "/streams/"+stream.ID+"/video-cover-state", showBody, deniedCookie, deniedCSRF)
	if denied.Code != http.StatusForbidden || dispatcher.calls != 0 {
		t.Fatalf("denied status=%d calls=%d body=%s", denied.Code, dispatcher.calls, denied.Body.String())
	}
	unsupported := serveUserJSON(t, handler, http.MethodPut, "/streams/"+stream.ID+"/video-cover-state", showBody, allowedCookie, allowedCSRF)
	if unsupported.Code != http.StatusConflict || !strings.Contains(unsupported.Body.String(), "video_cover_capability_unavailable") || dispatcher.calls != 0 {
		t.Fatalf("unsupported status=%d calls=%d body=%s", unsupported.Code, dispatcher.calls, unsupported.Body.String())
	}
	unchanged, err := covers.GetCurrentState(t.Context(), stream.ID)
	if err != nil || unchanged.DesiredRevision != 1 {
		t.Fatalf("capability absence mutated state=%#v err=%v", unchanged, err)
	}
	if _, err = auth.Heartbeat(t.Context(), token, store.ServiceHeartbeat{ServiceID: "encoder-cover", Status: "healthy", CurrentStreamID: stream.ID, Capabilities: map[string]any{"live_video_cover_v1": true}}); err != nil {
		t.Fatal(err)
	}
	visual.assets = streamvisual.AssetReadiness{}
	unready := serveUserJSON(t, handler, http.MethodPut, "/streams/"+stream.ID+"/video-cover-state", showBody, allowedCookie, allowedCSRF)
	if unready.Code != http.StatusConflict || !strings.Contains(unready.Body.String(), "video_cover_variant_unavailable") || dispatcher.calls != 0 {
		t.Fatalf("unready variant status=%d calls=%d body=%s", unready.Code, dispatcher.calls, unready.Body.String())
	}
	visual.assets = streamvisual.AssetReadiness{CoverVariantReady: true}
	tampered := serveUserJSON(t, handler, http.MethodPut, "/streams/"+stream.ID+"/video-cover-state", showBody, allowedCookie, allowedCSRF)
	if tampered.Code != http.StatusConflict || !strings.Contains(tampered.Body.String(), "media_asset_integrity") || dispatcher.calls != 0 {
		t.Fatalf("tampered variant status=%d calls=%d body=%s", tampered.Code, dispatcher.calls, tampered.Body.String())
	}
	visual.assets = streamvisual.AssetReadiness{CoverVariantReady: true, MediaAssetIntegrity: true}
	shown := serveUserJSON(t, handler, http.MethodPut, "/streams/"+stream.ID+"/video-cover-state", showBody, allowedCookie, allowedCSRF)
	if shown.Code != http.StatusOK || dispatcher.calls != 1 {
		t.Fatalf("shown status=%d calls=%d body=%s", shown.Code, dispatcher.calls, shown.Body.String())
	}
	// A persisted action remains an exact idempotent replay even when mutable
	// asset health changes after the original dispatch.
	visual.assets = streamvisual.AssetReadiness{CoverVariantReady: true}
	duplicate := serveUserJSON(t, handler, http.MethodPut, "/streams/"+stream.ID+"/video-cover-state", showBody, allowedCookie, allowedCSRF)
	if duplicate.Code != http.StatusOK || dispatcher.calls != 1 {
		t.Fatalf("duplicate status=%d calls=%d body=%s", duplicate.Code, dispatcher.calls, duplicate.Body.String())
	}
	conflictingReplay := serveUserJSON(t, handler, http.MethodPut, "/streams/"+stream.ID+"/video-cover-state", `{"active":false,"expected_job_generation":1,"expected_revision":1,"idempotency_key":"show-1","hide_confirmed":true}`, allowedCookie, allowedCSRF)
	if conflictingReplay.Code != http.StatusConflict || !strings.Contains(conflictingReplay.Body.String(), "idempotency_conflict") || dispatcher.calls != 1 {
		t.Fatalf("conflicting replay status=%d calls=%d body=%s", conflictingReplay.Code, dispatcher.calls, conflictingReplay.Body.String())
	}
	visual.assets = streamvisual.AssetReadiness{CoverVariantReady: true, MediaAssetIntegrity: true}
	hideMissingConfirmation := serveUserJSON(t, handler, http.MethodPut, "/streams/"+stream.ID+"/video-cover-state", `{"active":false,"expected_job_generation":1,"expected_revision":2,"idempotency_key":"hide-1"}`, allowedCookie, allowedCSRF)
	if hideMissingConfirmation.Code != http.StatusBadRequest || dispatcher.calls != 1 {
		t.Fatalf("hide without confirmation status=%d calls=%d", hideMissingConfirmation.Code, dispatcher.calls)
	}
	dispatcher.result = servicecall.VideoCoverDispatchResult{Ambiguous: true}
	hideBody := `{"active":false,"expected_job_generation":1,"expected_revision":2,"idempotency_key":"hide-1","hide_confirmed":true}`
	ambiguous := serveUserJSON(t, handler, http.MethodPut, "/streams/"+stream.ID+"/video-cover-state", hideBody, allowedCookie, allowedCSRF)
	if ambiguous.Code != http.StatusAccepted || dispatcher.calls != 2 || !strings.Contains(ambiguous.Body.String(), `"status":"confirming"`) {
		t.Fatalf("ambiguous status=%d calls=%d body=%s", ambiguous.Code, dispatcher.calls, ambiguous.Body.String())
	}
	state, err := covers.GetCurrentState(t.Context(), stream.ID)
	if err != nil || state.AppliedActive == nil || !*state.AppliedActive || state.AppliedRevision == nil || *state.AppliedRevision != 2 {
		t.Fatalf("hide ambiguity fabricated public-video applied state=%#v err=%v", state, err)
	}
	reconcile := serveUserJSON(t, handler, http.MethodPut, "/streams/"+stream.ID+"/video-cover-state", hideBody, allowedCookie, allowedCSRF)
	if reconcile.Code != http.StatusOK || dispatcher.calls != 2 {
		t.Fatalf("ambiguous duplicate resent status=%d calls=%d", reconcile.Code, dispatcher.calls)
	}
	dispatcher.result = servicecall.VideoCoverDispatchResult{SafeErrorCode: "permission_denied"}
	failedBody := `{"active":true,"expected_job_generation":1,"expected_revision":3,"idempotency_key":"show-2"}`
	failed := serveUserJSON(t, handler, http.MethodPut, "/streams/"+stream.ID+"/video-cover-state", failedBody, allowedCookie, allowedCSRF)
	if failed.Code != http.StatusBadGateway || dispatcher.calls != 3 {
		t.Fatalf("backend failure status=%d calls=%d body=%s", failed.Code, dispatcher.calls, failed.Body.String())
	}
	failedReplay := serveUserJSON(t, handler, http.MethodPut, "/streams/"+stream.ID+"/video-cover-state", failedBody, allowedCookie, allowedCSRF)
	if failedReplay.Code != http.StatusOK || dispatcher.calls != 3 {
		t.Fatalf("backend 403-like replay resent status=%d calls=%d", failedReplay.Code, dispatcher.calls)
	}
	if _, err = covers.EnsureGeneration(t.Context(), stream.ID, 2, "variant-cover-2", false); err != nil {
		t.Fatal(err)
	}
	stale := serveUserJSON(t, handler, http.MethodPut, "/streams/"+stream.ID+"/video-cover-state", `{"active":true,"expected_job_generation":1,"expected_revision":4,"idempotency_key":"stale-generation"}`, allowedCookie, allowedCSRF)
	if stale.Code != http.StatusConflict || !strings.Contains(stale.Body.String(), "stale_job_generation") || dispatcher.calls != 3 {
		t.Fatalf("stale status=%d calls=%d body=%s", stale.Code, dispatcher.calls, stale.Body.String())
	}
}
