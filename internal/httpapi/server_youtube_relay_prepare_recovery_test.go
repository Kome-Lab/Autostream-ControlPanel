package httpapi

import (
	"bytes"
	"context"
	"errors"
	"github.com/example/autostream-control-panel/internal/store"
	ytlive "github.com/example/autostream-control-panel/internal/youtube"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestStartStreamRelayStaticReconcilesCommittedFinalizeResponseLossBeforeDispatch(t *testing.T) {
	fixture := newRelayStaticStartFixtureForTest(t, nil, nil)
	streamStore := &relayStaticFinalizeResponseLostStreamStore{MemoryStreamStore: fixture.streams, responseLost: true}
	fixture.server.streams = streamStore

	response := fixture.start(t)
	if response.Code != http.StatusOK {
		t.Fatalf("finalizer response loss should reconcile the committed state before dispatch: status=%d body=%s", response.Code, response.Body.String())
	}
	if fixture.youtubeLive.relayStaticPrepareCalls != 1 || fixture.dispatcher.startCalls != 1 {
		t.Fatalf("reconciled finalizer response loss must prepare and dispatch exactly once: youtube=%#v dispatcher=%#v", fixture.youtubeLive, fixture.dispatcher)
	}
	runtime, err := fixture.streams.GetStreamYouTubeRuntime(t.Context(), fixture.stream.ID)
	if err != nil || runtime.Mode != "live_api_relay_static" || runtime.BroadcastID != "broadcast-static-dispatch" {
		t.Fatalf("finalizer response loss must retain exact committed runtime: runtime=%#v err=%v", runtime, err)
	}
	claim, err := fixture.streams.GetStreamYouTubeRelayBindingClaim(t.Context(), fixture.relayBindingID)
	if err != nil || claim.State != store.YouTubeRelayBindingClaimStatePrepared || claim.BroadcastID != runtime.BroadcastID {
		t.Fatalf("finalizer response loss must retain exact prepared claim: claim=%#v err=%v", claim, err)
	}
	stream, err := fixture.streams.GetStream(t.Context(), fixture.stream.ID)
	if err != nil || stream.Status != "live" {
		t.Fatalf("reconciled finalizer response loss must complete normal start lifecycle: stream=%#v err=%v", stream, err)
	}
}

func TestStartStreamRelayStaticPrepareMarkerResponseLossRequiresRecoveryBeforeProviderCall(t *testing.T) {
	fixture := newRelayStaticStartFixtureForTest(t, nil, nil)
	fixture.server.streams = &relayStaticPrepareMarkerResponseLostStreamStore{MemoryStreamStore: fixture.streams, responseLost: true}

	response := fixture.start(t)
	if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), errYouTubeRelayStaticRecoveryRequired.Error()) {
		t.Fatalf("prepare marker response loss status=%d body=%s", response.Code, response.Body.String())
	}
	if fixture.youtubeLive.prepareCalls != 0 || fixture.youtubeLive.relayStaticPrepareCalls != 0 || fixture.dispatcher.startCalls != 0 {
		t.Fatalf("prepare marker response loss must not call provider or dispatch: youtube=%#v dispatcher=%#v", fixture.youtubeLive, fixture.dispatcher)
	}
	if _, err := fixture.streams.GetStreamYouTubeRuntime(t.Context(), fixture.stream.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("prepare marker response loss must not create runtime: %v", err)
	}
	claim, err := fixture.streams.GetStreamYouTubeRelayBindingClaim(t.Context(), fixture.relayBindingID)
	if err != nil || claim.State != store.YouTubeRelayBindingClaimStateRecoveryRequired || claim.PrepareState != store.YouTubeRelayBindingClaimPrepareStatePossiblyPrepared || claim.DispatchState != store.YouTubeRelayBindingClaimDispatchStateNotDispatched || claim.BroadcastID != youtubeRelayStaticUnknownBroadcastID {
		t.Fatalf("prepare marker response loss must retain a pre-dispatch recovery claim: claim=%#v err=%v", claim, err)
	}
	stream, err := fixture.streams.GetStream(t.Context(), fixture.stream.ID)
	if err != nil || stream.Status != "failed" {
		t.Fatalf("prepare marker response loss must converge static lifecycle to failed: stream=%#v err=%v", stream, err)
	}
}

