package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/example/autostream-control-panel/internal/store"
	"github.com/example/autostream-control-panel/internal/streamvisual"
)

func (s *Server) registerStreamRoutes() {
	s.mux.HandleFunc("GET /streams", s.requirePermission("streams.read", s.listStreams))
	s.mux.HandleFunc("GET /archive/streams", s.requirePermission("archives.read", s.listArchiveStreams))
	s.mux.HandleFunc("GET /archive/processing-streams", s.requirePermission("archives.read", s.listArchiveProcessingStreams))
	s.mux.HandleFunc("POST /streams", s.requirePermission("streams.create", s.createStream))
	s.mux.HandleFunc("POST /media-assets/upload-sessions", s.requireAnyPermission([]string{"streams.create", "streams.update"}, s.createMediaUploadSession))
	s.mux.HandleFunc("POST /media-assets", s.requireAnyPermission([]string{"streams.create", "streams.update"}, s.uploadMediaAsset))
	s.mux.HandleFunc("GET /media-assets/{id}", s.requireAnyPermission([]string{"streams.create", "streams.update"}, s.getMediaAsset))
	s.mux.HandleFunc("POST /media-assets/{id}/variants", s.requireAnyPermission([]string{"streams.create", "streams.update"}, s.createMediaAssetVariant))
	s.mux.HandleFunc("DELETE /media-assets/{id}", s.requireAnyPermission([]string{"streams.create", "streams.update"}, s.deleteMediaAsset))
	s.mux.HandleFunc("GET /internal/streams/{stream_id}/media-assets/{variant_id}", s.internalMediaAsset)
	s.mux.HandleFunc("GET /streams/{id}", s.requirePermission("streams.read", s.getStream))
	s.mux.HandleFunc("GET /streams/{id}/visual-settings", s.requirePermission("streams.read", s.getStreamVisualSettings))
	s.mux.HandleFunc("PUT /streams/{id}/visual-settings", s.requirePermission("streams.update", s.updateStreamVisualSettings))
	s.mux.HandleFunc("GET /streams/{id}/video-cover-state", s.requirePermission("streams.read", s.getVideoCoverState))
	s.mux.HandleFunc("PUT /streams/{id}/video-cover-state", s.requirePermission("", s.updateVideoCoverState))
	s.mux.HandleFunc("GET /video-cover-presets", s.requirePermission("video_cover_presets.read", s.listVideoCoverPresets))
	s.mux.HandleFunc("POST /video-cover-presets", s.requirePermission("video_cover_presets.create", s.createVideoCoverPreset))
	s.mux.HandleFunc("GET /video-cover-presets/{id}", s.requirePermission("video_cover_presets.read", s.getVideoCoverPreset))
	s.mux.HandleFunc("PUT /video-cover-presets/{id}", s.requirePermission("video_cover_presets.update", s.updateVideoCoverPreset))
	s.mux.HandleFunc("DELETE /video-cover-presets/{id}", s.requirePermission("video_cover_presets.delete", s.deleteVideoCoverPreset))
	s.mux.HandleFunc("DELETE /streams/{id}", s.requirePermission("streams.delete", s.deleteStream))
	s.mux.HandleFunc("GET /streams/{id}/external-e2e-config", s.requirePermission("streams.read", s.externalE2EConfig))
	s.mux.HandleFunc("PUT /streams/{id}/settings", s.requirePermission("streams.update", s.updateStreamSettings))
	s.mux.HandleFunc("PUT /streams/{id}/runtime-settings", s.requirePermission("streams.update", s.updateStreamRuntimeSettings))
	s.mux.HandleFunc("POST /streams/{id}/start-readiness", s.requirePermission("streams.start", s.startReadiness))
	s.mux.HandleFunc("POST /streams/{id}/start", s.requirePermission("streams.start", s.startStream))
	s.mux.HandleFunc("GET /streams/{id}/youtube-live-notification", s.requirePermission("streams.read", s.getDiscordYouTubeLiveNotification))
	s.mux.HandleFunc("POST /streams/{id}/youtube-live-notifications/{notification_id}/recover", s.requirePermission("streams.start", s.recoverDiscordYouTubeLiveNotification))
	s.mux.HandleFunc("POST /streams/{id}/stop", s.requirePermission("streams.stop", s.stopStream))
	s.mux.HandleFunc("POST /streams/{id}/force-stop", s.requirePermission("streams.stop", s.forceStopStream))
	s.mux.HandleFunc("POST /streams/{id}/youtube/complete", s.requirePermission("streams.stop", s.completeYouTubeStream))
	// This is intentionally guarded by streams.stop, like the existing manual
	// YouTube completion route. It is an exceptional, explicitly confirmed
	// recovery path for a static fixed relay; normal completion must continue
	// through /youtube/complete or the retry loop.
	s.mux.HandleFunc("POST /streams/{id}/youtube/relay-static/recovery/resolve", s.requirePermission("streams.stop", s.resolveYouTubeRelayStaticRecovery))
	s.mux.HandleFunc("POST /streams/{id}/mark-failed", s.requirePermission("streams.update", s.markStreamFailed))
	s.mux.HandleFunc("POST /streams/{id}/retry-upload", s.requirePermission("streams.retry_upload", s.retryUpload))
	s.mux.HandleFunc("GET /streams/{id}/encoder-preflight", s.requirePermission("streams.read", s.streamEncoderPreflight))
	s.mux.HandleFunc("GET /streams/{id}/preview/{name}", s.requirePermission("streams.read", s.streamPreviewAsset))
	s.mux.HandleFunc("POST /streams/{id}/preview-links", s.requirePermission("streams.read", s.createStreamPreviewLink))
	s.mux.HandleFunc("GET /streams/{id}/audio-status", s.requirePermission("streams.read", s.streamAudioStatus))
	s.mux.HandleFunc("GET /streams/{id}/worker-events", s.requirePermission("streams.read", s.streamWorkerEvents))
	s.mux.HandleFunc("POST /streams/{id}/worker-events/test", s.requirePermission("streams.update", s.sendWorkerTestEvent))
	s.mux.HandleFunc("GET /streams/{id}/logs", s.requirePermission("logs.read", s.streamLogs))
	s.mux.HandleFunc("GET /stream-logs", s.requirePermission("logs.read", s.streamLogHistory))
}

