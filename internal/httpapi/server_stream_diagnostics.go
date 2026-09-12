package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/example/autostream-control-panel/internal/servicecall"
	"github.com/example/autostream-control-panel/internal/store"
)

var requiredWorkerEventServiceTypes = []string{"worker"}

func (s *Server) streamAudioStatus(w http.ResponseWriter, r *http.Request) {
	stream, err := s.streams.GetStream(r.Context(), r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "get_stream_failed"})
		return
	}
	assignments, err := s.streamAssignments(r.Context(), stream.ID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "list_stream_assignments_failed"})
		return
	}
	primaryAssignments := primaryStreamAssignments(assignments)
	if missing := missingServiceTypes(primaryAssignments, requiredRetryUploadServiceTypes); len(missing) > 0 {
		writeJSON(w, http.StatusConflict, map[string]any{"code": "missing_stream_assignments", "missing_service_types": missing})
		return
	}
	result := s.dispatcher.AudioStatus(r.Context(), stream, primaryAssignments)
	if !result.Success {
		writeJSON(w, http.StatusBadGateway, map[string]any{"code": "service_dispatch_failed", "dispatch": result})
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) streamEncoderPreflight(w http.ResponseWriter, r *http.Request) {
	stream, err := s.streams.GetStream(r.Context(), r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "get_stream_failed"})
		return
	}
	assignments, err := s.streamAssignments(r.Context(), stream.ID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "list_stream_assignments_failed"})
		return
	}
	primaryAssignments := primaryStreamAssignments(assignments)
	if missing := missingServiceTypes(primaryAssignments, requiredRetryUploadServiceTypes); len(missing) > 0 {
		writeJSON(w, http.StatusConflict, map[string]any{"code": "missing_stream_assignments", "missing_service_types": missing})
		return
	}
	if err := s.validateYouTubeLiveAPIOutputRelay(r.Context(), primaryAssignments, &servicecall.StartRequest{YouTubeOutputID: stream.YouTubeOutputID}); err != nil {
		code := youtubeOutputCode(err)
		writeJSON(w, youtubeOutputStatus(err), map[string]string{"code": code})
		return
	}
	result := s.dispatcher.EncoderPreflight(r.Context(), stream, primaryAssignments)
	result = servicecall.RedactServicePreflightResult(result)
	if !result.Success {
		writeJSON(w, http.StatusBadGateway, map[string]any{"code": "service_dispatch_failed", "dispatch": result})
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) streamWorkerEvents(w http.ResponseWriter, r *http.Request) {
	stream, err := s.streams.GetStream(r.Context(), r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "get_stream_failed"})
		return
	}
	assignments, err := s.streamAssignments(r.Context(), stream.ID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "list_stream_assignments_failed"})
		return
	}
	primaryAssignments := primaryStreamAssignments(assignments)
	if missing := missingServiceTypes(primaryAssignments, requiredRetryUploadServiceTypes); len(missing) > 0 {
		writeJSON(w, http.StatusConflict, map[string]any{"code": "missing_stream_assignments", "missing_service_types": missing})
		return
	}
	result := servicecall.RedactWorkerEventsResult(s.dispatcher.WorkerEvents(r.Context(), stream, primaryAssignments))
	if !result.Success {
		writeJSON(w, http.StatusBadGateway, map[string]any{"code": "service_dispatch_failed", "dispatch": result})
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) sendWorkerTestEvent(w http.ResponseWriter, r *http.Request) {
	stream, err := s.streams.GetStream(r.Context(), r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "get_stream_failed"})
		return
	}
	var body servicecall.WorkerEventRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "bad_request"})
		return
	}
	assignments, err := s.streamAssignments(r.Context(), stream.ID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "list_stream_assignments_failed"})
		return
	}
	primaryAssignments := primaryStreamAssignments(assignments)
	if missing := missingServiceTypes(primaryAssignments, requiredWorkerEventServiceTypes); len(missing) > 0 {
		writeJSON(w, http.StatusConflict, map[string]any{"code": "missing_stream_assignments", "missing_service_types": missing})
		return
	}
	result := s.dispatcher.SendWorkerEvent(r.Context(), stream, primaryAssignments, body)
	current := currentFromContext(r.Context())
	if !result.Success {
		s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "streams.worker_event_test", ResourceType: "stream", ResourceID: stream.ID, Result: "failure", Metadata: map[string]any{"event_type": body.EventType, "dispatch": result}})
		writeJSON(w, http.StatusBadGateway, map[string]any{"code": "service_dispatch_failed", "dispatch": result})
		return
	}
	s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "streams.worker_event_test", ResourceType: "stream", ResourceID: stream.ID, Result: "success", Metadata: map[string]any{"event_type": body.EventType, "dispatch": result}})
	writeJSON(w, http.StatusAccepted, map[string]any{"dispatch": result})
}

func (s *Server) streamLogs(w http.ResponseWriter, r *http.Request) {
	logs, err := s.streams.ListStreamLogs(r.Context(), r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "list_stream_logs_failed"})
		return
	}
	writeJSON(w, http.StatusOK, logs)
}

func (s *Server) streamLogHistory(w http.ResponseWriter, r *http.Request) {
	limit := 500
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > 1000 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_limit"})
			return
		}
		limit = parsed
	}
	before, beforeID, err := parseStreamLogHistoryCursor(r)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_history_cursor"})
		return
	}
	logs, err := s.streams.ListStreamLogHistory(r.Context(), limit, before, beforeID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "list_stream_logs_failed"})
		return
	}
	writeJSON(w, http.StatusOK, logs)
}

func parseStreamLogHistoryCursor(r *http.Request) (time.Time, string, error) {
	rawBefore := strings.TrimSpace(r.URL.Query().Get("before"))
	beforeID := strings.TrimSpace(r.URL.Query().Get("before_id"))
	if rawBefore == "" && beforeID == "" {
		return time.Time{}, "", nil
	}
	if rawBefore == "" || beforeID == "" {
		return time.Time{}, "", errors.New("before and before_id are both required")
	}
	before, err := time.Parse(time.RFC3339Nano, rawBefore)
	if err != nil {
		return time.Time{}, "", err
	}
	return before.UTC(), beforeID, nil
}
