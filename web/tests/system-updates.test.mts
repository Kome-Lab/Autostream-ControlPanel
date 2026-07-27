import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";

import { auditActionLabel } from "../src/lib/audit-action.ts";
import {
  buildBootstrapEnvelopeAAD,
  encryptBootstrapCredentials,
} from "../src/lib/bootstrap-envelope.ts";
import {
  activeUpdaterHostBootstrapStatus,
  compareSystemUpdateVersions,
  isControlPanelUpdateTarget,
  isSystemUpdateJobActive,
  isSystemUpdateJobCancellable,
  isUpdaterHostBootstrapBulkCandidate,
  isUpdaterHostBootstrapJobActive,
  isUpdaterPolicyHostID,
  normalizeUpdaterHostBootstrapJobsResponse,
  normalizeUpdaterSettingsResponse,
  normalizeSystemUpdatesResponse,
  recoverUpdaterHostBootstrapRequest,
  requestSystemUpdateWithRecovery,
  requestUpdaterHostBootstrapWithRecovery,
  runSystemUpdatesSequentially,
  systemUpdateDeploymentLabel,
  systemUpdateErrorMessage,
  systemUpdateConnectivity,
  systemUpdateHostReachabilityLabel,
  systemUpdateHostReachabilityMessage,
  systemUpdateJobStatusLabel,
  systemUpdateMayDisconnectPanel,
  systemUpdatePolicyErrorMessage,
  systemUpdateJobFromResponse,
  systemUpdateUpdaterPolicyState,
  systemUpdateProgress,
  systemUpdateRequest,
  systemUpdateStrategyForTarget,
  systemUpdateTargetBlockedReason,
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
} from "../src/types/domain.ts";
import { mockGet, mockPost, mockPut } from "../src/features/mock-data.ts";
import {
  canRegenerateNodeConfigureToken,
  canIssueNodeConfiguration,
  canRotateNodeRuntimeToken,
} from "../src/lib/node-configuration.ts";

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
    targets: [{ target_id: "worker-main", service_type: "worker", name: "Worker", host_id: "host-main", update_agent_id: "updater-1", updater_online: true, eligible: true, update_available: true, update_check_source: "github_release", update_check_error: "rate_limited" }],
    jobs: [{ id: "job-1", idempotency_key: "request-1", target_id: "worker-main", target_service_type: "worker", requested_by_username: "ops", status: "queued", progress: 0, sequence: 3, lease_generation: 2, created_at: "2026-07-18T00:00:00Z", updated_at: "2026-07-18T00:00:00Z" }],
  });
  assert.equal(response.targets[0].target_type, "worker");
  assert.equal(response.targets[0].host_id, "host-main");
  assert.equal(response.targets[0].updater_id, "updater-1");
  assert.equal(response.targets[0].updater_online, true);
  assert.deepEqual(response.updaters[0], { updater_id: "updater-1", name: "Central Updater", status: "online", online: true, version: "v1.7.0", last_heartbeat_at: "2026-07-18T00:00:00Z" });
  assert.deepEqual(response.hosts[0], { host_id: "host-main", name: "Main Host", updater_id: "updater-1", reachability: "reachable", reachability_checked_at: "2026-07-18T00:00:00Z", reachability_code: "" });
  assert.equal(response.targets[0].update_check_source, "github_release");
  assert.equal(response.targets[0].update_check_error, "rate_limited");
  assert.equal(response.jobs[0].target_type, "worker");
  assert.equal(response.jobs[0].idempotency_key, "request-1");
  assert.equal(response.jobs[0].requested_by, "ops");
  assert.equal(response.jobs[0].sequence, 3);
  assert.equal(response.jobs[0].lease_generation, 2);
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

test("central updater policy status and generated SSH client keys are normalized fail closed", () => {
  const response = normalizeSystemUpdatesResponse({
    updaters: [{
      updater_id: "updater-main",
      name: "Central Updater",
      status: "online",
      online: true,
      desired_revision: 4,
      applied_revision: 3,
      policy_status: "pending",
      policy_error_code: "",
      ssh_client_public_keys: { "host-main": "ssh-ed25519 AAAATEST autostream-updater@central" },
      ssh_client_key_fingerprints: { "host-main": "SHA256:client-key" },
    }],
  });
  assert.equal(response.updaters[0].desired_revision, 4);
  assert.equal(response.updaters[0].applied_revision, 3);
  assert.equal(response.updaters[0].ssh_client_public_keys?.["host-main"], "ssh-ed25519 AAAATEST autostream-updater@central");
  assert.deepEqual(systemUpdateUpdaterPolicyState(response.updaters[0]), { label: "反映待ち", tone: "secondary", ready: false });
  assert.deepEqual(systemUpdateUpdaterPolicyState({ ...response.updaters[0], applied_revision: 4, policy_status: "applied" }), { label: "反映済み", tone: "default", ready: true });
  assert.deepEqual(systemUpdateUpdaterPolicyState({ ...response.updaters[0], policy_status: "failed" }), { label: "反映失敗", tone: "destructive", ready: false });
  assert.deepEqual(systemUpdateUpdaterPolicyState({ ...response.updaters[0], online: false }), { label: "オフライン", tone: "destructive", ready: false });
  assert.deepEqual(systemUpdateUpdaterPolicyState({ updater_id: "new", name: "New", status: "online", online: true, version: "" }), { label: "未設定", tone: "outline", ready: false });
  assert.match(systemUpdatePolicyErrorMessage("active_job_pending"), /処理完了後に自動で反映/);
});

