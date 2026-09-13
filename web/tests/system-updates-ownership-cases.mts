import assert from "node:assert/strict";
import test from "node:test";
import type { SystemUpdateAgentStatus, UpdaterSettings } from "../src/types/domain.ts";
import { pullUpdaterOwnershipActivationEligibility, pullUpdaterOwnershipActivationRequest, normalizePullUpdaterOwnershipActivationResponse, normalizeSystemUpdatesResponse, normalizeUpdaterSettingsResponse, pullUpdaterOwnershipDeactivationEligibility, pullUpdaterOwnershipDeactivationRequest, pullOwnershipMutationFenceAdvanced, normalizePullUpdaterOwnershipDeactivationResponse } from "./system-updates-fixture.mts";


export function registerOwnershipCases() {


test("pull ownership activation uses only server-fenced settings and readiness fields", () => {
  const digest = `sha256:${"a".repeat(64)}`;
  const updater: SystemUpdateAgentStatus = {
    updater_id: "host-agent-main",
    name: "Host Agent",
    status: "online",
    online: true,
    version: "v2.0.0",
    transport_mode: "pull_v2",
    execution_host_id: "host-main",
    ownership_epoch: 0,
  };
  const settings: UpdaterSettings = {
    updater_id: updater.updater_id,
    revision: 9,
    projection_revision: 4,
    local_executor_policy_revision: 6,
    transport_mode: "pull_v2",
    execution_host_id: "host-main",
    execution_host_ownership: {
      transport_mode: "pull_v2",
      agent_service_id: "",
      ownership_epoch: 12,
      policy_revision: 8,
    },
    pull_activation: {
      ready: true,
      status: "online",
      last_heartbeat_at: "2026-07-28T00:00:00Z",
      observe_only: true,
      update_executor: true,
      mutation_enabled: false,
      recovery_pending: false,
      reported_ownership_epoch: 0,
      reported_projection_revision: 4,
    },
    local_executor_policy_sha256: digest,
    api: { bind_host: "127.0.0.1", host: "127.0.0.1", port: 8090, ssl_enabled: false },
    poll_interval_seconds: 15,
    heartbeat_interval_seconds: 30,
    hosts: [],
    targets: [{ target_id: "worker-main", service_id: "worker-main", host_id: "host-main", service_type: "worker", deployment_mode: "systemd" }],
    github_token_configured: false,
  };
  const eligibility = pullUpdaterOwnershipActivationEligibility({
    updater,
    settings,
    jobs: [],
    requestState: "idle",
  });
  assert.deepEqual(eligibility, { ready: true, reason: "" });
  const request = pullUpdaterOwnershipActivationRequest(updater, settings);
  assert.deepEqual(request, {
    expected_execution_host_id: "host-main",
    expected_ownership_epoch: 12,
    expected_source_policy_revision: 9,
    expected_projection_revision: 4,
    expected_local_executor_policy_revision: 6,
    expected_local_executor_policy_sha256: digest,
  });
  assert.equal(pullUpdaterOwnershipActivationEligibility({ updater: { ...updater, online: false }, settings, jobs: [], requestState: "idle" }).reason, "observer_offline");
  assert.equal(pullUpdaterOwnershipActivationEligibility({
    updater,
    settings: { ...settings, pull_activation: { ...settings.pull_activation!, recovery_pending: true, ready: false } },
    jobs: [],
    requestState: "idle",
  }).reason, "recovery_required");
  assert.equal(pullUpdaterOwnershipActivationEligibility({
    updater,
    settings,
    jobs: [{ id: "active", target_id: "worker-main", target_type: "worker", status: "applying", created_at: "", updated_at: "" }],
    requestState: "idle",
  }).reason, "active_job");
  assert.equal(pullUpdaterOwnershipActivationEligibility({ updater, settings, jobs: [], requestState: "ambiguous" }).reason, "request_ambiguous");
  assert.equal(pullUpdaterOwnershipActivationEligibility({
    updater,
    settings: { ...settings, pull_activation: { ...settings.pull_activation!, update_executor: false, ready: false } },
    jobs: [],
    requestState: "idle",
  }).reason, "observer_not_ready");
  assert.throws(
    () => pullUpdaterOwnershipActivationRequest(updater, { ...settings, projection_revision: undefined }),
    /pull_ownership_contract_unavailable/,
  );

  assert.deepEqual(normalizePullUpdaterOwnershipActivationResponse({
    updater_id: "host-agent-main",
    execution_host_id: "host-main",
    transport_mode: "pull_v2",
    agent_service_id: "host-agent-main",
    ownership_epoch: 13,
    source_policy_revision: 9,
    projection_revision: 4,
    local_executor_policy_revision: 6,
    local_executor_policy_sha256: digest,
  }), {
    updater_id: "host-agent-main",
    execution_host_id: "host-main",
    transport_mode: "pull_v2",
    agent_service_id: "host-agent-main",
    ownership_epoch: 13,
    source_policy_revision: 9,
    projection_revision: 4,
    local_executor_policy_revision: 6,
    local_executor_policy_sha256: digest,
  });
  assert.throws(() => normalizePullUpdaterOwnershipActivationResponse({ ownership_epoch: 13 }), /invalid_pull_ownership_activation_response/);
});

test("wire normalization preserves pull ownership epoch zero and rejects legacy ownership", () => {
  const digest = `sha256:${"a".repeat(64)}`;
  const response = normalizeSystemUpdatesResponse({
    updaters: [
      {
        updater_id: "host-agent-main",
        name: "Host Agent",
        status: "online",
        online: true,
        version: "v2.0.0",
        transport_mode: "pull_v2",
        execution_host_id: "host-main",
        ownership_epoch: 0,
      },
      {
        updater_id: "updater-central",
        name: "Central Updater",
        status: "online",
        online: true,
        version: "v2.0.0",
        transport_mode: "ssh_v1",
      },
    ],
  });
  const pullUpdater = response.updaters[0];
  const unsupportedUpdater = response.updaters[1];
  assert.equal(pullUpdater.ownership_epoch, 0);
  assert.equal("ownership_epoch" in pullUpdater, true);
  assert.equal(unsupportedUpdater.ownership_epoch, undefined);
  assert.equal("ownership_epoch" in unsupportedUpdater, false);
  assert.equal(unsupportedUpdater.transport_mode, undefined);

  const settings = normalizeUpdaterSettingsResponse({
    updater_id: "host-agent-main",
    revision: 9,
    projection_revision: 4,
    local_executor_policy_revision: 6,
    local_executor_policy_sha256: digest,
    transport_mode: "pull_v2",
    execution_host_id: "host-main",
    execution_host_ownership: {
      transport_mode: "pull_v2",
      agent_service_id: "",
      ownership_epoch: 12,
      policy_revision: 8,
    },
    pull_activation: {
      ready: true,
      status: "online",
      last_heartbeat_at: "2026-08-04T00:00:00Z",
      observe_only: true,
      update_executor: true,
      mutation_enabled: false,
      recovery_pending: false,
      reported_ownership_epoch: 0,
      reported_projection_revision: 4,
    },
    targets: [],
  });
  assert.deepEqual(
    pullUpdaterOwnershipActivationEligibility({
      updater: pullUpdater,
      settings,
      jobs: [],
      requestState: "idle",
    }),
    { ready: true, reason: "" },
  );
  assert.equal(
    pullUpdaterOwnershipActivationRequest(pullUpdater, settings).expected_ownership_epoch,
    12,
  );
});

test("pull ownership release is visible only for the exact active owner and validates the observer response", () => {
  const digest = `sha256:${"a".repeat(64)}`;
  const updater: SystemUpdateAgentStatus = {
    updater_id: "host-agent-main",
    name: "Host Agent",
    status: "online",
    online: true,
    version: "v2.0.0",
    transport_mode: "pull_v2",
    execution_host_id: "host-main",
    ownership_epoch: 13,
  };
  const settings: UpdaterSettings = {
    updater_id: updater.updater_id,
    revision: 9,
    projection_revision: 4,
    local_executor_policy_revision: 6,
    transport_mode: "pull_v2",
    execution_host_id: "host-main",
    execution_host_ownership: {
      transport_mode: "pull_v2",
      agent_service_id: updater.updater_id,
      ownership_epoch: 13,
      policy_revision: 4,
    },
    pull_activation: {
      ready: false,
      status: "online",
      last_heartbeat_at: "2026-07-28T00:00:00Z",
      observe_only: false,
      update_executor: true,
      mutation_enabled: true,
      recovery_pending: false,
      reported_ownership_epoch: 13,
      reported_projection_revision: 4,
    },
    local_executor_policy_sha256: digest,
    api: { bind_host: "127.0.0.1", host: "127.0.0.1", port: 8090, ssl_enabled: false },
    poll_interval_seconds: 15,
    heartbeat_interval_seconds: 30,
    hosts: [],
    targets: [{ target_id: "worker-main", service_id: "worker-main", host_id: "host-main", service_type: "worker", deployment_mode: "systemd" }],
    github_token_configured: false,
  };

  assert.deepEqual(pullUpdaterOwnershipDeactivationEligibility({
    updater,
    settings,
    jobs: [],
    requestState: "idle",
  }), { ready: true, reason: "" });
  assert.deepEqual(pullUpdaterOwnershipDeactivationRequest(updater, settings), {
    expected_execution_host_id: "host-main",
    expected_ownership_epoch: 13,
    expected_source_policy_revision: 9,
    expected_projection_revision: 4,
    expected_local_executor_policy_revision: 6,
    expected_local_executor_policy_sha256: digest,
  });
  assert.equal(pullUpdaterOwnershipDeactivationEligibility({
    updater,
    settings: {
      ...settings,
      execution_host_ownership: { ...settings.execution_host_ownership!, agent_service_id: "" },
    },
    jobs: [],
    requestState: "idle",
  }).reason, "pull_rollback_contract_unavailable");
  assert.equal(pullUpdaterOwnershipDeactivationEligibility({
    updater: { ...updater, ownership_epoch: 12 },
    settings,
    jobs: [],
    requestState: "idle",
  }).reason, "pull_rollback_contract_unavailable");
  assert.equal(pullUpdaterOwnershipDeactivationEligibility({
    updater,
    settings,
    jobs: [{ id: "active", target_id: "worker-main", target_type: "worker", status: "applying", created_at: "", updated_at: "" }],
    requestState: "idle",
  }).reason, "active_job");
  assert.equal(pullUpdaterOwnershipDeactivationEligibility({
    updater,
    settings: { ...settings, pull_activation: { ...settings.pull_activation!, recovery_pending: true } },
    jobs: [],
    requestState: "idle",
  }).reason, "recovery_required");
  assert.equal(pullUpdaterOwnershipDeactivationEligibility({
    updater,
    settings,
    jobs: [],
    requestState: "ambiguous",
  }).reason, "request_ambiguous");
  const deactivationAttempt = pullUpdaterOwnershipDeactivationRequest(updater, settings);
  const releasedSettings: UpdaterSettings = {
    ...settings,
    execution_host_ownership: {
      ...settings.execution_host_ownership!,
      transport_mode: "pull_v2",
      agent_service_id: updater.updater_id,
      ownership_epoch: 14,
    },
  };
  assert.equal(
    pullOwnershipMutationFenceAdvanced(deactivationAttempt, releasedSettings),
    true,
  );
  assert.equal(
    pullOwnershipMutationFenceAdvanced(deactivationAttempt, {
      ...releasedSettings,
      execution_host_ownership: {
        ...releasedSettings.execution_host_ownership!,
        transport_mode: "pull_v2",
        agent_service_id: updater.updater_id,
        ownership_epoch: 15,
      },
    }),
    true,
    "a resolved ambiguous attempt must not become ambiguous after the reverse transition",
  );
  assert.equal(
    pullOwnershipMutationFenceAdvanced(deactivationAttempt, {
      ...releasedSettings,
      execution_host_id: "host-other",
    }),
    false,
  );

  assert.deepEqual(normalizePullUpdaterOwnershipDeactivationResponse({
    updater_id: updater.updater_id,
    execution_host_id: "host-main",
    transport_mode: "pull_v2",
    agent_service_id: updater.updater_id,
    ownership_epoch: 14,
    agent_ownership_epoch: 0,
    source_policy_revision: 9,
    projection_revision: 4,
    local_executor_policy_revision: 6,
    local_executor_policy_sha256: digest,
  }), {
    updater_id: updater.updater_id,
    execution_host_id: "host-main",
    transport_mode: "pull_v2",
    agent_service_id: updater.updater_id,
    ownership_epoch: 14,
    agent_ownership_epoch: 0,
    source_policy_revision: 9,
    projection_revision: 4,
    local_executor_policy_revision: 6,
    local_executor_policy_sha256: digest,
  });
  assert.throws(() => normalizePullUpdaterOwnershipDeactivationResponse({
    updater_id: updater.updater_id,
    execution_host_id: "host-main",
    transport_mode: "ssh_v1",
    agent_service_id: updater.updater_id,
    ownership_epoch: 14,
    agent_ownership_epoch: 0,
    source_policy_revision: 9,
    projection_revision: 4,
    local_executor_policy_revision: 6,
    local_executor_policy_sha256: digest,
  }), /invalid_pull_ownership_deactivation_response/);
});
}
