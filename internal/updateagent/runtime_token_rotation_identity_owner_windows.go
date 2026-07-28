//go:build windows

package updateagent

import "os"

func runtimeCredentialOwnedBy(os.FileInfo, uint32, uint32) bool {
	return false
}
