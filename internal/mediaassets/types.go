package mediaassets

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"time"
)

const (
	MaxUploadBytes    int64 = 20 * 1024 * 1024
	MaxDecodedPixels        = 40_000_000
	MaxImageSide            = 8192
	ProcessorRevision       = 1
	DraftRetention          = 24 * time.Hour
)

var (
	ErrNotFound            = errors.New("media asset not found")
	ErrInvalidImage        = errors.New("invalid image")
	ErrUnsupportedImage    = errors.New("unsupported image")
	ErrUploadTooLarge      = errors.New("upload too large")
	ErrImageDimensions     = errors.New("invalid image dimensions")
	ErrContentTypeMismatch = errors.New("content type mismatch")
	ErrAnimatedImage       = errors.New("animated image")
	ErrForbidden           = errors.New("media asset forbidden")
	ErrDraftExpired        = errors.New("upload draft expired")
	ErrDraftClaimed        = errors.New("upload draft already claimed")
	ErrIntegrity           = errors.New("media asset integrity failure")
)

type UploadSession struct {
	ID              string     `json:"id"`
	UserID          string     `json:"-"`
	OwnerType       string     `json:"owner_type"`
	ClaimedStreamID string     `json:"claimed_stream_id,omitempty"`
	ExpiresAt       time.Time  `json:"expires_at"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
	ClaimedAt       *time.Time `json:"claimed_at,omitempty"`
}

type Asset struct {
	ID                string     `json:"id"`
	OwnerUserID       string     `json:"-"`
	OwnerType         string     `json:"owner_type"`
	OwnerID           string     `json:"owner_id"`
	UploadSessionID   string     `json:"upload_session_id,omitempty"`
	UsageType         string     `json:"usage_type"`
	StorageKey        string     `json:"-"`
	SHA256            string     `json:"sha256"`
	MediaType         string     `json:"media_type"`
	ByteSize          int64      `json:"byte_size"`
	Width             int        `json:"width"`
	Height            int        `json:"height"`
	Opaque            bool       `json:"opaque"`
	ProcessorRevision int        `json:"processor_revision"`
	DeletedAt         *time.Time `json:"deleted_at,omitempty"`
	RetentionUntil    *time.Time `json:"retention_until,omitempty"`
	CreatedAt         time.Time  `json:"created_at"`
	UpdatedAt         time.Time  `json:"updated_at"`
}

type Variant struct {
	ID                string    `json:"id"`
	AssetID           string    `json:"asset_id"`
	TargetWidth       int       `json:"target_width"`
	TargetHeight      int       `json:"target_height"`
	CropMode          string    `json:"crop_mode"`
	ProcessorRevision int       `json:"processor_revision"`
	Status            string    `json:"status"`
	StorageKey        string    `json:"-"`
	SHA256            string    `json:"sha256,omitempty"`
	MediaType         string    `json:"media_type,omitempty"`
	ByteSize          int64     `json:"byte_size,omitempty"`
	Width             int       `json:"width,omitempty"`
	Height            int       `json:"height,omitempty"`
	Opaque            bool      `json:"opaque"`
	LastErrorCode     string    `json:"last_error_code,omitempty"`
	CreatedAt         time.Time `json:"created_at"`
	UpdatedAt         time.Time `json:"updated_at"`
}

type UploadInput struct {
	SessionID   string
	UserID      string
	UsageType   string
	Filename    string
	ContentType string
	Body        io.Reader
}

type ProcessedImage struct {
	StorageKey string
	SHA256     string
	MediaType  string
	ByteSize   int64
	Width      int
	Height     int
	Opaque     bool
}

type InternalAsset struct {
	Asset   Asset
	Variant Variant
	Reader  io.ReadCloser
}

// Repository owns durable metadata and reference authorization. Storage keys
// and readers are intentionally available only to trusted server code.
type Repository interface {
	CreateUploadSession(ctx context.Context, userID string, expiresAt time.Time) (UploadSession, error)
	Upload(ctx context.Context, in UploadInput) (Asset, error)
	EnsureVariant(ctx context.Context, userID, assetID string, width, height int, opaque bool) (Variant, error)
	GetAsset(ctx context.Context, userID, assetID string) (Asset, error)
	SoftDeleteAsset(ctx context.Context, userID, assetID string, now time.Time) error
	OpenInternalVariant(ctx context.Context, streamID, variantID string) (InternalAsset, error)
	GarbageCollect(ctx context.Context, now time.Time, limit int) (int, error)
}

// DraftClaimer participates in the caller's stream-create transaction. A
// failed transaction therefore leaves the draft reusable.
type DraftClaimer interface {
	ClaimDraftTx(ctx context.Context, tx *sql.Tx, userID, sessionID, streamID string, now time.Time) error
}

type IntegrityInspector interface {
	VerifyVariant(ctx context.Context, assetID, variantID string) error
}
