package store

import (
	"errors"
	"testing"
	"time"

	"github.com/go-sql-driver/mysql"
)

func TestMemoryStreamYouTubeRelayBindingClaimLifecycle(t *testing.T) {
	streams := NewMemoryStreamStore()
	stream, err := streams.CreateStream(t.Context(), "relay static lifecycle")
	if err != nil {
		t.Fatalf("create stream: %v", err)
	}
	claim := testMemoryYouTubeRelayBindingClaim(t, streams, stream.ID)

	if _, err := streams.ReserveStreamYouTubeRelayBindingClaim(t.Context(), claim); !errors.Is(err, ErrYouTubeRelayBindingClaimState) {
		t.Fatalf("reserve outside starting state error = %v, want state error", err)
	}
	if _, err := streams.UpdateStreamStatus(t.Context(), stream.ID, "starting"); err != nil {
		t.Fatalf("move relay stream to starting: %v", err)
	}
	reserved, err := streams.ReserveStreamYouTubeRelayBindingClaim(t.Context(), claim)
	if err != nil {
		t.Fatalf("reserve claim: %v", err)
	}
	if reserved.State != YouTubeRelayBindingClaimStateReserved || reserved.DispatchState != YouTubeRelayBindingClaimDispatchStateNotDispatched || reserved.CreatedAt.IsZero() || reserved.UpdatedAt.IsZero() {
		t.Fatalf("reserved claim = %#v", reserved)
	}
	if has, err := streams.HasStreamYouTubeRelayBindingClaimForOutput(t.Context(), claim.YouTubeOutputID); err != nil || !has {
		t.Fatalf("has active output claim = %v, %v; want true, nil", has, err)
	}
	if has, err := streams.HasStreamYouTubeRelayBindingClaim(t.Context(), claim.RelayBindingID); err != nil || !has {
		t.Fatalf("has active binding claim = %v, %v; want true, nil", has, err)
	}
	if got, err := streams.GetStreamYouTubeRelayBindingClaim(t.Context(), claim.RelayBindingID); err != nil || got.StreamID != stream.ID {
		t.Fatalf("get reserved claim = %#v, %v", got, err)
	}

	second, err := streams.CreateStream(t.Context(), "relay static conflict")
	if err != nil {
		t.Fatalf("create second stream: %v", err)
	}
	if _, err := streams.UpdateStreamSettings(t.Context(), second.ID, StreamSettings{YouTubeOutputID: claim.YouTubeOutputID}); err != nil {
		t.Fatalf("set duplicate relay static output: %v", err)
	}
	if _, err := streams.UpdateStreamStatus(t.Context(), second.ID, "starting"); err != nil {
		t.Fatalf("move duplicate relay stream to starting: %v", err)
	}
	conflictingLiveStream := testYouTubeRelayBindingClaim(second.ID)
	conflictingLiveStream.RelayBindingID = "relay-00000000-0000-4000-8000-000000000102"
	conflictingLiveStream.YouTubeOutputID = claim.YouTubeOutputID
	setExpectedYouTubeRelayBindingProfileRevision(&conflictingLiveStream, 0)
	conflictingLiveStream.OAuthAccountID = claim.OAuthAccountID
	if _, err := streams.ReserveStreamYouTubeRelayBindingClaim(t.Context(), conflictingLiveStream); !errors.Is(err, ErrYouTubeRelayBindingClaimConflict) {
		t.Fatalf("reserve duplicate reusable stream error = %v, want conflict", err)
	}
	conflictingStream := claim
	conflictingStream.RelayBindingID = "relay-00000000-0000-4000-8000-000000000103"
	conflictingStream.ReusableLiveStreamID = "youtube_reusable_live_stream_002"
	if _, err := streams.ReserveStreamYouTubeRelayBindingClaim(t.Context(), conflictingStream); !errors.Is(err, ErrYouTubeRelayBindingClaimConflict) {
		t.Fatalf("reserve duplicate stream error = %v, want conflict", err)
	}

	runtime := StreamYouTubeRuntime{
		StreamID:       stream.ID,
		YouTubeOutput:  claim.YouTubeOutputID,
		OAuthAccountID: claim.OAuthAccountID,
		Mode:           "live_api_relay_static",
		BroadcastID:    "broadcast-static-001",
		LiveStreamID:   claim.ReusableLiveStreamID,
		CompleteOnStop: true,
	}
	possiblyPrepared, err := streams.MarkReservedStreamYouTubeRelayBindingClaimPossiblyPrepared(t.Context(), reserved)
	if err != nil {
		t.Fatalf("mark claim possibly prepared: %v", err)
	}
	if possiblyPrepared.PrepareState != YouTubeRelayBindingClaimPrepareStatePossiblyPrepared || possiblyPrepared.DispatchState != YouTubeRelayBindingClaimDispatchStateNotDispatched {
		t.Fatalf("possibly prepared claim = %#v", possiblyPrepared)
	}
	preparedClaim := possiblyPrepared
	preparedClaim.BroadcastID = runtime.BroadcastID
	invalidRetryRuntime := runtime
	invalidRetryRuntime.CompleteRetryCount = 1
	if err := streams.FinalizeStreamYouTubeRuntimeAndMarkRelayBindingPrepared(t.Context(), preparedClaim, invalidRetryRuntime); !errors.Is(err, ErrInvalidYouTubeRelayBindingClaim) {
		t.Fatalf("finalize with pending completion retry error = %v, want invalid claim", err)
	}
	if err := streams.FinalizeStreamYouTubeRuntimeAndMarkRelayBindingPrepared(t.Context(), preparedClaim, runtime); err != nil {
		t.Fatalf("finalize runtime and claim: %v", err)
	}
	prepared, err := streams.GetStreamYouTubeRelayBindingClaimForStream(t.Context(), stream.ID)
	if err != nil {
		t.Fatalf("get prepared claim: %v", err)
	}
	if prepared.State != YouTubeRelayBindingClaimStatePrepared || prepared.PrepareState != YouTubeRelayBindingClaimPrepareStatePossiblyPrepared || prepared.DispatchState != YouTubeRelayBindingClaimDispatchStateNotDispatched || prepared.BroadcastID != runtime.BroadcastID {
		t.Fatalf("prepared claim = %#v", prepared)
	}
	if got, err := streams.GetStreamYouTubeRuntime(t.Context(), stream.ID); err != nil || got.BroadcastID != runtime.BroadcastID || got.Mode != "live_api_relay_static" {
		t.Fatalf("stored runtime = %#v, %v", got, err)
	}
	if err := streams.ReleaseReservedStreamYouTubeRelayBindingClaim(t.Context(), prepared); !errors.Is(err, ErrYouTubeRelayBindingClaimState) {
		t.Fatalf("release prepared claim error = %v, want state error", err)
	}
	if err := streams.DeleteStreamYouTubeRuntime(t.Context(), stream.ID); !errors.Is(err, ErrYouTubeRelayBindingClaimActive) {
		t.Fatalf("generic runtime delete error = %v, want active claim error", err)
	}
	if err := streams.SaveStreamYouTubeRuntime(t.Context(), StreamYouTubeRuntime{StreamID: stream.ID, YouTubeOutput: claim.YouTubeOutputID, OAuthAccountID: claim.OAuthAccountID, Mode: "live_api"}); !errors.Is(err, ErrYouTubeRelayBindingClaimActive) {
		t.Fatalf("generic runtime overwrite error = %v, want active claim error", err)
	}
	staleDispatchFence := prepared
	staleDispatchFence.ReservationToken = newUUID()
	if _, err := streams.MarkPreparedStreamYouTubeRelayBindingClaimPossiblyDispatched(t.Context(), staleDispatchFence); !errors.Is(err, ErrYouTubeRelayBindingClaimConflict) {
		t.Fatalf("mark stale prepared claim possibly dispatched error = %v, want conflict", err)
	}
	possiblyDispatched, err := streams.MarkPreparedStreamYouTubeRelayBindingClaimPossiblyDispatched(t.Context(), prepared)
	if err != nil {
		t.Fatalf("mark prepared claim possibly dispatched: %v", err)
	}
	if possiblyDispatched.State != YouTubeRelayBindingClaimStatePrepared || possiblyDispatched.DispatchState != YouTubeRelayBindingClaimDispatchStatePossiblyDispatched {
		t.Fatalf("possibly dispatched claim = %#v", possiblyDispatched)
	}
	if again, err := streams.MarkPreparedStreamYouTubeRelayBindingClaimPossiblyDispatched(t.Context(), prepared); err != nil || again.DispatchState != YouTubeRelayBindingClaimDispatchStatePossiblyDispatched {
		t.Fatalf("idempotent possibly-dispatched marker = %#v, %v", again, err)
	}
	if _, err := streams.RecordStreamYouTubeRuntimeCompleteFailure(t.Context(), stream.ID, "youtube_live_api_complete_failed", time.Now().UTC().Add(-time.Second)); err != nil {
		t.Fatalf("record static runtime completion failure: %v", err)
	}
	due, err := streams.ListDueStreamYouTubeRuntimes(t.Context(), time.Now().UTC(), 10)
	if err != nil {
		t.Fatalf("list due static runtimes: %v", err)
	}
	if len(due) != 1 || due[0].StreamID != stream.ID || due[0].Mode != youtubeRelayBindingClaimStaticRuntimeMode {
		t.Fatalf("due static runtimes = %#v, want %q", due, stream.ID)
	}
	stalePrepared := possiblyDispatched
	stalePrepared.ReservationToken = newUUID()
	if err := streams.CompleteStreamYouTubeRuntimeAndReleaseRelayBindingClaim(t.Context(), stalePrepared); !errors.Is(err, ErrYouTubeRelayBindingClaimConflict) {
		t.Fatalf("complete stale prepared claim error = %v, want conflict", err)
	}
	if err := streams.CompleteStreamYouTubeRuntimeAndReleaseRelayBindingClaim(t.Context(), possiblyDispatched); !errors.Is(err, ErrYouTubeRelayBindingClaimState) {
		t.Fatalf("complete active static runtime error = %v, want state error", err)
	}
	if _, err := streams.UpdateStreamStatus(t.Context(), stream.ID, "failed"); err != nil {
		t.Fatalf("move static stream to failed: %v", err)
	}
	if err := streams.CompleteStreamYouTubeRuntimeAndReleaseRelayBindingClaim(t.Context(), possiblyDispatched); !errors.Is(err, ErrYouTubeRelayBindingClaimState) {
		t.Fatalf("complete unconfirmed failed static runtime error = %v, want state error", err)
	}
	if _, err := streams.UpdateStreamStatus(t.Context(), stream.ID, "completed"); err != nil {
		t.Fatalf("move static stream to completed: %v", err)
	}

	if err := streams.CompleteStreamYouTubeRuntimeAndReleaseRelayBindingClaim(t.Context(), possiblyDispatched); err != nil {
		t.Fatalf("complete runtime and release claim: %v", err)
	}
	if _, err := streams.GetStreamYouTubeRuntime(t.Context(), stream.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("runtime after successful complete error = %v, want not found", err)
	}
	if _, err := streams.GetStreamYouTubeRelayBindingClaim(t.Context(), claim.RelayBindingID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("claim after successful complete error = %v, want not found", err)
	}
	if has, err := streams.HasStreamYouTubeRelayBindingClaimForOutput(t.Context(), claim.YouTubeOutputID); err != nil || has {
		t.Fatalf("has output claim after complete = %v, %v; want false, nil", has, err)
	}
}

