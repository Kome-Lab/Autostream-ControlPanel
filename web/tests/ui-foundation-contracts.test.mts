import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import { readFileSync } from "node:fs";
import { join } from "node:path";
import { fileURLToPath } from "node:url";
import test from "node:test";

import type {
  ActionAvailability,
  ActionId,
  ActionRisk,
  ActionVisibility,
  ConfirmationPolicy,
  MutationOutcome,
  RetryPolicy,
  TypedTokenSource,
} from "../src/lib/foundation/actions/contracts.ts";
import type { AdaptedAPIError } from "../src/lib/foundation/api-errors/contracts.ts";
import type { PermissionRequirement } from "../src/lib/foundation/permissions/contracts.ts";
import type { Freshness, RemoteState } from "../src/lib/foundation/remote-state/contracts.ts";
import type { DomainStatusPresentation } from "../src/lib/foundation/status/contracts.ts";
import { assertUIFoundationContractBoundaries } from "./helpers/ui-foundation-contract-imports.mts";
import { actionInventoryKeys, currentActionInventory, frozenActionInventory, v2ActionInventory } from "./helpers/ui-foundation-action-inventory.mts";

const webRoot = fileURLToPath(new URL("..", import.meta.url));
const fixtureRoot = join(webRoot, "tests", "fixtures");
const reasonKey = "actions" as const;
const adaptedError = { kind: "unknown", messageKey: reasonKey } as const satisfies AdaptedAPIError;

const risks = ["routine", "guarded", "high", "critical"] as const satisfies readonly ActionRisk[];
const visibility = [
  { kind: "visible" },
  { kind: "hidden", reason: "not-applicable" },
] as const satisfies readonly ActionVisibility[];
const availability = [
  { kind: "allowed" },
  { kind: "denied", reasonKey },
  { kind: "blocked", reasonKey },
  { kind: "unknown", reasonKey },
  { kind: "pending", reasonKey },
] as const satisfies readonly ActionAvailability[];
const permissions = [
  { kind: "all", permissions: ["workers.restart"] },
  { kind: "any", permissions: ["workers.restart"] },
  { kind: "none" },
] as const satisfies readonly PermissionRequirement[];
const typedTokens = [
  { kind: "target-label" },
  { kind: "public-stable-id" },
  { kind: "fixed-ascii", value: "CONFIRM" },
] as const satisfies readonly TypedTokenSource[];
const confirmations = [
  { mode: "none", requireSubmitRevalidation: false },
  { mode: "consequence", consequenceKey: reasonKey, requireSubmitRevalidation: true },
  { mode: "typed-target", consequenceKey: reasonKey, typedToken: typedTokens[0], requireSubmitRevalidation: true },
] as const satisfies readonly ConfirmationPolicy[];
const retries = [
  { kind: "never" },
  { kind: "manual-after-refresh" },
  { kind: "server-idempotent", maxAttempts: 2 },
  { kind: "lookup-only" },
] as const satisfies readonly RetryPolicy[];
const mutationOutcomes = [
  { kind: "succeeded", value: "ok" },
  { kind: "failed", error: adaptedError },
  { kind: "outcome_unknown", nextAction: "refresh-resource" },
] as const satisfies readonly MutationOutcome<string>[];
const freshness = [
  { kind: "fresh", lastSuccessAt: 1 },
  { kind: "refreshing", lastSuccessAt: 1 },
  { kind: "stale", lastSuccessAt: 1, error: adaptedError },
] as const satisfies readonly Freshness[];
const remoteStates = [
  { kind: "initial-loading" },
  { kind: "empty", freshness: freshness[2] },
  { kind: "ready", data: ["worker"], freshness: freshness[2] },
  { kind: "partial", data: ["worker"], missingSections: ["health"], sectionErrors: { health: adaptedError }, freshness: freshness[2] },
  { kind: "blocking-error", error: adaptedError },
] as const satisfies readonly RemoteState<readonly string[]>[];
const statuses = [
  { known: true, tone: "success", labelKey: reasonKey, icon: "healthy" },
  { known: false, tone: "unknown", labelKey: reasonKey, icon: "unknown" },
] as const satisfies readonly DomainStatusPresentation[];

