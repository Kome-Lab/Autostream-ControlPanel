package store_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/example/autostream-control-panel/internal/database"
	"github.com/example/autostream-control-panel/internal/store"
)

func TestMariaDBStreamYouTubeRelayBindingClaimLifecycle(t *testing.T) {
	dsn := os.Getenv("AUTOSTREAM_MARIADB_TEST_DSN")
	if dsn == "" {
		t.Skip("AUTOSTREAM_MARIADB_TEST_DSN is not configured")
	}
	t.Setenv("DATABASE_URL", dsn)
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	db, err := database.OpenFromEnv(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := database.RunEmbeddedMigrations(ctx, db); err != nil {
		t.Fatal(err)
	}
	if err := database.RunEmbeddedMigrations(ctx, db); err != nil {
		t.Fatalf("reapply embedded migrations: %v", err)
	}

	streams := store.NewMariaDBStreamStore(db)
	profiles := store.NewMariaDBProfileStore(db)
	stream, err := streams.CreateStream(ctx, "relay static MariaDB claim")
	if err != nil {
		t.Fatalf("create stream: %v", err)
	}
	claim := mariaDBYouTubeRelayBindingClaim(stream.ID)
	output, err := profiles.CreateProfile(ctx, store.ProfileYouTubeOutput, fmt.Sprintf("relay static MariaDB output %d", time.Now().UnixNano()), map[string]any{})
	if err != nil {
		t.Fatalf("create YouTube output profile: %v", err)
	}
	claim.YouTubeOutputID = output.ID
	setMariaDBRelayBindingExpectedRevision(&claim, output.YouTubeRelayBindingRevision)
	if _, err := streams.UpdateStreamSettings(ctx, stream.ID, store.StreamSettings{YouTubeOutputID: output.ID}); err != nil {
		t.Fatalf("set relay static output: %v", err)
	}
	if _, err := streams.ReserveStreamYouTubeRelayBindingClaim(ctx, claim); !errors.Is(err, store.ErrYouTubeRelayBindingClaimState) {
		t.Fatalf("reserve before stream starting err = %v, want state error", err)
	}
	if _, err := streams.UpdateStreamStatus(ctx, stream.ID, "starting"); err != nil {
		t.Fatalf("move relay stream to starting: %v", err)
	}
	otherOutput, err := profiles.CreateProfile(ctx, store.ProfileYouTubeOutput, fmt.Sprintf("relay static MariaDB replacement output %d", time.Now().UnixNano()), map[string]any{})
	if err != nil {
		t.Fatalf("create replacement YouTube output profile: %v", err)
	}
	if _, err := streams.UpdateStreamSettings(ctx, stream.ID, store.StreamSettings{YouTubeOutputID: otherOutput.ID}); err != nil {
		t.Fatalf("replace persisted YouTube output: %v", err)
	}
	if _, err := streams.ReserveStreamYouTubeRelayBindingClaim(ctx, claim); !errors.Is(err, store.ErrYouTubeRelayBindingClaimStreamOutputConflict) {
		t.Fatalf("reserve stale stream output err = %v, want stream output conflict", err)
	}
	if _, err := streams.UpdateStreamSettings(ctx, stream.ID, store.StreamSettings{YouTubeOutputID: output.ID}); err != nil {
		t.Fatalf("restore persisted YouTube output: %v", err)
	}
	reserved, err := streams.ReserveStreamYouTubeRelayBindingClaim(ctx, claim)
	if err != nil {
		t.Fatalf("reserve claim: %v", err)
	}
	if reserved.State != store.YouTubeRelayBindingClaimStateReserved || reserved.PrepareState != store.YouTubeRelayBindingClaimPrepareStateNotAttempted || reserved.DispatchState != store.YouTubeRelayBindingClaimDispatchStateNotDispatched || reserved.CreatedAt.IsZero() {
		t.Fatalf("reserved claim = %#v", reserved)
	}
	if _, err := profiles.UpdateProfile(ctx, store.ProfileYouTubeOutput, output.ID, output.Name+" updated", map[string]any{}); !errors.Is(err, store.ErrYouTubeRelayBindingClaimActive) {
		t.Fatalf("update claimed output err = %v, want active claim", err)
	}
	if err := profiles.DeleteProfile(ctx, store.ProfileYouTubeOutput, output.ID); !errors.Is(err, store.ErrYouTubeRelayBindingClaimActive) {
		t.Fatalf("delete claimed output err = %v, want active claim", err)
	}

	second, err := streams.CreateStream(ctx, "relay static MariaDB conflict")
	if err != nil {
		t.Fatalf("create second stream: %v", err)
	}
	conflict := mariaDBYouTubeRelayBindingClaim(second.ID)
	conflict.RelayBindingID = mariaDBRelayBindingID()
	conflict.YouTubeOutputID = claim.YouTubeOutputID
	setMariaDBRelayBindingExpectedRevision(&conflict, output.YouTubeRelayBindingRevision)
	conflict.OAuthAccountID = claim.OAuthAccountID
	conflict.ReusableLiveStreamID = claim.ReusableLiveStreamID
	if _, err := streams.UpdateStreamSettings(ctx, second.ID, store.StreamSettings{YouTubeOutputID: output.ID}); err != nil {
		t.Fatalf("set conflict relay static output: %v", err)
	}
	if _, err := streams.UpdateStreamStatus(ctx, second.ID, "starting"); err != nil {
		t.Fatalf("move conflict relay stream to starting: %v", err)
	}
	if _, err := streams.ReserveStreamYouTubeRelayBindingClaim(ctx, conflict); !errors.Is(err, store.ErrYouTubeRelayBindingClaimConflict) {
		t.Fatalf("reserve duplicate reusable stream err = %v, want conflict", err)
	}

	runtime := store.StreamYouTubeRuntime{
		StreamID:       stream.ID,
		YouTubeOutput:  claim.YouTubeOutputID,
		OAuthAccountID: claim.OAuthAccountID,
		Mode:           "live_api_relay_static",
		BroadcastID:    "broadcast-static-mariadb-001",
		LiveStreamID:   claim.ReusableLiveStreamID,
		CompleteOnStop: true,
	}
	possiblyPrepared, err := streams.MarkReservedStreamYouTubeRelayBindingClaimPossiblyPrepared(ctx, reserved)
	if err != nil {
		t.Fatalf("mark claim possibly prepared: %v", err)
	}
	prepared := possiblyPrepared
	prepared.BroadcastID = runtime.BroadcastID
	if err := streams.FinalizeStreamYouTubeRuntimeAndMarkRelayBindingPrepared(ctx, prepared, runtime); err != nil {
		t.Fatalf("finalize runtime and claim: %v", err)
	}
	prepared, err = streams.GetStreamYouTubeRelayBindingClaimForStream(ctx, stream.ID)
	if err != nil || prepared.State != store.YouTubeRelayBindingClaimStatePrepared || prepared.PrepareState != store.YouTubeRelayBindingClaimPrepareStatePossiblyPrepared || prepared.DispatchState != store.YouTubeRelayBindingClaimDispatchStateNotDispatched || prepared.BroadcastID != runtime.BroadcastID || prepared.ReservationToken == "" {
		t.Fatalf("prepared claim = %#v, %v", prepared, err)
	}
	if got, err := streams.GetStreamYouTubeRuntime(ctx, stream.ID); err != nil || got.Mode != runtime.Mode || got.BroadcastID != runtime.BroadcastID {
		t.Fatalf("prepared runtime = %#v, %v", got, err)
	}
	if err := streams.SaveStreamYouTubeRuntime(ctx, store.StreamYouTubeRuntime{StreamID: stream.ID, YouTubeOutput: claim.YouTubeOutputID, OAuthAccountID: claim.OAuthAccountID, Mode: "live_api"}); !errors.Is(err, store.ErrYouTubeRelayBindingClaimActive) {
		t.Fatalf("generic runtime overwrite err = %v, want active claim", err)
	}
	if _, err := streams.UpdateStreamSettings(ctx, stream.ID, store.StreamSettings{YouTubeOutputID: "33333333-3333-4333-8333-333333333333"}); !errors.Is(err, store.ErrYouTubeRelayBindingClaimActive) {
		t.Fatalf("update claimed output err = %v, want active claim", err)
	}
	if err := streams.DeleteStreamYouTubeRuntime(ctx, stream.ID); !errors.Is(err, store.ErrYouTubeRelayBindingClaimActive) {
		t.Fatalf("delete claimed runtime err = %v, want active claim", err)
	}
	staleDispatchFence := prepared
	staleDispatchFence.ReservationToken = "33333333-3333-4333-8333-333333333333"
	if _, err := streams.MarkPreparedStreamYouTubeRelayBindingClaimPossiblyDispatched(ctx, staleDispatchFence); !errors.Is(err, store.ErrYouTubeRelayBindingClaimConflict) {
		t.Fatalf("mark stale prepared claim possibly dispatched err = %v, want conflict", err)
	}
	possiblyDispatched, err := streams.MarkPreparedStreamYouTubeRelayBindingClaimPossiblyDispatched(ctx, prepared)
	if err != nil {
		t.Fatalf("mark prepared claim possibly dispatched: %v", err)
	}
	if possiblyDispatched.State != store.YouTubeRelayBindingClaimStatePrepared || possiblyDispatched.DispatchState != store.YouTubeRelayBindingClaimDispatchStatePossiblyDispatched {
		t.Fatalf("possibly dispatched claim = %#v", possiblyDispatched)
	}
	if again, err := streams.MarkPreparedStreamYouTubeRelayBindingClaimPossiblyDispatched(ctx, prepared); err != nil || again.DispatchState != store.YouTubeRelayBindingClaimDispatchStatePossiblyDispatched {
		t.Fatalf("idempotent possibly-dispatched marker = %#v, %v", again, err)
	}
	if _, err := streams.RecordStreamYouTubeRuntimeCompleteFailure(ctx, stream.ID, "youtube_live_api_complete_failed", time.Now().UTC().Add(-time.Second)); err != nil {
		t.Fatalf("record static runtime completion failure: %v", err)
	}
	due, err := streams.ListDueStreamYouTubeRuntimes(ctx, time.Now().UTC(), 100)
	if err != nil {
		t.Fatalf("list due static runtimes: %v", err)
	}
	foundDueStaticRuntime := false
	for _, dueRuntime := range due {
		if dueRuntime.StreamID == stream.ID && dueRuntime.Mode == runtime.Mode {
			foundDueStaticRuntime = true
			break
		}
	}
	if !foundDueStaticRuntime {
		t.Fatalf("due static runtimes = %#v, want stream %q", due, stream.ID)
	}
	if err := streams.DeleteStream(ctx, stream.ID); !errors.Is(err, store.ErrYouTubeRelayBindingClaimActive) {
		t.Fatalf("delete claimed stream err = %v, want active claim", err)
	}
	stalePrepared := possiblyDispatched
	stalePrepared.ReservationToken = "33333333-3333-4333-8333-333333333333"
	if err := streams.CompleteStreamYouTubeRuntimeAndReleaseRelayBindingClaim(ctx, stalePrepared); !errors.Is(err, store.ErrYouTubeRelayBindingClaimConflict) {
		t.Fatalf("complete stale prepared claim err = %v, want conflict", err)
	}
	if err := streams.CompleteStreamYouTubeRuntimeAndReleaseRelayBindingClaim(ctx, possiblyDispatched); !errors.Is(err, store.ErrYouTubeRelayBindingClaimState) {
		t.Fatalf("complete active static runtime err = %v, want state error", err)
	}
	if _, err := streams.UpdateStreamStatus(ctx, stream.ID, "failed"); err != nil {
		t.Fatalf("move static stream to failed: %v", err)
	}
	if err := streams.CompleteStreamYouTubeRuntimeAndReleaseRelayBindingClaim(ctx, possiblyDispatched); !errors.Is(err, store.ErrYouTubeRelayBindingClaimState) {
		t.Fatalf("complete unconfirmed failed static runtime err = %v, want state error", err)
	}
	if _, err := streams.UpdateStreamStatus(ctx, stream.ID, "completed"); err != nil {
		t.Fatalf("move static stream to completed: %v", err)
	}

	if err := streams.CompleteStreamYouTubeRuntimeAndReleaseRelayBindingClaim(ctx, possiblyDispatched); err != nil {
		t.Fatalf("complete runtime and claim: %v", err)
	}
	if _, err := streams.GetStreamYouTubeRuntime(ctx, stream.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("runtime after complete err = %v, want not found", err)
	}
	if _, err := streams.GetStreamYouTubeRelayBindingClaim(ctx, claim.RelayBindingID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("claim after complete err = %v, want not found", err)
	}
	if _, err := profiles.UpdateProfile(ctx, store.ProfileYouTubeOutput, output.ID, output.Name+" released", map[string]any{}); err != nil {
		t.Fatalf("update output after claim release: %v", err)
	}
	if _, err := streams.UpdateStreamStatus(ctx, stream.ID, "starting"); err != nil {
		t.Fatalf("restart stream for profile revision fence: %v", err)
	}
	if _, err := streams.ReserveStreamYouTubeRelayBindingClaim(ctx, claim); !errors.Is(err, store.ErrYouTubeRelayBindingClaimProfileRevisionConflict) {
		t.Fatalf("reserve after output update with stale revision err = %v, want profile revision conflict", err)
	}
	output, err = profiles.GetProfile(ctx, store.ProfileYouTubeOutput, output.ID)
	if err != nil {
		t.Fatalf("reload updated output profile: %v", err)
	}
	setMariaDBRelayBindingExpectedRevision(&claim, output.YouTubeRelayBindingRevision)
	staleFirst, err := streams.ReserveStreamYouTubeRelayBindingClaim(ctx, claim)
	if err != nil {
		t.Fatalf("reserve first stale-fence claim: %v", err)
	}
	if _, err := streams.UpdateStreamStatus(ctx, stream.ID, "failed"); err != nil {
		t.Fatalf("move first stale-fence stream to failed: %v", err)
	}
	if err := streams.ReleaseReservedStreamYouTubeRelayBindingClaim(ctx, staleFirst); err != nil {
		t.Fatalf("release first stale-fence claim: %v", err)
	}
	if _, err := streams.UpdateStreamStatus(ctx, stream.ID, "starting"); err != nil {
		t.Fatalf("restart stream for second stale-fence claim: %v", err)
	}
	staleSecond, err := streams.ReserveStreamYouTubeRelayBindingClaim(ctx, claim)
	if err != nil {
		t.Fatalf("reserve second stale-fence claim: %v", err)
	}
	staleFinalize := staleFirst
	staleFinalize.BroadcastID = "broadcast-static-mariadb-stale"
	staleRuntime := store.StreamYouTubeRuntime{
		StreamID:       stream.ID,
		YouTubeOutput:  output.ID,
		OAuthAccountID: claim.OAuthAccountID,
		Mode:           "live_api_relay_static",
		BroadcastID:    staleFinalize.BroadcastID,
		LiveStreamID:   claim.ReusableLiveStreamID,
		CompleteOnStop: true,
	}
	if err := streams.FinalizeStreamYouTubeRuntimeAndMarkRelayBindingPrepared(ctx, staleFinalize, staleRuntime); !errors.Is(err, store.ErrYouTubeRelayBindingClaimConflict) {
		t.Fatalf("stale finalize err = %v, want claim conflict", err)
	}
	if err := streams.ReleaseReservedStreamYouTubeRelayBindingClaim(ctx, staleFirst); !errors.Is(err, store.ErrYouTubeRelayBindingClaimConflict) {
		t.Fatalf("stale release err = %v, want claim conflict", err)
	}
	if _, err := streams.UpdateStreamStatus(ctx, stream.ID, "failed"); err != nil {
		t.Fatalf("move second stale-fence stream to failed: %v", err)
	}
	if err := streams.ReleaseReservedStreamYouTubeRelayBindingClaim(ctx, staleSecond); err != nil {
		t.Fatalf("release second stale-fence claim: %v", err)
	}
	if _, err := streams.UpdateStreamStatus(ctx, stream.ID, "starting"); err != nil {
		t.Fatalf("restart stream for prepare-fence claim: %v", err)
	}
	prepareNotAttempted, err := streams.ReserveStreamYouTubeRelayBindingClaim(ctx, claim)
	if err != nil {
		t.Fatalf("reserve prepare-fence release claim: %v", err)
	}
	if _, err := streams.UpdateStreamStatus(ctx, stream.ID, "failed"); err != nil {
		t.Fatalf("move prepare-fence release stream to failed: %v", err)
	}
	prepareReconcile := prepareNotAttempted
	prepareReconcile.BroadcastID = "unknown_external_broadcast"
	prepareReconcile.LastError = "youtube_relay_static_prepare_marker_unconfirmed"
	prepareResolution, err := streams.ReconcileReservedStreamYouTubeRelayBindingClaimAfterPrepareFence(ctx, prepareReconcile)
	if err != nil || !prepareResolution.Released {
		t.Fatalf("not-attempted prepare reconcile = %#v, %v", prepareResolution, err)
	}
	if _, err := streams.UpdateStreamStatus(ctx, stream.ID, "starting"); err != nil {
		t.Fatalf("restart stream for possibly-prepared fence: %v", err)
	}
	preparePossiblyReserved, err := streams.ReserveStreamYouTubeRelayBindingClaim(ctx, claim)
	if err != nil {
		t.Fatalf("reserve possibly-prepared fence claim: %v", err)
	}
	preparePossibly, err := streams.MarkReservedStreamYouTubeRelayBindingClaimPossiblyPrepared(ctx, preparePossiblyReserved)
	if err != nil {
		t.Fatalf("mark possibly-prepared fence claim: %v", err)
	}
	if _, err := streams.UpdateStreamStatus(ctx, stream.ID, "failed"); err != nil {
		t.Fatalf("move possibly-prepared fence stream to failed: %v", err)
	}
	prepareReconcile = preparePossibly
	prepareReconcile.BroadcastID = "unknown_external_broadcast"
	prepareReconcile.LastError = "youtube_relay_static_prepare_marker_unconfirmed"
	stalePrepareReconcile := prepareReconcile
	stalePrepareReconcile.ReservationToken = "33333333-3333-4333-8333-333333333333"
	if _, err := streams.ReconcileReservedStreamYouTubeRelayBindingClaimAfterPrepareFence(ctx, stalePrepareReconcile); !errors.Is(err, store.ErrYouTubeRelayBindingClaimConflict) {
		t.Fatalf("stale prepare reconcile err = %v, want conflict", err)
	}
	prepareResolution, err = streams.ReconcileReservedStreamYouTubeRelayBindingClaimAfterPrepareFence(ctx, prepareReconcile)
	if err != nil || prepareResolution.Released || prepareResolution.Claim.State != store.YouTubeRelayBindingClaimStateRecoveryRequired || prepareResolution.Claim.PrepareState != store.YouTubeRelayBindingClaimPrepareStatePossiblyPrepared {
		t.Fatalf("possibly-prepared reconcile = %#v, %v", prepareResolution, err)
	}
	if err := streams.ResolveStreamYouTubeRelayBindingRecovery(ctx, prepareResolution.Claim); err != nil {
		t.Fatalf("resolve possibly-prepared recovery claim: %v", err)
	}
	if _, err := streams.UpdateStreamStatus(ctx, stream.ID, "starting"); err != nil {
		t.Fatalf("restart stream after prepare recovery: %v", err)
	}
	recoveryReserved, err := streams.ReserveStreamYouTubeRelayBindingClaim(ctx, claim)
	if err != nil {
		t.Fatalf("reserve recovery claim: %v", err)
	}
	recoveryPossiblyPrepared, err := streams.MarkReservedStreamYouTubeRelayBindingClaimPossiblyPrepared(ctx, recoveryReserved)
	if err != nil {
		t.Fatalf("mark recovery claim possibly prepared: %v", err)
	}
	recoveryPrepared := recoveryPossiblyPrepared
	recoveryPrepared.BroadcastID = "broadcast-static-mariadb-cleanup"
	recoveryRuntime := store.StreamYouTubeRuntime{
		StreamID:       stream.ID,
		YouTubeOutput:  output.ID,
		OAuthAccountID: claim.OAuthAccountID,
		Mode:           "live_api_relay_static",
		BroadcastID:    recoveryPrepared.BroadcastID,
		LiveStreamID:   claim.ReusableLiveStreamID,
		CompleteOnStop: true,
	}
	if err := streams.FinalizeStreamYouTubeRuntimeAndMarkRelayBindingPrepared(ctx, recoveryPrepared, recoveryRuntime); err != nil {
		t.Fatalf("finalize pre-dispatch recovery runtime: %v", err)
	}
	recovery := recoveryPrepared
	recovery.LastError = "youtube_relay_static_bind_cleanup_uncertain"
	recovery, err = streams.MarkStreamYouTubeRelayBindingClaimRecoveryRequired(ctx, recovery)
	if err != nil {
		t.Fatalf("mark recovery claim: %v", err)
	}
	if recovery.State != store.YouTubeRelayBindingClaimStateRecoveryRequired || recovery.DispatchState != store.YouTubeRelayBindingClaimDispatchStateNotDispatched {
		t.Fatalf("pre-dispatch recovery claim = %#v", recovery)
	}
	if _, err := streams.GetStreamYouTubeRuntime(ctx, stream.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("pre-dispatch recovery must remove static runtime: %v", err)
	}
	if err := streams.ResolveStreamYouTubeRelayBindingRecovery(ctx, recovery); !errors.Is(err, store.ErrYouTubeRelayBindingClaimState) {
		t.Fatalf("resolve active recovery claim err = %v, want state error", err)
	}
	if _, err := streams.UpdateStreamStatus(ctx, stream.ID, "failed"); err != nil {
		t.Fatalf("move recovery stream to failed: %v", err)
	}
	if err := streams.ResolveStreamYouTubeRelayBindingRecovery(ctx, recovery); err != nil {
		t.Fatalf("resolve recovery claim: %v", err)
	}
	if _, err := streams.GetStreamYouTubeRelayBindingClaim(ctx, claim.RelayBindingID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("claim after recovery resolve err = %v, want not found", err)
	}

	abandonStream, err := streams.CreateStream(ctx, "relay static MariaDB abandon")
	if err != nil {
		t.Fatalf("create abandon stream: %v", err)
	}
	if _, err := streams.UpdateStreamStatus(ctx, abandonStream.ID, "starting"); err != nil {
		t.Fatalf("move abandon stream to starting: %v", err)
	}
	abandonClaim := mariaDBYouTubeRelayBindingClaim(abandonStream.ID)
	abandonClaim.YouTubeOutputID = output.ID
	setMariaDBRelayBindingExpectedRevision(&abandonClaim, output.YouTubeRelayBindingRevision)
	if _, err := streams.UpdateStreamSettings(ctx, abandonStream.ID, store.StreamSettings{YouTubeOutputID: output.ID}); err != nil {
		t.Fatalf("set abandon relay static output: %v", err)
	}
	abandonReserved, err := streams.ReserveStreamYouTubeRelayBindingClaim(ctx, abandonClaim)
	if err != nil {
		t.Fatalf("reserve abandon claim: %v", err)
	}
	abandonRuntime := store.StreamYouTubeRuntime{
		StreamID:       abandonStream.ID,
		YouTubeOutput:  output.ID,
		OAuthAccountID: abandonClaim.OAuthAccountID,
		Mode:           "live_api_relay_static",
		BroadcastID:    "broadcast-static-mariadb-abandon",
		LiveStreamID:   abandonClaim.ReusableLiveStreamID,
		CompleteOnStop: true,
	}
	abandonPossiblyPrepared, err := streams.MarkReservedStreamYouTubeRelayBindingClaimPossiblyPrepared(ctx, abandonReserved)
	if err != nil {
		t.Fatalf("mark abandon claim possibly prepared: %v", err)
	}
	abandonPrepared := abandonPossiblyPrepared
	abandonPrepared.BroadcastID = abandonRuntime.BroadcastID
	if err := streams.FinalizeStreamYouTubeRuntimeAndMarkRelayBindingPrepared(ctx, abandonPrepared, abandonRuntime); err != nil {
		t.Fatalf("finalize abandon runtime: %v", err)
	}
	abandonPrepared, err = streams.GetStreamYouTubeRelayBindingClaim(ctx, abandonClaim.RelayBindingID)
	if err != nil {
		t.Fatalf("get abandon prepared claim: %v", err)
	}
	possiblyAbandon, err := streams.MarkPreparedStreamYouTubeRelayBindingClaimPossiblyDispatched(ctx, abandonPrepared)
	if err != nil {
		t.Fatalf("mark abandon claim possibly dispatched: %v", err)
	}
	if possiblyAbandon.DispatchState != store.YouTubeRelayBindingClaimDispatchStatePossiblyDispatched {
		t.Fatalf("possibly dispatched abandon claim = %#v", possiblyAbandon)
	}
	if _, err := streams.RecordStreamYouTubeRuntimeCompleteFailure(ctx, abandonStream.ID, "youtube_live_api_complete_failed", time.Now().UTC().Add(-time.Second)); err != nil {
		t.Fatalf("queue abandon completion retry: %v", err)
	}
	possiblyAbandon.LastError = "youtube_relay_static_encoder_dispatch_failed"
	if _, err := streams.AbandonPreparedStreamYouTubeRuntimeAndMarkRelayBindingRecoveryRequired(ctx, possiblyAbandon); !errors.Is(err, store.ErrYouTubeRelayBindingClaimState) {
		t.Fatalf("abandon active prepared runtime err = %v, want state error", err)
	}
	if _, err := streams.UpdateStreamStatus(ctx, abandonStream.ID, "failed"); err != nil {
		t.Fatalf("move abandon stream to failed: %v", err)
	}
	encoderStopped, err := streams.MarkPreparedStreamYouTubeRelayBindingClaimEncoderStopConfirmed(ctx, possiblyAbandon)
	if err != nil || encoderStopped.EncoderStopConfirmedAt.IsZero() {
		t.Fatalf("mark encoder stop receipt before abandon = %#v, %v", encoderStopped, err)
	}
	possiblyAbandon = encoderStopped
	possiblyAbandon.LastError = "youtube_relay_static_encoder_dispatch_failed"
	abandoned, err := streams.AbandonPreparedStreamYouTubeRuntimeAndMarkRelayBindingRecoveryRequired(ctx, possiblyAbandon)
	if err != nil {
		t.Fatalf("abandon prepared runtime: %v", err)
	}
	if abandoned.State != store.YouTubeRelayBindingClaimStateRecoveryRequired || abandoned.DispatchState != store.YouTubeRelayBindingClaimDispatchStatePossiblyDispatched || abandoned.LastError != possiblyAbandon.LastError || !abandoned.EncoderStopConfirmedAt.Equal(encoderStopped.EncoderStopConfirmedAt) {
		t.Fatalf("abandoned claim = %#v", abandoned)
	}
	if _, err := streams.GetStreamYouTubeRuntime(ctx, abandonStream.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("abandoned runtime lookup err = %v, want not found", err)
	}
	due, err = streams.ListDueStreamYouTubeRuntimes(ctx, time.Now().UTC(), 100)
	if err != nil {
		t.Fatalf("list due after abandon: %v", err)
	}
	for _, runtime := range due {
		if runtime.StreamID == abandonStream.ID {
			t.Fatalf("abandoned runtime remained due: %#v", runtime)
		}
	}
}

func TestMariaDBStreamYouTubeRelayBindingClaimDispatchFenceReconcile(t *testing.T) {
	dsn := os.Getenv("AUTOSTREAM_MARIADB_TEST_DSN")
	if dsn == "" {
		t.Skip("AUTOSTREAM_MARIADB_TEST_DSN is not configured")
	}
	t.Setenv("DATABASE_URL", dsn)
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	db, err := database.OpenFromEnv(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := database.RunEmbeddedMigrations(ctx, db); err != nil {
		t.Fatal(err)
	}

	streams := store.NewMariaDBStreamStore(db)
	profiles := store.NewMariaDBProfileStore(db)
	output, err := profiles.CreateProfile(ctx, store.ProfileYouTubeOutput, fmt.Sprintf("relay static dispatch fence output %d", time.Now().UnixNano()), map[string]any{})
	if err != nil {
		t.Fatalf("create YouTube output profile: %v", err)
	}

	for _, dispatchState := range []string{
		store.YouTubeRelayBindingClaimDispatchStateNotDispatched,
		store.YouTubeRelayBindingClaimDispatchStatePossiblyDispatched,
	} {
		t.Run(dispatchState, func(t *testing.T) {
			stream, err := streams.CreateStream(ctx, "relay static MariaDB dispatch fence "+dispatchState)
			if err != nil {
				t.Fatalf("create stream: %v", err)
			}
			if _, err := streams.UpdateStreamStatus(ctx, stream.ID, "starting"); err != nil {
				t.Fatalf("move stream to starting: %v", err)
			}
			claim := mariaDBYouTubeRelayBindingClaim(stream.ID)
			claim.YouTubeOutputID = output.ID
			setMariaDBRelayBindingExpectedRevision(&claim, output.YouTubeRelayBindingRevision)
			if _, err := streams.UpdateStreamSettings(ctx, stream.ID, store.StreamSettings{YouTubeOutputID: output.ID}); err != nil {
				t.Fatalf("set relay output: %v", err)
			}
			reserved, err := streams.ReserveStreamYouTubeRelayBindingClaim(ctx, claim)
			if err != nil {
				t.Fatalf("reserve claim: %v", err)
			}
			possiblyPrepared, err := streams.MarkReservedStreamYouTubeRelayBindingClaimPossiblyPrepared(ctx, reserved)
			if err != nil {
				t.Fatalf("mark possibly prepared: %v", err)
			}
			runtime := store.StreamYouTubeRuntime{
				StreamID:       stream.ID,
				YouTubeOutput:  output.ID,
				OAuthAccountID: claim.OAuthAccountID,
				Mode:           "live_api_relay_static",
				BroadcastID:    "broadcast-static-mariadb-dispatch-fence-" + dispatchState,
				LiveStreamID:   claim.ReusableLiveStreamID,
				CompleteOnStop: true,
			}
			prepared := possiblyPrepared
			prepared.BroadcastID = runtime.BroadcastID
			if err := streams.FinalizeStreamYouTubeRuntimeAndMarkRelayBindingPrepared(ctx, prepared, runtime); err != nil {
				t.Fatalf("finalize runtime: %v", err)
			}
			prepared, err = streams.GetStreamYouTubeRelayBindingClaim(ctx, claim.RelayBindingID)
			if err != nil {
				t.Fatalf("get prepared claim: %v", err)
			}
			if dispatchState == store.YouTubeRelayBindingClaimDispatchStatePossiblyDispatched {
				prepared, err = streams.MarkPreparedStreamYouTubeRelayBindingClaimPossiblyDispatched(ctx, prepared)
				if err != nil {
					t.Fatalf("mark possibly dispatched: %v", err)
				}
			}
			if _, err := streams.UpdateStreamStatus(ctx, stream.ID, "failed"); err != nil {
				t.Fatalf("move stream to failed: %v", err)
			}
			reconcile := prepared
			reconcile.LastError = "youtube_relay_static_dispatch_marker_unconfirmed"
			stale := reconcile
			stale.ReservationToken = "44444444-4444-4444-8444-444444444444"
			if _, err := streams.ReconcilePreparedStreamYouTubeRelayBindingClaimAfterDispatchFence(ctx, stale); !errors.Is(err, store.ErrYouTubeRelayBindingClaimConflict) {
				t.Fatalf("stale reconcile error = %v, want conflict", err)
			}
			recovery, err := streams.ReconcilePreparedStreamYouTubeRelayBindingClaimAfterDispatchFence(ctx, reconcile)
			if err != nil {
				t.Fatalf("reconcile dispatch fence: %v", err)
			}
			if recovery.State != store.YouTubeRelayBindingClaimStateRecoveryRequired || recovery.PrepareState != store.YouTubeRelayBindingClaimPrepareStatePossiblyPrepared || recovery.DispatchState != dispatchState || recovery.BroadcastID != prepared.BroadcastID || recovery.LastError != reconcile.LastError {
				t.Fatalf("dispatch fence recovery = %#v", recovery)
			}
			if _, err := streams.GetStreamYouTubeRuntime(ctx, stream.ID); !errors.Is(err, store.ErrNotFound) {
				t.Fatalf("runtime after reconcile err = %v, want not found", err)
			}
			if err := streams.ReleaseReservedStreamYouTubeRelayBindingClaim(ctx, recovery); !errors.Is(err, store.ErrYouTubeRelayBindingClaimState) {
				t.Fatalf("recovery release err = %v, want state error", err)
			}
			if again, err := streams.ReconcilePreparedStreamYouTubeRelayBindingClaimAfterDispatchFence(ctx, reconcile); err != nil || again.State != store.YouTubeRelayBindingClaimStateRecoveryRequired {
				t.Fatalf("idempotent reconcile = %#v, %v", again, err)
			}
			if dispatchState == store.YouTubeRelayBindingClaimDispatchStatePossiblyDispatched {
				if err := streams.ResolveStreamYouTubeRelayBindingRecovery(ctx, recovery); !errors.Is(err, store.ErrYouTubeRelayBindingClaimState) {
					t.Fatalf("resolve without encoder receipt err = %v, want state error", err)
				}
				recovery, err = streams.MarkPreparedStreamYouTubeRelayBindingClaimEncoderStopConfirmed(ctx, recovery)
				if err != nil || recovery.EncoderStopConfirmedAt.IsZero() {
					t.Fatalf("mark recovery encoder receipt = %#v, %v", recovery, err)
				}
			}
			if err := streams.ResolveStreamYouTubeRelayBindingRecovery(ctx, recovery); err != nil {
				t.Fatalf("resolve explicit recovery cleanup: %v", err)
			}
		})
	}
}

func mariaDBYouTubeRelayBindingClaim(streamID string) store.YouTubeRelayBindingClaim {
	suffix := time.Now().UnixNano()
	expectedRevision := uint64(0)
	return store.YouTubeRelayBindingClaim{
		RelayBindingID:                mariaDBRelayBindingID(),
		StreamID:                      streamID,
		YouTubeOutputID:               "11111111-1111-4111-8111-111111111111",
		ExpectedYouTubeOutputRevision: &expectedRevision,
		OAuthAccountID:                "22222222-2222-4222-8222-222222222222",
		ReusableLiveStreamID:          fmt.Sprintf("youtube_reusable_live_stream_mariadb_%d", suffix),
	}
}

func mariaDBRelayBindingID() string {
	return fmt.Sprintf("relay-00000000-0000-4000-8000-%012x", time.Now().UnixNano()&0xffffffffffff)
}

func setMariaDBRelayBindingExpectedRevision(claim *store.YouTubeRelayBindingClaim, revision uint64) {
	claim.ExpectedYouTubeOutputRevision = &revision
}
