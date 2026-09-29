package servicecall

import (
	"context"
	"encoding/json"
	"github.com/example/autostream-contracts/pkg/contracts"
	fixtures "github.com/example/autostream-contracts/tests"
	"github.com/example/autostream-control-panel/internal/ingesttoken"
	"github.com/example/autostream-control-panel/internal/store"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type preparationHarness struct {
	t                 *testing.T
	mode              string
	mu                sync.Mutex
	events            []string
	identity          contracts.EncoderStartPreparationIdentity
	prepared, running map[string]any
	workerPayload     map[string]any
	workerToken       string
	claim             atomic.Bool
	server            *httptest.Server
	client            Client
	services          []store.RegisteredService
	req               StartRequest
	control           *StartPreparationControl
}

func newPreparationHarness(t *testing.T, mode string) *preparationHarness {
	t.Helper()
	var corpus map[string]json.RawMessage
	if err := json.Unmarshal([]byte(fixtures.StartPreparationJSON), &corpus); err != nil {
		t.Fatal(err)
	}
	h := &preparationHarness{t: t, mode: mode}
	_ = json.Unmarshal(corpus["prepared"], &h.prepared)
	_ = json.Unmarshal(corpus["running"], &h.running)
	_ = json.Unmarshal(corpus["prepared"], &struct {
		Identity *contracts.EncoderStartPreparationIdentity `json:"identity"`
	}{&h.identity})
	h.claim.Store(true)
	h.control = &StartPreparationControl{StartID: h.identity.StartID, CheckClaim: func(context.Context) bool { return h.claim.Load() }}
	h.server = httptest.NewServer(http.HandlerFunc(h.handle))
	t.Cleanup(h.server.Close)
	h.client = testClient()
	h.client.Config.IngestTokenSigningKey = "synthetic-signing-key"
	h.services = []store.RegisteredService{
		{ServiceID: "encoder-01", ServiceType: "encoder_recorder", PublicURL: h.server.URL, ReportedCapabilities: map[string]any{"worker_frame_ingest_mjpeg_srt": true, CapabilityStreamStartPrepareCommitV2: true, CapabilityLiveVideoCoverV1: true}},
		{ServiceID: "worker-01", ServiceType: "worker", PublicURL: h.server.URL, ReportedCapabilities: map[string]any{"scene_frames_mjpeg_srt": true}},
		{ServiceID: "bot-01", ServiceType: "discord_bot", PublicURL: h.server.URL, ReportedCapabilities: map[string]any{CapabilityDiscordResolvedTargetV2: true}},
	}
	h.req = StartRequest{StartPreparation: h.control, ArchiveRunID: "archive-01", ArchiveStartedAt: time.Now().UTC(), EncoderProfileID: "profile-01", EncoderVideoWidth: 854, EncoderVideoHeight: 480, EncoderVideoFPS: 15, DiscordTargetRevision: 1, DiscordGuildID: "1001", DiscordVoiceChannelID: "1002", DiscordTextChannelID: "1003", VideoCoverStart: &VideoCoverStartSnapshot{JobGeneration: 7, Revision: 1, Active: false, IdempotencyKey: "initial-7"}}
	return h
}
func (h *preparationHarness) status(phase string) map[string]any {
	if phase == "running" {
		return h.running
	}
	return map[string]any{"schema_version": 2, "identity": h.identity, "phase": phase, "expires_at": time.Now().Add(30 * time.Second).UTC()}
}
func (h *preparationHarness) handle(w http.ResponseWriter, r *http.Request) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if r.Header.Get("Authorization") != "Bearer service-token" {
		h.t.Error("missing service authentication")
		w.WriteHeader(401)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	b, _ := io.ReadAll(r.Body)
	var payload map[string]any
	if len(b) > 0 {
		_ = json.Unmarshal(b, &payload)
	}
	emit := func(code int, value any) { w.WriteHeader(code); _ = json.NewEncoder(w).Encode(value) }
	lose := func() {
		conn, _, err := w.(http.Hijacker).Hijack()
		if err != nil {
			h.t.Error(err)
			return
		}
		_ = conn.Close()
	}
	switch {
	case r.URL.Path == "/streams/start-preparations":
		h.events = append(h.events, "prepare")
		decoded, err := contracts.DecodeEncoderStartPreparation(b)
		if err != nil {
			h.t.Error("CP prepare not canonical:", err)
			emit(400, map[string]string{"code": "bad_fixture"})
			return
		}
		h.workerToken = decoded.StartRequest.WorkerVideoIngestToken
		if _, err := ingesttoken.Verify("synthetic-signing-key", h.workerToken, ingesttoken.Expected{StreamID: "stream-01", ServiceID: "worker-01", ServiceType: "worker", Purpose: "worker_video", Audience: "encoder_recorder"}); err != nil {
			h.t.Error(err)
		}
		h.prepared["expires_at"] = time.Now().Add(30 * time.Second).UTC()
		switch h.mode {
		case "prepare_loss":
			lose()
			return
		case "prepare_invalid":
			h.prepared["phase"] = "running"
		case "route_secret":
			h.prepared["video_ingest"].(map[string]any)["url"] = "srt://encoder.example.com:19000?passphrase=private"
		case "no_store":
			w.Header().Del("Cache-Control")
		}
		emit(202, h.prepared)
	case r.Method == "GET" && strings.Contains(r.URL.Path, "/start-preparations/"):
		h.events = append(h.events, "get")
		if h.mode == "status_unknown" {
			emit(404, map[string]string{"code": "start_preparation_unknown"})
			return
		}
		if h.mode == "commit_loss" {
			emit(200, h.status("running"))
		} else {
			emit(200, h.status("prepared"))
		}
	case strings.HasSuffix(r.URL.Path, "/commit"):
		h.events = append(h.events, "commit")
		if h.control.WorkerGeneration != 17 {
			h.t.Error("commit preceded positive Worker generation")
		}
		if _, err := contracts.DecodeEncoderStartPreparationAction(b, "stream-01", h.identity.StartID); err != nil {
			h.t.Error(err)
		}
		switch h.mode {
		case "commit_loss":
			lose()
			return
		case "commit_failure", "status_unknown", "worker_reassigned":
			emit(502, map[string]string{"code": "start_preparation_runtime_failed"})
			return
		case "witness_invalid":
			delete(h.running["cover_state"].(map[string]any), "applied_witness")
		}
		emit(200, h.running)
	case strings.HasSuffix(r.URL.Path, "/abort"):
		h.events = append(h.events, "abort")
		emit(200, h.status("aborted"))
	case r.URL.Path == "/status":
		h.events = append(h.events, "worker_status")
		generation := 17
		if h.mode == "worker_reassigned" {
			generation = 18
		}
		emit(200, map[string]any{"service_id": "worker-01", "service_type": "worker", "worker": map[string]any{"current_stream_id": "stream-01", "job_generation": generation}})
	case strings.HasSuffix(r.URL.Path, "/stop"):
		h.events = append(h.events, "worker_stop")
		emit(200, map[string]string{"status": "stopped"})
	case payload["video_ingest_url"] != nil:
		h.events = append(h.events, "worker")
		h.workerPayload = payload
		if h.mode == "worker_reject" {
			emit(400, map[string]string{"code": "video_ingest_unavailable"})
			return
		}
		if h.mode == "worker_unknown" {
			emit(503, map[string]string{"code": "unavailable"})
			return
		}
		if h.mode == "claim_lost" {
			h.claim.Store(false)
		}
		emit(202, map[string]any{"job_generation": 17})
	case payload["discord_target"] != nil:
		h.events = append(h.events, "bot")
		if !h.control.EncoderCommitted {
			h.t.Error("Bot before actual commit")
		}
		if h.mode == "bot_failure" {
			emit(503, map[string]string{"code": "unavailable"})
			return
		}
		emit(202, map[string]string{"status": "running"})
	default:
		h.t.Error("unexpected request", r.URL.Path)
		emit(404, map[string]string{"code": "not_found"})
	}
}
func (h *preparationHarness) start() []DispatchResult {
	return h.client.Start(h.t.Context(), store.Stream{ID: "stream-01", Name: "Synthetic"}, h.services, h.req)
}
func (h *preparationHarness) assertSecretSafe(results []DispatchResult) {
	h.t.Helper()
	b, _ := json.Marshal(results)
	for _, secret := range []string{"synthetic_worker_video_passphrase_32bytes", h.workerToken} {
		if secret != "" && strings.Contains(string(b), secret) {
			h.t.Fatal("private route credential leaked")
		}
	}
}
func TestStartPreparationNormalOrderAndCredentialBoundary(t *testing.T) {
	h := newPreparationHarness(t, "")
	results := h.start()
	h.assertSecretSafe(results)
	if hasFailedDispatchResult(results) || len(results) != 3 || !h.control.EncoderCommitted || !h.control.BotStarted {
		t.Fatalf("start failed: %+v", results)
	}
	if !reflect.DeepEqual(h.events, []string{"prepare", "worker", "commit", "bot"}) {
		t.Fatal(h.events)
	}
	if h.control.Identity.JobGeneration != 7 || h.control.WorkerGeneration != 17 {
		t.Fatal("visual and Worker generation confused")
	}
	if h.workerPayload["video_width"] != float64(854) || h.workerPayload["video_fps"] != float64(15) || h.workerPayload["video_ingest_passphrase"] != "synthetic_worker_video_passphrase_32bytes" {
		t.Fatal("profile/route not retained")
	}
}
func TestStartPreparationFailureAndLossDoNotRetryPosts(t *testing.T) {
	for _, mode := range []string{"prepare_loss", "prepare_invalid", "route_secret", "no_store", "worker_reject", "worker_unknown", "claim_lost", "commit_loss", "commit_failure", "status_unknown", "witness_invalid", "worker_reassigned", "bot_failure"} {
		t.Run(mode, func(t *testing.T) {
			h := newPreparationHarness(t, mode)
			results := h.start()
			h.assertSecretSafe(results)
			counts := map[string]int{}
			for _, e := range h.events {
				counts[e]++
			}
			for _, e := range []string{"prepare", "worker", "commit", "bot", "abort", "worker_stop"} {
				if counts[e] > 1 {
					t.Fatalf("blind retry %s: %v", e, h.events)
				}
			}
			if mode == "commit_loss" {
				if hasFailedDispatchResult(results) || !h.control.EncoderCommitted || counts["get"] != 1 || counts["bot"] != 1 {
					t.Fatal(results, h.events)
				}
				return
			}
			if !hasFailedDispatchResult(results) || counts["bot"] > 0 && mode != "bot_failure" {
				t.Fatal("failed stage advanced", results, h.events)
			}
			if mode == "claim_lost" || mode == "worker_unknown" || mode == "worker_reassigned" || mode == "bot_failure" || mode == "status_unknown" {
				if h.control.CleanupConfirmed {
					t.Fatal("unknown ownership was released")
				}
			}
			if mode == "claim_lost" && (counts["abort"] > 0 || counts["worker_stop"] > 0) {
				t.Fatal("destructive compensation without claim")
			}
			if mode == "worker_reassigned" && counts["worker_stop"] > 0 {
				t.Fatal("stopped newer Worker generation")
			}
			if mode == "prepare_loss" && (counts["get"] != 1 || counts["worker"] != 0 || counts["abort"] != 1) {
				t.Fatal("prepare loss reissued secret or advanced", h.events)
			}
		})
	}
}
func TestStartPreparationActualCapabilityRequiredBeforeDispatch(t *testing.T) {
	for _, value := range []any{nil, false, "true"} {
		h := newPreparationHarness(t, "")
		h.services[0].ReportedCapabilities[CapabilityStreamStartPrepareCommitV2] = value
		h.services[0].Capabilities = map[string]any{CapabilityStreamStartPrepareCommitV2: true}
		results := h.start()
		if len(h.events) != 0 || len(results) != 1 || results[0].Code != "start_preparation_capability_unavailable" {
			t.Fatal(results, h.events)
		}
		if !hasIssueCode(h.client.StartReadinessIssues(h.services, h.req, time.Now()), "start_preparation_capability_unavailable") {
			t.Fatal("readiness accepted fabricated operator capability")
		}
	}
}
