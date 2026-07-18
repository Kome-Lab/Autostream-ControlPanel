package updateagent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"time"
)

type Agent struct {
	Config                   Config
	ConfigPath               string
	Panel                    PanelClient
	Downloader               ReleaseDownloader
	Journal                  *Journal
	Runner                   CommandRunner
	Logf                     func(string, ...any)
	PersistenceRetryInterval time.Duration
	PersistenceRetryLimit    time.Duration
	// RecoveryRetryInterval and ReportAckTimeout are normally left at zero.
	// They are configurable primarily so the recovery crash-fencing behaviour
	// can be exercised without waiting for the production 15s/10m windows.
	RecoveryRetryInterval time.Duration
	ReportAckTimeout      time.Duration
	updating              atomic.Bool
}

func NewAgent(cfg Config, configPath string) (*Agent, error) {
	journal, err := OpenJournal(cfg.StateDir)
	if err != nil {
		return nil, err
	}
	if err := garbageCollectJobDirectories(cfg.StateDir, journal); err != nil {
		return nil, fmt.Errorf("clean stale update job state: %w", err)
	}
	return &Agent{
		Config: cfg, ConfigPath: configPath,
		Panel:      PanelClient{BaseURL: cfg.PanelURL, Token: cfg.RuntimeToken},
		Downloader: ReleaseDownloader{Token: cfg.GitHubToken},
		Journal:    journal, Runner: OSCommandRunner{NewProcessGroup: true}, Logf: log.Printf,
	}, nil
}

func (a *Agent) Run(ctx context.Context) error {
	if a.Logf == nil {
		a.Logf = func(string, ...any) {}
	}
	if interrupted := a.Journal.Active(); interrupted != nil {
		a.Logf("interrupted update %s will be reconciled after server lease recovery", interrupted.ID)
	}
	_ = a.flushReports(ctx)
	statusServer, statusErrors := a.startStatusServer()
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = statusServer.Shutdown(shutdownCtx)
	}()
	heartbeatDone := make(chan struct{})
	go func() {
		defer close(heartbeatDone)
		heartbeatTicker := time.NewTicker(a.Config.HeartbeatInterval())
		defer heartbeatTicker.Stop()
		for {
			status := "online"
			if a.updating.Load() {
				status = "updating"
			}
			deployed := a.deployedVersions()
			if err := a.Panel.Register(ctx, a.Config, deployed); err != nil {
				a.Logf("updater register: %v", err)
			}
			if err := a.Panel.Heartbeat(ctx, a.Config, status, deployed); err != nil {
				a.Logf("updater heartbeat: %v", err)
			}
			select {
			case <-ctx.Done():
				return
			case <-heartbeatTicker.C:
			}
		}
	}()
	pollTicker := time.NewTicker(a.Config.PollInterval())
	defer pollTicker.Stop()
	for {
		if err := a.pollOnce(ctx); err != nil {
			if !errors.Is(err, context.Canceled) {
				a.Logf("updater poll: %v", err)
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err := <-statusErrors:
			return err
		case <-heartbeatDone:
			return ctx.Err()
		case <-pollTicker.C:
		}
	}
}

func (a *Agent) pollOnce(ctx context.Context) error {
	if err := a.flushReports(ctx); err != nil {
		return err
	}
	active := a.Journal.Active()
	activeID := ""
	if active != nil {
		activeID = active.ID
	}
	job, clearActive, err := a.Panel.Claim(ctx, a.Config.NodeID, activeID)
	if err != nil || job == nil {
		if err == nil && clearActive {
			return a.Journal.ClearActive()
		}
		return err
	}
	if active != nil && (active.ID != job.ID || !job.RecoveryRequired) {
		return fmt.Errorf("refusing claim %s while interrupted job %s awaits recovery", job.ID, active.ID)
	}
	return a.processJob(ctx, *job)
}

