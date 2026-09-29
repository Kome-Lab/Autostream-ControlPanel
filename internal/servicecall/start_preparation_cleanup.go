package servicecall

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/example/autostream-contracts/pkg/contracts"
	"github.com/example/autostream-control-panel/internal/store"
	"net/http"
	"time"
)

var errStartPreparationResponse = errors.New("start preparation response invalid")

func (p *StartPreparationControl) reconcile(c Client, ctx context.Context, encoder store.RegisteredService, base string) (contracts.EncoderStartPreparationStatus, bool) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	result, data := c.preparationJSON(ctx, encoder, http.MethodGet, base, nil, 5*time.Second)
	if !result.Success || result.StatusCode != 200 {
		p.EncoderPrepared = false
		return contracts.EncoderStartPreparationStatus{}, false
	}
	state, err := contracts.DecodeEncoderStartPreparationStatus(data, p.Identity)
	if err != nil {
		p.EncoderPrepared = false
		return state, false
	}
	p.EncoderPrepared = true
	p.phase("reconcile_" + state.Phase)
	return state, true
}
func (c Client) cleanupStartPreparation(ctx context.Context, p *StartPreparationControl, stream store.Stream, encoder, worker, bot store.RegisteredService, base string) bool {
	if p.CleanupAttempted || p.CleanupConfirmed {
		return p.CleanupConfirmed
	}
	p.CleanupAttempted = true
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if !p.claim(ctx) {
		p.phase("cleanup_owner_unknown")
		return false
	}
	confirmed := !p.WorkerUnknown
	if p.BotAttempted && !p.BotStarted {
		confirmed = false
	}
	if p.BotStarted {
		endpoint, payload, _ := stopPayload(stream, bot)
		r := c.post(ctx, bot, endpoint, payload)
		p.CleanupResults = append(p.CleanupResults, r)
		confirmed = confirmed && r.Success
	}
	if p.WorkerGeneration > 0 {
		if !p.claim(ctx) || !c.preparedWorkerStillOwned(ctx, worker, stream.ID, p.WorkerGeneration) {
			confirmed = false
		} else {
			endpoint, payload, _ := stopPayload(stream, worker)
			// Existing Worker stop accepts a stream ID, not a generation argument.
			r := c.post(ctx, worker, endpoint, payload)
			p.CleanupResults = append(p.CleanupResults, r)
			confirmed = confirmed && r.Success
		}
	}
	if !p.EncoderPrepared {
		confirmed = false
	} else if p.claim(ctx) {
		r, body := c.preparationJSON(ctx, encoder, http.MethodPost, base+"/abort", preparationAction(p.Identity), 5*time.Second)
		if r.Success {
			state, err := contracts.DecodeEncoderStartPreparationStatus(body, p.Identity)
			r.Success = err == nil && state.Phase == "aborted" && r.StatusCode == 200
		}
		p.CleanupResults = append(p.CleanupResults, r)
		confirmed = confirmed && r.Success
	} else {
		confirmed = false
	}
	p.CleanupConfirmed = confirmed
	p.phase("abort_end")
	return confirmed
}
func (c Client) preparedWorkerStillOwned(ctx context.Context, worker store.RegisteredService, streamID string, generation uint64) bool {
	// The existing status envelope is not the new preparation protocol and does
	// not promise no-store. Read only its bounded, non-secret ownership fields.
	token, err := c.authToken(worker)
	if err != nil || c.Config.URLPolicy.ValidateURL(worker.PublicURL) != nil {
		return false
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, joinURL(worker.PublicURL, "/status"), nil)
	if err != nil {
		return false
	}
	request.Header.Set("Authorization", "Bearer "+token)
	response, err := c.httpClient().Do(request)
	if err != nil {
		return false
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return false
	}
	var state struct {
		ServiceID   string `json:"service_id"`
		ServiceType string `json:"service_type"`
		Worker      struct {
			StreamID   string `json:"current_stream_id"`
			Generation uint64 `json:"job_generation"`
		} `json:"worker"`
	}
	if json.NewDecoder(http.MaxBytesReader(nil, response.Body, 1<<20)).Decode(&state) != nil {
		return false
	}
	return state.ServiceID == worker.ServiceID && state.ServiceType == "worker" && state.Worker.StreamID == streamID && state.Worker.Generation == generation
}