func TestResolveRelayStaticRecoveryReconcilesInactiveReservedPrepareFence(t *testing.T) {
	reserve := func(t *testing.T, fixture relayStaticStartFixtureForTest, possiblyPrepared bool) store.YouTubeRelayBindingClaim {
		t.Helper()
		starting, transitioned, err := fixture.streams.TransitionStreamStatus(t.Context(), fixture.stream.ID, fixture.stream.Status, "starting")
		if err != nil || !transitioned {
			t.Fatalf("claim static stream start: transitioned=%t err=%v", transitioned, err)
		}
		expectedRevision := fixture.youtube.YouTubeRelayBindingRevision
		claim, err := fixture.streams.ReserveStreamYouTubeRelayBindingClaim(t.Context(), store.YouTubeRelayBindingClaim{
			RelayBindingID:                fixture.relayBindingID,
			StreamID:                      starting.ID,
			YouTubeOutputID:               fixture.youtube.ID,
			ExpectedYouTubeOutputRevision: &expectedRevision,
			OAuthAccountID:                configString(fixture.youtube.Config, "oauth_account_id"),
			ReusableLiveStreamID:          configString(fixture.youtube.Config, "reusable_live_stream_id"),
		})
		if err != nil {
			t.Fatalf("reserve static prepare-fence claim: %v", err)
		}
		if possiblyPrepared {
			claim, err = fixture.streams.MarkReservedStreamYouTubeRelayBindingClaimPossiblyPrepared(t.Context(), claim)
			if err != nil {
				t.Fatalf("mark static prepare-fence claim: %v", err)
			}
		}
		if _, transitioned, err := fixture.streams.TransitionStreamStatus(t.Context(), starting.ID, "starting", "failed"); err != nil || !transitioned {
			t.Fatalf("terminalize reserved prepare-fence stream: transitioned=%t err=%v", transitioned, err)
		}
		return claim
	}
	resolve := func(t *testing.T, fixture relayStaticStartFixtureForTest) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, "/streams/"+fixture.stream.ID+"/youtube/relay-static/recovery/resolve", bytes.NewBufferString(`{"confirm_external_cleanup":true}`))
		req.AddCookie(fixture.cookie)
		req.Header.Set("X-CSRF-Token", fixture.csrf)
		response := httptest.NewRecorder()
		fixture.server.ServeHTTP(response, req)
		return response
	}

	t.Run("not attempted reservation releases without provider or encoder call", func(t *testing.T) {
		fixture := newRelayStaticStartFixtureForTest(t, nil, nil)
		claim := reserve(t, fixture, false)
		response := resolve(t, fixture)
		if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"resolved":true`) {
			t.Fatalf("reserved not-attempted recovery status=%d body=%s", response.Code, response.Body.String())
		}
		if fixture.dispatcher.stopCalls != 0 || fixture.youtubeLive.relayStaticPrepareCalls != 0 || fixture.youtubeLive.relayStaticCleanupCalls != 0 || fixture.youtubeLive.completeCalls != 0 {
			t.Fatalf("not-attempted reservation must not call Encoder or provider: dispatcher=%#v youtube=%#v", fixture.dispatcher, fixture.youtubeLive)
		}
		if _, err := fixture.streams.GetStreamYouTubeRelayBindingClaim(t.Context(), claim.RelayBindingID); !errors.Is(err, store.ErrNotFound) {
			t.Fatalf("not-attempted reserved claim must be released: %v", err)
		}
	})

	t.Run("possibly prepared reservation becomes explicit unknown-broadcast recovery", func(t *testing.T) {
		fixture := newRelayStaticStartFixtureForTest(t, nil, nil)
		claim := reserve(t, fixture, true)
		response := resolve(t, fixture)
		if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"cleanup":"operator_confirmed_unknown_broadcast"`) {
			t.Fatalf("reserved possibly-prepared recovery status=%d body=%s", response.Code, response.Body.String())
		}
		if fixture.dispatcher.stopCalls != 1 || fixture.youtubeLive.relayStaticPrepareCalls != 0 || fixture.youtubeLive.relayStaticCleanupCalls != 0 || fixture.youtubeLive.completeCalls != 0 {
			t.Fatalf("possibly-prepared claim must require the no-process fence but no provider cleanup: dispatcher=%#v youtube=%#v", fixture.dispatcher, fixture.youtubeLive)
		}
		if _, err := fixture.streams.GetStreamYouTubeRelayBindingClaim(t.Context(), claim.RelayBindingID); !errors.Is(err, store.ErrNotFound) {
			t.Fatalf("operator-confirmed unknown-broadcast recovery must release claim: %v", err)
		}
	})
}

