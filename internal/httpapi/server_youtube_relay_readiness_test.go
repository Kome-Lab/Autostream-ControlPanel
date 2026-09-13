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

func TestStartStreamRejectsRelayStaticBindingMismatchBeforeClaimReservePrepareOrDispatch(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "operator", Roles: []string{"stream_operator"}}, "correct horse battery", []string{"streams.create", "streams.start"}); err != nil {
		t.Fatal(err)
	}
	streams := store.NewMemoryStreamStore()
	stream, err := streams.CreateStream(t.Context(), "mismatched fixed relay stream")
	if err != nil {
		t.Fatal(err)
	}
	registerServiceInstanceWithCapabilities(t, auth, "encoder_recorder-01", "encoder_recorder", map[string]any{
		"output_relay_mode":       "live_api_relay_static",
		"output_relay_binding_id": "relay-00000000-0000-4000-8000-000000000002",
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
	discord := createDiscordConfigForTest(t, profiles, "mismatched fixed relay discord", "discord_bot-01", "guild-static", "voice-static", "")
	youtube, err := profiles.CreateProfile(t.Context(), store.ProfileYouTubeOutput, "mismatched fixed relay output", map[string]any{
		"mode":                    "live_api_relay_static",
		"oauth_account_id":        account.ID,
		"relay_binding_id":        "relay-00000000-0000-4000-8000-000000000001",
		"reusable_live_stream_id": "youtube-live-stream-primary",
	})
	if err != nil {
		t.Fatal(err)
	}
	dispatcher := &fakeServiceDispatcher{}
	youtubeLive := &fakeYouTubeLiveClient{relayStaticPrepared: ytlive.PreparedOutput{BroadcastID: "broadcast-static-primary", LiveStreamID: "youtube-live-stream-primary"}}
	handler := NewServer(streams,
		WithAuthStore(auth),
		WithAuditStore(auth),
		WithServiceRegistryStore(auth),
		WithProfileStore(profiles),
		WithIntegrationStore(integrations),
		WithYouTubeLiveClient(youtubeLive),
		withManualDiscordTargetForTest(t, streams, stream.ID, "1001", "1002", "1003"),
		WithServiceDispatcher(dispatcher),
	)
	cookie, csrf := loginForTest(t, handler, "operator", "correct horse battery")
	request := httptest.NewRequest(http.MethodPost, "/streams/"+stream.ID+"/start", bytes.NewBufferString(`{"discord_config_id":"`+discord.ID+`","youtube_output_id":"`+youtube.ID+`"}`))
	request.AddCookie(cookie)
	request.Header.Set("X-CSRF-Token", csrf)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), "youtube_relay_static_binding_unavailable") {
		t.Fatalf("expected fixed binding rejection, status=%d body=%s", response.Code, response.Body.String())
	}
	if youtubeLive.prepareCalls != 0 || youtubeLive.relayStaticPrepareCalls != 0 || dispatcher.startCalls != 0 {
		t.Fatalf("binding mismatch must reject before provider prepare or dispatch: live_api=%d relay_static=%d dispatch=%d", youtubeLive.prepareCalls, youtubeLive.relayStaticPrepareCalls, dispatcher.startCalls)
	}
	if _, err := streams.GetStreamYouTubeRelayBindingClaim(t.Context(), "relay-00000000-0000-4000-8000-000000000001"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("binding mismatch must reject before reserving a claim, err=%v", err)
	}
	unchanged, err := streams.GetStream(t.Context(), stream.ID)
	if err != nil || unchanged.Status != "created" {
		t.Fatalf("binding mismatch changed stream state: stream=%#v err=%v", unchanged, err)
	}
}

