import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { register } from "node:module";
import { join } from "node:path";
import test from "node:test";
import { fileURLToPath } from "node:url";

import { QueryClient } from "@tanstack/react-query";
import ts from "typescript";

import {
  assertWorkerFoundationBoundaries,
  workerRestartTriggerCompositionIssues,
} from "./helpers/workers-foundation-imports.mts";

const resolverSource = [
  "let webRootURL;",
  "export function initialize(data) { webRootURL = data.webRootURL; }",
  "export async function resolve(specifier, context, nextResolve) {",
  "  if (specifier.startsWith('@/')) {",
  "    const target = new URL('src/' + specifier.slice(2), webRootURL);",
  "    if (!/\\.[cm]?[jt]sx?$/.test(target.pathname)) target.pathname += '.ts';",
  "    return nextResolve(target.href, context);",
  "  }",
  "  return nextResolve(specifier, context);",
  "}",
].join("\n");

register(`data:text/javascript,${encodeURIComponent(resolverSource)}`, {
  parentURL: import.meta.url,
  data: { webRootURL: new URL("../", import.meta.url).href },
});

const { buildWorkerRestartDescriptor, workerRestartDuplicateKey } = await import("../src/features/workers/workers-action-descriptors.ts");
const { createWorkerRestartController } = await import("../src/features/workers/workers-action-controller.ts");
const { copyCanonicalWorkerWireList, copyCanonicalWorkerWireValue } = await import("../src/features/workers/workers-wire-normalizer.ts");
const { presentWorkerOperationalStatus, summarizeWorkerOperations } = await import("../src/features/workers/workers-status-presenter.ts");
const { workerConfigurationDescriptor } = await import("../src/features/workers/workers-configuration-descriptor.ts");
const { createWorkerConfigurationController } = await import("../src/features/workers/workers-configuration-controller.ts");

const webRoot = fileURLToPath(new URL("..", import.meta.url));
const authority = JSON.parse(readFileSync(join(webRoot, "tests", "fixtures", "workers-foundation-pilot-authority.json"), "utf8"));
const workerStatusPresenterPath = join(webRoot, "src", "features", "workers", "workers-status-presenter.ts");

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

test("Workers status pilot keeps assignment separate and excludes unknown from the healthy numerator", () => {
  const assigned = worker("assigned-worker");
  assigned.status = "assigned";
  delete assigned.health_status;
  const future = worker("future-worker");
  future.status = "future_online_v2";
  future.health_status = "future_healthy_v2";
  assert.deepEqual(presentWorkerOperationalStatus(worker("healthy-worker")), {
    known: true,
    tone: "success",
    labelKey: "statusNodeHealthy",
    icon: "heart-pulse",
  });
  assert.deepEqual(presentWorkerOperationalStatus(assigned), {
    known: true,
    tone: "info",
    labelKey: "statusNodeAssigned",
    detailKey: "statusNodeAssignedDetail",
    icon: "link",
  });
  assert.deepEqual(presentWorkerOperationalStatus(future), safeUnknownWorkerPresentation());
  const summary = summarizeWorkerOperations([worker("healthy-worker"), assigned, future]);
  assert.deepEqual(summary, {
    total: 3,
    healthy: 1,
    attention: 2,
  });
  assert.equal(Object.isFrozen(summary), true);
});

