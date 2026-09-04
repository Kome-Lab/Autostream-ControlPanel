package httpapi

import (
	"crypto/ecdh"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/example/autostream-control-panel/internal/security"
	"github.com/example/autostream-control-panel/internal/store"
)

const updateHostBootstrapRequestMaxBytes = UpdateHostBootstrapMaxEnvelopeBytes +
	UpdateHostBootstrapMaxHosts*192 + 16*1024

type updateHostBootstrapEnvelopeRequest struct {
	Version            int    `json:"version"`
	EphemeralPublicKey string `json:"ephemeral_public_key"`
	Nonce              string `json:"nonce"`
	Ciphertext         string `json:"ciphertext"`
}

type updateHostBootstrapCreateRequest struct {
	JobID                   string                             `json:"job_id"`
	IdempotencyKey          string                             `json:"idempotency_key"`
	ExpectedRevision        int64                              `json:"expected_revision"`
	RecipientKeyFingerprint string                             `json:"recipient_key_fingerprint"`
	HostIDs                 []string                           `json:"host_ids"`
	Envelope                updateHostBootstrapEnvelopeRequest `json:"envelope"`
}

func (s *Server) createUpdateHostBootstrapJob(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	current := currentFromContext(r.Context())
	if !security.HasPermission(current.Permissions, "secrets.update") {
		writeJSON(w, http.StatusForbidden, map[string]string{"code": "permission_denied"})
		return
	}
	if !runtimeSecretTransportAllowed(r) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "secure_transport_required"})
		return
	}

	var body updateHostBootstrapCreateRequest
	if !decodeSingleUpdateHostBootstrapJSON(w, r, &body) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "bad_request"})
		return
	}
	if !validUpdateHostBootstrapJobID(body.JobID) ||
		!validUpdateHostBootstrapRecipientKeyFingerprint(body.RecipientKeyFingerprint) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_bootstrap_job_request"})
		return
	}
	envelope, err := normalizeUpdateHostBootstrapEnvelope(body.Envelope)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_bootstrap_envelope"})
		return
	}
	defer wipeUpdateHostBootstrapBytes(envelope)

	updaterID := strings.TrimSpace(r.PathValue("id"))
	s.systemUpdateOperationMu.Lock()
	defer s.systemUpdateOperationMu.Unlock()
	agent, ok := s.registeredUpdateAgent(w, r, updaterID)
	if !ok {
		return
	}
	if updaterID != agent.ServiceID {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_update_agent"})
		return
	}
	updaterID = agent.ServiceID
	now := time.Now().UTC()
	if updateHostBootstrapUpdaterConfigurationPending(agent) {
		writeJSON(w, http.StatusConflict, map[string]string{"code": "updater_configuration_pending"})
		return
	}
	policy, err := s.updaterPolicies.GetUpdaterPolicy(r.Context(), updaterID)
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeJSON(w, http.StatusConflict, map[string]string{"code": "updater_policy_not_configured"})
		return
	case err != nil:
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "get_updater_policy_failed"})
		return
	}
	if body.ExpectedRevision != policy.Revision {
		writeJSON(w, http.StatusConflict, map[string]string{"code": "bootstrap_policy_revision_mismatch"})
		return
	}
	hostIDs, err := validateUpdateHostBootstrapHosts(policy, body.HostIDs)
	if err != nil {
		writeUpdateHostBootstrapValidationError(w, err)
		return
	}
	releaseTokenStatus, err := s.updaterReleaseTokenStatus(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "get_updater_release_token_status_failed"})
		return
	}
	if !releaseTokenStatus.Configured {
		writeJSON(w, http.StatusConflict, map[string]string{"code": "updater_release_token_not_configured"})
		return
	}
	if !systemUpdateAgentAvailable(agent, now) {
		writeJSON(w, http.StatusConflict, map[string]string{"code": "updater_offline"})
		return
	}
	appliedRevision, policyStatus, _ := systemUpdateManagedPolicyReport(agent)
	expectedAppliedRevision := policy.ProjectionRevision
	if expectedAppliedRevision < 1 {
		expectedAppliedRevision = policy.Revision
	}
	if appliedRevision != expectedAppliedRevision || policyStatus != "applied" {
		writeJSON(w, http.StatusConflict, map[string]string{"code": "updater_policy_not_applied"})
		return
	}
	publicKey, currentFingerprint := updateHostBootstrapEncryptionIdentity(agent)
	if publicKey == "" {
		writeJSON(w, http.StatusConflict, map[string]string{"code": "bootstrap_encryption_key_unavailable"})
		return
	}
	if body.RecipientKeyFingerprint != currentFingerprint {
		writeJSON(w, http.StatusConflict, map[string]string{"code": "bootstrap_recipient_key_changed"})
		return
	}

	job, replayed, err := s.updateHostBootstrapJobs.Create(UpdateHostBootstrapCreateParams{
		UpdaterID:               updaterID,
		ExpectedRevision:        body.ExpectedRevision,
		ClientJobID:             body.JobID,
		IdempotencyKey:          body.IdempotencyKey,
		RecipientKeyFingerprint: body.RecipientKeyFingerprint,
		HostIDs:                 hostIDs,
		Envelope:                envelope,
	})
	if err != nil {
		writeUpdateHostBootstrapBrokerError(w, err)
		return
	}
	if !replayed {
		s.writeAudit(r, store.AuditEvent{
			ActorUserID: current.User.ID, ActorUsername: current.User.Username,
			Action: "system_updates.bootstrap.create", ResourceType: "update_host_bootstrap", ResourceID: job.ID, Result: "success",
			Metadata: map[string]any{
				"updater_id": updaterID, "expected_revision": body.ExpectedRevision,
				"host_count": len(hostIDs), "idempotent_replay": false,
			},
		})
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"jobs": []UpdateHostBootstrapJob{job}})
}

