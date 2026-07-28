package httpapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/example/autostream-control-panel/internal/security"
	"github.com/example/autostream-control-panel/internal/store"
)

const maxHostAgentControlRequestBytes = 16 * 1024

type runtimeTokenRotationResponse struct {
	ID                                  string     `json:"id"`
	ServiceID                           string     `json:"service_id"`
	ExecutionHostID                     string     `json:"execution_host_id"`
	Status                              string     `json:"status"`
	Revision                            int64      `json:"revision"`
	ExpectedOwnershipEpoch              int64      `json:"expected_ownership_epoch"`
	ExpectedSourcePolicyRevision        int64      `json:"expected_source_policy_revision"`
	ExpectedProjectionRevision          int64      `json:"expected_projection_revision"`
	ExpectedLocalExecutorPolicyRevision int64      `json:"expected_local_executor_policy_revision"`
	PreviousTokenID                     string     `json:"previous_token_id"`
	StagedTokenID                       string     `json:"staged_token_id"`
	LocalStageReceiptID                 string     `json:"local_stage_receipt_id,omitempty"`
	CredentialClaimedAt                 *time.Time `json:"credential_claimed_at,omitempty"`
	LocalStageAcknowledgedAt            *time.Time `json:"local_stage_acknowledged_at,omitempty"`
	LocalStagedAt                       *time.Time `json:"local_staged_at,omitempty"`
	HeartbeatProvedAt                   *time.Time `json:"heartbeat_proved_at,omitempty"`
	ActivatedAt                         *time.Time `json:"activated_at,omitempty"`
	CancelRequestedAt                   *time.Time `json:"cancel_requested_at,omitempty"`
	CanceledAt                          *time.Time `json:"canceled_at,omitempty"`
	EmergencyRevokedTokenID             string     `json:"emergency_revoked_token_id,omitempty"`
	EmergencyRevokedAt                  *time.Time `json:"emergency_revoked_at,omitempty"`
	CreatedAt                           time.Time  `json:"created_at"`
	UpdatedAt                           time.Time  `json:"updated_at"`
}

type runtimeTokenRotationMutationResponse struct {
	Rotation runtimeTokenRotationResponse `json:"rotation"`
	Applied  bool                         `json:"applied"`
	Code     string                       `json:"code,omitempty"`
}

type runtimeTokenRotationCredentialResponse struct {
	TokenID      string `json:"token_id"`
	RuntimeToken string `json:"runtime_token"`
}

type runtimeTokenRotationClaimResponse struct {
	Rotation   runtimeTokenRotationResponse           `json:"rotation"`
	Credential runtimeTokenRotationCredentialResponse `json:"credential"`
	Claimed    bool                                   `json:"claimed"`
}

func publicRuntimeTokenRotation(
	rotation store.SystemUpdateRuntimeTokenRotation,
) runtimeTokenRotationResponse {
	return runtimeTokenRotationResponse{
		ID:                                  rotation.ID,
		ServiceID:                           rotation.ServiceID,
		ExecutionHostID:                     rotation.ExecutionHostID,
		Status:                              rotation.Status,
		Revision:                            rotation.Revision,
		ExpectedOwnershipEpoch:              rotation.ExpectedOwnershipEpoch,
		ExpectedSourcePolicyRevision:        rotation.ExpectedSourcePolicyRevision,
		ExpectedProjectionRevision:          rotation.ExpectedProjectionRevision,
		ExpectedLocalExecutorPolicyRevision: rotation.ExpectedLocalExecutorPolicyRevision,
		PreviousTokenID:                     rotation.PreviousTokenID,
		StagedTokenID:                       rotation.StagedTokenID,
		LocalStageReceiptID:                 rotation.LocalStageReceiptID,
		CredentialClaimedAt:                 rotation.CredentialClaimedAt,
		LocalStageAcknowledgedAt:            rotation.LocalStageAcknowledgedAt,
		LocalStagedAt:                       rotation.LocalStagedAt,
		HeartbeatProvedAt:                   rotation.HeartbeatProvedAt,
		ActivatedAt:                         rotation.ActivatedAt,
		CancelRequestedAt:                   rotation.CancelRequestedAt,
		CanceledAt:                          rotation.CanceledAt,
		EmergencyRevokedTokenID:             rotation.EmergencyRevokedTokenID,
		EmergencyRevokedAt:                  rotation.EmergencyRevokedAt,
		CreatedAt:                           rotation.CreatedAt,
		UpdatedAt:                           rotation.UpdatedAt,
	}
}

