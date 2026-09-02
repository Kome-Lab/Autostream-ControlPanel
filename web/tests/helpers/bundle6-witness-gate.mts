import assert from "node:assert/strict";

export type Bundle6Witness = Readonly<{
  task_id: string;
  objective_clauses: readonly string[];
  production_sources: readonly string[];
  behaviors: readonly string[];
  behavior_test: string;
  package_script: string;
  workflow_command: string;
  primary_oracle: string;
  required_scenarios: readonly string[];
}>;

export type Bundle6WitnessManifest = Readonly<{
  format_version: number;
  expected_task_count: number;
  witnesses: readonly Bundle6Witness[];
}>;

export type CompletedWitnessScenario = Readonly<{
  name: string;
  passed: boolean;
  skipped: boolean;
  todo: boolean;
}>;

export type WitnessRunSummary = Readonly<{
  success: boolean;
  counts: Readonly<{
    cancelled: number;
    passed: number;
    skipped: number;
    tests: number;
    todo: number;
  }>;
}>;

const authorities = Object.freeze({
  "UI-FOUNDATION-001B-B09C-OBSERVABILITY": Object.freeze({
    behaviorTest: "tests/observability-actions.test.mts",
  }),
  "UI-FOUNDATION-001B-B09E-UPDATER-UI": Object.freeze({
    behaviorTest: "tests/system-updates.test.mts",
  }),
});

const expectedTaskIDs = Object.freeze(Object.keys(authorities).sort());

export function assertBundle6WitnessManifest(
  manifest: Bundle6WitnessManifest,
  context: Readonly<{
    fileExists: (relativePath: string) => boolean;
    packageScripts: Readonly<Record<string, unknown>>;
    workflowSource: string;
  }>,
) {
  assert.equal(manifest.format_version, 1, "Bundle 6 witness format must be version 1");
  assert.equal(manifest.expected_task_count, 2, "Bundle 6 witness denominator must remain 2");
  assert.equal(manifest.witnesses.length, 2, "Bundle 6 must bind exactly two R6F witnesses");
  assert.deepEqual(
    manifest.witnesses.map((witness) => witness.task_id).sort(),
    expectedTaskIDs,
    "Bundle 6 witness tasks must not be removed or substituted",
  );

  for (const witness of manifest.witnesses) {
    const authority = authorities[witness.task_id as keyof typeof authorities];
    assert.ok(authority, `unrelated witness substituted for ${witness.task_id}`);
    assert.equal(witness.behavior_test, authority.behaviorTest, `${witness.task_id} behavior test changed`);
    assert.equal(witness.primary_oracle, "runtime-behavior", `${witness.task_id} must use a runtime behavior oracle`);
    assertNonEmptyUnique(witness.objective_clauses, `${witness.task_id} objective clauses`);
    assertNonEmptyUnique(witness.production_sources, `${witness.task_id} production sources`);
    assertNonEmptyUnique(witness.behaviors, `${witness.task_id} behaviors`);
    assertNonEmptyUnique(witness.required_scenarios, `${witness.task_id} required scenarios`);
    for (const path of [...witness.production_sources, witness.behavior_test]) {
      assert.equal(context.fileExists(path), true, `${witness.task_id} witness path is missing: ${path}`);
    }
    const packageCommand = context.packageScripts[witness.package_script];
    assert.equal(typeof packageCommand, "string", `${witness.task_id} package script is missing`);
    assert.match(packageCommand as string, /tests\/bundle6-witness-gate\.test\.mts/);
    assert.match(packageCommand as string, /tests\/helpers\/run-bundle6-witnesses\.mts/);
    assert.equal(
      context.workflowSource.includes(witness.workflow_command),
      true,
      `${witness.task_id} is not reachable from Official web CI`,
    );
  }
}

export function assertBundle6WitnessExecution(
  manifest: Bundle6WitnessManifest,
  summary: WitnessRunSummary | undefined,
  completed: readonly CompletedWitnessScenario[],
) {
  assert.ok(summary, "Bundle 6 witness runner did not report a summary");
  assert.equal(summary.success, true, "Bundle 6 focused witness run failed");
  assert.ok(summary.counts.tests > 0, "Bundle 6 focused witness run matched zero tests");
  assert.ok(summary.counts.passed > 0, "Bundle 6 focused witness run passed zero tests");
  assert.equal(summary.counts.cancelled, 0, "Bundle 6 focused witness run cancelled tests");
  assert.equal(summary.counts.skipped, 0, "Bundle 6 focused witness run skipped tests");
  assert.equal(summary.counts.todo, 0, "Bundle 6 focused witness run left TODO tests");

  for (const witness of manifest.witnesses) {
    for (const requiredName of witness.required_scenarios) {
      const matches = completed.filter((scenario) => scenario.name === requiredName);
      assert.equal(matches.length, 1, `${witness.task_id} required scenario did not run exactly once: ${requiredName}`);
      assert.equal(matches[0]?.passed, true, `${witness.task_id} required scenario did not pass: ${requiredName}`);
      assert.equal(matches[0]?.skipped, false, `${witness.task_id} required scenario was skipped: ${requiredName}`);
      assert.equal(matches[0]?.todo, false, `${witness.task_id} required scenario was TODO: ${requiredName}`);
    }
  }
}

function assertNonEmptyUnique(values: readonly string[], label: string) {
  assert.ok(Array.isArray(values) && values.length > 0, `${label} must not be empty`);
  assert.equal(new Set(values).size, values.length, `${label} must be unique`);
  for (const value of values) {
    assert.equal(typeof value, "string", `${label} contains a non-string value`);
    assert.equal(value.trim(), value, `${label} contains an untrimmed value`);
    assert.ok(value.length > 0, `${label} contains an empty value`);
  }
}
