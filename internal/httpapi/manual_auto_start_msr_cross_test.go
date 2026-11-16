//go:build manual_stop_integration

package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	lifecycle "github.com/Kome-Lab/Autostream-Contracts/v3lifecycle"
	"github.com/example/autostream-control-panel/internal/manualhold"
	"github.com/example/autostream-control-panel/internal/store"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"
)

type msrExternalProcess struct {
	command    *exec.Cmd
	ready, url string
	done       chan error
	output     bytes.Buffer
	stopOnce   sync.Once
}

func msrExternal(t *testing.T, binary, entry string, env []string, ready string) *msrExternalProcess {
	t.Helper()
	if binary == "" {
		if os.Getenv("AUTOSTREAM_MSR_REQUIRED") == "1" {
			t.Fatal("required cross-repository process binary is missing")
		}
		t.Skip("cross-repository process binary not configured")
	}
	p := &msrExternalProcess{ready: ready, done: make(chan error, 1)}
	p.command = exec.Command(binary, "-test.run=^"+entry+"$", "-test.count=1")
	p.command.Env = append(os.Environ(), env...)
	p.command.Stdout = &p.output
	p.command.Stderr = &p.output
	if e := p.command.Start(); e != nil {
		t.Fatal("start isolated test process:", e)
	}
	go func() { p.done <- p.command.Wait() }()
	t.Cleanup(func() { p.stop(t) })
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	if e := msrWaitFile(ctx, ready); e != nil {
		t.Fatal("isolated process readiness not observed")
	}
	var e error
	p.url, e = msrReadProcessURL(ctx, ready)
	if e != nil {
		t.Fatal("isolated process did not publish a complete loopback URL")
	}

	return p
}
func msrDriverJSON(t *testing.T, base, method, endpoint string, body any, out any) {
	t.Helper()
	raw, e := json.Marshal(body)
	if e != nil {
		t.Fatal(e)
	}
	req, e := http.NewRequestWithContext(t.Context(), method, base+endpoint, bytes.NewReader(raw))
	if e != nil {
		t.Fatal(e)
	}
	req.Header.Set("Authorization", "Bearer synthetic-msr-helper-control")
	req.Header.Set("Content-Type", "application/json")
	response, e := (&http.Client{Timeout: 5 * time.Second}).Do(req)
	if e != nil {
		t.Fatal(e)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		t.Fatalf("synthetic process driver status=%d", response.StatusCode)
	}
	if out != nil {
		if e = json.NewDecoder(response.Body).Decode(out); e != nil {
			t.Fatal(e)
		}
	} else {
		_, _ = io.Copy(io.Discard, response.Body)
	}
}
func msrBotSnapshot(f *msrHTTPFixture, known, held bool, revision uint64) map[string]any {
	s := f.stream
	h := map[string]any{"stream_id": s.ID, "service_id": "msr-bot", "service_type": "discord_bot", "selection_epoch": time.Unix(100, 0).UTC().Format(time.RFC3339Nano), "profile_id": "msr-discord-profile", "profile_revision": "synthetic-profile-revision", "profile_owner_service_id": "msr-bot", "secret_name": "synthetic_reference_only", "fingerprint": "synthetic_reference_only", "ready": true, "guild_id": "100000000000000001", "voice_channel_id": "100000000000000002", "target_revision": uint64(7), "auto_start_trigger": "discord_voice_join", "auto_start_hold_required": true, "auto_start_hold_known": known, "auto_start_hold": held, "auto_start_hold_revision": revision, "expected_stream_revision": s.UpdatedAt.UTC().Format(time.RFC3339Nano), "expected_settings_revision": s.SettingsRevision, "expected_run_id": s.ArchiveRunID}
	return map[string]any{"service": map[string]any{"service_id": "msr-bot", "service_type": "discord_bot"}, "assignments": []any{}, "profiles": map[string]any{}, "current_discord_bindings": map[string]any{"version": 1, "bindings": []any{}}, "discord_waiting_monitor": map[string]any{"version": 1, "execution_active": false, "bindings": []any{h}}}
}
func msrRunBotProcess(t *testing.T, f *msrHTTPFixture, mode string) {
	t.Helper()
	before := f.hold(t)
	f.stream = f.source(t)
	run := func() {
		directory := t.TempDir()
		ready := filepath.Join(directory, "bot-ready.json")
		p := msrExternal(t, os.Getenv("AUTOSTREAM_MSR_BOT_TEST_BINARY"), "TestMSRBotProcessHelper", []string{"AUTOSTREAM_MSR_BOT_HELPER=1", "AUTOSTREAM_MSR_BOT_CP_URL=" + f.server.URL, "AUTOSTREAM_MSR_BOT_SERVICE_ID=msr-bot", "AUTOSTREAM_MSR_BOT_READY=" + ready}, ready)
		msrDriverJSON(t, p.url, http.MethodPost, "/snapshot", msrBotSnapshot(f, before.Known, before.Held, before.Revision), nil)
		if mode == "reconnect" {
			msrDriverJSON(t, p.url, http.MethodPost, "/reconnect", nil, nil)
		}
		msrDriverJSON(t, p.url, http.MethodPost, "/join", map[string]any{"stream_id": f.stream.ID, "guild_id": "100000000000000001", "voice_channel_id": "100000000000000002", "user_id": "synthetic-participant", "assignment_epoch": "1970-01-01T00:01:40Z/7/synthetic-profile-revision", "existing_presence": true}, nil)
		var state struct {
			ManagedCalls   int `json:"managed_calls"`
			LegacyCalls    int `json:"legacy_calls"`
			HTTPDispatches int `json:"http_dispatches"`
		}
		msrDriverJSON(t, p.url, http.MethodGet, "/state?stream_id="+f.stream.ID, nil, &state)
		if state.ManagedCalls != 0 || state.LegacyCalls != 0 || state.HTTPDispatches != 0 {
			t.Fatalf("Bot restarted/reconnected through durable hold: %+v", state)
		}
		msrDriverJSON(t, p.url, http.MethodPost, "/shutdown", nil, nil)
	}
	run()
	if mode == "restart" {
		run()
	}
	if f.hold(t) != before {
		t.Fatal("Bot lifecycle changed CP-owned hold")
	}
}

