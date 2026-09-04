package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	contracts "github.com/example/autostream-contracts/pkg/contracts"
	"github.com/example/autostream-control-panel/internal/store"
)

func (s *Server) serviceSystemUpdateMutationGrantIssueV2(w http.ResponseWriter, r *http.Request) {
	token, ok := s.authenticateService(w, r, "updates.authorize")
	if !ok {
		return
	}
	payload, err := readSystemUpdateV2Payload(r)
	now := time.Now().UTC()
	if err != nil || contracts.ValidateUpdaterMutationGrantIssueRequest(now, payload) != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_updater_v2_mutation_grant"})
		return
	}
	var request contracts.UpdaterMutationGrantIssueRequest
	if json.Unmarshal(payload, &request) != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_updater_v2_mutation_grant"})
		return
	}
	jobID := strings.TrimSpace(r.PathValue("id"))
	s.systemUpdateOperationMu.Lock()
	defer s.systemUpdateOperationMu.Unlock()
	token, ok = s.reauthenticateService(w, r, token, "updates.authorize")
	if !ok {
		return
	}
	agent, err := s.systemUpdateAgentForToken(
		r.Context(),
		token,
		request.Binding.Lease.Command.MutationAuthorization.UpdaterID,
	)
	if err != nil {
		writeSystemUpdateAgentError(w, err)
		return
	}
	if err := s.validatePullSystemUpdateAgentOwnership(r.Context(), agent); err != nil {
		writeSystemUpdateV2OwnershipError(w, err)
		return
	}
	v2Store, ok := s.systemUpdates.(store.SystemUpdateV2Store)
	if !ok {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"code": "updater_v2_adapter_unavailable"})
		return
	}
	job, err := v2Store.GetSystemUpdateJob(r.Context(), jobID)
	if err != nil {
		writeSystemUpdateV2JobError(w, err)
		return
	}
	expectedLease, err := s.systemUpdateV2Lease(r.Context(), job)
	if err != nil || !sameSystemUpdateV2Lease(expectedLease, request.Binding.Lease) {
		writeJSON(w, http.StatusConflict, map[string]string{"code": "updater_v2_lease_binding_mismatch"})
		return
	}
	binding, err := systemUpdateV2StoreGrantBinding(job, request.Binding)
	if err != nil {
		writeJSON(w, http.StatusConflict, map[string]string{"code": "updater_v2_mutation_binding_mismatch"})
		return
	}
	grantStore, ok := s.systemUpdates.(store.SystemUpdateMutationGrantStore)
	if !ok {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"code": "system_update_mutation_grant_unavailable"})
		return
	}
	issued, err := grantStore.IssueSystemUpdateMutationGrant(r.Context(), jobID, store.IssueSystemUpdateMutationGrantParams{
		ProtocolVersion: 2,
		AgentServiceID:  agent.ServiceID,
		ExecutionHostID: pullSystemUpdateExecutionHost(agent),
		LeaseGeneration: request.Binding.Lease.LeaseGeneration,
		Binding:         binding,
	}, now, store.SystemUpdateMutationGrantMaxTTL)
	metadata := systemUpdateMutationGrantAuditMetadata(binding)
	metadata["agent_service_id"] = agent.ServiceID
	metadata["lease_generation"] = request.Binding.Lease.LeaseGeneration
	metadata["protocol_version"] = 2
	if err != nil {
		status, code, reason := systemUpdateV2GrantIssueError(err)
		metadata["reason"] = reason
		s.writeServiceAudit(r, token, "system_updates.mutation_grant.issue", "system_update", jobID, "failure", metadata)
		writeJSON(w, status, map[string]string{"code": code})
		return
	}
	response := contracts.UpdaterMutationGrantIssueResponse{
		GrantToken: issued.GrantToken,
		ExpiresAt:  issued.Grant.ExpiresAt,
	}
	responsePayload, err := json.Marshal(response)
	if err != nil || contracts.ValidateUpdaterMutationGrantIssueResponse(now, responsePayload) != nil ||
		contracts.ValidateUpdaterMutationGrantIssueResponseForLease(now, expectedLease, response) != nil {
		metadata["reason"] = "invalid_grant_response"
		s.writeServiceAudit(r, token, "system_updates.mutation_grant.issue", "system_update", jobID, "failure", metadata)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "issue_system_update_mutation_grant_failed"})
		return
	}
	metadata["grant_id"] = issued.Grant.ID
	metadata["expires_at"] = issued.Grant.ExpiresAt
	s.writeServiceAudit(r, token, "system_updates.mutation_grant.issue", "system_update", jobID, "success", metadata)
	writeOneTimeSecretJSON(w, http.StatusCreated, response)
}

