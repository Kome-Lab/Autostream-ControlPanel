package updateagent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"
)

const (
	remoteMutationWaitLimit       = 30 * time.Minute
	remoteWorkerFIFOReadLimit     = 15 * time.Second
	remoteTransientDirectoryLimit = 64
	remoteTransientGCScanLimit    = remoteTransientDirectoryLimit + 1
	remoteTransientGCDeleteLimit  = 16
)

var (
	remoteTransientRequestNamePattern = regexp.MustCompile(`^\.(?:rpc|stage-fifo)-([a-f0-9]{64})-`)
	remoteTransientResultNamePattern  = regexp.MustCompile(`^session-([a-f0-9]{64})\.json$`)
)

type remoteWorkerResult struct {
	SchemaVersion int               `json:"schema_version"`
	JobID         string            `json:"job_id"`
	PlanSHA256    string            `json:"plan_sha256"`
	SessionID     string            `json:"session_id"`
	Operation     string            `json:"operation"`
	Response      RemoteRPCResponse `json:"response"`
}

func dispatchRemoteHelperRequest(ctx context.Context, cfg HelperConfig, configPath string, request RemoteRPCRequest, rt remoteHelperRuntime) RemoteRPCResponse {
	if request.Operation == "probe" || runtime.GOOS != "linux" {
		return handleRemoteHelperRequest(ctx, cfg, request, rt)
	}
	return runTransientRemoteHelperRequest(ctx, cfg, configPath, request, rt)
}

func runTransientRemoteHelperRequest(ctx context.Context, cfg HelperConfig, configPath string, request RemoteRPCRequest, rt remoteHelperRuntime) RemoteRPCResponse {
	if err := request.Validate(); err != nil {
		return remoteFailure("invalid_request")
	}
	if failure := remoteHelperConfigBindingFailure(cfg, *request.Plan); failure != "" {
		return remoteFailure(failure)
	}
	if ensureRemoteStateDirectories(cfg) != nil {
		return remoteFailure("state_unavailable")
	}
	plan := *request.Plan
	resultPath := remoteWorkerResultPath(cfg, plan.SessionID)
	if result, ok := readRemoteWorkerResult(cfg, resultPath, request, true); ok {
		return result
	}
	executable, err := os.Executable()
	if err != nil {
		return remoteFailure("launcher_unavailable")
	}
	executable, err = filepath.EvalSymlinks(executable)
	if err != nil || validateSecureRootPath(executable, false) != nil {
		return remoteFailure("launcher_unavailable")
	}
	systemdRun, err := resolveSystemdRun()
	if err != nil {
		return remoteFailure("launcher_unavailable")
	}
	if cleanupAgedRemoteTransientFiles(cfg, time.Now(), map[string]struct{}{filepath.Clean(resultPath): {}}) != nil || remoteTransientDirectoriesAtCapacity(cfg) {
		return remoteFailure("state_unavailable")
	}
	return launchTransientRemoteHelperRequest(ctx, cfg, configPath, request, rt, executable, systemdRun, resultPath)
}

