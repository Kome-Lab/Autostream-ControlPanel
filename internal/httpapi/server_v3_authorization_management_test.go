package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	evidence "github.com/Kome-Lab/Autostream-Contracts/v3evidence"
	"github.com/example/autostream-control-panel/internal/security"
	"github.com/example/autostream-control-panel/internal/store"
	"github.com/example/autostream-control-panel/internal/v3authorization"
)

// These are HTTP-boundary collaborators only, never product defaults or claims
// about SQL management authorization, bootstrap, Integrity or qualified time.
type v3ManagementHTTPSession struct {
	store.AuthStore
	status      string
	role        string
	revoked     bool
	missingHash bool
}

func (s *v3ManagementHTTPSession) GetSession(ctx context.Context, token string) (store.Session, error) {
	if err := ctx.Err(); err != nil {
		return store.Session{}, err
	}
	if s.revoked || token != "synthetic-session" {
		return store.Session{}, store.ErrUnauthorized
	}
	hash := security.HashToken(token)
	if s.missingHash {
		hash = ""
	}
	return store.Session{UserID: "synthetic-user", TokenHash: hash, CSRFTokenHash: security.HashToken("synthetic-csrf")}, nil
}
func (s *v3ManagementHTTPSession) GetUser(context.Context, string) (store.User, error) {
	return store.User{ID: "synthetic-user", Status: s.status, Roles: []string{s.role}}, nil
}
func (*v3ManagementHTTPSession) GetUserPermissions(context.Context, string) ([]string, error) {
	// Even the legacy wildcard is not management authority.
	return []string{"*"}, nil
}

type v3ManagementHTTPAPI struct {
	applied      v3authorization.ManagementResult
	inspected    v3authorization.ManagementResult
	applyCalls   int
	inspectCalls int
	actor        v3authorization.ManagementActor
	metadata     v3authorization.ManagementMetadata
	records      []v3authorization.AuthorityRecord
	grants       []v3authorization.ManagementGrant
	denials      []string
	deniedActors []v3authorization.Principal
	auditErr     error
}

var _ v3authorization.ManagementAPI = (*v3ManagementHTTPAPI)(nil)

func (s *v3ManagementHTTPAPI) Apply(_ context.Context, actor v3authorization.ManagementActor, metadata v3authorization.ManagementMetadata, records []v3authorization.AuthorityRecord, grants []v3authorization.ManagementGrant) v3authorization.ManagementResult {
	s.applyCalls++
	s.actor, s.metadata, s.records, s.grants = actor, metadata, records, grants
	return s.applied
}
func (s *v3ManagementHTTPAPI) Inspect(_ context.Context, actor v3authorization.ManagementActor, metadata v3authorization.ManagementMetadata) v3authorization.ManagementResult {
	s.inspectCalls++
	s.actor, s.metadata = actor, metadata
	return s.inspected
}
func (s *v3ManagementHTTPAPI) RecordTransportDenial(_ context.Context, principal v3authorization.Principal, reason string) error {
	s.denials = append(s.denials, reason)
	s.deniedActors = append(s.deniedActors, principal)
	return s.auditErr
}

func v3ManagementHTTPFixture() (*Server, *v3ManagementHTTPSession, *v3ManagementHTTPAPI) {
	session := &v3ManagementHTTPSession{status: "active", role: "super_admin"}
	api := &v3ManagementHTTPAPI{
		applied:   v3authorization.ManagementResult{Status: v3authorization.Denied},
		inspected: v3authorization.ManagementResult{Status: v3authorization.Denied},
	}
	return &Server{auth: session}, session, api
}
func v3ManagementHTTPScope() evidence.Scope {
	return evidence.Scope{Realm: "realm-a", Host: "host-a", Resource: "resource-a", Backend: "backend-a"}
}
func v3ManagementHTTPBody(t *testing.T, records any) string {
	t.Helper()
	body := map[string]any{
		"event_id": "event-a", "correlation_id": "correlation-a", "expected_revision": uint64(1),
		"scope": v3ManagementHTTPScope(), "action": v3authorization.Action,
	}
	if records != nil {
		body["records"] = records
	}
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}
func v3ManagementHTTPRealmBody(t *testing.T) string {
	return v3ManagementHTTPBody(t, []v3authorization.RealmRegistration{{Realm: "realm-a", State: "ACTIVE"}})
}
func v3ManagementHTTPRequest(method, path, body string) *http.Request {
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "synthetic-session"})
	request.Header.Set("X-CSRF-Token", "synthetic-csrf")
	return request
}
func v3ManagementHTTPResponse(t *testing.T, handler http.Handler, request *http.Request, status int) *httptest.ResponseRecorder {
	t.Helper()
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != status {
		t.Fatalf("status=%d, want=%d body=%s", response.Code, status, response.Body.String())
	}
	if response.Header().Get("Cache-Control") != "no-store" ||
		response.Header().Get("Pragma") != "no-cache" ||
		response.Header().Get("X-Content-Type-Options") != "nosniff" ||
		response.Header().Get("X-Frame-Options") != "DENY" {
		t.Fatal("management response lost security/cache boundary")
	}
	if strings.Contains(response.Body.String(), "secret") || strings.Contains(response.Body.String(), "SELECT") {
		t.Fatal("management response exposed private errors")
	}
	return response
}