func (s *Server) stageRuntimeTokenRotation(w http.ResponseWriter, r *http.Request) {
	setRuntimeTokenRotationNoStore(w)
	if !runtimeTokenRotationAdminAllowed(w, r) {
		return
	}
	var body struct {
		IdempotencyKey string `json:"idempotency_key"`
	}
	if !decodeHostAgentControlJSON(w, r, &body) {
		return
	}
	body.IdempotencyKey = strings.TrimSpace(body.IdempotencyKey)
	if body.IdempotencyKey == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_runtime_token_rotation"})
		return
	}
	serviceID := strings.TrimSpace(r.PathValue("id"))
	rotationStore, ok := s.runtimeTokenRotationStore(w)
	if !ok {
		return
	}
	seal, err := nodeRuntimeTokenSealer()
	if err != nil {
		s.writeRuntimeTokenRotationAdminAudit(
			r, "nodes.runtime_token_rotation.stage", serviceID, "failure",
			map[string]any{"reason": "runtime_token_encryption_unavailable"},
		)
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"code": "runtime_token_rotation_encryption_unavailable"})
		return
	}

	s.systemUpdateOperationMu.Lock()
	defer s.systemUpdateOperationMu.Unlock()
	service, policy, ownership, err := s.runtimeTokenRotationStageBinding(r, serviceID)
	if err != nil {
		status, code := runtimeTokenRotationHTTPError(err, false)
		s.writeRuntimeTokenRotationAdminAudit(
			r, "nodes.runtime_token_rotation.stage", serviceID, "failure",
			map[string]any{"reason": code},
		)
		writeJSON(w, status, map[string]string{"code": code})
		return
	}
	result, err := rotationStore.StageSystemUpdateRuntimeTokenRotation(
		r.Context(),
		s.services,
		s.updaterPolicies,
		store.StageSystemUpdateRuntimeTokenRotationParams{
			ServiceID:                           service.ServiceID,
			ExecutionHostID:                     service.ExecutionHostID,
			IdempotencyKey:                      body.IdempotencyKey,
			ExpectedOwnershipEpoch:              ownership.OwnershipEpoch,
			ExpectedSourcePolicyRevision:        policy.Revision,
			ExpectedProjectionRevision:          policy.ProjectionRevision,
			ExpectedLocalExecutorPolicyRevision: policy.LocalExecutorPolicyRevision,
			Now:                                 time.Now().UTC(),
		},
		seal,
	)
	if err != nil {
		status, code := runtimeTokenRotationHTTPError(err, false)
		s.writeRuntimeTokenRotationAdminAudit(
			r, "nodes.runtime_token_rotation.stage", serviceID, "failure",
			map[string]any{
				"execution_host_id": service.ExecutionHostID,
				"reason":            code,
			},
		)
		writeJSON(w, status, map[string]string{"code": code})
		return
	}
	s.writeRuntimeTokenRotationAdminAudit(
		r, "nodes.runtime_token_rotation.stage", serviceID, "success",
		runtimeTokenRotationAuditMetadata(result.Rotation, map[string]any{"created": result.Created}),
	)
	status := http.StatusOK
	if result.Created {
		status = http.StatusCreated
	}
	writeJSON(w, status, struct {
		Rotation runtimeTokenRotationResponse `json:"rotation"`
		Created  bool                         `json:"created"`
	}{
		Rotation: publicRuntimeTokenRotation(result.Rotation),
		Created:  result.Created,
	})
}

func (s *Server) getActiveRuntimeTokenRotation(w http.ResponseWriter, r *http.Request) {
	setRuntimeTokenRotationNoStore(w)
	serviceID := strings.TrimSpace(r.PathValue("id"))
	service, err := s.services.GetService(r.Context(), serviceID)
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "update_agent_not_registered"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "get_runtime_token_rotation_agent_failed"})
		return
	}
	if service.ServiceType != "update_agent" ||
		service.TransportMode != store.SystemUpdateTransportPullV2 ||
		strings.TrimSpace(service.ExecutionHostID) == "" {
		writeJSON(w, http.StatusConflict, map[string]string{"code": "host_agent_transport_mismatch"})
		return
	}
	rotationStore, ok := s.runtimeTokenRotationStore(w)
	if !ok {
		return
	}
	rotation, err := rotationStore.GetActiveSystemUpdateRuntimeTokenRotationByExecutionHost(
		r.Context(), service.ExecutionHostID,
	)
	if errors.Is(err, store.ErrNotFound) {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if err != nil {
		status, code := runtimeTokenRotationHTTPError(err, false)
		writeJSON(w, status, map[string]string{"code": code})
		return
	}
	if rotation.ServiceID != service.ServiceID ||
		rotation.ExecutionHostID != service.ExecutionHostID {
		writeJSON(w, http.StatusConflict, map[string]string{"code": "runtime_token_rotation_binding_conflict"})
		return
	}
	writeJSON(w, http.StatusOK, struct {
		Rotation runtimeTokenRotationResponse `json:"rotation"`
	}{Rotation: publicRuntimeTokenRotation(rotation)})
}

