package httpapi

import (
	"errors"
	contracts "github.com/example/autostream-contracts/pkg/contracts"
	"github.com/example/autostream-control-panel/internal/store"
	"net/http"
	"unicode"
)

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
	// The selected policy is resolved transactionally by the coordinator. An
	// invalid local runtime therefore becomes nil here: unrelated policies stay
	// usable, while a policy containing Control Panel fails closed.
	controlPanelTarget, _ := controlPanelPullUpdaterRuntimeTarget()
	job, created, err := coordinator.CreateDockerPortReconfigurationJob(
		r.Context(),
		s.services,
		s.updaterPolicies,
		store.CreateDockerPortReconfigurationJobParams{
			PortContractVersion: body.PortContractVersion, Mode: contracts.SystemUpdatePortMode(body.Mode),
			ExpectedSnapshotID: body.ExpectedSnapshotID, ExpectedDesiredRevision: body.DesiredRevision, ExpectedFence: body.Fence,
			BuildPolicySnapshot:      s.systemUpdatePortSnapshotBuilder(panelBaseURL(r)),
			TargetID:                 body.TargetID,
			NewAdvertisedPort:        body.NewAdvertisedPort,
			NewPublishedPort:         body.NewPublishedPort,
			NewContainerPort:         body.NewContainerPort,
			ExpectedEndpointRevision: body.ExpectedEndpointRevision,
			IdempotencyKey:           body.IdempotencyKey,
			RequestedByUserID:        current.User.ID,
			RequestedByUsername:      current.User.Username,
			ControlPanelTarget:       controlPanelTarget,
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
			addSystemUpdatePortAuditFields(metadata, job.PortReconfigure)
		}
		s.writeAudit(r, store.AuditEvent{
			ActorUserID: current.User.ID, ActorUsername: current.User.Username,
			Action: "system_updates.create", ResourceType: "system_update",
			ResourceID: job.ID, Result: "success", Metadata: metadata,
		})
	}
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	writeJSON(w, status, job)
}

func writeSystemUpdatePortCreateError(w http.ResponseWriter, err error) bool {
	if writeSystemUpdatePortV2Error(w, err) {
		return true
	}
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
	// A malformed local Control Panel endpoint must not block unrelated
	// policies. The coordinator resolves the selected pull_v2 policy and fails
	// closed when that policy actually contains the exact synthetic target.
	controlPanelTarget, _ := controlPanelPullUpdaterRuntimeTarget()
	job, created, err := coordinator.CreateSystemdPortReconfigurationJob(
		r.Context(),
		s.services,
		s.updaterPolicies,
		store.CreateSystemdPortReconfigurationJobParams{
			PortContractVersion: body.PortContractVersion, Mode: contracts.SystemUpdatePortMode(body.Mode), NewLocalListenPort: body.NewLocalListenPort,
			NewAdvertisedPort: body.NewAdvertisedPort, ExpectedSnapshotID: body.ExpectedSnapshotID,
			ExpectedDesiredRevision: body.DesiredRevision, ExpectedFence: body.Fence,
			BuildPolicySnapshot:      s.systemUpdatePortSnapshotBuilder(panelBaseURL(r)),
			TargetID:                 body.TargetID,
			NewPort:                  body.NewPort,
			ExpectedEndpointRevision: body.ExpectedEndpointRevision,
			IdempotencyKey:           body.IdempotencyKey,
			RequestedByUserID:        current.User.ID,
			RequestedByUsername:      current.User.Username,
			ControlPanelTarget:       controlPanelTarget,
		},
	)
	if writeSystemUpdatePortV2Error(w, err) {
		return
	}
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
			"target_id":             job.TargetID,
			"service_type":          job.TargetServiceType,
			"deployment_mode":       job.DeploymentMode,
			"operation":             job.Operation,
			"new_local_listen_port": body.NewLocalListenPort,
			"endpoint_revision":     body.ExpectedEndpointRevision,
			"idempotent_replay":     false,
		}
		if job.PortReconfigure != nil {
			addSystemUpdatePortAuditFields(metadata, job.PortReconfigure)
		}
		s.writeAudit(r, store.AuditEvent{
			ActorUserID: current.User.ID, ActorUsername: current.User.Username,
			Action: "system_updates.create", ResourceType: "system_update", ResourceID: job.ID, Result: "success",
			Metadata: metadata,
		})
	}
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	writeJSON(w, status, job)
}

func containsControlText(value string) bool {
	for _, char := range value {
		if unicode.IsControl(char) {
			return true
		}
	}
	return false
}
