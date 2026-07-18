package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/example/autostream-control-panel/internal/store"
)

func TestSystemUpdateCentralHostReachabilityIsIndependentFromUpdaterHeartbeat(t *testing.T) {
	t.Setenv("AUTOSTREAM_NODE_HEARTBEAT_OFFLINE_AFTER", "3m")
	now := time.Date(2026, 7, 19, 1, 0, 0, 0, time.UTC)
	freshHeartbeat := now.Add(-time.Minute)
	staleHeartbeat := now.Add(-4 * time.Minute)
	verified := map[string]serviceUpdateInfoResponse{
		"worker": {LatestVersion: "v1.1.0", UpdateCheckSource: "github", ManifestVerified: true},
	}
	tests := []struct {
		name             string
		heartbeat        time.Time
		hostStatus       string
		hostCheckedAt    time.Time
		hostCode         string
		wantUpdater      bool
		wantReachability string
		wantBlocked      string
		wantEligible     bool
	}{
		{name: "online updater and reachable host", heartbeat: freshHeartbeat, hostStatus: "reachable", hostCheckedAt: now.Add(-30 * time.Second), wantUpdater: true, wantReachability: "reachable", wantEligible: true},
		{name: "online updater and unreachable host", heartbeat: freshHeartbeat, hostStatus: "unreachable", hostCheckedAt: now.Add(-30 * time.Second), hostCode: "ssh_timeout", wantUpdater: true, wantReachability: "unreachable", wantBlocked: "target_unreachable"},
		{name: "online updater and stale host observation", heartbeat: freshHeartbeat, hostStatus: "reachable", hostCheckedAt: now.Add(-systemUpdateHostReachabilityTTL - time.Second), wantUpdater: true, wantReachability: "unknown", wantBlocked: "target_reachability_unknown"},
		{name: "offline updater and freshly reachable host", heartbeat: staleHeartbeat, hostStatus: "reachable", hostCheckedAt: now.Add(-30 * time.Second), wantReachability: "reachable", wantBlocked: "updater_offline"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			agent := centralHostTestAgent(test.heartbeat, test.hostStatus, test.hostCheckedAt.Format(time.RFC3339Nano), test.hostCode, "Main Host")
			assignments, updaters, hosts := systemUpdateAgentTopology([]store.RegisteredService{agent}, now)
			if len(updaters) != 1 || updaters[0].UpdaterID != agent.ServiceID || updaters[0].Online != test.wantUpdater {
				t.Fatalf("updaters = %#v", updaters)
			}
			if len(hosts) != 1 || hosts[0].HostID != "host-main" || hosts[0].UpdaterID != agent.ServiceID || hosts[0].Reachability != test.wantReachability {
				t.Fatalf("hosts = %#v", hosts)
			}
			if hosts[0].Code != test.hostCode {
				t.Fatalf("host code = %q, want %q", hosts[0].Code, test.hostCode)
			}
			assignment, ok := assignments["worker-01"]
			if !ok || assignment.Available != test.wantUpdater || assignment.HostReachability != test.wantReachability {
				t.Fatalf("assignment = %#v, exists=%v", assignment, ok)
			}
			target := buildSystemUpdateTarget("worker-01", "worker", "Worker 01", "v1.0.0", "", false, assignment, verified)
			if target.HostID != "host-main" || target.UpdaterOnline != test.wantUpdater || target.BlockedReason != test.wantBlocked || target.Eligible != test.wantEligible {
				t.Fatalf("target = %#v", target)
			}
		})
	}
}

