import assert from "node:assert/strict";
import test from "node:test";
import ts from "typescript";
import { createUIComparisonFixture } from "./helpers/browser-comparison-fixtures.mts";
import { assertUIComparisonCancelledMutations, navigateUIComparisonDocument } from "./helpers/browser-comparison-scenarios.mts";
import { apiObservationSummary, assertSameObservation, uiComparisonSurfaces, type APIObservation } from "./helpers/browser-comparison-contract.mts";
import { fixtureOrigin, navigationBoundary, flushTasks, findSourceNode, captureSource, sourceFunction, scenariosSource, actualPaintBarrier, sessionRefresh, invalidCancelledMutations, scenarioSource } from "./browser-navigation-fixture.mts";


export function registerBrowserNavigationCases() {


for (const method of ["GET", "HEAD"]) {
  test(`UI source comparison fixture continues same-origin ${method} admin documents without an API observation`, () => {
    const fixture = createUIComparisonFixture(fixtureOrigin);
    for (const path of ["/admin", "/admin/", "/admin?view=active", "/admin/?view=active", "/admin/streams/", "/admin/streams/?view=active"]) {
      assert.equal(fixture.resolver({ method, url: fixtureOrigin + path }), null, path);
    }
    assert.deepEqual(fixture.unexpected, []);
    assert.deepEqual(fixture.trace, []);
  });
}

test("UI source comparison fixture retains external, prefix, unknown API and non-document-method rejection", () => {
  for (const method of ["GET", "HEAD"]) {
    for (const path of ["/admin", "/admin/", "/admin/?view=active", "/admin/streams/"]) {
      const fixture = createUIComparisonFixture(fixtureOrigin);
      const response = fixture.resolver({ method, url: "http://127.0.0.1:4101" + path });
      assert.equal(response?.status, 502);
      assert.equal(response?.requiredResponse, true);
      assert.equal(fixture.unexpected.length, 1);
      assert.deepEqual(fixture.trace, []);
    }
    for (const path of ["/administer", "/administer/streams", "/unknown-api", "/unknown-api?view=active"]) {
      const fixture = createUIComparisonFixture(fixtureOrigin);
      const response = fixture.resolver({ method, url: fixtureOrigin + path });
      assert.equal(response?.status, 404);
      assert.equal(response?.requiredResponse, true);
      assert.deepEqual(fixture.unexpected, [`${method} ${path}`]);
      assert.deepEqual(fixture.trace, []);
    }
  }
  for (const method of ["POST", "PUT", "PATCH", "DELETE", "OPTIONS"]) {
    for (const path of ["/admin", "/admin/", "/admin/streams/"]) {
      const fixture = createUIComparisonFixture(fixtureOrigin);
      const response = fixture.resolver({ method, url: fixtureOrigin + path });
      assert.equal(response?.status, 404);
      assert.equal(response?.requiredResponse, true);
      assert.deepEqual(fixture.unexpected, [`${method} ${path}`]);
    }
  }
  const fixture = createUIComparisonFixture(fixtureOrigin);
  const response = fixture.resolver({ method: "GET", url: fixtureOrigin + "/auth/me" });
  assert.equal(response?.requiredResponse, true);
  assert.deepEqual(fixture.trace, [{ method: "GET", path: "/auth/me", body: null, status: 200 }]);
  assert.deepEqual(fixture.unexpected, []);
});

test("UI source comparison navigation waits for the previous required response ack before paint, blank and reset", async () => {
  const boundary = navigationBoundary();
  const ack = boundary.pendingResponse("previous-auth", "/auth/me");
  const navigation = navigateUIComparisonDocument(boundary.browser, boundary.fixture, fixtureOrigin + "/admin/");
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

test("UI source comparison viewport paint observes late requests before accepting handler idle", async () => {
  const boundary = navigationBoundary();
  await boundary.browser.setViewport(390, 844);
  const navigation = navigateUIComparisonDocument(boundary.browser, boundary.fixture, fixtureOrigin + "/admin/", () => { boundary.events.push("fixture-state"); });
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
  test(`UI source comparison navigation propagates the original ${failureMode} failure without starting the next phase`, async () => {
    const boundary = navigationBoundary();
    const original = new Error(failureMode === "fatal" ? "Invalid InterceptionId." : "controlled paint rejection");
    if (failureMode === "fatal") boundary.pendingResponse("pending-auth", "/auth/me");
    const navigation = navigateUIComparisonDocument(boundary.browser, boundary.fixture, fixtureOrigin + "/admin/");
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

for (const callerName of ["navigate", "archive-permission-denied"]) {
  test(`UI source comparison actual capture ${callerName} caller awaits paint, settlement and next-document completion`, async () => {
    const boundary = navigationBoundary();
    const callerNode = callerName === "navigate"
      ? findSourceNode(captureSource, (node) => ts.isVariableDeclaration(node) && ts.isIdentifier(node.name) && node.name.text === "navigate") as ts.VariableDeclaration
      : findSourceNode(captureSource, (node) => ts.isCallExpression(node) && ts.isIdentifier(node.expression) && node.expression.text === "scenario" && ts.isStringLiteral(node.arguments[0]) && node.arguments[0].text === callerName) as ts.CallExpression;
    const expression = ts.isVariableDeclaration(callerNode) ? callerNode.initializer! : callerNode.arguments[1];
    const caller = sourceFunction(expression.getText(scenariosSource), {
      browser: boundary.browser, fixture: boundary.fixture, baseURL: fixtureOrigin,
      navigateUIComparisonDocument, uiComparisonSurfaces, paintBarrier: actualPaintBarrier,
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

test("UI source comparison capture awaits every actual navigation call, including both shared-helper callers", () => {
  const calls: ts.CallExpression[] = [];
  const visit = (node: ts.Node) => {
    if (ts.isCallExpression(node) && ts.isIdentifier(node.expression) && ["navigate", "navigateUIComparisonDocument"].includes(node.expression.text)) calls.push(node);
    ts.forEachChild(node, visit);
  };
  visit(captureSource);
  assert.equal(calls.filter((call) => (call.expression as ts.Identifier).text === "navigateUIComparisonDocument").length, 2);
  assert.ok(calls.length > 2);
  for (const call of calls) assert.ok(ts.isAwaitExpression(call.parent), call.getText(scenariosSource));
});

test("UI source comparison ordering oracle detects omitted await, post-navigation paint and empty-idle-only mutants", async () => {
  const helper = findSourceNode(scenariosSource, (node) => ts.isFunctionDeclaration(node) && node.name?.text === "navigateUIComparisonDocument").getText(scenariosSource);
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

test("UI source comparison cancelled dialogs require exactly the observed session refresh and retain it in the full API comparison", () => {
  const trace = [{ method: "GET", path: "/auth/me", body: null, status: 200 }, sessionRefresh];
  const before = structuredClone(trace);
  assertUIComparisonCancelledMutations(trace);
  assert.deepEqual(trace, before);
  assert.throws(() => assertSameObservation(apiObservationSummary(trace), apiObservationSummary(trace.slice(0, 1))));
  for (const mutation of invalidCancelledMutations) assert.throws(() => assertUIComparisonCancelledMutations(mutation));
});

for (const name of ["resources-editor", "updater-settings"]) {
  test(`UI source comparison actual ${name} caller checks session-only mutations after closing and returning focus`, async () => {
    for (const mutations of [[sessionRefresh], ...invalidCancelledMutations]) {
      const events: string[] = [];
      const fixture = { trace: mutations };
      let assertions = 0;
      const caller = sourceFunction(scenarioSource(name), {
        fixture, navigate: async () => {}, rememberFocusTarget: async () => {},
        browser: { clickSelector: async () => {}, pressNativeKey: async (key: string) => { assert.equal(key, "Escape"); }, waitForResponseCount: async () => {}, waitFor: async () => {} },
        waitForDialog: async (_browser: unknown, open: boolean) => { events.push(open ? "open" : "closed"); },
        waitForExactFocus: async () => { events.push("focus"); }, record: async (capture: string) => { events.push(capture); },
        assertUIComparisonCancelledMutations: (trace: APIObservation[]) => {
          assertions += 1; assert.equal(trace, fixture.trace);
          assert.deepEqual(events.slice(-2), ["closed", "focus"]);
          assertUIComparisonCancelledMutations(trace);
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
}
