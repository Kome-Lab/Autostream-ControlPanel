package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/example/autostream-control-panel/internal/security"
	"github.com/example/autostream-control-panel/internal/servicecall"
	"github.com/example/autostream-control-panel/internal/store"
	ytlive "github.com/example/autostream-control-panel/internal/youtube"
)

const (
	discordYouTubeLiveNotificationRetryDefaultInterval = 15 * time.Second
	// A single claim has a bounded 75 second work deadline: one 25 second
	// provider lookup, one 25 second Bot call, and persistence headroom. The
	// durable lease is longer (two minutes) so another worker cannot reclaim
	// during either external operation.
	discordYouTubeLiveNotificationClaimTTL           = 2 * time.Minute
	discordYouTubeLiveNotificationClaimWorkTimeout   = 75 * time.Second
	discordYouTubeLiveNotificationLifecycleTimeout   = 25 * time.Second
	discordYouTubeLiveNotificationDispatchTimeout    = 25 * time.Second
	discordYouTubeLiveNotificationLifecyclePollDelay = 15 * time.Second
	// Claims are processed synchronously because the same durable lease covers
	// provider lookup, Bot dispatch, and receipt persistence. Taking more than
	// one at a time would let a later item outlive its lease before its first
	// external call. The 15-second loop drains backlog safely.
	discordYouTubeLiveNotificationClaimBatchSize = 1
)

// discordYouTubeLiveNotificationView intentionally contains only durable
// non-secret operational fields. The event id, OAuth credentials, stream key,
// and raw dispatch errors are never returned by the operator API.
type discordYouTubeLiveNotificationView struct {
	ID                   string     `json:"id"`
	StreamID             string     `json:"stream_id"`
	WatchURL             string     `json:"watch_url"`
	DiscordServiceID     string     `json:"discord_service_id"`
	DiscordTextChannelID string     `json:"discord_text_channel_id"`
	YouTubeMode          string     `json:"youtube_mode"`
	YouTubeBroadcastID   string     `json:"youtube_broadcast_id,omitempty"`
	LifecycleStatus      string     `json:"lifecycle_status,omitempty"`
	State                string     `json:"state"`
	AttemptCount         int        `json:"attempt_count"`
	DispatchAttemptCount int        `json:"dispatch_attempt_count"`
	NextAttemptAt        *time.Time `json:"next_attempt_at,omitempty"`
	LastError            string     `json:"last_error,omitempty"`
	DiscordMessageID     string     `json:"discord_message_id,omitempty"`
	DeliveredAt          *time.Time `json:"delivered_at,omitempty"`
	RecoveryOfID         string     `json:"recovery_of_id,omitempty"`
	CreatedAt            time.Time  `json:"created_at"`
	UpdatedAt            time.Time  `json:"updated_at"`
}

func safeDiscordYouTubeLiveNotification(notification store.DiscordYouTubeLiveNotification) discordYouTubeLiveNotificationView {
	return discordYouTubeLiveNotificationView{
		ID:                   notification.ID,
		StreamID:             notification.StreamID,
		WatchURL:             notification.WatchURL,
		DiscordServiceID:     notification.DiscordServiceID,
		DiscordTextChannelID: notification.DiscordTextChannelID,
		YouTubeMode:          notification.YouTubeMode,
		YouTubeBroadcastID:   notification.YouTubeBroadcastID,
		LifecycleStatus:      notification.LifecycleStatus,
		State:                notification.State,
		AttemptCount:         notification.AttemptCount,
		DispatchAttemptCount: notification.DispatchAttemptCount,
		NextAttemptAt:        notification.NextAttemptAt,
		LastError:            notification.LastError,
		DiscordMessageID:     notification.DiscordMessageID,
		DeliveredAt:          notification.DeliveredAt,
		RecoveryOfID:         notification.RecoveryOfID,
		CreatedAt:            notification.CreatedAt,
		UpdatedAt:            notification.UpdatedAt,
	}
}