func TestSystemUpdateTargetHostCapabilitiesArePinnedAndSanitized(t *testing.T) {
	now := time.Date(2026, 7, 19, 1, 0, 0, 0, time.UTC)
	freshHeartbeat := now.Add(-time.Minute)

	t.Run("legacy per-host mapping is never eligible", func(t *testing.T) {
		agent := centralHostTestAgent(freshHeartbeat, "reachable", now.Format(time.RFC3339Nano), "", "Main Host")
		delete(agent.Capabilities, "target_hosts")
		delete(agent.ReportedCapabilities, "target_hosts")
		assignments, updaters, hosts := systemUpdateAgentTopology([]store.RegisteredService{agent}, now)
		if len(assignments) != 0 || len(hosts) != 0 || len(updaters) != 1 {
			t.Fatalf("legacy updater topology assignments=%#v updaters=%#v hosts=%#v", assignments, updaters, hosts)
		}
	})

	t.Run("configured and reported target hosts must match", func(t *testing.T) {
		agent := centralHostTestAgent(freshHeartbeat, "reachable", now.Format(time.RFC3339Nano), "", "Main Host")
		agent.ReportedCapabilities["target_hosts"] = map[string]any{"worker-01": "host-other"}
		assignments, updaters, hosts := systemUpdateAgentTopology([]store.RegisteredService{agent}, now)
		if len(assignments) != 0 || len(hosts) != 0 || len(updaters) != 1 {
			t.Fatalf("mismatched topology assignments=%#v updaters=%#v hosts=%#v", assignments, updaters, hosts)
		}
	})

	t.Run("unapproved hosts and unsafe metadata are not exposed", func(t *testing.T) {
		agent := centralHostTestAgent(freshHeartbeat, "unreachable", now.Format(time.RFC3339Nano), "internal_detail", "bad\nhost name")
		agent.ReportedCapabilities["host_statuses"] = map[string]any{"host-main": "unreachable", "host-unapproved": "reachable"}
		agent.ReportedCapabilities["host_checked_at"] = map[string]any{"host-main": now.Format(time.RFC3339Nano), "host-unapproved": now.Format(time.RFC3339Nano)}
		agent.ReportedCapabilities["host_codes"] = map[string]any{"host-main": "internal_detail", "host-unapproved": "ssh_timeout"}
		agent.ReportedCapabilities["host_names"] = map[string]any{"host-main": "bad\nhost name", "host-unapproved": "Secret Host"}
		assignments, _, hosts := systemUpdateAgentTopology([]store.RegisteredService{agent}, now)
		if len(assignments) != 1 || len(hosts) != 1 {
			t.Fatalf("topology assignments=%#v hosts=%#v", assignments, hosts)
		}
		if hosts[0].HostID != "host-main" || hosts[0].Name != "host-main" || hosts[0].Code != "" || hosts[0].Reachability != "unreachable" {
			t.Fatalf("sanitized host = %#v", hosts[0])
		}
	})

	t.Run("host display name follows the public contract limit", func(t *testing.T) {
		agent := centralHostTestAgent(freshHeartbeat, "reachable", now.Format(time.RFC3339Nano), "", strings.Repeat("x", 192))
		_, _, hosts := systemUpdateAgentTopology([]store.RegisteredService{agent}, now)
		if len(hosts) != 1 || hosts[0].Name != "host-main" {
			t.Fatalf("overlong host name was exposed: %#v", hosts)
		}
	})

	for _, test := range []struct {
		name       string
		checkedAt  string
		status     string
		wantCheck  bool
		wantStatus string
	}{
		{name: "malformed timestamp", checkedAt: "not-a-time", status: "reachable", wantStatus: "unknown"},
		{name: "future timestamp", checkedAt: now.Add(systemUpdateHostClockSkew + time.Second).Format(time.RFC3339Nano), status: "reachable", wantStatus: "unknown"},
		{name: "unknown status", checkedAt: now.Format(time.RFC3339Nano), status: "healthy", wantCheck: true, wantStatus: "unknown"},
	} {
		t.Run(test.name, func(t *testing.T) {
			agent := centralHostTestAgent(freshHeartbeat, test.status, test.checkedAt, "ssh_timeout", "Main Host")
			_, _, hosts := systemUpdateAgentTopology([]store.RegisteredService{agent}, now)
			if len(hosts) != 1 || hosts[0].Reachability != test.wantStatus || (hosts[0].CheckedAt != nil) != test.wantCheck || hosts[0].Code != "" {
				t.Fatalf("host = %#v", hosts)
			}
		})
	}

	t.Run("invalid pinned host identifier is rejected", func(t *testing.T) {
		agent := centralHostTestAgent(freshHeartbeat, "reachable", now.Format(time.RFC3339Nano), "", "Main Host")
		agent.Capabilities["target_hosts"] = map[string]any{"worker-01": "host-main\nother"}
		agent.ReportedCapabilities["target_hosts"] = map[string]any{"worker-01": "host-main\nother"}
		assignments, _, hosts := systemUpdateAgentTopology([]store.RegisteredService{agent}, now)
		if len(assignments) != 0 || len(hosts) != 0 {
			t.Fatalf("invalid host was approved: assignments=%#v hosts=%#v", assignments, hosts)
		}
	})
}

