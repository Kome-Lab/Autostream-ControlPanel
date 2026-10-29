import assert from "node:assert/strict";
import test from "node:test";
import { BrowserHarness, type StubResponse } from "./browser-harness.mts";
import { BrowserNetworkCancelProof } from "./browser-network-cancel-proof.mts";
import { FetchRequestLifecycle, RejectableEventWaiters, invalidInterceptionIdMessage } from "./browser-request-lifecycle.mts";
import {
  declareLoginReturnAuthPhase, declareLoginReturnBootstrapPhase, retireDeclaredBootstrap,
  takeDeclaredAuthRetirement, takeDeclaredVersionRetirement,
} from "./browser-declared-auth-retirement.mts";

type Message = { sessionId: string; method: string; params: Record<string, unknown> };
type Input = Parameters<typeof declareLoginReturnBootstrapPhase>[1];
type Lane = "/auth/me" | "/version";
const session = "declared-version-session", frame = "declared-version-frame", loader = "declared-version-loader";
const baseURL = "http://127.0.0.1:3000", version: Lane = "/version", auth: Lane = "/auth/me";
const firstNetwork = "declared-version-first-network", secondNetwork = "declared-version-second-network";
const firstFetch = "declared-version-first-fetch", secondFetch = "declared-version-second-fetch";
const fulfill = "Fetch.fulfillRequest" as const;
const optional: StubResponse = { body: { version: "test-version" }, requiredResponse: false, observeBootstrapCancellation: true };
const required: StubResponse = { body: { version: "test-version" }, requiredResponse: true };
const message = (method: string, params: Record<string, unknown>): Message => ({ sessionId: session, method, params });
const documentEvent = (nextLoader = loader) => message("Page.frameNavigated", { frame: { id: frame, loaderId: nextLoader, url: baseURL + "/login/" } });
const start = (path: string = version, network = firstNetwork, nextLoader = loader) => message("Network.requestWillBeSent", {
  requestId: network, frameId: frame, loaderId: nextLoader, type: "Fetch", request: { method: "GET", url: baseURL + path },
});
const cancel = (network = firstNetwork) => message("Network.loadingFailed", { requestId: network, canceled: true, errorText: "net::ERR_ABORTED" });
const pause = (path: string = version, id = firstFetch, network = firstNetwork) => message("Fetch.requestPaused", {
  requestId: id, networkId: network, frameId: frame, resourceType: "XHR", request: { method: "GET", url: baseURL + path },
});
const request = (event: Message) => event.params.request as Record<string, unknown>;

function fixture(limit = 2048, initialDocument = documentEvent()) {
  const lifecycle = new FetchRequestLifecycle(), proof = new BrowserNetworkCancelProof(lifecycle, session, limit);
  const state: { frameId: string | undefined; closed: boolean } = { frameId: frame, closed: false };
  const input: Input = { lifecycle, session, current: () => ({ generation: lifecycle.generation, ...state }), assertHealthy: () => lifecycle.assertHealthy() };
  const emit = (event: Message, generation = lifecycle.generation) => proof.observe(event, generation);
  const registerPause = (event = pause(), requiredResponse = false) => {
    emit(event);
    const id = String(event.params.requestId), value = request(event), pathname = new URL(String(value.url)).pathname;
    lifecycle.register({ requestId: id, method: String(value.method), pathname, requiredResponse });
    proof.registerContract(id, { generation: lifecycle.generation, pathname, optionalFixture: !requiredResponse });
    return id;
  };
  emit(initialDocument);
  return {
    lifecycle, proof, input, state, emit, registerPause,
    arm: (url = baseURL) => declareLoginReturnBootstrapPhase(proof, input, url),
    retire: (id = firstFetch, response: StubResponse | null = optional) => retireDeclaredBootstrap(proof, id, response),
  };
}
type Fixture = ReturnType<typeof fixture>;

function harnessFixture(responses: StubResponse[]) {
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
  return { browser, lifecycle, sends, receive };
}

function candidate(owner: Fixture, path: string = version, id = firstFetch, network = firstNetwork) {
  owner.emit(start(path, network)); owner.emit(cancel(network)); owner.registerPause(pause(path, id, network));
}

