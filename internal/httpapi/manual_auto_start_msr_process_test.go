//go:build manual_stop_integration

package httpapi

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	lifecycle "github.com/Kome-Lab/Autostream-Contracts/v3lifecycle"
	lf "github.com/example/autostream-control-panel/internal/lifecyclefoundation"
	"github.com/example/autostream-control-panel/internal/manualhold"
	"github.com/example/autostream-control-panel/internal/store"
	"github.com/go-sql-driver/mysql"
)

type msrProcessInput struct {
	Original                                  store.StartClaimRequestOriginal
	Mode, URL, Path, StreamID, GuardDirectory string
	Body                                      json.RawMessage
	HoldRevision                              uint64
	Stream                                    store.Stream
	Assignments                               []store.RegisteredService
}
type msrProcessResult struct {
	Status  int
	Error   string
	Changed bool
}

func msrWaitFile(ctx context.Context, name string) error {
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		if _, e := os.Stat(name); e == nil {
			return nil
		} else if !errors.Is(e, os.ErrNotExist) {
			return e
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}
func msrWriteJSON(name string, value any) error {
	body, e := json.Marshal(value)
	if e != nil {
		return e
	}
	file, e := os.CreateTemp(filepath.Dir(name), ".msr-ready-*")
	if e != nil {
		return e
	}
	temporary := file.Name()
	defer os.Remove(temporary)
	if e = file.Chmod(0600); e != nil {
		file.Close()
		return e
	}
	if _, e = file.Write(body); e != nil {
		file.Close()
		return e
	}
	if e = file.Close(); e != nil {
		return e
	}
	return os.Rename(temporary, name)
}

// TestMSRProcessHelper is a test-binary entry point, never a product server. Its
// SQL connection and OS process are independent from the parent Server/Store.
func TestMSRProcessHelper(t *testing.T) {
	if os.Getenv("AUTOSTREAM_MSR_CP_PROCESS") != "1" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	var input msrProcessInput
	body, e := os.ReadFile(os.Getenv("AUTOSTREAM_MSR_PROCESS_INPUT"))
	if e != nil || json.Unmarshal(body, &input) != nil {
		t.Fatal("invalid synthetic process input")
	}
	ready, result, release := os.Getenv("AUTOSTREAM_MSR_PROCESS_READY"), os.Getenv("AUTOSTREAM_MSR_PROCESS_RESULT"), os.Getenv("AUTOSTREAM_MSR_PROCESS_RELEASE")
	if input.Mode == "http" {
		if e = msrWriteJSON(ready, map[string]any{"pid": os.Getpid()}); e != nil {
			t.Fatal(e)
		}
		if e = msrWaitFile(ctx, release); e != nil {
			t.Fatal(e)
		}
		req, e := http.NewRequestWithContext(ctx, http.MethodPost, input.URL+input.Path, bytes.NewReader(input.Body))
		if e != nil {
			t.Fatal(e)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-CSRF-Token", os.Getenv("AUTOSTREAM_MSR_PROCESS_CSRF"))
		req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: os.Getenv("AUTOSTREAM_MSR_PROCESS_SESSION")})
		response, e := (&http.Client{Timeout: 20 * time.Second}).Do(req)
		out := msrProcessResult{}
		if e != nil {
			out.Error = "transport_ack_unknown"
		} else {
			out.Status = response.StatusCode
			_, _ = io.Copy(io.Discard, response.Body)
			_ = response.Body.Close()
		}
		if e = msrWriteJSON(result, out); e != nil {
			t.Fatal(e)
		}
		return
	}
	db, e := sql.Open("mysql", os.Getenv("AUTOSTREAM_MSR_PROCESS_DSN"))
	if e != nil {
		t.Fatal("isolated SQL open failed")
	}
	defer db.Close()
	streams := store.NewMariaDBStreamStore(db)
	if input.Mode == "server" {
		auth := store.NewMariaDBAuthStoreWithSecretKey(db, "synthetic-msr-process-encryption-key")
		d := &msrDispatcher{owner: streams, directory: input.GuardDirectory, guards: map[string]*manualhold.Guard{}}
		server := NewServer(streams, WithAuthStore(auth), WithServiceRegistryStore(auth), WithAuditStore(store.NewMariaDBAuditStore(db)), WithProfileStore(store.NewMariaDBProfileStore(db)), WithServiceDispatcher(d))
		server.manualAutoStartProducer = func() (lifecycle.ProducerRef, error) { return msrProducer("C01", "control-panel"), nil }
		listener, e := net.Listen("tcp", "127.0.0.1:0")
		if e != nil {
			t.Fatal(e)
		}
		httpServer := &http.Server{Handler: server, ReadHeaderTimeout: 3 * time.Second}
		defer httpServer.Close()
		go func() { _ = httpServer.Serve(listener) }()
		if e = msrWriteJSON(ready, map[string]any{"url": "http://" + listener.Addr().String(), "pid": os.Getpid()}); e != nil {
			t.Fatal(e)
		}
		if e = msrWaitFile(ctx, release); e != nil {
			t.Fatal(e)
		}
		return
	}
	if e = msrWriteJSON(ready, map[string]any{"pid": os.Getpid()}); e != nil {
		t.Fatal(e)
	}
	if e = msrWaitFile(ctx, release); e != nil {
		t.Fatal(e)
	}
	out := msrProcessResult{}
	if input.Mode == "automatic-claim" || input.Mode == "manual-claim" {
		claimContext := store.WithAutomaticStartHold(ctx, input.HoldRevision)
		if input.Mode == "manual-claim" {
			claimContext = store.WithManualStartHold(ctx, input.HoldRevision)
		}
		if input.Original.RequestID != "" {
			claimContext = store.WithStartClaimRequest(claimContext, input.Original)
		}
		_, e = streams.ClaimStreamStart(claimContext, store.StreamStartClaimRequest{StreamID: input.StreamID, ExpectedStatus: input.Stream.Status, ExpectedStreamUpdatedAt: input.Stream.UpdatedAt, ExpectedPrimaryAssignments: input.Assignments, ArchiveEnabled: true, ArchiveStartedAt: time.Now().UTC()})
		if e == nil {
			out.Changed = true
		} else if errors.Is(e, lf.ErrHeld) || errors.Is(e, lf.ErrConflict) || errors.Is(e, store.ErrServiceAssignmentConflict) {
			out.Error = "rejected_unchanged"
		} else {
			out.Error = "unknown"
		}
	}
	if e = msrWriteJSON(result, out); e != nil {
		t.Fatal(e)
	}
}

