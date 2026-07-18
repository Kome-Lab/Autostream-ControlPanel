package updateagent

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

type panicRemoteReader struct{}

func (panicRemoteReader) Read([]byte) (int, error) { panic("stdin must not be read") }

type remoteDockerBaselineRunner struct {
	calls       []commandCall
	afterGate   bool
	mutation    string
	containerID string
	imageID     string
	repoDigest  string
	targetImage string
	targetRepo  string
}

type remoteSmokeCaptureRunner struct{ calls []commandCall }

func (r *remoteSmokeCaptureRunner) Run(_ context.Context, dir string, env []string, name string, args ...string) (string, error) {
	r.calls = append(r.calls, commandCall{dir: dir, env: append([]string(nil), env...), name: name, args: append([]string(nil), args...)})
	return "autostream-worker v1.1.0\n", nil
}

func (r *remoteDockerBaselineRunner) Run(_ context.Context, dir string, env []string, name string, args ...string) (string, error) {
	r.calls = append(r.calls, commandCall{dir: dir, env: append([]string(nil), env...), name: name, args: append([]string(nil), args...)})
	joined := strings.Join(args, " ")
	switch {
	case len(args) > 0 && args[0] == "ps":
		if r.afterGate && r.mutation == "container" {
			return r.containerID + "-changed\n", nil
		}
		return r.containerID + "\n", nil
	case len(args) > 0 && args[0] == "inspect":
		if r.afterGate && r.mutation == "image" {
			return "sha256:" + strings.Repeat("f", 64) + "\n", nil
		}
		return r.imageID + "\n", nil
	case len(args) > 2 && args[0] == "image" && args[1] == "inspect" && strings.Contains(joined, "RepoDigests"):
		ref := args[len(args)-1]
		if strings.Contains(ref, "@sha256:") {
			repo := r.targetRepo
			if r.afterGate && r.mutation == "target_repo" {
				repo = "sha256:" + strings.Repeat("7", 64)
			}
			return `["ghcr.io/kome-lab/autostream-docker/worker@` + repo + `"]`, nil
		}
		repo := r.repoDigest
		if r.afterGate && r.mutation == "repo" {
			repo = "sha256:" + strings.Repeat("9", 64)
		}
		return `["ghcr.io/kome-lab/autostream-docker/worker@` + repo + `"]`, nil
	case len(args) > 1 && args[0] == "image" && args[1] == "inspect":
		imageID := r.targetImage
		if r.afterGate && r.mutation == "target_image" {
			imageID = "sha256:" + strings.Repeat("6", 64)
		}
		return imageID + "\n", nil
	default:
		return "", nil
	}
}

func TestRemoteHelperForcedCommandIsExactAndCheckedBeforeStdin(t *testing.T) {
	for _, original := range []string{"", "autostream-update-rpc-v1 ", "autostream-update-rpc-v2", "rpc"} {
		err := RunRemoteHelperRPC(context.Background(), filepath.Join(t.TempDir(), "missing.json"), original, panicRemoteReader{}, &bytes.Buffer{})
		if err == nil || !strings.Contains(err.Error(), "forced command") {
			t.Fatalf("original command %q result = %v", original, err)
		}
	}
	err := RunRemoteHelperRPC(context.Background(), filepath.Join(t.TempDir(), "missing.json"), RemoteFixedCommand, panicRemoteReader{}, &bytes.Buffer{})
	if err == nil || strings.Contains(err.Error(), "forced command") {
		t.Fatalf("exact forced command was not accepted through the command gate: %v", err)
	}
}

func TestRemoteTargetResolutionBindsHostTypeModeAndArchitecture(t *testing.T) {
	cfg := validHelperTestConfig(t)
	plan := validRemotePlan()
	if _, failure := resolveRemoteTarget(cfg, plan, "linux", cfg.Arch); failure != "" {
		t.Fatalf("valid target failure = %q", failure)
	}
	cases := []struct {
		name string
		edit func(*RemotePlan, *HelperConfig)
		os   string
		arch string
	}{
		{name: "host", edit: func(p *RemotePlan, _ *HelperConfig) { p.HostID = "edge-02" }, os: "linux", arch: cfg.Arch},
		{name: "type", edit: func(p *RemotePlan, _ *HelperConfig) { p.ServiceType = "observability" }, os: "linux", arch: cfg.Arch},
		{name: "mode", edit: func(p *RemotePlan, _ *HelperConfig) { p.DeploymentMode = ModeDocker }, os: "linux", arch: cfg.Arch},
		{name: "arch", edit: func(*RemotePlan, *HelperConfig) {}, os: "linux", arch: alternateArch(cfg.Arch)},
		{name: "os", edit: func(*RemotePlan, *HelperConfig) {}, os: "windows", arch: cfg.Arch},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			candidate, local := plan, cfg
			tc.edit(&candidate, &local)
			if _, failure := resolveRemoteTarget(local, candidate, tc.os, tc.arch); failure != "target_mismatch" {
				t.Fatalf("failure = %q", failure)
			}
		})
	}
}

func TestRemoteRequestsRejectChangedPrivilegedHelperPolicy(t *testing.T) {
	cfg := validHelperTestConfig(t)
	digest, err := cfg.SHA256()
	if err != nil {
		t.Fatal(err)
	}
	plan := validRemotePlan()
	plan.ConfigSHA256 = digest
	plan.PlanSHA256, err = plan.ComputePlanSHA256()
	if err != nil {
		t.Fatal(err)
	}
	changed := cfg
	changed.Targets = append([]Target(nil), cfg.Targets...)
	changedSystemd := *cfg.Targets[0].Systemd
	changedSystemd.Unit = "autostream-other-privileged.service"
	changed.Targets[0].Systemd = &changedSystemd
	changedDigest, err := changed.SHA256()
	if err != nil || changedDigest == digest {
		t.Fatalf("changed helper policy digest = %q, %v", changedDigest, err)
	}

	for _, operation := range []string{"stage", "apply", "reconcile"} {
		t.Run(operation, func(t *testing.T) {
			request := RemoteRPCRequest{Version: RemoteProtocolVersion, Operation: operation, Plan: &plan}
			if operation == "stage" {
				request.ReleaseToken = NewRemoteSecret("release-token")
			} else {
				request.MutationGrant = NewRemoteSecret("mutation-grant")
			}
			response := handleRemoteHelperRequest(context.Background(), changed, request, remoteHelperRuntime{})
			if response.Error == nil || response.Error.Code != "config_mismatch" {
				t.Fatalf("changed privileged policy response = %#v", response)
			}
			transient := runTransientRemoteHelperRequest(context.Background(), changed, filepath.Join(t.TempDir(), "helper.json"), request, remoteHelperRuntime{})
			if transient.Error == nil || transient.Error.Code != "config_mismatch" {
				t.Fatalf("changed privileged transient policy response = %#v", transient)
			}
		})
	}
}

