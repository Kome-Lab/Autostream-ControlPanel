package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	evidence "github.com/Kome-Lab/Autostream-Contracts/v3evidence"
	"github.com/example/autostream-control-panel/internal/security"
	"github.com/example/autostream-control-panel/internal/store"
	"github.com/example/autostream-control-panel/internal/v3authorization"
)

// Only the existing CP authentication boundary is doubled here. No signer,
// clock, v3 authority, provider, credential or server process is installed.
type v3AuthorizationSessionFixture struct {
	store.AuthStore
	status string
}

func (f v3AuthorizationSessionFixture) GetSession(ctx context.Context, token string) (store.Session, error) {
	if ctx.Err() != nil {
		return store.Session{}, ctx.Err()
	}
	if token != "synthetic-session" {
		return store.Session{}, store.ErrUnauthorized
	}
	return store.Session{UserID: "synthetic-user", CSRFTokenHash: security.HashToken("synthetic-csrf")}, nil
}

func (f v3AuthorizationSessionFixture) GetUser(context.Context, string) (store.User, error) {
	return store.User{ID: "synthetic-user", Status: f.status, Roles: []string{"super_admin"}}, nil
}

func (f v3AuthorizationSessionFixture) GetUserPermissions(context.Context, string) ([]string, error) {
	// A v2 administrator wildcard still cannot supply missing v3 dependencies.
	return []string{"*", "system_updates.execute"}, nil
}

func v3AuthorizationHTTPFixtureRequest(path, body string) *http.Request {
	request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "synthetic-session"})
	request.Header.Set("X-CSRF-Token", "synthetic-csrf")
	return request
}

