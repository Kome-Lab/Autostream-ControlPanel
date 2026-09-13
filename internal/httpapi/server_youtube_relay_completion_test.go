package httpapi

import (
	"bytes"
	"errors"
	"github.com/example/autostream-control-panel/internal/store"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestCompleteRelayStaticYouTubeRetainsClaimUntilProviderCompletionSucceeds(t *testing.T) {
	streams := store.NewMemoryStreamStore()
	stream, err := streams.CreateStream(t.Context(), "relay static completion retry")
	if err != nil {
		t.Fatal(err)
	}
	integrations := store.NewMemoryIntegrationStore()
	provider, err := integrations.CreateOAuthProvider(t.Context(), store.OAuthProvider{
		ProviderType: "google",
		Name:         "YouTube Google",
		Enabled:      true,
		ClientID:     "youtube-client-id",
		ClientSecret: "raw-youtube-client-secret",
		Scopes:       []string{"https://www.googleapis.com/auth/youtube"},
		RedirectURI:  "https://control.example.com/auth/oauth/google/callback",
	})
	if err != nil {
		t.Fatal(err)
	}
	account, err := integrations.CreateOAuthAccount(t.Context(), store.OAuthAccount{
		ProviderID:   provider.ID,
		ProviderType: "google",
		AccountLabel: "youtube account",
		RefreshToken: "raw-youtube-refresh-token",
		Scopes:       []string{"https://www.googleapis.com/auth/youtube"},
	})
	if err != nil {
		t.Fatal(err)
	}
	profiles := store.NewMemoryProfileStore()
	youtube, err := profiles.CreateProfile(t.Context(), store.ProfileYouTubeOutput, "relay static completion output", map[string]any{
		"mode":                    "live_api_relay_static",
		"oauth_account_id":        account.ID,
		"relay_binding_id":        "relay-00000000-0000-4000-8000-00000000000a",
		"reusable_live_stream_id": "youtube-live-stream-complete",
	})
	if err != nil {
		t.Fatal(err)
	}
	stream = setRelayStaticReservationPreconditionsForTest(t, streams, profiles, stream, youtube)
	expectedYouTubeOutputRevision := youtube.YouTubeRelayBindingRevision
	reserved, err := streams.ReserveStreamYouTubeRelayBindingClaim(t.Context(), store.YouTubeRelayBindingClaim{
		RelayBindingID:                "relay-00000000-0000-4000-8000-00000000000a",
		StreamID:                      stream.ID,
		YouTubeOutputID:               youtube.ID,
		ExpectedYouTubeOutputRevision: &expectedYouTubeOutputRevision,
		OAuthAccountID:                account.ID,
		ReusableLiveStreamID:          "youtube-live-stream-complete",
	})
	if err != nil {
		t.Fatal(err)
	}
	reserved, err = streams.MarkReservedStreamYouTubeRelayBindingClaimPossiblyPrepared(t.Context(), reserved)
	if err != nil {
		t.Fatalf("mark static completion fixture possibly prepared: %v", err)
	}
	reserved.BroadcastID = "broadcast-static-complete"
	runtime := store.StreamYouTubeRuntime{
		StreamID:       stream.ID,
		YouTubeOutput:  reserved.YouTubeOutputID,
		OAuthAccountID: account.ID,
		Mode:           "live_api_relay_static",
		BroadcastID:    reserved.BroadcastID,
		LiveStreamID:   reserved.ReusableLiveStreamID,
		CompleteOnStop: true,
	}
	if err := streams.FinalizeStreamYouTubeRuntimeAndMarkRelayBindingPrepared(t.Context(), reserved, runtime); err != nil {
		t.Fatal(err)
	}
	if _, err := streams.MarkPreparedStreamYouTubeRelayBindingClaimPossiblyDispatched(t.Context(), reserved); err != nil {
		t.Fatalf("mark static completion fixture possibly dispatched: %v", err)
	}
	if _, transitioned, err := streams.TransitionStreamStatus(t.Context(), stream.ID, "starting", "completed"); err != nil || !transitioned {
		t.Fatalf("mark static completion fixture completed: transitioned=%t err=%v", transitioned, err)
	}
	youtubeLive := &fakeYouTubeLiveClient{completeErr: errors.New("youtube transition unavailable")}
	withoutReconciler := &Server{streams: streams, integrations: integrations, youtubeLive: youtubeLive}
	if _, err := withoutReconciler.completeYouTubeRuntime(t.Context(), stream.ID, true); !errors.Is(err, errYouTubeRelayStaticUnavailable) {
		t.Fatalf("static completion without a reconciler err=%v, want fail-closed unavailable", err)
	}
	if youtubeLive.completeCalls != 0 {
		t.Fatalf("static completion must not fall back to generic Complete: calls=%d", youtubeLive.completeCalls)
	}
	stillFencedRuntime, err := streams.GetStreamYouTubeRuntime(t.Context(), stream.ID)
	if err != nil || stillFencedRuntime.CompleteRetryCount != 1 {
		t.Fatalf("missing reconciler must retain and retry static runtime: runtime=%#v err=%v", stillFencedRuntime, err)
	}
	if claim, err := streams.GetStreamYouTubeRelayBindingClaim(t.Context(), reserved.RelayBindingID); err != nil || claim.State != store.YouTubeRelayBindingClaimStatePrepared {
		t.Fatalf("missing reconciler must retain static claim: claim=%#v err=%v", claim, err)
	}
	server := &Server{streams: streams, integrations: integrations, youtubeLive: &relayStaticCompletingYouTubeLiveClient{fakeYouTubeLiveClient: youtubeLive}}
	if _, err := server.completeYouTubeRuntime(t.Context(), stream.ID, true); !errors.Is(err, errYouTubeLiveAPICompleteFailed) {
		t.Fatalf("completion error=%v, want provider completion failure", err)
	}
	if youtubeLive.completeCalls != 1 || youtubeLive.completeRequest.BroadcastID != runtime.BroadcastID {
		t.Fatalf("provider completion was not attempted: %#v", youtubeLive.completeRequest)
	}
	storedRuntime, err := streams.GetStreamYouTubeRuntime(t.Context(), stream.ID)
	if err != nil {
		t.Fatalf("provider failure must retain runtime for retry: %v", err)
	}
	if storedRuntime.CompleteRetryCount != 2 || storedRuntime.CompleteNextRetryAt.IsZero() {
		t.Fatalf("provider failure did not schedule retry: %#v", storedRuntime)
	}
	claim, err := streams.GetStreamYouTubeRelayBindingClaim(t.Context(), reserved.RelayBindingID)
	if err != nil || claim.State != store.YouTubeRelayBindingClaimStatePrepared {
		t.Fatalf("provider failure must retain prepared claim: claim=%#v err=%v", claim, err)
	}

	youtubeLive.completeErr = nil
	if _, err := server.completeYouTubeRuntime(t.Context(), stream.ID, true); err != nil {
		t.Fatalf("completion retry: %v", err)
	}
	if youtubeLive.completeCalls != 2 {
		t.Fatalf("completion retry did not call provider: %d", youtubeLive.completeCalls)
	}
	if _, err := streams.GetStreamYouTubeRuntime(t.Context(), stream.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("successful completion must atomically release runtime, err=%v", err)
	}
	if _, err := streams.GetStreamYouTubeRelayBindingClaim(t.Context(), reserved.RelayBindingID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("successful completion must atomically release claim, err=%v", err)
	}
}

func TestYouTubeOutputMutationAndDeleteRejectActiveRelayStaticClaim(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "operator", Roles: []string{"stream_operator"}}, "correct horse battery", []string{"youtube_outputs.update", "youtube_outputs.delete"}); err != nil {
		t.Fatal(err)
	}
	streams := store.NewMemoryStreamStore()
	stream, err := streams.CreateStream(t.Context(), "claimed output stream")
	if err != nil {
		t.Fatal(err)
	}
	profiles := store.NewMemoryProfileStore()
	youtube, err := profiles.CreateProfile(t.Context(), store.ProfileYouTubeOutput, "claimed static output", map[string]any{
		"mode":                    "live_api_relay_static",
		"oauth_account_id":        "8fd47de4-5aec-486f-99c7-10591076c408",
		"relay_binding_id":        "relay-00000000-0000-4000-8000-00000000000b",
		"reusable_live_stream_id": "youtube-live-stream-claimed-output",
	})
	if err != nil {
		t.Fatal(err)
	}
	stream = setRelayStaticReservationPreconditionsForTest(t, streams, profiles, stream, youtube)
	expectedYouTubeOutputRevision := youtube.YouTubeRelayBindingRevision
	if _, err := streams.ReserveStreamYouTubeRelayBindingClaim(t.Context(), store.YouTubeRelayBindingClaim{
		RelayBindingID:                "relay-00000000-0000-4000-8000-00000000000b",
		StreamID:                      stream.ID,
		YouTubeOutputID:               youtube.ID,
		ExpectedYouTubeOutputRevision: &expectedYouTubeOutputRevision,
		OAuthAccountID:                "8fd47de4-5aec-486f-99c7-10591076c408",
		ReusableLiveStreamID:          "youtube-live-stream-claimed-output",
	}); err != nil {
		t.Fatal(err)
	}
	handler := NewServer(streams, WithAuthStore(auth), WithAuditStore(auth), WithProfileStore(profiles))
	cookie, csrf := loginForTest(t, handler, "operator", "correct horse battery")
	for _, method := range []string{http.MethodPut, http.MethodDelete} {
		t.Run(method, func(t *testing.T) {
			request := httptest.NewRequest(method, "/youtube/outputs/"+youtube.ID, bytes.NewBufferString(`{}`))
			request.AddCookie(cookie)
			request.Header.Set("X-CSRF-Token", csrf)
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), "youtube_relay_binding_release_pending") {
				t.Fatalf("active claim %s status=%d body=%s", method, response.Code, response.Body.String())
			}
		})
	}
	if _, err := profiles.GetProfile(t.Context(), store.ProfileYouTubeOutput, youtube.ID); err != nil {
		t.Fatalf("active relay claim must retain output profile: %v", err)
	}
}

