package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/example/autostream-control-panel/internal/security"
	"github.com/example/autostream-control-panel/internal/store"
)

func (s *Server) mfaStatus(w http.ResponseWriter, r *http.Request) {
	current := currentFromContext(r.Context())
	settings, err := s.settings.GetSecuritySettings(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "security_settings_unavailable"})
		return
	}
	if s.mfa == nil {
		writeJSON(w, http.StatusOK, map[string]any{
			"available":          false,
			"enabled":            false,
			"pending_enrollment": false,
			"policy_mode":        settings.MFAMode,
			"required":           mfaRequiredForUser(settings, current.User),
		})
		return
	}
	cfg, err := s.mfa.GetMFAConfig(r.Context(), current.User.ID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "mfa_state_unavailable"})
		return
	}
	method := ""
	if cfg.Enabled || strings.TrimSpace(cfg.PendingTOTPSecret) != "" {
		method = "totp"
	}
	response := map[string]any{
		"available":           true,
		"enabled":             cfg.Enabled,
		"method":              method,
		"pending_enrollment":  strings.TrimSpace(cfg.PendingTOTPSecret) != "",
		"recovery_code_count": len(cfg.RecoveryCodeHashes),
		"policy_mode":         settings.MFAMode,
		"required":            cfg.Enabled || mfaRequiredForUser(settings, current.User),
	}
	if !cfg.UpdatedAt.IsZero() {
		response["updated_at"] = cfg.UpdatedAt
	}
	writeJSON(w, http.StatusOK, response)
}

func (s *Server) mfaEnroll(w http.ResponseWriter, r *http.Request) {
	current := currentFromContext(r.Context())
	if !s.mfaAvailable(w, r, "mfa.enroll") {
		return
	}
	var body struct {
		Code string `json:"code"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil && !errors.Is(err, io.EOF) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "bad_request"})
		return
	}
	settings, err := s.settings.GetSecuritySettings(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "security_settings_unavailable"})
		return
	}
	if settings.MFAMode == "passkey" {
		code := "totp_mfa_unavailable"
		s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "mfa.enroll", ResourceType: "mfa", ResourceID: current.User.ID, Result: "failure", Metadata: map[string]any{"reason": code, "mfa_mode": settings.MFAMode}})
		writeJSON(w, http.StatusConflict, map[string]string{"code": code})
		return
	}
	cfg, err := s.mfa.GetMFAConfig(r.Context(), current.User.ID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "mfa_state_unavailable"})
		return
	}
	if cfg.Enabled {
		attemptKey := loginFailureKey("mfa:enroll:"+current.User.ID, clientIP(r))
		if !s.loginFailures.allow(attemptKey, sensitiveActionAttemptThreshold) {
			w.Header().Set("Retry-After", "300")
			writeJSON(w, http.StatusTooManyRequests, map[string]string{"code": "mfa_rate_limited"})
			return
		}
		if _, ok := s.verifyMFAConfigCode(r.Context(), current.User.ID, cfg, body.Code); !ok {
			s.loginFailures.record(attemptKey)
			s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "mfa.enroll", ResourceType: "mfa", ResourceID: current.User.ID, Result: "failure", Metadata: map[string]any{"reason": "invalid_current_code"}})
			writeJSON(w, http.StatusUnauthorized, map[string]string{"code": "invalid_mfa_code"})
			return
		}
		s.loginFailures.clear(attemptKey)
	}
	secret, err := security.GenerateTOTPSecret()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "mfa_secret_failed"})
		return
	}
	recoveryCodes, hashes, err := generateRecoveryCodesAndHashes()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "recovery_codes_failed"})
		return
	}
	if err := s.mfa.StartTOTPEnrollment(r.Context(), current.User.ID, secret, hashes); err != nil {
		code := "mfa_enroll_failed"
		if errors.Is(err, store.ErrSecretKeyRequired) {
			code = "secret_encryption_key_required"
		}
		s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "mfa.enroll", ResourceType: "mfa", ResourceID: current.User.ID, Result: "failure", Metadata: map[string]any{"reason": code}})
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"code": code})
		return
	}
	s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "mfa.enroll", ResourceType: "mfa", ResourceID: current.User.ID, Result: "success", Metadata: map[string]any{"method": "totp", "recovery_codes_issued": len(recoveryCodes)}})
	writeOneTimeSecretJSON(w, http.StatusOK, map[string]any{
		"method":           "totp",
		"secret":           secret,
		"provisioning_uri": security.ProvisioningURI("AutoStream", current.User.Username, secret),
		"recovery_codes":   recoveryCodes,
		"message":          "Verify a TOTP code to enable MFA. Recovery codes are shown only once.",
	})
}

func (s *Server) mfaVerify(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ChallengeToken string `json:"challenge_token"`
		Code           string `json:"code"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "bad_request"})
		return
	}
	// A login challenge is explicit in the request body.  Prefer it over a
	// possibly stale session cookie so that re-login/MFA verification does not
	// get misclassified as an authenticated enrollment request and rejected by
	// the old session's CSRF token.
	if strings.TrimSpace(body.ChallengeToken) != "" {
		s.mfaVerifyLoginChallenge(w, r, body.ChallengeToken, body.Code)
		return
	}
	if current, ok := s.authenticate(r); ok {
		if isUnsafeMethod(r.Method) && !security.VerifyTokenHash(r.Header.Get("X-CSRF-Token"), current.Session.CSRFTokenHash) {
			writeJSON(w, http.StatusForbidden, map[string]string{"code": "csrf_failed"})
			return
		}
		s.mfaVerifyEnrollment(w, r, current, body.Code)
		return
	}
	s.mfaVerifyLoginChallenge(w, r, "", body.Code)
}

