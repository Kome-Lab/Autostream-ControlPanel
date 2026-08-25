package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type Stream struct {
	ID                        string     `json:"id"`
	Name                      string     `json:"name"`
	Status                    string     `json:"status"`
	ArchiveRunID              string     `json:"archive_run_id,omitempty"`
	ArchiveStartedAt          *time.Time `json:"archive_started_at,omitempty"`
	ArchiveReportedAt         *time.Time `json:"archive_reported_at,omitempty"`
	ScheduledStartAt          *time.Time `json:"scheduled_start_at,omitempty"`
	ScheduledEndAt            *time.Time `json:"scheduled_end_at,omitempty"`
	DiscordConfigID           string     `json:"discord_config_id,omitempty"`
	DiscordGuildID            string     `json:"discord_guild_id,omitempty"`
	DiscordVoiceID            string     `json:"discord_voice_channel_id,omitempty"`
	DiscordTextID             string     `json:"discord_text_channel_id,omitempty"`
	AutoStartTrigger          string     `json:"auto_start_trigger,omitempty"`
	EncoderProfileID          string     `json:"encoder_profile_id,omitempty"`
	CaptionProfileID          string     `json:"caption_profile_id,omitempty"`
	OverlayProfileID          string     `json:"overlay_profile_id,omitempty"`
	EncoderAudioGainDB        float64    `json:"encoder_audio_gain_db"`
	ArchiveProfileID          string     `json:"archive_profile_id,omitempty"`
	ArchiveDriveDestinationID string     `json:"archive_drive_destination_id,omitempty"`
	ArchiveOAuthAccountID     string     `json:"archive_oauth_account_id,omitempty"`
	ArchiveFolderIDConfigured bool       `json:"archive_folder_id_configured,omitempty"`
	ArchiveMaskedFolderID     string     `json:"archive_masked_folder_id,omitempty"`
	ArchiveSharedDrive        bool       `json:"archive_shared_drive,omitempty"`
	ArchiveSharedDriveID      string     `json:"archive_shared_drive_id,omitempty"`
	ArchiveFileName           string     `json:"archive_file_name,omitempty"`
	YouTubeOutputID           string     `json:"youtube_output_id,omitempty"`
	EncoderInputURL           string     `json:"encoder_input_url,omitempty"`
	AssignedWorkerID          string     `json:"assigned_worker_id,omitempty"`
	AssignedEncoderID         string     `json:"assigned_encoder_id,omitempty"`
	CreatedAt                 time.Time  `json:"created_at"`
	UpdatedAt                 time.Time  `json:"updated_at"`
	DeletedAt                 *time.Time `json:"deleted_at,omitempty"`
}

type StreamSettings struct {
	Name                      string     `json:"name,omitempty"`
	ScheduledStartAt          *time.Time `json:"scheduled_start_at,omitempty"`
	ScheduledEndAt            *time.Time `json:"scheduled_end_at,omitempty"`
	DiscordConfigID           string     `json:"discord_config_id,omitempty"`
	DiscordGuildID            string     `json:"discord_guild_id,omitempty"`
	DiscordVoiceID            string     `json:"discord_voice_channel_id,omitempty"`
	DiscordTextID             string     `json:"discord_text_channel_id,omitempty"`
	AutoStartTrigger          string     `json:"auto_start_trigger,omitempty"`
	EncoderProfileID          string     `json:"encoder_profile_id,omitempty"`
	CaptionProfileID          string     `json:"caption_profile_id,omitempty"`
	OverlayProfileID          string     `json:"overlay_profile_id,omitempty"`
	EncoderAudioGainDB        float64    `json:"encoder_audio_gain_db"`
	ArchiveProfileID          string     `json:"archive_profile_id,omitempty"`
	ArchiveDriveDestinationID string     `json:"archive_drive_destination_id,omitempty"`
	ArchiveOAuthAccountID     string     `json:"archive_oauth_account_id,omitempty"`
	ArchiveSharedDrive        bool       `json:"archive_shared_drive,omitempty"`
	ArchiveSharedDriveID      string     `json:"archive_shared_drive_id,omitempty"`
	ArchiveFileName           string     `json:"archive_file_name,omitempty"`
	YouTubeOutputID           string     `json:"youtube_output_id,omitempty"`
	EncoderInputURL           string     `json:"encoder_input_url,omitempty"`
}

type StreamLog struct {
	ID              string         `json:"id"`
	StreamID        string         `json:"stream_id"`
	StreamName      string         `json:"stream_name,omitempty"`
	StreamDeletedAt *time.Time     `json:"stream_deleted_at,omitempty"`
	Level           string         `json:"level"`
	Message         string         `json:"message"`
	Fields          map[string]any `json:"fields"`
	CreatedAt       time.Time      `json:"created_at"`
}

type StreamArtifact struct {
	ID               string     `json:"id"`
	StreamID         string     `json:"stream_id"`
	ArchiveRunID     string     `json:"archive_run_id,omitempty"`
	ArchiveStartedAt *time.Time `json:"archive_started_at,omitempty"`
	Kind             string     `json:"kind"`
	Name             string     `json:"name"`
	RelativePath     string     `json:"relative_path"`
	SizeBytes        int64      `json:"size_bytes"`
	CreatedAt        time.Time  `json:"created_at"`
	SourceServiceID  string     `json:"-"`
}

type StreamArtifactShare struct {
	ID              string     `json:"id"`
	TokenHash       string     `json:"-"`
	StreamID        string     `json:"stream_id"`
	ArtifactID      string     `json:"artifact_id"`
	CreatedByUserID string     `json:"created_by_user_id,omitempty"`
	AllowDownload   bool       `json:"allow_download"`
	ExpiresAt       time.Time  `json:"expires_at"`
	CreatedAt       time.Time  `json:"created_at"`
	RevokedAt       *time.Time `json:"revoked_at,omitempty"`
}

// StreamMediaRuntime records non-secret facts about the media path selected by
// a successful start. It is durable so a Control Panel restart cannot infer a
// burn-in contract from mutable service capability advertisements.
type StreamMediaRuntime struct {
	StreamID           string    `json:"stream_id"`
	VideoOverlayBurnIn bool      `json:"video_overlay_burn_in"`
	UpdatedAt          time.Time `json:"updated_at"`
}

type StreamYouTubeRuntime struct {
	StreamID            string    `json:"stream_id"`
	YouTubeOutput       string    `json:"youtube_output"`
	OAuthAccountID      string    `json:"oauth_account_id,omitempty"`
	Mode                string    `json:"mode"`
	BroadcastID         string    `json:"broadcast_id,omitempty"`
	LiveStreamID        string    `json:"live_stream_id,omitempty"`
	RTMPURL             string    `json:"rtmp_url,omitempty"`
	StreamKeySecretName string    `json:"stream_key_secret_name,omitempty"`
	DryRun              bool      `json:"dry_run"`
	CompleteOnStop      bool      `json:"complete_on_stop"`
	CompleteRetryCount  int       `json:"complete_retry_count,omitempty"`
	CompleteNextRetryAt time.Time `json:"complete_next_retry_at,omitempty"`
	CompleteLastError   string    `json:"complete_last_error,omitempty"`
	CreatedAt           time.Time `json:"created_at"`
	UpdatedAt           time.Time `json:"updated_at"`
}

