//go:build !windows

package updateagent

import (
	"errors"
	"os"
)

func RequireRemoteHelperRoot() error {
	if os.Geteuid() != 0 {
		return errors.New("remote helper command requires root")
	}
	return nil
}
