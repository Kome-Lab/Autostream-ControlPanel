package updateagent

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// ValidateRuntimeTargets performs the non-mutating checks required before the
// updater service is enabled. Returned lines contain only target IDs and
// verified public versions; configuration secrets are never rendered.
func ValidateRuntimeTargets(ctx context.Context, cfg Config, runner CommandRunner) ([]string, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if runner == nil {
		runner = OSCommandRunner{}
	}
	results := make([]string, 0, len(cfg.Targets))
	downloader := ReleaseDownloader{Token: cfg.GitHubToken}
	for _, configured := range cfg.Targets {
		target, err := securePrivilegedTarget(configured)
		if err != nil {
			return nil, fmt.Errorf("target %s: %w", configured.TargetID, err)
		}
		unlock, err := acquireTargetLock(target)
		if err != nil {
			return nil, fmt.Errorf("target %s: %w", target.TargetID, err)
		}
		var version string
		if target.DeploymentMode == ModeDocker {
			version, err = validateDockerRuntimeTarget(ctx, target, runner, func(ctx context.Context, target Target, bundle string) (ResolvedDockerRelease, error) {
				trustedDir, mkdirErr := os.MkdirTemp(target.Docker.ProjectDir, ".updater-validate-manifest-")
				if mkdirErr != nil {
					return ResolvedDockerRelease{}, mkdirErr
				}
				defer os.RemoveAll(trustedDir)
				return downloader.ResolveDockerRelease(ctx, bundle, target.ServiceType, target.Docker.ImageRepo, target.Docker.Channel, trustedDir)
			}, readHealthyTargetVersion)
		} else {
			version, err = validateSystemdRuntimeTarget(ctx, target, runner)
		}
		unlock()
		if err != nil {
			return nil, fmt.Errorf("target %s: %w", target.TargetID, err)
		}
		results = append(results, fmt.Sprintf("%s: %s %s verified", target.TargetID, target.DeploymentMode, version))
	}
	return results, nil
}

type dockerBundleResolver func(context.Context, Target, string) (ResolvedDockerRelease, error)

func validateDockerRuntimeTarget(ctx context.Context, target Target, runner CommandRunner, resolve dockerBundleResolver, readVersion targetVersionReader) (string, error) {
	if target.Docker == nil || resolve == nil || readVersion == nil {
		return "", errors.New("Docker validation dependencies are incomplete")
	}
	d := target.Docker
	envBytes, _, existed, err := readVersionEnv(d.VersionEnvFile)
	if err != nil || !existed {
		return "", errors.New("version_env_file has not been bootstrapped")
	}
	bundle, manifestDigest, err := parseVersionEnvPin(envBytes, d.ImageVariable)
	if err != nil {
		return "", err
	}
	trusted, err := resolve(ctx, target, bundle)
	if err != nil {
		return "", fmt.Errorf("verify pinned Docker release manifest: %w", err)
	}
	if !strings.EqualFold(trusted.ManifestDigest, manifestDigest) || !digestPattern.MatchString(trusted.PlatformDigest) || !versionPattern.MatchString(trusted.SourceVersion) {
		return "", errors.New("version_env_file pin does not match the trusted release manifest")
	}
	cid, err := managedContainerID(ctx, runner, d)
	if err != nil {
		return "", errors.New("managed compose service has no running container")
	}
	imageOut, err := runner.Run(ctx, d.ProjectDir, dockerCommandEnv(), d.DockerPath, "inspect", "--format={{.Image}}", cid)
	imageID := strings.ToLower(strings.TrimSpace(imageOut))
	if err != nil || !digestPattern.MatchString(imageID) {
		return "", errors.New("running Docker image ID is not a canonical SHA256 digest")
	}
	repoDigests, err := runner.Run(ctx, d.ProjectDir, dockerCommandEnv(), d.DockerPath, "image", "inspect", "--format={{json .RepoDigests}}", imageID)
	if err != nil || (!repositoryHasDigest(repoDigests, d.ImageRepo, trusted.PlatformDigest) && !repositoryHasDigest(repoDigests, d.ImageRepo, trusted.ManifestDigest)) {
		return "", errors.New("running Docker RepoDigest does not match the trusted release index or platform manifest")
	}
	platformOut, err := runner.Run(ctx, d.ProjectDir, dockerCommandEnv(), d.DockerPath, "image", "inspect", "--format={{.Os}}/{{.Architecture}}", imageID)
	if err != nil || strings.TrimSpace(platformOut) != "linux/"+runtime.GOARCH {
		return "", errors.New("running Docker image platform does not match this updater host")
	}
	actualVersion, err := readVersion(ctx, target)
	if err != nil || !versionsEqual(actualVersion, trusted.SourceVersion) {
		return "", errors.New("running Docker health version does not match the trusted release manifest")
	}
	modelOut, err := runner.Run(ctx, d.ProjectDir, dockerCommandEnv(), d.DockerPath, append(composeArgs(d, ""), "config", "--format", "json", "--no-env-resolution")...)
	if err != nil {
		return "", errors.New("could not resolve the configured Compose model")
	}
	if err := validateComposeModelSecurity([]byte(modelOut), d); err != nil {
		return "", err
	}
	digest, err := composeModelHash([]byte(modelOut), d.Service)
	if err != nil || !strings.EqualFold(digest, d.ComposeConfigSHA256) {
		return "", errors.New("compose_config_sha256 does not match the canonical Compose model")
	}
	return bundle, nil
}

func validateSystemdRuntimeTarget(ctx context.Context, target Target, runner CommandRunner) (string, error) {
	s := target.Systemd
	release, _, markerVersion, err := currentRelease(s.CurrentLink, s.ReleaseRoot)
	if err != nil || release == "" {
		return "", errors.New("current_link does not select a valid managed release")
	}
	for _, required := range append([]string{s.BinaryPath}, s.RequiredPaths...) {
		if info, statErr := os.Stat(filepath.Join(release, filepath.FromSlash(required))); statErr != nil || (!info.Mode().IsRegular() && !info.IsDir()) {
			return "", fmt.Errorf("managed release is missing required path %s", required)
		}
	}
	if err := verifyManagedReleaseChecksums(release); err != nil {
		return "", err
	}
	if err := verifySystemdProcess(ctx, target, release, runner); err != nil {
		return "", err
	}
	actualVersion, err := readHealthyTargetVersion(ctx, target)
	if err != nil || !versionsEqual(actualVersion, markerVersion) {
		return "", errors.New("systemd health version does not match the managed release marker")
	}
	return markerVersion, nil
}
