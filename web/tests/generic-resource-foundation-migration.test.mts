import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { register } from "node:module";
import test from "node:test";

import type { ResourceActionIntent, ResourceActionTemplate } from "../src/features/resources/resource-action-descriptors.ts";
import type { ResourceActionStateSnapshot } from "../src/features/resources/resource-action-controller.ts";

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
  resourceActionRequest,
} = await import("../src/features/resources/resource-action-descriptors.ts");
const { createResourceActionController } = await import("../src/features/resources/resource-action-controller.ts");

type InventoryRow = {
  id: string;
  route: string;
  method: string;
  risk: string;
  confirmation: string;
  duplicateScope: string;
  audit: string;
  migrationWave: string;
};

const inventory = readFileSync(new URL("./fixtures/ui-foundation-action-inventory.jsonl", import.meta.url), "utf8")
  .trim()
  .split(/\r?\n/u)
  .map((line) => JSON.parse(line) as InventoryRow)
  .filter((entry) => entry.id.startsWith("RES-"));

test("Generic Resources declares the canonical RES 40/40 and frozen wave denominators", () => {
  const ids = resourceActionDescriptors.map(({ id }) => id);
  assert.equal(ids.length, 40);
  assert.equal(new Set(ids).size, 40);
  assert.deepEqual(ids, Array.from({ length: 40 }, (_, index) => `RES-${String(index + 1).padStart(2, "0")}`));
  assert.equal(resourceActionDescriptors.filter(({ wave }) => wave === "3A").length, 32);
  assert.equal(resourceActionDescriptors.filter(({ wave }) => wave === "3B").length, 8);
  assert.deepEqual(resourceActionDescriptors.filter(({ wave }) => wave === "3B").map(({ id }) => id), [
    "RES-23", "RES-24", "RES-25", "RES-26", "RES-27", "RES-28", "RES-29", "RES-40",
  ]);
});

test("the typed table is mechanically identical to the canonical inventory authority", () => {
  assert.equal(inventory.length, 40);
  for (const entry of inventory) {
    const descriptor = resourceActionDescriptors.find(({ id }) => id === entry.id);
    assert.ok(descriptor, entry.id);
    assert.equal(descriptor.route, entry.route, `${entry.id} route`);
    assert.equal(descriptor.method, entry.method, `${entry.id} method`);
    assert.equal(descriptor.risk, entry.risk, `${entry.id} risk`);
    assert.equal(descriptor.auditAction, entry.audit, `${entry.id} audit`);
    assert.equal(descriptor.wave, entry.migrationWave, `${entry.id} wave`);
    assert.equal(descriptor.duplicateScope, inventoryDuplicateScope(entry.duplicateScope), `${entry.id} duplicate scope`);
    assert.equal(descriptor.confirmation, inventoryConfirmation(entry.confirmation), `${entry.id} confirmation`);
  }
});

test("all 40 requests preserve their exact endpoint, method, and payload presence", () => {
  for (const template of resourceActionDescriptors) {
    const intent = intentFor(template);
    const request = resourceActionRequest(intent);
    assert.ok(request, template.id);
    assert.equal(request.method, template.method, `${template.id} method`);
    assert.equal(request.path, template.route.replace("{id}", "row%2Fa"), `${template.id} route`);
    if (template.method === "DELETE" || template.operation === "test") {
      assert.equal(request.body, undefined, `${template.id} omitted body`);
    } else {
      assert.deepEqual(request.body, intent.payload, `${template.id} included body`);
    }
  }
});

