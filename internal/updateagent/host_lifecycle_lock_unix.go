//go:build !windows

package updateagent

import (
	"os"
	"path/filepath"
)

func acquireHostLifecycleLock() (func(), error) {
	directory := privilegedLockDir()
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return func() {}, err
	}
	if err := os.Chmod(directory, 0o700); err != nil {
		return func() {}, err
	}
	return lockFile(filepath.Join(directory, ".autostream-host-lifecycle.lock"))
}
