import assert from "node:assert/strict";
import { register } from "node:module";
import test from "node:test";
import type { StreamVisualSettings } from "../src/features/streams/control-platform.ts";
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
const [{ APIError }, { createStreamVisualActionController, visualSettingsFingerprint }, { defaultStreamVisualDraft, buildStreamVisualFields }] = await Promise.all([
  import("../src/lib/api/client.ts"),
  import("../src/features/streams/stream-visual-action-controller.ts"),
  import("../src/features/streams/stream-visual-draft.ts"),
]);

const saved = (revision = 4): StreamVisualSettings => ({
  stream_id: "stream-one", revision,
  background_mode: "default", header_title_mode: "default",
  discord_target_mode: "inherit", discord_snapshot_revision: 0,
  cover_source: "none", cover_start_active: false,
});
const state = (value = saved()) => ({
  kind: "ready" as const, freshness: "fresh" as const,
  revision: value.revision, fingerprint: visualSettingsFingerprint(value), streamID: value.stream_id,
});
const permission = () => ({ kind: "ready" as const, permissions: ["streams.update"] });

test("POLL-VISUAL-001: a fresh local proof survives a concurrent observer refresh", async () => {
  let refreshing = false, reads = 0, writes = 0;
  const controller = createStreamVisualActionController({
    getPermission: permission,
    getState: () => refreshing ? { kind: "unknown", freshness: "refreshing" } : state(),
    revalidate: async () => { reads++; refreshing = true; return { permission: permission(), state: state() }; },
    mutate: async (request, signal) => {
      writes++; assert.equal(signal?.aborted, false); assert.equal(request.expected_revision, 4);
      return { ...saved(5), header_title_mode: "custom", header_title_value: "Draft stays" };
    },
  });
  assert.equal((await controller.issue({ header_title_mode: "custom", header_title_value: "Draft stays" }, 4)).kind, "succeeded");
  assert.equal(reads, 1); assert.equal(writes, 1); assert.equal(controller.unresolved, false);
});

test("POLL-VISUAL-002: save-time revocation and actual revision changes block dispatch", async () => {
  let writes = 0;
  for (const proof of [
    { permission: { kind: "ready" as const, permissions: [] }, state: state() },
    { permission: permission(), state: state(saved(5)) },
  ]) {
    const controller = createStreamVisualActionController({
      getPermission: permission, getState: state,
      revalidate: async () => proof,
      mutate: async () => { writes++; return saved(5); },
    });
    assert.deepEqual(await controller.issue({ header_title_mode: "default" }, 4), {
      kind: "blocked", reason: proof.permission.permissions.length ? "authority-changed" : "permission-denied",
    });
    assert.equal(controller.unresolved, false);
  }
  assert.equal(writes, 0);
});

test("POLL-VISUAL-003: cancelling a held authority read sends no PUT and creates no unknown outcome", async () => {
  let writes = 0;
  let started!: () => void;
  const entered = new Promise<void>((resolve) => { started = resolve; });
  const controller = createStreamVisualActionController({
    getPermission: permission, getState: state,
    revalidate: (signal) => new Promise<never>((_resolve, reject) => {
      started(); signal.addEventListener("abort", () => reject(new DOMException("Cancelled", "AbortError")), { once: true });
    }),
    mutate: async () => { writes++; return saved(5); },
  });
  const result = controller.issue({ header_title_mode: "default" }, 4);
  await entered; controller.cancel();
  assert.deepEqual(await result, { kind: "blocked", reason: "state-unavailable" });
  assert.equal(writes, 0); assert.equal(controller.pending, false); assert.equal(controller.unresolved, false);
});

test("POLL-VISUAL-004: only explicit rolled-back validation and conflict responses permit deliberate retry", async () => {
  for (const [status, code] of [[400, "invalid_visual_settings"], [409, "revision_conflict"], [409, "cover_upload_limit"], [409, "asset_claim_failed"]] as const) {
    let writes = 0;
    const controller = createStreamVisualActionController({
      getPermission: permission, getState: state,
      revalidate: async () => ({ permission: permission(), state: state() }),
      mutate: async () => { if (++writes === 1) throw new APIError("Rejected", status, code); return saved(5); },
    });
    assert.equal((await controller.issue({ header_title_mode: "default" }, 4)).kind, "failed", code);
    assert.equal(controller.unresolved, false, code); assert.equal(writes, 1, "there is no automatic retry");
    assert.equal((await controller.issue({ header_title_mode: "default" }, 4)).kind, "succeeded", code);
    assert.equal(writes, 2);
  }
});

