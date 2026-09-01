package videocover

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"strings"
	"time"

	"github.com/example/autostream-control-panel/internal/mediaassets"
)

type SQLRepository struct {
	db  *sql.DB
	now func() time.Time
}

func NewMariaDBRepository(db *sql.DB) *SQLRepository {
	return &SQLRepository{db: db, now: func() time.Time { return time.Now().UTC() }}
}

const presetSelect = `SELECT p.id,p.name,p.asset_id,p.asset_variant_id,p.enabled,p.system_preset,COALESCE(p.release_key,''),p.revision,COALESCE(p.created_by_user_id,''),COALESCE(p.updated_by_user_id,''),p.deleted_at,p.created_at,p.updated_at,(a.width<1920 OR a.height<1080) FROM video_cover_presets p JOIN media_assets a ON a.id=p.asset_id`

type scanner interface{ Scan(...any) error }

func scanPreset(row scanner) (Preset, error) {
	var p Preset
	var deleted sql.NullTime
	err := row.Scan(&p.ID, &p.Name, &p.AssetID, &p.AssetVariantID, &p.Enabled, &p.SystemPreset, &p.ReleaseKey, &p.Revision, &p.CreatedByUserID, &p.UpdatedByUserID, &deleted, &p.CreatedAt, &p.UpdatedAt, &p.LowResolutionWarning)
	if deleted.Valid {
		p.DeletedAt = &deleted.Time
	}
	return p, err
}
func (s *SQLRepository) ListPresets(ctx context.Context) ([]Preset, error) {
	rows, err := s.db.QueryContext(ctx, presetSelect+` WHERE p.deleted_at IS NULL ORDER BY LOWER(p.name),p.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []Preset{}
	for rows.Next() {
		p, err := scanPreset(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, p)
	}
	return result, rows.Err()
}
func (s *SQLRepository) GetPreset(ctx context.Context, id string, includeDeleted bool) (Preset, error) {
	query := presetSelect + ` WHERE p.id=?`
	if !includeDeleted {
		query += ` AND p.deleted_at IS NULL`
	}
	p, err := scanPreset(s.db.QueryRowContext(ctx, query, strings.TrimSpace(id)))
	if errors.Is(err, sql.ErrNoRows) {
		return Preset{}, ErrNotFound
	}
	return p, err
}
func (s *SQLRepository) validatePresetAsset(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, p Preset) error {
	if err := validatePreset(p); err != nil {
		return err
	}
	var status, usage string
	var sourceWidth, sourceHeight, targetWidth, targetHeight int
	var opaque bool
	err := q.QueryRowContext(ctx, `SELECT v.status,a.usage_type,a.width,a.height,v.width,v.height,v.opaque FROM media_assets a JOIN media_asset_variants v ON v.asset_id=a.id WHERE a.id=? AND v.id=? AND a.deleted_at IS NULL`, p.AssetID, p.AssetVariantID).Scan(&status, &usage, &sourceWidth, &sourceHeight, &targetWidth, &targetHeight, &opaque)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if status != "ready" || usage != "video_cover" || targetWidth != 1920 || targetHeight != 1080 || !opaque {
		return ErrInvalidRequest
	}
	ratio := float64(sourceWidth)/float64(sourceHeight)/(16.0/9.0) - 1
	if ratio < -0.001 || ratio > 0.001 {
		return ErrInvalidRequest
	}
	return nil
}
func (s *SQLRepository) CreatePreset(ctx context.Context, p Preset) (Preset, error) {
	p.Name = strings.TrimSpace(p.Name)
	now := s.now()
	id, err := randomID()
	if err != nil {
		return Preset{}, err
	}
	p.ID = id
	p.Revision = 1
	p.UpdatedByUserID = p.CreatedByUserID
	p.CreatedAt = now
	p.UpdatedAt = now
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Preset{}, err
	}
	defer tx.Rollback()
	if err := s.validatePresetAsset(ctx, tx, p); err != nil {
		return Preset{}, err
	}
	if err := claimPresetAsset(ctx, tx, p.AssetID, p.ID, p.CreatedByUserID, now); err != nil {
		return Preset{}, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO video_cover_presets(id,name,asset_id,asset_variant_id,enabled,system_preset,release_key,revision,created_by_user_id,updated_by_user_id,deleted_at,created_at,updated_at) VALUES(?,?,?,?,?,?,?,1,?,?,NULL,?,?)`, p.ID, p.Name, p.AssetID, p.AssetVariantID, p.Enabled, p.SystemPreset, nullString(p.ReleaseKey), nullString(p.CreatedByUserID), nullString(p.UpdatedByUserID), now, now)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "duplicate") {
			return Preset{}, ErrIdempotencyConflict
		}
		return Preset{}, err
	}
	if err := tx.Commit(); err != nil {
		return Preset{}, err
	}
	return s.GetPreset(ctx, p.ID, false)
}
func (s *SQLRepository) UpdatePreset(ctx context.Context, id string, p Preset, expected uint64) (Preset, error) {
	p.Name = strings.TrimSpace(p.Name)
	id = strings.TrimSpace(id)
	now := s.now()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Preset{}, err
	}
	defer tx.Rollback()
	var currentAsset string
	var currentRevision uint64
	var deleted sql.NullTime
	if err := tx.QueryRowContext(ctx, `SELECT asset_id,revision,deleted_at FROM video_cover_presets WHERE id=? FOR UPDATE`, id).Scan(&currentAsset, &currentRevision, &deleted); errors.Is(err, sql.ErrNoRows) || deleted.Valid {
		return Preset{}, ErrNotFound
	} else if err != nil {
		return Preset{}, err
	}
	if currentRevision != expected {
		return Preset{}, ErrRevisionConflict
	}
	if err := s.validatePresetAsset(ctx, tx, p); err != nil {
		return Preset{}, err
	}
	if err := claimPresetAsset(ctx, tx, p.AssetID, id, p.UpdatedByUserID, now); err != nil {
		return Preset{}, err
	}
	result, err := tx.ExecContext(ctx, `UPDATE video_cover_presets SET name=?,asset_id=?,asset_variant_id=?,enabled=?,revision=revision+1,updated_by_user_id=?,updated_at=? WHERE id=? AND deleted_at IS NULL AND revision=?`, p.Name, p.AssetID, p.AssetVariantID, p.Enabled, nullString(p.UpdatedByUserID), now, id, expected)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "duplicate") {
			return Preset{}, ErrIdempotencyConflict
		}
		return Preset{}, err
	}
	if n, _ := result.RowsAffected(); n != 1 {
		if _, getErr := s.GetPreset(ctx, id, false); errors.Is(getErr, ErrNotFound) {
			return Preset{}, ErrNotFound
		}
		return Preset{}, ErrRevisionConflict
	}
	if currentAsset != p.AssetID {
		if err := retirePresetAsset(ctx, tx, currentAsset, id, now); err != nil {
			return Preset{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return Preset{}, err
	}
	return s.GetPreset(ctx, id, false)
}
func (s *SQLRepository) DeletePreset(ctx context.Context, id, actor string, expected uint64) (Preset, error) {
	now := s.now()
	id = strings.TrimSpace(id)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Preset{}, err
	}
	defer tx.Rollback()
	var assetID string
	var revision uint64
	var deleted sql.NullTime
	if err := tx.QueryRowContext(ctx, `SELECT asset_id,revision,deleted_at FROM video_cover_presets WHERE id=? FOR UPDATE`, id).Scan(&assetID, &revision, &deleted); errors.Is(err, sql.ErrNoRows) || deleted.Valid {
		return Preset{}, ErrNotFound
	} else if err != nil {
		return Preset{}, err
	}
	if revision != expected {
		return Preset{}, ErrRevisionConflict
	}
	result, err := tx.ExecContext(ctx, `UPDATE video_cover_presets SET deleted_at=?,revision=revision+1,updated_by_user_id=?,updated_at=? WHERE id=? AND deleted_at IS NULL AND revision=?`, now, nullString(actor), now, id, expected)
	if err != nil {
		return Preset{}, err
	}
	if n, _ := result.RowsAffected(); n != 1 {
		if _, getErr := s.GetPreset(ctx, id, false); errors.Is(getErr, ErrNotFound) {
			return Preset{}, ErrNotFound
		}
		return Preset{}, ErrRevisionConflict
	}
	if err := retirePresetAsset(ctx, tx, assetID, id, now); err != nil {
		return Preset{}, err
	}
	if err := tx.Commit(); err != nil {
		return Preset{}, err
	}
	return s.GetPreset(ctx, id, true)
}