func (s *Server) listUpdateHostBootstrapJobs(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	updaterID := strings.TrimSpace(r.PathValue("id"))
	agent, ok := s.registeredUpdateAgent(w, r, updaterID)
	if !ok {
		return
	}
	updaterID = agent.ServiceID
	jobs, err := s.updateHostBootstrapJobs.List(updaterID)
	if err != nil {
		writeUpdateHostBootstrapBrokerError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"jobs": jobs})
}

func (s *Server) serviceUpdateHostBootstrapClaim(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	token, ok := s.authenticateService(w, r, "updates.claim")
	if !ok {
		return
	}
	if !runtimeSecretTransportAllowed(r) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "secure_transport_required"})
		return
	}
	var body struct {
		ServiceID               string `json:"service_id"`
		CurrentRevision         int64  `json:"current_revision"`
		RecipientKeyFingerprint string `json:"recipient_key_fingerprint"`
	}
	if !decodeSingleUpdateHostBootstrapJSON(w, r, &body) ||
		!validUpdateHostBootstrapServiceID(body.ServiceID) || body.CurrentRevision <= 0 ||
		!validUpdateHostBootstrapRecipientKeyFingerprint(body.RecipientKeyFingerprint) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "bad_request"})
		return
	}
	s.systemUpdateOperationMu.Lock()
	defer s.systemUpdateOperationMu.Unlock()
	token, ok = s.reauthenticateService(w, r, token, "updates.claim")
	if !ok {
		return
	}
	agent, policy, ok := s.updateHostBootstrapServicePolicy(w, r, token, body.ServiceID)
	if !ok {
		return
	}
	if body.CurrentRevision != policy.Revision {
		writeJSON(w, http.StatusConflict, map[string]string{"code": "bootstrap_policy_revision_mismatch"})
		return
	}
	_, currentRecipientKeyFingerprint := updateHostBootstrapEncryptionIdentity(agent)
	claim, err := s.updateHostBootstrapJobs.Claim(
		agent.ServiceID,
		policy.Revision,
		body.RecipientKeyFingerprint,
		currentRecipientKeyFingerprint,
	)
	if errors.Is(err, ErrUpdateHostBootstrapNotFound) || errors.Is(err, ErrUpdateHostBootstrapTransition) {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if err != nil {
		writeUpdateHostBootstrapBrokerError(w, err)
		return
	}
	defer wipeUpdateHostBootstrapBytes(claim.Envelope)

	releaseToken, err := s.secrets.GetSecretValue(r.Context(), store.UpdaterGitHubReleaseTokenSecretName)
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeJSON(w, http.StatusConflict, map[string]string{"code": "updater_release_token_not_configured"})
		return
	case err != nil:
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "get_updater_release_token_failed"})
		return
	}
	s.writeServiceAudit(r, token, "system_updates.bootstrap.claim", "update_host_bootstrap", claim.Job.ID, "success", map[string]any{
		"updater_id": agent.ServiceID, "expected_revision": claim.Job.ExpectedRevision, "host_count": len(claim.Job.HostIDs),
	})
	writeOneTimeSecretJSON(w, http.StatusOK, map[string]any{
		"id":                claim.Job.ID,
		"updater_id":        claim.Job.UpdaterID,
		"expected_revision": claim.Job.ExpectedRevision,
		"host_ids":          claim.Job.HostIDs,
		"envelope":          json.RawMessage(claim.Envelope),
		"lease_token":       claim.LeaseToken,
		"release_token":     releaseToken,
	})
}

