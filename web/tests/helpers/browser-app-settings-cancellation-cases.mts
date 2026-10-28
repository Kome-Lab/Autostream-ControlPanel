import assert from "node:assert/strict";
import test from "node:test";
import { BrowserHarness } from "./browser-harness.mts";
import { BrowserNetworkCancelProof } from "./browser-network-cancel-proof.mts";
import { FetchRequestLifecycle, RejectableEventWaiters, invalidInterceptionIdMessage } from "./browser-request-lifecycle.mts";
import { optionalAppCancellationEvent, waitForOptionalAppCancellation } from "./browser-optional-app-cancellation.mts";

type Event = { sessionId: string; method: string; params: Record<string, unknown>; generation: number };
type Contract = { generation: number; pathname: string; optionalFixture: boolean };
type Step = Event | { contract: Contract };
const session = "owned-session", frame = "owned-main-frame", loader = "owned-loader";
const id = "owned-fetch", networkID = "owned-network", path = "/integrations/oauth-accounts";
const fulfill = "Fetch.fulfillRequest" as const;
const current = { generation: 0, frameId: frame };
const event = (method: string, params: Record<string, unknown>, generation = 0): Event => ({ sessionId: session, method, params, generation });
const doc = () => event("Page.frameNavigated", { frame: { id: frame, loaderId: loader } });
const pause = (pathname = path) => event("Fetch.requestPaused", {
  requestId: id, networkId: networkID, frameId: frame, resourceType: "XHR",
  request: { method: "GET", url: `http://fixture.invalid${pathname}?owned=one` },
});
const start = (pathname = path) => event("Network.requestWillBeSent", {
  requestId: networkID, frameId: frame, loaderId: loader, type: "Fetch",
  request: { method: "GET", url: `http://fixture.invalid${pathname}?owned=one` },
});
const cancelled = () => event("Network.loadingFailed", { requestId: networkID, canceled: true, errorText: "net::ERR_ABORTED" });
const contract = (pathname = path): Step => ({ contract: { generation: 0, pathname, optionalFixture: true } });
function steps(pathname = path): Step[] { return [doc(), pause(pathname), contract(pathname), start(pathname), cancelled()]; }
function isEvent(step: Step): step is Event { return "method" in step; }
function changeEvent(input: Step[], method: string, change: (value: Event) => void) {
  const value = input.find((step): step is Event => isEvent(step) && step.method === method);
  assert.ok(value); change(value);
}
function request(value: Event) { return value.params.request as Record<string, unknown>; }
function fixture(input: Step[] = steps(), options: { required?: boolean; pathname?: string; method?: string } = {}) {
  const lifecycle = new FetchRequestLifecycle();
  lifecycle.register({ requestId: id, method: options.method ?? "GET", pathname: options.pathname ?? path, requiredResponse: options.required ?? false });
  const proof = new BrowserNetworkCancelProof(lifecycle, session);
  for (const step of input) {
    if (isEvent(step)) proof.observe(step, step.generation);
    else proof.registerContract(id, step.contract);
  }
  return { lifecycle, proof, claim: () => proof.claimBeforeSettlement(id, fulfill, current) };
}