func (p *msrExternalProcess) stop(t *testing.T) {
	t.Helper()
	p.stopOnce.Do(func() {
		_ = p.command.Process.Kill()
		select {
		case <-p.done:
		case <-time.After(5 * time.Second):
			t.Error("isolated process did not stop")
		}
	})
}

type msrRemoteTarget struct {
	URL, Token, CountFile, Directory, Binary, ServiceID, Kind string
	Producer                                                  lifecycle.ProducerRef
	Process                                                   *msrExternalProcess
}

func (r msrRemoteTarget) Close(ctx context.Context, record lifecycle.Record) (lifecycle.Record, error) {
	resource, e := manualhold.Resource(record)
	if e != nil {
		return lifecycle.Record{}, e
	}
	body, _ := json.Marshal(record)
	request, e := http.NewRequestWithContext(ctx, http.MethodPost, r.URL+"/streams/"+string(resource.ResourceID)+"/start-admission/close", bytes.NewReader(body))
	if e != nil {
		return lifecycle.Record{}, e
	}
	request.Header.Set("Authorization", "Bearer "+r.Token)
	request.Header.Set("Content-Type", "application/json")
	response, e := (&http.Client{Timeout: 10 * time.Second}).Do(request)
	if e != nil {
		return lifecycle.Record{}, manualhold.ErrUnknown
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return lifecycle.Record{}, manualhold.ErrUnknown
	}
	var acknowledgement lifecycle.Record
	if json.NewDecoder(response.Body).Decode(&acknowledgement) != nil {
		return lifecycle.Record{}, manualhold.ErrUnknown
	}
	return acknowledgement, nil
}
func msrTarget(t *testing.T, f *msrHTTPFixture, binary, id, kind, directory, count string) msrRemoteTarget {
	t.Helper()
	ready := filepath.Join(t.TempDir(), "target-ready")
	token := f.tokens[id].RawToken
	p := msrExternal(t, binary, "TestMSRTargetProcess", []string{"AUTOSTREAM_MSR_TARGET_HELPER=1", "AUTOSTREAM_MSR_LISTEN=127.0.0.1:0", "AUTOSTREAM_MSR_CP_URL=" + f.server.URL, "AUTOSTREAM_MSR_TOKEN=" + token, "AUTOSTREAM_MSR_SERVICE_ID=" + id, "AUTOSTREAM_MSR_GUARD_DIR=" + directory, "AUTOSTREAM_MSR_READY_FILE=" + ready, "AUTOSTREAM_MSR_START_COUNT_FILE=" + count}, ready)
	request, e := http.NewRequestWithContext(t.Context(), http.MethodGet, p.url+"/start-admission/identity", nil)
	if e != nil {
		t.Fatal(e)
	}
	request.Header.Set("Authorization", "Bearer "+token)
	response, e := (&http.Client{Timeout: 5 * time.Second}).Do(request)
	if e != nil {
		t.Fatal(e)
	}
	defer response.Body.Close()
	var producer lifecycle.ProducerRef
	if response.StatusCode != 200 || json.NewDecoder(response.Body).Decode(&producer) != nil || string(producer.ServiceID) != id {
		t.Fatal("actual target producer identity unavailable")
	}
	return msrRemoteTarget{URL: p.url, Token: token, CountFile: count, Directory: directory, Binary: binary, ServiceID: id, Kind: kind, Producer: producer, Process: p}
}
func (r msrRemoteTarget) count(t *testing.T) int {
	t.Helper()
	raw, e := os.ReadFile(r.CountFile)
	if os.IsNotExist(e) {
		return 0
	}
	if e != nil {
		t.Fatal(e)
	}
	value, e := strconv.Atoi(string(bytes.TrimSpace(raw)))
	if e != nil {
		t.Fatal("invalid actual launch counter")
	}
	return value
}
func (r msrRemoteTarget) start(t *testing.T, record lifecycle.Record, baseOverride string) int {
	t.Helper()
	resource, e := manualhold.Resource(record)
	if e != nil {
		t.Fatal(e)
	}
	endpoint := "/streams/start"
	body := map[string]any{"stream_id": string(resource.ResourceID), "name": "Synthetic MSR", "input_url": "srt://input.example.com:9000"}
	if r.Kind == "worker" {
		endpoint = "/jobs/start"
		body = map[string]any{"stream_id": string(resource.ResourceID), "stream_name": "Synthetic MSR", "encoder_profile_id": "synthetic-encoder-profile", "video_width": 854, "video_height": 480, "video_fps": 30, "video_ingest_url": "srt://synthetic.invalid:10000", "video_ingest_passphrase": "synthetic-test-only-passphrase", "video_ingest_pbkeylen": 16}
	}
	for _, entry := range record.Vector.Vector {
		if entry.Key.Axis == lifecycle.RunAxis && entry.Value.Literal != nil {
			body["archive_run_id"] = string(*entry.Value.Literal)
			if r.Kind != "worker" {
				body["started_at"] = time.Now().UTC().Format(time.RFC3339Nano)
			}
		}
	}
	raw, _ := json.Marshal(body)
	base := r.URL
	if baseOverride != "" {
		base = baseOverride
	}
	request, e := http.NewRequestWithContext(t.Context(), http.MethodPost, base+endpoint, bytes.NewReader(raw))
	if e != nil {
		t.Fatal(e)
	}
	request.Header.Set("Authorization", "Bearer "+r.Token)
	request.Header.Set("Content-Type", "application/json")
	encoded, e := manualhold.Encode(record)
	if e != nil {
		t.Fatal(e)
	}
	request.Header.Set(manualhold.Header, encoded)
	response, e := (&http.Client{Timeout: 15 * time.Second}).Do(request)
	if e != nil {
		t.Fatal("target request transport:", e)
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, response.Body)
	return response.StatusCode
}
func msrFrozenAdmission(t *testing.T, f *msrHTTPFixture, id string) store.AutoStartAdmission {
	t.Helper()
	hold := f.hold(t)
	if hold.Held {
		f.resume(t, "resume-before-"+id)
		hold = f.hold(t)
	}
	if _, e := f.streams.UpdateStreamStatus(t.Context(), f.stream.ID, "ready"); e != nil {
		t.Fatal(e)
	}
	stream := f.source(t)
	assignments, e := f.services.ListStreamAssignments(t.Context(), stream.ID)
	if e != nil {
		t.Fatal(e)
	}
	claimed, e := f.streams.(store.StreamStartClaimStore).ClaimStreamStart(store.WithAutomaticStartHold(t.Context(), hold.Revision), store.StreamStartClaimRequest{StreamID: stream.ID, ExpectedStatus: stream.Status, ExpectedStreamUpdatedAt: stream.UpdatedAt, ExpectedPrimaryAssignments: primaryStreamAssignments(assignments), ArchiveEnabled: true, ArchiveStartedAt: time.Now().UTC()})
	if e != nil {
		t.Fatal("real Start Claim fixture:", e)
	}
	stream = claimed.Stream
	a := store.AutoStartAdmission{RequestID: id, StreamID: stream.ID, Automatic: true, HoldRevision: hold.Revision, StreamUpdatedAt: stream.UpdatedAt, SettingsRevision: stream.SettingsRevision, RunID: stream.ArchiveRunID}
	for _, service := range primaryStreamAssignments(assignments) {
		if service.ServiceType != "encoder_recorder" && service.ServiceType != "worker" {
			continue
		}
		record, e := f.handler.manualHoldRecord(t.Context(), stream, hold, service, autoStartCorrelation(id, "auto-start", "start"))
		if e != nil {
			t.Fatal(e)
		}
		a.Records = append(a.Records, record)
	}
	if e = f.owner.SaveAutoStartAdmission(t.Context(), a); e != nil {
		t.Fatal(e)
	}
	return a
}
func msrStopForFence(t *testing.T, f *msrHTTPFixture) {
	t.Helper()
	stream := f.source(t)
	id := "msr-fence-stop-" + outputSecretRandom(t)
	status, body, header := f.call(t, http.MethodPost, "/streams/"+stream.ID+"/stop", map[string]any{"expected_updated_at": stream.UpdatedAt.UTC().Format(time.RFC3339Nano)}, id)
	if (status != 200 && status != 202) || header.Get("X-AutoStream-Manual-Hold") == "UNKNOWN" {
		t.Fatalf("actual target Hold closure failed status=%d body=%s", status, body)
	}
	hold := f.hold(t)
	if !hold.Held || hold.PendingAttemptID != "" {
		t.Fatal("target close ACKs were not committed")
	}
}
func msrRecordFor(t *testing.T, a store.AutoStartAdmission, id string) lifecycle.Record {
	t.Helper()
	for _, record := range a.Records {
		resource, e := manualhold.Resource(record)
		if e == nil && string(resource.OwnerID) == id {
			return record
		}
	}
	t.Fatal("original target record missing")
	return lifecycle.Record{}
}

