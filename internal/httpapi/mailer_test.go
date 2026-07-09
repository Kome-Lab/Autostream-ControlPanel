package httpapi

import (
	"mime"
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

func TestFormatPlainTextEmailEncodesJapaneseSubjectHeader(t *testing.T) {
	body := formatPlainTextEmail("AutoStream <no-reply@example.jp>", MailMessage{
		To:      "ops@example.jp",
		Subject: "Kome Panel SMTPテスト",
		Text:    "hello",
	})
	subjectLine := ""
	for _, line := range strings.Split(body, "\r\n") {
		if strings.HasPrefix(line, "Subject: ") {
			subjectLine = strings.TrimPrefix(line, "Subject: ")
		}
	}
	if subjectLine == "" {
		t.Fatalf("Subject header missing: %q", body)
	}
	if strings.Contains(subjectLine, "SMTPテスト") {
		t.Fatalf("Subject header is not encoded: %q", subjectLine)
	}
	decoded, err := new(mime.WordDecoder).DecodeHeader(subjectLine)
	if err != nil {
		t.Fatalf("Subject header decode failed: %v", err)
	}
	if decoded != "Kome Panel SMTPテスト" {
		t.Fatalf("decoded subject = %q", decoded)
	}
}
