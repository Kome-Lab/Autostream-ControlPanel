package httpapi

import (
	"errors"
	"fmt"
	"github.com/example/autostream-control-panel/internal/servicecall"
	"github.com/example/autostream-control-panel/internal/store"
	ytlive "github.com/example/autostream-control-panel/internal/youtube"
	"strings"
	"testing"
	"time"
)

func TestApplyYouTubeLiveAPIOutputRejectsEncoderCDNFormatMismatchBeforeProviderPrepare(t *testing.T) {
	integrations := store.NewMemoryIntegrationStore()
	provider, err := integrations.CreateOAuthProvider(t.Context(), store.OAuthProvider{
		ProviderType: "google",
		Name:         "YouTube",
		Enabled:      true,
		ClientID:     "youtube-client-id",
		ClientSecret: "youtube-client-secret",
		RedirectURI:  "https://control.example.test/oauth",
	})
	if err != nil {
		t.Fatal(err)
	}
	account, err := integrations.CreateOAuthAccount(t.Context(), store.OAuthAccount{
		ProviderID: provider.ID, ProviderType: "google", AccountLabel: "youtube", RefreshToken: "youtube-refresh-token", Scopes: []string{"https://www.googleapis.com/auth/youtube"},
	})
	if err != nil {
		t.Fatal(err)
	}
	youtubeClient := &fakeYouTubeLiveClient{prepared: ytlive.PreparedOutput{
		RTMPURL: "rtmps://youtube.example.test/live2", StreamKey: "runtime-key", BroadcastID: "broadcast-4k", LiveStreamID: "live-stream-4k",
	}}
	server := &Server{
		integrations: integrations,
		secrets:      store.NewMemorySecretStore(),
		youtubeLive:  youtubeClient,
	}
	req := &servicecall.StartRequest{EncoderVideoWidth: 1920, EncoderVideoHeight: 1080, EncoderVideoFPS: 60}
	err = server.applyYouTubeLiveAPIOutput(t.Context(), store.Stream{ID: "stream-01", Name: "1080p source"}, store.Profile{
		ID: "youtube-output-01",
		Config: map[string]any{
			"oauth_account_id": account.ID,
			"resolution":       "2160p",
			"frame_rate":       "60fps",
		},
	}, req)
	if !errors.Is(err, errYouTubeOutputVideoFormatMismatch) {
		t.Fatalf("1080p Encoder with 2160p YouTube CDN config error = %v, want %v", err, errYouTubeOutputVideoFormatMismatch)
	}
	if youtubeClient.prepareCalls != 0 {
		t.Fatalf("provider prepare ran before video-format validation: calls=%d request=%#v", youtubeClient.prepareCalls, youtubeClient.prepareRequest)
	}
}

