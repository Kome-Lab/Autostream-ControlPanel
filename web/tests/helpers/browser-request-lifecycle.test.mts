import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import {
  existsSync,
  lstatSync,
  mkdirSync,
  mkdtempSync,
  readdirSync,
  readFileSync,
  readlinkSync,
  rmSync,
  writeFileSync,
} from "node:fs";
import { tmpdir } from "node:os";
import { dirname, join, relative, resolve, sep } from "node:path";
import test from "node:test";
import { fileURLToPath } from "node:url";

import ts from "typescript";

import { BrowserHarness, type BrowserNativeKey } from "./browser-harness.mts";
import {
  FetchRequestLifecycle,
  RejectableEventWaiters,
  invalidInterceptionIdMessage,
} from "./browser-request-lifecycle.mts";
import {
  EXPECTED_UI_FOUNDATION_BROWSER_TESTS,
  assertExactUIFoundationBrowserExecution,
  requiredScenarioNames,
  withPreservedNextBuildDirectory,
} from "./run-ui-foundation-browser.mts";

const actionPath = "/streams/fixture/start-readiness";
const helperRoot = dirname(fileURLToPath(import.meta.url));
const browserHarnessPath = join(helperRoot, "browser-harness.mts");
const uiBrowserTestPath = join(helperRoot, "..", "ui-foundation-browser.test.mts");
const browserRunnerPath = join(helperRoot, "run-ui-foundation-browser.mts");

