package updateagent

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPanelAuthorizeBindsLeaseAndTarget(t *testing.T) {
	var got map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.EscapedPath() != "/services/update-jobs/job%2Fone/authorize" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.EscapedPath())
		}
		if r.Header.Get("Authorization") != "Bearer runtime-token" {
			t.Errorf("authorization header = %q", r.Header.Get("Authorization"))
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Error(err)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	body := map[string]any{
		"service_id":       "updater-1",
		"lease_token":      "lease-token",
		"lease_generation": uint64(7),
		"target_id":        "worker",
		"target_version":   "v2.0.0",
		"deployment_mode":  ModeDocker,
	}
	client := PanelClient{BaseURL: server.URL, Token: "runtime-token", HTTP: server.Client()}
	if err := client.Authorize(context.Background(), "job/one", body); err != nil {
		t.Fatal(err)
	}
	for key, want := range body {
		if key == "lease_generation" {
			if got[key] != float64(want.(uint64)) {
				t.Fatalf("%s = %#v, want %#v", key, got[key], want)
			}
			continue
		}
		if got[key] != want {
			t.Fatalf("%s = %#v, want %#v", key, got[key], want)
		}
	}
}

func TestPanelAuthorizePropagatesConflictCode(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte(`{"code":"system_update_authorization_mismatch"}`))
	}))
	defer server.Close()

	client := PanelClient{BaseURL: server.URL, Token: "runtime-token", HTTP: server.Client()}
	err := client.Authorize(context.Background(), "job-one", map[string]any{})
	var httpErr *PanelHTTPError
	if !errors.As(err, &httpErr) || httpErr.Status != http.StatusConflict || httpErr.Code != "system_update_authorization_mismatch" {
		t.Fatalf("authorize error = %#v", err)
	}
}

func TestPanelErrorDoesNotExposeUntrustedResponseBody(t *testing.T) {
	const secret = "lease-token-must-not-reach-logs"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte(`{"code":"` + secret + `","reflected":"` + secret + `"}`))
	}))
	defer server.Close()

	client := PanelClient{BaseURL: server.URL, Token: "runtime-token", HTTP: server.Client()}
	err := client.Authorize(context.Background(), "job-one", map[string]any{})
	if err == nil || strings.Contains(err.Error(), secret) {
		t.Fatalf("panel error exposed untrusted response: %v", err)
	}
	var httpErr *PanelHTTPError
	if !errors.As(err, &httpErr) || httpErr.Code != "" || httpErr.Status != http.StatusBadGateway {
		t.Fatalf("panel error = %#v", err)
	}
}

func TestPanelErrorAllowsMutationGrantConsumptionContractCode(t *testing.T) {
	const code = "invalid_system_update_mutation_grant_consumption"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"code":"` + code + `"}`))
	}))
	defer server.Close()

	client := PanelClient{BaseURL: server.URL, Token: "runtime-token", HTTP: server.Client()}
	err := client.Authorize(context.Background(), "job-one", map[string]any{})
	var httpErr *PanelHTTPError
	if !errors.As(err, &httpErr) || httpErr.Status != http.StatusBadRequest || httpErr.Code != code {
		t.Fatalf("panel error = %#v", err)
	}
}

func TestAuthorizeApplyPlanRejectsMissingLeaseBeforeNetwork(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls++ }))
	defer server.Close()

	err := authorizeApplyPlan(context.Background(), Config{PanelURL: server.URL}, ApplyPlan{JobID: "job-one", TargetID: "worker", TargetVersion: "v2.0.0", DeploymentMode: ModeDocker})
	if err == nil || calls != 0 {
		t.Fatalf("err=%v calls=%d", err, calls)
	}
}
