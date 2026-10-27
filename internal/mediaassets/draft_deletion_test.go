package mediaassets

import (
	"bytes"
	"errors"
	"testing"
	"time"
)

func TestMemoryRepositoryDraftCleanupPreservesClaimedAssets(t *testing.T) {
	repo, err := NewMemoryRepository(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	newDraft := func() (UploadSession, Asset) {
		t.Helper()
		session, err := repo.CreateUploadSession(t.Context(), "owner", time.Now().UTC().Add(time.Hour))
		if err != nil {
			t.Fatal(err)
		}
		asset, err := repo.Upload(t.Context(), UploadInput{SessionID: session.ID, UserID: "owner", UsageType: "video_cover", Filename: "cover.png", ContentType: "image/png", Body: bytes.NewReader(encodePNG(t, 32, 18, false))})
		if err != nil {
			t.Fatal(err)
		}
		return session, asset
	}
	session, asset := newDraft()
	other, _ := newDraft()
	if err = repo.SoftDeleteDraftAsset(t.Context(), "owner", asset.ID, other.ID, time.Now().UTC()); !errors.Is(err, ErrForbidden) {
		t.Fatalf("wrong draft cleanup error=%v", err)
	}
	if _, err = repo.GetAsset(t.Context(), "owner", asset.ID); err != nil {
		t.Fatalf("wrong draft cleanup changed asset: %v", err)
	}
	if err = repo.ClaimDraft(t.Context(), "owner", session.ID, "saved-stream", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if err = repo.SoftDeleteDraftAsset(t.Context(), "owner", asset.ID, session.ID, time.Now().UTC()); !errors.Is(err, ErrDraftClaimed) {
		t.Fatalf("claimed draft cleanup error=%v", err)
	}
	if got, err := repo.GetAsset(t.Context(), "owner", asset.ID); err != nil || got.OwnerType != "stream" || got.DeletedAt != nil {
		t.Fatalf("claimed asset was damaged owner=%s deleted=%t err=%v", got.OwnerType, got.DeletedAt != nil, err)
	}

	cleanSession, cleanAsset := newDraft()
	when := time.Now().UTC()
	for range 2 {
		if err = repo.SoftDeleteDraftAsset(t.Context(), "owner", cleanAsset.ID, cleanSession.ID, when); err != nil {
			t.Fatalf("draft cleanup should be idempotent: %v", err)
		}
	}
	if _, err = repo.GetAsset(t.Context(), "owner", cleanAsset.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cleaned draft remained visible: %v", err)
	}
	if err = repo.ClaimDraft(t.Context(), "owner", cleanSession.ID, "later-stream", when); err != nil {
		t.Fatal(err)
	}
	if repo.assets[cleanAsset.ID].OwnerType != "upload_draft" {
		t.Fatal("deleted draft asset was transferred by later claim")
	}
}
