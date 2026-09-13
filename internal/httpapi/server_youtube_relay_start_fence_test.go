package httpapi

import (
	"bytes"
	"errors"
	"github.com/example/autostream-control-panel/internal/store"
	ytlive "github.com/example/autostream-control-panel/internal/youtube"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestStartStreamPreparesRelayStaticYouTubeAndReleasesClaimOnlyAfterCompletion(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "operator", Roles: []string{"stream_operator"}}, "correct horse battery", []string{"streams.create", "streams.start", "streams.stop"}); err != nil {
		t.Fatal(err)
	}
	streams := store.NewMemoryStreamStore()
	stream, err := streams.CreateStream(t.Context(), "fixed relay stream")
	if err != nil {
		t.Fatal(err)
	}
	const relayBindingID = "relay-00000000-0000-4000-8000-000000000001"
	const reusableLiveStreamID = "youtube-live-stream-primary"
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
	discord := createDiscordConfigForTest(t, profiles, "fixed relay discord", "discord_bot-01", "guild-static", "voice-static", "")
	youtube, err := profiles.CreateProfile(t.Context(), store.ProfileYouTubeOutput, "fixed relay output", map[string]any{
		"mode":                    "live_api_relay_static",
		"oauth_account_id":        account.ID,
		"relay_binding_id":        relayBindingID,
		"reusable_live_stream_id": reusableLiveStreamID,
		"complete_on_stop":        false,
	})
	if err != nil {
		t.Fatal(err)
	}
	stream, err = streams.UpdateStreamSettings(t.Context(), stream.ID, store.StreamSettings{YouTubeOutputID: youtube.ID})
	if err != nil {
		t.Fatal(err)
	}
	dispatcher := &fakeServiceDispatcher{}
	youtubeLive := &fakeYouTubeLiveClient{relayStaticPrepared: ytlive.PreparedOutput{
		BroadcastID:  "broadcast-static-primary",
		LiveStreamID: reusableLiveStreamID,
	}}
	handler := NewServer(streams,
		WithAuthStore(auth),
		WithAuditStore(auth),
		WithServiceRegistryStore(auth),
		WithProfileStore(profiles),
		WithSecretStore(store.NewMemorySecretStore()),
		WithIntegrationStore(integrations),
		WithYouTubeLiveClient(&relayStaticCompletingYouTubeLiveClient{fakeYouTubeLiveClient: youtubeLive}),
		withManualDiscordTargetForTest(t, streams, stream.ID, "1001", "1002", "1003"),
		WithServiceDispatcher(dispatcher),
	)
	cookie, csrf := loginForTest(t, handler, "operator", "correct horse battery")

	startReq := httptest.NewRequest(http.MethodPost, "/streams/"+stream.ID+"/start", bytes.NewBufferString(`{"discord_config_id":"`+discord.ID+`","youtube_output_id":"`+youtube.ID+`","encoder_rtmp_url":"rtmps://attacker.example.com/live2"}`))
	startReq.AddCookie(cookie)
	startReq.Header.Set("X-CSRF-Token", csrf)
	startRes := httptest.NewRecorder()
	handler.ServeHTTP(startRes, startReq)
	if startRes.Code != http.StatusOK {
		t.Fatalf("start status=%d body=%s", startRes.Code, startRes.Body.String())
	}
	for _, secret := range []string{"raw-youtube-client-secret", "raw-youtube-refresh-token"} {
		if strings.Contains(startRes.Body.String(), secret) {
			t.Fatalf("start response leaked OAuth secret %q: %s", secret, startRes.Body.String())
		}
	}
	if youtubeLive.prepareCalls != 0 || youtubeLive.relayStaticPrepareCalls != 1 {
		t.Fatalf("unexpected prepare calls: live_api=%d relay_static=%d", youtubeLive.prepareCalls, youtubeLive.relayStaticPrepareCalls)
	}
	if youtubeLive.relayStaticRequest.ReusableLiveStreamID != reusableLiveStreamID ||
		youtubeLive.relayStaticRequest.Credentials.RefreshToken != "raw-youtube-refresh-token" ||
		!youtubeLive.relayStaticRequest.EnableAutoStart {
		t.Fatalf("relay static prepare request did not use the bound reusable stream and OAuth account: %#v", youtubeLive.relayStaticRequest)
	}
	if youtubeLive.relayStaticRequest.Resolution != "1080p" || youtubeLive.relayStaticRequest.FrameRate != "60fps" {
		t.Fatalf("relay static prepare request did not use the resolved Encoder format: %#v", youtubeLive.relayStaticRequest)
	}
	if dispatcher.startCalls != 1 || dispatcher.startRequest.EncoderRTMPURL != "" || dispatcher.startRequest.EncoderStreamKeySecretName != "" {
		t.Fatalf("relay static dispatch leaked an ingest endpoint or key: calls=%d request=%#v", dispatcher.startCalls, dispatcher.startRequest)
	}
	runtimeConfig := dispatcher.startRequest.YouTubeRuntime
	if runtimeConfig["mode"] != "live_api_relay_static" || runtimeConfig["relay_binding_id"] != relayBindingID || runtimeConfig["reusable_live_stream_id"] != reusableLiveStreamID || runtimeConfig["watch_url"] != "https://www.youtube.com/watch?v=broadcast-static-primary" || runtimeConfig["complete_on_stop"] != true {
		t.Fatalf("relay static dispatch runtime missing specialized values: %#v", runtimeConfig)
	}
	for _, key := range []string{"rtmp_url", "stream_key", "stream_key_secret_name"} {
		if value, ok := runtimeConfig[key]; ok {
			t.Fatalf("relay static dispatch runtime leaked %s=%#v: %#v", key, value, runtimeConfig)
		}
	}

	runtime, err := streams.GetStreamYouTubeRuntime(t.Context(), stream.ID)
	if err != nil {
		t.Fatalf("get relay static runtime: %v", err)
	}
	if runtime.Mode != "live_api_relay_static" || runtime.YouTubeOutput != youtube.ID || runtime.OAuthAccountID != account.ID || runtime.BroadcastID != "broadcast-static-primary" || runtime.LiveStreamID != reusableLiveStreamID || runtime.RTMPURL != "" || runtime.StreamKeySecretName != "" || !runtime.CompleteOnStop {
		t.Fatalf("unexpected relay static runtime: %#v", runtime)
	}
	claim, err := streams.GetStreamYouTubeRelayBindingClaim(t.Context(), relayBindingID)
	if err != nil {
		t.Fatalf("get relay static claim: %v", err)
	}
	if claim.State != store.YouTubeRelayBindingClaimStatePrepared || claim.StreamID != stream.ID || claim.YouTubeOutputID != youtube.ID || claim.OAuthAccountID != account.ID || claim.ReusableLiveStreamID != reusableLiveStreamID || claim.BroadcastID != runtime.BroadcastID {
		t.Fatalf("unexpected prepared relay static claim: %#v", claim)
	}

	stopReq := httptest.NewRequest(http.MethodPost, "/streams/"+stream.ID+"/stop", nil)
	stopReq.AddCookie(cookie)
	stopReq.Header.Set("X-CSRF-Token", csrf)
	stopRes := httptest.NewRecorder()
	handler.ServeHTTP(stopRes, stopReq)
	if stopRes.Code != http.StatusOK {
		t.Fatalf("stop status=%d body=%s", stopRes.Code, stopRes.Body.String())
	}
	if youtubeLive.completeCalls != 1 || youtubeLive.completeRequest.BroadcastID != runtime.BroadcastID || youtubeLive.completeRequest.Credentials.RefreshToken != "raw-youtube-refresh-token" {
		t.Fatalf("relay static completion did not finish the prepared broadcast: %#v", youtubeLive.completeRequest)
	}
	if _, err := streams.GetStreamYouTubeRuntime(t.Context(), stream.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("relay static runtime must be released only after provider completion, err=%v", err)
	}
	if _, err := streams.GetStreamYouTubeRelayBindingClaim(t.Context(), relayBindingID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("relay static claim must be released only after provider completion, err=%v", err)
	}
}

