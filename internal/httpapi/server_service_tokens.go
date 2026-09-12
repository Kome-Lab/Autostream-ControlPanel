package httpapi

import (
	"errors"
	"net/http"
	"strings"

	"github.com/example/autostream-control-panel/internal/security"
	"github.com/example/autostream-control-panel/internal/store"
)

func validateServiceTokenScopePermissions(actorPermissions, scopes []string) error {
	required := map[string]string{
		"service.secret.resolve": "secrets.update",
		"remediation.execute":    "remediation.execute",
		"streams.start":          "streams.start",
		"streams.stop":           "streams.stop",
		"updates.claim":          "system_updates.execute",
		"updates.report":         "system_updates.execute",
		"updates.authorize":      "system_updates.execute",
	}
	for _, scope := range scopes {
		permission := required[strings.TrimSpace(scope)]
		if permission == "" {
			continue
		}
		if !security.HasPermission(actorPermissions, permission) {
			return store.ErrPermissionEscalation
		}
	}
	return nil
}

func validUpdateAgentServiceTokenScopes(serviceType string, scopes []string) bool {
	return store.ValidateRequiredUpdateAgentScopes(serviceType, scopes) == nil
}

func validateNodeConfigurationSecretPermissions(actorPermissions []string, serviceType string) error {
	switch strings.TrimSpace(serviceType) {
	case "worker", "encoder_recorder", "update_agent":
		if !security.HasPermission(actorPermissions, "secrets.update") {
			return store.ErrPermissionEscalation
		}
	}
	return nil
}

func (s *Server) requireNodeTokenScopePermissions(w http.ResponseWriter, r *http.Request, service store.RegisteredService) bool {
	token, failure := s.selectNodeActionToken(r.Context(), nodeActionRuntimeTokenRotate, service)
	if failure != nil {
		writeJSON(w, failure.status, map[string]string{"code": failure.code})
		return false
	}
	return validateNodeTokenScopePermissions(w, r, service, token)
}

func (s *Server) requireNodeConfigureTokenScopePermissions(
	w http.ResponseWriter,
	r *http.Request,
	service store.RegisteredService,
) bool {
	token, failure := s.selectNodeActionToken(r.Context(), nodeActionConfigureTokenRegenerate, service)
	if failure != nil {
		writeJSON(w, failure.status, map[string]string{"code": failure.code})
		return false
	}
	return validateNodeTokenScopePermissions(w, r, service, token)
}

func validateNodeTokenScopePermissions(
	w http.ResponseWriter,
	r *http.Request,
	service store.RegisteredService,
	token store.ServiceToken,
) bool {
	authority := evaluateNodeTokenAuthority(currentFromContext(r.Context()).Permissions, service, token)
	if authority.reason == nodeAuthorityInvalidServiceScope {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_service_scope"})
		return false
	}
	if len(authority.missingPermissions) > 0 {
		writeJSON(w, http.StatusForbidden, map[string]string{"code": "permission_escalation"})
		return false
	}
	return true
}

func (s *Server) revokeServiceToken(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tokens, err := s.services.ListServiceTokens(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "list_service_tokens_failed"})
		return
	}
	var matchedToken *store.ServiceToken
	for _, token := range tokens {
		if token.ID != id {
			continue
		}
		tokenCopy := token
		matchedToken = &tokenCopy
		break
	}
	if matchedToken == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found"})
		return
	}
	updaterToken := matchedToken.RevokedAt == nil && matchedToken.ServiceType == "update_agent"
	if updaterToken {
		s.systemUpdateOperationMu.Lock()
		defer s.systemUpdateOperationMu.Unlock()
	}
	services, err := s.services.ListServices(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "list_services_failed"})
		return
	}
	for _, service := range services {
		if updaterToken && service.TokenID == id && service.ServiceType == "update_agent" &&
			s.rejectUpdaterIdentityMutationDuringActiveWork(w, r, service.ServiceID) {
			return
		}
	}
	for _, service := range services {
		if service.TokenID == id && (service.NodeTokenCiphertext != "" || service.NodeTokenNonce != "") {
			writeJSON(w, http.StatusConflict, map[string]string{"code": "node_runtime_token_requires_node_deletion"})
			return
		}
	}
	err = s.services.RevokeServiceToken(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "revoke_api_token_failed"})
		return
	}
	current := currentFromContext(r.Context())
	s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "api_tokens.revoke", ResourceType: "service_token", ResourceID: id, Result: "success"})
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) rotateServiceToken(w http.ResponseWriter, r *http.Request) {
	current := currentFromContext(r.Context())
	if !security.HasPermission(current.Permissions, "api_tokens.revoke") {
		writeJSON(w, http.StatusForbidden, map[string]string{"code": "permission_denied"})
		return
	}
	id := r.PathValue("id")
	tokens, err := s.services.ListServiceTokens(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "list_service_tokens_failed"})
		return
	}
	var existing store.ServiceToken
	for _, candidate := range tokens {
		if candidate.ID == id && candidate.RevokedAt == nil {
			existing = candidate
			break
		}
	}
	if existing.ID == "" {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found"})
		return
	}
	if !validUpdateAgentServiceTokenScopes(existing.ServiceType, existing.Scopes) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_service_scope"})
		return
	}
	// Rotation may apply a narrowly-scoped compatibility upgrade. Authorize the
	// replacement token's projected scopes so a caller cannot use generic token
	// rotation to gain a permission absent from their own role.
	if err := validateServiceTokenScopePermissions(current.Permissions, store.ProjectedServiceTokenScopesForRotation(existing)); err != nil {
		writeJSON(w, http.StatusForbidden, map[string]string{"code": "permission_escalation"})
		return
	}
	if existing.ServiceType == "update_agent" {
		if err := validateNodeConfigurationSecretPermissions(current.Permissions, existing.ServiceType); err != nil {
			writeJSON(w, http.StatusForbidden, map[string]string{"code": "permission_escalation"})
			return
		}
		s.systemUpdateOperationMu.Lock()
		defer s.systemUpdateOperationMu.Unlock()
	}
	services, err := s.services.ListServices(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "list_services_failed"})
		return
	}
	for _, service := range services {
		if existing.ServiceType == "update_agent" &&
			service.TokenID == id &&
			service.ServiceType == "update_agent" &&
			s.rejectUpdaterIdentityMutationDuringActiveWork(w, r, service.ServiceID) {
			return
		}
	}
	for _, service := range services {
		if service.TokenID == id && (service.NodeTokenCiphertext != "" || service.NodeTokenNonce != "") {
			writeJSON(w, http.StatusConflict, map[string]string{"code": "node_runtime_token_requires_node_rotation"})
			return
		}
	}
	token, err := s.services.RotateServiceToken(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "rotate_api_token_failed"})
		return
	}
	s.writeAudit(r, store.AuditEvent{
		ActorUserID:   current.User.ID,
		ActorUsername: current.User.Username,
		Action:        "api_tokens.rotate",
		ResourceType:  "service_token",
		ResourceID:    token.ID,
		Result:        "success",
		Metadata:      map[string]any{"old_token_id": id, "service_type": token.ServiceType, "scopes": token.Scopes},
	})
	writeOneTimeSecretJSON(w, http.StatusCreated, token)
}
