package httpapi

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/example/autostream-control-panel/internal/mediaassets"
)

func TestMariaDBDraftUploadOperationConcurrentReplayAndReadOnlyRecovery(t *testing.T) {
	st := preparationClaimStores(t, true)
	user, err := st.auth.CreateFirstAdmin(t.Context(), "nonce-owner", outputSecretRandom(t), []string{"streams.create"})
	if err != nil {
		t.Fatal(err)
	}
	media, err := mediaassets.NewMariaDBRepository(st.db, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	st.db.SetMaxOpenConns(6)
	session, err := media.CreateUploadSession(t.Context(), user.ID, time.Now().UTC().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	const operation = "3cf1a246-eac1-421b-b00e-48ec97ca6075"
	image := testAvatarPNG(t, 32, 32)
	input := mediaassets.UploadInput{UserID: user.ID, SessionID: session.ID, ClientOperationID: operation, UsageType: "video_cover", Filename: "nonce.png", ContentType: "image/png"}
	initial, err := media.InspectDraftUpload(t.Context(), user.ID, session.ID, operation)
	if err != nil {
		t.Fatal(err)
	}
	if initial.Session.ID != session.ID || initial.Session.OwnerType != "upload_draft" || initial.Session.ClaimedAt != nil || initial.OperationID != operation || len(initial.Assets) != 0 {
		t.Fatal("canonical session inspection did not read the migrated schema before any upload")
	}
	alias := strings.ToUpper(session.ID)
	if alias == session.ID {
		t.Fatal("session fixture requires a case alias")
	}
	aliased := input
	aliased.SessionID, aliased.Body = alias, bytes.NewReader(image)
	if _, aliasErr := media.Upload(t.Context(), aliased); !errors.Is(aliasErr, mediaassets.ErrNotFound) {
		t.Fatal("case-insensitive SQL session alias must not allocate an operation origin")
	}
	if _, aliasErr := media.InspectDraftUpload(t.Context(), user.ID, alias, operation); !errors.Is(aliasErr, mediaassets.ErrNotFound) {
		t.Fatal("read-only recovery accepted an unissued session alias")
	}
	blocker, err := st.db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer blocker.Rollback()
	var locked string
	if err = blocker.QueryRowContext(t.Context(), `SELECT id FROM media_upload_sessions WHERE id=? FOR UPDATE`, session.ID).Scan(&locked); err != nil {
		t.Fatal(err)
	}
	type outcome struct {
		asset mediaassets.Asset
		err   error
	}
	completed := make(chan outcome, 2)
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	for range 2 {
		go func() {
			in := input
			in.Body = bytes.NewReader(image)
			asset, err := media.Upload(ctx, in)
			completed <- outcome{asset, err}
		}()
	}
	// Observe both production requests waiting at the real session-row lock;
	// a goroutine-start barrier alone would not prove serialization.
	deadline := time.Now().Add(5 * time.Second)
	for {
		var waiting int
		err = st.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM information_schema.PROCESSLIST WHERE DB=DATABASE() AND COMMAND='Query' AND ID<>CONNECTION_ID() AND INFO LIKE '%media_upload_sessions%' AND INFO LIKE '%FOR UPDATE%'`).Scan(&waiting)
		if err != nil {
			t.Fatal(err)
		}
		if waiting >= 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("both uploads did not reach the observed session lock")
		}
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(10 * time.Millisecond):
		}
	}
	if err = blocker.Commit(); err != nil {
		t.Fatal(err)
	}
	var results []outcome
	for range 2 {
		select {
		case value := <-completed:
			results = append(results, value)
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
	if results[0].err != nil || results[1].err != nil || results[0].asset.ID == "" || results[0].asset.ID != results[1].asset.ID {
		t.Fatal("concurrent replay did not converge to one original")
	}
	var count int
	if err = st.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM media_assets WHERE upload_session_id=? AND client_operation_id=?`, session.ID, operation).Scan(&count); err != nil || count != 1 {
		t.Fatal("operation allocated duplicate database rows")
	}
	asset := results[0].asset
	variant, err := media.EnsureVariant(ctx, user.ID, asset.ID, 64, 64, true)
	if err != nil {
		t.Fatal(err)
	}
	before, err := media.InspectDraftUpload(ctx, user.ID, session.ID, operation)
	if err != nil || len(before.Assets) != 1 || before.Assets[0].Asset.ID != asset.ID || len(before.Assets[0].Variants) != 1 || before.Assets[0].Variants[0].ID != variant.ID {
		t.Fatal("committed operation is not recoverable through reads")
	}
	if _, err = media.InspectDraftUpload(ctx, "foreign-owner", session.ID, operation); !errors.Is(err, mediaassets.ErrNotFound) {
		t.Fatal("operation inspection disclosed another owner's session")
	}
	missing, err := media.InspectDraftUpload(ctx, user.ID, session.ID, "75972aab-8707-4758-87c8-7ed16937aba1")
	if err != nil || len(missing.Assets) != 0 {
		t.Fatal("missing operation must remain an empty, non-mutating observation")
	}
	if err = media.SoftDeleteDraftAsset(ctx, user.ID, asset.ID, session.ID, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	input.Body = bytes.NewReader(image)
	if _, err = media.Upload(ctx, input); !errors.Is(err, mediaassets.ErrNotFound) {
		t.Fatal("deleted operation was resurrected by replay")
	}
	after, err := media.InspectDraftUpload(ctx, user.ID, session.ID, operation)
	if err != nil || len(after.Assets) != 1 || after.Assets[0].Asset.DeletedAt == nil {
		t.Fatal("read-only tombstone proof missing after deletion")
	}
	if err = st.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM media_assets WHERE upload_session_id=?`, session.ID).Scan(&count); err != nil || count != 1 {
		t.Fatal("read-only recovery or replay allocated a row")
	}
	stream, err := st.streams.CreateStream(ctx, "claimed nonce snapshot")
	if err != nil {
		t.Fatal(err)
	}
	claimedAt := time.Now().UTC().Truncate(time.Microsecond)
	claimTx, err := st.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer claimTx.Rollback()
	if err = media.ClaimDraftTx(ctx, claimTx, user.ID, session.ID, stream.ID, claimedAt); err != nil {
		t.Fatal(err)
	}
	if err = claimTx.Commit(); err != nil {
		t.Fatal(err)
	}
	claimed, err := media.InspectDraftUpload(ctx, user.ID, session.ID, operation)
	if err != nil || claimed.Session.OwnerType != "stream" || claimed.Session.ClaimedStreamID != stream.ID || claimed.Session.ClaimedAt == nil || !claimed.Session.ClaimedAt.Equal(claimedAt) || !claimed.Session.UpdatedAt.Equal(claimedAt) || len(claimed.Assets) != 1 || claimed.Assets[0].Asset.ClientOperationSessionID != session.ID || claimed.Assets[0].Asset.DeletedAt == nil {
		t.Fatal("claimed session inspection did not preserve the committed claim timestamp and operation tombstone")
	}
}