function assertRejected(owner: Fixture, id = firstFetch, response: StubResponse | null = optional) {
  assert.equal(owner.retire(id, response), false);
  assert.equal(owner.lifecycle.declaredVersionRetirementCount, 0);
  assert.equal(owner.lifecycle.declaredAuthRetirementCount, 0);
  assert.equal(owner.lifecycle.activeCount, 1);
  const original = new Error(invalidInterceptionIdMessage), attempt = owner.lifecycle.beginSettlement(id, fulfill);
  assert.throws(() => owner.lifecycle.handleSettlementError(attempt, original), error => error === original,
    "an unproved VERSION must preserve the existing current-generation fatal error object");
}

type Refusal = { name: string; mutate: (events: Message[]) => void; required?: boolean; response?: StubResponse | null; limit?: number };
const refusals: Refusal[] = [
  { name: "required GET", required: true, response: { ...optional, requiredResponse: true }, mutate() {} },
  { name: "missing bootstrap flag", response: { body: optional.body, requiredResponse: false }, mutate() {} },
  { name: "unhandled response", response: null, mutate() {} },
  { name: "foreign Network session", mutate: e => { e[0].sessionId = "foreign-session"; } },
  { name: "foreign pause session", mutate: e => { e[2].sessionId = "foreign-session"; } },
  { name: "foreign Network frame", mutate: e => { e[0].params.frameId = "foreign-frame"; } },
  { name: "foreign pause frame", mutate: e => { e[2].params.frameId = "foreign-frame"; } },
  { name: "missing loader", mutate: e => { delete e[0].params.loaderId; } },
  { name: "wrong Network type", mutate: e => { e[0].params.type = "XHR"; } },
  { name: "wrong pause type", mutate: e => { e[2].params.resourceType = "Fetch"; } },
  { name: "different origin", mutate: e => { request(e[0]).url = "http://127.0.0.1:3001/version"; } },
  { name: "query", mutate: e => { request(e[0]).url = baseURL + "/version?extra=one"; } },
  { name: "subpath", mutate: e => { request(e[0]).url = baseURL + "/version/private"; } },
  { name: "duplicate Network ID", mutate: e => { e.splice(1, 0, start()); } },
  { name: "duplicate pause", mutate: e => { e.push(pause()); } },
  { name: "duplicate cancel", mutate: e => { e.splice(2, 0, cancel()); } },
  { name: "wrong Network join", mutate: e => { e[2].params.networkId = secondNetwork; } },
  { name: "response before cancel", mutate: e => { e.splice(1, 0, message("Network.responseReceived", { requestId: firstNetwork })); } },
  { name: "finish before cancel", mutate: e => { e.splice(1, 0, message("Network.loadingFinished", { requestId: firstNetwork })); } },
  { name: "not cancelled", mutate: e => { e[1].params.canceled = false; } },
  { name: "scope replacement", mutate: e => { e.splice(2, 0, documentEvent("replacement-loader")); } },
  { name: "owned detach", mutate: e => { e.splice(2, 0, message("Page.frameDetached", { frameId: frame })); } },
  { name: "proof overflow", limit: 1, mutate: e => { e.splice(1, 0, start("/unrelated", secondNetwork)); } },
];

