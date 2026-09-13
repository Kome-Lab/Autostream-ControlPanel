import assert from "node:assert/strict";
import { mkdtempSync, readFileSync, rmdirSync, statSync, unlinkSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import test from "node:test";
import { resolve } from "node:path";
import { runInNewContext } from "node:vm";
import ts from "typescript";
import { BrowserHarness } from "./helpers/browser-harness.mts";
import { FetchRequestLifecycle } from "./helpers/browser-request-lifecycle.mts";
import { createBundle9Fixture } from "./helpers/bundle9-browser-fixtures.mts";
import { retryBundle9Monitoring } from "./helpers/bundle9-browser-scenarios.mts";
import { assertJapaneseFonts, fontInventory } from "./helpers/run-bundle9-ui-comparison.mts";
import { assertSameObservation, sha256 } from "./helpers/bundle9-browser-contract.mts";
import { deferred, fixtureOrigin, actualPaintBarrier, flushTasks, sourceFunction, scenarioSource, findSourceNode, scenariosSource } from "./bundle9-browser-navigation-fixture.mts";


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

export function registerBrowserMonitoringCases() {


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
}