func TestSystemUpdateHostClaimAllowsOnlyReachableNewJobsAndUnavailableRecovery(t *testing.T) {
	now := time.Now().UTC()
	auth := store.NewMemoryAuthStore()
	registerCentralHostTestTarget(t, auth, "worker-a")
	registerCentralHostTestTarget(t, auth, "worker-b")
	reachableCapabilities := centralHostIntegrationCapabilities(now, "reachable", "reachable")
	agentToken := registerCentralHostTestAgent(t, auth, reachableCapabilities)
	updates := store.NewMemorySystemUpdateStore()
	jobA := createCentralHostTestJob(t, updates, "worker-a", "host-a", "host-job-a")
	jobB := createCentralHostTestJob(t, updates, "worker-b", "host-b", "host-job-b")
	server := NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth), WithServiceRegistryStore(auth), WithSystemUpdateStore(updates))

	claimB := claimCentralHostTestJob(t, server, agentToken, `{"service_id":"updater-central","host_id":"host-b"}`, http.StatusOK)
	if claimB.Job.ID != jobB.ID || claimB.Job.ID == jobA.ID || claimB.Job.ExecutionHostID != "host-b" {
		t.Fatalf("host-b claim = %#v", claimB)
	}

	unreachableCapabilities := centralHostIntegrationCapabilities(time.Now().UTC(), "unreachable", "unreachable")
	if _, err := auth.Heartbeat(t.Context(), agentToken, store.ServiceHeartbeat{ServiceID: "updater-central", Status: "online", Version: "v1.7.0", Capabilities: unreachableCapabilities}); err != nil {
		t.Fatal(err)
	}
	claimCentralHostTestJob(t, server, agentToken, `{"service_id":"updater-central","host_id":"host-a"}`, http.StatusNoContent)
	queuedA, err := updates.GetActiveSystemUpdateJob(t.Context(), jobA.TargetID)
	if err != nil || queuedA.Status != store.SystemUpdateStatusQueued {
		t.Fatalf("unreachable new job changed state: job=%#v err=%v", queuedA, err)
	}

	recoveryBody := `{"service_id":"updater-central","host_id":"host-b","active_job_id":"` + jobB.ID + `"}`
	recovered := claimCentralHostTestJob(t, server, agentToken, recoveryBody, http.StatusOK)
	if recovered.Job.ID != jobB.ID || !recovered.RecoveryRequired || recovered.Job.Status != store.SystemUpdateStatusReconciling || recovered.Job.ExecutionHostID != "host-b" {
		t.Fatalf("unreachable recovery claim = %#v", recovered)
	}

	authorize := func(hostID string) *httptest.ResponseRecorder {
		body, err := json.Marshal(map[string]any{
			"service_id": "updater-central", "host_id": hostID, "lease_token": recovered.LeaseToken,
			"lease_generation": recovered.LeaseGeneration, "target_id": recovered.Job.TargetID,
			"target_version": recovered.Job.TargetVersion, "deployment_mode": recovered.Job.DeploymentMode,
		})
		if err != nil {
			t.Fatal(err)
		}
		req := httptest.NewRequest(http.MethodPost, "/services/update-jobs/"+jobB.ID+"/authorize", strings.NewReader(string(body)))
		req.Header.Set("Authorization", "Bearer "+agentToken.RawToken)
		response := httptest.NewRecorder()
		server.ServeHTTP(response, req)
		return response
	}
	if response := authorize(""); response.Code != http.StatusGone || !strings.Contains(response.Body.String(), "legacy_system_update_authorization_disabled") {
		t.Fatalf("legacy host-less authorization = %d %s", response.Code, response.Body.String())
	}
	if response := authorize("host-b"); response.Code != http.StatusGone || !strings.Contains(response.Body.String(), "legacy_system_update_authorization_disabled") {
		t.Fatalf("legacy host-bound authorization = %d %s", response.Code, response.Body.String())
	}
}

