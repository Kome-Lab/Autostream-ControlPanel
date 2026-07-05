package youtube

import (
	"bytes"
	"context"
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

type fakeYouTubeRoundTripper struct {
	tokenRefreshes      int
	steps               []string
	apiRequests         []fakeYouTubeAPIRequest
	broadcastInsertBody string
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
	case req.Method == http.MethodPost && req.URL.Path == "/youtube/v3/liveBroadcasts" && hasParts(req, "snippet", "status", "contentDetails"):
		f.steps = append(f.steps, "insert_broadcast")
		f.broadcastInsertBody = body
		return fakeHTTPResponse(req, http.StatusOK, `{"id":"broadcast-01"}`), nil
	case req.Method == http.MethodPost && req.URL.Path == "/youtube/v3/liveStreams" && hasParts(req, "snippet", "cdn"):
		f.steps = append(f.steps, "insert_stream")
		return fakeHTTPResponse(req, http.StatusOK, `{"id":"live-stream-01","cdn":{"ingestionInfo":{"rtmpsIngestionAddress":"rtmps://a.rtmps.youtube.com/live2","streamName":"runtime-stream-key"}}}`), nil
	case req.Method == http.MethodPost && req.URL.Path == "/youtube/v3/liveBroadcasts/bind":
		f.steps = append(f.steps, "bind_broadcast")
		if req.URL.Query().Get("id") != "broadcast-01" || req.URL.Query().Get("streamId") != "live-stream-01" {
			return fakeHTTPResponse(req, http.StatusBadRequest, `{"error":{"message":"bad bind"}}`), nil
		}
		return fakeHTTPResponse(req, http.StatusOK, `{"id":"broadcast-01"}`), nil
	case req.Method == http.MethodPost && req.URL.Path == "/youtube/v3/liveBroadcasts/transition":
		f.steps = append(f.steps, "complete_broadcast")
		if req.URL.Query().Get("id") != "broadcast-01" || req.URL.Query().Get("broadcastStatus") != "complete" {
			return fakeHTTPResponse(req, http.StatusBadRequest, `{"error":{"message":"bad transition"}}`), nil
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
