import assert from "node:assert/strict";
import test from "node:test";
import { mockGet, mockPost } from "./system-updates-fixture.mts";
import { systemUpdatePortReconfigureRequest, portIdentity, portPlan, requestSystemUpdatePortReconfigureWithRecovery, SystemUpdateRequestAmbiguousError, systemUpdatePortReconfigureResultLabel, systemUpdateDockerPortReconfigureRequest, portBefore, systemUpdatePortRequestMatchesJob, systemUpdateJobFromResponse, systemUpdatePortJobResultLabel, normalizeSystemUpdatesResponse } from "./system-updates-fixture.mts";


export function registerPortRecoveryCases() {


test("port reconfiguration response loss never resends POST and remains visibly ambiguous", async () => {
  const request = systemUpdatePortReconfigureRequest({
    targetID: "worker-main",
    currentLocalListenPort: 8084, currentAdvertisedPort: 8084,
    newLocalListenPort: 18084, mode: "local_only", ...portIdentity,
    expectedEndpointRevision: 7,
    idempotencyKey: "port-response-loss",
  });
  let postCalls = 0;
  const committed = {
    id: "job-port-response-loss",
    idempotency_key: request.idempotency_key,
    target_id: request.target_id,
    target_type: "worker",
    operation: "port_reconfigure" as const,
    port_reconfigure: portPlan(), ownership_epoch: portIdentity.fence,
    status: "succeeded",
    created_at: "2026-07-28T00:00:00Z",
    updated_at: "2026-07-28T00:00:01Z",
  };
  const recovered = await requestSystemUpdatePortReconfigureWithRecovery(
    request,
    async () => {
      postCalls += 1;
      throw new Error("response_lost");
    },
    async () => [committed],
  );
  assert.equal(recovered.id, committed.id);
  assert.equal(postCalls, 1);
  await assert.rejects(
    () => requestSystemUpdatePortReconfigureWithRecovery(
      { ...request, idempotency_key: "port-still-ambiguous" },
      async () => {
        postCalls += 1;
        throw new Error("network_lost");
      },
      async () => [],
    ),
    SystemUpdateRequestAmbiguousError,
  );
  assert.equal(postCalls, 2);
  assert.equal(systemUpdatePortReconfigureResultLabel("applied"), "適用済み");
  assert.equal(systemUpdatePortReconfigureResultLabel("rolled_back"), "復旧済み");
  assert.equal(systemUpdatePortReconfigureResultLabel("unchanged"), "変更不要");
  assert.equal(systemUpdatePortReconfigureResultLabel("rollback_failed"), "復旧未完了／要再照合");
});

test("Docker port recovery matches the entire mapping identity", async () => {
  const currentMapping = {
    mode: "docker" as const,
    advertised_port: 8084,
    published_host_ip: "127.0.0.1",
    published_port: 18084,
    container_port: 8080,
    health_port: 18084,
    config_revision: 4,
    state: "applied" as const,
  };
  const request = systemUpdateDockerPortReconfigureRequest({
    targetID: "worker-main",
    currentMapping,
    newAdvertisedPort: 443,
    newPublishedPort: 28084,
    newContainerPort: 18080,
    expectedEndpointRevision: 7,
    idempotencyKey: "docker-response-loss",
    mode: "local_and_advertised", ...portIdentity, appliedConfigRevision: 4,
  });
  const committed = {
    id: "job-docker-response-loss",
    idempotency_key: request.idempotency_key,
    target_id: request.target_id,
    target_type: "worker",
    deployment_mode: "docker",
    operation: "port_reconfigure" as const,
    ownership_epoch: portIdentity.fence,
    port_reconfigure: (() => {
      const plan = portPlan({ ...portBefore, config_revision: 4, local_listen_port: 18084 }, 28084, 443);
      plan.target!.docker = { published_host_ip: "127.0.0.1", published_port: 28084, container_port: 18080, health_port: 28084,
        compose_policy_sha256: `sha256:${"7".repeat(64)}`, compose_revision: 5, version_env_sha256: `sha256:${"8".repeat(64)}`,
        image_id: `sha256:${"9".repeat(64)}`, repository_digest: `sha256:${"a".repeat(64)}` };
      return plan;
    })(),
    status: "queued",
    created_at: "2026-07-28T00:00:00Z",
    updated_at: "2026-07-28T00:00:00Z",
  };
  assert.equal(systemUpdatePortRequestMatchesJob(request, committed), true);
  assert.equal(systemUpdatePortRequestMatchesJob({ ...request, new_container_port: 18081 }, committed), false);
  let calls = 0;
  const recovered = await requestSystemUpdatePortReconfigureWithRecovery(
    request,
    async () => {
      calls += 1;
      throw new Error("response_lost");
    },
    async () => [committed],
  );
  assert.equal(recovered.id, committed.id);
  assert.equal(calls, 1);
});

test("ST-PORT accepted results survive JSON normalization without inferring success or losing recovery evidence", () => {
  const observedAt = "2026-09-05T17:00:00.123Z";
  const observation = { policy_disk_verified: true, policy_memory_verified: true, agent_projection_verified: true, listener_verified: true, observed_at: observedAt };
  for (const result of ["applied", "unchanged", "rolled_back"] as const) {
    const plan = portPlan(portBefore, result === "unchanged" ? portBefore.local_listen_port : 18084);
    const expected = result === "applied" ? plan.target! : result === "unchanged" ? plan.before! : plan.rollback!;
    const accepted = { result, observed_snapshot_id: expected.snapshot_id, observed_snapshot_sha256: expected.snapshot_sha256,
      observed_config_revision: expected.config_revision, observed_config_sha256: expected.config_sha256,
      observed_executor_policy_revision: expected.executor_policy_revision, observed_executor_policy_sha256: expected.executor_policy_sha256, observation };
    const wire = { id: `job-${result}`, target_id: "worker-main", target_type: "worker", operation: "port_reconfigure",
      status: result === "rolled_back" ? "rolled_back" : "succeeded", port_reconfigure: plan, port_result: accepted,
      created_at: observedAt, updated_at: observedAt };
    const job = systemUpdateJobFromResponse(JSON.parse(JSON.stringify(wire)));
    assert.deepEqual(JSON.parse(JSON.stringify(job.port_result)), accepted);
    assert.equal(job.port_result?.observation.observed_at, observedAt);
    assert.equal(job.port_reconfigure?.result, undefined);
    assert.equal(systemUpdatePortJobResultLabel(job), systemUpdatePortReconfigureResultLabel(result));
    assert.equal(systemUpdateJobFromResponse({ ...wire, port_result: undefined }).port_result, undefined);
    assert.equal(systemUpdateJobFromResponse({ ...wire, port_result: { ...accepted, observation: { ...observation, policy_memory_verified: false } } }).port_result, undefined);
    assert.equal(systemUpdateJobFromResponse({ ...wire, port_result: { ...accepted, observed_config_revision: expected.config_revision + 1 } }).port_result, undefined);
    assert.equal(systemUpdateJobFromResponse({ ...wire, status: "failed" }).port_result, undefined);
    if (result === "unchanged") {
      assert.equal(systemUpdateJobFromResponse({ ...wire, port_reconfigure: { ...plan, target: { ...plan.target!, local_listen_port: 18085 } } }).port_result, undefined);
      assert.equal(systemUpdateJobFromResponse({ ...wire, port_result: { ...accepted, result: "applied" } }).port_result, undefined);
    }
  }
  const failure = { result: "rollback_failed", observation: { ...observation, listener_verified: false } };
  const held = systemUpdateJobFromResponse({ id: "held", target_id: "worker-main", target_type: "worker", operation: "port_reconfigure",
    status: "failed", port_reconfigure: portPlan(), port_result: failure, last_recovery_observation: failure, recovery_required: true,
    created_at: observedAt, updated_at: observedAt });
  assert.equal(held.port_result, undefined);
  assert.deepEqual(held.last_recovery_observation, failure);
  assert.equal(systemUpdatePortJobResultLabel(held), "復旧未完了／要再照合");
});

test("ST-PORT mixed local and advertised inputs keep public HTTPS separate and reject partial Docker requests", () => {
  const local = systemUpdatePortReconfigureRequest({ targetID: "worker-main", currentLocalListenPort: 18081, newLocalListenPort: 18084,
    currentAdvertisedPort: 443, mode: "local_only", ...portIdentity, expectedEndpointRevision: 7, idempotencyKey: "local" });
  assert.equal(local.mode, "local_only");
  assert.equal("new_advertised_port" in local, false);
  assert.equal("new_port" in local, false);
  const combined = systemUpdatePortReconfigureRequest({ targetID: "worker-main", currentLocalListenPort: 18081, newLocalListenPort: 18084,
    currentAdvertisedPort: 443, newAdvertisedPort: 8443, mode: "local_and_advertised", ...portIdentity, expectedEndpointRevision: 7, idempotencyKey: "combined" });
  assert.equal(combined.new_advertised_port, 8443);
  assert.equal(combined.desired_revision, portBefore.config_revision + 1);
  assert.throws(() => systemUpdatePortReconfigureRequest({ targetID: "worker-main", currentLocalListenPort: 18081, newLocalListenPort: 18084,
    currentAdvertisedPort: 443, newAdvertisedPort: 8443, mode: "local_only", ...portIdentity, expectedEndpointRevision: 7, idempotencyKey: "mixed" }), /invalid_system_update_port_mode/);
  const mapping = { mode: "docker" as const, advertised_port: 443, published_host_ip: "127.0.0.1", published_port: 18081,
    container_port: 8080, health_port: 18081, config_revision: 11, state: "applied" as const };
  assert.throws(() => systemUpdateDockerPortReconfigureRequest({ targetID: "worker-main", currentMapping: mapping, mode: "local_and_advertised",
    newAdvertisedPort: 8443, newPublishedPort: 18081, newContainerPort: 8080, ...portIdentity, expectedEndpointRevision: 7, idempotencyKey: "advertised-docker" }), /system_update_advertised_only_unsupported/);
  assert.throws(() => systemUpdateDockerPortReconfigureRequest({ targetID: "worker-main", currentMapping: mapping, mode: "local_only",
    newPublishedPort: 18084, newContainerPort: undefined as unknown as number, ...portIdentity, expectedEndpointRevision: 7, idempotencyKey: "partial" }), /invalid_container_port/);
});

test("ST-PORT demo request produces the same explicit mode and snapshot shape", () => {
  const state = normalizeSystemUpdatesResponse(mockGet("/system-updates"));
  const target = state.targets.find((value) => value.target_id === "observability-main")!;
  const request = systemUpdateDockerPortReconfigureRequest({ targetID: target.target_id, currentMapping: target.port_mapping!, mode: "local_only",
    newPublishedPort: 28090, newContainerPort: 8080, expectedSnapshotID: target.port_policy_snapshot_id!,
    appliedConfigRevision: target.applied_config_revision!, fence: target.ownership_epoch!, expectedEndpointRevision: target.endpoint_revision!, idempotencyKey: "demo-port-v2" });
  const job = systemUpdateJobFromResponse(mockPost("/system-updates", request));
  assert.equal(systemUpdatePortRequestMatchesJob(request, job), true);
  assert.equal(job.port_reconfigure?.target?.local_listen_port, 28090);
  assert.equal(job.port_reconfigure?.target?.advertised_port, 443);
  assert.equal(job.port_reconfigure?.old_port, undefined);
  assert.equal(job.port_result, undefined);
  assert.equal(systemUpdatePortJobResultLabel(job), "確認中");
});
}
