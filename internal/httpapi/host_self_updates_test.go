package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/example/autostream-control-panel/internal/security"
	"github.com/example/autostream-control-panel/internal/store"
)

type fixedHostSelfUpdateReleaseResolver struct {
	release store.SystemUpdateHostReleaseMetadata
}

func (r fixedHostSelfUpdateReleaseResolver) ResolveHostSelfUpdateRelease(
	_ context.Context,
	version, arch string,
) (store.SystemUpdateHostReleaseMetadata, error) {
	release := r.release
	release.Tag = version
	release.Arch = arch
	return release, nil
}

func TestHostSelfUpdateHTTPPolicyAndGrantRemainExactlyBound(t *testing.T) {
	t.Setenv(
		"AUTOSTREAM_SECRET_ENCRYPTION_KEY",
		"test-secret-encryption-key-32-bytes",
	)
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(
		store.User{ID: "self-update-admin", Username: "self-update-admin"},
		"correct horse battery",
		[]string{
			"system_updates.read",
			"system_updates.execute",
			"api_tokens.create",
			"api_tokens.revoke",
			"secrets.update",
		},
	); err != nil {
		t.Fatal(err)
	}
	policies := store.NewMemoryUpdaterPolicyStore()
	updates := store.NewMemorySystemUpdateStore()
	saved, err := policies.SavePullUpdaterPolicy(
		t.Context(), updates, "host-agent-a", 0, 0,
		store.UpdaterPolicy{
			TransportMode:             store.SystemUpdateTransportPullV2,
			ExecutionHostID:           "host-a",
			LocalExecutorPolicySHA256: "sha256:" + strings.Repeat("a", 64),
			PollIntervalSeconds:       15,
			HeartbeatIntervalSeconds:  30,
			Targets: []store.UpdaterPolicyTarget{{
				TargetID:       "worker-a",
				ServiceID:      "worker-a",
				HostID:         "host-a",
				ServiceType:    "worker",
				DeploymentMode: "systemd",
			}},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	ownership, err := updates.SwitchSystemUpdateExecutionHost(
		t.Context(), "host-a", 0, store.SystemUpdateTransportPullV2,
		"host-agent-a", saved.ProjectionRevision,
	)
	if err != nil {
		t.Fatal(err)
	}
	token := registerRuntimeTokenRotationAgentForTest(
		t, auth, "host-agent-a", "host-a", ownership.OwnershipEpoch,
	)
	if _, err := auth.Heartbeat(
		t.Context(), token,
		store.ServiceHeartbeat{
			ServiceID: "host-agent-a",
			Status:    "online",
			Version:   "v1.7.8",
			OS:        "linux",
			Arch:      "amd64",
			Capabilities: map[string]any{
				"self_update_ready":                   true,
				"self_update_phase":                   "stable",
				"self_update_active_agent_version":    "v1.7.8",
				"self_update_active_executor_version": "v1.7.8",
				"executor_version":                    "v1.7.8",
				"agent_protocol_version":              "2",
				"executor_protocol_version":           2,
				"mutation_protocol_version":           2,
				"recovery_protocol_version": store.
					SystemUpdateHostSelfUpdateMinimumRecoveryProtocolVersion,
				"mutation_enabled":   true,
				"recovery_pending":   false,
				"self_update_cancel": true,
			},
		},
	); err != nil {
		t.Fatal(err)
	}
	published := time.Date(2026, 7, 27, 1, 0, 0, 0, time.UTC)
	handler := NewServer(
		store.NewMemoryStreamStore(),
		WithAuthStore(auth),
		WithAuditStore(auth),
		WithServiceRegistryStore(auth),
		WithUpdaterPolicyStore(policies),
		WithSystemUpdateStore(updates),
		WithHostSelfUpdateReleaseResolver(
			fixedHostSelfUpdateReleaseResolver{
				release: store.SystemUpdateHostReleaseMetadata{
					Commit:                  strings.Repeat("b", 40),
					PublishedAt:             published,
					ManifestAssetID:         1,
					ManifestAssetName:       "host-agent-manifest.json",
					ManifestSHA256:          strings.Repeat("1", 64),
					ManifestChecksumAssetID: 2,
					ManifestChecksumSHA256:  strings.Repeat("2", 64),
					ArchiveAssetID:          3,
					ArchiveAssetName:        "autostream-host-agent_v1.8.0_linux_amd64.tar.gz",
					ArchiveSize:             4096,
					ArchiveSHA256:           strings.Repeat("3", 64),
					ArchiveChecksumAssetID:  4,
					ArchiveChecksumSHA256:   strings.Repeat("4", 64),
					AgentProtocolVersion:    2,
					ExecutorProtocolVersion: 2,
					MutationProtocolVersion: 2,
					RecoveryProtocolVersion: store.
						SystemUpdateHostSelfUpdateMinimumRecoveryProtocolVersion,
					MinimumPanelVersion:   "v1.8.0",
					AttestationVerifiedAt: time.Now().UTC().Add(-time.Minute),
				},
			},
		),
	)
	cookie, csrf := loginForTest(
		t, handler, "self-update-admin", "correct horse battery",
	)
	createRequest := httptest.NewRequest(
		http.MethodPost,
		"/system-updates/hosts/host-a/self-updates",
		strings.NewReader(
			`{"target_version":"v1.8.0","idempotency_key":"self-http-a"}`,
		),
	)
	createRequest.AddCookie(cookie)
	createRequest.Header.Set("X-CSRF-Token", csrf)
	createResponse := httptest.NewRecorder()
	handler.ServeHTTP(createResponse, createRequest)
	if createResponse.Code != http.StatusAccepted {
		t.Fatalf(
			"create status=%d body=%s",
			createResponse.Code, createResponse.Body.String(),
		)
	}
	if createResponse.Header().Get("Cache-Control") != "no-store" ||
		strings.Contains(createResponse.Body.String(), "github.com") ||
		strings.Contains(createResponse.Body.String(), "token") {
		t.Fatalf("create leaked transport data: %s", createResponse.Body.String())
	}
	var created store.SystemUpdateHostSelfUpdate
	if err := json.NewDecoder(createResponse.Body).Decode(&created); err != nil {
		t.Fatal(err)
	}
	if created.IssuedAt.Equal(published) ||
		created.Release.MinimumPanelVersion != "v1.8.0" {
		t.Fatalf("issued/release times were conflated: %#v", created)
	}
	configureTokenRequest := httptest.NewRequest(
		http.MethodPost,
		"/nodes/host-agent-a/configure-token",
		nil,
	)
	configureTokenRequest.AddCookie(cookie)
	configureTokenRequest.Header.Set("X-CSRF-Token", csrf)
	configureTokenResponse := httptest.NewRecorder()
	handler.ServeHTTP(configureTokenResponse, configureTokenRequest)
	if configureTokenResponse.Code != http.StatusConflict ||
		!strings.Contains(
			configureTokenResponse.Body.String(),
			`"code":"system_update_active"`,
		) {
		t.Fatalf(
			"active self-update configure-token status=%d body=%s",
			configureTokenResponse.Code,
			configureTokenResponse.Body.String(),
		)
	}

	policyRequest := httptest.NewRequest(
		http.MethodPost, "/services/host-agent/policy",
		strings.NewReader(
			`{"service_id":"host-agent-a","current_revision":0}`,
		),
	)
	policyRequest.Header.Set("Authorization", "Bearer "+token.RawToken)
	policyResponse := httptest.NewRecorder()
	handler.ServeHTTP(policyResponse, policyRequest)
	if policyResponse.Code != http.StatusOK {
		t.Fatalf(
			"policy status=%d body=%s",
			policyResponse.Code, policyResponse.Body.String(),
		)
	}
	var policy hostAgentPolicyResponse
	if err := json.NewDecoder(policyResponse.Body).Decode(&policy); err != nil {
		t.Fatal(err)
	}
	if policy.SelfUpdate == nil || policy.RuntimeRequirement == nil ||
		policy.SelfUpdateID != created.ID ||
		policy.SelfUpdateRevision < 1 ||
		policy.SelfUpdate.Generation != created.AttemptGeneration ||
		policy.SelfUpdate.StagedAt.Equal(published) {
		t.Fatalf("policy omitted exact self-update: %#v", policy)
	}
	if policy.SelfUpdate.Release.Tag != created.Release.Tag ||
		policy.SelfUpdate.Release.ManifestAssetID !=
			created.Release.ManifestAssetID ||
		policy.SelfUpdate.Release.ManifestSHA256 !=
			created.Release.ManifestSHA256 ||
		policy.SelfUpdate.Release.ManifestChecksumAssetID !=
			created.Release.ManifestChecksumAssetID ||
		policy.SelfUpdate.Release.ManifestChecksumSHA256 !=
			created.Release.ManifestChecksumSHA256 ||
		policy.SelfUpdate.Release.ArchiveAssetID !=
			created.Release.ArchiveAssetID ||
		policy.SelfUpdate.Release.ArchiveAssetName !=
			created.Release.ArchiveAssetName ||
		policy.SelfUpdate.Release.ArchiveSize !=
			created.Release.ArchiveSize ||
		policy.SelfUpdate.Release.ArchiveSHA256 !=
			created.Release.ArchiveSHA256 ||
		policy.SelfUpdate.Release.ArchiveChecksumAssetID !=
			created.Release.ArchiveChecksumAssetID ||
		policy.SelfUpdate.Release.ArchiveChecksumSHA256 !=
			created.Release.ArchiveChecksumSHA256 ||
		policy.SelfUpdate.Release.MinimumPanelVersion !=
			created.Release.MinimumPanelVersion ||
		!policy.SelfUpdate.Release.PublishedAt.Equal(
			created.Release.PublishedAt,
		) {
		t.Fatalf("policy dropped immutable release metadata: %#v", policy.SelfUpdate)
	}

}
func TestHostSelfUpdateCreateSerializesAgainstConfigureStage(t *testing.T) {
	fixture := newHostSelfUpdateIdentityRaceFixture(t)
	fixture.server.systemUpdateOperationMu.Lock()
	locked := true
	defer func() {
		if locked {
			fixture.server.systemUpdateOperationMu.Unlock()
		}
	}()

	createDone := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		request := httptest.NewRequest(
			http.MethodPost,
			"/system-updates/hosts/host-a/self-updates",
			strings.NewReader(
				`{"target_version":"v1.8.0","idempotency_key":"self-update-race"}`,
			),
		)
		request.AddCookie(fixture.cookie)
		request.Header.Set("X-CSRF-Token", fixture.csrf)
		response := httptest.NewRecorder()
		fixture.server.ServeHTTP(response, request)
		createDone <- response
	}()

	stageDone := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		body, err := json.Marshal(map[string]any{
			"nodeId":          "host-agent-a",
			"configureToken":  fixture.configureToken,
			"protocolVersion": updateagent.HostAgentConfigureProtocolVersion,
			"agentUid":        uint32(1001),
			"agentGid":        uint32(1002),
		})
		if err != nil {
			stageDone <- httptest.NewRecorder()
			return
		}
		request := httptest.NewRequest(
			http.MethodPost,
			"/api/node-agent/configure/stage",
			strings.NewReader(string(body)),
		)
		response := httptest.NewRecorder()
		fixture.server.ServeHTTP(response, request)
		stageDone <- response
	}()

	select {
	case response := <-createDone:
		t.Fatalf(
			"self-update create bypassed lifecycle lock: status=%d body=%s",
			response.Code,
			response.Body.String(),
		)
	case response := <-stageDone:
		t.Fatalf(
			"configure stage bypassed lifecycle lock: status=%d body=%s",
			response.Code,
			response.Body.String(),
		)
	case <-time.After(50 * time.Millisecond):
	}

	fixture.server.systemUpdateOperationMu.Unlock()
	locked = false
	var createResponse, stageResponse *httptest.ResponseRecorder
	select {
	case createResponse = <-createDone:
	case <-time.After(time.Second):
		t.Fatal("self-update create did not resume after lifecycle lock release")
	}
	select {
	case stageResponse = <-stageDone:
	case <-time.After(time.Second):
		t.Fatal("configure stage did not resume after lifecycle lock release")
	}

	switch {
	case createResponse.Code == http.StatusAccepted:
		if stageResponse.Code != http.StatusConflict ||
			!strings.Contains(
				stageResponse.Body.String(),
				`"code":"system_update_active"`,
			) {
			t.Fatalf(
				"create winner did not fence configure stage: create=%d %s stage=%d %s",
				createResponse.Code,
				createResponse.Body.String(),
				stageResponse.Code,
				stageResponse.Body.String(),
			)
		}
		if _, err := fixture.updates.GetActiveSystemUpdateHostSelfUpdateByExecutionHost(
			t.Context(),
			"host-a",
		); err != nil {
			t.Fatalf("accepted self-update was not active: %v", err)
		}
	case stageResponse.Code == http.StatusOK:
		if createResponse.Code != http.StatusConflict ||
			!strings.Contains(
				createResponse.Body.String(),
				`"code":"host_lifecycle_busy"`,
			) {
			t.Fatalf(
				"stage winner did not fence self-update create: create=%d %s stage=%d %s",
				createResponse.Code,
				createResponse.Body.String(),
				stageResponse.Code,
				stageResponse.Body.String(),
			)
		}
		if _, err := fixture.updates.GetActiveSystemUpdateHostSelfUpdateByExecutionHost(
			t.Context(),
			"host-a",
		); !errors.Is(err, store.ErrNotFound) {
			t.Fatalf("rejected self-update became active: %v", err)
		}
		service, err := fixture.auth.GetService(t.Context(), "host-agent-a")
		if err != nil {
			t.Fatal(err)
		}
		if service.StagedNodeTokenID == "" {
			t.Fatalf("accepted configure stage omitted staged identity: %#v", service)
		}
	default:
		t.Fatalf(
			"lifecycle barrier produced no valid winner: create=%d %s stage=%d %s",
			createResponse.Code,
			createResponse.Body.String(),
			stageResponse.Code,
			stageResponse.Body.String(),
		)
	}
}

