package httpapi

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/example/autostream-control-panel/internal/servicecall"
	"github.com/example/autostream-control-panel/internal/store"
	ytlive "github.com/example/autostream-control-panel/internal/youtube"
)

const youtubeLiveTransitionTimeout = 20 * time.Second

const youtubeLiveTransitionAttempts = 5

func applyStreamSettingsDefaults(stream store.Stream, req *servicecall.StartRequest) {
	if strings.TrimSpace(req.DiscordConfigID) == "" {
		req.DiscordConfigID = stream.DiscordConfigID
	}
	if strings.TrimSpace(req.EncoderProfileID) == "" {
		req.EncoderProfileID = stream.EncoderProfileID
	}
	if strings.TrimSpace(req.CaptionProfileID) == "" {
		req.CaptionProfileID = stream.CaptionProfileID
	}
	if strings.TrimSpace(req.OverlayProfileID) == "" {
		req.OverlayProfileID = stream.OverlayProfileID
	}
	req.EncoderAudioGainDB = stream.EncoderAudioGainDB
	if strings.TrimSpace(req.ArchiveProfileID) == "" {
		req.ArchiveProfileID = stream.ArchiveProfileID
	}
	if strings.TrimSpace(req.YouTubeOutputID) == "" {
		req.YouTubeOutputID = stream.YouTubeOutputID
	}
	if strings.TrimSpace(req.EncoderInputURL) == "" {
		req.EncoderInputURL = stream.EncoderInputURL
	}
}

func (s *Server) applyEncoderVideoProfile(ctx context.Context, req *servicecall.StartRequest) error {
	if strings.TrimSpace(req.EncoderProfileID) == "" {
		return errEncoderProfileNotFound
	}
	if s.profiles == nil {
		return errEncoderProfileNotFound
	}
	profile, err := s.profiles.GetProfile(ctx, store.ProfileEncoder, req.EncoderProfileID)
	if err != nil {
		return err
	}
	req.EncoderVideoWidth = configInt(profile.Config, "width")
	req.EncoderVideoHeight = configInt(profile.Config, "height")
	req.EncoderVideoFPS = configInt(profile.Config, "fps")
	validSize := (req.EncoderVideoWidth == 1920 && req.EncoderVideoHeight == 1080) ||
		(req.EncoderVideoWidth == 1280 && req.EncoderVideoHeight == 720) ||
		(req.EncoderVideoWidth == 854 && req.EncoderVideoHeight == 480)
	if !validSize || req.EncoderVideoFPS < 1 || req.EncoderVideoFPS > 60 {
		return errors.New("encoder profile video dimensions are invalid")
	}
	return nil
}

type streamStartMaterialization struct {
	Service     store.RegisteredService
	ActorUserID string
}

func (s *Server) serviceStartStream(w http.ResponseWriter, r *http.Request) {
	token, ok := s.authenticateService(w, r, "streams.start")
	if !ok {
		return
	}
	if token.ServiceType != "discord_bot" {
		s.writeServiceAudit(r, token, "streams.start", "stream", r.PathValue("id"), "failure", map[string]any{"reason": "service_type_not_allowed"})
		writeJSON(w, http.StatusForbidden, map[string]string{"code": "service_type_not_allowed"})
		return
	}
	streamID := strings.TrimSpace(r.PathValue("id"))
	stream, err := s.streams.GetStream(r.Context(), streamID)
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "get_stream_failed"})
		return
	}
	service, assigned, err := s.serviceTokenPrimaryAssignedToStream(r.Context(), token, stream.ID, "discord_bot")
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "list_stream_assignments_failed"})
		return
	}
	assignBeforeStart := false
	if !assigned {
		configuredService, configured, err := s.discordServiceTokenConfiguredForStream(r.Context(), token, stream)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "list_stream_assignments_failed"})
			return
		}
		if configured {
			service = configuredService
			assigned = true
			assignBeforeStart = true
		}
	}
	if !assigned {
		s.writeServiceAudit(r, token, "streams.start", "stream", stream.ID, "failure", map[string]any{"reason": "service_not_primary_assignment"})
		writeJSON(w, http.StatusForbidden, map[string]string{"code": "service_not_primary_assignment"})
		return
	}
	if isActiveStreamStatus(stream.Status) {
		s.writeServiceAudit(r, token, "streams.start", "stream", stream.ID, "success", map[string]any{"service_id": service.ServiceID, "skipped": true, "reason": "stream_already_active"})
		writeJSON(w, http.StatusOK, map[string]any{"stream": stream, "already_active": true})
		return
	}
	if strings.TrimSpace(stream.AutoStartTrigger) != autoStartTriggerDiscordVoiceJoin {
		s.writeServiceAudit(r, token, "streams.start", "stream", stream.ID, "failure", map[string]any{"service_id": service.ServiceID, "reason": "stream_auto_start_not_enabled"})
		writeJSON(w, http.StatusConflict, map[string]string{"code": "stream_auto_start_not_enabled"})
		return
	}
	if !isAutoStartableStreamStatus(stream.Status) {
		s.writeServiceAudit(r, token, "streams.start", "stream", stream.ID, "failure", map[string]any{"service_id": service.ServiceID, "reason": "stream_not_waiting", "status": stream.Status})
		writeJSON(w, http.StatusConflict, map[string]string{"code": "stream_not_waiting"})
		return
	}
	var materialization *streamStartMaterialization
	if assignBeforeStart {
		materialization = &streamStartMaterialization{Service: service, ActorUserID: "service:" + service.ServiceID}
	}
	r.Body = http.NoBody
	r = r.WithContext(context.WithValue(r.Context(), currentUserKey{}, serviceCurrentUser(service)))
	s.startStreamWithMaterialization(w, r, materialization)
}

