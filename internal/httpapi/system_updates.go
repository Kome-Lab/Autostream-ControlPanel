package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/example/autostream-control-panel/internal/store"
	"github.com/example/autostream-control-panel/internal/version"
)

const systemUpdateClaimLeaseTTL = 2 * time.Minute
const systemUpdateExecutionLeaseTTL = 45 * time.Minute
const systemUpdateHostReachabilityTTL = 2 * time.Minute
const systemUpdateHostClockSkew = 30 * time.Second

type systemUpdateTargetResponse struct {
	TargetID          string `json:"target_id"`
	ServiceType       string `json:"target_type"`
	Name              string `json:"name"`
	HostID            string `json:"host_id,omitempty"`
	CurrentVersion    string `json:"current_version,omitempty"`
	LatestVersion     string `json:"latest_version,omitempty"`
	UpdateAvailable   bool   `json:"update_available"`
	DeploymentMode    string `json:"deployment_mode,omitempty"`
	UpdateAgentID     string `json:"updater_id,omitempty"`
	UpdaterOnline     bool   `json:"updater_online"`
	Eligible          bool   `json:"eligible"`
	BlockedReason     string `json:"blocked_reason,omitempty"`
	Busy              bool   `json:"busy"`
	CurrentStreamID   string `json:"current_stream_id,omitempty"`
	UpdateCheckSource string `json:"update_check_source,omitempty"`
	UpdateCheckError  string `json:"update_check_error,omitempty"`
}

type systemUpdateAgentResponse struct {
	UpdaterID     string     `json:"updater_id"`
	Name          string     `json:"name"`
	Status        string     `json:"status"`
	Online        bool       `json:"online"`
	Version       string     `json:"version"`
	LastHeartbeat *time.Time `json:"last_heartbeat_at,omitempty"`
}

type systemUpdateHostResponse struct {
	HostID       string     `json:"host_id"`
	Name         string     `json:"name"`
	UpdaterID    string     `json:"updater_id"`
	Reachability string     `json:"reachability"`
	CheckedAt    *time.Time `json:"reachability_checked_at,omitempty"`
	Code         string     `json:"reachability_code,omitempty"`
}

type systemUpdateAgentAssignment struct {
	AgentID          string
	AgentVersion     string
	DeploymentMode   string
	CurrentVersion   string
	Available        bool
	HostID           string
	HostName         string
	HostReachability string
	HostCheckedAt    *time.Time
	HostCode         string
}

func (s *Server) listSystemUpdates(w http.ResponseWriter, r *http.Request) {
	targets, updaters, hosts, err := s.systemUpdateSnapshot(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "list_system_update_targets_failed"})
		return
	}
	jobs, err := s.systemUpdates.ListSystemUpdateJobs(r.Context(), parseLimit(r, 100))
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "list_system_update_jobs_failed"})
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{"updaters": updaters, "hosts": hosts, "targets": targets, "jobs": jobs})
}

