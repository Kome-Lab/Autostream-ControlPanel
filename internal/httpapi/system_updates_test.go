package httpapi

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/example/autostream-control-panel/internal/servicecall"
	"github.com/example/autostream-control-panel/internal/store"
)

func TestSystemUpdateAdminAndAgentLifecycle(t *testing.T) {
	processLatestVersionCache.clear()
	workerRelease := newVerifiedWorkerReleaseServer(t)
	defer workerRelease.Close()
	originalTargets := append([]versionUpdateTarget(nil), nodeVersionUpdateTargets...)
	defer func() {
		nodeVersionUpdateTargets = originalTargets
		processLatestVersionCache.clear()
	}()
	for i := range nodeVersionUpdateTargets {
		if nodeVersionUpdateTargets[i].serviceType == "worker" {
			nodeVersionUpdateTargets[i].defaultURL = workerRelease.URL + "/release"
		}
	}
	for key, value := range map[string]string{
		"AUTOSTREAM_LATEST_VERSION":                  "v9.0.0",
		"AUTOSTREAM_ENCODER_RECORDER_LATEST_VERSION": "v1.1.0", "AUTOSTREAM_DISCORD_BOT_LATEST_VERSION": "v1.1.0",
		"AUTOSTREAM_OBSERVABILITY_LATEST_VERSION": "v1.1.0", "AUTOSTREAM_DOCKER_LATEST_VERSION": "v2.0.0",
	} {
		t.Setenv(key, value)
	}
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{ID: "update-admin", Username: "update-admin", Roles: []string{"super_admin"}}, "correct horse battery", []string{"system_updates.read", "system_updates.execute"}); err != nil {
		t.Fatal(err)
	}
	workerToken, err := auth.CreateServiceToken(t.Context(), "worker", []string{"service.register", "service.heartbeat"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := auth.PrecreateService(t.Context(), workerToken, store.ServiceRegistration{ServiceID: "worker-01", ServiceType: "worker", ServiceName: "Worker 01", PublicURL: "https://worker.example.com", Version: "v1.0.0", Capabilities: map[string]any{}}); err != nil {
		t.Fatal(err)
	}
	if _, err := auth.RegisterService(t.Context(), workerToken, store.ServiceRegistration{ServiceID: "worker-01", ServiceType: "worker", ServiceName: "Worker 01", PublicURL: "https://worker.example.com", Version: "v1.0.0", Capabilities: map[string]any{}}); err != nil {
		t.Fatal(err)
	}
	if _, err := auth.Heartbeat(t.Context(), workerToken, store.ServiceHeartbeat{ServiceID: "worker-01", Status: "online", Version: "v1.0.0"}); err != nil {
		t.Fatal(err)
	}
	agentToken, err := auth.CreateServiceToken(t.Context(), "update_agent", []string{"service.register", "service.heartbeat", "updates.claim", "updates.report"})
	if err != nil {
		t.Fatal(err)
	}
	capabilities := centralUpdateCapabilitiesForTest("host-01", map[string]string{"worker-01": "systemd"})
	if _, err := auth.PrecreateService(t.Context(), agentToken, store.ServiceRegistration{ServiceID: "updater-01", ServiceType: "update_agent", ServiceName: "Updater 01", PublicURL: "https://updater.example.com", Version: "v1.0.0", Capabilities: capabilities}); err != nil {
		t.Fatal(err)
	}
	if _, err := auth.RegisterService(t.Context(), agentToken, store.ServiceRegistration{ServiceID: "updater-01", ServiceType: "update_agent", ServiceName: "Updater 01", PublicURL: "https://updater.example.com", Version: "v1.0.0", Capabilities: capabilities}); err != nil {
		t.Fatal(err)
	}
	if _, err := auth.Heartbeat(t.Context(), agentToken, store.ServiceHeartbeat{ServiceID: "updater-01", Status: "online", Version: "v1.0.0", Capabilities: capabilities}); err != nil {
		t.Fatal(err)
	}
	handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth), WithSystemUpdateStore(store.NewMemorySystemUpdateStore()))
	cookie, csrf := loginForTest(t, handler, "update-admin", "correct horse battery")

	listRequest := httptest.NewRequest(http.MethodGet, "/system-updates", nil)
	listRequest.AddCookie(cookie)
	listResponse := httptest.NewRecorder()
	handler.ServeHTTP(listResponse, listRequest)
	if listResponse.Code != http.StatusOK || !strings.Contains(listResponse.Body.String(), `"target_id":"worker-01"`) || !strings.Contains(listResponse.Body.String(), `"updater_online":true`) || !strings.Contains(listResponse.Body.String(), `"target_type":"worker"`) {
		t.Fatalf("list response = %d %s", listResponse.Code, listResponse.Body.String())
	}

	createBody := []byte(`{"target_id":"worker-01","strategy":"maintenance","idempotency_key":"ui-request-01"}`)
	withoutCSRF := httptest.NewRequest(http.MethodPost, "/system-updates", bytes.NewReader(createBody))
	withoutCSRF.AddCookie(cookie)
	withoutCSRFResponse := httptest.NewRecorder()
	handler.ServeHTTP(withoutCSRFResponse, withoutCSRF)
	if withoutCSRFResponse.Code != http.StatusForbidden || !strings.Contains(withoutCSRFResponse.Body.String(), "csrf_failed") {
		t.Fatalf("create without CSRF = %d %s", withoutCSRFResponse.Code, withoutCSRFResponse.Body.String())
	}

	createRequest := httptest.NewRequest(http.MethodPost, "/system-updates", bytes.NewReader(createBody))
	createRequest.AddCookie(cookie)
	createRequest.Header.Set("X-CSRF-Token", csrf)
	createResponse := httptest.NewRecorder()
	handler.ServeHTTP(createResponse, createRequest)
	if createResponse.Code != http.StatusAccepted {
		t.Fatalf("create response = %d %s", createResponse.Code, createResponse.Body.String())
	}
	createPayload := createResponse.Body.Bytes()
	if strings.Contains(string(createPayload), "requested_by_user_id") || strings.Contains(string(createPayload), "agent_service_id") || !strings.Contains(string(createPayload), `"updater_id":"updater-01"`) || !strings.Contains(string(createPayload), `"requested_by":"update-admin"`) {
		t.Fatalf("public job shape leaked internal identity or omitted public fields: %s", createPayload)
	}
	var job store.SystemUpdateJob
	if err := json.Unmarshal(createPayload, &job); err != nil {
		t.Fatal(err)
	}
	if job.TargetID != "worker-01" || job.TargetServiceType != "worker" || job.TargetVersion != "v1.1.0" || job.Status != store.SystemUpdateStatusQueued {
		t.Fatalf("created job = %#v", job)
	}
	if _, err := auth.Heartbeat(t.Context(), agentToken, store.ServiceHeartbeat{ServiceID: "updater-01", Status: "offline", Version: "v0.9.0", Capabilities: capabilities}); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AUTOSTREAM_WORKER_LATEST_VERSION", "v9.9.9")
	replayRequest := httptest.NewRequest(http.MethodPost, "/system-updates", bytes.NewReader(createBody))
	replayRequest.AddCookie(cookie)
	replayRequest.Header.Set("X-CSRF-Token", csrf)
	replayResponse := httptest.NewRecorder()
	handler.ServeHTTP(replayResponse, replayRequest)
	var replayed store.SystemUpdateJob
	if replayResponse.Code != http.StatusAccepted || json.Unmarshal(replayResponse.Body.Bytes(), &replayed) != nil || replayed.ID != job.ID || replayed.TargetVersion != "v1.1.0" {
		t.Fatalf("idempotent response-loss replay after environment drift = %d %s", replayResponse.Code, replayResponse.Body.String())
	}
	conflictRequest := httptest.NewRequest(http.MethodPost, "/system-updates", strings.NewReader(`{"target_id":"control-panel","strategy":"maintenance","idempotency_key":"ui-request-01"}`))
	conflictRequest.AddCookie(cookie)
	conflictRequest.Header.Set("X-CSRF-Token", csrf)
	conflictResponse := httptest.NewRecorder()
	handler.ServeHTTP(conflictResponse, conflictRequest)
	if conflictResponse.Code != http.StatusConflict || !strings.Contains(conflictResponse.Body.String(), "idempotency_key_conflict") {
		t.Fatalf("idempotency client-field conflict = %d %s", conflictResponse.Code, conflictResponse.Body.String())
	}
	t.Setenv("AUTOSTREAM_WORKER_LATEST_VERSION", "")
	if _, err := auth.Heartbeat(t.Context(), agentToken, store.ServiceHeartbeat{ServiceID: "updater-01", Status: "online", Version: "v1.0.0", Capabilities: capabilities}); err != nil {
		t.Fatal(err)
	}

	claimRequest := httptest.NewRequest(http.MethodPost, "/services/update-jobs/claim", strings.NewReader(`{"service_id":"updater-01","host_id":"host-01"}`))
	claimRequest.Header.Set("Authorization", "Bearer "+agentToken.RawToken)
	claimResponse := httptest.NewRecorder()
	handler.ServeHTTP(claimResponse, claimRequest)
	if claimResponse.Code != http.StatusOK {
		t.Fatalf("claim response = %d %s", claimResponse.Code, claimResponse.Body.String())
	}
	var claim store.SystemUpdateClaim
	if err := json.NewDecoder(claimResponse.Body).Decode(&claim); err != nil {
		t.Fatal(err)
	}
	if claim.Job.ID != job.ID || claim.LeaseToken == "" || claim.LeaseExpiresAt.IsZero() {
		t.Fatalf("claim = %#v", claim)
	}

	if claim.ReportSequence != 1 || claim.LeaseGeneration != 1 || claim.RecoveryRequired {
		t.Fatalf("claim recovery contract = %#v", claim)
	}
	reportBody, err := json.Marshal(map[string]any{"service_id": "updater-01", "lease_token": claim.LeaseToken, "lease_generation": claim.LeaseGeneration, "sequence": claim.ReportSequence, "status": "succeeded", "progress": 100, "message": "update complete"})
	if err != nil {
		t.Fatal(err)
	}
	reportRequest := httptest.NewRequest(http.MethodPost, "/services/update-jobs/"+job.ID+"/report", bytes.NewReader(reportBody))
	reportRequest.Header.Set("Authorization", "Bearer "+agentToken.RawToken)
	reportResponse := httptest.NewRecorder()
	handler.ServeHTTP(reportResponse, reportRequest)
	if reportResponse.Code != http.StatusOK || strings.Contains(reportResponse.Body.String(), `"job":`) {
		t.Fatalf("report response = %d %s", reportResponse.Code, reportResponse.Body.String())
	}
	var completed store.SystemUpdateJob
	if err := json.NewDecoder(reportResponse.Body).Decode(&completed); err != nil {
		t.Fatal(err)
	}
	if completed.Status != store.SystemUpdateStatusSucceeded || completed.CompletedAt == nil {
		t.Fatalf("completed job = %#v", completed)
	}
	retryReportRequest := httptest.NewRequest(http.MethodPost, "/services/update-jobs/"+job.ID+"/report", bytes.NewReader(reportBody))
	retryReportRequest.Header.Set("Authorization", "Bearer "+agentToken.RawToken)
	retryReportResponse := httptest.NewRecorder()
	handler.ServeHTTP(retryReportResponse, retryReportRequest)
	var replayedCompleted store.SystemUpdateJob
	if retryReportResponse.Code != http.StatusOK || json.Unmarshal(retryReportResponse.Body.Bytes(), &replayedCompleted) != nil || replayedCompleted.ID != completed.ID || !replayedCompleted.UpdatedAt.Equal(completed.UpdatedAt) {
		t.Fatalf("terminal HTTP response-loss replay = %d %s", retryReportResponse.Code, retryReportResponse.Body.String())
	}
	secondCreateRequest := httptest.NewRequest(http.MethodPost, "/system-updates", strings.NewReader(`{"target_id":"worker-01","strategy":"maintenance","idempotency_key":"ui-request-02"}`))
	secondCreateRequest.AddCookie(cookie)
	secondCreateRequest.Header.Set("X-CSRF-Token", csrf)
	secondCreateResponse := httptest.NewRecorder()
	handler.ServeHTTP(secondCreateResponse, secondCreateRequest)
	if secondCreateResponse.Code != http.StatusAccepted {
		t.Fatalf("second create response = %d %s", secondCreateResponse.Code, secondCreateResponse.Body.String())
	}
	var cancelJob store.SystemUpdateJob
	if err := json.NewDecoder(secondCreateResponse.Body).Decode(&cancelJob); err != nil {
		t.Fatal(err)
	}
	clearActiveRequest := httptest.NewRequest(http.MethodPost, "/services/update-jobs/claim", strings.NewReader(`{"service_id":"updater-01","active_job_id":"`+job.ID+`"}`))
	clearActiveRequest.Header.Set("Authorization", "Bearer "+agentToken.RawToken)
	clearActiveResponse := httptest.NewRecorder()
	handler.ServeHTTP(clearActiveResponse, clearActiveRequest)
	if clearActiveResponse.Code != http.StatusOK || strings.TrimSpace(clearActiveResponse.Body.String()) != `{"clear_active_job_id":true}` {
		t.Fatalf("terminal active_job_id clear response = %d %s", clearActiveResponse.Code, clearActiveResponse.Body.String())
	}
	queuedAfterClear, err := handler.systemUpdates.GetActiveSystemUpdateJob(t.Context(), "worker-01")
	if err != nil || queuedAfterClear.ID != cancelJob.ID || queuedAfterClear.Status != store.SystemUpdateStatusQueued {
		t.Fatalf("active_job_id clear poisoned queued job: %#v err=%v", queuedAfterClear, err)
	}
	cancelRequest := httptest.NewRequest(http.MethodPost, "/system-updates/"+cancelJob.ID+"/cancel", nil)
	cancelRequest.AddCookie(cookie)
	cancelRequest.Header.Set("X-CSRF-Token", csrf)
	cancelResponse := httptest.NewRecorder()
	handler.ServeHTTP(cancelResponse, cancelRequest)
	if cancelResponse.Code != http.StatusOK || strings.Contains(cancelResponse.Body.String(), `"job":`) {
		t.Fatalf("cancel response = %d %s", cancelResponse.Code, cancelResponse.Body.String())
	}
	var canceled store.SystemUpdateJob
	if err := json.NewDecoder(cancelResponse.Body).Decode(&canceled); err != nil {
		t.Fatal(err)
	}
	if canceled.Status != "canceled" {
		t.Fatalf("canceled job = %#v", canceled)
	}
	events := auth.AuditEvents()
	if !hasAuditAction(events, "system_updates.create") || !hasAuditAction(events, "system_updates.succeeded") || !hasAuditAction(events, "system_updates.cancel") {
		t.Fatalf("system update audit actions missing: %#v", events)
	}
	createAudits, terminalAudits := 0, 0
	for _, event := range events {
		if event.Action == "system_updates.create" && event.ResourceID == job.ID {
			createAudits++
		}
		if event.Action == "system_updates.succeeded" && event.ResourceID == job.ID {
			terminalAudits++
		}
	}
	if createAudits != 1 || terminalAudits != 1 {
		t.Fatalf("idempotent replay duplicated audit side effects: create=%d terminal=%d events=%#v", createAudits, terminalAudits, events)
	}
}

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