// serviceStopStream is deliberately narrower than the operator stop route:
// only the primary Discord Bot assigned to a VC-triggered stream may request
// it. The actual transition remains stopStream so that dispatch, YouTube
// completion, auditing, and waiting-stream rearm have one implementation.
func (s *Server) serviceStopStream(w http.ResponseWriter, r *http.Request) {
	token, ok := s.authenticateService(w, r, "streams.stop")
	if !ok {
		return
	}
	if token.ServiceType != "discord_bot" {
		s.writeServiceAudit(r, token, "streams.stop", "stream", r.PathValue("id"), "failure", map[string]any{"reason": "service_type_not_allowed"})
		writeJSON(w, http.StatusForbidden, map[string]string{"code": "service_type_not_allowed"})
		return
	}
	stream, err := s.streams.GetStream(r.Context(), strings.TrimSpace(r.PathValue("id")))
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "get_stream_failed"})
		return
	}
	service, assigned, err := s.serviceTokenPrimaryAssignedToStream(r.Context(), token, stream.ID, "discord_bot")
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "list_stream_assignments_failed"})
		return
	}
	if !assigned && strings.EqualFold(strings.TrimSpace(stream.Status), "completed") {
		configuredService, configured, configuredErr := s.discordServiceTokenConfiguredForStream(r.Context(), token, stream)
		if configuredErr != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "list_stream_assignments_failed"})
			return
		}
		if configured {
			service = configuredService
			assigned = true
		}
	}
	switch strings.ToLower(strings.TrimSpace(stream.Status)) {
	case "completed":
		// Re-arming a VC stream reuses its existing row. A duplicate stop must
		// still be tied to that stream's configured Discord Bot; it is never an
		// authorization shortcut for an arbitrary registered bot.
		if !assigned {
			s.writeServiceAudit(r, token, "streams.stop", "stream", stream.ID, "failure", map[string]any{"reason": "service_not_primary_assignment"})
			writeJSON(w, http.StatusForbidden, map[string]string{"code": "service_not_primary_assignment"})
			return
		}
		s.writeServiceAudit(r, token, "streams.stop", "stream", stream.ID, "success", map[string]any{"service_id": service.ServiceID, "skipped": true, "reason": "stream_already_stopped"})
		writeJSON(w, http.StatusOK, map[string]any{"already_stopped": true})
		return
	}
	if !assigned {
		s.writeServiceAudit(r, token, "streams.stop", "stream", stream.ID, "failure", map[string]any{"reason": "service_not_primary_assignment"})
		writeJSON(w, http.StatusForbidden, map[string]string{"code": "service_not_primary_assignment"})
		return
	}
	if strings.TrimSpace(stream.AutoStartTrigger) != autoStartTriggerDiscordVoiceJoin {
		s.writeServiceAudit(r, token, "streams.stop", "stream", stream.ID, "failure", map[string]any{"service_id": service.ServiceID, "reason": "stream_auto_start_not_enabled"})
		writeJSON(w, http.StatusConflict, map[string]string{"code": "stream_auto_start_not_enabled"})
		return
	}
	switch strings.ToLower(strings.TrimSpace(stream.Status)) {
	case "stopping":
		s.writeServiceAudit(r, token, "streams.stop", "stream", stream.ID, "success", map[string]any{"service_id": service.ServiceID, "skipped": true, "reason": "stream_already_stopping"})
		writeJSON(w, http.StatusAccepted, map[string]any{"stream": stream, "already_stopping": true})
		return
	}
	if !isManuallyStoppableStreamStatus(stream.Status) {
		s.writeServiceAudit(r, token, "streams.stop", "stream", stream.ID, "failure", map[string]any{"service_id": service.ServiceID, "reason": "stream_status_not_stoppable", "status": stream.Status})
		writeJSON(w, http.StatusConflict, map[string]any{"code": "stream_status_not_stoppable", "status": stream.Status})
		return
	}
	r.Body = http.NoBody
	r = r.WithContext(context.WithValue(r.Context(), currentUserKey{}, serviceCurrentUser(service)))
	s.stopStream(w, r)
}

func (s *Server) discordServiceTokenConfiguredForStream(ctx context.Context, token store.ServiceToken, stream store.Stream) (store.RegisteredService, bool, error) {
	if s.services == nil || s.profiles == nil {
		return store.RegisteredService{}, false, nil
	}
	service, registered, err := s.registeredServiceForToken(ctx, token)
	if err != nil || !registered {
		return store.RegisteredService{}, false, err
	}
	if service.ServiceType != "discord_bot" {
		return store.RegisteredService{}, false, nil
	}
	matches, err := s.streamDiscordConfigMatchesService(ctx, stream, service.ServiceID)
	if err != nil || !matches {
		return store.RegisteredService{}, false, err
	}
	assignments, err := s.services.ListStreamAssignments(ctx, stream.ID)
	if err != nil {
		return store.RegisteredService{}, false, err
	}
	for _, assignedService := range assignments {
		if strings.TrimSpace(assignedService.ServiceType) == "discord_bot" && normalizeAssignmentRole(assignedService.AssignmentRole) == "primary" && strings.TrimSpace(assignedService.ServiceID) != service.ServiceID {
			return store.RegisteredService{}, false, nil
		}
	}
	service.AssignmentRole = "primary"
	return service, true, nil
}

func (s *Server) configuredDiscordAssignmentCandidate(ctx context.Context, streamID, discordConfigID string, assignments []store.RegisteredService) ([]store.RegisteredService, string, error) {
	if s.services == nil || primaryServiceID(primaryStreamAssignments(assignments), "discord_bot") != "" {
		return assignments, "", nil
	}
	service, configured, err := s.configuredDiscordServiceForStream(ctx, streamID, discordConfigID)
	if err != nil || !configured {
		return assignments, "", err
	}
	service.AssignmentRole = "primary"
	return append(assignments, service), service.ServiceID, nil
}

func (s *Server) configuredDiscordServiceForStream(ctx context.Context, streamID, discordConfigID string) (store.RegisteredService, bool, error) {
	if s.services == nil || s.profiles == nil || strings.TrimSpace(discordConfigID) == "" {
		return store.RegisteredService{}, false, nil
	}
	profile, err := s.profiles.GetProfile(ctx, store.ProfileDiscordConfig, strings.TrimSpace(discordConfigID))
	if errors.Is(err, store.ErrNotFound) {
		return store.RegisteredService{}, false, nil
	}
	if err != nil {
		return store.RegisteredService{}, false, err
	}
	services, err := s.services.ListServices(ctx)
	if err != nil {
		return store.RegisteredService{}, false, err
	}
	streamID = strings.TrimSpace(streamID)
	matches := make([]store.RegisteredService, 0, 1)
	for _, service := range services {
		if !strings.EqualFold(strings.TrimSpace(service.ServiceType), "discord_bot") || !runtimeProfileMatchesService(profile.Config, strings.TrimSpace(service.ServiceID)) {
			continue
		}
		// A start request must never silently take a Bot away from another
		// stream. An already assigned Bot on this same stream can be promoted
		// from standby by the store call below.
		if currentStreamID := strings.TrimSpace(service.CurrentStreamID); currentStreamID != "" && currentStreamID != streamID {
			continue
		}
		matches = append(matches, service)
	}
	if len(matches) != 1 {
		return store.RegisteredService{}, false, nil
	}
	matches[0].AssignmentRole = "primary"
	return matches[0], true, nil
}