func (a *Agent) processJob(ctx context.Context, job UpdateJob) error {
	a.updating.Store(true)
	defer a.updating.Store(false)
	if err := a.Journal.SetActive(&job); err != nil {
		return err
	}
	terminal := func(status, code, message string, result ApplyResult) error {
		_, queueErr := a.emit(ctx, job, status, code, message, 100, result.ArtifactDigest, result.PreviousDigest)
		if queueErr != nil {
			return queueErr
		}
		clearErr := a.Journal.ClearActive()
		return clearErr
	}
	reportFailure := func(err error) error {
		if errors.Is(err, ErrLeaseLost) || IsFatalReportError(err) {
			return err
		}
		return terminal("failed", "report_rejected", safeErrorMessage(err), ApplyResult{})
	}
	target, ok := a.Config.Target(job.TargetID)
	version := job.EffectiveVersion()
	if !ok || target.ServiceType != job.EffectiveType() || target.DeploymentMode != job.DeploymentMode {
		return terminal("failed", "target_mismatch", "job target does not match this updater's fixed configuration", ApplyResult{})
	}
	if !versionPattern.MatchString(version) || !identifierPattern.MatchString(job.ID) {
		return terminal("failed", "invalid_job", "job contains an invalid ID or target version", ApplyResult{})
	}
	if job.RecoveryRequired {
		return a.processRecovery(ctx, job, target, version)
	}
	if _, err := a.emit(ctx, job, "claimed", "", "update job claimed and fixed target validated", 5, "", ""); reportStopsExecution(err) {
		return reportFailure(err)
	}
	jobDir, err := ensurePrivateJobDirectory(a.Config.StateDir, job.ID)
	if err != nil {
		return terminal("failed", "stage_failed", "could not create private staging directory", ApplyResult{})
	}
	plan := ApplyPlan{JobID: job.ID, TargetID: target.TargetID, ServiceType: target.ServiceType, DeploymentMode: target.DeploymentMode, TargetVersion: version, CurrentVersion: strings.TrimSpace(job.CurrentVersion), LeaseToken: job.LeaseToken, LeaseGeneration: job.LeaseGeneration}
	if target.DeploymentMode == ModeSystemd {
		if _, err := a.emit(ctx, job, "downloading", "", "downloading signed release inputs", 20, "", ""); reportStopsExecution(err) {
			return reportFailure(err)
		}
		artifactDir := filepath.Join(jobDir, "artifact")
		downloaded, err := a.Downloader.Download(ctx, target.ServiceType, version, runtime.GOARCH, artifactDir)
		if err != nil {
			return terminal("failed", "artifact_verification_failed", safeErrorMessage(err), ApplyResult{})
		}
		plan.StageDir = downloaded.RootDir
		plan.ArtifactDigest = downloaded.SHA256
		reportedDigest := "sha256:" + downloaded.SHA256
		if _, err := a.emit(ctx, job, "verifying", "", "release SHA256 and archive contents verified", 40, reportedDigest, ""); reportStopsExecution(err) {
			return reportFailure(err)
		}
		if _, err := a.emit(ctx, job, "staging", "", "release artifact staged for local helper", 50, reportedDigest, ""); reportStopsExecution(err) {
			return reportFailure(err)
		}
	} else {
		if _, err := a.emit(ctx, job, "staging", "", "fixed compose target prepared", 50, "", ""); reportStopsExecution(err) {
			return reportFailure(err)
		}
	}
	planPath := filepath.Join(jobDir, "plan.json")
	if err := writePrivateJSON(planPath, plan); err != nil {
		return terminal("failed", "stage_failed", "could not persist apply plan", ApplyResult{})
	}
	if _, err := a.emit(ctx, job, "installing", "", "privileged helper is applying the fixed target", 65, normalizeDigest(plan.ArtifactDigest), ""); err != nil {
		if reportStopsExecution(err) {
			return reportFailure(err)
		}
		return terminal("failed", "lease_confirmation_failed", "privileged apply was not started because the installing report was not acknowledged", ApplyResult{})
	}
	result, err := a.invokeHelper(ctx, job, planPath)
	if err != nil {
		if errors.Is(err, ErrHelperAuthorizationRejected) {
			return terminal("failed", "authorization_rejected", safeErrorMessage(err), ApplyResult{})
		}
		if reportStopsExecution(err) {
			return err
		}
		a.Logf("privileged apply did not reach a terminal result; reconciling durable local state: %v", err)
		reconciled, reconcileErr := a.invokeHelperMode(ctx, job, planPath, "reconcile", "installing")
		if reconcileErr != nil {
			// Do not terminal-fail a job while a root checkpoint may still
			// describe an unknown cutover. Its lease will be reclaimed with
			// recovery_required and reconciliation will be retried.
			return fmt.Errorf("apply failed and local reconciliation remains pending: %w", reconcileErr)
		}
		result = reconciled
	}
	if _, err := a.emit(ctx, job, "health_checking", "", "helper completed local health and expected-version checks", 90, result.ArtifactDigest, result.PreviousDigest); reportStopsExecution(err) {
		return reportFailure(err)
	}
	if result.RolledBack || result.Status == "rolled_back" {
		if _, err := a.emit(ctx, job, "rolling_back", "", "post-update checks failed; previous release was restored", 95, result.ArtifactDigest, result.PreviousDigest); reportStopsExecution(err) {
			return reportFailure(err)
		}
		return terminal("rolled_back", "post_update_verification_failed", result.Message, result)
	}
	if target.DeploymentMode == ModeDocker {
		if err := a.persistDeployedVersion(ctx, target.TargetID, version); err != nil {
			return err
		}
	}
	if err := terminal("succeeded", "", "target updated and verified", result); err != nil {
		return err
	}
	return nil
}

