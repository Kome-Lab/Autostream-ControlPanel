import assert from "node:assert/strict";
import { tmpdir } from "node:os";
import test from "node:test";
import { resolve } from "node:path";
import { createContext, runInNewContext } from "node:vm";
import ts from "typescript";
import { BrowserHarness } from "./helpers/browser-harness.mts";
import { FetchRequestLifecycle } from "./helpers/browser-request-lifecycle.mts";
import { bundle9SyntheticMFASecret, createBundle9Fixture } from "./helpers/bundle9-browser-fixtures.mts";
import { type Bundle9Capture } from "./helpers/bundle9-browser-scenarios.mts";
import { apiObservationSummary, assertSameObservation, sha256, type APIObservation } from "./helpers/bundle9-browser-contract.mts";
import { findSourceNode, scenariosSource, captureSource, deferred, fixtureOrigin, actualPaintBarrier, sourceFunction, flushTasks } from "./bundle9-browser-navigation-fixture.mts";


const observerNode = findSourceNode(scenariosSource, (node) => ts.isVariableDeclaration(node) && ts.isIdentifier(node.name) && node.name.text === "observationExpression") as ts.VariableDeclaration;
const actualObserverExpression = runInNewContext(observerNode.initializer!.getText(scenariosSource), { bundle9SyntheticMFASecret }) as string;
const recordNode = findSourceNode(captureSource, (node) => ts.isVariableDeclaration(node) && ts.isIdentifier(node.name) && node.name.text === "record") as ts.VariableDeclaration;
const actualRecordSource = recordNode.initializer!.getText(scenariosSource);
type FocusObservation = { tag: string; role: string; label: string; text: string };
type DOMObservation = Record<string, unknown> & { focus: FocusObservation | null; focusReturnedToExactTrigger: boolean | null; hiddenDiagnostic: boolean; secretLeak: boolean };

class ObservationElement {
  tagName: string;
  textContent: string;
  innerText: string;
  attributes = new Map<string, string>();
  constructor(tag: string, text: string) { this.tagName = tag; this.textContent = text; this.innerText = text; }
  getAttribute(name: string) { return this.attributes.get(name) ?? null; }
  getClientRects() { return [{}]; }
}

function observerBoundary() {
  const body = new ObservationElement("BODY", " \nAutoStream 管理画面の監視情報と配信管理を確認します。\n ");
  const button = new ObservationElement("BUTTON", " 再試行 ");
  const document = {
    body, activeElement: body as unknown, title: "AutoStream",
    documentElement: { outerHTML: "", scrollWidth: 1440, scrollHeight: 900, lang: "ja" },
    querySelectorAll: (selector: string) => selector === 'a,button,input,textarea,select,[role="tab"]' ? [button] : [],
  };
  const context = {
    document, HTMLElement: ObservationElement,
    HTMLInputElement: class extends ObservationElement {}, HTMLTextAreaElement: class extends ObservationElement {}, HTMLSelectElement: class extends ObservationElement {},
    localStorage: {} as Record<string, string>, sessionStorage: {} as Record<string, string>,
    location: { pathname: "/admin/monitoring/", search: "?view=active", hash: "#summary" },
    innerWidth: 1440, innerHeight: 900, devicePixelRatio: 1,
    getComputedStyle: () => ({ colorScheme: "light", fontFamily: "sans-serif" }),
    __bundle9FocusTarget: undefined as unknown,
  };
  const setScript = (script: string) => { body.textContent = body.innerText + script; document.documentElement.outerHTML = `<body>${body.innerText}<script>${script}</script></body>`; };
  setScript('self.__next_f.push(["before-build-id"]);');
  return { body, button, document, context, setScript,
    observe(expression = actualObserverExpression) { return runInNewContext(expression, context) as DOMObservation; },
  };
}

function assertBodyFocusOracle(expression: string) {
  const boundary = observerBoundary();
  const before = boundary.observe(expression);
  assert.deepEqual(structuredClone(before.focus), { tag: "body", role: "", label: "", text: boundary.body.innerText.trim() });
  const initialRawText = boundary.body.textContent;
  boundary.setScript('self.__next_f.push(["different-after-build-id"]);');
  assert.notEqual(boundary.body.textContent, initialRawText);
  assertSameObservation(before, boundary.observe(expression));
  boundary.body.innerText = "AutoStream 管理画面の表示文言を実際に変更しました。";
  assert.throws(() => assertSameObservation(before.focus, boundary.observe(expression).focus), "a visible body text difference must remain observable in focus");
}

