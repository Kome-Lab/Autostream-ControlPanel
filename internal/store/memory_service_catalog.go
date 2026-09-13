package store

import (
	"context"
	"sort"
	"strings"
	"time"
)

func (s *MemoryAuthStore) ListServices(ctx context.Context) ([]RegisteredService, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	services := make([]RegisteredService, 0, len(s.services))
	for _, svc := range s.services {
		services = append(services, svc)
	}
	sort.Slice(services, func(i, j int) bool {
		if services[i].ServiceType == services[j].ServiceType {
			return services[i].ServiceName < services[j].ServiceName
		}
		return services[i].ServiceType < services[j].ServiceType
	})
	return services, nil
}

func (s *MemoryAuthStore) ListWorkers(ctx context.Context) ([]RegisteredService, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	workers := make([]RegisteredService, 0)
	for _, svc := range s.services {
		if svc.ServiceType == "worker" {
			workers = append(workers, svc)
		}
	}
	sort.Slice(workers, func(i, j int) bool { return workers[i].ServiceName < workers[j].ServiceName })
	return workers, nil
}

func (s *MemoryAuthStore) GetService(ctx context.Context, id string) (RegisteredService, error) {
	if err := ctx.Err(); err != nil {
		return RegisteredService{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	svc, ok := s.services[id]
	if !ok {
		return RegisteredService{}, ErrNotFound
	}
	return svc, nil
}

func (s *MemoryAuthStore) UpdateServiceMetadata(ctx context.Context, serviceID string, update ServiceMetadataUpdate) (RegisteredService, error) {
	if err := ctx.Err(); err != nil {
		return RegisteredService{}, err
	}
	update = normalizeServiceMetadataUpdate(update)
	if strings.TrimSpace(serviceID) == "" {
		return RegisteredService{}, ErrNotFound
	}
	if err := validateServiceMetadataUpdate(update); err != nil {
		return RegisteredService{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	svc, ok := s.services[serviceID]
	if !ok {
		return RegisteredService{}, ErrNotFound
	}
	if update.Endpointless && (svc.ServiceType != "update_agent" || svc.TransportMode != "pull_v2") {
		return RegisteredService{}, ErrInvalidServiceRegistration
	}
	svc.ServiceName = update.ServiceName
	svc.Description = update.Description
	if !update.Endpointless && !update.PreserveEndpoint {
		svc.Host = update.Host
		svc.Port = update.Port
		svc.SSLEnabled = update.SSLEnabled
		svc.PublicURL = update.PublicURL
		svc.AppliedEndpoint = serviceEndpoint(update.Host, update.Port, update.SSLEnabled, update.PublicURL)
		svc.DesiredEndpoint = copyServiceEndpoint(svc.AppliedEndpoint)
		svc.EndpointRevision++
		svc.EndpointStatus = "applied"
	}
	svc.UpdatedAt = time.Now().UTC()
	s.services[serviceID] = svc
	return svc, nil
}

func (s *MemoryAuthStore) DeleteService(ctx context.Context, serviceID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	svc, ok := s.services[serviceID]
	if !ok {
		return ErrNotFound
	}
	if streams := s.streamAssignmentGuard; streams != nil {
		streams.mu.Lock()
		defer streams.mu.Unlock()
		owner, _, err := s.consistentServiceAssignmentLocked(svc)
		if err != nil {
			return err
		}
		if owner != "" {
			stream, ok := streams.streams[owner]
			if !ok || stream.DeletedAt != nil {
				return ErrServiceAssignmentConflict
			}
			if memoryStreamAssignmentProtectionLocked(streams, stream).protected() {
				return ErrServiceUnassignProtectedStream
			}
		}
	}
	delete(s.services, serviceID)
	for key, assignedServiceID := range s.assignments {
		if assignedServiceID == serviceID {
			delete(s.assignments, key)
			delete(s.assignmentIDs, key)
		}
	}
	filteredEvents := s.streamEvents[:0]
	for _, event := range s.streamEvents {
		if event.ServiceID != serviceID {
			filteredEvents = append(filteredEvents, event)
		}
	}
	s.streamEvents = filteredEvents
	if token, ok := s.serviceTokens[svc.TokenID]; ok && token.RevokedAt == nil {
		now := time.Now().UTC()
		token.RevokedAt = &now
		s.serviceTokens[svc.TokenID] = token
	}
	return nil
}
