import assert from "node:assert/strict";
import { once } from "node:events";
import { existsSync, rmSync } from "node:fs";
import { dirname, resolve } from "node:path";
import process from "node:process";
import { run } from "node:test";
import { spec } from "node:test/reporters";
import { fileURLToPath } from "node:url";

import {
  captureNextGeneratedFiles,
  nextGeneratedFilesMatch,
  restoreNextGeneratedFiles,
} from "./browser-harness.mts";
import {
  assertBrowserSuiteExecution,
  authMeExpiryScenarioName,
  type BrowserSuiteSummary,
  type CompletedBrowserScenario,
} from "./ui-foundation-assertions.mts";

const webRoot = fileURLToPath(new URL("../..", import.meta.url));
const browserTest = fileURLToPath(new URL("../ui-foundation-browser.test.mts", import.meta.url));
const generatedFiles = captureNextGeneratedFiles(webRoot);
const nextBuildDirectory = resolve(webRoot, ".next");
const nextBuildDirectoryExisted = existsSync(nextBuildDirectory);

const requiredScenarioNames = [
  "query states distinguish loading, empty, unhealthy, error, stale, recovery, and update variants",
  "logout clears protected UI and sends exactly one mutation",
  "session expiry keeps a validated same-origin return URL without a redirect loop",
  authMeExpiryScenarioName,
  "stale refresh completion does not replace a newer authenticated session",
  "session guard ignores setup completion after unmount",
  "login rejects external return URL variants",
  "Streams start-readiness follows streams.start at render and confirm time",
  "desktop/mobile navigation parity, active route, and permission visibility are runtime-enforced",
  "same-route and cross-route mobile create release the navigation focus owner",
  "reduced motion removes Sheet animation while preserving close and focus",
  "locale and theme controls preserve route/session and expose translated accessible names",
  "status focus remains visible in normal and forced-colors modes",
  "false-positive guards reject invalid observable outcomes",
] as const;

let summary: BrowserSuiteSummary | undefined;
const completed: CompletedBrowserScenario[] = [];

try {
  const tests = run({
    cwd: webRoot,
    files: [browserTest],
    concurrency: false,
    forceExit: true,
    timeout: 360_000,
    execArgv: ["--no-warnings"],
  });
  tests.on("test:complete", (result) => {
    completed.push({
      name: result.name,
      passed: result.details.passed,
      skipped: Boolean(result.skip),
      todo: Boolean(result.todo),
    });
  });
  tests.on("test:summary", (result) => {
    summary = { success: result.success, counts: { ...result.counts } };
  });

  const report = tests.compose(spec);
  report.pipe(process.stdout, { end: false });
  await once(report, "end");
} finally {
  restoreNextGeneratedFiles(generatedFiles);
  removeSuiteOwnedNextBuildDirectory();
}

assert.equal(nextGeneratedFilesMatch(generatedFiles), true, "browser suite must restore Next-generated source files");
assert.equal(
  nextBuildDirectoryExisted || !existsSync(nextBuildDirectory),
  true,
  "browser suite must remove the Next build directory it created",
);
assertBrowserSuiteExecution(summary, completed, requiredScenarioNames);

function removeSuiteOwnedNextBuildDirectory() {
  if (nextBuildDirectoryExisted) return;
  if (dirname(nextBuildDirectory) !== resolve(webRoot)) {
    throw new Error(`Refusing unexpected Next build cleanup target: ${nextBuildDirectory}`);
  }
  rmSync(nextBuildDirectory, { recursive: true, force: true, maxRetries: 5, retryDelay: 100 });
}
