package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/example/autostream-control-panel/internal/store"
)

type fixedNodeProjectionTokenStore struct {
	store.ServiceRegistryStore
	tokens  []store.ServiceToken
	listErr error
	getErr  error
}

func (s fixedNodeProjectionTokenStore) ListServiceTokens(ctx context.Context) ([]store.ServiceToken, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return append([]store.ServiceToken(nil), s.tokens...), s.listErr
}

func (s fixedNodeProjectionTokenStore) GetService(ctx context.Context, id string) (store.RegisteredService, error) {
	if s.getErr != nil {
		return store.RegisteredService{}, s.getErr
	}
	return s.ServiceRegistryStore.GetService(ctx, id)
}

type noIdentityFenceSystemUpdateStore struct {
	store.SystemUpdateStore
}

type failingIdentityFenceSystemUpdateStore struct {
	store.SystemUpdateStore
}

func (s failingIdentityFenceSystemUpdateStore) HasSystemUpdateIdentityMutationFence(
	ctx context.Context,
	services store.ServiceRegistryStore,
	serviceID string,
) (bool, error) {
	return false, nil
}

func (s failingIdentityFenceSystemUpdateStore) IsSystemUpdateEmergencyIdentityRecovery(
	ctx context.Context,
	services store.ServiceRegistryStore,
	serviceID string,
) (bool, error) {
	return false, errors.New("identity fence read failed")
}

func TestNodeActionPermissionProjectionReadOnlySuccess(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(
		store.User{ID: "node-projection-admin", Username: "node-projection-admin"},
		"correct horse battery",
		[]string{"api_tokens.create", "api_tokens.revoke", "secrets.update"},
	); err != nil {
		t.Fatal(err)
	}
	token, err := auth.CreateServiceToken(t.Context(), "worker", []string{
		"service.register",
		"service.heartbeat",
		"service.config.read",
		"service.logs.write",
		"service.status.write",
		"worker.events.write",
		"observability.ingest",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := auth.PrecreateService(t.Context(), token, store.ServiceRegistration{
		ServiceID:   "worker-projection-01",
		ServiceType: "worker",
		ServiceName: "Worker Projection 01",
		PublicURL:   "https://worker.example.com",
	}); err != nil {
		t.Fatal(err)
	}
	handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth), WithAuditStore(auth))
	cookie, _ := loginForTest(t, handler, "node-projection-admin", "correct horse battery")
	tokensBefore, err := auth.ListServiceTokens(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	auditsBefore := len(auth.AuditEvents())

	req := newNodeActionProjectionRequest(
		"/nodes/action-permissions?action=configure_token_regenerate&node_id=worker-projection-01",
	)
	req.AddCookie(cookie)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("projection status=%d body=%s", res.Code, res.Body.String())
	}
	if res.Header().Get("Content-Type") != "application/json" ||
		res.Header().Get("Cache-Control") != "no-store, no-cache" ||
		res.Header().Get("Pragma") != "no-cache" ||
		res.Header().Get("Referrer-Policy") != "no-referrer" ||
		res.Header().Get(nodeProjectionContractMajorHeader) != nodeProjectionControlAPIMajor {
		t.Fatalf("projection headers=%#v", res.Header())
	}
	var body struct {
		ContractVersion    int      `json:"contract_version"`
		ProjectionRevision string   `json:"projection_revision"`
		Action             string   `json:"action"`
		Availability       string   `json:"availability"`
		ReasonCode         string   `json:"reason_code"`
		Required           []string `json:"required_permissions"`
		Missing            []string `json:"missing_permissions"`
	}
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.ContractVersion != 1 || body.ProjectionRevision == "" ||
		body.Action != "configure_token_regenerate" || body.Availability != "allowed" ||
		body.ReasonCode != "allowed" || len(body.Missing) != 0 {
		t.Fatalf("unexpected projection=%#v", body)
	}
	tokensAfter, err := auth.ListServiceTokens(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(tokensAfter) != len(tokensBefore) {
		t.Fatalf("projection mutated tokens: before=%d after=%d", len(tokensBefore), len(tokensAfter))
	}
	if got := len(auth.AuditEvents()); got != auditsBefore {
		t.Fatalf("projection wrote audit events: before=%d after=%d", auditsBefore, got)
	}
}

func TestNodeActionPermissionProjectionRequiresExactContractMajor(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(
		store.User{ID: "node-projection-contract-user", Username: "node-projection-contract-user"},
		"correct horse battery",
		[]string{"api_tokens.create"},
	); err != nil {
		t.Fatal(err)
	}
	handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth))
	cookie, _ := loginForTest(t, handler, "node-projection-contract-user", "correct horse battery")

	for _, tc := range []struct {
		name    string
		headers []string
	}{
		{name: "missing"},
		{name: "unsupported", headers: []string{"1"}},
		{name: "duplicated", headers: []string{"2", "2"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/nodes/action-permissions?action=registration_token&node_type=worker", nil)
			req.AddCookie(cookie)
			for _, value := range tc.headers {
				req.Header.Add("X-AutoStream-Contract-Major", value)
			}
			res := httptest.NewRecorder()
			handler.ServeHTTP(res, req)
			if res.Code != http.StatusUpgradeRequired ||
				res.Header().Get("X-AutoStream-Contract-Major") != "2" ||
				res.Header().Get("Cache-Control") != "no-store, no-cache" ||
				res.Header().Get("Pragma") != "no-cache" ||
				res.Header().Get("Referrer-Policy") != "no-referrer" ||
				!strings.Contains(res.Body.String(), `"code":"contract_major_unsupported"`) {
				t.Fatalf("contract boundary status=%d headers=%#v body=%s", res.Code, res.Header(), res.Body.String())
			}
			var body map[string]any
			if err := json.Unmarshal(res.Body.Bytes(), &body); err != nil || len(body) != 5 {
				t.Fatalf("contract boundary is not schema-strict: body=%#v err=%v", body, err)
			}
		})
	}
}

