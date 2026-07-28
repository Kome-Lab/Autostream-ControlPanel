package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/example/autostream-control-panel/internal/store"
	"github.com/example/autostream-control-panel/internal/version"
	"golang.org/x/crypto/ssh"
)

const systemUpdateClaimLeaseTTL = 2 * time.Minute
const systemUpdateExecutionLeaseTTL = 45 * time.Minute
const systemUpdateHostReachabilityTTL = 2 * time.Minute
const systemUpdateHostClockSkew = 30 * time.Second

type systemUpdateTargetResponse struct {
	TargetID                string                           `json:"target_id"`
	ServiceType             string                           `json:"target_type"`
	Name                    string                           `json:"name"`
	HostID                  string                           `json:"host_id,omitempty"`
	CurrentVersion          string                           `json:"current_version,omitempty"`
	LatestVersion           string                           `json:"latest_version,omitempty"`
	UpdateAvailable         bool                             `json:"update_available"`
	DeploymentMode          string                           `json:"deployment_mode,omitempty"`
	UpdateAgentID           string                           `json:"updater_id,omitempty"`
	UpdaterOnline           bool                             `json:"updater_online"`
	Eligible                bool                             `json:"eligible"`
	BlockedReason           string                           `json:"blocked_reason,omitempty"`
	EligibleOperations      []string                         `json:"eligible_operations"`
	OperationBlockedReasons map[string]string                `json:"operation_blocked_reasons"`
	Busy                    bool                             `json:"busy"`
	CurrentStreamID         string                           `json:"current_stream_id,omitempty"`
	UpdateCheckSource       string                           `json:"update_check_source,omitempty"`
	UpdateCheckError        string                           `json:"update_check_error,omitempty"`
	PortMapping             *systemUpdatePortMappingResponse `json:"port_mapping,omitempty"`
}

type systemUpdatePortMappingResponse struct {
	Mode            string     `json:"mode"`
	AdvertisedPort  int        `json:"advertised_port,omitempty"`
	PublishedHostIP string     `json:"published_host_ip,omitempty"`
	PublishedPort   int        `json:"published_port,omitempty"`
	ContainerPort   int        `json:"container_port,omitempty"`
	HealthPort      int        `json:"health_port,omitempty"`
	ConfigRevision  int64      `json:"config_revision,omitempty"`
	State           string     `json:"state"`
	ReportedAt      *time.Time `json:"reported_at,omitempty"`
}

type systemUpdateAgentResponse struct {
	UpdaterID                         string            `json:"updater_id"`
	Name                              string            `json:"name"`
	TransportMode                     string            `json:"transport_mode"`
	ExecutionHostID                   string            `json:"execution_host_id,omitempty"`
	OwnershipEpoch                    int64             `json:"ownership_epoch,omitempty"`
	Status                            string            `json:"status"`
	Online                            bool              `json:"online"`
	Version                           string            `json:"version"`
	LastHeartbeat                     *time.Time        `json:"last_heartbeat_at,omitempty"`
	DesiredRevision                   int64             `json:"desired_revision,omitempty"`
	AppliedRevision                   int64             `json:"applied_revision,omitempty"`
	PolicyStatus                      string            `json:"policy_status,omitempty"`
	PolicyErrorCode                   string            `json:"policy_error_code,omitempty"`
	SSHClientPublicKeys               map[string]string `json:"ssh_client_public_keys,omitempty"`
	SSHClientKeyFingerprints          map[string]string `json:"ssh_client_key_fingerprints,omitempty"`
	BootstrapEncryptionPublicKey      string            `json:"bootstrap_encryption_public_key,omitempty"`
	BootstrapEncryptionKeyFingerprint string            `json:"bootstrap_encryption_key_fingerprint,omitempty"`
}

type systemUpdateHostResponse struct {
	HostID                  string     `json:"host_id"`
	Name                    string     `json:"name"`
	UpdaterID               string     `json:"updater_id"`
	Reachability            string     `json:"reachability"`
	CheckedAt               *time.Time `json:"reachability_checked_at,omitempty"`
	Code                    string     `json:"reachability_code,omitempty"`
	SSHClientPublicKey      string     `json:"ssh_client_public_key,omitempty"`
	SSHClientKeyFingerprint string     `json:"ssh_client_key_fingerprint,omitempty"`
}

type systemUpdateAgentAssignment struct {
	AgentID                string
	AgentVersion           string
	AgentTransportMode     string
	DeploymentMode         string
	CurrentVersion         string
	Available              bool
	HostID                 string
	HostName               string
	HostReachability       string
	HostCheckedAt          *time.Time
	HostCode               string
	TargetServiceType      string
	PolicyManaged          bool
	PolicyReady            bool
	PolicyBlockedReason    string
	ReleaseTokenRequired   bool
	ReleaseTokenConfigured bool
}