func TestUpdateAgentAssignmentEndpointIsRejected(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "admin"}, "correct horse battery", []string{"services.assign"}); err != nil {
		t.Fatal(err)
	}
	streams := store.NewMemoryStreamStore()
	stream, err := streams.CreateStream(t.Context(), "assignment guard")
	if err != nil {
		t.Fatal(err)
	}
	token, err := auth.CreateServiceToken(t.Context(), "update_agent", []string{"service.register"})
	if err != nil {
		t.Fatal(err)
	}
	registerServiceWithTokenForTest(t, auth, token, store.ServiceRegistration{ServiceID: "updater-01", ServiceType: "update_agent", ServiceName: "Updater", PublicURL: "https://updater.example.com", Version: "v1.0.0"})
	handler := NewServer(streams, WithAuthStore(auth), WithAuditStore(auth), WithServiceRegistryStore(auth))
	cookie, csrf := loginForTest(t, handler, "admin", "correct horse battery")
	req := httptest.NewRequest(http.MethodPost, "/services/updater-01/assign", bytes.NewBufferString(`{"stream_id":"`+stream.ID+`"}`))
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrf)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusConflict || !strings.Contains(res.Body.String(), "service_assignment_unsupported") {
		t.Fatalf("update_agent assignment status = %d body = %s", res.Code, res.Body.String())
	}
	assignments, err := auth.ListStreamAssignments(t.Context(), stream.ID)
	if err != nil || len(assignments) != 0 {
		t.Fatalf("update_agent assignment mutated store: %#v, %v", assignments, err)
	}
}

