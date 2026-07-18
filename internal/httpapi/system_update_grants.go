package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/example/autostream-control-panel/internal/store"
)

type systemUpdateMutationGrantBindingBody struct {
	HostID         string `json:"host_id"`
	TargetID       string `json:"target_id"`
	TargetVersion  string `json:"target_version"`
	DeploymentMode string `json:"deployment_mode"`
	Operation      string `json:"operation"`
	PlanSHA256     string `json:"plan_sha256"`
	SessionID      string `json:"session_id"`
}

type systemUpdateMutationGrantConsumeBody struct {
	LeaseGeneration int64 `json:"lease_generation"`
	systemUpdateMutationGrantBindingBody
}

func (body systemUpdateMutationGrantBindingBody) storeBinding() store.SystemUpdateMutationGrantBinding {
	return store.SystemUpdateMutationGrantBinding{
		HostID: body.HostID, TargetID: body.TargetID, TargetVersion: body.TargetVersion,
		DeploymentMode: body.DeploymentMode, Operation: body.Operation,
		PlanSHA256: body.PlanSHA256, SessionID: body.SessionID,
	}
}

func (s *Server) serviceSystemUpdateMutationGrantIssue(w http.ResponseWriter, r *http.Request) {
	token, ok := s.authenticateService(w, r, "updates.authorize")
	if !ok {
		return
	}
	var body struct {
		ServiceID       string `json:"service_id"`
		LeaseToken      string `json:"lease_token"`
		LeaseGeneration int64  `json:"lease_generation"`
		systemUpdateMutationGrantBindingBody
	}
	if !decodeSingleSystemUpdateGrantJSON(r, &body) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "bad_request"})
		return
	}
	jobID := strings.TrimSpace(r.PathValue("id"))
	agent, err := s.systemUpdateAgentForToken(r.Context(), token, body.ServiceID)
	if err != nil {
		s.writeServiceAudit(r, token, "system_updates.mutation_grant.issue", "system_update", jobID, "failure", map[string]any{"reason": "update_agent_identity_invalid"})
		writeSystemUpdateAgentError(w, err)
		return
	}
	grantStore, ok := s.systemUpdates.(store.SystemUpdateMutationGrantStore)
	if !ok {
		s.writeServiceAudit(r, token, "system_updates.mutation_grant.issue", "system_update", jobID, "failure", map[string]any{"reason": "grant_store_unavailable"})
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "system_update_mutation_grant_unavailable"})
		return
	}
	binding := body.systemUpdateMutationGrantBindingBody.storeBinding()
	issued, err := grantStore.IssueSystemUpdateMutationGrant(r.Context(), jobID, store.IssueSystemUpdateMutationGrantParams{
		AgentServiceID: agent.ServiceID, LeaseToken: body.LeaseToken, LeaseGeneration: body.LeaseGeneration, Binding: binding,
	}, time.Now().UTC(), store.SystemUpdateMutationGrantMaxTTL)
	metadata := systemUpdateMutationGrantAuditMetadata(binding)
	metadata["agent_service_id"] = agent.ServiceID
	metadata["lease_generation"] = body.LeaseGeneration
	if err != nil {
		status, code, reason := http.StatusInternalServerError, "issue_system_update_mutation_grant_failed", "grant_store_failed"
		switch {
		case errors.Is(err, store.ErrNotFound):
			status, code, reason = http.StatusNotFound, "system_update_job_not_found", "job_not_found"
		case errors.Is(err, store.ErrSystemUpdateAuthorizationState):
			status, code, reason = http.StatusConflict, "system_update_mutation_grant_state_invalid", "authorization_state_invalid"
		case errors.Is(err, store.ErrSystemUpdateLeaseInvalid):
			status, code, reason = http.StatusConflict, "system_update_lease_invalid", "lease_invalid"
		case errors.Is(err, store.ErrSystemUpdateAuthorizationMismatch):
			status, code, reason = http.StatusConflict, "system_update_mutation_grant_binding_mismatch", "authorization_mismatch"
		case errors.Is(err, store.ErrSystemUpdateMutationGrantConflict):
			status, code, reason = http.StatusConflict, "system_update_mutation_grant_conflict", "grant_conflict"
		case errors.Is(err, store.ErrInvalidSystemUpdate):
			status, code, reason = http.StatusBadRequest, "invalid_system_update_mutation_grant", "invalid_request"
		}
		metadata["reason"] = reason
		s.writeServiceAudit(r, token, "system_updates.mutation_grant.issue", "system_update", jobID, "failure", metadata)
		writeJSON(w, status, map[string]string{"code": code})
		return
	}
	metadata["grant_id"] = issued.Grant.ID
	metadata["expires_at"] = issued.Grant.ExpiresAt
	s.writeServiceAudit(r, token, "system_updates.mutation_grant.issue", "system_update", jobID, "success", metadata)
	writeOneTimeSecretJSON(w, http.StatusCreated, map[string]any{
		"grant_token": issued.GrantToken, "expires_at": issued.Grant.ExpiresAt,
	})
}

