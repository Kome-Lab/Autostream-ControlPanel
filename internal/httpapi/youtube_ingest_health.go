package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"strings"
	"time"

	"github.com/example/autostream-control-panel/internal/store"
	ytlive "github.com/example/autostream-control-panel/internal/youtube"
)

const (
	youtubeIngestHealthDefaultInterval = 30 * time.Second
	youtubeIngestHealthLookupTimeout   = 20 * time.Second
)

type youtubeIngestHealthFingerprint struct {
	Result                string                              `json:"result"`
	ErrorCode             string                              `json:"error_code,omitempty"`
	Mode                  string                              `json:"mode"`
	LocalStreamStatus     string                              `json:"local_stream_status"`
	RuntimeLiveStreamID   string                              `json:"runtime_live_stream_id"`
	ProviderLiveStreamID  string                              `json:"provider_live_stream_id,omitempty"`
	BindingMatchesRuntime bool                                `json:"binding_matches_runtime"`
	ConfiguredResolution  string                              `json:"configured_resolution,omitempty"`
	ConfiguredFrameRate   string                              `json:"configured_frame_rate,omitempty"`
	ProviderStreamStatus  string                              `json:"provider_stream_status,omitempty"`
	ProviderHealthStatus  string                              `json:"provider_health_status,omitempty"`
	ConfigurationIssues   []ytlive.BroadcastIngestHealthIssue `json:"configuration_issues,omitempty"`
}

