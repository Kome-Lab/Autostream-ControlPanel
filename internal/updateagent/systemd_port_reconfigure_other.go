//go:build !linux

package updateagent

import "errors"

func newPlatformSystemdPortExecution(
	LocalExecutorPolicy,
	LocalExecutorTarget,
	Target,
	remoteHelperRuntime,
) (systemdPortRuntime, systemdPortStateStore, error) {
	return nil, nil, errors.New("systemd port reconfiguration requires Linux")
}
