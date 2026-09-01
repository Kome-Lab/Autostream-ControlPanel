package mediaassets

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

type SQLRepository struct {
	db        *sql.DB
	storage   *DiskStorage
	processor *Processor
	now       func() time.Time
}

func NewMariaDBRepository(db *sql.DB, storageRoot string) (*SQLRepository, error) {
	if db == nil {
		return nil, errors.New("media asset database is required")
	}
	storage, err := NewDiskStorage(storageRoot)
	if err != nil {
		return nil, err
	}
	return &SQLRepository{db: db, storage: storage, processor: NewProcessor(storage), now: func() time.Time { return time.Now().UTC() }}, nil
}

func (r *SQLRepository) CreateUploadSession(ctx context.Context, userID string, expiresAt time.Time) (UploadSession, error) {
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return UploadSession{}, ErrForbidden
	}
	now := r.now()
	if expiresAt.IsZero() {
		expiresAt = now.Add(DraftRetention)
	}
	if !expiresAt.After(now) || expiresAt.After(now.Add(DraftRetention)) {
		return UploadSession{}, ErrDraftExpired
	}
	id, err := newID()
	if err != nil {
		return UploadSession{}, err
	}
	session := UploadSession{ID: id, UserID: userID, OwnerType: "upload_draft", ExpiresAt: expiresAt.UTC(), CreatedAt: now, UpdatedAt: now}
	_, err = r.db.ExecContext(ctx, `INSERT INTO media_upload_sessions
      (id,user_id,owner_type,claimed_stream_id,expires_at,created_at,updated_at)
      VALUES (?,?,'upload_draft',NULL,?,?,?)`, session.ID, session.UserID, session.ExpiresAt, now, now)
	if err != nil {
		return UploadSession{}, err
	}
	return session, nil
}

