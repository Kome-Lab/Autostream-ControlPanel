import assert from "node:assert/strict";
import { once } from "node:events";
import { resolve } from "node:path";
import { run } from "node:test";
import { spec } from "node:test/reporters";
import { fileURLToPath } from "node:url";

import { captureNextGeneratedFiles, nextGeneratedFilesMatch, restoreNextGeneratedFiles } from "./browser-harness.mts";
import { assertBrowserSuiteExecution, type BrowserSuiteSummary, type CompletedBrowserScenario } from "./ui-foundation-assertions.mts";
import { isDirectExecution, RunnerSignalError, withPreservedNextBuildDirectory, withPreservedRunnerOutputDirectories, withRunnerSignalAbort } from "./run-ui-foundation-browser.mts";
import { stPortGetBrowserScenarioNames } from "./st-port-get-browser-harness.mts";

const webRoot = fileURLToPath(new URL("../..", import.meta.url));
const testFile = "tests/st-port-get-browser.test.mts";
const expectedPath = resolve(webRoot, testFile);

export async function main() {
  let summary: BrowserSuiteSummary | undefined;
  const completed: CompletedBrowserScenario[] = [];
  await withRunnerSignalAbort(async (signal) => {
    await withPreservedRunnerOutputDirectories(webRoot, async () => {
      await withPreservedNextBuildDirectory(webRoot, async () => {
        const generatedFiles = captureNextGeneratedFiles(webRoot);
        try {
          const tests = run({ cwd: webRoot, files: [expectedPath], concurrency: false, forceExit: true, signal, timeout: 360_000, execArgv: ["--no-warnings"] });
          tests.on("test:complete", (result) => {
            const wrapper = result.nesting === 0 && typeof result.file === "string" && resolve(result.file) === expectedPath
              && (result.name.replaceAll("\\", "/") === testFile || resolve(result.name) === expectedPath);
            if (wrapper) return;
            completed.push({ name: result.name, passed: result.details.passed, skipped: Boolean(result.skip), todo: Boolean(result.todo) });
          });
          tests.on("test:summary", (result) => { summary = { success: result.success, counts: { ...result.counts } }; });
          const report = tests.compose(spec);
          report.pipe(process.stdout, { end: false });
          await once(report, "end");
        } finally {
          restoreNextGeneratedFiles(generatedFiles);
        }
        assert.equal(nextGeneratedFilesMatch(generatedFiles), true, "ST-PORT browser must restore Next-generated source files");
      });
    });
  });
  assertBrowserSuiteExecution(summary, completed, stPortGetBrowserScenarioNames);
  assert.equal(completed.length, stPortGetBrowserScenarioNames.length, "ST-PORT browser completion inventory must be exact");
  for (const name of stPortGetBrowserScenarioNames) {
    assert.equal(completed.filter((entry) => entry.name === name).length, 1, "ST-PORT browser required scenario must complete exactly once");
  }
  assert.equal(summary?.counts.tests, 4, "ST-PORT browser requires one parent and all three accepted-result scenarios");
  assert.equal(summary?.counts.passed, 4, "ST-PORT browser requires all scenarios to pass");
  process.stdout.write(`ST_PORT_GET_BROWSER_SUMMARY ${JSON.stringify({ schema_version: 1, parent_pass: 1, child_pass: 3, fail: 0, skip: 0, scenario_ids: ["B1_local_only", "B2_unchanged", "B3_rolled_back"] })}\n`);
}

if (isDirectExecution(import.meta.url, process.argv[1])) {
  try {
    await main();
  } catch (error) {
    if (error instanceof RunnerSignalError) {
      process.exitCode = error.exitCode;
      process.stderr.write(`ST-PORT browser runner received ${error.signal}\n`);
    } else {
      throw error;
    }
  }
}