test("Worker composite status entry points are total and fail closed over hostile runtime input", () => {
  const getterReads = { status: 0, health: 0, serviceType: 0 };
  const revoked = Proxy.revocable({ status: "online", health_status: "healthy" }, {});
  revoked.revoke();
  const cyclic: Record<string, unknown> = { status: "RAW-CYCLIC-STATUS" };
  cyclic.self = cyclic;
  const hostileInputs: ReadonlyArray<readonly [string, unknown]> = [
    ["null", null],
    ["undefined", undefined],
    ["boolean", true],
    ["number", 42],
    ["string", "online"],
    ["symbol", Symbol("RAW-SYMBOL-MARKER")],
    ["bigint", 42n],
    ["array", ["RAW-ARRAY-MARKER"]],
    ["function", function hostileFunction() {}],
    ["plain empty object", {}],
    ["null prototype", Object.create(null)],
    ["throwing status getter", {
      get status() { getterReads.status += 1; throw new Error("RAW-STATUS-GETTER-MARKER"); },
      health_status: "healthy",
    }],
    ["throwing health getter", {
      status: "online",
      get health_status() { getterReads.health += 1; throw new Error("RAW-HEALTH-GETTER-MARKER"); },
    }],
    ["throwing service type getter", {
      status: "online",
      health_status: "healthy",
      get service_type() { getterReads.serviceType += 1; throw new Error("RAW-SERVICE-TYPE-GETTER-MARKER"); },
    }],
    ["throwing get proxy", new Proxy(
      { status: "online", health_status: "healthy", service_type: "worker" },
      { get() { throw new Error("RAW-PROXY-GET-MARKER"); } },
    )],
    ["throwing ownKeys proxy", new Proxy({}, { ownKeys() { throw new Error("RAW-PROXY-OWNKEYS-MARKER"); } })],
    ["revoked proxy", revoked.proxy],
    ["cyclic object", cyclic],
  ];

  for (const [name, input] of hostileInputs) {
    const presentation = presentWorkerOperationalStatus(input);
    assert.deepEqual(presentation, safeUnknownWorkerPresentation(), `${name} presentation`);
    assert.equal(Object.isFrozen(presentation), true, `${name} presentation must be frozen`);
    assert.equal(Object.values(presentation).some((value) => value === input), false, `${name} presentation retained source identity`);

    const directSummary = summarizeWorkerOperations(input);
    assert.equal(Object.isFrozen(directSummary), true, `${name} direct summary must be frozen`);
    assert.equal(directSummary.healthy, 0, `${name} direct summary became positive`);
    assert.equal(directSummary.attention, directSummary.total, `${name} direct summary did not fail closed`);

    const entrySummary = summarizeWorkerOperations([input]);
    assert.deepEqual(entrySummary, { total: 1, healthy: 0, attention: 1 }, `${name} entry summary`);
    assert.equal(Object.isFrozen(entrySummary), true, `${name} entry summary must be frozen`);
    assert.equal(Object.values(entrySummary).some((value) => value === input), false, `${name} summary retained source identity`);
    assert.doesNotMatch(
      JSON.stringify({ presentation, directSummary, entrySummary }),
      /RAW-|statusNodeOnline|statusNodeHealthy|statusNodeAssigned|ready|success/,
      `${name} exposed or positively classified hostile input`,
    );
  }

  assert.deepEqual(getterReads, { status: 0, health: 0, serviceType: 0 }, "descriptor validation must not invoke accessors");
});

test("Worker composite totality AST oracle rejects direct boundary property access mutants", () => {
  const source = readFileSync(workerStatusPresenterPath, "utf8").replace(/\r\n/g, "\n");
  assert.deepEqual(workerCompositeTotalityIssues(source), []);

  for (const property of ["status", "health_status"] as const) {
    const mutant = replaceWorkerViewExactlyOnce(
      source,
      "export function presentWorkerOperationalStatus(input: unknown): DomainStatusPresentation {\n",
      `export function presentWorkerOperationalStatus(input: unknown): DomainStatusPresentation {\n  const unsafe = input.${property};\n`,
    );
    const issues = workerCompositeTotalityIssues(mutant);
    assert.equal(
      issues.includes("direct-hostile-property-access"),
      true,
      `input.${property} mutant was accepted: ${JSON.stringify(issues)}`,
    );
  }
});

test("same-worker duplicate activations latch before authority GET while different workers remain independent", async () => {
  const firstPost = deferred();
  const secondPost = deferred();
  const harness = restartHarness({
    post: (path) => path.includes("worker-1") ? firstPost.promise : secondPost.promise,
  });
  harness.setCachedWorkers([worker("worker-1"), worker("worker-2")]);
  harness.setFreshWorkers([worker("worker-1"), worker("worker-2")]);
  const openOne = allowedOpen(harness.controller, worker("worker-1"));
  const openTwo = allowedOpen(harness.controller, worker("worker-2"));

  const first = harness.controller.submit(openOne);
  await waitFor(() => harness.postInvocations.length === 1);
  const duplicate = await harness.controller.submit(openOne);
  assert.equal(duplicate.state.kind, "revalidation-unavailable");
  assert.deepEqual(harness.calls(), { gets: 1, posts: 1, invalidations: 0 });

  const second = harness.controller.submit(openTwo);
  await waitFor(() => harness.postInvocations.length === 2);
  assert.equal(harness.postInvocations[1].path, "/workers/worker-2/restart");
  firstPost.resolve({ status: "accepted" });
  secondPost.resolve({ status: "accepted" });
  await Promise.all([first, second]);
  assert.deepEqual(harness.calls(), { gets: 2, posts: 2, invalidations: 2 });
});

