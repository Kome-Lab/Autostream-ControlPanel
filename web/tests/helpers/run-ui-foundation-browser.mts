import assert from "node:assert/strict";
import { once } from "node:events";
import {
  lstatSync,
  mkdtempSync,
  renameSync,
  rmSync,
  rmdirSync,
} from "node:fs";
import { dirname, isAbsolute, resolve } from "node:path";
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

export const EXPECTED_UI_FOUNDATION_BROWSER_TESTS = 35;

export const requiredBrowserTestFiles = Object.freeze([
  "tests/ui-foundation-browser.test.mts",
  "tests/ui-foundation-confirmation-browser.test.mts",
  "tests/ui-foundation-secrets-browser.test.mts",
] as const);

export const runnerOutputDirectoryNames = Object.freeze([
  "out",
  "coverage",
  "screenshots",
  "traces",
  "test-results",
  "playwright-report",
] as const);

export type RunnerSignalName = "SIGINT" | "SIGTERM";

type RunnerSignalTarget = Readonly<{
  once: (signal: RunnerSignalName, listener: () => void) => unknown;
  removeListener: (signal: RunnerSignalName, listener: () => void) => unknown;
}>;

export class RunnerSignalError extends Error {
  readonly exitCode: number;
  readonly signal: RunnerSignalName;

  constructor(signal: RunnerSignalName, cause?: unknown) {
    super(`UI Foundation browser runner received ${signal}`);
    this.name = "RunnerSignalError";
    this.signal = signal;
    this.exitCode = signal === "SIGINT" ? 130 : 143;
    if (cause !== undefined) Object.assign(this, { cause });
  }
}

export const requiredScenarioNames = Object.freeze([
  "UI Foundation runtime behavior",
  "query states distinguish loading, empty, unhealthy, error, stale, recovery, and update variants",
  "logout clears protected UI and sends exactly one mutation",
  "session expiry keeps a validated same-origin return URL without a redirect loop",
  authMeExpiryScenarioName,
  "stale refresh completion does not replace a newer authenticated session",
  "session guard ignores setup completion after unmount",
  "login rejects external return URL variants",
  "Streams start-readiness follows streams.start at render and confirm time",
  "handler guard structural oracle rejects in-memory regressions",
  "start only",
  "update only",
  "both",
  "neither",
  "wildcard",
  "permission changes before confirm",
  "backend 403 retains the existing action error mapping",
  "pending mutation keeps duplicate start-readiness blocked",
  "Worker restart uses fresh canonical action policy and one POST per worker",
  "Workers Configuration uses the server ANY permission and safe remote state",
  "desktop/mobile navigation parity, active route, and permission visibility are runtime-enforced",
  "same-route and cross-route mobile create release the navigation focus owner",
  "reduced motion removes Sheet animation while preserving close and focus",
  "locale and theme controls preserve route/session and expose translated accessible names",
  "Account appearance persists 12 themes and 3 modes with DB fallback and save rollback",
  "Stream detail presents visual snapshots and cover actions preserve request-count and applied-state boundaries",
  "Bundle 7 affected surfaces are responsive at every canonical width",
  "status focus remains visible in normal and forced-colors modes",
  "false-positive guards reject invalid observable outcomes",
  "high-risk and compatibility confirmations preserve browser focus, literal input, state, and intent boundaries",
  "conflict renders safe error and hides diagnostic metadata",
  "failed renders safe error without retry",
  "revalidation unavailable blocks confirmation and returns focus",
  "failure-state browser oracle rejects in-memory false-positive variants",
  "one-time secret browser boundary preserves controlled reveal, focus, cleanup, and leakage invariants",
] as const);

const webRoot = fileURLToPath(new URL("../..", import.meta.url));

export function assertExactBrowserTestFileInventory(candidateFiles: readonly string[]) {
  assert.equal(candidateFiles.length, requiredBrowserTestFiles.length, "browser runner test-file inventory count");
  assert.equal(new Set(candidateFiles).size, candidateFiles.length, "browser runner test-file inventory must not contain duplicates");
  assert.deepEqual([...candidateFiles], [...requiredBrowserTestFiles], "browser runner test-file inventory");
}

