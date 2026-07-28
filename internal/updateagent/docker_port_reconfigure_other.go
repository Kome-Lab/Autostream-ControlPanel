//go:build !linux

package updateagent

import "errors"

func newPlatformDockerPortExecution(
	LocalExecutorPolicy,
	LocalExecutorTarget,
	Target,
	remoteHelperRuntime,
) (dockerPortRuntime, dockerPortStateStore, error) {
	return nil, nil, errors.New("Docker port reconfiguration requires Linux")
}
