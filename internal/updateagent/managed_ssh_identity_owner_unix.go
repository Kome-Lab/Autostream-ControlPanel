//go:build !windows

package updateagent

import (
	"os"
	"syscall"
)

func managedSSHIdentityCallerAllowed() bool {
	return os.Geteuid() != 0
}

func managedSSHOwnedByCurrentUser(info os.FileInfo) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && stat.Uid == uint32(os.Geteuid())
}
