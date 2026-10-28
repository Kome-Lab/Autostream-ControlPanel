package mediaassets

import (
	"context"
	"database/sql"
	"errors"
	"strings"
)

func (r *SQLRepository) InspectDraftUpload(ctx context.Context, userID, sessionID, operationID string) (DraftUploadSnapshot, error) {
	userID, sessionID = strings.TrimSpace(userID), strings.TrimSpace(sessionID)
	if userID == "" || !ValidUploadOperationID(operationID) {
		return DraftUploadSnapshot{}, ErrForbidden
	}
	tx, err := r.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return DraftUploadSnapshot{}, err
	}
	defer tx.Rollback()
	var session UploadSession
	var claimed sql.NullString
	err = tx.QueryRowContext(ctx, `SELECT id,user_id,owner_type,claimed_stream_id,expires_at,created_at,updated_at FROM media_upload_sessions WHERE id=? AND user_id=?`, sessionID, userID).Scan(
		&session.ID, &session.UserID, &session.OwnerType, &claimed, &session.ExpiresAt, &session.CreatedAt, &session.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return DraftUploadSnapshot{}, ErrNotFound
	}
	if err != nil {
		return DraftUploadSnapshot{}, err
	}
	if session.ID != sessionID || session.UserID != userID {
		return DraftUploadSnapshot{}, ErrNotFound
	}
	if claimed.Valid {
		session.ClaimedStreamID = claimed.String
	}
	if session.OwnerType == "stream" && session.ClaimedStreamID != "" {
		// ClaimDraftTx persists the claim timestamp in updated_at; this schema
		// has no separate claimed_at column. Draft creation is not a claim.
		claimedAt := session.UpdatedAt
		session.ClaimedAt = &claimedAt
	}
	result := DraftUploadSnapshot{Session: session, OperationID: operationID, Assets: []DraftUploadAsset{}}
	asset, err := scanAsset(tx.QueryRowContext(ctx, assetSelect+` WHERE owner_user_id=? AND client_operation_session_id=? AND client_operation_id=?`, userID, sessionID, operationID))
	if errors.Is(err, sql.ErrNoRows) {
		if err = tx.Commit(); err != nil {
			return DraftUploadSnapshot{}, err
		}
		return result, nil
	}
	if err != nil {
		return DraftUploadSnapshot{}, err
	}
	item := DraftUploadAsset{Asset: asset, Variants: []Variant{}}
	rows, err := tx.QueryContext(ctx, variantSelect+` WHERE v.asset_id=? ORDER BY v.id LIMIT 65`, asset.ID)
	if err != nil {
		return DraftUploadSnapshot{}, err
	}
	for rows.Next() {
		variant, scanErr := scanVariant(rows)
		if scanErr != nil {
			rows.Close()
			return DraftUploadSnapshot{}, scanErr
		}
		item.Variants = append(item.Variants, variant)
	}
	err = rows.Err()
	if closeErr := rows.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return DraftUploadSnapshot{}, err
	}
	if len(item.Variants) > 64 {
		return DraftUploadSnapshot{}, ErrIntegrity
	}
	result.Assets = append(result.Assets, item)
	if err = tx.Commit(); err != nil {
		return DraftUploadSnapshot{}, err
	}
	return result, nil
}
