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
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	controlversion "github.com/example/autostream-control-panel/internal/version"
)

const (
	bootstrapTestVersion = "v1.2.3"
	bootstrapTestCommit  = "dddddddddddddddddddddddddddddddddddddddd"
)

type bootstrapManifestFixture struct {
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

type bootstrapReleaseFixture struct {
	archive         []byte
	archiveSidecar  string
	manifest        []byte
	manifestSidecar string
	immutable       bool
	draft           bool
	tagCommit       string
	tagChain        []string
	tagCycle        bool
	githubDigests   map[string]string
	assetStates     map[string]string
	duplicateAsset  string
	noncanonicalURL string
}

type bootstrapTarEntry struct {
	name string
	body []byte
}

type bootstrapProvenanceVerifierFunc func(
	context.Context,
	ReleaseDownloader,
	string,
	string,
	string,
) error

func (f bootstrapProvenanceVerifierFunc) Verify(
	ctx context.Context,
	downloader ReleaseDownloader,
	version string,
	manifestDigest string,
	tagCommit string,
) error {
	return f(ctx, downloader, version, manifestDigest, tagCommit)
}

func TestDownloadUpdateHostBootstrapVerifiesAndExtractsRelease(t *testing.T) {
	restoreControllerVersion(t, bootstrapTestVersion)
	fixture := newBootstrapReleaseFixture(t, nil)
	server := serveBootstrapRelease(t, fixture, func(r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer bootstrap-token" {
			t.Errorf("Authorization = %q", got)
		}
	})
	defer server.Close()

	dest := t.TempDir()
	downloader := trustedBootstrapReleaseDownloader(server)
	downloader.Token = "bootstrap-token"
	got, err := downloader.DownloadUpdateHostBootstrap(context.Background(), bootstrapTestVersion, "amd64", dest)
	if err != nil {
		t.Fatal(err)
	}
	assetName := bootstrapAssetName(bootstrapTestVersion, "amd64")
	if got.AssetName != assetName {
		t.Fatalf("AssetName = %q", got.AssetName)
	}
	if got.ArchivePath != filepath.Join(dest, assetName) {
		t.Fatalf("ArchivePath = %q", got.ArchivePath)
	}
	archiveSum := sha256.Sum256(fixture.archive)
	if got.SHA256 != hex.EncodeToString(archiveSum[:]) {
		t.Fatalf("SHA256 = %q", got.SHA256)
	}
	binary, err := os.ReadFile(filepath.Join(got.RootDir, "bin", "autostream-update-host"))
	if err != nil {
		t.Fatal(err)
	}
	if string(binary) != "helper-v1.2.3" {
		t.Fatalf("extracted helper = %q", binary)
	}
}

func TestDownloadUpdateHostBootstrapRequiresTrustedActionsProvenance(t *testing.T) {
	restoreControllerVersion(t, bootstrapTestVersion)
	fixture := newBootstrapReleaseFixture(t, nil)
	archiveRequested := false
	server := serveBootstrapRelease(t, fixture, func(r *http.Request) {
		if r.URL.Path == "/repos/Kome-Lab/Autostream-ControlPanel/releases/assets/1" {
			archiveRequested = true
		}
	})
	defer server.Close()

	downloader := ReleaseDownloader{
		APIBase:          server.URL,
		Client:           server.Client(),
		AllowHTTPForTest: true,
	}
	_, err := downloader.DownloadUpdateHostBootstrap(
		context.Background(),
		bootstrapTestVersion,
		"amd64",
		t.TempDir(),
	)
	if err == nil || !strings.Contains(err.Error(), "no trusted Actions provenance") {
		t.Fatalf("expected missing provenance rejection, got %v", err)
	}
	if archiveRequested {
		t.Fatal("privileged helper archive was downloaded before provenance verification")
	}
}

