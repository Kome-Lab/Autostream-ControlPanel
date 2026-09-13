package youtube

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"golang.org/x/oauth2"
	youtubeapi "google.golang.org/api/youtube/v3"
	"net/http"
	"strings"
	"testing"
	"time"
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
	wantSteps := []string{"insert_broadcast", "insert_stream", "bind_broadcast"}
	if fmt.Sprint(transport.steps) != fmt.Sprint(wantSteps) {
		t.Fatalf("fresh Live API lifecycle order=%#v want=%#v", transport.steps, wantSteps)
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

func TestLiveAPIClientPrepareBindsConfiguredReusableStreamKey(t *testing.T) {
	transport := &fakeYouTubeRoundTripper{
		accountReusableStreamResponse: `{"items":[{"id":"live-stream-custom","contentDetails":{"isReusable":true},"cdn":{"resolution":"1080p","frameRate":"60fps","ingestionInfo":{"rtmpsIngestionAddress":"rtmps://a.rtmps.youtube.com/live2","streamName":"operator-custom-key"}}}]}`,
	}
	httpClient := &http.Client{Transport: transport}
	client := LiveAPIClient{HTTPClient: httpClient}

	prepared, err := client.Prepare(context.Background(), PrepareRequest{
		Credentials: OAuthCredentials{
			ClientID:     "youtube-client-id",
			ClientSecret: "youtube-client-secret",
			RefreshToken: "youtube-refresh-token",
		},
		StreamID:           "stream-custom-key",
		StreamName:         "Custom key stream",
		Resolution:         "1080p",
		FrameRate:          "60fps",
		PreferredStreamKey: "operator-custom-key",
	})
	if err != nil {
		t.Fatal(err)
	}
	if prepared.LiveStreamID != "live-stream-custom" || prepared.StreamKey != "operator-custom-key" {
		t.Fatalf("configured reusable stream was not selected: %#v", prepared)
	}
	if transport.accountStreamInsertions != 0 || transport.saw("insert_stream") {
		t.Fatalf("configured reusable stream unexpectedly created another key: %#v", transport.steps)
	}
	if transport.boundStreamID != "live-stream-custom" {
		t.Fatalf("broadcast bound stream=%q", transport.boundStreamID)
	}
}

func TestLiveAPIClientPrepareConfiguredStreamKeyFailsClosed(t *testing.T) {
	for _, test := range []struct {
		name     string
		response string
		want     error
	}{
		{
			name:     "missing",
			response: `{"items":[]}`,
			want:     ErrPreferredStreamKeyNotFound,
		},
		{
			name:     "not reusable",
			response: `{"items":[{"id":"live-stream-custom","contentDetails":{"isReusable":false},"cdn":{"resolution":"1080p","frameRate":"60fps","ingestionInfo":{"rtmpsIngestionAddress":"rtmps://a.rtmps.youtube.com/live2","streamName":"operator-custom-key"}}}]}`,
			want:     ErrReusableLiveStreamNotReusable,
		},
		{
			name:     "format mismatch",
			response: `{"items":[{"id":"live-stream-custom","contentDetails":{"isReusable":true},"cdn":{"resolution":"720p","frameRate":"30fps","ingestionInfo":{"rtmpsIngestionAddress":"rtmps://a.rtmps.youtube.com/live2","streamName":"operator-custom-key"}}}]}`,
			want:     ErrReusableLiveStreamFormatMismatch,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			transport := &fakeYouTubeRoundTripper{accountReusableStreamResponse: test.response}
			client := LiveAPIClient{HTTPClient: &http.Client{Transport: transport}}
			_, err := client.Prepare(context.Background(), PrepareRequest{
				Credentials: OAuthCredentials{ClientID: "youtube-client-id", ClientSecret: "youtube-client-secret", RefreshToken: "youtube-refresh-token"},
				Resolution:  "1080p", FrameRate: "60fps", PreferredStreamKey: "operator-custom-key",
			})
			if !errors.Is(err, test.want) {
				t.Fatalf("error=%v want=%v", err, test.want)
			}
			if got := RedactedError(err); got != test.want.Error() {
				t.Fatalf("redacted error=%q want=%q", got, test.want.Error())
			}
			if strings.Contains(err.Error(), "operator-custom-key") || strings.Contains(RedactedError(err), "operator-custom-key") {
				t.Fatalf("configured stream key leaked through error: %v", err)
			}
			if transport.saw("insert_stream") || transport.saw("insert_broadcast") || transport.saw("bind_broadcast") {
				t.Fatalf("failed configured-key lookup mutated provider state: %#v", transport.steps)
			}
		})
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