func (r *SQLRepository) Upload(ctx context.Context, in UploadInput) (Asset, error) {
	in.SessionID = strings.TrimSpace(in.SessionID)
	in.UserID = strings.TrimSpace(in.UserID)
	in.UsageType = strings.TrimSpace(in.UsageType)
	if in.SessionID == "" || in.UserID == "" || (in.UsageType != "scene_background" && in.UsageType != "video_cover") {
		return Asset{}, ErrForbidden
	}
	if err := r.validateDraft(ctx, r.db, in.SessionID, in.UserID, r.now(), false); err != nil {
		return Asset{}, err
	}
	processed, err := r.processor.ProcessUpload(in.Filename, in.ContentType, in.Body)
	if err != nil {
		return Asset{}, err
	}
	now := r.now()
	id, err := newID()
	if err != nil {
		return Asset{}, err
	}
	asset := Asset{
		ID: id, OwnerUserID: in.UserID, OwnerType: "upload_draft", OwnerID: in.SessionID,
		UploadSessionID: in.SessionID, UsageType: in.UsageType, StorageKey: processed.StorageKey,
		SHA256: processed.SHA256, MediaType: processed.MediaType, ByteSize: processed.ByteSize,
		Width: processed.Width, Height: processed.Height, Opaque: processed.Opaque,
		ProcessorRevision: ProcessorRevision, CreatedAt: now, UpdatedAt: now,
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return Asset{}, err
	}
	defer tx.Rollback()
	// Serialize the final validation with a concurrent stream-create claim. A
	// non-locking recheck leaves a window where an upload can be inserted as a
	// draft after the session has already been claimed by a stream.
	if err := r.validateDraft(ctx, tx, in.SessionID, in.UserID, now, true); err != nil {
		return Asset{}, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO media_assets
      (id,owner_user_id,owner_type,owner_id,upload_session_id,usage_type,storage_key,sha256,
       media_type,byte_size,width,height,opaque,processor_revision,deleted_at,retention_until,created_at,updated_at)
      VALUES (?,?, 'upload_draft', ?,?,?, ?,?,?, ?,?,?,?, ?,NULL,NULL,?,?)`,
		asset.ID, asset.OwnerUserID, asset.OwnerID, asset.UploadSessionID, asset.UsageType,
		asset.StorageKey, asset.SHA256, asset.MediaType, asset.ByteSize, asset.Width, asset.Height,
		asset.Opaque, asset.ProcessorRevision, now, now)
	if err != nil {
		return Asset{}, err
	}
	if err := tx.Commit(); err != nil {
		return Asset{}, err
	}
	return asset, nil
}

type rowQuerier interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func (r *SQLRepository) validateDraft(ctx context.Context, q rowQuerier, sessionID, userID string, now time.Time, lock bool) error {
	var ownerType, ownerUser, claimed string
	var expires time.Time
	var claimedNull sql.NullString
	query := `SELECT user_id,owner_type,claimed_stream_id,expires_at FROM media_upload_sessions WHERE id=?`
	if lock {
		query += ` FOR UPDATE`
	}
	err := q.QueryRowContext(ctx, query, sessionID).Scan(&ownerUser, &ownerType, &claimedNull, &expires)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if claimedNull.Valid {
		claimed = claimedNull.String
	}
	if ownerUser != userID {
		return ErrForbidden
	}
	if ownerType != "upload_draft" || claimed != "" {
		return ErrDraftClaimed
	}
	if !expires.After(now) {
		return ErrDraftExpired
	}
	return nil
}

func (r *SQLRepository) GetAsset(ctx context.Context, userID, assetID string) (Asset, error) {
	row := r.db.QueryRowContext(ctx, assetSelect+` WHERE id=? AND owner_user_id=?`, strings.TrimSpace(assetID), strings.TrimSpace(userID))
	asset, err := scanAsset(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Asset{}, ErrNotFound
	}
	if err != nil {
		return Asset{}, err
	}
	if asset.DeletedAt != nil {
		return Asset{}, ErrNotFound
	}
	return asset, nil
}

func (r *SQLRepository) EnsureVariant(ctx context.Context, userID, assetID string, width, height int, opaque bool) (Variant, error) {
	asset, err := r.GetAsset(ctx, userID, assetID)
	if err != nil {
		return Variant{}, err
	}
	if err := validateDimensions(width, height); err != nil {
		return Variant{}, err
	}
	cropMode := "center_crop"
	if opaque {
		cropMode = "center_crop_opaque"
	}
	if existing, err := r.getVariantBySpec(ctx, asset.ID, width, height, cropMode); err == nil {
		if existing.Status == "ready" {
			if err := r.processor.Verify(processedFromVariant(existing)); err != nil {
				_, _ = r.db.ExecContext(ctx, `UPDATE media_asset_variants SET status='failed',last_error_code='media_asset_integrity',updated_at=? WHERE id=?`, r.now(), existing.ID)
				return Variant{}, ErrIntegrity
			}
		}
		return existing, nil
	} else if !errors.Is(err, ErrNotFound) {
		return Variant{}, err
	}

	id, err := newID()
	if err != nil {
		return Variant{}, err
	}
	now := r.now()
	_, err = r.db.ExecContext(ctx, `INSERT INTO media_asset_variants
      (id,asset_id,target_width,target_height,crop_mode,processor_revision,status,opaque,created_at,updated_at)
      VALUES (?,?,?,?,?,?,'queued',0,?,?)`, id, asset.ID, width, height, cropMode, ProcessorRevision, now, now)
	if err != nil {
		return r.getVariantBySpec(ctx, asset.ID, width, height, cropMode)
	}
	_, err = r.db.ExecContext(ctx, `UPDATE media_asset_variants SET status='processing',updated_at=? WHERE id=? AND status='queued'`, now, id)
	if err != nil {
		return Variant{}, err
	}
	processed, processErr := r.processor.CreateVariant(processedFromAsset(asset), width, height, opaque)
	if processErr != nil {
		_, _ = r.db.ExecContext(ctx, `UPDATE media_asset_variants SET status='failed',last_error_code=?,updated_at=? WHERE id=?`, safeProcessorErrorCode(processErr), r.now(), id)
		return Variant{}, processErr
	}
	now = r.now()
	_, err = r.db.ExecContext(ctx, `UPDATE media_asset_variants SET
      status='ready',storage_key=?,sha256=?,media_type=?,byte_size=?,width=?,height=?,opaque=?,last_error_code=NULL,updated_at=?
      WHERE id=? AND status='processing'`, processed.StorageKey, processed.SHA256, processed.MediaType,
		processed.ByteSize, processed.Width, processed.Height, processed.Opaque, now, id)
	if err != nil {
		return Variant{}, err
	}
	variant, err := r.getVariantBySpec(ctx, asset.ID, width, height, cropMode)
	if err != nil {
		return Variant{}, err
	}
	if variant.Status != "ready" || r.processor.Verify(processedFromVariant(variant)) != nil {
		return Variant{}, ErrIntegrity
	}
	return variant, nil
}

func (r *SQLRepository) getVariantBySpec(ctx context.Context, assetID string, width, height int, cropMode string) (Variant, error) {
	row := r.db.QueryRowContext(ctx, variantSelect+` WHERE asset_id=? AND target_width=? AND target_height=? AND crop_mode=? AND processor_revision=?`, assetID, width, height, cropMode, ProcessorRevision)
	variant, err := scanVariant(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Variant{}, ErrNotFound
	}
	return variant, err
}

func (r *SQLRepository) SoftDeleteAsset(ctx context.Context, userID, assetID string, now time.Time) error {
	if now.IsZero() {
		now = r.now()
	}
	retention := now.Add(DraftRetention)
	result, err := r.db.ExecContext(ctx, `UPDATE media_assets SET deleted_at=COALESCE(deleted_at,?),retention_until=GREATEST(COALESCE(retention_until,?),?),updated_at=? WHERE id=? AND owner_user_id=?`, now, retention, retention, now, strings.TrimSpace(assetID), strings.TrimSpace(userID))
	if err != nil {
		return err
	}
	if affected, _ := result.RowsAffected(); affected == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *SQLRepository) OpenInternalVariant(ctx context.Context, streamID, variantID string) (InternalAsset, error) {
	row := r.db.QueryRowContext(ctx, variantSelect+` WHERE v.id=? AND v.status='ready' AND EXISTS (
      SELECT 1 FROM stream_visual_settings sv
      WHERE sv.stream_id=? AND (sv.background_variant_id=v.id OR sv.cover_variant_id=v.id)
      UNION ALL
      SELECT 1 FROM stream_video_cover_runtime vr
      WHERE vr.stream_id=? AND vr.asset_variant_id=v.id
    )`, strings.TrimSpace(variantID), strings.TrimSpace(streamID), strings.TrimSpace(streamID))
	variant, err := scanVariant(row)
	if errors.Is(err, sql.ErrNoRows) {
		return InternalAsset{}, ErrForbidden
	}
	if err != nil {
		return InternalAsset{}, err
	}
	assetRow := r.db.QueryRowContext(ctx, assetSelect+` WHERE id=?`, variant.AssetID)
	asset, err := scanAsset(assetRow)
	if err != nil {
		return InternalAsset{}, ErrIntegrity
	}
	if err := r.processor.Verify(processedFromVariant(variant)); err != nil {
		return InternalAsset{}, ErrIntegrity
	}
	reader, err := r.storage.Open(variant.StorageKey)
	if err != nil {
		return InternalAsset{}, ErrIntegrity
	}
	return InternalAsset{Asset: asset, Variant: variant, Reader: reader}, nil
}

func (r *SQLRepository) VerifyVariant(ctx context.Context, assetID, variantID string) error {
	variant, err := scanVariant(r.db.QueryRowContext(ctx, variantSelect+` WHERE v.id=? AND v.asset_id=? AND v.status='ready'`, strings.TrimSpace(variantID), strings.TrimSpace(assetID)))
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	return r.processor.Verify(processedFromVariant(variant))
}

func (r *SQLRepository) ClaimDraftTx(ctx context.Context, tx *sql.Tx, userID, sessionID, streamID string, now time.Time) error {
	if tx == nil || strings.TrimSpace(userID) == "" || strings.TrimSpace(sessionID) == "" || strings.TrimSpace(streamID) == "" {
		return ErrForbidden
	}
	var ownerUser, ownerType string
	var claimed sql.NullString
	var expires time.Time
	err := tx.QueryRowContext(ctx, `SELECT user_id,owner_type,claimed_stream_id,expires_at FROM media_upload_sessions WHERE id=? FOR UPDATE`, sessionID).Scan(&ownerUser, &ownerType, &claimed, &expires)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if ownerUser != userID {
		return ErrForbidden
	}
	if ownerType == "stream" && claimed.Valid && claimed.String == streamID {
		return nil
	}
	if ownerType != "upload_draft" || claimed.Valid {
		return ErrDraftClaimed
	}
	if !expires.After(now) {
		return ErrDraftExpired
	}
	var foreignAssets int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM media_assets WHERE upload_session_id=? AND owner_user_id<>?`, sessionID, userID).Scan(&foreignAssets); err != nil {
		return err
	}
	if foreignAssets != 0 {
		return ErrForbidden
	}
	if _, err := tx.ExecContext(ctx, `UPDATE media_upload_sessions SET owner_type='stream',claimed_stream_id=?,updated_at=? WHERE id=?`, streamID, now, sessionID); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE media_assets SET owner_type='stream',owner_id=?,updated_at=? WHERE upload_session_id=? AND owner_user_id=? AND owner_type='upload_draft'`, streamID, now, sessionID, userID)
	return err
}

func (r *SQLRepository) GarbageCollect(ctx context.Context, now time.Time, limit int) (int, error) {
	if limit < 1 || limit > 500 {
		return 0, errors.New("gc limit must be between 1 and 500")
	}
	if now.IsZero() {
		now = r.now()
	}
	rows, err := r.db.QueryContext(ctx, `SELECT DISTINCT a.id FROM media_assets a
      LEFT JOIN media_upload_sessions us ON us.id=a.upload_session_id
      WHERE ((a.deleted_at IS NOT NULL AND a.retention_until IS NOT NULL AND a.retention_until<=?)
        OR (a.owner_type='upload_draft' AND us.owner_type='upload_draft' AND us.expires_at<=?))
      AND NOT EXISTS (SELECT 1 FROM stream_visual_settings sv WHERE sv.background_asset_id=a.id OR sv.cover_asset_id=a.id)
      AND NOT EXISTS (SELECT 1 FROM video_cover_presets p WHERE p.asset_id=a.id AND p.deleted_at IS NULL)
      AND NOT EXISTS (SELECT 1 FROM stream_video_cover_runtime vr JOIN media_asset_variants v ON v.id=vr.asset_variant_id WHERE v.asset_id=a.id)
      ORDER BY a.id LIMIT ?`, now, now, limit)
	if err != nil {
		return 0, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return 0, err
		}
		ids = append(ids, id)
	}
	if err := rows.Close(); err != nil {
		return 0, err
	}
	removed := 0
	for _, id := range ids {
		count, err := r.deleteUnreferencedAsset(ctx, id, now)
		if err != nil {
			return removed, err
		}
		removed += count
	}
	_, _ = r.db.ExecContext(ctx, `DELETE us FROM media_upload_sessions us
      WHERE us.expires_at<=? AND (
        us.owner_type='stream' OR
        (us.owner_type='upload_draft' AND NOT EXISTS (SELECT 1 FROM media_assets a WHERE a.upload_session_id=us.id))
      )`, now)
	return removed, nil
}

func (r *SQLRepository) deleteUnreferencedAsset(ctx context.Context, id string, now time.Time) (int, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	var assetKey string
	if err := tx.QueryRowContext(ctx, `SELECT storage_key FROM media_assets WHERE id=? FOR UPDATE`, id).Scan(&assetKey); errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	} else if err != nil {
		return 0, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT storage_key FROM media_asset_variants WHERE asset_id=? AND storage_key IS NOT NULL`, id)
	if err != nil {
		return 0, err
	}
	keys := []string{assetKey}
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			_ = rows.Close()
			return 0, err
		}
		keys = append(keys, key)
	}
	_ = rows.Close()
	// A deleted preset is a retention tombstone, not a permanent asset
	// reference. Purge it only after the asset itself is eligible and only when
	// no saved stream still points at that preset. The final asset DELETE keeps
	// a second fail-closed check for any preset row the purge retained.
	if _, err := tx.ExecContext(ctx, `DELETE FROM video_cover_presets
      WHERE asset_id=? AND deleted_at IS NOT NULL
      AND NOT EXISTS (SELECT 1 FROM stream_visual_settings sv WHERE sv.cover_preset_id=video_cover_presets.id)`, id); err != nil {
		return 0, err
	}
	result, err := tx.ExecContext(ctx, `DELETE FROM media_assets WHERE id=?
      AND NOT EXISTS (SELECT 1 FROM stream_visual_settings sv WHERE sv.background_asset_id=media_assets.id OR sv.cover_asset_id=media_assets.id)
      AND NOT EXISTS (SELECT 1 FROM video_cover_presets p WHERE p.asset_id=media_assets.id)
      AND NOT EXISTS (SELECT 1 FROM stream_video_cover_runtime vr JOIN media_asset_variants v ON v.id=vr.asset_variant_id WHERE v.asset_id=media_assets.id)`, id)
	if err != nil {
		return 0, err
	}
	affected, _ := result.RowsAffected()
	if affected == 0 {
		return 0, nil
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	for _, key := range keys {
		var references int
		if err := r.db.QueryRowContext(ctx, `SELECT
          (SELECT COUNT(*) FROM media_assets WHERE storage_key=?) +
          (SELECT COUNT(*) FROM media_asset_variants WHERE storage_key=?)`, key, key).Scan(&references); err == nil && references == 0 {
			_ = r.storage.Remove(key)
		}
	}
	return 1, nil
}

