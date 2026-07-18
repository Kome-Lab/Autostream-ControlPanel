package httpapi

import (
	"context"
	"mime"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/example/autostream-control-panel/internal/store"
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

func TestSMTPMailerHonorsContextDeadlineWhileWaitingForGreeting(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	release := make(chan struct{})
	defer close(release)
	go func() {
		conn, acceptErr := listener.Accept()
		if acceptErr != nil {
			return
		}
		defer conn.Close()
		<-release
	}()

	host, portValue, err := net.SplitHostPort(listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(portValue)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	started := time.Now()
	err = (SMTPMailer{}).Send(ctx, store.AppSettings{
		SMTPEnabled: true,
		SMTPHost:    host,
		SMTPPort:    port,
		SMTPFrom:    "noreply@example.jp",
	}, "", MailMessage{To: "ops@example.jp", Subject: "deadline test", Text: "body"})
	if err == nil || safeErrorCode(err) != "smtp_dial_failed" {
		t.Fatalf("SMTPMailer deadline error = %v, safe code = %q", err, safeErrorCode(err))
	}
	if elapsed := time.Since(started); elapsed > 2*time.Second {
		t.Fatalf("SMTPMailer ignored context deadline: elapsed=%s err=%v", elapsed, err)
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
