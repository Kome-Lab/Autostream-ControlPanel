//go:build !linux

package updateagent

import "errors"

func InitializeUpdaterConfig(string, string) (bool, error) {
	return false, errors.New("updater configure is supported only on Linux and requires root")
}