func (s *Server) serviceUpdateHostBootstrapAccept(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	token, ok := s.authenticateService(w, r, "updates.report")
	if !ok {
		return
	}
	if !runtimeSecretTransportAllowed(r) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "secure_transport_required"})
		return
	}
	var body struct {
		ServiceID  string `json:"service_id"`
		LeaseToken string `json:"lease_token"`
	}
	if !decodeSingleUpdateHostBootstrapJSON(w, r, &body) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "bad_request"})
		return
	}
	jobID := r.PathValue("id")
	if !validUpdateHostBootstrapServiceID(body.ServiceID) ||
		!validUpdateHostBootstrapJobID(jobID) ||
		body.LeaseToken == "" || len(body.LeaseToken) > 256 || containsUpdateHostBootstrapControl(body.LeaseToken) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_bootstrap_job_request"})
		return
	}
	s.systemUpdateOperationMu.Lock()
	defer s.systemUpdateOperationMu.Unlock()
	token, ok = s.reauthenticateService(w, r, token, "updates.report")
	if !ok {
		return
	}
	agent, policy, ok := s.updateHostBootstrapServicePolicy(w, r, token, body.ServiceID)
	if !ok {
		return
	}
	if _, err := s.updateHostBootstrapJobs.Accept(jobID, agent.ServiceID, policy.Revision, body.LeaseToken); err != nil {
		writeUpdateHostBootstrapBrokerError(w, err)
		return
	}
	s.writeServiceAudit(r, token, "system_updates.bootstrap.accept", "update_host_bootstrap", jobID, "success", map[string]any{
		"updater_id": agent.ServiceID, "expected_revision": policy.Revision,
	})
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) serviceUpdateHostBootstrapReport(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	token, ok := s.authenticateService(w, r, "updates.report")
	if !ok {
		return
	}
	if !runtimeSecretTransportAllowed(r) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "secure_transport_required"})
		return
	}
	var body struct {
		ServiceID  string                        `json:"service_id"`
		LeaseToken string                        `json:"lease_token"`
		HostID     string                        `json:"host_id"`
		Status     UpdateHostBootstrapHostStatus `json:"status"`
		Progress   int                           `json:"progress"`
		Code       string                        `json:"code"`
		Message    string                        `json:"message"`
	}
	if !decodeSingleUpdateHostBootstrapJSON(w, r, &body) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "bad_request"})
		return
	}
	jobID := r.PathValue("id")
	if !validUpdateHostBootstrapServiceID(body.ServiceID) || !validUpdateHostBootstrapJobID(jobID) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_bootstrap_job_request"})
		return
	}
	s.systemUpdateOperationMu.Lock()
	defer s.systemUpdateOperationMu.Unlock()
	token, ok = s.reauthenticateService(w, r, token, "updates.report")
	if !ok {
		return
	}
	agent, policy, ok := s.updateHostBootstrapServicePolicy(w, r, token, body.ServiceID)
	if !ok {
		return
	}
	_, err := s.updateHostBootstrapJobs.Report(UpdateHostBootstrapReportParams{
		JobID: jobID, UpdaterID: agent.ServiceID, ExpectedRevision: policy.Revision,
		LeaseToken: body.LeaseToken, HostID: body.HostID, Status: body.Status,
		Progress: body.Progress, Code: body.Code, Message: body.Message,
	})
	if err != nil {
		writeUpdateHostBootstrapBrokerError(w, err)
		return
	}
	action := "system_updates.bootstrap.report"
	result := "success"
	switch body.Status {
	case UpdateHostBootstrapHostStatusSucceeded:
		action = "system_updates.bootstrap.succeeded"
	case UpdateHostBootstrapHostStatusFailed:
		action = "system_updates.bootstrap.failed"
		result = "failure"
	}
	s.writeServiceAudit(r, token, action, "update_host_bootstrap", jobID, result, map[string]any{
		"host_id": strings.TrimSpace(body.HostID), "status": body.Status, "code": strings.TrimSpace(body.Code),
	})
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) updateHostBootstrapServicePolicy(w http.ResponseWriter, r *http.Request, token store.ServiceToken, serviceID string) (store.RegisteredService, store.UpdaterPolicy, bool) {
	agent, err := s.systemUpdateAgentForToken(r.Context(), token, serviceID)
	if err != nil {
		writeSystemUpdateAgentError(w, err)
		return store.RegisteredService{}, store.UpdaterPolicy{}, false
	}
	policy, err := s.updaterPolicies.GetUpdaterPolicy(r.Context(), agent.ServiceID)
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeJSON(w, http.StatusConflict, map[string]string{"code": "updater_policy_not_configured"})
	case err != nil:
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "get_updater_policy_failed"})
	default:
		return agent, policy, true
	}
	return store.RegisteredService{}, store.UpdaterPolicy{}, false
}

