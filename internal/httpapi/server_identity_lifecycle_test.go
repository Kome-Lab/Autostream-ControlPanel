package httpapi

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	lf "github.com/example/autostream-control-panel/internal/lifecyclefoundation"
	"github.com/example/autostream-control-panel/internal/store"
)

// These tests exercise the real HTTP middleware/handlers. SQL durability and
// checkpoint behavior are covered separately by the tagged Identity fixtures.
type identityHTTPAuth struct {
	store.AuthStore
	required bool
	stage    string
	failure  error
	status   string
	calls    []string
}

func (a *identityHTTPAuth) IdentityLifecycleRequired() bool { return a.required }
func (a *identityHTTPAuth) result(stage string) error {
	a.calls = append(a.calls, stage)
	if a.stage == stage {
		return a.failure
	}
	return nil
}
func (a *identityHTTPAuth) GetSession(context.Context, string) (store.Session, error) {
	err := a.result("session")
	return store.Session{Token: "synthetic-session-token", UserID: "synthetic-user", CSRFToken: "synthetic-csrf-token"}, err
}
func (a *identityHTTPAuth) GetUser(context.Context, string) (store.User, error) {
	err := a.result("user")
	status := a.status
	if status == "" {
		status = "active"
	}
	return store.User{ID: "synthetic-user", Status: status}, err
}
func (a *identityHTTPAuth) GetUserPermissions(context.Context, string) ([]string, error) {
	return []string{"streams.view"}, a.result("permissions")
}
func (a *identityHTTPAuth) DeleteSession(context.Context, string) error {
	return a.result("delete")
}
func (a *identityHTTPAuth) CreateSession(context.Context, string, time.Duration, time.Duration) (store.Session, error) {
	return store.Session{Token: "legacy-token"}, a.result("legacy-create")
}
func (a *identityHTTPAuth) RecordLoginSuccess(context.Context, string, string) error {
	return a.result("legacy-success")
}

type identityHTTPLoginAuth struct{ *identityHTTPAuth }

func (a identityHTTPLoginAuth) CompleteIdentityLogin(context.Context, store.User, string, time.Duration, time.Duration) (store.Session, error) {
	err := a.result("complete-login")
	return store.Session{Token: "synthetic-unconfirmed-token", CSRFToken: "synthetic-unconfirmed-csrf"}, err
}

func identityHTTPRequest(method, target string) *http.Request {
	r := httptest.NewRequest(method, target, nil)
	r.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "synthetic-session-token"})
	return r
}

func identityHTTPAssertClosed(t *testing.T, w *httptest.ResponseRecorder, code string) {
	t.Helper()
	if w.Code != http.StatusServiceUnavailable || !strings.Contains(w.Body.String(), `"code":"`+code+`"`) {
		t.Fatalf("unexpected lifecycle response: status=%d body=%s", w.Code, w.Body.String())
	}
	if len(w.Result().Cookies()) != 0 || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("uncertain identity response emitted a cookie or was cacheable")
	}
	for _, secret := range []string{"synthetic-session-token", "synthetic-csrf-token", "synthetic-unconfirmed-token", "synthetic-unconfirmed-csrf", "opaque-source-detail"} {
		if strings.Contains(w.Body.String(), secret) {
			t.Fatal("identity failure exposed a captured result or source detail")
		}
	}
}

func TestB2HTTPIdentityUnknownUserState(t *testing.T) {
	auth := &identityHTTPAuth{required: true, status: "unknown"}
	s := &Server{auth: auth}
	w := httptest.NewRecorder()
	s.requirePermission("", func(http.ResponseWriter, *http.Request) {
		t.Fatal("unknown User state reached protected handler")
	})(w, identityHTTPRequest(http.MethodGet, "/auth/me"))
	identityHTTPAssertClosed(t, w, "identity_lifecycle_held")
}

func TestB2HTTPIdentityAuthenticationErrors(t *testing.T) {
	for _, stage := range []string{"session", "user", "permissions"} {
		for _, middleware := range []string{"permission", "any-permission"} {
			t.Run(stage+"/"+middleware, func(t *testing.T) {
				a := &identityHTTPAuth{required: true, stage: stage, failure: lf.ErrUnknown}
				s := &Server{auth: a}
				called := false
				next := func(http.ResponseWriter, *http.Request) { called = true }
				handler := s.requirePermission("", next)
				if middleware == "any-permission" {
					handler = s.requireAnyPermission(nil, next)
				}
				w := httptest.NewRecorder()
				handler(w, identityHTTPRequest(http.MethodGet, "/auth/me"))
				identityHTTPAssertClosed(t, w, "identity_lifecycle_unknown")
				if called {
					t.Fatal("uncertain source reached the authorized handler")
				}
			})
		}
	}
}