test("POLL-VISUAL-005: unknown HTTP codes, server failures, and response loss remain latched after a read", async () => {
  for (const error of [new APIError("Unknown", 400, "unknown_validation"), new APIError("Unknown", 409, "unknown_conflict"), new APIError("Failed", 500, "invalid_visual_settings"), new TypeError("Failed to fetch")]) {
    let writes = 0;
    const controller = createStreamVisualActionController({
      getPermission: permission, getState: state,
      mutate: async () => { writes++; throw error; },
    });
    await controller.issue({ header_title_mode: "custom", header_title_value: "Uncertain" }, 4);
    assert.equal(controller.unresolved, true);
    assert.equal(controller.reconcile(), false);
    assert.equal(controller.reconcile(saved(4)), false);
    assert.equal(controller.reconcile(saved(5)), false);
    assert.deepEqual(await controller.issue({ header_title_mode: "custom", header_title_value: "Uncertain" }, 4), { kind: "blocked", reason: "reconciliation-required" });
    assert.equal(writes, 1);
  }
});

test("POLL-VISUAL-006: an uncertain write needs the same stream and a valid newer desired-state observation", async () => {
  let writes = 0;
  const fields = { header_title_mode: "custom", header_title_value: "Requested", discord_target: { mode: "preset", preset_id: "preset-one", preset_revision: 7 }, cover_candidates: [{ name: " Long cover ", source: "upload", asset_id: "asset-one", variant_id: "variant-one" }] };
  const observed: StreamVisualSettings = { ...saved(5), header_title_mode: "custom", header_title_value: "Requested", discord_target_mode: "preset", discord_target_preset_id: "preset-one", discord_target_preset_revision: 7, cover_candidates: [{ name: "Long cover", source: "upload", asset_id: "asset-one", variant_id: "variant-one" }] };
  const controller = createStreamVisualActionController({
    getPermission: permission, getState: state,
    mutate: async () => { writes++; throw new TypeError("Response lost after commit"); },
  });
  await controller.issue(fields, 4);
  for (const invalid of [
    { ...observed, stream_id: "another-stream" }, { ...observed, revision: 4 },
    { ...observed, revision: Number.POSITIVE_INFINITY }, { ...observed, revision: 5.5 },
    { ...observed, header_title_value: "Other writer" }, { ...observed, discord_target_preset_revision: 8 },
    { ...observed, cover_candidates: [] },
  ]) assert.equal(controller.reconcile(invalid), false);
  assert.equal(controller.unresolved, true);
  assert.equal(controller.reconcile(observed), true);
  assert.equal(controller.unresolved, false);
  assert.equal(writes, 1, "reconciliation only observes state and never sends another PUT");
});

test("POLL-VISUAL-007: normal builder nullable resets reconcile only when every cleared field is observed", async () => {
  const fields = buildStreamVisualFields(defaultStreamVisualDraft());
  const clearedKeys = ["background_asset_id", "background_variant_id", "header_title_value", "cover_preset_id", "cover_asset_id", "cover_variant_id"];
  for (const key of clearedKeys) assert.equal(fields[key], null, "exercise the actual reset payload");
  for (const representation of ["omitted", "empty"] as const) {
    let writes = 0;
    const controller = createStreamVisualActionController({
      getPermission: permission, getState: state,
      mutate: async () => { writes++; throw new TypeError("Response lost after reset"); },
    });
    await controller.issue(fields, 4);
    const observed = { ...saved(5), cover_candidates: [], ...(representation === "empty" ? Object.fromEntries(clearedKeys.map((key) => [key, ""])) : {}) };
    for (const key of clearedKeys) assert.equal(controller.reconcile({ ...observed, [key]: "not cleared" }), false, key);
    assert.equal(controller.reconcile({ ...observed, cover_start_active: true }), false, "boolean state stays strict");
    assert.equal(controller.reconcile({ ...observed, background_mode: "image" }), false, "mode stays strict");
    assert.equal(controller.reconcile(observed), true, representation);
    assert.equal(controller.unresolved, false);
    assert.equal(writes, 1);
  }
});
