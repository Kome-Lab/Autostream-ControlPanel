package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/example/autostream-control-panel/internal/store"
	"github.com/example/autostream-control-panel/internal/updateradapter"
)

const (
	hostSelfUpdateGrantTTL                     = 90 * time.Second
	hostSelfUpdateGrantConsumeAttemptThreshold = 12
	maxHostSelfUpdateRequestBytes              = 16 * 1024
)

type HostSelfUpdateReleaseResolver interface {
	ResolveHostSelfUpdateRelease(
		context.Context,
		string,
		string,
	) (store.SystemUpdateHostReleaseMetadata, error)
}

type productionHostSelfUpdateReleaseResolver struct{}

func (productionHostSelfUpdateReleaseResolver) ResolveHostSelfUpdateRelease(
	ctx context.Context,
	version, arch string,
) (store.SystemUpdateHostReleaseMetadata, error) {
	metadata, err := (updateradapter.ReleaseDownloader{
		TrustedPublicOnly: true,
	}).ResolveHostAgentReleaseMetadata(ctx, version, arch)
	if err != nil {
		return store.SystemUpdateHostReleaseMetadata{}, err
	}
	return store.SystemUpdateHostReleaseMetadata{
		Tag:                     metadata.Tag,
		Commit:                  metadata.Commit,
		PublishedAt:             metadata.PublishedAt,
		ManifestAssetID:         metadata.ManifestAssetID,
		ManifestAssetName:       metadata.ManifestAssetName,
		ManifestSHA256:          metadata.ManifestSHA256,
		ManifestChecksumAssetID: metadata.ManifestChecksumAssetID,
		ManifestChecksumSHA256:  metadata.ManifestChecksumSHA256,
		ArchiveAssetID:          metadata.ArchiveAssetID,
		ArchiveAssetName:        metadata.ArchiveAssetName,
		ArchiveSize:             metadata.ArchiveSize,
		ArchiveSHA256:           metadata.ArchiveSHA256,
		ArchiveChecksumAssetID:  metadata.ArchiveChecksumAssetID,
		ArchiveChecksumSHA256:   metadata.ArchiveChecksumSHA256,
		Arch:                    metadata.Arch,
		AgentProtocolVersion:    metadata.AgentProtocolVersion,
		ExecutorProtocolVersion: metadata.ExecutorProtocolVersion,
		MutationProtocolVersion: metadata.MutationProtocolVersion,
		RecoveryProtocolVersion: metadata.RecoveryProtocolVersion,
		MinimumPanelVersion:     metadata.MinimumPanelVersion,
		AttestationVerifiedAt:   metadata.AttestationVerifiedAt,
	}, nil
}

func (s *Server) listHostSelfUpdates(w http.ResponseWriter, r *http.Request) {
	updates, ok := s.systemUpdates.(store.SystemUpdateHostSelfUpdateStore)
	if !ok {
		writeJSON(w, http.StatusConflict, map[string]string{
			"code": "host_self_update_store_unavailable",
		})
		return
	}
	items, err := updates.ListSystemUpdateHostSelfUpdates(
		r.Context(), parseLimit(r, 100),
	)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{
			"code": "list_host_self_updates_failed",
		})
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{"self_updates": items})
}