func TestMemoryStreamYouTubeRelayBindingClaimPrepareFenceReconcile(t *testing.T) {
	t.Run("not attempted releases only after stream becomes inactive", func(t *testing.T) {
		streams := NewMemoryStreamStore()
		stream, err := streams.CreateStream(t.Context(), "relay static prepare reconcile release")
		if err != nil {
			t.Fatal(err)
		}
		claim := testMemoryYouTubeRelayBindingClaim(t, streams, stream.ID)
		if _, err := streams.UpdateStreamStatus(t.Context(), stream.ID, "starting"); err != nil {
			t.Fatal(err)
		}
		reserved, err := streams.ReserveStreamYouTubeRelayBindingClaim(t.Context(), claim)
		if err != nil {
			t.Fatal(err)
		}
		reconcile := reserved
		reconcile.BroadcastID = "unknown_external_broadcast"
		reconcile.LastError = "youtube_relay_static_prepare_marker_unconfirmed"
		if _, err := streams.ReconcileReservedStreamYouTubeRelayBindingClaimAfterPrepareFence(t.Context(), reconcile); !errors.Is(err, ErrYouTubeRelayBindingClaimState) {
			t.Fatalf("active reserved reconcile error = %v, want state", err)
		}
		if _, err := streams.UpdateStreamStatus(t.Context(), stream.ID, "failed"); err != nil {
			t.Fatal(err)
		}
		result, err := streams.ReconcileReservedStreamYouTubeRelayBindingClaimAfterPrepareFence(t.Context(), reconcile)
		if err != nil || !result.Released || result.Claim.RelayBindingID != "" {
			t.Fatalf("not-attempted reconcile = %#v, %v", result, err)
		}
		if _, err := streams.GetStreamYouTubeRelayBindingClaim(t.Context(), claim.RelayBindingID); !errors.Is(err, ErrNotFound) {
			t.Fatalf("released reservation lookup error = %v, want not found", err)
		}
	})

	t.Run("possibly prepared remains fenced and stale token cannot reconcile", func(t *testing.T) {
		streams := NewMemoryStreamStore()
		stream, err := streams.CreateStream(t.Context(), "relay static prepare reconcile recovery")
		if err != nil {
			t.Fatal(err)
		}
		claim := testMemoryYouTubeRelayBindingClaim(t, streams, stream.ID)
		if _, err := streams.UpdateStreamStatus(t.Context(), stream.ID, "starting"); err != nil {
			t.Fatal(err)
		}
		reserved, err := streams.ReserveStreamYouTubeRelayBindingClaim(t.Context(), claim)
		if err != nil {
			t.Fatal(err)
		}
		possiblyPrepared, err := streams.MarkReservedStreamYouTubeRelayBindingClaimPossiblyPrepared(t.Context(), reserved)
		if err != nil {
			t.Fatal(err)
		}
		if err := streams.ReleaseReservedStreamYouTubeRelayBindingClaim(t.Context(), possiblyPrepared); !errors.Is(err, ErrYouTubeRelayBindingClaimState) {
			t.Fatalf("possibly-prepared release error = %v, want state", err)
		}
		if _, err := streams.UpdateStreamStatus(t.Context(), stream.ID, "failed"); err != nil {
			t.Fatal(err)
		}
		reconcile := possiblyPrepared
		reconcile.BroadcastID = "unknown_external_broadcast"
		reconcile.LastError = "youtube_relay_static_prepare_marker_unconfirmed"
		stale := reconcile
		stale.ReservationToken = newUUID()
		if _, err := streams.ReconcileReservedStreamYouTubeRelayBindingClaimAfterPrepareFence(t.Context(), stale); !errors.Is(err, ErrYouTubeRelayBindingClaimConflict) {
			t.Fatalf("stale reconcile error = %v, want conflict", err)
		}
		result, err := streams.ReconcileReservedStreamYouTubeRelayBindingClaimAfterPrepareFence(t.Context(), reconcile)
		if err != nil || result.Released || result.Claim.State != YouTubeRelayBindingClaimStateRecoveryRequired || result.Claim.PrepareState != YouTubeRelayBindingClaimPrepareStatePossiblyPrepared || result.Claim.DispatchState != YouTubeRelayBindingClaimDispatchStateNotDispatched {
			t.Fatalf("possibly-prepared reconcile = %#v, %v", result, err)
		}
		again, err := streams.ReconcileReservedStreamYouTubeRelayBindingClaimAfterPrepareFence(t.Context(), reconcile)
		if err != nil || again.Released || again.Claim.State != YouTubeRelayBindingClaimStateRecoveryRequired {
			t.Fatalf("idempotent recovery reconcile = %#v, %v", again, err)
		}
	})
}

