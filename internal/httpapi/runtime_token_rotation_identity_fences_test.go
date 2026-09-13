package httpapi

import (
	"encoding/json"
	"errors"
	"github.com/example/autostream-control-panel/internal/security"
	"github.com/example/autostream-control-panel/internal/store"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

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
