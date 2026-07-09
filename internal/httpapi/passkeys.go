package httpapi

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/example/autostream-control-panel/internal/store"
	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"
)

const passkeyCeremonyTTL = 10 * time.Minute

var errPasskeyProductionConfigRequired = errors.New("passkey production rp configuration required")

type passkeyRegistrationFinishRequest struct {
	RegistrationToken string          `json:"registration_token"`
	Name              string          `json:"name"`
	Credential        json.RawMessage `json:"credential"`
}

type passkeyLoginStartRequest struct {
	Username string `json:"username"`
}

type passkeyLoginStartResponse struct {
	ChallengeToken string    `json:"challenge_token"`
	ExpiresAt      time.Time `json:"expires_at"`
	PublicKey      any       `json:"public_key"`
}

type passkeyLoginFinishRequest struct {
	ChallengeToken string          `json:"challenge_token"`
	Credential     json.RawMessage `json:"credential"`
}

type webauthnUser struct {
	id          []byte
	name        string
	displayName string
	credentials []webauthn.Credential
}

func (u webauthnUser) WebAuthnID() []byte {
	return append([]byte(nil), u.id...)
}

func (u webauthnUser) WebAuthnName() string {
	return u.name
}

func (u webauthnUser) WebAuthnDisplayName() string {
	if u.displayName != "" {
		return u.displayName
	}
	return u.name
}

func (u webauthnUser) WebAuthnCredentials() []webauthn.Credential {
	return append([]webauthn.Credential(nil), u.credentials...)
}

func (s *Server) beginPasskeyRegistration(r *http.Request, user store.User, displayName, rpID, rpName string) (passkeyRegistrationStartResponse, error) {
	credentials, err := s.passkeys.ListPasskeyCredentialsForVerification(r.Context(), user.ID)
	if err != nil {
		return passkeyRegistrationStartResponse{}, err
	}
	wa, err := passkeyRuntime(r, rpID, rpName)
	if err != nil {
		return passkeyRegistrationStartResponse{}, err
	}
	creation, session, err := wa.BeginRegistration(webauthnUser{
		id:          []byte(user.ID),
		name:        user.Username,
		displayName: displayName,
		credentials: webauthnCredentials(credentials),
	})
	if err != nil {
		return passkeyRegistrationStartResponse{}, err
	}
	sessionJSON, err := json.Marshal(session)
	if err != nil {
		return passkeyRegistrationStartResponse{}, err
	}
	saved, err := s.passkeys.CreatePasskeyCeremonySession(r.Context(), user.ID, "registration", sessionJSON, passkeyCeremonyTTL)
	if err != nil {
		return passkeyRegistrationStartResponse{}, err
	}
	return passkeyRegistrationStartResponse{
		RegistrationToken: saved.Token,
		ExpiresAt:         saved.ExpiresAt,
		PublicKey:         creation.Response,
	}, nil
}

func (s *Server) finishPasskeyRegistration(w http.ResponseWriter, r *http.Request) {
	if !s.passkeysAvailable(w, r, "passkeys.registration.finish") {
		return
	}
	current := currentFromContext(r.Context())
	var body passkeyRegistrationFinishRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "bad_request"})
		return
	}
	body.RegistrationToken = strings.TrimSpace(body.RegistrationToken)
	if body.RegistrationToken == "" || len(body.Credential) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "passkey_registration_response_required"})
		return
	}
	sessionRecord, err := s.passkeys.ConsumePasskeyCeremonySession(r.Context(), body.RegistrationToken, "registration")
	if errors.Is(err, store.ErrNotFound) {
		s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "passkeys.registration.finish", ResourceType: "passkey", ResourceID: current.User.ID, Result: "failure", Metadata: map[string]any{"reason": "invalid_or_expired_challenge"}})
		writeJSON(w, http.StatusUnauthorized, map[string]string{"code": "invalid_passkey_registration_challenge"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "passkey_registration_challenge_unavailable"})
		return
	}
	if sessionRecord.UserID != current.User.ID {
		s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "passkeys.registration.finish", ResourceType: "passkey", ResourceID: current.User.ID, Result: "failure", Metadata: map[string]any{"reason": "challenge_user_mismatch"}})
		writeJSON(w, http.StatusUnauthorized, map[string]string{"code": "invalid_passkey_registration_challenge"})
		return
	}
	var session webauthn.SessionData
	if err := json.Unmarshal(sessionRecord.SessionJSON, &session); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "passkey_registration_session_invalid"})
		return
	}
	rpID, rpName := passkeyRelyingParty(r)
	wa, err := passkeyRuntime(r, rpID, rpName)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "passkey_runtime_unavailable"})
		return
	}
	credentialRequest, err := passkeyCredentialRequest(r, body.Credential)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "bad_request"})
		return
	}
	credential, err := wa.FinishRegistration(webauthnUser{
		id:          []byte(current.User.ID),
		name:        current.User.Username,
		displayName: current.User.Username,
	}, session, credentialRequest)
	if err != nil {
		s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "passkeys.registration.finish", ResourceType: "passkey", ResourceID: current.User.ID, Result: "failure", Metadata: map[string]any{"reason": "credential_verification_failed"}})
		writeJSON(w, http.StatusUnauthorized, map[string]string{"code": "passkey_registration_verification_failed"})
		return
	}
	name := strings.TrimSpace(body.Name)
	if name == "" {
		name = "Passkey"
	}
	created, err := s.passkeys.CreatePasskeyCredential(r.Context(), storeCredentialFromWebAuthn(current.User.ID, name, *credential))
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "passkey_create_failed"})
		return
	}
	s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "passkeys.registration.finish", ResourceType: "passkey", ResourceID: created.ID, Result: "success"})
	writeJSON(w, http.StatusCreated, created)
}

