package httpapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/example/autostream-control-panel/internal/security"
	"github.com/example/autostream-control-panel/internal/store"
	"github.com/example/autostream-control-panel/internal/updateradapter"
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

func TestRuntimeTokenRotationHTTPFencesEveryUpdaterIdentityMutation(t *testing.T) {
	for _, mutation := range []string{
		"configure token regeneration",
		"configure stage",
		"configure activation",
		"node runtime token rotation",
		"generic token rotation",
		"generic token revocation",
		"service deletion",
	} {
		t.Run(mutation, func(t *testing.T) {
			fixture := newRuntimeTokenRotationHTTPFixture(t)
			fixture.stage(t, "identity-fence-"+strings.ReplaceAll(mutation, " ", "-"))

			var response *httptest.ResponseRecorder
			switch mutation {
			case "configure token regeneration":
				response = fixture.adminRequest(
					t,
					http.MethodPost,
					"/nodes/host-agent-a/configure-token",
					"",
				)
			case "configure stage":
				const configureToken = "runtime-rotation-configure-stage"
				if _, err := fixture.auth.SetServiceConfigureToken(
					t.Context(),
					"host-agent-a",
					security.HashToken(configureToken),
					time.Now().UTC().Add(time.Hour),
				); err != nil {
					t.Fatal(err)
				}
				response = fixture.publicRequest(
					t,
					http.MethodPost,
					"/services/host-agent/runtime-identity/stage",
					`{"nodeId":"host-agent-a","configureToken":"`+
						configureToken+`"}`,
				)
			case "configure activation":
				staged := stageUpdaterIdentityConfiguration(
					t,
					fixture.auth,
					"host-agent-a",
					"runtime-rotation-configure-activation",
				)
				response = fixture.publicRequest(
					t,
					http.MethodPost,
					"/services/host-agent/runtime-identity/activate",
					`{"nodeId":"host-agent-a","configurationId":"`+
						staged.Token.ID+`","activationToken":"`+
						staged.ActivationToken+`"}`,
				)
			case "node runtime token rotation":
				response = fixture.adminRequest(
					t,
					http.MethodPost,
					"/nodes/host-agent-a/rotate-token",
					"",
				)
			case "generic token rotation":
				response = fixture.adminRequest(
					t,
					http.MethodPost,
					"/api-tokens/"+fixture.oldToken.ID+"/rotate",
					"",
				)
			case "generic token revocation":
				response = fixture.adminRequest(
					t,
					http.MethodDelete,
					"/api-tokens/"+fixture.oldToken.ID,
					"",
				)
			case "service deletion":
				response = fixture.adminRequest(
					t,
					http.MethodDelete,
					"/services/host-agent-a",
					"",
				)
			default:
				t.Fatalf("unhandled mutation %q", mutation)
			}
			assertUpdaterSystemUpdateMutationConflict(t, response)
			service, err := fixture.auth.GetService(t.Context(), "host-agent-a")
			if err != nil {
				t.Fatalf("blocked mutation removed updater: %v", err)
			}
			if service.TokenID != fixture.oldToken.ID {
				t.Fatalf(
					"blocked mutation changed active token: got=%q want=%q",
					service.TokenID,
					fixture.oldToken.ID,
				)
			}
		})
	}
}