type msrChild struct {
	cmd                    *exec.Cmd
	ready, result, release string
	output                 bytes.Buffer
	wait                   chan error
	once                   sync.Once
}

func msrProcessDSN(t *testing.T, f *msrHTTPFixture) string {
	t.Helper()
	cfg, e := mysql.ParseDSN(strings.TrimPrefix(os.Getenv("AUTOSTREAM_MARIADB_TEST_DSN"), "mysql://"))
	if e != nil {
		t.Fatal("invalid disposable SQL DSN")
	}
	if e = f.db.QueryRowContext(t.Context(), "SELECT DATABASE()").Scan(&cfg.DBName); e != nil {
		t.Fatal(e)
	}
	cfg.ParseTime = true
	return cfg.FormatDSN()
}
func msrStartChild(t *testing.T, f *msrHTTPFixture, input msrProcessInput) *msrChild {
	t.Helper()
	directory := t.TempDir()
	inputFile := filepath.Join(directory, "input.json")
	if e := msrWriteJSON(inputFile, input); e != nil {
		t.Fatal(e)
	}
	child := &msrChild{ready: filepath.Join(directory, "ready.json"), result: filepath.Join(directory, "result.json"), release: filepath.Join(directory, "release"), wait: make(chan error, 1)}
	child.cmd = exec.Command(os.Args[0], "-test.run=^TestMSRProcessHelper$", "-test.count=1")
	child.cmd.Env = append(os.Environ(), "AUTOSTREAM_MSR_CP_PROCESS=1", "AUTOSTREAM_MSR_PROCESS_INPUT="+inputFile, "AUTOSTREAM_MSR_PROCESS_READY="+child.ready, "AUTOSTREAM_MSR_PROCESS_RESULT="+child.result, "AUTOSTREAM_MSR_PROCESS_RELEASE="+child.release, "AUTOSTREAM_MSR_PROCESS_DSN="+msrProcessDSN(t, f), "AUTOSTREAM_MSR_PROCESS_SESSION="+f.cookie.Value, "AUTOSTREAM_MSR_PROCESS_CSRF="+f.csrf)
	child.cmd.Stdout = &child.output
	child.cmd.Stderr = &child.output
	if e := child.cmd.Start(); e != nil {
		t.Fatal(e)
	}
	go func() { child.wait <- child.cmd.Wait() }()
	t.Cleanup(func() {
		child.once.Do(func() {
			_ = os.WriteFile(child.release, []byte("release"), 0600)
			_ = child.cmd.Process.Kill()
			<-child.wait
		})
	})
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	if e := msrWaitFile(ctx, child.ready); e != nil {
		t.Fatal("independent process readiness failed")
	}
	return child
}
func (c *msrChild) releaseBarrier(t *testing.T) {
	t.Helper()
	if e := os.WriteFile(c.release, []byte("release"), 0600); e != nil {
		t.Fatal(e)
	}
}
func (c *msrChild) outcome(t *testing.T) msrProcessResult {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 25*time.Second)
	defer cancel()
	if e := msrWaitFile(ctx, c.result); e != nil {
		t.Fatal("independent process result missing")
	}
	var out msrProcessResult
	data, e := os.ReadFile(c.result)
	if e != nil || json.Unmarshal(data, &out) != nil {
		t.Fatal("invalid process result")
	}
	c.once.Do(func() {
		if e := <-c.wait; e != nil {
			t.Fatalf("independent process failed: %v", e)
		}
	})
	return out
}