type StreamStore interface {
	ListStreams(ctx context.Context) ([]Stream, error)
	CreateStream(ctx context.Context, name string) (Stream, error)
	GetStream(ctx context.Context, id string) (Stream, error)
	DeleteStream(ctx context.Context, id string) error
	UpdateStreamSettings(ctx context.Context, id string, settings StreamSettings) (Stream, error)
	UpdateStreamEncoderRuntimeSettings(ctx context.Context, id string, audioGainDB float64, overlayProfileID string) (Stream, error)
	UpdateStreamStatus(ctx context.Context, id, status string) (Stream, error)
	// TransitionStreamStatus updates a stream only when its persisted status still
	// equals expectedStatus. It prevents an asynchronous lifecycle completion from
	// overwriting a newer transition for the same stream.
	TransitionStreamStatus(ctx context.Context, id, expectedStatus, status string) (stream Stream, transitioned bool, err error)
	RetryArchiveUpload(ctx context.Context, id, actorUserID string) (StreamLog, error)
	AppendStreamLog(ctx context.Context, log StreamLog) (StreamLog, error)
	ListStreamLogs(ctx context.Context, id string) ([]StreamLog, error)
	ListStreamLogHistory(ctx context.Context, limit int, before time.Time, beforeID string) ([]StreamLog, error)
	ListStreamArtifacts(ctx context.Context, id string) ([]StreamArtifact, error)
	UpsertStreamArtifacts(ctx context.Context, id string, artifacts []StreamArtifact) error
}

// ActiveStreamStore provides an unbounded active-stream lookup for callers
// that must not depend on the paginated administrative stream listing.
type ActiveStreamStore interface {
	HasActiveStream(ctx context.Context) (bool, error)
}

// ArchiveStreamStore lists stream records that still have a locally managed
// recording artifact, including records that were removed from the operational
// stream list. Sidecars alone do not keep an empty stream in the archive picker.
type ArchiveStreamStore interface {
	ListArchiveStreams(ctx context.Context) ([]Stream, error)
}

// ArchiveProcessingStreamStore lists stopped streams whose configured
// recording has not been reported by the Encoder yet. This includes a
// Discord VC stream already re-armed to ready for its next run. A stream that
// reported artifacts once must not re-enter this list after an operator
// deliberately deletes its last recording.
type ArchiveProcessingStreamStore interface {
	ListArchiveProcessingStreams(ctx context.Context) ([]Stream, error)
}

type StreamArchiveRunStore interface {
	PrepareStreamArchiveRun(ctx context.Context, id, archiveRunID string, startedAt time.Time) (Stream, error)
}

func StreamArchiveRunIDForStart(startedAt time.Time) string {
	jst := time.FixedZone("JST", 9*60*60)
	local := startedAt.In(jst)
	return fmt.Sprintf("%s_%09d_%s", local.Format("20060102_150405"), local.Nanosecond(), local.Format("MST"))
}

type StreamArtifactAdminStore interface {
	DeleteStreamArtifact(ctx context.Context, streamID, artifactID string) error
	RenameStreamArtifact(ctx context.Context, streamID, artifactID, name string) (StreamArtifact, error)
}

type StreamArtifactShareStore interface {
	CreateStreamArtifactShare(ctx context.Context, share StreamArtifactShare) (StreamArtifactShare, error)
	ListStreamArtifactShares(ctx context.Context, streamID, artifactID string) ([]StreamArtifactShare, error)
	GetStreamArtifactShareByTokenHash(ctx context.Context, tokenHash string) (StreamArtifactShare, error)
	RevokeStreamArtifactShare(ctx context.Context, streamID, artifactID, shareID string) error
}

type StreamArtifactReportStore interface {
	WriteStreamArtifactReport(ctx context.Context, token ServiceToken, event ServiceStreamEvent, artifacts []StreamArtifact) error
}

type StreamMediaRuntimeStore interface {
	SetStreamVideoOverlayBurnIn(ctx context.Context, streamID string, enabled bool) error
	GetStreamMediaRuntime(ctx context.Context, streamID string) (StreamMediaRuntime, error)
}

type StreamYouTubeRuntimeStore interface {
	SaveStreamYouTubeRuntime(ctx context.Context, runtime StreamYouTubeRuntime) error
	GetStreamYouTubeRuntime(ctx context.Context, streamID string) (StreamYouTubeRuntime, error)
	ListStreamYouTubeRuntimes(ctx context.Context) ([]StreamYouTubeRuntime, error)
	ListDueStreamYouTubeRuntimes(ctx context.Context, now time.Time, limit int) ([]StreamYouTubeRuntime, error)
	RecordStreamYouTubeRuntimeCompleteFailure(ctx context.Context, streamID, lastError string, nextRetryAt time.Time) (StreamYouTubeRuntime, error)
	DeleteStreamYouTubeRuntime(ctx context.Context, streamID string) error
}

var (
	ErrNotFound = errors.New("not found")
)

type MariaDBStreamStore struct {
	db *sql.DB
}

