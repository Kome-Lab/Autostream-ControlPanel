package store

import (
	"context"
	"strings"
)

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