func (s *Server) streamDiscordConfigMatchesService(ctx context.Context, stream store.Stream, serviceID string) (bool, error) {
	if s.profiles == nil || strings.TrimSpace(stream.DiscordConfigID) == "" || strings.TrimSpace(serviceID) == "" {
		return false, nil
	}
	profile, err := s.profiles.GetProfile(ctx, store.ProfileDiscordConfig, stream.DiscordConfigID)
	if errors.Is(err, store.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return runtimeProfileMatchesService(profile.Config, serviceID), nil
}

func (s *Server) serviceTokenPrimaryAssignedToStream(ctx context.Context, token store.ServiceToken, streamID, serviceType string) (store.RegisteredService, bool, error) {
	if s.services == nil {
		return store.RegisteredService{}, false, nil
	}
	assignments, err := s.services.ListStreamAssignments(ctx, streamID)
	if err != nil {
		return store.RegisteredService{}, false, err
	}
	for _, service := range assignments {
		if strings.TrimSpace(service.TokenID) == token.ID &&
			strings.TrimSpace(service.ServiceType) == serviceType &&
			strings.TrimSpace(service.AssignmentRole) == "primary" {
			return service, true, nil
		}
	}
	return store.RegisteredService{}, false, nil
}

func serviceCurrentUser(service store.RegisteredService) currentUser {
	serviceID := strings.TrimSpace(service.ServiceID)
	if serviceID == "" {
		serviceID = strings.TrimSpace(service.ServiceType)
	}
	return currentUser{User: store.User{ID: "service:" + serviceID, Username: serviceID}}
}

func isActiveStreamStatus(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "starting", "live", "stopping":
		return true
	default:
		return false
	}
}

func isAutoStartableStreamStatus(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "created", "draft", "scheduled", "ready":
		return true
	default:
		return false
	}
}

func isManuallyStartableStreamStatus(status string) bool {
	return isAutoStartableStreamStatus(status) || strings.EqualFold(strings.TrimSpace(status), "failed")
}

func isManuallyStoppableStreamStatus(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "starting", "live", "failed":
		return true
	default:
		return false
	}
}

func isForceStoppableStreamStatus(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "starting", "live", "stopping", "failed":
		return true
	default:
		return false
	}
}

func (s *Server) startStream(w http.ResponseWriter, r *http.Request) {
	s.startStreamWithMaterialization(w, r, nil)
}