func TestResolveRelayStaticRecoveryRepairsInactivePreparedDispatchMarkerOutage(t *testing.T) {
	fixture := newRelayStaticStartFixtureForTest(t, nil, nil)
	fixture.server.streams = &relayStaticDispatchMarkerRepairStreamStore{
		MemoryStreamStore:       fixture.streams,
		failInitialMarkRecovery: true,
	}

	start := fixture.start(t)
	if start.Code != http.StatusConflict || !strings.Contains(start.Body.String(), errYouTubeRelayStaticRecoveryRequired.Error()) {
		t.Fatalf("dispatch marker outage start status=%d body=%s", start.Code, start.Body.String())
	}
	if fixture.youtubeLive.relayStaticPrepareCalls != 1 || fixture.dispatcher.startCalls != 0 {
		t.Fatalf("dispatch marker outage must not issue downstream Start: youtube=%#v dispatcher=%#v", fixture.youtubeLive, fixture.dispatcher)
	}
	stream, err := fixture.streams.GetStream(t.Context(), fixture.stream.ID)
	if err != nil || stream.Status != "failed" {
		t.Fatalf("dispatch marker outage must terminalize the start: stream=%#v err=%v", stream, err)
	}
	claim, err := fixture.streams.GetStreamYouTubeRelayBindingClaim(t.Context(), fixture.relayBindingID)
	if err != nil || claim.State != store.YouTubeRelayBindingClaimStatePrepared || claim.PrepareState != store.YouTubeRelayBindingClaimPrepareStatePossiblyPrepared || claim.DispatchState != store.YouTubeRelayBindingClaimDispatchStatePossiblyDispatched {
		t.Fatalf("dispatch marker outage must retain exact prepared handoff fence: claim=%#v err=%v", claim, err)
	}
	if _, err := fixture.streams.GetStreamYouTubeRuntime(t.Context(), fixture.stream.ID); err != nil {
		t.Fatalf("dispatch marker outage must retain runtime until explicit repair: %v", err)
	}

	recoveryReq := httptest.NewRequest(http.MethodPost, "/streams/"+fixture.stream.ID+"/youtube/relay-static/recovery/resolve", bytes.NewBufferString(`{"confirm_external_cleanup":true}`))
	recoveryReq.AddCookie(fixture.cookie)
	recoveryReq.Header.Set("X-CSRF-Token", fixture.csrf)
	recoveryRes := httptest.NewRecorder()
	fixture.server.ServeHTTP(recoveryRes, recoveryReq)
	if recoveryRes.Code != http.StatusOK || !strings.Contains(recoveryRes.Body.String(), `"cleanup":"provider_complete"`) {
		t.Fatalf("dispatch marker outage recovery status=%d body=%s", recoveryRes.Code, recoveryRes.Body.String())
	}
	if fixture.dispatcher.stopCalls != 1 || fixture.youtubeLive.completeCalls != 1 || fixture.youtubeLive.relayStaticCleanupCalls != 0 {
		t.Fatalf("prepared marker repair must Stop once then Complete, never Delete: dispatcher=%#v youtube=%#v", fixture.dispatcher, fixture.youtubeLive)
	}
	if _, err := fixture.streams.GetStreamYouTubeRuntime(t.Context(), fixture.stream.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("prepared marker repair must atomically remove runtime before release: %v", err)
	}
	if _, err := fixture.streams.GetStreamYouTubeRelayBindingClaim(t.Context(), fixture.relayBindingID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("prepared marker repair must release recovered claim: %v", err)
	}
}

