import assert from "node:assert/strict";
import test from "node:test";
import { BrowserHarness, type RouteResolver, type StubResponse } from "./browser-harness.mts";
import { CommandDeadlines, type PendingCommand } from "./browser-command-deadline.mts";
import { BrowserNetworkCancelProof } from "./browser-network-cancel-proof.mts";
import { FetchRequestLifecycle, RejectableEventWaiters, invalidInterceptionIdMessage } from "./browser-request-lifecycle.mts";
import { observeInFlightOAuthDocument, prepareInFlightOAuthCancellation, takeInFlightOAuthCancellation } from "./browser-inflight-oauth-cancellation.mts";
import { withTimeout } from "./browser-process-timing.mts";
import { createBrowserRouteFixture } from "../ui-browser-fixture.mts";

type Message = { sessionId?: string; id?: number; method?: string; params?: Record<string, unknown>; result?: Record<string, unknown>; error?: { message: string } };
type Wire = { id: number; sessionId: string; method: string; params: Record<string, unknown> };
type SocketWaiter = { fetch: string; resolve: (wire: Wire) => void; reject: (error: Error) => void; timer: ReturnType<typeof setTimeout> };
type Internals = {
  receive(raw: string): void; rejectPendingCommands(error: Error): void; recordFatalError(error: unknown): void;
  closed: boolean; fatalError: Error | undefined; pendingCommands: Map<number, PendingCommand>;
};
const session = "inflight-owned-session", frame = "inflight-owned-frame", loader = "inflight-owned-loader";
const pathname = "/integrations/oauth-accounts", baseURL = "http://127.0.0.1:3002", url = baseURL + pathname;
const fetchID = "inflight-owned-fetch", networkID = "inflight-owned-network", peerFetch = "inflight-required-peer-fetch";
const observed: StubResponse = { body: [], requiredResponse: false, observeInFlightOAuthCancellation: true };
const event = (method: string, params: Record<string, unknown>): Message => ({ sessionId: session, method, params });
const documentEvent = (nextLoader = loader) => event("Page.frameNavigated", { frame: { id: frame, loaderId: nextLoader, url: baseURL + "/admin/streams/" } });
const start = (id = networkID) => event("Network.requestWillBeSent", { requestId: id, frameId: frame, loaderId: loader, type: "Fetch", request: { method: "GET", url } });
const pause = (id = fetchID, network = networkID) => event("Fetch.requestPaused", { requestId: id, networkId: network, frameId: frame, resourceType: "XHR", request: { method: "GET", url } });
const cancel = (id = networkID) => event("Network.loadingFailed", { requestId: id, canceled: true, errorText: "net::ERR_ABORTED" });
const request = (message: Message) => message.params?.request as Record<string, unknown>;

function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>(accept => { resolve = accept; });
  return { promise, resolve };
}

class FakeSocket {
  readonly commands: Wire[] = [];
  throwOnSend: Error | undefined;
  private readonly waiting = new Set<SocketWaiter>();
  send(raw: string) {
    const wire = JSON.parse(raw) as Wire;
    assert.ok(Number.isSafeInteger(wire.id) && wire.id > 0);
    if (this.throwOnSend) throw this.throwOnSend;
    this.commands.push(wire);
    for (const waiter of this.waiting) {
      if (wire.params.requestId !== waiter.fetch) continue;
      clearTimeout(waiter.timer); this.waiting.delete(waiter); waiter.resolve(wire);
    }
  }
  next(fetch: string) {
    const existing = this.commands.find(wire => wire.params.requestId === fetch);
    if (existing) return Promise.resolve(existing);
    return new Promise<Wire>((resolve, reject) => {
      const waiter: SocketWaiter = { fetch, resolve, reject, timer: setTimeout(() => {
        this.waiting.delete(waiter); reject(new Error("actual HH did not send the expected fixture command"));
      }, 1_000) };
      this.waiting.add(waiter);
    });
  }
  dispose(error: Error) {
    for (const waiter of this.waiting) { clearTimeout(waiter.timer); waiter.reject(error); }
    this.waiting.clear();
  }
}

