package httpapi

import (
	"bytes"
	"context"
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

type caseInsensitiveServiceLookupStore struct {
	store.ServiceRegistryStore
	scrubConfigureTokenHash bool
}

type caseInsensitiveTokenRevokeStore struct {
	store.ServiceRegistryStore
	revokeCalled bool
}

func (s *caseInsensitiveTokenRevokeStore) RevokeServiceToken(
	ctx context.Context,
	tokenID string,
) error {
	s.revokeCalled = true
	tokens, err := s.ListServiceTokens(ctx)
	if err != nil {
		return err
	}
	for _, token := range tokens {
		if strings.EqualFold(token.ID, tokenID) {
			return s.ServiceRegistryStore.RevokeServiceToken(ctx, token.ID)
		}
	}
	return store.ErrNotFound
}

func (s caseInsensitiveServiceLookupStore) GetService(
	ctx context.Context,
	serviceID string,
) (store.RegisteredService, error) {
	services, err := s.ListServices(ctx)
	if err != nil {
		return store.RegisteredService{}, err
	}
	for _, service := range services {
		if !strings.EqualFold(service.ServiceID, strings.TrimSpace(serviceID)) {
			continue
		}
		if s.scrubConfigureTokenHash {
			service.ConfigureTokenHash = ""
		}
		return service, nil
	}
	return store.RegisteredService{}, store.ErrNotFound
}

type updaterIdentityMutationFixture struct {
	auth                      *store.MemoryAuthStore
	server                    *Server
	broker                    *UpdateHostBootstrapBroker
	serviceID                 string
	executionHostID           string
	policyRevision            int64
	initialToken              store.ServiceToken
	cookie                    *http.Cookie
	csrf                      string
	stagedTokenID             string
	activationConfigurationID string
	activationToken           string
	activationPolicy          updateradapter.ConfigurePolicyProjection
}

func newUpdaterIdentityMutationFixture(
	t *testing.T,
	configured bool,
	stageNext bool,
) updaterIdentityMutationFixture {
	return newUpdaterIdentityMutationFixtureWithServiceStore(t, configured, stageNext, nil)
}

func newUpdaterIdentityMutationFixtureWithServiceStore(
	t *testing.T,
	configured bool,
	stageNext bool,
	wrapServiceStore func(*store.MemoryAuthStore) store.ServiceRegistryStore,
) updaterIdentityMutationFixture {
	t.Helper()
	t.Setenv("AUTOSTREAM_SECRET_ENCRYPTION_KEY", "test-secret-encryption-key-32-bytes")
	t.Setenv("AUTOSTREAM_PUBLIC_URL", "https://panel.example.com")
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(
		store.User{Username: "bootstrap-admin"},
		"correct horse battery",
		[]string{
			"api_tokens.create",
			"api_tokens.revoke",
			"services.disable",
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
	const serviceID = "updater-identity-guard"
	if _, err := auth.PrecreateService(t.Context(), token, store.ServiceRegistration{
		ServiceID:       serviceID,
		ServiceType:     "update_agent",
		ServiceName:     "Updater",
		TransportMode:   store.SystemUpdateTransportPullV2,
		ExecutionHostID: "host-identity-guard",
		Version:         "v1.0.0",
	}); err != nil {
		t.Fatal(err)
	}
	if configured {
		staged := stageUpdaterIdentityConfiguration(t, auth, serviceID, "configure-initial")
		if _, _, _, err := auth.ActivateServiceNodeConfiguration(
			t.Context(),
			serviceID,
			staged.Token.ID,
			staged.ActivationToken,
			time.Now().UTC(),
			store.ServiceRuntimeReport{ServiceID: serviceID, Version: "v1.0.0"},
		); err != nil {
			t.Fatal(err)
		}
	}
	workerToken, err := auth.CreateServiceToken(
		t.Context(),
		"worker",
		[]string{"service.register", "service.heartbeat"},
	)
	if err != nil {
		t.Fatal(err)
	}
	registerServiceWithTokenForTest(t, auth, workerToken, store.ServiceRegistration{
		ServiceID: "worker-01", ServiceType: "worker", ServiceName: "Worker 01",
		Host: "worker-01.example.com", Port: 8084, SSLEnabled: true,
		PublicURL: "https://worker-01.example.com:8084", Version: "v1.0.0",
	})
	policies := store.NewMemoryUpdaterPolicyStore()
	updates := store.NewMemorySystemUpdateStore()
	hostKey, _ := ed25519AuthorizedKeyForTest(t, "")
	policyInput := updaterPolicyForHTTPTest(hostKey)
	policyInput.ExecutionHostID = "host-identity-guard"
	policyInput.Targets[0].HostID = "host-identity-guard"
	policy, err := policies.SavePullUpdaterPolicy(
		t.Context(), updates, serviceID, 0, 0, policyInput,
	)
	if err != nil {
		t.Fatal(err)
	}

	fixture := updaterIdentityMutationFixture{
		auth:            auth,
		broker:          NewUpdateHostBootstrapBroker(),
		serviceID:       serviceID,
		executionHostID: policy.ExecutionHostID,
		policyRevision:  policy.ProjectionRevision,
		initialToken:    token,
	}
	if stageNext {
		staged := stageUpdaterIdentityConfiguration(t, auth, serviceID, "configure-next")
		fixture.stagedTokenID = staged.Token.ID
		fixture.activationToken = staged.ActivationToken
	}
	var serviceStore store.ServiceRegistryStore = auth
	if wrapServiceStore != nil {
		serviceStore = wrapServiceStore(auth)
	}
	fixture.server = NewServer(
		store.NewMemoryStreamStore(),
		WithAuthStore(auth),
		WithAuditStore(auth),
		WithServiceRegistryStore(serviceStore),
		WithUpdaterPolicyStore(policies),
		WithSystemUpdateStore(updates),
		WithUpdateHostBootstrapBroker(fixture.broker),
	)
	if stageNext {
		service, err := auth.GetService(t.Context(), serviceID)
		if err != nil {
			t.Fatal(err)
		}
		projectionRequest := httptest.NewRequest(
			http.MethodPost,
			"/services/host-agent/runtime-identity/activate",
			nil,
		)
		projection, err := fixture.server.hostAgentConfigurePolicyProjection(
			t.Context(), projectionRequest, service,
			updaterIdentityFixtureAgentUID, updaterIdentityFixtureAgentGID,
		)
		if err != nil {
			t.Fatal(err)
		}
		bindingKey, err := nodeRuntimeTokenEncryptionKey()
		if err != nil {
			t.Fatal(err)
		}
		fixture.activationConfigurationID = hostAgentConfigureBoundConfigurationID(
			fixture.stagedTokenID,
			updaterIdentityFixtureAgentUID,
			updaterIdentityFixtureAgentGID,
			projection,
			bindingKey,
		)
		fixture.activationPolicy = projection
	}
	fixture.cookie, fixture.csrf = loginForTest(
		t,
		fixture.server,
		"bootstrap-admin",
		"correct horse battery",
	)
	return fixture
}

func stageUpdaterIdentityConfiguration(
	t *testing.T,
	auth *store.MemoryAuthStore,
	serviceID string,
	rawConfigureToken string,
) store.StagedServiceNodeConfiguration {
	t.Helper()
	if _, err := auth.SetServiceConfigureToken(
		t.Context(),
		serviceID,
		security.HashToken(rawConfigureToken),
		time.Now().UTC().Add(time.Hour),
	); err != nil {
		t.Fatal(err)
	}
	seal, err := nodeRuntimeTokenSealer()
	if err != nil {
		t.Fatal(err)
	}
	staged, err := auth.StageServiceNodeConfiguration(
		t.Context(),
		serviceID,
		rawConfigureToken,
		time.Now().UTC(),
		seal,
	)
	if err != nil {
		t.Fatal(err)
	}
	return staged
}

func (f updaterIdentityMutationFixture) createBootstrapJob(
	t *testing.T,
	state UpdateHostBootstrapStatus,
) UpdateHostBootstrapJob {
	t.Helper()
	const revision = 7
	fingerprint := bootstrapBrokerRecipientFingerprint(1)
	job, _, err := f.broker.Create(UpdateHostBootstrapCreateParams{
		UpdaterID:               f.serviceID,
		ExpectedRevision:        revision,
		ClientJobID:             "identity-guard-job",
		IdempotencyKey:          "identity-guard-idempotency",
		RecipientKeyFingerprint: fingerprint,
		HostIDs:                 []string{"host-a"},
		Envelope:                []byte("opaque-credential"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if state == UpdateHostBootstrapStatusQueued {
		return job
	}
	claim, err := f.broker.Claim(f.serviceID, revision, fingerprint, fingerprint)
	if err != nil {
		t.Fatal(err)
	}
	if state == UpdateHostBootstrapStatusClaimed {
		return job
	}
	if _, err := f.broker.Accept(job.ID, f.serviceID, revision, claim.LeaseToken); err != nil {
		t.Fatal(err)
	}
	if _, err := f.broker.Report(UpdateHostBootstrapReportParams{
		JobID:            job.ID,
		UpdaterID:        f.serviceID,
		ExpectedRevision: revision,
		LeaseToken:       claim.LeaseToken,
		HostID:           "host-a",
		Status:           UpdateHostBootstrapHostStatusConnecting,
		Progress:         10,
	}); err != nil {
		t.Fatal(err)
	}
	if state != UpdateHostBootstrapStatusRunning {
		t.Fatalf("unsupported active bootstrap state %q", state)
	}
	return job
}

func (f updaterIdentityMutationFixture) createActiveSystemUpdateJob(
	t *testing.T,
	idempotencySuffix string,
) store.SystemUpdateJob {
	t.Helper()
	executionHosts, ok := f.server.systemUpdates.(store.SystemUpdateExecutionHostStore)
	if !ok {
		t.Fatalf("unexpected system update store %T", f.server.systemUpdates)
	}
	ownership, err := executionHosts.GetSystemUpdateExecutionHost(t.Context(), f.executionHostID)
	if err != nil {
		t.Fatal(err)
	}
	if ownership.OwnershipEpoch == 0 {
		if _, err := executionHosts.SwitchSystemUpdateExecutionHost(
			t.Context(), f.executionHostID, 0,
			store.SystemUpdateTransportPullV2, f.serviceID, f.policyRevision,
		); err != nil {
			t.Fatal(err)
		}
	}
	job, _, err := f.server.systemUpdates.CreateSystemUpdateJob(
		t.Context(),
		store.CreateSystemUpdateJobParams{
			TargetID:          "worker-" + idempotencySuffix,
			TargetServiceType: "worker",
			DeploymentMode:    "systemd",
			AgentServiceID:    f.serviceID,
			ExecutionHostID:   f.executionHostID,
			CurrentVersion:    "v1.0.0",
			TargetVersion:     "v1.1.0",
			Strategy:          store.SystemUpdateStrategyWhenIdle,
			IdempotencyKey:    "identity-guard-" + idempotencySuffix,
			RequestedByUserID: "bootstrap-admin",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	return job
}

func (f updaterIdentityMutationFixture) cancelBootstrapJob(
	t *testing.T,
	job UpdateHostBootstrapJob,
) {
	t.Helper()
	if _, err := f.broker.Cancel(job.ID, f.serviceID, job.ExpectedRevision); err != nil {
		t.Fatal(err)
	}
	active, err := f.broker.HasActiveJob(f.serviceID)
	if err != nil {
		t.Fatal(err)
	}
	if active {
		t.Fatal("terminal bootstrap job remained active")
	}
}

func (f updaterIdentityMutationFixture) activationRequestBody(t *testing.T) string {
	t.Helper()
	payload, err := json.Marshal(map[string]any{
		"nodeId":                      f.serviceID,
		"configurationId":             f.activationConfigurationID,
		"activationToken":             f.activationToken,
		"version":                     "v1.0.1",
		"configureProtocolVersion":    updateradapter.HostAgentConfigureProtocolVersion,
		"agentUid":                    updaterIdentityFixtureAgentUID,
		"agentGid":                    updaterIdentityFixtureAgentGID,
		"localExecutorPolicySha256":   f.activationPolicy.SHA256,
		"sourcePolicyRevision":        f.activationPolicy.SourcePolicyRevision,
		"projectionRevision":          f.activationPolicy.ProjectionRevision,
		"localExecutorPolicyRevision": f.activationPolicy.PolicyRevision,
	})
	if err != nil {
		t.Fatal(err)
	}
	return string(payload)
}

func (f updaterIdentityMutationFixture) rotateRuntimeIdentity(t *testing.T) string {
	t.Helper()
	return rotateUpdaterRuntimeIdentityForTest(t, f.server, f.serviceID, f.adminRequest)
}

func (f updaterIdentityMutationFixture) adminRequest(
	t *testing.T,
	method string,
	path string,
	body string,
) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, path, bytes.NewBufferString(body))
	request.AddCookie(f.cookie)
	request.Header.Set("X-CSRF-Token", f.csrf)
	result := httptest.NewRecorder()
	f.server.ServeHTTP(result, request)
	return result
}

func (f updaterIdentityMutationFixture) publicRequest(
	t *testing.T,
	method string,
	path string,
	body string,
) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, path, bytes.NewBufferString(body))
	result := httptest.NewRecorder()
	f.server.ServeHTTP(result, request)
	return result
}

func assertUpdaterBootstrapMutationConflict(t *testing.T, result *httptest.ResponseRecorder) {
	t.Helper()
	if result.Code != http.StatusConflict ||
		!strings.Contains(result.Body.String(), `"code":"updater_host_bootstrap_in_progress"`) {
		t.Fatalf("active bootstrap mutation status=%d body=%s", result.Code, result.Body.String())
	}
}

func assertUpdaterSystemUpdateMutationConflict(t *testing.T, result *httptest.ResponseRecorder) {
	t.Helper()
	if result.Code != http.StatusConflict ||
		!strings.Contains(result.Body.String(), `"code":"system_update_active"`) {
		t.Fatalf("active system update mutation status=%d body=%s", result.Code, result.Body.String())
	}
}