func (s *Server) createSystemUpdate(w http.ResponseWriter, r *http.Request) {
	var body struct {
		TargetID       string `json:"target_id"`
		Strategy       string `json:"strategy"`
		IdempotencyKey string `json:"idempotency_key"`
	}
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "bad_request"})
		return
	}
	body.TargetID = strings.TrimSpace(body.TargetID)
	body.Strategy = strings.ToLower(strings.TrimSpace(body.Strategy))
	body.IdempotencyKey = strings.TrimSpace(body.IdempotencyKey)
	if body.TargetID == "" || body.IdempotencyKey == "" || (body.Strategy != store.SystemUpdateStrategyWhenIdle && body.Strategy != store.SystemUpdateStrategyMaintenance) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_system_update_request"})
		return
	}
	s.systemUpdateOperationMu.Lock()
	defer s.systemUpdateOperationMu.Unlock()
	current := currentFromContext(r.Context())
	existing, err := s.systemUpdates.GetSystemUpdateJobByIdempotency(r.Context(), current.User.ID, body.IdempotencyKey)
	if err == nil {
		if existing.TargetID != body.TargetID || existing.Strategy != body.Strategy {
			writeJSON(w, http.StatusConflict, map[string]string{"code": "idempotency_key_conflict"})
			return
		}
		writeJSON(w, http.StatusAccepted, existing)
		return
	}
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "get_system_update_idempotency_failed"})
		return
	}
	targets, err := s.systemUpdateTargets(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "list_system_update_targets_failed"})
		return
	}
	var target *systemUpdateTargetResponse
	for index := range targets {
		if targets[index].TargetID == body.TargetID {
			target = &targets[index]
			break
		}
	}
	if target == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "system_update_target_not_found"})
		return
	}
	if !target.Eligible {
		code := target.BlockedReason
		if code == "" {
			code = "system_update_target_unavailable"
		}
		writeJSON(w, http.StatusConflict, map[string]any{"code": code, "target": target})
		return
	}
	if body.Strategy == store.SystemUpdateStrategyMaintenance && target.Busy {
		writeJSON(w, http.StatusConflict, map[string]any{"code": "system_update_target_busy", "current_stream_id": target.CurrentStreamID})
		return
	}
	job, created, err := s.systemUpdates.CreateSystemUpdateJob(r.Context(), store.CreateSystemUpdateJobParams{
		TargetID: target.TargetID, TargetServiceType: target.ServiceType, DeploymentMode: target.DeploymentMode,
		AgentServiceID: target.UpdateAgentID, ExecutionHostID: target.HostID,
		CurrentVersion: target.CurrentVersion, TargetVersion: target.LatestVersion, Strategy: body.Strategy,
		IdempotencyKey: body.IdempotencyKey, RequestedByUserID: current.User.ID, RequestedByUsername: current.User.Username,
	})
	if errors.Is(err, store.ErrSystemUpdateTargetActive) {
		writeJSON(w, http.StatusConflict, map[string]string{"code": "system_update_target_active"})
		return
	}
	if errors.Is(err, store.ErrAlreadyExists) {
		writeJSON(w, http.StatusConflict, map[string]string{"code": "idempotency_key_conflict"})
		return
	}
	if errors.Is(err, store.ErrInvalidSystemUpdate) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_system_update_request"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "create_system_update_failed"})
		return
	}
	if created {
		s.writeAudit(r, store.AuditEvent{
			ActorUserID: current.User.ID, ActorUsername: current.User.Username,
			Action: "system_updates.create", ResourceType: "system_update", ResourceID: job.ID, Result: "success",
			Metadata: map[string]any{"target_id": job.TargetID, "service_type": job.TargetServiceType, "deployment_mode": job.DeploymentMode, "current_version": job.CurrentVersion, "target_version": job.TargetVersion, "strategy": job.Strategy, "idempotent_replay": false},
		})
	}
	writeJSON(w, http.StatusAccepted, job)
}

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
	token, ok := s.authenticateService(w, r, "updates.claim")
	if !ok {
		return
	}
	var body struct {
		ServiceID   string  `json:"service_id"`
		HostID      string  `json:"host_id,omitempty"`
		ActiveJobID *string `json:"active_job_id,omitempty"`
	}
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "bad_request"})
		return
	}
	activeJobID := ""
	if body.ActiveJobID != nil {
		activeJobID = strings.TrimSpace(*body.ActiveJobID)
		if activeJobID == "" || len(activeJobID) > 64 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"code": "bad_request"})
			return
		}
	}
	agent, err := s.systemUpdateAgentForToken(r.Context(), token, body.ServiceID)
	if err != nil {
		writeSystemUpdateAgentError(w, err)
		return
	}
	now := time.Now().UTC()
	if !systemUpdateAgentAvailable(agent, now) {
		writeJSON(w, http.StatusConflict, map[string]string{"code": "updater_offline"})
		return
	}
	hostID := strings.TrimSpace(body.HostID)
	if hostID == "" {
		hostID = agent.ServiceID
	}
	if !validSystemUpdateCapabilityIdentifier(hostID) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "bad_request"})
		return
	}
	s.systemUpdateOperationMu.Lock()
	defer s.systemUpdateOperationMu.Unlock()
	eligibleTargets, err := s.systemUpdateTargetsForAgentHostClaim(r.Context(), agent, hostID, activeJobID != "")
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "resolve_system_update_targets_failed"})
		return
	}
	if len(eligibleTargets) == 0 {
		if activeJobID == "" {
			w.WriteHeader(http.StatusNoContent)
			return
		}
	}
	claim, clearActiveJob, err := s.systemUpdates.ClaimSystemUpdateJob(r.Context(), agent.ServiceID, hostID, activeJobID, eligibleTargets, now, systemUpdateClaimLeaseTTL)
	if err == nil && clearActiveJob {
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, http.StatusOK, map[string]bool{"clear_active_job_id": true})
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
	if errors.Is(err, store.ErrInvalidSystemUpdate) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "bad_request"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "claim_system_update_failed"})
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	s.writeServiceAudit(r, token, "system_updates.claim", "system_update", claim.Job.ID, "success", map[string]any{"agent_service_id": agent.ServiceID, "host_id": hostID, "target_id": claim.Job.TargetID, "target_version": claim.Job.TargetVersion, "lease_generation": claim.LeaseGeneration, "recovery_required": claim.RecoveryRequired, "last_status": claim.LastStatus})
	writeOneTimeSecretJSON(w, http.StatusOK, claim)
}

