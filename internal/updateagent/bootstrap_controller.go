package updateagent

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	controlversion "github.com/example/autostream-control-panel/internal/version"
)

// BootstrapPanel is the narrow, outbound-only Control Panel boundary used by
// the bootstrap poller. The credential envelope and release token are accepted
// only into updater memory.
type BootstrapPanel interface {
	ClaimBootstrap(context.Context, string, int64, string) (BootstrapJobClaim, bool, error)
	AcceptBootstrap(context.Context, string, string, string) error
	ReportBootstrap(context.Context, string, BootstrapJobReport) error
}

// BootstrapArtifactDownloader prepares the signed helper installer bundle for
// one architecture. Implementations must treat their release token as
// job-scoped and memory-only.
type BootstrapArtifactDownloader interface {
	DownloadUpdateHostBootstrap(context.Context, string, string, string) (DownloadedArtifact, error)
}

// BootstrapHostInstaller is deliberately narrower than a general SSH client:
// callers cannot supply remote commands, paths, or privileged policy fields.
type BootstrapHostInstaller interface {
	InspectStandardSystemdDatabases(context.Context, BootstrapSSHHost, BootstrapSSHCredential, []Target) (map[string]string, error)
	Install(context.Context, BootstrapSSHHost, BootstrapSSHCredential, BootstrapPayload) (string, error)
}

// BootstrapMaintenanceRunner serializes bootstrap with ordinary update work
// for a selected host.
type BootstrapMaintenanceRunner interface {
	RunHostMaintenance(context.Context, string, func(SSHHost, []Target) error) error
}

const bootstrapManagedHostUser = "autostream-update-host"

var (
	ErrBootstrapControllerIncomplete = errors.New("bootstrap controller dependencies are incomplete")
	ErrBootstrapClaimFailed          = errors.New("bootstrap job claim failed")
	ErrBootstrapCredentialRejected   = errors.New("bootstrap credential envelope was rejected")
	ErrBootstrapAcceptFailed         = errors.New("bootstrap job acceptance failed")
	ErrBootstrapReportFailed         = errors.New("bootstrap job progress report failed")
	ErrBootstrapTemporaryState       = errors.New("bootstrap temporary state is unavailable")
)

// BootstrapController contains injectable seams for focused tests. Its zero
// value, as returned by NewBootstrapController, uses the production Panel,
// release, SSH, crypto, filesystem, and probe implementations.
type BootstrapController struct {
	Panel             BootstrapPanel
	Decrypt           func(string, BootstrapEnvelopeBinding, []byte) (BootstrapAdministratorCredential, error)
	NewDownloader     func(RemoteSecret) BootstrapArtifactDownloader
	Installer         BootstrapHostInstaller
	Remote            RemoteExecutor
	BuildHelperConfig func(string, string, string, []Target, map[string]string) (HelperConfig, error)
	CurrentVersion    func() string
	MakeTempDir       func(string, string) (string, error)
	RemoveAll         func(string) error
}

func NewBootstrapController() *BootstrapController {
	return &BootstrapController{}
}