func (s *Server) startStreamWithMaterialization(w http.ResponseWriter, r *http.Request, materialization *streamStartMaterialization) {
	unlockLifecycle := s.lockStreamLifecycle(r.PathValue("id"))
	defer unlockLifecycle()

	var body servicecall.StartRequest
	if r.Body != nil {
		if err := decodeOptionalSingleJSON(r, &body); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"code": "bad_request"})
			return
		}
	}
	stream, err := s.streams.GetStream(r.Context(), r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "get_stream_failed"})
		return
	}
	if !isManuallyStartableStreamStatus(stream.Status) {
		current := currentFromContext(r.Context())
		s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "streams.start", ResourceType: "stream", ResourceID: stream.ID, Result: "failure", Metadata: map[string]any{"reason": "stream_status_not_startable", "current_status": stream.Status}})
		writeJSON(w, http.StatusConflict, map[string]any{"code": "stream_status_not_startable", "status": stream.Status})
		return
	}
	applyStreamSettingsDefaults(stream, &body)
	if err := s.materializeDiscordStartTarget(r.Context(), stream.ID, &body); err != nil {
		writeJSON(w, http.StatusConflict, map[string]string{"code": "discord_target_snapshot_failed"})
		return
	}
	if err := validateEncoderInputURL(body.EncoderInputURL); err != nil {
		current := currentFromContext(r.Context())
		s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "streams.start", ResourceType: "stream", ResourceID: stream.ID, Result: "failure", Metadata: map[string]any{"reason": "encoder_input_url_blocked"}})
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "encoder_input_url_blocked"})
		return
	}
	// Snapshot a static fixed relay before any capability/readiness precheck.
	// Subsequent static checks use this exact profile version and the later
	// start-wide fence rejects a concurrent static-to-dynamic edit before any
	// provider Prepare or service dispatch can observe it.
	relayStaticOutput, err := s.selectedYouTubeRelayStaticOutputStartFence(r.Context(), body.YouTubeOutputID)
	if err != nil {
		current := currentFromContext(r.Context())
		code := youtubeOutputCode(err)
		s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "streams.start", ResourceType: "stream", ResourceID: stream.ID, Result: "failure", Metadata: map[string]any{"reason": code, "youtube_output_id": body.YouTubeOutputID}})
		writeJSON(w, youtubeOutputStatus(err), map[string]string{"code": code})
		return
	}
	// A start request normally inherits the persisted output, but callers may
	// explicitly supply an output ID. Do not let such an override downgrade an
	// already configured static fixed relay into a dynamic path: that would skip
	// its binding claim and invalidate the selected profile snapshot entirely.
	if relayStaticOutput == nil && strings.TrimSpace(stream.YouTubeOutputID) != "" && strings.TrimSpace(stream.YouTubeOutputID) != strings.TrimSpace(body.YouTubeOutputID) {
		persistedStaticOutput, persistedErr := s.selectedYouTubeRelayStaticOutputStartFence(r.Context(), stream.YouTubeOutputID)
		if persistedErr != nil {
			current := currentFromContext(r.Context())
			code := youtubeOutputCode(persistedErr)
			s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "streams.start", ResourceType: "stream", ResourceID: stream.ID, Result: "failure", Metadata: map[string]any{"reason": code, "youtube_output_id": stream.YouTubeOutputID}})
			writeJSON(w, youtubeOutputStatus(persistedErr), map[string]string{"code": code})
			return
		}
		if persistedStaticOutput != nil {
			current := currentFromContext(r.Context())
			s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "streams.start", ResourceType: "stream", ResourceID: stream.ID, Result: "failure", Metadata: map[string]any{"reason": errYouTubeRelayStaticConfigChanged.Error(), "youtube_output_id": stream.YouTubeOutputID}})
			writeJSON(w, http.StatusConflict, map[string]string{"code": errYouTubeRelayStaticConfigChanged.Error()})
			return
		}
	}
	if err := s.validateArchiveConfigReadiness(r.Context(), &body); err != nil {
		current := currentFromContext(r.Context())
		code := archiveConfigCode(err)
		s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "streams.start", ResourceType: "stream", ResourceID: stream.ID, Result: "failure", Metadata: map[string]any{"reason": code, "archive_profile_id": body.ArchiveProfileID}})
		writeJSON(w, archiveConfigStatus(err), map[string]string{"code": code})
		return
	}
	assignments, err := s.streamAssignments(r.Context(), stream.ID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "list_stream_assignments_failed"})
		return
	}
	primaryAssignments := primaryStreamAssignments(assignments)
	missing := missingServiceTypes(primaryAssignments, requiredStartServiceTypes)
	claimMaterializeServiceID := ""
	claimMaterializeActorUserID := ""
	if materialization != nil {
		service := materialization.Service
		service.AssignmentRole = "primary"
		assignments = append(assignments, service)
		primaryAssignments = primaryStreamAssignments(assignments)
		missing = missingServiceTypes(primaryAssignments, requiredStartServiceTypes)
		claimMaterializeServiceID = strings.TrimSpace(service.ServiceID)
		claimMaterializeActorUserID = strings.TrimSpace(materialization.ActorUserID)
	}
	// The stream form selects a Discord config, whose service_id identifies the
	// Bot node, but historically only Worker/Encoder IDs were materialized as
	// stream assignments. Auto-start has a service-token fallback for this
	// configuration; operator start must converge to the same primary assignment
	// before dispatch so later stop/runtime operations see the Bot as well.
	if materialization == nil && len(missing) == 1 && missing[0] == "discord_bot" {
		current := currentFromContext(r.Context())
		updatedAssignments, materializeServiceID, materializeErr := s.configuredDiscordAssignmentCandidate(r.Context(), stream.ID, body.DiscordConfigID, assignments)
		if materializeErr != nil {
			code := "assign_service_failed"
			status := http.StatusInternalServerError
			if guardedCode, guardedStatus, handled := serviceAssignmentHTTPError(materializeErr, false); handled {
				code = guardedCode
				status = guardedStatus
			}
			s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "streams.start", ResourceType: "stream", ResourceID: stream.ID, Result: "failure", Metadata: map[string]any{"reason": code, "error_type": fmt.Sprintf("%T", materializeErr)}})
			writeJSON(w, status, map[string]string{"code": code})
			return
		}
		if materializeServiceID != "" {
			assignments = updatedAssignments
			primaryAssignments = primaryStreamAssignments(assignments)
			claimMaterializeServiceID = materializeServiceID
			claimMaterializeActorUserID = current.User.ID
			missing = missingServiceTypes(primaryAssignments, requiredStartServiceTypes)
		}
	}
	if len(missing) > 0 {
		current := currentFromContext(r.Context())
		s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "streams.start", ResourceType: "stream", ResourceID: stream.ID, Result: "failure", Metadata: map[string]any{"missing_service_types": missing}})
		writeJSON(w, http.StatusConflict, map[string]any{"code": "missing_stream_assignments", "missing_service_types": missing})
		return
	}
	if issues, readinessErr := s.controlPlatformReadinessIssues(r.Context(), stream.ID, primaryAssignments); readinessErr != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "control_platform_readiness_failed"})
		return
	} else if len(issues) > 0 {
		current := currentFromContext(r.Context())
		s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "streams.start", ResourceType: "stream", ResourceID: stream.ID, Result: "failure", Metadata: map[string]any{"readiness_issues": issues}})
		writeJSON(w, http.StatusConflict, map[string]any{"code": "stream_start_not_ready", "issues": issues})
		return
	}
	s.systemUpdateOperationMu.Lock()
	updateOperationLocked := true
	defer func() {
		if updateOperationLocked {
			s.systemUpdateOperationMu.Unlock()
		}
	}()
	if job, active, err := s.activeSystemUpdateForStreamTargets(r.Context(), primaryAssignments); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "check_service_update_failed"})
		return
	} else if active {
		current := currentFromContext(r.Context())
		s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "streams.start", ResourceType: "stream", ResourceID: stream.ID, Result: "failure", Metadata: map[string]any{"reason": "service_update_in_progress", "update_job_id": job.ID, "target_id": job.TargetID, "update_status": job.Status}})
		writeJSON(w, http.StatusConflict, map[string]any{"code": "service_update_in_progress", "job_id": job.ID, "target_id": job.TargetID, "status": job.Status})
		return
	}
	if err := s.applyDiscordConfig(r.Context(), primaryAssignments, &body); err != nil {
		current := currentFromContext(r.Context())
		code := discordConfigCode(err)
		s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "streams.start", ResourceType: "stream", ResourceID: stream.ID, Result: "failure", Metadata: map[string]any{"reason": code, "discord_config_id": body.DiscordConfigID}})
		writeJSON(w, discordConfigStatus(err), map[string]string{"code": code})
		return
	}
	if err := s.applyCaptionDispatchConfig(r.Context(), &body); err != nil {
		current := currentFromContext(r.Context())
		code := streamSettingsReferenceCode(err)
		status := http.StatusInternalServerError
		if errors.Is(err, errCaptionProfileNotFound) {
			status = http.StatusNotFound
		}
		s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "streams.start", ResourceType: "stream", ResourceID: stream.ID, Result: "failure", Metadata: map[string]any{"reason": code, "caption_profile_id": body.CaptionProfileID}})
		writeJSON(w, status, map[string]string{"code": code})
		return
	}
	validateYouTubeReadiness := func() error {
		if relayStaticOutput != nil {
			return s.validateYouTubeOutputReadinessProfile(r.Context(), stream, relayStaticOutput.Profile, &body)
		}
		return s.validateYouTubeOutputReadiness(r.Context(), stream, &body)
	}
	// The Encoder declares direct output or the claimed Live API static relay. Enforce
	// that compatibility boundary before readiness can prepare dynamic output or
	// before the static path can reserve its reusable binding.
	if err := s.validateYouTubeLiveAPIOutputRelay(r.Context(), primaryAssignments, &body); err != nil {
		current := currentFromContext(r.Context())
		code := youtubeOutputCode(err)
		s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "streams.start", ResourceType: "stream", ResourceID: stream.ID, Result: "failure", Metadata: map[string]any{"reason": code, "youtube_output_id": body.YouTubeOutputID}})
		writeJSON(w, youtubeOutputStatus(err), map[string]string{"code": code})
		return
	}
	if err := validateYouTubeReadiness(); err != nil {
		current := currentFromContext(r.Context())
		code := youtubeOutputCode(err)
		s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "streams.start", ResourceType: "stream", ResourceID: stream.ID, Result: "failure", Metadata: map[string]any{"reason": code, "youtube_output_id": body.YouTubeOutputID}})
		writeJSON(w, youtubeOutputStatus(err), map[string]string{"code": code})
		return
	}
	if checker, ok := s.dispatcher.(startReadinessChecker); ok {
		if issues := checker.StartReadinessIssues(primaryAssignments, body, time.Now().UTC()); len(issues) > 0 {
			current := currentFromContext(r.Context())
			s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "streams.start", ResourceType: "stream", ResourceID: stream.ID, Result: "failure", Metadata: map[string]any{"readiness_issues": issues}})
			writeJSON(w, http.StatusConflict, map[string]any{"code": "stream_start_not_ready", "issues": issues})
			return
		}
	}
	if servicecall.WorkerVideoCapabilitiesEnabled(primaryAssignments) {
		if err := s.applyEncoderVideoProfile(r.Context(), &body); err != nil {
			current := currentFromContext(r.Context())
			s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "streams.start", ResourceType: "stream", ResourceID: stream.ID, Result: "failure", Metadata: map[string]any{"reason": "encoder_profile_resolution_failed", "encoder_profile_id": body.EncoderProfileID}})
			writeJSON(w, http.StatusConflict, map[string]string{"code": "encoder_profile_resolution_failed"})
			return
		}
	}
	if err := s.applyArchiveConfig(r.Context(), &body); err != nil {
		current := currentFromContext(r.Context())
		code := archiveConfigCode(err)
		s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "streams.start", ResourceType: "stream", ResourceID: stream.ID, Result: "failure", Metadata: map[string]any{"reason": code, "archive_profile_id": body.ArchiveProfileID}})
		writeJSON(w, archiveConfigStatus(err), map[string]string{"code": code})
		return
	}
	claimStore, ok := s.streams.(store.StreamStartClaimStore)
	if !ok {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"code": "stream_start_claim_unavailable"})
		return
	}
	claimedStart, err := claimStore.ClaimStreamStart(r.Context(), store.StreamStartClaimRequest{
		StreamID: stream.ID, ExpectedStatus: stream.Status, ExpectedStreamUpdatedAt: stream.UpdatedAt,
		ExpectedPrimaryAssignments: primaryAssignments,
		MaterializeServiceID:       claimMaterializeServiceID, MaterializeActorUserID: claimMaterializeActorUserID,
		ArchiveEnabled: strings.TrimSpace(body.ArchiveProfileID) != "", ArchiveStartedAt: time.Now().UTC(),
	})
	if err != nil {
		current := currentFromContext(r.Context())
		code := "stream_start_claim_failed"
		status := http.StatusInternalServerError
		if errors.Is(err, store.ErrServiceAssignmentConflict) || errors.Is(err, store.ErrNotFound) {
			code = "service_assignment_conflict"
			status = http.StatusConflict
		} else if errors.Is(err, store.ErrServiceAssignmentGuardUnavailable) {
			code = "stream_start_claim_unavailable"
			status = http.StatusServiceUnavailable
		}
		s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "streams.start", ResourceType: "stream", ResourceID: stream.ID, Result: "failure", Metadata: map[string]any{"reason": code}})
		writeJSON(w, status, map[string]string{"code": code})
		return
	}
	stream = claimedStart.Stream
	primaryAssignments = claimedStart.PrimaryAssignments
	body.ArchiveRunID = claimedStart.ArchiveAuthority.RunID
	body.ArchiveStartedAt = time.Time{}
	if claimedStart.ArchiveAuthority.StartedAt != nil {
		body.ArchiveStartedAt = claimedStart.ArchiveAuthority.StartedAt.UTC()
	}
	if claimedStart.Materialized != nil {
		current := currentFromContext(r.Context())
		s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "services.assign", ResourceType: "service", ResourceID: claimedStart.Materialized.ServiceID, Result: "success", Metadata: map[string]any{"stream_id": stream.ID, "service_type": claimedStart.Materialized.ServiceType, "assignment_role": "primary", "source": "streams.start"}})
	}
	videoCoverGeneration, err := s.beginVideoCoverGeneration(r.Context(), stream.ID)
	if err != nil {
		_, _, _ = claimStore.TransitionClaimedStreamStart(r.Context(), claimedStart.OwnershipClaim, "failed")
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "video_cover_generation_failed"})
		return
	}
	if err := s.materializeVisualStartSnapshot(r.Context(), stream.ID, primaryAssignments, videoCoverGeneration, &body); err != nil {
		_, _, _ = claimStore.TransitionClaimedStreamStart(r.Context(), claimedStart.OwnershipClaim, "failed")
		writeJSON(w, http.StatusConflict, map[string]string{"code": "visual_start_snapshot_failed"})
		return
	}
	s.systemUpdateOperationMu.Unlock()
	updateOperationLocked = false

	// The static fixed relay reserves a pre-provisioned YouTube LiveStream. The
	// universal ownership claim now precedes every external Prepare/dispatch.
	relayStaticStartClaimed := relayStaticOutput != nil
	if relayStaticStartClaimed {
		// A static start must remain bound to the exact output version that was
		// selected before we claimed the lifecycle. Do not let a concurrent
		// static-to-dynamic output edit fall through to a different Prepare or
		// dispatch path after the stream has entered starting.
		if err := s.validateYouTubeRelayStaticOutputStartFence(r.Context(), stream.ID, *relayStaticOutput); err != nil {
			s.convergeRelayStaticPreDispatchFailure(r.Context(), claimedStart.OwnershipClaim)
			current := currentFromContext(r.Context())
			code := youtubeOutputCode(err)
			s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "streams.start", ResourceType: "stream", ResourceID: stream.ID, Result: "failure", Metadata: map[string]any{"reason": code, "youtube_output_id": body.YouTubeOutputID}})
			writeJSON(w, youtubeOutputStatus(err), map[string]string{"code": code})
			return
		}
	}
	applyYouTubeOutput := func(ctx context.Context, stream store.Stream, req *servicecall.StartRequest) error {
		return s.applyYouTubeOutput(ctx, stream, claimedStart.OwnershipClaim, req)
	}
	if relayStaticOutput != nil {
		applyYouTubeOutput = func(ctx context.Context, stream store.Stream, req *servicecall.StartRequest) error {
			return s.applyYouTubeOutputProfile(ctx, stream, relayStaticOutput.Profile, claimedStart.OwnershipClaim, req)
		}
	}
	if err := applyYouTubeOutput(r.Context(), stream, &body); err != nil {
		s.convergeRelayStaticPreDispatchFailure(r.Context(), claimedStart.OwnershipClaim)
		current := currentFromContext(r.Context())
		code := youtubeOutputCode(err)
		metadata := map[string]any{"reason": code, "youtube_output_id": body.YouTubeOutputID}
		for key, value := range youtubeLiveAPIPrepareFailureMetadata(err) {
			metadata[key] = value
		}
		if _, ok := metadata["prepare_stage"]; ok {
			log.Printf("youtube live api prepare failed: stream_id=%s output_id=%s stage=%s error_type=%s error_class=%v transport_operation=%v provider_status_code=%v provider_reason=%v", stream.ID, body.YouTubeOutputID, metadata["prepare_stage"], metadata["error_type"], metadata["error_class"], metadata["transport_operation"], metadata["provider_status_code"], metadata["provider_reason"])
		}
		s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "streams.start", ResourceType: "stream", ResourceID: stream.ID, Result: "failure", Metadata: metadata})
		writeJSON(w, youtubeOutputStatus(err), map[string]string{"code": code})
		return
	}
	if err := s.saveYouTubeRuntime(r.Context(), stream.ID, body.YouTubeRuntime); err != nil {
		s.convergeRelayStaticPreDispatchFailure(r.Context(), claimedStart.OwnershipClaim)
		current := currentFromContext(r.Context())
		errorCode := store.YouTubeRuntimeSaveErrorCode(err)
		log.Printf("stream start runtime save failed: stream_id=%s error_code=%s error_type=%T", stream.ID, errorCode, err)
		s.writeAudit(r, store.AuditEvent{
			ActorUserID:   current.User.ID,
			ActorUsername: current.User.Username,
			Action:        "streams.start",
			ResourceType:  "stream",
			ResourceID:    stream.ID,
			Result:        "failure",
			Metadata: map[string]any{
				"reason":            "save_youtube_runtime_failed",
				"error_code":        errorCode,
				"youtube_output_id": body.YouTubeOutputID,
			},
		})
		s.clearYouTubeRuntimeSecretFromMap(r.Context(), body.YouTubeRuntime)
		writeJSON(w, http.StatusInternalServerError, map[string]string{
			"code":        "save_youtube_runtime_failed",
			"detail_code": errorCode,
		})
		return
	}
	if relayStaticStartClaimed {
		// Persist the external-dispatch fence before the first downstream Start
		// request. A returned timeout can otherwise look like a harmless local
		// failure even though the Encoder may have received the request and begun
		// pushing this reusable fixed relay.
		if err := s.markYouTubeRelayStaticPossiblyDispatched(r.Context(), stream.ID); err != nil {
			s.convergeRelayStaticPreDispatchFailure(r.Context(), claimedStart.OwnershipClaim)
			current := currentFromContext(r.Context())
			code := youtubeOutputCode(err)
			s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "streams.start", ResourceType: "stream", ResourceID: stream.ID, Result: "failure", Metadata: map[string]any{"reason": code, "trigger": "before_start_dispatch", "youtube_output_id": body.YouTubeOutputID}})
			writeJSON(w, youtubeOutputStatus(err), map[string]string{"code": code})
			return
		}
	}
	results := s.dispatcher.Start(r.Context(), stream, primaryAssignments, body)
	results = sanitizeDispatchResults(results)
	if hasDispatchFailure(results) {
		failed, transitioned, transitionErr := claimStore.TransitionClaimedStreamStart(r.Context(), claimedStart.OwnershipClaim, "failed")
		if errors.Is(transitionErr, store.ErrNotFound) {
			writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found"})
			return
		}
		if transitionErr != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "update_stream_failed"})
			return
		}
		if !transitioned {
			s.writeStartSupersededResponse(w, r, failed, results, primaryAssignments)
			return
		}
		// Start dispatch is ordered Encoder -> Worker -> Discord Bot and stops at
		// the first failure. A failure can therefore leave earlier services
		// running even though the stream has already become terminal locally.
		// Compensate every primary assignment on a detached, bounded context so a
		// Discord-triggered caller disconnect cannot strand the Encoder or Worker.
		stopRequest, cancelStopLifecycle := detachedStopLifecycleRequest(r)
		defer cancelStopLifecycle()
		stopResults := sanitizeDispatchResults(s.dispatcher.Stop(stopRequest.Context(), failed, primaryAssignments))
		if relayStaticStartClaimed {
			// An Encoder Start timeout or partial failure is not evidence that the
			// Encoder never started pushing the fixed relay. Do not delete or
			// complete the Broadcast in this branch: atomically remove the runtime
			// from automatic completion and retain the binding for an explicit,
			// stop-confirmed recovery flow.
			staticRuntime, err := s.abandonYouTubeRelayStaticRuntimeForUnconfirmedDispatch(stopRequest.Context(), stream.ID, "youtube_relay_static_start_dispatch_unconfirmed")
			current := currentFromContext(stopRequest.Context())
			metadata := map[string]any{"trigger": "start_dispatch_failed", "mode": "live_api_relay_static"}
			if err != nil {
				metadata["reason"] = "youtube_relay_static_start_dispatch_recovery_fence_failed"
				s.writeAudit(stopRequest, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "youtube.relay_static_recovery", ResourceType: "stream", ResourceID: stream.ID, Result: "failure", Metadata: metadata})
			} else if staticRuntime {
				metadata["recovery_required"] = true
				s.writeAudit(stopRequest, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "youtube.relay_static_recovery", ResourceType: "stream", ResourceID: stream.ID, Result: "success", Metadata: metadata})
			}
		} else if metadata, err := s.completeYouTubeRuntime(stopRequest.Context(), stream.ID, true); err != nil {
			code := "complete_youtube_runtime_failed"
			if errors.Is(err, errYouTubeLiveAPICompleteFailed) {
				code = errYouTubeLiveAPICompleteFailed.Error()
			}
			current := currentFromContext(stopRequest.Context())
			s.writeAudit(stopRequest, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "youtube.complete", ResourceType: "stream", ResourceID: stream.ID, Result: "failure", Metadata: map[string]any{"reason": code, "trigger": "start_dispatch_failed"}})
		} else if len(metadata) > 0 {
			current := currentFromContext(stopRequest.Context())
			metadata["trigger"] = "start_dispatch_failed"
			s.writeAudit(stopRequest, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "youtube.complete", ResourceType: "stream", ResourceID: stream.ID, Result: "success", Metadata: metadata})
		}
		current := currentFromContext(stopRequest.Context())
		s.writeAudit(stopRequest, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "streams.start", ResourceType: "stream", ResourceID: stream.ID, Result: "failure", Metadata: map[string]any{"dispatch": results, "stop_dispatch": stopResults}})
		writeJSON(w, http.StatusBadGateway, map[string]any{"code": "service_dispatch_failed", "stream": failed, "dispatch": results, "stop_dispatch": stopResults})
		return
	}
	if body.VideoCoverStart != nil {
		if _, err := s.videoCovers.RecordStartApplied(r.Context(), stream.ID, body.VideoCoverStart.JobGeneration, body.VideoCoverStart.Active, body.VideoCoverStart.Revision); err != nil {
			failed, transitioned, transitionErr := claimStore.TransitionClaimedStreamStart(r.Context(), claimedStart.OwnershipClaim, "failed")
			if transitionErr != nil || !transitioned {
				writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "record_video_cover_start_failed"})
				return
			}
			stopRequest, cancel := detachedStopLifecycleRequest(r)
			defer cancel()
			stopResults := sanitizeDispatchResults(s.dispatcher.Stop(stopRequest.Context(), failed, primaryAssignments))
			current := currentFromContext(stopRequest.Context())
			s.writeAudit(stopRequest, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "streams.start", ResourceType: "stream", ResourceID: stream.ID, Result: "failure", Metadata: map[string]any{"reason": "record_video_cover_start_failed", "dispatch": results, "stop_dispatch": stopResults}})
			writeJSON(w, http.StatusInternalServerError, map[string]any{"code": "record_video_cover_start_failed", "stream": failed, "dispatch": results, "stop_dispatch": stopResults})
			return
		}
	}
	videoOverlayBurnIn := workerVideoOverlayBurnInNegotiated(results)
	if mediaRuntimeStore, ok := s.streams.(store.StreamMediaRuntimeStore); ok {
		if err := mediaRuntimeStore.SetStreamVideoOverlayBurnIn(r.Context(), stream.ID, videoOverlayBurnIn); err != nil {
			failed, transitioned, transitionErr := claimStore.TransitionClaimedStreamStart(r.Context(), claimedStart.OwnershipClaim, "failed")
			if transitionErr != nil || !transitioned {
				writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "save_stream_media_runtime_failed"})
				return
			}
			stopRequest, cancel := detachedStopLifecycleRequest(r)
			defer cancel()
			stopResults := sanitizeDispatchResults(s.dispatcher.Stop(stopRequest.Context(), failed, primaryAssignments))
			current := currentFromContext(stopRequest.Context())
			s.writeAudit(stopRequest, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "streams.start", ResourceType: "stream", ResourceID: stream.ID, Result: "failure", Metadata: map[string]any{"reason": "save_stream_media_runtime_failed", "dispatch": results, "stop_dispatch": stopResults}})
			writeJSON(w, http.StatusInternalServerError, map[string]any{"code": "save_stream_media_runtime_failed", "stream": failed, "dispatch": results, "stop_dispatch": stopResults})
			return
		}
	}
	if err := s.ensureYouTubeBroadcastLive(r.Context(), body.YouTubeRuntime); err != nil {
		s.failYouTubeLiveAPIStart(w, r, stream, primaryAssignments, claimedStart.OwnershipClaim, results, err)
		return
	}
	s.completeStreamStart(w, r, stream, primaryAssignments, body, results)
}