func (s *Server) serviceSystemUpdateMutationGrantConsume(w http.ResponseWriter, r *http.Request) {
	grantToken, ok := systemUpdateMutationGrantBearer(r)
	if !ok {
		w.Header().Set("WWW-Authenticate", `Bearer realm="system-update-mutation-grant"`)
		writeJSON(w, http.StatusUnauthorized, map[string]string{"code": "system_update_mutation_grant_required"})
		return
	}
	var body systemUpdateMutationGrantConsumeBody
	if !decodeSingleSystemUpdateGrantJSON(r, &body) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "bad_request"})
		return
	}
	jobID := strings.TrimSpace(r.PathValue("id"))
	binding := body.systemUpdateMutationGrantBindingBody.storeBinding()
	metadata := systemUpdateMutationGrantAuditMetadata(binding)
	metadata["lease_generation"] = body.LeaseGeneration
	grantStore, ok := s.systemUpdates.(store.SystemUpdateMutationGrantStore)
	if !ok {
		metadata["reason"] = "grant_store_unavailable"
		s.writeSystemUpdateMutationGrantConsumeAudit(r, jobID, "failure", metadata)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "system_update_mutation_grant_unavailable"})
		return
	}
	grant, replayed, err := grantStore.ConsumeSystemUpdateMutationGrant(r.Context(), jobID, grantToken, body.LeaseGeneration, binding, time.Now().UTC())
	if err != nil {
		status, code, reason := http.StatusInternalServerError, "consume_system_update_mutation_grant_failed", "grant_store_failed"
		switch {
		case errors.Is(err, store.ErrInvalidSystemUpdate):
			status, code, reason = http.StatusBadRequest, "invalid_system_update_mutation_grant_consumption", "invalid_request"
		case errors.Is(err, store.ErrSystemUpdateMutationGrantConflict), errors.Is(err, store.ErrNotFound):
			status, code, reason = http.StatusConflict, "system_update_mutation_grant_conflict", "grant_conflict"
		}
		metadata["reason"] = reason
		s.writeSystemUpdateMutationGrantConsumeAudit(r, jobID, "failure", metadata)
		writeJSON(w, status, map[string]string{"code": code})
		return
	}
	metadata["grant_id"] = grant.ID
	metadata["idempotent_replay"] = replayed
	s.writeSystemUpdateMutationGrantConsumeAudit(r, jobID, "success", metadata)
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
	w.WriteHeader(http.StatusNoContent)
}

func decodeSingleSystemUpdateGrantJSON(r *http.Request, out any) bool {
	decoder := json.NewDecoder(io.LimitReader(r.Body, 64*1024))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(out); err != nil {
		return false
	}
	return errors.Is(decoder.Decode(&struct{}{}), io.EOF)
}

func systemUpdateMutationGrantBearer(r *http.Request) (string, bool) {
	values := r.Header.Values("Authorization")
	if len(values) != 1 {
		return "", false
	}
	parts := strings.Fields(values[0])
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") || !strings.HasPrefix(parts[1], "ast_mutation_") {
		return "", false
	}
	return parts[1], true
}

func systemUpdateMutationGrantAuditMetadata(binding store.SystemUpdateMutationGrantBinding) map[string]any {
	return map[string]any{
		"host_id": strings.TrimSpace(binding.HostID), "target_id": strings.TrimSpace(binding.TargetID),
		"target_version": strings.TrimSpace(binding.TargetVersion), "deployment_mode": strings.ToLower(strings.TrimSpace(binding.DeploymentMode)),
		"operation": strings.ToLower(strings.TrimSpace(binding.Operation)), "plan_sha256": strings.TrimSpace(binding.PlanSHA256),
		"session_id": strings.TrimSpace(binding.SessionID),
	}
}

func (s *Server) writeSystemUpdateMutationGrantConsumeAudit(r *http.Request, jobID, result string, metadata map[string]any) {
	s.writeAudit(r, store.AuditEvent{
		ActorUserID: "service:update_host", ActorUsername: "update_host",
		Action: "system_updates.mutation_grant.consume", ResourceType: "system_update", ResourceID: jobID,
		Result: result, Metadata: metadata,
	})
}