// RunYouTubeIngestHealthLoop records provider ingest state while the exact
// non-reusable LiveStream still exists. Runtime rows are removed after a
// successful stop, so this evidence must be captured during the broadcast.
func (s *Server) RunYouTubeIngestHealthLoop(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = youtubeIngestHealthDefaultInterval
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		if _, err := s.AuditActiveYouTubeIngestHealth(ctx); err != nil {
			log.Printf("youtube ingest health scan failed: error_class=scan_failed")
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// AuditActiveYouTubeIngestHealth reads only the safe status fields exposed by
// BroadcastIngestHealthClient. Unchanged snapshots are deduplicated in memory;
// state transitions remain durable in audit_logs after the runtime row is
// removed. No ingest URL, stream key, OAuth token, Authorization header, or raw
// provider error is included in metadata or logs.
func (s *Server) AuditActiveYouTubeIngestHealth(ctx context.Context) (map[string]any, error) {
	result := map[string]any{"checked": 0, "recorded": 0, "failed": 0, "skipped": 0}
	runtimeStore, ok := s.streams.(store.StreamYouTubeRuntimeStore)
	if !ok || s.audit == nil {
		result["skipped"] = 1
		return result, nil
	}
	healthClient, ok := s.youtubeLive.(ytlive.BroadcastIngestHealthClient)
	if !ok || healthClient == nil {
		result["skipped"] = 1
		return result, nil
	}
	runtimes, err := runtimeStore.ListStreamYouTubeRuntimes(ctx)
	if err != nil {
		return result, err
	}

	checked, recorded, failed, skipped := 0, 0, 0, 0
	seen := make(map[string]bool, len(runtimes))
	for _, runtime := range runtimes {
		if !youtubeIngestHealthRuntimeEligible(runtime) {
			skipped++
			continue
		}
		stream, err := s.streams.GetStream(ctx, runtime.StreamID)
		if errors.Is(err, store.ErrNotFound) {
			skipped++
			continue
		}
		if err != nil {
			return result, err
		}
		if !youtubeIngestHealthStreamActive(stream.Status) {
			skipped++
			continue
		}

		key := youtubeIngestHealthStateKey(runtime.StreamID, runtime.BroadcastID)
		seen[key] = true
		checked++
		metadata := map[string]any{
			"youtube_mode":           strings.TrimSpace(runtime.Mode),
			"broadcast_id":           strings.TrimSpace(runtime.BroadcastID),
			"runtime_live_stream_id": strings.TrimSpace(runtime.LiveStreamID),
			"local_stream_status":    strings.ToLower(strings.TrimSpace(stream.Status)),
		}
		fingerprint := youtubeIngestHealthFingerprint{
			Mode:                strings.TrimSpace(runtime.Mode),
			LocalStreamStatus:   strings.ToLower(strings.TrimSpace(stream.Status)),
			RuntimeLiveStreamID: strings.TrimSpace(runtime.LiveStreamID),
		}

		credentials, credentialsErr := s.youtubeOAuthCredentials(ctx, runtime.OAuthAccountID)
		if credentialsErr != nil {
			code := youtubeIngestHealthErrorCode(credentialsErr)
			metadata["error_code"] = code
			fingerprint.Result = "failure"
			fingerprint.ErrorCode = code
			failed++
			if s.writeYouTubeIngestHealthAuditIfChanged(ctx, key, runtime.StreamID, "failure", metadata, fingerprint) {
				recorded++
				log.Printf("youtube ingest health changed: stream_id=%s broadcast_id=%s result=failure error_class=%s", runtime.StreamID, runtime.BroadcastID, code)
			}
			continue
		}

		lookupCtx, cancel := context.WithTimeout(ctx, youtubeIngestHealthLookupTimeout)
		snapshot, healthErr := healthClient.BroadcastIngestHealth(lookupCtx, ytlive.BroadcastIngestHealthRequest{
			Credentials: credentials,
			BroadcastID: runtime.BroadcastID,
		})
		cancel()
		if healthErr != nil {
			code := youtubeIngestHealthErrorCode(healthErr)
			metadata["error_code"] = code
			fingerprint.Result = "failure"
			fingerprint.ErrorCode = code
			failed++
			if s.writeYouTubeIngestHealthAuditIfChanged(ctx, key, runtime.StreamID, "failure", metadata, fingerprint) {
				recorded++
				log.Printf("youtube ingest health changed: stream_id=%s broadcast_id=%s result=failure error_class=%s", runtime.StreamID, runtime.BroadcastID, code)
			}
			continue
		}

		bindingMatches := strings.TrimSpace(runtime.LiveStreamID) != "" && strings.TrimSpace(runtime.LiveStreamID) == strings.TrimSpace(snapshot.LiveStreamID)
		issues := youtubeIngestHealthIssuesMetadata(snapshot.ConfigurationIssues)
		metadata["provider_live_stream_id"] = strings.TrimSpace(snapshot.LiveStreamID)
		metadata["binding_matches_runtime"] = bindingMatches
		metadata["configured_resolution"] = strings.TrimSpace(snapshot.ConfiguredResolution)
		metadata["configured_frame_rate"] = strings.TrimSpace(snapshot.ConfiguredFrameRate)
		metadata["provider_stream_status"] = strings.TrimSpace(snapshot.StreamStatus)
		metadata["provider_health_status"] = strings.TrimSpace(snapshot.HealthStatus)
		metadata["health_last_update_time_seconds"] = snapshot.LastUpdateTimeSeconds
		metadata["configuration_issues"] = issues
		fingerprint.Result = "success"
		fingerprint.ProviderLiveStreamID = strings.TrimSpace(snapshot.LiveStreamID)
		fingerprint.BindingMatchesRuntime = bindingMatches
		fingerprint.ConfiguredResolution = strings.TrimSpace(snapshot.ConfiguredResolution)
		fingerprint.ConfiguredFrameRate = strings.TrimSpace(snapshot.ConfiguredFrameRate)
		fingerprint.ProviderStreamStatus = strings.TrimSpace(snapshot.StreamStatus)
		fingerprint.ProviderHealthStatus = strings.TrimSpace(snapshot.HealthStatus)
		fingerprint.ConfigurationIssues = append([]ytlive.BroadcastIngestHealthIssue(nil), snapshot.ConfigurationIssues...)
		if s.writeYouTubeIngestHealthAuditIfChanged(ctx, key, runtime.StreamID, "success", metadata, fingerprint) {
			recorded++
			log.Printf("youtube ingest health changed: stream_id=%s broadcast_id=%s result=success binding_matches_runtime=%t stream_status=%s health_status=%s issue_count=%d", runtime.StreamID, runtime.BroadcastID, bindingMatches, snapshot.StreamStatus, snapshot.HealthStatus, len(snapshot.ConfigurationIssues))
		}
	}

	s.clearInactiveYouTubeIngestHealthState(seen)
	result["checked"] = checked
	result["recorded"] = recorded
	result["failed"] = failed
	result["skipped"] = skipped
	return result, nil
}

func youtubeIngestHealthRuntimeEligible(runtime store.StreamYouTubeRuntime) bool {
	if runtime.DryRun || strings.TrimSpace(runtime.BroadcastID) == "" || strings.TrimSpace(runtime.OAuthAccountID) == "" {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(runtime.Mode)) {
	case "live_api", "relay_static":
		return true
	default:
		return false
	}
}

func youtubeIngestHealthStreamActive(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "starting", "live", "stopping":
		return true
	default:
		return false
	}
}

func youtubeIngestHealthStateKey(streamID, broadcastID string) string {
	return strings.TrimSpace(streamID) + "\x00" + strings.TrimSpace(broadcastID)
}

func youtubeIngestHealthIssuesMetadata(issues []ytlive.BroadcastIngestHealthIssue) []any {
	out := make([]any, 0, len(issues))
	for _, issue := range issues {
		out = append(out, map[string]any{
			"type":       strings.TrimSpace(issue.Type),
			"severity":   strings.TrimSpace(issue.Severity),
			"dimensions": append([]string(nil), issue.Dimensions...),
		})
	}
	return out
}

func youtubeIngestHealthErrorCode(err error) string {
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return "youtube_ingest_health_timeout"
	case errors.Is(err, context.Canceled):
		return "youtube_ingest_health_canceled"
	case errors.Is(err, errYouTubeOAuthAccountUnavailable):
		return errYouTubeOAuthAccountUnavailable.Error()
	case errors.Is(err, ytlive.ErrMissingCredentials):
		return ytlive.ErrMissingCredentials.Error()
	case errors.Is(err, ytlive.ErrMissingBroadcastID):
		return ytlive.ErrMissingBroadcastID.Error()
	case errors.Is(err, ytlive.ErrBroadcastNotFound):
		return ytlive.ErrBroadcastNotFound.Error()
	case errors.Is(err, ytlive.ErrBroadcastLiveStreamUnavailable):
		return ytlive.ErrBroadcastLiveStreamUnavailable.Error()
	case errors.Is(err, ytlive.ErrLiveStreamNotFound):
		return ytlive.ErrLiveStreamNotFound.Error()
	default:
		return "youtube_ingest_health_provider_unavailable"
	}
}