func archiveRunIDForStart(startedAt time.Time) string {
	return store.StreamArchiveRunIDForStart(startedAt)
}

func (s *Server) applyCaptionDispatchConfig(ctx context.Context, req *servicecall.StartRequest) error {
	profileID := strings.TrimSpace(req.CaptionProfileID)
	if profileID == "" {
		return nil
	}
	profile, err := s.profiles.GetProfile(ctx, store.ProfileCaption, profileID)
	if errors.Is(err, store.ErrNotFound) {
		return errCaptionProfileNotFound
	}
	if err != nil {
		return err
	}
	req.CaptionAudioFlushMS = configInt(profile.Config, "caption_audio_flush_ms")
	if req.CaptionAudioFlushMS < 10 || req.CaptionAudioFlushMS > 1000 {
		req.CaptionAudioFlushMS = 100
	}
	req.CaptionAudioMaxBatchPackets = configInt(profile.Config, "caption_audio_max_batch_packets")
	if req.CaptionAudioMaxBatchPackets < 1 || req.CaptionAudioMaxBatchPackets > 100 {
		req.CaptionAudioMaxBatchPackets = 5
	}
	req.UnresolvedSSRCBufferMS = configInt(profile.Config, "unresolved_ssrc_buffer_ms")
	if req.UnresolvedSSRCBufferMS < 0 || req.UnresolvedSSRCBufferMS > 5000 {
		req.UnresolvedSSRCBufferMS = 1000
	}
	return nil
}

