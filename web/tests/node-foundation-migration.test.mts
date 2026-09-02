import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { register } from "node:module";
import { join } from "node:path";
import test from "node:test";
import { fileURLToPath } from "node:url";

import type { NodeActionProjection } from "../src/features/nodes/node-action-controller.ts";

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

const {
  NODE_FOUNDATION_SOURCE_ENABLED,
  nodeActionDescriptors,
} = await import("../src/features/nodes/node-action-descriptors.ts");
const { createNodeActionController } = await import("../src/features/nodes/node-action-controller.ts");
const {
  nodeProjectionRequest,
  parseNodeActionProjectionResponse,
} = await import("../src/features/nodes/node-permission-projection.ts");
const { adoptNodeOneTimeResponse } = await import("../src/features/nodes/node-one-time-secret.ts");
const { createOneTimeSecretLifecycleOwner } = await import("../src/lib/foundation/secrets/lifecycle-owner.ts");

const allowedProjection = (revision = "v1-authority-a"): NodeActionProjection => Object.freeze({
  contractVersion: 1,
  projectionRevision: revision,
  evaluatedAt: "2026-09-02T00:00:00Z",
  action: "configure_token_regenerate",
  availability: "allowed",
  reasonCode: "allowed",
  requiredPermissions: Object.freeze(["api_tokens.create", "api_tokens.revoke"]),
  missingPermissions: Object.freeze([]),
});

test("Node A2 artifact stays source-only until A3 while declaring NOD 5/5", () => {
  assert.equal(NODE_FOUNDATION_SOURCE_ENABLED, false);
  assert.deepEqual(nodeActionDescriptors.map((descriptor) => descriptor.id), [
    "NOD-01",
    "NOD-02",
    "NOD-03",
    "NOD-04",
    "NOD-05",
  ]);
  assert.deepEqual(nodeActionDescriptors.map((descriptor) => descriptor.risk), [
    "critical",
    "critical",
    "critical",
    "high",
    "critical",
  ]);
});

test("Node route selects the A2 artifact only through the immutable A3 fence", () => {
  const webRoot = fileURLToPath(new URL("../", import.meta.url));
  const view = readFileSync(join(webRoot, "src", "features", "nodes", "node-registration-view.tsx"), "utf8");
  const artifact = readFileSync(join(webRoot, "src", "features", "nodes", "node-foundation-artifact.tsx"), "utf8");
  assert.match(view, /NODE_FOUNDATION_SOURCE_ENABLED\s*\?\s*<NodeFoundationRegistrationArtifact/);
  assert.doesNotMatch(artifact, /DangerConfirm|localStorage|sessionStorage|console\.|analytics/);
  assert.match(artifact, /HighRiskConfirmation/);
  assert.match(artifact, /ActionAvailabilityBoundary/);
  assert.match(artifact, /OneTimeSecretReveal/);
});

test("projection request and response enforce the exact mixed-version boundary", () => {
  assert.deepEqual(nodeProjectionRequest({
    action: "registration_token",
    nodeType: "worker",
    allowRuntimeSecrets: true,
    allowRemediation: false,
  }), {
    path: "/nodes/action-permissions?action=registration_token&node_type=worker&allow_runtime_secrets=true&allow_remediation=false",
    headers: { Accept: "application/json", "X-AutoStream-Contract-Major": "2" },
  });

  const headers = new Headers({
    "content-type": "application/json; charset=utf-8",
    "cache-control": "no-store, no-cache",
    pragma: "no-cache",
    "referrer-policy": "no-referrer",
    "x-autostream-contract-major": "2",
  });
  const wire = {
    contract_version: 1,
    projection_revision: "v1-authority-a",
    evaluated_at: "2026-09-02T00:00:00Z",
    action: "configure_token_regenerate",
    availability: "allowed",
    reason_code: "allowed",
    required_permissions: ["api_tokens.create", "api_tokens.revoke"],
    missing_permissions: [],
  };
  assert.equal(parseNodeActionProjectionResponse({ status: 200, headers, body: wire })?.projectionRevision, "v1-authority-a");
  for (const mutant of [
    { status: 200, headers: new Headers(headers), body: { ...wire, contract_version: 2 } },
    { status: 200, headers: new Headers({ ...Object.fromEntries(headers), "x-autostream-contract-major": "1" }), body: wire },
    { status: 200, headers: new Headers({ ...Object.fromEntries(headers), "content-type": "text/html" }), body: wire },
    { status: 200, headers: new Headers(headers), body: { ...wire, projection_revision: "" } },
    { status: 200, headers: new Headers(headers), body: { ...wire, missing_permissions: ["api_tokens.revoke", "api_tokens.revoke"] } },
  ]) assert.equal(parseNodeActionProjectionResponse(mutant), undefined);
});

