import assert from "node:assert/strict";
import test from "node:test";
import type { BrowserHarness } from "../helpers/browser-harness.mts";
import { conditions, inventory, selectedConditions, assertExecution } from "./matrix.mts";
import { createUIFixture } from "./route-fixture.mts";
import { navigateDocument } from "./navigation.mts";
import { observationExpression } from "./observation.mts";
import { requiredScenarioNames, EXPECTED_UI_FOUNDATION_BROWSER_TESTS } from "../helpers/run-ui-foundation-browser.mts";

test("UI-MATRIX-001: specification denominators and nonready contracts are explicit", () => {
  assert.equal(inventory.surfaces.length, 28);
  assert.equal(conditions.filter(row => row.kind === "ready").length, 784);
  assert.equal(conditions.filter(row => row.kind === "shared").length, 480);
  assert.equal(new Set(conditions.map(row => row.id)).size, conditions.length);
  for (const surface of inventory.surfaces) {
    assert.equal(Object.keys(surface.states).length, 8);
    for (const state of Object.values(surface.states)) {
      assert.equal(typeof state.applicable, "boolean");
      assert.ok(state.contract.length > 30 && state.source.startsWith("web/src/"));
    }
    assert.ok(selectedConditions(surface.id).length > 28);
  }
});
test("UI-MATRIX-002: zero, missing, duplicate, failed, skipped and unreached executions fail closed", () => {
  const expected = selectedConditions("dashboard");
  const passing = expected.map(row => ({ id: row.id, status: "PASS" }));
  assertExecution(expected, passing);
  for (const invalid of [[], passing.slice(1), [...passing, passing[0]], ...["FAIL", "SKIP", "CANCELLED", "NOT_REACHED"].map(status => [{ ...passing[0], status }, ...passing.slice(1)])]) {
    assert.throws(() => assertExecution(expected, invalid));
  }
  assert.throws(() => assertExecution([], []));
  assert.throws(() => selectedConditions("unregistered"));
});
test("UI-PARITY-035: original browser IDs and exact denominator stay separate", () => {
  assert.equal(EXPECTED_UI_FOUNDATION_BROWSER_TESTS, 35);
  assert.equal(requiredScenarioNames.length, 35);
  assert.equal(new Set(requiredScenarioNames).size, 35);
  assert.ok(requiredScenarioNames.includes("false-positive guards reject invalid observable outcomes"));
});
test("UI-FIXTURE-001: current display fixture rejects unknown APIs and external requests", () => {
  const fixture = createUIFixture("http://127.0.0.1:3002");
  fixture.reset(conditions[0], "/streams");
  assert.equal(fixture.resolver({ method: "POST", url: "http://127.0.0.1:3002/not-allowed" })?.status, 404);
  assert.equal(fixture.resolver({ method: "GET", url: "https://external.invalid/" })?.status, 502);
  assert.equal(fixture.unexpected.length, 2);
  fixture.release();
});
test("UI-FIXTURE-002: pending GET is held by the real resolver until released", async () => {
  const fixture = createUIFixture("http://127.0.0.1:3002");
  fixture.reset({ ...conditions[0], state: "initial-loading" }, "/streams");
  const response = fixture.resolver({ method: "GET", url: "http://127.0.0.1:3002/streams" });
  let settled = false;
  response?.waitUntil?.then(() => { settled = true; });
  await Promise.resolve();
  assert.equal(settled, false);
  fixture.release();
  await response?.waitUntil;
  assert.equal(settled, true);
});
test("UI-LIFECYCLE-001: current caller drains old document before resetting fixture", async () => {
  const calls: string[] = [];
  const browser = {
    setFetchDiagnosticContext: (context: { phase: string }) => calls.push(context.phase),
    evaluate: async () => calls.push("paint"),
    assertNoFatalError: () => calls.push("healthy"),
    navigate: async (url: string) => calls.push(url),
    waitForRequestHandlersIdle: async () => calls.push("drain"),
    clearRequestCounts: () => calls.push("clear"), clearNavigationCount: () => calls.push("clear-navigation"), clearConsoleErrors: () => calls.push("clear-console"),
  };
  await navigateDocument(browser as unknown as BrowserHarness, "http://127.0.0.1/product", () => calls.push("reset"));
  assert.ok(calls.indexOf("about:blank") < calls.lastIndexOf("drain"));
  assert.ok(calls.lastIndexOf("drain") < calls.indexOf("reset"));
  assert.ok(calls.indexOf("reset") < calls.indexOf("http://127.0.0.1/product"));
});

test("UI-OBSERVATION-001: the browser observation expression is executable JavaScript", () => {
  assert.doesNotThrow(() => new Function("return " + observationExpression));
});

test("UI-FIXTURE-003: new Dashboard GET is explicit and denied-read permissions omit incidents.read", () => {
  const condition = conditions.find(row => row.exercise === "denied-read")!;
  assert.ok(condition);
  const fixture = createUIFixture("http://127.0.0.1:3002");
  fixture.reset(condition, "/streams");
  const me = fixture.resolver({ method: "GET", url: "http://127.0.0.1:3002/auth/me" });
  assert.ok(me);
  assert.doesNotMatch(JSON.stringify(me.body), /incidents.read/);
  fixture.release();
});
