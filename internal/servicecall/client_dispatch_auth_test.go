package servicecall

import (
	"github.com/example/autostream-control-panel/internal/netpolicy"
	"github.com/example/autostream-control-panel/internal/security"
	"github.com/example/autostream-control-panel/internal/store"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestStartStopsAtFirstFailedDependency(t *testing.T) {
	var paths []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		if r.URL.Path == "/streams/start" {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		t.Fatalf("downstream start must not continue after encoder failure: %s", r.URL.Path)
	}))
	defer server.Close()

	client := testClient()
	results := client.Start(t.Context(), store.Stream{ID: "stream-01"}, []store.RegisteredService{
		{ServiceID: "discord-01", ServiceType: "discord_bot", PublicURL: server.URL, ReportedCapabilities: map[string]any{CapabilityDiscordResolvedTargetV2: true}},
		{ServiceID: "worker-01", ServiceType: "worker", PublicURL: server.URL},
		{ServiceID: "enc-01", ServiceType: "encoder_recorder", PublicURL: server.URL},
	}, StartRequest{DiscordTargetRevision: 1, DiscordGuildID: "1001", DiscordVoiceChannelID: "1003", DiscordTextChannelID: "1002"})
	if len(results) != 1 {
		t.Fatalf("expected only the failed encoder result, got %#v", results)
	}
	if results[0].ServiceType != "encoder_recorder" || results[0].Success {
		t.Fatalf("unexpected failed encoder result: %#v", results[0])
	}
	if got, want := strings.Join(paths, ","), "/streams/start"; got != want {
		t.Fatalf("start dispatch continued after failure: got %q want %q", got, want)
	}
}

func TestStartPayloadOmitsCaptionRouteWhenCaptionProfileIsNotSelected(t *testing.T) {
	client := testClient()
	client.Config.IngestTokenSigningKey = "test-ingest-signing-key"
	_, payloadValue, ok := client.startPayload(
		store.Stream{ID: "stream-01"},
		store.RegisteredService{ServiceID: "discord-01", ServiceType: "discord_bot"},
		StartRequest{},
		"https://encoder.example.com",
		store.RegisteredService{ServiceID: "worker-01", ServiceType: "worker", PublicURL: "https://worker.example.com"},
		time.Now().UTC(),
	)
	if !ok {
		t.Fatal("discord start payload was not built")
	}
	payload := payloadValue.(map[string]any)
	if _, exists := payload["caption_audio_url"]; exists {
		t.Fatalf("caption route must be omitted without a caption profile: %#v", payload)
	}
	if _, exists := payload["caption_audio_token"]; exists {
		t.Fatalf("caption token must be omitted without a caption profile: %#v", payload)
	}
}

func TestStartUsesEncryptedNodeRuntimeTokenBeforeGlobalFallback(t *testing.T) {
	var auth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusAccepted)
	}))
	defer server.Close()
	ciphertext, nonce, err := security.EncryptSecret("node-runtime-token", "secret-key")
	if err != nil {
		t.Fatal(err)
	}
	client := Client{Config: Config{
		Timeout:      time.Second,
		NodeTokenKey: "secret-key",
		URLPolicy: netpolicy.ServiceURLPolicy{
			AllowedHosts: map[string]struct{}{"127.0.0.1": {}},
		},
	}}
	if client.RuntimeTokenResolver != nil {
		t.Fatal("encrypted-node-token fixture must use the real resolver")
	}
	results := client.Start(t.Context(), store.Stream{ID: "stream-01", Name: "Morning"}, []store.RegisteredService{{
		ServiceID:           "enc-01",
		ServiceType:         "encoder_recorder",
		PublicURL:           server.URL,
		NodeTokenCiphertext: ciphertext,
		NodeTokenNonce:      nonce,
	}}, StartRequest{EncoderProfileID: "enc-prof-01"})
	if len(results) != 1 || !results[0].Success {
		t.Fatalf("dispatch failed: %#v", results)
	}
	if auth != "Bearer node-runtime-token" {
		t.Fatal("dispatch authorization did not use the node credential")
	}
}

func TestDispatchErrorDoesNotLeakToken(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "service-token", http.StatusForbidden)
	}))
	defer server.Close()
	client := testClient()
	results := client.Start(t.Context(), store.Stream{ID: "stream-01", Name: "Morning"}, []store.RegisteredService{{ServiceID: "enc-01", ServiceType: "encoder_recorder", PublicURL: server.URL}}, StartRequest{})
	if len(results) != 1 || results[0].Success {
		t.Fatalf("expected failed result: %#v", results)
	}
	if strings.Contains(results[0].Error, "service-token") {
		t.Fatalf("token leaked in error: %#v", results[0])
	}
}