func TestV3AuthorizationManagementHTTPIdentityAndCSRF(t *testing.T) {
	for _, tc := range []struct {
		name, status, reason               string
		cookie, csrf, revoked, missingHash bool
		want                               int
	}{
		{"anonymous", "active", "unauthenticated", false, true, false, false, http.StatusUnauthorized},
		{"revoked-session", "active", "unauthenticated", true, true, true, false, http.StatusUnauthorized},
		{"unknown-session-hash", "active", "unauthenticated", true, true, false, true, http.StatusUnauthorized},
		{"csrf", "active", "csrf_denied", true, false, false, false, http.StatusForbidden},
		{"pending-password-change", "pending_password_change", "inactive_principal", true, true, false, false, http.StatusForbidden},
		{"inactive", "pending", "inactive_principal", true, true, false, false, http.StatusForbidden},
		{"disabled", "disabled", "unauthenticated", true, true, false, false, http.StatusUnauthorized},
		{"locked", "locked", "unauthenticated", true, true, false, false, http.StatusUnauthorized},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server, session, api := v3ManagementHTTPFixture()
			session.status, session.revoked, session.missingHash = tc.status, tc.revoked, tc.missingHash
			request := v3ManagementHTTPRequest(http.MethodPost, "/v3/authority-management/realms", v3ManagementHTTPRealmBody(t))
			if !tc.cookie {
				request.Header.Del("Cookie")
			}
			if !tc.csrf {
				request.Header.Del("X-CSRF-Token")
			}
			v3ManagementHTTPResponse(t, server.V3AuthorityManagementHandler(api), request, tc.want)
			if api.applyCalls != 0 || api.inspectCalls != 0 || len(api.denials) != 1 || api.denials[0] != tc.reason {
				t.Fatal("identity/CSRF rejection was not audited before management")
			}
		})
	}
}