func TestUpdateAgentOnboardingRequiresManualJSONConfiguration(t *testing.T) {
	t.Setenv("AUTOSTREAM_SECRET_ENCRYPTION_KEY", "test-secret-encryption-key-32-bytes")
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "admin", Roles: []string{"super_admin"}}, "correct horse battery", []string{"api_tokens.create", "api_tokens.revoke", "service_health.read", "system_updates.execute"}); err != nil {
		t.Fatal(err)
	}
	handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth), WithAuditStore(auth), WithServiceRegistryStore(auth))
	cookie, csrf := loginForTest(t, handler, "admin", "correct horse battery")

	create := httptest.NewRequest(http.MethodPost, "/nodes/registration-tokens", strings.NewReader(`{"node_type":"update_agent","node_id":"updater-01","name":"Updater 01","host":"updater.example.com","port":8090,"ssl_enabled":true}`))
	create.AddCookie(cookie)
	create.Header.Set("X-CSRF-Token", csrf)
	created := httptest.NewRecorder()
	handler.ServeHTTP(created, create)
	if created.Code != http.StatusCreated {
		t.Fatalf("create updater status = %d body = %s", created.Code, created.Body.String())
	}
	assertManual := func(t *testing.T, body string, requireRuntimeToken bool) {
		t.Helper()
		for _, want := range []string{`"manual_configuration_required":true`, `"configuration_path":"/etc/autostream/updater.json"`, `"configuration_example":"release/autostream-updater.json.example"`, `"configure_command":""`} {
			if !strings.Contains(body, want) {
				t.Fatalf("manual updater configuration response missing %s: %s", want, body)
			}
		}
		for _, forbidden := range []string{"autostream-updater configure", "/etc/autostream-update-agent/config.yml", `"configuration_yaml"`, `"configure_token"`} {
			if strings.Contains(body, forbidden) {
				t.Fatalf("updater response contains invalid auto-configuration %q: %s", forbidden, body)
			}
		}
		if requireRuntimeToken && !strings.Contains(body, `"runtime_token":"ast_svc_`) {
			t.Fatalf("manual updater response omitted one-time runtime token: %s", body)
		}
	}
	assertManual(t, created.Body.String(), true)
	if !strings.Contains(created.Body.String(), `"updates.authorize"`) {
		t.Fatalf("updater registration omitted authorize scope: %s", created.Body.String())
	}

	configuration := httptest.NewRequest(http.MethodGet, "/nodes/updater-01/configuration", nil)
	configuration.AddCookie(cookie)
	configurationResult := httptest.NewRecorder()
	handler.ServeHTTP(configurationResult, configuration)
	if configurationResult.Code != http.StatusOK {
		t.Fatalf("get updater configuration status = %d body = %s", configurationResult.Code, configurationResult.Body.String())
	}
	assertManual(t, configurationResult.Body.String(), false)

	regenerate := httptest.NewRequest(http.MethodPost, "/nodes/updater-01/configure-token", nil)
	regenerate.AddCookie(cookie)
	regenerate.Header.Set("X-CSRF-Token", csrf)
	regenerateResult := httptest.NewRecorder()
	handler.ServeHTTP(regenerateResult, regenerate)
	if regenerateResult.Code != http.StatusConflict || !strings.Contains(regenerateResult.Body.String(), "manual_configuration_required") {
		t.Fatalf("updater configure-token status = %d body = %s", regenerateResult.Code, regenerateResult.Body.String())
	}

	agentConfigure := httptest.NewRequest(http.MethodPost, "/api/node-agent/configure", strings.NewReader(`{"nodeId":"updater-01","configureToken":"must-not-work"}`))
	agentConfigureResult := httptest.NewRecorder()
	handler.ServeHTTP(agentConfigureResult, agentConfigure)
	if agentConfigureResult.Code != http.StatusConflict || !strings.Contains(agentConfigureResult.Body.String(), "manual_configuration_required") {
		t.Fatalf("updater agent configure status = %d body = %s", agentConfigureResult.Code, agentConfigureResult.Body.String())
	}

	rotate := httptest.NewRequest(http.MethodPost, "/nodes/updater-01/rotate-token", nil)
	rotate.AddCookie(cookie)
	rotate.Header.Set("X-CSRF-Token", csrf)
	rotateResult := httptest.NewRecorder()
	handler.ServeHTTP(rotateResult, rotate)
	if rotateResult.Code != http.StatusCreated {
		t.Fatalf("rotate updater runtime token status = %d body = %s", rotateResult.Code, rotateResult.Body.String())
	}
	assertManual(t, rotateResult.Body.String(), true)
}

