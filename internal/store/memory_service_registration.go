package store

import (
	"context"
	"time"
)

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
		svc.AppliedEndpointRevision = existing.AppliedEndpointRevision
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
