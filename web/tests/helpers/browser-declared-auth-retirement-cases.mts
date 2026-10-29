import assert from "node:assert/strict";
import test from "node:test";
import { BrowserHarness, type StubResponse } from "./browser-harness.mts";
import { BrowserNetworkCancelProof } from "./browser-network-cancel-proof.mts";
import { FetchRequestLifecycle, RejectableEventWaiters, invalidInterceptionIdMessage } from "./browser-request-lifecycle.mts";
import { declareLoginReturnAuthPhase, retireDeclaredAuth, takeDeclaredAuthRetirement } from "./browser-declared-auth-retirement.mts";

type Message = { sessionId: string; method: string; params: Record<string, unknown> };
type Input = Parameters<typeof declareLoginReturnAuthPhase>[1];
const session = "declared-auth-session", frame = "declared-auth-frame", loader = "declared-auth-loader";
const authURL = "http://127.0.0.1:3000/auth/me", path = "/auth/me";
const firstNetwork = "declared-auth-network-one", secondNetwork = "declared-auth-network-two";
const firstFetch = "declared-auth-fetch-one", secondFetch = "declared-auth-fetch-two";
const fulfill = "Fetch.fulfillRequest" as const;
const optional: StubResponse = { body: { permissions: ["streams.read"] }, requiredResponse: false, observeBootstrapCancellation: true };
const message = (method: string, params: Record<string, unknown>): Message => ({ sessionId: session, method, params });
const documentEvent = (nextLoader = loader) => message("Page.frameNavigated", { frame: { id: frame, loaderId: nextLoader, url: "http://127.0.0.1:3000/login/" } });
const start = (network = firstNetwork) => message("Network.requestWillBeSent", {
  requestId: network, frameId: frame, loaderId: loader, type: "Fetch", request: { method: "GET", url: authURL },
});
const cancel = (network = firstNetwork) => message("Network.loadingFailed", { requestId: network, canceled: true, errorText: "net::ERR_ABORTED" });
const pause = (id = firstFetch, network = firstNetwork) => message("Fetch.requestPaused", {
  requestId: id, networkId: network, frameId: frame, resourceType: "XHR", request: { method: "GET", url: authURL },
});
const request = (event: Message) => event.params.request as Record<string, unknown>;

function fixture(limit = 2048, initialDocument = documentEvent()) {
  const lifecycle = new FetchRequestLifecycle(), proof = new BrowserNetworkCancelProof(lifecycle, session, limit);
  const state: { frameId: string | undefined; closed: boolean } = { frameId: frame, closed: false };
  const input: Input = {
    lifecycle, session, current: () => ({ generation: lifecycle.generation, ...state }),
    assertHealthy: () => lifecycle.assertHealthy(),
  };
  const emit = (event: Message, generation = lifecycle.generation) => {
    proof.observe(event, generation);
  };
  const registerPause = (event = pause(), required = false) => {
    emit(event);
    const id = String(event.params.requestId), value = request(event), pathname = new URL(String(value.url)).pathname;
    lifecycle.register({ requestId: id, method: String(value.method), pathname, requiredResponse: required });
    proof.registerContract(id, { generation: lifecycle.generation, pathname, optionalFixture: !required });
    return id;
  };
  emit(initialDocument);
  return {
    lifecycle, proof, state, input, emit, registerPause,
    arm: (url = authURL) => declareLoginReturnAuthPhase(proof, input, url),
    retire: (id = firstFetch, response: StubResponse | null = optional) => retireDeclaredAuth(proof, id, response),
  };
}
type Fixture = ReturnType<typeof fixture>;

function harnessFixture(responses: StubResponse[] = [optional]) {
  const lifecycle = new FetchRequestLifecycle(), proof = new BrowserNetworkCancelProof(lifecycle, session);
  const sends: string[] = [], pending = responses.slice();
  const browser = Object.assign(Object.create(BrowserHarness.prototype), {
    sessionId: session, requestLifecycle: lifecycle, networkCancellation: proof,
    requests: new Map<string, number>(), responses: new Map<string, number>(), responseStatuses: new Map<string, number[]>(),
    eventWaiters: new RejectableEventWaiters(), pendingCommands: new Map(), topLevelNavigationEvents: 0,
    topLevelNavigationPending: false, closed: false, fatalError: undefined,
    routeResolver: () => { const response = pending.shift(); assert.ok(response); return response; },
    send: async (command: string) => { sends.push(command); return {}; },
  }) as BrowserHarness;
  const receive = (event: Message) => (browser as unknown as { receive(raw: string): void }).receive(JSON.stringify(event));
  return { browser, lifecycle, proof, sends, receive };
}