// Each scenario owns a fresh Stream, assignments and target journals. A prior
// modern Run keeps its artifact-report obligation; tests never clear it by fiat.
func msrDelayedTargetCases(t *testing.T, parent *msrHTTPFixture) {
	t.Helper()
	newFixture := func(t *testing.T) *msrHTTPFixture {
		if parent.db == nil {
			return msrFixtureFromStores(t, preparationClaimStores(t, false))
		}
		return msrFixture(t)
	}
	setup := func(t *testing.T, f *msrHTTPFixture) (msrRemoteTarget, msrRemoteTarget) {
		erDir, wkDir := filepath.Join(t.TempDir(), "encoder"), filepath.Join(t.TempDir(), "worker")
		erCount, wkCount := filepath.Join(t.TempDir(), "encoder-count"), filepath.Join(t.TempDir(), "worker-count")
		encoder := msrTarget(t, f, os.Getenv("AUTOSTREAM_MSR_ENCODER_TEST_BINARY"), "selected-encoder", "encoder_recorder", erDir, erCount)
		worker := msrTarget(t, f, os.Getenv("AUTOSTREAM_MSR_WORKER_TEST_BINARY"), "msr-worker", "worker", wkDir, wkCount)
		f.dispatcher.mu.Lock()
		f.dispatcher.remotes = map[string]msrRemoteTarget{"selected-encoder": encoder, "msr-worker": worker}
		f.dispatcher.mu.Unlock()
		return encoder, worker
	}
	t.Run("positive_actual_target_launch", func(t *testing.T) {
		f := newFixture(t)
		encoder, worker := setup(t, f)
		a := msrFrozenAdmission(t, f, "msr-positive-"+outputSecretRandom(t))
		for _, target := range []msrRemoteTarget{encoder, worker} {
			before := target.count(t)
			status := target.start(t, msrRecordFor(t, a, target.ServiceID), "")
			if status != 200 && status != 202 {
				t.Fatalf("actual target positive start status=%d kind=%s", status, target.Kind)
			}
			if target.count(t) != before+1 {
				t.Fatal("actual target fake launch boundary was not exercised")
			}
		}
		msrStopForFence(t, f)
	})
	names := []string{"claim_before_dispatch", "dispatch_before_arrival", "encoder_old_start_after_hold", "worker_old_job_after_hold", "resume_old_start_race", "old_retry_after_hold", "bot_stale_cache", "cp_bot_partition", "target_restart_old_request", "same_id_different_content"}
	for _, name := range names {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t)
			encoder, worker := setup(t, f)
			a := msrFrozenAdmission(t, f, "msr-delayed-"+outputSecretRandom(t))
			beforeER, beforeWK := encoder.count(t), worker.count(t)
			var delayed *httptest.Server
			var release chan struct{}
			var statusResult chan int
			if name == "dispatch_before_arrival" {
				reached := make(chan struct{})
				release = make(chan struct{})
				delayed = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					close(reached)
					<-release
					request, e := http.NewRequestWithContext(r.Context(), r.Method, encoder.URL+r.URL.Path, r.Body)
					if e != nil {
						w.WriteHeader(503)
						return
					}
					request.Header = r.Header.Clone()
					response, e := http.DefaultClient.Do(request)
					if e != nil {
						w.WriteHeader(503)
						return
					}
					defer response.Body.Close()
					w.WriteHeader(response.StatusCode)
					_, _ = io.Copy(w, response.Body)
				}))
				defer delayed.Close()
				statusResult = make(chan int, 1)
				go func() { statusResult <- encoder.start(t, msrRecordFor(t, a, encoder.ServiceID), delayed.URL) }()
				select {
				case <-reached:
				case <-time.After(5 * time.Second):
					t.Fatal("HTTP arrival barrier not reached")
				}
			}
			msrStopForFence(t, f)
			if name == "bot_stale_cache" || name == "cp_bot_partition" {
				msrBotStaleDispatch(t, f, name == "cp_bot_partition")
				return
			}
			if name == "resume_old_start_race" {
				f.resume(t, "msr-resume-old-start-"+outputSecretRandom(t))
			}
			if name == "target_restart_old_request" {
				encoder.Process.stop(t)
				worker.Process.stop(t)
				encoder = msrTarget(t, f, encoder.Binary, encoder.ServiceID, encoder.Kind, encoder.Directory, encoder.CountFile)
				worker = msrTarget(t, f, worker.Binary, worker.ServiceID, worker.Kind, worker.Directory, worker.CountFile)
				f.dispatcher.mu.Lock()
				f.dispatcher.remotes[encoder.ServiceID], f.dispatcher.remotes[worker.ServiceID] = encoder, worker
				f.dispatcher.mu.Unlock()
			}
			if name == "dispatch_before_arrival" {
				close(release)
				select {
				case status := <-statusResult:
					if status < 400 {
						t.Fatal("delayed HTTP request crossed committed Hold")
					}
				case <-time.After(15 * time.Second):
					t.Fatal("delayed HTTP request result missing")
				}
			}
			for _, target := range []msrRemoteTarget{encoder, worker} {
				if name == "encoder_old_start_after_hold" && target.Kind == "worker" {
					continue
				}
				if name == "worker_old_job_after_hold" && target.Kind != "worker" {
					continue
				}
				record := msrRecordFor(t, a, target.ServiceID)
				if name == "same_id_different_content" {
					record.Header.DomainRef.SnapshotRef = "same-id-different-content"
				}
				repeats := 1
				if name == "old_retry_after_hold" {
					repeats = 2
				}
				for i := 0; i < repeats; i++ {
					status := target.start(t, record, "")
					if status < 400 {
						t.Fatalf("stale target Start accepted status=%d", status)
					}
				}
			}
			if encoder.count(t) != beforeER || worker.count(t) != beforeWK {
				t.Fatal("stale Start launched an actual Encoder process or Worker job")
			}
			if _, e := f.owner.InspectAutoStartAdmission(t.Context(), f.stream.ID, a.RequestID); e == nil {
				t.Fatal("stale CP source remained current")
			}
		})
	}
}

