package httpapi

import (
	"context"
	"github.com/example/autostream-control-panel/internal/servicecall"
	"github.com/example/autostream-control-panel/internal/store"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

type blockingStartDispatcher struct {
	fakeServiceDispatcher
	startEntered chan struct{}
	releaseStart chan struct{}
	stopEntered  chan struct{}
}

// gatedLifecycleTransitionStore lets separate Server instances reach the same
// conditional transition together. It verifies the persisted CAS claim rather
// than relying on one Server's in-process stream lock.
type gatedLifecycleTransitionStore struct {
	store.StreamStore
	startClaims store.StreamStartClaimStore
	expected    string
	status      string
	entered     chan struct{}
	release     chan struct{}
}

func (s *gatedLifecycleTransitionStore) ClaimStreamStart(ctx context.Context, request store.StreamStartClaimRequest) (store.ClaimedStreamStart, error) {
	if s.startClaims == nil {
		return store.ClaimedStreamStart{}, store.ErrServiceAssignmentGuardUnavailable
	}
	if strings.EqualFold(strings.TrimSpace(request.ExpectedStatus), s.expected) && strings.EqualFold(s.status, "starting") {
		select {
		case s.entered <- struct{}{}:
		case <-ctx.Done():
			return store.ClaimedStreamStart{}, ctx.Err()
		}
		select {
		case <-s.release:
		case <-ctx.Done():
			return store.ClaimedStreamStart{}, ctx.Err()
		}
	}
	return s.startClaims.ClaimStreamStart(ctx, request)
}

func (s *gatedLifecycleTransitionStore) TransitionClaimedStreamStart(ctx context.Context, claim store.StreamStartOwnershipClaim, status string) (store.Stream, bool, error) {
	if s.startClaims == nil {
		return store.Stream{}, false, store.ErrServiceAssignmentGuardUnavailable
	}
	return s.startClaims.TransitionClaimedStreamStart(ctx, claim, status)
}

func (s *gatedLifecycleTransitionStore) TransitionStreamStatus(ctx context.Context, id, expectedStatus, status string) (store.Stream, bool, error) {
	if strings.EqualFold(strings.TrimSpace(expectedStatus), s.expected) && strings.EqualFold(strings.TrimSpace(status), s.status) {
		select {
		case s.entered <- struct{}{}:
		case <-ctx.Done():
			return store.Stream{}, false, ctx.Err()
		}
		select {
		case <-s.release:
		case <-ctx.Done():
			return store.Stream{}, false, ctx.Err()
		}
	}
	return s.StreamStore.TransitionStreamStatus(ctx, id, expectedStatus, status)
}

type synchronizedServiceDispatcher struct {
	fakeServiceDispatcher
	mu sync.Mutex
}

// cancelingRequestStopDispatcher simulates the Discord Bot's self-stop: the
// Bot cancels the outbound auto-stop request while processing the Panel's
// downstream stop dispatch. The Panel must continue the durable lifecycle on
// a detached, bounded context.
type cancelingRequestStopDispatcher struct {
	fakeServiceDispatcher
	cancelRequest        context.CancelFunc
	stopContextCancelled bool
}

func (f *cancelingRequestStopDispatcher) Stop(ctx context.Context, stream store.Stream, services []store.RegisteredService) []servicecall.DispatchResult {
	if f.cancelRequest != nil {
		f.cancelRequest()
	}
	f.stopContextCancelled = ctx.Err() != nil
	return f.fakeServiceDispatcher.Stop(ctx, stream, services)
}

func (f *synchronizedServiceDispatcher) Start(ctx context.Context, stream store.Stream, services []store.RegisteredService, req servicecall.StartRequest) []servicecall.DispatchResult {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.fakeServiceDispatcher.Start(ctx, stream, services, req)
}

func (f *synchronizedServiceDispatcher) Stop(ctx context.Context, stream store.Stream, services []store.RegisteredService) []servicecall.DispatchResult {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.fakeServiceDispatcher.Stop(ctx, stream, services)
}

func (f *synchronizedServiceDispatcher) StartCalls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.startCalls
}

func (f *synchronizedServiceDispatcher) StopCalls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.stopCalls
}

func waitForLifecycleTransitionAttempts(t *testing.T, entered <-chan struct{}, want int) {
	t.Helper()
	for range want {
		select {
		case <-entered:
		case <-time.After(time.Second):
			t.Fatalf("only part of the concurrent lifecycle transition reached the CAS gate")
		}
	}
}

func receiveLifecycleResponseCodes(t *testing.T, responses <-chan *httptest.ResponseRecorder, want int) map[int]int {
	t.Helper()
	codes := make(map[int]int, want)
	for range want {
		select {
		case response := <-responses:
			codes[response.Code]++
		case <-time.After(time.Second):
			t.Fatalf("concurrent lifecycle request did not complete")
		}
	}
	return codes
}
