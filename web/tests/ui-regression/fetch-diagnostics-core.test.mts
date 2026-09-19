// Current harness core. Original 38-case history suite remains byte-identical at fixed b266.
import assert from "node:assert/strict";
import { existsSync } from "node:fs";
import test, { type TestContext } from "node:test";
import { createHarnessFixture } from "../helpers/browser-cdp-socket-fixture.mts";
import { BrowserHarness } from "../helpers/browser-harness.mts";

const invalid = "Invalid InterceptionId.";
const sentinel = "PRIVATE_SENTINEL_015_" + "s".repeat(20_000);
const phases = ["paint-before-leave", "to-blank", "old-handlers-drain", "phase-reset", "to-product", "required-responses", "capture", "other"];
const newPrefix = "BROWSER_FETCH_FAILURE ";
type Fixture = ReturnType<typeof createHarnessFixture>;
type Case = { timing: "pre-nav" | "late" | "same"; phase?: string; required?: boolean; optionalFulfill?: boolean; error?: string; success?: boolean; params?: Record<string, unknown>; context?: Record<string, unknown>; afterResponse?: boolean; unknownMain?: boolean; saturated?: boolean };

function context(browser: BrowserHarness, value: Record<string, unknown>) {
  // Baseline behavior sequences also run on the unmodified delivered source.
  Reflect.get(browser, "setFetchDiagnosticContext")?.call(browser, value);
}

function paused(socket: Fixture["socket"], params: Record<string, unknown> = {}) {
  socket.emitEvent("Fetch.requestPaused", {
    requestId: "request-015", resourceType: "Document", frameId: "main-015", networkId: "network-015",
    request: { method: "GET", url: "http://fixture.local/optional" }, ...params,
  });
}

function parsed(logs: string[]) {
  const entries = logs.filter((line) => line.startsWith(newPrefix));
  assert.equal(entries.length, 1, "first fatal has exactly one generation diagnostic");
  assert.ok(Buffer.byteLength(entries[0]) <= 4096, "whole diagnostic stderr line is bounded");
  const value = JSON.parse(entries[0].slice(newPrefix.length)).generation_diagnostic;
  assert.ok(value, "additional generation diagnostic is connected to the original first fatal");
  return value;
}

async function runCase(t: TestContext, config: Case) {
  const { harness, socket, profile } = createHarnessFixture();
  const logs: string[] = [];
  const logger = t.mock.method(console, "error", (line: string) => { logs.push(line); });
  const commandMethod = config.required || config.optionalFulfill ? "Fetch.fulfillRequest" : "Fetch.continueRequest";
  socket.hold(commandMethod);
  socket.hold("Page.navigate");
  socket.hold("Runtime.evaluate");
  let release = () => {};
  const ready = new Promise<void>((resolve) => { release = resolve; });
  if (config.required || config.optionalFulfill) harness.setRouteResolver(() => ({ body: {}, requiredResponse: !!config.required, waitUntil: ready }));
  context(harness, { scenario: "account-1440", side: "before", phase: config.phase ?? "to-blank", ...config.context });
  if (!config.unknownMain) socket.emitEvent("Page.frameNavigated", { frame: { id: "main-015" } });
  if (config.saturated) Reflect.set(Reflect.get(harness, "requestLifecycle"), "diagnosticOrder", 999_999);
  let navigation: Promise<unknown> | undefined;
  const begin = () => { navigation = harness.navigate("about:blank").catch((error) => error); };
  try {
    if (config.timing === "late") begin();
    paused(socket, config.params);
    if (config.timing === "pre-nav") begin();
    release();
    const command = await socket.waitForCommand(commandMethod);
    if (config.afterResponse) {
      socket.respond(await socket.waitForCommand("Page.navigate"), { result: {} });
      socket.emitEvent("Page.frameNavigated", { frame: { id: "main-015" } });
      socket.emitEvent("Page.loadEventFired", {});
      await navigation;
      context(harness, { phase: "required-responses" });
    }
    const idle = harness.waitForRequestHandlersIdle().catch((error) => error);
    const pending = harness.evaluate("true").catch((error) => error);
    socket.respond(command, config.success ? { result: {} } : { error: { message: config.error ?? invalid } });
    const cause = await idle;
    if (cause) {
      assert.throws(() => harness.assertNoFatalError(), (error) => error === cause);
      assert.equal(await pending, cause, "pending CDP command keeps first fatal identity");
      if (navigation && !config.afterResponse) assert.equal(await navigation, cause, "navigation waiter keeps first fatal identity");
    } else {
      harness.assertNoFatalError();
      socket.respond(await socket.waitForCommand("Runtime.evaluate"), { result: { result: { value: true } } });
      await pending;
      if (navigation && !config.afterResponse) {
        socket.respond(await socket.waitForCommand("Page.navigate"), { result: {} });
        socket.emitEvent("Page.loadEventFired", {});
        await navigation;
      }
    }
    const outcome = { fatal: !!cause, cancellations: harness.safeFetchCancellationCount, requests: [...harness.requests.values()].reduce((a, b) => a + b, 0), responses: [...harness.responses.values()].reduce((a, b) => a + b, 0), statuses: [...harness.responseStatuses.values()].flat() };
    const stored = Reflect.get(harness, "fetchFailureDiagnosticJSON");
    t.diagnostic(`FETCH_EVIDENCE ${JSON.stringify(logs)}`);
    return { outcome, logs, cause, stored };
  } finally {
    release();
    await harness.close();
    logger.mock.restore();
    assert.equal(existsSync(profile), false, "fixture profile cleanup");
    assert.equal(Reflect.get(harness, "pendingCommands").size, 0);
    assert.equal(Reflect.get(harness, "eventWaiters").pendingCount, 0);
    assert.equal(Reflect.get(harness, "requestLifecycle").activeCount, 0);
  }
}

