package updateradapter

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	controlversion "github.com/example/autostream-control-panel/internal/version"
)

const (
	defaultMaxArtifactBytes               = int64(256 << 20)
	localExecutorProtocolVersion          = 1
	hostSelfUpdateRecoveryProtocolVersion = 2
)

var versionPattern = regexp.MustCompile(`^v[0-9]+\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z]+(?:[.-][0-9A-Za-z]+)*)?$`)

type RepoSpec struct {
	Owner  string
	Repo   string
	Prefix string
}

var hostAgentReleaseRepository = RepoSpec{
	Owner: "Kome-Lab", Repo: "Autostream-Updater", Prefix: "autostream-host-agent",
}

type ReleaseDownloader struct {
	Client            *http.Client
	APIBase           string
	Token             string
	TrustedPublicOnly bool
	AllowHTTPForTest  bool

	hostAgentProvenanceVerifier releaseProvenanceVerifier
}

type githubRelease struct {
	TagName    string               `json:"tag_name"`
	Draft      bool                 `json:"draft"`
	Prerelease bool                 `json:"prerelease"`
	Immutable  bool                 `json:"immutable"`
	Assets     []githubReleaseAsset `json:"assets"`
}

type githubReleaseAsset struct {
	ID     int64  `json:"id"`
	Name   string `json:"name"`
	URL    string `json:"url"`
	Digest string `json:"digest"`
	State  string `json:"state"`
}

type hostAgentReleaseManifest struct {
	SchemaVersion                           int                   `json:"schema_version"`
	ReleaseID                               string                `json:"release_id"`
	Channel                                 string                `json:"channel"`
	PublishedAt                             string                `json:"published_at"`
	Commit                                  string                `json:"commit"`
	AgentVersion                            string                `json:"agent_version"`
	ProtocolVersion                         int                   `json:"protocol_version"`
	ObserveOnly                             bool                  `json:"observe_only"`
	LocalExecutorProtocolVersion            int                   `json:"local_executor_protocol_version"`
	LocalExecutorProbeOnly                  bool                  `json:"local_executor_probe_only"`
	LocalExecutorProtocolMinVersion         int                   `json:"local_executor_protocol_min_version"`
	LocalExecutorProtocolMaxVersion         int                   `json:"local_executor_protocol_max_version"`
	LocalExecutorProbeCompatible            bool                  `json:"local_executor_probe_compatible"`
	LocalExecutorMutationProtocolVersion    int                   `json:"local_executor_mutation_protocol_version"`
	LocalExecutorMutationEnabled            bool                  `json:"local_executor_mutation_enabled"`
	LocalExecutorMutationRequiresRootPolicy bool                  `json:"local_executor_mutation_requires_root_policy"`
	RecoveryProtocolVersion                 int                   `json:"recovery_protocol_version"`
	MinimumPanelVersion                     string                `json:"minimum_panel_version"`
	Artifacts                               []HostReleaseArtifact `json:"artifacts"`
}