func (s *Server) listStreams(w http.ResponseWriter, r *http.Request) {
	items, err := s.streams.ListStreams(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "list_streams_failed"})
		return
	}
	items, err = s.streamsWithAssignedNodes(r.Context(), items)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "list_stream_assignments_failed"})
		return
	}
	writeJSON(w, http.StatusOK, items)
}

func (s *Server) listArchiveStreams(w http.ResponseWriter, r *http.Request) {
	archiveStore, ok := s.streams.(store.ArchiveStreamStore)
	if !ok {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "archive_stream_store_not_configured"})
		return
	}
	items, err := archiveStore.ListArchiveStreams(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "list_archive_streams_failed"})
		return
	}
	items, err = s.streamsWithAssignedNodes(r.Context(), items)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "list_stream_assignments_failed"})
		return
	}
	writeJSON(w, http.StatusOK, items)
}

func (s *Server) listArchiveProcessingStreams(w http.ResponseWriter, r *http.Request) {
	processingStore, ok := s.streams.(store.ArchiveProcessingStreamStore)
	if !ok {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "archive_processing_stream_store_not_configured"})
		return
	}
	items, err := processingStore.ListArchiveProcessingStreams(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "list_archive_processing_streams_failed"})
		return
	}
	items, err = s.streamsWithAssignedNodes(r.Context(), items)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "list_stream_assignments_failed"})
		return
	}
	writeJSON(w, http.StatusOK, items)
}

type streamSettingsRequest struct {
	Name               string  `json:"name,omitempty"`
	ScheduledStartAt   string  `json:"scheduled_start_at,omitempty"`
	ScheduledEndAt     string  `json:"scheduled_end_at,omitempty"`
	DiscordConfigID    string  `json:"discord_config_id,omitempty"`
	AutoStartTrigger   string  `json:"auto_start_trigger,omitempty"`
	EncoderProfileID   string  `json:"encoder_profile_id,omitempty"`
	CaptionProfileID   string  `json:"caption_profile_id,omitempty"`
	OverlayProfileID   string  `json:"overlay_profile_id,omitempty"`
	EncoderAudioGainDB float64 `json:"encoder_audio_gain_db"`
	ArchiveProfileID   string  `json:"archive_profile_id,omitempty"`
	// Direct archive fields predate archive_profile_id. Keep their presence so a
	// normal profile-based edit can omit them without erasing legacy state, while
	// an API caller can still deliberately clear a field with "" or false.
	ArchiveOAuthAccountID *string `json:"archive_oauth_account_id,omitempty"`
	ArchiveFolderID       *string `json:"archive_folder_id,omitempty"`
	ArchiveSharedDrive    *bool   `json:"archive_shared_drive,omitempty"`
	ArchiveSharedDriveID  *string `json:"archive_shared_drive_id,omitempty"`
	ArchiveFileName       *string `json:"archive_file_name,omitempty"`
	ArchiveRetentionDays  *int    `json:"archive_retention_days,omitempty"`
	YouTubeOutputID       string  `json:"youtube_output_id,omitempty"`
	EncoderInputURL       string  `json:"encoder_input_url,omitempty"`
	// Assignment IDs are presence-aware for edits: omitted preserves the
	// existing assignment, while an explicit empty string unassigns that role.
	EncoderServiceID *string `json:"encoder_service_id,omitempty"`
	WorkerServiceID  *string `json:"worker_service_id,omitempty"`
}

