package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"sort"
	"strings"
	"time"
)

// DiscordYouTubeLiveNotificationState is deliberately independent from the
// stream state. A stream can have started locally while the provider has not
// yet confirmed that its broadcast is live.
const (
	DiscordYouTubeLiveNotificationStateAwaitingYouTubeLive = "awaiting_youtube_live"
	DiscordYouTubeLiveNotificationStateDispatchPending     = "dispatch_pending"
	// Dispatching means the dispatcher holds an internally reclaimable lease
	// but has not crossed the Discord side-effect boundary.
	DiscordYouTubeLiveNotificationStateDispatching = "dispatching"
	// BotDispatching is committed immediately before the Bot HTTP request. Its
	// lease expiry must be fenced to delivery_unknown, never re-dispatched.
	DiscordYouTubeLiveNotificationStateBotDispatching = "bot_dispatching"
	DiscordYouTubeLiveNotificationStateDelivered      = "delivered"
	// DeliveryUnknown is a recovery fence, not a retryable failure. It is used
	// after a transport/5xx outcome where Discord may already have accepted the
	// post. Retrying that event automatically could duplicate a public message.
	DiscordYouTubeLiveNotificationStateDeliveryUnknown = "delivery_unknown"
	DiscordYouTubeLiveNotificationStateSuppressed      = "suppressed"
)

const (
	discordYouTubeLiveNotificationEventPrefix = "youtube-live-"
	discordYouTubeLiveNotificationMaxWatchURL = 1900
	discordYouTubeLiveNotificationMaxError    = 128
	discordYouTubeLiveNotificationClaimTTL    = 2 * time.Minute
)

var (
	ErrInvalidDiscordYouTubeLiveNotification      = errors.New("invalid discord youtube live notification")
	ErrDiscordYouTubeLiveNotificationClaimLost    = errors.New("discord youtube live notification claim lost")
	ErrDiscordYouTubeLiveNotificationRecoveryGone = errors.New("discord youtube live notification recovery unavailable")
	ErrDiscordYouTubeLiveNotificationRecoveryUsed = errors.New("discord youtube live notification recovery already requested")
)

// DiscordYouTubeLiveNotification is a durable, secret-free delivery record.
// EventID is intentionally stable for a single attempt chain and is sent to
// the Bot's idempotency boundary. DiscordMessageID is a confirmed receipt.
type DiscordYouTubeLiveNotification struct {
	ID                    string     `json:"id"`
	StreamID              string     `json:"stream_id"`
	EventID               string     `json:"event_id"`
	WatchURL              string     `json:"watch_url"`
	DiscordServiceID      string     `json:"discord_service_id"`
	DiscordTextChannelID  string     `json:"discord_text_channel_id"`
	YouTubeMode           string     `json:"youtube_mode"`
	YouTubeOAuthAccountID string     `json:"youtube_oauth_account_id,omitempty"`
	YouTubeBroadcastID    string     `json:"youtube_broadcast_id,omitempty"`
	LifecycleStatus       string     `json:"lifecycle_status,omitempty"`
	State                 string     `json:"state"`
	AttemptCount          int        `json:"attempt_count"`
	DispatchAttemptCount  int        `json:"dispatch_attempt_count"`
	NextAttemptAt         *time.Time `json:"next_attempt_at,omitempty"`
	LastError             string     `json:"last_error,omitempty"`
	DiscordMessageID      string     `json:"discord_message_id,omitempty"`
	DeliveredAt           *time.Time `json:"delivered_at,omitempty"`
	RecoveryOfID          string     `json:"recovery_of_id,omitempty"`
	CreatedAt             time.Time  `json:"created_at"`
	UpdatedAt             time.Time  `json:"updated_at"`
}

// DiscordYouTubeLiveNotificationClaim carries the opaque lease only between
// the durable store and the in-process dispatcher. The raw lease is never
// serialized or retained by the database.
type DiscordYouTubeLiveNotificationClaim struct {
	Notification DiscordYouTubeLiveNotification
	LeaseToken   string `json:"-"`
}

// StreamDiscordYouTubeLiveNotificationStore is optional so older focused
// StreamStore fakes remain usable. Production and MemoryStreamStore implement
// it; without it the Panel does not claim that a notification was delivered.
type StreamDiscordYouTubeLiveNotificationStore interface {
	TransitionStreamStatusAndEnqueueDiscordYouTubeLiveNotification(ctx context.Context, streamID, expectedStatus, status string, notification DiscordYouTubeLiveNotification) (Stream, DiscordYouTubeLiveNotification, bool, error)
	ClaimDueDiscordYouTubeLiveNotifications(ctx context.Context, now time.Time, leaseTTL time.Duration, limit int) ([]DiscordYouTubeLiveNotificationClaim, error)
	FenceExpiredDiscordYouTubeLiveNotificationDispatches(ctx context.Context, now time.Time, limit int) ([]DiscordYouTubeLiveNotification, error)
	BeginDiscordYouTubeLiveNotificationBotDispatch(ctx context.Context, id, leaseToken string) (DiscordYouTubeLiveNotification, error)
	RecordDiscordYouTubeLiveNotificationRetry(ctx context.Context, id, leaseToken, state, lifecycleStatus, lastError string, nextAttemptAt time.Time) error
	MarkDiscordYouTubeLiveNotificationDelivered(ctx context.Context, id, leaseToken, lifecycleStatus, messageID string) error
	MarkDiscordYouTubeLiveNotificationDeliveryUnknown(ctx context.Context, id, leaseToken, lifecycleStatus, lastError string) error
	MarkDiscordYouTubeLiveNotificationSuppressed(ctx context.Context, id, leaseToken, lifecycleStatus, lastError string) error
	CreateDiscordYouTubeLiveNotificationRecovery(ctx context.Context, streamID, priorNotificationID string) (DiscordYouTubeLiveNotification, error)
	GetLatestDiscordYouTubeLiveNotification(ctx context.Context, streamID string) (DiscordYouTubeLiveNotification, error)
}

