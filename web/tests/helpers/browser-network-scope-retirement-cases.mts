import assert from "node:assert/strict";
import test from "node:test";
import { BrowserHarness } from "./browser-harness.mts";
import { BrowserNetworkCancelProof, takeNetworkCancellationTicket } from "./browser-network-cancel-proof.mts";
import { FetchRequestLifecycle, RejectableEventWaiters, invalidInterceptionIdMessage } from "./browser-request-lifecycle.mts";

const session = "retirement-session", frame = "retirement-frame", path = "/roles";
const fulfill = "Fetch.fulfillRequest" as const;
type Message = { sessionId: string; method: string; params: Record<string, unknown> };
const message = (method: string, params: Record<string, unknown>): Message => ({ sessionId: session, method, params });
const documentEvent = (loader: string, id = frame) => message("Page.frameNavigated", { frame: { id, loaderId: loader } });
const pauseEvent = (id: string, network: string) => message("Fetch.requestPaused", {
  requestId: id, networkId: network, frameId: frame, resourceType: "XHR",
  request: { method: "GET", url: `http://fixture.invalid${path}` },
});
const startEvent = (network: string, loader: string) => message("Network.requestWillBeSent", {
  requestId: network, frameId: frame, loaderId: loader, type: "Fetch",
  request: { method: "GET", url: `http://fixture.invalid${path}` },
});
const cancelledEvent = (network: string) => message("Network.loadingFailed", { requestId: network, canceled: true, errorText: "net::ERR_ABORTED" });

function directFixture(limit = 3) {
  const lifecycle = new FetchRequestLifecycle(), proof = new BrowserNetworkCancelProof(lifecycle, session, limit);
  const current = () => ({ generation: lifecycle.generation, frameId: frame });
  const observe = (value: Message) => proof.observe(value, lifecycle.generation);
  const ledger = () => proof.failureDiagnostic("unrecorded", lifecycle, session, current())!.ledger;
  const facts = (id: string, network: string, loader: string, cancelled = true, options: { method?: string; required?: boolean; pathname?: string } = {}) => {
    const method = options.method || "GET", pathname = options.pathname || path;
    lifecycle.register({ requestId: id, method, pathname, requiredResponse: options.required || false });
    const pause = pauseEvent(id, network), start = startEvent(network, loader);
    for (const value of [pause, start]) Object.assign(value.params.request as Record<string, unknown>, { method, url: `http://fixture.invalid${pathname}` });
    observe(pause);
    proof.registerContract(id, { generation: lifecycle.generation, pathname, optionalFixture: true });
    observe(start);
    if (cancelled) observe(cancelledEvent(network));
  };
  return { lifecycle, proof, current, observe, ledger, facts };
}

function harnessFixture(response: object, failFulfill = false) {
  const lifecycle = new FetchRequestLifecycle(), proof = new BrowserNetworkCancelProof(lifecycle, session, 3);
  const waiters = new RejectableEventWaiters(), sends: string[] = [], original = new Error(invalidInterceptionIdMessage);
  const browser = Object.assign(Object.create(BrowserHarness.prototype), {
    sessionId: session, requestLifecycle: lifecycle, networkCancellation: proof,
    requests: new Map(), responses: new Map(), responseStatuses: new Map(),
    eventWaiters: waiters, pendingCommands: new Map(), topLevelNavigationEvents: 0,
    topLevelNavigationPending: false, closed: false, fatalError: undefined,
    routeResolver: () => response,
    send: async (command: string) => { sends.push(command); if (failFulfill && command === fulfill) throw original; return {}; },
  }) as BrowserHarness;
  const receive = (value: Message) => (browser as unknown as { receive(raw: string): void }).receive(JSON.stringify(value));
  const navigate = async (loader: string) => {
    const navigation = browser.navigate(`http://fixture.invalid/admin/${loader}/`);
    receive(documentEvent(loader)); receive(message("Page.loadEventFired", {}));
    await navigation;
  };
  return { browser, lifecycle, proof, waiters, sends, original, receive, navigate };
}