test("UI browser runner import is inert and its exact 26-test inventory accepts only the complete fixture", () => {
  assert.equal(EXPECTED_UI_FOUNDATION_BROWSER_TESTS, 26);
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
      name: "tests=25",
      summary: browserSummary({ tests: 25, passed: 25 }),
      completed: passing.completed.slice(0, 25),
    },
    {
      name: "tests=27",
      summary: browserSummary({ tests: 27, passed: 27 }),
      completed: [...passing.completed, passingScenario("independent extra leaf")],
    },
    {
      name: "required absent",
      summary: passing.summary,
      completed: passing.completed.map((scenario) =>
        scenario.name === firstRequiredName ? passingScenario("replacement non-required leaf") : scenario),
    },
    {
      name: "required duplicated while total remains 26",
      summary: passing.summary,
      completed: passing.completed.map((scenario, index) =>
        index === passing.completed.length - 1 ? passingScenario(firstRequiredName) : scenario),
    },
    {
      name: "failure=1",
      summary: { ...passing.summary, success: false, counts: { ...passing.summary.counts, passed: 25 } },
      completed: passing.completed.map((scenario, index) => index === 0 ? { ...scenario, passed: false } : scenario),
    },
    {
      name: "skipped=1",
      summary: browserSummary({ passed: 25, skipped: 1 }),
      completed: passing.completed.map((scenario, index) => index === 0 ? { ...scenario, passed: false, skipped: true } : scenario),
    },
    {
      name: "todo=1",
      summary: browserSummary({ passed: 25, todo: 1 }),
      completed: passing.completed.map((scenario, index) => index === 0 ? { ...scenario, passed: false, todo: true } : scenario),
    },
    {
      name: "cancelled=1",
      summary: browserSummary({ passed: 25, cancelled: 1 }),
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

test("Worker restart focus AST oracle accepts production and complete unconditional, switch and if-else paths", () => {
  const production = readFileSync(uiBrowserTestPath, "utf8").replace(/\r\n/g, "\n");
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
  const production = readFileSync(uiBrowserTestPath, "utf8").replace(/\r\n/g, "\n");
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

test("known stale interception cancelled by a newer navigation is nonfatal", () => {
  const lifecycle = new FetchRequestLifecycle();
  lifecycle.register({ requestId: "stale-1", method: "GET", pathname: "/old-document", requiredResponse: false });
  const attempt = lifecycle.beginSettlement("stale-1", "Fetch.continueRequest");
  lifecycle.beginNavigation("navigate");

  assert.deepEqual(lifecycle.handleSettlementError(attempt, new Error(invalidInterceptionIdMessage)), { cancelled: true });
  lifecycle.assertHealthy();
  assert.equal(lifecycle.activeCount, 0);
  assert.equal(lifecycle.safeCancellationCount, 1);
});

test("required current interception cancellation fails the scenario", () => {
  const lifecycle = new FetchRequestLifecycle();
  lifecycle.register({ requestId: "required-1", method: "POST", pathname: actionPath, requiredResponse: true });
  const attempt = lifecycle.beginSettlement("required-1", "Fetch.fulfillRequest");

  assert.throws(
    () => lifecycle.handleSettlementError(attempt, new Error(invalidInterceptionIdMessage)),
    /Invalid InterceptionId\./,
  );
});

test("required stale interception cancellation remains fatal", () => {
  const lifecycle = new FetchRequestLifecycle();
  lifecycle.register({ requestId: "required-stale-1", method: "POST", pathname: actionPath, requiredResponse: true });
  const attempt = lifecycle.beginSettlement("required-stale-1", "Fetch.fulfillRequest");
  lifecycle.beginNavigation("navigate");

  assert.throws(
    () => lifecycle.handleSettlementError(attempt, new Error(invalidInterceptionIdMessage)),
    /Invalid InterceptionId\./,
  );
});

test("known stale non-required fulfill cancellation is nonfatal", () => {
  const lifecycle = new FetchRequestLifecycle();
  lifecycle.register({ requestId: "optional-stale-1", method: "GET", pathname: "/optional", requiredResponse: false });
  const attempt = lifecycle.beginSettlement("optional-stale-1", "Fetch.fulfillRequest");
  lifecycle.beginNavigation("reload");

  assert.deepEqual(lifecycle.handleSettlementError(attempt, new Error(invalidInterceptionIdMessage)), { cancelled: true });
  lifecycle.assertHealthy();
  assert.equal(lifecycle.activeCount, 0);
});

test("unknown request IDs and duplicate settlement attempts are fatal", () => {
  const lifecycle = new FetchRequestLifecycle();
  assert.throws(
    () => lifecycle.beginSettlement("missing", "Fetch.continueRequest"),
    /Unknown Fetch request ID: missing/,
  );

  lifecycle.register({ requestId: "duplicate-1", method: "GET", pathname: "/fixture", requiredResponse: false });
  lifecycle.beginSettlement("duplicate-1", "Fetch.continueRequest");
  assert.throws(
    () => lifecycle.beginSettlement("duplicate-1", "Fetch.continueRequest"),
    /Duplicate Fetch settlement attempt: duplicate-1/,
  );
  assert.throws(
    () => lifecycle.register({ requestId: "duplicate-1", method: "GET", pathname: "/fixture", requiredResponse: false }),
    /Duplicate Fetch request ID: duplicate-1/,
  );
});

test("non-Invalid-InterceptionId CDP errors remain fatal", () => {
  const lifecycle = new FetchRequestLifecycle();
  lifecycle.register({ requestId: "cdp-1", method: "GET", pathname: "/fixture", requiredResponse: false });
  const attempt = lifecycle.beginSettlement("cdp-1", "Fetch.continueRequest");
  lifecycle.beginNavigation("reload");
  assert.throws(() => lifecycle.handleSettlementError(attempt, new Error("Target closed")), /Target closed/);
});

test("malformed Invalid InterceptionId errors remain fatal", () => {
  const lifecycle = new FetchRequestLifecycle();
  lifecycle.register({ requestId: "malformed-1", method: "GET", pathname: "/fixture", requiredResponse: false });
  const attempt = lifecycle.beginSettlement("malformed-1", "Fetch.continueRequest");
  lifecycle.beginNavigation("navigate");
  assert.throws(
    () => lifecycle.handleSettlementError(attempt, new Error("Invalid InterceptionId")),
    /^Error: Invalid InterceptionId$/,
  );
});

test("Page.loadEventFired waiter rejects immediately with the fatal cause", async () => {
  const waiters = new RejectableEventWaiters();
  const fatal = new Error("fatal fetch failure");
  const waiter = waiters.wait("Page.loadEventFired", 1_000, "load timeout");
  waiters.rejectAll(fatal);

  const outcome = await settlePromptly(waiter.promise);
  if (outcome === "pending") waiter.cancel(new Error("Red fixture cleanup"));
  assert.notEqual(outcome, "pending", "fatal waiter stayed pending until its timeout");
  assert.equal(outcome, fatal);
  assert.equal(waiters.pendingCount, 0);
});

test("Page.loadEventFired waiter rejects immediately on socket close", async () => {
  const waiters = new RejectableEventWaiters();
  const closed = new Error("Browser CDP connection closed");
  const waiter = waiters.wait("Page.loadEventFired", 1_000, "load timeout");
  waiters.rejectAll(closed);

  const outcome = await settlePromptly(waiter.promise);
  if (outcome === "pending") waiter.cancel(new Error("Red fixture cleanup"));
  assert.notEqual(outcome, "pending", "socket-close waiter stayed pending until its timeout");
  assert.equal(outcome, closed);
  assert.equal(waiters.pendingCount, 0);
});

test("successful event resolves once and removes its waiter", async () => {
  const waiters = new RejectableEventWaiters();
  const waiter = waiters.wait("Page.loadEventFired", 1_000, "load timeout");
  waiters.resolve("Page.loadEventFired", { frameId: "main" });
  waiters.resolve("Page.loadEventFired", { frameId: "duplicate" });
  assert.deepEqual(await waiter.promise, { frameId: "main" });
  assert.equal(waiters.pendingCount, 0);
});

test("event timeout removes its waiter", async () => {
  const waiters = new RejectableEventWaiters();
  await assert.rejects(waiters.wait("Page.loadEventFired", 5, "bounded load timeout").promise, /bounded load timeout/);
  assert.equal(waiters.pendingCount, 0);
});

test("action-path settlement wait ignores background polling", async () => {
  const lifecycle = new FetchRequestLifecycle();
  lifecycle.register({ requestId: "poll-1", method: "GET", pathname: "/service-health", requiredResponse: false });
  lifecycle.register({ requestId: "action-1", method: "POST", pathname: actionPath, requiredResponse: true });
  const actionAttempt = lifecycle.beginSettlement("action-1", "Fetch.fulfillRequest");
  const actionIdle = lifecycle.waitForIdle({ pathname: actionPath, method: "POST", timeoutMs: 100 });

  lifecycle.completeSettlement(actionAttempt);
  await actionIdle;
  assert.equal(lifecycle.activeCount, 1, "background polling must not block the focused action wait");
  lifecycle.close();
  assert.equal(lifecycle.activeCount, 0);
});

test("pending deferred request is not idle and becomes idle only after settlement", async () => {
  const lifecycle = new FetchRequestLifecycle();
  lifecycle.register({ requestId: "deferred-1", method: "POST", pathname: actionPath, requiredResponse: true });
  const attempt = lifecycle.beginSettlement("deferred-1", "Fetch.fulfillRequest");
  let settled = false;
  const idle = lifecycle.waitForIdle({ pathname: actionPath, method: "POST", timeoutMs: 100 }).then(() => { settled = true; });
  await Promise.resolve();
  assert.equal(settled, false, "pending deferred request was reported idle");

  lifecycle.completeSettlement(attempt);
  await idle;
  assert.equal(settled, true);
  assert.equal(lifecycle.activeCount, 0);
});

test("settlement timeout fails with bounded diagnostics", async () => {
  const lifecycle = new FetchRequestLifecycle();
  for (let index = 0; index < 8; index += 1) {
    lifecycle.register({ requestId: `diagnostic-${index}`, method: "POST", pathname: actionPath, requiredResponse: true });
  }
  await assert.rejects(
    lifecycle.waitForIdle({ pathname: actionPath, method: "POST", timeoutMs: 5 }),
    (error: Error) => {
      assert.match(error.message, /Timed out waiting for request handlers to settle/);
      assert.match(error.message, /"activeCount":8/);
      assert.match(error.message, /"omitted":3/);
      assert.doesNotMatch(error.message, /diagnostic-5/);
      return true;
    },
  );
  lifecycle.close();
  assert.equal(lifecycle.activeCount, 0);
});

test("fatal error rejects a pending settlement wait with the original cause", async () => {
  const lifecycle = new FetchRequestLifecycle();
  lifecycle.register({ requestId: "fatal-1", method: "POST", pathname: actionPath, requiredResponse: true });
  const idle = lifecycle.waitForIdle({ pathname: actionPath, timeoutMs: 1_000 });
  const fatal = new Error("fatal request handler");
  lifecycle.fail(fatal);
  await assert.rejects(idle, (error) => error === fatal);
  assert.throws(() => lifecycle.assertHealthy(), (error) => error === fatal);
  lifecycle.close();
});

for (const expected of [
  {
    name: "Enter",
    key: "Enter",
    keyValue: "Enter",
    code: "Enter",
    virtualKeyCode: 13,
    text: "\r",
  },
  {
    name: "Space",
    key: "Space",
    keyValue: " ",
    code: "Space",
    virtualKeyCode: 32,
    text: " ",
  },
  {
    name: "Escape",
    key: "Escape",
    keyValue: "Escape",
    code: "Escape",
    virtualKeyCode: 27,
  },
] as const satisfies ReadonlyArray<{
  name: string;
  key: BrowserNativeKey;
  keyValue: string;
  code: string;
  virtualKeyCode: number;
  text?: string;
}>) {
  test(`BrowserHarness pressNativeKey dispatches ordered ${expected.name} key-down and key-up`, async (t) => {
    const { harness, socket } = createHarnessFixture();
    t.after(() => harness.close());

    await harness.pressNativeKey(expected.key);

    const commands = socket.commandsFor("Input.dispatchKeyEvent");
    const base = {
      key: expected.keyValue,
      code: expected.code,
      windowsVirtualKeyCode: expected.virtualKeyCode,
      nativeVirtualKeyCode: expected.virtualKeyCode,
    };
    assert.deepEqual(commands.map((command) => command.params), [
      {
        ...base,
        ...(expected.text === undefined ? {} : { text: expected.text, unmodifiedText: expected.text }),
        type: "keyDown",
      },
      { ...base, type: "keyUp" },
    ]);
    assert.deepEqual(commands.map((command) => command.sessionId), ["test-session", "test-session"]);
  });
}

test("BrowserHarness pressNativeKey propagates socket fatal errors without swallowing them", async (t) => {
  const { harness, socket } = createHarnessFixture();
  t.after(() => harness.close());
  socket.hold("Input.dispatchKeyEvent");

  const input = harness.pressNativeKey("Enter");
  await socket.waitForCommand("Input.dispatchKeyEvent");
  socket.close();

  await assert.rejects(input, /Browser CDP connection closed/);
  await assert.rejects(harness.pressNativeKey("Space"), /Browser CDP connection closed/);
});

test("BrowserHarness pressNativeKey rejects after close", async () => {
  const { harness, profile } = createHarnessFixture();
  await harness.close();
  assert.equal(existsSync(profile), false, "closed native-key fixture left its owned profile behind");
  await assert.rejects(harness.pressNativeKey("Escape"), /Browser harness closed/);
});

test("BrowserHarness native-key API is closed and rejects unsupported keys through TypeScript", () => {
  const diagnostics = nativeKeyTypeDiagnostics(`
    import { BrowserHarness } from "./browser-harness.mts";
    declare const browser: BrowserHarness;
    browser.pressNativeKey("Enter");
    browser.pressNativeKey("Space");
    browser.pressNativeKey("Escape");
    browser.pressNativeKey("Tab");
  `);
  assert.equal(diagnostics.length, 1, diagnostics.map(formatDiagnostic).join("\n"));
  assert.equal(diagnostics[0].code, 2345);
  assert.match(formatDiagnostic(diagnostics[0]), /"Tab"/);
});

test("BrowserHarness native-key source oracle rejects public/raw, widened, incomplete, swallowed and cast mutants", () => {
  const harnessSource = readFileSync(browserHarnessPath, "utf8");
  const uiBrowserSource = readFileSync(uiBrowserTestPath, "utf8");
  assert.deepEqual(browserNativeInputContractIssues(harnessSource, uiBrowserSource), []);

  const mutants = [
    {
      name: "public generic send",
      harnessSource: replaceExactlyOnce(harnessSource, "  private async send(", "  async send("),
      uiBrowserSource,
      expectedIssue: "generic-public-cdp",
    },
    {
      name: "key union widened to string",
      harnessSource: replaceExactlyOnce(
        harnessSource,
        'export type BrowserNativeKey = "Enter" | "Space" | "Escape";',
        "export type BrowserNativeKey = string;",
      ),
      uiBrowserSource,
      expectedIssue: "native-key-union",
    },
    {
      name: "Space omitted",
      harnessSource: replaceExactlyOnce(
        harnessSource,
        'export type BrowserNativeKey = "Enter" | "Space" | "Escape";',
        'export type BrowserNativeKey = "Enter" | "Escape";',
      ),
      uiBrowserSource,
      expectedIssue: "native-key-union",
    },
    {
      name: "key-up omitted",
      harnessSource: replaceExactlyOnce(
        harnessSource,
        '    await this.send("Input.dispatchKeyEvent", { ...base, type: "keyUp" });',
        "",
      ),
      uiBrowserSource,
      expectedIssue: "native-key-order",
    },
    {
      name: "fatal error swallowed",
      harnessSource: replaceExactlyOnce(
        harnessSource,
        '    await this.send("Input.dispatchKeyEvent", { ...base, type: "keyUp" });',
        '    await this.send("Input.dispatchKeyEvent", { ...base, type: "keyUp" }).catch(() => {});',
      ),
      uiBrowserSource,
      expectedIssue: "native-key-swallow",
    },
    {
      name: "UI browser unknown double cast",
      harnessSource,
      uiBrowserSource: `${uiBrowserSource}\nconst rawInput = browser as unknown as { send(method: string): Promise<void> };\n`,
      expectedIssue: "ui-private-input-bypass",
    },
  ];

  for (const mutant of mutants) {
    const issues = browserNativeInputContractIssues(mutant.harnessSource, mutant.uiBrowserSource);
    assert.equal(
      issues.includes(mutant.expectedIssue),
      true,
      `${mutant.name} was accepted by the native-key source oracle: ${JSON.stringify(issues)}`,
    );
  }
});

test("BrowserHarness accepts only a known stale continue cancellation", async (t) => {
  const { harness, socket } = createHarnessFixture();
  t.after(() => harness.close());
  socket.hold("Fetch.continueRequest");
  socket.emitEvent("Fetch.requestPaused", {
    requestId: "stale-integration-1",
    request: { method: "GET", url: "http://fixture.test/old-document" },
  });
  const continueRequest = await socket.waitForCommand("Fetch.continueRequest");
  let idle = false;
  const handlerIdle = harness.waitForRequestHandlersIdle({ pathname: "/old-document", method: "GET", timeoutMs: 100 }).then(() => { idle = true; });
  await Promise.resolve();
  assert.equal(idle, false, "held continue request was reported idle");

  await harness.navigate("http://fixture.test/new-document");
  socket.respond(continueRequest, { error: { message: invalidInterceptionIdMessage } });
  await handlerIdle;
  harness.assertNoFatalError();
  assert.equal(harness.safeFetchCancellationCount, 1);
  assert.equal(harness.responses.get("/old-document") || 0, 0, "cancelled interception must not be counted as a response");
});

test("BrowserHarness fatal Fetch failure rejects Page.loadEventFired with the original cause", async (t) => {
  const { harness, socket } = createHarnessFixture();
  t.after(() => harness.close());
  socket.autoLoadEvent = false;
  socket.hold("Fetch.fulfillRequest");
  harness.setRouteResolver(() => ({ body: { ok: true }, requiredResponse: true }));

  const navigation = harness.navigate("http://fixture.test/current-document");
  await socket.waitForCommand("Page.navigate");
  socket.emitEvent("Fetch.requestPaused", {
    requestId: "required-integration-1",
    request: { method: "POST", url: `http://fixture.test${actionPath}` },
  });
  const fulfillRequest = await socket.waitForCommand("Fetch.fulfillRequest");
  socket.respond(fulfillRequest, { error: { message: invalidInterceptionIdMessage } });

  const outcome = await settlePromptly(navigation);
  assert.notEqual(outcome, "pending", "fatal navigation waited for the generic load timeout");
  assert.equal((outcome as Error).message, invalidInterceptionIdMessage);
  assert.throws(() => harness.assertNoFatalError(), /Invalid InterceptionId\./);
});

test("BrowserHarness socket close rejects Page.loadEventFired immediately", async (t) => {
  const { harness, socket } = createHarnessFixture();
  t.after(() => harness.close());
  socket.autoLoadEvent = false;
  const navigation = harness.navigate("http://fixture.test/socket-close");
  await socket.waitForCommand("Page.navigate");
  socket.close();

  const outcome = await settlePromptly(navigation);
  assert.notEqual(outcome, "pending", "socket-close navigation waited for the generic load timeout");
  assert.equal((outcome as Error).message, "Browser CDP connection closed");
});

test("unsafe matrix navigation before required POST settlement poisons later commands", async (t) => {
  const { harness, socket } = createHarnessFixture();
  t.after(() => harness.close());
  socket.hold("Fetch.fulfillRequest");
  harness.setRouteResolver(() => ({ body: { ready: true }, requiredResponse: true }));
  socket.emitEvent("Fetch.requestPaused", {
    requestId: "unsafe-matrix-1",
    request: { method: "POST", url: `http://fixture.test${actionPath}` },
  });
  const fulfillRequest = await socket.waitForCommand("Fetch.fulfillRequest");
  await harness.waitForRequestCount(actionPath, 1);
  const handlerIdle = harness.waitForRequestHandlersIdle({ pathname: actionPath, method: "POST", timeoutMs: 100 });

  await harness.navigate("http://fixture.test/admin/streams/");
  socket.respond(fulfillRequest, { error: { message: invalidInterceptionIdMessage } });
  await assert.rejects(handlerIdle, /Invalid InterceptionId\./);
  await assert.rejects(harness.evaluate("true"), /Invalid InterceptionId\./);
});

test("BrowserHarness close rejects a pending load waiter and removes its profile", async () => {
  const { harness, profile, socket } = createHarnessFixture();
  socket.autoLoadEvent = false;
  const navigation = harness.navigate("http://fixture.test/harness-close");
  await socket.waitForCommand("Page.navigate");
  await harness.close();

  const outcome = await settlePromptly(navigation);
  assert.notEqual(outcome, "pending", "harness close left the load waiter pending");
  assert.equal((outcome as Error).message, "Browser harness closed");
  assert.equal(existsSync(profile), false, "harness close left its owned profile behind");
});

const independentRequiredBrowserScenarioNames = Object.freeze([
  "query states distinguish loading, empty, unhealthy, error, stale, recovery, and update variants",
  "logout clears protected UI and sends exactly one mutation",
  "session expiry keeps a validated same-origin return URL without a redirect loop",
  "/auth/me 401 expiry preserves the exact query and hash return URL",
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

const independentGenericBrowserScenarioNames = Object.freeze([
  "fixture generic leaf 01",
  "fixture generic leaf 02",
  "fixture generic leaf 03",
  "fixture generic leaf 04",
  "fixture generic leaf 05",
  "fixture generic leaf 06",
  "fixture generic leaf 07",
  "fixture generic leaf 08",
  "fixture generic leaf 09",
  "fixture generic leaf 10",
] as const);

function passingScenario(name: string) {
  return { name, passed: true, skipped: false, todo: false };
}

function browserSummary(overrides: Partial<{
  cancelled: number;
  passed: number;
  skipped: number;
  suites: number;
  tests: number;
  todo: number;
  topLevel: number;
}> = {}) {
  return {
    success: true,
    counts: {
      cancelled: 0,
      passed: 26,
      skipped: 0,
      suites: 1,
      tests: 26,
      todo: 0,
      topLevel: 1,
      ...overrides,
    },
  };
}

function passingBrowserInventoryFixture() {
  return {
    summary: browserSummary(),
    completed: [
      ...independentRequiredBrowserScenarioNames.map(passingScenario),
      ...independentGenericBrowserScenarioNames.map(passingScenario),
    ],
  };
}

function completeWorkerRestartFocusFixtures() {
  const preamble = workerRestartFocusPreambleLines();
  const assertions = workerRestartFocusAssertionLines();
  const completion = workerRestartFocusCompletionLines();
  const switchSetup = [
    "let outcomeFocus;",
    "switch (failure.name) {",
    "  case \"403\":",
    ...indentWorkerRestartFixtureLines(workerRestartFocusAssignmentLines("outcomeFocus"), 4),
    "    break;",
    "  case \"409\":",
    ...indentWorkerRestartFixtureLines(workerRestartFocusAssignmentLines("outcomeFocus"), 4),
    "    break;",
    "  case \"outcome_unknown\":",
    ...indentWorkerRestartFixtureLines(workerRestartFocusAssignmentLines("outcomeFocus"), 4),
    "    break;",
    "}",
  ];
  const ifElseSetup = [
    "let outcomeFocus;",
    "if (failure.name === \"403\") {",
    ...indentWorkerRestartFixtureLines(workerRestartFocusAssignmentLines("outcomeFocus"), 2),
    "} else if (failure.name === \"409\") {",
    ...indentWorkerRestartFixtureLines(workerRestartFocusAssignmentLines("outcomeFocus"), 2),
    "} else {",
    ...indentWorkerRestartFixtureLines(workerRestartFocusAssignmentLines("outcomeFocus"), 2),
    "}",
  ];
  return [
    {
      name: "complete unconditional fixture",
      source: workerRestartFocusFixture([
        ...preamble,
        ...workerRestartFocusAssignmentLines("const outcomeFocus"),
        ...assertions,
        ...completion,
      ]),
    },
    {
      name: "complete explicit switch fixture",
      source: workerRestartFocusFixture([...preamble, ...switchSetup, ...assertions, ...completion]),
    },
    {
      name: "complete explicit if-else fixture",
      source: workerRestartFocusFixture([...preamble, ...ifElseSetup, ...assertions, ...completion]),
    },
  ];
}

function mandatoryWorkerRestartFocusMutants(production: string) {
  const focusBlock = workerRestartProductionFocusBlock();
  const conditionalSyntheticFocusBlock = [
    "        const outcomeFocus = failure.name === \"403\"",
    "          ? await waitForWorkerRestartOutcomeFocus(",
    "            browser,",
    "            \"Worker One\",",
    "            failure.publicText,",
    "            failure.name,",
    "          )",
    "          : {",
    "            dialogCount: 1,",
    "            activeExists: true,",
    "            activeInside: true,",
    "            activeIsBody: false,",
    "            activeIsTrigger: false,",
    "            activeHiddenOrInert: false,",
    "            activeVisible: true,",
    "            safeOutcomeTextVisible: true,",
    "          };",
    "",
  ].join("\n");
  const preamble = workerRestartFocusPreambleLines();
  const focus = workerRestartFocusAssignmentLines("const outcomeFocus");
  const assertions = workerRestartFocusAssertionLines();
  const completion = workerRestartFocusCompletionLines();
  const escapeAndClose = completion.slice(0, 2);
  const focusReturn = completion[2];
  const ternaryFocus = [
    "const outcomeFocus = failure.name === \"403\"",
    "  ? await waitForWorkerRestartOutcomeFocus(",
    "    browser,",
    "    \"Worker One\",",
    "    failure.publicText,",
    "    failure.name,",
    "  )",
    "  : undefined;",
  ];
  const logicalFocus = [
    "const outcomeFocus = failure.name === \"403\" && await waitForWorkerRestartOutcomeFocus(",
    "  browser,",
    "  \"Worker One\",",
    "  failure.publicText,",
    "  failure.name,",
    ");",
  ];
  const missingSwitchSetup = [
    "let outcomeFocus;",
    "switch (failure.name) {",
    "  case \"403\":",
    ...indentWorkerRestartFixtureLines(workerRestartFocusAssignmentLines("outcomeFocus"), 4),
    "    break;",
    "  case \"outcome_unknown\":",
    ...indentWorkerRestartFixtureLines(workerRestartFocusAssignmentLines("outcomeFocus"), 4),
    "    break;",
    "}",
  ];
  return [
    {
      name: "early continue bypasses 409 and outcome_unknown",
      source: replaceExactlyOnce(production, focusBlock, `        if (failure.name !== "403") continue;\n${focusBlock}`),
      expectedDiagnostic: "outcome=409;stage=real-focus;reason=early-continue",
    },
    {
      name: "403 real helper with 409 and outcome_unknown synthetic passing snapshots",
      source: replaceExactlyOnce(production, focusBlock, conditionalSyntheticFocusBlock),
      expectedDiagnostic: "outcome=409;stage=real-focus;reason=escape-before-real-focus",
    },
    {
      name: "helper executes only in the true ternary branch",
      source: workerRestartFocusFixture([...preamble, ...ternaryFocus, ...assertions, ...completion]),
      expectedDiagnostic: "outcome=409;stage=real-focus;reason=escape-before-real-focus",
    },
    {
      name: "helper executes only through logical and",
      source: workerRestartFocusFixture([...preamble, ...logicalFocus, ...assertions, ...completion]),
      expectedDiagnostic: "outcome=409;stage=real-focus;reason=escape-before-real-focus",
    },
    {
      name: "outcome_unknown returns before focus evidence",
      source: workerRestartFocusFixture([
        ...preamble,
        "if (failure.name === \"outcome_unknown\") return;",
        ...focus,
        ...assertions,
        ...completion,
      ]),
      expectedDiagnostic: "outcome=outcome_unknown;stage=real-focus;reason=early-return",
    },
    {
      name: "409 throws before focus evidence",
      source: workerRestartFocusFixture([
        ...preamble,
        "if (failure.name === \"409\") throw new Error(\"bypass\");",
        ...focus,
        ...assertions,
        ...completion,
      ]),
      expectedDiagnostic: "outcome=409;stage=real-focus;reason=early-throw",
    },
    {
      name: "switch omits the 409 focus case",
      source: workerRestartFocusFixture([...preamble, ...missingSwitchSetup, ...assertions, ...completion]),
      expectedDiagnostic: "outcome=409;stage=real-focus;reason=escape-before-real-focus",
    },
    {
      name: "real helper executes after Escape",
      source: workerRestartFocusFixture([
        ...preamble,
        escapeAndClose[0],
        ...focus,
        ...assertions,
        escapeAndClose[1],
        focusReturn,
      ]),
      expectedDiagnostic: "outcome=403;stage=real-focus;reason=helper-after-escape",
    },
    {
      name: "real helper result is ignored while assertions use a synthetic snapshot",
      source: workerRestartFocusFixture([
        ...preamble,
        ...workerRestartFocusAssignmentLines("const realOutcomeFocus"),
        ...workerRestartPassingSnapshotLines("const outcomeFocus"),
        ...assertions,
        ...completion,
      ]),
      expectedDiagnostic: "outcome=403;stage=containment;reason=assertion-not-real-symbol:dialogCount",
    },
    {
      name: "409 containment assertions are conditionally removed",
      source: workerRestartFocusFixture([
        ...preamble,
        ...focus,
        "if (failure.name !== \"409\") {",
        ...indentWorkerRestartFixtureLines(assertions, 2),
        "}",
        ...completion,
      ]),
      expectedDiagnostic: "outcome=409;stage=containment;reason=escape-before-complete-containment",
    },
    {
      name: "outcome_unknown focus return is conditionally removed",
      source: workerRestartFocusFixture([
        ...preamble,
        ...focus,
        ...assertions,
        ...escapeAndClose,
        "if (failure.name !== \"outcome_unknown\") {",
        `  ${focusReturn}`,
        "}",
      ]),
      expectedDiagnostic: "outcome=outcome_unknown;stage=focus-return;reason=missing-focus-return",
    },
    {
      name: "409 break exits before focus evidence",
      source: workerRestartFocusFixture([
        ...preamble,
        "if (failure.name === \"409\") break;",
        ...focus,
        ...assertions,
        ...completion,
      ]),
      expectedDiagnostic: "outcome=409;stage=real-focus;reason=early-break",
    },
  ];
}

function legacyWorkerRestartFocusMutants(production: string) {
  const focusBlock = workerRestartProductionFocusBlock();
  const escapeLine = "        await browser.pressNativeKey(\"Escape\");\n";
  const focusReturnLine = "        await waitForWorkerRestartTriggerFocus(browser, \"Worker One\", `${failure.name} Escape`);\n";
  const guardedFocusBlock = [
    "        if (failure.name === \"403\") {",
    ...focusBlock.trimEnd().split("\n").map((line) => `  ${line}`),
    "        }",
    "",
  ].join("\n");
  return [
    { name: "focus helper removed", source: replaceExactlyOnce(production, focusBlock, "") },
    { name: "focus helper after Escape", source: moveBlockAfter(production, focusBlock, escapeLine) },
    { name: "focus helper after focus return", source: moveBlockAfter(production, focusBlock, focusReturnLine) },
    {
      name: "body focus replacement",
      source: replaceExactlyOnce(
        production,
        focusBlock,
        "        const outcomeFocus = await browser.evaluate(\"document.body.focus(); ({})\");\n",
      ),
    },
    {
      name: "trigger focus replacement",
      source: replaceExactlyOnce(
        production,
        focusBlock,
        "        const outcomeFocus = await waitForWorkerRestartTriggerFocus(browser, \"Worker One\", failure.name);\n",
      ),
    },
    { name: "Escape removed", source: replaceExactlyOnce(production, escapeLine, "") },
    { name: "focus return removed", source: replaceExactlyOnce(production, focusReturnLine, "") },
    { name: "403-only guarded focus", source: replaceExactlyOnce(production, focusBlock, guardedFocusBlock) },
  ];
}

function independentWorkerRestartFocusMutants() {
  const preamble = workerRestartFocusPreambleLines();
  const assertions = workerRestartFocusAssertionLines();
  const completion = workerRestartFocusCompletionLines();
  const nestedHelper = [
    "const readNestedOutcomeFocus = async () => {",
    "  if (failure.name === \"403\") {",
    "    return await waitForWorkerRestartOutcomeFocus(",
    "      browser,",
    "      \"Worker One\",",
    "      failure.publicText,",
    "      failure.name,",
    "    );",
    "  }",
    "  return undefined;",
    "};",
    "const outcomeFocus = await readNestedOutcomeFocus();",
  ];
  return [
    {
      name: "nested helper returns real focus only for 403",
      source: workerRestartFocusFixture([...preamble, ...nestedHelper, ...assertions, ...completion]),
      expectedDiagnostic: "outcome=409;stage=real-focus;reason=escape-before-real-focus",
    },
    {
      name: "try-finally continue bypasses 409 before the real focus path",
      source: workerRestartFocusFixture([
        ...preamble,
        "try {",
        "  if (failure.name === \"409\") continue;",
        "} finally {",
        "  await waitForAnimationFrames(browser);",
        "}",
        ...workerRestartFocusAssignmentLines("const outcomeFocus"),
        ...assertions,
        ...completion,
      ]),
      expectedDiagnostic: "outcome=409;stage=real-focus;reason=early-continue",
    },
  ];
}

function workerRestartProductionFocusBlock() {
  return [
    "        const outcomeFocus = await waitForWorkerRestartOutcomeFocus(",
    "          browser,",
    "          \"Worker One\",",
    "          failure.publicText,",
    "          failure.name,",
    "        );",
  ].join("\n") + "\n";
}

function workerRestartFocusPreambleLines() {
  return [
    "await waitForWorkerRestartDialog(browser);",
    "await browser.waitFor(\"document.body.textContent || ''\", (value: string) => value.length > 0, \"safe outcome\");",
    "const renderedOutcome = await browser.evaluate<string>(\"document.body.textContent || ''\");",
  ];
}

function workerRestartFocusAssignmentLines(target: string) {
  return [
    `${target} = await waitForWorkerRestartOutcomeFocus(`,
    "  browser,",
    "  \"Worker One\",",
    "  failure.publicText,",
    "  failure.name,",
    ");",
  ];
}

function workerRestartFocusAssertionLines(symbol = "outcomeFocus") {
  return [
    `assert.equal(${symbol}.dialogCount, 1);`,
    `assert.equal(${symbol}.activeExists, true);`,
    `assert.equal(${symbol}.activeInside, true);`,
    `assert.equal(${symbol}.activeIsBody, false);`,
    `assert.equal(${symbol}.activeIsTrigger, false);`,
    `assert.equal(${symbol}.activeHiddenOrInert, false);`,
    `assert.equal(${symbol}.activeVisible, true);`,
    `assert.equal(${symbol}.safeOutcomeTextVisible, true);`,
  ];
}

function workerRestartPassingSnapshotLines(target: string) {
  return [
    `${target} = {`,
    "  dialogCount: 1,",
    "  activeExists: true,",
    "  activeInside: true,",
    "  activeIsBody: false,",
    "  activeIsTrigger: false,",
    "  activeHiddenOrInert: false,",
    "  activeVisible: true,",
    "  safeOutcomeTextVisible: true,",
    "};",
  ];
}

function workerRestartFocusCompletionLines() {
  return [
    "await browser.pressNativeKey(\"Escape\");",
    "await waitForWorkerRestartDialogClosed(browser);",
    "await waitForWorkerRestartTriggerFocus(browser, \"Worker One\", `${failure.name} Escape`);",
  ];
}

function workerRestartFocusFixture(body: readonly string[]) {
  return [
    "t.test(\"focus oracle fixture\", async () => {",
    "  for (const failure of [",
    "    { name: \"403\", publicText: \"forbidden\" },",
    "    { name: \"409\", publicText: \"conflict\" },",
    "    { name: \"outcome_unknown\", publicText: \"unknown\" },",
    "  ] as const) {",
    ...indentWorkerRestartFixtureLines(body, 4),
    "  }",
    "});",
    "",
  ].join("\n");
}

function indentWorkerRestartFixtureLines(lines: readonly string[], spaces: number) {
  const indentation = " ".repeat(spaces);
  return lines.map((line) => `${indentation}${line}`);
}

function assertWorkerRestartFocusDiagnosticOrder(issues: readonly string[]) {
  const outcomeOrder = new Map(workerRestartFocusOutcomes.map((outcome, index) => [outcome, index]));
  let previousOutcome = -1;
  let previousPosition = -1;
  for (const issue of issues) {
    const match = /^outcome=(403|409|outcome_unknown);stage=(real-focus|containment|escape|focus-return);reason=.+;line=(\d+);column=(\d+)$/.exec(issue);
    assert.ok(match, `focus diagnostic is missing outcome/stage/reason/line/column: ${issue}`);
    const currentOutcome = outcomeOrder.get(match[1] as WorkerRestartFocusOutcome);
    assert.notEqual(currentOutcome, undefined);
    const currentPosition = Number(match[3]) * 1_000_000 + Number(match[4]);
    assert.equal((currentOutcome as number) >= previousOutcome, true, `outcome order regressed at ${issue}`);
    if (currentOutcome === previousOutcome) {
      assert.equal(currentPosition >= previousPosition, true, `source position order regressed at ${issue}`);
    } else {
      previousPosition = -1;
    }
    previousOutcome = currentOutcome as number;
    previousPosition = currentPosition;
  }
}

const workerRestartFocusOutcomes = ["403", "409", "outcome_unknown"] as const;
type WorkerRestartFocusOutcome = typeof workerRestartFocusOutcomes[number];
type WorkerRestartFocusStage = "real-focus" | "containment" | "escape" | "focus-return";
type OutcomeTruth = true | false | "unknown";
type FocusValue =
  | { kind: "real"; origin?: ts.Symbol }
  | { kind: "synthetic" }
  | { kind: "unknown" };

type WorkerRestartFocusIssue = {
  outcome: WorkerRestartFocusOutcome;
  stage: WorkerRestartFocusStage;
  reason: string;
  position: number;
  line: number;
  column: number;
};

type WorkerRestartFocusState = {
  values: Map<ts.Symbol, FocusValue>;
  containment: Map<ts.Symbol, Set<string>>;
  dialogReady: boolean;
  renderedWait: boolean;
  renderedOutcome: boolean;
  realCallSeen: boolean;
  escaped: boolean;
  focusReturned: boolean;
};

type WorkerRestartFocusContext = {
  sourceFile: ts.SourceFile;
  checker: ts.TypeChecker;
  outcome: WorkerRestartFocusOutcome;
  outcomeVariable: string;
  issues: WorkerRestartFocusIssue[];
};

const workerRestartFocusExpectations = new Map<string, boolean | number>([
  ["dialogCount", 1],
  ["activeExists", true],
  ["activeInside", true],
  ["activeIsBody", false],
  ["activeIsTrigger", false],
  ["activeHiddenOrInert", false],
  ["activeVisible", true],
  ["safeOutcomeTextVisible", true],
]);

function workerRestartOutcomeFocusIssues(source: string) {
  const { sourceFile, checker, syntacticDiagnostics } = workerRestartFocusProgram(source);
  const issues: WorkerRestartFocusIssue[] = [];
  const structuralIssue = (reason: string, node: ts.Node = sourceFile) => {
    for (const outcome of workerRestartFocusOutcomes) {
      addWorkerRestartFocusIssue({ sourceFile, checker, outcome, outcomeVariable: "failure", issues }, "real-focus", reason, node);
    }
  };
  if (syntacticDiagnostics.length > 0) {
    const first = syntacticDiagnostics[0];
    structuralIssue("syntax-error", focusNodeAtPosition(sourceFile, first.start ?? 0));
    return formatWorkerRestartFocusIssues(issues);
  }

  const candidateLoops: ts.ForOfStatement[] = [];
  const collectLoops = (node: ts.Node) => {
    if (ts.isForOfStatement(node)) {
      const expression = unwrapExpression(node.expression);
      if (ts.isArrayLiteralExpression(expression)) {
        const names = outcomeNames(expression, sourceFile);
        if (names.some((name) => workerRestartFocusOutcomes.includes(name as WorkerRestartFocusOutcome))) {
          candidateLoops.push(node);
        }
      }
    }
    ts.forEachChild(node, collectLoops);
  };
  collectLoops(sourceFile);
  if (candidateLoops.length !== 1) {
    structuralIssue("outcome-loop-count");
    return formatWorkerRestartFocusIssues(issues);
  }

  const loop = candidateLoops[0];
  const outcomeVariable = workerRestartOutcomeVariable(loop);
  if (!outcomeVariable) structuralIssue("outcome-loop-variable", loop);
  let containingFunction: ts.FunctionLikeDeclaration | undefined;
  let ancestor: ts.Node | undefined = loop.parent;
  while (ancestor && !ts.isSourceFile(ancestor)) {
    if (ts.isIfStatement(ancestor) || ts.isConditionalExpression(ancestor)) {
      structuralIssue("outcome-loop-conditional", ancestor);
    }
    if (ts.isFunctionLike(ancestor)) {
      containingFunction = ancestor;
      break;
    }
    ancestor = ancestor.parent;
  }
  if (!containingFunction
    || !ts.isCallExpression(containingFunction.parent)
    || !containingFunction.parent.arguments.includes(containingFunction)
    || !propertyCall(containingFunction.parent, "test")) {
    structuralIssue("outcome-loop-uninvoked-callback", loop);
  }
  const expression = unwrapExpression(loop.expression);
  if (!ts.isArrayLiteralExpression(expression)) {
    structuralIssue("outcome-inventory", loop.expression);
    return formatWorkerRestartFocusIssues(issues);
  }
  const actualOutcomes = outcomeNames(expression, sourceFile).sort();
  if (JSON.stringify(actualOutcomes) !== JSON.stringify([...workerRestartFocusOutcomes].sort())) {
    structuralIssue("outcome-inventory", expression);
  }
  if (!ts.isBlock(loop.statement) || !outcomeVariable) {
    structuralIssue("outcome-block", loop.statement);
    return formatWorkerRestartFocusIssues(issues);
  }

  for (const outcome of workerRestartFocusOutcomes) {
    const context: WorkerRestartFocusContext = { sourceFile, checker, outcome, outcomeVariable, issues };
    const finalStates = analyzeWorkerRestartFocusStatements(
      [...loop.statement.statements],
      [newWorkerRestartFocusState()],
      context,
    );
    for (const state of finalStates) validateWorkerRestartFocusCompletion(state, context, loop.statement);
  }
  return formatWorkerRestartFocusIssues(issues);
}

function workerRestartFocusProgram(source: string) {
  const fileName = "/ui-foundation-browser.test.mts";
  const parsed = ts.createSourceFile(fileName, source, ts.ScriptTarget.Latest, true, ts.ScriptKind.TS);
  const options: ts.CompilerOptions = {
    module: ts.ModuleKind.ESNext,
    noLib: true,
    noResolve: true,
    target: ts.ScriptTarget.Latest,
  };
  const host: ts.CompilerHost = {
    fileExists: (candidate) => candidate === fileName,
    getCanonicalFileName: (candidate) => candidate,
    getCurrentDirectory: () => "/",
    getDefaultLibFileName: () => "/lib.d.ts",
    getDirectories: () => [],
    getNewLine: () => "\n",
    getSourceFile: (candidate) => candidate === fileName ? parsed : undefined,
    readFile: (candidate) => candidate === fileName ? source : undefined,
    useCaseSensitiveFileNames: () => true,
    writeFile: () => {},
  };
  const program = ts.createProgram([fileName], options, host);
  const sourceFile = program.getSourceFile(fileName);
  assert.ok(sourceFile, "focus oracle source file was not created");
  return {
    sourceFile,
    checker: program.getTypeChecker(),
    syntacticDiagnostics: program.getSyntacticDiagnostics(sourceFile),
  };
}

function workerRestartOutcomeVariable(loop: ts.ForOfStatement) {
  if (!ts.isVariableDeclarationList(loop.initializer) || loop.initializer.declarations.length !== 1) return undefined;
  const declaration = loop.initializer.declarations[0];
  return ts.isIdentifier(declaration.name) ? declaration.name.text : undefined;
}

function newWorkerRestartFocusState(): WorkerRestartFocusState {
  return {
    values: new Map(),
    containment: new Map(),
    dialogReady: false,
    renderedWait: false,
    renderedOutcome: false,
    realCallSeen: false,
    escaped: false,
    focusReturned: false,
  };
}

function cloneWorkerRestartFocusState(state: WorkerRestartFocusState): WorkerRestartFocusState {
  return {
    ...state,
    values: new Map(state.values),
    containment: new Map([...state.containment].map(([symbol, fields]) => [symbol, new Set(fields)])),
  };
}

function analyzeWorkerRestartFocusStatements(
  statements: readonly ts.Statement[],
  states: WorkerRestartFocusState[],
  context: WorkerRestartFocusContext,
) {
  let current = states;
  for (const statement of statements) {
    if (current.length === 0) break;
    current = analyzeWorkerRestartFocusStatement(statement, current, context);
  }
  return current;
}

function analyzeWorkerRestartFocusStatement(
  statement: ts.Statement,
  states: WorkerRestartFocusState[],
  context: WorkerRestartFocusContext,
): WorkerRestartFocusState[] {
  if (ts.isBlock(statement)) {
    return analyzeWorkerRestartFocusStatements([...statement.statements], states, context);
  }
  if (ts.isVariableStatement(statement)) {
    let current = states;
    for (const declaration of statement.declarationList.declarations) {
      current = current.flatMap((state) => analyzeWorkerRestartFocusDeclaration(declaration, state, context));
    }
    return current;
  }
  if (ts.isExpressionStatement(statement)) {
    return states.flatMap((state) => analyzeWorkerRestartFocusExpressionStatement(statement.expression, state, context));
  }
  if (ts.isIfStatement(statement)) {
    return states.flatMap((state) => analyzeWorkerRestartFocusIf(statement, state, context));
  }
  if (ts.isSwitchStatement(statement)) {
    return states.flatMap((state) => analyzeWorkerRestartFocusSwitch(statement, state, context));
  }
  if (ts.isTryStatement(statement)) {
    return states.flatMap((state) => analyzeWorkerRestartFocusTry(statement, state, context));
  }
  if (ts.isLabeledStatement(statement)) {
    return analyzeWorkerRestartFocusStatement(statement.statement, states, context);
  }
  if (ts.isContinueStatement(statement)) return workerRestartFocusEarlyExit(states, context, statement, "early-continue");
  if (ts.isBreakStatement(statement)) return workerRestartFocusEarlyExit(states, context, statement, "early-break");
  if (ts.isReturnStatement(statement)) return workerRestartFocusEarlyExit(states, context, statement, "early-return");
  if (ts.isThrowStatement(statement)) return workerRestartFocusEarlyExit(states, context, statement, "early-throw");
  if (ts.isForStatement(statement)
    || ts.isForInStatement(statement)
    || ts.isForOfStatement(statement)
    || ts.isWhileStatement(statement)
    || ts.isDoStatement(statement)) {
    return workerRestartFocusUnsupportedControl(states, context, statement, "unsupported-nested-loop");
  }
  if (ts.isFunctionDeclaration(statement)
    || ts.isClassDeclaration(statement)
    || ts.isEmptyStatement(statement)) {
    return states;
  }
  return states;
}

function analyzeWorkerRestartFocusDeclaration(
  declaration: ts.VariableDeclaration,
  state: WorkerRestartFocusState,
  context: WorkerRestartFocusContext,
) {
  const results = declaration.initializer
    ? evaluateWorkerRestartFocusValue(declaration.initializer, state, context)
    : [{ state, value: { kind: "unknown" } as FocusValue }];
  return results.map((result) => {
    if (ts.isIdentifier(declaration.name)) {
      const symbol = context.checker.getSymbolAtLocation(declaration.name);
      if (symbol) retainWorkerRestartFocusValue(result.state, symbol, result.value);
      if (declaration.name.text === "renderedOutcome") result.state.renderedOutcome = true;
    }
    return result.state;
  });
}

function analyzeWorkerRestartFocusExpressionStatement(
  expression: ts.Expression,
  state: WorkerRestartFocusState,
  context: WorkerRestartFocusContext,
): WorkerRestartFocusState[] {
  const direct = directAwaitedCall(expression);
  if (direct && isAssertEqualCall(direct.call)) {
    analyzeWorkerRestartFocusAssertion(direct.call, state, context);
    return [state];
  }
  const candidate = unwrapFocusExpression(expression);
  if (ts.isBinaryExpression(candidate) && candidate.operatorToken.kind === ts.SyntaxKind.EqualsToken) {
    const target = unwrapFocusExpression(candidate.left as ts.Expression);
    if (ts.isIdentifier(target)) {
      const symbol = context.checker.getSymbolAtLocation(target);
      return evaluateWorkerRestartFocusValue(candidate.right, state, context).map((result) => {
        if (symbol) retainWorkerRestartFocusValue(result.state, symbol, result.value);
        return result.state;
      });
    }
  }
  if (direct) {
    if (identifierCall(direct.call, "waitForWorkerRestartDialog") && direct.awaited) {
      state.dialogReady = true;
      return [state];
    }
    if (propertyCall(direct.call, "waitFor") && direct.awaited) {
      state.renderedWait = true;
      return [state];
    }
    if (propertyCall(direct.call, "pressNativeKey") && stringArgument(direct.call, 0) === "Escape") {
      analyzeWorkerRestartEscape(direct.call, direct.awaited, state, context);
      return [state];
    }
    if (identifierCall(direct.call, "waitForWorkerRestartTriggerFocus")) {
      analyzeWorkerRestartFocusReturn(direct.call, direct.awaited, state, context);
      return [state];
    }
  }
  if (containsWorkerRestartFocusCall(expression)) {
    return evaluateWorkerRestartFocusValue(expression, state, context).map((result) => result.state);
  }
  return [state];
}

function analyzeWorkerRestartFocusIf(
  statement: ts.IfStatement,
  state: WorkerRestartFocusState,
  context: WorkerRestartFocusContext,
) {
  const truth = workerRestartOutcomeTruth(statement.expression, context);
  if (truth === true) {
    return analyzeWorkerRestartFocusStatement(statement.thenStatement, [state], context);
  }
  if (truth === false) {
    return statement.elseStatement
      ? analyzeWorkerRestartFocusStatement(statement.elseStatement, [state], context)
      : [state];
  }
  const whenTrue = analyzeWorkerRestartFocusStatement(
    statement.thenStatement,
    [cloneWorkerRestartFocusState(state)],
    context,
  );
  const whenFalse = statement.elseStatement
    ? analyzeWorkerRestartFocusStatement(
      statement.elseStatement,
      [cloneWorkerRestartFocusState(state)],
      context,
    )
    : [cloneWorkerRestartFocusState(state)];
  return [...whenTrue, ...whenFalse];
}

function analyzeWorkerRestartFocusSwitch(
  statement: ts.SwitchStatement,
  state: WorkerRestartFocusState,
  context: WorkerRestartFocusContext,
) {
  const discriminant = workerRestartOutcomeString(statement.expression, context);
  if (discriminant === undefined) {
    return workerRestartFocusUnsupportedControl([state], context, statement, "unknown-switch-discriminant");
  }
  const clauses = [...statement.caseBlock.clauses];
  let start = clauses.findIndex((clause) =>
    ts.isCaseClause(clause) && workerRestartOutcomeString(clause.expression, context) === discriminant);
  if (start < 0) start = clauses.findIndex(ts.isDefaultClause);
  if (start < 0) return [state];
  let current = [state];
  for (let clauseIndex = start; clauseIndex < clauses.length; clauseIndex += 1) {
    for (const clauseStatement of clauses[clauseIndex].statements) {
      if (ts.isBreakStatement(clauseStatement) && !clauseStatement.label) return current;
      current = analyzeWorkerRestartFocusStatement(clauseStatement, current, context);
      if (current.length === 0) return current;
    }
  }
  return current;
}

function analyzeWorkerRestartFocusTry(
  statement: ts.TryStatement,
  state: WorkerRestartFocusState,
  context: WorkerRestartFocusContext,
) {
  const original = cloneWorkerRestartFocusState(state);
  const tryStates = analyzeWorkerRestartFocusStatement(statement.tryBlock, [state], context);
  const catchStates = statement.catchClause
    ? analyzeWorkerRestartFocusStatement(statement.catchClause.block, [original], context)
    : [];
  const normalStates = [...tryStates, ...catchStates];
  return statement.finallyBlock
    ? analyzeWorkerRestartFocusStatement(statement.finallyBlock, normalStates, context)
    : normalStates;
}

function evaluateWorkerRestartFocusValue(
  expression: ts.Expression,
  state: WorkerRestartFocusState,
  context: WorkerRestartFocusContext,
  awaited = false,
): Array<{ state: WorkerRestartFocusState; value: FocusValue }> {
  const candidate = unwrapFocusExpression(expression);
  if (ts.isAwaitExpression(candidate)) {
    return evaluateWorkerRestartFocusValue(candidate.expression, state, context, true);
  }
  if (ts.isConditionalExpression(candidate)) {
    const truth = workerRestartOutcomeTruth(candidate.condition, context);
    if (truth === true) return evaluateWorkerRestartFocusValue(candidate.whenTrue, state, context, awaited);
    if (truth === false) return evaluateWorkerRestartFocusValue(candidate.whenFalse, state, context, awaited);
    return [
      ...evaluateWorkerRestartFocusValue(candidate.whenTrue, cloneWorkerRestartFocusState(state), context, awaited),
      ...evaluateWorkerRestartFocusValue(candidate.whenFalse, cloneWorkerRestartFocusState(state), context, awaited),
    ];
  }
  if (ts.isBinaryExpression(candidate)) {
    const operator = candidate.operatorToken.kind;
    if (operator === ts.SyntaxKind.AmpersandAmpersandToken || operator === ts.SyntaxKind.BarBarToken) {
      const truth = workerRestartOutcomeTruth(candidate.left, context);
      const executeRight = operator === ts.SyntaxKind.AmpersandAmpersandToken ? truth === true : truth === false;
      const skipRight = operator === ts.SyntaxKind.AmpersandAmpersandToken ? truth === false : truth === true;
      if (executeRight) return evaluateWorkerRestartFocusValue(candidate.right, state, context, awaited);
      if (skipRight) return [{ state, value: { kind: "unknown" } }];
      return [
        { state: cloneWorkerRestartFocusState(state), value: { kind: "unknown" } },
        ...evaluateWorkerRestartFocusValue(candidate.right, cloneWorkerRestartFocusState(state), context, awaited),
      ];
    }
  }
  if (ts.isCallExpression(candidate) && identifierCall(candidate, "waitForWorkerRestartOutcomeFocus")) {
    state.realCallSeen = true;
    if (!awaited) {
      addWorkerRestartFocusIssue(context, "real-focus", "helper-not-awaited", candidate);
      return [{ state, value: { kind: "unknown" } }];
    }
    if (state.escaped) {
      addWorkerRestartFocusIssue(context, "real-focus", "helper-after-escape", candidate);
      return [{ state, value: { kind: "unknown" } }];
    }
    if (!state.dialogReady || !state.renderedWait || !state.renderedOutcome) {
      addWorkerRestartFocusIssue(context, "real-focus", "helper-before-safe-outcome", candidate);
    }
    if (!exactWorkerRestartFocusCall(candidate, context)) {
      addWorkerRestartFocusIssue(context, "real-focus", "wrong-helper-arguments", candidate);
      return [{ state, value: { kind: "unknown" } }];
    }
    return [{ state, value: { kind: "real" } }];
  }
  if (ts.isIdentifier(candidate)) {
    const symbol = context.checker.getSymbolAtLocation(candidate);
    return [{ state, value: symbol ? state.values.get(symbol) ?? { kind: "unknown" } : { kind: "unknown" } }];
  }
  if (ts.isObjectLiteralExpression(candidate) || ts.isArrayLiteralExpression(candidate)) {
    return [{ state, value: { kind: "synthetic" } }];
  }
  return [{ state, value: { kind: "unknown" } }];
}

function retainWorkerRestartFocusValue(state: WorkerRestartFocusState, symbol: ts.Symbol, value: FocusValue) {
  state.values.set(symbol, value.kind === "real" && !value.origin ? { ...value, origin: symbol } : value);
}

function analyzeWorkerRestartFocusAssertion(
  call: ts.CallExpression,
  state: WorkerRestartFocusState,
  context: WorkerRestartFocusContext,
) {
  if (call.arguments.length < 2) return;
  const property = unwrapFocusExpression(call.arguments[0]);
  if (!ts.isPropertyAccessExpression(property)) return;
  const field = property.name.text;
  if (!workerRestartFocusExpectations.has(field)) return;
  if (state.escaped) {
    addWorkerRestartFocusIssue(context, "containment", `assertion-after-escape:${field}`, call);
    return;
  }
  const root = unwrapFocusExpression(property.expression);
  const symbol = ts.isIdentifier(root) ? context.checker.getSymbolAtLocation(root) : undefined;
  const value = symbol ? state.values.get(symbol) : undefined;
  if (!symbol || value?.kind !== "real" || value.origin !== symbol) {
    addWorkerRestartFocusIssue(context, "containment", `assertion-not-real-symbol:${field}`, call);
    return;
  }
  const expected = workerRestartFocusExpectations.get(field);
  if (workerRestartLiteralValue(call.arguments[1]) !== expected) {
    addWorkerRestartFocusIssue(context, "containment", `wrong-expected-value:${field}`, call);
    return;
  }
  const fields = state.containment.get(symbol) ?? new Set<string>();
  fields.add(field);
  state.containment.set(symbol, fields);
}

function analyzeWorkerRestartEscape(
  call: ts.CallExpression,
  awaited: boolean,
  state: WorkerRestartFocusState,
  context: WorkerRestartFocusContext,
) {
  if (!awaited) addWorkerRestartFocusIssue(context, "escape", "escape-not-awaited", call);
  if (!hasWorkerRestartRealFocus(state)) {
    addWorkerRestartFocusIssue(context, "real-focus", "escape-before-real-focus", call);
  } else if (!hasWorkerRestartCompleteContainment(state)) {
    addWorkerRestartFocusIssue(context, "containment", "escape-before-complete-containment", call);
  }
  state.escaped = true;
}

function analyzeWorkerRestartFocusReturn(
  call: ts.CallExpression,
  awaited: boolean,
  state: WorkerRestartFocusState,
  context: WorkerRestartFocusContext,
) {
  if (!awaited || !exactWorkerRestartFocusReturn(call, context)) {
    addWorkerRestartFocusIssue(context, "focus-return", "wrong-trigger-focus-return", call);
    return;
  }
  if (!state.escaped) {
    addWorkerRestartFocusIssue(context, "escape", "focus-return-before-escape", call);
    return;
  }
  state.focusReturned = true;
}

function workerRestartFocusEarlyExit(
  states: WorkerRestartFocusState[],
  context: WorkerRestartFocusContext,
  node: ts.Node,
  reason: string,
) {
  for (const state of states) {
    const stage = firstMissingWorkerRestartFocusStage(state);
    if (stage) addWorkerRestartFocusIssue(context, stage, reason, node);
  }
  return [];
}

function workerRestartFocusUnsupportedControl(
  states: WorkerRestartFocusState[],
  context: WorkerRestartFocusContext,
  node: ts.Node,
  reason: string,
) {
  const remaining: WorkerRestartFocusState[] = [];
  for (const state of states) {
    const stage = firstMissingWorkerRestartFocusStage(state);
    if (stage) addWorkerRestartFocusIssue(context, stage, reason, node);
    else remaining.push(state);
  }
  return remaining;
}

function validateWorkerRestartFocusCompletion(
  state: WorkerRestartFocusState,
  context: WorkerRestartFocusContext,
  node: ts.Node,
) {
  const stage = firstMissingWorkerRestartFocusStage(state);
  if (!stage) return;
  let reason = "missing-focus-return";
  if (stage === "real-focus") reason = state.realCallSeen ? "result-not-retained" : "missing-real-focus";
  if (stage === "containment") {
    reason = `missing-containment:${missingWorkerRestartContainmentFields(state).join(",")}`;
  }
  if (stage === "escape") reason = "missing-escape";
  addWorkerRestartFocusIssue(context, stage, reason, node);
}

function firstMissingWorkerRestartFocusStage(state: WorkerRestartFocusState): WorkerRestartFocusStage | undefined {
  if (!hasWorkerRestartRealFocus(state)) return "real-focus";
  if (!hasWorkerRestartCompleteContainment(state)) return "containment";
  if (!state.escaped) return "escape";
  if (!state.focusReturned) return "focus-return";
  return undefined;
}

function hasWorkerRestartRealFocus(state: WorkerRestartFocusState) {
  return [...state.values.values()].some((value) => value.kind === "real" && value.origin);
}

function hasWorkerRestartCompleteContainment(state: WorkerRestartFocusState) {
  return [...state.values.entries()].some(([symbol, value]) =>
    value.kind === "real"
      && value.origin === symbol
      && workerRestartFocusExpectations.size === (state.containment.get(symbol)?.size ?? 0));
}

function missingWorkerRestartContainmentFields(state: WorkerRestartFocusState) {
  const best = [...state.values.entries()]
    .filter(([symbol, value]) => value.kind === "real" && value.origin === symbol)
    .map(([symbol]) => state.containment.get(symbol) ?? new Set<string>())
    .sort((left, right) => right.size - left.size)[0] ?? new Set<string>();
  return [...workerRestartFocusExpectations.keys()].filter((field) => !best.has(field));
}

function exactWorkerRestartFocusCall(call: ts.CallExpression, context: WorkerRestartFocusContext) {
  return call.arguments.length >= 4
    && isIdentifierText(call.arguments[0], "browser")
    && stringArgument(call, 1) === "Worker One"
    && isOutcomeProperty(call.arguments[2], context.outcomeVariable, "publicText")
    && isOutcomeNameExpression(call.arguments[3], context);
}

function exactWorkerRestartFocusReturn(call: ts.CallExpression, context: WorkerRestartFocusContext) {
  if (call.arguments.length < 3
    || !isIdentifierText(call.arguments[0], "browser")
    || stringArgument(call, 1) !== "Worker One") return false;
  const label = unwrapFocusExpression(call.arguments[2]);
  if (ts.isStringLiteral(label)) return label.text === `${context.outcome} Escape`;
  return ts.isTemplateExpression(label)
    && label.head.text === ""
    && label.templateSpans.length === 1
    && isOutcomeNameExpression(label.templateSpans[0].expression, context)
    && label.templateSpans[0].literal.text === " Escape";
}

function isAssertEqualCall(call: ts.CallExpression) {
  return ts.isPropertyAccessExpression(call.expression)
    && ts.isIdentifier(call.expression.expression)
    && call.expression.expression.text === "assert"
    && call.expression.name.text === "equal";
}

function directAwaitedCall(expression: ts.Expression) {
  let candidate = unwrapFocusExpression(expression);
  let awaited = false;
  if (ts.isAwaitExpression(candidate)) {
    awaited = true;
    candidate = unwrapFocusExpression(candidate.expression);
  }
  return ts.isCallExpression(candidate) ? { call: candidate, awaited } : undefined;
}

function containsWorkerRestartFocusCall(node: ts.Node) {
  let found = false;
  const visit = (candidate: ts.Node) => {
    if (found || (candidate !== node && ts.isFunctionLike(candidate))) return;
    if (ts.isCallExpression(candidate) && identifierCall(candidate, "waitForWorkerRestartOutcomeFocus")) {
      found = true;
      return;
    }
    ts.forEachChild(candidate, visit);
  };
  visit(node);
  return found;
}

function workerRestartOutcomeTruth(expression: ts.Expression, context: WorkerRestartFocusContext): OutcomeTruth {
  const candidate = unwrapFocusExpression(expression);
  if (candidate.kind === ts.SyntaxKind.TrueKeyword) return true;
  if (candidate.kind === ts.SyntaxKind.FalseKeyword) return false;
  if (ts.isPrefixUnaryExpression(candidate) && candidate.operator === ts.SyntaxKind.ExclamationToken) {
    const value = workerRestartOutcomeTruth(candidate.operand, context);
    return value === "unknown" ? value : !value;
  }
  if (ts.isBinaryExpression(candidate)) {
    const operator = candidate.operatorToken.kind;
    if (operator === ts.SyntaxKind.AmpersandAmpersandToken) {
      const left = workerRestartOutcomeTruth(candidate.left, context);
      if (left === false) return false;
      const right = workerRestartOutcomeTruth(candidate.right, context);
      if (left === true) return right;
      return right === false ? false : "unknown";
    }
    if (operator === ts.SyntaxKind.BarBarToken) {
      const left = workerRestartOutcomeTruth(candidate.left, context);
      if (left === true) return true;
      const right = workerRestartOutcomeTruth(candidate.right, context);
      if (left === false) return right;
      return right === true ? true : "unknown";
    }
    if (operator === ts.SyntaxKind.EqualsEqualsEqualsToken
      || operator === ts.SyntaxKind.EqualsEqualsToken
      || operator === ts.SyntaxKind.ExclamationEqualsEqualsToken
      || operator === ts.SyntaxKind.ExclamationEqualsToken) {
      const left = workerRestartOutcomeString(candidate.left, context);
      const right = workerRestartOutcomeString(candidate.right, context);
      if (left === undefined || right === undefined) return "unknown";
      const equal = left === right;
      return operator === ts.SyntaxKind.EqualsEqualsEqualsToken || operator === ts.SyntaxKind.EqualsEqualsToken
        ? equal
        : !equal;
    }
  }
  return "unknown";
}

function workerRestartOutcomeString(expression: ts.Expression, context: WorkerRestartFocusContext) {
  const candidate = unwrapFocusExpression(expression);
  if (ts.isStringLiteral(candidate) || ts.isNoSubstitutionTemplateLiteral(candidate)) return candidate.text;
  if (isOutcomeProperty(candidate, context.outcomeVariable, "name")) return context.outcome;
  return undefined;
}

function isOutcomeNameExpression(expression: ts.Expression, context: WorkerRestartFocusContext) {
  const candidate = unwrapFocusExpression(expression);
  return isOutcomeProperty(candidate, context.outcomeVariable, "name")
    || ((ts.isStringLiteral(candidate) || ts.isNoSubstitutionTemplateLiteral(candidate))
      && candidate.text === context.outcome);
}

function isOutcomeProperty(expression: ts.Expression, variable: string, property: string) {
  const candidate = unwrapFocusExpression(expression);
  if (ts.isPropertyAccessExpression(candidate)) {
    return isIdentifierText(candidate.expression, variable) && candidate.name.text === property;
  }
  if (ts.isElementAccessExpression(candidate) && candidate.argumentExpression) {
    const argument = unwrapFocusExpression(candidate.argumentExpression);
    return isIdentifierText(candidate.expression, variable)
      && ts.isStringLiteral(argument)
      && argument.text === property;
  }
  return false;
}

function isIdentifierText(expression: ts.Expression, name: string) {
  const candidate = unwrapFocusExpression(expression);
  return ts.isIdentifier(candidate) && candidate.text === name;
}

function workerRestartLiteralValue(expression: ts.Expression) {
  const candidate = unwrapFocusExpression(expression);
  if (candidate.kind === ts.SyntaxKind.TrueKeyword) return true;
  if (candidate.kind === ts.SyntaxKind.FalseKeyword) return false;
  if (ts.isNumericLiteral(candidate)) return Number(candidate.text);
  return undefined;
}

function unwrapFocusExpression(expression: ts.Expression): ts.Expression {
  if (ts.isNonNullExpression(expression) || ts.isTypeAssertionExpression(expression)) {
    return unwrapFocusExpression(expression.expression);
  }
  return unwrapExpression(expression);
}

function addWorkerRestartFocusIssue(
  context: WorkerRestartFocusContext,
  stage: WorkerRestartFocusStage,
  reason: string,
  node: ts.Node,
) {
  const position = node.getStart(context.sourceFile);
  const location = context.sourceFile.getLineAndCharacterOfPosition(position);
  context.issues.push({
    outcome: context.outcome,
    stage,
    reason,
    position,
    line: location.line + 1,
    column: location.character + 1,
  });
}

function formatWorkerRestartFocusIssues(issues: WorkerRestartFocusIssue[]) {
  const outcomeOrder = new Map(workerRestartFocusOutcomes.map((outcome, index) => [outcome, index]));
  const stageOrder = new Map<WorkerRestartFocusStage, number>([
    ["real-focus", 0],
    ["containment", 1],
    ["escape", 2],
    ["focus-return", 3],
  ]);
  const unique = new Map<string, WorkerRestartFocusIssue>();
  for (const issue of issues) {
    const key = `${issue.outcome}\0${issue.stage}\0${issue.reason}\0${issue.position}`;
    if (!unique.has(key)) unique.set(key, issue);
  }
  return [...unique.values()]
    .sort((left, right) =>
      (outcomeOrder.get(left.outcome) ?? 99) - (outcomeOrder.get(right.outcome) ?? 99)
        || left.position - right.position
        || (stageOrder.get(left.stage) ?? 99) - (stageOrder.get(right.stage) ?? 99)
        || left.reason.localeCompare(right.reason))
    .map((issue) =>
      `outcome=${issue.outcome};stage=${issue.stage};reason=${issue.reason};line=${issue.line};column=${issue.column}`);
}

function focusNodeAtPosition(sourceFile: ts.SourceFile, position: number) {
  let found: ts.Node = sourceFile;
  const visit = (node: ts.Node) => {
    if (position < node.getFullStart() || position >= node.getEnd()) return;
    found = node;
    ts.forEachChild(node, visit);
  };
  visit(sourceFile);
  return found;
}

function outcomeNames(expression: ts.ArrayLiteralExpression, sourceFile: ts.SourceFile) {
  const names: string[] = [];
  for (const element of expression.elements) {
    const candidate = unwrapExpression(element);
    if (!ts.isObjectLiteralExpression(candidate)) continue;
    const nameProperty = candidate.properties.find((property): property is ts.PropertyAssignment =>
      ts.isPropertyAssignment(property) && property.name.getText(sourceFile).replace(/^['"]|['"]$/g, "") === "name");
    if (nameProperty && ts.isStringLiteral(nameProperty.initializer)) names.push(nameProperty.initializer.text);
  }
  return names;
}

function identifierCall(call: ts.CallExpression, name: string) {
  return ts.isIdentifier(call.expression) && call.expression.text === name;
}

function propertyCall(call: ts.CallExpression, name: string) {
  return ts.isPropertyAccessExpression(call.expression) && call.expression.name.text === name;
}

function stringArgument(call: ts.CallExpression, index: number) {
  const argument = call.arguments[index];
  return argument && ts.isStringLiteral(argument) ? argument.text : undefined;
}

function runnerNextPreservationIssues(source: string) {
  const issues = new Set<string>();
  const sourceFile = ts.createSourceFile(
    "run-ui-foundation-browser.mts",
    source,
    ts.ScriptTarget.Latest,
    true,
    ts.ScriptKind.TS,
  );
  const helper = sourceFile.statements.find((statement): statement is ts.FunctionDeclaration =>
    ts.isFunctionDeclaration(statement) && statement.name?.text === "withPreservedNextBuildDirectory");
  if (!helper?.body || !(helper.modifiers?.some((modifier) => modifier.kind === ts.SyntaxKind.ExportKeyword) ?? false)) {
    issues.add("preservation-helper");
    return [...issues];
  }
  const tryStatements: ts.TryStatement[] = [];
  const collectTryStatements = (node: ts.Node) => {
    if (ts.isFunctionLike(node) && node !== helper) return;
    if (ts.isTryStatement(node)) tryStatements.push(node);
    ts.forEachChild(node, collectTryStatements);
  };
  collectTryStatements(helper);
  const callbackTry = tryStatements.find((statement) => containsAwaitedIdentifierCall(statement.tryBlock, "callback"));
  if (!callbackTry?.finallyBlock) {
    issues.add("restore-finally");
  } else {
    const finallyCalls = callsWithin(callbackTry.finallyBlock);
    if (!finallyCalls.some((call) => identifierCall(call, "removeFreshNextBuildDirectory"))) {
      issues.add("fresh-cleanup-finally");
    }
    if (!finallyCalls.some((call) =>
      identifierCall(call, "renameSync")
        && identifierArgument(call, 0) === "backupNextBuildDirectory"
        && identifierArgument(call, 1) === "nextBuildDirectory")) {
      issues.add("restore-rename-finally");
    }
  }

  const guardedMain = sourceFile.statements.some((statement) =>
    ts.isIfStatement(statement)
      && ts.isCallExpression(statement.expression)
      && identifierCall(statement.expression, "isDirectExecution")
      && callsWithin(statement.thenStatement).some((call) => identifierCall(call, "main")));
  if (!guardedMain) issues.add("direct-execution-guard");
  return [...issues].sort();
}

function containsAwaitedIdentifierCall(node: ts.Node, name: string) {
  let found = false;
  const visit = (candidate: ts.Node) => {
    if (found || (ts.isFunctionLike(candidate) && candidate !== node)) return;
    if (ts.isAwaitExpression(candidate)
      && ts.isCallExpression(candidate.expression)
      && identifierCall(candidate.expression, name)) {
      found = true;
      return;
    }
    ts.forEachChild(candidate, visit);
  };
  visit(node);
  return found;
}

function callsWithin(node: ts.Node) {
  const calls: ts.CallExpression[] = [];
  const visit = (candidate: ts.Node) => {
    if (ts.isFunctionLike(candidate) && candidate !== node) return;
    if (ts.isCallExpression(candidate)) calls.push(candidate);
    ts.forEachChild(candidate, visit);
  };
  visit(node);
  return calls;
}

function identifierArgument(call: ts.CallExpression, index: number) {
  const argument = call.arguments[index];
  return argument && ts.isIdentifier(argument) ? argument.text : undefined;
}

function moveBlockAfter(source: string, block: string, anchor: string) {
  return replaceExactlyOnce(replaceExactlyOnce(source, block, ""), anchor, `${anchor}${block}`);
}

function temporaryWebRoot(t: { after(callback: () => void): void }) {
  const root = mkdtempSync(join(tmpdir(), "autostream-ui-browser-next-fixture-"));
  t.after(() => {
    const resolvedRoot = resolve(root);
    const relativeToTemp = relative(resolve(tmpdir()), resolvedRoot);
    assert.equal(relativeToTemp.startsWith(".."), false, "fixture root escaped the OS temp directory");
    assert.equal(relativeToTemp.includes(sep), false, "fixture root is not an immediate OS temp child");
    assert.equal(relativeToTemp.startsWith("autostream-ui-browser-next-fixture-"), true);
    rmSync(resolvedRoot, { recursive: true, force: true });
  });
  return root;
}

function createOriginalNextFixture(nextBuildDirectory: string) {
  mkdirSync(join(nextBuildDirectory, "nested", "empty"), { recursive: true });
  writeFileSync(join(nextBuildDirectory, "root.txt"), "original-root\r\n");
  writeFileSync(join(nextBuildDirectory, "nested", "bytes.bin"), Buffer.from([0, 1, 2, 255]));
}

function runnerBackupDirectories(fakeWebRoot: string) {
  return readdirSync(fakeWebRoot, { withFileTypes: true })
    .filter((entry) => entry.isDirectory() && entry.name.startsWith(".ui-foundation-browser-next-backup-"))
    .map((entry) => entry.name)
    .sort();
}

function nextFixtureFingerprint(nextBuildDirectory: string) {
  if (!existsSync(nextBuildDirectory)) {
    return { state: "ABSENT", files: 0, directories: 0, bytes: 0, entries: 0, sha256: null } as const;
  }
  const rows: string[] = [];
  let files = 0;
  let directories = 0;
  let bytes = 0;
  const walk = (directory: string) => {
    const entries = readdirSync(directory).sort((left, right) => Buffer.from(left).compare(Buffer.from(right)));
    for (const name of entries) {
      const fullPath = join(directory, name);
      const relativePath = relative(nextBuildDirectory, fullPath).split(sep).join("/");
      const status = lstatSync(fullPath);
      if (status.isDirectory()) {
        directories += 1;
        rows.push(`D\0${relativePath}\0-\n`);
        walk(fullPath);
      } else if (status.isFile()) {
        const content = readFileSync(fullPath);
        files += 1;
        bytes += content.length;
        rows.push(`F\0${relativePath}\0${sha256(content)}\n`);
      } else if (status.isSymbolicLink()) {
        rows.push(`L\0${relativePath}\0${sha256(Buffer.from(readlinkSync(fullPath)))}\n`);
      } else {
        rows.push(`O\0${relativePath}\0-\n`);
      }
    }
  };
  walk(nextBuildDirectory);
  return {
    state: "PRESENT",
    files,
    directories,
    bytes,
    entries: rows.length,
    sha256: sha256(Buffer.from(rows.join(""))),
  } as const;
}

function sha256(value: NodeJS.ArrayBufferView) {
  return createHash("sha256").update(value).digest("hex");
}

function nativeKeyTypeDiagnostics(probeSource: string) {
  const virtualFileName = join(helperRoot, "browser-native-key.type-probe.mts");
  const canonicalVirtualFileName = ts.sys.resolvePath(virtualFileName);
  const isVirtualFile = (fileName: string) => ts.sys.resolvePath(fileName) === canonicalVirtualFileName;
  const options: ts.CompilerOptions = {
    allowImportingTsExtensions: true,
    module: ts.ModuleKind.NodeNext,
    moduleResolution: ts.ModuleResolutionKind.NodeNext,
    noEmit: true,
    skipLibCheck: true,
    strict: true,
    target: ts.ScriptTarget.ES2022,
    types: ["node"],
  };
  const host = ts.createCompilerHost(options, true);
  const originalFileExists = host.fileExists.bind(host);
  const originalReadFile = host.readFile.bind(host);
  const originalGetSourceFile = host.getSourceFile.bind(host);
  host.fileExists = (fileName) => isVirtualFile(fileName) || originalFileExists(fileName);
  host.readFile = (fileName) => isVirtualFile(fileName) ? probeSource : originalReadFile(fileName);
  host.getSourceFile = (fileName, languageVersion, onError, shouldCreateNewSourceFile) => {
    if (isVirtualFile(fileName)) {
      return ts.createSourceFile(fileName, probeSource, languageVersion, true, ts.ScriptKind.TS);
    }
    return originalGetSourceFile(fileName, languageVersion, onError, shouldCreateNewSourceFile);
  };
  const program = ts.createProgram([virtualFileName], options, host);
  return ts.getPreEmitDiagnostics(program)
    .filter((diagnostic) => diagnostic.file && isVirtualFile(diagnostic.file.fileName));
}

function formatDiagnostic(diagnostic: ts.Diagnostic) {
  return ts.flattenDiagnosticMessageText(diagnostic.messageText, "\n");
}

function browserNativeInputContractIssues(harnessSource: string, uiBrowserSource: string) {
  const issues = new Set<string>();
  const sourceFile = ts.createSourceFile("browser-harness.mts", harnessSource, ts.ScriptTarget.Latest, true, ts.ScriptKind.TS);
  const nativeKeyAlias = sourceFile.statements.find((statement): statement is ts.TypeAliasDeclaration =>
    ts.isTypeAliasDeclaration(statement) && statement.name.text === "BrowserNativeKey");
  const nativeKeyValues = nativeKeyAlias && ts.isUnionTypeNode(nativeKeyAlias.type)
    ? nativeKeyAlias.type.types
      .filter((type): type is ts.LiteralTypeNode & { literal: ts.StringLiteral } =>
        ts.isLiteralTypeNode(type) && ts.isStringLiteral(type.literal))
      .map((type) => type.literal.text)
      .sort()
    : [];
  if (JSON.stringify(nativeKeyValues) !== JSON.stringify(["Enter", "Escape", "Space"])) {
    issues.add("native-key-union");
  }

  const harnessClass = sourceFile.statements.find((statement): statement is ts.ClassDeclaration =>
    ts.isClassDeclaration(statement) && statement.name?.text === "BrowserHarness");
  if (!harnessClass) {
    issues.add("browser-harness-class");
    return [...issues];
  }

  for (const member of harnessClass.members) {
    if (!ts.isMethodDeclaration(member)) continue;
    const name = member.name.getText(sourceFile);
    const isPrivate = member.modifiers?.some((modifier) =>
      modifier.kind === ts.SyntaxKind.PrivateKeyword || modifier.kind === ts.SyntaxKind.ProtectedKeyword) ?? false;
    if (!isPrivate && ["send", "dispatchCDP", "executeProtocol", "rawSocket"].includes(name)) {
      issues.add("generic-public-cdp");
    }
  }

  const pressNativeKey = harnessClass.members.find((member): member is ts.MethodDeclaration =>
    ts.isMethodDeclaration(member) && member.name.getText(sourceFile) === "pressNativeKey");
  if (!pressNativeKey
    || pressNativeKey.modifiers?.some((modifier) =>
      modifier.kind === ts.SyntaxKind.PrivateKeyword || modifier.kind === ts.SyntaxKind.ProtectedKeyword)
    || pressNativeKey.parameters.length !== 1
    || pressNativeKey.parameters[0].type?.getText(sourceFile) !== "BrowserNativeKey"
    || pressNativeKey.type?.getText(sourceFile) !== "Promise<void>"
    || !pressNativeKey.body) {
    issues.add("native-key-method");
  }

  const mappingDeclaration = sourceFile.statements
    .filter(ts.isVariableStatement)
    .flatMap((statement) => [...statement.declarationList.declarations])
    .find((declaration) => ts.isIdentifier(declaration.name) && declaration.name.text === "browserNativeKeyInputs");
  const mappingExpression = mappingDeclaration?.initializer
    ? unwrapExpression(mappingDeclaration.initializer)
    : undefined;
  const mappingKeys = mappingExpression && ts.isObjectLiteralExpression(mappingExpression)
    ? mappingExpression.properties
      .filter((property): property is ts.PropertyAssignment => ts.isPropertyAssignment(property))
      .map((property) => property.name.getText(sourceFile).replace(/^['"]|['"]$/g, ""))
      .sort()
    : [];
  if (JSON.stringify(mappingKeys) !== JSON.stringify(["Enter", "Escape", "Space"])) {
    issues.add("native-key-mapping");
  }

  if (pressNativeKey?.body) {
    const dispatchTypes: string[] = [];
    let swallowsFailure = false;
    const visit = (node: ts.Node) => {
      if (ts.isTryStatement(node)) swallowsFailure = true;
      if (ts.isPropertyAccessExpression(node) && node.name.text === "catch") swallowsFailure = true;
      if (ts.isCallExpression(node)
        && ts.isPropertyAccessExpression(node.expression)
        && node.expression.expression.kind === ts.SyntaxKind.ThisKeyword
        && node.expression.name.text === "send"
        && node.arguments[0]
        && ts.isStringLiteral(node.arguments[0])
        && node.arguments[0].text === "Input.dispatchKeyEvent"
        && node.arguments[1]
        && ts.isObjectLiteralExpression(node.arguments[1])) {
        const typeProperty = node.arguments[1].properties.find((property): property is ts.PropertyAssignment =>
          ts.isPropertyAssignment(property) && property.name.getText(sourceFile) === "type");
        if (typeProperty && ts.isStringLiteral(typeProperty.initializer)) dispatchTypes.push(typeProperty.initializer.text);
      }
      ts.forEachChild(node, visit);
    };
    visit(pressNativeKey.body);
    if (JSON.stringify(dispatchTypes) !== JSON.stringify(["keyDown", "keyUp"])) {
      issues.add("native-key-order");
    }
    if (swallowsFailure) issues.add("native-key-swallow");
  }

  const uiSourceFile = ts.createSourceFile("ui-foundation-browser.test.mts", uiBrowserSource, ts.ScriptTarget.Latest, true, ts.ScriptKind.TS);
  const visitUI = (node: ts.Node) => {
    if (ts.isAsExpression(node)
      && ts.isAsExpression(node.expression)
      && node.expression.type.kind === ts.SyntaxKind.UnknownKeyword) {
      issues.add("ui-private-input-bypass");
    }
    if (ts.isCallExpression(node)
      && ts.isPropertyAccessExpression(node.expression)
      && node.expression.name.text === "send"
      && node.arguments[0]
      && ts.isStringLiteral(node.arguments[0])
      && node.arguments[0].text === "Input.dispatchKeyEvent") {
      issues.add("ui-private-input-bypass");
    }
    ts.forEachChild(node, visitUI);
  };
  visitUI(uiSourceFile);
  return [...issues];
}

function unwrapExpression(expression: ts.Expression): ts.Expression {
  if (ts.isSatisfiesExpression(expression)
    || ts.isAsExpression(expression)
    || ts.isParenthesizedExpression(expression)) {
    return unwrapExpression(expression.expression);
  }
  return expression;
}

function replaceExactlyOnce(source: string, before: string, after: string) {
  const index = source.indexOf(before);
  assert.notEqual(index, -1, `mutation source was not found: ${before}`);
  assert.equal(source.indexOf(before, index + before.length), -1, `mutation source was not unique: ${before}`);
  return `${source.slice(0, index)}${after}${source.slice(index + before.length)}`;
}

async function settlePromptly(promise: Promise<unknown>) {
  return Promise.race([
    promise.then(
      () => new Error("waiter unexpectedly resolved"),
      (error) => error,
    ),
    new Promise<"pending">((resolvePending) => setTimeout(() => resolvePending("pending"), 20)),
  ]);
}

type FakeCDPCommand = {
  id: number;
  method: string;
  params: Record<string, unknown>;
  sessionId?: string;
};

class FakeCDPSocket extends EventTarget {
  autoLoadEvent = true;
  private readonly commands: FakeCDPCommand[] = [];
  private readonly heldMethods = new Set<string>();
  private readonly commandWaiters = new Map<string, Array<(command: FakeCDPCommand) => void>>();

  hold(method: string) {
    this.heldMethods.add(method);
  }

  send(rawMessage: string) {
    const command = JSON.parse(rawMessage) as FakeCDPCommand;
    this.commands.push(command);
    const waiter = this.commandWaiters.get(command.method)?.shift();
    if (waiter) waiter(command);
    if (this.heldMethods.has(command.method)) return;
    queueMicrotask(() => {
      this.respond(command, { result: {} });
      if (command.method === "Page.navigate" && this.autoLoadEvent) {
        this.emitEvent("Page.loadEventFired", { timestamp: 1 });
      }
    });
  }

  close() {
    this.dispatchEvent(new Event("close"));
  }

  emitEvent(method: string, params: Record<string, unknown>) {
    this.emitMessage({ method, params, sessionId: "test-session" });
  }

  respond(command: FakeCDPCommand, response: { result?: Record<string, unknown>; error?: { message: string } }) {
    this.emitMessage({ id: command.id, sessionId: command.sessionId, ...response });
  }

  waitForCommand(method: string) {
    const existing = this.commands.find((command) => command.method === method);
    if (existing) return Promise.resolve(existing);
    return new Promise<FakeCDPCommand>((resolveCommand) => {
      const waiters = this.commandWaiters.get(method) || [];
      waiters.push(resolveCommand);
      this.commandWaiters.set(method, waiters);
    });
  }

  commandsFor(method: string) {
    return this.commands.filter((command) => command.method === method);
  }

  private emitMessage(message: Record<string, unknown>) {
    this.dispatchEvent(new MessageEvent("message", { data: JSON.stringify(message) }));
  }
}

function createHarnessFixture() {
  const socket = new FakeCDPSocket();
  const profile = mkdtempSync(join(tmpdir(), "autostream-ui-browser-"));
  const browserProcess = { exitCode: 0, signalCode: null };
  const harness = Reflect.construct(
    BrowserHarness,
    [browserProcess, profile, socket as unknown as WebSocket, "test-session"],
  ) as BrowserHarness;
  return { harness, profile, socket };
}
