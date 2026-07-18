package updateagent

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"time"

	controlversion "github.com/example/autostream-control-panel/internal/version"
)

type remoteHelperRuntime struct {
	runner        CommandRunner
	httpClient    *http.Client
	helperVersion string
	platformOS    string
	platformArch  string
	consumeGrant  func(context.Context, string, string, string, MutationGrantBinding, *http.Client) error
}

func defaultRemoteHelperRuntime() remoteHelperRuntime {
	return remoteHelperRuntime{
		runner: OSCommandRunner{NewProcessGroup: true}, helperVersion: controlversion.Current(),
		platformOS: runtime.GOOS, platformArch: runtime.GOARCH, consumeGrant: ConsumeMutationGrant,
	}
}

// ValidateRemoteHelperConfig validates the token-free host policy using the
// same root ownership and strict JSON checks as the forced-command endpoint.
func ValidateRemoteHelperConfig(path string) (HelperConfig, error) {
	cfg, err := LoadHelperConfig(path, true)
	if err != nil {
		return HelperConfig{}, err
	}
	if runtime.GOOS != "linux" {
		return HelperConfig{}, errors.New("remote helper requires Linux")
	}
	launcher, err := resolveSystemdRun()
	if err != nil {
		return HelperConfig{}, err
	}
	checkCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := validateSystemdRun(checkCtx, OSCommandRunner{}, launcher); err != nil {
		return HelperConfig{}, err
	}
	return cfg, nil
}

// RunRemoteHelperRPC serves the single forced SSH command. The exact original
// command is checked before stdin is read, so an unset or altered sudo
// environment fails closed. All request failures are mapped to allow-listed,
// credential-free RPC failures.
func RunRemoteHelperRPC(ctx context.Context, configPath, originalCommand string, input io.Reader, output io.Writer) error {
	if originalCommand != RemoteFixedCommand {
		return errors.New("remote RPC forced command rejected")
	}
	cfg, err := LoadHelperConfig(configPath, true)
	if err != nil {
		return errors.New("remote helper configuration rejected")
	}
	if runtime.GOOS != "linux" {
		return errors.New("remote helper platform rejected")
	}
	if _, err := resolveSystemdRun(); err != nil {
		return errors.New("remote helper launcher rejected")
	}
	request, err := DecodeRemoteRPCRequest(input)
	if err != nil {
		return EncodeRemoteRPCResponse(output, remoteFailure("invalid_request"))
	}
	response := dispatchRemoteHelperRequest(ctx, cfg, configPath, request, defaultRemoteHelperRuntime())
	return EncodeRemoteRPCResponse(output, response)
}

func handleRemoteHelperRequest(ctx context.Context, cfg HelperConfig, request RemoteRPCRequest, rt remoteHelperRuntime) RemoteRPCResponse {
	if err := request.Validate(); err != nil {
		return remoteFailure("invalid_request")
	}
	if request.Operation != "probe" {
		if failure := remoteHelperConfigBindingFailure(cfg, *request.Plan); failure != "" {
			return remoteFailure(failure)
		}
	}
	if rt.runner == nil {
		rt.runner = OSCommandRunner{NewProcessGroup: true}
	}
	if rt.consumeGrant == nil {
		rt.consumeGrant = ConsumeMutationGrant
	}
	if rt.helperVersion == "" {
		rt.helperVersion = controlversion.Current()
	}
	if rt.platformOS == "" {
		rt.platformOS = runtime.GOOS
	}
	if rt.platformArch == "" {
		rt.platformArch = runtime.GOARCH
	}
	if request.Operation == "probe" {
		return remoteProbeResponse(cfg, rt)
	}
	plan := *request.Plan
	target, failure := resolveRemoteTarget(cfg, plan, rt.platformOS, rt.platformArch)
	if failure != "" {
		return remoteFailure(failure)
	}
	if err := ensureRemoteStateDirectories(cfg); err != nil {
		return remoteFailure("state_unavailable")
	}
	secured, err := securePrivilegedTarget(target)
	if err != nil {
		return remoteFailure("target_unavailable")
	}
	unlock, err := acquireTargetLock(secured)
	if err != nil {
		return remoteFailure("target_busy")
	}
	defer unlock()

	ledger, err := loadRemoteMutationLedger(cfg, target.TargetID)
	if err != nil {
		return remoteFailure("state_invalid")
	}
	switch request.Operation {
	case "stage":
		return remoteStageRequest(ctx, cfg, secured, plan, request.ReleaseToken, ledger, rt)
	case "apply", "reconcile":
		return remoteMutationRequest(ctx, cfg, secured, plan, request.Operation, request.MutationGrant, ledger, rt)
	default:
		return remoteFailure("invalid_request")
	}
}

func remoteHelperConfigBindingFailure(cfg HelperConfig, plan RemotePlan) string {
	digest, err := cfg.SHA256()
	if err != nil {
		return "state_invalid"
	}
	if digest != plan.ConfigSHA256 {
		return "config_mismatch"
	}
	return ""
}

func resolveRemoteTarget(cfg HelperConfig, plan RemotePlan, platformOS, platformArch string) (Target, string) {
	if platformOS != "linux" || platformArch != cfg.Arch || plan.HostID != cfg.HostID {
		return Target{}, "target_mismatch"
	}
	target, ok := cfg.Target(plan.TargetID)
	if !ok || target.HostID != cfg.HostID || target.ServiceType != plan.ServiceType || target.DeploymentMode != plan.DeploymentMode {
		return Target{}, "target_mismatch"
	}
	return target, ""
}

