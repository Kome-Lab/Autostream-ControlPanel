package updateagent

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"
)

type updaterRoundTripFunc func(*http.Request) (*http.Response, error)

func (f updaterRoundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func TestMergeUpdaterConfiguredIdentityPreservesLocalPolicy(t *testing.T) {
	cfg := validCentralTestConfig(t)
	cfg.PanelURL = "https://old-panel.example.com"
	cfg.NodeID = "old-updater"
	cfg.RuntimeToken = "old-runtime-token"
	cfg.ServiceName = "Old Updater"
	cfg.GitHubToken = "github-local-secret"
	cfg.API = APIConfig{BindHost: "127.0.0.1", Host: "updater.internal", Port: 9443, SSLEnabled: true, TLSCertFile: "/etc/autostream/tls/updater.crt", TLSKeyFile: "/etc/autostream/tls/updater.key"}
	cfg.StateDir = "/var/lib/custom-updater"
	cfg.PollIntervalSeconds = 23
	cfg.HeartbeatIntervalSeconds = 47
	existing, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	identity := UpdaterConfigureIdentity{
		PanelURL: "https://new-panel.example.com", NodeID: "central-updater", RuntimeToken: "new-runtime-token", ServiceName: "Central Updater", ServiceType: "update_agent",
		API: APIConfig{Host: "panel-supplied.example.com", Port: 8090, SSLEnabled: true},
	}

	merged, err := mergeUpdaterConfiguredIdentity(existing, identity)
	if err != nil {
		t.Fatal(err)
	}
	var got Config
	if err := json.Unmarshal(merged, &got); err != nil {
		t.Fatal(err)
	}
	if got.PanelURL != identity.PanelURL || got.NodeID != identity.NodeID || got.RuntimeToken != identity.RuntimeToken || got.ServiceName != identity.ServiceName {
		t.Fatalf("configured identity = %#v", got)
	}
	gotTargets, err := json.Marshal(got.Targets)
	if err != nil {
		t.Fatal(err)
	}
	wantTargets, err := json.Marshal(cfg.Targets)
	if err != nil {
		t.Fatal(err)
	}
	if got.GitHubToken != cfg.GitHubToken || got.StateDir != cfg.StateDir || got.PollIntervalSeconds != cfg.PollIntervalSeconds || got.HeartbeatIntervalSeconds != cfg.HeartbeatIntervalSeconds || !reflect.DeepEqual(got.API, cfg.API) || !reflect.DeepEqual(got.Hosts, cfg.Hosts) || string(gotTargets) != string(wantTargets) {
		t.Fatalf("local policy changed: got=%#v want=%#v", got, cfg)
	}
	if strings.Contains(string(merged), "configure-token") || strings.Contains(string(merged), "panel-supplied.example.com") {
		t.Fatalf("configure-only data entered updater config: %s", merged)
	}
}

func TestMergeUpdaterConfiguredIdentityRequiresExistingLocalPolicy(t *testing.T) {
	identity := UpdaterConfigureIdentity{PanelURL: "https://panel.example.com", NodeID: "central-updater", RuntimeToken: "runtime-token", ServiceName: "Central Updater", ServiceType: "update_agent"}
	if _, err := mergeUpdaterConfiguredIdentity(nil, identity); err == nil || !strings.Contains(err.Error(), "existing updater config") {
		t.Fatalf("missing local policy merge = %v", err)
	}
}

func TestStageUpdaterConfigurationUsesOneTimeTokenWithoutLocalPolicy(t *testing.T) {
	configureToken := "configure-token-must-not-return"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/node-agent/configure/stage" {
			t.Fatalf("stage path = %q", r.URL.Path)
		}
		var payload map[string]string
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		if payload["nodeId"] != "central-updater" || payload["configureToken"] != configureToken || len(payload) != 2 {
			t.Fatalf("stage request = %#v", payload)
		}
		for _, forbidden := range []string{"github_token", "hosts", "targets", "identity_file"} {
			if _, exists := payload[forbidden]; exists {
				t.Fatalf("configure request leaked %s: %#v", forbidden, payload)
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"configuration_id":"configuration-01","activation_token":"activation-token","activation_expires_at":"2099-01-01T00:00:00Z","config":{"panel_url":"` + serverURL(r) + `","node_id":"central-updater","runtime_token":"runtime-token","service_name":"Central Updater","service_type":"update_agent","api":{"host":"updater.example.com","port":8090,"ssl_enabled":true}}}`))
	}))
	defer server.Close()

	staged, err := StageUpdaterConfiguration(context.Background(), server.Client(), server.URL, "central-updater", configureToken, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if staged.Config.NodeID != "central-updater" || staged.Config.ServiceType != "update_agent" || staged.Config.RuntimeToken != "runtime-token" || staged.Config.API.Host != "updater.example.com" || staged.ConfigurationID != "configuration-01" || staged.ActivationToken != "activation-token" || staged.ActivationExpiresAt.IsZero() {
		t.Fatalf("staged configuration = %#v", staged)
	}
}

func TestStageUpdaterConfigurationRejectsMismatchWithoutLeakingToken(t *testing.T) {
	secret := "one-time-configure-secret"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"configuration_id":"configuration-01","activation_token":"activation-token","activation_expires_at":"2099-01-01T00:00:00Z","config":{"panel_url":"https://panel.example.com","node_id":"other-updater","runtime_token":"runtime-token","service_name":"Other","service_type":"worker","api":{"host":"127.0.0.1","port":8090,"ssl_enabled":false}}}`))
	}))
	defer server.Close()

	_, err := StageUpdaterConfiguration(context.Background(), server.Client(), server.URL, "central-updater", secret, time.Second)
	if err == nil || !strings.Contains(err.Error(), "does not match") || strings.Contains(err.Error(), secret) {
		t.Fatalf("mismatched configure response = %v", err)
	}
}

