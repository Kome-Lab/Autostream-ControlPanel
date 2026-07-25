//go:build !windows

package updateagent

import (
	"os"
	"syscall"
)

func managedSnapshotOwnedByCurrentUser(info os.FileInfo) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && stat.Uid == uint32(os.Geteuid())
}

func snapshotModeEnforced() bool { return true }