export function resolveRequiredBrowserTestPaths(candidateWebRoot: string, candidateFiles = requiredBrowserTestFiles) {
  assertExactBrowserTestFileInventory(candidateFiles);
  return candidateFiles.map((candidate) => {
    const testPath = resolve(candidateWebRoot, candidate);
    assert.equal(pathEntryExists(testPath), true, `required browser test file is missing: ${candidate}`);
    return testPath;
  });
}

export function isRequiredBrowserTestFileCompletion(
  result: Readonly<{ name: string; nesting?: number; file?: string }>,
  candidateWebRoot = webRoot,
) {
  const normalizedName = result.name.replaceAll("\\", "/");
  if (result.nesting !== 0 || typeof result.file !== "string") return false;
  return requiredBrowserTestFiles.some((candidate) => {
    const expectedPath = resolve(candidateWebRoot, candidate);
    const nameMatches = normalizedName === candidate
      || (isAbsolute(result.name) && resolve(result.name) === expectedPath);
    return nameMatches && resolve(result.file) === expectedPath;
  });
}

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
  assert.equal(
    completed.length,
    EXPECTED_UI_FOUNDATION_BROWSER_TESTS,
    `browser suite must complete exactly ${EXPECTED_UI_FOUNDATION_BROWSER_TESTS} scenarios`,
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

export async function withPreservedRunnerOutputDirectories<T>(
  candidateWebRoot: string,
  callback: () => Promise<T>,
): Promise<T> {
  const resolvedWebRoot = resolve(candidateWebRoot);
  const outputDirectories = runnerOutputDirectoryNames.map((name) => ({
    name,
    path: resolveRunnerOutputDirectory(resolvedWebRoot, name),
  }));
  const preExisting = outputDirectories.filter((entry) => pathEntryExists(entry.path));
  const backupDirectory = preExisting.length > 0
    ? mkdtempSync(resolve(resolvedWebRoot, ".ui-foundation-browser-artifacts-backup-"))
    : undefined;
  const moved: typeof outputDirectories = [];

  try {
    if (backupDirectory) {
      for (const entry of preExisting) {
        renameSync(entry.path, resolve(backupDirectory, entry.name));
        moved.push(entry);
      }
    }
  } catch (isolationError) {
    const restorationErrors = restoreRunnerOutputDirectories(resolvedWebRoot, outputDirectories, moved, backupDirectory, false);
    if (restorationErrors.length > 0) {
      throw new AggregateError([isolationError, ...restorationErrors], "Runner artifact isolation and restoration both failed");
    }
    throw isolationError;
  }

  let callbackOutcome: Readonly<{ ok: true; value: T }> | Readonly<{ ok: false; error: unknown }> | undefined;
  try {
    callbackOutcome = { ok: true, value: await callback() };
  } catch (error) {
    callbackOutcome = { ok: false, error };
  } finally {
    const restorationErrors = restoreRunnerOutputDirectories(resolvedWebRoot, outputDirectories, moved, backupDirectory, true);
    if (restorationErrors.length > 0) {
      if (callbackOutcome?.ok === false) {
        throw new AggregateError(
          [callbackOutcome.error, ...restorationErrors],
          "UI Foundation browser execution and runner artifact restoration both failed",
        );
      }
      throw new AggregateError(restorationErrors, "UI Foundation browser runner artifact restoration failed");
    }
  }

  if (callbackOutcome?.ok === false) throw callbackOutcome.error;
  if (!callbackOutcome) throw new Error("UI Foundation browser artifact callback did not settle");
  return callbackOutcome.value;
}

export async function withRunnerSignalAbort<T>(
  callback: (signal: AbortSignal) => Promise<T>,
  signalTarget: RunnerSignalTarget = process,
): Promise<T> {
  const controller = new AbortController();
  let receivedSignal: RunnerSignalName | undefined;
  const onSignal = (signal: RunnerSignalName) => () => {
    if (receivedSignal) return;
    receivedSignal = signal;
    controller.abort(new RunnerSignalError(signal));
  };
  const onSigint = onSignal("SIGINT");
  const onSigterm = onSignal("SIGTERM");
  signalTarget.once("SIGINT", onSigint);
  signalTarget.once("SIGTERM", onSigterm);

  let outcome: Readonly<{ ok: true; value: T }> | Readonly<{ ok: false; error: unknown }> | undefined;
  try {
    outcome = { ok: true, value: await callback(controller.signal) };
  } catch (error) {
    outcome = { ok: false, error };
  } finally {
    signalTarget.removeListener("SIGINT", onSigint);
    signalTarget.removeListener("SIGTERM", onSigterm);
  }

  if (receivedSignal) throw new RunnerSignalError(receivedSignal, outcome?.ok === false ? outcome.error : undefined);
  if (outcome?.ok === false) throw outcome.error;
  if (!outcome) throw new Error("UI Foundation browser signal callback did not settle");
  return outcome.value;
}

export async function main() {
  let summary: BrowserSuiteSummary | undefined;
  const completed: CompletedBrowserScenario[] = [];

  await withRunnerSignalAbort(async (signal) => {
    await withPreservedRunnerOutputDirectories(webRoot, async () => {
      await withPreservedNextBuildDirectory(webRoot, async () => {
        const generatedFiles = captureNextGeneratedFiles(webRoot);
        try {
          const tests = run({
            cwd: webRoot,
            files: resolveRequiredBrowserTestPaths(webRoot),
            concurrency: false,
            forceExit: true,
            signal,
            timeout: 450_000,
            execArgv: ["--no-warnings"],
          });
          tests.on("test:complete", (result) => {
            // Node 26 emits one additional completion for each input test file.
            // Exclude only the three exact, nesting-zero file wrappers; every
            // actual or unexpected scenario still participates in exact count.
            if (isRequiredBrowserTestFileCompletion(result)) return;
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
    });
  });

  assertExactUIFoundationBrowserExecution(summary, completed);
}

export function isDirectExecution(moduleURL: string, entryPath: string | undefined) {
  return entryPath !== undefined && pathToFileURL(resolve(entryPath)).href === moduleURL;
}

if (isDirectExecution(import.meta.url, process.argv[1])) {
  try {
    await main();
  } catch (error) {
    if (error instanceof RunnerSignalError) {
      process.exitCode = error.exitCode;
      process.stderr.write(`${error.message}\n`);
    } else {
      throw error;
    }
  }
}

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

function resolveRunnerOutputDirectory(resolvedWebRoot: string, name: (typeof runnerOutputDirectoryNames)[number]) {
  const outputDirectory = resolve(resolvedWebRoot, name);
  if (dirname(outputDirectory) !== resolvedWebRoot) throw new Error(`Refusing unexpected runner artifact target: ${name}`);
  return outputDirectory;
}

function restoreRunnerOutputDirectories(
  resolvedWebRoot: string,
  outputDirectories: readonly Readonly<{ name: (typeof runnerOutputDirectoryNames)[number]; path: string }>[],
  moved: readonly Readonly<{ name: (typeof runnerOutputDirectoryNames)[number]; path: string }>[],
  backupDirectory: string | undefined,
  removeFresh: boolean,
) {
  const restorationErrors: unknown[] = [];
  const movedNames = new Set(moved.map((entry) => entry.name));
  const entriesToRestore = removeFresh ? outputDirectories : moved;
  for (const entry of [...entriesToRestore].reverse()) {
    try {
      const exactOutputDirectory = resolveRunnerOutputDirectory(resolvedWebRoot, entry.name);
      assert.equal(entry.path, exactOutputDirectory, `runner artifact target changed: ${entry.name}`);
      if (removeFresh && pathEntryExists(exactOutputDirectory)) {
        rmSync(exactOutputDirectory, { recursive: true, force: true, maxRetries: 5, retryDelay: 100 });
      }
      if (!removeFresh) assert.equal(pathEntryExists(exactOutputDirectory), false, `runner artifact target was replaced during isolation: ${entry.name}`);
      if (backupDirectory && movedNames.has(entry.name)) {
        renameSync(resolve(backupDirectory, entry.name), exactOutputDirectory);
      }
    } catch (error) {
      restorationErrors.push(error);
    }
  }
  if (backupDirectory) {
    try {
      if (pathEntryExists(backupDirectory)) rmdirSync(backupDirectory);
    } catch (error) {
      restorationErrors.push(error);
    }
  }
  return restorationErrors;
}

function isMissingPathError(error: unknown): error is NodeJS.ErrnoException {
  return error instanceof Error && "code" in error && error.code === "ENOENT";
}