func TestHostSelfUpdateRetryUsesLifecycleLock(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(
		store.User{ID: "retry-admin", Username: "retry-admin"},
		"correct horse battery",
		[]string{"system_updates.execute"},
	); err != nil {
		t.Fatal(err)
	}
	probe := &hostSelfUpdateRetryProbeStore{
		MemorySystemUpdateStore: store.NewMemorySystemUpdateStore(),
		entered:                 make(chan struct{}),
	}
	server := NewServer(
		store.NewMemoryStreamStore(),
		WithAuthStore(auth),
		WithServiceRegistryStore(auth),
		WithSystemUpdateStore(probe),
	)
	cookie, csrf := loginForTest(
		t,
		server,
		"retry-admin",
		"correct horse battery",
	)
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
			"/system-updates/host-self-updates/missing/retry",
			strings.NewReader(`{"idempotency_key":"retry-lock"}`),
		)
		request.AddCookie(cookie)
		request.Header.Set("X-CSRF-Token", csrf)
		response := httptest.NewRecorder()
		server.ServeHTTP(response, request)
		done <- response
	}()
	select {
	case <-probe.entered:
		t.Fatal("retry store call bypassed lifecycle lock")
	case response := <-done:
		t.Fatalf(
			"retry completed before lifecycle lock release: status=%d body=%s",
			response.Code,
			response.Body.String(),
		)
	case <-time.After(50 * time.Millisecond):
	}
	server.systemUpdateOperationMu.Unlock()
	locked = false
	select {
	case <-probe.entered:
	case <-time.After(time.Second):
		t.Fatal("retry store call did not resume after lifecycle lock release")
	}
	select {
	case response := <-done:
		if response.Code != http.StatusNotFound {
			t.Fatalf(
				"serialized retry status=%d body=%s",
				response.Code,
				response.Body.String(),
			)
		}
	case <-time.After(time.Second):
		t.Fatal("serialized retry did not complete")
	}
}