function recordBoundary(recordSource = actualRecordSource, observer = observerBoundary(), fault?: "png" | "json") {
  const events: string[] = [];
  const captures: Bundle9Capture[] = [];
  const json = new Map<string, string>(); const jsonValues = new Map<string, unknown>();
  const png = Buffer.from("controlled screenshot bytes");
  const screenshot = deferred();
  const failure = new Error(`controlled ${fault ?? "screenshot"} failure`);
  const body = { policy: { limits: [1, 2], enabled: true } };
  const fixture = createBundle9Fixture(fixtureOrigin);
  fixture.trace.push(
    { method: "GET", path: "/service-health", body: null, status: 503 },
    { method: "GET", path: "/service-health", body: null, status: 503 },
    { method: "GET", path: "/streams", body: null, status: 200 },
    { method: "GET", path: "/auth/me", body: null, status: 200 },
    { method: "POST", path: "/streams/one/start-readiness", body, status: 200 },
    { method: "POST", path: "/auth/session/refresh", body: null, status: 200 },
    { method: "POST", path: "/controlled/no-body", status: 200 } as APIObservation,
  );
  const health = [503, 503]; const streams = [200]; const account = [200];
  const responseStatuses = new Map([["/service-health", health], ["/streams", streams], ["/auth/me", account]]);
  const extraObservation: Record<string, unknown> = {};
  const lifecycle = new FetchRequestLifecycle();
  const browser = {
    requestLifecycle: lifecycle, responseStatuses, consoleErrorCount: 0, navigationCount: 1,
    waitForRequestHandlersIdle: BrowserHarness.prototype.waitForRequestHandlersIdle,
    assertNoFatalError: BrowserHarness.prototype.assertNoFatalError,
    async evaluate(expression: string) {
      if (expression.includes("document.fonts.ready")) { events.push("paint"); return true; }
      assert.equal(expression, actualObserverExpression, "the actual record must execute the real observer");
      events.push("observe"); return { ...observer.observe(), ...extraObservation };
    },
    async captureScreenshot() { events.push("screenshot"); await screenshot.promise; return png; },
  } as unknown as BrowserHarness;
  const recordContext = createContext({
    assert, browser, fixture, baseURL: fixtureOrigin, output: resolve(tmpdir(), "bundle9-controlled-record"),
    paintBarrier: actualPaintBarrier, observationExpression: actualObserverExpression,
    apiObservationSummary, structuredClone, captures, URL, resolve, sha256,
    writeFileSync: (_path: string, bytes: Buffer, options: { flag: string }) => {
      assert.equal(options.flag, "wx"); assert.equal(bytes, png); events.push("write-png");
      if (fault === "png") throw failure;
    },
    writeJSON: (path: string, value: unknown) => {
      events.push("write-json"); if (fault === "json") throw failure;
      jsonValues.set(path, value); json.set(path, JSON.stringify(value));
    },
  });
  // Match the callback's array realm without changing its real deep assertion.
  fixture.unexpected = runInNewContext("[]", recordContext) as string[];
  const record = sourceFunction(recordSource, recordContext);
  const saved = (name: string) => JSON.parse(json.get(resolve(tmpdir(), "bundle9-controlled-record", `${name}.json`))!) as Record<string, unknown>;
  return { record, browser, fixture, health, streams, account, body, screenshot, events, captures, json, jsonValues, saved, png, observer, extraObservation, failure };
}

