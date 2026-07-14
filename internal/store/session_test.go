package store

import (
	"errors"
	"testing"
	"time"
)

func TestMemoryAuthStoreRefreshSessionSlidesIdleExpiryWithinAbsoluteLifetime(t *testing.T) {
	auth := NewMemoryAuthStore()
	if err := auth.AddUser(User{ID: "session-user", Username: "session-user"}, "correct horse battery", nil); err != nil {
		t.Fatal(err)
	}
	session, err := auth.CreateSession(t.Context(), "session-user", time.Minute, 10*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	refreshed, err := auth.RefreshSession(t.Context(), session.Token, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if !refreshed.IdleExpiresAt.After(session.IdleExpiresAt) {
		t.Fatalf("idle expiry did not advance: before=%s after=%s", session.IdleExpiresAt, refreshed.IdleExpiresAt)
	}
	if !refreshed.IdleExpiresAt.Equal(session.AbsoluteExpiresAt) {
		t.Fatalf("idle expiry must be capped by absolute expiry: idle=%s absolute=%s", refreshed.IdleExpiresAt, session.AbsoluteExpiresAt)
	}
}

func TestMemoryAuthStoreRefreshSessionRejectsExpiredSession(t *testing.T) {
	auth := NewMemoryAuthStore()
	if err := auth.AddUser(User{ID: "expired-user", Username: "expired-user"}, "correct horse battery", nil); err != nil {
		t.Fatal(err)
	}
	session, err := auth.CreateSession(t.Context(), "expired-user", -time.Second, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := auth.RefreshSession(t.Context(), session.Token, time.Minute); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expired refresh error = %v", err)
	}
}