func msrBotStaleDispatch(t *testing.T, f *msrHTTPFixture, partition bool) {
	t.Helper()
	hold := f.hold(t)
	if !hold.Held {
		msrStopForFence(t, f)
		hold = f.hold(t)
	}
	f.stream = f.source(t)
	beforeER, beforeWK := f.dispatcher.remotes["selected-encoder"].count(t), f.dispatcher.remotes["msr-worker"].count(t)
	var mu sync.Mutex
	posts, gets, denied := 0, 0, false
	panel := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		if r.Method == http.MethodPost {
			posts++
		} else {
			gets++
		}
		mu.Unlock()
		if partition {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		r.Header.Set("Authorization", "Bearer "+f.tokens["msr-bot"].RawToken)
		capture := httptest.NewRecorder()
		f.handler.ServeHTTP(capture, r)
		if r.Method == http.MethodPost {
			mu.Lock()
			denied = capture.Code == http.StatusConflict && (bytes.Contains(capture.Body.Bytes(), []byte("manual_hold")) || bytes.Contains(capture.Body.Bytes(), []byte("revision")))
			mu.Unlock()
		}
		for key, values := range capture.Header() {
			w.Header()[key] = values
		}
		w.WriteHeader(capture.Code)
		_, _ = w.Write(capture.Body.Bytes())
	}))
	defer panel.Close()
	ready := filepath.Join(t.TempDir(), "bot-ready.json")
	p := msrExternal(t, os.Getenv("AUTOSTREAM_MSR_BOT_TEST_BINARY"), "TestMSRBotProcessHelper", []string{"AUTOSTREAM_MSR_BOT_HELPER=1", "AUTOSTREAM_MSR_BOT_CP_URL=" + panel.URL, "AUTOSTREAM_MSR_BOT_SERVICE_ID=msr-bot", "AUTOSTREAM_MSR_BOT_READY=" + ready}, ready)
	msrDriverJSON(t, p.url, http.MethodPost, "/presence", map[string]bool{"known": true, "present": false}, nil)
	msrDriverJSON(t, p.url, http.MethodPost, "/dispatch-block", nil, nil)
	stale := hold.Revision - 1
	if stale == 0 {
		stale = 1
	}
	msrDriverJSON(t, p.url, http.MethodPost, "/snapshot", msrBotSnapshot(f, true, false, stale), nil)
	msrDriverJSON(t, p.url, http.MethodPost, "/presence", map[string]bool{"known": true, "present": true}, nil)
	msrDriverJSON(t, p.url, http.MethodPost, "/join", map[string]any{"stream_id": f.stream.ID, "guild_id": "100000000000000001", "voice_channel_id": "100000000000000002", "user_id": "synthetic-participant", "assignment_epoch": "1970-01-01T00:01:40Z/7/synthetic-profile-revision", "existing_presence": true}, nil)
	type botState struct {
		ManagedCalls   int `json:"managed_calls"`
		HTTPDispatches int `json:"http_dispatches"`
		BlockedCalls   int `json:"blocked_calls"`
		InspectCalls   int `json:"inspect_calls"`
	}
	var state botState
	deadline := time.Now().Add(5 * time.Second)
	for {
		msrDriverJSON(t, p.url, http.MethodGet, "/state?stream_id="+f.stream.ID, nil, &state)
		if state.BlockedCalls == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("Bot barrier not reached: %+v", state)
		}
		time.Sleep(10 * time.Millisecond)
	}
	msrDriverJSON(t, p.url, http.MethodPost, "/dispatch-release", nil, nil)
	deadline = time.Now().Add(5 * time.Second)
	for {
		msrDriverJSON(t, p.url, http.MethodGet, "/state?stream_id="+f.stream.ID, nil, &state)
		if state.HTTPDispatches == 1 && state.InspectCalls == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("original Bot Attempt did not perform one dispatch+Inspect: %+v", state)
		}
		time.Sleep(10 * time.Millisecond)
	}
	msrDriverJSON(t, p.url, http.MethodPost, "/reconnect", nil, nil)
	msrDriverJSON(t, p.url, http.MethodPost, "/source-unavailable", nil, nil)
	msrDriverJSON(t, p.url, http.MethodGet, "/state?stream_id="+f.stream.ID, nil, &state)
	mu.Lock()
	sent, inspections, rejected := posts, gets, denied
	mu.Unlock()
	if sent != 1 || inspections != 1 || state.ManagedCalls != 1 || state.HTTPDispatches != 1 || (!partition && !rejected) {
		t.Fatalf("old Bot Attempt retried, bypassed Hold or skipped pure Inspect: posts=%d get=%d state=%+v rejected=%v", sent, inspections, state, rejected)
	}
	if f.hold(t) != hold || f.dispatcher.remotes["selected-encoder"].count(t) != beforeER || f.dispatcher.remotes["msr-worker"].count(t) != beforeWK {
		t.Fatal("stale/disconnected Bot changed Hold or launched target")
	}
	msrDriverJSON(t, p.url, http.MethodPost, "/shutdown", nil, nil)
}

