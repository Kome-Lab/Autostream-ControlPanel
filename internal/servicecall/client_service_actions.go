package servicecall

import (
	"context"
	"github.com/example/autostream-control-panel/internal/store"
	"net/http"
	"net/url"
	"strings"
)

func (c Client) RetryArchiveUpload(ctx context.Context, stream store.Stream, services []store.RegisteredService, archiveConfig map[string]any) []DispatchResult {
	results := make([]DispatchResult, 0, len(services))
	for _, service := range services {
		if service.ServiceType != "encoder_recorder" {
			continue
		}
		if strings.TrimSpace(stream.ArchiveRunID) == "" || stream.ArchiveStartedAt == nil || stream.ArchiveStartedAt.IsZero() {
			results = append(results, DispatchResult{ServiceID: service.ServiceID, ServiceType: service.ServiceType, Code: "archive_run_authority_unavailable", FailurePhase: "pre_dispatch", Error: "archive run id and start time are required"})
			continue
		}
		payload := map[string]any{
			"stream_id":      stream.ID,
			"name":           stream.Name,
			"archive_run_id": stream.ArchiveRunID,
			"started_at":     stream.ArchiveStartedAt.UTC(),
			"dry_run":        false,
		}
		results = append(results, c.post(ctx, service, "/streams/package", payload))
	}
	return results
}

func (c Client) AudioStatus(ctx context.Context, stream store.Stream, services []store.RegisteredService) AudioStatusResult {
	for _, service := range services {
		if service.ServiceType == "encoder_recorder" {
			return c.getAudioStatus(ctx, service, "/streams/"+url.PathEscape(stream.ID)+"/audio-status")
		}
	}
	return AudioStatusResult{ServiceType: "encoder_recorder", Error: "assigned encoder_recorder service not found"}
}

func (c Client) WorkerEvents(ctx context.Context, stream store.Stream, services []store.RegisteredService) WorkerEventsResult {
	for _, service := range services {
		if service.ServiceType == "encoder_recorder" {
			return c.getWorkerEvents(ctx, service, "/streams/"+url.PathEscape(stream.ID)+"/worker-events")
		}
	}
	return WorkerEventsResult{ServiceType: "encoder_recorder", Error: "assigned encoder_recorder service not found"}
}

func (c Client) EncoderPreflight(ctx context.Context, stream store.Stream, services []store.RegisteredService) ServicePreflightResult {
	for _, service := range services {
		if service.ServiceType == "encoder_recorder" {
			return c.getEncoderPreflight(ctx, service, "/preflight")
		}
	}
	return ServicePreflightResult{ServiceType: "encoder_recorder", Endpoint: "/preflight", Error: "assigned encoder_recorder service not found"}
}

func (c Client) PreviewAsset(ctx context.Context, stream store.Stream, services []store.RegisteredService, name, byteRange string) PreviewAssetResult {
	for _, service := range services {
		if service.ServiceType == "encoder_recorder" {
			endpoint := "/streams/" + url.PathEscape(stream.ID) + "/preview/" + url.PathEscape(name)
			return c.getPreviewAsset(ctx, service, endpoint, name, byteRange)
		}
	}
	return PreviewAssetResult{ServiceType: "encoder_recorder", Error: "assigned encoder_recorder service not found"}
}

func (c Client) NotifyDiscordYouTubeLive(ctx context.Context, stream store.Stream, services []store.RegisteredService, eventID, watchURL string) DispatchResult {
	for _, service := range services {
		if service.ServiceType != "discord_bot" {
			continue
		}
		endpoint := "/streams/" + url.PathEscape(stream.ID) + "/notifications/youtube-live"
		payload := map[string]string{"event_id": strings.TrimSpace(eventID), "watch_url": strings.TrimSpace(watchURL)}
		// This is intentionally one attempt. A response loss (and many 5xx
		// outcomes) may occur after Discord accepted the message. The durable
		// Control Panel outbox decides whether an explicit service receipt makes
		// a retry safe; this transport client must not create a hidden retry loop.
		return c.post(ctx, service, endpoint, payload)
	}
	return DispatchResult{ServiceType: "discord_bot", Error: "assigned discord_bot service not found"}
}

func (c Client) DownloadArchiveArtifact(ctx context.Context, stream store.Stream, services []store.RegisteredService, artifact store.StreamArtifact, byteRange string) ArchiveArtifactDownloadResult {
	for _, service := range services {
		if service.ServiceType == "encoder_recorder" {
			if strings.TrimSpace(artifact.ArchiveRunID) == "" {
				return ArchiveArtifactDownloadResult{ServiceID: service.ServiceID, ServiceType: service.ServiceType, Code: "archive_run_authority_unavailable", Error: "archive run id is required"}
			}
			return c.getArchiveArtifact(ctx, service, archiveArtifactEndpoint(stream.ID, artifact.ArchiveRunID, artifact.Name), artifact.Name, byteRange)
		}
	}
	return ArchiveArtifactDownloadResult{ServiceType: "encoder_recorder", Error: "assigned encoder_recorder service not found"}
}

func (c Client) DeleteArchiveArtifact(ctx context.Context, stream store.Stream, services []store.RegisteredService, artifact store.StreamArtifact) DispatchResult {
	for _, service := range services {
		if service.ServiceType == "encoder_recorder" {
			if strings.TrimSpace(artifact.ArchiveRunID) == "" {
				return DispatchResult{ServiceID: service.ServiceID, ServiceType: service.ServiceType, Code: "archive_run_authority_unavailable", FailurePhase: "pre_dispatch", Error: "archive run id is required"}
			}
			return c.serviceJSONAction(ctx, service, http.MethodDelete, archiveArtifactEndpoint(stream.ID, artifact.ArchiveRunID, artifact.Name), nil)
		}
	}
	return DispatchResult{ServiceType: "encoder_recorder", Error: "assigned encoder_recorder service not found"}
}

func (c Client) RenameArchiveArtifact(ctx context.Context, stream store.Stream, services []store.RegisteredService, artifact store.StreamArtifact, name string) DispatchResult {
	for _, service := range services {
		if service.ServiceType == "encoder_recorder" {
			if strings.TrimSpace(artifact.ArchiveRunID) == "" {
				return DispatchResult{ServiceID: service.ServiceID, ServiceType: service.ServiceType, Code: "archive_run_authority_unavailable", FailurePhase: "pre_dispatch", Error: "archive run id is required"}
			}
			return c.serviceJSONAction(ctx, service, http.MethodPut, archiveArtifactEndpoint(stream.ID, artifact.ArchiveRunID, artifact.Name), map[string]string{"name": name})
		}
	}
	return DispatchResult{ServiceType: "encoder_recorder", Error: "assigned encoder_recorder service not found"}
}

func (c Client) SendWorkerEvent(ctx context.Context, stream store.Stream, services []store.RegisteredService, req WorkerEventRequest) DispatchResult {
	for _, service := range services {
		if service.ServiceType != "worker" {
			continue
		}
		endpoint, payload, ok := workerEventPayload(stream, req)
		if !ok {
			return DispatchResult{ServiceID: service.ServiceID, ServiceType: service.ServiceType, Error: "unsupported worker event type"}
		}
		return c.post(ctx, service, endpoint, payload)
	}
	return DispatchResult{ServiceType: "worker", Error: "assigned worker service not found"}
}
