import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";

import { auditActionLabel } from "../src/lib/audit-action.ts";
import {
  buildBootstrapEnvelopeAAD,
  encryptBootstrapCredentials,
} from "../src/lib/bootstrap-envelope.ts";
import {
  acquireSystemUpdateTargetRequestLock,
  activeUpdaterHostBootstrapStatus,
  applyUpdaterSettingsTargetSelection,
  applyUpdaterSettingsTargetPatch,
  compareSystemUpdateVersions,
  isSystemUpdateEndpointRevisionConflict,
  isControlPanelUpdateTarget,
  isSystemUpdateJobActive,
  isSystemUpdateJobCancellable,
  isUpdaterHostBootstrapBulkCandidate,
  isUpdaterHostBootstrapJobActive,
  isUpdaterPolicyHostID,
  firstUnusedUpdaterSettingsTarget,
  normalizeUpdaterHostBootstrapJobsResponse,
  normalizeUpdaterSettingsResponse,
  normalizeSystemUpdatesResponse,
  normalizePullUpdaterOwnershipActivationResponse,
  normalizePullUpdaterOwnershipDeactivationResponse,
  pullUpdaterOwnershipActivationEligibility,
  pullUpdaterOwnershipActivationRequest,
  pullUpdaterOwnershipDeactivationEligibility,
  pullUpdaterOwnershipDeactivationRequest,
  pullOwnershipMutationFenceAdvanced,
  recoverUpdaterHostBootstrapRequest,
  requestSystemUpdatePortReconfigureWithRecovery,
  requestSystemUpdateWithRecovery,
  requestUpdaterHostBootstrapWithRecovery,
  runSystemUpdatesSequentially,
  systemUpdateDeploymentLabel,
  systemUpdateDockerPortReconfigureRequest,
  systemUpdateErrorMessage,
  systemUpdateConnectivity,
  systemUpdateHostReachabilityLabel,
  systemUpdateHostReachabilityMessage,
  systemUpdateJobStatusLabel,
  systemUpdateMayDisconnectPanel,
  systemUpdatePolicyErrorMessage,
  systemUpdateJobFromResponse,
  systemUpdateUpdaterPolicyState,
  normalizeUpdaterSettingsTargetDatabaseName,
  normalizeUpdaterSettingsTargetLocalListenPort,
  systemUpdateProgress,
  systemUpdatePortReconfigureEligibility,
  systemUpdatePortReconfigureRequest,
  systemUpdatePortReconfigureResultLabel,
  systemUpdatePortRequestMatchesJob,
  systemUpdateRequest,
  systemUpdateSoftwareOperationEligibility,
  systemUpdateStrategyForTarget,
  systemUpdateTargetOperationEligibility,
  systemUpdateTargetBlockedReason,
  updaterSettingsTargetRequiresDatabase,
  updaterSettingsTargetRequiresLocalListenPort,
  updaterSettingsTargetOptions,
  SystemUpdateRequestAmbiguousError,
  updaterHostBootstrapConfirmationContext,
  updaterHostBootstrapEligibility,
  updaterHostBootstrapEligibilityMessage,
  updaterHostBootstrapRequestIdentity,
  UpdaterHostBootstrapRequestAmbiguousError,
} from "../src/lib/system-updates.ts";
import type {
  SystemUpdateAgentStatus,
  SystemUpdateHostStatus,
  SystemUpdateTarget,
  UpdaterSettings,
  UpdaterSettingsHost,
  UpdaterSettingsTarget,
  WorkerNode,
} from "../src/types/domain.ts";
import { mockGet, mockPost, mockPut } from "../src/features/mock-data.ts";
import {
  canRegenerateNodeConfigureToken,
  canIssueNodeConfiguration,
  canRotateNodeRuntimeToken,
} from "../src/lib/node-configuration.ts";
import {
  buildNodeRegistrationRequest,
  isExecutionHostID,
  isServicePort,
  nodeEndpointState,
  nodeEndpointStatusPresentation,
  nodeRegistrationDraftValid,
  nodeServiceEndpointURL,
} from "../src/lib/node-registration.ts";

const baseTarget: SystemUpdateTarget = {
  target_id: "worker-main",
  target_type: "worker",
  name: "Main Worker",
  host_id: "host-main",
  current_version: "v1.0.0",
  latest_version: "v1.1.0",
  update_available: true,
  deployment_mode: "systemd",
  updater_id: "updater-main",
  updater_online: true,
  eligible: true,
  eligible_operations: ["software_update", "port_reconfigure"],
};

function toBase64URL(bytes: Uint8Array) {
  let binary = "";
  for (const byte of bytes) binary += String.fromCharCode(byte);
  return btoa(binary).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/g, "");
}

function fromBase64URL(value: string) {
  const padded = value.replace(/-/g, "+").replace(/_/g, "/").padEnd(Math.ceil(value.length / 4) * 4, "=");
  return Uint8Array.from(atob(padded), (character) => character.charCodeAt(0));
}

test("bootstrap envelope uses canonical AAD and P-256 ECDH AES-GCM without plaintext fields", async () => {
  const receiver = await crypto.subtle.generateKey({ name: "ECDH", namedCurve: "P-256" }, true, ["deriveBits"]);
  const receiverPublicKey = toBase64URL(new Uint8Array(await crypto.subtle.exportKey("raw", receiver.publicKey)));
  const context = {
    updaterID: "updater-main",
    expectedRevision: 7,
    jobID: "019f-bootstrap-job",
    hostIDs: ["host-z", "host-a"],
  };
  const credentials = {
    administrator_user: "autostream-admin",
    private_key: "-----BEGIN OPENSSH PRIVATE KEY-----\nsecret\n-----END OPENSSH PRIVATE KEY-----",
    passphrase: "one-time-passphrase",
  };

  assert.equal(
    buildBootstrapEnvelopeAAD(context),
    '{"version":1,"updater_id":"updater-main","policy_revision":7,"job_id":"019f-bootstrap-job","host_ids":["host-a","host-z"]}',
  );

  const envelope = await encryptBootstrapCredentials(receiverPublicKey, context, credentials);
  assert.equal(envelope.version, 1);
  assert.doesNotMatch(JSON.stringify(envelope), /autostream-admin|OPENSSH|one-time-passphrase/);

  const ephemeralPublicKey = await crypto.subtle.importKey(
    "raw",
    fromBase64URL(envelope.ephemeral_public_key),
    { name: "ECDH", namedCurve: "P-256" },
    false,
    [],
  );
  const sharedSecret = await crypto.subtle.deriveBits(
    { name: "ECDH", public: ephemeralPublicKey },
    receiver.privateKey,
    256,
  );
  const hkdfKey = await crypto.subtle.importKey("raw", sharedSecret, "HKDF", false, ["deriveKey"]);
  const contentKey = await crypto.subtle.deriveKey(
    {
      name: "HKDF",
      hash: "SHA-256",
      salt: new Uint8Array(0),
      info: new TextEncoder().encode("autostream-bootstrap-envelope-v1"),
    },
    hkdfKey,
    { name: "AES-GCM", length: 256 },
    false,
    ["decrypt"],
  );
  const plaintext = await crypto.subtle.decrypt(
    {
      name: "AES-GCM",
      iv: fromBase64URL(envelope.nonce),
      additionalData: new TextEncoder().encode(buildBootstrapEnvelopeAAD(context)),
    },
    contentKey,
    fromBase64URL(envelope.ciphertext),
  );
  assert.deepEqual(JSON.parse(new TextDecoder().decode(plaintext)), {
    administrator_user: credentials.administrator_user,
    private_key: toBase64URL(new TextEncoder().encode(credentials.private_key)),
    passphrase: toBase64URL(new TextEncoder().encode(credentials.passphrase)),
  });
});

test("bootstrap envelope matches the Go WebCrypto interoperability vector", async () => {
  const recipientPrivate = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAE";
  const recipientPublic = fromBase64URL("BGsX0fLhLEJH-Lzm5WOkQPJ3A32BLeszoPShOUXYmMKWT-NC4v4af5uO5-tKfA-eFivOM1drMV7Oy7ZAaDe_UfU");
  const recipientKey = await crypto.subtle.importKey(
    "jwk",
    {
      kty: "EC",
      crv: "P-256",
      x: toBase64URL(recipientPublic.slice(1, 33)),
      y: toBase64URL(recipientPublic.slice(33, 65)),
      d: recipientPrivate,
      ext: true,
      key_ops: ["deriveBits"],
    },
    { name: "ECDH", namedCurve: "P-256" },
    false,
    ["deriveBits"],
  );
  const ephemeralPublicKey = await crypto.subtle.importKey(
    "raw",
    fromBase64URL("BHzyexiNA09-ilI4AwS1GsPAiWnid_IbNaYLSPxHZpl4B3dVENuO0EApPZrGn3Qw27p9reY86YIpngS3nSJ4c9E"),
    { name: "ECDH", namedCurve: "P-256" },
    false,
    [],
  );
  const sharedSecret = await crypto.subtle.deriveBits(
    { name: "ECDH", public: ephemeralPublicKey },
    recipientKey,
    256,
  );
  const hkdfKey = await crypto.subtle.importKey("raw", sharedSecret, "HKDF", false, ["deriveKey"]);
  const contentKey = await crypto.subtle.deriveKey(
    {
      name: "HKDF",
      hash: "SHA-256",
      salt: new Uint8Array(0),
      info: new TextEncoder().encode("autostream-bootstrap-envelope-v1"),
    },
    hkdfKey,
    { name: "AES-GCM", length: 256 },
    false,
    ["decrypt"],
  );
  const context = {
    updaterID: "updater-01",
    expectedRevision: 7,
    jobID: "bootstrap-job-01",
    hostIDs: ["host-b", "host-a"],
  };
  const plaintext = await crypto.subtle.decrypt(
    {
      name: "AES-GCM",
      iv: fromBase64URL("AAECAwQFBgcICQoL"),
      additionalData: new TextEncoder().encode(buildBootstrapEnvelopeAAD(context)),
    },
    contentKey,
    fromBase64URL("2KTE0tK-dlqNjJhoI4r7bcqaKhpQksriceJVF6BZYOGFQfKoOJEiSzNIJVCzxYmwMLD9ozGPidtYQA9R1aOJndP3rJQ4ViWbW8wc4KIWmG6iPwe6nQsATMHRef1y2XFVuY5KmQM3d2etdodnItFv8vZAsuXEgSgaje8"),
  );

  assert.equal(
    buildBootstrapEnvelopeAAD(context),
    '{"version":1,"updater_id":"updater-01","policy_revision":7,"job_id":"bootstrap-job-01","host_ids":["host-a","host-b"]}',
  );
  assert.equal(
    new TextDecoder().decode(plaintext),
    '{"administrator_user":"deploy","private_key":"dGVzdC1wcml2YXRlLWtleQ","passphrase":"dGVzdC1wYXNzcGhyYXNl"}',
  );
});