func TestServiceDeletionIsFencedByActiveTargetOrUpdaterJob(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "admin"}, "correct horse battery", []string{"services.disable"}); err != nil {
		t.Fatal(err)
	}
	registerServiceInstance(t, auth, "worker-delete", "worker")
	updaterToken, err := auth.CreateServiceToken(t.Context(), "update_agent", []string{"service.register"})
	if err != nil {
		t.Fatal(err)
	}
	registerServiceWithTokenForTest(t, auth, updaterToken, store.ServiceRegistration{ServiceID: "updater-delete", ServiceType: "update_agent", ServiceName: "Updater", PublicURL: "https://updater.example.com"})
	updates := store.NewMemorySystemUpdateStore()
	job, _, err := updates.CreateSystemUpdateJob(t.Context(), store.CreateSystemUpdateJobParams{
		TargetID: "worker-delete", TargetServiceType: "worker", AgentServiceID: "updater-delete", DeploymentMode: "systemd",
		CurrentVersion: "v1.0.0", TargetVersion: "v1.1.0", Strategy: store.SystemUpdateStrategyWhenIdle, IdempotencyKey: "delete-fence", RequestedByUserID: "admin",
	})
	if err != nil {
		t.Fatal(err)
	}
	handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth), WithAuditStore(auth), WithServiceRegistryStore(auth), WithSystemUpdateStore(updates))
	cookie, csrf := loginForTest(t, handler, "admin", "correct horse battery")
	deleteService := func(serviceID string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodDelete, "/services/"+serviceID, nil)
		req.AddCookie(cookie)
		req.Header.Set("X-CSRF-Token", csrf)
		res := httptest.NewRecorder()
		handler.ServeHTTP(res, req)
		return res
	}
	for _, serviceID := range []string{"worker-delete", "updater-delete"} {
		res := deleteService(serviceID)
		if res.Code != http.StatusConflict || !strings.Contains(res.Body.String(), "system_update_active") {
			t.Fatalf("active delete %s status = %d body = %s", serviceID, res.Code, res.Body.String())
		}
		if _, err := auth.GetService(t.Context(), serviceID); err != nil {
			t.Fatalf("active delete removed %s: %v", serviceID, err)
		}
	}
	now := time.Now().UTC()
	claim, _, err := updates.ClaimSystemUpdateJob(t.Context(), "updater-delete", "", "", map[string]string{"worker-delete": "systemd"}, now, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := updates.ReportSystemUpdateJob(t.Context(), job.ID, store.SystemUpdateReport{AgentServiceID: "updater-delete", LeaseToken: claim.LeaseToken, LeaseGeneration: claim.LeaseGeneration, Sequence: 1, Status: store.SystemUpdateStatusSucceeded, Progress: 100}, now.Add(time.Second), time.Minute); err != nil {
		t.Fatal(err)
	}
	for _, serviceID := range []string{"worker-delete", "updater-delete"} {
		res := deleteService(serviceID)
		if res.Code != http.StatusOK {
			t.Fatalf("terminal delete %s status = %d body = %s", serviceID, res.Code, res.Body.String())
		}
	}
}

func TestTerminalUpdaterServiceAuditReachesNotificationPipelineOnly(t *testing.T) {
	received := make(chan map[string]any, 2)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"code": "bad_request"})
			return
		}
		received <- payload
		writeJSON(w, http.StatusAccepted, []map[string]string{{"status": "success"}})
	}))
	defer upstream.Close()
	auth := store.NewMemoryAuthStore()
	token := registerObservabilityNodeForTest(t, auth, "terminal-update-notification-token", upstream.URL)
	if _, err := auth.Heartbeat(t.Context(), token, store.ServiceHeartbeat{ServiceID: "observability-01", Status: "online"}); err != nil {
		t.Fatal(err)
	}
	server := NewServer(store.NewMemoryStreamStore(), WithAuditStore(auth), WithServiceRegistryStore(auth))
	server.writeSystemAudit(t.Context(), store.AuditEvent{Action: "system_updates.succeeded", ActorUserID: "service:updater-01", ActorUsername: "updater-01", ResourceType: "system_update", ResourceID: "job-01", Result: "success"})
	select {
	case payload := <-received:
		if payload["action"] != "system_updates.succeeded" || payload["event_type"] != "admin.audit" {
			t.Fatalf("terminal updater notification payload = %#v", payload)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("terminal updater audit did not reach notification pipeline")
	}
	server.writeSystemAudit(t.Context(), store.AuditEvent{Action: "system_updates.authorize", ActorUserID: "service:updater-01", ActorUsername: "updater-01", ResourceType: "system_update", ResourceID: "job-01", Result: "success"})
	select {
	case payload := <-received:
		t.Fatalf("nonterminal updater audit reached notification pipeline: %#v", payload)
	case <-time.After(150 * time.Millisecond):
	}
}