func validateUpdateHostBootstrapHosts(policy store.UpdaterPolicy, requested []string) ([]string, error) {
	if len(requested) == 0 || len(requested) > UpdateHostBootstrapMaxHosts {
		return nil, errUpdateHostBootstrapHostSelection
	}
	policyHosts := make(map[string]store.UpdaterPolicyHost, len(policy.Hosts))
	for _, host := range policy.Hosts {
		policyHosts[host.HostID] = host
	}
	selected := make(map[string]bool, len(requested))
	hostIDs := make([]string, 0, len(requested))
	for _, rawHostID := range requested {
		hostID := strings.TrimSpace(rawHostID)
		host, exists := policyHosts[hostID]
		if !updateHostBootstrapIDPattern.MatchString(hostID) || selected[hostID] || !exists {
			return nil, errUpdateHostBootstrapHostSelection
		}
		if host.User != "autostream-update-host" {
			return nil, errUpdateHostBootstrapUnsupportedProfile
		}
		selected[hostID] = true
		hostIDs = append(hostIDs, hostID)
	}
	return hostIDs, nil
}

var (
	errUpdateHostBootstrapHostSelection      = errors.New("invalid bootstrap host selection")
	errUpdateHostBootstrapUnsupportedProfile = errors.New("unsupported bootstrap profile")
)

func validUpdateHostBootstrapJobID(value string) bool {
	if value != strings.TrimSpace(value) {
		return false
	}
	if len(value) != 36 {
		return false
	}
	for index, char := range value {
		switch index {
		case 8, 13, 18, 23:
			if char != '-' {
				return false
			}
		default:
			if !((char >= '0' && char <= '9') || (char >= 'a' && char <= 'f')) {
				return false
			}
		}
	}
	return true
}

func validUpdateHostBootstrapServiceID(value string) bool {
	return value == strings.TrimSpace(value) && len(value) <= 128 &&
		updateHostBootstrapIDPattern.MatchString(value)
}

