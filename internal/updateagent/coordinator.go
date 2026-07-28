package updateagent

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/example/autostream-control-panel/internal/version"
)

// CoordinatorPanel is the narrow Control Panel boundary used by the central
// updater. Keeping it explicit makes it possible to exercise host scheduling,
// fencing, and recovery without a live Control Panel.
type CoordinatorPanel interface {
	RegisterWithHosts(context.Context, Config, map[string]string, map[string]HostHeartbeat) error
	HeartbeatWithHosts(context.Context, Config, string, map[string]string, map[string]HostHeartbeat) error
	ClaimHost(context.Context, string, string, string) (*UpdateJob, bool, error)
	Report(context.Context, string, JobReport) error
	IssueMutationGrant(context.Context, string, MutationGrantRequest) (MutationGrant, error)
}

// CoordinatorDownloader verifies release metadata and artifacts on the
// central updater before the same immutable intent is sent to a host.
type CoordinatorDownloader interface {
	Download(context.Context, string, string, string, string) (DownloadedArtifact, error)
	ResolveDockerReleaseForArch(context.Context, string, string, string, string, string, string) (ResolvedDockerRelease, error)
}

type CentralCoordinator struct {
	Config     Config
	Panel      CoordinatorPanel
	Downloader CoordinatorDownloader
	// NewDownloader creates a job-scoped downloader from the one-use release
	// token returned by claim. The resulting value is never retained in durable
	// state. Downloader remains for legacy configs and test injection.
	NewDownloader func(RemoteSecret) CoordinatorDownloader
	Remote        RemoteExecutor
	Bootstrap     *BootstrapController
	Logf          func(string, ...any)

	ProbeTimeout      time.Duration
	MutationTimeout   time.Duration
	KeepaliveInterval time.Duration
	ReportAckTimeout  time.Duration
	NewSessionID      func() (string, error)

	workers map[string]*centralHostWorker

	// executionGate lets updates for different hosts run in parallel while
	// making a Control Panel update globally exclusive. The exclusive path
	// prevents other workers from claiming, granting, or reporting jobs while
	// the API they depend on may be restarting.
	executionGate sync.RWMutex
	replacementMu sync.Mutex

	statusMu          sync.RWMutex
	hostStatus        map[string]HostHeartbeat
	probeVersions     map[string]map[string]string
	probeConfigSHA256 map[string]string
	updating          atomic.Int64
	draining          atomic.Bool
	policyActivation  chan struct{}
	policyActivate    sync.Once
	now               func() time.Time
}

type centralHostWorker struct {
	coordinator *CentralCoordinator
	host        SSHHost
	targets     map[string]Target
	journal     *Journal
	mu          sync.Mutex
}

// Bootstrap credentials expire independently of ordinary update jobs. Keep
// their claim cadence short and fixed so a large poll_interval_seconds cannot
// consume most of the one-time credential lifetime.
const bootstrapPollCadence = 15 * time.Second

var (
	ErrHostMaintenanceUnknownHost = errors.New("host maintenance target is not configured")
	ErrHostMaintenanceDraining    = errors.New("host maintenance is blocked by policy replacement")
	ErrHostMaintenanceActiveJob   = errors.New("host maintenance is blocked by an active update job")
)

// coordinatorIntent is the durable, secret-free update identity used to
// rebind an interrupted operation to a fresh lease generation and session. It
// intentionally excludes lease tokens, mutation grants, release credentials,
// local staging paths, session IDs, and authorization hashes.
type coordinatorIntent struct {
	JobID                  string `json:"job_id"`
	HostID                 string `json:"host_id"`
	TargetID               string `json:"target_id"`
	ServiceType            string `json:"service_type"`
	DeploymentMode         string `json:"deployment_mode"`
	TargetVersion          string `json:"target_version"`
	CurrentVersion         string `json:"current_version"`
	ConfigSHA256           string `json:"config_sha256"`
	ArtifactDigest         string `json:"artifact_digest"`
	ExpectedVersion        string `json:"expected_version,omitempty"`
	ExpectedImageDigest    string `json:"expected_image_digest,omitempty"`
	ExpectedPlatformDigest string `json:"expected_platform_digest,omitempty"`
}

func NewCentralCoordinator(cfg Config) (*CentralCoordinator, error) {
	if len(cfg.Hosts) == 0 {
		return nil, errors.New("central coordinator requires at least one SSH host")
	}
	hostsRoot := filepath.Join(cfg.StateDir, "hosts")
	if err := os.MkdirAll(hostsRoot, 0o700); err != nil {
		return nil, fmt.Errorf("create central host state root: %w", err)
	}
	hostsRootInfo, err := os.Lstat(hostsRoot)
	if err != nil || !privateJobDirectoryInfo(hostsRootInfo) {
		return nil, errors.New("central host state root must be a private non-symlink directory")
	}
	c := &CentralCoordinator{
		Config:     cfg,
		Panel:      PanelClient{BaseURL: cfg.PanelURL, Token: cfg.RuntimeToken},
		Downloader: ReleaseDownloader{Token: cfg.GitHubToken},
		NewDownloader: func(token RemoteSecret) CoordinatorDownloader {
			return ReleaseDownloader{Token: token.Reveal()}
		},
		Remote:            SSHRemoteExecutor{},
		Bootstrap:         NewBootstrapController(),
		Logf:              log.Printf,
		workers:           make(map[string]*centralHostWorker, len(cfg.Hosts)),
		hostStatus:        make(map[string]HostHeartbeat, len(cfg.Hosts)),
		probeVersions:     make(map[string]map[string]string, len(cfg.Hosts)),
		probeConfigSHA256: make(map[string]string, len(cfg.Hosts)),
		policyActivation:  make(chan struct{}),
		now:               time.Now,
	}
	for _, host := range cfg.Hosts {
		stateDir := coordinatorHostStateDir(cfg.StateDir, host.HostID)
		if !pathWithin(hostsRoot, stateDir) {
			return nil, fmt.Errorf("state path for host %s is invalid", host.HostID)
		}
		stateInfo, statErr := os.Lstat(stateDir)
		if errors.Is(statErr, os.ErrNotExist) {
			if err := os.Mkdir(stateDir, 0o700); err != nil {
				return nil, fmt.Errorf("create state directory for host %s: %w", host.HostID, err)
			}
			stateInfo, statErr = os.Lstat(stateDir)
		}
		if statErr != nil || !privateJobDirectoryInfo(stateInfo) {
			return nil, fmt.Errorf("state directory for host %s must be private and non-symlink", host.HostID)
		}
		journal, err := OpenJournal(stateDir)
		if err != nil {
			return nil, fmt.Errorf("open journal for host %s: %w", host.HostID, err)
		}
		if err := garbageCollectJobDirectories(stateDir, journal); err != nil {
			return nil, fmt.Errorf("clean stale job state for host %s: %w", host.HostID, err)
		}
		targets := make(map[string]Target)
		for _, target := range cfg.TargetsForHost(host.HostID) {
			targets[target.TargetID] = target
		}
		c.workers[host.HostID] = &centralHostWorker{coordinator: c, host: host, targets: targets, journal: journal}
		c.hostStatus[host.HostID] = HostHeartbeat{Name: host.Name, Reachability: "unknown", Arch: host.Arch}
	}
	return c, nil
}

func (c *CentralCoordinator) Run(ctx context.Context) error {
	done, err := c.StartManaged(ctx)
	if err != nil {
		return err
	}
	c.ActivatePolicy()
	return <-done
}