test("base permission absence hides the token family with zero projection GET and mutation", async () => {
  const harness = nodeHarness({ permissions: [] });
  assert.equal(harness.controller.evaluate({ id: "NOD-02", node: node() }).visibility.kind, "hidden");
  assert.deepEqual(await harness.controller.open({ id: "NOD-02", node: node() }), { kind: "blocked", reason: "base-permission-missing" });
  assert.deepEqual(harness.calls(), { projections: 0, mutations: 0 });
});

test("additional projected permission is visible-denied and never mutates", async () => {
  const harness = nodeHarness({
    projections: [{ ...allowedProjection(), availability: "denied", reasonCode: "additional_permission_required", missingPermissions: ["api_tokens.revoke"] }],
  });
  const opened = await harness.controller.open({ id: "NOD-02", node: node() });
  assert.equal(opened.kind, "blocked");
  assert.equal(opened.kind === "blocked" ? opened.reason : "", "projection-denied");
  assert.equal(harness.controller.evaluate({ id: "NOD-02", node: node() }).availability.kind, "denied");
  assert.deepEqual(harness.calls(), { projections: 1, mutations: 0 });
});

test("projection preparation moves the shared evaluator from unknown to allowed without a mutation", async () => {
  const harness = nodeHarness();
  const intent = { id: "NOD-02" as const, node: node() };
  assert.equal(harness.controller.evaluate(intent).availability.kind, "unknown");
  await harness.controller.prepare(intent);
  assert.equal(harness.controller.evaluate(intent).availability.kind, "allowed");
  assert.deepEqual(harness.calls(), { projections: 1, mutations: 0 });
});

test("same-scope duplicate is locked before GET and authority revision changes block POST", async () => {
  const firstProjection = deferred<NodeActionProjection>();
  let projectionInvocation = 0;
  const harness = nodeHarness({
    projectionImpl: async () => {
      projectionInvocation += 1;
      return projectionInvocation === 1 ? firstProjection.promise : allowedProjection("v1-authority-b");
    },
  });
  const first = harness.controller.open({ id: "NOD-02", node: node() });
  await waitFor(() => harness.calls().projections === 1);
  assert.deepEqual(await harness.controller.open({ id: "NOD-02", node: node() }), { kind: "blocked", reason: "duplicate" });
  assert.deepEqual(harness.calls(), { projections: 1, mutations: 0 });
  firstProjection.resolve(allowedProjection());
  const opened = await first;
  assert.equal(opened.kind, "allowed");
  if (opened.kind !== "allowed") return;
  assert.deepEqual(await harness.controller.submit(opened, { confirmed: true, typedValue: "Node A" }), {
    kind: "blocked",
    reason: "authority-changed",
  });
  assert.deepEqual(harness.calls(), { projections: 2, mutations: 0 });
});