func TestApplyYouTubeLiveAPIOutputBindsFreshStreamToValidatedEncoderFormat(t *testing.T) {
	integrations := store.NewMemoryIntegrationStore()
	provider, err := integrations.CreateOAuthProvider(t.Context(), store.OAuthProvider{
		ProviderType: "google",
		Name:         "YouTube",
		Enabled:      true,
		ClientID:     "youtube-client-id",
		ClientSecret: "youtube-client-secret",
		RedirectURI:  "https://control.example.test/oauth",
	})
	if err != nil {
		t.Fatal(err)
	}
	account, err := integrations.CreateOAuthAccount(t.Context(), store.OAuthAccount{
		ProviderID: provider.ID, ProviderType: "google", AccountLabel: "youtube", RefreshToken: "youtube-refresh-token", Scopes: []string{"https://www.googleapis.com/auth/youtube"},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name       string
		width      int
		height     int
		fps        int
		resolution string
		frameRate  string
	}{
		{name: "1080p60", width: 1920, height: 1080, fps: 60, resolution: "1080p", frameRate: "60fps"},
		{name: "720p30", width: 1280, height: 720, fps: 30, resolution: "720p", frameRate: "30fps"},
		{name: "480p60", width: 854, height: 480, fps: 60, resolution: "480p", frameRate: "60fps"},
	} {
		t.Run(test.name, func(t *testing.T) {
			youtubeClient := &fakeYouTubeLiveClient{prepared: ytlive.PreparedOutput{
				RTMPURL: "rtmps://youtube.example.test/live2", StreamKey: "runtime-key", BroadcastID: "broadcast-" + test.name, LiveStreamID: "live-stream-" + test.name,
			}}
			server := &Server{
				integrations: integrations,
				secrets:      store.NewMemorySecretStore(),
				youtubeLive:  youtubeClient,
			}
			req := &servicecall.StartRequest{EncoderVideoWidth: test.width, EncoderVideoHeight: test.height, EncoderVideoFPS: test.fps}
			err := server.applyYouTubeLiveAPIOutput(t.Context(), store.Stream{ID: "stream-" + test.name, Name: test.name + " source"}, store.Profile{
				ID:     "youtube-output-01",
				Config: map[string]any{"oauth_account_id": account.ID},
			}, req)
			if err != nil {
				t.Fatal(err)
			}
			if youtubeClient.prepareCalls != 1 {
				t.Fatalf("provider prepare calls = %d, want 1", youtubeClient.prepareCalls)
			}
			if got := youtubeClient.prepareRequest.Resolution; got != test.resolution {
				t.Fatalf("provider CDN resolution = %q, want %s", got, test.resolution)
			}
			if got := youtubeClient.prepareRequest.FrameRate; got != test.frameRate {
				t.Fatalf("provider CDN frame rate = %q, want %s", got, test.frameRate)
			}
			if youtubeClient.prepareRequest.ReuseAccountStream {
				t.Fatal("stream-scoped provider ingest unexpectedly enabled account-wide reuse")
			}
		})
	}
}

func TestApplyYouTubeLiveAPIOutputUsesConfiguredReusableStreamKey(t *testing.T) {
	integrations := store.NewMemoryIntegrationStore()
	provider, err := integrations.CreateOAuthProvider(t.Context(), store.OAuthProvider{
		ProviderType: "google", Name: "YouTube", Enabled: true, ClientID: "youtube-client-id", ClientSecret: "youtube-client-secret", RedirectURI: "https://control.example.test/oauth",
	})
	if err != nil {
		t.Fatal(err)
	}
	account, err := integrations.CreateOAuthAccount(t.Context(), store.OAuthAccount{
		ProviderID: provider.ID, ProviderType: "google", AccountLabel: "youtube", RefreshToken: "youtube-refresh-token", Scopes: []string{"https://www.googleapis.com/auth/youtube"},
	})
	if err != nil {
		t.Fatal(err)
	}
	secrets := store.NewMemorySecretStore()
	const secretName = "youtube_stream_key_configured_reusable"
	const configuredKey = "operator-custom-key"
	if _, err := secrets.UpdateSecret(t.Context(), secretName, configuredKey); err != nil {
		t.Fatal(err)
	}
	youtubeClient := &fakeYouTubeLiveClient{prepared: ytlive.PreparedOutput{
		RTMPURL: "rtmps://youtube.example.test/live2", StreamKey: configuredKey, BroadcastID: "broadcast-custom", LiveStreamID: "live-stream-custom",
	}}
	server := &Server{integrations: integrations, secrets: secrets, youtubeLive: youtubeClient}
	req := &servicecall.StartRequest{EncoderVideoWidth: 1920, EncoderVideoHeight: 1080, EncoderVideoFPS: 60}
	profile := store.Profile{ID: "youtube-output-custom", Config: map[string]any{
		"oauth_account_id":          account.ID,
		"stream_key_secret_name":    secretName,
		"use_configured_stream_key": true,
	}}
	if err := server.applyYouTubeLiveAPIOutput(t.Context(), store.Stream{ID: "stream-custom", Name: "custom key source"}, profile, req); err != nil {
		t.Fatal(err)
	}
	if youtubeClient.prepareRequest.PreferredStreamKey != configuredKey {
		t.Fatal("configured reusable stream key was not passed to the provider client")
	}
	if youtubeClient.prepareRequest.ReuseAccountStream {
		t.Fatal("configured key selection must not use heuristic account-stream reuse")
	}
	if got := mapString(req.YouTubeRuntime, "ingest_selection"); got != "configured_reusable_stream" {
		t.Fatalf("ingest selection=%q runtime=%#v", got, req.YouTubeRuntime)
	}
	if strings.Contains(fmt.Sprintf("%#v", req.YouTubeRuntime), configuredKey) {
		t.Fatalf("configured stream key leaked into runtime metadata: %#v", req.YouTubeRuntime)
	}
}

func TestApplyYouTubeLiveAPIOutputResolvesEncoderProfileWithoutVideoCapabilities(t *testing.T) {
	integrations := store.NewMemoryIntegrationStore()
	provider, err := integrations.CreateOAuthProvider(t.Context(), store.OAuthProvider{
		ProviderType: "google",
		Name:         "YouTube",
		Enabled:      true,
		ClientID:     "youtube-client-id",
		ClientSecret: "youtube-client-secret",
		RedirectURI:  "https://control.example.test/oauth",
	})
	if err != nil {
		t.Fatal(err)
	}
	account, err := integrations.CreateOAuthAccount(t.Context(), store.OAuthAccount{
		ProviderID: provider.ID, ProviderType: "google", AccountLabel: "youtube", RefreshToken: "youtube-refresh-token", Scopes: []string{"https://www.googleapis.com/auth/youtube"},
	})
	if err != nil {
		t.Fatal(err)
	}
	profiles := store.NewMemoryProfileStore()
	encoderProfile, err := profiles.CreateProfile(t.Context(), store.ProfileEncoder, "legacy-capability-720p30", map[string]any{
		"width": 1280, "height": 720, "fps": 30,
	})
	if err != nil {
		t.Fatal(err)
	}
	youtubeClient := &fakeYouTubeLiveClient{prepared: ytlive.PreparedOutput{
		RTMPURL: "rtmps://youtube.example.test/live2", StreamKey: "runtime-key", BroadcastID: "broadcast-720p", LiveStreamID: "live-stream-720p",
	}}
	server := &Server{
		profiles:     profiles,
		integrations: integrations,
		secrets:      store.NewMemorySecretStore(),
		youtubeLive:  youtubeClient,
	}
	req := &servicecall.StartRequest{EncoderProfileID: encoderProfile.ID}
	err = server.applyYouTubeLiveAPIOutput(t.Context(), store.Stream{ID: "stream-legacy", Name: "legacy capability"}, store.Profile{
		ID:     "youtube-output-01",
		Config: map[string]any{"oauth_account_id": account.ID},
	}, req)
	if err != nil {
		t.Fatal(err)
	}
	if youtubeClient.prepareRequest.Resolution != "720p" || youtubeClient.prepareRequest.FrameRate != "30fps" {
		t.Fatalf("provider format did not resolve the selected Encoder profile: %#v", youtubeClient.prepareRequest)
	}
}

func TestEnsureYouTubeBroadcastLiveLeavesFutureScheduleToYouTube(t *testing.T) {
	youtubeLive := &transitioningYouTubeLiveClient{fakeYouTubeLiveClient: &fakeYouTubeLiveClient{}}
	server := &Server{youtubeLive: youtubeLive}
	future := time.Now().UTC().Add(time.Hour).Format(time.RFC3339Nano)
	if err := server.ensureYouTubeBroadcastLive(t.Context(), map[string]any{
		"mode":               "live_api",
		"oauth_account_id":   "oauth-account-01",
		"broadcast_id":       "broadcast-01",
		"scheduled_start_at": future,
	}); err != nil {
		t.Fatalf("future scheduled broadcast should not be transitioned immediately: %v", err)
	}
	if youtubeLive.transitionCalls != 0 {
		t.Fatalf("future scheduled broadcast was transitioned immediately: %#v", youtubeLive)
	}
}

func TestEnsureYouTubeBroadcastLiveDefersWhenAutoStartEnabled(t *testing.T) {
	integrations := store.NewMemoryIntegrationStore()
	provider, err := integrations.CreateOAuthProvider(t.Context(), store.OAuthProvider{
		ProviderType: "google",
		Name:         "YouTube Google",
		Enabled:      true,
		ClientID:     "youtube-client-id",
		ClientSecret: "youtube-client-secret",
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
		RefreshToken: "youtube-refresh-token",
		Scopes:       []string{"https://www.googleapis.com/auth/youtube"},
	})
	if err != nil {
		t.Fatal(err)
	}
	youtubeLive := &transitioningYouTubeLiveClient{fakeYouTubeLiveClient: &fakeYouTubeLiveClient{}}
	server := &Server{integrations: integrations, youtubeLive: youtubeLive}

	if err := server.ensureYouTubeBroadcastLive(t.Context(), map[string]any{
		"mode":              "live_api",
		"oauth_account_id":  account.ID,
		"broadcast_id":      "broadcast-01",
		"enable_auto_start": true,
	}); err != nil {
		t.Fatalf("auto-start should defer the provider transition: %v", err)
	}
	if youtubeLive.transitionCalls != 0 {
		t.Fatalf("auto-start path attempted an explicit provider transition: %#v", youtubeLive)
	}
}