func NewMariaDBStreamStore(db *sql.DB) MariaDBStreamStore {
	return MariaDBStreamStore{db: db}
}

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
    OR (
      s.archive_reported_at IS NULL
      AND LOWER(TRIM(s.status)) IN ('stopping', 'completed', 'ready')
	  AND `+streamLogGuardPendingCondition+`
    )
  )`),
		archiveRetryAssignmentGuardLogMessage, archiveRetryAssignmentGuardClosedLogMessage,
		legacyArchiveAssignmentGuardLogMessage, legacyArchiveAssignmentGuardClosedLogMessage,
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
  COALESCE(ss.discord_config_id, ''), COALESCE(ss.discord_guild_id, ''), COALESCE(ss.discord_voice_channel_id, ''), COALESCE(ss.discord_text_channel_id, ''), COALESCE(ss.auto_start_trigger, ''),
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
	if err := scanner.Scan(&stream.ID, &stream.Name, &stream.Status, &stream.ArchiveRunID, &archiveStartedAt, &archiveReportedAt, &scheduledStart, &scheduledEnd, &stream.DiscordConfigID, &stream.DiscordGuildID, &stream.DiscordVoiceID, &stream.DiscordTextID, &stream.AutoStartTrigger, &stream.EncoderProfileID, &stream.CaptionProfileID, &stream.OverlayProfileID, &stream.EncoderAudioGainDB, &stream.ArchiveProfileID, &stream.YouTubeOutputID, &stream.ArchiveDriveDestinationID, &stream.ArchiveOAuthAccountID, &stream.ArchiveFolderIDConfigured, &stream.ArchiveMaskedFolderID, &stream.ArchiveSharedDrive, &stream.ArchiveSharedDriveID, &stream.ArchiveFileName, &stream.EncoderInputURL, &stream.CreatedAt, &stream.UpdatedAt, &deletedAt); err != nil {
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
	if archiveRunID != "" && !validArchiveRunID(archiveRunID) {
		return Stream{}, ErrInvalidStreamArtifact
	}
	if archiveRunID == "" {
		startedAt = time.Time{}
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
	var started any
	if !startedAt.IsZero() {
		started = startedAt.UTC()
	}
	now := time.Now().UTC()
	if _, err := tx.ExecContext(ctx, `UPDATE streams SET archive_run_id = ?, archive_started_at = ?, archive_reported_at = NULL, updated_at = ? WHERE id = ?`, archiveRunID, started, now, id); err != nil {
		return Stream{}, err
	}
	if err := closeMariaDBStreamLogGuard(ctx, tx, id, legacyArchiveAssignmentGuardLogMessage, legacyArchiveAssignmentGuardClosedLogMessage, now); err != nil {
		return Stream{}, err
	}
	if archiveRunID == "" && strings.TrimSpace(state.ArchiveProfileID) != "" {
		if err := insertMariaDBStreamLogGuard(ctx, tx, id, legacyArchiveAssignmentGuardLogMessage, now); err != nil {
			return Stream{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return Stream{}, err
	}
	stream := state.Stream
	stream.ArchiveRunID = archiveRunID
	stream.ArchiveStartedAt = nil
	if !startedAt.IsZero() {
		stream.ArchiveStartedAt = cloneTimePtr(&startedAt)
	}
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

func (s MariaDBStreamStore) DeleteStream(ctx context.Context, id string) (err error) {
	defer func() {
		if isMariaDBLockConflict(err) {
			err = mariaDBLockConflictAsAssignmentConflict(err)
		}
	}()
	id = strings.TrimSpace(id)
	if id == "" {
		return ErrNotFound
	}
	discoveredRows, err := discoverMariaDBAssignmentsForStream(ctx, s.db, id)
	if err != nil {
		return err
	}
	discoveredCurrentServiceIDs, err := discoverMariaDBCurrentStreamServiceIDs(ctx, s.db, id)
	if err != nil {
		return err
	}
	serviceIDs := append([]string(nil), discoveredCurrentServiceIDs...)
	for _, row := range discoveredRows {
		serviceIDs = append(serviceIDs, row.ServiceID)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	lockedStreams, err := lockMariaDBStreamsSorted(ctx, tx, []string{id})
	if err != nil {
		return err
	}
	if target, ok := lockedStreams[id]; !ok || target.DeletedAt != nil {
		return ErrNotFound
	}
	lockedServices, err := lockMariaDBServicesSorted(ctx, tx, serviceIDs)
	if err != nil {
		return ErrServiceAssignmentConflict
	}
	if err := lockMariaDBAssignmentRowsSorted(ctx, tx, discoveredRows); err != nil {
		return err
	}
	revalidatedRows, err := discoverMariaDBAssignmentsForStream(ctx, tx, id)
	if err != nil {
		return err
	}
	revalidatedCurrentServiceIDs, err := discoverMariaDBCurrentStreamServiceIDs(ctx, tx, id)
	if err != nil {
		return err
	}
	if !mariaDBAssignmentRowsEqual(discoveredRows, revalidatedRows) || !equalSortedStrings(discoveredCurrentServiceIDs, revalidatedCurrentServiceIDs) {
		return ErrServiceAssignmentConflict
	}
	assignmentServiceIDs := make([]string, 0, len(revalidatedRows))
	for _, row := range revalidatedRows {
		assignmentServiceIDs = append(assignmentServiceIDs, row.ServiceID)
	}
	if !equalSortedStrings(assignmentServiceIDs, revalidatedCurrentServiceIDs) {
		return ErrServiceAssignmentConflict
	}
	for _, row := range revalidatedRows {
		service, exists := lockedServices[row.ServiceID]
		if !exists || service.ServiceType != row.ServiceType {
			return ErrServiceAssignmentConflict
		}
		owner, role, consistencyErr := consistentMariaDBServiceAssignment(ctx, tx, service)
		if consistencyErr != nil || owner != id || role != normalizeAssignmentRole(row.AssignmentRole) {
			return ErrServiceAssignmentConflict
		}
	}
	state, err := mariaDBStreamAssignmentProtectionAfterLocks(ctx, tx, id)
	if err != nil {
		return err
	}
	if hasClaim, err := hasStreamYouTubeRelayBindingClaimForStreamTx(ctx, tx, id); err != nil {
		return err
	} else if hasClaim {
		return ErrYouTubeRelayBindingClaimActive
	}
	if state.protected() {
		return ErrServiceUnassignProtectedStream
	}
	var archiveEncoderID string
	encoderRows := append([]mariaDBAssignmentRow(nil), revalidatedRows...)
	sort.Slice(encoderRows, func(i, j int) bool {
		leftPrimary := normalizeAssignmentRole(encoderRows[i].AssignmentRole) == "primary"
		rightPrimary := normalizeAssignmentRole(encoderRows[j].AssignmentRole) == "primary"
		if leftPrimary != rightPrimary {
			return leftPrimary
		}
		return encoderRows[i].ServiceID < encoderRows[j].ServiceID
	})
	for _, row := range encoderRows {
		if row.ServiceType == "encoder_recorder" {
			archiveEncoderID = row.ServiceID
			break
		}
	}
	if archiveEncoderID != "" {
		if _, err := tx.ExecContext(ctx, `UPDATE stream_artifacts SET source_service_id = ? WHERE stream_id = ? AND COALESCE(TRIM(source_service_id), '') = ''`, archiveEncoderID, id); err != nil {
			return err
		}
	}
	now := time.Now().UTC()
	for _, serviceID := range sortedUniqueStrings(assignmentServiceIDs) {
		result, updateErr := tx.ExecContext(ctx, `UPDATE services SET current_stream_id = NULL, status = CASE WHEN status = 'assigned' THEN 'registered' ELSE status END, updated_at = ? WHERE service_id = ? AND current_stream_id = ?`, now, serviceID, id)
		if updateErr != nil {
			return updateErr
		}
		if affected, _ := result.RowsAffected(); affected != 1 {
			return ErrServiceAssignmentConflict
		}
	}
	for _, row := range revalidatedRows {
		result, deleteErr := tx.ExecContext(ctx, `DELETE FROM stream_service_assignments WHERE id = ?`, row.ID)
		if deleteErr != nil {
			return deleteErr
		}
		if affected, _ := result.RowsAffected(); affected != 1 {
			return ErrServiceAssignmentConflict
		}
	}
	for _, query := range []string{
		`DELETE FROM runtime_secret_leases WHERE stream_id = ?`,
		`DELETE FROM service_remediation_executions WHERE stream_id = ?`,
		`DELETE FROM service_stream_events WHERE stream_id = ?`,
		`DELETE FROM stream_youtube_runtimes WHERE stream_id = ?`,
	} {
		if _, err := tx.ExecContext(ctx, query, id); err != nil {
			return err
		}
	}
	result, err := tx.ExecContext(ctx, `UPDATE streams SET status = 'completed', deleted_at = COALESCE(deleted_at, ?), updated_at = ? WHERE id = ?`, now, now, id)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return ErrNotFound
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	return nil
}

func (s MariaDBStreamStore) GetStream(ctx context.Context, id string) (Stream, error) {
	stream, err := scanStreamRow(s.db.QueryRowContext(ctx, streamListQuery("s.id = ?"), id))
	if err == sql.ErrNoRows {
		return Stream{}, ErrNotFound
	}
	if err != nil {
		return Stream{}, err
	}
	return stream, nil
}

func (s MariaDBStreamStore) UpdateStreamSettings(ctx context.Context, id string, settings StreamSettings) (Stream, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return Stream{}, ErrNotFound
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Stream{}, err
	}
	defer tx.Rollback()
	var streamID string
	err = tx.QueryRowContext(ctx, `SELECT id FROM streams WHERE id = ? FOR UPDATE`, id).Scan(&streamID)
	if errors.Is(err, sql.ErrNoRows) {
		return Stream{}, ErrNotFound
	}
	if err != nil {
		return Stream{}, err
	}
	var claimOutputID string
	err = tx.QueryRowContext(ctx, `SELECT youtube_output_id FROM stream_youtube_relay_binding_claims WHERE stream_id = ? FOR UPDATE`, id).Scan(&claimOutputID)
	if err == nil && claimOutputID != strings.TrimSpace(settings.YouTubeOutputID) {
		return Stream{}, ErrYouTubeRelayBindingClaimActive
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return Stream{}, err
	}
	now := time.Now().UTC()
	if _, err := tx.ExecContext(ctx, `UPDATE streams SET name = COALESCE(NULLIF(?, ''), name), scheduled_start_at = ?, scheduled_end_at = ?, updated_at = ? WHERE id = ?`, strings.TrimSpace(settings.Name), nullableTime(settings.ScheduledStartAt), nullableTime(settings.ScheduledEndAt), now, id); err != nil {
		return Stream{}, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO stream_settings (stream_id, discord_config_id, discord_guild_id, discord_voice_channel_id, discord_text_channel_id, auto_start_trigger, encoder_profile_id, caption_profile_id, overlay_profile_id, encoder_audio_gain_db, archive_profile_id, archive_drive_destination_id, archive_oauth_account_id, archive_shared_drive, archive_shared_drive_id, archive_file_name, youtube_output_id, encoder_input_url, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON DUPLICATE KEY UPDATE discord_config_id = VALUES(discord_config_id), discord_guild_id = VALUES(discord_guild_id), discord_voice_channel_id = VALUES(discord_voice_channel_id), discord_text_channel_id = VALUES(discord_text_channel_id), auto_start_trigger = VALUES(auto_start_trigger), encoder_profile_id = VALUES(encoder_profile_id), caption_profile_id = VALUES(caption_profile_id), overlay_profile_id = VALUES(overlay_profile_id), encoder_audio_gain_db = VALUES(encoder_audio_gain_db), archive_profile_id = VALUES(archive_profile_id), archive_drive_destination_id = VALUES(archive_drive_destination_id), archive_oauth_account_id = VALUES(archive_oauth_account_id), archive_shared_drive = VALUES(archive_shared_drive), archive_shared_drive_id = VALUES(archive_shared_drive_id), archive_file_name = VALUES(archive_file_name), youtube_output_id = VALUES(youtube_output_id), encoder_input_url = VALUES(encoder_input_url), updated_at = VALUES(updated_at)`,
		id, nullEmpty(settings.DiscordConfigID), nullEmpty(settings.DiscordGuildID), nullEmpty(settings.DiscordVoiceID), nullEmpty(settings.DiscordTextID), strings.TrimSpace(settings.AutoStartTrigger), nullEmpty(settings.EncoderProfileID), nullEmpty(settings.CaptionProfileID), nullEmpty(settings.OverlayProfileID), settings.EncoderAudioGainDB, nullEmpty(settings.ArchiveProfileID), nullEmpty(settings.ArchiveDriveDestinationID), nullEmpty(settings.ArchiveOAuthAccountID), settings.ArchiveSharedDrive, nullEmpty(settings.ArchiveSharedDriveID), nullEmpty(settings.ArchiveFileName), nullEmpty(settings.YouTubeOutputID), nullEmpty(settings.EncoderInputURL), now)
	if err != nil {
		return Stream{}, err
	}
	if err := tx.Commit(); err != nil {
		return Stream{}, err
	}
	return s.GetStream(ctx, id)
}

