import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";

import { requiredOperationSuiteFiles } from "./helpers/run-ui-foundation-operations.mts";

type InventoryRow = Readonly<{
  id: string;
  risk: string;
  retry: string;
  migrationWave: string;
  route: string;
}>;

const inventory = readFileSync(new URL("./fixtures/ui-foundation-action-inventory.jsonl", import.meta.url), "utf8")
  .trimEnd()
  .split("\n")
  .map((line) => JSON.parse(line) as InventoryRow);

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

test("final UI Foundation denominator is exactly 100/100 across the nine action families", () => {
  const ids = inventory.map(({ id }) => id);
  assert.equal(ids.length, 100);
  assert.equal(new Set(ids).size, 100);
  assert.deepEqual(familyCounts(ids), {
    APP: 2,
    ARC: 5,
    AUTH: 21,
    NOD: 5,
    OBS: 5,
    RES: 40,
    STR: 11,
    UPD: 10,
    WKR: 1,
  });
  assert.equal(ids.filter((id) => id.startsWith("STR-") || id.startsWith("RES-") || id.startsWith("APP-")).length, 53, "Bundle 7 53/53");
  assert.equal(inventory.filter(({ migrationWave }) => migrationWave === "3A").length, 32, "Wave 3A 32/32");
  assert.equal(inventory.filter(({ migrationWave, id }) => migrationWave === "3B" && id.startsWith("RES-")).length, 8, "Wave 3B Resource 8/8");
  assert.equal(inventory.filter(({ migrationWave, id }) => migrationWave === "3B" && id.startsWith("APP-")).length, 2, "APP 2/2");
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
    "tests/app-settings-foundation-migration.test.mts",
    "tests/remaining-consumers-foundation.test.mts",
    "tests/ui-foundation-final-gate.test.mts",
  ];
  for (const file of required) assert.ok(requiredOperationSuiteFiles.includes(file), file);
});

function readAuthoritySources() {
  return new Map(authoritySources.map((path) => [path, readFileSync(new URL(`../${path}`, import.meta.url), "utf8")]));
}

function assertFinalActionCoverage(rows: readonly InventoryRow[], sources: ReadonlyMap<string, string>) {
  assert.deepEqual([...sources.keys()], [...authoritySources]);
  const implementationIDs = [...sources.values()]
    .flatMap((source) => source.match(/\b(?:APP|ARC|AUTH|NOD|OBS|RES|STR|UPD|WKR)-\d{2}\b/gu) || []);
  const uniqueImplementationIDs = [...new Set(implementationIDs)].sort();
  const inventoryIDs = rows.map(({ id }) => id).sort();
  assert.deepEqual(uniqueImplementationIDs, inventoryIDs, "implementation authority IDs must equal canonical inventory IDs");
}

function familyCounts(ids: readonly string[]) {
  const counts: Record<string, number> = {};
  for (const id of ids) {
    const family = id.split("-")[0];
    counts[family] = (counts[family] || 0) + 1;
  }
  return counts;
}
