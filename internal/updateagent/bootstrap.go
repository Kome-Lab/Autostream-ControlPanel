package updateagent

import (
	"context"
	"errors"
	"fmt"
	"os"
	"runtime"
	"strings"
)

// BootstrapDockerTarget records a verified, already-running Docker bundle as
// the updater's rollback baseline.  It deliberately accepts only a configured
// target: image names, paths and versions never come from the command line.
//
// The returned digest is the canonical compose_config_sha256 to copy into the
// root-owned configuration after review.  The version env file is changed only
// after the running container, its repository digest, and its local health
// version all agree with the trusted GitHub release manifest.
func BootstrapDockerTarget(ctx context.Context, cfg Config, targetID string, runner CommandRunner) (string, error) {
	if err := cfg.validate(true); err != nil {
		return "", err
	}
	target, ok := cfg.Target(targetID)
	if !ok || target.DeploymentMode != ModeDocker || target.Docker == nil {
		return "", errors.New("target is not a configured Docker target")
	}
	if runner == nil {
		runner = OSCommandRunner{}
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

	downloader := ReleaseDownloader{Token: cfg.GitHubToken}
	return bootstrapDockerTarget(ctx, secureTarget, runner, func(ctx context.Context, target Target) (ResolvedDockerRelease, error) {
		trustedDir, err := os.MkdirTemp(target.Docker.ProjectDir, ".updater-bootstrap-manifest-")
		if err != nil {
			return ResolvedDockerRelease{}, err
		}
		defer os.RemoveAll(trustedDir)
		return downloader.ResolveDockerRelease(ctx, target.Docker.CurrentVersion, target.ServiceType, target.Docker.ImageRepo, target.Docker.Channel, trustedDir)
	}, readHealthyTargetVersion)
}

// dockerReleaseResolver and targetVersionReader make the security-critical
// decision table testable without permitting callers to inject paths or image
// identities into the public bootstrap API.
type dockerReleaseResolver func(context.Context, Target) (ResolvedDockerRelease, error)
type targetVersionReader func(context.Context, Target) (string, error)

func bootstrapDockerTarget(ctx context.Context, target Target, runner CommandRunner, resolve dockerReleaseResolver, readVersion targetVersionReader) (string, error) {
	if target.DeploymentMode != ModeDocker || target.Docker == nil || runner == nil || resolve == nil || readVersion == nil {
		return "", errors.New("Docker bootstrap dependencies are incomplete")
	}
	d := target.Docker
	trusted, err := resolve(ctx, target)
	if err != nil {
		return "", fmt.Errorf("verify configured Docker release manifest: %w", err)
	}
	if !versionPattern.MatchString(d.CurrentVersion) || !versionPattern.MatchString(trusted.SourceVersion) || !digestPattern.MatchString(trusted.ManifestDigest) || !digestPattern.MatchString(trusted.PlatformDigest) {
		return "", errors.New("trusted Docker release metadata is incomplete")
	}
	digestRef := d.ImageRepo + "@" + trusted.PlatformDigest
	if _, err := runner.Run(ctx, d.ProjectDir, dockerCommandEnv(), d.DockerPath, "pull", digestRef); err != nil {
		return "", fmt.Errorf("authenticate and pull trusted Docker platform manifest: %w", err)
	}
	pulledIDOut, err := runner.Run(ctx, d.ProjectDir, dockerCommandEnv(), d.DockerPath, "image", "inspect", "--format={{.Id}}", digestRef)
	pulledID := strings.ToLower(strings.TrimSpace(pulledIDOut))
	if err != nil || !digestPattern.MatchString(pulledID) {
		return "", errors.New("pulled trusted Docker image ID is invalid")
	}

	cid, err := managedContainerID(ctx, runner, d)
	if err != nil {
		return "", fmt.Errorf("find configured Docker container: %w", err)
	}
	imageID, err := runner.Run(ctx, d.ProjectDir, dockerCommandEnv(), d.DockerPath, "inspect", "--format={{.Image}}", cid)
	if err != nil {
		return "", fmt.Errorf("inspect configured Docker container: %w", err)
	}
	imageID = strings.ToLower(strings.TrimSpace(imageID))
	if !digestPattern.MatchString(imageID) || imageID != pulledID {
		return "", errors.New("running Docker image ID does not match the freshly pulled trusted platform manifest")
	}
	repoDigests, err := runner.Run(ctx, d.ProjectDir, dockerCommandEnv(), d.DockerPath, "image", "inspect", "--format={{json .RepoDigests}}", imageID)
	if err != nil {
		return "", fmt.Errorf("inspect configured Docker image repository digest: %w", err)
	}
	if !repositoryHasDigest(repoDigests, d.ImageRepo, trusted.PlatformDigest) && !repositoryHasDigest(repoDigests, d.ImageRepo, trusted.ManifestDigest) {
		return "", errors.New("running Docker image RepoDigest does not match the trusted release index or platform manifest")
	}
	platform, err := runner.Run(ctx, d.ProjectDir, dockerCommandEnv(), d.DockerPath, "image", "inspect", "--format={{.Os}}/{{.Architecture}}", imageID)
	if err != nil || strings.TrimSpace(platform) != "linux/"+runtime.GOARCH {
		return "", errors.New("running Docker image platform does not match this updater host")
	}
	actualVersion, err := readVersion(ctx, target)
	if err != nil || !versionsEqual(actualVersion, trusted.SourceVersion) {
		return "", errors.New("running Docker target health version does not match the trusted release manifest")
	}

	original, mode, existed, err := readVersionEnv(d.VersionEnvFile)
	if err != nil {
		return "", err
	}
	seed, err := updateVersionEnv(original, d.ImageVariable, d.CurrentVersion+"@"+strings.ToLower(trusted.ManifestDigest))
	if err != nil {
		return "", fmt.Errorf("prepare Docker bootstrap version env: %w", err)
	}
	if !existed {
		mode = 0o640
	}
	if err := writeAtomicFile(d.VersionEnvFile, seed, mode); err != nil {
		return "", fmt.Errorf("seed Docker bootstrap version env: %w", err)
	}

	// Calculate the approval digest after seeding the exact, trusted pin.  If
	// the Compose model is unsafe or cannot be resolved, restore the prior env
	// state so a failed bootstrap cannot leave a half-configured target behind.
	digest, digestErr := bootstrapComposeDigest(ctx, runner, d)
	if digestErr != nil {
		if restoreErr := restoreVersionEnv(d.VersionEnvFile, original, mode, existed); restoreErr != nil {
			return "", fmt.Errorf("%v; restore Docker version env: %w", digestErr, restoreErr)
		}
		return "", digestErr
	}
	return digest, nil
}

func bootstrapComposeDigest(ctx context.Context, runner CommandRunner, d *DockerTarget) (string, error) {
	out, err := runner.Run(ctx, d.ProjectDir, dockerCommandEnv(), d.DockerPath, append(composeArgs(d, ""), "config", "--format", "json", "--no-env-resolution")...)
	if err != nil {
		return "", fmt.Errorf("resolve Docker compose model: %w", err)
	}
	if err := validateComposeModelSecurity([]byte(out), d); err != nil {
		return "", err
	}
	digest, err := composeModelHash([]byte(out), d.Service)
	if err != nil {
		return "", err
	}
	return digest, nil
}
