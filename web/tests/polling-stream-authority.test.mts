import assert from "node:assert/strict";
import { register } from "node:module";
import test from "node:test";
import { QueryClient } from "@tanstack/react-query";
import type { StreamActionIntent } from "../src/features/streams/stream-action-descriptors.ts";
import type { Stream } from "../src/types/domain.ts";
import { registerSourceResolution } from "./helpers/moved-source.mts";

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
registerSourceResolution();
const [{ APIError }, { createStreamActionController }, { streamActionStateSnapshot, streamPermissionSnapshot, cacheStreamActionResponse, revalidateStreamActionAuthority }, { streamActionBlockedMessage }] = await Promise.all([
  import("../src/lib/api/client.ts"),
  import("../src/features/streams/stream-action-controller.ts"),
  import("../src/features/streams/stream-action-runtime.ts"),
  import("../src/features/streams/stream-action-feedback.ts"),
]);

const createIntent = (): StreamActionIntent => ({
  id: "STR-01", publicLabel: "A new stream",
  payload: { name: "A new stream", auto_start_trigger: "", encoder_service_id: "encoder-one", worker_service_id: "worker-one" },
});

test("POLL-STREAM-010: real configuration, ownership, lifecycle, and deletion conflicts expose only safe field labels", async () => {
  const baseline: Stream = { id: "stream-one", name: "Original", status: "ready", settings_revision: "2026-10-28T00:00:00Z", assigned_encoder_id: "encoder-one" };
  const intent: StreamActionIntent = { id: "STR-02", stream: baseline, payload: { name: "Unsaved draft", expected_settings_revision: baseline.settings_revision } };
  const scenarios = [
    { row: { ...baseline, name: "Never display this server value", settings_revision: "2026-10-28T00:00:00.000001Z" }, kind: "settings", fields: ["name"] },
    { row: { ...baseline, assigned_encoder_id: "private-node-identifier" }, kind: "ownership", fields: ["nodes"] },
    { row: { ...baseline, status: "live" }, kind: "lifecycle", fields: [] },
    { row: undefined, kind: "missing", fields: [] },
  ] as const;
  for (const scenario of scenarios) {
    const client = new QueryClient();
    try {
      client.setQueryData(["streams"], scenario.row ? [scenario.row] : []);
      let writes = 0;
      const controller = createStreamActionController({
        getPermissions: () => ({ kind: "ready", permissions: ["*"] }),
        getState: (action) => streamActionStateSnapshot(client, action),
        mutate: async () => { writes++; return baseline; },
      });
      const result = await controller.open(intent);
      assert.equal(result.kind, "blocked");
      if (result.kind !== "blocked") throw Error("changed baseline must be rejected");
      assert.equal(result.authorityChange, scenario.kind);
      assert.deepEqual(result.changedFields || [], scenario.fields);
      const message = streamActionBlockedMessage(result.reason, undefined, result);
      assert.doesNotMatch(message, /Never display|private-node-identifier|2026-10-28/);
      assert.match(message, /入力内容は保持/);
      assert.equal(writes, 0);
      assert.equal(intent.payload?.name, "Unsaved draft");
    } finally { client.clear(); }
  }
});

test("POLL-STREAM-011: a same-content lifecycle timestamp does not invalidate a settings baseline", () => {
  const client = new QueryClient();
  try {
    const baseline: Stream = { id: "stream-one", name: "Original", status: "completed", settings_revision: "2026-10-28T00:00:00Z", updated_at: "2026-10-28T00:01:00Z" };
    const intent: StreamActionIntent = { id: "STR-02", stream: baseline, payload: { name: "Draft" } };
    client.setQueryData(["streams"], [baseline]);
    const first = streamActionStateSnapshot(client, intent);
    client.setQueryData(["streams"], [{ ...baseline, updated_at: "2026-10-28T00:05:00Z", archive_reported_at: "2026-10-28T00:05:00Z" }]);
    assert.deepEqual(streamActionStateSnapshot(client, intent), first);
    assert.equal(first.baselineChanged, false);
  } finally { client.clear(); }
});

test("POLL-STREAM-001: a background auth read retains the last successful permission snapshot", () => {
  const client = new QueryClient();
  try {
    client.setQueryData(["auth", "me"], { permissions: ["streams.create"] });
    const query = client.getQueryCache().find({ queryKey: ["auth", "me"], exact: true });
    assert.ok(query);
    query.setState({ fetchStatus: "fetching" });
    assert.equal(query.state.status, "success");
    assert.deepEqual(streamPermissionSnapshot(client), { kind: "ready", permissions: ["streams.create"] });
  } finally { client.clear(); }
});

