package httpapi

import (
	"bytes"
	"crypto/ecdh"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/example/autostream-control-panel/internal/store"
)

func TestUpdateHostBootstrapJobKeepsCredentialOpaqueAndUsesOutboundClaim(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(
		store.User{ID: "bootstrap-admin", Username: "bootstrap-admin"},
		"correct horse battery",
		[]string{"system_updates.read", "system_updates.execute", "secrets.update"},
	); err != nil {
		t.Fatal(err)
	}
	updaterToken := registerUpdateAgentForPolicyTest(t, auth, "updater-01")
	policies := store.NewMemoryUpdaterPolicyStore()
	hostKey, _ := ed25519AuthorizedKeyForTest(t, "")
	policy := savePullUpdaterPolicyForHTTPTest(t, policies, "updater-01", updaterPolicyForHTTPTest(hostKey))
	secrets := updaterReleaseTokenSecretStoreForBootstrapTest(t, "github_pat_bootstrap_release")
	bootstrapPublicKey := p256PublicKeyForBootstrapTest(t)
	if _, err := auth.Heartbeat(t.Context(), updaterToken, store.ServiceHeartbeat{
		ServiceID: "updater-01",
		Status:    "online",
		Version:   "v1.8.0",
		Capabilities: map[string]any{
			"policy_revision":                      policy.ProjectionRevision,
			"policy_desired_revision":              policy.ProjectionRevision,
			"policy_status":                        "applied",
			"bootstrap_encryption_public_key":      base64.RawURLEncoding.EncodeToString(bootstrapPublicKey),
			"bootstrap_encryption_key_fingerprint": bootstrapFingerprintForTest(bootstrapPublicKey),
		},
	}); err != nil {
		t.Fatal(err)
	}

	handler := NewServer(
		store.NewMemoryStreamStore(),
		WithAuthStore(auth),
		WithAuditStore(auth),
		WithServiceRegistryStore(auth),
		WithUpdaterPolicyStore(policies),
		WithSecretStore(secrets),
	)
	cookie, csrf := loginForTest(t, handler, "bootstrap-admin", "correct horse battery")
	const jobID = "6ba7b810-9dad-4f0e-9a58-4aee7cb5560f"
	ephemeralPublicKey := p256PublicKeyForBootstrapTest(t)
	envelope := map[string]any{
		"version":              1,
		"ephemeral_public_key": base64.RawURLEncoding.EncodeToString(ephemeralPublicKey),
		"nonce":                base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{3}, 12)),
		"ciphertext":           base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{4}, 96)),
	}
	createBody := map[string]any{
		"job_id":                    jobID,
		"idempotency_key":           "bootstrap-host-01-once",
		"expected_revision":         policy.Revision,
		"recipient_key_fingerprint": bootstrapFingerprintForTest(bootstrapPublicKey),
		"host_ids":                  []string{"host-01"},
		"envelope":                  envelope,
	}
	payload, err := json.Marshal(createBody)
	if err != nil {
		t.Fatal(err)
	}
	create := httptest.NewRequest(
		http.MethodPost,
		"https://panel.example.com/system-updates/updaters/updater-01/bootstrap-jobs",
		bytes.NewReader(payload),
	)
	create.AddCookie(cookie)
	create.Header.Set("X-CSRF-Token", csrf)
	createResult := httptest.NewRecorder()
	handler.ServeHTTP(createResult, create)
	if createResult.Code != http.StatusAccepted {
		t.Fatalf("create bootstrap job status=%d body=%s", createResult.Code, createResult.Body.String())
	}
	if strings.Contains(createResult.Body.String(), envelope["ciphertext"].(string)) ||
		strings.Contains(createResult.Body.String(), "lease_token") {
		t.Fatalf("public bootstrap response exposed secret material: %s", createResult.Body.String())
	}
	if got := createResult.Header().Get("Cache-Control"); !strings.Contains(got, "no-store") {
		t.Fatalf("create bootstrap job Cache-Control=%q", got)
	}

	claimPayload, err := json.Marshal(map[string]any{
		"service_id": "updater-01", "current_revision": policy.Revision,
		"recipient_key_fingerprint": bootstrapFingerprintForTest(bootstrapPublicKey),
	})
	if err != nil {
		t.Fatal(err)
	}
	claim := httptest.NewRequest(
		http.MethodPost,
		"https://panel.example.com/services/update-agent/bootstrap-jobs/claim",
		bytes.NewReader(claimPayload),
	)
	claim.Header.Set("Authorization", "Bearer "+updaterToken.RawToken)
	claimResult := httptest.NewRecorder()
	handler.ServeHTTP(claimResult, claim)
	if claimResult.Code != http.StatusOK {
		t.Fatalf("claim bootstrap job status=%d body=%s", claimResult.Code, claimResult.Body.String())
	}
	if !strings.Contains(claimResult.Body.String(), envelope["ciphertext"].(string)) ||
		!strings.Contains(claimResult.Body.String(), `"release_token":"github_pat_bootstrap_release"`) {
		t.Fatalf("service claim omitted the one-time encrypted credential or release token: %s", claimResult.Body.String())
	}
	if got := claimResult.Header().Get("Cache-Control"); !strings.Contains(got, "no-store") {
		t.Fatalf("claim bootstrap job Cache-Control=%q", got)
	}
	var claimed struct {
		ID         string `json:"id"`
		UpdaterID  string `json:"updater_id"`
		LeaseToken string `json:"lease_token"`
	}
	if err := json.Unmarshal(claimResult.Body.Bytes(), &claimed); err != nil ||
		claimed.ID != jobID || claimed.UpdaterID != "updater-01" || claimed.LeaseToken == "" {
		t.Fatalf("claim=%#v err=%v", claimed, err)
	}

	acceptBody, _ := json.Marshal(map[string]any{
		"service_id":  "updater-01",
		"lease_token": claimed.LeaseToken,
	})
	accept := httptest.NewRequest(
		http.MethodPost,
		"https://panel.example.com/services/update-agent/bootstrap-jobs/"+jobID+"/accept",
		bytes.NewReader(acceptBody),
	)
	accept.Header.Set("Authorization", "Bearer "+updaterToken.RawToken)
	acceptResult := httptest.NewRecorder()
	handler.ServeHTTP(acceptResult, accept)
	if acceptResult.Code != http.StatusNoContent {
		t.Fatalf("accept bootstrap job status=%d body=%s", acceptResult.Code, acceptResult.Body.String())
	}

	claimAgain := httptest.NewRequest(
		http.MethodPost,
		"https://panel.example.com/services/update-agent/bootstrap-jobs/claim",
		bytes.NewReader(claimPayload),
	)
	claimAgain.Header.Set("Authorization", "Bearer "+updaterToken.RawToken)
	claimAgainResult := httptest.NewRecorder()
	handler.ServeHTTP(claimAgainResult, claimAgain)
	if claimAgainResult.Code != http.StatusNoContent {
		t.Fatalf("accepted credential was claimable again: status=%d body=%s", claimAgainResult.Code, claimAgainResult.Body.String())
	}

	for _, progressReport := range []struct {
		status   string
		progress int
	}{
		{status: "connecting", progress: 10},
		{status: "verifying", progress: 20},
		{status: "uploading", progress: 40},
		{status: "installing", progress: 65},
		{status: "probing", progress: 90},
		{status: "succeeded", progress: 100},
	} {
		reportBody, _ := json.Marshal(map[string]any{
			"service_id":  "updater-01",
			"lease_token": claimed.LeaseToken,
			"host_id":     "host-01",
			"status":      progressReport.status,
			"progress":    progressReport.progress,
		})
		report := httptest.NewRequest(
			http.MethodPost,
			"https://panel.example.com/services/update-agent/bootstrap-jobs/"+jobID+"/report",
			bytes.NewReader(reportBody),
		)
		report.Header.Set("Authorization", "Bearer "+updaterToken.RawToken)
		reportResult := httptest.NewRecorder()
		handler.ServeHTTP(reportResult, report)
		if reportResult.Code != http.StatusNoContent {
			t.Fatalf(
				"report bootstrap job status=%q HTTP=%d body=%s",
				progressReport.status,
				reportResult.Code,
				reportResult.Body.String(),
			)
		}
	}

	list := httptest.NewRequest(
		http.MethodGet,
		"https://panel.example.com/system-updates/updaters/updater-01/bootstrap-jobs",
		nil,
	)
	list.AddCookie(cookie)
	listResult := httptest.NewRecorder()
	handler.ServeHTTP(listResult, list)
	if listResult.Code != http.StatusOK ||
		!strings.Contains(listResult.Body.String(), `"status":"succeeded"`) ||
		strings.Contains(listResult.Body.String(), envelope["ciphertext"].(string)) ||
		strings.Contains(listResult.Body.String(), claimed.LeaseToken) {
		t.Fatalf("public bootstrap job list status=%d body=%s", listResult.Code, listResult.Body.String())
	}

	const rotatedJobID = "7ca7b810-9dad-4f0e-9a58-4aee7cb5560f"
	createBody["job_id"] = rotatedJobID
	createBody["idempotency_key"] = "bootstrap-host-01-before-key-rotation"
	payload, err = json.Marshal(createBody)
	if err != nil {
		t.Fatal(err)
	}
	createBeforeRotation := httptest.NewRequest(
		http.MethodPost,
		"https://panel.example.com/system-updates/updaters/updater-01/bootstrap-jobs",
		bytes.NewReader(payload),
	)
	createBeforeRotation.AddCookie(cookie)
	createBeforeRotation.Header.Set("X-CSRF-Token", csrf)
	createBeforeRotationResult := httptest.NewRecorder()
	handler.ServeHTTP(createBeforeRotationResult, createBeforeRotation)
	if createBeforeRotationResult.Code != http.StatusAccepted {
		t.Fatalf("create before key rotation status=%d body=%s", createBeforeRotationResult.Code, createBeforeRotationResult.Body.String())
	}

	rotatedPublicKey := p256PublicKeyForBootstrapTest(t)
	if _, err := auth.Heartbeat(t.Context(), updaterToken, store.ServiceHeartbeat{
		ServiceID: "updater-01", Status: "online", Version: "v1.8.0",
		Capabilities: map[string]any{
			"policy_revision": policy.ProjectionRevision, "policy_desired_revision": policy.ProjectionRevision, "policy_status": "applied",
			"bootstrap_encryption_public_key":      base64.RawURLEncoding.EncodeToString(rotatedPublicKey),
			"bootstrap_encryption_key_fingerprint": bootstrapFingerprintForTest(rotatedPublicKey),
		},
	}); err != nil {
		t.Fatal(err)
	}
	claimAfterRotation := httptest.NewRequest(
		http.MethodPost,
		"https://panel.example.com/services/update-agent/bootstrap-jobs/claim",
		bytes.NewReader(claimPayload),
	)
	claimAfterRotation.Header.Set("Authorization", "Bearer "+updaterToken.RawToken)
	claimAfterRotationResult := httptest.NewRecorder()
	handler.ServeHTTP(claimAfterRotationResult, claimAfterRotation)
	if claimAfterRotationResult.Code != http.StatusConflict ||
		!strings.Contains(claimAfterRotationResult.Body.String(), `"code":"bootstrap_recipient_key_changed"`) {
		t.Fatalf("claim after key rotation status=%d body=%s", claimAfterRotationResult.Code, claimAfterRotationResult.Body.String())
	}

	listAfterRotation := httptest.NewRequest(
		http.MethodGet,
		"https://panel.example.com/system-updates/updaters/updater-01/bootstrap-jobs",
		nil,
	)
	listAfterRotation.AddCookie(cookie)
	listAfterRotationResult := httptest.NewRecorder()
	handler.ServeHTTP(listAfterRotationResult, listAfterRotation)
	if listAfterRotationResult.Code != http.StatusOK ||
		!strings.Contains(listAfterRotationResult.Body.String(), `"job_id":"`+rotatedJobID+`"`) ||
		!strings.Contains(listAfterRotationResult.Body.String(), `"status":"credential_expired"`) ||
		!strings.Contains(listAfterRotationResult.Body.String(), `"code":"bootstrap_recipient_key_changed"`) {
		t.Fatalf("key-rotation job was not terminalized: status=%d body=%s", listAfterRotationResult.Code, listAfterRotationResult.Body.String())
	}
}