func launchTransientRemoteHelperRequest(ctx context.Context, cfg HelperConfig, configPath string, request RemoteRPCRequest, rt remoteHelperRuntime, executable, systemdRun, resultPath string) RemoteRPCResponse {
	plan := *request.Plan
	requestPath := ""
	var err error
	unitKey := remoteTransientUnitKey(plan.HostID, plan.SessionID)
	if request.Operation == "stage" {
		requestPath, err = createRemoteWorkerFIFO(cfg, unitKey)
	} else {
		requestPath, err = writeRemoteWorkerRequest(cfg, request)
	}
	if err != nil {
		return remoteFailure("state_unavailable")
	}
	// A mutation request contains a one-time grant. The worker normally unlinks
	// it before decoding, but every launcher/timeout return must also remove an
	// unaccepted request. os.Remove is deliberately idempotent with the worker.
	defer cleanupRemoteWorkerRequest(requestPath)
	unit := remoteTransientUnitName(unitKey)
	args := []string{
		"--quiet", "--collect", "--service-type=exec", "--unit=" + unit,
		"--property=UMask=0077", "--property=NoNewPrivileges=yes", "--property=PrivateTmp=yes",
		"--property=RuntimeMaxSec=30min", "--property=TimeoutStopSec=2min", "--property=KillMode=mixed",
		executable, "worker", "--config", configPath, "--request", requestPath, "--result", resultPath,
	}
	if _, err := rt.runner.Run(ctx, "", nil, systemdRun, args...); err != nil {
		if response, ok := remoteLedgerOutcome(cfg, request); ok {
			return response
		}
		if request.Operation == "stage" {
			if writeRemoteWorkerFIFOWithTimeout(requestPath, request, 10*time.Second) == nil {
				return remoteFailure("operation_continues")
			}
		} else if waitRemoteWorkerAccepted(cfg, requestPath, resultPath, request, 10*time.Second) {
			return remoteFailure("operation_continues")
		}
		return remoteFailure("launcher_unavailable")
	}
	if request.Operation == "stage" {
		if err := writeRemoteWorkerFIFOWithTimeout(requestPath, request, 10*time.Second); err != nil {
			return remoteFailure("launcher_unavailable")
		}
		request.ReleaseToken = ""
	}
	waitCtx, cancel := context.WithTimeout(ctx, remoteMutationWaitLimit)
	defer cancel()
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		if result, ok := readRemoteWorkerResult(cfg, resultPath, request, true); ok {
			return result
		}
		if response, ok := remoteLedgerOutcome(cfg, request); ok {
			if response.Result != nil || (response.Error != nil && response.Error.Code == "reconcile_required") {
				return response
			}
		}
		select {
		case <-waitCtx.Done():
			return remoteFailure("operation_continues")
		case <-ticker.C:
		}
	}
}

func resolveSystemdRun() (string, error) {
	resolved, err := resolveSecureExecutable("/usr/bin/systemd-run")
	if err != nil {
		return "", errors.New("systemd-run unavailable")
	}
	return resolved, nil
}

var systemdVersionPattern = regexp.MustCompile(`(?m)^systemd\s+([0-9]{2,4})(?:\s|$)`)

func validateSystemdRun(ctx context.Context, runner CommandRunner, path string) error {
	versionOutput, err := runner.Run(ctx, "", nil, path, "--version")
	match := systemdVersionPattern.FindStringSubmatch(versionOutput)
	if err != nil || len(match) != 2 {
		return errors.New("systemd-run version unavailable")
	}
	major, err := strconv.Atoi(match[1])
	if err != nil || major < 236 {
		return errors.New("systemd-run lacks transient collection support")
	}
	helpOutput, err := runner.Run(ctx, "", nil, path, "--help")
	if err != nil || !strings.Contains(helpOutput, "--collect") || !strings.Contains(helpOutput, "--service-type=") {
		return errors.New("systemd-run required options unavailable")
	}
	return nil
}

func writeRemoteWorkerRequest(cfg HelperConfig, request RemoteRPCRequest) (string, error) {
	var payload bytes.Buffer
	if err := EncodeRemoteRPCRequest(&payload, request); err != nil {
		return "", errors.New("encode worker request")
	}
	unitKey := remoteTransientUnitKey(request.Plan.HostID, request.Plan.SessionID)
	f, err := os.CreateTemp(filepath.Join(cfg.StateDir, "requests"), ".rpc-"+unitKey+"-")
	if err != nil {
		return "", errors.New("create worker request")
	}
	path := f.Name()
	remove := true
	defer func() {
		_ = f.Close()
		if remove {
			cleanupRemoteWorkerRequest(path)
		}
	}()
	if err := f.Chmod(0o600); err != nil {
		return "", errors.New("secure worker request")
	}
	if _, err := f.Write(payload.Bytes()); err != nil {
		return "", errors.New("write worker request")
	}
	if err := f.Sync(); err != nil {
		return "", errors.New("sync worker request")
	}
	if err := f.Close(); err != nil {
		return "", errors.New("close worker request")
	}
	remove = false
	return path, nil
}

