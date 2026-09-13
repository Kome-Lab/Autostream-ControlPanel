package httpapi

import (
	"bytes"
	"context"
	"errors"
	"github.com/example/autostream-control-panel/internal/store"
	ytlive "github.com/example/autostream-control-panel/internal/youtube"
	"net/http"
	"net/http/httptest"
	"testing"
)

type relayStaticStartFixtureForTest struct {
	server         *Server
	streams        *store.MemoryStreamStore
	profiles       *store.MemoryProfileStore
	stream         store.Stream
	relayBindingID string
	discord        store.Profile
	youtube        store.Profile
	dispatcher     *fakeServiceDispatcher
	youtubeLive    *fakeYouTubeLiveClient
	cookie         *http.Cookie
	csrf           string
}

func newRelayStaticStartFixtureForTest(t *testing.T, dispatcher *fakeServiceDispatcher, youtubeLive *fakeYouTubeLiveClient) relayStaticStartFixtureForTest {
	t.Helper()
	if dispatcher == nil {
		dispatcher = &fakeServiceDispatcher{}
	}
	const relayBindingID = "relay-00000000-0000-4000-8000-000000000003"
	const reusableLiveStreamID = "youtube-live-stream-dispatch"
	if youtubeLive == nil {
		youtubeLive = &fakeYouTubeLiveClient{relayStaticPrepared: ytlive.PreparedOutput{BroadcastID: "broadcast-static-dispatch", LiveStreamID: reusableLiveStreamID}}
	}

	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "operator", Roles: []string{"stream_operator"}}, "correct horse battery", []string{"streams.create", "streams.start", "streams.stop"}); err != nil {
		t.Fatal(err)
	}
	streams := store.NewMemoryStreamStore()
	stream, err := streams.CreateStream(t.Context(), "static relay dispatch")
	if err != nil {
		t.Fatal(err)
	}
	registerServiceInstanceWithCapabilities(t, auth, "encoder_recorder-01", "encoder_recorder", map[string]any{
		"output_relay_mode":       "live_api_relay_static",
		"output_relay_binding_id": relayBindingID,
	})
	registerServiceInstance(t, auth, "worker-01", "worker")
	registerServiceInstance(t, auth, "discord_bot-01", "discord_bot")
	for _, serviceID := range []string{"encoder_recorder-01", "worker-01", "discord_bot-01"} {
		if _, err := auth.AssignServiceToStream(t.Context(), serviceID, stream.ID, "test-user"); err != nil {
			t.Fatal(err)
		}
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
	discord := createDiscordConfigForTest(t, profiles, "static relay dispatch discord", "discord_bot-01", "guild-static", "voice-static", "")
	youtube, err := profiles.CreateProfile(t.Context(), store.ProfileYouTubeOutput, "static relay dispatch output", map[string]any{
		"mode":                    "live_api_relay_static",
		"oauth_account_id":        account.ID,
		"relay_binding_id":        relayBindingID,
		"reusable_live_stream_id": reusableLiveStreamID,
	})
	if err != nil {
		t.Fatal(err)
	}
	stream, err = streams.UpdateStreamSettings(t.Context(), stream.ID, store.StreamSettings{YouTubeOutputID: youtube.ID})
	if err != nil {
		t.Fatal(err)
	}
	server := NewServer(streams,
		WithAuthStore(auth),
		WithAuditStore(auth),
		WithServiceRegistryStore(auth),
		WithProfileStore(profiles),
		WithIntegrationStore(integrations),
		WithYouTubeLiveClient(&relayStaticCompletingYouTubeLiveClient{fakeYouTubeLiveClient: youtubeLive}),
		withManualDiscordTargetForTest(t, streams, stream.ID, "1001", "1002", "1003"),
		WithServiceDispatcher(dispatcher),
	)
	cookie, csrf := loginForTest(t, server, "operator", "correct horse battery")
	return relayStaticStartFixtureForTest{
		server:         server,
		streams:        streams,
		profiles:       profiles,
		stream:         stream,
		relayBindingID: relayBindingID,
		discord:        discord,
		youtube:        youtube,
		dispatcher:     dispatcher,
		youtubeLive:    youtubeLive,
		cookie:         cookie,
		csrf:           csrf,
	}
}

