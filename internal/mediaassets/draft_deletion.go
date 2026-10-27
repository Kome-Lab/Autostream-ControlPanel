package mediaassets

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

// DraftAssetDeleter limits cleanup to an unclaimed upload session. In
// particular, a lost save response cannot turn stale client draft metadata
// into authority to delete an asset that has already been saved by a stream.
type DraftAssetDeleter interface {
	SoftDeleteDraftAsset(ctx context.Context, userID, assetID, sessionID string, now time.Time) error
}

func (r *SQLRepository) SoftDeleteDraftAsset(ctx context.Context, userID, assetID, sessionID string, now time.Time) error {
	userID, assetID, sessionID = strings.TrimSpace(userID), strings.TrimSpace(assetID), strings.TrimSpace(sessionID)
	if userID == "" || assetID == "" || sessionID == "" {
		return ErrForbidden
	}
	if now.IsZero() {
		now = r.now()
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// ClaimDraftTx and Upload acquire this same session lock before asset rows.
	var ownerUser, ownerType string
	var claimed sql.NullString
	if err := tx.QueryRowContext(ctx, `SELECT user_id,owner_type,claimed_stream_id FROM media_upload_sessions WHERE id=? FOR UPDATE`, sessionID).Scan(&ownerUser, &ownerType, &claimed); errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	} else if err != nil {
		return err
	}
	if ownerUser != userID {
		return ErrForbidden
	}
	if ownerType != "upload_draft" || claimed.Valid {
		return ErrDraftClaimed
	}
	var assetUser, assetType, ownerID, uploadSession string
	if err := tx.QueryRowContext(ctx, `SELECT owner_user_id,owner_type,owner_id,COALESCE(upload_session_id,'') FROM media_assets WHERE id=? FOR UPDATE`, assetID).Scan(&assetUser, &assetType, &ownerID, &uploadSession); errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	} else if err != nil {
		return err
	}
	if assetUser != userID || ownerID != sessionID || uploadSession != sessionID {
		return ErrForbidden
	}
	if assetType != "upload_draft" {
		return ErrDraftClaimed
	}
	if _, err := tx.ExecContext(ctx, `UPDATE media_assets SET deleted_at=COALESCE(deleted_at,?),retention_until=COALESCE(retention_until,?),updated_at=? WHERE id=? AND owner_user_id=? AND owner_type='upload_draft' AND owner_id=? AND upload_session_id=?`, now, now.Add(DraftRetention), now, assetID, userID, sessionID, sessionID); err != nil {
		return err
	}
	return tx.Commit()
}

func (r *MemoryRepository) SoftDeleteDraftAsset(_ context.Context, userID, assetID, sessionID string, now time.Time) error {
	userID, assetID, sessionID = strings.TrimSpace(userID), strings.TrimSpace(assetID), strings.TrimSpace(sessionID)
	if userID == "" || assetID == "" || sessionID == "" {
		return ErrForbidden
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	session, ok := r.sessions[sessionID]
	if !ok {
		return ErrNotFound
	}
	if session.UserID != userID {
		return ErrForbidden
	}
	if session.OwnerType != "upload_draft" || session.ClaimedStreamID != "" {
		return ErrDraftClaimed
	}
	asset, ok := r.assets[assetID]
	if !ok {
		return ErrNotFound
	}
	if asset.OwnerUserID != userID || asset.OwnerID != sessionID || asset.UploadSessionID != sessionID {
		return ErrForbidden
	}
	if asset.OwnerType != "upload_draft" {
		return ErrDraftClaimed
	}
	if now.IsZero() {
		now = r.now()
	}
	if asset.DeletedAt == nil {
		deleted, retention := now, now.Add(DraftRetention)
		asset.DeletedAt, asset.RetentionUntil = &deleted, &retention
	}
	asset.UpdatedAt = now
	r.assets[assetID] = asset
	return nil
}

var _ DraftAssetDeleter = (*SQLRepository)(nil)
var _ DraftAssetDeleter = (*MemoryRepository)(nil)