func TestSystemUpdateMutationAuthorizationEndpointFencesEveryFieldAndAudits(t *testing.T) {
	type fixture struct {
		handler http.Handler
		auth    *store.MemoryAuthStore
		token   store.ServiceToken
		job     store.SystemUpdateJob
		claim   store.SystemUpdateClaim
	}
	setup := func(t *testing.T, base time.Time, leaseTTL time.Duration, terminal bool) fixture {
		t.Helper()
		auth := store.NewMemoryAuthStore()
		token, err := auth.CreateServiceToken(t.Context(), "update_agent", []string{"service.register", "updates.authorize"})
		if err != nil {
			t.Fatal(err)
		}
		registerServiceWithTokenForTest(t, auth, token, store.ServiceRegistration{ServiceID: "updater-authorize", ServiceType: "update_agent", ServiceName: "Updater", PublicURL: "https://updater.example.com", Version: "v1.0.0"})
		updates := store.NewMemorySystemUpdateStore()
		job, _, err := updates.CreateSystemUpdateJob(t.Context(), store.CreateSystemUpdateJobParams{
			TargetID: "worker-01", TargetServiceType: "worker", AgentServiceID: "updater-authorize", DeploymentMode: "systemd",
			CurrentVersion: "v1.0.0", TargetVersion: "v1.1.0", Strategy: store.SystemUpdateStrategyWhenIdle, IdempotencyKey: "authorize-endpoint", RequestedByUserID: "user-01",
		})
		if err != nil {
			t.Fatal(err)
		}
		claim, _, err := updates.ClaimSystemUpdateJob(t.Context(), "updater-authorize", "", "", map[string]string{"worker-01": "systemd"}, base, time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		if _, _, err := updates.ReportSystemUpdateJob(t.Context(), job.ID, store.SystemUpdateReport{AgentServiceID: "updater-authorize", LeaseToken: claim.LeaseToken, LeaseGeneration: claim.LeaseGeneration, Sequence: 1, Status: store.SystemUpdateStatusInstalling, Progress: 70}, base.Add(time.Second), leaseTTL); err != nil {
			t.Fatal(err)
		}
		if terminal {
			if _, _, err := updates.ReportSystemUpdateJob(t.Context(), job.ID, store.SystemUpdateReport{AgentServiceID: "updater-authorize", LeaseToken: claim.LeaseToken, LeaseGeneration: claim.LeaseGeneration, Sequence: 2, Status: store.SystemUpdateStatusSucceeded, Progress: 100}, base.Add(2*time.Second), leaseTTL); err != nil {
				t.Fatal(err)
			}
		}
		handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth), WithAuditStore(auth), WithServiceRegistryStore(auth), WithSystemUpdateStore(updates))
		return fixture{handler: handler, auth: auth, token: token, job: job, claim: claim}
	}
	tests := []struct {
		name       string
		base       time.Time
		leaseTTL   time.Duration
		terminal   bool
		mutate     func(map[string]any)
		wantStatus int
		wantCode   string
		wantReason string
	}{
		{name: "previously valid request", base: time.Now().UTC(), leaseTTL: 15 * time.Minute, wantStatus: http.StatusGone, wantCode: "legacy_system_update_authorization_disabled", wantReason: "legacy_endpoint_disabled"},
		{name: "wrong lease is still disabled", base: time.Now().UTC(), leaseTTL: 15 * time.Minute, mutate: func(body map[string]any) { body["lease_token"] = "wrong" }, wantStatus: http.StatusGone, wantCode: "legacy_system_update_authorization_disabled", wantReason: "legacy_endpoint_disabled"},
		{name: "expired lease is still disabled", base: time.Now().UTC().Add(-3 * time.Minute), leaseTTL: time.Minute, wantStatus: http.StatusGone, wantCode: "legacy_system_update_authorization_disabled", wantReason: "legacy_endpoint_disabled"},
		{name: "target mismatch is still disabled", base: time.Now().UTC(), leaseTTL: 15 * time.Minute, mutate: func(body map[string]any) { body["target_id"] = "worker-02" }, wantStatus: http.StatusGone, wantCode: "legacy_system_update_authorization_disabled", wantReason: "legacy_endpoint_disabled"},
		{name: "terminal request is still disabled", base: time.Now().UTC(), leaseTTL: 15 * time.Minute, terminal: true, wantStatus: http.StatusGone, wantCode: "legacy_system_update_authorization_disabled", wantReason: "legacy_endpoint_disabled"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := setup(t, test.base, test.leaseTTL, test.terminal)
			body := map[string]any{"service_id": "updater-authorize", "lease_token": fixture.claim.LeaseToken, "lease_generation": fixture.claim.LeaseGeneration, "target_id": "worker-01", "target_version": "v1.1.0", "deployment_mode": "systemd"}
			if test.mutate != nil {
				test.mutate(body)
			}
			encoded, err := json.Marshal(body)
			if err != nil {
				t.Fatal(err)
			}
			req := httptest.NewRequest(http.MethodPost, "/services/update-jobs/"+fixture.job.ID+"/authorize", bytes.NewReader(encoded))
			req.Header.Set("Authorization", "Bearer "+fixture.token.RawToken)
			res := httptest.NewRecorder()
			fixture.handler.ServeHTTP(res, req)
			if res.Code != test.wantStatus || (test.wantCode != "" && !strings.Contains(res.Body.String(), `"code":"`+test.wantCode+`"`)) {
				t.Fatalf("authorize status = %d body = %s", res.Code, res.Body.String())
			}
			events := fixture.auth.AuditEvents()
			if len(events) == 0 {
				t.Fatal("authorization attempt was not audited")
			}
			event := events[len(events)-1]
			wantResult := "success"
			if test.wantStatus != http.StatusNoContent {
				wantResult = "failure"
			}
			if event.Action != "system_updates.authorize" || event.Result != wantResult || (test.wantReason != "" && event.Metadata["reason"] != test.wantReason) {
				t.Fatalf("authorization audit = %#v", event)
			}
			if strings.Contains(fmt.Sprintf("%#v", event), fixture.claim.LeaseToken) {
				t.Fatal("lease token leaked into authorization audit")
			}
		})
	}
}

func TestSystemUpdateMutationAuthorizationRequiresDedicatedScope(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	token, err := auth.CreateServiceToken(t.Context(), "update_agent", []string{"service.register", "updates.report"})
	if err != nil {
		t.Fatal(err)
	}
	handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth), WithServiceRegistryStore(auth))
	req := httptest.NewRequest(http.MethodPost, "/services/update-jobs/job-01/authorize", strings.NewReader(`{"service_id":"updater-01"}`))
	req.Header.Set("Authorization", "Bearer "+token.RawToken)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusForbidden || !strings.Contains(res.Body.String(), "missing_service_scope") {
		t.Fatalf("missing authorize scope status = %d body = %s", res.Code, res.Body.String())
	}
}

func TestReadUpdateResponseLimitedRejectsTruncatedPrefix(t *testing.T) {
	if _, err := readUpdateResponseLimited(strings.NewReader("12345"), 4); err == nil {
		t.Fatal("oversized update response was silently truncated")
	}
	body, err := readUpdateResponseLimited(strings.NewReader("1234"), 4)
	if err != nil || string(body) != "1234" {
		t.Fatalf("bounded update response = %q, %v", body, err)
	}
}