// discordYouTubeLiveNotificationForStart decides whether this stream actually
// has a public YouTube URL and a text target to announce. It deliberately does
// not fabricate a target for a stream without a configured Discord text
// channel.
func discordYouTubeLiveNotificationForStart(stream store.Stream, assignments []store.RegisteredService, req servicecall.StartRequest) (store.DiscordYouTubeLiveNotification, bool) {
	watchURL, ok := normalizeYouTubeWatchURL(mapString(req.YouTubeRuntime, "watch_url"))
	if !ok || mapBool(req.YouTubeRuntime, "dry_run") || strings.TrimSpace(req.DiscordTextChannelID) == "" {
		return store.DiscordYouTubeLiveNotification{}, false
	}
	mode := strings.TrimSpace(mapString(req.YouTubeRuntime, "mode"))
	switch mode {
	case "stream_key", "legacy_stream_key", "live_api", "live_api_relay_static":
	default:
		return store.DiscordYouTubeLiveNotification{}, false
	}
	serviceID := primaryServiceID(primaryStreamAssignments(assignments), "discord_bot")
	if serviceID == "" {
		// A start request requires a primary bot before dispatch, but keep this
		// defensive guard so an unassigned service can never be mistaken for a
		// delivered notification.
		return store.DiscordYouTubeLiveNotification{}, false
	}
	return store.DiscordYouTubeLiveNotification{
		StreamID:              stream.ID,
		WatchURL:              watchURL,
		DiscordServiceID:      serviceID,
		DiscordTextChannelID:  strings.TrimSpace(req.DiscordTextChannelID),
		YouTubeMode:           mode,
		YouTubeOAuthAccountID: strings.TrimSpace(mapString(req.YouTubeRuntime, "oauth_account_id")),
		YouTubeBroadcastID:    strings.TrimSpace(mapString(req.YouTubeRuntime, "broadcast_id")),
	}, true
}

func discordYouTubeLiveNotificationAuditMetadata(notification store.DiscordYouTubeLiveNotification) map[string]any {
	return map[string]any{
		"notification_id":         notification.ID,
		"event_id_fingerprint":    security.SecretFingerprint(notification.EventID),
		"watch_url_fingerprint":   security.SecretFingerprint(notification.WatchURL),
		"discord_service_id":      notification.DiscordServiceID,
		"discord_text_channel_id": notification.DiscordTextChannelID,
		"youtube_mode":            notification.YouTubeMode,
		"youtube_broadcast_id":    notification.YouTubeBroadcastID,
		"lifecycle_status":        notification.LifecycleStatus,
		"state":                   notification.State,
		"attempt_count":           notification.AttemptCount,
		"dispatch_attempt_count":  notification.DispatchAttemptCount,
	}
}

func discordYouTubeLiveNotificationDispatchRetryDelay(attempt int) time.Duration {
	switch {
	case attempt <= 1:
		return 15 * time.Second
	case attempt == 2:
		return 30 * time.Second
	case attempt == 3:
		return time.Minute
	case attempt == 4:
		return 2 * time.Minute
	case attempt == 5:
		return 5 * time.Minute
	case attempt == 6:
		return 10 * time.Minute
	case attempt == 7:
		return 30 * time.Minute
	default:
		return time.Hour
	}
}

// DispatchDueDiscordYouTubeLiveNotifications is restart-safe. It claims rows
// under a short durable lease, checks provider lifecycle before any Bot call,
// and treats an unconfirmed HTTP outcome as delivery_unknown rather than
// automatically producing a potentially duplicate Discord post.
func (s *Server) DispatchDueDiscordYouTubeLiveNotifications(ctx context.Context, limit int) (map[string]any, error) {
	result := map[string]any{"claimed": 0, "delivered": 0, "retry_scheduled": 0, "delivery_unknown": 0, "suppressed": 0}
	outbox, ok := s.streams.(store.StreamDiscordYouTubeLiveNotificationStore)
	if !ok {
		return result, nil
	}
	now := time.Now().UTC()
	fenced, err := outbox.FenceExpiredDiscordYouTubeLiveNotificationDispatches(ctx, now, limit)
	if err != nil {
		return nil, err
	}
	for _, notification := range fenced {
		metadata := discordYouTubeLiveNotificationAuditMetadata(notification)
		metadata["state"] = store.DiscordYouTubeLiveNotificationStateDeliveryUnknown
		metadata["reason"] = notification.LastError
		s.writeSystemAudit(ctx, store.AuditEvent{Action: "streams.discord_youtube_notify", ResourceType: "stream", ResourceID: notification.StreamID, Result: "failure", Metadata: metadata})
	}
	result["delivery_unknown"] = len(fenced)
	claims, err := outbox.ClaimDueDiscordYouTubeLiveNotifications(ctx, now, discordYouTubeLiveNotificationClaimTTL, discordYouTubeLiveNotificationClaimBatchSize)
	if err != nil {
		return nil, err
	}
	result["claimed"] = len(claims)
	for _, claim := range claims {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		claimCtx, cancel := context.WithTimeout(ctx, discordYouTubeLiveNotificationClaimWorkTimeout)
		outcome := s.dispatchClaimedDiscordYouTubeLiveNotification(claimCtx, outbox, claim)
		cancel()
		if _, exists := result[outcome]; exists {
			result[outcome] = result[outcome].(int) + 1
		}
	}
	return result, nil
}

