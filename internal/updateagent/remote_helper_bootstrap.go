package updateagent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const remoteBootstrapTokenMaxBytes = 16 << 10

// LoadBootstrapRemoteHelperConfig is the only entrypoint that accepts the
// all-zero compose approval sentinel. It accepts it for exactly the selected
// Docker target; the same draft remains invalid for validate-config and rpc.
func LoadBootstrapRemoteHelperConfig(path, targetID string, requireRootOwned bool) (HelperConfig, error) {
	if !filepath.IsAbs(path) {
		return HelperConfig{}, errors.New("helper config path must be absolute")
	}
	info, err := os.Lstat(path)
	if err != nil {
		return HelperConfig{}, errors.New("stat bootstrap helper config")
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > helperConfigMaxBytes {
		return HelperConfig{}, errors.New("bootstrap helper config must be a bounded regular non-symlink file")
	}
	f, err := os.Open(path)
	if err != nil {
		return HelperConfig{}, errors.New("open bootstrap helper config")
	}
	defer f.Close()
	openedInfo, err := f.Stat()
	if err != nil || !openedInfo.Mode().IsRegular() || !os.SameFile(info, openedInfo) || openedInfo.Size() <= 0 || openedInfo.Size() > helperConfigMaxBytes {
		return HelperConfig{}, errors.New("bootstrap helper config changed during secure open")
	}
	if requireRootOwned {
		if openedInfo.Mode().Perm()&0o007 != 0 || openedInfo.Mode().Perm()&0o022 != 0 {
			return HelperConfig{}, errors.New("bootstrap helper config must be root-owned and inaccessible to other users")
		}
		if err := validateRootOwnedFileAndParents(path, openedInfo, "bootstrap helper config"); err != nil {
			return HelperConfig{}, err
		}
	}
	data, err := io.ReadAll(io.LimitReader(f, helperConfigMaxBytes+1))
	if err != nil || len(data) == 0 || len(data) > helperConfigMaxBytes {
		return HelperConfig{}, errors.New("read bootstrap helper config")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var cfg HelperConfig
	if err := decoder.Decode(&cfg); err != nil {
		return HelperConfig{}, errors.New("decode bootstrap helper config")
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return HelperConfig{}, errors.New("bootstrap helper config contains trailing data")
	}

	validated := cfg
	validated.Targets = append([]Target(nil), cfg.Targets...)
	found := false
	for i := range validated.Targets {
		if validated.Targets[i].TargetID != targetID {
			continue
		}
		if found || validated.Targets[i].DeploymentMode != ModeDocker || validated.Targets[i].Docker == nil || validated.Targets[i].Docker.ComposeConfigSHA256 != strings.Repeat("0", 64) {
			return HelperConfig{}, errors.New("bootstrap target must be one zero-sentinel Docker target")
		}
		found = true
		docker := *validated.Targets[i].Docker
		docker.ComposeConfigSHA256 = strings.Repeat("a", 64)
		validated.Targets[i].Docker = &docker
	}
	if !found {
		return HelperConfig{}, errors.New("bootstrap target must be one zero-sentinel Docker target")
	}
	if err := validated.Validate(); err != nil {
		return HelperConfig{}, err
	}
	return cfg, nil
}

// ReadRemoteBootstrapToken reads a single one-time GitHub read token from
// stdin. It accepts one conventional line ending but rejects other whitespace,
// control bytes, empty input, and oversized input.
func ReadRemoteBootstrapToken(input io.Reader) (RemoteSecret, error) {
	data, err := io.ReadAll(io.LimitReader(input, remoteBootstrapTokenMaxBytes+1))
	if err != nil || len(data) == 0 || len(data) > remoteBootstrapTokenMaxBytes {
		return "", errors.New("bootstrap token input rejected")
	}
	data = bytes.TrimSuffix(data, []byte("\n"))
	data = bytes.TrimSuffix(data, []byte("\r"))
	value := string(data)
	for i := range data {
		data[i] = 0
	}
	if !validRemoteSecret(value) || strings.TrimSpace(value) != value {
		return "", errors.New("bootstrap token input rejected")
	}
	return NewRemoteSecret(value), nil
}

// BootstrapRemoteDockerTarget verifies an already-running Docker target
// against the signed release manifest, seeds its version pin atomically, and
// returns the compose approval digest. The one-time token is never stored.
func BootstrapRemoteDockerTarget(ctx context.Context, cfg HelperConfig, targetID string, token RemoteSecret, runner CommandRunner) (string, error) {
	if !validRemoteSecret(token.Reveal()) {
		return "", errors.New("bootstrap token is invalid")
	}
	target, ok := cfg.Target(targetID)
	if !ok || target.DeploymentMode != ModeDocker || target.Docker == nil || target.Docker.ComposeConfigSHA256 != strings.Repeat("0", 64) {
		return "", errors.New("target is not a zero-sentinel Docker bootstrap target")
	}
	if runner == nil {
		runner = OSCommandRunner{NewProcessGroup: true}
	}
	secureTarget, err := securePrivilegedTarget(target)
	if err != nil {
		return "", err
	}
	unlock, err := acquireTargetLock(secureTarget)
	if err != nil {
		return "", err
	}
	defer unlock()

	downloader := ReleaseDownloader{Token: token.Reveal()}
	digest, err := bootstrapDockerTarget(ctx, secureTarget, runner, func(ctx context.Context, target Target) (ResolvedDockerRelease, error) {
		trustedDir, err := os.MkdirTemp(target.Docker.ProjectDir, ".update-host-bootstrap-")
		if err != nil {
			return ResolvedDockerRelease{}, err
		}
		defer os.RemoveAll(trustedDir)
		return downloader.ResolveDockerRelease(ctx, target.Docker.CurrentVersion, target.ServiceType, target.Docker.ImageRepo, target.Docker.Channel, trustedDir)
	}, readHealthyTargetVersion)
	downloader.Token = ""
	if err != nil {
		return "", err
	}
	if len(digest) != 64 || strings.ToLower(digest) != digest {
		return "", errors.New("bootstrap compose digest is invalid")
	}
	return digest, nil
}
