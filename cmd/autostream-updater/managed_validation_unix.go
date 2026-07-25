//go:build !windows

package main

import "os"

func managedValidationRunsAsRoot() bool {
	return os.Geteuid() == 0
}