func (s *Server) mfaVerifyEnrollment(w http.ResponseWriter, r *http.Request, current currentUser, code string) {
	if !s.mfaAvailable(w, r, "mfa.verify") {
		return
	}
	cfg, err := s.mfa.GetMFAConfig(r.Context(), current.User.ID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "mfa_state_unavailable"})
		return
	}
	if cfg.PendingTOTPSecret == "" {
		writeJSON(w, http.StatusConflict, map[string]string{"code": "mfa_enrollment_not_pending"})
		return
	}
	attemptKey := loginFailureKey("mfa:verify:"+current.User.ID, clientIP(r))
	if !s.loginFailures.allow(attemptKey, sensitiveActionAttemptThreshold) {
		w.Header().Set("Retry-After", "300")
		writeJSON(w, http.StatusTooManyRequests, map[string]string{"code": "mfa_rate_limited"})
		return
	}
	if !security.VerifyTOTP(cfg.PendingTOTPSecret, code, time.Now()) {
		s.loginFailures.record(attemptKey)
		s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "mfa.verify", ResourceType: "mfa", ResourceID: current.User.ID, Result: "failure", Metadata: map[string]any{"reason": "invalid_code"}})
		writeJSON(w, http.StatusUnauthorized, map[string]string{"code": "invalid_mfa_code"})
		return
	}
	if err := s.mfa.ConfirmTOTPEnrollment(r.Context(), current.User.ID); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "mfa_verify_failed"})
		return
	}
	s.loginFailures.clear(attemptKey)
	s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "mfa.verify", ResourceType: "mfa", ResourceID: current.User.ID, Result: "success", Metadata: map[string]any{"method": "totp"}})
	writeJSON(w, http.StatusOK, map[string]string{"status": "mfa_enabled", "method": "totp"})
}

