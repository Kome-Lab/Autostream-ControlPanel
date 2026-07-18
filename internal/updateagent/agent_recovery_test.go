package updateagent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type recordingHelperRunner struct {
	calls [][]string
}

func TestPersistDeployedVersionIsBoundedAndKeepsActiveRecoveryState(t *testing.T) {
	journal, err := OpenJournal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	active := &UpdateJob{ID: "job-persist", ReportSequence: 1}
	if err := journal.SetActive(active); err != nil {
		t.Fatal(err)
	}
	// Force every journal replacement to fail after Active has been durably
	// recorded. The agent must return to the active-job recovery protocol
	// instead of blocking indefinitely after a verified Docker cutover.
	journal.path = filepath.Join(t.TempDir(), "missing", "journal.json")
	agent := Agent{
		Journal:                  journal,
		Logf:                     func(string, ...any) {},
		PersistenceRetryInterval: time.Millisecond,
		PersistenceRetryLimit:    5 * time.Millisecond,
	}
	started := time.Now()
	if err := agent.persistDeployedVersion(context.Background(), "worker", "v2.0.0"); err == nil {
		t.Fatal("expected bounded persistence failure")
	}
	if time.Since(started) > time.Second {
		t.Fatal("persistence retry did not respect its deadline")
	}
	if got := journal.Active(); got == nil || got.ID != active.ID {
		t.Fatalf("active recovery state was lost: %+v", got)
	}
}

func (r *recordingHelperRunner) Run(_ context.Context, _ string, _ []string, name string, args ...string) (string, error) {
	r.calls = append(r.calls, append([]string{name}, args...))
	return `{"status":"rolled_back","previous_digest":"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","rolled_back":true}`, nil
}

func TestRecoveryRequiredInvokesOnlyReconcileAtMonotonicProgress(t *testing.T) {
	var reports []JobReport
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/report") {
			var report JobReport
			if err := json.NewDecoder(r.Body).Decode(&report); err != nil {
				t.Error(err)
			}
			reports = append(reports, report)
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
	runner := &recordingHelperRunner{}
	agent := Agent{
		Config:     Config{NodeID: "updater-1", StateDir: t.TempDir(), HelperArgv: []string{"/usr/bin/sudo"}, Targets: []Target{{TargetID: "worker", ServiceType: "worker", DeploymentMode: ModeSystemd}}},
		ConfigPath: "/etc/autostream/updater.json",
		Panel:      PanelClient{BaseURL: server.URL, Token: "token", HTTP: server.Client()},
		Journal:    journal,
		Runner:     runner,
		Logf:       func(string, ...any) {},
	}
	job := UpdateJob{ID: "job-recover", TargetID: "worker", ServiceType: "worker", DeploymentMode: ModeSystemd, CurrentVersion: "v1.0.0", TargetVersion: "v2.0.0", LeaseToken: "lease", LeaseGeneration: 2, RecoveryRequired: true, ReportSequence: 1, Progress: 95}
	if err := agent.processJob(context.Background(), job); err != nil {
		t.Fatal(err)
	}
	if len(runner.calls) != 1 {
		t.Fatalf("helper calls = %v", runner.calls)
	}
	joined := strings.Join(runner.calls[0], " ")
	if !strings.Contains(joined, "helper reconcile") || strings.Contains(joined, "helper apply") {
		t.Fatalf("recovery invoked unsafe helper: %s", joined)
	}
	if len(reports) != 2 || reports[0].Status != "reconciling" || reports[0].Progress != 99 || reports[1].Status != "rolled_back" || reports[1].Progress != 100 {
		t.Fatalf("unexpected recovery reports: %+v", reports)
	}
	if journal.Active() != nil {
		t.Fatal("terminal report acknowledgement did not clear active job")
	}
}

func TestPollOnceUsesActiveJobProtocolAndClearsWithoutAnotherClaim(t *testing.T) {
	journal, err := OpenJournal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	active := &UpdateJob{ID: "job-active", LeaseToken: "old"}
	if err := journal.SetActive(active); err != nil {
		t.Fatal(err)
	}
	claims := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		claims++
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["active_job_id"] != active.ID {
			t.Errorf("active_job_id = %q", body["active_job_id"])
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"clear_active_job_id":true}`))
	}))
	defer server.Close()
	agent := Agent{Config: Config{NodeID: "updater-1"}, Panel: PanelClient{BaseURL: server.URL, Token: "token", HTTP: server.Client()}, Journal: journal, Logf: func(string, ...any) {}}
	if err := agent.pollOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if claims != 1 || journal.Active() != nil {
		t.Fatalf("claims=%d active=%+v", claims, journal.Active())
	}
}