test("POLL-STREAM-002: unrelated stream transitions and list order do not change new-stream authority", () => {
  const client = new QueryClient();
  try {
    const intent = createIntent();
    client.setQueryData(["streams"], [
      { id: "other-a", name: "A", status: "ready", updated_at: "2026-10-28T00:00:00Z" },
      { id: "other-b", name: "B", status: "live", updated_at: "2026-10-28T00:00:01Z" },
    ]);
    client.setQueryData(["service-health"], [
      { id: "encoder-one", service_id: "encoder-one", service_type: "encoder_recorder", current_stream_id: "" },
      { id: "worker-one", service_id: "worker-one", service_type: "worker", current_stream_id: "" },
    ]);
    const before = streamActionStateSnapshot(client, intent);
    assert.equal(before.kind, "ready");
    client.setQueryData(["streams"], [
      { id: "other-b", name: "B", status: "completed", updated_at: "2026-10-28T00:03:00Z" },
      { id: "other-a", name: "A", status: "ready", updated_at: "2026-10-28T00:00:00Z" },
    ]);
    assert.deepEqual(streamActionStateSnapshot(client, intent), before);
  } finally { client.clear(); }
});

test("POLL-STREAM-003: one submit intent revalidates permission before sending and blocks a real revocation", async () => {
  let reads = 0, mutations = 0;
  const dependencies = {
    getPermissions: () => ({ kind: "ready" as const, permissions: ["*"] }),
    getState: () => ({ kind: "ready" as const, freshness: "fresh" as const, fingerprint: "selected-prerequisites" }),
    revalidate: async () => {
      reads++;
      return { permissions: { kind: "ready" as const, permissions: [] }, state: { kind: "ready" as const, freshness: "fresh" as const, fingerprint: "selected-prerequisites" } };
    },
    mutate: async () => { mutations++; return { id: "unexpected" }; },
  };
  const controller = createStreamActionController(dependencies);
  const opened = await controller.open(createIntent());
  assert.equal(opened.kind, "allowed");
  if (opened.kind !== "allowed") throw Error("test requires an initially permitted draft");
  assert.deepEqual(await controller.submit(opened, { confirmed: true }), { kind: "blocked", reason: "permission-denied" });
  assert.equal(reads, 1);
  assert.equal(mutations, 0);
});

test("POLL-STREAM-004: a proven pre-create schedule rejection permits a corrected deliberate attempt", async () => {
  let mutations = 0;
  const snapshot = () => ({ permissions: { kind: "ready" as const, permissions: ["*"] }, state: { kind: "ready" as const, freshness: "fresh" as const, fingerprint: "selected-prerequisites" } });
  const dependencies = {
    getPermissions: () => snapshot().permissions, getState: () => snapshot().state,
    revalidate: async () => snapshot(),
    mutate: async () => {
      mutations++;
      if (mutations === 1) throw new APIError("redacted", 400, "schedule_time_invalid");
      return { id: "created-once" };
    },
  };
  const controller = createStreamActionController(dependencies);
  const opened = await controller.open(createIntent());
  assert.equal(opened.kind, "allowed");
  if (opened.kind !== "allowed") throw Error("initially permitted draft");
  const rejected = await controller.submit(opened, { confirmed: true });
  assert.equal(rejected.kind, "failed");
  if (rejected.kind === "failed") assert.equal(rejected.retryAfterCorrection, true, "only a proven non-applied rejection gets correction guidance");
  assert.equal(mutations, 1, "the rejected operation is never automatically retried");
  const corrected = await controller.open({ ...createIntent(), payload: { ...createIntent().payload, scheduled_start_at: "2026-10-29T00:00:00Z" } });
  assert.equal(corrected.kind, "allowed", "known pre-create rejection must not become an unresolved receipt");
  if (corrected.kind !== "allowed") throw Error("corrected deliberate attempt blocked");
  assert.equal((await controller.submit(corrected, { confirmed: true })).kind, "succeeded");
  assert.equal(mutations, 2);
});

test("POLL-STREAM-005: response loss retains the unresolved latch and never automatically creates again", async () => {
  let mutations = 0;
  const snapshot = () => ({ permissions: { kind: "ready" as const, permissions: ["*"] }, state: { kind: "ready" as const, freshness: "fresh" as const, fingerprint: "selected-prerequisites" } });
  const controller = createStreamActionController({
    getPermissions: () => snapshot().permissions, getState: () => snapshot().state,
    mutate: async () => { mutations++; throw new TypeError("controlled response loss"); },
  });
  const opened = await controller.open(createIntent());
  assert.equal(opened.kind, "allowed");
  if (opened.kind !== "allowed") throw Error("initially permitted draft");
  assert.equal((await controller.submit(opened, { confirmed: true })).kind, "outcome_unknown");
  controller.reconcile(createIntent());
  assert.equal((await controller.open(createIntent())).kind, "blocked");
  assert.equal(mutations, 1);
});