func (s *Server) createHostSelfUpdate(w http.ResponseWriter, r *http.Request) {
	updates, ok := s.systemUpdates.(store.SystemUpdateHostSelfUpdateStore)
	if !ok {
		writeJSON(w, http.StatusConflict, map[string]string{
			"code": "host_self_update_store_unavailable",
		})
		return
	}
	hosts, ok := s.systemUpdates.(store.SystemUpdateExecutionHostStore)
	if !ok {
		writeJSON(w, http.StatusConflict, map[string]string{
			"code": "host_self_update_store_unavailable",
		})
		return
	}
	hostID := strings.TrimSpace(r.PathValue("host_id"))
	var body struct {
		TargetVersion  string `json:"target_version"`
		IdempotencyKey string `json:"idempotency_key"`
	}
	if err := decodeHostSelfUpdateJSON(r, &body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"code": "invalid_host_self_update_request",
		})
		return
	}
	body.TargetVersion = strings.TrimSpace(body.TargetVersion)
	body.IdempotencyKey = strings.TrimSpace(body.IdempotencyKey)
	if hostID == "" || body.TargetVersion == "" ||
		body.IdempotencyKey == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"code": "invalid_host_self_update_request",
		})
		return
	}
	ownership, err := hosts.GetSystemUpdateExecutionHost(
		r.Context(), hostID,
	)
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{
			"code": "host_self_update_host_not_found",
		})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{
			"code": "get_host_self_update_host_failed",
		})
		return
	}
	if ownership.TransportMode != store.SystemUpdateTransportPullV2 ||
		ownership.OwnershipEpoch < 1 ||
		ownership.AgentServiceID == "" {
		writeJSON(w, http.StatusConflict, map[string]string{
			"code": "host_self_update_host_not_ready",
		})
		return
	}
	agent, err := s.services.GetService(
		r.Context(), ownership.AgentServiceID,
	)
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{
			"code": "host_self_update_agent_not_found",
		})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{
			"code": "get_host_self_update_agent_failed",
		})
		return
	}
	arch := strings.ToLower(strings.TrimSpace(agent.ReportedArch))
	if arch != "amd64" && arch != "arm64" {
		writeJSON(w, http.StatusConflict, map[string]string{
			"code": "host_self_update_arch_unavailable",
		})
		return
	}
	release, err := s.hostSelfUpdateReleases.ResolveHostSelfUpdateRelease(
		r.Context(), body.TargetVersion, arch,
	)
	if err != nil {
		writeJSON(w, http.StatusConflict, map[string]string{
			"code": "host_self_update_release_unverified",
		})
		return
	}
	s.systemUpdateOperationMu.Lock()
	defer s.systemUpdateOperationMu.Unlock()
	ownership, err = hosts.GetSystemUpdateExecutionHost(r.Context(), hostID)
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{
			"code": "host_self_update_host_not_found",
		})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{
			"code": "get_host_self_update_host_failed",
		})
		return
	}
	if ownership.TransportMode != store.SystemUpdateTransportPullV2 ||
		ownership.OwnershipEpoch < 1 ||
		ownership.AgentServiceID == "" {
		writeJSON(w, http.StatusConflict, map[string]string{
			"code": "host_self_update_host_not_ready",
		})
		return
	}
	agent, err = s.services.GetService(r.Context(), ownership.AgentServiceID)
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{
			"code": "host_self_update_agent_not_found",
		})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{
			"code": "get_host_self_update_agent_failed",
		})
		return
	}
	if currentArch := strings.ToLower(strings.TrimSpace(agent.ReportedArch)); currentArch != arch ||
		currentArch != strings.ToLower(strings.TrimSpace(release.Arch)) {
		writeJSON(w, http.StatusConflict, map[string]string{
			"code": "host_self_update_arch_unavailable",
		})
		return
	}
	current := currentFromContext(r.Context())
	now := time.Now().UTC()
	update, created, err := updates.CreateSystemUpdateHostSelfUpdate(
		r.Context(), s.services, s.updaterPolicies,
		store.CreateSystemUpdateHostSelfUpdateParams{
			ExecutionHostID:     hostID,
			TargetVersion:       body.TargetVersion,
			IdempotencyKey:      body.IdempotencyKey,
			RequestedByUserID:   current.User.ID,
			RequestedByUsername: current.User.Username,
			Release:             release,
			Now:                 now,
		},
	)
	if writeHostSelfUpdateStoreError(w, err) {
		return
	}
	if created {
		s.writeAudit(r, store.AuditEvent{
			ActorUserID:   current.User.ID,
			ActorUsername: current.User.Username,
			Action:        "system_updates.host_self_update.create",
			ResourceType:  "host_self_update",
			ResourceID:    update.ID,
			Result:        "success",
			Metadata: map[string]any{
				"execution_host_id": update.ExecutionHostID,
				"target_version":    update.TargetVersion,
				"release_commit":    update.Release.Commit,
				"artifact_arch":     update.Release.Arch,
				"idempotent_replay": false,
			},
		})
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusAccepted, update)
}