func (s *Server) claimRuntimeTokenRotationCredential(w http.ResponseWriter, r *http.Request) {
	setRuntimeTokenRotationNoStore(w)
	token, ok := s.authenticateService(w, r, "service.config.read")
	if !ok {
		return
	}
	var body struct {
		ExpectedRevision int64  `json:"expected_revision"`
		ClaimID          string `json:"claim_id"`
	}
	if !decodeHostAgentControlJSON(w, r, &body) {
		return
	}
	body.ClaimID = strings.TrimSpace(body.ClaimID)
	rotationID := strings.TrimSpace(r.PathValue("rotation_id"))
	rotationStore, ok := s.runtimeTokenRotationStore(w)
	if !ok {
		return
	}
	unseal, err := nodeRuntimeTokenUnsealer()
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"code": "runtime_token_rotation_encryption_unavailable"})
		return
	}

	s.systemUpdateOperationMu.Lock()
	defer s.systemUpdateOperationMu.Unlock()
	token, ok = s.reauthenticateService(w, r, token, "service.config.read")
	if !ok {
		return
	}
	rotation, err := rotationStore.GetSystemUpdateRuntimeTokenRotation(r.Context(), rotationID)
	if err != nil {
		status, code := runtimeTokenRotationHTTPError(err, false)
		writeJSON(w, status, map[string]string{"code": code})
		return
	}
	agent, err := s.systemUpdateAgentForToken(r.Context(), token, rotation.ServiceID)
	if err != nil ||
		agent.TransportMode != store.SystemUpdateTransportPullV2 ||
		agent.ExecutionHostID != rotation.ExecutionHostID {
		s.writeServiceAudit(
			r, token, "nodes.runtime_token_rotation.credential.claim",
			"runtime_token_rotation", rotation.ID, "failure",
			map[string]any{"reason": "runtime_token_rotation_agent_mismatch"},
		)
		writeJSON(w, http.StatusForbidden, map[string]string{"code": "runtime_token_rotation_agent_mismatch"})
		return
	}
	result, err := rotationStore.ClaimSystemUpdateRuntimeTokenRotationStagedCredential(
		r.Context(),
		s.services,
		s.updaterPolicies,
		store.ClaimSystemUpdateRuntimeTokenRotationStagedCredentialParams{
			RotationID:                   rotation.ID,
			ServiceID:                    agent.ServiceID,
			ExecutionHostID:              agent.ExecutionHostID,
			AuthenticatedPreviousTokenID: token.ID,
			ExpectedRevision:             body.ExpectedRevision,
			ClaimID:                      body.ClaimID,
			Now:                          time.Now().UTC(),
		},
		unseal,
	)
	if err != nil {
		status, code := runtimeTokenRotationHTTPError(err, false)
		s.writeServiceAudit(
			r, token, "nodes.runtime_token_rotation.credential.claim",
			"runtime_token_rotation", rotation.ID, "failure",
			map[string]any{"reason": code},
		)
		writeJSON(w, status, map[string]string{"code": code})
		return
	}
	s.writeServiceAudit(
		r, token, "nodes.runtime_token_rotation.credential.claim",
		"runtime_token_rotation", rotation.ID, "success",
		runtimeTokenRotationAuditMetadata(result.Rotation, map[string]any{"claimed": result.Claimed}),
	)
	writeOneTimeSecretJSON(w, http.StatusOK, runtimeTokenRotationClaimResponse{
		Rotation: publicRuntimeTokenRotation(result.Rotation),
		Credential: runtimeTokenRotationCredentialResponse{
			TokenID:      result.Token.ID,
			RuntimeToken: result.Token.RawToken,
		},
		Claimed: result.Claimed,
	})
}

func (s *Server) acknowledgeRuntimeTokenRotationLocalStage(w http.ResponseWriter, r *http.Request) {
	s.runtimeTokenRotationStagedTransition(
		w,
		r,
		"nodes.runtime_token_rotation.local_stage_acknowledge",
		func(
			rotationStore store.SystemUpdateRuntimeTokenRotationStore,
			rotation store.SystemUpdateRuntimeTokenRotation,
			expectedRevision int64,
			rawToken string,
		) (store.SystemUpdateRuntimeTokenRotation, bool, error) {
			return rotationStore.MarkSystemUpdateRuntimeTokenRotationLocalStaged(
				r.Context(),
				s.services,
				s.updaterPolicies,
				store.MarkSystemUpdateRuntimeTokenRotationLocalStagedParams{
					RotationID:       rotation.ID,
					ExecutionHostID:  rotation.ExecutionHostID,
					ExpectedRevision: expectedRevision,
					RawStagedToken:   rawToken,
					Now:              time.Now().UTC(),
				},
			)
		},
	)
}