func remoteProbeResponse(cfg HelperConfig, rt remoteHelperRuntime) RemoteRPCResponse {
	digest, err := cfg.SHA256()
	if err != nil {
		return remoteFailure("state_invalid")
	}
	targets := make([]RemoteProbeTarget, 0, len(cfg.Targets))
	for _, target := range cfg.Targets {
		targets = append(targets, RemoteProbeTarget{
			TargetID: target.TargetID, ServiceType: target.ServiceType,
			DeploymentMode: target.DeploymentMode, CurrentVersion: probeRemoteTargetVersion(target),
		})
	}
	probe := RemoteProbeResult{
		ProtocolVersion: RemoteProtocolVersion, HelperVersion: rt.helperVersion,
		HostID: cfg.HostID, OS: rt.platformOS, Arch: rt.platformArch,
		ConfigSHA256: digest, Targets: targets,
	}
	if err := probe.Validate(); err != nil {
		return remoteFailure("target_unavailable")
	}
	return RemoteRPCResponse{Version: RemoteProtocolVersion, Probe: &probe}
}

func probeRemoteTargetVersion(target Target) string {
	if target.DeploymentMode == ModeSystemd && target.Systemd != nil {
		_, _, current, err := currentRelease(target.Systemd.CurrentLink, target.Systemd.ReleaseRoot)
		if err == nil && versionPattern.MatchString(current) {
			return current
		}
		return ""
	}
	if target.DeploymentMode == ModeDocker && target.Docker != nil {
		if data, _, exists, err := readVersionEnv(target.Docker.VersionEnvFile); err == nil && exists {
			if current, _, err := parseVersionEnvPin(data, target.Docker.ImageVariable); err == nil {
				return current
			}
		}
		if versionPattern.MatchString(target.Docker.CurrentVersion) {
			return target.Docker.CurrentVersion
		}
	}
	return ""
}

// verifyRemotePlanCurrentVersion fences a remote operation to the exact
// deployed version observed when the immutable plan was issued. Remote plans
// never permit an unknown baseline because that could authorize a downgrade.
func verifyRemotePlanCurrentVersion(target Target, plan RemotePlan) error {
	planned := strings.TrimSpace(plan.CurrentVersion)
	if planned == "" {
		return errors.New("remote plan is missing its current version baseline")
	}
	actual, err := managedRemoteCurrentVersion(target)
	if err != nil {
		return errors.New("could not verify the planned current version")
	}
	return requirePlannedCurrentVersion(planned, actual)
}

func managedRemoteCurrentVersion(target Target) (string, error) {
	switch target.DeploymentMode {
	case ModeSystemd:
		if target.Systemd == nil {
			return "", errors.New("systemd target is unavailable")
		}
		current, _, version, err := currentRelease(target.Systemd.CurrentLink, target.Systemd.ReleaseRoot)
		if err != nil || current == "" || !versionPattern.MatchString(version) {
			return "", errors.New("managed systemd current release is unavailable")
		}
		return version, nil
	case ModeDocker:
		if target.Docker == nil {
			return "", errors.New("Docker target is unavailable")
		}
		data, _, exists, err := readVersionEnv(target.Docker.VersionEnvFile)
		if err != nil {
			return "", errors.New("read Docker version pin")
		}
		if exists {
			version, _, err := parseVersionEnvPin(data, target.Docker.ImageVariable)
			if err != nil {
				return "", errors.New("Docker version pin is invalid")
			}
			return version, nil
		}
		version := strings.TrimSpace(target.Docker.CurrentVersion)
		if !versionPattern.MatchString(version) {
			return "", errors.New("Docker current version is unavailable")
		}
		return version, nil
	default:
		return "", errors.New("unsupported deployment mode")
	}
}

func requirePlannedCurrentVersion(planned, actual string) error {
	planned = strings.TrimSpace(planned)
	if planned == "" {
		return nil
	}
	if !versionPattern.MatchString(planned) || !versionPattern.MatchString(strings.TrimSpace(actual)) || strings.TrimSpace(actual) != planned {
		return errors.New("managed target current version does not match the immutable plan")
	}
	return nil
}

func remoteStageRequest(ctx context.Context, cfg HelperConfig, target Target, plan RemotePlan, releaseToken RemoteSecret, ledger *remoteMutationLedger, rt remoteHelperRuntime) RemoteRPCResponse {
	var previousTerminalStage *remoteStage
	if ledger != nil {
		if ledger.JobID == plan.JobID && ledger.PlanSHA256 != plan.PlanSHA256 {
			return remoteFailure("plan_conflict")
		}
		if ledger.JobID == plan.JobID && ledger.PlanSHA256 == plan.PlanSHA256 {
			if ledger.State == remoteLedgerStaged && validateRemoteStage(cfg, target, plan, ledger.Stage) == nil {
				if err := verifyRemotePlanCurrentVersion(target, plan); err != nil {
					return remoteFailure("stage_failed")
				}
				stage := remoteStageResult(plan, *ledger.Stage)
				return RemoteRPCResponse{Version: RemoteProtocolVersion, Stage: &stage}
			}
			if ledger.State == remoteLedgerTerminal {
				return remoteFailure("already_terminal")
			}
			return remoteFailure("reconcile_required")
		}
		if ledger.State != remoteLedgerTerminal {
			return remoteFailure("reconcile_required")
		}
		previousTerminalStage = ledger.Stage
	}
	stage, err := prepareRemoteStage(ctx, cfg, target, plan, releaseToken, rt)
	if err != nil {
		return remoteFailure("stage_failed")
	}
	record := remoteMutationLedger{
		SchemaVersion: remoteLedgerSchemaVersion, JobID: plan.JobID, TargetID: plan.TargetID,
		PlanSHA256: plan.PlanSHA256, SessionID: plan.SessionID, LeaseGeneration: plan.LeaseGeneration,
		Intent: newRemoteMutationIntent(plan), Operation: "stage",
		State: remoteLedgerStaged, Stage: &stage,
	}
	if err := saveRemoteMutationLedger(cfg, record); err != nil {
		return remoteFailure("state_unavailable")
	}
	cleanupRemoteTerminalStage(cfg, previousTerminalStage, stage.RootDir)
	result := remoteStageResult(plan, stage)
	return RemoteRPCResponse{Version: RemoteProtocolVersion, Stage: &result}
}