type systemUpdateClaimResponse struct {
	store.SystemUpdateClaim
	ReleaseToken string `json:"release_token,omitempty"`
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
	body, err := decodeSystemUpdateCreateRequest(r)
	if err != nil {
		code := "bad_request"
		if errors.Is(err, store.ErrInvalidSystemUpdate) {
			code = "invalid_system_update_request"
		}
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": code})
		return
	}
	s.systemUpdateOperationMu.Lock()
	defer s.systemUpdateOperationMu.Unlock()
	current := currentFromContext(r.Context())
	existing, err := s.systemUpdates.GetSystemUpdateJobByIdempotency(r.Context(), current.User.ID, body.IdempotencyKey)
	if err == nil {
		if !sameSystemUpdateCreateRequest(existing, body) {
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
	if body.Operation == store.SystemUpdateOperationPortReconfigure {
		if body.Docker {
			s.createDockerPortReconfiguration(w, r, body, current)
		} else {
			s.createSystemdPortReconfiguration(w, r, body, current)
		}
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
	if errors.Is(err, store.ErrSystemUpdateOwnershipConflict) {
		writeJSON(w, http.StatusConflict, map[string]string{"code": "system_update_ownership_conflict"})
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

type systemUpdateCreateRequest struct {
	Operation                string
	TargetID                 string
	Strategy                 string
	NewPort                  int
	NewAdvertisedPort        int
	NewPublishedPort         int
	NewContainerPort         int
	Docker                   bool
	ExpectedEndpointRevision int64
	IdempotencyKey           string
}

func decodeSystemUpdateCreateRequest(r *http.Request) (systemUpdateCreateRequest, error) {
	var raw struct {
		Operation                json.RawMessage `json:"operation"`
		TargetID                 string          `json:"target_id"`
		Strategy                 json.RawMessage `json:"strategy"`
		NewPort                  json.RawMessage `json:"new_port"`
		NewAdvertisedPort        json.RawMessage `json:"new_advertised_port"`
		NewPublishedPort         json.RawMessage `json:"new_published_port"`
		NewContainerPort         json.RawMessage `json:"new_container_port"`
		ExpectedEndpointRevision json.RawMessage `json:"expected_endpoint_revision"`
		IdempotencyKey           string          `json:"idempotency_key"`
	}
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&raw); err != nil {
		return systemUpdateCreateRequest{}, err
	}
	if !errors.Is(decoder.Decode(&struct{}{}), io.EOF) {
		return systemUpdateCreateRequest{}, store.ErrInvalidSystemUpdate
	}
	request := systemUpdateCreateRequest{
		TargetID:       strings.TrimSpace(raw.TargetID),
		IdempotencyKey: strings.TrimSpace(raw.IdempotencyKey),
	}
	request.Operation = store.SystemUpdateOperationSoftwareUpdate
	if raw.Operation != nil {
		var operation string
		if json.Unmarshal(raw.Operation, &operation) != nil ||
			strings.TrimSpace(operation) == "" {
			return systemUpdateCreateRequest{}, store.ErrInvalidSystemUpdate
		}
		request.Operation = strings.ToLower(strings.TrimSpace(operation))
	}
	if !validSystemUpdateCapabilityIdentifier(request.TargetID) ||
		request.IdempotencyKey == "" ||
		len(request.IdempotencyKey) > 128 ||
		containsControlText(request.IdempotencyKey) {
		return systemUpdateCreateRequest{}, store.ErrInvalidSystemUpdate
	}
	switch request.Operation {
	case store.SystemUpdateOperationSoftwareUpdate:
		if raw.Strategy == nil ||
			raw.NewPort != nil ||
			raw.NewAdvertisedPort != nil ||
			raw.NewPublishedPort != nil ||
			raw.NewContainerPort != nil ||
			raw.ExpectedEndpointRevision != nil ||
			json.Unmarshal(raw.Strategy, &request.Strategy) != nil {
			return systemUpdateCreateRequest{}, store.ErrInvalidSystemUpdate
		}
		request.Strategy = strings.ToLower(strings.TrimSpace(request.Strategy))
		if request.Strategy != store.SystemUpdateStrategyWhenIdle &&
			request.Strategy != store.SystemUpdateStrategyMaintenance {
			return systemUpdateCreateRequest{}, store.ErrInvalidSystemUpdate
		}
	case store.SystemUpdateOperationPortReconfigure:
		if raw.Strategy != nil ||
			raw.ExpectedEndpointRevision == nil ||
			json.Unmarshal(raw.ExpectedEndpointRevision, &request.ExpectedEndpointRevision) != nil ||
			request.ExpectedEndpointRevision < 1 {
			return systemUpdateCreateRequest{}, store.ErrInvalidSystemUpdate
		}
		systemdShape := raw.NewPort != nil &&
			raw.NewAdvertisedPort == nil &&
			raw.NewPublishedPort == nil &&
			raw.NewContainerPort == nil
		dockerShape := raw.NewPort == nil &&
			raw.NewAdvertisedPort != nil &&
			raw.NewPublishedPort != nil &&
			raw.NewContainerPort != nil
		switch {
		case systemdShape:
			if json.Unmarshal(raw.NewPort, &request.NewPort) != nil ||
				request.NewPort < 1024 || request.NewPort > 65535 {
				return systemUpdateCreateRequest{}, store.ErrInvalidSystemUpdate
			}
		case dockerShape:
			request.Docker = true
			if json.Unmarshal(raw.NewAdvertisedPort, &request.NewAdvertisedPort) != nil ||
				json.Unmarshal(raw.NewPublishedPort, &request.NewPublishedPort) != nil ||
				json.Unmarshal(raw.NewContainerPort, &request.NewContainerPort) != nil ||
				request.NewAdvertisedPort < 1 || request.NewAdvertisedPort > 65535 ||
				request.NewPublishedPort < 1024 || request.NewPublishedPort > 65535 ||
				request.NewContainerPort < 1024 || request.NewContainerPort > 65535 {
				return systemUpdateCreateRequest{}, store.ErrInvalidSystemUpdate
			}
		default:
			return systemUpdateCreateRequest{}, store.ErrInvalidSystemUpdate
		}
	default:
		return systemUpdateCreateRequest{}, store.ErrInvalidSystemUpdate
	}
	return request, nil
}

func sameSystemUpdateCreateRequest(job store.SystemUpdateJob, request systemUpdateCreateRequest) bool {
	operation := strings.ToLower(strings.TrimSpace(job.Operation))
	if operation == "" {
		operation = store.SystemUpdateOperationSoftwareUpdate
	}
	if job.TargetID != request.TargetID || operation != request.Operation {
		return false
	}
	if operation == store.SystemUpdateOperationPortReconfigure {
		if job.PortReconfigure == nil ||
			job.PortReconfigure.ExpectedEndpointRevision != request.ExpectedEndpointRevision {
			return false
		}
		if request.Docker {
			return job.DeploymentMode == "docker" &&
				job.PortReconfigure.Docker != nil &&
				job.PortReconfigure.NewPort == request.NewAdvertisedPort &&
				job.PortReconfigure.Docker.NewPublishedPort == request.NewPublishedPort &&
				job.PortReconfigure.Docker.NewContainerPort == request.NewContainerPort
		}
		return job.DeploymentMode != "docker" &&
			job.PortReconfigure.Docker == nil &&
			job.PortReconfigure.NewPort == request.NewPort
	}
	return job.Strategy == request.Strategy
}

func (s *Server) createDockerPortReconfiguration(
	w http.ResponseWriter,
	r *http.Request,
	body systemUpdateCreateRequest,
	current currentUser,
) {
	coordinator, ok := s.systemUpdates.(store.SystemUpdateDockerPortReconfigurationStore)
	if !ok {
		writeJSON(w, http.StatusConflict, map[string]string{"code": "system_update_port_store_mismatch"})
		return
	}
	job, created, err := coordinator.CreateDockerPortReconfigurationJob(
		r.Context(),
		s.services,
		s.updaterPolicies,
		store.CreateDockerPortReconfigurationJobParams{
			TargetID:                 body.TargetID,
			NewAdvertisedPort:        body.NewAdvertisedPort,
			NewPublishedPort:         body.NewPublishedPort,
			NewContainerPort:         body.NewContainerPort,
			ExpectedEndpointRevision: body.ExpectedEndpointRevision,
			IdempotencyKey:           body.IdempotencyKey,
			RequestedByUserID:        current.User.ID,
			RequestedByUsername:      current.User.Username,
		},
	)
	if writeSystemUpdatePortCreateError(w, err) {
		return
	}
	if created {
		metadata := map[string]any{
			"target_id":           job.TargetID,
			"service_type":        job.TargetServiceType,
			"deployment_mode":     job.DeploymentMode,
			"operation":           job.Operation,
			"new_advertised_port": body.NewAdvertisedPort,
			"new_published_port":  body.NewPublishedPort,
			"new_container_port":  body.NewContainerPort,
			"endpoint_revision":   body.ExpectedEndpointRevision,
			"idempotent_replay":   false,
		}
		if job.PortReconfigure != nil {
			metadata["old_advertised_port"] = job.PortReconfigure.OldPort
			metadata["target_endpoint_revision"] = job.PortReconfigure.TargetEndpointRevision
			if job.PortReconfigure.Docker != nil {
				metadata["old_published_port"] = job.PortReconfigure.Docker.OldPublishedPort
				metadata["old_container_port"] = job.PortReconfigure.Docker.OldContainerPort
			}
		}
		s.writeAudit(r, store.AuditEvent{
			ActorUserID: current.User.ID, ActorUsername: current.User.Username,
			Action: "system_updates.create", ResourceType: "system_update",
			ResourceID: job.ID, Result: "success", Metadata: metadata,
		})
	}
	writeJSON(w, http.StatusAccepted, job)
}

func writeSystemUpdatePortCreateError(w http.ResponseWriter, err error) bool {
	switch {
	case err == nil:
		return false
	case errors.Is(err, store.ErrInvalidSystemUpdate):
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_system_update_request"})
	case errors.Is(err, store.ErrNotFound):
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "system_update_target_not_found"})
	case errors.Is(err, store.ErrAlreadyExists):
		writeJSON(w, http.StatusConflict, map[string]string{"code": "idempotency_key_conflict"})
	case errors.Is(err, store.ErrSystemUpdateTargetActive):
		writeJSON(w, http.StatusConflict, map[string]string{"code": "system_update_target_active"})
	case errors.Is(err, store.ErrSystemUpdateEndpointStale):
		writeJSON(w, http.StatusConflict, map[string]string{"code": "system_update_endpoint_revision_conflict"})
	case errors.Is(err, store.ErrServicePortReserved):
		writeJSON(w, http.StatusConflict, map[string]string{"code": "service_port_reserved"})
	case errors.Is(err, store.ErrSystemUpdatePortStoreMismatch),
		errors.Is(err, store.ErrSystemUpdatePortCoordinatorRequired):
		writeJSON(w, http.StatusConflict, map[string]string{"code": "system_update_port_store_mismatch"})
	case errors.Is(err, store.ErrSystemUpdateOwnershipConflict):
		writeJSON(w, http.StatusConflict, map[string]string{"code": "system_update_ownership_conflict"})
	case errors.Is(err, store.ErrSystemUpdatePortUnsupported),
		errors.Is(err, store.ErrSystemUpdateAgentInactive),
		errors.Is(err, store.ErrSystemUpdateAgentNotReady),
		errors.Is(err, store.ErrConflict):
		writeJSON(w, http.StatusConflict, map[string]string{"code": "system_update_port_reconfigure_not_ready"})
	default:
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "create_system_update_failed"})
	}
	return true
}

