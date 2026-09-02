package httpapi

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/example/autostream-control-panel/internal/security"
	"github.com/example/autostream-control-panel/internal/store"
)

type nodeAction string

const (
	nodeActionRegistrationToken        nodeAction = "registration_token"
	nodeActionConfigureTokenRegenerate nodeAction = "configure_token_regenerate"
	nodeActionRuntimeTokenRotate       nodeAction = "runtime_token_rotate"
	nodeProjectionContractVersion                 = 1
	nodeProjectionControlAPIMajor                 = "2"
	nodeProjectionContractMajorHeader             = "X-AutoStream-Contract-Major"
)

type nodeAuthorityReason string

const (
	nodeAuthorityAllowed             nodeAuthorityReason = "allowed"
	nodeAuthorityInvalidServiceType  nodeAuthorityReason = "invalid_service_type"
	nodeAuthorityInvalidServiceScope nodeAuthorityReason = "invalid_service_scope"
)

type nodePermissionAuthority struct {
	reason              nodeAuthorityReason
	requiredPermissions []string
	missingPermissions  []string
	projectedScopes     []string
}

type nodeActionTokenFailure struct {
	status int
	code   string
}

type nodeActionPermissionProjectionResponse struct {
	ContractVersion     int      `json:"contract_version"`
	ProjectionRevision  string   `json:"projection_revision"`
	EvaluatedAt         string   `json:"evaluated_at"`
	Action              string   `json:"action"`
	Availability        string   `json:"availability"`
	ReasonCode          string   `json:"reason_code"`
	RequiredPermissions []string `json:"required_permissions"`
	MissingPermissions  []string `json:"missing_permissions"`
}

type nodeProjectionContractMajorError struct {
	ContractMajor int    `json:"contract_major"`
	Code          string `json:"code"`
	Message       string `json:"message"`
	Retryable     bool   `json:"retryable"`
	ExpectedMajor int    `json:"expected_major"`
}

type nodeActionProjectionQuery struct {
	action              nodeAction
	nodeType            string
	nodeID              string
	allowRuntimeSecrets bool
	allowRemediation    bool
}

func nodeActionProjectionBoundary(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store, no-cache")
		w.Header().Set("Pragma", "no-cache")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set(nodeProjectionContractMajorHeader, nodeProjectionControlAPIMajor)
		contractMajors := r.Header.Values(nodeProjectionContractMajorHeader)
		if len(contractMajors) != 1 || strings.TrimSpace(contractMajors[0]) != nodeProjectionControlAPIMajor {
			writeJSON(w, http.StatusUpgradeRequired, nodeProjectionContractMajorError{
				ContractMajor: 2,
				Code:          "contract_major_unsupported",
				Message:       "contract major 2 is required",
				Retryable:     false,
				ExpectedMajor: 2,
			})
			return
		}
		next.ServeHTTP(w, r)
	}
}

func (s *Server) nodeActionPermissionProjection(w http.ResponseWriter, r *http.Request) {
	query, ok := parseNodeActionProjectionQuery(r)
	if !ok {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_node_action_projection_request"})
		return
	}
	current := currentFromContext(r.Context())
	if query.action == nodeActionRegistrationToken {
		authority := evaluateNodeRegistrationAuthority(
			current.Permissions,
			query.nodeType,
			query.allowRuntimeSecrets,
			query.allowRemediation,
		)
		availability, reason := nodeProjectionDisposition(authority)
		writeNodeActionPermissionProjection(w, query, authority, availability, reason, nil, nil)
		return
	}

	service, err := s.services.GetService(r.Context(), query.nodeID)
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "node_not_found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "get_node_failed"})
		return
	}
	token, failure := s.selectNodeActionToken(r.Context(), query.action, service)
	if failure != nil {
		writeJSON(w, failure.status, map[string]string{"code": failure.code})
		return
	}
	authority := evaluateNodeTokenAuthority(current.Permissions, service, token)
	availability, reason := nodeProjectionDisposition(authority)
	if authority.reason == nodeAuthorityAllowed && query.action == nodeActionRuntimeTokenRotate && isPullV2HostAgent(service) {
		availability = "not_applicable"
		reason = "staged_runtime_token_rotation_required"
		authority.missingPermissions = []string{}
	}
	writeNodeActionPermissionProjection(w, query, authority, availability, reason, &service, &token)
}

