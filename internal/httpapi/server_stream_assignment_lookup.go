package httpapi

import (
	"context"
	"errors"
	"github.com/example/autostream-control-panel/internal/store"
	"strings"
)

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
