package httpapi

import (
	"errors"
	"net/http"
	"time"

	"github.com/example/autostream-control-panel/internal/servicecall"
	"github.com/example/autostream-control-panel/internal/store"
)

func (s *Server) startReadiness(w http.ResponseWriter, r *http.Request) {
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
	applyStreamSettingsDefaults(stream, &body)
	if err := s.materializeDiscordStartTarget(r.Context(), stream.ID, &body); err != nil {
		writeJSON(w, http.StatusConflict, map[string]string{"code": "discord_target_snapshot_failed"})
		return
	}
	assignments, err := s.streamAssignments(r.Context(), stream.ID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "list_stream_assignments_failed"})
		return
	}
	primaryAssignments := primaryStreamAssignments(assignments)
	missing := missingServiceTypes(primaryAssignments, requiredStartServiceTypes)
	issues := []servicecall.ReadinessIssue{}
	if len(missing) == 0 {
		controlIssues, controlErr := s.controlPlatformReadinessIssues(r.Context(), stream.ID, primaryAssignments)
		if controlErr != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "control_platform_readiness_failed"})
			return
		}
		issues = append(issues, controlIssues...)
		if job, active, err := s.activeSystemUpdateForStreamTargets(r.Context(), primaryAssignments); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "check_service_update_failed"})
			return
		} else if active {
			issues = append(issues, servicecall.ReadinessIssue{ServiceID: job.TargetID, ServiceType: job.TargetServiceType, Code: "service_update_in_progress", Message: "A required service is being updated."})
		}
		if err := s.applyDiscordConfig(r.Context(), primaryAssignments, &body); err != nil {
			writeJSON(w, discordConfigStatus(err), map[string]string{"code": discordConfigCode(err)})
			return
		}
		if servicecall.WorkerVideoCapabilitiesEnabled(primaryAssignments) {
			if err := s.applyEncoderVideoProfile(r.Context(), &body); err != nil {
				issues = append(issues, servicecall.ReadinessIssue{
					ServiceType: "worker", Code: "worker_video_encoder_profile_unsupported",
					Message: "The selected Encoder profile is not supported by the Worker scene renderer.",
				})
			}
		}
		if err := s.validateYouTubeLiveAPIOutputRelay(r.Context(), primaryAssignments, &body); err != nil {
			issues = append(issues, servicecall.ReadinessIssue{ServiceType: "encoder_recorder", Code: youtubeOutputCode(err), Message: youtubeOutputReadinessMessage(err)})
		} else {
			issues = append(issues, s.youtubeOutputReadinessIssues(r.Context(), stream, &body)...)
		}
		issues = append(issues, s.archiveConfigReadinessIssues(r.Context(), &body)...)
		if checker, ok := s.dispatcher.(startReadinessChecker); ok {
			issues = append(issues, checker.StartReadinessIssues(primaryAssignments, body, time.Now().UTC())...)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"stream_id":              stream.ID,
		"ready":                  len(missing) == 0 && len(issues) == 0,
		"missing_service_types":  missing,
		"issues":                 issues,
		"assigned_service_count": len(assignments),
		"primary_service_count":  len(primaryAssignments),
		"assignments":            assignments,
	})
}