func cleanupRemoteTerminalStage(cfg HelperConfig, oldStage *remoteStage, currentRoot string) {
	if oldStage == nil || oldStage.RootDir == "" || oldStage.RootDir == currentRoot {
		return
	}
	stagesRoot := filepath.Join(cfg.StateDir, "stages")
	relative, err := filepath.Rel(stagesRoot, oldStage.RootDir)
	if err != nil || relative == "." || filepath.IsAbs(relative) || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return
	}
	component := strings.Split(filepath.ToSlash(relative), "/")[0]
	if !remotePlanHashPattern.MatchString(component) {
		return
	}
	oldRoot := filepath.Join(stagesRoot, component)
	if pathWithin(stagesRoot, oldRoot) && !pathWithin(oldRoot, currentRoot) {
		_ = os.RemoveAll(oldRoot)
		_ = syncDirectory(stagesRoot)
	}
}

func remoteStageResult(plan RemotePlan, stage remoteStage) RemoteStageResult {
	return RemoteStageResult{
		Status: "staged", SessionID: plan.SessionID, PlanSHA256: plan.PlanSHA256,
		ArtifactDigest: strings.TrimPrefix(normalizeDigest(stage.ArtifactDigest), "sha256:"),
	}
}

func prepareRemoteStage(ctx context.Context, cfg HelperConfig, target Target, plan RemotePlan, releaseToken RemoteSecret, rt remoteHelperRuntime) (remoteStage, error) {
	if err := verifyRemotePlanCurrentVersion(target, plan); err != nil {
		return remoteStage{}, err
	}
	if err := gcAgedRemoteStagePartials(cfg, time.Now()); err != nil {
		return remoteStage{}, err
	}
	stageRoot := filepath.Join(cfg.StateDir, "stages", remoteStableKey(plan.JobID, plan.PlanSHA256))
	if !pathWithin(filepath.Join(cfg.StateDir, "stages"), stageRoot) {
		return remoteStage{}, errors.New("stage path escaped state directory")
	}
	if err := os.RemoveAll(stageRoot); err != nil {
		return remoteStage{}, errors.New("clear incomplete stage")
	}
	partial, err := os.MkdirTemp(filepath.Join(cfg.StateDir, "stages"), ".partial-")
	if err != nil {
		return remoteStage{}, errors.New("create stage")
	}
	committed := false
	defer func() {
		if !committed {
			_ = os.RemoveAll(partial)
		}
	}()
	downloader := ReleaseDownloader{Client: rt.httpClient, Token: releaseToken.Reveal()}
	var stage remoteStage
	switch target.DeploymentMode {
	case ModeSystemd:
		downloaded, err := downloader.Download(ctx, target.ServiceType, plan.TargetVersion, cfg.Arch, filepath.Join(partial, "artifact"))
		if err != nil || normalizeDigest(downloaded.SHA256) != normalizeDigest(plan.ArtifactDigest) {
			return remoteStage{}, errors.New("systemd release digest mismatch")
		}
		rel, err := filepath.Rel(partial, downloaded.RootDir)
		if err != nil || rel == "." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return remoteStage{}, errors.New("systemd release stage is invalid")
		}
		stage = remoteStage{RootDir: filepath.Join(stageRoot, rel), ArtifactDigest: downloaded.SHA256, ExpectedVersion: plan.ExpectedVersion}
		if err := preflightRemoteSystemdStage(ctx, target, plan, downloaded.RootDir, &stage, rt.runner); err != nil {
			return remoteStage{}, err
		}
	case ModeDocker:
		resolved, err := downloader.ResolveDockerRelease(ctx, plan.TargetVersion, target.ServiceType, target.Docker.ImageRepo, target.Docker.Channel, partial)
		if err != nil || resolved.SourceVersion != plan.ExpectedVersion || normalizeDigest(resolved.ManifestDigest) != normalizeDigest(plan.ExpectedImageDigest) || normalizeDigest(resolved.PlatformDigest) != normalizeDigest(plan.ExpectedPlatformDigest) || normalizeDigest(resolved.ManifestSHA256) != normalizeDigest(plan.ArtifactDigest) {
			return remoteStage{}, errors.New("Docker release binding mismatch")
		}
		if err := verifyComposeModel(ctx, rt.runner, target.Docker, composeArgs(target.Docker, "")); err != nil {
			return remoteStage{}, err
		}
		digestRef := target.Docker.ImageRepo + "@" + plan.ExpectedPlatformDigest
		overridePath := filepath.Join(partial, "compose-override.json")
		if err := writeDockerOverride(overridePath, target.Docker.Service, digestRef); err != nil {
			return remoteStage{}, err
		}
		base := composeArgs(target.Docker, overridePath)
		if err := verifyComposeConfig(ctx, rt.runner, target.Docker, base, digestRef, filepath.Join(partial, "compose-frozen.json")); err != nil {
			return remoteStage{}, err
		}
		// Release resolution and compose validation can be slow. Capture the
		// complete rollback baseline immediately before the remaining stage side
		// effects; apply must see this exact baseline again.
		baselineCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		baseline, baselineErr := observeDockerMutationBaseline(baselineCtx, target, rt.runner)
		cancel()
		if baselineErr != nil {
			return remoteStage{}, baselineErr
		}
		if err := requirePlannedCurrentVersion(plan.CurrentVersion, baseline.Baseline.BundleVersion); err != nil {
			return remoteStage{}, err
		}
		if err := runFixedCommand(ctx, rt.runner, target.BackupArgv); err != nil {
			return remoteStage{}, errors.New("backup configured Docker target")
		}
		if _, err := rt.runner.Run(ctx, target.Docker.ProjectDir, dockerCommandEnv(), target.Docker.DockerPath, "pull", digestRef); err != nil {
			return remoteStage{}, errors.New("pull trusted Docker image")
		}
		imageOut, err := rt.runner.Run(ctx, target.Docker.ProjectDir, dockerCommandEnv(), target.Docker.DockerPath, "image", "inspect", "--format={{.Id}}", digestRef)
		imageID := strings.ToLower(strings.TrimSpace(imageOut))
		if err != nil || !digestPattern.MatchString(imageID) {
			return remoteStage{}, errors.New("inspect trusted Docker image")
		}
		repoDigests, err := rt.runner.Run(ctx, target.Docker.ProjectDir, dockerCommandEnv(), target.Docker.DockerPath, "image", "inspect", "--format={{json .RepoDigests}}", digestRef)
		if err != nil || !repositoryHasDigest(repoDigests, target.Docker.ImageRepo, plan.ExpectedPlatformDigest) {
			return remoteStage{}, errors.New("trusted Docker image digest mismatch")
		}
		stage = remoteStage{
			RootDir: stageRoot, ArtifactDigest: resolved.ManifestSHA256,
			ExpectedVersion: resolved.SourceVersion, ExpectedImageDigest: resolved.ManifestDigest,
			ExpectedPlatformDigest: resolved.PlatformDigest, ImageID: imageID, DockerBaseline: &baseline.Baseline,
		}
	default:
		return remoteStage{}, errors.New("unsupported deployment mode")
	}
	if err := os.Rename(partial, stageRoot); err != nil {
		return remoteStage{}, errors.New("commit release stage")
	}
	committed = true
	if err := syncDirectory(filepath.Dir(stageRoot)); err != nil {
		return remoteStage{}, errors.New("sync release stage")
	}
	return stage, nil
}

