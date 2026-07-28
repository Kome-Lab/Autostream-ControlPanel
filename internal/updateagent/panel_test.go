package updateagent

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestJobReportPortReconfigureExposesOnlyPublicResult(t *testing.T) {
	payload, err := json.Marshal(JobReport{
		ServiceID:       "host-agent-a",
		LeaseToken:      "lease-token",
		Sequence:        3,
		LeaseGeneration: 2,
		Status:          "succeeded",
		Progress:        100,
		PortReconfigure: &PortReconfigurationJobReport{Result: systemdPortResultApplied},
	})
	if err != nil {
		t.Fatal(err)
	}
	var report map[string]json.RawMessage
	if err := json.Unmarshal(payload, &report); err != nil {
		t.Fatal(err)
	}
	var port map[string]json.RawMessage
	if err := json.Unmarshal(report["port_reconfigure"], &port); err != nil {
		t.Fatal(err)
	}
	if len(port) != 1 || string(port["result"]) != `"applied"` {
		t.Fatalf("public port result leaked local reconciliation state: %s", payload)
	}
}

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

func TestConsumeMutationGrantNeverFollowsRedirect(t *testing.T) {
	var redirectedCalls atomic.Int32
	redirectTarget := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		redirectedCalls.Add(1)
		if authorization := r.Header.Get("Authorization"); authorization != "" {
			t.Errorf("mutation grant reached redirect target: %q", authorization)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer redirectTarget.Close()

	redirectSource := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Location", redirectTarget.URL+"/credential-capture")
		w.WriteHeader(http.StatusTemporaryRedirect)
	}))
	defer redirectSource.Close()

	err := ConsumeMutationGrant(
		context.Background(),
		redirectSource.URL,
		"job-one",
		"one-time-mutation-grant",
		MutationGrantBinding{},
		redirectSource.Client(),
	)
	var panelError *PanelHTTPError
	if !errors.As(err, &panelError) || panelError.Status != http.StatusTemporaryRedirect {
		t.Fatalf("redirect response error = %v", err)
	}
	if redirectedCalls.Load() != 0 {
		t.Fatalf("mutation grant followed %d redirects", redirectedCalls.Load())
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