test("handler 403 and ambiguous results issue once and require explicit reconciliation", async () => {
  for (const scenario of ["forbidden", "ambiguous"] as const) {
    const harness = nodeHarness({ mutationFailure: scenario });
    const opened = await harness.controller.open({ id: "NOD-02", node: node() });
    assert.equal(opened.kind, "allowed");
    if (opened.kind !== "allowed") continue;
    const first = await harness.controller.submit(opened, { confirmed: true, typedValue: "Node A" });
    assert.equal(first.kind, scenario === "ambiguous" ? "outcome_unknown" : "failed");
    assert.deepEqual(await harness.controller.submit(opened, { confirmed: true, typedValue: "Node A" }), {
      kind: "blocked",
      reason: "reconciliation-required",
    });
    assert.equal(harness.calls().mutations, 1);
  }
});

test("all five actions preserve exact paths, methods, and typed public targets", async () => {
  const cases = [
    { id: "NOD-01", registration: { nodeType: "worker", nodeId: "node-public-1", allowRuntimeSecrets: true, allowRemediation: false }, path: "/nodes/registration-tokens", method: "POST", typed: "node-public-1" },
    { id: "NOD-02", node: node(), path: "/nodes/node-a/configure-token", method: "POST", typed: "Node A" },
    { id: "NOD-03", node: node(), path: "/nodes/node-a/rotate-token", method: "POST", typed: "Node A" },
    { id: "NOD-04", node: node(), path: "/nodes/node-a", method: "PUT", typed: undefined },
    { id: "NOD-05", node: node(), path: "/services/node-a", method: "DELETE", typed: "Node A" },
  ];
  for (const scenario of cases) {
    const requests: Array<{ path: string; method: string }> = [];
    const harness = nodeHarness({
      permissions: ["api_tokens.create", "api_tokens.revoke", "services.disable"],
      mutationImpl: async (request) => {
        requests.push(request);
        return { status: "ok" };
      },
    });
    const intent = { id: scenario.id, node: scenario.node, registration: scenario.registration, body: { safe: true } };
    const opened = await harness.controller.open(intent);
    assert.equal(opened.kind, "allowed", scenario.id);
    if (opened.kind !== "allowed") continue;
    assert.equal(opened.typedToken, scenario.typed, scenario.id);
    const result = await harness.controller.submit(opened, { confirmed: true, typedValue: scenario.typed });
    assert.equal(result.kind, "succeeded", scenario.id);
    assert.deepEqual(requests.map(({ path, method }) => ({ path, method })), [{ path: scenario.path, method: scenario.method }]);
  }
});

test("cancellation invalidates a late projection and releases the scope exactly once", async () => {
  const late = deferred<NodeActionProjection>();
  const harness = nodeHarness({ projectionImpl: async () => late.promise });
  const intent = { id: "NOD-02" as const, node: node() };
  const opening = harness.controller.open(intent);
  await waitFor(() => harness.calls().projections === 1);
  harness.controller.cancel(intent);
  harness.controller.cancel(intent);
  late.resolve(allowedProjection());
  assert.deepEqual(await opening, { kind: "blocked", reason: "cancelled" });
  assert.equal(harness.controller.isPending(intent), false);
  assert.equal((await harness.controller.open(intent)).kind, "allowed");
});

test("non-token high-risk actions block a changed fresh resource fingerprint", async () => {
  let fingerprint = JSON.stringify(["node-a", "worker", "online"]);
  let mutations = 0;
  const controller = createNodeActionController({
    getPermissions: () => ({ kind: "ready", permissions: ["api_tokens.create"] }),
    getNodeState: () => ({ kind: "ready", freshness: "fresh", fingerprint }),
    fetchProjection: async () => undefined,
    mutate: async () => { mutations += 1; },
  });
  const intent = { id: "NOD-04" as const, node: node(), body: { service_name: "Node A" } };
  const opened = await controller.open(intent);
  assert.equal(opened.kind, "allowed");
  if (opened.kind !== "allowed") return;
  fingerprint = JSON.stringify(["node-a", "worker", "degraded"]);
  assert.deepEqual(await controller.submit(opened, { confirmed: true }), { kind: "blocked", reason: "authority-changed" });
  assert.equal(mutations, 0);
});