func TestDownloadUpdateHostBootstrapBindsProvenanceToDigestVersionAndCommit(t *testing.T) {
	restoreControllerVersion(t, bootstrapTestVersion)
	fixture := newBootstrapReleaseFixture(t, nil)
	server := serveBootstrapRelease(t, fixture, nil)
	defer server.Close()

	called := false
	downloader := trustedBootstrapReleaseDownloader(server)
	downloader.bootstrapProvenanceVerifier = bootstrapProvenanceVerifierFunc(func(
		_ context.Context,
		_ ReleaseDownloader,
		version string,
		manifestDigest string,
		tagCommit string,
	) error {
		called = true
		expectedManifestDigest := strings.TrimPrefix(
			fixture.githubDigests[updateHostBootstrapManifestName],
			"sha256:",
		)
		if version != bootstrapTestVersion ||
			manifestDigest != expectedManifestDigest ||
			tagCommit != bootstrapTestCommit {
			return errors.New("unexpected provenance binding")
		}
		return nil
	})
	if _, err := downloader.DownloadUpdateHostBootstrap(
		context.Background(),
		bootstrapTestVersion,
		"amd64",
		t.TempDir(),
	); err != nil {
		t.Fatalf("trusted provenance binding rejected: %v", err)
	}
	if !called {
		t.Fatal("provenance verifier was not called")
	}
}

func TestDownloadUpdateHostBootstrapRejectsMutableRelease(t *testing.T) {
	restoreControllerVersion(t, bootstrapTestVersion)
	fixture := newBootstrapReleaseFixture(t, nil)
	fixture.immutable = false
	server := serveBootstrapRelease(t, fixture, nil)
	defer server.Close()

	downloader := trustedBootstrapReleaseDownloader(server)
	_, err := downloader.DownloadUpdateHostBootstrap(context.Background(), bootstrapTestVersion, "amd64", t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "not immutable") {
		t.Fatalf("expected mutable release rejection, got %v", err)
	}
}

func TestDownloadUpdateHostBootstrapRejectsDraftAndIncompleteAsset(t *testing.T) {
	restoreControllerVersion(t, bootstrapTestVersion)
	t.Run("draft", func(t *testing.T) {
		fixture := newBootstrapReleaseFixture(t, nil)
		fixture.draft = true
		server := serveBootstrapRelease(t, fixture, nil)
		defer server.Close()

		downloader := trustedBootstrapReleaseDownloader(server)
		if _, err := downloader.DownloadUpdateHostBootstrap(context.Background(), bootstrapTestVersion, "amd64", t.TempDir()); err == nil || !strings.Contains(err.Error(), "still a draft") {
			t.Fatalf("expected draft release rejection, got %v", err)
		}
	})
	t.Run("asset state", func(t *testing.T) {
		fixture := newBootstrapReleaseFixture(t, nil)
		fixture.assetStates[updateHostBootstrapManifestName] = "new"
		server := serveBootstrapRelease(t, fixture, nil)
		defer server.Close()

		downloader := trustedBootstrapReleaseDownloader(server)
		if _, err := downloader.DownloadUpdateHostBootstrap(context.Background(), bootstrapTestVersion, "amd64", t.TempDir()); err == nil || !strings.Contains(err.Error(), "not completely uploaded") {
			t.Fatalf("expected incomplete asset rejection, got %v", err)
		}
	})
}

func TestDownloadUpdateHostBootstrapPeelsAnnotatedReleaseTag(t *testing.T) {
	restoreControllerVersion(t, bootstrapTestVersion)
	for name, chain := range map[string][]string{
		"one tag":    {strings.Repeat("a", 40)},
		"three tags": {strings.Repeat("a", 40), strings.Repeat("b", 40), strings.Repeat("c", 40)},
	} {
		t.Run(name, func(t *testing.T) {
			fixture := newBootstrapReleaseFixture(t, nil)
			fixture.tagChain = chain
			server := serveBootstrapRelease(t, fixture, nil)
			defer server.Close()

			downloader := trustedBootstrapReleaseDownloader(server)
			if _, err := downloader.DownloadUpdateHostBootstrap(context.Background(), bootstrapTestVersion, "amd64", t.TempDir()); err != nil {
				t.Fatalf("annotated release tag rejected: %v", err)
			}
		})
	}
}