func TestNodeActionPermissionAuthorityMatrices(t *testing.T) {
	service := store.RegisteredService{ServiceID: "node-1", ServiceType: "observability", TokenID: "token-1"}
	neutral := store.ServiceToken{
		ID: "token-1", ServiceType: "observability",
		Scopes: []string{"service.register", "service.heartbeat"},
	}
	authority := evaluateNodeTokenAuthority([]string{"api_tokens.create", "api_tokens.revoke"}, service, neutral)
	if authority.reason != nodeAuthorityAllowed ||
		!slices.Equal(authority.requiredPermissions, []string{"api_tokens.create", "api_tokens.revoke"}) ||
		len(authority.missingPermissions) != 0 {
		t.Fatalf("known neutral scope authority=%#v", authority)
	}

	mapped := store.ServiceToken{
		ID: "token-2", ServiceType: "discord_bot",
		Scopes: []string{"service.register", "service.heartbeat", "streams.start", "streams.stop"},
	}
	mappedService := store.RegisteredService{ServiceID: "bot-1", ServiceType: "discord_bot", TokenID: "token-2"}
	authority = evaluateNodeTokenAuthority(
		[]string{"api_tokens.create", "api_tokens.revoke", "streams.start", "streams.stop"},
		mappedService,
		mapped,
	)
	if authority.reason != nodeAuthorityAllowed ||
		!slices.Equal(authority.requiredPermissions, []string{"api_tokens.create", "api_tokens.revoke", "streams.start", "streams.stop"}) ||
		len(authority.missingPermissions) != 0 {
		t.Fatalf("known mapped scope authority=%#v", authority)
	}

	cases := []struct {
		name    string
		service store.RegisteredService
		token   store.ServiceToken
	}{
		{
			name:    "service type mismatch",
			service: store.RegisteredService{ServiceID: "node-1", ServiceType: "worker", TokenID: "token-1"},
			token:   neutral,
		},
		{
			name:    "unknown scope",
			service: service,
			token:   store.ServiceToken{ID: "token-1", ServiceType: "observability", Scopes: []string{"service.register", "future.privileged"}},
		},
		{
			name:    "malformed empty stored scope",
			service: service,
			token:   store.ServiceToken{ID: "token-1", ServiceType: "observability", Scopes: nil},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := evaluateNodeTokenAuthority([]string{"*"}, tc.service, tc.token)
			if got.reason != nodeAuthorityInvalidServiceScope || len(got.missingPermissions) != 0 {
				t.Fatalf("invalid authority=%#v", got)
			}
		})
	}

	mandatory := []string{"updates.claim", "updates.report", "updates.authorize"}
	for _, missingScope := range mandatory {
		t.Run("update agent missing "+missingScope, func(t *testing.T) {
			scopes := []string{"service.register", "service.heartbeat"}
			for _, scope := range mandatory {
				if scope != missingScope {
					scopes = append(scopes, scope)
				}
			}
			got := evaluateNodeTokenAuthority(
				[]string{"*"},
				store.RegisteredService{ServiceID: "updater-1", ServiceType: "update_agent", TokenID: "updater-token"},
				store.ServiceToken{ID: "updater-token", ServiceType: "update_agent", Scopes: scopes},
			)
			if got.reason != nodeAuthorityInvalidServiceScope {
				t.Fatalf("missing %s authority=%#v", missingScope, got)
			}
		})
	}

	allScopes := []string{
		"service.register", "service.heartbeat", "service.secret.resolve",
		"updates.claim", "updates.report", "updates.authorize",
		"streams.start", "streams.stop", "remediation.execute",
	}
	updater := store.RegisteredService{ServiceID: "updater-all", ServiceType: "update_agent", TokenID: "updater-token"}
	updaterToken := store.ServiceToken{ID: "updater-token", ServiceType: "update_agent", Scopes: allScopes}
	exact := evaluateNodeTokenAuthority(
		[]string{"api_tokens.create", "api_tokens.revoke", "secrets.update", "system_updates.execute", "streams.start", "streams.stop", "remediation.execute"},
		updater,
		updaterToken,
	)
	if exact.reason != nodeAuthorityAllowed || len(exact.missingPermissions) != 0 {
		t.Fatalf("exact permission actor authority=%#v", exact)
	}
	if wildcard := evaluateNodeTokenAuthority([]string{"*"}, updater, updaterToken); len(wildcard.missingPermissions) != 0 {
		t.Fatalf("wildcard actor authority=%#v", wildcard)
	}
	for _, missingPermission := range exact.requiredPermissions {
		t.Run("actor missing "+missingPermission, func(t *testing.T) {
			permissions := make([]string, 0, len(exact.requiredPermissions)-1)
			for _, permission := range exact.requiredPermissions {
				if permission != missingPermission {
					permissions = append(permissions, permission)
				}
			}
			got := evaluateNodeTokenAuthority(permissions, updater, updaterToken)
			if !slices.Equal(got.missingPermissions, []string{missingPermission}) {
				t.Fatalf("missing %s authority=%#v", missingPermission, got)
			}
		})
	}
}

