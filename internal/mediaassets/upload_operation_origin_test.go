package mediaassets

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestUploadOperationOriginSurvivesOwnershipDetach(t *testing.T) {
	repo, err := NewMemoryRepository(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	session, err := repo.CreateUploadSession(t.Context(), "origin-owner", time.Now().UTC().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	image := encodePNG(t, 32, 32, false)
	const operation = "3cf1a246-eac1-421b-b00e-48ec97ca6075"
	input := UploadInput{UserID: "origin-owner", SessionID: session.ID, ClientOperationID: operation, UsageType: "video_cover", Filename: "origin.png", ContentType: "image/png", Body: bytes.NewReader(image)}
	asset, err := repo.Upload(t.Context(), input)
	if err != nil {
		t.Fatal(err)
	}
	// Reproduce the metadata transition of the actual SQL preset claim. Its real
	// API/SQL execution is covered by the separate MariaDB preset-detach parent.
	repo.mu.Lock()
	detached := repo.assets[asset.ID]
	detached.OwnerType, detached.OwnerID, detached.UploadSessionID = "preset", "saved-preset", ""
	repo.assets[asset.ID] = detached
	repo.mu.Unlock()
	input.Body = bytes.NewReader(image)
	if _, err = repo.Upload(t.Context(), input); !errors.Is(err, ErrDraftClaimed) {
		t.Fatal("detached operation replay allocated another original")
	}
	observed, err := repo.InspectDraftUpload(t.Context(), "origin-owner", session.ID, operation)
	if err != nil || len(observed.Assets) != 1 || observed.Assets[0].Asset.ID != asset.ID || observed.Assets[0].Asset.OwnerType != "preset" {
		t.Fatal("read-only origin lookup lost a detached asset")
	}
	raw, err := json.Marshal(observed)
	if err != nil || strings.Contains(string(raw), "client_operation_session_id") {
		t.Fatal("private operation origin leaked into API JSON")
	}
}
