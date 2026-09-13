import assert from "node:assert/strict";
import test from "node:test";
import { baseTarget, runSystemUpdatesSequentially, requestSystemUpdateWithRecovery, requestUpdaterHostBootstrapWithRecovery, UpdaterHostBootstrapRequestAmbiguousError, updaterHostBootstrapRequestIdentity, recoverUpdaterHostBootstrapRequest } from "./system-updates-fixture.mts";


export function registerRequestRecoveryCases() {


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
}