func TestSystemUpdatePublicTargetOmitsEmptyHostID(t *testing.T) {
	payload, err := json.Marshal(systemUpdateTargetResponse{TargetID: "unassigned", ServiceType: "worker", Name: "Unassigned"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(payload), `"host_id"`) {
		t.Fatalf("empty host_id violates public schema: %s", payload)
	}
}

func centralHostTestAgent(heartbeat time.Time, hostStatus, checkedAt, code, hostName string) store.RegisteredService {
	configured := map[string]any{
		"managed_targets":  []any{"worker-01"},
		"deployment_modes": map[string]any{"worker-01": "systemd"},
		"target_hosts":     map[string]any{"worker-01": "host-main"},
	}
	reported := map[string]any{
		"managed_targets":  []any{"worker-01"},
		"deployment_modes": map[string]any{"worker-01": "systemd"},
		"target_hosts":     map[string]any{"worker-01": "host-main"},
		"deployed_versions": map[string]any{
			"worker-01": "v1.0.0",
		},
		"host_statuses":   map[string]any{"host-main": hostStatus},
		"host_checked_at": map[string]any{"host-main": checkedAt},
		"host_codes":      map[string]any{"host-main": code},
		"host_names":      map[string]any{"host-main": hostName},
	}
	return store.RegisteredService{
		ServiceID: "updater-central", ServiceType: "update_agent", ServiceName: "Central Updater", Status: "online",
		Version: "v1.7.0", ReportedVersion: "v1.7.0", LastHeartbeatAt: &heartbeat,
		Capabilities: configured, ReportedCapabilities: reported,
	}
}

func centralHostIntegrationCapabilities(checkedAt time.Time, hostAStatus, hostBStatus string) map[string]any {
	return map[string]any{
		"managed_targets":  []any{"worker-a", "worker-b"},
		"deployment_modes": map[string]any{"worker-a": "systemd", "worker-b": "systemd"},
		"target_hosts":     map[string]any{"worker-a": "host-a", "worker-b": "host-b"},
		"deployed_versions": map[string]any{
			"worker-a": "v1.0.0", "worker-b": "v1.0.0",
		},
		"host_statuses": map[string]any{"host-a": hostAStatus, "host-b": hostBStatus},
		"host_checked_at": map[string]any{
			"host-a": checkedAt.UTC().Format(time.RFC3339Nano), "host-b": checkedAt.UTC().Format(time.RFC3339Nano),
		},
		"host_codes": map[string]any{"host-a": "ssh_timeout", "host-b": "ssh_timeout"},
		"host_names": map[string]any{"host-a": "Host A", "host-b": "Host B"},
	}
}

func registerCentralHostTestAgent(t *testing.T, auth *store.MemoryAuthStore, capabilities map[string]any) store.ServiceToken {
	t.Helper()
	token, err := auth.CreateServiceToken(t.Context(), "update_agent", []string{"service.register", "service.heartbeat", "updates.claim", "updates.report", "updates.authorize"})
	if err != nil {
		t.Fatal(err)
	}
	registration := store.ServiceRegistration{ServiceID: "updater-central", ServiceType: "update_agent", ServiceName: "Central Updater", PublicURL: "https://updater-central.example.com", Version: "v1.7.0", Capabilities: capabilities}
	if _, err := auth.PrecreateService(t.Context(), token, registration); err != nil {
		t.Fatal(err)
	}
	if _, err := auth.RegisterService(t.Context(), token, registration); err != nil {
		t.Fatal(err)
	}
	if _, err := auth.Heartbeat(t.Context(), token, store.ServiceHeartbeat{ServiceID: registration.ServiceID, Status: "online", Version: registration.Version, Capabilities: capabilities}); err != nil {
		t.Fatal(err)
	}
	return token
}

func registerCentralHostTestTarget(t *testing.T, auth *store.MemoryAuthStore, serviceID string) {
	t.Helper()
	token, err := auth.CreateServiceToken(t.Context(), "worker", []string{"service.register", "service.heartbeat"})
	if err != nil {
		t.Fatal(err)
	}
	registration := store.ServiceRegistration{ServiceID: serviceID, ServiceType: "worker", ServiceName: serviceID, PublicURL: "https://" + serviceID + ".example.com", Version: "v1.0.0", Capabilities: map[string]any{}}
	if _, err := auth.PrecreateService(t.Context(), token, registration); err != nil {
		t.Fatal(err)
	}
	if _, err := auth.RegisterService(t.Context(), token, registration); err != nil {
		t.Fatal(err)
	}
	if _, err := auth.Heartbeat(t.Context(), token, store.ServiceHeartbeat{ServiceID: serviceID, Status: "online", Version: "v1.0.0"}); err != nil {
		t.Fatal(err)
	}
}

func createCentralHostTestJob(t *testing.T, updates *store.MemorySystemUpdateStore, targetID, hostID, idempotencyKey string) store.SystemUpdateJob {
	t.Helper()
	job, _, err := updates.CreateSystemUpdateJob(t.Context(), store.CreateSystemUpdateJobParams{
		TargetID: targetID, TargetServiceType: "worker", AgentServiceID: "updater-central", ExecutionHostID: hostID,
		DeploymentMode: "systemd", CurrentVersion: "v1.0.0", TargetVersion: "v1.1.0", Strategy: store.SystemUpdateStrategyMaintenance,
		IdempotencyKey: idempotencyKey, RequestedByUserID: "admin-01", RequestedByUsername: "admin",
	})
	if err != nil {
		t.Fatal(err)
	}
	return job
}

func claimCentralHostTestJob(t *testing.T, server *Server, token store.ServiceToken, body string, wantStatus int) store.SystemUpdateClaim {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/services/update-jobs/claim", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token.RawToken)
	response := httptest.NewRecorder()
	server.ServeHTTP(response, req)
	if response.Code != wantStatus {
		t.Fatalf("claim status = %d %s, want %d", response.Code, response.Body.String(), wantStatus)
	}
	if wantStatus == http.StatusNoContent {
		return store.SystemUpdateClaim{}
	}
	var claim store.SystemUpdateClaim
	if err := json.NewDecoder(response.Body).Decode(&claim); err != nil {
		t.Fatal(err)
	}
	return claim
}