func TestStartStreamRelayStaticRejectsNoncanonicalBindingBeforeClaimPrepareOrDispatch(t *testing.T) {
	fixture := newRelayStaticStartFixtureForTest(t, nil, nil)
	const rawBindingID = "yt-stream-key-like-value"
	if _, err := fixture.profiles.UpdateProfile(t.Context(), store.ProfileYouTubeOutput, fixture.youtube.ID, fixture.youtube.Name, map[string]any{
		"mode":                    "live_api_relay_static",
		"oauth_account_id":        configString(fixture.youtube.Config, "oauth_account_id"),
		"relay_binding_id":        rawBindingID,
		"reusable_live_stream_id": configString(fixture.youtube.Config, "reusable_live_stream_id"),
	}); err != nil {
		t.Fatal(err)
	}
	res := fixture.start(t)
	if res.Code != http.StatusConflict || !strings.Contains(res.Body.String(), "youtube_output_invalid_config") {
		t.Fatalf("noncanonical relay binding start status=%d body=%s", res.Code, res.Body.String())
	}
	if fixture.youtubeLive.prepareCalls != 0 || fixture.youtubeLive.relayStaticPrepareCalls != 0 {
		t.Fatalf("noncanonical relay binding called YouTube Prepare: %#v", fixture.youtubeLive)
	}
	if fixture.dispatcher.startCalls != 0 {
		t.Fatalf("noncanonical relay binding dispatched the Encoder: %#v", fixture.dispatcher.startRequest)
	}
	if _, err := fixture.streams.GetStreamYouTubeRelayBindingClaim(t.Context(), rawBindingID); !errors.Is(err, store.ErrInvalidYouTubeRelayBindingClaim) {
		t.Fatalf("noncanonical relay binding lookup must fail strict validation: %v", err)
	}
	if _, err := fixture.streams.GetStreamYouTubeRelayBindingClaim(t.Context(), fixture.relayBindingID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("noncanonical relay binding created a claim: %v", err)
	}
	unchanged, err := fixture.streams.GetStream(t.Context(), fixture.stream.ID)
	if err != nil {
		t.Fatal(err)
	}
	if unchanged.Status != "created" {
		t.Fatalf("noncanonical relay binding transitioned the stream: %q", unchanged.Status)
	}
}