func TestUpdateHostBootstrapClaimRejectsTokenRotatedAfterInitialAuthentication(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	oldToken := registerUpdateAgentForPolicyTest(t, auth, "updater-bootstrap-reauth")
	policies := store.NewMemoryUpdaterPolicyStore()
	hostKey, _ := ed25519AuthorizedKeyForTest(t, "")
	policy := savePullUpdaterPolicyForHTTPTest(t, policies, "updater-bootstrap-reauth", updaterPolicyForHTTPTest(hostKey))
	secrets := updaterReleaseTokenSecretStoreForBootstrapTest(t, "github_pat_bootstrap_reauth")
	bootstrapPublicKey := p256PublicKeyForBootstrapTest(t)
	fingerprint := bootstrapFingerprintForTest(bootstrapPublicKey)
	capabilities := map[string]any{
		"policy_revision":                      policy.ProjectionRevision,
		"policy_desired_revision":              policy.ProjectionRevision,
		"policy_status":                        "applied",
		"bootstrap_encryption_public_key":      base64.RawURLEncoding.EncodeToString(bootstrapPublicKey),
		"bootstrap_encryption_key_fingerprint": fingerprint,
	}
	if _, err := auth.Heartbeat(t.Context(), oldToken, store.ServiceHeartbeat{
		ServiceID:    "updater-bootstrap-reauth",
		Status:       "online",
		Version:      "v1.8.0",
		Capabilities: capabilities,
	}); err != nil {
		t.Fatal(err)
	}
	services := &serviceAuthenticationBarrierStore{
		ServiceRegistryStore: auth,
		authenticated:        make(chan struct{}),
	}
	broker := NewUpdateHostBootstrapBroker()
	server := NewServer(
		store.NewMemoryStreamStore(),
		WithAuthStore(auth),
		WithServiceRegistryStore(services),
		WithUpdaterPolicyStore(policies),
		WithSecretStore(secrets),
		WithUpdateHostBootstrapBroker(broker),
	)
	claimPayload, err := json.Marshal(map[string]any{
		"service_id":                "updater-bootstrap-reauth",
		"current_revision":          policy.Revision,
		"recipient_key_fingerprint": fingerprint,
	})
	if err != nil {
		t.Fatal(err)
	}

	server.systemUpdateOperationMu.Lock()
	locked := true
	defer func() {
		if locked {
			server.systemUpdateOperationMu.Unlock()
		}
	}()
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		request := httptest.NewRequest(
			http.MethodPost,
			"https://panel.example.com/services/update-agent/bootstrap-jobs/claim",
			bytes.NewReader(claimPayload),
		)
		request.Header.Set("Authorization", "Bearer "+oldToken.RawToken)
		result := httptest.NewRecorder()
		server.ServeHTTP(result, request)
		done <- result
	}()
	select {
	case <-services.authenticated:
	case <-time.After(3 * time.Second):
		t.Fatal("bootstrap claim did not complete its initial service-token authentication")
	}

	newToken, err := auth.RotateServiceToken(t.Context(), oldToken.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := auth.Heartbeat(t.Context(), newToken, store.ServiceHeartbeat{
		ServiceID:    "updater-bootstrap-reauth",
		Status:       "online",
		Version:      "v1.8.1",
		Capabilities: capabilities,
	}); err != nil {
		t.Fatal(err)
	}
	job, _, err := broker.Create(UpdateHostBootstrapCreateParams{
		UpdaterID:               "updater-bootstrap-reauth",
		ExpectedRevision:        policy.Revision,
		ClientJobID:             "3ba7b810-9dad-4f0e-9a58-4aee7cb5560f",
		IdempotencyKey:          "bootstrap-reauth",
		RecipientKeyFingerprint: fingerprint,
		HostIDs:                 []string{"host-01"},
		Envelope:                []byte("opaque-credential"),
	})
	if err != nil {
		t.Fatal(err)
	}
	server.systemUpdateOperationMu.Unlock()
	locked = false

	select {
	case result := <-done:
		if result.Code != http.StatusUnauthorized ||
			!strings.Contains(result.Body.String(), `"code":"invalid_service_token"`) {
			t.Fatalf("rotated-token bootstrap claim status=%d body=%s", result.Code, result.Body.String())
		}
	case <-time.After(3 * time.Second):
		t.Fatal("bootstrap claim did not finish after identity lock release")
	}
	jobs, err := broker.List("updater-bootstrap-reauth")
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 1 || jobs[0].ID != job.ID ||
		jobs[0].Status != UpdateHostBootstrapStatusQueued {
		t.Fatalf("pre-rotation request claimed post-rotation bootstrap job: %#v", jobs)
	}
}