func TestMemoryStreamYouTubeRelayBindingClaimEncoderStopReceiptIsDurable(t *testing.T) {
	t.Run("preserves receipt through abandon", func(t *testing.T) {
		streams, stream, claim := newMemoryPossiblyDispatchedRelayClaim(t)
		if _, err := streams.UpdateStreamStatus(t.Context(), stream.ID, "failed"); err != nil {
			t.Fatal(err)
		}
		confirmed, err := streams.MarkPreparedStreamYouTubeRelayBindingClaimEncoderStopConfirmed(t.Context(), claim)
		if err != nil || confirmed.EncoderStopConfirmedAt.IsZero() {
			t.Fatalf("mark encoder stop receipt = %#v, %v", confirmed, err)
		}
		abandon := confirmed
		abandon.LastError = "youtube_relay_static_stop_dispatch_unconfirmed"
		recovery, err := streams.AbandonPreparedStreamYouTubeRuntimeAndMarkRelayBindingRecoveryRequired(t.Context(), abandon)
		if err != nil {
			t.Fatal(err)
		}
		if !recovery.EncoderStopConfirmedAt.Equal(confirmed.EncoderStopConfirmedAt) {
			t.Fatalf("abandon lost encoder receipt: got=%s want=%s", recovery.EncoderStopConfirmedAt, confirmed.EncoderStopConfirmedAt)
		}
	})

	t.Run("records delayed recovery receipt with store time", func(t *testing.T) {
		streams, stream, claim := newMemoryPossiblyDispatchedRelayClaim(t)
		if _, err := streams.UpdateStreamStatus(t.Context(), stream.ID, "failed"); err != nil {
			t.Fatal(err)
		}
		abandon := claim
		abandon.LastError = "youtube_relay_static_stop_dispatch_unconfirmed"
		recovery, err := streams.AbandonPreparedStreamYouTubeRuntimeAndMarkRelayBindingRecoveryRequired(t.Context(), abandon)
		if err != nil {
			t.Fatal(err)
		}
		forged := recovery
		forged.EncoderStopConfirmedAt = time.Date(2099, time.January, 1, 0, 0, 0, 0, time.UTC)
		confirmed, err := streams.MarkPreparedStreamYouTubeRelayBindingClaimEncoderStopConfirmed(t.Context(), forged)
		if err != nil || confirmed.EncoderStopConfirmedAt.IsZero() || confirmed.EncoderStopConfirmedAt.Equal(forged.EncoderStopConfirmedAt) {
			t.Fatalf("delayed recovery receipt = %#v, %v", confirmed, err)
		}
		stale := recovery
		stale.ReservationToken = newUUID()
		if _, err := streams.MarkPreparedStreamYouTubeRelayBindingClaimEncoderStopConfirmed(t.Context(), stale); !errors.Is(err, ErrYouTubeRelayBindingClaimConflict) {
			t.Fatalf("stale receipt marker error = %v, want conflict", err)
		}
		persisted, err := streams.GetStreamYouTubeRelayBindingClaim(t.Context(), recovery.RelayBindingID)
		if err != nil || !persisted.EncoderStopConfirmedAt.Equal(confirmed.EncoderStopConfirmedAt) {
			t.Fatalf("persisted delayed receipt = %#v, %v", persisted, err)
		}
	})
}

func TestMemoryStreamYouTubeRelayBindingClaimAbandonPreparedRuntimeIsAtomic(t *testing.T) {
	streams := NewMemoryStreamStore()
	stream, err := streams.CreateStream(t.Context(), "relay static abandon prepared")
	if err != nil {
		t.Fatalf("create stream: %v", err)
	}
	if _, err := streams.UpdateStreamStatus(t.Context(), stream.ID, "starting"); err != nil {
		t.Fatalf("move relay stream to starting: %v", err)
	}
	claim := testMemoryYouTubeRelayBindingClaim(t, streams, stream.ID)
	reserved, err := streams.ReserveStreamYouTubeRelayBindingClaim(t.Context(), claim)
	if err != nil {
		t.Fatalf("reserve claim: %v", err)
	}
	runtime := StreamYouTubeRuntime{
		StreamID:       stream.ID,
		YouTubeOutput:  claim.YouTubeOutputID,
		OAuthAccountID: claim.OAuthAccountID,
		Mode:           youtubeRelayBindingClaimStaticRuntimeMode,
		BroadcastID:    "broadcast-static-abandon",
		LiveStreamID:   claim.ReusableLiveStreamID,
		CompleteOnStop: true,
	}
	possiblyPrepared, err := streams.MarkReservedStreamYouTubeRelayBindingClaimPossiblyPrepared(t.Context(), reserved)
	if err != nil {
		t.Fatalf("mark abandon claim possibly prepared: %v", err)
	}
	prepared := possiblyPrepared
	prepared.BroadcastID = runtime.BroadcastID
	if err := streams.FinalizeStreamYouTubeRuntimeAndMarkRelayBindingPrepared(t.Context(), prepared, runtime); err != nil {
		t.Fatalf("finalize runtime and claim: %v", err)
	}
	prepared, err = streams.GetStreamYouTubeRelayBindingClaim(t.Context(), claim.RelayBindingID)
	if err != nil {
		t.Fatalf("get prepared claim: %v", err)
	}
	abandon := prepared
	abandon.LastError = "youtube_relay_static_encoder_dispatch_failed"
	if _, err := streams.UpdateStreamStatus(t.Context(), stream.ID, "failed"); err != nil {
		t.Fatalf("move not-dispatched stream to failed: %v", err)
	}
	if _, err := streams.AbandonPreparedStreamYouTubeRuntimeAndMarkRelayBindingRecoveryRequired(t.Context(), abandon); !errors.Is(err, ErrYouTubeRelayBindingClaimState) {
		t.Fatalf("abandon not-dispatched prepared runtime error = %v, want state error", err)
	}
	if _, err := streams.UpdateStreamStatus(t.Context(), stream.ID, "starting"); err != nil {
		t.Fatalf("return abandon stream to starting: %v", err)
	}
	possiblyDispatched, err := streams.MarkPreparedStreamYouTubeRelayBindingClaimPossiblyDispatched(t.Context(), prepared)
	if err != nil {
		t.Fatalf("mark abandon claim possibly dispatched: %v", err)
	}
	abandon = possiblyDispatched
	abandon.LastError = "youtube_relay_static_encoder_dispatch_failed"
	if _, err := streams.RecordStreamYouTubeRuntimeCompleteFailure(t.Context(), stream.ID, "youtube_live_api_complete_failed", time.Now().UTC().Add(-time.Second)); err != nil {
		t.Fatalf("queue completion retry before abandon: %v", err)
	}
	if _, err := streams.AbandonPreparedStreamYouTubeRuntimeAndMarkRelayBindingRecoveryRequired(t.Context(), abandon); !errors.Is(err, ErrYouTubeRelayBindingClaimState) {
		t.Fatalf("abandon active stream error = %v, want state error", err)
	}
	if _, err := streams.UpdateStreamStatus(t.Context(), stream.ID, "failed"); err != nil {
		t.Fatalf("move abandoned stream to failed: %v", err)
	}
	recovery, err := streams.AbandonPreparedStreamYouTubeRuntimeAndMarkRelayBindingRecoveryRequired(t.Context(), abandon)
	if err != nil {
		t.Fatalf("abandon prepared static runtime: %v", err)
	}
	if recovery.State != YouTubeRelayBindingClaimStateRecoveryRequired || recovery.DispatchState != YouTubeRelayBindingClaimDispatchStatePossiblyDispatched || recovery.LastError != abandon.LastError {
		t.Fatalf("abandoned recovery claim = %#v", recovery)
	}
	if _, err := streams.GetStreamYouTubeRuntime(t.Context(), stream.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("runtime after abandon error = %v, want not found", err)
	}
	due, err := streams.ListDueStreamYouTubeRuntimes(t.Context(), time.Now().UTC(), 10)
	if err != nil {
		t.Fatalf("list due runtimes after abandon: %v", err)
	}
	for _, runtime := range due {
		if runtime.StreamID == stream.ID {
			t.Fatalf("abandoned static runtime remained due: %#v", runtime)
		}
	}
	if got, err := streams.AbandonPreparedStreamYouTubeRuntimeAndMarkRelayBindingRecoveryRequired(t.Context(), abandon); err != nil || got.State != YouTubeRelayBindingClaimStateRecoveryRequired {
		t.Fatalf("idempotent abandon = %#v, %v", got, err)
	}
}

