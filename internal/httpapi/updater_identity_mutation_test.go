package httpapi

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/example/autostream-control-panel/internal/security"
	"github.com/example/autostream-control-panel/internal/store"
	"github.com/example/autostream-control-panel/internal/updateradapter"
)

const (
	updaterIdentityFixtureAgentUID = uint32(1001)
	updaterIdentityFixtureAgentGID = uint32(1002)
)

func TestUpdateAgentRegistrationSerializesWithBootstrapIdentityChecks(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	updaterToken := registerUpdateAgentForPolicyTest(t, auth, "updater-serialized-register")
	workerToken, err := auth.CreateServiceToken(
		t.Context(),
		"worker",
		[]string{"service.register", "service.heartbeat"},
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := auth.PrecreateService(t.Context(), workerToken, store.ServiceRegistration{
		ServiceID:   "worker-unserialized-register",
		ServiceType: "worker",
		ServiceName: "Worker",
		PublicURL:   "https://worker.example.com",
		Version:     "v1.0.0",
	}); err != nil {
		t.Fatal(err)
	}
	server := NewServer(
		store.NewMemoryStreamStore(),
		WithAuthStore(auth),
		WithServiceRegistryStore(auth),
	)

	server.systemUpdateOperationMu.Lock()
	locked := true
	defer func() {
		if locked {
			server.systemUpdateOperationMu.Unlock()
		}
	}()

	updaterDone := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		request := httptest.NewRequest(
			http.MethodPost,
			"/services/register",
			strings.NewReader(`{"service_id":"updater-serialized-register","service_type":"update_agent","service_name":"Updater","version":"v1.0.1"}`),
		)
		request.Header.Set("Authorization", "Bearer "+updaterToken.RawToken)
		result := httptest.NewRecorder()
		server.ServeHTTP(result, request)
		updaterDone <- result
	}()

	select {
	case result := <-updaterDone:
		t.Fatalf(
			"update-agent registration bypassed bootstrap identity lock: status=%d body=%s",
			result.Code,
			result.Body.String(),
		)
	case <-time.After(50 * time.Millisecond):
	}

	workerDone := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		request := httptest.NewRequest(
			http.MethodPost,
			"/services/register",
			strings.NewReader(`{"service_id":"worker-unserialized-register","service_type":"worker","service_name":"Worker","public_url":"https://worker.example.com","version":"v1.0.0"}`),
		)
		request.Header.Set("Authorization", "Bearer "+workerToken.RawToken)
		result := httptest.NewRecorder()
		server.ServeHTTP(result, request)
		workerDone <- result
	}()
	select {
	case result := <-workerDone:
		if result.Code != http.StatusAccepted {
			t.Fatalf("normal service registration status=%d body=%s", result.Code, result.Body.String())
		}
	case <-time.After(time.Second):
		t.Fatal("normal service registration was serialized by the update-agent identity lock")
	}

	server.systemUpdateOperationMu.Unlock()
	locked = false
	select {
	case result := <-updaterDone:
		if result.Code != http.StatusAccepted {
			t.Fatalf("serialized update-agent registration status=%d body=%s", result.Code, result.Body.String())
		}
	case <-time.After(time.Second):
		t.Fatal("serialized update-agent registration did not resume after identity lock release")
	}
}

