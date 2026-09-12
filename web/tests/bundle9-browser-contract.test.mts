import assert from "node:assert/strict";
import { mkdtempSync, readFileSync, rmdirSync, statSync, unlinkSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import test from "node:test";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { runInNewContext } from "node:vm";
import ts from "typescript";
import { BrowserHarness } from "./helpers/browser-harness.mts";
import { FetchRequestLifecycle } from "./helpers/browser-request-lifecycle.mts";
import { createBundle9Fixture } from "./helpers/bundle9-browser-fixtures.mts";
import { assertBundle9CancelledMutations, navigateBundle9Document, retryBundle9Monitoring } from "./helpers/bundle9-browser-scenarios.mts";
import { assertJapaneseFonts, assertOwnedOutput, fontInventory } from "./helpers/run-bundle9-ui-comparison.mts";
import {
  BUNDLE9_BROWSER_BEFORE, apiObservationSummary, assertCaptureInventory,
  assertPixelIdentity, assertSameObservation, assertSourcePair,
  bundle9BrowserSurfaces, bundle9ExpectedCaptureNames, sha256, type APIObservation,
} from "./helpers/bundle9-browser-contract.mts";

test("Bundle 9 browser denominator requires every surface, viewport and interaction", () => {
  assertCaptureInventory(bundle9ExpectedCaptureNames);
  assert.throws(() => assertCaptureInventory([]));
  assert.throws(() => assertCaptureInventory(bundle9ExpectedCaptureNames.slice(1)));
  assert.throws(() => assertCaptureInventory([...bundle9ExpectedCaptureNames, bundle9ExpectedCaptureNames[0]]));
});

test("Bundle 9 API comparison retains method, query, body, response, multiplicity and mutation order", () => {
  const trace: APIObservation[] = [
    { method: "GET", path: "/streams?view=active", body: null, status: 200 },
    { method: "POST", path: "/streams/one/start-readiness", body: { revision: 7 }, status: 200 },
    { method: "POST", path: "/streams/two/start-readiness", body: null, status: 403 },
  ];
  const expected = apiObservationSummary(trace);
  assertSameObservation(expected, apiObservationSummary(structuredClone(trace)));
  for (const mutant of [
    trace.slice(1), [...trace, trace[0]],
    trace.map((row, index) => index === 1 ? { ...row, method: "PUT" } : row),
    trace.map((row, index) => index === 0 ? { ...row, path: "/streams" } : row),
    trace.map((row, index) => index === 1 ? { ...row, body: { revision: 8 } } : row),
    trace.map((row, index) => index === 2 ? { ...row, status: 200 } : row),
    [trace[0], trace[2], trace[1]],
  ]) assert.throws(() => assertSameObservation(expected, apiObservationSummary(mutant)));
});

test("Bundle 9 independent GET scheduling is not a mutation ordering allowance", () => {
  const first = { method: "GET", path: "/auth/me", body: null, status: 200 };
  const second = { method: "GET", path: "/settings/app", body: null, status: 200 };
  assertSameObservation(apiObservationSummary([first, second]), apiObservationSummary([second, first]));
});

test("Bundle 9 visual comparison rejects one changed channel with no threshold or mask", () => {
  const before = { width: 2, height: 1, channels: 4, data: Uint8Array.from([1, 2, 3, 255, 4, 5, 6, 255]) };
  assert.deepEqual(assertPixelIdentity(before, structuredClone(before)), { pixels: 2, differentPixels: 0 });
  for (let channel = 0; channel < before.data.length; channel += 1) {
    const after = structuredClone(before);
    after.data[channel] ^= 1;
    assert.throws(() => assertPixelIdentity(before, after), /strict screenshot pixel difference/);
  }
  assert.throws(() => assertPixelIdentity(before, { ...before, width: 1, height: 2 }));
  assert.throws(() => assertPixelIdentity({ ...before, data: before.data.slice(1) }, before));
});

test("Bundle 9 observation comparison rejects focus, permission, URL and hidden errors", () => {
  const before = { route: "/admin/streams/?view=active#preview", focus: "Start", disabled: false, errors: 0 };
  assertSameObservation(before, structuredClone(before));
  for (const after of [
    { ...before, route: "/admin/streams/" }, { ...before, focus: "body" },
    { ...before, disabled: true }, { ...before, errors: 1 },
  ]) assert.throws(() => assertSameObservation(before, after));
});

test("Bundle 9 source pair rejects refreshed before, stale after and a dummy same-source comparison", () => {
  const current = "a".repeat(40);
  assertSourcePair(BUNDLE9_BROWSER_BEFORE, current, current);
  assert.throws(() => assertSourcePair(current, current, current));
  assert.throws(() => assertSourcePair(BUNDLE9_BROWSER_BEFORE, current, "b".repeat(40)));
  assert.throws(() => assertSourcePair(BUNDLE9_BROWSER_BEFORE, BUNDLE9_BROWSER_BEFORE, BUNDLE9_BROWSER_BEFORE));
});

test("Bundle 9 CDP setup fixes the same locale, timezone and pre-hydration document script", async () => {
  const calls: { method: string; params: unknown }[] = [];
  const fake = { async send(method: string, params: unknown) { calls.push({ method, params }); } };
  await BrowserHarness.prototype.configureDeterministicDocument.call(fake as never, { source: "Date.now = () => 123;", timezone: "Asia/Tokyo", locale: "ja-JP" });
  assert.deepEqual(calls, [
    { method: "Emulation.setTimezoneOverride", params: { timezoneId: "Asia/Tokyo" } },
    { method: "Emulation.setLocaleOverride", params: { locale: "ja-JP" } },
    { method: "Page.addScriptToEvaluateOnNewDocument", params: { source: "Date.now = () => 123;" } },
  ]);
});

test("Bundle 9 screenshot helper captures the entire product document and rejects empty geometry", async () => {
  const calls: { method: string; params: unknown }[] = [];
  let width = 1440;
  const fake = { async send(method: string, params: unknown) {
    calls.push({ method, params });
    return method === "Page.getLayoutMetrics" ? { cssContentSize: { width, height: 1800 } } : { data: Buffer.from("fixture-png").toString("base64") };
  } };
  assert.equal((await BrowserHarness.prototype.captureScreenshot.call(fake as never)).toString(), "fixture-png");
  assert.deepEqual(calls[1], { method: "Page.captureScreenshot", params: { format: "png", fromSurface: true, captureBeyondViewport: true, clip: { x: 0, y: 0, width: 1440, height: 1800, scale: 1 } } });
  width = 0;
  calls.length = 0;
  await assert.rejects(() => BrowserHarness.prototype.captureScreenshot.call(fake as never), /Invalid or unbounded/);
  assert.equal(calls.length, 1, "invalid geometry must not trigger a screenshot");
});

test("Bundle 9 before/after output refuses existing paths and paths outside the owned CI directory", () => {
  const directory = dirname(fileURLToPath(import.meta.url));
  assertOwnedOutput(directory, resolve(directory, "bundle9-output-must-not-exist"));
  assert.throws(() => assertOwnedOutput(directory, directory));
  assert.throws(() => assertOwnedOutput(directory, resolve(directory, "..", "escaped")));
  assert.throws(() => assertOwnedOutput(dirname(directory), directory), /already exists/);
});

const fixtureOrigin = "http://127.0.0.1:4100";

for (const method of ["GET", "HEAD"]) {
  test(`Bundle 9 fixture continues same-origin ${method} admin documents without an API observation`, () => {
    const fixture = createBundle9Fixture(fixtureOrigin);
    for (const path of ["/admin", "/admin/", "/admin?view=active", "/admin/?view=active", "/admin/streams/", "/admin/streams/?view=active"]) {
      assert.equal(fixture.resolver({ method, url: fixtureOrigin + path }), null, path);
    }
    assert.deepEqual(fixture.unexpected, []);
    assert.deepEqual(fixture.trace, []);
  });
}

test("Bundle 9 fixture retains external, prefix, unknown API and non-document-method rejection", () => {
  for (const method of ["GET", "HEAD"]) {
    for (const path of ["/admin", "/admin/", "/admin/?view=active", "/admin/streams/"]) {
      const fixture = createBundle9Fixture(fixtureOrigin);
      const response = fixture.resolver({ method, url: "http://127.0.0.1:4101" + path });
      assert.equal(response?.status, 502);
      assert.equal(response?.requiredResponse, true);
      assert.equal(fixture.unexpected.length, 1);
      assert.deepEqual(fixture.trace, []);
    }
    for (const path of ["/administer", "/administer/streams", "/unknown-api", "/unknown-api?view=active"]) {
      const fixture = createBundle9Fixture(fixtureOrigin);
      const response = fixture.resolver({ method, url: fixtureOrigin + path });
      assert.equal(response?.status, 404);
      assert.equal(response?.requiredResponse, true);
      assert.deepEqual(fixture.unexpected, [`${method} ${path}`]);
      assert.deepEqual(fixture.trace, []);
    }
  }
  for (const method of ["POST", "PUT", "PATCH", "DELETE", "OPTIONS"]) {
    for (const path of ["/admin", "/admin/", "/admin/streams/"]) {
      const fixture = createBundle9Fixture(fixtureOrigin);
      const response = fixture.resolver({ method, url: fixtureOrigin + path });
      assert.equal(response?.status, 404);
      assert.equal(response?.requiredResponse, true);
      assert.deepEqual(fixture.unexpected, [`${method} ${path}`]);
    }
  }
  const fixture = createBundle9Fixture(fixtureOrigin);
  const response = fixture.resolver({ method: "GET", url: fixtureOrigin + "/auth/me" });
  assert.equal(response?.requiredResponse, true);
  assert.deepEqual(fixture.trace, [{ method: "GET", path: "/auth/me", body: null, status: 200 }]);
  assert.deepEqual(fixture.unexpected, []);
});

test("Bundle 9 navigation waits for the previous required response ack before paint, blank and reset", async () => {
  const boundary = navigationBoundary();
  const ack = boundary.pendingResponse("previous-auth", "/auth/me");
  const navigation = navigateBundle9Document(boundary.browser, boundary.fixture, fixtureOrigin + "/admin/");
  await flushTasks();
  assert.deepEqual(boundary.events, []);
  assert.equal(boundary.fixture.trace.length, 1);
  ack();
  await flushTasks();
  assert.deepEqual(boundary.events, ["paint"]);
  boundary.paint.resolve();
  await flushTasks();
  assert.deepEqual(boundary.events, ["paint", "blank"]);
  assert.equal(boundary.fixture.trace.length, 1, "blank acknowledgement must precede resets");
  boundary.blank.resolve();
  await flushTasks();
  assert.deepEqual(boundary.events, ["paint", "blank", "fixture-reset", "response-reset", "navigation-reset", "console-reset", "document"]);
  assert.deepEqual(boundary.fixture.trace, []);
  boundary.document.resolve();
  await navigation;
});

test("Bundle 9 viewport paint observes late requests before accepting handler idle", async () => {
  const boundary = navigationBoundary();
  await boundary.browser.setViewport(390, 844);
  const navigation = navigateBundle9Document(boundary.browser, boundary.fixture, fixtureOrigin + "/admin/", () => { boundary.events.push("fixture-state"); });
  await flushTasks();
  assert.deepEqual(boundary.events, ["viewport", "paint"]);
  const ack = boundary.pendingResponse("viewport-request", "/settings/app");
  boundary.paint.resolve();
  await flushTasks();
  assert.deepEqual(boundary.events, ["viewport", "paint"], "empty idle before paint cannot prove a later request settled");
  ack();
  await flushTasks();
  assert.deepEqual(boundary.events, ["viewport", "paint", "blank"]);
  boundary.blank.resolve();
  boundary.document.resolve();
  await navigation;
  assert.deepEqual(boundary.events.slice(3), ["fixture-state", "fixture-reset", "response-reset", "navigation-reset", "console-reset", "document"]);
});

for (const failureMode of ["paint", "fatal"] as const) {
  test(`Bundle 9 navigation propagates the original ${failureMode} failure without starting the next phase`, async () => {
    const boundary = navigationBoundary();
    const original = new Error(failureMode === "fatal" ? "Invalid InterceptionId." : "controlled paint rejection");
    if (failureMode === "fatal") boundary.pendingResponse("pending-auth", "/auth/me");
    const navigation = navigateBundle9Document(boundary.browser, boundary.fixture, fixtureOrigin + "/admin/");
    const rejected = assert.rejects(navigation, (error) => error === original);
    await flushTasks();
    if (failureMode === "fatal") boundary.lifecycle.fail(original);
    else boundary.paint.reject(original);
    await rejected;
    assert.equal(boundary.events.includes("blank"), false);
    assert.equal(boundary.events.includes("fixture-reset"), false);
    assert.equal(boundary.fixture.trace.length, 1);
    if (failureMode === "fatal") assert.throws(() => boundary.browser.assertNoFatalError(), (error) => error === original);
  });
}

// Compile the actual caller expressions from captureBundle9Source. This runs
// their await/control flow without launching a browser or modelling a new caller.
const scenariosSource = ts.createSourceFile("bundle9-browser-scenarios.mts", readFileSync(new URL("./helpers/bundle9-browser-scenarios.mts", import.meta.url), "utf8"), ts.ScriptTarget.Latest, true, ts.ScriptKind.TS);
const captureSource = findSourceNode(scenariosSource, (node) => ts.isFunctionDeclaration(node) && node.name?.text === "captureBundle9Source");
const paintSource = findSourceNode(scenariosSource, (node) => ts.isFunctionDeclaration(node) && node.name?.text === "paintBarrier");
const actualPaintBarrier = sourceFunction(paintSource.getText(scenariosSource), {});

for (const callerName of ["navigate", "archive-permission-denied"]) {
  test(`Bundle 9 actual capture ${callerName} caller awaits paint, settlement and next-document completion`, async () => {
    const boundary = navigationBoundary();
    const callerNode = callerName === "navigate"
      ? findSourceNode(captureSource, (node) => ts.isVariableDeclaration(node) && ts.isIdentifier(node.name) && node.name.text === "navigate") as ts.VariableDeclaration
      : findSourceNode(captureSource, (node) => ts.isCallExpression(node) && ts.isIdentifier(node.expression) && node.expression.text === "scenario" && ts.isStringLiteral(node.arguments[0]) && node.arguments[0].text === callerName) as ts.CallExpression;
    const expression = ts.isVariableDeclaration(callerNode) ? callerNode.initializer! : callerNode.arguments[1];
    const caller = sourceFunction(expression.getText(scenariosSource), {
      browser: boundary.browser, fixture: boundary.fixture, baseURL: fixtureOrigin,
      navigateBundle9Document, bundle9BrowserSurfaces, paintBarrier: actualPaintBarrier,
    });
    const operation = caller("streams");
    const rejected = assert.rejects(operation, (error) => error === boundary.nextObservation);
    await flushTasks();
    assert.deepEqual(boundary.events, ["paint"]);
    assert.deepEqual(boundary.fixture.state.permissions, ["*"]);
    const ack = boundary.pendingResponse("old-page", "/settings/app");
    boundary.paint.resolve();
    await flushTasks();
    assert.deepEqual(boundary.events, ["paint"]);
    ack();
    await flushTasks();
    assert.deepEqual(boundary.events, ["paint", "blank"]);
    boundary.blank.resolve();
    await flushTasks();
    assert.equal(boundary.events.includes("observe-next"), false, "caller observed a document before awaited navigation completed");
    assert.equal(boundary.events.filter((event) => event === "fixture-reset").length, 1);
    if (callerName === "archive-permission-denied") assert.deepEqual(Array.from(boundary.fixture.state.permissions), ["archive_profiles.read"]);
    boundary.document.resolve();
    await rejected;
    assert.equal(boundary.events.filter((event) => event === "document").length, 1);
  });
}

test("Bundle 9 capture awaits every actual navigation call, including both shared-helper callers", () => {
  const calls: ts.CallExpression[] = [];
  const visit = (node: ts.Node) => {
    if (ts.isCallExpression(node) && ts.isIdentifier(node.expression) && ["navigate", "navigateBundle9Document"].includes(node.expression.text)) calls.push(node);
    ts.forEachChild(node, visit);
  };
  visit(captureSource);
  assert.equal(calls.filter((call) => (call.expression as ts.Identifier).text === "navigateBundle9Document").length, 2);
  assert.ok(calls.length > 2);
  for (const call of calls) assert.ok(ts.isAwaitExpression(call.parent), call.getText(scenariosSource));
});

test("Bundle 9 ordering oracle detects omitted await, post-navigation paint and empty-idle-only mutants", async () => {
  const helper = findSourceNode(scenariosSource, (node) => ts.isFunctionDeclaration(node) && node.name?.text === "navigateBundle9Document").getText(scenariosSource);
  const mutants = [
    helper.replace("await paintBarrier(browser);", "void paintBarrier(browser);"),
    helper.replace("await paintBarrier(browser);", "").replace('await browser.navigate("about:blank");', 'await browser.navigate("about:blank"); await paintBarrier(browser);'),
    helper.replace("await paintBarrier(browser);", "await browser.waitForRequestHandlersIdle();"),
  ];
  for (const mutant of mutants) {
    assert.notEqual(mutant, helper);
    const boundary = navigationBoundary();
    const navigate = sourceFunction(mutant.replace(/^export /, ""), { paintBarrier: actualPaintBarrier });
    const operation = navigate(boundary.browser, boundary.fixture, fixtureOrigin + "/admin/");
    await flushTasks();
    assert.equal(boundary.events.includes("blank"), true, "mutant escaped the ordering oracle");
    assert.throws(() => assert.deepEqual(boundary.events, ["paint"]));
    boundary.paint.resolve(); boundary.blank.resolve(); boundary.document.resolve();
    await operation;
  }
});

function deferred() {
  let resolve!: () => void;
  let reject!: (error: Error) => void;
  const promise = new Promise<void>((done, fail) => { resolve = done; reject = fail; });
  return { promise, resolve, reject };
}
async function flushTasks() { await new Promise<void>((done) => setImmediate(done)); }

function navigationBoundary() {
  const lifecycle = new FetchRequestLifecycle();
  const fixture = createBundle9Fixture(fixtureOrigin);
  fixture.resolver({ method: "GET", url: fixtureOrigin + "/auth/me" });
  const events: string[] = [];
  const reset = fixture.resetTrace;
  fixture.resetTrace = () => { events.push("fixture-reset"); reset(); };
  const paint = deferred(); const blank = deferred(); const document = deferred();
  const nextObservation = new Error("controlled next-document observation");
  const browser = {
    requestLifecycle: lifecycle,
    waitForRequestHandlersIdle: BrowserHarness.prototype.waitForRequestHandlersIdle,
    assertNoFatalError: BrowserHarness.prototype.assertNoFatalError,
    setViewport: BrowserHarness.prototype.setViewport,
    async send(method: string) { assert.equal(method, "Emulation.setDeviceMetricsOverride"); events.push("viewport"); return {}; },
    async evaluate(expression: string) { assert.ok(expression.includes("document.fonts.ready")); events.push("paint"); await paint.promise; return true; },
    async navigate(url: string) { const isBlank = url === "about:blank"; events.push(isBlank ? "blank" : "document"); await (isBlank ? blank.promise : document.promise); },
    clearRequestCounts() { events.push("response-reset"); },
    clearNavigationCount() { events.push("navigation-reset"); },
    clearConsoleErrors() { events.push("console-reset"); },
    async waitFor() { events.push("observe-next"); throw nextObservation; },
    async waitForResponseCount() { events.push("observe-next"); throw nextObservation; },
  } as unknown as BrowserHarness;
  return { browser, fixture, lifecycle, events, paint, blank, document, nextObservation,
    pendingResponse(requestId: string, pathname: string) {
      lifecycle.register({ requestId, pathname, method: "GET", requiredResponse: true });
      const attempt = lifecycle.beginSettlement(requestId, "Fetch.fulfillRequest");
      return () => lifecycle.completeSettlement(attempt);
    },
  };
}

function findSourceNode(root: ts.Node, predicate: (node: ts.Node) => boolean): ts.Node {
  let found: ts.Node | undefined;
  const visit = (node: ts.Node) => { if (!found && predicate(node)) found = node; if (!found) ts.forEachChild(node, visit); };
  visit(root);
  assert.ok(found, "actual Bundle 9 caller/helper source must exist");
  return found;
}
function sourceFunction(source: string, bindings: Record<string, unknown>) {
  return runInNewContext(ts.transpile(`(${source})`, { target: ts.ScriptTarget.ES2023, module: ts.ModuleKind.CommonJS }), bindings) as (...args: unknown[]) => Promise<void>;
}

const sessionRefresh: APIObservation = { method: "POST", path: "/auth/session/refresh", body: null, status: 200 };
const invalidCancelledMutations: APIObservation[][] = [
  [], [sessionRefresh, sessionRefresh],
  ...[{ method: "PUT" }, { path: "/auth/session/refresh?extra=1" }, { path: "/auth/other" }, { body: {} }, { status: 500 }].map((change) => [{ ...sessionRefresh, ...change }]),
  [sessionRefresh, { method: "POST", path: "/system-updates/updaters/host-agent-main/settings", body: { enabled: false }, status: 200 }],
];

test("Bundle 9 cancelled dialogs require exactly the observed session refresh and retain it in the full API comparison", () => {
  const trace = [{ method: "GET", path: "/auth/me", body: null, status: 200 }, sessionRefresh];
  const before = structuredClone(trace);
  assertBundle9CancelledMutations(trace);
  assert.deepEqual(trace, before);
  assert.throws(() => assertSameObservation(apiObservationSummary(trace), apiObservationSummary(trace.slice(0, 1))));
  for (const mutation of invalidCancelledMutations) assert.throws(() => assertBundle9CancelledMutations(mutation));
});

function scenarioSource(name: string) {
  const call = findSourceNode(captureSource, (node) => ts.isCallExpression(node) && ts.isIdentifier(node.expression) && node.expression.text === "scenario" && ts.isStringLiteral(node.arguments[0]) && node.arguments[0].text === name) as ts.CallExpression;
  return call.arguments[1].getText(scenariosSource);
}

for (const name of ["resources-editor", "updater-settings"]) {
  test(`Bundle 9 actual ${name} caller checks session-only mutations after closing and returning focus`, async () => {
    for (const mutations of [[sessionRefresh], ...invalidCancelledMutations]) {
      const events: string[] = [];
      const fixture = { trace: mutations };
      let assertions = 0;
      const caller = sourceFunction(scenarioSource(name), {
        fixture, navigate: async () => {}, rememberFocusTarget: async () => {},
        browser: { clickSelector: async () => {}, pressNativeKey: async (key: string) => { assert.equal(key, "Escape"); }, waitForResponseCount: async () => {}, waitFor: async () => {} },
        waitForDialog: async (_browser: unknown, open: boolean) => { events.push(open ? "open" : "closed"); },
        waitForExactFocus: async () => { events.push("focus"); }, record: async (capture: string) => { events.push(capture); },
        assertBundle9CancelledMutations: (trace: APIObservation[]) => {
          assertions += 1; assert.equal(trace, fixture.trace);
          assert.deepEqual(events.slice(-2), ["closed", "focus"]);
          assertBundle9CancelledMutations(trace);
        },
      });
      if (mutations.length === 1 && mutations[0] === sessionRefresh) {
        await caller();
        assert.equal(events.at(-1), name === "resources-editor" ? "resources-cancel-focus" : "updater-cancel-focus");
      } else await assert.rejects(caller());
      assert.equal(assertions, 1, "the actual cancellation caller must invoke the exact assertion");
    }
  });
}

const monitoringPaths = ["/service-health", "/observability/incidents", "/observability/diagnostics", "/streams"];
function monitoringBoundary() {
  const enabled = deferred(); const errorRecorded = deferred(); const responsesCompleted = deferred(); const recovered = deferred();
  const events: string[] = [];
  const attributes = new Map<string, string>();
  const button = { textContent: "再試行", disabled: true, visible: true,
    getClientRects() { return this.visible ? [{}] : []; },
    getBoundingClientRect() { return { left: 10, top: 10, width: 20, height: 20 }; },
    setAttribute(name: string, value: string) { attributes.set(name, value); }, removeAttribute(name: string) { attributes.delete(name); }, scrollIntoView() {},
  };
  const buttons = [button];
  const section = { textContent: "一部の情報を取得できません", querySelector: () => ({ textContent: "現在の問題・Node稼働・診断を分けて確認" }), querySelectorAll: () => buttons };
  const document = {
    querySelectorAll: (selector: string) => selector === "main section" ? [section] : attributes.has("data-bundle9-monitoring-retry") ? [button] : [],
    querySelector: () => section,
  };
  const context = { document, getComputedStyle: () => ({ visibility: "visible" }), HTMLElement: Object };
  const fixture = createBundle9Fixture(fixtureOrigin);
  fixture.state.healthError = true;
  const lifecycle = new FetchRequestLifecycle();
  const browser = {
    responses: new Map(monitoringPaths.map((path) => [path, 1])),
    responseStatuses: new Map(monitoringPaths.map((path) => [path, [path === "/service-health" ? 503 : 200]])),
    requestLifecycle: lifecycle,
    waitForRequestHandlersIdle: BrowserHarness.prototype.waitForRequestHandlersIdle,
    assertNoFatalError: BrowserHarness.prototype.assertNoFatalError,
    async evaluate(expression: string) {
      if (expression.includes("document.fonts.ready")) { events.push("paint"); return true; }
      return runInNewContext(expression, context);
    },
    async waitFor(this: BrowserHarness, expression: string, accept: (value: unknown) => boolean, description: string, timeoutMs = 10_000) {
      assert.equal(timeoutMs, 10_000, "production observation deadline remains unchanged");
      const readiness = description.includes("summary retry");
      events.push(readiness ? "wait-enabled" : "wait-recovery");
      if (readiness) assert.equal(accept(await this.evaluate(expression)), false, "disabled button is not ready");
      await (readiness ? enabled.promise : recovered.promise);
      if (!accept(await this.evaluate(expression))) throw new Error(description);
    },
    clickSelector: BrowserHarness.prototype.clickSelector,
    clickAt: BrowserHarness.prototype.clickAt,
    async send(method: string, params: { type: string }) {
      assert.equal(method, "Input.dispatchMouseEvent");
      assert.equal(button.disabled, false); assert.equal(fixture.state.healthError, false);
      if (params.type === "mouseReleased") events.push("click");
      return {};
    },
    async waitForResponseCount(path: string, count: number) {
      assert.equal(count, 2, "initial completed GET must not count as the retry response");
      events.push(`wait-response:${path}`);
      await responsesCompleted.promise;
      // Use the real response-count check with a short regression-only deadline.
      await BrowserHarness.prototype.waitForResponseCount.call(this as unknown as BrowserHarness, path, count, 1);
    },
  } as unknown as BrowserHarness;
  const record = async (name: string) => {
    events.push(name);
    if (name === "monitoring-error") {
      assert.equal(fixture.state.healthError, true);
      await errorRecorded.promise;
    }
    await actualPaintBarrier(browser);
    events.push(`${name}:settled`);
  };
  return { browser, fixture, events, button, buttons, section, enabled, errorRecorded, responsesCompleted, recovered, lifecycle, record,
    completeResponses(status = 200, missing?: string) {
      for (const path of monitoringPaths.filter((path) => path !== missing)) { browser.responses.set(path, 2); browser.responseStatuses.get(path)!.push(status); }
      responsesCompleted.resolve();
    },
  };
}

async function advanceMonitoringToClick(boundary: ReturnType<typeof monitoringBoundary>) {
  await flushTasks();
  assert.deepEqual(boundary.events, ["wait-enabled"]);
  assert.equal(boundary.fixture.state.healthError, true);
  boundary.button.disabled = false; boundary.enabled.resolve();
  await flushTasks();
  assert.equal(boundary.events.at(-1), "monitoring-error");
  assert.equal(boundary.fixture.state.healthError, true);
  boundary.errorRecorded.resolve();
  await flushTasks();
  assert.equal(boundary.events.filter((event) => event === "click").length, 1);
  assert.equal(boundary.events.includes("monitoring-recovered"), false);
}

test("Bundle 9 Monitoring helper waits for enabled error, capture, one real click, every new successful GET, UI and settlement", async () => {
  const boundary = monitoringBoundary();
  const operation = retryBundle9Monitoring(boundary.browser, boundary.fixture, boundary.record);
  await advanceMonitoringToClick(boundary);
  boundary.completeResponses();
  await flushTasks();
  assert.equal(boundary.events.at(-1), "wait-recovery");
  assert.equal(boundary.events.includes("monitoring-recovered"), false);
  boundary.lifecycle.register({ requestId: "late-retry", pathname: "/service-health", method: "GET", requiredResponse: true });
  const settlement = boundary.lifecycle.beginSettlement("late-retry", "Fetch.fulfillRequest");
  boundary.section.textContent = "監視情報は正常に取得済み"; boundary.recovered.resolve();
  await flushTasks();
  assert.equal(boundary.events.includes("monitoring-recovered:settled"), false);
  boundary.lifecycle.completeSettlement(settlement);
  await operation;
  assert.equal(boundary.events.at(-1), "monitoring-recovered:settled");
  assert.equal(boundary.events.filter((event) => event === "click").length, 1);
});

for (const mode of ["disabled", "missing", "duplicate", "hidden"] as const) {
  test(`Bundle 9 Monitoring helper rejects ${mode} summary retry without normalizing or clicking`, async () => {
    const boundary = monitoringBoundary();
    if (mode !== "disabled") boundary.button.disabled = false;
    if (mode === "missing") boundary.buttons.length = 0;
    if (mode === "duplicate") boundary.buttons.push({ ...boundary.button });
    if (mode === "hidden") boundary.button.visible = false;
    const operation = retryBundle9Monitoring(boundary.browser, boundary.fixture, boundary.record);
    const rejected = assert.rejects(operation, /unique visible enabled Monitoring summary retry/);
    boundary.enabled.resolve(); await rejected;
    assert.equal(boundary.fixture.state.healthError, true);
    assert.deepEqual(boundary.events, ["wait-enabled"]);
  });
}

for (const mode of ["missing-response", "failed-response", "missing-recovery"] as const) {
  test(`Bundle 9 Monitoring helper rejects ${mode} after one click without a recovery capture`, async () => {
    const boundary = monitoringBoundary();
    const operation = retryBundle9Monitoring(boundary.browser, boundary.fixture, boundary.record);
    const rejected = assert.rejects(operation);
    await advanceMonitoringToClick(boundary);
    boundary.completeResponses(mode === "failed-response" ? 503 : 200, mode === "missing-response" ? "/observability/diagnostics" : undefined);
    boundary.recovered.resolve(); await rejected;
    assert.equal(boundary.events.includes("monitoring-recovered"), false);
    assert.equal(boundary.events.filter((event) => event === "click").length, 1);
  });
}

test("Bundle 9 actual Monitoring caller awaits the helper before restoring fixture state", async () => {
  const boundary = monitoringBoundary();
  let helperCalls = 0;
  const helper = deferred();
  const caller = sourceFunction(scenarioSource("monitoring-error-and-recovery"), {
    browser: boundary.browser, fixture: boundary.fixture, record: boundary.record, paintBarrier: actualPaintBarrier,
    navigate: async (_surface: string, _text: string, prepare: () => void) => { prepare(); },
    retryBundle9Monitoring: async (browser: BrowserHarness, fixture: unknown, record: unknown) => {
      helperCalls += 1; assert.equal(browser, boundary.browser); assert.equal(fixture, boundary.fixture); assert.equal(record, boundary.record); await helper.promise;
    },
  });
  const operation = caller(); await flushTasks();
  assert.equal(helperCalls, 1); assert.equal(boundary.fixture.state.healthError, true);
  assert.deepEqual(boundary.events, []);
  helper.resolve(); await operation;
  assert.equal(boundary.fixture.state.healthError, false);
});

test("Bundle 9 Monitoring ordering oracle detects removal of the enabled wait", async () => {
  const helper = findSourceNode(scenariosSource, (node) => ts.isFunctionDeclaration(node) && node.name?.text === "retryBundle9Monitoring").getText(scenariosSource);
  const expression = findSourceNode(scenariosSource, (node) => ts.isVariableDeclaration(node) && ts.isIdentifier(node.name) && node.name.text === "monitoringRetryExpression") as ts.VariableDeclaration;
  const mutant = helper.replace("await browser.waitFor(monitoringRetryExpression", "void browser.waitFor(monitoringRetryExpression");
  assert.notEqual(mutant, helper);
  const boundary = monitoringBoundary();
  const execute = sourceFunction(mutant.replace(/^export /, ""), { assert, monitoringRetryExpression: runInNewContext(expression.initializer!.getText(scenariosSource)) });
  const operation = execute(boundary.browser, boundary.fixture, boundary.record);
  await flushTasks();
  assert.throws(() => assert.deepEqual(boundary.events, ["wait-enabled"]), "removed await must violate the real helper ordering oracle");
  boundary.button.disabled = false; boundary.enabled.resolve(); boundary.errorRecorded.resolve();
  const rejected = assert.rejects(operation, /monitoring retry recovery/);
  await flushTasks();
  assert.equal(boundary.events.filter((event) => event === "click").length, 1);
  boundary.completeResponses();
  await flushTasks(); boundary.recovered.resolve();
  await rejected;
});

test("Bundle 9 real font inventory requires Japanese fontconfig coverage backed by the same files and hashes", (t) => {
  const directory = mkdtempSync(resolve(tmpdir(), "bundle9-font-contract-"));
  const latin = resolve(directory, "latin.font"); const japanese = resolve(directory, "japanese.font");
  // Only the fc-list process is controlled; the inventory reads and hashes real files.
  writeFileSync(latin, "controlled Latin font bytes"); writeFileSync(japanese, "controlled Japanese font bytes");
  t.after(() => { unlinkSync(latin); unlinkSync(japanese); rmdirSync(directory); });
  const queries: (string | undefined)[] = [];
  const inventory = fontInventory((pattern) => { queries.push(pattern); return pattern ? [japanese] : [latin, japanese]; });
  assert.deepEqual(queries, [undefined, ":lang=ja"]);
  assert.deepEqual(inventory.japanese, [{ path: japanese, sha256: sha256(readFileSync(japanese)) }]);
  assert.throws(() => fontInventory(() => []), /nonempty/);
  assert.throws(() => fontInventory((pattern) => pattern ? [] : [latin]), /:lang=ja/);
  assert.throws(() => fontInventory((pattern) => pattern ? [japanese] : [latin]), /registered/);
  assert.throws(() => assertJapaneseFonts([{ path: japanese, sha256: "wrong" }], [japanese]), /hash mismatch/);
  assert.throws(() => assertJapaneseFonts([{ path: resolve(directory, "missing.font"), sha256: "missing" }], [japanese]));
  writeFileSync(japanese, "changed font bytes on after side");
  const changed = fontInventory((pattern) => pattern ? [japanese] : [latin, japanese]);
  assert.throws(() => assertSameObservation(inventory, changed));
  assert.throws(() => assertSameObservation(inventory, { ...inventory, japanese: [] }));

  const runner = ts.createSourceFile("runner.mts", readFileSync(new URL("./helpers/run-bundle9-ui-comparison.mts", import.meta.url), "utf8"), ts.ScriptTarget.Latest, true, ts.ScriptKind.TS);
  const helper = findSourceNode(runner, (node) => ts.isFunctionDeclaration(node) && node.name?.text === "fontInventory").getText(runner);
  const mutant = helper.replace('assertJapaneseFonts(all, listFiles(":lang=ja"))', "[]");
  assert.notEqual(mutant, helper);
  const execute = sourceFunction(mutant.replace(/^export /, ""), { assert, readFileSync, statSync, sha256 });
  assert.throws(() => assert.throws(() => execute((pattern?: string) => pattern ? [] : [latin])), "removing the coverage check must fail the Latin-only rejection oracle");
});

test("Bundle 9 comparison runner binds the same checked Japanese font set to source conditions and both capture sides", () => {
  const runner = ts.createSourceFile("runner.mts", readFileSync(new URL("./helpers/run-bundle9-ui-comparison.mts", import.meta.url), "utf8"), ts.ScriptTarget.Latest, true, ts.ScriptKind.TS);
  const main = findSourceNode(runner, (node) => ts.isFunctionDeclaration(node) && node.name?.text === "main");
  const calls: ts.CallExpression[] = [];
  const visit = (node: ts.Node) => { if (ts.isCallExpression(node) && node.expression.getText(runner) === "fontInventory") calls.push(node); ts.forEachChild(node, visit); };
  visit(main);
  assert.equal(calls.length, 3);
  assert.ok(ts.isVariableDeclaration(calls[0].parent) && calls[0].parent.name.getText(runner) === "fonts");
  for (const call of calls.slice(1)) {
    assert.ok(ts.isCallExpression(call.parent));
    assert.equal(call.parent.expression.getText(runner), "assertSameObservation");
    assert.equal(call.parent.arguments[0].getText(runner), "fonts");
    const statement = call.parent.parent;
    assert.ok(ts.isExpressionStatement(statement) && ts.isBlock(statement.parent));
    const next = statement.parent.statements[statement.parent.statements.indexOf(statement) + 1];
    assert.match(next.getText(runner), /await captureBundle9Source\(/, "font equality must be checked immediately before each source capture");
  }
  const conditions = findSourceNode(main, (node) => ts.isVariableDeclaration(node) && ts.isIdentifier(node.name) && node.name.text === "conditions");
  assert.match(conditions.getText(runner), /japaneseFonts: fonts\.japanese, japaneseFontPattern: ":lang=ja"/);
});