func TestMemoryStreamYouTubeRelayBindingClaimDispatchFenceReconcileIsAtomic(t *testing.T) {
	t.Run("not dispatched marker uncertainty retains recovery", func(t *testing.T) {
		streams, stream, prepared := newMemoryPreparedRelayClaim(t)
		if _, err := streams.UpdateStreamStatus(t.Context(), stream.ID, "failed"); err != nil {
			t.Fatal(err)
		}
		reconcile := prepared
		reconcile.LastError = "youtube_relay_static_dispatch_marker_unconfirmed"
		stale := reconcile
		stale.ReservationToken = newUUID()
		if _, err := streams.ReconcilePreparedStreamYouTubeRelayBindingClaimAfterDispatchFence(t.Context(), stale); !errors.Is(err, ErrYouTubeRelayBindingClaimConflict) {
			t.Fatalf("stale dispatch-fence reconcile error = %v, want conflict", err)
		}
		recovery, err := streams.ReconcilePreparedStreamYouTubeRelayBindingClaimAfterDispatchFence(t.Context(), reconcile)
		if err != nil {
			t.Fatal(err)
		}
		if recovery.State != YouTubeRelayBindingClaimStateRecoveryRequired || recovery.PrepareState != YouTubeRelayBindingClaimPrepareStatePossiblyPrepared || recovery.DispatchState != YouTubeRelayBindingClaimDispatchStateNotDispatched || recovery.BroadcastID != prepared.BroadcastID || recovery.LastError != reconcile.LastError {
			t.Fatalf("not-dispatched recovery = %#v", recovery)
		}
		if _, err := streams.GetStreamYouTubeRuntime(t.Context(), stream.ID); !errors.Is(err, ErrNotFound) {
			t.Fatalf("runtime after dispatch-fence reconcile error = %v, want not found", err)
		}
		if err := streams.ReleaseReservedStreamYouTubeRelayBindingClaim(t.Context(), recovery); !errors.Is(err, ErrYouTubeRelayBindingClaimState) {
			t.Fatalf("dispatch-fence recovery release error = %v, want state error", err)
		}
		if again, err := streams.ReconcilePreparedStreamYouTubeRelayBindingClaimAfterDispatchFence(t.Context(), reconcile); err != nil || again.State != YouTubeRelayBindingClaimStateRecoveryRequired {
			t.Fatalf("idempotent dispatch-fence reconcile = %#v, %v", again, err)
		}
	})

	t.Run("possibly dispatched marker uncertainty is never released", func(t *testing.T) {
		streams, stream, possiblyDispatched := newMemoryPossiblyDispatchedRelayClaim(t)
		if _, err := streams.UpdateStreamStatus(t.Context(), stream.ID, "failed"); err != nil {
			t.Fatal(err)
		}
		reconcile := possiblyDispatched
		reconcile.LastError = "youtube_relay_static_dispatch_marker_unconfirmed"
		recovery, err := streams.ReconcilePreparedStreamYouTubeRelayBindingClaimAfterDispatchFence(t.Context(), reconcile)
		if err != nil {
			t.Fatal(err)
		}
		if recovery.State != YouTubeRelayBindingClaimStateRecoveryRequired || recovery.DispatchState != YouTubeRelayBindingClaimDispatchStatePossiblyDispatched || recovery.BroadcastID != possiblyDispatched.BroadcastID {
			t.Fatalf("possibly-dispatched recovery = %#v", recovery)
		}
		if has, err := streams.HasStreamYouTubeRelayBindingClaim(t.Context(), recovery.RelayBindingID); err != nil || !has {
			t.Fatalf("possibly-dispatched recovery claim existence = %v, %v; want true, nil", has, err)
		}
		if err := streams.ResolveStreamYouTubeRelayBindingRecovery(t.Context(), recovery); !errors.Is(err, ErrYouTubeRelayBindingClaimState) {
			t.Fatalf("resolve possibly-dispatched recovery without encoder receipt error = %v, want state error", err)
		}
		confirmed, err := streams.MarkPreparedStreamYouTubeRelayBindingClaimEncoderStopConfirmed(t.Context(), recovery)
		if err != nil || confirmed.EncoderStopConfirmedAt.IsZero() {
			t.Fatalf("mark recovery encoder receipt = %#v, %v", confirmed, err)
		}
		if err := streams.ResolveStreamYouTubeRelayBindingRecovery(t.Context(), confirmed); err != nil {
			t.Fatalf("resolve possibly-dispatched recovery with encoder receipt: %v", err)
		}
	})
}