// relayStaticReservationBarrierStreamStore lets an HTTP regression mutate the
// durable output configuration exactly after the server has read it but before
// the static binding reservation is attempted. The actual MemoryStreamStore
// performs the reservation, so this exercises the same revision and settings
// fences as the regular in-memory server.
type relayStaticReservationBarrierStreamStore struct {
	*store.MemoryStreamStore
	beforeReserve func()
}

func (s *relayStaticReservationBarrierStreamStore) ReserveStreamYouTubeRelayBindingClaim(ctx context.Context, claim store.YouTubeRelayBindingClaim) (store.YouTubeRelayBindingClaim, error) {
	if before := s.beforeReserve; before != nil {
		s.beforeReserve = nil
		before()
	}
	return s.MemoryStreamStore.ReserveStreamYouTubeRelayBindingClaim(ctx, claim)
}

// relayStaticSelectionBarrierStreamStore changes the selected output after the
// static mode was read but before the post-claim start-wide fence runs. This
// is the dangerous static-to-dynamic window: an implementation that rereads
// the output only in applyYouTubeOutput would otherwise run a different
// provider/encoder path under a static lifecycle claim.
type relayStaticSelectionBarrierStreamStore struct {
	*store.MemoryStreamStore
	afterStaticStartClaim func()
}

func (s *relayStaticSelectionBarrierStreamStore) ClaimStreamStart(ctx context.Context, request store.StreamStartClaimRequest) (store.ClaimedStreamStart, error) {
	claimed, err := s.MemoryStreamStore.ClaimStreamStart(ctx, request)
	if err == nil && s.afterStaticStartClaim != nil {
		after := s.afterStaticStartClaim
		s.afterStaticStartClaim = nil
		after()
	}
	return claimed, err
}

func (s *relayStaticSelectionBarrierStreamStore) TransitionStreamStatus(ctx context.Context, id, from, to string) (store.Stream, bool, error) {
	stream, transitioned, err := s.MemoryStreamStore.TransitionStreamStatus(ctx, id, from, to)
	if transitioned && to == "starting" && s.afterStaticStartClaim != nil {
		after := s.afterStaticStartClaim
		s.afterStaticStartClaim = nil
		after()
	}
	return stream, transitioned, err
}

// relayStaticFinalizeResponseLostStreamStore models a response loss after the
// atomic finalizer committed. The server must verify the exact prepared claim
// and runtime before it sends the Encoder start request.
type relayStaticFinalizeResponseLostStreamStore struct {
	*store.MemoryStreamStore
	responseLost bool
}

func (s *relayStaticFinalizeResponseLostStreamStore) FinalizeStreamYouTubeRuntimeAndMarkRelayBindingPrepared(ctx context.Context, claim store.YouTubeRelayBindingClaim, runtime store.StreamYouTubeRuntime) error {
	if err := s.MemoryStreamStore.FinalizeStreamYouTubeRuntimeAndMarkRelayBindingPrepared(ctx, claim, runtime); err != nil {
		return err
	}
	if s.responseLost {
		s.responseLost = false
		return errors.New("relay static finalizer response lost")
	}
	return nil
}

// relayStaticPrepareMarkerResponseLostStreamStore models the worst safe
// provider handoff boundary: the marker commits but its response is lost before
// the Panel can call PrepareRelayStatic. The server must not issue a second
// Prepare; it must turn the visible possible-prepare reservation into recovery.
type relayStaticPrepareMarkerResponseLostStreamStore struct {
	*store.MemoryStreamStore
	responseLost bool
}

func (s *relayStaticPrepareMarkerResponseLostStreamStore) MarkReservedStreamYouTubeRelayBindingClaimPossiblyPrepared(ctx context.Context, claim store.YouTubeRelayBindingClaim) (store.YouTubeRelayBindingClaim, error) {
	marked, err := s.MemoryStreamStore.MarkReservedStreamYouTubeRelayBindingClaimPossiblyPrepared(ctx, claim)
	if err != nil {
		return store.YouTubeRelayBindingClaim{}, err
	}
	if s.responseLost {
		s.responseLost = false
		return store.YouTubeRelayBindingClaim{}, errors.New("relay static prepare marker response lost")
	}
	return marked, nil
}

// relayStaticDispatchMarkerRepairStreamStore models the nastiest downstream
// handoff outage: the dispatch marker commits, its response and the immediate
// re-read are lost, and the first attempt to mark recovery also fails. The
// explicit recovery endpoint must later normalize the inactive prepared claim
// atomically instead of leaving the reusable binding permanently occupied.
type relayStaticDispatchMarkerRepairStreamStore struct {
	*store.MemoryStreamStore
	markerCommitted         bool
	failPostMarkerRead      bool
	failInitialMarkRecovery bool
}

