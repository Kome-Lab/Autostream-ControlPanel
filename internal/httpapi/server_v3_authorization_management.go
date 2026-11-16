package httpapi

import (
	"io"
	"net/http"
	"reflect"

	"github.com/example/autostream-control-panel/internal/security"
	"github.com/example/autostream-control-panel/internal/v3authorization"
)

const maxV3AuthorityManagementBodyBytes = 64 << 10

// V3AuthorityManagementHandler is an explicit, separate composition point.
// Session authentication supplies identity only. Current v3 management grants,
// session revalidation, CAS and atomic audit/save belong to the supplied API.
// No v2 role, wildcard permission, environment flag or default server enables it.
// Bootstrap deliberately has no HTTP route while its trust root is undecided.
func (s *Server) V3AuthorityManagementHandler(service v3authorization.ManagementAPI) http.Handler {
	return secureHeaders(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Pragma", "no-cache")
		w.Header().Set("Expires", "0")
		if v3ManagementMissing(service) {
			writeV3ManagementResult(w, v3authorization.ManagementResult{Status: v3authorization.Unavailable})
			return
		}
		current, authenticated := s.authenticate(r)
		if !authenticated || current.User.ID == "" || current.Session.TokenHash == "" {
			v3ManagementTransportDenial(w, r, service, v3authorization.Principal{}, "unauthenticated")
			return
		}
		principal := v3authorization.Principal{ID: current.User.ID}
		if current.User.Status != "active" {
			v3ManagementTransportDenial(w, r, service, principal, "inactive_principal")
			return
		}
		if isUnsafeMethod(r.Method) && !security.VerifyTokenHash(r.Header.Get("X-CSRF-Token"), current.Session.CSRFTokenHash) {
			v3ManagementTransportDenial(w, r, service, principal, "csrf_denied")
			return
		}
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", http.MethodPost)
			v3ManagementTransportDenial(w, r, service, principal, "method_denied")
			return
		}
		var kind v3authorization.AuthorityRecordKind
		administrators, inspection := false, false
		switch r.URL.Path {
		case "/v3/authority-management/realms":
			kind = v3authorization.RealmRecord
		case "/v3/authority-management/hosts":
			kind = v3authorization.HostRecord
		case "/v3/authority-management/resources":
			kind = v3authorization.ResourceRecord
		case "/v3/authority-management/backends":
			kind = v3authorization.BackendRecord
		case "/v3/authority-management/candidates":
			kind = v3authorization.CandidateRecord
		case "/v3/authority-management/capabilities":
			kind = v3authorization.CapabilityRecord
		case "/v3/authority-management/policies":
			kind = v3authorization.PolicyRecord
		case "/v3/authority-management/trust":
			kind = v3authorization.TrustRecord
		case "/v3/authority-management/memberships":
			kind = v3authorization.MembershipRecord
		case "/v3/authority-management/administrators":
			administrators = true
		case "/v3/authority-management/inspect":
			inspection = true
		default:
			v3ManagementTransportDenial(w, r, service, principal, "route_not_found")
			return
		}
		if r.Body == nil {
			v3ManagementTransportDenial(w, r, service, principal, "body_invalid")
			return
		}
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxV3AuthorityManagementBodyBytes))
		if err != nil {
			v3ManagementTransportDenial(w, r, service, principal, "body_invalid")
			return
		}
		actor := v3authorization.ManagementActor{Principal: principal, SessionHash: current.Session.TokenHash}
		if inspection {
			metadata, err := v3authorization.DecodeManagementInspection(body)
			if err != nil {
				v3ManagementTransportDenial(w, r, service, principal, "body_invalid")
				return
			}
			writeV3ManagementResult(w, service.Inspect(r.Context(), actor, metadata))
			return
		}
		if administrators {
			metadata, grants, err := v3authorization.DecodeManagementAdministrators(body)
			if err != nil {
				v3ManagementTransportDenial(w, r, service, principal, "body_invalid")
				return
			}
			writeV3ManagementResult(w, service.Apply(r.Context(), actor, metadata, nil, grants))
			return
		}
		metadata, records, err := v3authorization.DecodeManagementRecords(body, kind)
		if err != nil {
			v3ManagementTransportDenial(w, r, service, principal, "body_invalid")
			return
		}
		writeV3ManagementResult(w, service.Apply(r.Context(), actor, metadata, records, nil))
	}))
}

