package updateagent

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"

	"golang.org/x/crypto/ssh"
)

const (
	bootstrapSSHPrivateKeyMaxBytes = 1 << 20
	bootstrapSSHPassphraseMaxBytes = 16 << 10
	bootstrapHelperMaxBytes        = 64 << 20
	bootstrapSupportMaxBytes       = 1 << 20
	bootstrapConfigMaxBytes        = helperConfigMaxBytes
	bootstrapPublicKeyMaxBytes     = maxHostPublicKeyBytes
	bootstrapArchiveMaxBytes       = 72 << 20
	bootstrapOutputMaxBytes        = 64 << 10

	defaultBootstrapSSHOperationTimeout = 10 * time.Minute

	bootstrapInstallSourceCIDRPrefix = "AUTOSTREAM_BOOTSTRAP_SOURCE_CIDR="
)

// bootstrapInstallRemoteCommand is deliberately closed over every remote
// command and path. The only variable data arrives in the verified archive on
// stdin; the source CIDR is derived from sshd's numeric SSH_CONNECTION value.
const bootstrapInstallRemoteCommand = `/bin/bash -c 'set -euo pipefail
umask 077
export PATH=/usr/sbin:/usr/bin:/sbin:/bin
stage=$(mktemp -d /tmp/autostream-update-host-bootstrap.XXXXXXXX)
cleanup() {
  rm -rf -- "${stage}"
}
trap cleanup EXIT HUP INT TERM
connection=${SSH_CONNECTION:-}
read -r source_address source_port server_address server_port extra <<<"${connection}"
[[ -n ${source_address} && -n ${source_port} && -n ${server_address} && -n ${server_port} && -z ${extra:-} ]] || exit 64
if [[ ${source_address} == *:* ]]; then
  [[ ${source_address} =~ ^[0-9A-Fa-f:]+$ ]] || exit 64
  source_cidr="${source_address}/128"
else
  [[ ${source_address} =~ ^[0-9.]+$ ]] || exit 64
  IFS=. read -r -a source_octets <<<"${source_address}"
  [[ ${#source_octets[@]} -eq 4 ]] || exit 64
  for source_octet in "${source_octets[@]}"; do
    [[ ${source_octet} =~ ^[0-9]{1,3}$ ]] && ((10#${source_octet} <= 255)) || exit 64
  done
  source_cidr="${source_address}/32"
fi
getent ahosts "${source_address}" >/dev/null 2>&1 || exit 64
mkdir -p -- "${stage}/bin" "${stage}/sudoers" "${stage}/install" "${stage}/config" "${stage}/keys"
tar --extract --gzip --file=- --directory="${stage}" --no-same-owner --no-same-permissions -- \
  bin/autostream-update-host \
  sudoers/autostream-update-host \
  install/install-autostream-update-host \
  config/update-host.json \
  keys/autostream-updater.pub \
  checksums.sha256
for payload_file in \
  bin/autostream-update-host \
  sudoers/autostream-update-host \
  install/install-autostream-update-host \
  config/update-host.json \
  keys/autostream-updater.pub \
  checksums.sha256; do
  [[ -f ${stage}/${payload_file} && ! -L ${stage}/${payload_file} ]] || exit 65
done
(cd -- "${stage}" && sha256sum --check --strict checksums.sha256 >/dev/null)
chmod 0700 "${stage}/bin/autostream-update-host" "${stage}/install/install-autostream-update-host"
chmod 0400 "${stage}/sudoers/autostream-update-host" "${stage}/config/update-host.json" "${stage}/keys/autostream-updater.pub" "${stage}/checksums.sha256"
/usr/bin/sudo -n "${stage}/install/install-autostream-update-host" \
  --config "${stage}/config/update-host.json" \
  --authorized-key "${stage}/keys/autostream-updater.pub" \
  --source-cidr "${source_cidr}" \
  --install-sshd-policy >/dev/null
printf "AUTOSTREAM_BOOTSTRAP_SOURCE_CIDR=%s\n" "${source_cidr}"
'`

