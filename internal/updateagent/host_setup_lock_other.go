//go:build !linux

package updateagent

import "errors"

func AcquireHostRuntimeSetupLock() (func(), error) {
	return func() {}, errors.New("Host runtime setup lock is supported only on Linux")
}

func AcquireHostRuntimeSetupAndLifecycleLocks() (func(), error) {
	return func() {}, errors.New("Host runtime setup and lifecycle locks are supported only on Linux")
}
