package updateagent

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func TestJournalKeepsLeaseTokensOutOfPersistentJSON(t *testing.T) {
	stateDir := t.TempDir()
	journal, err := OpenJournal(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	job := &UpdateJob{ID: "job-secret-free-journal", LeaseToken: "lease-must-not-persist", LeaseGeneration: 1, ReportSequence: 1}
	if err := journal.SetActive(job); err != nil {
		t.Fatal(err)
	}
	if _, err := journal.Queue(job.ID, "updater", job.LeaseToken, job.LeaseGeneration, "staging", "", "", 50, "", ""); err != nil {
		t.Fatal(err)
	}
	contents, err := os.ReadFile(filepath.Join(stateDir, "journal.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(contents), job.LeaseToken) {
		t.Fatal("journal persisted a raw execution lease")
	}
	if pending := journal.Pending(); len(pending) != 1 || pending[0].Report.LeaseToken != job.LeaseToken {
		t.Fatalf("in-process report lost its ephemeral lease: %+v", pending)
	}
	reopened, err := OpenJournal(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(reopened.Pending()) != 0 || reopened.Active() == nil || reopened.Active().LeaseToken != "" {
		t.Fatalf("restart did not drop stale reports and preserve a secret-free cursor: active=%+v pending=%+v", reopened.Active(), reopened.Pending())
	}
}

func TestJournalReportRetryPreservesMonotonicSequence(t *testing.T) {
	journal, err := OpenJournal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	first, err := journal.Queue("job-1", "updater-1", "lease", 1, "installing", "", "", 50, "", "")
	if err != nil {
		t.Fatal(err)
	}
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if attempts.Add(1) == 1 {
			http.Error(w, "offline", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	agent := Agent{Config: Config{NodeID: "updater-1"}, Panel: PanelClient{BaseURL: server.URL, Token: "token", HTTP: server.Client()}, Journal: journal}
	if err := agent.flushReports(context.Background()); err == nil || len(journal.Pending()) != 1 {
		t.Fatalf("first delivery should remain queued: %v", err)
	}
	if err := agent.flushReports(context.Background()); err != nil || len(journal.Pending()) != 0 {
		t.Fatalf("retry should be acknowledged: %v", err)
	}
	second, err := journal.Queue("job-1", "updater-1", "lease", 1, "succeeded", "", "", 100, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if first.Sequence != 1 || second.Sequence != 2 {
		t.Fatalf("sequences are not monotonic: %d then %d", first.Sequence, second.Sequence)
	}
}

func TestStaleLeaseReportIsDroppedButInvalidTransitionIsFatal(t *testing.T) {
	journal, err := OpenJournal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	_, _ = journal.Queue("job-stale", "updater-1", "old-lease", 1, "failed", "", "", 100, "", "")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte(`{"code":"system_update_lease_invalid"}`))
	}))
	agent := Agent{Config: Config{NodeID: "updater-1"}, Panel: PanelClient{BaseURL: server.URL, Token: "token", HTTP: server.Client()}, Journal: journal, Logf: func(string, ...any) {}}
	if err := agent.flushReports(context.Background()); err != nil || len(journal.Pending()) != 0 {
		t.Fatalf("stale lease should be dropped: %v pending=%d", err, len(journal.Pending()))
	}
	server.Close()
	transitionServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte(`{"code":"system_update_transition_invalid"}`))
	}))
	defer transitionServer.Close()
	agent.Panel = PanelClient{BaseURL: transitionServer.URL, Token: "token", HTTP: transitionServer.Client()}
	_, _ = journal.Queue("job-invalid", "updater-1", "lease", 1, "installing", "", "", 50, "", "")
	err = agent.flushReports(context.Background())
	if !IsFatalReportError(err) || len(journal.Pending()) != 1 {
		t.Fatalf("invalid transition must remain a fatal error: %v pending=%d", err, len(journal.Pending()))
	}
}

func TestClaimReportSequenceIsUsedExactlyAndTerminalAckClearsActive(t *testing.T) {
	journal, err := OpenJournal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	job := &UpdateJob{ID: "job-sequence", Sequence: 5, ReportSequence: 6, LeaseToken: "lease", LeaseGeneration: 2}
	if err := journal.SetActive(job); err != nil {
		t.Fatal(err)
	}
	report, err := journal.Queue(job.ID, "updater", job.LeaseToken, job.LeaseGeneration, "succeeded", "", "", 100, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if report.Sequence != 6 {
		t.Fatalf("first report sequence = %d, want exact claim value 6", report.Sequence)
	}
	if err := journal.Ack(job.ID, report.Sequence); err != nil {
		t.Fatal(err)
	}
	if journal.Active() != nil {
		t.Fatal("terminal acknowledgement must atomically clear ActiveJob")
	}
}

func TestActiveExecutionStopsOnStaleLeaseReport(t *testing.T) {
	journal, err := OpenJournal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	_, _ = journal.Queue("job-active", "updater", "old", 1, "installing", "", "", 70, "", "")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte(`{"code":"system_update_lease_invalid"}`))
	}))
	defer server.Close()
	agent := Agent{Panel: PanelClient{BaseURL: server.URL, Token: "token", HTTP: server.Client()}, Journal: journal, Logf: func(string, ...any) {}}
	agent.updating.Store(true)
	if err := agent.flushReports(context.Background()); !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("active stale lease error = %v", err)
	}
	if len(journal.Pending()) != 0 {
		t.Fatal("permanently stale report was not dropped")
	}
}
