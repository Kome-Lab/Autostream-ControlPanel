package httpapi

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/example/autostream-control-panel/internal/mediaassets"
	"github.com/example/autostream-control-panel/internal/servicecall"
	"github.com/example/autostream-control-panel/internal/store"
	"github.com/example/autostream-control-panel/internal/streamvisual"
	"github.com/example/autostream-control-panel/internal/videocover"
)

type countingVideoCoverDispatcher struct {
	calls           int
	result          servicecall.VideoCoverDispatchResult
	requests        []servicecall.VideoCoverDispatchRequest
	reconcileCalls  int
	reconcileResult servicecall.VideoCoverDispatchResult
}

func (d *countingVideoCoverDispatcher) DispatchVideoCover(_ context.Context, _ store.RegisteredService, request servicecall.VideoCoverDispatchRequest) servicecall.VideoCoverDispatchResult {
	d.calls++
	d.requests = append(d.requests, request)
	return d.result
}

func (d *countingVideoCoverDispatcher) ReconcileVideoCover(_ context.Context, _ store.RegisteredService, _ servicecall.VideoCoverReconcileRequest) servicecall.VideoCoverDispatchResult {
	d.reconcileCalls++
	return d.reconcileResult
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
	media, err := mediaassets.NewMemoryRepository(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	session, err := media.CreateUploadSession(t.Context(), "cover-allowed", time.Now().UTC().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	asset, err := media.Upload(t.Context(), mediaassets.UploadInput{SessionID: session.ID, UserID: "cover-allowed", UsageType: "video_cover", Filename: "cover.png", ContentType: "image/png", Body: bytes.NewReader(testVideoCoverPNG(t))})
	if err != nil {
		t.Fatal(err)
	}
	variant, err := media.EnsureVariant(t.Context(), "cover-allowed", asset.ID, 1920, 1080, true)
	if err != nil {
		t.Fatal(err)
	}
	if err = media.ClaimDraft(t.Context(), "cover-allowed", session.ID, stream.ID, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	media.ReferenceVariant(stream.ID, variant.ID)
	covers := videocover.NewMemoryRepository()
	if _, err = covers.EnsureGeneration(t.Context(), stream.ID, 1, variant.ID, false); err != nil {
		t.Fatal(err)
	}
	dispatcher := &countingVideoCoverDispatcher{result: servicecall.VideoCoverDispatchResult{Applied: true}}
	visual := &fixedVisualRepository{
		settings: streamvisual.Settings{StreamID: stream.ID, BackgroundMode: "default", HeaderTitleMode: "default", CoverSource: "upload", CoverAssetID: asset.ID, CoverVariantID: variant.ID, Revision: 1},
		assets:   streamvisual.AssetReadiness{CoverVariantReady: true, MediaAssetIntegrity: true},
	}
	handler := NewServer(streams, WithAuthStore(auth), WithAuditStore(auth), WithServiceRegistryStore(auth), WithMediaAssetRepository(media), WithStreamVisualRepository(visual), WithVideoCoverRepository(covers), WithVideoCoverDispatcher(dispatcher))
	allowedCookie, allowedCSRF := loginForTest(t, handler, "cover-allowed", "correct horse battery")
	deniedCookie, deniedCSRF := loginForTest(t, handler, "cover-denied", "correct horse battery")
	invalidBodies := map[string]string{
		"missing_active":   `{"expected_job_generation":1,"expected_revision":1,"idempotency_key":"show-1"}`,
		"null_active":      `{"active":null,"expected_job_generation":1,"expected_revision":1,"idempotency_key":"show-1"}`,
		"duplicate_active": `{"active":true,"active":false,"expected_job_generation":1,"expected_revision":1,"idempotency_key":"show-1","hide_confirmed":true}`,
		"show_with_hide":   `{"active":true,"expected_job_generation":1,"expected_revision":1,"idempotency_key":"show-1","hide_confirmed":true}`,
		"padded_key":       `{"active":true,"expected_job_generation":1,"expected_revision":1,"idempotency_key":" show-1"}`,
	}
	invalidUTF8 := append([]byte(`{"active":true,"expected_job_generation":1,"expected_revision":1,"idempotency_key":"`), 0xff, '"', '}')
	invalidBodies["invalid_utf8"] = string(invalidUTF8)
	for name, body := range invalidBodies {
		t.Run("invalid_request_"+name, func(t *testing.T) {
			response := serveUserJSON(t, handler, http.MethodPut, "/streams/"+stream.ID+"/video-cover-state", body, allowedCookie, allowedCSRF)
			if response.Code != http.StatusBadRequest || dispatcher.calls != 0 {
				t.Fatalf("status=%d calls=%d body=%s", response.Code, dispatcher.calls, response.Body.String())
			}
		})
	}
	beforeAction, err := covers.GetCurrentState(t.Context(), stream.ID)
	if err != nil || beforeAction.DesiredRevision != 1 || beforeAction.LastIdempotencyKey != "" {
		t.Fatalf("invalid request mutated state=%#v err=%v", beforeAction, err)
	}
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
	// Replaying an older applied key while the newer key is ambiguous must not
	// reconcile or overwrite the newer action's shared confirming state.
	visual.assets = streamvisual.AssetReadiness{CoverVariantReady: true}
	olderWhileConfirming := serveUserJSON(t, handler, http.MethodPut, "/streams/"+stream.ID+"/video-cover-state", showBody, allowedCookie, allowedCSRF)
	if olderWhileConfirming.Code != http.StatusOK || dispatcher.calls != 2 || dispatcher.reconcileCalls != 0 {
		t.Fatalf("older replay reconciled newer action status=%d dispatch=%d reconcile=%d body=%s", olderWhileConfirming.Code, dispatcher.calls, dispatcher.reconcileCalls, olderWhileConfirming.Body.String())
	}
	state, err = covers.GetCurrentState(t.Context(), stream.ID)
	if err != nil || state.Status != "confirming" || state.LastIdempotencyKey != "hide-1" {
		t.Fatalf("older replay overwrote newer confirming state=%#v err=%v", state, err)
	}
	visual.assets = streamvisual.AssetReadiness{CoverVariantReady: true, MediaAssetIntegrity: true}
	dispatcher.reconcileResult = servicecall.VideoCoverDispatchResult{Applied: true}
	reconcile := serveUserJSON(t, handler, http.MethodPut, "/streams/"+stream.ID+"/video-cover-state", hideBody, allowedCookie, allowedCSRF)
	if reconcile.Code != http.StatusOK || dispatcher.calls != 2 || dispatcher.reconcileCalls != 1 {
		t.Fatalf("ambiguous duplicate mutation resent status=%d dispatch=%d reconcile=%d", reconcile.Code, dispatcher.calls, dispatcher.reconcileCalls)
	}
	// The first key remains an exact replay after a later key becomes current,
	// even when mutable asset health has since degraded.
	visual.assets = streamvisual.AssetReadiness{CoverVariantReady: true}
	olderReplay := serveUserJSON(t, handler, http.MethodPut, "/streams/"+stream.ID+"/video-cover-state", showBody, allowedCookie, allowedCSRF)
	if olderReplay.Code != http.StatusOK || dispatcher.calls != 2 || dispatcher.reconcileCalls != 1 {
		t.Fatalf("older exact replay consulted mutable state status=%d dispatch=%d reconcile=%d body=%s", olderReplay.Code, dispatcher.calls, dispatcher.reconcileCalls, olderReplay.Body.String())
	}
	visual.assets = streamvisual.AssetReadiness{CoverVariantReady: true, MediaAssetIntegrity: true}
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
	visual.settings.CoverAssetID = "missing-cover-asset"
	visual.settings.CoverVariantID = "variant-cover-2"
	visual.assets = streamvisual.AssetReadiness{CoverVariantReady: true, MediaAssetIntegrity: true}
	descriptorFailureBody := `{"active":true,"expected_job_generation":2,"expected_revision":1,"idempotency_key":"descriptor-failure"}`
	descriptorFailure := serveUserJSON(t, handler, http.MethodPut, "/streams/"+stream.ID+"/video-cover-state", descriptorFailureBody, allowedCookie, allowedCSRF)
	if descriptorFailure.Code != http.StatusBadGateway || dispatcher.calls != 3 || !strings.Contains(descriptorFailure.Body.String(), `"status":"failed"`) || !strings.Contains(descriptorFailure.Body.String(), `"last_error_code":"media_asset_integrity"`) {
		t.Fatalf("descriptor failure status=%d calls=%d body=%s", descriptorFailure.Code, dispatcher.calls, descriptorFailure.Body.String())
	}
	descriptorReplay := serveUserJSON(t, handler, http.MethodPut, "/streams/"+stream.ID+"/video-cover-state", descriptorFailureBody, allowedCookie, allowedCSRF)
	if descriptorReplay.Code != http.StatusOK || dispatcher.calls != 3 || !strings.Contains(descriptorReplay.Body.String(), `"status":"failed"`) {
		t.Fatalf("descriptor failure replay status=%d calls=%d body=%s", descriptorReplay.Code, dispatcher.calls, descriptorReplay.Body.String())
	}
	foundFailureAudit := false
	for _, event := range auth.AuditEvents() {
		if event.Action == "streams.video_cover.show" && event.ResourceID == stream.ID && event.Result == "failure" && event.Metadata["status"] == "failed" && event.Metadata["error_code"] == "media_asset_integrity" {
			foundFailureAudit = true
			break
		}
	}
	if !foundFailureAudit {
		t.Fatalf("descriptor failure immutable audit missing: %#v", auth.AuditEvents())
	}
}

func testVideoCoverPNG(t *testing.T) []byte {
	t.Helper()
	frame := image.NewNRGBA(image.Rect(0, 0, 16, 9))
	for y := 0; y < 9; y++ {
		for x := 0; x < 16; x++ {
			frame.SetNRGBA(x, y, color.NRGBA{R: uint8(x * 8), G: uint8(y * 16), B: 64, A: 255})
		}
	}
	var body bytes.Buffer
	if err := png.Encode(&body, frame); err != nil {
		t.Fatal(err)
	}
	return body.Bytes()
}
