package httpapi

import (
	"io"
	"net/http"

	"github.com/example/autostream-control-panel/internal/v3authorization"
)

// V3AuthorizationHandler is an explicit composition point. NewServer does not
// register it, and no environment flag, v2 Grant or administrator role enables
// issuance. The supplied Service must independently authorize the current v3
// principal, target, action and binding. This handler reuses only authentication,
// CSRF and password-change protection from the existing CP session boundary.
func (s *Server) V3AuthorizationHandler(service *v3authorization.Service) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v3/cp-authorizations", s.requirePermission("", func(w http.ResponseWriter, r *http.Request) {
		principal, ok := v3AuthorizationPrincipal(w, r)
		if !ok {
			return
		}
		body, ok := readV3AuthorizationBody(w, r)
		if !ok {
			return
		}
		request, err := v3authorization.DecodeIssueRequest(body)
		if err != nil {
			writeV3AuthorizationResult(w, v3authorization.Result{Status: v3authorization.Invalid})
			return
		}
		if service == nil {
			writeV3AuthorizationResult(w, v3authorization.Result{Status: v3authorization.Unavailable})
			return
		}
		writeV3AuthorizationResult(w, service.Issue(r.Context(), principal, request))
	}))
	mux.HandleFunc("POST /v3/cp-authorizations/lookup", s.requirePermission("", func(w http.ResponseWriter, r *http.Request) {
		principal, ok := v3AuthorizationPrincipal(w, r)
		if !ok {
			return
		}
		body, ok := readV3AuthorizationBody(w, r)
		if !ok {
			return
		}
		query, err := v3authorization.DecodeLookup(body)
		if err != nil {
			writeV3AuthorizationResult(w, v3authorization.Result{Status: v3authorization.Invalid})
			return
		}
		if service == nil {
			writeV3AuthorizationResult(w, v3authorization.Result{Status: v3authorization.Unavailable})
			return
		}
		writeV3AuthorizationResult(w, service.Read(r.Context(), principal, query))
	}))
	return secureHeaders(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Apply even to authentication, decoding, 404 and 405 responses.
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Pragma", "no-cache")
		w.Header().Set("Expires", "0")
		mux.ServeHTTP(w, r)
	}))
}

func v3AuthorizationPrincipal(w http.ResponseWriter, r *http.Request) (v3authorization.Principal, bool) {
	current := currentFromContext(r.Context())
	if current.User.ID == "" || current.User.Status != "active" {
		writeV3AuthorizationResult(w, v3authorization.Result{Status: v3authorization.Denied})
		return v3authorization.Principal{}, false
	}
	return v3authorization.Principal{ID: current.User.ID}, true
}

func readV3AuthorizationBody(w http.ResponseWriter, r *http.Request) ([]byte, bool) {
	if r.Body == nil {
		writeV3AuthorizationResult(w, v3authorization.Result{Status: v3authorization.Invalid})
		return nil, false
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxControlRequestBytes))
	if err != nil {
		writeV3AuthorizationResult(w, v3authorization.Result{Status: v3authorization.Invalid})
		return nil, false
	}
	return body, true
}

func writeV3AuthorizationResult(w http.ResponseWriter, result v3authorization.Result) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("Expires", "0")
	status, code := http.StatusServiceUnavailable, "v3_authorization_unavailable"
	switch result.Status {
	case v3authorization.Issued, v3authorization.Replay:
		// A successful HTTP response never upgrades historical evidence into an
		// execution permit. Integrity covers these exact persisted bytes; generic
		// writeRawJSON would redact/rewrite the cp_authorization payload.
		if len(result.Envelope) == 0 || result.AuthorizesExecution {
			break
		}
		applicability := v3authorization.ApplicabilityUnknown
		switch result.Applicability {
		case v3authorization.Current, v3authorization.Expired, v3authorization.Revoked:
			applicability = result.Applicability
		}
		w.Header().Set("X-Evidence-Result", string(result.Status))
		w.Header().Set("X-Evidence-Applicability", string(applicability))
		w.Header().Set("Content-Type", "application/json")
		if result.Status == v3authorization.Issued {
			w.WriteHeader(http.StatusCreated)
		} else {
			w.WriteHeader(http.StatusOK)
		}
		_, _ = w.Write(result.Envelope)
		return
	case v3authorization.Denied:
		status, code = http.StatusForbidden, "v3_authorization_denied"
	case v3authorization.Conflict:
		status, code = http.StatusConflict, "v3_authorization_conflict"
	case v3authorization.Pending:
		status, code = http.StatusAccepted, "v3_authorization_pending"
	case v3authorization.Unknown:
		status, code = http.StatusServiceUnavailable, "v3_authorization_storage_unknown"
	case v3authorization.Invalid:
		status, code = http.StatusBadRequest, "v3_authorization_invalid"
	}
	// Dependency/SQL/path/key errors and arbitrary Code strings never cross this
	// boundary. HTTP errors are not signed negative Evidence or Backend receipts.
	writeJSON(w, status, map[string]string{"code": code})
}