func TestNodeActionPermissionProjectionStrictErrorsAndFailClosedScope(t *testing.T) {
	t.Setenv("AUTOSTREAM_SECRET_ENCRYPTION_KEY", "node-projection-test-encryption-key-32-bytes")
	auth := store.NewMemoryAuthStore()
	permissions := []string{"api_tokens.create", "api_tokens.revoke"}
	if err := auth.AddUser(store.User{ID: "projection-user", Username: "projection-user"}, "correct horse battery", permissions); err != nil {
		t.Fatal(err)
	}
	token, err := auth.CreateServiceToken(t.Context(), "observability", []string{"service.register", "service.heartbeat"})
	if err != nil {
		t.Fatal(err)
	}
	service, err := auth.PrecreateService(t.Context(), token, store.ServiceRegistration{
		ServiceID: "observability-projection", ServiceType: "observability", ServiceName: "Observability Projection", PublicURL: "https://observability.example.com",
	})
	if err != nil {
		t.Fatal(err)
	}
	invalidToken := token
	invalidToken.RawToken = ""
	invalidToken.TokenHash = ""
	invalidToken.Scopes = append(append([]string(nil), token.Scopes...), "future.privileged")
	serviceStore := fixedNodeProjectionTokenStore{ServiceRegistryStore: auth, tokens: []store.ServiceToken{invalidToken}}
	handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth), WithAuditStore(auth), WithServiceRegistryStore(serviceStore))
	cookie, csrf := loginForTest(t, handler, "projection-user", "correct horse battery")

	for _, target := range []string{
		"/nodes/action-permissions?action=unknown",
		"/nodes/action-permissions?action=registration_token&node_type=worker&future=true",
		"/nodes/action-permissions?action=registration_token&node_type=worker&node_type=worker",
		"/nodes/action-permissions?action=registration_token&node_type=worker&node_id=unexpected",
		"/nodes/action-permissions?action=registration_token&node_type=worker&allow_remediation=maybe",
		"/nodes/action-permissions?action=registration_token&node_type=worker&allow_remediation=1",
		"/nodes/action-permissions?action=runtime_token_rotate&node_id=:invalid",
		"/nodes/action-permissions?action=runtime_token_rotate&node_id=valid&allow_runtime_secrets=false",
	} {
		bad := newNodeActionProjectionRequest(target)
		bad.AddCookie(cookie)
		badResponse := httptest.NewRecorder()
		handler.ServeHTTP(badResponse, bad)
		assertNodeProjectionError(t, badResponse, http.StatusBadRequest, "invalid_node_action_projection_request")
	}

	unauthorized := newNodeActionProjectionRequest("/nodes/action-permissions?action=runtime_token_rotate&node_id=observability-projection")
	unauthorizedResponse := httptest.NewRecorder()
	handler.ServeHTTP(unauthorizedResponse, unauthorized)
	assertNodeProjectionError(t, unauthorizedResponse, http.StatusUnauthorized, "unauthorized")

	missing := newNodeActionProjectionRequest("/nodes/action-permissions?action=runtime_token_rotate&node_id=missing-node")
	missing.AddCookie(cookie)
	missingResponse := httptest.NewRecorder()
	handler.ServeHTTP(missingResponse, missing)
	assertNodeProjectionError(t, missingResponse, http.StatusNotFound, "node_not_found")

	projection := newNodeActionProjectionRequest("/nodes/action-permissions?action=configure_token_regenerate&node_id=observability-projection")
	projection.AddCookie(cookie)
	projectionResponse := httptest.NewRecorder()
	handler.ServeHTTP(projectionResponse, projection)
	if projectionResponse.Code != http.StatusOK ||
		!strings.Contains(projectionResponse.Body.String(), `"availability":"unknown"`) ||
		!strings.Contains(projectionResponse.Body.String(), `"reason_code":"invalid_service_scope"`) {
		t.Fatalf("invalid-scope projection status=%d body=%s", projectionResponse.Code, projectionResponse.Body.String())
	}
	for _, marker := range []string{token.ID, token.RawToken, service.ServiceID, "future.privileged", "token_hash", "ciphertext", "nonce", "stack", "https://"} {
		if marker != "" && strings.Contains(projectionResponse.Body.String(), marker) {
			t.Fatalf("projection leaked forbidden marker category: %q", marker)
		}
	}
	auditsBefore := len(auth.AuditEvents())
	mutation := httptest.NewRequest(http.MethodPost, "/nodes/observability-projection/configure-token", nil)
	mutation.AddCookie(cookie)
	mutation.Header.Set("X-CSRF-Token", csrf)
	mutationResponse := httptest.NewRecorder()
	handler.ServeHTTP(mutationResponse, mutation)
	if mutationResponse.Code != http.StatusBadRequest || !strings.Contains(mutationResponse.Body.String(), `"code":"invalid_service_scope"`) {
		t.Fatalf("invalid-scope mutation status=%d body=%s", mutationResponse.Code, mutationResponse.Body.String())
	}
	if got := len(auth.AuditEvents()); got != auditsBefore {
		t.Fatalf("rejected mutation wrote audit events: before=%d after=%d", auditsBefore, got)
	}

	errorStore := fixedNodeProjectionTokenStore{ServiceRegistryStore: auth, listErr: errors.New("list failed")}
	errorHandler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth), WithServiceRegistryStore(errorStore))
	errorCookie, _ := loginForTest(t, errorHandler, "projection-user", "correct horse battery")
	listError := newNodeActionProjectionRequest("/nodes/action-permissions?action=runtime_token_rotate&node_id=observability-projection")
	listError.AddCookie(errorCookie)
	listErrorResponse := httptest.NewRecorder()
	errorHandler.ServeHTTP(listErrorResponse, listError)
	assertNodeProjectionError(t, listErrorResponse, http.StatusInternalServerError, "list_service_tokens_failed")

	getErrorStore := fixedNodeProjectionTokenStore{ServiceRegistryStore: auth, getErr: errors.New("get failed")}
	getErrorHandler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth), WithServiceRegistryStore(getErrorStore))
	getErrorCookie, _ := loginForTest(t, getErrorHandler, "projection-user", "correct horse battery")
	getError := newNodeActionProjectionRequest("/nodes/action-permissions?action=runtime_token_rotate&node_id=observability-projection")
	getError.AddCookie(getErrorCookie)
	getErrorResponse := httptest.NewRecorder()
	getErrorHandler.ServeHTTP(getErrorResponse, getError)
	assertNodeProjectionError(t, getErrorResponse, http.StatusInternalServerError, "get_node_failed")

	deniedAuth := store.NewMemoryAuthStore()
	if err := deniedAuth.AddUser(store.User{ID: "denied-user", Username: "denied-user"}, "correct horse battery", nil); err != nil {
		t.Fatal(err)
	}
	deniedHandler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(deniedAuth))
	deniedCookie, _ := loginForTest(t, deniedHandler, "denied-user", "correct horse battery")
	denied := newNodeActionProjectionRequest("/nodes/action-permissions?action=registration_token&node_type=worker")
	denied.AddCookie(deniedCookie)
	deniedResponse := httptest.NewRecorder()
	deniedHandler.ServeHTTP(deniedResponse, denied)
	assertNodeProjectionError(t, deniedResponse, http.StatusForbidden, "permission_denied")

	pendingAuth := store.NewMemoryAuthStore()
	if err := pendingAuth.AddUser(
		store.User{ID: "pending-user", Username: "pending-user", Status: "pending_password_change"},
		"temporary correct battery",
		[]string{"api_tokens.create"},
	); err != nil {
		t.Fatal(err)
	}
	pendingHandler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(pendingAuth))
	pendingCookie, _ := loginForTest(t, pendingHandler, "pending-user", "temporary correct battery")
	pending := newNodeActionProjectionRequest("/nodes/action-permissions?action=registration_token&node_type=worker")
	pending.AddCookie(pendingCookie)
	pendingResponse := httptest.NewRecorder()
	pendingHandler.ServeHTTP(pendingResponse, pending)
	assertNodeProjectionError(t, pendingResponse, http.StatusForbidden, "password_change_required")
}