func createRemoteWorkerFIFO(cfg HelperConfig, unitKeys ...string) (string, error) {
	pattern := ".stage-fifo-"
	if len(unitKeys) > 0 && remotePlanHashPattern.MatchString(unitKeys[0]) {
		pattern += unitKeys[0] + "-"
	}
	f, err := os.CreateTemp(filepath.Join(cfg.StateDir, "requests"), pattern)
	if err != nil {
		return "", errors.New("reserve stage FIFO")
	}
	path := f.Name()
	if err := f.Close(); err != nil {
		_ = os.Remove(path)
		return "", errors.New("close stage FIFO reservation")
	}
	if err := os.Remove(path); err != nil {
		return "", errors.New("prepare stage FIFO")
	}
	if err := makeRemoteWorkerFIFO(path, 0o600); err != nil {
		return "", errors.New("create stage FIFO")
	}
	return path, nil
}

func writeRemoteWorkerFIFOWithTimeout(path string, request RemoteRPCRequest, timeout time.Duration) error {
	var payload bytes.Buffer
	if err := EncodeRemoteRPCRequest(&payload, request); err != nil {
		return errors.New("encode stage FIFO request")
	}
	data := payload.Bytes()
	defer func() {
		for i := range data {
			data[i] = 0
		}
	}()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		f, retry, err := openRemoteWorkerFIFOWriter(path)
		if err == nil {
			offset := 0
			for offset < len(data) && time.Now().Before(deadline) {
				n, writeErr := writeRemoteWorkerFIFOChunk(f, data[offset:])
				if n > 0 {
					offset += n
				}
				if writeErr != nil && !retryRemoteWorkerFIFOWrite(writeErr) {
					_ = f.Close()
					return errors.New("write stage FIFO")
				}
				if offset < len(data) {
					time.Sleep(25 * time.Millisecond)
				}
			}
			closeErr := f.Close()
			if offset != len(data) || closeErr != nil {
				return errors.New("stage FIFO write deadline")
			}
			return nil
		}
		if !retry {
			return errors.New("open stage FIFO")
		}
		time.Sleep(50 * time.Millisecond)
	}
	return errors.New("stage FIFO reader unavailable")
}

func waitRemoteWorkerAccepted(cfg HelperConfig, requestPath, resultPath string, request RemoteRPCRequest, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if _, err := os.Lstat(requestPath); errors.Is(err, os.ErrNotExist) {
			return true
		}
		if _, ok := readRemoteWorkerResult(cfg, resultPath, request, false); ok {
			return true
		}
		if _, ok := remoteLedgerOutcome(cfg, request); ok {
			return true
		}
		time.Sleep(100 * time.Millisecond)
	}
	return false
}

// RunRemoteHelperWorker is an internal transient-unit entrypoint. The request
// file must be a root-owned one-time file under state_dir/requests. It is read
// into memory and unlinked before JSON decoding or any network/host action.
func RunRemoteHelperWorker(ctx context.Context, configPath, requestPath, resultPath string) error {
	cfg, err := LoadHelperConfig(configPath, true)
	if err != nil {
		return errors.New("worker configuration rejected")
	}
	if err := ensureRemoteStateDirectories(cfg); err != nil {
		return errors.New("worker state unavailable")
	}
	request, err := readAndUnlinkRemoteWorkerRequestContext(ctx, cfg, requestPath, remoteWorkerFIFOReadLimit)
	if err != nil {
		return err
	}
	if resultPath != remoteWorkerResultPath(cfg, request.Plan.SessionID) {
		return errors.New("worker result path rejected")
	}
	response := handleRemoteHelperRequest(ctx, cfg, request, defaultRemoteHelperRuntime())
	envelope := remoteWorkerResult{
		SchemaVersion: 1, JobID: request.Plan.JobID, PlanSHA256: request.Plan.PlanSHA256,
		SessionID: request.Plan.SessionID, Operation: request.Operation, Response: response,
	}
	if err := writeRemoteWorkerResult(cfg, resultPath, envelope); err != nil {
		return errors.New("worker result unavailable")
	}
	return nil
}

