package httpapi

import (
	"github.com/example/autostream-control-panel/internal/store"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
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
