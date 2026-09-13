package httpapi

import (
	"encoding/json"
	"github.com/example/autostream-control-panel/internal/security"
	"github.com/example/autostream-control-panel/internal/updateradapter"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestUpdaterIdentityMutationsRejectActiveSystemUpdate(t *testing.T) {
	t.Run("generic updater token rotation", func(t *testing.T) {
		fixture := newUpdaterIdentityMutationFixture(t, false, false)
		fixture.createActiveSystemUpdateJob(t, "generic-rotate")
		before, err := fixture.auth.GetService(t.Context(), fixture.serviceID)
		if err != nil {
			t.Fatal(err)
		}

		result := fixture.adminRequest(
			t,
			http.MethodPost,
			"/api-tokens/"+before.TokenID+"/rotate",
			"",
		)
		assertUpdaterSystemUpdateMutationConflict(t, result)
		after, err := fixture.auth.GetService(t.Context(), fixture.serviceID)
		if err != nil {
			t.Fatal(err)
		}
		if after.TokenID != before.TokenID {
			t.Fatalf("active system update allowed generic token rotation: before=%#v after=%#v", before, after)
		}
	})

	t.Run("generic updater token revoke", func(t *testing.T) {
		fixture := newUpdaterIdentityMutationFixture(t, false, false)
		fixture.createActiveSystemUpdateJob(t, "generic-revoke")

		result := fixture.adminRequest(
			t,
			http.MethodDelete,
			"/api-tokens/"+fixture.initialToken.ID,
			"",
		)
		assertUpdaterSystemUpdateMutationConflict(t, result)
		if _, err := fixture.auth.AuthenticateServiceToken(
			t.Context(),
			fixture.initialToken.RawToken,
			"updates.claim",
		); err != nil {
			t.Fatalf("active system update allowed updater token revoke: %v", err)
		}
	})

	t.Run("runtime token rotation", func(t *testing.T) {
		fixture := newUpdaterIdentityMutationFixture(t, true, false)
		fixture.createActiveSystemUpdateJob(t, "runtime-rotate")
		before, err := fixture.auth.GetService(t.Context(), fixture.serviceID)
		if err != nil {
			t.Fatal(err)
		}

		result := fixture.adminRequest(
			t,
			http.MethodPost,
			"/nodes/"+fixture.serviceID+"/rotate-token",
			"",
		)
		assertUpdaterSystemUpdateMutationConflict(t, result)
		after, err := fixture.auth.GetService(t.Context(), fixture.serviceID)
		if err != nil {
			t.Fatal(err)
		}
		if after.TokenID != before.TokenID ||
			after.NodeTokenCiphertext != before.NodeTokenCiphertext {
			t.Fatalf("active system update allowed runtime token rotation: before=%#v after=%#v", before, after)
		}
	})

	t.Run("staged configuration activation", func(t *testing.T) {
		fixture := newUpdaterIdentityMutationFixture(t, true, true)
		fixture.createActiveSystemUpdateJob(t, "configuration-activate")
		before, err := fixture.auth.GetService(t.Context(), fixture.serviceID)
		if err != nil {
			t.Fatal(err)
		}

		result := fixture.publicRequest(
			t,
			http.MethodPost,
			"/services/host-agent/runtime-identity/activate",
			fixture.activationRequestBody(t),
		)
		assertUpdaterSystemUpdateMutationConflict(t, result)
		after, err := fixture.auth.GetService(t.Context(), fixture.serviceID)
		if err != nil {
			t.Fatal(err)
		}
		if after.TokenID != before.TokenID ||
			after.StagedNodeTokenID != before.StagedNodeTokenID {
			t.Fatalf("active system update allowed staged activation: before=%#v after=%#v", before, after)
		}
	})
}

func TestUpdaterTokenRevokeClearsBootstrapRuntimeReadiness(t *testing.T) {
	fixture := newBootstrapIdentityReadinessFixture(t)
	result := fixture.adminRequest(
		t,
		http.MethodDelete,
		"/api-tokens/"+fixture.runtimeToken.ID,
		"",
	)
	if result.Code != http.StatusOK {
		t.Fatalf("updater token revoke status=%d body=%s", result.Code, result.Body.String())
	}
	service, err := fixture.auth.GetService(t.Context(), fixture.serviceID)
	if err != nil {
		t.Fatal(err)
	}
	if service.LastHeartbeatAt != nil || len(service.ReportedCapabilities) != 0 {
		t.Fatalf("updater token revoke retained runtime readiness: %#v", service)
	}
	result = fixture.createBootstrapRequest(t, "revoked-runtime-token")
	if result.Code != http.StatusConflict ||
		!strings.Contains(result.Body.String(), `"code":"updater_offline"`) {
		t.Fatalf("revoked updater authorized bootstrap create: status=%d body=%s", result.Code, result.Body.String())
	}
}

func TestUpdaterConfigureStageRejectsEveryActiveBootstrapStateAndResumesAfterTerminal(t *testing.T) {
	for _, state := range []UpdateHostBootstrapStatus{
		UpdateHostBootstrapStatusQueued,
		UpdateHostBootstrapStatusClaimed,
		UpdateHostBootstrapStatusRunning,
	} {
		t.Run(string(state), func(t *testing.T) {
			fixture := newUpdaterIdentityMutationFixture(t, true, false)
			rawConfigureToken := "configure-during-" + string(state)
			if _, err := fixture.auth.SetServiceConfigureToken(
				t.Context(),
				fixture.serviceID,
				security.HashToken(rawConfigureToken),
				time.Now().UTC().Add(time.Hour),
			); err != nil {
				t.Fatal(err)
			}
			job := fixture.createBootstrapJob(t, state)
			payload, err := json.Marshal(map[string]any{
				"nodeId":          fixture.serviceID,
				"configureToken":  rawConfigureToken,
				"protocolVersion": updateradapter.HostAgentConfigureProtocolVersion,
				"agentUid":        updaterIdentityFixtureAgentUID,
				"agentGid":        updaterIdentityFixtureAgentGID,
			})
			if err != nil {
				t.Fatal(err)
			}

			result := fixture.publicRequest(
				t,
				http.MethodPost,
				"/services/host-agent/runtime-identity/stage",
				string(payload),
			)
			assertUpdaterBootstrapMutationConflict(t, result)
			blocked, err := fixture.auth.GetService(t.Context(), fixture.serviceID)
			if err != nil {
				t.Fatal(err)
			}
			if blocked.StagedNodeTokenID != "" || blocked.ConfigureTokenUsedAt != nil {
				t.Fatalf("active bootstrap stage mutated updater identity: %#v", blocked)
			}

			fixture.cancelBootstrapJob(t, job)
			result = fixture.publicRequest(
				t,
				http.MethodPost,
				"/services/host-agent/runtime-identity/stage",
				string(payload),
			)
			if result.Code != http.StatusOK {
				t.Fatalf(
					"terminal bootstrap configure stage status=%d body=%s",
					result.Code,
					result.Body.String(),
				)
			}
			staged, err := fixture.auth.GetService(t.Context(), fixture.serviceID)
			if err != nil {
				t.Fatal(err)
			}
			if staged.StagedNodeTokenID == "" || staged.ConfigureTokenUsedAt == nil {
				t.Fatalf("terminal bootstrap configure stage did not create staged identity: %#v", staged)
			}
		})
	}
}

func TestUpdaterConfigureTokenRegenerationRetainsPendingTombstoneAndInvalidatesOldStage(t *testing.T) {
	fixture := newUpdaterIdentityMutationFixture(t, true, true)
	before, err := fixture.auth.GetService(t.Context(), fixture.serviceID)
	if err != nil {
		t.Fatal(err)
	}

	result := fixture.adminRequest(
		t,
		http.MethodPost,
		"/nodes/"+fixture.serviceID+"/configure-token",
		"",
	)
	if result.Code != http.StatusCreated ||
		!strings.Contains(result.Body.String(), `"configure_token":"`) {
		t.Fatalf("pending configuration token regeneration status=%d body=%s", result.Code, result.Body.String())
	}
	after, err := fixture.auth.GetService(t.Context(), fixture.serviceID)
	if err != nil {
		t.Fatal(err)
	}
	if after.StagedNodeTokenID != before.StagedNodeTokenID ||
		after.StagedNodePreviousTokenID != "" ||
		after.StagedNodeTokenHash != "" ||
		len(after.StagedNodeTokenScopes) != 0 ||
		after.StagedNodeTokenCiphertext != "" ||
		after.StagedNodeTokenNonce != "" ||
		after.StagedNodeActivationTokenHash != "" ||
		after.StagedNodeTokenAt != nil ||
		after.ConfigureTokenHash == before.ConfigureTokenHash ||
		after.ConfigureTokenUsedAt != nil {
		t.Fatalf("configure-token regeneration did not retain a secret-free pending tombstone: before=%#v after=%#v", before, after)
	}
	oldActivation := fixture.publicRequest(
		t,
		http.MethodPost,
		"/services/host-agent/runtime-identity/activate",
		fixture.activationRequestBody(t),
	)
	if oldActivation.Code != http.StatusUnauthorized ||
		!strings.Contains(oldActivation.Body.String(), `"code":"invalid_activation_token"`) {
		t.Fatalf("regenerated configure token left old activation usable: status=%d body=%s", oldActivation.Code, oldActivation.Body.String())
	}
}

func TestExpiredUpdaterConfigurationRemainsPendingForBootstrapAndRegeneration(t *testing.T) {
	fixture := newBootstrapIdentityReadinessFixture(t)
	const configureToken = "configure-expired-pending"
	expiresAt := time.Now().UTC().Add(150 * time.Millisecond)
	if _, err := fixture.auth.SetServiceConfigureToken(
		t.Context(),
		fixture.serviceID,
		security.HashToken(configureToken),
		expiresAt,
	); err != nil {
		t.Fatal(err)
	}
	stagePayload, err := json.Marshal(map[string]any{
		"nodeId":          fixture.serviceID,
		"configureToken":  configureToken,
		"protocolVersion": updateradapter.HostAgentConfigureProtocolVersion,
		"agentUid":        updaterIdentityFixtureAgentUID,
		"agentGid":        updaterIdentityFixtureAgentGID,
	})
	if err != nil {
		t.Fatal(err)
	}
	stage := fixture.publicRequest(
		t,
		http.MethodPost,
		"/services/host-agent/runtime-identity/stage",
		string(stagePayload),
	)
	if stage.Code != http.StatusOK {
		t.Fatalf("stage expiring updater configuration status=%d body=%s", stage.Code, stage.Body.String())
	}
	if wait := time.Until(expiresAt) + 25*time.Millisecond; wait > 0 {
		time.Sleep(wait)
	}

	create := fixture.createBootstrapRequest(t, "expired-pending-configuration")
	if create.Code != http.StatusConflict ||
		!strings.Contains(create.Body.String(), `"code":"updater_configuration_pending"`) {
		t.Fatalf("expired staged configuration bootstrap status=%d body=%s", create.Code, create.Body.String())
	}
	jobs, err := fixture.server.updateHostBootstrapJobs.List(fixture.serviceID)
	if err != nil || len(jobs) != 0 {
		t.Fatalf("expired staged configuration queued bootstrap jobs=%#v err=%v", jobs, err)
	}

	before, err := fixture.auth.GetService(t.Context(), fixture.serviceID)
	if err != nil {
		t.Fatal(err)
	}
	regenerate := fixture.adminRequest(
		t,
		http.MethodPost,
		"/nodes/"+fixture.serviceID+"/configure-token",
		"",
	)
	if regenerate.Code != http.StatusCreated ||
		!strings.Contains(regenerate.Body.String(), `"configure_token":"`) {
		t.Fatalf("expired pending configure-token regeneration status=%d body=%s", regenerate.Code, regenerate.Body.String())
	}
	after, err := fixture.auth.GetService(t.Context(), fixture.serviceID)
	if err != nil {
		t.Fatal(err)
	}
	if after.StagedNodeTokenID != before.StagedNodeTokenID ||
		after.StagedNodeActivationTokenHash != "" ||
		after.StagedNodeTokenHash != "" ||
		after.StagedNodeTokenCiphertext != "" ||
		after.StagedNodeTokenNonce != "" {
		t.Fatalf("expired pending configuration did not become a secret-free tombstone: before=%#v after=%#v", before, after)
	}
	create = fixture.createBootstrapRequest(t, "expired-pending-configuration")
	if create.Code != http.StatusConflict ||
		!strings.Contains(create.Body.String(), `"code":"updater_configuration_pending"`) {
		t.Fatalf("regenerated expired stage unblocked bootstrap: status=%d body=%s", create.Code, create.Body.String())
	}
}