async function assertRecordSnapshotOracle(recordSource: string) {
  const boundary = recordBoundary(recordSource);
  const initialAPI = structuredClone(apiObservationSummary(boundary.fixture.trace));
  const operation = boundary.record("monitoring-error");
  await flushTasks();
  assert.equal(boundary.events.at(-1), "screenshot");
  assert.equal(boundary.captures.length, 0); assert.equal(boundary.json.size, 0);
  for (const live of [boundary.health, boundary.streams, boundary.account, boundary.fixture.trace, boundary.body.policy.limits]) assert.equal(Object.isFrozen(live), false);
  // These events arrive while the real record callback is awaiting its PNG.
  boundary.health.push(200); boundary.streams.push(200);
  boundary.browser.responseStatuses.set("/auth/me", [200, 200]);
  boundary.body.policy.limits[0] = 99;
  boundary.fixture.trace.push({ method: "GET", path: "/service-health", body: null, status: 200 });
  boundary.screenshot.resolve(); await operation;
  const first = boundary.saved("monitoring-error");
  assert.deepEqual(first.statuses, [["/auth/me", [200]], ["/service-health", [503, 503]], ["/streams", [200]]], "snapshot must precede the screenshot await and detach status arrays");
  assertSameObservation(initialAPI, first.api);
  assert.deepEqual(first, JSON.parse(JSON.stringify(boundary.captures[0].observation)), "individual JSON and later manifest must contain identical values");
  assert.equal(boundary.captures[0].observation, [...boundary.jsonValues.values()][0], "one snapshot object must feed JSON and captures");
  assert.equal(boundary.captures[0].pngSHA256, sha256(boundary.png));
  assert.deepEqual(boundary.events.slice(-3), ["screenshot", "write-png", "write-json"]);
  boundary.browser.responseStatuses.clear();
  boundary.account.push(401);
  assert.deepEqual(first, JSON.parse(JSON.stringify(boundary.captures[0].observation)), "Map clear/replacement and later account responses must not alter old evidence");
  boundary.browser.responseStatuses.set("/service-health", boundary.health);
  boundary.browser.responseStatuses.set("/streams", boundary.streams);
  boundary.browser.responseStatuses.set("/auth/me", boundary.account);
  await boundary.record("monitoring-recovered");
  const second = boundary.saved("monitoring-recovered");
  assert.deepEqual(second.statuses, [["/auth/me", [200, 401]], ["/service-health", [503, 503, 200]], ["/streams", [200, 200]]]);
  assertSameObservation(apiObservationSummary(boundary.fixture.trace), second.api);
  assert.deepEqual(second, JSON.parse(JSON.stringify(boundary.captures[1].observation)));
  assert.deepEqual(first, boundary.saved("monitoring-error"));
  assert.deepEqual(first, JSON.parse(JSON.stringify(boundary.captures[0].observation)));
  assert.equal(boundary.browser.responseStatuses.get("/service-health"), boundary.health);
  assert.equal(boundary.fixture.trace.length, 8, "record must not clear or suppress live request history");
}

