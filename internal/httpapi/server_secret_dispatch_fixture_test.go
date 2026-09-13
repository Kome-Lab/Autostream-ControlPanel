package httpapi

import (
	"context"
	"github.com/example/autostream-control-panel/internal/servicecall"
	"github.com/example/autostream-control-panel/internal/store"
	"net/http"
	"time"
)

type activeRuntimeSecretLeaseStore struct{}

func (activeRuntimeSecretLeaseStore) ClaimRuntimeSecretLease(ctx context.Context, lease store.RuntimeSecretLease, ttl time.Duration) (store.RuntimeSecretLease, error) {
	return store.RuntimeSecretLease{}, store.ErrRuntimeSecretLeaseActive
}

func (activeRuntimeSecretLeaseStore) ReleaseRuntimeSecretLease(ctx context.Context, lease store.RuntimeSecretLease) error {
	return nil
}

type trackingSecretStore struct {
	getCalls int
	statuses []store.SecretStatus
}

func (s *trackingSecretStore) ListSecretStatus(ctx context.Context) ([]store.SecretStatus, error) {
	return append([]store.SecretStatus(nil), s.statuses...), nil
}

func (s *trackingSecretStore) UpdateSecret(ctx context.Context, name, value string) (store.SecretStatus, error) {
	return store.SecretStatus{Name: name, Configured: value != ""}, nil
}

func (s *trackingSecretStore) GetSecretValue(ctx context.Context, name string) (string, error) {
	s.getCalls++
	return "Bot <RAW_DISCORD_TOKEN>", nil
}

type fakeServiceDispatcher struct {
	startCalls               int
	stopCalls                int
	retryCalls               int
	audioStatusCalls         int
	workerEventsCalls        int
	encoderPreflightCalls    int
	workerEventSendCalls     int
	archiveDownloadCalls     int
	archiveDeleteCalls       int
	archiveRenameCalls       int
	startedStream            store.Stream
	stoppedStream            store.Stream
	retriedStream            store.Stream
	audioStatusStream        store.Stream
	workerEventsStream       store.Stream
	encoderPreflightStream   store.Stream
	workerEventStream        store.Stream
	archiveStream            store.Stream
	archiveArtifact          store.StreamArtifact
	archiveByteRange         string
	archiveRenameName        string
	startRequest             servicecall.StartRequest
	retriedArchiveConfig     map[string]any
	startedServices          []store.RegisteredService
	stoppedServices          []store.RegisteredService
	retriedServices          []store.RegisteredService
	audioStatusServices      []store.RegisteredService
	workerEventsServices     []store.RegisteredService
	encoderPreflightServices []store.RegisteredService
	workerEventServices      []store.RegisteredService
	audioStatus              servicecall.AudioStatusResult
	workerEvents             servicecall.WorkerEventsResult
	encoderPreflight         servicecall.ServicePreflightResult
	workerEventRequest       servicecall.WorkerEventRequest
	startResultsOverride     []servicecall.DispatchResult
	stopResultsOverride      []servicecall.DispatchResult
	failStart                bool
	failStop                 bool
	failRetry                bool
	failAudioStatus          bool
	failWorkerEvents         bool
	failEncoderPreflight     bool
	failWorkerEventSend      bool
	failArchiveAction        bool
	dispatchFailureError     string
}

type captionRuntimeServiceDispatcher struct {
	fakeServiceDispatcher
	captionRuntimeCalls     int
	captionRuntimeStream    store.Stream
	captionRuntimeServices  []store.RegisteredService
	captionRuntimeProfileID string
	captionRuntimeResult    servicecall.DispatchResult
}

func (f *captionRuntimeServiceDispatcher) UpdateWorkerCaptionRuntimeSettings(_ context.Context, stream store.Stream, services []store.RegisteredService, captionProfileID string) servicecall.DispatchResult {
	f.captionRuntimeCalls++
	f.captionRuntimeStream = stream
	f.captionRuntimeServices = append([]store.RegisteredService(nil), services...)
	f.captionRuntimeProfileID = captionProfileID
	if f.captionRuntimeResult.ServiceType != "" || f.captionRuntimeResult.Code != "" || f.captionRuntimeResult.Success {
		return f.captionRuntimeResult
	}
	return servicecall.DispatchResult{ServiceID: "worker-caption-01", ServiceType: "worker", Endpoint: "/jobs/" + stream.ID + "/caption-runtime-settings", StatusCode: http.StatusOK, Success: true}
}