func (s *Server) mfaVerifyLoginChallenge(w http.ResponseWriter, r *http.Request, challengeToken, code string) {
	if s.mfa == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"code": "mfa_not_configured"})
		return
	}
	challenge, err := s.mfa.GetMFAChallenge(r.Context(), strings.TrimSpace(challengeToken))
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"code": "invalid_mfa_challenge"})
		return
	}
	user, err := s.auth.GetUser(r.Context(), challenge.UserID)
	if err != nil || user.Status == "disabled" || user.Status == "locked" {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"code": "invalid_mfa_challenge"})
		return
	}
	cfg, err := s.mfa.GetMFAConfig(r.Context(), user.ID)
	if err != nil || !cfg.Enabled {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"code": "mfa_not_enabled"})
		return
	}
	settings, err := s.settings.GetSecuritySettings(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "security_settings_unavailable"})
		return
	}
	usedRecovery, ok := s.verifyMFAConfigCode(r.Context(), user.ID, cfg, code)
	if !ok {
		_ = s.mfa.DeleteMFAChallenge(r.Context(), challengeToken)
		_ = s.auth.RecordLoginFailure(r.Context(), user.Username, settings.LoginLockoutThreshold)
		s.writeAudit(r, store.AuditEvent{ActorUserID: user.ID, ActorUsername: user.Username, Action: "mfa.verify", ResourceType: "mfa", ResourceID: user.ID, Result: "failure", Metadata: map[string]any{"reason": "invalid_code"}})
		writeJSON(w, http.StatusUnauthorized, map[string]string{"code": "invalid_mfa_code"})
		return
	}
	_ = s.mfa.DeleteMFAChallenge(r.Context(), challengeToken)
	s.writeAudit(r, store.AuditEvent{ActorUserID: user.ID, ActorUsername: user.Username, Action: "mfa.verify", ResourceType: "mfa", ResourceID: user.ID, Result: "success", Metadata: map[string]any{"method": map[bool]string{true: "recovery_code", false: "totp"}[usedRecovery]}})
	s.completeLogin(w, r, user, settings)
}

func (s *Server) mfaDisable(w http.ResponseWriter, r *http.Request) {
	current := currentFromContext(r.Context())
	if !s.mfaAvailable(w, r, "mfa.disable") {
		return
	}
	var body struct {
		Code string `json:"code"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "bad_request"})
		return
	}
	cfg, err := s.mfa.GetMFAConfig(r.Context(), current.User.ID)
	if err != nil || !cfg.Enabled {
		writeJSON(w, http.StatusConflict, map[string]string{"code": "mfa_not_enabled"})
		return
	}
	attemptKey := loginFailureKey("mfa:disable:"+current.User.ID, clientIP(r))
	if !s.loginFailures.allow(attemptKey, sensitiveActionAttemptThreshold) {
		w.Header().Set("Retry-After", "300")
		writeJSON(w, http.StatusTooManyRequests, map[string]string{"code": "mfa_rate_limited"})
		return
	}
	if _, ok := s.verifyMFAConfigCode(r.Context(), current.User.ID, cfg, body.Code); !ok {
		s.loginFailures.record(attemptKey)
		s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "mfa.disable", ResourceType: "mfa", ResourceID: current.User.ID, Result: "failure", Metadata: map[string]any{"reason": "invalid_code"}})
		writeJSON(w, http.StatusUnauthorized, map[string]string{"code": "invalid_mfa_code"})
		return
	}
	if err := s.mfa.DisableMFA(r.Context(), current.User.ID); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "mfa_disable_failed"})
		return
	}
	s.loginFailures.clear(attemptKey)
	s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "mfa.disable", ResourceType: "mfa", ResourceID: current.User.ID, Result: "success"})
	writeJSON(w, http.StatusOK, map[string]string{"status": "mfa_disabled"})
}

func (s *Server) mfaRegenerateRecoveryCodes(w http.ResponseWriter, r *http.Request) {
	current := currentFromContext(r.Context())
	if !s.mfaAvailable(w, r, "mfa.recovery_codes.regenerate") {
		return
	}
	var body struct {
		Code string `json:"code"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "bad_request"})
		return
	}
	cfg, err := s.mfa.GetMFAConfig(r.Context(), current.User.ID)
	if err != nil || !cfg.Enabled {
		writeJSON(w, http.StatusConflict, map[string]string{"code": "mfa_not_enabled"})
		return
	}
	attemptKey := loginFailureKey("mfa:recovery-regenerate:"+current.User.ID, clientIP(r))
	if !s.loginFailures.allow(attemptKey, sensitiveActionAttemptThreshold) {
		w.Header().Set("Retry-After", "300")
		writeJSON(w, http.StatusTooManyRequests, map[string]string{"code": "mfa_rate_limited"})
		return
	}
	if _, ok := s.verifyMFAConfigCode(r.Context(), current.User.ID, cfg, body.Code); !ok {
		s.loginFailures.record(attemptKey)
		s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "mfa.recovery_codes.regenerate", ResourceType: "mfa", ResourceID: current.User.ID, Result: "failure", Metadata: map[string]any{"reason": "invalid_code"}})
		writeJSON(w, http.StatusUnauthorized, map[string]string{"code": "invalid_mfa_code"})
		return
	}
	codes, hashes, err := generateRecoveryCodesAndHashes()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "recovery_codes_failed"})
		return
	}
	if err := s.mfa.RegenerateRecoveryCodes(r.Context(), current.User.ID, hashes); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "recovery_codes_failed"})
		return
	}
	s.loginFailures.clear(attemptKey)
	s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "mfa.recovery_codes.regenerate", ResourceType: "mfa", ResourceID: current.User.ID, Result: "success", Metadata: map[string]any{"recovery_codes_issued": len(codes)}})
	writeOneTimeSecretJSON(w, http.StatusOK, map[string]any{"recovery_codes": codes, "message": "Recovery codes are shown only once."})
}

