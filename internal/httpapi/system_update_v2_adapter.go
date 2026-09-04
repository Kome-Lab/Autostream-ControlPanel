package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	contracts "github.com/example/autostream-contracts/pkg/contracts"
	"github.com/example/autostream-control-panel/internal/store"
)

const (
	systemUpdateContractMajorHeader = "X-AutoStream-Contract-Major"
	systemUpdateContractMajorV2     = "2"
	maxSystemUpdateV2PayloadBytes   = 1 << 20
)

// systemUpdateV2ContractBoundary keeps transport negotiation outside the
// route handlers so authentication failures and ServeMux-generated method
// errors carry the same non-cacheable v2 confirmation as successful calls.
func systemUpdateV2ContractBoundary(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		values := r.Header.Values(systemUpdateContractMajorHeader)
		if len(values) == 0 || !isSystemUpdateExecutionPath(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		w.Header().Set(systemUpdateContractMajorHeader, systemUpdateContractMajorV2)
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Pragma", "no-cache")
		w.Header().Set("Expires", "0")
		if len(values) != 1 || strings.TrimSpace(values[0]) != systemUpdateContractMajorV2 {
			writeJSON(w, http.StatusUpgradeRequired, map[string]string{"code": "updater_contract_major_unsupported"})
			return
		}
		next.ServeHTTP(w, r)
	})
}

func isSystemUpdateV2Request(r *http.Request) bool {
	values := r.Header.Values(systemUpdateContractMajorHeader)
	return len(values) == 1 && strings.TrimSpace(values[0]) == systemUpdateContractMajorV2
}

func isSystemUpdateExecutionPath(path string) bool {
	if path == "/services/update-jobs/claim" {
		return true
	}
	const prefix = "/services/update-jobs/"
	if !strings.HasPrefix(path, prefix) {
		return false
	}
	remainder := strings.TrimPrefix(path, prefix)
	jobID, suffix, ok := strings.Cut(remainder, "/")
	if !ok || jobID == "" || strings.Contains(jobID, "/") {
		return false
	}
	switch suffix {
	case "report", "authorize", "mutation-grants", "mutation-grants/consume":
		return true
	default:
		return false
	}
}

func writeSystemUpdateV2Clear(w http.ResponseWriter) {
	writeJSON(w, http.StatusOK, contracts.UpdateAgentClearActiveJobResponse{ClearActiveJobID: true})
}

func (s *Server) writeSystemUpdateV2Lease(w http.ResponseWriter, ctx context.Context, job store.SystemUpdateJob) error {
	lease, err := s.systemUpdateV2Lease(ctx, job)
	if err != nil {
		return err
	}
	payload, err := json.Marshal(lease)
	if err != nil || contracts.ValidateUpdaterLeaseEnvelope(systemUpdateV2ValidationTime(lease.LeaseExpiresAt), payload) != nil {
		return errors.New("validate projected updater v2 lease")
	}
	writeJSON(w, http.StatusOK, lease)
	return nil
}

