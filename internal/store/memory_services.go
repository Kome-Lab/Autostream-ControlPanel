package store

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/example/autostream-control-panel/internal/security"
)

func (s *MemoryAuthStore) CreateServiceToken(ctx context.Context, serviceType string, scopes []string) (ServiceToken, error) {
	if err := ctx.Err(); err != nil {
		return ServiceToken{}, err
	}
	if err := validateServiceType(serviceType); err != nil {
		return ServiceToken{}, err
	}
	if err := validateServiceScopes(scopes); err != nil {
		return ServiceToken{}, err
	}
	raw, err := security.RandomToken(32)
	if err != nil {
		return ServiceToken{}, err
	}
	token := ServiceToken{ID: newUUID(), ServiceType: serviceType, Scopes: append([]string(nil), scopes...), RawToken: "ast_svc_" + raw, CreatedAt: time.Now().UTC()}
	token.TokenHash = security.HashToken(token.RawToken)
	s.mu.Lock()
	s.serviceTokens[token.ID] = token
	s.mu.Unlock()
	return token, nil
}

func (s *MemoryAuthStore) ListServiceTokens(ctx context.Context) ([]ServiceToken, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tokens := make([]ServiceToken, 0, len(s.serviceTokens))
	for _, token := range s.serviceTokens {
		token.RawToken = ""
		token.TokenHash = ""
		tokens = append(tokens, token)
	}
	sort.Slice(tokens, func(i, j int) bool { return tokens[i].CreatedAt.After(tokens[j].CreatedAt) })
	return tokens, nil
}

func (s *MemoryAuthStore) RevokeServiceToken(ctx context.Context, id string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	token, ok := s.serviceTokens[id]
	if !ok {
		return ErrNotFound
	}
	now := time.Now().UTC()
	token.RevokedAt = &now
	s.serviceTokens[id] = token
	return nil
}

func (s *MemoryAuthStore) RotateServiceToken(ctx context.Context, id string) (ServiceToken, error) {
	if err := ctx.Err(); err != nil {
		return ServiceToken{}, err
	}
	raw, err := security.RandomToken(32)
	if err != nil {
		return ServiceToken{}, err
	}
	now := time.Now().UTC()
	s.mu.Lock()
	defer s.mu.Unlock()
	oldToken, ok := s.serviceTokens[id]
	if !ok || oldToken.RevokedAt != nil {
		return ServiceToken{}, ErrNotFound
	}
	oldToken.RevokedAt = &now
	s.serviceTokens[id] = oldToken

	token := ServiceToken{
		ID:          newUUID(),
		ServiceType: oldToken.ServiceType,
		Scopes:      append([]string(nil), oldToken.Scopes...),
		RawToken:    "ast_svc_" + raw,
		CreatedAt:   now,
	}
	token.TokenHash = security.HashToken(token.RawToken)
	s.serviceTokens[token.ID] = token

	for serviceID, service := range s.services {
		if service.TokenID == id {
			service.TokenID = token.ID
			service.NodeTokenRotatedAt = &now
			service.UpdatedAt = now
			s.services[serviceID] = service
		}
	}
	return token, nil
}

func (s *MemoryAuthStore) AuthenticateServiceToken(ctx context.Context, rawToken, requiredScope string) (ServiceToken, error) {
	if err := ctx.Err(); err != nil {
		return ServiceToken{}, err
	}
	hash := security.HashToken(rawToken)
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, token := range s.serviceTokens {
		if token.TokenHash == hash {
			if token.RevokedAt != nil {
				return ServiceToken{}, ErrUnauthorized
			}
			if requiredScope != "" && !hasString(token.Scopes, requiredScope) {
				return ServiceToken{}, ErrForbidden
			}
			return token, nil
		}
	}
	return ServiceToken{}, ErrUnauthorized
}