func TestMemoryProfileStoreRelayStaticClaimBlocksOnlyYouTubeOutputMutation(t *testing.T) {
	streams := NewMemoryStreamStore()
	profiles := NewMemoryProfileStore()
	profiles.BindStreamYouTubeRelayBindingClaims(streams)
	output, err := profiles.CreateProfile(t.Context(), ProfileYouTubeOutput, "claimed relay output", map[string]any{"mode": youtubeRelayBindingClaimStaticRuntimeMode})
	if err != nil {
		t.Fatalf("create YouTube output profile: %v", err)
	}
	other, err := profiles.CreateProfile(t.Context(), ProfileEncoder, "unclaimed encoder", map[string]any{})
	if err != nil {
		t.Fatalf("create unrelated profile: %v", err)
	}
	stream, err := streams.CreateStream(t.Context(), "claimed output stream")
	if err != nil {
		t.Fatalf("create stream: %v", err)
	}
	claim := testYouTubeRelayBindingClaim(stream.ID)
	claim.YouTubeOutputID = output.ID
	setExpectedYouTubeRelayBindingProfileRevision(&claim, output.YouTubeRelayBindingRevision)
	if _, err := streams.UpdateStreamSettings(t.Context(), stream.ID, StreamSettings{YouTubeOutputID: output.ID}); err != nil {
		t.Fatalf("set claimed output on stream: %v", err)
	}
	if _, err := streams.UpdateStreamStatus(t.Context(), stream.ID, "starting"); err != nil {
		t.Fatalf("move claimed output stream to starting: %v", err)
	}
	reserved, err := streams.ReserveStreamYouTubeRelayBindingClaim(t.Context(), claim)
	if err != nil {
		t.Fatalf("reserve output claim: %v", err)
	}
	if _, err := profiles.UpdateProfile(t.Context(), ProfileYouTubeOutput, output.ID, "claimed relay output changed", map[string]any{"mode": youtubeRelayBindingClaimStaticRuntimeMode}); !errors.Is(err, ErrYouTubeRelayBindingClaimActive) {
		t.Fatalf("update claimed output error = %v, want active claim", err)
	}
	if err := profiles.DeleteProfile(t.Context(), ProfileYouTubeOutput, output.ID); !errors.Is(err, ErrYouTubeRelayBindingClaimActive) {
		t.Fatalf("delete claimed output error = %v, want active claim", err)
	}
	if _, err := profiles.UpdateProfile(t.Context(), ProfileEncoder, other.ID, "unclaimed encoder changed", map[string]any{}); err != nil {
		t.Fatalf("update unrelated profile: %v", err)
	}
	if err := profiles.DeleteProfile(t.Context(), ProfileEncoder, other.ID); err != nil {
		t.Fatalf("delete unrelated profile: %v", err)
	}
	if _, err := streams.UpdateStreamStatus(t.Context(), stream.ID, "failed"); err != nil {
		t.Fatalf("move claimed stream to failed before release: %v", err)
	}
	if err := streams.ReleaseReservedStreamYouTubeRelayBindingClaim(t.Context(), reserved); err != nil {
		t.Fatalf("release cleanup-confirmed claim: %v", err)
	}
	if _, err := profiles.UpdateProfile(t.Context(), ProfileYouTubeOutput, output.ID, "claimed relay output changed", map[string]any{"mode": youtubeRelayBindingClaimStaticRuntimeMode}); err != nil {
		t.Fatalf("update output after release: %v", err)
	}
}

func TestMemoryStreamYouTubeRelayBindingClaimReserveRequiresCurrentBoundOutputRevision(t *testing.T) {
	streams := NewMemoryStreamStore()
	stream, err := streams.CreateStream(t.Context(), "relay static revision fence")
	if err != nil {
		t.Fatalf("create stream: %v", err)
	}
	if _, err := streams.UpdateStreamStatus(t.Context(), stream.ID, "starting"); err != nil {
		t.Fatalf("move revision fence stream to starting: %v", err)
	}
	claim := testYouTubeRelayBindingClaim(stream.ID)
	if _, err := streams.UpdateStreamSettings(t.Context(), stream.ID, StreamSettings{YouTubeOutputID: claim.YouTubeOutputID}); err != nil {
		t.Fatalf("set unbound output: %v", err)
	}
	if _, err := streams.ReserveStreamYouTubeRelayBindingClaim(t.Context(), claim); !errors.Is(err, ErrNotFound) {
		t.Fatalf("reserve without paired output profile error = %v, want not found", err)
	}

	profiles := NewMemoryProfileStore()
	profiles.BindStreamYouTubeRelayBindingClaims(streams)
	nonOutput, err := profiles.CreateProfile(t.Context(), ProfileEncoder, "not a YouTube output", map[string]any{})
	if err != nil {
		t.Fatalf("create non-output profile: %v", err)
	}
	claim.YouTubeOutputID = nonOutput.ID
	setExpectedYouTubeRelayBindingProfileRevision(&claim, nonOutput.YouTubeRelayBindingRevision)
	if _, err := streams.UpdateStreamSettings(t.Context(), stream.ID, StreamSettings{YouTubeOutputID: nonOutput.ID}); err != nil {
		t.Fatalf("set non-output profile: %v", err)
	}
	if _, err := streams.ReserveStreamYouTubeRelayBindingClaim(t.Context(), claim); !errors.Is(err, ErrNotFound) {
		t.Fatalf("reserve non-output profile error = %v, want not found", err)
	}

	output, err := profiles.CreateProfile(t.Context(), ProfileYouTubeOutput, "relay static revision output", map[string]any{"mode": youtubeRelayBindingClaimStaticRuntimeMode})
	if err != nil {
		t.Fatalf("create YouTube output: %v", err)
	}
	claim.YouTubeOutputID = output.ID
	setExpectedYouTubeRelayBindingProfileRevision(&claim, output.YouTubeRelayBindingRevision)
	if _, err := streams.UpdateStreamSettings(t.Context(), stream.ID, StreamSettings{YouTubeOutputID: output.ID}); err != nil {
		t.Fatalf("set YouTube output: %v", err)
	}
	otherOutput, err := profiles.CreateProfile(t.Context(), ProfileYouTubeOutput, "relay static replacement output", map[string]any{"mode": youtubeRelayBindingClaimStaticRuntimeMode})
	if err != nil {
		t.Fatalf("create replacement YouTube output: %v", err)
	}
	if _, err := streams.UpdateStreamSettings(t.Context(), stream.ID, StreamSettings{YouTubeOutputID: otherOutput.ID}); err != nil {
		t.Fatalf("replace persisted YouTube output: %v", err)
	}
	if _, err := streams.ReserveStreamYouTubeRelayBindingClaim(t.Context(), claim); !errors.Is(err, ErrYouTubeRelayBindingClaimStreamOutputConflict) {
		t.Fatalf("reserve stale stream output error = %v, want stream output conflict", err)
	}
	if _, err := streams.UpdateStreamSettings(t.Context(), stream.ID, StreamSettings{YouTubeOutputID: output.ID}); err != nil {
		t.Fatalf("restore persisted YouTube output: %v", err)
	}
	if _, err := profiles.UpdateProfile(t.Context(), ProfileYouTubeOutput, output.ID, "relay static revision output updated", map[string]any{"mode": youtubeRelayBindingClaimStaticRuntimeMode}); err != nil {
		t.Fatalf("update output before reserve: %v", err)
	}
	if _, err := streams.ReserveStreamYouTubeRelayBindingClaim(t.Context(), claim); !errors.Is(err, ErrYouTubeRelayBindingClaimProfileRevisionConflict) {
		t.Fatalf("reserve stale output revision error = %v, want profile revision conflict", err)
	}
	output, err = profiles.GetProfile(t.Context(), ProfileYouTubeOutput, output.ID)
	if err != nil {
		t.Fatalf("reload output profile: %v", err)
	}
	setExpectedYouTubeRelayBindingProfileRevision(&claim, output.YouTubeRelayBindingRevision)
	reserved, err := streams.ReserveStreamYouTubeRelayBindingClaim(t.Context(), claim)
	if err != nil {
		t.Fatalf("reserve current output revision: %v", err)
	}
	if reserved.YouTubeOutputRevision != output.YouTubeRelayBindingRevision || reserved.ExpectedYouTubeOutputRevision != nil {
		t.Fatalf("reserved output revision fence = %#v", reserved)
	}
}