func TestDownloadUpdateHostBootstrapRejectsCyclicAnnotatedReleaseTag(t *testing.T) {
	restoreControllerVersion(t, bootstrapTestVersion)
	fixture := newBootstrapReleaseFixture(t, nil)
	fixture.tagChain = []string{strings.Repeat("a", 40), strings.Repeat("b", 40)}
	fixture.tagCycle = true
	server := serveBootstrapRelease(t, fixture, nil)
	defer server.Close()

	downloader := trustedBootstrapReleaseDownloader(server)
	if _, err := downloader.DownloadUpdateHostBootstrap(context.Background(), bootstrapTestVersion, "amd64", t.TempDir()); err == nil || !strings.Contains(err.Error(), "cycle") {
		t.Fatalf("expected cyclic tag rejection, got %v", err)
	}
}

func TestDownloadUpdateHostBootstrapRejectsAnnotatedReleaseTagBeyondDepthLimit(t *testing.T) {
	restoreControllerVersion(t, bootstrapTestVersion)
	fixture := newBootstrapReleaseFixture(t, nil)
	for i := 0; i < 9; i++ {
		fixture.tagChain = append(fixture.tagChain, fmt.Sprintf("%040x", i+1))
	}
	server := serveBootstrapRelease(t, fixture, nil)
	defer server.Close()

	downloader := trustedBootstrapReleaseDownloader(server)
	if _, err := downloader.DownloadUpdateHostBootstrap(context.Background(), bootstrapTestVersion, "amd64", t.TempDir()); err == nil || !strings.Contains(err.Error(), "dereference limit") {
		t.Fatalf("expected tag depth rejection, got %v", err)
	}
}

func TestDownloadUpdateHostBootstrapRejectsUntrustedGitHubAssetMetadata(t *testing.T) {
	restoreControllerVersion(t, bootstrapTestVersion)
	assetName := bootstrapAssetName(bootstrapTestVersion, "amd64")
	tests := map[string]func(*bootstrapReleaseFixture){
		"missing digest": func(fixture *bootstrapReleaseFixture) {
			fixture.githubDigests[assetName] = ""
		},
		"noncanonical digest": func(fixture *bootstrapReleaseFixture) {
			fixture.githubDigests[assetName] = "SHA256:" + strings.Repeat("A", 64)
		},
		"duplicate name": func(fixture *bootstrapReleaseFixture) {
			fixture.duplicateAsset = assetName
		},
		"noncanonical API URL": func(fixture *bootstrapReleaseFixture) {
			fixture.noncanonicalURL = assetName
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			fixture := newBootstrapReleaseFixture(t, nil)
			mutate(&fixture)
			server := serveBootstrapRelease(t, fixture, nil)
			defer server.Close()

			downloader := trustedBootstrapReleaseDownloader(server)
			if _, err := downloader.DownloadUpdateHostBootstrap(context.Background(), bootstrapTestVersion, "amd64", t.TempDir()); err == nil {
				t.Fatal("expected untrusted GitHub asset metadata rejection")
			}
		})
	}
}

func TestDownloadUpdateHostBootstrapRejectsInternallyConsistentReplacementAgainstGitHubDigest(t *testing.T) {
	restoreControllerVersion(t, bootstrapTestVersion)
	fixture := newBootstrapReleaseFixture(t, nil)
	trustedDigests := make(map[string]string, len(fixture.githubDigests))
	for name, digest := range fixture.githubDigests {
		trustedDigests[name] = digest
	}

	fixture.archive = makeBootstrapArchive(t, "malicious-but-internally-consistent", "")
	fixture.refreshOuterMetadata(t)
	fixture.githubDigests = trustedDigests

	server := serveBootstrapRelease(t, fixture, nil)
	defer server.Close()
	downloader := trustedBootstrapReleaseDownloader(server)
	_, err := downloader.DownloadUpdateHostBootstrap(context.Background(), bootstrapTestVersion, "amd64", t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "GitHub API digest") {
		t.Fatalf("expected GitHub digest rejection for mutually consistent replacement, got %v", err)
	}
}