func (s MariaDBStreamStore) UpdateStreamEncoderRuntimeSettings(ctx context.Context, id string, audioGainDB float64, overlayProfileID string) (Stream, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return Stream{}, ErrNotFound
	}
	now := time.Now().UTC()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Stream{}, err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `UPDATE streams SET updated_at = ? WHERE id = ?`, now, id)
	if err != nil {
		return Stream{}, err
	}
	if affected, err := result.RowsAffected(); err != nil || affected == 0 {
		if err != nil {
			return Stream{}, err
		}
		return Stream{}, ErrNotFound
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO stream_settings (stream_id, overlay_profile_id, encoder_audio_gain_db, updated_at)
VALUES (?, ?, ?, ?)
ON DUPLICATE KEY UPDATE overlay_profile_id = VALUES(overlay_profile_id), encoder_audio_gain_db = VALUES(encoder_audio_gain_db), updated_at = VALUES(updated_at)`, id, nullEmpty(overlayProfileID), audioGainDB, now)
	if err != nil {
		return Stream{}, err
	}
	if err := tx.Commit(); err != nil {
		return Stream{}, err
	}
	return s.GetStream(ctx, id)
}

func (s MariaDBStreamStore) UpdateStreamStatus(ctx context.Context, id, status string) (Stream, error) {
	now := time.Now().UTC()
	result, err := s.db.ExecContext(ctx, `UPDATE streams SET status = ?, updated_at = ? WHERE id = ?`, status, now, id)
	if err != nil {
		return Stream{}, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return Stream{}, err
	}
	if affected == 0 {
		return Stream{}, ErrNotFound
	}
	return s.GetStream(ctx, id)
}

func (s MariaDBStreamStore) TransitionStreamStatus(ctx context.Context, id, expectedStatus, status string) (Stream, bool, error) {
	now := time.Now().UTC()
	result, err := s.db.ExecContext(ctx, `UPDATE streams
SET status = ?, updated_at = ?
WHERE id = ? AND LOWER(TRIM(status)) = LOWER(TRIM(?))`, status, now, id, expectedStatus)
	if err != nil {
		return Stream{}, false, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return Stream{}, false, err
	}
	stream, err := s.GetStream(ctx, id)
	if err != nil {
		return Stream{}, false, err
	}
	return stream, affected > 0, nil
}

const (
	streamYouTubeRuntimeSaveAttempts = 2
	streamYouTubeRuntimeRetryDelay   = 100 * time.Millisecond
)

func (s MariaDBStreamStore) SaveStreamYouTubeRuntime(ctx context.Context, runtime StreamYouTubeRuntime) error {
	if strings.TrimSpace(runtime.Mode) == youtubeRelayBindingClaimStaticRuntimeMode {
		return ErrInvalidYouTubeRelayBindingClaim
	}
	if strings.TrimSpace(runtime.StreamID) == "" {
		return ErrNotFound
	}
	if runtime.CreatedAt.IsZero() {
		runtime.CreatedAt = time.Now().UTC()
	}
	var lastErr error
	for attempt := 0; attempt < streamYouTubeRuntimeSaveAttempts; attempt++ {
		runtime.UpdatedAt = time.Now().UTC()
		lastErr = s.saveStreamYouTubeRuntimeOnce(ctx, runtime)
		if lastErr == nil || attempt+1 == streamYouTubeRuntimeSaveAttempts || !isTransientDatabaseConnectionError(lastErr) {
			return lastErr
		}
		timer := time.NewTimer(streamYouTubeRuntimeRetryDelay)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return ctx.Err()
		case <-timer.C:
		}
	}
	return lastErr
}

func (s MariaDBStreamStore) saveStreamYouTubeRuntimeOnce(ctx context.Context, runtime StreamYouTubeRuntime) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var streamID string
	err = tx.QueryRowContext(ctx, `SELECT id FROM streams WHERE id = ? FOR UPDATE`, runtime.StreamID).Scan(&streamID)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if hasClaim, err := hasStreamYouTubeRelayBindingClaimForStreamTx(ctx, tx, runtime.StreamID); err != nil {
		return err
	} else if hasClaim {
		return ErrYouTubeRelayBindingClaimActive
	}
	if err := saveStreamYouTubeRuntimeTx(ctx, tx, runtime); err != nil {
		return err
	}
	return tx.Commit()
}

func streamYouTubeRuntimeCompleteLastError(value string) string {
	return truncateString(strings.TrimSpace(value), 255)
}

func (s MariaDBStreamStore) GetStreamYouTubeRuntime(ctx context.Context, streamID string) (StreamYouTubeRuntime, error) {
	var runtime StreamYouTubeRuntime
	err := scanStreamYouTubeRuntime(s.db.QueryRowContext(ctx, `SELECT stream_id, youtube_output, oauth_account_id, mode, broadcast_id, live_stream_id, rtmp_url, stream_key_secret_name, dry_run, complete_on_stop, complete_retry_count, complete_next_retry_at, complete_last_error, created_at, updated_at FROM stream_youtube_runtimes WHERE stream_id = ?`, streamID), &runtime)
	if err == sql.ErrNoRows {
		return StreamYouTubeRuntime{}, ErrNotFound
	}
	return runtime, err
}

func (s MariaDBStreamStore) ListStreamYouTubeRuntimes(ctx context.Context) ([]StreamYouTubeRuntime, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT stream_id, youtube_output, oauth_account_id, mode, broadcast_id, live_stream_id, rtmp_url, stream_key_secret_name, dry_run, complete_on_stop, complete_retry_count, complete_next_retry_at, complete_last_error, created_at, updated_at
FROM stream_youtube_runtimes
ORDER BY updated_at DESC, stream_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var runtimes []StreamYouTubeRuntime
	for rows.Next() {
		var runtime StreamYouTubeRuntime
		if err := scanStreamYouTubeRuntime(rows, &runtime); err != nil {
			return nil, err
		}
		runtimes = append(runtimes, runtime)
	}
	return runtimes, rows.Err()
}

func (s MariaDBStreamStore) ListDueStreamYouTubeRuntimes(ctx context.Context, now time.Time, limit int) ([]StreamYouTubeRuntime, error) {
	if limit <= 0 || limit > 100 {
		limit = 25
	}
	rows, err := s.db.QueryContext(ctx, `SELECT stream_id, youtube_output, oauth_account_id, mode, broadcast_id, live_stream_id, rtmp_url, stream_key_secret_name, dry_run, complete_on_stop, complete_retry_count, complete_next_retry_at, complete_last_error, created_at, updated_at
FROM stream_youtube_runtimes
WHERE mode IN ('live_api', 'live_api_relay_static') AND complete_next_retry_at IS NOT NULL AND complete_next_retry_at <= ?
ORDER BY complete_next_retry_at ASC LIMIT ?`, now.UTC(), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var runtimes []StreamYouTubeRuntime
	for rows.Next() {
		var runtime StreamYouTubeRuntime
		if err := scanStreamYouTubeRuntime(rows, &runtime); err != nil {
			return nil, err
		}
		runtimes = append(runtimes, runtime)
	}
	return runtimes, rows.Err()
}

func (s MariaDBStreamStore) RecordStreamYouTubeRuntimeCompleteFailure(ctx context.Context, streamID, lastError string, nextRetryAt time.Time) (StreamYouTubeRuntime, error) {
	_, err := s.db.ExecContext(ctx, `UPDATE stream_youtube_runtimes
SET complete_retry_count = complete_retry_count + 1, complete_next_retry_at = ?, complete_last_error = ?, updated_at = ?
WHERE stream_id = ?`, nextRetryAt.UTC(), truncateString(strings.TrimSpace(lastError), 255), time.Now().UTC(), streamID)
	if err != nil {
		return StreamYouTubeRuntime{}, err
	}
	return s.GetStreamYouTubeRuntime(ctx, streamID)
}

func (s MariaDBStreamStore) DeleteStreamYouTubeRuntime(ctx context.Context, streamID string) error {
	streamID = strings.TrimSpace(streamID)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var lockedStreamID string
	err = tx.QueryRowContext(ctx, `SELECT id FROM streams WHERE id = ? FOR UPDATE`, streamID).Scan(&lockedStreamID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if err == nil {
		if hasClaim, err := hasStreamYouTubeRelayBindingClaimForStreamTx(ctx, tx, streamID); err != nil {
			return err
		} else if hasClaim {
			return ErrYouTubeRelayBindingClaimActive
		}
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM stream_youtube_runtimes WHERE stream_id = ?`, streamID); err != nil {
		return err
	}
	return tx.Commit()
}