func TestMemoryStreamYouTubeRelayBindingClaimRecoveryIsNotAutoReleased(t *testing.T) {
	streams := NewMemoryStreamStore()
	stream, err := streams.CreateStream(t.Context(), "relay static recovery")
	if err != nil {
		t.Fatalf("create stream: %v", err)
	}
	claim := testMemoryYouTubeRelayBindingClaim(t, streams, stream.ID)
	if _, err := streams.UpdateStreamStatus(t.Context(), stream.ID, "starting"); err != nil {
		t.Fatalf("move recovery stream to starting: %v", err)
	}
	reserved, err := streams.ReserveStreamYouTubeRelayBindingClaim(t.Context(), claim)
	if err != nil {
		t.Fatalf("reserve claim: %v", err)
	}
	recovery := reserved
	recovery.BroadcastID = "broadcast-static-uncertain"
	recovery.LastError = "youtube_relay_static_bind_cleanup_uncertain"
	if _, err := streams.MarkStreamYouTubeRelayBindingClaimRecoveryRequired(t.Context(), recovery); err != nil {
		t.Fatalf("mark recovery required: %v", err)
	}
	if err := streams.ReleaseReservedStreamYouTubeRelayBindingClaim(t.Context(), reserved); !errors.Is(err, ErrYouTubeRelayBindingClaimState) {
		t.Fatalf("release recovery claim error = %v, want state error", err)
	}
	if _, err := streams.UpdateStreamSettings(t.Context(), stream.ID, StreamSettings{YouTubeOutputID: newUUID()}); !errors.Is(err, ErrYouTubeRelayBindingClaimActive) {
		t.Fatalf("update active claim output error = %v, want active claim error", err)
	}
	if err := streams.DeleteStream(t.Context(), stream.ID); !errors.Is(err, ErrYouTubeRelayBindingClaimActive) {
		t.Fatalf("delete active claim stream error = %v, want active claim error", err)
	}
	got, err := streams.GetStreamYouTubeRelayBindingClaim(t.Context(), claim.RelayBindingID)
	if err != nil {
		t.Fatalf("get recovery claim: %v", err)
	}
	if got.State != YouTubeRelayBindingClaimStateRecoveryRequired || got.DispatchState != YouTubeRelayBindingClaimDispatchStateNotDispatched || got.BroadcastID != recovery.BroadcastID || got.LastError != recovery.LastError {
		t.Fatalf("recovery claim = %#v", got)
	}
}

func TestMemoryStreamYouTubeRelayBindingClaimRecoveryResolutionRequiresNoRuntime(t *testing.T) {
	streams := NewMemoryStreamStore()
	stream, err := streams.CreateStream(t.Context(), "relay static recovery resolution")
	if err != nil {
		t.Fatalf("create stream: %v", err)
	}
	claim := testMemoryYouTubeRelayBindingClaim(t, streams, stream.ID)
	if _, err := streams.UpdateStreamStatus(t.Context(), stream.ID, "starting"); err != nil {
		t.Fatalf("move recovery resolution stream to starting: %v", err)
	}
	reserved, err := streams.ReserveStreamYouTubeRelayBindingClaim(t.Context(), claim)
	if err != nil {
		t.Fatalf("reserve claim: %v", err)
	}
	possiblyPrepared, err := streams.MarkReservedStreamYouTubeRelayBindingClaimPossiblyPrepared(t.Context(), reserved)
	if err != nil {
		t.Fatalf("mark recovery claim possibly prepared: %v", err)
	}
	prepared := possiblyPrepared
	prepared.BroadcastID = "broadcast-static-cleanup"
	runtime := StreamYouTubeRuntime{
		StreamID:       stream.ID,
		YouTubeOutput:  claim.YouTubeOutputID,
		OAuthAccountID: claim.OAuthAccountID,
		Mode:           youtubeRelayBindingClaimStaticRuntimeMode,
		BroadcastID:    prepared.BroadcastID,
		LiveStreamID:   claim.ReusableLiveStreamID,
		CompleteOnStop: true,
	}
	if err := streams.FinalizeStreamYouTubeRuntimeAndMarkRelayBindingPrepared(t.Context(), prepared, runtime); err != nil {
		t.Fatalf("finalize pre-dispatch recovery runtime: %v", err)
	}
	recovery := prepared
	recovery.LastError = "youtube_relay_static_bind_cleanup_uncertain"
	recovery, err = streams.MarkStreamYouTubeRelayBindingClaimRecoveryRequired(t.Context(), recovery)
	if err != nil {
		t.Fatalf("mark recovery required: %v", err)
	}
	if recovery.State != YouTubeRelayBindingClaimStateRecoveryRequired || recovery.DispatchState != YouTubeRelayBindingClaimDispatchStateNotDispatched {
		t.Fatalf("pre-dispatch recovery claim = %#v", recovery)
	}
	if _, err := streams.GetStreamYouTubeRuntime(t.Context(), stream.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("pre-dispatch recovery must remove static runtime: %v", err)
	}
	if err := streams.ResolveStreamYouTubeRelayBindingRecovery(t.Context(), recovery); !errors.Is(err, ErrYouTubeRelayBindingClaimState) {
		t.Fatalf("resolve active recovery error = %v, want state error", err)
	}
	if _, err := streams.UpdateStreamStatus(t.Context(), stream.ID, "failed"); err != nil {
		t.Fatalf("move recovery resolution stream to failed: %v", err)
	}
	streams.mu.Lock()
	streams.youtubeRuntimes[stream.ID] = StreamYouTubeRuntime{StreamID: stream.ID, Mode: youtubeRelayBindingClaimStaticRuntimeMode}
	streams.mu.Unlock()
	if err := streams.ResolveStreamYouTubeRelayBindingRecovery(t.Context(), recovery); !errors.Is(err, ErrYouTubeRelayBindingClaimState) {
		t.Fatalf("resolve recovery with runtime error = %v, want state error", err)
	}
	streams.mu.Lock()
	delete(streams.youtubeRuntimes, stream.ID)
	streams.mu.Unlock()
	if err := streams.ResolveStreamYouTubeRelayBindingRecovery(t.Context(), recovery); err != nil {
		t.Fatalf("resolve recovered cleanup claim: %v", err)
	}
	if _, err := streams.GetStreamYouTubeRelayBindingClaim(t.Context(), claim.RelayBindingID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("resolved recovery claim lookup error = %v, want not found", err)
	}
}

func TestMemoryStreamYouTubeRelayBindingClaimReleaseRequiresReservedState(t *testing.T) {
	streams := NewMemoryStreamStore()
	stream, err := streams.CreateStream(t.Context(), "relay static cleanup")
	if err != nil {
		t.Fatalf("create stream: %v", err)
	}
	claim := testMemoryYouTubeRelayBindingClaim(t, streams, stream.ID)
	if _, err := streams.UpdateStreamStatus(t.Context(), stream.ID, "starting"); err != nil {
		t.Fatalf("move cleanup stream to starting: %v", err)
	}
	reserved, err := streams.ReserveStreamYouTubeRelayBindingClaim(t.Context(), claim)
	if err != nil {
		t.Fatalf("reserve claim: %v", err)
	}
	if err := streams.ReleaseReservedStreamYouTubeRelayBindingClaim(t.Context(), reserved); !errors.Is(err, ErrYouTubeRelayBindingClaimState) {
		t.Fatalf("active release must fail closed: %v", err)
	}
	if _, err := streams.UpdateStreamStatus(t.Context(), stream.ID, "failed"); err != nil {
		t.Fatalf("move cleanup stream to failed: %v", err)
	}
	if err := streams.ReleaseReservedStreamYouTubeRelayBindingClaim(t.Context(), reserved); err != nil {
		t.Fatalf("release inactive confirmed cleanup claim: %v", err)
	}
	if _, err := streams.GetStreamYouTubeRelayBindingClaimForStream(t.Context(), stream.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("released claim lookup error = %v, want not found", err)
	}
}