func (s *Server) dispatchClaimedDiscordYouTubeLiveNotification(ctx context.Context, outbox store.StreamDiscordYouTubeLiveNotificationStore, claim store.DiscordYouTubeLiveNotificationClaim) string {
	notification := claim.Notification
	stream, err := s.streams.GetStream(ctx, notification.StreamID)
	if errors.Is(err, store.ErrNotFound) {
		return s.suppressClaimedDiscordYouTubeLiveNotification(ctx, outbox, claim, notification.LifecycleStatus, "stream_not_live")
	}
	if err != nil {
		return s.retryClaimedDiscordYouTubeLiveNotification(ctx, outbox, claim, store.DiscordYouTubeLiveNotificationStateDispatchPending, notification.LifecycleStatus, "stream_lookup_failed")
	}
	if !strings.EqualFold(strings.TrimSpace(stream.Status), "live") {
		return s.suppressClaimedDiscordYouTubeLiveNotification(ctx, outbox, claim, notification.LifecycleStatus, "stream_not_live")
	}

	lifecycleStatus, ready, terminal, lifecycleCode := s.discordYouTubeLiveNotificationLifecycle(ctx, notification)
	if terminal {
		return s.suppressClaimedDiscordYouTubeLiveNotification(ctx, outbox, claim, lifecycleStatus, lifecycleCode)
	}
	if !ready {
		return s.retryClaimedDiscordYouTubeLiveNotification(ctx, outbox, claim, store.DiscordYouTubeLiveNotificationStateAwaitingYouTubeLive, lifecycleStatus, lifecycleCode)
	}

	assignments, err := s.streamAssignments(ctx, notification.StreamID)
	if err != nil {
		return s.retryClaimedDiscordYouTubeLiveNotification(ctx, outbox, claim, store.DiscordYouTubeLiveNotificationStateDispatchPending, lifecycleStatus, "discord_assignment_lookup_failed")
	}
	assignedBot := discordYouTubeLiveNotificationAssignedBot(primaryStreamAssignments(assignments), notification.DiscordServiceID)
	if len(assignedBot) == 0 {
		return s.retryClaimedDiscordYouTubeLiveNotification(ctx, outbox, claim, store.DiscordYouTubeLiveNotificationStateDispatchPending, lifecycleStatus, "discord_bot_target_unavailable")
	}
	notifier, ok := s.dispatcher.(discordLiveNotificationDispatcher)
	if !ok {
		return s.retryClaimedDiscordYouTubeLiveNotification(ctx, outbox, claim, store.DiscordYouTubeLiveNotificationStateDispatchPending, lifecycleStatus, "discord_notification_dispatcher_unavailable")
	}
	// Persist the external side-effect fence before the Bot request. From this
	// point a crash or receipt-save failure is delivery_unknown, never an
	// automatic retry of this event ID.
	begun, err := outbox.BeginDiscordYouTubeLiveNotificationBotDispatch(ctx, notification.ID, claim.LeaseToken)
	if err != nil {
		log.Printf("discord youtube notification bot-dispatch fence failed: notification_id=%s error=%v", notification.ID, err)
		return ""
	}
	notification = begun
	claim.Notification = begun

	dispatchCtx, cancel := context.WithTimeout(ctx, discordYouTubeLiveNotificationDispatchTimeout)
	defer cancel()
	dispatch := notifier.NotifyDiscordYouTubeLive(dispatchCtx, stream, assignedBot, notification.EventID, notification.WatchURL)
	dispatch = sanitizeDispatchResults([]servicecall.DispatchResult{dispatch})[0]
	if dispatch.Success && strings.TrimSpace(dispatch.MessageID) != "" {
		if err := outbox.MarkDiscordYouTubeLiveNotificationDelivered(ctx, notification.ID, claim.LeaseToken, lifecycleStatus, dispatch.MessageID); err != nil {
			log.Printf("discord youtube notification delivery receipt save failed: notification_id=%s error=%v", notification.ID, err)
			return ""
		}
		metadata := discordYouTubeLiveNotificationAuditMetadata(notification)
		metadata["state"] = store.DiscordYouTubeLiveNotificationStateDelivered
		metadata["discord_message_id_fingerprint"] = security.SecretFingerprint(dispatch.MessageID)
		metadata["already_sent"] = dispatch.AlreadySent
		s.writeSystemAudit(ctx, store.AuditEvent{Action: "streams.discord_youtube_notify", ResourceType: "stream", ResourceID: notification.StreamID, Result: "success", Metadata: metadata})
		return "delivered"
	}
	if dispatch.Success {
		return s.deliveryUnknownClaimedDiscordYouTubeLiveNotification(ctx, outbox, claim, lifecycleStatus, "discord_delivery_receipt_missing", dispatch)
	}
	if discordYouTubeLiveNotificationConfirmedRetryable(dispatch) {
		return s.retryClaimedDiscordYouTubeLiveNotification(ctx, outbox, claim, store.DiscordYouTubeLiveNotificationStateDispatchPending, lifecycleStatus, discordYouTubeLiveNotificationDispatchCode(dispatch))
	}
	if dispatch.StatusCode == 0 || dispatch.StatusCode >= http.StatusInternalServerError || strings.EqualFold(strings.TrimSpace(dispatch.FailurePhase), "transport") {
		return s.deliveryUnknownClaimedDiscordYouTubeLiveNotification(ctx, outbox, claim, lifecycleStatus, discordYouTubeLiveNotificationDispatchCode(dispatch), dispatch)
	}
	return s.suppressClaimedDiscordYouTubeLiveNotification(ctx, outbox, claim, lifecycleStatus, discordYouTubeLiveNotificationDispatchCode(dispatch))
}

