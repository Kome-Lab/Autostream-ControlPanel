import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";

import { resourceActionDescriptors } from "../src/features/resources/resource-action-descriptors.ts";
import { requiredOperationSuiteFiles } from "./helpers/run-ui-foundation-operations.mts";
import {
  assertResourceActionInventory,
  currentActionInventory as inventory,
  frozenActionInventory,
  v2ActionInventory,
  type ActionInventoryRow,
} from "./helpers/ui-foundation-action-inventory.mts";

const authoritySources = Object.freeze([
  "src/features/settings/app-settings-action-policy.ts",
  "src/features/archive/archive-action-policy.ts",
  "src/features/account/account-action-policy.ts",
  "src/features/nodes/node-action-descriptors.ts",
  "src/features/observability/action-policy.ts",
  "src/features/resources/resource-action-descriptors.ts",
  "src/features/streams/stream-action-descriptors.ts",
  "src/features/application/updater-action-policy.ts",
  "src/features/workers/workers-action-descriptors.ts",
]);
const authorityByFamily = new Map(["APP", "ARC", "AUTH", "NOD", "OBS", "RES", "STR", "UPD", "WKR"].map((family, index) => [family, authoritySources[index]]));

test("final UI Foundation denominator is the frozen 100 plus six v2 actions, exactly 106/106", () => {
  const ids = inventory.map(({ id }) => id);
  assert.equal(frozenActionInventory.length, 100);
  assert.equal(ids.length, 106);
  assert.equal(new Set(ids).size, 106);
  assert.deepEqual(familyCounts(ids), {
    APP: 2,
    ARC: 5,
    AUTH: 21,
    NOD: 5,
    OBS: 5,
    RES: 46,
    STR: 11,
    UPD: 10,
    WKR: 1,
  });
  assert.equal(frozenActionInventory.filter(({ id }) => id.startsWith("STR-") || id.startsWith("RES-") || id.startsWith("APP-")).length, 53, "historical Bundle 7 53/53");
  assert.equal(ids.filter((id) => id.startsWith("STR-") || id.startsWith("RES-") || id.startsWith("APP-")).length, 59, "current Streams/Resource/App 59/59");
  assert.equal(inventory.filter(({ migrationWave }) => migrationWave === "3A").length, 32, "Wave 3A 32/32");
  assert.equal(inventory.filter(({ migrationWave, id }) => migrationWave === "3B" && id.startsWith("RES-")).length, 8, "Wave 3B Resource 8/8");
  assert.equal(inventory.filter(({ migrationWave, id }) => migrationWave === "3B" && id.startsWith("APP-")).length, 2, "APP 2/2");
  assert.deepEqual(inventory.filter(({ migrationWave }) => migrationWave === "v2"), v2ActionInventory);
  assert.deepEqual(v2ActionInventory.map(({ id }) => id), ["RES-41", "RES-42", "RES-43", "RES-44", "RES-45", "RES-46"]);
});

test("typed implementation authorities own every canonical action ID exactly once", () => {
  assert.doesNotThrow(() => assertFinalActionCoverage(inventory, readAuthoritySources()));
  const nodeSource = readFileSync(new URL("../src/features/nodes/node-action-descriptors.ts", import.meta.url), "utf8");
  assert.match(nodeSource, /NODE_FOUNDATION_SOURCE_ENABLED\s*=\s*false\s+as const/);
  assert.match(nodeSource, /A2 is intentionally source-only/);
});

test("the final coverage oracle rejects removal of one canonical action", () => {
  const sources = readAuthoritySources();
  const appPath = authoritySources[0];
  const mutated = new Map(sources);
  mutated.set(appPath, (mutated.get(appPath) || "").replaceAll('"APP-02"', '"APP-X2"'));
  assert.throws(() => assertFinalActionCoverage(inventory, mutated), /implementation authority IDs/);
});

test("the current coverage oracle rejects omission of any v2 inventory row or implementation action", () => {
  for (const { id } of v2ActionInventory) {
    const sources = readAuthoritySources();
    const path = authorityByFamily.get("RES")!;
    sources.set(path, sources.get(path)!.replaceAll(`"${id}"`, '"RES-XX"'));
    assert.throws(() => assertFinalActionCoverage(inventory, sources), /implementation authority IDs/, id);
    assert.throws(() => assertFinalActionCoverage(inventory.filter((row) => row.id !== id), readAuthoritySources()), /implementation authority IDs/, id);
  }
});

test("current coverage rejects duplicate IDs, multiple owners, and actions moved to the wrong family owner", () => {
  assert.throws(() => assertFinalActionCoverage([...inventory, v2ActionInventory[0]], readAuthoritySources()), /inventory IDs must be unique/);
  const duplicate = readAuthoritySources();
  duplicate.set(authoritySources[0], duplicate.get(authoritySources[0]) + '\n// "RES-41"\n');
  assert.throws(() => assertFinalActionCoverage(inventory, duplicate), /exactly one implementation owner/);
  const moved = new Map(duplicate);
  const resourcePath = authorityByFamily.get("RES")!;
  moved.set(resourcePath, moved.get(resourcePath)!.replaceAll('"RES-41"', '"RES-XX"'));
  assert.throws(() => assertFinalActionCoverage(inventory, moved), /expected family authority/);
});

