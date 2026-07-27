package updateagent

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestManagedBootstrapLoadsWithoutReleaseCredentialOrLocalPolicy(t *testing.T) {
	path := filepath.Join(t.TempDir(), "updater.json")
	payload := []byte(`{
  "panel_url": "https://panel.example.com",
  "node_id": "central-updater",
  "runtime_token": "runtime-secret",
  "service_name": "Central Updater"
}`)
	if err := os.WriteFile(path, payload, 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadManagedBootstrapConfig(path, false)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.IsManagedBootstrap() || cfg.EffectiveStateDir() != ManagedUpdaterStateDir {
		t.Fatalf("managed bootstrap = %v state=%q", cfg.IsManagedBootstrap(), cfg.EffectiveStateDir())
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("managed bootstrap validation: %v", err)
	}

	withLegacyField := append(payload[:len(payload)-1], []byte(`, "github_token": ""}`)...)
	if err := os.WriteFile(path, withLegacyField, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadManagedBootstrapConfig(path, false); err == nil || !strings.Contains(err.Error(), "identity fields only") {
		t.Fatalf("strict managed bootstrap accepted a legacy field: %v", err)
	}
}

func TestPanelFetchManagedPolicyUsesRevisionAndRequiresNoStore(t *testing.T) {
	var request ManagedPolicyRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/services/update-agent/policy" {
			t.Errorf("request = %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer runtime-secret" {
			t.Errorf("authorization = %q", r.Header.Get("Authorization"))
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
		}
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
		  "updater_id":"central-updater",
		  "revision":7,
		  "api":{"bind_host":"127.0.0.1","host":"127.0.0.1","port":8090,"ssl_enabled":false},
		  "poll_interval_seconds":15,
		  "heartbeat_interval_seconds":30,
		  "hosts":[{
		    "host_id":"edge-01","name":"Edge 01","address":"192.0.2.10","port":55850,
		    "user":"autostream-update-host","arch":"amd64","host_public_key":"ssh-ed25519 AAAATEST",
		    "host_public_key_fingerprint":"SHA256:test"
		  }],
		  "targets":[{"target_id":"worker-01","host_id":"edge-01","service_type":"worker","deployment_mode":"systemd"}],
		  "updated_at":"2026-07-25T00:00:00Z"
		}`))
	}))
	defer server.Close()

	client := PanelClient{BaseURL: server.URL, Token: "runtime-secret", HTTP: server.Client()}
	policy, changed, err := client.FetchManagedPolicy(context.Background(), "central-updater", 6)
	if err != nil {
		t.Fatal(err)
	}
	if !changed || policy == nil || policy.Revision != 7 || policy.UpdaterID != "central-updater" {
		t.Fatalf("policy = %#v changed=%v", policy, changed)
	}
	if request.ServiceID != "central-updater" || request.CurrentRevision != 6 {
		t.Fatalf("request = %#v", request)
	}
}

func TestPanelFetchManagedPolicyRejectsCacheableOrUnknownResponse(t *testing.T) {
	for _, test := range []struct {
		name        string
		cache       string
		body        string
		wantMessage string
	}{
		{name: "cacheable", body: `{"updater_id":"central-updater","revision":2}`, wantMessage: "no-store"},
		{name: "unknown field", cache: "no-store", body: `{"updater_id":"central-updater","revision":2,"release_token":"must-not-be-here"}`, wantMessage: "decode"},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				if test.cache != "" {
					w.Header().Set("Cache-Control", test.cache)
				}
				_, _ = w.Write([]byte(test.body))
			}))
			defer server.Close()
			_, _, err := (PanelClient{BaseURL: server.URL, Token: "runtime", HTTP: server.Client()}).FetchManagedPolicy(context.Background(), "central-updater", 1)
			if err == nil || !strings.Contains(err.Error(), test.wantMessage) || strings.Contains(err.Error(), "must-not-be-here") {
				t.Fatalf("error = %v", err)
			}
		})
	}
}

func TestClaimReleaseTokenIsMemoryOnlyAndRedacted(t *testing.T) {
	const releaseToken = "github-release-job-secret"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write([]byte(`{
		  "job":{"id":"job-one","host_id":"edge-01","target_id":"worker-01","service_type":"worker",
		    "deployment_mode":"systemd","current_version":"v1.0.0","target_version":"v1.1.0"},
		  "lease_token":"lease-secret","release_token":"` + releaseToken + `",
		  "lease_generation":1,"report_sequence":1
		}`))
	}))
	defer server.Close()
	job, _, err := (PanelClient{BaseURL: server.URL, Token: "runtime", HTTP: server.Client()}).ClaimHost(context.Background(), "central-updater", "edge-01", "")
	if err != nil {
		t.Fatal(err)
	}
	if job.ReleaseToken.Reveal() != releaseToken {
		t.Fatal("claim did not retain the one-use release token in memory")
	}
	if strings.Contains(fmt.Sprintf("%v %#v", job.ReleaseToken, job.ReleaseToken), releaseToken) {
		t.Fatal("release token formatting was not redacted")
	}
	stateDir := t.TempDir()
	journal, err := OpenJournal(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := journal.SetActive(job); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(stateDir, "journal.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), releaseToken) || strings.Contains(string(data), "[REDACTED]") || strings.Contains(string(data), "release_token") {
		t.Fatalf("journal persisted release token material: %s", data)
	}
}

func TestCoordinatorPolicyStatusCapabilitiesAreSafe(t *testing.T) {
	cfg := Config{
		PolicyRevision:                    4,
		PolicyStatus:                      PolicyStatusApplied,
		SSHClientPublicKeys:               map[string]string{"edge-01": "ssh-ed25519 AAAATEST central-updater"},
		SSHClientKeyFingerprints:          map[string]string{"edge-01": "SHA256:client"},
		BootstrapEncryptionPublicKey:      "BAc-public-key",
		BootstrapEncryptionKeyFingerprint: "SHA256:bootstrap",
	}
	capabilities := coordinatorCapabilities(cfg, nil, nil)
	if capabilities["policy_revision"] != int64(4) || capabilities["policy_status"] != PolicyStatusApplied {
		t.Fatalf("policy capabilities = %#v", capabilities)
	}
	if capabilities["policy_desired_revision"] != int64(4) {
		t.Fatalf("desired policy revision = %#v", capabilities["policy_desired_revision"])
	}
	if fmt.Sprint(capabilities["ssh_client_public_keys"]) == "" || fmt.Sprint(capabilities["ssh_client_key_fingerprints"]) == "" {
		t.Fatalf("SSH client identity capabilities = %#v", capabilities)
	}
	if capabilities["bootstrap_encryption_public_key"] != "BAc-public-key" ||
		capabilities["bootstrap_encryption_key_fingerprint"] != "SHA256:bootstrap" {
		t.Fatalf("bootstrap envelope identity capabilities = %#v", capabilities)
	}

	cfg.PolicyStatus = PolicyStatusFailed
	cfg.PolicyErrorCode = "raw failure must not escape"
	capabilities = coordinatorCapabilities(cfg, nil, nil)
	if capabilities["policy_error_code"] != PolicyErrorInvalid {
		t.Fatalf("unsafe policy error was reported: %#v", capabilities)
	}
}

func TestCoordinatorPendingPolicyDoesNotAdvanceAppliedRevision(t *testing.T) {
	coordinator := &CentralCoordinator{Config: Config{
		PolicyRevision: 4, PolicyDesiredRevision: 4, PolicyStatus: PolicyStatusApplied,
	}}
	coordinator.SetPolicyStatus(5, PolicyStatusPending, "", nil, nil)
	capabilities := coordinatorCapabilities(coordinator.Config, nil, nil)
	if capabilities["policy_revision"] != int64(4) || capabilities["policy_desired_revision"] != int64(5) || capabilities["policy_status"] != PolicyStatusPending {
		t.Fatalf("pending policy capabilities = %#v", capabilities)
	}
	coordinator.SetPolicyStatus(5, PolicyStatusPending, PolicyErrorActiveJob, nil, nil)
	capabilities = coordinatorCapabilities(coordinator.Config, nil, nil)
	if capabilities["policy_error_code"] != PolicyErrorActiveJob {
		t.Fatalf("pending active-job reason = %#v", capabilities)
	}
	coordinator.SetPolicyStatus(5, PolicyStatusApplied, "", nil, nil)
	capabilities = coordinatorCapabilities(coordinator.Config, nil, nil)
	if capabilities["policy_revision"] != int64(5) || capabilities["policy_status"] != PolicyStatusApplied {
		t.Fatalf("applied policy capabilities = %#v", capabilities)
	}
}

func TestManagedPolicyResponseUpdatedAtAcceptsRFC3339(t *testing.T) {
	var policy ManagedPolicy
	if err := json.Unmarshal([]byte(`{"updater_id":"central-updater","revision":1,"updated_at":"2026-07-25T12:34:56Z"}`), &policy); err != nil {
		t.Fatal(err)
	}
	if policy.UpdatedAt.UTC() != time.Date(2026, 7, 25, 12, 34, 56, 0, time.UTC) {
		t.Fatalf("updated_at = %s", policy.UpdatedAt)
	}
}