func (a *Agent) processRecovery(ctx context.Context, job UpdateJob, target Target, version string) error {
	terminal := func(status, code, message string, result ApplyResult) error {
		_, queueErr := a.emit(ctx, job, status, code, message, 100, result.ArtifactDigest, result.PreviousDigest)
		if queueErr != nil {
			return queueErr
		}
		clearErr := a.Journal.ClearActive()
		return clearErr
	}
	plan := ApplyPlan{JobID: job.ID, TargetID: target.TargetID, ServiceType: target.ServiceType, DeploymentMode: target.DeploymentMode, TargetVersion: version, CurrentVersion: strings.TrimSpace(job.CurrentVersion), LeaseToken: job.LeaseToken, LeaseGeneration: job.LeaseGeneration}
	jobDir := jobDirectory(a.Config.StateDir, job.ID)
	planPath := filepath.Join(jobDir, "plan.json")
	if _, err := a.emit(ctx, job, "reconciling", "", "inspecting interrupted local update state without reapplying", 99, "", ""); err != nil {
		if errors.Is(err, ErrLeaseLost) || IsFatalReportError(err) {
			return err
		}
		return err
	}
	lastAcknowledged := time.Now()
	var result ApplyResult
	var err error
	for {
		if ensured, dirErr := ensurePrivateJobDirectory(a.Config.StateDir, job.ID); dirErr == nil {
			jobDir = ensured
			planPath = filepath.Join(jobDir, "plan.json")
			err = writePrivateJSON(planPath, plan)
			if err == nil {
				result, err = a.invokeHelperMode(ctx, job, planPath, "reconcile", "reconciling")
			}
			if err == nil {
				break
			}
			if errors.Is(err, ErrHelperAuthorizationRejected) {
				return terminal("failed", "authorization_rejected", safeErrorMessage(err), ApplyResult{})
			}
			if errors.Is(err, ErrLeaseLost) || IsFatalReportError(err) {
				return err
			}
			a.Logf("reconciliation remains pending and will retry under the same lease: %v", err)
		} else {
			a.Logf("reconciliation staging remains pending and will retry: %v", err)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(a.recoveryRetryInterval()):
		}
		var fenceErr error
		if len(a.Journal.Pending()) > 0 {
			fenceErr = a.flushReports(ctx)
		} else {
			_, fenceErr = a.emit(ctx, job, "reconciling", "", "local reconciliation is still pending and will retry", 99, "", "")
		}
		for fenceErr != nil {
			if errors.Is(fenceErr, ErrLeaseLost) || IsFatalReportError(fenceErr) || time.Since(lastAcknowledged) >= a.reportAckTimeout() {
				return fenceErr
			}
			// Do not start another privileged helper until this exact
			// keepalive has been acknowledged. Pending remains bounded at one.
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(a.recoveryRetryInterval()):
			}
			fenceErr = a.flushReports(ctx)
		}
		lastAcknowledged = time.Now()
	}
	if result.Status == "rolled_back" || result.RolledBack {
		return terminal("rolled_back", "", "interrupted update resolved to the verified previous release", result)
	}
	if target.DeploymentMode == ModeDocker {
		if err := a.persistDeployedVersion(ctx, target.TargetID, version); err != nil {
			return err
		}
	}
	return terminal("succeeded", "", "interrupted update resolved to the requested release", result)
}