func TestMemoryStreamYouTubeRelayBindingClaimStaleReservationCannotMutateReReservation(t *testing.T) {
	streams := NewMemoryStreamStore()
	stream, err := streams.CreateStream(t.Context(), "relay static reservation fence")
	if err != nil {
		t.Fatalf("create stream: %v", err)
	}
	claim := testMemoryYouTubeRelayBindingClaim(t, streams, stream.ID)
	if _, err := streams.UpdateStreamStatus(t.Context(), stream.ID, "starting"); err != nil {
		t.Fatalf("move reservation fence stream to starting: %v", err)
	}
	first, err := streams.ReserveStreamYouTubeRelayBindingClaim(t.Context(), claim)
	if err != nil {
		t.Fatalf("reserve first claim: %v", err)
	}
	if _, err := streams.UpdateStreamStatus(t.Context(), stream.ID, "failed"); err != nil {
		t.Fatalf("move first reservation to failed before release: %v", err)
	}
	if err := streams.ReleaseReservedStreamYouTubeRelayBindingClaim(t.Context(), first); err != nil {
		t.Fatalf("release first claim: %v", err)
	}
	if _, err := streams.UpdateStreamStatus(t.Context(), stream.ID, "starting"); err != nil {
		t.Fatalf("restart stream for second reservation: %v", err)
	}
	second, err := streams.ReserveStreamYouTubeRelayBindingClaim(t.Context(), claim)
	if err != nil {
		t.Fatalf("reserve second claim: %v", err)
	}
	if first.ReservationToken == second.ReservationToken || first.ReservationToken == "" || second.ReservationToken == "" {
		t.Fatalf("reservation tokens = %q, %q; want distinct nonempty fences", first.ReservationToken, second.ReservationToken)
	}
	stale := first
	stale.BroadcastID = "broadcast-static-stale"
	staleRuntime := StreamYouTubeRuntime{
		StreamID:       stream.ID,
		YouTubeOutput:  claim.YouTubeOutputID,
		OAuthAccountID: claim.OAuthAccountID,
		Mode:           youtubeRelayBindingClaimStaticRuntimeMode,
		BroadcastID:    stale.BroadcastID,
		LiveStreamID:   claim.ReusableLiveStreamID,
		CompleteOnStop: true,
	}
	if _, err := streams.UpdateStreamStatus(t.Context(), stream.ID, "starting"); err != nil {
		t.Fatalf("keep relay stream in starting: %v", err)
	}
	if err := streams.FinalizeStreamYouTubeRuntimeAndMarkRelayBindingPrepared(t.Context(), stale, staleRuntime); !errors.Is(err, ErrYouTubeRelayBindingClaimConflict) {
		t.Fatalf("stale finalize error = %v, want conflict", err)
	}
	if err := streams.ReleaseReservedStreamYouTubeRelayBindingClaim(t.Context(), first); !errors.Is(err, ErrYouTubeRelayBindingClaimConflict) {
		t.Fatalf("stale release error = %v, want conflict", err)
	}
	got, err := streams.GetStreamYouTubeRelayBindingClaim(t.Context(), claim.RelayBindingID)
	if err != nil {
		t.Fatalf("get re-reserved claim: %v", err)
	}
	if got.State != YouTubeRelayBindingClaimStateReserved || got.ReservationToken != second.ReservationToken {
		t.Fatalf("re-reserved claim after stale calls = %#v", got)
	}
}

func TestMemoryStreamYouTubeRelayBindingClaimStaleRecoveryCannotResolveReReservation(t *testing.T) {
	streams := NewMemoryStreamStore()
	stream, err := streams.CreateStream(t.Context(), "relay static stale recovery fence")
	if err != nil {
		t.Fatalf("create stream: %v", err)
	}
	claim := testMemoryYouTubeRelayBindingClaim(t, streams, stream.ID)
	if _, err := streams.UpdateStreamStatus(t.Context(), stream.ID, "starting"); err != nil {
		t.Fatalf("move stale recovery stream to starting: %v", err)
	}
	first, err := streams.ReserveStreamYouTubeRelayBindingClaim(t.Context(), claim)
	if err != nil {
		t.Fatalf("reserve first recovery claim: %v", err)
	}
	staleRecovery := first
	staleRecovery.BroadcastID = "broadcast-static-stale-recovery"
	staleRecovery.LastError = "youtube_relay_static_bind_cleanup_uncertain"
	staleRecovery, err = streams.MarkStreamYouTubeRelayBindingClaimRecoveryRequired(t.Context(), staleRecovery)
	if err != nil {
		t.Fatalf("mark first recovery claim: %v", err)
	}
	if _, err := streams.UpdateStreamStatus(t.Context(), stream.ID, "failed"); err != nil {
		t.Fatalf("move first recovery stream to failed: %v", err)
	}
	if err := streams.ResolveStreamYouTubeRelayBindingRecovery(t.Context(), staleRecovery); err != nil {
		t.Fatalf("resolve first recovery claim: %v", err)
	}
	if _, err := streams.UpdateStreamStatus(t.Context(), stream.ID, "starting"); err != nil {
		t.Fatalf("restart stream before second reservation: %v", err)
	}
	second, err := streams.ReserveStreamYouTubeRelayBindingClaim(t.Context(), claim)
	if err != nil {
		t.Fatalf("reserve second claim: %v", err)
	}
	if _, err := streams.UpdateStreamStatus(t.Context(), stream.ID, "failed"); err != nil {
		t.Fatalf("move re-reserved stream to failed: %v", err)
	}
	if err := streams.ResolveStreamYouTubeRelayBindingRecovery(t.Context(), staleRecovery); !errors.Is(err, ErrYouTubeRelayBindingClaimConflict) {
		t.Fatalf("stale recovery resolve error = %v, want claim conflict", err)
	}
	got, err := streams.GetStreamYouTubeRelayBindingClaim(t.Context(), claim.RelayBindingID)
	if err != nil {
		t.Fatalf("get second claim: %v", err)
	}
	if got.ReservationToken != second.ReservationToken || got.State != YouTubeRelayBindingClaimStateReserved {
		t.Fatalf("second claim after stale recovery resolve = %#v", got)
	}
}

func TestYouTubeRelayBindingClaimProfileConstraintErrorMappingIsNarrow(t *testing.T) {
	if !isYouTubeRelayBindingClaimProfileConstraintError(&mysql.MySQLError{Number: 1451, Message: "CONSTRAINT `fk_yt_relay_claim_youtube_output_revision`"}) {
		t.Fatal("named relay output revision foreign key should map to active claim")
	}
	for _, err := range []error{
		&mysql.MySQLError{Number: 1451, Message: "CONSTRAINT `unrelated_foreign_key`"},
		&mysql.MySQLError{Number: 1452, Message: "CONSTRAINT `fk_yt_relay_claim_youtube_output`"},
		errors.New("fk_yt_relay_claim_youtube_output"),
	} {
		if isYouTubeRelayBindingClaimProfileConstraintError(err) {
			t.Fatalf("unrelated database error mapped to active claim: %v", err)
		}
	}
}