// discordYouTubeLiveNotificationLifecycle returns ready only after a provider
// `live` response. stream_key is the explicit legacy exception: it has no
// Broadcast identity and is retained as legacy_unverified rather than falsely
// claiming provider confirmation.
func (s *Server) discordYouTubeLiveNotificationLifecycle(ctx context.Context, notification store.DiscordYouTubeLiveNotification) (lifecycle string, ready, terminal bool, code string) {
	if notification.YouTubeMode == "stream_key" || notification.YouTubeMode == "legacy_stream_key" {
		return "legacy_unverified", true, false, ""
	}
	lifecycleClient, ok := s.youtubeLive.(ytlive.BroadcastLifecycleClient)
	if !ok || lifecycleClient == nil {
		return notification.LifecycleStatus, false, false, "youtube_lifecycle_client_unavailable"
	}
	credentials, err := s.youtubeOAuthCredentials(ctx, notification.YouTubeOAuthAccountID)
	if err != nil {
		if errors.Is(err, errYouTubeOAuthAccountUnavailable) {
			return notification.LifecycleStatus, false, false, errYouTubeOAuthAccountUnavailable.Error()
		}
		return notification.LifecycleStatus, false, false, "youtube_lifecycle_credentials_unavailable"
	}
	lookupCtx, cancel := context.WithTimeout(ctx, discordYouTubeLiveNotificationLifecycleTimeout)
	defer cancel()
	lifecycle, err = lifecycleClient.BroadcastLifecycle(lookupCtx, ytlive.BroadcastLifecycleRequest{Credentials: credentials, BroadcastID: notification.YouTubeBroadcastID})
	if err != nil {
		if errors.Is(err, ytlive.ErrBroadcastNotFound) {
			return notification.LifecycleStatus, false, true, ytlive.ErrBroadcastNotFound.Error()
		}
		return notification.LifecycleStatus, false, false, "youtube_broadcast_lifecycle_unavailable"
	}
	lifecycle = strings.ToLower(strings.TrimSpace(lifecycle))
	switch lifecycle {
	case "live":
		return lifecycle, true, false, ""
	case "complete", "revoked":
		return lifecycle, false, true, "youtube_lifecycle_" + lifecycle
	case "":
		return notification.LifecycleStatus, false, false, "youtube_broadcast_lifecycle_unavailable"
	default:
		return lifecycle, false, false, "youtube_lifecycle_" + lifecycle
	}
}

func discordYouTubeLiveNotificationAssignedBot(assignments []store.RegisteredService, serviceID string) []store.RegisteredService {
	for _, assignment := range assignments {
		if assignment.ServiceType == "discord_bot" && strings.TrimSpace(assignment.ServiceID) == strings.TrimSpace(serviceID) {
			return []store.RegisteredService{assignment}
		}
	}
	return nil
}

