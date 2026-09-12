package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/example/autostream-control-panel/internal/servicecall"
	"github.com/example/autostream-control-panel/internal/store"
)

var requiredStartServiceTypes = []string{"discord_bot", "worker", "encoder_recorder"}

var requiredStopServiceTypes = []string{"discord_bot", "worker", "encoder_recorder"}

func (s *Server) markStreamFailed(w http.ResponseWriter, r *http.Request) {
	s.transition(w, r, r.PathValue("id"), "failed", "streams.mark_failed")
}

func (s *Server) transition(w http.ResponseWriter, r *http.Request, id, status, action string) {
	s.transitionWithDispatch(w, r, id, status, action, nil)
}

func (s *Server) transitionWithDispatch(w http.ResponseWriter, r *http.Request, id, status, action string, dispatch []servicecall.DispatchResult) {
	dispatch = sanitizeDispatchResults(dispatch)
	stream, err := s.streams.UpdateStreamStatus(r.Context(), id, status)
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "update_stream_failed"})
		return
	}
	current := currentFromContext(r.Context())
	metadata := map[string]any{"status": status}
	if dispatch != nil {
		metadata["dispatch"] = dispatch
	}
	s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: action, ResourceType: "stream", ResourceID: stream.ID, Result: "success", Metadata: metadata})
	if dispatch != nil {
		writeJSON(w, http.StatusOK, map[string]any{"stream": stream, "dispatch": dispatch})
		return
	}
	writeJSON(w, http.StatusOK, stream)
}

func (s *Server) streamAssignments(ctx context.Context, streamID string) ([]store.RegisteredService, error) {
	if s.services == nil {
		return nil, nil
	}
	return s.services.ListStreamAssignments(ctx, streamID)
}

func normalizeAssignmentRole(role string) string {
	role = strings.TrimSpace(strings.ToLower(role))
	if role == "standby" {
		return "standby"
	}
	return "primary"
}

func primaryStreamAssignments(assignments []store.RegisteredService) []store.RegisteredService {
	out := make([]store.RegisteredService, 0, len(assignments))
	for _, assignment := range assignments {
		if normalizeAssignmentRole(assignment.AssignmentRole) == "primary" {
			assignment.AssignmentRole = "primary"
			out = append(out, assignment)
		}
	}
	return out
}

func missingServiceTypes(assignments []store.RegisteredService, required []string) []string {
	assigned := make(map[string]bool, len(assignments))
	for _, service := range assignments {
		assigned[strings.ToLower(strings.TrimSpace(service.ServiceType))] = true
	}
	missing := make([]string, 0, len(required))
	for _, serviceType := range required {
		if !assigned[strings.ToLower(strings.TrimSpace(serviceType))] {
			missing = append(missing, serviceType)
		}
	}
	return missing
}

func (s *Server) archiveArtifactAssignments(ctx context.Context, streamID string, artifact store.StreamArtifact) ([]store.RegisteredService, error) {
	assignments, err := s.streamAssignments(ctx, streamID)
	if err != nil {
		return nil, err
	}
	primaryAssignments := primaryStreamAssignments(assignments)
	if len(missingServiceTypes(primaryAssignments, requiredRetryUploadServiceTypes)) == 0 {
		return primaryAssignments, nil
	}
	if s.services == nil || strings.TrimSpace(artifact.SourceServiceID) == "" {
		return primaryAssignments, nil
	}
	service, err := s.services.GetService(ctx, strings.TrimSpace(artifact.SourceServiceID))
	if errors.Is(err, store.ErrNotFound) {
		return primaryAssignments, nil
	}
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(service.ServiceType) != "encoder_recorder" {
		return primaryAssignments, nil
	}
	service.AssignmentRole = "primary"
	for _, assigned := range primaryAssignments {
		if assigned.ServiceID == service.ServiceID {
			return primaryAssignments, nil
		}
	}
	return append(primaryAssignments, service), nil
}

func hasDispatchFailure(results []servicecall.DispatchResult) bool {
	for _, result := range results {
		if !result.Success {
			return true
		}
	}
	return false
}

