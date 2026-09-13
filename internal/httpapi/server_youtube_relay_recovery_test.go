package httpapi

import (
	"bytes"
	"errors"
	"github.com/example/autostream-control-panel/internal/servicecall"
	"github.com/example/autostream-control-panel/internal/store"
	ytlive "github.com/example/autostream-control-panel/internal/youtube"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestResolveRelayStaticYouTubeRecoveryRequiresConfirmationAndFencedCleanup(t *testing.T) {
	type recoveryFixture struct {
		handler     http.Handler
		streams     *store.MemoryStreamStore
		stream      store.Stream
		claim       store.YouTubeRelayBindingClaim
		youtubeLive *fakeYouTubeLiveClient
		dispatcher  *fakeServiceDispatcher
		cookie      *http.Cookie
		csrf        string
		auth        *store.MemoryAuthStore
	}
	newFixture := func(t *testing.T, relayBindingID, broadcastID string) recoveryFixture {
		t.Helper()
		auth := store.NewMemoryAuthStore()
		if err := auth.AddUser(store.User{Username: "operator", Roles: []string{"stream_operator"}}, "correct horse battery", []string{"streams.stop"}); err != nil {
			t.Fatal(err)
		}
		streams := store.NewMemoryStreamStore()
		stream, err := streams.CreateStream(t.Context(), "relay static recovery")
		if err != nil {
			t.Fatal(err)
		}
		registerServiceInstance(t, auth, "encoder-recorder-01", "encoder_recorder")
		if _, err := auth.AssignServiceToStream(t.Context(), "encoder-recorder-01", stream.ID, "test-user"); err != nil {
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
		youtube, err := profiles.CreateProfile(t.Context(), store.ProfileYouTubeOutput, "relay static recovery output", map[string]any{
			"mode":                    "live_api_relay_static",
			"oauth_account_id":        account.ID,
			"relay_binding_id":        relayBindingID,
			"reusable_live_stream_id": "youtube-live-stream-recovery",
		})
		if err != nil {
			t.Fatal(err)
		}
		stream = setRelayStaticReservationPreconditionsForTest(t, streams, profiles, stream, youtube)
		expectedYouTubeOutputRevision := youtube.YouTubeRelayBindingRevision
		reserved, err := streams.ReserveStreamYouTubeRelayBindingClaim(t.Context(), store.YouTubeRelayBindingClaim{
			RelayBindingID:                relayBindingID,
			StreamID:                      stream.ID,
			YouTubeOutputID:               youtube.ID,
			ExpectedYouTubeOutputRevision: &expectedYouTubeOutputRevision,
			OAuthAccountID:                account.ID,
			ReusableLiveStreamID:          "youtube-live-stream-recovery",
		})
		if err != nil {
			t.Fatal(err)
		}
		reserved.BroadcastID = broadcastID
		reserved.LastError = "youtube_relay_static_prepare_uncertain"
		claim, err := streams.MarkStreamYouTubeRelayBindingClaimRecoveryRequired(t.Context(), reserved)
		if err != nil {
			t.Fatal(err)
		}
		stream, transitioned, err := streams.TransitionStreamStatus(t.Context(), stream.ID, "starting", "failed")
		if err != nil || !transitioned {
			t.Fatalf("terminalize recovery fixture stream: transitioned=%t err=%v", transitioned, err)
		}
		youtubeLive := &fakeYouTubeLiveClient{}
		dispatcher := &fakeServiceDispatcher{}
		handler := NewServer(streams,
			WithAuthStore(auth),
			WithAuditStore(auth),
			WithServiceRegistryStore(auth),
			WithIntegrationStore(integrations),
			WithProfileStore(profiles),
			WithYouTubeLiveClient(youtubeLive),
			WithServiceDispatcher(dispatcher),
		)
		cookie, csrf := loginForTest(t, handler, "operator", "correct horse battery")
		return recoveryFixture{handler: handler, streams: streams, stream: stream, claim: claim, youtubeLive: youtubeLive, dispatcher: dispatcher, cookie: cookie, csrf: csrf, auth: auth}
	}
	resolve := func(t *testing.T, fixture recoveryFixture, body string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, "/streams/"+fixture.stream.ID+"/youtube/relay-static/recovery/resolve", bytes.NewBufferString(body))
		req.AddCookie(fixture.cookie)
		req.Header.Set("X-CSRF-Token", fixture.csrf)
		response := httptest.NewRecorder()
		fixture.handler.ServeHTTP(response, req)
		return response
	}

	t.Run("requires explicit confirmation", func(t *testing.T) {
		fixture := newFixture(t, "relay-00000000-0000-4000-8000-000000000007", youtubeRelayStaticUnknownBroadcastID)
		response := resolve(t, fixture, `{}`)
		if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "youtube_relay_static_external_cleanup_confirmation_required") {
			t.Fatalf("missing confirmation status=%d body=%s", response.Code, response.Body.String())
		}
		if _, err := fixture.streams.GetStreamYouTubeRelayBindingClaim(t.Context(), fixture.claim.RelayBindingID); err != nil {
			t.Fatalf("missing confirmation must retain claim: %v", err)
		}
	})

	t.Run("known broadcast uses confirmed provider delete", func(t *testing.T) {
		fixture := newFixture(t, "relay-00000000-0000-4000-8000-000000000008", "broadcast-static-recovery-known")
		// This claim was durably marked before any Start dispatch. The Encoder's
		// exact no-process reply is therefore a safe receipt, unlike it would be
		// after a possibly-dispatched hand-off.
		fixture.dispatcher.stopResultsOverride = []servicecall.DispatchResult{{
			ServiceID: "encoder-recorder-01", ServiceType: "encoder_recorder", Endpoint: "/stop",
			StatusCode: http.StatusNotFound, Code: "stream_not_running", Success: false,
		}}
		response := resolve(t, fixture, `{"confirm_external_cleanup":true}`)
		if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"cleanup":"provider_delete"`) {
			t.Fatalf("known recovery status=%d body=%s", response.Code, response.Body.String())
		}
		if fixture.youtubeLive.relayStaticCleanupCalls != 1 || fixture.youtubeLive.relayStaticCleanupRequest.BroadcastID != fixture.claim.BroadcastID || fixture.youtubeLive.relayStaticCleanupRequest.Credentials.RefreshToken != "raw-youtube-refresh-token" {
			t.Fatalf("known recovery did not perform the fixed-relay delete: %#v", fixture.youtubeLive)
		}
		if _, err := fixture.streams.GetStreamYouTubeRelayBindingClaim(t.Context(), fixture.claim.RelayBindingID); !errors.Is(err, store.ErrNotFound) {
			t.Fatalf("confirmed provider delete must release recovery claim: %v", err)
		}
		if !hasAuditAction(fixture.auth.AuditEvents(), "streams.youtube_relay_static_recovery.resolve") {
			t.Fatalf("recovery resolution audit is missing: %#v", fixture.auth.AuditEvents())
		}
	})

	t.Run("unknown broadcast requires and records operator attestation", func(t *testing.T) {
		fixture := newFixture(t, "relay-00000000-0000-4000-8000-000000000009", youtubeRelayStaticUnknownBroadcastID)
		response := resolve(t, fixture, `{"confirm_external_cleanup":true}`)
		if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"cleanup":"operator_confirmed_unknown_broadcast"`) {
			t.Fatalf("unknown recovery status=%d body=%s", response.Code, response.Body.String())
		}
		if fixture.youtubeLive.relayStaticCleanupCalls != 0 {
			t.Fatalf("unknown broadcast sentinel must never be sent to provider delete: %#v", fixture.youtubeLive.relayStaticCleanupRequest)
		}
		if _, err := fixture.streams.GetStreamYouTubeRelayBindingClaim(t.Context(), fixture.claim.RelayBindingID); !errors.Is(err, store.ErrNotFound) {
			t.Fatalf("operator-confirmed unknown recovery must release claim: %v", err)
		}
	})
}

