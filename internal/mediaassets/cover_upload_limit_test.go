package mediaassets

import (
	"bytes"
	"errors"
	"sync"
	"testing"
	"time"
)

func TestMemoryRepositoryCoverUploadLimitAndDeletedDraftRecovery(t *testing.T) {
	repo, err := NewMemoryRepository(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	session, err := repo.CreateUploadSession(t.Context(), "quota-user", time.Now().UTC().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = repo.Upload(t.Context(), UploadInput{SessionID: session.ID, UserID: "quota-user", UsageType: "video_cover", Filename: "bad.png", ContentType: "image/png", Body: bytes.NewReader([]byte("invalid image"))}); err == nil {
		t.Fatal("invalid upload unexpectedly succeeded")
	}
	upload := func(usage string, width int) (Asset, error) {
		return repo.Upload(t.Context(), UploadInput{SessionID: session.ID, UserID: "quota-user", UsageType: usage, Filename: "cover.png", ContentType: "image/png", Body: bytes.NewReader(encodePNG(t, width, 18, false))})
	}
	var first Asset
	for i := 0; i < 4; i++ {
		asset, err := upload("video_cover", 32+i)
		if err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			first = asset
		}
	}
	if _, err = repo.EnsureVariant(t.Context(), "quota-user", first.ID, 16, 9, true); err != nil {
		t.Fatal(err)
	}
	if _, err = upload("scene_background", 40); err != nil {
		t.Fatalf("background consumed cover quota: %v", err)
	}
	if _, err = upload("video_cover", 41); !errors.Is(err, ErrCoverUploadLimit) {
		t.Fatalf("fifth upload error=%v", err)
	}
	if err = repo.SoftDeleteAsset(t.Context(), "quota-user", first.ID, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if _, err = upload("video_cover", 42); err != nil {
		t.Fatalf("deleted draft did not free quota: %v", err)
	}
	if err = repo.ClaimDraft(t.Context(), "quota-user", session.ID, "quota-stream", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if got := repo.assets[first.ID]; got.OwnerType != "upload_draft" || got.DeletedAt == nil {
		t.Fatalf("deleted draft was claimed: owner=%s deleted=%t", got.OwnerType, got.DeletedAt != nil)
	}
}

func TestMemoryRepositoryConcurrentCoverUploadsCannotCommitFifth(t *testing.T) {
	repo, err := NewMemoryRepository(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	session, err := repo.CreateUploadSession(t.Context(), "quota-user", time.Now().UTC().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		_, err = repo.Upload(t.Context(), UploadInput{SessionID: session.ID, UserID: "quota-user", UsageType: "video_cover", Filename: "cover.png", ContentType: "image/png", Body: bytes.NewReader(encodePNG(t, 32+i, 18, false))})
		if err != nil {
			t.Fatal(err)
		}
	}
	start := make(chan struct{})
	results := make(chan error, 2)
	var ready sync.WaitGroup
	ready.Add(2)
	for i := 0; i < 2; i++ {
		body := encodePNG(t, 50+i, 18, false)
		go func() {
			ready.Done()
			<-start
			_, uploadErr := repo.Upload(t.Context(), UploadInput{SessionID: session.ID, UserID: "quota-user", UsageType: "video_cover", Filename: "cover.png", ContentType: "image/png", Body: bytes.NewReader(body)})
			results <- uploadErr
		}()
	}
	ready.Wait()
	close(start)
	successes, limited := 0, 0
	for range 2 {
		err := <-results
		switch {
		case err == nil:
			successes++
		case errors.Is(err, ErrCoverUploadLimit):
			limited++
		default:
			t.Fatalf("concurrent upload error=%v", err)
		}
	}
	if successes != 1 || limited != 1 || repo.draftCoverCountLocked(session.ID) != 4 {
		t.Fatalf("successes=%d limited=%d stored=%d", successes, limited, repo.draftCoverCountLocked(session.ID))
	}
}

func TestMemoryRepositoryCoverClaimRejectsLegacyOversizedDraftAtomically(t *testing.T) {
	repo, err := NewMemoryRepository(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	session, err := repo.CreateUploadSession(t.Context(), "quota-user", time.Now().UTC().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"a", "b", "c", "d", "e"} {
		repo.assets[id] = Asset{ID: id, OwnerUserID: "quota-user", OwnerType: "upload_draft", OwnerID: session.ID, UploadSessionID: session.ID, UsageType: "video_cover"}
	}
	if err = repo.ClaimDraft(t.Context(), "quota-user", session.ID, "stream", time.Now().UTC()); !errors.Is(err, ErrCoverUploadLimit) {
		t.Fatalf("claim error=%v", err)
	}
	if repo.sessions[session.ID].OwnerType != "upload_draft" {
		t.Fatal("failed claim mutated session")
	}
	for _, asset := range repo.assets {
		if asset.OwnerType != "upload_draft" {
			t.Fatal("failed claim mutated asset")
		}
	}
}
