package store

import (
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/example/autostream-control-panel/internal/security"
	"strings"
	"time"
)

func (s MariaDBAuthStore) decryptMFASecret(ciphertext, nonce string) (string, error) {
	if s.secretKeyMaterial == "" {
		return "", ErrSecretKeyRequired
	}
	return security.DecryptSecret(ciphertext, nonce, s.secretKeyMaterial)
}

type publicPasskeyScanner interface {
	Scan(dest ...any) error
}

func scanPublicPasskeyCredential(scanner publicPasskeyScanner) (PasskeyCredential, error) {
	var credential PasskeyCredential
	var transportsRaw string
	var aaguid sql.NullString
	var lastUsed sql.NullTime
	if err := scanner.Scan(&credential.ID, &credential.UserID, &credential.Name, &credential.CredentialIDHash, &credential.SignCount, &transportsRaw, &aaguid, &credential.BackupEligible, &credential.BackedUp, &credential.CreatedAt, &credential.UpdatedAt, &lastUsed); err != nil {
		return PasskeyCredential{}, err
	}
	_ = json.Unmarshal([]byte(transportsRaw), &credential.Transports)
	credential.AAGUID = aaguid.String
	if lastUsed.Valid {
		credential.LastUsedAt = &lastUsed.Time
	}
	return credential, nil
}

func normalizePasskeyCredential(credential PasskeyCredential) (PasskeyCredential, error) {
	credential.UserID = strings.TrimSpace(credential.UserID)
	credential.Name = strings.TrimSpace(credential.Name)
	credential.AAGUID = strings.TrimSpace(credential.AAGUID)
	if credential.UserID == "" || len(credential.CredentialID) == 0 || len(credential.PublicKeyCBOR) == 0 {
		return PasskeyCredential{}, errors.New("passkey credential user, credential id, and public key are required")
	}
	if credential.ID == "" {
		credential.ID = newUUID()
	}
	if credential.Name == "" {
		credential.Name = "Passkey"
	}
	credential.CredentialIDHash = passkeyCredentialIDHash(credential.CredentialID)
	credential.Transports = cleanPasskeyStringSlice(credential.Transports)
	now := time.Now().UTC()
	if credential.CreatedAt.IsZero() {
		credential.CreatedAt = now
	}
	credential.UpdatedAt = now
	return credential, nil
}

func publicPasskeyCredential(credential PasskeyCredential) PasskeyCredential {
	credential.CredentialID = nil
	credential.PublicKeyCBOR = nil
	return credential
}

func passkeyCredentialIDHash(credentialID []byte) string {
	sum := sha256.Sum256(credentialID)
	return hex.EncodeToString(sum[:])
}

func newPasskeyRegistrationChallenge(userID, rpID, rpName, userName, userDisplayName string, ttl time.Duration) (PasskeyRegistrationChallenge, error) {
	userID = strings.TrimSpace(userID)
	rpID = strings.TrimSpace(rpID)
	rpName = strings.TrimSpace(rpName)
	userName = strings.TrimSpace(userName)
	userDisplayName = strings.TrimSpace(userDisplayName)
	if userID == "" || rpID == "" || rpName == "" || userName == "" {
		return PasskeyRegistrationChallenge{}, errors.New("passkey registration user and relying party are required")
	}
	if userDisplayName == "" {
		userDisplayName = userName
	}
	rawToken, err := security.RandomToken(32)
	if err != nil {
		return PasskeyRegistrationChallenge{}, err
	}
	rawChallenge, err := security.RandomToken(32)
	if err != nil {
		return PasskeyRegistrationChallenge{}, err
	}
	now := time.Now().UTC()
	return PasskeyRegistrationChallenge{
		Token: rawToken, TokenHash: security.HashToken(rawToken), UserID: userID,
		Challenge: rawChallenge, UserHandle: base64.RawURLEncoding.EncodeToString([]byte(userID)),
		RPID: rpID, RPName: rpName, UserName: userName, UserDisplayName: userDisplayName,
		ExpiresAt: now.Add(ttl), CreatedAt: now,
	}, nil
}

func newEmailChangeChallenge(userID, email string, ttl time.Duration) (EmailChangeChallenge, error) {
	userID = strings.TrimSpace(userID)
	email, err := normalizeUserEmail(email)
	if err != nil {
		return EmailChangeChallenge{}, err
	}
	if userID == "" || email == "" {
		return EmailChangeChallenge{}, ErrInvalidSettings
	}
	rawToken, err := security.RandomToken(32)
	if err != nil {
		return EmailChangeChallenge{}, err
	}
	if ttl <= 0 {
		ttl = time.Hour
	}
	now := time.Now().UTC()
	return EmailChangeChallenge{
		Token:     rawToken,
		TokenHash: security.HashToken(rawToken),
		UserID:    userID,
		Email:     email,
		ExpiresAt: now.Add(ttl),
		CreatedAt: now,
	}, nil
}

func newPasskeyCeremonySession(userID, ceremony string, sessionJSON []byte, ttl time.Duration) (PasskeyCeremonySession, error) {
	userID = strings.TrimSpace(userID)
	ceremony = strings.TrimSpace(ceremony)
	if ceremony == "" || len(sessionJSON) == 0 {
		return PasskeyCeremonySession{}, errors.New("passkey ceremony session user, ceremony, and data are required")
	}
	switch ceremony {
	case "registration", "login":
	default:
		return PasskeyCeremonySession{}, errors.New("invalid passkey ceremony")
	}
	rawToken, err := security.RandomToken(32)
	if err != nil {
		return PasskeyCeremonySession{}, err
	}
	now := time.Now().UTC()
	return PasskeyCeremonySession{
		Token:       rawToken,
		TokenHash:   security.HashToken(rawToken),
		UserID:      userID,
		Ceremony:    ceremony,
		SessionJSON: append([]byte(nil), sessionJSON...),
		ExpiresAt:   now.Add(ttl),
		CreatedAt:   now,
	}, nil
}

func cleanPasskeyStringSlice(values []string) []string {
	out := make([]string, 0, len(values))
	seen := map[string]bool{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}
