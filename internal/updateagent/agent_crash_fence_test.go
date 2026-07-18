package updateagent

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

type scriptedHelperRunner struct {
	mu    sync.Mutex
	calls [][]string
	run   func(args []string) (string, error)
}

func (r *scriptedHelperRunner) Run(_ context.Context, _ string, _ []string, name string, args ...string) (string, error) {
	call := append([]string{name}, args...)
	r.mu.Lock()
	r.calls = append(r.calls, call)
	r.mu.Unlock()
	return r.run(call)
}

func (r *scriptedHelperRunner) Calls() [][]string {
	r.mu.Lock()
	defer r.mu.Unlock()
	result := make([][]string, len(r.calls))
	for i, call := range r.calls {
		result[i] = append([]string(nil), call...)
	}
	return result
}

func TestTerminalReportResponseLossUsesFreshRecoveryClaimAfterRestart(t *testing.T) {
	stateDir := t.TempDir()
	journal, err := OpenJournal(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	job := &UpdateJob{ID: "job-response-loss", LeaseToken: "lease", LeaseGeneration: 3, ReportSequence: 7}
	if err := journal.SetActive(job); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var sequences []uint64
	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var report JobReport
		if err := json.NewDecoder(r.Body).Decode(&report); err != nil {
			t.Error(err)
		}
		mu.Lock()
		attempts++
		sequences = append(sequences, report.Sequence)
		attempt := attempts
		mu.Unlock()
		if attempt == 1 {
			// The panel has processed the report, but the connection dies before
			// its response reaches the agent. The in-process queue can retry, but
			// no bearer lease is persisted for replay after restart.
			hijacker, ok := w.(http.Hijacker)
			if !ok {
				t.Fatal("httptest response writer does not support hijacking")
			}
			conn, _, err := hijacker.Hijack()
			if err != nil {
				t.Fatal(err)
			}
			_ = conn.Close()
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	agent := Agent{Config: Config{NodeID: "updater-1"}, Panel: PanelClient{BaseURL: server.URL, Token: "token", HTTP: server.Client()}, Journal: journal, Logf: func(string, ...any) {}}
	if _, err := agent.emit(context.Background(), *job, "succeeded", "", "verified", 100, "", ""); err == nil {
		t.Fatal("expected response-loss delivery error")
	}
	if active := journal.Active(); active == nil || active.ID != job.ID {
		t.Fatalf("active job was cleared before a terminal acknowledgement: %+v", active)
	}
	if pending := journal.Pending(); len(pending) != 1 || pending[0].Report.Sequence != 7 {
		t.Fatalf("terminal report was not queued for in-process retry: %+v", pending)
	}

	// Simulate a process restart. The stale report is discarded because its
	// short-lived bearer token is intentionally never persisted. The active
	// cursor remains so pollOnce obtains a fresh recovery claim and sequence.
	reopened, err := OpenJournal(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	if active := reopened.Active(); active == nil || active.ID != job.ID {
		t.Fatalf("reopened journal lost active job: %+v", active)
	}
	if active := reopened.Active(); active.LeaseToken != "" {
		t.Fatal("reopened active cursor retained a lease token")
	}
	if len(reopened.Pending()) != 0 {
		t.Fatalf("reopened journal retained stale tokenless reports: %+v", reopened.Pending())
	}
	mu.Lock()
	defer mu.Unlock()
	if len(sequences) != 1 || sequences[0] != 7 {
		t.Fatalf("terminal response loss unexpectedly replayed a stale lease: %v", sequences)
	}
}

func TestApplyErrorReconcilesUnderSameLeaseBeforeTerminal(t *testing.T) {
	var reports []JobReport
	var reportMu sync.Mutex
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/report") {
			var report JobReport
			if err := json.NewDecoder(r.Body).Decode(&report); err != nil {
				t.Error(err)
			}
			reportMu.Lock()
			reports = append(reports, report)
			reportMu.Unlock()
			w.WriteHeader(http.StatusNoContent)
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()
	journal, err := OpenJournal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	runner := &scriptedHelperRunner{run: func(args []string) (string, error) {
		command := strings.Join(args, " ")
		if strings.Contains(command, "helper apply") {
			return "", errors.New("helper transport interrupted")
		}
		if strings.Contains(command, "helper reconcile") {
			return `{"status":"succeeded","artifact_digest":"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}`, nil
		}
		return "", errors.New("unexpected helper mode")
	}}
	stateDir := t.TempDir()
	agent := Agent{
		Config:     Config{NodeID: "updater-1", StateDir: stateDir, HelperArgv: []string{"/usr/bin/sudo"}, Targets: []Target{{TargetID: "control-panel", ServiceType: "control-panel", DeploymentMode: ModeDocker}}},
		ConfigPath: "/etc/autostream/updater.json",
		Panel:      PanelClient{BaseURL: server.URL, Token: "token", HTTP: server.Client()},
		Journal:    journal,
		Runner:     runner,
		Logf:       func(string, ...any) {},
	}
	job := UpdateJob{ID: "job-apply-reconcile", TargetID: "control-panel", ServiceType: "control-panel", DeploymentMode: ModeDocker, CurrentVersion: "v1.0.0", TargetVersion: "v2.0.0", LeaseToken: "fixed-lease", LeaseGeneration: 8, ReportSequence: 12}
	if err := agent.processJob(context.Background(), job); err != nil {
		t.Fatal(err)
	}
	calls := runner.Calls()
	if len(calls) != 2 || !strings.Contains(strings.Join(calls[0], " "), "helper apply") || !strings.Contains(strings.Join(calls[1], " "), "helper reconcile") {
		t.Fatalf("apply failure did not reconcile under the active lease: %v", calls)
	}
	if journal.Active() != nil {
		t.Fatal("terminally reconciled job remained active")
	}
	reportMu.Lock()
	defer reportMu.Unlock()
	if len(reports) == 0 || reports[len(reports)-1].Status != "succeeded" || reports[len(reports)-1].LeaseToken != job.LeaseToken || reports[len(reports)-1].LeaseGeneration != job.LeaseGeneration {
		t.Fatalf("terminal report was not bound to the original lease: %+v", reports)
	}
}

func TestHelperAuthorizationRejectionFailsBeforeReconcile(t *testing.T) {
	var reports []JobReport
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var report JobReport
		if err := json.NewDecoder(r.Body).Decode(&report); err != nil {
			t.Error(err)
		}
		reports = append(reports, report)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	journal, err := OpenJournal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	runner := &scriptedHelperRunner{run: func([]string) (string, error) {
		return "autostream-updater: panel returned HTTP 403 (system_update_lease_invalid)\n", errors.New("exit status 1")
	}}
	stateDir := t.TempDir()
	agent := Agent{
		Config:     Config{NodeID: "updater-1", StateDir: stateDir, HelperArgv: []string{"/usr/bin/sudo"}, Targets: []Target{{TargetID: "worker", ServiceType: "worker", DeploymentMode: ModeDocker}}},
		ConfigPath: "/etc/autostream/updater.json", Panel: PanelClient{BaseURL: server.URL, Token: "token", HTTP: server.Client()}, Journal: journal, Runner: runner, Logf: func(string, ...any) {},
	}
	job := UpdateJob{ID: "job-auth-rejected", TargetID: "worker", ServiceType: "worker", DeploymentMode: ModeDocker, CurrentVersion: "v1.0.0", TargetVersion: "v2.0.0", LeaseToken: "lease", LeaseGeneration: 2, ReportSequence: 1}
	if err := agent.processJob(context.Background(), job); err != nil {
		t.Fatal(err)
	}
	if calls := runner.Calls(); len(calls) != 1 || !strings.Contains(strings.Join(calls[0], " "), "helper apply") {
		t.Fatalf("authorization rejection launched another privileged operation: %v", calls)
	}
	if len(reports) == 0 || reports[len(reports)-1].Status != "failed" || reports[len(reports)-1].Code != "authorization_rejected" {
		t.Fatalf("authorization rejection was not diagnosable: %+v", reports)
	}
}

func TestRecoveryRetriesAtProgressNinetyNineWithoutTerminalizing(t *testing.T) {
	var reports []JobReport
	var mu sync.Mutex
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var report JobReport
		if err := json.NewDecoder(r.Body).Decode(&report); err != nil {
			t.Error(err)
		}
		mu.Lock()
		reports = append(reports, report)
		mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	journal, err := OpenJournal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	runner := &scriptedHelperRunner{run: func([]string) (string, error) { return "", errors.New("checkpoint remains unsettled") }}
	ctx, cancel := context.WithTimeout(context.Background(), 400*time.Millisecond)
	defer cancel()
	agent := Agent{
		Config:                Config{NodeID: "updater-1", StateDir: t.TempDir(), HelperArgv: []string{"/usr/bin/sudo"}, Targets: []Target{{TargetID: "worker", ServiceType: "worker", DeploymentMode: ModeSystemd}}},
		ConfigPath:            "/etc/autostream/updater.json",
		Panel:                 PanelClient{BaseURL: server.URL, Token: "token", HTTP: server.Client()},
		Journal:               journal,
		Runner:                runner,
		Logf:                  func(string, ...any) {},
		RecoveryRetryInterval: 5 * time.Millisecond,
		ReportAckTimeout:      time.Second,
	}
	job := UpdateJob{ID: "job-retry-recovery", TargetID: "worker", ServiceType: "worker", DeploymentMode: ModeSystemd, CurrentVersion: "v1.0.0", TargetVersion: "v2.0.0", LeaseToken: "lease", LeaseGeneration: 4, RecoveryRequired: true, ReportSequence: 1, Progress: 95}
	if err := agent.processJob(ctx, job); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("transient reconcile should remain pending until context cancellation: %v", err)
	}
	if len(runner.Calls()) < 2 {
		t.Fatalf("reconciliation was not retried: %v", runner.Calls())
	}
	if active := journal.Active(); active == nil || active.ID != job.ID {
		t.Fatalf("transient reconciliation terminalized or lost active job: %+v", active)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(reports) < 2 {
		t.Fatalf("expected initial and retry reconciliation reports: %+v", reports)
	}
	for _, report := range reports {
		if report.Status != "reconciling" || report.Progress != 99 {
			t.Fatalf("recovery report must stay at reconciling/99, got %+v", report)
		}
	}
}

func TestRecoveryReportOutageFencesHelperAndBoundsPendingReport(t *testing.T) {
	var reportCalls int
	var mu sync.Mutex
	outageSeen := make(chan struct{})
	var outageOnce sync.Once
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		reportCalls++
		call := reportCalls
		mu.Unlock()
		if call == 1 {
			w.WriteHeader(http.StatusNoContent) // initial reconciliation acknowledgement
			return
		}
		outageOnce.Do(func() { close(outageSeen) })
		http.Error(w, "panel outage", http.StatusServiceUnavailable)
	}))
	defer server.Close()
	journal, err := OpenJournal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	runner := &scriptedHelperRunner{run: func([]string) (string, error) { return "", errors.New("temporary reconcile failure") }}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	agent := Agent{
		Config:                Config{NodeID: "updater-1", StateDir: t.TempDir(), HelperArgv: []string{"/usr/bin/sudo"}, Targets: []Target{{TargetID: "worker", ServiceType: "worker", DeploymentMode: ModeSystemd}}},
		ConfigPath:            "/etc/autostream/updater.json",
		Panel:                 PanelClient{BaseURL: server.URL, Token: "token", HTTP: server.Client()},
		Journal:               journal,
		Runner:                runner,
		Logf:                  func(string, ...any) {},
		RecoveryRetryInterval: 5 * time.Millisecond,
		ReportAckTimeout:      time.Second,
	}
	job := UpdateJob{ID: "job-outage-fence", TargetID: "worker", ServiceType: "worker", DeploymentMode: ModeSystemd, CurrentVersion: "v1.0.0", TargetVersion: "v2.0.0", LeaseToken: "lease", LeaseGeneration: 5, RecoveryRequired: true, ReportSequence: 1}
	done := make(chan error, 1)
	go func() { done <- agent.processJob(ctx, job) }()
	select {
	case <-outageSeen:
		cancel()
	case <-time.After(5 * time.Second):
		t.Fatal("report outage was not observed")
	}
	err = <-done
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("outage should leave recovery pending rather than terminalizing: %v", err)
	}
	if calls := runner.Calls(); len(calls) != 1 {
		t.Fatalf("helper was restarted before the unacknowledged keepalive was fenced: %v", calls)
	}
	if pending := journal.Pending(); len(pending) != 1 || pending[0].Report.Status != "reconciling" || pending[0].Report.Progress != 99 {
		t.Fatalf("outage must retain one reconciliation keepalive for replay: %+v", pending)
	}
	if active := journal.Active(); active == nil || active.ID != job.ID {
		t.Fatalf("outage must preserve active recovery state: %+v", active)
	}
}
