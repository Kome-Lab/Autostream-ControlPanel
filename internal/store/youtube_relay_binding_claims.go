package store

import (
	"context"
	"errors"
	"fmt"
	"time"
)

const (
	YouTubeRelayBindingClaimStateReserved         = "reserved"
	YouTubeRelayBindingClaimStatePrepared         = "prepared"
	YouTubeRelayBindingClaimStateRecoveryRequired = "recovery_required"

	// YouTubeRelayBindingClaimPrepareStateNotAttempted is durable proof that
	// the Panel has not yet invoked the external PrepareRelayStatic provider
	// operation for this reservation.
	YouTubeRelayBindingClaimPrepareStateNotAttempted = "not_attempted"
	// YouTubeRelayBindingClaimPrepareStatePossiblyPrepared is set immediately
	// before PrepareRelayStatic. A process crash or lost provider response after
	// that point must retain the binding for external-cleanup recovery.
	YouTubeRelayBindingClaimPrepareStatePossiblyPrepared = "possibly_prepared"

	// YouTubeRelayBindingClaimDispatchStateNotDispatched is durable proof that
	// the Control Panel has not yet invoked the downstream Start dispatcher.
	YouTubeRelayBindingClaimDispatchStateNotDispatched = "not_dispatched"
	// YouTubeRelayBindingClaimDispatchStatePossiblyDispatched is set before
	// invoking Start. A failed or lost response must therefore be recovered as
	// possibly-running, not as a no-process pre-dispatch failure.
	YouTubeRelayBindingClaimDispatchStatePossiblyDispatched = "possibly_dispatched"

	youtubeRelayBindingClaimReusableLiveStreamIDMax = 255
	youtubeRelayBindingClaimBroadcastIDMax          = 255
	youtubeRelayBindingClaimLastErrorMax            = 255
	youtubeRelayBindingClaimStaticRuntimeMode       = "live_api_relay_static"
)

var (
	ErrInvalidYouTubeRelayBindingClaim  = errors.New("invalid youtube relay binding claim")
	ErrYouTubeRelayBindingClaimConflict = errors.New("youtube relay binding claim conflict")
	// ErrYouTubeRelayBindingClaimProfileRevisionConflict means the output
	// profile changed after the caller read it. It deliberately wraps the
	// generic conflict so older callers remain fail-closed as well.
	ErrYouTubeRelayBindingClaimProfileRevisionConflict = fmt.Errorf("%w: youtube output profile revision changed", ErrYouTubeRelayBindingClaimConflict)
	// ErrYouTubeRelayBindingClaimStreamOutputConflict means the stream's
	// persisted YouTube output changed after the start request was built.
	ErrYouTubeRelayBindingClaimStreamOutputConflict = fmt.Errorf("%w: stream youtube output changed", ErrYouTubeRelayBindingClaimConflict)
	ErrYouTubeRelayBindingClaimState                = errors.New("youtube relay binding claim state invalid")
	ErrYouTubeRelayBindingClaimActive               = errors.New("youtube relay binding claim active")
)

// YouTubeRelayBindingClaim reserves a fixed relay binding before the Panel
// asks YouTube to create and bind a Broadcast. It contains only non-secret
// identifiers. ReservationToken is an opaque persisted fence: callers must
// use the value returned by Reserve for every later mutation.
type YouTubeRelayBindingClaim struct {
	RelayBindingID   string `json:"relay_binding_id"`
	ReservationToken string `json:"-"`
	StreamID         string `json:"stream_id"`
	YouTubeOutputID  string `json:"youtube_output_id"`
	// ExpectedYouTubeOutputRevision is populated from the Profile returned to
	// the caller, and is consumed only by Reserve. A nil value fails closed so
	// a stale caller cannot silently bind an updated output profile.
	ExpectedYouTubeOutputRevision *uint64 `json:"-"`
	YouTubeOutputRevision         uint64  `json:"-"`
	OAuthAccountID                string  `json:"oauth_account_id"`
	ReusableLiveStreamID          string  `json:"reusable_live_stream_id"`
	BroadcastID                   string  `json:"broadcast_id,omitempty"`
	State                         string  `json:"state"`
	PrepareState                  string  `json:"prepare_state"`
	DispatchState                 string  `json:"dispatch_state"`
	// EncoderStopConfirmedAt is written only by the fenced Store marker after
	// a positive primary Encoder Stop receipt. It is durable so recovery does
	// not depend on a service's short-lived receipt cache.
	EncoderStopConfirmedAt time.Time `json:"encoder_stop_confirmed_at,omitempty"`
	LastError              string    `json:"last_error,omitempty"`
	CreatedAt              time.Time `json:"created_at"`
	UpdatedAt              time.Time `json:"updated_at"`
}

// YouTubeRelayBindingClaimPrepareFenceResolution is the durable outcome when
// a caller cannot determine whether the Prepare handoff marker committed. A
// nil error with Released=true means the Store proved no provider Prepare call
// could have occurred. Otherwise Claim is the retained recovery fence.
type YouTubeRelayBindingClaimPrepareFenceResolution struct {
	Claim    YouTubeRelayBindingClaim
	Released bool
}