// ensureYouTubeBroadcastLive handles the provider transition after a
// successful Encoder dispatch. AutoStart-enabled immediate broadcasts are
// initially left for YouTube to move directly into live after it observes
// ingest; the durable notification outbox polls that lifecycle and performs
// one fenced reconciliation if the provider remains at liveStarting.
// Explicit auto-start=false retains the operator-controlled transition path.
// Scheduled broadcasts remain under YouTube's own schedule.
func (s *Server) ensureYouTubeBroadcastLive(ctx context.Context, runtime map[string]any) error {
	if strings.ToLower(strings.TrimSpace(mapString(runtime, "mode"))) != "live_api" {
		return nil
	}
	if scheduledStart, ok := youtubeRuntimeScheduledStart(runtime); ok && scheduledStart.After(time.Now().UTC()) {
		return nil
	}
	// With YouTube AutoStart enabled, the provider must observe the Encoder
	// ingest before it can move the Broadcast through testing/into live. Do not
	// race that provider transition synchronously from the start request. The
	// durable Discord notification outbox polls lifecycle and delivers only
	// after YouTube reports live. Explicit auto-start=false keeps the legacy
	// operator-controlled transition path below.
	if mapBoolDefault(runtime, "enable_auto_start", true) {
		return nil
	}
	transitionClient, ok := s.youtubeLive.(ytlive.BroadcastTransitionClient)
	if !ok {
		// Keep narrow test doubles and older integrations source-compatible. The
		// production LiveAPIClient implements this optional capability.
		return nil
	}
	broadcastID := strings.TrimSpace(mapString(runtime, "broadcast_id"))
	oauthAccountID := strings.TrimSpace(mapString(runtime, "oauth_account_id"))
	if broadcastID == "" || oauthAccountID == "" {
		return errYouTubeLiveAPIStartFailed
	}
	credentials, err := s.youtubeOAuthCredentials(ctx, oauthAccountID)
	if err != nil {
		return err
	}
	transitionCtx, cancel := context.WithTimeout(ctx, youtubeLiveTransitionTimeout)
	defer cancel()
	request := ytlive.BroadcastTransitionRequest{Credentials: credentials, BroadcastID: broadcastID}
	var lastErr error
	for attempt := 0; attempt < youtubeLiveTransitionAttempts; attempt++ {
		if err := transitionClient.TransitionBroadcastLive(transitionCtx, request); err == nil {
			return nil
		} else {
			lastErr = err
		}
		if lifecycleClient, ok := s.youtubeLive.(ytlive.BroadcastLifecycleClient); ok {
			lifecycle, lifecycleErr := lifecycleClient.BroadcastLifecycle(transitionCtx, ytlive.BroadcastLifecycleRequest{Credentials: credentials, BroadcastID: broadcastID})
			if lifecycleErr == nil && strings.EqualFold(strings.TrimSpace(lifecycle), "live") {
				return nil
			}
		}
		if attempt+1 < youtubeLiveTransitionAttempts {
			delay := time.Duration(1<<attempt) * 500 * time.Millisecond
			timer := time.NewTimer(delay)
			select {
			case <-transitionCtx.Done():
				timer.Stop()
				lastErr = transitionCtx.Err()
				attempt = youtubeLiveTransitionAttempts
			case <-timer.C:
			}
		}
	}
	log.Printf("youtube live api broadcast transition failed: broadcast_id=%s error_type=%T", broadcastID, lastErr)
	return errYouTubeLiveAPIStartFailed
}

