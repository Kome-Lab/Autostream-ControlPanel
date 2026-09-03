import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";

import {
  assertOperationsExecution,
  assertOperationsSuiteInventory,
  assertOperationsWiring,
  requiredOperationSuiteFiles,
} from "./helpers/run-ui-foundation-operations.mts";
import type { BrowserSuiteSummary as OperationsSuiteSummary } from "./helpers/ui-foundation-assertions.mts";

test("operations runner has a non-empty, unique, exact, existing suite inventory", () => {
  assert.equal(requiredOperationSuiteFiles.length, 19);
  assert.doesNotThrow(() => assertOperationsSuiteInventory(requiredOperationSuiteFiles));
});

test("operations inventory rejects omissions, Streams/Resource removal, duplicates, and renames", () => {
  const cases = [
    requiredOperationSuiteFiles.slice(1),
    requiredOperationSuiteFiles.filter((file) => file !== "tests/streams-foundation-migration.test.mts"),
    requiredOperationSuiteFiles.filter((file) => file !== "tests/generic-resource-foundation-migration.test.mts"),
    [...requiredOperationSuiteFiles, requiredOperationSuiteFiles[0]],
    requiredOperationSuiteFiles.map((file) => file === "tests/app-settings-foundation-migration.test.mts" ? "tests/app-settings-renamed.test.mts" : file),
  ];
  for (const candidate of cases) assert.throws(() => assertOperationsSuiteInventory(candidate));
});

test("operations execution rejects zero, failure, skip, todo, cancel, crash, and premature exit summaries", () => {
  const passing = summary();
  assert.doesNotThrow(() => assertOperationsExecution(passing));
  for (const candidate of [
    summary({ tests: 0, passed: 0 }),
    { ...passing, success: false, counts: { ...passing.counts, passed: passing.counts.tests - 1 } },
    summary({ skipped: 1, passed: 39 }),
    summary({ todo: 1, passed: 39 }),
    summary({ cancelled: 1, passed: 39 }),
    undefined,
  ]) assert.throws(() => assertOperationsExecution(candidate));
});

test("package and Official web job wire both blocking entrypoints before lint/build", () => {
  const packageJSON = JSON.parse(readFileSync(new URL("../package.json", import.meta.url), "utf8"));
  const workflow = readFileSync(new URL("../../.github/workflows/ci.yml", import.meta.url), "utf8");
  assert.doesNotThrow(() => assertOperationsWiring(packageJSON, workflow));
});

test("wiring validator rejects source-only, omitted, weakened, and late operations execution", () => {
  const packageJSON = JSON.parse(readFileSync(new URL("../package.json", import.meta.url), "utf8")) as { scripts: Record<string, string> };
  const workflow = readFileSync(new URL("../../.github/workflows/ci.yml", import.meta.url), "utf8");
  assert.throws(() => assertOperationsWiring({ ...packageJSON, scripts: { ...packageJSON.scripts, "test:ui-foundation-operations": "node --test tests/ui-foundation-final-gate.test.mts" } }, workflow));
  assert.throws(() => assertOperationsWiring(packageJSON, workflow.replace("npm run test:ui-foundation-operations", "npm run typecheck")));
  assert.throws(() => assertOperationsWiring(packageJSON, workflow.replace("npm run test:ui-foundation-browser", "npm run typecheck")));
  const late = workflow.replace("npm run test:ui-foundation-operations", "npm run typecheck").replace("npm run build", "npm run build\n      - run: npm run test:ui-foundation-operations");
  assert.throws(() => assertOperationsWiring(packageJSON, late));
});

function summary(overrides: Partial<OperationsSuiteSummary["counts"]> = {}): OperationsSuiteSummary {
  return {
    success: true,
    counts: {
      cancelled: 0,
      passed: 40,
      skipped: 0,
      suites: 19,
      tests: 40,
      todo: 0,
      topLevel: 24,
      ...overrides,
    },
  };
}
