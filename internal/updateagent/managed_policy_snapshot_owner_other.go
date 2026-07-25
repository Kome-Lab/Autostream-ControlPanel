//go:build windows

package updateagent

import "os"

func managedSnapshotOwnedByCurrentUser(os.FileInfo) bool { return true }

func snapshotModeEnforced() bool { return false }