func archiveRequestString(value *string) string {
	if value == nil {
		return ""
	}
	return strings.TrimSpace(*value)
}

func archiveRequestBool(value *bool) bool {
	return value != nil && *value
}

func archiveRequestInt(value *int) int {
	if value == nil {
		return 0
	}
	return *value
}

const autoStartTriggerDiscordVoiceJoin = "discord_voice_join"

var createStreamV2Fields = map[string]struct{}{
	"name": {}, "scheduled_start_at": {}, "scheduled_end_at": {},
	"discord_config_id": {}, "auto_start_trigger": {},
	"encoder_profile_id": {}, "caption_profile_id": {}, "overlay_profile_id": {},
	"encoder_audio_gain_db": {}, "archive_profile_id": {},
	"archive_oauth_account_id": {}, "archive_folder_id": {}, "archive_shared_drive": {},
	"archive_shared_drive_id": {}, "archive_file_name": {}, "archive_retention_days": {},
	"youtube_output_id": {}, "encoder_input_url": {},
	"upload_session_id": {}, "visual_settings": {},
}

func (s *Server) createStream(w http.ResponseWriter, r *http.Request) {
	var fields map[string]json.RawMessage
	if err := decodeSingleJSON(r, &fields); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "bad_request"})
		return
	}
	for name := range fields {
		if _, ok := createStreamV2Fields[name]; !ok {
			writeJSON(w, http.StatusBadRequest, map[string]string{"code": "bad_request"})
			return
		}
	}
	encoded, err := json.Marshal(fields)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "bad_request"})
		return
	}
	var body streamSettingsRequest
	if err := json.Unmarshal(encoded, &body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "bad_request"})
		return
	}
	body.Name = strings.TrimSpace(body.Name)
	if body.Name == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "name_required"})
		return
	}
	settings, code := streamSettingsFromRequest(body)
	if code != "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": code})
		return
	}
	if err := s.validateStreamSettingsReferences(r.Context(), settings); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": streamSettingsReferenceCode(err)})
		return
	}
	current := currentFromContext(r.Context())
	var stream store.Stream
	_, visualSettingsPresent := fields["visual_settings"]
	_, uploadSessionPresent := fields["upload_session_id"]
	if !visualSettingsPresent {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "visual_settings_required"})
		return
	}
	if uploadSessionPresent && streamArchiveDirectRequested(body) {
		writeJSON(w, http.StatusConflict, map[string]string{"code": "stream_draft_archive_create_unsupported"})
		return
	}
	creator, ok := s.streamVisual.(streamvisual.AtomicCreator)
	if !ok {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"code": "stream_visual_create_unavailable"})
		return
	}
	var envelope createStreamVisualEnvelope
	if err := json.Unmarshal(encoded, &envelope); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "bad_request"})
		return
	}
	visualUpdate := streamvisual.Update{}
	if len(envelope.VisualSettings) == 0 || string(envelope.VisualSettings) == "null" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "visual_settings_required"})
		return
	}
	decoder := json.NewDecoder(bytes.NewReader(envelope.VisualSettings))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&visualUpdate); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "bad_request"})
		return
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "bad_request"})
		return
	}
	stream, _, err = creator.CreateStream(r.Context(), current.User.ID, streamvisual.Create{Name: body.Name, UploadSessionID: envelope.UploadSessionID, Settings: visualUpdate, StreamSettings: settings})
	if err != nil {
		writeStreamVisualError(w, err)
		return
	}
	if streamArchiveDirectRequested(body) {
		settings, err = s.materializeStreamArchiveSettings(r.Context(), stream, settings, body)
		if err != nil {
			writeJSON(w, streamArchiveSettingsStatus(err), map[string]string{"code": streamArchiveSettingsCode(err)})
			return
		}
		stream, err = s.streams.UpdateStreamSettings(r.Context(), stream.ID, settings)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "update_stream_settings_failed"})
			return
		}
	}
	// Creating a stream persists configuration only. Runtime ownership changes
	// remain an explicit settings update so a new inactive slot cannot steal a
	// service assignment from a live stream or an in-flight archive package.
	s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "streams.create", ResourceType: "stream", ResourceID: stream.ID, Result: "success", Metadata: streamSettingsAuditMetadata(stream)})
	writeJSON(w, http.StatusCreated, stream)
}