func TestStartStreamRelayStaticRejectsOutputChangesBeforeClaimReservation(t *testing.T) {
	for _, tt := range []struct {
		name       string
		wantStatus string
		mutate     func(t *testing.T, fixture relayStaticStartFixtureForTest) error
	}{
		{
			name:       "output profile revision changes",
			wantStatus: "failed",
			mutate: func(t *testing.T, fixture relayStaticStartFixtureForTest) error {
				_, err := fixture.profiles.UpdateProfile(t.Context(), store.ProfileYouTubeOutput, fixture.youtube.ID, "static relay dispatch output changed", map[string]any{
					"mode":                    "live_api_relay_static",
					"oauth_account_id":        configString(fixture.youtube.Config, "oauth_account_id"),
					"relay_binding_id":        fixture.relayBindingID,
					"reusable_live_stream_id": "youtube-live-stream-dispatch",
				})
				return err
			},
		},
		{
			name:       "persisted stream output changes",
			wantStatus: "starting",
			mutate: func(t *testing.T, fixture relayStaticStartFixtureForTest) error {
				alternate, err := fixture.profiles.CreateProfile(t.Context(), store.ProfileYouTubeOutput, "alternate static relay output", map[string]any{
					"mode":                    "live_api_relay_static",
					"oauth_account_id":        configString(fixture.youtube.Config, "oauth_account_id"),
					"relay_binding_id":        "relay-00000000-0000-4000-8000-000000000004",
					"reusable_live_stream_id": "youtube-live-stream-alternate",
				})
				if err != nil {
					return err
				}
				_, err = fixture.streams.UpdateStreamSettings(t.Context(), fixture.stream.ID, store.StreamSettings{YouTubeOutputID: alternate.ID})
				return err
			},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			fixture := newRelayStaticStartFixtureForTest(t, nil, nil)
			barrier := &relayStaticReservationBarrierStreamStore{MemoryStreamStore: fixture.streams}
			fixture.server.streams = barrier
			var mutationErr error
			barrier.beforeReserve = func() {
				mutationErr = tt.mutate(t, fixture)
			}

			response := fixture.start(t)
			if mutationErr != nil {
				t.Fatalf("reservation barrier mutation: %v", mutationErr)
			}
			if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), errYouTubeRelayStaticConfigChanged.Error()) {
				t.Fatalf("stale static output must request reload before prepare: status=%d body=%s", response.Code, response.Body.String())
			}
			if fixture.youtubeLive.prepareCalls != 0 || fixture.youtubeLive.relayStaticPrepareCalls != 0 || fixture.dispatcher.startCalls != 0 {
				t.Fatalf("stale static output must not prepare or dispatch: youtube=%#v dispatcher=%#v", fixture.youtubeLive, fixture.dispatcher)
			}
			if _, err := fixture.streams.GetStreamYouTubeRelayBindingClaim(t.Context(), fixture.relayBindingID); !errors.Is(err, store.ErrNotFound) {
				t.Fatalf("stale static output must not reserve a recovery claim: %v", err)
			}
			if _, err := fixture.streams.GetStreamYouTubeRuntime(t.Context(), fixture.stream.ID); !errors.Is(err, store.ErrNotFound) {
				t.Fatalf("stale static output must not create a runtime: %v", err)
			}
			stream, err := fixture.streams.GetStream(t.Context(), fixture.stream.ID)
			if err != nil || stream.Status != tt.wantStatus {
				t.Fatalf("claim-fenced compensation status=%q want=%q stream=%#v err=%v", stream.Status, tt.wantStatus, stream, err)
			}
		})
	}
}

