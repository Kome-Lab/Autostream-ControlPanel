package updateagent

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	controlversion "github.com/example/autostream-control-panel/internal/version"
)

const updateHostBootstrapManifestName = "update-host-bootstrap-manifest.json"

var updateHostBootstrapCommitPattern = regexp.MustCompile(`^[0-9a-f]{40}$`)
var updateHostBootstrapAssetDigestPattern = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

var updateHostBootstrapRepository = RepoSpec{
	Owner:  "Kome-Lab",
	Repo:   "Autostream-ControlPanel",
	Prefix: "autostream-update-host",
}

type updateHostBootstrapManifest struct {
	SchemaVersion            int                   `json:"schema_version"`
	ReleaseID                string                `json:"release_id"`
	Channel                  string                `json:"channel"`
	PublishedAt              string                `json:"published_at"`
	Commit                   string                `json:"commit"`
	HelperVersion            string                `json:"helper_version"`
	ProtocolVersion          int                   `json:"protocol_version"`
	MinimumControllerVersion string                `json:"minimum_controller_version"`
	Artifacts                []HostReleaseArtifact `json:"artifacts"`
}

type updateHostBootstrapGitHubAsset struct {
	ID     int64  `json:"id"`
	Name   string `json:"name"`
	URL    string `json:"url"`
	Digest string `json:"digest"`
	State  string `json:"state"`
}

type updateHostBootstrapGitHubRelease struct {
	TagName   string                           `json:"tag_name"`
	Draft     bool                             `json:"draft"`
	Immutable bool                             `json:"immutable"`
	Assets    []updateHostBootstrapGitHubAsset `json:"assets"`
}

type updateHostBootstrapGitObject struct {
	Type string `json:"type"`
	SHA  string `json:"sha"`
}

type updateHostBootstrapGitRef struct {
	Object updateHostBootstrapGitObject `json:"object"`
}

type updateHostBootstrapGitTag struct {
	Object updateHostBootstrapGitObject `json:"object"`
}

type resolvedUpdateHostBootstrapRelease struct {
	Archive          updateHostBootstrapGitHubAsset
	ArchiveChecksum  updateHostBootstrapGitHubAsset
	Manifest         updateHostBootstrapGitHubAsset
	ManifestChecksum updateHostBootstrapGitHubAsset
	TagCommit        string
}