func TestCustomUpdateCheckURLNeverReceivesGitHubToken(t *testing.T) {
	processLatestVersionCache.clear()
	defer processLatestVersionCache.clear()
	t.Setenv("AUTOSTREAM_UPDATE_CHECK_TOKEN", "must-not-leak")
	t.Setenv("AUTOSTREAM_TEST_CUSTOM_LATEST", "")
	var authorization string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authorization = r.Header.Get("Authorization")
		writeJSON(w, http.StatusOK, map[string]string{"latest_version": "v1.2.0"})
	}))
	defer server.Close()
	t.Setenv("AUTOSTREAM_TEST_CUSTOM_URL", server.URL)
	result := latestVersion(t.Context(), versionUpdateTarget{serviceType: "worker", latestVersionEnv: "AUTOSTREAM_TEST_CUSTOM_LATEST", updateCheckURLEnv: "AUTOSTREAM_TEST_CUSTOM_URL", defaultURL: defaultWorkerUpdateCheckURL})
	if authorization != "" || result.LatestVersion != "v1.2.0" || result.ManifestErrorCode != "manifest_unverified" {
		t.Fatalf("custom update check auth=%q result=%#v", authorization, result)
	}
}

func TestBuildSystemUpdateTargetShowsLatestWhenUpdaterMissingAndRejectsOverride(t *testing.T) {
	verified := serviceUpdateInfoResponse{LatestVersion: "v1.2.0", UpdateCheckSource: "github", ManifestVerified: true}
	missing := buildSystemUpdateTarget("worker-01", "worker", "Worker", "v1.0.0", "", false, systemUpdateAgentAssignment{}, map[string]serviceUpdateInfoResponse{"worker": verified})
	if missing.LatestVersion != "v1.2.0" || !missing.UpdateAvailable || missing.Eligible || missing.BlockedReason != "updater_missing" {
		t.Fatalf("updater-missing target = %#v", missing)
	}
	override := verified
	override.UpdateCheckSource = "env"
	override.ManifestVerified = false
	override.ManifestErrorCode = "manifest_unverified"
	unverified := buildSystemUpdateTarget("worker-01", "worker", "Worker", "v1.0.0", "", false, systemUpdateAgentAssignment{AgentID: "updater-01", DeploymentMode: "systemd", Available: true, HostReachability: "reachable"}, map[string]serviceUpdateInfoResponse{"worker": override})
	if unverified.Eligible || unverified.BlockedReason != "manifest_unverified" || !unverified.UpdateAvailable {
		t.Fatalf("unverified override target = %#v", unverified)
	}
	requiresNewerAgent := verified
	requiresNewerAgent.MinimumAgentVersion = "v1.1.0"
	incompatible := buildSystemUpdateTarget("worker-01", "worker", "Worker", "v1.0.0", "", false, systemUpdateAgentAssignment{AgentID: "updater-01", AgentVersion: "v1.0.0", DeploymentMode: "systemd", Available: true, HostReachability: "reachable"}, map[string]serviceUpdateInfoResponse{"worker": requiresNewerAgent})
	if incompatible.Eligible || incompatible.BlockedReason != "updater_version_incompatible" {
		t.Fatalf("incompatible updater target = %#v", incompatible)
	}
}

func TestBuildSystemUpdateTargetRejectsUnknownCurrentVersion(t *testing.T) {
	verified := serviceUpdateInfoResponse{LatestVersion: "v1.2.0", UpdateCheckSource: "github", ManifestVerified: true}
	assignment := systemUpdateAgentAssignment{AgentID: "updater-01", AgentVersion: "v1.2.0", DeploymentMode: "systemd", Available: true, HostReachability: "reachable"}
	for _, current := range []string{"", "dev", "not-a-version", "1.2.3", "v1.2.3+build.1"} {
		target := buildSystemUpdateTarget("worker-01", "worker", "Worker", current, "", false, assignment, map[string]serviceUpdateInfoResponse{"worker": verified})
		if target.UpdateAvailable || target.Eligible || target.BlockedReason != "current_version_unknown" {
			t.Fatalf("unknown current version %q target = %#v", current, target)
		}
	}
}

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