// PollOnce claims at most one batch, binds its encrypted credential to the
// exact updater/revision/job/host set, and processes every host independently.
// Host failures are reported with fixed public messages and never return raw
// SSH, crypto, credential, or release errors.
func (c *BootstrapController) PollOnce(
	ctx context.Context,
	cfg Config,
	maintenance BootstrapMaintenanceRunner,
) error {
	if c == nil || maintenance == nil ||
		!identifierPattern.MatchString(strings.TrimSpace(cfg.NodeID)) ||
		cfg.PolicyRevision <= 0 ||
		!validBootstrapRecipientKeyFingerprint(cfg.BootstrapEncryptionKeyFingerprint) {
		return ErrBootstrapControllerIncomplete
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	panel := c.Panel
	if panel == nil {
		panel = PanelClient{BaseURL: cfg.PanelURL, Token: cfg.RuntimeToken}
	}
	claim, ok, err := panel.ClaimBootstrap(
		ctx,
		cfg.NodeID,
		cfg.PolicyRevision,
		cfg.BootstrapEncryptionKeyFingerprint,
	)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return ErrBootstrapClaimFailed
	}
	if !ok {
		return nil
	}
	defer clearBootstrapJobClaim(&claim)
	if err := claim.Validate(cfg.NodeID, cfg.PolicyRevision); err != nil {
		return ErrBootstrapClaimFailed
	}

	hostIDs := append([]string(nil), claim.HostIDs...)
	sort.Strings(hostIDs)
	binding := BootstrapEnvelopeBinding{
		Version: BootstrapEnvelopeVersion, UpdaterID: cfg.NodeID,
		PolicyRevision: cfg.PolicyRevision, JobID: claim.ID, HostIDs: hostIDs,
	}
	envelopeJSON, err := json.Marshal(claim.Envelope)
	claim.Envelope = BootstrapCredentialEnvelope{}
	if err != nil {
		return ErrBootstrapCredentialRejected
	}
	defer clear(envelopeJSON)
	decrypt := c.Decrypt
	if decrypt == nil {
		decrypt = DecryptBootstrapCredentialEnvelope
	}
	credential, err := decrypt(cfg.StateDir, binding, envelopeJSON)
	if err != nil || validateBootstrapAdministratorCredential(credential) != nil {
		clearBootstrapAdministratorCredential(&credential)
		return ErrBootstrapCredentialRejected
	}
	defer clearBootstrapAdministratorCredential(&credential)

	makeTempDir := c.MakeTempDir
	if makeTempDir == nil {
		makeTempDir = os.MkdirTemp
	}
	tempRoot, err := makeTempDir(cfg.StateDir, ".bootstrap-job-*")
	if err != nil || !validBootstrapTemporaryDirectory(cfg.StateDir, tempRoot) {
		if tempRoot != "" {
			removeAll := c.RemoveAll
			if removeAll == nil {
				removeAll = os.RemoveAll
			}
			_ = removeAll(tempRoot)
		}
		return ErrBootstrapTemporaryState
	}
	removeAll := c.RemoveAll
	if removeAll == nil {
		removeAll = os.RemoveAll
	}
	defer func() { _ = removeAll(tempRoot) }()

	var downloader BootstrapArtifactDownloader
	var defaultDownloader *ReleaseDownloader
	if c.NewDownloader == nil {
		defaultDownloader = &ReleaseDownloader{Token: claim.ReleaseToken.Reveal()}
		downloader = defaultDownloader
	} else {
		downloader = c.NewDownloader(claim.ReleaseToken)
	}
	claim.ReleaseToken = ""
	defer func() {
		if defaultDownloader != nil {
			defaultDownloader.Token = ""
		}
		downloader = nil
	}()
	if downloader == nil {
		return ErrBootstrapControllerIncomplete
	}
	installer := c.Installer
	if installer == nil {
		installer = BootstrapSSHExecutor{}
	}
	remote := c.Remote
	if remote == nil {
		remote = SSHRemoteExecutor{}
	}
	currentVersion := c.CurrentVersion
	if currentVersion == nil {
		currentVersion = controlversion.Current
	}
	helperVersion := currentVersion()
	if !versionPattern.MatchString(helperVersion) {
		return ErrBootstrapControllerIncomplete
	}

	if err := retryBootstrapPanelCall(ctx, func() error {
		return panel.AcceptBootstrap(ctx, claim.ID, cfg.NodeID, claim.LeaseToken)
	}); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return ErrBootstrapAcceptFailed
	}

	artifacts := make(map[string]DownloadedArtifact)
	report := func(hostID, status string, progress int, code, message string) error {
		payload := BootstrapJobReport{
			ServiceID: cfg.NodeID, LeaseToken: claim.LeaseToken, HostID: hostID,
			Status: status, Progress: progress, Code: code, Message: message,
		}
		err := retryBootstrapPanelCall(ctx, func() error {
			return panel.ReportBootstrap(ctx, claim.ID, payload)
		})
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return ErrBootstrapReportFailed
		}
		return nil
	}

	for _, hostID := range claim.HostIDs {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := report(hostID, BootstrapHostStatusConnecting, 10, "", "connecting to the selected host"); err != nil {
			return err
		}
		hostErr := maintenance.RunHostMaintenance(ctx, hostID, func(host SSHHost, targets []Target) error {
			return c.installHost(
				ctx, cfg, host, targets, credential, installer, remote,
				downloader, helperVersion, tempRoot, artifacts, report,
			)
		})
		if hostErr == nil {
			if err := report(hostID, BootstrapHostStatusSucceeded, 100, "", "host helper installation verified"); err != nil {
				return err
			}
			continue
		}
		if errors.Is(hostErr, context.Canceled) || errors.Is(hostErr, context.DeadlineExceeded) {
			return hostErr
		}
		if errors.Is(hostErr, ErrBootstrapReportFailed) {
			return ErrBootstrapReportFailed
		}
		failure := safeBootstrapHostFailure(hostErr)
		if err := report(hostID, BootstrapHostStatusFailed, failure.progress, failure.code, failure.message); err != nil {
			return err
		}
	}
	return nil
}