type streamYouTubeRuntimeScanner interface {
	Scan(dest ...any) error
}

func scanStreamYouTubeRuntime(scanner streamYouTubeRuntimeScanner, runtime *StreamYouTubeRuntime) error {
	var oauthAccountID sql.NullString
	var broadcastID sql.NullString
	var liveStreamID sql.NullString
	var rtmpURL sql.NullString
	var nextRetryAt sql.NullTime
	var lastError sql.NullString
	err := scanner.Scan(&runtime.StreamID, &runtime.YouTubeOutput, &oauthAccountID, &runtime.Mode, &broadcastID, &liveStreamID, &rtmpURL, &runtime.StreamKeySecretName, &runtime.DryRun, &runtime.CompleteOnStop, &runtime.CompleteRetryCount, &nextRetryAt, &lastError, &runtime.CreatedAt, &runtime.UpdatedAt)
	if oauthAccountID.Valid {
		runtime.OAuthAccountID = oauthAccountID.String
	}
	if broadcastID.Valid {
		runtime.BroadcastID = broadcastID.String
	}
	if liveStreamID.Valid {
		runtime.LiveStreamID = liveStreamID.String
	}
	if rtmpURL.Valid {
		runtime.RTMPURL = rtmpURL.String
	}
	if nextRetryAt.Valid {
		runtime.CompleteNextRetryAt = nextRetryAt.Time
	}
	if lastError.Valid {
		runtime.CompleteLastError = lastError.String
	}
	return err
}

