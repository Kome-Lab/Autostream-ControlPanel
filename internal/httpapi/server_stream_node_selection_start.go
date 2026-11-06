package httpapi

import (
	"context"
	"strings"

	"github.com/example/autostream-control-panel/internal/servicecall"
	"github.com/example/autostream-control-panel/internal/store"
)

// Configured selections are preflight candidates, never runtime assignments.
// ClaimStreamStart revalidates them and obtains all three owners atomically.
func (s *Server) selectedStartCandidates(ctx context.Context, stream store.Stream, assignments []store.RegisteredService) ([]store.RegisteredService, *store.StreamNodeSelections, error) {
	selected := store.StreamNodeSelections{
		EncoderServiceID: strings.TrimSpace(stream.SelectedEncoderID),
		WorkerServiceID: strings.TrimSpace(stream.SelectedWorkerID),
		DiscordServiceID: strings.TrimSpace(stream.SelectedDiscordID),
	}
	if !stream.NodeSelectionsConfigured && selected.EncoderServiceID == "" && selected.WorkerServiceID == "" && selected.DiscordServiceID == "" {
		return assignments, nil, nil
	}
	if s.services == nil {
		return nil, nil, store.ErrServiceAssignmentGuardUnavailable
	}
	candidates := make([]store.RegisteredService, 0, 3)
	for _, item := range []struct{ serviceType, serviceID string }{
		{"encoder_recorder", selected.EncoderServiceID},
		{"worker", selected.WorkerServiceID},
		{"discord_bot", selected.DiscordServiceID},
	} {
		if item.serviceID == "" {
			continue
		}
		service, err := s.services.GetService(ctx, item.serviceID)
		if err != nil {
			return nil, nil, err
		}
		if service.ServiceType != item.serviceType {
			return nil, nil, store.ErrInvalidServiceAssignment
		}
		service.AssignmentRole = "primary"
		candidates = append(candidates, service)
	}
	return candidates, &selected, nil
}

func (s *Server) selectedNodeReadinessIssues(ctx context.Context, streamID string, candidates []store.RegisteredService) ([]servicecall.ReadinessIssue, error) {
	issues := []servicecall.ReadinessIssue{}
	for _, service := range candidates {
		owner := strings.TrimSpace(service.CurrentStreamID)
		if owner == "" || owner == streamID {
			continue
		}
		current, err := s.streams.GetStream(ctx, owner)
		if err != nil {
			return nil, err
		}
		pendingArchive := current.ArchiveStartedAt != nil && current.ArchiveReportedAt == nil &&
			(current.Status == "completed" || current.Status == "ready")
		if isActiveStreamStatus(current.Status) || pendingArchive {
			issues = append(issues, servicecall.ReadinessIssue{
				ServiceID: service.ServiceID, ServiceType: service.ServiceType, Code: "service_node_busy",
				Message: "The selected Node is still in use by another stream.",
			})
		}
	}
	return issues, nil
}