test("payload-aware OAuth and User permission builders distinguish omitted, empty, and present", () => {
  const cases: Array<[ResourceActionIntent, string[]]> = [
    [{ id: "RES-23", payload: { name: "Provider" }, publicLabel: "Provider" }, ["integrations.create"]],
    [{ id: "RES-23", payload: { name: "Provider", default_role_ids: [] }, publicLabel: "Provider" }, ["integrations.create"]],
    [{ id: "RES-23", payload: { name: "Provider", default_role_ids: ["role-a"] }, publicLabel: "Provider" }, ["integrations.create", "roles.assign"]],
    [{ id: "RES-24", row: { id: "provider-a", name: "Provider" }, payload: { default_role_ids: [] } }, ["integrations.update"]],
    [{ id: "RES-24", row: { id: "provider-a", name: "Provider" }, payload: { default_role_ids: ["role-a"] } }, ["integrations.update", "roles.assign"]],
    [{ id: "RES-30", payload: { username: "operator", role_ids: [] }, publicLabel: "operator" }, ["users.create"]],
    [{ id: "RES-30", payload: { username: "operator", role_ids: ["role-a"] }, publicLabel: "operator" }, ["users.create", "roles.assign"]],
    [{ id: "RES-31", row: { id: "user-a", username: "operator" }, payload: {} }, ["users.update"]],
    [{ id: "RES-31", row: { id: "user-a", username: "operator" }, payload: { role_ids: [] } }, ["users.update", "roles.assign"]],
    [{ id: "RES-31", row: { id: "user-a", username: "operator" }, payload: { role_ids: ["role-a"] } }, ["users.update", "roles.assign"]],
  ];
  for (const [intent, permissions] of cases) {
    assert.deepEqual(buildResourceActionPermissionRequirement(intent), { kind: "all", permissions }, JSON.stringify(intent));
  }
});

test("critical confirmations use only public labels or the two frozen fixed phrases", () => {
  const critical = resourceActionDescriptors.filter(({ risk }) => risk === "critical");
  assert.deepEqual(critical.map(({ id }) => id), ["RES-13", "RES-24", "RES-25", "RES-32", "RES-34", "RES-35", "RES-40"]);
  for (const template of critical) {
    const descriptor = buildResourceActionDescriptor(intentFor(template));
    assert.equal(descriptor?.confirmation.mode, "typed-target", template.id);
    assert.notEqual(descriptor?.target.publicLabel, "row/a", template.id);
    assert.doesNotMatch(descriptor?.target.publicLabel || "", /secret|token|password/i, template.id);
  }
  assert.equal(resourceActionDescriptors.find(({ id }) => id === "RES-13")?.fixedToken, "deepgram_api_key");
  assert.equal(resourceActionDescriptors.find(({ id }) => id === "RES-40")?.fixedToken, "SECURITY POLICY");
});

test("wildcard permits actions while permission refresh and stale rows fail closed", () => {
  let permissions: readonly string[] | undefined = ["*"];
  let state: ResourceActionStateSnapshot = { kind: "ready", freshness: "fresh", fingerprint: "authority-a" };
  const harness = resourceHarness({
    permissions: () => permissions ? { kind: "ready", permissions } : { kind: "refreshing" },
    state: () => state,
  });
  const intent = intentFor(resourceActionDescriptors[0]);
  assert.equal(harness.controller.evaluate(intent).availability.kind, "allowed");
  permissions = undefined;
  assert.equal(harness.controller.evaluate(intent).availability.kind, "unknown");
  permissions = ["*"];
  state = { kind: "ready", freshness: "stale", fingerprint: "authority-a" };
  assert.equal(harness.controller.evaluate(intent).availability.kind, "unknown");
  assert.equal(harness.calls(), 0);
});

test("open and direct submit both rebuild payload-aware permission authority from fresh state", async () => {
  let permissions: readonly string[] = ["integrations.create", "roles.assign"];
  const harness = resourceHarness({ permissions: () => ({ kind: "ready", permissions }) });
  const intent: ResourceActionIntent = { id: "RES-23", payload: { name: "Provider", default_role_ids: ["role-a"] }, publicLabel: "Provider" };
  const opened = await harness.controller.open(intent);
  assert.equal(opened.kind, "allowed");
  assert.equal(harness.refreshes(), 1);
  if (opened.kind !== "allowed") return;
  permissions = ["integrations.create"];
  assert.deepEqual(await harness.controller.submit(opened, { confirmed: true }), { kind: "blocked", reason: "permission-denied" });
  assert.equal(harness.calls(), 0);
  assert.equal(harness.refreshes(), 2);
});