func (s *Server) verifyMFAConfigCode(ctx context.Context, userID string, cfg store.MFAConfig, code string) (bool, bool) {
	if security.VerifyTOTP(cfg.TOTPSecret, code, time.Now()) {
		return false, true
	}
	hash := security.HashRecoveryCode(code)
	for _, candidate := range cfg.RecoveryCodeHashes {
		if candidate == hash {
			if err := s.mfa.ConsumeRecoveryCode(ctx, userID, hash); err != nil {
				return false, false
			}
			return true, true
		}
	}
	return false, false
}

func (s *Server) mfaAvailable(w http.ResponseWriter, r *http.Request, action string) bool {
	if s.mfa != nil {
		return true
	}
	current := currentFromContext(r.Context())
	s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: action, ResourceType: "mfa", ResourceID: current.User.ID, Result: "failure", Metadata: map[string]any{"reason": "mfa_not_configured"}})
	writeJSON(w, http.StatusServiceUnavailable, map[string]string{"code": "mfa_not_configured"})
	return false
}

func (s *Server) listPasskeys(w http.ResponseWriter, r *http.Request) {
	if !s.passkeysAvailable(w, r, "passkeys.list") {
		return
	}
	current := currentFromContext(r.Context())
	credentials, err := s.passkeys.ListPasskeyCredentials(r.Context(), current.User.ID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "passkeys_unavailable"})
		return
	}
	writeJSON(w, http.StatusOK, credentials)
}

func (s *Server) deletePasskey(w http.ResponseWriter, r *http.Request) {
	if !s.passkeysAvailable(w, r, "passkeys.delete") {
		return
	}
	current := currentFromContext(r.Context())
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "passkey_id_required"})
		return
	}
	if err := s.passkeys.DeletePasskeyCredential(r.Context(), current.User.ID, id); errors.Is(err, store.ErrNotFound) {
		s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "passkeys.delete", ResourceType: "passkey", ResourceID: id, Result: "failure", Metadata: map[string]any{"reason": "not_found"}})
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found"})
		return
	} else if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "passkey_delete_failed"})
		return
	}
	s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "passkeys.delete", ResourceType: "passkey", ResourceID: id, Result: "success"})
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) passkeysAvailable(w http.ResponseWriter, r *http.Request, action string) bool {
	if s.passkeys != nil {
		return true
	}
	current := currentFromContext(r.Context())
	s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: action, ResourceType: "passkey", ResourceID: current.User.ID, Result: "failure", Metadata: map[string]any{"reason": "passkeys_not_configured"}})
	writeJSON(w, http.StatusServiceUnavailable, map[string]string{"code": "passkeys_not_configured"})
	return false
}

