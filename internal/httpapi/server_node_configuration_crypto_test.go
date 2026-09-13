package httpapi

import (
	"bytes"
	"github.com/example/autostream-control-panel/internal/security"
	"github.com/example/autostream-control-panel/internal/store"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestNodeConfigureCommandUsesPOSIXShellQuoting(t *testing.T) {
	t.Setenv("AUTOSTREAM_PUBLIC_URL", "https://panel.example.com/it's/$(touch pwn)")
	command := nodeConfigureCommand(
		nil,
		store.RegisteredService{
			ServiceID:     "updater'$(touch pwn)`id`",
			ServiceType:   "update_agent",
			TransportMode: store.SystemUpdateTransportPullV2,
		},
		"ast_cfg_secret'$(touch token)",
		"/etc/autostream/updater'config.json",
	)
	for _, quoted := range []string{
		posixShellQuote("https://panel.example.com/it's/$(touch pwn)"),
		posixShellQuote("updater'$(touch pwn)`id`"),
		posixShellQuote("/etc/autostream/updater'config.json"),
	} {
		if !strings.Contains(command, quoted) {
			t.Fatalf("configure command omitted shell-safe argument %q: %s", quoted, command)
		}
	}
	if strings.Contains(command, `"$(touch`) || strings.Contains(command, "\"`id`\"") {
		t.Fatalf("configure command left command substitution in double quotes: %s", command)
	}
	if strings.Contains(command, "ast_cfg_secret") || strings.Contains(command, "--token") {
		t.Fatalf("updater configure command exposed the Configure Token in argv: %s", command)
	}
}

func TestNodeConfigurationYAMLScopesSigningKeyToOneTimeWorkerEncoderConfigs(t *testing.T) {
	t.Setenv("AUTOSTREAM_STREAM_INGEST_SIGNING_KEY", "test-stream-ingest-signing-key-32-bytes")
	request := httptest.NewRequest(http.MethodGet, "https://control.example.jp/nodes", nil)
	worker := store.RegisteredService{ServiceID: "worker-01", ServiceName: "Worker 01", ServiceType: "worker", Host: "worker.example.jp", Port: 8443, SSLEnabled: true}

	oneTime := nodeConfigurationYAML(request, worker, "token-id", "runtime-token")
	if !strings.Contains(oneTime, `stream_ingest:`) || !strings.Contains(oneTime, `signing_key: "test-stream-ingest-signing-key-32-bytes"`) {
		t.Fatalf("one-time worker config omitted signing key: %s", oneTime)
	}
	redacted := nodeConfigurationYAML(request, worker, "token-id", "")
	if strings.Contains(redacted, "test-stream-ingest-signing-key-32-bytes") || strings.Contains(redacted, "stream_ingest") {
		t.Fatalf("normal node config leaked signing key: %s", redacted)
	}
	discord := worker
	discord.ServiceType = "discord_bot"
	discordConfig := nodeConfigurationYAML(request, discord, "token-id", "runtime-token")
	if strings.Contains(discordConfig, "test-stream-ingest-signing-key-32-bytes") || strings.Contains(discordConfig, "stream_ingest") {
		t.Fatalf("discord config received an unrelated signing key: %s", discordConfig)
	}
}

func TestNodeAgentConfigureRejectsMissingEncryptionBeforeConsumingConfigureToken(t *testing.T) {
	t.Setenv("AUTOSTREAM_SECRET_ENCRYPTION_KEY", "")
	t.Setenv("AUTOSTREAM_STREAM_INGEST_SIGNING_KEY", "test-stream-ingest-signing-key-32-bytes")
	auth := store.NewMemoryAuthStore()
	token, err := auth.CreateServiceToken(t.Context(), "worker", []string{"service.register", "service.heartbeat", "service.config.read"})
	if err != nil {
		t.Fatal(err)
	}
	service, err := auth.PrecreateService(t.Context(), token, store.ServiceRegistration{
		ServiceID:   "studio-worker-01",
		ServiceType: "worker",
		ServiceName: "Studio Worker 01",
		Host:        "worker.example.com",
		Port:        8443,
		SSLEnabled:  true,
		PublicURL:   "https://worker.example.com:8443",
	})
	if err != nil {
		t.Fatal(err)
	}
	configureToken := "ast_cfg_test_runtime_report"
	if _, err := auth.SetServiceConfigureToken(t.Context(), service.ServiceID, security.HashToken(configureToken), time.Now().UTC().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth), WithAuditStore(auth))

	req := httptest.NewRequest(http.MethodPost, "/services/runtime-identity/configuration", bytes.NewBufferString(`{"nodeId":"studio-worker-01","configureToken":"`+configureToken+`","version":"1.4.1","commit":"abc1234","build_date":"2026-07-09T00:00:00Z","hostname":"studio-worker-01","os":"linux","arch":"amd64"}`))
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusInternalServerError || !strings.Contains(res.Body.String(), "store_node_runtime_token_failed") {
		t.Fatalf("configure without encryption key status = %d body = %s", res.Code, res.Body.String())
	}
	got, err := auth.GetService(t.Context(), service.ServiceID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "pending" || got.ReportedVersion != "" || got.ReportedCommit != "" || got.ReportedBuildDate != "" || got.ReportedHostname != "" || got.ReportedOS != "" || got.ReportedArch != "" {
		t.Fatalf("configure must not mutate the runtime report before encryption is available: %s", formatSafeHTTPSensitiveDiagnostic(got))
	}
	if got.TokenID != token.ID {
		t.Fatalf("runtime token should not rotate before encryption is available: old=%s got=%s", token.ID, got.TokenID)
	}
	if got.ConfigureTokenUsedAt != nil {
		t.Fatalf("configure token must remain usable after prerequisite failure: %s", formatSafeHTTPSensitiveDiagnostic(got))
	}
}

func TestNodeRuntimeTokenEncryptionKeyRejectsWeakValues(t *testing.T) {
	for _, tc := range []struct {
		name string
		key  string
	}{
		{name: "missing"},
		{name: "short", key: "short-key"},
		{name: "placeholder", key: "<CHANGE_ME_32_BYTES>"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("AUTOSTREAM_SECRET_ENCRYPTION_KEY", tc.key)
			if _, err := nodeRuntimeTokenEncryptionKey(); err == nil {
				t.Fatalf("weak encryption key %q should be rejected", tc.key)
			}
		})
	}
	t.Setenv("AUTOSTREAM_SECRET_ENCRYPTION_KEY", "test-secret-encryption-key-32-bytes")
	if key, err := nodeRuntimeTokenEncryptionKey(); err != nil || key == "" {
		t.Fatalf("strong encryption key should be accepted: key=%q err=%v", key, err)
	}
}