// The database inspection commands are a fixed allowlist selected from the
// configured target service types. No target value is interpolated into the
// remote shell command.
const bootstrapInspectDatabaseRemoteCommandPrefix = `/usr/bin/sudo -n /bin/bash -c 'set -euo pipefail
export PATH=/usr/sbin:/usr/bin:/sbin:/bin
inspect_database() {
  local service=$1
  local env_file=$2
  local line
  local database_url=
  local matches=0
  local database=
  if [[ ! -e ${env_file} ]]; then
    return 0
  fi
  [[ -f ${env_file} && ! -L ${env_file} ]] || exit 65
  while IFS= read -r line || [[ -n ${line} ]]; do
    if [[ ${line} == DATABASE_URL=* ]]; then
      database_url=${line#DATABASE_URL=}
      ((matches+=1))
    fi
  done <"${env_file}"
  [[ ${matches} -eq 1 && -n ${database_url} ]] || exit 65
  database=${database_url##*/}
  database=${database%%\?*}
  database=${database%%\#*}
  [[ ${database} =~ ^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$ ]] || exit 65
  printf "%s=%s\n" "${service}" "${database}"
}
`

const (
	bootstrapInspectControlPanelDatabaseRemoteCommand = bootstrapInspectDatabaseRemoteCommandPrefix + `inspect_database control_panel /etc/autostream/control-panel.env
'`
	bootstrapInspectObservabilityDatabaseRemoteCommand = bootstrapInspectDatabaseRemoteCommandPrefix + `inspect_database observability /etc/autostream/observability.env
'`
	bootstrapInspectBothDatabasesRemoteCommand = bootstrapInspectDatabaseRemoteCommandPrefix + `inspect_database control_panel /etc/autostream/control-panel.env
inspect_database observability /etc/autostream/observability.env
'`
)

var bootstrapDatabaseOutputPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$`)

type bootstrapDatabaseInspection uint8

const (
	bootstrapDatabaseInspectionNone          bootstrapDatabaseInspection = 0
	bootstrapDatabaseInspectionControlPanel  bootstrapDatabaseInspection = 1 << 0
	bootstrapDatabaseInspectionObservability bootstrapDatabaseInspection = 1 << 1
)

func bootstrapDatabaseInspectionForTargets(targets []Target) bootstrapDatabaseInspection {
	var inspection bootstrapDatabaseInspection
	for _, target := range targets {
		switch target.ServiceType {
		case "control_panel":
			inspection |= bootstrapDatabaseInspectionControlPanel
		case "observability":
			inspection |= bootstrapDatabaseInspectionObservability
		}
	}
	return inspection
}

func (i bootstrapDatabaseInspection) includes(serviceType string) bool {
	switch serviceType {
	case "control_panel":
		return i&bootstrapDatabaseInspectionControlPanel != 0
	case "observability":
		return i&bootstrapDatabaseInspectionObservability != 0
	default:
		return false
	}
}

// BootstrapSSHHost is the complete, fixed trust input for one first-time
// installation. Unlike SSHHost it intentionally has no mutable identity or
// known_hosts path: the admin identity is memory-only and the one canonical
// Ed25519 host key is pinned directly.
type BootstrapSSHHost struct {
	HostID        string `json:"host_id"`
	Address       string `json:"address"`
	Port          int    `json:"port"`
	AdminUser     string `json:"admin_user"`
	Arch          string `json:"arch"`
	HostPublicKey string `json:"host_public_key"`
}

func (h BootstrapSSHHost) validate() error {
	if !identifierPattern.MatchString(strings.TrimSpace(h.HostID)) {
		return errors.New("host_id is invalid")
	}
	if !validSSHAddress(h.Address) {
		return errors.New("address is invalid")
	}
	if h.Port < 1 || h.Port > 65535 {
		return errors.New("port is invalid")
	}
	if !sshUserPattern.MatchString(h.AdminUser) || h.AdminUser == "root" {
		return errors.New("admin_user must be a fixed non-root account")
	}
	if h.Arch != "amd64" && h.Arch != "arm64" {
		return errors.New("arch must be amd64 or arm64")
	}
	if _, err := parsePinnedHostPublicKey(h.HostPublicKey); err != nil {
		return errors.New("host_public_key must be one canonical ssh-ed25519 key")
	}
	return nil
}

func (h BootstrapSSHHost) dialAddress() string {
	return net.JoinHostPort(strings.TrimSpace(h.Address), strconv.Itoa(h.Port))
}

// BootstrapSSHCredential remains in memory only. Callers should discard their
// byte slices after Install or InspectStandardSystemdDatabases returns.
type BootstrapSSHCredential struct {
	PrivateKey []byte
	Passphrase []byte
}

// BootstrapPayload joins a verified, safely extracted release artifact with
// the host-specific configuration and managed updater public key. Only three
// fixed paths below ArtifactRootDir are ever opened or sent.
type BootstrapPayload struct {
	ArtifactRootDir  string
	ConfigJSON       []byte
	ManagedPublicKey []byte
}

type BootstrapSSHExecutor struct {
	DialTimeout      time.Duration
	OperationTimeout time.Duration
	OutputLimit      int
}

// Install transfers the fixed helper bootstrap payload and runs the fixed
// installer transaction. It does not accept a remote command, argv, or path.
func (e BootstrapSSHExecutor) Install(
	ctx context.Context,
	host BootstrapSSHHost,
	credential BootstrapSSHCredential,
	payload BootstrapPayload,
) (string, error) {
	if err := host.validate(); err != nil {
		return "", newSSHTransportError(SSHErrorRemoteConfigInvalid, nil)
	}
	signer, err := bootstrapSSHSigner(credential)
	if err != nil {
		return "", newSSHTransportError(SSHErrorRemoteConfigInvalid, nil)
	}
	archive, err := buildBootstrapArchive(payload)
	if err != nil {
		return "", err
	}
	defer clear(archive)
	output, err := e.execute(ctx, host, signer, bootstrapSSHInstall, archive)
	if err != nil {
		return "", err
	}
	return parseBootstrapSourceCIDR(output)
}

// InspectStandardSystemdDatabases extracts only database names required by the
// selected database-owning targets. Worker-only and other non-database hosts
// do not execute a remote inspection command.
func (e BootstrapSSHExecutor) InspectStandardSystemdDatabases(
	ctx context.Context,
	host BootstrapSSHHost,
	credential BootstrapSSHCredential,
	targets []Target,
) (map[string]string, error) {
	inspection := bootstrapDatabaseInspectionForTargets(targets)
	if inspection == bootstrapDatabaseInspectionNone {
		return map[string]string{}, nil
	}
	if err := host.validate(); err != nil {
		return nil, newSSHTransportError(SSHErrorRemoteConfigInvalid, nil)
	}
	signer, err := bootstrapSSHSigner(credential)
	if err != nil {
		return nil, newSSHTransportError(SSHErrorRemoteConfigInvalid, nil)
	}
	operation := bootstrapSSHInspectBothDatabases
	switch inspection {
	case bootstrapDatabaseInspectionControlPanel:
		operation = bootstrapSSHInspectControlPanelDatabase
	case bootstrapDatabaseInspectionObservability:
		operation = bootstrapSSHInspectObservabilityDatabase
	case bootstrapDatabaseInspectionControlPanel | bootstrapDatabaseInspectionObservability:
	default:
		return nil, newSSHTransportError(SSHErrorRemoteConfigInvalid, nil)
	}
	output, err := e.execute(ctx, host, signer, operation, nil)
	if err != nil {
		return nil, err
	}
	databases, err := parseBootstrapDatabaseOutput(output)
	if err != nil {
		return nil, err
	}
	for serviceType := range databases {
		if !inspection.includes(serviceType) {
			return nil, errors.New("bootstrap database inspection returned an unexpected service")
		}
	}
	return databases, nil
}

func bootstrapSSHSigner(credential BootstrapSSHCredential) (ssh.Signer, error) {
	if len(credential.PrivateKey) == 0 ||
		len(credential.PrivateKey) > bootstrapSSHPrivateKeyMaxBytes ||
		len(credential.Passphrase) > bootstrapSSHPassphraseMaxBytes {
		return nil, errors.New("bootstrap SSH credential is invalid")
	}
	privateKey := append([]byte(nil), credential.PrivateKey...)
	passphrase := append([]byte(nil), credential.Passphrase...)
	defer clear(privateKey)
	defer clear(passphrase)
	if len(passphrase) != 0 {
		return ssh.ParsePrivateKeyWithPassphrase(privateKey, passphrase)
	}
	return ssh.ParsePrivateKey(privateKey)
}

type bootstrapArchiveFile struct {
	name string
	mode int64
	data []byte
}

func buildBootstrapArchive(payload BootstrapPayload) ([]byte, error) {
	if len(payload.ConfigJSON) == 0 ||
		len(payload.ConfigJSON) > bootstrapConfigMaxBytes ||
		!json.Valid(payload.ConfigJSON) {
		return nil, errors.New("bootstrap payload config is invalid")
	}
	managedKey, err := canonicalBootstrapManagedPublicKey(payload.ManagedPublicKey)
	if err != nil {
		return nil, errors.New("bootstrap payload managed public key is invalid")
	}
	helper, err := readBootstrapArtifactFile(payload.ArtifactRootDir, "bin/autostream-update-host", bootstrapHelperMaxBytes)
	if err != nil {
		return nil, errors.New("bootstrap payload helper artifact is invalid")
	}
	sudoers, err := readBootstrapArtifactFile(payload.ArtifactRootDir, "sudoers/autostream-update-host", bootstrapSupportMaxBytes)
	if err != nil {
		return nil, errors.New("bootstrap payload sudoers artifact is invalid")
	}
	installer, err := readBootstrapArtifactFile(payload.ArtifactRootDir, "install/install-autostream-update-host", bootstrapSupportMaxBytes)
	if err != nil {
		return nil, errors.New("bootstrap payload installer artifact is invalid")
	}
	files := []bootstrapArchiveFile{
		{name: "bin/autostream-update-host", mode: 0o755, data: helper},
		{name: "sudoers/autostream-update-host", mode: 0o440, data: sudoers},
		{name: "install/install-autostream-update-host", mode: 0o755, data: installer},
		{name: "config/update-host.json", mode: 0o600, data: append([]byte(nil), payload.ConfigJSON...)},
		{name: "keys/autostream-updater.pub", mode: 0o600, data: managedKey},
	}
	defer func() {
		for i := range files {
			clear(files[i].data)
		}
	}()

	var checksums bytes.Buffer
	for _, file := range files {
		sum := sha256.Sum256(file.data)
		_, _ = fmt.Fprintf(&checksums, "%s  %s\n", hex.EncodeToString(sum[:]), file.name)
	}
	files = append(files, bootstrapArchiveFile{name: "checksums.sha256", mode: 0o600, data: checksums.Bytes()})
	return encodeBootstrapArchive(files)
}

func canonicalBootstrapManagedPublicKey(value []byte) ([]byte, error) {
	if len(value) == 0 || len(value) > bootstrapPublicKeyMaxBytes+1 {
		return nil, errors.New("managed public key is invalid")
	}
	text := string(value)
	line := strings.TrimSuffix(text, "\n")
	if text != line && text != line+"\n" {
		return nil, errors.New("managed public key is invalid")
	}
	key, err := parsePinnedHostPublicKey(line)
	if err != nil {
		return nil, errors.New("managed public key is invalid")
	}
	return ssh.MarshalAuthorizedKey(key), nil
}

func readBootstrapArtifactFile(root, relative string, limit int64) ([]byte, error) {
	if !filepath.IsAbs(root) || filepath.Clean(root) == string(filepath.Separator) {
		return nil, errors.New("artifact root is invalid")
	}
	rootInfo, err := os.Lstat(root)
	if err != nil || rootInfo.Mode()&os.ModeSymlink != 0 || !rootInfo.IsDir() {
		return nil, errors.New("artifact root is invalid")
	}
	path := filepath.Join(root, filepath.FromSlash(relative))
	info, err := os.Lstat(path)
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > limit {
		return nil, errors.New("artifact file is invalid")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, errors.New("artifact file is unavailable")
	}
	defer file.Close()
	openedInfo, err := file.Stat()
	if err != nil ||
		!openedInfo.Mode().IsRegular() ||
		!os.SameFile(info, openedInfo) ||
		info.Size() != openedInfo.Size() ||
		info.Mode() != openedInfo.Mode() ||
		!info.ModTime().Equal(openedInfo.ModTime()) ||
		openedInfo.Size() <= 0 ||
		openedInfo.Size() > limit {
		return nil, errors.New("artifact file changed during secure open")
	}
	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil || len(data) == 0 || int64(len(data)) > limit {
		return nil, errors.New("artifact file is invalid")
	}
	return data, nil
}

func encodeBootstrapArchive(files []bootstrapArchiveFile) ([]byte, error) {
	output := &bootstrapLimitedBuffer{limit: bootstrapArchiveMaxBytes}
	compressed := gzip.NewWriter(output)
	archive := tar.NewWriter(compressed)
	for _, file := range files {
		header := &tar.Header{
			Name:     file.name,
			Mode:     file.mode,
			Size:     int64(len(file.data)),
			Typeflag: tar.TypeReg,
			Format:   tar.FormatUSTAR,
		}
		if err := archive.WriteHeader(header); err != nil {
			_ = archive.Close()
			_ = compressed.Close()
			return nil, errors.New("bootstrap payload archive is too large")
		}
		if _, err := archive.Write(file.data); err != nil {
			_ = archive.Close()
			_ = compressed.Close()
			return nil, errors.New("bootstrap payload archive is too large")
		}
	}
	if err := archive.Close(); err != nil {
		_ = compressed.Close()
		return nil, errors.New("bootstrap payload archive is too large")
	}
	if err := compressed.Close(); err != nil {
		return nil, errors.New("bootstrap payload archive is too large")
	}
	return append([]byte(nil), output.Bytes()...), nil
}

type bootstrapLimitedBuffer struct {
	bytes.Buffer
	limit int
}

func (b *bootstrapLimitedBuffer) Write(value []byte) (int, error) {
	if len(value) > b.limit-b.Len() {
		return 0, errors.New("bootstrap archive size limit exceeded")
	}
	return b.Buffer.Write(value)
}

type bootstrapSSHOperation uint8

const (
	bootstrapSSHInstall bootstrapSSHOperation = iota + 1
	bootstrapSSHInspectControlPanelDatabase
	bootstrapSSHInspectObservabilityDatabase
	bootstrapSSHInspectBothDatabases
)

func (e BootstrapSSHExecutor) execute(
	ctx context.Context,
	host BootstrapSSHHost,
	signer ssh.Signer,
	operation bootstrapSSHOperation,
	stdin []byte,
) ([]byte, error) {
	command := ""
	switch operation {
	case bootstrapSSHInstall:
		command = bootstrapInstallRemoteCommand
	case bootstrapSSHInspectControlPanelDatabase:
		command = bootstrapInspectControlPanelDatabaseRemoteCommand
		if len(stdin) != 0 {
			return nil, newSSHTransportError(SSHErrorRemoteConfigInvalid, nil)
		}
	case bootstrapSSHInspectObservabilityDatabase:
		command = bootstrapInspectObservabilityDatabaseRemoteCommand
		if len(stdin) != 0 {
			return nil, newSSHTransportError(SSHErrorRemoteConfigInvalid, nil)
		}
	case bootstrapSSHInspectBothDatabases:
		command = bootstrapInspectBothDatabasesRemoteCommand
		if len(stdin) != 0 {
			return nil, newSSHTransportError(SSHErrorRemoteConfigInvalid, nil)
		}
	default:
		return nil, newSSHTransportError(SSHErrorRemoteConfigInvalid, nil)
	}
	operationTimeout := e.OperationTimeout
	if operationTimeout <= 0 {
		operationTimeout = defaultBootstrapSSHOperationTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, operationTimeout)
	defer cancel()

	client, err := e.dial(ctx, host, signer)
	if err != nil {
		return nil, err
	}
	defer client.Close()
	stopCancellation := context.AfterFunc(ctx, func() { _ = client.Close() })
	defer stopCancellation()
	session, err := client.NewSession()
	if err != nil {
		if ctx.Err() != nil {
			return nil, sshContextError(ctx)
		}
		return nil, newSSHTransportError(SSHErrorRemoteHelperUnavailable, nil)
	}
	defer session.Close()
	overflow := make(chan struct{}, 1)
	stdout := &boundedSSHBuffer{limit: e.outputLimit(), overflowSignal: overflow}
	stderr := &boundedSSHBuffer{limit: e.outputLimit(), overflowSignal: overflow}
	session.Stdin = bytes.NewReader(stdin)
	session.Stdout = stdout
	session.Stderr = stderr
	done := make(chan error, 1)
	go func() { done <- session.Run(command) }()
	select {
	case <-ctx.Done():
		_ = session.Close()
		_ = client.Close()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
		}
		return nil, sshContextError(ctx)
	case <-overflow:
		_ = session.Close()
		_ = client.Close()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
		}
		if ctx.Err() != nil {
			return nil, sshContextError(ctx)
		}
		return nil, newSSHTransportError(SSHErrorRemoteConfigInvalid, errSSHOutputLimit)
	case runErr := <-done:
		if ctx.Err() != nil {
			return nil, sshContextError(ctx)
		}
		if stdout.overflow.Load() || stderr.overflow.Load() {
			return nil, newSSHTransportError(SSHErrorRemoteConfigInvalid, errSSHOutputLimit)
		}
		if runErr != nil {
			return nil, newSSHTransportError(SSHErrorRemoteHelperUnavailable, nil)
		}
	}
	return append([]byte(nil), stdout.Bytes()...), nil
}

func (e BootstrapSSHExecutor) dial(ctx context.Context, host BootstrapSSHHost, signer ssh.Signer) (*ssh.Client, error) {
	expected, err := parsePinnedHostPublicKey(host.HostPublicKey)
	if err != nil {
		return nil, newSSHTransportError(SSHErrorRemoteConfigInvalid, nil)
	}
	expectedKey := expected.Marshal()
	config := &ssh.ClientConfig{
		User: host.AdminUser,
		Auth: []ssh.AuthMethod{ssh.PublicKeys(signer)},
		HostKeyCallback: func(_ string, _ net.Addr, actual ssh.PublicKey) error {
			if actual == nil || !bytes.Equal(actual.Marshal(), expectedKey) {
				return errSSHPinnedHostKeyMismatch
			}
			return nil
		},
		HostKeyAlgorithms: []string{ssh.KeyAlgoED25519},
	}
	timeout := e.DialTimeout
	if timeout <= 0 {
		timeout = defaultSSHDialTimeout
	}
	dialDeadline, dialDeadlineFromContext := effectiveSSHDeadline(ctx, time.Now().Add(timeout))
	dialer := net.Dialer{Deadline: dialDeadline}
	address := host.dialAddress()
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
			return nil, newSSHTransportError(SSHErrorConnectionRefused, nil)
		}
		return nil, newSSHTransportError(SSHErrorRemoteHelperUnavailable, nil)
	}
	stopCancellation := context.AfterFunc(ctx, func() { _ = raw.Close() })
	defer stopCancellation()
	handshakeDeadline, handshakeDeadlineFromContext := effectiveSSHDeadline(ctx, time.Now().Add(timeout))
	_ = raw.SetDeadline(handshakeDeadline)
	connection, channels, requests, err := ssh.NewClientConn(raw, address, config)
	if err != nil {
		_ = raw.Close()
		if ctx.Err() != nil {
			return nil, sshContextError(ctx)
		}
		if errors.Is(err, errSSHPinnedHostKeyMismatch) {
			return nil, newSSHTransportError(SSHErrorHostKeyMismatch, nil)
		}
		var netErr net.Error
		if errors.As(err, &netErr) && netErr.Timeout() {
			return nil, sshNetTimeoutError(err, handshakeDeadlineFromContext)
		}
		return nil, newSSHTransportError(SSHErrorAuthFailed, nil)
	}
	_ = raw.SetDeadline(time.Time{})
	return ssh.NewClient(connection, channels, requests), nil
}

func (e BootstrapSSHExecutor) outputLimit() int {
	if e.OutputLimit <= 0 || e.OutputLimit > bootstrapOutputMaxBytes {
		return bootstrapOutputMaxBytes
	}
	return e.OutputLimit
}

func parseBootstrapSourceCIDR(output []byte) (string, error) {
	if len(output) == 0 || len(output) > bootstrapOutputMaxBytes {
		return "", newSSHTransportError(SSHErrorRemoteConfigInvalid, nil)
	}
	line := strings.TrimSuffix(string(output), "\n")
	if line == string(output) || strings.ContainsAny(line, "\r\n") || !strings.HasPrefix(line, bootstrapInstallSourceCIDRPrefix) {
		return "", newSSHTransportError(SSHErrorRemoteConfigInvalid, nil)
	}
	value := strings.TrimPrefix(line, bootstrapInstallSourceCIDRPrefix)
	ip, network, err := net.ParseCIDR(value)
	if err != nil || ip == nil || network == nil || !ip.Equal(network.IP) {
		return "", newSSHTransportError(SSHErrorRemoteConfigInvalid, nil)
	}
	ones, bits := network.Mask.Size()
	if ones != bits || bits != 32 && bits != 128 {
		return "", newSSHTransportError(SSHErrorRemoteConfigInvalid, nil)
	}
	return value, nil
}

func parseBootstrapDatabaseOutput(output []byte) (map[string]string, error) {
	if len(output) > bootstrapOutputMaxBytes {
		return nil, newSSHTransportError(SSHErrorRemoteConfigInvalid, nil)
	}
	result := make(map[string]string)
	if len(output) == 0 {
		return result, nil
	}
	text := string(output)
	if !strings.HasSuffix(text, "\n") || strings.Contains(text, "\r") {
		return nil, newSSHTransportError(SSHErrorRemoteConfigInvalid, nil)
	}
	lines := strings.Split(strings.TrimSuffix(text, "\n"), "\n")
	if len(lines) > 2 {
		return nil, newSSHTransportError(SSHErrorRemoteConfigInvalid, nil)
	}
	for _, line := range lines {
		key, value, ok := strings.Cut(line, "=")
		if !ok ||
			key != "control_panel" && key != "observability" ||
			!bootstrapDatabaseOutputPattern.MatchString(value) ||
			result[key] != "" {
			return nil, newSSHTransportError(SSHErrorRemoteConfigInvalid, nil)
		}
		result[key] = value
	}
	return result, nil
}
