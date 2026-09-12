package httpapi

import (
	"context"
	"errors"
	"log"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strings"

	"github.com/example/autostream-control-panel/internal/security"
	"github.com/example/autostream-control-panel/internal/store"
)

type streamServiceAssignmentOperation struct {
	serviceID   *string
	serviceType string
	permission  string
	notFound    string
	wrongType   string
}

func streamServiceAssignmentOperations(body streamSettingsRequest) []streamServiceAssignmentOperation {
	return []streamServiceAssignmentOperation{
		{serviceID: body.EncoderServiceID, serviceType: "encoder_recorder", permission: "services.assign", notFound: "encoder_service_not_found", wrongType: "encoder_service_type_invalid"},
		{serviceID: body.WorkerServiceID, serviceType: "worker", permission: "workers.assign", notFound: "worker_service_not_found", wrongType: "worker_service_type_invalid"},
	}
}

func assignmentRequestServiceID(value *string) string {
	if value == nil {
		return ""
	}
	return strings.TrimSpace(*value)
}

func (s *Server) validateStreamServiceAssignmentRequest(ctx context.Context, body streamSettingsRequest, permissions []string) (string, int) {
	for _, item := range streamServiceAssignmentOperations(body) {
		if item.serviceID == nil {
			continue
		}
		if !security.HasPermission(permissions, item.permission) {
			return "permission_denied", http.StatusForbidden
		}
		if serviceID := assignmentRequestServiceID(item.serviceID); serviceID != "" {
			_, code, status := s.assignableStreamService(ctx, serviceID, item.serviceType, item.notFound, item.wrongType)
			if code != "" {
				return code, status
			}
		}
	}
	return "", 0
}

func (s *Server) assignableStreamService(ctx context.Context, serviceID, serviceType, notFoundCode, wrongTypeCode string) (store.RegisteredService, string, int) {
	if s.services == nil {
		return store.RegisteredService{}, "service_registry_not_configured", http.StatusServiceUnavailable
	}
	service, err := s.services.GetService(ctx, serviceID)
	if errors.Is(err, store.ErrNotFound) {
		return store.RegisteredService{}, notFoundCode, http.StatusBadRequest
	}
	if err != nil {
		return store.RegisteredService{}, "get_service_failed", http.StatusInternalServerError
	}
	if strings.TrimSpace(service.ServiceType) != serviceType {
		return store.RegisteredService{}, wrongTypeCode, http.StatusBadRequest
	}
	return service, "", 0
}

type streamServiceAssignmentSnapshot struct {
	ServiceID      string
	ServiceType    string
	CurrentStream  string
	AssignmentRole string
}

type appliedStreamServiceAssignment struct {
	Service     store.RegisteredService
	ServiceType string
}

type appliedStreamServiceAssignments struct {
	StreamID  string
	Snapshots []streamServiceAssignmentSnapshot
	Applied   []appliedStreamServiceAssignment
	Cleared   []streamServiceAssignmentSnapshot
	Mutated   bool
}