func initialDiscordYouTubeLiveNotificationState(mode string) string {
	// Existing stream-key outputs have no provider Broadcast identity. Preserve
	// their established behavior as an explicit compatibility decision: dispatch
	// only from the durable outbox after a successful local start, while marking
	// lifecycle confirmation unavailable rather than inventing provider proof.
	if strings.EqualFold(strings.TrimSpace(mode), "stream_key") {
		return DiscordYouTubeLiveNotificationStateDispatchPending
	}
	return DiscordYouTubeLiveNotificationStateAwaitingYouTubeLive
}

func normalizeNewDiscordYouTubeLiveNotification(notification DiscordYouTubeLiveNotification, now time.Time) (DiscordYouTubeLiveNotification, error) {
	notification.ID = strings.TrimSpace(notification.ID)
	if notification.ID == "" {
		notification.ID = newUUID()
	}
	notification.StreamID = strings.TrimSpace(notification.StreamID)
	notification.EventID = strings.TrimSpace(notification.EventID)
	if notification.EventID == "" {
		notification.EventID = discordYouTubeLiveNotificationEventPrefix + newUUID()
	}
	notification.WatchURL = strings.TrimSpace(notification.WatchURL)
	notification.DiscordServiceID = strings.TrimSpace(notification.DiscordServiceID)
	notification.DiscordTextChannelID = strings.TrimSpace(notification.DiscordTextChannelID)
	notification.YouTubeMode = strings.TrimSpace(notification.YouTubeMode)
	notification.YouTubeOAuthAccountID = strings.TrimSpace(notification.YouTubeOAuthAccountID)
	notification.YouTubeBroadcastID = strings.TrimSpace(notification.YouTubeBroadcastID)
	notification.LifecycleStatus = normalizeDiscordYouTubeLiveNotificationCode(notification.LifecycleStatus)
	notification.State = strings.TrimSpace(notification.State)
	if notification.State == "" {
		notification.State = initialDiscordYouTubeLiveNotificationState(notification.YouTubeMode)
	}
	if notification.StreamID == "" || notification.EventID == "" || len(notification.EventID) > 256 ||
		!validDiscordYouTubeLiveNotificationOpaqueValue(notification.EventID) || notification.WatchURL == "" ||
		len(notification.WatchURL) > discordYouTubeLiveNotificationMaxWatchURL || !validDiscordYouTubeLiveNotificationOpaqueValue(notification.WatchURL) ||
		notification.DiscordServiceID == "" || notification.DiscordTextChannelID == "" ||
		!validDiscordYouTubeLiveNotificationOpaqueValue(notification.DiscordServiceID) ||
		!validDiscordYouTubeLiveNotificationOpaqueValue(notification.DiscordTextChannelID) {
		return DiscordYouTubeLiveNotification{}, ErrInvalidDiscordYouTubeLiveNotification
	}
	switch notification.YouTubeMode {
	case "stream_key":
		if notification.State != DiscordYouTubeLiveNotificationStateDispatchPending {
			return DiscordYouTubeLiveNotification{}, ErrInvalidDiscordYouTubeLiveNotification
		}
		notification.LifecycleStatus = "legacy_unverified"
	case "live_api", "live_api_relay_static":
		if notification.State != DiscordYouTubeLiveNotificationStateAwaitingYouTubeLive || notification.YouTubeOAuthAccountID == "" || notification.YouTubeBroadcastID == "" ||
			!validDiscordYouTubeLiveNotificationOpaqueValue(notification.YouTubeBroadcastID) {
			return DiscordYouTubeLiveNotification{}, ErrInvalidDiscordYouTubeLiveNotification
		}
	default:
		return DiscordYouTubeLiveNotification{}, ErrInvalidDiscordYouTubeLiveNotification
	}
	notification.AttemptCount = 0
	notification.DispatchAttemptCount = 0
	notification.LastError = ""
	notification.DiscordMessageID = ""
	notification.DeliveredAt = nil
	notification.RecoveryOfID = strings.TrimSpace(notification.RecoveryOfID)
	notification.NextAttemptAt = timePtrUTC(now)
	notification.CreatedAt = now.UTC()
	notification.UpdatedAt = now.UTC()
	return notification, nil
}

func validDiscordYouTubeLiveNotificationOpaqueValue(value string) bool {
	if value == "" {
		return false
	}
	for _, character := range value {
		if character < 0x20 || character == 0x7f {
			return false
		}
	}
	return true
}

func normalizeDiscordYouTubeLiveNotificationCode(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if len(value) > discordYouTubeLiveNotificationMaxError {
		return ""
	}
	for _, character := range value {
		if (character >= 'a' && character <= 'z') || (character >= '0' && character <= '9') || character == '_' || character == '-' {
			continue
		}
		return ""
	}
	return value
}

func validDiscordYouTubeLiveNotificationRetryState(state string) bool {
	return state == DiscordYouTubeLiveNotificationStateAwaitingYouTubeLive || state == DiscordYouTubeLiveNotificationStateDispatchPending
}