func readAndUnlinkRemoteWorkerRequest(cfg HelperConfig, path string) (RemoteRPCRequest, error) {
	return readAndUnlinkRemoteWorkerRequestContext(context.Background(), cfg, path, remoteWorkerFIFOReadLimit)
}

func readAndUnlinkRemoteWorkerRequestContext(ctx context.Context, cfg HelperConfig, path string, fifoTimeout time.Duration) (RemoteRPCRequest, error) {
	requestDir := filepath.Join(cfg.StateDir, "requests")
	if !filepath.IsAbs(path) || !pathWithin(requestDir, path) {
		return RemoteRPCRequest{}, errors.New("worker request path rejected")
	}
	info, err := os.Lstat(path)
	isRegular := info != nil && info.Mode().IsRegular()
	isFIFO := info != nil && info.Mode()&os.ModeNamedPipe != 0
	if err != nil || (!isRegular && !isFIFO) || info.Mode()&os.ModeSymlink != 0 || !remotePrivateFileMode(info) || (isRegular && (info.Size() <= 0 || info.Size() > RemoteProtocolMaxFrameBytes)) || !isRootOwner(info) {
		return RemoteRPCRequest{}, errors.New("worker request file rejected")
	}
	var data []byte
	var readErr, closeErr, removeErr error
	if isFIFO {
		data, readErr = readAndUnlinkRemoteWorkerFIFO(ctx, path, requestDir, fifoTimeout)
		removeErr = os.Remove(path)
		if errors.Is(removeErr, os.ErrNotExist) {
			removeErr = nil
		}
	} else {
		f, err := os.Open(path)
		if err != nil {
			return RemoteRPCRequest{}, errors.New("open worker request")
		}
		if runtime.GOOS == "windows" {
			// Windows cannot unlink an open file. The production helper is Linux-
			// only; this branch preserves equivalent testability without weakening
			// the Linux unlink-before-read boundary.
			data, readErr = io.ReadAll(io.LimitReader(f, RemoteProtocolMaxFrameBytes+1))
			closeErr = f.Close()
			removeErr = os.Remove(path)
		} else {
			removeErr = os.Remove(path)
			_ = syncDirectory(requestDir)
			data, readErr = io.ReadAll(io.LimitReader(f, RemoteProtocolMaxFrameBytes+1))
			closeErr = f.Close()
		}
	}
	if readErr != nil || closeErr != nil || removeErr != nil || len(data) == 0 || len(data) > RemoteProtocolMaxFrameBytes {
		return RemoteRPCRequest{}, errors.New("consume worker request")
	}
	request, err := DecodeRemoteRPCRequest(bytes.NewReader(data))
	for i := range data {
		data[i] = 0
	}
	if err != nil || request.Plan == nil || request.Operation == "probe" {
		return RemoteRPCRequest{}, errors.New("worker request rejected")
	}
	return request, nil
}

