import assert from "node:assert/strict";
import { existsSync, realpathSync } from "node:fs";
import { once } from "node:events";
import { relative, resolve } from "node:path";
import process from "node:process";
import { run } from "node:test";
import { spec } from "node:test/reporters";
import { fileURLToPath, pathToFileURL } from "node:url";

import type { BrowserSuiteSummary as OperationsSuiteSummary } from "./ui-foundation-assertions.mts";

export const requiredOperationSuiteFiles = Object.freeze([
  "tests/ui-foundations.test.mts",
  "tests/ui-foundation-contracts.test.mts",
  "tests/ui-foundation-api-errors.test.mts",
  "tests/ui-foundation-permissions.test.mts",
  "tests/ui-foundation-confirmation.test.mts",
  "tests/ui-foundation-remote-state.test.mts",
  "tests/ui-foundation-status.test.mts",
  "tests/ui-foundation-secrets.test.mts",
  "tests/workers-foundation-pilot.test.mts",
  "tests/app-shell-characterization.test.mts",
  "tests/streams-foundation-migration.test.mts",
  "tests/streams-component-split.test.mts",
  "tests/streams-visual-integration.test.mts",
  "tests/operational-remote-state.test.mts",
  "tests/generic-resource-foundation-migration.test.mts",
  "tests/app-settings-foundation-migration.test.mts",
  "tests/remaining-consumers-foundation.test.mts",
  "tests/ui-foundation-final-gate.test.mts",
  "tests/ui-foundation-operations-runner.test.mts",
] as const);

const webRoot = fileURLToPath(new URL("../..", import.meta.url));

export function assertOperationsSuiteInventory(
  candidate: readonly string[],
  candidateWebRoot = webRoot,
) {
  assert.ok(candidate.length > 0, "operations inventory must not be empty");
  assert.deepEqual(candidate, requiredOperationSuiteFiles, "operations inventory must exactly match required suites");
  assert.equal(new Set(candidate).size, candidate.length, "operations inventory must not contain duplicates");
  const resolvedRoot = realpathSync(candidateWebRoot);
  for (const file of candidate) {
    assert.match(file, /^tests\/[a-z0-9-]+\.test\.mts$/u, `invalid operations suite path: ${file}`);
    const resolvedFile = realpathSync(resolve(resolvedRoot, file));
    const withinRoot = relative(resolvedRoot, resolvedFile);
    assert.equal(withinRoot.startsWith("..") || resolve(resolvedFile) === resolvedRoot, false, `operations suite escapes web root: ${file}`);
    assert.equal(existsSync(resolvedFile), true, `missing operations suite: ${file}`);
  }
}

export function assertOperationsExecution(summary: OperationsSuiteSummary | undefined) {
  assert.ok(summary, "operations test runner did not report a summary");
  assert.equal(summary.success, true, "operations test runner reported a failure or crash");
  assert.ok(summary.counts.tests > 0, "operations suite must execute at least one test");
  assert.equal(summary.counts.passed, summary.counts.tests, "every operations test must pass");
  assert.equal(summary.counts.cancelled, 0, "operations suite must not cancel tests");
  assert.equal(summary.counts.skipped, 0, "operations suite must not skip tests");
  assert.equal(summary.counts.todo, 0, "operations suite must not leave TODO tests");
}

export function assertOperationsWiring(
  packageJSON: Readonly<{ scripts?: Readonly<Record<string, unknown>> }>,
  workflowSource: string,
) {
  const command = packageJSON.scripts?.["test:ui-foundation-operations"];
  assert.equal(command, "node --no-warnings tests/helpers/run-ui-foundation-operations.mts", "package script must execute the canonical operations runner");
  const webJob = workflowSource.slice(workflowSource.indexOf("\n  web:"), workflowSource.indexOf("\n  overall:"));
  const operationsIndex = webJob.indexOf("npm run test:ui-foundation-operations");
  const browserIndex = webJob.indexOf("npm run test:ui-foundation-browser");
  const bundle4Index = webJob.indexOf("npm run test:control-platform");
  const bundle6WitnessIndex = webJob.indexOf("npm run test:bundle6-witnesses");
  const bundle6FeatureIndex = webJob.indexOf("npm run test:bundle6-features");
  const lintIndex = webJob.indexOf("npm run lint");
  const buildIndex = webJob.indexOf("npm run build");
  assert.ok(operationsIndex >= 0, "Official web job must execute operations tests");
  assert.ok(browserIndex >= 0, "Official web job must execute browser tests");
  assert.ok(bundle4Index >= 0 && bundle6WitnessIndex >= 0 && bundle6FeatureIndex >= 0, "existing Bundle 4/6 blocking proofs must remain wired");
  assert.ok(operationsIndex < lintIndex && operationsIndex < buildIndex, "operations tests must run before lint/build");
  assert.ok(browserIndex < lintIndex && browserIndex < buildIndex, "browser tests must run before lint/build");
  assert.doesNotMatch(webJob, /continue-on-error|\|\|\s*true/u);
}

export async function main() {
  assertOperationsSuiteInventory(requiredOperationSuiteFiles);
  let summary: OperationsSuiteSummary | undefined;
  const tests = run({
    cwd: webRoot,
    files: requiredOperationSuiteFiles.map((file) => resolve(webRoot, file)),
    concurrency: false,
    forceExit: true,
    timeout: 360_000,
    execArgv: ["--no-warnings"],
  });
  tests.on("test:summary", (result) => {
    summary = { success: result.success, counts: { ...result.counts } };
  });
  const report = tests.compose(spec);
  report.pipe(process.stdout, { end: false });
  await once(report, "end");
  assertOperationsExecution(summary);
}

export function isDirectExecution(moduleURL: string, entryPath: string | undefined) {
  return entryPath !== undefined && pathToFileURL(resolve(entryPath)).href === moduleURL;
}

if (isDirectExecution(import.meta.url, process.argv[1])) await main();