func TestNodeActionPermissionProjectionIdentityFenceParity(t *testing.T) {
	for _, tc := range []struct {
		name       string
		updates    store.SystemUpdateStore
		wantStatus int
		wantCode   string
	}{
		{
			name:       "E1 unavailable",
			updates:    noIdentityFenceSystemUpdateStore{SystemUpdateStore: store.NewMemorySystemUpdateStore()},
			wantStatus: http.StatusServiceUnavailable,
			wantCode:   "system_update_identity_fence_unavailable",
		},
		{
			name:       "E2 recovery check failed",
			updates:    failingIdentityFenceSystemUpdateStore{SystemUpdateStore: store.NewMemorySystemUpdateStore()},
			wantStatus: http.StatusInternalServerError,
			wantCode:   "check_system_update_emergency_recovery_failed",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("AUTOSTREAM_SECRET_ENCRYPTION_KEY", "node-projection-test-encryption-key-32-bytes")
			auth := store.NewMemoryAuthStore()
			if err := auth.AddUser(
				store.User{ID: "fence-user", Username: "fence-user"},
				"correct horse battery",
				[]string{"api_tokens.create", "api_tokens.revoke"},
			); err != nil {
				t.Fatal(err)
			}
			token, err := auth.CreateServiceToken(t.Context(), "observability", []string{"service.register", "service.heartbeat"})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := auth.PrecreateService(t.Context(), token, store.ServiceRegistration{
				ServiceID: "fence-node", ServiceType: "observability", ServiceName: "Fence Node", PublicURL: "https://observability.example.com",
			}); err != nil {
				t.Fatal(err)
			}
			if err := auth.RevokeServiceToken(t.Context(), token.ID); err != nil {
				t.Fatal(err)
			}
			handler := NewServer(
				store.NewMemoryStreamStore(),
				WithAuthStore(auth),
				WithAuditStore(auth),
				WithSystemUpdateStore(tc.updates),
			)
			cookie, csrf := loginForTest(t, handler, "fence-user", "correct horse battery")
			auditsBefore := len(auth.AuditEvents())
			tokensBefore, err := auth.ListServiceTokens(t.Context())
			if err != nil {
				t.Fatal(err)
			}

			get := newNodeActionProjectionRequest("/nodes/action-permissions?action=configure_token_regenerate&node_id=fence-node")
			get.AddCookie(cookie)
			getResponse := httptest.NewRecorder()
			handler.ServeHTTP(getResponse, get)
			assertNodeProjectionError(t, getResponse, tc.wantStatus, tc.wantCode)

			post := httptest.NewRequest(http.MethodPost, "/nodes/fence-node/configure-token", nil)
			post.AddCookie(cookie)
			post.Header.Set("X-CSRF-Token", csrf)
			postResponse := httptest.NewRecorder()
			handler.ServeHTTP(postResponse, post)
			if postResponse.Code != tc.wantStatus || !strings.Contains(postResponse.Body.String(), `"code":"`+tc.wantCode+`"`) {
				t.Fatalf("mutation parity status=%d body=%s", postResponse.Code, postResponse.Body.String())
			}
			if got := len(auth.AuditEvents()); got != auditsBefore {
				t.Fatalf("identity-fence rejection wrote audit: before=%d after=%d", auditsBefore, got)
			}
			tokensAfter, err := auth.ListServiceTokens(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			if len(tokensAfter) != len(tokensBefore) || !tokensAfter[0].CreatedAt.Equal(tokensBefore[0].CreatedAt) {
				t.Fatalf("identity-fence rejection mutated tokens: before=%#v after=%#v", tokensBefore, tokensAfter)
			}
		})
	}
}