func TestRuntimeTokenRotationHTTPIdentityFenceCoversEveryActivePhase(t *testing.T) {
	for _, phase := range []string{
		"staged",
		"credential_claimed",
		"local_staged",
		"heartbeat_proved",
		"cancel_requested",
	} {
		t.Run(phase, func(t *testing.T) {
			heartbeatClock := &runtimeTokenRotationHeartbeatClock{}
			fixture := newRuntimeTokenRotationHTTPFixtureWithHeartbeatClock(
				t,
				heartbeatClock.Now,
			)
			rotation := fixture.stage(t, "identity-phase-"+phase)
			var stagedRawToken string
			if phase != "staged" {
				claim := fixture.serviceRequest(
					t,
					fixture.oldToken.RawToken,
					"/services/host-agent/runtime-token-rotations/"+
						rotation.ID+"/credential/claim",
					`{"expected_revision":1,"claim_id":"55555555-5555-4555-8555-555555555555"}`,
				)
				if claim.Code != http.StatusOK {
					t.Fatalf(
						"claim status=%d body=%s",
						claim.Code,
						claim.Body.String(),
					)
				}
				var claimed runtimeTokenRotationClaimResponse
				if err := json.NewDecoder(claim.Body).Decode(&claimed); err != nil {
					t.Fatal(err)
				}
				rotation = claimed.Rotation
				stagedRawToken = claimed.Credential.RuntimeToken
			}
			if phase == "local_staged" ||
				phase == "heartbeat_proved" ||
				phase == "cancel_requested" {
				local := fixture.serviceRequest(
					t,
					stagedRawToken,
					"/services/host-agent/runtime-token-rotations/"+
						rotation.ID+"/local-staged",
					`{"expected_revision":2}`,
				)
				if local.Code != http.StatusOK {
					t.Fatalf(
						"local stage status=%d body=%s",
						local.Code,
						local.Body.String(),
					)
				}
				var localBody runtimeTokenRotationMutationResponse
				if err := json.NewDecoder(local.Body).Decode(&localBody); err != nil {
					t.Fatal(err)
				}
				rotation = localBody.Rotation
			}
			if phase == "heartbeat_proved" {
				freshHeartbeatAt := rotation.LocalStageAcknowledgedAt.
					Add(time.Millisecond)
				if wait := time.Until(freshHeartbeatAt); wait > 0 {
					time.Sleep(wait)
				}
				heartbeatClock.Set(freshHeartbeatAt)
				fixture.reportRuntimeTokenRotationHeartbeat(t, rotation)
				proof := fixture.serviceRequest(
					t,
					stagedRawToken,
					"/services/host-agent/runtime-token-rotations/"+
						rotation.ID+"/heartbeat-proof",
					runtimeTokenRotationHeartbeatProofBody(
						t,
						fixture,
						rotation,
					),
				)
				if proof.Code != http.StatusOK {
					t.Fatalf(
						"heartbeat proof status=%d body=%s",
						proof.Code,
						proof.Body.String(),
					)
				}
				var proofBody runtimeTokenRotationMutationResponse
				if err := json.NewDecoder(proof.Body).Decode(&proofBody); err != nil {
					t.Fatal(err)
				}
				rotation = proofBody.Rotation
			}
			if phase == "cancel_requested" {
				cancel := fixture.adminRequest(
					t,
					http.MethodPost,
					"/system-updates/updaters/host-agent-a/runtime-token-rotations/"+
						rotation.ID+"/cancel",
					`{"expected_revision":3}`,
				)
				if cancel.Code != http.StatusOK {
					t.Fatalf(
						"cancel request status=%d body=%s",
						cancel.Code,
						cancel.Body.String(),
					)
				}
			}

			response := fixture.adminRequest(
				t,
				http.MethodPost,
				"/nodes/host-agent-a/configure-token",
				"",
			)
			assertUpdaterSystemUpdateMutationConflict(t, response)
		})
	}
}

func TestRuntimeTokenRotationHTTPEmergencyTerminalAllowsManualIdentityRecovery(t *testing.T) {
	fixture := newRuntimeTokenRotationHTTPFixture(t)
	const preEmergencyConfigureToken = "pre-emergency-configure-token"
	if _, err := fixture.auth.SetServiceConfigureToken(
		t.Context(),
		"host-agent-a",
		security.HashToken(preEmergencyConfigureToken),
		time.Now().UTC().Add(time.Hour),
	); err != nil {
		t.Fatal(err)
	}
	staged := fixture.stage(t, "emergency-manual-recovery")
	emergency := fixture.adminRequest(
		t,
		http.MethodPost,
		"/system-updates/updaters/host-agent-a/runtime-token-rotations/"+
			staged.ID+"/emergency-revoke",
		`{"expected_revision":1,"token_slot":"previous"}`,
	)
	if emergency.Code != http.StatusOK {
		t.Fatalf(
			"emergency revoke status=%d body=%s",
			emergency.Code,
			emergency.Body.String(),
		)
	}
	staleConfigure := fixture.publicRequest(
		t,
		http.MethodPost,
		"/services/host-agent/runtime-identity/stage",
		`{"nodeId":"host-agent-a","configureToken":"`+
			preEmergencyConfigureToken+`"}`,
	)
	if staleConfigure.Code != http.StatusUnauthorized ||
		!strings.Contains(
			staleConfigure.Body.String(),
			`"code":"invalid_configure_token"`,
		) {
		t.Fatalf(
			"pre-emergency configure token survived: status=%d body=%s",
			staleConfigure.Code,
			staleConfigure.Body.String(),
		)
	}

	configure := fixture.adminRequest(
		t,
		http.MethodPost,
		"/nodes/host-agent-a/configure-token",
		"",
	)
	if configure.Code != http.StatusCreated ||
		!strings.Contains(configure.Body.String(), `"configure_token":"`) {
		t.Fatalf(
			"emergency recovery configure-token status=%d body=%s",
			configure.Code,
			configure.Body.String(),
		)
	}
	deleted := fixture.adminRequest(
		t,
		http.MethodDelete,
		"/services/host-agent-a",
		"",
	)
	if deleted.Code != http.StatusOK {
		t.Fatalf(
			"emergency recovery delete status=%d body=%s",
			deleted.Code,
			deleted.Body.String(),
		)
	}
	if _, err := fixture.auth.GetService(
		t.Context(),
		"host-agent-a",
	); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("emergency recovery deletion result err=%v", err)
	}
}