test("403, 409 and transport ambiguity never resend a non-idempotent restart", async () => {
  for (const scenario of [
    { name: "403", error: apiError(403, "permission_denied"), expectedState: "failed", expectedOutcome: "failed", expectedGets: 1 },
    { name: "409", error: apiError(409, "worker_busy"), expectedState: "conflict", expectedOutcome: "failed", expectedGets: 2 },
    { name: "network", error: new TypeError("RAW NETWORK MARKER"), expectedState: "outcome-unknown", expectedOutcome: "outcome_unknown", expectedGets: 1 },
    { name: "timeout", error: Object.assign(new Error("RAW TIMEOUT MARKER"), { name: "TimeoutError" }), expectedState: "outcome-unknown", expectedOutcome: "outcome_unknown", expectedGets: 1 },
    { name: "protocol", error: apiError(200, "non_json_response"), expectedState: "outcome-unknown", expectedOutcome: "outcome_unknown", expectedGets: 1 },
  ]) {
    const harness = restartHarness({ post: async () => { throw scenario.error; } });
    const result = await harness.controller.submit(allowedOpen(harness.controller, worker("worker-1")));
    assert.equal(result.state.kind, scenario.expectedState, scenario.name);
    assert.equal(result.outcome?.kind, scenario.expectedOutcome, scenario.name);
    assert.deepEqual(harness.calls(), { gets: scenario.expectedGets, posts: 1, invalidations: 0 }, scenario.name);
    if (scenario.name === "403") assert.equal("error" in result.state ? result.state.error.kind : undefined, "forbidden");
    if (scenario.name === "409") assert.equal("error" in result.state ? result.state.error.kind : undefined, "conflict");
    assert.equal(JSON.stringify(result).includes("RAW"), false, `${scenario.name} retained a raw error marker`);
  }
});

test("Configuration descriptor preserves exact server ANY permission independently of page access", () => {
  assert.deepEqual(workerConfigurationDescriptor, {
    labelKey: "workerConfigurationAction",
    permissions: { kind: "any", permissions: ["service_health.read", "api_tokens.create"] },
    disclosure: "visible-denied",
  });
  assert.deepEqual(authority.configuration, {
    method: "GET",
    pathTemplate: "/nodes/{encoded_id}/configuration",
    permission: { kind: "any", permissions: ["service_health.read", "api_tokens.create"] },
    pagePermission: "workers.read",
    queryKey: null,
  });
  const harness = configurationHarness();
  for (const [permissions, expected] of [
    [["service_health.read"], "allowed"],
    [["api_tokens.create"], "allowed"],
    [["service_health.read", "api_tokens.create"], "allowed"],
    [["*"], "allowed"],
    [[], "denied"],
    [["workers.read"], "denied"],
  ]) {
    harness.setAuth(permissions);
    assert.equal(harness.controller.evaluate().availability.kind, expected, permissions.join(","));
  }
});

test("Configuration handler re-evaluates latest auth and sends no GET when denied or unknown", async () => {
  const denied = configurationHarness();
  denied.setAuth(["workers.read"]);
  await denied.controller.select("node-1");
  assert.equal(denied.calls(), 0);
  assert.equal(denied.controller.getSnapshot().evaluation.availability.kind, "denied");

  const removed = configurationHarness();
  assert.equal(removed.controller.evaluate().availability.kind, "allowed");
  removed.setAuth([]);
  await removed.controller.select("node-1");
  assert.equal(removed.calls(), 0);

  const refreshing = configurationHarness();
  const authRefresh = deferred();
  const refreshPromise = refreshing.queryClient.fetchQuery({ queryKey: ["auth", "me"], queryFn: () => authRefresh.promise });
  await waitFor(() => refreshing.controller.evaluate().availability.kind === "unknown");
  await refreshing.controller.select("node-1");
  assert.equal(refreshing.calls(), 0);
  authRefresh.resolve(auth(["service_health.read"]));
  await refreshPromise;
});

