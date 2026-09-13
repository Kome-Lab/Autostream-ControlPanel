package store

import (
	"encoding/hex"
	"github.com/example/autostream-control-panel/internal/security"
	"strings"
	"time"
)

func normalizeSystemUpdateCreate(params CreateSystemUpdateJobParams) CreateSystemUpdateJobParams {
	params.TargetID = strings.TrimSpace(params.TargetID)
	params.TargetServiceType = strings.TrimSpace(params.TargetServiceType)
	params.Operation = strings.ToLower(strings.TrimSpace(params.Operation))
	if params.Operation == "" {
		params.Operation = SystemUpdateOperationSoftwareUpdate
	}
	params.PortReconfigure = normalizeSystemUpdatePortReconfiguration(params.PortReconfigure)
	params.AgentServiceID = strings.TrimSpace(params.AgentServiceID)
	params.ExecutionHostID = normalizeSystemUpdateExecutionHostID(params.AgentServiceID, params.ExecutionHostID)
	params.DeploymentMode = strings.ToLower(strings.TrimSpace(params.DeploymentMode))
	params.CurrentVersion = strings.TrimSpace(params.CurrentVersion)
	params.TargetVersion = strings.TrimSpace(params.TargetVersion)
	params.Strategy = strings.ToLower(strings.TrimSpace(params.Strategy))
	params.IdempotencyKey = strings.TrimSpace(params.IdempotencyKey)
	params.RequestedByUserID = strings.TrimSpace(params.RequestedByUserID)
	params.RequestedByUsername = strings.TrimSpace(params.RequestedByUsername)
	return params
}

func normalizeSystemUpdateAuthorization(authorization SystemUpdateAuthorization) SystemUpdateAuthorization {
	authorization.AgentServiceID = strings.TrimSpace(authorization.AgentServiceID)
	authorization.ExecutionHostID = normalizeSystemUpdateExecutionHostID(authorization.AgentServiceID, authorization.ExecutionHostID)
	authorization.LeaseToken = strings.TrimSpace(authorization.LeaseToken)
	authorization.TargetID = strings.TrimSpace(authorization.TargetID)
	authorization.TargetVersion = strings.TrimSpace(authorization.TargetVersion)
	authorization.DeploymentMode = strings.ToLower(strings.TrimSpace(authorization.DeploymentMode))
	return authorization
}

func validateSystemUpdateAuthorization(authorization SystemUpdateAuthorization) error {
	if authorization.AgentServiceID == "" || !validSystemUpdateExecutionHostID(authorization.ExecutionHostID) || authorization.LeaseToken == "" || authorization.LeaseGeneration <= 0 ||
		authorization.TargetID == "" || authorization.TargetVersion == "" || !validSystemUpdateDeploymentMode(authorization.DeploymentMode) {
		return ErrInvalidSystemUpdate
	}
	if len(authorization.AgentServiceID) > 191 || len(authorization.LeaseToken) > 256 || len(authorization.TargetID) > 191 || len(authorization.TargetVersion) > 128 ||
		containsControl(authorization.AgentServiceID) || containsControl(authorization.LeaseToken) || containsControl(authorization.TargetID) || containsControl(authorization.TargetVersion) {
		return ErrInvalidSystemUpdate
	}
	return nil
}

func authorizeSystemUpdateMutation(job SystemUpdateJob, authorization SystemUpdateAuthorization, now time.Time) error {
	if job.Status != SystemUpdateStatusInstalling && job.Status != SystemUpdateStatusReconciling {
		return ErrSystemUpdateAuthorizationState
	}
	if job.AgentServiceID != authorization.AgentServiceID || job.LeaseGeneration != authorization.LeaseGeneration ||
		job.LeaseExpiresAt == nil || !job.LeaseExpiresAt.After(now) || !security.VerifyTokenHash(authorization.LeaseToken, job.leaseTokenHash) {
		return ErrSystemUpdateLeaseInvalid
	}
	if job.ExecutionHostID != authorization.ExecutionHostID || job.TargetID != authorization.TargetID || job.TargetVersion != authorization.TargetVersion || job.DeploymentMode != authorization.DeploymentMode {
		return ErrSystemUpdateAuthorizationMismatch
	}
	return nil
}