const behaviorCases: [string, Case, boolean, number][] = [
  ["pre-nav optional continue cancels", { timing: "pre-nav" }, false, 1],
  ["pre-nav optional deferred fulfill cancels", { timing: "pre-nav", optionalFulfill: true }, false, 1],
  ["late paused before navigate response is fatal", { timing: "late" }, true, 0],
  ["old required deferred fulfill is fatal", { timing: "pre-nav", required: true }, true, 0],
  ["other CDP error is fatal", { timing: "pre-nav", error: "Target closed" }, true, 0],
  ["nonexact interception error is fatal", { timing: "pre-nav", error: "Invalid InterceptionId" }, true, 0],
  ["successful continue counts once", { timing: "same", success: true }, false, 0],
  ["successful required fulfill counts status once", { timing: "same", required: true, success: true }, false, 0],
  ...phases.map((phase): [string, Case, boolean, number] => [`same generation stays fatal in ${phase}`, { timing: "same", phase }, true, 0]),
];
test('021 history/current late pause after navigate response preserves unknown ownership and exact fatal cause',async t=>{
 for(const resourceType of ['Script','XHR']){
  const {harness,socket,profile}=createHarnessFixture(),logs:string[]=[];
  const logger=t.mock.method(console,'error',(line:string)=>logs.push(line));
  socket.hold('Page.navigate');socket.hold('Fetch.continueRequest');socket.hold('Runtime.evaluate');socket.autoLoadEvent=false;
  context(harness,{scenario:'archive-1440',side:'before',phase:'to-blank'});socket.emitEvent('Page.frameNavigated',{frame:{id:'main-015'}});
  try{
   const navigation=harness.navigate('about:blank');socket.respond(await socket.waitForCommand('Page.navigate'),{result:{}});await Promise.resolve();
   paused(socket,{resourceType,networkId:undefined});const command=await socket.waitForCommand('Fetch.continueRequest');
   socket.emitEvent('Page.frameNavigated',{frame:{id:'main-015'}});socket.emitEvent('Page.loadEventFired',{});await navigation;
   const idle=harness.waitForRequestHandlersIdle().catch(error=>error),pending=harness.evaluate('true').catch(error=>error);
   socket.respond(command,{error:{message:invalid}});const cause=await idle;
   assert.ok(cause instanceof Error);assert.equal(cause.message,invalid);assert.equal(await pending,cause);assert.throws(()=>harness.assertNoFatalError(),error=>error===cause);
   const value=parsed(logs),failure=JSON.parse(logs[0].slice(newPrefix.length));
   assert.equal(value.origin,'unknown');assert.equal(value.resource.resource_type,resourceType);assert.equal(value.resource.network_id_present,false);
   assert.equal(value.paused.page_navigate_response_received,true);assert.equal(value.paused.top_level_navigation_pending,true);assert.equal(value.failure.top_level_navigation_pending,false);
   assert.ok(value.paused.navigation_begin_order<value.paused.order&&value.paused.order<value.settlement.order&&value.settlement.order<value.failure.order);
   assert.equal(failure.request_generation,failure.current_generation);assert.equal(failure.cancellation_context_present,false);assert.equal(harness.safeFetchCancellationCount,0);
   assert.equal(socket.commandsFor('Fetch.continueRequest').length,1);assert.equal(socket.commandsFor('Fetch.fulfillRequest').length,0);
  }finally{await harness.close();logger.mock.restore();assert.equal(existsSync(profile),false);}
 }
});
for (const [name, config, fatal, cancellations] of behaviorCases) {
  test(`015 behavior: ${name}`, async (t) => {
    const result = await runCase(t, config);
    assert.deepEqual(result.outcome, { fatal, cancellations, requests: 1, responses: config.success ? 1 : 0, statuses: config.success && config.required ? [200] : [] });
    const legacy = result.logs.filter((line) => line.startsWith("BROWSER_FETCH_FAILURE "));
    assert.equal(legacy.length, fatal ? 1 : 0);
    if (fatal) assert.equal(JSON.parse(legacy[0].slice("BROWSER_FETCH_FAILURE ".length)).error_category, (config.error ?? invalid) === invalid ? "invalid_interception_id" : "other");
    t.diagnostic(`BEHAVIOR ${JSON.stringify({ name, ...result.outcome })}`);
  });
}