func TestHostSelfUpdateJSONDecoderIsBoundedStrictAndSingleDocument(t *testing.T) {
	for _, test := range []struct {
		name string
		body string
	}{
		{
			name: "oversized prefix",
			body: strings.Repeat(" ", maxHostSelfUpdateRequestBytes+1) +
				`{"value":"accepted"}`,
		},
		{
			name: "unknown field",
			body: `{"value":"accepted","unexpected":true}`,
		},
		{
			name: "trailing document",
			body: `{"value":"accepted"}{"value":"replayed"}`,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(
				http.MethodPost, "/", strings.NewReader(test.body),
			)
			var decoded struct {
				Value string `json:"value"`
			}
			if err := decodeHostSelfUpdateJSON(request, &decoded); err == nil {
				t.Fatalf("unsafe host self-update JSON accepted: %q", test.body)
			}
		})
	}

	request := httptest.NewRequest(
		http.MethodPost, "/", strings.NewReader(`{"value":"accepted"}`),
	)
	var decoded struct {
		Value string `json:"value"`
	}
	if err := decodeHostSelfUpdateJSON(request, &decoded); err != nil ||
		decoded.Value != "accepted" {
		t.Fatalf("bounded exact JSON rejected: value=%q err=%v", decoded.Value, err)
	}
}

