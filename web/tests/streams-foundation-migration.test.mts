import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { register } from "node:module";
import test from "node:test";

import type {
  StreamActionIntent,
} from "../src/features/streams/stream-action-descriptors.ts";
import type {
  StreamActionStateSnapshot,
} from "../src/features/streams/stream-action-controller.ts";

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
  buildStreamActionDescriptor,
  buildStreamActionPermissionRequirement,
  streamActionDescriptors,
  streamActionRequest,
} = await import("../src/features/streams/stream-action-descriptors.ts");
const {
  createStreamActionController,
} = await import("../src/features/streams/stream-action-controller.ts");

const stream = (status = "ready") => ({
  id: "stream-a",
  name: "Public Stream A",
  status,
  updated_at: "2026-09-02T00:00:00Z",
});

test("Streams declares the canonical STR 11/11 action mapping", () => {
  assert.deepEqual(streamActionDescriptors.map(({ id }) => id), [
    "STR-01", "STR-02", "STR-03", "STR-04", "STR-05", "STR-06",
    "STR-07", "STR-08", "STR-09", "STR-10", "STR-11",
  ]);
  assert.deepEqual(streamActionDescriptors.map(({ risk }) => risk), [
    "guarded", "guarded", "high", "high", "high", "critical",
    "critical", "guarded", "guarded", "critical", "routine",
  ]);
});

test("STR routes, methods, and fixed payloads preserve server authority", () => {
  const cases: Array<[StreamActionIntent, unknown]> = [
    [{ id: "STR-01", payload: { name: "Public Stream A" }, publicLabel: "Public Stream A" }, ["POST", "/streams", { name: "Public Stream A" }]],
    [{ id: "STR-02", stream: stream(), payload: { encoder_service_id: "" } }, ["PUT", "/streams/stream-a/settings", { encoder_service_id: "" }]],
    [{ id: "STR-03", stream: stream("live"), payload: { encoder_audio_gain_db: 1 } }, ["PUT", "/streams/stream-a/runtime-settings", { encoder_audio_gain_db: 1 }]],
    [{ id: "STR-04", stream: stream() }, ["POST", "/streams/stream-a/start", undefined]],
    [{ id: "STR-05", stream: stream("live") }, ["POST", "/streams/stream-a/stop", undefined]],
    [{ id: "STR-06", stream: stream("stopping") }, ["POST", "/streams/stream-a/force-stop", undefined]],
    [{ id: "STR-07", stream: stream("failed"), staticRelayRecoveryAvailable: true }, ["POST", "/streams/stream-a/youtube/relay-static/recovery/resolve", { confirm_external_cleanup: true }]],
    [{ id: "STR-08", stream: stream() }, ["POST", "/streams/stream-a/start-readiness", undefined]],
    [{ id: "STR-09", stream: stream() }, ["POST", "/streams/stream-a/worker-events/test", { event_type: "current_time" }]],
    [{ id: "STR-10", stream: stream("completed") }, ["DELETE", "/streams/stream-a", undefined]],
    [{ id: "STR-11", stream: stream("live") }, ["POST", "/streams/stream-a/preview-links", undefined]],
  ];
  for (const [intent, expected] of cases) {
    const request = streamActionRequest(intent);
    assert.deepEqual(request && [request.method, request.path, request.body], expected, intent.id);
  }
});

test("STR-02 permission builder treats explicit empty and unchanged fields as present", () => {
  const cases: Array<[Record<string, unknown>, string[]]> = [
    [{}, ["streams.update"]],
    [{ encoder_service_id: "encoder-a" }, ["streams.update", "services.assign"]],
    [{ encoder_service_id: "" }, ["streams.update", "services.assign"]],
    [{ worker_service_id: "worker-a" }, ["streams.update", "workers.assign"]],
    [{ worker_service_id: "" }, ["streams.update", "workers.assign"]],
    [{ encoder_service_id: "encoder-a", worker_service_id: "worker-a" }, ["streams.update", "services.assign", "workers.assign"]],
  ];
  for (const [payload, permissions] of cases) {
    assert.deepEqual(buildStreamActionPermissionRequirement({ id: "STR-02", stream: stream(), payload }), {
      kind: "all",
      permissions,
    });
  }
});

test("critical actions use the public stream label and never a private identifier as typed token", () => {
  for (const id of ["STR-06", "STR-07", "STR-10"] as const) {
    const descriptor = buildStreamActionDescriptor({ id, stream: stream("failed"), staticRelayRecoveryAvailable: true });
    assert.equal(descriptor?.confirmation.mode, "typed-target");
    assert.equal(descriptor?.target.publicLabel, "Public Stream A");
    assert.notEqual(descriptor?.target.publicLabel, descriptor?.target.resourceId);
  }
});

test("wildcard is authoritative while refreshing permissions and stale state fail closed", async () => {
  let permissions: readonly string[] | undefined = ["*"];
  let state: StreamActionStateSnapshot = { kind: "ready", freshness: "fresh", fingerprint: "authority-a" };
  const harness = streamHarness({
    permissions: () => permissions ? { kind: "ready", permissions } : { kind: "refreshing" },
    state: () => state,
  });
  const intent = { id: "STR-04" as const, stream: stream() };
  assert.equal(harness.controller.evaluate(intent).availability.kind, "allowed");
  permissions = undefined;
  assert.equal(harness.controller.evaluate(intent).availability.kind, "unknown");
  permissions = ["*"];
  state = { kind: "ready", freshness: "stale", fingerprint: "authority-a" };
  assert.equal(harness.controller.evaluate(intent).availability.kind, "unknown");
  assert.equal(harness.calls(), 0);
});