func (s *Server) createSystemdPortReconfiguration(
	w http.ResponseWriter,
	r *http.Request,
	body systemUpdateCreateRequest,
	current currentUser,
) {
	coordinator, ok := s.systemUpdates.(store.SystemUpdatePortReconfigurationStore)
	if !ok {
		writeJSON(w, http.StatusConflict, map[string]string{"code": "system_update_port_store_mismatch"})
		return
	}
	job, created, err := coordinator.CreateSystemdPortReconfigurationJob(
		r.Context(),
		s.services,
		s.updaterPolicies,
		store.CreateSystemdPortReconfigurationJobParams{
			TargetID:                 body.TargetID,
			NewPort:                  body.NewPort,
			ExpectedEndpointRevision: body.ExpectedEndpointRevision,
			IdempotencyKey:           body.IdempotencyKey,
			RequestedByUserID:        current.User.ID,
			RequestedByUsername:      current.User.Username,
		},
	)
	switch {
	case errors.Is(err, store.ErrInvalidSystemUpdate):
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_system_update_request"})
		return
	case errors.Is(err, store.ErrNotFound):
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "system_update_target_not_found"})
		return
	case errors.Is(err, store.ErrAlreadyExists):
		writeJSON(w, http.StatusConflict, map[string]string{"code": "idempotency_key_conflict"})
		return
	case errors.Is(err, store.ErrSystemUpdateTargetActive):
		writeJSON(w, http.StatusConflict, map[string]string{"code": "system_update_target_active"})
		return
	case errors.Is(err, store.ErrSystemUpdateEndpointStale):
		writeJSON(w, http.StatusConflict, map[string]string{"code": "system_update_endpoint_revision_conflict"})
		return
	case errors.Is(err, store.ErrServicePortReserved):
		writeJSON(w, http.StatusConflict, map[string]string{"code": "service_port_reserved"})
		return
	case errors.Is(err, store.ErrSystemUpdatePortStoreMismatch),
		errors.Is(err, store.ErrSystemUpdatePortCoordinatorRequired):
		writeJSON(w, http.StatusConflict, map[string]string{"code": "system_update_port_store_mismatch"})
		return
	case errors.Is(err, store.ErrSystemUpdateOwnershipConflict):
		writeJSON(w, http.StatusConflict, map[string]string{"code": "system_update_ownership_conflict"})
		return
	case errors.Is(err, store.ErrSystemUpdatePortUnsupported),
		errors.Is(err, store.ErrSystemUpdateAgentInactive),
		errors.Is(err, store.ErrSystemUpdateAgentNotReady),
		errors.Is(err, store.ErrConflict):
		writeJSON(w, http.StatusConflict, map[string]string{"code": "system_update_port_reconfigure_not_ready"})
		return
	case err != nil:
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "create_system_update_failed"})
		return
	}
	if created {
		metadata := map[string]any{
			"target_id":         job.TargetID,
			"service_type":      job.TargetServiceType,
			"deployment_mode":   job.DeploymentMode,
			"operation":         job.Operation,
			"new_port":          body.NewPort,
			"endpoint_revision": body.ExpectedEndpointRevision,
			"idempotent_replay": false,
		}
		if job.PortReconfigure != nil {
			metadata["old_port"] = job.PortReconfigure.OldPort
			metadata["target_endpoint_revision"] = job.PortReconfigure.TargetEndpointRevision
		}
		s.writeAudit(r, store.AuditEvent{
			ActorUserID: current.User.ID, ActorUsername: current.User.Username,
			Action: "system_updates.create", ResourceType: "system_update", ResourceID: job.ID, Result: "success",
			Metadata: metadata,
		})
	}
	writeJSON(w, http.StatusAccepted, job)
}

func containsControlText(value string) bool {
	for _, char := range value {
		if unicode.IsControl(char) {
			return true
		}
	}
	return false
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
	s.systemUpdateOperationMu.Lock()
	defer s.systemUpdateOperationMu.Unlock()
	token, ok = s.reauthenticateService(w, r, token, "updates.claim")
	if !ok {
		return
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
	hostID, err := s.systemUpdateClaimHost(r.Context(), agent, body.HostID)
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
	if activeJobID != "" {
		clearActiveJob, err := s.systemUpdates.ShouldClearSystemUpdateActiveJob(r.Context(), agent.ServiceID, activeJobID)
		if errors.Is(err, store.ErrInvalidSystemUpdate) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"code": "bad_request"})
			return
		}
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "inspect_system_update_active_job_failed"})
			return
		}
		if clearActiveJob {
			w.Header().Set("Cache-Control", "no-store")
			writeJSON(w, http.StatusOK, map[string]bool{"clear_active_job_id": true})
			return
		}
	}
	releaseToken := ""
	if systemUpdateAgentRequiresReleaseToken(agent) {
		_, policyErr := s.updaterPolicies.GetUpdaterPolicy(r.Context(), agent.ServiceID)
		switch {
		case policyErr == nil:
			releaseToken, err = s.updaterPolicies.GetUpdaterReleaseTokenValue(r.Context())
			if errors.Is(err, store.ErrNotFound) {
				w.Header().Set("Cache-Control", "no-store")
				writeJSON(w, http.StatusConflict, map[string]string{"code": "updater_release_token_not_configured"})
				return
			}
			if err != nil {
				w.Header().Set("Cache-Control", "no-store")
				writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "get_updater_release_token_failed"})
				return
			}
		case errors.Is(policyErr, store.ErrNotFound):
		default:
			writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "get_updater_policy_failed"})
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
	writeOneTimeSecretJSON(w, http.StatusOK, systemUpdateClaimResponse{SystemUpdateClaim: claim, ReleaseToken: releaseToken})
}