// TestResolveRelayStaticYouTubeRecoveryPossiblyDispatchedRequiresEncoderStop
// exercises the durable hand-off fence set immediately before downstream Start.
// A failed Start response is never proof that the Encoder did not receive it:
// recovery must require an Encoder receipt and Complete (never Delete) before
// releasing the binding.
func TestResolveRelayStaticYouTubeRecoveryPossiblyDispatchedRequiresEncoderStop(t *testing.T) {
	newFixture := func(t *testing.T, youtubeLive *fakeYouTubeLiveClient) relayStaticStartFixtureForTest {
		t.Helper()
		fixture := newRelayStaticStartFixtureForTest(t, &fakeServiceDispatcher{failStart: true}, youtubeLive)
		response := fixture.start(t)
		if response.Code != http.StatusBadGateway {
			t.Fatalf("create possibly-dispatched recovery fixture: status=%d body=%s", response.Code, response.Body.String())
		}
		claim, err := fixture.streams.GetStreamYouTubeRelayBindingClaim(t.Context(), fixture.relayBindingID)
		if err != nil || claim.State != store.YouTubeRelayBindingClaimStateRecoveryRequired || claim.DispatchState != store.YouTubeRelayBindingClaimDispatchStatePossiblyDispatched {
			t.Fatalf("start failure must retain a possibly-dispatched recovery claim: claim=%#v err=%v", claim, err)
		}
		return fixture
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

	t.Run("encoder receipt completes without delete", func(t *testing.T) {
		fixture := newFixture(t, nil)
		response := resolve(t, fixture)
		if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"cleanup":"provider_complete"`) {
			t.Fatalf("possibly-dispatched recovery status=%d body=%s", response.Code, response.Body.String())
		}
		if fixture.youtubeLive.completeCalls != 1 || fixture.youtubeLive.relayStaticCleanupCalls != 0 {
			t.Fatalf("possibly-dispatched recovery must Complete and never Delete: youtube=%#v", fixture.youtubeLive)
		}
		if _, err := fixture.streams.GetStreamYouTubeRelayBindingClaim(t.Context(), fixture.relayBindingID); !errors.Is(err, store.ErrNotFound) {
			t.Fatalf("confirmed encoder stop and Complete must release claim: %v", err)
		}
	})

	t.Run("encoder no-process response is insufficient after dispatch hand-off", func(t *testing.T) {
		fixture := newFixture(t, nil)
		fixture.dispatcher.stopResultsOverride = []servicecall.DispatchResult{{
			ServiceID: "encoder_recorder-01", ServiceType: "encoder_recorder", Endpoint: "/stop",
			StatusCode: http.StatusNotFound, Code: "stream_not_running", Success: false,
		}}
		response := resolve(t, fixture)
		if response.Code != http.StatusBadGateway || !strings.Contains(response.Body.String(), "youtube_relay_static_recovery_encoder_stop_unconfirmed") {
			t.Fatalf("possibly-dispatched idle reply status=%d body=%s", response.Code, response.Body.String())
		}
		if fixture.youtubeLive.completeCalls != 0 || fixture.youtubeLive.relayStaticCleanupCalls != 0 {
			t.Fatalf("unconfirmed encoder stop must not touch provider cleanup: youtube=%#v", fixture.youtubeLive)
		}
		if _, err := fixture.streams.GetStreamYouTubeRelayBindingClaim(t.Context(), fixture.relayBindingID); err != nil {
			t.Fatalf("unconfirmed encoder stop must retain claim: %v", err)
		}
	})

	t.Run("encoder stop failure retains claim before provider cleanup", func(t *testing.T) {
		fixture := newFixture(t, nil)
		fixture.dispatcher.failStop = true
		response := resolve(t, fixture)
		if response.Code != http.StatusBadGateway || !strings.Contains(response.Body.String(), "youtube_relay_static_recovery_encoder_stop_unconfirmed") {
			t.Fatalf("failed encoder stop status=%d body=%s", response.Code, response.Body.String())
		}
		if fixture.youtubeLive.completeCalls != 0 || fixture.youtubeLive.relayStaticCleanupCalls != 0 {
			t.Fatalf("failed encoder stop must not touch provider cleanup: youtube=%#v", fixture.youtubeLive)
		}
		if _, err := fixture.streams.GetStreamYouTubeRelayBindingClaim(t.Context(), fixture.relayBindingID); err != nil {
			t.Fatalf("failed encoder stop must retain claim: %v", err)
		}
	})

	t.Run("operator attestation releases known broadcast after reconciled completion failure", func(t *testing.T) {
		fixture := newFixture(t, &fakeYouTubeLiveClient{relayStaticPrepared: ytlive.PreparedOutput{BroadcastID: "broadcast-static-dispatch", LiveStreamID: "youtube-live-stream-dispatch"}, completeErr: errors.New("youtube completion unavailable")})
		response := resolve(t, fixture)
		if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"cleanup":"operator_confirmed_provider_cleanup"`) {
			t.Fatalf("operator-attested completion recovery status=%d body=%s", response.Code, response.Body.String())
		}
		if fixture.youtubeLive.completeCalls != 1 || fixture.youtubeLive.relayStaticCleanupCalls != 0 {
			t.Fatalf("operator-attested completion must never fall back to Delete: youtube=%#v", fixture.youtubeLive)
		}
		if _, err := fixture.streams.GetStreamYouTubeRelayBindingClaim(t.Context(), fixture.relayBindingID); !errors.Is(err, store.ErrNotFound) {
			t.Fatalf("explicit operator attestation with durable Encoder receipt must release claim: %v", err)
		}
		var attested bool
		for _, event := range fixture.server.audit.(*store.MemoryAuthStore).AuditEvents() {
			if event.Action == "streams.youtube_relay_static_recovery.resolve" && event.Metadata != nil {
				attested, _ = event.Metadata["operator_attested_provider_cleanup"].(bool)
			}
		}
		if !attested {
			t.Fatalf("operator-confirmed provider cleanup must be audited: %#v", fixture.server.audit.(*store.MemoryAuthStore).AuditEvents())
		}
	})

	t.Run("worker and bot stop warnings do not replace encoder receipt", func(t *testing.T) {
		fixture := newFixture(t, nil)
		fixture.dispatcher.stopResultsOverride = []servicecall.DispatchResult{
			{ServiceID: "encoder_recorder-01", ServiceType: "encoder_recorder", Endpoint: "/stop", StatusCode: http.StatusAccepted, Success: true},
			{ServiceID: "worker-01", ServiceType: "worker", Endpoint: "/stop", StatusCode: http.StatusBadGateway, Code: "worker_stop_failed", Success: false},
			{ServiceID: "discord_bot-01", ServiceType: "discord_bot", Endpoint: "/stop", StatusCode: http.StatusBadGateway, Code: "bot_stop_failed", Success: false},
		}
		response := resolve(t, fixture)
		if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"cleanup":"provider_complete"`) {
			t.Fatalf("non-encoder warning recovery status=%d body=%s", response.Code, response.Body.String())
		}
		if fixture.youtubeLive.completeCalls != 1 || fixture.youtubeLive.relayStaticCleanupCalls != 0 {
			t.Fatalf("non-encoder warnings must preserve encoder-gated Complete recovery: youtube=%#v", fixture.youtubeLive)
		}
	})

	t.Run("durable encoder receipt survives recovery retry and suppresses stale encoder no-process", func(t *testing.T) {
		youtubeLive := &fakeYouTubeLiveClient{
			relayStaticPrepared: ytlive.PreparedOutput{BroadcastID: "broadcast-static-dispatch", LiveStreamID: "youtube-live-stream-dispatch"},
		}
		fixture := newFixture(t, youtubeLive)
		initialStopCalls := fixture.dispatcher.stopCalls
		// First persist the positive Encoder receipt while the reconciled
		// completion client is unavailable. This retains the recovery claim
		// without using the explicit operator-cleanup attestation branch.
		fixture.server.youtubeLive = youtubeLive
		fixture.dispatcher.stopResultsOverride = []servicecall.DispatchResult{
			{ServiceID: "encoder_recorder-01", ServiceType: "encoder_recorder", Endpoint: "/stop", StatusCode: http.StatusAccepted, Success: true},
			{ServiceID: "worker-01", ServiceType: "worker", Endpoint: "/stop", StatusCode: http.StatusBadGateway, Code: "worker_stop_failed", Success: false},
			{ServiceID: "discord_bot-01", ServiceType: "discord_bot", Endpoint: "/stop", StatusCode: http.StatusBadGateway, Code: "bot_stop_failed", Success: false},
		}
		first := resolve(t, fixture)
		if first.Code != http.StatusServiceUnavailable || !strings.Contains(first.Body.String(), "youtube_relay_static_recovery_cleanup_unavailable") {
			t.Fatalf("receipt persistence recovery status=%d body=%s", first.Code, first.Body.String())
		}
		claim, err := fixture.streams.GetStreamYouTubeRelayBindingClaim(t.Context(), fixture.relayBindingID)
		if err != nil || claim.EncoderStopConfirmedAt.IsZero() {
			t.Fatalf("positive encoder Stop must persist recovery receipt: claim=%#v err=%v", claim, err)
		}
		if fixture.dispatcher.stopCalls != initialStopCalls+1 || youtubeLive.completeCalls != 0 {
			t.Fatalf("first recovery must dispatch one additional stop and retain before completion: initial_stop_calls=%d dispatcher=%#v youtube=%#v", initialStopCalls, fixture.dispatcher, youtubeLive)
		}

		// Simulate a later retry after the Encoder has restarted and only reports
		// its normal no-process 404. The durable first receipt, not this stale
		// response, is what permits the reconciled provider completion.
		fixture.server.youtubeLive = &relayStaticCompletingYouTubeLiveClient{fakeYouTubeLiveClient: youtubeLive}
		fixture.dispatcher.stopResultsOverride = []servicecall.DispatchResult{{
			ServiceID: "encoder_recorder-01", ServiceType: "encoder_recorder", Endpoint: "/stop",
			StatusCode: http.StatusNotFound, Code: "stream_not_running", Success: false,
		}}
		second := resolve(t, fixture)
		if second.Code != http.StatusOK || !strings.Contains(second.Body.String(), `"cleanup":"provider_complete"`) {
			t.Fatalf("durable-receipt retry status=%d body=%s", second.Code, second.Body.String())
		}
		if fixture.dispatcher.stopCalls != initialStopCalls+1 || youtubeLive.completeCalls != 1 || youtubeLive.relayStaticCleanupCalls != 0 {
			t.Fatalf("durable receipt must skip stale stop and never Delete: dispatcher=%#v youtube=%#v", fixture.dispatcher, youtubeLive)
		}
		if _, err := fixture.streams.GetStreamYouTubeRelayBindingClaim(t.Context(), fixture.relayBindingID); !errors.Is(err, store.ErrNotFound) {
			t.Fatalf("reconciled completion must release the durable receipt claim: %v", err)
		}
	})
}