func (c *BootstrapController) installHost(
	ctx context.Context,
	cfg Config,
	host SSHHost,
	targets []Target,
	credential BootstrapAdministratorCredential,
	installer BootstrapHostInstaller,
	remote RemoteExecutor,
	downloader BootstrapArtifactDownloader,
	helperVersion string,
	tempRoot string,
	artifacts map[string]DownloadedArtifact,
	report func(string, string, int, string, string) error,
) error {
	if err := validateBootstrapHostSelection(host, targets); err != nil {
		return err
	}
	managedPublicKey := strings.TrimSpace(cfg.SSHClientPublicKeys[host.HostID])
	if managedPublicKey == "" {
		return newBootstrapHostFailure(
			"bootstrap_managed_key_missing",
			"managed updater SSH public key is unavailable",
			10,
		)
	}
	bootstrapHost := BootstrapSSHHost{
		HostID: host.HostID, Address: host.Address, Port: host.Port,
		AdminUser: credential.AdministratorUser, Arch: host.Arch,
		HostPublicKey: host.HostPublicKey,
	}
	sshCredential := BootstrapSSHCredential{
		PrivateKey: credential.PrivateKey,
		Passphrase: credential.Passphrase,
	}
	if err := report(host.HostID, BootstrapHostStatusVerifying, 20, "", "inspecting the standard systemd host profile"); err != nil {
		return err
	}
	databaseNames := make(map[string]string)
	databaseTargets := bootstrapDatabaseTargets(targets)
	if len(databaseTargets) != 0 {
		var err error
		databaseNames, err = installer.InspectStandardSystemdDatabases(ctx, bootstrapHost, sshCredential, databaseTargets)
		if err != nil {
			return safeBootstrapSSHFailure(
				err, "bootstrap_inspection_failed",
				"standard host settings could not be inspected", 20,
			)
		}
	}
	buildHelperConfig := c.BuildHelperConfig
	if buildHelperConfig == nil {
		buildHelperConfig = BuildStandardSystemdHelperConfig
	}
	helperConfig, err := buildHelperConfig(
		cfg.PanelURL, host.HostID, host.Arch, targets, databaseNames,
	)
	if err != nil {
		return newBootstrapHostFailure(
			"bootstrap_config_failed",
			"fixed helper configuration could not be generated",
			25,
		)
	}
	configJSON, err := json.Marshal(helperConfig)
	if err != nil {
		return newBootstrapHostFailure(
			"bootstrap_config_failed",
			"fixed helper configuration could not be generated",
			25,
		)
	}
	defer clear(configJSON)
	if err := report(host.HostID, BootstrapHostStatusUploading, 40, "", "preparing the verified helper artifact"); err != nil {
		return err
	}
	artifact, ok := artifacts[host.Arch]
	if !ok {
		artifact, err = downloader.DownloadUpdateHostBootstrap(
			ctx, helperVersion, host.Arch, filepath.Join(tempRoot, host.Arch),
		)
		if err != nil || strings.TrimSpace(artifact.RootDir) == "" {
			return newBootstrapHostFailure(
				"bootstrap_artifact_failed",
				"verified helper artifact could not be prepared",
				40,
			)
		}
		artifacts[host.Arch] = artifact
	}
	if err := report(host.HostID, BootstrapHostStatusInstalling, 65, "", "installing the fixed non-resident helper policy"); err != nil {
		return err
	}
	_, installErr := installer.Install(ctx, bootstrapHost, sshCredential, BootstrapPayload{
		ArtifactRootDir:  artifact.RootDir,
		ConfigJSON:       configJSON,
		ManagedPublicKey: []byte(managedPublicKey),
	})
	if err := report(host.HostID, BootstrapHostStatusProbing, 90, "", "verifying the installed helper and target set"); err != nil {
		return err
	}
	probe, probeErr := remote.Probe(ctx, host)
	if probeErr == nil {
		probeErr = validateBootstrapInstalledProbe(host, targets, helperConfig, probe)
	}
	if probeErr == nil {
		// A successful exact probe is the reconciliation proof when the SSH
		// session lost the install result after the remote transaction committed.
		return nil
	}
	if installErr != nil {
		return safeBootstrapSSHFailure(
			installErr, "bootstrap_install_failed",
			"host helper installation could not be verified", 90,
		)
	}
	var transportErr *SSHTransportError
	if errors.As(probeErr, &transportErr) || errors.Is(probeErr, context.DeadlineExceeded) {
		return safeBootstrapSSHFailure(
			probeErr, "bootstrap_probe_failed",
			"installed helper did not answer the verification probe", 90,
		)
	}
	return newBootstrapHostFailure(
		"bootstrap_probe_mismatch",
		"installed helper does not match the selected host and targets",
		90,
	)
}

