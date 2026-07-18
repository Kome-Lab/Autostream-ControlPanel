package updateagent

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

const (
	defaultSSHDialTimeout      = 10 * time.Second
	defaultSSHOperationTimeout = 30 * time.Minute
	defaultSSHOutputLimit      = RemoteProtocolMaxFrameBytes
	sshStderrLimit             = 16 << 10

	SSHErrorTimeout                 = "ssh_timeout"
	SSHErrorConnectionRefused       = "ssh_connection_refused"
	SSHErrorAuthFailed              = "ssh_auth_failed"
	SSHErrorHostKeyMismatch         = "ssh_host_key_mismatch"
	SSHErrorRemoteHelperUnavailable = "remote_helper_unavailable"
	SSHErrorRemoteConfigInvalid     = "remote_config_invalid"
)

// SSHTransportError exposes only a stable allow-listed status code. The
// underlying network/SSH error remains available to errors.Is without ever
// being formatted into logs or API responses.
type SSHTransportError struct {
	Code  string
	cause error
}

func (e *SSHTransportError) Error() string {
	if e == nil || e.Code == "" {
		return "remote SSH operation failed"
	}
	return "remote SSH operation failed (" + e.Code + ")"
}

func (e *SSHTransportError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.cause
}

func newSSHTransportError(code string, cause error) error {
	return &SSHTransportError{Code: code, cause: cause}
}

func sshContextError(ctx context.Context) error {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return newSSHTransportError(SSHErrorTimeout, context.DeadlineExceeded)
	}
	return ctx.Err()
}

// effectiveSSHDeadline records whether the socket deadline came from the
// operation context. A socket timeout can win the race by a few microseconds
// before ctx.Done is observable, so callers need this provenance to preserve a
// stable context.DeadlineExceeded cause.
func effectiveSSHDeadline(ctx context.Context, fallback time.Time) (time.Time, bool) {
	if deadline, ok := ctx.Deadline(); ok && !deadline.After(fallback) {
		return deadline, true
	}
	return fallback, false
}

func sshNetTimeoutError(cause error, contextDeadline bool) error {
	if contextDeadline {
		cause = errors.Join(context.DeadlineExceeded, cause)
	}
	return newSSHTransportError(SSHErrorTimeout, cause)
}

