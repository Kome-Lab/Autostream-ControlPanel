package videocover

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
)

var (
	ErrNotFound            = errors.New("video_cover_not_found")
	ErrStaleGeneration     = errors.New("stale_job_generation")
	ErrRevisionConflict    = errors.New("revision_conflict")
	ErrIdempotencyConflict = errors.New("idempotency_conflict")
	ErrInvalidRequest      = errors.New("invalid_video_cover_request")
)

var PipelineOrder = []string{"base_or_worker_scene", "video_cover", "watermark", "video_encode", "tee_live_archive_preview"}

type Preset struct {
	ID                   string     `json:"id"`
	Name                 string     `json:"name"`
	AssetID              string     `json:"asset_id"`
	AssetVariantID       string     `json:"asset_variant_id"`
	Enabled              bool       `json:"enabled"`
	SystemPreset         bool       `json:"system_preset"`
	ReleaseKey           string     `json:"release_key,omitempty"`
	Revision             uint64     `json:"revision"`
	CreatedByUserID      string     `json:"created_by_user_id,omitempty"`
	UpdatedByUserID      string     `json:"updated_by_user_id,omitempty"`
	DeletedAt            *time.Time `json:"deleted_at,omitempty"`
	CreatedAt            time.Time  `json:"created_at"`
	UpdatedAt            time.Time  `json:"updated_at"`
	LowResolutionWarning bool       `json:"low_resolution_warning"`
}

type State struct {
	StreamID                  string    `json:"stream_id"`
	JobGeneration             uint64    `json:"job_generation"`
	DesiredActive             bool      `json:"desired_active"`
	DesiredRevision           uint64    `json:"desired_revision"`
	AppliedActive             *bool     `json:"applied_active"`
	AppliedRevision           *uint64   `json:"applied_revision"`
	AssetVariantID            string    `json:"asset_variant_id,omitempty"`
	LastErrorCode             string    `json:"last_error_code,omitempty"`
	Status                    string    `json:"status"`
	LastIdempotencyKey        string    `json:"-"`
	CreatedAt                 time.Time `json:"created_at"`
	UpdatedAt                 time.Time `json:"updated_at"`
	PipelineOrder             []string  `json:"pipeline_order"`
	CoverWatermarkIndependent bool      `json:"cover_watermark_independent"`
}

type ActionRequest struct {
	Active                bool   `json:"active"`
	ExpectedJobGeneration uint64 `json:"expected_job_generation"`
	ExpectedRevision      uint64 `json:"expected_revision"`
	IdempotencyKey        string `json:"idempotency_key"`
	HideConfirmed         bool   `json:"hide_confirmed,omitempty"`
}

type PreparedAction struct {
	State             State
	Replay            bool
	Dispatch          bool
	RequestedRevision uint64
}

type Repository interface {
	ListPresets(context.Context) ([]Preset, error)
	GetPreset(context.Context, string, bool) (Preset, error)
	CreatePreset(context.Context, Preset) (Preset, error)
	UpdatePreset(context.Context, string, Preset, uint64) (Preset, error)
	DeletePreset(context.Context, string, string, uint64) (Preset, error)
	EnsureGeneration(context.Context, string, uint64, string, bool) (State, error)
	GetCurrentState(context.Context, string) (State, error)
	PrepareAction(context.Context, string, ActionRequest) (PreparedAction, error)
	RecordApplied(context.Context, string, uint64, string, bool, uint64) (State, error)
	RecordAmbiguous(context.Context, string, uint64, string) (State, error)
	RecordFailed(context.Context, string, uint64, string, string) (State, error)
}

func NormalizeState(state State) State {
	state.PipelineOrder = append([]string(nil), PipelineOrder...)
	state.CoverWatermarkIndependent = true
	return state
}
func ValidateRequest(request ActionRequest) error {
	request.IdempotencyKey = strings.TrimSpace(request.IdempotencyKey)
	if request.ExpectedJobGeneration < 1 || request.ExpectedRevision < 1 || request.IdempotencyKey == "" || len(request.IdempotencyKey) > 128 {
		return ErrInvalidRequest
	}
	if !request.Active && !request.HideConfirmed {
		return ErrInvalidRequest
	}
	return nil
}
func RequestFingerprint(request ActionRequest) string {
	raw := fmt.Sprintf("active=%t\ngeneration=%d\nrevision=%d\nidempotency=%s", request.Active, request.ExpectedJobGeneration, request.ExpectedRevision, strings.TrimSpace(request.IdempotencyKey))
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}