func validateBootstrapHostSelection(host SSHHost, targets []Target) error {
	if !identifierPattern.MatchString(strings.TrimSpace(host.HostID)) ||
		host.User != bootstrapManagedHostUser ||
		(host.Arch != "amd64" && host.Arch != "arm64") ||
		len(targets) == 0 {
		return newBootstrapHostFailure(
			"bootstrap_profile_unsupported",
			"host does not use the standard systemd update profile",
			10,
		)
	}
	seen := make(map[string]bool, len(targets))
	for _, target := range targets {
		if target.HostID != host.HostID || target.DeploymentMode != ModeSystemd ||
			seen[target.TargetID] || target.ValidateCentralIdentity() != nil {
			return newBootstrapHostFailure(
				"bootstrap_profile_unsupported",
				"host does not use the standard systemd update profile",
				10,
			)
		}
		if _, ok := standardSystemdProfileFor(target.ServiceType); !ok {
			return newBootstrapHostFailure(
				"bootstrap_profile_unsupported",
				"host does not use the standard systemd update profile",
				10,
			)
		}
		seen[target.TargetID] = true
	}
	return nil
}

func bootstrapDatabaseTargets(targets []Target) []Target {
	databaseTargets := make([]Target, 0, 2)
	for _, target := range targets {
		profile, ok := standardSystemdProfileFor(target.ServiceType)
		if ok && profile.backupExecutable != "" {
			databaseTargets = append(databaseTargets, target)
		}
	}
	return databaseTargets
}

func validateBootstrapInstalledProbe(
	host SSHHost,
	targets []Target,
	helperConfig HelperConfig,
	probe RemoteProbeResult,
) error {
	targetMap := make(map[string]Target, len(targets))
	for _, target := range targets {
		targetMap[target.TargetID] = target
	}
	if err := validateCoordinatorProbe(host, targetMap, probe); err != nil {
		return err
	}
	for _, target := range probe.Targets {
		currentVersion := strings.TrimSpace(target.CurrentVersion)
		if !versionPattern.MatchString(currentVersion) || currentVersion != target.CurrentVersion {
			return errors.New("installed helper could not establish the target current version baseline")
		}
		if !target.EndpointVerified {
			return errors.New("installed helper target health and version endpoint is not verified")
		}
	}
	expectedDigest, err := helperConfig.SHA256()
	if err != nil || probe.ConfigSHA256 != expectedDigest {
		return errors.New("installed helper config digest does not match generated policy")
	}
	return nil
}

type bootstrapHostFailure struct {
	code     string
	message  string
	progress int
}

func (e *bootstrapHostFailure) Error() string {
	if e == nil {
		return "bootstrap host operation failed"
	}
	return e.message
}

func newBootstrapHostFailure(code, message string, progress int) error {
	return &bootstrapHostFailure{code: code, message: message, progress: progress}
}

func safeBootstrapHostFailure(err error) *bootstrapHostFailure {
	var failure *bootstrapHostFailure
	if errors.As(err, &failure) &&
		failure != nil &&
		failure.progress >= 0 && failure.progress <= 100 &&
		safeBootstrapHostFailureCode(failure.code) {
		return failure
	}
	switch {
	case errors.Is(err, ErrHostMaintenanceActiveJob), errors.Is(err, ErrHostMaintenanceDraining):
		return &bootstrapHostFailure{
			code: "bootstrap_host_busy", message: "host has an active update job or policy replacement", progress: 10,
		}
	case errors.Is(err, ErrHostMaintenanceUnknownHost):
		return &bootstrapHostFailure{
			code: "bootstrap_host_unknown", message: "selected host is not configured on this updater", progress: 10,
		}
	default:
		return &bootstrapHostFailure{
			code: "bootstrap_internal_error", message: "host helper installation could not be completed", progress: 10,
		}
	}
}