// DownloadUpdateHostBootstrap downloads and verifies the non-resident host
// helper from the matching Control Panel release before extracting it.
func (d ReleaseDownloader) DownloadUpdateHostBootstrap(ctx context.Context, version, arch, destDir string) (DownloadedArtifact, error) {
	if !versionPattern.MatchString(version) {
		return DownloadedArtifact{}, errors.New("bootstrap helper version is invalid")
	}
	if arch != "amd64" && arch != "arm64" {
		return DownloadedArtifact{}, errors.New("only amd64 and arm64 bootstrap helpers are supported")
	}
	if err := os.MkdirAll(destDir, 0o700); err != nil {
		return DownloadedArtifact{}, fmt.Errorf("create bootstrap artifact directory: %w", err)
	}

	assetName := updateHostBootstrapAssetName(version, arch)
	release, err := d.resolveUpdateHostBootstrapRelease(ctx, version, assetName)
	if err != nil {
		return DownloadedArtifact{}, err
	}

	manifestPath := filepath.Join(destDir, updateHostBootstrapManifestName)
	manifestDigest, err := d.downloadUpdateHostBootstrapAsset(ctx, release.Manifest, manifestPath, 4<<20)
	if err != nil {
		return DownloadedArtifact{}, fmt.Errorf("download update host bootstrap manifest: %w", err)
	}
	manifestChecksumPath := manifestPath + ".sha256"
	if _, err := d.downloadUpdateHostBootstrapAsset(ctx, release.ManifestChecksum, manifestChecksumPath, 64<<10); err != nil {
		return DownloadedArtifact{}, fmt.Errorf("download update host bootstrap manifest checksum: %w", err)
	}
	expectedManifestDigest, err := readSHA256File(manifestChecksumPath, updateHostBootstrapManifestName)
	if err != nil || !strings.EqualFold(expectedManifestDigest, manifestDigest) {
		return DownloadedArtifact{}, errors.New("update host bootstrap manifest SHA256 sidecar does not match")
	}
	if err := d.verifyUpdateHostBootstrapProvenance(ctx, version, manifestDigest, release.TagCommit); err != nil {
		return DownloadedArtifact{}, err
	}
	manifestArtifact, err := validateUpdateHostBootstrapManifest(manifestPath, version, arch, assetName, release.TagCommit)
	if err != nil {
		return DownloadedArtifact{}, err
	}

	archivePath := filepath.Join(destDir, assetName)
	maxArtifact := d.MaxArtifactBytes
	if maxArtifact <= 0 {
		maxArtifact = defaultMaxArtifactBytes
	}
	digest, err := d.downloadUpdateHostBootstrapAsset(ctx, release.Archive, archivePath, maxArtifact)
	if err != nil {
		return DownloadedArtifact{}, fmt.Errorf("download update host bootstrap artifact: %w", err)
	}
	checksumPath := archivePath + ".sha256"
	if _, err := d.downloadUpdateHostBootstrapAsset(ctx, release.ArchiveChecksum, checksumPath, 64<<10); err != nil {
		return DownloadedArtifact{}, fmt.Errorf("download update host bootstrap checksum: %w", err)
	}
	expectedDigest, err := readSHA256File(checksumPath, assetName)
	if err != nil {
		return DownloadedArtifact{}, err
	}
	if !strings.EqualFold(expectedDigest, digest) {
		return DownloadedArtifact{}, errors.New("update host bootstrap artifact SHA256 does not match sidecar")
	}
	archiveInfo, statErr := os.Stat(archivePath)
	if statErr != nil || !archiveInfo.Mode().IsRegular() || archiveInfo.Size() != manifestArtifact.Size || !strings.EqualFold(digest, manifestArtifact.SHA256) {
		return DownloadedArtifact{}, errors.New("update host bootstrap artifact does not match the trusted manifest")
	}

	maxExtract := d.MaxExtractBytes
	if maxExtract <= 0 {
		maxExtract = defaultMaxExtractBytes
	}
	maxEntries := d.MaxEntries
	if maxEntries <= 0 {
		maxEntries = defaultMaxEntries
	}
	root, err := ExtractTarGz(archivePath, filepath.Join(destDir, "extracted"), maxExtract, maxEntries)
	if err != nil {
		return DownloadedArtifact{}, err
	}
	if err := VerifyInnerChecksums(root); err != nil {
		return DownloadedArtifact{}, err
	}
	return DownloadedArtifact{
		ArchivePath: archivePath,
		RootDir:     root,
		SHA256:      digest,
		AssetName:   assetName,
	}, nil
}