func TestHostSelfUpdateGrantConsumeRateLimitsInvalidSoleBearer(t *testing.T) {
	handler := NewServer(
		store.NewMemoryStreamStore(),
		WithSystemUpdateStore(store.NewMemorySystemUpdateStore()),
	)
	for attempt := 1; attempt <= hostSelfUpdateGrantConsumeAttemptThreshold; attempt++ {
		request := httptest.NewRequest(
			http.MethodPost,
			"/services/host-agent/self-update-grants/consume",
			strings.NewReader(`{`),
		)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if attempt < hostSelfUpdateGrantConsumeAttemptThreshold {
			if response.Code != http.StatusBadRequest {
				t.Fatalf(
					"attempt %d status=%d body=%s",
					attempt, response.Code, response.Body.String(),
				)
			}
			continue
		}
		if response.Code != http.StatusTooManyRequests ||
			response.Header().Get("Retry-After") != "300" {
			t.Fatalf(
				"rate limit status=%d retry-after=%q body=%s",
				response.Code,
				response.Header().Get("Retry-After"),
				response.Body.String(),
			)
		}
	}
}

type hostSelfUpdateIdentityRaceFixture struct {
	auth           *store.MemoryAuthStore
	updates        *store.MemorySystemUpdateStore
	server         *Server
	cookie         *http.Cookie
	csrf           string
	configureToken string
}