func TestDownloadUpdateHostBootstrapRejectsEachGitHubAssetDigestMismatch(t *testing.T) {
	restoreControllerVersion(t, bootstrapTestVersion)
	for _, name := range []string{
		bootstrapAssetName(bootstrapTestVersion, "amd64"),
		bootstrapAssetName(bootstrapTestVersion, "amd64") + ".sha256",
		"update-host-bootstrap-manifest.json",
		"update-host-bootstrap-manifest.json.sha256",
	} {
		t.Run(name, func(t *testing.T) {
			fixture := newBootstrapReleaseFixture(t, nil)
			fixture.githubDigests[name] = "sha256:" + strings.Repeat("a", 64)
			server := serveBootstrapRelease(t, fixture, nil)
			defer server.Close()

			downloader := trustedBootstrapReleaseDownloader(server)
			_, err := downloader.DownloadUpdateHostBootstrap(context.Background(), bootstrapTestVersion, "amd64", t.TempDir())
			if err == nil || !strings.Contains(err.Error(), "GitHub API digest") {
				t.Fatalf("expected %s GitHub digest mismatch rejection, got %v", name, err)
			}
		})
	}
}

func TestDownloadUpdateHostBootstrapRejectsManifestCommitThatDoesNotMatchReleaseTag(t *testing.T) {
	restoreControllerVersion(t, bootstrapTestVersion)
	fixture := newBootstrapReleaseFixture(t, nil)
	fixture.tagCommit = strings.Repeat("f", 40)
	server := serveBootstrapRelease(t, fixture, nil)
	defer server.Close()

	downloader := trustedBootstrapReleaseDownloader(server)
	_, err := downloader.DownloadUpdateHostBootstrap(context.Background(), bootstrapTestVersion, "amd64", t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "manifest commit does not match") {
		t.Fatalf("expected release tag commit mismatch rejection, got %v", err)
	}
}

func TestDownloadUpdateHostBootstrapRejectsInvalidManifest(t *testing.T) {
	restoreControllerVersion(t, bootstrapTestVersion)
	tests := map[string]func(*bootstrapManifestFixture){
		"schema": func(manifest *bootstrapManifestFixture) {
			manifest.SchemaVersion = 2
		},
		"release id": func(manifest *bootstrapManifestFixture) {
			manifest.ReleaseID = "v1.2.2"
		},
		"channel": func(manifest *bootstrapManifestFixture) {
			manifest.Channel = "host"
		},
		"helper version": func(manifest *bootstrapManifestFixture) {
			manifest.HelperVersion = "v1.2.2"
		},
		"protocol": func(manifest *bootstrapManifestFixture) {
			manifest.ProtocolVersion = RemoteProtocolVersion + 1
		},
		"minimum controller": func(manifest *bootstrapManifestFixture) {
			manifest.MinimumControllerVersion = "v1.2.4"
		},
		"artifact name": func(manifest *bootstrapManifestFixture) {
			manifest.Artifacts[0].Name = "autostream-update-host_v1.2.2_linux_amd64.tar.gz"
		},
		"artifact size": func(manifest *bootstrapManifestFixture) {
			manifest.Artifacts[0].Size++
		},
		"artifact digest": func(manifest *bootstrapManifestFixture) {
			manifest.Artifacts[0].SHA256 = strings.Repeat("a", 64)
		},
		"missing architecture": func(manifest *bootstrapManifestFixture) {
			manifest.Artifacts = manifest.Artifacts[1:]
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			fixture := newBootstrapReleaseFixture(t, mutate)
			server := serveBootstrapRelease(t, fixture, nil)
			defer server.Close()
			downloader := trustedBootstrapReleaseDownloader(server)
			if _, err := downloader.DownloadUpdateHostBootstrap(context.Background(), bootstrapTestVersion, "amd64", t.TempDir()); err == nil {
				t.Fatal("expected invalid bootstrap manifest rejection")
			}
		})
	}
}

