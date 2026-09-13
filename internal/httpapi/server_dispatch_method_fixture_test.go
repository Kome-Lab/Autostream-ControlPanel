package httpapi

import (
	"context"
	"github.com/example/autostream-control-panel/internal/servicecall"
	"github.com/example/autostream-control-panel/internal/store"
	ytlive "github.com/example/autostream-control-panel/internal/youtube"
	"io"
	"net/http"
	"strings"
	"time"
)

func (f *fakeYouTubeLiveClient) Prepare(ctx context.Context, req ytlive.PrepareRequest) (ytlive.PreparedOutput, error) {
	f.prepareCalls++
	f.prepareRequest = req
	if f.prepareErr != nil {
		return ytlive.PreparedOutput{}, f.prepareErr
	}
	return f.prepared, nil
}

func (f *fakeYouTubeLiveClient) PrepareRelayStatic(ctx context.Context, req ytlive.RelayStaticPrepareRequest) (ytlive.PreparedOutput, error) {
	f.relayStaticPrepareCalls++
	f.relayStaticRequest = req
	if f.onRelayStaticPrepare != nil {
		f.onRelayStaticPrepare(ctx, req)
	}
	if f.relayStaticPrepareErr != nil {
		return ytlive.PreparedOutput{}, f.relayStaticPrepareErr
	}
	return f.relayStaticPrepared, nil
}

func (f *fakeYouTubeLiveClient) DeleteRelayStaticBroadcast(ctx context.Context, req ytlive.RelayStaticBroadcastCleanupRequest) error {
	f.relayStaticCleanupCalls++
	f.relayStaticCleanupRequest = req
	return f.relayStaticCleanupErr
}

func (f *fakeYouTubeLiveClient) Complete(ctx context.Context, req ytlive.CompleteRequest) error {
	f.completeCalls++
	f.completeRequest = req
	if f.onComplete != nil {
		f.onComplete(ctx, req)
	}
	return f.completeErr
}

type readinessBlockDispatcher struct {
	fakeServiceDispatcher
	issues []servicecall.ReadinessIssue
}

type relayStaticPreFenceMutationDispatcher struct {
	*fakeServiceDispatcher
	mutate func()
}

func (f *relayStaticPreFenceMutationDispatcher) StartReadinessIssues(_ []store.RegisteredService, _ servicecall.StartRequest, _ time.Time) []servicecall.ReadinessIssue {
	if mutate := f.mutate; mutate != nil {
		f.mutate = nil
		mutate()
	}
	return nil
}

type relayStaticNotificationDispatcher struct {
	*fakeServiceDispatcher
	notifyCalls     int
	notifiedStream  store.Stream
	notifiedEventID string
	notifiedURL     string
}

func (f *relayStaticNotificationDispatcher) NotifyDiscordYouTubeLive(_ context.Context, stream store.Stream, _ []store.RegisteredService, eventID, watchURL string) servicecall.DispatchResult {
	f.notifyCalls++
	f.notifiedStream = stream
	f.notifiedEventID = eventID
	f.notifiedURL = watchURL
	return servicecall.DispatchResult{ServiceID: "discord_bot-01", ServiceType: "discord_bot", Endpoint: "/streams/" + stream.ID + "/notifications/youtube-live", StatusCode: http.StatusOK, Success: true, MessageID: "discord-static-message-01"}
}

func (f *readinessBlockDispatcher) StartReadinessIssues(services []store.RegisteredService, req servicecall.StartRequest, now time.Time) []servicecall.ReadinessIssue {
	return f.issues
}

func (f *fakeServiceDispatcher) Start(ctx context.Context, stream store.Stream, services []store.RegisteredService, req servicecall.StartRequest) []servicecall.DispatchResult {
	f.startCalls++
	f.startedStream = stream
	f.startRequest = req
	f.startedServices = append([]store.RegisteredService(nil), services...)
	if f.startResultsOverride != nil {
		return append([]servicecall.DispatchResult(nil), f.startResultsOverride...)
	}
	if f.failStart {
		return []servicecall.DispatchResult{{ServiceID: services[0].ServiceID, ServiceType: services[0].ServiceType, Endpoint: "/jobs/start", Success: false, Error: f.failureError()}}
	}
	results := make([]servicecall.DispatchResult, 0, len(services))
	workerVideoNegotiated := servicecall.WorkerVideoCapabilitiesEnabled(services)
	for _, service := range services {
		result := servicecall.DispatchResult{ServiceID: service.ServiceID, ServiceType: service.ServiceType, Endpoint: "/start", StatusCode: http.StatusAccepted, Success: true}
		if workerVideoNegotiated && service.ServiceType == "worker" {
			result.VideoOverlayBurnInNegotiated = true
		}
		results = append(results, result)
	}
	return results
}

func (f *blockingStartDispatcher) Start(ctx context.Context, stream store.Stream, services []store.RegisteredService, req servicecall.StartRequest) []servicecall.DispatchResult {
	select {
	case f.startEntered <- struct{}{}:
	default:
	}
	<-f.releaseStart
	return f.fakeServiceDispatcher.Start(ctx, stream, services, req)
}