func (s *Server) startPasskeyLogin(w http.ResponseWriter, r *http.Request) {
	if s.auth == nil || s.passkeys == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"code": "passkeys_not_configured"})
		return
	}
	var body passkeyLoginStartRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil && !errors.Is(err, io.EOF) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "bad_request"})
		return
	}
	username := strings.TrimSpace(body.Username)
	failureKeyName := username
	if failureKeyName == "" {
		failureKeyName = "passkey"
	}
	failureKey := loginFailureKey("passkey:start:"+failureKeyName, clientIP(r))
	if !s.loginFailures.allow(failureKey, sensitiveActionAttemptThreshold) {
		w.Header().Set("Retry-After", "300")
		writeJSON(w, http.StatusTooManyRequests, map[string]string{"code": "login_rate_limited"})
		return
	}
	rpID, rpName := passkeyRelyingParty(r)
	wa, err := passkeyRuntime(r, rpID, rpName)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "passkey_runtime_unavailable"})
		return
	}
	assertion, session, err := wa.BeginDiscoverableLogin(webauthn.WithUserVerification(protocol.VerificationRequired))
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "passkey_login_challenge_failed"})
		return
	}
	sessionJSON, err := json.Marshal(session)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "passkey_login_challenge_failed"})
		return
	}
	saved, err := s.passkeys.CreatePasskeyCeremonySession(r.Context(), "", "login", sessionJSON, passkeyCeremonyTTL)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "passkey_login_challenge_failed"})
		return
	}
	s.loginFailures.record(failureKey)
	s.writeAudit(r, store.AuditEvent{Action: "auth.passkey.login.start", ResourceType: "passkey", Result: "success"})
	writeOneTimeSecretJSON(w, http.StatusOK, passkeyLoginStartResponse{ChallengeToken: saved.Token, ExpiresAt: saved.ExpiresAt, PublicKey: assertion.Response})
}