func (s *Server) systemUpdateV2Lease(ctx context.Context, job store.SystemUpdateJob) (contracts.UpdaterLeaseEnvelope, error) {
	if job.ID == "" || job.AgentServiceID == "" || job.ExecutionHostID == "" ||
		job.LeaseGeneration < 1 || job.OwnershipEpoch < 1 ||
		normalizedHTTPSystemUpdateTransportMode(job.TransportMode) != store.SystemUpdateTransportPullV2 {
		return contracts.UpdaterLeaseEnvelope{}, errors.New("updater v2 job binding is incomplete")
	}
	expiresAt, err := systemUpdateV2LeaseExpiry(job)
	if err != nil {
		return contracts.UpdaterLeaseEnvelope{}, err
	}
	target, desiredRevision, desired, err := s.systemUpdateV2Intent(ctx, job)
	if err != nil {
		return contracts.UpdaterLeaseEnvelope{}, err
	}
	digest, err := contracts.ComputeUpdaterCommandCanonicalDigest(target, desiredRevision, job.OwnershipEpoch, desired)
	if err != nil {
		return contracts.UpdaterLeaseEnvelope{}, errors.New("compute updater v2 command digest")
	}
	generation := strconv.FormatInt(job.LeaseGeneration, 10)
	idSuffix := job.ID + ":" + generation
	capability, err := systemUpdateV2Capability(desired.Operation)
	if err != nil {
		return contracts.UpdaterLeaseEnvelope{}, err
	}
	command := contracts.UpdaterCommandEnvelope{
		ProtocolVersion: 2,
		CommandID:       "command:" + idSuffix,
		Issuer: contracts.UpdaterCommandIssuer{
			ServiceID:      controlPanelSystemUpdateServiceID,
			ServiceType:    controlPanelSystemUpdateServiceType,
			Authentication: "assignment_bound_rotating_service_identity",
			Permission:     "updates.authorize",
		},
		IdempotencyKey:         "lease:" + idSuffix,
		CanonicalPayloadDigest: digest,
		MutationAuthorization: contracts.UpdaterMutationAuthorization{
			AuthorizationID:         "authorization:" + idSuffix,
			NonceID:                 "nonce:" + idSuffix,
			JobID:                   job.ID,
			UpdaterID:               job.AgentServiceID,
			HostID:                  job.ExecutionHostID,
			ActionType:              capability,
			Target:                  target,
			CanonicalArgumentDigest: digest,
			DesiredRevision:         desiredRevision,
			Fence:                   job.OwnershipEpoch,
			ExpiresAt:               expiresAt,
			RequiredCapability:      capability,
			OneTime:                 true,
		},
		DesiredOperation:   desired,
		AuditCorrelationID: "audit:" + idSuffix,
	}
	lease := contracts.UpdaterLeaseEnvelope{
		ProtocolVersion: 2,
		LeaseID:         "lease:" + idSuffix,
		LeaseGeneration: job.LeaseGeneration,
		LeaseExpiresAt:  expiresAt,
		Command:         command,
	}
	payload, err := json.Marshal(lease)
	if err != nil || contracts.ValidateUpdaterLeaseEnvelope(systemUpdateV2ValidationTime(expiresAt), payload) != nil {
		return contracts.UpdaterLeaseEnvelope{}, errors.New("project updater v2 lease")
	}
	return lease, nil
}

func (s *Server) systemUpdateV2Intent(ctx context.Context, job store.SystemUpdateJob) (contracts.UpdaterTargetIdentity, int64, contracts.UpdaterDesiredOperation, error) {
	target := contracts.UpdaterTargetIdentity{
		TargetKind:     contracts.UpdaterTargetApplication,
		ServiceID:      job.TargetID,
		ServiceType:    contracts.SystemUpdateTargetType(job.TargetServiceType),
		DeploymentMode: contracts.SystemUpdateDeploymentMode(job.DeploymentMode),
	}
	var desired contracts.UpdaterDesiredOperation
	var desiredRevision int64
	switch job.Operation {
	case store.SystemUpdateOperationSoftwareUpdate:
		service, err := s.systemUpdateV2TargetService(ctx, job.TargetID)
		if err != nil || service.ServiceType != job.TargetServiceType || service.AppliedConfigRevision < 1 {
			return contracts.UpdaterTargetIdentity{}, 0, contracts.UpdaterDesiredOperation{}, errors.New("resolve updater v2 target identity")
		}
		target.ExpectedConfigRevision = service.AppliedConfigRevision
		desiredRevision = service.AppliedConfigRevision
		desired = contracts.UpdaterDesiredOperation{
			Operation: contracts.UpdaterDesiredSoftwareUpdate,
			SoftwareUpdate: &contracts.UpdaterSoftwareUpdateDesiredOperation{
				ExpectedCurrentVersion: job.CurrentVersion,
				TargetVersion:          job.TargetVersion,
				Strategy:               contracts.SystemUpdateStrategy(job.Strategy),
			},
		}
	case store.SystemUpdateOperationPortReconfigure:
		if job.PortReconfigure == nil || job.PortReconfigure.ExpectedConfigRevision < 1 || job.PortReconfigure.TargetConfigRevision < 1 {
			return contracts.UpdaterTargetIdentity{}, 0, contracts.UpdaterDesiredOperation{}, errors.New("updater v2 port intent is incomplete")
		}
		target.ExpectedConfigRevision = job.PortReconfigure.ExpectedConfigRevision
		desiredRevision = job.PortReconfigure.TargetConfigRevision
		desired = contracts.UpdaterDesiredOperation{
			Operation:       contracts.UpdaterDesiredPortReconfigure,
			PortReconfigure: systemUpdateV2PortPlan(job.PortReconfigure),
		}
	default:
		return contracts.UpdaterTargetIdentity{}, 0, contracts.UpdaterDesiredOperation{}, errors.New("updater v2 operation is unsupported")
	}
	return target, desiredRevision, desired, nil
}

func (s *Server) systemUpdateV2TargetService(ctx context.Context, targetID string) (store.RegisteredService, error) {
	if targetID == controlPanelSystemUpdateServiceID {
		return controlPanelSystemUpdateService()
	}
	if s.services == nil {
		return store.RegisteredService{}, store.ErrNotFound
	}
	return s.services.GetService(ctx, targetID)
}

