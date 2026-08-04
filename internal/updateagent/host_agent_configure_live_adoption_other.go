//go:build !linux

package updateagent

import (
	"context"
	"errors"
)

func verifyHostAgentLiveSystemdSidecar(
	context.Context,
	LocalExecutorPolicy,
	LocalExecutorPolicy,
	LocalExecutorTarget,
	LocalExecutorTarget,
) (hostAgentLiveSystemdSidecarProof, error) {
	return hostAgentLiveSystemdSidecarProof{}, errors.New("live systemd sidecar adoption is supported only on Linux")
}