func (s *Server) proveRuntimeTokenRotationHeartbeat(w http.ResponseWriter, r *http.Request) {
	setRuntimeTokenRotationNoStore(w)
	rawToken, ok := runtimeTokenRotationStagedBearer(r)
	if !ok {
		w.Header().Set("WWW-Authenticate", `Bearer realm="runtime-token-rotation"`)
		writeJSON(w, http.StatusUnauthorized, map[string]string{"code": "staged_runtime_token_required"})
		return
	}
	var body struct {
		ExpectedRevision            int64  `json:"expected_revision"`
		AgentVersion                string `json:"agent_version"`
		AgentProtocolVersion        int    `json:"agent_protocol_version"`
		ExecutorVersion             string `json:"executor_version"`
		ExecutorProtocolVersion     int    `json:"executor_protocol_version"`
		MutationProtocolVersion     int    `json:"mutation_protocol_version"`
		OwnershipEpoch              int64  `json:"ownership_epoch"`
		SourcePolicyRevision        int64  `json:"source_policy_revision"`
		ProjectionRevision          int64  `json:"projection_revision"`
		LocalExecutorPolicyRevision int64  `json:"local_executor_policy_revision"`
		LocalExecutorPolicySHA256   string `json:"local_executor_policy_sha256"`
		LocalStageReceiptID         string `json:"local_stage_receipt_id"`
		LocalPhase                  string `json:"local_phase"`
	}
	if !decodeHostAgentControlJSON(w, r, &body) {
		return
	}
	rotationStore, ok := s.runtimeTokenRotationStore(w)
	if !ok {
		return
	}
	rotationID := strings.TrimSpace(r.PathValue("rotation_id"))
	s.systemUpdateOperationMu.Lock()
	defer s.systemUpdateOperationMu.Unlock()
	rotation, err := rotationStore.GetSystemUpdateRuntimeTokenRotation(
		r.Context(), rotationID,
	)
	if err != nil {
		status, code := runtimeTokenRotationHTTPError(err, true)
		writeJSON(w, status, map[string]string{"code": code})
		return
	}
	updated, applied, err := rotationStore.ProveSystemUpdateRuntimeTokenRotationHeartbeat(
		r.Context(),
		s.services,
		s.updaterPolicies,
		store.ProveSystemUpdateRuntimeTokenRotationHeartbeatParams{
			RotationID:                          rotation.ID,
			ServiceID:                           rotation.ServiceID,
			ExecutionHostID:                     rotation.ExecutionHostID,
			ExpectedRevision:                    body.ExpectedRevision,
			RawStagedToken:                      rawToken,
			Phase:                               strings.TrimSpace(body.LocalPhase),
			AgentVersion:                        strings.TrimSpace(body.AgentVersion),
			AgentProtocolVersion:                body.AgentProtocolVersion,
			ExecutorVersion:                     strings.TrimSpace(body.ExecutorVersion),
			ExecutorProtocolVersion:             body.ExecutorProtocolVersion,
			MutationProtocolVersion:             body.MutationProtocolVersion,
			ExpectedOwnershipEpoch:              body.OwnershipEpoch,
			ExpectedSourcePolicyRevision:        body.SourcePolicyRevision,
			ExpectedProjectionRevision:          body.ProjectionRevision,
			ExpectedLocalExecutorPolicyRevision: body.LocalExecutorPolicyRevision,
			ExpectedLocalExecutorPolicySHA256:   strings.TrimSpace(body.LocalExecutorPolicySHA256),
			LocalStageReceiptID:                 strings.TrimSpace(body.LocalStageReceiptID),
			Now:                                 time.Now().UTC(),
		},
	)
	if err != nil {
		status, code := runtimeTokenRotationHTTPError(err, true)
		s.writeRuntimeTokenRotationStagedAudit(
			r,
			"nodes.runtime_token_rotation.heartbeat_prove",
			rotation,
			"failure",
			map[string]any{"reason": code},
		)
		writeJSON(w, status, map[string]string{"code": code})
		return
	}
	s.writeRuntimeTokenRotationStagedAudit(
		r,
		"nodes.runtime_token_rotation.heartbeat_prove",
		updated,
		"success",
		map[string]any{
			"applied":                   applied,
			"agent_version":             body.AgentVersion,
			"agent_protocol_version":    body.AgentProtocolVersion,
			"executor_version":          body.ExecutorVersion,
			"executor_protocol_version": body.ExecutorProtocolVersion,
			"mutation_protocol_version": body.MutationProtocolVersion,
			"local_phase":               body.LocalPhase,
		},
	)
	writeJSON(w, http.StatusOK, runtimeTokenRotationMutationResponse{
		Rotation: publicRuntimeTokenRotation(updated),
		Applied:  applied,
	})
}

func (s *Server) activateRuntimeTokenRotation(w http.ResponseWriter, r *http.Request) {
	s.runtimeTokenRotationStagedTransition(
		w,
		r,
		"nodes.runtime_token_rotation.activate",
		func(
			rotationStore store.SystemUpdateRuntimeTokenRotationStore,
			rotation store.SystemUpdateRuntimeTokenRotation,
			expectedRevision int64,
			rawToken string,
		) (store.SystemUpdateRuntimeTokenRotation, bool, error) {
			return rotationStore.ActivateSystemUpdateRuntimeTokenRotation(
				r.Context(),
				s.services,
				store.ActivateSystemUpdateRuntimeTokenRotationParams{
					RotationID:       rotation.ID,
					ExecutionHostID:  rotation.ExecutionHostID,
					ExpectedRevision: expectedRevision,
					RawStagedToken:   rawToken,
					Now:              time.Now().UTC(),
				},
			)
		},
	)
}