func (f *blockingStartDispatcher) Stop(ctx context.Context, stream store.Stream, services []store.RegisteredService) []servicecall.DispatchResult {
	if f.stopEntered != nil {
		select {
		case f.stopEntered <- struct{}{}:
		default:
		}
	}
	return f.fakeServiceDispatcher.Stop(ctx, stream, services)
}

func (f *fakeServiceDispatcher) Stop(ctx context.Context, stream store.Stream, services []store.RegisteredService) []servicecall.DispatchResult {
	f.stopCalls++
	f.stoppedStream = stream
	f.stoppedServices = append([]store.RegisteredService(nil), services...)
	if f.stopResultsOverride != nil {
		return append([]servicecall.DispatchResult(nil), f.stopResultsOverride...)
	}
	if f.failStop {
		return []servicecall.DispatchResult{{ServiceID: services[0].ServiceID, ServiceType: services[0].ServiceType, Endpoint: "/stop", Success: false, Error: f.failureError()}}
	}
	results := make([]servicecall.DispatchResult, 0, len(services))
	for _, service := range services {
		results = append(results, servicecall.DispatchResult{ServiceID: service.ServiceID, ServiceType: service.ServiceType, Endpoint: "/stop", StatusCode: http.StatusAccepted, Success: true})
	}
	return results
}

func (f *fakeServiceDispatcher) RetryArchiveUpload(ctx context.Context, stream store.Stream, services []store.RegisteredService, archiveConfig map[string]any) []servicecall.DispatchResult {
	f.retryCalls++
	f.retriedStream = stream
	f.retriedArchiveConfig = archiveConfig
	for _, service := range services {
		if service.ServiceType == "encoder_recorder" {
			f.retriedServices = append(f.retriedServices, service)
		}
	}
	if f.failRetry && len(f.retriedServices) > 0 {
		return []servicecall.DispatchResult{{ServiceID: f.retriedServices[0].ServiceID, ServiceType: f.retriedServices[0].ServiceType, Endpoint: "/streams/package", Success: false, Error: f.failureError()}}
	}
	results := make([]servicecall.DispatchResult, 0, len(f.retriedServices))
	for _, service := range f.retriedServices {
		results = append(results, servicecall.DispatchResult{ServiceID: service.ServiceID, ServiceType: service.ServiceType, Endpoint: "/streams/package", StatusCode: http.StatusAccepted, Success: true})
	}
	return results
}

func (f *fakeServiceDispatcher) AudioStatus(ctx context.Context, stream store.Stream, services []store.RegisteredService) servicecall.AudioStatusResult {
	f.audioStatusCalls++
	f.audioStatusStream = stream
	f.audioStatusServices = append([]store.RegisteredService(nil), services...)
	if f.failAudioStatus {
		return servicecall.AudioStatusResult{ServiceID: services[0].ServiceID, ServiceType: services[0].ServiceType, Endpoint: "/streams/" + stream.ID + "/audio-status", Success: false, Error: "failed"}
	}
	if f.audioStatus.Success || f.audioStatus.Error != "" {
		return f.audioStatus
	}
	return servicecall.AudioStatusResult{
		ServiceID:   services[0].ServiceID,
		ServiceType: services[0].ServiceType,
		Endpoint:    "/streams/" + stream.ID + "/audio-status",
		StatusCode:  http.StatusOK,
		Success:     true,
		AudioBridgeState: servicecall.AudioBridgeStatus{
			StreamID:     stream.ID,
			BridgeActive: true,
		},
	}
}

func (f *fakeServiceDispatcher) WorkerEvents(ctx context.Context, stream store.Stream, services []store.RegisteredService) servicecall.WorkerEventsResult {
	f.workerEventsCalls++
	f.workerEventsStream = stream
	f.workerEventsServices = append([]store.RegisteredService(nil), services...)
	if f.failWorkerEvents {
		return servicecall.WorkerEventsResult{ServiceID: services[0].ServiceID, ServiceType: services[0].ServiceType, Endpoint: "/streams/" + stream.ID + "/worker-events", Success: false, Error: "failed"}
	}
	if f.workerEvents.Success || f.workerEvents.Error != "" {
		return f.workerEvents
	}
	return servicecall.WorkerEventsResult{
		ServiceID:   services[0].ServiceID,
		ServiceType: services[0].ServiceType,
		Endpoint:    "/streams/" + stream.ID + "/worker-events",
		StatusCode:  http.StatusOK,
		Success:     true,
		Events:      []servicecall.WorkerEvent{},
	}
}