func TestRuntimeTokenRotationHTTPStrictBodiesCancelAndEmergencyRevoke(t *testing.T) {
	t.Run("strict bounded stage body", func(t *testing.T) {
		fixture := newRuntimeTokenRotationHTTPFixture(t)
		unknown := fixture.adminRequest(
			t,
			http.MethodPost,
			"/system-updates/updaters/host-agent-a/runtime-token-rotations",
			`{"idempotency_key":"request","unexpected":true}`,
		)
		if unknown.Code != http.StatusBadRequest ||
			!strings.Contains(unknown.Body.String(), `"code":"bad_request"`) {
			t.Fatalf("unknown field status=%d body=%s", unknown.Code, unknown.Body.String())
		}
		oversized := fixture.adminRequest(
			t,
			http.MethodPost,
			"/system-updates/updaters/host-agent-a/runtime-token-rotations",
			`{"idempotency_key":"`+strings.Repeat("a", maxHostAgentControlRequestBytes)+`"}`,
		)
		if oversized.Code != http.StatusBadRequest ||
			!strings.Contains(oversized.Body.String(), `"code":"bad_request"`) {
			t.Fatalf("oversized status=%d body=%s", oversized.Code, oversized.Body.String())
		}
		staged := fixture.stage(t, "strict-cancel-request")
		clientAssertedRollback := fixture.adminRequest(
			t,
			http.MethodPost,
			"/system-updates/updaters/host-agent-a/runtime-token-rotations/"+staged.ID+"/cancel",
			`{"expected_revision":1,"local_rollback_confirmed":true}`,
		)
		if clientAssertedRollback.Code != http.StatusBadRequest ||
			!strings.Contains(clientAssertedRollback.Body.String(), `"code":"bad_request"`) {
			t.Fatalf(
				"client asserted rollback status=%d body=%s",
				clientAssertedRollback.Code,
				clientAssertedRollback.Body.String(),
			)
		}
	})

	t.Run("admin cancel before local mutation", func(t *testing.T) {
		fixture := newRuntimeTokenRotationHTTPFixture(t)
		staged := fixture.stage(t, "cancel-request")
		canceled := fixture.adminRequest(
			t,
			http.MethodPost,
			"/system-updates/updaters/host-agent-a/runtime-token-rotations/"+staged.ID+"/cancel",
			`{"expected_revision":1}`,
		)
		if canceled.Code != http.StatusOK {
			t.Fatalf("cancel status=%d body=%s", canceled.Code, canceled.Body.String())
		}
		var body runtimeTokenRotationMutationResponse
		if err := json.NewDecoder(canceled.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if !body.Applied ||
			body.Rotation.Status != store.SystemUpdateRuntimeTokenRotationCanceled ||
			body.Rotation.Revision != 2 {
			t.Fatalf("unexpected cancel: %#v", body)
		}
		active := fixture.adminRequest(
			t,
			http.MethodGet,
			"/system-updates/updaters/host-agent-a/runtime-token-rotations/active",
			"",
		)
		if active.Code != http.StatusNoContent {
			t.Fatalf("canceled rotation remained active: %d %s", active.Code, active.Body.String())
		}
	})

	t.Run("claimed rotation requires old-token cancel acknowledgement", func(t *testing.T) {
		fixture := newRuntimeTokenRotationHTTPFixture(t)
		staged := fixture.stage(t, "cancel-ack-request")
		claim := fixture.serviceRequest(
			t,
			fixture.oldToken.RawToken,
			"/services/host-agent/runtime-token-rotations/"+staged.ID+"/credential/claim",
			`{"expected_revision":1,"claim_id":"44444444-4444-4444-8444-444444444444"}`,
		)
		if claim.Code != http.StatusOK {
			t.Fatalf("claim status=%d body=%s", claim.Code, claim.Body.String())
		}
		var claimed runtimeTokenRotationClaimResponse
		if err := json.NewDecoder(claim.Body).Decode(&claimed); err != nil {
			t.Fatal(err)
		}
		local := fixture.serviceRequest(
			t,
			claimed.Credential.RuntimeToken,
			"/services/host-agent/runtime-token-rotations/"+staged.ID+"/local-staged",
			`{"expected_revision":2}`,
		)
		if local.Code != http.StatusOK {
			t.Fatalf("local stage status=%d body=%s", local.Code, local.Body.String())
		}
		cancelRequested := fixture.adminRequest(
			t,
			http.MethodPost,
			"/system-updates/updaters/host-agent-a/runtime-token-rotations/"+staged.ID+"/cancel",
			`{"expected_revision":3}`,
		)
		if cancelRequested.Code != http.StatusOK {
			t.Fatalf(
				"cancel request status=%d body=%s",
				cancelRequested.Code,
				cancelRequested.Body.String(),
			)
		}
		var requested runtimeTokenRotationMutationResponse
		if err := json.NewDecoder(cancelRequested.Body).Decode(&requested); err != nil {
			t.Fatal(err)
		}
		if !requested.Applied ||
			requested.Rotation.Status != "cancel_requested" ||
			requested.Rotation.Revision != 4 {
			t.Fatalf("unexpected cancel request: %#v", requested)
		}
		policy := fixture.serviceRequest(
			t,
			fixture.oldToken.RawToken,
			"/services/host-agent/policy",
			`{"service_id":"host-agent-a","current_revision":0}`,
		)
		if policy.Code != http.StatusOK {
			t.Fatalf("cancel policy status=%d body=%s", policy.Code, policy.Body.String())
		}
		var policyBody hostAgentPolicyResponse
		if err := json.NewDecoder(policy.Body).Decode(&policyBody); err != nil {
			t.Fatal(err)
		}
		if policyBody.RuntimeTokenRotation == nil ||
			policyBody.RuntimeTokenRotation.Status != "cancel_requested" ||
			policyBody.RuntimeTokenRotation.Revision != 4 {
			t.Fatalf("policy omitted cancel request: %#v", policyBody.RuntimeTokenRotation)
		}
		acknowledged := fixture.serviceRequest(
			t,
			fixture.oldToken.RawToken,
			"/services/host-agent/runtime-token-rotations/"+staged.ID+"/cancel-ack",
			`{"expected_revision":4}`,
		)
		if acknowledged.Code != http.StatusOK {
			t.Fatalf(
				"cancel ack status=%d body=%s",
				acknowledged.Code,
				acknowledged.Body.String(),
			)
		}
		var canceled runtimeTokenRotationMutationResponse
		if err := json.NewDecoder(acknowledged.Body).Decode(&canceled); err != nil {
			t.Fatal(err)
		}
		if !canceled.Applied ||
			canceled.Rotation.Status != store.SystemUpdateRuntimeTokenRotationCanceled ||
			canceled.Rotation.Revision != 5 {
			t.Fatalf("unexpected cancel ack: %#v", canceled)
		}
		active := fixture.adminRequest(
			t,
			http.MethodGet,
			"/system-updates/updaters/host-agent-a/runtime-token-rotations/active",
			"",
		)
		if active.Code != http.StatusNoContent {
			t.Fatalf("cancel ack remained active: %d %s", active.Code, active.Body.String())
		}
	})

	t.Run("server-selected emergency token slot", func(t *testing.T) {
		fixture := newRuntimeTokenRotationHTTPFixture(t)
		staged := fixture.stage(t, "emergency-request")
		invalidSlot := fixture.adminRequest(
			t,
			http.MethodPost,
			"/system-updates/updaters/host-agent-a/runtime-token-rotations/"+staged.ID+"/emergency-revoke",
			`{"expected_revision":1,"token_slot":"attacker-selected-token-id"}`,
		)
		if invalidSlot.Code != http.StatusBadRequest ||
			!strings.Contains(invalidSlot.Body.String(), `"code":"invalid_runtime_token_rotation_token_slot"`) {
			t.Fatalf("invalid slot status=%d body=%s", invalidSlot.Code, invalidSlot.Body.String())
		}
		claim := fixture.serviceRequest(
			t,
			fixture.oldToken.RawToken,
			"/services/host-agent/runtime-token-rotations/"+staged.ID+"/credential/claim",
			`{"expected_revision":1,"claim_id":"33333333-3333-4333-8333-333333333333"}`,
		)
		if claim.Code != http.StatusOK {
			t.Fatalf("claim status=%d body=%s", claim.Code, claim.Body.String())
		}
		var claimed runtimeTokenRotationClaimResponse
		if err := json.NewDecoder(claim.Body).Decode(&claimed); err != nil {
			t.Fatal(err)
		}
		if !claimed.Claimed ||
			claimed.Rotation.Revision != 2 ||
			claimed.Credential.TokenID != staged.StagedTokenID ||
			claimed.Credential.RuntimeToken == "" {
			t.Fatalf("unexpected credential claim: %#v", claimed)
		}
		revoked := fixture.adminRequest(
			t,
			http.MethodPost,
			"/system-updates/updaters/host-agent-a/runtime-token-rotations/"+staged.ID+"/emergency-revoke",
			`{"expected_revision":2,"token_slot":"previous"}`,
		)
		if revoked.Code != http.StatusOK {
			t.Fatalf("emergency revoke status=%d body=%s", revoked.Code, revoked.Body.String())
		}
		var body runtimeTokenRotationMutationResponse
		if err := json.NewDecoder(revoked.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if !body.Applied ||
			body.Code != store.SystemUpdateRuntimeTokenRotationEmergencyCode ||
			body.Rotation.Status != store.SystemUpdateRuntimeTokenRotationCanceled ||
			body.Rotation.EmergencyRevokedTokenID != staged.PreviousTokenID ||
			body.Rotation.Revision != 3 {
			t.Fatalf("unexpected emergency revoke: %#v", body)
		}

		replay := fixture.adminRequest(
			t,
			http.MethodPost,
			"/system-updates/updaters/host-agent-a/runtime-token-rotations/"+staged.ID+"/emergency-revoke",
			`{"expected_revision":2,"token_slot":"previous"}`,
		)
		if replay.Code != http.StatusOK {
			t.Fatalf("emergency revoke replay status=%d body=%s", replay.Code, replay.Body.String())
		}
		var replayed runtimeTokenRotationMutationResponse
		if err := json.NewDecoder(replay.Body).Decode(&replayed); err != nil {
			t.Fatal(err)
		}
		if replayed.Applied ||
			replayed.Code != store.SystemUpdateRuntimeTokenRotationEmergencyCode ||
			replayed.Rotation.Status != store.SystemUpdateRuntimeTokenRotationCanceled ||
			replayed.Rotation.EmergencyRevokedTokenID != staged.PreviousTokenID ||
			replayed.Rotation.Revision != 3 {
			t.Fatalf("unexpected emergency revoke replay: %#v", replayed)
		}

		for name, rawToken := range map[string]string{
			"previous": fixture.oldToken.RawToken,
			"staged":   claimed.Credential.RuntimeToken,
		} {
			t.Run(name+" token is revoked", func(t *testing.T) {
				policy := fixture.serviceRequest(
					t,
					rawToken,
					"/services/host-agent/policy",
					`{"service_id":"host-agent-a","current_revision":0}`,
				)
				if policy.Code != http.StatusUnauthorized ||
					!strings.Contains(policy.Body.String(), `"code":"invalid_service_token"`) {
					t.Fatalf(
						"%s token remained usable: status=%d body=%s",
						name,
						policy.Code,
						policy.Body.String(),
					)
				}
			})
		}

		active := fixture.adminRequest(
			t,
			http.MethodGet,
			"/system-updates/updaters/host-agent-a/runtime-token-rotations/active",
			"",
		)
		if active.Code != http.StatusNoContent {
			t.Fatalf("emergency rotation remained active: %d %s", active.Code, active.Body.String())
		}
	})
}

func TestRuntimeTokenRotationAdminPermissionMatrix(t *testing.T) {
	fixture := newRuntimeTokenRotationHTTPFixture(t)
	required := []string{
		"system_updates.execute",
		"api_tokens.create",
		"api_tokens.revoke",
		"secrets.update",
	}
	for index, missing := range required {
		t.Run("missing "+missing, func(t *testing.T) {
			username := "rotation-limited-" + string(rune('a'+index))
			permissions := []string{"system_updates.read"}
			for _, permission := range required {
				if permission != missing {
					permissions = append(permissions, permission)
				}
			}
			if err := fixture.auth.AddUser(
				store.User{ID: username, Username: username},
				"correct horse battery",
				permissions,
			); err != nil {
				t.Fatal(err)
			}
			cookie, csrf := loginForTest(
				t, fixture.handler, username, "correct horse battery",
			)
			request := httptest.NewRequest(
				http.MethodPost,
				"/system-updates/updaters/host-agent-a/runtime-token-rotations",
				strings.NewReader(`{"idempotency_key":"permission-matrix"}`),
			)
			request.AddCookie(cookie)
			request.Header.Set("X-CSRF-Token", csrf)
			response := httptest.NewRecorder()
			fixture.handler.ServeHTTP(response, request)
			if response.Code != http.StatusForbidden ||
				!strings.Contains(response.Body.String(), `"code":"permission_denied"`) {
				t.Fatalf(
					"missing %s status=%d body=%s",
					missing,
					response.Code,
					response.Body.String(),
				)
			}
		})
	}

	if err := fixture.auth.AddUser(
		store.User{ID: "rotation-reader", Username: "rotation-reader"},
		"correct horse battery",
		[]string{"system_updates.read"},
	); err != nil {
		t.Fatal(err)
	}
	readerCookie, _ := loginForTest(
		t, fixture.handler, "rotation-reader", "correct horse battery",
	)
	readRequest := httptest.NewRequest(
		http.MethodGet,
		"/system-updates/updaters/host-agent-a/runtime-token-rotations/active",
		nil,
	)
	readRequest.AddCookie(readerCookie)
	readResponse := httptest.NewRecorder()
	fixture.handler.ServeHTTP(readResponse, readRequest)
	if readResponse.Code != http.StatusNoContent {
		t.Fatalf(
			"read-only active status=%d body=%s",
			readResponse.Code,
			readResponse.Body.String(),
		)
	}
}

type runtimeTokenRotationHTTPFixture struct {
	auth           *store.MemoryAuthStore
	handler        http.Handler
	cookie         *http.Cookie
	csrf           string
	oldToken       store.ServiceToken
	wrongHostToken store.ServiceToken
	policies       *store.MemoryUpdaterPolicyStore
	updates        *store.MemorySystemUpdateStore
	policy         store.UpdaterPolicy
	ownershipEpoch int64
}

func newRuntimeTokenRotationHTTPFixture(t *testing.T) runtimeTokenRotationHTTPFixture {
	return newRuntimeTokenRotationHTTPFixtureWithHeartbeatClock(t, nil)
}

func newRuntimeTokenRotationHTTPFixtureWithHeartbeatClock(
	t *testing.T,
	heartbeatNow func() time.Time,
) runtimeTokenRotationHTTPFixture {
	t.Helper()
	t.Setenv("AUTOSTREAM_SECRET_ENCRYPTION_KEY", strings.Repeat("runtime-rotation-key-", 2))
	auth := store.NewMemoryAuthStore(
		store.WithMemoryServiceHeartbeatClock(heartbeatNow),
	)
	if err := auth.AddUser(
		store.User{ID: "rotation-admin", Username: "rotation-admin"},
		"correct horse battery",
		[]string{
			"system_updates.read",
			"system_updates.execute",
			"api_tokens.create",
			"api_tokens.revoke",
			"services.disable",
			"secrets.update",
		},
	); err != nil {
		t.Fatal(err)
	}
	policies := store.NewMemoryUpdaterPolicyStore()
	updates := store.NewMemorySystemUpdateStore()
	workerToken, err := auth.CreateServiceToken(
		t.Context(),
		"worker",
		[]string{"service.register", "service.heartbeat"},
	)
	if err != nil {
		t.Fatal(err)
	}
	registerServiceWithTokenForTest(
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
	policy := store.UpdaterPolicy{
		TransportMode:             store.SystemUpdateTransportPullV2,
		ExecutionHostID:           "host-a",
		LocalExecutorPolicySHA256: "sha256:" + strings.Repeat("a", 64),
		PollIntervalSeconds:       15,
		HeartbeatIntervalSeconds:  30,
		Targets: []store.UpdaterPolicyTarget{{
			TargetID:        "worker-a",
			ServiceID:       "worker-a",
			ServiceType:     "worker",
			HostID:          "host-a",
			DeploymentMode:  "systemd",
			LocalListenPort: 18081,
		}},
	}
	savedPolicy, err := policies.SavePullUpdaterPolicy(
		t.Context(),
		updates,
		"host-agent-a",
		0,
		0,
		policy,
	)
	if err != nil {
		t.Fatal(err)
	}
	oldToken := registerRuntimeTokenRotationAgentForTest(
		t, auth, "host-agent-a", "host-a", 0,
	)
	reportedConfigSHA256, err := updateradapter.SystemdConfigurePortSidecarSHA256(
		"worker",
		18081,
		1,
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := auth.Heartbeat(t.Context(), oldToken, store.ServiceHeartbeat{
		ServiceID: "host-agent-a",
		Status:    "online",
		Version:   "v2.0.0",
		Capabilities: map[string]any{
			"host_agent":                         true,
			"observe_only":                       true,
			"update_executor":                    true,
			"mutation_enabled":                   false,
			"recovery_pending":                   false,
			"transport_mode":                     store.SystemUpdateTransportPullV2,
			"agent_protocol_version":             "2",
			"execution_host_id":                  "host-a",
			"ownership_epoch":                    int64(0),
			"source_policy_revision":             savedPolicy.Revision,
			"policy_revision":                    savedPolicy.ProjectionRevision,
			"policy_status":                      "applied",
			"local_executor_policy_revision":     savedPolicy.LocalExecutorPolicyRevision,
			"target_availability":                map[string]any{"worker-a": "available"},
			"target_availability_codes":          map[string]any{"worker-a": "executor_verified"},
			"reported_ports":                     map[string]any{"worker-a": int64(18081)},
			"port_drift":                         map[string]any{"worker-a": false},
			"reported_service_types":             map[string]any{"worker-a": "worker"},
			"reported_deployment_modes":          map[string]any{"worker-a": "systemd"},
			"reported_executor_policy_revisions": map[string]any{"worker-a": savedPolicy.LocalExecutorPolicyRevision},
			"reported_executor_policy_sha256": map[string]any{
				"worker-a": savedPolicy.LocalExecutorPolicySHA256,
			},
			"reported_config_revisions": map[string]any{"worker-a": int64(1)},
			"reported_config_sha256": map[string]any{
				"worker-a": reportedConfigSHA256,
			},
		},
	}); err != nil {
		t.Fatal(err)
	}
	activation, err := policies.ActivatePullUpdaterOwnership(
		t.Context(),
		auth,
		updates,
		store.ActivatePullUpdaterOwnershipParams{
			ServiceID:                           "host-agent-a",
			ExecutionHostID:                     "host-a",
			ExpectedExecutionHostOwnershipEpoch: 0,
			ExpectedSourcePolicyRevision:        savedPolicy.Revision,
			ExpectedProjectionRevision:          savedPolicy.ProjectionRevision,
			ExpectedLocalExecutorPolicyRevision: savedPolicy.LocalExecutorPolicyRevision,
			ExpectedLocalExecutorPolicySHA256:   savedPolicy.LocalExecutorPolicySHA256,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	ownership := activation.Ownership
	wrongHostToken := registerRuntimeTokenRotationAgentForTest(
		t, auth, "host-agent-b", "host-b", 1,
	)
	handler := NewServer(
		store.NewMemoryStreamStore(),
		WithAuthStore(auth),
		WithAuditStore(auth),
		WithServiceRegistryStore(auth),
		WithUpdaterPolicyStore(policies),
		WithSystemUpdateStore(updates),
	)
	cookie, csrf := loginForTest(
		t, handler, "rotation-admin", "correct horse battery",
	)
	return runtimeTokenRotationHTTPFixture{
		auth:           auth,
		handler:        handler,
		cookie:         cookie,
		csrf:           csrf,
		oldToken:       oldToken,
		wrongHostToken: wrongHostToken,
		policies:       policies,
		updates:        updates,
		policy:         savedPolicy,
		ownershipEpoch: ownership.OwnershipEpoch,
	}
}

type runtimeTokenRotationHeartbeatClock struct {
	mu    sync.Mutex
	fixed *time.Time
}

func (c *runtimeTokenRotationHeartbeatClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.fixed == nil {
		return time.Now().UTC()
	}
	return c.fixed.UTC()
}

func (c *runtimeTokenRotationHeartbeatClock) Set(now time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	now = now.UTC()
	c.fixed = &now
}

func registerRuntimeTokenRotationAgentForTest(
	t *testing.T,
	auth *store.MemoryAuthStore,
	serviceID string,
	executionHostID string,
	ownershipEpoch int64,
) store.ServiceToken {
	t.Helper()
	token, err := auth.CreateServiceToken(
		t.Context(),
		"update_agent",
		[]string{
			"service.register",
			"service.heartbeat",
			"service.config.read",
			"updates.claim",
			"updates.report",
			"updates.authorize",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := auth.PrecreateService(
		t.Context(),
		token,
		store.ServiceRegistration{
			ServiceID:       serviceID,
			ServiceType:     "update_agent",
			ServiceName:     serviceID,
			TransportMode:   store.SystemUpdateTransportPullV2,
			ExecutionHostID: executionHostID,
			OwnershipEpoch:  ownershipEpoch,
		},
	); err != nil {
		t.Fatal(err)
	}
	if _, err := auth.RegisterService(
		t.Context(),
		token,
		store.ServiceRegistration{
			ServiceID:       serviceID,
			ServiceType:     "update_agent",
			ServiceName:     serviceID,
			TransportMode:   store.SystemUpdateTransportPullV2,
			ExecutionHostID: executionHostID,
			OwnershipEpoch:  ownershipEpoch,
		},
	); err != nil {
		t.Fatal(err)
	}
	return token
}

func (f runtimeTokenRotationHTTPFixture) stage(
	t *testing.T,
	idempotencyKey string,
) runtimeTokenRotationResponse {
	t.Helper()
	response := f.adminRequest(
		t,
		http.MethodPost,
		"/system-updates/updaters/host-agent-a/runtime-token-rotations",
		`{"idempotency_key":"`+idempotencyKey+`"}`,
	)
	if response.Code != http.StatusCreated {
		t.Fatalf("stage status=%d body=%s", response.Code, response.Body.String())
	}
	var body struct {
		Rotation runtimeTokenRotationResponse `json:"rotation"`
	}
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	return body.Rotation
}

func (f runtimeTokenRotationHTTPFixture) reportRuntimeTokenRotationHeartbeat(
	t *testing.T,
	rotation runtimeTokenRotationResponse,
) {
	f.reportRuntimeTokenRotationHeartbeatWithToken(
		t,
		rotation,
		f.oldToken.RawToken,
	)
}

func (f runtimeTokenRotationHTTPFixture) reportRuntimeTokenRotationHeartbeatWithToken(
	t *testing.T,
	rotation runtimeTokenRotationResponse,
	rawToken string,
) {
	t.Helper()
	body, err := json.Marshal(map[string]any{
		"service_id": "host-agent-a",
		"status":     "online",
		"version":    "v2.0.0",
		"capabilities": map[string]any{
			"agent_version":                  "v2.0.0",
			"agent_protocol_version":         2,
			"executor_version":               "v2.0.0",
			"executor_protocol_version":      1,
			"mutation_protocol_version":      1,
			"execution_host_id":              rotation.ExecutionHostID,
			"ownership_epoch":                rotation.ExpectedOwnershipEpoch,
			"source_policy_revision":         rotation.ExpectedSourcePolicyRevision,
			"projection_revision":            rotation.ExpectedProjectionRevision,
			"local_executor_policy_revision": rotation.ExpectedLocalExecutorPolicyRevision,
			"local_executor_policy_sha256":   f.policy.LocalExecutorPolicySHA256,
			"local_stage_receipt_id":         rotation.LocalStageReceiptID,
			"local_phase":                    "staged_token_active",
			"host_agent":                     true,
			"update_executor":                true,
			"mutation_enabled":               true,
			"recovery_pending":               false,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	response := f.serviceRequest(
		t,
		rawToken,
		"/services/heartbeat",
		string(body),
	)
	if response.Code != http.StatusAccepted {
		t.Fatalf(
			"rotation heartbeat status=%d body=%s",
			response.Code,
			response.Body.String(),
		)
	}
}

func runtimeTokenRotationHeartbeatProofBody(
	t *testing.T,
	fixture runtimeTokenRotationHTTPFixture,
	rotation runtimeTokenRotationResponse,
) string {
	t.Helper()
	body, err := json.Marshal(map[string]any{
		"expected_revision":              rotation.Revision,
		"agent_version":                  "v2.0.0",
		"agent_protocol_version":         2,
		"executor_version":               "v2.0.0",
		"executor_protocol_version":      1,
		"mutation_protocol_version":      1,
		"ownership_epoch":                rotation.ExpectedOwnershipEpoch,
		"source_policy_revision":         rotation.ExpectedSourcePolicyRevision,
		"projection_revision":            rotation.ExpectedProjectionRevision,
		"local_executor_policy_revision": rotation.ExpectedLocalExecutorPolicyRevision,
		"local_executor_policy_sha256":   fixture.policy.LocalExecutorPolicySHA256,
		"local_stage_receipt_id":         rotation.LocalStageReceiptID,
		"local_phase":                    "staged_token_active",
	})
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

func (f runtimeTokenRotationHTTPFixture) adminRequest(
	t *testing.T,
	method string,
	path string,
	body string,
) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.AddCookie(f.cookie)
	if method != http.MethodGet {
		request.Header.Set("X-CSRF-Token", f.csrf)
	}
	response := httptest.NewRecorder()
	f.handler.ServeHTTP(response, request)
	return response
}

func (f runtimeTokenRotationHTTPFixture) serviceRequest(
	t *testing.T,
	rawToken string,
	path string,
	body string,
) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	request.Header.Set("Authorization", "Bearer "+rawToken)
	response := httptest.NewRecorder()
	f.handler.ServeHTTP(response, request)
	return response
}

func (f runtimeTokenRotationHTTPFixture) publicRequest(
	t *testing.T,
	method string,
	path string,
	body string,
) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	response := httptest.NewRecorder()
	f.handler.ServeHTTP(response, request)
	return response
}

func assertRuntimeTokenRotationNoStore(
	t *testing.T,
	response *httptest.ResponseRecorder,
) {
	t.Helper()
	if response.Header().Get("Cache-Control") != "no-store" ||
		response.Header().Get("Pragma") != "no-cache" ||
		response.Header().Get("Referrer-Policy") != "no-referrer" {
		t.Fatalf("unsafe response headers: %#v", response.Header())
	}
}
