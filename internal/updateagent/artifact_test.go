package updateagent

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	controlversion "github.com/example/autostream-control-panel/internal/version"
)

func TestCanonicalVersionPattern(t *testing.T) {
	for _, version := range []string{"v0.0.0", "v1.2.3", "v1.2.3-rc.1", "v1.2.3-alpha-2"} {
		if !versionPattern.MatchString(version) {
			t.Fatalf("canonical version %q was rejected", version)
		}
	}
	for _, version := range []string{"1.2.3", "v1.2.3+build.1", "v1.2.3.rc1", "v1.2.3-", "v1.2.3-rc..1", " v1.2.3"} {
		if versionPattern.MatchString(version) {
			t.Fatalf("noncanonical version %q was accepted", version)
		}
	}
}

type tarEntry struct {
	name     string
	typeflag byte
	body     []byte
	mode     int64
}

func makeTarGz(t *testing.T, entries []tarEntry) []byte {
	t.Helper()
	var out bytes.Buffer
	gz := gzip.NewWriter(&out)
	tw := tar.NewWriter(gz)
	for _, entry := range entries {
		typeflag := entry.typeflag
		if typeflag == 0 {
			typeflag = tar.TypeReg
		}
		mode := entry.mode
		if mode == 0 {
			mode = 0o755
		}
		h := &tar.Header{Name: entry.name, Typeflag: typeflag, Mode: mode, Size: int64(len(entry.body))}
		if typeflag != tar.TypeReg {
			h.Size = 0
		}
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		if h.Size > 0 {
			_, _ = tw.Write(entry.body)
		}
	}
	_ = tw.Close()
	_ = gz.Close()
	return out.Bytes()
}

