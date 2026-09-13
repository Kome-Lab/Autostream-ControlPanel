package httpapi

import (
	"context"
	"encoding/json"
	"github.com/example/autostream-control-panel/internal/observability"
	"github.com/example/autostream-control-panel/internal/store"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func assertAuditFailureReason(t *testing.T, events []store.AuditEvent, action, resourceID, reason string) {
	t.Helper()
	for _, event := range events {
		if event.Action == action && event.ResourceID == resourceID && event.Result == "failure" && event.Metadata["reason"] == reason {
			return
		}
	}
	t.Fatalf("missing audit failure action=%q resource=%q reason=%q: %#v", action, resourceID, reason, events)
}

func toJSONForTest(t *testing.T, value any) string {
	t.Helper()
	body, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

type captureMailer struct {
	messages  []MailMessage
	settings  []store.AppSettings
	passwords []string
	err       error
}

func (m *captureMailer) Send(_ context.Context, settings store.AppSettings, password string, message MailMessage) error {
	m.settings = append(m.settings, settings)
	m.passwords = append(m.passwords, password)
	m.messages = append(m.messages, message)
	return m.err
}

type fakeTurnstileVerifier struct {
	requests []TurnstileVerifyRequest
	result   TurnstileVerifyResult
	err      error
}

func (f *fakeTurnstileVerifier) Verify(_ context.Context, req TurnstileVerifyRequest) (TurnstileVerifyResult, error) {
	f.requests = append(f.requests, req)
	if f.err != nil {
		return TurnstileVerifyResult{}, f.err
	}
	return f.result, nil
}

func remediationValidationClient(t *testing.T, expectedToken string, contexts map[string]observability.RemediationDispatchContext) (observability.Client, func()) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+expectedToken {
			t.Fatalf("unexpected observability auth header: %q", r.Header.Get("Authorization"))
		}
		if r.Method != http.MethodGet || !strings.HasPrefix(r.URL.Path, "/remediation-actions/") || !strings.HasSuffix(r.URL.Path, "/dispatch-context") {
			http.NotFound(w, r)
			return
		}
		actionID := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/remediation-actions/"), "/dispatch-context")
		context, ok := contexts[actionID]
		if !ok {
			writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found"})
			return
		}
		writeJSON(w, http.StatusOK, context)
	}))
	return observability.Client{BaseURL: server.URL, Token: expectedToken, Timeout: time.Second, HTTP: server.Client()}, server.Close
}

type failingArtifactReportStreamStore struct {
	*store.MemoryStreamStore
	err error
}

func (s failingArtifactReportStreamStore) WriteStreamArtifactReport(context.Context, store.ServiceToken, store.ServiceStreamEvent, []store.StreamArtifact) error {
	return s.err
}