func authorizeSystemUpdateJobOwnership(job SystemUpdateJob, ownership SystemUpdateExecutionHost, agentServiceID, executionHostID string) error {
	agentServiceID = strings.TrimSpace(agentServiceID)
	executionHostID = strings.TrimSpace(executionHostID)
	transportMode := normalizedSystemUpdateTransportMode(job.TransportMode)
	if ownership.ExecutionHostID != executionHostID ||
		job.ExecutionHostID != executionHostID ||
		transportMode != ownership.TransportMode ||
		job.OwnershipEpoch != ownership.OwnershipEpoch ||
		(!isSystemUpdatePortV2(job) && job.PolicyRevision != ownership.PolicyRevision || isSystemUpdatePortV2(job) && !systemUpdatePortV2OwnershipMatches(job, ownership)) {
		return ErrSystemUpdateOwnershipConflict
	}
	if ownership.OwnershipEpoch > 0 &&
		(ownership.AgentServiceID != agentServiceID || job.AgentServiceID != agentServiceID) {
		return ErrSystemUpdateOwnershipConflict
	}
	if transportMode == SystemUpdateTransportPullV2 &&
		(job.OwnershipEpoch < 1 || job.PolicyRevision < 1) {
		return ErrSystemUpdateOwnershipConflict
	}
	return nil
}

func authorizeSystemUpdateExecutionHostAgent(ownership SystemUpdateExecutionHost, agentServiceID string) error {
	agentServiceID = strings.TrimSpace(agentServiceID)
	if normalizedSystemUpdateTransportMode(ownership.TransportMode) != SystemUpdateTransportPullV2 ||
		ownership.OwnershipEpoch < 1 ||
		ownership.PolicyRevision < 1 ||
		ownership.AgentServiceID != agentServiceID {
		return ErrSystemUpdateOwnershipConflict
	}
	return nil
}

func authenticatedSystemUpdateExecutionHost(job SystemUpdateJob, executionHostID string) string {
	executionHostID = strings.TrimSpace(executionHostID)
	if executionHostID != "" {
		return executionHostID
	}
	return ""
}

func normalizedSystemUpdateTransportMode(transportMode string) string {
	transportMode = strings.ToLower(strings.TrimSpace(transportMode))
	return transportMode
}

func validateSystemUpdateCreate(params CreateSystemUpdateJobParams) error {
	if params.TargetID == "" || len(params.TargetID) > 191 || params.TargetServiceType == "" || len(params.TargetServiceType) > 64 || params.AgentServiceID == "" || len(params.AgentServiceID) > 191 || !validSystemUpdateExecutionHostID(params.ExecutionHostID) || params.CurrentVersion == "" || len(params.CurrentVersion) > 128 || params.TargetVersion == "" || len(params.TargetVersion) > 128 || params.RequestedByUserID == "" || len(params.RequestedByUserID) > 64 || params.IdempotencyKey == "" || len(params.IdempotencyKey) > 128 {
		return ErrInvalidSystemUpdate
	}
	if !systemUpdateJobVersionPattern.MatchString(params.CurrentVersion) || !systemUpdateJobVersionPattern.MatchString(params.TargetVersion) {
		return ErrInvalidSystemUpdate
	}
	if !validSystemUpdateDeploymentMode(params.DeploymentMode) {
		return ErrInvalidSystemUpdate
	}
	if params.Strategy != SystemUpdateStrategyWhenIdle && params.Strategy != SystemUpdateStrategyMaintenance {
		return ErrInvalidSystemUpdate
	}
	if containsControl(params.TargetID) || containsControl(params.IdempotencyKey) {
		return ErrInvalidSystemUpdate
	}
	switch params.Operation {
	case SystemUpdateOperationSoftwareUpdate:
		if params.PortReconfigure != nil {
			return ErrInvalidSystemUpdate
		}
	case SystemUpdateOperationPortReconfigure:
		if validateSystemUpdatePortReconfigurationPlanForDeployment(
			params.PortReconfigure,
			strings.ToLower(strings.TrimSpace(params.DeploymentMode)),
		) != nil {
			return ErrInvalidSystemUpdate
		}
	default:
		return ErrInvalidSystemUpdate
	}
	return nil
}

func normalizeSystemUpdateExecutionHostID(agentServiceID, executionHostID string) string {
	executionHostID = strings.TrimSpace(executionHostID)
	if executionHostID == "" {
		return strings.TrimSpace(agentServiceID)
	}
	return executionHostID
}

func validSystemUpdateExecutionHostID(executionHostID string) bool {
	return executionHostID != "" && len(executionHostID) <= 191 && !containsControl(executionHostID)
}

func validSystemUpdateDeploymentMode(mode string) bool {
	return mode == "systemd" || mode == "docker"
}

func normalizeSystemUpdateReport(report SystemUpdateReport) SystemUpdateReport {
	report.AgentServiceID = strings.TrimSpace(report.AgentServiceID)
	report.ExecutionHostID = strings.TrimSpace(report.ExecutionHostID)
	report.LeaseToken = strings.TrimSpace(report.LeaseToken)
	report.Status = strings.ToLower(strings.TrimSpace(report.Status))
	report.Code = strings.TrimSpace(report.Code)
	report.Message = sanitizeSystemUpdateMessage(report.Message)
	report.ArtifactDigest = strings.TrimSpace(report.ArtifactDigest)
	report.PreviousDigest = strings.TrimSpace(report.PreviousDigest)
	report.PortReconfigure = normalizeSystemUpdatePortReconfiguration(report.PortReconfigure)
	return report
}