// failYouTubeLiveAPIStart prevents a provider Broadcast from being left
// scheduled/live after the Encoder accepted the stream but YouTube could not
// be moved to live. Downstream stop and provider completion use a detached,
// bounded context so a cancelled Discord auto-start request cannot abandon
// cleanup.
func (s *Server) failYouTubeLiveAPIStart(w http.ResponseWriter, r *http.Request, stream store.Stream, assignments []store.RegisteredService, ownership store.StreamStartOwnershipClaim, dispatch []servicecall.DispatchResult, transitionErr error) {
	log.Printf("youtube live api start failed: stream_id=%s error_type=%T", stream.ID, transitionErr)
	claimStore, ok := s.streams.(store.StreamStartClaimStore)
	if !ok {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"code": "stream_start_claim_unavailable"})
		return
	}
	failed, transitioned, err := claimStore.TransitionClaimedStreamStart(r.Context(), ownership, "failed")
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "update_stream_failed"})
		return
	}
	if !transitioned {
		s.writeStartSupersededResponse(w, r, failed, dispatch, assignments)
		return
	}
	stopRequest, cancel := detachedStopLifecycleRequest(r)
	defer cancel()
	stopDispatch := sanitizeDispatchResults(s.dispatcher.Stop(stopRequest.Context(), failed, assignments))
	metadata := map[string]any{
		"reason":          errYouTubeLiveAPIStartFailed.Error(),
		"dispatch":        dispatch,
		"stop_dispatch":   stopDispatch,
		"transition_code": errYouTubeLiveAPIStartFailed.Error(),
	}
	if completeMetadata, completeErr := s.completeYouTubeRuntime(stopRequest.Context(), stream.ID, true); completeErr != nil {
		metadata["youtube_complete_error"] = "youtube_live_api_complete_failed"
		current := currentFromContext(stopRequest.Context())
		s.writeAudit(stopRequest, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "youtube.complete", ResourceType: "stream", ResourceID: stream.ID, Result: "failure", Metadata: map[string]any{"reason": "youtube_live_api_complete_failed", "trigger": "start_transition_failed"}})
	} else if len(completeMetadata) > 0 {
		metadata["youtube_complete"] = completeMetadata
		current := currentFromContext(stopRequest.Context())
		s.writeAudit(stopRequest, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "youtube.complete", ResourceType: "stream", ResourceID: stream.ID, Result: "success", Metadata: map[string]any{"trigger": "start_transition_failed", "complete": completeMetadata}})
	}
	current := currentFromContext(stopRequest.Context())
	s.writeAudit(stopRequest, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "streams.start", ResourceType: "stream", ResourceID: stream.ID, Result: "failure", Metadata: metadata})
	writeJSON(w, http.StatusBadGateway, map[string]any{"code": errYouTubeLiveAPIStartFailed.Error(), "stream": failed, "dispatch": dispatch, "stop_dispatch": stopDispatch})
}

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
