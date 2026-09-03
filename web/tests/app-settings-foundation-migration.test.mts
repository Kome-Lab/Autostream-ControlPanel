import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { register } from "node:module";
import test from "node:test";

import type { AppSettingsActionIntent, AppSettingsActionState } from "../src/features/settings/app-settings-action-policy.ts";

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
  appSettingsActionDefinitions,
  appSettingsActionRequest,
  buildAppSettingsActionDescriptor,
  createAppSettingsActionController,
} = await import("../src/features/settings/app-settings-action-policy.ts");
const { managedAppSettingsRemoteState } = await import("../src/features/settings/app-settings-action-runtime.ts");

type InventoryRow = {
  id: string;
  route: string;
  method: string;
  permission: string;
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
  .filter(({ id }) => id.startsWith("APP-"));

const saveIntent: AppSettingsActionIntent = Object.freeze({
  id: "APP-01",
  payload: Object.freeze({ app_name: "AutoStream", smtp_password: "" }),
});
const emailIntent: AppSettingsActionIntent = Object.freeze({
  id: "APP-02",
  payload: Object.freeze({ to: "operator@example.invalid" }),
});

test("Application settings declares APP 2/2 exactly as the canonical inventory", () => {
  assert.equal(inventory.length, 2);
  assert.deepEqual(appSettingsActionDefinitions.map(({ id }) => id), ["APP-01", "APP-02"]);
  for (const row of inventory) {
    const definition = appSettingsActionDefinitions.find(({ id }) => id === row.id);
    assert.ok(definition, row.id);
    assert.equal(definition.path, row.route, `${row.id} route`);
    assert.equal(definition.method, row.method, `${row.id} method`);
    assert.equal(definition.permission, row.permission, `${row.id} permission`);
    assert.equal(definition.risk, row.risk, `${row.id} risk`);
    assert.equal(definition.auditAction, row.audit, `${row.id} audit`);
    assert.equal(row.confirmation, "C", `${row.id} confirmation authority`);
    assert.equal(row.duplicateScope, "RA", `${row.id} duplicate authority`);
    assert.equal(row.migrationWave, "3B", `${row.id} wave`);
  }
});

test("APP requests preserve endpoint, method, payload, permission, confirmation, and audit contracts", () => {
  assert.deepEqual(appSettingsActionRequest(saveIntent), {
    id: "APP-01",
    method: "PUT",
    path: "/settings/app",
    body: saveIntent.payload,
  });
  assert.deepEqual(appSettingsActionRequest(emailIntent), {
    id: "APP-02",
    method: "POST",
    path: "/settings/app/test-email",
    body: emailIntent.payload,
  });
  for (const intent of [saveIntent, emailIntent]) {
    const descriptor = buildAppSettingsActionDescriptor(intent);
    assert.ok(descriptor, intent.id);
    assert.deepEqual(descriptor.permissions, { kind: "all", permissions: ["system_settings.update"] });
    assert.equal(descriptor.confirmation.mode, "consequence");
    assert.equal(descriptor.duplicate.scope, "resource-action");
    assert.equal(descriptor.retry.kind, "never");
    assert.equal(descriptor.stateIndependent, false);
  }
});

test("wildcard allows APP actions while refreshing, stale, and missing authority fail closed", () => {
  let permissions: readonly string[] | undefined = ["*"];
  let state: AppSettingsActionState = { kind: "ready", freshness: "fresh", fingerprint: "settings-a" };
  const harness = appHarness({
    permissions: () => permissions ? { kind: "ready", permissions } : { kind: "refreshing" },
    state: () => state,
  });
  assert.equal(harness.controller.evaluate(saveIntent).availability.kind, "allowed");
  permissions = undefined;
  assert.equal(harness.controller.evaluate(saveIntent).availability.kind, "unknown");
  permissions = ["*"];
  state = { kind: "ready", freshness: "stale", fingerprint: "settings-a" };
  assert.equal(harness.controller.evaluate(saveIntent).availability.kind, "unknown");
  state = { kind: "unknown", freshness: "unavailable" };
  assert.equal(harness.controller.evaluate(saveIntent).availability.kind, "unknown");
  assert.equal(harness.calls(), 0);
});

test("open and submit independently refresh authority and block changed permission or fingerprint pre-send", async () => {
  let permissions: readonly string[] = ["system_settings.update"];
  let fingerprint = "settings-a";
  const harness = appHarness({
    permissions: () => ({ kind: "ready", permissions }),
    state: () => ({ kind: "ready", freshness: "fresh", fingerprint }),
  });
  const opened = await harness.controller.open(saveIntent);
  assert.equal(opened.kind, "allowed");
  assert.equal(harness.refreshes(), 1);
  if (opened.kind !== "allowed") return;
  permissions = [];
  assert.deepEqual(await harness.controller.submit(opened, { confirmed: true }), { kind: "blocked", reason: "permission-denied" });
  assert.equal(harness.calls(), 0);
  assert.equal(harness.refreshes(), 2);

  permissions = ["system_settings.update"];
  const reopened = await harness.controller.open(saveIntent);
  assert.equal(reopened.kind, "allowed");
  if (reopened.kind !== "allowed") return;
  fingerprint = "settings-b";
  assert.deepEqual(await harness.controller.submit(reopened, { confirmed: true }), { kind: "blocked", reason: "authority-changed" });
  assert.equal(harness.calls(), 0);
});

test("403 and ambiguous outcomes send once and latch until explicit reconciliation", async () => {
  for (const failure of ["forbidden", "ambiguous"] as const) {
    const harness = appHarness({ failure });
    const opened = await harness.controller.open(emailIntent);
    assert.equal(opened.kind, "allowed");
    if (opened.kind !== "allowed") continue;
    const result = await harness.controller.submit(opened, { confirmed: true });
    assert.equal(result.kind, failure === "ambiguous" ? "outcome_unknown" : "failed");
    assert.deepEqual(await harness.controller.submit(opened, { confirmed: true }), { kind: "blocked", reason: "reconciliation-required" });
    assert.equal(harness.calls(), 1);
    harness.controller.reconcile(emailIntent);
    const reopened = await harness.controller.open(emailIntent);
    assert.equal(reopened.kind, "allowed");
  }
});

test("duplicate APP dispatch is blocked while the first request remains pending", async () => {
  let release!: () => void;
  const pending = new Promise<void>((resolve) => { release = resolve; });
  const harness = appHarness({ mutation: async () => pending });
  const firstOpen = await harness.controller.open(saveIntent);
  const secondOpen = await harness.controller.open(saveIntent);
  assert.equal(firstOpen.kind, "allowed");
  assert.equal(secondOpen.kind, "allowed");
  if (firstOpen.kind !== "allowed" || secondOpen.kind !== "allowed") return;
  const first = harness.controller.submit(firstOpen, { confirmed: true });
  await new Promise((resolve) => setImmediate(resolve));
  assert.deepEqual(await harness.controller.submit(secondOpen, { confirmed: true }), { kind: "blocked", reason: "duplicate" });
  assert.equal(harness.calls(), 1);
  release();
  assert.equal((await first).kind, "succeeded");
});

test("conditional input secrets and hostile errors never enter descriptor, result, or stale state", async () => {
  const marker = "PRIVATE_APP_SECRET_MARKER_77";
  const intent: AppSettingsActionIntent = {
    id: "APP-01",
    payload: { app_name: "AutoStream", smtp_password: marker, turnstile_secret: marker },
  };
  const descriptor = buildAppSettingsActionDescriptor(intent);
  assert.ok(descriptor);
  assert.doesNotMatch(JSON.stringify(descriptor), new RegExp(marker));
  const harness = appHarness({ failure: "ambiguous", rawFailure: marker });
  const opened = await harness.controller.open(intent);
  assert.equal(opened.kind, "allowed");
  if (opened.kind !== "allowed") return;
  const result = await harness.controller.submit(opened, { confirmed: true });
  assert.equal(result.kind, "outcome_unknown");
  assert.doesNotMatch(JSON.stringify(result), new RegExp(marker));

  const stale = managedAppSettingsRemoteState({
    status: "error",
    isFetching: false,
    data: { app_name: "AutoStream", timezone: "Asia/Tokyo" },
    error: new TypeError(marker),
    dataUpdatedAt: 7,
  });
  assert.equal(stale.kind, "ready");
  assert.equal(stale.kind === "ready" ? stale.freshness.kind : "", "stale");
  assert.doesNotMatch(JSON.stringify(stale), new RegExp(marker));
});

test("APP secret cleanup is notified only after the request is handed off", async () => {
  let dispatches = 0;
  const sent = appHarness({ failure: "ambiguous" });
  const opened = await sent.controller.open(saveIntent);
  assert.equal(opened.kind, "allowed");
  if (opened.kind !== "allowed") return;
  assert.equal((await sent.controller.submit(opened, { confirmed: true }, { onDispatch: () => { dispatches += 1; } })).kind, "outcome_unknown");
  assert.equal(dispatches, 1);

  let fingerprint = "settings-a";
  const blocked = appHarness({ state: () => ({ kind: "ready", freshness: "fresh", fingerprint }) });
  const blockedOpen = await blocked.controller.open(saveIntent);
  assert.equal(blockedOpen.kind, "allowed");
  if (blockedOpen.kind !== "allowed") return;
  fingerprint = "settings-b";
  assert.equal((await blocked.controller.submit(blockedOpen, { confirmed: true }, { onDispatch: () => { dispatches += 1; } })).kind, "blocked");
  assert.equal(dispatches, 1);
  assert.equal(blocked.calls(), 0);
});

test("Settings UI has one guarded path, keeps cached data, and clears input secrets on APP-01 dispatch", () => {
  const source = readFileSync(new URL("../src/features/settings/settings-view.tsx", import.meta.url), "utf8");
  assert.match(source, /appSettingsIntent\("APP-01"/);
  assert.match(source, /appSettingsIntent\("APP-02"/);
  assert.match(source, /AppSettingsActionConfirmationHost/);
  assert.match(source, /RemoteStateBoundary/);
  assert.match(source, /setSMTPPassword\(""\)/);
  assert.match(source, /setTurnstileSecret\(""\)/);
  assert.match(source, /onDispatch/);
  assert.doesNotMatch(source, /\bapi(?:Post|Put|Patch|Delete)\b|DangerConfirm|window\.confirm/);
  assert.doesNotMatch(source, /error\.message(?!Key)/);
});

function appHarness(options: {
  permissions?: () => { kind: "ready"; permissions: readonly string[] } | { kind: "refreshing" } | { kind: "unavailable" };
  state?: () => AppSettingsActionState;
  failure?: "forbidden" | "ambiguous";
  rawFailure?: string;
  mutation?: () => Promise<unknown>;
} = {}) {
  let mutations = 0;
  let refreshes = 0;
  const permissions = options.permissions ?? (() => ({ kind: "ready" as const, permissions: ["*"] }));
  const state = options.state ?? (() => ({ kind: "ready" as const, freshness: "fresh" as const, fingerprint: "settings-a" }));
  const controller = createAppSettingsActionController({
    getPermissions: permissions,
    getState: state,
    refresh: async () => {
      refreshes += 1;
      return { permissions: permissions(), state: state() };
    },
    mutate: async () => {
      mutations += 1;
      if (options.failure === "forbidden") throw Object.assign(new Error(options.rawFailure || "RAW"), { name: "APIError", status: 403, code: "permission_denied" });
      if (options.failure === "ambiguous") throw new TypeError(options.rawFailure || "RAW transport detail");
      return options.mutation ? options.mutation() : { status: "ok" };
    },
  });
  return { controller, calls: () => mutations, refreshes: () => refreshes };
}
