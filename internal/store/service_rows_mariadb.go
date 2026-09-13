package store

import (
	"database/sql"
	"encoding/json"
)

type serviceScanner interface {
	Scan(dest ...any) error
}

func scanService(row serviceScanner) (RegisteredService, error) {
	var service RegisteredService
	var desiredEndpoint ServiceEndpoint
	var reportedEndpoint ServiceEndpoint
	var lastHeartbeat sql.NullTime
	var lastReported sql.NullTime
	var currentStream sql.NullString
	var capabilities string
	var reportedCapabilities string
	var metrics string
	var stagedTokenScopes string
	var stagedTokenAt sql.NullTime
	var configureExpires sql.NullTime
	var configureUsed sql.NullTime
	var nodeTokenRotated sql.NullTime
	err := row.Scan(
		&service.ServiceID, &service.ServiceType, &service.ServiceName, &service.Description,
		&service.Host, &service.Port, &service.SSLEnabled, &service.PublicURL,
		&service.TransportMode, &service.ExecutionHostID, &service.OwnershipEpoch,
		&desiredEndpoint.Host, &desiredEndpoint.Port, &desiredEndpoint.SSLEnabled, &desiredEndpoint.PublicURL,
		&reportedEndpoint.Host, &reportedEndpoint.Port, &reportedEndpoint.SSLEnabled, &reportedEndpoint.PublicURL,
		&service.EndpointRevision, &service.AppliedEndpointRevision, &service.EndpointStatus,
		&service.AppliedConfigRevision, &service.AppliedConfigSHA256,
		&service.Version, &service.ReportedVersion, &service.ReportedCommit, &service.ReportedBuildDate,
		&service.Status, &lastHeartbeat, &lastReported, &currentStream, &capabilities, &reportedCapabilities, &metrics, &service.TokenID,
		&service.NodeTokenCiphertext, &service.NodeTokenNonce, &service.StagedNodePreviousTokenID, &service.StagedNodeTokenID,
		&service.StagedNodeTokenHash, &stagedTokenScopes, &service.StagedNodeTokenCiphertext, &service.StagedNodeTokenNonce,
		&service.StagedNodeActivationTokenHash, &stagedTokenAt, &service.ReportedHostname, &service.ReportedOS, &service.ReportedArch,
		&configureExpires, &configureUsed, &nodeTokenRotated, &service.CreatedAt, &service.UpdatedAt,
	)
	if err != nil {
		return RegisteredService{}, err
	}
	if lastHeartbeat.Valid {
		service.LastHeartbeatAt = &lastHeartbeat.Time
	}
	if lastReported.Valid {
		service.LastReportedAt = &lastReported.Time
	}
	if currentStream.Valid {
		service.CurrentStreamID = currentStream.String
	}
	_ = json.Unmarshal([]byte(capabilities), &service.Capabilities)
	if service.Capabilities == nil {
		service.Capabilities = map[string]any{}
	}
	_ = json.Unmarshal([]byte(reportedCapabilities), &service.ReportedCapabilities)
	if service.ReportedCapabilities == nil {
		service.ReportedCapabilities = map[string]any{}
	}
	if service.ReportedVersion == "" {
		service.ReportedVersion = service.Version
	}
	_ = json.Unmarshal([]byte(stagedTokenScopes), &service.StagedNodeTokenScopes)
	if stagedTokenAt.Valid {
		service.StagedNodeTokenAt = &stagedTokenAt.Time
	}
	_ = json.Unmarshal([]byte(metrics), &service.Metrics)
	if service.Metrics == nil {
		service.Metrics = map[string]any{}
	}
	if configureExpires.Valid {
		service.ConfigureTokenExpiresAt = &configureExpires.Time
	}
	if configureUsed.Valid {
		service.ConfigureTokenUsedAt = &configureUsed.Time
	}
	if nodeTokenRotated.Valid {
		service.NodeTokenRotatedAt = &nodeTokenRotated.Time
	}
	if service.Host == "" || service.Port == 0 {
		fillServiceEndpointFromURL(&service)
	}
	service.AppliedEndpoint = serviceEndpoint(service.Host, service.Port, service.SSLEnabled, service.PublicURL)
	service.DesiredEndpoint = serviceEndpoint(desiredEndpoint.Host, desiredEndpoint.Port, desiredEndpoint.SSLEnabled, desiredEndpoint.PublicURL)
	service.ReportedEndpoint = serviceEndpoint(reportedEndpoint.Host, reportedEndpoint.Port, reportedEndpoint.SSLEnabled, reportedEndpoint.PublicURL)
	hydrateServiceEndpointState(&service)
	return service, nil
}