func TestV3AuthorizationManagementHTTPFixedRoutesAndTrustedActor(t *testing.T) {
	scope := v3ManagementHTTPScope()
	for _, tc := range []struct {
		path           string
		kind           v3authorization.AuthorityRecordKind
		payload        any
		admin, inspect bool
	}{
		{"realms", v3authorization.RealmRecord, []v3authorization.RealmRegistration{{Realm: scope.Realm, State: "ACTIVE"}}, false, false},
		{"hosts", v3authorization.HostRecord, []v3authorization.HostRegistration{{Realm: scope.Realm, Host: scope.Host, OwnerDomain: "domain-a", State: "ACTIVE"}}, false, false},
		{"resources", v3authorization.ResourceRecord, []v3authorization.ResourceRegistration{{Scope: scope, ReferenceRevision: "revision-a", StateVersion: 0, State: "ACTIVE"}}, false, false},
		{"backends", v3authorization.BackendRecord, []v3authorization.BackendRegistration{{Realm: scope.Realm, Host: scope.Host, Backend: scope.Backend, State: "ACTIVE"}}, false, false},
		{"candidates", v3authorization.CandidateRecord, []v3authorization.CandidateApproval{{Scope: scope, Revision: "candidate-a", ContentPin: "content-a", Approval: "PENDING", Approver: "", State: "ACTIVE"}}, false, false},
		{"capabilities", v3authorization.CapabilityRecord, []v3authorization.V3Capability{{Principal: "subject-a", Scope: scope, Action: v3authorization.Action, Revision: "capability-a", State: "ACTIVE", Issue: true, ReadHistory: true}}, false, false},
		{"policies", v3authorization.PolicyRecord, []v3authorization.ScopeRule{{Scope: scope, Action: v3authorization.Action, Revision: "policy-a", State: "ACTIVE", Revocation: "ACTIVE"}}, false, false},
		{"trust", v3authorization.TrustRecord, []v3authorization.ScopeRule{{Scope: scope, Action: v3authorization.Action, Revision: "trust-a", State: "ACTIVE", Revocation: "ACTIVE"}}, false, false},
		{"memberships", v3authorization.MembershipRecord, []v3authorization.DomainMembership{{Realm: scope.Realm, Principal: "subject-a", Domain: "domain-a", Revision: "membership-a", State: "MEMBER"}}, false, false},
		{"administrators", "", []v3authorization.ManagementGrant{{Principal: "subject-a", Scope: scope, Action: v3authorization.Action, State: "ACTIVE", Operations: []v3authorization.ManagementOperation{v3authorization.ManagementOperation("read")}, Subjects: []string{"subject-a"}}}, true, false},
		{"inspect", "", nil, false, true},
	} {
		t.Run(tc.path, func(t *testing.T) {
			server, _, api := v3ManagementHTTPFixture()
			api.applied = v3authorization.ManagementResult{Status: v3authorization.ManagementApplied, Generation: 2, Initialized: true}
			api.inspected = v3authorization.ManagementResult{Status: v3authorization.ManagementInspected, Generation: 2, Initialized: true}
			request := v3ManagementHTTPRequest(http.MethodPost, "/v3/authority-management/"+tc.path, v3ManagementHTTPBody(t, tc.payload))
			// A client-supplied actor header is never the management actor.
			request.Header.Set("X-Actor-Principal", "attacker")
			v3ManagementHTTPResponse(t, server.V3AuthorityManagementHandler(api), request, http.StatusOK)
			if api.actor.Principal.ID != "synthetic-user" || api.actor.SessionHash != security.HashToken("synthetic-session") ||
				api.metadata.EventID != "event-a" || api.metadata.CorrelationID != "correlation-a" ||
				api.metadata.ExpectedRevision != 1 || api.metadata.Scope != scope || len(api.denials) != 0 {
				t.Fatal("trusted actor or explicit metadata lost")
			}
			if tc.inspect {
				if api.inspectCalls != 1 || api.applyCalls != 0 {
					t.Fatal("inspection routed to a mutation")
				}
			} else if api.applyCalls != 1 || api.inspectCalls != 0 {
				t.Fatal("wrong management operation")
			} else if tc.admin {
				if len(api.grants) != 1 || len(api.records) != 0 || api.grants[0].Principal != "subject-a" {
					t.Fatal("administrator route accepted a generic record")
				}
			} else if len(api.records) != 1 || api.records[0].Kind != tc.kind || len(api.grants) != 0 {
				t.Fatal("route did not pin its record kind")
			}
		})
	}
}