func (s *Server) runtimeTokenRotationStagedTransition(
	w http.ResponseWriter,
	r *http.Request,
	action string,
	transition func(
		store.SystemUpdateRuntimeTokenRotationStore,
		store.SystemUpdateRuntimeTokenRotation,
		int64,
		string,
	) (store.SystemUpdateRuntimeTokenRotation, bool, error),
) {
	setRuntimeTokenRotationNoStore(w)
	rawToken, ok := runtimeTokenRotationStagedBearer(r)
	if !ok {
		w.Header().Set("WWW-Authenticate", `Bearer realm="runtime-token-rotation"`)
		writeJSON(w, http.StatusUnauthorized, map[string]string{"code": "staged_runtime_token_required"})
		return
	}
	var body struct {
		ExpectedRevision int64 `json:"expected_revision"`
	}
	if !decodeHostAgentControlJSON(w, r, &body) {
		return
	}
	rotationStore, ok := s.runtimeTokenRotationStore(w)
	if !ok {
		return
	}
	rotationID := strings.TrimSpace(r.PathValue("rotation_id"))

	s.systemUpdateOperationMu.Lock()
	defer s.systemUpdateOperationMu.Unlock()
	rotation, err := rotationStore.GetSystemUpdateRuntimeTokenRotation(r.Context(), rotationID)
	if err != nil {
		status, code := runtimeTokenRotationHTTPError(err, true)
		writeJSON(w, status, map[string]string{"code": code})
		return
	}
	updated, applied, err := transition(rotationStore, rotation, body.ExpectedRevision, rawToken)
	if err != nil {
		status, code := runtimeTokenRotationHTTPError(err, true)
		s.writeRuntimeTokenRotationStagedAudit(
			r, action, rotation, "failure", map[string]any{"reason": code},
		)
		writeJSON(w, status, map[string]string{"code": code})
		return
	}
	s.writeRuntimeTokenRotationStagedAudit(
		r, action, updated, "success", map[string]any{"applied": applied},
	)
	writeJSON(w, http.StatusOK, runtimeTokenRotationMutationResponse{
		Rotation: publicRuntimeTokenRotation(updated),
		Applied:  applied,
	})
}

func (s *Server) cancelRuntimeTokenRotation(w http.ResponseWriter, r *http.Request) {
	setRuntimeTokenRotationNoStore(w)
	if !runtimeTokenRotationAdminAllowed(w, r) {
		return
	}
	var body struct {
		ExpectedRevision int64 `json:"expected_revision"`
	}
	if !decodeHostAgentControlJSON(w, r, &body) {
		return
	}
	rotationStore, ok := s.runtimeTokenRotationStore(w)
	if !ok {
		return
	}
	rotationID := strings.TrimSpace(r.PathValue("rotation_id"))
	serviceID := strings.TrimSpace(r.PathValue("id"))
	s.systemUpdateOperationMu.Lock()
	defer s.systemUpdateOperationMu.Unlock()
	rotation, err := rotationStore.GetSystemUpdateRuntimeTokenRotation(r.Context(), rotationID)
	if err != nil {
		status, code := runtimeTokenRotationHTTPError(err, false)
		writeJSON(w, status, map[string]string{"code": code})
		return
	}
	if rotation.ServiceID != serviceID {
		writeJSON(w, http.StatusConflict, map[string]string{"code": "runtime_token_rotation_binding_conflict"})
		return
	}
	updated, applied, err := rotationStore.CancelSystemUpdateRuntimeTokenRotation(
		r.Context(),
		s.services,
		store.CancelSystemUpdateRuntimeTokenRotationParams{
			RotationID:       rotation.ID,
			ExecutionHostID:  rotation.ExecutionHostID,
			ExpectedRevision: body.ExpectedRevision,
			Now:              time.Now().UTC(),
		},
	)
	if err != nil {
		status, code := runtimeTokenRotationHTTPError(err, false)
		s.writeRuntimeTokenRotationAdminAudit(
			r, "nodes.runtime_token_rotation.cancel", serviceID, "failure",
			runtimeTokenRotationAuditMetadata(rotation, map[string]any{"reason": code}),
		)
		writeJSON(w, status, map[string]string{"code": code})
		return
	}
	s.writeRuntimeTokenRotationAdminAudit(
		r, "nodes.runtime_token_rotation.cancel", serviceID, "success",
		runtimeTokenRotationAuditMetadata(updated, map[string]any{"applied": applied}),
	)
	writeJSON(w, http.StatusOK, runtimeTokenRotationMutationResponse{
		Rotation: publicRuntimeTokenRotation(updated),
		Applied:  applied,
	})
}