func (s *Server) serviceSystemUpdateReport(w http.ResponseWriter, r *http.Request) {
	token, ok := s.authenticateService(w, r, "updates.report")
	if !ok {
		return
	}
	var body struct {
		ServiceID       string `json:"service_id"`
		LeaseToken      string `json:"lease_token"`
		LeaseGeneration int64  `json:"lease_generation"`
		Sequence        int64  `json:"sequence"`
		Status          string `json:"status"`
		Progress        int    `json:"progress"`
		Code            string `json:"code"`
		Message         string `json:"message"`
		ArtifactDigest  string `json:"artifact_digest"`
		PreviousDigest  string `json:"previous_digest"`
	}
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "bad_request"})
		return
	}
	agent, err := s.systemUpdateAgentForToken(r.Context(), token, body.ServiceID)
	if err != nil {
		writeSystemUpdateAgentError(w, err)
		return
	}
	job, applied, err := s.systemUpdates.ReportSystemUpdateJob(r.Context(), strings.TrimSpace(r.PathValue("id")), store.SystemUpdateReport{
		AgentServiceID: agent.ServiceID, LeaseToken: body.LeaseToken, LeaseGeneration: body.LeaseGeneration, Sequence: body.Sequence, Status: body.Status,
		Progress: body.Progress, Code: body.Code, Message: body.Message, ArtifactDigest: body.ArtifactDigest, PreviousDigest: body.PreviousDigest,
	}, time.Now().UTC(), systemUpdateExecutionLeaseTTL)
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "system_update_job_not_found"})
		return
	case errors.Is(err, store.ErrSystemUpdateLeaseInvalid):
		writeJSON(w, http.StatusConflict, map[string]string{"code": "system_update_lease_invalid"})
		return
	case errors.Is(err, store.ErrSystemUpdateSequenceStale):
		writeJSON(w, http.StatusConflict, map[string]string{"code": "system_update_sequence_stale"})
		return
	case errors.Is(err, store.ErrSystemUpdateTransition):
		writeJSON(w, http.StatusConflict, map[string]string{"code": "system_update_transition_invalid"})
		return
	case errors.Is(err, store.ErrInvalidSystemUpdate):
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_system_update_report"})
		return
	case err != nil:
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "report_system_update_failed"})
		return
	}
	if applied && systemUpdateStatusTerminal(job.Status) {
		result := "success"
		if job.Status != store.SystemUpdateStatusSucceeded {
			result = "failure"
		}
		s.writeServiceAudit(r, token, "system_updates."+job.Status, "system_update", job.ID, result, map[string]any{"agent_service_id": agent.ServiceID, "target_id": job.TargetID, "target_version": job.TargetVersion, "status": job.Status, "code": job.Code})
	}
	writeJSON(w, http.StatusOK, job)
}

