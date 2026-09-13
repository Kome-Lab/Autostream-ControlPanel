package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	contracts "github.com/example/autostream-contracts/pkg/contracts"
	"github.com/example/autostream-control-panel/internal/store"
	"io"
	"net/http"
	"strings"
	"time"
)

func (s *Server) cancelSystemUpdate(w http.ResponseWriter, r *http.Request) {
	current := currentFromContext(r.Context())
	job, err := s.systemUpdates.CancelSystemUpdateJob(r.Context(), strings.TrimSpace(r.PathValue("id")), current.User.ID)
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "system_update_job_not_found"})
		return
	}
	if errors.Is(err, store.ErrSystemUpdateNotCancellable) {
		writeJSON(w, http.StatusConflict, map[string]string{"code": "system_update_not_cancellable"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "cancel_system_update_failed"})
		return
	}
	s.writeAudit(r, store.AuditEvent{
		ActorUserID: current.User.ID, ActorUsername: current.User.Username,
		Action: "system_updates.cancel", ResourceType: "system_update", ResourceID: job.ID, Result: "success",
		Metadata: map[string]any{"target_id": job.TargetID, "target_version": job.TargetVersion},
	})
	writeJSON(w, http.StatusOK, job)
}

func (s *Server) serviceSystemUpdateClaim(w http.ResponseWriter, r *http.Request) {
	v2 := isSystemUpdateV2Request(r)
	if !v2 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "unsupported_updater_protocol"})
		return
	}
	token, ok := s.authenticateService(w, r, "updates.claim")
	if !ok {
		return
	}
	var body contracts.UpdateAgentClaimRequest
	decoder := json.NewDecoder(io.LimitReader(r.Body, maxSystemUpdateV2PayloadBytes+1))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&body); err != nil ||
		!errors.Is(decoder.Decode(&struct{}{}), io.EOF) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "bad_request"})
		return
	}
	if !validSystemUpdateClaimIdentity(body.UpdaterID, 128) ||
		!validSystemUpdateClaimIdentity(body.HostID, 191) ||
		body.LeaseGeneration < 1 || body.Fence < 1 ||
		(body.ActiveJobID != "" && !validSystemUpdateClaimIdentity(body.ActiveJobID, 64)) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "bad_request"})
		return
	}
	activeJobID := body.ActiveJobID
	s.systemUpdateOperationMu.Lock()
	defer s.systemUpdateOperationMu.Unlock()
	token, ok = s.reauthenticateService(w, r, token, "updates.claim")
	if !ok {
		return
	}
	agent, err := s.systemUpdateAgentForToken(r.Context(), token, body.UpdaterID)
	if err != nil {
		writeSystemUpdateAgentError(w, err)
		return
	}
	now := time.Now().UTC()
	if !systemUpdateAgentAvailable(agent, now) {
		writeJSON(w, http.StatusConflict, map[string]string{"code": "updater_offline"})
		return
	}
	hostID, err := s.systemUpdateClaimHost(r.Context(), agent)
	if errors.Is(err, store.ErrSystemUpdateOwnershipConflict) {
		s.writeServiceAudit(r, token, "system_updates.claim", "update_agent", agent.ServiceID, "failure", map[string]any{
			"reason":             "ownership_conflict",
			"transport_mode":     systemUpdateAgentTransportMode(agent),
			"execution_host_id":  strings.TrimSpace(agent.ExecutionHostID),
			"ownership_epoch":    agent.OwnershipEpoch,
			"active_job_present": activeJobID != "",
		})
		writeJSON(w, http.StatusConflict, map[string]string{"code": "system_update_ownership_conflict"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "bad_request"})
		return
	}
	if body.HostID != hostID || body.Fence != agent.OwnershipEpoch {
		writeJSON(w, http.StatusConflict, map[string]string{"code": "system_update_ownership_conflict"})
		return
	}
	var activeJob *store.SystemUpdateJob
	if activeJobID != "" {
		terminalJob, clearActiveJob, err := s.systemUpdates.InspectSystemUpdateActiveJob(r.Context(), agent.ServiceID, activeJobID)
		if errors.Is(err, store.ErrInvalidSystemUpdate) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"code": "bad_request"})
			return
		}
		if errors.Is(err, store.ErrSystemUpdateOwnershipConflict) {
			writeJSON(w, http.StatusConflict, map[string]string{"code": "system_update_ownership_conflict"})
			return
		}
		if errors.Is(err, store.ErrSystemUpdateRecoveryProofUnavailable) {
			writeJSON(w, http.StatusConflict, map[string]string{"code": "system_update_recovery_proof_unavailable"})
			return
		}
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "inspect_system_update_active_job_failed"})
			return
		}
		if terminalJob.LeaseGeneration != body.LeaseGeneration || terminalJob.OwnershipEpoch != body.Fence || terminalJob.ExecutionHostID != body.HostID {
			writeJSON(w, http.StatusConflict, map[string]string{"code": "system_update_lease_invalid"})
			return
		}
		if clearActiveJob {
			writeSystemUpdateV2Clear(w)
			return
		}
		activeJob = &terminalJob
	}
	var eligibleTargets map[string]string
	if activeJob != nil {
		eligibleTargets, err = s.systemUpdatePullRecoveryEligibleTarget(r.Context(), agent, hostID, *activeJob)
	} else {
		eligibleTargets, err = s.systemUpdateTargetsForAgentHostClaim(r.Context(), agent, hostID, activeJobID != "")
	}
	if errors.Is(err, store.ErrSystemUpdateOwnershipConflict) {
		writeJSON(w, http.StatusConflict, map[string]string{"code": "system_update_ownership_conflict"})
		return
	}
	if errors.Is(err, store.ErrSystemUpdateActiveUnavailable) {
		writeJSON(w, http.StatusConflict, map[string]string{"code": "system_update_active_target_unavailable"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "resolve_system_update_targets_failed"})
		return
	}
	if len(eligibleTargets) == 0 && activeJobID == "" {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	claimTTL := systemUpdateExecutionLeaseTTL
	var claim store.SystemUpdateClaim
	var clearActiveJob bool
	v2Store, available := s.systemUpdates.(store.SystemUpdateV2Store)
	if !available {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"code": "updater_v2_adapter_unavailable"})
		return
	}
	claim, clearActiveJob, err = v2Store.ClaimSystemUpdateJobV2(
		r.Context(), agent.ServiceID, hostID, activeJobID,
		body.LeaseGeneration, body.Fence,
		eligibleTargets, now, claimTTL,
	)
	if err == nil && clearActiveJob {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "claim_system_update_recovery_proof_missing"})
		return
	}
	if errors.Is(err, store.ErrNotFound) {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if errors.Is(err, store.ErrSystemUpdateTakeoverForbidden) {
		s.writeServiceAudit(r, token, "system_updates.claim", "update_agent", agent.ServiceID, "failure", map[string]any{"reason": "automatic_cross_agent_takeover_forbidden"})
		writeJSON(w, http.StatusConflict, map[string]string{"code": "system_update_takeover_forbidden"})
		return
	}
	if errors.Is(err, store.ErrSystemUpdateActiveUnavailable) {
		writeJSON(w, http.StatusConflict, map[string]string{"code": "system_update_active_target_unavailable"})
		return
	}
	if errors.Is(err, store.ErrSystemUpdateRecoveryProofUnavailable) {
		writeJSON(w, http.StatusConflict, map[string]string{"code": "system_update_recovery_proof_unavailable"})
		return
	}
	if errors.Is(err, store.ErrSystemUpdateLeaseInvalid) {
		writeJSON(w, http.StatusConflict, map[string]string{"code": "system_update_lease_invalid"})
		return
	}
	if errors.Is(err, store.ErrSystemUpdateOwnershipConflict) {
		s.writeServiceAudit(r, token, "system_updates.claim", "update_agent", agent.ServiceID, "failure", map[string]any{
			"reason":                    "ownership_conflict",
			"transport_mode":            systemUpdateAgentTransportMode(agent),
			"execution_host_id":         hostID,
			"ownership_epoch":           agent.OwnershipEpoch,
			"reported_recovery_pending": capabilityBool(agent.ReportedCapabilities["recovery_pending"]),
			"active_job_present":        activeJobID != "",
		})
		writeJSON(w, http.StatusConflict, map[string]string{"code": "system_update_ownership_conflict"})
		return
	}
	if errors.Is(err, store.ErrInvalidSystemUpdate) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "bad_request"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "claim_system_update_failed"})
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	s.writeServiceAudit(r, token, "system_updates.claim", "system_update", claim.Job.ID, "success", map[string]any{
		"agent_service_id":          agent.ServiceID,
		"host_id":                   hostID,
		"target_id":                 claim.Job.TargetID,
		"target_version":            claim.Job.TargetVersion,
		"lease_generation":          claim.LeaseGeneration,
		"recovery_required":         claim.RecoveryRequired,
		"last_status":               claim.LastStatus,
		"transport_mode":            systemUpdateAgentTransportMode(agent),
		"ownership_epoch":           claim.Job.OwnershipEpoch,
		"policy_revision":           claim.Job.PolicyRevision,
		"reported_recovery_pending": capabilityBool(agent.ReportedCapabilities["recovery_pending"]),
		"active_job_present":        activeJobID != "",
	})
	if err := s.writeSystemUpdateV2Lease(w, r.Context(), claim.Job); err != nil {
		s.writeServiceAudit(r, token, "system_updates.claim", "system_update", claim.Job.ID, "failure", map[string]any{
			"reason": "updater_v2_lease_projection_failed",
		})
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "updater_v2_lease_projection_failed"})
	}
}