func TestStartStreamStaticRelayEncoderRejectsNonStaticOrMissingYouTubeOutputBeforeProviderOrDispatch(t *testing.T) {
	for _, tt := range []struct {
		name   string
		mutate func(t *testing.T, fixture relayStaticStartFixtureForTest)
		body   func(fixture relayStaticStartFixtureForTest) string
	}{
		{
			name: "stream key output",
			mutate: func(t *testing.T, fixture relayStaticStartFixtureForTest) {
				t.Helper()
				if _, err := fixture.profiles.UpdateProfile(t.Context(), store.ProfileYouTubeOutput, fixture.youtube.ID, "dynamic stream key output", map[string]any{
					"mode":                   "stream_key",
					"rtmp_url":               "rtmps://a.rtmps.youtube.com/live2",
					"stream_key_secret_name": "youtube_stream_key_static_guard",
				}); err != nil {
					t.Fatal(err)
				}
			},
			body: func(fixture relayStaticStartFixtureForTest) string {
				return `{"discord_config_id":"` + fixture.discord.ID + `","youtube_output_id":"` + fixture.youtube.ID + `"}`
			},
		},
		{
			name: "live api dry run output",
			mutate: func(t *testing.T, fixture relayStaticStartFixtureForTest) {
				t.Helper()
				if _, err := fixture.profiles.UpdateProfile(t.Context(), store.ProfileYouTubeOutput, fixture.youtube.ID, "dynamic dry run output", map[string]any{
					"mode": "live_api_dry_run",
				}); err != nil {
					t.Fatal(err)
				}
			},
			body: func(fixture relayStaticStartFixtureForTest) string {
				return `{"discord_config_id":"` + fixture.discord.ID + `","youtube_output_id":"` + fixture.youtube.ID + `"}`
			},
		},
		{
			name: "missing output",
			mutate: func(t *testing.T, fixture relayStaticStartFixtureForTest) {
				t.Helper()
				if _, err := fixture.streams.UpdateStreamSettings(t.Context(), fixture.stream.ID, store.StreamSettings{}); err != nil {
					t.Fatal(err)
				}
			},
			body: func(fixture relayStaticStartFixtureForTest) string {
				return `{"discord_config_id":"` + fixture.discord.ID + `"}`
			},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			fixture := newRelayStaticStartFixtureForTest(t, nil, nil)
			tt.mutate(t, fixture)
			req := httptest.NewRequest(http.MethodPost, "/streams/"+fixture.stream.ID+"/start", bytes.NewBufferString(tt.body(fixture)))
			req.AddCookie(fixture.cookie)
			req.Header.Set("X-CSRF-Token", fixture.csrf)
			response := httptest.NewRecorder()
			fixture.server.ServeHTTP(response, req)
			if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), errYouTubeRelayStaticBindingUnavailable.Error()) {
				t.Fatalf("static relay Encoder %s status=%d body=%s", tt.name, response.Code, response.Body.String())
			}
			if fixture.youtubeLive.prepareCalls != 0 || fixture.youtubeLive.relayStaticPrepareCalls != 0 || fixture.dispatcher.startCalls != 0 {
				t.Fatalf("static relay Encoder %s must reject before provider/dispatch: youtube=%#v dispatcher=%#v", tt.name, fixture.youtubeLive, fixture.dispatcher)
			}
			if _, err := fixture.streams.GetStreamYouTubeRelayBindingClaim(t.Context(), fixture.relayBindingID); !errors.Is(err, store.ErrNotFound) {
				t.Fatalf("static relay Encoder %s must not reserve a static claim: %v", tt.name, err)
			}
			stream, err := fixture.streams.GetStream(t.Context(), fixture.stream.ID)
			if err != nil || stream.Status != "created" {
				t.Fatalf("static relay Encoder %s must leave stream unstarted: stream=%#v err=%v", tt.name, stream, err)
			}
		})
	}
}

