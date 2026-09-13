package store

import (
	"context"
	"strings"
)

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