function fixture(response: StubResponse | null = observed, limit = 2048) {
  const lifecycle = new FetchRequestLifecycle(), proof = new BrowserNetworkCancelProof(lifecycle, session, limit);
  const socket = new FakeSocket(), waiters = new RejectableEventWaiters(), pendingCommands = new Map<number, PendingCommand>();
  const fatal = deferred<unknown>(), watched: Promise<unknown>[] = [];
  const commandDeadlines = new CommandDeadlines(error => { internal.recordFatalError(error); internal.rejectPendingCommands(error); });
  const browser = Object.assign(Object.create(BrowserHarness.prototype), {
    socket, sessionId: session, nextCommandId: 0, pendingCommands, commandDeadlines,
    requests: new Map<string, number>(), responses: new Map<string, number>(), responseStatuses: new Map<string, number[]>(),
    eventWaiters: waiters, requestLifecycle: lifecycle, networkCancellation: proof, routeResolver: () => response,
    operationBoundary: undefined, manualHeaded: false, closed: false, fatalError: undefined,
    diagnosticNavigateResponded: false, topLevelNavigationEvents: 0, topLevelNavigationPending: false,
    generationFailureReported: false, fetchFailureReported: false, consoleErrorEvents: 0,
  }) as BrowserHarness;
  const internal = browser as unknown as Internals;
  const record = internal.recordFatalError.bind(browser);
  internal.recordFatalError = error => { record(error); fatal.resolve(error); };
  const watch = <T>(promise: Promise<T>) => { void promise.catch(() => {}); watched.push(promise); return promise; };
  const receive = (message: Message) => internal.receive(JSON.stringify(message));
  const fail = (wire: Wire, text = invalidInterceptionIdMessage, afterReject?: () => void, responseSession = session) => {
    const command = pendingCommands.get(wire.id); assert.ok(command);
    const reject = command.reject;
    let original: Error | undefined;
    command.reject = error => { original = error; reject(error); afterReject?.(); };
    receive({ id: wire.id, sessionId: responseSession, error: { message: text } });
    assert.ok(original, "actual receive must construct and reject with the original command Error");
    return original;
  };
  return {
    browser, lifecycle, proof, socket, internal, receive, fail,
    wire: (id = fetchID) => watch(socket.next(id)),
    idle: () => watch(browser.waitForRequestHandlersIdle({ timeoutMs: 1_000 })),
    fatalAttempt: () => watch(withTimeout(fatal.promise, 1_000, "actual HH did not handle the expected fatal rejection")),
    ack: (wire: Wire) => receive({ id: wire.id, sessionId: session, result: {} }),
    async cleanup() {
      const error = new Error("in-flight fake CDP fixture cleanup");
      internal.rejectPendingCommands(error); waiters.rejectAll(error); socket.dispose(error); lifecycle.close(error);
      await Promise.allSettled(watched);
      assert.equal(pendingCommands.size, 0); assert.equal(waiters.pendingCount, 0);
    },
  };
}
type Fixture = ReturnType<typeof fixture>;

function begin(owner: Fixture, messages = [documentEvent(), start(), pause()]) {
  for (const message of messages) owner.receive(message);
}
function noRetirement(owner: Fixture) {
  assert.equal(owner.lifecycle.observedInFlightOAuthCancellationCount, 0);
  assert.equal(owner.lifecycle.preSettlementCancellationCount, 0);
  assert.equal(owner.lifecycle.safeCancellationCount, 0);
  assert.equal(owner.browser.responses.get(pathname) || 0, 0);
  assert.deepEqual(owner.browser.responseStatuses.get(pathname) || [], []);
}
async function originalFatal(owner: Fixture, waiting: Promise<void>, original: Error) {
  await assert.rejects(waiting, error => error === original);
  assert.throws(() => owner.browser.assertNoFatalError(), error => error === original);
  noRetirement(owner);
  assert.equal(owner.proof.claimBeforeSettlement(fetchID, "Fetch.fulfillRequest", { generation: owner.lifecycle.generation, frameId: frame }), undefined);
}

