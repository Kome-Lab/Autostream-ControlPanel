package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/example/autostream-control-panel/internal/store"
	ytlive "github.com/example/autostream-control-panel/internal/youtube"
)

type scriptedYouTubeIngestHealthClient struct {
	*fakeYouTubeLiveClient
	snapshot ytlive.BroadcastIngestHealthSnapshot
	err      error
	calls    int
	request  ytlive.BroadcastIngestHealthRequest
}

func (f *scriptedYouTubeIngestHealthClient) BroadcastIngestHealth(_ context.Context, req ytlive.BroadcastIngestHealthRequest) (ytlive.BroadcastIngestHealthSnapshot, error) {
	f.calls++
	f.request = req
	if f.err != nil {
		return ytlive.BroadcastIngestHealthSnapshot{}, f.err
	}
	return f.snapshot, nil
}

func TestAuditActiveYouTubeIngestHealthPersistsSafeChangedSnapshots(t *testing.T) {
	streams, auth, integrations, stream, account := youtubeIngestHealthFixture(t)
	client := &scriptedYouTubeIngestHealthClient{
		fakeYouTubeLiveClient: &fakeYouTubeLiveClient{},
		snapshot: ytlive.BroadcastIngestHealthSnapshot{
			BroadcastID:           "broadcast-01",
			LiveStreamID:          "live-stream-01",
			ConfiguredResolution:  "1080p",
			ConfiguredFrameRate:   "60fps",
			StreamStatus:          "active",
			HealthStatus:          "good",
			LastUpdateTimeSeconds: 1787029583,
		},
	}
	handler := NewServer(
		streams,
		WithAuditStore(auth),
		WithIntegrationStore(integrations),
		WithYouTubeLiveClient(client),
	)

	result, err := handler.AuditActiveYouTubeIngestHealth(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if result["checked"] != 1 || result["recorded"] != 1 || result["failed"] != 0 {
		t.Fatalf("first ingest health result = %#v", result)
	}
	events := auth.AuditEvents()
	if len(events) != 1 {
		t.Fatalf("first ingest health audit count = %d, want 1", len(events))
	}
	event := events[0]
	if event.Action != "youtube.ingest_health" || event.ResourceID != stream.ID || event.Result != "success" || event.ActorUsername != "service:control-panel" {
		t.Fatalf("unexpected ingest health audit: %#v", event)
	}
	if event.Metadata["broadcast_id"] != "broadcast-01" || event.Metadata["provider_live_stream_id"] != "live-stream-01" || event.Metadata["binding_matches_runtime"] != true {
		t.Fatalf("ingest binding evidence missing: %#v", event.Metadata)
	}
	if event.Metadata["configured_resolution"] != "1080p" || event.Metadata["configured_frame_rate"] != "60fps" || event.Metadata["provider_health_status"] != "good" {
		t.Fatalf("ingest health evidence missing: %#v", event.Metadata)
	}
	if client.request.BroadcastID != "broadcast-01" || client.request.Credentials.RefreshToken != "youtube-refresh" || account.ID == "" {
		t.Fatalf("health client did not receive the exact runtime credentials and Broadcast: broadcast_id=%q account_id=%q", client.request.BroadcastID, account.ID)
	}
	assertYouTubeIngestHealthAuditHasNoSecrets(t, events)

	client.snapshot.LastUpdateTimeSeconds++
	result, err = handler.AuditActiveYouTubeIngestHealth(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if result["recorded"] != 0 || len(auth.AuditEvents()) != 1 {
		t.Fatalf("timestamp-only update was not deduplicated: result=%#v events=%#v", result, auth.AuditEvents())
	}

	client.snapshot.HealthStatus = "bad"
	client.snapshot.ConfigurationIssues = []ytlive.BroadcastIngestHealthIssue{
		{Type: "noAudioStream", Severity: "error"},
		{Type: "videoResolutionSuboptimal", Severity: "warning", Dimensions: []string{"1080x1080", "1920x1080"}},
	}
	result, err = handler.AuditActiveYouTubeIngestHealth(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if result["recorded"] != 1 || len(auth.AuditEvents()) != 2 || client.calls != 3 {
		t.Fatalf("changed health was not persisted once: result=%#v calls=%d events=%#v", result, client.calls, auth.AuditEvents())
	}
	latest := auth.AuditEvents()[1]
	if latest.Metadata["provider_health_status"] != "bad" {
		t.Fatalf("changed provider health missing: %#v", latest.Metadata)
	}
	issues, ok := latest.Metadata["configuration_issues"].([]any)
	if !ok || len(issues) != 2 {
		t.Fatalf("safe configuration issues missing: %#v", latest.Metadata["configuration_issues"])
	}
	assertYouTubeIngestHealthAuditHasNoSecrets(t, auth.AuditEvents())
}

func TestAuditActiveYouTubeIngestHealthPersistsOnlyBoundedErrorClass(t *testing.T) {
	streams, auth, integrations, _, _ := youtubeIngestHealthFixture(t)
	client := &scriptedYouTubeIngestHealthClient{
		fakeYouTubeLiveClient: &fakeYouTubeLiveClient{},
		err:                   errors.New("provider raw error Authorization: Bearer raw-access-token stream_key=raw-stream-key https://secret.example.test/live"),
	}
	handler := NewServer(streams, WithAuditStore(auth), WithIntegrationStore(integrations), WithYouTubeLiveClient(client))

	result, err := handler.AuditActiveYouTubeIngestHealth(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if result["failed"] != 1 || result["recorded"] != 1 {
		t.Fatalf("provider failure result = %#v", result)
	}
	events := auth.AuditEvents()
	if len(events) != 1 || events[0].Result != "failure" || events[0].Metadata["error_code"] != "youtube_ingest_health_provider_unavailable" {
		t.Fatalf("provider failure audit = %#v", events)
	}
	assertYouTubeIngestHealthAuditHasNoSecrets(t, events)

	result, err = handler.AuditActiveYouTubeIngestHealth(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if result["recorded"] != 0 || len(auth.AuditEvents()) != 1 {
		t.Fatalf("unchanged provider failure was not deduplicated: result=%#v events=%#v", result, auth.AuditEvents())
	}
}

func youtubeIngestHealthFixture(t *testing.T) (*store.MemoryStreamStore, *store.MemoryAuthStore, *store.MemoryIntegrationStore, store.Stream, store.OAuthAccount) {
	t.Helper()
	streams := store.NewMemoryStreamStore()
	auth := store.NewMemoryAuthStore()
	integrations := store.NewMemoryIntegrationStore()
	stream, err := streams.CreateStream(t.Context(), "YouTube ingest health")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := streams.UpdateStreamStatus(t.Context(), stream.ID, "live"); err != nil {
		t.Fatal(err)
	}
	provider, err := integrations.CreateOAuthProvider(t.Context(), store.OAuthProvider{
		ProviderType: "google",
		Name:         "YouTube",
		Enabled:      true,
		ClientID:     "youtube-client",
		ClientSecret: "youtube-client-secret",
		RedirectURI:  "https://control.example.test/oauth",
	})
	if err != nil {
		t.Fatal(err)
	}
	account, err := integrations.CreateOAuthAccount(t.Context(), store.OAuthAccount{
		ProviderID:   provider.ID,
		ProviderType: "google",
		AccountLabel: "youtube",
		RefreshToken: "youtube-refresh",
		Scopes:       []string{"https://www.googleapis.com/auth/youtube"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := streams.SaveStreamYouTubeRuntime(t.Context(), store.StreamYouTubeRuntime{
		StreamID:       stream.ID,
		YouTubeOutput:  "youtube-output-01",
		OAuthAccountID: account.ID,
		Mode:           "live_api",
		BroadcastID:    "broadcast-01",
		LiveStreamID:   "live-stream-01",
	}); err != nil {
		t.Fatal(err)
	}
	stream, err = streams.GetStream(t.Context(), stream.ID)
	if err != nil {
		t.Fatal(err)
	}
	return streams, auth, integrations, stream, account
}

func assertYouTubeIngestHealthAuditHasNoSecrets(t *testing.T, events []store.AuditEvent) {
	t.Helper()
	encoded, err := json.Marshal(events)
	if err != nil {
		t.Fatal(err)
	}
	lower := strings.ToLower(string(encoded))
	for _, forbidden := range []string{
		"youtube-client-secret",
		"youtube-refresh",
		"raw-access-token",
		"raw-stream-key",
		"authorization: bearer",
		"secret.example.test",
		"rtmps://",
	} {
		if strings.Contains(lower, forbidden) {
			t.Fatalf("secret value %q leaked into ingest health audit: %s", forbidden, encoded)
		}
	}
}
