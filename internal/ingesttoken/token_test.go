package ingesttoken

import (
	"strings"
	"testing"
	"time"
)

func TestPreviewTokenIsCompactStableAndVerifiable(t *testing.T) {
	now := time.Date(2026, 8, 11, 4, 0, 0, 0, time.UTC)
	expiresAt := now.Add(12 * time.Hour).Truncate(time.Hour).Unix()
	first, err := IssuePreview("preview-secret", "stream-01", expiresAt)
	if err != nil {
		t.Fatalf("issue first preview token: %v", err)
	}
	second, err := IssuePreview("preview-secret", "stream-01", expiresAt)
	if err != nil {
		t.Fatalf("issue second preview token: %v", err)
	}
	if first != second {
		t.Fatalf("preview token must be stable for one expiry bucket: %q != %q", first, second)
	}
	if len(first) >= 160 {
		t.Fatalf("compact preview token is still too long (%d): %q", len(first), first)
	}
	streamID, gotExpiry, err := VerifyPreview("preview-secret", first, now)
	if err != nil {
		t.Fatalf("verify preview token: %v", err)
	}
	if streamID != "stream-01" || gotExpiry != expiresAt {
		t.Fatalf("unexpected preview claims: stream=%q expiry=%d", streamID, gotExpiry)
	}
	if strings.HasPrefix(first, Prefix+".") {
		t.Fatal("preview token must not use the legacy long ingest prefix")
	}
}

func TestPreviewTokenRejectsExpiryAndSignatureChanges(t *testing.T) {
	now := time.Date(2026, 8, 11, 4, 0, 0, 0, time.UTC)
	token, err := IssuePreview("preview-secret", "stream-01", now.Add(time.Hour).Unix())
	if err != nil {
		t.Fatalf("issue preview token: %v", err)
	}
	if _, _, err := VerifyPreview("wrong-secret", token, now); err == nil {
		t.Fatal("wrong signing key must be rejected")
	}
	if _, _, err := VerifyPreview("preview-secret", token, now.Add(2*time.Hour)); err == nil {
		t.Fatal("expired preview token must be rejected")
	}
	parts := strings.Split(token, ".")
	parts[2] = strings.Repeat("A", len(parts[2]))
	if _, _, err := VerifyPreview("preview-secret", strings.Join(parts, "."), now); err == nil {
		t.Fatal("tampered preview token must be rejected")
	}
}
