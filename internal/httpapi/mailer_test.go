package httpapi

import (
	"strings"
	"testing"
)

func TestSMTPEnvelopeFromAcceptsDisplayNameAddress(t *testing.T) {
	got, err := smtpEnvelopeFrom("AutoStream <no-reply@example.jp>")
	if err != nil {
		t.Fatalf("smtpEnvelopeFrom returned error: %v", err)
	}
	if got != "no-reply@example.jp" {
		t.Fatalf("smtpEnvelopeFrom = %q, want address only", got)
	}
}

func TestSMTPEnvelopeFromRejectsLocalOnlyAddress(t *testing.T) {
	if got, err := smtpEnvelopeFrom("AutoStream <no-reply>"); err == nil || got != "" {
		t.Fatalf("smtpEnvelopeFrom accepted local-only address: got=%q err=%v", got, err)
	}
}

func TestFormatPlainTextEmailKeepsDisplayNameFromHeader(t *testing.T) {
	body := formatPlainTextEmail("AutoStream <no-reply@example.jp>", MailMessage{
		To:      "ops@example.jp",
		Subject: "SMTP test",
		Text:    "hello",
	})
	if !strings.Contains(body, "From: \"AutoStream\" <no-reply@example.jp>\r\n") {
		t.Fatalf("From header did not keep display name address: %q", body)
	}
}
