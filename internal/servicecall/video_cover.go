package servicecall

import (
	"context"

	"github.com/example/autostream-control-panel/internal/store"
)

// VideoCoverDispatcher is the Bundle 4 controller boundary. Bundle 5 may
// attach an Encoder transport implementation, but Control Panel semantics do
// not depend on an endpoint or payload before live_video_cover_v1 is present.
type VideoCoverDispatcher interface {
	DispatchVideoCover(context.Context, store.RegisteredService, VideoCoverDispatchRequest) VideoCoverDispatchResult
}

type VideoCoverDispatchRequest struct {
	StreamID       string
	JobGeneration  uint64
	Revision       uint64
	Active         bool
	AssetVariantID string
	IdempotencyKey string
}

type VideoCoverDispatchResult struct {
	Applied       bool
	Ambiguous     bool
	SafeErrorCode string
}