func TestUpdateHostBootstrapJobRequiresSecretPermissionAndSecureTransport(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(
		store.User{ID: "bootstrap-operator", Username: "bootstrap-operator"},
		"correct horse battery",
		[]string{"system_updates.read", "system_updates.execute"},
	); err != nil {
		t.Fatal(err)
	}
	registerUpdateAgentForPolicyTest(t, auth, "updater-01")
	handler := NewServer(
		store.NewMemoryStreamStore(),
		WithAuthStore(auth),
		WithAuditStore(auth),
		WithServiceRegistryStore(auth),
	)
	cookie, csrf := loginForTest(t, handler, "bootstrap-operator", "correct horse battery")
	body := `{"job_id":"6ba7b810-9dad-4f0e-9a58-4aee7cb5560f","idempotency_key":"once","expected_revision":1,"host_ids":["host-01"],"envelope":{"version":1,"ephemeral_public_key":"AA","nonce":"AA","ciphertext":"AA"}}`
	request := httptest.NewRequest(
		http.MethodPost,
		"https://panel.example.com/system-updates/updaters/updater-01/bootstrap-jobs",
		strings.NewReader(body),
	)
	request.AddCookie(cookie)
	request.Header.Set("X-CSRF-Token", csrf)
	result := httptest.NewRecorder()
	handler.ServeHTTP(result, request)
	if result.Code != http.StatusForbidden || !strings.Contains(result.Body.String(), `"code":"permission_denied"`) {
		t.Fatalf("secret permission status=%d body=%s", result.Code, result.Body.String())
	}

	t.Setenv("AUTOSTREAM_ENV", "production")
	secureAuth := store.NewMemoryAuthStore()
	if err := secureAuth.AddUser(
		store.User{ID: "bootstrap-admin", Username: "bootstrap-admin"},
		"correct horse battery",
		[]string{"system_updates.read", "system_updates.execute", "secrets.update"},
	); err != nil {
		t.Fatal(err)
	}
	secureUpdaterToken := registerUpdateAgentForPolicyTest(t, secureAuth, "updater-01")
	secureHandler := NewServer(
		store.NewMemoryStreamStore(),
		WithAuthStore(secureAuth),
		WithAuditStore(secureAuth),
		WithServiceRegistryStore(secureAuth),
	)
	secureCookie, secureCSRF := loginForTest(t, secureHandler, "bootstrap-admin", "correct horse battery")
	insecure := httptest.NewRequest(
		http.MethodPost,
		"http://panel.example.com/system-updates/updaters/updater-01/bootstrap-jobs",
		strings.NewReader(body),
	)
	insecure.AddCookie(secureCookie)
	insecure.Header.Set("X-CSRF-Token", secureCSRF)
	insecure.RemoteAddr = "203.0.113.10:51234"
	insecureResult := httptest.NewRecorder()
	secureHandler.ServeHTTP(insecureResult, insecure)
	if insecureResult.Code != http.StatusBadRequest ||
		!strings.Contains(insecureResult.Body.String(), `"code":"secure_transport_required"`) {
		t.Fatalf("secure transport status=%d body=%s", insecureResult.Code, insecureResult.Body.String())
	}

	insecureClaim := httptest.NewRequest(
		http.MethodPost,
		"http://panel.example.com/services/update-agent/bootstrap-jobs/claim",
		strings.NewReader(`{"service_id":"updater-01","current_revision":1}`),
	)
	insecureClaim.Header.Set("Authorization", "Bearer "+secureUpdaterToken.RawToken)
	insecureClaim.RemoteAddr = "203.0.113.10:51235"
	insecureClaimResult := httptest.NewRecorder()
	secureHandler.ServeHTTP(insecureClaimResult, insecureClaim)
	if insecureClaimResult.Code != http.StatusBadRequest ||
		!strings.Contains(insecureClaimResult.Body.String(), `"code":"secure_transport_required"`) {
		t.Fatalf("service claim secure transport status=%d body=%s", insecureClaimResult.Code, insecureClaimResult.Body.String())
	}
	for _, endpoint := range []struct {
		name string
		path string
	}{
		{name: "accept", path: "/services/update-agent/bootstrap-jobs/6ba7b810-9dad-4f0e-9a58-4aee7cb5560f/accept"},
		{name: "report", path: "/services/update-agent/bootstrap-jobs/6ba7b810-9dad-4f0e-9a58-4aee7cb5560f/report"},
	} {
		t.Run(endpoint.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, "http://panel.example.com"+endpoint.path, strings.NewReader(`{}`))
			request.Header.Set("Authorization", "Bearer "+secureUpdaterToken.RawToken)
			request.RemoteAddr = "203.0.113.10:51236"
			result := httptest.NewRecorder()
			secureHandler.ServeHTTP(result, request)
			if result.Code != http.StatusBadRequest ||
				!strings.Contains(result.Body.String(), `"code":"secure_transport_required"`) {
				t.Fatalf("secure transport status=%d body=%s", result.Code, result.Body.String())
			}
		})
	}
}