func claimPresetAsset(ctx context.Context, tx *sql.Tx, assetID, presetID, actor string, now time.Time) error {
	actor = strings.TrimSpace(actor)
	assetID = strings.TrimSpace(assetID)
	var observedOwnerType string
	var observedUploadSession sql.NullString
	if err := tx.QueryRowContext(ctx, `SELECT owner_type,upload_session_id FROM media_assets WHERE id=?`, assetID).Scan(&observedOwnerType, &observedUploadSession); errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	} else if err != nil {
		return err
	}
	// Keep the same session -> asset lock order as stream draft claims. Locking
	// the asset first would deadlock against ClaimDraftTx under contention.
	if observedOwnerType == "upload_draft" {
		if actor == "" || !observedUploadSession.Valid || strings.TrimSpace(observedUploadSession.String) == "" {
			return ErrInvalidRequest
		}
		var sessionUser, sessionOwnerType string
		var expiresAt time.Time
		if err := tx.QueryRowContext(ctx, `SELECT user_id,owner_type,expires_at FROM media_upload_sessions WHERE id=? FOR UPDATE`, observedUploadSession.String).Scan(&sessionUser, &sessionOwnerType, &expiresAt); errors.Is(err, sql.ErrNoRows) {
			return ErrInvalidRequest
		} else if err != nil {
			return err
		}
		if sessionUser != actor || sessionOwnerType != "upload_draft" || !expiresAt.After(now) {
			return ErrInvalidRequest
		}
	}

	var ownerUser, ownerType, ownerID string
	var uploadSession sql.NullString
	var deleted sql.NullTime
	err := tx.QueryRowContext(ctx, `SELECT owner_user_id,owner_type,owner_id,upload_session_id,deleted_at FROM media_assets WHERE id=? FOR UPDATE`, assetID).Scan(&ownerUser, &ownerType, &ownerID, &uploadSession, &deleted)
	if errors.Is(err, sql.ErrNoRows) || deleted.Valid {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if ownerType == "preset" && ownerID == presetID {
		return nil
	}
	if ownerType != "upload_draft" || actor == "" || ownerUser != actor || !uploadSession.Valid || uploadSession.String != observedUploadSession.String {
		return ErrInvalidRequest
	}
	if _, err := tx.ExecContext(ctx, `UPDATE media_assets SET owner_type='preset',owner_id=?,upload_session_id=NULL,updated_at=? WHERE id=?`, presetID, now, assetID); err != nil {
		return err
	}
	if uploadSession.Valid {
		_, err = tx.ExecContext(ctx, `DELETE FROM media_upload_sessions WHERE id=? AND owner_type='upload_draft' AND NOT EXISTS (SELECT 1 FROM media_assets WHERE upload_session_id=media_upload_sessions.id)`, uploadSession.String)
	}
	return err
}

