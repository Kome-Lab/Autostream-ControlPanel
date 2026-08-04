package updateagent

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type coordinatorReportErrorPanel struct {
	CoordinatorPanel
	err error
}

func (p *coordinatorReportErrorPanel) Report(context.Context, string, JobReport) error {
	return p.err
}

type coordinatorTerminalProofPanel struct {
	CoordinatorPanel
	terminal UpdateJob
}

func (p *coordinatorTerminalProofPanel) ClaimHost(
	context.Context,
	string,
	string,
	string,
) (*UpdateJob, bool, error) {
	job := p.terminal
	return &job, true, nil
}

type hostPullTerminalProofPanel struct {
	HostPullControlPlane
	HostPullExecutionControlPlane
	terminal UpdateJob
}

func (p *hostPullTerminalProofPanel) ClaimHost(
	context.Context,
	string,
	string,
	string,
) (*UpdateJob, bool, error) {
	job := p.terminal
	return &job, true, nil
}

func TestAgentFatalTerminalReportPreservesRecoveryState(t *testing.T) {
	stateDir := t.TempDir()
	journal, err := OpenJournal(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	job := terminalRecoveryStateJob("updater-one", "", "worker-one", "job-agent-fatal")
	job.LeaseToken = strings.Repeat("l", 48)
	job.LeaseGeneration = 3
	setTerminalRecoveryState(t, journal, job)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"code": "system_update_transition_invalid",
		})
	}))
	defer server.Close()

	agent := Agent{
		Config:  Config{NodeID: job.AgentServiceID, StateDir: stateDir},
		Panel:   PanelClient{BaseURL: server.URL, Token: "runtime-token", HTTP: server.Client()},
		Journal: journal,
		Logf:    func(string, ...any) {},
	}
	_, err = agent.emit(
		context.Background(), job, "failed", "remote_stage_missing",
		"terminal report rejected", 100, "", "",
	)
	if !IsFatalReportError(err) {
		t.Fatalf("fatal terminal report error=%v", err)
	}
	assertTerminalRecoveryStatePreserved(t, journal, job.ID)
}

func TestCentralCoordinatorFatalTerminalReportPreservesRecoveryState(t *testing.T) {
	coordinator, panel, _ := newCoordinatorFixture(t, "host-a")
	worker := coordinator.workers["host-a"]
	job := coordinatorJob("host-a", "target-host-a", "job-coordinator-fatal")
	job.AgentServiceID = coordinator.Config.NodeID
	setTerminalRecoveryState(t, worker.journal, job)
	coordinator.Panel = &coordinatorReportErrorPanel{
		CoordinatorPanel: panel,
		err: &PanelHTTPError{
			Status: http.StatusConflict,
			Code:   "system_update_transition_invalid",
		},
	}

	_, err := worker.emit(
		context.Background(), job, "failed", "remote_stage_missing",
		"terminal report rejected", 100, "", "",
	)
	if !IsFatalReportError(err) {
		t.Fatalf("fatal terminal report error=%v", err)
	}
	assertTerminalRecoveryStatePreserved(t, worker.journal, job.ID)
}

func TestAgentTerminalReportCleanupFailurePreservesRecoveryState(t *testing.T) {
	stateDir := t.TempDir()
	journal, err := OpenJournal(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	job := terminalRecoveryStateJob("updater-one", "", "worker-one", "job-agent-report-cleanup")
	setTerminalRecoveryState(t, journal, job)
	queueTerminalRecoveryReport(t, journal, job)
	makeUnsafeJobsRoot(t, stateDir)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var report JobReport
		if err := json.NewDecoder(r.Body).Decode(&report); err != nil {
			t.Error(err)
		}
		writeCommittedTerminalReport(t, w, job.ID, report)
	}))
	defer server.Close()
	agent := Agent{
		Config:  Config{NodeID: job.AgentServiceID, StateDir: stateDir},
		Panel:   PanelClient{BaseURL: server.URL, Token: "runtime-token", HTTP: server.Client()},
		Journal: journal,
		Logf:    func(string, ...any) {},
	}

	err = agent.flushReports(context.Background())
	assertTerminalReportCleanupFailure(t, err, journal, job.ID)
}

func TestHostPullTerminalReportCleanupFailurePreservesRecoveryState(t *testing.T) {
	agent, panel, _, _, _ := newHostPullExecutionHarness(t, true)
	job := *panel.job
	job.RecoveryRequired = false
	setTerminalRecoveryState(t, agent.Journal, job)
	queueTerminalRecoveryReport(t, agent.Journal, job)
	makeUnsafeJobsRoot(t, agent.StateDir)

	err := agent.flushExecutionReports(context.Background(), panel)
	assertTerminalReportCleanupFailure(t, err, agent.Journal, job.ID)
}