func (s *Server) applyStreamServiceAssignments(r *http.Request, streamID string, body streamSettingsRequest, current currentUser) (appliedStreamServiceAssignments, string, int) {
	result := appliedStreamServiceAssignments{StreamID: streamID}
	operations := streamServiceAssignmentOperations(body)
	requestedIDs := make([]string, 0, len(operations))
	affectedServiceTypes := make(map[string]struct{}, len(operations))
	for _, operation := range operations {
		if operation.serviceID == nil {
			continue
		}
		affectedServiceTypes[operation.serviceType] = struct{}{}
		if serviceID := assignmentRequestServiceID(operation.serviceID); serviceID != "" {
			requestedIDs = append(requestedIDs, serviceID)
		}
	}
	if len(affectedServiceTypes) == 0 {
		return result, "", 0
	}
	if s.services == nil {
		return result, "service_registry_not_configured", http.StatusServiceUnavailable
	}
	snapshots, err := s.snapshotStreamServiceAssignments(r.Context(), streamID, requestedIDs, affectedServiceTypes)
	if err != nil {
		return result, "list_stream_assignments_failed", http.StatusInternalServerError
	}
	result.Snapshots = snapshots
	for _, operation := range operations {
		if operation.serviceID == nil {
			continue
		}
		serviceID := assignmentRequestServiceID(operation.serviceID)
		if serviceID == "" {
			for _, snapshot := range result.Snapshots {
				if snapshot.ServiceType != operation.serviceType || snapshot.CurrentStream != streamID {
					continue
				}
				expected := streamID
				if _, err := s.services.UnassignServiceFromStreamGuarded(r.Context(), store.ServiceUnassignmentMutation{ServiceID: snapshot.ServiceID, ActorUserID: current.User.ID, ExpectedCurrentStreamID: &expected}); err != nil {
					if rollbackErr := result.Rollback(r.Context(), s.services, current.User.ID); rollbackErr != nil {
						log.Printf("stream service assignment rollback failed after unassign: stream_id=%s error=%v", streamID, rollbackErr)
						return result, "assign_service_rollback_failed", http.StatusInternalServerError
					}
					if code, status, handled := serviceAssignmentHTTPError(err, true); handled {
						return result, code, status
					}
					return result, "unassign_service_failed", http.StatusInternalServerError
				}
				result.Cleared = append(result.Cleared, snapshot)
				result.Mutated = true
			}
			continue
		}
		expectedCurrentStreamID := ""
		for _, snapshot := range result.Snapshots {
			if snapshot.ServiceID == serviceID {
				expectedCurrentStreamID = snapshot.CurrentStream
				break
			}
		}
		service, err := s.services.AssignServiceToStreamGuarded(r.Context(), store.ServiceAssignmentMutation{ServiceID: serviceID, StreamID: streamID, ActorUserID: current.User.ID, AssignmentRole: "primary", ExpectedCurrentStreamID: &expectedCurrentStreamID})
		if err != nil {
			if rollbackErr := result.Rollback(r.Context(), s.services, current.User.ID); rollbackErr != nil {
				log.Printf("stream service assignment rollback failed: stream_id=%s error=%v", streamID, rollbackErr)
				return result, "assign_service_rollback_failed", http.StatusInternalServerError
			}
			if errors.Is(err, store.ErrNotFound) {
				return result, "service_not_found", http.StatusBadRequest
			}
			if code, status, handled := serviceAssignmentHTTPError(err, false); handled {
				return result, code, status
			}
			return result, "assign_service_failed", http.StatusInternalServerError
		}
		alreadyAssigned := false
		for _, snapshot := range result.Snapshots {
			if snapshot.ServiceID == serviceID && snapshot.CurrentStream == streamID && snapshot.AssignmentRole == "primary" {
				alreadyAssigned = true
				break
			}
		}
		if alreadyAssigned {
			continue
		}
		result.Applied = append(result.Applied, appliedStreamServiceAssignment{Service: service, ServiceType: operation.serviceType})
		result.Mutated = true
	}
	return result, "", 0
}

func (s *Server) snapshotStreamServiceAssignments(ctx context.Context, streamID string, requestedServiceIDs []string, affectedServiceTypes map[string]struct{}) ([]streamServiceAssignmentSnapshot, error) {
	assignments, err := s.services.ListStreamAssignments(ctx, streamID)
	if err != nil {
		return nil, err
	}
	snapshots := make(map[string]streamServiceAssignmentSnapshot, len(assignments)+len(requestedServiceIDs))
	add := func(service store.RegisteredService) {
		serviceID := strings.TrimSpace(service.ServiceID)
		if serviceID == "" {
			return
		}
		snapshots[serviceID] = streamServiceAssignmentSnapshot{
			ServiceID:      serviceID,
			ServiceType:    strings.TrimSpace(service.ServiceType),
			CurrentStream:  strings.TrimSpace(service.CurrentStreamID),
			AssignmentRole: normalizeAssignmentRole(service.AssignmentRole),
		}
	}
	for _, assignment := range assignments {
		if _, affected := affectedServiceTypes[assignment.ServiceType]; !affected {
			continue
		}
		add(assignment)
	}
	for _, serviceID := range requestedServiceIDs {
		service, err := s.services.GetService(ctx, serviceID)
		if err != nil {
			return nil, err
		}
		add(service)
	}
	result := make([]streamServiceAssignmentSnapshot, 0, len(snapshots))
	for _, snapshot := range snapshots {
		result = append(result, snapshot)
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].ServiceID < result[j].ServiceID
	})
	return result, nil
}