func TestCompleteDueYouTubeRuntimesCompletesRelayStaticAndReleasesClaim(t *testing.T) {
	streams := store.NewMemoryStreamStore()
	stream, err := streams.CreateStream(t.Context(), "due relay static completion")
	if err != nil {
		t.Fatal(err)
	}
	integrations := store.NewMemoryIntegrationStore()
	provider, err := integrations.CreateOAuthProvider(t.Context(), store.OAuthProvider{
		ProviderType: "google",
		Name:         "YouTube Google",
		Enabled:      true,
		ClientID:     "youtube-client-id",
		ClientSecret: "raw-youtube-client-secret",
		Scopes:       []string{"https://www.googleapis.com/auth/youtube"},
		RedirectURI:  "https://control.example.com/auth/oauth/google/callback",
	})
	if err != nil {
		t.Fatal(err)
	}
	account, err := integrations.CreateOAuthAccount(t.Context(), store.OAuthAccount{
		ProviderID:   provider.ID,
		ProviderType: "google",
		AccountLabel: "youtube account",
		RefreshToken: "raw-youtube-refresh-token",
		Scopes:       []string{"https://www.googleapis.com/auth/youtube"},
	})
	if err != nil {
		t.Fatal(err)
	}
	profiles := store.NewMemoryProfileStore()
	youtube, err := profiles.CreateProfile(t.Context(), store.ProfileYouTubeOutput, "due relay static completion output", map[string]any{
		"mode":                    "live_api_relay_static",
		"oauth_account_id":        account.ID,
		"relay_binding_id":        "relay-00000000-0000-4000-8000-00000000000c",
		"reusable_live_stream_id": "youtube-live-stream-due-complete",
	})
	if err != nil {
		t.Fatal(err)
	}
	stream = setRelayStaticReservationPreconditionsForTest(t, streams, profiles, stream, youtube)
	expectedYouTubeOutputRevision := youtube.YouTubeRelayBindingRevision
	reserved, err := streams.ReserveStreamYouTubeRelayBindingClaim(t.Context(), store.YouTubeRelayBindingClaim{
		RelayBindingID:                "relay-00000000-0000-4000-8000-00000000000c",
		StreamID:                      stream.ID,
		YouTubeOutputID:               youtube.ID,
		ExpectedYouTubeOutputRevision: &expectedYouTubeOutputRevision,
		OAuthAccountID:                account.ID,
		ReusableLiveStreamID:          "youtube-live-stream-due-complete",
	})
	if err != nil {
		t.Fatal(err)
	}
	reserved, err = streams.MarkReservedStreamYouTubeRelayBindingClaimPossiblyPrepared(t.Context(), reserved)
	if err != nil {
		t.Fatalf("mark due static completion fixture possibly prepared: %v", err)
	}
	reserved.BroadcastID = "broadcast-static-due-complete"
	runtime := store.StreamYouTubeRuntime{
		StreamID:       stream.ID,
		YouTubeOutput:  reserved.YouTubeOutputID,
		OAuthAccountID: account.ID,
		Mode:           "live_api_relay_static",
		BroadcastID:    reserved.BroadcastID,
		LiveStreamID:   reserved.ReusableLiveStreamID,
		CompleteOnStop: true,
	}
	if err := streams.FinalizeStreamYouTubeRuntimeAndMarkRelayBindingPrepared(t.Context(), reserved, runtime); err != nil {
		t.Fatal(err)
	}
	if _, err := streams.MarkPreparedStreamYouTubeRelayBindingClaimPossiblyDispatched(t.Context(), reserved); err != nil {
		t.Fatalf("mark due static completion fixture possibly dispatched: %v", err)
	}
	if _, transitioned, err := streams.TransitionStreamStatus(t.Context(), stream.ID, "starting", "completed"); err != nil || !transitioned {
		t.Fatalf("mark due static completion fixture completed: transitioned=%t err=%v", transitioned, err)
	}
	if _, err := streams.RecordStreamYouTubeRuntimeCompleteFailure(t.Context(), stream.ID, "youtube_live_api_complete_failed", time.Now().UTC().Add(-time.Second)); err != nil {
		t.Fatal(err)
	}
	youtubeLive := &fakeYouTubeLiveClient{}
	server := &Server{streams: streams, integrations: integrations, youtubeLive: &relayStaticCompletingYouTubeLiveClient{fakeYouTubeLiveClient: youtubeLive}}
	result, err := server.CompleteDueYouTubeRuntimes(t.Context(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if result["attempted"] != 1 || result["completed"] != 1 || result["failed"] != 0 || youtubeLive.completeCalls != 1 || youtubeLive.completeRequest.BroadcastID != runtime.BroadcastID {
		t.Fatalf("due relay-static completion result=%#v completion=%#v", result, youtubeLive.completeRequest)
	}
	if _, err := streams.GetStreamYouTubeRuntime(t.Context(), stream.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("due relay-static completion must release runtime, err=%v", err)
	}
	if _, err := streams.GetStreamYouTubeRelayBindingClaim(t.Context(), reserved.RelayBindingID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("due relay-static completion must release claim, err=%v", err)
	}
}
