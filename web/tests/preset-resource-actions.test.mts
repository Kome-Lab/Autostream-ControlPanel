import assert from "node:assert/strict";
import { register } from "node:module";
import test, { type TestContext } from "node:test";
import { QueryClient } from "@tanstack/react-query";

import { registerSourceResolution } from "./helpers/moved-source.mts";
import type { ResourceActionIntent } from "../src/features/resources/resource-action-descriptors.ts";

registerSourceResolution();
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
  buildResourceActionDescriptor,
  buildResourceActionPermissionRequirement,
  resourceActionDescriptors,
  resourceActionID,
  resourceActionRequest,
} = await import("../src/features/resources/resource-action-descriptors.ts");
const { createResourceActionController } = await import("../src/features/resources/resource-action-controller.ts");
const {
  mutateResourceAction,
  refreshResourceAction,
  resourceActionStateSnapshot,
  resourcePermissionSnapshot,
} = await import("../src/features/resources/resource-action-runtime.ts");

const presets = [
  {
    path: "/discord/target-presets", permission: "discord_target_presets",
    create: "RES-41", update: "RES-42", delete: "RES-43",
    payload: { name: "Main stage", guild_id: "123", text_channel_id: "456", voice_channel_id: "789" },
  },
  {
    path: "/video-cover-presets", permission: "video_cover_presets",
    create: "RES-44", update: "RES-45", delete: "RES-46",
    payload: { name: "Standby cover", asset_id: "asset-1", asset_variant_id: "variant-1", enabled: true },
  },
] as const;
type Preset = typeof presets[number];

test("six preset actions extend the existing forty actions with exact API and permission contracts", () => {
  const legacy = resourceActionDescriptors.filter(({ wave }) => wave !== "v2");
  assert.deepEqual(legacy.map(({ id }) => id), Array.from({ length: 40 }, (_, index) => `RES-${String(index + 1).padStart(2, "0")}`));
  assert.equal(legacy.filter(({ wave }) => wave === "3A").length, 32);
  assert.equal(legacy.filter(({ wave }) => wave === "3B").length, 8);
  assert.equal(new Set(resourceActionDescriptors.map(({ id }) => id)).size, 46);
  for (const preset of presets) {
    for (const operation of ["create", "update", "delete"] as const) {
      assert.equal(resourceActionID(preset.path, operation), preset[operation]);
      const intent = intentFor(preset, operation);
      const request = resourceActionRequest(intent);
      assert.ok(request);
      assert.equal(request.path, operation === "create" ? preset.path : `${preset.path}/preset%2Fone`);
      assert.equal(request.method, { create: "POST", update: "PUT", delete: "DELETE" }[operation]);
      assert.deepEqual(buildResourceActionPermissionRequirement(intent), { kind: "all", permissions: [`${preset.permission}.${operation}`] });
      const descriptor = buildResourceActionDescriptor(intent);
      assert.ok(descriptor);
      assert.equal(descriptor.risk, operation === "create" ? "guarded" : "high");
      assert.equal(descriptor.audit.action, `${preset.permission}.${operation}`);
      assert.equal(descriptor.confirmation.mode, "consequence");
      assert.equal(descriptor.confirmation.requireSubmitRevalidation, true);
      assert.deepEqual(descriptor.retry, { kind: "never" });
      assert.equal(descriptor.duplicate.scope, operation === "create" ? "resource-action" : "resource-target");
      assert.deepEqual(request.body, operation === "delete" ? { expected_revision: 7 } : intent.payload);
    }
  }
});

test("preset writes require positive safe revisions and never replace a stale edit revision", () => {
  for (const preset of presets) {
    for (const revision of [undefined, null, 0, -1, 1.5, NaN, Infinity, "7", Number.MAX_SAFE_INTEGER + 1]) {
      for (const operation of ["update", "delete"] as const) {
        const intent = intentFor(preset, operation);
        const invalid = { ...intent, row: { ...intent.row, revision } };
        assert.equal(resourceActionRequest(invalid), undefined, `${preset.path} ${operation}: invalid row revision ${revision}`);
        assert.equal(buildResourceActionDescriptor(invalid), undefined);
      }
      const intent = intentFor(preset, "update");
      assert.equal(resourceActionRequest({ ...intent, payload: { ...intent.payload, expected_revision: revision } }), undefined);
    }
    const update = intentFor(preset, "update");
    assert.equal(resourceActionRequest({ ...update, row: { ...update.row, revision: 8 } }), undefined);
    assert.equal(update.payload?.expected_revision, 7);
    const deletion = intentFor(preset, "delete");
    assert.deepEqual(resourceActionRequest({ ...deletion, payload: { expected_revision: 999, name: "ignored" } })?.body, { expected_revision: 7 });
  }
});

test("unrelated resource deletions retain their bodyless request contract", () => {
  for (const template of resourceActionDescriptors.filter(({ method, wave }) => method === "DELETE" && wave !== "v2")) {
    const request = resourceActionRequest({ id: template.id, row: { id: "existing", name: "Existing resource", revision: 7 }, payload: { expected_revision: 7 } });
    assert.ok(request);
    assert.equal(Object.hasOwn(request, "body"), false, template.id);
  }
});