// Only these codes are explicit, pre-send receipts from the existing Bot
// contract. A generic retryable bit or a generic 5xx cannot prove that Discord
// did not accept the post, so they intentionally do not enter this branch.
func discordYouTubeLiveNotificationConfirmedRetryable(result servicecall.DispatchResult) bool {
	if strings.EqualFold(strings.TrimSpace(result.FailurePhase), "pre_dispatch") || result.StatusCode == http.StatusTooManyRequests {
		return true
	}
	switch strings.TrimSpace(result.Code) {
	case "runtime_config_unavailable", "runtime_config_fetch_failed", "notification_capacity_reached", "live_job_not_active", "discord_config_not_found", "discord_rate_limited":
		return true
	default:
		return false
	}
}

func discordYouTubeLiveNotificationDispatchCode(result servicecall.DispatchResult) string {
	code := strings.TrimSpace(strings.ToLower(result.Code))
	if code != "" {
		return code
	}
	if strings.EqualFold(strings.TrimSpace(result.FailurePhase), "transport") {
		return "discord_delivery_transport_unknown"
	}
	if result.StatusCode >= http.StatusInternalServerError {
		return "discord_delivery_server_error_unknown"
	}
	if result.StatusCode == http.StatusTooManyRequests {
		return "discord_rate_limited"
	}
	if result.StatusCode == 0 {
		return "discord_delivery_unknown"
	}
	return "discord_notification_rejected"
}

func (s *Server) retryClaimedDiscordYouTubeLiveNotification(ctx context.Context, outbox store.StreamDiscordYouTubeLiveNotificationStore, claim store.DiscordYouTubeLiveNotificationClaim, state, lifecycleStatus, code string) string {
	notification := claim.Notification
	delay := discordYouTubeLiveNotificationLifecyclePollDelay
	if state == store.DiscordYouTubeLiveNotificationStateDispatchPending && notification.DispatchAttemptCount > 0 {
		delay = discordYouTubeLiveNotificationDispatchRetryDelay(notification.DispatchAttemptCount)
	}
	nextAttemptAt := time.Now().UTC().Add(delay)
	if err := outbox.RecordDiscordYouTubeLiveNotificationRetry(ctx, notification.ID, claim.LeaseToken, state, lifecycleStatus, code, nextAttemptAt); err != nil {
		log.Printf("discord youtube notification retry save failed: notification_id=%s error=%v", notification.ID, err)
		return ""
	}
	metadata := discordYouTubeLiveNotificationAuditMetadata(notification)
	metadata["state"] = state
	metadata["lifecycle_status"] = lifecycleStatus
	metadata["reason"] = code
	metadata["next_attempt_at"] = nextAttemptAt
	s.writeSystemAudit(ctx, store.AuditEvent{Action: "streams.discord_youtube_notify", ResourceType: "stream", ResourceID: notification.StreamID, Result: "pending", Metadata: metadata})
	return "retry_scheduled"
}

func (s *Server) deliveryUnknownClaimedDiscordYouTubeLiveNotification(ctx context.Context, outbox store.StreamDiscordYouTubeLiveNotificationStore, claim store.DiscordYouTubeLiveNotificationClaim, lifecycleStatus, code string, dispatch servicecall.DispatchResult) string {
	notification := claim.Notification
	if err := outbox.MarkDiscordYouTubeLiveNotificationDeliveryUnknown(ctx, notification.ID, claim.LeaseToken, lifecycleStatus, code); err != nil {
		log.Printf("discord youtube notification unknown-delivery save failed: notification_id=%s error=%v", notification.ID, err)
		return ""
	}
	metadata := discordYouTubeLiveNotificationAuditMetadata(notification)
	metadata["state"] = store.DiscordYouTubeLiveNotificationStateDeliveryUnknown
	metadata["lifecycle_status"] = lifecycleStatus
	metadata["reason"] = code
	metadata["status_code"] = dispatch.StatusCode
	s.writeSystemAudit(ctx, store.AuditEvent{Action: "streams.discord_youtube_notify", ResourceType: "stream", ResourceID: notification.StreamID, Result: "failure", Metadata: metadata})
	return "delivery_unknown"
}

