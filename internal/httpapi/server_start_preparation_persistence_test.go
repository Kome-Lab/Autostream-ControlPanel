package httpapi

import (
	"context"
	"github.com/example/autostream-control-panel/internal/servicecall"
	"github.com/example/autostream-control-panel/internal/store"
)

// This HTTP-owner fixture supplies the already-validated dispatcher outcome.
// Wire order and media witnesses are covered by the real servicecall and Encoder tests.
type preparedStartPersistenceDispatcher struct{ fakeServiceDispatcher }

func (f *preparedStartPersistenceDispatcher) Start(ctx context.Context, stream store.Stream, services []store.RegisteredService, req servicecall.StartRequest) []servicecall.DispatchResult {
	results := f.fakeServiceDispatcher.Start(ctx, stream, services, req)
	if p := req.StartPreparation; p != nil && p.CheckClaim(ctx) {
		p.EncoderPrepared = true
		p.EncoderCommitted = true
		p.WorkerGeneration = 1
		p.BotStarted = true
	}
	return results
}