func TestDownloadUpdateHostBootstrapRejectsUntrustedChecksumsAndArchive(t *testing.T) {
	restoreControllerVersion(t, bootstrapTestVersion)
	tests := map[string]func(*bootstrapReleaseFixture){
		"manifest sidecar": func(fixture *bootstrapReleaseFixture) {
			fixture.manifestSidecar = strings.Repeat("a", 64) + "  update-host-bootstrap-manifest.json\n"
		},
		"archive sidecar": func(fixture *bootstrapReleaseFixture) {
			fixture.archiveSidecar = strings.Repeat("b", 64) + "  " + bootstrapAssetName(bootstrapTestVersion, "amd64") + "\n"
		},
		"inner checksum": func(fixture *bootstrapReleaseFixture) {
			fixture.archive = makeBootstrapArchive(t, "helper-v1.2.3", strings.Repeat("c", 64))
			fixture.refreshOuterMetadata(t)
		},
		"unsafe archive path": func(fixture *bootstrapReleaseFixture) {
			fixture.archive = makeBootstrapTarGz(t, []bootstrapTarEntry{{
				name: bootstrapArtifactRoot(bootstrapTestVersion, "amd64") + "/../../escape",
				body: []byte("escape"),
			}})
			fixture.refreshOuterMetadata(t)
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			fixture := newBootstrapReleaseFixture(t, nil)
			mutate(&fixture)
			server := serveBootstrapRelease(t, fixture, nil)
			defer server.Close()
			downloader := trustedBootstrapReleaseDownloader(server)
			if _, err := downloader.DownloadUpdateHostBootstrap(context.Background(), bootstrapTestVersion, "amd64", t.TempDir()); err == nil {
				t.Fatal("expected untrusted bootstrap release rejection")
			}
		})
	}
}

func TestDownloadUpdateHostBootstrapBoundsArchiveDownload(t *testing.T) {
	restoreControllerVersion(t, bootstrapTestVersion)
	fixture := newBootstrapReleaseFixture(t, nil)
	server := serveBootstrapRelease(t, fixture, nil)
	defer server.Close()
	downloader := trustedBootstrapReleaseDownloader(server)
	downloader.MaxArtifactBytes = int64(len(fixture.archive) - 1)
	if _, err := downloader.DownloadUpdateHostBootstrap(context.Background(), bootstrapTestVersion, "amd64", t.TempDir()); err == nil || !strings.Contains(err.Error(), "size limit") {
		t.Fatalf("expected bounded download rejection, got %v", err)
	}
}

func TestDownloadUpdateHostBootstrapValidatesRequest(t *testing.T) {
	downloader := ReleaseDownloader{}
	for name, request := range map[string]struct {
		version string
		arch    string
	}{
		"version": {version: "1.2.3", arch: "amd64"},
		"arch":    {version: bootstrapTestVersion, arch: "386"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := downloader.DownloadUpdateHostBootstrap(context.Background(), request.version, request.arch, t.TempDir()); err == nil {
				t.Fatal("expected invalid bootstrap request rejection")
			}
		})
	}
}