func (s *relayStaticDispatchMarkerRepairStreamStore) MarkPreparedStreamYouTubeRelayBindingClaimPossiblyDispatched(ctx context.Context, claim store.YouTubeRelayBindingClaim) (store.YouTubeRelayBindingClaim, error) {
	_, err := s.MemoryStreamStore.MarkPreparedStreamYouTubeRelayBindingClaimPossiblyDispatched(ctx, claim)
	if err != nil {
		return store.YouTubeRelayBindingClaim{}, err
	}
	s.markerCommitted = true
	s.failPostMarkerRead = true
	return store.YouTubeRelayBindingClaim{}, errors.New("relay static dispatch marker response lost")
}

func (s *relayStaticDispatchMarkerRepairStreamStore) GetStreamYouTubeRelayBindingClaimForStream(ctx context.Context, streamID string) (store.YouTubeRelayBindingClaim, error) {
	if s.markerCommitted && s.failPostMarkerRead {
		s.failPostMarkerRead = false
		return store.YouTubeRelayBindingClaim{}, errors.New("relay static dispatch marker read unavailable")
	}
	return s.MemoryStreamStore.GetStreamYouTubeRelayBindingClaimForStream(ctx, streamID)
}

func (s *relayStaticDispatchMarkerRepairStreamStore) MarkStreamYouTubeRelayBindingClaimRecoveryRequired(ctx context.Context, claim store.YouTubeRelayBindingClaim) (store.YouTubeRelayBindingClaim, error) {
	if s.failInitialMarkRecovery {
		s.failInitialMarkRecovery = false
		return store.YouTubeRelayBindingClaim{}, errors.New("relay static initial recovery write unavailable")
	}
	return s.MemoryStreamStore.MarkStreamYouTubeRelayBindingClaimRecoveryRequired(ctx, claim)
}

// relayStaticEncoderStopReceiptRecordingStreamStore proves the panel persists
// the Encoder acknowledgement before it releases the static claim. Once the
// provider completion succeeds the claim is intentionally gone, so observing
// this fenced Store call is the only direct regression proof of ordering.
type relayStaticEncoderStopReceiptRecordingStreamStore struct {
	*store.MemoryStreamStore
	encoderStopReceiptCalls int
	lastEncoderStopClaim    store.YouTubeRelayBindingClaim
}

func (s *relayStaticEncoderStopReceiptRecordingStreamStore) MarkPreparedStreamYouTubeRelayBindingClaimEncoderStopConfirmed(ctx context.Context, claim store.YouTubeRelayBindingClaim) (store.YouTubeRelayBindingClaim, error) {
	s.encoderStopReceiptCalls++
	s.lastEncoderStopClaim = claim
	return s.MemoryStreamStore.MarkPreparedStreamYouTubeRelayBindingClaimEncoderStopConfirmed(ctx, claim)
}

func (f relayStaticStartFixtureForTest) start(t *testing.T) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/streams/"+f.stream.ID+"/start", bytes.NewBufferString(`{"discord_config_id":"`+f.discord.ID+`","youtube_output_id":"`+f.youtube.ID+`"}`))
	req.AddCookie(f.cookie)
	req.Header.Set("X-CSRF-Token", f.csrf)
	res := httptest.NewRecorder()
	f.server.ServeHTTP(res, req)
	return res
}

func setRelayStaticReservationPreconditionsForTest(t *testing.T, streams *store.MemoryStreamStore, profiles *store.MemoryProfileStore, stream store.Stream, youtube store.Profile) store.Stream {
	t.Helper()
	profiles.BindStreamYouTubeRelayBindingClaims(streams)
	configured, err := streams.UpdateStreamSettings(t.Context(), stream.ID, store.StreamSettings{YouTubeOutputID: youtube.ID})
	if err != nil {
		t.Fatalf("set relay-static stream output: %v", err)
	}
	starting, transitioned, err := streams.TransitionStreamStatus(t.Context(), configured.ID, configured.Status, "starting")
	if err != nil || !transitioned {
		t.Fatalf("claim relay-static stream lifecycle: transitioned=%t err=%v", transitioned, err)
	}
	return starting
}
