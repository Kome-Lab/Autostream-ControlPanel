//go:build !linux

package updateagent

import (
	"context"
	"errors"
)

func (LocalExecutorClient) Probe(context.Context, string) (LocalExecutorProbe, error) {
	return LocalExecutorProbe{}, errors.New("local executor client requires Linux")
}

func (LocalExecutorClient) Stage(context.Context, RemotePlan, LocalExecutorMutationFence) (RemoteStageResult, error) {
	return RemoteStageResult{}, errors.New("local executor client requires Linux")
}

func (LocalExecutorClient) Apply(context.Context, RemotePlan, LocalExecutorMutationFence, RemoteSecret) (ApplyResult, error) {
	return ApplyResult{}, errors.New("local executor client requires Linux")
}

func (LocalExecutorClient) Reconcile(context.Context, RemotePlan, LocalExecutorMutationFence, RemoteSecret) (ApplyResult, error) {
	return ApplyResult{}, errors.New("local executor client requires Linux")
}

func (LocalExecutorClient) PortReconfigure(context.Context, SystemdPortReconfigurePlan, LocalExecutorMutationFence, RemoteSecret) (SystemdPortReconfigureResult, error) {
	return SystemdPortReconfigureResult{}, errors.New("local executor client requires Linux")
}

func (LocalExecutorClient) PortReconfigureReconcile(context.Context, SystemdPortReconfigurePlan, LocalExecutorMutationFence, RemoteSecret) (SystemdPortReconfigureResult, error) {
	return SystemdPortReconfigureResult{}, errors.New("local executor client requires Linux")
}