func (a *Agent) persistDeployedVersion(ctx context.Context, targetID, version string) error {
	retryInterval := a.PersistenceRetryInterval
	if retryInterval <= 0 {
		retryInterval = 5 * time.Second
	}
	retryLimit := a.PersistenceRetryLimit
	if retryLimit <= 0 {
		retryLimit = 10 * time.Minute
	}
	deadline := time.Now().Add(retryLimit)
	var lastErr error
	for {
		if err := a.Journal.MarkDeployed(targetID, version); err == nil {
			return nil
		} else {
			lastErr = err
			a.Logf("verified Docker bundle is running but durable version persistence will retry: %v", err)
		}
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return fmt.Errorf("persist verified Docker bundle before recovery retry: %w", lastErr)
		}
		wait := retryInterval
		if wait > remaining {
			wait = remaining
		}
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}

func (a *Agent) invokeHelper(ctx context.Context, job UpdateJob, planPath string) (ApplyResult, error) {
	return a.invokeHelperMode(ctx, job, planPath, "apply", "installing")
}

func (a *Agent) invokeHelperMode(ctx context.Context, job UpdateJob, planPath, mode, keepaliveStatus string) (ApplyResult, error) {
	argv := append([]string(nil), a.Config.HelperArgv...)
	argv = append(argv, "helper", mode, "--config", a.ConfigPath, "--plan", planPath)
	if len(argv) == 0 {
		return ApplyResult{}, errors.New("helper command is not configured")
	}
	helperCtx, cancel := context.WithTimeout(ctx, 30*time.Minute)
	defer cancel()
	type helperResult struct {
		output string
		err    error
	}
	completed := make(chan helperResult, 1)
	go func() {
		output, err := a.Runner.Run(helperCtx, "", nil, argv[0], argv[1:]...)
		completed <- helperResult{output: output, err: err}
	}()
	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()
	lastAcknowledged := time.Now()
	var output string
	for {
		select {
		case <-helperCtx.Done():
			cancel()
			select {
			case <-completed:
				return ApplyResult{}, helperCtx.Err()
			case <-time.After(135 * time.Second):
				return ApplyResult{}, errors.New("helper process tree did not terminate after cancellation fence")
			}
		case done := <-completed:
			if done.err != nil {
				detail := sanitizeHelperFailure(done.output)
				if helperAuthorizationFailure(detail) {
					return ApplyResult{}, fmt.Errorf("%w: %s", ErrHelperAuthorizationRejected, detail)
				}
				if detail != "" {
					return ApplyResult{}, fmt.Errorf("helper failed: %w (%s)", done.err, detail)
				}
				return ApplyResult{}, fmt.Errorf("helper failed: %w", done.err)
			}
			output = done.output
			goto decode
		case <-ticker.C:
			message := "privileged helper is still applying the fixed target"
			if mode == "reconcile" && keepaliveStatus == "reconciling" {
				message = "privileged helper is still reconciling fixed local state"
			}
			progress := 70
			if mode == "reconcile" && keepaliveStatus == "reconciling" {
				progress = 99
			} else if mode == "reconcile" {
				progress = 89
			}
			if _, err := a.emit(ctx, job, keepaliveStatus, "", message, progress, "", ""); err != nil {
				if reportStopsExecution(err) || time.Since(lastAcknowledged) >= a.reportAckTimeout() {
					cancel()
					select {
					case <-completed:
						return ApplyResult{}, err
					case <-time.After(135 * time.Second):
						return ApplyResult{}, errors.New("helper process tree did not terminate after report lease loss")
					}
				}
			} else {
				lastAcknowledged = time.Now()
			}
		}
	}

decode:
	var result ApplyResult
	if err := json.Unmarshal([]byte(strings.TrimSpace(output)), &result); err != nil {
		return ApplyResult{}, errors.New("helper returned an invalid result")
	}
	if result.Status != "succeeded" && result.Status != "rolled_back" {
		return result, errors.New("helper did not report successful completion")
	}
	return result, nil
}

var ErrHelperAuthorizationRejected = errors.New("privileged helper authorization was rejected")

func sanitizeHelperFailure(output string) string {
	output = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return ' '
		}
		return r
	}, output)
	output = strings.Join(strings.Fields(output), " ")
	if len(output) > 500 {
		output = output[:500]
	}
	return output
}

