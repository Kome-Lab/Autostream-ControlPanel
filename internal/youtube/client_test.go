package youtube

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"golang.org/x/oauth2"
	youtubeapi "google.golang.org/api/youtube/v3"
)

func TestRTMPSIngestRequiresSecureAddress(t *testing.T) {
	rtmpURL, streamKey, err := rtmpsIngest(&youtubeapi.IngestionInfo{
		RtmpsIngestionAddress: "rtmps://a.rtmps.youtube.com/live2",
		IngestionAddress:      "rtmp://a.rtmp.youtube.com/live2",
		StreamName:            "stream-key",
	})
	if err != nil {
		t.Fatal(err)
	}
	if rtmpURL != "rtmps://a.rtmps.youtube.com/live2" || streamKey != "stream-key" {
		t.Fatalf("unexpected RTMPS ingest values: url=%q key=%q", rtmpURL, streamKey)
	}
}

func TestRTMPSIngestRejectsPlainRTMPFallback(t *testing.T) {
	_, _, err := rtmpsIngest(&youtubeapi.IngestionInfo{
		IngestionAddress: "rtmp://a.rtmp.youtube.com/live2",
		StreamName:       "stream-key",
	})
	if !errors.Is(err, ErrMissingIngestInfo) {
		t.Fatalf("expected RTMP-only ingest info to be rejected, got %v", err)
	}
}