func (s *Server) acknowledgeRuntimeTokenRotationCancel(w http.ResponseWriter, r *http.Request) {
	setRuntimeTokenRotationNoStore(w)
	token, ok := s.authenticateService(w, r, "service.config.read")
	if !ok {
		return
	}
	var body struct {
		ExpectedRevision int64 `json:"expected_revision"`
	}
	if !decodeHostAgentControlJSON(w, r, &body) {
		return
	}
	rotationStore, ok := s.runtimeTokenRotationStore(w)
	if !ok {
		return
	}
	rotationID := strings.TrimSpace(r.PathValue("rotation_id"))
	s.systemUpdateOperationMu.Lock()
	defer s.systemUpdateOperationMu.Unlock()
	token, ok = s.reauthenticateService(w, r, token, "service.config.read")
	if !ok {
		return
	}
	rotation, err := rotationStore.GetSystemUpdateRuntimeTokenRotation(
		r.Context(), rotationID,
	)
	if err != nil {
		status, code := runtimeTokenRotationHTTPError(err, false)
		writeJSON(w, status, map[string]string{"code": code})
		return
	}
	agent, err := s.systemUpdateAgentForToken(r.Context(), token, rotation.ServiceID)
	if err != nil ||
		agent.TransportMode != store.SystemUpdateTransportPullV2 ||
		agent.ExecutionHostID != rotation.ExecutionHostID {
		s.writeServiceAudit(
			r, token, "nodes.runtime_token_rotation.cancel_acknowledge",
			"runtime_token_rotation", rotation.ID, "failure",
			map[string]any{"reason": "runtime_token_rotation_agent_mismatch"},
		)
		writeJSON(w, http.StatusForbidden, map[string]string{"code": "runtime_token_rotation_agent_mismatch"})
		return
	}
	updated, applied, err := rotationStore.AcknowledgeSystemUpdateRuntimeTokenRotationCancel(
		r.Context(),
		s.services,
		s.updaterPolicies,
		store.AcknowledgeSystemUpdateRuntimeTokenRotationCancelParams{
			RotationID:                   rotation.ID,
			ServiceID:                    agent.ServiceID,
			ExecutionHostID:              agent.ExecutionHostID,
			AuthenticatedPreviousTokenID: token.ID,
			ExpectedRevision:             body.ExpectedRevision,
			Now:                          time.Now().UTC(),
		},
	)
	if err != nil {
		status, code := runtimeTokenRotationHTTPError(err, false)
		s.writeServiceAudit(
			r, token, "nodes.runtime_token_rotation.cancel_acknowledge",
			"runtime_token_rotation", rotation.ID, "failure",
			map[string]any{"reason": code},
		)
		writeJSON(w, status, map[string]string{"code": code})
		return
	}
	s.writeServiceAudit(
		r, token, "nodes.runtime_token_rotation.cancel_acknowledge",
		"runtime_token_rotation", rotation.ID, "success",
		runtimeTokenRotationAuditMetadata(updated, map[string]any{"applied": applied}),
	)
	writeJSON(w, http.StatusOK, runtimeTokenRotationMutationResponse{
		Rotation: publicRuntimeTokenRotation(updated),
		Applied:  applied,
	})
}

func (s *Server) emergencyRevokeRuntimeTokenRotation(w http.ResponseWriter, r *http.Request) {
	setRuntimeTokenRotationNoStore(w)
	if !runtimeTokenRotationAdminAllowed(w, r) {
		return
	}
	var body struct {
		ExpectedRevision int64  `json:"expected_revision"`
		TokenSlot        string `json:"token_slot"`
	}
	if !decodeHostAgentControlJSON(w, r, &body) {
		return
	}
	body.TokenSlot = strings.ToLower(strings.TrimSpace(body.TokenSlot))
	if body.TokenSlot != "previous" && body.TokenSlot != "staged" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_runtime_token_rotation_token_slot"})
		return
	}
	rotationStore, ok := s.runtimeTokenRotationStore(w)
	if !ok {
		return
	}
	rotationID := strings.TrimSpace(r.PathValue("rotation_id"))
	serviceID := strings.TrimSpace(r.PathValue("id"))
	s.systemUpdateOperationMu.Lock()
	defer s.systemUpdateOperationMu.Unlock()
	rotation, err := rotationStore.GetSystemUpdateRuntimeTokenRotation(r.Context(), rotationID)
	if err != nil {
		status, code := runtimeTokenRotationHTTPError(err, false)
		writeJSON(w, status, map[string]string{"code": code})
		return
	}
	if rotation.ServiceID != serviceID {
		writeJSON(w, http.StatusConflict, map[string]string{"code": "runtime_token_rotation_binding_conflict"})
		return
	}
	tokenID := rotation.PreviousTokenID
	if body.TokenSlot == "staged" {
		tokenID = rotation.StagedTokenID
	}
	updated, applied, err := rotationStore.EmergencyRevokeSystemUpdateRuntimeToken(
		r.Context(),
		s.services,
		store.EmergencyRevokeSystemUpdateRuntimeTokenParams{
			RotationID:       rotation.ID,
			ExecutionHostID:  rotation.ExecutionHostID,
			ExpectedRevision: body.ExpectedRevision,
			TokenID:          tokenID,
			Now:              time.Now().UTC(),
		},
	)
	if err != nil {
		status, code := runtimeTokenRotationHTTPError(err, false)
		s.writeRuntimeTokenRotationAdminAudit(
			r, "nodes.runtime_token_rotation.emergency_revoke", serviceID, "failure",
			runtimeTokenRotationAuditMetadata(
				rotation,
				map[string]any{"reason": code, "token_slot": body.TokenSlot},
			),
		)
		writeJSON(w, status, map[string]string{"code": code})
		return
	}
	s.writeRuntimeTokenRotationAdminAudit(
		r, "nodes.runtime_token_rotation.emergency_revoke", serviceID, "success",
		runtimeTokenRotationAuditMetadata(
			updated,
			map[string]any{
				"applied":           applied,
				"requested_slot":    body.TokenSlot,
				"terminal_code":     store.SystemUpdateRuntimeTokenRotationEmergencyCode,
				"recovery_required": true,
			},
		),
	)
	writeJSON(w, http.StatusOK, runtimeTokenRotationMutationResponse{
		Rotation: publicRuntimeTokenRotation(updated),
		Applied:  applied,
		Code:     store.SystemUpdateRuntimeTokenRotationEmergencyCode,
	})
}