export function registerNetworkScopeRetirementCases() {
  test("new owned documents retire bounded ledgers after invalidating the old scope", () => {
    const f = directFixture(2);
    let lastOrder = 0;
    for (let index = 0; index < 5; index++) {
      if (index) f.lifecycle.beginNavigation("navigate");
      f.observe(documentEvent(`loader-${index}`));
      assert.deepEqual(f.ledger(), [null, 0, 0, 0, 2]);
      const id = `fetch-${index}`;
      f.facts(id, `network-${index}`, `loader-${index}`);
      const ticket = f.proof.claimBeforeSettlement(id, fulfill, f.current());
      assert.ok(ticket);
      const first = f.proof.failureDiagnostic(id, f.lifecycle, session, f.current())!.first!;
      assert.ok(first.at > lastOrder); lastOrder = first.at;
      f.lifecycle.completePreSettlementCancellation(id, fulfill, ticket);
      assert.deepEqual(f.ledger(), [null, 1, 1, 1, 2]);
      assert.equal(f.proof.overflowed, false);
    }
    assert.equal(f.lifecycle.preSettlementCancellationCount, 5);
  });

  test("same-generation, same-loader, foreign, subframe and malformed events never reclaim ledgers", () => {
    const variants: [Message, number][] = [
      [documentEvent("new-loader"), 0], [documentEvent("old-loader"), 1], [documentEvent("old-loader"), 0],
      [documentEvent("new-loader", "foreign-main-frame"), 1],
      [message("Page.frameNavigated", { frame: { id: "subframe", loaderId: "sub-loader", parentId: frame } }), 1],
      [{ ...documentEvent("new-loader"), sessionId: "foreign-session" }, 1],
      [message("Page.frameNavigated", { frame: null }), 1],
      [message("Page.frameDetached", { frameId: frame }), 1], [documentEvent("new-loader"), NaN],
    ];
    for (const [value, generation] of variants) {
      const f = directFixture(); f.observe(documentEvent("old-loader"));
      f.facts("old-fetch", "old-network", "old-loader", false);
      assert.equal(f.proof.claimBeforeSettlement("old-fetch", fulfill, f.current()), undefined);
      const before = f.ledger().slice(1);
      f.proof.observe(value, generation);
      assert.deepEqual(f.ledger().slice(1), before);
    }
  });

  test("a minted old-document ticket is invalid and burned after the new scope is assigned", () => {
    const f = directFixture(); f.observe(documentEvent("old-loader"));
    f.facts("old-fetch", "old-network", "old-loader");
    const ticket = f.proof.claimBeforeSettlement("old-fetch", fulfill, f.current());
    assert.ok(ticket);
    f.lifecycle.beginNavigation("navigate"); f.observe(documentEvent("new-loader"));
    assert.deepEqual(f.ledger(), [null, 0, 0, 0, 3]);
    assert.equal(takeNetworkCancellationTicket(ticket, f.lifecycle, "old-fetch", 0, fulfill, path), false);
    assert.equal(takeNetworkCancellationTicket(ticket, f.lifecycle, "old-fetch", 0, fulfill, path), false);
    assert.throws(() => f.lifecycle.completePreSettlementCancellation("old-fetch", fulfill, ticket), /Invalid pre-settlement/);
    assert.equal(f.lifecycle.preSettlementCancellationCount, 0);
  });

  test("a failed old first claim cannot be repaired by a late cancellation after retirement", () => {
    const f = directFixture(); f.observe(documentEvent("old-loader"));
    f.facts("old-fetch", "old-network", "old-loader", false);
    assert.equal(f.proof.claimBeforeSettlement("old-fetch", fulfill, f.current()), undefined);
    f.lifecycle.beginNavigation("navigate"); f.observe(documentEvent("new-loader"));
    f.observe(cancelledEvent("old-network"));
    assert.equal(f.proof.claimBeforeSettlement("old-fetch", fulfill, f.current()), undefined);
    assert.equal(f.lifecycle.preSettlementCancellationCount, 0);
  });

  test("actual harness rejects a duplicate old Fetch ID across documents through the original lifecycle", async () => {
    const f = harnessFixture({ body: [], requiredResponse: false });
    f.receive(documentEvent("old-loader")); f.receive(pauseEvent("old-fetch", "old-network"));
    await f.browser.waitForRequestHandlersIdle({ timeoutMs: 100 });
    await f.navigate("new-loader");
    const fault = f.waiters.wait("observe-original-fatal", 1_000, "duplicate Fetch ID was not rejected");
    f.receive(pauseEvent("old-fetch", "new-network"));
    let original: unknown;
    await assert.rejects(fault.promise, error => { original = error; return error instanceof Error && error.message === "Duplicate Fetch request ID: old-fetch"; });
    assert.throws(() => f.browser.assertNoFatalError(), error => error === original);
    assert.equal(f.sends.filter(command => command === fulfill).length, 1);
    assert.equal(f.browser.responses.get(path), 1);
    assert.equal(f.lifecycle.preSettlementCancellationCount, 0);
  });

  test("actual old pending settlement retains navigation cancellation and never receives a new-scope ticket", async () => {
    let release!: () => void;
    const gate = new Promise<void>(resolve => { release = resolve; });
    const f = harnessFixture({ body: [], requiredResponse: false, waitUntil: gate }, true);
    f.receive(documentEvent("old-loader")); f.receive(pauseEvent("old-fetch", "old-network"));
    assert.equal(f.lifecycle.activeCount, 1);
    await f.navigate("new-loader");
    f.receive(cancelledEvent("old-network")); release();
    await f.browser.waitForRequestHandlersIdle({ timeoutMs: 100 });
    f.browser.assertNoFatalError();
    assert.equal(f.sends.filter(command => command === fulfill).length, 1);
    assert.equal(f.browser.responses.get(path) || 0, 0);
    assert.equal(f.lifecycle.safeCancellationCount, 1);
    assert.equal(f.lifecycle.preSettlementCancellationCount, 0);
  });

  test("within-document caps stay closed, first disable reason stays sticky and metadata is read-only", () => {
    for (const reason of ["networks", "pauses", "attempts"] as const) {
      const f = directFixture(1); f.observe(documentEvent("old-loader"));
      if (reason === "networks") { f.observe(startEvent("one", "old-loader")); f.observe(startEvent("two", "old-loader")); }
      if (reason === "pauses") { f.observe(pauseEvent("one", "same-network")); f.observe(pauseEvent("two", "same-network")); }
      if (reason === "attempts") { f.proof.claimBeforeSettlement("one", fulfill, f.current()); f.proof.claimBeforeSettlement("two", fulfill, f.current()); }
      assert.equal(f.proof.overflowed, true);
      assert.equal(f.ledger()[0], reason);
      const order = Reflect.get(f.proof, "order"), before = [...f.ledger()];
      const diagnostic = f.proof.failureDiagnostic("unrecorded", f.lifecycle, session, f.current())!;
      assert.deepEqual(diagnostic.ledger, before);
      assert.equal(Object.isFrozen(diagnostic.ledger), true);
      assert.equal(Reflect.get(f.proof, "order"), order);
      assert.ok(diagnostic.ledger.slice(1).every(value => typeof value === "number" && value <= 1));
      assert.equal(JSON.stringify(diagnostic).includes("retirement-"), false);
      f.proof.observe({ sessionId: session, method: "Network.loadingFailed", params: null }, 0);
      assert.equal(f.ledger()[0], reason, "later malformed input cannot overwrite the first reason");
      f.lifecycle.beginNavigation("navigate"); f.observe(documentEvent("new-loader"));
      assert.deepEqual(f.ledger(), [reason, 0, 0, 0, 1]);
      f.facts("fresh-fetch", "fresh-network", "new-loader");
      assert.equal(f.proof.claimBeforeSettlement("fresh-fetch", fulfill, f.current()), undefined);
      assert.equal(f.proof.overflowed, true);
    }
  });

  test("retirement never authorizes unknown continue, required reads or writes in the new document", () => {
    for (const kind of ["continue", "required", "write"] as const) {
      const f = directFixture(); f.observe(documentEvent("old-loader"));
      f.lifecycle.beginNavigation("navigate"); f.observe(documentEvent("new-loader"));
      f.facts("fresh-fetch", "fresh-network", "new-loader", true, {
        method: kind === "write" ? "PUT" : "GET", required: kind === "required", pathname: kind === "continue" ? "/unknown-continued" : path,
      });
      const command = kind === "continue" ? "Fetch.continueRequest" : fulfill;
      const ticket = f.proof.claimBeforeSettlement("fresh-fetch", command, f.current());
      if (kind !== "required") assert.equal(ticket, undefined);
      else {
        assert.ok(ticket);
        assert.throws(() => f.lifecycle.completePreSettlementCancellation("fresh-fetch", fulfill, ticket), /Invalid pre-settlement/);
      }
      const original = new Error(invalidInterceptionIdMessage);
      assert.throws(() => f.lifecycle.handleSettlementError(f.lifecycle.beginSettlement("fresh-fetch", command), original), error => error === original);
      assert.equal(f.lifecycle.safeCancellationCount, 0);
      assert.equal(f.lifecycle.preSettlementCancellationCount, 0);
    }
  });
}
