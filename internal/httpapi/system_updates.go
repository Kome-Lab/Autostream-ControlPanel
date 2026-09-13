package httpapi

import (
	"errors"
	"github.com/example/autostream-control-panel/internal/store"
	"net/http"
	"regexp"
	"time"
)

const systemUpdateExecutionLeaseTTL = 45 * time.Minute

type systemUpdateTargetResponse struct {
	PortContractVersion     int                              `json:"port_contract_version,omitempty"`
	PortPolicySnapshotID    string                           `json:"port_policy_snapshot_id,omitempty"`
	LocalListenPort         int                              `json:"local_listen_port,omitempty"`
	EndpointRevision        int64                            `json:"endpoint_revision,omitempty"`
	AppliedEndpointRevision int64                            `json:"applied_endpoint_revision,omitempty"`
	AppliedConfigRevision   int64                            `json:"applied_config_revision,omitempty"`
	OwnershipEpoch          int64                            `json:"ownership_epoch,omitempty"`
	PortModes               []string                         `json:"port_modes,omitempty"`
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
	OwnershipEpoch                    *int64            `json:"ownership_epoch,omitempty"`
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
	HostID       string     `json:"host_id"`
	Name         string     `json:"name"`
	UpdaterID    string     `json:"updater_id"`
	Reachability string     `json:"reachability"`
	CheckedAt    *time.Time `json:"reachability_checked_at,omitempty"`
	Code         string     `json:"reachability_code,omitempty"`
}

type systemUpdateAgentAssignment struct {
	AgentID              string
	AgentVersion         string
	AgentTransportMode   string
	DeploymentMode       string
	CurrentVersion       string
	Available            bool
	HostID               string
	HostName             string
	HostReachability     string
	HostCheckedAt        *time.Time
	HostCode             string
	TargetServiceType    string
	LocalListenPortBound bool
	PolicyManaged        bool
	PolicyReady          bool
	PolicyBlockedReason  string
}

func (s *Server) listSystemUpdates(w http.ResponseWriter, r *http.Request) {
	targets, updaters, hosts, err := s.systemUpdateSnapshot(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "list_system_update_targets_failed"})
		return
	}
	s.decorateSystemUpdatePortSnapshots(r, targets)
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
		if errors.Is(err, store.ErrSystemUpdatePortContractRequired) {
			writeJSON(w, http.StatusConflict, map[string]string{"code": "system_update_port_contract_required"})
			return
		}
		code := "bad_request"
		if errors.Is(err, errInvalidSystemUpdatePortMode) {
			code = "invalid_system_update_port_mode"
		}
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
			code := "idempotency_key_conflict"
			if body.Operation == store.SystemUpdateOperationPortReconfigure {
				code = "system_update_port_idempotency_conflict"
			}
			writeJSON(w, http.StatusConflict, map[string]string{"code": code})
			return
		}
		status := http.StatusAccepted
		if body.Operation == store.SystemUpdateOperationPortReconfigure {
			status = http.StatusOK
		}
		writeJSON(w, status, existing)
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

var systemUpdateClaimIdentityPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]*$`)

var (
	systemUpdateRawSHA256Pattern         = regexp.MustCompile(`^[a-f0-9]{64}$`)
	systemUpdateDockerContainerIDPattern = regexp.MustCompile(`^[a-f0-9]{12,64}$`)
)

var systemUpdateVersionPattern = regexp.MustCompile(`^v[0-9]+\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z]+(?:[.-][0-9A-Za-z]+)*)?$`)