const normativeMatrix = {
  routine: {
    defaultConfirmation: "none",
    submitRevalidation: "normally-not-required",
    ambiguousRetry: "explicit-server-idempotent-only",
  },
  guarded: {
    defaultConfirmation: "consequence",
    submitRevalidation: "required-when-state-dependent",
    ambiguousRetry: "manual-after-refresh",
  },
  high: {
    defaultConfirmation: "consequence",
    submitRevalidation: "required",
    ambiguousRetry: "never",
  },
  critical: {
    defaultConfirmation: "typed-target",
    submitRevalidation: "required",
    ambiguousRetry: "never-unless-explicit-server-idempotent",
  },
} as const satisfies Record<ActionRisk, {
  defaultConfirmation: "none" | "consequence" | "typed-target";
  submitRevalidation: "normally-not-required" | "required-when-state-dependent" | "required";
  ambiguousRetry: "explicit-server-idempotent-only" | "manual-after-refresh" | "never" | "never-unless-explicit-server-idempotent";
}>;

const freshnessMatrix = Object.fromEntries(risks.map((risk) => [risk, {
  fresh: "allowed",
  refreshing: "blocked",
  stale: "blocked",
  "unknown/missing": "blocked",
}])) as Record<ActionRisk, Record<"fresh" | "refreshing" | "stale" | "unknown/missing", "allowed" | "blocked">>;

test("pure contract fixtures cover every discriminated branch", () => {
  assert.deepEqual(risks, ["routine", "guarded", "high", "critical"]);
  assert.deepEqual(visibility.map(({ kind }) => kind), ["visible", "hidden"]);
  assert.deepEqual(availability.map(({ kind }) => kind), ["allowed", "denied", "blocked", "unknown", "pending"]);
  assert.deepEqual(permissions.map(({ kind }) => kind), ["all", "any", "none"]);
  assert.deepEqual(confirmations.map(({ mode }) => mode), ["none", "consequence", "typed-target"]);
  assert.deepEqual(typedTokens.map(({ kind }) => kind), ["target-label", "public-stable-id", "fixed-ascii"]);
  assert.deepEqual(retries.map(({ kind }) => kind), ["never", "manual-after-refresh", "server-idempotent", "lookup-only"]);
  assert.deepEqual(mutationOutcomes.map(({ kind }) => kind), ["succeeded", "failed", "outcome_unknown"]);
  assert.deepEqual(freshness.map(({ kind }) => kind), ["fresh", "refreshing", "stale"]);
  assert.deepEqual(remoteStates.map(({ kind }) => kind), ["initial-loading", "empty", "ready", "partial", "blocking-error"]);
  assert.deepEqual(statuses.map(({ known }) => known), [true, false]);
  assert.equal(remoteStates[1].freshness.kind, "stale", "empty + stale must be representable");
  assert.equal(remoteStates[2].freshness.kind, "stale", "ready + stale must be representable");
  assert.equal(remoteStates[3].freshness.kind, "stale", "partial + stale must be representable");
});

test("normative risk and freshness matrices preserve the frozen defaults", () => {
  assert.deepEqual(normativeMatrix, {
    routine: { defaultConfirmation: "none", submitRevalidation: "normally-not-required", ambiguousRetry: "explicit-server-idempotent-only" },
    guarded: { defaultConfirmation: "consequence", submitRevalidation: "required-when-state-dependent", ambiguousRetry: "manual-after-refresh" },
    high: { defaultConfirmation: "consequence", submitRevalidation: "required", ambiguousRetry: "never" },
    critical: { defaultConfirmation: "typed-target", submitRevalidation: "required", ambiguousRetry: "never-unless-explicit-server-idempotent" },
  });
  for (const risk of risks) {
    assert.deepEqual(freshnessMatrix[risk], {
      fresh: "allowed",
      refreshing: "blocked",
      stale: "blocked",
      "unknown/missing": "blocked",
    });
  }
  assert.deepEqual(
    { risk: "routine", stateIndependent: true, retry: { kind: "server-idempotent" } },
    { risk: "routine", stateIndependent: true, retry: { kind: "server-idempotent" } },
    "the future routine exception must remain a frozen fixture, not an evaluator",
  );
});

