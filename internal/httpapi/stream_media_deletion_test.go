package httpapi

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/example/autostream-control-panel/internal/mediaassets"
	"github.com/example/autostream-control-panel/internal/store"
)

func TestDeleteStreamHTTPCollectsCoversOnlyAfterGuardedSuccess(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{ID: "delete-owner", Username: "delete-owner"}, "correct horse battery", []string{"streams.delete"}); err != nil {
		t.Fatal(err)
	}
	streams := store.NewMemoryStreamStore()
	stream, err := streams.CreateStream(t.Context(), "cover deletion HTTP")
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	media, err := mediaassets.NewMemoryRepository(root)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	session, err := media.CreateUploadSession(t.Context(), "delete-owner", now.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	asset, err := media.Upload(t.Context(), mediaassets.UploadInput{UserID: "delete-owner", SessionID: session.ID, UsageType: "video_cover", Filename: "cover.png", ContentType: "image/png", Body: bytes.NewReader(testAvatarPNG(t, 12, 8))})
	if err != nil {
		t.Fatal(err)
	}
	variant, err := media.EnsureVariant(t.Context(), "delete-owner", asset.ID, 6, 4, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := media.ClaimDraft(t.Context(), "delete-owner", session.ID, stream.ID, now); err != nil {
		t.Fatal(err)
	}
	media.ReferenceVariant(stream.ID, variant.ID)
	handler := NewServer(streams, WithAuthStore(auth), WithAuditStore(auth), WithServiceRegistryStore(auth), WithMediaAssetRepository(media))
	cookie, csrf := loginForTest(t, handler, "delete-owner", "correct horse battery")
	if _, err := streams.UpdateStreamStatus(t.Context(), stream.ID, "live"); err != nil {
		t.Fatal(err)
	}
	if response := serveUserJSON(t, handler, http.MethodDelete, "/streams/"+stream.ID, "", cookie, csrf); response.Code != http.StatusConflict {
		t.Fatalf("active delete status=%d", response.Code)
	}
	if _, err := media.GetAsset(t.Context(), "delete-owner", asset.ID); err != nil {
		t.Fatalf("rejected deletion hid image: %v", err)
	}
	if _, err := streams.UpdateStreamStatus(t.Context(), stream.ID, "completed"); err != nil {
		t.Fatal(err)
	}
	if response := serveUserJSON(t, handler, http.MethodDelete, "/streams/"+stream.ID, "", cookie, csrf); response.Code != http.StatusOK {
		t.Fatalf("guarded delete status=%d body=%s", response.Code, response.Body.String())
	}
	for _, key := range []string{asset.StorageKey, variant.StorageKey} {
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(key))); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("image file remains after successful DELETE: %v", err)
		}
	}
}

type retryingMediaGC struct {
	mediaassets.Repository
	calls    chan time.Time
	attempts atomic.Int32
}

func (r *retryingMediaGC) GarbageCollect(ctx context.Context, now time.Time, _ int) (int, error) {
	attempt := r.attempts.Add(1)
	select {
	case r.calls <- now:
	case <-ctx.Done():
		return 0, ctx.Err()
	}
	if attempt == 1 {
		return 0, errors.New("physical removal failure fixture")
	}
	return 0, nil
}

func TestMediaAssetCollectorResumesAtStartupAndRetriesAfterFailure(t *testing.T) {
	collector := &retryingMediaGC{calls: make(chan time.Time, 10)}
	server := &Server{mediaAssets: collector}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		server.RunMediaAssetGarbageCollector(ctx, 10*time.Millisecond)
	}()
	for range 2 {
		select {
		case <-collector.calls:
		case <-time.After(time.Second):
			t.Fatal("startup/retry collector did not run")
		}
	}
	cancel()
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("collector did not stop")
	}
}