function observeCandidate(owner: Fixture) {
  owner.emit(start()); owner.emit(cancel()); owner.registerPause();
}

function assertRejected(owner: Fixture, id = firstFetch, response: StubResponse | null = optional) {
  assert.equal(owner.retire(id, response), false);
  assert.equal(owner.lifecycle.declaredAuthRetirementCount, 0);
  assert.equal(owner.lifecycle.activeCount, 1);
  const original = new Error(invalidInterceptionIdMessage);
  const attempt = owner.lifecycle.beginSettlement(id, fulfill);
  assert.throws(() => owner.lifecycle.handleSettlementError(attempt, original), error => error === original,
    "a rejected declaration cannot replace the existing current-generation fatal rule");
}

type Mutation = { name: string; mutate: (events: Message[]) => void; required?: boolean; response?: StubResponse | null; limit?: number };
const rejected: Mutation[] = [
  { name: "required response", required: true, response: { ...optional, requiredResponse: true }, mutate() {} },
  { name: "missing explicit bootstrap observation", response: { body: optional.body, requiredResponse: false }, mutate() {} },
  { name: "unhandled route", response: null, mutate() {} },
  { name: "foreign Network session", mutate: events => { events[0].sessionId = "foreign-session"; } },
  { name: "foreign pause session", mutate: events => { events[2].sessionId = "foreign-session"; } },
  { name: "foreign Network frame", mutate: events => { events[0].params.frameId = "foreign-frame"; } },
  { name: "foreign pause frame", mutate: events => { events[2].params.frameId = "foreign-frame"; } },
  { name: "wrong loader", mutate: events => { events[0].params.loaderId = "foreign-loader"; } },
  { name: "wrong Network type", mutate: events => { events[0].params.type = "XHR"; } },
  { name: "wrong pause type", mutate: events => { events[2].params.resourceType = "Fetch"; } },
  { name: "wrong origin", mutate: events => { request(events[0]).url = "http://127.0.0.1:3001/auth/me"; } },
  { name: "query", mutate: events => { request(events[0]).url = authURL + "?extra=one"; } },
  { name: "subpath", mutate: events => { request(events[0]).url = authURL + "/private"; } },
  { name: "different pause URL", mutate: events => { request(events[2]).url = "http://localhost:3000/auth/me"; } },
  { name: "different Network identity", mutate: events => { events[2].params.networkId = secondNetwork; } },
  { name: "redirect", mutate: events => { events[0].params.redirectResponse = {}; } },
  { name: "duplicate SAME Network ID", mutate: events => { events.splice(1, 0, start()); } },
  { name: "duplicate pause", mutate: events => { events.push(pause()); } },
  { name: "two Fetch IDs for one Network ID", mutate: events => { events.push(pause(secondFetch)); } },
  { name: "duplicate cancellation", mutate: events => { events.splice(2, 0, cancel()); } },
  { name: "not canceled", mutate: events => { events[1].params.canceled = false; } },
  { name: "different network error", mutate: events => { events[1].params.errorText = "net::ERR_FAILED"; } },
  { name: "response before cancellation", mutate: events => { events.splice(1, 0, message("Network.responseReceived", { requestId: firstNetwork })); } },
  { name: "finish before cancellation", mutate: events => { events.splice(1, 0, message("Network.loadingFinished", { requestId: firstNetwork })); } },
  { name: "scope replacement", mutate: events => { events.splice(2, 0, documentEvent("replacement-loader")); } },
  { name: "owned frame detach", mutate: events => { events.splice(2, 0, message("Page.frameDetached", { frameId: frame })); } },
  { name: "unknown frame detach", mutate: events => { events.splice(2, 0, message("Page.frameDetached", {})); } },
  { name: "proof overflow", limit: 1, mutate: events => { events.splice(1, 0, start(secondNetwork)); } },
];