func (s *Server) getStream(w http.ResponseWriter, r *http.Request) {
	stream, err := s.streams.GetStream(r.Context(), r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "get_stream_failed"})
		return
	}
	stream, err = s.streamWithAssignedNodes(r.Context(), stream)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "list_stream_assignments_failed"})
		return
	}
	writeJSON(w, http.StatusOK, stream)
}

func (s *Server) deleteStream(w http.ResponseWriter, r *http.Request) {
	streamID := strings.TrimSpace(r.PathValue("id"))
	current := currentFromContext(r.Context())
	auditFailure := func(reason string) {
		s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "streams.delete", ResourceType: "stream", ResourceID: streamID, Result: "failure", Metadata: map[string]any{"reason": reason}})
	}
	stream, err := s.streams.GetStream(r.Context(), streamID)
	if errors.Is(err, store.ErrNotFound) {
		auditFailure("not_found")
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found"})
		return
	}
	if err != nil {
		auditFailure("get_stream_failed")
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "get_stream_failed"})
		return
	}
	if isActiveStreamStatus(stream.Status) {
		s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "streams.delete", ResourceType: "stream", ResourceID: stream.ID, Result: "failure", Metadata: map[string]any{"reason": "stream_active", "status": stream.Status}})
		writeJSON(w, http.StatusConflict, map[string]string{"code": "stream_active", "status": stream.Status})
		return
	}
	if claimed, err := s.youtubeRelayBindingStreamClaimActive(r.Context(), stream.ID); err != nil {
		auditFailure("youtube_relay_binding_claim_check_failed")
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "youtube_relay_binding_claim_check_failed"})
		return
	} else if claimed {
		auditFailure("youtube_relay_binding_release_pending")
		writeJSON(w, http.StatusConflict, map[string]string{"code": "youtube_relay_binding_release_pending"})
		return
	}
	if err := s.streams.DeleteStream(r.Context(), stream.ID); errors.Is(err, store.ErrNotFound) {
		auditFailure("not_found")
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found"})
		return
	} else if errors.Is(err, store.ErrYouTubeRelayBindingClaimActive) {
		auditFailure("youtube_relay_binding_release_pending")
		writeJSON(w, http.StatusConflict, map[string]string{"code": "youtube_relay_binding_release_pending"})
		return
	} else if code, status, handled := serviceAssignmentHTTPError(err, true); handled {
		auditFailure(code)
		writeJSON(w, status, map[string]string{"code": code})
		return
	} else if err != nil {
		auditFailure("delete_stream_failed")
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "delete_stream_failed"})
		return
	}
	s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "streams.delete", ResourceType: "stream", ResourceID: stream.ID, Result: "success", Metadata: map[string]any{"name": stream.Name, "status": stream.Status}})
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted", "stream_id": stream.ID})
}

func (s *Server) streamsWithAssignedNodes(ctx context.Context, streams []store.Stream) ([]store.Stream, error) {
	for index := range streams {
		stream, err := s.streamWithAssignedNodes(ctx, streams[index])
		if err != nil {
			return nil, err
		}
		streams[index] = stream
	}
	return streams, nil
}

func (s *Server) streamWithAssignedNodes(ctx context.Context, stream store.Stream) (store.Stream, error) {
	stream.AssignedWorkerID = ""
	stream.AssignedEncoderID = ""
	if s.services == nil {
		return stream, nil
	}
	assignments, err := s.services.ListStreamAssignments(ctx, stream.ID)
	if err != nil {
		return store.Stream{}, err
	}
	for _, assignment := range assignments {
		if normalizeAssignmentRole(assignment.AssignmentRole) != "primary" {
			continue
		}
		switch strings.TrimSpace(assignment.ServiceType) {
		case "worker":
			if stream.AssignedWorkerID == "" {
				stream.AssignedWorkerID = strings.TrimSpace(assignment.ServiceID)
			}
		case "encoder_recorder":
			if stream.AssignedEncoderID == "" {
				stream.AssignedEncoderID = strings.TrimSpace(assignment.ServiceID)
			}
		}
	}
	return stream, nil
}
