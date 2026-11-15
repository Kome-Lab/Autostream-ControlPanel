package store

import (
	"context"
	"errors"
	"testing"
	"time"

	lf "github.com/example/autostream-control-panel/internal/lifecyclefoundation"
)

func TestB2FactorRequiredClosure(t *testing.T) {
	ctx := context.Background()
	// A nil DB proves refusal precedes SQL. Secret-like inputs are synthetic.
	s := NewMariaDBAuthStoreWithLifecycle(nil, "", lf.NewRequiredGate())
	cases := []struct {
		name string
		run  func() error
	}{
		{"mfa-read-missing-provider", func() error {
			value, err := s.GetMFAConfig(ctx, "synthetic-user")
			if value.UserID != "" || value.TOTPSecret != "" || value.PendingTOTPSecret != "" || len(value.RecoveryCodeHashes) != 0 {
				t.Fatal("rejected read disclosed factor state")
			}
			return err
		}},
		{"recovery-missing-provider", func() error { return s.ConsumeRecoveryCode(ctx, "synthetic-user", "synthetic-hash") }},
		{"enrollment", func() error { return s.StartTOTPEnrollment(ctx, "synthetic-user", "synthetic-secret", []string{"synthetic-hash"}) }},
		{"confirm", func() error { return s.ConfirmTOTPEnrollment(ctx, "synthetic-user") }},
		{"disable", func() error { return s.DisableMFA(ctx, "synthetic-user") }},
		{"regenerate", func() error { return s.RegenerateRecoveryCodes(ctx, "synthetic-user", []string{"synthetic-hash"}) }},
		{"mfa-challenge-create", func() error {
			value, err := s.CreateMFAChallenge(ctx, "synthetic-user", time.Minute)
			if value.Token != "" || value.TokenHash != "" { t.Fatal("rejected challenge disclosed a token") }
			return err
		}},
		{"mfa-challenge-read", func() error { _, err := s.GetMFAChallenge(ctx, "synthetic-token"); return err }},
		{"mfa-challenge-delete", func() error { return s.DeleteMFAChallenge(ctx, "synthetic-token") }},
		{"registration-create", func() error { _, err := s.CreatePasskeyRegistrationChallenge(ctx, "synthetic-user", "synthetic-rp", "rp", "user", "user", time.Minute); return err }},
		{"registration-read", func() error { _, err := s.GetPasskeyRegistrationChallenge(ctx, "synthetic-token"); return err }},
		{"registration-delete", func() error { return s.DeletePasskeyRegistrationChallenge(ctx, "synthetic-token") }},
		{"ceremony-create", func() error { _, err := s.CreatePasskeyCeremonySession(ctx, "synthetic-user", "login", []byte("{}"), time.Minute); return err }},
		{"ceremony-read", func() error { _, err := s.GetPasskeyCeremonySession(ctx, "synthetic-token", "login"); return err }},
		{"ceremony-consume", func() error { _, err := s.ConsumePasskeyCeremonySession(ctx, "synthetic-token", "login"); return err }},
		{"ceremony-delete", func() error { return s.DeletePasskeyCeremonySession(ctx, "synthetic-token") }},
		{"credential-list", func() error { _, err := s.ListPasskeyCredentials(ctx, "synthetic-user"); return err }},
		{"credential-verification-list", func() error { _, err := s.ListPasskeyCredentialsForVerification(ctx, "synthetic-user"); return err }},
		{"credential-create", func() error { _, err := s.CreatePasskeyCredential(ctx, PasskeyCredential{}); return err }},
		{"credential-find", func() error { _, err := s.FindPasskeyCredentialByCredentialID(ctx, []byte("synthetic-credential")); return err }},
		{"credential-counter", func() error { return s.UpdatePasskeySignCount(ctx, "synthetic-credential", 1) }},
		{"credential-delete", func() error { return s.DeletePasskeyCredential(ctx, "synthetic-user", "synthetic-credential") }},
		{"email-create", func() error { _, err := s.CreateEmailChangeChallenge(ctx, "synthetic-user", "synthetic@example.test", time.Minute); return err }},
		{"email-read", func() error { _, err := s.GetEmailChangeChallenge(ctx, "synthetic-token"); return err }},
		{"email-consume", func() error { _, err := s.ConsumeEmailChangeChallenge(ctx, "synthetic-token"); return err }},
		{"email-delete-helper", func() error { return s.deleteEmailChangeChallenge(ctx, "synthetic-token") }},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			if err := test.run(); !errors.Is(err, lf.ErrHeld) {
				t.Fatalf("required factor path returned %v", err)
			}
		})
	}
}