func (s *Server) retryHostSelfUpdate(w http.ResponseWriter, r *http.Request) {
	updates, ok := s.systemUpdates.(store.SystemUpdateHostSelfUpdateStore)
	if !ok {
		writeJSON(w, http.StatusConflict, map[string]string{
			"code": "host_self_update_store_unavailable",
		})
		return
	}
	var body struct {
		IdempotencyKey string `json:"idempotency_key"`
	}
	if err := decodeHostSelfUpdateJSON(r, &body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"code": "invalid_host_self_update_retry",
		})
		return
	}
	s.systemUpdateOperationMu.Lock()
	defer s.systemUpdateOperationMu.Unlock()
	current := currentFromContext(r.Context())
	update, created, err := updates.RetrySystemUpdateHostSelfUpdate(
		r.Context(), s.services, s.updaterPolicies,
		store.RetrySystemUpdateHostSelfUpdateParams{
			ID:                  r.PathValue("id"),
			IdempotencyKey:      strings.TrimSpace(body.IdempotencyKey),
			RequestedByUserID:   current.User.ID,
			RequestedByUsername: current.User.Username,
			Now:                 time.Now().UTC(),
		},
	)
	if writeHostSelfUpdateStoreError(w, err) {
		return
	}
	if created {
		s.writeAudit(r, store.AuditEvent{
			ActorUserID:   current.User.ID,
			ActorUsername: current.User.Username,
			Action:        "system_updates.host_self_update.retry",
			ResourceType:  "host_self_update",
			ResourceID:    update.ID,
			Result:        "success",
			Metadata: map[string]any{
				"retry_of_id":       update.RetryOfID,
				"execution_host_id": update.ExecutionHostID,
				"target_version":    update.TargetVersion,
			},
		})
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusAccepted, update)
}

func (s *Server) cancelHostSelfUpdate(w http.ResponseWriter, r *http.Request) {
	updates, ok := s.systemUpdates.(store.SystemUpdateHostSelfUpdateStore)
	if !ok {
		writeJSON(w, http.StatusConflict, map[string]string{
			"code": "host_self_update_store_unavailable",
		})
		return
	}
	var body struct {
		ExpectedRevision int64 `json:"expected_revision"`
	}
	if err := decodeHostSelfUpdateJSON(r, &body); err != nil ||
		body.ExpectedRevision < 1 {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"code": "invalid_host_self_update_cancel",
		})
		return
	}
	before, err := updates.GetSystemUpdateHostSelfUpdate(
		r.Context(), r.PathValue("id"),
	)
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{
			"code": "host_self_update_not_found",
		})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{
			"code": "get_host_self_update_failed",
		})
		return
	}
	current := currentFromContext(r.Context())
	update, err := updates.CancelSystemUpdateHostSelfUpdate(
		r.Context(), before.ID, current.User.ID,
		body.ExpectedRevision, false, time.Now().UTC(),
	)
	if writeHostSelfUpdateStoreError(w, err) {
		return
	}
	s.writeAudit(r, store.AuditEvent{
		ActorUserID:   current.User.ID,
		ActorUsername: current.User.Username,
		Action:        "system_updates.host_self_update.cancel",
		ResourceType:  "host_self_update",
		ResourceID:    update.ID,
		Result:        "success",
		Metadata: map[string]any{
			"execution_host_id": update.ExecutionHostID,
			"status":            update.Status,
		},
	})
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, update)
}

func (s *Server) issueHostSelfUpdateGrant(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	token, ok := s.authenticateService(w, r, "service.config.read")
	if !ok {
		return
	}
	updates, ok := s.systemUpdates.(store.SystemUpdateHostSelfUpdateStore)
	if !ok {
		writeJSON(w, http.StatusConflict, map[string]string{
			"code": "host_self_update_store_unavailable",
		})
		return
	}
	var body struct {
		ExpectedRevision int64  `json:"expected_revision"`
		Operation        string `json:"operation"`
		PlanSHA256       string `json:"plan_sha256"`
		SessionID        string `json:"session_id"`
	}
	if err := decodeHostSelfUpdateJSON(r, &body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"code": "invalid_host_self_update_grant_request",
		})
		return
	}
	update, err := updates.GetSystemUpdateHostSelfUpdate(
		r.Context(), r.PathValue("id"),
	)
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{
			"code": "host_self_update_not_found",
		})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{
			"code": "get_host_self_update_failed",
		})
		return
	}
	agent, err := s.systemUpdateAgentForToken(
		r.Context(), token, update.AgentServiceID,
	)
	if err != nil {
		writeSystemUpdateAgentError(w, err)
		return
	}
	result, err := updates.IssueSystemUpdateHostSelfUpdateGrant(
		r.Context(), s.services, s.updaterPolicies,
		store.IssueSystemUpdateHostSelfUpdateGrantParams{
			SelfUpdateID:     update.ID,
			ExecutionHostID:  agent.ExecutionHostID,
			AgentServiceID:   agent.ServiceID,
			ExpectedRevision: body.ExpectedRevision,
			Operation:        strings.TrimSpace(body.Operation),
			PlanSHA256:       strings.TrimSpace(body.PlanSHA256),
			SessionID:        strings.TrimSpace(body.SessionID),
			Now:              time.Now().UTC(),
			TTL:              hostSelfUpdateGrantTTL,
		},
	)
	if writeHostSelfUpdateStoreError(w, err) {
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusCreated, result)
}