func gcAgedRemoteStagePartials(cfg HelperConfig, now time.Time) error {
	stagesRoot := filepath.Join(cfg.StateDir, "stages")
	dir, err := os.Open(stagesRoot)
	if err != nil {
		return errors.New("open remote stage directory for cleanup")
	}
	defer dir.Close()
	const maxScanned, maxRemoved = 256, 32
	entries, err := dir.ReadDir(maxScanned + 1)
	if err != nil && !errors.Is(err, io.EOF) {
		return errors.New("scan remote stage directory for cleanup")
	}
	if len(entries) > maxScanned {
		entries = entries[:maxScanned]
	}
	removed := 0
	for _, entry := range entries {
		if removed >= maxRemoved || !strings.HasPrefix(entry.Name(), ".partial-") {
			continue
		}
		candidate := filepath.Join(stagesRoot, entry.Name())
		if !pathWithin(stagesRoot, candidate) {
			continue
		}
		info, statErr := os.Lstat(candidate)
		if statErr != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || !isRootOwner(info) || (!now.IsZero() && now.Sub(info.ModTime()) <= remoteMutationWaitLimit+5*time.Minute) {
			continue
		}
		// A transient worker is force-stopped at remoteMutationWaitLimit, and a
		// committed/ledger-referenced stage never retains the .partial- prefix.
		// Therefore only an aged, structurally uncommitted directory reaches this
		// removal path. Root ownership prevents an unprivileged swap during GC.
		if err := os.RemoveAll(candidate); err != nil {
			return errors.New("remove abandoned remote stage")
		}
		removed++
	}
	if removed > 0 {
		return syncDirectory(stagesRoot)
	}
	return nil
}

func validateRemoteStage(cfg HelperConfig, target Target, plan RemotePlan, stage *remoteStage) error {
	if stage == nil || !filepath.IsAbs(stage.RootDir) || !pathWithin(filepath.Join(cfg.StateDir, "stages"), stage.RootDir) || normalizeDigest(stage.ArtifactDigest) != normalizeDigest(plan.ArtifactDigest) {
		return errors.New("staged release binding is invalid")
	}
	info, err := os.Lstat(stage.RootDir)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || !isRootOwner(info) {
		return errors.New("staged release is unavailable")
	}
	if target.DeploymentMode == ModeDocker {
		if stage.ExpectedVersion != plan.ExpectedVersion || normalizeDigest(stage.ExpectedImageDigest) != normalizeDigest(plan.ExpectedImageDigest) || normalizeDigest(stage.ExpectedPlatformDigest) != normalizeDigest(plan.ExpectedPlatformDigest) || !digestPattern.MatchString(stage.ImageID) || stage.DockerBaseline == nil || stage.DockerBaseline.validate() != nil || requirePlannedCurrentVersion(plan.CurrentVersion, stage.DockerBaseline.BundleVersion) != nil {
			return errors.New("staged Docker release binding is invalid")
		}
	}
	if target.DeploymentMode == ModeSystemd {
		if stage.ExpectedVersion != plan.ExpectedVersion || !filepath.IsAbs(stage.NewRelease) || !filepath.IsAbs(stage.PreviousRelease) || !digestPattern.MatchString(normalizeDigest(stage.PreviousDigest)) || !versionPattern.MatchString(stage.PreviousVersion) || requirePlannedCurrentVersion(plan.CurrentVersion, stage.PreviousVersion) != nil {
			return errors.New("staged systemd release binding is invalid")
		}
	}
	return nil
}