func TestUpdateAgentHeartbeatSerializesWithBootstrapIdentityChecks(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	updaterToken := registerUpdateAgentForPolicyTest(t, auth, "updater-serialized")
	server := NewServer(
		store.NewMemoryStreamStore(),
		WithAuthStore(auth),
		WithServiceRegistryStore(auth),
	)

	server.systemUpdateOperationMu.Lock()
	started := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		close(started)
		_, err := server.persistServiceHeartbeat(t.Context(), updaterToken, updaterToken.RawToken, store.ServiceHeartbeat{
			ServiceID: "updater-serialized", Status: "online", Version: "v1.8.0",
		})
		done <- err
	}()
	<-started
	select {
	case err := <-done:
		server.systemUpdateOperationMu.Unlock()
		t.Fatalf("update-agent heartbeat bypassed bootstrap identity lock: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	server.systemUpdateOperationMu.Unlock()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("serialized heartbeat error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("serialized heartbeat did not resume after bootstrap identity lock")
	}
}

func TestUpdateHostBootstrapJobRequiresReleaseTokenBeforeQueueing(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(
		store.User{ID: "bootstrap-admin", Username: "bootstrap-admin"},
		"correct horse battery",
		[]string{"system_updates.read", "system_updates.execute", "secrets.update"},
	); err != nil {
		t.Fatal(err)
	}
	updaterToken := registerUpdateAgentForPolicyTest(t, auth, "updater-01")
	policies := store.NewMemoryUpdaterPolicyStore()
	hostKey, _ := ed25519AuthorizedKeyForTest(t, "")
	policy := savePullUpdaterPolicyForHTTPTest(t, policies, "updater-01", updaterPolicyForHTTPTest(hostKey))
	bootstrapPublicKey := p256PublicKeyForBootstrapTest(t)
	if _, err := auth.Heartbeat(t.Context(), updaterToken, store.ServiceHeartbeat{
		ServiceID: "updater-01", Status: "online", Version: "v1.8.0",
		Capabilities: map[string]any{
			"policy_revision": policy.ProjectionRevision, "policy_status": "applied",
			"bootstrap_encryption_public_key":      base64.RawURLEncoding.EncodeToString(bootstrapPublicKey),
			"bootstrap_encryption_key_fingerprint": bootstrapFingerprintForTest(bootstrapPublicKey),
		},
	}); err != nil {
		t.Fatal(err)
	}
	broker := NewUpdateHostBootstrapBroker()
	handler := NewServer(
		store.NewMemoryStreamStore(),
		WithAuthStore(auth),
		WithAuditStore(auth),
		WithServiceRegistryStore(auth),
		WithUpdaterPolicyStore(policies),
		WithSecretStore(store.NewMemorySecretStore()),
		WithUpdateHostBootstrapBroker(broker),
	)
	cookie, csrf := loginForTest(t, handler, "bootstrap-admin", "correct horse battery")
	ephemeralPublicKey := p256PublicKeyForBootstrapTest(t)
	payload, err := json.Marshal(map[string]any{
		"job_id": "6ba7b810-9dad-4f0e-9a58-4aee7cb5560f", "idempotency_key": "missing-release-token",
		"expected_revision":         policy.Revision,
		"recipient_key_fingerprint": bootstrapFingerprintForTest(bootstrapPublicKey),
		"host_ids":                  []string{"host-01"},
		"envelope": map[string]any{
			"version": 1, "ephemeral_public_key": base64.RawURLEncoding.EncodeToString(ephemeralPublicKey),
			"nonce":      base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{3}, 12)),
			"ciphertext": base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{4}, 96)),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(
		http.MethodPost,
		"https://panel.example.com/system-updates/updaters/updater-01/bootstrap-jobs",
		bytes.NewReader(payload),
	)
	request.AddCookie(cookie)
	request.Header.Set("X-CSRF-Token", csrf)
	result := httptest.NewRecorder()
	handler.ServeHTTP(result, request)
	if result.Code != http.StatusConflict ||
		!strings.Contains(result.Body.String(), `"code":"updater_release_token_not_configured"`) {
		t.Fatalf("release token status=%d body=%s", result.Code, result.Body.String())
	}
	jobs, err := broker.List("updater-01")
	if err != nil || len(jobs) != 0 {
		t.Fatalf("bootstrap job queued without release token: jobs=%#v err=%v", jobs, err)
	}
}