func TestCentralCoordinatorTerminalReportCleanupFailurePreservesRecoveryState(t *testing.T) {
	coordinator, _, _ := newCoordinatorFixture(t, "host-a")
	worker := coordinator.workers["host-a"]
	job := coordinatorJob("host-a", "target-host-a", "job-coordinator-report-cleanup")
	job.AgentServiceID = coordinator.Config.NodeID
	setTerminalRecoveryState(t, worker.journal, job)
	queueTerminalRecoveryReport(t, worker.journal, job)
	makeUnsafeJobsRoot(t, worker.stateDir())

	err := worker.flushReports(context.Background())
	assertTerminalReportCleanupFailure(t, err, worker.journal, job.ID)
}

func TestAgentRestartTerminalProofCleansExactJobDirectory(t *testing.T) {
	for _, test := range []struct {
		name           string
		unsafeJobsRoot bool
	}{
		{name: "cleanup succeeds"},
		{name: "cleanup failure is returned before journal clear", unsafeJobsRoot: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			stateDir := t.TempDir()
			job := terminalRecoveryStateJob("updater-one", "", "worker-one", "job-agent-proof")
			journal, jobDir := restartedTerminalResponseLossJournal(
				t, stateDir, job, test.unsafeJobsRoot,
			)
			terminal := terminalRecoveryProof(job)
			server := terminalRecoveryProofServer(t, terminal)
			defer server.Close()
			agent := Agent{
				Config:  Config{NodeID: job.AgentServiceID, StateDir: stateDir},
				Panel:   PanelClient{BaseURL: server.URL, Token: "runtime-token", HTTP: server.Client()},
				Journal: journal,
				Logf:    func(string, ...any) {},
			}

			err := agent.pollOnce(context.Background())
			assertTerminalProofCleanupResult(
				t, err, journal, jobDir, test.unsafeJobsRoot,
			)
		})
	}
}

func TestHostPullRestartTerminalProofCleansExactJobDirectory(t *testing.T) {
	for _, test := range []struct {
		name           string
		unsafeJobsRoot bool
	}{
		{name: "cleanup succeeds"},
		{name: "cleanup failure is returned before journal clear", unsafeJobsRoot: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			agent, panel, _, binding, policy := newHostPullExecutionHarness(t, true)
			job := *panel.job
			job.RecoveryRequired = false
			journal, jobDir := restartedTerminalResponseLossJournal(
				t, agent.StateDir, job, test.unsafeJobsRoot,
			)
			agent.Journal = journal
			agent.ControlPlane = &hostPullTerminalProofPanel{
				HostPullControlPlane:          panel,
				HostPullExecutionControlPlane: panel,
				terminal:                      terminalRecoveryProof(job),
			}

			err := agent.executeOnce(context.Background(), binding, policy)
			assertTerminalProofCleanupResult(
				t, err, journal, jobDir, test.unsafeJobsRoot,
			)
		})
	}
}

func TestCentralCoordinatorRestartTerminalProofCleansExactJobDirectory(t *testing.T) {
	for _, test := range []struct {
		name           string
		unsafeJobsRoot bool
	}{
		{name: "cleanup succeeds"},
		{name: "cleanup failure is returned before journal clear", unsafeJobsRoot: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			coordinator, panel, _ := newCoordinatorFixture(t, "host-a")
			worker := coordinator.workers["host-a"]
			job := coordinatorJob("host-a", "target-host-a", "job-coordinator-proof")
			job.AgentServiceID = coordinator.Config.NodeID
			journal, jobDir := restartedTerminalResponseLossJournal(
				t, worker.stateDir(), job, test.unsafeJobsRoot,
			)
			worker.journal = journal
			coordinator.Panel = &coordinatorTerminalProofPanel{
				CoordinatorPanel: panel,
				terminal:         terminalRecoveryProof(job),
			}

			err := worker.pollOnce(context.Background())
			assertTerminalProofCleanupResult(
				t, err, journal, jobDir, test.unsafeJobsRoot,
			)
		})
	}
}

func terminalRecoveryStateJob(agentID, hostID, targetID, jobID string) UpdateJob {
	return UpdateJob{
		ID: jobID, AgentServiceID: agentID, HostID: hostID,
		TargetID: targetID, TargetType: "worker", ServiceType: "worker",
		DeploymentMode: ModeSystemd, CurrentVersion: "v1.0.0", TargetVersion: "v1.1.0",
		LeaseToken: strings.Repeat("l", 48), LeaseGeneration: 2, ReportSequence: 1,
	}
}

func terminalRecoveryProof(job UpdateJob) UpdateJob {
	job.LeaseToken = ""
	job.ReleaseToken = ""
	job.Status = "failed"
	job.Progress = 100
	job.Code = "remote_stage_missing"
	job.Sequence = 9
	return job
}

func setTerminalRecoveryState(t *testing.T, journal *Journal, job UpdateJob) {
	t.Helper()
	if err := journal.SetActive(&job); err != nil {
		t.Fatal(err)
	}
	plan := validRemotePlan()
	plan.JobID = job.ID
	plan.PlanSHA256 = ""
	var err error
	plan.PlanSHA256, err = plan.ComputePlanSHA256()
	if err != nil {
		t.Fatal(err)
	}
	if err := journal.SetActivePlan(plan); err != nil {
		t.Fatal(err)
	}
}