func TestMemoryStreamYouTubeRelayBindingClaimRejectsInvalidIdentifiers(t *testing.T) {
	streams := NewMemoryStreamStore()
	stream, err := streams.CreateStream(t.Context(), "relay static validation")
	if err != nil {
		t.Fatalf("create stream: %v", err)
	}
	for _, relayBindingID := range []string{
		"relay binding with spaces",
		"relay-static-primary",
		"yt-stream-key-like-value",
		"relay-01234567-89AB-4cde-8f01-23456789abcd",
		" relay-01234567-89ab-4cde-8f01-23456789abcd ",
	} {
		claim := testYouTubeRelayBindingClaim(stream.ID)
		claim.RelayBindingID = relayBindingID
		if _, err := streams.ReserveStreamYouTubeRelayBindingClaim(t.Context(), claim); !errors.Is(err, ErrInvalidYouTubeRelayBindingClaim) {
			t.Fatalf("invalid binding id %q error = %v, want invalid claim", relayBindingID, err)
		}
		if _, err := streams.GetStreamYouTubeRelayBindingClaim(t.Context(), relayBindingID); !errors.Is(err, ErrInvalidYouTubeRelayBindingClaim) {
			t.Fatalf("get invalid binding id %q error = %v, want invalid claim", relayBindingID, err)
		}
		if _, err := streams.HasStreamYouTubeRelayBindingClaim(t.Context(), relayBindingID); !errors.Is(err, ErrInvalidYouTubeRelayBindingClaim) {
			t.Fatalf("has invalid binding id %q error = %v, want invalid claim", relayBindingID, err)
		}
	}
	claim := testYouTubeRelayBindingClaim(stream.ID)
	claim.ReusableLiveStreamID = string(make([]byte, youtubeRelayBindingClaimReusableLiveStreamIDMax+1))
	if _, err := streams.ReserveStreamYouTubeRelayBindingClaim(t.Context(), claim); !errors.Is(err, ErrInvalidYouTubeRelayBindingClaim) {
		t.Fatalf("oversized reusable live stream id error = %v, want invalid claim", err)
	}
	claim = testYouTubeRelayBindingClaim(stream.ID)
	claim.ReservationToken = newUUID()
	if _, err := streams.ReserveStreamYouTubeRelayBindingClaim(t.Context(), claim); !errors.Is(err, ErrInvalidYouTubeRelayBindingClaim) {
		t.Fatalf("caller-provided reservation token error = %v, want invalid claim", err)
	}
	claim = testYouTubeRelayBindingClaim(stream.ID)
	claim.DispatchState = YouTubeRelayBindingClaimDispatchStatePossiblyDispatched
	if _, err := streams.ReserveStreamYouTubeRelayBindingClaim(t.Context(), claim); !errors.Is(err, ErrInvalidYouTubeRelayBindingClaim) {
		t.Fatalf("caller-provided dispatch state error = %v, want invalid claim", err)
	}
}

func TestMemoryStreamYouTubeRelayBindingClaimRejectsPersistedNoncanonicalBindingBeforeNewReservation(t *testing.T) {
	streams := NewMemoryStreamStore()
	stream, err := streams.CreateStream(t.Context(), "legacy invalid relay binding")
	if err != nil {
		t.Fatal(err)
	}
	claim := testMemoryYouTubeRelayBindingClaim(t, streams, stream.ID)
	if _, err := streams.UpdateStreamStatus(t.Context(), stream.ID, "starting"); err != nil {
		t.Fatal(err)
	}
	const rawBindingID = "yt-stream-key-like-value"
	persisted := claim
	persisted.RelayBindingID = rawBindingID
	streams.mu.Lock()
	streams.youtubeRelayBindingClaims = map[string]YouTubeRelayBindingClaim{rawBindingID: persisted}
	streams.mu.Unlock()
	if _, err := streams.GetStreamYouTubeRelayBindingClaimForStream(t.Context(), stream.ID); !errors.Is(err, ErrInvalidYouTubeRelayBindingClaim) {
		t.Fatalf("persisted noncanonical binding lookup error = %v, want invalid claim", err)
	}
	if _, err := streams.ReserveStreamYouTubeRelayBindingClaim(t.Context(), claim); !errors.Is(err, ErrYouTubeRelayBindingClaimConflict) {
		t.Fatalf("persisted noncanonical binding allowed a new reservation: %v", err)
	}
}

func newMemoryPreparedRelayClaim(t *testing.T) (*MemoryStreamStore, Stream, YouTubeRelayBindingClaim) {
	t.Helper()
	streams := NewMemoryStreamStore()
	stream, err := streams.CreateStream(t.Context(), "relay static encoder receipt")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := streams.UpdateStreamStatus(t.Context(), stream.ID, "starting"); err != nil {
		t.Fatal(err)
	}
	claim := testMemoryYouTubeRelayBindingClaim(t, streams, stream.ID)
	reserved, err := streams.ReserveStreamYouTubeRelayBindingClaim(t.Context(), claim)
	if err != nil {
		t.Fatal(err)
	}
	possiblyPrepared, err := streams.MarkReservedStreamYouTubeRelayBindingClaimPossiblyPrepared(t.Context(), reserved)
	if err != nil {
		t.Fatal(err)
	}
	runtime := StreamYouTubeRuntime{
		StreamID:       stream.ID,
		YouTubeOutput:  claim.YouTubeOutputID,
		OAuthAccountID: claim.OAuthAccountID,
		Mode:           youtubeRelayBindingClaimStaticRuntimeMode,
		BroadcastID:    "broadcast-static-encoder-receipt",
		LiveStreamID:   claim.ReusableLiveStreamID,
		CompleteOnStop: true,
	}
	prepared := possiblyPrepared
	prepared.BroadcastID = runtime.BroadcastID
	if err := streams.FinalizeStreamYouTubeRuntimeAndMarkRelayBindingPrepared(t.Context(), prepared, runtime); err != nil {
		t.Fatal(err)
	}
	prepared, err = streams.GetStreamYouTubeRelayBindingClaim(t.Context(), claim.RelayBindingID)
	if err != nil {
		t.Fatal(err)
	}
	return streams, stream, prepared
}

func newMemoryPossiblyDispatchedRelayClaim(t *testing.T) (*MemoryStreamStore, Stream, YouTubeRelayBindingClaim) {
	t.Helper()
	streams, stream, prepared := newMemoryPreparedRelayClaim(t)
	possiblyDispatched, err := streams.MarkPreparedStreamYouTubeRelayBindingClaimPossiblyDispatched(t.Context(), prepared)
	if err != nil {
		t.Fatal(err)
	}
	return streams, stream, possiblyDispatched
}

func testYouTubeRelayBindingClaim(streamID string) YouTubeRelayBindingClaim {
	expectedRevision := uint64(0)
	return YouTubeRelayBindingClaim{
		RelayBindingID:                "relay-00000000-0000-4000-8000-000000000101",
		StreamID:                      streamID,
		YouTubeOutputID:               newUUID(),
		ExpectedYouTubeOutputRevision: &expectedRevision,
		OAuthAccountID:                newUUID(),
		ReusableLiveStreamID:          "youtube_reusable_live_stream_001",
	}
}

func testMemoryYouTubeRelayBindingClaim(t *testing.T, streams *MemoryStreamStore, streamID string) YouTubeRelayBindingClaim {
	t.Helper()
	profiles := NewMemoryProfileStore()
	profiles.BindStreamYouTubeRelayBindingClaims(streams)
	output, err := profiles.CreateProfile(t.Context(), ProfileYouTubeOutput, "relay static output "+streamID, map[string]any{"mode": youtubeRelayBindingClaimStaticRuntimeMode})
	if err != nil {
		t.Fatalf("create relay static output: %v", err)
	}
	if _, err := streams.UpdateStreamSettings(t.Context(), streamID, StreamSettings{YouTubeOutputID: output.ID}); err != nil {
		t.Fatalf("set relay static output: %v", err)
	}
	claim := testYouTubeRelayBindingClaim(streamID)
	claim.YouTubeOutputID = output.ID
	setExpectedYouTubeRelayBindingProfileRevision(&claim, output.YouTubeRelayBindingRevision)
	return claim
}

func setExpectedYouTubeRelayBindingProfileRevision(claim *YouTubeRelayBindingClaim, revision uint64) {
	claim.ExpectedYouTubeOutputRevision = &revision
}
