package servicecall

import (
	"context"
	"github.com/example/autostream-contracts/pkg/contracts"
	"github.com/example/autostream-control-panel/internal/store"
	"github.com/google/uuid"
	"log"
	"net/http"
	"net/url"
	"time"
)

// One CP start owner supplies a live claim fence; it is never an HTTP input.
type StartPreparationControl struct {
	OwnershipClaim   *store.StreamStartOwnershipClaim
	StartID          string
	CheckClaim       func(context.Context) bool
	Identity         contracts.EncoderStartPreparationIdentity
	EncoderPrepared  bool
	EncoderCommitted bool
	WorkerAttempted  bool
	WorkerGeneration uint64
	WorkerUnknown    bool
	BotAttempted     bool
	BotStarted       bool
	CleanupAttempted bool
	CleanupConfirmed bool
	CleanupResults   []DispatchResult
	Cleanup          func(context.Context) bool
}

func (p *StartPreparationControl) phase(event string) {
	log.Printf("cp start: event=%s stream_id=%s start_id=%s job_generation=%d worker_generation=%d", event, p.Identity.StreamID, p.StartID, p.Identity.JobGeneration, p.WorkerGeneration)
}
func (p *StartPreparationControl) claim(ctx context.Context) bool {
	return p.CheckClaim != nil && p.CheckClaim(ctx)
}
func (c Client) startPrepared(ctx context.Context, stream store.Stream, services []store.RegisteredService, req StartRequest) (results []DispatchResult) {
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	p := req.StartPreparation
	if p == nil {
		p = &StartPreparationControl{}
	}
	encoder, worker, bot := firstService(services, "encoder_recorder"), firstService(services, "worker"), firstService(services, "discord_bot")
	failed := func(service store.RegisteredService, code string) []DispatchResult {
		return append(results, DispatchResult{ServiceID: service.ServiceID, ServiceType: service.ServiceType, Code: code, Error: code, FailurePhase: "pre_dispatch"})
	}
	if p.StartID == "" {
		p.StartID = uuid.NewString()
	}
	if req.VideoCoverStart == nil || req.VideoCoverStart.JobGeneration == 0 || req.ArchiveRunID == "" || req.ArchiveStartedAt.IsZero() {
		return failed(encoder, "start_preparation_identity_unavailable")
	}
	p.Identity = contracts.EncoderStartPreparationIdentity{StreamID: stream.ID, StartID: p.StartID, EncoderServiceID: encoder.ServiceID, JobGeneration: req.VideoCoverStart.JobGeneration, ArchiveRunID: req.ArchiveRunID}
	if !p.claim(ctx) {
		return failed(encoder, "start_preparation_claim_unknown")
	}
	base := "/streams/" + url.PathEscape(stream.ID) + "/start-preparations/" + p.StartID
	p.Cleanup = func(cleanupCtx context.Context) bool {
		return c.cleanupStartPreparation(cleanupCtx, p, stream, encoder, worker, bot, base)
	}
	completed := false
	defer func() {
		if !completed {
			cleanupCtx, stop := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			defer stop()
			p.Cleanup(cleanupCtx)
		}
	}()
	now := time.Now().UTC()
	token := c.issueIngestTokenForAudience(stream.ID, worker, "worker_video", "encoder_recorder", now)
	if token == "" {
		p.CleanupConfirmed = true
		return failed(worker, "worker_video_ingest_token_unavailable")
	}
	_, payload, _ := c.startPayload(stream, encoder, req, encoder.PublicURL, worker, now)
	nested := payload.(map[string]any)
	delete(nested, "input_url")
	delete(nested, "input_mode")
	nested["worker_video_ingest"] = true
	nested["worker_video_ingest_token"] = token
	p.phase("prepare_begin")
	prepare, body := c.preparationJSON(ctx, encoder, http.MethodPost, "/streams/start-preparations", map[string]any{"schema_version": 2, "start_id": p.StartID, "encoder_service_id": encoder.ServiceID, "start_request": nested}, 15*time.Second)
	if !prepare.Success {
		if prepare.FailurePhase != "pre_dispatch" {
			p.reconcile(c, ctx, encoder, base)
		} else {
			p.CleanupConfirmed = true
		}
		return append(results, prepare)
	}
	prepared, err := contracts.DecodeEncoderStartPreparationPrepared(body, p.Identity)
	if prepare.StatusCode != 202 || err != nil || !prepared.ExpiresAt.After(time.Now()) || prepared.ExpiresAt.After(time.Now().Add(35*time.Second)) {
		prepare.Success = false
		prepare.Code = "start_preparation_response_invalid"
		prepare.FailurePhase = "protocol"
		prepare.Error = prepare.Code
		p.reconcile(c, ctx, encoder, base)
		return append(results, prepare)
	}
	p.EncoderPrepared = true
	p.phase("prepare_response")
	route := workerVideoIngestRoute{URL: prepared.VideoIngest.URL, Passphrase: prepared.VideoIngest.Passphrase, PBKeyLen: prepared.VideoIngest.PBKeyLen}
	if validateWorkerVideoIngestRoute(route) != nil {
		return failed(encoder, "worker_video_ingest_response_invalid")
	}
	if !p.claim(ctx) {
		return failed(worker, "start_preparation_claim_unknown")
	}
	endpoint, payload, _ := c.startPayload(stream, worker, req, encoder.PublicURL, worker, now)
	values := payload.(map[string]any)
	values["video_ingest_url"] = route.URL
	values["video_ingest_passphrase"] = route.secret()
	values["video_ingest_pbkeylen"] = route.PBKeyLen
	values["encoder_profile_id"] = req.EncoderProfileID
	values["video_width"] = req.EncoderVideoWidth
	values["video_height"] = req.EncoderVideoHeight
	values["video_fps"] = req.EncoderVideoFPS
	p.phase("worker_start_request")
	p.WorkerAttempted = true
	wr := c.post(ctx, worker, endpoint, values)
	if !wr.Success || wr.JobGeneration == 0 {
		p.WorkerUnknown = wr.FailurePhase == "transport" || wr.FailurePhase == "protocol" || wr.StatusCode >= 500 || wr.StatusCode == 409 || wr.Success
		wr.Success = false
		if wr.Code == "" {
			wr.Code = "worker_job_generation_response_invalid"
		}
		wr.Error = wr.Code
		return append(results, wr)
	}
	p.WorkerGeneration = wr.JobGeneration
	req.WorkerJobGeneration = wr.JobGeneration
	wr.VideoOverlayBurnInNegotiated = true
	results = append(results, wr)
	p.phase("worker_generation")
	if !p.claim(ctx) {
		return failed(encoder, "start_preparation_claim_unknown")
	}
	p.phase("commit_begin")
	commit, body := c.preparationJSON(ctx, encoder, http.MethodPost, base+"/commit", preparationAction(p.Identity), 10*time.Second)
	var committed contracts.EncoderStartPreparationStatus
	if commit.Success && commit.StatusCode == 200 {
		committed, err = contracts.DecodeEncoderStartPreparationStatus(body, p.Identity)
	} else {
		err = errStartPreparationResponse
	}
	if err != nil || committed.Phase != "running" {
		reconciled, known := p.reconcile(c, ctx, encoder, base)
		if !known || reconciled.Phase != "running" {
			commit.Success = false
			commit.Code = "start_preparation_commit_unknown"
			commit.Error = commit.Code
			return append(results, commit)
		}
		committed = reconciled
	}
	if committed.CoverState == nil || committed.CoverState.Desired.Revision != req.VideoCoverStart.Revision || committed.CoverState.Desired.Active != req.VideoCoverStart.Active {
		return failed(encoder, "start_preparation_witness_mismatch")
	}
	if req.VideoCoverStart.CoverAsset != nil && (committed.CoverState.CoverAsset == nil || committed.CoverState.CoverAsset.AssetID != req.VideoCoverStart.CoverAsset.AssetID || committed.CoverState.CoverAsset.VariantID != req.VideoCoverStart.CoverAsset.VariantID || committed.CoverState.CoverAsset.SHA256 != req.VideoCoverStart.CoverAsset.SHA256) {
		return failed(encoder, "start_preparation_witness_mismatch")
	}
	p.EncoderCommitted = true
	p.phase("commit_success")
	commit.Success = true
	commit.StatusCode = 200
	commit.Error = ""
	commit.Code = ""
	commit.FailurePhase = ""
	results = append([]DispatchResult{commit}, results...)
	if !p.claim(ctx) {
		return failed(bot, "start_preparation_claim_unknown")
	}
	if bot.ServiceID != "" {
		endpoint, payload, _ = c.startPayload(stream, bot, req, encoder.PublicURL, worker, now)
		p.phase("bot_start")
		p.BotAttempted = true
		br := c.post(ctx, bot, endpoint, payload)
		results = append(results, br)
		if !br.Success {
			return results
		}
		p.BotStarted = true
	}
	completed = true
	return results
}
