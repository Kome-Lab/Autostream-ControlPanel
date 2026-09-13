package httpapi

import (
	"context"
	"errors"
	"github.com/example/autostream-control-panel/internal/servicecall"
	"github.com/example/autostream-control-panel/internal/store"
	ytlive "github.com/example/autostream-control-panel/internal/youtube"
	"net/http"
)

type failingYouTubeRuntimeStreamStore struct {
	*store.MemoryStreamStore
	err error
}

func (s *failingYouTubeRuntimeStreamStore) SaveStreamYouTubeRuntime(context.Context, store.StreamYouTubeRuntime) error {
	return s.err
}

type failingRelayBindingClaimLookupStreamStore struct {
	*store.MemoryStreamStore
	err error
}

func (s *failingRelayBindingClaimLookupStreamStore) GetStreamYouTubeRelayBindingClaimForStream(context.Context, string) (store.YouTubeRelayBindingClaim, error) {
	return store.YouTubeRelayBindingClaim{}, s.err
}

type failingRearmStreamStore struct {
	*store.MemoryStreamStore
	failSettingsUpdate bool
}

func (s *failingRearmStreamStore) UpdateStreamSettings(ctx context.Context, id string, settings store.StreamSettings) (store.Stream, error) {
	if s.failSettingsUpdate {
		return store.Stream{}, errors.New("waiting stream settings unavailable")
	}
	return s.MemoryStreamStore.UpdateStreamSettings(ctx, id, settings)
}

func (s *failingRearmStreamStore) TransitionStreamStatus(ctx context.Context, id, expectedStatus, status string) (store.Stream, bool, error) {
	if s.failSettingsUpdate && expectedStatus == "completed" && status == "ready" {
		return store.Stream{}, false, errors.New("waiting stream status unavailable")
	}
	return s.MemoryStreamStore.TransitionStreamStatus(ctx, id, expectedStatus, status)
}

type previewFakeDispatcher struct {
	fakeServiceDispatcher
	result    servicecall.PreviewAssetResult
	calls     int
	name      string
	byteRange string
}

type notificationFakeDispatcher struct {
	fakeServiceDispatcher
	result          servicecall.DispatchResult
	notifyCalls     int
	notifiedStream  store.Stream
	notifiedEventID string
	notifiedURL     string
}

func (f *previewFakeDispatcher) PreviewAsset(ctx context.Context, stream store.Stream, services []store.RegisteredService, name, byteRange string) servicecall.PreviewAssetResult {
	f.calls++
	f.name = name
	f.byteRange = byteRange
	result := f.result
	if result.ServiceID == "" && len(services) > 0 {
		result.ServiceID = services[0].ServiceID
		result.ServiceType = services[0].ServiceType
	}
	return result
}

func (f *notificationFakeDispatcher) NotifyDiscordYouTubeLive(ctx context.Context, stream store.Stream, services []store.RegisteredService, eventID, watchURL string) servicecall.DispatchResult {
	f.notifyCalls++
	f.notifiedStream = stream
	f.notifiedEventID = eventID
	f.notifiedURL = watchURL
	result := f.result
	if result.ServiceID == "" {
		for _, service := range services {
			if service.ServiceType == "discord_bot" {
				result.ServiceID = service.ServiceID
				result.ServiceType = service.ServiceType
				break
			}
		}
	}
	if result.Endpoint == "" {
		result.Endpoint = "/streams/" + stream.ID + "/notifications/youtube-live"
	}
	if result.StatusCode == 0 && result.Error == "" && result.Code == "" {
		result.StatusCode = http.StatusOK
		result.Success = true
	}
	if result.Success && result.MessageID == "" {
		result.MessageID = "discord-message-01"
	}
	return result
}

type fakeYouTubeLiveClient struct {
	prepareCalls              int
	relayStaticPrepareCalls   int
	relayStaticCleanupCalls   int
	completeCalls             int
	prepareRequest            ytlive.PrepareRequest
	relayStaticRequest        ytlive.RelayStaticPrepareRequest
	relayStaticCleanupRequest ytlive.RelayStaticBroadcastCleanupRequest
	completeRequest           ytlive.CompleteRequest
	prepared                  ytlive.PreparedOutput
	relayStaticPrepared       ytlive.PreparedOutput
	prepareErr                error
	relayStaticPrepareErr     error
	relayStaticCleanupErr     error
	completeErr               error
	onRelayStaticPrepare      func(context.Context, ytlive.RelayStaticPrepareRequest)
	onComplete                func(context.Context, ytlive.CompleteRequest)
}

// relayStaticCompletingYouTubeLiveClient is intentionally opt-in. Generic
// LiveClient fakes must not accidentally gain the response-loss reconciliation
// contract required before a reusable static relay claim can be released.
type relayStaticCompletingYouTubeLiveClient struct {
	*fakeYouTubeLiveClient
}

type transitioningYouTubeLiveClient struct {
	*fakeYouTubeLiveClient
	transitionCalls   int
	transitionRequest ytlive.BroadcastTransitionRequest
	transitionErr     error
}

func (f *transitioningYouTubeLiveClient) TransitionBroadcastLive(_ context.Context, req ytlive.BroadcastTransitionRequest) error {
	f.transitionCalls++
	f.transitionRequest = req
	return f.transitionErr
}

func (f *relayStaticCompletingYouTubeLiveClient) CompleteRelayStaticBroadcast(ctx context.Context, req ytlive.CompleteRequest) error {
	return f.fakeYouTubeLiveClient.Complete(ctx, req)
}
