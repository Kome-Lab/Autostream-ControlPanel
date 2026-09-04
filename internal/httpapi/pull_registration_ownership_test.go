package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/example/autostream-control-panel/internal/store"
)

type pullRegistrationOwnershipSpy struct {
	store.SystemUpdateStore
	switchCalls int
}

func (s *pullRegistrationOwnershipSpy) GetSystemUpdateExecutionHost(
	ctx context.Context,
	executionHostID string,
) (store.SystemUpdateExecutionHost, error) {
	return s.SystemUpdateStore.(store.SystemUpdateExecutionHostStore).
		GetSystemUpdateExecutionHost(ctx, executionHostID)
}

func (s *pullRegistrationOwnershipSpy) SwitchSystemUpdateExecutionHost(
	ctx context.Context,
	executionHostID string,
	expectedEpoch int64,
	transportMode string,
	agentServiceID string,
	policyRevision int64,
) (store.SystemUpdateExecutionHost, error) {
	s.switchCalls++
	return s.SystemUpdateStore.(store.SystemUpdateExecutionHostStore).
		SwitchSystemUpdateExecutionHost(
			ctx,
			executionHostID,
			expectedEpoch,
			transportMode,
			agentServiceID,
			policyRevision,
		)
}

func TestCreatePullNodeRegistrationIsObserveOnlyAndDoesNotClaimOwnership(t *testing.T) {
	testCases := map[string]bool{
		"new execution host":       false,
		"existing pull-owned host": true,
	}
	for name, existingPullOwner := range testCases {
		t.Run(name, func(t *testing.T) {
			t.Setenv("AUTOSTREAM_SECRET_ENCRYPTION_KEY", "test-secret-encryption-key-32-bytes")
			updates := store.NewMemorySystemUpdateStore()
			if existingPullOwner {
				if _, err := updates.SwitchSystemUpdateExecutionHost(
					t.Context(),
					"host-a",
					0,
					store.SystemUpdateTransportPullV2,
					"existing-host-agent",
					7,
				); err != nil {
					t.Fatal(err)
				}
			}
			before, err := updates.GetSystemUpdateExecutionHost(t.Context(), "host-a")
			if err != nil {
				t.Fatal(err)
			}
			spy := &pullRegistrationOwnershipSpy{SystemUpdateStore: updates}
			auth, cookie, csrf, handler := newPullRegistrationOwnershipServer(t, spy)

			response := postPullNodeRegistration(t, handler, cookie, csrf, "host-agent-a", "host-a")
			if response.Code != http.StatusCreated {
				t.Fatalf("create pull node = %d %s", response.Code, response.Body.String())
			}
			if spy.switchCalls != 0 {
				t.Fatalf("observer registration switched ownership %d time(s)", spy.switchCalls)
			}
			registered, err := auth.GetService(t.Context(), "host-agent-a")
			if err != nil {
				t.Fatal(err)
			}
			if registered.TransportMode != store.SystemUpdateTransportPullV2 ||
				registered.ExecutionHostID != "host-a" ||
				registered.OwnershipEpoch != 0 {
				t.Fatalf("observer binding = %#v", registered)
			}
			after, err := updates.GetSystemUpdateExecutionHost(t.Context(), "host-a")
			if err != nil {
				t.Fatal(err)
			}
			if after != before {
				t.Fatalf("observer registration mutated ownership:\nbefore=%#v\nafter=%#v", before, after)
			}
		})
	}
}

func TestCreateGenericPullServiceTokenIsObserveOnlyAndDoesNotClaimOwnership(t *testing.T) {
	updates := store.NewMemorySystemUpdateStore()
	if _, err := updates.SwitchSystemUpdateExecutionHost(
		t.Context(),
		"host-generic",
		0,
		store.SystemUpdateTransportPullV2,
		"existing-host-agent",
		3,
	); err != nil {
		t.Fatal(err)
	}
	before, err := updates.GetSystemUpdateExecutionHost(t.Context(), "host-generic")
	if err != nil {
		t.Fatal(err)
	}
	spy := &pullRegistrationOwnershipSpy{SystemUpdateStore: updates}
	auth, cookie, csrf, handler := newPullRegistrationOwnershipServer(t, spy)
	request := httptest.NewRequest(
		http.MethodPost,
		"/api-tokens",
		strings.NewReader(`{
			"service_type":"update_agent",
			"scopes":["service.register","service.heartbeat","updates.claim","updates.report","updates.authorize"],
			"service_id":"host-agent-generic",
			"service_name":"Generic Host Agent",
			"transport_mode":"pull_v2",
			"execution_host_id":"host-generic"
		}`),
	)
	request.AddCookie(cookie)
	request.Header.Set("X-CSRF-Token", csrf)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("create generic pull token = %d %s", response.Code, response.Body.String())
	}
	if spy.switchCalls != 0 {
		t.Fatalf("observer token creation switched ownership %d time(s)", spy.switchCalls)
	}
	registered, err := auth.GetService(t.Context(), "host-agent-generic")
	if err != nil {
		t.Fatal(err)
	}
	if registered.OwnershipEpoch != 0 {
		t.Fatalf("observer ownership epoch = %d", registered.OwnershipEpoch)
	}
	after, err := updates.GetSystemUpdateExecutionHost(t.Context(), "host-generic")
	if err != nil {
		t.Fatal(err)
	}
	if after != before {
		t.Fatalf("observer token creation mutated ownership:\nbefore=%#v\nafter=%#v", before, after)
	}
}