func v3ManagementMissing(service v3authorization.ManagementAPI) bool {
	if service == nil {
		return true
	}
	value := reflect.ValueOf(service)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return value.IsNil()
	default:
		return false
	}
}

// Only these fixed, trusted transport codes are passed to audit or returned.
// Arbitrary paths, payloads, SQL errors, tokens and service Code strings never
// become error response data. Audit uncertainty never becomes operation success.
func v3ManagementTransportDenial(w http.ResponseWriter, r *http.Request, service v3authorization.ManagementAPI, principal v3authorization.Principal, reason string) {
	status, code := http.StatusServiceUnavailable, "v3_authority_management_unavailable"
	switch reason {
	case "unauthenticated":
		status, code = http.StatusUnauthorized, "v3_authority_management_unauthenticated"
	case "inactive_principal":
		status, code = http.StatusForbidden, "v3_authority_management_inactive_principal"
	case "csrf_denied":
		status, code = http.StatusForbidden, "v3_authority_management_csrf_denied"
	case "method_denied":
		status, code = http.StatusMethodNotAllowed, "v3_authority_management_method_denied"
	case "route_not_found":
		status, code = http.StatusNotFound, "v3_authority_management_route_not_found"
	case "body_invalid":
		status, code = http.StatusBadRequest, "v3_authority_management_body_invalid"
	default:
		writeV3ManagementResult(w, v3authorization.ManagementResult{Status: v3authorization.Unavailable})
		return
	}
	if service.RecordTransportDenial(r.Context(), principal, reason) != nil {
		writeV3ManagementResult(w, v3authorization.ManagementResult{Status: v3authorization.Unknown})
		return
	}
	writeJSON(w, status, map[string]string{"code": code})
}

func writeV3ManagementResult(w http.ResponseWriter, result v3authorization.ManagementResult) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("Expires", "0")
	status, code := http.StatusServiceUnavailable, "v3_authority_management_unavailable"
	publicStatus := v3authorization.Unavailable
	switch result.Status {
	case v3authorization.ManagementApplied, v3authorization.ManagementInspected:
		if result.Status == v3authorization.ManagementApplied {
			code = "v3_authority_management_applied"
		} else {
			code = "v3_authority_management_inspected"
		}
		response := struct {
			Status         v3authorization.Status            `json:"status"`
			Code           string                            `json:"code"`
			Generation     uint64                            `json:"generation"`
			Initialized    bool                              `json:"initialized"`
			Records        []v3authorization.AuthorityRecord `json:"records,omitempty"`
			Administrators []v3authorization.ManagementGrant `json:"administrators,omitempty"`
		}{Status: result.Status, Code: code, Generation: result.Generation, Initialized: result.Initialized}
		if result.Status == v3authorization.ManagementInspected {
			response.Records = result.Records
			response.Administrators = result.Administrators
		}
		writeJSON(w, http.StatusOK, response)
		return
	case v3authorization.Denied:
		status, code, publicStatus = http.StatusForbidden, "v3_authority_management_denied", v3authorization.Denied
	case v3authorization.Conflict:
		status, code, publicStatus = http.StatusConflict, "v3_authority_management_conflict", v3authorization.Conflict
	case v3authorization.Invalid:
		status, code, publicStatus = http.StatusBadRequest, "v3_authority_management_invalid", v3authorization.Invalid
	case v3authorization.Unknown:
		code, publicStatus = "v3_authority_management_unknown", v3authorization.Unknown
	}
	writeJSON(w, status, map[string]string{"status": string(publicStatus), "code": code})
}
