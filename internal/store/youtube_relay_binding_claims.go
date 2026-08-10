package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/go-sql-driver/mysql"
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

func (s MariaDBStreamStore) ReserveStreamYouTubeRelayBindingClaim(ctx context.Context, claim YouTubeRelayBindingClaim) (YouTubeRelayBindingClaim, error) {
	if err := ctx.Err(); err != nil {
		return YouTubeRelayBindingClaim{}, err
	}
	claim = normalizeYouTubeRelayBindingClaim(claim)
	if err := validateYouTubeRelayBindingClaimReservation(claim); err != nil {
		return YouTubeRelayBindingClaim{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return YouTubeRelayBindingClaim{}, err
	}
	defer tx.Rollback()
	var streamID, streamStatus string
	err = tx.QueryRowContext(ctx, `SELECT id, status FROM streams WHERE id = ? FOR UPDATE`, claim.StreamID).Scan(&streamID, &streamStatus)
	if errors.Is(err, sql.ErrNoRows) {
		return YouTubeRelayBindingClaim{}, ErrNotFound
	}
	if err != nil {
		return YouTubeRelayBindingClaim{}, err
	}
	if !strings.EqualFold(strings.TrimSpace(streamStatus), "starting") {
		return YouTubeRelayBindingClaim{}, ErrYouTubeRelayBindingClaimState
	}
	var assignedOutputID sql.NullString
	err = tx.QueryRowContext(ctx, `SELECT youtube_output_id FROM stream_settings WHERE stream_id = ? FOR UPDATE`, claim.StreamID).Scan(&assignedOutputID)
	if errors.Is(err, sql.ErrNoRows) {
		return YouTubeRelayBindingClaim{}, ErrYouTubeRelayBindingClaimStreamOutputConflict
	}
	if err != nil {
		return YouTubeRelayBindingClaim{}, err
	}
	if strings.TrimSpace(assignedOutputID.String) != claim.YouTubeOutputID {
		return YouTubeRelayBindingClaim{}, ErrYouTubeRelayBindingClaimStreamOutputConflict
	}
	var outputRevision uint64
	err = tx.QueryRowContext(ctx, `SELECT youtube_relay_binding_revision
FROM profiles WHERE id = ? AND kind = ? FOR UPDATE`, claim.YouTubeOutputID, string(ProfileYouTubeOutput)).Scan(&outputRevision)
	if errors.Is(err, sql.ErrNoRows) {
		return YouTubeRelayBindingClaim{}, ErrNotFound
	}
	if err != nil {
		return YouTubeRelayBindingClaim{}, err
	}
	if outputRevision != *claim.ExpectedYouTubeOutputRevision {
		return YouTubeRelayBindingClaim{}, ErrYouTubeRelayBindingClaimProfileRevisionConflict
	}
	now := nowYouTubeRelayBindingClaim()
	claim.ReservationToken = newUUID()
	claim.YouTubeOutputRevision = outputRevision
	claim.ExpectedYouTubeOutputRevision = nil
	claim.State = YouTubeRelayBindingClaimStateReserved
	claim.PrepareState = YouTubeRelayBindingClaimPrepareStateNotAttempted
	claim.DispatchState = YouTubeRelayBindingClaimDispatchStateNotDispatched
	claim.EncoderStopConfirmedAt = time.Time{}
	claim.BroadcastID = ""
	claim.LastError = ""
	claim.CreatedAt = now
	claim.UpdatedAt = now
	_, err = tx.ExecContext(ctx, `INSERT INTO stream_youtube_relay_binding_claims
	  (relay_binding_id, reservation_token, stream_id, youtube_output_id, youtube_output_revision, oauth_account_id, reusable_live_stream_id, broadcast_id, state, prepare_state, dispatch_state, encoder_stop_confirmed_at, last_error, created_at, updated_at)
	VALUES (?, ?, ?, ?, ?, ?, ?, NULL, ?, ?, ?, NULL, '', ?, ?)`,
		claim.RelayBindingID, claim.ReservationToken, claim.StreamID, claim.YouTubeOutputID, claim.YouTubeOutputRevision, claim.OAuthAccountID, claim.ReusableLiveStreamID, claim.State, claim.PrepareState, claim.DispatchState, claim.CreatedAt, claim.UpdatedAt)
	if err != nil {
		if isDuplicateKeyError(err) {
			return YouTubeRelayBindingClaim{}, ErrYouTubeRelayBindingClaimConflict
		}
		return YouTubeRelayBindingClaim{}, err
	}
	if err := tx.Commit(); err != nil {
		return YouTubeRelayBindingClaim{}, err
	}
	return claim, nil
}

// MarkReservedStreamYouTubeRelayBindingClaimPossiblyPrepared fences the
// external YouTube PrepareRelayStatic boundary. It intentionally advances the
// claim before the provider call: a timeout or process crash after this point
// is always recovered as a possible provider-side Broadcast creation.
func (s MariaDBStreamStore) MarkReservedStreamYouTubeRelayBindingClaimPossiblyPrepared(ctx context.Context, claim YouTubeRelayBindingClaim) (YouTubeRelayBindingClaim, error) {
	if err := ctx.Err(); err != nil {
		return YouTubeRelayBindingClaim{}, err
	}
	claim = normalizeYouTubeRelayBindingClaim(claim)
	if err := validateYouTubeRelayBindingClaimPrepareFence(claim); err != nil {
		return YouTubeRelayBindingClaim{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return YouTubeRelayBindingClaim{}, err
	}
	defer tx.Rollback()
	var streamID, streamStatus string
	err = tx.QueryRowContext(ctx, `SELECT id, status FROM streams WHERE id = ? FOR UPDATE`, claim.StreamID).Scan(&streamID, &streamStatus)
	if errors.Is(err, sql.ErrNoRows) {
		return YouTubeRelayBindingClaim{}, ErrNotFound
	}
	if err != nil {
		return YouTubeRelayBindingClaim{}, err
	}
	if !strings.EqualFold(strings.TrimSpace(streamStatus), "starting") {
		return YouTubeRelayBindingClaim{}, ErrYouTubeRelayBindingClaimState
	}
	existing, err := getYouTubeRelayBindingClaimTx(ctx, tx, claim.RelayBindingID)
	if err != nil {
		return YouTubeRelayBindingClaim{}, err
	}
	if !sameYouTubeRelayBindingReservation(existing, claim) {
		return YouTubeRelayBindingClaim{}, ErrYouTubeRelayBindingClaimConflict
	}
	if existing.State != YouTubeRelayBindingClaimStateReserved || existing.DispatchState != YouTubeRelayBindingClaimDispatchStateNotDispatched || existing.BroadcastID != "" || existing.LastError != "" {
		return YouTubeRelayBindingClaim{}, ErrYouTubeRelayBindingClaimState
	}
	if _, err := getStreamYouTubeRuntimeTx(ctx, tx, claim.StreamID); err == nil {
		return YouTubeRelayBindingClaim{}, ErrYouTubeRelayBindingClaimState
	} else if !errors.Is(err, ErrNotFound) {
		return YouTubeRelayBindingClaim{}, err
	}
	switch existing.PrepareState {
	case YouTubeRelayBindingClaimPrepareStatePossiblyPrepared:
		if err := tx.Commit(); err != nil {
			return YouTubeRelayBindingClaim{}, err
		}
		return existing, nil
	case YouTubeRelayBindingClaimPrepareStateNotAttempted:
		// Continue below.
	default:
		return YouTubeRelayBindingClaim{}, ErrYouTubeRelayBindingClaimState
	}
	now := nowYouTubeRelayBindingClaim()
	result, err := tx.ExecContext(ctx, `UPDATE stream_youtube_relay_binding_claims
SET prepare_state = ?, updated_at = ?
WHERE relay_binding_id = ? AND reservation_token = ? AND stream_id = ? AND created_at = ?
  AND state = ? AND prepare_state = ? AND dispatch_state = ?`,
		YouTubeRelayBindingClaimPrepareStatePossiblyPrepared, now,
		claim.RelayBindingID, claim.ReservationToken, claim.StreamID, claim.CreatedAt,
		YouTubeRelayBindingClaimStateReserved, YouTubeRelayBindingClaimPrepareStateNotAttempted, YouTubeRelayBindingClaimDispatchStateNotDispatched)
	if err != nil {
		return YouTubeRelayBindingClaim{}, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return YouTubeRelayBindingClaim{}, err
	}
	if affected != 1 {
		return YouTubeRelayBindingClaim{}, ErrYouTubeRelayBindingClaimState
	}
	existing.PrepareState = YouTubeRelayBindingClaimPrepareStatePossiblyPrepared
	existing.UpdatedAt = now
	if err := tx.Commit(); err != nil {
		return YouTubeRelayBindingClaim{}, err
	}
	return existing, nil
}

// ReconcileReservedStreamYouTubeRelayBindingClaimAfterPrepareFence safely
// converges a marker write/read failure without trusting the caller's observed
// phase. A not_attempted reservation proves no provider Prepare call occurred
// and is released; a possibly_prepared reservation is retained as recovery.
func (s MariaDBStreamStore) ReconcileReservedStreamYouTubeRelayBindingClaimAfterPrepareFence(ctx context.Context, claim YouTubeRelayBindingClaim) (YouTubeRelayBindingClaimPrepareFenceResolution, error) {
	if err := ctx.Err(); err != nil {
		return YouTubeRelayBindingClaimPrepareFenceResolution{}, err
	}
	claim = normalizeYouTubeRelayBindingClaim(claim)
	if err := validateYouTubeRelayBindingClaimRecovery(claim); err != nil {
		return YouTubeRelayBindingClaimPrepareFenceResolution{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return YouTubeRelayBindingClaimPrepareFenceResolution{}, err
	}
	defer tx.Rollback()
	var streamID, streamStatus string
	err = tx.QueryRowContext(ctx, `SELECT id, status FROM streams WHERE id = ? FOR UPDATE`, claim.StreamID).Scan(&streamID, &streamStatus)
	if errors.Is(err, sql.ErrNoRows) {
		return YouTubeRelayBindingClaimPrepareFenceResolution{}, ErrNotFound
	}
	if err != nil {
		return YouTubeRelayBindingClaimPrepareFenceResolution{}, err
	}
	if isActiveYouTubeRelayBindingClaimStreamStatus(streamStatus) {
		return YouTubeRelayBindingClaimPrepareFenceResolution{}, ErrYouTubeRelayBindingClaimState
	}
	existing, err := getYouTubeRelayBindingClaimTx(ctx, tx, claim.RelayBindingID)
	if err != nil {
		return YouTubeRelayBindingClaimPrepareFenceResolution{}, err
	}
	if !sameYouTubeRelayBindingReservation(existing, claim) {
		return YouTubeRelayBindingClaimPrepareFenceResolution{}, ErrYouTubeRelayBindingClaimConflict
	}
	if existing.State == YouTubeRelayBindingClaimStateRecoveryRequired {
		if err := tx.Commit(); err != nil {
			return YouTubeRelayBindingClaimPrepareFenceResolution{}, err
		}
		return YouTubeRelayBindingClaimPrepareFenceResolution{Claim: existing}, nil
	}
	if existing.State != YouTubeRelayBindingClaimStateReserved || existing.DispatchState != YouTubeRelayBindingClaimDispatchStateNotDispatched || existing.BroadcastID != "" || existing.LastError != "" || !existing.EncoderStopConfirmedAt.IsZero() {
		return YouTubeRelayBindingClaimPrepareFenceResolution{}, ErrYouTubeRelayBindingClaimState
	}
	if _, err := getStreamYouTubeRuntimeTx(ctx, tx, claim.StreamID); err == nil {
		return YouTubeRelayBindingClaimPrepareFenceResolution{}, ErrYouTubeRelayBindingClaimState
	} else if !errors.Is(err, ErrNotFound) {
		return YouTubeRelayBindingClaimPrepareFenceResolution{}, err
	}
	switch existing.PrepareState {
	case YouTubeRelayBindingClaimPrepareStateNotAttempted:
		result, err := tx.ExecContext(ctx, `DELETE FROM stream_youtube_relay_binding_claims
WHERE relay_binding_id = ? AND reservation_token = ? AND stream_id = ? AND created_at = ?
  AND state = ? AND prepare_state = ? AND dispatch_state = ? AND encoder_stop_confirmed_at IS NULL`,
			claim.RelayBindingID, claim.ReservationToken, claim.StreamID, claim.CreatedAt,
			YouTubeRelayBindingClaimStateReserved, YouTubeRelayBindingClaimPrepareStateNotAttempted, YouTubeRelayBindingClaimDispatchStateNotDispatched)
		if err != nil {
			return YouTubeRelayBindingClaimPrepareFenceResolution{}, err
		}
		affected, err := result.RowsAffected()
		if err != nil {
			return YouTubeRelayBindingClaimPrepareFenceResolution{}, err
		}
		if affected != 1 {
			return YouTubeRelayBindingClaimPrepareFenceResolution{}, ErrYouTubeRelayBindingClaimState
		}
		if err := tx.Commit(); err != nil {
			return YouTubeRelayBindingClaimPrepareFenceResolution{}, err
		}
		return YouTubeRelayBindingClaimPrepareFenceResolution{Released: true}, nil
	case YouTubeRelayBindingClaimPrepareStatePossiblyPrepared:
		now := nowYouTubeRelayBindingClaim()
		result, err := tx.ExecContext(ctx, `UPDATE stream_youtube_relay_binding_claims
SET broadcast_id = ?, state = ?, last_error = ?, updated_at = ?
WHERE relay_binding_id = ? AND reservation_token = ? AND stream_id = ? AND created_at = ?
  AND state = ? AND prepare_state = ? AND dispatch_state = ? AND encoder_stop_confirmed_at IS NULL`,
			claim.BroadcastID, YouTubeRelayBindingClaimStateRecoveryRequired, claim.LastError, now,
			claim.RelayBindingID, claim.ReservationToken, claim.StreamID, claim.CreatedAt,
			YouTubeRelayBindingClaimStateReserved, YouTubeRelayBindingClaimPrepareStatePossiblyPrepared, YouTubeRelayBindingClaimDispatchStateNotDispatched)
		if err != nil {
			return YouTubeRelayBindingClaimPrepareFenceResolution{}, err
		}
		affected, err := result.RowsAffected()
		if err != nil {
			return YouTubeRelayBindingClaimPrepareFenceResolution{}, err
		}
		if affected != 1 {
			return YouTubeRelayBindingClaimPrepareFenceResolution{}, ErrYouTubeRelayBindingClaimState
		}
		existing.BroadcastID = claim.BroadcastID
		existing.State = YouTubeRelayBindingClaimStateRecoveryRequired
		existing.LastError = claim.LastError
		existing.UpdatedAt = now
		if err := tx.Commit(); err != nil {
			return YouTubeRelayBindingClaimPrepareFenceResolution{}, err
		}
		return YouTubeRelayBindingClaimPrepareFenceResolution{Claim: existing}, nil
	default:
		return YouTubeRelayBindingClaimPrepareFenceResolution{}, ErrYouTubeRelayBindingClaimState
	}
}

func (s MariaDBStreamStore) FinalizeStreamYouTubeRuntimeAndMarkRelayBindingPrepared(ctx context.Context, claim YouTubeRelayBindingClaim, runtime StreamYouTubeRuntime) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	claim = normalizeYouTubeRelayBindingClaim(claim)
	runtime = normalizeRelayStaticRuntime(runtime)
	if err := validateYouTubeRelayBindingClaimFinalize(claim, runtime); err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var streamID, streamStatus string
	err = tx.QueryRowContext(ctx, `SELECT id, status FROM streams WHERE id = ? FOR UPDATE`, claim.StreamID).Scan(&streamID, &streamStatus)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if !strings.EqualFold(strings.TrimSpace(streamStatus), "starting") {
		return ErrYouTubeRelayBindingClaimState
	}
	existing, err := getYouTubeRelayBindingClaimTx(ctx, tx, claim.RelayBindingID)
	if err != nil {
		return err
	}
	if !sameYouTubeRelayBindingReservation(existing, claim) {
		return ErrYouTubeRelayBindingClaimConflict
	}
	switch existing.State {
	case YouTubeRelayBindingClaimStatePrepared:
		if existing.PrepareState != YouTubeRelayBindingClaimPrepareStatePossiblyPrepared || existing.BroadcastID != claim.BroadcastID {
			return ErrYouTubeRelayBindingClaimConflict
		}
		existingRuntime, err := getStreamYouTubeRuntimeTx(ctx, tx, runtime.StreamID)
		if err != nil {
			return err
		}
		if !sameRelayStaticRuntime(existingRuntime, runtime) {
			return ErrYouTubeRelayBindingClaimConflict
		}
		return tx.Commit()
	case YouTubeRelayBindingClaimStateReserved:
		if existing.PrepareState != YouTubeRelayBindingClaimPrepareStatePossiblyPrepared || existing.DispatchState != YouTubeRelayBindingClaimDispatchStateNotDispatched {
			return ErrYouTubeRelayBindingClaimState
		}
		if runtime.CompleteRetryCount != 0 || !runtime.CompleteNextRetryAt.IsZero() || strings.TrimSpace(runtime.CompleteLastError) != "" {
			return ErrInvalidYouTubeRelayBindingClaim
		}
		// Continue below.
	default:
		return ErrYouTubeRelayBindingClaimState
	}
	if existingRuntime, err := getStreamYouTubeRuntimeTx(ctx, tx, runtime.StreamID); err == nil {
		if sameRelayStaticRuntime(existingRuntime, runtime) {
			return ErrYouTubeRelayBindingClaimState
		}
		return ErrYouTubeRelayBindingClaimConflict
	} else if !errors.Is(err, ErrNotFound) {
		return err
	}
	now := nowYouTubeRelayBindingClaim()
	if runtime.CreatedAt.IsZero() {
		runtime.CreatedAt = now
	}
	runtime.UpdatedAt = now
	if err := saveStreamYouTubeRuntimeTx(ctx, tx, runtime); err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `UPDATE stream_youtube_relay_binding_claims
	SET broadcast_id = ?, state = ?, dispatch_state = ?, last_error = '', updated_at = ?
	WHERE relay_binding_id = ? AND reservation_token = ? AND stream_id = ? AND created_at = ? AND state = ? AND prepare_state = ? AND dispatch_state = ?`,
		claim.BroadcastID, YouTubeRelayBindingClaimStatePrepared, YouTubeRelayBindingClaimDispatchStateNotDispatched, now,
		claim.RelayBindingID, claim.ReservationToken, claim.StreamID, claim.CreatedAt, YouTubeRelayBindingClaimStateReserved, YouTubeRelayBindingClaimPrepareStatePossiblyPrepared, YouTubeRelayBindingClaimDispatchStateNotDispatched)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected != 1 {
		return ErrYouTubeRelayBindingClaimState
	}
	return tx.Commit()
}

// MarkPreparedStreamYouTubeRelayBindingClaimPossiblyDispatched fences the
// external Start boundary. It intentionally changes state before dispatch: a
// timeout after that point is treated as potentially running and can never use
// the no-process recovery path.
func (s MariaDBStreamStore) MarkPreparedStreamYouTubeRelayBindingClaimPossiblyDispatched(ctx context.Context, claim YouTubeRelayBindingClaim) (YouTubeRelayBindingClaim, error) {
	if err := ctx.Err(); err != nil {
		return YouTubeRelayBindingClaim{}, err
	}
	claim = normalizeYouTubeRelayBindingClaim(claim)
	if err := validateYouTubeRelayBindingClaimPreparedFence(claim); err != nil {
		return YouTubeRelayBindingClaim{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return YouTubeRelayBindingClaim{}, err
	}
	defer tx.Rollback()
	var streamID, streamStatus string
	err = tx.QueryRowContext(ctx, `SELECT id, status FROM streams WHERE id = ? FOR UPDATE`, claim.StreamID).Scan(&streamID, &streamStatus)
	if errors.Is(err, sql.ErrNoRows) {
		return YouTubeRelayBindingClaim{}, ErrNotFound
	}
	if err != nil {
		return YouTubeRelayBindingClaim{}, err
	}
	if !strings.EqualFold(strings.TrimSpace(streamStatus), "starting") {
		return YouTubeRelayBindingClaim{}, ErrYouTubeRelayBindingClaimState
	}
	existing, err := getYouTubeRelayBindingClaimTx(ctx, tx, claim.RelayBindingID)
	if err != nil {
		return YouTubeRelayBindingClaim{}, err
	}
	if !sameYouTubeRelayBindingReservation(existing, claim) {
		return YouTubeRelayBindingClaim{}, ErrYouTubeRelayBindingClaimConflict
	}
	if existing.State != YouTubeRelayBindingClaimStatePrepared || existing.PrepareState != YouTubeRelayBindingClaimPrepareStatePossiblyPrepared || existing.BroadcastID != claim.BroadcastID {
		return YouTubeRelayBindingClaim{}, ErrYouTubeRelayBindingClaimState
	}
	runtime, err := getStreamYouTubeRuntimeTx(ctx, tx, claim.StreamID)
	if err != nil {
		return YouTubeRelayBindingClaim{}, err
	}
	if !runtimeMatchesYouTubeRelayBindingClaim(runtime, existing) || runtime.CompleteRetryCount != 0 || !runtime.CompleteNextRetryAt.IsZero() || strings.TrimSpace(runtime.CompleteLastError) != "" {
		return YouTubeRelayBindingClaim{}, ErrYouTubeRelayBindingClaimState
	}
	switch existing.DispatchState {
	case YouTubeRelayBindingClaimDispatchStatePossiblyDispatched:
		if err := tx.Commit(); err != nil {
			return YouTubeRelayBindingClaim{}, err
		}
		return existing, nil
	case YouTubeRelayBindingClaimDispatchStateNotDispatched:
		// Continue below.
	default:
		return YouTubeRelayBindingClaim{}, ErrYouTubeRelayBindingClaimState
	}
	now := nowYouTubeRelayBindingClaim()
	result, err := tx.ExecContext(ctx, `UPDATE stream_youtube_relay_binding_claims
SET dispatch_state = ?, updated_at = ?
WHERE relay_binding_id = ? AND reservation_token = ? AND stream_id = ? AND created_at = ?
  AND broadcast_id = ? AND state = ? AND prepare_state = ? AND dispatch_state = ?`,
		YouTubeRelayBindingClaimDispatchStatePossiblyDispatched, now,
		claim.RelayBindingID, claim.ReservationToken, claim.StreamID, claim.CreatedAt,
		claim.BroadcastID, YouTubeRelayBindingClaimStatePrepared, YouTubeRelayBindingClaimPrepareStatePossiblyPrepared, YouTubeRelayBindingClaimDispatchStateNotDispatched)
	if err != nil {
		return YouTubeRelayBindingClaim{}, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return YouTubeRelayBindingClaim{}, err
	}
	if affected != 1 {
		return YouTubeRelayBindingClaim{}, ErrYouTubeRelayBindingClaimState
	}
	existing.DispatchState = YouTubeRelayBindingClaimDispatchStatePossiblyDispatched
	existing.UpdatedAt = now
	if err := tx.Commit(); err != nil {
		return YouTubeRelayBindingClaim{}, err
	}
	return existing, nil
}

// MarkPreparedStreamYouTubeRelayBindingClaimEncoderStopConfirmed stores a
// non-secret receipt immediately after the primary Encoder acknowledges Stop.
// It accepts the prepared claim or its runtime-free recovery descendant. The
// timestamp is created in this transaction, never accepted from a caller.
func (s MariaDBStreamStore) MarkPreparedStreamYouTubeRelayBindingClaimEncoderStopConfirmed(ctx context.Context, claim YouTubeRelayBindingClaim) (YouTubeRelayBindingClaim, error) {
	if err := ctx.Err(); err != nil {
		return YouTubeRelayBindingClaim{}, err
	}
	claim = normalizeYouTubeRelayBindingClaim(claim)
	if err := validateYouTubeRelayBindingClaimPreparedFence(claim); err != nil {
		return YouTubeRelayBindingClaim{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return YouTubeRelayBindingClaim{}, err
	}
	defer tx.Rollback()
	var streamID, streamStatus string
	err = tx.QueryRowContext(ctx, `SELECT id, status FROM streams WHERE id = ? FOR UPDATE`, claim.StreamID).Scan(&streamID, &streamStatus)
	if errors.Is(err, sql.ErrNoRows) {
		return YouTubeRelayBindingClaim{}, ErrNotFound
	}
	if err != nil {
		return YouTubeRelayBindingClaim{}, err
	}
	if !isYouTubeRelayBindingClaimEncoderStopReceiptStreamStatus(streamStatus) {
		return YouTubeRelayBindingClaim{}, ErrYouTubeRelayBindingClaimState
	}
	existing, err := getYouTubeRelayBindingClaimTx(ctx, tx, claim.RelayBindingID)
	if err != nil {
		return YouTubeRelayBindingClaim{}, err
	}
	if !sameYouTubeRelayBindingReservation(existing, claim) {
		return YouTubeRelayBindingClaim{}, ErrYouTubeRelayBindingClaimConflict
	}
	if existing.PrepareState != YouTubeRelayBindingClaimPrepareStatePossiblyPrepared || existing.DispatchState != YouTubeRelayBindingClaimDispatchStatePossiblyDispatched || existing.BroadcastID != claim.BroadcastID {
		return YouTubeRelayBindingClaim{}, ErrYouTubeRelayBindingClaimState
	}
	switch existing.State {
	case YouTubeRelayBindingClaimStatePrepared:
		if existing.LastError != "" {
			return YouTubeRelayBindingClaim{}, ErrYouTubeRelayBindingClaimState
		}
		runtime, err := getStreamYouTubeRuntimeTx(ctx, tx, claim.StreamID)
		if err != nil {
			return YouTubeRelayBindingClaim{}, err
		}
		if !runtimeMatchesYouTubeRelayBindingClaim(runtime, existing) {
			return YouTubeRelayBindingClaim{}, ErrYouTubeRelayBindingClaimState
		}
	case YouTubeRelayBindingClaimStateRecoveryRequired:
		if _, err := getStreamYouTubeRuntimeTx(ctx, tx, claim.StreamID); err == nil {
			return YouTubeRelayBindingClaim{}, ErrYouTubeRelayBindingClaimState
		} else if !errors.Is(err, ErrNotFound) {
			return YouTubeRelayBindingClaim{}, err
		}
	default:
		return YouTubeRelayBindingClaim{}, ErrYouTubeRelayBindingClaimState
	}
	if !existing.EncoderStopConfirmedAt.IsZero() {
		if err := tx.Commit(); err != nil {
			return YouTubeRelayBindingClaim{}, err
		}
		return existing, nil
	}
	now := nowYouTubeRelayBindingClaim()
	result, err := tx.ExecContext(ctx, `UPDATE stream_youtube_relay_binding_claims
SET encoder_stop_confirmed_at = ?, updated_at = ?
WHERE relay_binding_id = ? AND reservation_token = ? AND stream_id = ? AND created_at = ?
  AND broadcast_id = ? AND state IN (?, ?) AND prepare_state = ? AND dispatch_state = ? AND encoder_stop_confirmed_at IS NULL`,
		now, now, claim.RelayBindingID, claim.ReservationToken, claim.StreamID, claim.CreatedAt,
		claim.BroadcastID, YouTubeRelayBindingClaimStatePrepared, YouTubeRelayBindingClaimStateRecoveryRequired, YouTubeRelayBindingClaimPrepareStatePossiblyPrepared, YouTubeRelayBindingClaimDispatchStatePossiblyDispatched)
	if err != nil {
		return YouTubeRelayBindingClaim{}, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return YouTubeRelayBindingClaim{}, err
	}
	if affected != 1 {
		return YouTubeRelayBindingClaim{}, ErrYouTubeRelayBindingClaimState
	}
	existing.EncoderStopConfirmedAt = now
	existing.UpdatedAt = now
	if err := tx.Commit(); err != nil {
		return YouTubeRelayBindingClaim{}, err
	}
	return existing, nil
}

func (s MariaDBStreamStore) MarkStreamYouTubeRelayBindingClaimRecoveryRequired(ctx context.Context, claim YouTubeRelayBindingClaim) (YouTubeRelayBindingClaim, error) {
	if err := ctx.Err(); err != nil {
		return YouTubeRelayBindingClaim{}, err
	}
	claim = normalizeYouTubeRelayBindingClaim(claim)
	if err := validateYouTubeRelayBindingClaimRecovery(claim); err != nil {
		return YouTubeRelayBindingClaim{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return YouTubeRelayBindingClaim{}, err
	}
	defer tx.Rollback()
	var streamID string
	err = tx.QueryRowContext(ctx, `SELECT id FROM streams WHERE id = ? FOR UPDATE`, claim.StreamID).Scan(&streamID)
	if errors.Is(err, sql.ErrNoRows) {
		return YouTubeRelayBindingClaim{}, ErrNotFound
	}
	if err != nil {
		return YouTubeRelayBindingClaim{}, err
	}
	existing, err := getYouTubeRelayBindingClaimTx(ctx, tx, claim.RelayBindingID)
	if err != nil {
		return YouTubeRelayBindingClaim{}, err
	}
	if !sameYouTubeRelayBindingReservation(existing, claim) {
		return YouTubeRelayBindingClaim{}, ErrYouTubeRelayBindingClaimConflict
	}
	if existing.DispatchState != YouTubeRelayBindingClaimDispatchStateNotDispatched {
		return YouTubeRelayBindingClaim{}, ErrYouTubeRelayBindingClaimState
	}
	switch existing.State {
	case YouTubeRelayBindingClaimStateReserved:
		if (existing.PrepareState != YouTubeRelayBindingClaimPrepareStateNotAttempted && existing.PrepareState != YouTubeRelayBindingClaimPrepareStatePossiblyPrepared) || !existing.EncoderStopConfirmedAt.IsZero() {
			return YouTubeRelayBindingClaim{}, ErrYouTubeRelayBindingClaimState
		}
		if _, err := getStreamYouTubeRuntimeTx(ctx, tx, claim.StreamID); err == nil {
			return YouTubeRelayBindingClaim{}, ErrYouTubeRelayBindingClaimState
		} else if !errors.Is(err, ErrNotFound) {
			return YouTubeRelayBindingClaim{}, err
		}
	case YouTubeRelayBindingClaimStatePrepared:
		if existing.PrepareState != YouTubeRelayBindingClaimPrepareStatePossiblyPrepared || existing.BroadcastID != claim.BroadcastID {
			return YouTubeRelayBindingClaim{}, ErrYouTubeRelayBindingClaimConflict
		}
		runtime, err := getStreamYouTubeRuntimeTx(ctx, tx, claim.StreamID)
		if err != nil {
			return YouTubeRelayBindingClaim{}, err
		}
		if !runtimeMatchesYouTubeRelayBindingClaim(runtime, existing) {
			return YouTubeRelayBindingClaim{}, ErrYouTubeRelayBindingClaimState
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM stream_youtube_runtimes WHERE stream_id = ?`, claim.StreamID); err != nil {
			return YouTubeRelayBindingClaim{}, err
		}
	case YouTubeRelayBindingClaimStateRecoveryRequired:
		if existing.BroadcastID != claim.BroadcastID {
			return YouTubeRelayBindingClaim{}, ErrYouTubeRelayBindingClaimConflict
		}
		if _, err := getStreamYouTubeRuntimeTx(ctx, tx, claim.StreamID); err == nil {
			return YouTubeRelayBindingClaim{}, ErrYouTubeRelayBindingClaimState
		} else if !errors.Is(err, ErrNotFound) {
			return YouTubeRelayBindingClaim{}, err
		}
	default:
		return YouTubeRelayBindingClaim{}, ErrYouTubeRelayBindingClaimState
	}
	now := nowYouTubeRelayBindingClaim()
	result, err := tx.ExecContext(ctx, `UPDATE stream_youtube_relay_binding_claims
SET broadcast_id = ?, state = ?, last_error = ?, updated_at = ?
WHERE relay_binding_id = ? AND reservation_token = ? AND stream_id = ? AND created_at = ?
  AND state IN (?, ?, ?) AND dispatch_state = ?`,
		claim.BroadcastID, YouTubeRelayBindingClaimStateRecoveryRequired, claim.LastError, now,
		claim.RelayBindingID, claim.ReservationToken, claim.StreamID, claim.CreatedAt,
		YouTubeRelayBindingClaimStateReserved, YouTubeRelayBindingClaimStatePrepared, YouTubeRelayBindingClaimStateRecoveryRequired,
		YouTubeRelayBindingClaimDispatchStateNotDispatched)
	if err != nil {
		return YouTubeRelayBindingClaim{}, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return YouTubeRelayBindingClaim{}, err
	}
	if affected != 1 {
		return YouTubeRelayBindingClaim{}, ErrYouTubeRelayBindingClaimState
	}
	claim.State = YouTubeRelayBindingClaimStateRecoveryRequired
	claim.PrepareState = existing.PrepareState
	claim.DispatchState = YouTubeRelayBindingClaimDispatchStateNotDispatched
	claim.UpdatedAt = now
	if err := tx.Commit(); err != nil {
		return YouTubeRelayBindingClaim{}, err
	}
	return claim, nil
}

// AbandonPreparedStreamYouTubeRuntimeAndMarkRelayBindingRecoveryRequired
// records an uncertain provider cleanup after a prepared runtime could not be
// dispatched. The runtime deletion and recovery fence transition share one
// transaction so completion retry cannot race the recovery workflow.
func (s MariaDBStreamStore) AbandonPreparedStreamYouTubeRuntimeAndMarkRelayBindingRecoveryRequired(ctx context.Context, claim YouTubeRelayBindingClaim) (YouTubeRelayBindingClaim, error) {
	if err := ctx.Err(); err != nil {
		return YouTubeRelayBindingClaim{}, err
	}
	claim = normalizeYouTubeRelayBindingClaim(claim)
	if err := validateYouTubeRelayBindingClaimRecovery(claim); err != nil {
		return YouTubeRelayBindingClaim{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return YouTubeRelayBindingClaim{}, err
	}
	defer tx.Rollback()
	var streamID, streamStatus string
	err = tx.QueryRowContext(ctx, `SELECT id, status FROM streams WHERE id = ? FOR UPDATE`, claim.StreamID).Scan(&streamID, &streamStatus)
	if errors.Is(err, sql.ErrNoRows) {
		return YouTubeRelayBindingClaim{}, ErrNotFound
	}
	if err != nil {
		return YouTubeRelayBindingClaim{}, err
	}
	if !strings.EqualFold(strings.TrimSpace(streamStatus), "failed") {
		return YouTubeRelayBindingClaim{}, ErrYouTubeRelayBindingClaimState
	}
	existing, err := getYouTubeRelayBindingClaimTx(ctx, tx, claim.RelayBindingID)
	if err != nil {
		return YouTubeRelayBindingClaim{}, err
	}
	if !sameYouTubeRelayBindingReservation(existing, claim) {
		return YouTubeRelayBindingClaim{}, ErrYouTubeRelayBindingClaimConflict
	}
	if existing.PrepareState != YouTubeRelayBindingClaimPrepareStatePossiblyPrepared || existing.DispatchState != YouTubeRelayBindingClaimDispatchStatePossiblyDispatched {
		return YouTubeRelayBindingClaim{}, ErrYouTubeRelayBindingClaimState
	}
	switch existing.State {
	case YouTubeRelayBindingClaimStateRecoveryRequired:
		if existing.BroadcastID != claim.BroadcastID || existing.LastError != claim.LastError {
			return YouTubeRelayBindingClaim{}, ErrYouTubeRelayBindingClaimConflict
		}
		if _, err := getStreamYouTubeRuntimeTx(ctx, tx, claim.StreamID); err == nil {
			return YouTubeRelayBindingClaim{}, ErrYouTubeRelayBindingClaimState
		} else if !errors.Is(err, ErrNotFound) {
			return YouTubeRelayBindingClaim{}, err
		}
		if err := tx.Commit(); err != nil {
			return YouTubeRelayBindingClaim{}, err
		}
		return existing, nil
	case YouTubeRelayBindingClaimStatePrepared:
		if existing.BroadcastID != claim.BroadcastID {
			return YouTubeRelayBindingClaim{}, ErrYouTubeRelayBindingClaimConflict
		}
	default:
		return YouTubeRelayBindingClaim{}, ErrYouTubeRelayBindingClaimState
	}
	runtime, err := getStreamYouTubeRuntimeTx(ctx, tx, claim.StreamID)
	if err != nil {
		return YouTubeRelayBindingClaim{}, err
	}
	if !runtimeMatchesYouTubeRelayBindingClaim(runtime, existing) {
		return YouTubeRelayBindingClaim{}, ErrYouTubeRelayBindingClaimState
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM stream_youtube_runtimes WHERE stream_id = ?`, claim.StreamID); err != nil {
		return YouTubeRelayBindingClaim{}, err
	}
	now := nowYouTubeRelayBindingClaim()
	result, err := tx.ExecContext(ctx, `UPDATE stream_youtube_relay_binding_claims
SET state = ?, last_error = ?, updated_at = ?
WHERE relay_binding_id = ? AND reservation_token = ? AND stream_id = ? AND created_at = ?
	  AND broadcast_id = ? AND state = ? AND prepare_state = ? AND dispatch_state = ?`,
		YouTubeRelayBindingClaimStateRecoveryRequired, claim.LastError, now,
		claim.RelayBindingID, claim.ReservationToken, claim.StreamID, claim.CreatedAt,
		claim.BroadcastID, YouTubeRelayBindingClaimStatePrepared, YouTubeRelayBindingClaimPrepareStatePossiblyPrepared, YouTubeRelayBindingClaimDispatchStatePossiblyDispatched)
	if err != nil {
		return YouTubeRelayBindingClaim{}, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return YouTubeRelayBindingClaim{}, err
	}
	if affected != 1 {
		return YouTubeRelayBindingClaim{}, ErrYouTubeRelayBindingClaimState
	}
	claim.State = YouTubeRelayBindingClaimStateRecoveryRequired
	claim.PrepareState = YouTubeRelayBindingClaimPrepareStatePossiblyPrepared
	claim.DispatchState = YouTubeRelayBindingClaimDispatchStatePossiblyDispatched
	claim.UpdatedAt = now
	if err := tx.Commit(); err != nil {
		return YouTubeRelayBindingClaim{}, err
	}
	return claim, nil
}

// ReconcilePreparedStreamYouTubeRelayBindingClaimAfterDispatchFence closes the
// write/read-loss window for the durable dispatch marker. The caller must have
// already moved the stream to failed. Regardless of whether the marker made it
// to storage, the static runtime is removed atomically and the external
// broadcast stays fenced for explicit recovery.
func (s MariaDBStreamStore) ReconcilePreparedStreamYouTubeRelayBindingClaimAfterDispatchFence(ctx context.Context, claim YouTubeRelayBindingClaim) (YouTubeRelayBindingClaim, error) {
	if err := ctx.Err(); err != nil {
		return YouTubeRelayBindingClaim{}, err
	}
	claim = normalizeYouTubeRelayBindingClaim(claim)
	if err := validateYouTubeRelayBindingClaimRecovery(claim); err != nil {
		return YouTubeRelayBindingClaim{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return YouTubeRelayBindingClaim{}, err
	}
	defer tx.Rollback()
	var streamID, streamStatus string
	err = tx.QueryRowContext(ctx, `SELECT id, status FROM streams WHERE id = ? FOR UPDATE`, claim.StreamID).Scan(&streamID, &streamStatus)
	if errors.Is(err, sql.ErrNoRows) {
		return YouTubeRelayBindingClaim{}, ErrNotFound
	}
	if err != nil {
		return YouTubeRelayBindingClaim{}, err
	}
	if !strings.EqualFold(strings.TrimSpace(streamStatus), "failed") {
		return YouTubeRelayBindingClaim{}, ErrYouTubeRelayBindingClaimState
	}
	existing, err := getYouTubeRelayBindingClaimTx(ctx, tx, claim.RelayBindingID)
	if err != nil {
		return YouTubeRelayBindingClaim{}, err
	}
	if !sameYouTubeRelayBindingReservation(existing, claim) {
		return YouTubeRelayBindingClaim{}, ErrYouTubeRelayBindingClaimConflict
	}
	if existing.PrepareState != YouTubeRelayBindingClaimPrepareStatePossiblyPrepared || existing.BroadcastID != claim.BroadcastID || !isYouTubeRelayBindingClaimDispatchFenceState(existing.DispatchState) {
		return YouTubeRelayBindingClaim{}, ErrYouTubeRelayBindingClaimState
	}
	switch existing.State {
	case YouTubeRelayBindingClaimStateRecoveryRequired:
		if existing.LastError != claim.LastError {
			return YouTubeRelayBindingClaim{}, ErrYouTubeRelayBindingClaimConflict
		}
		if _, err := getStreamYouTubeRuntimeTx(ctx, tx, claim.StreamID); err == nil {
			return YouTubeRelayBindingClaim{}, ErrYouTubeRelayBindingClaimState
		} else if !errors.Is(err, ErrNotFound) {
			return YouTubeRelayBindingClaim{}, err
		}
		if err := tx.Commit(); err != nil {
			return YouTubeRelayBindingClaim{}, err
		}
		return existing, nil
	case YouTubeRelayBindingClaimStatePrepared:
		if existing.LastError != "" {
			return YouTubeRelayBindingClaim{}, ErrYouTubeRelayBindingClaimState
		}
	default:
		return YouTubeRelayBindingClaim{}, ErrYouTubeRelayBindingClaimState
	}
	runtime, err := getStreamYouTubeRuntimeTx(ctx, tx, claim.StreamID)
	if err != nil {
		return YouTubeRelayBindingClaim{}, err
	}
	if !runtimeMatchesYouTubeRelayBindingClaim(runtime, existing) {
		return YouTubeRelayBindingClaim{}, ErrYouTubeRelayBindingClaimState
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM stream_youtube_runtimes WHERE stream_id = ?`, claim.StreamID); err != nil {
		return YouTubeRelayBindingClaim{}, err
	}
	now := nowYouTubeRelayBindingClaim()
	result, err := tx.ExecContext(ctx, `UPDATE stream_youtube_relay_binding_claims
SET state = ?, last_error = ?, updated_at = ?
WHERE relay_binding_id = ? AND reservation_token = ? AND stream_id = ? AND created_at = ?
  AND broadcast_id = ? AND state = ? AND prepare_state = ? AND dispatch_state IN (?, ?)`,
		YouTubeRelayBindingClaimStateRecoveryRequired, claim.LastError, now,
		claim.RelayBindingID, claim.ReservationToken, claim.StreamID, claim.CreatedAt,
		claim.BroadcastID, YouTubeRelayBindingClaimStatePrepared, YouTubeRelayBindingClaimPrepareStatePossiblyPrepared,
		YouTubeRelayBindingClaimDispatchStateNotDispatched, YouTubeRelayBindingClaimDispatchStatePossiblyDispatched)
	if err != nil {
		return YouTubeRelayBindingClaim{}, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return YouTubeRelayBindingClaim{}, err
	}
	if affected != 1 {
		return YouTubeRelayBindingClaim{}, ErrYouTubeRelayBindingClaimState
	}
	existing.State = YouTubeRelayBindingClaimStateRecoveryRequired
	existing.LastError = claim.LastError
	existing.UpdatedAt = now
	if err := tx.Commit(); err != nil {
		return YouTubeRelayBindingClaim{}, err
	}
	return existing, nil
}

func (s MariaDBStreamStore) ReleaseReservedStreamYouTubeRelayBindingClaim(ctx context.Context, claim YouTubeRelayBindingClaim) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	claim = normalizeYouTubeRelayBindingClaim(claim)
	if err := validateYouTubeRelayBindingClaimReservationFence(claim); err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var streamID, streamStatus string
	err = tx.QueryRowContext(ctx, `SELECT id, status FROM streams WHERE id = ? FOR UPDATE`, claim.StreamID).Scan(&streamID, &streamStatus)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	existing, err := getYouTubeRelayBindingClaimTx(ctx, tx, claim.RelayBindingID)
	if err != nil {
		return err
	}
	if !sameYouTubeRelayBindingReservation(existing, claim) {
		return ErrYouTubeRelayBindingClaimConflict
	}
	if isActiveYouTubeRelayBindingClaimStreamStatus(streamStatus) {
		return ErrYouTubeRelayBindingClaimState
	}
	if existing.State != YouTubeRelayBindingClaimStateReserved || existing.PrepareState != YouTubeRelayBindingClaimPrepareStateNotAttempted || existing.DispatchState != YouTubeRelayBindingClaimDispatchStateNotDispatched || !existing.EncoderStopConfirmedAt.IsZero() || existing.BroadcastID != "" || existing.LastError != "" {
		return ErrYouTubeRelayBindingClaimState
	}
	if _, err := getStreamYouTubeRuntimeTx(ctx, tx, claim.StreamID); err == nil {
		return ErrYouTubeRelayBindingClaimState
	} else if !errors.Is(err, ErrNotFound) {
		return err
	}
	result, err := tx.ExecContext(ctx, `DELETE FROM stream_youtube_relay_binding_claims
WHERE relay_binding_id = ? AND reservation_token = ? AND stream_id = ? AND created_at = ?
  AND state = ? AND prepare_state = ? AND dispatch_state = ? AND encoder_stop_confirmed_at IS NULL`,
		claim.RelayBindingID, claim.ReservationToken, claim.StreamID, claim.CreatedAt,
		YouTubeRelayBindingClaimStateReserved, YouTubeRelayBindingClaimPrepareStateNotAttempted, YouTubeRelayBindingClaimDispatchStateNotDispatched)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected != 1 {
		return ErrYouTubeRelayBindingClaimState
	}
	return tx.Commit()
}

func (s MariaDBStreamStore) CompleteStreamYouTubeRuntimeAndReleaseRelayBindingClaim(ctx context.Context, claim YouTubeRelayBindingClaim) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	claim = normalizeYouTubeRelayBindingClaim(claim)
	if err := validateYouTubeRelayBindingClaimPreparedFence(claim); err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var streamID, streamStatus string
	err = tx.QueryRowContext(ctx, `SELECT id, status FROM streams WHERE id = ? FOR UPDATE`, claim.StreamID).Scan(&streamID, &streamStatus)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	existing, err := getYouTubeRelayBindingClaimTx(ctx, tx, claim.RelayBindingID)
	if err != nil {
		return err
	}
	if !sameYouTubeRelayBindingReservation(existing, claim) {
		return ErrYouTubeRelayBindingClaimConflict
	}
	if existing.State != YouTubeRelayBindingClaimStatePrepared || existing.PrepareState != YouTubeRelayBindingClaimPrepareStatePossiblyPrepared || existing.BroadcastID != claim.BroadcastID || existing.DispatchState != YouTubeRelayBindingClaimDispatchStatePossiblyDispatched {
		return ErrYouTubeRelayBindingClaimState
	}
	// A fixed relay may be reused only after the normal stop lifecycle has
	// durably recorded a completed stream. `failed` also represents an
	// unacknowledged force stop or a partial downstream failure, so it is not
	// evidence that the Encoder has stopped sending to the shared relay.
	if !strings.EqualFold(strings.TrimSpace(streamStatus), "completed") {
		return ErrYouTubeRelayBindingClaimState
	}
	runtime, err := getStreamYouTubeRuntimeTx(ctx, tx, claim.StreamID)
	if err != nil {
		return err
	}
	if !runtimeMatchesYouTubeRelayBindingClaim(runtime, existing) {
		return ErrYouTubeRelayBindingClaimState
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM stream_youtube_runtimes WHERE stream_id = ?`, claim.StreamID); err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `DELETE FROM stream_youtube_relay_binding_claims
WHERE relay_binding_id = ? AND reservation_token = ? AND stream_id = ? AND created_at = ?
  AND state = ? AND prepare_state = ? AND dispatch_state = ?`,
		claim.RelayBindingID, claim.ReservationToken, claim.StreamID, claim.CreatedAt,
		YouTubeRelayBindingClaimStatePrepared, YouTubeRelayBindingClaimPrepareStatePossiblyPrepared, YouTubeRelayBindingClaimDispatchStatePossiblyDispatched)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected != 1 {
		return ErrYouTubeRelayBindingClaimState
	}
	return tx.Commit()
}

func (s MariaDBStreamStore) ResolveStreamYouTubeRelayBindingRecovery(ctx context.Context, claim YouTubeRelayBindingClaim) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	claim = normalizeYouTubeRelayBindingClaim(claim)
	if err := validateYouTubeRelayBindingClaimPreparedFence(claim); err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var streamID, streamStatus string
	err = tx.QueryRowContext(ctx, `SELECT id, status FROM streams WHERE id = ? FOR UPDATE`, claim.StreamID).Scan(&streamID, &streamStatus)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if isActiveYouTubeRelayBindingClaimStreamStatus(streamStatus) {
		return ErrYouTubeRelayBindingClaimState
	}
	existing, err := getYouTubeRelayBindingClaimTx(ctx, tx, claim.RelayBindingID)
	if err != nil {
		return err
	}
	if !sameYouTubeRelayBindingReservation(existing, claim) {
		return ErrYouTubeRelayBindingClaimConflict
	}
	if existing.State != YouTubeRelayBindingClaimStateRecoveryRequired || existing.BroadcastID != claim.BroadcastID || !isYouTubeRelayBindingClaimDispatchFenceState(existing.DispatchState) {
		return ErrYouTubeRelayBindingClaimState
	}
	if existing.DispatchState == YouTubeRelayBindingClaimDispatchStatePossiblyDispatched && existing.EncoderStopConfirmedAt.IsZero() {
		return ErrYouTubeRelayBindingClaimState
	}
	if _, err := getStreamYouTubeRuntimeTx(ctx, tx, claim.StreamID); err == nil {
		return ErrYouTubeRelayBindingClaimState
	} else if !errors.Is(err, ErrNotFound) {
		return err
	}
	result, err := tx.ExecContext(ctx, `DELETE FROM stream_youtube_relay_binding_claims
WHERE relay_binding_id = ? AND reservation_token = ? AND stream_id = ? AND created_at = ?
  AND broadcast_id = ? AND state = ?
  AND (dispatch_state = ? OR (dispatch_state = ? AND encoder_stop_confirmed_at IS NOT NULL))`,
		claim.RelayBindingID, claim.ReservationToken, claim.StreamID, claim.CreatedAt, claim.BroadcastID, YouTubeRelayBindingClaimStateRecoveryRequired,
		YouTubeRelayBindingClaimDispatchStateNotDispatched, YouTubeRelayBindingClaimDispatchStatePossiblyDispatched)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected != 1 {
		return ErrYouTubeRelayBindingClaimState
	}
	return tx.Commit()
}

func (s MariaDBStreamStore) GetStreamYouTubeRelayBindingClaim(ctx context.Context, relayBindingID string) (YouTubeRelayBindingClaim, error) {
	if err := ctx.Err(); err != nil {
		return YouTubeRelayBindingClaim{}, err
	}
	if !isValidYouTubeRelayBindingID(relayBindingID) {
		return YouTubeRelayBindingClaim{}, ErrInvalidYouTubeRelayBindingClaim
	}
	claim, err := scanYouTubeRelayBindingClaim(s.db.QueryRowContext(ctx, youtubeRelayBindingClaimSelect+` WHERE relay_binding_id = ?`, relayBindingID))
	if errors.Is(err, sql.ErrNoRows) {
		return YouTubeRelayBindingClaim{}, ErrNotFound
	}
	return claim, err
}

func (s MariaDBStreamStore) GetStreamYouTubeRelayBindingClaimForStream(ctx context.Context, streamID string) (YouTubeRelayBindingClaim, error) {
	if err := ctx.Err(); err != nil {
		return YouTubeRelayBindingClaim{}, err
	}
	streamID = strings.TrimSpace(streamID)
	if !isCanonicalUUID(streamID) {
		return YouTubeRelayBindingClaim{}, ErrInvalidYouTubeRelayBindingClaim
	}
	claim, err := scanYouTubeRelayBindingClaim(s.db.QueryRowContext(ctx, youtubeRelayBindingClaimSelect+` WHERE stream_id = ?`, streamID))
	if errors.Is(err, sql.ErrNoRows) {
		return YouTubeRelayBindingClaim{}, ErrNotFound
	}
	return claim, err
}

func (s MariaDBStreamStore) HasStreamYouTubeRelayBindingClaimForOutput(ctx context.Context, youtubeOutputID string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	youtubeOutputID = strings.TrimSpace(youtubeOutputID)
	if !isCanonicalUUID(youtubeOutputID) {
		return false, ErrInvalidYouTubeRelayBindingClaim
	}
	var exists bool
	err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM stream_youtube_relay_binding_claims WHERE youtube_output_id = ?)`, youtubeOutputID).Scan(&exists)
	return exists, err
}

func (s MariaDBStreamStore) HasStreamYouTubeRelayBindingClaim(ctx context.Context, relayBindingID string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if !isValidYouTubeRelayBindingID(relayBindingID) {
		return false, ErrInvalidYouTubeRelayBindingClaim
	}
	var exists bool
	err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM stream_youtube_relay_binding_claims WHERE relay_binding_id = ?)`, relayBindingID).Scan(&exists)
	return exists, err
}

func (s *MemoryStreamStore) ReserveStreamYouTubeRelayBindingClaim(ctx context.Context, claim YouTubeRelayBindingClaim) (YouTubeRelayBindingClaim, error) {
	if err := ctx.Err(); err != nil {
		return YouTubeRelayBindingClaim{}, err
	}
	claim = normalizeYouTubeRelayBindingClaim(claim)
	if err := validateYouTubeRelayBindingClaimReservation(claim); err != nil {
		return YouTubeRelayBindingClaim{}, err
	}
	s.youtubeRelayBindingOutputMu.Lock()
	defer s.youtubeRelayBindingOutputMu.Unlock()
	s.mu.Lock()
	profiles := s.relayBindingClaimProfiles
	s.mu.Unlock()
	if profiles == nil {
		// A memory stream store without its paired profile store cannot safely
		// verify the output fence. Fail closed just as a missing MariaDB output
		// profile would.
		return YouTubeRelayBindingClaim{}, ErrNotFound
	}
	profiles.mu.Lock()
	defer profiles.mu.Unlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	stream, ok := s.streams[claim.StreamID]
	if !ok {
		return YouTubeRelayBindingClaim{}, ErrNotFound
	}
	if !strings.EqualFold(strings.TrimSpace(stream.Status), "starting") {
		return YouTubeRelayBindingClaim{}, ErrYouTubeRelayBindingClaimState
	}
	if strings.TrimSpace(stream.YouTubeOutputID) != claim.YouTubeOutputID {
		return YouTubeRelayBindingClaim{}, ErrYouTubeRelayBindingClaimStreamOutputConflict
	}
	output, ok := profiles.profiles[claim.YouTubeOutputID]
	if !ok || output.Kind != ProfileYouTubeOutput {
		return YouTubeRelayBindingClaim{}, ErrNotFound
	}
	if output.YouTubeRelayBindingRevision != *claim.ExpectedYouTubeOutputRevision {
		return YouTubeRelayBindingClaim{}, ErrYouTubeRelayBindingClaimProfileRevisionConflict
	}
	if s.youtubeRelayBindingClaims == nil {
		s.youtubeRelayBindingClaims = map[string]YouTubeRelayBindingClaim{}
	}
	if memoryYouTubeRelayBindingClaimConflict(s.youtubeRelayBindingClaims, claim) {
		return YouTubeRelayBindingClaim{}, ErrYouTubeRelayBindingClaimConflict
	}
	now := nowYouTubeRelayBindingClaim()
	claim.ReservationToken = newUUID()
	claim.YouTubeOutputRevision = output.YouTubeRelayBindingRevision
	claim.ExpectedYouTubeOutputRevision = nil
	claim.State = YouTubeRelayBindingClaimStateReserved
	claim.PrepareState = YouTubeRelayBindingClaimPrepareStateNotAttempted
	claim.DispatchState = YouTubeRelayBindingClaimDispatchStateNotDispatched
	claim.EncoderStopConfirmedAt = time.Time{}
	claim.BroadcastID = ""
	claim.LastError = ""
	claim.CreatedAt = now
	claim.UpdatedAt = now
	s.youtubeRelayBindingClaims[claim.RelayBindingID] = claim
	return claim, nil
}

func (s *MemoryStreamStore) MarkReservedStreamYouTubeRelayBindingClaimPossiblyPrepared(ctx context.Context, claim YouTubeRelayBindingClaim) (YouTubeRelayBindingClaim, error) {
	if err := ctx.Err(); err != nil {
		return YouTubeRelayBindingClaim{}, err
	}
	claim = normalizeYouTubeRelayBindingClaim(claim)
	if err := validateYouTubeRelayBindingClaimPrepareFence(claim); err != nil {
		return YouTubeRelayBindingClaim{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	stream, ok := s.streams[claim.StreamID]
	if !ok {
		return YouTubeRelayBindingClaim{}, ErrNotFound
	}
	if !strings.EqualFold(strings.TrimSpace(stream.Status), "starting") {
		return YouTubeRelayBindingClaim{}, ErrYouTubeRelayBindingClaimState
	}
	existing, ok := s.youtubeRelayBindingClaims[claim.RelayBindingID]
	if !ok {
		return YouTubeRelayBindingClaim{}, ErrNotFound
	}
	if !sameYouTubeRelayBindingReservation(existing, claim) {
		return YouTubeRelayBindingClaim{}, ErrYouTubeRelayBindingClaimConflict
	}
	if existing.State != YouTubeRelayBindingClaimStateReserved || existing.DispatchState != YouTubeRelayBindingClaimDispatchStateNotDispatched || existing.BroadcastID != "" || existing.LastError != "" {
		return YouTubeRelayBindingClaim{}, ErrYouTubeRelayBindingClaimState
	}
	if _, ok := s.youtubeRuntimes[claim.StreamID]; ok {
		return YouTubeRelayBindingClaim{}, ErrYouTubeRelayBindingClaimState
	}
	switch existing.PrepareState {
	case YouTubeRelayBindingClaimPrepareStatePossiblyPrepared:
		return existing, nil
	case YouTubeRelayBindingClaimPrepareStateNotAttempted:
		existing.PrepareState = YouTubeRelayBindingClaimPrepareStatePossiblyPrepared
		existing.UpdatedAt = nowYouTubeRelayBindingClaim()
		s.youtubeRelayBindingClaims[existing.RelayBindingID] = existing
		return existing, nil
	default:
		return YouTubeRelayBindingClaim{}, ErrYouTubeRelayBindingClaimState
	}
}

func (s *MemoryStreamStore) ReconcileReservedStreamYouTubeRelayBindingClaimAfterPrepareFence(ctx context.Context, claim YouTubeRelayBindingClaim) (YouTubeRelayBindingClaimPrepareFenceResolution, error) {
	if err := ctx.Err(); err != nil {
		return YouTubeRelayBindingClaimPrepareFenceResolution{}, err
	}
	claim = normalizeYouTubeRelayBindingClaim(claim)
	if err := validateYouTubeRelayBindingClaimRecovery(claim); err != nil {
		return YouTubeRelayBindingClaimPrepareFenceResolution{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	stream, ok := s.streams[claim.StreamID]
	if !ok {
		return YouTubeRelayBindingClaimPrepareFenceResolution{}, ErrNotFound
	}
	if isActiveYouTubeRelayBindingClaimStreamStatus(stream.Status) {
		return YouTubeRelayBindingClaimPrepareFenceResolution{}, ErrYouTubeRelayBindingClaimState
	}
	existing, ok := s.youtubeRelayBindingClaims[claim.RelayBindingID]
	if !ok {
		return YouTubeRelayBindingClaimPrepareFenceResolution{}, ErrNotFound
	}
	if !sameYouTubeRelayBindingReservation(existing, claim) {
		return YouTubeRelayBindingClaimPrepareFenceResolution{}, ErrYouTubeRelayBindingClaimConflict
	}
	if existing.State == YouTubeRelayBindingClaimStateRecoveryRequired {
		return YouTubeRelayBindingClaimPrepareFenceResolution{Claim: existing}, nil
	}
	if existing.State != YouTubeRelayBindingClaimStateReserved || existing.DispatchState != YouTubeRelayBindingClaimDispatchStateNotDispatched || existing.BroadcastID != "" || existing.LastError != "" || !existing.EncoderStopConfirmedAt.IsZero() {
		return YouTubeRelayBindingClaimPrepareFenceResolution{}, ErrYouTubeRelayBindingClaimState
	}
	if _, ok := s.youtubeRuntimes[claim.StreamID]; ok {
		return YouTubeRelayBindingClaimPrepareFenceResolution{}, ErrYouTubeRelayBindingClaimState
	}
	switch existing.PrepareState {
	case YouTubeRelayBindingClaimPrepareStateNotAttempted:
		delete(s.youtubeRelayBindingClaims, existing.RelayBindingID)
		return YouTubeRelayBindingClaimPrepareFenceResolution{Released: true}, nil
	case YouTubeRelayBindingClaimPrepareStatePossiblyPrepared:
		existing.BroadcastID = claim.BroadcastID
		existing.State = YouTubeRelayBindingClaimStateRecoveryRequired
		existing.LastError = claim.LastError
		existing.UpdatedAt = nowYouTubeRelayBindingClaim()
		s.youtubeRelayBindingClaims[existing.RelayBindingID] = existing
		return YouTubeRelayBindingClaimPrepareFenceResolution{Claim: existing}, nil
	default:
		return YouTubeRelayBindingClaimPrepareFenceResolution{}, ErrYouTubeRelayBindingClaimState
	}
}

func (s *MemoryStreamStore) FinalizeStreamYouTubeRuntimeAndMarkRelayBindingPrepared(ctx context.Context, claim YouTubeRelayBindingClaim, runtime StreamYouTubeRuntime) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	claim = normalizeYouTubeRelayBindingClaim(claim)
	runtime = normalizeRelayStaticRuntime(runtime)
	if err := validateYouTubeRelayBindingClaimFinalize(claim, runtime); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	stream, ok := s.streams[claim.StreamID]
	if !ok {
		return ErrNotFound
	}
	if !strings.EqualFold(strings.TrimSpace(stream.Status), "starting") {
		return ErrYouTubeRelayBindingClaimState
	}
	existing, ok := s.youtubeRelayBindingClaims[claim.RelayBindingID]
	if !ok {
		return ErrNotFound
	}
	if !sameYouTubeRelayBindingReservation(existing, claim) {
		return ErrYouTubeRelayBindingClaimConflict
	}
	switch existing.State {
	case YouTubeRelayBindingClaimStatePrepared:
		if existing.PrepareState != YouTubeRelayBindingClaimPrepareStatePossiblyPrepared || existing.BroadcastID != claim.BroadcastID || !sameRelayStaticRuntime(s.youtubeRuntimes[runtime.StreamID], runtime) {
			return ErrYouTubeRelayBindingClaimConflict
		}
		return nil
	case YouTubeRelayBindingClaimStateReserved:
		if existing.PrepareState != YouTubeRelayBindingClaimPrepareStatePossiblyPrepared || existing.DispatchState != YouTubeRelayBindingClaimDispatchStateNotDispatched {
			return ErrYouTubeRelayBindingClaimState
		}
		if runtime.CompleteRetryCount != 0 || !runtime.CompleteNextRetryAt.IsZero() || strings.TrimSpace(runtime.CompleteLastError) != "" {
			return ErrInvalidYouTubeRelayBindingClaim
		}
		if current, ok := s.youtubeRuntimes[runtime.StreamID]; ok {
			if sameRelayStaticRuntime(current, runtime) {
				return ErrYouTubeRelayBindingClaimState
			}
			return ErrYouTubeRelayBindingClaimConflict
		}
	default:
		return ErrYouTubeRelayBindingClaimState
	}
	now := nowYouTubeRelayBindingClaim()
	if runtime.CreatedAt.IsZero() {
		runtime.CreatedAt = now
	}
	runtime.UpdatedAt = now
	s.youtubeRuntimes[runtime.StreamID] = runtime
	existing.BroadcastID = claim.BroadcastID
	existing.State = YouTubeRelayBindingClaimStatePrepared
	existing.DispatchState = YouTubeRelayBindingClaimDispatchStateNotDispatched
	existing.LastError = ""
	existing.UpdatedAt = now
	s.youtubeRelayBindingClaims[existing.RelayBindingID] = existing
	return nil
}

func (s *MemoryStreamStore) MarkPreparedStreamYouTubeRelayBindingClaimPossiblyDispatched(ctx context.Context, claim YouTubeRelayBindingClaim) (YouTubeRelayBindingClaim, error) {
	if err := ctx.Err(); err != nil {
		return YouTubeRelayBindingClaim{}, err
	}
	claim = normalizeYouTubeRelayBindingClaim(claim)
	if err := validateYouTubeRelayBindingClaimPreparedFence(claim); err != nil {
		return YouTubeRelayBindingClaim{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	stream, ok := s.streams[claim.StreamID]
	if !ok {
		return YouTubeRelayBindingClaim{}, ErrNotFound
	}
	if !strings.EqualFold(strings.TrimSpace(stream.Status), "starting") {
		return YouTubeRelayBindingClaim{}, ErrYouTubeRelayBindingClaimState
	}
	existing, ok := s.youtubeRelayBindingClaims[claim.RelayBindingID]
	if !ok {
		return YouTubeRelayBindingClaim{}, ErrNotFound
	}
	if !sameYouTubeRelayBindingReservation(existing, claim) {
		return YouTubeRelayBindingClaim{}, ErrYouTubeRelayBindingClaimConflict
	}
	if existing.State != YouTubeRelayBindingClaimStatePrepared || existing.PrepareState != YouTubeRelayBindingClaimPrepareStatePossiblyPrepared || existing.BroadcastID != claim.BroadcastID {
		return YouTubeRelayBindingClaim{}, ErrYouTubeRelayBindingClaimState
	}
	runtime, ok := s.youtubeRuntimes[claim.StreamID]
	if !ok || !runtimeMatchesYouTubeRelayBindingClaim(runtime, existing) || runtime.CompleteRetryCount != 0 || !runtime.CompleteNextRetryAt.IsZero() || strings.TrimSpace(runtime.CompleteLastError) != "" {
		return YouTubeRelayBindingClaim{}, ErrYouTubeRelayBindingClaimState
	}
	switch existing.DispatchState {
	case YouTubeRelayBindingClaimDispatchStatePossiblyDispatched:
		return existing, nil
	case YouTubeRelayBindingClaimDispatchStateNotDispatched:
		existing.DispatchState = YouTubeRelayBindingClaimDispatchStatePossiblyDispatched
		existing.UpdatedAt = nowYouTubeRelayBindingClaim()
		s.youtubeRelayBindingClaims[existing.RelayBindingID] = existing
		return existing, nil
	default:
		return YouTubeRelayBindingClaim{}, ErrYouTubeRelayBindingClaimState
	}
}

func (s *MemoryStreamStore) MarkPreparedStreamYouTubeRelayBindingClaimEncoderStopConfirmed(ctx context.Context, claim YouTubeRelayBindingClaim) (YouTubeRelayBindingClaim, error) {
	if err := ctx.Err(); err != nil {
		return YouTubeRelayBindingClaim{}, err
	}
	claim = normalizeYouTubeRelayBindingClaim(claim)
	if err := validateYouTubeRelayBindingClaimPreparedFence(claim); err != nil {
		return YouTubeRelayBindingClaim{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	stream, ok := s.streams[claim.StreamID]
	if !ok {
		return YouTubeRelayBindingClaim{}, ErrNotFound
	}
	if !isYouTubeRelayBindingClaimEncoderStopReceiptStreamStatus(stream.Status) {
		return YouTubeRelayBindingClaim{}, ErrYouTubeRelayBindingClaimState
	}
	existing, ok := s.youtubeRelayBindingClaims[claim.RelayBindingID]
	if !ok {
		return YouTubeRelayBindingClaim{}, ErrNotFound
	}
	if !sameYouTubeRelayBindingReservation(existing, claim) {
		return YouTubeRelayBindingClaim{}, ErrYouTubeRelayBindingClaimConflict
	}
	if existing.PrepareState != YouTubeRelayBindingClaimPrepareStatePossiblyPrepared || existing.DispatchState != YouTubeRelayBindingClaimDispatchStatePossiblyDispatched || existing.BroadcastID != claim.BroadcastID {
		return YouTubeRelayBindingClaim{}, ErrYouTubeRelayBindingClaimState
	}
	switch existing.State {
	case YouTubeRelayBindingClaimStatePrepared:
		if existing.LastError != "" {
			return YouTubeRelayBindingClaim{}, ErrYouTubeRelayBindingClaimState
		}
		runtime, ok := s.youtubeRuntimes[claim.StreamID]
		if !ok || !runtimeMatchesYouTubeRelayBindingClaim(runtime, existing) {
			return YouTubeRelayBindingClaim{}, ErrYouTubeRelayBindingClaimState
		}
	case YouTubeRelayBindingClaimStateRecoveryRequired:
		if _, ok := s.youtubeRuntimes[claim.StreamID]; ok {
			return YouTubeRelayBindingClaim{}, ErrYouTubeRelayBindingClaimState
		}
	default:
		return YouTubeRelayBindingClaim{}, ErrYouTubeRelayBindingClaimState
	}
	if !existing.EncoderStopConfirmedAt.IsZero() {
		return existing, nil
	}
	existing.EncoderStopConfirmedAt = nowYouTubeRelayBindingClaim()
	existing.UpdatedAt = existing.EncoderStopConfirmedAt
	s.youtubeRelayBindingClaims[existing.RelayBindingID] = existing
	return existing, nil
}

func (s *MemoryStreamStore) AbandonPreparedStreamYouTubeRuntimeAndMarkRelayBindingRecoveryRequired(ctx context.Context, claim YouTubeRelayBindingClaim) (YouTubeRelayBindingClaim, error) {
	if err := ctx.Err(); err != nil {
		return YouTubeRelayBindingClaim{}, err
	}
	claim = normalizeYouTubeRelayBindingClaim(claim)
	if err := validateYouTubeRelayBindingClaimRecovery(claim); err != nil {
		return YouTubeRelayBindingClaim{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	stream, ok := s.streams[claim.StreamID]
	if !ok {
		return YouTubeRelayBindingClaim{}, ErrNotFound
	}
	if !strings.EqualFold(strings.TrimSpace(stream.Status), "failed") {
		return YouTubeRelayBindingClaim{}, ErrYouTubeRelayBindingClaimState
	}
	existing, ok := s.youtubeRelayBindingClaims[claim.RelayBindingID]
	if !ok {
		return YouTubeRelayBindingClaim{}, ErrNotFound
	}
	if !sameYouTubeRelayBindingReservation(existing, claim) {
		return YouTubeRelayBindingClaim{}, ErrYouTubeRelayBindingClaimConflict
	}
	if existing.PrepareState != YouTubeRelayBindingClaimPrepareStatePossiblyPrepared || existing.DispatchState != YouTubeRelayBindingClaimDispatchStatePossiblyDispatched {
		return YouTubeRelayBindingClaim{}, ErrYouTubeRelayBindingClaimState
	}
	switch existing.State {
	case YouTubeRelayBindingClaimStateRecoveryRequired:
		if existing.BroadcastID != claim.BroadcastID || existing.LastError != claim.LastError {
			return YouTubeRelayBindingClaim{}, ErrYouTubeRelayBindingClaimConflict
		}
		if _, ok := s.youtubeRuntimes[claim.StreamID]; ok {
			return YouTubeRelayBindingClaim{}, ErrYouTubeRelayBindingClaimState
		}
		return existing, nil
	case YouTubeRelayBindingClaimStatePrepared:
		if existing.BroadcastID != claim.BroadcastID {
			return YouTubeRelayBindingClaim{}, ErrYouTubeRelayBindingClaimConflict
		}
	default:
		return YouTubeRelayBindingClaim{}, ErrYouTubeRelayBindingClaimState
	}
	runtime, ok := s.youtubeRuntimes[claim.StreamID]
	if !ok || !runtimeMatchesYouTubeRelayBindingClaim(runtime, existing) {
		return YouTubeRelayBindingClaim{}, ErrYouTubeRelayBindingClaimState
	}
	delete(s.youtubeRuntimes, claim.StreamID)
	existing.State = YouTubeRelayBindingClaimStateRecoveryRequired
	existing.LastError = claim.LastError
	existing.UpdatedAt = nowYouTubeRelayBindingClaim()
	s.youtubeRelayBindingClaims[existing.RelayBindingID] = existing
	return existing, nil
}

func (s *MemoryStreamStore) ReconcilePreparedStreamYouTubeRelayBindingClaimAfterDispatchFence(ctx context.Context, claim YouTubeRelayBindingClaim) (YouTubeRelayBindingClaim, error) {
	if err := ctx.Err(); err != nil {
		return YouTubeRelayBindingClaim{}, err
	}
	claim = normalizeYouTubeRelayBindingClaim(claim)
	if err := validateYouTubeRelayBindingClaimRecovery(claim); err != nil {
		return YouTubeRelayBindingClaim{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	stream, ok := s.streams[claim.StreamID]
	if !ok {
		return YouTubeRelayBindingClaim{}, ErrNotFound
	}
	if !strings.EqualFold(strings.TrimSpace(stream.Status), "failed") {
		return YouTubeRelayBindingClaim{}, ErrYouTubeRelayBindingClaimState
	}
	existing, ok := s.youtubeRelayBindingClaims[claim.RelayBindingID]
	if !ok {
		return YouTubeRelayBindingClaim{}, ErrNotFound
	}
	if !sameYouTubeRelayBindingReservation(existing, claim) {
		return YouTubeRelayBindingClaim{}, ErrYouTubeRelayBindingClaimConflict
	}
	if existing.PrepareState != YouTubeRelayBindingClaimPrepareStatePossiblyPrepared || existing.BroadcastID != claim.BroadcastID || !isYouTubeRelayBindingClaimDispatchFenceState(existing.DispatchState) {
		return YouTubeRelayBindingClaim{}, ErrYouTubeRelayBindingClaimState
	}
	switch existing.State {
	case YouTubeRelayBindingClaimStateRecoveryRequired:
		if existing.LastError != claim.LastError {
			return YouTubeRelayBindingClaim{}, ErrYouTubeRelayBindingClaimConflict
		}
		if _, ok := s.youtubeRuntimes[claim.StreamID]; ok {
			return YouTubeRelayBindingClaim{}, ErrYouTubeRelayBindingClaimState
		}
		return existing, nil
	case YouTubeRelayBindingClaimStatePrepared:
		if existing.LastError != "" {
			return YouTubeRelayBindingClaim{}, ErrYouTubeRelayBindingClaimState
		}
	default:
		return YouTubeRelayBindingClaim{}, ErrYouTubeRelayBindingClaimState
	}
	runtime, ok := s.youtubeRuntimes[claim.StreamID]
	if !ok || !runtimeMatchesYouTubeRelayBindingClaim(runtime, existing) {
		return YouTubeRelayBindingClaim{}, ErrYouTubeRelayBindingClaimState
	}
	delete(s.youtubeRuntimes, claim.StreamID)
	existing.State = YouTubeRelayBindingClaimStateRecoveryRequired
	existing.LastError = claim.LastError
	existing.UpdatedAt = nowYouTubeRelayBindingClaim()
	s.youtubeRelayBindingClaims[existing.RelayBindingID] = existing
	return existing, nil
}

func (s *MemoryStreamStore) MarkStreamYouTubeRelayBindingClaimRecoveryRequired(ctx context.Context, claim YouTubeRelayBindingClaim) (YouTubeRelayBindingClaim, error) {
	if err := ctx.Err(); err != nil {
		return YouTubeRelayBindingClaim{}, err
	}
	claim = normalizeYouTubeRelayBindingClaim(claim)
	if err := validateYouTubeRelayBindingClaimRecovery(claim); err != nil {
		return YouTubeRelayBindingClaim{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.streams[claim.StreamID]; !ok {
		return YouTubeRelayBindingClaim{}, ErrNotFound
	}
	existing, ok := s.youtubeRelayBindingClaims[claim.RelayBindingID]
	if !ok {
		return YouTubeRelayBindingClaim{}, ErrNotFound
	}
	if !sameYouTubeRelayBindingReservation(existing, claim) {
		return YouTubeRelayBindingClaim{}, ErrYouTubeRelayBindingClaimConflict
	}
	if existing.DispatchState != YouTubeRelayBindingClaimDispatchStateNotDispatched {
		return YouTubeRelayBindingClaim{}, ErrYouTubeRelayBindingClaimState
	}
	switch existing.State {
	case YouTubeRelayBindingClaimStateReserved:
		if (existing.PrepareState != YouTubeRelayBindingClaimPrepareStateNotAttempted && existing.PrepareState != YouTubeRelayBindingClaimPrepareStatePossiblyPrepared) || !existing.EncoderStopConfirmedAt.IsZero() {
			return YouTubeRelayBindingClaim{}, ErrYouTubeRelayBindingClaimState
		}
		if _, ok := s.youtubeRuntimes[claim.StreamID]; ok {
			return YouTubeRelayBindingClaim{}, ErrYouTubeRelayBindingClaimState
		}
	case YouTubeRelayBindingClaimStatePrepared:
		if existing.PrepareState != YouTubeRelayBindingClaimPrepareStatePossiblyPrepared || existing.BroadcastID != claim.BroadcastID {
			return YouTubeRelayBindingClaim{}, ErrYouTubeRelayBindingClaimConflict
		}
		runtime, ok := s.youtubeRuntimes[claim.StreamID]
		if !ok || !runtimeMatchesYouTubeRelayBindingClaim(runtime, existing) {
			return YouTubeRelayBindingClaim{}, ErrYouTubeRelayBindingClaimState
		}
		delete(s.youtubeRuntimes, claim.StreamID)
	case YouTubeRelayBindingClaimStateRecoveryRequired:
		if existing.BroadcastID != claim.BroadcastID {
			return YouTubeRelayBindingClaim{}, ErrYouTubeRelayBindingClaimConflict
		}
		if _, ok := s.youtubeRuntimes[claim.StreamID]; ok {
			return YouTubeRelayBindingClaim{}, ErrYouTubeRelayBindingClaimState
		}
	default:
		return YouTubeRelayBindingClaim{}, ErrYouTubeRelayBindingClaimState
	}
	existing.BroadcastID = claim.BroadcastID
	existing.State = YouTubeRelayBindingClaimStateRecoveryRequired
	existing.DispatchState = YouTubeRelayBindingClaimDispatchStateNotDispatched
	existing.LastError = claim.LastError
	existing.UpdatedAt = nowYouTubeRelayBindingClaim()
	s.youtubeRelayBindingClaims[existing.RelayBindingID] = existing
	return existing, nil
}

func (s *MemoryStreamStore) ReleaseReservedStreamYouTubeRelayBindingClaim(ctx context.Context, claim YouTubeRelayBindingClaim) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	claim = normalizeYouTubeRelayBindingClaim(claim)
	if err := validateYouTubeRelayBindingClaimReservationFence(claim); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	stream, ok := s.streams[claim.StreamID]
	if !ok {
		return ErrNotFound
	}
	existing, ok := s.youtubeRelayBindingClaims[claim.RelayBindingID]
	if !ok {
		return ErrNotFound
	}
	if !sameYouTubeRelayBindingReservation(existing, claim) {
		return ErrYouTubeRelayBindingClaimConflict
	}
	if isActiveYouTubeRelayBindingClaimStreamStatus(stream.Status) {
		return ErrYouTubeRelayBindingClaimState
	}
	if existing.State != YouTubeRelayBindingClaimStateReserved || existing.PrepareState != YouTubeRelayBindingClaimPrepareStateNotAttempted || existing.DispatchState != YouTubeRelayBindingClaimDispatchStateNotDispatched || !existing.EncoderStopConfirmedAt.IsZero() {
		return ErrYouTubeRelayBindingClaimState
	}
	if existing.BroadcastID != "" || existing.LastError != "" {
		return ErrYouTubeRelayBindingClaimState
	}
	if _, ok := s.youtubeRuntimes[claim.StreamID]; ok {
		return ErrYouTubeRelayBindingClaimState
	}
	delete(s.youtubeRelayBindingClaims, claim.RelayBindingID)
	return nil
}

func (s *MemoryStreamStore) CompleteStreamYouTubeRuntimeAndReleaseRelayBindingClaim(ctx context.Context, claim YouTubeRelayBindingClaim) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	claim = normalizeYouTubeRelayBindingClaim(claim)
	if err := validateYouTubeRelayBindingClaimPreparedFence(claim); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	existing, ok := s.youtubeRelayBindingClaims[claim.RelayBindingID]
	if !ok {
		return ErrNotFound
	}
	if !sameYouTubeRelayBindingReservation(existing, claim) {
		return ErrYouTubeRelayBindingClaimConflict
	}
	if existing.State != YouTubeRelayBindingClaimStatePrepared || existing.PrepareState != YouTubeRelayBindingClaimPrepareStatePossiblyPrepared || existing.BroadcastID != claim.BroadcastID || existing.DispatchState != YouTubeRelayBindingClaimDispatchStatePossiblyDispatched {
		return ErrYouTubeRelayBindingClaimState
	}
	stream, ok := s.streams[claim.StreamID]
	if !ok {
		return ErrNotFound
	}
	if !strings.EqualFold(strings.TrimSpace(stream.Status), "completed") {
		return ErrYouTubeRelayBindingClaimState
	}
	runtime, ok := s.youtubeRuntimes[claim.StreamID]
	if !ok {
		return ErrYouTubeRelayBindingClaimState
	}
	if !runtimeMatchesYouTubeRelayBindingClaim(runtime, existing) {
		return ErrYouTubeRelayBindingClaimState
	}
	delete(s.youtubeRuntimes, claim.StreamID)
	delete(s.youtubeRelayBindingClaims, claim.RelayBindingID)
	return nil
}

func (s *MemoryStreamStore) ResolveStreamYouTubeRelayBindingRecovery(ctx context.Context, claim YouTubeRelayBindingClaim) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	claim = normalizeYouTubeRelayBindingClaim(claim)
	if err := validateYouTubeRelayBindingClaimPreparedFence(claim); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	stream, ok := s.streams[claim.StreamID]
	if !ok {
		return ErrNotFound
	}
	if isActiveYouTubeRelayBindingClaimStreamStatus(stream.Status) {
		return ErrYouTubeRelayBindingClaimState
	}
	existing, ok := s.youtubeRelayBindingClaims[claim.RelayBindingID]
	if !ok {
		return ErrNotFound
	}
	if !sameYouTubeRelayBindingReservation(existing, claim) {
		return ErrYouTubeRelayBindingClaimConflict
	}
	if existing.State != YouTubeRelayBindingClaimStateRecoveryRequired || existing.BroadcastID != claim.BroadcastID || !isYouTubeRelayBindingClaimDispatchFenceState(existing.DispatchState) {
		return ErrYouTubeRelayBindingClaimState
	}
	if existing.DispatchState == YouTubeRelayBindingClaimDispatchStatePossiblyDispatched && existing.EncoderStopConfirmedAt.IsZero() {
		return ErrYouTubeRelayBindingClaimState
	}
	if _, ok := s.youtubeRuntimes[claim.StreamID]; ok {
		return ErrYouTubeRelayBindingClaimState
	}
	delete(s.youtubeRelayBindingClaims, claim.RelayBindingID)
	return nil
}

func (s *MemoryStreamStore) GetStreamYouTubeRelayBindingClaim(ctx context.Context, relayBindingID string) (YouTubeRelayBindingClaim, error) {
	if err := ctx.Err(); err != nil {
		return YouTubeRelayBindingClaim{}, err
	}
	if !isValidYouTubeRelayBindingID(relayBindingID) {
		return YouTubeRelayBindingClaim{}, ErrInvalidYouTubeRelayBindingClaim
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	claim, ok := s.youtubeRelayBindingClaims[relayBindingID]
	if !ok {
		return YouTubeRelayBindingClaim{}, ErrNotFound
	}
	if !isValidYouTubeRelayBindingID(claim.RelayBindingID) {
		return YouTubeRelayBindingClaim{}, ErrInvalidYouTubeRelayBindingClaim
	}
	return claim, nil
}

func (s *MemoryStreamStore) GetStreamYouTubeRelayBindingClaimForStream(ctx context.Context, streamID string) (YouTubeRelayBindingClaim, error) {
	if err := ctx.Err(); err != nil {
		return YouTubeRelayBindingClaim{}, err
	}
	streamID = strings.TrimSpace(streamID)
	if !isCanonicalUUID(streamID) {
		return YouTubeRelayBindingClaim{}, ErrInvalidYouTubeRelayBindingClaim
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, claim := range s.youtubeRelayBindingClaims {
		if claim.StreamID == streamID {
			if !isValidYouTubeRelayBindingID(claim.RelayBindingID) {
				return YouTubeRelayBindingClaim{}, ErrInvalidYouTubeRelayBindingClaim
			}
			return claim, nil
		}
	}
	return YouTubeRelayBindingClaim{}, ErrNotFound
}

func (s *MemoryStreamStore) HasStreamYouTubeRelayBindingClaimForOutput(ctx context.Context, youtubeOutputID string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	youtubeOutputID = strings.TrimSpace(youtubeOutputID)
	if !isCanonicalUUID(youtubeOutputID) {
		return false, ErrInvalidYouTubeRelayBindingClaim
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, claim := range s.youtubeRelayBindingClaims {
		if claim.YouTubeOutputID == youtubeOutputID {
			return true, nil
		}
	}
	return false, nil
}

func (s *MemoryStreamStore) HasStreamYouTubeRelayBindingClaim(ctx context.Context, relayBindingID string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if !isValidYouTubeRelayBindingID(relayBindingID) {
		return false, ErrInvalidYouTubeRelayBindingClaim
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.youtubeRelayBindingClaims[relayBindingID]
	return ok, nil
}

const youtubeRelayBindingClaimSelect = `SELECT relay_binding_id, reservation_token, stream_id, youtube_output_id, youtube_output_revision, oauth_account_id,
  reusable_live_stream_id, COALESCE(broadcast_id, ''), state, prepare_state, dispatch_state, encoder_stop_confirmed_at, last_error, created_at, updated_at
FROM stream_youtube_relay_binding_claims`

type youtubeRelayBindingClaimScanner interface {
	Scan(dest ...any) error
}

func getYouTubeRelayBindingClaimTx(ctx context.Context, tx *sql.Tx, relayBindingID string) (YouTubeRelayBindingClaim, error) {
	claim, err := scanYouTubeRelayBindingClaim(tx.QueryRowContext(ctx, youtubeRelayBindingClaimSelect+` WHERE relay_binding_id = ? FOR UPDATE`, relayBindingID))
	if errors.Is(err, sql.ErrNoRows) {
		return YouTubeRelayBindingClaim{}, ErrNotFound
	}
	return claim, err
}

func hasStreamYouTubeRelayBindingClaimForStreamTx(ctx context.Context, tx *sql.Tx, streamID string) (bool, error) {
	var exists bool
	err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM stream_youtube_relay_binding_claims WHERE stream_id = ?)`, streamID).Scan(&exists)
	return exists, err
}

func scanYouTubeRelayBindingClaim(scanner youtubeRelayBindingClaimScanner) (YouTubeRelayBindingClaim, error) {
	var claim YouTubeRelayBindingClaim
	var encoderStopConfirmedAt sql.NullTime
	err := scanner.Scan(&claim.RelayBindingID, &claim.ReservationToken, &claim.StreamID, &claim.YouTubeOutputID, &claim.YouTubeOutputRevision, &claim.OAuthAccountID,
		&claim.ReusableLiveStreamID, &claim.BroadcastID, &claim.State, &claim.PrepareState, &claim.DispatchState, &encoderStopConfirmedAt, &claim.LastError, &claim.CreatedAt, &claim.UpdatedAt)
	if err != nil {
		return YouTubeRelayBindingClaim{}, err
	}
	if !isValidYouTubeRelayBindingID(claim.RelayBindingID) {
		return YouTubeRelayBindingClaim{}, ErrInvalidYouTubeRelayBindingClaim
	}
	if encoderStopConfirmedAt.Valid {
		claim.EncoderStopConfirmedAt = encoderStopConfirmedAt.Time.UTC()
	}
	return claim, nil
}

func getStreamYouTubeRuntimeTx(ctx context.Context, tx *sql.Tx, streamID string) (StreamYouTubeRuntime, error) {
	var runtime StreamYouTubeRuntime
	err := scanStreamYouTubeRuntime(tx.QueryRowContext(ctx, `SELECT stream_id, youtube_output, oauth_account_id, mode, broadcast_id, live_stream_id, rtmp_url, stream_key_secret_name, dry_run, complete_on_stop, complete_retry_count, complete_next_retry_at, complete_last_error, created_at, updated_at FROM stream_youtube_runtimes WHERE stream_id = ? FOR UPDATE`, streamID), &runtime)
	if errors.Is(err, sql.ErrNoRows) {
		return StreamYouTubeRuntime{}, ErrNotFound
	}
	return runtime, err
}

func saveStreamYouTubeRuntimeTx(ctx context.Context, tx *sql.Tx, runtime StreamYouTubeRuntime) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO stream_youtube_runtimes (stream_id, youtube_output, oauth_account_id, mode, broadcast_id, live_stream_id, rtmp_url, stream_key_secret_name, dry_run, complete_on_stop, complete_retry_count, complete_next_retry_at, complete_last_error, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON DUPLICATE KEY UPDATE youtube_output = VALUES(youtube_output), oauth_account_id = VALUES(oauth_account_id), mode = VALUES(mode), broadcast_id = VALUES(broadcast_id), live_stream_id = VALUES(live_stream_id), rtmp_url = VALUES(rtmp_url), stream_key_secret_name = VALUES(stream_key_secret_name), dry_run = VALUES(dry_run), complete_on_stop = VALUES(complete_on_stop), complete_retry_count = VALUES(complete_retry_count), complete_next_retry_at = VALUES(complete_next_retry_at), complete_last_error = VALUES(complete_last_error), updated_at = VALUES(updated_at)`,
		runtime.StreamID, runtime.YouTubeOutput, runtime.OAuthAccountID, runtime.Mode, runtime.BroadcastID, runtime.LiveStreamID, runtime.RTMPURL, runtime.StreamKeySecretName, runtime.DryRun, runtime.CompleteOnStop, runtime.CompleteRetryCount, nullTime(runtime.CompleteNextRetryAt), streamYouTubeRuntimeCompleteLastError(runtime.CompleteLastError), runtime.CreatedAt, runtime.UpdatedAt)
	return err
}

func normalizeYouTubeRelayBindingClaim(claim YouTubeRelayBindingClaim) YouTubeRelayBindingClaim {
	claim.ReservationToken = strings.TrimSpace(claim.ReservationToken)
	claim.StreamID = strings.TrimSpace(claim.StreamID)
	claim.YouTubeOutputID = strings.TrimSpace(claim.YouTubeOutputID)
	claim.OAuthAccountID = strings.TrimSpace(claim.OAuthAccountID)
	claim.ReusableLiveStreamID = strings.TrimSpace(claim.ReusableLiveStreamID)
	claim.BroadcastID = strings.TrimSpace(claim.BroadcastID)
	claim.State = strings.TrimSpace(claim.State)
	claim.PrepareState = strings.TrimSpace(claim.PrepareState)
	claim.DispatchState = strings.TrimSpace(claim.DispatchState)
	claim.LastError = strings.TrimSpace(claim.LastError)
	if !claim.EncoderStopConfirmedAt.IsZero() {
		claim.EncoderStopConfirmedAt = claim.EncoderStopConfirmedAt.UTC()
	}
	if !claim.CreatedAt.IsZero() {
		claim.CreatedAt = claim.CreatedAt.UTC()
	}
	if !claim.UpdatedAt.IsZero() {
		claim.UpdatedAt = claim.UpdatedAt.UTC()
	}
	return claim
}

func nowYouTubeRelayBindingClaim() time.Time {
	// MariaDB DATETIME(6) preserves microseconds, not Go's nanoseconds. The
	// returned CreatedAt is stored alongside the opaque reservation token, so
	// normalize it before both persistence and return.
	return time.Now().UTC().Truncate(time.Microsecond)
}

func normalizeRelayStaticRuntime(runtime StreamYouTubeRuntime) StreamYouTubeRuntime {
	runtime.StreamID = strings.TrimSpace(runtime.StreamID)
	runtime.YouTubeOutput = strings.TrimSpace(runtime.YouTubeOutput)
	runtime.OAuthAccountID = strings.TrimSpace(runtime.OAuthAccountID)
	runtime.Mode = strings.TrimSpace(runtime.Mode)
	runtime.BroadcastID = strings.TrimSpace(runtime.BroadcastID)
	runtime.LiveStreamID = strings.TrimSpace(runtime.LiveStreamID)
	runtime.RTMPURL = strings.TrimSpace(runtime.RTMPURL)
	runtime.StreamKeySecretName = strings.TrimSpace(runtime.StreamKeySecretName)
	return runtime
}

func validateYouTubeRelayBindingClaimReservation(claim YouTubeRelayBindingClaim) error {
	if !validYouTubeRelayBindingClaimIdentity(claim) || claim.ExpectedYouTubeOutputRevision == nil || claim.YouTubeOutputRevision != 0 || claim.ReservationToken != "" || claim.BroadcastID != "" || claim.State != "" || claim.PrepareState != "" || claim.DispatchState != "" || !claim.EncoderStopConfirmedAt.IsZero() || claim.LastError != "" {
		return ErrInvalidYouTubeRelayBindingClaim
	}
	return nil
}

func validateYouTubeRelayBindingClaimReservationFence(claim YouTubeRelayBindingClaim) error {
	if !validYouTubeRelayBindingClaimIdentity(claim) || !isCanonicalUUID(claim.ReservationToken) || claim.CreatedAt.IsZero() {
		return ErrInvalidYouTubeRelayBindingClaim
	}
	return nil
}

// validateYouTubeRelayBindingClaimPrepareFence accepts only the immutable
// reservation identity. State and handoff fields are read from durable storage
// so a caller cannot forge a Prepare boundary.
func validateYouTubeRelayBindingClaimPrepareFence(claim YouTubeRelayBindingClaim) error {
	if err := validateYouTubeRelayBindingClaimReservationFence(claim); err != nil || claim.BroadcastID != "" || claim.LastError != "" {
		return ErrInvalidYouTubeRelayBindingClaim
	}
	return nil
}

func validateYouTubeRelayBindingClaimFinalize(claim YouTubeRelayBindingClaim, runtime StreamYouTubeRuntime) error {
	if err := validateYouTubeRelayBindingClaimReservationFence(claim); err != nil || !isValidYouTubeRelayBindingExternalID(claim.BroadcastID, youtubeRelayBindingClaimBroadcastIDMax) || claim.LastError != "" {
		return ErrInvalidYouTubeRelayBindingClaim
	}
	if !runtimeMatchesYouTubeRelayBindingClaim(runtime, claim) {
		return ErrInvalidYouTubeRelayBindingClaim
	}
	return nil
}

func validateYouTubeRelayBindingClaimRecovery(claim YouTubeRelayBindingClaim) error {
	if err := validateYouTubeRelayBindingClaimReservationFence(claim); err != nil || !isValidYouTubeRelayBindingExternalID(claim.BroadcastID, youtubeRelayBindingClaimBroadcastIDMax) || !isSafeYouTubeRelayBindingErrorCode(claim.LastError) {
		return ErrInvalidYouTubeRelayBindingClaim
	}
	return nil
}

func validateYouTubeRelayBindingClaimPreparedFence(claim YouTubeRelayBindingClaim) error {
	if err := validateYouTubeRelayBindingClaimReservationFence(claim); err != nil || !isValidYouTubeRelayBindingExternalID(claim.BroadcastID, youtubeRelayBindingClaimBroadcastIDMax) {
		return ErrInvalidYouTubeRelayBindingClaim
	}
	return nil
}

func validYouTubeRelayBindingClaimIdentity(claim YouTubeRelayBindingClaim) bool {
	return isValidYouTubeRelayBindingID(claim.RelayBindingID) &&
		isCanonicalUUID(claim.StreamID) &&
		isCanonicalUUID(claim.YouTubeOutputID) &&
		isCanonicalUUID(claim.OAuthAccountID) &&
		isValidYouTubeRelayBindingExternalID(claim.ReusableLiveStreamID, youtubeRelayBindingClaimReusableLiveStreamIDMax)
}

func isValidYouTubeRelayBindingID(value string) bool {
	const prefix = "relay-"
	if len(value) != len(prefix)+36 || !strings.HasPrefix(value, prefix) {
		return false
	}
	for index := len(prefix); index < len(value); index++ {
		char := value[index]
		switch index - len(prefix) {
		case 8, 13, 18, 23:
			if char != '-' {
				return false
			}
		default:
			if !((char >= '0' && char <= '9') || (char >= 'a' && char <= 'f')) {
				return false
			}
		}
	}
	return true
}

func isValidYouTubeRelayBindingExternalID(value string, max int) bool {
	if len(value) == 0 || len(value) > max {
		return false
	}
	for index := 0; index < len(value); index++ {
		char := value[index]
		if !((char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') || (char >= '0' && char <= '9') || char == '-' || char == '_') {
			return false
		}
	}
	return true
}

func isSafeYouTubeRelayBindingErrorCode(value string) bool {
	if len(value) == 0 || len(value) > youtubeRelayBindingClaimLastErrorMax {
		return false
	}
	for index := 0; index < len(value); index++ {
		char := value[index]
		if !((char >= 'a' && char <= 'z') || (char >= '0' && char <= '9') || char == '-' || char == '_' || char == '.') {
			return false
		}
	}
	return true
}

func isActiveYouTubeRelayBindingClaimStreamStatus(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "starting", "live", "stopping":
		return true
	default:
		return false
	}
}

func isYouTubeRelayBindingClaimDispatchFenceState(state string) bool {
	switch state {
	case YouTubeRelayBindingClaimDispatchStateNotDispatched, YouTubeRelayBindingClaimDispatchStatePossiblyDispatched:
		return true
	default:
		return false
	}
}

// isYouTubeRelayBindingClaimEncoderStopReceiptStreamStatus prevents an
// arbitrary active start from forging a shutdown receipt. The normal stop
// path records it while stopping; force-stop and a partial stop failure record
// it after the stream became failed. Completed supports an idempotent retry
// after the final status write but before provider completion.
func isYouTubeRelayBindingClaimEncoderStopReceiptStreamStatus(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "stopping", "failed", "completed":
		return true
	default:
		return false
	}
}

// isYouTubeRelayBindingClaimProfileConstraintError maps only the named relay
// binding foreign keys to the public, secret-safe active-claim condition.
func isYouTubeRelayBindingClaimProfileConstraintError(err error) bool {
	var mysqlErr *mysql.MySQLError
	if !errors.As(err, &mysqlErr) || mysqlErr.Number != 1451 {
		return false
	}
	message := strings.ToLower(mysqlErr.Message)
	return strings.Contains(message, "fk_yt_relay_claim_youtube_output") ||
		strings.Contains(message, "fk_yt_relay_claim_youtube_output_revision")
}

func isCanonicalUUID(value string) bool {
	if len(value) != 36 {
		return false
	}
	for index := 0; index < len(value); index++ {
		char := value[index]
		if index == 8 || index == 13 || index == 18 || index == 23 {
			if char != '-' {
				return false
			}
			continue
		}
		if !((char >= 'a' && char <= 'f') || (char >= 'A' && char <= 'F') || (char >= '0' && char <= '9')) {
			return false
		}
	}
	return true
}

func sameYouTubeRelayBindingReservation(left, right YouTubeRelayBindingClaim) bool {
	return left.RelayBindingID == right.RelayBindingID &&
		left.ReservationToken == right.ReservationToken &&
		left.StreamID == right.StreamID &&
		left.YouTubeOutputID == right.YouTubeOutputID &&
		left.YouTubeOutputRevision == right.YouTubeOutputRevision &&
		left.OAuthAccountID == right.OAuthAccountID &&
		left.ReusableLiveStreamID == right.ReusableLiveStreamID &&
		left.CreatedAt.Equal(right.CreatedAt)
}

func memoryYouTubeRelayBindingClaimConflict(claims map[string]YouTubeRelayBindingClaim, claim YouTubeRelayBindingClaim) bool {
	for _, existing := range claims {
		if existing.RelayBindingID == claim.RelayBindingID || existing.StreamID == claim.StreamID ||
			(existing.OAuthAccountID == claim.OAuthAccountID && existing.ReusableLiveStreamID == claim.ReusableLiveStreamID) {
			return true
		}
	}
	return false
}

func runtimeMatchesYouTubeRelayBindingClaim(runtime StreamYouTubeRuntime, claim YouTubeRelayBindingClaim) bool {
	return strings.TrimSpace(runtime.StreamID) == claim.StreamID &&
		strings.TrimSpace(runtime.YouTubeOutput) == claim.YouTubeOutputID &&
		strings.TrimSpace(runtime.OAuthAccountID) == claim.OAuthAccountID &&
		strings.TrimSpace(runtime.Mode) == youtubeRelayBindingClaimStaticRuntimeMode &&
		strings.TrimSpace(runtime.BroadcastID) == claim.BroadcastID &&
		strings.TrimSpace(runtime.LiveStreamID) == claim.ReusableLiveStreamID &&
		strings.TrimSpace(runtime.RTMPURL) == "" &&
		strings.TrimSpace(runtime.StreamKeySecretName) == "" &&
		!runtime.DryRun && runtime.CompleteOnStop
}

func sameRelayStaticRuntime(left, right StreamYouTubeRuntime) bool {
	return strings.TrimSpace(left.StreamID) == strings.TrimSpace(right.StreamID) &&
		strings.TrimSpace(left.YouTubeOutput) == strings.TrimSpace(right.YouTubeOutput) &&
		strings.TrimSpace(left.OAuthAccountID) == strings.TrimSpace(right.OAuthAccountID) &&
		strings.TrimSpace(left.Mode) == strings.TrimSpace(right.Mode) &&
		strings.TrimSpace(left.BroadcastID) == strings.TrimSpace(right.BroadcastID) &&
		strings.TrimSpace(left.LiveStreamID) == strings.TrimSpace(right.LiveStreamID) &&
		strings.TrimSpace(left.RTMPURL) == strings.TrimSpace(right.RTMPURL) &&
		strings.TrimSpace(left.StreamKeySecretName) == strings.TrimSpace(right.StreamKeySecretName) &&
		left.DryRun == right.DryRun && left.CompleteOnStop == right.CompleteOnStop
}

var _ StreamYouTubeRelayBindingClaimStore = (*MemoryStreamStore)(nil)
var _ StreamYouTubeRelayBindingClaimStore = (*MariaDBStreamStore)(nil)