func TestUpdateHostBootstrapManifestUsesSemverPrereleaseOrdering(t *testing.T) {
	tests := map[string]struct {
		current string
		minimum string
		ok      bool
	}{
		"same prerelease":      {current: "v1.2.3-rc.1", minimum: "v1.2.3-rc.1", ok: true},
		"newer prerelease":     {current: "v1.2.3-rc.2", minimum: "v1.2.3-rc.1", ok: true},
		"stable after release": {current: "v1.2.3", minimum: "v1.2.3-rc.1", ok: true},
		"older prerelease":     {current: "v1.2.3-rc.1", minimum: "v1.2.3-rc.2", ok: false},
		"prerelease before stable": {
			current: "v1.2.3-rc.2",
			minimum: "v1.2.3",
			ok:      false,
		},
		"numeric identifiers": {current: "v1.2.3-beta.11", minimum: "v1.2.3-beta.2", ok: true},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			restoreControllerVersion(t, test.current)
			fixture := newBootstrapReleaseFixture(t, func(manifest *bootstrapManifestFixture) {
				manifest.MinimumControllerVersion = test.minimum
			})
			path := filepath.Join(t.TempDir(), "update-host-bootstrap-manifest.json")
			if err := os.WriteFile(path, fixture.manifest, 0o600); err != nil {
				t.Fatal(err)
			}
			_, err := validateUpdateHostBootstrapManifest(
				path,
				bootstrapTestVersion,
				"amd64",
				bootstrapAssetName(bootstrapTestVersion, "amd64"),
				bootstrapTestCommit,
			)
			if test.ok && err != nil {
				t.Fatalf("compatible SemVer rejected: %v", err)
			}
			if !test.ok && err == nil {
				t.Fatal("incompatible SemVer accepted")
			}
		})
	}
}

func newBootstrapReleaseFixture(t *testing.T, mutate func(*bootstrapManifestFixture)) bootstrapReleaseFixture {
	t.Helper()
	archive := makeBootstrapArchive(t, "helper-v1.2.3", "")
	archiveSum := sha256.Sum256(archive)
	assetName := bootstrapAssetName(bootstrapTestVersion, "amd64")
	manifest := bootstrapManifestFixture{
		SchemaVersion:            1,
		ReleaseID:                bootstrapTestVersion,
		Channel:                  "update-host-bootstrap",
		PublishedAt:              "2026-07-27T00:00:00Z",
		Commit:                   bootstrapTestCommit,
		HelperVersion:            bootstrapTestVersion,
		ProtocolVersion:          RemoteProtocolVersion,
		MinimumControllerVersion: "v1.2.0",
		Artifacts: []HostReleaseArtifact{
			{
				OS:     "linux",
				Arch:   "amd64",
				Name:   assetName,
				Size:   int64(len(archive)),
				SHA256: hex.EncodeToString(archiveSum[:]),
			},
			{
				OS:     "linux",
				Arch:   "arm64",
				Name:   bootstrapAssetName(bootstrapTestVersion, "arm64"),
				Size:   1,
				SHA256: strings.Repeat("e", 64),
			},
		},
	}
	if mutate != nil {
		mutate(&manifest)
	}
	manifestJSON, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	manifestSum := sha256.Sum256(manifestJSON)
	fixture := bootstrapReleaseFixture{
		archive:         archive,
		archiveSidecar:  fmt.Sprintf("%x  %s\n", archiveSum, assetName),
		manifest:        manifestJSON,
		manifestSidecar: fmt.Sprintf("%x  update-host-bootstrap-manifest.json\n", manifestSum),
		immutable:       true,
		tagCommit:       bootstrapTestCommit,
	}
	fixture.refreshGitHubDigests()
	fixture.assetStates = map[string]string{}
	for name := range fixture.githubDigests {
		fixture.assetStates[name] = "uploaded"
	}
	return fixture
}

