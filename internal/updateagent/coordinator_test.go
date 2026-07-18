package updateagent

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type coordinatorEventLog struct {
	mu     sync.Mutex
	events []string
}

func (l *coordinatorEventLog) add(event string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.events = append(l.events, event)
}

func (l *coordinatorEventLog) snapshot() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.events...)
}

type coordinatorTestPanel struct {
	mu sync.Mutex

	jobs       map[string][]UpdateJob
	claimHosts []string
	reports    []JobReport
	reportJobs []string
	grants     []MutationGrantRequest
	events     *coordinatorEventLog

	heartbeatOnce sync.Once
	heartbeatSeen chan struct{}
	lastDeployed  map[string]string
	lastHosts     map[string]HostHeartbeat
}

func (p *coordinatorTestPanel) RegisterWithHosts(_ context.Context, _ Config, _ map[string]string, _ map[string]HostHeartbeat) error {
	return nil
}

func (p *coordinatorTestPanel) HeartbeatWithHosts(_ context.Context, _ Config, _ string, deployed map[string]string, hosts map[string]HostHeartbeat) error {
	p.mu.Lock()
	p.lastDeployed = cloneStringMap(deployed)
	p.lastHosts = cloneHostHeartbeats(hosts)
	p.mu.Unlock()
	if p.heartbeatSeen != nil {
		p.heartbeatOnce.Do(func() { close(p.heartbeatSeen) })
	}
	return nil
}

func (p *coordinatorTestPanel) ClaimHost(_ context.Context, _, hostID, _ string) (*UpdateJob, bool, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.claimHosts = append(p.claimHosts, hostID)
	queue := p.jobs[hostID]
	if len(queue) == 0 {
		return nil, false, nil
	}
	job := queue[0]
	p.jobs[hostID] = append([]UpdateJob(nil), queue[1:]...)
	return &job, false, nil
}

func (p *coordinatorTestPanel) Report(_ context.Context, jobID string, report JobReport) error {
	p.mu.Lock()
	p.reports = append(p.reports, report)
	p.reportJobs = append(p.reportJobs, jobID)
	p.mu.Unlock()
	if p.events != nil {
		p.events.add("report:" + report.Status)
	}
	return nil
}

func (p *coordinatorTestPanel) IssueMutationGrant(_ context.Context, _ string, request MutationGrantRequest) (MutationGrant, error) {
	p.mu.Lock()
	p.grants = append(p.grants, request)
	grantNumber := len(p.grants)
	p.mu.Unlock()
	if p.events != nil {
		p.events.add("grant:" + request.Operation)
	}
	return MutationGrant{Token: fmt.Sprintf("grant-%d", grantNumber), ExpiresAt: time.Now().Add(time.Minute).Format(time.RFC3339)}, nil
}

type coordinatorTestDownloader struct {
	downloads atomic.Int64
}

func (d *coordinatorTestDownloader) Download(_ context.Context, _, _, _ string, dest string) (DownloadedArtifact, error) {
	d.downloads.Add(1)
	return DownloadedArtifact{RootDir: filepath.Join(dest, "root"), SHA256: strings.Repeat("a", 64)}, nil
}

func (d *coordinatorTestDownloader) ResolveDockerReleaseForArch(_ context.Context, _, _, _, _, _, _ string) (ResolvedDockerRelease, error) {
	d.downloads.Add(1)
	return ResolvedDockerRelease{
		SourceVersion: "v1.2.3", ManifestDigest: "sha256:" + strings.Repeat("b", 64),
		ManifestSHA256: "sha256:" + strings.Repeat("c", 64), PlatformDigest: "sha256:" + strings.Repeat("d", 64),
	}, nil
}

type coordinatorTestRemote struct {
	mu sync.Mutex

	probes    map[string]RemoteProbeResult
	probeErrs map[string]error
	events    *coordinatorEventLog

	stageStarted       chan string
	stageRelease       <-chan struct{}
	stageReleaseByHost map[string]<-chan struct{}
	stageActive        int
	maxStage           int
	stagePlans         []RemotePlan
	stageTokens        []string
	stageErr           error
	applyPlans         []RemotePlan
	applyGrants        []string
	applyStarted       chan struct{}
	applyRelease       <-chan struct{}
	reconcile          []RemotePlan
	applyErr           error
	reconcileErr       error
}

func (r *coordinatorTestRemote) Probe(_ context.Context, host SSHHost) (RemoteProbeResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.probeErrs[host.HostID]; err != nil {
		return RemoteProbeResult{}, err
	}
	return r.probes[host.HostID], nil
}

func (r *coordinatorTestRemote) Stage(ctx context.Context, host SSHHost, plan RemotePlan, token RemoteSecret) (RemoteStageResult, error) {
	r.mu.Lock()
	r.stagePlans = append(r.stagePlans, plan)
	r.stageTokens = append(r.stageTokens, token.Reveal())
	stageErr := r.stageErr
	r.stageErr = nil
	r.stageActive++
	if r.stageActive > r.maxStage {
		r.maxStage = r.stageActive
	}
	r.mu.Unlock()
	if r.events != nil {
		r.events.add("stage:" + host.HostID)
	}
	if r.stageStarted != nil {
		r.stageStarted <- host.HostID
	}
	stageRelease := r.stageRelease
	if release, ok := r.stageReleaseByHost[host.HostID]; ok {
		stageRelease = release
	}
	if stageRelease != nil {
		select {
		case <-ctx.Done():
			r.stageDone()
			return RemoteStageResult{}, ctx.Err()
		case <-stageRelease:
		}
	}
	r.stageDone()
	if stageErr != nil {
		return RemoteStageResult{}, stageErr
	}
	return RemoteStageResult{Status: "staged", SessionID: plan.SessionID, PlanSHA256: plan.PlanSHA256, ArtifactDigest: plan.ArtifactDigest}, nil
}