export function registerDeclaredVersionRetirementCases() {
  test("declared VERSION actual HH retirement preserves a held required second VERSION and global idle", async () => {
    let release!: () => void;
    const held = new Promise<void>(resolve => { release = resolve; });
    const { browser, lifecycle, sends, receive } = harnessFixture([{ ...required, waitUntil: held }, optional]);
    const active = Reflect.get(lifecycle, "activeRequests") as Map<string, unknown>;
    receive(documentEvent());
    const phase = browser.declareLoginReturnBootstrapPhase(baseURL);
    let idle = false;
    let waiting: Promise<void> | undefined;
    try {
      receive(start()); receive(start(version, secondNetwork)); receive(cancel()); receive(pause(version, secondFetch, secondNetwork));
      assert.equal(active.has(secondFetch), true);
      waiting = browser.waitForRequestHandlersIdle({ timeoutMs: 1_000 }).then(() => { idle = true; });
      void waiting.catch(() => {});
      await Promise.resolve(); assert.equal(idle, false);
      receive(pause()); await Promise.resolve(); browser.assertNoFatalError();
      assert.deepEqual(sends, []); assert.equal(browser.responses.get(version) || 0, 0);
      assert.equal(browser.responseStatuses.get(version), undefined);
      assert.equal(lifecycle.declaredVersionRetirementCount, 1); assert.equal(browser.declaredAuthRetirementCount, 0);
      assert.equal(active.has(firstFetch), false); assert.equal(active.has(secondFetch), true);
      assert.equal(lifecycle.activeCount, 1); assert.equal(idle, false);
      release(); await waiting; browser.assertNoFatalError();
      assert.deepEqual(sends, [fulfill]); assert.equal(browser.requests.get(version), 2);
      assert.equal(browser.responses.get(version), 1); assert.deepEqual(browser.responseStatuses.get(version), [200]);
      assert.equal(lifecycle.declaredVersionRetirementCount, 1); assert.equal(lifecycle.preSettlementCancellationCount, 0);
      assert.equal(lifecycle.activeCount, 0); assert.equal(idle, true);
    } finally {
      release(); phase.dispose();
      const cleanup = new Error("declared VERSION fake closed");
      (Reflect.get(browser, "eventWaiters") as RejectableEventWaiters).rejectAll(cleanup);
      if (lifecycle.activeCount) lifecycle.fail(cleanup);
      await waiting?.catch(() => {});
    }
  });

  for (const successful of [auth, version]) test("declared bootstrap actual HH " + successful + " success leaves the other lane's first retirement available", async () => {
    const retired = successful === auth ? version : auth;
    const { browser, lifecycle, sends, receive } = harnessFixture([required, optional]);
    receive(documentEvent()); const phase = browser.declareLoginReturnBootstrapPhase(baseURL);
    try {
      receive(start(successful)); receive(pause(successful));
      await browser.waitForRequestHandlersIdle({ timeoutMs: 1_000 }); browser.assertNoFatalError();
      assert.deepEqual(sends, [fulfill]); assert.equal(browser.responses.get(successful), 1);
      assert.deepEqual(browser.responseStatuses.get(successful), [200]);
      assert.equal(browser.declaredAuthRetirementCount, 0); assert.equal(lifecycle.declaredVersionRetirementCount, 0);
      receive(start(retired, secondNetwork)); receive(cancel(secondNetwork)); receive(pause(retired, secondFetch, secondNetwork));
      await browser.waitForRequestHandlersIdle({ timeoutMs: 1_000 }); browser.assertNoFatalError();
      assert.deepEqual(sends, [fulfill]); assert.equal(browser.responses.get(retired) || 0, 0);
      assert.equal(browser.responseStatuses.get(retired), undefined);
      assert.equal(browser.declaredAuthRetirementCount, retired === auth ? 1 : 0);
      assert.equal(lifecycle.declaredVersionRetirementCount, retired === version ? 1 : 0);
      assert.equal(lifecycle.activeCount, 0); assert.equal(lifecycle.preSettlementCancellationCount, 0);
    } finally { phase.dispose(); }
  });

  for (const timing of ["before declaration", "after declaration"]) test("declared VERSION known foreign loader does not consume the current document: " + timing, () => {
    const known = "known-previous-loader", owner = fixture(2048, documentEvent(known));
    owner.emit(documentEvent());
    if (timing === "before declaration") owner.emit(start(version, "known-foreign-network", known));
    const phase = owner.arm();
    try {
      if (timing === "after declaration") owner.emit(start(version, "known-foreign-network", known));
      candidate(owner); assert.equal(owner.retire(), true);
      assert.equal(owner.lifecycle.declaredVersionRetirementCount, 1); assert.equal(owner.lifecycle.declaredAuthRetirementCount, 0);
      assert.equal(owner.lifecycle.activeCount, 0); assert.equal(owner.retire("known-foreign-fetch"), false);
    } finally { phase.dispose(); }
  });

  for (const timing of ["before declaration", "after declaration"]) test("declared VERSION unknown same-epoch loader cannot authorize a later current start: " + timing, () => {
    const owner = fixture(), unknown = start(version, "unknown-loader-network", "unobserved-loader");
    if (timing === "before declaration") {
      owner.emit(unknown); assert.throws(() => owner.arm());
    } else {
      const phase = owner.arm();
      try { owner.emit(unknown); candidate(owner); assertRejected(owner); }
      finally { phase.dispose(); }
    }
    assert.equal(owner.lifecycle.declaredVersionRetirementCount, 0);
  });

  for (const entry of refusals) test("declared VERSION rejects " + entry.name, () => {
    const owner = fixture(entry.limit), phase = owner.arm(), events = [start(), cancel(), pause()];
    entry.mutate(events); let registered = false;
    try {
      for (const event of events) {
        if (event.method === "Fetch.requestPaused" && !registered) { owner.registerPause(event, entry.required); registered = true; }
        else owner.emit(event);
      }
      assert.ok(registered); assertRejected(owner, firstFetch, entry.response === undefined ? optional : entry.response);
    } finally { phase.dispose(); }
  });

  for (const method of ["HEAD", "POST", "PUT", "PATCH", "DELETE"]) test("declared VERSION rejects method " + method, () => {
    const owner = fixture(), phase = owner.arm(), first = start(), paused = pause();
    request(first).method = method; request(paused).method = method;
    try { owner.emit(first); owner.emit(cancel()); owner.registerPause(paused); assertRejected(owner); }
    finally { phase.dispose(); }
  });

  test("declared VERSION first successful start consumes its own lane before a second early cancellation", () => {
    const owner = fixture(), phase = owner.arm();
    try {
      owner.emit(start()); owner.emit(message("Network.responseReceived", { requestId: firstNetwork }));
      owner.emit(message("Network.loadingFinished", { requestId: firstNetwork }));
      owner.emit(start(version, secondNetwork)); owner.emit(cancel(secondNetwork)); owner.registerPause(pause(version, secondFetch, secondNetwork));
      assertRejected(owner, secondFetch);
    } finally { phase.dispose(); }
  });

  for (const source of [auth, version]) test("declared bootstrap receipt from " + source + " is refused and burned by the opposite terminal", () => {
    const owner = fixture(), phase = owner.arm(), sentinel = new Error("wrong-path receipt refused");
    const original = source === auth ? owner.lifecycle.completeDeclaredAuthRetirement.bind(owner.lifecycle) : owner.lifecycle.completeDeclaredVersionRetirement.bind(owner.lifecycle);
    const opposite = source === auth ? owner.lifecycle.completeDeclaredVersionRetirement.bind(owner.lifecycle) : owner.lifecycle.completeDeclaredAuthRetirement.bind(owner.lifecycle);
    const capture = (id: string, receipt: unknown): ReturnType<typeof original> => {
      assert.deepEqual(Object.keys(receipt as object), []); assert.equal(Object.isFrozen(receipt), true);
      const active = Reflect.get(owner.lifecycle, "activeRequests") as Map<string, { pathname: string; settlementAttemptCount: number }>;
      const entry = active.get(id); assert.ok(entry);
      const originalPath = entry.pathname;
      entry.pathname = source === auth ? version : auth;
      try { assert.throws(() => opposite(id, receipt), /declared|retirement|receipt/i); }
      finally { entry.pathname = originalPath; }
      assert.equal(entry.settlementAttemptCount, 0, "wrong-path receipt rejection must precede every settlement attempt");
      assert.equal(owner.lifecycle.activeCount, 1); assert.equal(owner.lifecycle.declaredAuthRetirementCount, 0); assert.equal(owner.lifecycle.declaredVersionRetirementCount, 0);
      assert.throws(() => original(id, receipt), /declared|retirement|receipt/i, "wrong-path refusal must burn the genuine receipt");
      throw sentinel;
    };
    if (source === auth) owner.lifecycle.completeDeclaredAuthRetirement = capture;
    else owner.lifecycle.completeDeclaredVersionRetirement = capture;
    try {
      candidate(owner, source); assert.throws(() => owner.retire(), error => error === sentinel);
      assert.equal(owner.retire(), false); assert.equal(owner.lifecycle.activeCount, 1);
    } finally { phase.dispose(); }
  });

  for (const fault of ["owner", "id", "generation", "required", "disposed"]) test("declared VERSION opaque receipt independently refuses and burns " + fault, () => {
    const owner = fixture(), phase = owner.arm(), complete = owner.lifecycle.completeDeclaredVersionRetirement.bind(owner.lifecycle);
    owner.lifecycle.completeDeclaredVersionRetirement = (id, receipt) => {
      if (fault === "required") owner.lifecycle.setRequiredResponse(id, true);
      else if (fault === "disposed") phase.dispose();
      else {
        assert.equal(takeDeclaredVersionRetirement(receipt, fault === "owner" ? {} : owner.lifecycle,
          fault === "id" ? "foreign-fetch" : id, fault === "generation" ? owner.lifecycle.generation + 1 : owner.lifecycle.generation), false);
        assert.equal(takeDeclaredAuthRetirement(receipt, owner.lifecycle, id, owner.lifecycle.generation), false);
      }
      return complete(id, receipt);
    };
    try { candidate(owner); assert.throws(() => owner.retire(), /declared|retirement|receipt/i); assert.equal(owner.lifecycle.declaredVersionRetirementCount, 0); assert.equal(owner.retire(), false); }
    finally { phase.dispose(); }
  });

  for (const late of ["start", "cancel"]) test("declared VERSION cannot arm retrospectively after " + late, () => {
    const owner = fixture(); owner.emit(start()); if (late === "cancel") owner.emit(cancel());
    assert.throws(() => owner.arm()); assert.equal(owner.lifecycle.declaredVersionRetirementCount, 0);
  });

  test("declared VERSION disposal cannot rearm the same document", () => {
    const owner = fixture(), phase = owner.arm(); phase.dispose();
    assert.throws(() => owner.arm()); candidate(owner); assertRejected(owner);
  });

  test("declared VERSION cannot retire after an actual settlement attempt", () => {
    const owner = fixture(), phase = owner.arm();
    try {
      candidate(owner); const attempt = owner.lifecycle.beginSettlement(firstFetch, fulfill), original = new Error(invalidInterceptionIdMessage);
      assert.throws(() => owner.retire(), /declared|retirement|receipt/i);
      assert.equal(owner.lifecycle.declaredVersionRetirementCount, 0); assert.equal(owner.retire(), false);
      assert.throws(() => owner.lifecycle.handleSettlementError(attempt, original), error => error === original);
    } finally { phase.dispose(); }
  });

  test("declared VERSION cannot follow a rejected original first claim", () => {
    const owner = fixture(), phase = owner.arm();
    try { candidate(owner); assert.equal(owner.proof.claimBeforeSettlement(firstFetch, fulfill, owner.input.current()), undefined); assertRejected(owner); }
    finally { phase.dispose(); }
  });

  test("the existing AUTH-only declaration grants no VERSION capability", () => {
    const owner = fixture(), phase = declareLoginReturnAuthPhase(owner.proof, owner.input, baseURL + auth);
    try { candidate(owner); assertRejected(owner); }
    finally { phase.dispose(); }
  });

  test("declared bootstrap grants no APP or unknown-route retirement", () => {
    for (const path of ["/settings/app", "/settings/app/private", "/unknown"]) {
      const owner = fixture(), phase = owner.arm();
      try { candidate(owner, path); assertRejected(owner); }
      finally { phase.dispose(); }
    }
  });

  test("declared VERSION closed and fatal owners preserve refusal and original fatal identity", () => {
    const closed = fixture(), closedPhase = closed.arm();
    try { candidate(closed); closed.state.closed = true; assertRejected(closed); }
    finally { closedPhase.dispose(); }
    const owner = fixture(), phase = owner.arm(), fatal = new Error("declared VERSION owned fatal");
    try { candidate(owner); owner.lifecycle.fail(fatal); assert.throws(() => owner.retire(), error => error === fatal); assert.equal(owner.lifecycle.declaredVersionRetirementCount, 0); }
    finally { phase.dispose(); }
  });

  test("declared bootstrap declaration requires the original proof identity and exact witnessed origin", () => {
    const owner = fixture();
    assert.throws(() => declareLoginReturnBootstrapPhase(owner.proof, { ...owner.input, lifecycle: new FetchRequestLifecycle() }, baseURL));
    assert.throws(() => declareLoginReturnBootstrapPhase(owner.proof, { ...owner.input, session: "foreign-session" }, baseURL));
    assert.throws(() => owner.arm("http://127.0.0.1:3001"));
    const initial = documentEvent(); delete (initial.params.frame as Record<string, unknown>).url;
    assert.throws(() => fixture(2048, initial).arm());
  });
}