// StartManaged completes every startup operation that can fail before
// returning. In particular, the status listener and TLS identity are opened
// synchronously and interrupted Control Panel work is reconciled. A supervisor
// can therefore persist and announce a new policy only after this method
// succeeds.
func (c *CentralCoordinator) StartManaged(ctx context.Context) (<-chan error, error) {
	if c.Logf == nil {
		c.Logf = func(string, ...any) {}
	}
	if c.Panel == nil || c.Downloader == nil || c.Remote == nil {
		return nil, errors.New("central coordinator dependencies are incomplete")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	// A pending managed candidate remains closed until its supervisor durably
	// commits the desired policy. Legacy and already-applied runtimes start
	// immediately and preserve their existing first-poll behavior.
	if c.Config.PolicyStatus == PolicyStatusPending {
		c.draining.Store(true)
	} else {
		c.ActivatePolicy()
	}
	runCtx, cancelRun := context.WithCancel(ctx)
	for _, worker := range c.workers {
		if interrupted := worker.journal.Active(); interrupted != nil {
			c.Logf("host %s interrupted update %s will only be reconciled", worker.host.HostID, interrupted.ID)
		}
	}

	server, listener, err := c.prepareStatusServer()
	if err != nil {
		cancelRun()
		return nil, err
	}
	// A restarted coordinator must resolve an interrupted Control Panel update
	// before any other host worker can claim work. The active journal is the
	// durable part of the global barrier; recovery only reconciles host state and
	// never reapplies the interrupted mutation.
	if err := c.recoverInterruptedControlPanel(runCtx); err != nil {
		_ = listener.Close()
		cancelRun()
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("recover interrupted Control Panel update: %w", err)
	}
	if err := ctx.Err(); err != nil {
		_ = listener.Close()
		cancelRun()
		return nil, err
	}

	done := make(chan error, 1)
	go func() {
		defer close(done)
		defer cancelRun()
		serverErrors := make(chan error, 1)
		serverStopped := make(chan struct{})
		go func() {
			defer close(serverStopped)
			err := server.Serve(listener)
			if err != nil && !errors.Is(err, http.ErrServerClosed) {
				serverErrors <- err
			}
			close(serverErrors)
		}()

		var wg sync.WaitGroup
		// Reachability is runtime status, not an activation gate. Probes run
		// concurrently after listener/recovery readiness, and workers will not
		// claim from a host until its bounded probe succeeds.
		wg.Add(1)
		go func() {
			defer wg.Done()
			c.probeAll(runCtx)
		}()
		for _, worker := range c.workers {
			worker := worker
			wg.Add(1)
			go func() {
				defer wg.Done()
				if !c.waitForPolicyActivation(runCtx) {
					return
				}
				worker.run(runCtx)
			}()
		}
		if c.Bootstrap != nil {
			wg.Add(1)
			go func() {
				defer wg.Done()
				if !c.waitForPolicyActivation(runCtx) {
					return
				}
				c.bootstrapLoop(runCtx)
			}()
		}
		heartbeatDone := make(chan struct{})
		go func() {
			defer close(heartbeatDone)
			c.heartbeatLoop(runCtx)
		}()

		var runErr error
		select {
		case <-ctx.Done():
			runErr = ctx.Err()
		case err, ok := <-serverErrors:
			if !ok || err == nil {
				runErr = errors.New("central updater status server stopped")
			} else {
				runErr = err
			}
		case <-heartbeatDone:
			if ctx.Err() != nil {
				runErr = ctx.Err()
			} else {
				runErr = errors.New("central updater heartbeat loop stopped")
			}
		}
		cancelRun()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		_ = server.Shutdown(shutdownCtx)
		cancel()
		<-serverStopped
		wg.Wait()
		<-heartbeatDone
		done <- runErr
	}()
	return done, nil
}

func (c *CentralCoordinator) bootstrapLoop(ctx context.Context) {
	ticker := time.NewTicker(bootstrapPollCadence)
	defer ticker.Stop()
	for {
		cfg := c.bootstrapConfigSnapshot()
		if !c.draining.Load() &&
			cfg.PolicyRevision > 0 &&
			strings.TrimSpace(cfg.BootstrapEncryptionPublicKey) != "" {
			controller := c.Bootstrap
			if controller.Remote == nil {
				controllerCopy := *controller
				controllerCopy.Remote = c.Remote
				controller = &controllerCopy
			}
			if err := controller.PollOnce(ctx, cfg, c); err != nil && ctx.Err() == nil {
				// Crypto and transport causes may be secret-bearing. This fixed
				// message is the entire runtime logging boundary.
				c.Logf("managed updater bootstrap poll failed")
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (c *CentralCoordinator) bootstrapConfigSnapshot() Config {
	c.statusMu.RLock()
	defer c.statusMu.RUnlock()
	cfg := c.Config
	cfg.SSHClientPublicKeys = cloneCapabilityStringMap(c.Config.SSHClientPublicKeys)
	cfg.SSHClientKeyFingerprints = cloneCapabilityStringMap(c.Config.SSHClientKeyFingerprints)
	cfg.Hosts = append([]SSHHost(nil), c.Config.Hosts...)
	cfg.Targets = append([]Target(nil), c.Config.Targets...)
	return cfg
}

func (w *centralHostWorker) run(ctx context.Context) {
	ticker := time.NewTicker(w.coordinator.Config.PollInterval())
	defer ticker.Stop()
	for {
		if err := w.pollOnce(ctx); err != nil && !errors.Is(err, context.Canceled) {
			w.coordinator.Logf("updater host %s poll: %v", w.host.HostID, err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (c *CentralCoordinator) heartbeatLoop(ctx context.Context) {
	ticker := time.NewTicker(c.Config.HeartbeatInterval())
	defer ticker.Stop()
	for {
		cfg, deployed, hosts := c.heartbeatSnapshot()
		status := "online"
		if c.updating.Load() > 0 {
			status = "updating"
		}
		if err := c.Panel.RegisterWithHosts(ctx, cfg, deployed, hosts); err != nil && ctx.Err() == nil {
			c.Logf("central updater register: %v", err)
		}
		if err := c.Panel.HeartbeatWithHosts(ctx, cfg, status, deployed, hosts); err != nil && ctx.Err() == nil {
			c.Logf("central updater heartbeat: %v", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			c.probeAll(ctx)
		}
	}
}

func (c *CentralCoordinator) recoveryHeartbeat(ctx context.Context) error {
	cfg, deployed, hosts := c.heartbeatSnapshot()
	if err := c.Panel.HeartbeatWithHosts(ctx, cfg, "updating", deployed, hosts); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return err
	}
	return nil
}

// ActivatePolicy opens fresh job claims after a managed supervisor has
// durably committed the policy backing this ready runtime. Run calls it
// immediately for the legacy non-supervised lifecycle.
func (c *CentralCoordinator) ActivatePolicy() {
	c.draining.Store(false)
	c.policyActivate.Do(func() {
		if c.policyActivation != nil {
			close(c.policyActivation)
		}
	})
}

func (c *CentralCoordinator) waitForPolicyActivation(ctx context.Context) bool {
	select {
	case <-ctx.Done():
		return false
	case <-c.policyActivation:
		return true
	}
}

func (c *CentralCoordinator) probeAll(ctx context.Context) {
	var wg sync.WaitGroup
	for _, worker := range c.workers {
		worker := worker
		wg.Add(1)
		go func() {
			defer wg.Done()
			c.probeHost(ctx, worker)
		}()
	}
	wg.Wait()
}

func (c *CentralCoordinator) recoverInterruptedControlPanel(ctx context.Context) error {
	for {
		worker, active := c.interruptedControlPanelWorker()
		if worker == nil {
			return nil
		}
		c.Logf("host %s interrupted Control Panel update %s is blocking all other host claims until reconciliation", worker.host.HostID, active.ID)
		// A restart may leave the updater beyond the Control Panel's heartbeat
		// availability deadline. Refresh it before every recovery claim, rather
		// than only once, so a long or repeatedly interrupted reconciliation
		// cannot become permanently fenced as updater_offline. No ordinary host
		// workers have started yet, so this only enables the durable
		// active_job_id recovery cursor.
		if err := c.recoveryHeartbeat(ctx); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			c.Logf("host %s Control Panel recovery heartbeat: %v", worker.host.HostID, err)
		} else if err := c.ensureRecoveryBaseline(ctx, worker, active); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			c.Logf("host %s Control Panel recovery probe: %v", worker.host.HostID, err)
		} else if err := worker.pollOnce(ctx); err != nil && !errors.Is(err, context.Canceled) {
			c.Logf("host %s Control Panel recovery poll: %v", worker.host.HostID, err)
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if nextWorker, _ := c.interruptedControlPanelWorker(); nextWorker == nil {
			return nil
		}
		timer := time.NewTimer(c.Config.PollInterval())
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return ctx.Err()
		case <-timer.C:
		}
	}
}

func (c *CentralCoordinator) ensureRecoveryBaseline(ctx context.Context, worker *centralHostWorker, active *UpdateJob) error {
	intentPath := filepath.Join(worker.stateDir(), active.ID, "intent.json")
	if _, err := loadCoordinatorIntent(intentPath); err == nil || !errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if _, ok := c.hostConfigSHA256(worker.host.HostID); ok {
		return nil
	}
	c.probeHost(ctx, worker)
	if err := ctx.Err(); err != nil {
		return err
	}
	if _, ok := c.hostConfigSHA256(worker.host.HostID); !ok {
		return errors.New("remote helper config digest is unavailable")
	}
	return nil
}

func (c *CentralCoordinator) interruptedControlPanelWorker() (*centralHostWorker, *UpdateJob) {
	for _, host := range c.Config.Hosts {
		worker := c.workers[host.HostID]
		if worker == nil {
			continue
		}
		active := worker.journal.Active()
		if active == nil {
			continue
		}
		target, configured := worker.targets[active.TargetID]
		if (configured && isControlPanelTarget(target)) || isControlPanelTarget(Target{TargetID: active.TargetID, ServiceType: active.EffectiveType()}) {
			return worker, active
		}
	}
	return nil, nil
}

func (c *CentralCoordinator) probeHost(ctx context.Context, worker *centralHostWorker) {
	probeCtx, cancel := context.WithTimeout(ctx, c.probeTimeout())
	defer cancel()
	result, err := c.Remote.Probe(probeCtx, worker.host)
	if err == nil {
		err = validateCoordinatorProbe(worker.host, worker.targets, result)
	}
	if ctx.Err() != nil {
		return
	}
	checkedAt := c.now().UTC()
	heartbeat := HostHeartbeat{Name: worker.host.Name, Reachability: "reachable", CheckedAt: checkedAt, Arch: worker.host.Arch}
	versions := make(map[string]string, len(result.Targets))
	if err != nil {
		heartbeat.Reachability = "unreachable"
		heartbeat.Code = coordinatorProbeErrorCode(err)
	} else {
		for _, target := range result.Targets {
			if version := strings.TrimSpace(target.CurrentVersion); version != "" {
				versions[target.TargetID] = version
			}
		}
	}
	c.statusMu.Lock()
	c.hostStatus[worker.host.HostID] = heartbeat
	if err == nil {
		c.probeVersions[worker.host.HostID] = versions
		c.probeConfigSHA256[worker.host.HostID] = result.ConfigSHA256
	}
	c.statusMu.Unlock()
}

func validateCoordinatorProbe(host SSHHost, targets map[string]Target, result RemoteProbeResult) error {
	if err := result.Validate(); err != nil {
		return err
	}
	if result.HostID != host.HostID || result.Arch != host.Arch || result.OS != "linux" {
		return errors.New("remote helper host identity does not match central configuration")
	}
	if len(result.Targets) != len(targets) {
		return errors.New("remote helper target set does not match central configuration")
	}
	seen := make(map[string]bool, len(result.Targets))
	for _, reported := range result.Targets {
		target, ok := targets[reported.TargetID]
		if !ok || seen[reported.TargetID] || reported.ServiceType != target.ServiceType || reported.DeploymentMode != target.DeploymentMode {
			return errors.New("remote helper target identity does not match central configuration")
		}
		seen[reported.TargetID] = true
	}
	return nil
}

func coordinatorProbeErrorCode(err error) string {
	var transportErr *SSHTransportError
	if errors.As(err, &transportErr) {
		switch transportErr.Code {
		case SSHErrorTimeout, SSHErrorConnectionRefused, SSHErrorAuthFailed, SSHErrorHostKeyMismatch, SSHErrorRemoteHelperUnavailable, SSHErrorRemoteConfigInvalid:
			return transportErr.Code
		}
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "ssh_timeout"
	}
	message := strings.ToLower(err.Error())
	switch {
	case strings.Contains(message, "host key"):
		return "ssh_host_key_mismatch"
	case strings.Contains(message, "authenticate"):
		return "ssh_auth_failed"
	case strings.Contains(message, "connect remote ssh") || strings.Contains(message, "connection refused"):
		return "ssh_connection_refused"
	case strings.Contains(message, "identity") || strings.Contains(message, "target") || strings.Contains(message, "config"):
		return "remote_config_invalid"
	default:
		return "remote_helper_unavailable"
	}
}

func (c *CentralCoordinator) hostReachable(hostID string) bool {
	c.statusMu.RLock()
	defer c.statusMu.RUnlock()
	return c.hostStatus[hostID].Reachability == "reachable"
}

func (c *CentralCoordinator) hostConfigSHA256(hostID string) (string, bool) {
	c.statusMu.RLock()
	defer c.statusMu.RUnlock()
	digest, ok := c.probeConfigSHA256[hostID]
	return digest, ok && digestPattern.MatchString(digest)
}

func (c *CentralCoordinator) releaseTokenForJob(job UpdateJob) RemoteSecret {
	if !job.ReleaseToken.Empty() {
		return job.ReleaseToken
	}
	return NewRemoteSecret(strings.TrimSpace(c.Config.GitHubToken))
}

func (c *CentralCoordinator) downloaderForJob(job UpdateJob) (CoordinatorDownloader, error) {
	if job.ReleaseToken.Empty() {
		if strings.TrimSpace(c.Config.GitHubToken) == "" {
			return nil, errors.New("release credential is unavailable for this update job")
		}
		if c.Downloader == nil {
			return nil, errors.New("legacy release downloader is unavailable")
		}
		return c.Downloader, nil
	}
	if c.NewDownloader == nil {
		return nil, errors.New("job-scoped release downloader is unavailable")
	}
	downloader := c.NewDownloader(job.ReleaseToken)
	if downloader == nil {
		return nil, errors.New("job-scoped release downloader is unavailable")
	}
	return downloader, nil
}

func (c *CentralCoordinator) statusSnapshot() (map[string]string, map[string]HostHeartbeat) {
	c.statusMu.RLock()
	defer c.statusMu.RUnlock()
	hosts := make(map[string]HostHeartbeat, len(c.hostStatus))
	deployed := make(map[string]string)
	for id, status := range c.hostStatus {
		hosts[id] = status
	}
	for _, versions := range c.probeVersions {
		for targetID, current := range versions {
			deployed[targetID] = current
		}
	}
	for _, worker := range c.workers {
		for targetID, current := range worker.journal.DeployedVersions() {
			if _, observed := deployed[targetID]; !observed {
				deployed[targetID] = current
			}
		}
	}
	return deployed, hosts
}

func (c *CentralCoordinator) heartbeatSnapshot() (Config, map[string]string, map[string]HostHeartbeat) {
	c.statusMu.RLock()
	defer c.statusMu.RUnlock()
	cfg := c.Config
	cfg.SSHClientPublicKeys = cloneCapabilityStringMap(c.Config.SSHClientPublicKeys)
	cfg.SSHClientKeyFingerprints = cloneCapabilityStringMap(c.Config.SSHClientKeyFingerprints)
	hosts := make(map[string]HostHeartbeat, len(c.hostStatus))
	deployed := make(map[string]string)
	for id, status := range c.hostStatus {
		hosts[id] = status
	}
	for _, versions := range c.probeVersions {
		for targetID, current := range versions {
			deployed[targetID] = current
		}
	}
	for _, worker := range c.workers {
		for targetID, current := range worker.journal.DeployedVersions() {
			if _, observed := deployed[targetID]; !observed {
				deployed[targetID] = current
			}
		}
	}
	return cfg, deployed, hosts
}

// SetPolicyState changes only runtime heartbeat metadata. Applied and desired
// revisions are explicit so a ready-but-not-yet-durable candidate can report
// pending without claiming that the new revision is committed. Raw errors are
// never accepted; callers supply one of the allow-listed stable codes.
func (c *CentralCoordinator) SetPolicyState(appliedRevision, desiredRevision int64, status, errorCode string, publicKeys, fingerprints map[string]string) {
	if appliedRevision < 0 || desiredRevision < appliedRevision {
		return
	}
	c.statusMu.Lock()
	defer c.statusMu.Unlock()
	c.Config.PolicyStatus = normalizePolicyStatus(status)
	c.Config.PolicyRevision = appliedRevision
	c.Config.PolicyDesiredRevision = desiredRevision
	c.Config.PolicyErrorCode = ""
	if c.Config.PolicyStatus == PolicyStatusFailed {
		c.Config.PolicyErrorCode = safePolicyErrorCode(errorCode)
	} else if c.Config.PolicyStatus == PolicyStatusPending && strings.TrimSpace(errorCode) == PolicyErrorActiveJob {
		c.Config.PolicyErrorCode = PolicyErrorActiveJob
	}
	if publicKeys != nil {
		c.Config.SSHClientPublicKeys = cloneCapabilityStringMap(publicKeys)
	}
	if fingerprints != nil {
		c.Config.SSHClientKeyFingerprints = cloneCapabilityStringMap(fingerprints)
	}
}

// SetPolicyStatus retains the original single-revision convenience contract
// for focused callers and tests.
func (c *CentralCoordinator) SetPolicyStatus(revision int64, status, errorCode string, publicKeys, fingerprints map[string]string) {
	c.statusMu.RLock()
	appliedRevision := c.Config.PolicyRevision
	c.statusMu.RUnlock()
	if normalizePolicyStatus(status) == PolicyStatusApplied {
		appliedRevision = revision
	}
	c.SetPolicyState(appliedRevision, revision, status, errorCode, publicKeys, fingerprints)
}

// BeginPolicyReplacement prevents new claims, waits for in-flight execution to
// leave the shared gate, and then verifies no recovery cursor remains. The
// returned release function receives whether replacement is committed.
// A committed drain remains closed to new claims while the canceled Run exits;
// an aborted drain safely resumes the existing coordinator.
func (c *CentralCoordinator) BeginPolicyReplacement(ctx context.Context) (release func(bool), ready bool, err error) {
	c.replacementMu.Lock()
	defer c.replacementMu.Unlock()
	wasDraining := c.draining.Swap(true)
	acquired := make(chan struct{})
	go func() {
		c.executionGate.Lock()
		close(acquired)
	}()
	select {
	case <-ctx.Done():
		go func() {
			<-acquired
			c.executionGate.Unlock()
			if !wasDraining {
				c.draining.Store(false)
			}
		}()
		return nil, false, ctx.Err()
	case <-acquired:
	}
	for _, worker := range c.workers {
		if worker.journal.Active() != nil {
			c.executionGate.Unlock()
			return nil, false, nil
		}
	}
	var once sync.Once
	return func(replacing bool) {
		once.Do(func() {
			c.executionGate.Unlock()
			if !replacing {
				c.draining.Store(false)
			}
		})
	}, true, nil
}

// AbortPolicyReplacement reopens claims after a pending or failed policy is
// abandoned. It is only called when BeginPolicyReplacement is not holding the
// execution writer gate.
func (c *CentralCoordinator) AbortPolicyReplacement() {
	c.draining.Store(false)
}

// RunHostMaintenance serializes a short-lived host bootstrap transaction with
// that host's ordinary update worker and with global policy replacement. The
// lock order deliberately matches pollOnce: host worker first, then the shared
// execution gate. This prevents a bootstrap from racing a claim or using a
// host policy while it is being replaced.
func (c *CentralCoordinator) RunHostMaintenance(
	ctx context.Context,
	hostID string,
	run func(SSHHost, []Target) error,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if run == nil {
		return errors.New("host maintenance callback is required")
	}
	worker, ok := c.workers[strings.TrimSpace(hostID)]
	if !ok {
		return ErrHostMaintenanceUnknownHost
	}
	worker.mu.Lock()
	defer worker.mu.Unlock()
	c.executionGate.RLock()
	defer c.executionGate.RUnlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if c.draining.Load() {
		return ErrHostMaintenanceDraining
	}
	if worker.journal.Active() != nil {
		return ErrHostMaintenanceActiveJob
	}
	targets := append([]Target(nil), c.Config.TargetsForHost(worker.host.HostID)...)
	c.updating.Add(1)
	defer c.updating.Add(-1)
	return run(worker.host, targets)
}

func (w *centralHostWorker) pollOnce(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	w.coordinator.executionGate.RLock()
	readGateHeld := true
	defer func() {
		if readGateHeld {
			w.coordinator.executionGate.RUnlock()
		}
	}()
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := w.flushReports(ctx); err != nil {
		return err
	}
	active := w.journal.Active()
	if w.coordinator.draining.Load() && active == nil {
		return nil
	}
	if active == nil && !w.coordinator.hostReachable(w.host.HostID) {
		return nil
	}
	activeID := ""
	if active != nil {
		activeID = active.ID
	}
	job, clearActive, err := w.coordinator.Panel.ClaimHost(ctx, w.coordinator.Config.NodeID, w.host.HostID, activeID)
	if err != nil || job == nil {
		if err == nil && clearActive {
			if active != nil {
				_ = cleanupJobDirectory(w.stateDir(), active.ID)
			}
			return w.journal.ClearActive()
		}
		return err
	}
	if active != nil && (active.ID != job.ID || !job.RecoveryRequired) {
		return fmt.Errorf("refusing host %s claim %s while interrupted job %s awaits recovery", w.host.HostID, job.ID, active.ID)
	}
	// processJobWithReadGate takes ownership of the read lock. This closes the
	// gap between a claim and execution, so a Control Panel writer cannot start
	// while a newly claimed host job is still being prepared.
	readGateHeld = false
	return w.processJobWithReadGate(ctx, *job)
}

func (w *centralHostWorker) processJob(ctx context.Context, job UpdateJob) error {
	w.coordinator.executionGate.RLock()
	return w.processJobWithReadGate(ctx, job)
}

// processJobWithReadGate is entered with executionGate held for reading and
// always releases it. A Control Panel job first emits a lease-extending report,
// then upgrades by releasing the read lock and acquiring the global write lock.
func (w *centralHostWorker) processJobWithReadGate(ctx context.Context, job UpdateJob) error {
	readGateHeld := true
	defer func() {
		if readGateHeld {
			w.coordinator.executionGate.RUnlock()
		}
	}()
	w.coordinator.updating.Add(1)
	defer w.coordinator.updating.Add(-1)
	// The active record is only a recovery cursor. A fresh claim always returns
	// a new lease, so persisting the bearer token in that cursor adds risk and
	// no recovery value. Report credentials also remain process-memory-only.
	persistedJob := job
	persistedJob.LeaseToken = ""
	persistedJob.ReleaseToken = ""
	if err := w.journal.SetActive(&persistedJob); err != nil {
		return err
	}
	target, ok := w.targets[job.TargetID]
	version := job.EffectiveVersion()
	if !ok || strings.TrimSpace(job.HostID) == "" || job.HostID != w.host.HostID || target.HostID != w.host.HostID || target.ServiceType != job.EffectiveType() || target.DeploymentMode != job.DeploymentMode {
		return w.terminal(ctx, job, "failed", "target_mismatch", "job host and target do not match this coordinator's fixed configuration", ApplyResult{})
	}
	if !versionPattern.MatchString(strings.TrimSpace(job.CurrentVersion)) {
		if job.RecoveryRequired {
			return errors.New("recovery job is missing its immutable current version baseline")
		}
		return w.terminal(ctx, job, "failed", "invalid_job", "job contains an invalid current version baseline", ApplyResult{})
	}
	if !versionPattern.MatchString(version) || !identifierPattern.MatchString(job.ID) || job.LeaseGeneration == 0 || strings.TrimSpace(job.LeaseToken) == "" {
		return w.terminal(ctx, job, "failed", "invalid_job", "job contains an invalid identity, version, or lease", ApplyResult{})
	}
	initialStatus := "claimed"
	initialMessage := "update job claimed and fixed host target validated"
	if job.RecoveryRequired {
		initialStatus = "reconciling"
		initialMessage = "reconciling interrupted host state without reapplying"
	}
	initialProgress := 5
	if job.RecoveryRequired {
		initialProgress = 99
	}
	_, initialReportErr := w.emit(ctx, job, initialStatus, "", initialMessage, initialProgress, "", "")
	if reportStopsExecution(initialReportErr) {
		return initialReportErr
	}
	if isControlPanelTarget(target) {
		// Do not intentionally take the Control Panel offline unless its first
		// lease-extending report was acknowledged. A transient failure remains
		// queued and the job will be reclaimed for reconciliation.
		if initialReportErr != nil {
			return initialReportErr
		}
		w.coordinator.executionGate.RUnlock()
		readGateHeld = false
		if err := w.acquireExclusiveExecution(ctx, job, initialStatus, initialProgress); err != nil {
			return err
		}
		defer w.coordinator.executionGate.Unlock()
	}
	if job.RecoveryRequired {
		return w.processRecovery(ctx, job, target, version)
	}
	plan, err := w.preparePlan(ctx, job, target, version, false)
	if err != nil {
		return w.terminal(ctx, job, "failed", "artifact_verification_failed", safeErrorMessage(err), ApplyResult{})
	}
	remotePlan, err := w.newRemotePlan(plan)
	if err != nil {
		return w.terminal(ctx, job, "failed", "stage_failed", "could not bind the immutable remote plan", ApplyResult{})
	}
	if err := w.stageRemote(ctx, job, remotePlan); err != nil {
		if definiteRemoteStageFailure(err) {
			return w.terminal(ctx, job, "failed", "remote_stage_failed", "remote release staging failed before execution", ApplyResult{})
		}
		// An SSH failure can happen after the remote host durably records the
		// staged intent. Never mark that job terminal or retry Apply: preserve the
		// cursor so a fresh claim can reconcile the staged-only ledger safely.
		return fmt.Errorf("remote stage result is uncertain and reconciliation is required: %w", err)
	}
	if _, err := w.emit(ctx, job, "installing", "", "remote helper is applying the fixed host target", 65, normalizeDigest(plan.ArtifactDigest), ""); err != nil {
		return err
	}
	result, err := w.invokeMutation(ctx, job, remotePlan, "apply", "installing", 70)
	if err != nil {
		if errors.Is(err, ErrLeaseLost) || IsFatalReportError(err) {
			return err
		}
		w.coordinator.Logf("host %s apply result is uncertain; reconciling without reapplying: %v", w.host.HostID, err)
		if _, reportErr := w.emit(ctx, job, "reconciling", "", "remote apply result is uncertain; reconciling durable host state", 99, "", ""); reportErr != nil {
			return reportErr
		}
		reconcilePlan, planErr := w.newRemotePlan(plan)
		if planErr != nil {
			return planErr
		}
		result, err = w.invokeMutation(ctx, job, reconcilePlan, "reconcile", "reconciling", 99)
		if err != nil {
			return fmt.Errorf("remote apply is uncertain and reconciliation remains pending: %w", err)
		}
	}
	return w.finishResult(ctx, job, target, version, result, false)
}

func (w *centralHostWorker) processRecovery(ctx context.Context, job UpdateJob, target Target, targetVersion string) error {
	plan, err := w.preparePlan(ctx, job, target, targetVersion, true)
	if err != nil {
		// A failed verification is not terminal while the remote root ledger may
		// still contain an uncertain cutover. Preserve the active journal.
		return fmt.Errorf("recovery release verification remains pending: %w", err)
	}
	remotePlan, err := w.newRemotePlan(plan)
	if err != nil {
		return err
	}
	result, err := w.invokeMutation(ctx, job, remotePlan, "reconcile", "reconciling", 99)
	if err != nil {
		if remoteExecutionCode(err) == "stage_required" {
			return w.terminal(ctx, job, "failed", "remote_stage_missing", "interrupted job has no remote staged or mutating state", ApplyResult{})
		}
		return fmt.Errorf("remote reconciliation remains pending: %w", err)
	}
	return w.finishResult(ctx, job, target, targetVersion, result, true)
}

func isControlPanelTarget(target Target) bool {
	serviceType := strings.ReplaceAll(strings.ToLower(strings.TrimSpace(target.ServiceType)), "-", "_")
	return target.TargetID == "control-panel" || serviceType == "control_panel"
}

// acquireExclusiveExecution keeps the job lease alive while a writer waits
// for already-running host updates to leave the shared execution gate. A
// pending RWMutex writer also prevents later pollers from claiming new work.
func (w *centralHostWorker) acquireExclusiveExecution(ctx context.Context, job UpdateJob, status string, progress int) error {
	acquired := make(chan struct{})
	go func() {
		w.coordinator.executionGate.Lock()
		close(acquired)
	}()
	releaseAfterAcquire := func() {
		go func() {
			<-acquired
			w.coordinator.executionGate.Unlock()
		}()
	}
	ticker := time.NewTicker(w.coordinator.keepaliveInterval())
	defer ticker.Stop()
	lastAcknowledged := w.coordinator.now()
	for {
		select {
		case <-acquired:
			return nil
		case <-ctx.Done():
			releaseAfterAcquire()
			return ctx.Err()
		case <-ticker.C:
			var reportErr error
			if len(w.journal.Pending()) > 0 {
				reportErr = w.flushReports(ctx)
			} else {
				_, reportErr = w.emit(ctx, job, status, "", "waiting for active host updates before restarting the Control Panel", progress, "", "")
			}
			if reportErr == nil {
				lastAcknowledged = w.coordinator.now()
				continue
			}
			if reportStopsExecution(reportErr) || w.coordinator.now().Sub(lastAcknowledged) >= w.coordinator.reportAckTimeout() {
				releaseAfterAcquire()
				return reportErr
			}
		}
	}
}

func definiteRemoteStageFailure(err error) bool {
	var transportErr *SSHTransportError
	if errors.As(err, &transportErr) {
		switch transportErr.Code {
		case SSHErrorConnectionRefused, SSHErrorAuthFailed, SSHErrorHostKeyMismatch:
			return true
		}
	}
	switch remoteExecutionCode(err) {
	case "stage_failed", "config_mismatch", "state_unavailable", "invalid_request", "target_unavailable", "target_busy":
		return true
	default:
		return false
	}
}

func remoteExecutionCode(err error) string {
	var remoteErr *RemoteExecutionError
	if errors.As(err, &remoteErr) {
		return remoteErr.Code
	}
	return ""
}

func (w *centralHostWorker) preparePlan(ctx context.Context, job UpdateJob, target Target, targetVersion string, recovery bool) (ApplyPlan, error) {
	jobDir, err := ensurePrivateJobDirectory(w.stateDir(), job.ID)
	if err != nil {
		return ApplyPlan{}, err
	}
	intentPath := filepath.Join(jobDir, "intent.json")
	if recovery {
		intent, loadErr := loadCoordinatorIntent(intentPath)
		if loadErr == nil {
			return intent.rebind(job, target, w.host.HostID)
		}
		if !errors.Is(loadErr, os.ErrNotExist) {
			return ApplyPlan{}, loadErr
		}
	}
	configSHA256, ok := w.coordinator.hostConfigSHA256(w.host.HostID)
	if !ok {
		return ApplyPlan{}, errors.New("remote helper config digest is unavailable")
	}
	plan := ApplyPlan{
		JobID: job.ID, HostID: w.host.HostID, TargetID: target.TargetID,
		ServiceType: target.ServiceType, DeploymentMode: target.DeploymentMode,
		TargetVersion: targetVersion, CurrentVersion: strings.TrimSpace(job.CurrentVersion),
		ConfigSHA256:    configSHA256,
		LeaseGeneration: job.LeaseGeneration,
	}
	verificationDir := filepath.Join(jobDir, fmt.Sprintf("verification-%d", job.LeaseGeneration))
	if target.DeploymentMode == ModeSystemd {
		if !recovery {
			if _, err := w.emit(ctx, job, "downloading", "", "downloading and verifying the signed host release", 20, "", ""); err != nil {
				return ApplyPlan{}, err
			}
		}
		downloader, err := w.coordinator.downloaderForJob(job)
		if err != nil {
			return ApplyPlan{}, err
		}
		artifact, err := downloader.Download(ctx, target.ServiceType, targetVersion, w.host.Arch, filepath.Join(verificationDir, "artifact"))
		if err != nil {
			return ApplyPlan{}, err
		}
		plan.StageDir = artifact.RootDir
		plan.ArtifactDigest = artifact.SHA256
		plan.ExpectedVersion = targetVersion
	} else {
		if !recovery {
			if _, err := w.emit(ctx, job, "downloading", "", "downloading and verifying the Docker release manifest", 20, "", ""); err != nil {
				return ApplyPlan{}, err
			}
		}
		imageRepo, err := coordinatorDockerImageRepo(target.ServiceType)
		if err != nil {
			return ApplyPlan{}, err
		}
		downloader, err := w.coordinator.downloaderForJob(job)
		if err != nil {
			return ApplyPlan{}, err
		}
		resolved, err := downloader.ResolveDockerReleaseForArch(ctx, targetVersion, target.ServiceType, imageRepo, "docker", w.host.Arch, filepath.Join(verificationDir, "docker"))
		if err != nil {
			return ApplyPlan{}, err
		}
		// RemotePlan uses raw SHA256 hex for the release-manifest sidecar;
		// reports add the canonical sha256: prefix at their boundary.
		plan.ArtifactDigest = strings.TrimPrefix(normalizeDigest(resolved.ManifestSHA256), "sha256:")
		plan.ExpectedVersion = resolved.SourceVersion
		plan.ExpectedImageDigest = resolved.ManifestDigest
		plan.ExpectedPlatformDigest = resolved.PlatformDigest
	}
	intent := coordinatorIntentFromPlan(plan)
	if err := intent.validate(job, target, w.host.HostID); err != nil {
		return ApplyPlan{}, err
	}
	if err := writePrivateJSON(intentPath, intent); err != nil {
		return ApplyPlan{}, fmt.Errorf("persist secret-free update intent: %w", err)
	}
	if !recovery {
		if _, err := w.emit(ctx, job, "verifying", "", "release identity, SHA256, and target architecture verified", 40, normalizeDigest(plan.ArtifactDigest), ""); err != nil {
			return ApplyPlan{}, err
		}
		if _, err := w.emit(ctx, job, "staging", "", "immutable remote update plan prepared", 50, normalizeDigest(plan.ArtifactDigest), ""); err != nil {
			return ApplyPlan{}, err
		}
	}
	return plan, nil
}

func coordinatorIntentFromPlan(plan ApplyPlan) coordinatorIntent {
	return coordinatorIntent{
		JobID: plan.JobID, HostID: plan.HostID, TargetID: plan.TargetID,
		ServiceType: plan.ServiceType, DeploymentMode: plan.DeploymentMode,
		TargetVersion: plan.TargetVersion, CurrentVersion: plan.CurrentVersion, ConfigSHA256: plan.ConfigSHA256,
		ArtifactDigest: plan.ArtifactDigest, ExpectedVersion: plan.ExpectedVersion,
		ExpectedImageDigest: plan.ExpectedImageDigest, ExpectedPlatformDigest: plan.ExpectedPlatformDigest,
	}
}

func (i coordinatorIntent) validate(job UpdateJob, target Target, hostID string) error {
	if i.JobID != job.ID || i.HostID != hostID || i.TargetID != target.TargetID || i.ServiceType != target.ServiceType || i.DeploymentMode != target.DeploymentMode || i.TargetVersion != job.EffectiveVersion() || i.CurrentVersion != strings.TrimSpace(job.CurrentVersion) {
		return errors.New("durable update intent does not match the claimed host job")
	}
	if !identifierPattern.MatchString(i.JobID) || !identifierPattern.MatchString(i.HostID) || !identifierPattern.MatchString(i.TargetID) || !versionPattern.MatchString(i.TargetVersion) || !remotePlanHashPattern.MatchString(i.ArtifactDigest) {
		return errors.New("durable update intent identity is invalid")
	}
	if !versionPattern.MatchString(i.CurrentVersion) || !digestPattern.MatchString(i.ConfigSHA256) || normalizeDigest(i.ConfigSHA256) != i.ConfigSHA256 {
		return errors.New("durable update intent current version or helper config digest is invalid")
	}
	switch i.DeploymentMode {
	case ModeSystemd:
		if i.ExpectedVersion != i.TargetVersion || i.ExpectedImageDigest != "" || i.ExpectedPlatformDigest != "" {
			return errors.New("durable systemd update intent is invalid")
		}
	case ModeDocker:
		if !versionPattern.MatchString(i.ExpectedVersion) || !digestPattern.MatchString(i.ExpectedImageDigest) || !digestPattern.MatchString(i.ExpectedPlatformDigest) {
			return errors.New("durable Docker update intent is invalid")
		}
	default:
		return errors.New("durable update intent mode is invalid")
	}
	return nil
}

func (i coordinatorIntent) rebind(job UpdateJob, target Target, hostID string) (ApplyPlan, error) {
	if err := i.validate(job, target, hostID); err != nil {
		return ApplyPlan{}, err
	}
	return ApplyPlan{
		JobID: i.JobID, HostID: i.HostID, TargetID: i.TargetID,
		ServiceType: i.ServiceType, DeploymentMode: i.DeploymentMode,
		TargetVersion: i.TargetVersion, CurrentVersion: i.CurrentVersion, ConfigSHA256: i.ConfigSHA256,
		LeaseGeneration: job.LeaseGeneration, ArtifactDigest: i.ArtifactDigest,
		ExpectedVersion: i.ExpectedVersion, ExpectedImageDigest: i.ExpectedImageDigest,
		ExpectedPlatformDigest: i.ExpectedPlatformDigest,
	}, nil
}

func loadCoordinatorIntent(path string) (coordinatorIntent, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return coordinatorIntent{}, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > 64<<10 || (runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0) {
		return coordinatorIntent{}, errors.New("durable update intent must be a private bounded regular file")
	}
	f, err := os.Open(path)
	if err != nil {
		return coordinatorIntent{}, err
	}
	defer f.Close()
	var intent coordinatorIntent
	decoder := json.NewDecoder(io.LimitReader(f, 64<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&intent); err != nil {
		return coordinatorIntent{}, errors.New("decode durable update intent")
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return coordinatorIntent{}, errors.New("durable update intent contains trailing data")
	}
	return intent, nil
}

func coordinatorDockerImageRepo(serviceType string) (string, error) {
	service := dockerManifestService(serviceType)
	switch service {
	case "control-panel", "worker", "encoder-recorder", "discord-bot", "observability":
		return "ghcr.io/kome-lab/autostream-docker/" + service, nil
	default:
		return "", errors.New("service type has no fixed Docker image repository")
	}
}

func (w *centralHostWorker) newRemotePlan(plan ApplyPlan) (RemotePlan, error) {
	planSHA256, err := MutationPlanSHA256(plan)
	if err != nil {
		return RemotePlan{}, err
	}
	sessionID, err := w.coordinator.sessionID()
	if err != nil {
		return RemotePlan{}, errors.New("create mutation session identity")
	}
	return RemotePlan{
		JobID: plan.JobID, HostID: plan.HostID, TargetID: plan.TargetID,
		ServiceType: plan.ServiceType, DeploymentMode: plan.DeploymentMode,
		CurrentVersion: plan.CurrentVersion, ConfigSHA256: plan.ConfigSHA256, TargetVersion: plan.TargetVersion,
		LeaseGeneration: plan.LeaseGeneration, ArtifactDigest: plan.ArtifactDigest,
		ExpectedVersion: plan.ExpectedVersion, ExpectedImageDigest: plan.ExpectedImageDigest,
		ExpectedPlatformDigest: plan.ExpectedPlatformDigest, SessionID: sessionID, PlanSHA256: planSHA256,
	}, nil
}

func (w *centralHostWorker) stageRemote(ctx context.Context, job UpdateJob, plan RemotePlan) error {
	stageCtx, cancel := context.WithTimeout(ctx, w.coordinator.mutationTimeout())
	defer cancel()
	type outcome struct {
		result RemoteStageResult
		err    error
	}
	done := make(chan outcome, 1)
	go func() {
		result, err := w.coordinator.Remote.Stage(stageCtx, w.host, plan, w.coordinator.releaseTokenForJob(job))
		done <- outcome{result: result, err: err}
	}()
	ticker := time.NewTicker(w.coordinator.keepaliveInterval())
	defer ticker.Stop()
	lastAcknowledged := w.coordinator.now()
	for {
		select {
		case result := <-done:
			if result.err != nil {
				return result.err
			}
			if result.result.Status != "staged" || result.result.SessionID != plan.SessionID || result.result.PlanSHA256 != plan.PlanSHA256 || normalizeDigest(result.result.ArtifactDigest) != normalizeDigest(plan.ArtifactDigest) {
				return errors.New("remote stage result does not match the immutable plan")
			}
			return nil
		case <-stageCtx.Done():
			return stageCtx.Err()
		case <-ticker.C:
			var reportErr error
			if len(w.journal.Pending()) > 0 {
				reportErr = w.flushReports(ctx)
			} else {
				_, reportErr = w.emit(ctx, job, "staging", "", "remote helper is still staging verified release inputs", 55, plan.ArtifactDigest, "")
			}
			if reportErr == nil {
				lastAcknowledged = w.coordinator.now()
				continue
			}
			if reportStopsExecution(reportErr) || w.coordinator.now().Sub(lastAcknowledged) >= w.coordinator.reportAckTimeout() {
				cancel()
				return reportErr
			}
		}
	}
}

func (w *centralHostWorker) invokeMutation(ctx context.Context, job UpdateJob, remotePlan RemotePlan, operation, keepaliveStatus string, progress int) (ApplyResult, error) {
	binding := MutationGrantBinding{
		LeaseGeneration: job.LeaseGeneration, HostID: w.host.HostID, TargetID: job.TargetID,
		ServiceType: job.EffectiveType(), TargetVersion: job.EffectiveVersion(), DeploymentMode: job.DeploymentMode,
		Operation: operation, PlanSHA256: remotePlan.PlanSHA256, SessionID: remotePlan.SessionID,
	}
	grant, err := w.coordinator.Panel.IssueMutationGrant(ctx, job.ID, MutationGrantRequest{ServiceID: w.coordinator.Config.NodeID, LeaseToken: job.LeaseToken, MutationGrantBinding: binding})
	if err != nil {
		return ApplyResult{}, err
	}
	mutationCtx, cancel := context.WithTimeout(ctx, w.coordinator.mutationTimeout())
	defer cancel()
	type outcome struct {
		result ApplyResult
		err    error
	}
	done := make(chan outcome, 1)
	go func() {
		var result ApplyResult
		var err error
		if operation == "apply" {
			result, err = w.coordinator.Remote.Apply(mutationCtx, w.host, remotePlan, NewRemoteSecret(grant.Token))
		} else {
			result, err = w.coordinator.Remote.Reconcile(mutationCtx, w.host, remotePlan, NewRemoteSecret(grant.Token))
		}
		done <- outcome{result: result, err: err}
	}()
	ticker := time.NewTicker(w.coordinator.keepaliveInterval())
	defer ticker.Stop()
	lastAcknowledged := w.coordinator.now()
	for {
		select {
		case result := <-done:
			return result.result, result.err
		case <-mutationCtx.Done():
			return ApplyResult{}, mutationCtx.Err()
		case <-ticker.C:
			var reportErr error
			if len(w.journal.Pending()) > 0 {
				reportErr = w.flushReports(ctx)
			} else {
				_, reportErr = w.emit(ctx, job, keepaliveStatus, "", "remote helper operation is still in progress", progress, "", "")
			}
			if reportErr == nil {
				lastAcknowledged = w.coordinator.now()
				continue
			}
			if reportStopsExecution(reportErr) || w.coordinator.now().Sub(lastAcknowledged) >= w.coordinator.reportAckTimeout() {
				cancel()
				return ApplyResult{}, reportErr
			}
		}
	}
}

func (w *centralHostWorker) finishResult(ctx context.Context, job UpdateJob, target Target, targetVersion string, result ApplyResult, recovery bool) error {
	if result.Status == "rolled_back" || result.RolledBack {
		if !recovery {
			if _, err := w.emit(ctx, job, "rolling_back", "", "post-update verification failed; previous release was restored", 95, result.ArtifactDigest, result.PreviousDigest); err != nil {
				return err
			}
		}
		code := "post_update_verification_failed"
		if recovery {
			code = ""
		}
		return w.terminal(ctx, job, "rolled_back", code, result.Message, result)
	}
	if !recovery {
		if _, err := w.emit(ctx, job, "health_checking", "", "remote helper completed health and expected-version checks", 90, result.ArtifactDigest, result.PreviousDigest); err != nil {
			return err
		}
	}
	if err := w.journal.MarkDeployed(target.TargetID, targetVersion); err != nil {
		return fmt.Errorf("persist verified deployed version: %w", err)
	}
	message := "target updated and verified"
	if recovery {
		message = "interrupted update resolved to the requested release"
	}
	return w.terminal(ctx, job, "succeeded", "", message, result)
}

func (w *centralHostWorker) terminal(ctx context.Context, job UpdateJob, status, code, message string, result ApplyResult) error {
	if _, err := w.emit(ctx, job, status, code, message, 100, result.ArtifactDigest, result.PreviousDigest); err != nil {
		return err
	}
	return w.journal.ClearActive()
}

func (w *centralHostWorker) emit(ctx context.Context, job UpdateJob, status, code, message string, progress int, artifact, previous string) (JobReport, error) {
	report, err := w.journal.Queue(job.ID, w.coordinator.Config.NodeID, job.LeaseToken, job.LeaseGeneration, status, code, message, progress, canonicalReportDigest(artifact), canonicalReportDigest(previous))
	if err != nil {
		return report, err
	}
	if err := w.flushReports(ctx); err != nil {
		w.coordinator.Logf("host %s update report queued for retry: %v", w.host.HostID, err)
		if IsFatalReportError(err) {
			pending := w.journal.Pending()
			if len(pending) > 0 {
				_ = w.journal.Ack(pending[0].JobID, pending[0].Report.Sequence)
			}
		}
		return report, err
	}
	return report, nil
}

func (w *centralHostWorker) flushReports(ctx context.Context) error {
	for {
		pending := w.journal.Pending()
		if len(pending) == 0 {
			return nil
		}
		item := pending[0]
		if err := w.coordinator.Panel.Report(ctx, item.JobID, item.Report); err != nil {
			if IsPermanentReportError(err) {
				if ackErr := w.journal.Ack(item.JobID, item.Report.Sequence); ackErr != nil {
					return ackErr
				}
				if isTerminalUpdateStatus(item.Report.Status) {
					if cleanupErr := cleanupJobDirectory(w.stateDir(), item.JobID); cleanupErr != nil {
						return cleanupErr
					}
				}
				return ErrLeaseLost
			}
			return err
		}
		if err := w.journal.Ack(item.JobID, item.Report.Sequence); err != nil {
			return err
		}
		if isTerminalUpdateStatus(item.Report.Status) {
			if err := cleanupJobDirectory(w.stateDir(), item.JobID); err != nil {
				return fmt.Errorf("clean acknowledged host update state: %w", err)
			}
		}
	}
}

func (w *centralHostWorker) stateDir() string {
	return coordinatorHostStateDir(w.coordinator.Config.StateDir, w.host.HostID)
}

func coordinatorHostStateDir(stateDir, hostID string) string {
	digest := sha256.Sum256([]byte(hostID))
	return filepath.Join(stateDir, "hosts", hex.EncodeToString(digest[:]))
}

func (c *CentralCoordinator) sessionID() (string, error) {
	if c.NewSessionID != nil {
		return c.NewSessionID()
	}
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	return "session-" + hex.EncodeToString(value[:]), nil
}

func (c *CentralCoordinator) probeTimeout() time.Duration {
	if c.ProbeTimeout > 0 {
		return c.ProbeTimeout
	}
	return 15 * time.Second
}

func (c *CentralCoordinator) mutationTimeout() time.Duration {
	if c.MutationTimeout > 0 {
		return c.MutationTimeout
	}
	return 30 * time.Minute
}

func (c *CentralCoordinator) keepaliveInterval() time.Duration {
	if c.KeepaliveInterval > 0 {
		return c.KeepaliveInterval
	}
	return 60 * time.Second
}

func (c *CentralCoordinator) reportAckTimeout() time.Duration {
	if c.ReportAckTimeout > 0 {
		return c.ReportAckTimeout
	}
	return 10 * time.Minute
}

func (c *CentralCoordinator) prepareStatusServer() (*http.Server, net.Listener, error) {
	server := &http.Server{Addr: c.Config.API.BindAddress(), Handler: c.statusHandler(), ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 1 << 20}
	listener, err := net.Listen("tcp", server.Addr)
	if err != nil {
		return nil, nil, fmt.Errorf("listen for central updater status: %w", err)
	}
	if !c.Config.API.SSLEnabled {
		return server, listener, nil
	}
	certificate, err := tls.LoadX509KeyPair(c.Config.API.TLSCertFile, c.Config.API.TLSKeyFile)
	if err != nil {
		_ = listener.Close()
		return nil, nil, errors.New("load central updater status TLS identity")
	}
	tlsConfig := &tls.Config{
		MinVersion:   tls.VersionTLS12,
		Certificates: []tls.Certificate{certificate},
	}
	server.TLSConfig = tlsConfig
	return server, tls.NewListener(listener, tlsConfig), nil
}

func (c *CentralCoordinator) statusHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, _ *http.Request) {
		writeStatusJSON(w, http.StatusOK, map[string]any{"status": "ok", "service_type": ServiceTypeUpdateAgent, "version": version.Current()})
	})
	mux.HandleFunc("GET /version", func(w http.ResponseWriter, _ *http.Request) {
		writeStatusJSON(w, http.StatusOK, map[string]any{"service": "autostream-updater", "version": version.Current(), "commit": version.Commit, "build_date": version.BuildDate})
	})
	mux.HandleFunc("GET /status", func(w http.ResponseWriter, r *http.Request) {
		if !validBearerToken(r.Header.Get("Authorization"), c.Config.RuntimeToken) {
			w.Header().Set("WWW-Authenticate", "Bearer")
			writeStatusJSON(w, http.StatusUnauthorized, map[string]string{"code": "unauthorized"})
			return
		}
		deployed, hosts := c.statusSnapshot()
		status := "online"
		if c.updating.Load() > 0 {
			status = "updating"
		}
		writeStatusJSON(w, http.StatusOK, map[string]any{"status": status, "service_id": c.Config.NodeID, "service_type": ServiceTypeUpdateAgent, "version": version.Current(), "hosts": hosts, "deployed_versions": deployed})
	})
	return mux
}

// ValidateCentralHosts performs the same bounded, exact probe used at runtime
// without starting workers or claiming jobs.
func ValidateCentralHosts(ctx context.Context, cfg Config, remote RemoteExecutor) ([]string, error) {
	if len(cfg.Hosts) == 0 || remote == nil {
		return nil, errors.New("central SSH hosts are not configured")
	}
	results := make([]string, len(cfg.Hosts))
	errs := make([]error, len(cfg.Hosts))
	var wg sync.WaitGroup
	for i, host := range cfg.Hosts {
		i, host := i, host
		wg.Add(1)
		go func() {
			defer wg.Done()
			probeCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
			defer cancel()
			probe, err := remote.Probe(probeCtx, host)
			if err == nil {
				targets := make(map[string]Target)
				for _, target := range cfg.TargetsForHost(host.HostID) {
					targets[target.TargetID] = target
				}
				err = validateCoordinatorProbe(host, targets, probe)
			}
			if err != nil {
				errs[i] = fmt.Errorf("host %s (%s)", host.HostID, coordinatorProbeErrorCode(err))
				return
			}
			results[i] = fmt.Sprintf("host %s remote helper and %d targets valid", host.HostID, len(probe.Targets))
		}()
	}
	wg.Wait()
	var failures []string
	for _, err := range errs {
		if err != nil {
			failures = append(failures, err.Error())
		}
	}
	if len(failures) > 0 {
		return nil, errors.New(strings.Join(failures, "; "))
	}
	return results, nil
}