func TestMSR09ResumeSchedulerRace(t *testing.T) {
	f := msrFixture(t)
	f.stop(t)
	if _, e := f.streams.UpdateStreamStatus(t.Context(), f.stream.ID, "ready"); e != nil {
		t.Fatal(e)
	}
	current, hold := f.source(t), f.hold(t)
	assignments, e := f.services.ListStreamAssignments(t.Context(), current.ID)
	if e != nil {
		t.Fatal(e)
	}
	child := msrStartChild(t, f, msrProcessInput{Mode: "automatic-claim", StreamID: current.ID, Stream: current, HoldRevision: hold.Revision, Assignments: primaryStreamAssignments(assignments)})
	body := f.resumeBody(t, "msr-racing-resume")
	req := store.AutoStartHoldRequest{RequestID: "msr-racing-resume", StreamID: current.ID, Operation: store.AutoStartExplicitResume, ActorUserID: hold.ActorUserID, ExpectedHoldRevision: hold.Revision, ExpectedStreamUpdatedAt: current.UpdatedAt, ExpectedSettingsRevision: current.SettingsRevision, ExpectedRunID: current.ArchiveRunID, Correlation: autoStartCorrelation("msr-racing-resume", store.AutoStartExplicitResume, "close"), RequiredTargets: manualHoldTargets(primaryStreamAssignments(assignments), current.ID)}
	a, created, e := f.owner.BeginExplicitResume(t.Context(), req)
	if e != nil || !created {
		t.Fatal("BeginResume before race barrier", e)
	}
	t.Run("manual_claim_pending_resume", func(t *testing.T) {
		manual := msrStartChild(t, f, msrProcessInput{Mode: "manual-claim", StreamID: current.ID, Stream: current, HoldRevision: a.Hold.Revision, Assignments: primaryStreamAssignments(assignments)})
		manual.releaseBarrier(t)
		out := manual.outcome(t)
		after, e := f.streams.GetStream(t.Context(), current.ID)
		if out.Changed || out.Error != "rejected_unchanged" || e != nil || !reflect.DeepEqual(current, after) {
			t.Fatalf("manual claim crossed pending Resume or changed Stream: %+v err=%v", out, e)
		}
	})
	child.releaseBarrier(t)
	out := child.outcome(t)
	if out.Changed || out.Error != "rejected_unchanged" {
		t.Fatalf("scheduler crossed pending hold: %+v", out)
	}
	evidence, e := f.handler.closeManualHoldTargets(t.Context(), current, a, primaryStreamAssignments(assignments))
	if e != nil {
		t.Fatal(e)
	}
	condition, e := f.handler.currentResumeCondition(t.Context(), current, a.Hold, assignments)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = f.owner.CommitExplicitResume(t.Context(), req, condition, evidence); e != nil {
		t.Fatal(e)
	}
	status, _, _ := f.call(t, http.MethodPost, "/streams/"+current.ID+"/auto-start/resume", body, "")
	if status != 200 && status != 202 {
		t.Fatal("same Attempt result inspection failed")
	}
	if f.hold(t).Held {
		t.Fatal("explicit Resume failed after scheduler rejection")
	}
	msrDelayedTargetCases(t, f)
}
func TestMSR11LostAcknowledgement(t *testing.T) {
	for _, after := range []bool{false, true} {
		name := "before_commit"
		if after {
			name = "after_commit"
		}
		t.Run(name, func(t *testing.T) {
			f := msrFixture(t)
			f.stop(t)
			requestID := "msr-lost-ack"
			body := f.resumeBody(t, requestID)
			raw, _ := json.Marshal(body)
			var drop atomic.Bool
			drop.Store(true)
			wrapped := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/auto-start/resume") && drop.CompareAndSwap(true, false) {
					if after {
						record := httptest.NewRecorder()
						f.handler.ServeHTTP(record, r)
					}
					connection, _, e := w.(http.Hijacker).Hijack()
					if e == nil {
						_ = connection.Close()
					}
					return
				}
				f.handler.ServeHTTP(w, r)
			}))
			defer wrapped.Close()
			child := msrStartChild(t, f, msrProcessInput{Mode: "http", URL: wrapped.URL, Path: "/streams/" + f.stream.ID + "/auto-start/resume", Body: raw})
			child.releaseBarrier(t)
			out := child.outcome(t)
			if out.Error != "transport_ack_unknown" {
				t.Fatalf("ACK loss was not transport UNKNOWN: %+v", out)
			}
			a, e := f.owner.InspectAutoStartAttempt(t.Context(), f.stream.ID, requestID)
			if !after {
				if !errors.Is(e, store.ErrNotFound) || !f.hold(t).Held {
					t.Fatal("precommit loss changed state")
				}
				return
			}
			if e != nil || !a.Final || a.Hold.Held {
				t.Fatal("postcommit effect missing behind lost ACK")
			}
			before := f.snapshot(t)
			status, _, _ := f.call(t, http.MethodGet, "/streams/"+f.stream.ID+"/auto-start/resume-attempts/"+requestID, nil, "")
			if status != 200 || !bytes.Equal(before, f.snapshot(t)) {
				t.Fatal("lost-ACK Inspect reexecuted or mutated")
			}
			starts, _, closes := f.dispatcher.counts()
			if starts != 0 || closes != 4 {
				t.Fatal("lost ACK caused duplicate Start/close")
			}
		})
	}
	f := msrFixture(t)
	f.stop(t)
	f.resume(t, "msr-after-ack-loss-for-targets")
	msrDelayedTargetCases(t, f)
}
func TestMSR12IndependentServers(t *testing.T) {
	f := msrFixture(t)
	f.stop(t)
	body := f.resumeBody(t, "msr-independent-resume")
	server := msrStartChild(t, f, msrProcessInput{Mode: "server", GuardDirectory: f.dispatcher.directory})
	var ready struct{ URL string }
	data, e := os.ReadFile(server.ready)
	if e != nil || json.Unmarshal(data, &ready) != nil || ready.URL == "" {
		t.Fatal("independent server URL missing")
	}
	raw, _ := json.Marshal(body)
	first := msrStartChild(t, f, msrProcessInput{Mode: "http", URL: f.server.URL, Path: "/streams/" + f.stream.ID + "/auto-start/resume", Body: raw})
	second := msrStartChild(t, f, msrProcessInput{Mode: "http", URL: ready.URL, Path: "/streams/" + f.stream.ID + "/auto-start/resume", Body: raw})
	first.releaseBarrier(t)
	second.releaseBarrier(t)
	one, two := first.outcome(t), second.outcome(t)
	for _, out := range []msrProcessResult{one, two} {
		if out.Error != "" || (out.Status != 200 && out.Status != 202 && out.Status != 409) {
			t.Fatalf("independent Resume: %+v", out)
		}
	}
	a, e := f.owner.InspectAutoStartAttempt(t.Context(), f.stream.ID, "msr-independent-resume")
	if e != nil || !a.Final || a.Hold.Held {
		t.Fatal("independent processes did not preserve one final Attempt")
	}
	history, e := f.owner.ListHoldHistory(t.Context(), f.stream.ID)
	if e != nil || len(history) != 5 {
		t.Fatal("independent duplicate Resume changed history count")
	}
	server.releaseBarrier(t)
	server.once.Do(func() {
		if e := <-server.wait; e != nil {
			t.Fatalf("independent server exited before explicit shutdown: %v", e)
		}
	})
	t.Run("global_claim_request_uniqueness", func(t *testing.T) { msrSQLClaimRequestUniqueness(t, f) })
	msrDelayedTargetCases(t, f)
}