test("updater settings response keeps only the panel-managed policy contract", () => {
  const settings = normalizeUpdaterSettingsResponse({
    updater_id: "updater-main",
    revision: 7,
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
  });

  assert.equal(settings.api.port, 8090);
  assert.equal(settings.hosts[0].host_public_key, "ssh-ed25519 AAAAHOST root@main");
  assert.equal(settings.hosts[0].host_key_fingerprint, "SHA256:host-key");
  assert.equal(settings.hosts[0].ssh_client_public_key, "ssh-ed25519 AAAACLIENT autostream-updater@central");
  assert.equal(settings.github_token_configured, true);
  assert.equal("github_token" in settings, false);
  assert.equal("known_hosts_file" in settings, false);
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

test("bootstrap eligibility requires the applied saved policy, generated host key, and no active job", () => {
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
    desired_revision: 7,
    applied_revision: 7,
    policy_status: "applied",
    bootstrap_encryption_public_key: "BAc-public-key",
    bootstrap_encryption_key_fingerprint: "SHA256:bootstrap-key",
    ssh_client_public_keys: { "host-main": "ssh-ed25519 AAAACLIENT updater" },
    ssh_client_key_fingerprints: { "host-main": "SHA256:client-key" },
  };
  const savedTargets: UpdaterSettingsTarget[] = [{
    target_id: "worker-main",
    host_id: savedHost.host_id,
    service_type: "worker",
    deployment_mode: "systemd",
  }];
  const base = {
    updater,
    expectedRevision: 7,
    savedHost,
    currentHost: { ...savedHost },
    savedTargets,
    currentTargets: savedTargets.map((target) => ({ ...target })),
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
  assert.equal(updaterHostBootstrapEligibility({ ...base, updater: { ...updater, desired_revision: 8 } }).reason, "policy_pending");
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
  assert.equal(updaterHostBootstrapEligibility({ ...base, savedTargets: [], currentTargets: [] }).reason, "unsupported_profile");
  const dockerTargets = savedTargets.map((target) => ({ ...target, deployment_mode: "docker" }));
  assert.equal(updaterHostBootstrapEligibility({ ...base, savedTargets: dockerTargets, currentTargets: dockerTargets }).reason, "unsupported_profile");
  const customTargets = savedTargets.map((target) => ({ ...target, service_type: "custom" }));
  assert.equal(updaterHostBootstrapEligibility({ ...base, savedTargets: customTargets, currentTargets: customTargets }).reason, "unsupported_profile");
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
  assert.equal(updaterHostBootstrapEligibility({ ...base, currentTargets: dockerTargets }).reason, "host_unsaved");
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
  const targets: UpdaterSettingsTarget[] = [{
    target_id: "worker-main",
    host_id: savedHost.host_id,
    service_type: "worker",
    deployment_mode: "systemd",
  }];
  assert.equal(updaterHostBootstrapEligibility({
    updater: missing,
    expectedRevision: 7,
    savedHost,
    currentHost: { ...savedHost },
    savedTargets: targets,
    currentTargets: targets.map((target) => ({ ...target })),
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
  const path = "/system-updates/updaters/updater-central/settings";
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
  const path = "/system-updates/updaters/updater-central/bootstrap-jobs";
  const current = mockGet("/system-updates/updaters/updater-central/settings") as UpdaterSettings;
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
  const path = "/system-updates/updaters/updater-central/bootstrap-jobs";
  const current = mockGet("/system-updates/updaters/updater-central/settings") as UpdaterSettings;
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

  assert.match(applicationSource, /canManageUpdaterSecrets=\{hasPermission\(currentUser\.data, "secrets\.update"\)\}/);
  assert.doesNotMatch(applicationSource, /各ホストへのUpdater導入は不要/);
  assert.match(applicationSource, /常駐Updaterサービス/);
  assert.match(applicationSource, /非常駐helper/);
  assert.match(applicationSource, /canEdit=\{canExecute && canManageUpdaterSecrets\}/);
  assert.match(settingsSource, /設定を保存/);
  assert.match(settingsSource, /Updaterが自動で反映/);
  assert.match(settingsSource, /別の安全な経路.*照合/s);
  assert.match(settingsSource, /設定の変更には system_updates\.execute と secrets\.update の両方の権限が必要/);
  assert.match(settingsSource, /system_updates\.execute/);
  assert.match(settingsSource, /secrets\.update/);
  assert.match(settingsSource, /GitHub Release Token/);
  assert.match(settingsSource, /公開中のControl Panel repositoryにもTokenを必須/);
  assert.match(settingsSource, /private repositoryへ変更した場合、この公開repository用の証明検証では更新できません/);
  assert.match(settingsSource, /host_public_key/);
  assert.match(settingsSource, /ssh_client_public_keys/);
  assert.doesNotMatch(settingsSource, /authorized_keys に登録してください/);
  assert.match(settingsSource, /const \[baseRevision, setBaseRevision\] = useState\(settings\.revision\)/);
  assert.match(settingsSource, /expected_revision: expectedRevision/);
  assert.doesNotMatch(settingsSource, /expected_revision: settings\.revision/);
  assert.match(settingsSource, /\{ value: "docker", label: "Docker" \}/);
  assert.match(settingsSource, /min=\{5\}[\s\S]*max=\{3600\}/);
  const heartbeatField = settingsSource.match(/label="Heartbeat間隔（秒）"[\s\S]*?<\/Field>/)?.[0] ?? "";
  assert.match(heartbeatField, /hint="5〜60秒の範囲で設定してください。"/);
  assert.match(heartbeatField, /min=\{5\}[\s\S]*max=\{60\}/);
  assert.doesNotMatch(heartbeatField, /max=\{3600\}/);
  assert.match(settingsSource, /requiredHeartbeatInterval\(form\.heartbeatInterval\)/);
  assert.match(settingsSource, /Heartbeat間隔は5〜60秒の整数で入力してください。現在の値を5〜60秒に変更してから保存してください。/);
  assert.match(settingsSource, /中央UpdaterのSSH秘密鍵は廃棄され、再追加時は新しい鍵になります/);
  assert.match(settingsSource, /authorized_keys・sudoers・helper設定を撤去/);
  assert.match(settingsSource, /\^ssh-ed25519\\s\+/);
  assert.match(settingsSource, /UpdaterHostBootstrapPanel/);
  assert.match(settingsSource, /key=\{canEdit \? "bootstrap-edit" : "bootstrap-readonly"\}/);
  assert.match(settingsSource, /releaseTokenConfigured=\{settings\.github_token_configured\}/);
  assert.match(settingsSource, /const \[bootstrapActive, setBootstrapActive\] = useState\(false\)/);
  assert.match(settingsSource, /const \[bootstrapCloseBlocked, setBootstrapCloseBlocked\] = useState\(false\)/);
  assert.match(settingsSource, /if \(!nextOpen && bootstrapCloseBlocked\) return/);
  assert.match(settingsSource, /showCloseButton=\{!bootstrapCloseBlocked\}/);
  assert.match(settingsSource, /onActiveChange=\{setBootstrapActive\}/);
  assert.match(settingsSource, /onCloseBlockedChange=\{onBootstrapCloseBlockedChange\}/);
  assert.match(settingsSource, /disabled=\{saveSettings\.isPending \|\| bootstrapActive\}/);
  assert.match(bootstrapSource, /onActiveChange\(Boolean\(activeBootstrapStatus\) \|\| busy\)/);
  assert.match(bootstrapSource, /onCloseBlockedChange\(busy\)/);
  assert.match(bootstrapSource, /未セットアップを一括セットアップ/);
  assert.match(bootstrapSource, /常駐service・listener・helper専用port・helper用env・Node Runtime Tokenは作成しません/);
  assert.match(bootstrapSource, /v1の自動セットアップは標準systemdサービス[\s\S]*?だけに対応/);
  assert.match(bootstrapSource, /Docker、非標準ポート、非標準パス、カスタム構成は手動導入/);
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
  assert.equal(systemUpdateErrorMessage({ code: "updater_offline" }), "中央Updaterがオフラインです。接続状態を確認してください。");
  assert.match(systemUpdateErrorMessage({ code: "checksum_mismatch" }), /検証に失敗/);
  assert.match(systemUpdateErrorMessage({ code: "release_version_invalid", status: 409, message: "manifest tag v1.bad" }), /公開された更新バージョン.*manifest tag v1\.bad/);
  assert.match(systemUpdateErrorMessage({ code: "download_failed", message: "GitHub returned 403 for asset X" }), /ダウンロード.*GitHub returned 403/);
  assert.match(systemUpdateErrorMessage({ code: "system_update_target_active" }), /進行中/);
  assert.match(systemUpdateErrorMessage({ code: "system_update_not_cancellable" }), /キャンセルできません/);
  assert.equal(
    systemUpdateErrorMessage({ code: "updater_host_bootstrap_in_progress" }),
    "ホストの自動セットアップ中はUpdater設定を変更できません。完了後に再試行してください。",
  );
  assert.equal(
    systemUpdateErrorMessage({ code: "bootstrap_recipient_key_changed" }),
    "Updaterのbootstrap暗号鍵が変わりました。状態を再取得し、Fingerprintを確認してから再試行してください。",
  );
  assert.match(systemUpdateErrorMessage({ status: 403 }), /権限/);
  assert.match(systemUpdateTargetBlockedReason("updater_not_configured"), /中央Updater.*設定されていません/);
  assert.equal(systemUpdateTargetBlockedReason("updater_missing"), "中央Updaterが設定されていません。");
  assert.equal(systemUpdateTargetBlockedReason("target_unreachable"), "中央Updaterから対象ホストへ接続できません。");
  assert.equal(systemUpdateTargetBlockedReason("target_reachability_unknown"), "対象ホストへの接続状態をまだ確認できません。");
  assert.match(systemUpdateTargetBlockedReason("updater_policy_pending"), /設定の反映を待っています/);
  assert.match(systemUpdateTargetBlockedReason("updater_policy_failed"), /反映できませんでした/);
  assert.match(systemUpdateTargetBlockedReason("updater_policy_mismatch"), /反映が完了していません/);
  assert.match(systemUpdateTargetBlockedReason("updater_policy_target_type_mismatch"), /サービス種別/);
  assert.match(systemUpdateTargetBlockedReason("updater_release_token_not_configured"), /GitHub Release Tokenが未設定/);
  assert.equal(systemUpdateErrorMessage({ code: "target_unreachable" }), "中央Updaterから対象ホストへ接続できません。");
  assert.equal(systemUpdateTargetBlockedReason("release_manifest_missing"), "更新用リリース情報が公開されていないため、適用できません。");
  assert.equal(systemUpdateTargetBlockedReason("release_manifest_invalid"), "更新用リリース情報を検証できないため、適用できません。");
  assert.equal(systemUpdateTargetBlockedReason("manifest_unverified"), "最新バージョンは確認できましたが、更新用リリース情報を検証できないため自動適用できません。");
  assert.equal(systemUpdateTargetBlockedReason("updater_version_incompatible"), "minimum_agent_versionを満たすように中央Updaterを更新してください。");
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
  assert.match(response.configure_command || "", /sudo \/usr\/local\/bin\/autostream-updater configure/);
  assert.doesNotMatch(response.configure_command || "", /--token|ast_cfg_/);
  assert.equal(
    ["updates.claim", "updates.report", "updates.authorize"].every((scope) => response.scopes?.includes(scope)),
    true,
  );
});

test("updater configure failure guidance requires a fresh token before restart", () => {
  const source = readFileSync(new URL("../src/features/nodes/node-registration-view.tsx", import.meta.url), "utf8");

  assert.match(source, /設定処理が失敗または結果不確定の場合は、Updaterを再起動しないでください。/);
  assert.match(source, /新しいConfigure Tokenを発行し、同じtoken-free commandを新しいTokenで再実行/);
  assert.doesNotMatch(source, /失敗または結果不確定の場合も旧Runtime Tokenは維持/);
  assert.doesNotMatch(source, /同じコマンドで再開|再生成を求められた場合だけ/);
});

test("updater configure delegates managed policy to the system update screen", () => {
  const source = readFileSync(new URL("../src/features/nodes/node-registration-view.tsx", import.meta.url), "utf8");

  assert.match(source, /中央Updaterホストで1回実行/);
  assert.match(source, /「アプリケーション情報」の中央Updater設定から登録/);
  assert.match(source, /保存後はUpdaterが自動で反映/);
  assert.match(source, /ローカル設定ファイルを手作業で編集する必要はありません/);
  assert.doesNotMatch(source, /updater\.json|known_hosts|--init-from|JSON手動設定/);
});

test("updater node description identifies its central multi-host responsibility", () => {
  const source = readFileSync(new URL("../src/features/nodes/node-registration-view.tsx", import.meta.url), "utf8");

  assert.match(source, /value: "update_agent"[^{}\r\n]*description: "各管理対象ホストのサービス更新、検証、ロールバックを中央から担当するUpdater"/);
  assert.doesNotMatch(source, /value: "update_agent"[^{}\r\n]*description: "このホストのサービス更新、検証、ロールバックを担当するUpdater"/);
});
