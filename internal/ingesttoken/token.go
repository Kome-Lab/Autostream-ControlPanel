package ingesttoken

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

const Prefix = "ast_ingest_v1"

type Claims struct {
	StreamID    string `json:"stream_id"`
	ServiceID   string `json:"service_id"`
	ServiceType string `json:"service_type"`
	Purpose     string `json:"purpose"`
	Audience    string `json:"audience"`
	ExpiresAt   int64  `json:"exp"`
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

func Expiry(now time.Time, ttl time.Duration) int64 {
	if ttl <= 0 {
		ttl = 12 * time.Hour
	}
	return now.UTC().Add(ttl).Unix()
}

func sign(secret, payload string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(Prefix))
	mac.Write([]byte("."))
	mac.Write([]byte(payload))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}
