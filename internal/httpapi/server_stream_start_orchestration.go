package httpapi

import (
	"context"
	"errors"
	"fmt"
	"github.com/example/autostream-control-panel/internal/servicecall"
	"github.com/example/autostream-control-panel/internal/store"
	"log"
	"net/http"
	"strings"
	"time"
)

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
