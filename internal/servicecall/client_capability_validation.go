package servicecall

import (
	"errors"
	"github.com/example/autostream-control-panel/internal/store"
	"net"
	"net/url"
	"strconv"
	"strings"
)

func firstServiceURL(services []store.RegisteredService, serviceType string) string {
	return firstService(services, serviceType).PublicURL
}

func firstService(services []store.RegisteredService, serviceType string) store.RegisteredService {
	for _, service := range services {
		if service.ServiceType == serviceType {
			return service
		}
	}
	return store.RegisteredService{}
}

func workerVideoCapabilitiesEnabled(services []store.RegisteredService) bool {
	worker := firstService(services, "worker")
	encoder := firstService(services, "encoder_recorder")
	workerEnabled := reportedCapabilityTrue(worker.ReportedCapabilities, "scene_frames_mjpeg_srt")
	encoderEnabled := reportedCapabilityTrue(encoder.ReportedCapabilities, "worker_frame_ingest_mjpeg_srt")
	return workerEnabled && encoderEnabled
}

// WorkerVideoCapabilitiesEnabled reports whether the exact primary services
// advertise the compatible ends of the new media path. A successful Worker
// dispatch is still required before callers may persist an active contract.
func WorkerVideoCapabilitiesEnabled(services []store.RegisteredService) bool {
	return workerVideoCapabilitiesEnabled(services)
}

func workerVideoCapabilityMismatch(services []store.RegisteredService) (ReadinessIssue, bool) {
	worker := firstService(services, "worker")
	encoder := firstService(services, "encoder_recorder")
	workerEnabled := reportedCapabilityTrue(worker.ReportedCapabilities, "scene_frames_mjpeg_srt")
	encoderEnabled := reportedCapabilityTrue(encoder.ReportedCapabilities, "worker_frame_ingest_mjpeg_srt")
	if workerEnabled == encoderEnabled {
		return ReadinessIssue{}, false
	}
	missing := worker
	if workerEnabled {
		missing = encoder
	}
	return ReadinessIssue{
		ServiceID: missing.ServiceID, ServiceType: missing.ServiceType,
		Code:    "worker_video_capability_mismatch",
		Message: "Worker scene-frame output and Encoder MJPEG frame ingest capabilities must be upgraded together.",
	}, true
}

func reportedCapabilityTrue(capabilities map[string]any, name string) bool {
	value, ok := capabilities[name].(bool)
	return ok && value
}

func validDiscordTargetID(value string) bool {
	value = strings.TrimSpace(value)
	if len(value) < 1 || len(value) > 32 {
		return false
	}
	for _, r := range value {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func validateWorkerVideoIngestRoute(route workerVideoIngestRoute) error {
	rawURL := route.URL
	if rawURL == "" || rawURL != strings.TrimSpace(rawURL) {
		return errors.New("invalid SRT ingest URL")
	}
	parsed, err := url.Parse(rawURL)
	if err != nil || !strings.EqualFold(parsed.Scheme, "srt") || parsed.Opaque != "" || parsed.User != nil || parsed.Path != "" || parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "" {
		return errors.New("invalid SRT ingest URL")
	}
	host, portText, err := net.SplitHostPort(parsed.Host)
	port, portErr := strconv.Atoi(portText)
	if err != nil || portErr != nil || strings.TrimSpace(host) == "" || port < 1 || port > 65535 {
		return errors.New("invalid SRT ingest URL")
	}
	secret := route.secret()
	if len(secret) < 32 || len(secret) > 79 || !isBase64URLCredential(secret) || route.PBKeyLen != 32 {
		return errors.New("invalid SRT ingest credential")
	}
	return nil
}

func isBase64URLCredential(value string) bool {
	for _, character := range []byte(value) {
		if (character >= 'A' && character <= 'Z') || (character >= 'a' && character <= 'z') || (character >= '0' && character <= '9') || character == '_' || character == '-' {
			continue
		}
		return false
	}
	return true
}

func capabilityBool(capabilities map[string]any, name string) (bool, bool) {
	if capabilities == nil {
		return false, false
	}
	value, ok := capabilities[name]
	if !ok {
		return false, false
	}
	switch typed := value.(type) {
	case bool:
		return typed, true
	case string:
		normalized := strings.ToLower(strings.TrimSpace(typed))
		if normalized == "true" || normalized == "1" || normalized == "yes" {
			return true, true
		}
		if normalized == "false" || normalized == "0" || normalized == "no" {
			return false, true
		}
	}
	return false, false
}