func safeBootstrapSSHFailure(err error, fallbackCode, fallbackMessage string, progress int) error {
	var transportErr *SSHTransportError
	if errors.As(err, &transportErr) {
		switch transportErr.Code {
		case SSHErrorTimeout:
			return newBootstrapHostFailure(SSHErrorTimeout, "host SSH operation timed out", progress)
		case SSHErrorConnectionRefused:
			return newBootstrapHostFailure(SSHErrorConnectionRefused, "host SSH connection was refused", progress)
		case SSHErrorAuthFailed:
			return newBootstrapHostFailure(SSHErrorAuthFailed, "administrator SSH authentication failed", progress)
		case SSHErrorHostKeyMismatch:
			return newBootstrapHostFailure(SSHErrorHostKeyMismatch, "host SSH key did not match the registered key", progress)
		case SSHErrorRemoteHelperUnavailable:
			return newBootstrapHostFailure(SSHErrorRemoteHelperUnavailable, "remote bootstrap command is unavailable", progress)
		case SSHErrorRemoteConfigInvalid:
			return newBootstrapHostFailure(SSHErrorRemoteConfigInvalid, "remote bootstrap configuration was rejected", progress)
		}
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return newBootstrapHostFailure(SSHErrorTimeout, "host SSH operation timed out", progress)
	}
	return newBootstrapHostFailure(fallbackCode, fallbackMessage, progress)
}

func safeBootstrapHostFailureCode(code string) bool {
	switch code {
	case "bootstrap_profile_unsupported",
		"bootstrap_managed_key_missing",
		"bootstrap_inspection_failed",
		"bootstrap_config_failed",
		"bootstrap_artifact_failed",
		"bootstrap_install_failed",
		"bootstrap_probe_failed",
		"bootstrap_probe_mismatch",
		"bootstrap_host_busy",
		"bootstrap_host_unknown",
		"bootstrap_internal_error",
		SSHErrorTimeout,
		SSHErrorConnectionRefused,
		SSHErrorAuthFailed,
		SSHErrorHostKeyMismatch,
		SSHErrorRemoteHelperUnavailable,
		SSHErrorRemoteConfigInvalid:
		return true
	default:
		return false
	}
}

func validBootstrapTemporaryDirectory(stateDir, tempRoot string) bool {
	if strings.TrimSpace(stateDir) == "" || strings.TrimSpace(tempRoot) == "" ||
		!pathWithin(filepath.Clean(stateDir), filepath.Clean(tempRoot)) {
		return false
	}
	info, err := os.Lstat(tempRoot)
	return err == nil && privateJobDirectoryInfo(info)
}

const bootstrapPanelMaxAttempts = 3

func retryBootstrapPanelCall(ctx context.Context, call func() error) error {
	var err error
	for attempt := 0; attempt < bootstrapPanelMaxAttempts; attempt++ {
		err = call()
		if err == nil {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if !retryableBootstrapPanelError(err) || attempt+1 == bootstrapPanelMaxAttempts {
			return err
		}
		delay := time.Duration(attempt+1) * 25 * time.Millisecond
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return ctx.Err()
		case <-timer.C:
		}
	}
	return err
}

func retryableBootstrapPanelError(err error) bool {
	var httpErr *PanelHTTPError
	if !errors.As(err, &httpErr) {
		return true
	}
	return httpErr.Status >= 500 && httpErr.Status <= 599
}

func clearBootstrapAdministratorCredential(credential *BootstrapAdministratorCredential) {
	if credential == nil {
		return
	}
	clear(credential.PrivateKey)
	clear(credential.Passphrase)
	credential.PrivateKey = nil
	credential.Passphrase = nil
	credential.AdministratorUser = ""
}

func clearBootstrapJobClaim(claim *BootstrapJobClaim) {
	if claim == nil {
		return
	}
	for index := range claim.HostIDs {
		claim.HostIDs[index] = ""
	}
	claim.HostIDs = nil
	claim.Envelope = BootstrapCredentialEnvelope{}
	claim.LeaseToken = ""
	claim.ReleaseToken = ""
	claim.ID = ""
	claim.UpdaterID = ""
	claim.ExpectedRevision = 0
}