const assetSelect = `SELECT id,owner_user_id,owner_type,owner_id,COALESCE(upload_session_id,''),usage_type,
  storage_key,sha256,media_type,byte_size,width,height,opaque,processor_revision,deleted_at,retention_until,created_at,updated_at
  FROM media_assets`

type scanner interface{ Scan(...any) error }

func scanAsset(row scanner) (Asset, error) {
	var asset Asset
	var deleted, retention sql.NullTime
	err := row.Scan(&asset.ID, &asset.OwnerUserID, &asset.OwnerType, &asset.OwnerID, &asset.UploadSessionID,
		&asset.UsageType, &asset.StorageKey, &asset.SHA256, &asset.MediaType, &asset.ByteSize, &asset.Width,
		&asset.Height, &asset.Opaque, &asset.ProcessorRevision, &deleted, &retention, &asset.CreatedAt, &asset.UpdatedAt)
	if deleted.Valid {
		asset.DeletedAt = &deleted.Time
	}
	if retention.Valid {
		asset.RetentionUntil = &retention.Time
	}
	return asset, err
}

const variantSelect = `SELECT v.id,v.asset_id,v.target_width,v.target_height,v.crop_mode,v.processor_revision,v.status,
  COALESCE(v.storage_key,''),COALESCE(v.sha256,''),COALESCE(v.media_type,''),COALESCE(v.byte_size,0),
  COALESCE(v.width,0),COALESCE(v.height,0),v.opaque,COALESCE(v.last_error_code,''),v.created_at,v.updated_at
  FROM media_asset_variants v`