func TestApplyRelayStaticYouTubePreservesRecoveryClaimWhenBindCleanupIsUncertain(t *testing.T) {
	streams := store.NewMemoryStreamStore()
	stream, err := streams.CreateStream(t.Context(), "uncertain fixed relay stream")
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
	youtube, err := profiles.CreateProfile(t.Context(), store.ProfileYouTubeOutput, "uncertain fixed relay output", map[string]any{
		"mode":                    "live_api_relay_static",
		"oauth_account_id":        account.ID,
		"relay_binding_id":        "relay-00000000-0000-4000-8000-000000000005",
		"reusable_live_stream_id": "youtube-live-stream-uncertain",
	})
	if err != nil {
		t.Fatal(err)
	}
	stream = setRelayStaticReservationPreconditionsForTest(t, streams, profiles, stream, youtube)
	youtubeLive := &fakeYouTubeLiveClient{relayStaticPrepareErr: &ytlive.RelayStaticBindError{
		BroadcastID:      "broadcast-static-uncertain",
		LiveStreamID:     "youtube-live-stream-uncertain",
		CleanupConfirmed: false,
	}}
	server := &Server{streams: streams, profiles: profiles, integrations: integrations, youtubeLive: youtubeLive}
	request := &servicecall.StartRequest{YouTubeOutputID: youtube.ID}
	err = server.applyYouTubeRelayStaticOutput(t.Context(), stream, youtube, store.StreamStartOwnershipClaim{}, request)
	if !errors.Is(err, errYouTubeRelayStaticRecoveryRequired) {
		t.Fatalf("uncertain bind error=%v, want recovery-required", err)
	}
	if youtubeLive.relayStaticPrepareCalls != 1 {
		t.Fatalf("relay-static provider was not called once: %d", youtubeLive.relayStaticPrepareCalls)
	}
	claim, err := streams.GetStreamYouTubeRelayBindingClaim(t.Context(), "relay-00000000-0000-4000-8000-000000000005")
	if err != nil {
		t.Fatalf("uncertain bind claim was not preserved: %v", err)
	}
	if claim.State != store.YouTubeRelayBindingClaimStateRecoveryRequired || claim.BroadcastID != "broadcast-static-uncertain" || claim.LastError != ytlive.ErrRelayStaticBindCleanupUncertain.Error() {
		t.Fatalf("unexpected recovery claim: %#v", claim)
	}
	if _, err := streams.GetStreamYouTubeRuntime(t.Context(), stream.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("uncertain bind must not create a runtime, err=%v", err)
	}
}

func TestApplyRelayStaticYouTubeRetainsEveryPostPrepareFenceFailure(t *testing.T) {
	for _, tt := range []struct {
		name            string
		prepareErr      error
		wantBroadcastID string
	}{
		{
			name:            "unknown transport result remains fenced",
			prepareErr:      errors.New("youtube insert response lost"),
			wantBroadcastID: youtubeRelayStaticUnknownBroadcastID,
		},
		{
			name:            "known reusable stream validation remains fenced after marker",
			prepareErr:      ytlive.ErrReusableLiveStreamNotFound,
			wantBroadcastID: youtubeRelayStaticUnknownBroadcastID,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			streams := store.NewMemoryStreamStore()
			stream, err := streams.CreateStream(t.Context(), "relay static prepare classification")
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
			youtube, err := profiles.CreateProfile(t.Context(), store.ProfileYouTubeOutput, "relay static output", map[string]any{
				"mode":                    "live_api_relay_static",
				"oauth_account_id":        account.ID,
				"relay_binding_id":        "relay-00000000-0000-4000-8000-000000000006",
				"reusable_live_stream_id": "youtube-live-stream-prepare-classification",
			})
			if err != nil {
				t.Fatal(err)
			}
			stream = setRelayStaticReservationPreconditionsForTest(t, streams, profiles, stream, youtube)
			server := &Server{
				streams:      streams,
				profiles:     profiles,
				integrations: integrations,
				youtubeLive:  &fakeYouTubeLiveClient{relayStaticPrepareErr: tt.prepareErr},
			}
			err = server.applyYouTubeRelayStaticOutput(t.Context(), stream, youtube, store.StreamStartOwnershipClaim{}, &servicecall.StartRequest{YouTubeOutputID: youtube.ID})
			if !errors.Is(err, errYouTubeRelayStaticRecoveryRequired) {
				t.Fatalf("prepare error=%v, want recovery-required", err)
			}
			claim, claimErr := streams.GetStreamYouTubeRelayBindingClaim(t.Context(), "relay-00000000-0000-4000-8000-000000000006")
			if claimErr != nil {
				t.Fatalf("post-marker prepare result must retain recovery claim: %v", claimErr)
			}
			if claim.State != store.YouTubeRelayBindingClaimStateRecoveryRequired || claim.BroadcastID != tt.wantBroadcastID || claim.LastError != "youtube_relay_static_prepare_uncertain" {
				t.Fatalf("unexpected post-marker recovery claim: %#v", claim)
			}
			if _, err := streams.GetStreamYouTubeRuntime(t.Context(), stream.ID); !errors.Is(err, store.ErrNotFound) {
				t.Fatalf("post-marker prepare result must not create a runtime, err=%v", err)
			}
		})
	}
}