func TestUpdateHostBootstrapJobRejectsMissingOrStaleRecipientKeyBeforeQueueing(t *testing.T) {
	for _, test := range []struct {
		name                 string
		recipientFingerprint string
		wantStatus           int
		wantCode             string
	}{
		{
			name:       "missing recipient fingerprint",
			wantStatus: http.StatusBadRequest,
			wantCode:   "invalid_bootstrap_job_request",
		},
		{
			name:                 "stale recipient fingerprint",
			recipientFingerprint: bootstrapFingerprintForTest(bytes.Repeat([]byte{9}, 65)),
			wantStatus:           http.StatusConflict,
			wantCode:             "bootstrap_recipient_key_changed",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			auth := store.NewMemoryAuthStore()
			if err := auth.AddUser(
				store.User{ID: "bootstrap-admin", Username: "bootstrap-admin"},
				"correct horse battery",
				[]string{"system_updates.read", "system_updates.execute", "secrets.update"},
			); err != nil {
				t.Fatal(err)
			}
			updaterToken := registerUpdateAgentForPolicyTest(t, auth, "updater-01")
			policies := store.NewMemoryUpdaterPolicyStore()
			hostKey, _ := ed25519AuthorizedKeyForTest(t, "")
			policy := savePullUpdaterPolicyForHTTPTest(t, policies, "updater-01", updaterPolicyForHTTPTest(hostKey))
			secrets := updaterReleaseTokenSecretStoreForBootstrapTest(t, "github_pat_bootstrap_release")
			bootstrapPublicKey := p256PublicKeyForBootstrapTest(t)
			currentFingerprint := bootstrapFingerprintForTest(bootstrapPublicKey)
			if _, err := auth.Heartbeat(t.Context(), updaterToken, store.ServiceHeartbeat{
				ServiceID: "updater-01", Status: "online", Version: "v1.8.0",
				Capabilities: map[string]any{
					"policy_revision":                      policy.ProjectionRevision,
					"policy_status":                        "applied",
					"bootstrap_encryption_public_key":      base64.RawURLEncoding.EncodeToString(bootstrapPublicKey),
					"bootstrap_encryption_key_fingerprint": currentFingerprint,
				},
			}); err != nil {
				t.Fatal(err)
			}
			broker := NewUpdateHostBootstrapBroker()
			handler := NewServer(
				store.NewMemoryStreamStore(),
				WithAuthStore(auth),
				WithAuditStore(auth),
				WithServiceRegistryStore(auth),
				WithUpdaterPolicyStore(policies),
				WithSecretStore(secrets),
				WithUpdateHostBootstrapBroker(broker),
			)
			cookie, csrf := loginForTest(t, handler, "bootstrap-admin", "correct horse battery")
			requestBody := map[string]any{
				"job_id": "6ba7b810-9dad-4f0e-9a58-4aee7cb5560f", "idempotency_key": "stale-recipient",
				"expected_revision": policy.Revision, "host_ids": []string{"host-01"},
				"envelope": map[string]any{
					"version": 1, "ephemeral_public_key": base64.RawURLEncoding.EncodeToString(p256PublicKeyForBootstrapTest(t)),
					"nonce":      base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{3}, 12)),
					"ciphertext": base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{4}, 96)),
				},
			}
			if test.recipientFingerprint != "" {
				requestBody["recipient_key_fingerprint"] = test.recipientFingerprint
			}
			payload, err := json.Marshal(requestBody)
			if err != nil {
				t.Fatal(err)
			}
			request := httptest.NewRequest(
				http.MethodPost,
				"https://panel.example.com/system-updates/updaters/updater-01/bootstrap-jobs",
				bytes.NewReader(payload),
			)
			request.AddCookie(cookie)
			request.Header.Set("X-CSRF-Token", csrf)
			result := httptest.NewRecorder()
			handler.ServeHTTP(result, request)
			if result.Code != test.wantStatus ||
				!strings.Contains(result.Body.String(), `"code":"`+test.wantCode+`"`) {
				t.Fatalf("recipient validation status=%d body=%s", result.Code, result.Body.String())
			}
			jobs, err := broker.List("updater-01")
			if err != nil || len(jobs) != 0 {
				t.Fatalf("bootstrap job queued for invalid recipient: jobs=%#v err=%v", jobs, err)
			}
		})
	}
}

