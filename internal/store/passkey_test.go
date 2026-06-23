package store

import (
	"context"
	"testing"
	"time"
)

func TestMemoryPasskeyCredentialLifecycle(t *testing.T) {
	st := NewMemoryAuthStore()
	ctx := context.Background()

	created, err := st.CreatePasskeyCredential(ctx, PasskeyCredential{
		UserID:         "user-01",
		Name:           "Admin laptop",
		CredentialID:   []byte("credential-id"),
		PublicKeyCBOR:  []byte{0xa5, 0x01, 0x02},
		SignCount:      10,
		Transports:     []string{"usb", "internal", "usb"},
		BackupEligible: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if created.ID == "" || created.CredentialIDHash == "" {
		t.Fatalf("expected public passkey fields: %#v", created)
	}
	if len(created.CredentialID) != 0 || len(created.PublicKeyCBOR) != 0 {
		t.Fatalf("public passkey must not expose raw credential material: %#v", created)
	}
	if len(created.Transports) != 2 {
		t.Fatalf("expected deduplicated transports: %#v", created.Transports)
	}

	listed, err := st.ListPasskeyCredentials(ctx, "user-01")
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) != 1 || len(listed[0].CredentialID) != 0 || len(listed[0].PublicKeyCBOR) != 0 {
		t.Fatalf("list must return public passkey shape: %#v", listed)
	}

	privateCredential, err := st.FindPasskeyCredentialByCredentialID(ctx, []byte("credential-id"))
	if err != nil {
		t.Fatal(err)
	}
	if string(privateCredential.CredentialID) != "credential-id" || len(privateCredential.PublicKeyCBOR) == 0 {
		t.Fatalf("private lookup must retain verifier material: %#v", privateCredential)
	}

	if err := st.UpdatePasskeySignCount(ctx, created.ID, 12); err != nil {
		t.Fatal(err)
	}
	updated, err := st.FindPasskeyCredentialByCredentialID(ctx, []byte("credential-id"))
	if err != nil {
		t.Fatal(err)
	}
	if updated.SignCount != 12 || updated.LastUsedAt == nil {
		t.Fatalf("sign count was not updated: %#v", updated)
	}

	if err := st.DeletePasskeyCredential(ctx, "other-user", created.ID); err != ErrNotFound {
		t.Fatalf("cross-user delete should fail, got %v", err)
	}
	if err := st.DeletePasskeyCredential(ctx, "user-01", created.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := st.FindPasskeyCredentialByCredentialID(ctx, []byte("credential-id")); err != ErrNotFound {
		t.Fatalf("expected passkey to be deleted, got %v", err)
	}
}

func TestPasskeyCredentialRequiresVerifierMaterial(t *testing.T) {
	st := NewMemoryAuthStore()
	if _, err := st.CreatePasskeyCredential(context.Background(), PasskeyCredential{UserID: "user-01", CredentialID: []byte("credential-id")}); err == nil {
		t.Fatal("expected missing public key to be rejected")
	}
}

func TestMemoryPasskeyRegistrationChallengeLifecycle(t *testing.T) {
	st := NewMemoryAuthStore()
	ctx := context.Background()
	challenge, err := st.CreatePasskeyRegistrationChallenge(ctx, "user-01", "control.example.com", "AutoStream", "admin", "", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if challenge.Token == "" || challenge.TokenHash == "" || challenge.Challenge == "" || challenge.UserHandle == "" {
		t.Fatalf("expected one-time registration challenge fields: %#v", challenge)
	}
	if challenge.TokenHash == challenge.Token {
		t.Fatalf("registration challenge must be stored by hash, got raw token")
	}
	loaded, err := st.GetPasskeyRegistrationChallenge(ctx, challenge.Token)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.UserID != "user-01" || loaded.RPID != "control.example.com" || loaded.UserDisplayName != "admin" {
		t.Fatalf("unexpected loaded challenge: %#v", loaded)
	}
	if err := st.DeletePasskeyRegistrationChallenge(ctx, challenge.Token); err != nil {
		t.Fatal(err)
	}
	if _, err := st.GetPasskeyRegistrationChallenge(ctx, challenge.Token); err != ErrNotFound {
		t.Fatalf("expected deleted challenge to be missing, got %v", err)
	}
	expired, err := st.CreatePasskeyRegistrationChallenge(ctx, "user-01", "control.example.com", "AutoStream", "admin", "Admin", -time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.GetPasskeyRegistrationChallenge(ctx, expired.Token); err != ErrNotFound {
		t.Fatalf("expected expired challenge to be missing, got %v", err)
	}
}

func TestMemoryPasskeyCeremonySessionLifecycle(t *testing.T) {
	st := NewMemoryAuthStore()
	ctx := context.Background()
	session, err := st.CreatePasskeyCeremonySession(ctx, "user-01", "registration", []byte(`{"challenge":"abc"}`), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if session.Token == "" || session.TokenHash == "" || session.TokenHash == session.Token || len(session.SessionJSON) == 0 {
		t.Fatalf("expected hashed one-time ceremony session: %#v", session)
	}
	if _, err := st.GetPasskeyCeremonySession(ctx, session.Token, "login"); err != ErrNotFound {
		t.Fatalf("ceremony mismatch should fail, got %v", err)
	}
	loaded, err := st.GetPasskeyCeremonySession(ctx, session.Token, "registration")
	if err != nil {
		t.Fatal(err)
	}
	if loaded.UserID != "user-01" || loaded.Ceremony != "registration" || string(loaded.SessionJSON) != `{"challenge":"abc"}` {
		t.Fatalf("unexpected ceremony session: %#v", loaded)
	}
	consumed, err := st.ConsumePasskeyCeremonySession(ctx, session.Token, "registration")
	if err != nil {
		t.Fatal(err)
	}
	if consumed.UserID != "user-01" || string(consumed.SessionJSON) != `{"challenge":"abc"}` {
		t.Fatalf("unexpected consumed ceremony session: %#v", consumed)
	}
	if _, err := st.ConsumePasskeyCeremonySession(ctx, session.Token, "registration"); err != ErrNotFound {
		t.Fatalf("ceremony session must be consumed atomically once, got %v", err)
	}
	session, err = st.CreatePasskeyCeremonySession(ctx, "user-01", "registration", []byte(`{"challenge":"def"}`), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.DeletePasskeyCeremonySession(ctx, session.Token); err != nil {
		t.Fatal(err)
	}
	if _, err := st.GetPasskeyCeremonySession(ctx, session.Token, "registration"); err != ErrNotFound {
		t.Fatalf("expected deleted ceremony session to be missing, got %v", err)
	}
	expired, err := st.CreatePasskeyCeremonySession(ctx, "user-01", "login", []byte(`{"challenge":"expired"}`), -time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.GetPasskeyCeremonySession(ctx, expired.Token, "login"); err != ErrNotFound {
		t.Fatalf("expected expired ceremony session to be missing, got %v", err)
	}
}

func TestPasskeyCeremonySessionRejectsInvalidInput(t *testing.T) {
	st := NewMemoryAuthStore()
	if _, err := st.CreatePasskeyCeremonySession(context.Background(), "user-01", "invalid", []byte(`{}`), time.Minute); err == nil {
		t.Fatal("expected invalid ceremony to be rejected")
	}
	if _, err := st.CreatePasskeyCeremonySession(context.Background(), "user-01", "login", nil, time.Minute); err == nil {
		t.Fatal("expected missing session data to be rejected")
	}
}