func TestExtractTarGzRejectsTraversalAndLinks(t *testing.T) {
	for name, entries := range map[string][]tarEntry{
		"traversal":         {{name: "root/../../escape", body: []byte("x")}},
		"symlink":           {{name: "root/link", typeflag: tar.TypeSymlink}},
		"device":            {{name: "root/device", typeflag: tar.TypeChar}},
		"setuid executable": {{name: "root/worker", body: []byte("x"), mode: 0o4755}},
		"setgid executable": {{name: "root/worker", body: []byte("x"), mode: 0o2755}},
		"sticky directory":  {{name: "root", typeflag: tar.TypeDir, mode: 0o1755}},
	} {
		t.Run(name, func(t *testing.T) {
			archive := filepath.Join(t.TempDir(), "bad.tar.gz")
			if err := os.WriteFile(archive, makeTarGz(t, entries), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := ExtractTarGz(archive, filepath.Join(t.TempDir(), "out"), 1<<20, 10); err == nil {
				t.Fatal("expected unsafe archive rejection")
			}
		})
	}
}

func TestVerifyInnerChecksumsAndUnlistedFile(t *testing.T) {
	root := t.TempDir()
	data := []byte("binary")
	if err := os.WriteFile(filepath.Join(root, "worker"), data, 0o755); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	manifest := fmt.Sprintf("%x  ./worker\n", sum)
	if err := os.WriteFile(filepath.Join(root, "checksums.txt"), []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := VerifyInnerChecksums(root); err != nil {
		t.Fatalf("valid manifest rejected: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "unlisted"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := VerifyInnerChecksums(root); err == nil || !strings.Contains(err.Error(), "not listed") {
		t.Fatalf("expected unlisted file rejection, got %v", err)
	}
}

func TestReleaseDownloaderVerifiesOuterAndInnerSHA256(t *testing.T) {
	binary := []byte("worker-v1.2.3")
	inner := sha256.Sum256(binary)
	manifest := []byte(fmt.Sprintf("%x  ./bin/worker\n", inner))
	archive := makeTarGz(t, []tarEntry{{name: "autostream-worker_v1.2.3_linux_amd64/bin/worker", body: binary}, {name: "autostream-worker_v1.2.3_linux_amd64/checksums.txt", body: manifest}})
	outer := sha256.Sum256(archive)
	hostManifest, err := json.Marshal(HostReleaseManifest{SchemaVersion: 1, ReleaseID: "v1.2.3", Channel: "host", PublishedAt: "2026-07-18T00:00:00Z", MinimumAgentVersion: "v1.0.0", Components: []HostReleaseComponent{{Service: "worker", SourceVersion: "v1.2.3", Commit: strings.Repeat("a", 40), RollbackCompatible: true, DatabaseSchema: "none", Artifacts: []HostReleaseArtifact{{OS: "linux", Arch: "amd64", Name: "autostream-worker_v1.2.3_linux_amd64.tar.gz", Size: int64(len(archive)), SHA256: hex.EncodeToString(outer[:])}, {OS: "linux", Arch: "arm64", Name: "autostream-worker_v1.2.3_linux_arm64.tar.gz", Size: 1, SHA256: strings.Repeat("b", 64)}}}}})
	if err != nil {
		t.Fatal(err)
	}
	hostManifestSum := sha256.Sum256(hostManifest)
	oldVersion := controlversion.Version
	controlversion.Version = "v1.0.0"
	defer func() { controlversion.Version = oldVersion }()
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/Kome-Lab/Autostream-Worker/releases/tags/v1.2.3":
			fmt.Fprintf(w, `{"assets":[{"name":"autostream-worker_v1.2.3_linux_amd64.tar.gz","url":%q},{"name":"autostream-worker_v1.2.3_linux_amd64.tar.gz.sha256","url":%q},{"name":"release-manifest.json","url":%q},{"name":"release-manifest.json.sha256","url":%q}]}`, server.URL+"/archive", server.URL+"/checksum", server.URL+"/manifest", server.URL+"/manifest-checksum")
		case "/archive":
			_, _ = w.Write(archive)
		case "/checksum":
			fmt.Fprintf(w, "%x  autostream-worker_v1.2.3_linux_amd64.tar.gz\n", outer)
		case "/manifest":
			_, _ = w.Write(hostManifest)
		case "/manifest-checksum":
			fmt.Fprintf(w, "%x  release-manifest.json\n", hostManifestSum)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	d := ReleaseDownloader{APIBase: server.URL, Client: server.Client(), AllowHTTPForTest: true}
	got, err := d.Download(context.Background(), "worker", "v1.2.3", "amd64", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if got.SHA256 != hex.EncodeToString(outer[:]) {
		t.Fatalf("digest = %s", got.SHA256)
	}
}

func TestReadSHA256FileRequiresCanonicalSingleLine(t *testing.T) {
	path := filepath.Join(t.TempDir(), "asset.sha256")
	valid := strings.Repeat("a", 64) + "  asset.tar.gz\n"
	if err := os.WriteFile(path, []byte(valid), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, err := readSHA256File(path, "asset.tar.gz"); err != nil || got != strings.Repeat("a", 64) {
		t.Fatalf("canonical sidecar rejected: %q %v", got, err)
	}
	for name, value := range map[string]string{
		"extra line": valid + valid,
		"upper":      strings.Repeat("A", 64) + "  asset.tar.gz\n",
		"path":       strings.Repeat("a", 64) + "  artifacts/asset.tar.gz\n",
		"field":      strings.Repeat("a", 64) + "  asset.tar.gz extra\n",
	} {
		t.Run(name, func(t *testing.T) {
			if err := os.WriteFile(path, []byte(value), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := readSHA256File(path, "asset.tar.gz"); err == nil {
				t.Fatal("expected noncanonical sidecar rejection")
			}
		})
	}
}

func TestHostManifestRejectsMinimumAgentAndRollbackPolicy(t *testing.T) {
	oldVersion := controlversion.Version
	controlversion.Version = "v1.2.0"
	defer func() { controlversion.Version = oldVersion }()
	base := HostReleaseManifest{SchemaVersion: 1, ReleaseID: "v1.2.3", Channel: "host", PublishedAt: "2026-07-18T00:00:00Z", MinimumAgentVersion: "v1.0.0", Components: []HostReleaseComponent{{Service: "worker", SourceVersion: "v1.2.3", Commit: strings.Repeat("a", 40), RollbackCompatible: true, DatabaseSchema: "none", Artifacts: []HostReleaseArtifact{{OS: "linux", Arch: "amd64", Name: "autostream-worker_v1.2.3_linux_amd64.tar.gz", Size: 10, SHA256: strings.Repeat("b", 64)}, {OS: "linux", Arch: "arm64", Name: "autostream-worker_v1.2.3_linux_arm64.tar.gz", Size: 10, SHA256: strings.Repeat("c", 64)}}}}}
	for name, mutate := range map[string]func(*HostReleaseManifest){
		"missing minimum": func(manifest *HostReleaseManifest) { manifest.MinimumAgentVersion = "" },
		"new minimum":     func(manifest *HostReleaseManifest) { manifest.MinimumAgentVersion = "v9.0.0" },
		"rollback false":  func(manifest *HostReleaseManifest) { manifest.Components[0].RollbackCompatible = false },
		"schema mismatch": func(manifest *HostReleaseManifest) { manifest.Components[0].DatabaseSchema = "backward_compatible" },
	} {
		t.Run(name, func(t *testing.T) {
			manifest := base
			manifest.Components = append([]HostReleaseComponent(nil), base.Components...)
			mutate(&manifest)
			payload, _ := json.Marshal(manifest)
			path := filepath.Join(t.TempDir(), "release-manifest.json")
			if err := os.WriteFile(path, payload, 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := validateHostReleaseManifest(path, "worker", repositoryByServiceType["worker"], "v1.2.3", "amd64", "autostream-worker_v1.2.3_linux_amd64.tar.gz"); err == nil {
				t.Fatal("expected unsafe host manifest rejection")
			}
		})
	}
}

func TestReleaseRedirectDoesNotLeakAuthorization(t *testing.T) {
	seenAuth := "unset"
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenAuth = r.Header.Get("Authorization")
		_, _ = w.Write([]byte("asset"))
	}))
	defer destination.Close()
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, destination.URL, http.StatusFound)
	}))
	defer source.Close()
	d := ReleaseDownloader{Token: "top-secret", AllowHTTPForTest: true}
	_, err := d.downloadFile(context.Background(), source.URL, filepath.Join(t.TempDir(), "asset"), 100)
	if err != nil {
		t.Fatal(err)
	}
	if seenAuth != "" {
		t.Fatalf("authorization leaked to redirect host: %q", seenAuth)
	}
}