test("015 diagnostic: late paused records actual send/response ordering with unknown origin", async (t) => {
  const { logs, stored } = await runCase(t, { timing: "late" });
  const diagnostic = parsed(logs);
  assert.equal(diagnostic.origin, "unknown");
  assert.deepEqual(diagnostic.context, { scenario: "account-1440", side: "before" });
  for (const point of ["paused", "settlement", "failure"]) {
    assert.equal(diagnostic[point].phase, "to-blank");
    assert.equal(diagnostic[point].page_navigate_sent, true);
    assert.equal(diagnostic[point].page_navigate_response_received, false);
    assert.equal(diagnostic[point].top_level_navigation_pending, true);
  }
  assert.ok(diagnostic.paused.navigation_begin_order < diagnostic.paused.order);
  assert.ok(diagnostic.paused.order < diagnostic.settlement.order);
  assert.ok(diagnostic.settlement.order < diagnostic.failure.order);
  assert.ok(typeof stored === "string");
  assert.deepEqual(JSON.parse(stored).generation_diagnostic, diagnostic);
});

test("015 diagnostic: registered old required request retains pre-nav and post-nav snapshots", async (t) => {
  const diagnostic = parsed((await runCase(t, { timing: "pre-nav", required: true, afterResponse: true })).logs);
  assert.equal(diagnostic.paused.page_navigate_sent, false);
  assert.ok(diagnostic.paused.order < diagnostic.settlement.navigation_begin_order);
  assert.ok(diagnostic.settlement.navigation_begin_order < diagnostic.settlement.order);
  assert.equal(diagnostic.settlement.page_navigate_response_received, false);
  assert.equal(diagnostic.failure.page_navigate_response_received, true);
  assert.equal(diagnostic.failure.top_level_navigation_pending, false);
  assert.equal(diagnostic.failure.phase, "required-responses");
});