func preflightRemoteSystemdStage(ctx context.Context, target Target, plan RemotePlan, artifactRoot string, stage *remoteStage, runner CommandRunner) error {
	systemd := target.Systemd
	resolved, err := filepath.EvalSymlinks(artifactRoot)
	if err != nil || !filepath.IsAbs(resolved) {
		return errors.New("staged systemd artifact path is invalid")
	}
	if err := VerifyInnerChecksums(resolved); err != nil {
		return errors.New("staged systemd artifact checksum verification failed")
	}
	for _, rel := range append([]string{systemd.BinaryPath}, systemd.RequiredPaths...) {
		info, err := os.Stat(filepath.Join(resolved, filepath.FromSlash(rel)))
		if err != nil || (rel == systemd.BinaryPath && !info.Mode().IsRegular()) {
			return errors.New("staged systemd artifact is incomplete")
		}
	}
	// The state tree intentionally remains root-only. The root helper chdirs to
	// the already-verified artifact root before runuser drops privileges, so a
	// safe relative binary path lets the smoke user execute the artifact without
	// granting traversal into state_dir, ledger, requests, or results.
	smokeBinary := "." + string(filepath.Separator) + filepath.FromSlash(systemd.BinaryPath)
	smokeCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	output, smokeErr := runner.Run(smokeCtx, resolved, nil, systemd.RunuserPath, "-u", systemd.SmokeUser, "--", smokeBinary, "--version")
	cancel()
	if smokeErr != nil || !versionMatches(output, plan.TargetVersion) {
		return errors.New("staged systemd binary smoke check failed")
	}
	previous, previousDigest, previousVersion, err := currentRelease(systemd.CurrentLink, systemd.ReleaseRoot)
	if err != nil || previous == "" {
		return errors.New("managed systemd release bootstrap is required")
	}
	if err := requirePlannedCurrentVersion(plan.CurrentVersion, previousVersion); err != nil {
		return err
	}
	if err := runFixedCommand(ctx, runner, target.BackupArgv); err != nil {
		return errors.New("backup configured systemd target")
	}
	if err := verifyManagedReleaseChecksums(previous); err != nil {
		return errors.New("previous systemd release is not rollback-safe")
	}
	for _, rel := range append([]string{systemd.BinaryPath}, systemd.RequiredPaths...) {
		info, err := os.Stat(filepath.Join(previous, filepath.FromSlash(rel)))
		if err != nil || (rel == systemd.BinaryPath && !info.Mode().IsRegular()) {
			return errors.New("previous systemd release is incomplete")
		}
	}
	if err := firstError(verifySystemdProcess(ctx, target, previous, runner), verifyTarget(ctx, target, previousVersion)); err != nil {
		return errors.New("previous systemd release is not healthy")
	}
	newRelease := filepath.Join(systemd.ReleaseRoot, plan.TargetVersion+"-"+strings.ToLower(plan.ArtifactDigest[:12]))
	if !pathWithin(systemd.ReleaseRoot, newRelease) {
		return errors.New("systemd release path is invalid")
	}
	stage.NewRelease = newRelease
	stage.PreviousRelease = previous
	stage.PreviousDigest = normalizeDigest(previousDigest)
	stage.PreviousVersion = previousVersion
	return nil
}

func applyRemoteStagedSystemd(ctx context.Context, target Target, plan ApplyPlan, stage remoteStage, runner CommandRunner, mutationGate func(context.Context) error) (ApplyResult, error) {
	return applyRemoteStagedSystemdWithVerifier(ctx, target, plan, stage, runner, mutationGate, verifyRemoteSystemdBaseline)
}

type remoteSystemdBaselineVerifier func(context.Context, Target, remoteStage, CommandRunner) error

func applyRemoteStagedSystemdWithVerifier(ctx context.Context, target Target, plan ApplyPlan, stage remoteStage, runner CommandRunner, mutationGate func(context.Context) error, verifyBaseline remoteSystemdBaselineVerifier) (ApplyResult, error) {
	if plan.StageDir == "" || !filepath.IsAbs(plan.StageDir) || filepath.Clean(plan.StageDir) != filepath.Clean(stage.RootDir) || len(plan.ArtifactDigest) != 64 || mutationGate == nil {
		return ApplyResult{}, errors.New("remote systemd stage is incomplete")
	}
	if verifyBaseline == nil {
		return ApplyResult{}, errors.New("remote systemd baseline verifier is unavailable")
	}
	if err := requirePlannedCurrentVersion(plan.CurrentVersion, stage.PreviousVersion); err != nil {
		return ApplyResult{}, err
	}
	preflightCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	err := verifyBaseline(preflightCtx, target, stage, runner)
	cancel()
	if err != nil {
		return ApplyResult{}, err
	}
	checkpoint, err := loadCheckpoint(target)
	if err != nil {
		return ApplyResult{}, err
	}
	if checkpoint != nil && checkpoint.Phase != "succeeded" && checkpoint.Phase != "rolled_back" {
		return ApplyResult{}, errors.New("an interrupted systemd checkpoint requires reconcile")
	}
	if err := mutationGate(ctx); err != nil {
		return ApplyResult{}, err
	}
	// Grant consumption is an external round trip. Re-read both the durable
	// checkpoint state and the complete running rollback baseline immediately
	// afterward, before installing a release or touching the service.
	afterGrantCheckpoint, err := loadCheckpoint(target)
	if err != nil || !reflect.DeepEqual(checkpoint, afterGrantCheckpoint) {
		return ApplyResult{}, errors.New("systemd checkpoint changed while consuming the mutation grant")
	}
	postGrantCtx, postGrantCancel := context.WithTimeout(ctx, 10*time.Second)
	err = verifyBaseline(postGrantCtx, target, stage, runner)
	postGrantCancel()
	if err != nil {
		return ApplyResult{}, errors.New("systemd target changed while consuming the mutation grant")
	}
	afterVerifyCheckpoint, err := loadCheckpoint(target)
	if err != nil || !reflect.DeepEqual(checkpoint, afterVerifyCheckpoint) {
		return ApplyResult{}, errors.New("systemd checkpoint changed while verifying the mutation baseline")
	}
	if checkpoint != nil {
		if err := clearCheckpoint(target); err != nil {
			return ApplyResult{}, err
		}
	}
	if err := installReleaseTree(plan.StageDir, stage.NewRelease, plan.ArtifactDigest, plan.TargetVersion); err != nil {
		return ApplyResult{}, err
	}
	if err := verifyManagedReleaseChecksums(stage.NewRelease); err != nil {
		return ApplyResult{}, errors.New("installed systemd release integrity check failed")
	}
	for _, rel := range append([]string{target.Systemd.BinaryPath}, target.Systemd.RequiredPaths...) {
		info, err := os.Stat(filepath.Join(stage.NewRelease, filepath.FromSlash(rel)))
		if err != nil || (rel == target.Systemd.BinaryPath && !info.Mode().IsRegular()) {
			return ApplyResult{}, errors.New("installed systemd release is incomplete")
		}
	}
	// Installing and checksumming a large tree can outlive the earlier
	// post-grant observation. Perform one final short baseline/checkpoint check
	// immediately before commitRemoteSystemdMutation persists a new checkpoint
	// and stops the service.
	finalCheckpoint, err := loadCheckpoint(target)
	if err != nil || finalCheckpoint != nil {
		return ApplyResult{}, errors.New("systemd checkpoint changed before service mutation")
	}
	finalCtx, finalCancel := context.WithTimeout(ctx, 10*time.Second)
	err = verifyBaseline(finalCtx, target, stage, runner)
	finalCancel()
	if err != nil {
		return ApplyResult{}, errors.New("systemd target changed before service mutation")
	}
	finalCheckpoint, err = loadCheckpoint(target)
	if err != nil || finalCheckpoint != nil {
		return ApplyResult{}, errors.New("systemd checkpoint changed during final baseline verification")
	}
	return commitRemoteSystemdMutation(ctx, target, plan, stage, runner)
}