func TestCreateGenericPullServiceTokenRejectsClientOwnershipEpoch(t *testing.T) {
	auth, cookie, csrf, handler := newPullRegistrationOwnershipServer(t, store.NewMemorySystemUpdateStore())
	request := httptest.NewRequest(
		http.MethodPost,
		"/api-tokens",
		strings.NewReader(`{
			"service_type":"update_agent",
			"scopes":["service.register","service.heartbeat","updates.claim","updates.report","updates.authorize"],
			"service_id":"host-agent-generic",
			"service_name":"Generic Host Agent",
			"transport_mode":"pull_v2",
			"execution_host_id":"host-generic",
			"ownership_epoch":7
		}`),
	)
	request.AddCookie(cookie)
	request.Header.Set("X-CSRF-Token", csrf)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest ||
		!strings.Contains(response.Body.String(), `"code":"invalid_service_registration"`) {
		t.Fatalf("generic client ownership epoch = %d %s", response.Code, response.Body.String())
	}
	if _, err := auth.GetService(t.Context(), "host-agent-generic"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("client ownership epoch created service: %v", err)
	}
	tokens, err := auth.ListServiceTokens(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(tokens) != 0 {
		t.Fatalf("client ownership epoch created token: %#v", tokens)
	}
}

func TestCreatePullNodeDoesNotRequireExecutionHostOwnershipStore(t *testing.T) {
	t.Setenv("AUTOSTREAM_SECRET_ENCRYPTION_KEY", "test-secret-encryption-key-32-bytes")
	auth, cookie, csrf, handler := newPullRegistrationOwnershipServer(
		t,
		&pullRegistrationSystemUpdateStoreWithoutOwnership{
			SystemUpdateStore: store.NewMemorySystemUpdateStore(),
		},
	)
	response := postPullNodeRegistration(t, handler, cookie, csrf, "host-agent-a", "host-a")
	if response.Code != http.StatusCreated {
		t.Fatalf("create pull observer without ownership store = %d %s", response.Code, response.Body.String())
	}
	registered, err := auth.GetService(t.Context(), "host-agent-a")
	if err != nil {
		t.Fatal(err)
	}
	if registered.OwnershipEpoch != 0 {
		t.Fatalf("observer ownership epoch = %d", registered.OwnershipEpoch)
	}
}

type pullRegistrationSystemUpdateStoreWithoutOwnership struct {
	store.SystemUpdateStore
}

func newPullRegistrationOwnershipServer(
	t *testing.T,
	systemUpdates store.SystemUpdateStore,
) (*store.MemoryAuthStore, *http.Cookie, string, *Server) {
	t.Helper()
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(
		store.User{Username: "admin", Roles: []string{"super_admin"}},
		"correct horse battery",
		[]string{"api_tokens.create", "secrets.update", "system_updates.execute"},
	); err != nil {
		t.Fatal(err)
	}
	options := []ServerOption{WithAuthStore(auth), WithAuditStore(auth)}
	if systemUpdates != nil {
		options = append(options, WithSystemUpdateStore(systemUpdates))
	}
	handler := NewServer(store.NewMemoryStreamStore(), options...)
	cookie, csrf := loginForTest(t, handler, "admin", "correct horse battery")
	return auth, cookie, csrf, handler
}

func postPullNodeRegistration(
	t *testing.T,
	handler http.Handler,
	cookie *http.Cookie,
	csrf string,
	serviceID string,
	executionHostID string,
) *httptest.ResponseRecorder {
	t.Helper()
	payload, err := json.Marshal(map[string]any{
		"node_type":         "update_agent",
		"node_id":           serviceID,
		"name":              serviceID,
		"transport_mode":    store.SystemUpdateTransportPullV2,
		"execution_host_id": executionHostID,
	})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/nodes/registration-tokens", strings.NewReader(string(payload)))
	request.AddCookie(cookie)
	request.Header.Set("X-CSRF-Token", csrf)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}
