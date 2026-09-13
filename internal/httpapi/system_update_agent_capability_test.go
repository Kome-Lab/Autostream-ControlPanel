package httpapi

import (
	"bytes"
	"encoding/json"
	"github.com/example/autostream-control-panel/internal/store"
	"testing"
	"time"
)

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