func TestNodeRuntimeTokenDecryptRequiresValidatedEncryptionKey(t *testing.T) {
	const weakKey = "short-key"
	ciphertext, nonce, err := security.EncryptSecret("runtime-token", weakKey)
	if err != nil {
		t.Fatal(err)
	}
	service := store.RegisteredService{NodeTokenCiphertext: ciphertext, NodeTokenNonce: nonce}
	t.Setenv("AUTOSTREAM_SECRET_ENCRYPTION_KEY", weakKey)
	if _, err := nodeRuntimeToken(service); err == nil || !strings.Contains(err.Error(), "at least 32 bytes") {
		t.Fatalf("weak decryption key should be rejected, got %v", err)
	}

	strongKey := "test-secret-encryption-key-32-bytes"
	ciphertext, nonce, err = security.EncryptSecret("runtime-token", strongKey)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("AUTOSTREAM_SECRET_ENCRYPTION_KEY", strongKey)
	if got, err := nodeRuntimeToken(store.RegisteredService{NodeTokenCiphertext: ciphertext, NodeTokenNonce: nonce}); err != nil || got != "runtime-token" {
		t.Fatalf("runtime token decrypt = %q, err=%v", got, err)
	}
}

func TestNodeAgentConfigurePersistsRuntimeReportForSupportedNodeTypes(t *testing.T) {
	t.Setenv("AUTOSTREAM_SECRET_ENCRYPTION_KEY", "test-secret-encryption-key-32-bytes")
	t.Setenv("AUTOSTREAM_STREAM_INGEST_SIGNING_KEY", "test-stream-ingest-signing-key-32-bytes")
	auth := store.NewMemoryAuthStore()
	handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth), WithAuditStore(auth))
	for _, serviceType := range []string{"worker", "encoder_recorder", "discord_bot", "observability"} {
		t.Run(serviceType, func(t *testing.T) {
			token, err := auth.CreateServiceToken(t.Context(), serviceType, []string{"service.register", "service.heartbeat", "service.config.read"})
			if err != nil {
				t.Fatal(err)
			}
			serviceID := strings.ReplaceAll(serviceType, "_", "-") + "-01"
			service, err := auth.PrecreateService(t.Context(), token, store.ServiceRegistration{
				ServiceID:   serviceID,
				ServiceType: serviceType,
				ServiceName: serviceType + " Node 01",
				Host:        serviceID + ".example.com",
				Port:        8443,
				SSLEnabled:  true,
				PublicURL:   "https://" + serviceID + ".example.com:8443",
			})
			if err != nil {
				t.Fatal(err)
			}
			configureToken := "ast_cfg_test_" + strings.ReplaceAll(serviceType, "_", "-")
			if _, err := auth.SetServiceConfigureToken(t.Context(), service.ServiceID, security.HashToken(configureToken), time.Now().UTC().Add(time.Hour)); err != nil {
				t.Fatal(err)
			}
			payload := `{"nodeId":"` + service.ServiceID + `","configureToken":"` + configureToken + `","version":"1.4.1","commit":"abc1234","build_date":"2026-07-09T00:00:00Z","hostname":"` + service.ServiceID + `","os":"linux","arch":"amd64"}`
			req := httptest.NewRequest(http.MethodPost, "/services/runtime-identity/configuration", bytes.NewBufferString(payload))
			res := httptest.NewRecorder()
			handler.ServeHTTP(res, req)
			if res.Code != http.StatusOK {
				t.Fatalf("configure node status = %d body = %s", res.Code, res.Body.String())
			}
			got, err := auth.GetService(t.Context(), service.ServiceID)
			if err != nil {
				t.Fatal(err)
			}
			if got.Status != "registered" || got.ReportedVersion != "1.4.1" || got.ReportedCommit != "abc1234" || got.ReportedBuildDate != "2026-07-09T00:00:00Z" || got.ReportedHostname != service.ServiceID || got.ReportedOS != "linux" || got.ReportedArch != "amd64" || got.LastReportedAt == nil {
				t.Fatalf("configure runtime report was not persisted for %s: %s", serviceType, formatSafeHTTPSensitiveDiagnostic(got))
			}
			if got.ConfigureTokenUsedAt == nil {
				t.Fatalf("configure token was not marked used for %s: %s", serviceType, formatSafeHTTPSensitiveDiagnostic(got))
			}
		})
	}
}