func TestSystemUpdateAgentResponseExposesOnlyBootstrapPublicIdentity(t *testing.T) {
	now := time.Now().UTC()
	publicKeyBytes := p256PublicKeyForBootstrapTest(t)
	publicKey := base64.RawURLEncoding.EncodeToString(publicKeyBytes)
	publicKeyFingerprint := bootstrapFingerprintForTest(publicKeyBytes)
	services := []store.RegisteredService{{
		ServiceID: "updater-01", ServiceType: "update_agent", ServiceName: "Updater 01",
		TransportMode: store.SystemUpdateTransportPullV2, ExecutionHostID: "host-01",
		Status: "online", LastHeartbeatAt: &now,
		ReportedCapabilities: map[string]any{
			"bootstrap_encryption_public_key":      publicKey,
			"bootstrap_encryption_key_fingerprint": publicKeyFingerprint,
			"bootstrap_encryption_private_key":     "must-not-leak",
		},
	}}
	_, updaters, _ := systemUpdateAgentTopology(services, now)
	if len(updaters) != 1 ||
		updaters[0].BootstrapEncryptionPublicKey != publicKey ||
		updaters[0].BootstrapEncryptionKeyFingerprint != publicKeyFingerprint {
		t.Fatalf("bootstrap public identity response=%#v", updaters)
	}
	encoded, err := json.Marshal(updaters[0])
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "must-not-leak") || strings.Contains(string(encoded), "private_key") {
		t.Fatalf("bootstrap response exposed private capability: %s", encoded)
	}
	if !strings.Contains(string(encoded), `"bootstrap_encryption_key_fingerprint"`) ||
		strings.Contains(string(encoded), `"bootstrap_encryption_public_key_fingerprint"`) {
		t.Fatalf("bootstrap response used a non-canonical fingerprint field: %s", encoded)
	}

	services[0].ReportedCapabilities["bootstrap_encryption_key_fingerprint"] = "SHA256:mismatched"
	_, mismatched, _ := systemUpdateAgentTopology(services, now)
	if len(mismatched) != 1 ||
		mismatched[0].BootstrapEncryptionPublicKey != "" ||
		mismatched[0].BootstrapEncryptionKeyFingerprint != "" {
		t.Fatalf("mismatched bootstrap fingerprint was exposed: %#v", mismatched)
	}
}