// StreamYouTubeRelayBindingClaimStore provides the durable reservation needed
// by live_api_relay_static. Claims never expire automatically: an uncertain
// external bind must remain fenced until explicit recovery proves it safe.
type StreamYouTubeRelayBindingClaimStore interface {
	// ReserveStreamYouTubeRelayBindingClaim must run before the external
	// PrepareRelayStatic call.
	ReserveStreamYouTubeRelayBindingClaim(ctx context.Context, claim YouTubeRelayBindingClaim) (YouTubeRelayBindingClaim, error)
	// FinalizeStreamYouTubeRuntimeAndMarkRelayBindingPrepared atomically saves
	// the static runtime and advances its fenced reservation to prepared with a
	// possibly_prepared/not_dispatched fence.
	FinalizeStreamYouTubeRuntimeAndMarkRelayBindingPrepared(ctx context.Context, claim YouTubeRelayBindingClaim, runtime StreamYouTubeRuntime) error
	// MarkReservedStreamYouTubeRelayBindingClaimPossiblyPrepared must run
	// immediately before the external YouTube PrepareRelayStatic operation. It
	// atomically advances only the exact reserved/not_attempted reservation; a
	// lost marker response is fail-closed as possibly prepared.
	MarkReservedStreamYouTubeRelayBindingClaimPossiblyPrepared(ctx context.Context, claim YouTubeRelayBindingClaim) (YouTubeRelayBindingClaim, error)
	// ReconcileReservedStreamYouTubeRelayBindingClaimAfterPrepareFence is the
	// fail-closed recovery path after a marker write/read failure. It releases
	// only an inactive exact reserved/not_attempted claim; a
	// possibly_prepared claim becomes recovery_required and remains fenced.
	ReconcileReservedStreamYouTubeRelayBindingClaimAfterPrepareFence(ctx context.Context, claim YouTubeRelayBindingClaim) (YouTubeRelayBindingClaimPrepareFenceResolution, error)
	// MarkPreparedStreamYouTubeRelayBindingClaimPossiblyDispatched must run
	// immediately before the downstream service dispatcher invokes Start. It
	// atomically changes only the exact prepared reservation's non-secret
	// dispatch fence and is idempotent after a lost response.
	MarkPreparedStreamYouTubeRelayBindingClaimPossiblyDispatched(ctx context.Context, claim YouTubeRelayBindingClaim) (YouTubeRelayBindingClaim, error)
	// MarkPreparedStreamYouTubeRelayBindingClaimEncoderStopConfirmed records a
	// positive primary Encoder Stop receipt for the exact prepared,
	// possibly-dispatched claim. The Store owns the timestamp; caller input is
	// ignored so it cannot forge or extend a receipt.
	MarkPreparedStreamYouTubeRelayBindingClaimEncoderStopConfirmed(ctx context.Context, claim YouTubeRelayBindingClaim) (YouTubeRelayBindingClaim, error)
	// MarkStreamYouTubeRelayBindingClaimRecoveryRequired retains the claim after
	// uncertain external cleanup; it never releases it automatically. A prepared
	// not_dispatched claim has its matching static runtime removed atomically;
	// a possibly_dispatched claim must use Abandon instead.
	MarkStreamYouTubeRelayBindingClaimRecoveryRequired(ctx context.Context, claim YouTubeRelayBindingClaim) (YouTubeRelayBindingClaim, error)
	// AbandonPreparedStreamYouTubeRuntimeAndMarkRelayBindingRecoveryRequired
	// atomically removes a matching prepared static runtime and leaves its
	// external binding fenced for explicit recovery. It is for an uncertain
	// provider cleanup after dispatch failed before the broadcast started.
	AbandonPreparedStreamYouTubeRuntimeAndMarkRelayBindingRecoveryRequired(ctx context.Context, claim YouTubeRelayBindingClaim) (YouTubeRelayBindingClaim, error)
	// ReconcilePreparedStreamYouTubeRelayBindingClaimAfterDispatchFence
	// handles an uncertain dispatch-fence marker commit/read after the stream
	// has become failed. It atomically removes the matching prepared runtime and
	// retains the claim for explicit recovery without changing its
	// prepare/dispatch/broadcast identity. It never releases a claim.
	ReconcilePreparedStreamYouTubeRelayBindingClaimAfterDispatchFence(ctx context.Context, claim YouTubeRelayBindingClaim) (YouTubeRelayBindingClaim, error)
	// ReleaseReservedStreamYouTubeRelayBindingClaim must be called only after
	// the external client reports CleanupConfirmed=true and the stream is
	// inactive. It releases only an exact not_attempted reservation.
	ReleaseReservedStreamYouTubeRelayBindingClaim(ctx context.Context, claim YouTubeRelayBindingClaim) error
	// CompleteStreamYouTubeRuntimeAndReleaseRelayBindingClaim must be called
	// only after YouTube Complete succeeds; it atomically deletes both records.
	CompleteStreamYouTubeRuntimeAndReleaseRelayBindingClaim(ctx context.Context, claim YouTubeRelayBindingClaim) error
	// ResolveStreamYouTubeRelayBindingRecovery must be called only after YouTube
	// cleanup succeeds. It deletes a fenced recovery claim only when no runtime
	// remains for the stream.
	ResolveStreamYouTubeRelayBindingRecovery(ctx context.Context, claim YouTubeRelayBindingClaim) error
	GetStreamYouTubeRelayBindingClaim(ctx context.Context, relayBindingID string) (YouTubeRelayBindingClaim, error)
	GetStreamYouTubeRelayBindingClaimForStream(ctx context.Context, streamID string) (YouTubeRelayBindingClaim, error)
	HasStreamYouTubeRelayBindingClaimForOutput(ctx context.Context, youtubeOutputID string) (bool, error)
	HasStreamYouTubeRelayBindingClaim(ctx context.Context, relayBindingID string) (bool, error)
}

var _ StreamYouTubeRelayBindingClaimStore = (*MemoryStreamStore)(nil)
var _ StreamYouTubeRelayBindingClaimStore = (*MariaDBStreamStore)(nil)