func (s *Server) serviceSystemUpdateMutationGrantConsumeV2(w http.ResponseWriter, r *http.Request) {
	grantToken, ok := systemUpdateMutationGrantBearer(r)
	if !ok {
		w.Header().Set("WWW-Authenticate", `Bearer realm="system-update-mutation-grant"`)
		writeJSON(w, http.StatusUnauthorized, map[string]string{"code": "system_update_mutation_grant_required"})
		return
	}
	payload, err := readSystemUpdateV2Payload(r)
	now := time.Now().UTC()
	if err != nil || contracts.ValidateUpdaterMutationGrantConsumeRequest(now, payload) != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_updater_v2_mutation_grant_consumption"})
		return
	}
	var request contracts.UpdaterMutationGrantConsumeRequest
	if json.Unmarshal(payload, &request) != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_updater_v2_mutation_grant_consumption"})
		return
	}
	jobID := strings.TrimSpace(r.PathValue("id"))
	v2Store, ok := s.systemUpdates.(store.SystemUpdateV2Store)
	if !ok {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"code": "updater_v2_adapter_unavailable"})
		return
	}
	job, err := v2Store.GetSystemUpdateJob(r.Context(), jobID)
	if err != nil {
		writeSystemUpdateV2JobError(w, err)
		return
	}
	expectedLease, err := s.systemUpdateV2Lease(r.Context(), job)
	if err != nil || !sameSystemUpdateV2Lease(expectedLease, request.Binding.Lease) {
		writeJSON(w, http.StatusConflict, map[string]string{"code": "updater_v2_lease_binding_mismatch"})
		return
	}
	binding, err := systemUpdateV2StoreGrantBinding(job, request.Binding)
	if err != nil {
		writeJSON(w, http.StatusConflict, map[string]string{"code": "updater_v2_mutation_binding_mismatch"})
		return
	}
	metadata := systemUpdateMutationGrantAuditMetadata(binding)
	metadata["lease_generation"] = request.Binding.Lease.LeaseGeneration
	metadata["protocol_version"] = 2
	grantStore, ok := s.systemUpdates.(store.SystemUpdateMutationGrantStore)
	if !ok {
		metadata["reason"] = "grant_store_unavailable"
		s.writeSystemUpdateMutationGrantConsumeAudit(r, jobID, "failure", metadata)
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"code": "system_update_mutation_grant_unavailable"})
		return
	}
	grant, replayed, err := grantStore.ConsumeSystemUpdateMutationGrant(
		r.Context(),
		jobID,
		grantToken,
		request.Binding.Lease.LeaseGeneration,
		binding,
		now,
	)
	if err != nil {
		status, code, reason := systemUpdateV2GrantConsumeError(err)
		metadata["reason"] = reason
		s.writeSystemUpdateMutationGrantConsumeAudit(r, jobID, "failure", metadata)
		writeJSON(w, status, map[string]string{"code": code})
		return
	}
	metadata["grant_id"] = grant.ID
	metadata["idempotent_replay"] = replayed
	s.writeSystemUpdateMutationGrantConsumeAudit(r, jobID, "success", metadata)
	w.WriteHeader(http.StatusNoContent)
}

func systemUpdateV2StoreGrantBinding(job store.SystemUpdateJob, binding contracts.UpdaterMutationGrantBinding) (store.SystemUpdateMutationGrantBinding, error) {
	if binding.Lease.Command.MutationAuthorization.JobID != job.ID ||
		binding.Lease.Command.MutationAuthorization.UpdaterID != job.AgentServiceID ||
		binding.Lease.Command.MutationAuthorization.HostID != job.ExecutionHostID ||
		binding.Lease.Command.MutationAuthorization.Fence != job.OwnershipEpoch ||
		binding.Lease.LeaseGeneration != job.LeaseGeneration {
		return store.SystemUpdateMutationGrantBinding{}, systemUpdateV2BindingError("mutation grant")
	}
	operation := string(binding.Operation)
	planSHA256 := strings.TrimPrefix(binding.Lease.Command.CanonicalPayloadDigest, "sha256:")
	var port *store.SystemUpdatePortReconfiguration
	if job.Operation == store.SystemUpdateOperationPortReconfigure {
		var err error
		planSHA256, err = store.ComputeSystemUpdatePortRuntimePlanSHA256(job, binding.SessionID)
		if err != nil {
			return store.SystemUpdateMutationGrantBinding{}, err
		}
		port = cloneSystemUpdateV2StorePortPlan(job.PortReconfigure)
		port.PortPlanSHA256 = planSHA256
	}
	if len(planSHA256) != 64 {
		return store.SystemUpdateMutationGrantBinding{}, systemUpdateV2BindingError("plan digest")
	}
	return store.SystemUpdateMutationGrantBinding{
		HostID:            job.ExecutionHostID,
		TransportMode:     store.SystemUpdateTransportPullV2,
		OwnershipEpoch:    job.OwnershipEpoch,
		PolicyRevision:    job.PolicyRevision,
		TargetID:          job.TargetID,
		TargetServiceType: job.TargetServiceType,
		TargetVersion:     job.TargetVersion,
		DeploymentMode:    job.DeploymentMode,
		JobOperation:      job.Operation,
		Operation:         operation,
		PlanSHA256:        planSHA256,
		SessionID:         binding.SessionID,
		PortReconfigure:   port,
	}, nil
}

func cloneSystemUpdateV2StorePortPlan(plan *store.SystemUpdatePortReconfiguration) *store.SystemUpdatePortReconfiguration {
	if plan == nil {
		return nil
	}
	copy := *plan
	copy.Result = ""
	if plan.Docker != nil {
		docker := *plan.Docker
		copy.Docker = &docker
	}
	return &copy
}