func (d ReleaseDownloader) resolveUpdateHostBootstrapRelease(ctx context.Context, version, assetName string) (resolvedUpdateHostBootstrapRelease, error) {
	base := strings.TrimRight(d.APIBase, "/")
	if base == "" {
		base = "https://api.github.com"
	}
	endpoint := fmt.Sprintf(
		"%s/repos/%s/%s/releases/tags/%s",
		base,
		url.PathEscape(updateHostBootstrapRepository.Owner),
		url.PathEscape(updateHostBootstrapRepository.Repo),
		url.PathEscape(version),
	)
	var release updateHostBootstrapGitHubRelease
	if err := d.getJSON(ctx, endpoint, &release); err != nil {
		return resolvedUpdateHostBootstrapRelease{}, fmt.Errorf("resolve update host bootstrap release: %w", err)
	}
	if release.TagName != version {
		return resolvedUpdateHostBootstrapRelease{}, errors.New("update host bootstrap release tag does not match the requested version")
	}
	if release.Draft {
		return resolvedUpdateHostBootstrapRelease{}, errors.New("update host bootstrap release is still a draft")
	}
	if !release.Immutable {
		return resolvedUpdateHostBootstrapRelease{}, errors.New("update host bootstrap release is not immutable")
	}
	assets := make(map[string]updateHostBootstrapGitHubAsset, len(release.Assets))
	for _, asset := range release.Assets {
		if _, exists := assets[asset.Name]; exists {
			return resolvedUpdateHostBootstrapRelease{}, fmt.Errorf("update host bootstrap release contains duplicate asset %q", asset.Name)
		}
		assets[asset.Name] = asset
	}
	archive, archiveOK := assets[assetName]
	archiveChecksum, checksumOK := assets[assetName+".sha256"]
	manifest, manifestOK := assets[updateHostBootstrapManifestName]
	manifestChecksum, manifestChecksumOK := assets[updateHostBootstrapManifestName+".sha256"]
	if !archiveOK || !checksumOK || !manifestOK || !manifestChecksumOK {
		return resolvedUpdateHostBootstrapRelease{}, fmt.Errorf("release is missing %s, its checksum, or the update host bootstrap manifest checksums", assetName)
	}
	for _, asset := range []updateHostBootstrapGitHubAsset{archive, archiveChecksum, manifest, manifestChecksum} {
		if asset.State != "uploaded" {
			return resolvedUpdateHostBootstrapRelease{}, fmt.Errorf("release asset %q is not completely uploaded", asset.Name)
		}
		if err := d.validateUpdateHostBootstrapAssetURL(asset, base); err != nil {
			return resolvedUpdateHostBootstrapRelease{}, err
		}
		if !updateHostBootstrapAssetDigestPattern.MatchString(asset.Digest) {
			return resolvedUpdateHostBootstrapRelease{}, fmt.Errorf("GitHub returned an invalid digest for release asset %q", asset.Name)
		}
	}
	tagCommit, err := d.resolveUpdateHostBootstrapTagCommit(ctx, base, version)
	if err != nil {
		return resolvedUpdateHostBootstrapRelease{}, err
	}
	return resolvedUpdateHostBootstrapRelease{
		Archive:          archive,
		ArchiveChecksum:  archiveChecksum,
		Manifest:         manifest,
		ManifestChecksum: manifestChecksum,
		TagCommit:        tagCommit,
	}, nil
}

func (d ReleaseDownloader) validateUpdateHostBootstrapAssetURL(asset updateHostBootstrapGitHubAsset, base string) error {
	if asset.ID <= 0 {
		return fmt.Errorf("GitHub returned an invalid id for release asset %q", asset.Name)
	}
	if err := d.validateAssetURL(asset.URL, base); err != nil {
		return err
	}
	assetURL, assetErr := url.Parse(asset.URL)
	baseURL, baseErr := url.Parse(base)
	if assetErr != nil || baseErr != nil {
		return errors.New("GitHub returned an invalid release asset API URL")
	}
	expectedPath := strings.TrimRight(baseURL.EscapedPath(), "/") + fmt.Sprintf(
		"/repos/%s/%s/releases/assets/%d",
		url.PathEscape(updateHostBootstrapRepository.Owner),
		url.PathEscape(updateHostBootstrapRepository.Repo),
		asset.ID,
	)
	if assetURL.EscapedPath() != expectedPath ||
		assetURL.RawQuery != "" ||
		assetURL.Fragment != "" ||
		assetURL.User != nil {
		return fmt.Errorf("release asset %q does not use its canonical GitHub asset API URL", asset.Name)
	}
	return nil
}