test("pre-submit fingerprint change blocks before the mutation", async () => {
  let fingerprint = "authority-a";
  const harness = resourceHarness({ state: () => ({ kind: "ready", freshness: "fresh", fingerprint }) });
  const intent = intentFor(resourceActionDescriptors[0]);
  const opened = await harness.controller.open(intent);
  assert.equal(opened.kind, "allowed");
  if (opened.kind !== "allowed") return;
  fingerprint = "authority-b";
  assert.deepEqual(await harness.controller.submit(opened, { confirmed: true }), { kind: "blocked", reason: "authority-changed" });
  assert.equal(harness.calls(), 0);
});

test("sensitive cleanup is notified once only after a mutation is dispatched", async () => {
  let dispatches = 0;
  const sent = resourceHarness({ failure: "ambiguous" });
  const intent = intentFor(resourceActionDescriptors[0]);
  const opened = await sent.controller.open(intent);
  assert.equal(opened.kind, "allowed");
  if (opened.kind !== "allowed") return;
  assert.equal((await sent.controller.submit(opened, { confirmed: true }, { onDispatch: () => { dispatches += 1; } })).kind, "outcome_unknown");
  assert.equal(dispatches, 1);

  let fingerprint = "authority-a";
  const blocked = resourceHarness({ state: () => ({ kind: "ready", freshness: "fresh", fingerprint }) });
  const blockedOpen = await blocked.controller.open(intent);
  assert.equal(blockedOpen.kind, "allowed");
  if (blockedOpen.kind !== "allowed") return;
  fingerprint = "authority-b";
  assert.equal((await blocked.controller.submit(blockedOpen, { confirmed: true }, { onDispatch: () => { dispatches += 1; } })).kind, "blocked");
  assert.equal(dispatches, 1);
  assert.equal(blocked.calls(), 0);
});

test("403, protected 409, and ambiguous transport send once and never resend before reconciliation", async () => {
  for (const failure of ["forbidden", "protected", "ambiguous"] as const) {
    const harness = resourceHarness({ failure });
    const intent = intentFor(resourceActionDescriptors[0]);
    const opened = await harness.controller.open(intent);
    assert.equal(opened.kind, "allowed");
    if (opened.kind !== "allowed") continue;
    const first = await harness.controller.submit(opened, { confirmed: true });
    assert.equal(first.kind, failure === "ambiguous" ? "outcome_unknown" : "failed");
    assert.deepEqual(await harness.controller.submit(opened, { confirmed: true }), { kind: "blocked", reason: "reconciliation-required" });
    assert.equal(harness.calls(), 1);
  }
});

test("resource-target duplicate scope blocks concurrent update/delete dispatch", async () => {
  let release!: () => void;
  const pending = new Promise<void>((resolve) => { release = resolve; });
  const harness = resourceHarness({ mutation: async () => pending });
  const update = intentFor(resourceActionDescriptors[1]);
  const remove = intentFor(resourceActionDescriptors[2]);
  const openedUpdate = await harness.controller.open(update);
  const openedDelete = await harness.controller.open(remove);
  assert.equal(openedUpdate.kind, "allowed");
  assert.equal(openedDelete.kind, "allowed");
  if (openedUpdate.kind !== "allowed" || openedDelete.kind !== "allowed") return;
  const first = harness.controller.submit(openedUpdate, { confirmed: true });
  await new Promise((resolve) => setImmediate(resolve));
  assert.deepEqual(await harness.controller.submit(openedDelete, { confirmed: true }), { kind: "blocked", reason: "duplicate" });
  assert.equal(harness.calls(), 1);
  release();
  assert.equal((await first).kind, "succeeded");
});