test("frozen inventory has the exact canonical bytes, rows, keys, IDs, and waves", () => {
  const raw = readFileSync(join(fixtureRoot, "ui-foundation-action-inventory.jsonl"));
  assert.equal(raw.length, 47_486);
  assert.equal(createHash("sha256").update(raw).digest("hex"), "eb5f0c21f9a9895a9fef23353d33ed81043640c646e55a1047d9b4d2ef862f37");
  assert.notDeepEqual([...raw.subarray(0, 3)], [0xef, 0xbb, 0xbf], "inventory must not have a BOM");
  assert.equal(raw.includes(0x0d), false, "inventory must use LF line endings");
  assert.equal(raw.at(-1), 0x0a, "inventory must have one terminal LF");
  assert.notEqual(raw.at(-2), 0x0a, "inventory must have exactly one terminal LF");

  const lines = raw.toString("utf8").slice(0, -1).split("\n");
  const rows = lines.map((line) => JSON.parse(line) as Record<string, unknown>);
  const requiredKeys = [
    "id", "feature", "action", "uiSource", "route", "method", "userTriggered", "permission", "risk",
    "visibility", "availabilityInputs", "confirmation", "duplicateScope", "retry", "errorMapping", "audit",
    "secretCapability", "migrationWave",
  ];
  assert.equal(rows.length, 100);
  for (const [index, row] of rows.entries()) {
    assert.deepEqual(Object.keys(row), requiredKeys, `row ${index + 1} key order`);
    assert.equal(JSON.stringify(row), lines[index], `row ${index + 1} must be compact canonical JSON`);
  }

  const ids = rows.map((row) => String(row.id));
  const inventoryIds = new Set(ids);
  assert.equal(inventoryIds.size, 100);
  assert.deepEqual(ids, [...ids].sort(), "Action IDs must be in lexical order");
  assert.equal(ids.every((id) => isFrozenActionId(id, inventoryIds)), true);
  assert.equal(isFrozenActionId("UNKNOWN-01", inventoryIds), false, "ActionId validation must use the inventory, not a regex");
  assert.deepEqual([...new Set(rows.map((row) => row.userTriggered))].sort(), ["yes", "yes (preview open/retry)"]);
  assert.equal(rows.filter((row) => row.userTriggered === "yes").length, 99);
  assert.equal(rows.filter((row) => row.userTriggered === "yes (preview open/retry)").length, 1);
  assert.equal(rows.find((row) => row.userTriggered === "yes (preview open/retry)")?.id, "STR-11");
  assert.deepEqual([...new Set(rows.map((row) => row.risk))].sort(), ["critical", "guarded", "high", "routine"]);
  assert.equal(
    rows.flatMap((row) => Object.values(row)).filter((value) => ["R0", "R1", "R2", "R3"].includes(String(value))).length,
    0,
    "legacy R0-R3 normative values are forbidden",
  );

  const expectedWaveCounts = {
    "1A": 1,
    "1C": 5,
    "1D": 21,
    "1E": 5,
    "1F": 5,
    "1G": 10,
    "2A": 10,
    "Pre-Wave/B-00 then 2A": 1,
    "3A": 32,
    "3B": 10,
  };
  const actualWaveCounts = Object.fromEntries(Object.keys(expectedWaveCounts).map((wave) => [
    wave,
    rows.filter((row) => row.migrationWave === wave).length,
  ]));
  assert.deepEqual(actualWaveCounts, expectedWaveCounts);
  assert.equal(rows.find((row) => row.id === "RES-23")?.migrationWave, "3B");
  assert.equal(rows.find((row) => row.id === "RES-24")?.migrationWave, "3B");
  for (let number = 30; number <= 39; number += 1) {
    assert.equal(rows.find((row) => row.id === `RES-${number}`)?.migrationWave, "3A");
  }

  const metadata = JSON.parse(readFileSync(join(fixtureRoot, "ui-foundation-action-inventory.meta.json"), "utf8"));
  assert.deepEqual(metadata, {
    inventoryAuthorityHead: "34e32c276ddd6d235167112598a5ec851305ad8d",
    designArtifactSha256: "82e5dea2a3453f7b71fca1cc2ef974b9aa47a653dfe53f15949354b140361011",
    canonicalBytes: 47_486,
    canonicalSha256: "eb5f0c21f9a9895a9fef23353d33ed81043640c646e55a1047d9b4d2ef862f37",
    rowCount: 100,
    uniqueIdCount: 100,
    requiredKeyCount: 18,
    excludedAutomaticOperationCount: 6,
    waveCounts: expectedWaveCounts,
  });
});

