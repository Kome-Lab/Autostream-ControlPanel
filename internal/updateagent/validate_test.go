package updateagent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateDockerRuntimeSeparatesBundleAndSourceVersions(t *testing.T) {
	trusted, err := resolveDockerReleaseForTest(t, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	envPath := filepath.Join(root, "worker.env")
	if err := os.WriteFile(envPath, []byte("AUTOSTREAM_DOCKER_VERSION=v1.2.3@"+trusted.ManifestDigest+"\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	model := `{"services":{"worker":{"image":"ghcr.io/kome-lab/autostream-docker/worker@` + trusted.PlatformDigest + `"}}}`
	digest, err := composeModelHash([]byte(model), "worker")
	if err != nil {
		t.Fatal(err)
	}
	target := Target{TargetID: "worker-docker", ServiceType: "worker", DeploymentMode: ModeDocker, Docker: &DockerTarget{
		DockerPath: "/usr/bin/docker", ComposeProject: "autostream", ProjectDir: root, ComposeFiles: []string{filepath.Join(root, "compose.yml")}, Service: "worker", ImageRepo: "ghcr.io/kome-lab/autostream-docker/worker", ImageVariable: "AUTOSTREAM_DOCKER_VERSION", VersionEnvFile: envPath, CurrentVersion: "v1.2.3", ComposeConfigSHA256: digest,
	}}
	runner := &bootstrapRunner{imageID: "sha256:" + strings.Repeat("e", 64), repoDigest: trusted.PlatformDigest, model: model}
	bundle, err := validateDockerRuntimeTarget(context.Background(), target, runner,
		func(context.Context, Target, string) (ResolvedDockerRelease, error) { return trusted, nil },
		func(context.Context, Target) (string, error) { return trusted.SourceVersion, nil },
	)
	if err != nil {
		t.Fatal(err)
	}
	if bundle != "v1.2.3" || bundle == trusted.SourceVersion {
		t.Fatalf("bundle/source versions were conflated: bundle=%q source=%q", bundle, trusted.SourceVersion)
	}
}

func TestValidateDockerRuntimeRejectsComposeDriftAndWrongRepoDigest(t *testing.T) {
	trusted, err := resolveDockerReleaseForTest(t, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	envPath := filepath.Join(root, "worker.env")
	if err := os.WriteFile(envPath, []byte("AUTOSTREAM_DOCKER_VERSION=v1.2.3@"+trusted.ManifestDigest+"\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	model := `{"services":{"worker":{"image":"ghcr.io/kome-lab/autostream-docker/worker@` + trusted.PlatformDigest + `"}}}`
	target := Target{TargetID: "worker-docker", ServiceType: "worker", DeploymentMode: ModeDocker, Docker: &DockerTarget{
		DockerPath: "/usr/bin/docker", ComposeProject: "autostream", ProjectDir: root, ComposeFiles: []string{filepath.Join(root, "compose.yml")}, Service: "worker", ImageRepo: "ghcr.io/kome-lab/autostream-docker/worker", ImageVariable: "AUTOSTREAM_DOCKER_VERSION", VersionEnvFile: envPath, CurrentVersion: "v1.2.3", ComposeConfigSHA256: strings.Repeat("d", 64),
	}}
	resolve := func(context.Context, Target, string) (ResolvedDockerRelease, error) { return trusted, nil }
	readVersion := func(context.Context, Target) (string, error) { return trusted.SourceVersion, nil }

	t.Run("repository", func(t *testing.T) {
		runner := &bootstrapRunner{imageID: "sha256:" + strings.Repeat("e", 64), repoDigest: "sha256:" + strings.Repeat("f", 64), model: model}
		if _, err := validateDockerRuntimeTarget(context.Background(), target, runner, resolve, readVersion); err == nil || !strings.Contains(err.Error(), "RepoDigest") {
			t.Fatalf("wrong repository digest accepted: %v", err)
		}
	})
	t.Run("compose", func(t *testing.T) {
		runner := &bootstrapRunner{imageID: "sha256:" + strings.Repeat("e", 64), repoDigest: trusted.PlatformDigest, model: model}
		if _, err := validateDockerRuntimeTarget(context.Background(), target, runner, resolve, readVersion); err == nil || !strings.Contains(err.Error(), "compose_config_sha256") {
			t.Fatalf("compose drift accepted: %v", err)
		}
	})
}
