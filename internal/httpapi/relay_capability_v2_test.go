package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/example/autostream-control-panel/internal/store"
)

func relayCapabilityV2InvalidValues() []struct {
	name         string
	capabilities map[string]any
} {
	return []struct {
		name         string
		capabilities map[string]any
	}{
		{"static", map[string]any{"output_relay_mode": "static"}},
		{"legacy_stream_key", map[string]any{"output_relay_mode": "legacy_stream_key"}},
		{"live_api_static", map[string]any{"output_relay_mode": "live_api_static"}},
		{"missing", map[string]any{}},
		{"null", map[string]any{"output_relay_mode": nil}},
		{"bool", map[string]any{"output_relay_mode": true}},
		{"empty", map[string]any{"output_relay_mode": ""}},
		{"unknown", map[string]any{"output_relay_mode": "unsupported"}},
	}
}

func TestRelayCapabilityV2NormalizerAndProfile(t *testing.T) {
	const binding = "relay-00000000-0000-4000-8000-000000000003"
	for _, mode := range []string{"direct", "live_api_relay_static"} {
		if got := normalizedEncoderOutputRelayMode(mode); string(got) != mode {
			t.Fatalf("canonical mode %q became %q", mode, got)
		}
	}
	for _, tc := range relayCapabilityV2InvalidValues() {
		t.Run(tc.name, func(t *testing.T) {
			primary := store.RegisteredService{ServiceType: "encoder_recorder", AssignmentRole: "primary", Capabilities: tc.capabilities}
			if mode, selected := primaryEncoderOutputRelayMode(primary); !selected || mode != encoderOutputRelayModeUnknown {
				t.Fatalf("invalid capability accepted: %q", mode)
			}
			for _, output := range []string{"stream_key", "live_api", "live_api_dry_run", "live_api_relay_static"} {
				if err := validateYouTubeLiveAPIOutputRelayProfile([]store.RegisteredService{primary}, store.Profile{Config: map[string]any{"mode": output, "relay_binding_id": binding}}); err == nil {
					t.Fatalf("invalid capability accepted output %s", output)
				}
			}
		})
	}
	for _, tc := range []struct {
		name, capability, output, profileBinding, encoderBinding string
		want                                                     error
	}{
		{"direct_key", "direct", "stream_key", "", "", nil},
		{"direct_live", "direct", "live_api", "", "", nil},
		{"direct_dry_run", "direct", "live_api_dry_run", "", "", nil},
		{"static", "live_api_relay_static", "live_api_relay_static", binding, binding, nil},
		{"direct_static", "direct", "live_api_relay_static", binding, binding, errYouTubeRelayStaticBindingUnavailable},
		{"missing_binding", "live_api_relay_static", "live_api_relay_static", "", binding, errYouTubeOutputInvalidConfig},
		{"invalid_binding", "live_api_relay_static", "live_api_relay_static", "invalid", binding, errYouTubeOutputInvalidConfig},
		{"mismatch", "live_api_relay_static", "live_api_relay_static", binding, "relay-00000000-0000-4000-8000-000000000004", errYouTubeRelayStaticBindingUnavailable},
		{"missing_encoder_binding", "live_api_relay_static", "live_api_relay_static", binding, "", errYouTubeRelayStaticBindingUnavailable},
		{"static_key", "live_api_relay_static", "stream_key", "", binding, errYouTubeRelayStaticBindingUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			services := []store.RegisteredService{
				{ServiceType: "encoder_recorder", AssignmentRole: "standby", Capabilities: map[string]any{"output_relay_mode": "unsupported"}},
				{ServiceType: "worker", AssignmentRole: "primary", Capabilities: map[string]any{"output_relay_mode": "unsupported"}},
				{ServiceType: "encoder_recorder", AssignmentRole: "primary", Capabilities: map[string]any{"output_relay_mode": tc.capability, "output_relay_binding_id": tc.encoderBinding}},
			}
			err := validateYouTubeLiveAPIOutputRelayProfile(services, store.Profile{Config: map[string]any{"mode": tc.output, "relay_binding_id": tc.profileBinding}})
			if !errors.Is(err, tc.want) {
				t.Fatalf("profile validation=%v want=%v", err, tc.want)
			}
			services[0].Capabilities["output_relay_mode"] = "direct"
			services[1].Capabilities["output_relay_mode"] = "direct"
			services[2].Capabilities = nil
			if err := validateYouTubeLiveAPIOutputRelayProfile(services, store.Profile{Config: map[string]any{"mode": "stream_key"}}); err == nil {
				t.Fatal("standby or worker authorized an invalid primary Encoder")
			}
		})
	}
}

type relayCapabilityV2ClaimStore struct {
	*store.MemoryStreamStore
	claims, reservations int
}

func (s *relayCapabilityV2ClaimStore) ClaimStreamStart(ctx context.Context, request store.StreamStartClaimRequest) (store.ClaimedStreamStart, error) {
	s.claims++
	return s.MemoryStreamStore.ClaimStreamStart(ctx, request)
}
func (s *relayCapabilityV2ClaimStore) ReserveStreamYouTubeRelayBindingClaim(ctx context.Context, claim store.YouTubeRelayBindingClaim) (store.YouTubeRelayBindingClaim, error) {
	s.reservations++
	return s.MemoryStreamStore.ReserveStreamYouTubeRelayBindingClaim(ctx, claim)
}