// Reuse all original tuple mutations unchanged; this module owns only the new
// app-settings observation and exact eighth-path regression cases.
export function registerAppSettingsCancellationCases(negatives: readonly [string, (input: Step[]) => void][]) {
  test("app-settings observation waits for its owned cancellation before the unchanged first claim", async () => {
    const pathname = "/settings/app", lifecycle = new FetchRequestLifecycle();
    const proof = new BrowserNetworkCancelProof(lifecycle, session), waiters = new RejectableEventWaiters(), sends: string[] = [];
    const browser = Object.assign(Object.create(BrowserHarness.prototype), {
      sessionId: session, requestLifecycle: lifecycle, networkCancellation: proof,
      requests: new Map([[pathname, 1]]), responses: new Map([[pathname, 1]]), responseStatuses: new Map([[pathname, [200]]]),
      eventWaiters: waiters, pendingCommands: new Map(), topLevelNavigationEvents: 0,
      topLevelNavigationPending: false, closed: false, fatalError: undefined,
      routeResolver: () => ({ body: { app_name: "AutoStream", timezone: "Asia/Tokyo" }, requiredResponse: false, observeAppSettingsCancellation: true }),
      send: async (command: string) => { sends.push(command); return {}; },
    }) as BrowserHarness;
    const receive = (value: Event) => (browser as unknown as { receive(raw: string): void }).receive(JSON.stringify(value));
    receive(doc()); receive(pause(pathname));
    assert.equal(waiters.pendingCount, 1);
    assert.equal(proof.failureDiagnostic(id, lifecycle, session, current)?.first, null);
    const unrelated = cancelled(); unrelated.params.requestId = "unrelated-network"; receive(unrelated);
    const foreign = cancelled(); foreign.sessionId = "foreign-session"; receive(foreign);
    await Promise.resolve();
    assert.equal(waiters.pendingCount, 1);
    assert.deepEqual(sends, []);
    receive(start(pathname)); receive(cancelled());
    await browser.waitForRequestHandlersIdle({ pathname, timeoutMs: 100 });
    browser.assertNoFatalError();
    assert.equal(waiters.pendingCount, 0);
    assert.deepEqual(sends, []);
    assert.equal(browser.requests.get(pathname), 2);
    assert.equal(browser.responses.get(pathname), 1);
    assert.deepEqual(browser.responseStatuses.get(pathname), [200]);
    assert.equal(lifecycle.activeCount, 0);
    assert.equal(lifecycle.preSettlementCancellationCount, 1);
    assert.equal(lifecycle.safeCancellationCount, 1);
    const first = proof.failureDiagnostic(id, lifecycle, session, current)?.first;
    assert.deepEqual(first?.present, [1, 1, 1, 1, 1]);
    assert.ok(first!.orders[4]! < first!.at, "real cancellation preceded the first and only claim");
    assert.equal(proof.claimBeforeSettlement(id, fulfill, current), undefined);
  });

  test("app-settings cancellation preserves all existing tuple rejections and the original fatal identity", () => {
    const pathname = "/settings/app";
    for (const [name, mutate] of negatives) {
      const input = steps(pathname); mutate(input);
      for (const step of input) {
        if (isEvent(step)) {
          const value = step.params.request as Record<string, unknown> | undefined;
          if (typeof value?.url === "string") value.url = value.url.replace(path, pathname);
        } else if (step.contract.pathname === path) step.contract.pathname = pathname;
      }
      const { lifecycle, claim } = fixture(input, { pathname });
      assert.equal(claim(), undefined, name);
      const original = new Error(invalidInterceptionIdMessage);
      assert.throws(() => lifecycle.handleSettlementError(lifecycle.beginSettlement(id, fulfill), original), error => error === original, name);
      assert.equal(lifecycle.activeCount, 1);
      assert.equal(lifecycle.safeCancellationCount, 0);
      assert.equal(lifecycle.preSettlementCancellationCount, 0);
    }
  });

  test("app-settings required reads, writes, subroutes, continue and late cancellation remain fatal", () => {
    const pathname = "/settings/app", input = steps(pathname);
    (input[2] as { contract: Contract }).contract.optionalFixture = false;
    const required = fixture(input, { pathname, required: true });
    assert.equal(required.claim(), undefined);
    const original = new Error(invalidInterceptionIdMessage);
    assert.throws(() => required.lifecycle.handleSettlementError(required.lifecycle.beginSettlement(id, fulfill), original), error => error === original);
    for (const method of ["HEAD", "POST", "PUT", "PATCH", "DELETE"]) {
      const value = steps(pathname);
      for (const eventName of ["Fetch.requestPaused", "Network.requestWillBeSent"]) changeEvent(value, eventName, entry => { request(entry).method = method; });
      const { lifecycle, claim } = fixture(value, { pathname, method });
      assert.equal(claim(), undefined, method);
      assert.throws(() => lifecycle.handleSettlementError(lifecycle.beginSettlement(id, fulfill), original), error => error === original);
    }
    for (const unknown of ["/settings/app/private", "/settings/apps", "/settings/app-other"]) {
      const { lifecycle, claim } = fixture(steps(unknown), { pathname: unknown });
      assert.equal(claim(), undefined);
      assert.throws(() => lifecycle.handleSettlementError(lifecycle.beginSettlement(id, fulfill), original), error => error === original);
    }
    assert.equal(fixture(steps(pathname), { pathname }).proof.claimBeforeSettlement(id, "Fetch.continueRequest", current), undefined);
    const late = fixture(steps(pathname).slice(0, -1), { pathname });
    assert.equal(late.claim(), undefined);
    late.proof.observe(cancelled(), 0);
    assert.equal(late.claim(), undefined);
    assert.throws(() => late.lifecycle.handleSettlementError(late.lifecycle.beginSettlement(id, fulfill), original), error => error === original);
    assert.equal(late.lifecycle.preSettlementCancellationCount, 0);
  });

  test("app-settings observation times out once into normal settlement without creating a cancellation", async t => {
    t.mock.timers.enable({ apis: ["setTimeout"] });
    const pathname = "/settings/app", lifecycle = new FetchRequestLifecycle();
    const proof = new BrowserNetworkCancelProof(lifecycle, session), waiters = new RejectableEventWaiters(), sends: string[] = [];
    const browser = Object.assign(Object.create(BrowserHarness.prototype), {
      sessionId: session, requestLifecycle: lifecycle, networkCancellation: proof,
      requests: new Map(), responses: new Map(), responseStatuses: new Map(),
      eventWaiters: waiters, pendingCommands: new Map(), topLevelNavigationEvents: 0,
      topLevelNavigationPending: false, closed: false, fatalError: undefined,
      routeResolver: () => ({ body: { app_name: "AutoStream", timezone: "Asia/Tokyo" }, requiredResponse: false, observeAppSettingsCancellation: true }),
      send: async (command: string) => { sends.push(command); return {}; },
    }) as BrowserHarness;
    const receive = (value: Event) => (browser as unknown as { receive(raw: string): void }).receive(JSON.stringify(value));
    receive(doc()); receive(pause(pathname)); receive(start(pathname));
    assert.equal(waiters.pendingCount, 1);
    t.mock.timers.tick(4_999); await Promise.resolve();
    assert.deepEqual(sends, []);
    t.mock.timers.tick(1);
    await browser.waitForRequestHandlersIdle({ pathname, timeoutMs: 100 });
    browser.assertNoFatalError();
    assert.equal(waiters.pendingCount, 0);
    assert.deepEqual(sends, [fulfill]);
    assert.equal(browser.responses.get(pathname), 1);
    assert.deepEqual(browser.responseStatuses.get(pathname), [200]);
    assert.equal(proof.failureDiagnostic(id, lifecycle, session, current)?.first?.present[4], 0);
    receive(cancelled());
    assert.equal(proof.claimBeforeSettlement(id, fulfill, current), undefined, "late events cannot replace the spent first claim");
    assert.equal(lifecycle.safeCancellationCount, 0);
    assert.equal(lifecycle.preSettlementCancellationCount, 0);
  });

  test("app-settings observation propagates the exact fatal or close cause and releases its waiter", async () => {
    for (const original of [new Error("fatal fixture failure"), new Error("Browser harness closed"), new Error("Optional app-settings cancellation observation window elapsed")]) {
      const waiters = new RejectableEventWaiters();
      const observation = waitForOptionalAppCancellation(waiters, { method: "GET", url: "http://fixture.invalid/settings/app" }, { requiredResponse: false, observeAppSettingsCancellation: true }, networkID);
      waiters.rejectAll(original);
      await assert.rejects(observation, error => error === original);
      assert.equal(waiters.pendingCount, 0);
    }
  });

  test("app-settings observation opt-in cannot wait for required, unknown, write or malformed Network identities", async () => {
    for (const [method, pathname, required, enabled, network] of [
      ["GET", "/settings/app", true, true, networkID], ["GET", "/settings/app/private", false, true, networkID],
      ["POST", "/settings/app", false, true, networkID], ["GET", "/settings/app", false, undefined, networkID],
      ["GET", "/settings/app", false, true, undefined], ["GET", "/settings/app", false, true, ""],
    ] as const) {
      const waiters = new RejectableEventWaiters();
      await waitForOptionalAppCancellation(waiters, { method, url: `http://fixture.invalid${pathname}` }, { requiredResponse: required, observeAppSettingsCancellation: enabled }, network);
      assert.equal(waiters.pendingCount, 0);
    }
    assert.equal(optionalAppCancellationEvent(""), undefined);
    assert.equal(optionalAppCancellationEvent("x".repeat(257)), undefined);
  });
}