type passkeyRegistrationStartRequest struct {
	DisplayName string `json:"display_name"`
}

type passkeyRegistrationStartResponse struct {
	RegistrationToken string    `json:"registration_token"`
	ExpiresAt         time.Time `json:"expires_at"`
	PublicKey         any       `json:"public_key"`
}

type passkeyCreationOptionsJSON struct {
	Challenge              string                        `json:"challenge"`
	RP                     passkeyRelyingPartyJSON       `json:"rp"`
	User                   passkeyUserJSON               `json:"user"`
	PubKeyCredParams       []passkeyCredentialParamJSON  `json:"pubKeyCredParams"`
	Timeout                int                           `json:"timeout"`
	Attestation            string                        `json:"attestation"`
	AuthenticatorSelection passkeyAuthenticatorSelection `json:"authenticatorSelection"`
}

type passkeyRelyingPartyJSON struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type passkeyUserJSON struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	DisplayName string `json:"displayName"`
}

type passkeyCredentialParamJSON struct {
	Type string `json:"type"`
	Alg  int    `json:"alg"`
}

type passkeyAuthenticatorSelection struct {
	ResidentKey      string `json:"residentKey"`
	UserVerification string `json:"userVerification"`
}

func (s *Server) startPasskeyRegistration(w http.ResponseWriter, r *http.Request) {
	if !s.passkeysAvailable(w, r, "passkeys.registration.start") {
		return
	}
	current := currentFromContext(r.Context())
	var input passkeyRegistrationStartRequest
	if r.Body != nil {
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil && !errors.Is(err, io.EOF) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"code": "bad_request"})
			return
		}
	}
	rpID, rpName := passkeyRelyingParty(r)
	displayName := strings.TrimSpace(input.DisplayName)
	if displayName == "" {
		displayName = current.User.Username
	}
	response, err := s.beginPasskeyRegistration(r, current.User, displayName, rpID, rpName)
	if err != nil {
		s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "passkeys.registration.start", ResourceType: "passkey", ResourceID: current.User.ID, Result: "failure", Metadata: map[string]any{"reason": "challenge_create_failed"}})
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "passkey_registration_challenge_failed"})
		return
	}
	s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "passkeys.registration.start", ResourceType: "passkey", ResourceID: current.User.ID, Result: "success", Metadata: map[string]any{"rp_id": rpID}})
	writeOneTimeSecretJSON(w, http.StatusCreated, response)
}

func passkeyRelyingParty(r *http.Request) (string, string) {
	rpName := strings.TrimSpace(os.Getenv("AUTOSTREAM_WEBAUTHN_RP_NAME"))
	if rpName == "" {
		rpName = "AutoStream Control Panel"
	}
	if rpID := strings.TrimSpace(os.Getenv("AUTOSTREAM_WEBAUTHN_RP_ID")); rpID != "" {
		return rpID, rpName
	}
	if publicURL := strings.TrimSpace(os.Getenv("AUTOSTREAM_PUBLIC_URL")); publicURL != "" {
		if parsed, err := url.Parse(publicURL); err == nil && parsed.Hostname() != "" {
			return parsed.Hostname(), rpName
		}
	}
	if host := strings.TrimSpace(r.Host); host != "" {
		if parsedHost, _, err := net.SplitHostPort(host); err == nil && parsedHost != "" {
			return parsedHost, rpName
		}
		return strings.TrimPrefix(host, "["), rpName
	}
	return "localhost", rpName
}

func generateRecoveryCodesAndHashes() ([]string, []string, error) {
	codes, err := security.GenerateRecoveryCodes(10)
	if err != nil {
		return nil, nil, err
	}
	hashes := make([]string, 0, len(codes))
	for _, code := range codes {
		hashes = append(hashes, security.HashRecoveryCode(code))
	}
	return codes, hashes, nil
}
