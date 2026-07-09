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
	"strings"
	"time"
)

type Stream struct {
	ID                        string     `json:"id"`
	Name                      string     `json:"name"`
	Status                    string     `json:"status"`
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
	CreatedAt                 time.Time  `json:"created_at"`
	UpdatedAt                 time.Time  `json:"updated_at"`
}

type StreamSettings struct {
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
	ID        string         `json:"id"`
	StreamID  string         `json:"stream_id"`
	Level     string         `json:"level"`
	Message   string         `json:"message"`
	Fields    map[string]any `json:"fields"`
	CreatedAt time.Time      `json:"created_at"`
}

type StreamArtifact struct {
	ID           string    `json:"id"`
	StreamID     string    `json:"stream_id"`
	Kind         string    `json:"kind"`
	Name         string    `json:"name"`
	RelativePath string    `json:"relative_path"`
	SizeBytes    int64     `json:"size_bytes"`
	CreatedAt    time.Time `json:"created_at"`
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
	UpdateStreamSettings(ctx context.Context, id string, settings StreamSettings) (Stream, error)
	UpdateStreamStatus(ctx context.Context, id, status string) (Stream, error)
	RetryArchiveUpload(ctx context.Context, id, actorUserID string) (StreamLog, error)
	ListStreamLogs(ctx context.Context, id string) ([]StreamLog, error)
	ListStreamArtifacts(ctx context.Context, id string) ([]StreamArtifact, error)
	UpsertStreamArtifacts(ctx context.Context, id string, artifacts []StreamArtifact) error
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

type StreamYouTubeRuntimeStore interface {
	SaveStreamYouTubeRuntime(ctx context.Context, runtime StreamYouTubeRuntime) error
	GetStreamYouTubeRuntime(ctx context.Context, streamID string) (StreamYouTubeRuntime, error)
	ListStreamYouTubeRuntimes(ctx context.Context) ([]StreamYouTubeRuntime, error)
	ListDueStreamYouTubeRuntimes(ctx context.Context, now time.Time, limit int) ([]StreamYouTubeRuntime, error)
	RecordStreamYouTubeRuntimeCompleteFailure(ctx context.Context, streamID, lastError string, nextRetryAt time.Time) (StreamYouTubeRuntime, error)
	DeleteStreamYouTubeRuntime(ctx context.Context, streamID string) error
}

var ErrNotFound = errors.New("not found")

type MariaDBStreamStore struct {
	db *sql.DB
}

func NewMariaDBStreamStore(db *sql.DB) MariaDBStreamStore {
	return MariaDBStreamStore{db: db}
}

func (s MariaDBStreamStore) ListStreams(ctx context.Context) ([]Stream, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT s.id, s.name, s.status, s.scheduled_start_at, s.scheduled_end_at,
  COALESCE(ss.discord_config_id, ''), COALESCE(ss.discord_guild_id, ''), COALESCE(ss.discord_voice_channel_id, ''), COALESCE(ss.discord_text_channel_id, ''), COALESCE(ss.auto_start_trigger, ''),
  COALESCE(ss.encoder_profile_id, ''), COALESCE(ss.caption_profile_id, ''),
  COALESCE(ss.overlay_profile_id, ''), COALESCE(ss.archive_profile_id, ''), COALESCE(ss.youtube_output_id, ''),
  COALESCE(ss.archive_drive_destination_id, ''), COALESCE(ss.archive_oauth_account_id, ''),
  CASE WHEN dd.folder_id_fingerprint IS NULL OR dd.folder_id_fingerprint = '' THEN 0 ELSE 1 END,
  COALESCE(dd.masked_folder_id, ''), COALESCE(ss.archive_shared_drive, 0), COALESCE(ss.archive_shared_drive_id, ''),
  COALESCE(ss.archive_file_name, ''), COALESCE(ss.encoder_input_url, ''), s.created_at, s.updated_at
FROM streams s
LEFT JOIN stream_settings ss ON ss.stream_id = s.id
LEFT JOIN drive_destinations dd ON dd.id = ss.archive_drive_destination_id
ORDER BY s.created_at DESC LIMIT 100`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var streams []Stream
	for rows.Next() {
		var stream Stream
		var scheduledStart, scheduledEnd sql.NullTime
		if err := rows.Scan(&stream.ID, &stream.Name, &stream.Status, &scheduledStart, &scheduledEnd, &stream.DiscordConfigID, &stream.DiscordGuildID, &stream.DiscordVoiceID, &stream.DiscordTextID, &stream.AutoStartTrigger, &stream.EncoderProfileID, &stream.CaptionProfileID, &stream.OverlayProfileID, &stream.ArchiveProfileID, &stream.YouTubeOutputID, &stream.ArchiveDriveDestinationID, &stream.ArchiveOAuthAccountID, &stream.ArchiveFolderIDConfigured, &stream.ArchiveMaskedFolderID, &stream.ArchiveSharedDrive, &stream.ArchiveSharedDriveID, &stream.ArchiveFileName, &stream.EncoderInputURL, &stream.CreatedAt, &stream.UpdatedAt); err != nil {
			return nil, err
		}
		stream.ScheduledStartAt = nullTimePtr(scheduledStart)
		stream.ScheduledEndAt = nullTimePtr(scheduledEnd)
		streams = append(streams, stream)
	}
	return streams, rows.Err()
}

func (s MariaDBStreamStore) CreateStream(ctx context.Context, name string) (Stream, error) {
	now := time.Now().UTC()
	stream := Stream{ID: newUUID(), Name: name, Status: "created", CreatedAt: now, UpdatedAt: now}
	_, err := s.db.ExecContext(ctx, `INSERT INTO streams (id, name, status, created_at, updated_at) VALUES (?, ?, ?, ?, ?)`, stream.ID, stream.Name, stream.Status, stream.CreatedAt, stream.UpdatedAt)
	return stream, err
}

func (s MariaDBStreamStore) GetStream(ctx context.Context, id string) (Stream, error) {
	var stream Stream
	var scheduledStart, scheduledEnd sql.NullTime
	err := s.db.QueryRowContext(ctx, `SELECT s.id, s.name, s.status, s.scheduled_start_at, s.scheduled_end_at,
  COALESCE(ss.discord_config_id, ''), COALESCE(ss.discord_guild_id, ''), COALESCE(ss.discord_voice_channel_id, ''), COALESCE(ss.discord_text_channel_id, ''), COALESCE(ss.auto_start_trigger, ''),
  COALESCE(ss.encoder_profile_id, ''), COALESCE(ss.caption_profile_id, ''),
  COALESCE(ss.overlay_profile_id, ''), COALESCE(ss.archive_profile_id, ''), COALESCE(ss.youtube_output_id, ''),
  COALESCE(ss.archive_drive_destination_id, ''), COALESCE(ss.archive_oauth_account_id, ''),
  CASE WHEN dd.folder_id_fingerprint IS NULL OR dd.folder_id_fingerprint = '' THEN 0 ELSE 1 END,
  COALESCE(dd.masked_folder_id, ''), COALESCE(ss.archive_shared_drive, 0), COALESCE(ss.archive_shared_drive_id, ''),
  COALESCE(ss.archive_file_name, ''), COALESCE(ss.encoder_input_url, ''), s.created_at, s.updated_at
FROM streams s
LEFT JOIN stream_settings ss ON ss.stream_id = s.id
LEFT JOIN drive_destinations dd ON dd.id = ss.archive_drive_destination_id
WHERE s.id = ?`, id).Scan(&stream.ID, &stream.Name, &stream.Status, &scheduledStart, &scheduledEnd, &stream.DiscordConfigID, &stream.DiscordGuildID, &stream.DiscordVoiceID, &stream.DiscordTextID, &stream.AutoStartTrigger, &stream.EncoderProfileID, &stream.CaptionProfileID, &stream.OverlayProfileID, &stream.ArchiveProfileID, &stream.YouTubeOutputID, &stream.ArchiveDriveDestinationID, &stream.ArchiveOAuthAccountID, &stream.ArchiveFolderIDConfigured, &stream.ArchiveMaskedFolderID, &stream.ArchiveSharedDrive, &stream.ArchiveSharedDriveID, &stream.ArchiveFileName, &stream.EncoderInputURL, &stream.CreatedAt, &stream.UpdatedAt)
	if err == sql.ErrNoRows {
		return Stream{}, ErrNotFound
	}
	if err != nil {
		return Stream{}, err
	}
	stream.ScheduledStartAt = nullTimePtr(scheduledStart)
	stream.ScheduledEndAt = nullTimePtr(scheduledEnd)
	return stream, nil
}

func (s MariaDBStreamStore) UpdateStreamSettings(ctx context.Context, id string, settings StreamSettings) (Stream, error) {
	if _, err := s.GetStream(ctx, id); err != nil {
		return Stream{}, err
	}
	now := time.Now().UTC()
	if _, err := s.db.ExecContext(ctx, `UPDATE streams SET scheduled_start_at = ?, scheduled_end_at = ?, updated_at = ? WHERE id = ?`, nullableTime(settings.ScheduledStartAt), nullableTime(settings.ScheduledEndAt), now, id); err != nil {
		return Stream{}, err
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO stream_settings (stream_id, discord_config_id, discord_guild_id, discord_voice_channel_id, discord_text_channel_id, auto_start_trigger, encoder_profile_id, caption_profile_id, overlay_profile_id, archive_profile_id, archive_drive_destination_id, archive_oauth_account_id, archive_shared_drive, archive_shared_drive_id, archive_file_name, youtube_output_id, encoder_input_url, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON DUPLICATE KEY UPDATE discord_config_id = VALUES(discord_config_id), discord_guild_id = VALUES(discord_guild_id), discord_voice_channel_id = VALUES(discord_voice_channel_id), discord_text_channel_id = VALUES(discord_text_channel_id), auto_start_trigger = VALUES(auto_start_trigger), encoder_profile_id = VALUES(encoder_profile_id), caption_profile_id = VALUES(caption_profile_id), overlay_profile_id = VALUES(overlay_profile_id), archive_profile_id = VALUES(archive_profile_id), archive_drive_destination_id = VALUES(archive_drive_destination_id), archive_oauth_account_id = VALUES(archive_oauth_account_id), archive_shared_drive = VALUES(archive_shared_drive), archive_shared_drive_id = VALUES(archive_shared_drive_id), archive_file_name = VALUES(archive_file_name), youtube_output_id = VALUES(youtube_output_id), encoder_input_url = VALUES(encoder_input_url), updated_at = VALUES(updated_at)`,
		id, nullEmpty(settings.DiscordConfigID), nullEmpty(settings.DiscordGuildID), nullEmpty(settings.DiscordVoiceID), nullEmpty(settings.DiscordTextID), nullEmpty(settings.AutoStartTrigger), nullEmpty(settings.EncoderProfileID), nullEmpty(settings.CaptionProfileID), nullEmpty(settings.OverlayProfileID), nullEmpty(settings.ArchiveProfileID), nullEmpty(settings.ArchiveDriveDestinationID), nullEmpty(settings.ArchiveOAuthAccountID), settings.ArchiveSharedDrive, nullEmpty(settings.ArchiveSharedDriveID), nullEmpty(settings.ArchiveFileName), nullEmpty(settings.YouTubeOutputID), nullEmpty(settings.EncoderInputURL), now)
	if err != nil {
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

func (s MariaDBStreamStore) SaveStreamYouTubeRuntime(ctx context.Context, runtime StreamYouTubeRuntime) error {
	if strings.TrimSpace(runtime.StreamID) == "" {
		return ErrNotFound
	}
	if _, err := s.GetStream(ctx, runtime.StreamID); err != nil {
		return err
	}
	now := time.Now().UTC()
	if runtime.CreatedAt.IsZero() {
		runtime.CreatedAt = now
	}
	runtime.UpdatedAt = now
	_, err := s.db.ExecContext(ctx, `INSERT INTO stream_youtube_runtimes (stream_id, youtube_output, oauth_account_id, mode, broadcast_id, live_stream_id, rtmp_url, stream_key_secret_name, dry_run, complete_on_stop, complete_retry_count, complete_next_retry_at, complete_last_error, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON DUPLICATE KEY UPDATE youtube_output = VALUES(youtube_output), oauth_account_id = VALUES(oauth_account_id), mode = VALUES(mode), broadcast_id = VALUES(broadcast_id), live_stream_id = VALUES(live_stream_id), rtmp_url = VALUES(rtmp_url), stream_key_secret_name = VALUES(stream_key_secret_name), dry_run = VALUES(dry_run), complete_on_stop = VALUES(complete_on_stop), complete_retry_count = VALUES(complete_retry_count), complete_next_retry_at = VALUES(complete_next_retry_at), complete_last_error = VALUES(complete_last_error), updated_at = VALUES(updated_at)`,
		runtime.StreamID, runtime.YouTubeOutput, runtime.OAuthAccountID, runtime.Mode, runtime.BroadcastID, runtime.LiveStreamID, runtime.RTMPURL, runtime.StreamKeySecretName, runtime.DryRun, runtime.CompleteOnStop, runtime.CompleteRetryCount, nullTime(runtime.CompleteNextRetryAt), nullEmpty(runtime.CompleteLastError), runtime.CreatedAt, runtime.UpdatedAt)
	return err
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
WHERE mode = 'live_api' AND complete_next_retry_at IS NOT NULL AND complete_next_retry_at <= ?
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
	_, err := s.db.ExecContext(ctx, `DELETE FROM stream_youtube_runtimes WHERE stream_id = ?`, streamID)
	return err
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
	if _, err := s.GetStream(ctx, id); err != nil {
		return StreamLog{}, err
	}
	log := StreamLog{
		ID: newUUID(), StreamID: id, Level: "info", Message: "archive upload retry requested",
		Fields: map[string]any{"actor_user_id": actorUserID}, CreatedAt: time.Now().UTC(),
	}
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
	rows, err := s.db.QueryContext(ctx, `SELECT id, stream_id, level, message, fields, created_at FROM stream_logs WHERE stream_id = ? ORDER BY created_at DESC LIMIT 500`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var logs []StreamLog
	for rows.Next() {
		var log StreamLog
		var fields string
		if err := rows.Scan(&log.ID, &log.StreamID, &log.Level, &log.Message, &fields, &log.CreatedAt); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(fields), &log.Fields)
		if log.Fields == nil {
			log.Fields = map[string]any{}
		}
		logs = append(logs, log)
	}
	return logs, rows.Err()
}

func (s MariaDBStreamStore) ListStreamArtifacts(ctx context.Context, id string) ([]StreamArtifact, error) {
	if _, err := s.GetStream(ctx, id); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id, stream_id, kind, name, relative_path, size_bytes, created_at FROM stream_artifacts WHERE stream_id = ? ORDER BY created_at DESC`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var artifacts []StreamArtifact
	for rows.Next() {
		var artifact StreamArtifact
		if err := rows.Scan(&artifact.ID, &artifact.StreamID, &artifact.Kind, &artifact.Name, &artifact.RelativePath, &artifact.SizeBytes, &artifact.CreatedAt); err != nil {
			return nil, err
		}
		if isSafeRelativePath(artifact.RelativePath) {
			artifacts = append(artifacts, artifact)
		}
	}
	return artifacts, rows.Err()
}

func (s MariaDBStreamStore) UpsertStreamArtifacts(ctx context.Context, id string, artifacts []StreamArtifact) error {
	if _, err := s.GetStream(ctx, id); err != nil {
		return err
	}
	if err := ValidateStreamArtifactReport(id, artifacts); err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, artifact := range NormalizeStreamArtifacts(id, artifacts) {
		artifact.ID = newUUID()
		artifact.CreatedAt = time.Now().UTC()
		if _, err := tx.ExecContext(ctx, `DELETE FROM stream_artifacts WHERE stream_id = ? AND kind = ? AND name = ?`, id, artifact.Kind, artifact.Name); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO stream_artifacts (id, stream_id, kind, name, relative_path, size_bytes, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
			artifact.ID, id, artifact.Kind, artifact.Name, artifact.RelativePath, artifact.SizeBytes, artifact.CreatedAt); err != nil {
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
	if err := s.db.QueryRowContext(ctx, `SELECT id FROM stream_artifacts WHERE stream_id = ? AND kind = ? AND name = ? LIMIT 1`, streamID, artifact.Kind, name).Scan(&conflict); err == nil {
		return StreamArtifact{}, ErrAlreadyExists
	} else if !errors.Is(err, sql.ErrNoRows) {
		return StreamArtifact{}, err
	}
	artifact.Name = name
	artifact.RelativePath = path.Join("final", streamID, name)
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
	var artifact StreamArtifact
	err := s.db.QueryRowContext(ctx, `SELECT id, stream_id, kind, name, relative_path, size_bytes, created_at FROM stream_artifacts WHERE stream_id = ? AND id = ?`, streamID, artifactID).Scan(&artifact.ID, &artifact.StreamID, &artifact.Kind, &artifact.Name, &artifact.RelativePath, &artifact.SizeBytes, &artifact.CreatedAt)
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
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var streamID string
	if err := tx.QueryRowContext(ctx, `SELECT id FROM streams WHERE id = ? FOR UPDATE`, event.StreamID).Scan(&streamID); errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	} else if err != nil {
		return err
	}
	var serviceType, tokenID string
	if err := tx.QueryRowContext(ctx, `SELECT service_type, token_id FROM services WHERE service_id = ? FOR UPDATE`, event.ServiceID).Scan(&serviceType, &tokenID); errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	} else if err != nil {
		return err
	}
	if tokenID != token.ID {
		return ErrForbidden
	}
	if !serviceStreamEventAllowed(serviceType, event.EventType) {
		return ErrInvalidServiceStreamEvent
	}
	var assignedServiceID string
	if err := tx.QueryRowContext(ctx, `SELECT service_id FROM stream_service_assignments WHERE service_id = ? AND stream_id = ? FOR UPDATE`, event.ServiceID, event.StreamID).Scan(&assignedServiceID); errors.Is(err, sql.ErrNoRows) {
		return ErrForbidden
	} else if err != nil {
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
	if _, err := tx.ExecContext(ctx, `INSERT INTO service_stream_events (id, service_id, stream_id, event_type, payload, created_at) VALUES (?, ?, ?, ?, ?, ?)`, newUUID(), event.ServiceID, event.StreamID, event.EventType, string(body), time.Now().UTC()); err != nil {
		return err
	}
	for _, artifact := range NormalizeStreamArtifacts(event.StreamID, artifacts) {
		artifact.ID = newUUID()
		artifact.CreatedAt = time.Now().UTC()
		if _, err := tx.ExecContext(ctx, `INSERT INTO stream_artifacts (id, stream_id, kind, name, relative_path, size_bytes, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)
ON DUPLICATE KEY UPDATE id = VALUES(id), relative_path = VALUES(relative_path), size_bytes = VALUES(size_bytes), created_at = VALUES(created_at)`,
			artifact.ID, event.StreamID, artifact.Kind, artifact.Name, artifact.RelativePath, artifact.SizeBytes, artifact.CreatedAt); err != nil {
			return err
		}
	}
	return tx.Commit()
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
		if artifact.RelativePath != path.Join("final", streamID, name) {
			return errors.New("artifact path does not match stream and name")
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

func NormalizeStreamArtifacts(streamID string, artifacts []StreamArtifact) []StreamArtifact {
	normalized := make([]StreamArtifact, 0, len(artifacts))
	for _, artifact := range artifacts {
		artifact.ID = ""
		artifact.StreamID = streamID
		artifact.Kind = strings.TrimSpace(artifact.Kind)
		artifact.Name = strings.TrimSpace(artifact.Name)
		artifact.RelativePath = strings.TrimSpace(artifact.RelativePath)
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