test("the v2 extension uses the same typed schema and current acceptance includes all 106 actions", () => {
  const raw = readFileSync(join(fixtureRoot, "ui-foundation-action-inventory-v2.jsonl"));
  assert.equal(raw.length, 2_918);
  assert.equal(createHash("sha256").update(raw).digest("hex"), "399c58edacfa31e5416c1cd77b4aa26c19cb79182391a97493f380eeffdda7b8");
  assert.notDeepEqual([...raw.subarray(0, 3)], [0xef, 0xbb, 0xbf]);
  assert.equal(raw.includes(0x0d), false);
  assert.equal(raw.at(-1), 0x0a);
  assert.notEqual(raw.at(-2), 0x0a);
  const lines = raw.toString("utf8").slice(0, -1).split("\n");
  assert.deepEqual(v2ActionInventory.map(({ id }) => id), ["RES-41", "RES-42", "RES-43", "RES-44", "RES-45", "RES-46"]);
  for (const [index, row] of v2ActionInventory.entries()) {
    assert.deepEqual(Object.keys(row), actionInventoryKeys);
    assert.equal(JSON.stringify(row), lines[index]);
    assert.equal(row.migrationWave, "v2");
    assert.equal(row.userTriggered, "yes");
  }
  assert.equal(currentActionInventory.length, 106);
  assert.equal(new Set(currentActionInventory.map(({ id }) => id)).size, 106);
  assert.deepEqual(currentActionInventory.map(({ id }) => id), currentActionInventory.map(({ id }) => id).sort());
  assert.deepEqual(currentActionInventory.filter(({ migrationWave }) => migrationWave !== "v2"), frozenActionInventory);
  assert.deepEqual(currentActionInventory.filter(({ migrationWave }) => migrationWave === "v2"), v2ActionInventory);
  assert.equal(currentActionInventory.filter(({ userTriggered }) => userTriggered === "yes").length, 105);
  assert.deepEqual(currentActionInventory.filter(({ userTriggered }) => userTriggered !== "yes").map(({ id }) => id), ["STR-11"]);
});

test("excluded automatic operations are the exact six reviewed families", () => {
  const exclusions = JSON.parse(readFileSync(join(fixtureRoot, "ui-foundation-action-exclusions.json"), "utf8"));
  assert.deepEqual(exclusions, [
    {
      id: "EX-AUTO-01",
      source: "web/src/features/queries.ts polling query hooks; archive-view.tsx artifact poll",
      routeOrOperation: "existing GET resource/status/system-update/bootstrap/artifact routes",
      reason: "timer/background polling; no new user intent",
    },
    {
      id: "EX-AUTO-02",
      source: "use-shell-session-guard.ts/useShellSessionGuard",
      routeOrOperation: "POST /auth/session/refresh",
      reason: "activity-driven automatic session refresh; explicitly not logout or a user action",
    },
    {
      id: "EX-AUTO-03",
      source: "features/queries.ts/useSetupStatus; Shell/auth bootstrap",
      routeOrOperation: "GET /setup/status and auth bootstrap reads",
      reason: "initial routing/session/setup discovery read",
    },
    {
      id: "EX-AUTO-04",
      source: "internal/httpapi/server.go OAuth login callback handlers",
      routeOrOperation: "GET/POST /auth/oauth/callback",
      reason: "provider-driven automatic callback after the included start action",
    },
    {
      id: "EX-AUTO-05",
      source: "internal/httpapi/server.go connected-account callback handlers",
      routeOrOperation: "GET/POST /integrations/oauth-accounts/callback",
      reason: "provider-driven automatic callback after connect/relink start",
    },
    {
      id: "EX-AUTO-06",
      source: "feature mutation onSuccess and recovery controllers",
      routeOrOperation: "existing invalidateQueries / refetch calls",
      reason: "consequence/reconciliation of an included action, not a new action",
    },
  ]);
  assert.equal(exclusions.length, 6);
  assert.deepEqual(exclusions.map((entry: { id: string }) => entry.id), [
    "EX-AUTO-01", "EX-AUTO-02", "EX-AUTO-03", "EX-AUTO-04", "EX-AUTO-05", "EX-AUTO-06",
  ]);
  assert.equal(new Set(exclusions.map((entry: { id: string }) => entry.id)).size, 6);
  assert.equal(exclusions.every((entry: Record<string, unknown>) =>
    ["id", "source", "routeOrOperation", "reason"].every((key) => typeof entry[key] === "string" && entry[key] !== "")), true);
  assert.equal(exclusions.some((entry: { routeOrOperation: string }) => entry.routeOrOperation === "POST /auth/session/refresh"), true);

  assert.equal(currentActionInventory.some((row) => row.id === "AUTH-08" && row.route === "/auth/logout"), true, "logout must remain included");
  for (const row of v2ActionInventory) {
    assert.equal(exclusions.some((entry: { routeOrOperation: string }) => entry.routeOrOperation === `${row.method} ${row.route}`), false, `${row.id} must not be excluded`);
  }
});

test("foundation imports, ownership, syntax, and dependency graph stay pure", () => {
  assert.deepEqual(assertUIFoundationContractBoundaries(webRoot), { fileCount: 22 });
});

function isFrozenActionId(value: string, inventoryIds: ReadonlySet<string>): value is ActionId {
  return inventoryIds.has(value);
}
