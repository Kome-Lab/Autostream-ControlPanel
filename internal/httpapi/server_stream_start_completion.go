package httpapi

import (
	"context"
	"errors"
	"github.com/example/autostream-control-panel/internal/servicecall"
	"github.com/example/autostream-control-panel/internal/store"
	"net/http"
	"strings"
)

func (s *Server) completeStreamStart(w http.ResponseWriter, r *http.Request, stream store.Stream, assignments []store.RegisteredService, req servicecall.StartRequest, dispatch []servicecall.DispatchResult) {
	dispatch = sanitizeDispatchResults(dispatch)
	queuedNotification, notificationRequested := discordYouTubeLiveNotificationForStart(stream, assignments, req)
	var (
		liveStream         store.Stream
		transitioned       bool
		err                error
		notificationQueued *store.DiscordYouTubeLiveNotification
		outboxUnavailable  bool
	)
	if notificationRequested {
		if outbox, ok := s.streams.(store.StreamDiscordYouTubeLiveNotificationStore); ok {
			var queued store.DiscordYouTubeLiveNotification
			liveStream, queued, transitioned, err = outbox.TransitionStreamStatusAndEnqueueDiscordYouTubeLiveNotification(r.Context(), stream.ID, "starting", "live", queuedNotification)
			if transitioned {
				notificationQueued = &queued
			}
		} else {
			// Production stores implement the durable outbox. Keep older narrow
			// test doubles source-compatible, but never represent this fallback as
			// a delivered Discord notification.
			outboxUnavailable = true
			liveStream, transitioned, err = s.streams.TransitionStreamStatus(r.Context(), stream.ID, "starting", "live")
		}
	} else {
		liveStream, transitioned, err = s.streams.TransitionStreamStatus(r.Context(), stream.ID, "starting", "live")
	}
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found"})
		return
	}
	if err != nil {
		if notificationRequested {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "discord_youtube_notification_enqueue_failed"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "update_stream_failed"})
		return
	}
	if !transitioned {
		s.writeStartSupersededResponse(w, r, liveStream, dispatch, assignments)
		return
	}

	current := currentFromContext(r.Context())
	metadata := map[string]any{"status": "live", "dispatch": dispatch}
	response := map[string]any{"stream": liveStream, "dispatch": dispatch}
	if notificationQueued != nil {
		notificationMetadata := discordYouTubeLiveNotificationAuditMetadata(*notificationQueued)
		metadata["discord_notification"] = notificationMetadata
		response["discord_notification"] = safeDiscordYouTubeLiveNotification(*notificationQueued)
		s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "streams.discord_youtube_notify", ResourceType: "stream", ResourceID: stream.ID, Result: "pending", Metadata: notificationMetadata})
	} else if notificationRequested && outboxUnavailable {
		metadata["discord_notification"] = map[string]any{"state": "outbox_unavailable"}
		response["discord_notification"] = map[string]any{"state": "outbox_unavailable"}
		s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "streams.discord_youtube_notify", ResourceType: "stream", ResourceID: stream.ID, Result: "failure", Metadata: map[string]any{"reason": "discord_youtube_notification_outbox_unavailable"}})
	}
	s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "streams.start", ResourceType: "stream", ResourceID: liveStream.ID, Result: "success", Metadata: metadata})
	writeJSON(w, http.StatusOK, response)
}

// writeStartSupersededResponse reports a completed start dispatch whose stream
// transitioned away from starting while the downstream request was in flight.
// The newer lifecycle transition is authoritative and must never be replaced by
// this delayed response.
func (s *Server) writeStartSupersededResponse(w http.ResponseWriter, r *http.Request, stream store.Stream, dispatch []servicecall.DispatchResult, assignments []store.RegisteredService) {
	compensatingStop := s.compensateSupersededStreamStart(r, stream, assignments)
	current := currentFromContext(r.Context())
	metadata := map[string]any{
		"status":           stream.Status,
		"dispatch":         dispatch,
		"start_superseded": true,
	}
	response := map[string]any{
		"stream":           stream,
		"dispatch":         dispatch,
		"start_superseded": true,
	}
	if len(compensatingStop) > 0 {
		metadata["compensating_stop"] = compensatingStop
		response["compensating_stop"] = compensatingStop
	}
	s.writeAudit(r, store.AuditEvent{
		ActorUserID:   current.User.ID,
		ActorUsername: current.User.Username,
		Action:        "streams.start",
		ResourceType:  "stream",
		ResourceID:    stream.ID,
		Result:        "success",
		Metadata:      metadata,
	})
	writeJSON(w, http.StatusAccepted, response)
}

