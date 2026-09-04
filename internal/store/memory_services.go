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
	if !ok || token.RevokedAt != nil {
		return ErrNotFound
	}
	now := time.Now().UTC()
	token.RevokedAt = &now
	s.serviceTokens[id] = token
	for serviceID, service := range s.services {
		if service.TokenID != id {
			continue
		}
		service.LastHeartbeatAt = nil
		service.ReportedCapabilities = map[string]any{}
		service.UpdatedAt = now
		s.services[serviceID] = service
	}
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
		Scopes:      serviceTokenScopesForRotation(oldToken),
		RawToken:    "ast_svc_" + raw,
		CreatedAt:   now,
	}
	token.TokenHash = security.HashToken(token.RawToken)
	s.serviceTokens[token.ID] = token

	for serviceID, service := range s.services {
		if service.TokenID == id {
			service.TokenID = token.ID
			service.LastHeartbeatAt = nil
			service.ReportedCapabilities = map[string]any{}
			clearStagedNodeConfiguration(&service)
			service.NodeTokenRotatedAt = &now
			service.UpdatedAt = now
			s.services[serviceID] = service
		}
	}
	return token, nil
}

func (s *MemoryAuthStore) RotateServiceNodeToken(ctx context.Context, serviceID, expectedTokenID string, seal NodeTokenSealer) (ServiceToken, RegisteredService, error) {
	if err := ctx.Err(); err != nil {
		return ServiceToken{}, RegisteredService{}, err
	}
	if seal == nil {
		return ServiceToken{}, RegisteredService{}, errNodeTokenSealerRequired
	}
	serviceID = strings.TrimSpace(serviceID)
	expectedTokenID = strings.TrimSpace(expectedTokenID)
	if serviceID == "" || expectedTokenID == "" {
		return ServiceToken{}, RegisteredService{}, ErrNotFound
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	service, ok := s.services[serviceID]
	if !ok || service.TokenID != expectedTokenID {
		return ServiceToken{}, RegisteredService{}, ErrNotFound
	}
	if requiresStagedNodeTokenRotation(service) {
		return ServiceToken{}, RegisteredService{}, ErrConflict
	}
	oldToken, ok := s.serviceTokens[expectedTokenID]
	if !ok || oldToken.RevokedAt != nil {
		return ServiceToken{}, RegisteredService{}, ErrNotFound
	}
	if service.ServiceType != oldToken.ServiceType {
		return ServiceToken{}, RegisteredService{}, ErrForbidden
	}

	now := time.Now().UTC()
	token, _, err := newRotatedServiceToken(oldToken, now)
	if err != nil {
		return ServiceToken{}, RegisteredService{}, err
	}
	ciphertext, nonce, err := seal(token.RawToken)
	if err != nil {
		return ServiceToken{}, RegisteredService{}, err
	}
	if err := ctx.Err(); err != nil {
		return ServiceToken{}, RegisteredService{}, err
	}

	oldTokenStillReferenced := false
	for candidateID, candidate := range s.services {
		if candidateID != serviceID && candidate.TokenID == oldToken.ID {
			oldTokenStillReferenced = true
			break
		}
	}
	if !oldTokenStillReferenced {
		oldToken.RevokedAt = &now
	}
	s.serviceTokens[oldToken.ID] = oldToken
	s.serviceTokens[token.ID] = token
	service.TokenID = token.ID
	service.NodeTokenCiphertext = ciphertext
	service.NodeTokenNonce = nonce
	service.ConfigureTokenHash = ""
	service.ConfigureTokenExpiresAt = nil
	service.ConfigureTokenUsedAt = nil
	clearStagedNodeConfiguration(&service)
	service.LastHeartbeatAt = nil
	service.ReportedCapabilities = map[string]any{}
	service.NodeTokenRotatedAt = &now
	service.UpdatedAt = now
	s.services[serviceID] = service
	return token, service, nil
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
		Description: registration.Description, TransportMode: registration.TransportMode, ExecutionHostID: registration.ExecutionHostID, OwnershipEpoch: registration.OwnershipEpoch,
		Host: registration.Host, Port: registration.Port, SSLEnabled: registration.SSLEnabled,
		PublicURL: registration.PublicURL, Version: registration.Version, ReportedVersion: "", Status: "pending",
		Capabilities: sanitizeServiceCapabilities(registration.Capabilities), ReportedCapabilities: sanitizeServiceCapabilities(registration.Capabilities), Metrics: map[string]any{}, TokenID: token.ID, NodeTokenRotatedAt: &token.CreatedAt, CreatedAt: now, UpdatedAt: now,
	}
	hydrateServiceEndpointState(&svc)
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
	s.mu.Lock()
	defer s.mu.Unlock()
	existing, ok := s.services[registration.ServiceID]
	if !ok {
		return RegisteredService{}, ErrForbidden
	}
	if existing.ServiceType != registration.ServiceType || existing.TokenID != token.ID {
		return RegisteredService{}, ErrForbidden
	}
	registration = bindPrecreatedUpdateAgentRegistration(
		registration,
		existing.TransportMode,
		existing.ExecutionHostID,
		existing.OwnershipEpoch,
	)
	if err := validateServiceRegistration(registration); err != nil {
		return RegisteredService{}, err
	}
	now := time.Now().UTC()
	capabilities := sanitizeServiceCapabilities(registration.Capabilities)
	svc := RegisteredService{
		ServiceID: registration.ServiceID, ServiceType: registration.ServiceType, ServiceName: registration.ServiceName,
		Description: registration.Description, TransportMode: registration.TransportMode, ExecutionHostID: registration.ExecutionHostID, OwnershipEpoch: registration.OwnershipEpoch,
		Host: registration.Host, Port: registration.Port, SSLEnabled: registration.SSLEnabled,
		PublicURL: registration.PublicURL, Version: registration.Version, ReportedVersion: registration.Version, ReportedCommit: registration.Commit, ReportedBuildDate: registration.BuildDate, Status: "registered",
		Capabilities: capabilities, ReportedCapabilities: capabilities, TokenID: token.ID,
		ReportedHostname: registration.Hostname, ReportedOS: registration.OS, ReportedArch: registration.Arch, LastReportedAt: &now,
		CreatedAt: now, UpdatedAt: now,
	}
	hydrateServiceEndpointState(&svc)
	if svc.Capabilities == nil {
		svc.Capabilities = map[string]any{}
	}
	if existing.ServiceType == "update_agent" && existing.Status != "pending" {
		svc.Capabilities = existing.Capabilities
	}
	if existing.Status != "pending" {
		svc.Host = existing.Host
		svc.Port = existing.Port
		svc.SSLEnabled = existing.SSLEnabled
		svc.PublicURL = existing.PublicURL
		svc.DesiredEndpoint = copyServiceEndpoint(existing.DesiredEndpoint)
		svc.AppliedEndpoint = copyServiceEndpoint(existing.AppliedEndpoint)
		svc.EndpointRevision = existing.EndpointRevision
		svc.EndpointStatus = existing.EndpointStatus
		svc.ReportedEndpoint = serviceEndpoint(registration.Host, registration.Port, registration.SSLEnabled, registration.PublicURL)
	}
	if existing.AppliedConfigRevision > 0 {
		svc.AppliedConfigRevision = existing.AppliedConfigRevision
		svc.AppliedConfigSHA256 = existing.AppliedConfigSHA256
	}
	if existing.ServiceType == "update_agent" {
		svc.TransportMode = existing.TransportMode
		svc.ExecutionHostID = existing.ExecutionHostID
		svc.OwnershipEpoch = existing.OwnershipEpoch
	}
	svc.CreatedAt = existing.CreatedAt
	svc.LastHeartbeatAt = existing.LastHeartbeatAt
	svc.CurrentStreamID = existing.CurrentStreamID
	svc.Metrics = existing.Metrics
	svc.NodeTokenCiphertext = existing.NodeTokenCiphertext
	svc.NodeTokenNonce = existing.NodeTokenNonce
	svc.StagedNodePreviousTokenID = existing.StagedNodePreviousTokenID
	svc.StagedNodeTokenID = existing.StagedNodeTokenID
	svc.StagedNodeTokenHash = existing.StagedNodeTokenHash
	svc.StagedNodeTokenScopes = append([]string(nil), existing.StagedNodeTokenScopes...)
	svc.StagedNodeTokenCiphertext = existing.StagedNodeTokenCiphertext
	svc.StagedNodeTokenNonce = existing.StagedNodeTokenNonce
	svc.StagedNodeActivationTokenHash = existing.StagedNodeActivationTokenHash
	svc.StagedNodeTokenAt = existing.StagedNodeTokenAt
	svc.ConfigureTokenHash = existing.ConfigureTokenHash
	svc.ConfigureTokenExpiresAt = existing.ConfigureTokenExpiresAt
	svc.ConfigureTokenUsedAt = existing.ConfigureTokenUsedAt
	svc.NodeTokenRotatedAt = existing.NodeTokenRotatedAt
	if existing.Status == "assigned" || existing.Status == "restart_requested" {
		svc.Status = existing.Status
	}
	s.services[svc.ServiceID] = svc
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
	activeToken, ok := s.serviceTokens[token.ID]
	if !ok || activeToken.RevokedAt != nil {
		return RegisteredService{}, ErrForbidden
	}
	if heartbeat.CurrentStreamID != "" && !s.isAssignedLocked(heartbeat.ServiceID, heartbeat.CurrentStreamID) {
		return RegisteredService{}, ErrForbidden
	}
	now := s.heartbeatNow().UTC()
	svc.Status = heartbeat.Status
	svc.LastHeartbeatAt = &now
	heartbeatCommit := truncateServiceReportedValue(strings.TrimSpace(heartbeat.Commit), 80)
	heartbeatBuildDate := truncateServiceReportedValue(strings.TrimSpace(heartbeat.BuildDate), 80)
	if heartbeat.CurrentStreamID != "" {
		svc.CurrentStreamID = heartbeat.CurrentStreamID
	}
	svc.Metrics = sanitizeServiceMetrics(heartbeat.Metrics)
	if strings.TrimSpace(heartbeat.Version) != "" {
		svc.Version = strings.TrimSpace(heartbeat.Version)
		svc.ReportedVersion = svc.Version
	}
	if heartbeatCommit != "" {
		svc.ReportedCommit = heartbeatCommit
	}
	if heartbeatBuildDate != "" {
		svc.ReportedBuildDate = heartbeatBuildDate
	}
	if len(heartbeat.Capabilities) > 0 {
		reportedCapabilities := sanitizeServiceCapabilities(heartbeat.Capabilities)
		if svc.ServiceType != "update_agent" {
			svc.Capabilities = reportedCapabilities
		}
		svc.ReportedCapabilities = reportedCapabilities
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
		if apiHost != "" && heartbeat.API.Port >= 1 && heartbeat.API.Port <= 65535 {
			svc.ReportedEndpoint = serviceEndpoint(apiHost, heartbeat.API.Port, heartbeat.API.SSLEnabled, "")
		}
	}
	if heartbeat.Version != "" || heartbeatCommit != "" || heartbeatBuildDate != "" || len(heartbeat.Capabilities) > 0 || heartbeat.Hostname != "" || heartbeat.OS != "" || heartbeat.Arch != "" || heartbeat.API != nil {
		svc.LastReportedAt = &now
	}
	svc.UpdatedAt = now
	s.services[svc.ServiceID] = svc
	s.recordMetricHistoryLocked(svc, now)
	return svc, nil
}