func TestStartStreamRelayStaticRejectsModeChangesAfterSelectionBeforePrepareOrDispatch(t *testing.T) {
	for _, tt := range []struct {
		name   string
		config func(fixture relayStaticStartFixtureForTest) map[string]any
	}{
		{
			name: "live api",
			config: func(fixture relayStaticStartFixtureForTest) map[string]any {
				return map[string]any{
					"mode":             "live_api",
					"oauth_account_id": configString(fixture.youtube.Config, "oauth_account_id"),
				}
			},
		},
		{
			name: "stream key",
			config: func(fixture relayStaticStartFixtureForTest) map[string]any {
				return map[string]any{
					"mode":                   "stream_key",
					"rtmp_url":               "rtmps://a.rtmps.youtube.com/live2",
					"stream_key_secret_name": "youtube_stream_key_mode_change",
				}
			},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			fixture := newRelayStaticStartFixtureForTest(t, nil, nil)
			barrier := &relayStaticSelectionBarrierStreamStore{MemoryStreamStore: fixture.streams}
			fixture.server.streams = barrier
			var mutationErr error
			barrier.afterStaticStartClaim = func() {
				_, mutationErr = fixture.profiles.UpdateProfile(t.Context(), store.ProfileYouTubeOutput, fixture.youtube.ID, "static relay output mode changed", tt.config(fixture))
			}

			response := fixture.start(t)
			if mutationErr != nil {
				t.Fatalf("selection barrier mutation: %v", mutationErr)
			}
			if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), errYouTubeRelayStaticConfigChanged.Error()) {
				t.Fatalf("static mode change must request reload before prepare: status=%d body=%s", response.Code, response.Body.String())
			}
			if fixture.youtubeLive.prepareCalls != 0 || fixture.youtubeLive.relayStaticPrepareCalls != 0 || fixture.dispatcher.startCalls != 0 {
				t.Fatalf("static mode change must not prepare or dispatch: youtube=%#v dispatcher=%#v", fixture.youtubeLive, fixture.dispatcher)
			}
			if _, err := fixture.streams.GetStreamYouTubeRelayBindingClaim(t.Context(), fixture.relayBindingID); !errors.Is(err, store.ErrNotFound) {
				t.Fatalf("static mode change must not reserve a claim: %v", err)
			}
			if _, err := fixture.streams.GetStreamYouTubeRuntime(t.Context(), fixture.stream.ID); !errors.Is(err, store.ErrNotFound) {
				t.Fatalf("static mode change must not create a runtime: %v", err)
			}
			stream, err := fixture.streams.GetStream(t.Context(), fixture.stream.ID)
			if err != nil || stream.Status != "failed" {
				t.Fatalf("static mode change must converge claimed lifecycle to failed: stream=%#v err=%v", stream, err)
			}
		})
	}
}