test("hostile secret or transport text cannot enter safe failure or audit presentation", async () => {
  const marker = "PRIVATE_DEEPGRAM_MARKER_92f";
  const harness = resourceHarness({ failure: "ambiguous", rawFailure: marker });
  const intent: ResourceActionIntent = { id: "RES-13", payload: { value: marker }, publicLabel: "Deepgram API key" };
  const descriptor = buildResourceActionDescriptor(intent);
  assert.ok(descriptor);
  assert.doesNotMatch(JSON.stringify(descriptor), new RegExp(marker));
  const opened = await harness.controller.open(intent);
  assert.equal(opened.kind, "allowed");
  if (opened.kind !== "allowed") return;
  const result = await harness.controller.submit(opened, { confirmed: true, typedValue: "deepgram_api_key" });
  assert.equal(result.kind, "outcome_unknown");
  assert.doesNotMatch(JSON.stringify(result), new RegExp(marker));
});

test("desktop and mobile rows share one guarded action factory and cached rows survive refresh errors", () => {
  const source = readFileSync(new URL("../src/features/resources/resource-page.tsx", import.meta.url), "utf8");
  assert.match(source, /const rowActions = \(row: ResourceRow\) =>/);
  assert.equal((source.match(/\{rowActions\(row\)\}/g) || []).length, 2);
  assert.match(source, /query\.isLoading && rows\.length === 0/);
  assert.match(source, /rows\.length === 0 && query\.isError \? null/);
  assert.match(source, /ResourceActionConfirmationHost/);
  assert.match(source, /ResourceActionControl/);
  assert.match(source, /Omit<Submission, "payload">/);
  assert.match(source, /onSensitiveDispatched/);
  assert.doesNotMatch(source, /onSecretStored/);
  assert.doesNotMatch(source, /roles\?\.includes\("super_admin"\)/);
});

function intentFor(template: ResourceActionTemplate): ResourceActionIntent {
  const row = { id: "row/a", name: "Public Resource", username: "public-operator", revision: 7, updated_at: "2026-09-03T00:00:00Z" };
  const payload = {
    name: "Public Resource",
    username: "public-operator",
    provider_id: "provider-a",
    oauth_account_id: "account-a",
  };
  return Object.freeze({
    id: template.id,
    ...(template.route.includes("{id}") ? { row } : {}),
    ...(template.method === "DELETE" || template.operation === "test" ? {} : { payload }),
    publicLabel: "Public Resource",
  });
}

function inventoryDuplicateScope(value: string) {
  if (value === "RA") return "resource-action";
  if (value === "RT") return "resource-target";
  if (value === "SESS") return "session-flow";
  throw new Error(`unknown duplicate scope: ${value}`);
}

function inventoryConfirmation(value: string) {
  if (value === "C") return "consequence";
  if (value.startsWith("T(")) return value === "T(deepgram_api_key)" || value === "T(SECURITY POLICY)" ? "typed-fixed" : "typed-label";
  throw new Error(`unknown confirmation: ${value}`);
}

function resourceHarness(options: {
  permissions?: () => { kind: "ready"; permissions: readonly string[] } | { kind: "refreshing" } | { kind: "unavailable" };
  state?: (intent: ResourceActionIntent) => ResourceActionStateSnapshot;
  failure?: "forbidden" | "protected" | "ambiguous";
  rawFailure?: string;
  mutation?: () => Promise<unknown>;
} = {}) {
  let mutations = 0;
  let refreshes = 0;
  const permissions = options.permissions ?? (() => ({ kind: "ready" as const, permissions: ["*"] }));
  const state = options.state ?? (() => ({ kind: "ready" as const, freshness: "fresh" as const, fingerprint: "authority-a" }));
  const controller = createResourceActionController({
    getPermissions: permissions,
    getState: state,
    refresh: async (intent) => {
      refreshes += 1;
      return { intent, permissions: permissions(), state: state(intent) };
    },
    mutate: async () => {
      mutations += 1;
      if (options.failure === "forbidden") throw Object.assign(new Error(options.rawFailure || "RAW"), { name: "APIError", status: 403, code: "permission_denied" });
      if (options.failure === "protected") throw Object.assign(new Error(options.rawFailure || "RAW"), { name: "APIError", status: 409, code: "profile_in_use" });
      if (options.failure === "ambiguous") throw new TypeError(options.rawFailure || "RAW transport detail");
      return options.mutation ? options.mutation() : { status: "ok" };
    },
  });
  return { controller, calls: () => mutations, refreshes: () => refreshes };
}
