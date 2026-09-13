package store

import (
	"context"
	"database/sql"
	"strings"
	"time"
)

func (s MariaDBStreamStore) ListStreams(ctx context.Context) ([]Stream, error) {
	rows, err := s.db.QueryContext(ctx, streamListQuery("s.deleted_at IS NULL"))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var streams []Stream
	for rows.Next() {
		stream, err := scanStreamRow(rows)
		if err != nil {
			return nil, err
		}
		streams = append(streams, stream)
	}
	return streams, rows.Err()
}

const archiveRecordingArtifactExistsCondition = `EXISTS (
  SELECT 1
  FROM stream_artifacts a
  WHERE a.stream_id = s.id
    AND LOWER(TRIM(a.kind)) = 'archive'
    AND (
      LOWER(TRIM(a.name)) LIKE '%.mp4'
      OR LOWER(TRIM(a.name)) LIKE '%.webm'
      OR LOWER(TRIM(a.name)) LIKE '%.m4v'
      OR LOWER(TRIM(a.name)) LIKE '%.mov'
      OR LOWER(TRIM(a.name)) LIKE '%.mkv'
    )
)`

func (s MariaDBStreamStore) ListArchiveStreams(ctx context.Context) ([]Stream, error) {
	rows, err := s.db.QueryContext(ctx, streamListQuery(archiveRecordingArtifactExistsCondition))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var streams []Stream
	for rows.Next() {
		stream, err := scanStreamRow(rows)
		if err != nil {
			return nil, err
		}
		streams = append(streams, stream)
	}
	return streams, rows.Err()
}

