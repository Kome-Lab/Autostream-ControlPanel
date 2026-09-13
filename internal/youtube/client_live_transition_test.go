package youtube

import (
	"context"
	"encoding/json"
	"errors"
	"golang.org/x/oauth2"
	"net/http"
	"strings"
	"testing"
)

func TestLiveAPIClientCompleteUsesOAuthAndTransition(t *testing.T) {
	transport := &fakeYouTubeRoundTripper{}
	httpClient := &http.Client{Transport: transport}
	ctx := context.WithValue(context.Background(), oauth2.HTTPClient, httpClient)
	client := LiveAPIClient{HTTPClient: httpClient}

	err := client.Complete(ctx, CompleteRequest{
		Credentials: OAuthCredentials{
			ClientID:     "youtube-client-id",
			ClientSecret: "youtube-client-secret",
			RefreshToken: "youtube-refresh-token",
		},
		BroadcastID: "broadcast-01",
	})
	if err != nil {
		t.Fatal(err)
	}
	if transport.tokenRefreshes != 1 {
		t.Fatalf("expected one OAuth refresh, got %d", transport.tokenRefreshes)
	}
	if !transport.saw("complete_broadcast") {
		t.Fatalf("missing complete transition in %#v", transport.steps)
	}
	for _, request := range transport.apiRequests {
		if request.Authorization != "Bearer ya29.fake-youtube-access-token" {
			t.Fatalf("YouTube complete request did not use refreshed bearer token: %#v", request)
		}
	}
}

func TestLiveAPIClientTransitionBroadcastLiveUsesOAuthAndTransition(t *testing.T) {
	transport := &fakeYouTubeRoundTripper{}
	httpClient := &http.Client{Transport: transport}
	ctx := context.WithValue(context.Background(), oauth2.HTTPClient, httpClient)
	client := LiveAPIClient{HTTPClient: httpClient}

	err := client.TransitionBroadcastLive(ctx, BroadcastTransitionRequest{
		Credentials: OAuthCredentials{
			ClientID:     "youtube-client-id",
			ClientSecret: "youtube-client-secret",
			RefreshToken: "youtube-refresh-token",
		},
		BroadcastID: "broadcast-01",
	})
	if err != nil {
		t.Fatal(err)
	}
	if transport.tokenRefreshes != 1 {
		t.Fatalf("expected one OAuth refresh, got %d", transport.tokenRefreshes)
	}
	if !transport.saw("live_broadcast") {
		t.Fatalf("missing live transition in %#v", transport.steps)
	}
	for _, request := range transport.apiRequests {
		if request.Authorization != "Bearer ya29.fake-youtube-access-token" {
			t.Fatalf("YouTube live transition did not use refreshed bearer token: %#v", request)
		}
	}
}

func TestLiveAPIClientCompleteRelayStaticBroadcastReconcilesRedundantTransition(t *testing.T) {
	transport := &fakeYouTubeRoundTripper{
		completeBroadcastStatus: http.StatusForbidden,
		broadcastListResponse:   `{"items":[{"id":"broadcast-01","status":{"lifeCycleStatus":"complete"}}]}`,
	}
	httpClient := &http.Client{Transport: transport}
	ctx := context.WithValue(context.Background(), oauth2.HTTPClient, httpClient)
	client := LiveAPIClient{HTTPClient: httpClient}

	err := client.CompleteRelayStaticBroadcast(ctx, CompleteRequest{
		Credentials: OAuthCredentials{ClientID: "youtube-client-id", ClientSecret: "youtube-client-secret", RefreshToken: "youtube-refresh-token"},
		BroadcastID: "broadcast-01",
	})
	if err != nil {
		t.Fatalf("complete relay-static broadcast: %v", err)
	}
	if !transport.saw("complete_broadcast") || !transport.saw("get_broadcast_status") {
		t.Fatalf("expected transition and lifecycle reconcile, got %#v", transport.steps)
	}
}