func (s *MemoryAuthStore) PrecreateService(ctx context.Context, token ServiceToken, registration ServiceRegistration) (RegisteredService, error) {
	if err := ctx.Err(); err != nil {
		return RegisteredService{}, err
	}
	if registration.ServiceType != token.ServiceType {
		return RegisteredService{}, ErrForbidden
	}
	registration = normalizeServiceRegistration(registration)
	if err := validateServiceRegistration(registration); err != nil {
		return RegisteredService{}, err
	}
	now := time.Now().UTC()
	svc := RegisteredService{
		ServiceID: registration.ServiceID, ServiceType: registration.ServiceType, ServiceName: registration.ServiceName,
		Description: registration.Description, Host: registration.Host, Port: registration.Port, SSLEnabled: registration.SSLEnabled,
		PublicURL: registration.PublicURL, Version: registration.Version, ReportedVersion: "", Status: "pending",
		Capabilities: sanitizeServiceCapabilities(registration.Capabilities), ReportedCapabilities: sanitizeServiceCapabilities(registration.Capabilities), Metrics: map[string]any{}, TokenID: token.ID, NodeTokenRotatedAt: &token.CreatedAt, CreatedAt: now, UpdatedAt: now,
	}
	if svc.Capabilities == nil {
		svc.Capabilities = map[string]any{}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.services[svc.ServiceID]; ok {
		return RegisteredService{}, ErrAlreadyExists
	}
	s.services[svc.ServiceID] = svc
	return svc, nil
}

func (s *MemoryAuthStore) RegisterService(ctx context.Context, token ServiceToken, registration ServiceRegistration) (RegisteredService, error) {
	if err := ctx.Err(); err != nil {
		return RegisteredService{}, err
	}
	if registration.ServiceType != token.ServiceType {
		return RegisteredService{}, ErrForbidden
	}
	registration = normalizeServiceRegistration(registration)
	if err := validateServiceRegistration(registration); err != nil {
		return RegisteredService{}, err
	}
	now := time.Now().UTC()
	capabilities := sanitizeServiceCapabilities(registration.Capabilities)
	svc := RegisteredService{
		ServiceID: registration.ServiceID, ServiceType: registration.ServiceType, ServiceName: registration.ServiceName,
		Description: registration.Description, Host: registration.Host, Port: registration.Port, SSLEnabled: registration.SSLEnabled,
		PublicURL: registration.PublicURL, Version: registration.Version, ReportedVersion: registration.Version, Status: "registered",
		Capabilities: capabilities, ReportedCapabilities: capabilities, TokenID: token.ID,
		ReportedHostname: registration.Hostname, ReportedOS: registration.OS, ReportedArch: registration.Arch, LastReportedAt: &now,
		CreatedAt: now, UpdatedAt: now,
	}
	if svc.Capabilities == nil {
		svc.Capabilities = map[string]any{}
	}
	s.mu.Lock()
	existing, ok := s.services[svc.ServiceID]
	if !ok {
		s.mu.Unlock()
		return RegisteredService{}, ErrForbidden
	}
	if existing.ServiceType != svc.ServiceType || existing.TokenID != token.ID {
		s.mu.Unlock()
		return RegisteredService{}, ErrForbidden
	}
	svc.CreatedAt = existing.CreatedAt
	svc.LastHeartbeatAt = existing.LastHeartbeatAt
	svc.CurrentStreamID = existing.CurrentStreamID
	svc.Metrics = existing.Metrics
	if existing.Status == "assigned" || existing.Status == "restart_requested" {
		svc.Status = existing.Status
	}
	s.services[svc.ServiceID] = svc
	s.mu.Unlock()
	return svc, nil
}

func (s *MemoryAuthStore) Heartbeat(ctx context.Context, token ServiceToken, heartbeat ServiceHeartbeat) (RegisteredService, error) {
	if err := ctx.Err(); err != nil {
		return RegisteredService{}, err
	}
	if heartbeat.ServiceID == "" {
		heartbeat.ServiceID = strings.TrimSpace(heartbeat.NodeID)
	}
	if heartbeat.ServiceID == "" {
		heartbeat.ServiceID = strings.TrimSpace(heartbeat.NodeIDSnake)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	svc, ok := s.services[heartbeat.ServiceID]
	if !ok {
		return RegisteredService{}, ErrNotFound
	}
	if svc.TokenID != token.ID {
		return RegisteredService{}, ErrForbidden
	}
	if heartbeat.CurrentStreamID != "" && !s.isAssignedLocked(heartbeat.ServiceID, heartbeat.CurrentStreamID) {
		return RegisteredService{}, ErrForbidden
	}
	now := time.Now().UTC()
	svc.Status = heartbeat.Status
	svc.LastHeartbeatAt = &now
	if heartbeat.CurrentStreamID != "" {
		svc.CurrentStreamID = heartbeat.CurrentStreamID
	}
	svc.Metrics = sanitizeServiceMetrics(heartbeat.Metrics)
	if strings.TrimSpace(heartbeat.Version) != "" {
		svc.Version = strings.TrimSpace(heartbeat.Version)
		svc.ReportedVersion = svc.Version
	}
	if len(heartbeat.Capabilities) > 0 {
		svc.Capabilities = sanitizeServiceCapabilities(heartbeat.Capabilities)
		svc.ReportedCapabilities = svc.Capabilities
	}
	if strings.TrimSpace(heartbeat.Hostname) != "" {
		svc.ReportedHostname = strings.TrimSpace(heartbeat.Hostname)
	}
	if strings.TrimSpace(heartbeat.OS) != "" {
		svc.ReportedOS = strings.TrimSpace(heartbeat.OS)
	}
	if strings.TrimSpace(heartbeat.Arch) != "" {
		svc.ReportedArch = strings.TrimSpace(heartbeat.Arch)
	}
	if heartbeat.API != nil {
		apiHost := strings.TrimSpace(heartbeat.API.Host)
		if apiHost != "" {
			svc.Host = apiHost
			svc.Port = heartbeat.API.Port
			svc.SSLEnabled = heartbeat.API.SSLEnabled
			svc.PublicURL = buildServiceURL(svc.Host, svc.Port, svc.SSLEnabled)
		}
	}
	if heartbeat.Version != "" || len(heartbeat.Capabilities) > 0 || heartbeat.Hostname != "" || heartbeat.OS != "" || heartbeat.Arch != "" || heartbeat.API != nil {
		svc.LastReportedAt = &now
	}
	svc.UpdatedAt = now
	s.services[svc.ServiceID] = svc
	return svc, nil
}

func (s *MemoryAuthStore) SetServiceConfigureToken(ctx context.Context, serviceID, tokenHash string, expiresAt time.Time) (RegisteredService, error) {
	if err := ctx.Err(); err != nil {
		return RegisteredService{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	svc, ok := s.services[serviceID]
	if !ok {
		return RegisteredService{}, ErrNotFound
	}
	svc.ConfigureTokenExpiresAt = &expiresAt
	svc.ConfigureTokenUsedAt = nil
	svc.ConfigureTokenHash = tokenHash
	svc.UpdatedAt = time.Now().UTC()
	s.services[serviceID] = svc
	return svc, nil
}

func (s *MemoryAuthStore) ConsumeServiceConfigureToken(ctx context.Context, serviceID, rawToken string, now time.Time) (RegisteredService, error) {
	if err := ctx.Err(); err != nil {
		return RegisteredService{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	svc, ok := s.services[serviceID]
	if !ok {
		return RegisteredService{}, ErrNotFound
	}
	if svc.ConfigureTokenHash == "" || svc.ConfigureTokenExpiresAt == nil || svc.ConfigureTokenUsedAt != nil || !now.Before(*svc.ConfigureTokenExpiresAt) || !security.VerifyTokenHash(rawToken, svc.ConfigureTokenHash) {
		return RegisteredService{}, ErrUnauthorized
	}
	svc.ConfigureTokenUsedAt = &now
	svc.UpdatedAt = now
	s.services[serviceID] = svc
	return svc, nil
}

func (s *MemoryAuthStore) SetServiceNodeTokenSecret(ctx context.Context, serviceID, ciphertext, nonce string) (RegisteredService, error) {
	if err := ctx.Err(); err != nil {
		return RegisteredService{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	svc, ok := s.services[serviceID]
	if !ok {
		return RegisteredService{}, ErrNotFound
	}
	svc.NodeTokenCiphertext = ciphertext
	svc.NodeTokenNonce = nonce
	svc.UpdatedAt = time.Now().UTC()
	s.services[serviceID] = svc
	return svc, nil
}

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
	delete(s.services, serviceID)
	for key, assignedServiceID := range s.assignments {
		if assignedServiceID == serviceID {
			delete(s.assignments, key)
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

func (s *MemoryAuthStore) AssignServiceToStream(ctx context.Context, serviceID, streamID, actorUserID string) (RegisteredService, error) {
	return s.AssignServiceToStreamWithRole(ctx, serviceID, streamID, actorUserID, "primary")
}

func (s *MemoryAuthStore) AssignServiceToStreamWithRole(ctx context.Context, serviceID, streamID, actorUserID, assignmentRole string) (RegisteredService, error) {
	if err := ctx.Err(); err != nil {
		return RegisteredService{}, err
	}
	assignmentRole = normalizeAssignmentRole(assignmentRole)
	s.mu.Lock()
	defer s.mu.Unlock()
	svc, ok := s.services[serviceID]
	if !ok {
		return RegisteredService{}, ErrNotFound
	}
	targetKey := assignmentKey(streamID, svc.ServiceType, assignmentRole, serviceID)
	replacedServiceIDs := make(map[string]bool)
	for key, assignedServiceID := range s.assignments {
		if assignedServiceID == serviceID || (assignmentRole == "primary" && assignmentKeyMatchesPrimary(key, streamID, svc.ServiceType)) || key == targetKey {
			delete(s.assignments, key)
			if assignedServiceID != serviceID {
				replacedServiceIDs[assignedServiceID] = true
			}
		}
	}
	for replacedServiceID := range replacedServiceIDs {
		replaced := s.services[replacedServiceID]
		replaced.CurrentStreamID = ""
		if replaced.Status == "assigned" {
			replaced.Status = "registered"
		}
		replaced.UpdatedAt = time.Now().UTC()
		s.services[replacedServiceID] = replaced
	}
	s.assignments[targetKey] = serviceID
	svc.CurrentStreamID = streamID
	svc.Status = "assigned"
	svc.AssignmentRole = assignmentRole
	svc.UpdatedAt = time.Now().UTC()
	s.services[serviceID] = svc
	return svc, nil
}

func (s *MemoryAuthStore) UnassignServiceFromStream(ctx context.Context, serviceID, actorUserID string) (RegisteredService, error) {
	if err := ctx.Err(); err != nil {
		return RegisteredService{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	svc, ok := s.services[serviceID]
	if !ok {
		return RegisteredService{}, ErrNotFound
	}
	for key, assignedServiceID := range s.assignments {
		if assignedServiceID == serviceID {
			delete(s.assignments, key)
		}
	}
	svc.CurrentStreamID = ""
	if svc.Status == "assigned" {
		svc.Status = "registered"
	}
	svc.UpdatedAt = time.Now().UTC()
	s.services[serviceID] = svc
	return svc, nil
}

func (s *MemoryAuthStore) ListStreamAssignments(ctx context.Context, streamID string) ([]RegisteredService, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	services := make([]RegisteredService, 0)
	for key, serviceID := range s.assignments {
		if !strings.HasPrefix(key, streamID+"\x00") {
			continue
		}
		if svc, ok := s.services[serviceID]; ok {
			svc.AssignmentRole = assignmentRoleFromKey(key)
			services = append(services, svc)
		}
	}
	sort.Slice(services, func(i, j int) bool {
		if services[i].ServiceType == services[j].ServiceType {
			if services[i].AssignmentRole != services[j].AssignmentRole {
				return services[i].AssignmentRole == "primary"
			}
			return services[i].ServiceName < services[j].ServiceName
		}
		return services[i].ServiceType < services[j].ServiceType
	})
	return services, nil
}

func (s *MemoryAuthStore) ListServiceAssignmentsForService(ctx context.Context, serviceID string) ([]StreamServiceAssignment, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	assignments := make([]StreamServiceAssignment, 0)
	for key, assignedServiceID := range s.assignments {
		if assignedServiceID != serviceID {
			continue
		}
		streamID, serviceType, role := assignmentPartsFromKey(key)
		assignments = append(assignments, StreamServiceAssignment{
			StreamID:       streamID,
			ServiceID:      serviceID,
			ServiceType:    serviceType,
			AssignmentRole: role,
			AssignedAt:     s.services[serviceID].UpdatedAt,
		})
	}
	sort.Slice(assignments, func(i, j int) bool {
		if assignments[i].StreamID == assignments[j].StreamID {
			return assignments[i].AssignmentRole < assignments[j].AssignmentRole
		}
		return assignments[i].StreamID < assignments[j].StreamID
	})
	return assignments, nil
}

func (s *MemoryAuthStore) RequestServiceRestart(ctx context.Context, serviceID string) (RegisteredService, error) {
	if err := ctx.Err(); err != nil {
		return RegisteredService{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	svc, ok := s.services[serviceID]
	if !ok {
		return RegisteredService{}, ErrNotFound
	}
	svc.Status = "restart_requested"
	svc.UpdatedAt = time.Now().UTC()
	s.services[serviceID] = svc
	return svc, nil
}

func (s *MemoryAuthStore) WriteStreamEvent(ctx context.Context, token ServiceToken, event ServiceStreamEvent) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	svc, ok := s.services[event.ServiceID]
	if !ok {
		return ErrNotFound
	}
	if svc.TokenID != token.ID {
		return ErrForbidden
	}
	if !serviceStreamEventAllowed(svc.ServiceType, event.EventType) {
		return ErrInvalidServiceStreamEvent
	}
	if !s.isAssignedLocked(event.ServiceID, event.StreamID) {
		return ErrForbidden
	}
	if event.Payload == nil {
		event.Payload = map[string]any{}
	}
	event.Payload = sanitizeServiceEventPayload(event.Payload)
	s.streamEvents = append(s.streamEvents, event)
	return nil
}

func (s *MemoryAuthStore) isAssignedLocked(serviceID, streamID string) bool {
	for key, assignedServiceID := range s.assignments {
		if assignedServiceID == serviceID && strings.HasPrefix(key, streamID+"\x00") {
			return true
		}
	}
	return false
}

func assignmentKey(streamID, serviceType, assignmentRole, serviceID string) string {
	if assignmentRole == "standby" {
		return streamID + "\x00standby\x00" + serviceType + "\x00" + serviceID
	}
	return streamID + "\x00primary\x00" + serviceType
}

func assignmentKeyMatchesPrimary(key, streamID, serviceType string) bool {
	return key == assignmentKey(streamID, serviceType, "primary", "")
}

func assignmentRoleFromKey(key string) string {
	parts := strings.Split(key, "\x00")
	if len(parts) >= 2 && parts[1] == "standby" {
		return "standby"
	}
	return "primary"
}

func assignmentPartsFromKey(key string) (streamID, serviceType, assignmentRole string) {
	parts := strings.Split(key, "\x00")
	if len(parts) >= 3 {
		streamID = parts[0]
		assignmentRole = parts[1]
		serviceType = parts[2]
		if assignmentRole != "standby" {
			assignmentRole = "primary"
		}
		return streamID, serviceType, assignmentRole
	}
	return "", "", "primary"
}