func verifyRemoteSystemdBaseline(ctx context.Context, target Target, stage remoteStage, runner CommandRunner) error {
	if err := verifyRemoteSystemdStagedArtifact(ctx, target, stage, runner); err != nil {
		return errors.New("staged systemd artifact changed after staging")
	}
	current, digest, version, err := currentRelease(target.Systemd.CurrentLink, target.Systemd.ReleaseRoot)
	if err != nil || current != filepath.Clean(stage.PreviousRelease) || normalizeDigest(digest) != normalizeDigest(stage.PreviousDigest) || version != stage.PreviousVersion {
		return errors.New("systemd target changed after staging")
	}
	if err := firstError(
		verifyManagedReleaseChecksums(current),
		verifySystemdProcess(ctx, target, current, runner),
		verifyRemoteHealthyVersionExact(ctx, target, version),
	); err != nil {
		return errors.New("staged systemd rollback baseline is no longer healthy and exact")
	}
	return nil
}

func verifyRemoteSystemdStagedArtifact(ctx context.Context, target Target, stage remoteStage, runner CommandRunner) error {
	if target.Systemd == nil || runner == nil || !filepath.IsAbs(stage.RootDir) || !versionPattern.MatchString(stage.ExpectedVersion) {
		return errors.New("staged systemd artifact binding is invalid")
	}
	resolved, err := filepath.EvalSymlinks(stage.RootDir)
	if err != nil || filepath.Clean(resolved) != filepath.Clean(stage.RootDir) {
		return errors.New("staged systemd artifact path changed")
	}
	if err := VerifyInnerChecksums(resolved); err != nil {
		return errors.New("staged systemd artifact integrity changed")
	}
	for _, rel := range append([]string{target.Systemd.BinaryPath}, target.Systemd.RequiredPaths...) {
		path := filepath.Join(resolved, filepath.FromSlash(rel))
		info, err := os.Lstat(path)
		if err != nil || info.Mode()&os.ModeSymlink != 0 || (!info.IsDir() && !info.Mode().IsRegular()) {
			return errors.New("staged systemd artifact required path changed")
		}
		if rel == target.Systemd.BinaryPath && (!info.Mode().IsRegular() || info.Mode().Perm() != 0o755) {
			return errors.New("staged systemd binary mode changed")
		}
	}
	smokeBinary := "." + string(filepath.Separator) + filepath.FromSlash(target.Systemd.BinaryPath)
	output, err := runner.Run(ctx, resolved, nil, target.Systemd.RunuserPath, "-u", target.Systemd.SmokeUser, "--", smokeBinary, "--version")
	if err != nil || !versionMatches(output, stage.ExpectedVersion) {
		return errors.New("staged systemd binary smoke check changed")
	}
	return nil
}

func verifyRemoteHealthyVersionExact(ctx context.Context, target Target, expected string) error {
	actual, err := readHealthyTargetVersion(ctx, target)
	if err != nil || strings.TrimSpace(actual) != expected {
		return errors.New("managed target health version does not exactly match its release marker")
	}
	return nil
}

