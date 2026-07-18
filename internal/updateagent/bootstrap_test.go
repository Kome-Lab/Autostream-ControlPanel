package updateagent

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

type bootstrapRunner struct {
	imageID    string
	repoDigest string
	model      string
	pullErr    error
	calls      []commandCall
}

func (r *bootstrapRunner) Run(_ context.Context, dir string, env []string, name string, args ...string) (string, error) {
	r.calls = append(r.calls, commandCall{dir: dir, env: append([]string(nil), env...), name: name, args: append([]string(nil), args...)})
	joined := strings.Join(args, " ")
	switch {
	case len(args) > 0 && args[0] == "pull":
		if r.pullErr != nil {
			return "", r.pullErr
		}
		return "pulled\n", nil
	case len(args) > 0 && args[0] == "ps":
		return "bootstrap-container\n", nil
	case len(args) > 0 && args[0] == "inspect":
		return r.imageID + "\n", nil
	case len(args) > 1 && args[0] == "image" && args[1] == "inspect":
		if strings.Contains(joined, "{{.Id}}") {
			return r.imageID + "\n", nil
		}
		if strings.Contains(joined, "{{.Os}}/{{.Architecture}}") {
			return "linux/" + runtime.GOARCH + "\n", nil
		}
		return fmt.Sprintf(`["ghcr.io/kome-lab/autostream-docker/worker@%s"]`, r.repoDigest), nil
	case strings.Contains(joined, "config --format json --no-env-resolution"):
		return r.model, nil
	default:
		return "", fmt.Errorf("unexpected Docker command: %s", joined)
	}
}

