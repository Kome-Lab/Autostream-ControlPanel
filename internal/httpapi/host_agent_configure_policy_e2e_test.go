package httpapi

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/example/autostream-control-panel/internal/security"
	"github.com/example/autostream-control-panel/internal/store"
	"github.com/example/autostream-control-panel/internal/updateagent"
)

func TestHostAgentConfigureBindsStageProjectionThroughActivation(t *testing.T) {
	t.Setenv("AUTOSTREAM_BIND_ADDR", "0.0.0.0:80")
	t.Setenv("AUTOSTREAM_CONFIG_REVISION", "0")
	t.Setenv(
		"AUTOSTREAM_SECRET_ENCRYPTION_KEY",
		"test-secret-encryption-key-32-bytes",
	)
	t.Setenv("AUTOSTREAM_PUBLIC_URL", "https://panel.example.com")

	auth := store.NewMemoryAuthStore()
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
			Port:        443,
			SSLEnabled:  true,
			PublicURL:   "https://worker.example.com",
			Version:     "v1.0.0",
		},
	)
	agentToken := registerPullSystemUpdateAgentForOwnershipTest(
		t,
		auth,
		"host-agent-a",
		"host-a",
		0,
		map[string]any{
			"host_agent":             true,
			"observe_only":           true,
			"agent_protocol_version": 2,
		},
	)
	const configureToken = "ast_cfg_host_agent_policy_binding"
	if _, err := auth.SetServiceConfigureToken(
		t.Context(),
		"host-agent-a",
		security.HashToken(configureToken),
		time.Now().UTC().Add(time.Hour),
	); err != nil {
		t.Fatal(err)
	}

	updates := store.NewMemorySystemUpdateStore()
	policies := store.NewMemoryUpdaterPolicyStore()
	placeholderDigest := "sha256:" + strings.Repeat("a", 64)
	saved, err := policies.SavePullUpdaterPolicy(
		t.Context(),
		updates,
		"host-agent-a",
		0,
		0,
		store.UpdaterPolicy{
			TransportMode:             store.SystemUpdateTransportPullV2,
			ExecutionHostID:           "host-a",
			LocalExecutorPolicySHA256: placeholderDigest,
			PollIntervalSeconds:       15,
			HeartbeatIntervalSeconds:  30,
			Targets: []store.UpdaterPolicyTarget{{
				TargetID:        worker.ServiceID,
				ServiceID:       worker.ServiceID,
				HostID:          "host-a",
				ServiceType:     worker.ServiceType,
				DeploymentMode:  updateagent.ModeSystemd,
				LocalListenPort: 18081,
			}},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	ownershipBeforeActivation, err := updates.GetSystemUpdateExecutionHost(
		t.Context(),
		"host-a",
	)
	if err != nil {
		t.Fatal(err)
	}
	failingServices := &failOnceServiceActivationStore{
		ServiceRegistryStore:   auth,
		stageFailuresRemaining: 1,
		stageFailure:           store.ErrConflict,
		failuresRemaining:      1,
	}
	handler := NewServer(
		store.NewMemoryStreamStore(),
		WithAuthStore(auth),
		WithServiceRegistryStore(failingServices),
		WithUpdaterPolicyStore(policies),
		WithSystemUpdateStore(updates),
	)

	stagePayload, err := json.Marshal(map[string]any{
		"nodeId":          "host-agent-a",
		"configureToken":  configureToken,
		"protocolVersion": updateagent.HostAgentConfigureProtocolVersion,
		"agentUid":        uint32(1001),
		"agentGid":        uint32(1002),
	})
	if err != nil {
		t.Fatal(err)
	}
	concurrentStageRequest := httptest.NewRequest(
		http.MethodPost,
		"/api/node-agent/configure/stage",
		bytes.NewReader(stagePayload),
	)
	concurrentStageResponse := httptest.NewRecorder()
	handler.ServeHTTP(concurrentStageResponse, concurrentStageRequest)
	if concurrentStageResponse.Code != http.StatusConflict ||
		!strings.Contains(
			concurrentStageResponse.Body.String(),
			`"code":"runtime_token_rotation_conflict"`,
		) {
		t.Fatalf(
			"concurrent stage status=%d body=%s",
			concurrentStageResponse.Code,
			concurrentStageResponse.Body.String(),
		)
	}
	afterConcurrentStage, err := auth.GetService(t.Context(), "host-agent-a")
	if err != nil {
		t.Fatal(err)
	}
	if afterConcurrentStage.ConfigureTokenUsedAt != nil ||
		afterConcurrentStage.StagedNodeTokenID != "" {
		t.Fatalf(
			"concurrent stage response changed identity: %s",
			formatSafeHTTPSensitiveDiagnostic(afterConcurrentStage),
		)
	}
	stageRequest := httptest.NewRequest(
		http.MethodPost,
		"/api/node-agent/configure/stage",
		bytes.NewReader(stagePayload),
	)
	stageResponse := httptest.NewRecorder()
	handler.ServeHTTP(stageResponse, stageRequest)
	if stageResponse.Code != http.StatusOK {
		t.Fatalf(
			"stage status=%d body=%s",
			stageResponse.Code,
			stageResponse.Body.String(),
		)
	}
	if stageResponse.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("stage Cache-Control=%q", stageResponse.Header().Get("Cache-Control"))
	}
	var staged updateagent.UpdaterStagedConfiguration
	if err := json.NewDecoder(stageResponse.Body).Decode(&staged); err != nil {
		t.Fatal(err)
	}
	if staged.ConfigureProtocol != updateagent.HostAgentConfigureProtocolVersion ||
		staged.LocalExecutorPolicy == nil ||
		staged.Config.TransportMode != store.SystemUpdateTransportPullV2 ||
		staged.Config.API != (updateagent.APIConfig{}) {
		t.Fatalf("staged configuration = %s", formatSafeHTTPSensitiveDiagnostic(staged))
	}
	var stagedPolicy updateagent.LocalExecutorPolicy
	if err := json.Unmarshal(
		staged.LocalExecutorPolicy.Policy,
		&stagedPolicy,
	); err != nil {
		t.Fatal(err)
	}
	if stagedPolicy.AgentUID != 1001 ||
		stagedPolicy.AgentGID != 1002 ||
		stagedPolicy.SourcePolicyRevision != saved.Revision ||
		stagedPolicy.ProjectionRevision != saved.ProjectionRevision ||
		stagedPolicy.PolicyRevision != saved.LocalExecutorPolicyRevision ||
		len(stagedPolicy.Targets) != 1 ||
		stagedPolicy.Targets[0].LocalListen != (updateagent.LocalExecutorEndpoint{
			Host: "127.0.0.1",
			Port: 18081,
		}) {
		t.Fatalf("staged canonical policy = %#v", stagedPolicy)
	}
	if worker.AppliedEndpoint == nil ||
		worker.AppliedEndpoint.Host != "worker.example.com" ||
		worker.AppliedEndpoint.Port != 443 ||
		!worker.AppliedEndpoint.SSLEnabled ||
		worker.AppliedEndpoint.PublicURL != "https://worker.example.com" {
		t.Fatalf("public endpoint was not preserved: %#v", worker.AppliedEndpoint)
	}
	stagedService, err := auth.GetService(t.Context(), "host-agent-a")
	if err != nil {
		t.Fatal(err)
	}
	if staged.ConfigurationID == stagedService.StagedNodeTokenID ||
		!strings.HasPrefix(staged.ConfigurationID, "hac1-") ||
		len(staged.ConfigurationID) != len("hac1-")+sha256.Size*2 {
		t.Fatalf(
			"external configuration ID is not stage-bound: external=%q internal=%q",
			staged.ConfigurationID,
			stagedService.StagedNodeTokenID,
		)
	}
	if stagedService.TokenID != agentToken.ID ||
		stagedService.ConfigureTokenUsedAt == nil {
		t.Fatalf("stage changed the active identity: %s", formatSafeHTTPSensitiveDiagnostic(stagedService))
	}
	if _, err := auth.AuthenticateServiceToken(
		t.Context(),
		agentToken.RawToken,
		"service.heartbeat",
	); err != nil {
		t.Fatalf("old runtime token stopped before activation: %v", err)
	}

	alteredProjection, err := updateagent.BuildHostAgentConfigurePolicy(
		updateagent.HostAgentConfigurePolicySource{
			PanelURL:                    "https://panel.example.com",
			ExecutionHostID:             "host-a",
			AgentUID:                    2001,
			AgentGID:                    1002,
			SourcePolicyRevision:        saved.Revision,
			ProjectionRevision:          saved.ProjectionRevision,
			LocalExecutorPolicyRevision: saved.LocalExecutorPolicyRevision,
			Targets: []updateagent.HostAgentConfigurePolicyTarget{{
				ServiceID:             worker.ServiceID,
				ServiceType:           worker.ServiceType,
				DeploymentMode:        updateagent.ModeSystemd,
				EndpointRevision:      worker.EndpointRevision,
				AppliedConfigRevision: worker.AppliedConfigRevision,
				AppliedConfigSHA256:   worker.AppliedConfigSHA256,
				AppliedEndpointPort:   worker.AppliedEndpoint.Port,
				LocalListenPort:       18081,
			}},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	tampered := hostAgentConfigureActivationPayload(
		staged,
		2001,
		1002,
		alteredProjection,
	)
	tamperedResponse := sendHostAgentConfigureActivation(
		t,
		handler,
		tampered,
	)
	if tamperedResponse.Code != http.StatusConflict ||
		!strings.Contains(
			tamperedResponse.Body.String(),
			`"code":"local_executor_policy_binding_mismatch"`,
		) {
		t.Fatalf(
			"tampered activation status=%d body=%s",
			tamperedResponse.Code,
			tamperedResponse.Body.String(),
		)
	}
	assertHostAgentConfigureNotActivated(
		t,
		auth,
		policies,
		agentToken,
		placeholderDigest,
		stagedService.StagedNodeTokenID,
	)

	activation := hostAgentConfigureActivationPayload(
		staged,
		stagedPolicy.AgentUID,
		stagedPolicy.AgentGID,
		*staged.LocalExecutorPolicy,
	)
	activationResponse := sendHostAgentConfigureActivation(
		t,
		handler,
		activation,
	)
	if activationResponse.Code != http.StatusInternalServerError ||
		!strings.Contains(
			activationResponse.Body.String(),
			`"code":"activate_node_configuration_failed"`,
		) {
		t.Fatalf(
			"injected activation status=%d body=%s",
			activationResponse.Code,
			activationResponse.Body.String(),
		)
	}
	assertHostAgentConfigureNotActivated(
		t,
		auth,
		policies,
		agentToken,
		staged.LocalExecutorPolicy.SHA256,
		stagedService.StagedNodeTokenID,
	)
	if _, err := auth.AuthenticateServiceToken(
		t.Context(),
		staged.Config.RuntimeToken,
		"service.heartbeat",
	); !errors.Is(err, store.ErrUnauthorized) {
		t.Fatalf("staged runtime token became active after failed activation: %v", err)
	}
	failingServices.failuresRemaining = 1
	failingServices.failure = store.ErrSystemUpdateRuntimeTokenRotationSharedToken
	sharedTokenResponse := sendHostAgentConfigureActivation(
		t,
		handler,
		activation,
	)
	if sharedTokenResponse.Code != http.StatusConflict ||
		!strings.Contains(
			sharedTokenResponse.Body.String(),
			`"code":"runtime_token_rotation_shared_token"`,
		) {
		t.Fatalf(
			"shared-token activation status=%d body=%s",
			sharedTokenResponse.Code,
			sharedTokenResponse.Body.String(),
		)
	}
	failingServices.failuresRemaining = 1
	failingServices.failure = store.ErrConflict
	concurrentChangeResponse := sendHostAgentConfigureActivation(
		t,
		handler,
		activation,
	)
	if concurrentChangeResponse.Code != http.StatusConflict ||
		!strings.Contains(
			concurrentChangeResponse.Body.String(),
			`"code":"runtime_token_rotation_conflict"`,
		) {
		t.Fatalf(
			"concurrent activation status=%d body=%s",
			concurrentChangeResponse.Code,
			concurrentChangeResponse.Body.String(),
		)
	}
	failingServices.failure = nil
	ownershipAfterFailure, err := updates.GetSystemUpdateExecutionHost(
		t.Context(),
		"host-a",
	)
	if err != nil {
		t.Fatal(err)
	}
	if ownershipAfterFailure != ownershipBeforeActivation {
		t.Fatalf(
			"failed configure activation changed mutation ownership: before=%#v after=%#v",
			ownershipBeforeActivation,
			ownershipAfterFailure,
		)
	}

	for name, mutate := range map[string]func(map[string]any){
		"uid": func(payload map[string]any) {
			payload["agentUid"] = uint32(2001)
		},
		"gid": func(payload map[string]any) {
			payload["agentGid"] = uint32(2002)
		},
		"digest": func(payload map[string]any) {
			payload["localExecutorPolicySha256"] =
				"sha256:" + strings.Repeat("c", 64)
		},
	} {
		t.Run("failed_activation_rejects_changed_"+name, func(t *testing.T) {
			changed := cloneHostAgentConfigureActivationPayload(activation)
			mutate(changed)
			response := sendHostAgentConfigureActivation(t, handler, changed)
			if response.Code != http.StatusConflict ||
				!strings.Contains(
					response.Body.String(),
					`"code":"local_executor_policy_binding_mismatch"`,
				) {
				t.Fatalf(
					"changed retry status=%d body=%s",
					response.Code,
					response.Body.String(),
				)
			}
		})
	}
	assertHostAgentConfigureNotActivated(
		t,
		auth,
		policies,
		agentToken,
		staged.LocalExecutorPolicy.SHA256,
		stagedService.StagedNodeTokenID,
	)

	activationResponse = sendHostAgentConfigureActivation(
		t,
		handler,
		activation,
	)
	if activationResponse.Code != http.StatusOK {
		t.Fatalf(
			"activation retry status=%d body=%s",
			activationResponse.Code,
			activationResponse.Body.String(),
		)
	}
	var result updateagent.UpdaterActivationResult
	if err := json.NewDecoder(activationResponse.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	if result.State != "activated" ||
		result.ConfigurationID != staged.ConfigurationID ||
		result.ConfigureProtocol != updateagent.HostAgentConfigureProtocolVersion ||
		result.LocalExecutorPolicySHA256 != staged.LocalExecutorPolicy.SHA256 ||
		result.SourcePolicyRevision != staged.LocalExecutorPolicy.SourcePolicyRevision ||
		result.ProjectionRevision != staged.LocalExecutorPolicy.ProjectionRevision ||
		result.LocalExecutorPolicyRevision != staged.LocalExecutorPolicy.PolicyRevision {
		t.Fatalf("activation result = %#v", result)
	}
	activatedService, err := auth.GetService(t.Context(), "host-agent-a")
	if err != nil {
		t.Fatal(err)
	}
	if activatedService.TokenID != stagedService.StagedNodeTokenID {
		t.Fatalf("active runtime token did not switch: %s", formatSafeHTTPSensitiveDiagnostic(activatedService))
	}
	if _, err := auth.AuthenticateServiceToken(
		t.Context(),
		staged.Config.RuntimeToken,
		"service.heartbeat",
	); err != nil {
		t.Fatalf("staged runtime token is not active: %v", err)
	}
	if _, err := auth.AuthenticateServiceToken(
		t.Context(),
		agentToken.RawToken,
		"service.heartbeat",
	); !errors.Is(err, store.ErrUnauthorized) {
		t.Fatalf("previous runtime token remained active: %v", err)
	}
	boundPolicy, err := policies.GetUpdaterPolicy(
		t.Context(),
		"host-agent-a",
	)
	if err != nil {
		t.Fatal(err)
	}
	if boundPolicy.LocalExecutorPolicySHA256 != staged.LocalExecutorPolicy.SHA256 ||
		boundPolicy.Revision != saved.Revision ||
		boundPolicy.ProjectionRevision != saved.ProjectionRevision ||
		boundPolicy.LocalExecutorPolicyRevision != saved.LocalExecutorPolicyRevision {
		t.Fatalf("activated policy binding = %#v", boundPolicy)
	}
	wrongReplay := cloneHostAgentConfigureActivationPayload(activation)
	wrongReplay["activationToken"] = "wrong-activation-token"
	wrongReplayResponse := sendHostAgentConfigureActivation(t, handler, wrongReplay)
	if wrongReplayResponse.Code != http.StatusUnauthorized ||
		!strings.Contains(wrongReplayResponse.Body.String(), `"code":"invalid_activation_token"`) {
		t.Fatalf(
			"wrong activation replay status=%d body=%s",
			wrongReplayResponse.Code,
			wrongReplayResponse.Body.String(),
		)
	}
	tamperedReplay := cloneHostAgentConfigureActivationPayload(activation)
	tamperedReplay["agentUid"] = uint32(2001)
	tamperedReplayResponse := sendHostAgentConfigureActivation(t, handler, tamperedReplay)
	if tamperedReplayResponse.Code != http.StatusConflict ||
		!strings.Contains(
			tamperedReplayResponse.Body.String(),
			`"code":"local_executor_policy_binding_mismatch"`,
		) {
		t.Fatalf(
			"tampered activation replay status=%d body=%s",
			tamperedReplayResponse.Code,
			tamperedReplayResponse.Body.String(),
		)
	}

	replayResponse := sendHostAgentConfigureActivation(
		t,
		handler,
		activation,
	)
	if replayResponse.Code != http.StatusOK ||
		!strings.Contains(replayResponse.Body.String(), `"state":"already_activated"`) ||
		!strings.Contains(
			replayResponse.Body.String(),
			`"local_executor_policy_sha256":"`+
				staged.LocalExecutorPolicy.SHA256+`"`,
		) {
		t.Fatalf(
			"activation replay status=%d body=%s",
			replayResponse.Code,
			replayResponse.Body.String(),
		)
	}
}

func hostAgentConfigureActivationPayload(
	staged updateagent.UpdaterStagedConfiguration,
	agentUID, agentGID uint32,
	projection updateagent.ConfigurePolicyProjection,
) map[string]any {
	return map[string]any{
		"nodeId":                      staged.Config.NodeID,
		"configurationId":             staged.ConfigurationID,
		"activationToken":             staged.ActivationToken,
		"version":                     "v1.0.1",
		"commit":                      "abc123",
		"build_date":                  "2026-07-28",
		"hostname":                    "host-a",
		"os":                          "linux",
		"arch":                        "amd64",
		"configureProtocolVersion":    updateagent.HostAgentConfigureProtocolVersion,
		"agentUid":                    agentUID,
		"agentGid":                    agentGID,
		"localExecutorPolicySha256":   projection.SHA256,
		"sourcePolicyRevision":        projection.SourcePolicyRevision,
		"projectionRevision":          projection.ProjectionRevision,
		"localExecutorPolicyRevision": projection.PolicyRevision,
	}
}

func cloneHostAgentConfigureActivationPayload(
	payload map[string]any,
) map[string]any {
	cloned := make(map[string]any, len(payload))
	for key, value := range payload {
		cloned[key] = value
	}
	return cloned
}

func sendHostAgentConfigureActivation(
	t *testing.T,
	handler http.Handler,
	payload map[string]any,
) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(
		http.MethodPost,
		"/api/node-agent/configure/activate",
		bytes.NewReader(body),
	)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func assertHostAgentConfigureNotActivated(
	t *testing.T,
	auth *store.MemoryAuthStore,
	policies *store.MemoryUpdaterPolicyStore,
	oldToken store.ServiceToken,
	wantPolicySHA256, stagedTokenID string,
) {
	t.Helper()
	service, err := auth.GetService(t.Context(), "host-agent-a")
	if err != nil {
		t.Fatal(err)
	}
	if service.TokenID != oldToken.ID ||
		service.StagedNodeTokenID != stagedTokenID {
		t.Fatalf("rejected activation changed identity: %s", formatSafeHTTPSensitiveDiagnostic(service))
	}
	if _, err := auth.AuthenticateServiceToken(
		t.Context(),
		oldToken.RawToken,
		"service.heartbeat",
	); err != nil {
		t.Fatalf("rejected activation invalidated old token: %v", err)
	}
	policy, err := policies.GetUpdaterPolicy(t.Context(), "host-agent-a")
	if err != nil {
		t.Fatal(err)
	}
	if policy.LocalExecutorPolicySHA256 != wantPolicySHA256 {
		t.Fatalf("rejected activation changed policy: %#v", policy)
	}
}

type failOnceServiceActivationStore struct {
	store.ServiceRegistryStore
	stageFailuresRemaining int
	stageFailure           error
	failuresRemaining      int
	failure                error
}

func (s *failOnceServiceActivationStore) StageServiceNodeConfiguration(
	ctx context.Context,
	serviceID, rawConfigureToken string,
	now time.Time,
	seal store.NodeTokenSealer,
) (store.StagedServiceNodeConfiguration, error) {
	if s.stageFailuresRemaining > 0 {
		s.stageFailuresRemaining--
		failure := s.stageFailure
		if failure == nil {
			failure = errors.New("injected service stage failure")
		}
		return store.StagedServiceNodeConfiguration{}, failure
	}
	return s.ServiceRegistryStore.StageServiceNodeConfiguration(
		ctx,
		serviceID,
		rawConfigureToken,
		now,
		seal,
	)
}

func (s *failOnceServiceActivationStore) ActivateServiceNodeConfiguration(
	ctx context.Context,
	serviceID, configurationID, rawActivationToken string,
	now time.Time,
	report store.ServiceRuntimeReport,
) (store.ServiceToken, store.RegisteredService, bool, error) {
	if s.failuresRemaining > 0 {
		s.failuresRemaining--
		failure := s.failure
		if failure == nil {
			failure = errors.New("injected service activation failure")
		}
		return store.ServiceToken{},
			store.RegisteredService{},
			false,
			failure
	}
	return s.ServiceRegistryStore.ActivateServiceNodeConfiguration(
		ctx,
		serviceID,
		configurationID,
		rawActivationToken,
		now,
		report,
	)
}