test("lifecycle applicability hides impossible actions with mutation zero", async () => {
  const harness = streamHarness();
  const intent = { id: "STR-04" as const, stream: stream("live") };
  assert.equal(harness.controller.evaluate(intent).visibility.kind, "hidden");
  assert.deepEqual(await harness.controller.open(intent), { kind: "blocked", reason: "not-applicable" });
  assert.equal(harness.calls(), 0);
});

test("pre-submit permission or fingerprint changes block before HTTP", async () => {
  let permissions: readonly string[] = ["streams.start"];
  let fingerprint = "authority-a";
  const harness = streamHarness({
    permissions: () => ({ kind: "ready", permissions }),
    state: () => ({ kind: "ready", freshness: "fresh", fingerprint }),
  });
  const intent = { id: "STR-04" as const, stream: stream() };
  const opened = await harness.controller.open(intent);
  assert.equal(opened.kind, "allowed");
  if (opened.kind !== "allowed") return;
  permissions = [];
  assert.deepEqual(await harness.controller.submit(opened, { confirmed: true }), { kind: "blocked", reason: "permission-denied" });
  assert.equal(harness.calls(), 0);
  permissions = ["streams.start"];
  const reopened = await harness.controller.open(intent);
  assert.equal(reopened.kind, "allowed");
  if (reopened.kind !== "allowed") return;
  fingerprint = "authority-b";
  assert.deepEqual(await harness.controller.submit(reopened, { confirmed: true }), { kind: "blocked", reason: "authority-changed" });
  assert.equal(harness.calls(), 0);
});

test("403, 409, and ambiguous transport each send once and latch until reconciliation", async () => {
  for (const failure of ["forbidden", "conflict", "ambiguous"] as const) {
    const harness = streamHarness({ failure });
    const intent = { id: "STR-04" as const, stream: stream() };
    const opened = await harness.controller.open(intent);
    assert.equal(opened.kind, "allowed");
    if (opened.kind !== "allowed") continue;
    const first = await harness.controller.submit(opened, { confirmed: true });
    assert.equal(first.kind, failure === "ambiguous" ? "outcome_unknown" : "failed");
    assert.deepEqual(await harness.controller.submit(opened, { confirmed: true }), { kind: "blocked", reason: "reconciliation-required" });
    assert.equal(harness.calls(), 1);
  }
});

test("same resource/action concurrent submit is locked before a second request", async () => {
  let release!: () => void;
  const pending = new Promise<void>((resolve) => { release = resolve; });
  const harness = streamHarness({ mutation: async () => pending });
  const intent = { id: "STR-04" as const, stream: stream() };
  const opened = await harness.controller.open(intent);
  assert.equal(opened.kind, "allowed");
  if (opened.kind !== "allowed") return;
  const first = harness.controller.submit(opened, { confirmed: true });
  await new Promise((resolve) => setImmediate(resolve));
  assert.deepEqual(await harness.controller.submit(opened, { confirmed: true }), { kind: "blocked", reason: "duplicate" });
  assert.equal(harness.calls(), 1);
  release();
  assert.equal((await first).kind, "succeeded");
});

test("preview capability is component-owned, non-persistent, and uses the shared controller", () => {
  const source = readFileSync(new URL("../src/features/streams/stream-preview.tsx", import.meta.url), "utf8");
  assert.doesNotMatch(source, /previewLinkCache|localStorage|sessionStorage|queryClient\.setQueryData/);
  assert.doesNotMatch(source, /setTimeout\(requestLink|attempts\s*</);
  assert.match(source, /controller\.open/);
  assert.match(source, /controller\.submit/);
});

function streamHarness(options: {
  permissions?: () => { kind: "ready"; permissions: readonly string[] } | { kind: "refreshing" } | { kind: "unavailable" };
  state?: (intent: StreamActionIntent) => StreamActionStateSnapshot;
  failure?: "forbidden" | "conflict" | "ambiguous";
  mutation?: () => Promise<unknown>;
} = {}) {
  let mutations = 0;
  const controller = createStreamActionController({
    getPermissions: options.permissions ?? (() => ({ kind: "ready", permissions: [
      "streams.create", "streams.update", "streams.start", "streams.stop", "streams.delete", "streams.read",
      "services.assign", "workers.assign",
    ] })),
    getState: options.state ?? (() => ({ kind: "ready", freshness: "fresh", fingerprint: "authority-a" })),
    mutate: async () => {
      mutations += 1;
      if (options.failure === "forbidden") throw Object.assign(new Error("RAW"), { name: "APIError", status: 403, code: "permission_denied" });
      if (options.failure === "conflict") throw Object.assign(new Error("RAW"), { name: "APIError", status: 409, code: "stream_status_not_startable" });
      if (options.failure === "ambiguous") throw new TypeError("RAW transport detail");
      return options.mutation ? options.mutation() : { status: "ok" };
    },
  });
  return { controller, calls: () => mutations };
}