func readAndUnlinkRemoteWorkerFIFO(ctx context.Context, path, requestDir string, timeout time.Duration) ([]byte, error) {
	if timeout <= 0 {
		return nil, errors.New("consume stage FIFO deadline")
	}
	f, err := openRemoteWorkerFIFOReader(path)
	if err != nil {
		return nil, errors.New("open stage FIFO")
	}
	defer f.Close()
	defer os.Remove(path)

	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	data := make([]byte, 0, 4096)
	buffer := make([]byte, 32<<10)
	unlinked := false
	for {
		n, readErr := readRemoteWorkerFIFO(f, buffer)
		if n > 0 {
			if !unlinked {
				if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
					return nil, errors.New("unlink stage FIFO")
				}
				_ = syncDirectory(requestDir)
				unlinked = true
			}
			data = append(data, buffer[:n]...)
			if len(data) > RemoteProtocolMaxFrameBytes {
				return nil, errors.New("consume stage FIFO")
			}
		}
		if (errors.Is(readErr, io.EOF) || (n == 0 && readErr == nil)) && len(data) > 0 {
			return data, nil
		}
		if readErr != nil && !errors.Is(readErr, io.EOF) && !retryRemoteWorkerFIFORead(readErr) {
			return nil, errors.New("read stage FIFO")
		}

		select {
		case <-ctx.Done():
			return nil, errors.New("consume stage FIFO canceled")
		case <-deadline.C:
			return nil, errors.New("consume stage FIFO deadline")
		case <-ticker.C:
		}
	}
}

func remoteTransientUnitKey(hostID, sessionID string) string {
	return remoteStableKey(hostID, sessionID)
}

func remoteTransientUnitName(unitKey string) string {
	return "autostream-update-host-" + unitKey
}

// cleanupRemoteWorkerRequest removes only the root-owned request types that
// this helper creates. Regular mutation requests are overwritten before
// unlinking so an unconsumed one-time grant is not left in the state tree.
func cleanupRemoteWorkerRequest(path string) {
	info, err := os.Lstat(path)
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !isRootOwner(info) {
		return
	}
	if info.Mode().IsRegular() {
		wipeRemoteWorkerRequest(path, info)
	} else if info.Mode()&os.ModeNamedPipe == 0 {
		return
	}
	_ = os.Remove(path)
	_ = syncDirectory(filepath.Dir(path))
}

func wipeRemoteWorkerRequest(path string, expected os.FileInfo) {
	f, err := os.OpenFile(path, os.O_WRONLY, 0)
	if err != nil {
		return
	}
	info, statErr := f.Stat()
	if statErr != nil || !os.SameFile(expected, info) || !info.Mode().IsRegular() {
		_ = f.Close()
		return
	}
	remaining := info.Size()
	if remaining > RemoteProtocolMaxFrameBytes {
		remaining = RemoteProtocolMaxFrameBytes
	}
	zeros := make([]byte, 4096)
	for remaining > 0 {
		chunk := int64(len(zeros))
		if remaining < chunk {
			chunk = remaining
		}
		if _, err := f.Write(zeros[:chunk]); err != nil {
			break
		}
		remaining -= chunk
	}
	_ = f.Sync()
	_ = f.Close()
}

func cleanupAgedRemoteTransientFiles(cfg HelperConfig, now time.Time, excluded map[string]struct{}) error {
	return cleanupAgedRemoteTransientFilesWithUnitCheck(cfg, now, excluded, remoteTransientUnitLoaded)
}