var (
	sshUserPattern     = regexp.MustCompile(`^[a-z_][a-z0-9_-]{0,31}$`)
	sshHostnamePattern = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9.-]{0,251}[A-Za-z0-9])?$`)
	errSSHOutputLimit  = errors.New("SSH output limit exceeded")
)

type SSHHost struct {
	HostID         string `json:"host_id"`
	Name           string `json:"name"`
	Address        string `json:"address"`
	Port           int    `json:"port"`
	User           string `json:"user"`
	IdentityFile   string `json:"identity_file"`
	KnownHostsFile string `json:"known_hosts_file"`
	Arch           string `json:"arch"`
}

func (h SSHHost) Validate() error {
	if !identifierPattern.MatchString(strings.TrimSpace(h.HostID)) {
		return errors.New("host_id is invalid")
	}
	name := strings.TrimSpace(h.Name)
	if name == "" || len(name) > 128 || containsUnsafeText(name) {
		return errors.New("name is invalid")
	}
	if !validSSHAddress(h.Address) {
		return errors.New("address is invalid")
	}
	if h.Port < 1 || h.Port > 65535 {
		return errors.New("port is invalid")
	}
	if !sshUserPattern.MatchString(h.User) || h.User == "root" {
		return errors.New("user must be a fixed non-root account")
	}
	if h.Arch != "amd64" && h.Arch != "arm64" {
		return errors.New("arch must be amd64 or arm64")
	}
	if err := validateSSHFile(h.IdentityFile, true); err != nil {
		return fmt.Errorf("identity_file: %w", err)
	}
	if err := validateSSHFile(h.KnownHostsFile, false); err != nil {
		return fmt.Errorf("known_hosts_file: %w", err)
	}
	if filepath.Clean(h.IdentityFile) == filepath.Clean(h.KnownHostsFile) {
		return errors.New("identity_file and known_hosts_file must differ")
	}
	if _, err := h.identityFingerprint(); err != nil {
		return errors.New("identity_file is not a usable SSH private key")
	}
	if _, err := loadKnownHostsCallback(h.KnownHostsFile); err != nil {
		return errors.New("known_hosts_file is invalid")
	}
	return nil
}

func (h SSHHost) identityFingerprint() (string, error) {
	signer, err := loadSSHSigner(h.IdentityFile)
	if err != nil {
		return "", err
	}
	return ssh.FingerprintSHA256(signer.PublicKey()), nil
}

func (h SSHHost) DialAddress() string {
	return net.JoinHostPort(strings.TrimSpace(h.Address), strconv.Itoa(h.Port))
}

func validSSHAddress(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 253 || containsUnsafeText(value) || strings.ContainsAny(value, "/@?#[]") {
		return false
	}
	if ip := net.ParseIP(value); ip != nil {
		return true
	}
	return !strings.Contains(value, ":") && sshHostnamePattern.MatchString(value) && !strings.Contains(value, "..")
}

func containsUnsafeText(value string) bool {
	for _, r := range value {
		if r < 0x20 || r == 0x7f {
			return true
		}
	}
	return false
}

func validateSSHFile(path string, private bool) error {
	if !filepath.IsAbs(path) || filepath.Clean(path) == string(filepath.Separator) {
		return errors.New("path must be a non-root absolute path")
	}
	info, err := os.Lstat(path)
	if err != nil {
		return errors.New("file is unavailable")
	}
	return validateSSHFileSnapshot(path, info, private)
}

func validateSSHFileSnapshot(path string, info os.FileInfo, private bool) error {
	if !filepath.IsAbs(path) || filepath.Clean(path) == string(filepath.Separator) {
		return errors.New("path must be a non-root absolute path")
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > 1<<20 {
		return errors.New("must be a bounded regular non-symlink file")
	}
	if runtime.GOOS != "windows" {
		if private && info.Mode().Perm()&0o037 != 0 {
			return errors.New("private key may only be group-readable")
		}
		if !private && info.Mode().Perm()&0o022 != 0 {
			return errors.New("known_hosts must not be writable by group or other users")
		}
	}
	return nil
}

func (h SSHHost) validateRootOwnedFiles() error {
	for _, item := range []struct {
		path    string
		private bool
		label   string
	}{{h.IdentityFile, true, "identity_file"}, {h.KnownHostsFile, false, "known_hosts_file"}} {
		file, pathInfo, err := openSSHFile(item.path, item.private)
		if err != nil {
			return fmt.Errorf("%s: %w", item.label, err)
		}
		openedInfo, statErr := file.Stat()
		_ = file.Close()
		if statErr != nil || !os.SameFile(pathInfo, openedInfo) {
			return fmt.Errorf("%s changed during secure open", item.label)
		}
		if err := validateRootOwnedFileAndParents(item.path, openedInfo, item.label); err != nil {
			return err
		}
	}
	return nil
}

type SSHRemoteExecutor struct {
	DialTimeout      time.Duration
	OperationTimeout time.Duration
	OutputLimit      int
}

func (e SSHRemoteExecutor) Probe(ctx context.Context, host SSHHost) (RemoteProbeResult, error) {
	response, err := e.execute(ctx, host, RemoteRPCRequest{Version: RemoteProtocolVersion, Operation: "probe"})
	if err != nil {
		return RemoteProbeResult{}, err
	}
	if response.Probe == nil || response.Result != nil {
		return RemoteProbeResult{}, newSSHTransportError(SSHErrorRemoteConfigInvalid, nil)
	}
	if response.Probe.HostID != host.HostID || response.Probe.Arch != host.Arch {
		return RemoteProbeResult{}, newSSHTransportError(SSHErrorRemoteConfigInvalid, nil)
	}
	return *response.Probe, nil
}

func (e SSHRemoteExecutor) Stage(ctx context.Context, host SSHHost, plan RemotePlan, releaseToken RemoteSecret) (RemoteStageResult, error) {
	if plan.HostID != host.HostID {
		return RemoteStageResult{}, errors.New("remote plan host does not match SSH host")
	}
	response, err := e.execute(ctx, host, RemoteRPCRequest{Version: RemoteProtocolVersion, Operation: "stage", Plan: &plan, ReleaseToken: releaseToken})
	if err != nil {
		return RemoteStageResult{}, err
	}
	if response.Stage == nil || response.Probe != nil || response.Result != nil {
		return RemoteStageResult{}, newSSHTransportError(SSHErrorRemoteConfigInvalid, nil)
	}
	if response.Stage.SessionID != plan.SessionID || response.Stage.PlanSHA256 != plan.PlanSHA256 || response.Stage.ArtifactDigest != plan.ArtifactDigest {
		return RemoteStageResult{}, newSSHTransportError(SSHErrorRemoteConfigInvalid, nil)
	}
	return *response.Stage, nil
}

func (e SSHRemoteExecutor) Apply(ctx context.Context, host SSHHost, plan RemotePlan, grant RemoteSecret) (ApplyResult, error) {
	return e.executeMutation(ctx, host, "apply", plan, grant)
}

func (e SSHRemoteExecutor) Reconcile(ctx context.Context, host SSHHost, plan RemotePlan, grant RemoteSecret) (ApplyResult, error) {
	return e.executeMutation(ctx, host, "reconcile", plan, grant)
}

func (e SSHRemoteExecutor) executeMutation(ctx context.Context, host SSHHost, operation string, plan RemotePlan, grant RemoteSecret) (ApplyResult, error) {
	if plan.HostID != host.HostID {
		return ApplyResult{}, errors.New("remote plan host does not match SSH host")
	}
	response, err := e.execute(ctx, host, RemoteRPCRequest{Version: RemoteProtocolVersion, Operation: operation, Plan: &plan, MutationGrant: grant})
	if err != nil {
		return ApplyResult{}, err
	}
	if response.Result == nil || response.Probe != nil || response.Stage != nil {
		return ApplyResult{}, newSSHTransportError(SSHErrorRemoteConfigInvalid, nil)
	}
	if response.SessionID != plan.SessionID || response.PlanSHA256 != plan.PlanSHA256 {
		return ApplyResult{}, newSSHTransportError(SSHErrorRemoteConfigInvalid, nil)
	}
	result := *response.Result
	expectedDigest := plan.ResultArtifactDigest()
	if normalizeDigest(result.ArtifactDigest) != expectedDigest || (result.Status == "rolled_back") != result.RolledBack {
		return ApplyResult{}, newSSHTransportError(SSHErrorRemoteConfigInvalid, nil)
	}
	result.Message = redactRemoteCredentials(result.Message, RemoteRPCRequest{MutationGrant: grant})
	return result, nil
}

func (e SSHRemoteExecutor) execute(ctx context.Context, host SSHHost, request RemoteRPCRequest) (RemoteRPCResponse, error) {
	operationTimeout := e.OperationTimeout
	if operationTimeout <= 0 {
		operationTimeout = defaultSSHOperationTimeout
	}
	var cancel context.CancelFunc
	ctx, cancel = context.WithTimeout(ctx, operationTimeout)
	defer cancel()
	if err := host.Validate(); err != nil {
		return RemoteRPCResponse{}, newSSHTransportError(SSHErrorRemoteConfigInvalid, err)
	}
	if err := request.Validate(); err != nil {
		return RemoteRPCResponse{}, err
	}
	var requestPayload bytes.Buffer
	if err := EncodeRemoteRPCRequest(&requestPayload, request); err != nil {
		return RemoteRPCResponse{}, err
	}
	client, err := e.dial(ctx, host)
	if err != nil {
		return RemoteRPCResponse{}, err
	}
	defer client.Close()
	stopCancellation := context.AfterFunc(ctx, func() { _ = client.Close() })
	defer stopCancellation()
	session, err := client.NewSession()
	if err != nil {
		if ctx.Err() != nil {
			return RemoteRPCResponse{}, sshContextError(ctx)
		}
		return RemoteRPCResponse{}, newSSHTransportError(SSHErrorRemoteHelperUnavailable, err)
	}
	defer session.Close()
	overflow := make(chan struct{}, 1)
	stdout := &boundedSSHBuffer{limit: e.outputLimit(), overflowSignal: overflow}
	stderr := &boundedSSHBuffer{limit: sshStderrLimit, overflowSignal: overflow}
	session.Stdin = bytes.NewReader(requestPayload.Bytes())
	session.Stdout = stdout
	session.Stderr = stderr
	done := make(chan error, 1)
	go func() { done <- session.Run(RemoteFixedCommand) }()
	select {
	case <-ctx.Done():
		_ = session.Close()
		_ = client.Close()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
		}
		return RemoteRPCResponse{}, sshContextError(ctx)
	case <-overflow:
		_ = session.Close()
		_ = client.Close()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
		}
		if ctx.Err() != nil {
			return RemoteRPCResponse{}, sshContextError(ctx)
		}
		return RemoteRPCResponse{}, newSSHTransportError(SSHErrorRemoteConfigInvalid, errSSHOutputLimit)
	case runErr := <-done:
		if ctx.Err() != nil {
			return RemoteRPCResponse{}, sshContextError(ctx)
		}
		if stdout.overflow.Load() || stderr.overflow.Load() {
			return RemoteRPCResponse{}, newSSHTransportError(SSHErrorRemoteConfigInvalid, errSSHOutputLimit)
		}
		if runErr != nil {
			return RemoteRPCResponse{}, newSSHTransportError(SSHErrorRemoteHelperUnavailable, runErr)
		}
	}
	response, err := DecodeRemoteRPCResponse(bytes.NewReader(stdout.Bytes()))
	if err != nil {
		return RemoteRPCResponse{}, newSSHTransportError(SSHErrorRemoteConfigInvalid, err)
	}
	if response.Error != nil {
		message := redactRemoteCredentials(response.Error.Message, request)
		code := response.Error.Code
		for _, secret := range []RemoteSecret{request.MutationGrant, request.ReleaseToken} {
			if raw := secret.Reveal(); raw != "" && strings.Contains(code, raw) {
				code = "internal_error"
			}
		}
		return RemoteRPCResponse{}, &RemoteExecutionError{Code: code, Message: message}
	}
	return response, nil
}

func (e SSHRemoteExecutor) dial(ctx context.Context, host SSHHost) (*ssh.Client, error) {
	signer, err := loadSSHSigner(host.IdentityFile)
	if err != nil {
		return nil, newSSHTransportError(SSHErrorRemoteConfigInvalid, err)
	}
	hostKeyCallback, err := loadKnownHostsCallback(host.KnownHostsFile)
	if err != nil {
		return nil, newSSHTransportError(SSHErrorRemoteConfigInvalid, err)
	}
	config := &ssh.ClientConfig{
		User:            host.User,
		Auth:            []ssh.AuthMethod{ssh.PublicKeys(signer)},
		HostKeyCallback: hostKeyCallback,
	}
	timeout := e.DialTimeout
	if timeout <= 0 {
		timeout = defaultSSHDialTimeout
	}
	dialDeadline, dialDeadlineFromContext := effectiveSSHDeadline(ctx, time.Now().Add(timeout))
	dialer := net.Dialer{Deadline: dialDeadline}
	address := host.DialAddress()
	raw, err := dialer.DialContext(ctx, "tcp", address)
	if err != nil {
		if ctx.Err() != nil {
			return nil, sshContextError(ctx)
		}
		var netErr net.Error
		if errors.As(err, &netErr) && netErr.Timeout() {
			return nil, sshNetTimeoutError(err, dialDeadlineFromContext)
		}
		if errors.Is(err, syscall.ECONNREFUSED) {
			return nil, newSSHTransportError(SSHErrorConnectionRefused, err)
		}
		return nil, newSSHTransportError(SSHErrorRemoteHelperUnavailable, err)
	}
	stopCancellation := context.AfterFunc(ctx, func() { _ = raw.Close() })
	defer stopCancellation()
	deadline, handshakeDeadlineFromContext := effectiveSSHDeadline(ctx, time.Now().Add(timeout))
	_ = raw.SetDeadline(deadline)
	connection, channels, requests, err := ssh.NewClientConn(raw, address, config)
	if err != nil {
		_ = raw.Close()
		if ctx.Err() != nil {
			return nil, sshContextError(ctx)
		}
		var keyErr *knownhosts.KeyError
		if errors.As(err, &keyErr) {
			return nil, newSSHTransportError(SSHErrorHostKeyMismatch, err)
		}
		var netErr net.Error
		if errors.As(err, &netErr) && netErr.Timeout() {
			return nil, sshNetTimeoutError(err, handshakeDeadlineFromContext)
		}
		return nil, newSSHTransportError(SSHErrorAuthFailed, err)
	}
	_ = raw.SetDeadline(time.Time{})
	return ssh.NewClient(connection, channels, requests), nil
}

func loadSSHSigner(path string) (ssh.Signer, error) {
	file, _, err := openSSHFile(path, true)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	identity, err := io.ReadAll(io.LimitReader(file, 1<<20+1))
	if err != nil || len(identity) == 0 || len(identity) > 1<<20 {
		return nil, errors.New("read SSH identity")
	}
	return ssh.ParsePrivateKey(identity)
}

func openSSHFile(path string, private bool) (*os.File, os.FileInfo, error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) == string(filepath.Separator) {
		return nil, nil, errors.New("path must be a non-root absolute path")
	}
	pathInfo, err := os.Lstat(path)
	if err != nil {
		return nil, nil, errors.New("file is unavailable")
	}
	if err := validateSSHFileSnapshot(path, pathInfo, private); err != nil {
		return nil, nil, err
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, nil, errors.New("file is unavailable")
	}
	openedInfo, err := file.Stat()
	if err != nil || !openedInfo.Mode().IsRegular() || !os.SameFile(pathInfo, openedInfo) || pathInfo.Size() != openedInfo.Size() || pathInfo.Mode() != openedInfo.Mode() || !pathInfo.ModTime().Equal(openedInfo.ModTime()) || openedInfo.Size() <= 0 || openedInfo.Size() > 1<<20 {
		_ = file.Close()
		return nil, nil, errors.New("file changed during secure open")
	}
	return file, pathInfo, nil
}

// loadKnownHostsCallback snapshots the root-controlled file through the same
// verified descriptor used for metadata checks. knownhosts.New accepts only
// paths, so it parses a private temporary copy and never reopens the mutable
// configured pathname.
func loadKnownHostsCallback(path string) (ssh.HostKeyCallback, error) {
	file, _, err := openSSHFile(path, false)
	if err != nil {
		return nil, err
	}
	data, readErr := io.ReadAll(io.LimitReader(file, 1<<20+1))
	_ = file.Close()
	if readErr != nil || len(data) == 0 || len(data) > 1<<20 {
		return nil, errors.New("read SSH known_hosts")
	}
	directory, err := os.MkdirTemp("", "autostream-known-hosts-")
	if err != nil {
		return nil, errors.New("prepare SSH known_hosts parser")
	}
	defer os.RemoveAll(directory)
	if err := os.Chmod(directory, 0o700); err != nil {
		return nil, errors.New("secure SSH known_hosts parser")
	}
	snapshot := filepath.Join(directory, "known_hosts")
	if err := os.WriteFile(snapshot, data, 0o600); err != nil {
		return nil, errors.New("prepare SSH known_hosts parser")
	}
	callback, err := knownhosts.New(snapshot)
	if err != nil {
		return nil, err
	}
	return callback, nil
}

func (e SSHRemoteExecutor) outputLimit() int {
	if e.OutputLimit <= 0 || e.OutputLimit > RemoteProtocolMaxFrameBytes {
		return defaultSSHOutputLimit
	}
	return e.OutputLimit
}

type boundedSSHBuffer struct {
	buffer         bytes.Buffer
	limit          int
	overflow       atomic.Bool
	overflowSignal chan<- struct{}
}

func (b *boundedSSHBuffer) Write(value []byte) (int, error) {
	remaining := b.limit - b.buffer.Len()
	if remaining <= 0 {
		b.markOverflow()
		return 0, errSSHOutputLimit
	}
	if len(value) > remaining {
		b.markOverflow()
		written, _ := b.buffer.Write(value[:remaining])
		return written, errSSHOutputLimit
	}
	return b.buffer.Write(value)
}

func (b *boundedSSHBuffer) Bytes() []byte { return b.buffer.Bytes() }

func (b *boundedSSHBuffer) markOverflow() {
	b.overflow.Store(true)
	if b.overflowSignal != nil {
		select {
		case b.overflowSignal <- struct{}{}:
		default:
		}
	}
}

type RemoteExecutionError struct {
	Code    string
	Message string
}

func (e *RemoteExecutionError) Error() string {
	if e == nil {
		return "remote helper failed"
	}
	if strings.TrimSpace(e.Code) == "" {
		return "remote helper failed"
	}
	return "remote helper failed (" + e.Code + ")"
}

func redactRemoteCredentials(message string, request RemoteRPCRequest) string {
	for _, secret := range []RemoteSecret{request.MutationGrant, request.ReleaseToken} {
		if raw := secret.Reveal(); raw != "" {
			message = strings.ReplaceAll(message, raw, "[REDACTED]")
		}
	}
	if len(message) > 500 {
		message = message[:500]
	}
	return message
}