test("current coverage rejects a v2 risk, permission, or UI mutation owner mismatch", () => {
  for (const { id, risk } of v2ActionInventory) {
    const changedRisk = inventory.map((row) => row.id === id ? { ...row, risk: risk === "high" ? "guarded" as const : "high" as const } : row);
    assert.throws(() => assertFinalActionCoverage(changedRisk, readAuthoritySources()), new RegExp(`${id} risk`));
  }
  for (const [field, value, message] of [
    ["permission", "video_cover_presets.update", "RES-41 permission"],
    ["uiSource", "edit-resource-button.tsx/EditResourceButton", "RES-41 UI mutation owner"],
  ] as const) {
    const changed = inventory.map((row) => row.id === "RES-41" ? { ...row, [field]: value } : row);
    assert.throws(() => assertFinalActionCoverage(changed, readAuthoritySources()), new RegExp(message));
  }
});

test("final acceptance keeps canonical risks, exclusions, logout, and bounded retry semantics", () => {
  assert.deepEqual([...new Set(inventory.map(({ risk }) => risk))].sort(), ["critical", "guarded", "high", "routine"]);
  assert.equal(inventory.some((row) => Object.values(row).some((value) => /\bR[0-3]\b/u.test(String(value)))), false);
  const retryRows = inventory.filter(({ retry }) => retry !== "0");
  assert.deepEqual(retryRows.map(({ id }) => id), [
    "ARC-05", "OBS-01", "OBS-03", "STR-08", "UPD-01", "UPD-02", "UPD-03", "UPD-05", "UPD-06", "UPD-07", "UPD-09", "UPD-10",
  ]);
  assert.equal(retryRows.every(({ retry }) => /^(?:I \(GET\/range\)|M|I,L|L)$/u.test(retry)), true);

  const exclusions = JSON.parse(readFileSync(new URL("./fixtures/ui-foundation-action-exclusions.json", import.meta.url), "utf8")) as Array<{ id: string; routeOrOperation: string }>;
  assert.equal(exclusions.length, 6);
  assert.equal(exclusions.some(({ routeOrOperation }) => routeOrOperation === "POST /auth/session/refresh"), true);
  assert.equal(inventory.some(({ id, route }) => id === "AUTH-08" && route === "/auth/logout"), true);
});

test("durable operations inventory includes every final acceptance owner suite", () => {
  const required = [
    "tests/ui-foundation-contracts.test.mts",
    "tests/ui-foundation-permissions.test.mts",
    "tests/ui-foundation-api-errors.test.mts",
    "tests/ui-foundation-remote-state.test.mts",
    "tests/ui-foundation-status.test.mts",
    "tests/ui-foundation-secrets.test.mts",
    "tests/streams-foundation-migration.test.mts",
    "tests/streams-component-split.test.mts",
    "tests/streams-visual-integration.test.mts",
    "tests/operational-remote-state.test.mts",
    "tests/generic-resource-foundation-migration.test.mts",
    "tests/preset-resource-actions.test.mts",
    "tests/app-settings-foundation-migration.test.mts",
    "tests/remaining-consumers-foundation.test.mts",
    "tests/ui-foundation-final-gate.test.mts",
  ];
  for (const file of required) assert.ok(requiredOperationSuiteFiles.includes(file), file);
});

function readAuthoritySources() {
  return new Map(authoritySources.map((path) => [path, readFileSync(new URL(`../${path}`, import.meta.url), "utf8")]));
}

function assertFinalActionCoverage(rows: readonly ActionInventoryRow[], sources: ReadonlyMap<string, string>) {
  assert.deepEqual([...sources.keys()], [...authoritySources]);
  const owners = new Map<string, string[]>();
  for (const [path, source] of sources) {
    const ids = new Set(source.match(/\b(?:APP|ARC|AUTH|NOD|OBS|RES|STR|UPD|WKR)-\d{2}\b/gu) || []);
    for (const id of ids) owners.set(id, [...(owners.get(id) || []), path]);
  }
  const inventoryIDs = rows.map(({ id }) => id).sort();
  assert.equal(new Set(inventoryIDs).size, inventoryIDs.length, "inventory IDs must be unique");
  assert.deepEqual([...owners.keys()].sort(), inventoryIDs, "implementation authority IDs must equal canonical inventory IDs");
  for (const [id, paths] of owners) {
    assert.equal(paths.length, 1, `${id} must have exactly one implementation owner`);
    assert.equal(paths[0], authorityByFamily.get(id.split("-")[0]), `${id} expected family authority`);
  }
  assertResourceActionInventory(rows, resourceActionDescriptors);
}

function familyCounts(ids: readonly string[]) {
  const counts: Record<string, number> = {};
  for (const id of ids) {
    const family = id.split("-")[0];
    counts[family] = (counts[family] || 0) + 1;
  }
  return counts;
}
