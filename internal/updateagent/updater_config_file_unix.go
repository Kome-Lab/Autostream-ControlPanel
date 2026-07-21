//go:build linux

package updateagent

import (
	"errors"
	"fmt"
	"os"
	"os/user"
	"strconv"
	"syscall"
)

func updaterConfigInstallGID() (int, error) {
	if os.Geteuid() != 0 {
		return 0, errors.New("updater configure requires root")
	}
	group, err := user.LookupGroup(updaterConfigInstallGroup)
	if err != nil {
		return 0, fmt.Errorf("lookup %s group: %w", updaterConfigInstallGroup, err)
	}
	gid, err := strconv.Atoi(group.Gid)
	if err != nil || gid < 0 {
		return 0, fmt.Errorf("%s group has an invalid gid", updaterConfigInstallGroup)
	}
	return gid, nil
}

func updaterConfigHasInstallOwner(info os.FileInfo, gid int) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && stat.Uid == 0 && int(stat.Gid) == gid
}
