package youtube

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
)

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
		!strings.Contains(transport.reusableLiveStreamQuery, "cdn") ||
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

func TestLiveAPIClientPrepareRelayStaticRejectsReusableLiveStreamFormatMismatch(t *testing.T) {
	transport := &fakeYouTubeRoundTripper{
		liveStreamListResponse: `{
			"items": [{
				"id":"live-stream-static",
				"contentDetails":{"isReusable":true},
				"cdn":{"resolution":"2160p","frameRate":"60fps"}
			}]
		}`,
	}
	client := LiveAPIClient{HTTPClient: &http.Client{Transport: transport}}

	_, err := client.PrepareRelayStatic(context.Background(), RelayStaticPrepareRequest{
		PrepareRequest: PrepareRequest{
			Credentials: OAuthCredentials{
				ClientID:     "youtube-client-id",
				ClientSecret: "youtube-client-secret",
				RefreshToken: "youtube-refresh-token",
			},
			Resolution: "1080p",
			FrameRate:  "60fps",
		},
		ReusableLiveStreamID: "live-stream-static",
	})
	if err == nil || err.Error() != "youtube_reusable_live_stream_format_mismatch" {
		t.Fatalf("mismatched reusable stream format error = %v", err)
	}
	if transport.saw("insert_broadcast") || transport.saw("bind_broadcast") {
		t.Fatalf("format mismatch created or bound a Broadcast: %#v", transport.steps)
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
