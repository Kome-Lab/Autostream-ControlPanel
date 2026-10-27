package mediaassets

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDeletedStreamCoverCollectionRemovesOwnedUploadsAndAllVariants(t *testing.T) {
	repo, err := NewMemoryRepository(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	ctx := context.Background()
	upload := func(streamID, usage string, width int) (Asset, []Variant) {
		t.Helper()
		session, err := repo.CreateUploadSession(ctx, "user", now.Add(time.Hour))
		if err != nil {
			t.Fatal(err)
		}
		asset, err := repo.Upload(ctx, UploadInput{UserID: "user", SessionID: session.ID, UsageType: usage, Filename: "cover.png", ContentType: "image/png", Body: bytes.NewReader(encodePNG(t, width, 6, false))})
		if err != nil {
			t.Fatal(err)
		}
		var variants []Variant
		for _, size := range []int{3, 4} {
			variant, err := repo.EnsureVariant(ctx, "user", asset.ID, size, 2, false)
			if err != nil {
				t.Fatal(err)
			}
			variants = append(variants, variant)
		}
		if err := repo.ClaimDraft(ctx, "user", session.ID, streamID, now); err != nil {
			t.Fatal(err)
		}
		return asset, variants
	}
	selected, selectedVariants := upload("deleted", "video_cover", 9)
	excluded, excludedVariants := upload("deleted", "video_cover", 17)
	background, backgroundVariants := upload("deleted", "scene_background", 25)
	other, otherVariants := upload("other", "video_cover", 33)
	shared, sharedVariants := upload("deleted", "video_cover", 41)
	preset, presetVariants := upload("deleted", "video_cover", 49)
	repo.mu.Lock()
	presetOwned := repo.assets[preset.ID]
	presetOwned.OwnerType = "preset"
	repo.assets[preset.ID] = presetOwned
	repo.mu.Unlock()
	repo.ReferenceVariant("deleted", selectedVariants[0].ID)
	repo.ReferenceVariant("other", sharedVariants[0].ID)
	if err := repo.RetireStreamCoverAssets(ctx, "deleted", now); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.GetAsset(ctx, "user", excluded.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("retired upload is still user-visible: %v", err)
	}
	if removed, err := repo.GarbageCollect(ctx, now, 100); err != nil || removed != 2 {
		t.Fatalf("removed=%d err=%v", removed, err)
	}
	for _, entry := range []struct {
		asset    Asset
		variants []Variant
		removed  bool
	}{{selected, selectedVariants, true}, {excluded, excludedVariants, true}, {background, backgroundVariants, false}, {other, otherVariants, false}, {shared, sharedVariants, false}, {preset, presetVariants, false}} {
		keys := []string{entry.asset.StorageKey}
		for _, variant := range entry.variants {
			keys = append(keys, variant.StorageKey)
		}
		for _, key := range keys {
			_, err := os.Stat(filepath.Join(repo.storage.Root(), filepath.FromSlash(key)))
			if entry.removed != errors.Is(err, os.ErrNotExist) {
				t.Fatalf("physical removed=%v expected=%v", errors.Is(err, os.ErrNotExist), entry.removed)
			}
		}
	}
	if err := repo.RetireStreamCoverAssets(ctx, "deleted", now); err != nil {
		t.Fatal(err)
	}
	if removed, err := repo.GarbageCollect(ctx, now, 100); err != nil || removed != 0 {
		t.Fatalf("double collection removed=%d err=%v", removed, err)
	}
}

func TestCoverCollectionRetriesPhysicalFailureWithoutDroppingMetadata(t *testing.T) {
	repo, err := NewMemoryRepository(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	now := time.Now().UTC()
	session, err := repo.CreateUploadSession(ctx, "user", now.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	asset, err := repo.Upload(ctx, UploadInput{UserID: "user", SessionID: session.ID, UsageType: "video_cover", Filename: "cover.png", ContentType: "image/png", Body: bytes.NewReader(encodePNG(t, 9, 6, false))})
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.ClaimDraft(ctx, "user", session.ID, "deleted", now); err != nil {
		t.Fatal(err)
	}
	if err := repo.RetireStreamCoverAssets(ctx, "deleted", now); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(repo.storage.Root(), filepath.FromSlash(asset.StorageKey))
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	blocker := filepath.Join(path, "failure-fixture")
	if err := os.WriteFile(blocker, []byte("retry"), 0o600); err != nil {
		t.Fatal(err)
	}
	if removed, err := repo.GarbageCollect(ctx, now, 100); err == nil || removed != 0 {
		t.Fatalf("physical failure was hidden: removed=%d err=%v", removed, err)
	}
	if _, retained := repo.assets[asset.ID]; !retained {
		t.Fatal("failed physical deletion lost its retry metadata")
	}
	if err := os.Remove(blocker); err != nil {
		t.Fatal(err)
	}
	if removed, err := repo.GarbageCollect(ctx, now, 100); err != nil || removed != 1 {
		t.Fatalf("retry did not converge: removed=%d err=%v", removed, err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("file remains after retry: %v", err)
	}
}
