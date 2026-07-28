//go:build windows

package updateagent

import "sync"

var windowsHostLifecycleLock sync.Mutex

func acquireHostLifecycleLock() (func(), error) {
	windowsHostLifecycleLock.Lock()
	return windowsHostLifecycleLock.Unlock, nil
}