for (const [label, params, expected] of [
  ["known main frame", {}, { resource_type: "Document", frame_id_present: true, main_frame_known: true, matches_main_frame: true, network_id_present: true, request_stage_present: true, response_stage_present: false }],
  ["different frame and response stage", { frameId: "child-015", resourceType: "Fetch", responseStatusCode: 200 }, { resource_type: "Fetch", frame_id_present: true, main_frame_known: true, matches_main_frame: false, network_id_present: true, request_stage_present: true, response_stage_present: true }],
  ["invalid secret labels", { frameId: false, networkId: {}, resourceType: sentinel }, { resource_type: "unknown", frame_id_present: false, main_frame_known: true, matches_main_frame: false, network_id_present: false, request_stage_present: true, response_stage_present: false }],
] as const) {
  test(`015 diagnostic: closed resource metadata / ${label}`, async (t) => {
    const result = await runCase(t, { timing: "same", params: { ...params, requestId: sentinel, request: { method: "GET", url: `http://fixture.local/${sentinel}?token=${sentinel}`, headers: { Cookie: sentinel }, postData: sentinel } }, context: { scenario: sentinel, side: sentinel, phase: sentinel }, error: sentinel });
    const diagnostic = parsed(result.logs);
    assert.deepEqual(diagnostic.resource, expected);
    assert.deepEqual(diagnostic.context, { scenario: "unknown", side: "unknown" });
    assert.equal(diagnostic.paused.phase, "other");
    assert.ok(!result.logs.join("\n").includes("PRIVATE_SENTINEL"));
    assert.ok(typeof result.stored === "string");
    assert.ok(!result.stored.includes("PRIVATE_SENTINEL"));
    assert.equal(result.cause.message, sentinel, "original error is not replaced by its safe diagnostic category");
  });
}

test("015 diagnostic: first fatal only and original error wins when diagnostic collection or stderr fails", async (t) => {
  const { harness, socket, profile } = createHarnessFixture();
  const lifecycle = Reflect.get(harness, "requestLifecycle");
  const logs: string[] = [];
  const logger = t.mock.method(console, "error", (line: string) => { logs.push(line); });
  try {
    socket.hold("Fetch.continueRequest");
    paused(socket);
    paused(socket, { requestId: "second-015" });
    const commands = socket.commandsFor("Fetch.continueRequest");
    const idle = harness.waitForRequestHandlersIdle().catch((error) => error);
    socket.respond(commands[0], { error: { message: invalid } });
    const cause = await idle;
    socket.respond(commands[1], { error: { message: "later failure" } });
    assert.throws(() => harness.assertNoFatalError(), (error) => error === cause);
    parsed(logs);
    assert.equal(logs.filter((line) => line.startsWith("BROWSER_FETCH_FAILURE ")).length, 1);
    assert.equal(lifecycle.safeCancellationCount, 0);
  } finally { await harness.close(); logger.mock.restore(); assert.equal(existsSync(profile), false); }

  for (const failure of ["collection", "stderr"]) {
    const fixture = createHarnessFixture();
    t.after(() => fixture.harness.close());
    const owner = Reflect.get(fixture.harness, "requestLifecycle");
    const method = Reflect.get(owner, "generationFailureDiagnostic");
    assert.equal(typeof method, "function");
    if (failure === "collection") t.mock.method(owner, "generationFailureDiagnostic", () => { throw new Error("diagnostic only"); });
    const brokenLog = t.mock.method(console, "error", () => { if (failure === "stderr") throw new Error("logging only"); });
    try {
      fixture.socket.hold("Fetch.continueRequest");
      paused(fixture.socket);
      const idle = fixture.harness.waitForRequestHandlersIdle().catch((error) => error);
      const pending = fixture.harness.navigate("about:blank").catch((error) => error);
      // Required marking is deliberately done by the real resolver in runCase;
      // this case uses a nonexact error so navigation cannot make it cancellable.
      fixture.socket.respond(await fixture.socket.waitForCommand("Fetch.continueRequest"), { error: { message: "original fatal" } });
      const cause = await idle;
      assert.equal(cause.message, "original fatal");
      await pending;
      assert.throws(() => fixture.harness.assertNoFatalError(), (error) => error === cause);
    } finally { await fixture.harness.close(); brokenLog.mock.restore(); }
  }
});