func TestRemoteCurrentVersionMustBeKnownAndMatchExactly(t *testing.T) {
	plan := validRemotePlan()
	plan.CurrentVersion = ""
	if err := verifyRemotePlanCurrentVersion(validHelperTestConfig(t).Targets[0], plan); err == nil {
		t.Fatal("unknown current version was accepted")
	}
	if err := requirePlannedCurrentVersion("v1.2.3", "v1.2.3"); err != nil {
		t.Fatalf("exact current version was rejected: %v", err)
	}
	if err := requirePlannedCurrentVersion("v1.2.3", "v1.2.4"); err == nil {
		t.Fatal("stale non-empty current version was accepted")
	}
}

func TestRemoteSystemdStageAndApplyRejectStaleCurrentVersion(t *testing.T) {
	cfg := validHelperTestConfig(t)
	target := cfg.Targets[0]
	plan := validRemotePlan()
	stageRoot := filepath.Join(cfg.StateDir, "stages", remoteStableKey(plan.JobID, plan.PlanSHA256), "artifact")
	if err := os.MkdirAll(stageRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	stage := remoteStage{
		RootDir: stageRoot, ArtifactDigest: plan.ArtifactDigest, ExpectedVersion: plan.ExpectedVersion,
		NewRelease:      filepath.Join(target.Systemd.ReleaseRoot, "new"),
		PreviousRelease: filepath.Join(target.Systemd.ReleaseRoot, "old"), PreviousDigest: "sha256:" + strings.Repeat("b", 64), PreviousVersion: "v1.0.1",
	}
	if err := validateRemoteStage(cfg, target, plan, &stage); err == nil {
		t.Fatal("systemd stage from a different current version was accepted")
	}

	if runtime.GOOS == "windows" {
		return // The mutation assertion below requires a real current-link symlink.
	}
	actualRelease := stage.PreviousRelease
	if err := os.MkdirAll(actualRelease, 0o755); err != nil {
		t.Fatal(err)
	}
	actualDigest := strings.Repeat("b", 64)
	if err := os.WriteFile(filepath.Join(actualRelease, ".artifact-sha256"), []byte(actualDigest+"\n"), 0o444); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(actualRelease, ".version"), []byte(stage.PreviousVersion+"\n"), 0o444); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(actualRelease, target.Systemd.CurrentLink); err != nil {
		t.Fatal(err)
	}
	gateCalled := false
	applyPlan := plan.ApplyPlan()
	applyPlan.StageDir = stageRoot
	_, err := applyRemoteStagedSystemd(context.Background(), target, applyPlan, stage, &fakeRunner{}, func(context.Context) error {
		gateCalled = true
		return nil
	})
	if err == nil || gateCalled {
		t.Fatalf("stale systemd mutation result=%v gate_called=%v", err, gateCalled)
	}
}