type Refusal = { name: string; response?: StubResponse | null; limit?: number; mutate?: (messages: Message[]) => void; pending?: (owner: Fixture) => void; error?: string };
const refusals: Refusal[] = [
  { name: "default opt-in absent", response: { body: [], requiredResponse: false } },
  { name: "explicit opt-in false", response: { ...observed, observeInFlightOAuthCancellation: false } },
  { name: "required GET", response: { ...observed, requiredResponse: true } },
  { name: "continueRequest", response: null },
  { name: "missing owned document", mutate: m => { m.shift(); } },
  { name: "missing document origin", mutate: m => { delete (m[0].params?.frame as Record<string, unknown>).url; } },
  { name: "missing Network frame", mutate: m => { delete m[1].params?.frameId; } },
  { name: "missing pause frame", mutate: m => { delete m[2].params?.frameId; } },
  { name: "missing loader", mutate: m => { delete m[1].params?.loaderId; } },
  { name: "foreign Network session", mutate: m => { m[1].sessionId = "foreign-session"; } },
  { name: "missing pause session", mutate: m => { delete m[2].sessionId; } },
  { name: "foreign Network frame", mutate: m => { if (m[1].params) m[1].params.frameId = "foreign-frame"; } },
  { name: "foreign pause frame", mutate: m => { if (m[2].params) m[2].params.frameId = "foreign-frame"; } },
  { name: "foreign loader", mutate: m => { if (m[1].params) m[1].params.loaderId = "foreign-loader"; } },
  { name: "wrong Network type", mutate: m => { if (m[1].params) m[1].params.type = "XHR"; } },
  { name: "wrong pause type", mutate: m => { if (m[2].params) m[2].params.resourceType = "Fetch"; } },
  { name: "foreign origin", mutate: m => { request(m[1]).url = request(m[2]).url = "http://127.0.0.1:3003" + pathname; } },
  { name: "query", mutate: m => { request(m[1]).url = request(m[2]).url = url + "?extra=one"; } },
  { name: "wrong Network join", mutate: m => { if (m[2].params) m[2].params.networkId = "other-network"; } },
  { name: "duplicate Network start", mutate: m => { m.splice(2, 0, start()); } },
  { name: "duplicate cancellation", pending: owner => owner.receive(cancel()) },
  { name: "response received", pending: owner => owner.receive(event("Network.responseReceived", { requestId: networkID })) },
  { name: "loading finished", pending: owner => owner.receive(event("Network.loadingFinished", { requestId: networkID })) },
  { name: "changed document", pending: owner => owner.receive(documentEvent("replacement-loader")) },
  { name: "owned frame detached", pending: owner => owner.receive(event("Page.frameDetached", { frameId: frame })) },
  { name: "unknown at arm cannot be repaired", mutate: m => { m.splice(1, 1); }, pending: owner => owner.receive(start()) },
  { name: "overflow after arm", limit: 1, pending: owner => owner.receive(start("overflow-network")) },
  { name: "replaced active entry", pending: owner => {
    const active = Reflect.get(owner.lifecycle, "activeRequests") as Map<string, object>;
    const entry = active.get(fetchID); assert.ok(entry); active.set(fetchID, { ...entry });
  } },
  { name: "non-InvalidID error", error: "Target closed" },
  { name: "malformed InvalidID error", error: "Invalid InterceptionId" },
];

