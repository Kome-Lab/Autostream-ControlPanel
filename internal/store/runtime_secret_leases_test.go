package store

import (
	"errors"
	"testing"
	"time"
)

func TestMemoryRuntimeSecretLeaseStoreRejectsActiveDuplicate(t *testing.T) {
	st := NewMemoryRuntimeSecretLeaseStore()
	lease := RuntimeSecretLease{
		ServiceID:        "encoder-01",
		TokenID:          "token-01",
		StreamID:         "stream-01",
		ArchiveProfileID: "archive-01",
		SecretName:       "youtube_stream_key_runtime",
	}
	created, err := st.ClaimRuntimeSecretLease(t.Context(), lease, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if created.ID == "" || created.ExpiresAt.IsZero() {
		t.Fatalf("lease was not initialized: %#v", created)
	}
	if _, err := st.ClaimRuntimeSecretLease(t.Context(), lease, time.Minute); !errors.Is(err, ErrRuntimeSecretLeaseActive) {
		t.Fatalf("expected active duplicate to be rejected, got %v", err)
	}

	otherSecret := lease
	otherSecret.SecretName = "youtube_rtmp_url_runtime"
	if _, err := st.ClaimRuntimeSecretLease(t.Context(), otherSecret, time.Minute); err != nil {
		t.Fatalf("different secret should have an independent lease: %v", err)
	}
}

func TestMemoryRuntimeSecretLeaseStoreAllowsExpiredLeaseReplacement(t *testing.T) {
	st := NewMemoryRuntimeSecretLeaseStore()
	lease := RuntimeSecretLease{ServiceID: "encoder-01", TokenID: "token-01", SecretName: "youtube_stream_key_runtime"}
	if _, err := st.ClaimRuntimeSecretLease(t.Context(), lease, time.Nanosecond); err != nil {
		t.Fatal(err)
	}
	time.Sleep(time.Millisecond)
	if _, err := st.ClaimRuntimeSecretLease(t.Context(), lease, time.Minute); err != nil {
		t.Fatalf("expired lease should be replaceable: %v", err)
	}
}

func TestMemoryRuntimeSecretLeaseStoreTokenRotationCannotBypassActiveLease(t *testing.T) {
	st := NewMemoryRuntimeSecretLeaseStore()
	lease := RuntimeSecretLease{
		ServiceID:        "encoder-01",
		TokenID:          "token-old",
		StreamID:         "stream-01",
		ArchiveProfileID: "archive-01",
		SecretName:       "youtube_stream_key_runtime",
	}
	created, err := st.ClaimRuntimeSecretLease(t.Context(), lease, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	rotated := lease
	rotated.TokenID = "token-new"
	if _, err := st.ClaimRuntimeSecretLease(t.Context(), rotated, time.Minute); !errors.Is(err, ErrRuntimeSecretLeaseActive) {
		t.Fatalf("rotated token must not bypass an active context lease, got %v", err)
	}
	if err := st.ReleaseRuntimeSecretLease(t.Context(), RuntimeSecretLease{
		ID:               created.ID,
		ServiceID:        created.ServiceID,
		TokenID:          "token-new",
		StreamID:         created.StreamID,
		ArchiveProfileID: created.ArchiveProfileID,
		SecretName:       created.SecretName,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.ClaimRuntimeSecretLease(t.Context(), rotated, time.Minute); !errors.Is(err, ErrRuntimeSecretLeaseActive) {
		t.Fatalf("wrong token release must not delete active lease, got %v", err)
	}
	if err := st.ReleaseRuntimeSecretLease(t.Context(), created); err != nil {
		t.Fatal(err)
	}
	if _, err := st.ClaimRuntimeSecretLease(t.Context(), rotated, time.Minute); err != nil {
		t.Fatalf("rotated token should claim after the original lease is released: %v", err)
	}
}