func commitRemoteSystemdMutation(ctx context.Context, target Target, plan ApplyPlan, stage remoteStage, runner CommandRunner) (ApplyResult, error) {
	systemd := target.Systemd
	checkpoint := updateCheckpoint{
		JobID: plan.JobID, TargetID: target.TargetID, DeploymentMode: ModeSystemd, Phase: "prepared",
		TargetVersion: plan.TargetVersion, TargetDigest: normalizeDigest(plan.ArtifactDigest), NewRelease: stage.NewRelease,
		PreviousRelease: stage.PreviousRelease, PreviousDigest: normalizeDigest(stage.PreviousDigest), PreviousVersion: stage.PreviousVersion,
	}
	if err := saveCheckpoint(target, checkpoint); err != nil {
		return ApplyResult{}, errors.New("persist systemd update checkpoint")
	}
	if _, err := runner.Run(ctx, "", nil, systemd.SystemctlPath, "stop", systemd.Unit); err != nil {
		return ApplyResult{}, err
	}
	checkpoint.Phase = "stopped"
	if err := saveCheckpoint(target, checkpoint); err != nil {
		_, _ = runner.Run(context.Background(), "", nil, systemd.SystemctlPath, "start", systemd.Unit)
		return ApplyResult{}, errors.New("persist stopped systemd checkpoint")
	}
	if err := switchSymlink(systemd.CurrentLink, stage.NewRelease, plan.JobID); err != nil {
		_, _ = runner.Run(ctx, "", nil, systemd.SystemctlPath, "start", systemd.Unit)
		return ApplyResult{}, err
	}
	checkpoint.Phase = "switched"
	if err := saveCheckpoint(target, checkpoint); err != nil {
		return ApplyResult{}, errors.New("persist switched systemd checkpoint")
	}
	startOutput, startErr := runner.Run(ctx, "", nil, systemd.SystemctlPath, "start", systemd.Unit)
	checkpoint.Phase = "started"
	checkpointErr := saveCheckpoint(target, checkpoint)
	var verifyErr error
	if startErr == nil {
		verifyErr = firstError(checkpointErr, verifySystemdProcess(ctx, target, stage.NewRelease, runner), verifyTarget(ctx, target, plan.TargetVersion))
	}
	if startErr == nil && verifyErr == nil {
		checkpoint.Phase = "succeeded"
		if err := saveCheckpoint(target, checkpoint); err != nil {
			return ApplyResult{}, errors.New("persist terminal systemd checkpoint")
		}
		return ApplyResult{Status: "succeeded", ArtifactDigest: normalizeDigest(plan.ArtifactDigest), PreviousDigest: normalizeDigest(stage.PreviousDigest)}, nil
	}
	rollbackCtx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if err := rollbackSystemd(rollbackCtx, target, stage.PreviousRelease, stage.PreviousVersion, plan.JobID, runner); err != nil {
		return ApplyResult{Status: "failed", ArtifactDigest: normalizeDigest(plan.ArtifactDigest), PreviousDigest: normalizeDigest(stage.PreviousDigest)}, errors.New("systemd update and rollback failed")
	}
	checkpoint.Phase = "rolled_back"
	if err := saveCheckpoint(target, checkpoint); err != nil {
		return ApplyResult{Status: "failed"}, errors.New("persist rolled-back systemd checkpoint")
	}
	_ = startOutput
	return ApplyResult{Status: "rolled_back", ArtifactDigest: normalizeDigest(plan.ArtifactDigest), PreviousDigest: normalizeDigest(stage.PreviousDigest), RolledBack: true, Message: "systemd update was rolled back"}, nil
}

func remoteMutationRequest(ctx context.Context, cfg HelperConfig, target Target, plan RemotePlan, operation string, grant RemoteSecret, ledger *remoteMutationLedger, rt remoteHelperRuntime) RemoteRPCResponse {
	if ledger == nil {
		return remoteFailure("stage_required")
	}
	if failure := remoteLedgerRequestFailure(*ledger, plan, operation); failure != "" {
		return remoteFailure(failure)
	}
	if ledger.State == remoteLedgerTerminal {
		result, ok := bindRemoteApplyResult(plan, *ledger.Result)
		if !ok {
			return remoteFailure("state_invalid")
		}
		return RemoteRPCResponse{Version: RemoteProtocolVersion, Result: &result, SessionID: plan.SessionID, PlanSHA256: plan.PlanSHA256}
	}
	if err := validateRemoteStage(cfg, target, plan, ledger.Stage); err != nil {
		return remoteFailure("stage_invalid")
	}
	if operation == "apply" && ledger.State != remoteLedgerStaged {
		return remoteFailure("reconcile_required")
	}
	if operation == "reconcile" && ledger.State == remoteLedgerStaged {
		result, _ := bindRemoteApplyResult(plan, ApplyResult{Status: "rolled_back", RolledBack: true, Message: "staged release was not applied"})
		terminal := *ledger
		terminal.Operation, terminal.SessionID, terminal.State, terminal.Result = operation, plan.SessionID, remoteLedgerTerminal, &result
		terminal.PlanSHA256, terminal.LeaseGeneration = plan.PlanSHA256, plan.LeaseGeneration
		if err := saveRemoteMutationLedger(cfg, terminal); err != nil {
			return remoteFailure("state_unavailable")
		}
		return RemoteRPCResponse{Version: RemoteProtocolVersion, Result: &result, SessionID: plan.SessionID, PlanSHA256: plan.PlanSHA256}
	}

	working := *ledger
	working.Operation = operation
	working.SessionID = plan.SessionID
	working.PlanSHA256 = plan.PlanSHA256
	working.LeaseGeneration = plan.LeaseGeneration
	gateCalled := false
	gateDeadline := time.Now().Add(30 * time.Second)
	gate := func(gateCtx context.Context) error {
		if gateCalled {
			return errors.New("mutation gate was invoked more than once")
		}
		remaining := time.Until(gateDeadline)
		if remaining <= 0 {
			return errors.New("mutation grant preflight deadline exceeded")
		}
		gateCalled = true
		working.State = remoteLedgerGrantConsuming
		working.Result = nil
		if err := saveRemoteMutationLedger(cfg, working); err != nil {
			return errors.New("persist grant consumption fence")
		}
		binding := MutationGrantBinding{
			LeaseGeneration: plan.LeaseGeneration, HostID: plan.HostID, TargetID: plan.TargetID,
			TargetVersion: plan.TargetVersion, DeploymentMode: plan.DeploymentMode,
			Operation: operation, PlanSHA256: plan.PlanSHA256, SessionID: plan.SessionID,
		}
		consumeCtx, cancel := context.WithTimeout(gateCtx, remaining)
		defer cancel()
		if err := rt.consumeGrant(consumeCtx, cfg.PanelURL, plan.JobID, grant.Reveal(), binding, rt.httpClient); err != nil {
			return errors.New("mutation grant rejected")
		}
		working.State = remoteLedgerGrantConsumed
		if err := saveRemoteMutationLedger(cfg, working); err != nil {
			return errors.New("persist consumed mutation grant")
		}
		working.State = remoteLedgerMutating
		if err := saveRemoteMutationLedger(cfg, working); err != nil {
			return errors.New("persist mutation fence")
		}
		return nil
	}

	applyPlan := plan.ApplyPlan()
	applyPlan.StageDir = ledger.Stage.RootDir
	applyPlan.ArtifactDigest = strings.TrimPrefix(normalizeDigest(ledger.Stage.ArtifactDigest), "sha256:")
	var result ApplyResult
	var err error
	if operation == "apply" {
		if target.DeploymentMode == ModeSystemd {
			result, err = applyRemoteStagedSystemd(ctx, target, applyPlan, *ledger.Stage, rt.runner, gate)
		} else {
			result, err = applyDockerWithGateAndBaseline(ctx, target, applyPlan, rt.runner, gate, true, ledger.Stage.DockerBaseline, ledger.Stage.ImageID)
		}
	} else {
		// Recovery grants are consumed before potentially slow unhealthy-state
		// inspection. The inspection itself is hard-bounded so rollback begins
		// within 30 seconds of consumption; the grant is never allowed to expire
		// while a 90-second health loop is still deciding whether to mutate.
		if err = gate(ctx); err == nil {
			inspectCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
			defer cancel()
			if target.DeploymentMode == ModeSystemd {
				result, err = reconcileSystemdWithGate(inspectCtx, target, applyPlan, rt.runner, nil)
			} else {
				result, err = reconcileDockerWithGate(inspectCtx, target, applyPlan, rt.runner, nil)
			}
		}
	}
	if err != nil {
		if gateCalled {
			working.State = remoteLedgerAmbiguous
			working.Result = nil
			_ = saveRemoteMutationLedger(cfg, working)
			return remoteFailure("reconcile_required")
		}
		return remoteFailure("mutation_precondition_failed")
	}
	var bound bool
	result, bound = bindRemoteApplyResult(plan, result)
	if !bound {
		if gateCalled {
			working.State = remoteLedgerAmbiguous
			working.Result = nil
			_ = saveRemoteMutationLedger(cfg, working)
		}
		return remoteFailure("reconcile_required")
	}
	if result.Status != "succeeded" && result.Status != "rolled_back" {
		if gateCalled {
			working.State = remoteLedgerAmbiguous
			working.Result = nil
			_ = saveRemoteMutationLedger(cfg, working)
		}
		return remoteFailure("reconcile_required")
	}
	working.State = remoteLedgerTerminal
	working.Result = &result
	if err := saveRemoteMutationLedger(cfg, working); err != nil {
		return remoteFailure("reconcile_required")
	}
	return RemoteRPCResponse{Version: RemoteProtocolVersion, Result: &result, SessionID: plan.SessionID, PlanSHA256: plan.PlanSHA256}
}