func TestNormalizeUpdateHostBootstrapEnvelopeRejectsNonCanonicalInputs(t *testing.T) {
	valid := updateHostBootstrapEnvelopeRequest{
		Version:            1,
		EphemeralPublicKey: base64.RawURLEncoding.EncodeToString(p256PublicKeyForBootstrapTest(t)),
		Nonce:              base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{2}, 12)),
		Ciphertext:         base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{3}, 32)),
	}
	if _, err := normalizeUpdateHostBootstrapEnvelope(valid); err != nil {
		t.Fatalf("valid envelope rejected: %v", err)
	}
	cases := map[string]func(*updateHostBootstrapEnvelopeRequest){
		"wrong version": func(value *updateHostBootstrapEnvelopeRequest) { value.Version = 2 },
		"compressed key": func(value *updateHostBootstrapEnvelopeRequest) {
			value.EphemeralPublicKey = base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{2}, 65))
		},
		"off curve key": func(value *updateHostBootstrapEnvelopeRequest) {
			value.EphemeralPublicKey = base64.RawURLEncoding.EncodeToString(append([]byte{4}, make([]byte, 64)...))
		},
		"padded key": func(value *updateHostBootstrapEnvelopeRequest) { value.EphemeralPublicKey += "=" },
		"short nonce": func(value *updateHostBootstrapEnvelopeRequest) {
			value.Nonce = base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{2}, 11))
		},
		"padded nonce": func(value *updateHostBootstrapEnvelopeRequest) { value.Nonce += "=" },
		"short ciphertext": func(value *updateHostBootstrapEnvelopeRequest) {
			value.Ciphertext = base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{3}, 15))
		},
		"ciphertext whitespace": func(value *updateHostBootstrapEnvelopeRequest) { value.Ciphertext += " " },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			value := valid
			mutate(&value)
			if _, err := normalizeUpdateHostBootstrapEnvelope(value); err == nil {
				t.Fatal("invalid envelope accepted")
			}
		})
	}
}