func (fixture *bootstrapReleaseFixture) refreshOuterMetadata(t *testing.T) {
	t.Helper()
	archiveSum := sha256.Sum256(fixture.archive)
	assetName := bootstrapAssetName(bootstrapTestVersion, "amd64")
	fixture.archiveSidecar = fmt.Sprintf("%x  %s\n", archiveSum, assetName)

	var manifest bootstrapManifestFixture
	if err := json.Unmarshal(fixture.manifest, &manifest); err != nil {
		t.Fatal(err)
	}
	manifest.Artifacts[0].Size = int64(len(fixture.archive))
	manifest.Artifacts[0].SHA256 = hex.EncodeToString(archiveSum[:])
	var err error
	fixture.manifest, err = json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	manifestSum := sha256.Sum256(fixture.manifest)
	fixture.manifestSidecar = fmt.Sprintf("%x  update-host-bootstrap-manifest.json\n", manifestSum)
	fixture.refreshGitHubDigests()
}

func (fixture *bootstrapReleaseFixture) refreshGitHubDigests() {
	fixture.githubDigests = map[string]string{
		bootstrapAssetName(bootstrapTestVersion, "amd64"):             bootstrapGitHubDigest(fixture.archive),
		bootstrapAssetName(bootstrapTestVersion, "amd64") + ".sha256": bootstrapGitHubDigest([]byte(fixture.archiveSidecar)),
		"update-host-bootstrap-manifest.json":                         bootstrapGitHubDigest(fixture.manifest),
		"update-host-bootstrap-manifest.json.sha256":                  bootstrapGitHubDigest([]byte(fixture.manifestSidecar)),
	}
}