func TestB2HTTPIdentitySourceFailureAndLegacyDenial(t *testing.T) {
	for _, required := range []bool{true, false} {
		a := &identityHTTPAuth{required: required, stage: "permissions", failure: errors.New("opaque-source-detail")}
		s := &Server{auth: a}
		w := httptest.NewRecorder()
		s.requirePermission("", func(http.ResponseWriter, *http.Request) { t.Fatal("source failure authorized request") })(w, identityHTTPRequest(http.MethodGet, "/auth/me"))
		if required {
			identityHTTPAssertClosed(t, w, "identity_lifecycle_held")
		} else if w.Code != http.StatusUnauthorized {
			t.Fatalf("legacy denial changed: %d", w.Code)
		}
	}
}

func TestB2HTTPIdentityLogoutUnknown(t *testing.T) {
	a := &identityHTTPAuth{required: true, stage: "delete", failure: lf.ErrUnknown}
	s := &Server{auth: a}
	w := httptest.NewRecorder()
	s.logout(w, identityHTTPRequest(http.MethodPost, "/auth/logout"))
	identityHTTPAssertClosed(t, w, "identity_lifecycle_unknown")
	if strings.Join(a.calls, ",") != "delete" {
		t.Fatal("logout repeated or bypassed its one deletion attempt")
	}
}

func TestB2HTTPIdentityLoginUnknown(t *testing.T) {
	a := &identityHTTPAuth{required: true, stage: "complete-login", failure: lf.ErrUnknown}
	s := &Server{auth: identityHTTPLoginAuth{a}}
	w := httptest.NewRecorder()
	s.completeLogin(w, identityHTTPRequest(http.MethodPost, "/auth/login"), store.User{ID: "synthetic-user"}, store.SecuritySettings{})
	identityHTTPAssertClosed(t, w, "identity_lifecycle_unknown")
	if strings.Join(a.calls, ",") != "complete-login" {
		t.Fatal("uncertain login used separate legacy issuance or bookkeeping")
	}
}

func TestB2HTTPIdentityMissingLoginComposition(t *testing.T) {
	a := &identityHTTPAuth{required: true}
	s := &Server{auth: a}
	w := httptest.NewRecorder()
	s.completeLogin(w, identityHTTPRequest(http.MethodPost, "/auth/login"), store.User{ID: "synthetic-user"}, store.SecuritySettings{})
	identityHTTPAssertClosed(t, w, "identity_lifecycle_held")
	if len(a.calls) != 0 {
		t.Fatal("missing Identity owner fell back to legacy Session issuance")
	}
}

func TestB2HTTPIdentityPendingOnboarding(t *testing.T) {
	for _, target := range []string{"/auth/me", "/streams"} {
		a := &identityHTTPAuth{required: true, status: "pending_password_change"}
		s := &Server{auth: a}
		called := false
		w := httptest.NewRecorder()
		s.requireAnyPermission(nil, func(w http.ResponseWriter, _ *http.Request) {
			called = true
			w.WriteHeader(http.StatusNoContent)
		})(w, identityHTTPRequest(http.MethodGet, target))
		if target == "/auth/me" {
			if !called || w.Code != http.StatusNoContent {
				t.Fatal("pending onboarding lost its existing me route")
			}
		} else if called || w.Code != http.StatusForbidden || !strings.Contains(w.Body.String(), "password_change_required") {
			t.Fatal("pending onboarding gained an unrelated authenticated route")
		}
	}
}

func TestB2HTTPIdentityUnknownCodePriority(t *testing.T) {
	s := &Server{}
	w := httptest.NewRecorder()
	if !s.writeIdentityLifecycleFailure(w, errors.Join(lf.ErrHeld, lf.ErrUnknown, errors.New("opaque-source-detail"))) {
		t.Fatal("joined Lifecycle uncertainty was not recognized")
	}
	identityHTTPAssertClosed(t, w, "identity_lifecycle_unknown")
}

