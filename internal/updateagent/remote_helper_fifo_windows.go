//go:build windows

package updateagent

import (
	"errors"
	"os"
)

func makeRemoteWorkerFIFO(string, os.FileMode) error {
	return errors.New("FIFO unsupported")
}

func openRemoteWorkerFIFOWriter(string) (*os.File, bool, error) {
	return nil, false, errors.New("FIFO unsupported")
}

func openRemoteWorkerFIFOReader(string) (*os.File, error) {
	return nil, errors.New("FIFO unsupported")
}

func readRemoteWorkerFIFO(*os.File, []byte) (int, error) {
	return 0, errors.New("FIFO unsupported")
}

func writeRemoteWorkerFIFOChunk(*os.File, []byte) (int, error) {
	return 0, errors.New("FIFO unsupported")
}

func retryRemoteWorkerFIFORead(error) bool  { return false }
func retryRemoteWorkerFIFOWrite(error) bool { return false }