func (d ReleaseDownloader) resolveUpdateHostBootstrapTagCommit(ctx context.Context, base, version string) (string, error) {
	endpoint := fmt.Sprintf(
		"%s/repos/%s/%s/git/ref/tags/%s",
		base,
		url.PathEscape(updateHostBootstrapRepository.Owner),
		url.PathEscape(updateHostBootstrapRepository.Repo),
		url.PathEscape(version),
	)
	var ref updateHostBootstrapGitRef
	if err := d.getJSON(ctx, endpoint, &ref); err != nil {
		return "", fmt.Errorf("resolve update host bootstrap release tag: %w", err)
	}
	object := ref.Object
	seenTags := make(map[string]struct{}, 8)
	for depth := 0; object.Type == "tag"; depth++ {
		if depth >= 8 {
			return "", errors.New("update host bootstrap release tag exceeds the dereference limit")
		}
		if !updateHostBootstrapCommitPattern.MatchString(object.SHA) {
			return "", errors.New("update host bootstrap release tag object is invalid")
		}
		if _, seen := seenTags[object.SHA]; seen {
			return "", errors.New("update host bootstrap release tag contains a cycle")
		}
		seenTags[object.SHA] = struct{}{}
		tagEndpoint := fmt.Sprintf(
			"%s/repos/%s/%s/git/tags/%s",
			base,
			url.PathEscape(updateHostBootstrapRepository.Owner),
			url.PathEscape(updateHostBootstrapRepository.Repo),
			url.PathEscape(object.SHA),
		)
		var tag updateHostBootstrapGitTag
		if err := d.getJSON(ctx, tagEndpoint, &tag); err != nil {
			return "", fmt.Errorf("dereference update host bootstrap release tag: %w", err)
		}
		object = tag.Object
	}
	if object.Type != "commit" || !updateHostBootstrapCommitPattern.MatchString(object.SHA) {
		return "", errors.New("update host bootstrap release tag does not resolve to a valid commit")
	}
	return object.SHA, nil
}

func (d ReleaseDownloader) downloadUpdateHostBootstrapAsset(ctx context.Context, asset updateHostBootstrapGitHubAsset, dest string, max int64) (string, error) {
	digest, err := d.downloadFile(ctx, asset.URL, dest, max)
	if err != nil {
		return "", err
	}
	if asset.Digest != "sha256:"+digest {
		_ = os.Remove(dest)
		return "", fmt.Errorf("release asset %q does not match the GitHub API digest", asset.Name)
	}
	return digest, nil
}

func validateUpdateHostBootstrapManifest(path, releaseVersion, arch, assetName, tagCommit string) (HostReleaseArtifact, error) {
	f, err := os.Open(path)
	if err != nil {
		return HostReleaseArtifact{}, err
	}
	defer f.Close()

	decoder := json.NewDecoder(io.LimitReader(f, 4<<20))
	decoder.DisallowUnknownFields()
	var manifest updateHostBootstrapManifest
	if err := decoder.Decode(&manifest); err != nil {
		return HostReleaseArtifact{}, errors.New("update host bootstrap manifest is invalid JSON")
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return HostReleaseArtifact{}, errors.New("update host bootstrap manifest contains trailing data")
	}
	if manifest.SchemaVersion != 1 ||
		manifest.ReleaseID != releaseVersion ||
		manifest.HelperVersion != releaseVersion ||
		manifest.Channel != "update-host-bootstrap" ||
		manifest.ProtocolVersion != RemoteProtocolVersion {
		return HostReleaseArtifact{}, errors.New("update host bootstrap manifest identity is invalid")
	}
	if _, err := time.Parse(time.RFC3339, manifest.PublishedAt); err != nil || !updateHostBootstrapCommitPattern.MatchString(manifest.Commit) {
		return HostReleaseArtifact{}, errors.New("update host bootstrap manifest provenance metadata is invalid")
	}
	if manifest.Commit != tagCommit {
		return HostReleaseArtifact{}, errors.New("update host bootstrap manifest commit does not match the immutable release tag")
	}
	if !versionPattern.MatchString(manifest.MinimumControllerVersion) ||
		!updateHostBootstrapSemverAtLeast(controlversion.Current(), manifest.MinimumControllerVersion) {
		return HostReleaseArtifact{}, fmt.Errorf("update host bootstrap requires controller %s or newer", manifest.MinimumControllerVersion)
	}
	if len(manifest.Artifacts) != 2 {
		return HostReleaseArtifact{}, errors.New("update host bootstrap manifest must contain amd64 and arm64 artifacts")
	}

	seen := make(map[string]bool, len(manifest.Artifacts))
	var selected HostReleaseArtifact
	for _, artifact := range manifest.Artifacts {
		expectedName := updateHostBootstrapAssetName(releaseVersion, artifact.Arch)
		if artifact.OS != "linux" ||
			(artifact.Arch != "amd64" && artifact.Arch != "arm64") ||
			seen[artifact.Arch] ||
			artifact.Name != expectedName ||
			artifact.Size <= 0 ||
			artifact.Size > defaultMaxArtifactBytes ||
			len(artifact.SHA256) != 64 ||
			artifact.SHA256 != strings.ToLower(artifact.SHA256) {
			return HostReleaseArtifact{}, errors.New("update host bootstrap manifest contains invalid artifact metadata")
		}
		if _, err := hex.DecodeString(artifact.SHA256); err != nil {
			return HostReleaseArtifact{}, errors.New("update host bootstrap manifest contains an invalid artifact SHA256")
		}
		seen[artifact.Arch] = true
		if artifact.Arch == arch && artifact.Name == assetName {
			selected = artifact
		}
	}
	if !seen["amd64"] || !seen["arm64"] || selected.Name == "" {
		return HostReleaseArtifact{}, errors.New("update host bootstrap manifest is missing the requested architecture")
	}
	return selected, nil
}

