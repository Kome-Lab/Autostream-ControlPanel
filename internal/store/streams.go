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
	ID               string    `json:"id"`
	Name             string    `json:"name"`
	Status           string    `json:"status"`
	DiscordConfigID  string    `json:"discord_config_id,omitempty"`
	DiscordGuildID   string    `json:"discord_guild_id,omitempty"`
	DiscordVoiceID   string    `json:"discord_voice_channel_id,omitempty"`
	DiscordTextID    string    `json:"discord_text_channel_id,omitempty"`
	EncoderProfileID string    `json:"encoder_profile_id,omitempty"`
	CaptionProfileID string    `json:"caption_profile_id,omitempty"`
	OverlayProfileID string    `json:"overlay_profile_id,omitempty"`
	ArchiveProfileID string    `json:"archive_profile_id,omitempty"`
	YouTubeOutputID  string    `json:"youtube_output_id,omitempty"`
	EncoderInputURL  string    `json:"encoder_input_url,omitempty"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}

type StreamSettings struct {
	DiscordConfigID  string `json:"discord_config_id,omitempty"`
	DiscordGuildID   string `json:"discord_guild_id,omitempty"`
	DiscordVoiceID   string `json:"discord_voice_channel_id,omitempty"`
	DiscordTextID    string `json:"discord_text_channel_id,omitempty"`
	EncoderProfileID string `json:"encoder_profile_id,omitempty"`
	CaptionProfileID string `json:"caption_profile_id,omitempty"`
	OverlayProfileID string `json:"overlay_profile_id,omitempty"`
	ArchiveProfileID string `json:"archive_profile_id,omitempty"`
	YouTubeOutputID  string `json:"youtube_output_id,omitempty"`
	EncoderInputURL  string `json:"encoder_input_url,omitempty"`
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
	rows, err := s.db.QueryContext(ctx, `SELECT s.id, s.name, s.status,
  COALESCE(ss.discord_config_id, ''), COALESCE(ss.discord_guild_id, ''), COALESCE(ss.discord_voice_channel_id, ''), COALESCE(ss.discord_text_channel_id, ''),
  COALESCE(ss.encoder_profile_id, ''), COALESCE(ss.caption_profile_id, ''),
  COALESCE(ss.overlay_profile_id, ''), COALESCE(ss.archive_profile_id, ''), COALESCE(ss.youtube_output_id, ''),
  COALESCE(ss.encoder_input_url, ''), s.created_at, s.updated_at
FROM streams s
LEFT JOIN stream_settings ss ON ss.stream_id = s.id
ORDER BY s.created_at DESC LIMIT 100`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var streams []Stream
	for rows.Next() {
		var stream Stream
		if err := rows.Scan(&stream.ID, &stream.Name, &stream.Status, &stream.DiscordConfigID, &stream.DiscordGuildID, &stream.DiscordVoiceID, &stream.DiscordTextID, &stream.EncoderProfileID, &stream.CaptionProfileID, &stream.OverlayProfileID, &stream.ArchiveProfileID, &stream.YouTubeOutputID, &stream.EncoderInputURL, &stream.CreatedAt, &stream.UpdatedAt); err != nil {
			return nil, err
		}
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
	err := s.db.QueryRowContext(ctx, `SELECT s.id, s.name, s.status,
  COALESCE(ss.discord_config_id, ''), COALESCE(ss.discord_guild_id, ''), COALESCE(ss.discord_voice_channel_id, ''), COALESCE(ss.discord_text_channel_id, ''),
  COALESCE(ss.encoder_profile_id, ''), COALESCE(ss.caption_profile_id, ''),
  COALESCE(ss.overlay_profile_id, ''), COALESCE(ss.archive_profile_id, ''), COALESCE(ss.youtube_output_id, ''),
  COALESCE(ss.encoder_input_url, ''), s.created_at, s.updated_at
FROM streams s
LEFT JOIN stream_settings ss ON ss.stream_id = s.id
WHERE s.id = ?`, id).Scan(&stream.ID, &stream.Name, &stream.Status, &stream.DiscordConfigID, &stream.DiscordGuildID, &stream.DiscordVoiceID, &stream.DiscordTextID, &stream.EncoderProfileID, &stream.CaptionProfileID, &stream.OverlayProfileID, &stream.ArchiveProfileID, &stream.YouTubeOutputID, &stream.EncoderInputURL, &stream.CreatedAt, &stream.UpdatedAt)
	if err == sql.ErrNoRows {
		return Stream{}, ErrNotFound
	}
	return stream, err
}

func (s MariaDBStreamStore) UpdateStreamSettings(ctx context.Context, id string, settings StreamSettings) (Stream, error) {
	if _, err := s.GetStream(ctx, id); err != nil {
		return Stream{}, err
	}
	now := time.Now().UTC()
	_, err := s.db.ExecContext(ctx, `INSERT INTO stream_settings (stream_id, discord_config_id, discord_guild_id, discord_voice_channel_id, discord_text_channel_id, encoder_profile_id, caption_profile_id, overlay_profile_id, archive_profile_id, youtube_output_id, encoder_input_url, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON DUPLICATE KEY UPDATE discord_config_id = VALUES(discord_config_id), discord_guild_id = VALUES(discord_guild_id), discord_voice_channel_id = VALUES(discord_voice_channel_id), discord_text_channel_id = VALUES(discord_text_channel_id), encoder_profile_id = VALUES(encoder_profile_id), caption_profile_id = VALUES(caption_profile_id), overlay_profile_id = VALUES(overlay_profile_id), archive_profile_id = VALUES(archive_profile_id), youtube_output_id = VALUES(youtube_output_id), encoder_input_url = VALUES(encoder_input_url), updated_at = VALUES(updated_at)`,
		id, nullEmpty(settings.DiscordConfigID), nullEmpty(settings.DiscordGuildID), nullEmpty(settings.DiscordVoiceID), nullEmpty(settings.DiscordTextID), nullEmpty(settings.EncoderProfileID), nullEmpty(settings.CaptionProfileID), nullEmpty(settings.OverlayProfileID), nullEmpty(settings.ArchiveProfileID), nullEmpty(settings.YouTubeOutputID), nullEmpty(settings.EncoderInputURL), now)
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