func cleanupAgedRemoteTransientFilesWithUnitCheck(cfg HelperConfig, now time.Time, excluded map[string]struct{}, unitLoaded func(string) bool) error {
	cutoff := now.Add(-remoteMutationWaitLimit)
	deleted := 0
	for _, candidateDir := range []struct {
		path      string
		isRequest bool
	}{
		{path: filepath.Join(cfg.StateDir, "requests"), isRequest: true},
		{path: filepath.Join(cfg.StateDir, "results")},
	} {
		dir, err := os.Open(candidateDir.path)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return err
		}
		entries, readErr := dir.ReadDir(remoteTransientGCScanLimit)
		_ = dir.Close()
		if readErr != nil && !errors.Is(readErr, io.EOF) {
			return readErr
		}
		for _, entry := range entries {
			if deleted >= remoteTransientGCDeleteLimit {
				return nil
			}
			unitKey, ok := remoteTransientUnitKeyFromFile(entry.Name(), candidateDir.isRequest)
			if !ok {
				continue
			}
			path := filepath.Clean(filepath.Join(candidateDir.path, entry.Name()))
			if _, keep := excluded[path]; keep {
				continue
			}
			info, err := os.Lstat(path)
			if err != nil || info.Mode()&os.ModeSymlink != 0 || !isRootOwner(info) || !info.ModTime().Before(cutoff) {
				continue
			}
			if candidateDir.isRequest {
				if !info.Mode().IsRegular() && info.Mode()&os.ModeNamedPipe == 0 {
					continue
				}
			} else if !info.Mode().IsRegular() {
				continue
			}
			if unitLoaded(unitKey) {
				continue
			}
			if candidateDir.isRequest {
				cleanupRemoteWorkerRequest(path)
			} else {
				unlock, err := acquireRemoteTransientFileLock(remoteTransientResultLockPath(cfg), 250*time.Millisecond)
				if err != nil {
					continue
				}
				current, statErr := os.Lstat(path)
				if statErr == nil && current.Mode().IsRegular() && current.Mode()&os.ModeSymlink == 0 && isRootOwner(current) && current.ModTime().Before(cutoff) && !unitLoaded(unitKey) {
					_ = os.Remove(path)
					_ = syncDirectory(candidateDir.path)
				}
				unlock()
			}
			if _, err := os.Lstat(path); errors.Is(err, os.ErrNotExist) {
				deleted++
			}
		}
	}
	return nil
}

func remoteTransientDirectoriesAtCapacity(cfg HelperConfig) bool {
	for _, dirPath := range []string{filepath.Join(cfg.StateDir, "requests"), filepath.Join(cfg.StateDir, "results")} {
		dir, err := os.Open(dirPath)
		if err != nil {
			return true
		}
		entries, readErr := dir.ReadDir(remoteTransientDirectoryLimit)
		_ = dir.Close()
		if readErr != nil && !errors.Is(readErr, io.EOF) {
			return true
		}
		// Normal operation is not allowed to create the entry that would sit
		// beyond the bounded GC page. Unknown/protected entries therefore fail
		// closed instead of permanently starving an aged grant-bearing request.
		if len(entries) >= remoteTransientDirectoryLimit {
			return true
		}
	}
	return false
}

func remoteTransientResultLockPath(cfg HelperConfig) string {
	return filepath.Join(cfg.StateDir, "ledger", ".transient-results.lock")
}

func remoteTransientUnitKeyFromFile(name string, isRequest bool) (string, bool) {
	pattern := remoteTransientResultNamePattern
	if isRequest {
		pattern = remoteTransientRequestNamePattern
	}
	match := pattern.FindStringSubmatch(name)
	if len(match) != 2 {
		return "", false
	}
	return match[1], true
}

func remoteTransientUnitLoaded(unitKey string) bool {
	if runtime.GOOS != "linux" || !remotePlanHashPattern.MatchString(unitKey) {
		return false
	}
	unitPath := filepath.Join("/run/systemd/transient", remoteTransientUnitName(unitKey)+".service")
	_, err := os.Lstat(unitPath)
	if errors.Is(err, os.ErrNotExist) {
		return false
	}
	// Any loaded entry or unexpected lookup failure is preserved. systemd-run
	// uses --collect, so the file disappears after the bounded unit terminates.
	return true
}

func remoteWorkerResultPath(cfg HelperConfig, sessionID string) string {
	return filepath.Join(cfg.StateDir, "results", "session-"+remoteTransientUnitKey(cfg.HostID, sessionID)+".json")
}

func writeRemoteWorkerResult(cfg HelperConfig, path string, result remoteWorkerResult) error {
	if err := result.Response.Validate(); err != nil {
		return err
	}
	payload, err := json.Marshal(result)
	if err != nil {
		return err
	}
	unlock, err := acquireRemoteTransientFileLock(remoteTransientResultLockPath(cfg), 5*time.Second)
	if err != nil {
		return err
	}
	defer unlock()
	return writeAtomicFile(path, append(payload, '\n'), 0o600)
}