func TestOldNodeAgentRuntimeIdentityRoutesAreAbsent(t *testing.T) {
	server := NewServer(store.NewMemoryStreamStore())
	for _, path := range []string{
		"/api/node-agent/configure",
		"/api/node-agent/configure/stage",
		"/api/node-agent/configure/activate",
	} {
		t.Run(path, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{}`))
			result := httptest.NewRecorder()
			server.ServeHTTP(result, request)
			if result.Code != http.StatusNotFound {
				t.Fatalf("removed legacy runtime identity route %s returned status=%d body=%s", path, result.Code, result.Body.String())
			}
		})
	}
}

func TestUpdaterIdentityMutationsRejectActiveBootstrapAndResumeAfterTerminal(t *testing.T) {
	t.Run("queued service deletion", func(t *testing.T) {
		fixture := newUpdaterIdentityMutationFixture(t, false, false)
		job := fixture.createBootstrapJob(t, UpdateHostBootstrapStatusQueued)

		result := fixture.adminRequest(t, http.MethodDelete, "/services/"+fixture.serviceID, "")
		assertUpdaterBootstrapMutationConflict(t, result)
		if _, err := fixture.auth.GetService(t.Context(), fixture.serviceID); err != nil {
			t.Fatalf("active bootstrap service deletion mutated updater: %v", err)
		}

		fixture.cancelBootstrapJob(t, job)
		result = fixture.adminRequest(t, http.MethodDelete, "/services/"+fixture.serviceID, "")
		if result.Code != http.StatusOK {
			t.Fatalf("terminal bootstrap service deletion status=%d body=%s", result.Code, result.Body.String())
		}
	})

	t.Run("claimed runtime token rotation", func(t *testing.T) {
		fixture := newUpdaterIdentityMutationFixture(t, true, false)
		job := fixture.createBootstrapJob(t, UpdateHostBootstrapStatusClaimed)
		before, err := fixture.auth.GetService(t.Context(), fixture.serviceID)
		if err != nil {
			t.Fatal(err)
		}

		result := fixture.adminRequest(t, http.MethodPost, "/nodes/"+fixture.serviceID+"/rotate-token", "")
		assertUpdaterBootstrapMutationConflict(t, result)
		after, err := fixture.auth.GetService(t.Context(), fixture.serviceID)
		if err != nil {
			t.Fatal(err)
		}
		if after.TokenID != before.TokenID || after.NodeTokenCiphertext != before.NodeTokenCiphertext {
			t.Fatalf("active bootstrap runtime token rotation mutated updater: before=%#v after=%#v", before, after)
		}

		fixture.cancelBootstrapJob(t, job)
		fixture.rotateRuntimeIdentity(t)
		rotated, err := fixture.auth.GetService(t.Context(), fixture.serviceID)
		if err != nil {
			t.Fatal(err)
		}
		if rotated.TokenID == before.TokenID {
			t.Fatalf("terminal bootstrap runtime token rotation did not update token: %#v", rotated)
		}
	})

	t.Run("running staged configuration activation", func(t *testing.T) {
		fixture := newUpdaterIdentityMutationFixture(t, true, true)
		job := fixture.createBootstrapJob(t, UpdateHostBootstrapStatusRunning)
		before, err := fixture.auth.GetService(t.Context(), fixture.serviceID)
		if err != nil {
			t.Fatal(err)
		}
		body := fixture.activationRequestBody(t)

		invalid := strings.Replace(body, fixture.activationToken, "ast_act_invalid", 1)
		invalidResult := fixture.publicRequest(
			t,
			http.MethodPost,
			"/services/host-agent/runtime-identity/activate",
			invalid,
		)
		if invalidResult.Code != http.StatusUnauthorized ||
			!strings.Contains(invalidResult.Body.String(), `"code":"invalid_activation_token"`) {
			t.Fatalf(
				"invalid activation token exposed active bootstrap state: status=%d body=%s",
				invalidResult.Code,
				invalidResult.Body.String(),
			)
		}

		result := fixture.publicRequest(t, http.MethodPost, "/services/host-agent/runtime-identity/activate", body)
		assertUpdaterBootstrapMutationConflict(t, result)
		after, err := fixture.auth.GetService(t.Context(), fixture.serviceID)
		if err != nil {
			t.Fatal(err)
		}
		if after.TokenID != before.TokenID || after.StagedNodeTokenID != before.StagedNodeTokenID {
			t.Fatalf("active bootstrap configuration activation mutated updater: before=%#v after=%#v", before, after)
		}

		fixture.cancelBootstrapJob(t, job)
		result = fixture.publicRequest(t, http.MethodPost, "/services/host-agent/runtime-identity/activate", body)
		if result.Code != http.StatusOK {
			t.Fatalf("terminal bootstrap configuration activation status=%d body=%s", result.Code, result.Body.String())
		}
		activated, err := fixture.auth.GetService(t.Context(), fixture.serviceID)
		if err != nil {
			t.Fatal(err)
		}
		if activated.TokenID != before.StagedNodeTokenID {
			t.Fatalf("terminal bootstrap configuration activation did not bind staged token: %#v", activated)
		}
	})

	t.Run("queued generic updater token rotation", func(t *testing.T) {
		fixture := newUpdaterIdentityMutationFixture(t, false, false)
		job := fixture.createBootstrapJob(t, UpdateHostBootstrapStatusQueued)
		before, err := fixture.auth.GetService(t.Context(), fixture.serviceID)
		if err != nil {
			t.Fatal(err)
		}

		result := fixture.adminRequest(t, http.MethodPost, "/api-tokens/"+before.TokenID+"/rotate", "")
		assertUpdaterBootstrapMutationConflict(t, result)
		after, err := fixture.auth.GetService(t.Context(), fixture.serviceID)
		if err != nil {
			t.Fatal(err)
		}
		if after.TokenID != before.TokenID {
			t.Fatalf("active bootstrap generic token rotation mutated updater: before=%#v after=%#v", before, after)
		}

		fixture.cancelBootstrapJob(t, job)
		result = fixture.adminRequest(t, http.MethodPost, "/api-tokens/"+before.TokenID+"/rotate", "")
		if result.Code != http.StatusCreated {
			t.Fatalf("terminal bootstrap generic token rotation status=%d body=%s", result.Code, result.Body.String())
		}
	})

	t.Run("claimed generic updater token revoke", func(t *testing.T) {
		fixture := newUpdaterIdentityMutationFixture(t, false, false)
		job := fixture.createBootstrapJob(t, UpdateHostBootstrapStatusClaimed)
		before, err := fixture.auth.GetService(t.Context(), fixture.serviceID)
		if err != nil {
			t.Fatal(err)
		}

		result := fixture.adminRequest(t, http.MethodDelete, "/api-tokens/"+before.TokenID, "")
		assertUpdaterBootstrapMutationConflict(t, result)
		if _, err := fixture.auth.AuthenticateServiceToken(
			t.Context(),
			fixture.initialToken.RawToken,
			"updates.claim",
		); err != nil {
			t.Fatalf("active bootstrap generic revoke invalidated updater token: %v", err)
		}

		fixture.cancelBootstrapJob(t, job)
		result = fixture.adminRequest(t, http.MethodDelete, "/api-tokens/"+before.TokenID, "")
		if result.Code != http.StatusOK {
			t.Fatalf("terminal bootstrap generic token revoke status=%d body=%s", result.Code, result.Body.String())
		}
	})

	t.Run("queued configure token regeneration", func(t *testing.T) {
		fixture := newUpdaterIdentityMutationFixture(t, true, false)
		job := fixture.createBootstrapJob(t, UpdateHostBootstrapStatusQueued)
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
		assertUpdaterBootstrapMutationConflict(t, result)
		after, err := fixture.auth.GetService(t.Context(), fixture.serviceID)
		if err != nil {
			t.Fatal(err)
		}
		if after.ConfigureTokenHash != before.ConfigureTokenHash ||
			after.ConfigureTokenExpiresAt != before.ConfigureTokenExpiresAt {
			t.Fatalf("active bootstrap configure-token regeneration mutated updater: before=%#v after=%#v", before, after)
		}

		fixture.cancelBootstrapJob(t, job)
		result = fixture.adminRequest(
			t,
			http.MethodPost,
			"/nodes/"+fixture.serviceID+"/configure-token",
			"",
		)
		if result.Code != http.StatusCreated {
			t.Fatalf("terminal bootstrap configure-token regeneration status=%d body=%s", result.Code, result.Body.String())
		}
	})

	t.Run("case-aliased updater token revoke", func(t *testing.T) {
		var services *caseInsensitiveTokenRevokeStore
		fixture := newUpdaterIdentityMutationFixtureWithServiceStore(
			t,
			false,
			false,
			func(auth *store.MemoryAuthStore) store.ServiceRegistryStore {
				services = &caseInsensitiveTokenRevokeStore{ServiceRegistryStore: auth}
				return services
			},
		)
		fixture.createBootstrapJob(t, UpdateHostBootstrapStatusQueued)
		aliasTokenID := strings.ToUpper(fixture.initialToken.ID)
		if aliasTokenID == fixture.initialToken.ID {
			t.Fatalf("generated token ID has no case-distinct alias: %q", aliasTokenID)
		}

		result := fixture.adminRequest(
			t,
			http.MethodDelete,
			"/api-tokens/"+aliasTokenID,
			"",
		)
		if result.Code != http.StatusNotFound ||
			!strings.Contains(result.Body.String(), `"code":"not_found"`) {
			t.Fatalf("case-aliased updater revoke status=%d body=%s", result.Code, result.Body.String())
		}
		if services.revokeCalled {
			t.Fatal("case-aliased token ID reached the case-insensitive revoke store")
		}
		if _, err := fixture.auth.AuthenticateServiceToken(
			t.Context(),
			fixture.initialToken.RawToken,
			"updates.claim",
		); err != nil {
			t.Fatalf("case-aliased revoke invalidated canonical updater token: %v", err)
		}
	})
}

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

func TestBootstrapCreateRequiresSettledUpdaterIdentityAndFreshTokenHeartbeat(t *testing.T) {
	t.Run("runtime token rotation", func(t *testing.T) {
		fixture := newBootstrapIdentityReadinessFixture(t)
		before, err := fixture.auth.GetService(t.Context(), fixture.serviceID)
		if err != nil {
			t.Fatal(err)
		}

		rotatedRuntimeToken := fixture.rotateRuntimeIdentity(t)
		registerPayload, err := json.Marshal(store.ServiceRegistration{
			ServiceID:   fixture.serviceID,
			ServiceType: "update_agent",
			ServiceName: "Updater",
			Version:     "v1.0.1",
		})
		if err != nil {
			t.Fatal(err)
		}
		registerRequest := httptest.NewRequest(
			http.MethodPost,
			"/services/register",
			bytes.NewReader(registerPayload),
		)
		registerRequest.Header.Set("Authorization", "Bearer "+rotatedRuntimeToken)
		register := httptest.NewRecorder()
		fixture.server.ServeHTTP(register, registerRequest)
		if register.Code != http.StatusAccepted {
			t.Fatalf("post-rotation service registration status=%d body=%s", register.Code, register.Body.String())
		}
		after, err := fixture.auth.GetService(t.Context(), fixture.serviceID)
		if err != nil {
			t.Fatal(err)
		}
		if after.NodeTokenRotatedAt == nil ||
			before.LastHeartbeatAt == nil ||
			after.NodeTokenRotatedAt.Before(*before.LastHeartbeatAt) {
			t.Fatalf("rotation timestamps do not establish a new identity generation: before=%#v after=%#v", before, after)
		}

		create := fixture.createBootstrapRequest(t, "post-runtime-rotation")
		if create.Code != http.StatusConflict ||
			!strings.Contains(create.Body.String(), `"code":"updater_offline"`) {
			t.Fatalf(
				"pre-rotation heartbeat authorized bootstrap: status=%d body=%s",
				create.Code,
				create.Body.String(),
			)
		}

		fixture.heartbeatAfterRotation(t, rotatedRuntimeToken)
		create = fixture.createBootstrapRequest(t, "post-runtime-rotation")
		if create.Code != http.StatusAccepted {
			t.Fatalf(
				"new-token heartbeat did not restore bootstrap readiness: status=%d body=%s",
				create.Code,
				create.Body.String(),
			)
		}
	})

	t.Run("staged configuration activation", func(t *testing.T) {
		fixture := newBootstrapIdentityReadinessFixture(t)
		rawConfigureToken := "configure-bootstrap-readiness"
		if _, err := fixture.auth.SetServiceConfigureToken(
			t.Context(),
			fixture.serviceID,
			security.HashToken(rawConfigureToken),
			time.Now().UTC().Add(time.Hour),
		); err != nil {
			t.Fatal(err)
		}
		stageBody, err := json.Marshal(map[string]any{
			"nodeId":          fixture.serviceID,
			"configureToken":  rawConfigureToken,
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
			string(stageBody),
		)
		if stage.Code != http.StatusOK {
			t.Fatalf("configuration stage status=%d body=%s", stage.Code, stage.Body.String())
		}
		var staged updateradapter.UpdaterStagedConfiguration
		if err := json.NewDecoder(stage.Body).Decode(&staged); err != nil {
			t.Fatal(err)
		}
		if staged.ConfigurationID == "" ||
			staged.ActivationToken == "" ||
			staged.Config.RuntimeToken == "" ||
			staged.LocalExecutorPolicy == nil {
			t.Fatalf("configuration stage response incomplete: %#v", staged)
		}

		create := fixture.createBootstrapRequest(t, "pending-configuration")
		if create.Code != http.StatusConflict ||
			!strings.Contains(create.Body.String(), `"code":"updater_configuration_pending"`) {
			t.Fatalf(
				"pending updater configuration authorized bootstrap: status=%d body=%s",
				create.Code,
				create.Body.String(),
			)
		}

		activateBody, err := json.Marshal(hostAgentConfigureActivationPayload(
			staged,
			updaterIdentityFixtureAgentUID,
			updaterIdentityFixtureAgentGID,
			*staged.LocalExecutorPolicy,
		))
		if err != nil {
			t.Fatal(err)
		}
		activate := fixture.publicRequest(
			t,
			http.MethodPost,
			"/services/host-agent/runtime-identity/activate",
			string(activateBody),
		)
		if activate.Code != http.StatusOK {
			t.Fatalf("configuration activation status=%d body=%s", activate.Code, activate.Body.String())
		}

		create = fixture.createBootstrapRequest(t, "pending-configuration")
		if create.Code != http.StatusConflict ||
			!strings.Contains(create.Body.String(), `"code":"updater_offline"`) {
			t.Fatalf(
				"pre-activation heartbeat authorized bootstrap: status=%d body=%s",
				create.Code,
				create.Body.String(),
			)
		}

		fixture.heartbeatAfterRotation(t, staged.Config.RuntimeToken)
		create = fixture.createBootstrapRequest(t, "pending-configuration")
		if create.Code != http.StatusAccepted {
			t.Fatalf(
				"activated-token heartbeat did not restore bootstrap readiness: status=%d body=%s",
				create.Code,
				create.Body.String(),
			)
		}
	})
}

func TestBootstrapCreateRequiresCurrentRuntimeReportedEncryptionIdentity(t *testing.T) {
	fixture := newBootstrapIdentityReadinessFixture(t)
	rotatedRuntimeToken := fixture.rotateRuntimeIdentity(t)
	registerPayload, err := json.Marshal(store.ServiceRegistration{
		ServiceID:   fixture.serviceID,
		ServiceType: "update_agent",
		ServiceName: "Updater",
		Version:     "v1.0.1",
	})
	if err != nil {
		t.Fatal(err)
	}
	registerRequest := httptest.NewRequest(
		http.MethodPost,
		"/services/register",
		bytes.NewReader(registerPayload),
	)
	registerRequest.Header.Set("Authorization", "Bearer "+rotatedRuntimeToken)
	register := httptest.NewRecorder()
	fixture.server.ServeHTTP(register, registerRequest)
	if register.Code != http.StatusAccepted {
		t.Fatalf("post-rotation registration status=%d body=%s", register.Code, register.Body.String())
	}

	reportedWithoutEncryptionIdentity := make(map[string]any, len(fixture.capabilities))
	for key, value := range fixture.capabilities {
		reportedWithoutEncryptionIdentity[key] = value
	}
	delete(reportedWithoutEncryptionIdentity, "bootstrap_encryption_public_key")
	delete(reportedWithoutEncryptionIdentity, "bootstrap_encryption_key_fingerprint")
	delete(reportedWithoutEncryptionIdentity, "bootstrap_encryption_public_key_fingerprint")
	heartbeatPayload, err := json.Marshal(store.ServiceHeartbeat{
		ServiceID:    fixture.serviceID,
		Status:       "online",
		Version:      "v1.0.1",
		Capabilities: reportedWithoutEncryptionIdentity,
	})
	if err != nil {
		t.Fatal(err)
	}
	heartbeatRequest := httptest.NewRequest(
		http.MethodPost,
		"/services/heartbeat",
		bytes.NewReader(heartbeatPayload),
	)
	heartbeatRequest.Header.Set("Authorization", "Bearer "+rotatedRuntimeToken)
	heartbeat := httptest.NewRecorder()
	fixture.server.ServeHTTP(heartbeat, heartbeatRequest)
	if heartbeat.Code != http.StatusAccepted {
		t.Fatalf("post-rotation heartbeat status=%d body=%s", heartbeat.Code, heartbeat.Body.String())
	}
	service, err := fixture.auth.GetService(t.Context(), fixture.serviceID)
	if err != nil {
		t.Fatal(err)
	}
	if capabilityString(service.Capabilities["bootstrap_encryption_public_key"]) == "" ||
		capabilityString(service.ReportedCapabilities["bootstrap_encryption_public_key"]) != "" {
		t.Fatalf("test fixture did not retain only the stale configured key: %#v", service)
	}

	create := fixture.createBootstrapRequest(t, "missing-current-runtime-encryption-identity")
	if create.Code != http.StatusConflict ||
		!strings.Contains(create.Body.String(), `"code":"bootstrap_encryption_key_unavailable"`) {
		t.Fatalf("stale configured bootstrap key authorized create: status=%d body=%s", create.Code, create.Body.String())
	}
	jobs, err := fixture.server.updateHostBootstrapJobs.List(fixture.serviceID)
	if err != nil || len(jobs) != 0 {
		t.Fatalf("missing current runtime key queued bootstrap jobs=%#v err=%v", jobs, err)
	}
}

func TestSystemUpdateAgentAvailabilityAcceptsSamePrecisionHeartbeatAfterAtomicReset(t *testing.T) {
	t.Setenv("AUTOSTREAM_NODE_HEARTBEAT_OFFLINE_AFTER", "3m")
	now := time.Now().UTC()
	rotatedAt := now.Add(-time.Minute)
	if systemUpdateAgentAvailable(store.RegisteredService{
		Status:             "online",
		NodeTokenRotatedAt: &rotatedAt,
	}, now) {
		t.Fatal("updater without a post-reset heartbeat was treated as current")
	}
	heartbeatAt := rotatedAt
	if !systemUpdateAgentAvailable(store.RegisteredService{
		Status:             "online",
		LastHeartbeatAt:    &heartbeatAt,
		NodeTokenRotatedAt: &rotatedAt,
	}, now) {
		t.Fatal("same-precision post-reset heartbeat was treated as stale")
	}
}

func TestBootstrapCreateRejectsCustomManagedHostUser(t *testing.T) {
	fixture := newBootstrapIdentityReadinessFixture(t)
	policies, ok := fixture.server.updaterPolicies.(*store.MemoryUpdaterPolicyStore)
	if !ok {
		t.Fatalf("unexpected updater policy store %T", fixture.server.updaterPolicies)
	}
	current, err := policies.GetUpdaterPolicy(t.Context(), fixture.serviceID)
	if err != nil {
		t.Fatal(err)
	}
	replacement := current
	replacement.Hosts = append([]store.UpdaterPolicyHost(nil), current.Hosts...)
	replacement.Hosts[0].User = "custom-deploy-user"
	saved, err := policies.SavePullUpdaterPolicy(
		t.Context(),
		store.NewMemorySystemUpdateStore(),
		fixture.serviceID,
		current.Revision,
		0,
		replacement,
	)
	if err != nil {
		t.Fatal(err)
	}
	fixture.policyRevision = saved.Revision
	fixture.capabilities["policy_revision"] = saved.ProjectionRevision
	fixture.capabilities["policy_desired_revision"] = saved.ProjectionRevision
	if _, err := fixture.auth.Heartbeat(t.Context(), fixture.runtimeToken, store.ServiceHeartbeat{
		ServiceID:    fixture.serviceID,
		Status:       "online",
		Version:      "v1.0.0",
		Capabilities: fixture.capabilities,
	}); err != nil {
		t.Fatal(err)
	}

	result := fixture.createBootstrapRequest(t, "custom-managed-user")
	if result.Code != http.StatusConflict ||
		!strings.Contains(result.Body.String(), `"code":"unsupported_bootstrap_profile"`) {
		t.Fatalf("custom managed user bootstrap status=%d body=%s", result.Code, result.Body.String())
	}
	jobs, err := fixture.server.updateHostBootstrapJobs.List(fixture.serviceID)
	if err != nil || len(jobs) != 0 {
		t.Fatalf("custom managed user queued bootstrap jobs=%#v err=%v", jobs, err)
	}
}

func TestMixedCaseUpdaterAliasUsesCanonicalIdentityForBootstrapAndGuards(t *testing.T) {
	t.Setenv("AUTOSTREAM_SECRET_ENCRYPTION_KEY", "test-secret-encryption-key-32-bytes")
	t.Setenv("AUTOSTREAM_PUBLIC_URL", "https://panel.example.com")
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(
		store.User{Username: "mixed-case-admin"},
		"correct horse battery",
		[]string{"system_updates.read", "system_updates.execute", "secrets.update"},
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
	const canonicalServiceID = "Updater-Mixed-Case"
	aliasServiceID := strings.ToLower(canonicalServiceID)
	registerServiceWithTokenForTest(t, auth, token, store.ServiceRegistration{
		ServiceID:       canonicalServiceID,
		ServiceType:     "update_agent",
		ServiceName:     "Mixed Case Updater",
		TransportMode:   store.SystemUpdateTransportPullV2,
		ExecutionHostID: "host-01",
		Version:         "v1.0.0",
		Capabilities: map[string]any{
			"host_agent":   true,
			"observe_only": true,
		},
	})

	policies := store.NewMemoryUpdaterPolicyStore()
	hostKey, _ := ed25519AuthorizedKeyForTest(t, "")
	policy := savePullUpdaterPolicyForHTTPTest(t, policies, canonicalServiceID, updaterPolicyForHTTPTest(hostKey))
	secrets := updaterReleaseTokenSecretStoreForBootstrapTest(t, "github_pat_mixed_case")
	bootstrapPublicKey := p256PublicKeyForBootstrapTest(t)
	recipientFingerprint := bootstrapFingerprintForTest(bootstrapPublicKey)
	capabilities := map[string]any{
		"host_agent":                           true,
		"observe_only":                         true,
		"policy_revision":                      policy.ProjectionRevision,
		"policy_desired_revision":              policy.ProjectionRevision,
		"policy_status":                        "applied",
		"bootstrap_encryption_public_key":      base64.RawURLEncoding.EncodeToString(bootstrapPublicKey),
		"bootstrap_encryption_key_fingerprint": recipientFingerprint,
	}
	if _, err := auth.Heartbeat(t.Context(), token, store.ServiceHeartbeat{
		ServiceID:    canonicalServiceID,
		Status:       "online",
		Version:      "v1.0.0",
		Capabilities: capabilities,
	}); err != nil {
		t.Fatal(err)
	}

	broker := NewUpdateHostBootstrapBroker()
	services := caseInsensitiveServiceLookupStore{ServiceRegistryStore: auth, scrubConfigureTokenHash: true}
	server := NewServer(
		store.NewMemoryStreamStore(),
		WithAuthStore(auth),
		WithAuditStore(auth),
		WithServiceRegistryStore(services),
		WithUpdaterPolicyStore(policies),
		WithSecretStore(secrets),
		WithUpdateHostBootstrapBroker(broker),
	)
	cookie, csrf := loginForTest(t, server, "mixed-case-admin", "correct horse battery")
	adminRequest := func(method, path string, body []byte) *httptest.ResponseRecorder {
		t.Helper()
		request := httptest.NewRequest(method, path, bytes.NewReader(body))
		request.AddCookie(cookie)
		request.Header.Set("X-CSRF-Token", csrf)
		result := httptest.NewRecorder()
		server.ServeHTTP(result, request)
		return result
	}

	createPayload, err := json.Marshal(map[string]any{
		"job_id":                    "7ba7b810-9dad-4f0e-9a58-4aee7cb5560f",
		"idempotency_key":           "mixed-case-create",
		"expected_revision":         policy.Revision,
		"recipient_key_fingerprint": recipientFingerprint,
		"host_ids":                  []string{"host-01"},
		"envelope": map[string]any{
			"version":              1,
			"ephemeral_public_key": base64.RawURLEncoding.EncodeToString(p256PublicKeyForBootstrapTest(t)),
			"nonce":                base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{3}, 12)),
			"ciphertext":           base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{4}, 96)),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	aliasCreate := adminRequest(
		http.MethodPost,
		"https://panel.example.com/system-updates/updaters/"+aliasServiceID+"/bootstrap-jobs",
		createPayload,
	)
	if aliasCreate.Code != http.StatusBadRequest ||
		!strings.Contains(aliasCreate.Body.String(), `"code":"invalid_update_agent"`) {
		t.Fatalf("non-canonical bootstrap create status=%d body=%s", aliasCreate.Code, aliasCreate.Body.String())
	}
	create := adminRequest(
		http.MethodPost,
		"https://panel.example.com/system-updates/updaters/"+canonicalServiceID+"/bootstrap-jobs",
		createPayload,
	)
	if create.Code != http.StatusAccepted {
		t.Fatalf("mixed-case bootstrap create status=%d body=%s", create.Code, create.Body.String())
	}
	var created struct {
		Jobs []UpdateHostBootstrapJob `json:"jobs"`
	}
	if err := json.NewDecoder(create.Body).Decode(&created); err != nil {
		t.Fatal(err)
	}
	if len(created.Jobs) != 1 || created.Jobs[0].UpdaterID != canonicalServiceID {
		t.Fatalf("bootstrap job did not store canonical updater identity: %#v", created.Jobs)
	}

	list := adminRequest(
		http.MethodGet,
		"/system-updates/updaters/"+aliasServiceID+"/bootstrap-jobs",
		nil,
	)
	if list.Code != http.StatusOK ||
		!strings.Contains(list.Body.String(), `"updater_id":"`+canonicalServiceID+`"`) {
		t.Fatalf("mixed-case bootstrap list status=%d body=%s", list.Code, list.Body.String())
	}

	claimPayload, err := json.Marshal(map[string]any{
		"service_id":                aliasServiceID,
		"current_revision":          policy.Revision,
		"recipient_key_fingerprint": recipientFingerprint,
	})
	if err != nil {
		t.Fatal(err)
	}
	claimRequest := httptest.NewRequest(
		http.MethodPost,
		"https://panel.example.com/services/update-agent/bootstrap-jobs/claim",
		bytes.NewReader(claimPayload),
	)
	claimRequest.Header.Set("Authorization", "Bearer "+token.RawToken)
	claim := httptest.NewRecorder()
	server.ServeHTTP(claim, claimRequest)
	if claim.Code != http.StatusOK ||
		!strings.Contains(claim.Body.String(), `"updater_id":"`+canonicalServiceID+`"`) {
		t.Fatalf("mixed-case bootstrap claim status=%d body=%s", claim.Code, claim.Body.String())
	}

	replacement := updaterPolicyForHTTPTest(hostKey)
	policyPayload, err := json.Marshal(map[string]any{
		"expected_revision":            policy.Revision,
		"poll_interval_seconds":        replacement.PollIntervalSeconds,
		"heartbeat_interval_seconds":   replacement.HeartbeatIntervalSeconds,
		"local_executor_policy_sha256": replacement.LocalExecutorPolicySHA256,
		"hosts":                        replacement.Hosts,
		"targets":                      updaterPolicyTargetRequestsForTest(replacement.Targets),
		"github_token":                 "github_pat_mixed_case_changed",
	})
	if err != nil {
		t.Fatal(err)
	}
	policySave := adminRequest(
		http.MethodPut,
		"/system-updates/updaters/"+aliasServiceID+"/settings",
		policyPayload,
	)
	assertUpdaterBootstrapMutationConflict(t, policySave)

	const configureToken = "configure-mixed-case"
	if _, err := auth.SetServiceConfigureToken(
		t.Context(),
		canonicalServiceID,
		security.HashToken(configureToken),
		time.Now().UTC().Add(time.Hour),
	); err != nil {
		t.Fatal(err)
	}
	stageRequest := func(rawToken string) *httptest.ResponseRecorder {
		t.Helper()
		payload, err := json.Marshal(map[string]any{
			"nodeId":          aliasServiceID,
			"configureToken":  rawToken,
			"protocolVersion": updateradapter.HostAgentConfigureProtocolVersion,
			"agentUid":        updaterIdentityFixtureAgentUID,
			"agentGid":        updaterIdentityFixtureAgentGID,
		})
		if err != nil {
			t.Fatal(err)
		}
		request := httptest.NewRequest(
			http.MethodPost,
			"/services/host-agent/runtime-identity/stage",
			bytes.NewReader(payload),
		)
		result := httptest.NewRecorder()
		server.ServeHTTP(result, request)
		return result
	}
	invalidStage := stageRequest("invalid-configure-token")
	if invalidStage.Code != http.StatusUnauthorized ||
		!strings.Contains(invalidStage.Body.String(), `"code":"invalid_configure_token"`) {
		t.Fatalf("invalid mixed-case configure token exposed bootstrap state: status=%d body=%s", invalidStage.Code, invalidStage.Body.String())
	}
	assertUpdaterBootstrapMutationConflict(t, stageRequest(configureToken))
	blockedStage, err := auth.GetService(t.Context(), canonicalServiceID)
	if err != nil {
		t.Fatal(err)
	}
	if blockedStage.StagedNodeTokenID != "" || blockedStage.ConfigureTokenUsedAt != nil {
		t.Fatalf("mixed-case active guard allowed configuration stage: %#v", blockedStage)
	}

	staged := stageUpdaterIdentityConfiguration(t, auth, canonicalServiceID, "configure-activation")
	activationRequest := func(rawToken string) *httptest.ResponseRecorder {
		t.Helper()
		payload, err := json.Marshal(map[string]string{
			"nodeId":          aliasServiceID,
			"configurationId": staged.Token.ID,
			"activationToken": rawToken,
			"version":         "v1.0.1",
		})
		if err != nil {
			t.Fatal(err)
		}
		request := httptest.NewRequest(
			http.MethodPost,
			"/services/host-agent/runtime-identity/activate",
			bytes.NewReader(payload),
		)
		result := httptest.NewRecorder()
		server.ServeHTTP(result, request)
		return result
	}
	invalidActivation := activationRequest("ast_act_invalid")
	if invalidActivation.Code != http.StatusUnauthorized ||
		!strings.Contains(invalidActivation.Body.String(), `"code":"invalid_activation_token"`) {
		t.Fatalf("invalid mixed-case activation token exposed bootstrap state: status=%d body=%s", invalidActivation.Code, invalidActivation.Body.String())
	}
	assertUpdaterBootstrapMutationConflict(t, activationRequest(staged.ActivationToken))
	blockedActivation, err := auth.GetService(t.Context(), canonicalServiceID)
	if err != nil {
		t.Fatal(err)
	}
	if blockedActivation.TokenID == staged.Token.ID {
		t.Fatalf("mixed-case active guard activated staged updater identity: %#v", blockedActivation)
	}
}

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

type bootstrapIdentityReadinessFixture struct {
	auth                  *store.MemoryAuthStore
	server                *Server
	serviceID             string
	runtimeToken          store.ServiceToken
	cookie                *http.Cookie
	csrf                  string
	policyRevision        int64
	capabilities          map[string]any
	recipientFingerprint  string
	ephemeralPublicKeyB64 string
}

func newBootstrapIdentityReadinessFixture(t *testing.T) bootstrapIdentityReadinessFixture {
	t.Helper()
	t.Setenv("AUTOSTREAM_SECRET_ENCRYPTION_KEY", "test-secret-encryption-key-32-bytes")
	t.Setenv("AUTOSTREAM_PUBLIC_URL", "https://panel.example.com")
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(
		store.User{Username: "bootstrap-readiness-admin"},
		"correct horse battery",
		[]string{
			"api_tokens.create",
			"api_tokens.revoke",
			"system_updates.read",
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
	const serviceID = "updater-bootstrap-readiness"
	bootstrapPublicKey := p256PublicKeyForBootstrapTest(t)
	registerServiceWithTokenForTest(t, auth, token, store.ServiceRegistration{
		ServiceID:       serviceID,
		ServiceType:     "update_agent",
		ServiceName:     "Updater",
		TransportMode:   store.SystemUpdateTransportPullV2,
		ExecutionHostID: "host-01",
		Version:         "v1.0.0",
		Capabilities: map[string]any{
			"bootstrap_encryption_public_key":      base64.RawURLEncoding.EncodeToString(bootstrapPublicKey),
			"bootstrap_encryption_key_fingerprint": bootstrapFingerprintForTest(bootstrapPublicKey),
			"host_agent":                           true,
			"observe_only":                         true,
		},
	})

	workerToken, err := auth.CreateServiceToken(
		t.Context(),
		"worker",
		[]string{"service.register", "service.heartbeat"},
	)
	if err != nil {
		t.Fatal(err)
	}
	registerServiceWithTokenForTest(t, auth, workerToken, store.ServiceRegistration{
		ServiceID:   "worker-01",
		ServiceType: "worker",
		ServiceName: "Worker 01",
		Host:        "worker-01.example.com",
		Port:        8084,
		SSLEnabled:  true,
		PublicURL:   "https://worker-01.example.com:8084",
		Version:     "v1.0.0",
	})

	policies := store.NewMemoryUpdaterPolicyStore()
	updates := store.NewMemorySystemUpdateStore()
	hostKey, _ := ed25519AuthorizedKeyForTest(t, "")
	policy, err := policies.SavePullUpdaterPolicy(
		t.Context(),
		updates,
		serviceID,
		0,
		0,
		updaterPolicyForHTTPTest(hostKey),
	)
	if err != nil {
		t.Fatal(err)
	}
	secrets := updaterReleaseTokenSecretStoreForBootstrapTest(t, "github_pat_bootstrap_readiness")
	capabilities := map[string]any{
		"host_agent":                           true,
		"observe_only":                         true,
		"policy_revision":                      policy.ProjectionRevision,
		"policy_desired_revision":              policy.ProjectionRevision,
		"policy_status":                        "applied",
		"bootstrap_encryption_public_key":      base64.RawURLEncoding.EncodeToString(bootstrapPublicKey),
		"bootstrap_encryption_key_fingerprint": bootstrapFingerprintForTest(bootstrapPublicKey),
	}
	if _, err := auth.Heartbeat(t.Context(), token, store.ServiceHeartbeat{
		ServiceID:    serviceID,
		Status:       "online",
		Version:      "v1.0.0",
		Capabilities: capabilities,
	}); err != nil {
		t.Fatal(err)
	}

	server := NewServer(
		store.NewMemoryStreamStore(),
		WithAuthStore(auth),
		WithAuditStore(auth),
		WithServiceRegistryStore(auth),
		WithUpdaterPolicyStore(policies),
		WithSystemUpdateStore(updates),
		WithSecretStore(secrets),
	)
	cookie, csrf := loginForTest(
		t,
		server,
		"bootstrap-readiness-admin",
		"correct horse battery",
	)
	return bootstrapIdentityReadinessFixture{
		auth:                  auth,
		server:                server,
		serviceID:             serviceID,
		runtimeToken:          token,
		cookie:                cookie,
		csrf:                  csrf,
		policyRevision:        policy.ProjectionRevision,
		capabilities:          capabilities,
		recipientFingerprint:  bootstrapFingerprintForTest(bootstrapPublicKey),
		ephemeralPublicKeyB64: base64.RawURLEncoding.EncodeToString(p256PublicKeyForBootstrapTest(t)),
	}
}

func (f bootstrapIdentityReadinessFixture) rotateRuntimeIdentity(t *testing.T) string {
	t.Helper()
	return rotateUpdaterRuntimeIdentityForTest(t, f.server, f.serviceID, f.adminRequest)
}

func rotateUpdaterRuntimeIdentityForTest(
	t *testing.T,
	server *Server,
	serviceID string,
	adminRequest func(*testing.T, string, string, string) *httptest.ResponseRecorder,
) string {
	t.Helper()
	configure := adminRequest(
		t,
		http.MethodPost,
		"/nodes/"+serviceID+"/configure-token",
		"",
	)
	if configure.Code != http.StatusCreated {
		t.Fatalf("updater configure-token status=%d", configure.Code)
	}
	var configureResponse struct {
		ConfigureToken string `json:"configure_token"`
	}
	if err := json.NewDecoder(configure.Body).Decode(&configureResponse); err != nil {
		t.Fatal(err)
	}
	if configureResponse.ConfigureToken == "" {
		t.Fatal("updater configure-token response omitted the one-time token")
	}

	stagePayload, err := json.Marshal(map[string]any{
		"nodeId":          serviceID,
		"configureToken":  configureResponse.ConfigureToken,
		"protocolVersion": updateradapter.HostAgentConfigureProtocolVersion,
		"agentUid":        updaterIdentityFixtureAgentUID,
		"agentGid":        updaterIdentityFixtureAgentGID,
	})
	if err != nil {
		t.Fatal(err)
	}
	stageRequest := httptest.NewRequest(
		http.MethodPost,
		"https://panel.example.com/services/host-agent/runtime-identity/stage",
		bytes.NewReader(stagePayload),
	)
	stage := httptest.NewRecorder()
	server.ServeHTTP(stage, stageRequest)
	if stage.Code != http.StatusOK {
		t.Fatalf("updater runtime identity stage status=%d body=%s", stage.Code, stage.Body.String())
	}
	var staged updateradapter.UpdaterStagedConfiguration
	if err := json.NewDecoder(stage.Body).Decode(&staged); err != nil {
		t.Fatal(err)
	}
	if staged.Config.RuntimeToken == "" ||
		staged.ConfigurationID == "" ||
		staged.ActivationToken == "" ||
		staged.LocalExecutorPolicy == nil {
		t.Fatal("updater runtime identity stage response is incomplete")
	}

	activationPayload, err := json.Marshal(hostAgentConfigureActivationPayload(
		staged,
		updaterIdentityFixtureAgentUID,
		updaterIdentityFixtureAgentGID,
		*staged.LocalExecutorPolicy,
	))
	if err != nil {
		t.Fatal(err)
	}
	activationRequest := httptest.NewRequest(
		http.MethodPost,
		"https://panel.example.com/services/host-agent/runtime-identity/activate",
		bytes.NewReader(activationPayload),
	)
	activation := httptest.NewRecorder()
	server.ServeHTTP(activation, activationRequest)
	if activation.Code != http.StatusOK {
		t.Fatalf("updater runtime identity activation status=%d body=%s", activation.Code, activation.Body.String())
	}
	return staged.Config.RuntimeToken
}

func (f bootstrapIdentityReadinessFixture) createBootstrapRequest(
	t *testing.T,
	idempotencySuffix string,
) *httptest.ResponseRecorder {
	t.Helper()
	jobID := "8ba7b810-9dad-4f0e-9a58-4aee7cb5560f"
	if idempotencySuffix == "pending-configuration" {
		jobID = "9ba7b810-9dad-4f0e-9a58-4aee7cb5560f"
	}
	payload, err := json.Marshal(map[string]any{
		"job_id":                    jobID,
		"idempotency_key":           "idempotency-" + idempotencySuffix,
		"expected_revision":         f.policyRevision,
		"recipient_key_fingerprint": f.recipientFingerprint,
		"host_ids":                  []string{"host-01"},
		"envelope": map[string]any{
			"version":              1,
			"ephemeral_public_key": f.ephemeralPublicKeyB64,
			"nonce": base64.RawURLEncoding.EncodeToString(
				bytes.Repeat([]byte{3}, 12),
			),
			"ciphertext": base64.RawURLEncoding.EncodeToString(
				bytes.Repeat([]byte{4}, 96),
			),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(
		http.MethodPost,
		"https://panel.example.com/system-updates/updaters/"+f.serviceID+"/bootstrap-jobs",
		bytes.NewReader(payload),
	)
	request.AddCookie(f.cookie)
	request.Header.Set("X-CSRF-Token", f.csrf)
	result := httptest.NewRecorder()
	f.server.ServeHTTP(result, request)
	return result
}

func (f bootstrapIdentityReadinessFixture) heartbeat(t *testing.T, rawToken string) {
	t.Helper()
	payload, err := json.Marshal(store.ServiceHeartbeat{
		ServiceID:    f.serviceID,
		Status:       "online",
		Version:      "v1.0.1",
		Capabilities: f.capabilities,
	})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(
		http.MethodPost,
		"https://panel.example.com/services/heartbeat",
		bytes.NewReader(payload),
	)
	request.Header.Set("Authorization", "Bearer "+rawToken)
	result := httptest.NewRecorder()
	f.server.ServeHTTP(result, request)
	if result.Code != http.StatusAccepted {
		t.Fatalf("new-token heartbeat status=%d body=%s", result.Code, result.Body.String())
	}
}

func (f bootstrapIdentityReadinessFixture) heartbeatAfterRotation(
	t *testing.T,
	rawToken string,
) {
	t.Helper()
	f.heartbeat(t, rawToken)
	service, err := f.auth.GetService(t.Context(), f.serviceID)
	if err != nil {
		t.Fatal(err)
	}
	if service.LastHeartbeatAt == nil {
		t.Fatalf("post-rotation heartbeat was not persisted: %#v", service)
	}
}

func (f bootstrapIdentityReadinessFixture) adminRequest(
	t *testing.T,
	method string,
	path string,
	body string,
) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.AddCookie(f.cookie)
	request.Header.Set("X-CSRF-Token", f.csrf)
	result := httptest.NewRecorder()
	f.server.ServeHTTP(result, request)
	return result
}

func (f bootstrapIdentityReadinessFixture) publicRequest(
	t *testing.T,
	method string,
	path string,
	body string,
) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	result := httptest.NewRecorder()
	f.server.ServeHTTP(result, request)
	return result
}