func bootstrapGitHubDigest(payload []byte) string {
	sum := sha256.Sum256(payload)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func makeBootstrapArchive(t *testing.T, binary, checksumOverride string) []byte {
	t.Helper()
	sum := sha256.Sum256([]byte(binary))
	digest := hex.EncodeToString(sum[:])
	if checksumOverride != "" {
		digest = checksumOverride
	}
	root := bootstrapArtifactRoot(bootstrapTestVersion, "amd64")
	return makeBootstrapTarGz(t, []bootstrapTarEntry{
		{name: root + "/bin/autostream-update-host", body: []byte(binary)},
		{name: root + "/checksums.txt", body: []byte(digest + "  ./bin/autostream-update-host\n")},
	})
}

func makeBootstrapTarGz(t *testing.T, entries []bootstrapTarEntry) []byte {
	t.Helper()
	var out bytes.Buffer
	gz := gzip.NewWriter(&out)
	tw := tar.NewWriter(gz)
	for _, entry := range entries {
		header := &tar.Header{
			Name:     entry.name,
			Typeflag: tar.TypeReg,
			Mode:     0o755,
			Size:     int64(len(entry.body)),
		}
		if err := tw.WriteHeader(header); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(entry.body); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

func serveBootstrapRelease(t *testing.T, fixture bootstrapReleaseFixture, inspect func(*http.Request)) *httptest.Server {
	t.Helper()
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if inspect != nil {
			inspect(r)
		}
		assetName := bootstrapAssetName(bootstrapTestVersion, "amd64")
		switch r.URL.Path {
		case "/repos/Kome-Lab/Autostream-ControlPanel/releases/tags/" + bootstrapTestVersion:
			assets := []updateHostBootstrapGitHubAsset{
				{ID: 1, Name: assetName, URL: server.URL + "/repos/Kome-Lab/Autostream-ControlPanel/releases/assets/1", Digest: fixture.githubDigests[assetName], State: fixture.assetStates[assetName]},
				{ID: 2, Name: assetName + ".sha256", URL: server.URL + "/repos/Kome-Lab/Autostream-ControlPanel/releases/assets/2", Digest: fixture.githubDigests[assetName+".sha256"], State: fixture.assetStates[assetName+".sha256"]},
				{ID: 3, Name: updateHostBootstrapManifestName, URL: server.URL + "/repos/Kome-Lab/Autostream-ControlPanel/releases/assets/3", Digest: fixture.githubDigests[updateHostBootstrapManifestName], State: fixture.assetStates[updateHostBootstrapManifestName]},
				{ID: 4, Name: updateHostBootstrapManifestName + ".sha256", URL: server.URL + "/repos/Kome-Lab/Autostream-ControlPanel/releases/assets/4", Digest: fixture.githubDigests[updateHostBootstrapManifestName+".sha256"], State: fixture.assetStates[updateHostBootstrapManifestName+".sha256"]},
			}
			for i := range assets {
				if assets[i].Name == fixture.noncanonicalURL {
					assets[i].URL = server.URL + "/not-a-release-asset/" + fmt.Sprint(assets[i].ID)
				}
				if assets[i].Name == fixture.duplicateAsset {
					duplicate := assets[i]
					duplicate.ID = 5
					duplicate.URL = server.URL + "/repos/Kome-Lab/Autostream-ControlPanel/releases/assets/5"
					assets = append(assets, duplicate)
					break
				}
			}
			_ = json.NewEncoder(w).Encode(updateHostBootstrapGitHubRelease{
				TagName:   bootstrapTestVersion,
				Draft:     fixture.draft,
				Immutable: fixture.immutable,
				Assets:    assets,
			})
		case "/repos/Kome-Lab/Autostream-ControlPanel/git/ref/tags/" + bootstrapTestVersion:
			if len(fixture.tagChain) == 0 {
				fmt.Fprintf(w, `{"object":{"type":"commit","sha":%q}}`, fixture.tagCommit)
			} else {
				fmt.Fprintf(w, `{"object":{"type":"tag","sha":%q}}`, fixture.tagChain[0])
			}
		case "/repos/Kome-Lab/Autostream-ControlPanel/releases/assets/1":
			_, _ = w.Write(fixture.archive)
		case "/repos/Kome-Lab/Autostream-ControlPanel/releases/assets/2":
			_, _ = w.Write([]byte(fixture.archiveSidecar))
		case "/repos/Kome-Lab/Autostream-ControlPanel/releases/assets/3":
			_, _ = w.Write(fixture.manifest)
		case "/repos/Kome-Lab/Autostream-ControlPanel/releases/assets/4":
			_, _ = w.Write([]byte(fixture.manifestSidecar))
		default:
			tagPrefix := "/repos/Kome-Lab/Autostream-ControlPanel/git/tags/"
			if strings.HasPrefix(r.URL.Path, tagPrefix) {
				sha := strings.TrimPrefix(r.URL.Path, tagPrefix)
				for i, candidate := range fixture.tagChain {
					if candidate != sha {
						continue
					}
					switch {
					case i+1 < len(fixture.tagChain):
						fmt.Fprintf(w, `{"object":{"type":"tag","sha":%q}}`, fixture.tagChain[i+1])
					case fixture.tagCycle:
						fmt.Fprintf(w, `{"object":{"type":"tag","sha":%q}}`, fixture.tagChain[0])
					default:
						fmt.Fprintf(w, `{"object":{"type":"commit","sha":%q}}`, fixture.tagCommit)
					}
					return
				}
			}
			http.NotFound(w, r)
		}
	}))
	return server
}

func bootstrapAssetName(version, arch string) string {
	return "autostream-update-host_" + version + "_linux_" + arch + ".tar.gz"
}

func bootstrapArtifactRoot(version, arch string) string {
	return strings.TrimSuffix(bootstrapAssetName(version, arch), ".tar.gz")
}

func restoreControllerVersion(t *testing.T, current string) {
	t.Helper()
	oldVersion := controlversion.Version
	controlversion.Version = current
	t.Cleanup(func() {
		controlversion.Version = oldVersion
	})
}

func trustedBootstrapReleaseDownloader(server *httptest.Server) ReleaseDownloader {
	return ReleaseDownloader{
		APIBase:          server.URL,
		Client:           server.Client(),
		AllowHTTPForTest: true,
		bootstrapProvenanceVerifier: bootstrapProvenanceVerifierFunc(func(
			context.Context,
			ReleaseDownloader,
			string,
			string,
			string,
		) error {
			return nil
		}),
	}
}