func systemUpdateV2PortPlan(plan *store.SystemUpdatePortReconfiguration) *contracts.SystemUpdatePortReconfiguration {
	if plan == nil {
		return nil
	}
	mapped := &contracts.SystemUpdatePortReconfiguration{
		NetworkNamespace:               plan.NetworkNamespace,
		Protocol:                       contracts.SystemUpdatePortProtocol(plan.Protocol),
		OldPort:                        plan.OldPort,
		NewPort:                        plan.NewPort,
		ExpectedEndpointRevision:       plan.ExpectedEndpointRevision,
		TargetEndpointRevision:         plan.TargetEndpointRevision,
		ExpectedConfigRevision:         plan.ExpectedConfigRevision,
		TargetConfigRevision:           plan.TargetConfigRevision,
		ExpectedConfigSHA256:           plan.ExpectedConfigSHA256,
		TargetConfigSHA256:             plan.TargetConfigSHA256,
		ExpectedSourcePolicyRevision:   plan.ExpectedSourcePolicyRevision,
		ExpectedUpdaterPolicyRevision:  plan.ExpectedUpdaterPolicyRevision,
		ExpectedExecutorPolicyRevision: plan.ExpectedExecutorPolicyRevision,
		ExpectedExecutorPolicySHA256:   plan.ExpectedExecutorPolicySHA256,
		PortPlanSHA256:                 plan.PortPlanSHA256,
	}
	if plan.Docker != nil {
		mapped.Docker = &contracts.SystemUpdateDockerPortReconfiguration{
			PublishedHostIP:             plan.Docker.PublishedHostIP,
			OldPublishedPort:            plan.Docker.OldPublishedPort,
			NewPublishedPort:            plan.Docker.NewPublishedPort,
			OldContainerPort:            plan.Docker.OldContainerPort,
			NewContainerPort:            plan.Docker.NewContainerPort,
			OldHealthPort:               plan.Docker.OldHealthPort,
			NewHealthPort:               plan.Docker.NewHealthPort,
			ApprovedComposeConfigSHA256: plan.Docker.ApprovedComposeConfigSHA256,
			ApprovedComposeRevision:     plan.Docker.ApprovedComposeRevision,
			ExpectedVersionEnvSHA256:    plan.Docker.ExpectedVersionEnvSHA256,
			ExpectedContainerID:         plan.Docker.ExpectedContainerID,
			ExpectedImageID:             plan.Docker.ExpectedImageID,
			ExpectedRepositoryDigest:    plan.Docker.ExpectedRepositoryDigest,
		}
	}
	return mapped
}

func systemUpdateV2LeaseExpiry(job store.SystemUpdateJob) (time.Time, error) {
	if job.LeaseExpiresAt != nil && !job.LeaseExpiresAt.IsZero() {
		return job.LeaseExpiresAt.UTC(), nil
	}
	if job.CompletedAt != nil && !job.CompletedAt.IsZero() {
		return job.CompletedAt.UTC().Add(systemUpdateExecutionLeaseTTL), nil
	}
	return time.Time{}, errors.New("updater v2 lease expiry is unavailable")
}

func systemUpdateV2ValidationTime(expiresAt time.Time) time.Time {
	now := time.Now().UTC()
	if now.Before(expiresAt) {
		return now
	}
	return expiresAt.Add(-time.Nanosecond)
}

func systemUpdateV2Capability(operation contracts.UpdaterDesiredOperationType) (contracts.UpdaterCapability, error) {
	switch operation {
	case contracts.UpdaterDesiredSoftwareUpdate:
		return contracts.UpdaterCapabilityUpdate, nil
	case contracts.UpdaterDesiredPortReconfigure:
		return contracts.UpdaterCapabilityPort, nil
	default:
		return "", errors.New("updater v2 capability is unsupported")
	}
}

func normalizedHTTPSystemUpdateTransportMode(mode string) string {
	return strings.ToLower(strings.TrimSpace(mode))
}

