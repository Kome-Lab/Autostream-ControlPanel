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
)

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
	ingestHealthBroadcastStatus   int
	ingestHealthBroadcastResponse string
	ingestHealthStreamStatus      int
	ingestHealthStreamResponse    string
	accountReusableStreamCreated  bool
	accountReusableStreamResponse string
	accountStreamInsertBody       string
	accountStreamInsertions       int
}

type fakeYouTubeAPIRequest struct {
	Method        string
	Path          string
	RawQuery      string
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

	f.apiRequests = append(f.apiRequests, fakeYouTubeAPIRequest{Method: req.Method, Path: req.URL.Path, RawQuery: req.URL.RawQuery, Authorization: req.Header.Get("Authorization")})
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
	case req.Method == http.MethodGet && req.URL.Path == "/youtube/v3/liveStreams" && req.URL.Query().Get("id") == "live-stream-01" && hasParts(req, "id", "cdn", "status"):
		f.steps = append(f.steps, "get_ingest_health")
		status := f.ingestHealthStreamStatus
		if status == 0 {
			status = http.StatusOK
		}
		response := f.ingestHealthStreamResponse
		if response == "" {
			response = `{"items":[{"id":"live-stream-01","cdn":{"resolution":"1080p","frameRate":"60fps"},"status":{"streamStatus":"active","healthStatus":{"status":"bad","lastUpdateTimeSeconds":"1787029583","configurationIssues":[{"type":"noAudioStream","severity":"error","description":"provider free-form details must not be retained"},{"type":"videoResolutionSuboptimal","severity":"warning","description":"Current resolution 1080x1080; expected 1920x1080. provider free-form details must not be retained"}]}}}]}`
		}
		return fakeHTTPResponse(req, status, response), nil
	case req.Method == http.MethodGet && req.URL.Path == "/youtube/v3/liveStreams":
		f.steps = append(f.steps, "get_reusable_stream")
		f.reusableLiveStreamQuery = req.URL.RawQuery
		if req.URL.Query().Get("id") != "live-stream-static" || !hasParts(req, "id", "contentDetails", "cdn") {
			return fakeHTTPResponse(req, http.StatusBadRequest, `{"error":{"message":"bad reusable stream lookup"}}`), nil
		}
		status := f.liveStreamListStatus
		if status == 0 {
			status = http.StatusOK
		}
		body := f.liveStreamListResponse
		if body == "" {
			body = `{"items":[{"id":"live-stream-static","contentDetails":{"isReusable":true},"cdn":{"resolution":"1080p","frameRate":"60fps"}}]}`
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
		if req.URL.Query().Get("id") != "broadcast-01" || (f.boundStreamID != "live-stream-01" && f.boundStreamID != "live-stream-static" && f.boundStreamID != "live-stream-custom") {
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
	case req.Method == http.MethodGet && req.URL.Path == "/youtube/v3/liveBroadcasts" && hasParts(req, "id", "contentDetails"):
		f.steps = append(f.steps, "get_broadcast_binding")
		if req.URL.Query().Get("id") != "broadcast-01" {
			return fakeHTTPResponse(req, http.StatusBadRequest, `{"error":{"message":"bad broadcast binding lookup"}}`), nil
		}
		status := f.ingestHealthBroadcastStatus
		if status == 0 {
			status = http.StatusOK
		}
		response := f.ingestHealthBroadcastResponse
		if response == "" {
			response = `{"items":[{"id":"broadcast-01","contentDetails":{"boundStreamId":"live-stream-01"}}]}`
		}
		return fakeHTTPResponse(req, status, response), nil
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