func parseNodeActionProjectionQuery(r *http.Request) (nodeActionProjectionQuery, bool) {
	values := r.URL.Query()
	allowed := map[string]bool{
		"action": true, "node_type": true, "node_id": true,
		"allow_runtime_secrets": true, "allow_remediation": true,
	}
	for key, entries := range values {
		if !allowed[key] || len(entries) != 1 {
			return nodeActionProjectionQuery{}, false
		}
	}
	action := nodeAction(strings.TrimSpace(values.Get("action")))
	query := nodeActionProjectionQuery{
		action:   action,
		nodeType: strings.TrimSpace(values.Get("node_type")),
		nodeID:   strings.TrimSpace(values.Get("node_id")),
	}
	var ok bool
	if query.allowRuntimeSecrets, ok = optionalProjectionBool(values, "allow_runtime_secrets"); !ok {
		return nodeActionProjectionQuery{}, false
	}
	if query.allowRemediation, ok = optionalProjectionBool(values, "allow_remediation"); !ok {
		return nodeActionProjectionQuery{}, false
	}
	switch action {
	case nodeActionRegistrationToken:
		if query.nodeType == "" || query.nodeID != "" {
			return nodeActionProjectionQuery{}, false
		}
	case nodeActionConfigureTokenRegenerate, nodeActionRuntimeTokenRotate:
		if query.nodeID == "" || query.nodeType != "" || values.Has("allow_runtime_secrets") || values.Has("allow_remediation") {
			return nodeActionProjectionQuery{}, false
		}
	default:
		return nodeActionProjectionQuery{}, false
	}
	if !validProjectionIdentifier(query.nodeID, 128) || !validProjectionNodeType(query.nodeType) {
		return nodeActionProjectionQuery{}, false
	}
	return query, true
}

func optionalProjectionBool(values map[string][]string, key string) (bool, bool) {
	if _, exists := values[key]; !exists {
		return false, true
	}
	switch values[key][0] {
	case "true":
		return true, true
	case "false":
		return false, true
	default:
		return false, false
	}
}

func validProjectionIdentifier(value string, maximum int) bool {
	if value == "" {
		return true
	}
	first := value[0]
	if len(value) > maximum || !((first >= 'A' && first <= 'Z') ||
		(first >= 'a' && first <= 'z') || (first >= '0' && first <= '9')) {
		return false
	}
	for _, char := range value {
		if (char >= 'A' && char <= 'Z') || (char >= 'a' && char <= 'z') ||
			(char >= '0' && char <= '9') || char == '.' || char == '_' || char == ':' || char == '-' {
			continue
		}
		return false
	}
	return true
}

func validProjectionNodeType(value string) bool {
	if value == "" {
		return true
	}
	if len(value) > 64 || value[0] < 'a' || value[0] > 'z' {
		return false
	}
	for _, char := range value {
		if (char >= 'a' && char <= 'z') || (char >= '0' && char <= '9') || char == '_' {
			continue
		}
		return false
	}
	return true
}

func (s *Server) selectNodeActionToken(
	ctx context.Context,
	action nodeAction,
	service store.RegisteredService,
) (store.ServiceToken, *nodeActionTokenFailure) {
	tokens, err := s.services.ListServiceTokens(ctx)
	if err != nil {
		return store.ServiceToken{}, &nodeActionTokenFailure{status: http.StatusInternalServerError, code: "list_service_tokens_failed"}
	}
	for _, token := range tokens {
		if token.ID == service.TokenID && token.RevokedAt == nil {
			return token, nil
		}
	}
	if action != nodeActionConfigureTokenRegenerate {
		return store.ServiceToken{}, &nodeActionTokenFailure{status: http.StatusNotFound, code: "runtime_token_not_found"}
	}
	identityFenceStore, ok := s.systemUpdates.(store.SystemUpdateIdentityMutationFenceStore)
	if !ok {
		return store.ServiceToken{}, &nodeActionTokenFailure{status: http.StatusServiceUnavailable, code: "system_update_identity_fence_unavailable"}
	}
	recovery, err := identityFenceStore.IsSystemUpdateEmergencyIdentityRecovery(ctx, s.services, service.ServiceID)
	if err != nil {
		return store.ServiceToken{}, &nodeActionTokenFailure{status: http.StatusInternalServerError, code: "check_system_update_emergency_recovery_failed"}
	}
	if recovery {
		for _, token := range tokens {
			if token.ID == service.TokenID {
				return token, nil
			}
		}
	}
	return store.ServiceToken{}, &nodeActionTokenFailure{status: http.StatusNotFound, code: "runtime_token_not_found"}
}