export function registerBrowserObservationCases() {


test("Bundle 9 actual observer reads visible body focus text while retaining real displayed differences", () => {
  assertBodyFocusOracle(actualObserverExpression);
});

test("Bundle 9 actual observer retains focus target, control text/tag/role/label and exact trigger identity", () => {
  const boundary = observerBoundary();
  const bodyFocus = boundary.observe().focus;
  boundary.document.activeElement = boundary.button;
  const control = boundary.observe();
  assert.throws(() => assertSameObservation(bodyFocus, control.focus));
  assert.deepEqual(structuredClone(control.focus), { tag: "button", text: "再試行", role: "", label: "" });
  for (const change of [
    () => { boundary.button.textContent = " 更新 "; },
    () => { boundary.button.tagName = "A"; },
    () => { boundary.button.attributes.set("role", "tab"); },
    () => { boundary.button.attributes.set("aria-label", "別の操作"); },
  ]) {
    const before = boundary.observe().focus;
    change();
    assert.throws(() => assertSameObservation(before, boundary.observe().focus));
  }
  boundary.context.__bundle9FocusTarget = boundary.button;
  const exact = boundary.observe();
  assert.equal(exact.focusReturnedToExactTrigger, true);
  boundary.context.__bundle9FocusTarget = new ObservationElement(boundary.button.tagName, boundary.button.textContent);
  assert.equal(boundary.observe().focusReturnedToExactTrigger, false);
  assert.throws(() => assertSameObservation(exact, boundary.observe()));
  for (const nonHTMLElement of [null, {}]) {
    boundary.document.activeElement = nonHTMLElement;
    assert.equal(boundary.observe().focus, null, "absent/non-HTMLElement focus must not be replaced by body");
    assert.equal(boundary.document.activeElement, nonHTMLElement, "observation must not move focus");
  }
});

test("Bundle 9 body focus oracle rejects raw script text, omitted focus and constant text mutants", () => {
  for (const mutant of [
    actualObserverExpression.replace("focus.text = document.body.innerText.trim()", "focus.text = document.body.textContent.trim()"),
    actualObserverExpression.replace("focus, focusReturnedToExactTrigger:", "focusReturnedToExactTrigger:"),
    actualObserverExpression.replace("focus.text = document.body.innerText.trim()", 'focus.text = ""'),
    actualObserverExpression.replace("focus, focusReturnedToExactTrigger:", "focus: null, focusReturnedToExactTrigger:"),
  ]) {
    assert.notEqual(mutant, actualObserverExpression);
    assert.throws(() => assertBodyFocusOracle(mutant));
  }
});

test("Bundle 9 actual observer and record retain hidden markup/storage secret and diagnostic rejection", async () => {
  for (const [location, content] of [
    ["markup", bundle9SyntheticMFASecret], ["markup", "B9-SYNTHETIC-RECOVERY"], ["markup", "B9-HIDDEN-DIAGNOSTIC"],
    ["local", bundle9SyntheticMFASecret], ["session", "B9-SYNTHETIC-RECOVERY"],
  ]) {
    const observer = observerBoundary();
    if (location === "markup") observer.setScript(content);
    else (location === "local" ? observer.context.localStorage : observer.context.sessionStorage).hidden = content;
    const observed = observer.observe();
    assert.equal(observed.focus?.text, observer.body.innerText.trim());
    assert.equal(content === "B9-HIDDEN-DIAGNOSTIC" ? observed.hiddenDiagnostic : observed.secretLeak, true);
    const boundary = recordBoundary(actualRecordSource, observer);
    await assert.rejects(boundary.record("hidden-content"), /diagnostic disclosure|secret must not leak/);
    assert.equal(boundary.events.includes("screenshot"), false);
    assert.equal(boundary.json.size, 0); assert.equal(boundary.captures.length, 0);
  }
});

test("Bundle 9 actual record snapshots nested observations before PNG await while later captures retain new events", async () => {
  await assertRecordSnapshotOracle(actualRecordSource);
});

test("Bundle 9 real record oracle rejects no-copy, shallow-copy and post-screenshot-copy mutants", async () => {
  for (const mutant of [
    actualRecordSource.replace("structuredClone({", "({"),
    actualRecordSource.replace("structuredClone({", "Object.assign({}, {"),
    actualRecordSource.replace("const evidence = structuredClone({", "let evidence = ({").replace("const png = await browser.captureScreenshot();", "const png = await browser.captureScreenshot(); evidence = structuredClone(evidence);"),
  ]) {
    assert.notEqual(mutant, actualRecordSource);
    await assert.rejects(() => assertRecordSnapshotOracle(mutant), /snapshot must precede the screenshot await/);
  }
});

test("Bundle 9 actual record preserves structured-clone values without JSON round-trip conversion", async () => {
  const boundary = recordBoundary();
  const extra = { absent: undefined, explicitNull: null, date: new Date("2026-09-12T00:00:00Z"), bytes: Uint8Array.from([1, 2]) };
  boundary.extraObservation.extra = extra;
  const operation = boundary.record("typed-values"); await flushTasks();
  extra.date.setUTCFullYear(2027); extra.bytes[0] = 9;
  boundary.screenshot.resolve(); await operation;
  const savedExtra = (boundary.captures[0].observation as { extra: typeof extra }).extra;
  assert.equal(Object.hasOwn(savedExtra, "absent"), true); assert.equal(savedExtra.absent, undefined); assert.equal(savedExtra.explicitNull, null);
  assert.ok(savedExtra.date instanceof Date); assert.equal(savedExtra.date.toISOString(), "2026-09-12T00:00:00.000Z");
  assert.ok(savedExtra.bytes instanceof Uint8Array); assert.deepEqual([...savedExtra.bytes], [1, 2]);
  assert.deepEqual(boundary.saved("typed-values"), JSON.parse(JSON.stringify(boundary.captures[0].observation)));
});

test("Bundle 9 actual record propagates clone, screenshot and write failures without registering a capture", async () => {
  for (const fault of ["clone", "screenshot", "png", "json"] as const) {
    const boundary = recordBoundary(actualRecordSource, observerBoundary(), fault === "png" || fault === "json" ? fault : undefined);
    if (fault === "clone") boundary.extraObservation.unsupported = () => {};
    const operation = boundary.record("failed-capture");
    const rejected = assert.rejects(operation, (error) => fault === "clone" ? (error as Error).name === "DataCloneError" : error === boundary.failure);
    await flushTasks();
    if (fault === "screenshot") boundary.screenshot.reject(boundary.failure);
    else boundary.screenshot.resolve();
    await rejected;
    assert.equal(boundary.captures.length, 0); assert.equal(boundary.json.size, 0);
    if (fault === "clone") assert.equal(boundary.events.includes("screenshot"), false);
  }
});
}