func (assignments appliedStreamServiceAssignments) Rollback(ctx context.Context, services store.ServiceRegistryStore, actorUserID string) error {
	if !assignments.Mutated || services == nil {
		return nil
	}
	appliedIDs := make([]string, 0, len(assignments.Applied))
	for _, applied := range assignments.Applied {
		appliedIDs = append(appliedIDs, applied.Service.ServiceID)
	}
	sort.Strings(appliedIDs)
	for _, serviceID := range appliedIDs {
		expected := assignments.StreamID
		if _, err := services.UnassignServiceFromStreamGuarded(ctx, store.ServiceUnassignmentMutation{ServiceID: serviceID, ActorUserID: actorUserID, ExpectedCurrentStreamID: &expected}); err != nil && !errors.Is(err, store.ErrNotFound) {
			return err
		}
	}
	snapshots := append([]streamServiceAssignmentSnapshot(nil), assignments.Snapshots...)
	sort.SliceStable(snapshots, func(i, j int) bool {
		iTarget := snapshots[i].CurrentStream == assignments.StreamID
		jTarget := snapshots[j].CurrentStream == assignments.StreamID
		if iTarget != jTarget {
			return !iTarget
		}
		return snapshots[i].ServiceID < snapshots[j].ServiceID
	})
	for _, snapshot := range snapshots {
		if snapshot.CurrentStream == "" {
			continue
		}
		expected := ""
		if _, err := services.AssignServiceToStreamGuarded(ctx, store.ServiceAssignmentMutation{ServiceID: snapshot.ServiceID, StreamID: snapshot.CurrentStream, ActorUserID: actorUserID, AssignmentRole: snapshot.AssignmentRole, ExpectedCurrentStreamID: &expected}); err != nil {
			return err
		}
	}
	return nil
}

func (assignments appliedStreamServiceAssignments) WriteAudit(r *http.Request, s *Server, current currentUser) {
	for _, applied := range assignments.Applied {
		s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "services.assign", ResourceType: "service", ResourceID: applied.Service.ServiceID, Result: "success", Metadata: map[string]any{"stream_id": assignments.StreamID, "service_type": applied.ServiceType, "assignment_role": "primary", "source": "stream_settings"}})
	}
	for _, cleared := range assignments.Cleared {
		s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "services.unassign", ResourceType: "service", ResourceID: cleared.ServiceID, Result: "success", Metadata: map[string]any{"stream_id": assignments.StreamID, "service_type": cleared.ServiceType, "assignment_role": cleared.AssignmentRole, "source": "stream_settings"}})
	}
}

func streamSettingsReferenceCode(err error) string {
	switch {
	case errors.Is(err, errAutoStartTriggerInvalid):
		return "auto_start_trigger_invalid"
	case errors.Is(err, errAutoStartDiscordRequired):
		return "auto_start_discord_required"
	case errors.Is(err, errDiscordConfigRequired):
		return "discord_config_required"
	case errors.Is(err, errDiscordConfigNotFound):
		return "discord_config_not_found"
	case errors.Is(err, errYouTubeOutputNotFound):
		return "youtube_output_not_found"
	case errors.Is(err, errEncoderProfileNotFound):
		return "encoder_profile_not_found"
	case errors.Is(err, errCaptionProfileNotFound):
		return "caption_profile_not_found"
	case errors.Is(err, errOverlayProfileNotFound):
		return "overlay_profile_not_found"
	case errors.Is(err, errArchiveProfileNotFound):
		return "archive_profile_not_found"
	case errors.Is(err, errDriveOAuthAccountUnavailable):
		return "drive_oauth_account_unavailable"
	case errors.Is(err, errEncoderInputURLBlocked):
		return "encoder_input_url_blocked"
	default:
		return "invalid_stream_settings_reference"
	}
}

func validateEncoderInputURL(raw string) error {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" || parsed.User != nil || parsed.Fragment != "" {
		return errEncoderInputURLBlocked
	}
	switch strings.ToLower(parsed.Scheme) {
	case "srt", "rtmp", "rtmps", "http", "https":
	default:
		return errEncoderInputURLBlocked
	}
	host := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(parsed.Hostname()), "."))
	if host == "" || host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return errEncoderInputURLBlocked
	}
	if ip := net.ParseIP(host); ip != nil && unsafeEncoderInputIP(ip) {
		return errEncoderInputURLBlocked
	}
	return nil
}

func unsafeEncoderInputIP(ip net.IP) bool {
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() || ip.IsUnspecified() || ip.IsMulticast()
}