test("Node one-time responses are concealed, detached from public state, and honor shorter backend expiry", () => {
  let now = 1_000;
  let nextTimer = 0;
  const timers = new Map<number, { callback: () => void; due: number }>();
  const owner = createOneTimeSecretLifecycleOwner({
    epochNowMs: () => now,
    monotonicNowMs: () => now,
    schedule: (callback, delayMs) => {
      nextTimer += 1;
      timers.set(nextTimer, { callback, due: now + delayMs });
      return nextTimer;
    },
    cancel: (handle) => { timers.delete(handle as number); },
  });
  const response = {
    node: node(),
    configure_token: "NODE-CONFIGURE-SECRET-MARKER",
    runtime_token: "NODE-RUNTIME-SECRET-MARKER",
    configuration_yaml: "NODE-YAML-SECRET-MARKER",
    configure_token_expires_at: new Date(now + 90_000).toISOString(),
  };
  const adopted = adoptNodeOneTimeResponse(owner, response);
  assert.equal(adopted.adopted, true);
  assert.equal(owner.getSnapshot().phase, "concealed");
  assert.equal(JSON.stringify(adopted.publicResult).includes("SECRET-MARKER"), false);
  assert.equal(owner.readRevealedValue(), undefined);
  owner.reveal();
  assert.match(JSON.stringify(owner.readRevealedValue()), /NODE-CONFIGURE-SECRET-MARKER/);
  now += 30_000;
  for (const [id, timer] of [...timers]) if (timer.due <= now) { timers.delete(id); timer.callback(); }
  assert.equal(owner.getSnapshot().warningActive, true);
  now += 60_000;
  for (const [id, timer] of [...timers]) if (timer.due <= now) { timers.delete(id); timer.callback(); }
  assert.equal(owner.getSnapshot().clearReason, "expired");
  assert.equal(owner.readRevealedValue(), undefined);
});

function nodeHarness(options: {
  permissions?: string[];
  projections?: NodeActionProjection[];
  projectionImpl?: (invocation: number) => Promise<NodeActionProjection>;
  mutationFailure?: "forbidden" | "ambiguous";
  mutationImpl?: (request: { path: string; method: string }) => Promise<unknown>;
} = {}) {
  let current = options.projections?.[0] ?? allowedProjection();
  let projections = 0;
  let mutations = 0;
  const controller = createNodeActionController({
    getPermissions: () => ({ kind: "ready", permissions: options.permissions ?? ["api_tokens.create", "api_tokens.revoke"] }),
    getNodeState: () => ({ kind: "ready", freshness: "fresh" }),
    fetchProjection: async (input) => {
      projections += 1;
      return options.projectionImpl
        ? options.projectionImpl(projections)
        : { ...current, action: input.action } as NodeActionProjection;
    },
    mutate: async (request) => {
      mutations += 1;
      if (options.mutationFailure === "forbidden") throw Object.assign(new Error("RAW"), { name: "APIError", status: 403, code: "forbidden" });
      if (options.mutationFailure === "ambiguous") throw new TypeError("RAW response lost");
      return options.mutationImpl ? options.mutationImpl(request) : { status: "ok" };
    },
  });
  return {
    controller,
    calls: () => ({ projections, mutations }),
    setProjection: (next: NodeActionProjection) => { current = next; },
  };
}

function node() {
  return { service_id: "node-a", service_type: "worker", service_name: "Node A", status: "online" };
}

function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((resolver) => { resolve = resolver; });
  return { promise, resolve };
}

async function waitFor(predicate: () => boolean) {
  for (let attempt = 0; attempt < 50; attempt += 1) {
    if (predicate()) return;
    await new Promise((resolve) => setImmediate(resolve));
  }
  assert.fail("condition not reached");
}