func (s *Server) serviceSystemUpdateAuthorize(w http.ResponseWriter, r *http.Request) {
	token, ok := s.authenticateService(w, r, "updates.authorize")
	if !ok {
		return
	}
	jobID := strings.TrimSpace(r.PathValue("id"))
	w.Header().Set("Cache-Control", "no-store")
	s.writeServiceAudit(r, token, "system_updates.authorize", "system_update", jobID, "failure", map[string]any{"reason": "legacy_endpoint_disabled"})
	writeJSON(w, http.StatusGone, map[string]string{"code": "legacy_system_update_authorization_disabled"})
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

func (s *Server) systemUpdateTargets(ctx context.Context) ([]systemUpdateTargetResponse, error) {
	targets, _, _, err := s.systemUpdateSnapshot(ctx)
	return targets, err
}

func (s *Server) systemUpdateSnapshot(ctx context.Context) ([]systemUpdateTargetResponse, []systemUpdateAgentResponse, []systemUpdateHostResponse, error) {
	services, err := s.services.ListServices(ctx)
	if err != nil {
		return nil, nil, nil, err
	}
	now := time.Now().UTC()
	agents, updaters, hosts := systemUpdateAgentTopology(services, now)
	checks := latestVersions(ctx, append(append([]versionUpdateTarget{}, controlPanelVersionUpdateTarget), append(nodeVersionUpdateTargets, dockerVersionUpdateTarget)...))
	panelBusy, err := s.systemUpdateControlPanelBusy(ctx)
	if err != nil {
		return nil, nil, nil, err
	}
	targets := make([]systemUpdateTargetResponse, 0, len(services)+1)
	targets = append(targets, buildSystemUpdateTarget("control-panel", "control_panel", "Control Panel", version.Current(), "", panelBusy, agents["control-panel"], checks))
	for _, service := range services {
		if service.ServiceType == "update_agent" {
			continue
		}
		currentVersion := strings.TrimSpace(service.ReportedVersion)
		if currentVersion == "" {
			currentVersion = strings.TrimSpace(service.Version)
		}
		busy, err := s.systemUpdateServiceBusy(ctx, service)
		if err != nil {
			return nil, nil, nil, err
		}
		name := strings.TrimSpace(service.ServiceName)
		if name == "" {
			name = service.ServiceID
		}
		targets = append(targets, buildSystemUpdateTarget(service.ServiceID, service.ServiceType, name, currentVersion, service.CurrentStreamID, busy, agents[service.ServiceID], checks))
	}
	sort.Slice(targets, func(i, j int) bool {
		if targets[i].TargetID == "control-panel" {
			return true
		}
		if targets[j].TargetID == "control-panel" {
			return false
		}
		return targets[i].Name < targets[j].Name
	})
	return targets, updaters, hosts, nil
}

func buildSystemUpdateTarget(targetID, serviceType, name, serviceVersion, currentStreamID string, busy bool, assignment systemUpdateAgentAssignment, checks map[string]serviceUpdateInfoResponse) systemUpdateTargetResponse {
	target := systemUpdateTargetResponse{TargetID: targetID, ServiceType: serviceType, Name: name, HostID: assignment.HostID, CurrentVersion: serviceVersion, DeploymentMode: assignment.DeploymentMode, UpdateAgentID: assignment.AgentID, UpdaterOnline: assignment.Available, Busy: busy, CurrentStreamID: currentStreamID}
	checkKey := serviceType
	if targetID == "control-panel" {
		checkKey = "control-panel"
	}
	if assignment.DeploymentMode == "docker" {
		checkKey = "docker"
		if assignment.CurrentVersion != "" {
			target.CurrentVersion = assignment.CurrentVersion
		} else {
			target.CurrentVersion = ""
		}
	}
	check := checks[checkKey]
	target.LatestVersion = strings.TrimSpace(check.LatestVersion)
	target.UpdateCheckSource = check.UpdateCheckSource
	target.UpdateCheckError = check.UpdateCheckError
	versionValid := validSystemUpdateVersion(target.LatestVersion)
	currentVersionValid := validSystemUpdateVersion(target.CurrentVersion)
	if versionValid && currentVersionValid && versionIsNewer(target.LatestVersion, target.CurrentVersion) {
		target.UpdateAvailable = true
	}

	if assignment.AgentID == "" {
		target.BlockedReason = "updater_missing"
		return target
	}
	if !assignment.Available {
		target.BlockedReason = "updater_offline"
		return target
	}
	if assignment.HostReachability == "unreachable" {
		target.BlockedReason = "target_unreachable"
		return target
	}
	if assignment.HostReachability != "reachable" {
		target.BlockedReason = "target_reachability_unknown"
		return target
	}
	if assignment.DeploymentMode != "systemd" && assignment.DeploymentMode != "docker" {
		target.BlockedReason = "unsupported_deployment_mode"
		return target
	}
	if !currentVersionValid {
		target.BlockedReason = "current_version_unknown"
		return target
	}
	if target.LatestVersion == "" {
		target.BlockedReason = "release_manifest_unavailable"
		return target
	}
	if !versionValid {
		target.BlockedReason = "release_version_invalid"
		return target
	}
	if !target.UpdateAvailable {
		target.BlockedReason = "update_not_available"
		return target
	}
	if check.ManifestErrorCode != "" {
		target.BlockedReason = check.ManifestErrorCode
		return target
	}
	if !check.ManifestVerified {
		target.BlockedReason = "manifest_unverified"
		return target
	}
	if check.MinimumAgentVersion != "" && !systemUpdateAgentVersionAtLeast(assignment.AgentVersion, check.MinimumAgentVersion) {
		target.BlockedReason = "updater_version_incompatible"
		return target
	}
	target.Eligible = true
	return target
}

func systemUpdateAgentAssignments(services []store.RegisteredService) map[string]systemUpdateAgentAssignment {
	assignments, _, _ := systemUpdateAgentTopology(services, time.Now().UTC())
	return assignments
}

func systemUpdateAgentTopology(services []store.RegisteredService, now time.Time) (map[string]systemUpdateAgentAssignment, []systemUpdateAgentResponse, []systemUpdateHostResponse) {
	agentServices := make([]store.RegisteredService, 0)
	for _, service := range services {
		if service.ServiceType == "update_agent" {
			agentServices = append(agentServices, service)
		}
	}
	sort.Slice(agentServices, func(i, j int) bool {
		iAvailable := systemUpdateAgentAvailable(agentServices[i], now)
		jAvailable := systemUpdateAgentAvailable(agentServices[j], now)
		if iAvailable != jAvailable {
			return iAvailable
		}
		return agentServices[i].ServiceID < agentServices[j].ServiceID
	})
	assignments := map[string]systemUpdateAgentAssignment{}
	updaters := make([]systemUpdateAgentResponse, 0, len(agentServices))
	hostOwners := map[string]string{}
	hostsByID := map[string]systemUpdateHostResponse{}
	for _, agent := range agentServices {
		agentVersion := systemUpdateAgentVersion(agent)
		updaters = append(updaters, systemUpdateAgentResponse{
			UpdaterID: agent.ServiceID, Name: systemUpdateDisplayName(agent.ServiceName, agent.ServiceID), Status: strings.TrimSpace(agent.Status),
			Online: systemUpdateAgentAvailable(agent, now), Version: agentVersion, LastHeartbeat: agent.LastHeartbeatAt,
		})
		approved := approvedSystemUpdateAgentTargetAssignments(agent, now)
		for _, targetID := range sortedApprovedSystemUpdateTargetIDs(approved) {
			if _, exists := assignments[targetID]; exists {
				continue
			}
			approvedTarget := approved[targetID]
			if owner, exists := hostOwners[approvedTarget.Host.HostID]; exists && owner != agent.ServiceID {
				continue
			}
			hostOwners[approvedTarget.Host.HostID] = agent.ServiceID
			if _, exists := hostsByID[approvedTarget.Host.HostID]; !exists {
				hostsByID[approvedTarget.Host.HostID] = approvedTarget.Host
			}
			assignments[targetID] = systemUpdateAgentAssignment{
				AgentID: agent.ServiceID, AgentVersion: agentVersion, DeploymentMode: approvedTarget.DeploymentMode,
				CurrentVersion: approvedTarget.CurrentVersion, Available: systemUpdateAgentAvailable(agent, now),
				HostID: approvedTarget.Host.HostID, HostName: approvedTarget.Host.Name, HostReachability: approvedTarget.Host.Reachability,
				HostCheckedAt: approvedTarget.Host.CheckedAt, HostCode: approvedTarget.Host.Code,
			}
		}
	}
	sort.Slice(updaters, func(i, j int) bool { return updaters[i].UpdaterID < updaters[j].UpdaterID })
	hosts := make([]systemUpdateHostResponse, 0, len(hostsByID))
	for _, host := range hostsByID {
		hosts = append(hosts, host)
	}
	sort.Slice(hosts, func(i, j int) bool {
		if hosts[i].Name != hosts[j].Name {
			return hosts[i].Name < hosts[j].Name
		}
		return hosts[i].HostID < hosts[j].HostID
	})
	return assignments, updaters, hosts
}

func (s *Server) systemUpdateEligibleTargetsForAgent(ctx context.Context, agent store.RegisteredService) (map[string]string, error) {
	return s.systemUpdateTargetsForAgentClaim(ctx, agent, false)
}

func (s *Server) systemUpdateTargetsForAgentClaim(ctx context.Context, agent store.RegisteredService, allowBusyRecovery bool) (map[string]string, error) {
	return s.systemUpdateTargetsForAgentHostClaim(ctx, agent, "", allowBusyRecovery)
}

func (s *Server) systemUpdateTargetsForAgentHostClaim(ctx context.Context, agent store.RegisteredService, hostID string, allowBusyRecovery bool) (map[string]string, error) {
	approved := approvedSystemUpdateAgentTargetAssignments(agent, time.Now().UTC())
	services, err := s.services.ListServices(ctx)
	if err != nil {
		return nil, err
	}
	byID := make(map[string]store.RegisteredService, len(services))
	for _, service := range services {
		byID[service.ServiceID] = service
	}
	eligible := map[string]string{}
	for _, targetID := range sortedApprovedSystemUpdateTargetIDs(approved) {
		targetApproval := approved[targetID]
		if hostID != "" && targetApproval.Host.HostID != hostID {
			continue
		}
		if !allowBusyRecovery && targetApproval.Host.Reachability != "reachable" {
			continue
		}
		mode := targetApproval.DeploymentMode
		if targetID == "control-panel" {
			busy, err := s.systemUpdateControlPanelBusy(ctx)
			if err != nil {
				return nil, err
			}
			if allowBusyRecovery || !busy {
				eligible[targetID] = mode
			}
			continue
		}
		target, ok := byID[targetID]
		if !ok || target.ServiceType == "update_agent" {
			continue
		}
		busy, err := s.systemUpdateServiceBusy(ctx, target)
		if err != nil {
			return nil, err
		}
		if allowBusyRecovery || !busy {
			eligible[targetID] = mode
		}
	}
	return eligible, nil
}

func (s *Server) systemUpdateServiceBusy(ctx context.Context, service store.RegisteredService) (bool, error) {
	streamID := strings.TrimSpace(service.CurrentStreamID)
	if streamID != "" {
		active, err := s.systemUpdateStreamActive(ctx, streamID)
		if err != nil || active {
			return active, err
		}
	}
	assignments, err := s.services.ListServiceAssignmentsForService(ctx, service.ServiceID)
	if err != nil {
		return false, err
	}
	for _, assignment := range assignments {
		assignmentStreamID := strings.TrimSpace(assignment.StreamID)
		if assignmentStreamID == "" || assignmentStreamID == streamID {
			continue
		}
		active, err := s.systemUpdateStreamActive(ctx, assignmentStreamID)
		if err != nil || active {
			return active, err
		}
	}
	return false, nil
}

func (s *Server) systemUpdateControlPanelBusy(ctx context.Context) (bool, error) {
	if activeStore, ok := s.streams.(store.ActiveStreamStore); ok {
		return activeStore.HasActiveStream(ctx)
	}
	activeStreams, err := s.systemUpdateActiveStreams(ctx)
	return len(activeStreams) > 0, err
}

func (s *Server) systemUpdateStreamActive(ctx context.Context, streamID string) (bool, error) {
	stream, err := s.streams.GetStream(ctx, streamID)
	if errors.Is(err, store.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return isActiveStreamStatus(stream.Status), nil
}

func (s *Server) systemUpdateActiveStreams(ctx context.Context) (map[string]bool, error) {
	streams, err := s.streams.ListStreams(ctx)
	if err != nil {
		return nil, err
	}
	active := make(map[string]bool)
	for _, stream := range streams {
		if isActiveStreamStatus(stream.Status) {
			active[stream.ID] = true
		}
	}
	return active, nil
}

func approvedSystemUpdateAgentTargets(agent store.RegisteredService) (map[string]string, map[string]string) {
	approved := approvedSystemUpdateAgentTargetAssignments(agent, time.Now().UTC())
	modes := make(map[string]string, len(approved))
	versions := make(map[string]string, len(approved))
	for targetID, target := range approved {
		modes[targetID] = target.DeploymentMode
		versions[targetID] = target.CurrentVersion
	}
	return modes, versions
}

type systemUpdateApprovedTarget struct {
	DeploymentMode string
	CurrentVersion string
	Host           systemUpdateHostResponse
}

func approvedSystemUpdateAgentTargetAssignments(agent store.RegisteredService, now time.Time) map[string]systemUpdateApprovedTarget {
	configuredManaged := capabilityStringSlice(agent.Capabilities["managed_targets"])
	configuredModes := capabilityStringMap(agent.Capabilities["deployment_modes"])
	configuredHosts := capabilityStringMap(agent.Capabilities["target_hosts"])
	reportedManaged := capabilityStringSlice(agent.ReportedCapabilities["managed_targets"])
	reportedModes := capabilityStringMap(agent.ReportedCapabilities["deployment_modes"])
	reportedHosts := capabilityStringMap(agent.ReportedCapabilities["target_hosts"])
	reportedVersions := capabilityStringMap(agent.ReportedCapabilities["deployed_versions"])
	if len(configuredHosts) == 0 || len(reportedHosts) == 0 {
		return map[string]systemUpdateApprovedTarget{}
	}
	reportedSet := make(map[string]bool, len(reportedManaged))
	for _, targetID := range reportedManaged {
		reportedSet[targetID] = true
	}
	approved := make(map[string]systemUpdateApprovedTarget)
	for _, targetID := range configuredManaged {
		configuredMode := strings.ToLower(strings.TrimSpace(configuredModes[targetID]))
		reportedMode := strings.ToLower(strings.TrimSpace(reportedModes[targetID]))
		if !reportedSet[targetID] || configuredMode != reportedMode || (configuredMode != "systemd" && configuredMode != "docker") {
			continue
		}
		configuredHost, configured := configuredHosts[targetID]
		reportedHost, reported := reportedHosts[targetID]
		if !configured || !reported || configuredHost != reportedHost || !validSystemUpdateCapabilityIdentifier(configuredHost) {
			continue
		}
		hostID := configuredHost
		if !validSystemUpdateCapabilityIdentifier(hostID) {
			continue
		}
		approved[targetID] = systemUpdateApprovedTarget{
			DeploymentMode: configuredMode,
			CurrentVersion: strings.TrimSpace(reportedVersions[targetID]),
			Host:           systemUpdateHostStatus(agent, hostID, now),
		}
	}
	return approved
}

func systemUpdateHostStatus(agent store.RegisteredService, hostID string, now time.Time) systemUpdateHostResponse {
	host := systemUpdateHostResponse{HostID: hostID, Name: hostID, UpdaterID: agent.ServiceID, Reachability: "unknown"}
	names := capabilityStringMap(agent.ReportedCapabilities["host_names"])
	if name := strings.TrimSpace(names[hostID]); validSystemUpdateHostDisplayName(name) {
		host.Name = name
	}
	checkedValues := capabilityStringMap(agent.ReportedCapabilities["host_checked_at"])
	checkedAt, checked := parseSystemUpdateHostCheckedAt(checkedValues[hostID], now)
	if checkedAt != nil {
		host.CheckedAt = checkedAt
	}
	if !checked {
		return host
	}
	status := strings.ToLower(strings.TrimSpace(capabilityStringMap(agent.ReportedCapabilities["host_statuses"])[hostID]))
	if status != "reachable" && status != "unreachable" {
		return host
	}
	host.Reachability = status
	if status == "unreachable" {
		host.Code = allowedSystemUpdateHostCode(capabilityStringMap(agent.ReportedCapabilities["host_codes"])[hostID])
	}
	return host
}

func parseSystemUpdateHostCheckedAt(raw string, now time.Time) (*time.Time, bool) {
	checkedAt, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(raw))
	if err != nil || checkedAt.After(now.Add(systemUpdateHostClockSkew)) {
		return nil, false
	}
	checkedAt = checkedAt.UTC()
	age := now.Sub(checkedAt)
	if age < 0 {
		age = 0
	}
	return &checkedAt, age <= systemUpdateHostReachabilityTTL
}

func allowedSystemUpdateHostCode(raw string) string {
	code := strings.ToLower(strings.TrimSpace(raw))
	switch code {
	case "ssh_timeout", "ssh_connection_refused", "ssh_auth_failed", "ssh_host_key_mismatch", "remote_helper_unavailable", "remote_config_invalid":
		return code
	default:
		return ""
	}
}

func validSystemUpdateCapabilityIdentifier(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 191 {
		return false
	}
	for _, char := range value {
		if unicode.IsControl(char) {
			return false
		}
	}
	return true
}

func validSystemUpdateHostDisplayName(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 191 {
		return false
	}
	for _, char := range value {
		if unicode.IsControl(char) {
			return false
		}
	}
	return true
}

func systemUpdateDisplayName(name, fallback string) string {
	name = strings.TrimSpace(name)
	if !validSystemUpdateHostDisplayName(name) {
		return fallback
	}
	return name
}

func systemUpdateAgentVersion(agent store.RegisteredService) string {
	if value := strings.TrimSpace(agent.ReportedVersion); value != "" {
		return value
	}
	return strings.TrimSpace(agent.Version)
}

func sortedApprovedSystemUpdateTargetIDs(targets map[string]systemUpdateApprovedTarget) []string {
	ids := make([]string, 0, len(targets))
	for targetID := range targets {
		ids = append(ids, targetID)
	}
	sort.Strings(ids)
	return ids
}

func sortedCapabilityTargetIDs(modes map[string]string) []string {
	ids := make([]string, 0, len(modes))
	for targetID := range modes {
		ids = append(ids, targetID)
	}
	sort.Strings(ids)
	return ids
}

func systemUpdateAgentVersionAtLeast(current, minimum string) bool {
	if !validMinimumUpdateAgentVersion(minimum) || !strings.HasPrefix(strings.TrimSpace(current), "v") {
		return false
	}
	currentParts, currentOK := parseVersionParts(current)
	minimumParts, minimumOK := parseVersionParts(minimum)
	if !currentOK || !minimumOK {
		return false
	}
	for index := range currentParts {
		if currentParts[index] != minimumParts[index] {
			return currentParts[index] > minimumParts[index]
		}
	}
	return !strings.Contains(strings.TrimPrefix(strings.TrimSpace(current), "v"), "-")
}

func (s *Server) activeSystemUpdateForStreamTargets(ctx context.Context, assignments []store.RegisteredService) (store.SystemUpdateJob, bool, error) {
	if s.systemUpdates == nil {
		return store.SystemUpdateJob{}, false, nil
	}
	targetIDs := make([]string, 0, len(assignments)+1)
	targetIDs = append(targetIDs, "control-panel")
	seen := map[string]bool{"control-panel": true}
	for _, assignment := range assignments {
		targetID := strings.TrimSpace(assignment.ServiceID)
		if targetID != "" && !seen[targetID] {
			seen[targetID] = true
			targetIDs = append(targetIDs, targetID)
		}
	}
	for _, targetID := range targetIDs {
		job, err := s.systemUpdates.GetActiveSystemUpdateJob(ctx, targetID)
		if errors.Is(err, store.ErrNotFound) {
			continue
		}
		if err != nil {
			return store.SystemUpdateJob{}, false, err
		}
		if job.Status != store.SystemUpdateStatusQueued {
			return job, true, nil
		}
	}
	return store.SystemUpdateJob{}, false, nil
}

func capabilityStringSlice(value any) []string {
	var raw []string
	switch typed := value.(type) {
	case []string:
		raw = append(raw, typed...)
	case []any:
		for _, item := range typed {
			if text, ok := item.(string); ok {
				raw = append(raw, text)
			}
		}
	}
	seen := map[string]bool{}
	out := make([]string, 0, len(raw))
	for _, item := range raw {
		item = strings.TrimSpace(item)
		if item != "" && len(item) <= 191 && !seen[item] {
			seen[item] = true
			out = append(out, item)
		}
	}
	return out
}

func capabilityStringMap(value any) map[string]string {
	out := map[string]string{}
	switch typed := value.(type) {
	case map[string]string:
		for key, item := range typed {
			out[strings.TrimSpace(key)] = strings.TrimSpace(item)
		}
	case map[string]any:
		for key, item := range typed {
			if text, ok := item.(string); ok {
				out[strings.TrimSpace(key)] = strings.TrimSpace(text)
			}
		}
	}
	return out
}

func systemUpdateAgentAvailable(agent store.RegisteredService, now time.Time) bool {
	switch strings.ToLower(strings.TrimSpace(agent.Status)) {
	case "offline", "disabled", "pending":
		return false
	}
	if agent.LastHeartbeatAt == nil {
		return false
	}
	age := now.Sub(agent.LastHeartbeatAt.UTC())
	return age >= 0 && age <= heartbeatOfflineAfter()
}

func validSystemUpdateVersion(raw string) bool {
	raw = strings.TrimSpace(raw)
	return len(raw) <= 128 && systemUpdateVersionPattern.MatchString(raw)
}

var systemUpdateVersionPattern = regexp.MustCompile(`^v[0-9]+\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z]+(?:[.-][0-9A-Za-z]+)*)?$`)

func systemUpdateStatusTerminal(status string) bool {
	return status == store.SystemUpdateStatusSucceeded || status == store.SystemUpdateStatusRolledBack || status == store.SystemUpdateStatusFailed
}