func (s *Server) serviceSystemUpdateReportV2(w http.ResponseWriter, r *http.Request) {
	token, ok := s.authenticateService(w, r, "updates.report")
	if !ok {
		return
	}
	payload, err := readSystemUpdateV2Payload(r)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_updater_v2_report"})
		return
	}
	jobID := strings.TrimSpace(r.PathValue("id"))
	s.systemUpdateOperationMu.Lock()
	defer s.systemUpdateOperationMu.Unlock()
	token, ok = s.reauthenticateService(w, r, token, "updates.report")
	if !ok {
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
	agent, err := s.systemUpdateAgentForToken(r.Context(), token, job.AgentServiceID)
	if err != nil {
		writeSystemUpdateAgentError(w, err)
		return
	}
	if err := s.validatePullSystemUpdateAgentOwnership(r.Context(), agent); err != nil {
		writeSystemUpdateV2OwnershipError(w, err)
		return
	}
	lease, err := s.systemUpdateV2Lease(r.Context(), job)
	if err != nil {
		writeJSON(w, http.StatusConflict, map[string]string{"code": "updater_v2_lease_unavailable"})
		return
	}
	report, err := mapSystemUpdateV2Report(job, lease, payload)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_updater_v2_report"})
		return
	}
	updated, applied, err := s.systemUpdates.ReportSystemUpdateJob(r.Context(), jobID, report, time.Now().UTC(), systemUpdateExecutionLeaseTTL)
	if err != nil {
		writeSystemUpdateV2ReportError(w, err)
		return
	}
	if applied && systemUpdateStatusTerminal(updated.Status) {
		result := "success"
		if updated.Status != store.SystemUpdateStatusSucceeded {
			result = "failure"
		}
		s.writeServiceAudit(r, token, "system_updates."+updated.Status, "system_update", updated.ID, result, map[string]any{
			"agent_service_id": agent.ServiceID,
			"target_id":        updated.TargetID,
			"target_version":   updated.TargetVersion,
			"operation":        updated.Operation,
			"status":           updated.Status,
			"code":             updated.Code,
			"protocol_version": 2,
		})
	}
	writeJSON(w, http.StatusOK, updated)
}

func mapSystemUpdateV2Report(job store.SystemUpdateJob, lease contracts.UpdaterLeaseEnvelope, payload []byte) (store.SystemUpdateReport, error) {
	if contracts.ValidateUpdaterProgressEnvelope(lease, payload) == nil {
		var progress contracts.UpdaterProgressEnvelope
		if json.Unmarshal(payload, &progress) != nil {
			return store.SystemUpdateReport{}, errors.New("decode updater v2 progress")
		}
		status, ok := systemUpdateV2ProgressStatus(progress.Phase)
		if !ok {
			return store.SystemUpdateReport{}, errors.New("map updater v2 progress")
		}
		return store.SystemUpdateReport{
			ProtocolVersion: 2,
			AgentServiceID:  progress.UpdaterID,
			ExecutionHostID: progress.HostID,
			LeaseGeneration: progress.LeaseGeneration,
			DesiredRevision: progress.DesiredRevision,
			Fence:           progress.Fence,
			Sequence:        progress.Sequence,
			Status:          status,
			Progress:        progress.Progress,
		}, nil
	}
	if contracts.ValidateUpdaterResultEnvelope(lease, payload) != nil {
		return store.SystemUpdateReport{}, errors.New("validate updater v2 result")
	}
	var result contracts.UpdaterResultEnvelope
	if json.Unmarshal(payload, &result) != nil {
		return store.SystemUpdateReport{}, errors.New("decode updater v2 result")
	}
	sequence := job.Sequence + 1
	if systemUpdateStatusTerminal(job.Status) {
		sequence = job.Sequence
	}
	progress := 100
	code, message := "", ""
	if result.Outcome == contracts.UpdaterOutcomeAmbiguous {
		progress = job.Progress
		if job.Status == store.SystemUpdateStatusReconciling && job.Code == "outcome_ambiguous" {
			sequence = job.Sequence
		}
	}
	if result.SafeError != nil {
		code = result.SafeError.Code
		message = result.SafeError.Message
	}
	artifact, previous := systemUpdateV2EvidenceDigests(result)
	report := store.SystemUpdateReport{
		ProtocolVersion: 2,
		AgentServiceID:  result.UpdaterID,
		ExecutionHostID: result.HostID,
		LeaseGeneration: result.LeaseGeneration,
		DesiredRevision: result.DesiredRevision,
		Fence:           result.Fence,
		Sequence:        sequence,
		Status:          string(result.Status),
		Progress:        progress,
		Code:            code,
		Message:         message,
		ArtifactDigest:  artifact,
		PreviousDigest:  previous,
	}
	if job.Operation == store.SystemUpdateOperationPortReconfigure && result.Outcome != contracts.UpdaterOutcomeAmbiguous {
		portResult := store.SystemUpdatePortReconfigurationApplied
		switch result.Outcome {
		case contracts.UpdaterOutcomeSucceeded:
		case contracts.UpdaterOutcomeRolledBack:
			portResult = store.SystemUpdatePortReconfigurationRolledBack
		case contracts.UpdaterOutcomeFailed:
			portResult = store.SystemUpdatePortReconfigurationRollbackFailed
		default:
			return store.SystemUpdateReport{}, errors.New("map updater v2 port result")
		}
		report.PortReconfigure = &store.SystemUpdatePortReconfiguration{Result: portResult}
	}
	return report, nil
}

