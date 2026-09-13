package httpapi

import (
	"encoding/json"
	"github.com/example/autostream-control-panel/internal/store"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

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