func TestV3AuthorizationManagementHTTPStrictBodiesAndTransportAudit(t *testing.T) {
	body := v3ManagementHTTPRealmBody(t)
	for name, invalid := range map[string]string{
		"empty":           "",
		"missing":         "{}",
		"null":            "null",
		"extra":           strings.TrimSuffix(body, "}") + `,"private-key":"private-secret"}`,
		"principal-spoof": strings.TrimSuffix(body, "}") + `,"principal":"attacker"}`,
		"session-spoof":   strings.TrimSuffix(body, "}") + `,"session_hash":"attacker"}`,
		"duplicate":       strings.Replace(body, `"event_id":"event-a"`, `"event_id":"event-a","event_id":"event-b"`, 1),
		"escaped-alias":   strings.Replace(body, `"event_id":"event-a"`, `"event_id":"event-a","\u0065vent_id":"event-b"`, 1),
		"wrong-case":      strings.Replace(body, `"event_id"`, `"Event_ID"`, 1),
		"null-records":    strings.Replace(body, `"records":[{"Realm":"realm-a","State":"ACTIVE"}]`, `"records":null`, 1),
		"overflow":        strings.Replace(body, `"expected_revision":1`, `"expected_revision":18446744073709551616`, 1),
		"trailing":        body + "{}",
		"utf8":            string([]byte{0xff}),
		"oversize":        strings.Repeat(" ", maxV3AuthorityManagementBodyBytes+1),
	} {
		t.Run(name, func(t *testing.T) {
			if invalid == body {
				t.Fatal("fixture mutation missed its field")
			}
			server, _, api := v3ManagementHTTPFixture()
			v3ManagementHTTPResponse(t, server.V3AuthorityManagementHandler(api),
				v3ManagementHTTPRequest(http.MethodPost, "/v3/authority-management/realms", invalid), http.StatusBadRequest)
			if api.applyCalls != 0 || api.inspectCalls != 0 || len(api.denials) != 1 || api.denials[0] != "body_invalid" {
				t.Fatal("body refusal crossed management or omitted audit")
			}
		})
	}
	for _, tc := range []struct {
		method, path, reason string
		status               int
	}{
		{http.MethodGet, "/v3/authority-management/realms", "method_denied", http.StatusMethodNotAllowed},
		{http.MethodDelete, "/v3/authority-management/realms", "method_denied", http.StatusMethodNotAllowed},
		{http.MethodPost, "/v3/authority-management/unknown", "route_not_found", http.StatusNotFound},
		{http.MethodPost, "/v3/authority-management/bootstrap", "route_not_found", http.StatusNotFound},
	} {
		t.Run(tc.method+tc.path, func(t *testing.T) {
			server, _, api := v3ManagementHTTPFixture()
			v3ManagementHTTPResponse(t, server.V3AuthorityManagementHandler(api),
				v3ManagementHTTPRequest(tc.method, tc.path, body), tc.status)
			if api.applyCalls != 0 || api.inspectCalls != 0 || len(api.denials) != 1 || api.denials[0] != tc.reason {
				t.Fatal("unsupported transport entered management")
			}
		})
	}
	t.Run("audit-failure", func(t *testing.T) {
		server, _, api := v3ManagementHTTPFixture()
		api.auditErr = errors.New("SELECT private-secret FROM private-db")
		request := v3ManagementHTTPRequest(http.MethodPost, "/v3/authority-management/realms", body)
		request.Header.Del("X-CSRF-Token")
		response := v3ManagementHTTPResponse(t, server.V3AuthorityManagementHandler(api), request, http.StatusServiceUnavailable)
		if api.applyCalls != 0 || len(api.denials) != 1 || !strings.Contains(response.Body.String(), "v3_authority_management_unknown") {
			t.Fatal("audit uncertainty became success or bypassed audit")
		}
	})
}

func TestV3AuthorizationManagementHTTPCurrentSessionAndV2Separation(t *testing.T) {
	for _, role := range []string{"member", "super_admin"} {
		t.Run(role, func(t *testing.T) {
			server, session, api := v3ManagementHTTPFixture()
			session.role = role
			v3ManagementHTTPResponse(t, server.V3AuthorityManagementHandler(api),
				v3ManagementHTTPRequest(http.MethodPost, "/v3/authority-management/realms", v3ManagementHTTPRealmBody(t)), http.StatusForbidden)
			if api.applyCalls != 1 || api.actor.Principal.ID != "synthetic-user" {
				t.Fatal("v2 identity was not passed to independent v3 authorization")
			}
		})
	}
	t.Run("session-revoked-between-requests", func(t *testing.T) {
		server, session, api := v3ManagementHTTPFixture()
		api.applied = v3authorization.ManagementResult{Status: v3authorization.ManagementApplied, Generation: 2, Initialized: true}
		handler := server.V3AuthorityManagementHandler(api)
		v3ManagementHTTPResponse(t, handler, v3ManagementHTTPRequest(http.MethodPost, "/v3/authority-management/realms", v3ManagementHTTPRealmBody(t)), http.StatusOK)
		session.revoked = true
		v3ManagementHTTPResponse(t, handler, v3ManagementHTTPRequest(http.MethodPost, "/v3/authority-management/realms", v3ManagementHTTPRealmBody(t)), http.StatusUnauthorized)
		if api.applyCalls != 1 || len(api.denials) != 1 || api.denials[0] != "unauthenticated" {
			t.Fatal("revoked session reused an earlier management result")
		}
	})
	t.Run("v3-decision-not-cached", func(t *testing.T) {
		server, _, api := v3ManagementHTTPFixture()
		api.applied = v3authorization.ManagementResult{Status: v3authorization.ManagementApplied, Generation: 2, Initialized: true}
		handler := server.V3AuthorityManagementHandler(api)
		v3ManagementHTTPResponse(t, handler, v3ManagementHTTPRequest(http.MethodPost, "/v3/authority-management/realms", v3ManagementHTTPRealmBody(t)), http.StatusOK)
		api.applied = v3authorization.ManagementResult{Status: v3authorization.Denied, Code: "private-secret"}
		v3ManagementHTTPResponse(t, handler, v3ManagementHTTPRequest(http.MethodPost, "/v3/authority-management/realms", v3ManagementHTTPRealmBody(t)), http.StatusForbidden)
		if api.applyCalls != 2 {
			t.Fatal("management authorization cached after revocation")
		}
	})
}

