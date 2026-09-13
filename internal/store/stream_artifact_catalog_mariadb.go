package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

func (s MariaDBStreamStore) ListStreamArtifacts(ctx context.Context, id string) ([]StreamArtifact, error) {
	if _, err := s.GetStream(ctx, id); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id, stream_id, archive_run_id, archive_started_at, kind, name, relative_path, size_bytes, created_at, source_service_id FROM stream_artifacts WHERE stream_id = ? ORDER BY COALESCE(archive_started_at, created_at) DESC, created_at DESC`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var artifacts []StreamArtifact
	for rows.Next() {
		artifact, err := scanStreamArtifact(rows)
		if err != nil {
			return nil, err
		}
		if isSafeRelativePath(artifact.RelativePath) {
			artifacts = append(artifacts, artifact)
		}
	}
	return artifacts, rows.Err()
}

func (s MariaDBStreamStore) UpsertStreamArtifacts(ctx context.Context, id string, artifacts []StreamArtifact) error {
	if err := ValidateStreamArtifactReport(id, artifacts); err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	state, err := lockMariaDBStreamAssignmentProtection(ctx, tx, id)
	if err != nil {
		return err
	}
	authority := state.Stream
	normalized := NormalizeStreamArtifacts(id, artifacts)
	for _, artifact := range normalized {
		artifact.ID = newUUID()
		artifact.CreatedAt = time.Now().UTC()
		if _, err := tx.ExecContext(ctx, `INSERT INTO stream_artifacts (id, stream_id, archive_run_id, archive_started_at, kind, name, relative_path, size_bytes, created_at, source_service_id) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON DUPLICATE KEY UPDATE relative_path = VALUES(relative_path), size_bytes = VALUES(size_bytes), source_service_id = IF(VALUES(source_service_id) <> '', VALUES(source_service_id), source_service_id)`,
			artifact.ID, id, artifact.ArchiveRunID, artifact.ArchiveStartedAt, artifact.Kind, artifact.Name, artifact.RelativePath, artifact.SizeBytes, artifact.CreatedAt, strings.TrimSpace(artifact.SourceServiceID)); err != nil {
			return err
		}
	}
	if streamArtifactReportMatchesArchiveAuthority(authority, normalized) {
		if err := markStreamArchiveRunReported(ctx, tx, id, authority, normalized); err != nil {
			return err
		}
		closedAt := time.Now().UTC()
		if err := closeMariaDBStreamLogGuard(ctx, tx, id, archiveRetryAssignmentGuardLogMessage, archiveRetryAssignmentGuardClosedLogMessage, closedAt); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s MariaDBStreamStore) DeleteStreamArtifact(ctx context.Context, streamID, artifactID string) error {
	if _, err := s.GetStream(ctx, streamID); err != nil {
		return err
	}
	result, err := s.db.ExecContext(ctx, `DELETE FROM stream_artifacts WHERE stream_id = ? AND id = ?`, streamID, artifactID)
	if err != nil {
		return err
	}
	if affected, _ := result.RowsAffected(); affected == 0 {
		return ErrNotFound
	}
	return nil
}

func (s MariaDBStreamStore) RenameStreamArtifact(ctx context.Context, streamID, artifactID, name string) (StreamArtifact, error) {
	if !isSafeArtifactFileName(name) {
		return StreamArtifact{}, ErrInvalidStreamArtifact
	}
	artifact, err := s.streamArtifactByID(ctx, streamID, artifactID)
	if err != nil {
		return StreamArtifact{}, err
	}
	if artifact.Name == name {
		return artifact, nil
	}
	var conflict string
	if err := s.db.QueryRowContext(ctx, `SELECT id FROM stream_artifacts WHERE stream_id = ? AND archive_run_id = ? AND kind = ? AND name = ? LIMIT 1`, streamID, artifact.ArchiveRunID, artifact.Kind, name).Scan(&conflict); err == nil {
		return StreamArtifact{}, ErrAlreadyExists
	} else if !errors.Is(err, sql.ErrNoRows) {
		return StreamArtifact{}, err
	}
	artifact.Name = name
	artifact.RelativePath = streamArtifactRelativePath(streamID, artifact.ArchiveRunID, name)
	if !isSafeRelativePath(artifact.RelativePath) {
		return StreamArtifact{}, ErrInvalidStreamArtifact
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE stream_artifacts SET name = ?, relative_path = ? WHERE stream_id = ? AND id = ?`, artifact.Name, artifact.RelativePath, streamID, artifactID); err != nil {
		return StreamArtifact{}, err
	}
	return artifact, nil
}