export function registerDeclaredAuthRetirementCases() {
  test("declared AUTH actual HH retires first ID while a distinct required second ID completes normally", async () => {
    let release!: () => void;
    const held = new Promise<void>(resolve => { release = resolve; });
    const required: StubResponse = { body: { permissions: ["streams.read"] }, requiredResponse: true, waitUntil: held };
    const { browser, lifecycle, sends, receive } = harnessFixture([required, optional]);
    const active = Reflect.get(lifecycle, "activeRequests") as Map<string, unknown>;
    receive(documentEvent());
    const phase = browser.declareLoginReturnAuthPhase(authURL);
    let idle = false;
    let waiting: Promise<void> | undefined;
    try {
      receive(start()); receive(start(secondNetwork)); receive(cancel()); receive(pause(secondFetch, secondNetwork));
      assert.equal(active.has(secondFetch), true);
      assert.equal(lifecycle.activeCount, 1);
      waiting = browser.waitForRequestHandlersIdle({ timeoutMs: 1_000 }).then(() => { idle = true; });
      void waiting.catch(() => {}); // Awaited after release; preserve earlier assertions on failure.
      await Promise.resolve(); assert.equal(idle, false);
      receive(pause());
      await Promise.resolve();
      browser.assertNoFatalError();
      assert.deepEqual(sends, [], "the browser-cancelled first ID must never reach CDP");
      assert.equal(browser.responses.get(path) || 0, 0);
      assert.equal(browser.responseStatuses.get(path), undefined);
      assert.equal(browser.declaredAuthRetirementCount, 1);
      assert.equal(lifecycle.declaredAuthRetirementCount, 1);
      assert.equal(active.has(firstFetch), false);
      assert.equal(active.has(secondFetch), true, "retiring AUTH1 must preserve the unrelated required AUTH2");
      assert.equal(lifecycle.activeCount, 1);
      assert.equal(idle, false, "global idle must still wait for the held required AUTH2");
      release(); await waiting;
      browser.assertNoFatalError();
      assert.deepEqual(sends, [fulfill], "the distinct required AUTH request retains its normal settlement");
      assert.equal(browser.requests.get(path), 2);
      assert.equal(browser.responses.get(path), 1);
      assert.deepEqual(browser.responseStatuses.get(path), [200]);
      assert.equal(browser.declaredAuthRetirementCount, 1);
      assert.equal(lifecycle.activeCount, 0);
      assert.equal(idle, true);
      assert.equal((Reflect.get(browser, "eventWaiters") as RejectableEventWaiters).pendingCount, 0);
    } finally {
      release(); phase.dispose();
      const cleanup = new Error("declared AUTH fake closed");
      (Reflect.get(browser, "eventWaiters") as RejectableEventWaiters).rejectAll(cleanup);
      if (lifecycle.activeCount) lifecycle.fail(cleanup);
      await waiting?.catch(() => {});
    }
  });

  for (const entry of rejected) test("declared AUTH rejects " + entry.name, () => {
    const owner = fixture(entry.limit), phase = owner.arm(), events = [start(), cancel(), pause()];
    entry.mutate(events);
    let registered = false;
    try {
      for (const event of events) {
        if (event.method === "Fetch.requestPaused" && !registered) { owner.registerPause(event, entry.required); registered = true; }
        else owner.emit(event);
      }
      assert.ok(registered);
      assertRejected(owner, firstFetch, entry.response === undefined ? optional : entry.response);
    } finally { phase.dispose(); }
  });

  for (const method of ["HEAD", "POST", "PUT", "PATCH", "DELETE"]) test("declared AUTH rejects method " + method, () => {
    const owner = fixture(), phase = owner.arm(), first = start(), paused = pause();
    request(first).method = method; request(paused).method = method;
    try { owner.emit(first); owner.emit(cancel()); owner.registerPause(paused); assertRejected(owner); }
    finally { phase.dispose(); }
  });

  for (const late of ["start", "cancel"]) test("declared AUTH cannot arm retrospectively after " + late, () => {
    const { browser, lifecycle, sends, receive } = harnessFixture();
    receive(documentEvent()); receive(start()); if (late === "cancel") receive(cancel());
    assert.throws(() => browser.declareLoginReturnAuthPhase(authURL));
    assert.equal(browser.declaredAuthRetirementCount, 0);
    assert.equal(lifecycle.activeCount, 0);
    assert.deepEqual(sends, []);
  });

  for (const boundary of ["generation-only notification", "return to an already-started loader"]) test("declared AUTH cannot rearm through " + boundary, () => {
    const { browser, lifecycle, sends, receive } = harnessFixture();
    receive(documentEvent()); receive(start());
    lifecycle.beginNavigation("navigate");
    if (boundary === "return to an already-started loader") {
      receive(documentEvent("different-loader"));
      lifecycle.beginNavigation("navigate");
    }
    receive(documentEvent());
    assert.throws(() => browser.declareLoginReturnAuthPhase(authURL));
    assert.equal(browser.declaredAuthRetirementCount, 0);
    assert.equal(lifecycle.activeCount, 0);
    assert.deepEqual(sends, []);
  });

  for (const [label, url] of [
    ["missing", undefined], ["unparseable", ":"],
    ["foreign origin", "https://foreign.invalid/login/"], ["different loopback port", "http://127.0.0.1:3001/login/"],
  ] as const) test("declared AUTH requires a witnessed document origin: " + label, () => {
    const document = documentEvent(), ownedFrame = document.params.frame as Record<string, unknown>;
    if (url === undefined) delete ownedFrame.url;
    else ownedFrame.url = url;
    const owner = fixture(undefined, document);
    assert.throws(() => owner.arm());
    assert.equal(owner.lifecycle.declaredAuthRetirementCount, 0);
    assert.equal(owner.lifecycle.activeCount, 0);
  });

  test("declared AUTH first successful start consumes the phase and second cancellation cannot retire", () => {
    const owner = fixture(), phase = owner.arm();
    try {
      owner.emit(start());
      owner.emit(message("Network.responseReceived", { requestId: firstNetwork }));
      owner.emit(message("Network.loadingFinished", { requestId: firstNetwork }));
      owner.emit(start(secondNetwork)); owner.emit(cancel(secondNetwork)); owner.registerPause(pause(secondFetch, secondNetwork));
      assertRejected(owner, secondFetch);
    } finally { phase.dispose(); }
  });

  test("declared AUTH rejects evidence from a different generation", () => {
    const owner = fixture(), phase = owner.arm();
    try {
      owner.emit(start(), owner.lifecycle.generation + 1); owner.emit(cancel(), owner.lifecycle.generation + 1);
      owner.registerPause(); assertRejected(owner);
    } finally { phase.dispose(); }
  });

  test("declared AUTH disposal and same-document rearm cannot revive a capability", () => {
    const owner = fixture(), phase = owner.arm(); phase.dispose();
    assert.throws(() => owner.arm());
    owner.emit(documentEvent());
    assert.throws(() => owner.arm(), "a repeated notification of the same frame/loader/generation is not a new document");
    observeCandidate(owner); assertRejected(owner);
  });

  test("declared AUTH closed and fatal owners retain refusal and fatal identity", () => {
    const closed = fixture(), closedPhase = closed.arm();
    try { observeCandidate(closed); closed.state.closed = true; assertRejected(closed); }
    finally { closedPhase.dispose(); }
    const owner = fixture(), phase = owner.arm(), fatal = new Error("owned declared AUTH fatal");
    try {
      observeCandidate(owner); owner.lifecycle.fail(fatal);
      assert.throws(() => owner.retire(), error => error === fatal);
      assert.equal(owner.lifecycle.declaredAuthRetirementCount, 0);
    } finally { phase.dispose(); }
  });

  test("declared AUTH cannot follow a rejected old first claim", () => {
    const owner = fixture(), phase = owner.arm();
    try {
      observeCandidate(owner);
      assert.equal(owner.proof.claimBeforeSettlement(firstFetch, fulfill, owner.input.current()), undefined);
      assertRejected(owner);
    } finally { phase.dispose(); }
  });

  test("declared AUTH receipt is opaque, frozen, one-use and releases the actual lifecycle idle waiter", async () => {
    const owner = fixture(), phase = owner.arm(), complete = owner.lifecycle.completeDeclaredAuthRetirement.bind(owner.lifecycle);
    let receipt: unknown;
    owner.lifecycle.completeDeclaredAuthRetirement = (id, value) => {
      receipt = value; assert.deepEqual(Object.keys(value as object), []); assert.equal(Object.isFrozen(value), true);
      return complete(id, value);
    };
    try {
      observeCandidate(owner);
      let idle = false;
      const waiting = owner.lifecycle.waitForIdle({ pathname: path, timeoutMs: 100 }).then(() => { idle = true; });
      void waiting.catch(() => {}); // Awaited below; also observe a rejection if an earlier assertion fails.
      await Promise.resolve(); assert.equal(idle, false);
      assert.equal(owner.retire(), true); await waiting;
      assert.equal(idle, true); assert.equal(owner.lifecycle.activeCount, 0); assert.equal(owner.lifecycle.declaredAuthRetirementCount, 1);
      assert.equal(takeDeclaredAuthRetirement(receipt, owner.lifecycle, firstFetch, owner.lifecycle.generation), false);
      assert.equal(takeDeclaredAuthRetirement(Object.freeze({}), owner.lifecycle, firstFetch, owner.lifecycle.generation), false);
      assert.equal(owner.retire(), false);
    } finally {
      phase.dispose();
      if (owner.lifecycle.activeCount) owner.lifecycle.fail(new Error("declared AUTH fake closed"));
    }
  });

  for (const fault of ["owner", "id", "generation", "attempt", "required", "disposed"]) test("declared AUTH actual receipt independently rejects " + fault, () => {
    const owner = fixture(), phase = owner.arm(), complete = owner.lifecycle.completeDeclaredAuthRetirement.bind(owner.lifecycle);
    owner.lifecycle.completeDeclaredAuthRetirement = (id, value) => {
      if (fault === "attempt") owner.lifecycle.beginSettlement(id, fulfill);
      else if (fault === "required") owner.lifecycle.setRequiredResponse(id, true);
      else if (fault === "disposed") phase.dispose();
      else assert.equal(takeDeclaredAuthRetirement(value, fault === "owner" ? {} : owner.lifecycle,
        fault === "id" ? "foreign-fetch" : id, fault === "generation" ? owner.lifecycle.generation + 1 : owner.lifecycle.generation), false);
      return complete(id, value);
    };
    try {
      observeCandidate(owner);
      assert.throws(() => owner.retire(), /declared|retirement|receipt/i);
      assert.equal(owner.lifecycle.declaredAuthRetirementCount, 0); assert.equal(owner.lifecycle.activeCount, 1);
      assert.equal(owner.retire(), false, "failed terminal validation burns the first boundary");
    } finally { phase.dispose(); }
  });

  test("declared AUTH start-loader history overflow stays closed after parent document ledgers retire", () => {
    const owner = fixture();
    for (let index = 0; index < 2049; index += 1) {
      const nextLoader = `history-loader-${index}`;
      owner.lifecycle.beginNavigation("navigate"); owner.emit(documentEvent(nextLoader));
      const event = start(`history-network-${index}`); event.params.loaderId = nextLoader; owner.emit(event);
    }
    assert.equal(owner.proof.overflowed, false, "the parent proof cleared each old document ledger normally");
    owner.lifecycle.beginNavigation("navigate"); owner.emit(documentEvent("history-fresh-loader"));
    assert.throws(() => owner.arm(), "new AUTH history overflow must reject an otherwise fresh document");
    owner.lifecycle.beginNavigation("navigate"); owner.emit(documentEvent("history-later-loader"));
    assert.throws(() => owner.arm(), "a later document cannot clear the bounded history refusal");
    assert.equal(owner.proof.overflowed, false);
    assert.equal(owner.lifecycle.declaredAuthRetirementCount, 0);
  });

  test("declared AUTH declaration history cannot exceed 2048 or revive in another fresh loader", () => {
    const owner = fixture();
    for (let index = 0; index < 2048; index += 1) {
      owner.lifecycle.beginNavigation("navigate"); owner.emit(documentEvent(`declared-loader-${index}`));
      owner.arm().dispose();
    }
    owner.lifecycle.beginNavigation("navigate"); owner.emit(documentEvent("declaration-overflow-loader"));
    assert.throws(() => owner.arm(), /capacity exceeded/);
    owner.lifecycle.beginNavigation("navigate"); owner.emit(documentEvent("declaration-no-revival-loader"));
    assert.throws(() => owner.arm());
    assert.equal(owner.proof.overflowed, false);
    assert.equal(owner.lifecycle.declaredAuthRetirementCount, 0);
  });

  test("declared AUTH declaration rejects foreign proof identity and noncanonical URLs", () => {
    const owner = fixture();
    assert.throws(() => declareLoginReturnAuthPhase(owner.proof, { ...owner.input, lifecycle: new FetchRequestLifecycle() }, authURL));
    assert.throws(() => declareLoginReturnAuthPhase(owner.proof, { ...owner.input, session: "foreign-session" }, authURL));
    for (const url of ["https://foreign.invalid/auth/me", authURL + "?x=one", authURL + "#hash", authURL + "/private", "http://user@127.0.0.1:3000/auth/me"]) {
      assert.throws(() => owner.arm(url), TypeError);
    }
    owner.state.frameId = "foreign-main-frame";
    assert.throws(() => owner.arm());
    const missing = fixture(); missing.emit(message("Page.frameDetached", {}));
    assert.throws(() => missing.arm());
  });
}
