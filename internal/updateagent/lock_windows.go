//go:build windows

package updateagent

import (
	"os"
	"path/filepath"
	"sync"
)

var windowsHelperLock sync.Mutex

func privilegedLockDir() string { return filepath.Join(os.TempDir(), "autostream-updater-locks") }

func lockFile(string) (func(), error) {
	windowsHelperLock.Lock()
	return windowsHelperLock.Unlock, nil
}