func (s *Server) finishPasskeyLogin(w http.ResponseWriter, r *http.Request) {
	if s.auth == nil || s.passkeys == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"code": "passkeys_not_configured"})
		return
	}
	var body passkeyLoginFinishRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "bad_request"})
		return
	}
	body.ChallengeToken = strings.TrimSpace(body.ChallengeToken)
	if body.ChallengeToken == "" || len(body.Credential) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "passkey_login_response_required"})
		return
	}
	sessionRecord, err := s.passkeys.ConsumePasskeyCeremonySession(r.Context(), body.ChallengeToken, "login")
	if errors.Is(err, store.ErrNotFound) {
		s.writeAudit(r, store.AuditEvent{Action: "auth.passkey.login.finish", ResourceType: "passkey", Result: "failure", Metadata: map[string]any{"reason": "invalid_or_expired_challenge"}})
		writeJSON(w, http.StatusUnauthorized, map[string]string{"code": "invalid_passkey_login_challenge"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "passkey_login_challenge_unavailable"})
		return
	}
	var session webauthn.SessionData
	if err := json.Unmarshal(sessionRecord.SessionJSON, &session); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "passkey_login_session_invalid"})
		return
	}
	rpID, rpName := passkeyRelyingParty(r)
	wa, err := passkeyRuntime(r, rpID, rpName)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "passkey_runtime_unavailable"})
		return
	}
	credentialRequest, err := passkeyCredentialRequest(r, body.Credential)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "bad_request"})
		return
	}
	var user store.User
	verified, err := wa.FinishDiscoverableLogin(func(rawID, userHandle []byte) (webauthn.User, error) {
		stored, err := s.passkeys.FindPasskeyCredentialByCredentialID(r.Context(), rawID)
		if err != nil {
			return nil, err
		}
		candidate, err := s.auth.GetUser(r.Context(), stored.UserID)
		if err != nil || candidate.Status == "disabled" || candidate.Status == "locked" {
			return nil, store.ErrNotFound
		}
		credentials, err := s.passkeys.ListPasskeyCredentialsForVerification(r.Context(), candidate.ID)
		if err != nil {
			return nil, err
		}
		user = candidate
		return webauthnUser{
			id:          []byte(candidate.ID),
			name:        candidate.Username,
			displayName: candidate.Username,
			credentials: webauthnCredentials(credentials),
		}, nil
	}, session, credentialRequest)
	if err != nil {
		s.loginFailures.record(loginFailureKey("passkey", clientIP(r)))
		s.writeAudit(r, store.AuditEvent{Action: "auth.passkey.login.finish", ResourceType: "passkey", Result: "failure", Metadata: map[string]any{"reason": "credential_verification_failed"}})
		writeJSON(w, http.StatusUnauthorized, map[string]string{"code": "invalid_credentials"})
		return
	}
	if user.ID == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"code": "invalid_credentials"})
		return
	}
	stored, err := s.passkeys.FindPasskeyCredentialByCredentialID(r.Context(), verified.ID)
	if err == nil && stored.UserID == user.ID {
		if err := s.passkeys.UpdatePasskeySignCount(r.Context(), stored.ID, verified.Authenticator.SignCount); err != nil {
			s.writeAudit(r, store.AuditEvent{ActorUserID: user.ID, ActorUsername: user.Username, Action: "auth.passkey.login.finish", ResourceType: "passkey", ResourceID: stored.ID, Result: "failure", Metadata: map[string]any{"reason": "sign_count_update_failed"}})
			writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "passkey_sign_count_update_failed"})
			return
		}
	} else {
		s.writeAudit(r, store.AuditEvent{ActorUserID: user.ID, ActorUsername: user.Username, Action: "auth.passkey.login.finish", ResourceType: "passkey", Result: "failure", Metadata: map[string]any{"reason": "credential_lookup_failed"}})
		writeJSON(w, http.StatusUnauthorized, map[string]string{"code": "invalid_credentials"})
		return
	}
	settings, err := s.settings.GetSecuritySettings(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "security_settings_unavailable"})
		return
	}
	mfaConfig, mfaRequired, err := s.loginMFARequirement(r.Context(), settings, user)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "mfa_state_unavailable"})
		return
	}
	if mfaRequired {
		if s.mfa == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"code": "mfa_not_configured"})
			return
		}
		if mfaConfig.Enabled {
			challenge, err := s.mfa.CreateMFAChallenge(r.Context(), user.ID, passkeyCeremonyTTL)
			if err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "mfa_challenge_failed"})
				return
			}
			s.writeAudit(r, store.AuditEvent{ActorUserID: user.ID, ActorUsername: user.Username, Action: "auth.passkey.login.finish", ResourceType: "user", ResourceID: user.ID, Result: "mfa_required"})
			writeJSON(w, http.StatusAccepted, map[string]any{"mfa_required": true, "challenge_token": challenge.Token, "expires_at": challenge.ExpiresAt})
			return
		}
		s.writeAudit(r, store.AuditEvent{ActorUserID: user.ID, ActorUsername: user.Username, Action: "auth.passkey.login.finish", ResourceType: "user", ResourceID: user.ID, Result: "failure", Metadata: map[string]any{"reason": "mfa_enrollment_required"}})
		writeJSON(w, http.StatusForbidden, map[string]string{"code": "mfa_enrollment_required"})
		return
	}
	s.writeAudit(r, store.AuditEvent{ActorUserID: user.ID, ActorUsername: user.Username, Action: "auth.passkey.login.finish", ResourceType: "user", ResourceID: user.ID, Result: "success"})
	s.completeLogin(w, r, user, settings)
}

func passkeyRuntime(r *http.Request, rpID, rpName string) (*webauthn.WebAuthn, error) {
	if productionEnvironment() && !passkeyProductionRPConfigured() {
		return nil, errPasskeyProductionConfigRequired
	}
	origins, err := passkeyOrigins(r)
	if err != nil {
		return nil, err
	}
	return webauthn.New(&webauthn.Config{
		RPID:          rpID,
		RPDisplayName: rpName,
		RPOrigins:     origins,
		AuthenticatorSelection: protocol.AuthenticatorSelection{
			ResidentKey:      protocol.ResidentKeyRequirementPreferred,
			UserVerification: protocol.VerificationRequired,
		},
	})
}