test("POLL-STREAM-006: a new poll during revalidation cannot invalidate the local proof or duplicate its one save", async () => {
  let fetching = false, mutations = 0, reads = 0;
  const permissions = { kind: "ready" as const, permissions: ["*"] };
  const dependencies = {
    getPermissions: () => permissions,
    getState: () => ({ kind: "ready" as const, freshness: fetching ? "refreshing" as const : "fresh" as const, fingerprint: "selected-prerequisites" }),
    revalidate: async () => {
      reads++;
      fetching = true; // A separate observer starts a new background GET.
      return { permissions, state: { kind: "ready" as const, freshness: "fresh" as const, fingerprint: "selected-prerequisites" } };
    },
    mutate: async () => { mutations++; return { id: "one" }; },
  };
  const controller = createStreamActionController(dependencies);
  const opened = await controller.open(createIntent());
  assert.equal(opened.kind, "allowed");
  if (opened.kind !== "allowed") throw Error("initially permitted draft");
  assert.equal((await controller.submit(opened, { confirmed: true })).kind, "succeeded");
  assert.equal(reads, 1);
  assert.equal(mutations, 1);
});

test("POLL-STREAM-007: cancelling a held preflight aborts the read without dispatching or latching an unknown write", async () => {
  let release!: () => void, started!: () => void, signal: AbortSignal | undefined;
  let mutations = 0;
  const began = new Promise<void>((resolve) => { started = resolve; });
  const permissions = { kind: "ready" as const, permissions: ["*"] };
  const state = { kind: "ready" as const, freshness: "fresh" as const, fingerprint: "selected-prerequisites" };
  const dependencies = {
    getPermissions: () => permissions, getState: () => state,
    revalidate: async (_intent: StreamActionIntent, inputSignal: AbortSignal) => {
      signal = inputSignal; started();
      await new Promise<void>((resolve) => { release = resolve; });
      return { permissions, state };
    },
    mutate: async () => { mutations++; return {}; },
  };
  const controller = createStreamActionController(dependencies);
  const intent = createIntent(), opened = await controller.open(intent);
  assert.equal(opened.kind, "allowed");
  if (opened.kind !== "allowed") throw Error("initially permitted draft");
  const pending = controller.submit(opened, { confirmed: true });
  await began;
  controller.cancel(intent);
  assert.equal(signal?.aborted, true);
  assert.deepEqual(await pending, { kind: "blocked", reason: "cancelled" });
  release();
  assert.equal(mutations, 0);
  assert.equal((await controller.open(intent)).kind, "allowed");
});

test("POLL-STREAM-008: a delayed pre-mutation list GET cannot roll back the confirmed mutation cache", async () => {
  const client = new QueryClient();
  let release!: (rows: { id: string; name: string; status: string }[]) => void;
  let signal: AbortSignal | undefined;
  const old = { id: "old-row", name: "Old list", status: "ready" };
  const created = { id: "created-row", name: "Saved exactly once", status: "created" };
  client.setQueryData(["streams"], [old]);
  const delayed = client.fetchQuery({ queryKey: ["streams"], queryFn: (context) => {
    signal = context.signal;
    return new Promise<{ id: string; name: string; status: string }[]>((resolve) => { release = resolve; });
  } }).catch(() => undefined);
  try {
    assert.ok(release, "the old GET actually began before the mutation response");
    await cacheStreamActionResponse(client, createIntent(), created);
    assert.equal(signal?.aborted, true);
    release([old]);
    await delayed;
    const rows = client.getQueryData<{ id: string }[]>(["streams"]);
    assert.ok(rows?.some((row) => row.id === created.id));
    assert.equal(rows?.filter((row) => row.id === created.id).length, 1);
  } finally { client.clear(); }
});

test("POLL-STREAM-009: an older direct authority GET cannot publish over a confirmed same-stream mutation", async () => {
  const client = new QueryClient();
  const originalFetch = globalThis.fetch;
  const old = { id: "stream-one", name: "Old settings", status: "created", settings_revision: "2026-10-28T00:00:00Z" };
  const saved = { ...old, name: "New settings", settings_revision: "2026-10-28T00:00:01Z" };
  const intent: StreamActionIntent = { id: "STR-02", stream: old, payload: { name: saved.name, expected_settings_revision: old.settings_revision } };
  let release!: () => void, started!: () => void;
  const began = new Promise<void>((resolve) => { started = resolve; });
  globalThis.fetch = async (input) => {
    const path = input instanceof Request ? input.url : String(input);
    if (path.endsWith("/auth/me")) return Response.json({ permissions: ["*"] });
    assert.ok(path.endsWith("/streams/stream-one"), "authority reads only its exact stream and permissions");
    started();
    await new Promise<void>((resolve) => { release = resolve; });
    return Response.json(old);
  };
  client.setQueryData(["streams"], [old]);
  try {
    const reading = revalidateStreamActionAuthority(client, intent, new AbortController().signal);
    await began;
    await cacheStreamActionResponse(client, intent, saved);
    release();
    const authority = await reading;
    assert.equal(authority.state.kind, "unknown", "a real same-target mutation supersedes the older local read");
    assert.deepEqual(client.getQueryData(["streams"]), [saved]);
  } finally { globalThis.fetch = originalFetch; client.clear(); }
});