func (s MariaDBStreamStore) CreateStreamArtifactShare(ctx context.Context, share StreamArtifactShare) (StreamArtifactShare, error) {
	share.StreamID = strings.TrimSpace(share.StreamID)
	share.ArtifactID = strings.TrimSpace(share.ArtifactID)
	share.TokenHash = strings.TrimSpace(share.TokenHash)
	share.CreatedByUserID = strings.TrimSpace(share.CreatedByUserID)
	if share.StreamID == "" || share.ArtifactID == "" || share.TokenHash == "" || !share.ExpiresAt.After(time.Now().UTC()) {
		return StreamArtifactShare{}, ErrInvalidStreamArtifact
	}
	if _, err := s.streamArtifactByID(ctx, share.StreamID, share.ArtifactID); err != nil {
		return StreamArtifactShare{}, err
	}
	now := time.Now().UTC()
	share.ID = newUUID()
	share.ExpiresAt = share.ExpiresAt.UTC()
	share.CreatedAt = now
	_, err := s.db.ExecContext(ctx, `INSERT INTO stream_artifact_shares (id, token_hash, stream_id, artifact_id, created_by_user_id, allow_download, expires_at, created_at, revoked_at) VALUES (?, ?, ?, ?, NULLIF(?, ''), ?, ?, ?, NULL)`,
		share.ID, share.TokenHash, share.StreamID, share.ArtifactID, share.CreatedByUserID, share.AllowDownload, share.ExpiresAt, share.CreatedAt)
	if err != nil {
		return StreamArtifactShare{}, err
	}
	return share, nil
}

func (s MariaDBStreamStore) ListStreamArtifactShares(ctx context.Context, streamID, artifactID string) ([]StreamArtifactShare, error) {
	streamID = strings.TrimSpace(streamID)
	artifactID = strings.TrimSpace(artifactID)
	if _, err := s.streamArtifactByID(ctx, streamID, artifactID); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id, token_hash, stream_id, artifact_id, COALESCE(created_by_user_id, ''), allow_download, expires_at, created_at, revoked_at FROM stream_artifact_shares WHERE stream_id = ? AND artifact_id = ? ORDER BY created_at DESC`, streamID, artifactID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var shares []StreamArtifactShare
	for rows.Next() {
		share, err := scanStreamArtifactShare(rows)
		if err != nil {
			return nil, err
		}
		shares = append(shares, share)
	}
	return shares, rows.Err()
}

func (s MariaDBStreamStore) GetStreamArtifactShareByTokenHash(ctx context.Context, tokenHash string) (StreamArtifactShare, error) {
	tokenHash = strings.TrimSpace(tokenHash)
	if tokenHash == "" {
		return StreamArtifactShare{}, ErrNotFound
	}
	row := s.db.QueryRowContext(ctx, `SELECT id, token_hash, stream_id, artifact_id, COALESCE(created_by_user_id, ''), allow_download, expires_at, created_at, revoked_at FROM stream_artifact_shares WHERE token_hash = ?`, tokenHash)
	share, err := scanStreamArtifactShare(row)
	if errors.Is(err, sql.ErrNoRows) {
		return StreamArtifactShare{}, ErrNotFound
	}
	if err != nil {
		return StreamArtifactShare{}, err
	}
	return share, nil
}

func (s MariaDBStreamStore) RevokeStreamArtifactShare(ctx context.Context, streamID, artifactID, shareID string) error {
	streamID = strings.TrimSpace(streamID)
	artifactID = strings.TrimSpace(artifactID)
	shareID = strings.TrimSpace(shareID)
	now := time.Now().UTC()
	result, err := s.db.ExecContext(ctx, `UPDATE stream_artifact_shares SET revoked_at = ? WHERE id = ? AND stream_id = ? AND artifact_id = ? AND revoked_at IS NULL`, now, shareID, streamID, artifactID)
	if err != nil {
		return err
	}
	if affected, _ := result.RowsAffected(); affected == 0 {
		return ErrNotFound
	}
	return nil
}

type streamArtifactShareScanner interface {
	Scan(dest ...any) error
}

func scanStreamArtifactShare(scanner streamArtifactShareScanner) (StreamArtifactShare, error) {
	var share StreamArtifactShare
	var revoked sql.NullTime
	if err := scanner.Scan(&share.ID, &share.TokenHash, &share.StreamID, &share.ArtifactID, &share.CreatedByUserID, &share.AllowDownload, &share.ExpiresAt, &share.CreatedAt, &revoked); err != nil {
		return StreamArtifactShare{}, err
	}
	if revoked.Valid {
		revokedAt := revoked.Time.UTC()
		share.RevokedAt = &revokedAt
	}
	return share, nil
}

func (s MariaDBStreamStore) streamArtifactByID(ctx context.Context, streamID, artifactID string) (StreamArtifact, error) {
	if _, err := s.GetStream(ctx, streamID); err != nil {
		return StreamArtifact{}, err
	}
	artifact, err := scanStreamArtifact(s.db.QueryRowContext(ctx, `SELECT id, stream_id, archive_run_id, archive_started_at, kind, name, relative_path, size_bytes, created_at, source_service_id FROM stream_artifacts WHERE stream_id = ? AND id = ?`, streamID, artifactID))
	if errors.Is(err, sql.ErrNoRows) {
		return StreamArtifact{}, ErrNotFound
	}
	if err != nil {
		return StreamArtifact{}, err
	}
	if !isSafeRelativePath(artifact.RelativePath) {
		return StreamArtifact{}, ErrInvalidStreamArtifact
	}
	return artifact, nil
}