func passkeyProductionRPConfigured() bool {
	if strings.TrimSpace(os.Getenv("AUTOSTREAM_WEBAUTHN_RP_ID")) != "" {
		if strings.TrimSpace(os.Getenv("AUTOSTREAM_WEBAUTHN_RP_ORIGINS")) != "" || strings.TrimSpace(os.Getenv("AUTOSTREAM_PUBLIC_URL")) != "" {
			return true
		}
	}
	if publicURL := strings.TrimSpace(os.Getenv("AUTOSTREAM_PUBLIC_URL")); publicURL != "" {
		if parsed, err := url.Parse(publicURL); err == nil && parsed.Scheme != "" && parsed.Host != "" && parsed.Hostname() != "" {
			return true
		}
	}
	return false
}

func passkeyOrigins(r *http.Request) ([]string, error) {
	if raw := strings.TrimSpace(os.Getenv("AUTOSTREAM_WEBAUTHN_RP_ORIGINS")); raw != "" {
		out := []string{}
		for _, origin := range strings.Split(raw, ",") {
			if origin = strings.TrimSpace(origin); origin != "" {
				out = append(out, origin)
			}
		}
		if len(out) > 0 {
			return out, nil
		}
	}
	if publicURL := strings.TrimSpace(os.Getenv("AUTOSTREAM_PUBLIC_URL")); publicURL != "" {
		if parsed, err := url.Parse(publicURL); err == nil && parsed.Scheme != "" && parsed.Host != "" {
			return []string{parsed.Scheme + "://" + parsed.Host}, nil
		}
	}
	if productionEnvironment() {
		return nil, errPasskeyProductionConfigRequired
	}
	return []string{requestBaseURL(r)}, nil
}

func requestBaseURL(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	} else if forwarded := strings.TrimSpace(r.Header.Get("X-Forwarded-Proto")); forwarded == "http" || forwarded == "https" {
		scheme = forwarded
	}
	host := strings.TrimSpace(r.Host)
	if host == "" {
		host = "localhost"
	}
	return scheme + "://" + host
}

func passkeyCredentialRequest(r *http.Request, credential json.RawMessage) (*http.Request, error) {
	req, err := http.NewRequestWithContext(r.Context(), http.MethodPost, requestBaseURL(r)+r.URL.Path, bytes.NewReader(credential))
	if err != nil {
		return nil, err
	}
	req.Host = r.Host
	req.Header.Set("Content-Type", "application/json")
	if ua := strings.TrimSpace(r.Header.Get("User-Agent")); ua != "" {
		req.Header.Set("User-Agent", ua)
	}
	return req, nil
}

func webauthnCredentials(credentials []store.PasskeyCredential) []webauthn.Credential {
	out := make([]webauthn.Credential, 0, len(credentials))
	for _, credential := range credentials {
		out = append(out, webauthn.Credential{
			ID:        append([]byte(nil), credential.CredentialID...),
			PublicKey: append([]byte(nil), credential.PublicKeyCBOR...),
			Transport: passkeyTransportsToWebAuthn(credential.Transports),
			Flags: webauthn.CredentialFlags{
				BackupEligible: credential.BackupEligible,
				BackupState:    credential.BackedUp,
			},
			Authenticator: webauthn.Authenticator{
				AAGUID:    decodeAAGUID(credential.AAGUID),
				SignCount: credential.SignCount,
			},
		})
	}
	return out
}

func storeCredentialFromWebAuthn(userID, name string, credential webauthn.Credential) store.PasskeyCredential {
	return store.PasskeyCredential{
		UserID:         userID,
		Name:           name,
		CredentialID:   append([]byte(nil), credential.ID...),
		PublicKeyCBOR:  append([]byte(nil), credential.PublicKey...),
		SignCount:      credential.Authenticator.SignCount,
		Transports:     passkeyTransportsFromWebAuthn(credential.Transport),
		AAGUID:         hex.EncodeToString(credential.Authenticator.AAGUID),
		BackupEligible: credential.Flags.BackupEligible,
		BackedUp:       credential.Flags.BackupState,
	}
}

func passkeyTransportsToWebAuthn(values []string) []protocol.AuthenticatorTransport {
	out := make([]protocol.AuthenticatorTransport, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			out = append(out, protocol.AuthenticatorTransport(value))
		}
	}
	return out
}

func passkeyTransportsFromWebAuthn(values []protocol.AuthenticatorTransport) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		if string(value) != "" {
			out = append(out, string(value))
		}
	}
	return out
}

func decodeAAGUID(value string) []byte {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	decoded, err := hex.DecodeString(value)
	if err != nil {
		return nil
	}
	return decoded
}