func updateHostBootstrapAssetName(version, arch string) string {
	return fmt.Sprintf("%s_%s_linux_%s.tar.gz", updateHostBootstrapRepository.Prefix, version, arch)
}

type updateHostBootstrapSemver struct {
	core       [3]uint64
	prerelease []string
}

func updateHostBootstrapSemverAtLeast(current, minimum string) bool {
	actual, actualOK := parseUpdateHostBootstrapSemver(current)
	required, requiredOK := parseUpdateHostBootstrapSemver(minimum)
	if !actualOK || !requiredOK {
		return false
	}
	for i := range actual.core {
		if actual.core[i] != required.core[i] {
			return actual.core[i] > required.core[i]
		}
	}
	return compareUpdateHostBootstrapPrerelease(actual.prerelease, required.prerelease) >= 0
}

func parseUpdateHostBootstrapSemver(value string) (updateHostBootstrapSemver, bool) {
	if !versionPattern.MatchString(value) {
		return updateHostBootstrapSemver{}, false
	}
	value = strings.TrimPrefix(value, "v")
	core, prerelease, hasPrerelease := strings.Cut(value, "-")
	coreParts := strings.Split(core, ".")
	if len(coreParts) != 3 {
		return updateHostBootstrapSemver{}, false
	}
	var parsed updateHostBootstrapSemver
	for i, part := range coreParts {
		if len(part) > 1 && part[0] == '0' {
			return updateHostBootstrapSemver{}, false
		}
		number, err := strconv.ParseUint(part, 10, 64)
		if err != nil {
			return updateHostBootstrapSemver{}, false
		}
		parsed.core[i] = number
	}
	if !hasPrerelease {
		return parsed, true
	}
	parsed.prerelease = strings.Split(prerelease, ".")
	for _, identifier := range parsed.prerelease {
		if identifier == "" || (decimalIdentifier(identifier) && len(identifier) > 1 && identifier[0] == '0') {
			return updateHostBootstrapSemver{}, false
		}
	}
	return parsed, true
}

func compareUpdateHostBootstrapPrerelease(actual, required []string) int {
	if len(actual) == 0 && len(required) == 0 {
		return 0
	}
	if len(actual) == 0 {
		return 1
	}
	if len(required) == 0 {
		return -1
	}
	for i := 0; i < len(actual) && i < len(required); i++ {
		if actual[i] == required[i] {
			continue
		}
		actualNumeric := decimalIdentifier(actual[i])
		requiredNumeric := decimalIdentifier(required[i])
		switch {
		case actualNumeric && requiredNumeric:
			if len(actual[i]) != len(required[i]) {
				if len(actual[i]) > len(required[i]) {
					return 1
				}
				return -1
			}
		case actualNumeric:
			return -1
		case requiredNumeric:
			return 1
		}
		if actual[i] > required[i] {
			return 1
		}
		return -1
	}
	switch {
	case len(actual) > len(required):
		return 1
	case len(actual) < len(required):
		return -1
	default:
		return 0
	}
}

func decimalIdentifier(value string) bool {
	if value == "" {
		return false
	}
	for _, ch := range value {
		if ch < '0' || ch > '9' {
			return false
		}
	}
	return true
}
