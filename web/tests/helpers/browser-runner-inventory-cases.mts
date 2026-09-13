import { readBrowserSuiteSource } from "./read-browser-suite-source.mts";
import assert from "node:assert/strict";
import { join } from "node:path";
import test from "node:test";
import { EXPECTED_UI_FOUNDATION_BROWSER_TESTS, assertExactBrowserTestFileInventory, assertExactUIFoundationBrowserExecution, isRequiredBrowserTestFileCompletion, requiredBrowserTestFiles, requiredScenarioNames } from "./run-ui-foundation-browser.mts";
import { independentRequiredBrowserScenarioNames, passingBrowserInventoryFixture, browserSummary, passingScenario } from "./browser-runner-inventory-fixture.mts";
import { helperRoot, uiBrowserTestPath } from "./browser-lifecycle-source-paths.mts";
import { completeWorkerRestartFocusFixtures, mandatoryWorkerRestartFocusMutants, legacyWorkerRestartFocusMutants, independentWorkerRestartFocusMutants, assertWorkerRestartFocusDiagnosticOrder } from "./browser-worker-focus-mutants.mts";
import { workerRestartOutcomeFocusIssues } from "./browser-worker-focus-analysis.mts";


export function registerRunnerInventoryCases() {


test("UI browser runner import is inert and its exact 35-test inventory accepts only the complete fixture", () => {
  assert.equal(EXPECTED_UI_FOUNDATION_BROWSER_TESTS, 35);
  assert.deepEqual([...requiredScenarioNames], [...independentRequiredBrowserScenarioNames]);
  assert.equal(new Set(requiredScenarioNames).size, requiredScenarioNames.length, "runner required scenario names must be unique");
  const fixture = passingBrowserInventoryFixture();
  assert.doesNotThrow(() => assertExactUIFoundationBrowserExecution(fixture.summary, fixture.completed));
});

test("UI browser exact inventory rejects count, result and required-name negative fixtures", () => {
  const passing = passingBrowserInventoryFixture();
  const firstRequiredName = independentRequiredBrowserScenarioNames[0];
  const negativeFixtures = [
    {
      name: "tests=0",
      summary: browserSummary({ tests: 0, passed: 0 }),
      completed: [],
    },
    {
      name: "tests=34",
      summary: browserSummary({ tests: 34, passed: 34 }),
      completed: passing.completed.slice(0, 34),
    },
    {
      name: "tests=36",
      summary: browserSummary({ tests: 36, passed: 36 }),
      completed: [...passing.completed, passingScenario("independent extra leaf")],
    },
    {
      name: "required absent",
      summary: passing.summary,
      completed: passing.completed.map((scenario) =>
        scenario.name === firstRequiredName ? passingScenario("replacement non-required leaf") : scenario),
    },
    {
      name: "required duplicated while total remains 35",
      summary: passing.summary,
      completed: passing.completed.map((scenario, index) =>
        index === passing.completed.length - 1 ? passingScenario(firstRequiredName) : scenario),
    },
    {
      name: "failure=1",
      summary: { ...passing.summary, success: false, counts: { ...passing.summary.counts, passed: 34 } },
      completed: passing.completed.map((scenario, index) => index === 0 ? { ...scenario, passed: false } : scenario),
    },
    {
      name: "skipped=1",
      summary: browserSummary({ passed: 34, skipped: 1 }),
      completed: passing.completed.map((scenario, index) => index === 0 ? { ...scenario, passed: false, skipped: true } : scenario),
    },
    {
      name: "todo=1",
      summary: browserSummary({ passed: 34, todo: 1 }),
      completed: passing.completed.map((scenario, index) => index === 0 ? { ...scenario, passed: false, todo: true } : scenario),
    },
    {
      name: "cancelled=1",
      summary: browserSummary({ passed: 34, cancelled: 1 }),
      completed: passing.completed,
    },
  ];

  for (const fixture of negativeFixtures) {
    assert.throws(
      () => assertExactUIFoundationBrowserExecution(fixture.summary, fixture.completed),
      `${fixture.name} was accepted by the exact browser inventory`,
    );
  }
});

test("UI browser runner rejects missing, duplicate, renamed, and reordered test-file inventories", () => {
  assert.deepEqual([...requiredBrowserTestFiles], [
    "tests/ui-foundation-browser.test.mts",
    "tests/ui-foundation-confirmation-browser.test.mts",
    "tests/ui-foundation-secrets-browser.test.mts",
  ]);
  assert.doesNotThrow(() => assertExactBrowserTestFileInventory(requiredBrowserTestFiles));
  const negatives = [
    requiredBrowserTestFiles.slice(0, -1),
    [requiredBrowserTestFiles[0], requiredBrowserTestFiles[1], requiredBrowserTestFiles[1]],
    [requiredBrowserTestFiles[0], requiredBrowserTestFiles[1], "tests/renamed-browser.test.mts"],
    [requiredBrowserTestFiles[1], requiredBrowserTestFiles[0], requiredBrowserTestFiles[2]],
  ];
  for (const candidate of negatives) {
    assert.throws(() => assertExactBrowserTestFileInventory(candidate), `invalid browser test-file inventory accepted: ${candidate.join(",")}`);
  }
});

test("UI browser runner excludes only exact Node test-file completion wrappers", () => {
  const webRoot = join(helperRoot, "..", "..");
  for (const file of requiredBrowserTestFiles) {
    const absoluteFile = join(webRoot, ...file.split("/"));
    assert.equal(isRequiredBrowserTestFileCompletion({
      name: file,
      nesting: 0,
      file: absoluteFile,
    }, webRoot), true, file);
    assert.equal(isRequiredBrowserTestFileCompletion({
      name: absoluteFile,
      nesting: 0,
      file: absoluteFile,
    }, webRoot), true, `${file} absolute wrapper`);
  }
  const realScenario = independentRequiredBrowserScenarioNames[0];
  assert.equal(isRequiredBrowserTestFileCompletion({
    name: realScenario,
    nesting: 0,
    file: join(webRoot, "tests", "ui-foundation-browser.test.mts"),
  }, webRoot), false);
  assert.equal(isRequiredBrowserTestFileCompletion({
    name: requiredBrowserTestFiles[0],
    nesting: 1,
    file: join(webRoot, "tests", "ui-foundation-browser.test.mts"),
  }, webRoot), false);
  assert.equal(isRequiredBrowserTestFileCompletion({
    name: requiredBrowserTestFiles[0],
    nesting: 0,
    file: join(webRoot, "tests", "renamed-browser.test.mts"),
  }, webRoot), false);
  assert.equal(isRequiredBrowserTestFileCompletion({
    name: join(webRoot, "tests", "renamed-browser.test.mts"),
    nesting: 0,
    file: join(webRoot, "tests", "ui-foundation-browser.test.mts"),
  }, webRoot), false);
});

test("Worker restart focus AST oracle accepts production and complete unconditional, switch and if-else paths", () => {
  const production = readBrowserSuiteSource(uiBrowserTestPath).replace(/\r\n/g, "\n");
  const fixtures = [
    { name: "production source", source: production },
    ...completeWorkerRestartFocusFixtures(),
  ];
  for (const fixture of fixtures) {
    assert.deepEqual(
      workerRestartOutcomeFocusIssues(fixture.source),
      [],
      `${fixture.name} was rejected by the focus AST oracle`,
    );
  }
});

test("Worker restart focus AST oracle rejects the permanent outcome, data-flow, exit and ordering mutant matrix", () => {
  const production = readBrowserSuiteSource(uiBrowserTestPath).replace(/\r\n/g, "\n");
  const mandatoryMutants = mandatoryWorkerRestartFocusMutants(production);
  assert.equal(mandatoryMutants.length, 12, "the permanent R5-P3-001 matrix must retain all twelve required mutants");

  const acceptedMutants: string[] = [];
  for (const mutant of [...mandatoryMutants, ...legacyWorkerRestartFocusMutants(production)]) {
    const issues = workerRestartOutcomeFocusIssues(mutant.source);
    if (issues.length === 0) acceptedMutants.push(mutant.name);
    if (mutant.expectedDiagnostic) {
      assert.equal(
        issues.some((issue) => issue.includes(mutant.expectedDiagnostic as string)),
        true,
        `${mutant.name} did not report ${mutant.expectedDiagnostic}: ${issues.join(" | ")}`,
      );
    }
  }
  assert.deepEqual(acceptedMutants, [], "focus AST oracle accepted negative mutants");
});

test("Worker restart focus AST oracle fails closed for independent nested-helper and try-finally bypasses with deterministic diagnostics", () => {
  const mutants = independentWorkerRestartFocusMutants();
  assert.equal(mutants.length, 2);
  for (const mutant of mutants) {
    const first = workerRestartOutcomeFocusIssues(mutant.source);
    const second = workerRestartOutcomeFocusIssues(mutant.source);
    assert.notDeepEqual(first, [], `${mutant.name} was accepted by the focus AST oracle`);
    assert.deepEqual(second, first, `${mutant.name} diagnostics were not deterministic`);
    assert.equal(first.some((issue) => issue.includes(mutant.expectedDiagnostic)), true, mutant.name);
    assertWorkerRestartFocusDiagnosticOrder(first);
  }
});
}