func TestRemoteSystemdSmokeUsesArtifactRelativeBinaryUnderRootOnlyState(t *testing.T) {
	cfg := validHelperTestConfig(t)
	if err := ensureRemoteStateDirectories(cfg); err != nil {
		t.Fatal(err)
	}
	artifactRoot := filepath.Join(cfg.StateDir, "stages", "root-only-parent", "artifact")
	binary := filepath.Join(artifactRoot, "bin", "worker")
	if err := os.MkdirAll(filepath.Dir(binary), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(binary, []byte("verified-binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	digest, err := hashFile(binary)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(artifactRoot, "checksums.txt"), []byte(digest+"  bin/worker\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		if err := os.Chmod(cfg.StateDir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(filepath.Join(cfg.StateDir, "stages"), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	runner := &remoteSmokeCaptureRunner{}
	stage := remoteStage{}
	plan := validRemotePlan()
	if err := preflightRemoteSystemdStage(context.Background(), cfg.Targets[0], plan, artifactRoot, &stage, runner); err == nil {
		t.Fatal("unbootstrapped systemd target unexpectedly completed preflight")
	}
	if len(runner.calls) == 0 {
		t.Fatal("systemd artifact smoke command was not attempted")
	}
	call := runner.calls[0]
	gotBinary := call.args[len(call.args)-2]
	wantBinary := "." + string(filepath.Separator) + filepath.Join("bin", "worker")
	if call.dir != filepath.Clean(artifactRoot) || filepath.IsAbs(gotBinary) || gotBinary != wantBinary {
		t.Fatalf("smoke command escaped artifact cwd: dir=%q binary=%q want=%q", call.dir, gotBinary, wantBinary)
	}
	if runtime.GOOS != "windows" {
		for _, path := range []string{cfg.StateDir, filepath.Join(cfg.StateDir, "stages")} {
			info, err := os.Stat(path)
			if err != nil {
				t.Fatalf("stat root-only state path %s: %v", path, err)
			}
			if info.Mode().Perm() != 0o700 {
				t.Fatalf("smoke preflight weakened root-only state path %s: mode=%v", path, info.Mode().Perm())
			}
		}
	}
}

func TestRemoteSystemdApplyRechecksBaselineAndCheckpointAfterGrant(t *testing.T) {
	for _, tc := range []struct {
		name             string
		mutateCheckpoint bool
	}{
		{name: "running baseline changed"},
		{name: "checkpoint changed", mutateCheckpoint: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := validHelperTestConfig(t)
			target := cfg.Targets[0]
			if err := os.MkdirAll(target.Systemd.ReleaseRoot, 0o755); err != nil {
				t.Fatal(err)
			}
			stageRoot := filepath.Join(cfg.StateDir, "stages", "systemd-artifact")
			if err := os.MkdirAll(stageRoot, 0o700); err != nil {
				t.Fatal(err)
			}
			plan := validRemotePlan().ApplyPlan()
			plan.StageDir = stageRoot
			stage := remoteStage{
				RootDir: stageRoot, ArtifactDigest: plan.ArtifactDigest,
				NewRelease: filepath.Join(target.Systemd.ReleaseRoot, "new"), PreviousRelease: filepath.Join(target.Systemd.ReleaseRoot, "old"),
				PreviousDigest: "sha256:" + strings.Repeat("b", 64), PreviousVersion: plan.CurrentVersion,
			}
			verifications := 0
			verifier := func(context.Context, Target, remoteStage, CommandRunner) error {
				verifications++
				if verifications == 2 && !tc.mutateCheckpoint {
					return errors.New("manual deployment changed the running baseline")
				}
				return nil
			}
			gateCalled := false
			_, err := applyRemoteStagedSystemdWithVerifier(context.Background(), target, plan, stage, &fakeRunner{}, func(context.Context) error {
				gateCalled = true
				if tc.mutateCheckpoint {
					return saveCheckpoint(target, updateCheckpoint{JobID: plan.JobID, TargetID: target.TargetID, DeploymentMode: ModeSystemd, Phase: "prepared", TargetVersion: plan.TargetVersion})
				}
				return nil
			}, verifier)
			if err == nil || !gateCalled {
				t.Fatalf("post-grant systemd drift result=%v gate_called=%v", err, gateCalled)
			}
			if _, statErr := os.Lstat(stage.NewRelease); !os.IsNotExist(statErr) {
				t.Fatalf("release was installed after baseline drift: %v", statErr)
			}
		})
	}
}

func TestRemoteSystemdApplyRejectsStagedModeDriftAfterGrant(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix executable mode policy")
	}
	cfg := validHelperTestConfig(t)
	target := cfg.Targets[0]
	if err := os.MkdirAll(target.Systemd.ReleaseRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	stageRoot := filepath.Join(cfg.StateDir, "stages", "systemd-stage-mode-fence")
	binary := filepath.Join(stageRoot, "bin", "worker")
	if err := os.MkdirAll(filepath.Dir(binary), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(binary, []byte("new-release"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(binary, 0o755); err != nil {
		t.Fatal(err)
	}
	digest, err := hashFile(binary)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stageRoot, "checksums.txt"), []byte(digest+"  bin/worker\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	plan := validRemotePlan().ApplyPlan()
	plan.StageDir = stageRoot
	stage := remoteStage{
		RootDir: stageRoot, ArtifactDigest: plan.ArtifactDigest, ExpectedVersion: plan.ExpectedVersion,
		NewRelease: filepath.Join(target.Systemd.ReleaseRoot, "new"), PreviousRelease: filepath.Join(target.Systemd.ReleaseRoot, "old"),
		PreviousDigest: "sha256:" + strings.Repeat("b", 64), PreviousVersion: plan.CurrentVersion,
	}
	runner := &remoteSmokeCaptureRunner{}
	gateCalled := false
	_, err = applyRemoteStagedSystemdWithVerifier(context.Background(), target, plan, stage, runner, func(context.Context) error {
		gateCalled = true
		return os.Chmod(binary, 0o644)
	}, func(ctx context.Context, target Target, stage remoteStage, runner CommandRunner) error {
		return verifyRemoteSystemdStagedArtifact(ctx, target, stage, runner)
	})
	if err == nil || !gateCalled {
		t.Fatalf("staged mode drift result=%v gate_called=%v", err, gateCalled)
	}
	if _, statErr := os.Lstat(stage.NewRelease); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("release was installed after staged mode drift: %v", statErr)
	}
}

func TestRemoteSystemdApplyFinalBaselineFenceRunsAfterInstallBeforeStop(t *testing.T) {
	cfg := validHelperTestConfig(t)
	target := cfg.Targets[0]
	if err := os.MkdirAll(target.Systemd.ReleaseRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	stageRoot := filepath.Join(cfg.StateDir, "stages", "systemd-final-fence")
	binary := filepath.Join(stageRoot, "bin", "worker")
	if err := os.MkdirAll(filepath.Dir(binary), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(binary, []byte("new-release"), 0o755); err != nil {
		t.Fatal(err)
	}
	digest, err := hashFile(binary)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stageRoot, "checksums.txt"), []byte(digest+"  bin/worker\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	plan := validRemotePlan().ApplyPlan()
	plan.StageDir = stageRoot
	stage := remoteStage{
		RootDir: stageRoot, ArtifactDigest: plan.ArtifactDigest,
		NewRelease: filepath.Join(target.Systemd.ReleaseRoot, "new"), PreviousRelease: filepath.Join(target.Systemd.ReleaseRoot, "old"),
		PreviousDigest: "sha256:" + strings.Repeat("b", 64), PreviousVersion: plan.CurrentVersion,
	}
	verifications := 0
	runner := &fakeRunner{}
	_, err = applyRemoteStagedSystemdWithVerifier(context.Background(), target, plan, stage, runner, func(context.Context) error {
		return nil
	}, func(context.Context, Target, remoteStage, CommandRunner) error {
		verifications++
		if verifications == 3 {
			return errors.New("manual deployment during release copy")
		}
		return nil
	})
	if err == nil || verifications != 3 {
		t.Fatalf("final baseline fence result=%v verifications=%d", err, verifications)
	}
	if _, statErr := os.Lstat(checkpointPath(target)); !os.IsNotExist(statErr) {
		t.Fatalf("checkpoint was written before final baseline fence: %v", statErr)
	}
	for _, call := range runner.calls {
		if len(call.args) > 0 && call.args[0] == "stop" {
			t.Fatalf("service was stopped before final baseline fence: %+v", call)
		}
	}
}

func TestRemoteSystemdBaselineRejectsCorruptOrStoppedPreviousRelease(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("requires a real current-link symlink")
	}
	for _, corrupt := range []bool{true, false} {
		name := "stopped_or_wrong_binary"
		if corrupt {
			name = "corrupt_bytes"
		}
		t.Run(name, func(t *testing.T) {
			cfg := validHelperTestConfig(t)
			target := cfg.Targets[0]
			release := filepath.Join(target.Systemd.ReleaseRoot, "old")
			binary := filepath.Join(release, "bin", "worker")
			if err := os.MkdirAll(filepath.Dir(binary), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(binary, []byte("original"), 0o755); err != nil {
				t.Fatal(err)
			}
			digest, err := hashFile(binary)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(release, "checksums.txt"), []byte(digest+"  bin/worker\n"), 0o444); err != nil {
				t.Fatal(err)
			}
			artifactDigest := strings.Repeat("b", 64)
			if err := os.WriteFile(filepath.Join(release, ".artifact-sha256"), []byte(artifactDigest+"\n"), 0o444); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(release, ".version"), []byte("v1.0.0\n"), 0o444); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(release, target.Systemd.CurrentLink); err != nil {
				t.Fatal(err)
			}
			if corrupt {
				if err := os.WriteFile(binary, []byte("tampered"), 0o755); err != nil {
					t.Fatal(err)
				}
			}
			stage := remoteStage{PreviousRelease: release, PreviousDigest: "sha256:" + artifactDigest, PreviousVersion: "v1.0.0"}
			if err := verifyRemoteSystemdBaseline(context.Background(), target, stage, &fakeRunner{}); err == nil {
				t.Fatal("unsafe systemd rollback baseline was accepted")
			}
		})
	}
}

func TestRemoteDockerStageAndApplyRejectStaleCurrentVersion(t *testing.T) {
	cfg := bootstrapHelperTestConfig(t)
	target := cfg.Targets[0]
	versionPin := target.Docker.ImageVariable + "=v1.0.1@sha256:" + strings.Repeat("b", 64) + "\n"
	if err := os.MkdirAll(filepath.Dir(target.Docker.VersionEnvFile), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target.Docker.VersionEnvFile, []byte(versionPin), 0o600); err != nil {
		t.Fatal(err)
	}
	plan := validRemotePlan()
	plan.TargetID = target.TargetID
	plan.DeploymentMode = ModeDocker
	plan.CurrentVersion = "v1.0.0"
	plan.ArtifactDigest = strings.Repeat("c", 64)
	plan.ExpectedVersion = "v1.1.0"
	plan.ExpectedImageDigest = "sha256:" + strings.Repeat("d", 64)
	plan.ExpectedPlatformDigest = "sha256:" + strings.Repeat("e", 64)
	plan.PlanSHA256, _ = plan.ComputePlanSHA256()
	_, err := prepareRemoteStage(context.Background(), cfg, target, plan, NewRemoteSecret("release-token"), remoteHelperRuntime{})
	if err == nil || !strings.Contains(err.Error(), "current version") {
		t.Fatalf("stale Docker stage result = %v", err)
	}

	stageRoot := filepath.Join(cfg.StateDir, "stages", "docker")
	if err := os.MkdirAll(stageRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	gateCalled := false
	runner := &fakeRunner{}
	applyPlan := plan.ApplyPlan()
	applyPlan.StageDir = stageRoot
	_, err = applyDockerWithGate(context.Background(), target, applyPlan, runner, func(context.Context) error {
		gateCalled = true
		return nil
	}, true)
	if err == nil || !strings.Contains(err.Error(), "current version") || gateCalled || len(runner.calls) != 0 {
		t.Fatalf("stale Docker mutation result=%v gate_called=%v calls=%+v", err, gateCalled, runner.calls)
	}
}

func TestRemoteDockerApplyRejectsStageBaselineDriftBeforeGrant(t *testing.T) {
	target, plan, runner, staged := remoteDockerMutationFixture(t)
	runner.afterGate = true
	runner.mutation = "image"
	gateCalled := false
	_, err := applyDockerWithGateAndBaseline(context.Background(), target, plan, runner, func(context.Context) error {
		gateCalled = true
		return nil
	}, true, &staged, runner.targetImage)
	if err == nil || !strings.Contains(err.Error(), "changed after staging") || gateCalled {
		t.Fatalf("stage/apply baseline drift result=%v gate_called=%v", err, gateCalled)
	}
}

func TestRemoteDockerApplyRechecksCompleteBaselineAfterGrant(t *testing.T) {
	for _, mutation := range []string{"env", "container", "image", "repo"} {
		t.Run(mutation, func(t *testing.T) {
			target, plan, runner, staged := remoteDockerMutationFixture(t)
			gateCalled := false
			_, err := applyDockerWithGateAndBaseline(context.Background(), target, plan, runner, func(context.Context) error {
				gateCalled = true
				runner.afterGate = true
				runner.mutation = mutation
				if mutation == "env" {
					data, _, _, readErr := readVersionEnv(target.Docker.VersionEnvFile)
					if readErr != nil {
						return readErr
					}
					if writeErr := os.WriteFile(target.Docker.VersionEnvFile, append(data, []byte("UNRELATED_CHANGED=value\n")...), 0o600); writeErr != nil {
						return writeErr
					}
				}
				return nil
			}, true, &staged, runner.targetImage)
			if err == nil || !strings.Contains(err.Error(), "while consuming") || !gateCalled {
				t.Fatalf("post-grant %s drift result=%v gate_called=%v", mutation, err, gateCalled)
			}
			if _, statErr := os.Lstat(checkpointPath(target)); !os.IsNotExist(statErr) {
				t.Fatalf("checkpoint was written after %s drift: %v", mutation, statErr)
			}
			for _, call := range runner.calls {
				if strings.Contains(strings.Join(call.args, " "), " up -d ") {
					t.Fatalf("Docker service mutated after %s drift: %+v", mutation, call)
				}
			}
		})
	}
}

func TestRemoteDockerApplyRechecksStagedInputsAfterGrant(t *testing.T) {
	for _, mutation := range []string{"frozen_compose", "target_image", "target_repo"} {
		t.Run(mutation, func(t *testing.T) {
			target, plan, runner, staged := remoteDockerMutationFixture(t)
			gateCalled := false
			_, err := applyDockerWithGateAndBaseline(context.Background(), target, plan, runner, func(context.Context) error {
				gateCalled = true
				runner.afterGate = true
				runner.mutation = mutation
				if mutation == "frozen_compose" {
					return os.WriteFile(filepath.Join(plan.StageDir, "compose-frozen.json"), []byte(`{"services":{}}`), 0o600)
				}
				return nil
			}, true, &staged, runner.targetImage)
			if err == nil || !strings.Contains(err.Error(), "staged Docker inputs changed") || !gateCalled {
				t.Fatalf("post-grant %s drift result=%v gate_called=%v", mutation, err, gateCalled)
			}
			if _, statErr := os.Lstat(checkpointPath(target)); !errors.Is(statErr, os.ErrNotExist) {
				t.Fatalf("checkpoint was written after staged %s drift: %v", mutation, statErr)
			}
			for _, call := range runner.calls {
				if strings.Contains(strings.Join(call.args, " "), " up -d ") {
					t.Fatalf("Docker service mutated after staged %s drift: %+v", mutation, call)
				}
			}
		})
	}
}

func remoteDockerMutationFixture(t *testing.T) (Target, ApplyPlan, *remoteDockerBaselineRunner, dockerMutationBaseline) {
	t.Helper()
	oldSource, newSource := "v1.5.0", "v1.6.0"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if strings.HasSuffix(request.URL.Path, "/version") {
			_, _ = fmt.Fprintf(w, `{"version":%q}`, oldSource)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(server.Close)
	root := t.TempDir()
	stageDir := filepath.Join(root, "stage")
	if err := os.MkdirAll(stageDir, 0o700); err != nil {
		t.Fatal(err)
	}
	repo := "ghcr.io/kome-lab/autostream-docker/worker"
	targetPlatform := "sha256:" + strings.Repeat("e", 64)
	frozen := []byte(`{"services":{"worker":{"image":"` + repo + `@` + targetPlatform + `"}}}`)
	modelDigest, err := composeModelHash(frozen, "worker")
	if err != nil {
		t.Fatal(err)
	}
	versionEnv := filepath.Join(root, "worker.env")
	currentManifest := "sha256:" + strings.Repeat("b", 64)
	if err := os.WriteFile(versionEnv, []byte("AUTOSTREAM_DOCKER_VERSION=v1.0.0@"+currentManifest+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	target := Target{
		TargetID: "worker-01", HostID: "edge-01", ServiceType: "worker", DeploymentMode: ModeDocker,
		HealthURL: server.URL + "/health", VersionURL: server.URL + "/version",
		Docker: &DockerTarget{
			DockerPath: filepath.Join(root, "docker"), ComposeProject: "autostream", ProjectDir: root,
			ComposeFiles: []string{filepath.Join(root, "compose.yml")}, Service: "worker", ImageRepo: repo,
			ImageVariable: "AUTOSTREAM_DOCKER_VERSION", VersionEnvFile: versionEnv, CurrentVersion: "v1.0.0", ComposeConfigSHA256: modelDigest,
		},
	}
	if err := os.WriteFile(filepath.Join(stageDir, "compose-frozen.json"), frozen, 0o600); err != nil {
		t.Fatal(err)
	}
	runner := &remoteDockerBaselineRunner{
		containerID: "container-stable", imageID: "sha256:" + strings.Repeat("a", 64), repoDigest: "sha256:" + strings.Repeat("c", 64),
		targetImage: "sha256:" + strings.Repeat("d", 64), targetRepo: targetPlatform,
	}
	observation, err := observeDockerMutationBaseline(context.Background(), target, runner)
	if err != nil {
		t.Fatal(err)
	}
	runner.calls = nil
	plan := ApplyPlan{
		JobID: "job-01", TargetID: target.TargetID, ServiceType: target.ServiceType, DeploymentMode: ModeDocker,
		CurrentVersion: "v1.0.0", TargetVersion: "v2.0.0", StageDir: stageDir,
		ExpectedVersion: newSource, ExpectedImageDigest: "sha256:" + strings.Repeat("8", 64), ExpectedPlatformDigest: targetPlatform,
	}
	return target, plan, runner, observation.Baseline
}

func TestReconcileCheckpointPreviousVersionMustMatchImmutablePlan(t *testing.T) {
	plan := ApplyPlan{CurrentVersion: "v1.0.0"}
	systemdCheckpoint := updateCheckpoint{PreviousVersion: "v1.0.1"}
	if err := requirePlannedCurrentVersion(plan.CurrentVersion, systemdCheckpoint.PreviousVersion); err == nil {
		t.Fatal("systemd recovery accepted a checkpoint from a different current version")
	}
	dockerCheckpoint := updateCheckpoint{PreviousBundleVersion: "v1.0.1"}
	if err := requirePlannedCurrentVersion(plan.CurrentVersion, dockerCheckpoint.PreviousBundleVersion); err == nil {
		t.Fatal("Docker recovery accepted a checkpoint from a different current version")
	}
}

func TestRemoteLedgerAllowsOnlyReconcileAcrossLeaseGeneration(t *testing.T) {
	plan := validRemotePlan()
	plan.LeaseGeneration = 1
	plan.SessionID = "session-generation-0001"
	plan.PlanSHA256, _ = plan.ComputePlanSHA256()
	ledger := remoteMutationLedger{
		SchemaVersion: remoteLedgerSchemaVersion, JobID: plan.JobID, TargetID: plan.TargetID,
		PlanSHA256: plan.PlanSHA256, SessionID: plan.SessionID, LeaseGeneration: plan.LeaseGeneration,
		Intent: newRemoteMutationIntent(plan), Operation: "apply", State: remoteLedgerAmbiguous,
		Stage: &remoteStage{RootDir: filepath.Join(t.TempDir(), "stage"), ArtifactDigest: plan.ArtifactDigest},
	}
	recovery := plan
	recovery.LeaseGeneration = 2
	recovery.SessionID = "session-generation-0002"
	recovery.PlanSHA256, _ = recovery.ComputePlanSHA256()
	if failure := remoteLedgerRequestFailure(ledger, recovery, "reconcile"); failure != "" {
		t.Fatalf("fresh-generation reconcile rejected: %q", failure)
	}
	if failure := remoteLedgerRequestFailure(ledger, recovery, "apply"); failure != "plan_conflict" {
		t.Fatalf("fresh-generation apply failure = %q", failure)
	}
	stale := recovery
	stale.LeaseGeneration = 0
	if failure := remoteLedgerRequestFailure(ledger, stale, "reconcile"); failure != "plan_conflict" {
		t.Fatalf("stale reconcile failure = %q", failure)
	}
	mismatch := recovery
	mismatch.TargetVersion = "v1.2.0"
	mismatch.ExpectedVersion = mismatch.TargetVersion
	mismatch.PlanSHA256, _ = mismatch.ComputePlanSHA256()
	if failure := remoteLedgerRequestFailure(ledger, mismatch, "reconcile"); failure != "plan_conflict" {
		t.Fatalf("intent mismatch failure = %q", failure)
	}
}

func TestSystemdRecoveryWithoutCheckpointRequiresExactArtifactDigest(t *testing.T) {
	planned := strings.Repeat("a", 64)
	if !systemdRequestedReleaseDigestMatches(nil, "sha256:"+planned, planned) {
		t.Fatal("matching systemd recovery digest was rejected")
	}
	if systemdRequestedReleaseDigestMatches(nil, "sha256:"+strings.Repeat("b", 64), planned) {
		t.Fatal("same-version different-artifact recovery was accepted")
	}
	if !systemdRequestedReleaseDigestMatches(&updateCheckpoint{TargetDigest: "sha256:" + planned}, "sha256:"+strings.Repeat("b", 64), planned) {
		t.Fatal("checkpoint-specific digest validation was not delegated to the checkpoint branch")
	}
}

func TestRemoteTerminalResultAlwaysBindsRequestedArtifact(t *testing.T) {
	systemdPlan := validRemotePlan()
	bound, ok := bindRemoteApplyResult(systemdPlan, ApplyResult{Status: "rolled_back", RolledBack: true})
	if !ok || normalizeDigest(bound.ArtifactDigest) != normalizeDigest(systemdPlan.ResultArtifactDigest()) {
		t.Fatalf("systemd result=%#v ok=%v", bound, ok)
	}
	dockerPlan := systemdPlan
	dockerPlan.DeploymentMode = ModeDocker
	dockerPlan.ArtifactDigest = strings.Repeat("c", 64)
	dockerPlan.ExpectedVersion = "v1.1.0"
	dockerPlan.ExpectedImageDigest = "sha256:" + strings.Repeat("d", 64)
	dockerPlan.ExpectedPlatformDigest = "sha256:" + strings.Repeat("e", 64)
	dockerPlan.PlanSHA256, _ = dockerPlan.ComputePlanSHA256()
	bound, ok = bindRemoteApplyResult(dockerPlan, ApplyResult{Status: "rolled_back", RolledBack: true})
	if !ok || normalizeDigest(bound.ArtifactDigest) != normalizeDigest(dockerPlan.ResultArtifactDigest()) {
		t.Fatalf("Docker result=%#v ok=%v", bound, ok)
	}
	if _, ok := bindRemoteApplyResult(systemdPlan, ApplyResult{Status: "succeeded", ArtifactDigest: "sha256:" + strings.Repeat("f", 64)}); ok {
		t.Fatal("mismatched terminal artifact digest was accepted")
	}
}

func TestStagedOnlyRecoveryBecomesTerminalWithoutGrantOrApply(t *testing.T) {
	if RequireRemoteHelperRoot() != nil {
		t.Skip("root-owned ledger policy")
	}
	cfg := validHelperTestConfig(t)
	if err := ensureRemoteStateDirectories(cfg); err != nil {
		t.Fatal(err)
	}
	plan := validRemotePlan()
	plan.LeaseGeneration = 1
	plan.SessionID = "session-staged-only-01"
	plan.PlanSHA256, _ = plan.ComputePlanSHA256()
	stageRoot := filepath.Join(cfg.StateDir, "stages", remoteStableKey(plan.JobID, plan.PlanSHA256))
	if err := os.MkdirAll(stageRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	stage := remoteStage{
		RootDir: stageRoot, ArtifactDigest: plan.ArtifactDigest, ExpectedVersion: plan.ExpectedVersion,
		NewRelease:      filepath.Join(cfg.Targets[0].Systemd.ReleaseRoot, "new"),
		PreviousRelease: filepath.Join(cfg.Targets[0].Systemd.ReleaseRoot, "old"), PreviousDigest: "sha256:" + strings.Repeat("b", 64), PreviousVersion: plan.CurrentVersion,
	}
	ledger := remoteMutationLedger{
		SchemaVersion: remoteLedgerSchemaVersion, JobID: plan.JobID, TargetID: plan.TargetID,
		PlanSHA256: plan.PlanSHA256, SessionID: plan.SessionID, LeaseGeneration: 1,
		Intent: newRemoteMutationIntent(plan), Operation: "stage", State: remoteLedgerStaged, Stage: &stage,
	}
	if err := saveRemoteMutationLedger(cfg, ledger); err != nil {
		t.Fatal(err)
	}
	recovery := plan
	recovery.LeaseGeneration = 2
	recovery.SessionID = "session-staged-only-02"
	recovery.PlanSHA256, _ = recovery.ComputePlanSHA256()
	consumes := 0
	rt := remoteHelperRuntime{consumeGrant: func(context.Context, string, string, string, MutationGrantBinding, *http.Client) error {
		consumes++
		return nil
	}}
	response := remoteMutationRequest(context.Background(), cfg, cfg.Targets[0], recovery, "reconcile", NewRemoteSecret("grant-one"), &ledger, rt)
	if response.Result == nil || response.Result.Status != "rolled_back" || normalizeDigest(response.Result.ArtifactDigest) != normalizeDigest(recovery.ResultArtifactDigest()) || response.SessionID != recovery.SessionID || response.PlanSHA256 != recovery.PlanSHA256 || consumes != 0 {
		t.Fatalf("staged recovery response=%#v consumes=%d", response, consumes)
	}
	terminal, err := loadRemoteMutationLedger(cfg, plan.TargetID)
	if err != nil || terminal == nil || terminal.State != remoteLedgerTerminal || terminal.LeaseGeneration != 2 || terminal.PlanSHA256 != recovery.PlanSHA256 {
		t.Fatalf("terminal ledger=%#v err=%v", terminal, err)
	}
}

func TestStageReleaseTokenUsesFIFOAndNeverAppearsInStateFiles(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix FIFO")
	}
	if RequireRemoteHelperRoot() != nil {
		t.Skip("root-owned request policy")
	}
	cfg := validHelperTestConfig(t)
	if err := ensureRemoteStateDirectories(cfg); err != nil {
		t.Fatal(err)
	}
	plan := validRemotePlan()
	secret := "github-release-token-never-on-disk"
	request := RemoteRPCRequest{Version: RemoteProtocolVersion, Operation: "stage", Plan: &plan, ReleaseToken: NewRemoteSecret(secret)}
	path, err := createRemoteWorkerFIFO(cfg)
	if err != nil {
		t.Fatal(err)
	}
	read := make(chan RemoteRPCRequest, 1)
	errs := make(chan error, 1)
	go func() {
		decoded, err := readAndUnlinkRemoteWorkerRequest(cfg, path)
		if err != nil {
			errs <- err
			return
		}
		read <- decoded
	}()
	if err := writeRemoteWorkerFIFOWithTimeout(path, request, 5*time.Second); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-errs:
		t.Fatal(err)
	case decoded := <-read:
		if decoded.ReleaseToken.Reveal() != secret {
			t.Fatal("release token was not transferred through FIFO")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("FIFO reader did not finish")
	}
	assertStateTreeDoesNotContain(t, cfg.StateDir, secret)
}

func TestStageFIFONoReaderFailsWithinBound(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix FIFO")
	}
	if RequireRemoteHelperRoot() != nil {
		t.Skip("root-owned request policy")
	}
	cfg := validHelperTestConfig(t)
	if err := ensureRemoteStateDirectories(cfg); err != nil {
		t.Fatal(err)
	}
	path, err := createRemoteWorkerFIFO(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(path)
	plan := validRemotePlan()
	request := RemoteRPCRequest{Version: RemoteProtocolVersion, Operation: "stage", Plan: &plan, ReleaseToken: NewRemoteSecret("fifo-no-reader-secret")}
	started := time.Now()
	if err := writeRemoteWorkerFIFOWithTimeout(path, request, 150*time.Millisecond); err == nil {
		t.Fatal("FIFO without a worker reader unexpectedly succeeded")
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("FIFO no-reader path was not bounded: %v", elapsed)
	}
	assertStateTreeDoesNotContain(t, cfg.StateDir, "fifo-no-reader-secret")
}

func TestFreshReconcileConsumesGrantBeforeHostInspection(t *testing.T) {
	if RequireRemoteHelperRoot() != nil {
		t.Skip("root-owned ledger policy")
	}
	cfg := validHelperTestConfig(t)
	if err := ensureRemoteStateDirectories(cfg); err != nil {
		t.Fatal(err)
	}
	plan := validRemotePlan()
	plan.LeaseGeneration = 1
	plan.SessionID = "session-reconcile-old-01"
	plan.PlanSHA256, _ = plan.ComputePlanSHA256()
	stageRoot := filepath.Join(cfg.StateDir, "stages", remoteStableKey(plan.JobID, plan.PlanSHA256))
	if err := os.MkdirAll(stageRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	stage := &remoteStage{
		RootDir: stageRoot, ArtifactDigest: plan.ArtifactDigest, ExpectedVersion: plan.ExpectedVersion,
		NewRelease:      filepath.Join(cfg.Targets[0].Systemd.ReleaseRoot, "new"),
		PreviousRelease: filepath.Join(cfg.Targets[0].Systemd.ReleaseRoot, "old"), PreviousDigest: "sha256:" + strings.Repeat("b", 64), PreviousVersion: plan.CurrentVersion,
	}
	ledger := remoteMutationLedger{
		SchemaVersion: 1, JobID: plan.JobID, TargetID: plan.TargetID, PlanSHA256: plan.PlanSHA256,
		SessionID: plan.SessionID, LeaseGeneration: 1, Intent: newRemoteMutationIntent(plan),
		Operation: "apply", State: remoteLedgerAmbiguous, Stage: stage,
	}
	recovery := plan
	recovery.LeaseGeneration = 2
	recovery.SessionID = "session-reconcile-new-02"
	recovery.PlanSHA256, _ = recovery.ComputePlanSHA256()
	consumedAt := time.Time{}
	rt := remoteHelperRuntime{
		runner: OSCommandRunner{}, platformOS: "linux", platformArch: cfg.Arch,
		consumeGrant: func(context.Context, string, string, string, MutationGrantBinding, *http.Client) error {
			consumedAt = time.Now()
			return nil
		},
	}
	started := time.Now()
	response := remoteMutationRequest(context.Background(), cfg, cfg.Targets[0], recovery, "reconcile", NewRemoteSecret("grant-reconcile"), &ledger, rt)
	if consumedAt.IsZero() || consumedAt.Sub(started) > time.Second {
		t.Fatalf("reconcile grant was not consumed before inspection: %v", consumedAt.Sub(started))
	}
	if response.Error == nil || response.Error.Code != "reconcile_required" {
		t.Fatalf("expected missing host state to remain recoverable, response=%#v", response)
	}
	updated, err := loadRemoteMutationLedger(cfg, plan.TargetID)
	if err != nil || updated == nil || updated.LeaseGeneration != 2 || updated.PlanSHA256 != recovery.PlanSHA256 || updated.State != remoteLedgerAmbiguous {
		t.Fatalf("recovery ledger=%#v err=%v", updated, err)
	}
}

func TestMutationRequestFileIsUnlinkedBeforeUseAndSecretSafe(t *testing.T) {
	if RequireRemoteHelperRoot() != nil {
		t.Skip("root-owned request policy")
	}
	cfg := validHelperTestConfig(t)
	if err := ensureRemoteStateDirectories(cfg); err != nil {
		t.Fatal(err)
	}
	plan := validRemotePlan()
	secret := "mutation-grant-one-time-secret"
	request := RemoteRPCRequest{Version: RemoteProtocolVersion, Operation: "apply", Plan: &plan, MutationGrant: NewRemoteSecret(secret)}
	path, err := writeRemoteWorkerRequest(cfg, request)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := readAndUnlinkRemoteWorkerRequest(cfg, path)
	if err != nil || decoded.MutationGrant.Reveal() != secret {
		t.Fatalf("decoded request=%#v err=%v", decoded, err)
	}
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Fatalf("request still exists: %v", err)
	}
	assertStateTreeDoesNotContain(t, cfg.StateDir, secret)
}

func TestRemoteTerminalStageCleanupUsesFullHashAndPreservesCurrent(t *testing.T) {
	cfg := validHelperTestConfig(t)
	stages := filepath.Join(cfg.StateDir, "stages")
	oldRoot := filepath.Join(stages, remoteStableKey("old", "plan"))
	currentRoot := filepath.Join(stages, remoteStableKey("current", "plan"))
	if err := os.MkdirAll(filepath.Join(oldRoot, "artifact"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(currentRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	cleanupRemoteTerminalStage(cfg, &remoteStage{RootDir: filepath.Join(oldRoot, "artifact")}, currentRoot)
	if _, err := os.Stat(oldRoot); !os.IsNotExist(err) {
		t.Fatalf("old stage was not removed: %v", err)
	}
	if _, err := os.Stat(currentRoot); err != nil {
		t.Fatalf("current stage was removed: %v", err)
	}
	if got := filepath.Base(remoteLedgerPath(cfg, "worker-01")); !strings.Contains(got, remoteStableKey("worker-01")) {
		t.Fatalf("ledger path does not use full stable hash: %s", got)
	}
}

func TestRemoteStagePartialGCIsAgedSafeAndBounded(t *testing.T) {
	if RequireRemoteHelperRoot() != nil {
		t.Skip("root-owned stage GC policy")
	}
	cfg := validHelperTestConfig(t)
	if err := ensureRemoteStateDirectories(cfg); err != nil {
		t.Fatal(err)
	}
	stagesRoot := filepath.Join(cfg.StateDir, "stages")
	now := time.Now()
	old := now.Add(-remoteMutationWaitLimit - 6*time.Minute)
	for index := 0; index < 40; index++ {
		path := filepath.Join(stagesRoot, fmt.Sprintf(".partial-old-%02d", index))
		if err := os.Mkdir(path, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(path, old, old); err != nil {
			t.Fatal(err)
		}
	}
	young := filepath.Join(stagesRoot, ".partial-active")
	committed := filepath.Join(stagesRoot, remoteStableKey("job", "plan"))
	if err := os.Mkdir(young, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(committed, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(committed, old, old); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(cfg.StateDir, "outside-preserved")
	if err := os.Mkdir(outside, 0o700); err != nil {
		t.Fatal(err)
	}
	unsafeLink := filepath.Join(stagesRoot, ".partial-symlink")
	if runtime.GOOS != "windows" {
		if err := os.Symlink(outside, unsafeLink); err != nil {
			t.Fatal(err)
		}
	}
	if err := gcAgedRemoteStagePartials(cfg, now); err != nil {
		t.Fatal(err)
	}
	remainingOld := 0
	for index := 0; index < 40; index++ {
		if _, err := os.Lstat(filepath.Join(stagesRoot, fmt.Sprintf(".partial-old-%02d", index))); err == nil {
			remainingOld++
		} else if !os.IsNotExist(err) {
			t.Fatal(err)
		}
	}
	if remainingOld < 8 || remainingOld >= 40 {
		t.Fatalf("bounded GC left %d of 40 aged partials", remainingOld)
	}
	for _, path := range []string{young, committed, outside} {
		if _, err := os.Lstat(path); err != nil {
			t.Fatalf("GC removed protected path %s: %v", path, err)
		}
	}
	if runtime.GOOS != "windows" {
		if info, err := os.Lstat(unsafeLink); err != nil || info.Mode()&os.ModeSymlink == 0 {
			t.Fatalf("GC followed or removed unsafe symlink: %v", err)
		}
	}
}

func assertStateTreeDoesNotContain(t *testing.T, root, secret string) {
	t.Helper()
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if !entry.Type().IsRegular() {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if bytes.Contains(data, []byte(secret)) {
			return fmt.Errorf("secret found in %s", path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func alternateArch(arch string) string {
	if arch == "amd64" {
		return "arm64"
	}
	return "amd64"
}

func TestBootstrapHelperConfigAllowsZeroOnlyForSelectedDockerTarget(t *testing.T) {
	cfg := bootstrapHelperTestConfig(t)
	path := writeHelperTestConfig(t, cfg)
	loaded, err := LoadBootstrapRemoteHelperConfig(path, "worker-docker", false)
	if err != nil || loaded.Targets[0].Docker.ComposeConfigSHA256 != strings.Repeat("0", 64) {
		t.Fatalf("bootstrap load=%#v err=%v", loaded, err)
	}
	if _, err := LoadHelperConfig(path, false); err == nil || !strings.Contains(err.Error(), "sentinel") {
		t.Fatalf("runtime loader accepted bootstrap sentinel: %v", err)
	}
	if _, err := LoadBootstrapRemoteHelperConfig(path, "missing", false); err == nil {
		t.Fatal("bootstrap loader accepted an unconfigured target")
	}
}

func TestBootstrapTokenIsBoundedSinglePayloadAndRedacted(t *testing.T) {
	secret := "github-bootstrap-secret"
	token, err := ReadRemoteBootstrapToken(strings.NewReader(secret + "\r\n"))
	if err != nil || token.Reveal() != secret {
		t.Fatalf("token=%v err=%v", token, err)
	}
	formatted := fmt.Sprintf("%v %#v", token, token)
	if strings.Contains(formatted, secret) || !strings.Contains(formatted, "REDACTED") {
		t.Fatalf("token formatting leaked: %s", formatted)
	}
	if _, err := ReadRemoteBootstrapToken(strings.NewReader(secret + " extra\n")); err == nil || strings.Contains(err.Error(), secret) {
		t.Fatalf("whitespace token result=%v", err)
	}
	if _, err := ReadRemoteBootstrapToken(bytes.NewReader(bytes.Repeat([]byte("x"), remoteBootstrapTokenMaxBytes+1))); err == nil {
		t.Fatal("oversized token accepted")
	}
}

func bootstrapHelperTestConfig(t *testing.T) HelperConfig {
	t.Helper()
	root := t.TempDir()
	dockerPath := filepath.Join(root, "bin", "docker")
	projectDir := filepath.Join(root, "project")
	composeFile := filepath.Join(projectDir, "compose.yml")
	versionFile := filepath.Join(root, "config", "worker.env")
	return HelperConfig{
		SchemaVersion: 1, HostID: "edge-01", PanelURL: "https://panel.example.com", Arch: runtime.GOARCH,
		StateDir: filepath.Join(root, "state"),
		Targets: []Target{{
			TargetID: "worker-docker", HostID: "edge-01", ServiceType: "worker", DeploymentMode: ModeDocker,
			HealthURL: "http://127.0.0.1:8081/health", VersionURL: "http://127.0.0.1:8081/version",
			Docker: &DockerTarget{
				DockerPath: dockerPath, ComposeProject: "autostream", ProjectDir: projectDir,
				ComposeFiles: []string{composeFile}, Service: "worker",
				ImageRepo: "ghcr.io/kome-lab/autostream-docker/worker", ImageVariable: "AUTOSTREAM_DOCKER_VERSION",
				VersionEnvFile: versionFile, ComposeConfigSHA256: strings.Repeat("0", 64),
				CurrentVersion: "v1.0.0", Channel: "docker",
			},
		}},
	}
}
