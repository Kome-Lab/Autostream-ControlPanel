package httpapi

import (
	"bytes"
	"encoding/json"
	contracts "github.com/example/autostream-contracts/pkg/contracts"
	"github.com/example/autostream-control-panel/internal/store"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
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
