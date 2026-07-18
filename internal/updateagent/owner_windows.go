//go:build windows

package updateagent

import "os"

func requireRootOwner(os.FileInfo) error { return nil }

func isRootOwner(os.FileInfo) bool { return true }