func validateSystemUpdateReport(report SystemUpdateReport) error {
	if report.AgentServiceID == "" || report.LeaseGeneration <= 0 || report.Sequence <= 0 || report.Progress < 0 || report.Progress > 100 || !validSystemUpdateCode(report.Code) || len(report.Message) > 500 || !validSystemUpdateDigest(report.ArtifactDigest) || !validSystemUpdateDigest(report.PreviousDigest) || !isReportableSystemUpdateStatus(report.Status) {
		return ErrInvalidSystemUpdate
	}
	switch report.ProtocolVersion {
	case 0, 1:
		if report.LeaseToken == "" || report.DesiredRevision != 0 || report.Fence != 0 {
			return ErrInvalidSystemUpdate
		}
	case 2:
		if report.LeaseToken != "" || report.DesiredRevision < 1 || report.Fence < 1 {
			return ErrInvalidSystemUpdate
		}
	default:
		return ErrInvalidSystemUpdate
	}
	if report.ExecutionHostID != "" && !validSystemUpdateExecutionHostID(report.ExecutionHostID) {
		return ErrInvalidSystemUpdate
	}
	if report.PortReconfigure != nil && validateSystemUpdatePortReconfigurationResult(report.PortReconfigure) != nil {
		return ErrInvalidSystemUpdate
	}
	return nil
}

func systemUpdateReportLeaseMatches(job SystemUpdateJob, report SystemUpdateReport, now time.Time, requireUnexpired bool) bool {
	if job.AgentServiceID != report.AgentServiceID || job.LeaseGeneration != report.LeaseGeneration {
		return false
	}
	if requireUnexpired && (job.LeaseExpiresAt == nil || !job.LeaseExpiresAt.After(now)) {
		return false
	}
	switch report.ProtocolVersion {
	case 0, 1:
		return security.VerifyTokenHash(report.LeaseToken, job.leaseTokenHash)
	case 2:
		return normalizedSystemUpdateTransportMode(job.TransportMode) == SystemUpdateTransportPullV2 &&
			report.LeaseToken == "" &&
			report.ExecutionHostID == job.ExecutionHostID &&
			report.Fence == job.OwnershipEpoch
	default:
		return false
	}
}

func validSystemUpdateCode(value string) bool {
	if value == "" {
		return true
	}
	if len(value) > 128 {
		return false
	}
	for _, r := range value {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '_' && r != '.' && r != '-' {
			return false
		}
	}
	return true
}

func validSystemUpdateDigest(value string) bool {
	if value == "" {
		return true
	}
	if len(value) != 71 || !strings.HasPrefix(value, "sha256:") || value != strings.ToLower(value) {
		return false
	}
	_, err := hex.DecodeString(strings.TrimPrefix(value, "sha256:"))
	return err == nil
}

func normalizedEligibleTargets(input map[string]string) map[string]string {
	out := make(map[string]string, len(input))
	for id, mode := range input {
		id = strings.TrimSpace(id)
		mode = strings.ToLower(strings.TrimSpace(mode))
		if id != "" && len(id) <= 191 && (mode == "systemd" || mode == "docker") {
			out[id] = mode
		}
	}
	return out
}

func sameSystemUpdateRequest(job SystemUpdateJob, params CreateSystemUpdateJobParams) bool {
	if job.TargetID != params.TargetID || job.Strategy != params.Strategy || job.Operation != params.Operation {
		return false
	}
	if job.Operation == SystemUpdateOperationPortReconfigure {
		return sameSystemUpdatePortReconfigurationIntent(job.PortReconfigure, params.PortReconfigure)
	}
	return true
}

func sameSystemUpdateReport(job SystemUpdateJob, report SystemUpdateReport) bool {
	if isSystemUpdatePortV2(job) {
		return job.Status == report.Status && job.Progress == report.Progress && job.Code == report.Code && job.Message == report.Message && job.ArtifactDigest == report.ArtifactDigest && job.PreviousDigest == report.PreviousDigest && sameSystemUpdatePortV2Report(job, report)
	}
	return job.Status == report.Status && job.Progress == report.Progress && job.Code == report.Code && job.Message == report.Message && job.ArtifactDigest == report.ArtifactDigest && job.PreviousDigest == report.PreviousDigest &&
		sameSystemUpdatePortReconfigurationResult(job.PortReconfigure, report.PortReconfigure)
}