func (s MariaDBStreamStore) RetryArchiveUpload(ctx context.Context, id, actorUserID string) (StreamLog, error) {
	return s.AppendStreamLog(ctx, StreamLog{
		ID: newUUID(), StreamID: id, Level: "info", Message: "archive upload retry requested",
		Fields: map[string]any{"actor_user_id": actorUserID}, CreatedAt: time.Now().UTC(),
	})
}

func (s MariaDBStreamStore) AppendStreamLog(ctx context.Context, log StreamLog) (StreamLog, error) {
	if _, err := s.GetStream(ctx, strings.TrimSpace(log.StreamID)); err != nil {
		return StreamLog{}, err
	}
	log = normalizeStreamLog(log)
	fields, err := json.Marshal(log.Fields)
	if err != nil {
		return StreamLog{}, err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO stream_logs (id, stream_id, level, message, fields, created_at) VALUES (?, ?, ?, ?, ?, ?)`, log.ID, log.StreamID, log.Level, log.Message, string(fields), log.CreatedAt)
	return log, err
}

func (s MariaDBStreamStore) ListStreamLogs(ctx context.Context, id string) ([]StreamLog, error) {
	if _, err := s.GetStream(ctx, id); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT l.id, l.stream_id, COALESCE(s.name, ''), s.deleted_at, l.level, l.message, l.fields, l.created_at
FROM stream_logs l
LEFT JOIN streams s ON s.id = l.stream_id
WHERE l.stream_id = ?
ORDER BY l.created_at DESC
LIMIT 500`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var logs []StreamLog
	for rows.Next() {
		var log StreamLog
		var fields string
		var deletedAt sql.NullTime
		if err := rows.Scan(&log.ID, &log.StreamID, &log.StreamName, &deletedAt, &log.Level, &log.Message, &fields, &log.CreatedAt); err != nil {
			return nil, err
		}
		if deletedAt.Valid {
			value := deletedAt.Time
			log.StreamDeletedAt = &value
		}
		_ = json.Unmarshal([]byte(fields), &log.Fields)
		if log.Fields == nil {
			log.Fields = map[string]any{}
		}
		logs = append(logs, log)
	}
	return logs, rows.Err()
}

func (s MariaDBStreamStore) ListStreamLogHistory(ctx context.Context, limit int, before time.Time, beforeID string) ([]StreamLog, error) {
	limit = boundedStreamLogLimit(limit)
	const columns = `SELECT l.id, l.stream_id, COALESCE(s.name, ''), s.deleted_at, l.level, l.message, l.fields, l.created_at
FROM stream_logs l
LEFT JOIN streams s ON s.id = l.stream_id`
	var rows *sql.Rows
	var err error
	if before.IsZero() {
		rows, err = s.db.QueryContext(ctx, columns+` ORDER BY l.created_at DESC, l.id DESC LIMIT ?`, limit)
	} else {
		rows, err = s.db.QueryContext(ctx, columns+` WHERE l.created_at < ? OR (l.created_at = ? AND l.id < ?) ORDER BY l.created_at DESC, l.id DESC LIMIT ?`, before.UTC(), before.UTC(), strings.TrimSpace(beforeID), limit)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	logs := make([]StreamLog, 0, limit)
	for rows.Next() {
		var log StreamLog
		var fields string
		var deletedAt sql.NullTime
		if err := rows.Scan(&log.ID, &log.StreamID, &log.StreamName, &deletedAt, &log.Level, &log.Message, &fields, &log.CreatedAt); err != nil {
			return nil, err
		}
		if deletedAt.Valid {
			value := deletedAt.Time
			log.StreamDeletedAt = &value
		}
		_ = json.Unmarshal([]byte(fields), &log.Fields)
		if log.Fields == nil {
			log.Fields = map[string]any{}
		}
		logs = append(logs, log)
	}
	return logs, rows.Err()
}

func normalizeStreamLog(log StreamLog) StreamLog {
	log.StreamID = strings.TrimSpace(log.StreamID)
	if strings.TrimSpace(log.ID) == "" {
		log.ID = newUUID()
	}
	log.Level = strings.ToLower(strings.TrimSpace(log.Level))
	switch log.Level {
	case "debug", "info", "warning", "error":
	default:
		log.Level = "info"
	}
	log.Message = strings.TrimSpace(log.Message)
	if log.Fields == nil {
		log.Fields = map[string]any{}
	}
	if log.CreatedAt.IsZero() {
		log.CreatedAt = time.Now().UTC()
	} else {
		log.CreatedAt = log.CreatedAt.UTC()
	}
	return log
}

func boundedStreamLogLimit(limit int) int {
	if limit <= 0 {
		return 500
	}
	if limit > 1000 {
		return 1000
	}
	return limit
}

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
	legacyAuthority := state.LegacyArchivePending || state.ArchiveRetryPending || (authority.ArchiveReportedAt != nil && legacyArchiveReportStatus(authority))
	if streamArtifactReportMatchesArchiveAuthority(authority, normalized, legacyAuthority) {
		if err := markStreamArchiveRunReported(ctx, tx, id, authority, normalized); err != nil {
			return err
		}
		closedAt := time.Now().UTC()
		if err := closeMariaDBStreamLogGuard(ctx, tx, id, archiveRetryAssignmentGuardLogMessage, archiveRetryAssignmentGuardClosedLogMessage, closedAt); err != nil {
			return err
		}
		if err := closeMariaDBStreamLogGuard(ctx, tx, id, legacyArchiveAssignmentGuardLogMessage, legacyArchiveAssignmentGuardClosedLogMessage, closedAt); err != nil {
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

func (s MariaDBStreamStore) WriteStreamArtifactReport(ctx context.Context, token ServiceToken, event ServiceStreamEvent, artifacts []StreamArtifact) error {
	if event.ServiceID == "" || event.StreamID == "" || event.EventType == "" {
		return errors.New("missing required stream event field")
	}
	if err := ValidateStreamArtifactReport(event.StreamID, artifacts); err != nil {
		return err
	}
	if event.Payload == nil {
		event.Payload = map[string]any{}
	}
	event.Payload = sanitizeServiceEventPayload(event.Payload)
	body, err := json.Marshal(event.Payload)
	if err != nil {
		return err
	}
	normalized := NormalizeStreamArtifacts(event.StreamID, artifacts)
	auth := MariaDBAuthStore{db: s.db}
	discoveredService, err := auth.getService(ctx, event.ServiceID)
	if err != nil {
		return err
	}
	discoveredRows, err := discoverMariaDBAssignmentsForService(ctx, s.db, event.ServiceID)
	if err != nil {
		return err
	}
	streamIDs := []string{event.StreamID, discoveredService.CurrentStreamID}
	for _, row := range discoveredRows {
		streamIDs = append(streamIDs, row.StreamID)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	lockedStreams, err := lockMariaDBStreamsSorted(ctx, tx, streamIDs)
	if err != nil {
		return ErrForbidden
	}
	if target, ok := lockedStreams[event.StreamID]; !ok || target.DeletedAt != nil {
		return ErrForbidden
	}
	lockedServices, err := lockMariaDBServicesSorted(ctx, tx, []string{event.ServiceID})
	if err != nil {
		return ErrForbidden
	}
	service, ok := lockedServices[event.ServiceID]
	if !ok || service.ServiceType != discoveredService.ServiceType || strings.TrimSpace(service.CurrentStreamID) != strings.TrimSpace(discoveredService.CurrentStreamID) || service.TokenID != token.ID {
		return ErrForbidden
	}
	if err := lockMariaDBAssignmentRowsSorted(ctx, tx, discoveredRows); err != nil {
		if errors.Is(err, ErrServiceAssignmentConflict) {
			return ErrForbidden
		}
		return err
	}
	revalidatedRows, err := discoverMariaDBAssignmentsForService(ctx, tx, event.ServiceID)
	if err != nil {
		return err
	}
	if !mariaDBAssignmentRowsEqual(discoveredRows, revalidatedRows) {
		return ErrForbidden
	}
	if !serviceStreamEventAllowed(service.ServiceType, event.EventType) {
		return ErrInvalidServiceStreamEvent
	}
	owner, _, err := consistentMariaDBServiceAssignment(ctx, tx, service)
	if err != nil || owner != event.StreamID {
		return ErrForbidden
	}
	state, err := mariaDBStreamAssignmentProtectionAfterLocks(ctx, tx, event.StreamID)
	if err != nil {
		return err
	}
	authority := state.Stream
	var activeTokenID string
	if err := tx.QueryRowContext(ctx, `SELECT id FROM service_tokens WHERE id = ? AND revoked_at IS NULL FOR UPDATE`, token.ID).Scan(&activeTokenID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrForbidden
		}
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO service_stream_events (id, service_id, stream_id, event_type, payload, created_at) VALUES (?, ?, ?, ?, ?, ?)`, newUUID(), event.ServiceID, event.StreamID, event.EventType, string(body), time.Now().UTC()); err != nil {
		return err
	}
	for _, artifact := range normalized {
		artifact.ID = newUUID()
		artifact.CreatedAt = time.Now().UTC()
		artifact.SourceServiceID = strings.TrimSpace(event.ServiceID)
		if _, err := tx.ExecContext(ctx, `INSERT INTO stream_artifacts (id, stream_id, archive_run_id, archive_started_at, kind, name, relative_path, size_bytes, created_at, source_service_id) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON DUPLICATE KEY UPDATE relative_path = VALUES(relative_path), size_bytes = VALUES(size_bytes), source_service_id = IF(VALUES(source_service_id) <> '', VALUES(source_service_id), source_service_id)`,
			artifact.ID, event.StreamID, artifact.ArchiveRunID, artifact.ArchiveStartedAt, artifact.Kind, artifact.Name, artifact.RelativePath, artifact.SizeBytes, artifact.CreatedAt, artifact.SourceServiceID); err != nil {
			return err
		}
	}
	legacyAuthority := state.LegacyArchivePending || state.ArchiveRetryPending || (authority.ArchiveReportedAt != nil && legacyArchiveReportStatus(authority))
	if streamArtifactReportMatchesArchiveAuthority(authority, normalized, legacyAuthority) {
		if err := markStreamArchiveRunReported(ctx, tx, event.StreamID, authority, normalized); err != nil {
			return err
		}
		closedAt := time.Now().UTC()
		if err := closeMariaDBStreamLogGuard(ctx, tx, event.StreamID, archiveRetryAssignmentGuardLogMessage, archiveRetryAssignmentGuardClosedLogMessage, closedAt); err != nil {
			return err
		}
		if err := closeMariaDBStreamLogGuard(ctx, tx, event.StreamID, legacyArchiveAssignmentGuardLogMessage, legacyArchiveAssignmentGuardClosedLogMessage, closedAt); err != nil {
			return err
		}
	}
	return tx.Commit()
}

type streamArtifactScanner interface {
	Scan(dest ...any) error
}

func scanStreamArtifact(scanner streamArtifactScanner) (StreamArtifact, error) {
	var artifact StreamArtifact
	var archiveStartedAt sql.NullTime
	if err := scanner.Scan(&artifact.ID, &artifact.StreamID, &artifact.ArchiveRunID, &archiveStartedAt, &artifact.Kind, &artifact.Name, &artifact.RelativePath, &artifact.SizeBytes, &artifact.CreatedAt, &artifact.SourceServiceID); err != nil {
		return StreamArtifact{}, err
	}
	artifact.ArchiveStartedAt = nullTimePtr(archiveStartedAt)
	return artifact, nil
}

type streamArchiveRunReporter interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

func markStreamArchiveRunReported(ctx context.Context, reporter streamArchiveRunReporter, streamID string, authority Stream, artifacts []StreamArtifact) error {
	if len(artifacts) == 0 {
		return nil
	}
	now := time.Now().UTC()
	var err error
	if strings.TrimSpace(authority.ArchiveRunID) == "" {
		_, err = reporter.ExecContext(ctx, `UPDATE streams
SET updated_at = CASE WHEN archive_reported_at IS NULL THEN ? ELSE updated_at END,
    archive_reported_at = COALESCE(archive_reported_at, ?)
WHERE id = ? AND COALESCE(archive_run_id, '') = ''`, now, now, streamID)
	} else {
		_, err = reporter.ExecContext(ctx, `UPDATE streams
SET updated_at = CASE WHEN archive_reported_at IS NULL THEN ? ELSE updated_at END,
    archive_reported_at = COALESCE(archive_reported_at, ?)
WHERE id = ? AND archive_started_at = ? AND archive_run_id = ?`, now, now, streamID, artifacts[0].ArchiveStartedAt, artifacts[0].ArchiveRunID)
	}
	return err
}

func streamArtifactReportMatchesArchiveAuthority(stream Stream, artifacts []StreamArtifact, legacyAuthority bool) bool {
	if len(artifacts) == 0 {
		return false
	}
	report := artifacts[0]
	currentRunID := strings.TrimSpace(stream.ArchiveRunID)
	reportRunID := strings.TrimSpace(report.ArchiveRunID)
	if currentRunID == "" {
		if reportRunID != "" || report.ArchiveStartedAt != nil {
			return false
		}
		// New legacy starts carry a durable auxiliary pending marker instead of
		// fabricating a run timestamp. The second branch closes pre-FIX-003
		// in-flight legacy starts without weakening non-empty modern run matching.
		return legacyAuthority || (stream.ArchiveStartedAt != nil && legacyArchiveReportStatus(stream))
	}
	return stream.ArchiveStartedAt != nil &&
		reportRunID == currentRunID &&
		report.ArchiveStartedAt != nil &&
		report.ArchiveStartedAt.UTC().Equal(stream.ArchiveStartedAt.UTC())
}

func legacyArchiveReportStatus(stream Stream) bool {
	if strings.TrimSpace(stream.ArchiveProfileID) == "" {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(stream.Status)) {
	case "starting", "live", "stopping", "completed", "ready", "failed":
		return true
	default:
		return false
	}
}

func ValidateStreamArtifactReport(streamID string, artifacts []StreamArtifact) error {
	if strings.TrimSpace(streamID) == "" || len(artifacts) == 0 || len(artifacts) > 20 {
		return errors.New("invalid artifact report")
	}
	allowedKinds := map[string]bool{
		"archive": true, "caption": true, "transcript": true, "metadata": true, "logs": true,
	}
	allowedNames := map[string]string{
		"archive":    "final.mp4",
		"caption":    "captions.vtt",
		"transcript": "transcript.json",
		"metadata":   "metadata.json",
		"logs":       "logs.jsonl",
	}
	seen := map[string]bool{}
	reportRunID := ""
	var reportStartedAt *time.Time
	reportRunSet := false
	for _, artifact := range NormalizeStreamArtifacts(streamID, artifacts) {
		kind := artifact.Kind
		name := artifact.Name
		if !allowedKinds[kind] || name == "" || len(name) > 255 || strings.ContainsAny(name, `/\`) {
			return errors.New("invalid artifact metadata")
		}
		if allowedNames[kind] != name {
			return errors.New("unsupported artifact name")
		}
		if artifact.SizeBytes < 0 || len(artifact.RelativePath) > 1024 || !isSafeRelativePath(artifact.RelativePath) {
			return errors.New("unsafe artifact path")
		}
		if artifact.ArchiveRunID == "" {
			if artifact.ArchiveStartedAt != nil {
				return errors.New("archive start time requires run id")
			}
		} else if !validArchiveRunID(artifact.ArchiveRunID) || artifact.ArchiveStartedAt == nil || artifact.ArchiveStartedAt.IsZero() {
			return errors.New("invalid archive run metadata")
		}
		if artifact.RelativePath != streamArtifactRelativePath(streamID, artifact.ArchiveRunID, name) {
			return errors.New("artifact path does not match stream and name")
		}
		if !reportRunSet {
			reportRunID = artifact.ArchiveRunID
			reportStartedAt = artifact.ArchiveStartedAt
			reportRunSet = true
		} else if reportRunID != artifact.ArchiveRunID || !sameOptionalTime(reportStartedAt, artifact.ArchiveStartedAt) {
			return errors.New("mixed archive runs in one report")
		}
		key := kind + "\x00" + name
		if seen[key] {
			return errors.New("duplicate artifact")
		}
		seen[key] = true
	}
	return nil
}

var ErrInvalidStreamArtifact = errors.New("invalid stream artifact")

func isSafeArtifactFileName(name string) bool {
	return ValidStreamArtifactFileName(name)
}

func ValidStreamArtifactFileName(name string) bool {
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 255 || strings.Contains(name, "..") || strings.ContainsAny(name, `/\`) {
		return false
	}
	allowedExt := map[string]bool{
		".mp4": true, ".mkv": true, ".json": true, ".jsonl": true, ".vtt": true,
	}
	if !allowedExt[strings.ToLower(path.Ext(name))] {
		return false
	}
	for _, r := range name {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' || r == '.' {
			continue
		}
		return false
	}
	return true
}

func validArchiveRunID(id string) bool {
	id = strings.TrimSpace(id)
	if id == "" || len(id) > 128 || strings.Contains(id, "..") || strings.ContainsAny(id, `/\`) || !isASCIIAlphaNumeric(id[0]) {
		return false
	}
	for _, r := range id {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' || r == '.' {
			continue
		}
		return false
	}
	return true
}

func isASCIIAlphaNumeric(value byte) bool {
	return value >= 'a' && value <= 'z' || value >= 'A' && value <= 'Z' || value >= '0' && value <= '9'
}

func streamArtifactRelativePath(streamID, archiveRunID, name string) string {
	if archiveRunID == "" {
		return path.Join("final", streamID, name)
	}
	return path.Join("final", streamID, archiveRunID, name)
}

func sameOptionalTime(left, right *time.Time) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return left.Equal(*right)
}

func isArchiveRecordingArtifact(artifact StreamArtifact) bool {
	if !strings.EqualFold(strings.TrimSpace(artifact.Kind), "archive") {
		return false
	}
	switch strings.ToLower(path.Ext(strings.TrimSpace(artifact.Name))) {
	case ".mp4", ".webm", ".m4v", ".mov", ".mkv":
		return true
	default:
		return false
	}
}

func NormalizeStreamArtifacts(streamID string, artifacts []StreamArtifact) []StreamArtifact {
	normalized := make([]StreamArtifact, 0, len(artifacts))
	for _, artifact := range artifacts {
		artifact.ID = ""
		artifact.StreamID = streamID
		artifact.Kind = strings.TrimSpace(artifact.Kind)
		artifact.Name = strings.TrimSpace(artifact.Name)
		artifact.RelativePath = strings.TrimSpace(artifact.RelativePath)
		artifact.ArchiveRunID = strings.TrimSpace(artifact.ArchiveRunID)
		if artifact.ArchiveStartedAt != nil {
			startedAt := artifact.ArchiveStartedAt.UTC()
			artifact.ArchiveStartedAt = &startedAt
		}
		artifact.CreatedAt = time.Time{}
		normalized = append(normalized, artifact)
	}
	return normalized
}

func newUUID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(fmt.Errorf("generate uuid: %w", err))
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	encoded := hex.EncodeToString(b[:])
	return encoded[0:8] + "-" + encoded[8:12] + "-" + encoded[12:16] + "-" + encoded[16:20] + "-" + encoded[20:32]
}

func nullEmpty(value string) any {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	return value
}

func nullTime(value time.Time) any {
	if value.IsZero() {
		return nil
	}
	return value.UTC()
}

func nullableTime(value *time.Time) any {
	if value == nil || value.IsZero() {
		return nil
	}
	return value.UTC()
}

func nullTimePtr(value sql.NullTime) *time.Time {
	if !value.Valid {
		return nil
	}
	utc := value.Time.UTC()
	return &utc
}

func truncateString(value string, maxLen int) string {
	if maxLen <= 0 || len(value) <= maxLen {
		return value
	}
	return value[:maxLen]
}

func isSafeRelativePath(path string) bool {
	if path == "" || filepath.IsAbs(path) || strings.HasPrefix(path, "/") || strings.ContainsAny(path, `\:`) {
		return false
	}
	clean := filepath.Clean(path)
	slashClean := strings.ReplaceAll(clean, `\`, "/")
	if slashClean == "." || slashClean != path || strings.HasPrefix(slashClean, "../") || strings.Contains(slashClean, "/../") {
		return false
	}
	return true
}