func validSystemUpdateClaimIdentity(value string, maxLength int) bool {
	return value == strings.TrimSpace(value) &&
		len(value) >= 1 && len(value) <= maxLength &&
		systemUpdateClaimIdentityPattern.MatchString(value)
}

func (s *Server) serviceSystemUpdateReport(w http.ResponseWriter, r *http.Request) {
	if !isSystemUpdateV2Request(r) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "unsupported_updater_protocol"})
		return
	}
	s.serviceSystemUpdateReportV2(w, r)
}

func (s *Server) systemUpdateAgentForToken(ctx context.Context, token store.ServiceToken, serviceID string) (store.RegisteredService, error) {
	serviceID = strings.TrimSpace(serviceID)
	if serviceID == "" {
		return store.RegisteredService{}, store.ErrInvalidSystemUpdate
	}
	agent, err := s.services.GetService(ctx, serviceID)
	if err != nil {
		return store.RegisteredService{}, err
	}
	if agent.ServiceType != "update_agent" || agent.TokenID != token.ID || token.ServiceType != "update_agent" {
		return store.RegisteredService{}, store.ErrForbidden
	}
	return agent, nil
}

func (s *Server) systemUpdateClaimHost(ctx context.Context, agent store.RegisteredService) (string, error) {
	transportMode := strings.ToLower(strings.TrimSpace(agent.TransportMode))
	if transportMode != store.SystemUpdateTransportPullV2 {
		return "", store.ErrInvalidSystemUpdate
	}
	if err := s.validatePullSystemUpdateAgentOwnership(ctx, agent); err != nil {
		return "", err
	}
	return strings.TrimSpace(agent.ExecutionHostID), nil
}