func msrSQLClaimOriginal(id, actor string, stream store.Stream) store.StartClaimRequestOriginal {
	return store.StartClaimRequestOriginal{RequestID: id, StreamID: stream.ID, ActorUserID: actor, Operation: "manual-start", ExpectedStreamUpdatedAt: stream.UpdatedAt, ExpectedSettingsRevision: stream.SettingsRevision, ExpectedRunID: stream.ArchiveRunID}
}
func msrSQLClaimRequestUniqueness(t *testing.T, f *msrHTTPFixture) {
	t.Helper()
	if _, e := f.streams.UpdateStreamStatus(t.Context(), f.stream.ID, "ready"); e != nil {
		t.Fatal(e)
	}
	first := f.source(t)
	assignments, e := f.services.ListStreamAssignments(t.Context(), first.ID)
	if e != nil {
		t.Fatal(e)
	}
	hold := f.hold(t)
	original := msrSQLClaimOriginal("msr-global-original", "synthetic-human", first)
	context := store.WithStartClaimRequest(store.WithManualStartHold(t.Context(), hold.Revision), original)
	claim, e := f.streams.(store.StreamStartClaimStore).ClaimStreamStart(context, store.StreamStartClaimRequest{StreamID: first.ID, ExpectedStatus: first.Status, ExpectedStreamUpdatedAt: first.UpdatedAt, ExpectedPrimaryAssignments: primaryStreamAssignments(assignments), ArchiveEnabled: true, ArchiveStartedAt: time.Now().UTC()})
	if e != nil {
		t.Fatal("SQL original claim:", e)
	}
	if _, changed, e := f.streams.(store.StreamStartClaimStore).TransitionClaimedStreamStart(t.Context(), claim.OwnershipClaim, "failed"); e != nil || !changed {
		t.Fatal(e)
	}
	// The prior claim dispatched no target. Its explicit failed owner state is
	// startable without erasing a completed Run artifact-report obligation.
	first = f.source(t)
	fresh := msrSQLClaimOriginal(original.RequestID, "different-human", first)
	before := f.snapshot(t)
	ctx := store.WithStartClaimRequest(store.WithManualStartHold(t.Context(), hold.Revision), fresh)
	if _, e = f.streams.(store.StreamStartClaimStore).ClaimStreamStart(ctx, store.StreamStartClaimRequest{StreamID: first.ID, ExpectedStatus: first.Status, ExpectedStreamUpdatedAt: first.UpdatedAt, ExpectedPrimaryAssignments: primaryStreamAssignments(assignments), ArchiveEnabled: true, ArchiveStartedAt: time.Now().UTC()}); !errors.Is(e, lf.ErrConflict) {
		t.Fatal("SQL reused original ID accepted", e)
	}
	after, e := f.streams.GetStream(t.Context(), first.ID)
	if e != nil || !reflect.DeepEqual(first, after) || !bytes.Equal(before, f.snapshot(t)) {
		t.Fatal("rejected SQL ID reuse changed CP rows, Run or metadata")
	}
	secondID := f.otherStream.ID
	for _, kind := range []string{"encoder_recorder", "worker", "discord_bot"} {
		id := "unique-" + kind
		f.outputSecretFixture.register(t, id, kind)
		assignServiceForTest(t, f.handler, f.cookie, f.csrf, id, secondID)
	}
	second, e := f.streams.GetStream(t.Context(), secondID)
	if e != nil {
		t.Fatal(e)
	}
	secondAssignments, e := f.services.ListStreamAssignments(t.Context(), secondID)
	if e != nil {
		t.Fatal(e)
	}
	secondHold, e := f.owner.ObserveAutoStartHold(t.Context(), secondID)
	if e != nil {
		t.Fatal(e)
	}
	shared := "msr-global-race"
	one := msrStartChild(t, f, msrProcessInput{Mode: "manual-claim", StreamID: first.ID, Stream: first, HoldRevision: hold.Revision, Assignments: primaryStreamAssignments(assignments), Original: msrSQLClaimOriginal(shared, "synthetic-human", first)})
	two := msrStartChild(t, f, msrProcessInput{Mode: "manual-claim", StreamID: second.ID, Stream: second, HoldRevision: secondHold.Revision, Assignments: primaryStreamAssignments(secondAssignments), Original: msrSQLClaimOriginal(shared, "synthetic-human", second)})
	one.releaseBarrier(t)
	two.releaseBarrier(t)
	outcomes := []msrProcessResult{one.outcome(t), two.outcome(t)}
	success := 0
	for i, out := range outcomes {
		if out.Changed {
			success++
		} else {
			if out.Error != "rejected_unchanged" {
				t.Fatalf("SQL process reuse unexpected result=%+v", out)
			}
			expected := first
			if i == 1 {
				expected = second
			}
			actual, e := f.streams.GetStream(t.Context(), expected.ID)
			if e != nil || !reflect.DeepEqual(expected, actual) {
				t.Fatal("losing SQL process changed its Stream/Run")
			}
		}
	}
	if success != 1 {
		t.Fatalf("SQL global claim request winners=%d", success)
	}
	var count int
	if e = f.db.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM stream_auto_start_claim_requests WHERE request_id=?", shared).Scan(&count); e != nil || count != 1 {
		t.Fatal("SQL request uniqueness marker count was not one")
	}
	for i, out := range outcomes {
		if out.Changed {
			streamID := first.ID
			if i == 1 {
				streamID = second.ID
			}
			stored, e := f.owner.LookupStartClaimRequest(t.Context(), streamID, shared)
			if e != nil || stored.RequestID != shared {
				t.Fatal("pure SQL original lookup failed")
			}
		}
	}
}
