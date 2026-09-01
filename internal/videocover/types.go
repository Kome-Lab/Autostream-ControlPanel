package videocover

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"
	"unicode"
	"unicode/utf8"
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

func (request *ActionRequest) UnmarshalJSON(body []byte) error {
	if !utf8.Valid(body) || rejectDuplicateActionJSONFields(body) != nil {
		return ErrInvalidRequest
	}
	type wire ActionRequest
	var value wire
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&value); err != nil {
		return ErrInvalidRequest
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return ErrInvalidRequest
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(body, &fields); err != nil || fields == nil {
		return ErrInvalidRequest
	}
	for _, name := range []string{"active", "expected_job_generation", "expected_revision", "idempotency_key"} {
		raw, exists := fields[name]
		if !exists || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			return ErrInvalidRequest
		}
	}
	_, hidePresent := fields["hide_confirmed"]
	if value.Active && hidePresent || !value.Active && (!hidePresent || !value.HideConfirmed) {
		return ErrInvalidRequest
	}
	candidate := ActionRequest(value)
	if err := ValidateRequest(candidate); err != nil {
		return err
	}
	*request = candidate
	return nil
}

type PreparedAction struct {
	State             State
	Replay            bool
	Dispatch          bool
	RequestedRevision uint64
	Outcome           string
	SafeErrorCode     string
}

type Repository interface {
	ListPresets(context.Context) ([]Preset, error)
	GetPreset(context.Context, string, bool) (Preset, error)
	CreatePreset(context.Context, Preset) (Preset, error)
	UpdatePreset(context.Context, string, Preset, uint64) (Preset, error)
	DeletePreset(context.Context, string, string, uint64) (Preset, error)
	EnsureGeneration(context.Context, string, uint64, string, bool) (State, error)
	RecordStartApplied(context.Context, string, uint64, bool, uint64) (State, error)
	GetCurrentState(context.Context, string) (State, error)
	LookupActionReplay(context.Context, string, ActionRequest) (PreparedAction, bool, error)
	LookupActionOutcome(context.Context, string, ActionRequest) (PreparedAction, bool, error)
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
	if request.ExpectedJobGeneration < 1 || request.ExpectedRevision < 1 || !validActionIdempotencyKey(request.IdempotencyKey) {
		return ErrInvalidRequest
	}
	if request.Active && request.HideConfirmed || !request.Active && !request.HideConfirmed {
		return ErrInvalidRequest
	}
	return nil
}
func RequestFingerprint(request ActionRequest) string {
	raw := fmt.Sprintf("active=%t\ngeneration=%d\nrevision=%d\nidempotency=%s", request.Active, request.ExpectedJobGeneration, request.ExpectedRevision, request.IdempotencyKey)
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

func validActionIdempotencyKey(value string) bool {
	if !utf8.ValidString(value) {
		return false
	}
	length := utf8.RuneCountInString(value)
	if length < 1 || length > 128 {
		return false
	}
	first, _ := utf8.DecodeRuneInString(value)
	last, _ := utf8.DecodeLastRuneInString(value)
	if actionIdempotencyEdgeSpace(first) || actionIdempotencyEdgeSpace(last) {
		return false
	}
	for _, char := range value {
		if char < 0x20 || char == 0x7f {
			return false
		}
	}
	return true
}

func actionIdempotencyEdgeSpace(character rune) bool {
	return unicode.IsSpace(character) || character == '\ufeff'
}

func rejectDuplicateActionJSONFields(body []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	if err := consumeUniqueActionJSONValue(decoder); err != nil {
		return err
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return ErrInvalidRequest
	}
	return nil
}

func consumeUniqueActionJSONValue(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delimiter, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delimiter {
	case '{':
		seen := map[string]struct{}{}
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return err
			}
			key, ok := keyToken.(string)
			if !ok {
				return ErrInvalidRequest
			}
			if _, exists := seen[key]; exists {
				return ErrInvalidRequest
			}
			seen[key] = struct{}{}
			if err := consumeUniqueActionJSONValue(decoder); err != nil {
				return err
			}
		}
		closing, err := decoder.Token()
		if err != nil || closing != json.Delim('}') {
			return ErrInvalidRequest
		}
	case '[':
		for decoder.More() {
			if err := consumeUniqueActionJSONValue(decoder); err != nil {
				return err
			}
		}
		closing, err := decoder.Token()
		if err != nil || closing != json.Delim(']') {
			return ErrInvalidRequest
		}
	default:
		return ErrInvalidRequest
	}
	return nil
}
