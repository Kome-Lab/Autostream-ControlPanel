package httpapi

import (
	"bytes"
	"encoding/json"
	"github.com/example/autostream-control-panel/internal/store"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

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