type HostReleaseArtifact struct {
	OS     string `json:"os"`
	Arch   string `json:"arch"`
	Name   string `json:"name"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

// HostAgentReleaseMetadata is the Control Plane-safe immutable release view.
// URLs, credentials, local paths, and archive bytes are deliberately absent.
type HostAgentReleaseMetadata struct {
	Tag                     string
	Commit                  string
	PublishedAt             time.Time
	ManifestAssetID         int64
	ManifestAssetName       string
	ManifestSHA256          string
	ManifestChecksumAssetID int64
	ManifestChecksumSHA256  string
	ArchiveAssetID          int64
	ArchiveAssetName        string
	ArchiveSize             int64
	ArchiveSHA256           string
	ArchiveChecksumAssetID  int64
	ArchiveChecksumSHA256   string
	Arch                    string
	AgentProtocolVersion    int
	ExecutorProtocolVersion int
	MutationProtocolVersion int
	RecoveryProtocolVersion int
	MinimumPanelVersion     string
	AttestationVerifiedAt   time.Time
}

// ResolveHostAgentReleaseMetadata verifies the independent Updater release,
// exact tag commit, asset digests, manifest compatibility, and Actions
// provenance. It never downloads or exposes the runtime archive itself.
func (d ReleaseDownloader) ResolveHostAgentReleaseMetadata(ctx context.Context, version, arch string) (HostAgentReleaseMetadata, error) {
	if !versionPattern.MatchString(version) || (arch != "amd64" && arch != "arm64") {
		return HostAgentReleaseMetadata{}, errors.New("host agent release identity is invalid")
	}
	if !d.TrustedPublicOnly && !d.AllowHTTPForTest {
		return HostAgentReleaseMetadata{}, errors.New("host self-update requires public immutable release mode")
	}
	if strings.TrimSpace(d.Token) != "" {
		return HostAgentReleaseMetadata{}, errors.New("host self-update does not accept a release credential")
	}
	root, err := os.MkdirTemp("", "autostream-updater-release-metadata-")
	if err != nil {
		return HostAgentReleaseMetadata{}, err
	}
	defer os.RemoveAll(root)
	if err := os.Chmod(root, 0o700); err != nil {
		return HostAgentReleaseMetadata{}, err
	}

	assetName := hostAgentReleaseAssetName(version, arch)
	release, err := d.resolveHostAgentRelease(ctx, version, assetName)
	if err != nil {
		return HostAgentReleaseMetadata{}, err
	}
	manifestPath := filepath.Join(root, hostAgentManifestName)
	manifestDigest, err := d.downloadUpdaterReleaseAsset(ctx, release.Manifest, manifestPath, 4<<20)
	if err != nil {
		return HostAgentReleaseMetadata{}, err
	}
	manifestChecksumPath := manifestPath + ".sha256"
	manifestChecksumDigest, err := d.downloadUpdaterReleaseAsset(ctx, release.ManifestChecksum, manifestChecksumPath, 64<<10)
	if err != nil {
		return HostAgentReleaseMetadata{}, err
	}
	expectedManifestDigest, err := readSHA256File(manifestChecksumPath, hostAgentManifestName)
	if err != nil || expectedManifestDigest != manifestDigest {
		return HostAgentReleaseMetadata{}, errors.New("host agent manifest SHA256 sidecar does not match")
	}
	if err := d.verifyHostAgentReleaseProvenance(ctx, version, manifestDigest, release.TagCommit); err != nil {
		return HostAgentReleaseMetadata{}, err
	}
	manifest, artifact, publishedAt, err := validateHostAgentReleaseManifest(manifestPath, version, arch, assetName, release.TagCommit)
	if err != nil {
		return HostAgentReleaseMetadata{}, err
	}
	if !semverAtLeast(controlversion.Current(), manifest.MinimumPanelVersion) {
		return HostAgentReleaseMetadata{}, errors.New("Control Panel is older than the Updater release minimum")
	}
	archiveChecksumPath := filepath.Join(root, assetName+".sha256")
	archiveChecksumDigest, err := d.downloadUpdaterReleaseAsset(ctx, release.ArchiveChecksum, archiveChecksumPath, 64<<10)
	if err != nil {
		return HostAgentReleaseMetadata{}, err
	}
	expectedArchiveDigest, err := readSHA256File(archiveChecksumPath, assetName)
	if err != nil || expectedArchiveDigest != artifact.SHA256 || release.Archive.Digest != "sha256:"+artifact.SHA256 {
		return HostAgentReleaseMetadata{}, errors.New("host agent archive metadata does not match")
	}
	return HostAgentReleaseMetadata{
		Tag:                     version,
		Commit:                  release.TagCommit,
		PublishedAt:             publishedAt,
		ManifestAssetID:         release.Manifest.ID,
		ManifestAssetName:       release.Manifest.Name,
		ManifestSHA256:          manifestDigest,
		ManifestChecksumAssetID: release.ManifestChecksum.ID,
		ManifestChecksumSHA256:  manifestChecksumDigest,
		ArchiveAssetID:          release.Archive.ID,
		ArchiveAssetName:        release.Archive.Name,
		ArchiveSize:             artifact.Size,
		ArchiveSHA256:           artifact.SHA256,
		ArchiveChecksumAssetID:  release.ArchiveChecksum.ID,
		ArchiveChecksumSHA256:   archiveChecksumDigest,
		Arch:                    arch,
		AgentProtocolVersion:    manifest.ProtocolVersion,
		ExecutorProtocolVersion: manifest.LocalExecutorProtocolVersion,
		MutationProtocolVersion: manifest.LocalExecutorMutationProtocolVersion,
		RecoveryProtocolVersion: manifest.RecoveryProtocolVersion,
		MinimumPanelVersion:     manifest.MinimumPanelVersion,
		AttestationVerifiedAt:   time.Now().UTC(),
	}, nil
}

func (d ReleaseDownloader) resolveHostAgentRelease(ctx context.Context, version, assetName string) (resolvedImmutableRelease, error) {
	base := strings.TrimRight(strings.TrimSpace(d.APIBase), "/")
	if base == "" {
		base = "https://api.github.com"
	}
	if !d.AllowHTTPForTest {
		parsed, err := url.Parse(base)
		if err != nil || parsed.Scheme != "https" || !strings.EqualFold(parsed.Host, "api.github.com") ||
			parsed.EscapedPath() != "" || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.User != nil {
			return resolvedImmutableRelease{}, errors.New("host self-update requires GitHub's production API origin")
		}
	}
	endpoint := fmt.Sprintf("%s/repos/%s/%s/releases/tags/%s", base,
		url.PathEscape(hostAgentReleaseRepository.Owner), url.PathEscape(hostAgentReleaseRepository.Repo), url.PathEscape(version))
	var release githubRelease
	if err := d.getJSON(ctx, endpoint, &release); err != nil {
		return resolvedImmutableRelease{}, fmt.Errorf("resolve host agent release: %w", err)
	}
	if release.TagName != version || release.Draft || release.Prerelease || !release.Immutable {
		return resolvedImmutableRelease{}, errors.New("public Updater release is not immutable and stable for the requested tag")
	}
	assets := make(map[string]githubReleaseAsset, len(release.Assets))
	for _, asset := range release.Assets {
		if _, exists := assets[asset.Name]; exists {
			return resolvedImmutableRelease{}, fmt.Errorf("Updater release contains duplicate asset %q", asset.Name)
		}
		assets[asset.Name] = asset
	}
	archive, archiveOK := assets[assetName]
	archiveChecksum, checksumOK := assets[assetName+".sha256"]
	manifest, manifestOK := assets[hostAgentManifestName]
	manifestChecksum, manifestChecksumOK := assets[hostAgentManifestName+".sha256"]
	if !archiveOK || !checksumOK || !manifestOK || !manifestChecksumOK {
		return resolvedImmutableRelease{}, errors.New("host agent release is incomplete")
	}
	convert := func(asset githubReleaseAsset) (immutableReleaseAsset, error) {
		converted := immutableReleaseAsset{ID: asset.ID, Name: asset.Name, URL: asset.URL, Digest: asset.Digest, State: asset.State}
		if converted.State != "uploaded" || !immutableReleaseAssetDigestPattern.MatchString(converted.Digest) {
			return immutableReleaseAsset{}, fmt.Errorf("host agent release asset %q has invalid immutable metadata", converted.Name)
		}
		if err := d.validateUpdaterReleaseAssetURL(converted, base); err != nil {
			return immutableReleaseAsset{}, err
		}
		return converted, nil
	}
	converted := make([]immutableReleaseAsset, 0, 4)
	for _, asset := range []githubReleaseAsset{archive, archiveChecksum, manifest, manifestChecksum} {
		item, err := convert(asset)
		if err != nil {
			return resolvedImmutableRelease{}, err
		}
		converted = append(converted, item)
	}
	tagCommit, err := d.resolveUpdaterReleaseTagCommit(ctx, base, version)
	if err != nil {
		return resolvedImmutableRelease{}, err
	}
	return resolvedImmutableRelease{
		Archive: converted[0], ArchiveChecksum: converted[1], Manifest: converted[2], ManifestChecksum: converted[3], TagCommit: tagCommit,
	}, nil
}

func (d ReleaseDownloader) verifyHostAgentReleaseProvenance(ctx context.Context, version, manifestDigest, tagCommit string) error {
	verifier := d.hostAgentProvenanceVerifier
	if verifier == nil {
		verifier = sigstoreHostAgentProvenanceVerifier{}
	}
	if err := verifier.Verify(ctx, d, version, manifestDigest, tagCommit); err != nil {
		return fmt.Errorf("host agent manifest has no trusted Actions provenance: %w", err)
	}
	return nil
}

func validateHostAgentReleaseManifest(path, releaseVersion, arch, assetName, tagCommit string) (hostAgentReleaseManifest, HostReleaseArtifact, time.Time, error) {
	file, err := os.Open(path)
	if err != nil {
		return hostAgentReleaseManifest{}, HostReleaseArtifact{}, time.Time{}, err
	}
	defer file.Close()
	decoder := json.NewDecoder(io.LimitReader(file, 4<<20))
	decoder.DisallowUnknownFields()
	var manifest hostAgentReleaseManifest
	if err := decoder.Decode(&manifest); err != nil {
		return hostAgentReleaseManifest{}, HostReleaseArtifact{}, time.Time{}, errors.New("host agent manifest is invalid JSON")
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return hostAgentReleaseManifest{}, HostReleaseArtifact{}, time.Time{}, errors.New("host agent manifest contains trailing data")
	}
	publishedAt, err := time.Parse(time.RFC3339, manifest.PublishedAt)
	if err != nil || publishedAt.Location() != time.UTC {
		return hostAgentReleaseManifest{}, HostReleaseArtifact{}, time.Time{}, errors.New("host agent manifest published_at is invalid")
	}
	if manifest.SchemaVersion != 1 || manifest.ReleaseID != releaseVersion || manifest.Channel != "host-agent" ||
		manifest.AgentVersion != releaseVersion || manifest.Commit != tagCommit || !updaterReleaseCommitPattern.MatchString(manifest.Commit) {
		return hostAgentReleaseManifest{}, HostReleaseArtifact{}, time.Time{}, errors.New("host agent manifest identity is invalid")
	}
	if manifest.ProtocolVersion != 2 || manifest.ObserveOnly ||
		manifest.LocalExecutorProtocolVersion != LocalExecutorMutationProtocolVersion || manifest.LocalExecutorProbeOnly ||
		manifest.LocalExecutorProtocolMinVersion != localExecutorProtocolVersion ||
		manifest.LocalExecutorProtocolMaxVersion != LocalExecutorMutationProtocolVersion || !manifest.LocalExecutorProbeCompatible ||
		manifest.LocalExecutorMutationProtocolVersion != LocalExecutorMutationProtocolVersion || !manifest.LocalExecutorMutationEnabled ||
		!manifest.LocalExecutorMutationRequiresRootPolicy || manifest.RecoveryProtocolVersion != hostSelfUpdateRecoveryProtocolVersion {
		return hostAgentReleaseManifest{}, HostReleaseArtifact{}, time.Time{}, errors.New("host agent manifest protocol compatibility is invalid")
	}
	if !versionPattern.MatchString(manifest.MinimumPanelVersion) || len(manifest.Artifacts) != 2 {
		return hostAgentReleaseManifest{}, HostReleaseArtifact{}, time.Time{}, errors.New("host agent manifest compatibility or artifact set is invalid")
	}
	seen := make(map[string]bool, 2)
	var selected HostReleaseArtifact
	for _, artifact := range manifest.Artifacts {
		expectedName := hostAgentReleaseAssetName(releaseVersion, artifact.Arch)
		if artifact.OS != "linux" || (artifact.Arch != "amd64" && artifact.Arch != "arm64") || seen[artifact.Arch] ||
			artifact.Name != expectedName || artifact.Size <= 0 || artifact.Size > defaultMaxArtifactBytes ||
			len(artifact.SHA256) != 64 || artifact.SHA256 != strings.ToLower(artifact.SHA256) {
			return hostAgentReleaseManifest{}, HostReleaseArtifact{}, time.Time{}, errors.New("host agent manifest contains invalid artifact metadata")
		}
		if _, err := hex.DecodeString(artifact.SHA256); err != nil {
			return hostAgentReleaseManifest{}, HostReleaseArtifact{}, time.Time{}, errors.New("host agent manifest contains an invalid artifact SHA256")
		}
		seen[artifact.Arch] = true
		if artifact.Arch == arch && artifact.Name == assetName {
			selected = artifact
		}
	}
	if !seen["amd64"] || !seen["arm64"] || selected.Name == "" {
		return hostAgentReleaseManifest{}, HostReleaseArtifact{}, time.Time{}, errors.New("host agent manifest is missing the requested architecture")
	}
	return manifest, selected, publishedAt, nil
}

func hostAgentReleaseAssetName(version, arch string) string {
	return hostAgentReleaseRepository.Prefix + "_" + version + "_linux_" + arch + ".tar.gz"
}

func (d ReleaseDownloader) httpClient() *http.Client {
	base := d.Client
	if base == nil {
		base = &http.Client{Timeout: 2 * time.Minute}
	}
	clone := *base
	if clone.Timeout == 0 || clone.Timeout > 2*time.Minute {
		clone.Timeout = 2 * time.Minute
	}
	prior := clone.CheckRedirect
	clone.CheckRedirect = func(request *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return errors.New("too many release download redirects")
		}
		if request.URL.Scheme != "https" && !d.AllowHTTPForTest {
			return errors.New("release download redirect must use HTTPS")
		}
		if d.TrustedPublicOnly && !trustedGitHubReleaseRedirectHost(request.URL.Hostname()) {
			return errors.New("public release download redirected outside trusted GitHub storage")
		}
		if len(via) > 0 && !strings.EqualFold(request.URL.Host, via[0].URL.Host) {
			request.Header.Del("Authorization")
		}
		if prior != nil {
			return prior(request, via)
		}
		return nil
	}
	return &clone
}

func trustedGitHubReleaseRedirectHost(host string) bool {
	switch strings.ToLower(strings.TrimSpace(host)) {
	case "api.github.com", "github.com", "objects.githubusercontent.com", "release-assets.githubusercontent.com":
		return true
	default:
		return false
	}
}

func (d ReleaseDownloader) newRequest(ctx context.Context, raw string) (*http.Request, error) {
	if d.TrustedPublicOnly && strings.TrimSpace(d.Token) != "" {
		return nil, errors.New("public release metadata mode does not accept a release credential")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	request.Header.Set("User-Agent", "autostream-control-panel-updater-adapter")
	if strings.TrimSpace(d.Token) != "" {
		request.Header.Set("Authorization", "Bearer "+strings.TrimSpace(d.Token))
	}
	return request, nil
}

func (d ReleaseDownloader) getJSON(ctx context.Context, raw string, out any) error {
	request, err := d.newRequest(ctx, raw)
	if err != nil {
		return err
	}
	response, err := d.httpClient().Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("GitHub returned HTTP %d", response.StatusCode)
	}
	decoder := json.NewDecoder(io.LimitReader(response.Body, 4<<20))
	if err := decoder.Decode(out); err != nil {
		return err
	}
	return nil
}

func (d ReleaseDownloader) validateAssetURL(raw, apiBase string) error {
	assetURL, err := url.Parse(raw)
	if err != nil || assetURL.Host == "" {
		return errors.New("GitHub returned an invalid asset URL")
	}
	baseURL, err := url.Parse(apiBase)
	if err != nil || baseURL.Host == "" {
		return errors.New("GitHub API base is invalid")
	}
	if assetURL.Scheme != "https" && !d.AllowHTTPForTest {
		return errors.New("release asset URL must use HTTPS")
	}
	if !strings.EqualFold(assetURL.Host, baseURL.Host) {
		return errors.New("release asset URL must use the configured GitHub API host")
	}
	return nil
}

func (d ReleaseDownloader) downloadFile(ctx context.Context, raw, destination string, maxBytes int64) (string, error) {
	request, err := d.newRequest(ctx, raw)
	if err != nil {
		return "", err
	}
	request.Header.Set("Accept", "application/octet-stream")
	response, err := d.httpClient().Do(request)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return "", fmt.Errorf("asset endpoint returned HTTP %d", response.StatusCode)
	}
	if maxBytes <= 0 || response.ContentLength > maxBytes {
		return "", errors.New("release asset exceeds size limit")
	}
	temporary := destination + ".part"
	file, err := os.OpenFile(temporary, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return "", err
	}
	remove := true
	defer func() {
		_ = file.Close()
		if remove {
			_ = os.Remove(temporary)
		}
	}()
	hash := sha256.New()
	written, err := io.Copy(io.MultiWriter(file, hash), io.LimitReader(response.Body, maxBytes+1))
	if err != nil {
		return "", err
	}
	if written > maxBytes {
		return "", errors.New("release asset exceeds size limit")
	}
	if err := file.Sync(); err != nil {
		return "", err
	}
	if err := file.Close(); err != nil {
		return "", err
	}
	if err := os.Rename(temporary, destination); err != nil {
		return "", err
	}
	remove = false
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func readSHA256File(path, expectedName string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	scanner := bufio.NewScanner(io.LimitReader(file, 1<<20))
	if !scanner.Scan() {
		return "", errors.New("SHA256 sidecar is empty")
	}
	line := scanner.Text()
	if len(line) < 67 || line[64:66] != "  " {
		return "", errors.New("SHA256 sidecar has an invalid format or filename")
	}
	digest, checksumName := line[:64], line[66:]
	if checksumName != expectedName || strings.ToLower(digest) != digest || checksumName == "" {
		return "", errors.New("SHA256 sidecar has an invalid format or filename")
	}
	if _, err := hex.DecodeString(digest); err != nil {
		return "", errors.New("SHA256 sidecar contains an invalid digest")
	}
	if scanner.Scan() || scanner.Err() != nil {
		return "", errors.New("SHA256 sidecar must contain exactly one line")
	}
	return digest, nil
}

func semverAtLeast(current, minimum string) bool {
	parse := func(value string) ([3]int, bool) {
		var out [3]int
		value = strings.TrimPrefix(strings.TrimSpace(value), "v")
		if _, err := fmt.Sscanf(strings.SplitN(value, "-", 2)[0], "%d.%d.%d", &out[0], &out[1], &out[2]); err != nil {
			return out, false
		}
		return out, true
	}
	if !regexp.MustCompile(`^v[0-9]+\.[0-9]+\.[0-9]+$`).MatchString(minimum) {
		return false
	}
	actual, actualOK := parse(current)
	required, requiredOK := parse(minimum)
	if !actualOK || !requiredOK {
		return false
	}
	for index := range actual {
		if actual[index] != required[index] {
			return actual[index] > required[index]
		}
	}
	return !strings.Contains(strings.TrimPrefix(strings.TrimSpace(current), "v"), "-")
}