func TestStartStreamRelayStaticObservedFinalizeCommitStillNotifiesDiscord(t *testing.T) {
	fixture := newRelayStaticStartFixtureForTest(t, nil, nil)
	if _, err := fixture.streams.UpdateStreamSettings(t.Context(), fixture.stream.ID, store.StreamSettings{
		YouTubeOutputID: fixture.youtube.ID,
	}); err != nil {
		t.Fatal(err)
	}
	notifier := &relayStaticNotificationDispatcher{fakeServiceDispatcher: fixture.dispatcher}
	fixture.server.dispatcher = notifier
	fixture.server.streams = &relayStaticFinalizeResponseLostStreamStore{MemoryStreamStore: fixture.streams, responseLost: true}

	response := fixture.start(t)
	if response.Code != http.StatusOK {
		t.Fatalf("observed finalizer commit start status=%d body=%s", response.Code, response.Body.String())
	}
	queued, err := fixture.streams.GetLatestDiscordYouTubeLiveNotification(t.Context(), fixture.stream.ID)
	if err != nil {
		t.Fatal(err)
	}
	if queued.State != store.DiscordYouTubeLiveNotificationStateAwaitingYouTubeLive || queued.WatchURL != "https://www.youtube.com/watch?v=broadcast-static-dispatch" || notifier.notifyCalls != 0 {
		t.Fatalf("observed finalizer commit must queue the static watch URL before provider-live delivery: notification=%s notifier=%s", formatSafeHTTPSensitiveDiagnostic(queued), formatSafeHTTPSensitiveDiagnostic(notifier))
	}
	fixture.server.youtubeLive = &scriptedYouTubeLifecycleClient{fakeYouTubeLiveClient: fixture.youtubeLive, statuses: []string{"live"}}
	result, err := fixture.server.DispatchDueDiscordYouTubeLiveNotifications(t.Context(), 1)
	if err != nil || result["claimed"] != 1 || result["delivered"] != 1 {
		t.Fatalf("observed finalizer notification delivery result=%#v err=%v", result, err)
	}
	if notifier.notifyCalls != 1 || notifier.notifiedStream.Status != "live" || notifier.notifiedURL != "https://www.youtube.com/watch?v=broadcast-static-dispatch" {
		t.Fatalf("observed finalizer commit must retain static watch URL for Discord notification: notifier=%s", formatSafeHTTPSensitiveDiagnostic(notifier))
	}
	if fixture.youtubeLive.relayStaticPrepareCalls != 1 || fixture.dispatcher.startCalls != 1 {
		t.Fatalf("observed finalizer commit must still perform one static prepare and dispatch: youtube=%#v dispatcher=%#v", fixture.youtubeLive, fixture.dispatcher)
	}
}

func TestStartStreamRelayStaticPrepareStatusChangeRetainsRecoveryFence(t *testing.T) {
	fixture := newRelayStaticStartFixtureForTest(t, nil, nil)
	fixture.youtubeLive.onRelayStaticPrepare = func(ctx context.Context, _ ytlive.RelayStaticPrepareRequest) {
		if _, transitioned, err := fixture.streams.TransitionStreamStatus(ctx, fixture.stream.ID, "starting", "failed"); err != nil || !transitioned {
			t.Fatalf("supersede static start after provider prepare: transitioned=%t err=%v", transitioned, err)
		}
	}

	res := fixture.start(t)
	if res.Code != http.StatusConflict || !strings.Contains(res.Body.String(), errYouTubeRelayStaticRecoveryRequired.Error()) {
		t.Fatalf("prepare/status conflict status=%d body=%s", res.Code, res.Body.String())
	}
	if fixture.youtubeLive.relayStaticPrepareCalls != 1 || fixture.dispatcher.startCalls != 0 || fixture.youtubeLive.completeCalls != 0 || fixture.youtubeLive.relayStaticCleanupCalls != 0 {
		t.Fatalf("superseded static prepare must not dispatch or clean up as a live stream: youtube=%#v dispatcher=%#v", fixture.youtubeLive, fixture.dispatcher)
	}
	updated, err := fixture.streams.GetStream(t.Context(), fixture.stream.ID)
	if err != nil || updated.Status != "failed" {
		t.Fatalf("provider/status conflict must preserve superseding lifecycle state: stream=%#v err=%v", updated, err)
	}
	if _, err := fixture.streams.GetStreamYouTubeRuntime(t.Context(), fixture.stream.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("finalize conflict must not orphan a static runtime, err=%v", err)
	}
	claim, err := fixture.streams.GetStreamYouTubeRelayBindingClaim(t.Context(), fixture.relayBindingID)
	if err != nil || claim.State != store.YouTubeRelayBindingClaimStateRecoveryRequired || claim.BroadcastID != "broadcast-static-dispatch" {
		t.Fatalf("provider/status conflict must preserve a recovery fence: claim=%#v err=%v", claim, err)
	}
	due, err := fixture.streams.ListDueStreamYouTubeRuntimes(t.Context(), time.Now().UTC(), 10)
	if err != nil || len(due) != 0 {
		t.Fatalf("finalize conflict left an unexpected completion retry: due=%#v err=%v", due, err)
	}
}