func scanAssignedService(row serviceScanner) (RegisteredService, error) {
	service, err := scanServiceWithExtraRole(row)
	if err != nil {
		return RegisteredService{}, err
	}
	return service, nil
}

func scanServiceWithExtraRole(row serviceScanner) (RegisteredService, error) {
	var service RegisteredService
	var desiredEndpoint ServiceEndpoint
	var reportedEndpoint ServiceEndpoint
	var lastHeartbeat sql.NullTime
	var lastReported sql.NullTime
	var currentStream sql.NullString
	var capabilities string
	var reportedCapabilities string
	var metrics string
	var stagedTokenScopes string
	var stagedTokenAt sql.NullTime
	var configureExpires sql.NullTime
	var configureUsed sql.NullTime
	var nodeTokenRotated sql.NullTime
	err := row.Scan(
		&service.ServiceID, &service.ServiceType, &service.ServiceName, &service.Description,
		&service.Host, &service.Port, &service.SSLEnabled, &service.PublicURL,
		&service.TransportMode, &service.ExecutionHostID, &service.OwnershipEpoch,
		&desiredEndpoint.Host, &desiredEndpoint.Port, &desiredEndpoint.SSLEnabled, &desiredEndpoint.PublicURL,
		&reportedEndpoint.Host, &reportedEndpoint.Port, &reportedEndpoint.SSLEnabled, &reportedEndpoint.PublicURL,
		&service.EndpointRevision, &service.AppliedEndpointRevision, &service.EndpointStatus,
		&service.AppliedConfigRevision, &service.AppliedConfigSHA256,
		&service.Version, &service.ReportedVersion, &service.ReportedCommit, &service.ReportedBuildDate,
		&service.Status, &lastHeartbeat, &lastReported, &currentStream, &capabilities, &reportedCapabilities, &metrics, &service.TokenID,
		&service.NodeTokenCiphertext, &service.NodeTokenNonce, &service.StagedNodePreviousTokenID, &service.StagedNodeTokenID,
		&service.StagedNodeTokenHash, &stagedTokenScopes, &service.StagedNodeTokenCiphertext, &service.StagedNodeTokenNonce,
		&service.StagedNodeActivationTokenHash, &stagedTokenAt, &service.ReportedHostname, &service.ReportedOS, &service.ReportedArch,
		&configureExpires, &configureUsed, &nodeTokenRotated, &service.CreatedAt, &service.UpdatedAt, &service.AssignmentRole,
	)
	if err != nil {
		return RegisteredService{}, err
	}
	if lastHeartbeat.Valid {
		service.LastHeartbeatAt = &lastHeartbeat.Time
	}
	if lastReported.Valid {
		service.LastReportedAt = &lastReported.Time
	}
	if currentStream.Valid {
		service.CurrentStreamID = currentStream.String
	}
	_ = json.Unmarshal([]byte(capabilities), &service.Capabilities)
	if service.Capabilities == nil {
		service.Capabilities = map[string]any{}
	}
	_ = json.Unmarshal([]byte(reportedCapabilities), &service.ReportedCapabilities)
	if service.ReportedCapabilities == nil {
		service.ReportedCapabilities = map[string]any{}
	}
	if service.ReportedVersion == "" {
		service.ReportedVersion = service.Version
	}
	_ = json.Unmarshal([]byte(stagedTokenScopes), &service.StagedNodeTokenScopes)
	if stagedTokenAt.Valid {
		service.StagedNodeTokenAt = &stagedTokenAt.Time
	}
	_ = json.Unmarshal([]byte(metrics), &service.Metrics)
	if service.Metrics == nil {
		service.Metrics = map[string]any{}
	}
	if configureExpires.Valid {
		service.ConfigureTokenExpiresAt = &configureExpires.Time
	}
	if configureUsed.Valid {
		service.ConfigureTokenUsedAt = &configureUsed.Time
	}
	if nodeTokenRotated.Valid {
		service.NodeTokenRotatedAt = &nodeTokenRotated.Time
	}
	if service.Host == "" || service.Port == 0 {
		fillServiceEndpointFromURL(&service)
	}
	service.AppliedEndpoint = serviceEndpoint(service.Host, service.Port, service.SSLEnabled, service.PublicURL)
	service.DesiredEndpoint = serviceEndpoint(desiredEndpoint.Host, desiredEndpoint.Port, desiredEndpoint.SSLEnabled, desiredEndpoint.PublicURL)
	service.ReportedEndpoint = serviceEndpoint(reportedEndpoint.Host, reportedEndpoint.Port, reportedEndpoint.SSLEnabled, reportedEndpoint.PublicURL)
	hydrateServiceEndpointState(&service)
	return service, nil
}
