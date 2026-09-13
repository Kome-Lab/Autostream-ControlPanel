package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
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

var ErrInvalidStreamArtifact = errors.New("invalid stream artifact")
