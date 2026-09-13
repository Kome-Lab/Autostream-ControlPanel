import assert from "node:assert/strict";
import test from "node:test";
import { authority, buildWorkerRestartDescriptor, worker, workerRestartDuplicateKey, copyCanonicalWorkerWireValue, copyCanonicalWorkerWireList, restartHarness, allowedOpen, deferred, waitFor, auth } from "./workers-pilot-fixture.mts";


export function registerWorkerRestartAuthorityCases() {


test("WKR-01 fixture and descriptor freeze the exact method-independent action authority", () => {
  assert.deepEqual(authority.restart, {
    actionId: "WKR-01",
    featureAction: "workers.restart",
    method: "POST",
    pathTemplate: "/workers/{encoded_id}/restart",
    permission: { kind: "all", permissions: ["workers.restart"] },
    risk: "high",
    confirmation: "consequence",
    duplicate: { scope: "resource-action", keyTemplate: "worker:{worker_id}:restart" },
    retry: { kind: "never", automaticAttempts: 0 },
    auditAction: "workers.restart",
    requestBody: "none",
  });
  const descriptor = buildWorkerRestartDescriptor(worker("worker/a", "Primary Worker"));
  assert.deepEqual(descriptor, {
    id: "WKR-01",
    labelKey: "workerRestartAction",
    risk: "high",
    target: { resourceType: "worker", resourceId: "worker/a", publicLabel: "Primary Worker", publicStableId: "worker/a" },
    permissions: { kind: "all", permissions: ["workers.restart"] },
    applicability: { ruleIds: ["worker-restart-target"], requiredSections: ["worker"] },
    confirmation: { mode: "consequence", consequenceKey: "workerRestartConsequence", requireSubmitRevalidation: true },
    duplicate: { scope: "resource-action", whilePending: "block" },
    retry: { kind: "never" },
    audit: { action: "workers.restart", labelKey: "workerRestartAudit", safeReferenceFieldIds: ["resourceId"] },
    stateIndependent: false,
    revalidation: { kind: "safe-fingerprint", fieldIds: ["canonicalWorkerId", "serviceType"] },
  });
  assert.equal(Object.isFrozen(descriptor), true);
  assert.equal(Object.isFrozen(descriptor.permissions.permissions), true);
  assert.equal(workerRestartDuplicateKey(worker("worker/a")), "worker:worker/a:restart");
});

test("canonical Worker wire normalization owns service_id and returns a detached frozen allowlist", () => {
  const source = {
    service_id: "worker-real-shape-1",
    service_type: "worker",
    service_name: "Worker A",
    status: "online",
    health_status: "healthy",
    assignment_role: "primary",
    current_stream_id: "stream-1",
    reported_version: "v1.2.3",
    reported_os: "linux",
    reported_arch: "amd64",
    last_heartbeat_at: "2026-08-28T00:00:00Z",
    heartbeat_age_sec: 4,
    capabilities: { codecs: ["h264"], nested: { enabled: true } },
    reported_capabilities: { jobs: 2 },
    metrics: { active_jobs: 1, state: "ready" },
    raw_unknown_marker: "RAW-WORKER-UNKNOWN-MARKER",
  };
  const canonical = copyCanonicalWorkerWireValue(source);
  assert.ok(canonical);
  assert.equal(canonical.id, source.service_id);
  assert.equal(canonical.service_id, source.service_id);
  assert.equal(canonical.service_type, source.service_type);
  assert.equal(Object.isFrozen(canonical), true);
  assert.equal(Object.isFrozen(canonical.capabilities), true);
  assert.equal(Object.isFrozen(canonical.capabilities.codecs), true);
  assert.equal(Object.isFrozen(canonical.metrics), true);
  assert.notEqual(canonical, source);
  assert.notEqual(canonical.capabilities, source.capabilities);
  assert.equal("raw_unknown_marker" in canonical, false);
  source.capabilities.nested.enabled = false;
  source.metrics.active_jobs = 9;
  assert.equal(canonical.capabilities.nested.enabled, true);
  assert.equal(canonical.metrics.active_jobs, 1);

  const compatible = copyCanonicalWorkerWireValue({ ...worker("worker-compatible"), id: "worker-compatible" });
  assert.equal(compatible?.id, "worker-compatible");
  assert.equal(copyCanonicalWorkerWireValue({ ...worker("worker-mismatch"), id: "different-worker" }), undefined);
  assert.equal(copyCanonicalWorkerWireValue({ id: "legacy-only", service_type: "worker", service_name: "Legacy", status: "online" }), undefined);
  assert.equal(copyCanonicalWorkerWireValue({ ...worker(""), service_id: "" }), undefined);
  assert.equal(copyCanonicalWorkerWireValue({ ...worker("worker-number"), service_id: 7 }), undefined);
  assert.equal(copyCanonicalWorkerWireValue({ service_id: "worker-missing-type", service_name: "Missing", status: "online" }), undefined);

  const throwingGetter = Object.defineProperty({
    service_type: "worker",
    service_name: "Throwing",
    status: "online",
  }, "service_id", { enumerable: true, get() { throw new Error("RAW GETTER MARKER"); } });
  const throwingProxy = new Proxy(worker("worker-proxy"), {
    ownKeys() { throw new Error("RAW PROXY MARKER"); },
  });
  const revoked = Proxy.revocable(worker("worker-revoked"), {});
  revoked.revoke();
  class WorkerRecord {
    service_id = "worker-class";
    service_type = "worker";
    service_name = "Class";
    status = "online";
  }
  for (const malformed of [null, undefined, [], new Date(), new Map(), new WorkerRecord(), throwingGetter, throwingProxy, revoked.proxy]) {
    assert.doesNotThrow(() => copyCanonicalWorkerWireValue(malformed));
    assert.equal(copyCanonicalWorkerWireValue(malformed), undefined);
  }
  assert.equal(copyCanonicalWorkerWireList([worker("worker-1"), worker("worker-2")])?.length, 2);
  assert.equal(copyCanonicalWorkerWireList([worker("worker-1"), { ...worker("worker-2"), id: "mismatch" }]), undefined);
});

test("open and submit use current permissions plus a fresh target fingerprint", async () => {
  const deniedHarness = restartHarness();
  deniedHarness.setAuth([]);
  assert.equal(deniedHarness.controller.open(worker("worker-1")).kind, "blocked");
  assert.deepEqual(deniedHarness.calls(), { gets: 0, posts: 0, invalidations: 0 });

  const permissionRemoved = restartHarness();
  const opened = allowedOpen(permissionRemoved.controller, worker("worker-1"));
  permissionRemoved.setAuth([]);
  const permissionResult = await permissionRemoved.controller.submit(opened);
  assert.equal(permissionResult.state.kind, "revalidation-unavailable");
  assert.deepEqual(permissionRemoved.calls(), { gets: 0, posts: 0, invalidations: 0 });

  const permissionRefreshing = restartHarness();
  const authRefresh = deferred();
  const refreshPromise = permissionRefreshing.queryClient.fetchQuery({
    queryKey: ["auth", "me"],
    queryFn: () => authRefresh.promise,
  });
  await waitFor(() => permissionRefreshing.controller.evaluate(worker("worker-1")).availability.kind === "unknown");
  assert.equal(permissionRefreshing.controller.open(worker("worker-1")).kind, "blocked");
  assert.deepEqual(permissionRefreshing.calls(), { gets: 0, posts: 0, invalidations: 0 });
  authRefresh.resolve(auth(["workers.restart"]));
  await refreshPromise;

  const targetRemoved = restartHarness();
  const removedOpen = allowedOpen(targetRemoved.controller, worker("worker-1"));
  targetRemoved.setFreshWorkers([]);
  const missingResult = await targetRemoved.controller.submit(removedOpen);
  assert.equal(missingResult.state.kind, "stale-blocked");
  assert.deepEqual(targetRemoved.calls(), { gets: 1, posts: 0, invalidations: 0 });

  const typeChanged = restartHarness();
  const typeOpen = allowedOpen(typeChanged.controller, worker("worker-1"));
  typeChanged.setFreshWorkers([worker("worker-1", "Worker", "encoder_recorder")]);
  const typeResult = await typeChanged.controller.submit(typeOpen);
  assert.equal(typeResult.state.kind, "stale-blocked");
  assert.deepEqual(typeChanged.calls(), { gets: 1, posts: 0, invalidations: 0 });

  const refetchFailed = restartHarness({ fetchFailure: true });
  const refetchOpen = allowedOpen(refetchFailed.controller, worker("worker-1"));
  const refetchResult = await refetchFailed.controller.submit(refetchOpen);
  assert.equal(refetchResult.state.kind, "revalidation-unavailable");
  assert.deepEqual(refetchFailed.calls(), { gets: 1, posts: 0, invalidations: 0 });
});

test("success sends one bodyless encoded POST and invalidates the existing workers key once", async () => {
  const harness = restartHarness();
  harness.setCachedWorkers([worker("worker/a")]);
  harness.setFreshWorkers([worker("worker/a")]);
  assert.equal(harness.controller.evaluate(worker("worker/a")).availability.kind, "allowed");
  const opened = allowedOpen(harness.controller, worker("worker/a"));
  const result = await harness.controller.submit(opened);
  assert.equal(result.outcome?.kind, "succeeded");
  assert.deepEqual(harness.postInvocations, [{ path: "/workers/worker%2Fa/restart", argumentCount: 1 }]);
  assert.deepEqual(harness.invalidatedKeys, [["workers"]]);
  assert.deepEqual(harness.calls(), { gets: 1, posts: 1, invalidations: 1 });

  const refreshFailure = restartHarness({ invalidateFailure: true });
  const accepted = await refreshFailure.controller.submit(allowedOpen(refreshFailure.controller, worker("worker-1")));
  assert.equal(accepted.outcome?.kind, "succeeded", "a failed follow-up refresh must not erase a known accepted POST outcome");
  assert.deepEqual(refreshFailure.calls(), { gets: 1, posts: 1, invalidations: 1 });
});
}
