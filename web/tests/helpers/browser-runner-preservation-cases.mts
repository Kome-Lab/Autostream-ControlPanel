import assert from "node:assert/strict";
import { EventEmitter } from "node:events";
import { existsSync, mkdirSync, readFileSync, writeFileSync } from "node:fs";
import { join } from "node:path";
import test from "node:test";
import { RunnerSignalError, runnerOutputDirectoryNames, withPreservedNextBuildDirectory, withPreservedRunnerOutputDirectories, withRunnerSignalAbort } from "./run-ui-foundation-browser.mts";
import { temporaryWebRoot, createOriginalNextFixture, nextFixtureFingerprint, runnerBackupDirectories, runnerArtifactBackupDirectories, runnerNextPreservationIssues } from "./browser-runner-preservation-fixture.mts";
import { browserRunnerPath } from "./browser-lifecycle-source-paths.mts";
import { replaceExactlyOnce } from "./browser-native-input-oracle.mts";


export function registerRunnerPreservationCases() {


test("runner .next helper restores an existing tree exactly after success", async (t) => {
  const fakeWebRoot = temporaryWebRoot(t);
  const nextBuildDirectory = join(fakeWebRoot, ".next");
  createOriginalNextFixture(nextBuildDirectory);
  const before = nextFixtureFingerprint(nextBuildDirectory);

  const result = await withPreservedNextBuildDirectory(fakeWebRoot, async () => {
    assert.equal(existsSync(nextBuildDirectory), false, "pre-existing .next was not isolated before callback");
    mkdirSync(join(nextBuildDirectory, "fresh"), { recursive: true });
    writeFileSync(join(nextBuildDirectory, "fresh", "runner-only.bin"), Buffer.from([9, 8, 7]));
    return "completed";
  });

  assert.equal(result, "completed");
  assert.deepEqual(nextFixtureFingerprint(nextBuildDirectory), before);
  assert.equal(existsSync(join(nextBuildDirectory, "fresh", "runner-only.bin")), false);
  assert.deepEqual(runnerBackupDirectories(fakeWebRoot), []);
});

test("runner .next helper restores an existing tree and original error after callback failure", async (t) => {
  const fakeWebRoot = temporaryWebRoot(t);
  const nextBuildDirectory = join(fakeWebRoot, ".next");
  createOriginalNextFixture(nextBuildDirectory);
  const before = nextFixtureFingerprint(nextBuildDirectory);
  const callbackError = new Error("fixture callback failed");

  await assert.rejects(
    withPreservedNextBuildDirectory(fakeWebRoot, async () => {
      assert.equal(existsSync(nextBuildDirectory), false, "pre-existing .next was not isolated before failing callback");
      mkdirSync(nextBuildDirectory);
      writeFileSync(join(nextBuildDirectory, "runner-only.txt"), "fresh");
      throw callbackError;
    }),
    (error) => error === callbackError,
  );

  assert.deepEqual(nextFixtureFingerprint(nextBuildDirectory), before);
  assert.equal(existsSync(join(nextBuildDirectory, "runner-only.txt")), false);
  assert.deepEqual(runnerBackupDirectories(fakeWebRoot), []);
});

test("runner .next helper removes a fresh tree when no original exists on success and failure", async (t) => {
  for (const shouldThrow of [false, true]) {
    const fakeWebRoot = temporaryWebRoot(t);
    const nextBuildDirectory = join(fakeWebRoot, ".next");
    const callbackError = new Error(`absent callback failure ${shouldThrow}`);
    const execution = withPreservedNextBuildDirectory(fakeWebRoot, async () => {
      mkdirSync(join(nextBuildDirectory, "fresh"), { recursive: true });
      writeFileSync(join(nextBuildDirectory, "fresh", "runner-only.txt"), "fresh");
      if (shouldThrow) throw callbackError;
      return "completed";
    });
    if (shouldThrow) await assert.rejects(execution, (error) => error === callbackError);
    else assert.equal(await execution, "completed");
    assert.equal(existsSync(nextBuildDirectory), false, `absent .next remained after shouldThrow=${shouldThrow}`);
    assert.deepEqual(runnerBackupDirectories(fakeWebRoot), []);
  }
});

test("runner .next helper rejects an outside cleanup target without touching unrelated data", async (t) => {
  const fakeWebRoot = temporaryWebRoot(t);
  const unrelatedRoot = temporaryWebRoot(t);
  const unrelatedNext = join(unrelatedRoot, ".next");
  const markerPath = join(unrelatedRoot, "UNRELATED-MARKER.txt");
  mkdirSync(unrelatedNext);
  writeFileSync(markerPath, "unchanged");
  let callbackCalls = 0;

  await assert.rejects(
    withPreservedNextBuildDirectory(
      fakeWebRoot,
      async () => { callbackCalls += 1; },
      unrelatedNext,
    ),
    /Refusing unexpected Next build isolation target/,
  );
  assert.equal(callbackCalls, 0);
  assert.equal(readFileSync(markerPath, "utf8"), "unchanged");
  assert.equal(existsSync(unrelatedNext), true);
});

test("runner artifact helper preserves every pre-existing output and removes fresh outputs on success and failure", async (t) => {
  for (const shouldThrow of [false, true]) {
    const fakeWebRoot = temporaryWebRoot(t);
    const before = new Map<string, ReturnType<typeof nextFixtureFingerprint>>();
    for (const name of runnerOutputDirectoryNames) {
      const outputDirectory = join(fakeWebRoot, name);
      createOriginalNextFixture(outputDirectory);
      before.set(name, nextFixtureFingerprint(outputDirectory));
    }
    const callbackError = new Error(`artifact callback failure ${shouldThrow}`);
    const execution = withPreservedRunnerOutputDirectories(fakeWebRoot, async () => {
      for (const name of runnerOutputDirectoryNames) {
        const outputDirectory = join(fakeWebRoot, name);
        assert.equal(existsSync(outputDirectory), false, `pre-existing ${name} was not isolated`);
        mkdirSync(outputDirectory, { recursive: true });
        writeFileSync(join(outputDirectory, "runner-only.txt"), "fresh");
      }
      if (shouldThrow) throw callbackError;
      return "completed";
    });
    if (shouldThrow) await assert.rejects(execution, (error) => error === callbackError);
    else assert.equal(await execution, "completed");
    for (const name of runnerOutputDirectoryNames) {
      assert.deepEqual(nextFixtureFingerprint(join(fakeWebRoot, name)), before.get(name), `${name} was not byte-preserved`);
      assert.equal(existsSync(join(fakeWebRoot, name, "runner-only.txt")), false, `${name} retained runner output`);
    }
    assert.deepEqual(runnerArtifactBackupDirectories(fakeWebRoot), []);
  }
});

test("runner signal abort restores pre-existing artifacts and removes signal listeners", async (t) => {
  const fakeWebRoot = temporaryWebRoot(t);
  const tracesDirectory = join(fakeWebRoot, "traces");
  createOriginalNextFixture(tracesDirectory);
  const before = nextFixtureFingerprint(tracesDirectory);
  const signalTarget = new EventEmitter();

  await assert.rejects(
    withRunnerSignalAbort(
      (signal) => withPreservedRunnerOutputDirectories(fakeWebRoot, async () => {
        mkdirSync(tracesDirectory, { recursive: true });
        writeFileSync(join(tracesDirectory, "runner-only.txt"), "fresh");
        queueMicrotask(() => signalTarget.emit("SIGTERM"));
        await new Promise<void>((_resolve, reject) => {
          signal.addEventListener("abort", () => reject(signal.reason), { once: true });
        });
      }),
      signalTarget,
    ),
    (error) => error instanceof RunnerSignalError && error.signal === "SIGTERM" && error.exitCode === 143,
  );

  assert.deepEqual(nextFixtureFingerprint(tracesDirectory), before);
  assert.equal(signalTarget.listenerCount("SIGINT"), 0);
  assert.equal(signalTarget.listenerCount("SIGTERM"), 0);
  assert.deepEqual(runnerArtifactBackupDirectories(fakeWebRoot), []);
});

test("runner .next source oracle rejects omitted restore and callback-error-only restore mutants", () => {
  const source = readFileSync(browserRunnerPath, "utf8").replace(/\r\n/g, "\n");
  assert.deepEqual(runnerNextPreservationIssues(source), []);
  const restoreLine = "        renameSync(backupNextBuildDirectory, nextBuildDirectory);\n";
  const finallyStart = [
    "  } finally {",
    "    try {",
    "      removeFreshNextBuildDirectory(resolvedWebRoot, nextBuildDirectory);",
  ].join("\n");
  const conditionalStart = [
    "  }",
    "  if (callbackOutcome?.ok === true) {",
    "    try {",
    "      removeFreshNextBuildDirectory(resolvedWebRoot, nextBuildDirectory);",
  ].join("\n");
  const mutants = [
    { name: "restore omitted", source: replaceExactlyOnce(source, restoreLine, "") },
    { name: "callback throw skips restore", source: replaceExactlyOnce(source, finallyStart, conditionalStart) },
  ];
  for (const mutant of mutants) {
    assert.notDeepEqual(runnerNextPreservationIssues(mutant.source), [], `${mutant.name} mutant was accepted`);
  }
});
}
