package updateagent

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
)

const localExecutorDockerWorkDir = "/var/lib/autostream-local-executor/docker-work"

// dockerTempBase keeps the legacy updater's project-local working directory,
// while the fixed Local Executor profile uses its private StateDirectory.
// ProtectSystem=strict can therefore keep /opt/autostream read-only.
func dockerTempBase(target Target) string {
	if target.DeploymentMode == ModeDocker &&
		matchesLocalExecutorDockerProfile(target.ServiceType, target.Docker) {
		return localExecutorDockerWorkDir
	}
	if target.Docker == nil {
		return ""
	}
	return target.Docker.ProjectDir
}

func makeDockerTempDir(target Target, pattern string) (string, error) {
	base := dockerTempBase(target)
	if base == "" {
		return "", errors.New("Docker working directory is unavailable")
	}
	if base == localExecutorDockerWorkDir {
		if err := ensureLocalExecutorDockerWorkDir(); err != nil {
			return "", err
		}
	}
	return os.MkdirTemp(base, pattern)
}

func ensureLocalExecutorDockerWorkDir() error {
	if runtime.GOOS != "windows" {
		if err := validateSecureRootPath(LocalExecutorMutationStateDir, true); err != nil {
			return errors.New("Local Executor state directory is not root-controlled")
		}
	}
	if err := os.Mkdir(localExecutorDockerWorkDir, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
		return errors.New("create Local Executor Docker working directory")
	}
	info, err := os.Lstat(localExecutorDockerWorkDir)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm() != 0o700 {
		return errors.New("Local Executor Docker working directory must be a private non-symlink directory")
	}
	if runtime.GOOS != "windows" {
		if err := validateSecureRootPath(localExecutorDockerWorkDir, true); err != nil {
			return errors.New("Local Executor Docker working directory is not root-controlled")
		}
	}
	if !pathWithin(LocalExecutorMutationStateDir, filepath.Clean(localExecutorDockerWorkDir)) {
		return errors.New("Local Executor Docker working directory escaped state directory")
	}
	return nil
}