func (s *Server) serviceSystemUpdateReport(w http.ResponseWriter, r *http.Request) {
	token, ok := s.authenticateService(w, r, "updates.report")
	if !ok {
		return
	}
	var body struct {
		ServiceID       string                                 `json:"service_id"`
		LeaseToken      string                                 `json:"lease_token"`
		LeaseGeneration int64                                  `json:"lease_generation"`
		Sequence        int64                                  `json:"sequence"`
		Status          string                                 `json:"status"`
		Progress        int                                    `json:"progress"`
		Code            string                                 `json:"code"`
		Message         string                                 `json:"message"`
		ArtifactDigest  string                                 `json:"artifact_digest"`
		PreviousDigest  string                                 `json:"previous_digest"`
		PortReconfigure *store.SystemUpdatePortReconfiguration `json:"port_reconfigure"`
	}
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&body); err != nil ||
		!errors.Is(decoder.Decode(&struct{}{}), io.EOF) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "bad_request"})
		return
	}
	s.systemUpdateOperationMu.Lock()
	defer s.systemUpdateOperationMu.Unlock()
	token, ok = s.reauthenticateService(w, r, token, "updates.report")
	if !ok {
		return
	}
	agent, err := s.systemUpdateAgentForToken(r.Context(), token, body.ServiceID)
	if err != nil {
		writeSystemUpdateAgentError(w, err)
		return
	}
	if err := s.validatePullSystemUpdateAgentOwnership(r.Context(), agent); err != nil {
		if errors.Is(err, store.ErrSystemUpdateOwnershipConflict) {
			s.writeServiceAudit(r, token, "system_updates.report", "system_update", strings.TrimSpace(r.PathValue("id")), "failure", map[string]any{
				"reason":            "ownership_conflict",
				"agent_service_id":  agent.ServiceID,
				"execution_host_id": strings.TrimSpace(agent.ExecutionHostID),
				"ownership_epoch":   agent.OwnershipEpoch,
			})
			writeJSON(w, http.StatusConflict, map[string]string{"code": "system_update_ownership_conflict"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "resolve_system_update_ownership_failed"})
		return
	}
	job, applied, err := s.systemUpdates.ReportSystemUpdateJob(r.Context(), strings.TrimSpace(r.PathValue("id")), store.SystemUpdateReport{
		AgentServiceID: agent.ServiceID, ExecutionHostID: pullSystemUpdateExecutionHost(agent),
		LeaseToken: body.LeaseToken, LeaseGeneration: body.LeaseGeneration, Sequence: body.Sequence, Status: body.Status,
		Progress: body.Progress, Code: body.Code, Message: body.Message, ArtifactDigest: body.ArtifactDigest, PreviousDigest: body.PreviousDigest,
		PortReconfigure: body.PortReconfigure,
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
	case errors.Is(err, store.ErrSystemUpdateOwnershipConflict):
		s.writeServiceAudit(r, token, "system_updates.report", "system_update", strings.TrimSpace(r.PathValue("id")), "failure", map[string]any{
			"reason":            "ownership_conflict",
			"agent_service_id":  agent.ServiceID,
			"execution_host_id": strings.TrimSpace(agent.ExecutionHostID),
			"ownership_epoch":   agent.OwnershipEpoch,
		})
		writeJSON(w, http.StatusConflict, map[string]string{"code": "system_update_ownership_conflict"})
		return
	case errors.Is(err, store.ErrSystemUpdateEndpointStale), errors.Is(err, store.ErrConflict):
		writeJSON(w, http.StatusConflict, map[string]string{"code": "system_update_endpoint_revision_conflict"})
		return
	case errors.Is(err, store.ErrServicePortReserved):
		writeJSON(w, http.StatusConflict, map[string]string{"code": "service_port_reserved"})
		return
	case errors.Is(err, store.ErrSystemUpdatePortStoreMismatch),
		errors.Is(err, store.ErrSystemUpdatePortCoordinatorRequired):
		writeJSON(w, http.StatusConflict, map[string]string{"code": "system_update_port_store_mismatch"})
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
		metadata := map[string]any{
			"agent_service_id": agent.ServiceID,
			"target_id":        job.TargetID,
			"target_version":   job.TargetVersion,
			"operation":        job.Operation,
			"status":           job.Status,
			"code":             job.Code,
		}
		if job.Operation == store.SystemUpdateOperationPortReconfigure && job.PortReconfigure != nil {
			metadata["old_port"] = job.PortReconfigure.OldPort
			metadata["new_port"] = job.PortReconfigure.NewPort
			metadata["port_result"] = job.PortReconfigure.Result
		}
		s.writeServiceAudit(r, token, "system_updates."+job.Status, "system_update", job.ID, result, metadata)
	}
	writeJSON(w, http.StatusOK, job)
}

func (s *Server) serviceSystemUpdateAuthorize(w http.ResponseWriter, r *http.Request) {
	token, ok := s.authenticateService(w, r, "updates.authorize")
	if !ok {
		return
	}
	s.systemUpdateOperationMu.Lock()
	defer s.systemUpdateOperationMu.Unlock()
	token, ok = s.reauthenticateService(w, r, token, "updates.authorize")
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

func (s *Server) systemUpdateClaimHost(ctx context.Context, agent store.RegisteredService, requestedHostID string) (string, error) {
	transportMode := strings.ToLower(strings.TrimSpace(agent.TransportMode))
	if transportMode == "" {
		transportMode = store.SystemUpdateTransportSSHV1
	}
	switch transportMode {
	case store.SystemUpdateTransportPullV2:
		if err := s.validatePullSystemUpdateAgentOwnership(ctx, agent); err != nil {
			return "", err
		}
		return strings.TrimSpace(agent.ExecutionHostID), nil
	case store.SystemUpdateTransportSSHV1:
		hostID := strings.TrimSpace(requestedHostID)
		if hostID == "" {
			hostID = agent.ServiceID
		}
		if !validSystemUpdateCapabilityIdentifier(hostID) {
			return "", store.ErrInvalidSystemUpdate
		}
		return hostID, nil
	default:
		return "", store.ErrInvalidSystemUpdate
	}
}

func (s *Server) validatePullSystemUpdateAgentOwnership(ctx context.Context, agent store.RegisteredService) error {
	transportMode := strings.ToLower(strings.TrimSpace(agent.TransportMode))
	if transportMode == "" || transportMode == store.SystemUpdateTransportSSHV1 {
		return nil
	}
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

func systemUpdateAgentRequiresReleaseToken(agent store.RegisteredService) bool {
	return systemUpdateAgentTransportMode(agent) == store.SystemUpdateTransportSSHV1
}

func systemUpdateAgentTransportMode(agent store.RegisteredService) string {
	transportMode := strings.ToLower(strings.TrimSpace(agent.TransportMode))
	if transportMode == "" {
		return store.SystemUpdateTransportSSHV1
	}
	return transportMode
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
	servicesByID := make(map[string]store.RegisteredService, len(services))
	for _, service := range services {
		servicesByID[service.ServiceID] = service
	}
	policyItems, err := s.updaterPolicies.ListUpdaterPolicies(ctx)
	if err != nil {
		return nil, nil, nil, err
	}
	policies := make(map[string]store.UpdaterPolicy, len(policyItems))
	for _, policy := range policyItems {
		policies[policy.UpdaterID] = policy
	}
	releaseTokenConfigured := false
	releaseTokenRequired := false
	for _, service := range services {
		if _, managed := policies[service.ServiceID]; managed &&
			service.ServiceType == "update_agent" &&
			systemUpdateAgentRequiresReleaseToken(service) {
			releaseTokenRequired = true
			break
		}
	}
	if releaseTokenRequired {
		releaseTokenStatus, err := s.updaterReleaseTokenStatus(ctx)
		if err != nil {
			return nil, nil, nil, err
		}
		releaseTokenConfigured = releaseTokenStatus.Configured
	}
	now := time.Now().UTC()
	agents, updaters, hosts := systemUpdateAgentTopologyWithPolicies(services, now, policies, releaseTokenConfigured)
	checks := latestVersions(ctx, append(append([]versionUpdateTarget{}, controlPanelVersionUpdateTarget), append(nodeVersionUpdateTargets, dockerVersionUpdateTarget)...))
	panelBusy, err := s.systemUpdateControlPanelBusy(ctx)
	if err != nil {
		return nil, nil, nil, err
	}
	targets := make([]systemUpdateTargetResponse, 0, len(services)+1)
	controlPanelAssignment := agents["control-panel"]
	controlPanelTarget := buildSystemUpdateTarget("control-panel", "control_panel", "Control Panel", version.Current(), "", panelBusy, controlPanelAssignment, checks)
	decorateSystemUpdateTargetOperations(&controlPanelTarget, nil, controlPanelAssignment)
	targets = append(targets, controlPanelTarget)
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
		assignment := agents[service.ServiceID]
		target := buildSystemUpdateTarget(service.ServiceID, service.ServiceType, name, currentVersion, service.CurrentStreamID, busy, assignment, checks)
		if assignment.DeploymentMode == "docker" {
			target.PortMapping = systemUpdateDockerPortMappingSnapshot(
				service,
				servicesByID[assignment.AgentID],
				policies[assignment.AgentID],
				assignment,
			)
		}
		decorateSystemUpdateTargetOperations(&target, &service, assignment)
		targets = append(targets, target)
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

func decorateSystemUpdateTargetOperations(
	target *systemUpdateTargetResponse,
	service *store.RegisteredService,
	assignment systemUpdateAgentAssignment,
) {
	if target == nil {
		return
	}
	target.EligibleOperations = make([]string, 0, 2)
	target.OperationBlockedReasons = make(map[string]string, 2)
	if target.Eligible {
		target.EligibleOperations = append(target.EligibleOperations, store.SystemUpdateOperationSoftwareUpdate)
	} else {
		reason := strings.TrimSpace(target.BlockedReason)
		if reason == "" {
			reason = "system_update_target_unavailable"
		}
		target.OperationBlockedReasons[store.SystemUpdateOperationSoftwareUpdate] = reason
	}
	if reason := systemUpdatePortReconfigureBlockedReason(*target, service, assignment); reason != "" {
		target.OperationBlockedReasons[store.SystemUpdateOperationPortReconfigure] = reason
	} else {
		target.EligibleOperations = append(target.EligibleOperations, store.SystemUpdateOperationPortReconfigure)
	}
}

func systemUpdatePortReconfigureBlockedReason(
	target systemUpdateTargetResponse,
	service *store.RegisteredService,
	assignment systemUpdateAgentAssignment,
) string {
	if service == nil ||
		service.ServiceID != target.TargetID ||
		!supportedSystemUpdatePortServiceType(service.ServiceType) {
		return "system_update_port_reconfigure_not_ready"
	}
	if assignment.AgentID == "" {
		return "updater_missing"
	}
	if target.Busy || strings.TrimSpace(target.CurrentStreamID) != "" {
		return "system_update_target_busy"
	}
	if (assignment.DeploymentMode != "systemd" &&
		assignment.DeploymentMode != "docker") ||
		assignment.AgentTransportMode != store.SystemUpdateTransportPullV2 ||
		!assignment.PolicyManaged {
		return "system_update_port_reconfigure_not_ready"
	}
	if assignment.PolicyManaged && !assignment.PolicyReady {
		if reason := strings.TrimSpace(assignment.PolicyBlockedReason); reason != "" {
			return reason
		}
		return "updater_policy_pending"
	}
	if assignment.TargetServiceType != "" && assignment.TargetServiceType != service.ServiceType {
		return "updater_policy_target_type_mismatch"
	}
	if !assignment.Available {
		return "updater_offline"
	}
	if assignment.HostReachability == "unreachable" {
		return "target_unreachable"
	}
	if assignment.HostReachability != "reachable" {
		return "target_reachability_unknown"
	}
	minAdvertisedPort := 1024
	if assignment.DeploymentMode == "docker" {
		minAdvertisedPort = 1
		if target.PortMapping == nil ||
			target.PortMapping.Mode != "docker" ||
			target.PortMapping.State != "applied" {
			return "system_update_port_reconfigure_not_ready"
		}
	}
	if service.EndpointStatus != "applied" ||
		service.EndpointRevision < 1 ||
		service.AppliedConfigRevision < 1 ||
		!validUpdateManifestDigest(service.AppliedConfigSHA256) ||
		service.AppliedEndpoint == nil ||
		service.DesiredEndpoint == nil ||
		!sameSystemUpdateServiceEndpoint(service.AppliedEndpoint, service.DesiredEndpoint) ||
		service.AppliedEndpoint.Port < minAdvertisedPort ||
		service.AppliedEndpoint.Port > 65535 {
		return "system_update_endpoint_revision_conflict"
	}
	return ""
}

func systemUpdateDockerPortMappingSnapshot(
	service store.RegisteredService,
	agent store.RegisteredService,
	policy store.UpdaterPolicy,
	assignment systemUpdateAgentAssignment,
) *systemUpdatePortMappingResponse {
	unavailable := &systemUpdatePortMappingResponse{Mode: "docker", State: "unavailable"}
	if assignment.DeploymentMode != "docker" ||
		assignment.AgentTransportMode != store.SystemUpdateTransportPullV2 ||
		assignment.AgentID == "" ||
		agent.ServiceID != assignment.AgentID ||
		agent.ServiceType != "update_agent" ||
		!assignment.PolicyManaged ||
		!assignment.Available ||
		policy.UpdaterID != assignment.AgentID ||
		policy.LocalExecutorPolicyRevision < 1 ||
		!validUpdateManifestDigest(policy.LocalExecutorPolicySHA256) ||
		!validSystemUpdateCapabilityIdentifier(service.ServiceID) ||
		!supportedSystemUpdatePortServiceType(service.ServiceType) ||
		service.AppliedEndpoint == nil ||
		service.AppliedEndpoint.Port < 1 ||
		service.AppliedEndpoint.Port > 65535 ||
		service.AppliedConfigRevision < 1 ||
		!validUpdateManifestDigest(service.AppliedConfigSHA256) ||
		agent.LastHeartbeatAt == nil ||
		agent.LastHeartbeatAt.IsZero() {
		return unavailable
	}
	serviceID := service.ServiceID
	capabilities := agent.ReportedCapabilities
	availability := capabilityStringMap(capabilities["target_availability"])
	availabilityCodes := capabilityStringMap(capabilities["target_availability_codes"])
	reportedServiceTypes := capabilityStringMap(capabilities["reported_service_types"])
	reportedDeploymentModes := capabilityStringMap(capabilities["reported_deployment_modes"])
	reportedPolicyRevisions := capabilityInt64Map(capabilities["reported_executor_policy_revisions"])
	reportedPolicyDigests := capabilityStringMap(capabilities["reported_executor_policy_sha256"])
	reportedConfigRevisions := capabilityInt64Map(capabilities["reported_config_revisions"])
	reportedConfigDigests := capabilityStringMap(capabilities["reported_config_sha256"])
	reportedAdvertisedPorts := capabilityInt64Map(capabilities["reported_ports"])
	reportedPortDrift := capabilityBoolMap(capabilities["port_drift"])
	versions := capabilityStringMap(capabilities["reported_docker_port_capabilities"])
	publishedPorts := capabilityInt64Map(capabilities["reported_docker_published_ports"])
	containerPorts := capabilityInt64Map(capabilities["reported_docker_container_ports"])
	healthPorts := capabilityInt64Map(capabilities["reported_docker_health_ports"])
	composeDigests := capabilityStringMap(capabilities["reported_docker_compose_sha256"])
	composeRevisions := capabilityInt64Map(capabilities["reported_docker_compose_revisions"])
	versionEnvDigests := capabilityStringMap(capabilities["reported_docker_version_env_sha256"])
	containerIDs := capabilityStringMap(capabilities["reported_docker_container_ids"])
	imageIDs := capabilityStringMap(capabilities["reported_docker_image_ids"])
	repositoryDigests := capabilityStringMap(capabilities["reported_docker_repository_digests"])

	reportedAvailability, availabilityOK := availability[serviceID]
	reportedAvailabilityCode, availabilityCodeOK := availabilityCodes[serviceID]
	reportedServiceType, serviceTypeOK := reportedServiceTypes[serviceID]
	reportedDeploymentMode, deploymentModeOK := reportedDeploymentModes[serviceID]
	reportedPolicyRevision, policyRevisionOK := reportedPolicyRevisions[serviceID]
	reportedPolicyDigest, policyDigestOK := reportedPolicyDigests[serviceID]
	reportedConfigRevision, configRevisionOK := reportedConfigRevisions[serviceID]
	reportedConfigDigest, configDigestOK := reportedConfigDigests[serviceID]
	reportedAdvertisedPort, advertisedPortOK := reportedAdvertisedPorts[serviceID]
	portDrift, portDriftOK := reportedPortDrift[serviceID]
	version, versionOK := versions[serviceID]
	publishedPort, publishedPortOK := publishedPorts[serviceID]
	containerPort, containerPortOK := containerPorts[serviceID]
	healthPort, healthPortOK := healthPorts[serviceID]
	composeDigest, composeDigestOK := composeDigests[serviceID]
	composeRevision, composeRevisionOK := composeRevisions[serviceID]
	versionEnvDigest, versionEnvDigestOK := versionEnvDigests[serviceID]
	containerID, containerIDOK := containerIDs[serviceID]
	imageID, imageIDOK := imageIDs[serviceID]
	repositoryDigest, repositoryDigestOK := repositoryDigests[serviceID]
	containerID = strings.TrimSpace(containerID)

	if !availabilityOK || reportedAvailability != "available" ||
		!availabilityCodeOK || reportedAvailabilityCode != "executor_verified" ||
		!serviceTypeOK || !supportedSystemUpdatePortServiceType(reportedServiceType) ||
		!deploymentModeOK ||
		(reportedDeploymentMode != "systemd" && reportedDeploymentMode != "docker") ||
		!policyRevisionOK || reportedPolicyRevision < 1 ||
		!policyDigestOK || !validUpdateManifestDigest(reportedPolicyDigest) ||
		!configRevisionOK || reportedConfigRevision < 1 ||
		!configDigestOK || !validUpdateManifestDigest(reportedConfigDigest) ||
		!advertisedPortOK || reportedAdvertisedPort < 1 || reportedAdvertisedPort > 65535 ||
		!portDriftOK ||
		!versionOK || version != "v1" ||
		!publishedPortOK ||
		publishedPort < 1024 || publishedPort > 65535 ||
		!containerPortOK ||
		containerPort < 1024 || containerPort > 65535 ||
		!healthPortOK ||
		healthPort < 1024 || healthPort > 65535 ||
		!composeDigestOK || !systemUpdateRawSHA256Pattern.MatchString(composeDigest) ||
		!composeRevisionOK || composeRevision < 1 ||
		!versionEnvDigestOK || !validUpdateManifestDigest(versionEnvDigest) ||
		!containerIDOK ||
		!systemUpdateDockerContainerIDPattern.MatchString(containerID) ||
		!imageIDOK || !validUpdateManifestDigest(imageID) ||
		!repositoryDigestOK || !validUpdateManifestDigest(repositoryDigest) {
		return unavailable
	}
	reportedConfigSHA256, err := store.SystemUpdateDockerPortConfigSHA256(
		reportedServiceType,
		int(publishedPort),
		int(containerPort),
		reportedConfigRevision,
	)
	if err != nil || reportedConfigSHA256 != reportedConfigDigest {
		return unavailable
	}
	reportedAt := agent.LastHeartbeatAt.UTC()
	mapping := &systemUpdatePortMappingResponse{
		Mode: "docker", AdvertisedPort: int(reportedAdvertisedPort),
		PublishedHostIP: "127.0.0.1", PublishedPort: int(publishedPort),
		ContainerPort: int(containerPort), HealthPort: int(healthPort),
		ConfigRevision: reportedConfigRevision,
		State:          "drifted", ReportedAt: &reportedAt,
	}
	if !portDrift &&
		reportedServiceType == service.ServiceType &&
		reportedDeploymentMode == "docker" &&
		reportedPolicyRevision == policy.LocalExecutorPolicyRevision &&
		reportedPolicyDigest == policy.LocalExecutorPolicySHA256 &&
		reportedConfigRevision == service.AppliedConfigRevision &&
		reportedConfigDigest == service.AppliedConfigSHA256 &&
		reportedAdvertisedPort == int64(service.AppliedEndpoint.Port) &&
		healthPort == publishedPort &&
		composeRevision == policy.LocalExecutorPolicyRevision {
		mapping.State = "applied"
	}
	return mapping
}

var (
	systemUpdateRawSHA256Pattern         = regexp.MustCompile(`^[a-f0-9]{64}$`)
	systemUpdateDockerContainerIDPattern = regexp.MustCompile(`^[a-f0-9]{12,64}$`)
)

func supportedSystemUpdatePortServiceType(serviceType string) bool {
	switch strings.TrimSpace(serviceType) {
	case "worker", "encoder_recorder", "discord_bot", "observability":
		return true
	default:
		return false
	}
}

func sameSystemUpdateServiceEndpoint(left, right *store.ServiceEndpoint) bool {
	if left == nil || right == nil {
		return left == right
	}
	return *left == *right
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
	if assignment.PolicyManaged && !assignment.PolicyReady {
		target.BlockedReason = assignment.PolicyBlockedReason
		if target.BlockedReason == "" {
			target.BlockedReason = "updater_policy_pending"
		}
		return target
	}
	if assignment.TargetServiceType != "" && assignment.TargetServiceType != serviceType {
		target.BlockedReason = "updater_policy_target_type_mismatch"
		return target
	}
	if assignment.ReleaseTokenRequired && !assignment.ReleaseTokenConfigured {
		target.BlockedReason = "updater_release_token_not_configured"
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
	return systemUpdateAgentTopologyWithPolicies(services, now, nil, false)
}

func systemUpdateAgentTopologyWithPolicies(services []store.RegisteredService, now time.Time, policies map[string]store.UpdaterPolicy, releaseTokenConfigured bool) (map[string]systemUpdateAgentAssignment, []systemUpdateAgentResponse, []systemUpdateHostResponse) {
	agentServices := make([]store.RegisteredService, 0)
	servicesByID := make(map[string]store.RegisteredService, len(services))
	for _, service := range services {
		servicesByID[service.ServiceID] = service
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
		reportedHosts := newSystemUpdateReportedHostSnapshot(agent)
		transportMode := strings.ToLower(strings.TrimSpace(agent.TransportMode))
		if transportMode == "" {
			transportMode = store.SystemUpdateTransportSSHV1
		}
		updater := systemUpdateAgentResponse{
			UpdaterID: agent.ServiceID, Name: systemUpdateDisplayName(agent.ServiceName, agent.ServiceID), Status: strings.TrimSpace(agent.Status),
			TransportMode: transportMode,
			Online:        systemUpdateAgentAvailable(agent, now), Version: agentVersion, LastHeartbeat: agent.LastHeartbeatAt,
		}
		if transportMode == store.SystemUpdateTransportPullV2 {
			updater.ExecutionHostID = strings.TrimSpace(agent.ExecutionHostID)
			updater.OwnershipEpoch = agent.OwnershipEpoch
		}
		updater.BootstrapEncryptionPublicKey, updater.BootstrapEncryptionKeyFingerprint = updateHostBootstrapEncryptionIdentity(agent)
		var managedPolicy *store.UpdaterPolicy
		if policy, ok := policies[agent.ServiceID]; ok {
			managedPolicy = &policy
			updater.DesiredRevision = policy.ProjectionRevision
			updater.AppliedRevision, updater.PolicyStatus, updater.PolicyErrorCode = systemUpdateManagedPolicyReport(agent)
			managedHostIDs := make(map[string]bool, len(policy.Hosts))
			for _, host := range policy.Hosts {
				managedHostIDs[host.HostID] = true
			}
			updater.SSHClientPublicKeys, updater.SSHClientKeyFingerprints = systemUpdateSSHClientKeys(reportedHosts, managedHostIDs)
		}
		updaters = append(updaters, updater)
		approved := approvedSystemUpdateAgentTargetAssignmentsForPolicyWithHosts(agent, now, managedPolicy, reportedHosts, servicesByID)
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
				AgentID: agent.ServiceID, AgentVersion: agentVersion, AgentTransportMode: transportMode, DeploymentMode: approvedTarget.DeploymentMode,
				CurrentVersion: approvedTarget.CurrentVersion, Available: systemUpdateAgentAvailable(agent, now),
				HostID: approvedTarget.Host.HostID, HostName: approvedTarget.Host.Name, HostReachability: approvedTarget.Host.Reachability,
				HostCheckedAt: approvedTarget.Host.CheckedAt, HostCode: approvedTarget.Host.Code,
				TargetServiceType: approvedTarget.ServiceType, PolicyManaged: approvedTarget.PolicyManaged,
				PolicyReady: approvedTarget.PolicyReady, PolicyBlockedReason: approvedTarget.PolicyBlockedReason,
				ReleaseTokenRequired:   approvedTarget.PolicyManaged && systemUpdateAgentRequiresReleaseToken(agent),
				ReleaseTokenConfigured: releaseTokenConfigured,
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
	var managedPolicy *store.UpdaterPolicy
	policy, err := s.updaterPolicies.GetUpdaterPolicy(ctx, agent.ServiceID)
	switch {
	case err == nil:
		managedPolicy = &policy
	case errors.Is(err, store.ErrNotFound):
	default:
		return nil, err
	}
	services, err := s.services.ListServices(ctx)
	if err != nil {
		return nil, err
	}
	byID := make(map[string]store.RegisteredService, len(services))
	for _, service := range services {
		byID[service.ServiceID] = service
	}
	var approved map[string]systemUpdateApprovedTarget
	if allowBusyRecovery &&
		managedPolicy != nil &&
		systemUpdateAgentTransportMode(agent) != store.SystemUpdateTransportPullV2 {
		approved = reportedSystemUpdateAgentTargetAssignments(agent, time.Now().UTC())
	} else {
		approved = approvedSystemUpdateAgentTargetAssignmentsForPolicyWithHosts(
			agent,
			time.Now().UTC(),
			managedPolicy,
			newSystemUpdateReportedHostSnapshot(agent),
			byID,
		)
	}
	eligible := map[string]string{}
	for _, targetID := range sortedApprovedSystemUpdateTargetIDs(approved) {
		targetApproval := approved[targetID]
		if targetApproval.PolicyManaged && !targetApproval.PolicyReady {
			continue
		}
		if hostID != "" && targetApproval.Host.HostID != hostID {
			continue
		}
		if !allowBusyRecovery && targetApproval.Host.Reachability != "reachable" {
			continue
		}
		mode := targetApproval.DeploymentMode
		if targetID == "control-panel" {
			if targetApproval.ServiceType != "" && targetApproval.ServiceType != "control_panel" {
				continue
			}
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
		if !ok || target.ServiceType == "update_agent" || (targetApproval.ServiceType != "" && targetApproval.ServiceType != target.ServiceType) {
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
	DeploymentMode      string
	CurrentVersion      string
	ServiceType         string
	Host                systemUpdateHostResponse
	PolicyManaged       bool
	PolicyReady         bool
	PolicyBlockedReason string
}

func approvedSystemUpdateAgentTargetAssignments(agent store.RegisteredService, now time.Time) map[string]systemUpdateApprovedTarget {
	return approvedSystemUpdateAgentTargetAssignmentsForPolicy(agent, now, nil)
}

func approvedSystemUpdateAgentTargetAssignmentsForPolicy(agent store.RegisteredService, now time.Time, policy *store.UpdaterPolicy) map[string]systemUpdateApprovedTarget {
	return approvedSystemUpdateAgentTargetAssignmentsForPolicyWithHosts(agent, now, policy, newSystemUpdateReportedHostSnapshot(agent), nil)
}

func approvedSystemUpdateAgentTargetAssignmentsForPolicyWithHosts(
	agent store.RegisteredService,
	now time.Time,
	policy *store.UpdaterPolicy,
	reportedHosts systemUpdateReportedHostSnapshot,
	servicesByID map[string]store.RegisteredService,
) map[string]systemUpdateApprovedTarget {
	if policy != nil {
		if systemUpdateAgentTransportMode(agent) == store.SystemUpdateTransportPullV2 {
			return approvedPullSystemUpdateAgentTargetAssignments(agent, now, *policy, servicesByID)
		}
		return approvedManagedSystemUpdateAgentTargetAssignments(agent, now, *policy, reportedHosts)
	}
	return approvedLegacySystemUpdateAgentTargetAssignments(agent, now, reportedHosts)
}

func approvedLegacySystemUpdateAgentTargetAssignments(agent store.RegisteredService, now time.Time, reportedHostStatus systemUpdateReportedHostSnapshot) map[string]systemUpdateApprovedTarget {
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
			Host:           reportedHostStatus.status(hostID, now),
		}
	}
	return approved
}

func approvedManagedSystemUpdateAgentTargetAssignments(agent store.RegisteredService, now time.Time, policy store.UpdaterPolicy, reportedHostStatus systemUpdateReportedHostSnapshot) map[string]systemUpdateApprovedTarget {
	appliedRevision, policyStatus, _ := systemUpdateManagedPolicyReport(agent)
	policyApplied := appliedRevision == policy.ProjectionRevision && policyStatus == "applied"
	reportedManaged := capabilityStringSlice(agent.ReportedCapabilities["managed_targets"])
	reportedModes := capabilityStringMap(agent.ReportedCapabilities["deployment_modes"])
	reportedHosts := capabilityStringMap(agent.ReportedCapabilities["target_hosts"])
	reportedVersions := capabilityStringMap(agent.ReportedCapabilities["deployed_versions"])
	reportedSet := make(map[string]bool, len(reportedManaged))
	for _, targetID := range reportedManaged {
		reportedSet[targetID] = true
	}
	hosts := make(map[string]store.UpdaterPolicyHost, len(policy.Hosts))
	for _, host := range policy.Hosts {
		hosts[host.HostID] = host
	}
	approved := make(map[string]systemUpdateApprovedTarget, len(policy.Targets))
	for _, target := range policy.Targets {
		hostPolicy, ok := hosts[target.HostID]
		if !ok {
			continue
		}
		host := reportedHostStatus.status(target.HostID, now)
		host.Name = hostPolicy.Name
		entry := systemUpdateApprovedTarget{
			DeploymentMode: target.DeploymentMode, CurrentVersion: strings.TrimSpace(reportedVersions[target.TargetID]),
			ServiceType: target.ServiceType, Host: host, PolicyManaged: true,
		}
		switch {
		case policyStatus == "failed":
			entry.PolicyBlockedReason = "updater_policy_failed"
		case !policyApplied:
			entry.PolicyBlockedReason = "updater_policy_pending"
		case !reportedSet[target.TargetID] ||
			strings.ToLower(strings.TrimSpace(reportedModes[target.TargetID])) != target.DeploymentMode ||
			strings.TrimSpace(reportedHosts[target.TargetID]) != target.HostID:
			entry.PolicyBlockedReason = "updater_policy_mismatch"
		default:
			entry.PolicyReady = true
		}
		approved[target.TargetID] = entry
	}
	return approved
}

func approvedPullSystemUpdateAgentTargetAssignments(
	agent store.RegisteredService,
	now time.Time,
	policy store.UpdaterPolicy,
	servicesByID map[string]store.RegisteredService,
) map[string]systemUpdateApprovedTarget {
	if agent.OwnershipEpoch < 1 {
		return map[string]systemUpdateApprovedTarget{}
	}
	appliedRevision, policyStatus, _ := systemUpdateManagedPolicyReport(agent)
	policyApplied := appliedRevision == policy.ProjectionRevision && policyStatus == "applied"
	availability := capabilityStringMap(agent.ReportedCapabilities["target_availability"])
	availabilityCodes := capabilityStringMap(agent.ReportedCapabilities["target_availability_codes"])
	reportedPorts := capabilityInt64Map(agent.ReportedCapabilities["reported_ports"])
	reportedServiceTypes := capabilityStringMap(agent.ReportedCapabilities["reported_service_types"])
	reportedDeploymentModes := capabilityStringMap(agent.ReportedCapabilities["reported_deployment_modes"])
	reportedPolicyRevisions := capabilityInt64Map(agent.ReportedCapabilities["reported_executor_policy_revisions"])
	reportedPolicyDigests := capabilityStringMap(agent.ReportedCapabilities["reported_executor_policy_sha256"])
	reportedConfigRevisions := capabilityInt64Map(agent.ReportedCapabilities["reported_config_revisions"])
	reportedConfigDigests := capabilityStringMap(agent.ReportedCapabilities["reported_config_sha256"])
	reportedPortDrift := capabilityBoolMap(agent.ReportedCapabilities["port_drift"])

	executionHostID := strings.TrimSpace(policy.ExecutionHostID)
	reportedOwnershipEpoch, reportedOwnershipEpochOK := capabilityInt64(agent.ReportedCapabilities["ownership_epoch"])
	agentOnline := strings.EqualFold(strings.TrimSpace(agent.Status), "online") &&
		systemUpdateAgentAvailable(agent, now)
	mutationReady := capabilityBool(agent.ReportedCapabilities["host_agent"]) &&
		!capabilityBool(agent.ReportedCapabilities["observe_only"]) &&
		capabilityBool(agent.ReportedCapabilities["update_executor"]) &&
		capabilityBool(agent.ReportedCapabilities["mutation_enabled"]) &&
		capabilityString(agent.ReportedCapabilities["agent_protocol_version"]) == "2"
	bindingReady := policy.TransportMode == store.SystemUpdateTransportPullV2 &&
		executionHostID != "" &&
		executionHostID == strings.TrimSpace(agent.ExecutionHostID) &&
		agent.OwnershipEpoch > 0 &&
		capabilityString(agent.ReportedCapabilities["transport_mode"]) == store.SystemUpdateTransportPullV2 &&
		capabilityString(agent.ReportedCapabilities["execution_host_id"]) == executionHostID &&
		reportedOwnershipEpochOK &&
		reportedOwnershipEpoch == agent.OwnershipEpoch &&
		policy.ProjectionRevision > 0 &&
		policy.LocalExecutorPolicyRevision > 0 &&
		validUpdateManifestDigest(policy.LocalExecutorPolicySHA256)
	approved := make(map[string]systemUpdateApprovedTarget, len(policy.Targets))
	for _, target := range policy.Targets {
		serviceID := strings.TrimSpace(target.ServiceID)
		if !validSystemUpdateCapabilityIdentifier(serviceID) {
			continue
		}
		service, serviceExists := servicesByID[serviceID]
		expectedServiceType := strings.TrimSpace(target.ServiceType)
		expectedDeploymentMode := strings.ToLower(strings.TrimSpace(target.DeploymentMode))
		expectedConfigRevision := int64(1)
		expectedConfigSHA256 := ""
		expectedPort := 0
		if serviceExists {
			if service.AppliedConfigRevision > 0 {
				expectedConfigRevision = service.AppliedConfigRevision
			}
			expectedConfigSHA256 = service.AppliedConfigSHA256
			if service.AppliedEndpoint != nil && service.AppliedEndpoint.Port > 0 {
				expectedPort = service.AppliedEndpoint.Port
			}
		}
		host := systemUpdateHostResponse{
			HostID: executionHostID, Name: executionHostID, UpdaterID: agent.ServiceID, Reachability: "unknown",
		}
		if agent.LastHeartbeatAt != nil {
			checkedAt := agent.LastHeartbeatAt.UTC()
			host.CheckedAt = &checkedAt
		}
		portDrift, portDriftReported := reportedPortDrift[serviceID]
		validExpectedPort := expectedPort >= 1024 && expectedPort <= 65535
		if expectedDeploymentMode == "docker" {
			validExpectedPort = expectedPort >= 1 && expectedPort <= 65535
		}
		executorVerified := availability[serviceID] == "available" &&
			availabilityCodes[serviceID] == "executor_verified" &&
			reportedServiceTypes[serviceID] == expectedServiceType &&
			strings.ToLower(reportedDeploymentModes[serviceID]) == expectedDeploymentMode &&
			reportedPolicyRevisions[serviceID] == policy.LocalExecutorPolicyRevision &&
			reportedPolicyDigests[serviceID] == policy.LocalExecutorPolicySHA256 &&
			reportedConfigRevisions[serviceID] == expectedConfigRevision &&
			(expectedConfigSHA256 == "" || reportedConfigDigests[serviceID] == expectedConfigSHA256) &&
			portDriftReported &&
			!portDrift &&
			validExpectedPort &&
			reportedPorts[serviceID] == int64(expectedPort)
		if agentOnline {
			switch {
			case executorVerified:
				host.Reachability = "reachable"
			case availability[serviceID] == "unavailable":
				host.Reachability = "unreachable"
			}
		}
		entry := systemUpdateApprovedTarget{
			DeploymentMode: expectedDeploymentMode,
			ServiceType:    expectedServiceType,
			Host:           host,
			PolicyManaged:  true,
		}
		if serviceExists {
			entry.CurrentVersion = strings.TrimSpace(service.ReportedVersion)
			if entry.CurrentVersion == "" {
				entry.CurrentVersion = strings.TrimSpace(service.Version)
			}
		}
		switch {
		case policyStatus == "failed":
			entry.PolicyBlockedReason = "updater_policy_failed"
		case !policyApplied:
			entry.PolicyBlockedReason = "updater_policy_pending"
		case !bindingReady ||
			!mutationReady ||
			!agentOnline ||
			strings.TrimSpace(target.HostID) != executionHostID ||
			!serviceExists ||
			service.ServiceType == "update_agent" ||
			service.ServiceType != expectedServiceType ||
			(expectedDeploymentMode != "systemd" && expectedDeploymentMode != "docker") ||
			!executorVerified:
			entry.PolicyBlockedReason = "updater_policy_mismatch"
		default:
			entry.PolicyReady = true
		}
		approved[serviceID] = entry
	}
	return approved
}

func reportedSystemUpdateAgentTargetAssignments(agent store.RegisteredService, now time.Time) map[string]systemUpdateApprovedTarget {
	reportedManaged := capabilityStringSlice(agent.ReportedCapabilities["managed_targets"])
	reportedModes := capabilityStringMap(agent.ReportedCapabilities["deployment_modes"])
	reportedHosts := capabilityStringMap(agent.ReportedCapabilities["target_hosts"])
	reportedVersions := capabilityStringMap(agent.ReportedCapabilities["deployed_versions"])
	reportedHostStatus := newSystemUpdateReportedHostSnapshot(agent)
	approved := make(map[string]systemUpdateApprovedTarget, len(reportedManaged))
	for _, targetID := range reportedManaged {
		if !validSystemUpdateCapabilityIdentifier(targetID) {
			continue
		}
		mode := strings.ToLower(strings.TrimSpace(reportedModes[targetID]))
		if mode != "systemd" && mode != "docker" {
			continue
		}
		hostID := strings.TrimSpace(reportedHosts[targetID])
		if !validSystemUpdateCapabilityIdentifier(hostID) {
			continue
		}
		approved[targetID] = systemUpdateApprovedTarget{
			DeploymentMode: mode, CurrentVersion: strings.TrimSpace(reportedVersions[targetID]),
			Host: reportedHostStatus.status(hostID, now), PolicyManaged: true, PolicyReady: true,
		}
	}
	return approved
}

type systemUpdateReportedHostSnapshot struct {
	updaterID                string
	names                    map[string]string
	sshClientPublicKeys      map[string]string
	sshClientKeyFingerprints map[string]string
	checkedAt                map[string]string
	statuses                 map[string]string
	codes                    map[string]string
}

func newSystemUpdateReportedHostSnapshot(agent store.RegisteredService) systemUpdateReportedHostSnapshot {
	rawClientKeys := capabilityStringMap(agent.ReportedCapabilities["ssh_client_public_keys"])
	clientKeys := make(map[string]string, len(rawClientKeys))
	clientFingerprints := make(map[string]string, len(rawClientKeys))
	for hostID, raw := range rawClientKeys {
		if !validSystemUpdateCapabilityIdentifier(hostID) {
			continue
		}
		key, err := parseUpdaterED25519PublicKey(raw)
		if err != nil {
			continue
		}
		clientKeys[hostID] = strings.TrimSpace(string(ssh.MarshalAuthorizedKey(key)))
		clientFingerprints[hostID] = ssh.FingerprintSHA256(key)
	}
	return systemUpdateReportedHostSnapshot{
		updaterID:                agent.ServiceID,
		names:                    capabilityStringMap(agent.ReportedCapabilities["host_names"]),
		sshClientPublicKeys:      clientKeys,
		sshClientKeyFingerprints: clientFingerprints,
		checkedAt:                capabilityStringMap(agent.ReportedCapabilities["host_checked_at"]),
		statuses:                 capabilityStringMap(agent.ReportedCapabilities["host_statuses"]),
		codes:                    capabilityStringMap(agent.ReportedCapabilities["host_codes"]),
	}
}

func (snapshot systemUpdateReportedHostSnapshot) status(hostID string, now time.Time) systemUpdateHostResponse {
	host := systemUpdateHostResponse{HostID: hostID, Name: hostID, UpdaterID: snapshot.updaterID, Reachability: "unknown"}
	if name := strings.TrimSpace(snapshot.names[hostID]); validSystemUpdateHostDisplayName(name) {
		host.Name = name
	}
	host.SSHClientPublicKey = snapshot.sshClientPublicKeys[hostID]
	host.SSHClientKeyFingerprint = snapshot.sshClientKeyFingerprints[hostID]
	checkedAt, checked := parseSystemUpdateHostCheckedAt(snapshot.checkedAt[hostID], now)
	if checkedAt != nil {
		host.CheckedAt = checkedAt
	}
	if !checked {
		return host
	}
	status := strings.ToLower(strings.TrimSpace(snapshot.statuses[hostID]))
	if status != "reachable" && status != "unreachable" {
		return host
	}
	host.Reachability = status
	if status == "unreachable" {
		host.Code = allowedSystemUpdateHostCode(snapshot.codes[hostID])
	}
	return host
}

func systemUpdateManagedPolicyReport(agent store.RegisteredService) (int64, string, string) {
	revision, _ := capabilityInt64(agent.ReportedCapabilities["policy_revision"])
	status := strings.ToLower(strings.TrimSpace(capabilityString(agent.ReportedCapabilities["policy_status"])))
	switch status {
	case "applied", "pending", "failed":
	default:
		status = "pending"
	}
	errorCode := strings.ToLower(strings.TrimSpace(capabilityString(agent.ReportedCapabilities["policy_error_code"])))
	if !validSystemUpdatePolicyErrorCode(errorCode) ||
		(status != "failed" && !(status == "pending" && errorCode == "active_job_pending")) {
		errorCode = ""
	}
	return revision, status, errorCode
}

func systemUpdateSSHClientKeys(reported systemUpdateReportedHostSnapshot, allowedHostIDs map[string]bool) (map[string]string, map[string]string) {
	keys := make(map[string]string, len(allowedHostIDs))
	fingerprints := make(map[string]string, len(allowedHostIDs))
	for hostID, key := range reported.sshClientPublicKeys {
		if !allowedHostIDs[hostID] || !validSystemUpdateCapabilityIdentifier(hostID) {
			continue
		}
		keys[hostID] = key
		fingerprints[hostID] = reported.sshClientKeyFingerprints[hostID]
	}
	return keys, fingerprints
}

func capabilityInt64(value any) (int64, bool) {
	switch typed := value.(type) {
	case int:
		if typed >= 0 {
			return int64(typed), true
		}
	case int64:
		if typed >= 0 {
			return typed, true
		}
	case float64:
		integer := int64(typed)
		if typed >= 0 && float64(integer) == typed {
			return integer, true
		}
	case json.Number:
		integer, err := typed.Int64()
		if err == nil && integer >= 0 {
			return integer, true
		}
	case string:
		integer, err := strconv.ParseInt(strings.TrimSpace(typed), 10, 64)
		if err == nil && integer >= 0 {
			return integer, true
		}
	}
	return 0, false
}

func capabilityString(value any) string {
	text, _ := value.(string)
	return text
}

func validSystemUpdatePolicyErrorCode(value string) bool {
	switch value {
	case "", "policy_fetch_failed", "policy_invalid", "ssh_identity_failed", "ssh_connectivity_failed", "policy_snapshot_failed", "coordinator_start_failed", "active_job_pending":
		return true
	default:
		return false
	}
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

func capabilityInt64Map(value any) map[string]int64 {
	out := map[string]int64{}
	switch typed := value.(type) {
	case map[string]int:
		for key, item := range typed {
			if item >= 0 {
				out[strings.TrimSpace(key)] = int64(item)
			}
		}
	case map[string]int64:
		for key, item := range typed {
			if item >= 0 {
				out[strings.TrimSpace(key)] = item
			}
		}
	case map[string]any:
		for key, item := range typed {
			if parsed, ok := capabilityInt64(item); ok {
				out[strings.TrimSpace(key)] = parsed
			}
		}
	}
	return out
}

func capabilityBool(value any) bool {
	parsed, _ := value.(bool)
	return parsed
}

func capabilityBoolMap(value any) map[string]bool {
	out := map[string]bool{}
	switch typed := value.(type) {
	case map[string]bool:
		for key, item := range typed {
			out[strings.TrimSpace(key)] = item
		}
	case map[string]any:
		for key, item := range typed {
			if parsed, ok := item.(bool); ok {
				out[strings.TrimSpace(key)] = parsed
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
	heartbeatAt := agent.LastHeartbeatAt.UTC()
	age := now.Sub(heartbeatAt)
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
