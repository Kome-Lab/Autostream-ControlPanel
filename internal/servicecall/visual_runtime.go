package servicecall

// Bundle 5 visual-runtime DTOs mirror the additive Contracts authority. They
// intentionally contain only immutable identities and safe media metadata;
// storage paths, fetch URLs, credentials, and raw bytes are not representable.

const (
	CapabilitySceneAppearanceV1       = "scene_appearance_v1"
	CapabilityLiveVideoCoverV1        = "live_video_cover_v1"
	CapabilityDiscordResolvedTargetV2 = "discord_resolved_target_v2"
	VisualReadinessReady              = "ready"
	VisualReadinessUnknown            = "unknown"
)

type VisualSafeError struct {
	Code      string `json:"code"`
	RequestID string `json:"request_id,omitempty"`
}

type MediaAssetDescriptor struct {
	AssetID             string           `json:"asset_id"`
	VariantID           string           `json:"variant_id"`
	Usage               string           `json:"usage"`
	MediaType           string           `json:"media_type"`
	Width               int              `json:"width"`
	Height              int              `json:"height"`
	ByteSize            int64            `json:"byte_size"`
	PixelCount          int64            `json:"pixel_count"`
	Animated            bool             `json:"animated"`
	AspectRatioErrorPPM *int             `json:"aspect_ratio_error_ppm,omitempty"`
	Opaque              *bool            `json:"opaque,omitempty"`
	SHA256              string           `json:"sha256"`
	Revision            uint64           `json:"revision"`
	Readiness           string           `json:"readiness"`
	Error               *VisualSafeError `json:"error,omitempty"`
}

type SceneAppearance struct {
	Generation      uint64                `json:"generation"`
	Revision        uint64                `json:"revision"`
	Capability      string                `json:"capability"`
	Readiness       string                `json:"readiness"`
	BackgroundMode  string                `json:"background_mode"`
	Background      *MediaAssetDescriptor `json:"background,omitempty"`
	HeaderTitleMode string                `json:"header_title_mode"`
	CustomTitle     string                `json:"custom_title,omitempty"`
	Error           *VisualSafeError      `json:"error,omitempty"`
}

type VideoCoverStartSnapshot struct {
	JobGeneration  uint64                `json:"job_generation"`
	Revision       uint64                `json:"revision"`
	Active         bool                  `json:"active"`
	IdempotencyKey string                `json:"idempotency_key"`
	CoverAsset     *MediaAssetDescriptor `json:"cover_asset,omitempty"`
}

type ResolvedDiscordTarget struct {
	GuildID        string `json:"guild_id"`
	TextChannelID  string `json:"text_channel_id"`
	VoiceChannelID string `json:"voice_channel_id"`
}

type DiscordTargetSnapshot struct {
	Revision uint64                `json:"revision"`
	Resolved ResolvedDiscordTarget `json:"resolved"`
}

type VideoCoverDesiredState struct {
	Active    bool   `json:"active"`
	Revision  uint64 `json:"revision"`
	Source    string `json:"source"`
	VariantID string `json:"variant_id,omitempty"`
}

type VideoCoverAppliedState struct {
	State     string `json:"state"`
	Active    *bool  `json:"active,omitempty"`
	Revision  uint64 `json:"revision,omitempty"`
	VariantID string `json:"variant_id,omitempty"`
}

type VideoVisualLayerState struct {
	Enabled   bool   `json:"enabled"`
	Revision  uint64 `json:"revision"`
	VariantID string `json:"variant_id,omitempty"`
}

type VisualAudioContinuity struct {
	ProcessRestart           int `json:"process_restart"`
	AudioEncoderRestart      int `json:"audio_encoder_restart"`
	AudioMuxRestart          int `json:"audio_mux_restart"`
	GraphRebuild             int `json:"graph_rebuild"`
	Reconnect                int `json:"reconnect"`
	SequenceLoss             int `json:"sequence_loss"`
	TimestampDiscontinuity   int `json:"timestamp_discontinuity"`
	IntentionalMuteInsertion int `json:"intentional_mute_insertion"`
}

type VisualPipelineInvariant struct {
	Layers                    []string              `json:"layers"`
	WatermarkTopmost          bool                  `json:"watermark_topmost"`
	CoverWatermarkIndependent bool                  `json:"cover_watermark_independent"`
	OutputParity              []string              `json:"output_parity"`
	AudioContinuity           VisualAudioContinuity `json:"audio_continuity"`
}

type VideoCoverAppliedWitness struct {
	GraphApplied bool                    `json:"graph_applied"`
	Generation   uint64                  `json:"generation"`
	Revision     uint64                  `json:"revision"`
	Active       bool                    `json:"active"`
	Cover        VideoVisualLayerState   `json:"cover"`
	Watermark    VideoVisualLayerState   `json:"watermark"`
	Pipeline     VisualPipelineInvariant `json:"pipeline"`
}

type VideoCoverRuntimeState struct {
	StreamID          string                    `json:"stream_id"`
	JobGeneration     uint64                    `json:"job_generation"`
	Generation        uint64                    `json:"generation"`
	Capability        string                    `json:"capability"`
	Readiness         string                    `json:"readiness"`
	Desired           VideoCoverDesiredState    `json:"desired"`
	Applied           VideoCoverAppliedState    `json:"applied"`
	Cover             VideoVisualLayerState     `json:"cover"`
	CoverAsset        *MediaAssetDescriptor     `json:"cover_asset,omitempty"`
	Watermark         VideoVisualLayerState     `json:"watermark"`
	Pipeline          VisualPipelineInvariant   `json:"pipeline"`
	AppliedWitness    *VideoCoverAppliedWitness `json:"applied_witness,omitempty"`
	NoAutomaticResend bool                      `json:"no_automatic_resend"`
	LastGoodApplied   *VideoCoverAppliedState   `json:"last_good_applied,omitempty"`
	Error             *VisualSafeError          `json:"error,omitempty"`
}

type EncoderVideoCoverApplyRequest struct {
	StreamID           string                `json:"stream_id"`
	JobGeneration      uint64                `json:"job_generation"`
	ExpectedGeneration uint64                `json:"expected_generation"`
	Revision           uint64                `json:"revision"`
	Active             bool                  `json:"active"`
	IdempotencyKey     string                `json:"idempotency_key"`
	CoverAsset         *MediaAssetDescriptor `json:"cover_asset,omitempty"`
	HideConfirmed      bool                  `json:"hide_confirmed,omitempty"`
}

type EncoderVideoCoverApplyResponse struct {
	StreamID          string                 `json:"stream_id"`
	JobGeneration     uint64                 `json:"job_generation"`
	RequestedRevision uint64                 `json:"requested_revision"`
	ActualGeneration  uint64                 `json:"actual_generation"`
	Accepted          bool                   `json:"accepted"`
	Rejected          bool                   `json:"rejected"`
	Applied           bool                   `json:"applied"`
	Outcome           string                 `json:"outcome"`
	Actual            VideoCoverRuntimeState `json:"actual"`
	Error             *VisualSafeError       `json:"error,omitempty"`
}
