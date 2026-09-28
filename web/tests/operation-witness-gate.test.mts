import assert from "node:assert/strict";
import { existsSync, readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import test from "node:test";

import {
  assertOperationWitnessExecution,
  assertOperationWitnessManifest,
  type OperationWitnessManifest,
} from "./helpers/operation-witness-gate.mts";

const webRoot = fileURLToPath(new URL("..", import.meta.url));
const manifest = JSON.parse(
  readFileSync(new URL("./fixtures/operation-witnesses.json", import.meta.url), "utf8"),
) as OperationWitnessManifest;
const packageJSON = JSON.parse(readFileSync(new URL("../package.json", import.meta.url), "utf8")) as {
  scripts?: Record<string, unknown>;
};
const workflowSource = readFileSync(new URL("../../.github/workflows/ci.yml", import.meta.url), "utf8");

function fileExists(relativePath: string) {
  return existsSync(`${webRoot}/${relativePath}`);
}

function passingSummary(testCount = 7) {
  return {
    success: true,
    counts: { cancelled: 0, passed: testCount, skipped: 0, tests: testCount, todo: 0 },
  } as const;
}

function completedRequiredScenarios(candidate = manifest) {
  return candidate.witnesses.flatMap((witness) => witness.required_scenarios.map((name) => ({
    name,
    passed: true,
    skipped: false,
    todo: false,
  })));
}

test("Operation witness witness authority binds exactly B09C and B09E to blocking Official web CI", () => {
  assert.doesNotThrow(() => assertOperationWitnessManifest(manifest, {
    fileExists,
    packageScripts: packageJSON.scripts ?? {},
    workflowSource,
  }));
});

test("Operation witness witness negatives reject removal substitution zero denominator and non-execution", () => {
  const context = { fileExists, packageScripts: packageJSON.scripts ?? {}, workflowSource };
  assert.throws(
    () => assertOperationWitnessManifest({ ...manifest, witnesses: manifest.witnesses.slice(1) }, context),
    /exactly two|removed or substituted/,
  );
  const substituted = structuredClone(manifest);
  substituted.witnesses[0].behavior_test = "tests/observability-ui-regressions.test.mts";
  assert.throws(() => assertOperationWitnessManifest(substituted, context), /behavior test changed/);
  assert.throws(
    () => assertOperationWitnessManifest({ ...manifest, expected_task_count: 0 }, context),
    /denominator must remain 2/,
  );
  const completed = completedRequiredScenarios();
  assert.throws(
    () => assertOperationWitnessExecution(manifest, passingSummary(0), []),
    /matched zero tests/,
  );
  assert.throws(
    () => assertOperationWitnessExecution(manifest, passingSummary(), completed.slice(1)),
    /required scenario did not run exactly once/,
  );
});