func TestRelayCapabilityV2InvalidHTTPHasNoSideEffects(t *testing.T) {
	for _, tc := range relayCapabilityV2InvalidValues() {
		t.Run(tc.name, func(t *testing.T) {
			auth := store.NewMemoryAuthStore()
			if err := auth.AddUser(store.User{Username: "operator"}, "correct horse battery", []string{"streams.start"}); err != nil {
				t.Fatal(err)
			}
			streams := &relayCapabilityV2ClaimStore{MemoryStreamStore: store.NewMemoryStreamStore()}
			stream, err := streams.CreateStream(t.Context(), "invalid relay capability")
			if err != nil {
				t.Fatal(err)
			}
			registerServiceInstanceWithCapabilities(t, auth, "encoder_recorder-01", "encoder_recorder", tc.capabilities)
			for _, kind := range []string{"worker", "discord_bot"} {
				registerServiceInstance(t, auth, kind+"-01", kind)
			}
			for _, id := range []string{"encoder_recorder-01", "worker-01", "discord_bot-01"} {
				if _, err := auth.AssignServiceToStream(t.Context(), id, stream.ID, "test-user"); err != nil {
					t.Fatal(err)
				}
			}
			profiles := store.NewMemoryProfileStore()
			discord := createDiscordConfigForTest(t, profiles, "relay v2 discord", "discord_bot-01", "guild", "voice", "")
			secrets := &trackingSecretStore{statuses: []store.SecretStatus{{Name: "youtube_stream_key_v2", Configured: true}}}
			dispatcher := &fakeServiceDispatcher{}
			youtube := &fakeYouTubeLiveClient{}
			server := NewServer(streams, WithAuthStore(auth), WithAuditStore(auth), WithServiceRegistryStore(auth), WithProfileStore(profiles), WithSecretStore(secrets), WithYouTubeLiveClient(youtube), WithServiceDispatcher(dispatcher), withManualDiscordTargetForTest(t, streams.MemoryStreamStore, stream.ID, "1001", "1002", "1003"))
			cookie, csrf := loginForTest(t, server, "operator", "correct horse battery")
			for _, mode := range []string{"stream_key", "live_api", "live_api_dry_run", "live_api_relay_static"} {
				config := map[string]any{"mode": mode}
				wantCode := "live_api_requires_managed_output_relay"
				if mode == "stream_key" {
					config["rtmp_url"] = "rtmps://youtube.example.com/live2"
					config["stream_key_secret_name"] = "youtube_stream_key_v2"
					wantCode = "youtube_output_invalid_config"
				}
				if mode == "live_api_relay_static" {
					config["relay_binding_id"] = "relay-00000000-0000-4000-8000-000000000003"
					config["reusable_live_stream_id"] = "youtube-v2-stream"
					wantCode = "youtube_relay_static_binding_unavailable"
				}
				profile, err := profiles.CreateProfile(t.Context(), store.ProfileYouTubeOutput, mode, config)
				if err != nil {
					t.Fatal(err)
				}
				body, _ := json.Marshal(map[string]any{"youtube_output_id": profile.ID, "discord_config_id": discord.ID})
				for _, route := range []string{"start-readiness", "start"} {
					req := httptest.NewRequest(http.MethodPost, "/streams/"+stream.ID+"/"+route, bytes.NewReader(body))
					req.AddCookie(cookie)
					req.Header.Set("X-CSRF-Token", csrf)
					res := httptest.NewRecorder()
					server.ServeHTTP(res, req)
					if route == "start" && res.Code != http.StatusConflict {
						t.Fatalf("%s/%s status=%d", mode, route, res.Code)
					}
					if route == "start" {
						var result struct {
							Code string `json:"code"`
						}
						if err := json.Unmarshal(res.Body.Bytes(), &result); err != nil {
							t.Fatal(err)
						}
						if result.Code != wantCode {
							t.Fatalf("%s start code=%q want=%q", mode, result.Code, wantCode)
						}
					}
					if route == "start-readiness" {
						var result struct {
							Ready  bool `json:"ready"`
							Issues []struct {
								Code string `json:"code"`
							} `json:"issues"`
						}
						if err := json.Unmarshal(res.Body.Bytes(), &result); err != nil {
							t.Fatal(err)
						}
						if res.Code != http.StatusOK || result.Ready || len(result.Issues) == 0 {
							t.Fatalf("%s readiness did not reject invalid capability", mode)
						}
						if len(result.Issues) != 1 || result.Issues[0].Code != wantCode {
							t.Fatalf("%s readiness issues=%v want=%q", mode, result.Issues, wantCode)
						}
					}
					if streams.claims != 0 || streams.reservations != 0 || secrets.getCalls != 0 || youtube.prepareCalls != 0 || youtube.relayStaticPrepareCalls != 0 || dispatcher.startCalls != 0 {
						t.Fatal("invalid capability reached claim, provider, secret, or dispatch")
					}
				}
			}
			unchanged, err := streams.GetStream(t.Context(), stream.ID)
			if err != nil || unchanged.Status != "created" {
				t.Fatal("invalid capability changed stream state")
			}
		})
	}
}

func TestRelayCapabilityV2CanonicalStaticStart(t *testing.T) {
	fixture := newRelayStaticStartFixtureForTest(t, nil, nil)
	res := fixture.start(t)
	if res.Code != http.StatusOK || fixture.youtubeLive.relayStaticPrepareCalls != 1 || fixture.dispatcher.startCalls != 1 {
		t.Fatalf("canonical static start status=%d", res.Code)
	}
	runtime, err := fixture.streams.GetStreamYouTubeRuntime(t.Context(), fixture.stream.ID)
	if err != nil || runtime.Mode != "live_api_relay_static" || fixture.dispatcher.startRequest.YouTubeRuntime["mode"] != "live_api_relay_static" {
		t.Fatal("canonical static mode was not retained downstream")
	}
}