func normalizeUpdateHostBootstrapEnvelope(input updateHostBootstrapEnvelopeRequest) ([]byte, error) {
	if input.EphemeralPublicKey != strings.TrimSpace(input.EphemeralPublicKey) ||
		input.Nonce != strings.TrimSpace(input.Nonce) ||
		input.Ciphertext != strings.TrimSpace(input.Ciphertext) {
		return nil, ErrUpdateHostBootstrapInvalid
	}
	publicKey, err := base64.RawURLEncoding.DecodeString(input.EphemeralPublicKey)
	if err != nil || input.Version != 1 || len(publicKey) != 65 || publicKey[0] != 0x04 ||
		base64.RawURLEncoding.EncodeToString(publicKey) != input.EphemeralPublicKey {
		return nil, ErrUpdateHostBootstrapInvalid
	}
	if _, err := ecdh.P256().NewPublicKey(publicKey); err != nil {
		return nil, ErrUpdateHostBootstrapInvalid
	}
	nonce, err := base64.RawURLEncoding.DecodeString(input.Nonce)
	if err != nil || len(nonce) != 12 || base64.RawURLEncoding.EncodeToString(nonce) != input.Nonce {
		return nil, ErrUpdateHostBootstrapInvalid
	}
	ciphertext, err := base64.RawURLEncoding.DecodeString(input.Ciphertext)
	if err != nil || len(ciphertext) < 16 || len(ciphertext) > UpdateHostBootstrapMaxEnvelopeBytes ||
		base64.RawURLEncoding.EncodeToString(ciphertext) != input.Ciphertext {
		return nil, ErrUpdateHostBootstrapInvalid
	}
	envelope, err := json.Marshal(input)
	if err != nil || len(envelope) > UpdateHostBootstrapMaxEnvelopeBytes {
		return nil, ErrUpdateHostBootstrapInvalid
	}
	return envelope, nil
}

func updateHostBootstrapEncryptionIdentity(agent store.RegisteredService) (string, string) {
	publicKey := capabilityString(agent.ReportedCapabilities["bootstrap_encryption_public_key"])
	fingerprint := capabilityString(agent.ReportedCapabilities["bootstrap_encryption_key_fingerprint"])
	if fingerprint == "" {
		fingerprint = capabilityString(agent.ReportedCapabilities["bootstrap_encryption_public_key_fingerprint"])
	}
	decoded, err := base64.RawURLEncoding.DecodeString(publicKey)
	if err != nil || len(decoded) != 65 || decoded[0] != 0x04 ||
		base64.RawURLEncoding.EncodeToString(decoded) != publicKey {
		return "", ""
	}
	if _, err := ecdh.P256().NewPublicKey(decoded); err != nil {
		return "", ""
	}
	digest := sha256.Sum256(decoded)
	expectedFingerprint := "SHA256:" + base64.RawStdEncoding.EncodeToString(digest[:])
	if fingerprint != expectedFingerprint {
		return "", ""
	}
	return publicKey, expectedFingerprint
}

func updateHostBootstrapUpdaterConfigurationPending(
	agent store.RegisteredService,
) bool {
	return agent.StagedNodeTokenID != "" &&
		agent.StagedNodeTokenID != agent.TokenID
}

func decodeSingleUpdateHostBootstrapJSON(w http.ResponseWriter, r *http.Request, out any) bool {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, updateHostBootstrapRequestMaxBytes))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(out); err != nil {
		return false
	}
	return errors.Is(decoder.Decode(&struct{}{}), io.EOF)
}

func writeUpdateHostBootstrapValidationError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, errUpdateHostBootstrapUnsupportedProfile):
		writeJSON(w, http.StatusConflict, map[string]string{"code": "unsupported_bootstrap_profile"})
	default:
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_bootstrap_host_selection"})
	}
}

func writeUpdateHostBootstrapBrokerError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrUpdateHostBootstrapNotFound):
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "bootstrap_job_not_found"})
	case errors.Is(err, ErrUpdateHostBootstrapActiveJob), errors.Is(err, ErrUpdateHostBootstrapIdempotencyConflict):
		writeJSON(w, http.StatusConflict, map[string]string{"code": "bootstrap_job_conflict"})
	case errors.Is(err, ErrUpdateHostBootstrapBinding):
		writeJSON(w, http.StatusConflict, map[string]string{"code": "bootstrap_policy_revision_mismatch"})
	case errors.Is(err, ErrUpdateHostBootstrapRecipientKeyMismatch):
		writeJSON(w, http.StatusConflict, map[string]string{"code": "bootstrap_recipient_key_changed"})
	case errors.Is(err, ErrUpdateHostBootstrapLeaseInvalid):
		writeJSON(w, http.StatusConflict, map[string]string{"code": "bootstrap_lease_invalid"})
	case errors.Is(err, ErrUpdateHostBootstrapTransition):
		writeJSON(w, http.StatusConflict, map[string]string{"code": "bootstrap_job_conflict"})
	case errors.Is(err, ErrUpdateHostBootstrapInvalid):
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_bootstrap_job_request"})
	default:
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "bootstrap_job_operation_failed"})
	}
}
