package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	lf "github.com/example/autostream-control-panel/internal/lifecyclefoundation"
	"github.com/example/autostream-control-panel/internal/store"
)

type identityLifecycleOwner interface {
	IdentityLifecycleRequired() bool
}

type identityLoginOwner interface {
	CompleteIdentityLogin(context.Context, store.User, string, time.Duration, time.Duration) (store.Session, error)
}

type identityPasswordOwner interface {
	ChangeIdentityPassword(context.Context, store.User, string) error
}

func (s *Server) identityLifecycleRequired() bool {
	owner, ok := s.auth.(identityLifecycleOwner)
	return ok && owner.IdentityLifecycleRequired()
}

// Identity source failures must not become an empty permission set, a missing
// user, or successful logout. The existing confirmed authentication denials keep
// their normal status; all other source failures in an explicit L2 owner close.
func (s *Server) identitySourceError(err error) error {
	if err == nil || !s.identityLifecycleRequired() || errors.Is(err, store.ErrNotFound) || errors.Is(err, store.ErrUnauthorized) {
		return err
	}
	if errors.Is(err, lf.ErrUnknown) || errors.Is(err, lf.ErrHeld) {
		return err
	}
	return errors.Join(lf.ErrHeld, err)
}

func (s *Server) identityMutationError(err error) error {
	if err == nil || !s.identityLifecycleRequired() || errors.Is(err, lf.ErrUnknown) || errors.Is(err, lf.ErrHeld) {
		return err
	}
	return errors.Join(lf.ErrHeld, err)
}

func (s *Server) writeIdentityLifecycleFailure(w http.ResponseWriter, err error) bool {
	code := ""
	switch {
	case errors.Is(err, lf.ErrUnknown):
		code = "identity_lifecycle_unknown"
	case errors.Is(err, lf.ErrHeld), errors.Is(err, lf.ErrConflict):
		code = "identity_lifecycle_held"
	default:
		return false
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusServiceUnavailable, map[string]string{"code": code})
	return true
}

// The legacy audit writer is not an Identity transaction participant. Its L2
// denial/success path remains explicitly unconnected rather than opening an
// untracked transaction after an admission or an uncertain result.
func (s *Server) writeIdentityAudit(r *http.Request, event store.AuditEvent) {
	if !s.identityLifecycleRequired() {
		s.writeAudit(r, event)
	}
}

// B2 supplies Store participation, not a qualified HTTP composition for Policy,
// Secret, OAuth or MFA. Reject those entrypoints before routing or external I/O.
// No setting or Server option enables this incomplete composition. The two
// retained routes use only the protected Identity owner and existing CSRF checks.
func (s *Server) identityHTTPCompositionClosed(w http.ResponseWriter, r *http.Request) bool {
	if !s.identityLifecycleRequired() {
		return false
	}
	if r.Method == http.MethodGet && r.URL.Path == "/auth/me" || r.Method == http.MethodPost && r.URL.Path == "/auth/logout" {
		return false
	}
	for _, namespace := range []string{
		"/auth", "/setup", "/users", "/roles", "/permissions",
		"/security/settings", "/settings/app", "/secrets",
		"/integrations/oauth-providers", "/integrations/oauth-accounts",
	} {
		if r.URL.Path == namespace || strings.HasPrefix(r.URL.Path, namespace+"/") {
			secureHeaders(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				s.writeIdentityLifecycleFailure(w, lf.ErrHeld)
			})).ServeHTTP(w, r)
			return true
		}
	}
	return false
}