for (const preset of presets) {
  test(`${preset.path}: fresh-confirmed deletion sends the current revision in JSON`, async (t) => {
    const harness = runtimeHarness(t, preset);
    harness.setRevision(8);
    const opened = await harness.controller.open(intentFor(preset, "delete"));
    assert.equal(opened.kind, "allowed");
    if (opened.kind !== "allowed") return;
    assert.equal(opened.intent.row?.revision, 8);
    assert.deepEqual(await harness.controller.submit(opened, { confirmed: false }), { kind: "blocked", reason: "confirmation-required" });
    assert.equal(harness.writes.length, 0);
    assert.equal((await harness.controller.submit(opened, { confirmed: true })).kind, "succeeded");
    assert.deepEqual(harness.writes, [{ path: `${preset.path}/preset%2Fone`, method: "DELETE", body: { expected_revision: 8 } }]);
    assert.equal(harness.reads.filter((path) => path === "/auth/me").length, 2);
    assert.equal(harness.reads.filter((path) => path === preset.path).length, 2);
  });

  test(`${preset.path}: a revision change after confirmation blocks deletion`, async (t) => {
    const harness = runtimeHarness(t, preset);
    const opened = await harness.controller.open(intentFor(preset, "delete"));
    assert.equal(opened.kind, "allowed");
    if (opened.kind !== "allowed") return;
    harness.setRevision(8);
    assert.deepEqual(await harness.controller.submit(opened, { confirmed: true }), { kind: "blocked", reason: "authority-changed" });
    assert.equal(harness.writes.length, 0);
  });

  test(`${preset.path}: refresh rejects a stale draft and leaves its expected revision intact`, async (t) => {
    const harness = runtimeHarness(t, preset);
    const intent = intentFor(preset, "update");
    harness.setRevision(8);
    const opened = await harness.controller.open(intent);
    assert.equal(opened.kind, "blocked");
    assert.equal(intent.payload?.expected_revision, 7);
    assert.equal(harness.writes.length, 0);
  });

  test(`${preset.path}: unchanged drafts submit their original revision; revoked permission blocks`, async (t) => {
    const harness = runtimeHarness(t, preset);
    const intent = intentFor(preset, "update");
    const opened = await harness.controller.open(intent);
    assert.equal(opened.kind, "allowed");
    if (opened.kind !== "allowed") return;
    harness.revokePermission();
    assert.deepEqual(await harness.controller.submit(opened, { confirmed: true }), { kind: "blocked", reason: "permission-denied" });
    assert.equal(harness.writes.length, 0);
    harness.restorePermission();
    assert.equal((await harness.controller.submit(opened, { confirmed: true })).kind, "succeeded");
    assert.deepEqual(harness.writes, [{ path: `${preset.path}/preset%2Fone`, method: "PUT", body: intent.payload }]);
  });
}

function intentFor(preset: Preset, operation: "create" | "update" | "delete"): ResourceActionIntent {
  return Object.freeze({
    id: preset[operation],
    ...(operation === "create" ? {} : { row: Object.freeze({ id: "preset/one", name: preset.payload.name, revision: 7 }) }),
    ...(operation === "delete" ? {} : { payload: Object.freeze({ ...preset.payload, ...(operation === "update" ? { expected_revision: 7 } : {}) }) }),
    publicLabel: preset.payload.name,
  });
}

function runtimeHarness(t: TestContext, preset: Preset) {
  const queryClient = new QueryClient();
  const row = { id: "preset/one", ...preset.payload, revision: 7 };
  let permissions = [`${preset.permission}.update`, `${preset.permission}.delete`];
  const reads: string[] = [];
  const writes: Array<{ path: string; method: string; body: unknown }> = [];
  const demo = process.env.NEXT_PUBLIC_AUTOSTREAM_DEMO;
  process.env.NEXT_PUBLIC_AUTOSTREAM_DEMO = "false";
  t.after(() => {
    queryClient.clear();
    if (demo === undefined) delete process.env.NEXT_PUBLIC_AUTOSTREAM_DEMO;
    else process.env.NEXT_PUBLIC_AUTOSTREAM_DEMO = demo;
  });
  t.mock.method(globalThis, "fetch", async (input: string | URL | Request, init?: RequestInit) => {
    const path = String(input);
    if (init?.method === "GET") {
      reads.push(path);
      if (path === "/auth/me") return Response.json({ permissions });
      if (path === preset.path) return Response.json({ items: [row] });
    }
    if (path === `${preset.path}/preset%2Fone` && (init?.method === "PUT" || init?.method === "DELETE")) {
      assert.equal(new Headers(init.headers).get("Content-Type"), "application/json");
      writes.push({ path, method: init.method, body: JSON.parse(String(init.body)) });
      return Response.json({ ...row, revision: row.revision + 1 });
    }
    throw new Error(`Unexpected preset test request: ${init?.method} ${path}`);
  });
  const controller = createResourceActionController({
    getPermissions: () => resourcePermissionSnapshot(queryClient),
    getState: (intent) => resourceActionStateSnapshot(queryClient, intent),
    refresh: (intent) => refreshResourceAction(queryClient, intent),
    mutate: mutateResourceAction,
  });
  return {
    controller, reads, writes,
    setRevision: (revision: number) => { row.revision = revision; },
    revokePermission: () => { permissions = []; },
    restorePermission: () => { permissions = [`${preset.permission}.update`, `${preset.permission}.delete`]; },
  };
}