func TestValidUpdateHostBootstrapJobIDMatchesUpdaterWireContract(t *testing.T) {
	if !validUpdateHostBootstrapJobID("6ba7b810-9dad-4f0e-9a58-4aee7cb5560f") {
		t.Fatal("lowercase UUID was rejected")
	}
	for _, invalid := range []string{
		"client-job-a",
		"6BA7B810-9DAD-4F0E-9A58-4AEE7CB5560F",
		"6ba7b8109dad4f0e9a584aee7cb5560f",
		" 6ba7b810-9dad-4f0e-9a58-4aee7cb5560f",
	} {
		if validUpdateHostBootstrapJobID(invalid) {
			t.Fatalf("invalid job ID accepted: %q", invalid)
		}
	}
}

func updaterReleaseTokenSecretStoreForBootstrapTest(t *testing.T, value string) *store.MemorySecretStore {
	t.Helper()
	secrets := store.NewMemorySecretStore()
	if _, err := secrets.UpdateSecret(t.Context(), store.UpdaterGitHubReleaseTokenSecretName, value); err != nil {
		t.Fatal(err)
	}
	return secrets
}

func bootstrapFingerprintForTest(publicKey []byte) string {
	digest := sha256.Sum256(publicKey)
	return "SHA256:" + base64.RawStdEncoding.EncodeToString(digest[:])
}

func p256PublicKeyForBootstrapTest(t *testing.T) []byte {
	t.Helper()
	privateKey, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return privateKey.PublicKey().Bytes()
}
