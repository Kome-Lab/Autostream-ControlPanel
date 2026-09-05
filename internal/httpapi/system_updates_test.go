package httpapi

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	contracts "github.com/example/autostream-contracts/pkg/contracts"
	"github.com/example/autostream-control-panel/internal/servicecall"
	"github.com/example/autostream-control-panel/internal/store"
	"github.com/example/autostream-control-panel/internal/updateradapter"
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
	updates := store.NewMemorySystemUpdateStore()
	policies := store.NewMemoryUpdaterPolicyStore()
	policy, err := policies.SavePullUpdaterPolicy(t.Context(), updates, "updater-01", 0, 0, store.UpdaterPolicy{
		TransportMode:             store.SystemUpdateTransportPullV2,
		ExecutionHostID:           "host-01",
		LocalExecutorPolicySHA256: "sha256:" + strings.Repeat("a", 64),
		PollIntervalSeconds:       15,
		HeartbeatIntervalSeconds:  30,
		Targets: []store.UpdaterPolicyTarget{{
			TargetID: "worker-01", ServiceID: "worker-01", HostID: "host-01",
			ServiceType: "worker", DeploymentMode: "systemd", LocalListenPort: 18081,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	bindSystemUpdateExecutionHostForTest(t, updates, "host-01", "updater-01")
	capabilities := centralUpdateCapabilitiesForTest("host-01", map[string]string{"worker-01": "systemd"})
	capabilities["host_agent"] = true
	capabilities["observe_only"] = false
	capabilities["update_executor"] = true
	capabilities["mutation_enabled"] = true
	capabilities["transport_mode"] = store.SystemUpdateTransportPullV2
	capabilities["agent_protocol_version"] = "2"
	capabilities["execution_host_id"] = "host-01"
	capabilities["ownership_epoch"] = int64(1)
	capabilities["policy_revision"] = policy.ProjectionRevision
	capabilities["policy_status"] = "applied"
	capabilities["target_availability"] = map[string]any{"worker-01": "available"}
	capabilities["target_availability_codes"] = map[string]any{"worker-01": "executor_verified"}
	capabilities["reported_ports"] = map[string]any{"worker-01": int64(18081)}
	capabilities["port_drift"] = map[string]any{"worker-01": false}
	capabilities["reported_service_types"] = map[string]any{"worker-01": "worker"}
	capabilities["reported_deployment_modes"] = map[string]any{"worker-01": "systemd"}
	capabilities["reported_executor_policy_revisions"] = map[string]any{"worker-01": policy.LocalExecutorPolicyRevision}
	capabilities["reported_executor_policy_sha256"] = map[string]any{"worker-01": policy.LocalExecutorPolicySHA256}
	capabilities["reported_config_revisions"] = map[string]any{"worker-01": int64(1)}
	agentToken := registerSystemUpdateAgentForTest(t, auth, "updater-01", capabilities)
	if _, err := auth.Heartbeat(t.Context(), agentToken, store.ServiceHeartbeat{ServiceID: "updater-01", Status: "online", Version: "v1.9.11", Capabilities: capabilities}); err != nil {
		t.Fatal(err)
	}
	handler := NewServer(
		store.NewMemoryStreamStore(),
		WithAuthStore(auth),
		WithUpdaterPolicyStore(policies),
		WithSystemUpdateStore(updates),
	)
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
	if _, err := auth.Heartbeat(t.Context(), agentToken, store.ServiceHeartbeat{ServiceID: "updater-01", Status: "online", Version: "v1.9.11", Capabilities: capabilities}); err != nil {
		t.Fatal(err)
	}

	claimResponse := postSystemUpdateV2JSON(t, handler, agentToken.RawToken, "/services/update-jobs/claim", contracts.UpdateAgentClaimRequest{
		UpdaterID: "updater-01", HostID: "host-01", LeaseGeneration: 1, Fence: 1,
	}, http.StatusOK)
	var claim contracts.UpdaterLeaseEnvelope
	if err := json.NewDecoder(claimResponse.Body).Decode(&claim); err != nil {
		t.Fatal(err)
	}
	if claim.Command.MutationAuthorization.JobID != job.ID || claim.LeaseID == "" || claim.LeaseExpiresAt.IsZero() {
		t.Fatalf("claim = %#v", claim)
	}
	if claim.LeaseGeneration != 1 || claim.Command.MutationAuthorization.Fence != 1 {
		t.Fatalf("claim recovery contract = %#v", claim)
	}
	authorization := claim.Command.MutationAuthorization
	result := contracts.UpdaterResultEnvelope{
		ProtocolVersion: 2, CommandID: claim.Command.CommandID, JobID: authorization.JobID,
		UpdaterID: authorization.UpdaterID, HostID: authorization.HostID,
		LeaseID: claim.LeaseID, LeaseGeneration: claim.LeaseGeneration,
		IdempotencyKey: claim.Command.IdempotencyKey, CanonicalPayloadDigest: claim.Command.CanonicalPayloadDigest,
		AuthorizationID: authorization.AuthorizationID, DesiredRevision: authorization.DesiredRevision,
		AppliedRevision: authorization.DesiredRevision, Fence: authorization.Fence,
		Outcome: contracts.UpdaterOutcomeSucceeded, Status: contracts.SystemUpdateSucceeded,
		AutomaticResendAllowed: false, AuditCorrelationID: claim.Command.AuditCorrelationID,
		Evidence: []contracts.UpdaterEvidence{{EvidenceCode: "application_probe_verified", ObservedAt: time.Now().UTC(), ObservedRevision: authorization.DesiredRevision}},
	}
	reportResponse := postSystemUpdateV2JSON(t, handler, agentToken.RawToken, "/services/update-jobs/"+job.ID+"/report", result, http.StatusOK)
	var completed store.SystemUpdateJob
	if err := json.NewDecoder(reportResponse.Body).Decode(&completed); err != nil {
		t.Fatal(err)
	}
	if completed.Status != store.SystemUpdateStatusSucceeded || completed.CompletedAt == nil {
		t.Fatalf("completed job = %#v", completed)
	}
	retryReportResponse := postSystemUpdateV2JSON(t, handler, agentToken.RawToken, "/services/update-jobs/"+job.ID+"/report", result, http.StatusOK)
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
	clearActiveResponse := postSystemUpdateV2JSON(t, handler, agentToken.RawToken, "/services/update-jobs/claim", contracts.UpdateAgentClaimRequest{
		UpdaterID: "updater-01", HostID: "host-01", LeaseGeneration: completed.LeaseGeneration,
		Fence: completed.OwnershipEpoch, ActiveJobID: job.ID,
	}, http.StatusOK)
	var terminalRecovery contracts.UpdateAgentClearActiveJobResponse
	if clearActiveResponse.Code != http.StatusOK || json.Unmarshal(clearActiveResponse.Body.Bytes(), &terminalRecovery) != nil ||
		!terminalRecovery.ClearActiveJobID ||
		clearActiveResponse.Header().Get("Cache-Control") != "no-store" {
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

func TestSystemUpdateClaimFailsClosedWithoutExactTerminalRecoveryProof(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	registerServiceInstance(t, auth, "worker-proof-queued", "worker")
	registerServiceInstance(t, auth, "worker-proof-foreign", "worker")
	capabilities := centralUpdateCapabilitiesForTest("host-proof", map[string]string{
		"worker-proof-queued":  "systemd",
		"worker-proof-foreign": "systemd",
	})
	token := registerSystemUpdateAgentForTest(t, auth, "updater-proof", capabilities)
	updates := store.NewMemorySystemUpdateStore()
	bindSystemUpdateExecutionHostForTest(t, updates, "host-proof", "updater-proof")
	queued, _, err := updates.CreateSystemUpdateJob(t.Context(), store.CreateSystemUpdateJobParams{
		TargetID: "worker-proof-queued", TargetServiceType: "worker", AgentServiceID: "updater-proof", ExecutionHostID: "host-proof",
		DeploymentMode: "systemd", CurrentVersion: "v1.0.0", TargetVersion: "v1.1.0", Strategy: store.SystemUpdateStrategyWhenIdle,
		IdempotencyKey: "terminal-proof-queued", RequestedByUserID: "admin-proof",
	})
	if err != nil {
		t.Fatal(err)
	}
	bindSystemUpdateExecutionHostForTest(t, updates, "host-foreign", "updater-other")
	foreign, _, err := updates.CreateSystemUpdateJob(t.Context(), store.CreateSystemUpdateJobParams{
		TargetID: "worker-proof-foreign", TargetServiceType: "worker", AgentServiceID: "updater-other", ExecutionHostID: "host-foreign",
		DeploymentMode: "systemd", CurrentVersion: "v1.0.0", TargetVersion: "v1.1.0", Strategy: store.SystemUpdateStrategyWhenIdle,
		IdempotencyKey: "terminal-proof-foreign", RequestedByUserID: "admin-proof",
	})
	if err != nil {
		t.Fatal(err)
	}
	server := NewServer(
		store.NewMemoryStreamStore(),
		WithAuthStore(auth),
		WithServiceRegistryStore(auth),
		WithSystemUpdateStore(updates),
	)

	for _, test := range []struct {
		name     string
		activeID string
		wantCode string
	}{
		{name: "missing job", activeID: "missing-job", wantCode: "system_update_recovery_proof_unavailable"},
		{name: "nonterminal job", activeID: queued.ID, wantCode: "system_update_recovery_proof_unavailable"},
		{name: "wrong agent", activeID: foreign.ID, wantCode: "system_update_ownership_conflict"},
	} {
		t.Run(test.name, func(t *testing.T) {
			body, err := json.Marshal(contracts.UpdateAgentClaimRequest{
				UpdaterID:       "updater-proof",
				HostID:          "host-proof",
				LeaseGeneration: 1,
				Fence:           1,
				ActiveJobID:     test.activeID,
			})
			if err != nil {
				t.Fatal(err)
			}
			request := httptest.NewRequest(http.MethodPost, "/services/update-jobs/claim", bytes.NewReader(body))
			request.Header.Set("Authorization", "Bearer "+token.RawToken)
			request.Header.Set(systemUpdateContractMajorHeader, "2")
			response := httptest.NewRecorder()
			server.ServeHTTP(response, request)
			var payload map[string]any
			if response.Code != http.StatusConflict || json.Unmarshal(response.Body.Bytes(), &payload) != nil ||
				payload["code"] != test.wantCode || payload["clear_active_job_id"] != nil || payload["terminal_job"] != nil {
				t.Fatalf("claim without exact terminal proof = %d %s", response.Code, response.Body.String())
			}
		})
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
	registerServiceWithTokenForTest(t, auth, token, store.ServiceRegistration{
		ServiceID: "updater-01", ServiceType: "update_agent", ServiceName: "Updater",
		TransportMode: store.SystemUpdateTransportPullV2, ExecutionHostID: "host-assignment", Version: "v1.0.0",
	})
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

func TestUpdateAgentOnboardingUsesOneTimeConfigureCommand(t *testing.T) {
	t.Setenv("AUTOSTREAM_SECRET_ENCRYPTION_KEY", "test-secret-encryption-key-32-bytes")
	t.Setenv("AUTOSTREAM_BIND_ADDR", "0.0.0.0:80")
	t.Setenv("AUTOSTREAM_CONFIG_REVISION", "0")
	t.Setenv("AUTOSTREAM_PUBLIC_URL", "https://panel.example.com")
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "admin", Roles: []string{"super_admin"}}, "correct horse battery", []string{"api_tokens.create", "api_tokens.revoke", "service_health.read", "system_updates.execute", "secrets.update"}); err != nil {
		t.Fatal(err)
	}
	workerToken, err := auth.CreateServiceToken(
		t.Context(),
		"worker",
		[]string{"service.register", "service.heartbeat"},
	)
	if err != nil {
		t.Fatal(err)
	}
	worker := registerServiceWithTokenForTest(
		t,
		auth,
		workerToken,
		store.ServiceRegistration{
			ServiceID:   "worker-onboarding",
			ServiceType: "worker",
			ServiceName: "Worker Onboarding",
			Host:        "worker.example.com",
			Port:        443,
			SSLEnabled:  true,
			PublicURL:   "https://worker.example.com",
			Version:     "v1.0.0",
		},
	)
	updates := store.NewMemorySystemUpdateStore()
	policies := store.NewMemoryUpdaterPolicyStore()
	handler := NewServer(
		store.NewMemoryStreamStore(),
		WithAuthStore(auth),
		WithAuditStore(auth),
		WithServiceRegistryStore(auth),
		WithUpdaterPolicyStore(policies),
		WithSystemUpdateStore(updates),
	)
	cookie, csrf := loginForTest(t, handler, "admin", "correct horse battery")

	create := httptest.NewRequest(http.MethodPost, "/nodes/registration-tokens", strings.NewReader(`{"node_type":"update_agent","node_id":"updater-01","name":"Updater 01","transport_mode":"pull_v2","execution_host_id":"host-01"}`))
	create.AddCookie(cookie)
	create.Header.Set("X-CSRF-Token", csrf)
	created := httptest.NewRecorder()
	handler.ServeHTTP(created, create)
	if created.Code != http.StatusCreated {
		t.Fatalf("create updater status = %d body = %s", created.Code, created.Body.String())
	}
	if created.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("updater registration secret response cache control = %q", created.Header().Get("Cache-Control"))
	}
	createdPayload := append([]byte(nil), created.Body.Bytes()...)
	var createdBody struct {
		ConfigureToken    string   `json:"configure_token"`
		ConfigureCommand  string   `json:"configure_command"`
		ConfigurationPath string   `json:"configuration_path"`
		ManualRequired    bool     `json:"manual_configuration_required"`
		RuntimeToken      string   `json:"runtime_token"`
		Scopes            []string `json:"scopes"`
	}
	if err := json.NewDecoder(bytes.NewReader(createdPayload)).Decode(&createdBody); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(createdBody.ConfigureToken, "ast_cfg_") || !strings.HasPrefix(createdBody.ConfigureCommand, "sudo /usr/local/bin/autostream-host-agent configure ") || strings.Contains(createdBody.ConfigureCommand, createdBody.ConfigureToken) || strings.Contains(createdBody.ConfigureCommand, "--token") {
		t.Fatalf("updater configure metadata = %#v", createdBody)
	}
	if createdBody.ConfigurationPath != "/etc/autostream/updater/agent.yaml" || createdBody.ManualRequired || bytes.Contains(createdPayload, []byte(`"configuration_example"`)) {
		t.Fatalf("updater configuration metadata = %#v", createdBody)
	}
	if !slices.Contains(createdBody.Scopes, "updates.authorize") {
		t.Fatalf("updater registration omitted authorize scope: %#v", createdBody.Scopes)
	}
	if !strings.HasPrefix(createdBody.RuntimeToken, "ast_svc_") {
		t.Fatalf("updater registration omitted initial runtime token: %#v", createdBody)
	}
	registeredService, err := auth.GetService(t.Context(), "updater-01")
	if err != nil {
		t.Fatal(err)
	}
	if registeredService.TokenID == "" {
		t.Fatal("updater registration omitted the active token ID")
	}
	initialTokenID := registeredService.TokenID

	configuration := httptest.NewRequest(http.MethodGet, "/nodes/updater-01/configuration", nil)
	configuration.AddCookie(cookie)
	configurationResult := httptest.NewRecorder()
	handler.ServeHTTP(configurationResult, configuration)
	if configurationResult.Code != http.StatusOK {
		t.Fatalf("get updater configuration status = %d body = %s", configurationResult.Code, configurationResult.Body.String())
	}
	if strings.Contains(configurationResult.Body.String(), `"configure_command"`) || strings.Contains(configurationResult.Body.String(), "regenerate-configure-token") || strings.Contains(configurationResult.Body.String(), `"manual_configuration_required":true`) {
		t.Fatalf("get updater configuration exposed a fake or legacy configure workflow: %s", configurationResult.Body.String())
	}

	regenerate := httptest.NewRequest(http.MethodPost, "/nodes/updater-01/configure-token", nil)
	regenerate.AddCookie(cookie)
	regenerate.Header.Set("X-CSRF-Token", csrf)
	regenerateResult := httptest.NewRecorder()
	handler.ServeHTTP(regenerateResult, regenerate)
	if regenerateResult.Code != http.StatusCreated {
		t.Fatalf("updater configure-token status = %d body = %s", regenerateResult.Code, regenerateResult.Body.String())
	}
	if regenerateResult.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("configure token secret response cache control = %q", regenerateResult.Header().Get("Cache-Control"))
	}
	var regenerated struct {
		ConfigureToken   string `json:"configure_token"`
		ConfigureCommand string `json:"configure_command"`
	}
	if err := json.NewDecoder(regenerateResult.Body).Decode(&regenerated); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(regenerated.ConfigureToken, "ast_cfg_") || strings.Contains(regenerated.ConfigureCommand, regenerated.ConfigureToken) || strings.Contains(regenerated.ConfigureCommand, "--token") || !strings.HasPrefix(regenerated.ConfigureCommand, "sudo /usr/local/bin/autostream-host-agent configure ") {
		t.Fatalf("regenerated updater configure metadata = %#v", regenerated)
	}
	placeholderDigest := "sha256:" + strings.Repeat("a", 64)
	savedPolicy, err := policies.SavePullUpdaterPolicy(
		t.Context(),
		updates,
		"updater-01",
		0,
		0,
		store.UpdaterPolicy{
			TransportMode:             store.SystemUpdateTransportPullV2,
			ExecutionHostID:           "host-01",
			LocalExecutorPolicySHA256: placeholderDigest,
			PollIntervalSeconds:       15,
			HeartbeatIntervalSeconds:  30,
			Targets: []store.UpdaterPolicyTarget{{
				TargetID:        worker.ServiceID,
				ServiceID:       worker.ServiceID,
				HostID:          "host-01",
				ServiceType:     worker.ServiceType,
				DeploymentMode:  updateradapter.ModeSystemd,
				LocalListenPort: 18081,
			}},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	newStageRequest := func(configureToken string) *http.Request {
		t.Helper()
		payload, err := json.Marshal(map[string]any{
			"nodeId":          "updater-01",
			"configureToken":  configureToken,
			"protocolVersion": updateradapter.HostAgentConfigureProtocolVersion,
			"agentUid":        uint32(1001),
			"agentGid":        uint32(1002),
		})
		if err != nil {
			t.Fatal(err)
		}
		return httptest.NewRequest(
			http.MethodPost,
			"/services/host-agent/runtime-identity/stage",
			bytes.NewReader(payload),
		)
	}
	supersededConfigure := newStageRequest(createdBody.ConfigureToken)
	supersededConfigureResult := httptest.NewRecorder()
	handler.ServeHTTP(supersededConfigureResult, supersededConfigure)
	if supersededConfigureResult.Code != http.StatusUnauthorized || !strings.Contains(supersededConfigureResult.Body.String(), "invalid_configure_token") {
		t.Fatalf("superseded updater configure token status = %d body = %s", supersededConfigureResult.Code, supersededConfigureResult.Body.String())
	}

	agentConfigure := newStageRequest(regenerated.ConfigureToken)
	agentConfigureResult := httptest.NewRecorder()
	handler.ServeHTTP(agentConfigureResult, agentConfigure)
	if agentConfigureResult.Code != http.StatusOK {
		t.Fatalf("updater agent configure status = %d body = %s", agentConfigureResult.Code, agentConfigureResult.Body.String())
	}
	if agentConfigureResult.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("configured runtime token response cache control = %q", agentConfigureResult.Header().Get("Cache-Control"))
	}
	assertExactKeys := func(label string, fields map[string]json.RawMessage, expected ...string) {
		t.Helper()
		got := make([]string, 0, len(fields))
		for name := range fields {
			got = append(got, name)
		}
		slices.Sort(got)
		slices.Sort(expected)
		if !slices.Equal(got, expected) {
			t.Fatalf("%s keys = %#v; want %#v", label, got, expected)
		}
	}
	stageResponse := agentConfigureResult.Body.Bytes()
	var configuredEnvelope map[string]json.RawMessage
	if err := json.Unmarshal(stageResponse, &configuredEnvelope); err != nil {
		t.Fatal(err)
	}
	assertExactKeys(
		"updater configure response",
		configuredEnvelope,
		"activation_expires_at",
		"activation_token",
		"config",
		"configuration_id",
		"configure_protocol_version",
		"local_executor_policy",
	)
	var configuredFields map[string]json.RawMessage
	if err := json.Unmarshal(configuredEnvelope["config"], &configuredFields); err != nil {
		t.Fatal(err)
	}
	assertExactKeys(
		"updater configure",
		configuredFields,
		"api",
		"node_id",
		"panel_url",
		"runtime_token",
		"service_name",
		"service_type",
		"transport_mode",
	)
	var staged updateradapter.UpdaterStagedConfiguration
	if err := json.Unmarshal(stageResponse, &staged); err != nil {
		t.Fatal(err)
	}
	if staged.ConfigureProtocol != updateradapter.HostAgentConfigureProtocolVersion ||
		staged.Config.TransportMode != store.SystemUpdateTransportPullV2 ||
		staged.Config.API != (updateradapter.UpdaterConfigureAPIAssertion{}) ||
		staged.LocalExecutorPolicy == nil {
		t.Fatal("updater stage did not return the canonical pull-v2 configure protocol")
	}
	if staged.Config.PanelURL != "https://panel.example.com" ||
		staged.Config.NodeID != "updater-01" ||
		staged.Config.ServiceName != "Updater 01" ||
		staged.Config.ServiceType != "update_agent" ||
		!strings.HasPrefix(staged.Config.RuntimeToken, "ast_svc_") ||
		!strings.HasPrefix(staged.ActivationToken, "ast_act_") ||
		staged.ActivationExpiresAt.IsZero() {
		t.Fatal("updater stage omitted required v2 identity or activation metadata")
	}
	if staged.LocalExecutorPolicy.SHA256 == "" ||
		staged.LocalExecutorPolicy.SourcePolicyRevision != savedPolicy.Revision ||
		staged.LocalExecutorPolicy.ProjectionRevision != savedPolicy.ProjectionRevision ||
		staged.LocalExecutorPolicy.PolicyRevision != savedPolicy.LocalExecutorPolicyRevision {
		t.Fatal("updater stage returned an unbound Local Executor policy projection")
	}
	var stagedPolicy updateradapter.LocalExecutorPolicy
	if err := json.Unmarshal(staged.LocalExecutorPolicy.Policy, &stagedPolicy); err != nil {
		t.Fatal(err)
	}
	if stagedPolicy.AgentUID != 1001 ||
		stagedPolicy.AgentGID != 1002 ||
		stagedPolicy.HostID != "host-01" ||
		stagedPolicy.SourcePolicyRevision != savedPolicy.Revision ||
		stagedPolicy.ProjectionRevision != savedPolicy.ProjectionRevision ||
		stagedPolicy.PolicyRevision != savedPolicy.LocalExecutorPolicyRevision ||
		len(stagedPolicy.Targets) != 1 {
		t.Fatal("updater stage returned an unexpected Local Executor policy identity or revision")
	}
	stagedTarget := stagedPolicy.Targets[0]
	if stagedTarget.ServiceID != worker.ServiceID ||
		stagedTarget.ServiceType != worker.ServiceType ||
		stagedTarget.DeploymentMode != updateradapter.ModeSystemd ||
		stagedTarget.LocalListen != (updateradapter.LocalExecutorEndpoint{
			Host: "127.0.0.1",
			Port: 18081,
		}) {
		t.Fatal("updater stage returned an unexpected Local Executor target")
	}
	for _, forbidden := range []struct {
		label string
		value string
	}{
		{label: "initial raw configure token", value: createdBody.ConfigureToken},
		{label: "regenerated raw configure token", value: regenerated.ConfigureToken},
		{label: "legacy config_yml", value: `"config_yml"`},
		{label: "legacy configuration_yaml", value: `"configuration_yaml"`},
		{label: "GitHub token", value: `"github_token"`},
		{label: "legacy host inventory", value: `"hosts"`},
		{label: "legacy identity file", value: `"identity_file"`},
		{label: "private SSH key", value: `"ssh_private_key"`},
		{label: "private key", value: `"private_key"`},
		{label: "plaintext bootstrap token", value: `"bootstrap_token"`},
		{label: "plaintext bootstrap credential", value: `"bootstrap_credential"`},
	} {
		if strings.Contains(string(stageResponse), forbidden.value) {
			t.Fatalf("updater configure response exposed %s", forbidden.label)
		}
	}
	if _, err := auth.AuthenticateServiceToken(t.Context(), createdBody.RuntimeToken, "service.heartbeat"); err != nil {
		t.Fatalf("initial updater runtime token stopped before activation: %v", err)
	}
	if _, err := auth.AuthenticateServiceToken(t.Context(), staged.Config.RuntimeToken, "updates.claim"); !errors.Is(err, store.ErrUnauthorized) {
		t.Fatalf("staged updater runtime token was active before activation: %v", err)
	}
	registeredService, err = auth.GetService(t.Context(), "updater-01")
	if err != nil {
		t.Fatal(err)
	}
	if staged.ConfigurationID == "" ||
		!strings.HasPrefix(staged.ConfigurationID, "hac1-") ||
		staged.ConfigurationID == registeredService.StagedNodeTokenID ||
		registeredService.StagedNodeTokenID == "" ||
		registeredService.TokenID != initialTokenID {
		t.Fatal("updater stage did not preserve the active token and bound external configuration ID")
	}
	replay := newStageRequest(regenerated.ConfigureToken)
	replayResult := httptest.NewRecorder()
	handler.ServeHTTP(replayResult, replay)
	if replayResult.Code != http.StatusUnauthorized || !strings.Contains(replayResult.Body.String(), "invalid_configure_token") {
		t.Fatalf("replayed updater configure token status = %d body = %s", replayResult.Code, replayResult.Body.String())
	}
	activation := hostAgentConfigureActivationPayload(
		staged,
		uint32(1001),
		uint32(1002),
		*staged.LocalExecutorPolicy,
	)
	activateResult := sendHostAgentConfigureActivation(t, handler, activation)
	if activateResult.Code != http.StatusOK {
		t.Fatalf("updater activation status = %d body = %s", activateResult.Code, activateResult.Body.String())
	}
	if activateResult.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("updater activation cache control = %q", activateResult.Header().Get("Cache-Control"))
	}
	var activationResult updateradapter.UpdaterActivationResult
	if err := json.NewDecoder(activateResult.Body).Decode(&activationResult); err != nil {
		t.Fatal(err)
	}
	if activationResult.State != "activated" ||
		activationResult.ConfigurationID != staged.ConfigurationID ||
		activationResult.ConfigureProtocol != updateradapter.HostAgentConfigureProtocolVersion ||
		activationResult.LocalExecutorPolicySHA256 != staged.LocalExecutorPolicy.SHA256 ||
		activationResult.SourcePolicyRevision != savedPolicy.Revision ||
		activationResult.ProjectionRevision != savedPolicy.ProjectionRevision ||
		activationResult.LocalExecutorPolicyRevision != savedPolicy.LocalExecutorPolicyRevision {
		t.Fatal("updater activation result did not match the staged v2 policy binding")
	}
	if _, err := auth.AuthenticateServiceToken(t.Context(), createdBody.RuntimeToken, "service.heartbeat"); !errors.Is(err, store.ErrUnauthorized) {
		t.Fatalf("initial updater runtime token survived activation: %v", err)
	}
	for _, scope := range []string{"service.register", "service.heartbeat", "updates.claim", "updates.report", "updates.authorize"} {
		if _, err := auth.AuthenticateServiceToken(t.Context(), staged.Config.RuntimeToken, scope); err != nil {
			t.Fatalf("activated updater runtime token lacks %s: %v", scope, err)
		}
	}
	activateReplayResult := sendHostAgentConfigureActivation(t, handler, activation)
	if activateReplayResult.Code != http.StatusOK {
		t.Fatalf("idempotent updater activation status = %d body = %s", activateReplayResult.Code, activateReplayResult.Body.String())
	}
	var activationReplay updateradapter.UpdaterActivationResult
	if err := json.NewDecoder(activateReplayResult.Body).Decode(&activationReplay); err != nil {
		t.Fatal(err)
	}
	if activationReplay.State != "already_activated" ||
		activationReplay.ConfigurationID != staged.ConfigurationID ||
		activationReplay.ConfigureProtocol != updateradapter.HostAgentConfigureProtocolVersion ||
		activationReplay.LocalExecutorPolicySHA256 != staged.LocalExecutorPolicy.SHA256 ||
		activationReplay.SourcePolicyRevision != savedPolicy.Revision ||
		activationReplay.ProjectionRevision != savedPolicy.ProjectionRevision ||
		activationReplay.LocalExecutorPolicyRevision != savedPolicy.LocalExecutorPolicyRevision {
		t.Fatal("idempotent updater activation did not preserve the staged v2 policy binding")
	}
	audits, err := auth.ListAudit(t.Context(), store.AuditFilter{Actions: []string{"nodes.configure"}, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(audits) != 1 || audits[0].ResourceID != "updater-01" {
		t.Fatalf("updater configure audit count=%d", len(audits))
	}
	auditMetadata := fmt.Sprint(audits[0].Metadata)
	for _, secret := range []string{
		createdBody.ConfigureToken,
		regenerated.ConfigureToken,
		createdBody.RuntimeToken,
		staged.Config.RuntimeToken,
		staged.ActivationToken,
	} {
		if strings.Contains(auditMetadata, secret) {
			t.Fatal("updater configure audit exposed a configure, runtime, or activation secret")
		}
	}

	outstandingRequest := httptest.NewRequest(http.MethodPost, "/nodes/updater-01/configure-token", nil)
	outstandingRequest.AddCookie(cookie)
	outstandingRequest.Header.Set("X-CSRF-Token", csrf)
	outstandingResult := httptest.NewRecorder()
	handler.ServeHTTP(outstandingResult, outstandingRequest)
	if outstandingResult.Code != http.StatusCreated {
		t.Fatalf("create outstanding configure token status = %d", outstandingResult.Code)
	}
	if outstandingResult.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("outstanding configure token cache control = %q", outstandingResult.Header().Get("Cache-Control"))
	}
	var outstanding struct {
		ConfigureToken string `json:"configure_token"`
	}
	if err := json.NewDecoder(outstandingResult.Body).Decode(&outstanding); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(outstanding.ConfigureToken, "ast_cfg_") {
		t.Fatal("outstanding configure token has an unexpected format")
	}
	configuredRuntimeToken := staged.Config.RuntimeToken
	beforeRejectedRotate, err := auth.GetService(t.Context(), "updater-01")
	if err != nil {
		t.Fatal(err)
	}
	tokensBeforeRejectedRotate, err := auth.ListServiceTokens(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if beforeRejectedRotate.ConfigureTokenHash == "" ||
		beforeRejectedRotate.ConfigureTokenExpiresAt == nil ||
		beforeRejectedRotate.ConfigureTokenUsedAt != nil {
		t.Fatal("outstanding configure credential is not pending before direct rotation")
	}
	if beforeRejectedRotate.StagedNodeTokenID != "" {
		t.Fatal("updater has a staged runtime token before direct rotation")
	}
	if beforeRejectedRotate.TokenID != registeredService.StagedNodeTokenID {
		t.Fatal("activated runtime token is not current before direct rotation")
	}

	rotate := httptest.NewRequest(http.MethodPost, "/nodes/updater-01/rotate-token", nil)
	rotate.AddCookie(cookie)
	rotate.Header.Set("X-CSRF-Token", csrf)
	rotateResult := httptest.NewRecorder()
	handler.ServeHTTP(rotateResult, rotate)
	if rotateResult.Code != http.StatusConflict {
		t.Fatalf("direct updater runtime token rotation status = %d; want 409", rotateResult.Code)
	}
	rotateResponse := rotateResult.Body.Bytes()
	for _, forbidden := range []struct {
		label string
		value string
	}{
		{label: "outstanding configure token", value: outstanding.ConfigureToken},
		{label: "initial configure token", value: createdBody.ConfigureToken},
		{label: "regenerated configure token", value: regenerated.ConfigureToken},
		{label: "initial runtime token", value: createdBody.RuntimeToken},
		{label: "configured runtime token", value: configuredRuntimeToken},
		{label: "activation token", value: staged.ActivationToken},
		{label: "runtime token field", value: `"runtime_token"`},
		{label: "runtime token prefix", value: "ast_svc_"},
		{label: "configuration path field", value: `"configuration_path"`},
		{label: "manual configuration field", value: `"manual_configuration_required"`},
		{label: "token ID field", value: `"token_id"`},
	} {
		if bytes.Contains(rotateResponse, []byte(forbidden.value)) {
			t.Fatalf("rejected direct rotation exposed %s", forbidden.label)
		}
	}
	var rotateFields map[string]json.RawMessage
	if err := json.Unmarshal(rotateResponse, &rotateFields); err != nil {
		t.Fatal("rejected direct rotation did not return a JSON object")
	}
	assertExactKeys("rejected direct rotation", rotateFields, "code")
	var rotateCode string
	if err := json.Unmarshal(rotateFields["code"], &rotateCode); err != nil {
		t.Fatal("rejected direct rotation code is not a string")
	}
	if rotateCode != "staged_runtime_token_rotation_required" {
		t.Fatal("rejected direct rotation did not require staged runtime token rotation")
	}

	afterRejectedRotate, err := auth.GetService(t.Context(), "updater-01")
	if err != nil {
		t.Fatal(err)
	}
	tokensAfterRejectedRotate, err := auth.ListServiceTokens(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	sameOptionalTime := func(left, right *time.Time) bool {
		if left == nil || right == nil {
			return left == nil && right == nil
		}
		return left.Equal(*right)
	}
	for _, field := range []struct {
		name      string
		unchanged bool
	}{
		{"TokenID", beforeRejectedRotate.TokenID == afterRejectedRotate.TokenID},
		{"NodeTokenCiphertext", beforeRejectedRotate.NodeTokenCiphertext == afterRejectedRotate.NodeTokenCiphertext},
		{"NodeTokenNonce", beforeRejectedRotate.NodeTokenNonce == afterRejectedRotate.NodeTokenNonce},
		{"NodeTokenRotatedAt", sameOptionalTime(beforeRejectedRotate.NodeTokenRotatedAt, afterRejectedRotate.NodeTokenRotatedAt)},
		{"ConfigureTokenHash", beforeRejectedRotate.ConfigureTokenHash == afterRejectedRotate.ConfigureTokenHash},
		{"ConfigureTokenExpiresAt", sameOptionalTime(beforeRejectedRotate.ConfigureTokenExpiresAt, afterRejectedRotate.ConfigureTokenExpiresAt)},
		{"ConfigureTokenUsedAt", sameOptionalTime(beforeRejectedRotate.ConfigureTokenUsedAt, afterRejectedRotate.ConfigureTokenUsedAt)},
		{"StagedNodePreviousTokenID", beforeRejectedRotate.StagedNodePreviousTokenID == afterRejectedRotate.StagedNodePreviousTokenID},
		{"StagedNodeTokenID", beforeRejectedRotate.StagedNodeTokenID == afterRejectedRotate.StagedNodeTokenID},
		{"StagedNodeTokenHash", beforeRejectedRotate.StagedNodeTokenHash == afterRejectedRotate.StagedNodeTokenHash},
		{"StagedNodeTokenScopes", slices.Equal(beforeRejectedRotate.StagedNodeTokenScopes, afterRejectedRotate.StagedNodeTokenScopes)},
		{"StagedNodeTokenCiphertext", beforeRejectedRotate.StagedNodeTokenCiphertext == afterRejectedRotate.StagedNodeTokenCiphertext},
		{"StagedNodeTokenNonce", beforeRejectedRotate.StagedNodeTokenNonce == afterRejectedRotate.StagedNodeTokenNonce},
		{"StagedNodeActivationTokenHash", beforeRejectedRotate.StagedNodeActivationTokenHash == afterRejectedRotate.StagedNodeActivationTokenHash},
		{"StagedNodeTokenAt", sameOptionalTime(beforeRejectedRotate.StagedNodeTokenAt, afterRejectedRotate.StagedNodeTokenAt)},
		{"service-token inventory count", len(tokensBeforeRejectedRotate) == len(tokensAfterRejectedRotate)},
	} {
		if !field.unchanged {
			t.Fatalf("rejected direct rotation changed %s", field.name)
		}
	}
	for _, scope := range []string{"service.register", "service.heartbeat", "updates.claim", "updates.report", "updates.authorize"} {
		if _, err := auth.AuthenticateServiceToken(t.Context(), configuredRuntimeToken, scope); err != nil {
			t.Fatalf("configured updater runtime token lacks %s after rejected direct rotation", scope)
		}
	}
	if _, err := auth.AuthenticateServiceToken(t.Context(), createdBody.RuntimeToken, "service.heartbeat"); !errors.Is(err, store.ErrUnauthorized) {
		t.Fatal("initial updater runtime token is not unauthorized after rejected direct rotation")
	}
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
	registerServiceWithTokenForTest(t, auth, updaterToken, store.ServiceRegistration{
		ServiceID: "updater-delete", ServiceType: "update_agent", ServiceName: "Updater",
		TransportMode: store.SystemUpdateTransportPullV2, ExecutionHostID: "host-delete", OwnershipEpoch: 1,
	})
	updates := store.NewMemorySystemUpdateStore()
	bindSystemUpdateExecutionHostForTest(t, updates, "host-delete", "updater-delete")
	job, _, err := updates.CreateSystemUpdateJob(t.Context(), store.CreateSystemUpdateJobParams{
		TargetID: "worker-delete", TargetServiceType: "worker", AgentServiceID: "updater-delete", ExecutionHostID: "host-delete", DeploymentMode: "systemd",
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
	claim, _, err := updates.ClaimSystemUpdateJob(t.Context(), "updater-delete", "host-delete", "", map[string]string{"worker-delete": "systemd"}, now, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := updates.ReportSystemUpdateJob(t.Context(), job.ID, store.SystemUpdateReport{AgentServiceID: "updater-delete", ExecutionHostID: "host-delete", LeaseToken: claim.LeaseToken, LeaseGeneration: claim.LeaseGeneration, Sequence: 1, Status: store.SystemUpdateStatusSucceeded, Progress: 100}, now.Add(time.Second), time.Minute); err != nil {
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

func TestSystemUpdateMutationAuthorizationEndpointIsAbsentForEveryLegacyPayload(t *testing.T) {
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
		registerServiceWithTokenForTest(t, auth, token, store.ServiceRegistration{
			ServiceID: "updater-authorize", ServiceType: "update_agent", ServiceName: "Updater",
			TransportMode: store.SystemUpdateTransportPullV2, ExecutionHostID: "host-authorize", OwnershipEpoch: 1, Version: "v1.0.0",
		})
		updates := store.NewMemorySystemUpdateStore()
		bindSystemUpdateExecutionHostForTest(t, updates, "host-authorize", "updater-authorize")
		job, _, err := updates.CreateSystemUpdateJob(t.Context(), store.CreateSystemUpdateJobParams{
			TargetID: "worker-01", TargetServiceType: "worker", AgentServiceID: "updater-authorize", ExecutionHostID: "host-authorize", DeploymentMode: "systemd",
			CurrentVersion: "v1.0.0", TargetVersion: "v1.1.0", Strategy: store.SystemUpdateStrategyWhenIdle, IdempotencyKey: "authorize-endpoint", RequestedByUserID: "user-01",
		})
		if err != nil {
			t.Fatal(err)
		}
		claim, _, err := updates.ClaimSystemUpdateJob(t.Context(), "updater-authorize", "host-authorize", "", map[string]string{"worker-01": "systemd"}, base, time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		if _, _, err := updates.ReportSystemUpdateJob(t.Context(), job.ID, store.SystemUpdateReport{AgentServiceID: "updater-authorize", ExecutionHostID: "host-authorize", LeaseToken: claim.LeaseToken, LeaseGeneration: claim.LeaseGeneration, Sequence: 1, Status: store.SystemUpdateStatusInstalling, Progress: 70}, base.Add(time.Second), leaseTTL); err != nil {
			t.Fatal(err)
		}
		if terminal {
			if _, _, err := updates.ReportSystemUpdateJob(t.Context(), job.ID, store.SystemUpdateReport{AgentServiceID: "updater-authorize", ExecutionHostID: "host-authorize", LeaseToken: claim.LeaseToken, LeaseGeneration: claim.LeaseGeneration, Sequence: 2, Status: store.SystemUpdateStatusSucceeded, Progress: 100}, base.Add(2*time.Second), leaseTTL); err != nil {
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
		{name: "previously valid request", base: time.Now().UTC(), leaseTTL: 15 * time.Minute, wantStatus: http.StatusNotFound},
		{name: "wrong lease is still absent", base: time.Now().UTC(), leaseTTL: 15 * time.Minute, mutate: func(body map[string]any) { body["lease_token"] = "wrong" }, wantStatus: http.StatusNotFound},
		{name: "expired lease is still absent", base: time.Now().UTC().Add(-3 * time.Minute), leaseTTL: time.Minute, wantStatus: http.StatusNotFound},
		{name: "target mismatch is still absent", base: time.Now().UTC(), leaseTTL: 15 * time.Minute, mutate: func(body map[string]any) { body["target_id"] = "worker-02" }, wantStatus: http.StatusNotFound},
		{name: "terminal request is still absent", base: time.Now().UTC(), leaseTTL: 15 * time.Minute, terminal: true, wantStatus: http.StatusNotFound},
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
			if events := fixture.auth.AuditEvents(); len(events) != 0 {
				t.Fatalf("absent route emitted audit events: %#v", events)
			}
		})
	}
}

func TestSystemUpdateMutationAuthorizationRouteIsAbsentBeforeScopeEvaluation(t *testing.T) {
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
	if res.Code != http.StatusNotFound {
		t.Fatalf("absent authorize route status = %d body = %s", res.Code, res.Body.String())
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
	bindSystemUpdateExecutionHostForTest(t, updates, "host-01", "updater-01")
	job, _, err := updates.CreateSystemUpdateJob(t.Context(), store.CreateSystemUpdateJobParams{
		TargetID: "worker-01", TargetServiceType: "worker", DeploymentMode: "systemd", CurrentVersion: "v1.0.0", TargetVersion: "v1.1.0",
		AgentServiceID: "updater-01", ExecutionHostID: "host-01",
		Strategy: store.SystemUpdateStrategyMaintenance, IdempotencyKey: "guard-stream-start", RequestedByUserID: "admin-01",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := updates.ClaimSystemUpdateJob(t.Context(), "updater-01", "host-01", "", map[string]string{"worker-01": "systemd"}, time.Now().UTC(), time.Minute); err != nil {
		t.Fatal(err)
	}
	profiles := store.NewMemoryProfileStore()
	config := createDiscordConfigForTest(t, profiles, "update guard discord", "discord_bot-01", "guild-ready", "voice-ready", "")
	dispatcher := &fakeServiceDispatcher{}
	handler := NewServer(streams, WithAuthStore(auth), WithAuditStore(auth), WithServiceRegistryStore(auth), WithProfileStore(profiles), withManualDiscordTargetForTest(t, streams, stream.ID, "1001", "1002", "1003"), WithServiceDispatcher(dispatcher), WithSystemUpdateStore(updates))
	cookie, csrf := loginForTest(t, handler, "operator", "correct horse battery")
	body := `{"discord_config_id":"` + config.ID + `","encoder_input_url":"srt://source.example.com:9000"}`

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

type serviceAuthenticationBarrierStore struct {
	store.ServiceRegistryStore
	once          sync.Once
	authenticated chan struct{}
}

func (s *serviceAuthenticationBarrierStore) AuthenticateServiceToken(
	ctx context.Context,
	rawToken string,
	requiredScope string,
) (store.ServiceToken, error) {
	token, err := s.ServiceRegistryStore.AuthenticateServiceToken(
		ctx,
		rawToken,
		requiredScope,
	)
	if err == nil {
		s.once.Do(func() { close(s.authenticated) })
	}
	return token, err
}

func TestSystemUpdateClaimRejectsTokenRotatedAfterInitialAuthentication(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	registerServiceInstance(t, auth, "worker-reauth-claim", "worker")
	capabilities := centralUpdateCapabilitiesForTest(
		"host-reauth-claim",
		map[string]string{"worker-reauth-claim": "systemd"},
	)
	oldToken := registerSystemUpdateAgentForTest(
		t,
		auth,
		"updater-reauth-claim",
		capabilities,
	)
	services := &serviceAuthenticationBarrierStore{
		ServiceRegistryStore: auth,
		authenticated:        make(chan struct{}),
	}
	updates := store.NewMemorySystemUpdateStore()
	bindSystemUpdateExecutionHostForTest(t, updates, "host-reauth-claim", "updater-reauth-claim")
	server := NewServer(
		store.NewMemoryStreamStore(),
		WithAuthStore(auth),
		WithServiceRegistryStore(services),
		WithSystemUpdateStore(updates),
	)

	server.systemUpdateOperationMu.Lock()
	locked := true
	defer func() {
		if locked {
			server.systemUpdateOperationMu.Unlock()
		}
	}()
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		payload, err := json.Marshal(contracts.UpdateAgentClaimRequest{
			UpdaterID: "updater-reauth-claim", HostID: "host-reauth-claim",
			LeaseGeneration: 1, Fence: 1,
		})
		if err != nil {
			done <- httptest.NewRecorder()
			return
		}
		request := httptest.NewRequest(
			http.MethodPost,
			"/services/update-jobs/claim",
			bytes.NewReader(payload),
		)
		request.Header.Set("Authorization", "Bearer "+oldToken.RawToken)
		request.Header.Set(systemUpdateContractMajorHeader, "2")
		result := httptest.NewRecorder()
		server.ServeHTTP(result, request)
		done <- result
	}()
	select {
	case <-services.authenticated:
	case <-time.After(3 * time.Second):
		t.Fatal("claim did not complete its initial service-token authentication")
	}

	newToken, err := auth.RotateServiceToken(t.Context(), oldToken.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := auth.Heartbeat(t.Context(), newToken, store.ServiceHeartbeat{
		ServiceID:    "updater-reauth-claim",
		Status:       "online",
		Version:      "v1.0.1",
		Capabilities: capabilities,
	}); err != nil {
		t.Fatal(err)
	}
	job, _, err := updates.CreateSystemUpdateJob(
		t.Context(),
		store.CreateSystemUpdateJobParams{
			TargetID:          "worker-reauth-claim",
			TargetServiceType: "worker",
			AgentServiceID:    "updater-reauth-claim",
			ExecutionHostID:   "host-reauth-claim",
			DeploymentMode:    "systemd",
			CurrentVersion:    "v1.0.0",
			TargetVersion:     "v1.1.0",
			Strategy:          store.SystemUpdateStrategyWhenIdle,
			IdempotencyKey:    "reauth-claim",
			RequestedByUserID: "admin-01",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	server.systemUpdateOperationMu.Unlock()
	locked = false

	select {
	case result := <-done:
		if result.Code != http.StatusUnauthorized ||
			!strings.Contains(result.Body.String(), `"code":"invalid_service_token"`) {
			t.Fatalf("rotated-token claim status=%d body=%s", result.Code, result.Body.String())
		}
	case <-time.After(3 * time.Second):
		t.Fatal("claim did not finish after identity lock release")
	}
	active, err := updates.GetActiveSystemUpdateJob(t.Context(), job.TargetID)
	if err != nil {
		t.Fatal(err)
	}
	if active.Status != store.SystemUpdateStatusQueued || active.LeaseGeneration != 0 {
		t.Fatalf("pre-rotation request claimed post-rotation job: %#v", active)
	}
}

func TestSystemUpdateReportRejectsTokenRotatedAfterInitialAuthentication(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	capabilities := centralUpdateCapabilitiesForTest(
		"host-reauth-report",
		map[string]string{"worker-reauth-report": "systemd"},
	)
	oldToken := registerSystemUpdateAgentForTest(
		t,
		auth,
		"updater-reauth-report",
		capabilities,
	)
	updates := store.NewMemorySystemUpdateStore()
	bindSystemUpdateExecutionHostForTest(t, updates, "host-reauth-report", "updater-reauth-report")
	job, _, err := updates.CreateSystemUpdateJob(
		t.Context(),
		store.CreateSystemUpdateJobParams{
			TargetID:          "worker-reauth-report",
			TargetServiceType: "worker",
			AgentServiceID:    "updater-reauth-report",
			ExecutionHostID:   "host-reauth-report",
			DeploymentMode:    "systemd",
			CurrentVersion:    "v1.0.0",
			TargetVersion:     "v1.1.0",
			Strategy:          store.SystemUpdateStrategyWhenIdle,
			IdempotencyKey:    "reauth-report",
			RequestedByUserID: "admin-01",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = updates.ClaimSystemUpdateJob(
		t.Context(),
		"updater-reauth-report",
		"host-reauth-report",
		"",
		map[string]string{"worker-reauth-report": "systemd"},
		time.Now().UTC(),
		systemUpdateExecutionLeaseTTL,
	)
	if err != nil {
		t.Fatal(err)
	}
	services := &serviceAuthenticationBarrierStore{
		ServiceRegistryStore: auth,
		authenticated:        make(chan struct{}),
	}
	server := NewServer(
		store.NewMemoryStreamStore(),
		WithAuthStore(auth),
		WithServiceRegistryStore(services),
		WithSystemUpdateStore(updates),
	)
	reportBody := []byte(`{}`)

	server.systemUpdateOperationMu.Lock()
	locked := true
	defer func() {
		if locked {
			server.systemUpdateOperationMu.Unlock()
		}
	}()
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		request := httptest.NewRequest(
			http.MethodPost,
			"/services/update-jobs/"+job.ID+"/report",
			bytes.NewReader(reportBody),
		)
		request.Header.Set("Authorization", "Bearer "+oldToken.RawToken)
		request.Header.Set(systemUpdateContractMajorHeader, "2")
		result := httptest.NewRecorder()
		server.ServeHTTP(result, request)
		done <- result
	}()
	select {
	case <-services.authenticated:
	case <-time.After(3 * time.Second):
		t.Fatal("report did not complete its initial service-token authentication")
	}
	newToken, err := auth.RotateServiceToken(t.Context(), oldToken.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := auth.Heartbeat(t.Context(), newToken, store.ServiceHeartbeat{
		ServiceID:    "updater-reauth-report",
		Status:       "online",
		Version:      "v1.0.1",
		Capabilities: capabilities,
	}); err != nil {
		t.Fatal(err)
	}
	server.systemUpdateOperationMu.Unlock()
	locked = false

	select {
	case result := <-done:
		if result.Code != http.StatusUnauthorized ||
			!strings.Contains(result.Body.String(), `"code":"invalid_service_token"`) {
			t.Fatalf("rotated-token report status=%d body=%s", result.Code, result.Body.String())
		}
	case <-time.After(3 * time.Second):
		t.Fatal("report did not finish after identity lock release")
	}
	active, err := updates.GetActiveSystemUpdateJob(t.Context(), job.TargetID)
	if err != nil {
		t.Fatal(err)
	}
	if active.Status != store.SystemUpdateStatusClaimed {
		t.Fatalf("pre-rotation request reported post-rotation job: %#v", active)
	}
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
	bindSystemUpdateExecutionHostForTest(t, updates, "host-race", "updater-race")
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
	server := NewServer(streams, WithAuthStore(auth), WithAuditStore(auth), WithServiceRegistryStore(auth), WithProfileStore(profiles), withManualDiscordTargetForTest(t, streams, stream.ID, "1001", "1002", "1003"), WithServiceDispatcher(dispatcher), WithSystemUpdateStore(updates))
	cookie, csrf := loginForTest(t, server, "operator", "correct horse battery")
	body := `{"discord_config_id":"` + config.ID + `","encoder_input_url":"srt://source.example.com:9000"}`

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
		req := httptest.NewRequest(http.MethodPost, "/services/update-jobs/claim", strings.NewReader(`{"updater_id":"updater-race","host_id":"host-race","lease_generation":1,"fence":1}`))
		req.Header.Set("Authorization", "Bearer "+agentToken.RawToken)
		req.Header.Set(systemUpdateContractMajorHeader, "2")
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
	bindSystemUpdateExecutionHostForTest(t, updates, "host-panel", "updater-panel")
	job, _, err := updates.CreateSystemUpdateJob(t.Context(), store.CreateSystemUpdateJobParams{
		TargetID: "control-panel", TargetServiceType: "control_panel", AgentServiceID: "updater-panel", ExecutionHostID: "host-panel", DeploymentMode: "systemd", CurrentVersion: "v1.0.0", TargetVersion: "v1.1.0",
		Strategy: store.SystemUpdateStrategyWhenIdle, IdempotencyKey: "panel-live", RequestedByUserID: "admin-01",
	})
	if err != nil {
		t.Fatal(err)
	}
	server := NewServer(streams, WithAuthStore(auth), WithServiceRegistryStore(auth), WithSystemUpdateStore(updates))
	req := httptest.NewRequest(http.MethodPost, "/services/update-jobs/claim", strings.NewReader(`{"updater_id":"updater-panel","host_id":"host-panel","lease_generation":1,"fence":1}`))
	req.Header.Set("Authorization", "Bearer "+agentToken.RawToken)
	req.Header.Set(systemUpdateContractMajorHeader, "2")
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
	hostID := ""
	if statuses, ok := capabilities["host_statuses"].(map[string]any); ok {
		for candidate := range statuses {
			hostID = candidate
			break
		}
	}
	registration := validPullV2UpdateAgentRegistrationForTest(serviceID, serviceID, hostID, 1)
	registration.Version = "v1.0.0"
	registration.Capabilities = capabilities
	if _, err := auth.PrecreateService(t.Context(), token, registration); err != nil {
		t.Fatal(err)
	}
	if _, err := auth.RegisterService(t.Context(), token, store.ServiceRegistration{
		ServiceID: serviceID, ServiceType: "update_agent", ServiceName: serviceID,
		Version: "v1.0.0", Capabilities: capabilities,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := auth.Heartbeat(t.Context(), token, store.ServiceHeartbeat{ServiceID: serviceID, Status: "online", Version: "v1.0.0", Capabilities: capabilities}); err != nil {
		t.Fatal(err)
	}
	return token
}

func validPullV2UpdateAgentRegistrationForTest(serviceID, serviceName, executionHostID string, ownershipEpoch int64) store.ServiceRegistration {
	return store.ServiceRegistration{
		ServiceID:       serviceID,
		ServiceType:     "update_agent",
		ServiceName:     serviceName,
		TransportMode:   store.SystemUpdateTransportPullV2,
		ExecutionHostID: executionHostID,
		OwnershipEpoch:  ownershipEpoch,
	}
}

func bindSystemUpdateExecutionHostForTest(t *testing.T, updates *store.MemorySystemUpdateStore, hostID, agentServiceID string) {
	t.Helper()
	if _, err := updates.SwitchSystemUpdateExecutionHost(
		t.Context(), hostID, 0, store.SystemUpdateTransportPullV2, agentServiceID, 1,
	); err != nil {
		t.Fatal(err)
	}
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
	t.Run("registry_capabilities_remain_pinned", func(t *testing.T) {
		auth := store.NewMemoryAuthStore()
		token, err := auth.CreateServiceToken(t.Context(), "update_agent", []string{
			"service.register", "service.heartbeat", "service.config.read",
			"updates.claim", "updates.report", "updates.authorize",
		})
		if err != nil {
			t.Fatal(err)
		}
		registration := func(capabilities map[string]any) store.ServiceRegistration {
			return store.ServiceRegistration{
				ServiceID: "updater-pinned", ServiceType: "update_agent", ServiceName: "Updater",
				TransportMode: store.SystemUpdateTransportPullV2, ExecutionHostID: "host-pinned",
				Version: "v1.0.0", Capabilities: capabilities,
			}
		}
		encodeCapabilities := func(value map[string]any) []byte {
			t.Helper()
			encoded, err := json.Marshal(value)
			if err != nil {
				t.Fatal("capability snapshot encoding failed")
			}
			return encoded
		}

		// These legacy advertisement fields remain data, not target authority.
		pinned := map[string]any{
			"managed_targets":  []any{"worker-01"},
			"deployment_modes": map[string]any{"worker-01": "systemd"},
		}
		expanded := map[string]any{
			"managed_targets":  []any{"worker-01", "worker-02"},
			"deployment_modes": map[string]any{"worker-01": "systemd", "worker-02": "docker"},
		}
		replacement := map[string]any{
			"managed_targets":  []any{"worker-02"},
			"deployment_modes": map[string]any{"worker-02": "docker"},
		}
		// Freeze expectations before any store call; never derive them from post-state.
		wantPinned := encodeCapabilities(pinned)
		wantExpanded := encodeCapabilities(expanded)
		wantReplacement := encodeCapabilities(replacement)
		assertPersisted := func(phase string, wantReported []byte) {
			t.Helper()
			service, err := auth.GetService(t.Context(), "updater-pinned")
			if err != nil {
				t.Fatal(err)
			}
			if service.ServiceType != "update_agent" ||
				service.TransportMode != store.SystemUpdateTransportPullV2 ||
				service.ExecutionHostID != "host-pinned" || service.TokenID != token.ID {
				t.Fatalf("%s changed registered updater identity", phase)
			}
			if !bytes.Equal(encodeCapabilities(service.Capabilities), wantPinned) {
				t.Fatalf("%s changed pinned capabilities", phase)
			}
			if !bytes.Equal(encodeCapabilities(service.ReportedCapabilities), wantReported) {
				t.Fatalf("%s did not preserve the exact reported capabilities", phase)
			}
			modes, versions := approvedSystemUpdateAgentTargets(service)
			if len(modes) != 0 || len(versions) != 0 {
				t.Fatalf("%s authorized targets without policy: modes=%d versions=%d", phase, len(modes), len(versions))
			}
		}
		if _, err := auth.PrecreateService(t.Context(), token, registration(map[string]any{})); err != nil {
			t.Fatal(err)
		}
		if _, err := auth.RegisterService(t.Context(), token, registration(pinned)); err != nil {
			t.Fatal(err)
		}
		assertPersisted("first registration", wantPinned)
		if _, err := auth.Heartbeat(t.Context(), token, store.ServiceHeartbeat{
			ServiceID: "updater-pinned", Status: "online", Version: "v1.0.0", Capabilities: expanded,
		}); err != nil {
			t.Fatal(err)
		}
		assertPersisted("heartbeat", wantExpanded)
		reregistration := registration(replacement)
		reregistration.Version = "v1.0.1"
		if _, err := auth.RegisterService(t.Context(), token, reregistration); err != nil {
			t.Fatal(err)
		}
		assertPersisted("re-registration", wantReplacement)
	})

	t.Run("pull_v2_approval_requires_policy_and_current_evidence", func(t *testing.T) {
		for _, name := range []string{
			"valid_policy",
			"missing_policy",
			"extra_reported_target",
			"missing_reported_evidence",
		} {
			t.Run(name, func(t *testing.T) {
				now := time.Now().UTC()
				// Reuse the current fixture, including the local-listener correction.
				agent, service, policy := pullSystemUpdateTargetApprovalFixture(now)
				if agent.ServiceID != "updater-pull" || service.ServiceID != "worker-a" ||
					len(policy.Targets) != 1 || policy.Targets[0].ServiceID != "worker-a" {
					t.Fatal("canonical pull target fixture identity differs from this test")
				}
				servicesByID := map[string]store.RegisteredService{"worker-a": service}
				assertOnePolicyTarget := func(got map[string]systemUpdateApprovedTarget, ready bool, code, reachability string) {
					t.Helper()
					target, exists := got["worker-a"]
					if len(got) != 1 || !exists {
						t.Fatalf("policy target set differs: count=%d expected_target_present=%t", len(got), exists)
					}
					if !target.PolicyManaged || target.PolicyReady != ready || target.PolicyBlockedReason != code ||
						target.DeploymentMode != "systemd" || target.ServiceType != "worker" ||
						target.Host.HostID != "host-a" || target.Host.Reachability != reachability {
						t.Fatalf("policy target state differs: managed=%t ready=%t code=%q reachability=%q",
							target.PolicyManaged, target.PolicyReady, target.PolicyBlockedReason, target.Host.Reachability)
					}
				}
				// Every negative starts from a demonstrated non-empty, ready baseline.
				assertOnePolicyTarget(approvedSystemUpdateAgentTargetAssignmentsForPolicyWithHosts(
					agent, now, &policy, servicesByID,
				), true, "", "reachable")

				switch name {
				case "valid_policy":
					// The baseline above is the positive case.
				case "missing_policy":
					got := approvedSystemUpdateAgentTargetAssignmentsForPolicyWithHosts(agent, now, nil, servicesByID)
					modes, versions := approvedSystemUpdateAgentTargets(agent)
					if len(got) != 0 || len(modes) != 0 || len(versions) != 0 {
						t.Fatalf("policy-free approval was not empty: targets=%d modes=%d versions=%d",
							len(got), len(modes), len(versions))
					}
				case "extra_reported_target":
					// Add a second registered-service snapshot and make every report for it look valid.
					// Only the authoritative policy intentionally excludes worker-b.
					extraService := service
					extraService.ServiceID = "worker-b"
					servicesByID["worker-b"] = extraService
					for _, key := range []string{
						"target_availability", "target_availability_codes", "reported_ports", "port_drift",
						"reported_service_types", "reported_deployment_modes",
						"reported_executor_policy_revisions", "reported_executor_policy_sha256",
						"reported_config_revisions", "reported_config_sha256",
					} {
						values, ok := agent.ReportedCapabilities[key].(map[string]any)
						if !ok {
							t.Fatalf("canonical report map missing: %s", key)
						}
						value, exists := values["worker-a"]
						if !exists {
							t.Fatalf("canonical report value missing: %s", key)
						}
						values["worker-b"] = value
					}
					agent.Capabilities = map[string]any{
						"managed_targets":  []any{"worker-a", "worker-b"},
						"deployment_modes": map[string]any{"worker-a": "systemd", "worker-b": "systemd"},
					}
					agent.ReportedCapabilities["managed_targets"] = []any{"worker-a", "worker-b"}
					agent.ReportedCapabilities["deployment_modes"] = map[string]any{"worker-a": "systemd", "worker-b": "systemd"}
					assertOnePolicyTarget(approvedSystemUpdateAgentTargetAssignmentsForPolicyWithHosts(
						agent, now, &policy, servicesByID,
					), true, "", "reachable")
				case "missing_reported_evidence":
					availability, ok := agent.ReportedCapabilities["target_availability"].(map[string]any)
					if !ok {
						t.Fatal("canonical availability report map missing")
					}
					delete(availability, "worker-a")
					// A configured but unverified target remains visible and blocked.
					// Do not replace this with an empty-map assertion or force Available=false.
					assertOnePolicyTarget(approvedSystemUpdateAgentTargetAssignmentsForPolicyWithHosts(
						agent, now, &policy, servicesByID,
					), false, "updater_policy_mismatch", "unknown")
				default:
					t.Fatalf("unhandled approval case: %s", name)
				}
			})
		}
	})

	t.Run("non_updater_heartbeat_remains_mutable", func(t *testing.T) {
		auth := store.NewMemoryAuthStore()
		workerToken, err := auth.CreateServiceToken(t.Context(), "worker", []string{"service.register", "service.heartbeat"})
		if err != nil {
			t.Fatal(err)
		}
		registration := store.ServiceRegistration{
			ServiceID: "worker-legacy", ServiceType: "worker", ServiceName: "Worker",
			PublicURL: "https://worker.example.com", Version: "v1.0.0", Capabilities: map[string]any{},
		}
		if _, err := auth.PrecreateService(t.Context(), workerToken, registration); err != nil {
			t.Fatal(err)
		}
		if _, err := auth.RegisterService(t.Context(), workerToken, registration); err != nil {
			t.Fatal(err)
		}
		if _, err := auth.Heartbeat(t.Context(), workerToken, store.ServiceHeartbeat{
			ServiceID: "worker-legacy", Status: "online", Capabilities: map[string]any{"feature": "heartbeat-updated"},
		}); err != nil {
			t.Fatal(err)
		}
		worker, err := auth.GetService(t.Context(), "worker-legacy")
		if err != nil {
			t.Fatal(err)
		}
		if worker.Capabilities["feature"] != "heartbeat-updated" ||
			worker.ReportedCapabilities["feature"] != "heartbeat-updated" {
			t.Fatal("non-updater heartbeat capability behavior changed")
		}
	})
}