func (r *coordinatorTestRemote) stageDone() {
	r.mu.Lock()
	r.stageActive--
	r.mu.Unlock()
}

func (r *coordinatorTestRemote) Apply(ctx context.Context, _ SSHHost, plan RemotePlan, grant RemoteSecret) (ApplyResult, error) {
	r.mu.Lock()
	r.applyPlans = append(r.applyPlans, plan)
	r.applyGrants = append(r.applyGrants, grant.Reveal())
	err := r.applyErr
	r.applyErr = nil
	r.mu.Unlock()
	if r.events != nil {
		r.events.add("apply")
	}
	if r.applyStarted != nil {
		select {
		case r.applyStarted <- struct{}{}:
		default:
		}
	}
	if r.applyRelease != nil {
		select {
		case <-ctx.Done():
			return ApplyResult{}, ctx.Err()
		case <-r.applyRelease:
		}
	}
	if err != nil {
		return ApplyResult{}, err
	}
	return ApplyResult{Status: "succeeded", ArtifactDigest: normalizeDigest(plan.ArtifactDigest)}, nil
}

func (r *coordinatorTestRemote) Reconcile(_ context.Context, _ SSHHost, plan RemotePlan, _ RemoteSecret) (ApplyResult, error) {
	r.mu.Lock()
	r.reconcile = append(r.reconcile, plan)
	err := r.reconcileErr
	r.reconcileErr = nil
	r.mu.Unlock()
	if r.events != nil {
		r.events.add("reconcile")
	}
	if err != nil {
		return ApplyResult{}, err
	}
	return ApplyResult{Status: "succeeded", ArtifactDigest: normalizeDigest(plan.ArtifactDigest)}, nil
}

