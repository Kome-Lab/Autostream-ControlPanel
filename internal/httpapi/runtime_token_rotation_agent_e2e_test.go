package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/example/autostream-control-panel/internal/updateagent"
)

func TestHostPullAgentRuntimeTokenRotationOrdersHeartbeatProofThroughHTTPStore(
	t *testing.T,
) {
	fixture := newRuntimeTokenRotationHTTPFixture(t)
	staged := fixture.stage(t, "real-host-agent-ordering")
	gate := newRuntimeTokenRotationOrderGate(fixture.handler)
	server := httptest.NewServer(gate)
	defer server.Close()

	identity := decodeManagedHostAgentIdentity(
		t,
		server.URL,
		fixture.oldToken.RawToken,
	)
	executor := &httpBackedRuntimeCredentialExecutor{
		active:       identity,
		httpClient:   server.Client(),
		policySHA256: fixture.policy.LocalExecutorPolicySHA256,
	}
	claims := &httpE2ERuntimeTokenClaimStore{}
	agent, err := updateagent.NewHostPullAgent(
		identity,
		updateagent.HostPullAgentOptions{
			StateDir:                  t.TempDir(),
			HTTPClient:                server.Client(),
			PollInterval:              5 * time.Millisecond,
			HeartbeatInterval:         5 * time.Millisecond,
			RuntimeCredentialExecutor: executor,
			RuntimeTokenClaimState:    claims,
			LoadRuntimeIdentity:       executor.loadIdentity,
			NewRuntimeTokenClaimID: func() (string, error) {
				return "99999999-9999-4999-8999-999999999999", nil
			},
			AgentVersion: "v2.0.0",
			ObserveTargets: func(
				_ context.Context,
				policy updateagent.HostAgentPolicy,
			) ([]updateagent.HostTargetObservation, error) {
				observations := make(
					[]updateagent.HostTargetObservation,
					0,
					len(policy.Targets),
				)
				for _, target := range policy.Targets {
					observations = append(
						observations,
						updateagent.HostTargetObservation{
							ServiceID:        target.ServiceID,
							Availability:     updateagent.TargetAvailabilityAvailable,
							AvailabilityCode: "executor_verified",
							PolicyRevision:   policy.LocalExecutorPolicyRevision,
							PolicySHA256:     policy.LocalExecutorPolicySHA256,
							ConfigRevision:   target.AppliedConfigRevision,
							ConfigSHA256:     target.AppliedConfigSHA256,
						},
					)
				}
				return observations, nil
			},
			Logf: func(string, ...any) {},
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- agent.Run(ctx)
	}()
	select {
	case <-gate.blockedHeartbeat:
	case <-time.After(5 * time.Second):
		cancel()
		t.Fatal("Host Agent never reached a local-staged heartbeat")
	}
	if calls := gate.proofCalls.Load(); calls != 0 {
		cancel()
		t.Fatalf(
			"heartbeat proof reached Store before a successful heartbeat: %d",
			calls,
		)
	}
	if gate.prematureProof.Load() {
		cancel()
		t.Fatal("heartbeat proof was attempted before Store persisted heartbeat proof")
	}
	blockedCapabilities := gate.lastBlockedCapabilities()
	if blockedCapabilities["mutation_enabled"] != true ||
		blockedCapabilities["update_executor"] != true {
		cancel()
		t.Fatalf(
			"base mutation capability disappeared during rotation: %#v",
			blockedCapabilities,
		)
	}
	if blockedCapabilities["runtime_token_rotation_phase"] !=
		updateagent.RuntimeCredentialPhaseLocalStaged {
		cancel()
		t.Fatalf(
			"local stage proof was absent from heartbeat: %#v",
			blockedCapabilities,
		)
	}

	gate.allowHeartbeat.Store(true)
	select {
	case <-gate.activated:
	case <-time.After(5 * time.Second):
		cancel()
		t.Fatal("Host Agent did not complete proof and activation")
	}
	identityDeadline := time.Now().Add(2 * time.Second)
	for executor.activeIdentity().RuntimeToken == fixture.oldToken.RawToken &&
		time.Now().Before(identityDeadline) {
		time.Sleep(time.Millisecond)
	}
	cancel()
	if err := <-done; err != nil &&
		!errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if gate.prematureProof.Load() ||
		gate.proofCalls.Load() != 1 ||
		!gate.successfulRotationHeartbeat.Load() {
		t.Fatalf(
			"ordering proof heartbeat=%t premature=%t proof_calls=%d",
			gate.successfulRotationHeartbeat.Load(),
			gate.prematureProof.Load(),
			gate.proofCalls.Load(),
		)
	}

	active := fixture.adminRequest(
		t,
		http.MethodGet,
		"/system-updates/updaters/host-agent-a/runtime-token-rotations/active",
		"",
	)
	if active.Code != http.StatusNoContent {
		t.Fatalf(
			"Store retained terminal rotation status=%d body=%s",
			active.Code,
			active.Body.String(),
		)
	}
	newIdentity := executor.activeIdentity()
	if newIdentity.RuntimeToken == fixture.oldToken.RawToken ||
		!strings.HasPrefix(newIdentity.RuntimeToken, "ast_svc_") {
		t.Fatal("Local Executor did not switch to the claimed staged identity")
	}
	oldPolicy := fixture.serviceRequest(
		t,
		fixture.oldToken.RawToken,
		"/services/host-agent/policy",
		`{"service_id":"host-agent-a","current_revision":0}`,
	)
	if oldPolicy.Code != http.StatusUnauthorized {
		t.Fatalf(
			"old token remained valid status=%d body=%s",
			oldPolicy.Code,
			oldPolicy.Body.String(),
		)
	}
	newPolicy := fixture.serviceRequest(
		t,
		newIdentity.RuntimeToken,
		"/services/host-agent/policy",
		`{"service_id":"host-agent-a","current_revision":0}`,
	)
	if newPolicy.Code != http.StatusOK {
		t.Fatalf(
			"new token was not active status=%d body=%s",
			newPolicy.Code,
			newPolicy.Body.String(),
		)
	}
	if _, exists, err := claims.Load(); err != nil || exists {
		t.Fatalf("terminal rotation retained claim state: %v", err)
	}
	if staged.ID == "" {
		t.Fatal("fixture did not stage a rotation")
	}
}

func TestEmergencyTerminalReconfiguresSamePullNodeWithoutPolicyDrift(
	t *testing.T,
) {
	t.Setenv("AUTOSTREAM_PUBLIC_URL", "https://panel.example.com")
	fixture := newRuntimeTokenRotationHTTPFixture(t)
	initial := configureEmergencyRuntimeTokenAgent(
		t,
		fixture,
		1001,
		1002,
	)
	before, err := fixture.policies.GetUpdaterPolicy(
		t.Context(),
		"host-agent-a",
	)
	if err != nil {
		t.Fatal(err)
	}
	if initial.Config.NodeID != "host-agent-a" ||
		initial.Config.TransportMode !=
			updateagent.HostTransportPullV2 ||
		initial.LocalExecutorPolicy == nil ||
		initial.LocalExecutorPolicy.SHA256 !=
			before.LocalExecutorPolicySHA256 {
		t.Fatalf("initial configured identity=%#v policy=%#v", initial, before)
	}
	// Starting a fresh Configure Token lane clears the completed activation
	// replay fields. The emergency terminal revokes this outstanding token
	// together with both runtime credential slots.
	preEmergencyConfigure := fixture.adminRequest(
		t,
		http.MethodPost,
		"/nodes/host-agent-a/configure-token",
		"",
	)
	if preEmergencyConfigure.Code != http.StatusCreated {
		t.Fatalf(
			"pre-emergency configure token status=%d body=%s",
			preEmergencyConfigure.Code,
			preEmergencyConfigure.Body.String(),
		)
	}

	staged := fixture.stage(t, "emergency-configure-cross-flow")
	const claimID = "77777777-7777-4777-8777-777777777777"
	claim := fixture.serviceRequest(
		t,
		initial.Config.RuntimeToken,
		"/services/host-agent/runtime-token-rotations/"+
			staged.ID+"/credential/claim",
		`{"expected_revision":1,"claim_id":"`+claimID+`"}`,
	)
	if claim.Code != http.StatusOK {
		t.Fatalf("claim status=%d body=%s", claim.Code, claim.Body.String())
	}
	var claimed runtimeTokenRotationClaimResponse
	if err := json.NewDecoder(claim.Body).Decode(&claimed); err != nil {
		t.Fatal(err)
	}
	emergency := fixture.adminRequest(
		t,
		http.MethodPost,
		"/system-updates/updaters/host-agent-a/runtime-token-rotations/"+
			staged.ID+"/emergency-revoke",
		`{"expected_revision":2,"token_slot":"previous"}`,
	)
	if emergency.Code != http.StatusOK {
		t.Fatalf(
			"emergency status=%d body=%s",
			emergency.Code,
			emergency.Body.String(),
		)
	}
	for name, token := range map[string]string{
		"previous": initial.Config.RuntimeToken,
		"staged":   claimed.Credential.RuntimeToken,
	} {
		response := fixture.serviceRequest(
			t,
			token,
			"/services/host-agent/policy",
			`{"service_id":"host-agent-a","current_revision":0}`,
		)
		if response.Code != http.StatusUnauthorized {
			t.Fatalf(
				"%s token survived emergency status=%d body=%s",
				name,
				response.Code,
				response.Body.String(),
			)
		}
	}

	// Simulate a lost stage response or a failed local identity install. The
	// operator must be able to issue a fresh Configure Token; doing so
	// invalidates the abandoned staged secret before retrying.
	abandoned := stageEmergencyRuntimeTokenAgent(
		t,
		fixture,
		1001,
		1002,
	)
	replacement := configureEmergencyRuntimeTokenAgent(
		t,
		fixture,
		1001,
		1002,
	)
	if replacement.Config.NodeID != initial.Config.NodeID ||
		replacement.Config.TransportMode !=
			updateagent.HostTransportPullV2 ||
		replacement.Config.RuntimeToken ==
			initial.Config.RuntimeToken ||
		replacement.Config.RuntimeToken ==
			claimed.Credential.RuntimeToken ||
		replacement.LocalExecutorPolicy == nil {
		t.Fatalf("replacement configured identity=%#v", replacement)
	}
	if replacement.LocalExecutorPolicy.SHA256 !=
		before.LocalExecutorPolicySHA256 ||
		replacement.LocalExecutorPolicy.SourcePolicyRevision !=
			before.Revision ||
		replacement.LocalExecutorPolicy.ProjectionRevision !=
			before.ProjectionRevision ||
		replacement.LocalExecutorPolicy.PolicyRevision !=
			before.LocalExecutorPolicyRevision {
		t.Fatalf(
			"replacement policy drifted: staged=%#v before=%#v",
			replacement.LocalExecutorPolicy,
			before,
		)
	}
	after, err := fixture.policies.GetUpdaterPolicy(
		t.Context(),
		"host-agent-a",
	)
	if err != nil {
		t.Fatal(err)
	}
	if after.TransportMode != before.TransportMode ||
		after.ExecutionHostID != before.ExecutionHostID ||
		after.Revision != before.Revision ||
		after.ProjectionRevision != before.ProjectionRevision ||
		after.LocalExecutorPolicyRevision !=
			before.LocalExecutorPolicyRevision ||
		after.LocalExecutorPolicySHA256 !=
			before.LocalExecutorPolicySHA256 {
		t.Fatalf(
			"configure activation changed policy: before=%#v after=%#v",
			before,
			after,
		)
	}
	abandonedPolicy := fixture.serviceRequest(
		t,
		abandoned.Config.RuntimeToken,
		"/services/host-agent/policy",
		`{"service_id":"host-agent-a","current_revision":0}`,
	)
	if abandonedPolicy.Code != http.StatusUnauthorized {
		t.Fatalf(
			"abandoned Configure stage token became active status=%d body=%s",
			abandonedPolicy.Code,
			abandonedPolicy.Body.String(),
		)
	}
	newPolicy := fixture.serviceRequest(
		t,
		replacement.Config.RuntimeToken,
		"/services/host-agent/policy",
		`{"service_id":"host-agent-a","current_revision":0}`,
	)
	if newPolicy.Code != http.StatusOK {
		t.Fatalf(
			"replacement token poll status=%d body=%s",
			newPolicy.Code,
			newPolicy.Body.String(),
		)
	}
}

func configureEmergencyRuntimeTokenAgent(
	t *testing.T,
	fixture runtimeTokenRotationHTTPFixture,
	agentUID uint32,
	agentGID uint32,
) updateagent.UpdaterStagedConfiguration {
	t.Helper()
	staged := stageEmergencyRuntimeTokenAgent(
		t,
		fixture,
		agentUID,
		agentGID,
	)
	activation := hostAgentConfigureActivationPayload(
		staged,
		agentUID,
		agentGID,
		*staged.LocalExecutorPolicy,
	)
	activated := sendHostAgentConfigureActivation(
		t,
		fixture.handler,
		activation,
	)
	if activated.Code != http.StatusOK {
		t.Fatalf(
			"configure activate status=%d body=%s",
			activated.Code,
			activated.Body.String(),
		)
	}
	return staged
}

func stageEmergencyRuntimeTokenAgent(
	t *testing.T,
	fixture runtimeTokenRotationHTTPFixture,
	agentUID uint32,
	agentGID uint32,
) updateagent.UpdaterStagedConfiguration {
	t.Helper()
	issued := fixture.adminRequest(
		t,
		http.MethodPost,
		"/nodes/host-agent-a/configure-token",
		"",
	)
	if issued.Code != http.StatusCreated {
		t.Fatalf(
			"configure token status=%d body=%s",
			issued.Code,
			issued.Body.String(),
		)
	}
	var token struct {
		ConfigureToken string `json:"configure_token"`
	}
	if err := json.NewDecoder(issued.Body).Decode(&token); err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(map[string]any{
		"nodeId":          "host-agent-a",
		"configureToken":  token.ConfigureToken,
		"protocolVersion": updateagent.HostAgentConfigureProtocolVersion,
		"agentUid":        agentUID,
		"agentGid":        agentGID,
	})
	if err != nil {
		t.Fatal(err)
	}
	stage := fixture.publicRequest(
		t,
		http.MethodPost,
		"/api/node-agent/configure/stage",
		string(payload),
	)
	if stage.Code != http.StatusOK {
		t.Fatalf(
			"configure stage status=%d body=%s",
			stage.Code,
			stage.Body.String(),
		)
	}
	var staged updateagent.UpdaterStagedConfiguration
	if err := json.NewDecoder(stage.Body).Decode(&staged); err != nil {
		t.Fatal(err)
	}
	if staged.LocalExecutorPolicy == nil {
		t.Fatal("configure stage omitted Local Executor policy")
	}
	return staged
}

type runtimeTokenRotationOrderGate struct {
	next                        http.Handler
	localStaged                 atomic.Bool
	allowHeartbeat              atomic.Bool
	successfulRotationHeartbeat atomic.Bool
	prematureProof              atomic.Bool
	proofCalls                  atomic.Int32
	blockedHeartbeat            chan struct{}
	activated                   chan struct{}
	mu                          sync.Mutex
	blockedCapabilities         map[string]any
}

func newRuntimeTokenRotationOrderGate(
	next http.Handler,
) *runtimeTokenRotationOrderGate {
	return &runtimeTokenRotationOrderGate{
		next:             next,
		blockedHeartbeat: make(chan struct{}, 1),
		activated:        make(chan struct{}, 1),
	}
}

func (g *runtimeTokenRotationOrderGate) ServeHTTP(
	w http.ResponseWriter,
	r *http.Request,
) {
	if r.URL.Path == "/services/heartbeat" &&
		g.localStaged.Load() &&
		!g.allowHeartbeat.Load() {
		data, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		r.Body = io.NopCloser(bytes.NewReader(data))
		var body struct {
			Capabilities map[string]any `json:"capabilities"`
		}
		_ = json.Unmarshal(data, &body)
		g.mu.Lock()
		g.blockedCapabilities = body.Capabilities
		g.mu.Unlock()
		select {
		case g.blockedHeartbeat <- struct{}{}:
		default:
		}
		http.Error(w, "heartbeat intentionally blocked", http.StatusServiceUnavailable)
		return
	}
	isProof := strings.HasSuffix(r.URL.Path, "/heartbeat-proof")
	if isProof {
		g.proofCalls.Add(1)
		if !g.successfulRotationHeartbeat.Load() {
			g.prematureProof.Store(true)
		}
	}
	recorder := &runtimeTokenRotationStatusWriter{ResponseWriter: w}
	g.next.ServeHTTP(recorder, r)
	switch {
	case strings.HasSuffix(r.URL.Path, "/local-staged") &&
		recorder.statusCode() >= 200 &&
		recorder.statusCode() < 300:
		g.localStaged.Store(true)
	case r.URL.Path == "/services/heartbeat" &&
		g.localStaged.Load() &&
		recorder.statusCode() >= 200 &&
		recorder.statusCode() < 300:
		g.successfulRotationHeartbeat.Store(true)
	case strings.HasSuffix(r.URL.Path, "/activate") &&
		recorder.statusCode() >= 200 &&
		recorder.statusCode() < 300:
		select {
		case g.activated <- struct{}{}:
		default:
		}
	}
}

func (g *runtimeTokenRotationOrderGate) lastBlockedCapabilities() map[string]any {
	g.mu.Lock()
	defer g.mu.Unlock()
	copy := make(map[string]any, len(g.blockedCapabilities))
	for key, value := range g.blockedCapabilities {
		copy[key] = value
	}
	return copy
}

type runtimeTokenRotationStatusWriter struct {
	http.ResponseWriter
	status int
}

func (w *runtimeTokenRotationStatusWriter) WriteHeader(status int) {
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

func (w *runtimeTokenRotationStatusWriter) Write(data []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(data)
}

func (w *runtimeTokenRotationStatusWriter) statusCode() int {
	if w.status == 0 {
		return http.StatusOK
	}
	return w.status
}

type httpBackedRuntimeCredentialExecutor struct {
	mu           sync.Mutex
	active       updateagent.Config
	staged       updateagent.Config
	status       updateagent.RuntimeCredentialStatus
	exists       bool
	httpClient   *http.Client
	policySHA256 string
}

func (e *httpBackedRuntimeCredentialExecutor) RuntimeCredentialStatus(
	context.Context,
	string,
) (updateagent.RuntimeCredentialStatus, bool, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.status, e.exists, nil
}

func (e *httpBackedRuntimeCredentialExecutor) StageRuntimeCredential(
	ctx context.Context,
	rotation updateagent.HostAgentRuntimeTokenRotation,
	token updateagent.RemoteSecret,
) (updateagent.RuntimeCredentialStatus, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.exists &&
		e.status.Phase !=
			updateagent.RuntimeCredentialPhaseClaimPrepared {
		return e.status, nil
	}
	e.staged = e.active
	e.staged.RuntimeToken = token.Reveal()
	acknowledged, err :=
		updateagent.AcknowledgeRuntimeTokenRotationLocalStage(
			ctx,
			e.active.PanelURL,
			rotation.ID,
			rotation.Revision,
			e.staged.RuntimeToken,
			e.httpClient,
		)
	if err != nil {
		return updateagent.RuntimeCredentialStatus{}, err
	}
	e.status = e.statusFor(
		acknowledged,
		updateagent.RuntimeCredentialPhaseLocalStaged,
	)
	e.exists = true
	return e.status, nil
}

func (e *httpBackedRuntimeCredentialExecutor) PrepareRuntimeCredential(
	_ context.Context,
	rotation updateagent.HostAgentRuntimeTokenRotation,
) (updateagent.RuntimeCredentialStatus, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.exists {
		return e.status, nil
	}
	e.status = e.statusFor(
		rotation,
		updateagent.RuntimeCredentialPhaseClaimPrepared,
	)
	e.exists = true
	return e.status, nil
}

func (e *httpBackedRuntimeCredentialExecutor) MarkRuntimeCredentialProofReady(
	_ context.Context,
	rotation updateagent.HostAgentRuntimeTokenRotation,
) (updateagent.RuntimeCredentialStatus, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if !e.exists ||
		e.status.Phase != updateagent.RuntimeCredentialPhaseLocalStaged {
		return updateagent.RuntimeCredentialStatus{},
			errors.New("local stage is unavailable")
	}
	e.status.Phase = updateagent.RuntimeCredentialPhaseProofReady
	e.status.RotationRevision = rotation.Revision
	return e.status, nil
}

func (e *httpBackedRuntimeCredentialExecutor) ActivateRuntimeCredential(
	ctx context.Context,
	rotation updateagent.HostAgentRuntimeTokenRotation,
) (updateagent.RuntimeCredentialStatus, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if !e.exists ||
		e.status.Phase != updateagent.RuntimeCredentialPhaseProofReady {
		return updateagent.RuntimeCredentialStatus{},
			errors.New("heartbeat proof is unavailable")
	}
	activated, err := updateagent.ActivateRuntimeTokenRotationAtPanel(
		ctx,
		e.active.PanelURL,
		rotation.ID,
		rotation.Revision,
		e.staged.RuntimeToken,
		e.httpClient,
	)
	if err != nil {
		return updateagent.RuntimeCredentialStatus{}, err
	}
	e.active = e.staged
	e.staged = updateagent.Config{}
	e.status = e.statusFor(
		activated,
		updateagent.RuntimeCredentialPhaseActivated,
	)
	return e.status, nil
}

func (*httpBackedRuntimeCredentialExecutor) CancelRuntimeCredential(
	context.Context,
	updateagent.HostAgentRuntimeTokenRotation,
) (updateagent.RuntimeCredentialStatus, error) {
	return updateagent.RuntimeCredentialStatus{},
		errors.New("unexpected cancel")
}

func (e *httpBackedRuntimeCredentialExecutor) FinalizeRuntimeCredential(
	_ context.Context,
	rotation updateagent.HostAgentRuntimeTokenRotation,
) (updateagent.RuntimeCredentialStatus, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if !e.exists || e.status.RotationID != rotation.ID {
		return updateagent.RuntimeCredentialStatus{},
			errors.New("terminal state is unavailable")
	}
	status := e.status
	e.exists = false
	return status, nil
}

func (e *httpBackedRuntimeCredentialExecutor) statusFor(
	rotation updateagent.HostAgentRuntimeTokenRotation,
	phase string,
) updateagent.RuntimeCredentialStatus {
	return updateagent.RuntimeCredentialStatus{
		Phase:                       phase,
		RotationID:                  rotation.ID,
		ServiceID:                   rotation.ServiceID,
		ExecutionHostID:             rotation.ExecutionHostID,
		PreviousTokenID:             rotation.PreviousTokenID,
		StagedTokenID:               rotation.StagedTokenID,
		RotationRevision:            rotation.Revision,
		OwnershipEpoch:              rotation.ExpectedOwnershipEpoch,
		SourcePolicyRevision:        rotation.ExpectedSourcePolicyRevision,
		ProjectionRevision:          rotation.ExpectedProjectionRevision,
		LocalExecutorPolicyRevision: rotation.ExpectedLocalExecutorPolicyRevision,
		StagedIdentitySHA256:        "sha256:" + strings.Repeat("b", 64),
		PreviousIdentitySHA256:      "sha256:" + strings.Repeat("c", 64),
		ActiveIdentitySHA256: func() string {
			if phase == updateagent.RuntimeCredentialPhaseActivated {
				return "sha256:" + strings.Repeat("b", 64)
			}
			return ""
		}(),
		LocalExecutorPolicySHA256: e.policySHA256,
		ExecutorVersion:           "v2.0.0",
		ExecutorProtocolVersion:   updateagent.LocalExecutorMutationProtocolVersion,
		MutationProtocolVersion:   updateagent.LocalExecutorMutationProtocolVersion,
		LocalStageReceiptID:       rotation.LocalStageReceiptID,
		StagedExpiresAt:           time.Now().UTC().Add(24 * time.Hour),
	}
}

func (e *httpBackedRuntimeCredentialExecutor) loadIdentity(
	path string,
	_ bool,
) (updateagent.Config, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	switch path {
	case updateagent.HostAgentIdentityPath:
		return e.active, nil
	case updateagent.HostAgentStagedIdentityPath:
		if !e.staged.IsManagedBootstrap() {
			return updateagent.Config{}, errors.New("staged identity is unavailable")
		}
		return e.staged, nil
	default:
		return updateagent.Config{}, errors.New("unexpected identity path")
	}
}

func (e *httpBackedRuntimeCredentialExecutor) activeIdentity() updateagent.Config {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.active
}

type httpE2ERuntimeTokenClaimStore struct {
	mu    sync.Mutex
	state *updateagent.RuntimeTokenClaimState
}

func (s *httpE2ERuntimeTokenClaimStore) Load() (
	updateagent.RuntimeTokenClaimState,
	bool,
	error,
) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state == nil {
		return updateagent.RuntimeTokenClaimState{}, false, nil
	}
	return *s.state, true, nil
}

func (s *httpE2ERuntimeTokenClaimStore) Save(
	state updateagent.RuntimeTokenClaimState,
) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state != nil && *s.state != state {
		return errors.New("claim state already exists")
	}
	copy := state
	s.state = &copy
	return nil
}

func (s *httpE2ERuntimeTokenClaimStore) Delete(
	expected updateagent.RuntimeTokenClaimState,
) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state == nil {
		return nil
	}
	if *s.state != expected {
		return errors.New("claim state changed before delete")
	}
	s.state = nil
	return nil
}

func decodeManagedHostAgentIdentity(
	t *testing.T,
	panelURL string,
	token string,
) updateagent.Config {
	t.Helper()
	payload, err := json.Marshal(map[string]string{
		"panel_url":     panelURL,
		"node_id":       "host-agent-a",
		"runtime_token": token,
		"service_name":  "Host Agent A",
	})
	if err != nil {
		t.Fatal(err)
	}
	var identity updateagent.Config
	if err := json.Unmarshal(payload, &identity); err != nil {
		t.Fatal(err)
	}
	if err := identity.Validate(); err != nil ||
		!identity.IsManagedBootstrap() {
		t.Fatalf("managed identity is invalid: %v", err)
	}
	return identity
}