func systemUpdateV2GrantIssueError(err error) (int, string, string) {
	switch {
	case errors.Is(err, store.ErrNotFound):
		return http.StatusNotFound, "system_update_job_not_found", "job_not_found"
	case errors.Is(err, store.ErrSystemUpdateAuthorizationState):
		return http.StatusConflict, "system_update_mutation_grant_state_invalid", "authorization_state_invalid"
	case errors.Is(err, store.ErrSystemUpdateLeaseInvalid):
		return http.StatusConflict, "system_update_lease_invalid", "lease_invalid"
	case errors.Is(err, store.ErrSystemUpdateAuthorizationMismatch):
		return http.StatusConflict, "system_update_mutation_grant_binding_mismatch", "authorization_mismatch"
	case errors.Is(err, store.ErrSystemUpdateMutationGrantConflict):
		return http.StatusConflict, "system_update_mutation_grant_conflict", "grant_conflict"
	case errors.Is(err, store.ErrSystemUpdateOwnershipConflict):
		return http.StatusConflict, "system_update_ownership_conflict", "ownership_conflict"
	case errors.Is(err, store.ErrInvalidSystemUpdate):
		return http.StatusBadRequest, "invalid_system_update_mutation_grant", "invalid_request"
	default:
		return http.StatusInternalServerError, "issue_system_update_mutation_grant_failed", "grant_store_failed"
	}
}

func systemUpdateV2GrantConsumeError(err error) (int, string, string) {
	switch {
	case errors.Is(err, store.ErrInvalidSystemUpdate):
		return http.StatusBadRequest, "invalid_system_update_mutation_grant_consumption", "invalid_request"
	case errors.Is(err, store.ErrSystemUpdateMutationGrantConflict), errors.Is(err, store.ErrNotFound):
		return http.StatusConflict, "system_update_mutation_grant_conflict", "grant_conflict"
	case errors.Is(err, store.ErrSystemUpdateOwnershipConflict):
		return http.StatusConflict, "system_update_ownership_conflict", "ownership_conflict"
	default:
		return http.StatusInternalServerError, "consume_system_update_mutation_grant_failed", "grant_store_failed"
	}
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
	jobOperation := strings.ToLower(strings.TrimSpace(binding.JobOperation))
	if jobOperation == "" {
		jobOperation = store.SystemUpdateOperationSoftwareUpdate
	}
	metadata := map[string]any{
		"host_id": strings.TrimSpace(binding.HostID), "target_id": strings.TrimSpace(binding.TargetID),
		"transport_mode":  strings.ToLower(strings.TrimSpace(binding.TransportMode)),
		"ownership_epoch": binding.OwnershipEpoch, "policy_revision": binding.PolicyRevision,
		"service_type":   strings.ToLower(strings.TrimSpace(binding.TargetServiceType)),
		"target_version": strings.TrimSpace(binding.TargetVersion), "deployment_mode": strings.ToLower(strings.TrimSpace(binding.DeploymentMode)),
		"job_operation": jobOperation,
		"operation":     strings.ToLower(strings.TrimSpace(binding.Operation)), "plan_sha256": strings.TrimSpace(binding.PlanSHA256),
		"session_id": strings.TrimSpace(binding.SessionID),
	}
	if port := binding.PortReconfigure; port != nil {
		metadata["port_reconfigure"] = map[string]any{
			"network_namespace": strings.ToLower(strings.TrimSpace(port.NetworkNamespace)),
			"protocol":          strings.ToLower(strings.TrimSpace(string(port.Protocol))),
			"old_port":          port.OldPort, "new_port": port.NewPort,
			"expected_endpoint_revision":        port.ExpectedEndpointRevision,
			"target_endpoint_revision":          port.TargetEndpointRevision,
			"expected_config_revision":          port.ExpectedConfigRevision,
			"target_config_revision":            port.TargetConfigRevision,
			"expected_config_sha256":            strings.TrimSpace(port.ExpectedConfigSHA256),
			"target_config_sha256":              strings.TrimSpace(port.TargetConfigSHA256),
			"expected_source_policy_revision":   port.ExpectedSourcePolicyRevision,
			"expected_updater_policy_revision":  port.ExpectedUpdaterPolicyRevision,
			"expected_executor_policy_revision": port.ExpectedExecutorPolicyRevision,
			"expected_executor_policy_sha256":   strings.TrimSpace(port.ExpectedExecutorPolicySHA256),
			"port_plan_sha256":                  strings.TrimSpace(port.PortPlanSHA256),
		}
	}
	return metadata
}

func (s *Server) writeSystemUpdateMutationGrantConsumeAudit(r *http.Request, jobID, result string, metadata map[string]any) {
	s.writeAudit(r, store.AuditEvent{
		ActorUserID: "service:update_host", ActorUsername: "update_host",
		Action: "system_updates.mutation_grant.consume", ResourceType: "system_update", ResourceID: jobID,
		Result: result, Metadata: metadata,
	})
}
