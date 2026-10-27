package httpapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/example/autostream-control-panel/internal/mediaassets"
	"github.com/example/autostream-control-panel/internal/store"
)

func TestMediaAssetHTTPExpectedDraftCleanupRejectsClaimedAssetsAndConcurrentClaim(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{ID: "draft-owner", Username: "draft-owner"}, "correct horse battery", []string{"streams.create", "streams.update"}); err != nil {
		t.Fatal(err)
	}
	media, err := mediaassets.NewMemoryRepository(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth), WithAuditStore(auth), WithMediaAssetRepository(media))
	cookie, csrf := loginForTest(t, handler, "draft-owner", "correct horse battery")
	newDraft := func() (mediaassets.UploadSession, mediaassets.Asset) {
		t.Helper()
		session, err := media.CreateUploadSession(t.Context(), "draft-owner", time.Now().UTC().Add(time.Hour))
		if err != nil {
			t.Fatal(err)
		}
		asset, err := media.Upload(t.Context(), mediaassets.UploadInput{SessionID: session.ID, UserID: "draft-owner", UsageType: "video_cover", Filename: "cover.png", ContentType: "image/png", Body: bytes.NewReader(testAvatarPNG(t, 32, 18))})
		if err != nil {
			t.Fatal(err)
		}
		return session, asset
	}
	path := func(sessionID, assetID string) string {
		return "/media-assets/" + assetID + "?expected_upload_session_id=" + url.QueryEscape(sessionID)
	}
	session, asset := newDraft()
	other, _ := newDraft()
	for _, suffix := range []string{"?expected_upload_session_id=", "?expected_upload_session_id=" + session.ID + "&expected_upload_session_id=" + other.ID, "?expected_upload_session_id=%ZZ"} {
		response := serveUserJSON(t, handler, http.MethodDelete, "/media-assets/"+asset.ID+suffix, "", cookie, csrf)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("malformed cleanup condition fell through status=%d", response.Code)
		}
		if _, err = media.GetAsset(t.Context(), "draft-owner", asset.ID); err != nil {
			t.Fatalf("malformed cleanup condition changed asset: %v", err)
		}
	}
	response := serveUserJSON(t, handler, http.MethodDelete, path(other.ID, asset.ID), "", cookie, csrf)
	if response.Code != http.StatusForbidden {
		t.Fatalf("wrong session cleanup status=%d", response.Code)
	}
	response = serveUserJSON(t, handler, http.MethodDelete, path(session.ID, asset.ID), "", cookie, csrf)
	if response.Code != http.StatusNoContent {
		t.Fatalf("draft cleanup status=%d body=%s", response.Code, response.Body.String())
	}
	if _, err = media.GetAsset(t.Context(), "draft-owner", asset.ID); !errors.Is(err, mediaassets.ErrNotFound) {
		t.Fatalf("draft cleanup did not retire asset: %v", err)
	}

	claimedSession, claimedAsset := newDraft()
	if err = media.ClaimDraft(t.Context(), "draft-owner", claimedSession.ID, "saved-stream", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	response = serveUserJSON(t, handler, http.MethodDelete, path(claimedSession.ID, claimedAsset.ID), "", cookie, csrf)
	var failure map[string]string
	if err = json.NewDecoder(response.Body).Decode(&failure); err != nil {
		t.Fatal(err)
	}
	if response.Code != http.StatusConflict || failure["code"] != "upload_session_claimed" {
		t.Fatalf("claimed cleanup status=%d code=%s", response.Code, failure["code"])
	}
	if got, err := media.GetAsset(t.Context(), "draft-owner", claimedAsset.ID); err != nil || got.OwnerType != "stream" || got.DeletedAt != nil {
		t.Fatalf("claimed asset damaged owner=%s deleted=%t err=%v", got.OwnerType, got.DeletedAt != nil, err)
	}

	// Both operations use the real repository mutex. Whichever wins, no
	// stream-owned asset may ever be soft-deleted by draft cleanup.
	concurrentSession, concurrentAsset := newDraft()
	concurrentVariant, err := media.EnsureVariant(t.Context(), "draft-owner", concurrentAsset.ID, 16, 9, true)
	if err != nil {
		t.Fatal(err)
	}
	media.ReferenceVariant("concurrent-stream", concurrentVariant.ID)
	start := make(chan struct{})
	cleanupResult := make(chan int, 1)
	claimResult := make(chan error, 1)
	request := httptest.NewRequest(http.MethodDelete, path(concurrentSession.ID, concurrentAsset.ID), nil)
	request.AddCookie(cookie)
	request.Header.Set("X-CSRF-Token", csrf)
	go func() {
		<-start
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		cleanupResult <- response.Code
	}()
	go func() {
		<-start
		claimResult <- media.ClaimDraft(t.Context(), "draft-owner", concurrentSession.ID, "concurrent-stream", time.Now().UTC())
	}()
	close(start)
	status, claimErr := <-cleanupResult, <-claimResult
	if claimErr != nil {
		t.Fatalf("concurrent claim error=%v", claimErr)
	}
	if status != http.StatusNoContent && status != http.StatusConflict {
		t.Fatalf("concurrent cleanup status=%d", status)
	}
	got, getErr := media.GetAsset(t.Context(), "draft-owner", concurrentAsset.ID)
	internal, internalErr := media.OpenInternalVariant(t.Context(), "concurrent-stream", concurrentVariant.ID)
	if internalErr != nil {
		t.Fatalf("inspect concurrent metadata: %v", internalErr)
	}
	_ = internal.Reader.Close()
	if status == http.StatusConflict {
		if getErr != nil || got.OwnerType != "stream" || got.DeletedAt != nil {
			t.Fatalf("claim winner damaged owner=%s deleted=%t err=%v", got.OwnerType, got.DeletedAt != nil, getErr)
		}
	} else if !errors.Is(getErr, mediaassets.ErrNotFound) || internal.Asset.OwnerType != "upload_draft" || internal.Asset.DeletedAt == nil {
		t.Fatalf("cleanup winner ownership=%s deleted=%t err=%v", internal.Asset.OwnerType, internal.Asset.DeletedAt != nil, getErr)
	}
}