func TestLiveAPIClientPrepareUsesOAuthAndBindsRTMPSStream(t *testing.T) {
	transport := &fakeYouTubeRoundTripper{}
	httpClient := &http.Client{Transport: transport}
	ctx := context.WithValue(context.Background(), oauth2.HTTPClient, httpClient)
	client := LiveAPIClient{HTTPClient: httpClient}

	prepared, err := client.Prepare(ctx, PrepareRequest{
		Credentials: OAuthCredentials{
			ClientID:     "youtube-client-id",
			ClientSecret: "youtube-client-secret",
			RefreshToken: "youtube-refresh-token",
		},
		StreamID:        "stream-01",
		StreamName:      "Morning Stream",
		OutputID:        "youtube-output-01",
		Title:           "Private Test",
		Description:     "AutoStream private test",
		PrivacyStatus:   "private",
		ScheduledStart:  time.Date(2026, 6, 11, 12, 0, 0, 0, time.UTC),
		Resolution:      "variable",
		FrameRate:       "variable",
		EnableAutoStart: true,
		EnableAutoStop:  true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if prepared.RTMPURL != "rtmps://a.rtmps.youtube.com/live2" ||
		prepared.StreamKey != "runtime-stream-key" ||
		prepared.BroadcastID != "broadcast-01" ||
		prepared.LiveStreamID != "live-stream-01" {
		t.Fatalf("unexpected prepared output: %#v", prepared)
	}
	if transport.tokenRefreshes != 1 {
		t.Fatalf("expected one OAuth refresh, got %d", transport.tokenRefreshes)
	}
	for _, step := range []string{"insert_broadcast", "insert_stream", "bind_broadcast"} {
		if !transport.saw(step) {
			t.Fatalf("missing YouTube API step %q in %#v", step, transport.steps)
		}
	}
	for _, request := range transport.apiRequests {
		if request.Authorization != "Bearer ya29.fake-youtube-access-token" {
			t.Fatalf("YouTube API request did not use refreshed bearer token: %#v", request)
		}
	}
	if !strings.Contains(transport.broadcastInsertBody, `"privacyStatus":"private"`) ||
		!strings.Contains(transport.broadcastInsertBody, `"title":"Private Test"`) {
		t.Fatalf("broadcast insert body omitted private test metadata: %s", transport.broadcastInsertBody)
	}
	if !strings.Contains(transport.broadcastInsertBody, `"enableAutoStart":true`) ||
		!strings.Contains(transport.broadcastInsertBody, `"enableAutoStop":true`) {
		t.Fatalf("broadcast insert body omitted YouTube auto start/stop settings: %s", transport.broadcastInsertBody)
	}
	if !strings.Contains(transport.broadcastInsertBody, `"enableMonitorStream":false`) {
		t.Fatalf("direct AutoStart broadcast must disable the optional monitor stream: %s", transport.broadcastInsertBody)
	}
	if !strings.Contains(transport.broadcastInsertBody, `"projection":"rectangular"`) {
		t.Fatalf("broadcast insert body must force rectangular playback projection: %s", transport.broadcastInsertBody)
	}
	if !strings.Contains(transport.accountStreamInsertBody, `"isReusable":false`) {
		t.Fatalf("stream-scoped ingest must explicitly disable provider reuse: %s", transport.accountStreamInsertBody)
	}
	if !strings.Contains(transport.accountStreamInsertBody, `"resolution":"variable"`) || !strings.Contains(transport.accountStreamInsertBody, `"frameRate":"variable"`) {
		t.Fatalf("stream-scoped ingest must let YouTube detect the exact Encoder geometry: %s", transport.accountStreamInsertBody)
	}
}

func TestLiveAPIClientPrepareUsesNearTermStartWhenUnscheduled(t *testing.T) {
	transport := &fakeYouTubeRoundTripper{}
	httpClient := &http.Client{Transport: transport}
	client := LiveAPIClient{HTTPClient: httpClient}

	_, err := client.Prepare(context.Background(), PrepareRequest{
		Credentials: OAuthCredentials{
			ClientID:     "youtube-client-id",
			ClientSecret: "youtube-client-secret",
			RefreshToken: "youtube-refresh-token",
		},
		StreamID:        "stream-immediate",
		StreamName:      "Immediate Stream",
		EnableAutoStart: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	var payload struct {
		Snippet struct {
			ScheduledStartTime string `json:"scheduledStartTime"`
		} `json:"snippet"`
	}
	if err := json.Unmarshal([]byte(transport.broadcastInsertBody), &payload); err != nil {
		t.Fatalf("decode broadcast request: %v\nbody=%s", err, transport.broadcastInsertBody)
	}
	scheduledStart, err := time.Parse(time.RFC3339, payload.Snippet.ScheduledStartTime)
	if err != nil {
		t.Fatalf("parse scheduled start %q: %v", payload.Snippet.ScheduledStartTime, err)
	}
	now := time.Now().UTC()
	if scheduledStart.Before(now.Add(10*time.Second)) || scheduledStart.After(now.Add(20*time.Second)) {
		t.Fatalf("immediate broadcast must use a near-term future start, got %s (now %s)", scheduledStart, now)
	}
	if !strings.Contains(transport.broadcastInsertBody, `"enableAutoStart":true`) ||
		!strings.Contains(transport.broadcastInsertBody, `"enableMonitorStream":false`) {
		t.Fatalf("unscheduled AutoStart broadcast must opt into direct ingest start: %s", transport.broadcastInsertBody)
	}
}

func TestLiveAPIClientPreparePreservesExplicitFutureSchedule(t *testing.T) {
	transport := &fakeYouTubeRoundTripper{}
	httpClient := &http.Client{Transport: transport}
	client := LiveAPIClient{HTTPClient: httpClient}
	scheduledStart := time.Now().UTC().Add(10 * time.Minute).Truncate(time.Second)

	_, err := client.Prepare(context.Background(), PrepareRequest{
		Credentials: OAuthCredentials{
			ClientID:     "youtube-client-id",
			ClientSecret: "youtube-client-secret",
			RefreshToken: "youtube-refresh-token",
		},
		StreamID:       "stream-scheduled",
		StreamName:     "Scheduled Stream",
		ScheduledStart: scheduledStart,
	})
	if err != nil {
		t.Fatal(err)
	}
	var payload struct {
		Snippet struct {
			ScheduledStartTime string `json:"scheduledStartTime"`
		} `json:"snippet"`
	}
	if err := json.Unmarshal([]byte(transport.broadcastInsertBody), &payload); err != nil {
		t.Fatalf("decode broadcast request: %v\nbody=%s", err, transport.broadcastInsertBody)
	}
	got, err := time.Parse(time.RFC3339, payload.Snippet.ScheduledStartTime)
	if err != nil {
		t.Fatalf("parse scheduled start %q: %v", payload.Snippet.ScheduledStartTime, err)
	}
	if !got.Equal(scheduledStart) {
		t.Fatalf("explicit future schedule changed: got %s want %s", got, scheduledStart)
	}
}

func TestLiveAPIClientPrepareReusesOneAccountLiveStream(t *testing.T) {
	transport := &fakeYouTubeRoundTripper{}
	httpClient := &http.Client{Transport: transport}
	ctx := context.WithValue(context.Background(), oauth2.HTTPClient, httpClient)
	client := LiveAPIClient{HTTPClient: httpClient}
	request := PrepareRequest{
		Credentials: OAuthCredentials{
			ClientID:     "youtube-client-id",
			ClientSecret: "youtube-client-secret",
			RefreshToken: "youtube-refresh-token",
		},
		StreamID:           "account-stream-01",
		StreamName:         "Account Stream",
		Title:              "Account Stream",
		ReuseAccountStream: true,
	}
	first, err := client.Prepare(ctx, request)
	if err != nil {
		t.Fatalf("first prepare: %v", err)
	}
	second, err := client.Prepare(ctx, request)
	if err != nil {
		t.Fatalf("second prepare: %v", err)
	}
	if first.LiveStreamID != "live-stream-01" || second.LiveStreamID != first.LiveStreamID {
		t.Fatalf("account stream was not reused: first=%#v second=%#v", first, second)
	}
	if first.StreamKey != "runtime-stream-key" || second.StreamKey != first.StreamKey {
		t.Fatalf("account stream ingest key changed: first=%q second=%q", first.StreamKey, second.StreamKey)
	}
	if transport.accountStreamInsertions != 1 {
		t.Fatalf("expected one account LiveStream insertion, got %d (steps=%#v)", transport.accountStreamInsertions, transport.steps)
	}
	if !transport.saw("list_account_reusable_streams") {
		t.Fatalf("expected account LiveStream lookup, got %#v", transport.steps)
	}
}

func TestLiveAPIClientPrepareDoesNotReuseMismatchedAccountLiveStream(t *testing.T) {
	transport := &fakeYouTubeRoundTripper{
		accountReusableStreamResponse: `{"items":[{"id":"legacy-4k-stream","snippet":{"title":"AutoStream account ingest"},"contentDetails":{"isReusable":true},"cdn":{"resolution":"2160p","frameRate":"60fps","ingestionInfo":{"rtmpsIngestionAddress":"rtmps://a.rtmps.youtube.com/live2","streamName":"legacy-4k-key"}}}]}`,
	}
	httpClient := &http.Client{Transport: transport}
	ctx := context.WithValue(context.Background(), oauth2.HTTPClient, httpClient)
	client := LiveAPIClient{HTTPClient: httpClient}

	prepared, err := client.Prepare(ctx, PrepareRequest{
		Credentials: OAuthCredentials{
			ClientID:     "youtube-client-id",
			ClientSecret: "youtube-client-secret",
			RefreshToken: "youtube-refresh-token",
		},
		StreamID:           "stream-1080p",
		StreamName:         "1080p Stream",
		Resolution:         "1080p",
		FrameRate:          "60fps",
		ReuseAccountStream: true,
	})
	if err != nil {
		t.Fatalf("prepare 1080p output with legacy 4K account stream: %v", err)
	}
	if prepared.LiveStreamID == "legacy-4k-stream" || prepared.StreamKey == "legacy-4k-key" {
		t.Fatalf("mismatched 4K account stream was reused for 1080p output: %#v", prepared)
	}
	if transport.accountStreamInsertions != 1 {
		t.Fatalf("expected one matching 1080p LiveStream insertion, got %d (steps=%#v)", transport.accountStreamInsertions, transport.steps)
	}
	if !strings.Contains(transport.accountStreamInsertBody, `"resolution":"1080p"`) ||
		!strings.Contains(transport.accountStreamInsertBody, `"frameRate":"60fps"`) {
		t.Fatalf("replacement LiveStream did not use requested video format: %s", transport.accountStreamInsertBody)
	}
}

func TestLiveAPIClientPrepareRelayStaticBindsReusableLiveStreamWithoutIngestInfo(t *testing.T) {
	transport := &fakeYouTubeRoundTripper{}
	httpClient := &http.Client{Transport: transport}
	client := LiveAPIClient{HTTPClient: httpClient}

	prepared, err := client.PrepareRelayStatic(context.Background(), RelayStaticPrepareRequest{
		PrepareRequest: PrepareRequest{
			Credentials: OAuthCredentials{
				ClientID:     "youtube-client-id",
				ClientSecret: "youtube-client-secret",
				RefreshToken: "youtube-refresh-token",
			},
			StreamID:        "stream-relay-static",
			StreamName:      "Relay Static Stream",
			Title:           "Relay Static Test",
			PrivacyStatus:   "private",
			EnableAutoStart: true,
			EnableAutoStop:  true,
		},
		ReusableLiveStreamID: "live-stream-static",
	})
	if err != nil {
		t.Fatal(err)
	}
	if prepared.BroadcastID != "broadcast-01" || prepared.LiveStreamID != "live-stream-static" {
		t.Fatalf("unexpected relay-static prepared output: %#v", prepared)
	}
	if prepared.RTMPURL != "" || prepared.StreamKey != "" {
		t.Fatalf("relay-static output must not expose ingest details: %#v", prepared)
	}
	if transport.boundStreamID != "live-stream-static" {
		t.Fatalf("relay-static broadcast bound the wrong reusable stream: %q", transport.boundStreamID)
	}
	for _, step := range []string{"get_reusable_stream", "insert_broadcast", "bind_broadcast"} {
		if !transport.saw(step) {
			t.Fatalf("missing YouTube API step %q in %#v", step, transport.steps)
		}
	}
	if transport.saw("insert_stream") {
		t.Fatalf("relay-static prepare must not create a live stream: %#v", transport.steps)
	}
	if !strings.Contains(transport.reusableLiveStreamQuery, "contentDetails") ||
		strings.Contains(transport.reusableLiveStreamQuery, "cdn") ||
		strings.Contains(transport.reusableLiveStreamQuery, "ingestion") {
		t.Fatalf("reusable stream validation requested unsafe fields: %q", transport.reusableLiveStreamQuery)
	}
	if !strings.Contains(transport.broadcastInsertBody, `"enableAutoStart":true`) ||
		!strings.Contains(transport.broadcastInsertBody, `"enableAutoStop":true`) {
		t.Fatalf("relay-static broadcast omitted YouTube auto start/stop settings: %s", transport.broadcastInsertBody)
	}
}

func TestLiveAPIClientPrepareRelayStaticRejectsInvalidReusableLiveStream(t *testing.T) {
	tests := []struct {
		name                   string
		reusableLiveStreamID   string
		liveStreamListResponse string
		liveStreamListStatus   int
		want                   error
	}{
		{
			name: "missing_id",
			want: ErrMissingReusableLiveStreamID,
		},
		{
			name:                 "not_found",
			reusableLiveStreamID: "live-stream-static",
			liveStreamListResponse: `{
				"items": []
			}`,
			want: ErrReusableLiveStreamNotFound,
		},
		{
			name:                   "not_found_http",
			reusableLiveStreamID:   "live-stream-static",
			liveStreamListStatus:   http.StatusNotFound,
			liveStreamListResponse: `{"error":{"code":404,"message":"not found"}}`,
			want:                   ErrReusableLiveStreamNotFound,
		},
		{
			name:                 "not_reusable",
			reusableLiveStreamID: "live-stream-static",
			liveStreamListResponse: `{
				"items": [{"id":"live-stream-static","contentDetails":{"isReusable":false}}]
			}`,
			want: ErrReusableLiveStreamNotReusable,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			transport := &fakeYouTubeRoundTripper{
				liveStreamListResponse: tt.liveStreamListResponse,
				liveStreamListStatus:   tt.liveStreamListStatus,
			}
			client := LiveAPIClient{HTTPClient: &http.Client{Transport: transport}}

			_, err := client.PrepareRelayStatic(context.Background(), RelayStaticPrepareRequest{
				PrepareRequest: PrepareRequest{Credentials: OAuthCredentials{
					ClientID:     "youtube-client-id",
					ClientSecret: "youtube-client-secret",
					RefreshToken: "youtube-refresh-token",
				}},
				ReusableLiveStreamID: tt.reusableLiveStreamID,
			})
			if !errors.Is(err, tt.want) {
				t.Fatalf("expected %v, got %v", tt.want, err)
			}
			if transport.saw("insert_broadcast") || transport.saw("bind_broadcast") || transport.saw("insert_stream") {
				t.Fatalf("invalid reusable stream must not create or bind YouTube resources: %#v", transport.steps)
			}
			if got := RedactedError(err); got != tt.want.Error() {
				t.Fatalf("expected safe error %q, got %q", tt.want, got)
			}
		})
	}
}

func TestLiveAPIClientPrepareRelayStaticBindFailureReturnsPartialResultAndCleansUp(t *testing.T) {
	transport := &fakeYouTubeRoundTripper{bindStatus: http.StatusInternalServerError}
	client := LiveAPIClient{HTTPClient: &http.Client{Transport: transport}}

	prepared, err := client.PrepareRelayStatic(context.Background(), RelayStaticPrepareRequest{
		PrepareRequest: PrepareRequest{Credentials: OAuthCredentials{
			ClientID:     "youtube-client-id",
			ClientSecret: "youtube-client-secret",
			RefreshToken: "youtube-refresh-token",
		}},
		ReusableLiveStreamID: "live-stream-static",
	})
	var bindErr *RelayStaticBindError
	if !errors.As(err, &bindErr) {
		t.Fatalf("expected typed relay-static bind error, got %v", err)
	}
	if !bindErr.CleanupConfirmed || !errors.Is(err, ErrRelayStaticBindFailed) || errors.Is(err, ErrRelayStaticBindCleanupUncertain) {
		t.Fatalf("unexpected safe bind cleanup outcome: %#v", bindErr)
	}
	if prepared.BroadcastID != "broadcast-01" || prepared.LiveStreamID != "live-stream-static" || prepared.RTMPURL != "" || prepared.StreamKey != "" {
		t.Fatalf("bind failure must return only safe partial identity: %#v", prepared)
	}
	if bindErr.BroadcastID != prepared.BroadcastID || bindErr.LiveStreamID != prepared.LiveStreamID {
		t.Fatalf("typed bind error did not preserve partial identity: %#v", bindErr)
	}
	if !transport.saw("delete_broadcast") {
		t.Fatalf("bind failure must attempt bounded cleanup: %#v", transport.steps)
	}
	if got := RedactedError(err); got != ErrRelayStaticBindFailed.Error() {
		t.Fatalf("expected safe bind failure code %q, got %q", ErrRelayStaticBindFailed, got)
	}
}

func TestLiveAPIClientPrepareRelayStaticBindFailureReportsUncertainCleanup(t *testing.T) {
	transport := &fakeYouTubeRoundTripper{
		bindStatus:            http.StatusInternalServerError,
		deleteBroadcastStatus: http.StatusInternalServerError,
	}
	client := LiveAPIClient{HTTPClient: &http.Client{Transport: transport}}

	prepared, err := client.PrepareRelayStatic(context.Background(), RelayStaticPrepareRequest{
		PrepareRequest: PrepareRequest{Credentials: OAuthCredentials{
			ClientID:     "youtube-client-id",
			ClientSecret: "youtube-client-secret",
			RefreshToken: "youtube-refresh-token",
		}},
		ReusableLiveStreamID: "live-stream-static",
	})
	var bindErr *RelayStaticBindError
	if !errors.As(err, &bindErr) {
		t.Fatalf("expected typed relay-static bind error, got %v", err)
	}
	if bindErr.CleanupConfirmed || !errors.Is(err, ErrRelayStaticBindFailed) || !errors.Is(err, ErrRelayStaticBindCleanupUncertain) {
		t.Fatalf("expected uncertain cleanup outcome, got %#v", bindErr)
	}
	if prepared.BroadcastID != "broadcast-01" || prepared.LiveStreamID != "live-stream-static" || prepared.RTMPURL != "" || prepared.StreamKey != "" {
		t.Fatalf("uncertain cleanup must preserve only safe partial identity: %#v", prepared)
	}
	if !transport.saw("delete_broadcast") {
		t.Fatalf("uncertain cleanup still must attempt deletion: %#v", transport.steps)
	}
	if got := RedactedError(err); got != ErrRelayStaticBindCleanupUncertain.Error() {
		t.Fatalf("expected safe uncertain cleanup code %q, got %q", ErrRelayStaticBindCleanupUncertain, got)
	}
}

func TestLiveAPIClientPrepareRelayStaticResponseLossAfterBroadcastInsertRetainsClaim(t *testing.T) {
	transport := &fakeYouTubeRoundTripper{broadcastInsertResponseLost: true}
	client := LiveAPIClient{HTTPClient: &http.Client{Transport: transport}}

	prepared, err := client.PrepareRelayStatic(context.Background(), RelayStaticPrepareRequest{
		PrepareRequest: PrepareRequest{Credentials: OAuthCredentials{
			ClientID:     "youtube-client-id",
			ClientSecret: "youtube-client-secret",
			RefreshToken: "youtube-refresh-token",
		}},
		ReusableLiveStreamID: "live-stream-static",
	})
	var createErr *RelayStaticBroadcastCreateError
	if !errors.As(err, &createErr) {
		t.Fatalf("expected typed relay-static broadcast-create uncertainty, got %v", err)
	}
	if !errors.Is(err, ErrRelayStaticBroadcastCreateUncertain) {
		t.Fatalf("response loss after broadcast insert must remain recovery-required, got %v", err)
	}
	if prepared.BroadcastID != "" || prepared.LiveStreamID != "live-stream-static" || prepared.RTMPURL != "" || prepared.StreamKey != "" {
		t.Fatalf("response-loss result must expose only the fixed LiveStream identity: %#v", prepared)
	}
	if createErr.LiveStreamID != prepared.LiveStreamID {
		t.Fatalf("typed uncertainty must preserve the fixed LiveStream identity: %#v", createErr)
	}
	if !transport.saw("insert_broadcast") || transport.saw("bind_broadcast") || transport.saw("delete_broadcast") {
		t.Fatalf("response-loss path must not bind or delete an unknown Broadcast: %#v", transport.steps)
	}
	if got := RedactedError(err); got != ErrRelayStaticBroadcastCreateUncertain.Error() {
		t.Fatalf("response-loss error leaked or changed safe code: got %q", got)
	}
}

func TestLiveAPIClientDeleteRelayStaticBroadcastConfirmsDeleteOrNotFound(t *testing.T) {
	for _, tt := range []struct {
		name                  string
		deleteBroadcastStatus int
		cancelCaller          bool
	}{
		{name: "deleted", deleteBroadcastStatus: http.StatusNoContent},
		{name: "already_deleted", deleteBroadcastStatus: http.StatusNotFound},
		{name: "caller_cancelled", deleteBroadcastStatus: http.StatusNoContent, cancelCaller: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			transport := &fakeYouTubeRoundTripper{deleteBroadcastStatus: tt.deleteBroadcastStatus}
			client := LiveAPIClient{HTTPClient: &http.Client{Transport: transport}}
			ctx, cancel := context.WithCancel(context.Background())
			if tt.cancelCaller {
				cancel()
			} else {
				defer cancel()
			}

			err := client.DeleteRelayStaticBroadcast(ctx, RelayStaticBroadcastCleanupRequest{
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
			if !transport.saw("delete_broadcast") {
				t.Fatalf("expected safe relay-static delete attempt, got %#v", transport.steps)
			}
		})
	}
}

func TestLiveAPIClientDeleteRelayStaticBroadcastResponseLossIsUncertain(t *testing.T) {
	transport := &fakeYouTubeRoundTripper{deleteBroadcastResponseLost: true}
	client := LiveAPIClient{HTTPClient: &http.Client{Transport: transport}}

	err := client.DeleteRelayStaticBroadcast(context.Background(), RelayStaticBroadcastCleanupRequest{
		Credentials: OAuthCredentials{
			ClientID:     "youtube-client-id",
			ClientSecret: "youtube-client-secret",
			RefreshToken: "youtube-refresh-token",
		},
		BroadcastID: "broadcast-01",
	})
	var cleanupErr *RelayStaticBroadcastCleanupError
	if !errors.As(err, &cleanupErr) {
		t.Fatalf("expected typed relay-static cleanup uncertainty, got %v", err)
	}
	if !errors.Is(err, ErrRelayStaticBroadcastCleanupFailed) || !errors.Is(err, ErrRelayStaticBroadcastCleanupUncertain) {
		t.Fatalf("response loss during delete must remain recovery-required, got %v", err)
	}
	if cleanupErr.BroadcastID != "broadcast-01" {
		t.Fatalf("typed cleanup uncertainty lost non-secret broadcast identity: %#v", cleanupErr)
	}
	if !transport.saw("delete_broadcast") {
		t.Fatalf("expected delete request before response loss, got %#v", transport.steps)
	}
	if got := RedactedError(err); got != ErrRelayStaticBroadcastCleanupUncertain.Error() {
		t.Fatalf("cleanup response-loss error leaked or changed safe code: got %q", got)
	}
}

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

func TestLiveAPIClientRefreshAccessTokenUsesProviderRefreshToken(t *testing.T) {
	transport := &fakeYouTubeRoundTripper{}
	httpClient := &http.Client{Transport: transport}
	client := LiveAPIClient{HTTPClient: httpClient}

	token, err := client.RefreshAccessToken(context.Background(), OAuthCredentials{
		ClientID:     "youtube-client-id",
		ClientSecret: "youtube-client-secret",
		RefreshToken: "youtube-refresh-token",
	})
	if err != nil {
		t.Fatal(err)
	}
	if token.AccessToken != "ya29.fake-youtube-access-token" || token.RefreshToken != "youtube-refresh-token" {
		t.Fatalf("unexpected refreshed token: %#v", token)
	}
	if transport.tokenRefreshes != 1 {
		t.Fatalf("expected one OAuth refresh, got %d", transport.tokenRefreshes)
	}
}

type fakeYouTubeRoundTripper struct {
	tokenRefreshes                int
	steps                         []string
	apiRequests                   []fakeYouTubeAPIRequest
	broadcastInsertBody           string
	reusableLiveStreamQuery       string
	liveStreamListResponse        string
	liveStreamListStatus          int
	boundStreamID                 string
	bindStatus                    int
	broadcastInsertResponseLost   bool
	deleteBroadcastStatus         int
	deleteBroadcastResponseLost   bool
	completeBroadcastStatus       int
	completeBroadcastResponseLost bool
	broadcastListStatus           int
	broadcastListResponse         string
	accountReusableStreamCreated  bool
	accountReusableStreamResponse string
	accountStreamInsertBody       string
	accountStreamInsertions       int
}

type fakeYouTubeAPIRequest struct {
	Method        string
	Path          string
	Authorization string
}

func (f *fakeYouTubeRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	var bodyBytes []byte
	if req.Body != nil {
		var err error
		bodyBytes, err = io.ReadAll(req.Body)
		if err != nil {
			return nil, err
		}
		req.Body.Close()
	}
	body := string(bodyBytes)

	if req.URL.Host == "oauth2.googleapis.com" && req.URL.Path == "/token" {
		f.tokenRefreshes++
		if req.Method != http.MethodPost ||
			!strings.Contains(body, "grant_type=refresh_token") ||
			!strings.Contains(body, "refresh_token=youtube-refresh-token") {
			return fakeHTTPResponse(req, http.StatusBadRequest, `{"error":"bad token request"}`), nil
		}
		clientID, clientSecret, ok := req.BasicAuth()
		bodyHasClientAuth := strings.Contains(body, "client_id=youtube-client-id") && strings.Contains(body, "client_secret=youtube-client-secret")
		if !(ok && clientID == "youtube-client-id" && clientSecret == "youtube-client-secret") && !bodyHasClientAuth {
			return fakeHTTPResponse(req, http.StatusBadRequest, `{"error":"bad client auth"}`), nil
		}
		return fakeHTTPResponse(req, http.StatusOK, `{"access_token":"ya29.fake-youtube-access-token","token_type":"Bearer","expires_in":3600}`), nil
	}

	f.apiRequests = append(f.apiRequests, fakeYouTubeAPIRequest{Method: req.Method, Path: req.URL.Path, Authorization: req.Header.Get("Authorization")})
	switch {
	case req.Method == http.MethodGet && req.URL.Path == "/youtube/v3/liveStreams" && req.URL.Query().Get("id") == "":
		f.steps = append(f.steps, "list_account_reusable_streams")
		if f.accountReusableStreamResponse != "" {
			return fakeHTTPResponse(req, http.StatusOK, f.accountReusableStreamResponse), nil
		}
		if f.accountReusableStreamCreated {
			return fakeHTTPResponse(req, http.StatusOK, `{"items":[{"id":"live-stream-01","snippet":{"title":"AutoStream account ingest"},"contentDetails":{"isReusable":true},"cdn":{"resolution":"1080p","frameRate":"60fps","ingestionInfo":{"rtmpsIngestionAddress":"rtmps://a.rtmps.youtube.com/live2","streamName":"runtime-stream-key"}}}]}`), nil
		}
		return fakeHTTPResponse(req, http.StatusOK, `{"items":[]}`), nil
	case req.Method == http.MethodGet && req.URL.Path == "/youtube/v3/liveStreams":
		f.steps = append(f.steps, "get_reusable_stream")
		f.reusableLiveStreamQuery = req.URL.RawQuery
		if req.URL.Query().Get("id") != "live-stream-static" || !hasParts(req, "id", "contentDetails") {
			return fakeHTTPResponse(req, http.StatusBadRequest, `{"error":{"message":"bad reusable stream lookup"}}`), nil
		}
		status := f.liveStreamListStatus
		if status == 0 {
			status = http.StatusOK
		}
		body := f.liveStreamListResponse
		if body == "" {
			body = `{"items":[{"id":"live-stream-static","contentDetails":{"isReusable":true}}]}`
		}
		return fakeHTTPResponse(req, status, body), nil
	case req.Method == http.MethodPost && req.URL.Path == "/youtube/v3/liveBroadcasts" && hasParts(req, "snippet", "status", "contentDetails"):
		f.steps = append(f.steps, "insert_broadcast")
		f.broadcastInsertBody = body
		if f.broadcastInsertResponseLost {
			return nil, errors.New("simulated response loss after liveBroadcasts.insert accepted request")
		}
		return fakeHTTPResponse(req, http.StatusOK, `{"id":"broadcast-01"}`), nil
	case req.Method == http.MethodPost && req.URL.Path == "/youtube/v3/liveStreams" && hasParts(req, "snippet", "cdn"):
		f.steps = append(f.steps, "insert_stream")
		f.accountReusableStreamCreated = true
		f.accountStreamInsertBody = body
		f.accountStreamInsertions++
		return fakeHTTPResponse(req, http.StatusOK, `{"id":"live-stream-01","cdn":{"ingestionInfo":{"rtmpsIngestionAddress":"rtmps://a.rtmps.youtube.com/live2","streamName":"runtime-stream-key"}}}`), nil
	case req.Method == http.MethodPost && req.URL.Path == "/youtube/v3/liveBroadcasts/bind":
		f.steps = append(f.steps, "bind_broadcast")
		f.boundStreamID = req.URL.Query().Get("streamId")
		if req.URL.Query().Get("id") != "broadcast-01" || (f.boundStreamID != "live-stream-01" && f.boundStreamID != "live-stream-static") {
			return fakeHTTPResponse(req, http.StatusBadRequest, `{"error":{"message":"bad bind"}}`), nil
		}
		if f.bindStatus != 0 {
			return fakeHTTPResponse(req, f.bindStatus, `{"error":{"message":"bind failed"}}`), nil
		}
		return fakeHTTPResponse(req, http.StatusOK, `{"id":"broadcast-01"}`), nil
	case req.Method == http.MethodDelete && req.URL.Path == "/youtube/v3/liveBroadcasts":
		f.steps = append(f.steps, "delete_broadcast")
		if req.URL.Query().Get("id") != "broadcast-01" {
			return fakeHTTPResponse(req, http.StatusBadRequest, `{"error":{"message":"bad delete"}}`), nil
		}
		if f.deleteBroadcastStatus != 0 {
			return fakeHTTPResponse(req, f.deleteBroadcastStatus, `{"error":{"message":"delete failed"}}`), nil
		}
		if f.deleteBroadcastResponseLost {
			return nil, errors.New("simulated response loss after liveBroadcasts.delete accepted request")
		}
		return fakeHTTPResponse(req, http.StatusNoContent, ""), nil
	case req.Method == http.MethodGet && req.URL.Path == "/youtube/v3/liveBroadcasts":
		f.steps = append(f.steps, "get_broadcast_status")
		if req.URL.Query().Get("id") != "broadcast-01" || !hasParts(req, "id", "status") {
			return fakeHTTPResponse(req, http.StatusBadRequest, `{"error":{"message":"bad broadcast status lookup"}}`), nil
		}
		status := f.broadcastListStatus
		if status == 0 {
			status = http.StatusOK
		}
		body := f.broadcastListResponse
		if body == "" {
			body = `{"items":[{"id":"broadcast-01","status":{"lifeCycleStatus":"complete"}}]}`
		}
		return fakeHTTPResponse(req, status, body), nil
	case req.Method == http.MethodPost && req.URL.Path == "/youtube/v3/liveBroadcasts/transition":
		broadcastStatus := req.URL.Query().Get("broadcastStatus")
		step := "complete_broadcast"
		if broadcastStatus == "live" {
			step = "live_broadcast"
		}
		f.steps = append(f.steps, step)
		if req.URL.Query().Get("id") != "broadcast-01" || (broadcastStatus != "complete" && broadcastStatus != "live") {
			return fakeHTTPResponse(req, http.StatusBadRequest, `{"error":{"message":"bad transition"}}`), nil
		}
		if f.completeBroadcastResponseLost {
			return nil, errors.New("simulated response loss after liveBroadcasts.transition accepted request")
		}
		if f.completeBroadcastStatus != 0 {
			return fakeHTTPResponse(req, f.completeBroadcastStatus, `{"error":{"errors":[{"reason":"redundantTransition"}],"code":403,"message":"transition already accepted"}}`), nil
		}
		return fakeHTTPResponse(req, http.StatusOK, `{"id":"broadcast-01","status":{"lifeCycleStatus":"complete"}}`), nil
	default:
		return fakeHTTPResponse(req, http.StatusNotFound, `{"error":{"message":"unexpected request"}}`), nil
	}
}

func hasParts(req *http.Request, want ...string) bool {
	parts := make(map[string]bool, len(want))
	for _, part := range req.URL.Query()["part"] {
		for _, value := range strings.Split(part, ",") {
			parts[value] = true
		}
	}
	for _, part := range want {
		if !parts[part] {
			return false
		}
	}
	return true
}

func (f *fakeYouTubeRoundTripper) saw(step string) bool {
	for _, seen := range f.steps {
		if seen == step {
			return true
		}
	}
	return false
}

func fakeHTTPResponse(req *http.Request, status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Status:     fmt.Sprintf("%d %s", status, http.StatusText(status)),
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(bytes.NewBufferString(body)),
		Request:    req,
	}
}