func (s *Server) validatePullSystemUpdateAgentOwnership(ctx context.Context, agent store.RegisteredService) error {
	transportMode := strings.ToLower(strings.TrimSpace(agent.TransportMode))
	if transportMode != store.SystemUpdateTransportPullV2 ||
		!validSystemUpdateCapabilityIdentifier(agent.ExecutionHostID) ||
		agent.OwnershipEpoch < 1 {
		return store.ErrSystemUpdateOwnershipConflict
	}
	ownershipStore, ok := s.systemUpdates.(store.SystemUpdateExecutionHostStore)
	if !ok {
		return store.ErrSystemUpdateOwnershipConflict
	}
	ownership, err := ownershipStore.GetSystemUpdateExecutionHost(ctx, agent.ExecutionHostID)
	if err != nil {
		return err
	}
	if ownership.TransportMode != store.SystemUpdateTransportPullV2 ||
		ownership.ExecutionHostID != strings.TrimSpace(agent.ExecutionHostID) ||
		ownership.AgentServiceID != agent.ServiceID ||
		ownership.OwnershipEpoch != agent.OwnershipEpoch {
		return store.ErrSystemUpdateOwnershipConflict
	}
	return nil
}

func pullSystemUpdateExecutionHost(agent store.RegisteredService) string {
	if systemUpdateAgentTransportMode(agent) == store.SystemUpdateTransportPullV2 {
		return strings.TrimSpace(agent.ExecutionHostID)
	}
	return ""
}

func systemUpdateAgentTransportMode(agent store.RegisteredService) string {
	return strings.ToLower(strings.TrimSpace(agent.TransportMode))
}

func writeSystemUpdateAgentError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "update_agent_not_registered"})
	case errors.Is(err, store.ErrForbidden):
		writeJSON(w, http.StatusForbidden, map[string]string{"code": "update_agent_not_assigned_to_token"})
	default:
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_update_agent"})
	}
}