func discordYouTubeLiveNotificationLeaseHash(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

func timePtrUTC(value time.Time) *time.Time {
	if value.IsZero() {
		return nil
	}
	utc := value.UTC()
	return &utc
}

func normalizeDiscordYouTubeLiveNotificationLeaseTTL(value time.Duration) time.Duration {
	if value <= 0 {
		return discordYouTubeLiveNotificationClaimTTL
	}
	return value
}

func normalizeDiscordYouTubeLiveNotificationLimit(value int) int {
	if value <= 0 || value > 100 {
		return 25
	}
	return value
}

func (s MariaDBStreamStore) TransitionStreamStatusAndEnqueueDiscordYouTubeLiveNotification(ctx context.Context, streamID, expectedStatus, status string, notification DiscordYouTubeLiveNotification) (Stream, DiscordYouTubeLiveNotification, bool, error) {
	if err := ctx.Err(); err != nil {
		return Stream{}, DiscordYouTubeLiveNotification{}, false, err
	}
	now := time.Now().UTC()
	notification.StreamID = strings.TrimSpace(streamID)
	notification, err := normalizeNewDiscordYouTubeLiveNotification(notification, now)
	if err != nil {
		return Stream{}, DiscordYouTubeLiveNotification{}, false, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Stream{}, DiscordYouTubeLiveNotification{}, false, err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `UPDATE streams SET status = ?, updated_at = ? WHERE id = ? AND LOWER(TRIM(status)) = LOWER(TRIM(?))`, status, now, notification.StreamID, expectedStatus)
	if err != nil {
		return Stream{}, DiscordYouTubeLiveNotification{}, false, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return Stream{}, DiscordYouTubeLiveNotification{}, false, err
	}
	if affected == 0 {
		if rollbackErr := tx.Rollback(); rollbackErr != nil {
			return Stream{}, DiscordYouTubeLiveNotification{}, false, rollbackErr
		}
		stream, getErr := s.GetStream(ctx, notification.StreamID)
		return stream, DiscordYouTubeLiveNotification{}, false, getErr
	}
	if err := insertDiscordYouTubeLiveNotificationTx(ctx, tx, notification); err != nil {
		return Stream{}, DiscordYouTubeLiveNotification{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return Stream{}, DiscordYouTubeLiveNotification{}, false, err
	}
	stream, err := s.GetStream(ctx, notification.StreamID)
	if err != nil {
		return Stream{}, DiscordYouTubeLiveNotification{}, false, err
	}
	return stream, notification, true, nil
}

func insertDiscordYouTubeLiveNotificationTx(ctx context.Context, tx *sql.Tx, notification DiscordYouTubeLiveNotification) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO stream_discord_youtube_live_notifications (
id, stream_id, event_id, watch_url, discord_service_id, discord_text_channel_id,
youtube_mode, youtube_oauth_account_id, youtube_broadcast_id, lifecycle_status,
 state, attempt_count, dispatch_attempt_count, next_attempt_at, lease_token_hash, lease_expires_at, last_error,
discord_message_id, delivered_at, recovery_of_id, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, NULLIF(?, ''), NULLIF(?, ''), ?, ?, ?, ?, ?, NULL, NULL, '', NULL, NULL, NULLIF(?, ''), ?, ?)`,
		notification.ID, notification.StreamID, notification.EventID, notification.WatchURL, notification.DiscordServiceID, notification.DiscordTextChannelID,
		notification.YouTubeMode, notification.YouTubeOAuthAccountID, notification.YouTubeBroadcastID, notification.LifecycleStatus,
		notification.State, notification.AttemptCount, notification.DispatchAttemptCount, notification.NextAttemptAt, notification.RecoveryOfID, notification.CreatedAt, notification.UpdatedAt)
	return err
}

func (s MariaDBStreamStore) ClaimDueDiscordYouTubeLiveNotifications(ctx context.Context, now time.Time, leaseTTL time.Duration, limit int) ([]DiscordYouTubeLiveNotificationClaim, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	leaseTTL = normalizeDiscordYouTubeLiveNotificationLeaseTTL(leaseTTL)
	limit = normalizeDiscordYouTubeLiveNotificationLimit(limit)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, discordYouTubeLiveNotificationSelect+`
WHERE ((state IN ('awaiting_youtube_live', 'dispatch_pending') AND next_attempt_at IS NOT NULL AND next_attempt_at <= ?)
   OR (state = 'dispatching' AND lease_expires_at IS NOT NULL AND lease_expires_at <= ?))
ORDER BY COALESCE(next_attempt_at, lease_expires_at) ASC, created_at ASC
LIMIT ? FOR UPDATE`, now.UTC(), now.UTC(), limit)
	if err != nil {
		return nil, err
	}
	items := make([]DiscordYouTubeLiveNotification, 0, limit)
	for rows.Next() {
		var item DiscordYouTubeLiveNotification
		if err := scanDiscordYouTubeLiveNotification(rows, &item); err != nil {
			rows.Close()
			return nil, err
		}
		items = append(items, item)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	claims := make([]DiscordYouTubeLiveNotificationClaim, 0, len(items))
	leaseExpiresAt := now.UTC().Add(leaseTTL)
	for _, item := range items {
		leaseToken := newUUID()
		result, err := tx.ExecContext(ctx, `UPDATE stream_discord_youtube_live_notifications
SET state = 'dispatching', attempt_count = attempt_count + 1, lease_token_hash = ?, lease_expires_at = ?, updated_at = ?
WHERE id = ? AND ((state IN ('awaiting_youtube_live', 'dispatch_pending') AND next_attempt_at IS NOT NULL AND next_attempt_at <= ?)
 OR (state = 'dispatching' AND lease_expires_at IS NOT NULL AND lease_expires_at <= ?))`, discordYouTubeLiveNotificationLeaseHash(leaseToken), leaseExpiresAt, now.UTC(), item.ID, now.UTC(), now.UTC())
		if err != nil {
			return nil, err
		}
		affected, err := result.RowsAffected()
		if err != nil {
			return nil, err
		}
		if affected != 1 {
			return nil, ErrDiscordYouTubeLiveNotificationClaimLost
		}
		item.State = DiscordYouTubeLiveNotificationStateDispatching
		item.AttemptCount++
		item.UpdatedAt = now.UTC()
		claims = append(claims, DiscordYouTubeLiveNotificationClaim{Notification: item, LeaseToken: leaseToken})
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return claims, nil
}

// FenceExpiredDiscordYouTubeLiveNotificationDispatches closes only leases that
// had already crossed the Bot side-effect boundary. It is deliberately a
// terminal transition: after a process crash or receipt-write failure, the
// Panel cannot know whether Discord accepted the message.
func (s MariaDBStreamStore) FenceExpiredDiscordYouTubeLiveNotificationDispatches(ctx context.Context, now time.Time, limit int) ([]DiscordYouTubeLiveNotification, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	limit = normalizeDiscordYouTubeLiveNotificationLimit(limit)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, discordYouTubeLiveNotificationSelect+`
WHERE state = 'bot_dispatching' AND lease_expires_at IS NOT NULL AND lease_expires_at <= ?
ORDER BY lease_expires_at ASC, created_at ASC
LIMIT ? FOR UPDATE`, now.UTC(), limit)
	if err != nil {
		return nil, err
	}
	items := make([]DiscordYouTubeLiveNotification, 0, limit)
	for rows.Next() {
		var item DiscordYouTubeLiveNotification
		if err := scanDiscordYouTubeLiveNotification(rows, &item); err != nil {
			rows.Close()
			return nil, err
		}
		items = append(items, item)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for index := range items {
		item := &items[index]
		result, err := tx.ExecContext(ctx, `UPDATE stream_discord_youtube_live_notifications
SET state = 'delivery_unknown', next_attempt_at = NULL, lease_token_hash = NULL, lease_expires_at = NULL,
last_error = 'discord_dispatch_lease_expired_unknown', updated_at = ?
WHERE id = ? AND state = 'bot_dispatching' AND lease_expires_at IS NOT NULL AND lease_expires_at <= ?`, now.UTC(), item.ID, now.UTC())
		if err != nil {
			return nil, err
		}
		affected, err := result.RowsAffected()
		if err != nil {
			return nil, err
		}
		if affected != 1 {
			return nil, ErrDiscordYouTubeLiveNotificationClaimLost
		}
		item.State = DiscordYouTubeLiveNotificationStateDeliveryUnknown
		item.LastError = "discord_dispatch_lease_expired_unknown"
		item.NextAttemptAt = nil
		item.UpdatedAt = now.UTC()
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return items, nil
}

// BeginDiscordYouTubeLiveNotificationBotDispatch persists a strict fence
// immediately before the Bot request. A caller must not make the external call
// until this succeeds; a later crash is recovered as delivery_unknown.
func (s MariaDBStreamStore) BeginDiscordYouTubeLiveNotificationBotDispatch(ctx context.Context, id, leaseToken string) (DiscordYouTubeLiveNotification, error) {
	if err := ctx.Err(); err != nil {
		return DiscordYouTubeLiveNotification{}, err
	}
	now := time.Now().UTC()
	result, err := s.db.ExecContext(ctx, `UPDATE stream_discord_youtube_live_notifications
SET state = 'bot_dispatching', dispatch_attempt_count = dispatch_attempt_count + 1, updated_at = ?
WHERE id = ? AND state = 'dispatching' AND lease_token_hash = ? AND lease_expires_at IS NOT NULL AND lease_expires_at > ?`, now, strings.TrimSpace(id), discordYouTubeLiveNotificationLeaseHash(leaseToken), now)
	if err != nil {
		return DiscordYouTubeLiveNotification{}, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return DiscordYouTubeLiveNotification{}, err
	}
	if affected != 1 {
		return DiscordYouTubeLiveNotification{}, ErrDiscordYouTubeLiveNotificationClaimLost
	}
	var notification DiscordYouTubeLiveNotification
	if err := scanDiscordYouTubeLiveNotification(s.db.QueryRowContext(ctx, discordYouTubeLiveNotificationSelect+` WHERE id = ?`, strings.TrimSpace(id)), &notification); err != nil {
		return DiscordYouTubeLiveNotification{}, err
	}
	return notification, nil
}

func (s MariaDBStreamStore) RecordDiscordYouTubeLiveNotificationRetry(ctx context.Context, id, leaseToken, state, lifecycleStatus, lastError string, nextAttemptAt time.Time) error {
	if !validDiscordYouTubeLiveNotificationRetryState(state) || strings.TrimSpace(leaseToken) == "" || nextAttemptAt.IsZero() {
		return ErrInvalidDiscordYouTubeLiveNotification
	}
	lifecycleStatus = normalizeDiscordYouTubeLiveNotificationCode(lifecycleStatus)
	lastError = normalizeDiscordYouTubeLiveNotificationCode(lastError)
	if strings.TrimSpace(lastError) == "" {
		return ErrInvalidDiscordYouTubeLiveNotification
	}
	return s.updateClaimedDiscordYouTubeLiveNotification(ctx, id, leaseToken, state, lifecycleStatus, lastError, nextAttemptAt, "", false, false)
}

func (s MariaDBStreamStore) MarkDiscordYouTubeLiveNotificationDelivered(ctx context.Context, id, leaseToken, lifecycleStatus, messageID string) error {
	messageID = strings.TrimSpace(messageID)
	lifecycleStatus = normalizeDiscordYouTubeLiveNotificationCode(lifecycleStatus)
	if messageID == "" || !validDiscordYouTubeLiveNotificationOpaqueValue(messageID) || strings.TrimSpace(leaseToken) == "" || lifecycleStatus == "" {
		return ErrInvalidDiscordYouTubeLiveNotification
	}
	return s.updateClaimedDiscordYouTubeLiveNotification(ctx, id, leaseToken, DiscordYouTubeLiveNotificationStateDelivered, lifecycleStatus, "", time.Time{}, messageID, true, true)
}

func (s MariaDBStreamStore) MarkDiscordYouTubeLiveNotificationDeliveryUnknown(ctx context.Context, id, leaseToken, lifecycleStatus, lastError string) error {
	lifecycleStatus = normalizeDiscordYouTubeLiveNotificationCode(lifecycleStatus)
	lastError = normalizeDiscordYouTubeLiveNotificationCode(lastError)
	if strings.TrimSpace(leaseToken) == "" || lastError == "" {
		return ErrInvalidDiscordYouTubeLiveNotification
	}
	return s.updateClaimedDiscordYouTubeLiveNotification(ctx, id, leaseToken, DiscordYouTubeLiveNotificationStateDeliveryUnknown, lifecycleStatus, lastError, time.Time{}, "", false, true)
}

func (s MariaDBStreamStore) MarkDiscordYouTubeLiveNotificationSuppressed(ctx context.Context, id, leaseToken, lifecycleStatus, lastError string) error {
	lifecycleStatus = normalizeDiscordYouTubeLiveNotificationCode(lifecycleStatus)
	lastError = normalizeDiscordYouTubeLiveNotificationCode(lastError)
	if strings.TrimSpace(leaseToken) == "" || lastError == "" {
		return ErrInvalidDiscordYouTubeLiveNotification
	}
	return s.updateClaimedDiscordYouTubeLiveNotification(ctx, id, leaseToken, DiscordYouTubeLiveNotificationStateSuppressed, lifecycleStatus, lastError, time.Time{}, "", false, false)
}

func (s MariaDBStreamStore) updateClaimedDiscordYouTubeLiveNotification(ctx context.Context, id, leaseToken, state, lifecycleStatus, lastError string, nextAttemptAt time.Time, messageID string, delivered, requireBotDispatch bool) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	now := time.Now().UTC()
	var nextAttempt any
	if !nextAttemptAt.IsZero() {
		nextAttempt = nextAttemptAt.UTC()
	}
	var deliveredAt any
	if delivered {
		deliveredAt = now
	}
	result, err := s.db.ExecContext(ctx, `UPDATE stream_discord_youtube_live_notifications
SET state = ?, lifecycle_status = ?, last_error = ?, next_attempt_at = ?, lease_token_hash = NULL, lease_expires_at = NULL,
discord_message_id = NULLIF(?, ''), delivered_at = ?, updated_at = ?
WHERE id = ? AND lease_token_hash = ? AND lease_expires_at IS NOT NULL AND lease_expires_at > ?
  AND (state = 'bot_dispatching' OR (? = FALSE AND state = 'dispatching'))`, state, lifecycleStatus, lastError, nextAttempt, messageID, deliveredAt, now, strings.TrimSpace(id), discordYouTubeLiveNotificationLeaseHash(leaseToken), now, requireBotDispatch)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected != 1 {
		return ErrDiscordYouTubeLiveNotificationClaimLost
	}
	return nil
}

func (s MariaDBStreamStore) CreateDiscordYouTubeLiveNotificationRecovery(ctx context.Context, streamID, priorNotificationID string) (DiscordYouTubeLiveNotification, error) {
	if err := ctx.Err(); err != nil {
		return DiscordYouTubeLiveNotification{}, err
	}
	streamID = strings.TrimSpace(streamID)
	priorNotificationID = strings.TrimSpace(priorNotificationID)
	if streamID == "" || priorNotificationID == "" {
		return DiscordYouTubeLiveNotification{}, ErrInvalidDiscordYouTubeLiveNotification
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return DiscordYouTubeLiveNotification{}, err
	}
	defer tx.Rollback()
	var streamStatus string
	if err := tx.QueryRowContext(ctx, `SELECT status FROM streams WHERE id = ? FOR UPDATE`, streamID).Scan(&streamStatus); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return DiscordYouTubeLiveNotification{}, ErrNotFound
		}
		return DiscordYouTubeLiveNotification{}, err
	}
	if !strings.EqualFold(strings.TrimSpace(streamStatus), "live") {
		return DiscordYouTubeLiveNotification{}, ErrDiscordYouTubeLiveNotificationRecoveryGone
	}
	var prior DiscordYouTubeLiveNotification
	err = scanDiscordYouTubeLiveNotification(tx.QueryRowContext(ctx, discordYouTubeLiveNotificationSelect+` WHERE id = ? AND stream_id = ? FOR UPDATE`, priorNotificationID, streamID), &prior)
	if errors.Is(err, sql.ErrNoRows) {
		return DiscordYouTubeLiveNotification{}, ErrNotFound
	}
	if err != nil {
		return DiscordYouTubeLiveNotification{}, err
	}
	if prior.State != DiscordYouTubeLiveNotificationStateDeliveryUnknown {
		return DiscordYouTubeLiveNotification{}, ErrDiscordYouTubeLiveNotificationRecoveryGone
	}
	var existing string
	err = tx.QueryRowContext(ctx, `SELECT id FROM stream_discord_youtube_live_notifications WHERE recovery_of_id = ?`, prior.ID).Scan(&existing)
	if err == nil {
		return DiscordYouTubeLiveNotification{}, ErrDiscordYouTubeLiveNotificationRecoveryUsed
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return DiscordYouTubeLiveNotification{}, err
	}
	now := time.Now().UTC()
	recovery := prior
	recovery.ID = ""
	recovery.EventID = ""
	recovery.RecoveryOfID = prior.ID
	recovery.State = initialDiscordYouTubeLiveNotificationState(prior.YouTubeMode)
	recovery, err = normalizeNewDiscordYouTubeLiveNotification(recovery, now)
	if err != nil {
		return DiscordYouTubeLiveNotification{}, err
	}
	if err := insertDiscordYouTubeLiveNotificationTx(ctx, tx, recovery); err != nil {
		return DiscordYouTubeLiveNotification{}, err
	}
	if err := tx.Commit(); err != nil {
		return DiscordYouTubeLiveNotification{}, err
	}
	return recovery, nil
}

func (s MariaDBStreamStore) GetLatestDiscordYouTubeLiveNotification(ctx context.Context, streamID string) (DiscordYouTubeLiveNotification, error) {
	var notification DiscordYouTubeLiveNotification
	err := scanDiscordYouTubeLiveNotification(s.db.QueryRowContext(ctx, discordYouTubeLiveNotificationSelect+` WHERE stream_id = ? ORDER BY created_at DESC, id DESC LIMIT 1`, strings.TrimSpace(streamID)), &notification)
	if errors.Is(err, sql.ErrNoRows) {
		return DiscordYouTubeLiveNotification{}, ErrNotFound
	}
	return notification, err
}

const discordYouTubeLiveNotificationSelect = `SELECT id, stream_id, event_id, watch_url, discord_service_id, discord_text_channel_id,
youtube_mode, COALESCE(youtube_oauth_account_id, ''), COALESCE(youtube_broadcast_id, ''), lifecycle_status,
 state, attempt_count, dispatch_attempt_count, next_attempt_at, last_error, COALESCE(discord_message_id, ''), delivered_at,
COALESCE(recovery_of_id, ''), created_at, updated_at
FROM stream_discord_youtube_live_notifications`

type discordYouTubeLiveNotificationScanner interface {
	Scan(dest ...any) error
}

func scanDiscordYouTubeLiveNotification(scanner discordYouTubeLiveNotificationScanner, notification *DiscordYouTubeLiveNotification) error {
	var nextAttemptAt sql.NullTime
	var deliveredAt sql.NullTime
	err := scanner.Scan(&notification.ID, &notification.StreamID, &notification.EventID, &notification.WatchURL, &notification.DiscordServiceID, &notification.DiscordTextChannelID,
		&notification.YouTubeMode, &notification.YouTubeOAuthAccountID, &notification.YouTubeBroadcastID, &notification.LifecycleStatus,
		&notification.State, &notification.AttemptCount, &notification.DispatchAttemptCount, &nextAttemptAt, &notification.LastError, &notification.DiscordMessageID, &deliveredAt,
		&notification.RecoveryOfID, &notification.CreatedAt, &notification.UpdatedAt)
	if err != nil {
		return err
	}
	if nextAttemptAt.Valid {
		notification.NextAttemptAt = timePtrUTC(nextAttemptAt.Time)
	}
	if deliveredAt.Valid {
		notification.DeliveredAt = timePtrUTC(deliveredAt.Time)
	}
	return nil
}

func (s *MemoryStreamStore) TransitionStreamStatusAndEnqueueDiscordYouTubeLiveNotification(ctx context.Context, streamID, expectedStatus, status string, notification DiscordYouTubeLiveNotification) (Stream, DiscordYouTubeLiveNotification, bool, error) {
	if err := ctx.Err(); err != nil {
		return Stream{}, DiscordYouTubeLiveNotification{}, false, err
	}
	now := time.Now().UTC()
	notification.StreamID = strings.TrimSpace(streamID)
	notification, err := normalizeNewDiscordYouTubeLiveNotification(notification, now)
	if err != nil {
		return Stream{}, DiscordYouTubeLiveNotification{}, false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	stream, ok := s.streams[notification.StreamID]
	if !ok {
		return Stream{}, DiscordYouTubeLiveNotification{}, false, ErrNotFound
	}
	if !strings.EqualFold(strings.TrimSpace(stream.Status), strings.TrimSpace(expectedStatus)) {
		return stream, DiscordYouTubeLiveNotification{}, false, nil
	}
	if s.discordYouTubeLiveNotifications == nil {
		s.discordYouTubeLiveNotifications = map[string]DiscordYouTubeLiveNotification{}
	}
	if s.discordYouTubeLiveNotificationEvents == nil {
		s.discordYouTubeLiveNotificationEvents = map[string]string{}
	}
	if s.discordYouTubeLiveNotificationLeases == nil {
		s.discordYouTubeLiveNotificationLeases = map[string]string{}
	}
	if s.discordYouTubeLiveNotificationLeaseExpires == nil {
		s.discordYouTubeLiveNotificationLeaseExpires = map[string]time.Time{}
	}
	if _, exists := s.discordYouTubeLiveNotificationEvents[notification.EventID]; exists {
		return Stream{}, DiscordYouTubeLiveNotification{}, false, ErrInvalidDiscordYouTubeLiveNotification
	}
	stream.Status = status
	stream.UpdatedAt = now
	s.streams[stream.ID] = stream
	s.discordYouTubeLiveNotifications[notification.ID] = notification
	s.discordYouTubeLiveNotificationEvents[notification.EventID] = notification.ID
	return stream, notification, true, nil
}

func (s *MemoryStreamStore) ClaimDueDiscordYouTubeLiveNotifications(ctx context.Context, now time.Time, leaseTTL time.Duration, limit int) ([]DiscordYouTubeLiveNotificationClaim, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	leaseTTL = normalizeDiscordYouTubeLiveNotificationLeaseTTL(leaseTTL)
	limit = normalizeDiscordYouTubeLiveNotificationLimit(limit)
	s.mu.Lock()
	defer s.mu.Unlock()
	items := make([]DiscordYouTubeLiveNotification, 0)
	for _, item := range s.discordYouTubeLiveNotifications {
		if (validDiscordYouTubeLiveNotificationRetryState(item.State) && item.NextAttemptAt != nil && !item.NextAttemptAt.After(now)) ||
			(item.State == DiscordYouTubeLiveNotificationStateDispatching && !s.discordYouTubeLiveNotificationLeaseExpires[item.ID].After(now)) {
			items = append(items, item)
		}
	}
	sort.Slice(items, func(left, right int) bool {
		leftAt := time.Time{}
		if items[left].NextAttemptAt != nil {
			leftAt = *items[left].NextAttemptAt
		}
		rightAt := time.Time{}
		if items[right].NextAttemptAt != nil {
			rightAt = *items[right].NextAttemptAt
		}
		if leftAt.Equal(rightAt) {
			return items[left].CreatedAt.Before(items[right].CreatedAt)
		}
		return leftAt.Before(rightAt)
	})
	if len(items) > limit {
		items = items[:limit]
	}
	claims := make([]DiscordYouTubeLiveNotificationClaim, 0, len(items))
	for _, item := range items {
		leaseToken := newUUID()
		item.State = DiscordYouTubeLiveNotificationStateDispatching
		item.AttemptCount++
		item.UpdatedAt = now.UTC()
		s.discordYouTubeLiveNotifications[item.ID] = item
		claims = append(claims, DiscordYouTubeLiveNotificationClaim{Notification: item, LeaseToken: leaseToken})
		// The Memory store keeps a hash only to exercise the same claim boundary.
		s.discordYouTubeLiveNotificationLeases[item.ID] = discordYouTubeLiveNotificationLeaseHash(leaseToken)
		s.discordYouTubeLiveNotificationLeaseExpires[item.ID] = now.Add(leaseTTL).UTC()
	}
	return claims, nil
}

func (s *MemoryStreamStore) FenceExpiredDiscordYouTubeLiveNotificationDispatches(ctx context.Context, now time.Time, limit int) ([]DiscordYouTubeLiveNotification, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	limit = normalizeDiscordYouTubeLiveNotificationLimit(limit)
	s.mu.Lock()
	defer s.mu.Unlock()
	items := make([]DiscordYouTubeLiveNotification, 0)
	for _, notification := range s.discordYouTubeLiveNotifications {
		if notification.State == DiscordYouTubeLiveNotificationStateBotDispatching && !s.discordYouTubeLiveNotificationLeaseExpires[notification.ID].After(now) {
			items = append(items, notification)
		}
	}
	sort.Slice(items, func(left, right int) bool {
		return items[left].CreatedAt.Before(items[right].CreatedAt)
	})
	if len(items) > limit {
		items = items[:limit]
	}
	for index := range items {
		item := &items[index]
		item.State = DiscordYouTubeLiveNotificationStateDeliveryUnknown
		item.LastError = "discord_dispatch_lease_expired_unknown"
		item.NextAttemptAt = nil
		item.UpdatedAt = now.UTC()
		delete(s.discordYouTubeLiveNotificationLeases, item.ID)
		delete(s.discordYouTubeLiveNotificationLeaseExpires, item.ID)
		s.discordYouTubeLiveNotifications[item.ID] = *item
	}
	return items, nil
}

func (s *MemoryStreamStore) BeginDiscordYouTubeLiveNotificationBotDispatch(ctx context.Context, id, leaseToken string) (DiscordYouTubeLiveNotification, error) {
	if err := ctx.Err(); err != nil {
		return DiscordYouTubeLiveNotification{}, err
	}
	now := time.Now().UTC()
	s.mu.Lock()
	defer s.mu.Unlock()
	notification, ok := s.discordYouTubeLiveNotifications[strings.TrimSpace(id)]
	if !ok || notification.State != DiscordYouTubeLiveNotificationStateDispatching ||
		s.discordYouTubeLiveNotificationLeases[notification.ID] != discordYouTubeLiveNotificationLeaseHash(leaseToken) ||
		!s.discordYouTubeLiveNotificationLeaseExpires[notification.ID].After(now) {
		return DiscordYouTubeLiveNotification{}, ErrDiscordYouTubeLiveNotificationClaimLost
	}
	notification.State = DiscordYouTubeLiveNotificationStateBotDispatching
	notification.DispatchAttemptCount++
	notification.UpdatedAt = now
	s.discordYouTubeLiveNotifications[notification.ID] = notification
	return notification, nil
}

func (s *MemoryStreamStore) RecordDiscordYouTubeLiveNotificationRetry(ctx context.Context, id, leaseToken, state, lifecycleStatus, lastError string, nextAttemptAt time.Time) error {
	if !validDiscordYouTubeLiveNotificationRetryState(state) || nextAttemptAt.IsZero() {
		return ErrInvalidDiscordYouTubeLiveNotification
	}
	return s.updateClaimedMemoryDiscordYouTubeLiveNotification(ctx, id, leaseToken, state, lifecycleStatus, lastError, nextAttemptAt, "", false, false)
}

func (s *MemoryStreamStore) MarkDiscordYouTubeLiveNotificationDelivered(ctx context.Context, id, leaseToken, lifecycleStatus, messageID string) error {
	lifecycleStatus = normalizeDiscordYouTubeLiveNotificationCode(lifecycleStatus)
	if strings.TrimSpace(messageID) == "" || !validDiscordYouTubeLiveNotificationOpaqueValue(strings.TrimSpace(messageID)) || lifecycleStatus == "" {
		return ErrInvalidDiscordYouTubeLiveNotification
	}
	return s.updateClaimedMemoryDiscordYouTubeLiveNotification(ctx, id, leaseToken, DiscordYouTubeLiveNotificationStateDelivered, lifecycleStatus, "", time.Time{}, messageID, true, true)
}

func (s *MemoryStreamStore) MarkDiscordYouTubeLiveNotificationDeliveryUnknown(ctx context.Context, id, leaseToken, lifecycleStatus, lastError string) error {
	return s.updateClaimedMemoryDiscordYouTubeLiveNotification(ctx, id, leaseToken, DiscordYouTubeLiveNotificationStateDeliveryUnknown, lifecycleStatus, lastError, time.Time{}, "", false, true)
}

func (s *MemoryStreamStore) MarkDiscordYouTubeLiveNotificationSuppressed(ctx context.Context, id, leaseToken, lifecycleStatus, lastError string) error {
	return s.updateClaimedMemoryDiscordYouTubeLiveNotification(ctx, id, leaseToken, DiscordYouTubeLiveNotificationStateSuppressed, lifecycleStatus, lastError, time.Time{}, "", false, false)
}

func (s *MemoryStreamStore) updateClaimedMemoryDiscordYouTubeLiveNotification(ctx context.Context, id, leaseToken, state, lifecycleStatus, lastError string, nextAttemptAt time.Time, messageID string, delivered, requireBotDispatch bool) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	lifecycleStatus = normalizeDiscordYouTubeLiveNotificationCode(lifecycleStatus)
	lastError = normalizeDiscordYouTubeLiveNotificationCode(lastError)
	if strings.TrimSpace(leaseToken) == "" || (state != DiscordYouTubeLiveNotificationStateDelivered && lastError == "") {
		return ErrInvalidDiscordYouTubeLiveNotification
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	notification, ok := s.discordYouTubeLiveNotifications[strings.TrimSpace(id)]
	if !ok || (notification.State != DiscordYouTubeLiveNotificationStateDispatching && notification.State != DiscordYouTubeLiveNotificationStateBotDispatching) ||
		(requireBotDispatch && notification.State != DiscordYouTubeLiveNotificationStateBotDispatching) ||
		s.discordYouTubeLiveNotificationLeases[notification.ID] != discordYouTubeLiveNotificationLeaseHash(leaseToken) ||
		!s.discordYouTubeLiveNotificationLeaseExpires[notification.ID].After(time.Now().UTC()) {
		return ErrDiscordYouTubeLiveNotificationClaimLost
	}
	notification.State = state
	notification.LifecycleStatus = lifecycleStatus
	notification.LastError = lastError
	notification.NextAttemptAt = timePtrUTC(nextAttemptAt)
	notification.DiscordMessageID = strings.TrimSpace(messageID)
	notification.UpdatedAt = time.Now().UTC()
	if delivered {
		notification.DeliveredAt = timePtrUTC(notification.UpdatedAt)
	}
	delete(s.discordYouTubeLiveNotificationLeases, notification.ID)
	delete(s.discordYouTubeLiveNotificationLeaseExpires, notification.ID)
	s.discordYouTubeLiveNotifications[notification.ID] = notification
	return nil
}

func (s *MemoryStreamStore) CreateDiscordYouTubeLiveNotificationRecovery(ctx context.Context, streamID, priorNotificationID string) (DiscordYouTubeLiveNotification, error) {
	if err := ctx.Err(); err != nil {
		return DiscordYouTubeLiveNotification{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	stream, ok := s.streams[strings.TrimSpace(streamID)]
	if !ok {
		return DiscordYouTubeLiveNotification{}, ErrNotFound
	}
	if !strings.EqualFold(strings.TrimSpace(stream.Status), "live") {
		return DiscordYouTubeLiveNotification{}, ErrDiscordYouTubeLiveNotificationRecoveryGone
	}
	prior, ok := s.discordYouTubeLiveNotifications[strings.TrimSpace(priorNotificationID)]
	if !ok || prior.StreamID != stream.ID {
		return DiscordYouTubeLiveNotification{}, ErrNotFound
	}
	if prior.State != DiscordYouTubeLiveNotificationStateDeliveryUnknown {
		return DiscordYouTubeLiveNotification{}, ErrDiscordYouTubeLiveNotificationRecoveryGone
	}
	for _, existing := range s.discordYouTubeLiveNotifications {
		if existing.RecoveryOfID == prior.ID {
			return DiscordYouTubeLiveNotification{}, ErrDiscordYouTubeLiveNotificationRecoveryUsed
		}
	}
	now := time.Now().UTC()
	recovery := prior
	recovery.ID = ""
	recovery.EventID = ""
	recovery.RecoveryOfID = prior.ID
	recovery.State = initialDiscordYouTubeLiveNotificationState(prior.YouTubeMode)
	var err error
	recovery, err = normalizeNewDiscordYouTubeLiveNotification(recovery, now)
	if err != nil {
		return DiscordYouTubeLiveNotification{}, err
	}
	if s.discordYouTubeLiveNotificationEvents == nil {
		s.discordYouTubeLiveNotificationEvents = map[string]string{}
	}
	s.discordYouTubeLiveNotificationEvents[recovery.EventID] = recovery.ID
	s.discordYouTubeLiveNotifications[recovery.ID] = recovery
	return recovery, nil
}

func (s *MemoryStreamStore) GetLatestDiscordYouTubeLiveNotification(ctx context.Context, streamID string) (DiscordYouTubeLiveNotification, error) {
	if err := ctx.Err(); err != nil {
		return DiscordYouTubeLiveNotification{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var latest DiscordYouTubeLiveNotification
	found := false
	for _, notification := range s.discordYouTubeLiveNotifications {
		if notification.StreamID != strings.TrimSpace(streamID) || (found && !notification.CreatedAt.After(latest.CreatedAt)) {
			continue
		}
		latest = notification
		found = true
	}
	if !found {
		return DiscordYouTubeLiveNotification{}, ErrNotFound
	}
	return latest, nil
}

var _ StreamDiscordYouTubeLiveNotificationStore = (*MemoryStreamStore)(nil)
var _ StreamDiscordYouTubeLiveNotificationStore = (*MariaDBStreamStore)(nil)