test("Configuration target generation fence never renders old target data as the new target", async () => {
  const nodeA = deferred();
  const nodeB = deferred();
  const harness = configurationHarness({
    get: (path) => path.includes("node-a") ? nodeA.promise : nodeB.promise,
  });
  const first = harness.controller.select("node-a");
  await waitFor(() => harness.calls() === 1);
  const second = harness.controller.select("node-b");
  assert.equal(harness.controller.getSnapshot().targetId, "node-b");
  assert.equal(harness.controller.getSnapshot().state.kind, "initial-loading");
  nodeA.resolve(configuration("node-a", "CONFIG-A-MARKER"));
  await first;
  assert.equal(JSON.stringify(harness.controller.getSnapshot()).includes("CONFIG-A-MARKER"), false);
  nodeB.resolve(configuration("node-b", "CONFIG-B-MARKER"));
  await second;
  assert.equal(JSON.stringify(harness.controller.getSnapshot()).includes("CONFIG-B-MARKER"), true);
});

test("Configuration rejects a response whose node does not match the selected target", async () => {
  const harness = configurationHarness({
    get: async () => configuration("different-node", "WRONG-TARGET-CONFIG-MARKER"),
  });
  await harness.controller.select("node-1");
  const snapshot = harness.controller.getSnapshot();
  assert.equal(snapshot.targetId, "node-1");
  assert.equal(snapshot.state.kind, "blocking-error");
  assert.equal(JSON.stringify(snapshot).includes("WRONG-TARGET-CONFIG-MARKER"), false);
  assert.equal(harness.calls(), 1);
});

test("same-target refresh preserves cached configuration through pending and safe stale error", async () => {
  const refresh = deferred();
  let invocation = 0;
  const harness = configurationHarness({
    get: async () => {
      invocation += 1;
      if (invocation === 1) return configuration("node-1", "CACHED-CONFIG-MARKER");
      return refresh.promise;
    },
  });
  await harness.controller.select("node-1");
  const pending = harness.controller.refresh();
  assert.equal(harness.controller.getSnapshot().state.kind, "ready");
  assert.equal(harness.controller.getSnapshot().state.freshness.kind, "refreshing");
  assert.equal(JSON.stringify(harness.controller.getSnapshot()).includes("CACHED-CONFIG-MARKER"), true);
  refresh.reject(new TypeError("RAW CONFIGURATION ERROR MARKER"));
  await pending;
  const stale = harness.controller.getSnapshot();
  assert.equal(stale.state.kind, "ready");
  assert.equal(stale.state.freshness.kind, "stale");
  assert.equal(JSON.stringify(stale).includes("CACHED-CONFIG-MARKER"), true);
  assert.equal(JSON.stringify(stale).includes("RAW CONFIGURATION ERROR MARKER"), false);
});

test("Configuration distinguishes empty, malformed blocking error and safe manual retry", async () => {
  const empty = configurationHarness({ get: async () => ({ node: worker("node-1") }) });
  await empty.controller.select("node-1");
  assert.equal(empty.controller.getSnapshot().state.kind, "empty");

  const malformed = configurationHarness({ get: async () => ({ node: worker("node-1"), configuration_yaml: 42 }) });
  await malformed.controller.select("node-1");
  assert.equal(malformed.controller.getSnapshot().state.kind, "blocking-error");

  let attempt = 0;
  const retry = configurationHarness({ get: async () => {
    attempt += 1;
    if (attempt === 1) throw apiError(500, "get_node_failed");
    return configuration("node-1", "RETRY-CONFIG-MARKER");
  } });
  await retry.controller.select("node-1");
  assert.equal(retry.controller.getSnapshot().state.kind, "blocking-error");
  await retry.controller.refresh();
  assert.equal(retry.controller.getSnapshot().state.kind, "ready");
  assert.equal(retry.calls(), 2);
});