// TestStartStreamRelayStaticRejectsModeChangesAfterPrechecksBeforeStaticFence
// covers the earlier race where capability/readiness checks had accepted the
// selected static output but another writer changed it before the first static
// lifecycle fence. The snapshot must remain static and fail closed rather than
// rerunning a dynamic Prepare path under the old static authorization.
func TestStartStreamRelayStaticRejectsModeChangesAfterPrechecksBeforeStaticFence(t *testing.T) {
	for _, tt := range []struct {
		name   string
		config func(fixture relayStaticStartFixtureForTest) map[string]any
	}{
		{
			name: "live api",
			config: func(fixture relayStaticStartFixtureForTest) map[string]any {
				return map[string]any{
					"mode":             "live_api",
					"oauth_account_id": configString(fixture.youtube.Config, "oauth_account_id"),
				}
			},
		},
		{
			name: "stream key",
			config: func(fixture relayStaticStartFixtureForTest) map[string]any {
				return map[string]any{
					"mode":                   "stream_key",
					"rtmp_url":               "rtmps://a.rtmps.youtube.com/live2",
					"stream_key_secret_name": "youtube_stream_key_mode_change",
				}
			},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			fixture := newRelayStaticStartFixtureForTest(t, nil, nil)
			var mutationErr error
			fixture.server.dispatcher = &relayStaticPreFenceMutationDispatcher{
				fakeServiceDispatcher: fixture.dispatcher,
				mutate: func() {
					_, mutationErr = fixture.profiles.UpdateProfile(t.Context(), store.ProfileYouTubeOutput, fixture.youtube.ID, "static relay output mode changed during precheck", tt.config(fixture))
				},
			}

			response := fixture.start(t)
			if mutationErr != nil {
				t.Fatalf("pre-fence mutation: %v", mutationErr)
			}
			if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), errYouTubeRelayStaticConfigChanged.Error()) {
				t.Fatalf("static precheck mode change must request reload before prepare: status=%d body=%s", response.Code, response.Body.String())
			}
			if fixture.youtubeLive.prepareCalls != 0 || fixture.youtubeLive.relayStaticPrepareCalls != 0 || fixture.dispatcher.startCalls != 0 {
				t.Fatalf("static precheck mode change must not prepare or dispatch: youtube=%#v dispatcher=%#v", fixture.youtubeLive, fixture.dispatcher)
			}
			if _, err := fixture.streams.GetStreamYouTubeRelayBindingClaim(t.Context(), fixture.relayBindingID); !errors.Is(err, store.ErrNotFound) {
				t.Fatalf("static precheck mode change must not reserve a claim: %v", err)
			}
			if _, err := fixture.streams.GetStreamYouTubeRuntime(t.Context(), fixture.stream.ID); !errors.Is(err, store.ErrNotFound) {
				t.Fatalf("static precheck mode change must not create a runtime: %v", err)
			}
			stream, err := fixture.streams.GetStream(t.Context(), fixture.stream.ID)
			if err != nil || stream.Status != "failed" {
				t.Fatalf("static precheck mode change must terminalize the static lifecycle: stream=%#v err=%v", stream, err)
			}
		})
	}
}

// TestStartStreamRelayStaticRejectsDynamicOutputOverrideBeforePrepareOrDispatch
// closes the request-level variant of the static-to-dynamic race: a caller
// cannot supply a different dynamic output while the persisted stream still
// owns a fixed relay binding.
func TestStartStreamRelayStaticRejectsDynamicOutputOverrideBeforePrepareOrDispatch(t *testing.T) {
	fixture := newRelayStaticStartFixtureForTest(t, nil, nil)
	alternate, err := fixture.profiles.CreateProfile(t.Context(), store.ProfileYouTubeOutput, "dynamic override output", map[string]any{
		"mode":             "live_api",
		"oauth_account_id": configString(fixture.youtube.Config, "oauth_account_id"),
	})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/streams/"+fixture.stream.ID+"/start", bytes.NewBufferString(`{"discord_config_id":"`+fixture.discord.ID+`","youtube_output_id":"`+alternate.ID+`"}`))
	req.AddCookie(fixture.cookie)
	req.Header.Set("X-CSRF-Token", fixture.csrf)
	response := httptest.NewRecorder()
	fixture.server.ServeHTTP(response, req)
	if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), errYouTubeRelayStaticConfigChanged.Error()) {
		t.Fatalf("dynamic output override status=%d body=%s", response.Code, response.Body.String())
	}
	if fixture.youtubeLive.prepareCalls != 0 || fixture.youtubeLive.relayStaticPrepareCalls != 0 || fixture.dispatcher.startCalls != 0 {
		t.Fatalf("dynamic output override must not prepare or dispatch: youtube=%#v dispatcher=%#v", fixture.youtubeLive, fixture.dispatcher)
	}
	if _, err := fixture.streams.GetStreamYouTubeRuntime(t.Context(), fixture.stream.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("dynamic output override must not create runtime: %v", err)
	}
	if _, err := fixture.streams.GetStreamYouTubeRelayBindingClaim(t.Context(), fixture.relayBindingID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("dynamic output override must not claim the fixed binding: %v", err)
	}
	stream, err := fixture.streams.GetStream(t.Context(), fixture.stream.ID)
	if err != nil || stream.Status != "created" {
		t.Fatalf("dynamic output override must fail before lifecycle claim: stream=%#v err=%v", stream, err)
	}
}
