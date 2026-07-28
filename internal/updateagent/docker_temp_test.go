package updateagent

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDockerTempBaseUsesLocalExecutorStateOnlyForFixedProfile(t *testing.T) {
	fixedDocker := validLocalDockerTarget(t)
	fixed := Target{
		TargetID:       "worker-01",
		ServiceType:    "worker",
		DeploymentMode: ModeDocker,
		Docker:         &fixedDocker,
	}
	if got := dockerTempBase(fixed); got != localExecutorDockerWorkDir {
		t.Fatalf("fixed Local Executor Docker temp base = %q", got)
	}

	legacyDocker := fixedDocker
	legacyDocker.ProjectDir = filepath.Join(t.TempDir(), "project")
	legacyDocker.ComposeFiles = []string{filepath.Join(legacyDocker.ProjectDir, "compose.yml")}
	legacyDocker.BaseEnvFile = ""
	legacyDocker.VersionEnvFile = filepath.Join(legacyDocker.ProjectDir, "version.env")
	legacy := fixed
	legacy.Docker = &legacyDocker
	if got := dockerTempBase(legacy); got != legacyDocker.ProjectDir {
		t.Fatalf("legacy Docker temp base = %q, want project_dir", got)
	}

	nearMatchDocker := fixedDocker
	nearMatchDocker.VersionEnvFile = "/opt/autostream/local-executor/docker/not-worker.env"
	nearMatch := fixed
	nearMatch.Docker = &nearMatchDocker
	if got := dockerTempBase(nearMatch); got != nearMatchDocker.ProjectDir {
		t.Fatalf("near-match Docker profile gained Local Executor state authority: %q", got)
	}
}

func TestMakeDockerTempDirPreservesLegacyProjectLocalBehavior(t *testing.T) {
	projectDir := filepath.Join(t.TempDir(), "project")
	if err := os.Mkdir(projectDir, 0o700); err != nil {
		t.Fatal(err)
	}
	target := Target{
		TargetID:       "legacy-worker",
		ServiceType:    "worker",
		DeploymentMode: ModeDocker,
		Docker: &DockerTarget{
			ProjectDir: projectDir,
		},
	}
	dir, err := makeDockerTempDir(target, ".legacy-test-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	if filepath.Dir(dir) != projectDir {
		t.Fatalf("legacy Docker temp directory = %q, want child of %q", dir, projectDir)
	}
}