func remoteLedgerRequestFailure(ledger remoteMutationLedger, plan RemotePlan, operation string) string {
	if ledger.JobID != plan.JobID {
		if ledger.State != remoteLedgerTerminal {
			return "reconcile_required"
		}
		return "stage_required"
	}
	if !ledger.Intent.matches(plan) {
		return "plan_conflict"
	}
	if operation == "apply" {
		if ledger.PlanSHA256 != plan.PlanSHA256 || ledger.SessionID != plan.SessionID || ledger.LeaseGeneration != plan.LeaseGeneration {
			return "plan_conflict"
		}
	} else if operation == "reconcile" {
		if plan.LeaseGeneration < ledger.LeaseGeneration {
			return "plan_conflict"
		}
	} else {
		return "invalid_request"
	}
	return ""
}

func sanitizeRemoteApplyResult(result ApplyResult) ApplyResult {
	clean := ApplyResult{
		Status: result.Status, ArtifactDigest: normalizeDigest(result.ArtifactDigest),
		PreviousDigest: normalizeDigest(result.PreviousDigest), RolledBack: result.RolledBack,
	}
	switch clean.Status {
	case "succeeded":
		clean.Message = "target update completed and was verified"
	case "rolled_back":
		clean.Message = "previous target state was restored and verified"
	}
	return clean
}

func bindRemoteApplyResult(plan RemotePlan, result ApplyResult) (ApplyResult, bool) {
	expected := normalizeDigest(plan.ResultArtifactDigest())
	if expected == "" || (result.ArtifactDigest != "" && normalizeDigest(result.ArtifactDigest) != expected) {
		return ApplyResult{}, false
	}
	result.ArtifactDigest = expected
	return sanitizeRemoteApplyResult(result), true
}

func remoteFailure(code string) RemoteRPCResponse {
	messages := map[string]string{
		"invalid_request":              "remote request was rejected",
		"target_mismatch":              "request does not match this host target",
		"config_mismatch":              "request does not match the current helper policy",
		"target_unavailable":           "configured target is unavailable",
		"target_busy":                  "another target operation is active",
		"state_unavailable":            "durable host state is unavailable",
		"state_invalid":                "durable host state requires operator review",
		"stage_failed":                 "release staging failed",
		"stage_required":               "the immutable release plan must be staged first",
		"stage_invalid":                "the staged release no longer matches the plan",
		"plan_conflict":                "job identity was reused with a different plan",
		"reconcile_required":           "host state is ambiguous and requires reconcile",
		"already_terminal":             "the update plan is already terminal",
		"mutation_precondition_failed": "target precondition validation failed",
		"launcher_unavailable":         "detached host execution is unavailable",
		"operation_continues":          "host operation continues and must be recovered by status or reconcile",
	}
	message, ok := messages[code]
	if !ok {
		code, message = "internal_error", "remote helper could not complete the request"
	}
	return RemoteRPCResponse{Version: RemoteProtocolVersion, Error: &RemoteRPCFailure{Code: code, Message: message}}
}

func (r remoteHelperRuntime) String() string {
	return fmt.Sprintf("remoteHelperRuntime{platform:%s/%s}", r.platformOS, r.platformArch)
}