for (const kind of ["unknown", "duplicate"] as const) {
  test(`015 behavior: ${kind} settlement rejects on the actual harness owner`, async (t) => {
    const { harness, socket } = createHarnessFixture();
    t.mock.method(console, "error", () => {});
    try {
      socket.hold("Fetch.continueRequest");
      if (kind === "duplicate") paused(socket);
      const settle = Reflect.get(harness, "settleFetchRequest").bind(harness);
      await assert.rejects(settle(kind === "unknown" ? "missing" : "request-015", "Fetch.continueRequest", {}), kind === "unknown" ? /Unknown Fetch request ID/ : /Duplicate Fetch settlement attempt/);
      assert.equal(harness.safeFetchCancellationCount, 0);
      assert.equal(harness.responses.size, 0);
      t.diagnostic(`BEHAVIOR ${JSON.stringify({ name: kind, rejected: true, cancellations: 0, responses: 0 })}`);
    } finally { await harness.close(); }
  });
}

test("015 diagnostic: absent main frame, network and resource remain unknown without inferring document origin", async (t) => {
  const result = await runCase(t, { timing: "same", unknownMain: true, params: { resourceType: null, frameId: "unproven-frame", networkId: null } });
  const diagnostic = parsed(result.logs);
  assert.deepEqual(diagnostic.resource, { resource_type: "unknown", frame_id_present: true, main_frame_known: false, matches_main_frame: false, network_id_present: false, request_stage_present: true, response_stage_present: false });
  assert.equal(diagnostic.origin, "unknown");
});

test("015 diagnostic: saturated order is explicitly bounded and is not ordering proof", async (t) => {
  const diagnostic = parsed((await runCase(t, { timing: "late", saturated: true })).logs);
  for (const point of [diagnostic.paused, diagnostic.settlement, diagnostic.failure]) {
    assert.equal(point.order, 1_000_000);
    assert.equal(point.navigation_begin_order, 1_000_000);
    assert.equal(point.order_saturated, true);
  }
});

for (const point of ["registration", "settlement"]) {
  test(`015 diagnostic: ${point} snapshot collection failure preserves original fatal`, async (t) => {
    const { harness, socket } = createHarnessFixture();
    const owner = Reflect.get(harness, "requestLifecycle");
    const snapshot = owner.diagnosticSnapshot.bind(owner);
    let calls = 0;
    t.mock.method(owner, "diagnosticSnapshot", (...args: unknown[]) => {
      calls += 1;
      if (calls === (point === "registration" ? 1 : 2)) throw new Error("diagnostic only");
      return snapshot(...args);
    });
    t.mock.method(console, "error", () => {});
    try {
      socket.hold("Fetch.continueRequest");
      paused(socket);
      const idle = harness.waitForRequestHandlersIdle().catch((error) => error);
      socket.respond(await socket.waitForCommand("Fetch.continueRequest"), { error: { message: invalid } });
      const cause = await idle;
      assert.equal(cause.message, invalid);
      assert.throws(() => harness.assertNoFatalError(), (error) => error === cause);
      assert.equal(harness.responses.size, 0);
      assert.equal(harness.safeFetchCancellationCount, 0);
    } finally { await harness.close(); }
  });
}

for (const invalidRequest of ["missing request body", "duplicate request ID"]) {
  test(`015 behavior: ${invalidRequest} stays fatal through real CDP event受付`, async (t) => {
    const { harness, socket } = createHarnessFixture();
    const logs: string[] = [];
    t.mock.method(console, "error", (line: string) => { logs.push(line); });
    try {
      socket.hold("Page.navigate");
      socket.hold("Fetch.continueRequest");
      const navigation = harness.navigate("about:blank").catch((error) => error);
      if (invalidRequest === "duplicate request ID") paused(socket);
      paused(socket, invalidRequest === "missing request body" ? { request: undefined } : {});
      const cause = await navigation;
      assert.ok(cause instanceof Error);
      assert.throws(() => harness.assertNoFatalError(), (error) => error === cause);
      assert.equal(harness.responses.size, 0);
      assert.equal(harness.safeFetchCancellationCount, 0);
      assert.ok(logs.length <= 1);
      assert.ok(!logs.join("\n").includes("request-015"));
      t.diagnostic(`BEHAVIOR ${JSON.stringify({ name: invalidRequest, fatal: true, responses: 0, cancellations: 0 })}`);
    } finally { await harness.close(); }
  });
}
