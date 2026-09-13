package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

func TestLatestVersionManifestGateUsesAPIAssetStripsRedirectAuthAndCaches(t *testing.T) {
	processLatestVersionCache.clear()
	defer processLatestVersionCache.clear()
	t.Setenv("AUTOSTREAM_UPDATE_CHECK_TOKEN", "private-release-token")
	t.Setenv("AUTOSTREAM_TEST_WORKER_LATEST", "")
	t.Setenv("AUTOSTREAM_TEST_WORKER_URL", "")

	var releaseCalls atomic.Int32
	var assetCalls atomic.Int32
	var browserCalls atomic.Int32
	var redirectSawAuthorization atomic.Bool
	manifestBody := testHostReleaseManifest("worker", "v1.1.0")
	manifestServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			redirectSawAuthorization.Store(true)
		}
		switch r.URL.Path {
		case "/manifest":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(manifestBody)
		case "/manifest.sha256":
			_, _ = w.Write(testReleaseManifestSidecar(manifestBody))
		default:
			http.NotFound(w, r)
		}
	}))
	defer manifestServer.Close()

	var releaseServer *httptest.Server
	releaseServer = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			t.Errorf("local update endpoint received private token: %q", r.Header.Get("Authorization"))
		}
		switch r.URL.Path {
		case "/release":
			releaseCalls.Add(1)
			writeTestGitHubRelease(w, releaseServer.URL, "v1.1.0", "/asset", "/browser")
		case "/asset":
			assetCalls.Add(1)
			if r.Header.Get("Accept") != "application/octet-stream" {
				http.Error(w, "missing asset accept header", http.StatusBadRequest)
				return
			}
			http.Redirect(w, r, manifestServer.URL+"/manifest", http.StatusFound)
		case "/asset.sha256":
			assetCalls.Add(1)
			if r.Header.Get("Accept") != "application/octet-stream" {
				http.Error(w, "missing sidecar accept header", http.StatusBadRequest)
				return
			}
			http.Redirect(w, r, manifestServer.URL+"/manifest.sha256", http.StatusFound)
		case "/browser":
			browserCalls.Add(1)
			http.Error(w, "browser URL must not be used", http.StatusTeapot)
		default:
			http.NotFound(w, r)
		}
	}))
	defer releaseServer.Close()

	target := versionUpdateTarget{serviceType: "worker", latestVersionEnv: "AUTOSTREAM_TEST_WORKER_LATEST", updateCheckURLEnv: "AUTOSTREAM_TEST_WORKER_URL", defaultURL: releaseServer.URL + "/release"}
	var wait sync.WaitGroup
	results := make(chan serviceUpdateInfoResponse, 8)
	for i := 0; i < 8; i++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			results <- latestVersions(t.Context(), []versionUpdateTarget{target})["worker"]
		}()
	}
	wait.Wait()
	close(results)
	for result := range results {
		if result.LatestVersion != "v1.1.0" || !result.ManifestVerified || result.ManifestErrorCode != "" || result.UpdateCheckError != "" {
			t.Fatalf("verified result = %#v", result)
		}
	}
	_ = latestVersions(t.Context(), []versionUpdateTarget{target})
	if releaseCalls.Load() != 1 || assetCalls.Load() != 2 || browserCalls.Load() != 0 || redirectSawAuthorization.Load() {
		t.Fatalf("upstream calls release=%d asset=%d browser=%d redirect_auth=%v", releaseCalls.Load(), assetCalls.Load(), browserCalls.Load(), redirectSawAuthorization.Load())
	}
}

func TestLatestVersionManifestMissingIsNegativeCachedAndTargetStillShowsLatest(t *testing.T) {
	processLatestVersionCache.clear()
	defer processLatestVersionCache.clear()
	t.Setenv("AUTOSTREAM_TEST_MISSING_LATEST", "")
	t.Setenv("AUTOSTREAM_TEST_MISSING_URL", "")
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		writeJSON(w, http.StatusOK, map[string]any{"tag_name": "v1.2.0", "assets": []any{}})
	}))
	defer server.Close()
	targetSpec := versionUpdateTarget{serviceType: "worker", latestVersionEnv: "AUTOSTREAM_TEST_MISSING_LATEST", updateCheckURLEnv: "AUTOSTREAM_TEST_MISSING_URL", defaultURL: server.URL}
	first := latestVersions(t.Context(), []versionUpdateTarget{targetSpec})["worker"]
	second := latestVersions(t.Context(), []versionUpdateTarget{targetSpec})["worker"]
	if calls.Load() != 1 || first.LatestVersion != "v1.2.0" || second.ManifestErrorCode != "release_manifest_missing" {
		t.Fatalf("negative cache calls=%d first=%#v second=%#v", calls.Load(), first, second)
	}
	target := buildSystemUpdateTarget("worker-01", "worker", "Worker", "v1.0.0", "", false, systemUpdateAgentAssignment{AgentID: "updater-01", DeploymentMode: "systemd", Available: true, HostReachability: "reachable"}, map[string]serviceUpdateInfoResponse{"worker": first})
	if !target.UpdateAvailable || target.Eligible || target.BlockedReason != "release_manifest_missing" || target.LatestVersion != "v1.2.0" {
		t.Fatalf("manifest-missing target = %#v", target)
	}
}