func (s *Server) consumeHostSelfUpdateGrant(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	attemptKey := loginFailureKey(
		"host-self-update-grant-consume",
		clientIP(r),
	)
	if !s.loginFailures.allow(
		attemptKey,
		hostSelfUpdateGrantConsumeAttemptThreshold,
	) {
		w.Header().Set("Retry-After", "300")
		writeJSON(w, http.StatusTooManyRequests, map[string]string{
			"code": "host_self_update_grant_rate_limited",
		})
		return
	}
	updates, ok := s.systemUpdates.(store.SystemUpdateHostSelfUpdateStore)
	if !ok {
		writeJSON(w, http.StatusConflict, map[string]string{
			"code": "host_self_update_store_unavailable",
		})
		return
	}
	var body struct {
		Token   string                                `json:"token"`
		Binding store.SystemUpdateHostSelfUpdateGrant `json:"binding"`
	}
	if err := decodeHostSelfUpdateJSON(r, &body); err != nil {
		s.loginFailures.record(attemptKey)
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"code": "invalid_host_self_update_grant_consume",
		})
		return
	}
	result, err := updates.ConsumeSystemUpdateHostSelfUpdateGrant(
		r.Context(), s.services, s.updaterPolicies,
		store.ConsumeSystemUpdateHostSelfUpdateGrantParams{
			RawToken: strings.TrimSpace(body.Token),
			Binding:  body.Binding,
			Now:      time.Now().UTC(),
		},
	)
	if err != nil {
		s.loginFailures.record(attemptKey)
		writeHostSelfUpdateStoreError(w, err)
		return
	}
	s.loginFailures.clear(attemptKey)
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, result)
}

func decodeHostSelfUpdateJSON(r *http.Request, out any) error {
	payload, err := io.ReadAll(io.LimitReader(
		r.Body,
		maxHostSelfUpdateRequestBytes+1,
	))
	if err != nil || len(payload) > maxHostSelfUpdateRequestBytes {
		return errors.New("host self-update request is too large")
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(out); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return errors.New("trailing JSON")
	}
	return nil
}

func writeHostSelfUpdateStoreError(w http.ResponseWriter, err error) bool {
	if err == nil {
		return false
	}
	status := http.StatusConflict
	code := "host_self_update_conflict"
	switch {
	case errors.Is(err, store.ErrInvalidSystemUpdateHostSelfUpdate),
		errors.Is(err, store.ErrSystemUpdateHostSelfUpdateGrant):
		status, code = http.StatusBadRequest, "invalid_host_self_update_request"
	case errors.Is(err, store.ErrNotFound):
		status, code = http.StatusNotFound, "host_self_update_not_found"
	case errors.Is(err, store.ErrAlreadyExists):
		code = "host_self_update_idempotency_conflict"
	case errors.Is(err, store.ErrSystemUpdateHostSelfUpdateBusy):
		code = "host_self_update_active"
	case errors.Is(err, store.ErrSystemUpdateHostSelfUpdateStale):
		code = "host_self_update_revision_conflict"
	case errors.Is(err, store.ErrSystemUpdateHostSelfUpdateCancel):
		code = "host_self_update_cancel_unsupported"
	case errors.Is(err, store.ErrSystemUpdateHostSelfUpdateExpired):
		code = "host_self_update_grant_expired"
	case errors.Is(err, store.ErrSystemUpdateHostSelfUpdateConsumed):
		code = "host_self_update_grant_consumed"
	case errors.Is(err, store.ErrSystemUpdateExecutionHostBusy),
		errors.Is(err, store.ErrSystemUpdateRuntimeTokenRotationBusy):
		code = "host_lifecycle_busy"
	case errors.Is(err, store.ErrSystemUpdateOwnershipConflict):
		code = "host_self_update_ownership_conflict"
	case errors.Is(err, store.ErrSystemUpdateAgentInactive),
		errors.Is(err, store.ErrSystemUpdateAgentNotReady):
		code = "host_self_update_agent_not_ready"
	default:
		status, code = http.StatusInternalServerError,
			"host_self_update_failed"
	}
	writeJSON(w, status, map[string]string{"code": code})
	return true
}
