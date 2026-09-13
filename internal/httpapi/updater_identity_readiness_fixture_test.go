package httpapi

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"github.com/example/autostream-control-panel/internal/store"
	"github.com/example/autostream-control-panel/internal/updateradapter"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type bootstrapIdentityReadinessFixture struct {
	auth                  *store.MemoryAuthStore
	server                *Server
	serviceID             string
	runtimeToken          store.ServiceToken
	cookie                *http.Cookie
	csrf                  string
	policyRevision        int64
	capabilities          map[string]any
	recipientFingerprint  string
	ephemeralPublicKeyB64 string
}

func newBootstrapIdentityReadinessFixture(t *testing.T) bootstrapIdentityReadinessFixture {
	t.Helper()
	t.Setenv("AUTOSTREAM_SECRET_ENCRYPTION_KEY", "test-secret-encryption-key-32-bytes")
	t.Setenv("AUTOSTREAM_PUBLIC_URL", "https://panel.example.com")
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(
		store.User{Username: "bootstrap-readiness-admin"},
		"correct horse battery",
		[]string{
			"api_tokens.create",
			"api_tokens.revoke",
			"system_updates.read",
			"system_updates.execute",
			"secrets.update",
		},
	); err != nil {
		t.Fatal(err)
	}
	token, err := auth.CreateServiceToken(
		t.Context(),
		"update_agent",
		[]string{
			"service.register",
			"service.heartbeat",
			"updates.claim",
			"updates.report",
			"updates.authorize",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	const serviceID = "updater-bootstrap-readiness"
	bootstrapPublicKey := p256PublicKeyForBootstrapTest(t)
	registerServiceWithTokenForTest(t, auth, token, store.ServiceRegistration{
		ServiceID:       serviceID,
		ServiceType:     "update_agent",
		ServiceName:     "Updater",
		TransportMode:   store.SystemUpdateTransportPullV2,
		ExecutionHostID: "host-01",
		Version:         "v1.0.0",
		Capabilities: map[string]any{
			"bootstrap_encryption_public_key":      base64.RawURLEncoding.EncodeToString(bootstrapPublicKey),
			"bootstrap_encryption_key_fingerprint": bootstrapFingerprintForTest(bootstrapPublicKey),
			"host_agent":                           true,
			"observe_only":                         true,
		},
	})

	workerToken, err := auth.CreateServiceToken(
		t.Context(),
		"worker",
		[]string{"service.register", "service.heartbeat"},
	)
	if err != nil {
		t.Fatal(err)
	}
	registerServiceWithTokenForTest(t, auth, workerToken, store.ServiceRegistration{
		ServiceID:   "worker-01",
		ServiceType: "worker",
		ServiceName: "Worker 01",
		Host:        "worker-01.example.com",
		Port:        8084,
		SSLEnabled:  true,
		PublicURL:   "https://worker-01.example.com:8084",
		Version:     "v1.0.0",
	})

	policies := store.NewMemoryUpdaterPolicyStore()
	updates := store.NewMemorySystemUpdateStore()
	hostKey, _ := ed25519AuthorizedKeyForTest(t, "")
	policy, err := policies.SavePullUpdaterPolicy(
		t.Context(),
		updates,
		serviceID,
		0,
		0,
		updaterPolicyForHTTPTest(hostKey),
	)
	if err != nil {
		t.Fatal(err)
	}
	secrets := updaterReleaseTokenSecretStoreForBootstrapTest(t, "github_pat_bootstrap_readiness")
	capabilities := map[string]any{
		"host_agent":                           true,
		"observe_only":                         true,
		"policy_revision":                      policy.ProjectionRevision,
		"policy_desired_revision":              policy.ProjectionRevision,
		"policy_status":                        "applied",
		"bootstrap_encryption_public_key":      base64.RawURLEncoding.EncodeToString(bootstrapPublicKey),
		"bootstrap_encryption_key_fingerprint": bootstrapFingerprintForTest(bootstrapPublicKey),
	}
	if _, err := auth.Heartbeat(t.Context(), token, store.ServiceHeartbeat{
		ServiceID:    serviceID,
		Status:       "online",
		Version:      "v1.0.0",
		Capabilities: capabilities,
	}); err != nil {
		t.Fatal(err)
	}

	server := NewServer(
		store.NewMemoryStreamStore(),
		WithAuthStore(auth),
		WithAuditStore(auth),
		WithServiceRegistryStore(auth),
		WithUpdaterPolicyStore(policies),
		WithSystemUpdateStore(updates),
		WithSecretStore(secrets),
	)
	cookie, csrf := loginForTest(
		t,
		server,
		"bootstrap-readiness-admin",
		"correct horse battery",
	)
	return bootstrapIdentityReadinessFixture{
		auth:                  auth,
		server:                server,
		serviceID:             serviceID,
		runtimeToken:          token,
		cookie:                cookie,
		csrf:                  csrf,
		policyRevision:        policy.ProjectionRevision,
		capabilities:          capabilities,
		recipientFingerprint:  bootstrapFingerprintForTest(bootstrapPublicKey),
		ephemeralPublicKeyB64: base64.RawURLEncoding.EncodeToString(p256PublicKeyForBootstrapTest(t)),
	}
}

func (f bootstrapIdentityReadinessFixture) rotateRuntimeIdentity(t *testing.T) string {
	t.Helper()
	return rotateUpdaterRuntimeIdentityForTest(t, f.server, f.serviceID, f.adminRequest)
}

func rotateUpdaterRuntimeIdentityForTest(
	t *testing.T,
	server *Server,
	serviceID string,
	adminRequest func(*testing.T, string, string, string) *httptest.ResponseRecorder,
) string {
	t.Helper()
	configure := adminRequest(
		t,
		http.MethodPost,
		"/nodes/"+serviceID+"/configure-token",
		"",
	)
	if configure.Code != http.StatusCreated {
		t.Fatalf("updater configure-token status=%d", configure.Code)
	}
	var configureResponse struct {
		ConfigureToken string `json:"configure_token"`
	}
	if err := json.NewDecoder(configure.Body).Decode(&configureResponse); err != nil {
		t.Fatal(err)
	}
	if configureResponse.ConfigureToken == "" {
		t.Fatal("updater configure-token response omitted the one-time token")
	}

	stagePayload, err := json.Marshal(map[string]any{
		"nodeId":          serviceID,
		"configureToken":  configureResponse.ConfigureToken,
		"protocolVersion": updateradapter.HostAgentConfigureProtocolVersion,
		"agentUid":        updaterIdentityFixtureAgentUID,
		"agentGid":        updaterIdentityFixtureAgentGID,
	})
	if err != nil {
		t.Fatal(err)
	}
	stageRequest := httptest.NewRequest(
		http.MethodPost,
		"https://panel.example.com/services/host-agent/runtime-identity/stage",
		bytes.NewReader(stagePayload),
	)
	stage := httptest.NewRecorder()
	server.ServeHTTP(stage, stageRequest)
	if stage.Code != http.StatusOK {
		t.Fatalf("updater runtime identity stage status=%d body=%s", stage.Code, stage.Body.String())
	}
	var staged updateradapter.UpdaterStagedConfiguration
	if err := json.NewDecoder(stage.Body).Decode(&staged); err != nil {
		t.Fatal(err)
	}
	if staged.Config.RuntimeToken == "" ||
		staged.ConfigurationID == "" ||
		staged.ActivationToken == "" ||
		staged.LocalExecutorPolicy == nil {
		t.Fatal("updater runtime identity stage response is incomplete")
	}

	activationPayload, err := json.Marshal(hostAgentConfigureActivationPayload(
		staged,
		updaterIdentityFixtureAgentUID,
		updaterIdentityFixtureAgentGID,
		*staged.LocalExecutorPolicy,
	))
	if err != nil {
		t.Fatal(err)
	}
	activationRequest := httptest.NewRequest(
		http.MethodPost,
		"https://panel.example.com/services/host-agent/runtime-identity/activate",
		bytes.NewReader(activationPayload),
	)
	activation := httptest.NewRecorder()
	server.ServeHTTP(activation, activationRequest)
	if activation.Code != http.StatusOK {
		t.Fatalf("updater runtime identity activation status=%d body=%s", activation.Code, activation.Body.String())
	}
	return staged.Config.RuntimeToken
}

func (f bootstrapIdentityReadinessFixture) createBootstrapRequest(
	t *testing.T,
	idempotencySuffix string,
) *httptest.ResponseRecorder {
	t.Helper()
	jobID := "8ba7b810-9dad-4f0e-9a58-4aee7cb5560f"
	if idempotencySuffix == "pending-configuration" {
		jobID = "9ba7b810-9dad-4f0e-9a58-4aee7cb5560f"
	}
	payload, err := json.Marshal(map[string]any{
		"job_id":                    jobID,
		"idempotency_key":           "idempotency-" + idempotencySuffix,
		"expected_revision":         f.policyRevision,
		"recipient_key_fingerprint": f.recipientFingerprint,
		"host_ids":                  []string{"host-01"},
		"envelope": map[string]any{
			"version":              1,
			"ephemeral_public_key": f.ephemeralPublicKeyB64,
			"nonce": base64.RawURLEncoding.EncodeToString(
				bytes.Repeat([]byte{3}, 12),
			),
			"ciphertext": base64.RawURLEncoding.EncodeToString(
				bytes.Repeat([]byte{4}, 96),
			),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(
		http.MethodPost,
		"https://panel.example.com/system-updates/updaters/"+f.serviceID+"/bootstrap-jobs",
		bytes.NewReader(payload),
	)
	request.AddCookie(f.cookie)
	request.Header.Set("X-CSRF-Token", f.csrf)
	result := httptest.NewRecorder()
	f.server.ServeHTTP(result, request)
	return result
}

func (f bootstrapIdentityReadinessFixture) heartbeat(t *testing.T, rawToken string) {
	t.Helper()
	payload, err := json.Marshal(store.ServiceHeartbeat{
		ServiceID:    f.serviceID,
		Status:       "online",
		Version:      "v1.0.1",
		Capabilities: f.capabilities,
	})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(
		http.MethodPost,
		"https://panel.example.com/services/heartbeat",
		bytes.NewReader(payload),
	)
	request.Header.Set("Authorization", "Bearer "+rawToken)
	result := httptest.NewRecorder()
	f.server.ServeHTTP(result, request)
	if result.Code != http.StatusAccepted {
		t.Fatalf("new-token heartbeat status=%d body=%s", result.Code, result.Body.String())
	}
}

func (f bootstrapIdentityReadinessFixture) heartbeatAfterRotation(
	t *testing.T,
	rawToken string,
) {
	t.Helper()
	f.heartbeat(t, rawToken)
	service, err := f.auth.GetService(t.Context(), f.serviceID)
	if err != nil {
		t.Fatal(err)
	}
	if service.LastHeartbeatAt == nil {
		t.Fatalf("post-rotation heartbeat was not persisted: %#v", service)
	}
}

func (f bootstrapIdentityReadinessFixture) adminRequest(
	t *testing.T,
	method string,
	path string,
	body string,
) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.AddCookie(f.cookie)
	request.Header.Set("X-CSRF-Token", f.csrf)
	result := httptest.NewRecorder()
	f.server.ServeHTTP(result, request)
	return result
}

func (f bootstrapIdentityReadinessFixture) publicRequest(
	t *testing.T,
	method string,
	path string,
	body string,
) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	result := httptest.NewRecorder()
	f.server.ServeHTTP(result, request)
	return result
}
