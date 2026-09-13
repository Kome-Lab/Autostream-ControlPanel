package httpapi

import (
	"bytes"
	"encoding/json"
	"github.com/example/autostream-control-panel/internal/store"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestRuntimeTokenRotationHTTPKeepsCredentialOffAdminPathAndRotatesThroughDedicatedBearer(t *testing.T) {
	heartbeatClock := &runtimeTokenRotationHeartbeatClock{}
	fixture := newRuntimeTokenRotationHTTPFixtureWithHeartbeatClock(
		t,
		heartbeatClock.Now,
	)
	const claimID = "11111111-1111-4111-8111-111111111111"
	stage := fixture.adminRequest(
		t,
		http.MethodPost,
		"/system-updates/updaters/host-agent-a/runtime-token-rotations",
		`{"idempotency_key":"rotation-request-1"}`,
	)
	if stage.Code != http.StatusCreated {
		t.Fatalf("stage status=%d body=%s", stage.Code, stage.Body.String())
	}
	assertRuntimeTokenRotationNoStore(t, stage)
	if strings.Contains(stage.Body.String(), "ast_svc_") ||
		strings.Contains(stage.Body.String(), `"runtime_token"`) {
		t.Fatalf("admin stage leaked runtime credential: %s", stage.Body.String())
	}
	var staged struct {
		Rotation runtimeTokenRotationResponse `json:"rotation"`
		Created  bool                         `json:"created"`
	}
	if err := json.NewDecoder(stage.Body).Decode(&staged); err != nil {
		t.Fatal(err)
	}
	if !staged.Created ||
		staged.Rotation.ServiceID != "host-agent-a" ||
		staged.Rotation.ExecutionHostID != "host-a" ||
		staged.Rotation.Status != store.SystemUpdateRuntimeTokenRotationStaged ||
		staged.Rotation.Revision != 1 ||
		staged.Rotation.ExpectedOwnershipEpoch != fixture.ownershipEpoch ||
		staged.Rotation.ExpectedSourcePolicyRevision != fixture.policy.Revision ||
		staged.Rotation.ExpectedProjectionRevision != fixture.policy.ProjectionRevision ||
		staged.Rotation.ExpectedLocalExecutorPolicyRevision != fixture.policy.LocalExecutorPolicyRevision {
		t.Fatalf("unexpected staged rotation: %#v", staged)
	}

	replay := fixture.adminRequest(
		t,
		http.MethodPost,
		"/system-updates/updaters/host-agent-a/runtime-token-rotations",
		`{"idempotency_key":"rotation-request-1"}`,
	)
	if replay.Code != http.StatusOK ||
		strings.Contains(replay.Body.String(), "ast_svc_") {
		t.Fatalf("stage replay status=%d body=%s", replay.Code, replay.Body.String())
	}
	var replayed struct {
		Rotation runtimeTokenRotationResponse `json:"rotation"`
		Created  bool                         `json:"created"`
	}
	if err := json.NewDecoder(replay.Body).Decode(&replayed); err != nil {
		t.Fatal(err)
	}
	if replayed.Created || replayed.Rotation.ID != staged.Rotation.ID {
		t.Fatalf("unexpected stage replay: %#v", replayed)
	}

	active := fixture.adminRequest(
		t,
		http.MethodGet,
		"/system-updates/updaters/host-agent-a/runtime-token-rotations/active",
		"",
	)
	if active.Code != http.StatusOK ||
		!strings.Contains(active.Body.String(), staged.Rotation.ID) {
		t.Fatalf("active status=%d body=%s", active.Code, active.Body.String())
	}
	assertRuntimeTokenRotationNoStore(t, active)

	policy := fixture.serviceRequest(
		t,
		fixture.oldToken.RawToken,
		"/services/host-agent/policy",
		`{"service_id":"host-agent-a","current_revision":0}`,
	)
	if policy.Code != http.StatusOK {
		t.Fatalf("policy status=%d body=%s", policy.Code, policy.Body.String())
	}
	var policyBody hostAgentPolicyResponse
	if err := json.NewDecoder(policy.Body).Decode(&policyBody); err != nil {
		t.Fatal(err)
	}
	if policyBody.RuntimeTokenRotation == nil ||
		policyBody.RuntimeTokenRotation.ID != staged.Rotation.ID ||
		policyBody.RuntimeTokenRotation.ServiceID != "host-agent-a" ||
		policyBody.RuntimeTokenRotation.ExecutionHostID != "host-a" ||
		policyBody.RuntimeTokenRotation.Revision != 1 ||
		policyBody.RuntimeTokenRotation.PreviousTokenID != fixture.oldToken.ID ||
		policyBody.RuntimeTokenRotation.StagedTokenID == "" {
		t.Fatalf("policy omitted bound rotation: %#v", policyBody.RuntimeTokenRotation)
	}

	wrongHost := fixture.serviceRequest(
		t,
		fixture.wrongHostToken.RawToken,
		"/services/host-agent/runtime-token-rotations/"+staged.Rotation.ID+"/credential/claim",
		`{"expected_revision":1,"claim_id":"`+claimID+`"}`,
	)
	if wrongHost.Code != http.StatusForbidden ||
		!strings.Contains(wrongHost.Body.String(), `"code":"runtime_token_rotation_agent_mismatch"`) {
		t.Fatalf("wrong-host claim status=%d body=%s", wrongHost.Code, wrongHost.Body.String())
	}

	claim := fixture.serviceRequest(
		t,
		fixture.oldToken.RawToken,
		"/services/host-agent/runtime-token-rotations/"+staged.Rotation.ID+"/credential/claim",
		`{"expected_revision":1,"claim_id":"`+claimID+`"}`,
	)
	if claim.Code != http.StatusOK {
		t.Fatalf("claim status=%d body=%s", claim.Code, claim.Body.String())
	}
	assertRuntimeTokenRotationNoStore(t, claim)
	var claimed runtimeTokenRotationClaimResponse
	if err := json.NewDecoder(claim.Body).Decode(&claimed); err != nil {
		t.Fatal(err)
	}
	if !claimed.Claimed ||
		claimed.Rotation.Revision != 2 ||
		claimed.Rotation.CredentialClaimedAt == nil ||
		claimed.Credential.TokenID != staged.Rotation.StagedTokenID ||
		!strings.HasPrefix(claimed.Credential.RuntimeToken, "ast_svc_") {
		t.Fatalf("unexpected credential claim: %#v", claimed)
	}

	lostResponseReplay := fixture.serviceRequest(
		t,
		fixture.oldToken.RawToken,
		"/services/host-agent/runtime-token-rotations/"+staged.Rotation.ID+"/credential/claim",
		`{"expected_revision":1,"claim_id":"`+claimID+`"}`,
	)
	if lostResponseReplay.Code != http.StatusOK {
		t.Fatalf("claim replay status=%d body=%s", lostResponseReplay.Code, lostResponseReplay.Body.String())
	}
	var replayedClaim runtimeTokenRotationClaimResponse
	if err := json.NewDecoder(lostResponseReplay.Body).Decode(&replayedClaim); err != nil {
		t.Fatal(err)
	}
	if replayedClaim.Claimed ||
		replayedClaim.Credential != claimed.Credential ||
		replayedClaim.Rotation.Revision != 2 {
		t.Fatalf("claim response-loss replay changed credential: %#v", replayedClaim)
	}
	newRevisionClaim := fixture.serviceRequest(
		t,
		fixture.oldToken.RawToken,
		"/services/host-agent/runtime-token-rotations/"+staged.Rotation.ID+"/credential/claim",
		`{"expected_revision":2,"claim_id":"22222222-2222-4222-8222-222222222222"}`,
	)
	if newRevisionClaim.Code != http.StatusConflict ||
		!strings.Contains(newRevisionClaim.Body.String(), `"code":"runtime_token_rotation_credential_already_claimed"`) {
		t.Fatalf("new-revision re-claim status=%d body=%s", newRevisionClaim.Code, newRevisionClaim.Body.String())
	}

	stagedNormalAPI := fixture.serviceRequest(
		t,
		claimed.Credential.RuntimeToken,
		"/services/host-agent/policy",
		`{"service_id":"host-agent-a","current_revision":0}`,
	)
	if stagedNormalAPI.Code != http.StatusUnauthorized {
		t.Fatalf("staged credential accessed normal API: %d %s", stagedNormalAPI.Code, stagedNormalAPI.Body.String())
	}

	localStaged := fixture.serviceRequest(
		t,
		claimed.Credential.RuntimeToken,
		"/services/host-agent/runtime-token-rotations/"+staged.Rotation.ID+"/local-staged",
		`{"expected_revision":2}`,
	)
	if localStaged.Code != http.StatusOK {
		t.Fatalf("local-staged status=%d body=%s", localStaged.Code, localStaged.Body.String())
	}
	var localStagedBody runtimeTokenRotationMutationResponse
	if err := json.NewDecoder(localStaged.Body).Decode(&localStagedBody); err != nil {
		t.Fatal(err)
	}
	if !localStagedBody.Applied ||
		localStagedBody.Rotation.Status != store.SystemUpdateRuntimeTokenRotationLocalStaged ||
		localStagedBody.Rotation.Revision != 3 ||
		!strings.HasPrefix(localStagedBody.Rotation.LocalStageReceiptID, "staged-token:") ||
		localStagedBody.Rotation.LocalStageAcknowledgedAt == nil {
		t.Fatalf("unexpected local stage acknowledgement: %#v", localStagedBody)
	}
	possessionOnlyProof := fixture.serviceRequest(
		t,
		claimed.Credential.RuntimeToken,
		"/services/host-agent/runtime-token-rotations/"+staged.Rotation.ID+"/heartbeat-proof",
		`{"expected_revision":3}`,
	)
	if possessionOnlyProof.Code != http.StatusBadRequest ||
		!strings.Contains(possessionOnlyProof.Body.String(), `"code":"invalid_runtime_token_rotation"`) {
		t.Fatalf(
			"credential-only proof status=%d body=%s",
			possessionOnlyProof.Code,
			possessionOnlyProof.Body.String(),
		)
	}
	heartbeatClock.Set(*localStagedBody.Rotation.LocalStageAcknowledgedAt)
	fixture.reportRuntimeTokenRotationHeartbeat(t, localStagedBody.Rotation)
	heartbeatProofBody := runtimeTokenRotationHeartbeatProofBody(
		t, fixture, localStagedBody.Rotation,
	)
	equalTimestampProof := fixture.serviceRequest(
		t,
		claimed.Credential.RuntimeToken,
		"/services/host-agent/runtime-token-rotations/"+staged.Rotation.ID+"/heartbeat-proof",
		heartbeatProofBody,
	)
	if equalTimestampProof.Code != http.StatusConflict ||
		!strings.Contains(
			equalTimestampProof.Body.String(),
			`"code":"runtime_token_rotation_heartbeat_proof_invalid"`,
		) {
		t.Fatalf(
			"equal local-stage heartbeat proof status=%d body=%s",
			equalTimestampProof.Code,
			equalTimestampProof.Body.String(),
		)
	}
	freshHeartbeatAt := localStagedBody.Rotation.LocalStageAcknowledgedAt.
		Add(time.Millisecond)
	if wait := time.Until(freshHeartbeatAt); wait > 0 {
		time.Sleep(wait)
	}
	heartbeatClock.Set(freshHeartbeatAt)
	fixture.reportRuntimeTokenRotationHeartbeat(t, localStagedBody.Rotation)

	wrongProof := fixture.serviceRequest(
		t,
		fixture.oldToken.RawToken,
		"/services/host-agent/runtime-token-rotations/"+staged.Rotation.ID+"/heartbeat-proof",
		heartbeatProofBody,
	)
	if wrongProof.Code != http.StatusUnauthorized ||
		!strings.Contains(wrongProof.Body.String(), `"code":"invalid_staged_runtime_token"`) {
		t.Fatalf("wrong proof status=%d body=%s", wrongProof.Code, wrongProof.Body.String())
	}
	prematureActivation := fixture.serviceRequest(
		t,
		claimed.Credential.RuntimeToken,
		"/services/host-agent/runtime-token-rotations/"+staged.Rotation.ID+"/activate",
		`{"expected_revision":3}`,
	)
	if prematureActivation.Code != http.StatusConflict ||
		!strings.Contains(prematureActivation.Body.String(), `"code":"runtime_token_rotation_transition_invalid"`) {
		t.Fatalf(
			"activation without heartbeat proof status=%d body=%s",
			prematureActivation.Code,
			prematureActivation.Body.String(),
		)
	}
	proof := fixture.serviceRequest(
		t,
		claimed.Credential.RuntimeToken,
		"/services/host-agent/runtime-token-rotations/"+staged.Rotation.ID+"/heartbeat-proof",
		heartbeatProofBody,
	)
	if proof.Code != http.StatusOK {
		t.Fatalf("proof status=%d body=%s", proof.Code, proof.Body.String())
	}
	var proofBody runtimeTokenRotationMutationResponse
	if err := json.NewDecoder(proof.Body).Decode(&proofBody); err != nil {
		t.Fatal(err)
	}
	if !proofBody.Applied ||
		proofBody.Rotation.Status != store.SystemUpdateRuntimeTokenRotationHeartbeatProved ||
		proofBody.Rotation.Revision != 4 {
		t.Fatalf("unexpected proof: %#v", proofBody)
	}

	activate := fixture.serviceRequest(
		t,
		claimed.Credential.RuntimeToken,
		"/services/host-agent/runtime-token-rotations/"+staged.Rotation.ID+"/activate",
		`{"expected_revision":4}`,
	)
	if activate.Code != http.StatusOK {
		t.Fatalf("activate status=%d body=%s", activate.Code, activate.Body.String())
	}
	var activated runtimeTokenRotationMutationResponse
	if err := json.NewDecoder(activate.Body).Decode(&activated); err != nil {
		t.Fatal(err)
	}
	if !activated.Applied ||
		activated.Rotation.Status != store.SystemUpdateRuntimeTokenRotationActivated ||
		activated.Rotation.Revision != 5 {
		t.Fatalf("unexpected activation: %#v", activated)
	}
	activationReplay := fixture.serviceRequest(
		t,
		claimed.Credential.RuntimeToken,
		"/services/host-agent/runtime-token-rotations/"+staged.Rotation.ID+"/activate",
		`{"expected_revision":4}`,
	)
	if activationReplay.Code != http.StatusOK {
		t.Fatalf("activation replay status=%d body=%s", activationReplay.Code, activationReplay.Body.String())
	}
	var replayedActivation runtimeTokenRotationMutationResponse
	if err := json.NewDecoder(activationReplay.Body).Decode(&replayedActivation); err != nil {
		t.Fatal(err)
	}
	if replayedActivation.Applied ||
		replayedActivation.Rotation.Status != store.SystemUpdateRuntimeTokenRotationActivated ||
		replayedActivation.Rotation.Revision != 5 {
		t.Fatalf("unexpected activation replay: %#v", replayedActivation)
	}
	preHeartbeatMutation := fixture.adminRequest(
		t,
		http.MethodPost,
		"/nodes/host-agent-a/configure-token",
		"",
	)
	assertUpdaterSystemUpdateMutationConflict(t, preHeartbeatMutation)
	activatedService, err := fixture.auth.GetService(t.Context(), "host-agent-a")
	if err != nil {
		t.Fatal(err)
	}
	if activatedService.NodeTokenRotatedAt == nil {
		t.Fatalf("activated service omitted token generation timestamp: %#v", activatedService)
	}
	postActivationHeartbeatAt := activatedService.NodeTokenRotatedAt.Add(time.Millisecond)
	if wait := time.Until(postActivationHeartbeatAt); wait > 0 {
		time.Sleep(wait)
	}
	heartbeatClock.Set(postActivationHeartbeatAt)
	fixture.reportRuntimeTokenRotationHeartbeatWithToken(
		t,
		activated.Rotation,
		claimed.Credential.RuntimeToken,
	)
	postHeartbeatMutation := fixture.adminRequest(
		t,
		http.MethodPost,
		"/nodes/host-agent-a/configure-token",
		"",
	)
	if postHeartbeatMutation.Code != http.StatusCreated {
		t.Fatalf(
			"fresh activated-token heartbeat did not release identity fence: status=%d body=%s",
			postHeartbeatMutation.Code,
			postHeartbeatMutation.Body.String(),
		)
	}

	oldAfterActivation := fixture.serviceRequest(
		t,
		fixture.oldToken.RawToken,
		"/services/host-agent/policy",
		`{"service_id":"host-agent-a","current_revision":0}`,
	)
	if oldAfterActivation.Code != http.StatusUnauthorized {
		t.Fatalf("old token survived activation: %d %s", oldAfterActivation.Code, oldAfterActivation.Body.String())
	}
	newAfterActivation := fixture.serviceRequest(
		t,
		claimed.Credential.RuntimeToken,
		"/services/host-agent/policy",
		`{"service_id":"host-agent-a","current_revision":0}`,
	)
	if newAfterActivation.Code != http.StatusOK {
		t.Fatalf("new token did not access normal API: %d %s", newAfterActivation.Code, newAfterActivation.Body.String())
	}
	var newPolicy hostAgentPolicyResponse
	if err := json.NewDecoder(newAfterActivation.Body).Decode(&newPolicy); err != nil {
		t.Fatal(err)
	}
	if newPolicy.RuntimeTokenRotation != nil {
		t.Fatalf("terminal rotation remained in active policy: %#v", newPolicy.RuntimeTokenRotation)
	}

	auditJSON, err := json.Marshal(fixture.auth.AuditEvents())
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(auditJSON, []byte(claimed.Credential.RuntimeToken)) ||
		bytes.Contains(auditJSON, []byte(fixture.oldToken.RawToken)) {
		t.Fatalf("audit leaked a runtime credential: %s", auditJSON)
	}
}