func (s *Server) runtimeTokenRotationStageBinding(
	r *http.Request,
	serviceID string,
) (
	store.RegisteredService,
	store.UpdaterPolicy,
	store.SystemUpdateExecutionHost,
	error,
) {
	service, err := s.services.GetService(r.Context(), serviceID)
	if err != nil {
		return store.RegisteredService{}, store.UpdaterPolicy{},
			store.SystemUpdateExecutionHost{}, err
	}
	if service.ServiceType != "update_agent" ||
		service.TransportMode != store.SystemUpdateTransportPullV2 ||
		strings.TrimSpace(service.ExecutionHostID) == "" ||
		service.OwnershipEpoch < 1 {
		return store.RegisteredService{}, store.UpdaterPolicy{},
			store.SystemUpdateExecutionHost{}, store.ErrSystemUpdateOwnershipConflict
	}
	policy, err := s.updaterPolicies.GetUpdaterPolicy(r.Context(), service.ServiceID)
	if err != nil {
		return store.RegisteredService{}, store.UpdaterPolicy{},
			store.SystemUpdateExecutionHost{}, err
	}
	ownershipStore, ok := s.systemUpdates.(store.SystemUpdateExecutionHostStore)
	if !ok {
		return store.RegisteredService{}, store.UpdaterPolicy{},
			store.SystemUpdateExecutionHost{},
			store.ErrSystemUpdateRuntimeTokenRotationStoreMismatch
	}
	ownership, err := ownershipStore.GetSystemUpdateExecutionHost(
		r.Context(), service.ExecutionHostID,
	)
	if err != nil {
		return store.RegisteredService{}, store.UpdaterPolicy{},
			store.SystemUpdateExecutionHost{}, err
	}
	if policy.UpdaterID != service.ServiceID ||
		policy.TransportMode != store.SystemUpdateTransportPullV2 ||
		policy.ExecutionHostID != service.ExecutionHostID ||
		policy.Revision < 1 ||
		policy.ProjectionRevision < 1 ||
		policy.LocalExecutorPolicyRevision < 1 ||
		ownership.ExecutionHostID != service.ExecutionHostID ||
		ownership.TransportMode != store.SystemUpdateTransportPullV2 ||
		ownership.AgentServiceID != service.ServiceID ||
		ownership.OwnershipEpoch != service.OwnershipEpoch ||
		ownership.PolicyRevision != policy.ProjectionRevision {
		return store.RegisteredService{}, store.UpdaterPolicy{},
			store.SystemUpdateExecutionHost{}, store.ErrSystemUpdateOwnershipConflict
	}
	return service, policy, ownership, nil
}

func (s *Server) runtimeTokenRotationStore(
	w http.ResponseWriter,
) (store.SystemUpdateRuntimeTokenRotationStore, bool) {
	rotationStore, ok := s.systemUpdates.(store.SystemUpdateRuntimeTokenRotationStore)
	if !ok {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"code": "runtime_token_rotation_unavailable"})
		return nil, false
	}
	return rotationStore, true
}

func nodeRuntimeTokenUnsealer() (store.NodeTokenUnsealer, error) {
	key, err := nodeRuntimeTokenEncryptionKey()
	if err != nil {
		return nil, err
	}
	return func(ciphertext, nonce string) (string, error) {
		rawToken, err := security.DecryptSecret(ciphertext, nonce, key)
		if err != nil || strings.TrimSpace(rawToken) == "" {
			return "", errors.New("node runtime token could not be decrypted")
		}
		return rawToken, nil
	}, nil
}

func decodeHostAgentControlJSON(
	w http.ResponseWriter,
	r *http.Request,
	target any,
) bool {
	payload, err := io.ReadAll(io.LimitReader(
		r.Body,
		maxHostAgentControlRequestBytes+1,
	))
	if err != nil || len(payload) > maxHostAgentControlRequestBytes {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "bad_request"})
		return false
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "bad_request"})
		return false
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "bad_request"})
		return false
	}
	return true
}

func runtimeTokenRotationStagedBearer(r *http.Request) (string, bool) {
	raw, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	raw = strings.TrimSpace(raw)
	return raw, ok && raw != ""
}