func (s *Server) writeYouTubeIngestHealthAuditIfChanged(ctx context.Context, key, streamID, result string, metadata map[string]any, fingerprint youtubeIngestHealthFingerprint) bool {
	encoded, err := json.Marshal(fingerprint)
	if err != nil {
		return false
	}
	s.youtubeIngestHealthMu.Lock()
	defer s.youtubeIngestHealthMu.Unlock()
	if s.youtubeIngestHealthState == nil {
		s.youtubeIngestHealthState = make(map[string]string)
	}
	if s.youtubeIngestHealthState[key] == string(encoded) {
		return false
	}
	event := store.AuditEvent{
		Timestamp:     time.Now().UTC(),
		ActorUsername: "service:control-panel",
		Action:        "youtube.ingest_health",
		ResourceType:  "stream",
		ResourceID:    strings.TrimSpace(streamID),
		Result:        result,
		Metadata:      metadata,
		RequestID:     requestID(),
	}
	if err := s.audit.WriteAudit(ctx, event); err != nil {
		log.Printf("youtube ingest health audit write failed: stream_id=%s result=%s error_class=audit_write_failed", streamID, result)
		return false
	}
	s.youtubeIngestHealthState[key] = string(encoded)
	return true
}

func (s *Server) clearInactiveYouTubeIngestHealthState(seen map[string]bool) {
	s.youtubeIngestHealthMu.Lock()
	defer s.youtubeIngestHealthMu.Unlock()
	for key := range s.youtubeIngestHealthState {
		if !seen[key] {
			delete(s.youtubeIngestHealthState, key)
		}
	}
}