func TestB2HTTPIdentityCompositionClosedBeforeMux(t *testing.T) {
	requests := []struct {
		method string
		target string
	}{
		{http.MethodPost, "/auth/login"},
		{http.MethodPost, "/auth/change-password"},
		{http.MethodPost, "/auth/session/refresh"},
		{http.MethodPost, "/auth/mfa/enroll"},
		{http.MethodPost, "/auth/mfa/verify"},
		{http.MethodPost, "/auth/recovery-codes/regenerate"},
		{http.MethodPost, "/auth/passkeys/login/finish"},
		{http.MethodPost, "/auth/passkeys/register/start"},
		{http.MethodPut, "/auth/email"},
		{http.MethodPost, "/auth/email/confirm"},
		{http.MethodGet, "/auth/oauth/callback"},
		{http.MethodPost, "/auth/oauth/provider-1/start"},
		{http.MethodPost, "/auth/oauth-links/provider-1/start"},
		{http.MethodGet, "/integrations/oauth-accounts/callback"},
		{http.MethodPost, "/integrations/oauth-accounts/start"},
		{http.MethodDelete, "/integrations/oauth-accounts/account-1"},
		{http.MethodPost, "/integrations/oauth-providers"},
		{http.MethodGet, "/setup/status"},
		{http.MethodPost, "/setup/first-admin"},
		{http.MethodPut, "/security/settings"},
		{http.MethodGet, "/settings/app"},
		{http.MethodPost, "/settings/app/test-email"},
		{http.MethodPut, "/secrets/app.smtp.password"},
		{http.MethodPost, "/users"},
		{http.MethodPost, "/users/user-1/reset-password"},
		{http.MethodDelete, "/users/user-1/oauth-links/link-1"},
		{http.MethodPut, "/roles/role-1"},
		{http.MethodGet, "/permissions"},
		{http.MethodHead, "/auth/me"},
		{http.MethodGet, "/auth/logout"},
	}
	for _, request := range requests {
		t.Run(request.method+" "+request.target, func(t *testing.T) {
			a := &identityHTTPAuth{required: true}
			s := NewServer(store.NewMemoryStreamStore(), WithAuthStore(a))
			// Any request reaching the composed handler could execute Policy,
			// OAuth, SMTP or Turnstile I/O. This marker makes that a hard failure.
			s.handler = http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
				t.Fatal("unqualified Identity request reached the composed handler")
			})
			w := httptest.NewRecorder()
			s.ServeHTTP(w, identityHTTPRequest(request.method, request.target))
			identityHTTPAssertClosed(t, w, "identity_lifecycle_held")
			if len(a.calls) != 0 {
				t.Fatal("unqualified HTTP composition accessed an Identity source")
			}
		})
	}
}

func TestB2HTTPIdentityCompositionRetainsProtectedMiddleware(t *testing.T) {
	for _, request := range []struct {
		method string
		target string
	}{{http.MethodGet, "/auth/me"}, {http.MethodPost, "/auth/logout"}} {
		a := &identityHTTPAuth{required: true, stage: "session", failure: lf.ErrUnknown}
		s := NewServer(store.NewMemoryStreamStore(), WithAuthStore(a))
		w := httptest.NewRecorder()
		s.ServeHTTP(w, identityHTTPRequest(request.method, request.target))
		identityHTTPAssertClosed(t, w, "identity_lifecycle_unknown")
		if strings.Join(a.calls, ",") != "session" {
			t.Fatal("retained route skipped its protected authentication middleware")
		}
	}
}

func TestB2HTTPIdentityCompositionLeavesOtherDomainsAndLegacy(t *testing.T) {
	for _, required := range []bool{true, false} {
		for _, target := range []string{"/health", "/streams/stream-1/start", "/services/service-1/heartbeat", "/archive/destinations", "/auth/login"} {
			if required && target == "/auth/login" {
				continue
			}
			a := &identityHTTPAuth{required: required}
			s := NewServer(store.NewMemoryStreamStore(), WithAuthStore(a))
			called := false
			s.handler = http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				called = true
				w.WriteHeader(http.StatusNoContent)
			})
			w := httptest.NewRecorder()
			s.ServeHTTP(w, identityHTTPRequest(http.MethodPost, target))
			if !called || w.Code != http.StatusNoContent {
				t.Fatal("Identity composition boundary intercepted another domain or legacy owner")
			}
		}
	}
}