func TestStageUpdaterConfigurationRejectsPanelURLSubstitution(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"configuration_id":"configuration-01","activation_token":"activation-token","activation_expires_at":"2099-01-01T00:00:00Z","config":{"panel_url":"https://other-panel.example.com","node_id":"central-updater","runtime_token":"runtime-token","service_name":"Central Updater","service_type":"update_agent","api":{"host":"127.0.0.1","port":8090,"ssl_enabled":false}}}`))
	}))
	defer server.Close()
	_, err := StageUpdaterConfiguration(context.Background(), server.Client(), server.URL, "central-updater", "configure-secret", time.Second)
	if err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("panel URL substitution result = %v", err)
	}
}

func TestActivateUpdaterConfigurationRetriesTransientFailureWithSameSecret(t *testing.T) {
	staged := UpdaterStagedConfiguration{
		Config:              UpdaterConfigureIdentity{PanelURL: "", NodeID: "central-updater", RuntimeToken: "runtime-secret", ServiceName: "Central Updater", ServiceType: "update_agent"},
		ConfigurationID:     "configuration-01",
		ActivationToken:     "activation-secret",
		ActivationExpiresAt: time.Now().UTC().Add(time.Hour),
	}
	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/node-agent/configure/activate" || r.Header.Get("Authorization") != "" {
			t.Fatalf("activation request path=%q authorization=%q", r.URL.Path, r.Header.Get("Authorization"))
		}
		var payload map[string]string
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		if payload["nodeId"] != staged.Config.NodeID || payload["configurationId"] != staged.ConfigurationID || payload["activationToken"] != staged.ActivationToken || payload["hostname"] != "central-host" || payload["os"] != "linux" {
			t.Fatalf("activation payload = %#v", payload)
		}
		attempts++
		if attempts < 2 {
			http.Error(w, `{"code":"temporary"}`, http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte(`{"state":"activated","configuration_id":"configuration-01"}`))
	}))
	defer server.Close()
	client := server.Client()
	baseTransport := client.Transport
	transportAttempts := 0
	client.Transport = updaterRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		transportAttempts++
		if transportAttempts == 1 {
			return nil, errors.New("temporary transport failure")
		}
		return baseTransport.RoundTrip(request)
	})
	staged.Config.PanelURL = server.URL
	result, err := ActivateUpdaterConfiguration(context.Background(), client, server.URL, staged, UpdaterRuntimeReport{Version: "v1.7.0", Hostname: "central-host", OS: "linux", Arch: "amd64"}, 5*time.Second)
	if err != nil || result.State != "activated" || result.ConfigurationID != staged.ConfigurationID || attempts != 2 || transportAttempts != 3 {
		t.Fatalf("activation result=%#v err=%v handler_attempts=%d transport_attempts=%d", result, err, attempts, transportAttempts)
	}
}

func TestActivateUpdaterConfigurationFailsClosedWithoutLeakingSecrets(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusBadGateway} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				http.Error(w, `{"code":"activation_rejected"}`, status)
			}))
			defer server.Close()
			staged := UpdaterStagedConfiguration{
				Config:          UpdaterConfigureIdentity{PanelURL: server.URL, NodeID: "central-updater", RuntimeToken: "runtime-secret", ServiceName: "Central Updater", ServiceType: "update_agent"},
				ConfigurationID: "configuration-01", ActivationToken: "activation-secret", ActivationExpiresAt: time.Now().UTC().Add(time.Hour),
			}
			_, err := ActivateUpdaterConfiguration(context.Background(), server.Client(), server.URL, staged, UpdaterRuntimeReport{OS: "linux", Arch: "amd64"}, time.Second)
			if err == nil || strings.Contains(err.Error(), staged.ActivationToken) || strings.Contains(err.Error(), staged.Config.RuntimeToken) {
				t.Fatalf("activation error = %v", err)
			}
		})
	}
}

func serverURL(r *http.Request) string {
	return "http://" + r.Host
}
