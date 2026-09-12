package httpapi

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"time"
)

// streamStopLifecycleTimeout bounds the durable part of a stop after its
// stopping/failed claim succeeds. A Discord Bot may cancel the originating
// auto-stop HTTP request while processing its own downstream /stop call, so
// this phase must not inherit that requester cancellation.
const streamStopLifecycleTimeout = 30 * time.Second

// streamLifecycleLock serializes one stream's externally visible lifecycle
// dispatches. It intentionally does not serialize independent streams.
type streamLifecycleLock struct {
	mu   sync.Mutex
	refs int
}

// lockStreamLifecycle serializes start/stop dispatches for one stream without
// blocking lifecycle work for other streams. Persisted CAS transitions remain
// the cross-request authority; this lock keeps the check, claim, and remote
// dispatch together within this Control Panel process.
func (s *Server) lockStreamLifecycle(streamID string) func() {
	streamID = strings.TrimSpace(streamID)
	if streamID == "" {
		return func() {}
	}

	s.streamLifecycleMu.Lock()
	if s.streamLifecycleLocks == nil {
		s.streamLifecycleLocks = make(map[string]*streamLifecycleLock)
	}
	entry := s.streamLifecycleLocks[streamID]
	if entry == nil {
		entry = &streamLifecycleLock{}
		s.streamLifecycleLocks[streamID] = entry
	}
	entry.refs++
	s.streamLifecycleMu.Unlock()

	entry.mu.Lock()
	return func() {
		entry.mu.Unlock()
		s.streamLifecycleMu.Lock()
		entry.refs--
		if entry.refs == 0 {
			delete(s.streamLifecycleLocks, streamID)
		}
		s.streamLifecycleMu.Unlock()
	}
}

// detachedStopLifecycleRequest keeps the durable post-claim stop sequence
// bounded without allowing an upstream disconnect to cancel it. In
// particular, the Discord Bot cancels its own VC auto-stop request when it
// receives the Panel's downstream /stop dispatch; worker/encoder shutdown,
// terminal persistence, and waiting-stream rearm must still finish.
func detachedStopLifecycleRequest(r *http.Request) (*http.Request, context.CancelFunc) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), streamStopLifecycleTimeout)
	return r.Clone(ctx), cancel
}