test("service_id-only Configuration becomes ready with a detached canonical node", async () => {
  const sourceNode = worker("worker-real-shape-1", "Worker A");
  const harness = configurationHarness({
    get: async () => ({
      node: sourceNode,
      node_api_url: "https://worker.invalid",
      configuration_yaml: "safe-fixture",
      configure_command: "safe-fixture",
      systemd_unit: "[Unit]",
    }),
  });
  await harness.controller.select("worker-real-shape-1");
  const snapshot = harness.controller.getSnapshot();
  assert.equal(harness.calls(), 1);
  assert.equal(snapshot.state.kind, "ready");
  assert.equal(snapshot.state.data.node.id, "worker-real-shape-1");
  assert.equal(snapshot.state.data.node.service_id, "worker-real-shape-1");
  assert.notEqual(snapshot.state.data.node, sourceNode);
  assert.equal(snapshot.state.data.configuration_yaml, "safe-fixture");
  assert.equal(snapshot.state.data.configure_command, "safe-fixture");
  assert.equal(snapshot.state.data.systemd_unit, "[Unit]");
});

test("AST guards reject DangerConfirm, secret imports, endpoint descriptors, retries and global locks", () => {
  assert.deepEqual(assertWorkerFoundationBoundaries(webRoot), { descriptorFiles: 2, controllerFiles: 2, normalizerFiles: 1 });
});

test("Worker restart AST oracle rejects ref, manual-open, wrapper and interactive trigger mutants", () => {
  const production = readFileSync(join(webRoot, "src", "features", "workers", "workers-view.tsx"), "utf8")
    .replace(/\r\n/g, "\n");
  assert.deepEqual(workerRestartTriggerCompositionIssues(production), []);

  const wrapTrigger = (source: string, wrapperOpen: string, wrapperClose: string) => {
    const opened = replaceWorkerViewExactlyOnce(
      source,
      "              trigger={(confirmationProps) => (\n                <Button\n",
      `              trigger={(confirmationProps) => (\n                ${wrapperOpen}\n                  <Button\n`,
    );
    return replaceWorkerViewExactlyOnce(
      opened,
      "                  <span>{translate(\"restart\")}</span>\n                </Button>\n              )}",
      `                  <span>{translate(\"restart\")}</span>\n                  </Button>\n                ${wrapperClose}\n              )}`,
    );
  };
  const withManualOnClick = (source: string, expression: string) => replaceWorkerViewExactlyOnce(
    source,
    "                  ref={restartTriggerRef}\n",
    `                  ref={restartTriggerRef}\n                  onClick={${expression}}\n`,
  );

  const extractedHelper = replaceWorkerViewExactlyOnce(
    production,
    "  return (\n    <div className=\"flex items-center gap-2\">",
    "  const openRestartFromTrigger = () => openRestart(row.original);\n\n  return (\n    <div className=\"flex items-center gap-2\">",
  );
  const movedAvailability = wrapTrigger(
    replaceWorkerViewExactlyOnce(
      replaceWorkerViewExactlyOnce(
        production,
        "        <ActionAvailabilityBoundary\n          evaluation={restartEvaluation}",
        "        <MovedAvailabilityBoundary\n          evaluation={restartEvaluation}",
      ),
      "        </ActionAvailabilityBoundary>\n      ) : null}",
      "        </MovedAvailabilityBoundary>\n      ) : null}",
    ),
    "<ActionAvailabilityBoundary>",
    "</ActionAvailabilityBoundary>",
  );

  const mutants = [
    {
      name: "restartTriggerRef removed",
      source: replaceWorkerViewExactlyOnce(production, "                  ref={restartTriggerRef}\n", ""),
      expectedIssue: "restart-trigger-ref",
    },
    {
      name: "restartTriggerRef moved to wrapper",
      source: wrapTrigger(
        replaceWorkerViewExactlyOnce(production, "                  ref={restartTriggerRef}\n", ""),
        "<div ref={restartTriggerRef}>",
        "</div>",
      ),
      expectedIssue: "immediate-trigger-button",
    },
    {
      name: "different ref substituted",
      source: replaceWorkerViewExactlyOnce(production, "ref={restartTriggerRef}", "ref={differentTriggerRef}"),
      expectedIssue: "restart-trigger-ref",
    },
    {
      name: "manual open onClick added",
      source: withManualOnClick(production, "() => openRestart(row.original)"),
      expectedIssue: "manual-open-onclick",
    },
    {
      name: "manual open callback extracted to helper",
      source: withManualOnClick(extractedHelper, "openRestartFromTrigger"),
      expectedIssue: "manual-open-onclick",
    },
    {
      name: "wrapper added",
      source: wrapTrigger(production, "<div>", "</div>"),
      expectedIssue: "immediate-trigger-button",
    },
    {
      name: "nested button added",
      source: replaceWorkerViewExactlyOnce(
        production,
        "                  <span>{translate(\"restart\")}</span>\n",
        "                  <span>{translate(\"restart\")}</span>\n                  <button type=\"button\">Nested</button>\n",
      ),
      expectedIssue: "interactive-trigger-descendant",
    },
    {
      name: "nested link added",
      source: replaceWorkerViewExactlyOnce(
        production,
        "                  <span>{translate(\"restart\")}</span>\n",
        "                  <span>{translate(\"restart\")}</span>\n                  <a href=\"#nested\">Nested</a>\n",
      ),
      expectedIssue: "interactive-trigger-descendant",
    },
    {
      name: "two trigger children",
      source: wrapTrigger(production, "<>", "<Button>Second trigger</Button>\n                </>"),
      expectedIssue: "immediate-trigger-button",
    },
    {
      name: "HighRiskConfirmation removed",
      source: replaceWorkerViewExactlyOnce(production, "<HighRiskConfirmation\n", "<RemovedHighRiskConfirmation\n"),
      expectedIssue: "high-risk-confirmation-count",
    },
    {
      name: "availability boundary moved inside trigger",
      source: movedAvailability,
      expectedIssue: "availability-boundary-not-outer",
    },
  ];

  for (const mutant of mutants) {
    const issues = workerRestartTriggerCompositionIssues(mutant.source, `${mutant.name}.tsx`);
    assert.equal(
      issues.includes(mutant.expectedIssue),
      true,
      `${mutant.name} was accepted by the Worker restart AST oracle: ${JSON.stringify(issues)}`,
    );
  }
});

