package httpapi

import (
	"net/url"
	"time"

	"github.com/example/autostream-control-panel/internal/store"
)

type nodeListResponse struct {
	ID                      string         `json:"id"`
	ServiceID               string         `json:"service_id"`
	ServiceType             string         `json:"service_type"`
	ServiceName             string         `json:"service_name"`
	Description             string         `json:"description,omitempty"`
	Host                    string         `json:"host,omitempty"`
	Port                    int            `json:"port,omitempty"`
	SSLEnabled              bool           `json:"ssl_enabled"`
	PublicURL               string         `json:"public_url"`
	Version                 string         `json:"version"`
	ReportedVersion         string         `json:"reported_version,omitempty"`
	ReportedCommit          string         `json:"reported_commit,omitempty"`
	ReportedBuildDate       string         `json:"reported_build_date,omitempty"`
	Status                  string         `json:"status"`
	HealthStatus            string         `json:"health_status"`
	HeartbeatStale          bool           `json:"heartbeat_stale"`
	HeartbeatAgeSec         *int64         `json:"heartbeat_age_sec,omitempty"`
	LastHeartbeatAt         *time.Time     `json:"last_heartbeat_at,omitempty"`
	LastReportedAt          *time.Time     `json:"last_reported_at,omitempty"`
	CurrentStreamID         string         `json:"current_stream_id,omitempty"`
	ReportedHostname        string         `json:"reported_hostname,omitempty"`
	ReportedOS              string         `json:"reported_os,omitempty"`
	ReportedArch            string         `json:"reported_arch,omitempty"`
	Capabilities            map[string]any `json:"capabilities,omitempty"`
	ReportedCapabilities    map[string]any `json:"reported_capabilities,omitempty"`
	Metrics                 map[string]any `json:"metrics,omitempty"`
	ConfigureTokenExpiresAt *time.Time     `json:"configure_token_expires_at,omitempty"`
	ConfigureTokenUsedAt    *time.Time     `json:"configure_token_used_at,omitempty"`
	NodeTokenRotatedAt      *time.Time     `json:"node_token_rotated_at,omitempty"`
	CreatedAt               time.Time      `json:"created_at"`
	UpdatedAt               time.Time      `json:"updated_at"`
}

func nodeListResponses(services []store.RegisteredService, now time.Time) []nodeListResponse {
	out := make([]nodeListResponse, 0, len(services))
	for _, service := range services {
		healthStatus, heartbeatStale, heartbeatAgeSec := serviceHealthFields(service, now)
		out = append(out, nodeListResponse{
			ID:                      service.ServiceID,
			ServiceID:               service.ServiceID,
			ServiceType:             service.ServiceType,
			ServiceName:             service.ServiceName,
			Description:             service.Description,
			Host:                    service.Host,
			Port:                    service.Port,
			SSLEnabled:              service.SSLEnabled,
			PublicURL:               service.PublicURL,
			Version:                 service.Version,
			ReportedVersion:         service.ReportedVersion,
			ReportedCommit:          service.ReportedCommit,
			ReportedBuildDate:       service.ReportedBuildDate,
			Status:                  service.Status,
			HealthStatus:            healthStatus,
			HeartbeatStale:          heartbeatStale,
			HeartbeatAgeSec:         heartbeatAgeSec,
			LastHeartbeatAt:         service.LastHeartbeatAt,
			LastReportedAt:          service.LastReportedAt,
			CurrentStreamID:         service.CurrentStreamID,
			ReportedHostname:        service.ReportedHostname,
			ReportedOS:              service.ReportedOS,
			ReportedArch:            service.ReportedArch,
			Capabilities:            service.Capabilities,
			ReportedCapabilities:    service.ReportedCapabilities,
			Metrics:                 service.Metrics,
			ConfigureTokenExpiresAt: service.ConfigureTokenExpiresAt,
			ConfigureTokenUsedAt:    service.ConfigureTokenUsedAt,
			NodeTokenRotatedAt:      service.NodeTokenRotatedAt,
			CreatedAt:               service.CreatedAt,
			UpdatedAt:               service.UpdatedAt,
		})
	}
	return out
}

type serviceHealthResponse struct {
	store.RegisteredService
	HealthStatus    string `json:"health_status"`
	HeartbeatStale  bool   `json:"heartbeat_stale"`
	HeartbeatAgeSec *int64 `json:"heartbeat_age_sec,omitempty"`
}

func serviceHealthResponses(services []store.RegisteredService, now time.Time) []serviceHealthResponse {
	out := make([]serviceHealthResponse, 0, len(services))
	for _, service := range services {
		response := serviceHealthResponse{RegisteredService: service}
		response.HealthStatus, response.HeartbeatStale, response.HeartbeatAgeSec = serviceHealthFields(service, now)
		out = append(out, response)
	}
	return out
}

func serviceHealthFields(service store.RegisteredService, now time.Time) (string, bool, *int64) {
	if service.Status == "offline" {
		return "offline", true, heartbeatAge(service.LastHeartbeatAt, now)
	}
	if service.LastHeartbeatAt == nil {
		return "unconfigured", true, nil
	}
	age := heartbeatAge(service.LastHeartbeatAt, now)
	if age != nil && time.Duration(*age)*time.Second > heartbeatOfflineAfter() {
		return "offline", true, age
	}
	if age != nil && time.Duration(*age)*time.Second > heartbeatWarningAfter() {
		return "warning", true, age
	}
	return "healthy", false, age
}

func publicArchiveShareAdmin(share store.StreamArtifactShare) map[string]any {
	status := "active"
	if share.RevokedAt != nil {
		status = "revoked"
	} else if !share.ExpiresAt.After(time.Now().UTC()) {
		status = "expired"
	}
	return map[string]any{
		"id":             share.ID,
		"stream_id":      share.StreamID,
		"artifact_id":    share.ArtifactID,
		"allow_download": share.AllowDownload,
		"expires_at":     share.ExpiresAt,
		"created_at":     share.CreatedAt,
		"revoked_at":     share.RevokedAt,
		"status":         status,
	}
}

func publicArchiveSharePayload(share store.StreamArtifactShare, stream store.Stream, artifact store.StreamArtifact, token string) map[string]any {
	payload := map[string]any{
		"id":             share.ID,
		"stream_id":      stream.ID,
		"stream_name":    stream.Name,
		"artifact_id":    artifact.ID,
		"artifact_name":  artifact.Name,
		"artifact_kind":  artifact.Kind,
		"size_bytes":     artifact.SizeBytes,
		"created_at":     artifact.CreatedAt,
		"allow_download": share.AllowDownload,
		"expires_at":     share.ExpiresAt,
		"playback_url":   "/archive-shares/" + url.PathEscape(token) + "/download",
	}
	if share.AllowDownload {
		payload["download_url"] = "/archive-shares/" + url.PathEscape(token) + "/download?download=1"
	}
	return payload
}
