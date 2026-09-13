import assert from "node:assert/strict";
import test from "node:test";
import type { SystemUpdateAgentStatus, WorkerNode } from "../src/types/domain.ts";
import { baseTarget, systemUpdateStrategyForTarget, systemUpdateRequest, systemUpdatePortReconfigureEligibility, portBefore, systemUpdatePortReconfigureRequest, portIdentity, portWireIdentity, systemUpdateDockerPortReconfigureRequest, systemUpdateTargetOperationEligibility, systemUpdateSoftwareOperationEligibility, acquireSystemUpdateTargetRequestLock, isSystemUpdateEndpointRevisionConflict } from "./system-updates-fixture.mts";


export function registerPortValidationCases() {


test("an active stream is always queued with the when_idle strategy", () => {
  const target = { ...baseTarget, current_stream_id: "stream-live" };

  assert.equal(systemUpdateStrategyForTarget(target), "when_idle");
  assert.deepEqual(systemUpdateRequest(target, "request-1"), {
    target_id: "worker-main",
    strategy: "when_idle",
    idempotency_key: "request-1",
  });
  assert.equal(systemUpdateStrategyForTarget(baseTarget), "maintenance");
  assert.equal(systemUpdateStrategyForTarget({ ...baseTarget, busy: false, current_stream_id: "stale-stream" }), "maintenance");
});

test("port reconfiguration is fail-closed and keeps the legacy software request unchanged", () => {
  const updater: SystemUpdateAgentStatus = {
    updater_id: "host-agent-main",
    name: "Host Agent",
    status: "online",
    online: true,
    version: "v2.0.0",
    transport_mode: "pull_v2",
    execution_host_id: "host-main",
    ownership_epoch: 3,
    desired_revision: 9,
    applied_revision: 9,
    policy_status: "applied",
  };
  const node: WorkerNode = {
    id: "worker-main",
    service_id: "worker-main",
    service_type: "worker",
    service_name: "Main Worker",
    status: "online",
    desired_endpoint: { host: "worker.example.test", port: 18084, ssl_enabled: true, public_url: "https://worker.example.test:18084" },
    applied_endpoint: { host: "worker.example.test", port: 8084, ssl_enabled: true, public_url: "https://worker.example.test:8084" },
    reported_endpoint: { host: "127.0.0.1", port: 8084, ssl_enabled: false, public_url: "http://127.0.0.1:8084" },
    endpoint_revision: 7,
    endpoint_status: "applied",
  };

  assert.deepEqual(systemUpdateRequest(baseTarget, "legacy-software-request"), {
    target_id: "worker-main",
    strategy: "maintenance",
    idempotency_key: "legacy-software-request",
  });
  const ready = systemUpdatePortReconfigureEligibility({
    target: baseTarget,
    updater,
    node,
    latestJob: undefined,
    requestState: "idle",
  });
  assert.deepEqual(ready, {
    ready: true,
    reason: "",
    deploymentMode: "systemd",
    currentPort: 8084,
    endpointRevision: 7,
    currentLocalListenPort: 8084, snapshotID: portBefore.snapshot_id, appliedConfigRevision: 11, fence: 5,
    dockerMapping: undefined,
  });
  assert.deepEqual(systemUpdatePortReconfigureRequest({
    targetID: baseTarget.target_id,
    currentLocalListenPort: ready.currentLocalListenPort!,
    newLocalListenPort: 18084,
    currentAdvertisedPort: ready.currentPort!, mode: "local_only", ...portIdentity,
    expectedEndpointRevision: ready.endpointRevision,
    idempotencyKey: "port-worker-main-7",
  }), {
    operation: "port_reconfigure",
    target_id: "worker-main",
    ...portWireIdentity, mode: "local_only", new_local_listen_port: 18084,
    expected_endpoint_revision: 7,
    idempotency_key: "port-worker-main-7",
  });

  const dockerMapping = {
    mode: "docker" as const,
    advertised_port: 8084,
    published_host_ip: "127.0.0.1",
    published_port: 18084,
    container_port: 8080,
    health_port: 18084,
    config_revision: 11,
    state: "applied" as const,
    reported_at: "2026-07-28T00:00:00Z",
  };
  const dockerTarget = { ...baseTarget, deployment_mode: "docker", port_mapping: dockerMapping, local_listen_port: dockerMapping.published_port };
  const dockerReady = systemUpdatePortReconfigureEligibility({ target: dockerTarget, updater, node, requestState: "idle" });
  assert.deepEqual(dockerReady, {
    ready: true,
    reason: "",
    deploymentMode: "docker",
    currentPort: 8084,
    endpointRevision: 7,
    currentLocalListenPort: 18084, snapshotID: portBefore.snapshot_id, appliedConfigRevision: 11, fence: 5,
    dockerMapping,
  });
  assert.deepEqual(systemUpdateDockerPortReconfigureRequest({
    targetID: "worker-main",
    currentMapping: dockerMapping,
    newAdvertisedPort: 443,
    mode: "local_and_advertised", ...portIdentity,
    newPublishedPort: 28084,
    newContainerPort: 18080,
    expectedEndpointRevision: 7,
    idempotencyKey: "docker-port-worker-main-7",
  }), {
    operation: "port_reconfigure",
    target_id: "worker-main",
    new_advertised_port: 443,
    ...portWireIdentity, mode: "local_and_advertised",
    new_published_port: 28084,
    new_container_port: 18080,
    expected_endpoint_revision: 7,
    idempotency_key: "docker-port-worker-main-7",
  });
  assert.equal(systemUpdatePortReconfigureEligibility({
    target: { ...dockerTarget, port_mapping: { ...dockerMapping, state: "drifted" } },
    updater,
    node,
    requestState: "idle",
  }).reason, "docker_mapping_drifted");
  assert.equal(systemUpdatePortReconfigureEligibility({
    target: { ...dockerTarget, port_mapping: { ...dockerMapping, published_host_ip: "0.0.0.0" } },
    updater,
    node,
    requestState: "idle",
  }).reason, "docker_mapping_unavailable");
  assert.equal(systemUpdatePortReconfigureEligibility({
    target: { ...dockerTarget, port_mapping: undefined },
    updater,
    node,
    requestState: "idle",
  }).reason, "docker_mapping_unavailable");
  assert.equal(systemUpdatePortReconfigureEligibility({ target: { ...baseTarget, deployment_mode: "binary" }, updater, node, requestState: "idle" }).reason, "unsupported_deployment");
  assert.equal(systemUpdatePortReconfigureEligibility({
    target: baseTarget,
    updater: { ...updater, transport_mode: "ssh_v1" } as unknown as SystemUpdateAgentStatus,
    node,
    requestState: "idle",
  }).reason, "unsupported_transport");
  assert.equal(systemUpdatePortReconfigureEligibility({ target: { ...baseTarget, target_type: "control_panel" }, updater, node, requestState: "idle" }).reason, "unsupported_target");
  assert.equal(systemUpdatePortReconfigureEligibility({ target: { ...baseTarget, busy: true }, updater, node, requestState: "idle" }).reason, "target_busy");
  assert.equal(systemUpdatePortReconfigureEligibility({ target: baseTarget, updater, node, requestState: "pending" }).reason, "request_pending");
  assert.equal(systemUpdatePortReconfigureEligibility({ target: baseTarget, updater, node, requestState: "ambiguous" }).reason, "request_ambiguous");
  assert.equal(systemUpdatePortReconfigureEligibility({
    target: { ...baseTarget, eligible_operations: ["software_update"], operation_blocked_reasons: { port_reconfigure: "system_update_port_reconfigure_not_ready" } },
    updater,
    node,
    requestState: "idle",
  }).reason, "system_update_port_reconfigure_not_ready");
  assert.equal(systemUpdatePortReconfigureEligibility({
    target: { ...baseTarget, eligible_operations: undefined },
    updater,
    node,
    requestState: "idle",
  }).reason, "operation_eligibility_unavailable");
  assert.equal(systemUpdatePortReconfigureEligibility({
    target: baseTarget,
    updater,
    node,
    latestJob: { id: "job-active", target_id: "worker-main", target_type: "worker", status: "applying", created_at: "", updated_at: "" },
    requestState: "idle",
  }).reason, "active_job");
  assert.equal(systemUpdatePortReconfigureEligibility({
    target: baseTarget,
    updater,
    node,
    latestJob: { id: "job-recovery", target_id: "worker-main", target_type: "worker", status: "failed", recovery_required: true, created_at: "", updated_at: "" },
    requestState: "idle",
  }).reason, "recovery_required");
  assert.equal(systemUpdatePortReconfigureRequest({
    targetID: "worker-main",
    currentLocalListenPort: 8084, currentAdvertisedPort: 443,
    newLocalListenPort: 8084, mode: "local_only", ...portIdentity,
    expectedEndpointRevision: 7,
    idempotencyKey: "same-port",
  }).desired_revision, portBefore.config_revision);
  assert.throws(() => systemUpdatePortReconfigureRequest({
    targetID: "worker-main", currentLocalListenPort: 8084, newLocalListenPort: 8084,
    currentAdvertisedPort: 443, newAdvertisedPort: 8443, mode: "local_and_advertised", ...portIdentity,
    expectedEndpointRevision: 7, idempotencyKey: "advertised-only",
  }), /system_update_advertised_only_unsupported/);
  assert.throws(() => systemUpdatePortReconfigureRequest({
    targetID: "worker-main",
    currentLocalListenPort: 8084, currentAdvertisedPort: 443,
    newLocalListenPort: 1023, mode: "local_only", ...portIdentity,
    expectedEndpointRevision: 7,
    idempotencyKey: "privileged-port",
  }), /invalid_service_port/);
  assert.equal(systemUpdateDockerPortReconfigureRequest({
    targetID: "worker-main",
    currentMapping: dockerMapping,
    newAdvertisedPort: 8084,
    mode: "local_and_advertised", ...portIdentity,
    newPublishedPort: 18084,
    newContainerPort: 8080,
    expectedEndpointRevision: 7,
    idempotencyKey: "unchanged-docker",
  }).desired_revision, portBefore.config_revision);
  assert.throws(() => systemUpdateDockerPortReconfigureRequest({
    targetID: "worker-main",
    currentMapping: dockerMapping,
    newAdvertisedPort: 443,
    newPublishedPort: 1023,
    newContainerPort: 8080,
    expectedEndpointRevision: 7,
    idempotencyKey: "privileged-published",
    mode: "local_and_advertised", ...portIdentity,
  }), /invalid_published_port/);
});

test("operation eligibility is strict when reported while legacy software eligibility stays compatible", () => {
  assert.deepEqual(
    systemUpdateTargetOperationEligibility(baseTarget, "software_update"),
    { ready: true, reason: "" },
  );
  assert.deepEqual(
    systemUpdateTargetOperationEligibility(baseTarget, "port_reconfigure"),
    { ready: true, reason: "" },
  );
  assert.deepEqual(
    systemUpdateTargetOperationEligibility(
      {
        ...baseTarget,
        eligible_operations: ["software_update"],
        operation_blocked_reasons: { port_reconfigure: "updater_policy_pending" },
      },
      "port_reconfigure",
    ),
    { ready: false, reason: "updater_policy_pending" },
  );
  assert.deepEqual(
    systemUpdateTargetOperationEligibility(
      { ...baseTarget, eligible_operations: [] },
      "software_update",
    ),
    { ready: false, reason: "system_update_software_update_not_ready" },
  );
  assert.deepEqual(
    systemUpdateTargetOperationEligibility(
      { ...baseTarget, eligible_operations: undefined },
      "software_update",
    ),
    { ready: false, reason: "operation_eligibility_unavailable" },
  );
  assert.deepEqual(
    systemUpdateSoftwareOperationEligibility({ ...baseTarget, eligible_operations: undefined }),
    { ready: true, reason: "" },
  );
  assert.deepEqual(
    systemUpdateSoftwareOperationEligibility({
      ...baseTarget,
      eligible: false,
      eligible_operations: undefined,
      blocked_reason: "updater_policy_pending",
    }),
    { ready: false, reason: "updater_policy_pending" },
  );
  assert.deepEqual(
    systemUpdateSoftwareOperationEligibility({ ...baseTarget, eligible_operations: [] }),
    { ready: false, reason: "system_update_software_update_not_ready" },
  );
});

test("port request identity is single-flight and stale endpoint revisions require a refresh", () => {
  const activeTargets = new Set<string>();
  assert.equal(acquireSystemUpdateTargetRequestLock(activeTargets, "worker-main"), true);
  assert.equal(acquireSystemUpdateTargetRequestLock(activeTargets, "worker-main"), false);
  assert.equal(acquireSystemUpdateTargetRequestLock(activeTargets, " worker-main "), false);
  activeTargets.delete("worker-main");
  assert.equal(acquireSystemUpdateTargetRequestLock(activeTargets, "worker-main"), true);
  assert.equal(acquireSystemUpdateTargetRequestLock(activeTargets, ""), false);

  assert.equal(isSystemUpdateEndpointRevisionConflict({
    status: 409,
    code: "system_update_endpoint_revision_conflict",
  }), true);
  assert.equal(isSystemUpdateEndpointRevisionConflict({
    status: 409,
    code: "service_port_reserved",
  }), false);
  assert.equal(isSystemUpdateEndpointRevisionConflict(new Error("system_update_endpoint_revision_conflict")), false);
});
}