func (s *MemoryAuthStore) recordMetricHistoryLocked(service RegisteredService, observedAt time.Time) {
	cutoff := observedAt.Add(-3 * time.Hour)
	next := s.metricHistory[:0]
	for _, snapshot := range s.metricHistory {
		if snapshot.ObservedAt.After(cutoff) || snapshot.ObservedAt.Equal(cutoff) {
			next = append(next, snapshot)
		}
	}
	for name, raw := range service.Metrics {
		value, ok := serviceMetricSnapshotNumber(raw)
		if !ok {
			continue
		}
		next = append(next, ServiceMetricSnapshot{
			Name:        name,
			ServiceID:   service.ServiceID,
			ServiceType: service.ServiceType,
			Status:      service.Status,
			Value:       value,
			ObservedAt:  observedAt,
		})
	}
	s.metricHistory = next
}

func (s *MemoryAuthStore) ListServiceMetricSnapshots(ctx context.Context, since time.Time, maxPointsPerSeries int) ([]ServiceMetricSnapshot, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if since.IsZero() {
		since = time.Now().UTC().Add(-3 * time.Hour)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	series := map[string][]ServiceMetricSnapshot{}
	for _, snapshot := range s.metricHistory {
		if snapshot.ObservedAt.Before(since) {
			continue
		}
		key := strings.Join([]string{snapshot.ServiceID, snapshot.ServiceType, snapshot.Name}, "\x00")
		series[key] = append(series[key], snapshot)
	}
	if maxPointsPerSeries <= 0 {
		maxPointsPerSeries = 360
	}
	out := make([]ServiceMetricSnapshot, 0)
	for _, points := range series {
		sort.SliceStable(points, func(i, j int) bool { return points[i].ObservedAt.Before(points[j].ObservedAt) })
		if len(points) <= maxPointsPerSeries {
			out = append(out, points...)
			continue
		}
		for bucket := 0; bucket < maxPointsPerSeries; bucket++ {
			end := (bucket + 1) * len(points) / maxPointsPerSeries
			start := bucket * len(points) / maxPointsPerSeries
			if end > start {
				out = append(out, points[end-1])
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].ObservedAt.Equal(out[j].ObservedAt) {
			return out[i].ServiceID+out[i].Name < out[j].ServiceID+out[j].Name
		}
		return out[i].ObservedAt.Before(out[j].ObservedAt)
	})
	return out, nil
}

func (s *MemoryAuthStore) UpdateServiceRuntimeReport(ctx context.Context, report ServiceRuntimeReport) (RegisteredService, error) {
	if err := ctx.Err(); err != nil {
		return RegisteredService{}, err
	}
	report = normalizeServiceRuntimeReport(report)
	if report.ServiceID == "" {
		return RegisteredService{}, ErrNotFound
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	svc, ok := s.services[report.ServiceID]
	if !ok {
		return RegisteredService{}, ErrNotFound
	}
	now := time.Now().UTC()
	if svc.Status == "pending" {
		svc.Status = "registered"
	}
	if report.Version != "" {
		svc.Version = report.Version
		svc.ReportedVersion = report.Version
	}
	if report.Commit != "" {
		svc.ReportedCommit = report.Commit
	}
	if report.BuildDate != "" {
		svc.ReportedBuildDate = report.BuildDate
	}
	if report.Hostname != "" {
		svc.ReportedHostname = report.Hostname
	}
	if report.OS != "" {
		svc.ReportedOS = report.OS
	}
	if report.Arch != "" {
		svc.ReportedArch = report.Arch
	}
	if report.Version != "" || report.Commit != "" || report.BuildDate != "" || report.Hostname != "" || report.OS != "" || report.Arch != "" {
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
	pendingStagedTokenID := ""
	if svc.StagedNodeTokenID != "" && svc.StagedNodeTokenID != svc.TokenID {
		pendingStagedTokenID = svc.StagedNodeTokenID
	}
	clearStagedNodeConfiguration(&svc)
	svc.StagedNodeTokenID = pendingStagedTokenID
	svc.UpdatedAt = time.Now().UTC()
	s.services[serviceID] = svc
	return svc, nil
}

func (s *MemoryAuthStore) ValidateServiceConfigureToken(ctx context.Context, serviceID, rawToken string, now time.Time) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	serviceID = strings.TrimSpace(serviceID)
	if serviceID == "" {
		return false, ErrNotFound
	}
	now = now.UTC()
	s.mu.Lock()
	defer s.mu.Unlock()
	svc, ok := s.services[serviceID]
	if !ok {
		return false, ErrNotFound
	}
	return svc.ConfigureTokenHash != "" &&
		svc.ConfigureTokenExpiresAt != nil &&
		svc.ConfigureTokenUsedAt == nil &&
		now.Before(svc.ConfigureTokenExpiresAt.UTC()) &&
		security.VerifyTokenHash(rawToken, svc.ConfigureTokenHash), nil
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

func (s *MemoryAuthStore) ConfigureServiceNode(ctx context.Context, serviceID, rawConfigureToken string, now time.Time, report ServiceRuntimeReport, seal NodeTokenSealer) (ServiceToken, RegisteredService, error) {
	if err := ctx.Err(); err != nil {
		return ServiceToken{}, RegisteredService{}, err
	}
	if seal == nil {
		return ServiceToken{}, RegisteredService{}, errNodeTokenSealerRequired
	}
	serviceID = strings.TrimSpace(serviceID)
	if serviceID == "" {
		return ServiceToken{}, RegisteredService{}, ErrNotFound
	}
	now = now.UTC()
	report.ServiceID = serviceID
	report = normalizeServiceRuntimeReport(report)

	s.mu.Lock()
	defer s.mu.Unlock()
	service, ok := s.services[serviceID]
	if !ok {
		return ServiceToken{}, RegisteredService{}, ErrNotFound
	}
	if service.ServiceType == "update_agent" {
		return ServiceToken{}, RegisteredService{}, ErrTwoPhaseConfigureRequired
	}
	if service.ConfigureTokenHash == "" || service.ConfigureTokenExpiresAt == nil || service.ConfigureTokenUsedAt != nil || !now.Before(*service.ConfigureTokenExpiresAt) || !security.VerifyTokenHash(rawConfigureToken, service.ConfigureTokenHash) {
		return ServiceToken{}, RegisteredService{}, ErrUnauthorized
	}
	oldToken, ok := s.serviceTokens[service.TokenID]
	if !ok || oldToken.RevokedAt != nil {
		return ServiceToken{}, RegisteredService{}, ErrNotFound
	}
	if service.ServiceType != oldToken.ServiceType {
		return ServiceToken{}, RegisteredService{}, ErrForbidden
	}

	token, _, err := newRotatedServiceToken(oldToken, now)
	if err != nil {
		return ServiceToken{}, RegisteredService{}, err
	}
	ciphertext, nonce, err := seal(token.RawToken)
	if err != nil {
		return ServiceToken{}, RegisteredService{}, err
	}
	if err := ctx.Err(); err != nil {
		return ServiceToken{}, RegisteredService{}, err
	}

	oldTokenStillReferenced := false
	for candidateID, candidate := range s.services {
		if candidateID != serviceID && candidate.TokenID == oldToken.ID {
			oldTokenStillReferenced = true
			break
		}
	}
	if !oldTokenStillReferenced {
		oldToken.RevokedAt = &now
	}
	s.serviceTokens[oldToken.ID] = oldToken
	s.serviceTokens[token.ID] = token
	service = applyServiceRuntimeReport(service, report, now)
	service.TokenID = token.ID
	service.NodeTokenCiphertext = ciphertext
	service.NodeTokenNonce = nonce
	service.ConfigureTokenUsedAt = &now
	service.LastHeartbeatAt = nil
	service.ReportedCapabilities = map[string]any{}
	service.NodeTokenRotatedAt = &now
	service.UpdatedAt = now
	s.services[serviceID] = service
	return token, service, nil
}

func (s *MemoryAuthStore) StageServiceNodeConfiguration(ctx context.Context, serviceID, rawConfigureToken string, now time.Time, seal NodeTokenSealer) (StagedServiceNodeConfiguration, error) {
	if err := ctx.Err(); err != nil {
		return StagedServiceNodeConfiguration{}, err
	}
	if seal == nil {
		return StagedServiceNodeConfiguration{}, errNodeTokenSealerRequired
	}
	serviceID = strings.TrimSpace(serviceID)
	if serviceID == "" {
		return StagedServiceNodeConfiguration{}, ErrNotFound
	}
	now = now.UTC()

	s.mu.Lock()
	defer s.mu.Unlock()
	service, ok := s.services[serviceID]
	if !ok {
		return StagedServiceNodeConfiguration{}, ErrNotFound
	}
	if service.ServiceType != "update_agent" {
		return StagedServiceNodeConfiguration{}, ErrForbidden
	}
	if service.ConfigureTokenHash == "" || service.ConfigureTokenExpiresAt == nil || service.ConfigureTokenUsedAt != nil || !now.Before(*service.ConfigureTokenExpiresAt) || !security.VerifyTokenHash(rawConfigureToken, service.ConfigureTokenHash) {
		return StagedServiceNodeConfiguration{}, ErrUnauthorized
	}
	oldToken, ok := s.serviceTokens[service.TokenID]
	if !ok {
		return StagedServiceNodeConfiguration{}, ErrNotFound
	}
	if oldToken.RevokedAt != nil &&
		(!isEmergencyRevokedNodeConfigurationAnchor(service, oldToken) ||
			(hasStagedServiceNodeConfiguration(service) &&
				!hasOnlyStagedNodeConfigurationTombstone(service))) {
		return StagedServiceNodeConfiguration{}, ErrNotFound
	}
	if oldToken.ServiceType != "update_agent" {
		return StagedServiceNodeConfiguration{}, ErrForbidden
	}
	if err := validateRequiredUpdateAgentScopes(oldToken.ServiceType, oldToken.Scopes); err != nil {
		return StagedServiceNodeConfiguration{}, err
	}
	for candidateID, candidate := range s.services {
		if !updateAgentStageTokenReferenceAllowed(
			candidateID,
			candidate.TokenID,
			candidate.StagedNodePreviousTokenID,
			candidate.StagedNodeTokenID,
			serviceID,
			oldToken.ID,
		) {
			return StagedServiceNodeConfiguration{}, ErrSystemUpdateRuntimeTokenRotationSharedToken
		}
	}
	token, _, err := newRotatedServiceToken(oldToken, now)
	if err != nil {
		return StagedServiceNodeConfiguration{}, err
	}
	ciphertext, nonce, err := seal(token.RawToken)
	if err != nil {
		return StagedServiceNodeConfiguration{}, err
	}
	activationRandom, err := security.RandomToken(32)
	if err != nil {
		return StagedServiceNodeConfiguration{}, err
	}
	if err := ctx.Err(); err != nil {
		return StagedServiceNodeConfiguration{}, err
	}
	activationToken := "ast_act_" + activationRandom
	service.StagedNodePreviousTokenID = oldToken.ID
	service.StagedNodeTokenID = token.ID
	service.StagedNodeTokenHash = token.TokenHash
	service.StagedNodeTokenScopes = append([]string(nil), token.Scopes...)
	service.StagedNodeTokenCiphertext = ciphertext
	service.StagedNodeTokenNonce = nonce
	service.StagedNodeActivationTokenHash = security.HashToken(activationToken)
	service.StagedNodeTokenAt = &now
	service.ConfigureTokenUsedAt = &now
	service.UpdatedAt = now
	s.services[serviceID] = service
	return StagedServiceNodeConfiguration{Token: token, Service: service, ActivationToken: activationToken, ActivationExpiresAt: *service.ConfigureTokenExpiresAt}, nil
}

func (s *MemoryAuthStore) ActivateServiceNodeConfiguration(ctx context.Context, serviceID, configurationID, rawActivationToken string, now time.Time, report ServiceRuntimeReport) (ServiceToken, RegisteredService, bool, error) {
	if err := ctx.Err(); err != nil {
		return ServiceToken{}, RegisteredService{}, false, err
	}
	serviceID = strings.TrimSpace(serviceID)
	configurationID = strings.TrimSpace(configurationID)
	rawActivationToken = strings.TrimSpace(rawActivationToken)
	if serviceID == "" || configurationID == "" || rawActivationToken == "" {
		return ServiceToken{}, RegisteredService{}, false, ErrUnauthorized
	}
	now = now.UTC()
	report.ServiceID = serviceID
	report = normalizeServiceRuntimeReport(report)

	s.mu.Lock()
	defer s.mu.Unlock()
	service, ok := s.services[serviceID]
	if !ok {
		return ServiceToken{}, RegisteredService{}, false, ErrNotFound
	}
	if service.ServiceType != "update_agent" {
		return ServiceToken{}, RegisteredService{}, false, ErrForbidden
	}
	if service.TokenID == configurationID && service.StagedNodeTokenID == "" {
		if service.ConfigureTokenUsedAt == nil ||
			service.ConfigureTokenExpiresAt != nil ||
			service.ConfigureTokenHash == "" ||
			!security.VerifyTokenHash(rawActivationToken, service.ConfigureTokenHash) {
			return ServiceToken{}, RegisteredService{}, false, ErrUnauthorized
		}
		token, ok := s.serviceTokens[service.TokenID]
		if !ok || token.RevokedAt != nil || token.ServiceType != service.ServiceType {
			return ServiceToken{}, RegisteredService{}, false, ErrUnauthorized
		}
		if err := validateRequiredUpdateAgentScopes(token.ServiceType, token.Scopes); err != nil {
			return ServiceToken{}, RegisteredService{}, false, err
		}
		return token, service, true, nil
	}
	if service.StagedNodeTokenID != configurationID || service.StagedNodeTokenHash == "" || service.StagedNodeActivationTokenHash == "" || !security.VerifyTokenHash(rawActivationToken, service.StagedNodeActivationTokenHash) {
		return ServiceToken{}, RegisteredService{}, false, ErrUnauthorized
	}
	if service.TokenID == service.StagedNodeTokenID {
		token, ok := s.serviceTokens[service.TokenID]
		if !ok || token.RevokedAt != nil || token.TokenHash != service.StagedNodeTokenHash {
			return ServiceToken{}, RegisteredService{}, false, ErrUnauthorized
		}
		if err := validateRequiredUpdateAgentScopes(token.ServiceType, token.Scopes); err != nil {
			return ServiceToken{}, RegisteredService{}, false, err
		}
		return token, service, true, nil
	}
	if service.ConfigureTokenExpiresAt == nil || !now.Before(*service.ConfigureTokenExpiresAt) || service.StagedNodeTokenAt == nil || service.TokenID != service.StagedNodePreviousTokenID {
		return ServiceToken{}, RegisteredService{}, false, ErrUnauthorized
	}
	oldToken, ok := s.serviceTokens[service.TokenID]
	if !ok ||
		oldToken.ServiceType != "update_agent" ||
		(oldToken.RevokedAt != nil &&
			!isEmergencyRevokedNodeConfigurationAnchor(service, oldToken)) {
		return ServiceToken{}, RegisteredService{}, false, ErrUnauthorized
	}
	if err := validateRequiredUpdateAgentScopes(oldToken.ServiceType, oldToken.Scopes); err != nil {
		return ServiceToken{}, RegisteredService{}, false, err
	}
	if err := validateRequiredUpdateAgentScopes(service.ServiceType, service.StagedNodeTokenScopes); err != nil {
		return ServiceToken{}, RegisteredService{}, false, err
	}
	for candidateID, candidate := range s.services {
		if !updateAgentActivationTokenReferenceAllowed(
			candidateID,
			candidate.TokenID,
			candidate.StagedNodePreviousTokenID,
			candidate.StagedNodeTokenID,
			serviceID,
			oldToken.ID,
			service.StagedNodeTokenID,
		) {
			return ServiceToken{}, RegisteredService{}, false, ErrSystemUpdateRuntimeTokenRotationSharedToken
		}
	}
	token := ServiceToken{ID: service.StagedNodeTokenID, ServiceType: "update_agent", Scopes: append([]string(nil), service.StagedNodeTokenScopes...), TokenHash: service.StagedNodeTokenHash, CreatedAt: service.StagedNodeTokenAt.UTC()}
	if err := validateServiceScopes(token.Scopes); err != nil {
		return ServiceToken{}, RegisteredService{}, false, err
	}
	if err := ctx.Err(); err != nil {
		return ServiceToken{}, RegisteredService{}, false, err
	}
	s.serviceTokens[token.ID] = token
	service = applyServiceRuntimeReport(service, report, now)
	service.TokenID = token.ID
	service.NodeTokenCiphertext = service.StagedNodeTokenCiphertext
	service.NodeTokenNonce = service.StagedNodeTokenNonce
	service.ConfigureTokenHash = service.StagedNodeActivationTokenHash
	service.ConfigureTokenExpiresAt = nil
	clearStagedNodeConfiguration(&service)
	service.LastHeartbeatAt = nil
	service.ReportedCapabilities = map[string]any{}
	service.NodeTokenRotatedAt = &now
	service.UpdatedAt = now
	s.services[serviceID] = service
	oldTokenStillReferenced := false
	for _, candidate := range s.services {
		if candidate.TokenID == oldToken.ID ||
			candidate.StagedNodePreviousTokenID == oldToken.ID ||
			candidate.StagedNodeTokenID == oldToken.ID {
			oldTokenStillReferenced = true
			break
		}
	}
	if !oldTokenStillReferenced && oldToken.RevokedAt == nil {
		oldToken.RevokedAt = &now
		s.serviceTokens[oldToken.ID] = oldToken
	}
	return token, service, false, nil
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

func (s *MemoryAuthStore) AssignServiceToStream(ctx context.Context, serviceID, streamID, actorUserID string) (RegisteredService, error) {
	return s.AssignServiceToStreamWithRole(ctx, serviceID, streamID, actorUserID, "primary")
}

func (s *MemoryAuthStore) AssignServiceToStreamWithRole(ctx context.Context, serviceID, streamID, actorUserID, assignmentRole string) (RegisteredService, error) {
	if err := ctx.Err(); err != nil {
		return RegisteredService{}, err
	}
	s.mu.Lock()
	guardBound := s.streamAssignmentGuard != nil
	s.mu.Unlock()
	if guardBound {
		return s.AssignServiceToStreamGuarded(ctx, ServiceAssignmentMutation{ServiceID: serviceID, StreamID: streamID, ActorUserID: actorUserID, AssignmentRole: assignmentRole})
	}
	assignmentRole = normalizeAssignmentRole(assignmentRole)
	s.mu.Lock()
	defer s.mu.Unlock()
	svc, ok := s.services[serviceID]
	if !ok {
		return RegisteredService{}, ErrNotFound
	}
	if !streamAssignableServiceType(svc.ServiceType) {
		return RegisteredService{}, ErrInvalidServiceAssignment
	}
	targetKey := assignmentKey(streamID, svc.ServiceType, assignmentRole, serviceID)
	replacedServiceIDs := make(map[string]bool)
	for key, assignedServiceID := range s.assignments {
		if assignedServiceID == serviceID || (assignmentRole == "primary" && assignmentKeyMatchesPrimary(key, streamID, svc.ServiceType)) || key == targetKey {
			delete(s.assignments, key)
			delete(s.assignmentIDs, key)
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
	s.assignmentIDs[targetKey] = newUUID()
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
	guardBound := s.streamAssignmentGuard != nil
	s.mu.Unlock()
	if guardBound {
		return s.UnassignServiceFromStreamGuarded(ctx, ServiceUnassignmentMutation{ServiceID: serviceID, ActorUserID: actorUserID})
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
			delete(s.assignmentIDs, key)
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

func (s *MemoryAuthStore) AssignServiceToStreamGuarded(ctx context.Context, mutation ServiceAssignmentMutation) (RegisteredService, error) {
	if err := ctx.Err(); err != nil {
		return RegisteredService{}, err
	}
	mutation.ServiceID = strings.TrimSpace(mutation.ServiceID)
	mutation.StreamID = strings.TrimSpace(mutation.StreamID)
	mutation.AssignmentRole = normalizeAssignmentRole(mutation.AssignmentRole)
	s.mu.Lock()
	defer s.mu.Unlock()
	streams := s.streamAssignmentGuard
	if streams == nil {
		return RegisteredService{}, ErrServiceAssignmentGuardUnavailable
	}
	streams.mu.Lock()
	defer streams.mu.Unlock()

	svc, ok := s.services[mutation.ServiceID]
	if !ok {
		return RegisteredService{}, ErrNotFound
	}
	if !streamAssignableServiceType(svc.ServiceType) {
		return RegisteredService{}, ErrInvalidServiceAssignment
	}
	target, ok := streams.streams[mutation.StreamID]
	if !ok || target.DeletedAt != nil {
		return RegisteredService{}, ErrNotFound
	}
	currentStreamID, currentRole, err := s.consistentServiceAssignmentLocked(svc)
	if err != nil {
		return RegisteredService{}, err
	}
	if mutation.ExpectedCurrentStreamID != nil && currentStreamID != strings.TrimSpace(*mutation.ExpectedCurrentStreamID) {
		return RegisteredService{}, ErrServiceAssignmentConflict
	}
	if currentStreamID == mutation.StreamID && currentRole == mutation.AssignmentRole {
		svc.AssignmentRole = currentRole
		return svc, nil
	}
	if currentStreamID != "" {
		source, ok := streams.streams[currentStreamID]
		if !ok || source.DeletedAt != nil {
			return RegisteredService{}, ErrServiceAssignmentConflict
		}
		if memoryStreamAssignmentProtectionLocked(streams, source).protected() {
			return RegisteredService{}, ErrServiceAssignmentProtectedStream
		}
	}
	if memoryStreamAssignmentProtectionLocked(streams, target).protected() {
		return RegisteredService{}, ErrServiceAssignmentProtectedStream
	}

	targetKey := assignmentKey(mutation.StreamID, svc.ServiceType, mutation.AssignmentRole, mutation.ServiceID)
	replacedServiceIDs := make(map[string]bool)
	for key, assignedServiceID := range s.assignments {
		if assignedServiceID == mutation.ServiceID ||
			(mutation.AssignmentRole == "primary" && assignmentKeyMatchesPrimary(key, mutation.StreamID, svc.ServiceType)) ||
			key == targetKey {
			if assignedServiceID != mutation.ServiceID {
				replaced, ok := s.services[assignedServiceID]
				if !ok {
					return RegisteredService{}, ErrServiceAssignmentConflict
				}
				replacedOwner, _, err := s.consistentServiceAssignmentLocked(replaced)
				if err != nil || replacedOwner != mutation.StreamID {
					return RegisteredService{}, ErrServiceAssignmentConflict
				}
				replacedServiceIDs[assignedServiceID] = true
			}
		}
	}

	now := time.Now().UTC()
	for key, assignedServiceID := range s.assignments {
		if assignedServiceID == mutation.ServiceID ||
			(mutation.AssignmentRole == "primary" && assignmentKeyMatchesPrimary(key, mutation.StreamID, svc.ServiceType)) ||
			key == targetKey {
			delete(s.assignments, key)
			delete(s.assignmentIDs, key)
		}
	}
	for replacedServiceID := range replacedServiceIDs {
		replaced := s.services[replacedServiceID]
		replaced.CurrentStreamID = ""
		if replaced.Status == "assigned" {
			replaced.Status = "registered"
		}
		replaced.UpdatedAt = now
		s.services[replacedServiceID] = replaced
	}
	s.assignments[targetKey] = mutation.ServiceID
	s.assignmentIDs[targetKey] = newUUID()
	svc.CurrentStreamID = mutation.StreamID
	svc.Status = "assigned"
	svc.AssignmentRole = mutation.AssignmentRole
	svc.UpdatedAt = now
	s.services[mutation.ServiceID] = svc
	return svc, nil
}

func (s *MemoryAuthStore) UnassignServiceFromStreamGuarded(ctx context.Context, mutation ServiceUnassignmentMutation) (RegisteredService, error) {
	if err := ctx.Err(); err != nil {
		return RegisteredService{}, err
	}
	mutation.ServiceID = strings.TrimSpace(mutation.ServiceID)
	s.mu.Lock()
	defer s.mu.Unlock()
	streams := s.streamAssignmentGuard
	if streams == nil {
		return RegisteredService{}, ErrServiceAssignmentGuardUnavailable
	}
	streams.mu.Lock()
	defer streams.mu.Unlock()

	svc, ok := s.services[mutation.ServiceID]
	if !ok {
		return RegisteredService{}, ErrNotFound
	}
	currentStreamID, currentRole, err := s.consistentServiceAssignmentLocked(svc)
	if err != nil {
		return RegisteredService{}, err
	}
	if mutation.ExpectedCurrentStreamID != nil && currentStreamID != strings.TrimSpace(*mutation.ExpectedCurrentStreamID) {
		return RegisteredService{}, ErrServiceAssignmentConflict
	}
	if currentStreamID == "" {
		svc.AssignmentRole = currentRole
		return svc, nil
	}
	owner, ok := streams.streams[currentStreamID]
	if !ok || owner.DeletedAt != nil {
		return RegisteredService{}, ErrServiceAssignmentConflict
	}
	if memoryStreamAssignmentProtectionLocked(streams, owner).protected() {
		return RegisteredService{}, ErrServiceUnassignProtectedStream
	}
	for key, assignedServiceID := range s.assignments {
		if assignedServiceID == mutation.ServiceID {
			delete(s.assignments, key)
			delete(s.assignmentIDs, key)
		}
	}
	svc.CurrentStreamID = ""
	if svc.Status == "assigned" {
		svc.Status = "registered"
	}
	svc.AssignmentRole = ""
	svc.UpdatedAt = time.Now().UTC()
	s.services[mutation.ServiceID] = svc
	return svc, nil
}

func (s *MemoryAuthStore) BeginStreamArchiveRetryGuarded(ctx context.Context, serviceID, streamID string) (Stream, error) {
	if err := ctx.Err(); err != nil {
		return Stream{}, err
	}
	serviceID = strings.TrimSpace(serviceID)
	streamID = strings.TrimSpace(streamID)
	s.mu.Lock()
	defer s.mu.Unlock()
	streams := s.streamAssignmentGuard
	if streams == nil {
		return Stream{}, ErrServiceAssignmentGuardUnavailable
	}
	streams.mu.Lock()
	defer streams.mu.Unlock()
	svc, ok := s.services[serviceID]
	if !ok {
		return Stream{}, ErrNotFound
	}
	if svc.ServiceType != "encoder_recorder" {
		return Stream{}, ErrInvalidServiceAssignment
	}
	owner, _, err := s.consistentServiceAssignmentLocked(svc)
	if err != nil {
		return Stream{}, err
	}
	if owner != streamID {
		return Stream{}, ErrServiceAssignmentConflict
	}
	stream, ok := streams.streams[streamID]
	if !ok || stream.DeletedAt != nil {
		return Stream{}, ErrNotFound
	}
	if streams.archiveRetryPending[streamID] {
		return stream, nil
	}
	streams.archiveRetryPending[streamID] = true
	if stream.ArchiveReportedAt != nil {
		stream.ArchiveReportedAt = nil
		stream.UpdatedAt = time.Now().UTC()
		streams.streams[streamID] = stream
		streams.artifactReports[streamID] = false
	}
	return stream, nil
}

func (s *MemoryAuthStore) consistentServiceAssignmentLocked(service RegisteredService) (string, string, error) {
	owner := ""
	role := ""
	count := 0
	for key, assignedServiceID := range s.assignments {
		if assignedServiceID != service.ServiceID {
			continue
		}
		streamID, serviceType, assignmentRole := assignmentPartsFromKey(key)
		if serviceType != service.ServiceType || streamID == "" {
			return "", "", ErrServiceAssignmentConflict
		}
		owner = streamID
		role = assignmentRole
		count++
	}
	if count > 1 || (count == 0) != (strings.TrimSpace(service.CurrentStreamID) == "") {
		return "", "", ErrServiceAssignmentConflict
	}
	if count == 1 && owner != strings.TrimSpace(service.CurrentStreamID) {
		return "", "", ErrServiceAssignmentConflict
	}
	return owner, role, nil
}

func memoryStreamAssignmentProtectionLocked(streams *MemoryStreamStore, stream Stream) streamAssignmentProtection {
	state := streamAssignmentProtection{Stream: stream, ArchiveRetryPending: streams.archiveRetryPending[stream.ID], HasArchiveReport: streams.artifactReports[stream.ID]}
	for _, artifact := range streams.artifacts[stream.ID] {
		if isArchiveRecordingArtifact(artifact) {
			state.HasRecordingArtifact = true
			break
		}
	}
	return state
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
