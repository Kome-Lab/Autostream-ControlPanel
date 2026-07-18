//go:build !windows

package updateagent

import (
	"errors"
	"os"
	"syscall"
)

func makeRemoteWorkerFIFO(path string, mode os.FileMode) error {
	return syscall.Mkfifo(path, uint32(mode.Perm()))
}

func openRemoteWorkerFIFOWriter(path string) (*os.File, bool, error) {
	fd, err := syscall.Open(path, syscall.O_WRONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, errors.Is(err, syscall.ENXIO), err
	}
	return os.NewFile(uintptr(fd), path), false, nil
}

func openRemoteWorkerFIFOReader(path string) (*os.File, error) {
	fd, err := syscall.Open(path, syscall.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(fd), path), nil
}

func readRemoteWorkerFIFO(f *os.File, buffer []byte) (int, error) {
	return syscall.Read(int(f.Fd()), buffer)
}

func writeRemoteWorkerFIFOChunk(f *os.File, buffer []byte) (int, error) {
	return syscall.Write(int(f.Fd()), buffer)
}

func retryRemoteWorkerFIFORead(err error) bool {
	return errors.Is(err, syscall.EAGAIN) || errors.Is(err, syscall.EWOULDBLOCK)
}

func retryRemoteWorkerFIFOWrite(err error) bool {
	return errors.Is(err, syscall.EAGAIN) || errors.Is(err, syscall.EWOULDBLOCK)
}