function replaceWorkerViewExactlyOnce(source: string, before: string, after: string) {
  const index = source.indexOf(before);
  assert.notEqual(index, -1, `Worker mutation source was not found: ${before}`);
  assert.equal(source.indexOf(before, index + before.length), -1, `Worker mutation source was not unique: ${before}`);
  return `${source.slice(0, index)}${after}${source.slice(index + before.length)}`;
}

function safeUnknownWorkerPresentation() {
  return {
    known: false,
    tone: "unknown",
    labelKey: "statusUnknown",
    detailKey: "statusUnknownDetail",
    icon: "circle-help",
  } as const;
}

function workerCompositeTotalityIssues(source: string) {
  const issues = new Set<string>();
  const sourceFile = ts.createSourceFile(
    "workers-status-presenter.ts",
    source,
    ts.ScriptTarget.Latest,
    true,
    ts.ScriptKind.TS,
  );
  const exportedEntrypoints = new Set(["presentWorkerOperationalStatus", "summarizeWorkerOperations"]);
  for (const statement of sourceFile.statements) {
    if (!ts.isFunctionDeclaration(statement) || !statement.name || !exportedEntrypoints.has(statement.name.text)) continue;
    const exported = statement.modifiers?.some((modifier) => modifier.kind === ts.SyntaxKind.ExportKeyword) ?? false;
    if (!exported || statement.parameters.length !== 1
      || statement.parameters[0].type?.kind !== ts.SyntaxKind.UnknownKeyword) {
      issues.add(`unknown-boundary:${statement.name.text}`);
    }
    exportedEntrypoints.delete(statement.name.text);
  }
  for (const missing of exportedEntrypoints) issues.add(`missing-entrypoint:${missing}`);

  const directKeys = new Set(["status", "health_status", "service_type"]);
  const visit = (node: ts.Node) => {
    let objectExpression: ts.Expression | undefined;
    let propertyName: string | undefined;
    if (ts.isPropertyAccessExpression(node)) {
      objectExpression = node.expression;
      propertyName = node.name.text;
    } else if (ts.isElementAccessExpression(node)
      && node.argumentExpression
      && ts.isStringLiteral(node.argumentExpression)) {
      objectExpression = node.expression;
      propertyName = node.argumentExpression.text;
    }
    if (objectExpression && propertyName && directKeys.has(propertyName) && ts.isIdentifier(objectExpression)) {
      let parent: ts.Node | undefined = node.parent;
      while (parent && !ts.isFunctionLike(parent)) parent = parent.parent;
      const parameterNames = parent && ts.isFunctionLike(parent)
        ? parent.parameters
          .map((parameter) => ts.isIdentifier(parameter.name) ? parameter.name.text : undefined)
          .filter((name): name is string => name !== undefined)
        : [];
      if (parameterNames.includes(objectExpression.text)) issues.add("direct-hostile-property-access");
    }
    ts.forEachChild(node, visit);
  };
  visit(sourceFile);
  return [...issues].sort();
}