func msrRuntimeSnapshot(t *testing.T, f *msrHTTPFixture) map[string]any {
	t.Helper()
	request, e := http.NewRequestWithContext(t.Context(), http.MethodGet, f.server.URL+"/services/runtime-config?service_id=msr-bot", nil)
	if e != nil {
		t.Fatal(e)
	}
	request.Header.Set("Authorization", "Bearer "+f.tokens["msr-bot"].RawToken)
	response, e := f.client.Do(request)
	if e != nil {
		t.Fatal(e)
	}
	defer response.Body.Close()
	var snapshot map[string]any
	if response.StatusCode != 200 || json.NewDecoder(response.Body).Decode(&snapshot) != nil {
		t.Fatal("actual runtime configuration not available")
	}
	return snapshot
}
func msrRuntimeBinding(t *testing.T, snapshot map[string]any, streamID string) map[string]any {
	t.Helper()
	monitor, ok := snapshot["discord_waiting_monitor"].(map[string]any)
	if !ok {
		t.Fatal("actual waiting monitor absent")
	}
	bindings, ok := monitor["bindings"].([]any)
	if !ok {
		t.Fatal("actual waiting bindings absent")
	}
	for _, raw := range bindings {
		binding, ok := raw.(map[string]any)
		if ok && binding["stream_id"] == streamID {
			return binding
		}
	}
	t.Fatal("actual waiting Stream binding absent")
	return nil
}
func msrResumeWithPendingBotRefresh(t *testing.T, f *msrHTTPFixture) {
	t.Helper()
	snapshot := msrRuntimeSnapshot(t, f)
	binding := msrRuntimeBinding(t, snapshot, f.stream.ID)
	if ready, _ := binding["ready"].(bool); !ready {
		t.Fatalf("actual waiting binding not Ready: %v", binding["readiness_code"])
	}
	readyFile := filepath.Join(t.TempDir(), "bot-pending-ready.json")
	bot := msrExternal(t, os.Getenv("AUTOSTREAM_MSR_BOT_TEST_BINARY"), "TestMSRBotProcessHelper", []string{"AUTOSTREAM_MSR_BOT_HELPER=1", "AUTOSTREAM_MSR_BOT_CP_URL=" + f.server.URL, "AUTOSTREAM_MSR_BOT_SERVICE_ID=msr-bot", "AUTOSTREAM_MSR_BOT_READY=" + readyFile}, readyFile)
	msrDriverJSON(t, bot.url, http.MethodPost, "/snapshot", snapshot, nil)
	f.dispatcher.mu.Lock()
	f.dispatcher.botURL = bot.url
	f.dispatcher.mu.Unlock()
	epoch := binding["selection_epoch"].(string) + "/" + strconv.FormatUint(uint64(binding["target_revision"].(float64)), 10) + "/" + binding["profile_revision"].(string)
	var refreshed bool
	f.dispatcher.mu.Lock()
	f.dispatcher.beforeClose = func(ctx context.Context, _ store.RegisteredService, _ lifecycle.Record) error {
		f.dispatcher.mu.Lock()
		if refreshed {
			f.dispatcher.mu.Unlock()
			return nil
		}
		refreshed = true
		f.dispatcher.mu.Unlock()
		pending, e := f.owner.ObserveAutoStartHold(ctx, f.stream.ID)
		if e != nil {
			return e
		}
		if pending.PendingAttemptID == "" || !pending.Held {
			t.Error("target Close did not observe pending Hold")
		}
		next := msrRuntimeSnapshot(t, f)
		nextBinding := msrRuntimeBinding(t, next, f.stream.ID)
		if known, _ := nextBinding["auto_start_hold_known"].(bool); known {
			t.Error("pending runtime projected Hold as fully known")
		}
		msrDriverJSON(t, bot.url, http.MethodPost, "/snapshot", next, nil)
		request, e := http.NewRequestWithContext(ctx, http.MethodGet, bot.url+"/streams/"+url.PathEscape(f.stream.ID)+"/auto-start-condition?hold_revision="+strconv.FormatUint(pending.Revision, 10)+"&assignment_epoch="+url.QueryEscape(epoch), nil)
		if e != nil {
			return e
		}
		request.Header.Set("Authorization", "Bearer synthetic-msr-bot-process-read")
		response, e := f.client.Do(request)
		if e != nil {
			return e
		}
		defer response.Body.Close()
		if response.StatusCode < 400 {
			t.Error("strict cached-Hold condition accepted pending runtime")
		}
		presence, e := f.dispatcher.ObserveAutoStartPresence(ctx, store.RegisteredService{}, f.stream.ID, epoch)
		if e != nil || !presence.Known || !presence.Present {
			t.Errorf("actual SDK presence was blocked by pending Hold: %+v err=%v", presence, e)
		}
		return nil
	}
	f.dispatcher.mu.Unlock()
	a := f.resume(t, "msr-pending-runtime-resume")
	f.dispatcher.mu.Lock()
	f.dispatcher.beforeClose = nil
	didRefresh := refreshed
	f.dispatcher.mu.Unlock()
	starts, _, _ := f.dispatcher.counts()
	if !didRefresh || a.Hold.Held || a.Condition.Value == nil || !*a.Condition.Value || starts != 2 {
		t.Fatalf("explicit Resume failed pending-refresh separation: refreshed=%v held=%v starts=%d", didRefresh, a.Hold.Held, starts)
	}
	var state struct {
		ManagedCalls   int `json:"managed_calls"`
		HTTPDispatches int `json:"http_dispatches"`
	}
	msrDriverJSON(t, bot.url, http.MethodGet, "/state?stream_id="+f.stream.ID, nil, &state)
	if state.ManagedCalls != 0 || state.HTTPDispatches != 0 {
		t.Fatal("pending refresh itself issued an automatic Start")
	}
	msrDriverJSON(t, bot.url, http.MethodPost, "/shutdown", nil, nil)
}
func TestManualAutoStartPendingBotRefreshMemory(t *testing.T) {
	f := msrFixtureFromStores(t, preparationClaimStores(t, false))
	f.stop(t)
	msrEnableAutomatic(t, f)
	msrResumeWithPendingBotRefresh(t, f)
}

func msrReadProcessURL(ctx context.Context, file string) (string, error) {
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		raw, e := os.ReadFile(file)
		if e == nil {
			candidate := string(bytes.TrimSpace(raw))
			var ready struct{ URL string }
			if json.Unmarshal(raw, &ready) == nil && ready.URL != "" {
				candidate = ready.URL
			}
			parsed, parseErr := url.Parse(candidate)
			if parseErr == nil && parsed.Scheme == "http" && (parsed.Hostname() == "127.0.0.1" || parsed.Hostname() == "::1") && parsed.Port() != "" && parsed.User == nil && parsed.RawQuery == "" && parsed.Fragment == "" && (parsed.Path == "" || parsed.Path == "/") {
				return candidate, nil
			}
		} else if !os.IsNotExist(e) {
			return "", e
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-ticker.C:
		}
	}
}