func assertTerminalRecoveryStatePreserved(t *testing.T, journal *Journal, jobID string) {
	t.Helper()
	if active := journal.Active(); active == nil || active.ID != jobID {
		t.Fatalf("fatal report rejection cleared active job: %+v", active)
	}
	if plan := journal.ActivePlan(); plan == nil || plan.JobID != jobID {
		t.Fatalf("fatal report rejection cleared active plan: %+v", plan)
	}
	if pending := journal.Pending(); len(pending) != 1 ||
		pending[0].JobID != jobID || !isTerminalUpdateStatus(pending[0].Report.Status) {
		t.Fatalf("fatal report rejection acknowledged terminal report: %+v", pending)
	}
}

func queueTerminalRecoveryReport(t *testing.T, journal *Journal, job UpdateJob) {
	t.Helper()
	if _, err := journal.Queue(
		job.ID, job.AgentServiceID, job.LeaseToken, job.LeaseGeneration,
		"failed", "remote_stage_missing", "terminal result", 100, "", "",
	); err != nil {
		t.Fatal(err)
	}
}

func makeUnsafeJobsRoot(t *testing.T, stateDir string) {
	t.Helper()
	if err := os.WriteFile(jobsRoot(stateDir), []byte("unsafe"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func assertTerminalReportCleanupFailure(
	t *testing.T,
	err error,
	journal *Journal,
	jobID string,
) {
	t.Helper()
	if err == nil || !strings.Contains(err.Error(), "jobs state root is unsafe") {
		t.Fatalf("terminal report cleanup error=%v", err)
	}
	if pending := journal.Pending(); len(pending) != 0 {
		t.Fatalf("committed terminal report cursor was not acknowledged: %+v", pending)
	}
	if active := journal.Active(); active == nil || active.ID != jobID {
		t.Fatalf("terminal report cleanup failure cleared active job: %+v", active)
	}
	if plan := journal.ActivePlan(); plan == nil || plan.JobID != jobID {
		t.Fatalf("terminal report cleanup failure cleared active plan: %+v", plan)
	}
}

func restartedTerminalResponseLossJournal(
	t *testing.T,
	stateDir string,
	job UpdateJob,
	unsafeJobsRoot bool,
) (*Journal, string) {
	t.Helper()
	journal, err := OpenJournal(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	setTerminalRecoveryState(t, journal, job)
	if _, err := journal.Queue(
		job.ID, job.AgentServiceID, job.LeaseToken, job.LeaseGeneration,
		"failed", "remote_stage_missing", "response lost", 100, "", "",
	); err != nil {
		t.Fatal(err)
	}
	jobDir := jobDirectory(stateDir, job.ID)
	if unsafeJobsRoot {
		if err := os.WriteFile(jobsRoot(stateDir), []byte("unsafe"), 0o600); err != nil {
			t.Fatal(err)
		}
	} else {
		created, err := ensurePrivateJobDirectory(stateDir, job.ID)
		if err != nil {
			t.Fatal(err)
		}
		jobDir = created
		if err := os.WriteFile(filepath.Join(jobDir, "intent.json"), []byte("{}\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	reopened, err := OpenJournal(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	if pending := reopened.Pending(); len(pending) != 0 {
		t.Fatalf("restart retained response-loss report: %+v", pending)
	}
	if active := reopened.Active(); active == nil || active.ID != job.ID {
		t.Fatalf("restart lost active recovery cursor: %+v", active)
	}
	return reopened, jobDir
}

func terminalRecoveryProofServer(t *testing.T, terminal UpdateJob) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/services/update-jobs/claim" {
			t.Errorf("unexpected terminal proof request: %s %s", r.Method, r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(ClaimResponse{
			ClearActiveJobID: true,
			TerminalJob:      &terminal,
		}); err != nil {
			t.Error(err)
		}
	}))
}

func assertTerminalProofCleanupResult(
	t *testing.T,
	err error,
	journal *Journal,
	jobDir string,
	unsafeJobsRoot bool,
) {
	t.Helper()
	if unsafeJobsRoot {
		if err == nil || !strings.Contains(err.Error(), "jobs state root is unsafe") {
			t.Fatalf("unsafe job cleanup error=%v", err)
		}
		if active := journal.Active(); active == nil {
			t.Fatal("cleanup failure cleared the active recovery cursor")
		}
		if plan := journal.ActivePlan(); plan == nil {
			t.Fatal("cleanup failure cleared the active recovery plan")
		}
		return
	} else if err != nil {
		t.Fatalf("terminal proof cleanup: %v", err)
	}
	if active := journal.Active(); active != nil {
		t.Fatalf("exact terminal proof did not clear active job: %+v", active)
	}
	if plan := journal.ActivePlan(); plan != nil {
		t.Fatalf("exact terminal proof did not clear active plan: %+v", plan)
	}
	if !unsafeJobsRoot {
		if _, statErr := os.Lstat(jobDir); !errors.Is(statErr, os.ErrNotExist) {
			t.Fatalf("exact terminal proof did not remove job directory %q: %v", jobDir, statErr)
		}
	}
}