func TestNodeActionPermissionRegistrationProjection(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	allPermissions := []string{
		"api_tokens.create", "secrets.update", "system_updates.execute",
		"streams.start", "streams.stop", "remediation.execute",
	}
	if err := auth.AddUser(store.User{ID: "registration-user", Username: "registration-user"}, "correct horse battery", allPermissions); err != nil {
		t.Fatal(err)
	}
	handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth), WithAuditStore(auth))
	cookie, _ := loginForTest(t, handler, "registration-user", "correct horse battery")
	for _, tc := range []struct {
		name         string
		query        string
		availability string
		reason       string
	}{
		{name: "worker", query: "node_type=worker", availability: "allowed", reason: "allowed"},
		{name: "discord", query: "node_type=discord_bot", availability: "allowed", reason: "allowed"},
		{name: "observability remediation", query: "node_type=observability&allow_remediation=true", availability: "allowed", reason: "allowed"},
		{name: "updater", query: "node_type=update_agent", availability: "allowed", reason: "allowed"},
		{name: "invalid type", query: "node_type=future_agent", availability: "not_applicable", reason: "invalid_service_type"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := newNodeActionProjectionRequest("/nodes/action-permissions?action=registration_token&" + tc.query)
			req.AddCookie(cookie)
			res := httptest.NewRecorder()
			handler.ServeHTTP(res, req)
			if res.Code != http.StatusOK ||
				!strings.Contains(res.Body.String(), `"availability":"`+tc.availability+`"`) ||
				!strings.Contains(res.Body.String(), `"reason_code":"`+tc.reason+`"`) {
				t.Fatalf("registration projection status=%d body=%s", res.Code, res.Body.String())
			}
		})
	}
}

func assertNodeProjectionError(t *testing.T, response *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	if response.Code != status || response.Body.String() != `{"code":"`+code+`"}`+"\n" {
		t.Fatalf("projection error status=%d body=%s", response.Code, response.Body.String())
	}
	if response.Header().Get("Content-Type") != "application/json" ||
		response.Header().Get("Cache-Control") != "no-store, no-cache" ||
		response.Header().Get("Pragma") != "no-cache" ||
		response.Header().Get("Referrer-Policy") != "no-referrer" ||
		response.Header().Get(nodeProjectionContractMajorHeader) != nodeProjectionControlAPIMajor {
		t.Fatalf("projection error headers=%#v", response.Header())
	}
}

func newNodeActionProjectionRequest(target string) *http.Request {
	req := httptest.NewRequest(http.MethodGet, target, nil)
	req.Header.Set(nodeProjectionContractMajorHeader, nodeProjectionControlAPIMajor)
	return req
}

var _ store.SystemUpdateIdentityMutationFenceStore = failingIdentityFenceSystemUpdateStore{}
