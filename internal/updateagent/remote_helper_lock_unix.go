//go:build !windows

package updateagent

import (
	"errors"
	"os"
	"syscall"
	"time"
)

func acquireRemoteTransientFileLock(path string, timeout time.Duration) (func(), error) {
	if timeout <= 0 {
		return nil, errors.New("transient lock deadline")
	}
	if info, err := os.Lstat(path); err == nil {
		if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || !isRootOwner(info) || !remotePrivateFileMode(info) {
			return nil, errors.New("transient lock file rejected")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, errors.New("inspect transient lock file")
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, errors.New("open transient lock file")
	}
	if err := f.Chmod(0o600); err != nil {
		_ = f.Close()
		return nil, errors.New("secure transient lock file")
	}
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || !isRootOwner(info) || !remotePrivateFileMode(info) {
		_ = f.Close()
		return nil, errors.New("transient lock file rejected")
	}
	deadline := time.Now().Add(timeout)
	for {
		err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			return func() {
				_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
				_ = f.Close()
			}, nil
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) && !errors.Is(err, syscall.EAGAIN) {
			_ = f.Close()
			return nil, errors.New("acquire transient lock")
		}
		if !time.Now().Before(deadline) {
			_ = f.Close()
			return nil, errors.New("transient lock deadline")
		}
		time.Sleep(25 * time.Millisecond)
	}
}