func newHostSelfUpdateIdentityRaceFixture(
	t *testing.T,
) hostSelfUpdateIdentityRaceFixture {
	t.Helper()
	t.Setenv(
		"AUTOSTREAM_SECRET_ENCRYPTION_KEY",
		"test-secret-encryption-key-32-bytes",
	)
	t.Setenv("AUTOSTREAM_PUBLIC_URL", "https://panel.example.com")
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(
		store.User{ID: "race-admin", Username: "race-admin"},
		"correct horse battery",
		[]string{
			"system_updates.execute",
			"api_tokens.create",
			"api_tokens.revoke",
			"secrets.update",
		},
	); err != nil {
		t.Fatal(err)
	}
	workerToken, err := auth.CreateServiceToken(
		t.Context(),
		"worker",
		[]string{"service.register", "service.heartbeat"},
	)
	if err != nil {
		t.Fatal(err)
	}
	worker := registerServiceWithTokenForTest(
		t,
		auth,
		workerToken,
		store.ServiceRegistration{
			ServiceID:   "worker-a",
			ServiceType: "worker",
			ServiceName: "Worker A",
			Host:        "worker.example.com",
			Port:        18081,
			SSLEnabled:  true,
			PublicURL:   "https://worker.example.com:18081",
			Version:     "v1.0.0",
		},
	)
	updates := store.NewMemorySystemUpdateStore()
	policies := store.NewMemoryUpdaterPolicyStore()
	saved, err := policies.SavePullUpdaterPolicy(
		t.Context(),
		updates,
		"host-agent-a",
		0,
		0,
		store.UpdaterPolicy{
			TransportMode:             store.SystemUpdateTransportPullV2,
			ExecutionHostID:           "host-a",
			LocalExecutorPolicySHA256: "sha256:" + strings.Repeat("a", 64),
			PollIntervalSeconds:       15,
			HeartbeatIntervalSeconds:  30,
			Targets: []store.UpdaterPolicyTarget{{
				TargetID:       worker.ServiceID,
				ServiceID:      worker.ServiceID,
				ServiceType:    worker.ServiceType,
				HostID:         "host-a",
				DeploymentMode: updateagent.ModeSystemd,
			}},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	ownership, err := updates.SwitchSystemUpdateExecutionHost(
		t.Context(),
		"host-a",
		0,
		store.SystemUpdateTransportPullV2,
		"host-agent-a",
		saved.ProjectionRevision,
	)
	if err != nil {
		t.Fatal(err)
	}
	token := registerRuntimeTokenRotationAgentForTest(
		t,
		auth,
		"host-agent-a",
		"host-a",
		ownership.OwnershipEpoch,
	)
	if _, err := auth.Heartbeat(
		t.Context(),
		token,
		store.ServiceHeartbeat{
			ServiceID: "host-agent-a",
			Status:    "online",
			Version:   "v1.7.8",
			OS:        "linux",
			Arch:      "amd64",
			Capabilities: map[string]any{
				"self_update_ready":                   true,
				"self_update_phase":                   "stable",
				"self_update_active_agent_version":    "v1.7.8",
				"self_update_active_executor_version": "v1.7.8",
				"executor_version":                    "v1.7.8",
				"agent_protocol_version":              2,
				"executor_protocol_version":           2,
				"mutation_protocol_version":           2,
				"recovery_protocol_version": store.
					SystemUpdateHostSelfUpdateMinimumRecoveryProtocolVersion,
				"mutation_enabled": true,
				"recovery_pending": false,
			},
		},
	); err != nil {
		t.Fatal(err)
	}
	const configureToken = "host-self-update-race-configure-token"
	if _, err := auth.SetServiceConfigureToken(
		t.Context(),
		"host-agent-a",
		security.HashToken(configureToken),
		time.Now().UTC().Add(time.Hour),
	); err != nil {
		t.Fatal(err)
	}
	server := NewServer(
		store.NewMemoryStreamStore(),
		WithAuthStore(auth),
		WithAuditStore(auth),
		WithServiceRegistryStore(auth),
		WithUpdaterPolicyStore(policies),
		WithSystemUpdateStore(updates),
		WithHostSelfUpdateReleaseResolver(
			fixedHostSelfUpdateReleaseResolver{
				release: store.SystemUpdateHostReleaseMetadata{
					Commit:                  strings.Repeat("b", 40),
					PublishedAt:             time.Now().UTC().Add(-time.Hour),
					ManifestAssetID:         1,
					ManifestAssetName:       "host-agent-manifest.json",
					ManifestSHA256:          strings.Repeat("1", 64),
					ManifestChecksumAssetID: 2,
					ManifestChecksumSHA256:  strings.Repeat("2", 64),
					ArchiveAssetID:          3,
					ArchiveAssetName:        "autostream-host-agent_v1.8.0_linux_amd64.tar.gz",
					ArchiveSize:             4096,
					ArchiveSHA256:           strings.Repeat("3", 64),
					ArchiveChecksumAssetID:  4,
					ArchiveChecksumSHA256:   strings.Repeat("4", 64),
					AgentProtocolVersion:    2,
					ExecutorProtocolVersion: 2,
					MutationProtocolVersion: 2,
					RecoveryProtocolVersion: store.
						SystemUpdateHostSelfUpdateMinimumRecoveryProtocolVersion,
					MinimumPanelVersion:   "v1.8.0",
					AttestationVerifiedAt: time.Now().UTC().Add(-time.Minute),
				},
			},
		),
	)
	cookie, csrf := loginForTest(
		t,
		server,
		"race-admin",
		"correct horse battery",
	)
	return hostSelfUpdateIdentityRaceFixture{
		auth:           auth,
		updates:        updates,
		server:         server,
		cookie:         cookie,
		csrf:           csrf,
		configureToken: configureToken,
	}
}

type hostSelfUpdateRetryProbeStore struct {
	*store.MemorySystemUpdateStore
	entered chan struct{}
}

func (s *hostSelfUpdateRetryProbeStore) RetrySystemUpdateHostSelfUpdate(
	context.Context,
	store.ServiceRegistryStore,
	store.UpdaterPolicyStore,
	store.RetrySystemUpdateHostSelfUpdateParams,
) (store.SystemUpdateHostSelfUpdate, bool, error) {
	close(s.entered)
	return store.SystemUpdateHostSelfUpdate{}, false, store.ErrNotFound
}

func jsonInteger(value int64) string {
	data, _ := json.Marshal(value)
	return string(data)
}