func evaluateNodeRegistrationAuthority(
	actorPermissions []string,
	serviceType string,
	allowRuntimeSecrets bool,
	allowRemediation bool,
) nodePermissionAuthority {
	if !knownNodeServiceType(serviceType) {
		return invalidNodeAuthority(nodeAuthorityInvalidServiceType, false, serviceType)
	}
	scopes := nodeRegistrationScopes(serviceType, allowRuntimeSecrets, allowRemediation)
	if store.ValidateServiceTokenScopes(scopes) != nil || store.ValidateRequiredUpdateAgentScopes(serviceType, scopes) != nil {
		return invalidNodeAuthority(nodeAuthorityInvalidServiceScope, false, serviceType)
	}
	return allowedNodeAuthority(actorPermissions, serviceType, scopes, false)
}

func evaluateNodeTokenAuthority(
	actorPermissions []string,
	service store.RegisteredService,
	token store.ServiceToken,
) nodePermissionAuthority {
	if !knownNodeServiceType(service.ServiceType) || token.ServiceType != service.ServiceType {
		return invalidNodeAuthority(nodeAuthorityInvalidServiceScope, true, service.ServiceType)
	}
	if store.ValidateServiceTokenScopes(token.Scopes) != nil ||
		store.ValidateRequiredUpdateAgentScopes(service.ServiceType, token.Scopes) != nil {
		return invalidNodeAuthority(nodeAuthorityInvalidServiceScope, true, service.ServiceType)
	}
	projected := store.ProjectedServiceTokenScopesForRotation(token)
	if store.ValidateServiceTokenScopes(projected) != nil || store.ValidateRequiredUpdateAgentScopes(service.ServiceType, projected) != nil {
		return invalidNodeAuthority(nodeAuthorityInvalidServiceScope, true, service.ServiceType)
	}
	return allowedNodeAuthority(actorPermissions, service.ServiceType, projected, true)
}

func allowedNodeAuthority(actorPermissions []string, serviceType string, scopes []string, rotation bool) nodePermissionAuthority {
	required := nodeActionRequiredPermissions(serviceType, scopes, rotation)
	missing := make([]string, 0, len(required))
	for _, permission := range required {
		if !security.HasPermission(actorPermissions, permission) {
			missing = append(missing, permission)
		}
	}
	return nodePermissionAuthority{
		reason:              nodeAuthorityAllowed,
		requiredPermissions: required,
		missingPermissions:  missing,
		projectedScopes:     append([]string(nil), scopes...),
	}
}

func invalidNodeAuthority(reason nodeAuthorityReason, rotation bool, serviceType string) nodePermissionAuthority {
	return nodePermissionAuthority{
		reason:              reason,
		requiredPermissions: nodeActionRequiredPermissions(serviceType, nil, rotation),
		missingPermissions:  []string{},
		projectedScopes:     []string{},
	}
}

func nodeActionRequiredPermissions(serviceType string, scopes []string, rotation bool) []string {
	required := map[string]bool{"api_tokens.create": true}
	if rotation {
		required["api_tokens.revoke"] = true
	}
	for _, scope := range scopes {
		switch strings.TrimSpace(scope) {
		case "service.secret.resolve":
			required["secrets.update"] = true
		case "updates.claim", "updates.report", "updates.authorize":
			required["system_updates.execute"] = true
		case "streams.start":
			required["streams.start"] = true
		case "streams.stop":
			required["streams.stop"] = true
		case "remediation.execute":
			required["remediation.execute"] = true
		}
	}
	switch strings.TrimSpace(serviceType) {
	case "worker", "encoder_recorder", "update_agent":
		required["secrets.update"] = true
	}
	order := []string{
		"api_tokens.create",
		"api_tokens.revoke",
		"secrets.update",
		"system_updates.execute",
		"streams.start",
		"streams.stop",
		"remediation.execute",
	}
	out := make([]string, 0, len(required))
	for _, permission := range order {
		if required[permission] {
			out = append(out, permission)
		}
	}
	return out
}