// compensateSupersededStreamStart makes a delayed start converge to the newer
// stop/terminal lifecycle in case a separate Control Panel process dispatched
// a stop while this start request was in flight. The database CAS still owns
// the state transition; this best-effort, bounded dispatch prevents a late
// remote start acknowledgement from being the final action at a service.
func (s *Server) compensateSupersededStreamStart(r *http.Request, stream store.Stream, assignments []store.RegisteredService) []servicecall.DispatchResult {
	if !supersededStartRequiresCompensatingStop(stream.Status) || len(assignments) == 0 {
		return nil
	}
	stopRequest, cancel := detachedStopLifecycleRequest(r)
	defer cancel()
	results := sanitizeDispatchResults(s.dispatcher.Stop(stopRequest.Context(), stream, assignments))
	current := currentFromContext(stopRequest.Context())
	result := "success"
	if hasDispatchFailure(results) {
		result = "failure"
	}
	s.writeAudit(stopRequest, store.AuditEvent{
		ActorUserID:   current.User.ID,
		ActorUsername: current.User.Username,
		Action:        "streams.start_compensate_stop",
		ResourceType:  "stream",
		ResourceID:    stream.ID,
		Result:        result,
		Metadata: map[string]any{
			"superseded_status": stream.Status,
			"dispatch":          results,
		},
	})
	return results
}

func supersededStartRequiresCompensatingStop(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "stopping", "completed", "failed":
		return true
	default:
		return false
	}
}

// writeStopSupersededResponse reports a stop dispatch whose persisted stream
// was moved by a newer lifecycle operation before its terminal transition.
// The newer persisted state is authoritative, so this response never retries
// or overwrites it.
func (s *Server) writeStopSupersededResponse(w http.ResponseWriter, r *http.Request, stream store.Stream, dispatch []servicecall.DispatchResult) {
	current := currentFromContext(r.Context())
	s.writeAudit(r, store.AuditEvent{
		ActorUserID:   current.User.ID,
		ActorUsername: current.User.Username,
		Action:        "streams.stop",
		ResourceType:  "stream",
		ResourceID:    stream.ID,
		Result:        "success",
		Metadata: map[string]any{
			"status":          stream.Status,
			"dispatch":        dispatch,
			"stop_superseded": true,
		},
	})
	writeJSON(w, http.StatusAccepted, map[string]any{
		"stream":          stream,
		"dispatch":        dispatch,
		"stop_superseded": true,
	})
}

func (s *Server) applyDiscordConfig(ctx context.Context, assignments []store.RegisteredService, req *servicecall.StartRequest) error {
	configID := strings.TrimSpace(req.DiscordConfigID)
	if configID == "" {
		return errDiscordConfigRequired
	}
	profile, err := s.profiles.GetProfile(ctx, store.ProfileDiscordConfig, configID)
	if errors.Is(err, store.ErrNotFound) {
		return errDiscordConfigNotFound
	}
	if err != nil {
		return err
	}
	serviceID := strings.TrimSpace(configString(profile.Config, "service_id"))
	if serviceID != "" {
		discordServiceID := primaryServiceID(assignments, "discord_bot")
		if discordServiceID == "" || discordServiceID != serviceID {
			return errDiscordConfigServiceMismatch
		}
	}
	// Stream settings have already filled req before this point.  Keep those
	// explicit per-stream values ahead of the selected Discord profile, while
	// allowing a complete profile to supply omitted channel defaults.
	guildID := strings.TrimSpace(firstNonEmpty(req.DiscordGuildID, configString(profile.Config, "guild_id")))
	voiceChannelID := strings.TrimSpace(firstNonEmpty(req.DiscordVoiceChannelID, configString(profile.Config, "voice_channel_id")))
	textChannelID := strings.TrimSpace(firstNonEmpty(req.DiscordTextChannelID, configString(profile.Config, "text_channel_id")))
	if guildID == "" || voiceChannelID == "" {
		return errDiscordConfigInvalid
	}
	req.DiscordGuildID = guildID
	req.DiscordVoiceChannelID = voiceChannelID
	req.DiscordTextChannelID = textChannelID
	return nil
}

func primaryServiceID(assignments []store.RegisteredService, serviceType string) string {
	for _, service := range assignments {
		if strings.EqualFold(strings.TrimSpace(service.ServiceType), strings.TrimSpace(serviceType)) && normalizeAssignmentRole(service.AssignmentRole) == "primary" {
			return strings.TrimSpace(service.ServiceID)
		}
	}
	return ""
}