test("bootstrap ephemeral ECDH private key is not extractable while the public raw key remains exportable", async () => {
  const keys = await crypto.subtle.generateKey(
    { name: "ECDH", namedCurve: "P-256" },
    false,
    ["deriveBits"],
  );
  assert.equal(keys.privateKey.extractable, false);
  assert.equal(keys.publicKey.extractable, true);
  await assert.rejects(() => crypto.subtle.exportKey("pkcs8", keys.privateKey));
  assert.equal((await crypto.subtle.exportKey("raw", keys.publicKey)).byteLength, 65);

  const source = readFileSync(new URL("../src/lib/bootstrap-envelope.ts", import.meta.url), "utf8");
  assert.match(source, /generateKey\([\s\S]*?\},\s*false,\s*\["deriveBits"\]/);
});

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
    dockerMapping: undefined,
  });
  assert.deepEqual(systemUpdatePortReconfigureRequest({
    targetID: baseTarget.target_id,
    currentPort: ready.currentPort,
    newPort: 18084,
    expectedEndpointRevision: ready.endpointRevision,
    idempotencyKey: "port-worker-main-7",
  }), {
    operation: "port_reconfigure",
    target_id: "worker-main",
    new_port: 18084,
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
  const dockerTarget = { ...baseTarget, deployment_mode: "docker", port_mapping: dockerMapping };
  const dockerReady = systemUpdatePortReconfigureEligibility({ target: dockerTarget, updater, node, requestState: "idle" });
  assert.deepEqual(dockerReady, {
    ready: true,
    reason: "",
    deploymentMode: "docker",
    currentPort: 8084,
    endpointRevision: 7,
    dockerMapping,
  });
  assert.deepEqual(systemUpdateDockerPortReconfigureRequest({
    targetID: "worker-main",
    currentMapping: dockerMapping,
    newAdvertisedPort: 443,
    newPublishedPort: 28084,
    newContainerPort: 18080,
    expectedEndpointRevision: 7,
    idempotencyKey: "docker-port-worker-main-7",
  }), {
    operation: "port_reconfigure",
    target_id: "worker-main",
    new_advertised_port: 443,
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
  assert.throws(() => systemUpdatePortReconfigureRequest({
    targetID: "worker-main",
    currentPort: 8084,
    newPort: 8084,
    expectedEndpointRevision: 7,
    idempotencyKey: "same-port",
  }), /port_unchanged/);
  assert.throws(() => systemUpdatePortReconfigureRequest({
    targetID: "worker-main",
    currentPort: 8084,
    newPort: 1023,
    expectedEndpointRevision: 7,
    idempotencyKey: "privileged-port",
  }), /invalid_service_port/);
  assert.throws(() => systemUpdateDockerPortReconfigureRequest({
    targetID: "worker-main",
    currentMapping: dockerMapping,
    newAdvertisedPort: 8084,
    newPublishedPort: 18084,
    newContainerPort: 8080,
    expectedEndpointRevision: 7,
    idempotencyKey: "unchanged-docker",
  }), /port_unchanged/);
  assert.throws(() => systemUpdateDockerPortReconfigureRequest({
    targetID: "worker-main",
    currentMapping: dockerMapping,
    newAdvertisedPort: 443,
    newPublishedPort: 1023,
    newContainerPort: 8080,
    expectedEndpointRevision: 7,
    idempotencyKey: "privileged-published",
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

test("port reconfiguration response loss never resends POST and remains visibly ambiguous", async () => {
  const request = systemUpdatePortReconfigureRequest({
    targetID: "worker-main",
    currentPort: 8084,
    newPort: 18084,
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
    port_reconfigure: { old_port: 8084, new_port: 18084, expected_endpoint_revision: 7, result: "applied" as const },
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
  assert.equal(systemUpdatePortReconfigureResultLabel("applied"), "新しいポートを適用済み");
  assert.equal(systemUpdatePortReconfigureResultLabel("rolled_back"), "以前のポートへロールバック済み");
  assert.equal(systemUpdatePortReconfigureResultLabel("rollback_failed"), "ロールバック失敗");
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
  });
  const committed = {
    id: "job-docker-response-loss",
    idempotency_key: request.idempotency_key,
    target_id: request.target_id,
    target_type: "worker",
    deployment_mode: "docker",
    operation: "port_reconfigure" as const,
    port_reconfigure: {
      old_port: 8084,
      new_port: 443,
      expected_endpoint_revision: 7,
      docker: {
        published_host_ip: "127.0.0.1",
        old_published_port: 18084,
        new_published_port: 28084,
        old_container_port: 8080,
        new_container_port: 18080,
        old_health_port: 18084,
        new_health_port: 28084,
        approved_compose_config_sha256: "a".repeat(64),
        approved_compose_revision: 4,
        expected_version_env_sha256: `sha256:${"b".repeat(64)}`,
        expected_container_id: "c".repeat(64),
        expected_image_id: `sha256:${"d".repeat(64)}`,
        expected_repository_digest: `sha256:${"e".repeat(64)}`,
      },
    },
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

test("bulk update requests are created sequentially", async () => {
  const targets = [baseTarget, { ...baseTarget, target_id: "worker-standby" }, { ...baseTarget, target_id: "control-panel", target_type: "control_panel" }];
  const order: string[] = [];
  let active = 0;
  let maxActive = 0;

  const results = await runSystemUpdatesSequentially(targets, async (target) => {
    active += 1;
    maxActive = Math.max(maxActive, active);
    order.push(target.target_id);
    await new Promise((resolve) => setTimeout(resolve, 1));
    active -= 1;
    return target.target_id;
  });

  assert.equal(maxActive, 1);
  assert.deepEqual(order, ["worker-main", "worker-standby", "control-panel"]);
  assert.deepEqual(results, order);
});

test("response loss recovers the committed job with the same idempotency key", async () => {
  const key = "web-worker-main-stable-operation";
  const requests: Array<{ idempotency_key: string }> = [];
  const committed = {
    id: "job-response-loss",
    idempotency_key: key,
    target_id: baseTarget.target_id,
    target_type: baseTarget.target_type,
    status: "queued",
    created_at: "2026-07-18T00:00:00Z",
    updated_at: "2026-07-18T00:00:00Z",
  };
  const recovered = await requestSystemUpdateWithRecovery(
    baseTarget,
    key,
    async (request) => {
      requests.push(request);
      throw new Error("response_lost_after_commit");
    },
    async () => [committed],
  );
  assert.equal(recovered.id, committed.id);
  assert.equal(recovered.idempotency_key, key);
  assert.deepEqual(requests.map((request) => request.idempotency_key), [key]);

  const retryRequests: string[] = [];
  await assert.rejects(() => requestSystemUpdateWithRecovery(
    baseTarget,
    key,
    async (request) => { retryRequests.push(request.idempotency_key); throw new Error("network_down"); },
    async () => [],
  ));
  const retried = await requestSystemUpdateWithRecovery(
    baseTarget,
    key,
    async (request) => { retryRequests.push(request.idempotency_key); return committed; },
    async () => [],
  );
  assert.equal(retried.id, committed.id);
  assert.deepEqual(retryRequests, [key, key]);
});

test("bootstrap response loss recovers the one committed privileged job without resending its envelope", async () => {
  const request = {
    job_id: "6ba7b810-9dad-4f0e-9a58-4aee7cb5560f",
    idempotency_key: "bootstrap-host-main-once",
    expected_revision: 7,
    host_ids: ["host-main"],
    recipient_key_fingerprint: "SHA256:bootstrap-key",
    envelope: {
      version: 1 as const,
      ephemeral_public_key: "ephemeral-public-key",
      nonce: "nonce",
      ciphertext: "encrypted-credential",
    },
  };
  const committedJobs: Array<{
    id: string;
    idempotency_key: string;
    updater_id: string;
    expected_revision: number;
    status: string;
    host_ids: string[];
    hosts: Array<{ host_id: string; status: string }>;
    created_at: string;
  }> = [];
  const postedRequests: typeof request[] = [];

  const recovered = await requestUpdaterHostBootstrapWithRecovery(
    request,
    async (stableRequest) => {
      postedRequests.push(stableRequest);
      committedJobs.push({
        id: stableRequest.job_id,
        idempotency_key: stableRequest.idempotency_key,
        updater_id: "updater-main",
        expected_revision: stableRequest.expected_revision,
        status: "queued",
        host_ids: [...stableRequest.host_ids],
        hosts: stableRequest.host_ids.map((hostID) => ({ host_id: hostID, status: "queued" })),
        created_at: "2026-07-27T00:00:00Z",
      });
      throw new Error("response_lost_after_commit");
    },
    async () => committedJobs,
  );

  assert.equal(postedRequests.length, 1);
  assert.equal(committedJobs.length, 1);
  assert.equal(postedRequests[0], request);
  assert.equal(postedRequests[0].envelope.ciphertext, "encrypted-credential");
  assert.equal(recovered.jobs.length, 1);
  assert.equal(recovered.jobs[0].id, request.job_id);
  assert.equal(recovered.jobs[0].idempotency_key, request.idempotency_key);

  await assert.rejects(
    () => requestUpdaterHostBootstrapWithRecovery(
      request,
      async () => { throw new Error("response_lost_after_commit"); },
      async () => [{ ...committedJobs[0], host_ids: ["different-host"] }],
    ),
    (error) => error instanceof UpdaterHostBootstrapRequestAmbiguousError,
  );
  assert.equal(request.envelope.ciphertext, "encrypted-credential");
});

test("bootstrap delayed visibility retries only the identical request and still creates one job", async () => {
  const request = {
    job_id: "e1880183-c61b-498d-8cda-f7e9fbfac50a",
    idempotency_key: "bootstrap-delayed-visibility-once",
    expected_revision: 7,
    host_ids: ["host-main"],
    recipient_key_fingerprint: "SHA256:bootstrap-key",
    envelope: {
      version: 1 as const,
      ephemeral_public_key: "same-ephemeral-public-key",
      nonce: "same-nonce",
      ciphertext: "same-encrypted-credential",
    },
  };
  const serverJobs: Array<{
    id: string;
    idempotency_key: string;
    updater_id: string;
    expected_revision: number;
    status: string;
    host_ids: string[];
    hosts: Array<{ host_id: string; status: string }>;
    created_at: string;
  }> = [];
  const postedRequests: typeof request[] = [];
  let listCalls = 0;

  const recovered = await requestUpdaterHostBootstrapWithRecovery(
    request,
    async (stableRequest) => {
      postedRequests.push(stableRequest);
      if (serverJobs.length === 0) {
        serverJobs.push({
          id: stableRequest.job_id,
          idempotency_key: stableRequest.idempotency_key,
          updater_id: "updater-main",
          expected_revision: stableRequest.expected_revision,
          status: "queued",
          host_ids: [...stableRequest.host_ids],
          hosts: stableRequest.host_ids.map((hostID) => ({ host_id: hostID, status: "queued" })),
          created_at: "2026-07-27T00:00:00Z",
        });
        throw new Error("response_lost_after_commit");
      }
      return { jobs: serverJobs };
    },
    async () => {
      listCalls += 1;
      return [];
    },
  );

  assert.equal(listCalls, 1);
  assert.equal(postedRequests.length, 2);
  assert.equal(postedRequests[0], request);
  assert.equal(postedRequests[1], request);
  assert.equal(postedRequests[1].job_id, postedRequests[0].job_id);
  assert.equal(postedRequests[1].idempotency_key, postedRequests[0].idempotency_key);
  assert.equal(postedRequests[1].envelope.ciphertext, postedRequests[0].envelope.ciphertext);
  assert.equal(serverJobs.length, 1);
  assert.equal(recovered.jobs[0].id, request.job_id);
});

test("bootstrap ambiguity keeps only one identity and later recovery polls never POST again", async () => {
  const request = {
    job_id: "9422ba4c-31c0-4382-a6c5-b25bb0fd61ad",
    idempotency_key: "bootstrap-ambiguous-list-recovery",
    expected_revision: 7,
    host_ids: ["host-main"],
    recipient_key_fingerprint: "SHA256:bootstrap-key",
    envelope: {
      version: 1 as const,
      ephemeral_public_key: "same-ephemeral-public-key",
      nonce: "same-nonce",
      ciphertext: "same-encrypted-credential",
    },
  };
  const serverJobs = [{
    id: request.job_id,
    idempotency_key: request.idempotency_key,
    updater_id: "updater-main",
    expected_revision: request.expected_revision,
    status: "queued",
    host_ids: [...request.host_ids],
    hosts: request.host_ids.map((hostID) => ({ host_id: hostID, status: "queued" })),
    created_at: "2026-07-27T00:00:00Z",
  }];
  const postedRequests: typeof request[] = [];
  let createListCalls = 0;

  await assert.rejects(
    () => requestUpdaterHostBootstrapWithRecovery(
      request,
      async (stableRequest) => {
        postedRequests.push(stableRequest);
        if (postedRequests.length === 1) throw new Error("response_lost_after_commit");
        throw Object.assign(new Error("bootstrap_already_exists"), { status: 409 });
      },
      async () => {
        createListCalls += 1;
        if (createListCalls === 1) throw new Error("bootstrap_list_unavailable");
        return [];
      },
    ),
    (error) => error instanceof UpdaterHostBootstrapRequestAmbiguousError,
  );

  const identity = updaterHostBootstrapRequestIdentity(request);
  assert.equal("envelope" in identity, false);
  assert.deepEqual(identity, {
    job_id: request.job_id,
    idempotency_key: request.idempotency_key,
    expected_revision: request.expected_revision,
    host_ids: request.host_ids,
  });
  assert.notEqual(identity.host_ids, request.host_ids);
  assert.equal(postedRequests.length, 2, "the bounded create path may replay only once");
  assert.equal(postedRequests[0], request);
  assert.equal(postedRequests[1], request);
  assert.equal(new Set(postedRequests.map((posted) => posted.job_id)).size, 1);
  assert.equal(new Set(postedRequests.map((posted) => posted.idempotency_key)).size, 1);
  assert.equal(serverJobs.length, 1);

  let recoveryListCalls = 0;
  const refreshJobs = async () => {
    recoveryListCalls += 1;
    if (recoveryListCalls === 1) return [];
    if (recoveryListCalls === 2) throw new Error("bootstrap_list_temporarily_unavailable");
    return serverJobs;
  };
  assert.equal(await recoverUpdaterHostBootstrapRequest(identity, refreshJobs), undefined);
  assert.equal(await recoverUpdaterHostBootstrapRequest(identity, refreshJobs), undefined);
  const recovered = await recoverUpdaterHostBootstrapRequest(identity, refreshJobs);

  assert.equal(recovered?.jobs[0].id, request.job_id);
  assert.equal(recovered?.jobs[0].idempotency_key, request.idempotency_key);
  assert.equal(postedRequests.length, 2, "delayed-list polling must never resend POST");
  assert.equal(request.envelope.ciphertext, "same-encrypted-credential");
});

test("control panel targets and job lifecycle states are classified", () => {
  assert.equal(isControlPanelUpdateTarget({ target_id: "control-panel", target_type: "control_panel" }), true);
  assert.equal(isSystemUpdateJobActive("restarting"), true);
  assert.equal(isSystemUpdateJobActive("staging"), true);
  assert.equal(isSystemUpdateJobActive("applying"), true);
  assert.equal(isSystemUpdateJobActive("reconciling"), true);
  assert.equal(isSystemUpdateJobActive("succeeded"), false);
  assert.equal(isSystemUpdateJobCancellable("queued"), true);
  assert.equal(isSystemUpdateJobCancellable("claimed"), false);
  assert.equal(isSystemUpdateJobCancellable("installing"), false);
  assert.equal(systemUpdateJobStatusLabel("health_checking"), "動作確認中");
  assert.equal(systemUpdateJobStatusLabel("reconciling"), "適用状態を確認中");
  assert.equal(systemUpdateMayDisconnectPanel("queued"), false);
  assert.equal(systemUpdateMayDisconnectPanel("downloading"), false);
  assert.equal(systemUpdateMayDisconnectPanel("stopping"), true);
  assert.equal(systemUpdateMayDisconnectPanel("restarting"), true);
});

test("service update versions follow SemVer prerelease precedence", () => {
  const cases: Array<[string, string, -1 | 0 | 1 | null]> = [
    ["v1.2.3-rc.1", "v1.2.3", -1],
    ["v1.2.3-rc.1", "v1.2.3-rc.2", -1],
    ["v1.2.3", "v1.2.3-rc.2", 1],
    ["v1.2.3-alpha", "v1.2.3-1", 1],
    ["v1.2.3-rc.1", "v1.2.3-rc.1.1", -1],
    ["v1.2.3+build.1", "v1.2.3+build.2", 0],
    ["v1.2.3-rc.01", "v1.2.3-rc.1", null],
    ["dev", "v1.2.3", null],
  ];
  for (const [current, latest, expected] of cases) {
    assert.equal(compareSystemUpdateVersions(current, latest), expected, `${current} vs ${latest}`);
  }
});

test("wire responses are normalized across the public and legacy field names", () => {
  const response = normalizeSystemUpdatesResponse({
    updaters: [{ updater_id: "updater-1", name: "Central Updater", status: "online", online: true, version: "v1.7.0", last_heartbeat_at: "2026-07-18T00:00:00Z" }],
    hosts: [{ host_id: "host-main", name: "Main Host", updater_id: "updater-1", reachability: "reachable", reachability_checked_at: "2026-07-18T00:00:00Z" }],
    targets: [{
      target_id: "worker-main",
      service_type: "worker",
      name: "Worker",
      host_id: "host-main",
      update_agent_id: "updater-1",
      updater_online: true,
      eligible: true,
      eligible_operations: ["software_update", "port_reconfigure"],
      operation_blocked_reasons: { software_update: "release_manifest_invalid" },
      update_available: true,
      update_check_source: "github_release",
      update_check_error: "rate_limited",
      port_mapping: {
        mode: "docker",
        advertised_port: 443,
        published_host_ip: "127.0.0.1",
        published_port: 18084,
        container_port: 8080,
        health_port: 18084,
        config_revision: 12,
        state: "applied",
        reported_at: "2026-07-18T00:00:00Z",
      },
    }],
    jobs: [{
      id: "job-1",
      idempotency_key: "request-1",
      target_id: "worker-main",
      target_service_type: "worker",
      requested_by_username: "ops",
      operation: "port_reconfigure",
      port_reconfigure: {
        old_port: 8084,
        new_port: 443,
        expected_endpoint_revision: 7,
        docker: {
          published_host_ip: "127.0.0.1",
          old_published_port: 8084,
          new_published_port: 18084,
          old_container_port: 8080,
          new_container_port: 18080,
          old_health_port: 8084,
          new_health_port: 18084,
          approved_compose_config_sha256: "a".repeat(64),
          approved_compose_revision: 12,
          expected_version_env_sha256: `sha256:${"b".repeat(64)}`,
          expected_container_id: "c".repeat(64),
          expected_image_id: `sha256:${"d".repeat(64)}`,
          expected_repository_digest: `sha256:${"e".repeat(64)}`,
        },
      },
      status: "queued",
      progress: 0,
      sequence: 3,
      lease_generation: 2,
      created_at: "2026-07-18T00:00:00Z",
      updated_at: "2026-07-18T00:00:00Z",
    }],
  });
  assert.equal(response.targets[0].target_type, "worker");
  assert.equal(response.targets[0].host_id, "host-main");
  assert.equal(response.targets[0].updater_id, "updater-1");
  assert.equal(response.targets[0].updater_online, true);
  assert.deepEqual(response.targets[0].eligible_operations, ["software_update", "port_reconfigure"]);
  assert.deepEqual(response.targets[0].operation_blocked_reasons, { software_update: "release_manifest_invalid" });
  assert.deepEqual(response.targets[0].port_mapping, {
    mode: "docker",
    advertised_port: 443,
    published_host_ip: "127.0.0.1",
    published_port: 18084,
    container_port: 8080,
    health_port: 18084,
    config_revision: 12,
    state: "applied",
    reported_at: "2026-07-18T00:00:00Z",
  });
  assert.deepEqual(response.updaters[0], { updater_id: "updater-1", name: "Central Updater", status: "online", online: true, version: "v1.7.0", last_heartbeat_at: "2026-07-18T00:00:00Z" });
  assert.deepEqual(response.hosts[0], { host_id: "host-main", name: "Main Host", updater_id: "updater-1", reachability: "reachable", reachability_checked_at: "2026-07-18T00:00:00Z", reachability_code: "" });
  assert.equal(response.targets[0].update_check_source, "github_release");
  assert.equal(response.targets[0].update_check_error, "rate_limited");
  assert.equal(response.jobs[0].target_type, "worker");
  assert.equal(response.jobs[0].idempotency_key, "request-1");
  assert.equal(response.jobs[0].requested_by, "ops");
  assert.equal(response.jobs[0].sequence, 3);
  assert.equal(response.jobs[0].lease_generation, 2);
  assert.equal(response.jobs[0].port_reconfigure?.docker?.new_published_port, 18084);
  assert.equal(response.jobs[0].port_reconfigure?.docker?.new_container_port, 18080);
  assert.equal(systemUpdateJobFromResponse({ job: response.jobs[0] }).id, "job-1");

  const claimJob = systemUpdateJobFromResponse({
    job: { id: "claim-job", target_id: "worker-main", target_type: "worker", status: "reconciling", created_at: "2026-07-18T00:00:00Z", updated_at: "2026-07-18T00:00:00Z" },
    report_sequence: 4,
    lease_generation: 2,
    recovery_required: true,
    last_status: "installing",
  });
  assert.equal(claimJob.report_sequence, 4);
  assert.equal(claimJob.lease_generation, 2);
  assert.equal(claimJob.recovery_required, true);
  assert.equal(claimJob.last_status, "installing");

  const legacy = normalizeSystemUpdatesResponse({
    targets: [{ target_id: "legacy", target_type: "worker", name: "Legacy", update_agent_id: "legacy-updater", eligible: false, update_available: false }],
    jobs: [{ id: "legacy-job", target_id: "legacy", status: "queued", created_at: "2026-07-18T00:00:00Z", updated_at: "2026-07-18T00:00:00Z" }],
  });
  assert.equal(legacy.targets[0].update_check_source, "");
  assert.equal(legacy.targets[0].update_check_error, "");
  assert.equal(legacy.targets[0].host_id, "");
  assert.equal(legacy.targets[0].updater_online, false);
  assert.deepEqual(legacy.updaters, []);
  assert.deepEqual(legacy.hosts, []);
  assert.equal(legacy.jobs[0].sequence, undefined);
  assert.equal(legacy.jobs[0].report_sequence, undefined);
  assert.equal(legacy.jobs[0].lease_generation, undefined);
  assert.equal(legacy.jobs[0].recovery_required, undefined);
  assert.equal(legacy.jobs[0].last_status, "");
});

test("Host Agent policy status is normalized fail closed", () => {
  const response = normalizeSystemUpdatesResponse({
    updaters: [{
      updater_id: "host-agent-main",
      name: "Host Agent",
      status: "online",
      online: true,
      transport_mode: "pull_v2",
      execution_host_id: "host-main",
      ownership_epoch: 1,
      desired_revision: 4,
      applied_revision: 3,
      policy_status: "pending",
      policy_error_code: "",
    }],
  });
  assert.equal(response.updaters[0].desired_revision, 4);
  assert.equal(response.updaters[0].applied_revision, 3);
  assert.deepEqual(systemUpdateUpdaterPolicyState(response.updaters[0]), { label: "反映待ち", tone: "secondary", ready: false });
  assert.deepEqual(systemUpdateUpdaterPolicyState({ ...response.updaters[0], applied_revision: 4, policy_status: "applied" }), { label: "反映済み", tone: "default", ready: true });
  assert.deepEqual(systemUpdateUpdaterPolicyState({ ...response.updaters[0], policy_status: "failed" }), { label: "反映失敗", tone: "destructive", ready: false });
  assert.deepEqual(systemUpdateUpdaterPolicyState({ ...response.updaters[0], online: false }), { label: "オフライン", tone: "destructive", ready: false });
  assert.deepEqual(systemUpdateUpdaterPolicyState({ updater_id: "new", name: "New", status: "online", online: true, version: "" }), { label: "未設定", tone: "outline", ready: false });
  assert.match(systemUpdatePolicyErrorMessage("active_job_pending"), /処理完了後に自動で反映/);
});

test("updater settings response rejects removed central policy fields", () => {
  assert.throws(() => normalizeUpdaterSettingsResponse({
    updater_id: "updater-main",
    revision: 7,
    transport_mode: "ssh_v1",
    api: { bind_host: "127.0.0.1", host: "127.0.0.1", port: 8090, ssl_enabled: false },
    poll_interval_seconds: 30,
    heartbeat_interval_seconds: 15,
    hosts: [{
      host_id: "host-main",
      name: "Main",
      address: "10.0.0.10",
      port: 55850,
      user: "autostream-update-host",
      arch: "amd64",
      host_public_key: "ssh-ed25519 AAAAHOST root@main",
      host_public_key_fingerprint: "SHA256:host-key",
      ssh_client_public_key: "ssh-ed25519 AAAACLIENT autostream-updater@central",
      ssh_client_key_fingerprint: "SHA256:client-key",
    }],
    targets: [{ target_id: "worker-main", host_id: "host-main", service_type: "worker", deployment_mode: "systemd" }],
    github_token_configured: true,
    github_token_fingerprint: "sha256:token",
    updated_at: "2026-07-25T00:00:00Z",
    github_token: "must-not-be-returned",
    known_hosts_file: "/etc/autostream/updater/ssh/known_hosts",
  }), /invalid_updater_settings_transport/);
});

test("pull_v2 updater settings remain portless and keep server-owned host binding", () => {
  const digest = `sha256:${"a".repeat(64)}`;
  const settings = normalizeUpdaterSettingsResponse({
    updater_id: "host-agent-main",
    revision: 3,
    projection_revision: 4,
    local_executor_policy_revision: 6,
    transport_mode: "pull_v2",
    execution_host_id: "host-main",
    execution_host_ownership: {
      execution_host_id: "host-main",
      transport_mode: "pull_v2",
      agent_service_id: "",
      ownership_epoch: 12,
      policy_revision: 3,
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
    poll_interval_seconds: 15,
    heartbeat_interval_seconds: 30,
    targets: [{
      target_id: "control-panel",
      service_id: "control-panel",
      host_id: "host-main",
      service_type: "control_panel",
      deployment_mode: "systemd",
      database_name: "autostream-kometubu_panel",
    }],
    api: {},
    hosts: [],
  });

  assert.equal(settings.transport_mode, "pull_v2");
  assert.equal(settings.execution_host_id, "host-main");
  assert.equal(settings.local_executor_policy_sha256, digest);
  assert.equal(settings.targets[0].service_id, "control-panel");
  assert.equal(settings.targets[0].database_name, "autostream-kometubu_panel");
  assert.equal(settings.hosts.length, 0);
  assert.deepEqual(settings.pull_activation, {
    ready: true,
    blocked_reason: "",
    status: "online",
    last_heartbeat_at: "2026-07-28T00:00:00Z",
    observe_only: true,
    update_executor: true,
    mutation_enabled: false,
    recovery_pending: false,
    reported_ownership_epoch: 0,
    reported_projection_revision: 4,
  });
  assert.equal(settings.github_token_configured, false);
});

test("pull_v2 database owner target validation is narrow and trims the saved database name", () => {
  const controlPanelTarget: UpdaterSettingsTarget = {
    target_id: "control-panel",
    service_id: "control-panel",
    host_id: "host-main",
    service_type: "control_panel",
    deployment_mode: "systemd",
    database_name: "  autostream-kometubu_panel  ",
  };
  assert.equal(updaterSettingsTargetRequiresDatabase("pull_v2", controlPanelTarget), true);
  assert.equal(
    normalizeUpdaterSettingsTargetDatabaseName("pull_v2", controlPanelTarget, "サービス 1"),
    "autostream-kometubu_panel",
  );
  assert.equal(updaterSettingsTargetRequiresDatabase("ssh_v1" as unknown as UpdaterSettings["transport_mode"], controlPanelTarget), false);
  assert.equal(
    updaterSettingsTargetRequiresDatabase("pull_v2", { ...controlPanelTarget, deployment_mode: "docker" }),
    false,
  );
  assert.equal(
    updaterSettingsTargetRequiresDatabase("pull_v2", { ...controlPanelTarget, service_type: "worker" }),
    false,
  );
  assert.throws(
    () => normalizeUpdaterSettingsTargetDatabaseName(
      "pull_v2",
      { ...controlPanelTarget, database_name: "" },
      "サービス 1",
    ),
    /MariaDBデータベース名を入力してください/,
  );
  assert.throws(
    () => normalizeUpdaterSettingsTargetDatabaseName(
      "pull_v2",
      { ...controlPanelTarget, database_name: "autostream.panel" },
      "サービス 1",
    ),
    /英数字・_・-/,
  );
  assert.throws(
    () => normalizeUpdaterSettingsTargetDatabaseName(
      "pull_v2",
      { ...controlPanelTarget, service_type: "worker" },
      "サービス 1",
    ),
    /MariaDBデータベース名を指定できません/,
  );
  assert.throws(
    () => normalizeUpdaterSettingsTargetDatabaseName("ssh_v1" as unknown as UpdaterSettings["transport_mode"], controlPanelTarget, "サービス 1"),
    /MariaDBデータベース名を指定できません/,
  );
});

test("changing a database owner target identity clears the previous database name", () => {
  const controlPanelTarget: UpdaterSettingsTarget = {
    target_id: "control-panel",
    service_id: "control-panel",
    host_id: "host-main",
    service_type: "control_panel",
    deployment_mode: "systemd",
    database_name: "autostream_control_panel",
  };
  assert.equal(
    applyUpdaterSettingsTargetPatch("pull_v2", controlPanelTarget, { service_type: "observability" }).database_name,
    undefined,
  );
  assert.equal(
    applyUpdaterSettingsTargetPatch("pull_v2", controlPanelTarget, {
      target_id: "control-panel-new",
      service_id: "control-panel-new",
    }).database_name,
    undefined,
  );
  assert.equal(
    applyUpdaterSettingsTargetPatch("pull_v2", controlPanelTarget, { host_id: "host-new" }).database_name,
    undefined,
  );
  assert.equal(
    applyUpdaterSettingsTargetPatch("pull_v2", controlPanelTarget, { deployment_mode: "docker" }).database_name,
    undefined,
  );
  assert.equal(
    applyUpdaterSettingsTargetPatch("pull_v2", controlPanelTarget, { database_name: "new_database" }).database_name,
    "new_database",
  );
});

test("pull_v2 systemd local listener stays separate from the public HTTPS port", () => {
  const observability: UpdaterSettingsTarget = {
    target_id: "observability-main",
    service_id: "observability-main",
    host_id: "host-main",
    service_type: "observability",
    deployment_mode: "systemd",
    local_listen_port: 8082,
  };
  assert.equal(updaterSettingsTargetRequiresLocalListenPort("pull_v2", observability), true);
  assert.equal(
    normalizeUpdaterSettingsTargetLocalListenPort("pull_v2", observability, "サービス 1"),
    8082,
  );
  assert.throws(
    () => normalizeUpdaterSettingsTargetLocalListenPort(
      "pull_v2",
      { ...observability, local_listen_port: 443 },
      "サービス 1",
    ),
    /1024〜65535/,
  );
  assert.equal(
    applyUpdaterSettingsTargetPatch("pull_v2", observability, { deployment_mode: "docker" }).local_listen_port,
    undefined,
  );
});

test("updater settings target options use registered supported services and preserve the current stale ID", () => {
  const registeredTargets: SystemUpdateTarget[] = [
    baseTarget,
    {
      ...baseTarget,
      target_id: "control-panel",
      target_type: "control_panel",
      name: "Control Panel",
    },
    {
      ...baseTarget,
      target_id: "updater-main",
      target_type: "update_agent",
      name: "Host Agent",
    },
    {
      ...baseTarget,
      target_id: "future-main",
      target_type: "future_service",
      name: "Future Service",
    },
  ];
  const configuredTargets: UpdaterSettingsTarget[] = [
    {
      target_id: "worker-main",
      service_id: "worker-main",
      host_id: "host-main",
      service_type: "worker",
      deployment_mode: "systemd",
    },
    {
      target_id: "stale-observer",
      service_id: "stale-observer",
      host_id: "host-main",
      service_type: "observability",
      deployment_mode: "systemd",
    },
  ];

  const staleOptions = updaterSettingsTargetOptions(registeredTargets, configuredTargets, 1);
  assert.deepEqual(staleOptions.map((option) => option.value), ["control-panel", "stale-observer"]);
  assert.match(staleOptions[0]?.label || "", /Control Panel.*control-panel/);
  assert.equal(staleOptions[1]?.current, true);
  assert.equal(staleOptions[1]?.stale, true);
  assert.match(staleOptions[1]?.label || "", /現在の設定/);

  const currentOptions = updaterSettingsTargetOptions(registeredTargets, configuredTargets, 0);
  const currentWorker = currentOptions.find((option) => option.value === "worker-main");
  assert.equal(currentWorker?.current, true);
  assert.equal(currentWorker?.stale, false);
  assert.match(currentWorker?.label || "", /Main Worker.*worker-main.*現在の設定/);
  assert.equal(currentOptions.some((option) => option.value === "updater-main"), false);
  assert.equal(currentOptions.some((option) => option.value === "future-main"), false);
});

test("first unused updater settings target skips duplicates and supports the synthetic control-panel target", () => {
  const registeredTargets: SystemUpdateTarget[] = [
    baseTarget,
    {
      ...baseTarget,
      target_id: "control-panel",
      target_type: "control_panel",
      name: "Control Panel",
    },
    {
      ...baseTarget,
      target_id: "updater-main",
      target_type: "update_agent",
      name: "Host Agent",
    },
  ];
  const worker: UpdaterSettingsTarget = {
    target_id: "worker-main",
    service_id: "worker-main",
    host_id: "host-main",
    service_type: "worker",
    deployment_mode: "systemd",
  };

  assert.deepEqual(firstUnusedUpdaterSettingsTarget("pull_v2", registeredTargets, [worker], "host-selected"), {
    target_id: "control-panel",
    service_id: "control-panel",
    host_id: "host-selected",
    service_type: "control_panel",
    deployment_mode: "systemd",
  });
  assert.equal(firstUnusedUpdaterSettingsTarget("pull_v2", registeredTargets, [
    worker,
    {
      target_id: "control-panel",
      service_id: "control-panel",
      host_id: "host-main",
      service_type: "control_panel",
      deployment_mode: "systemd",
    },
  ], "host-selected"), undefined);

  const rejectedLegacyTarget = firstUnusedUpdaterSettingsTarget(
    "ssh_v1" as unknown as UpdaterSettings["transport_mode"],
    [baseTarget],
    [],
    "host-selected",
  );
  assert.equal(rejectedLegacyTarget?.service_type, "worker");
  assert.equal(rejectedLegacyTarget?.local_listen_port, undefined);
});

test("selecting an updater settings target synchronizes both IDs and its derived service type", () => {
  const current: UpdaterSettingsTarget = {
    target_id: "control-panel",
    service_id: "control-panel",
    host_id: "host-main",
    service_type: "control_panel",
    deployment_mode: "systemd",
    database_name: "autostream_control_panel",
  };

  assert.deepEqual(applyUpdaterSettingsTargetSelection("pull_v2", current, {
    target_id: "worker-main",
    target_type: "worker",
  }), {
    target_id: "worker-main",
    service_id: "worker-main",
    host_id: "host-main",
    service_type: "worker",
    deployment_mode: "systemd",
    local_listen_port: 8084,
  });
  assert.equal(applyUpdaterSettingsTargetSelection("pull_v2", current, {
    target_id: "control-panel",
    target_type: "control_panel",
  }).database_name, "autostream_control_panel");
});

test("system update response exposes only the updater bootstrap encryption public key metadata", () => {
  const response = normalizeSystemUpdatesResponse({
    updaters: [{
      updater_id: "updater-main",
      name: "Central Updater",
      status: "online",
      online: true,
      version: "v1.8.0",
      bootstrap_encryption_public_key: "BAc-public-key",
      bootstrap_encryption_key_fingerprint: "SHA256:bootstrap-key",
      bootstrap_encryption_private_key: "must-not-be-returned",
    }],
  });

  assert.equal(response.updaters[0].bootstrap_encryption_public_key, "BAc-public-key");
  assert.equal(response.updaters[0].bootstrap_encryption_key_fingerprint, "SHA256:bootstrap-key");
  assert.equal("bootstrap_encryption_private_key" in response.updaters[0], false);
});

test("bootstrap job response is whitelisted and active states fail closed", () => {
  const response = normalizeUpdaterHostBootstrapJobsResponse({
    jobs: [{
      id: "bootstrap-1",
      job_id: "legacy-alias",
      idempotency_key: "idempotency-1",
      updater_id: "updater-main",
      expected_revision: 7,
      status: "installing",
      host_ids: ["host-main", "host-standby"],
      hosts: [
        { host_id: "host-main", status: "installing", progress: 55, message: "Installing", updated_at: "2026-07-27T00:00:00Z", private_key: "secret" },
        { host_id: "host-standby", status: "queued", progress: 0 },
      ],
      created_at: "2026-07-27T00:00:00Z",
      envelope: { ciphertext: "must-not-be-returned" },
      administrator_user: "must-not-be-returned",
    }],
  }, "updater-fallback");

  assert.equal(response.jobs[0].id, "bootstrap-1");
  assert.equal(response.jobs[0].updater_id, "updater-main");
  assert.equal(response.jobs[0].hosts[0].progress, 55);
  assert.equal("envelope" in response.jobs[0], false);
  assert.equal("administrator_user" in response.jobs[0], false);
  assert.equal("private_key" in response.jobs[0].hosts[0], false);
  for (const status of ["awaiting_credentials", "queued", "claimed", "connecting", "uploading", "verifying", "installing", "probing"]) {
    assert.equal(isUpdaterHostBootstrapJobActive(status), true, status);
  }
  for (const status of ["succeeded", "failed", "credential_expired", "", "unknown"]) {
    assert.equal(isUpdaterHostBootstrapJobActive(status), false, status);
  }
});

test("bootstrap eligibility requires the applied policy projection, saved bootstrap host, generated keys, and no active job", () => {
  const savedHost: UpdaterSettingsHost = {
    host_id: "host-main",
    name: "Main",
    address: "10.0.0.10",
    port: 22,
    user: "autostream-update-host",
    arch: "amd64",
    host_public_key: "ssh-ed25519 AAAAHOST main",
    host_key_fingerprint: "SHA256:host-key",
    ssh_client_public_key: "ssh-ed25519 AAAACLIENT updater",
    ssh_client_key_fingerprint: "SHA256:client-key",
  };
  const updater: SystemUpdateAgentStatus = {
    updater_id: "updater-main",
    name: "Central Updater",
    status: "online",
    online: true,
    version: "v1.8.0",
    desired_revision: 4,
    applied_revision: 4,
    policy_status: "applied",
    bootstrap_encryption_public_key: "BAc-public-key",
    bootstrap_encryption_key_fingerprint: "SHA256:bootstrap-key",
    ssh_client_public_keys: { "host-main": "ssh-ed25519 AAAACLIENT updater" },
    ssh_client_key_fingerprints: { "host-main": "SHA256:client-key" },
  };
  const base = {
    updater,
    expectedAppliedRevision: 4,
    savedHost,
    currentHost: { ...savedHost },
    releaseTokenConfigured: true,
  };

  assert.deepEqual(updaterHostBootstrapEligibility(base), { ready: true, reason: "" });
  const missingReleaseToken = updaterHostBootstrapEligibility({ ...base, releaseTokenConfigured: false });
  assert.deepEqual(missingReleaseToken, { ready: false, reason: "release_token_pending" });
  assert.equal(
    updaterHostBootstrapEligibilityMessage(missingReleaseToken.reason, true),
    "GitHub Release Tokenを保存してからホストセットアップを開始してください。",
  );
  assert.equal(updaterHostBootstrapEligibility({ ...base, updater: { ...updater, online: false } }).reason, "updater_offline");
  assert.equal(updaterHostBootstrapEligibility({ ...base, updater: { ...updater, desired_revision: 5 } }).reason, "policy_pending");
  assert.equal(updaterHostBootstrapEligibility({ ...base, expectedAppliedRevision: 7 }).reason, "policy_pending");
  assert.equal(updaterHostBootstrapEligibility({ ...base, currentHost: { ...savedHost, address: "10.0.0.11" } }).reason, "host_unsaved");
  assert.equal(updaterHostBootstrapEligibility({
    ...base,
    updater: { ...updater, ssh_client_public_keys: {}, ssh_client_key_fingerprints: {} },
  }).reason, "client_key_pending");
  assert.equal(updaterHostBootstrapEligibility({
    ...base,
    savedHost: { ...savedHost, host_key_fingerprint: "", host_public_key_fingerprint: "" },
  }).reason, "host_key_pending");
  assert.equal(updaterHostBootstrapEligibility({ ...base, bootstrapStatus: "installing" }).reason, "bootstrap_active");
  const configured = updaterHostBootstrapEligibility({ ...base, bootstrapStatus: "succeeded" });
  assert.deepEqual(configured, { ready: true, reason: "already_configured" });
  assert.equal(isUpdaterHostBootstrapBulkCandidate(configured), false);
  const expired = updaterHostBootstrapEligibility({ ...base, bootstrapStatus: "credential_expired" });
  assert.deepEqual(expired, { ready: true, reason: "" });
  assert.equal(isUpdaterHostBootstrapBulkCandidate(expired), true);
  assert.equal(isUpdaterHostBootstrapBulkCandidate(updaterHostBootstrapEligibility(base)), true);
  assert.equal(updaterHostBootstrapEligibility({ ...base, updater: { ...updater, bootstrap_encryption_public_key: "" } }).reason, "encryption_key_pending");
  assert.equal(
    updaterHostBootstrapEligibility({
      ...base,
      savedHost: { ...savedHost, user: "custom-updater" },
      currentHost: { ...savedHost, user: "custom-updater" },
    }).reason,
    "unsupported_profile",
  );
  assert.equal(
    updaterHostBootstrapEligibility({
      ...base,
      savedHost: { ...savedHost, user: " autostream-update-host" },
      currentHost: { ...savedHost, user: " autostream-update-host" },
    }).reason,
    "unsupported_profile",
  );
});

test("bootstrap confirmation follows fresh updater client-key rotation instead of stale settings", () => {
  const savedHost: UpdaterSettingsHost = {
    host_id: "host-main",
    name: "Main",
    address: "10.0.0.10",
    port: 22,
    user: "autostream-update-host",
    arch: "amd64",
    host_public_key: "ssh-ed25519 AAAAHOST main",
    host_key_fingerprint: "SHA256:host-key",
    ssh_client_public_key: "ssh-ed25519 AAAAOLD updater",
    ssh_client_key_fingerprint: "SHA256:old-client-key",
  };
  const updater: SystemUpdateAgentStatus = {
    updater_id: "updater-main",
    name: "Central Updater",
    status: "online",
    online: true,
    version: "v1.8.0",
    desired_revision: 7,
    applied_revision: 7,
    policy_status: "applied",
    bootstrap_encryption_public_key: "BAc-public-key",
    bootstrap_encryption_key_fingerprint: "SHA256:bootstrap-key",
    ssh_client_public_keys: { "host-main": "ssh-ed25519 AAAAOLD updater" },
    ssh_client_key_fingerprints: { "host-main": "SHA256:old-client-key" },
  };
  const confirmed = updaterHostBootstrapConfirmationContext(updater, 7, ["host-main"], [savedHost]);
  const rotated = updaterHostBootstrapConfirmationContext({
    ...updater,
    ssh_client_public_keys: { "host-main": "ssh-ed25519 AAAANEW updater" },
    ssh_client_key_fingerprints: { "host-main": "SHA256:new-client-key" },
  }, 7, ["host-main"], [savedHost]);

  assert.notEqual(confirmed, rotated);
  const rotatedHost = (JSON.parse(rotated) as { hosts: Array<Record<string, string>> }).hosts[0];
  assert.equal(rotatedHost.ssh_client_public_key, "ssh-ed25519 AAAANEW updater");
  assert.equal(rotatedHost.ssh_client_key_fingerprint, "SHA256:new-client-key");

  const missing = {
    ...updater,
    ssh_client_public_keys: undefined,
    ssh_client_key_fingerprints: undefined,
  };
  const missingContext = updaterHostBootstrapConfirmationContext(missing, 7, ["host-main"], [savedHost]);
  assert.notEqual(confirmed, missingContext);
  const missingHost = (JSON.parse(missingContext) as { hosts: Array<Record<string, string>> }).hosts[0];
  assert.equal(missingHost.ssh_client_public_key, "");
  assert.equal(missingHost.ssh_client_key_fingerprint, "");
  assert.equal(updaterHostBootstrapEligibility({
    updater: missing,
    expectedAppliedRevision: 7,
    savedHost,
    currentHost: { ...savedHost },
    releaseTokenConfigured: true,
  }).reason, "client_key_pending");
});

test("an active bootstrap batch blocks every host on the updater", () => {
  const status = activeUpdaterHostBootstrapStatus([
    {
      id: "bootstrap-running",
      updater_id: "updater-main",
      expected_revision: 7,
      status: "running",
      host_ids: ["host-a"],
      hosts: [{ host_id: "host-a", status: "installing" }],
      created_at: "2026-07-27T00:00:00Z",
    },
    {
      id: "bootstrap-complete",
      updater_id: "updater-main",
      expected_revision: 7,
      status: "succeeded",
      host_ids: ["host-b"],
      hosts: [{ host_id: "host-b", status: "succeeded" }],
      created_at: "2026-07-26T00:00:00Z",
    },
  ]);

  assert.equal(status, "running");
});

test("a terminal bootstrap batch overrides stale active child state", () => {
  const response = normalizeUpdaterHostBootstrapJobsResponse({
    jobs: [{
      id: "bootstrap-expired",
      updater_id: "updater-main",
      expected_revision: 7,
      status: "credential_expired",
      host_ids: ["host-a"],
      hosts: [{ host_id: "host-a", status: "queued" }],
      created_at: "2026-07-27T00:00:00Z",
      completed_at: "2026-07-27T00:05:00Z",
    }],
  });

  assert.equal(response.jobs[0].hosts[0].status, "credential_expired");
  assert.equal(activeUpdaterHostBootstrapStatus(response.jobs), "");
  assert.equal(activeUpdaterHostBootstrapStatus([{
    ...response.jobs[0],
    hosts: [{ host_id: "host-a", status: "queued" }],
  }]), "");
  assert.equal(activeUpdaterHostBootstrapStatus([{
    ...response.jobs[0],
    status: "unknown",
    hosts: [{ host_id: "host-a", status: "queued" }],
  }]), "queued");
});

test("updater policy host IDs exclude colon while retaining safe separators", () => {
  assert.equal(isUpdaterPolicyHostID("host-main_01.example"), true);
  assert.equal(isUpdaterPolicyHostID("host:main"), false);
  assert.equal(isUpdaterPolicyHostID("-host-main"), false);
  assert.equal(isUpdaterPolicyHostID(`h${"a".repeat(127)}`), true);
  assert.equal(isUpdaterPolicyHostID(`h${"a".repeat(128)}`), false);
});

test("updater settings save preserves, deletes, and replaces the write-only GitHub token explicitly", () => {
  const path = "/system-updates/updaters/host-agent-control/settings";
  const current = mockGet(path) as UpdaterSettings;
  const request = {
    api: current.api,
    poll_interval_seconds: current.poll_interval_seconds,
    heartbeat_interval_seconds: current.heartbeat_interval_seconds,
    hosts: current.hosts,
    targets: current.targets,
  };

  const preserved = mockPut(path, { ...request, expected_revision: current.revision }) as UpdaterSettings;
  assert.equal(preserved.github_token_configured, true);
  const deleted = mockPut(path, { ...request, expected_revision: preserved.revision, github_token: "" }) as UpdaterSettings;
  assert.equal(deleted.github_token_configured, false);
  const replaced = mockPut(path, { ...request, expected_revision: deleted.revision, github_token: "github_pat_test" }) as UpdaterSettings;
  assert.equal(replaced.github_token_configured, true);
});

test("bootstrap job mock stores job metadata without retaining the credential envelope", () => {
  const path = "/system-updates/updaters/host-agent-control/bootstrap-jobs";
  const current = mockGet("/system-updates/updaters/host-agent-control/settings") as UpdaterSettings;
  const request = {
    job_id: `bootstrap-${crypto.randomUUID()}`,
    idempotency_key: `idempotency-${crypto.randomUUID()}`,
    expected_revision: current.revision,
    host_ids: ["host-control", "host-main"],
    recipient_key_fingerprint: "SHA256:zAub8CwOeAN1WI8elABGIcIi2gdIyoxvFPxQ7HcqRlo",
    envelope: {
      version: 1 as const,
      ephemeral_public_key: "ephemeral-public-key",
      nonce: "nonce",
      ciphertext: "credential-ciphertext",
    },
  };
  const created = mockPost(path, request) as Record<string, unknown>;
  const createdJSON = JSON.stringify(created);
  assert.doesNotMatch(createdJSON, /credential-ciphertext|ephemeral-public-key/);

  const listed = mockGet(path) as { jobs: Array<Record<string, unknown>> };
  const stored = listed.jobs.find((job) => job.id === request.job_id);
  assert.ok(stored);
  assert.equal(stored.idempotency_key, request.idempotency_key);
  assert.deepEqual(stored.host_ids, request.host_ids);
  assert.equal("envelope" in stored, false);
});

test("bootstrap job mock rejects a missing or stale recipient key without storing a job", () => {
  const path = "/system-updates/updaters/host-agent-control/bootstrap-jobs";
  const current = mockGet("/system-updates/updaters/host-agent-control/settings") as UpdaterSettings;
  const jobsBefore = (mockGet(path) as { jobs: Array<Record<string, unknown>> }).jobs.length;
  const request = {
    job_id: `bootstrap-recipient-${crypto.randomUUID()}`,
    idempotency_key: `idempotency-recipient-${crypto.randomUUID()}`,
    expected_revision: current.revision,
    host_ids: ["host-main"],
    envelope: {
      version: 1 as const,
      ephemeral_public_key: "ephemeral-public-key",
      nonce: "nonce",
      ciphertext: "credential-ciphertext",
    },
  };

  assert.throws(
    () => mockPost(path, request),
    /invalid_updater_host_bootstrap_request/,
  );
  assert.throws(
    () => mockPost(path, {
      ...request,
      job_id: `bootstrap-recipient-${crypto.randomUUID()}`,
      idempotency_key: `idempotency-recipient-${crypto.randomUUID()}`,
      recipient_key_fingerprint: "SHA256:stale-bootstrap-envelope-key",
    }),
    /bootstrap_recipient_key_changed/,
  );
  const jobsAfter = (mockGet(path) as { jobs: Array<Record<string, unknown>> }).jobs.length;
  assert.equal(jobsAfter, jobsBefore);
});

test("mock bootstrap encryption fingerprint matches its P-256 public key", async () => {
  const response = mockGet("/system-updates") as {
    updaters: Array<{
      bootstrap_encryption_public_key?: string;
      bootstrap_encryption_key_fingerprint?: string;
    }>;
  };
  const updater = response.updaters[0];
  assert.ok(updater?.bootstrap_encryption_public_key);
  const digest = new Uint8Array(await crypto.subtle.digest(
    "SHA-256",
    fromBase64URL(updater.bootstrap_encryption_public_key),
  ));
  const fingerprint = `SHA256:${Buffer.from(digest).toString("base64").replace(/=+$/g, "")}`;
  assert.equal(updater.bootstrap_encryption_key_fingerprint, fingerprint);
});

test("system update UI manages updater policy in the panel and never instructs manual trust files", () => {
  const applicationSource = readFileSync(new URL("../src/features/application/application-info-view.tsx", import.meta.url), "utf8");
  const settingsSource = readFileSync(new URL("../src/features/application/updater-settings-panel.tsx", import.meta.url), "utf8");
  const bootstrapSource = readFileSync(new URL("../src/features/application/updater-host-bootstrap-panel.tsx", import.meta.url), "utf8");
  const queriesSource = readFileSync(new URL("../src/features/queries.ts", import.meta.url), "utf8");
  const nodeSource = readFileSync(new URL("../src/features/nodes/node-registration-view.tsx", import.meta.url), "utf8");
  const buttonSource = readFileSync(new URL("../src/components/ui/button.tsx", import.meta.url), "utf8");

  assert.match(applicationSource, /canManageUpdaterSecrets=\{hasPermission\(currentUser\.data, "secrets\.update"\)\}/);
  assert.doesNotMatch(applicationSource, /各ホストへのUpdater導入は不要/);
  assert.match(applicationSource, /Host Agentがoutbound通信で受け取って安全に適用/);
  assert.match(applicationSource, /SSH接続やUpdater用TCP受信ポートは使いません/);
  assert.doesNotMatch(applicationSource, /中央Updater/);
  assert.match(applicationSource, /lg:grid-cols-2 \[&>\*:only-child\]:col-span-full/);
  assert.match(applicationSource, /CardHeader className="min-w-0 gap-3 sm:flex-row sm:items-start sm:justify-between"/);
  assert.match(applicationSource, /className="h-auto max-w-full whitespace-normal text-left sm:h-8 sm:whitespace-nowrap"/);
  assert.match(applicationSource, /className="grid gap-4 2xl:hidden"/);
  assert.match(applicationSource, /className="hidden overflow-x-auto rounded-md border 2xl:block"/);
  assert.match(applicationSource, /title="Updater \/ Host Agent"/);
  assert.match(applicationSource, /Nodeサービスは登録されていません/);
  assert.match(applicationSource, /function RegisteredServiceMobileCard/);
  assert.doesNotMatch(applicationSource, /disabled:bg-muted disabled:text-muted-foreground disabled:opacity-100/);
  assert.match(buttonSource, /default: "bg-primary text-primary-foreground hover:bg-primary\/90 disabled:bg-muted disabled:text-muted-foreground disabled:opacity-100"/);
  assert.match(applicationSource, /canEdit=\{canExecute\}/);
  assert.match(applicationSource, /availableTargets=\{targets\}/);
  assert.match(applicationSource, /希望endpoint（未適用を含む）/);
  assert.match(applicationSource, /現在適用中のendpoint/);
  assert.match(applicationSource, /Node報告endpoint/);
  assert.match(applicationSource, /systemUpdatePortReconfigureEligibility/);
  assert.match(applicationSource, /systemUpdateSoftwareOperationEligibility/);
  assert.match(applicationSource, /requestSystemUpdatePortReconfigureWithRecovery/);
  assert.match(applicationSource, /activePortRequestTargets = useRef\(new Set<string>\(\)\)/);
  assert.match(applicationSource, /acquireSystemUpdateTargetRequestLock\(activePortRequestTargets\.current, targetID\)/);
  assert.match(applicationSource, /isSystemUpdateEndpointRevisionConflict\(error\)/);
  assert.match(applicationSource, /Promise\.allSettled\(refreshes\)/);
  assert.match(applicationSource, /systemUpdatePortRequestMatchesJob\(ambiguousPortRequest, job\)/);
  assert.match(applicationSource, /systemUpdateDockerPortReconfigureRequest/);
  assert.match(applicationSource, /公開originやreverse proxy設定は自動変更しません/);
  assert.match(applicationSource, /localhost publishedポート/);
  assert.match(applicationSource, /container待受ポート/);
  assert.match(applicationSource, /min=\{1024\}[\s\S]*max=\{65535\}/);
  assert.match(applicationSource, /aria-describedby=\{`\$\{helpID\} \$\{describedBy\}`\}/);
  assert.match(applicationSource, /aria-busy=\{submitting\}/);
  assert.match(applicationSource, /submittingRef\.current/);
  assert.match(applicationSource, /role="status" aria-live="polite"/);
  assert.match(applicationSource, /ambiguousPortTargetID=\{unresolvedAmbiguousPortRequest\?\.target_id\}/);
  assert.match(applicationSource, /retry:\s*false/);
  assert.match(settingsSource, /設定を保存/);
  assert.match(settingsSource, /Host Agentのconfigureを再実行/);
  assert.doesNotMatch(settingsSource, /中央Updater/);
  assert.match(settingsSource, /設定の変更には system_updates\.execute 権限が必要/);
  assert.match(settingsSource, /system_updates\.execute/);
  assert.match(settingsSource, /pullUpdaterOwnershipActivationEligibility/);
  assert.match(settingsSource, /pullUpdaterOwnershipActivationRequest/);
  assert.match(settingsSource, /\/pull-ownership\/activate/);
  assert.match(settingsSource, /expected_execution_host_id/);
  assert.match(settingsSource, /expected_ownership_epoch/);
  assert.match(settingsSource, /expected_source_policy_revision/);
  assert.match(settingsSource, /expected_projection_revision/);
  assert.match(settingsSource, /expected_local_executor_policy_revision/);
  assert.match(settingsSource, /expected_local_executor_policy_sha256/);
  assert.match(settingsSource, /Host Agentの更新実行権限をCASで切り替えます/);
  assert.match(settingsSource, /aria-busy=\{activateOwnership\.isPending\}/);
  assert.match(settingsSource, /pullUpdaterOwnershipDeactivationEligibility/);
  assert.match(settingsSource, /pullUpdaterOwnershipDeactivationRequest/);
  assert.match(settingsSource, /\/pull-ownership\/deactivate/);
  assert.doesNotMatch(settingsSource, /緊急Bridge rollback/);
  assert.doesNotMatch(settingsSource, /legacyAgentServiceID/);
  assert.match(settingsSource, /setAmbiguousDeactivationAttempt\(attempt\)/);
  assert.doesNotMatch(settingsSource, /deactivateOwnership\.mutate\(ambiguousDeactivationAttempt/);
  assert.match(settingsSource, /role=\{ownershipFeedback\?\.tone === "error"[\s\S]*\? "alert" : "status"\}/);
  assert.match(applicationSource, /canManageUpdaterSecrets=\{hasPermission\(currentUser\.data, "secrets\.update"\)\}/);
  assert.match(settingsSource, /GitHub Release Token/);
  assert.match(settingsSource, /Host Agentのpolicyや応答には含めません/);
  assert.match(settingsSource, /host_public_key/);
  assert.match(settingsSource, /DeferredUpdaterHostConfirmation/);
  assert.match(settingsSource, /const \[baseRevision, setBaseRevision\] = useState\(settings\.revision\)/);
  assert.match(settingsSource, /expected_revision: expectedRevision/);
  assert.doesNotMatch(settingsSource, /expected_revision: settings\.revision/);
  assert.match(settingsSource, /updater\.transport_mode !== "pull_v2"/);
  assert.match(settingsSource, /<h3[^>]*>Host Agentの動作<\/h3>/);
  assert.match(settingsSource, /受信APIや管理用ポートは使用しません/);
  assert.doesNotMatch(settingsSource, /APIポート/);
  assert.match(settingsSource, /SSHポート/);
  assert.doesNotMatch(settingsSource, /settings\.data\.revision !== 0/);
  assert.match(settingsSource, /service_id: serviceID/);
  assert.doesNotMatch(settingsSource, /service_id: targetID/);
  assert.match(settingsSource, /new Set\(targets\.map\(\(target\) => target\.service_id\)\)/);
  assert.match(settingsSource, /local_executor_policy_sha256: digest/);
  assert.doesNotMatch(settingsSource, /\.\.\.\(digest \? \{ local_executor_policy_sha256: digest \} : \{\}\)/);
  assert.match(settingsSource, /label="MariaDBデータベース名"/);
  assert.match(settingsSource, /ユーザー名・パスワード・DSNは入力しません/);
  assert.match(settingsSource, /updaterSettingsTargetRequiresDatabase\(settings\.transport_mode, target\)/);
  assert.match(settingsSource, /applyUpdaterSettingsTargetPatch\(/);
  assert.match(settingsSource, /updaterSettingsTargetOptions\(availableTargets, form\.targets, index\)/);
  assert.match(settingsSource, /firstUnusedUpdaterSettingsTarget\(settings\.transport_mode, availableTargets, current\.targets, hostID\)/);
  assert.match(settingsSource, /applyUpdaterSettingsTargetSelection\(settings\.transport_mode, target/);
  assert.match(settingsSource, /label="サービス種別（自動）"[\s\S]*?readOnly/);
  assert.doesNotMatch(settingsSource, /onChange=\{\(event\) => updateTarget\(index, \{ target_id:/);
  assert.match(settingsSource, /normalizeUpdaterSettingsTargetDatabaseName\(/);
  assert.match(settingsSource, /maxLength=\{64\}/);
  assert.match(settingsSource, /Host Agentのconfigureを再実行すると反映されます/);
  assert.match(settingsSource, /\{ value: "docker", label: "Docker" \}/);
  assert.match(settingsSource, /min=\{5\}[\s\S]*max=\{3600\}/);
  const heartbeatField = settingsSource.match(/label="Heartbeat間隔（秒）"[\s\S]*?<\/Field>/)?.[0] ?? "";
  assert.match(heartbeatField, /hint="5〜60秒の範囲で設定してください。"/);
  assert.match(heartbeatField, /min=\{5\}[\s\S]*max=\{60\}/);
  assert.doesNotMatch(heartbeatField, /max=\{3600\}/);
  assert.match(settingsSource, /requiredHeartbeatInterval\(form\.heartbeatInterval\)/);
  assert.match(settingsSource, /Heartbeat間隔は5〜60秒の整数で入力してください。現在の値を5〜60秒に変更してから保存してください。/);
  assert.match(settingsSource, /UpdaterHostBootstrapPanel/);
  assert.match(settingsSource, /savedHosts=\{settings\.hosts\}/);
  assert.match(settingsSource, /currentHosts=\{form\.hosts\}/);
  assert.match(settingsSource, /expectedAppliedRevision=\{settings\.projection_revision \?\? settings\.revision\}/);
  assert.match(settingsSource, /currentTargets=\{form\.targets\}/);
  assert.match(settingsSource, /const \[bootstrapActive, setBootstrapActive\] = useState\(false\)/);
  assert.match(settingsSource, /const \[bootstrapCloseBlocked, setBootstrapCloseBlocked\] = useState\(false\)/);
  assert.match(settingsSource, /if \(!nextOpen && \(bootstrapCloseBlocked \|\| ownershipMutationPending\)\) return/);
  assert.match(settingsSource, /showCloseButton=\{!bootstrapCloseBlocked && !ownershipMutationPending\}/);
  assert.match(settingsSource, /onActiveChange=\{setBootstrapActive\}/);
  assert.match(settingsSource, /onCloseBlockedChange=\{onBootstrapCloseBlockedChange\}/);
  assert.match(settingsSource, /disabled=\{saveSettings\.isPending \|\| bootstrapActive \|\| ownershipOperationBlocked\}/);
  assert.match(bootstrapSource, /onActiveChange\(Boolean\(activeBootstrapStatus\) \|\| busy\)/);
  assert.match(bootstrapSource, /onCloseBlockedChange\(busy\)/);
  assert.match(bootstrapSource, /未セットアップを一括セットアップ/);
  assert.match(bootstrapSource, /常駐service・listener・helper専用port・helper用env・Node Runtime Tokenは作成しません/);
  assert.match(bootstrapSource, /bootstrap対象ホストはpull_v2の実行対象・所有権から独立/);
  assert.match(bootstrapSource, /検証済みの標準Host Agent profile/);
  assert.match(bootstrapSource, /\/system-updates\/updaters\/.*\/bootstrap-jobs/);
  assert.match(bootstrapSource, /encryptBootstrapCredentials/);
  assert.match(bootstrapSource, /requestUpdaterHostBootstrapWithRecovery/);
  assert.match(bootstrapSource, /recoverUpdaterHostBootstrapRequest/);
  assert.match(bootstrapSource, /retry:\s*false/);
  assert.match(bootstrapSource, /pollAmbiguousBootstrap\(ambiguousRequest\)/);
  assert.doesNotMatch(bootstrapSource, /startBootstrap\.mutate\(ambiguousRequest\)/);
  assert.match(bootstrapSource, /Boolean\(ambiguousRequest\)/);
  assert.match(bootstrapSource, /要求識別子だけで状態を自動確認し、POSTの再送や新しいセットアップは開始しません/);
  assert.match(
    bootstrapSource,
    /const identity = updaterHostBootstrapRequestIdentity\(request\);[\s\S]*?clearBootstrapRequestEnvelope\(request\);[\s\S]*?setAmbiguousRequest\(identity\);/,
  );
  assert.match(bootstrapSource, /onSettled/);
  assert.match(bootstrapSource, /request\.envelope\.ciphertext = ""/);
  assert.match(bootstrapSource, /confirmedContext === confirmationContext/);
  assert.match(bootstrapSource, /selectionMode === "bulk"[\s\S]*isUpdaterHostBootstrapBulkCandidate/);
  assert.match(bootstrapSource, /submitGenerationRef/);
  assert.match(bootstrapSource, /mountedRef/);
  assert.match(
    bootstrapSource,
    /if \(activeBootstrapRequestRef\.current\) \{\s*clearBootstrapRequestEnvelope\(activeBootstrapRequestRef\.current\);/,
  );
  assert.match(bootstrapSource, /useLayoutEffect\(\(\) => \{\s*operationContextRef\.current = operationContext;/);
  assert.match(bootstrapSource, /useLayoutEffect\(\(\) => \{\s*mountedRef\.current = true;/);
  assert.match(bootstrapSource, /if \(!operationStillCurrent\(\)\) return/);
  assert.match(bootstrapSource, /if \(!canEdit \|\| !selectedHostsStillReady\)/);
  assert.match(
    bootstrapSource,
    /await Promise\.all\(\[\s*apiGet<unknown>\("\/system-updates"\),\s*bootstrapJobs\.refetch\(\),\s*\]\)/,
  );
  assert.match(
    bootstrapSource,
    /queryClient\.setQueryData\(\["system-updates"\], refreshedSystemUpdates\)/,
  );
  assert.match(
    bootstrapSource,
    /recipient_key_fingerprint:\s*refreshedUpdater\.bootstrap_encryption_key_fingerprint/,
  );
  assert.ok(
    bootstrapSource.indexOf("await Promise.all") < bootstrapSource.indexOf("const normalizedUser"),
    "plaintext snapshots must not be created before asynchronous preflight checks finish",
  );
  assert.match(bootstrapSource, /秘密鍵とパスフレーズは今回のセットアップだけに使用し、保存・再表示しません/);
  const passphraseInput = bootstrapSource.slice(
    bootstrapSource.indexOf('id={`${formID}-passphrase`}'),
    bootstrapSource.indexOf('id={`${formID}-passphrase`}') + 400,
  );
  assert.match(passphraseInput, /autoComplete="off"/);
  assert.doesNotMatch(passphraseInput, /autoComplete="new-password"/);
  assert.doesNotMatch(bootstrapSource, /localStorage|sessionStorage|URLSearchParams/);
  assert.doesNotMatch(bootstrapSource, /\.mutate(?:Async)?\(\s*\{[^}]*private_key/s);
  assert.match(queriesSource, /\? 2_000 : 15_000/);
  assert.match(nodeSource, /Updaterの登録とRuntime Tokenの発行には、secrets\.update 権限が必要/);
  assert.doesNotMatch(`${settingsSource}\n${nodeSource}`, /known_hosts|JSON手動設定|updater\.json.*編集|updater\.json.*設定を完成/);
});

test("central updater availability and target host reachability stay independent and fail closed", () => {
  const online: SystemUpdateAgentStatus = {
    updater_id: "updater-main",
    name: "Central Updater",
    status: "online",
    online: true,
    version: "v1.7.0",
    desired_revision: 2,
    applied_revision: 2,
    policy_status: "applied",
  };
  const offline: SystemUpdateAgentStatus = { ...online, status: "offline", online: false };
  const reachable: SystemUpdateHostStatus = { host_id: "host-main", name: "Main Host", updater_id: online.updater_id, reachability: "reachable" };
  const unreachable: SystemUpdateHostStatus = { ...reachable, reachability: "unreachable", reachability_code: "ssh_timeout" };
  const unknown: SystemUpdateHostStatus = { ...reachable, reachability: "unknown" };

  assert.deepEqual(systemUpdateConnectivity(baseTarget, [online], [reachable]), { updater: online, host: reachable, agentOnline: true, reachability: "reachable", ready: true });
  assert.deepEqual(systemUpdateConnectivity(baseTarget, [online], [unreachable]), { updater: online, host: unreachable, agentOnline: true, reachability: "unreachable", ready: false });
  assert.deepEqual(systemUpdateConnectivity(baseTarget, [offline], [reachable]), { updater: offline, host: reachable, agentOnline: false, reachability: "reachable", ready: false });
  assert.deepEqual(systemUpdateConnectivity(baseTarget, [online], [unknown]), { updater: online, host: unknown, agentOnline: true, reachability: "unknown", ready: false });
  assert.deepEqual(systemUpdateConnectivity(baseTarget, [], [reachable]), { updater: undefined, host: undefined, agentOnline: false, reachability: "unknown", ready: false });
  assert.deepEqual(systemUpdateConnectivity(baseTarget, [online], [{ ...reachable, updater_id: "other-updater" }]), { updater: online, host: undefined, agentOnline: true, reachability: "unknown", ready: false });
  assert.equal(systemUpdateConnectivity(baseTarget, [{ ...online, applied_revision: 1, policy_status: "pending" }], [reachable]).ready, false);
  assert.equal(systemUpdateConnectivity(baseTarget, [{ ...online, policy_status: "failed" }], [reachable]).ready, false);
  assert.equal(systemUpdateHostReachabilityLabel("reachable"), "到達可");
  assert.equal(systemUpdateHostReachabilityLabel("unreachable"), "接続不可");
  assert.equal(systemUpdateHostReachabilityLabel("unknown"), "未確認");
  assert.match(systemUpdateHostReachabilityMessage("ssh_host_key_mismatch"), /ホスト鍵/);

  const malformed = normalizeSystemUpdatesResponse({
    updaters: [{ updater_id: "updater-main", online: "true" }],
    hosts: [{ host_id: "host-main", updater_id: "updater-main", reachability: "healthy" }],
    targets: [{ target_id: "worker-main", host_id: "host-main", updater_id: "updater-main", updater_online: "true" }],
  });
  assert.equal(malformed.updaters[0].online, false);
  assert.equal(malformed.hosts[0].reachability, "unknown");
  assert.equal(malformed.targets[0].updater_online, false);
});

test("update API codes are shown as actionable Japanese guidance", () => {
  assert.equal(systemUpdateErrorMessage({ code: "updater_offline" }), "更新エージェントがオフラインです。接続状態を確認してください。");
  assert.match(systemUpdateErrorMessage({ code: "checksum_mismatch" }), /検証に失敗/);
  assert.match(systemUpdateErrorMessage({ code: "release_version_invalid", status: 409, message: "manifest tag v1.bad" }), /公開された更新バージョン.*manifest tag v1\.bad/);
  assert.match(systemUpdateErrorMessage({ code: "download_failed", message: "GitHub returned 403 for asset X" }), /ダウンロード.*GitHub returned 403/);
  assert.match(systemUpdateErrorMessage({ code: "system_update_target_active" }), /進行中/);
  assert.match(systemUpdateErrorMessage({ code: "system_update_not_cancellable" }), /キャンセルできません/);
  assert.match(systemUpdateErrorMessage({ code: "invalid_updater_database_name" }), /MariaDBデータベース名/);
  assert.equal(
    systemUpdateErrorMessage({ code: "updater_host_bootstrap_in_progress" }),
    "ホストの自動セットアップ中はUpdater設定を変更できません。完了後に再試行してください。",
  );
  assert.equal(
    systemUpdateErrorMessage({ code: "bootstrap_recipient_key_changed" }),
    "Updaterのbootstrap暗号鍵が変わりました。状態を再取得し、Fingerprintを確認してから再試行してください。",
  );
  assert.match(systemUpdateErrorMessage({ status: 403 }), /権限/);
  assert.equal(systemUpdateTargetBlockedReason("updater_not_configured"), "Host Agentが設定されていません。");
  assert.equal(systemUpdateTargetBlockedReason("updater_missing"), "Host Agentが設定されていません。");
  assert.equal(systemUpdateTargetBlockedReason("target_unreachable"), "更新エージェントから対象ホストへ接続できません。");
  assert.equal(systemUpdateTargetBlockedReason("target_reachability_unknown"), "対象ホストへの接続状態をまだ確認できません。");
  assert.match(systemUpdateTargetBlockedReason("updater_policy_pending"), /設定の反映を待っています/);
  assert.match(systemUpdateTargetBlockedReason("updater_policy_failed"), /反映できませんでした/);
  assert.match(systemUpdateTargetBlockedReason("updater_policy_mismatch"), /反映が完了していません/);
  assert.match(systemUpdateTargetBlockedReason("updater_policy_target_type_mismatch"), /サービス種別/);
  assert.match(systemUpdateTargetBlockedReason("updater_release_token_not_configured"), /GitHub Release Tokenが未設定/);
  assert.equal(systemUpdateErrorMessage({ code: "target_unreachable" }), "更新エージェントから対象ホストへ接続できません。");
  assert.equal(systemUpdateErrorMessage({ code: "updater_policy_mismatch" }), "Control Panelと更新エージェントの設定が一致していません。設定の反映完了を待ってください。");
  assert.equal(systemUpdateErrorMessage({ code: "policy_snapshot_failed" }), "Updater設定の安全な保存に失敗しました。更新エージェントのログとデータディレクトリを確認してください。");
  assert.equal(systemUpdateErrorMessage({ code: "stale_report" }), "更新エージェントの状態報告が古いため、更新を開始できません。");
  assert.equal(systemUpdateErrorMessage({ status: 500 }), "更新サービスでエラーが発生しました。更新エージェントとControl Panelのログを確認してください。");
  assert.equal(systemUpdateTargetBlockedReason("release_manifest_missing"), "更新用リリース情報が公開されていないため、適用できません。");
  assert.equal(systemUpdateTargetBlockedReason("release_manifest_invalid"), "更新用リリース情報を検証できないため、適用できません。");
  assert.equal(systemUpdateTargetBlockedReason("manifest_unverified"), "最新バージョンは確認できましたが、更新用リリース情報を検証できないため自動適用できません。");
  assert.equal(systemUpdateTargetBlockedReason("updater_version_incompatible"), "minimum_agent_versionを満たすように更新エージェントを更新してください。");
  assert.match(systemUpdatePolicyErrorMessage("policy_snapshot_failed"), /更新操作を停止して自動再試行/);
  assert.match(systemUpdatePolicyErrorMessage("ssh_connectivity_failed"), /SSH接続/);
});

test("deployment and progress presentation makes Docker bundle management explicit", () => {
  assert.equal(systemUpdateDeploymentLabel("docker_compose"), "Docker Compose（Bundle管理）");
  assert.equal(systemUpdateProgress({ progress: -10 }), 0);
  assert.equal(systemUpdateProgress({ progress: 57.6 }), 58);
  assert.equal(systemUpdateProgress({ progress: 180 }), 100);
});

test("system update audit actions have concrete Japanese labels", () => {
  assert.equal(auditActionLabel("system_updates.create"), "システム更新を依頼");
  assert.equal(auditActionLabel("system_updates.cancel"), "システム更新をキャンセル");
  assert.equal(auditActionLabel("system_updates.report"), "システム更新の進捗を報告");
  assert.equal(auditActionLabel("system_updates.updater_policy.save"), "中央Updater設定を保存");
  assert.equal(auditActionLabel("system_updates.bootstrap.create"), "ホストhelperのセットアップを開始");
  assert.equal(auditActionLabel("system_updates.bootstrap.succeeded"), "ホストhelperのセットアップに成功");
  assert.equal(auditActionLabel("system_updates.bootstrap.failed"), "ホストhelperのセットアップに失敗");
  assert.equal(auditActionLabel("system_updates.succeeded"), "システム更新に成功");
});

test("updater token operations require update execution and secret permissions", () => {
  const base = {
    serviceType: "update_agent",
    canCreateTokens: true,
    canResolveManagedSecret: false,
    requiresManagedSecret: false,
    canExecuteSystemUpdates: false,
  };
  assert.equal(canIssueNodeConfiguration(base), false);
  assert.equal(canIssueNodeConfiguration({ ...base, canExecuteSystemUpdates: true }), false);
  assert.equal(canIssueNodeConfiguration({ ...base, canResolveManagedSecret: true }), false);
  assert.equal(canIssueNodeConfiguration({ ...base, canResolveManagedSecret: true, canExecuteSystemUpdates: true }), true);
  assert.equal(canRegenerateNodeConfigureToken({ ...base, canRevokeTokens: true }), false);
  assert.equal(canRegenerateNodeConfigureToken({ ...base, canRevokeTokens: true, canResolveManagedSecret: true, canExecuteSystemUpdates: true }), true);
  assert.equal(canRegenerateNodeConfigureToken({ ...base, canRevokeTokens: false, canResolveManagedSecret: true, canExecuteSystemUpdates: true }), false);
  assert.equal(canRotateNodeRuntimeToken({ ...base, canRevokeTokens: true }), false);
  assert.equal(canRotateNodeRuntimeToken({ ...base, canRevokeTokens: true, canResolveManagedSecret: true, canExecuteSystemUpdates: true }), true);
  assert.equal(canRotateNodeRuntimeToken({ ...base, canRevokeTokens: false, canResolveManagedSecret: true, canExecuteSystemUpdates: true }), false);
  assert.equal(canIssueNodeConfiguration({
    serviceType: "observability",
    canCreateTokens: true,
    canResolveManagedSecret: false,
    requiresManagedSecret: false,
    canExecuteSystemUpdates: false,
  }), true);
});

test("updater mock configure command keeps the one-time token out of argv", () => {
  const response = mockPost("/nodes/registration-tokens", {
    node_type: "update_agent",
    node_id: "central-updater",
    name: "Central Updater",
    host: "127.0.0.1",
    port: 8090,
    ssl_enabled: false,
  }) as { configure_token?: string; configure_command?: string; scopes?: string[] };

  assert.match(response.configure_token || "", /^ast_cfg_/);
  assert.match(response.configure_command || "", /sudo \/usr\/local\/bin\/autostream-host-agent configure/);
  assert.doesNotMatch(response.configure_command || "", /--token|ast_cfg_/);
  assert.equal(
    ["updates.claim", "updates.report", "updates.authorize"].every((scope) => response.scopes?.includes(scope)),
    true,
  );
});

test("node endpoint presentation distinguishes desired, applied, reported, legacy, and pull ownership", () => {
  const baseNode: WorkerNode = {
    id: "worker-endpoint",
    service_type: "worker",
    service_name: "Endpoint Worker",
    status: "online",
    health_status: "healthy",
  };
  const structured = nodeEndpointState({
    ...baseNode,
    host: "legacy-must-not-be-current.example.com",
    port: 8443,
    ssl_enabled: true,
    public_url: "https://legacy-must-not-be-current.example.com:8443",
    desired_endpoint: {
      host: "desired.example.com",
      port: 18084,
      ssl_enabled: true,
      public_url: "https://desired.example.com:18084",
    },
    applied_endpoint: {
      host: "applied.example.com",
      port: 8084,
      ssl_enabled: false,
      public_url: "http://applied.example.com:8084",
    },
    reported_endpoint: {
      host: "reported.example.com",
      port: 28084,
      ssl_enabled: true,
      public_url: "https://reported.example.com:28084",
    },
    endpoint_revision: 4,
    endpoint_status: "pending",
  });
  assert.equal(structured.kind, "endpoint");
  assert.deepEqual(structured.desired, { url: "https://desired.example.com:18084", source: "structured" });
  assert.deepEqual(structured.applied, { url: "http://applied.example.com:8084", source: "structured" });
  assert.deepEqual(structured.reported, { url: "https://reported.example.com:28084", source: "structured" });
  assert.equal(structured.revision, 4);
  assert.equal(structured.status.label, "反映待ち");
  assert.equal(structured.status.tone, "secondary");
  assert.notEqual(structured.desired.url, structured.applied.url, "desired must never be presented as the applied endpoint");

  const legacy = nodeEndpointState({
    ...baseNode,
    host: "legacy.example.com",
    port: 8443,
    ssl_enabled: true,
    public_url: "https://legacy.example.com:8443",
  });
  assert.deepEqual(legacy.desired, { url: "", source: "missing" });
  assert.deepEqual(legacy.applied, { url: "https://legacy.example.com:8443", source: "legacy" });
  assert.deepEqual(legacy.reported, { url: "", source: "missing" });
  assert.equal(legacy.status.label, "反映済み");

  const partialStructured = nodeEndpointState({
    ...baseNode,
    host: "legacy.example.com",
    port: 8443,
    ssl_enabled: true,
    desired_endpoint: {
      host: "desired.example.com",
      port: 18084,
      ssl_enabled: true,
      public_url: "",
    },
  });
  assert.equal(partialStructured.desired.url, "https://desired.example.com:18084");
  assert.deepEqual(partialStructured.applied, { url: "", source: "missing" });
  assert.equal(partialStructured.status.label, "未報告");

  const pull = nodeEndpointState({
    ...baseNode,
    id: "host-agent-a",
    service_type: "update_agent",
    transport_mode: "pull_v2",
    execution_host_id: "host-a",
    ownership_epoch: 7,
    host: "must-be-ignored.example.com",
    port: 8090,
    public_url: "https://must-be-ignored.example.com:8090",
  });
  assert.equal(pull.kind, "pull_v2");
  assert.equal(pull.transportMode, "pull_v2");
  assert.equal(pull.executionHostID, "host-a");
  assert.equal(pull.ownershipEpoch, 7);
  assert.deepEqual(pull.applied, { url: "", source: "missing" });

  assert.equal(nodeServiceEndpointURL({
    host: "fallback.example.com",
    port: 18081,
    ssl_enabled: false,
    public_url: "",
  }), "http://fallback.example.com:18081");
  assert.deepEqual(
    ["applied", "pending", "drift", "rollback", "blocked", "rollback_failed", ""].map((status) => {
      const presentation = nodeEndpointStatusPresentation(status);
      return [presentation.label, presentation.tone];
    }),
    [
      ["反映済み", "default"],
      ["反映待ち", "secondary"],
      ["差分あり", "destructive"],
      ["ロールバック中", "secondary"],
      ["変更ブロック", "destructive"],
      ["ロールバック失敗", "destructive"],
      ["未報告", "outline"],
    ],
  );
  assert.equal(nodeEndpointStatusPresentation("future_state").label, "状態不明 (future_state)");
});

test("pull host agent registration is endpointless and ordinary node ports use the unprivileged range", () => {
  const base = {
    nodeType: "update_agent",
    nodeID: "host-agent-a",
    name: "Host Agent A",
    description: "Host A",
    host: "must-not-leak.example.com",
    port: "8090",
    sslEnabled: true,
    allowRuntimeSecrets: false,
    allowRemediation: false,
    transportMode: "pull_v2" as const,
    executionHostID: "host-a",
  };
  const pull = buildNodeRegistrationRequest(base);
  assert.deepEqual(pull, {
    node_type: "update_agent",
    node_id: "host-agent-a",
    name: "Host Agent A",
    description: "Host A",
    allow_runtime_secrets: false,
    allow_remediation: false,
    transport_mode: "pull_v2",
    execution_host_id: "host-a",
  });
  assert.equal(nodeRegistrationDraftValid(base), true);
  assert.equal(isExecutionHostID("host-a"), true);
  assert.equal(isExecutionHostID(" bad host "), false);

  const worker = {
    ...base,
    nodeType: "worker",
    nodeID: "worker-a",
    name: "Worker A",
    host: "worker.example.com",
    port: "18084",
    transportMode: "pull_v2" as const,
    executionHostID: "",
  };
  assert.deepEqual(buildNodeRegistrationRequest(worker), {
    node_type: "worker",
    node_id: "worker-a",
    name: "Worker A",
    description: "Host A",
    host: "worker.example.com",
    port: 18084,
    ssl_enabled: true,
    allow_runtime_secrets: false,
    allow_remediation: false,
  });
  assert.equal(nodeRegistrationDraftValid(worker), true);
  assert.equal(isServicePort("1023"), false);
  assert.equal(isServicePort("1024"), true);
  assert.equal(isServicePort("65535"), true);
  assert.equal(isServicePort("65536"), false);

  const assertEndpointlessNode = (value: unknown) => {
    const node = value as Record<string, unknown>;
    for (const field of [
      "host",
      "port",
      "ssl_enabled",
      "public_url",
      "desired_endpoint",
      "applied_endpoint",
      "reported_endpoint",
      "endpoint_revision",
      "endpoint_status",
    ]) {
      assert.equal(field in node, false, `pull_v2 node must not expose ${field}`);
    }
  };
  const assertEndpointlessResponse = (value: unknown) => {
    const response = value as { node?: Record<string, unknown>; node_api_url?: unknown };
    assert.equal("node_api_url" in response, false);
    assert.ok(response.node);
    assertEndpointlessNode(response.node);
    assert.doesNotMatch(JSON.stringify(response), /must-not-leak\.example\.com|8090/);
  };
  const mockPull = mockPost("/nodes/registration-tokens", {
    ...pull,
    host: "must-not-leak.example.com",
    port: 8090,
    ssl_enabled: true,
  });
  assertEndpointlessResponse(mockPull);
  assertEndpointlessResponse(mockGet("/nodes/host-agent-a/configuration"));
  assertEndpointlessResponse(mockPost("/nodes/host-agent-a/configure-token"));
  assertEndpointlessResponse(mockPost("/nodes/host-agent-a/rotate-token"));
  const updatedMockPull = mockPut("/nodes/host-agent-a", {
    service_name: "Host Agent A renamed",
    description: "Updated host agent",
    host: "must-not-return.example.com",
    port: 8090,
    ssl_enabled: false,
  }) as Record<string, unknown>;
  assert.equal(updatedMockPull.service_name, "Host Agent A renamed");
  assertEndpointlessNode(updatedMockPull);
  assert.doesNotMatch(JSON.stringify(updatedMockPull), /must-not-return\.example\.com|8090/);
});

test("updater configure failure guidance requires a fresh token before restart", () => {
  const source = readFileSync(new URL("../src/features/nodes/node-registration-view.tsx", import.meta.url), "utf8");

  assert.match(source, /設定処理が失敗または結果不確定の場合は、対象サービスを再起動しないでください。/);
  assert.match(source, /新しいConfigure Tokenを発行し、同じtoken-free commandを新しいTokenで再実行/);
  assert.doesNotMatch(source, /失敗または結果不確定の場合も旧Runtime Tokenは維持/);
  assert.doesNotMatch(source, /同じコマンドで再開|再生成を求められた場合だけ/);
});

test("Host Agent configure delegates managed policy to the system update screen", () => {
  const source = readFileSync(new URL("../src/features/nodes/node-registration-view.tsx", import.meta.url), "utf8");

  assert.match(source, /このHost Agentを稼働させる対象ホストで1回実行/);
  assert.match(source, /Host Agentの実行対象とpolicyは「アプリケーション情報」で設定/);
  assert.match(source, /受信API endpointや専用portは作成しません/);
  assert.doesNotMatch(source, /中央Updater|updater\.json|known_hosts|--init-from|JSON手動設定/);
});

test("updater node description identifies its portless per-host responsibility", () => {
  const source = readFileSync(new URL("../src/features/nodes/node-registration-view.tsx", import.meta.url), "utf8");

  assert.match(source, /value: "update_agent"[^{}\r\n]*description: "ホスト単位の更新状態をControl Panelへ外向き接続で報告するHost Agent"/);
  assert.match(source, /受信listenerは作成しません/);
  assert.match(source, /Host Pull Agent/);
  assert.match(source, /if \(isPullHostAgent\) return ""/);
  assert.match(source, /configuration\.node\?\.service_type !== "update_agent" && configuration\.node_api_url/);
  assert.match(source, /受信ポートなし（Outbound HTTPS）/);
  assert.match(source, /Host Agentの初期設定/);
  assert.match(source, /このHost Agentを稼働させる対象ホストで1回実行/);
  assert.match(source, /受信API endpointや専用portは作成しません/);
  assert.match(source, /transport_mode: \{state\.transportMode\}/);
  assert.match(source, /execution_host_id: \{state\.executionHostID \|\| "未報告"\}/);
  assert.match(source, /ownership_epoch: \{state\.ownershipEpoch \?\? "未報告"\}/);
  assert.match(source, /label="希望値"/);
  assert.match(source, /反映済み \(legacy\)/);
  assert.match(source, /label="Node報告"/);
  assert.match(source, /Revision \{state\.revision \?\? "未報告"\}/);
  assert.doesNotMatch(source, /port_reconfigure/);
  assert.doesNotMatch(source, /表示されたコマンドを中央Updaterホストで/);
});

test("registered node lists use responsive cards instead of forced-width tables", () => {
  const source = readFileSync(new URL("../src/features/nodes/node-registration-view.tsx", import.meta.url), "utf8");
  const tableSource = readFileSync(new URL("../src/components/tables/data-table.tsx", import.meta.url), "utf8");

  assert.match(source, /<DataTable[\s\S]*?responsive\s*\/>/);
  assert.doesNotMatch(source, /minTableWidthClass="min-w-\[960px\]"/);
  assert.match(tableSource, /responsive\?: boolean/);
  assert.match(tableSource, /cell\.column\.id === "endpoint" \|\| cell\.column\.id === "actions"/);
});
