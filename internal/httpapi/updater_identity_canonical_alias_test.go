package httpapi

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"github.com/example/autostream-control-panel/internal/security"
	"github.com/example/autostream-control-panel/internal/store"
	"github.com/example/autostream-control-panel/internal/updateradapter"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestMixedCaseUpdaterAliasUsesCanonicalIdentityForBootstrapAndGuards(t *testing.T) {
	t.Setenv("AUTOSTREAM_SECRET_ENCRYPTION_KEY", "test-secret-encryption-key-32-bytes")
	t.Setenv("AUTOSTREAM_PUBLIC_URL", "https://panel.example.com")
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(
		store.User{Username: "mixed-case-admin"},
		"correct horse battery",
		[]string{"system_updates.read", "system_updates.execute", "secrets.update"},
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
	const canonicalServiceID = "Updater-Mixed-Case"
	aliasServiceID := strings.ToLower(canonicalServiceID)
	registerServiceWithTokenForTest(t, auth, token, store.ServiceRegistration{
		ServiceID:       canonicalServiceID,
		ServiceType:     "update_agent",
		ServiceName:     "Mixed Case Updater",
		TransportMode:   store.SystemUpdateTransportPullV2,
		ExecutionHostID: "host-01",
		Version:         "v1.0.0",
		Capabilities: map[string]any{
			"host_agent":   true,
			"observe_only": true,
		},
	})

	policies := store.NewMemoryUpdaterPolicyStore()
	hostKey, _ := ed25519AuthorizedKeyForTest(t, "")
	policy := savePullUpdaterPolicyForHTTPTest(t, policies, canonicalServiceID, updaterPolicyForHTTPTest(hostKey))
	secrets := updaterReleaseTokenSecretStoreForBootstrapTest(t, "github_pat_mixed_case")
	bootstrapPublicKey := p256PublicKeyForBootstrapTest(t)
	recipientFingerprint := bootstrapFingerprintForTest(bootstrapPublicKey)
	capabilities := map[string]any{
		"host_agent":                           true,
		"observe_only":                         true,
		"policy_revision":                      policy.ProjectionRevision,
		"policy_desired_revision":              policy.ProjectionRevision,
		"policy_status":                        "applied",
		"bootstrap_encryption_public_key":      base64.RawURLEncoding.EncodeToString(bootstrapPublicKey),
		"bootstrap_encryption_key_fingerprint": recipientFingerprint,
	}
	if _, err := auth.Heartbeat(t.Context(), token, store.ServiceHeartbeat{
		ServiceID:    canonicalServiceID,
		Status:       "online",
		Version:      "v1.0.0",
		Capabilities: capabilities,
	}); err != nil {
		t.Fatal(err)
	}

	broker := NewUpdateHostBootstrapBroker()
	services := caseInsensitiveServiceLookupStore{ServiceRegistryStore: auth, scrubConfigureTokenHash: true}
	server := NewServer(
		store.NewMemoryStreamStore(),
		WithAuthStore(auth),
		WithAuditStore(auth),
		WithServiceRegistryStore(services),
		WithUpdaterPolicyStore(policies),
		WithSecretStore(secrets),
		WithUpdateHostBootstrapBroker(broker),
	)
	cookie, csrf := loginForTest(t, server, "mixed-case-admin", "correct horse battery")
	adminRequest := func(method, path string, body []byte) *httptest.ResponseRecorder {
		t.Helper()
		request := httptest.NewRequest(method, path, bytes.NewReader(body))
		request.AddCookie(cookie)
		request.Header.Set("X-CSRF-Token", csrf)
		result := httptest.NewRecorder()
		server.ServeHTTP(result, request)
		return result
	}

	createPayload, err := json.Marshal(map[string]any{
		"job_id":                    "7ba7b810-9dad-4f0e-9a58-4aee7cb5560f",
		"idempotency_key":           "mixed-case-create",
		"expected_revision":         policy.Revision,
		"recipient_key_fingerprint": recipientFingerprint,
		"host_ids":                  []string{"host-01"},
		"envelope": map[string]any{
			"version":              1,
			"ephemeral_public_key": base64.RawURLEncoding.EncodeToString(p256PublicKeyForBootstrapTest(t)),
			"nonce":                base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{3}, 12)),
			"ciphertext":           base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{4}, 96)),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	aliasCreate := adminRequest(
		http.MethodPost,
		"https://panel.example.com/system-updates/updaters/"+aliasServiceID+"/bootstrap-jobs",
		createPayload,
	)
	if aliasCreate.Code != http.StatusBadRequest ||
		!strings.Contains(aliasCreate.Body.String(), `"code":"invalid_update_agent"`) {
		t.Fatalf("non-canonical bootstrap create status=%d body=%s", aliasCreate.Code, aliasCreate.Body.String())
	}
	create := adminRequest(
		http.MethodPost,
		"https://panel.example.com/system-updates/updaters/"+canonicalServiceID+"/bootstrap-jobs",
		createPayload,
	)
	if create.Code != http.StatusAccepted {
		t.Fatalf("mixed-case bootstrap create status=%d body=%s", create.Code, create.Body.String())
	}
	var created struct {
		Jobs []UpdateHostBootstrapJob `json:"jobs"`
	}
	if err := json.NewDecoder(create.Body).Decode(&created); err != nil {
		t.Fatal(err)
	}
	if len(created.Jobs) != 1 || created.Jobs[0].UpdaterID != canonicalServiceID {
		t.Fatalf("bootstrap job did not store canonical updater identity: %#v", created.Jobs)
	}

	list := adminRequest(
		http.MethodGet,
		"/system-updates/updaters/"+aliasServiceID+"/bootstrap-jobs",
		nil,
	)
	if list.Code != http.StatusOK ||
		!strings.Contains(list.Body.String(), `"updater_id":"`+canonicalServiceID+`"`) {
		t.Fatalf("mixed-case bootstrap list status=%d body=%s", list.Code, list.Body.String())
	}

	claimPayload, err := json.Marshal(map[string]any{
		"service_id":                aliasServiceID,
		"current_revision":          policy.Revision,
		"recipient_key_fingerprint": recipientFingerprint,
	})
	if err != nil {
		t.Fatal(err)
	}
	claimRequest := httptest.NewRequest(
		http.MethodPost,
		"https://panel.example.com/services/update-agent/bootstrap-jobs/claim",
		bytes.NewReader(claimPayload),
	)
	claimRequest.Header.Set("Authorization", "Bearer "+token.RawToken)
	claim := httptest.NewRecorder()
	server.ServeHTTP(claim, claimRequest)
	if claim.Code != http.StatusOK ||
		!strings.Contains(claim.Body.String(), `"updater_id":"`+canonicalServiceID+`"`) {
		t.Fatalf("mixed-case bootstrap claim status=%d body=%s", claim.Code, claim.Body.String())
	}

	replacement := updaterPolicyForHTTPTest(hostKey)
	policyPayload, err := json.Marshal(map[string]any{
		"expected_revision":            policy.Revision,
		"poll_interval_seconds":        replacement.PollIntervalSeconds,
		"heartbeat_interval_seconds":   replacement.HeartbeatIntervalSeconds,
		"local_executor_policy_sha256": replacement.LocalExecutorPolicySHA256,
		"hosts":                        replacement.Hosts,
		"targets":                      updaterPolicyTargetRequestsForTest(replacement.Targets),
		"github_token":                 "github_pat_mixed_case_changed",
	})
	if err != nil {
		t.Fatal(err)
	}
	policySave := adminRequest(
		http.MethodPut,
		"/system-updates/updaters/"+aliasServiceID+"/settings",
		policyPayload,
	)
	assertUpdaterBootstrapMutationConflict(t, policySave)

	const configureToken = "configure-mixed-case"
	if _, err := auth.SetServiceConfigureToken(
		t.Context(),
		canonicalServiceID,
		security.HashToken(configureToken),
		time.Now().UTC().Add(time.Hour),
	); err != nil {
		t.Fatal(err)
	}
	stageRequest := func(rawToken string) *httptest.ResponseRecorder {
		t.Helper()
		payload, err := json.Marshal(map[string]any{
			"nodeId":          aliasServiceID,
			"configureToken":  rawToken,
			"protocolVersion": updateradapter.HostAgentConfigureProtocolVersion,
			"agentUid":        updaterIdentityFixtureAgentUID,
			"agentGid":        updaterIdentityFixtureAgentGID,
		})
		if err != nil {
			t.Fatal(err)
		}
		request := httptest.NewRequest(
			http.MethodPost,
			"/services/host-agent/runtime-identity/stage",
			bytes.NewReader(payload),
		)
		result := httptest.NewRecorder()
		server.ServeHTTP(result, request)
		return result
	}
	invalidStage := stageRequest("invalid-configure-token")
	if invalidStage.Code != http.StatusUnauthorized ||
		!strings.Contains(invalidStage.Body.String(), `"code":"invalid_configure_token"`) {
		t.Fatalf("invalid mixed-case configure token exposed bootstrap state: status=%d body=%s", invalidStage.Code, invalidStage.Body.String())
	}
	assertUpdaterBootstrapMutationConflict(t, stageRequest(configureToken))
	blockedStage, err := auth.GetService(t.Context(), canonicalServiceID)
	if err != nil {
		t.Fatal(err)
	}
	if blockedStage.StagedNodeTokenID != "" || blockedStage.ConfigureTokenUsedAt != nil {
		t.Fatalf("mixed-case active guard allowed configuration stage: %#v", blockedStage)
	}

	staged := stageUpdaterIdentityConfiguration(t, auth, canonicalServiceID, "configure-activation")
	activationRequest := func(rawToken string) *httptest.ResponseRecorder {
		t.Helper()
		payload, err := json.Marshal(map[string]string{
			"nodeId":          aliasServiceID,
			"configurationId": staged.Token.ID,
			"activationToken": rawToken,
			"version":         "v1.0.1",
		})
		if err != nil {
			t.Fatal(err)
		}
		request := httptest.NewRequest(
			http.MethodPost,
			"/services/host-agent/runtime-identity/activate",
			bytes.NewReader(payload),
		)
		result := httptest.NewRecorder()
		server.ServeHTTP(result, request)
		return result
	}
	invalidActivation := activationRequest("ast_act_invalid")
	if invalidActivation.Code != http.StatusUnauthorized ||
		!strings.Contains(invalidActivation.Body.String(), `"code":"invalid_activation_token"`) {
		t.Fatalf("invalid mixed-case activation token exposed bootstrap state: status=%d body=%s", invalidActivation.Code, invalidActivation.Body.String())
	}
	assertUpdaterBootstrapMutationConflict(t, activationRequest(staged.ActivationToken))
	blockedActivation, err := auth.GetService(t.Context(), canonicalServiceID)
	if err != nil {
		t.Fatal(err)
	}
	if blockedActivation.TokenID == staged.Token.ID {
		t.Fatalf("mixed-case active guard activated staged updater identity: %#v", blockedActivation)
	}
}