func (s *Server) suppressClaimedDiscordYouTubeLiveNotification(ctx context.Context, outbox store.StreamDiscordYouTubeLiveNotificationStore, claim store.DiscordYouTubeLiveNotificationClaim, lifecycleStatus, code string) string {
	notification := claim.Notification
	if err := outbox.MarkDiscordYouTubeLiveNotificationSuppressed(ctx, notification.ID, claim.LeaseToken, lifecycleStatus, code); err != nil {
		log.Printf("discord youtube notification suppress save failed: notification_id=%s error=%v", notification.ID, err)
		return ""
	}
	metadata := discordYouTubeLiveNotificationAuditMetadata(notification)
	metadata["state"] = store.DiscordYouTubeLiveNotificationStateSuppressed
	metadata["lifecycle_status"] = lifecycleStatus
	metadata["reason"] = code
	s.writeSystemAudit(ctx, store.AuditEvent{Action: "streams.discord_youtube_notify", ResourceType: "stream", ResourceID: notification.StreamID, Result: "failure", Metadata: metadata})
	return "suppressed"
}

// RunDiscordYouTubeLiveNotificationOutboxLoop performs an immediate restart
// scan before waiting for its ticker, so a successful stream start is not tied
// to the lifetime of the originating HTTP process.
func (s *Server) RunDiscordYouTubeLiveNotificationOutboxLoop(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = discordYouTubeLiveNotificationRetryDefaultInterval
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		if _, err := s.DispatchDueDiscordYouTubeLiveNotifications(ctx, 25); err != nil && !errors.Is(err, context.Canceled) {
			log.Printf("discord youtube notification outbox scan failed: %v", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (s *Server) getDiscordYouTubeLiveNotification(w http.ResponseWriter, r *http.Request) {
	streamID := strings.TrimSpace(r.PathValue("id"))
	if _, err := s.streams.GetStream(r.Context(), streamID); errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found"})
		return
	} else if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "get_stream_failed"})
		return
	}
	outbox, ok := s.streams.(store.StreamDiscordYouTubeLiveNotificationStore)
	if !ok {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"code": "discord_youtube_notification_outbox_unavailable"})
		return
	}
	notification, err := outbox.GetLatestDiscordYouTubeLiveNotification(r.Context(), streamID)
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "discord_youtube_notification_not_found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "get_discord_youtube_notification_failed"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"notification": safeDiscordYouTubeLiveNotification(notification)})
}

type recoverDiscordYouTubeLiveNotificationRequest struct {
	AcknowledgePossibleDuplicate bool `json:"acknowledge_possible_duplicate"`
}

// recoverDiscordYouTubeLiveNotification requires an explicit operator
// attestation because the previous request may have posted to Discord after the
// Panel lost its response. Recovery creates a new Bot event id; it never
// reopens or automatically retries the ambiguous event.
func (s *Server) recoverDiscordYouTubeLiveNotification(w http.ResponseWriter, r *http.Request) {
	var body recoverDiscordYouTubeLiveNotificationRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&body); err != nil || !body.AcknowledgePossibleDuplicate {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "duplicate_risk_acknowledgement_required"})
		return
	}
	streamID := strings.TrimSpace(r.PathValue("id"))
	notificationID := strings.TrimSpace(r.PathValue("notification_id"))
	outbox, ok := s.streams.(store.StreamDiscordYouTubeLiveNotificationStore)
	if !ok {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"code": "discord_youtube_notification_outbox_unavailable"})
		return
	}
	recovery, err := outbox.CreateDiscordYouTubeLiveNotificationRecovery(r.Context(), streamID, notificationID)
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "discord_youtube_notification_not_found"})
		return
	}
	if errors.Is(err, store.ErrDiscordYouTubeLiveNotificationRecoveryGone) {
		writeJSON(w, http.StatusConflict, map[string]string{"code": "discord_youtube_notification_recovery_not_allowed"})
		return
	}
	if errors.Is(err, store.ErrDiscordYouTubeLiveNotificationRecoveryUsed) {
		writeJSON(w, http.StatusConflict, map[string]string{"code": "discord_youtube_notification_recovery_already_requested"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "recover_discord_youtube_notification_failed"})
		return
	}
	current := currentFromContext(r.Context())
	metadata := discordYouTubeLiveNotificationAuditMetadata(recovery)
	metadata["prior_notification_id"] = notificationID
	metadata["operator_attested_possible_duplicate"] = true
	s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "streams.discord_youtube_notify.recover", ResourceType: "stream", ResourceID: streamID, Result: "success", Metadata: metadata})
	writeJSON(w, http.StatusAccepted, map[string]any{"notification": safeDiscordYouTubeLiveNotification(recovery)})
}
