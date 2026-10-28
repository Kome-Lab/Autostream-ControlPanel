package httpapi

import (
	"bytes"
	"errors"
	"testing"
	"time"

	"github.com/example/autostream-control-panel/internal/mediaassets"
	"github.com/example/autostream-control-panel/internal/videocover"
)

func TestMariaDBDraftUploadOperationPresetDetachPreservesNonce(t *testing.T) {
	st := preparationClaimStores(t, true)
	user, err := st.auth.CreateFirstAdmin(t.Context(), "preset-nonce-owner", outputSecretRandom(t), []string{"streams.create"})
	if err != nil {
		t.Fatal(err)
	}
	media, err := mediaassets.NewMariaDBRepository(st.db, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	session, err := media.CreateUploadSession(t.Context(), user.ID, time.Now().UTC().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	const operation = "3cf1a246-eac1-421b-b00e-48ec97ca6075"
	image := testVideoCoverPNG(t)
	input := mediaassets.UploadInput{UserID: user.ID, SessionID: session.ID, ClientOperationID: operation, UsageType: "video_cover", Filename: "preset.png", ContentType: "image/png", Body: bytes.NewReader(image)}
	asset, err := media.Upload(t.Context(), input)
	if err != nil {
		t.Fatal(err)
	}
	variant, err := media.EnsureVariant(t.Context(), user.ID, asset.ID, 1920, 1080, true)
	if err != nil {
		t.Fatal(err)
	}
	backgroundInput := input
	backgroundInput.ClientOperationID = "75972aab-8707-4758-87c8-7ed16937aba1"
	backgroundInput.UsageType, backgroundInput.Body = "scene_background", bytes.NewReader(image)
	background, err := media.Upload(t.Context(), backgroundInput)
	if err != nil {
		t.Fatal(err)
	}
	covers := videocover.NewMariaDBRepository(st.db)
	preset, err := covers.CreatePreset(t.Context(), videocover.Preset{Name: "nonce shared preset", AssetID: asset.ID, AssetVariantID: variant.ID, Enabled: true, CreatedByUserID: user.ID})
	if err != nil {
		t.Fatal(err)
	}
	inspection, err := media.InspectDraftUpload(t.Context(), user.ID, session.ID, operation)
	if err != nil || inspection.Session.ID != session.ID || inspection.Session.OwnerType != "upload_draft" || inspection.Session.ClaimedAt != nil || len(inspection.Assets) != 1 || inspection.Assets[0].Asset.ID != asset.ID || inspection.Assets[0].Asset.UploadSessionID != "" || inspection.Assets[0].Asset.OwnerType != "preset" || inspection.Assets[0].Asset.ClientOperationSessionID != session.ID || inspection.Assets[0].Asset.ClientOperationID != operation {
		t.Fatal("preset detach lost immutable operation origin or changed remaining draft ownership")
	}
	input.Body = bytes.NewReader(image)
	if _, err = media.Upload(t.Context(), input); !errors.Is(err, mediaassets.ErrDraftClaimed) {
		t.Fatal("a detached preset operation allocated a new original")
	}
	var originals int
	if err = st.db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM media_assets WHERE owner_user_id=?`, user.ID).Scan(&originals); err != nil || originals != 2 {
		t.Fatal("preset detach replay duplicated an original")
	}
	kept, err := media.GetAsset(t.Context(), user.ID, background.ID)
	if err != nil || kept.OwnerType != "upload_draft" || kept.OwnerID != session.ID {
		t.Fatal("replay disturbed the other background asset")
	}
	if _, err = covers.GetPreset(t.Context(), preset.ID, false); err != nil {
		t.Fatal("replay disturbed the shared preset")
	}
	if err = media.VerifyVariant(t.Context(), asset.ID, variant.ID); err != nil {
		t.Fatal("replay disturbed the shared image bytes")
	}
}
