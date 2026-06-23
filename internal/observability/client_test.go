package observability

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestClientGetAddsBearerToken(t *testing.T) {
	var auth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		_ = json.NewEncoder(w).Encode([]map[string]string{{"id": "inc-1"}})
	}))
	defer server.Close()

	client := Client{BaseURL: server.URL, Token: "secret-token", Timeout: time.Second}
	body, err := client.Get(t.Context(), "/incidents")
	if err != nil {
		t.Fatal(err)
	}
	if auth != "Bearer secret-token" {
		t.Fatalf("unexpected auth: %s", auth)
	}
	if !strings.Contains(string(body), "inc-1") {
		t.Fatalf("unexpected body: %s", string(body))
	}
}

func TestValidateRemediationDispatchContext(t *testing.T) {
	var gotAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		if r.URL.Path != "/remediation-actions/action-1/dispatch-context" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(RemediationDispatchContext{ActionID: "action-1", Action: "retry_package_remux", IncidentID: "inc-1", StreamID: "stream-1", Executable: true})
	}))
	defer server.Close()

	client := Client{BaseURL: server.URL, Token: "secret-token", Timeout: time.Second}
	if err := client.ValidateRemediationDispatchContext(t.Context(), "action-1", "retry_package_remux", "inc-1", "stream-1"); err != nil {
		t.Fatal(err)
	}
	if gotAuth != "Bearer secret-token" {
		t.Fatalf("unexpected auth: %s", gotAuth)
	}
}

func TestValidateRemediationDispatchContextRejectsMismatch(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(RemediationDispatchContext{ActionID: "action-1", Action: "retry_package_remux", IncidentID: "other", StreamID: "stream-1", Executable: true})
	}))
	defer server.Close()

	client := Client{BaseURL: server.URL, Token: "secret-token", Timeout: time.Second}
	err := client.ValidateRemediationDispatchContext(t.Context(), "action-1", "retry_package_remux", "inc-1", "stream-1")
	if err == nil || !strings.Contains(err.Error(), "context mismatch") {
		t.Fatalf("expected context mismatch, got %v", err)
	}
}

func TestValidateRemediationDispatchContextErrorDoesNotLeakTokenOrBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "secret-token and remediation detail", http.StatusForbidden)
	}))
	defer server.Close()

	client := Client{BaseURL: server.URL, Token: "secret-token", Timeout: time.Second}
	err := client.ValidateRemediationDispatchContext(t.Context(), "action-1", "retry_package_remux", "inc-1", "stream-1")
	if err == nil {
		t.Fatal("expected error")
	}
	if strings.Contains(err.Error(), "secret-token") || strings.Contains(err.Error(), "remediation detail") {
		t.Fatalf("sensitive upstream detail leaked: %v", err)
	}
}

func TestClientErrorDoesNotLeakTokenOrBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "secret-token", http.StatusForbidden)
	}))
	defer server.Close()

	client := Client{BaseURL: server.URL, Token: "secret-token", Timeout: time.Second}
	_, err := client.Get(t.Context(), "/incidents")
	if err == nil {
		t.Fatal("expected error")
	}
	if strings.Contains(err.Error(), "secret-token") {
		t.Fatalf("token leaked in error: %v", err)
	}
}

func TestFromEnv(t *testing.T) {
	t.Setenv("OBSERVABILITY_URL", "https://observability.example.com")
	t.Setenv("OBSERVABILITY_TOKEN", "<SERVICE_TOKEN>")
	t.Setenv("OBSERVABILITY_TIMEOUT_SEC", "3")
	client := FromEnv()
	if err := client.Validate(); err != nil {
		t.Fatal(err)
	}
	if client.Timeout != 3*time.Second {
		t.Fatalf("unexpected timeout: %s", client.Timeout)
	}
}

func TestValidateRejectsNonHTTPURL(t *testing.T) {
	client := Client{BaseURL: "ftp://observability.example.com/events", Token: "<SERVICE_TOKEN>", Timeout: time.Second}
	err := client.Validate()
	if err == nil {
		t.Fatal("expected validation error")
	}
	if !strings.Contains(err.Error(), "http or https") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateRejectsRemoteHTTPURL(t *testing.T) {
	client := Client{BaseURL: "http://observability.example.com", Token: "<SERVICE_TOKEN>", Timeout: time.Second}
	err := client.Validate()
	if err == nil {
		t.Fatal("expected validation error")
	}
	if !strings.Contains(err.Error(), "https for remote hosts") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateAllowsLocalHTTPURL(t *testing.T) {
	for _, baseURL := range []string{"http://localhost:8082", "http://127.0.0.1:8082", "http://[::1]:8082", "http://host.docker.internal:8082"} {
		client := Client{BaseURL: baseURL, Token: "<SERVICE_TOKEN>", Timeout: time.Second}
		if err := client.Validate(); err != nil {
			t.Fatalf("expected local HTTP URL to be allowed for %s: %v", baseURL, err)
		}
	}
}

func TestValidateRejectsURLComponentsThatCanLeakTokens(t *testing.T) {
	tests := []string{
		"https://user:pass@observability.example.com",
		"https://observability.example.com?api_key=<TOKEN>",
		"https://observability.example.com#fragment",
	}
	for _, baseURL := range tests {
		client := Client{BaseURL: baseURL, Token: "<SERVICE_TOKEN>", Timeout: time.Second}
		if err := client.Validate(); err == nil {
			t.Fatalf("expected validation error for %s", baseURL)
		}
	}
}

func TestClientDoesNotFollowRedirectWithBearerToken(t *testing.T) {
	var redirectedAuth string
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		redirectedAuth = r.Header.Get("Authorization")
		_ = json.NewEncoder(w).Encode(map[string]string{"ok": "true"})
	}))
	defer target.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+"/incidents", http.StatusFound)
	}))
	defer server.Close()

	client := Client{BaseURL: server.URL, Token: "secret-token", Timeout: time.Second}
	_, err := client.Get(t.Context(), "/incidents")
	if err == nil {
		t.Fatal("expected redirect response to be treated as an error")
	}
	if redirectedAuth != "" {
		t.Fatalf("bearer token was forwarded on redirect: %q", redirectedAuth)
	}
}