func systemUpdateV2ProgressStatus(phase string) (string, bool) {
	switch phase {
	case "accepted":
		return store.SystemUpdateStatusClaimed, true
	case "journaled", "preparing":
		return store.SystemUpdateStatusDownloading, true
	case "executing":
		return store.SystemUpdateStatusInstalling, true
	case "verifying":
		return store.SystemUpdateStatusHealthChecking, true
	case "rolling_back":
		return store.SystemUpdateStatusRollingBack, true
	case "reconciling":
		return store.SystemUpdateStatusReconciling, true
	default:
		return "", false
	}
}

func systemUpdateV2EvidenceDigests(result contracts.UpdaterResultEnvelope) (string, string) {
	artifact, previous := "", ""
	for _, evidence := range result.Evidence {
		if evidence.ArtifactDigest == "" {
			continue
		}
		if result.Outcome == contracts.UpdaterOutcomeRolledBack {
			if previous == "" {
				previous = evidence.ArtifactDigest
			}
		} else if artifact == "" {
			artifact = evidence.ArtifactDigest
		}
	}
	return artifact, previous
}

func readSystemUpdateV2Payload(r *http.Request) ([]byte, error) {
	payload, err := io.ReadAll(io.LimitReader(r.Body, maxSystemUpdateV2PayloadBytes+1))
	if err != nil || len(payload) == 0 || len(payload) > maxSystemUpdateV2PayloadBytes {
		return nil, errors.New("updater v2 payload is invalid")
	}
	return payload, nil
}

func sameSystemUpdateV2Lease(left, right contracts.UpdaterLeaseEnvelope) bool {
	leftJSON, leftErr := json.Marshal(left)
	rightJSON, rightErr := json.Marshal(right)
	return leftErr == nil && rightErr == nil && bytes.Equal(leftJSON, rightJSON)
}

func writeSystemUpdateV2JobError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "system_update_job_not_found"})
	case errors.Is(err, store.ErrInvalidSystemUpdate):
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_system_update_job"})
	default:
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "get_system_update_job_failed"})
	}
}

func writeSystemUpdateV2OwnershipError(w http.ResponseWriter, err error) {
	if errors.Is(err, store.ErrSystemUpdateOwnershipConflict) {
		writeJSON(w, http.StatusConflict, map[string]string{"code": "system_update_ownership_conflict"})
		return
	}
	writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "resolve_system_update_ownership_failed"})
}

func writeSystemUpdateV2ReportError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "system_update_job_not_found"})
	case errors.Is(err, store.ErrSystemUpdateLeaseInvalid):
		writeJSON(w, http.StatusConflict, map[string]string{"code": "system_update_lease_invalid"})
	case errors.Is(err, store.ErrSystemUpdateSequenceStale):
		writeJSON(w, http.StatusConflict, map[string]string{"code": "system_update_sequence_stale"})
	case errors.Is(err, store.ErrSystemUpdateTransition):
		writeJSON(w, http.StatusConflict, map[string]string{"code": "system_update_transition_invalid"})
	case errors.Is(err, store.ErrSystemUpdateOwnershipConflict):
		writeJSON(w, http.StatusConflict, map[string]string{"code": "system_update_ownership_conflict"})
	case errors.Is(err, store.ErrSystemUpdateEndpointStale), errors.Is(err, store.ErrConflict):
		writeJSON(w, http.StatusConflict, map[string]string{"code": "system_update_endpoint_revision_conflict"})
	case errors.Is(err, store.ErrServicePortReserved):
		writeJSON(w, http.StatusConflict, map[string]string{"code": "service_port_reserved"})
	case errors.Is(err, store.ErrSystemUpdatePortStoreMismatch), errors.Is(err, store.ErrSystemUpdatePortCoordinatorRequired):
		writeJSON(w, http.StatusConflict, map[string]string{"code": "system_update_port_store_mismatch"})
	case errors.Is(err, store.ErrInvalidSystemUpdate):
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_system_update_report"})
	default:
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "report_system_update_failed"})
	}
}

func systemUpdateV2BindingError(operation string) error {
	return fmt.Errorf("updater v2 %s binding does not match the authoritative job", operation)
}