export function registerInFlightOAuthCancellationCases() {
  test("in-flight OAuth actual wire retirement preserves a held required peer and global idle", async () => {
    const owner = fixture(), gate = deferred<void>();
    owner.browser.setRouteResolver(input => new URL(input.url).pathname === "/required-peer"
      ? { body: { ok: true }, requiredResponse: true, waitUntil: gate.promise } : observed);
    const complete = owner.lifecycle.completeObservedInFlightOAuthCancellation.bind(owner.lifecycle);
    let used: Parameters<typeof complete> | undefined;
    owner.lifecycle.completeObservedInFlightOAuthCancellation = (attempt, error) => { used = [attempt, error]; return complete(attempt, error); };
    try {
      begin(owner); const wire = await owner.wire();
      const peer = pause(peerFetch, "required-peer-network"); request(peer).url = baseURL + "/required-peer"; owner.receive(peer);
      const active = Reflect.get(owner.lifecycle, "activeRequests") as Map<string, unknown>;
      assert.equal(active.has(peerFetch), true);
      let idle = false; const waiting = owner.idle().then(() => { idle = true; }); void waiting.catch(() => {});
      owner.receive(cancel()); owner.fail(wire);
      await withTimeout(owner.browser.waitForRequestHandlersIdle({ pathname, timeoutMs: 1_000 }), 1_000, "OAuth retirement stayed pending");
      owner.browser.assertNoFatalError();
      assert.equal(owner.lifecycle.observedInFlightOAuthCancellationCount, 1);
      assert.equal(owner.lifecycle.safeCancellationCount, 1); assert.equal(owner.lifecycle.preSettlementCancellationCount, 0);
      assert.equal(owner.browser.responses.get(pathname) || 0, 0); assert.deepEqual(owner.browser.responseStatuses.get(pathname) || [], []);
      assert.equal(owner.socket.commands.length, 1); assert.equal(wire.method, "Fetch.fulfillRequest");
      assert.equal(active.has(fetchID), false); assert.equal(active.has(peerFetch), true); assert.equal(idle, false);
      assert.ok(used); assert.equal(complete(...used), false, "the consumed Error receipt cannot retire twice");
      assert.equal(owner.proof.claimBeforeSettlement(fetchID, "Fetch.fulfillRequest", { generation: 0, frameId: frame }), undefined);
      gate.resolve(); const peerWire = await owner.wire(peerFetch); owner.ack(peerWire); await waiting;
      assert.equal(idle, true); assert.equal(owner.lifecycle.activeCount, 0); assert.equal(owner.socket.commands.length, 2);
      assert.equal(owner.browser.responses.get("/required-peer"), 1); assert.deepEqual(owner.browser.responseStatuses.get("/required-peer"), [200]);
      assert.equal(owner.lifecycle.observedInFlightOAuthCancellationCount, 1);
    } finally { gate.resolve(); await owner.cleanup(); }
  });

  for (const entry of refusals) test("in-flight OAuth actual wire rejects " + entry.name, async () => {
    const owner = fixture(entry.response === undefined ? observed : entry.response, entry.limit), messages = [documentEvent(), start(), pause()];
    entry.mutate?.(messages);
    try {
      begin(owner, messages); const wire = await owner.wire(); const waiting = owner.idle();
      entry.pending?.(owner); owner.receive(cancel()); const original = owner.fail(wire, entry.error);
      await originalFatal(owner, waiting, original); assert.equal(owner.socket.commands.length, 1);
    } finally { await owner.cleanup(); }
  });

  for (const method of ["HEAD", "POST", "PUT", "PATCH", "DELETE"]) test("in-flight OAuth does not authorize method " + method, async () => {
    const owner = fixture(), messages = [documentEvent(), start(), pause()]; request(messages[1]).method = request(messages[2]).method = method;
    try { begin(owner, messages); const wire = await owner.wire(), waiting = owner.idle(); owner.receive(cancel()); await originalFatal(owner, waiting, owner.fail(wire)); }
    finally { await owner.cleanup(); }
  });

  for (const other of ["/integrations/oauth-providers", "/roles", "/auth/me", "/version", pathname + "/private"]) test("in-flight OAuth does not authorize route " + other, async () => {
    const owner = fixture(), messages = [documentEvent(), start(), pause()]; request(messages[1]).url = request(messages[2]).url = baseURL + other;
    try { begin(owner, messages); const wire = await owner.wire(), waiting = owner.idle(); owner.receive(cancel()); await originalFatal(owner, waiting, owner.fail(wire)); }
    finally { await owner.cleanup(); }
  });

  test("in-flight OAuth cancel after wire error in the same receive callback remains fatal", async () => {
    const owner = fixture();
    try {
      begin(owner); const wire = await owner.wire(), waiting = owner.idle();
      const original = owner.fail(wire, invalidInterceptionIdMessage, () => owner.receive(cancel()));
      await originalFatal(owner, waiting, original);
    } finally { await owner.cleanup(); }
  });

  test("in-flight OAuth duplicate wire error after pending-map deletion invalidates the receipt", async () => {
    const owner = fixture();
    try {
      begin(owner); const wire = await owner.wire(), waiting = owner.idle(); owner.receive(cancel());
      const original = owner.fail(wire, invalidInterceptionIdMessage, () => owner.receive({ id: wire.id, sessionId: session, error: { message: invalidInterceptionIdMessage } }));
      await originalFatal(owner, waiting, original);
    } finally { await owner.cleanup(); }
  });

  test("in-flight OAuth ACK then cancel preserves the normal success and grants no retirement", async () => {
    const owner = fixture();
    try {
      begin(owner); const wire = await owner.wire(), waiting = owner.idle(); owner.ack(wire); owner.receive(cancel()); await waiting;
      owner.browser.assertNoFatalError(); assert.equal(owner.lifecycle.observedInFlightOAuthCancellationCount, 0);
      assert.equal(owner.lifecycle.safeCancellationCount, 0); assert.equal(owner.lifecycle.preSettlementCancellationCount, 0);
      assert.equal(owner.browser.responses.get(pathname), 1); assert.deepEqual(owner.browser.responseStatuses.get(pathname), [200]);
      assert.equal(owner.socket.commands.length, 1); assert.equal(owner.lifecycle.activeCount, 0);
    } finally { await owner.cleanup(); }
  });

  test("in-flight OAuth opt-in preserves the old pre-send cancellation proof", async () => {
    const gate = deferred<void>(), owner = fixture({ ...observed, waitUntil: gate.promise });
    try {
      begin(owner); const waiting = owner.idle(); owner.receive(cancel()); gate.resolve(); await waiting;
      owner.browser.assertNoFatalError(); assert.equal(owner.socket.commands.length, 0);
      assert.equal(owner.lifecycle.observedInFlightOAuthCancellationCount, 0);
      assert.equal(owner.lifecycle.preSettlementCancellationCount, 1); assert.equal(owner.lifecycle.safeCancellationCount, 1);
      assert.equal(owner.browser.responses.get(pathname) || 0, 0); assert.deepEqual(owner.browser.responseStatuses.get(pathname) || [], []);
    } finally { gate.resolve(); await owner.cleanup(); }
  });

  test("in-flight OAuth cannot replace the existing newer-navigation cancellation rule", async () => {
    const owner = fixture();
    try {
      begin(owner); const wire = await owner.wire(), waiting = owner.idle();
      owner.lifecycle.beginNavigation("navigate"); owner.receive(cancel()); owner.fail(wire); await waiting;
      owner.browser.assertNoFatalError(); assert.equal(owner.lifecycle.observedInFlightOAuthCancellationCount, 0);
      assert.equal(owner.lifecycle.preSettlementCancellationCount, 0); assert.equal(owner.lifecycle.safeCancellationCount, 1);
      assert.equal(owner.browser.responses.get(pathname) || 0, 0); assert.deepEqual(owner.browser.responseStatuses.get(pathname) || [], []);
    } finally { await owner.cleanup(); }
  });

  test("in-flight OAuth socket throw before wire send preserves the original fatal", async () => {
    const owner = fixture(), original = new Error(invalidInterceptionIdMessage);
    owner.socket.throwOnSend = original;
    try {
      begin(owner); await originalFatal(owner, owner.idle(), original); assert.equal(owner.socket.commands.length, 0);
    } finally { await owner.cleanup(); }
  });

  test("in-flight OAuth cannot accept a foreign-session CDP error", async () => {
    const owner = fixture();
    try {
      begin(owner); const wire = await owner.wire(), waiting = owner.idle(); owner.receive(cancel());
      await originalFatal(owner, waiting, owner.fail(wire, invalidInterceptionIdMessage, undefined, "foreign-session"));
    } finally { await owner.cleanup(); }
  });

  test("in-flight OAuth copied actual first attempt remains fatal", async () => {
    const owner = fixture(), beginAttempt = owner.lifecycle.beginSettlement.bind(owner.lifecycle);
    owner.lifecycle.beginSettlement = (id, command) => ({ ...beginAttempt(id, command) });
    try { begin(owner); const wire = await owner.wire(), waiting = owner.idle(); owner.receive(cancel()); await originalFatal(owner, waiting, owner.fail(wire)); }
    finally { await owner.cleanup(); }
  });

  for (const fault of ["owner", "attempt"]) test("in-flight OAuth taking a genuine Error receipt with wrong " + fault + " burns it", async () => {
    const owner = fixture(), complete = owner.lifecycle.completeObservedInFlightOAuthCancellation.bind(owner.lifecycle);
    owner.lifecycle.completeObservedInFlightOAuthCancellation = (attempt, error) => {
      assert.equal(takeInFlightOAuthCancellation(error, fault === "owner" ? {} : owner.lifecycle, fault === "attempt" ? { ...attempt } : attempt, true), false);
      return complete(attempt, error);
    };
    try { begin(owner); const wire = await owner.wire(), waiting = owner.idle(); owner.receive(cancel()); await originalFatal(owner, waiting, owner.fail(wire)); }
    finally { await owner.cleanup(); }
  });

  test("in-flight OAuth cloned Error with the genuine attempt burns the original receipt", async () => {
    const owner = fixture(), complete = owner.lifecycle.completeObservedInFlightOAuthCancellation.bind(owner.lifecycle);
    owner.lifecycle.completeObservedInFlightOAuthCancellation = (attempt, error) => {
      assert.ok(error instanceof Error);
      assert.equal(takeInFlightOAuthCancellation(new Error(error.message), owner.lifecycle, attempt, true), false);
      return complete(attempt, error);
    };
    try { begin(owner); const wire = await owner.wire(), waiting = owner.idle(); owner.receive(cancel()); await originalFatal(owner, waiting, owner.fail(wire)); }
    finally { await owner.cleanup(); }
  });

  for (const malformed of ["missing loader", "wrong loader", "subframe"]) test("in-flight OAuth origin observer cannot repair an unknown origin using " + malformed, async () => {
    const owner = fixture(), initial = documentEvent(), repair = documentEvent();
    delete (initial.params?.frame as Record<string, unknown>).url;
    const candidate = repair.params?.frame as Record<string, unknown>;
    if (malformed === "missing loader") delete candidate.loaderId;
    else if (malformed === "wrong loader") candidate.loaderId = "unparsed-loader";
    else candidate.parentId = "foreign-parent";
    try {
      owner.receive(initial);
      // Probe the observer's already-parsed-scope guard; it cannot replace that scope.
      observeInFlightOAuthDocument(owner.proof, repair, owner.lifecycle.generation);
      begin(owner, [start(), pause()]); const wire = await owner.wire(), waiting = owner.idle();
      owner.receive(cancel()); await originalFatal(owner, waiting, owner.fail(wire));
    } finally { await owner.cleanup(); }
  });

  test("in-flight OAuth a real ignored subframe cannot clobber the owned origin", async () => {
    const owner = fixture();
    try {
      owner.receive(documentEvent());
      owner.receive(event("Page.frameNavigated", { frame: { id: "owned-child", parentId: frame, loaderId: "owned-child-loader", url: "https://foreign.invalid/" } }));
      begin(owner, [start(), pause()]); const wire = await owner.wire(), waiting = owner.idle();
      owner.receive(cancel()); owner.fail(wire); await waiting; owner.browser.assertNoFatalError();
      assert.equal(owner.lifecycle.observedInFlightOAuthCancellationCount, 1); assert.equal(owner.lifecycle.safeCancellationCount, 1);
      assert.equal(owner.lifecycle.preSettlementCancellationCount, 0); assert.equal(owner.socket.commands.length, 1);
      assert.equal(owner.browser.responses.get(pathname) || 0, 0); assert.deepEqual(owner.browser.responseStatuses.get(pathname) || [], []);
    } finally { await owner.cleanup(); }
  });

  test("in-flight OAuth own preparation history overflow rejects an actual wire in a fresh document", async () => {
    const owner = fixture();
    try {
      owner.receive(documentEvent());
      const context = { owner: owner.browser, lifecycle: owner.lifecycle, proof: owner.proof, session,
        current: () => ({ generation: owner.lifecycle.generation, frameId: frame, closed: false }),
        assertHealthy: () => owner.browser.assertNoFatalError() };
      for (let index = 0; index < 2048; index += 1) {
        const id = `invalid-preparation-${index}`;
        assert.equal(prepareInFlightOAuthCancellation(context, id, "Fetch.continueRequest", { requestId: id }, true), undefined);
      }
      owner.receive(documentEvent("fresh-capacity-loader"));
      const started = start(); if (started.params) started.params.loaderId = "fresh-capacity-loader";
      begin(owner, [started, pause()]); const wire = await owner.wire(), waiting = owner.idle();
      assert.equal(owner.proof.overflowed, false, "this must exercise the new registry capacity, not the old proof capacity");
      owner.receive(cancel()); await originalFatal(owner, waiting, owner.fail(wire));
      assert.equal(owner.proof.overflowed, false); assert.equal(owner.socket.commands.length, 1);
    } finally { await owner.cleanup(); }
  });

  test("in-flight OAuth an unrelated required request's actual wire fatal is never reclassified", async () => {
    const owner = fixture();
    owner.browser.setRouteResolver(input => new URL(input.url).pathname === "/required-peer" ? { body: {}, requiredResponse: true } : observed);
    try {
      begin(owner); await owner.wire();
      const peer = pause(peerFetch, "required-peer-network"); request(peer).url = baseURL + "/required-peer"; owner.receive(peer);
      const peerWire = await owner.wire(peerFetch), waiting = owner.idle(); owner.receive(cancel());
      const original = owner.fail(peerWire); await originalFatal(owner, waiting, original);
      assert.equal(owner.internal.pendingCommands.size, 0); assert.equal(owner.socket.commands.length, 2);
    } finally { await owner.cleanup(); }
  });

  test("in-flight OAuth duplicate owned pause preserves the first fatal and never retires", async () => {
    const owner = fixture();
    try {
      begin(owner); await owner.wire(); const waiting = owner.idle(); owner.receive(pause());
      const original = await owner.fatalAttempt(); assert.ok(original instanceof Error); assert.match(original.message, /Duplicate Fetch request ID/);
      await originalFatal(owner, waiting, original);
    } finally { await owner.cleanup(); }
  });

  test("in-flight OAuth closed context refuses retirement without inventing a response", async () => {
    const owner = fixture();
    try {
      begin(owner); const wire = await owner.wire(); owner.internal.closed = true; owner.receive(cancel());
      const original = owner.fail(wire); assert.equal(await owner.fatalAttempt(), original);
      noRetirement(owner); assert.equal(owner.lifecycle.activeCount, 1);
    } finally { await owner.cleanup(); }
  });

  test("in-flight OAuth existing fatal before the first send is preserved", async () => {
    const owner = fixture(), original = new Error(invalidInterceptionIdMessage);
    try {
      owner.internal.recordFatalError(original); begin(owner); assert.equal(owner.socket.commands.length, 0);
      assert.throws(() => owner.browser.assertNoFatalError(), error => error === original); noRetirement(owner);
    } finally { await owner.cleanup(); }
  });

  test("in-flight OAuth a foreign-session pause creates no owned request or wire send", async () => {
    const owner = fixture(), foreign = pause(); foreign.sessionId = "foreign-session";
    try { begin(owner, [documentEvent(), start(), foreign]); assert.equal(owner.lifecycle.activeCount, 0); assert.equal(owner.socket.commands.length, 0); noRetirement(owner); }
    finally { await owner.cleanup(); }
  });

  test("in-flight OAuth fixture opt-in is default-off, route-local and reversible", async () => {
    const owner = fixture();
    try {
      const configured = createBrowserRouteFixture(owner.browser);
      const resolve = Reflect.get(owner.browser, "routeResolver") as RouteResolver;
      const get = (path: string) => resolve({ method: "GET", url: baseURL + path });
      assert.equal(configured.observeInFlightOAuthCancellation, false);
      assert.equal(get(pathname)?.observeInFlightOAuthCancellation, undefined);
      configured.observeInFlightOAuthCancellation = true;
      assert.equal(get(pathname)?.observeInFlightOAuthCancellation, true); assert.equal(get(pathname)?.requiredResponse, false);
      assert.deepEqual(get(pathname)?.body, []);
      for (const path of ["/auth/me", "/version", "/settings/app", "/auth/oauth/providers", "/roles"]) assert.equal(get(path)?.observeInFlightOAuthCancellation, undefined);
      configured.streamsResponse = { body: [], requiredResponse: true }; assert.equal(get("/streams")?.requiredResponse, true);
      configured.observeInFlightOAuthCancellation = false;
      assert.equal(get(pathname)?.observeInFlightOAuthCancellation, undefined); assert.equal(get("/streams")?.requiredResponse, true);
    } finally { await owner.cleanup(); }
  });
}
