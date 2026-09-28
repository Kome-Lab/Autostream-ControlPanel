import assert from "node:assert/strict";
import test from "node:test";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { BrowserHarness } from "./helpers/browser-harness.mts";
import { assertOwnedOutput } from "./helpers/run-ui-source-comparison.mts";
import { UI_COMPARISON_BASELINE_COMMIT, apiObservationSummary, assertCaptureInventory, assertPixelIdentity, assertSameObservation, assertSourcePair, expectedUIComparisonCaptureNames, type APIObservation } from "./helpers/browser-comparison-contract.mts";


export function registerBrowserComparisonCases() {


test("UI source comparison browser denominator requires every surface, viewport and interaction", () => {
  assertCaptureInventory(expectedUIComparisonCaptureNames);
  assert.throws(() => assertCaptureInventory([]));
  assert.throws(() => assertCaptureInventory(expectedUIComparisonCaptureNames.slice(1)));
  assert.throws(() => assertCaptureInventory([...expectedUIComparisonCaptureNames, expectedUIComparisonCaptureNames[0]]));
});

test("UI source comparison API comparison retains method, query, body, response, multiplicity and mutation order", () => {
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

test("UI source comparison independent GET scheduling is not a mutation ordering allowance", () => {
  const first = { method: "GET", path: "/auth/me", body: null, status: 200 };
  const second = { method: "GET", path: "/settings/app", body: null, status: 200 };
  assertSameObservation(apiObservationSummary([first, second]), apiObservationSummary([second, first]));
});

test("UI source comparison visual comparison rejects one changed channel with no threshold or mask", () => {
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

test("UI source comparison observation comparison rejects focus, permission, URL and hidden errors", () => {
  const before = { route: "/admin/streams/?view=active#preview", focus: "Start", disabled: false, errors: 0 };
  assertSameObservation(before, structuredClone(before));
  for (const after of [
    { ...before, route: "/admin/streams/" }, { ...before, focus: "body" },
    { ...before, disabled: true }, { ...before, errors: 1 },
  ]) assert.throws(() => assertSameObservation(before, after));
});

test("UI source comparison source pair rejects refreshed before, stale after and a dummy same-source comparison", () => {
  const current = "a".repeat(40);
  assertSourcePair(UI_COMPARISON_BASELINE_COMMIT, current, current);
  assert.throws(() => assertSourcePair(current, current, current));
  assert.throws(() => assertSourcePair(UI_COMPARISON_BASELINE_COMMIT, current, "b".repeat(40)));
  assert.throws(() => assertSourcePair(UI_COMPARISON_BASELINE_COMMIT, UI_COMPARISON_BASELINE_COMMIT, UI_COMPARISON_BASELINE_COMMIT));
});

test("UI source comparison CDP setup fixes the same locale, timezone and pre-hydration document script", async () => {
  const calls: { method: string; params: unknown }[] = [];
  const fake = { async send(method: string, params: unknown) { calls.push({ method, params }); } };
  await BrowserHarness.prototype.configureDeterministicDocument.call(fake as never, { source: "Date.now = () => 123;", timezone: "Asia/Tokyo", locale: "ja-JP" });
  assert.deepEqual(calls, [
    { method: "Emulation.setTimezoneOverride", params: { timezoneId: "Asia/Tokyo" } },
    { method: "Emulation.setLocaleOverride", params: { locale: "ja-JP" } },
    { method: "Page.addScriptToEvaluateOnNewDocument", params: { source: "Date.now = () => 123;" } },
  ]);
});

test("UI source comparison screenshot helper captures the entire product document and rejects empty geometry", async () => {
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

test("UI source comparison before/after output refuses existing paths and paths outside the owned CI directory", () => {
  const directory = dirname(fileURLToPath(import.meta.url));
  assertOwnedOutput(directory, resolve(directory, "bundle9-output-must-not-exist"));
  assert.throws(() => assertOwnedOutput(directory, directory));
  assert.throws(() => assertOwnedOutput(directory, resolve(directory, "..", "escaped")));
  assert.throws(() => assertOwnedOutput(dirname(directory), directory), /already exists/);
});
}
