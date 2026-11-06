package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

// Caller has already locked and authenticated the exact primary encoder.
// The row is immutable, including when a later run reuses the same Node.
func recordStreamArchiveRunSourceTx(ctx context.Context, tx *sql.Tx, stream Stream, serviceID string) error {
	if strings.TrimSpace(stream.ArchiveRunID) == "" && stream.ArchiveStartedAt == nil {
		return nil
	}
	key, err := streamArchiveRunSourceKeyFor(stream)
	if err != nil {
		return err
	}
	serviceID = strings.TrimSpace(serviceID)
	if serviceID == "" {
		return ErrServiceAssignmentConflict
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO stream_archive_run_sources
(stream_id, archive_run_id, archive_started_at, service_id, created_at)
VALUES (?, ?, ?, ?, ?)
ON DUPLICATE KEY UPDATE service_id = service_id`,
		key.streamID, key.archiveRunID, key.archiveStartedAt, serviceID, time.Now().UTC()); err != nil {
		return err
	}
	var source string
	var startedAt time.Time
	if err := tx.QueryRowContext(ctx, `SELECT service_id, archive_started_at
FROM stream_archive_run_sources WHERE stream_id = ? AND archive_run_id = ? FOR UPDATE`,
		key.streamID, key.archiveRunID).Scan(&source, &startedAt); err != nil {
		return err
	}
	if source != serviceID || !startedAt.UTC().Equal(key.archiveStartedAt) {
		return ErrServiceAssignmentConflict
	}
	return nil
}

func streamArchiveRunSourceForReportTx(ctx context.Context, tx *sql.Tx, streamID string, artifacts []StreamArtifact) (string, bool, error) {
	source := ""
	found := false
	for _, artifact := range artifacts {
		var currentSource string
		var startedAt time.Time
		err := tx.QueryRowContext(ctx, `SELECT service_id, archive_started_at
FROM stream_archive_run_sources WHERE stream_id = ? AND archive_run_id = ? FOR UPDATE`,
			streamID, artifact.ArchiveRunID).Scan(&currentSource, &startedAt)
		if errors.Is(err, sql.ErrNoRows) {
			if found {
				return "", false, ErrForbidden
			}
			continue
		}
		if err != nil {
			return "", false, err
		}
		if artifact.ArchiveStartedAt == nil || !startedAt.UTC().Equal(artifact.ArchiveStartedAt.UTC()) ||
			currentSource == "" || (found && currentSource != source) {
			return "", false, ErrForbidden
		}
		source, found = currentSource, true
	}
	return source, found, nil
}

func validateStreamArtifactReportIdentityTx(ctx context.Context, tx *sql.Tx, streamID, serviceID string, artifacts []StreamArtifact) error {
	for _, artifact := range artifacts {
		var existing StreamArtifact
		var startedAt sql.NullTime
		err := tx.QueryRowContext(ctx, `SELECT archive_started_at, source_service_id
FROM stream_artifacts WHERE stream_id = ? AND archive_run_id = ? AND kind = ? AND name = ? FOR UPDATE`,
			streamID, artifact.ArchiveRunID, artifact.Kind, artifact.Name).Scan(&startedAt, &existing.SourceServiceID)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return err
		}
		existing.ArchiveStartedAt = nullTimePtr(startedAt)
		if streamArtifactReportHasConflictingIdentity(existing, artifact, serviceID) {
			return ErrForbidden
		}
	}
	return nil
}