func TestV3AuthorizationManagementHTTPResultSafetyAndClosedComposition(t *testing.T) {
	for _, tc := range []struct {
		status v3authorization.Status
		want   int
	}{
		{v3authorization.ManagementApplied, http.StatusOK},
		{v3authorization.ManagementInspected, http.StatusOK},
		{v3authorization.Denied, http.StatusForbidden},
		{v3authorization.Conflict, http.StatusConflict},
		{v3authorization.Invalid, http.StatusBadRequest},
		{v3authorization.Unknown, http.StatusServiceUnavailable},
		{v3authorization.Unavailable, http.StatusServiceUnavailable},
		{v3authorization.Status("private-secret"), http.StatusServiceUnavailable},
	} {
		t.Run(string(tc.status), func(t *testing.T) {
			response := httptest.NewRecorder()
			writeV3ManagementResult(response, v3authorization.ManagementResult{Status: tc.status, Code: "SELECT private-secret"})
			if response.Code != tc.want || strings.Contains(response.Body.String(), "private") || strings.Contains(response.Body.String(), "SELECT") {
				t.Fatal("result leaked errors or became success")
			}
			if response.Header().Get("Cache-Control") != "no-store" {
				t.Fatal("result was cacheable")
			}
		})
	}
	t.Run("failure-and-apply-do-not-echo-records", func(t *testing.T) {
		for _, status := range []v3authorization.Status{v3authorization.Denied, v3authorization.Unknown, v3authorization.ManagementApplied} {
			response := httptest.NewRecorder()
			writeV3ManagementResult(response, v3authorization.ManagementResult{
				Status: status, Code: "private-secret", Generation: 2, Initialized: true,
				Records: []v3authorization.AuthorityRecord{{Kind: v3authorization.RealmRecord, Realm: &v3authorization.RealmRegistration{Realm: "private-secret", State: "ACTIVE"}}},
			})
			if strings.Contains(response.Body.String(), "private-secret") {
				t.Fatal("error/write result echoed record payload")
			}
		}
	})
	t.Run("nil-service", func(t *testing.T) {
		server, _, _ := v3ManagementHTTPFixture()
		var typedNil *v3ManagementHTTPAPI
		for _, api := range []v3authorization.ManagementAPI{nil, typedNil} {
			v3ManagementHTTPResponse(t, server.V3AuthorityManagementHandler(api),
				v3ManagementHTTPRequest(http.MethodPost, "/v3/authority-management/realms", v3ManagementHTTPRealmBody(t)), http.StatusServiceUnavailable)
		}
	})
	t.Run("not-registered-by-default", func(t *testing.T) {
		server := NewServer(store.NewMemoryStreamStore(), WithAuthStore(store.NewMemoryAuthStore()))
		response := httptest.NewRecorder()
		server.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/v3/authority-management/realms", bytes.NewBufferString(v3ManagementHTTPRealmBody(t))))
		if response.Code != http.StatusNotFound {
			t.Fatal("default v2 server exposed management")
		}
	})
}
