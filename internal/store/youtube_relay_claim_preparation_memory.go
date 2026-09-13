package store

import (
	"context"
	"strings"
	"time"
)

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