// normalizeManualStopAlreadyStoppedResults is deliberately limited to a normal
// operator Stop after the stream has claimed stopping. Worker reports its exact
// no-active-job receipt when no job remains. Encoder's no-process reply is only
// equivalent for a stream with no durable static-relay claim: a fixed relay
// possibly dispatched to YouTube needs a positive Encoder acknowledgement
// before it may complete or release its binding. Preserve the raw HTTP status
// and code as audit evidence while clearing the failure presentation.
func (s *Server) normalizeManualStopAlreadyStoppedResults(ctx context.Context, streamID string, results []servicecall.DispatchResult) []servicecall.DispatchResult {
	encoderNoProcessMayBeNormalized := false
	for _, result := range results {
		if strings.EqualFold(strings.TrimSpace(result.ServiceType), "encoder_recorder") &&
			result.StatusCode == http.StatusNotFound && strings.TrimSpace(result.Code) == "stream_not_running" {
			claimStore, ok := s.streams.(store.StreamYouTubeRelayBindingClaimStore)
			if ok {
				_, err := claimStore.GetStreamYouTubeRelayBindingClaimForStream(ctx, strings.TrimSpace(streamID))
				encoderNoProcessMayBeNormalized = errors.Is(err, store.ErrNotFound)
			}
			break
		}
	}

	for index := range results {
		result := &results[index]
		workerNoActiveJob := strings.EqualFold(strings.TrimSpace(result.ServiceType), "worker") &&
			result.StatusCode == http.StatusConflict && strings.TrimSpace(result.Code) == "no_active_stream_job"
		encoderNoProcess := encoderNoProcessMayBeNormalized &&
			strings.EqualFold(strings.TrimSpace(result.ServiceType), "encoder_recorder") &&
			result.StatusCode == http.StatusNotFound && strings.TrimSpace(result.Code) == "stream_not_running"
		if !workerNoActiveJob && !encoderNoProcess {
			continue
		}
		result.Success = true
		result.Error = ""
		result.FailurePhase = ""
		result.ErrorClass = ""
	}
	return results
}

// relayStaticEncoderStopConfirmed separates the reusable fixed-relay ownership
// fence from best-effort stops for Worker and Discord. A possibly-dispatched
// static start requires a positive Encoder Stop acknowledgement. Only a claim
// durably marked not_dispatched may treat the Encoder's exact no-process reply
// as equivalent evidence, because no Start request was ever sent in that case.
func relayStaticEncoderStopConfirmed(assignments []store.RegisteredService, results []servicecall.DispatchResult, dispatchState string) (bool, []servicecall.DispatchResult) {
	encoderIDs := make(map[string]struct{})
	for _, assignment := range assignments {
		if strings.EqualFold(strings.TrimSpace(assignment.ServiceType), "encoder_recorder") {
			encoderIDs[strings.TrimSpace(assignment.ServiceID)] = struct{}{}
		}
	}
	if len(encoderIDs) == 0 {
		return false, nil
	}

	byServiceID := make(map[string][]servicecall.DispatchResult, len(results))
	warnings := make([]servicecall.DispatchResult, 0)
	for _, result := range results {
		serviceID := strings.TrimSpace(result.ServiceID)
		byServiceID[serviceID] = append(byServiceID[serviceID], result)
		if _, isEncoder := encoderIDs[serviceID]; !isEncoder && !result.Success {
			warnings = append(warnings, result)
		}
	}

	for encoderID := range encoderIDs {
		encoderResults := byServiceID[encoderID]
		if len(encoderResults) == 0 {
			return false, warnings
		}
		for _, result := range encoderResults {
			if result.Success {
				continue
			}
			if strings.TrimSpace(dispatchState) == store.YouTubeRelayBindingClaimDispatchStateNotDispatched &&
				result.StatusCode == http.StatusNotFound && strings.TrimSpace(result.Code) == "stream_not_running" {
				continue
			}
			return false, warnings
		}
	}
	return true, warnings
}

func sanitizeDispatchResults(results []servicecall.DispatchResult) []servicecall.DispatchResult {
	if results == nil {
		return nil
	}
	out := make([]servicecall.DispatchResult, len(results))
	for i, result := range results {
		out[i] = result
		out[i].Error = sanitizeDispatchError(result.Error)
	}
	return out
}

func workerVideoOverlayBurnInNegotiated(results []servicecall.DispatchResult) bool {
	for _, result := range results {
		if result.Success && result.VideoOverlayBurnInNegotiated {
			return true
		}
	}
	return false
}

func sanitizeDispatchError(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	lower := strings.ToLower(value)
	if strings.Contains(lower, "secret") ||
		strings.Contains(lower, "token") ||
		strings.Contains(lower, "authorization") ||
		strings.Contains(lower, "bearer ") ||
		strings.Contains(lower, "discord.com/api/webhooks") ||
		strings.Contains(lower, "hooks.slack.com/services") ||
		strings.Contains(lower, "://") {
		return "service dispatch failed"
	}
	return value
}