func TestStreamStartAndReadinessRejectClaimedServiceUpdate(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "operator"}, "correct horse battery", []string{"streams.start"}); err != nil {
		t.Fatal(err)
	}
	streams := store.NewMemoryStreamStore()
	stream, err := streams.CreateStream(t.Context(), "update guarded stream")
	if err != nil {
		t.Fatal(err)
	}
	registerAssignedServices(t, auth, stream.ID, requiredStartServiceTypes...)
	updates := store.NewMemorySystemUpdateStore()
	job, _, err := updates.CreateSystemUpdateJob(t.Context(), store.CreateSystemUpdateJobParams{
		TargetID: "worker-01", TargetServiceType: "worker", DeploymentMode: "systemd", CurrentVersion: "v1.0.0", TargetVersion: "v1.1.0",
		AgentServiceID: "updater-01",
		Strategy:       store.SystemUpdateStrategyMaintenance, IdempotencyKey: "guard-stream-start", RequestedByUserID: "admin-01",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := updates.ClaimSystemUpdateJob(t.Context(), "updater-01", "", "", map[string]string{"worker-01": "systemd"}, time.Now().UTC(), time.Minute); err != nil {
		t.Fatal(err)
	}
	profiles := store.NewMemoryProfileStore()
	config := createDiscordConfigForTest(t, profiles, "update guard discord", "discord_bot-01", "guild-ready", "voice-ready", "")
	dispatcher := &fakeServiceDispatcher{}
	handler := NewServer(streams, WithAuthStore(auth), WithAuditStore(auth), WithServiceRegistryStore(auth), WithProfileStore(profiles), WithServiceDispatcher(dispatcher), WithSystemUpdateStore(updates))
	cookie, csrf := loginForTest(t, handler, "operator", "correct horse battery")
	body := `{"discord_config_id":"` + config.ID + `","discord_guild_id":"guild-test","discord_voice_channel_id":"voice-test","encoder_input_url":"srt://source.example.com:9000"}`

	readinessRequest := httptest.NewRequest(http.MethodPost, "/streams/"+stream.ID+"/start-readiness", strings.NewReader(body))
	readinessRequest.AddCookie(cookie)
	readinessRequest.Header.Set("X-CSRF-Token", csrf)
	readinessResponse := httptest.NewRecorder()
	handler.ServeHTTP(readinessResponse, readinessRequest)
	if readinessResponse.Code != http.StatusOK || !strings.Contains(readinessResponse.Body.String(), "service_update_in_progress") {
		t.Fatalf("readiness response = %d %s", readinessResponse.Code, readinessResponse.Body.String())
	}

	startRequest := httptest.NewRequest(http.MethodPost, "/streams/"+stream.ID+"/start", strings.NewReader(body))
	startRequest.AddCookie(cookie)
	startRequest.Header.Set("X-CSRF-Token", csrf)
	startResponse := httptest.NewRecorder()
	handler.ServeHTTP(startResponse, startRequest)
	if startResponse.Code != http.StatusConflict || !strings.Contains(startResponse.Body.String(), "service_update_in_progress") || !strings.Contains(startResponse.Body.String(), job.ID) {
		t.Fatalf("start response = %d %s", startResponse.Code, startResponse.Body.String())
	}
	if dispatcher.startCalls != 0 {
		t.Fatalf("start dispatched while service update was active: %#v", dispatcher)
	}
}

type updateStartRaceDispatcher struct {
	fakeServiceDispatcher
	readinessEntered chan struct{}
	releaseReadiness chan struct{}
	once             sync.Once
}

func (f *updateStartRaceDispatcher) StartReadinessIssues(_ []store.RegisteredService, _ servicecall.StartRequest, _ time.Time) []servicecall.ReadinessIssue {
	f.once.Do(func() { close(f.readinessEntered) })
	<-f.releaseReadiness
	return nil
}

func TestStreamStartWinsClaimRaceAndKeepsQueuedUpdateUnclaimed(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "operator"}, "correct horse battery", []string{"streams.start"}); err != nil {
		t.Fatal(err)
	}
	streams := store.NewMemoryStreamStore()
	stream, err := streams.CreateStream(t.Context(), "claim race stream")
	if err != nil {
		t.Fatal(err)
	}
	registerAssignedServices(t, auth, stream.ID, requiredStartServiceTypes...)
	capabilities := centralUpdateCapabilitiesForTest("host-race", map[string]string{"worker-01": "systemd"})
	agentToken := registerSystemUpdateAgentForTest(t, auth, "updater-race", capabilities)
	updates := store.NewMemorySystemUpdateStore()
	job, _, err := updates.CreateSystemUpdateJob(t.Context(), store.CreateSystemUpdateJobParams{
		TargetID: "worker-01", TargetServiceType: "worker", AgentServiceID: "updater-race", ExecutionHostID: "host-race", DeploymentMode: "systemd", CurrentVersion: "v1.0.0", TargetVersion: "v1.1.0",
		Strategy: store.SystemUpdateStrategyWhenIdle, IdempotencyKey: "race-start-claim", RequestedByUserID: "admin-01",
	})
	if err != nil {
		t.Fatal(err)
	}
	profiles := store.NewMemoryProfileStore()
	config := createDiscordConfigForTest(t, profiles, "race discord", "discord_bot-01", "", "", "")
	dispatcher := &updateStartRaceDispatcher{readinessEntered: make(chan struct{}), releaseReadiness: make(chan struct{})}
	server := NewServer(streams, WithAuthStore(auth), WithAuditStore(auth), WithServiceRegistryStore(auth), WithProfileStore(profiles), WithServiceDispatcher(dispatcher), WithSystemUpdateStore(updates))
	cookie, csrf := loginForTest(t, server, "operator", "correct horse battery")
	body := `{"discord_config_id":"` + config.ID + `","discord_guild_id":"guild-test","discord_voice_channel_id":"voice-test","encoder_input_url":"srt://source.example.com:9000"}`

	startDone := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		req := httptest.NewRequest(http.MethodPost, "/streams/"+stream.ID+"/start", strings.NewReader(body))
		req.AddCookie(cookie)
		req.Header.Set("X-CSRF-Token", csrf)
		response := httptest.NewRecorder()
		server.ServeHTTP(response, req)
		startDone <- response
	}()
	select {
	case <-dispatcher.readinessEntered:
	case <-time.After(3 * time.Second):
		t.Fatal("stream start did not reach readiness barrier")
	}
	if server.systemUpdateOperationMu.TryLock() {
		server.systemUpdateOperationMu.Unlock()
		t.Fatal("stream start did not hold the update-operation mutex")
	}
	claimDone := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		req := httptest.NewRequest(http.MethodPost, "/services/update-jobs/claim", strings.NewReader(`{"service_id":"updater-race","host_id":"host-race"}`))
		req.Header.Set("Authorization", "Bearer "+agentToken.RawToken)
		response := httptest.NewRecorder()
		server.ServeHTTP(response, req)
		claimDone <- response
	}()
	close(dispatcher.releaseReadiness)

	var startResponse, claimResponse *httptest.ResponseRecorder
	select {
	case startResponse = <-startDone:
	case <-time.After(3 * time.Second):
		t.Fatal("stream start did not finish")
	}
	select {
	case claimResponse = <-claimDone:
	case <-time.After(3 * time.Second):
		t.Fatal("update claim did not finish")
	}
	if startResponse.Code != http.StatusOK || claimResponse.Code != http.StatusNoContent || dispatcher.startCalls != 1 {
		t.Fatalf("start/claim race start=%d %s claim=%d %s dispatch=%d", startResponse.Code, startResponse.Body.String(), claimResponse.Code, claimResponse.Body.String(), dispatcher.startCalls)
	}
	active, err := updates.GetActiveSystemUpdateJob(t.Context(), job.TargetID)
	if err != nil || active.Status != store.SystemUpdateStatusQueued || active.LeaseGeneration != 0 {
		t.Fatalf("busy update was claimed after stream start: %#v err=%v", active, err)
	}
}

func TestControlPanelUpdateClaimWaitsWhileAnyStreamIsActive(t *testing.T) {
	streams := store.NewMemoryStreamStore()
	stream, err := streams.CreateStream(t.Context(), "live stream")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := streams.UpdateStreamStatus(t.Context(), stream.ID, "live"); err != nil {
		t.Fatal(err)
	}
	auth := store.NewMemoryAuthStore()
	capabilities := centralUpdateCapabilitiesForTest("host-panel", map[string]string{"control-panel": "systemd"})
	agentToken := registerSystemUpdateAgentForTest(t, auth, "updater-panel", capabilities)
	updates := store.NewMemorySystemUpdateStore()
	job, _, err := updates.CreateSystemUpdateJob(t.Context(), store.CreateSystemUpdateJobParams{
		TargetID: "control-panel", TargetServiceType: "control_panel", AgentServiceID: "updater-panel", ExecutionHostID: "host-panel", DeploymentMode: "systemd", CurrentVersion: "v1.0.0", TargetVersion: "v1.1.0",
		Strategy: store.SystemUpdateStrategyWhenIdle, IdempotencyKey: "panel-live", RequestedByUserID: "admin-01",
	})
	if err != nil {
		t.Fatal(err)
	}
	server := NewServer(streams, WithAuthStore(auth), WithServiceRegistryStore(auth), WithSystemUpdateStore(updates))
	req := httptest.NewRequest(http.MethodPost, "/services/update-jobs/claim", strings.NewReader(`{"service_id":"updater-panel","host_id":"host-panel"}`))
	req.Header.Set("Authorization", "Bearer "+agentToken.RawToken)
	response := httptest.NewRecorder()
	server.ServeHTTP(response, req)
	if response.Code != http.StatusNoContent {
		t.Fatalf("control-panel claim while live = %d %s", response.Code, response.Body.String())
	}
	active, err := updates.GetActiveSystemUpdateJob(t.Context(), job.TargetID)
	if err != nil || active.Status != store.SystemUpdateStatusQueued {
		t.Fatalf("control-panel update was claimed while live: %#v err=%v", active, err)
	}
}

