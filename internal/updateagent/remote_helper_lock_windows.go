//go:build windows

package updateagent

import (
	"errors"
	"os"
	"sync"
	"time"
)

var remoteTransientWindowsLocks sync.Map

func acquireRemoteTransientFileLock(path string, timeout time.Duration) (func(), error) {
	if timeout <= 0 {
		return nil, errors.New("transient lock deadline")
	}
	semaphore := make(chan struct{}, 1)
	semaphore <- struct{}{}
	actual, _ := remoteTransientWindowsLocks.LoadOrStore(path, semaphore)
	lock := actual.(chan struct{})
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-lock:
		f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
		if err != nil {
			lock <- struct{}{}
			return nil, errors.New("open transient lock file")
		}
		return func() {
			_ = f.Close()
			lock <- struct{}{}
		}, nil
	case <-timer.C:
		return nil, errors.New("transient lock deadline")
	}
}