func helperAuthorizationFailure(detail string) bool {
	lower := strings.ToLower(detail)
	return strings.Contains(lower, "panel returned http 401") || strings.Contains(lower, "panel returned http 403") || strings.Contains(lower, "panel returned http 409") || strings.Contains(lower, "missing its execution lease authorization")
}

func reportStopsExecution(err error) bool {
	return err != nil && (errors.Is(err, ErrLeaseLost) || IsFatalReportError(err))
}

func (a *Agent) recoveryRetryInterval() time.Duration {
	if a.RecoveryRetryInterval > 0 {
		return a.RecoveryRetryInterval
	}
	return 15 * time.Second
}

func (a *Agent) reportAckTimeout() time.Duration {
	if a.ReportAckTimeout > 0 {
		return a.ReportAckTimeout
	}
	return 10 * time.Minute
}

func (a *Agent) emit(ctx context.Context, job UpdateJob, status, code, message string, progress int, artifact, previous string) (JobReport, error) {
	artifact = canonicalReportDigest(artifact)
	previous = canonicalReportDigest(previous)
	report, err := a.Journal.Queue(job.ID, a.Config.NodeID, job.LeaseToken, job.LeaseGeneration, status, code, message, progress, artifact, previous)
	if err != nil {
		return report, err
	}
	if err := a.flushReports(ctx); err != nil {
		a.Logf("update report queued for retry: %v", err)
		if IsFatalReportError(err) {
			pending := a.Journal.Pending()
			if len(pending) > 0 {
				_ = a.Journal.Ack(pending[0].JobID, pending[0].Report.Sequence)
			}
		}
		return report, err
	}
	return report, nil
}

func canonicalReportDigest(value string) string {
	value = normalizeDigest(value)
	if value != "" && !digestPattern.MatchString(value) {
		return ""
	}
	return value
}

func normalizeDigest(digest string) string {
	digest = strings.TrimSpace(strings.ToLower(digest))
	if len(digest) == 64 && !strings.HasPrefix(digest, "sha256:") {
		return "sha256:" + digest
	}
	return digest
}

func (a *Agent) flushReports(ctx context.Context) error {
	for {
		pending := a.Journal.Pending()
		if len(pending) == 0 {
			return nil
		}
		item := pending[0]
		if err := a.Panel.Report(ctx, item.JobID, item.Report); err != nil {
			if IsPermanentReportError(err) {
				a.Logf("dropping permanently rejected stale update report: %v", err)
				if ackErr := a.Journal.Ack(item.JobID, item.Report.Sequence); ackErr != nil {
					return ackErr
				}
				if isTerminalUpdateStatus(item.Report.Status) {
					if cleanupErr := cleanupJobDirectory(a.Config.StateDir, item.JobID); cleanupErr != nil {
						return cleanupErr
					}
				}
				if a.updating.Load() {
					return ErrLeaseLost
				}
				continue
			}
			return err
		}
		if err := a.Journal.Ack(item.JobID, item.Report.Sequence); err != nil {
			return err
		}
		if isTerminalUpdateStatus(item.Report.Status) {
			if err := cleanupJobDirectory(a.Config.StateDir, item.JobID); err != nil {
				return fmt.Errorf("clean acknowledged update job state: %w", err)
			}
		}
	}
}

func isTerminalUpdateStatus(status string) bool {
	return status == "succeeded" || status == "rolled_back" || status == "failed"
}

var ErrLeaseLost = errors.New("system update execution lease was lost")

func (a *Agent) deployedVersions() map[string]string {
	deployed := map[string]string{}
	for _, target := range a.Config.Targets {
		if target.DeploymentMode == ModeDocker && target.Docker != nil && strings.TrimSpace(target.Docker.CurrentVersion) != "" {
			deployed[target.TargetID] = strings.TrimSpace(target.Docker.CurrentVersion)
		}
	}
	for target, bundleVersion := range a.Journal.DeployedVersions() {
		deployed[target] = bundleVersion
	}
	return deployed
}

func writePrivateJSON(path string, value any) error {
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	encodeErr := json.NewEncoder(f).Encode(value)
	syncErr := f.Sync()
	closeErr := f.Close()
	if err := firstError(encodeErr, syncErr, closeErr); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return syncDirectory(filepath.Dir(path))
}

func safeErrorMessage(err error) string {
	if err == nil {
		return ""
	}
	message := strings.ReplaceAll(err.Error(), "\n", " ")
	if len(message) > 500 {
		message = message[:500]
	}
	return message
}