func TestBootstrapDockerTargetFailsClosedWhenRootGHCRPullFails(t *testing.T) {
	trusted, err := resolveDockerReleaseForTest(t, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	envPath := filepath.Join(root, "worker.env")
	original := []byte("OTHER_SETTING=kept\n")
	if err := os.WriteFile(envPath, original, 0o640); err != nil {
		t.Fatal(err)
	}
	target := Target{TargetID: "worker-docker", ServiceType: "worker", DeploymentMode: ModeDocker, Docker: &DockerTarget{
		DockerPath: "/usr/bin/docker", ComposeProject: "autostream", ProjectDir: root, ComposeFiles: []string{filepath.Join(root, "compose.yml")}, Service: "worker", ImageRepo: "ghcr.io/kome-lab/autostream-docker/worker", ImageVariable: "AUTOSTREAM_DOCKER_VERSION", VersionEnvFile: envPath, CurrentVersion: "v1.2.3", ComposeConfigSHA256: strings.Repeat("0", 64),
	}}
	runner := &bootstrapRunner{imageID: "sha256:" + strings.Repeat("e", 64), repoDigest: trusted.PlatformDigest, pullErr: errors.New("unauthorized: authentication required")}
	_, err = bootstrapDockerTarget(context.Background(), target, runner,
		func(context.Context, Target) (ResolvedDockerRelease, error) { return trusted, nil },
		func(context.Context, Target) (string, error) { return trusted.SourceVersion, nil },
	)
	if err == nil || !strings.Contains(err.Error(), "authenticate and pull") {
		t.Fatalf("GHCR authorization failure was not explicit: %v", err)
	}
	got, readErr := os.ReadFile(envPath)
	if readErr != nil || string(got) != string(original) {
		t.Fatalf("failed registry probe altered version env: %q err=%v", got, readErr)
	}
	if len(runner.calls) != 1 || !strings.Contains(strings.Join(runner.calls[0].env, " "), "DOCKER_CONFIG=/root/.docker") {
		t.Fatalf("registry probe did not use fixed root credentials: %+v", runner.calls)
	}
}

func TestBootstrapDockerTargetSeedsOnlyVerifiedConfiguredBundle(t *testing.T) {
	trusted, err := resolveDockerReleaseForTest(t, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	envPath := filepath.Join(root, "worker.env")
	if err := os.WriteFile(envPath, []byte("OTHER_SETTING=kept\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	model := `{"services":{"worker":{"image":"ghcr.io/kome-lab/autostream-docker/worker@` + trusted.PlatformDigest + `"}}}`
	wantDigest, err := composeModelHash([]byte(model), "worker")
	if err != nil {
		t.Fatal(err)
	}
	target := Target{TargetID: "worker-docker", ServiceType: "worker", DeploymentMode: ModeDocker, Docker: &DockerTarget{
		DockerPath: "/usr/bin/docker", ComposeProject: "autostream", ProjectDir: root, ComposeFiles: []string{filepath.Join(root, "compose.yml")}, Service: "worker", ImageRepo: "ghcr.io/kome-lab/autostream-docker/worker", ImageVariable: "AUTOSTREAM_DOCKER_VERSION", VersionEnvFile: envPath, CurrentVersion: "v1.2.3", ComposeConfigSHA256: strings.Repeat("0", 64),
	}}
	// Existing tag-based deployments commonly retain the trusted multi-arch
	// index digest rather than the selected platform manifest in RepoDigests.
	runner := &bootstrapRunner{imageID: "sha256:" + strings.Repeat("e", 64), repoDigest: trusted.ManifestDigest, model: model}
	digest, err := bootstrapDockerTarget(context.Background(), target, runner,
		func(context.Context, Target) (ResolvedDockerRelease, error) { return trusted, nil },
		func(context.Context, Target) (string, error) { return trusted.SourceVersion, nil },
	)
	if err != nil {
		t.Fatal(err)
	}
	if digest != wantDigest {
		t.Fatalf("compose digest = %s, want %s", digest, wantDigest)
	}
	seeded, err := os.ReadFile(envPath)
	if err != nil {
		t.Fatal(err)
	}
	wantEnv := "OTHER_SETTING=kept\n\nAUTOSTREAM_DOCKER_VERSION=v1.2.3@" + trusted.ManifestDigest + "\n"
	if string(seeded) != wantEnv {
		t.Fatalf("seeded env = %q, want %q", seeded, wantEnv)
	}
	if len(runner.calls) != 7 { // pull, pulled ID, ps, container, RepoDigests, platform, compose config
		t.Fatalf("unexpected Docker command count: %+v", runner.calls)
	}
}

func TestPublicBootstrapAllowsOnlyTheExplicitComposeDigestSentinel(t *testing.T) {
	cfg := validTestConfig(t)
	root := filepath.Dir(cfg.StateDir)
	cfg.Targets[0] = Target{TargetID: "worker-docker", ServiceType: "worker", DeploymentMode: ModeDocker,
		HealthURL: "http://127.0.0.1:8081/health", VersionURL: "http://127.0.0.1:8081/version",
		Docker: &DockerTarget{DockerPath: filepath.Join(root, "docker"), ComposeProject: "autostream", ProjectDir: root, ComposeFiles: []string{filepath.Join(root, "compose.yml")}, Service: "worker", ImageRepo: "ghcr.io/kome-lab/autostream-docker/worker", ImageVariable: "AUTOSTREAM_DOCKER_VERSION", VersionEnvFile: filepath.Join(root, "worker.env"), CurrentVersion: "v1.2.3", ComposeConfigSHA256: strings.Repeat("0", 64)}}
	_, err := BootstrapDockerTarget(context.Background(), cfg, "missing-target", &bootstrapRunner{})
	if err == nil || !strings.Contains(err.Error(), "not a configured Docker target") {
		t.Fatalf("public bootstrap rejected its explicit sentinel before target selection: %v", err)
	}
}

func TestBootstrapDockerTargetRejectsUnverifiedRuntimeWithoutChangingEnv(t *testing.T) {
	trusted, err := resolveDockerReleaseForTest(t, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	envPath := filepath.Join(root, "worker.env")
	original := []byte("AUTOSTREAM_DOCKER_VERSION=v1.0.0@sha256:" + strings.Repeat("d", 64) + "\n")
	if err := os.WriteFile(envPath, original, 0o640); err != nil {
		t.Fatal(err)
	}
	target := Target{TargetID: "worker-docker", ServiceType: "worker", DeploymentMode: ModeDocker, Docker: &DockerTarget{
		DockerPath: "/usr/bin/docker", ComposeProject: "autostream", ProjectDir: root, ComposeFiles: []string{filepath.Join(root, "compose.yml")}, Service: "worker", ImageRepo: "ghcr.io/kome-lab/autostream-docker/worker", ImageVariable: "AUTOSTREAM_DOCKER_VERSION", VersionEnvFile: envPath, CurrentVersion: "v1.2.3", ComposeConfigSHA256: strings.Repeat("0", 64),
	}}
	runner := &bootstrapRunner{imageID: "sha256:" + strings.Repeat("e", 64), repoDigest: "sha256:" + strings.Repeat("f", 64), model: `{"services":{}}`}
	_, err = bootstrapDockerTarget(context.Background(), target, runner,
		func(context.Context, Target) (ResolvedDockerRelease, error) { return trusted, nil },
		func(context.Context, Target) (string, error) { return trusted.SourceVersion, nil },
	)
	if err == nil || !strings.Contains(err.Error(), "RepoDigest") {
		t.Fatalf("expected platform identity rejection, got %v", err)
	}
	got, readErr := os.ReadFile(envPath)
	if readErr != nil || string(got) != string(original) {
		t.Fatalf("bootstrap altered env on rejection: %q err=%v", got, readErr)
	}
}

func TestBootstrapDockerTargetRestoresEnvWhenComposeApprovalFails(t *testing.T) {
	trusted, err := resolveDockerReleaseForTest(t, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	envPath := filepath.Join(root, "worker.env")
	original := []byte("OTHER_SETTING=kept\n")
	if err := os.WriteFile(envPath, original, 0o640); err != nil {
		t.Fatal(err)
	}
	target := Target{TargetID: "worker-docker", ServiceType: "worker", DeploymentMode: ModeDocker, Docker: &DockerTarget{
		DockerPath: "/usr/bin/docker", ComposeProject: "autostream", ProjectDir: root, ComposeFiles: []string{filepath.Join(root, "compose.yml")}, Service: "worker", ImageRepo: "ghcr.io/kome-lab/autostream-docker/worker", ImageVariable: "AUTOSTREAM_DOCKER_VERSION", VersionEnvFile: envPath, CurrentVersion: "v1.2.3", ComposeConfigSHA256: strings.Repeat("0", 64),
	}}
	unsafe := `{"services":{"worker":{"image":"ghcr.io/kome-lab/autostream-docker/worker:v1.2.3","privileged":true}}}`
	runner := &bootstrapRunner{imageID: "sha256:" + strings.Repeat("e", 64), repoDigest: trusted.PlatformDigest, model: unsafe}
	_, err = bootstrapDockerTarget(context.Background(), target, runner,
		func(context.Context, Target) (ResolvedDockerRelease, error) { return trusted, nil },
		func(context.Context, Target) (string, error) { return trusted.SourceVersion, nil },
	)
	if err == nil {
		t.Fatal("expected unsafe compose rejection")
	}
	got, readErr := os.ReadFile(envPath)
	if readErr != nil || string(got) != string(original) {
		t.Fatalf("failed bootstrap did not restore env: %q err=%v", got, readErr)
	}
}