func runtimeTokenRotationAdminAllowed(w http.ResponseWriter, r *http.Request) bool {
	current := currentFromContext(r.Context())
	if !security.HasPermission(current.Permissions, "api_tokens.create") ||
		!security.HasPermission(current.Permissions, "api_tokens.revoke") ||
		!security.HasPermission(current.Permissions, "secrets.update") {
		writeJSON(w, http.StatusForbidden, map[string]string{"code": "permission_denied"})
		return false
	}
	return true
}

func setRuntimeTokenRotationNoStore(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("Expires", "0")
	w.Header().Set("Referrer-Policy", "no-referrer")
}

func runtimeTokenRotationHTTPError(err error, stagedCredential bool) (int, string) {
	switch {
	case errors.Is(err, store.ErrInvalidSystemUpdateRuntimeTokenRotation):
		return http.StatusBadRequest, "invalid_runtime_token_rotation"
	case errors.Is(err, store.ErrNotFound):
		return http.StatusNotFound, "runtime_token_rotation_not_found"
	case errors.Is(err, store.ErrSystemUpdateRuntimeTokenRotationToken):
		if stagedCredential {
			return http.StatusUnauthorized, "invalid_staged_runtime_token"
		}
		return http.StatusConflict, "runtime_token_rotation_credential_conflict"
	case errors.Is(err, store.ErrSystemUpdateRuntimeTokenRotationStale):
		return http.StatusConflict, "runtime_token_rotation_revision_conflict"
	case errors.Is(err, store.ErrSystemUpdateRuntimeTokenRotationCredentialClaimed):
		return http.StatusConflict, "runtime_token_rotation_credential_already_claimed"
	case errors.Is(err, store.ErrSystemUpdateRuntimeTokenRotationHeartbeatProof):
		return http.StatusConflict, "runtime_token_rotation_heartbeat_proof_invalid"
	case errors.Is(err, store.ErrSystemUpdateRuntimeTokenRotationTransition):
		return http.StatusConflict, "runtime_token_rotation_transition_invalid"
	case errors.Is(err, store.ErrSystemUpdateRuntimeTokenRotationBusy):
		return http.StatusConflict, "runtime_token_rotation_active"
	case errors.Is(err, store.ErrSystemUpdateRuntimeTokenRotationSharedToken):
		return http.StatusConflict, "runtime_token_rotation_shared_token"
	case errors.Is(err, store.ErrSystemUpdateExecutionHostBusy):
		return http.StatusConflict, "system_update_execution_host_busy"
	case errors.Is(err, store.ErrSystemUpdateAgentInactive):
		return http.StatusConflict, "runtime_token_rotation_agent_inactive"
	case errors.Is(err, store.ErrSystemUpdateOwnershipConflict),
		errors.Is(err, store.ErrSystemUpdateAgentBindingMismatch),
		errors.Is(err, store.ErrSystemUpdateExecutionHostStale):
		return http.StatusConflict, "runtime_token_rotation_binding_conflict"
	case errors.Is(err, store.ErrAlreadyExists):
		return http.StatusConflict, "runtime_token_rotation_idempotency_conflict"
	case errors.Is(err, store.ErrConflict):
		return http.StatusConflict, "runtime_token_rotation_conflict"
	case errors.Is(err, store.ErrSystemUpdateRuntimeTokenRotationStoreMismatch):
		return http.StatusServiceUnavailable, "runtime_token_rotation_unavailable"
	default:
		return http.StatusInternalServerError, "runtime_token_rotation_failed"
	}
}

func runtimeTokenRotationAuditMetadata(
	rotation store.SystemUpdateRuntimeTokenRotation,
	extra map[string]any,
) map[string]any {
	metadata := map[string]any{
		"execution_host_id": rotation.ExecutionHostID,
		"status":            rotation.Status,
		"revision":          rotation.Revision,
	}
	for key, value := range extra {
		metadata[key] = value
	}
	return metadata
}

func (s *Server) writeRuntimeTokenRotationAdminAudit(
	r *http.Request,
	action string,
	serviceID string,
	result string,
	metadata map[string]any,
) {
	current := currentFromContext(r.Context())
	s.writeAudit(r, store.AuditEvent{
		ActorUserID:   current.User.ID,
		ActorUsername: current.User.Username,
		Action:        action,
		ResourceType:  "runtime_token_rotation",
		ResourceID:    serviceID,
		Result:        result,
		Metadata:      metadata,
	})
}

func (s *Server) writeRuntimeTokenRotationStagedAudit(
	r *http.Request,
	action string,
	rotation store.SystemUpdateRuntimeTokenRotation,
	result string,
	extra map[string]any,
) {
	s.writeAudit(r, store.AuditEvent{
		ActorUserID:   "runtime-token-rotation:" + rotation.ServiceID,
		ActorUsername: "runtime-token-rotation",
		Action:        action,
		ResourceType:  "runtime_token_rotation",
		ResourceID:    rotation.ID,
		Result:        result,
		Metadata: runtimeTokenRotationAuditMetadata(
			rotation,
			extra,
		),
	})
}