func TestLiveAPIClientCompleteRelayStaticBroadcastRetainsUnconfirmedTransition(t *testing.T) {
	transport := &fakeYouTubeRoundTripper{
		completeBroadcastResponseLost: true,
		broadcastListResponse:         `{"items":[{"id":"broadcast-01","status":{"lifeCycleStatus":"live"}}]}`,
	}
	httpClient := &http.Client{Transport: transport}
	ctx := context.WithValue(context.Background(), oauth2.HTTPClient, httpClient)
	client := LiveAPIClient{HTTPClient: httpClient}

	err := client.CompleteRelayStaticBroadcast(ctx, CompleteRequest{
		Credentials: OAuthCredentials{ClientID: "youtube-client-id", ClientSecret: "youtube-client-secret", RefreshToken: "youtube-refresh-token"},
		BroadcastID: "broadcast-01",
	})
	var completionErr *RelayStaticBroadcastCompletionError
	if !errors.As(err, &completionErr) || !errors.Is(err, ErrRelayStaticBroadcastCompletionFailed) || !errors.Is(err, ErrRelayStaticBroadcastCompletionUncertain) {
		t.Fatalf("unconfirmed relay-static completion error = %v", err)
	}
	if completionErr.BroadcastID != "broadcast-01" {
		t.Fatalf("unconfirmed completion lost safe broadcast identity: %#v", completionErr)
	}
	if got := RedactedError(err); got != ErrRelayStaticBroadcastCompletionUncertain.Error() {
		t.Fatalf("completion error leaked or changed safe code: %q", got)
	}
	if !transport.saw("complete_broadcast") || !transport.saw("get_broadcast_status") {
		t.Fatalf("expected transition and lifecycle reconcile, got %#v", transport.steps)
	}
}

func TestLiveAPIClientBroadcastIngestHealthReadsOnlySafeBoundStreamFields(t *testing.T) {
	transport := &fakeYouTubeRoundTripper{}
	httpClient := &http.Client{Transport: transport}
	client := LiveAPIClient{HTTPClient: httpClient}

	snapshot, err := client.BroadcastIngestHealth(context.Background(), BroadcastIngestHealthRequest{
		Credentials: OAuthCredentials{
			ClientID:     "youtube-client-id",
			ClientSecret: "youtube-client-secret",
			RefreshToken: "youtube-refresh-token",
		},
		BroadcastID: "broadcast-01",
	})
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.BroadcastID != "broadcast-01" || snapshot.LiveStreamID != "live-stream-01" {
		t.Fatalf("unexpected ingest binding: %#v", snapshot)
	}
	if snapshot.ConfiguredResolution != "1080p" || snapshot.ConfiguredFrameRate != "60fps" || snapshot.StreamStatus != "active" || snapshot.HealthStatus != "bad" {
		t.Fatalf("unexpected ingest status: %#v", snapshot)
	}
	if snapshot.LastUpdateTimeSeconds != 1787029583 || len(snapshot.ConfigurationIssues) != 2 {
		t.Fatalf("unexpected ingest health details: %#v", snapshot)
	}
	if snapshot.ConfigurationIssues[0].Type != "noAudioStream" || snapshot.ConfigurationIssues[1].Type != "videoResolutionSuboptimal" {
		t.Fatalf("configuration issues were not normalized deterministically: %#v", snapshot.ConfigurationIssues)
	}
	if got := strings.Join(snapshot.ConfigurationIssues[1].Dimensions, ","); got != "1080x1080,1920x1080" {
		t.Fatalf("safe dimensions were not extracted from the provider issue: %q", got)
	}
	if !transport.saw("get_broadcast_binding") || !transport.saw("get_ingest_health") {
		t.Fatalf("expected exact broadcast binding and LiveStream health reads, got %#v", transport.steps)
	}
	for _, request := range transport.apiRequests {
		lowerQuery := strings.ToLower(request.RawQuery)
		for _, forbidden := range []string{"ingestioninfo", "streamname", "ingestionaddress", "rtmpsingestionaddress"} {
			if strings.Contains(lowerQuery, forbidden) {
				t.Fatalf("secret-bearing YouTube field %q was requested: %#v", forbidden, request)
			}
		}
	}
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "provider free-form details") {
		t.Fatalf("free-form provider description leaked into snapshot: %s", encoded)
	}
}

func TestLiveAPIClientBroadcastIngestHealthRejectsMissingBoundStream(t *testing.T) {
	transport := &fakeYouTubeRoundTripper{
		ingestHealthBroadcastResponse: `{"items":[{"id":"broadcast-01","contentDetails":{}}]}`,
	}
	httpClient := &http.Client{Transport: transport}
	client := LiveAPIClient{HTTPClient: httpClient}

	_, err := client.BroadcastIngestHealth(context.Background(), BroadcastIngestHealthRequest{
		Credentials: OAuthCredentials{ClientID: "youtube-client-id", ClientSecret: "youtube-client-secret", RefreshToken: "youtube-refresh-token"},
		BroadcastID: "broadcast-01",
	})
	if !errors.Is(err, ErrBroadcastLiveStreamUnavailable) {
		t.Fatalf("missing bound stream error = %v", err)
	}
	if got := RedactedError(err); got != ErrBroadcastLiveStreamUnavailable.Error() {
		t.Fatalf("missing bound stream redacted error = %q", got)
	}
	if transport.saw("get_ingest_health") {
		t.Fatalf("LiveStream health was queried without a provider binding: %#v", transport.steps)
	}
}