func (s MariaDBStreamStore) ListArchiveProcessingStreams(ctx context.Context) ([]Stream, error) {
	rows, err := s.db.QueryContext(ctx, streamListQuery(`s.deleted_at IS NULL
  AND (
	`+streamLogGuardPendingCondition+`
    OR (
      COALESCE(TRIM(ss.archive_profile_id), '') <> ''
      AND s.archive_started_at IS NOT NULL
      AND s.archive_reported_at IS NULL
      AND LOWER(TRIM(s.status)) IN ('stopping', 'completed', 'ready')
    )
  )`),
		archiveRetryAssignmentGuardLogMessage, archiveRetryAssignmentGuardClosedLogMessage,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var streams []Stream
	for rows.Next() {
		stream, err := scanStreamRow(rows)
		if err != nil {
			return nil, err
		}
		streams = append(streams, stream)
	}
	return streams, rows.Err()
}

const streamSelectFields = `s.id, s.name, s.status, COALESCE(s.archive_run_id, ''), s.archive_started_at, s.archive_reported_at, s.scheduled_start_at, s.scheduled_end_at,
  COALESCE(ss.discord_config_id, ''), COALESCE(ss.auto_start_trigger, ''),
  COALESCE(ss.encoder_profile_id, ''), COALESCE(ss.caption_profile_id, ''),
  COALESCE(ss.overlay_profile_id, ''), COALESCE(ss.encoder_audio_gain_db, 0), COALESCE(ss.archive_profile_id, ''), COALESCE(ss.youtube_output_id, ''),
  COALESCE(ss.archive_drive_destination_id, ''), COALESCE(ss.archive_oauth_account_id, ''),
  CASE WHEN dd.folder_id_fingerprint IS NULL OR dd.folder_id_fingerprint = '' THEN 0 ELSE 1 END,
  COALESCE(dd.masked_folder_id, ''), COALESCE(ss.archive_shared_drive, 0), COALESCE(ss.archive_shared_drive_id, ''),
  COALESCE(ss.archive_file_name, ''), COALESCE(ss.encoder_input_url, ''), s.created_at, s.updated_at, s.deleted_at`

func streamListQuery(where string) string {
	query := `SELECT ` + streamSelectFields + `
FROM streams s
LEFT JOIN stream_settings ss ON ss.stream_id = s.id
LEFT JOIN drive_destinations dd ON dd.id = ss.archive_drive_destination_id`
	if strings.TrimSpace(where) != "" {
		query += ` WHERE ` + where
	}
	return query + ` ORDER BY s.created_at DESC LIMIT 100`
}

type streamRowScanner interface {
	Scan(dest ...any) error
}

func scanStreamRow(scanner streamRowScanner) (Stream, error) {
	var stream Stream
	var archiveStartedAt, archiveReportedAt, scheduledStart, scheduledEnd, deletedAt sql.NullTime
	if err := scanner.Scan(&stream.ID, &stream.Name, &stream.Status, &stream.ArchiveRunID, &archiveStartedAt, &archiveReportedAt, &scheduledStart, &scheduledEnd, &stream.DiscordConfigID, &stream.AutoStartTrigger, &stream.EncoderProfileID, &stream.CaptionProfileID, &stream.OverlayProfileID, &stream.EncoderAudioGainDB, &stream.ArchiveProfileID, &stream.YouTubeOutputID, &stream.ArchiveDriveDestinationID, &stream.ArchiveOAuthAccountID, &stream.ArchiveFolderIDConfigured, &stream.ArchiveMaskedFolderID, &stream.ArchiveSharedDrive, &stream.ArchiveSharedDriveID, &stream.ArchiveFileName, &stream.EncoderInputURL, &stream.CreatedAt, &stream.UpdatedAt, &deletedAt); err != nil {
		return Stream{}, err
	}
	stream.ScheduledStartAt = nullTimePtr(scheduledStart)
	stream.ScheduledEndAt = nullTimePtr(scheduledEnd)
	stream.ArchiveStartedAt = nullTimePtr(archiveStartedAt)
	stream.ArchiveReportedAt = nullTimePtr(archiveReportedAt)
	stream.DeletedAt = nullTimePtr(deletedAt)
	return stream, nil
}

func (s MariaDBStreamStore) PrepareStreamArchiveRun(ctx context.Context, id, archiveRunID string, startedAt time.Time) (Stream, error) {
	archiveRunID = strings.TrimSpace(archiveRunID)
	if !validArchiveRunID(archiveRunID) || startedAt.IsZero() {
		return Stream{}, ErrInvalidStreamArtifact
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Stream{}, err
	}
	defer tx.Rollback()
	state, err := lockMariaDBStreamAssignmentProtection(ctx, tx, strings.TrimSpace(id))
	if err != nil {
		return Stream{}, err
	}
	started := startedAt.UTC()
	now := time.Now().UTC()
	if _, err := tx.ExecContext(ctx, `UPDATE streams SET archive_run_id = ?, archive_started_at = ?, archive_reported_at = NULL, updated_at = ? WHERE id = ?`, archiveRunID, started, now, id); err != nil {
		return Stream{}, err
	}
	if err := tx.Commit(); err != nil {
		return Stream{}, err
	}
	stream := state.Stream
	stream.ArchiveRunID = archiveRunID
	stream.ArchiveStartedAt = cloneTimePtr(&startedAt)
	stream.ArchiveReportedAt = nil
	stream.UpdatedAt = now
	return stream, nil
}

func (s MariaDBStreamStore) HasActiveStream(ctx context.Context) (bool, error) {
	var active bool
	err := s.db.QueryRowContext(ctx, `SELECT EXISTS(
  SELECT 1 FROM streams WHERE LOWER(TRIM(status)) IN ('starting', 'live', 'stopping')
)`).Scan(&active)
	return active, err
}

func (s MariaDBStreamStore) CreateStream(ctx context.Context, name string) (Stream, error) {
	now := time.Now().UTC()
	stream := Stream{ID: newUUID(), Name: name, Status: "created", CreatedAt: now, UpdatedAt: now}
	_, err := s.db.ExecContext(ctx, `INSERT INTO streams (id, name, status, created_at, updated_at) VALUES (?, ?, ?, ?, ?)`, stream.ID, stream.Name, stream.Status, stream.CreatedAt, stream.UpdatedAt)
	return stream, err
}
