import assert from "node:assert/strict";
import { once } from "node:events";
import {
  lstatSync,
  mkdtempSync,
  renameSync,
  rmSync,
  rmdirSync,
} from "node:fs";
import { dirname, resolve } from "node:path";
import process from "node:process";
import { run } from "node:test";
import { spec } from "node:test/reporters";
import { fileURLToPath, pathToFileURL } from "node:url";

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

export const EXPECTED_UI_FOUNDATION_BROWSER_TESTS = 26;

export const requiredScenarioNames = Object.freeze([
  "query states distinguish loading, empty, unhealthy, error, stale, recovery, and update variants",
  "logout clears protected UI and sends exactly one mutation",
  "session expiry keeps a validated same-origin return URL without a redirect loop",
  authMeExpiryScenarioName,
  "stale refresh completion does not replace a newer authenticated session",
  "session guard ignores setup completion after unmount",
  "login rejects external return URL variants",
  "Streams start-readiness follows streams.start at render and confirm time",
  "Worker restart uses fresh canonical action policy and one POST per worker",
  "Workers Configuration uses the server ANY permission and safe remote state",
  "desktop/mobile navigation parity, active route, and permission visibility are runtime-enforced",
  "same-route and cross-route mobile create release the navigation focus owner",
  "reduced motion removes Sheet animation while preserving close and focus",
  "locale and theme controls preserve route/session and expose translated accessible names",
  "status focus remains visible in normal and forced-colors modes",
  "false-positive guards reject invalid observable outcomes",
] as const);

const webRoot = fileURLToPath(new URL("../..", import.meta.url));
const browserTest = fileURLToPath(new URL("../ui-foundation-browser.test.mts", import.meta.url));

export function assertExactUIFoundationBrowserExecution(
  summary: BrowserSuiteSummary | undefined,
  completed: CompletedBrowserScenario[],
) {
  assertBrowserSuiteExecution(summary, completed, requiredScenarioNames);
  assert.ok(summary, "browser test runner did not report a summary");
  assert.equal(
    summary.counts.tests,
    EXPECTED_UI_FOUNDATION_BROWSER_TESTS,
    `browser suite must execute exactly ${EXPECTED_UI_FOUNDATION_BROWSER_TESTS} tests`,
  );
  assert.equal(
    summary.counts.passed,
    EXPECTED_UI_FOUNDATION_BROWSER_TESTS,
    `browser suite must pass exactly ${EXPECTED_UI_FOUNDATION_BROWSER_TESTS} tests`,
  );
  for (const requiredName of requiredScenarioNames) {
    const occurrences = completed.filter((candidate) => candidate.name === requiredName);
    assert.equal(occurrences.length, 1, `required browser scenario must run exactly once: ${requiredName}`);
  }
}

export function resolveRunnerNextBuildDirectory(
  candidateWebRoot: string,
  candidateNextBuildDirectory = resolve(candidateWebRoot, ".next"),
) {
  const resolvedWebRoot = resolve(candidateWebRoot);
  const exactNextBuildDirectory = resolve(resolvedWebRoot, ".next");
  const resolvedCandidate = resolve(candidateNextBuildDirectory);
  if (resolvedCandidate !== exactNextBuildDirectory || dirname(resolvedCandidate) !== resolvedWebRoot) {
    throw new Error("Refusing unexpected Next build isolation target");
  }
  return exactNextBuildDirectory;
}

export async function withPreservedNextBuildDirectory<T>(
  candidateWebRoot: string,
  callback: () => Promise<T>,
  candidateNextBuildDirectory?: string,
): Promise<T> {
  const resolvedWebRoot = resolve(candidateWebRoot);
  const nextBuildDirectory = resolveRunnerNextBuildDirectory(resolvedWebRoot, candidateNextBuildDirectory);
  const hadPreExistingNextBuildDirectory = pathEntryExists(nextBuildDirectory);
  let backupDirectory: string | undefined;
  let backupNextBuildDirectory: string | undefined;

  if (hadPreExistingNextBuildDirectory) {
    backupDirectory = mkdtempSync(resolve(resolvedWebRoot, ".ui-foundation-browser-next-backup-"));
    backupNextBuildDirectory = resolve(backupDirectory, ".next");
    try {
      renameSync(nextBuildDirectory, backupNextBuildDirectory);
    } catch (error) {
      if (!pathEntryExists(backupNextBuildDirectory)) rmdirSync(backupDirectory);
      throw error;
    }
  }

  let callbackOutcome: Readonly<{ ok: true; value: T }> | Readonly<{ ok: false; error: unknown }> | undefined;
  let restorationError: unknown;
  try {
    callbackOutcome = { ok: true, value: await callback() };
  } catch (error) {
    callbackOutcome = { ok: false, error };
  } finally {
    try {
      removeFreshNextBuildDirectory(resolvedWebRoot, nextBuildDirectory);
      if (backupDirectory && backupNextBuildDirectory) {
        renameSync(backupNextBuildDirectory, nextBuildDirectory);
        rmdirSync(backupDirectory);
      }
    } catch (error) {
      restorationError = error;
    }
  }

  if (callbackOutcome?.ok === false && restorationError !== undefined) {
    throw new AggregateError(
      [callbackOutcome.error, restorationError],
      "UI Foundation browser execution and .next restoration both failed",
    );
  }
  if (restorationError !== undefined) throw restorationError;
  if (callbackOutcome?.ok === false) throw callbackOutcome.error;
  if (!callbackOutcome) throw new Error("UI Foundation browser callback did not settle");
  return callbackOutcome.value;
}

export async function main() {
  let summary: BrowserSuiteSummary | undefined;
  const completed: CompletedBrowserScenario[] = [];

  await withPreservedNextBuildDirectory(webRoot, async () => {
    const generatedFiles = captureNextGeneratedFiles(webRoot);
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
    }
    assert.equal(nextGeneratedFilesMatch(generatedFiles), true, "browser suite must restore Next-generated source files");
  });

  assertExactUIFoundationBrowserExecution(summary, completed);
}

export function isDirectExecution(moduleURL: string, entryPath: string | undefined) {
  return entryPath !== undefined && pathToFileURL(resolve(entryPath)).href === moduleURL;
}

if (isDirectExecution(import.meta.url, process.argv[1])) await main();

function pathEntryExists(path: string) {
  try {
    lstatSync(path);
    return true;
  } catch (error) {
    if (isMissingPathError(error)) return false;
    throw error;
  }
}

function removeFreshNextBuildDirectory(resolvedWebRoot: string, nextBuildDirectory: string) {
  const exactNextBuildDirectory = resolveRunnerNextBuildDirectory(resolvedWebRoot, nextBuildDirectory);
  if (!pathEntryExists(exactNextBuildDirectory)) return;
  rmSync(exactNextBuildDirectory, { recursive: true, force: true, maxRetries: 5, retryDelay: 100 });
}

function isMissingPathError(error: unknown): error is NodeJS.ErrnoException {
  return error instanceof Error && "code" in error && error.code === "ENOENT";
}
