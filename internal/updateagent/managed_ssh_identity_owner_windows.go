//go:build windows

package updateagent

import "os"

func managedSSHIdentityCallerAllowed() bool {
	return true
}

func managedSSHOwnedByCurrentUser(os.FileInfo) bool {
	return true
}