func TestLatestVersionCanceledWaiterDoesNotCancelSharedFetch(t *testing.T) {
	processLatestVersionCache.clear()
	defer processLatestVersionCache.clear()
	t.Setenv("AUTOSTREAM_TEST_CANCEL_LATEST", "")
	t.Setenv("AUTOSTREAM_TEST_CANCEL_URL", "")
	started := make(chan struct{}, 1)
	release := make(chan struct{})
	var calls atomic.Int32
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		manifestBody := testHostReleaseManifest("worker", "v1.1.0")
		switch r.URL.Path {
		case "/release":
			calls.Add(1)
			started <- struct{}{}
			<-release
			writeTestGitHubRelease(w, server.URL, "v1.1.0", "/manifest", "/manifest")
		case "/manifest":
			_, _ = w.Write(manifestBody)
		case "/manifest.sha256":
			_, _ = w.Write(testReleaseManifestSidecar(manifestBody))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	target := versionUpdateTarget{serviceType: "worker", latestVersionEnv: "AUTOSTREAM_TEST_CANCEL_LATEST", updateCheckURLEnv: "AUTOSTREAM_TEST_CANCEL_URL", defaultURL: server.URL + "/release"}

	ctx, cancel := context.WithCancel(context.Background())
	firstDone := make(chan serviceUpdateInfoResponse, 1)
	go func() { firstDone <- latestVersions(ctx, []versionUpdateTarget{target})["worker"] }()
	<-started
	cancel()
	first := <-firstDone
	if first.UpdateCheckError != "update check request canceled" {
		t.Fatalf("canceled waiter result = %#v", first)
	}
	secondDone := make(chan serviceUpdateInfoResponse, 1)
	go func() { secondDone <- latestVersions(context.Background(), []versionUpdateTarget{target})["worker"] }()
	close(release)
	second := <-secondDone
	if calls.Load() != 1 || !second.ManifestVerified || second.LatestVersion != "v1.1.0" {
		t.Fatalf("shared fetch calls=%d result=%#v", calls.Load(), second)
	}
}

func TestValidateDockerUpdateManifestRequiresPinnedFiveComponentRelease(t *testing.T) {
	digest := "sha256:" + strings.Repeat("a", 64)
	makeComponents := func() []map[string]any {
		components := make([]map[string]any, 0, 5)
		for _, name := range []string{"control-panel", "worker", "encoder-recorder", "discord-bot", "observability"} {
			databaseSchema := "none"
			if name == "control-panel" || name == "observability" {
				databaseSchema = "backward_compatible"
			}
			components = append(components, map[string]any{
				"service": name, "source_version": "v1.0.0", "image": "ghcr.io/kome-lab/autostream-docker/" + name + ":v2.0.0",
				"manifest_digest": digest, "platform_digests": map[string]string{"linux/amd64": digest, "linux/arm64": digest},
				"rollback_compatible": true, "database_schema": databaseSchema,
			})
		}
		return components
	}
	decode := func(t *testing.T, components []map[string]any) updateReleaseManifest {
		t.Helper()
		body, err := json.Marshal(map[string]any{"schema_version": 1, "release_id": "v2.0.0", "channel": "docker", "published_at": "2026-07-18T00:00:00Z", "minimum_agent_version": "v1.0.0", "bundle_version": "v2.0.0", "generated_at": "2026-07-18T00:00:00Z", "components": components})
		if err != nil {
			t.Fatal(err)
		}
		var manifest updateReleaseManifest
		if err := json.Unmarshal(body, &manifest); err != nil {
			t.Fatal(err)
		}
		return manifest
	}
	assets := map[string]updateReleaseAsset{"release-manifest.json.sha256": {Name: "release-manifest.json.sha256"}}
	manifest := decode(t, makeComponents())
	if err := validateDockerUpdateManifest(manifest, assets); err != nil {
		t.Fatalf("valid Docker manifest rejected: %v", err)
	}
	invalidPolicies := []struct {
		name   string
		mutate func([]map[string]any)
	}{
		{name: "missing rollback_compatible", mutate: func(components []map[string]any) { delete(components[0], "rollback_compatible") }},
		{name: "rollback_compatible false", mutate: func(components []map[string]any) { components[0]["rollback_compatible"] = false }},
		{name: "missing database_schema", mutate: func(components []map[string]any) { delete(components[0], "database_schema") }},
		{name: "wrong database_schema", mutate: func(components []map[string]any) { components[0]["database_schema"] = "none" }},
	}
	for _, test := range invalidPolicies {
		t.Run(test.name, func(t *testing.T) {
			components := makeComponents()
			test.mutate(components)
			if err := validateDockerUpdateManifest(decode(t, components), assets); err == nil {
				t.Fatal("unsafe Docker rollback policy was accepted")
			}
		})
	}
	delete(assets, "release-manifest.json.sha256")
	if err := validateDockerUpdateManifest(manifest, assets); err == nil {
		t.Fatal("Docker manifest without checksum asset was accepted")
	}
}

func TestValidateHostUpdateManifestMatchesUpdaterStrictContract(t *testing.T) {
	decode := func(t *testing.T) updateReleaseManifest {
		t.Helper()
		var manifest updateReleaseManifest
		if err := json.Unmarshal(testHostReleaseManifest("worker", "v1.1.0"), &manifest); err != nil {
			t.Fatal(err)
		}
		return manifest
	}
	prefix := "autostream-worker_v1.1.0_linux_"
	assets := map[string]updateReleaseAsset{
		"release-manifest.json.sha256": {Name: "release-manifest.json.sha256"},
		prefix + "amd64.tar.gz":        {Name: prefix + "amd64.tar.gz"},
		prefix + "amd64.tar.gz.sha256": {Name: prefix + "amd64.tar.gz.sha256"},
		prefix + "arm64.tar.gz":        {Name: prefix + "arm64.tar.gz"},
		prefix + "arm64.tar.gz.sha256": {Name: prefix + "arm64.tar.gz.sha256"},
	}
	if err := validateHostUpdateManifest(decode(t), assets, "v1.1.0", "worker"); err != nil {
		t.Fatalf("workflow-shaped host manifest rejected: %v", err)
	}
	for name, mutate := range map[string]func(*updateReleaseManifest, map[string]updateReleaseAsset){
		"missing manifest sidecar": func(_ *updateReleaseManifest, cloned map[string]updateReleaseAsset) {
			delete(cloned, "release-manifest.json.sha256")
		},
		"missing commit": func(manifest *updateReleaseManifest, _ map[string]updateReleaseAsset) {
			manifest.Components[0].Commit = ""
		},
		"oversized artifact": func(manifest *updateReleaseManifest, _ map[string]updateReleaseAsset) {
			manifest.Components[0].Artifacts[0].Size = maxHostUpdateArtifactBytes + 1
		},
		"extra component": func(manifest *updateReleaseManifest, _ map[string]updateReleaseAsset) {
			manifest.Components = append(manifest.Components, manifest.Components[0])
		},
		"alternate service_type": func(manifest *updateReleaseManifest, _ map[string]updateReleaseAsset) {
			manifest.Components[0].Service = ""
			manifest.Components[0].ServiceType = "worker"
		},
	} {
		t.Run(name, func(t *testing.T) {
			manifest := decode(t)
			clonedAssets := make(map[string]updateReleaseAsset, len(assets))
			for key, value := range assets {
				clonedAssets[key] = value
			}
			mutate(&manifest, clonedAssets)
			if err := validateHostUpdateManifest(manifest, clonedAssets, "v1.1.0", "worker"); err == nil {
				t.Fatal("invalid host manifest was accepted")
			}
		})
	}
}

func TestReleaseManifestSidecarRequiresExactMatchingDigest(t *testing.T) {
	body := testHostReleaseManifest("worker", "v1.1.0")
	if !releaseManifestSidecarMatches(body, testReleaseManifestSidecar(body)) {
		t.Fatal("matching release manifest sidecar was rejected")
	}
	if releaseManifestSidecarMatches(append([]byte(nil), body...), []byte(strings.Repeat("0", 64)+"  release-manifest.json\n")) {
		t.Fatal("mismatched release manifest sidecar was accepted")
	}
	if releaseManifestSidecarMatches(body, []byte(strings.Repeat("0", 64)+" release-manifest.json\n")) {
		t.Fatal("non-canonical release manifest sidecar was accepted")
	}
}