func registerSystemUpdateAgentForTest(t *testing.T, auth *store.MemoryAuthStore, serviceID string, capabilities map[string]any) store.ServiceToken {
	t.Helper()
	token, err := auth.CreateServiceToken(t.Context(), "update_agent", []string{"service.register", "service.heartbeat", "updates.claim", "updates.report"})
	if err != nil {
		t.Fatal(err)
	}
	registration := store.ServiceRegistration{ServiceID: serviceID, ServiceType: "update_agent", ServiceName: serviceID, PublicURL: "https://" + serviceID + ".example.com", Version: "v1.0.0", Capabilities: capabilities}
	if _, err := auth.PrecreateService(t.Context(), token, registration); err != nil {
		t.Fatal(err)
	}
	if _, err := auth.RegisterService(t.Context(), token, registration); err != nil {
		t.Fatal(err)
	}
	if _, err := auth.Heartbeat(t.Context(), token, store.ServiceHeartbeat{ServiceID: serviceID, Status: "online", Version: "v1.0.0", Capabilities: capabilities}); err != nil {
		t.Fatal(err)
	}
	return token
}

func centralUpdateCapabilitiesForTest(hostID string, targetModes map[string]string) map[string]any {
	managed := make([]any, 0, len(targetModes))
	modes := make(map[string]any, len(targetModes))
	hosts := make(map[string]any, len(targetModes))
	versions := make(map[string]any, len(targetModes))
	for targetID, mode := range targetModes {
		managed = append(managed, targetID)
		modes[targetID] = mode
		hosts[targetID] = hostID
		versions[targetID] = "v1.0.0"
	}
	return map[string]any{
		"managed_targets": managed, "deployment_modes": modes, "target_hosts": hosts, "deployed_versions": versions,
		"host_statuses": map[string]any{hostID: "reachable"}, "host_checked_at": map[string]any{hostID: time.Now().UTC().Format(time.RFC3339Nano)},
		"host_names": map[string]any{hostID: hostID},
	}
}

func TestSystemUpdateAgentAvailabilityUsesHeartbeatDeadline(t *testing.T) {
	t.Setenv("AUTOSTREAM_NODE_HEARTBEAT_OFFLINE_AFTER", "3m")
	now := time.Now().UTC()
	fresh := now.Add(-time.Minute)
	stale := now.Add(-4 * time.Minute)
	if !systemUpdateAgentAvailable(store.RegisteredService{Status: "online", LastHeartbeatAt: &fresh}, now) {
		t.Fatal("fresh updater heartbeat was treated as offline")
	}
	if systemUpdateAgentAvailable(store.RegisteredService{Status: "online", LastHeartbeatAt: &stale}, now) {
		t.Fatal("stale updater heartbeat was treated as online")
	}
	if systemUpdateAgentAvailable(store.RegisteredService{Status: "online"}, now) {
		t.Fatal("updater without heartbeat was treated as online")
	}
}

func TestUpdateAgentCapabilitiesAreTOFUPinnedAndIntersected(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	token, err := auth.CreateServiceToken(t.Context(), "update_agent", []string{"service.register", "service.heartbeat", "updates.claim"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := auth.PrecreateService(t.Context(), token, store.ServiceRegistration{ServiceID: "updater-pinned", ServiceType: "update_agent", ServiceName: "Updater", PublicURL: "https://updater.example.com", Version: "v1.0.0", Capabilities: map[string]any{}}); err != nil {
		t.Fatal(err)
	}
	approved := centralUpdateCapabilitiesForTest("host-pinned", map[string]string{"worker-01": "systemd"})
	if _, err := auth.RegisterService(t.Context(), token, store.ServiceRegistration{ServiceID: "updater-pinned", ServiceType: "update_agent", ServiceName: "Updater", PublicURL: "https://updater.example.com", Version: "v1.0.0", Capabilities: approved}); err != nil {
		t.Fatal(err)
	}
	expanded := centralUpdateCapabilitiesForTest("host-pinned", map[string]string{"worker-01": "systemd", "worker-02": "docker"})
	service, err := auth.Heartbeat(t.Context(), token, store.ServiceHeartbeat{ServiceID: "updater-pinned", Status: "online", Version: "v1.0.0", Capabilities: expanded})
	if err != nil {
		t.Fatal(err)
	}
	modes, _ := approvedSystemUpdateAgentTargets(service)
	if len(modes) != 1 || modes["worker-01"] != "systemd" || modes["worker-02"] != "" {
		t.Fatalf("heartbeat expanded pinned targets: configured=%#v reported=%#v approved=%#v", service.Capabilities, service.ReportedCapabilities, modes)
	}
	if _, err := auth.RegisterService(t.Context(), token, store.ServiceRegistration{ServiceID: "updater-pinned", ServiceType: "update_agent", ServiceName: "Updater", PublicURL: "https://updater.example.com", Version: "v1.0.1", Capabilities: map[string]any{"managed_targets": []any{"worker-02"}, "deployment_modes": map[string]any{"worker-02": "docker"}}}); err != nil {
		t.Fatal(err)
	}
	service, err = auth.GetService(t.Context(), "updater-pinned")
	if err != nil {
		t.Fatal(err)
	}
	modes, _ = approvedSystemUpdateAgentTargets(service)
	if len(modes) != 0 || len(capabilityStringSlice(service.Capabilities["managed_targets"])) != 1 || capabilityStringSlice(service.Capabilities["managed_targets"])[0] != "worker-01" {
		t.Fatalf("re-register changed pinned targets: configured=%#v reported=%#v approved=%#v", service.Capabilities, service.ReportedCapabilities, modes)
	}

	workerToken, err := auth.CreateServiceToken(t.Context(), "worker", []string{"service.register", "service.heartbeat"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := auth.PrecreateService(t.Context(), workerToken, store.ServiceRegistration{ServiceID: "worker-legacy", ServiceType: "worker", ServiceName: "Worker", PublicURL: "https://worker.example.com", Version: "v1.0.0", Capabilities: map[string]any{}}); err != nil {
		t.Fatal(err)
	}
	if _, err := auth.RegisterService(t.Context(), workerToken, store.ServiceRegistration{ServiceID: "worker-legacy", ServiceType: "worker", ServiceName: "Worker", PublicURL: "https://worker.example.com", Version: "v1.0.0", Capabilities: map[string]any{}}); err != nil {
		t.Fatal(err)
	}
	legacyCapabilities := map[string]any{"feature": "heartbeat-updated"}
	worker, err := auth.Heartbeat(t.Context(), workerToken, store.ServiceHeartbeat{ServiceID: "worker-legacy", Status: "online", Capabilities: legacyCapabilities})
	if err != nil || worker.Capabilities["feature"] != "heartbeat-updated" {
		t.Fatalf("non-updater heartbeat capability behavior changed: service=%#v err=%v", worker, err)
	}
}