func knownNodeServiceType(serviceType string) bool {
	switch strings.TrimSpace(serviceType) {
	case "discord_bot", "encoder_recorder", "worker", "observability", "update_agent":
		return true
	default:
		return false
	}
}

func nodeProjectionDisposition(authority nodePermissionAuthority) (string, string) {
	if authority.reason == nodeAuthorityInvalidServiceType {
		return "not_applicable", "invalid_service_type"
	}
	if authority.reason == nodeAuthorityInvalidServiceScope {
		return "unknown", "invalid_service_scope"
	}
	if len(authority.missingPermissions) > 0 {
		return "denied", "additional_permission_required"
	}
	return "allowed", "allowed"
}

func writeNodeActionPermissionProjection(
	w http.ResponseWriter,
	query nodeActionProjectionQuery,
	authority nodePermissionAuthority,
	availability string,
	reason string,
	service *store.RegisteredService,
	token *store.ServiceToken,
) {
	required := append([]string(nil), authority.requiredPermissions...)
	missing := append([]string(nil), authority.missingPermissions...)
	if required == nil {
		required = []string{}
	}
	if missing == nil {
		missing = []string{}
	}
	writeJSON(w, http.StatusOK, nodeActionPermissionProjectionResponse{
		ContractVersion:     nodeProjectionContractVersion,
		ProjectionRevision:  nodeActionProjectionRevision(query, authority, availability, reason, service, token),
		EvaluatedAt:         time.Now().UTC().Format(time.RFC3339Nano),
		Action:              string(query.action),
		Availability:        availability,
		ReasonCode:          reason,
		RequiredPermissions: required,
		MissingPermissions:  missing,
	})
}

func nodeActionProjectionRevision(
	query nodeActionProjectionQuery,
	authority nodePermissionAuthority,
	availability string,
	reason string,
	service *store.RegisteredService,
	token *store.ServiceToken,
) string {
	seed := struct {
		Action              nodeAction `json:"action"`
		NodeType            string     `json:"node_type"`
		NodeID              string     `json:"node_id"`
		AllowRuntimeSecrets bool       `json:"allow_runtime_secrets"`
		AllowRemediation    bool       `json:"allow_remediation"`
		Availability        string     `json:"availability"`
		Reason              string     `json:"reason"`
		Required            []string   `json:"required"`
		Missing             []string   `json:"missing"`
		ProjectedScopes     []string   `json:"projected_scopes"`
		ServiceTokenID      string     `json:"service_token_id"`
		ServiceUpdatedAt    time.Time  `json:"service_updated_at"`
		TokenID             string     `json:"token_id"`
		TokenRevoked        bool       `json:"token_revoked"`
	}{
		Action: query.action, NodeType: query.nodeType, NodeID: query.nodeID,
		AllowRuntimeSecrets: query.allowRuntimeSecrets, AllowRemediation: query.allowRemediation,
		Availability: availability, Reason: reason,
		Required:        append([]string(nil), authority.requiredPermissions...),
		Missing:         append([]string(nil), authority.missingPermissions...),
		ProjectedScopes: append([]string(nil), authority.projectedScopes...),
	}
	sort.Strings(seed.ProjectedScopes)
	if service != nil {
		seed.ServiceTokenID = service.TokenID
		seed.ServiceUpdatedAt = service.UpdatedAt
	}
	if token != nil {
		seed.TokenID = token.ID
		seed.TokenRevoked = token.RevokedAt != nil
	}
	encoded, _ := json.Marshal(seed)
	digest := sha256.Sum256(encoded)
	return "v1-" + hex.EncodeToString(digest[:])
}