func readRemoteWorkerResult(cfg HelperConfig, path string, request RemoteRPCRequest, consume bool) (RemoteRPCResponse, bool) {
	unlock, err := acquireRemoteTransientFileLock(remoteTransientResultLockPath(cfg), time.Second)
	if err != nil {
		return RemoteRPCResponse{}, false
	}
	defer unlock()
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || !remotePrivateFileMode(info) || info.Size() <= 0 || info.Size() > RemoteProtocolMaxFrameBytes || !isRootOwner(info) {
		return RemoteRPCResponse{}, false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return RemoteRPCResponse{}, false
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var result remoteWorkerResult
	if decoder.Decode(&result) != nil || !errors.Is(decoder.Decode(&struct{}{}), io.EOF) || request.Plan == nil || result.SchemaVersion != 1 || result.JobID != request.Plan.JobID || result.PlanSHA256 != request.Plan.PlanSHA256 || result.SessionID != request.Plan.SessionID || result.Operation != request.Operation || result.Response.Validate() != nil {
		return RemoteRPCResponse{}, false
	}
	if consume {
		if err := os.Remove(path); err != nil {
			return RemoteRPCResponse{}, false
		}
		_ = syncDirectory(filepath.Dir(path))
	}
	return result.Response, true
}

func remoteLedgerOutcome(cfg HelperConfig, request RemoteRPCRequest) (RemoteRPCResponse, bool) {
	if request.Plan == nil {
		return RemoteRPCResponse{}, false
	}
	ledger, err := loadRemoteMutationLedger(cfg, request.Plan.TargetID)
	if err != nil || ledger == nil || ledger.JobID != request.Plan.JobID || !ledger.Intent.matches(*request.Plan) {
		return RemoteRPCResponse{}, false
	}
	if request.Operation != "reconcile" && ledger.PlanSHA256 != request.Plan.PlanSHA256 {
		return RemoteRPCResponse{}, false
	}
	if ledger.State == remoteLedgerTerminal && ledger.Result != nil {
		return remoteTerminalLedgerOutcome(*ledger, request)
	}
	if ledger.State == remoteLedgerAmbiguous && (request.Operation != "reconcile" || (ledger.PlanSHA256 == request.Plan.PlanSHA256 && ledger.SessionID == request.Plan.SessionID)) {
		return remoteFailure("reconcile_required"), true
	}
	if request.Operation == "apply" && ledger.State != remoteLedgerStaged {
		return remoteFailure("reconcile_required"), true
	}
	return RemoteRPCResponse{}, false
}

func remoteTerminalLedgerOutcome(ledger remoteMutationLedger, request RemoteRPCRequest) (RemoteRPCResponse, bool) {
	if request.Plan == nil || ledger.State != remoteLedgerTerminal || ledger.Result == nil {
		return RemoteRPCResponse{}, false
	}
	// Durable results are authorization-sensitive. Only an apply that is
	// byte-for-byte bound to the original lease, or a non-stale reconcile for
	// the same immutable intent, may replay the terminal result.
	if request.Operation != "apply" && request.Operation != "reconcile" {
		return RemoteRPCResponse{}, false
	}
	if remoteLedgerRequestFailure(ledger, *request.Plan, request.Operation) != "" {
		return RemoteRPCResponse{}, false
	}
	result, ok := bindRemoteApplyResult(*request.Plan, *ledger.Result)
	if !ok {
		return remoteFailure("state_invalid"), true
	}
	return RemoteRPCResponse{Version: RemoteProtocolVersion, Result: &result, SessionID: request.Plan.SessionID, PlanSHA256: request.Plan.PlanSHA256}, true
}

func secureWorkerArgument(value string) bool {
	return strings.TrimSpace(value) == value && value != "" && !strings.ContainsRune(value, '\x00')
}
