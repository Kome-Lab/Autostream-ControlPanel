package observability

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

type Client struct {
	BaseURL string
	Token   string
	Timeout time.Duration
	HTTP    *http.Client
}

type RemediationDispatchContext struct {
	ActionID       string `json:"action_id"`
	Action         string `json:"action"`
	ActionStatus   string `json:"action_status"`
	IncidentID     string `json:"incident_id"`
	IncidentStatus string `json:"incident_status"`
	StreamID       string `json:"stream_id"`
	Executable     bool   `json:"executable"`
}

func FromEnv() Client {
	return Client{
		Timeout: envDuration("OBSERVABILITY_TIMEOUT_SEC", 5*time.Second),
	}
}

func (c Client) Enabled() bool {
	return strings.TrimSpace(c.BaseURL) != "" && strings.TrimSpace(c.Token) != ""
}

func (c Client) Validate() error {
	if strings.TrimSpace(c.BaseURL) == "" {
		return errors.New("observability URL is required")
	}
	if strings.TrimSpace(c.Token) == "" {
		return errors.New("observability token is required")
	}
	parsed, err := url.Parse(c.BaseURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return errors.New("observability URL must be an absolute URL")
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return errors.New("observability URL must use http or https")
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return errors.New("observability URL must not include userinfo, query, or fragment")
	}
	if parsed.Scheme == "http" && !isLocalObservabilityHost(parsed.Hostname()) {
		return errors.New("observability URL must use https for remote hosts")
	}
	return nil
}

func (c Client) Get(ctx context.Context, endpoint string) (json.RawMessage, error) {
	return c.do(ctx, http.MethodGet, endpoint, nil)
}

func (c Client) Post(ctx context.Context, endpoint string, payload any) (json.RawMessage, error) {
	return c.sendJSON(ctx, http.MethodPost, endpoint, payload)
}

func (c Client) Put(ctx context.Context, endpoint string, payload any) (json.RawMessage, error) {
	return c.sendJSON(ctx, http.MethodPut, endpoint, payload)
}

func (c Client) Delete(ctx context.Context, endpoint string) (json.RawMessage, error) {
	return c.do(ctx, http.MethodDelete, endpoint, nil)
}

func (c Client) ValidateRemediationDispatchContext(ctx context.Context, actionID, action, incidentID, streamID string) error {
	actionID = strings.TrimSpace(actionID)
	action = strings.TrimSpace(action)
	incidentID = strings.TrimSpace(incidentID)
	streamID = strings.TrimSpace(streamID)
	if actionID == "" || action == "" || incidentID == "" || streamID == "" {
		return errors.New("remediation dispatch context is incomplete")
	}
	if !c.Enabled() {
		return errors.New("observability validation is not configured")
	}
	body, err := c.Get(ctx, "/remediation-actions/"+url.PathEscape(actionID)+"/dispatch-context")
	if err != nil {
		return fmt.Errorf("observability remediation context validation failed: %w", err)
	}
	var got RemediationDispatchContext
	if err := json.Unmarshal(body, &got); err != nil {
		return errors.New("observability remediation context response is invalid")
	}
	if strings.TrimSpace(got.ActionID) != actionID ||
		strings.TrimSpace(got.Action) != action ||
		strings.TrimSpace(got.IncidentID) != incidentID ||
		strings.TrimSpace(got.StreamID) != streamID ||
		!got.Executable {
		return errors.New("observability remediation context mismatch")
	}
	return nil
}

func (c Client) sendJSON(ctx context.Context, method, endpoint string, payload any) (json.RawMessage, error) {
	var body io.Reader
	if payload != nil {
		encoded, err := json.Marshal(payload)
		if err != nil {
			return nil, err
		}
		body = bytes.NewReader(encoded)
	}
	return c.do(ctx, method, endpoint, body)
}

func (c Client) do(ctx context.Context, method, endpoint string, body io.Reader) (json.RawMessage, error) {
	if err := c.Validate(); err != nil {
		return nil, err
	}
	reqCtx := ctx
	if c.Timeout > 0 {
		var cancel context.CancelFunc
		reqCtx, cancel = context.WithTimeout(ctx, c.Timeout)
		defer cancel()
	}
	req, err := http.NewRequestWithContext(reqCtx, method, joinURL(c.BaseURL, endpoint), body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	client := c.HTTP
	if client == nil {
		client = &http.Client{CheckRedirect: rejectObservabilityRedirect}
	}
	res, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return nil, fmt.Errorf("observability request failed with status %d", res.StatusCode)
	}
	response, err := io.ReadAll(io.LimitReader(res.Body, 4*1024*1024))
	if err != nil {
		return nil, err
	}
	if len(response) == 0 {
		response = []byte("null")
	}
	return json.RawMessage(response), nil
}

func rejectObservabilityRedirect(req *http.Request, via []*http.Request) error {
	return http.ErrUseLastResponse
}

func isLocalObservabilityHost(host string) bool {
	host = strings.TrimSpace(strings.ToLower(host))
	switch host {
	case "localhost", "127.0.0.1", "::1", "host.docker.internal":
		return true
	default:
		return false
	}
}

func joinURL(baseURL, endpoint string) string {
	return strings.TrimRight(baseURL, "/") + "/" + strings.TrimLeft(endpoint, "/")
}

func envDuration(key string, fallback time.Duration) time.Duration {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	duration, err := time.ParseDuration(value + "s")
	if err != nil || duration <= 0 {
		return fallback
	}
	return duration
}