func allowedSystemUpdateTransition(current, next string) bool {
	if current == next {
		return !isTerminalSystemUpdateStatus(current)
	}
	if isTerminalSystemUpdateStatus(current) {
		return false
	}
	if current == SystemUpdateStatusReconciling {
		return next == SystemUpdateStatusSucceeded || next == SystemUpdateStatusRolledBack || next == SystemUpdateStatusFailed
	}
	if next == SystemUpdateStatusReconciling {
		return current == SystemUpdateStatusInstalling || current == SystemUpdateStatusStarting || current == SystemUpdateStatusHealthChecking || current == SystemUpdateStatusRollingBack
	}
	if next == SystemUpdateStatusFailed {
		return true
	}
	// Older pull agents can persist a verified rollback result without the
	// intermediate rolling_back report. Accept that terminal report so an
	// already-running job can settle; current agents emit rolling_back first.
	if current == SystemUpdateStatusHealthChecking && next == SystemUpdateStatusRolledBack {
		return true
	}
	if current == SystemUpdateStatusRollingBack {
		return next == SystemUpdateStatusRolledBack
	}
	if next == SystemUpdateStatusRollingBack {
		return systemUpdateForwardRank(current) >= systemUpdateForwardRank(SystemUpdateStatusStopping)
	}
	currentRank := systemUpdateForwardRank(current)
	nextRank := systemUpdateForwardRank(next)
	return currentRank >= 0 && nextRank > currentRank
}

func systemUpdateForwardRank(status string) int {
	order := map[string]int{
		SystemUpdateStatusClaimed: 0, SystemUpdateStatusDownloading: 1, SystemUpdateStatusVerifying: 2,
		SystemUpdateStatusStaging: 3, SystemUpdateStatusStopping: 4, SystemUpdateStatusInstalling: 5,
		SystemUpdateStatusStarting: 6, SystemUpdateStatusHealthChecking: 7, SystemUpdateStatusSucceeded: 8,
	}
	if rank, ok := order[status]; ok {
		return rank
	}
	return -1
}

func isReportableSystemUpdateStatus(status string) bool {
	switch status {
	case SystemUpdateStatusClaimed, SystemUpdateStatusDownloading, SystemUpdateStatusVerifying, SystemUpdateStatusStaging, SystemUpdateStatusStopping, SystemUpdateStatusInstalling, SystemUpdateStatusStarting, SystemUpdateStatusHealthChecking, SystemUpdateStatusReconciling, SystemUpdateStatusSucceeded, SystemUpdateStatusRollingBack, SystemUpdateStatusRolledBack, SystemUpdateStatusFailed:
		return true
	default:
		return false
	}
}

func isTerminalSystemUpdateStatus(status string) bool {
	return status == SystemUpdateStatusSucceeded || status == SystemUpdateStatusRolledBack || status == SystemUpdateStatusFailed || status == SystemUpdateStatusCancelled
}

func isExecutingSystemUpdateStatus(status string) bool {
	switch status {
	case SystemUpdateStatusClaimed, SystemUpdateStatusDownloading, SystemUpdateStatusVerifying, SystemUpdateStatusStaging, SystemUpdateStatusStopping, SystemUpdateStatusInstalling, SystemUpdateStatusStarting, SystemUpdateStatusHealthChecking, SystemUpdateStatusRollingBack, SystemUpdateStatusReconciling:
		return true
	default:
		return false
	}
}

func sanitizeSystemUpdateMessage(message string) string {
	message = strings.TrimSpace(message)
	message = strings.Map(func(r rune) rune {
		if r < 0x20 && r != '\t' {
			return ' '
		}
		return r
	}, message)
	fields := strings.Fields(message)
	redactNext := false
	for i, field := range fields {
		if redactNext {
			fields[i] = "[REDACTED]"
			redactNext = false
			continue
		}
		lower := strings.ToLower(field)
		label := strings.Trim(lower, "[](){}<>,;")
		if strings.EqualFold(label, "bearer") || label == "authorization:" || label == "token:" || label == "access_token:" || label == "secret:" || label == "password:" {
			redactNext = true
		}
		if strings.HasPrefix(field, "ast_svc_") || strings.HasPrefix(field, "ast_update_") || strings.HasPrefix(field, "ghp_") || strings.HasPrefix(lower, "github_pat_") || strings.Contains(lower, "authorization:") || strings.Contains(lower, "access_token=") || strings.Contains(lower, "token=") || strings.Contains(lower, "secret=") || strings.Contains(lower, "password=") || (strings.Contains(field, "://") && strings.Contains(field, "@")) {
			fields[i] = "[REDACTED]"
		}
	}
	message = strings.Join(fields, " ")
	if len(message) > 500 {
		message = message[:500]
	}
	return message
}

func containsControl(value string) bool {
	for _, r := range value {
		if r < 0x20 || r == 0x7f {
			return true
		}
	}
	return false
}
