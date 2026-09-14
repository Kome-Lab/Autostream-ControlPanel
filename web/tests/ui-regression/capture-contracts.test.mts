import assert from "node:assert/strict";
import test from "node:test";
import type { BrowserHarness } from "../helpers/browser-harness.mts";
import { captureObservation, preserveFetchDiagnostic } from "./capture.mts";
import { conditions } from "./matrix.mts";
import { navigateDocument } from "./navigation.mts";
import { assertObservation, type UIObservation } from "./observation.mts";

const condition = conditions[0];
const base: UIObservation = { h1: ["Streams"], lang: condition.locale, mode: condition.mode, theme: condition.theme, overflow: 0,
  hiddenDiagnostic: false, secretLeak: false, controls: [{ tag: "BUTTON", name: "Create", labelled: true, left: 0, right: 44, width: 44, height: 44, clipped: false }],
  text: "Ready", notices: [], duplicateIDs: [], invalidReferences: [], media: {forcedColors:false,reducedMotion:false,activeMotion:[],charts:[]} };

test("UI-CAPTURE-001: actual capture detaches nested API/status observations before PNG await", async () => {
  let release!: (bytes: Buffer) => void;
  const screenshot = new Promise<Buffer>(resolve => { release = resolve; });
  const trace = [{ method: "GET", path: "/streams", status: 200, body: { keys: [1] } }];
  const responses = [200];
  const writes = new Map<string, unknown>();
  const browser = {
    evaluate: async () => base, responseStatuses: new Map([["/streams", responses]]),
    requests: new Map([["/streams", 1]]), responses: new Map([["/streams", 1]]), consoleErrorCount: 0,
    captureScreenshot: () => screenshot,
  };
  const operation = captureObservation(browser as unknown as BrowserHarness, condition, trace, (name, value) => writes.set(name, value), (name, bytes) => writes.set(name, bytes));
  await Promise.resolve();
  const first = structuredClone(writes.get(condition.id + ".json"));
  responses.push(503); trace[0].body.keys.push(2); browser.requests.set("/streams", 2);
  release(Buffer.from("synthetic PNG"));
  const result = await operation;
  assert.deepEqual(result, first);
  assert.equal(writes.get(condition.id + ".json"), result);
  assert.deepEqual(result.statuses, [["/streams", [200]]]);
  assert.deepEqual(result.api[0].body, { keys: [1] });
  assert.deepEqual(browser.responseStatuses.get("/streams"), [200, 503]);
});
for (const failure of ["observe", "clone", "json", "png", "write-png"] as const) {
  test("UI-CAPTURE-002: actual capture propagates " + failure + " failure without success", async () => {
    const original = new Error(failure);
    const calls: string[] = [];
    const browser = {
      evaluate: async () => { if (failure === "observe") throw original; return failure === "clone" ? { callback() {} } : base; },
      responseStatuses: new Map(), requests: new Map(), responses: new Map(), consoleErrorCount: 0,
      captureScreenshot: async () => { calls.push("png"); if (failure === "png") throw original; return Buffer.from("PNG"); },
    };
    await assert.rejects(captureObservation(browser as unknown as BrowserHarness, condition, [], () => { calls.push("json"); if (failure === "json") throw original; },
      () => { calls.push("write-png"); if (failure === "write-png") throw original; }),
    error => failure === "clone" ? (error as Error).name === "DataCloneError" : error === original);
    if (["observe", "clone", "json"].includes(failure)) assert.ok(!calls.includes("png"));
  });
}
test("UI-CAPTURE-003: diagnostic retrieval/write failures cannot replace first fatal or overwrite evidence", () => {
  const first = new Error("Invalid InterceptionId.");
  for (const failure of ["read", "write", "none"]) {
    const existing = new Map([["fetch-failure-diagnostic.json", "original bytes"]]);
    const browser = { get fetchFailureDiagnosticJSON() { if (failure === "read") throw new Error("read"); return '{"bounded":true}'; } };
    assert.throws(() => { preserveFetchDiagnostic(browser as BrowserHarness, (name) => { assert.ok(!existing.has(name)); }); throw first; }, error => error === first);
    assert.equal(existing.get("fetch-failure-diagnostic.json"), "original bytes");
  }
});
test("UI-OBSERVATION-002: current acceptance rejects hidden diagnostics/secrets, overflow, duplicate headings and missing labels", () => {
  assertObservation(base, condition);
  for (const invalid of [
    { ...base, hiddenDiagnostic: true }, { ...base, secretLeak: true }, { ...base, overflow: 1 },
    { ...base, h1: [] }, { ...base, h1: ["One", "Two"] }, { ...base, lang: "wrong" },
    { ...base, controls: [{ ...base.controls[0], name: "" }] },
    { ...base, controls: [{ ...base.controls[0], tag: "INPUT", labelled: false }] },
  ]) assert.throws(() => assertObservation(invalid, condition));
});
test("UI-LIFECYCLE-002: current caller settles both sides of paint and blank before reset", async () => {
  const calls: string[] = [];
  const browser = {
    waitForRequestHandlersIdle: async () => { calls.push("idle"); },
    evaluate: async () => { calls.push("paint"); }, assertNoFatalError: () => {},
    navigate: async (url: string) => { calls.push(url); },
    clearRequestCounts: () => {}, clearNavigationCount: () => {}, clearConsoleErrors: () => {},
  };
  await navigateDocument(browser as unknown as BrowserHarness, "product", () => calls.push("reset"));
  assert.deepEqual(calls, ["idle", "paint", "idle", "about:blank", "idle", "reset", "product"]);
});
for (const fault of ["first-idle", "paint", "paint-idle", "blank", "old-idle"] as const) {
  test("UI-LIFECYCLE-003: current " + fault + " failure retains the original cause and forbids reset", async () => {
    const error = new Error(fault); let idle = 0, resets = 0, product = 0;
    const browser = {
      waitForRequestHandlersIdle: async () => { idle++; if ((fault === "first-idle" && idle === 1) || (fault === "paint-idle" && idle === 2) || (fault === "old-idle" && idle === 3)) throw error; },
      evaluate: async () => { if (fault === "paint") throw error; }, assertNoFatalError: () => {},
      navigate: async (url: string) => { if (url === "about:blank" && fault === "blank") throw error; if (url === "product") product++; },
      clearRequestCounts: () => {}, clearNavigationCount: () => {}, clearConsoleErrors: () => {},
    };
    await assert.rejects(navigateDocument(browser as unknown as BrowserHarness, "product", () => resets++), result => result === error);
    assert.equal(resets, 0); assert.equal(product, 0);
  });
}

test("UI-LAYOUT-001: layout probe parses and rejects clipped or unreachable controls beyond scrollWidth", async () => {
  const { layoutExpression, assertLayout } = await import("./layout-observation.mts");
  assert.doesNotThrow(() => new Function("return " + layoutExpression));
  assertLayout({ examined: 1, unreachable: [], clippedText: [], restored: true });
  for (const bad of [{ examined: 0, unreachable: [], clippedText: [], restored: true }, { examined: 1, unreachable: ["Submit"], clippedText: [], restored: true }, { examined: 1, unreachable: [], clippedText: ["Help"], restored: true }]) assert.throws(() => assertLayout(bad));
});
