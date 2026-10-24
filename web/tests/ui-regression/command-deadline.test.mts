import assert from "node:assert/strict";
import test, { type TestContext } from "node:test";
import { existsSync, mkdirSync, mkdtempSync, readFileSync, readdirSync, rmSync, writeFileSync } from "node:fs";
import { execFileSync } from "node:child_process";
import { tmpdir } from "node:os";
import { basename, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { setImmediate as turn } from "node:timers/promises";
import ts from "typescript";
import { createHarnessFixture } from "../helpers/browser-cdp-socket-fixture.mts";
import { BrowserCommandDeadlineError, commandTimeoutMs } from "../helpers/browser-command-deadline.mts";
import { createConditionRunner, ConditionFailure } from "./condition-lifecycle.mts";
import { conditions, selectedConditions, inventory, assertExecution, nativeZoomEvidence } from "./matrix.mts";
import { navigateDocument } from "./navigation.mts";
import { actualCallback } from "./source-callback.mts";
import { ownedOutput, DraftRestorationFailure, ConditionOutputFailure, conditionFailureStatus, writeConditionFailure } from "./run-browser.mts";
import { clickNamed, clickVisible, clickWorkerRestart, selectVisible, VisibleTriggerCleanupFailure } from "./visible-trigger.mts";

test("UI-CDP-DEADLINE-001: real waitFor borrows only its remaining response budget and fences all pending commands", async t => {
  t.mock.timers.enable({ apis: ["setTimeout", "Date"], now: 1000 });
  const owner = createHarnessFixture(); owner.socket.hold("Runtime.evaluate"); owner.socket.hold("Input.dispatchKeyEvent");
  let settled = 0;
  const waiting = owner.harness.waitFor("private expression must not be serialized", Boolean, "owned predicate", 25)
    .then(() => { settled++; return undefined; }, error => { settled++; return error; });
  const key = owner.harness.pressNativeKey("Enter").catch(error => error);
  const command = await owner.socket.waitForCommand("Runtime.evaluate");
  await owner.socket.waitForCommand("Input.dispatchKeyEvent");
  t.mock.timers.tick(26);
  const error = await waiting;
  assert.ok(error instanceof BrowserCommandDeadlineError); assert.equal(error.method, "Runtime.evaluate"); assert.equal(error.timeoutMs, 25);
  assert.equal(error.command, command.id); assert.equal(await key, error);
  assert.equal(error.message.includes("private expression"), false);
  const sent = owner.socket.commandsFor("Input.dispatchKeyEvent").length;
  owner.socket.respond(command, { result: { result: { value: true } } }); owner.socket.close();
  await assert.rejects(owner.harness.pressNativeKey("Enter"), failure => failure === error);
  assert.equal(owner.socket.commandsFor("Input.dispatchKeyEvent").length, sent); assert.equal(settled, 1);
  assert.equal(Reflect.get(owner.harness, "pendingCommands").size, 0);
  await owner.harness.close(); assert.equal(existsSync(owner.profile), false);
});

test("UI-CDP-DEADLINE-002: close rejects a pending action immediately, shares one cleanup completion and blocks late sends", async t => {
  t.mock.timers.enable({ apis: ["setTimeout"] });
  const owner = createHarnessFixture(); owner.socket.hold("Runtime.evaluate"); owner.socket.hold("Browser.close");
  const pending = owner.harness.evaluate("true").catch(error => error);
  const command = await owner.socket.waitForCommand("Runtime.evaluate");
  const first = owner.harness.close(), second = owner.harness.close(); assert.equal(first, second);
  await owner.socket.waitForCommand("Browser.close");
  assert.match(String(await pending), /Browser harness closed/);
  await assert.rejects(owner.harness.pressNativeKey("Enter"), /closed/);
  assert.equal(owner.socket.commandsFor("Input.dispatchKeyEvent").length, 0);
  owner.socket.respond(command, { result: { result: { value: true } } });
  t.mock.timers.tick(3001); await first; await second;
  assert.equal(owner.socket.commandsFor("Browser.close").length, 1); assert.equal(existsSync(owner.profile), false);
  assert.equal(Reflect.get(owner.harness, "pendingCommands").size, 0);
  assert.equal(owner.harness.commandDeadlineFailure, undefined, "missing close ACK keeps the original owned-cleanup fallback contract");
});

test("UI-CDP-DEADLINE-003: default unexpected dialog fails without accepting or importing G3 policy", async () => {
  const owner = createHarnessFixture(); owner.socket.hold("Runtime.evaluate");
  const pending = owner.harness.evaluate("true").catch(error => error);
  await owner.socket.waitForCommand("Runtime.evaluate");
  owner.socket.emitEvent("Page.javascriptDialogOpening", { type: "confirm", message: "private dialog value" });
  assert.match(String(await pending), /UNEXPECTED_JAVASCRIPT_DIALOG/);
  assert.equal(owner.socket.commandsFor("Page.handleJavaScriptDialog").length, 0);
  await assert.rejects(owner.harness.runBoundedOperation({ phase: "read-state", mode: "precheck", timeoutMs: 1000, url: "http://127.0.0.1:1234/admin/streams/" }, () => {}, async () => true), /idle owned headed target/);
  await owner.harness.close(); assert.equal(existsSync(owner.profile), false);
});

type MainFault = "Runtime.evaluate" | "Page.navigate" | "restoration" | "deadline-writer" | "late-during-close";
async function actualMainFault(t: TestContext, fault: MainFault) {
  const root = mkdtempSync(resolve(tmpdir(), "autostream-command-evidence-")), output = resolve(root, "output");
  const web = fileURLToPath(new URL("../..", import.meta.url)), repository = resolve(web, "..");
  const sha = execFileSync("git", ["rev-parse", "HEAD"], { cwd: repository, encoding: "utf8" }).trim();
  const planned = fault === "restoration" ? conditions.filter(c => c.family === "stream-create-edit" && c.exercise === "long-text").slice(0, 2)
    : selectedConditions("stream-create-edit").slice(45, 47);
  assert.equal(planned.length, 2);
  const held = fault === "Page.navigate" ? fault : "Runtime.evaluate";
  const owner = createHarnessFixture(); owner.socket.hold(held);
  let launches = 0, closes = 0, serverCloses = 0, settles = 0, writesAfterFailure = 0;
  const primary = Error("original assertion identity"), writer = Error("deadline evidence writer identity");
  const close = owner.harness.close.bind(owner.harness);
  let releaseClose!: () => void, enteredClose!: () => void;
  const closeGate = new Promise<void>(done => { releaseClose = done; });
  const closeEntered = new Promise<void>(done => { enteredClose = done; });
  owner.harness.close = async () => { closes++; enteredClose(); if (fault === "late-during-close") await closeGate; return close(); };
  const source = readFileSync(new URL("./run-browser.mts", import.meta.url), "utf8");
  const file = ts.createSourceFile("runner.mts", source, ts.ScriptTarget.Latest, true, ts.ScriptKind.TS);
  const declaration = file.statements.find((n): n is ts.FunctionDeclaration => ts.isFunctionDeclaration(n) && n.name?.text === "main"); assert.ok(declaration);
  const dependencies: Record<string, unknown> = {
    assert, repository, web, resolve, execFileSync, ownedOutput, mkdirSync,
    // Controlled unit collaborator, not an OS/CI identity for a real browser run.
    process: { env: { CI: "true", GITHUB_SHA: sha, RUNNER_TEMP: root, AUTOSTREAM_UI_REGRESSION_OUTPUT: output }, platform: "linux", argv: ["node", "actual-main-unit", "stream-create-edit"] },
    selectedConditions: () => planned, inventory, conditions, nativeZoomEvidence, assertExecution,
    readFileSync: () => JSON.stringify({ commit: sha }),
    writeFileSync: (path: string, data: string, options: { flag: string }) => {
      assert.equal(options.flag, "wx");
      if (fault === "deadline-writer" && basename(path) === planned[0].id + ".command-deadline.json") throw writer;
      writesAfterFailure++; return writeFileSync(path, data, options);
    },
    staticExportServer: async () => ({ baseURL: "http://ui.test", close: async () => { serverCloses++; } }),
    createConditionRunner: (baseURL: string, write: (name: string, value: unknown) => void) => createConditionRunner(baseURL, write, async () => { launches++; return owner.harness; }),
    DraftRestorationFailure, ConditionOutputFailure, conditionFailureStatus, writeConditionFailure, navigateDocument,
  };
  if (fault === "restoration") {
    // Only surrounding collaborators are controlled; the held restore uses the
    // real evaluate -> sendCommand, and main retains both exact thrown objects.
    owner.harness.waitFor = async <T,>() => undefined as T;
    owner.harness.waitForResponseCount = async () => {};
    Object.assign(dependencies, {
      navigateDocument: async () => {}, settleRender: async () => {}, paint: async () => {},
      closeOverlay: async () => {}, clickNamed: async () => {},
      driveFormContent: async () => async () => { await owner.harness.evaluate("private restore expression"); },
      exerciseAccessibility: async () => { throw primary; },
    });
  }
  const main = actualCallback("const selected=" + declaration.getText(file).replace(/^export /, ""), "selected", dependencies);
  t.mock.timers.enable({ apis: ["setTimeout", "Date"], now: 1000 });
  let caught: unknown;
  const action = main().then(() => { settles++; }, (error: unknown) => { settles++; caught = error; });
  try {
    const command = await owner.socket.waitForCommand(held);
    t.mock.timers.tick(commandTimeoutMs(held) + 1);
    if (fault === "late-during-close") {
      await closeEntered; assert.equal(settles, 0); assert.equal(launches, 1);
      assert.equal(existsSync(resolve(output, "execution.json")), false, "main cannot finish while cleanup is still pending");
      assert.equal(existsSync(resolve(output, planned[0].id + ".command-deadline.json")), true, "original deadline is durable before cleanup");
      owner.socket.respond(command, { result: { result: { value: true } } }); owner.socket.close();
      assert.equal(Reflect.get(owner.harness, "pendingCommands").size, 0); releaseClose();
    }
    await action;
    assert.ok(caught instanceof ConditionFailure); assert.equal(caught.stage, "exercise"); assert.equal(caught.stop, true);
    const deadline = owner.harness.commandDeadlineFailure; assert.ok(deadline instanceof BrowserCommandDeadlineError); assert.equal(deadline.method, held);
    if (fault === "restoration") {
      assert.ok(caught.cause instanceof DraftRestorationFailure); assert.equal(caught.cause.primary, primary); assert.equal(caught.cause.restoration, deadline);
    } else assert.equal(caught.cause, deadline);
    if (fault === "deadline-writer") assert.ok(caught.cleanup.includes(writer));
    const records = () => new Map(readdirSync(output).map(name => [name, readFileSync(resolve(output, name), "utf8")]));
    const before = records(), execution = JSON.parse(before.get("execution.json")!);
    assert.deepEqual(execution.results.map((r: { status: string }) => r.status), ["FAIL", "NOT_REACHED"]);
    if (fault === "restoration") assert.equal(execution.results[0].error, primary.message);
    assert.equal(JSON.parse(before.get(planned[0].id + ".lifecycle.json")!).completed, false);
    if (fault !== "deadline-writer") assert.deepEqual(JSON.parse(before.get(planned[0].id + ".command-deadline.json")!), { schemaVersion: 1, stage: "exercise", method: held, command: command.id, timeoutMs: commandTimeoutMs(held) });
    const priorWrites = writesAfterFailure;
    owner.socket.respond(command, { result: { result: { value: true } } });
    owner.socket.respond(command, { error: { message: "late error" } }); owner.socket.close(); await turn();
    assert.deepEqual(records(), before); assert.equal(writesAfterFailure, priorWrites); assert.equal(settles, 1);
    assert.equal(launches, 1); assert.equal(closes, 1); assert.equal(serverCloses, 1); assert.equal(existsSync(owner.profile), false);
    assert.equal(Reflect.get(owner.harness, "pendingCommands").size, 0);
  } finally {
    if (!settles) { releaseClose(); owner.socket.close(); await action; }
    t.mock.timers.reset();
    assert.equal(resolve(root, ".."), resolve(tmpdir())); assert.ok(basename(root).startsWith("autostream-command-evidence-"));
    rmSync(root, { recursive: true });
  }
}
for (const fault of ["Runtime.evaluate", "Page.navigate", "restoration", "deadline-writer", "late-during-close"] as const) {
  test("UI-CDP-DEADLINE-004-" + fault + ": actual main callback converges, retains causes and stops with NOT_REACHED", t => actualMainFault(t, fault));
}

test("UI-CDP-DEADLINE-005: normal replies cancel response timers and the explicit G3 user-operation budget is retained", async t => {
  t.mock.timers.enable({ apis: ["setTimeout"] });
  const owner = createHarnessFixture();
  await owner.harness.browserVersion(); t.mock.timers.tick(20_001); owner.harness.assertNoFatalError();
  await assert.rejects(owner.harness.runBoundedOperation({ phase: "read-state", mode: "precheck", timeoutMs: 1000, url: "http://127.0.0.1:1234/admin/streams/" }, () => {}, async () => true), /idle owned headed target/);
  Reflect.set(owner.harness, "manualHeaded", true); owner.socket.hold("Runtime.evaluate");
  const records: unknown[] = []; let settled = false;
  const operation = owner.harness.runBoundedOperation({ phase: "read-state", mode: "observe-user", timeoutMs: 60_000, url: "http://127.0.0.1:1234/admin/streams/" }, row => records.push(row), async () => owner.harness.evaluate("true"))
    .then(value => { settled = true; return value; });
  const command = await owner.socket.waitForCommand("Runtime.evaluate"); t.mock.timers.tick(10_001); await turn();
  assert.equal(settled, false); assert.equal(owner.harness.commandDeadlineFailure, undefined);
  owner.socket.respond(command, { result: { result: { value: true } } }); assert.equal(await operation, true);
  assert.ok(records.length > 0); assert.equal(owner.socket.commandsFor("Page.handleJavaScriptDialog").length, 0);
  await owner.harness.close(); assert.equal(existsSync(owner.profile), false);
});

for (const primaryFails of [false, true]) {
  test("UI-CDP-DEADLINE-006-" + primaryFails + ": first deadline during real required-response cleanup is saved without masking the original failure", async t => {
    t.mock.timers.enable({ apis: ["setTimeout", "Date"], now: 1000 });
    const owner = createHarnessFixture(); owner.socket.hold("Fetch.fulfillRequest");
    const files = new Map<string, unknown>(); let deadlineWrites = 0, launches = 0;
    const condition = { ...conditions[0], state: "initial-loading" as const }, surface = inventory.surfaces[0];
    const primary = Error("original body assertion");
    const run = createConditionRunner("http://ui.test", (name, value) => {
      assert.equal(files.has(name), false, "exclusive output"); files.set(name, value);
      if (name.endsWith(".command-deadline.json")) deadlineWrites++;
    }, async () => { launches++; return owner.harness; });
    const action = run(condition, surface, async browser => {
      owner.socket.emitEvent("Fetch.requestPaused", { requestId: "owned-required", resourceType: "XHR", frameId: "main", request: { method: "GET", url: "http://ui.test" + surface.primary } });
      await browser.waitForRequestCount(surface.primary, 1);
      if (primaryFails) throw primary;
    }).catch(error => error);
    const command = await owner.socket.waitForCommand("Fetch.fulfillRequest"); await turn();
    t.mock.timers.tick(10_001);
    const failure = await action; assert.ok(failure instanceof ConditionFailure); assert.equal(failure.stop, true);
    const deadline = owner.harness.commandDeadlineFailure; assert.ok(deadline instanceof BrowserCommandDeadlineError);
    if (primaryFails) { assert.equal(failure.cause, primary); assert.equal(failure.stage, "exercise"); }
    else { assert.equal(failure.stage, "settlement"); assert.equal(failure.cause, deadline); }
    assert.ok(failure.cleanup.includes(deadline)); assert.equal(deadlineWrites, 1);
    assert.deepEqual(files.get(condition.id + ".command-deadline.json"), { schemaVersion: 1, stage: "settlement", method: "Fetch.fulfillRequest", command: command.id, timeoutMs: 10_000 });
    assert.equal((files.get(condition.id + ".lifecycle.json") as { completed: boolean }).completed, false);
    owner.socket.respond(command, { result: {} }); owner.socket.close(); await turn(); assert.equal(deadlineWrites, 1);
    await assert.rejects(run({ ...condition, id: conditions[1].id }, surface, async () => {}), /stopped/);
    assert.equal(launches, 1); assert.equal(existsSync(owner.profile), false);
  });
}

// Real Form -> clickNamed -> clickVisible -> clickTarget -> BrowserHarness.
// Only transport replies and surrounding render/fixture collaborators are
// controlled. No trigger helper or its native input methods are substituted.
async function actualTriggerMainFault(t: TestContext, mode: "dual" | "body" | "cleanup" | "writer" | "diagnostic-writer" | "worker") {
  const root = mkdtempSync(resolve(tmpdir(), "autostream-trigger-evidence-")), output = resolve(root, "output");
  const web = fileURLToPath(new URL("../..", import.meta.url)), repository = resolve(web, "..");
  const sha = execFileSync("git", ["rev-parse", "HEAD"], { cwd: repository, encoding: "utf8" }).trim();
  const family = mode === "worker" ? "workers" : "stream-create-edit";
  const planned = mode === "worker" ? conditions.filter(c => c.family === family && c.exercise === "Confirmation").slice(0, 2)
    : mode === "body" ? selectedConditions(family).filter(c => c.exercise === "Form").slice(0, 2) : selectedConditions(family).slice(45, 47);
  assert.equal(planned.length, 2);
  const owners: ReturnType<typeof createHarnessFixture>[] = [];
  const primary: unknown[] = [], cleanupCommands: { id: number; method: string }[] = [];
  let enteredCleanup!: () => void, serverCloses = 0, deadlineWrites = 0;
  const cleanupEntered = new Promise<void>(done => { enteredCleanup = done; });
  const writer = Error("trigger failure writer identity");
  const originalOK = assert.ok;
  t.mock.method(assert, "ok", (...args: Parameters<typeof assert.ok>) => {
    try { return originalOK(...args); }
    catch (error) { if (args[1] === "native input needs the measured target point") primary.push(error); throw error; }
  });
  const launch = async () => {
    const owner = createHarnessFixture(); owners.push(owner); owner.socket.hold("Runtime.evaluate");
    // Routing is outside this fault: the real Form callback's response barrier
    // is already satisfied by the controlled fixture, before the trigger path.
    owner.harness.waitForResponseCount = async () => {};
    const send = owner.socket.send.bind(owner.socket); let dialogs = 0;
    owner.socket.send = raw => {
      const command = JSON.parse(raw) as { id: number; method: string; params: { expression?: string } };
      send(raw); if (command.method !== "Runtime.evaluate") return;
      const expression = command.params.expression || "";
      if (expression.startsWith("if(globalThis.__uiTriggerSettlement)cancelAnimationFrame")) {
        cleanupCommands.push(command); enteredCleanup();
        if (mode !== "body") return;
      }
      let value: unknown = true;
      if (expression.includes("document.querySelector('main h1')")) value = "Streams";
      else if (expression === "document.querySelectorAll('[role=dialog]').length") value = [1, 0, 1][dialogs++] ?? 1;
      else if (expression.includes("const state=globalThis.__uiTriggerSettlement;if(!state)")) {
        value = { x: mode === "cleanup" ? 20 : "unmeasured", y: 20 };
      }
      queueMicrotask(() => owner.socket.respond(command, { result: { result: { value } } }));
    };
    return owner.harness;
  };
  const source = readFileSync(new URL("./run-browser.mts", import.meta.url), "utf8");
  const file = ts.createSourceFile("runner.mts", source, ts.ScriptTarget.Latest, true, ts.ScriptKind.TS);
  const declaration = file.statements.find((n): n is ts.FunctionDeclaration => ts.isFunctionDeclaration(n) && n.name?.text === "main"); assert.ok(declaration);
  const main = actualCallback("const selected=" + declaration.getText(file).replace(/^export /, ""), "selected", {
    assert, repository, web, resolve, execFileSync, ownedOutput, mkdirSync,
    process: { env: { CI: "true", GITHUB_SHA: sha, RUNNER_TEMP: root, AUTOSTREAM_UI_REGRESSION_OUTPUT: output }, platform: "linux", argv: ["node", "controlled-trigger-main", family] },
    selectedConditions: () => planned, inventory, conditions, nativeZoomEvidence, assertExecution,
    readFileSync: () => JSON.stringify({ commit: sha }),
    writeFileSync: (path: string, data: string, options: { flag: string }) => {
      assert.equal(options.flag, "wx");
      if (basename(path).endsWith(".command-deadline.json")) deadlineWrites++;
      if (mode === "writer" && basename(path).endsWith(".failure.json") || mode === "diagnostic-writer" && basename(path).endsWith(".trigger-cleanup.json")) throw writer;
      return writeFileSync(path, data, options);
    },
    staticExportServer: async () => ({ baseURL: "http://ui.test", close: async () => { serverCloses++; } }),
    createConditionRunner: (baseURL: string, write: (name: string, value: unknown) => void) => createConditionRunner(baseURL, write, launch),
    DraftRestorationFailure, ConditionOutputFailure, conditionFailureStatus, writeConditionFailure, navigateDocument,
    clickNamed, clickVisible, clickWorkerRestart, settleRender: async () => {}, closeOverlay: async () => {},
  });
  t.mock.timers.enable({ apis: ["setTimeout", "Date"], now: 1000 });
  let caught: unknown, settled = false;
  const action = main().then(() => { settled = true; }, (error: unknown) => { settled = true; caught = error; });
  try {
    await cleanupEntered;
    if (mode !== "body") t.mock.timers.tick(10_001);
    await action;
    const rows = JSON.parse(readFileSync(resolve(output, "execution.json"), "utf8")).results;
    t.diagnostic(JSON.stringify({ caller: "actual-main", helper: mode === "worker" ? "worker-restart" : "form-clickNamed", bodyFailures: primary.length, cleanupHeld: mode !== "body", deadlineWrites, launches: owners.length, statuses: rows.map((r: { status: string }) => r.status) }));
    if (mode === "body") {
      assert.equal(owners.length, 2, "body-only nonfatal failure must reach the next case");
      assert.deepEqual(rows.map((r: { status: string }) => r.status), ["FAIL", "FAIL"]);
      assert.equal(deadlineWrites, 0); assert.equal(primary.length, 2);
      assert.equal(rows[0].error, (primary[0] as Error).message);
    } else {
      const outputFailed = mode === "writer" || mode === "diagnostic-writer";
      const failure = outputFailed ? (caught as ConditionOutputFailure).original : caught;
      if (outputFailed) { assert.ok(caught instanceof ConditionOutputFailure); assert.equal(caught.output, writer); }
      assert.ok(failure instanceof ConditionFailure); assert.equal(failure.stop, true);
      const deadline = owners[0].harness.commandDeadlineFailure; assert.ok(deadline instanceof BrowserCommandDeadlineError);
      if (mode === "cleanup") { assert.equal(failure.cause, deadline); assert.equal(primary.length, 0); }
      else {
        const composite = failure.cause as { primary: unknown; cleanup: unknown; cause: unknown; phase: string };
        const inner = mode === "worker" ? composite.primary as typeof composite : composite;
        assert.equal(inner.primary, primary[0], "actual assertion E1 identity survives cleanup deadline E2");
        assert.equal(composite.cleanup, deadline);
        assert.equal(inner.cause, primary[0]); assert.equal(inner.cleanup, deadline);
        if (mode === "worker") { assert.equal(composite.cause, inner); assert.equal(composite.phase, "worker-restart"); }
        assert.equal(rows[0].error, (primary[0] as Error).message, "existing error field reports original primary");
      }
      assert.deepEqual(rows.map((r: { status: string }) => r.status), ["FAIL", "NOT_REACHED"]);
      assert.equal(deadlineWrites, 1); assert.equal(owners.length, 1);
      assert.equal(JSON.parse(readFileSync(resolve(output, planned[0].id + ".lifecycle.json"), "utf8")).completed, false);
      if (!["cleanup", "writer", "diagnostic-writer"].includes(mode)) {
        const diagnostic = readFileSync(resolve(output, planned[0].id + ".trigger-cleanup.json"));
        assert.ok(diagnostic.length <= 4096); assert.equal(JSON.parse(diagnostic.toString()).conditionID, planned[0].id);
      }
    }
    assert.equal(serverCloses, 1);
    for (const owner of owners) { assert.equal(existsSync(owner.profile), false); assert.equal(Reflect.get(owner.harness, "pendingCommands").size, 0); }
  } finally {
    for (const owner of owners) owner.socket.close();
    if (!settled) await action;
    t.mock.timers.reset();
    assert.equal(resolve(root, ".."), resolve(tmpdir())); assert.ok(basename(root).startsWith("autostream-trigger-evidence-"));
    rmSync(root, { recursive: true });
  }
}

test("UI-CDP-CLEANUP-001: actual Form preserves E1 when real trigger cleanup reaches E2 deadline", t => actualTriggerMainFault(t, "dual"));
for (const mode of ["body", "cleanup", "writer", "diagnostic-writer", "worker"] as const) {
  test("UI-CDP-CLEANUP-002-" + mode + ": actual main retains helper causes and the existing continuation/output policy", t => actualTriggerMainFault(t, mode));
}

type Trigger = "click" | "worker" | "select";
type HelperFault = { body: boolean; cleanup: "none" | "deadline" | "raw"; rawBody?: { value: unknown }; rawCleanup?: { value: unknown } };
async function realTriggerHelperFault(t: TestContext, trigger: Trigger, fault: HelperFault) {
  const owners: ReturnType<typeof createHarnessFixture>[] = [], files = new Map<string, unknown>();
  let primary: unknown, primaryObserved = false, deadlineWrites = 0, cleanupEntered!: () => void;
  const enteringCleanup = new Promise<void>(done => { cleanupEntered = done; });
  const assertion = trigger === "select" ? "requested select option exists" : "native input needs the measured target point";
  const originalOK = assert.ok;
  t.mock.method(assert, "ok", (...args: Parameters<typeof assert.ok>) => {
    if (!args[0] && args[1] === assertion && fault.rawBody) {
      primary = fault.rawBody.value; primaryObserved = true; throw primary;
    }
    try { return originalOK(...args); }
    catch (error) { if (args[1] === assertion) { primary = error; primaryObserved = true; } throw error; }
  });
  const isCleanup = (expression: string) => expression.startsWith("if(globalThis.__uiTriggerSettlement)cancelAnimationFrame")
    || expression.startsWith("globalThis.__uiWorkerRow?.removeAttribute")
    || expression === "document.querySelector('[data-ui-scenario-target]')?.removeAttribute('data-ui-scenario-target');true";
  const launch = async () => {
    const owner = createHarnessFixture(); owners.push(owner); owner.socket.hold("Runtime.evaluate");
    const send = owner.socket.send.bind(owner.socket);
    owner.socket.send = raw => {
      const command = JSON.parse(raw) as { id: number; method: string; params: { expression?: string } };
      send(raw); if (command.method !== "Runtime.evaluate") return;
      const expression = command.params.expression || "";
      if (isCleanup(expression)) { cleanupEntered(); if (fault.cleanup === "deadline") return; }
      let value: unknown = true;
      if (expression.includes("const state=globalThis.__uiTriggerSettlement;if(!state)")) value = { x: fault.body ? "unmeasured" : 20, y: 20 };
      else if (expression.includes("findIndex(o=>o.value===")) value = fault.body ? -1 : 0;
      else if (expression === "document.querySelector('[data-ui-scenario-target]')?.value") value = "chosen";
      queueMicrotask(() => owner.socket.respond(command, { result: { result: { value } } }));
    };
    if (fault.cleanup === "raw") {
      const evaluate = owner.harness.evaluate.bind(owner.harness);
      // Exact non-Error throw at the cleanup API boundary; all trigger body
      // commands still use the real BrowserHarness and controlled CDP.
      owner.harness.evaluate = async <T,>(expression: string, timeout?: number) => {
        if (isCleanup(expression)) { cleanupEntered(); throw fault.rawCleanup!.value; }
        return evaluate<T>(expression, timeout);
      };
    }
    return owner.harness;
  };
  const family = trigger === "worker" ? "workers" : "stream-create-edit";
  const condition = conditions.find(c => c.family === family && c.state === "ready")!;
  const next = conditions.find(c => c.family === family && c.state === "ready" && c.id !== condition.id)!;
  const surface = inventory.surfaces.find(s => s.id === family)!;
  const run = createConditionRunner("http://ui.test", (name, value) => {
    assert.equal(files.has(name), false); files.set(name, value);
    if (name.endsWith(".command-deadline.json")) deadlineWrites++;
  }, launch);
  t.mock.timers.enable({ apis: ["setTimeout", "Date"], now: 1000 });
  let caught: unknown, settled = false;
  const action = run(condition, surface, async browser => {
    if (trigger === "click") await clickNamed(browser, /^Create stream$/, "main", true);
    else if (trigger === "worker") await clickWorkerRestart(browser, { id: "worker-one", type: "worker", name: "Worker one" });
    else await selectVisible(browser, "main select", "chosen");
  }).then(() => { settled = true; }, error => { settled = true; caught = error; });
  try {
    await enteringCleanup;
    if (fault.cleanup === "deadline") t.mock.timers.tick(10_001);
    await action;
    assert.ok(caught instanceof ConditionFailure); assert.equal(caught.stage, "exercise");
    assert.equal(primaryObserved, fault.body);
    const cleanup = fault.cleanup === "deadline" ? owners[0].harness.commandDeadlineFailure : fault.rawCleanup?.value;
    if (fault.body && fault.cleanup !== "none") {
      assert.ok(caught.cause instanceof VisibleTriggerCleanupFailure);
      const inner = trigger === "worker" ? caught.cause.primary : caught.cause;
      assert.ok(inner instanceof VisibleTriggerCleanupFailure);
      assert.equal(inner.primary, primary); assert.equal(inner.cause, primary); assert.equal(inner.cleanup, cleanup);
      assert.equal(caught.cause.cleanup, cleanup);
      if (trigger === "worker") { assert.equal(caught.cause.phase, "worker-restart"); assert.equal(inner.phase, "click-target"); }
    } else if (trigger === "worker" && fault.cleanup !== "none") {
      assert.ok(caught.cause instanceof VisibleTriggerCleanupFailure);
      assert.equal(caught.cause.phase, "worker-restart"); assert.equal(caught.cause.primary, cleanup); assert.equal(caught.cause.cleanup, cleanup);
    } else assert.equal(caught.cause, fault.body ? primary : cleanup, "a sole throw keeps its exact original identity");
    assert.equal(conditionFailureStatus(condition, caught).error, fault.body ? primary instanceof Error ? primary.message : String(primary) : cleanup instanceof Error ? cleanup.message : String(cleanup));
    assert.equal((files.get(condition.id + ".lifecycle.json") as { completed: boolean }).completed, false);
    if (fault.cleanup === "deadline") {
      assert.ok(cleanup instanceof BrowserCommandDeadlineError); assert.equal(deadlineWrites, 1); assert.equal(caught.stop, true);
      await assert.rejects(run(next, surface, async () => {}), /stopped/); assert.equal(owners.length, 1);
    } else {
      assert.equal(deadlineWrites, 0); assert.equal(caught.stop, false);
      await run(next, surface, async () => {}); assert.equal(owners.length, 2, "nonfatal body failures keep existing continuation");
    }
    for (const owner of owners) { assert.equal(existsSync(owner.profile), false); assert.equal(Reflect.get(owner.harness, "pendingCommands").size, 0); }
  } finally {
    for (const owner of owners) owner.socket.close();
    if (!settled) await action;
    t.mock.timers.reset();
  }
}
for (const trigger of ["click", "worker", "select"] as const) {
  for (const fault of ["body", "cleanup", "dual"] as const) {
    test("UI-CDP-CLEANUP-003-" + trigger + "-" + fault + ": real helper keeps exact body/cleanup and existing stop policy", t => realTriggerHelperFault(t, trigger, { body: fault !== "cleanup", cleanup: fault === "body" ? "none" : "deadline" }));
  }
}
for (const value of [false, 0, "", null, undefined]) {
  test("UI-CDP-CLEANUP-004-" + String(value) + ": falsy raw body survives a real helper finally", t => realTriggerHelperFault(t, "click", { body: true, cleanup: "none", rawBody: { value } }));
}
test("UI-CDP-CLEANUP-005: falsy body and raw cleanup keep both identities", t => realTriggerHelperFault(t, "select", { body: true, cleanup: "raw", rawBody: { value: false }, rawCleanup: { value: { code: "controlled-cleanup" } } }));
test("UI-CDP-CLEANUP-006: cleanup-only raw object is rethrown unchanged", t => realTriggerHelperFault(t, "select", { body: false, cleanup: "raw", rawCleanup: { value: { code: "controlled-cleanup-only" } } }));

test("UI-CDP-CLEANUP-007: known trigger plus draft/output layers retain identities and emit only closed diagnostics", async t => {
  const owner = createHarnessFixture(); owner.socket.hold("Runtime.evaluate");
  const primary = { raw: "controlled-primary" }, cleanup = false, restoration = Error("controlled restoration"), output = Error("controlled output");
  const evaluate = owner.harness.evaluate.bind(owner.harness);
  owner.harness.evaluate = async <T,>(expression: string, timeout?: number) => {
    if (expression.startsWith("if(globalThis.__uiTriggerSettlement)cancelAnimationFrame")) throw cleanup;
    return evaluate<T>(expression, timeout);
  };
  const send = owner.socket.send.bind(owner.socket);
  owner.socket.send = raw => {
    const command = JSON.parse(raw) as { id: number; method: string; params: { expression?: string } }; send(raw);
    if (command.method === "Runtime.evaluate") queueMicrotask(() => owner.socket.respond(command, { result: { result: { value: command.params.expression?.includes("const state=globalThis.__uiTriggerSettlement;if(!state)") ? { x: "invalid", y: 20 } : true } } }));
  };
  const originalOK = assert.ok;
  t.mock.method(assert, "ok", (...args: Parameters<typeof assert.ok>) => { if (!args[0] && args[1] === "native input needs the measured target point") throw primary; return originalOK(...args); });
  let trigger: unknown;
  try { await clickNamed(owner.harness, /^Create stream$/); } catch (error) { trigger = error; }
  try {
    assert.ok(trigger instanceof VisibleTriggerCleanupFailure); assert.equal(trigger.primary, primary); assert.equal(trigger.cleanup, cleanup);
    const draft = new DraftRestorationFailure(true, trigger, restoration), condition = selectedConditions("stream-create-edit")[45];
    const failure = new ConditionFailure("exercise", true, draft, []), files = new Map<string, unknown>();
    assert.throws(() => writeConditionFailure(condition, failure, (name, value) => files.set(name, value)), error => error === failure);
    assert.equal(conditionFailureStatus(condition, failure).error, String(primary));
    for (const suffix of ["draft-restoration.json", "trigger-cleanup.json"]) {
      const json = JSON.stringify(files.get(condition.id + "." + suffix)); assert.ok(Buffer.byteLength(json) <= 4096);
      assert.equal(json.includes("controlled-primary"), false); assert.equal(json.includes("controlled restoration"), false);
      assert.equal(json.includes("stack"), false); assert.equal(json.includes("expression"), false);
    }
    assert.throws(() => writeConditionFailure(condition, failure, () => { throw output; }), error => {
      assert.ok(error instanceof ConditionOutputFailure); assert.equal(error.original, failure); assert.equal(error.output, output);
      assert.equal(failure.cause, draft); assert.equal(draft.primary, trigger); assert.equal(draft.restoration, restoration); return true;
    });
    const cyclic = Error("unrelated wrapper"); cyclic.cause = cyclic;
    assert.equal(conditionFailureStatus(condition, cyclic).error, cyclic.message, "arbitrary cause graphs are not traversed");
  } finally { await owner.harness.close(); assert.equal(existsSync(owner.profile), false); }
});
