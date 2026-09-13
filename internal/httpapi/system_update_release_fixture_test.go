package httpapi

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func newVerifiedWorkerReleaseServer(t *testing.T) *httptest.Server {
	t.Helper()
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		manifestBody := testHostReleaseManifest("worker", "v1.1.0")
		switch r.URL.Path {
		case "/release":
			writeTestGitHubRelease(w, server.URL, "v1.1.0", "/manifest", "/manifest")
		case "/manifest":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(manifestBody)
		case "/manifest.sha256":
			_, _ = w.Write(testReleaseManifestSidecar(manifestBody))
		default:
			http.NotFound(w, r)
		}
	}))
	return server
}

func writeTestGitHubRelease(w http.ResponseWriter, baseURL, version, apiAssetPath, browserAssetPath string) {
	prefix := "autostream-worker_" + version + "_linux_"
	assets := []map[string]any{
		{"name": "release-manifest.json", "url": baseURL + apiAssetPath, "browser_download_url": baseURL + browserAssetPath},
		{"name": "release-manifest.json.sha256", "url": baseURL + apiAssetPath + ".sha256", "browser_download_url": baseURL + browserAssetPath + ".sha256"},
		{"name": prefix + "amd64.tar.gz"}, {"name": prefix + "amd64.tar.gz.sha256"},
		{"name": prefix + "arm64.tar.gz"}, {"name": prefix + "arm64.tar.gz.sha256"},
	}
	writeJSON(w, http.StatusOK, map[string]any{"tag_name": version, "assets": assets})
}

func testHostReleaseManifest(service, version string) []byte {
	databaseSchema := "none"
	if service == "control-panel" || service == "observability" {
		databaseSchema = "backward_compatible"
	}
	prefix := "autostream-" + service + "_" + version + "_linux_"
	body, _ := json.Marshal(map[string]any{
		"schema_version": 1, "release_id": version, "channel": "host", "published_at": "2026-07-18T00:00:00Z", "minimum_agent_version": "v1.0.0",
		"components": []map[string]any{{
			"service": service, "source_version": version, "commit": strings.Repeat("c", 40), "rollback_compatible": true, "database_schema": databaseSchema,
			"artifacts": []map[string]any{
				{"os": "linux", "arch": "amd64", "name": prefix + "amd64.tar.gz", "size": 123, "sha256": strings.Repeat("a", 64)},
				{"os": "linux", "arch": "arm64", "name": prefix + "arm64.tar.gz", "size": 456, "sha256": strings.Repeat("b", 64)},
			},
		}},
	})
	return body
}

func testReleaseManifestSidecar(body []byte) []byte {
	digest := sha256.Sum256(body)
	return []byte(fmt.Sprintf("%x  release-manifest.json\n", digest))
}