func scanVariant(row scanner) (Variant, error) {
	var variant Variant
	err := row.Scan(&variant.ID, &variant.AssetID, &variant.TargetWidth, &variant.TargetHeight,
		&variant.CropMode, &variant.ProcessorRevision, &variant.Status, &variant.StorageKey,
		&variant.SHA256, &variant.MediaType, &variant.ByteSize, &variant.Width, &variant.Height,
		&variant.Opaque, &variant.LastErrorCode, &variant.CreatedAt, &variant.UpdatedAt)
	return variant, err
}

func processedFromAsset(asset Asset) ProcessedImage {
	return ProcessedImage{StorageKey: asset.StorageKey, SHA256: asset.SHA256, MediaType: asset.MediaType, ByteSize: asset.ByteSize, Width: asset.Width, Height: asset.Height, Opaque: asset.Opaque}
}

func processedFromVariant(variant Variant) ProcessedImage {
	return ProcessedImage{StorageKey: variant.StorageKey, SHA256: variant.SHA256, MediaType: variant.MediaType, ByteSize: variant.ByteSize, Width: variant.Width, Height: variant.Height, Opaque: variant.Opaque}
}

var _ Repository = (*SQLRepository)(nil)
var _ DraftClaimer = (*SQLRepository)(nil)
var _ IntegrityInspector = (*SQLRepository)(nil)