function restartHarness(options = {}) {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  let freshWorkers = [worker("worker-1")];
  let gets = 0;
  let posts = 0;
  let invalidations = 0;
  const postInvocations = [];
  const invalidatedKeys = [];
  queryClient.setQueryData(["auth", "me"], auth(["workers.restart"]));
  queryClient.setQueryData(["workers"], freshWorkers);
  const originalInvalidate = queryClient.invalidateQueries.bind(queryClient);
  queryClient.invalidateQueries = async (filters) => {
    invalidations += 1;
    invalidatedKeys.push(filters.queryKey);
    if (options.invalidateFailure) throw new TypeError("RAW INVALIDATION MARKER");
    return originalInvalidate({ ...filters, refetchType: "none" });
  };
  const controller = createWorkerRestartController({
    queryClient,
    fetchWorkers: async () => {
      gets += 1;
      if (options.fetchFailure) throw new TypeError("RAW AUTHORITY GET MARKER");
      return freshWorkers;
    },
    postRestart: async function postRestart(path) {
      posts += 1;
      postInvocations.push({ path, argumentCount: arguments.length });
      return options.post ? options.post(path) : { status: "accepted" };
    },
  });
  return {
    queryClient,
    controller,
    postInvocations,
    invalidatedKeys,
    calls: () => ({ gets, posts, invalidations }),
    setAuth: (permissions) => queryClient.setQueryData(["auth", "me"], auth(permissions)),
    setCachedWorkers: (workers) => queryClient.setQueryData(["workers"], workers),
    setFreshWorkers: (workers) => { freshWorkers = workers; },
  };
}

function configurationHarness(options = {}) {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  let gets = 0;
  queryClient.setQueryData(["auth", "me"], auth(["service_health.read"]));
  const controller = createWorkerConfigurationController({
    queryClient,
    getConfiguration: async (path) => {
      gets += 1;
      return options.get ? options.get(path) : configuration("node-1", "CONFIGURATION-MARKER");
    },
  });
  return {
    queryClient,
    controller,
    calls: () => gets,
    setAuth: (permissions) => queryClient.setQueryData(["auth", "me"], auth(permissions)),
  };
}

function allowedOpen(controller, target) {
  const result = controller.open(target);
  assert.equal(result.kind, "allowed");
  return result;
}

function worker(id, name = "Worker", serviceType = "worker") {
  return { service_id: id, service_type: serviceType, service_name: name, status: "online", health_status: "healthy" };
}

function auth(permissions) {
  return { user: { id: "user-1", username: "operator" }, permissions };
}

function configuration(id, yaml) {
  return {
    node: worker(id, `Node ${id}`),
    node_api_url: `https://${id}.example.invalid`,
    configure_command: `configure ${id}`,
    configuration_yaml: yaml,
    systemd_unit: `[Unit]\nDescription=${id}`,
  };
}

function apiError(status, code) {
  return Object.assign(new Error("RAW API MARKER"), { name: "APIError", status, code, responseContentType: "application/json" });
}

function deferred() {
  let resolve;
  let reject;
  const promise = new Promise((resolver, rejecter) => { resolve = resolver; reject = rejecter; });
  return { promise, resolve, reject };
}

async function waitFor(predicate) {
  for (let attempt = 0; attempt < 50; attempt += 1) {
    if (predicate()) return;
    await new Promise((resolve) => setImmediate(resolve));
  }
  assert.fail("condition was not reached");
}
