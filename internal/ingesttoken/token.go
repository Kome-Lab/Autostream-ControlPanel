package ingesttoken

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"
)

const Prefix = "ast_ingest_v1"

// PreviewPrefix identifies the compact, bearer-only token used by the public
// preview player. It intentionally carries only a stream id and expiry; the
// HLS route still performs the normal active-stream and assignment checks.
const PreviewPrefix = "ast_preview_v1"

type Claims struct {
	StreamID    string `json:"stream_id"`
	ServiceID   string `json:"service_id"`
	ServiceType string `json:"service_type"`
	Purpose     string `json:"purpose"`
	Audience    string `json:"audience"`
	ExpiresAt   int64  `json:"exp"`
}

type Expected struct {
	StreamID    string
	ServiceID   string
	ServiceType string
	Purpose     string
	Audience    string
	Now         time.Time
}

func Issue(secret string, claims Claims) (string, error) {
	secret = strings.TrimSpace(secret)
	if secret == "" {
		return "", errors.New("ingest token signing key is required")
	}
	if strings.TrimSpace(claims.StreamID) == "" || strings.TrimSpace(claims.ServiceID) == "" || strings.TrimSpace(claims.ServiceType) == "" || strings.TrimSpace(claims.Purpose) == "" || strings.TrimSpace(claims.Audience) == "" || claims.ExpiresAt <= 0 {
		return "", errors.New("ingest token claims are incomplete")
	}
	payload, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	encodedPayload := base64.RawURLEncoding.EncodeToString(payload)
	sig := sign(secret, encodedPayload)
	return Prefix + "." + encodedPayload + "." + sig, nil
}

// IssuePreview creates a compact deterministic preview token. The caller
// chooses a stable expiry bucket so repeatedly opening a stream's details does
// not create a different long URL every time.
func IssuePreview(secret, streamID string, expiresAt int64) (string, error) {
	secret = strings.TrimSpace(secret)
	streamID = strings.TrimSpace(streamID)
	if secret == "" {
		return "", errors.New("ingest token signing key is required")
	}
	if streamID == "" || expiresAt <= 0 {
		return "", errors.New("preview token claims are incomplete")
	}
	payload := strconv.FormatInt(expiresAt, 10) + "." + streamID
	encodedPayload := base64.RawURLEncoding.EncodeToString([]byte(payload))
	sig := signWithPrefix(secret, PreviewPrefix, encodedPayload)
	return PreviewPrefix + "." + encodedPayload + "." + sig, nil
}

func Expiry(now time.Time, ttl time.Duration) int64 {
	if ttl <= 0 {
		ttl = 12 * time.Hour
	}
	return now.UTC().Add(ttl).Unix()
}

func Verify(secret, token string, expected Expected) (Claims, error) {
	secret = strings.TrimSpace(secret)
	if secret == "" {
		return Claims{}, errors.New("ingest token signing key is required")
	}
	parts := strings.Split(token, ".")
	if len(parts) != 3 || parts[0] != Prefix {
		return Claims{}, errors.New("invalid ingest token format")
	}
	wantSig := sign(secret, parts[1])
	if !hmac.Equal([]byte(parts[2]), []byte(wantSig)) {
		return Claims{}, errors.New("invalid ingest token signature")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return Claims{}, errors.New("invalid ingest token payload")
	}
	var claims Claims
	if err := json.Unmarshal(payload, &claims); err != nil {
		return Claims{}, errors.New("invalid ingest token claims")
	}
	now := expected.Now
	if now.IsZero() {
		now = time.Now().UTC()
	}
	if claims.ExpiresAt <= 0 || now.UTC().Unix() > claims.ExpiresAt {
		return Claims{}, errors.New("ingest token expired")
	}
	if expected.StreamID != "" && claims.StreamID != expected.StreamID {
		return Claims{}, errors.New("ingest token stream mismatch")
	}
	if expected.ServiceID != "" && claims.ServiceID != expected.ServiceID {
		return Claims{}, errors.New("ingest token service id mismatch")
	}
	if expected.ServiceType != "" && claims.ServiceType != expected.ServiceType {
		return Claims{}, errors.New("ingest token service type mismatch")
	}
	if expected.Purpose != "" && claims.Purpose != expected.Purpose {
		return Claims{}, errors.New("ingest token purpose mismatch")
	}
	if expected.Audience != "" && claims.Audience != expected.Audience {
		return Claims{}, errors.New("ingest token audience mismatch")
	}
	return claims, nil
}

// VerifyPreview validates a compact preview token and returns its stream id
// and expiry. Preview tokens intentionally have no service claims because the
// public route fixes those semantics itself before proxying HLS assets.
func VerifyPreview(secret, token string, now time.Time) (string, int64, error) {
	secret = strings.TrimSpace(secret)
	if secret == "" {
		return "", 0, errors.New("ingest token signing key is required")
	}
	parts := strings.Split(strings.TrimSpace(token), ".")
	if len(parts) != 3 || parts[0] != PreviewPrefix {
		return "", 0, errors.New("invalid preview token format")
	}
	wantSig := signWithPrefix(secret, PreviewPrefix, parts[1])
	if !hmac.Equal([]byte(parts[2]), []byte(wantSig)) {
		return "", 0, errors.New("invalid preview token signature")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return "", 0, errors.New("invalid preview token payload")
	}
	expiresText, streamID, ok := strings.Cut(string(payload), ".")
	if !ok || strings.TrimSpace(streamID) == "" {
		return "", 0, errors.New("invalid preview token claims")
	}
	expiresAt, err := strconv.ParseInt(expiresText, 10, 64)
	if err != nil || expiresAt <= 0 {
		return "", 0, errors.New("invalid preview token expiry")
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	if now.UTC().Unix() > expiresAt {
		return "", 0, errors.New("preview token expired")
	}
	return streamID, expiresAt, nil
}

func IsSigned(token string) bool {
	return strings.HasPrefix(token, Prefix+".")
}

func sign(secret, payload string) string {
	return signWithPrefix(secret, Prefix, payload)
}

func signWithPrefix(secret, prefix, payload string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(prefix))
	mac.Write([]byte("."))
	mac.Write([]byte(payload))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}