func retirePresetAsset(ctx context.Context, tx *sql.Tx, assetID, presetID string, now time.Time) error {
	retention := now.Add(mediaassets.DraftRetention)
	_, err := tx.ExecContext(ctx, `UPDATE media_assets SET deleted_at=COALESCE(deleted_at,?),retention_until=GREATEST(COALESCE(retention_until,?),?),updated_at=? WHERE id=? AND owner_type='preset' AND owner_id=?`, now, retention, retention, now, assetID, presetID)
	return err
}

const stateSelect = `SELECT stream_id,job_generation,desired_active,desired_revision,applied_active,applied_revision,COALESCE(asset_variant_id,''),COALESCE(last_error_code,''),reconciliation_status,COALESCE(last_idempotency_key,''),created_at,updated_at FROM stream_video_cover_runtime`

func scanState(row scanner) (State, error) {
	var state State
	var appliedActive sql.NullBool
	var appliedRevision sql.NullInt64
	err := row.Scan(&state.StreamID, &state.JobGeneration, &state.DesiredActive, &state.DesiredRevision, &appliedActive, &appliedRevision, &state.AssetVariantID, &state.LastErrorCode, &state.Status, &state.LastIdempotencyKey, &state.CreatedAt, &state.UpdatedAt)
	if appliedActive.Valid {
		v := appliedActive.Bool
		state.AppliedActive = &v
	}
	if appliedRevision.Valid {
		v := uint64(appliedRevision.Int64)
		state.AppliedRevision = &v
	}
	return NormalizeState(state), err
}
func (s *SQLRepository) EnsureGeneration(ctx context.Context, streamID string, generation uint64, variantID string, desired bool) (State, error) {
	if generation < 1 {
		return State{}, ErrInvalidRequest
	}
	now := s.now()
	_, err := s.db.ExecContext(ctx, `INSERT INTO stream_video_cover_runtime(stream_id,job_generation,desired_active,desired_revision,applied_active,applied_revision,asset_variant_id,last_error_code,reconciliation_status,last_idempotency_key,created_at,updated_at) VALUES(?,?,?,1,NULL,NULL,?,NULL,'idle',NULL,?,?) ON DUPLICATE KEY UPDATE stream_id=stream_id`, strings.TrimSpace(streamID), generation, desired, nullString(variantID), now, now)
	if err != nil {
		return State{}, err
	}
	return scanState(s.db.QueryRowContext(ctx, stateSelect+` WHERE stream_id=? AND job_generation=?`, streamID, generation))
}
func (s *SQLRepository) GetCurrentState(ctx context.Context, streamID string) (State, error) {
	state, err := scanState(s.db.QueryRowContext(ctx, stateSelect+` WHERE stream_id=? ORDER BY job_generation DESC LIMIT 1`, strings.TrimSpace(streamID)))
	if errors.Is(err, sql.ErrNoRows) {
		return State{}, ErrNotFound
	}
	return state, err
}
func (s *SQLRepository) PrepareAction(ctx context.Context, streamID string, request ActionRequest) (PreparedAction, error) {
	if err := ValidateRequest(request); err != nil {
		return PreparedAction{}, err
	}
	streamID = strings.TrimSpace(streamID)
	request.IdempotencyKey = strings.TrimSpace(request.IdempotencyKey)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return PreparedAction{}, err
	}
	defer tx.Rollback()
	state, err := scanState(tx.QueryRowContext(ctx, stateSelect+` WHERE stream_id=? ORDER BY job_generation DESC LIMIT 1 FOR UPDATE`, streamID))
	if errors.Is(err, sql.ErrNoRows) {
		return PreparedAction{}, ErrNotFound
	}
	if err != nil {
		return PreparedAction{}, err
	}
	if state.JobGeneration != request.ExpectedJobGeneration {
		return PreparedAction{}, ErrStaleGeneration
	}
	fingerprint := RequestFingerprint(request)
	var priorFingerprint string
	var priorRevision uint64
	err = tx.QueryRowContext(ctx, `SELECT request_fingerprint,requested_revision FROM stream_video_cover_actions WHERE stream_id=? AND job_generation=? AND idempotency_key=?`, streamID, state.JobGeneration, request.IdempotencyKey).Scan(&priorFingerprint, &priorRevision)
	if err == nil {
		if priorFingerprint != fingerprint {
			return PreparedAction{}, ErrIdempotencyConflict
		}
		if err = tx.Commit(); err != nil {
			return PreparedAction{}, err
		}
		return PreparedAction{State: state, Replay: true, Dispatch: false, RequestedRevision: priorRevision}, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return PreparedAction{}, err
	}
	if state.DesiredRevision != request.ExpectedRevision {
		return PreparedAction{}, ErrRevisionConflict
	}
	next := state.DesiredRevision + 1
	now := s.now()
	_, err = tx.ExecContext(ctx, `INSERT INTO stream_video_cover_actions(stream_id,job_generation,idempotency_key,requested_active,requested_revision,request_fingerprint,result_status,safe_error_code,created_at,updated_at) VALUES(?,?,?,?,?,?,'pending',NULL,?,?)`, streamID, state.JobGeneration, request.IdempotencyKey, request.Active, next, fingerprint, now, now)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "duplicate") {
			return PreparedAction{}, ErrRevisionConflict
		}
		return PreparedAction{}, err
	}
	result, err := tx.ExecContext(ctx, `UPDATE stream_video_cover_runtime SET desired_active=?,desired_revision=?,last_error_code=NULL,reconciliation_status='idle',last_idempotency_key=?,updated_at=? WHERE stream_id=? AND job_generation=? AND desired_revision=?`, request.Active, next, request.IdempotencyKey, now, streamID, state.JobGeneration, state.DesiredRevision)
	if err != nil {
		return PreparedAction{}, err
	}
	if n, _ := result.RowsAffected(); n != 1 {
		return PreparedAction{}, ErrRevisionConflict
	}
	if err = tx.Commit(); err != nil {
		return PreparedAction{}, err
	}
	state.DesiredActive = request.Active
	state.DesiredRevision = next
	state.LastIdempotencyKey = request.IdempotencyKey
	state.Status = "idle"
	state.LastErrorCode = ""
	state.UpdatedAt = now
	return PreparedAction{State: NormalizeState(state), Dispatch: true, RequestedRevision: next}, nil
}
func (s *SQLRepository) RecordApplied(ctx context.Context, streamID string, generation uint64, key string, active bool, revision uint64) (State, error) {
	return s.record(ctx, streamID, generation, key, "applied", "", &active, &revision)
}
func (s *SQLRepository) RecordAmbiguous(ctx context.Context, streamID string, generation uint64, key string) (State, error) {
	return s.record(ctx, streamID, generation, key, "confirming", "transport_outcome_unknown", nil, nil)
}
func (s *SQLRepository) RecordFailed(ctx context.Context, streamID string, generation uint64, key, code string) (State, error) {
	return s.record(ctx, streamID, generation, key, "failed", safeCode(code), nil, nil)
}
func (s *SQLRepository) record(ctx context.Context, streamID string, generation uint64, key, status, code string, active *bool, revision *uint64) (State, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return State{}, err
	}
	defer tx.Rollback()
	var requestedRevision uint64
	if err = tx.QueryRowContext(ctx, `SELECT requested_revision FROM stream_video_cover_actions WHERE stream_id=? AND job_generation=? AND idempotency_key=? FOR UPDATE`, streamID, generation, key).Scan(&requestedRevision); errors.Is(err, sql.ErrNoRows) {
		return State{}, ErrNotFound
	} else if err != nil {
		return State{}, err
	}
	if revision != nil && requestedRevision != *revision {
		return State{}, ErrRevisionConflict
	}
	now := s.now()
	if _, err = tx.ExecContext(ctx, `UPDATE stream_video_cover_actions SET result_status=?,safe_error_code=?,updated_at=? WHERE stream_id=? AND job_generation=? AND idempotency_key=?`, status, nullString(code), now, streamID, generation, key); err != nil {
		return State{}, err
	}
	if active != nil && revision != nil {
		_, err = tx.ExecContext(ctx, `UPDATE stream_video_cover_runtime SET applied_active=?,applied_revision=?,last_error_code=NULL,reconciliation_status='applied',updated_at=? WHERE stream_id=? AND job_generation=?`, *active, *revision, now, streamID, generation)
	} else {
		_, err = tx.ExecContext(ctx, `UPDATE stream_video_cover_runtime SET last_error_code=?,reconciliation_status=?,updated_at=? WHERE stream_id=? AND job_generation=?`, nullString(code), status, now, streamID, generation)
	}
	if err != nil {
		return State{}, err
	}
	if err = tx.Commit(); err != nil {
		return State{}, err
	}
	return scanState(s.db.QueryRowContext(ctx, stateSelect+` WHERE stream_id=? AND job_generation=?`, streamID, generation))
}
func nullString(value string) any {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	return value
}
func randomID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	e := hex.EncodeToString(b[:])
	return e[:8] + "-" + e[8:12] + "-" + e[12:16] + "-" + e[16:20] + "-" + e[20:], nil
}

var _ Repository = (*SQLRepository)(nil)