func v3AuthorizationHTTPFixtureBodies(t *testing.T) (string, string) {
	t.Helper()
	issue, err := json.Marshal(v3authorization.IssueRequest{
		OriginalRequest: json.RawMessage(`{"binding":"synthetic-binding-only"}`),
		Frame: evidence.BackendReadFrame{
			Issuer: "backend", ServingIncarnation: "incarnation", Generation: "generation",
			SnapshotID: "snapshot", ObservationProof: "observation",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	query, err := json.Marshal(v3authorization.Lookup{
		Scope: evidence.Scope{Realm: "realm", Host: "host", Resource: "resource", Backend: "backend"},
		Execution: evidence.Execution{RequestID: "request", ExecutionKey: "execution", OperationID: "operation", AttemptID: "attempt", StepID: "step"},
	})
	if err != nil {
		t.Fatal(err)
	}
	return string(issue), string(query)
}

func TestV3AuthorizationHandlerAuthenticationAndClosedComposition(t *testing.T) {
	issue, lookup := v3AuthorizationHTTPFixtureBodies(t)
	for _, endpoint := range []struct{ path, body string }{
		{"/v3/cp-authorizations", issue},
		{"/v3/cp-authorizations/lookup", lookup},
	} {
		t.Run(endpoint.path, func(t *testing.T) {
			for _, tc := range []struct {
				name, status string
				cookie, csrf bool
				want int
			}{
				{"unauthenticated", "active", false, true, http.StatusUnauthorized},
				{"csrf-required", "active", true, false, http.StatusForbidden},
				{"password-change", "pending_password_change", true, true, http.StatusForbidden},
				{"disabled", "disabled", true, true, http.StatusUnauthorized},
				{"locked", "locked", true, true, http.StatusUnauthorized},
				{"other-status", "pending", true, true, http.StatusForbidden},
				{"v2-admin-is-not-v3-authority", "active", true, true, http.StatusServiceUnavailable},
			} {
				t.Run(tc.name, func(t *testing.T) {
					server := &Server{auth: v3AuthorizationSessionFixture{status: tc.status}}
					handler := server.V3AuthorizationHandler(nil)
					request := v3AuthorizationHTTPFixtureRequest(endpoint.path, endpoint.body)
					if !tc.cookie {
						request.Header.Del("Cookie")
					}
					if !tc.csrf {
						request.Header.Del("X-CSRF-Token")
					}
					response := httptest.NewRecorder()
					handler.ServeHTTP(response, request)
					if response.Code != tc.want || response.Header().Get("Cache-Control") != "no-store" {
						t.Fatalf("status=%d, cache=%q, want=%d", response.Code, response.Header().Get("Cache-Control"), tc.want)
					}
					if response.Header().Get("X-Evidence-Result") != "" {
						t.Fatal("failed request appeared to issue evidence")
					}
				})
			}
		})
	}
}

func TestV3AuthorizationHandlerStrictBodyBoundary(t *testing.T) {
	server := &Server{auth: v3AuthorizationSessionFixture{status: "active"}}
	handler := server.V3AuthorizationHandler(nil)
	for name, body := range map[string]string{
		"empty": "",
		"missing": `{}`,
		"extra": `{"original_request":{},"backend_read_frame":{},"principal":"attacker"}`,
		"duplicate": `{"original_request":{},"original_request":{},"backend_read_frame":{}}`,
		"case": `{"Original_Request":{},"backend_read_frame":{}}`,
		"null": `{"original_request":null,"backend_read_frame":{}}`,
		"trailing": `{} {}`,
		"oversize": strings.Repeat(" ", maxControlRequestBytes+1),
	} {
		t.Run(name, func(t *testing.T) {
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, v3AuthorizationHTTPFixtureRequest("/v3/cp-authorizations", body))
			if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "v3_authorization_invalid") {
				t.Fatalf("invalid body status=%d", response.Code)
			}
		})
	}
}

func TestV3AuthorizationResultDeliversExactBytesWithoutRedaction(t *testing.T) {
	// Deliberate whitespace and an authorization-named field detect accidental
	// JSON reserialization/redaction. This tests transport, not authenticity.
	wire := []byte(" {\"cp_authorization\": {\"decision\":\"AUTHORIZED\"},\"proof\":\"synthetic-proof\"} \n")
	for _, status := range []v3authorization.Status{v3authorization.Issued, v3authorization.Replay} {
		for _, applicability := range []v3authorization.Applicability{
			v3authorization.Current, v3authorization.Expired, v3authorization.Revoked, v3authorization.ApplicabilityUnknown,
		} {
			response := httptest.NewRecorder()
			writeV3AuthorizationResult(response, v3authorization.Result{Status: status, Envelope: wire, Applicability: applicability})
			want := http.StatusOK
			if status == v3authorization.Issued {
				want = http.StatusCreated
			}
			if response.Code != want || !bytes.Equal(response.Body.Bytes(), wire) {
				t.Fatal("evidence transport rewrote bytes or status")
			}
			if response.Header().Get("X-Evidence-Result") != string(status) || response.Header().Get("X-Evidence-Applicability") != string(applicability) {
				t.Fatal("historical evidence applicability was lost")
			}
		}
	}
}

func TestV3AuthorizationResultErrorsAreSafeAndNotNegativeEvidence(t *testing.T) {
	for _, tc := range []struct {
		status v3authorization.Status
		want int
	}{
		{v3authorization.Denied, http.StatusForbidden},
		{v3authorization.Conflict, http.StatusConflict},
		{v3authorization.Pending, http.StatusAccepted},
		{v3authorization.Unknown, http.StatusServiceUnavailable},
		{v3authorization.Unavailable, http.StatusServiceUnavailable},
		{v3authorization.Invalid, http.StatusBadRequest},
		{v3authorization.Status("unexpected"), http.StatusServiceUnavailable},
	} {
		response := httptest.NewRecorder()
		writeV3AuthorizationResult(response, v3authorization.Result{
			Status: tc.status, Code: "/private/db SELECT key=secret", Envelope: []byte("not-evidence"),
		})
		if response.Code != tc.want || strings.Contains(response.Body.String(), "private") || strings.Contains(response.Body.String(), "secret") || strings.Contains(response.Body.String(), "not-evidence") {
			t.Fatal("unsafe dependency details leaked or status mismatch")
		}
		if response.Header().Get("X-Evidence-Result") != "" {
			t.Fatal("HTTP error represented as issued negative evidence")
		}
	}
	for _, result := range []v3authorization.Result{
		{Status: v3authorization.Issued},
		{Status: v3authorization.Replay, Envelope: []byte(`{}`), AuthorizesExecution: true},
	} {
		response := httptest.NewRecorder()
		writeV3AuthorizationResult(response, result)
		if response.Code != http.StatusServiceUnavailable {
			t.Fatal("invalid success escaped the transport boundary")
		}
	}
}

func TestV3AuthorizationNotRegisteredByDefault(t *testing.T) {
	server := NewServer(store.NewMemoryStreamStore(), WithAuthStore(v3AuthorizationSessionFixture{status: "active"}))
	for _, path := range []string{"/v3/cp-authorizations", "/v3/cp-authorizations/lookup"} {
		response := httptest.NewRecorder()
		server.ServeHTTP(response, v3AuthorizationHTTPFixtureRequest(path, `{}`))
		if response.Code != http.StatusNotFound {
			t.Fatalf("v3 handler was enabled by default: %d", response.Code)
		}
	}
}