func (f *fakeServiceDispatcher) EncoderPreflight(ctx context.Context, stream store.Stream, services []store.RegisteredService) servicecall.ServicePreflightResult {
	f.encoderPreflightCalls++
	f.encoderPreflightStream = stream
	f.encoderPreflightServices = append([]store.RegisteredService(nil), services...)
	if f.failEncoderPreflight {
		return servicecall.ServicePreflightResult{ServiceID: services[0].ServiceID, ServiceType: services[0].ServiceType, Endpoint: "/preflight", Success: false, Error: "failed"}
	}
	if f.encoderPreflight.Success || f.encoderPreflight.Error != "" {
		return f.encoderPreflight
	}
	return servicecall.ServicePreflightResult{
		ServiceID:   services[0].ServiceID,
		ServiceType: services[0].ServiceType,
		Endpoint:    "/preflight",
		StatusCode:  http.StatusOK,
		Success:     true,
		Ready:       true,
		CheckedAt:   time.Now().UTC(),
		Checks:      []servicecall.ServicePreflightCheck{},
	}
}

func (f *fakeServiceDispatcher) SendWorkerEvent(ctx context.Context, stream store.Stream, services []store.RegisteredService, req servicecall.WorkerEventRequest) servicecall.DispatchResult {
	f.workerEventSendCalls++
	f.workerEventStream = stream
	f.workerEventServices = append([]store.RegisteredService(nil), services...)
	f.workerEventRequest = req
	if f.failWorkerEventSend {
		return servicecall.DispatchResult{ServiceID: services[0].ServiceID, ServiceType: services[0].ServiceType, Endpoint: "/streams/" + stream.ID + "/events/" + req.EventType, Success: false, Error: f.failureError()}
	}
	return servicecall.DispatchResult{
		ServiceID:   services[0].ServiceID,
		ServiceType: services[0].ServiceType,
		Endpoint:    "/streams/" + stream.ID + "/events/" + req.EventType,
		StatusCode:  http.StatusAccepted,
		Success:     true,
	}
}

func (f *fakeServiceDispatcher) DownloadArchiveArtifact(ctx context.Context, stream store.Stream, services []store.RegisteredService, artifact store.StreamArtifact, byteRange string) servicecall.ArchiveArtifactDownloadResult {
	f.archiveDownloadCalls++
	f.archiveStream = stream
	f.archiveArtifact = artifact
	f.archiveByteRange = byteRange
	if f.failArchiveAction {
		return servicecall.ArchiveArtifactDownloadResult{ServiceID: services[0].ServiceID, ServiceType: services[0].ServiceType, Endpoint: "/streams/" + stream.ID + "/artifacts/" + artifact.Name, Success: false, Error: f.failureError()}
	}
	if byteRange == "bytes=0-3" {
		return servicecall.ArchiveArtifactDownloadResult{ServiceID: services[0].ServiceID, ServiceType: services[0].ServiceType, Endpoint: "/streams/" + stream.ID + "/artifacts/" + artifact.Name, StatusCode: http.StatusPartialContent, Success: true, FileName: artifact.Name, ContentType: "video/mp4", ContentRange: "bytes 0-3/13", AcceptRanges: "bytes", SizeBytes: 4, Body: io.NopCloser(strings.NewReader("arch"))}
	}
	return servicecall.ArchiveArtifactDownloadResult{ServiceID: services[0].ServiceID, ServiceType: services[0].ServiceType, Endpoint: "/streams/" + stream.ID + "/artifacts/" + artifact.Name, StatusCode: http.StatusOK, Success: true, FileName: artifact.Name, ContentType: "video/mp4", SizeBytes: 13, Body: io.NopCloser(strings.NewReader("archive-bytes"))}
}

func (f *fakeServiceDispatcher) DeleteArchiveArtifact(ctx context.Context, stream store.Stream, services []store.RegisteredService, artifact store.StreamArtifact) servicecall.DispatchResult {
	f.archiveDeleteCalls++
	f.archiveStream = stream
	f.archiveArtifact = artifact
	if f.failArchiveAction {
		return servicecall.DispatchResult{ServiceID: services[0].ServiceID, ServiceType: services[0].ServiceType, Endpoint: "/streams/" + stream.ID + "/artifacts/" + artifact.Name, Success: false, Error: f.failureError()}
	}
	return servicecall.DispatchResult{ServiceID: services[0].ServiceID, ServiceType: services[0].ServiceType, Endpoint: "/streams/" + stream.ID + "/artifacts/" + artifact.Name, StatusCode: http.StatusOK, Success: true}
}

func (f *fakeServiceDispatcher) RenameArchiveArtifact(ctx context.Context, stream store.Stream, services []store.RegisteredService, artifact store.StreamArtifact, name string) servicecall.DispatchResult {
	f.archiveRenameCalls++
	f.archiveStream = stream
	f.archiveArtifact = artifact
	f.archiveRenameName = name
	if f.failArchiveAction {
		return servicecall.DispatchResult{ServiceID: services[0].ServiceID, ServiceType: services[0].ServiceType, Endpoint: "/streams/" + stream.ID + "/artifacts/" + artifact.Name, Success: false, Error: f.failureError()}
	}
	return servicecall.DispatchResult{ServiceID: services[0].ServiceID, ServiceType: services[0].ServiceType, Endpoint: "/streams/" + stream.ID + "/artifacts/" + artifact.Name, StatusCode: http.StatusOK, Success: true}
}

func (f *fakeServiceDispatcher) failureError() string {
	if f.dispatchFailureError != "" {
		return f.dispatchFailureError
	}
	return "failed"
}
