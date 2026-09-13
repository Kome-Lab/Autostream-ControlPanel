package store

import (
	"errors"
	"github.com/go-sql-driver/mysql"
	"strings"
	"time"
)

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