func TestCentralCoordinatorRunsDifferentHostsInParallel(t *testing.T) {
	c, panel, remote := newCoordinatorFixture(t, "host-a", "host-b")
	panel.jobs["host-a"] = []UpdateJob{coordinatorJob("host-a", "target-host-a", "job-a")}
	panel.jobs["host-b"] = []UpdateJob{coordinatorJob("host-b", "target-host-b", "job-b")}
	started := make(chan string, 2)
	release := make(chan struct{})
	remote.stageStarted = started
	remote.stageRelease = release

	errs := make(chan error, 2)
	go func() { errs <- c.workers["host-a"].pollOnce(t.Context()) }()
	go func() { errs <- c.workers["host-b"].pollOnce(t.Context()) }()
	waitForValues(t, started, 2)
	close(release)
	for range 2 {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
	remote.mu.Lock()
	maxStage := remote.maxStage
	remote.mu.Unlock()
	if maxStage != 2 {
		t.Fatalf("different-host max concurrent stages = %d, want 2", maxStage)
	}
}

func TestCentralCoordinatorMakesControlPanelUpdateGloballyExclusive(t *testing.T) {
	c, panel, remote := newCoordinatorFixture(t, "host-a", "host-b", "host-c")
	c.KeepaliveInterval = 10 * time.Millisecond
	c.ReportAckTimeout = time.Second
	controlTarget := Target{TargetID: "control-panel", HostID: "host-b", ServiceType: "control_panel", DeploymentMode: ModeSystemd}
	c.workers["host-b"].targets = map[string]Target{controlTarget.TargetID: controlTarget}
	panel.jobs["host-a"] = []UpdateJob{coordinatorJob("host-a", "target-host-a", "job-a")}
	panel.jobs["host-b"] = []UpdateJob{{
		ID: "job-control-panel", HostID: "host-b", TargetID: controlTarget.TargetID,
		ServiceType: controlTarget.ServiceType, DeploymentMode: controlTarget.DeploymentMode,
		CurrentVersion: "v1.2.2", TargetVersion: "v1.2.3", LeaseToken: "lease-control-panel",
		LeaseGeneration: 1, ReportSequence: 1,
	}}
	panel.jobs["host-c"] = []UpdateJob{coordinatorJob("host-c", "target-host-c", "job-c")}

	started := make(chan string, 3)
	releaseA := make(chan struct{})
	releaseControlPanel := make(chan struct{})
	remote.stageStarted = started
	remote.stageReleaseByHost = map[string]<-chan struct{}{
		"host-a": releaseA,
		"host-b": releaseControlPanel,
	}

	errs := make(chan error, 3)
	go func() { errs <- c.workers["host-a"].pollOnce(t.Context()) }()
	if host := waitForValue(t, started); host != "host-a" {
		t.Fatalf("first staged host = %q, want host-a", host)
	}
	go func() { errs <- c.workers["host-b"].pollOnce(t.Context()) }()
	waitForPendingExecutionWriter(t, &c.executionGate)
	waitForCoordinatorReportCount(t, panel, "job-control-panel", 3)
	go func() { errs <- c.workers["host-c"].pollOnce(t.Context()) }()
	assertNoCoordinatorStage(t, started)

	close(releaseA)
	if host := waitForValue(t, started); host != "host-b" {
		t.Fatalf("exclusive staged host = %q, want host-b", host)
	}
	assertNoCoordinatorStage(t, started)

	close(releaseControlPanel)
	if host := waitForValue(t, started); host != "host-c" {
		t.Fatalf("post-exclusive staged host = %q, want host-c", host)
	}
	for range 3 {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
}

func TestCentralCoordinatorRecoversInterruptedControlPanelBeforeOtherHostClaims(t *testing.T) {
	c, panel, _ := newCoordinatorFixture(t, "host-control", "host-worker")
	c.Logf = func(string, ...any) {}
	controlTarget := Target{TargetID: "control-panel", HostID: "host-control", ServiceType: "control_panel", DeploymentMode: ModeSystemd}
	controlWorker := c.workers["host-control"]
	controlWorker.targets = map[string]Target{controlTarget.TargetID: controlTarget}
	interrupted := UpdateJob{
		ID: "job-control-recovery", HostID: "host-control", TargetID: controlTarget.TargetID,
		ServiceType: controlTarget.ServiceType, DeploymentMode: controlTarget.DeploymentMode,
		CurrentVersion: "v1.2.2", TargetVersion: "v1.2.3", LeaseGeneration: 1,
	}
	if err := controlWorker.journal.SetActive(&interrupted); err != nil {
		t.Fatal(err)
	}
	recovery := interrupted
	recovery.LeaseToken = "lease-control-recovery"
	recovery.LeaseGeneration = 2
	recovery.ReportSequence = 1
	recovery.RecoveryRequired = true
	panel.jobs["host-control"] = []UpdateJob{recovery}
	panel.jobs["host-worker"] = []UpdateJob{coordinatorJob("host-worker", "target-host-worker", "job-worker")}

	if err := c.recoverInterruptedControlPanel(t.Context()); err != nil {
		t.Fatal(err)
	}
	panel.mu.Lock()
	claimedBeforeWorkers := append([]string(nil), panel.claimHosts...)
	panel.mu.Unlock()
	if len(claimedBeforeWorkers) != 1 || claimedBeforeWorkers[0] != "host-control" {
		t.Fatalf("claims before Control Panel recovery completed = %v", claimedBeforeWorkers)
	}
	if active := controlWorker.journal.Active(); active != nil {
		t.Fatalf("Control Panel recovery cursor was not cleared: %+v", active)
	}

	if err := c.workers["host-worker"].pollOnce(t.Context()); err != nil {
		t.Fatal(err)
	}
	panel.mu.Lock()
	claimedAfterRecovery := append([]string(nil), panel.claimHosts...)
	panel.mu.Unlock()
	if len(claimedAfterRecovery) != 2 || claimedAfterRecovery[1] != "host-worker" {
		t.Fatalf("claims after Control Panel recovery = %v", claimedAfterRecovery)
	}
}

func TestCentralCoordinatorCanceledExclusiveWaitReleasesGateAfterReadersFinish(t *testing.T) {
	c, panel, remote := newCoordinatorFixture(t, "host-a", "host-control")
	controlTarget := Target{TargetID: "control-panel", HostID: "host-control", ServiceType: "control_panel", DeploymentMode: ModeSystemd}
	c.workers["host-control"].targets = map[string]Target{controlTarget.TargetID: controlTarget}
	panel.jobs["host-a"] = []UpdateJob{coordinatorJob("host-a", "target-host-a", "job-a")}
	panel.jobs["host-control"] = []UpdateJob{{
		ID: "job-control-cancel", HostID: "host-control", TargetID: controlTarget.TargetID,
		ServiceType: controlTarget.ServiceType, DeploymentMode: controlTarget.DeploymentMode,
		CurrentVersion: "v1.2.2", TargetVersion: "v1.2.3", LeaseToken: "lease-control-cancel",
		LeaseGeneration: 1, ReportSequence: 1,
	}}
	started := make(chan string, 1)
	releaseA := make(chan struct{})
	remote.stageStarted = started
	remote.stageReleaseByHost = map[string]<-chan struct{}{"host-a": releaseA}

	normalErr := make(chan error, 1)
	go func() { normalErr <- c.workers["host-a"].pollOnce(t.Context()) }()
	if host := waitForValue(t, started); host != "host-a" {
		t.Fatalf("first staged host = %q, want host-a", host)
	}
	controlCtx, cancelControl := context.WithCancel(t.Context())
	controlErr := make(chan error, 1)
	go func() { controlErr <- c.workers["host-control"].pollOnce(controlCtx) }()
	waitForPendingExecutionWriter(t, &c.executionGate)
	cancelControl()
	select {
	case err := <-controlErr:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled Control Panel wait error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("canceled Control Panel wait did not return")
	}

	close(releaseA)
	if err := <-normalErr; err != nil {
		t.Fatal(err)
	}
	waitForExecutionReadGate(t, &c.executionGate)
}

func TestCentralCoordinatorSerializesSameHost(t *testing.T) {
	c, panel, remote := newCoordinatorFixture(t, "host-a")
	panel.jobs["host-a"] = []UpdateJob{
		coordinatorJob("host-a", "target-host-a", "job-a-1"),
		coordinatorJob("host-a", "target-host-a", "job-a-2"),
	}
	started := make(chan string, 2)
	release := make(chan struct{})
	remote.stageStarted = started
	remote.stageRelease = release

	errs := make(chan error, 2)
	go func() { errs <- c.workers["host-a"].pollOnce(t.Context()) }()
	waitForValues(t, started, 1)
	go func() { errs <- c.workers["host-a"].pollOnce(t.Context()) }()
	select {
	case <-started:
		t.Fatal("second same-host stage started before the first completed")
	case <-time.After(100 * time.Millisecond):
	}
	close(release)
	for range 2 {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
	remote.mu.Lock()
	maxStage := remote.maxStage
	remote.mu.Unlock()
	if maxStage != 1 {
		t.Fatalf("same-host max concurrent stages = %d, want 1", maxStage)
	}
}

func TestCentralCoordinatorSkipsUnreachableClaimsButRecoversActiveHost(t *testing.T) {
	c, panel, remote := newCoordinatorFixture(t, "host-a", "host-b")
	setCoordinatorReachability(c, "host-a", "unreachable")
	setCoordinatorReachability(c, "host-b", "unreachable")
	if err := c.workers["host-b"].pollOnce(t.Context()); err != nil {
		t.Fatal(err)
	}
	panel.mu.Lock()
	if len(panel.claimHosts) != 0 {
		t.Fatalf("unreachable idle host was claimed: %v", panel.claimHosts)
	}
	panel.mu.Unlock()

	job := coordinatorJob("host-a", "target-host-a", "job-recovery")
	job.RecoveryRequired = true
	if err := c.workers["host-a"].journal.SetActive(&job); err != nil {
		t.Fatal(err)
	}
	panel.jobs["host-a"] = []UpdateJob{job}
	if err := c.workers["host-a"].pollOnce(t.Context()); err != nil {
		t.Fatal(err)
	}
	remote.mu.Lock()
	stages, applies, reconciles := len(remote.stagePlans), len(remote.applyPlans), len(remote.reconcile)
	remote.mu.Unlock()
	if stages != 0 || applies != 0 || reconciles != 1 {
		t.Fatalf("recovery calls stage=%d apply=%d reconcile=%d", stages, applies, reconciles)
	}
	panel.mu.Lock()
	claims := append([]string(nil), panel.claimHosts...)
	panel.mu.Unlock()
	if len(claims) != 1 || claims[0] != "host-a" {
		t.Fatalf("cross-host active isolation claims = %v", claims)
	}
}

func TestCentralCoordinatorRejectsUnknownCurrentVersionBeforeRemoteExecution(t *testing.T) {
	t.Run("new job is terminally rejected", func(t *testing.T) {
		c, panel, remote := newCoordinatorFixture(t, "host-a")
		job := coordinatorJob("host-a", "target-host-a", "job-unknown-current")
		job.CurrentVersion = ""
		panel.jobs["host-a"] = []UpdateJob{job}
		if err := c.workers["host-a"].pollOnce(t.Context()); err != nil {
			t.Fatal(err)
		}
		remote.mu.Lock()
		calls := len(remote.stagePlans) + len(remote.applyPlans) + len(remote.reconcile)
		remote.mu.Unlock()
		if calls != 0 || c.workers["host-a"].journal.Active() != nil {
			t.Fatalf("unknown-current new job reached remote execution: calls=%d active=%+v", calls, c.workers["host-a"].journal.Active())
		}
	})

	t.Run("recovery remains pending without mutation", func(t *testing.T) {
		c, panel, remote := newCoordinatorFixture(t, "host-a")
		job := coordinatorJob("host-a", "target-host-a", "job-unknown-recovery")
		job.CurrentVersion = ""
		job.RecoveryRequired = true
		if err := c.workers["host-a"].journal.SetActive(&job); err != nil {
			t.Fatal(err)
		}
		panel.jobs["host-a"] = []UpdateJob{job}
		if err := c.workers["host-a"].pollOnce(t.Context()); err == nil {
			t.Fatal("unknown-current recovery unexpectedly completed")
		}
		remote.mu.Lock()
		calls := len(remote.stagePlans) + len(remote.applyPlans) + len(remote.reconcile)
		remote.mu.Unlock()
		active := c.workers["host-a"].journal.Active()
		if calls != 0 || active == nil || active.ID != job.ID {
			t.Fatalf("unknown-current recovery state: calls=%d active=%+v", calls, active)
		}
	})
}

func TestCentralCoordinatorStagesBeforeGrantAndBindsGrant(t *testing.T) {
	events := &coordinatorEventLog{}
	c, panel, remote := newCoordinatorFixture(t, "host-a")
	panel.events = events
	remote.events = events
	panel.jobs["host-a"] = []UpdateJob{coordinatorJob("host-a", "target-host-a", "job-order")}
	if err := c.workers["host-a"].pollOnce(t.Context()); err != nil {
		t.Fatal(err)
	}

	ordered := events.snapshot()
	stageIndex := eventIndex(ordered, "stage:host-a")
	installIndex := eventIndex(ordered, "report:installing")
	grantIndex := eventIndex(ordered, "grant:apply")
	applyIndex := eventIndex(ordered, "apply")
	if !(stageIndex >= 0 && stageIndex < installIndex && installIndex < grantIndex && grantIndex < applyIndex) {
		t.Fatalf("stage/install/grant/apply order = %v", ordered)
	}
	panel.mu.Lock()
	grants := append([]MutationGrantRequest(nil), panel.grants...)
	panel.mu.Unlock()
	remote.mu.Lock()
	staged := remote.stagePlans[0]
	stageToken := remote.stageTokens[0]
	applyGrant := remote.applyGrants[0]
	remote.mu.Unlock()
	if len(grants) != 1 || grants[0].Operation != "apply" || grants[0].HostID != staged.HostID || grants[0].TargetID != staged.TargetID || grants[0].SessionID != staged.SessionID || grants[0].PlanSHA256 != staged.PlanSHA256 {
		t.Fatalf("grant binding = %+v, staged plan = %+v", grants, staged)
	}
	if stageToken != c.Config.GitHubToken || applyGrant != "grant-1" {
		t.Fatalf("credential boundaries stage=%q apply=%q", stageToken, applyGrant)
	}
}

func TestCentralCoordinatorBindsProbedHelperConfigDigestIntoEveryRemotePlan(t *testing.T) {
	c, panel, remote := newCoordinatorFixture(t, "host-a")
	wantConfigSHA256 := "sha256:" + strings.Repeat("f", 64)
	probe := coordinatorProbe(c.Config, "host-a", "v1.2.2")
	probe.ConfigSHA256 = wantConfigSHA256
	remote.probes["host-a"] = probe
	c.probeHost(t.Context(), c.workers["host-a"])
	panel.jobs["host-a"] = []UpdateJob{coordinatorJob("host-a", "target-host-a", "job-config-binding")}

	if err := c.workers["host-a"].pollOnce(t.Context()); err != nil {
		t.Fatal(err)
	}
	remote.mu.Lock()
	staged := append([]RemotePlan(nil), remote.stagePlans...)
	applied := append([]RemotePlan(nil), remote.applyPlans...)
	remote.mu.Unlock()
	if len(staged) != 1 || len(applied) != 1 || staged[0].ConfigSHA256 != wantConfigSHA256 || applied[0].ConfigSHA256 != wantConfigSHA256 {
		t.Fatalf("remote config binding staged=%+v applied=%+v", staged, applied)
	}
	if got, err := MutationPlanSHA256(staged[0].ApplyPlan()); err != nil || got != staged[0].PlanSHA256 || applied[0].PlanSHA256 != staged[0].PlanSHA256 {
		t.Fatalf("remote config plan hash staged=%q applied=%q computed=%q err=%v", staged[0].PlanSHA256, applied[0].PlanSHA256, got, err)
	}
}

func TestCentralCoordinatorDoesNotPersistExecutionSecrets(t *testing.T) {
	c, panel, remote := newCoordinatorFixture(t, "host-a")
	job := coordinatorJob("host-a", "target-host-a", "job-secret-scan")
	panel.jobs["host-a"] = []UpdateJob{job}
	started := make(chan struct{}, 1)
	release := make(chan struct{})
	remote.applyStarted = started
	remote.applyRelease = release
	done := make(chan error, 1)
	go func() { done <- c.workers["host-a"].pollOnce(t.Context()) }()
	released := false
	finished := false
	defer func() {
		if !released {
			close(release)
		}
		if !finished {
			<-done
		}
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for grant-authorized apply")
	}

	for _, forbidden := range []string{job.LeaseToken, c.Config.GitHubToken, "grant-1"} {
		err := filepath.Walk(c.Config.StateDir, func(path string, info os.FileInfo, walkErr error) error {
			if walkErr != nil || info.IsDir() {
				return walkErr
			}
			contents, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			if strings.Contains(string(contents), forbidden) {
				return fmt.Errorf("secret %q persisted in %s", forbidden, path)
			}
			return nil
		})
		if err != nil {
			close(release)
			<-done
			t.Fatal(err)
		}
	}
	if runtime.GOOS != "windows" {
		hostStateDir := coordinatorHostStateDir(c.Config.StateDir, "host-a")
		if info, err := os.Stat(hostStateDir); err != nil || info.Mode().Perm()&0o077 != 0 {
			t.Fatalf("host state directory permissions = %v, %v", info, err)
		}
		if info, err := os.Stat(filepath.Join(hostStateDir, "journal.json")); err != nil || info.Mode().Perm()&0o077 != 0 {
			t.Fatalf("host journal permissions = %v, %v", info, err)
		}
	}
	released = true
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	finished = true
}

func TestCentralCoordinatorUncertainApplyUsesFreshReconcileGrant(t *testing.T) {
	c, panel, remote := newCoordinatorFixture(t, "host-a")
	panel.jobs["host-a"] = []UpdateJob{coordinatorJob("host-a", "target-host-a", "job-uncertain")}
	remote.applyErr = errors.New("SSH session result unknown")
	if err := c.workers["host-a"].pollOnce(t.Context()); err != nil {
		t.Fatal(err)
	}
	panel.mu.Lock()
	grants := append([]MutationGrantRequest(nil), panel.grants...)
	panel.mu.Unlock()
	remote.mu.Lock()
	stages, applies, reconciles := len(remote.stagePlans), len(remote.applyPlans), len(remote.reconcile)
	remote.mu.Unlock()
	if stages != 1 || applies != 1 || reconciles != 1 {
		t.Fatalf("uncertain apply calls stage=%d apply=%d reconcile=%d", stages, applies, reconciles)
	}
	if len(grants) != 2 || grants[0].Operation != "apply" || grants[1].Operation != "reconcile" || grants[0].SessionID == grants[1].SessionID {
		t.Fatalf("fresh reconcile grants = %+v", grants)
	}
}

func TestCentralCoordinatorStageDisconnectRecoversWithoutApply(t *testing.T) {
	c, panel, remote := newCoordinatorFixture(t, "host-a")
	first := coordinatorJob("host-a", "target-host-a", "job-stage-disconnect")
	panel.jobs["host-a"] = []UpdateJob{first}
	remote.stageErr = errors.New("SSH response lost after durable stage")
	if err := c.workers["host-a"].pollOnce(t.Context()); err == nil {
		t.Fatal("uncertain stage unexpectedly became terminal success")
	}
	remote.mu.Lock()
	stages, applies, reconciles := len(remote.stagePlans), len(remote.applyPlans), len(remote.reconcile)
	remote.mu.Unlock()
	panel.mu.Lock()
	grantCount := len(panel.grants)
	panel.mu.Unlock()
	if stages != 1 || applies != 0 || reconciles != 0 || grantCount != 0 {
		t.Fatalf("uncertain stage calls stage=%d apply=%d reconcile=%d grants=%d", stages, applies, reconciles, grantCount)
	}

	recovery := first
	recovery.LeaseToken = "lease-generation-2"
	recovery.LeaseGeneration = 2
	recovery.ReportSequence = 10
	recovery.RecoveryRequired = true
	panel.jobs["host-a"] = []UpdateJob{recovery}
	if err := c.workers["host-a"].pollOnce(t.Context()); err != nil {
		t.Fatal(err)
	}
	remote.mu.Lock()
	stages, applies, reconciles = len(remote.stagePlans), len(remote.applyPlans), len(remote.reconcile)
	remote.mu.Unlock()
	downloader := c.Downloader.(*coordinatorTestDownloader)
	if stages != 1 || applies != 0 || reconciles != 1 || downloader.downloads.Load() != 1 {
		t.Fatalf("stage recovery calls stage=%d apply=%d reconcile=%d downloads=%d", stages, applies, reconciles, downloader.downloads.Load())
	}

	// The host lane is terminally settled and can process a later job.
	panel.jobs["host-a"] = []UpdateJob{coordinatorJob("host-a", "target-host-a", "job-after-stage-recovery")}
	if err := c.workers["host-a"].pollOnce(t.Context()); err != nil {
		t.Fatal(err)
	}
	remote.mu.Lock()
	stages, applies = len(remote.stagePlans), len(remote.applyPlans)
	remote.mu.Unlock()
	if stages != 2 || applies != 1 {
		t.Fatalf("host lane did not progress after recovery: stages=%d applies=%d", stages, applies)
	}
}

func TestCentralCoordinatorDefiniteStageFailureIsTerminal(t *testing.T) {
	c, panel, remote := newCoordinatorFixture(t, "host-a")
	panel.jobs["host-a"] = []UpdateJob{coordinatorJob("host-a", "target-host-a", "job-definite-stage-failure")}
	remote.stageErr = &RemoteExecutionError{Code: "stage_failed", Message: "release staging failed"}
	if err := c.workers["host-a"].pollOnce(t.Context()); err != nil {
		t.Fatal(err)
	}
	if active := c.workers["host-a"].journal.Active(); active != nil {
		t.Fatalf("definite pre-execution stage failure retained active cursor: %+v", active)
	}
	panel.mu.Lock()
	reports := append([]JobReport(nil), panel.reports...)
	panel.mu.Unlock()
	if len(reports) == 0 || reports[len(reports)-1].Status != "failed" || reports[len(reports)-1].Code != "remote_stage_failed" {
		t.Fatalf("definite stage terminal report = %+v", reports)
	}
}

func TestCentralCoordinatorRecoveryWithoutRemoteLedgerTerminates(t *testing.T) {
	c, panel, remote := newCoordinatorFixture(t, "host-a")
	job := coordinatorJob("host-a", "target-host-a", "job-no-remote-ledger")
	job.RecoveryRequired = true
	if err := c.workers["host-a"].journal.SetActive(&job); err != nil {
		t.Fatal(err)
	}
	panel.jobs["host-a"] = []UpdateJob{job}
	remote.reconcileErr = &RemoteExecutionError{Code: "stage_required", Message: "stage is absent"}
	if err := c.workers["host-a"].pollOnce(t.Context()); err != nil {
		t.Fatal(err)
	}
	if active := c.workers["host-a"].journal.Active(); active != nil {
		t.Fatalf("stage-required recovery remained active: %+v", active)
	}
	panel.mu.Lock()
	reports := append([]JobReport(nil), panel.reports...)
	panel.mu.Unlock()
	if len(reports) == 0 || reports[len(reports)-1].Status != "failed" || reports[len(reports)-1].Code != "remote_stage_missing" {
		t.Fatalf("stage-required terminal report = %+v", reports)
	}
}

func TestCentralCoordinatorGenerationTwoRecoveryRebindsStableIntent(t *testing.T) {
	c, panel, remote := newCoordinatorFixture(t, "host-a")
	first := coordinatorJob("host-a", "target-host-a", "job-generation-recovery")
	panel.jobs["host-a"] = []UpdateJob{first}
	remote.applyErr = errors.New("apply SSH result lost")
	remote.reconcileErr = errors.New("same-generation reconcile SSH result lost")
	if err := c.workers["host-a"].pollOnce(t.Context()); err == nil {
		t.Fatal("uncertain apply and reconcile unexpectedly succeeded")
	}

	recovery := first
	recovery.LeaseToken = "lease-generation-2"
	recovery.LeaseGeneration = 2
	recovery.ReportSequence = 12
	recovery.RecoveryRequired = true
	panel.jobs["host-a"] = []UpdateJob{recovery}
	c.statusMu.Lock()
	c.probeConfigSHA256["host-a"] = "sha256:" + strings.Repeat("f", 64)
	c.statusMu.Unlock()
	if err := c.workers["host-a"].pollOnce(t.Context()); err != nil {
		t.Fatal(err)
	}
	remote.mu.Lock()
	stagePlans := append([]RemotePlan(nil), remote.stagePlans...)
	applyPlans := append([]RemotePlan(nil), remote.applyPlans...)
	reconcilePlans := append([]RemotePlan(nil), remote.reconcile...)
	remote.mu.Unlock()
	if len(stagePlans) != 1 || len(applyPlans) != 1 || len(reconcilePlans) != 2 {
		t.Fatalf("generation recovery calls stage=%d apply=%d reconcile=%d", len(stagePlans), len(applyPlans), len(reconcilePlans))
	}
	latest := reconcilePlans[1]
	if latest.LeaseGeneration != 2 || latest.PlanSHA256 == applyPlans[0].PlanSHA256 || latest.SessionID == applyPlans[0].SessionID || latest.ArtifactDigest != applyPlans[0].ArtifactDigest || latest.TargetVersion != applyPlans[0].TargetVersion || latest.ConfigSHA256 != applyPlans[0].ConfigSHA256 || latest.ConfigSHA256 == c.probeConfigSHA256["host-a"] {
		t.Fatalf("fresh generation did not rebind stable intent: apply=%+v recovery=%+v", applyPlans[0], latest)
	}
	downloader := c.Downloader.(*coordinatorTestDownloader)
	if downloader.downloads.Load() != 1 {
		t.Fatalf("recovery re-downloaded instead of using durable intent: %d", downloader.downloads.Load())
	}
}

func TestCentralCoordinatorRestartDropsTokenlessPendingAndClaimsRecovery(t *testing.T) {
	first, _, _ := newCoordinatorFixture(t, "host-a")
	job := coordinatorJob("host-a", "target-host-a", "job-restart-pending")
	worker := first.workers["host-a"]
	if err := worker.journal.SetActive(&job); err != nil {
		t.Fatal(err)
	}
	if _, err := worker.journal.Queue(job.ID, first.Config.NodeID, job.LeaseToken, job.LeaseGeneration, "staging", "", "pending before restart", 50, "", ""); err != nil {
		t.Fatal(err)
	}

	restarted, err := NewCentralCoordinator(first.Config)
	if err != nil {
		t.Fatal(err)
	}
	restartedWorker := restarted.workers["host-a"]
	if len(restartedWorker.journal.Pending()) != 0 {
		t.Fatalf("restart retained a report without an in-memory lease: %+v", restartedWorker.journal.Pending())
	}
	if active := restartedWorker.journal.Active(); active == nil || active.ID != job.ID || active.LeaseToken != "" {
		t.Fatalf("restart did not preserve a secret-free active cursor: %+v", active)
	}
	panel := &coordinatorTestPanel{jobs: map[string][]UpdateJob{}}
	recovery := job
	recovery.LeaseToken = "fresh-recovery-lease"
	recovery.LeaseGeneration = 2
	recovery.ReportSequence = 9
	recovery.RecoveryRequired = true
	panel.jobs["host-a"] = []UpdateJob{recovery}
	remote := &coordinatorTestRemote{probes: map[string]RemoteProbeResult{}, probeErrs: map[string]error{}}
	restarted.Panel = panel
	restarted.Downloader = &coordinatorTestDownloader{}
	restarted.Remote = remote
	restarted.NewSessionID = func() (string, error) { return "session-restart-00000001", nil }
	remote.probes["host-a"] = coordinatorProbe(restarted.Config, "host-a", job.CurrentVersion)
	restarted.probeHost(t.Context(), restartedWorker)
	setCoordinatorReachability(restarted, "host-a", "unreachable")
	if err := restartedWorker.pollOnce(t.Context()); err != nil {
		t.Fatal(err)
	}
	remote.mu.Lock()
	stages, applies, reconciles := len(remote.stagePlans), len(remote.applyPlans), len(remote.reconcile)
	remote.mu.Unlock()
	if stages != 0 || applies != 0 || reconciles != 1 {
		t.Fatalf("restart recovery calls stage=%d apply=%d reconcile=%d", stages, applies, reconciles)
	}
}

func TestCentralCoordinatorHeartbeatAggregatesHostProbeState(t *testing.T) {
	c, panel, remote := newCoordinatorFixture(t, "host-a", "host-b")
	remote.probes["host-a"] = coordinatorProbe(c.Config, "host-a", "v1.2.3")
	remote.probeErrs["host-b"] = errors.New("connect remote SSH host")
	c.probeAll(t.Context())

	panel.heartbeatSeen = make(chan struct{})
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() {
		c.heartbeatLoop(ctx)
		close(done)
	}()
	select {
	case <-panel.heartbeatSeen:
	case <-time.After(time.Second):
		t.Fatal("heartbeat was not reported")
	}
	cancel()
	<-done

	panel.mu.Lock()
	hosts := cloneHostHeartbeats(panel.lastHosts)
	deployed := cloneStringMap(panel.lastDeployed)
	panel.mu.Unlock()
	if hosts["host-a"].Reachability != "reachable" || hosts["host-b"].Reachability != "unreachable" || hosts["host-b"].Code != "ssh_connection_refused" {
		t.Fatalf("aggregated hosts = %+v", hosts)
	}
	if deployed["target-host-a"] != "v1.2.3" {
		t.Fatalf("aggregated deployed versions = %+v", deployed)
	}
}

func TestCoordinatorDockerImageRepoIsFixedByServiceType(t *testing.T) {
	for serviceType, want := range map[string]string{
		"control_panel": "ghcr.io/kome-lab/autostream-docker/control-panel",
		"worker":        "ghcr.io/kome-lab/autostream-docker/worker",
		"discord_bot":   "ghcr.io/kome-lab/autostream-docker/discord-bot",
	} {
		got, err := coordinatorDockerImageRepo(serviceType)
		if err != nil || got != want {
			t.Fatalf("repo(%s) = %q, %v", serviceType, got, err)
		}
	}
	if _, err := coordinatorDockerImageRepo("attacker"); err == nil {
		t.Fatal("unknown service type received an image repository")
	}
}

func TestCoordinatorHostStateDirectoryUsesFilesystemSafeStableHash(t *testing.T) {
	base := t.TempDir()
	first := coordinatorHostStateDir(base, "host:name.with.punctuation")
	second := coordinatorHostStateDir(base, "host:name.with.punctuation")
	name := filepath.Base(first)
	if first != second || len(name) != 64 || strings.Trim(name, "0123456789abcdef") != "" || strings.Contains(name, ":") {
		t.Fatalf("unsafe or unstable host state directory: %q / %q", first, second)
	}
}

func newCoordinatorFixture(t *testing.T, hostIDs ...string) (*CentralCoordinator, *coordinatorTestPanel, *coordinatorTestRemote) {
	t.Helper()
	cfg := Config{
		PanelURL: "https://panel.example.test", NodeID: "central-updater", RuntimeToken: "runtime-token",
		GitHubToken: "github-release-token", StateDir: t.TempDir(),
		API: APIConfig{Host: "127.0.0.1", Port: 9191},
	}
	for _, hostID := range hostIDs {
		cfg.Hosts = append(cfg.Hosts, SSHHost{HostID: hostID, Name: hostID, Arch: "amd64"})
		cfg.Targets = append(cfg.Targets, Target{TargetID: "target-" + hostID, HostID: hostID, ServiceType: "worker", DeploymentMode: ModeSystemd})
	}
	c, err := NewCentralCoordinator(cfg)
	if err != nil {
		t.Fatal(err)
	}
	panel := &coordinatorTestPanel{jobs: make(map[string][]UpdateJob)}
	remote := &coordinatorTestRemote{probes: make(map[string]RemoteProbeResult), probeErrs: make(map[string]error)}
	c.Panel = panel
	c.Downloader = &coordinatorTestDownloader{}
	c.Remote = remote
	c.KeepaliveInterval = time.Hour
	var session atomic.Uint64
	c.NewSessionID = func() (string, error) { return fmt.Sprintf("session-test-%016d", session.Add(1)), nil }
	for _, hostID := range hostIDs {
		setCoordinatorReachability(c, hostID, "reachable")
		c.probeConfigSHA256[hostID] = "sha256:" + strings.Repeat("e", 64)
	}
	return c, panel, remote
}

func coordinatorJob(hostID, targetID, jobID string) UpdateJob {
	return UpdateJob{
		ID: jobID, HostID: hostID, TargetID: targetID, ServiceType: "worker", DeploymentMode: ModeSystemd,
		CurrentVersion: "v1.2.2", TargetVersion: "v1.2.3", LeaseToken: "lease-" + jobID,
		LeaseGeneration: 1, ReportSequence: 1,
	}
}

func coordinatorProbe(cfg Config, hostID, currentVersion string) RemoteProbeResult {
	host, _ := cfg.Host(hostID)
	probe := RemoteProbeResult{ProtocolVersion: RemoteProtocolVersion, HelperVersion: "v1.0.0", HostID: hostID, OS: "linux", Arch: host.Arch, ConfigSHA256: "sha256:" + strings.Repeat("e", 64)}
	for _, target := range cfg.TargetsForHost(hostID) {
		probe.Targets = append(probe.Targets, RemoteProbeTarget{TargetID: target.TargetID, ServiceType: target.ServiceType, DeploymentMode: target.DeploymentMode, CurrentVersion: currentVersion})
	}
	return probe
}

func setCoordinatorReachability(c *CentralCoordinator, hostID, reachability string) {
	c.statusMu.Lock()
	status := c.hostStatus[hostID]
	status.Reachability = reachability
	status.CheckedAt = time.Now().UTC()
	c.hostStatus[hostID] = status
	c.statusMu.Unlock()
}

func waitForValues(t *testing.T, values <-chan string, count int) {
	t.Helper()
	for range count {
		select {
		case <-values:
		case <-time.After(time.Second):
			t.Fatalf("timed out waiting for %d values", count)
		}
	}
}

func waitForValue(t *testing.T, values <-chan string) string {
	t.Helper()
	select {
	case value := <-values:
		return value
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for coordinator stage")
		return ""
	}
}

func waitForPendingExecutionWriter(t *testing.T, gate *sync.RWMutex) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if !gate.TryRLock() {
			return
		}
		gate.RUnlock()
		time.Sleep(time.Millisecond)
	}
	t.Fatal("timed out waiting for exclusive Control Panel update")
}

func waitForExecutionReadGate(t *testing.T, gate *sync.RWMutex) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if gate.TryRLock() {
			gate.RUnlock()
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("canceled exclusive waiter did not release the execution gate")
}

func assertNoCoordinatorStage(t *testing.T, started <-chan string) {
	t.Helper()
	select {
	case host := <-started:
		t.Fatalf("host %q staged while Control Panel barrier should block it", host)
	case <-time.After(50 * time.Millisecond):
	}
}

func waitForCoordinatorReportCount(t *testing.T, panel *coordinatorTestPanel, jobID string, count int) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		panel.mu.Lock()
		seen := 0
		for _, reportedJobID := range panel.reportJobs {
			if reportedJobID == jobID {
				seen++
			}
		}
		panel.mu.Unlock()
		if seen >= count {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("timed out waiting for %d reports for %s", count, jobID)
}

func eventIndex(events []string, wanted string) int {
	for i, event := range events {
		if event == wanted {
			return i
		}
	}
	return -1
}

func cloneStringMap(values map[string]string) map[string]string {
	result := make(map[string]string, len(values))
	for key, value := range values {
		result[key] = value
	}
	return result
}

func cloneHostHeartbeats(values map[string]HostHeartbeat) map[string]HostHeartbeat {
	result := make(map[string]HostHeartbeat, len(values))
	for key, value := range values {
		result[key] = value
	}
	return result
}
