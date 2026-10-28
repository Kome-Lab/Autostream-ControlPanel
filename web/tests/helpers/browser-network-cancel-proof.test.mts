import assert from "node:assert/strict";
import test from "node:test";
import { BrowserHarness } from "./browser-harness.mts";
import { BrowserNetworkCancelProof, takeNetworkCancellationTicket } from "./browser-network-cancel-proof.mts";
import { FetchRequestLifecycle, RejectableEventWaiters, invalidInterceptionIdMessage } from "./browser-request-lifecycle.mts";
import { waitForMapCount, waitForMinimumMapCount } from "./browser-response-count.mts";

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
  assert.ok(value);
  change(value);
}
function request(value: Event) { return value.params.request as Record<string, unknown>; }
function fixture(input: Step[] = steps(), options: { limit?: number; required?: boolean; pathname?: string; method?: string } = {}) {
  const lifecycle = new FetchRequestLifecycle();
  lifecycle.register({ requestId: id, method: options.method ?? "GET", pathname: options.pathname ?? path, requiredResponse: options.required ?? false });
  const proof = new BrowserNetworkCancelProof(lifecycle, session, options.limit);
  for (const step of input) {
    if (isEvent(step)) proof.observe(step, step.generation);
    else proof.registerContract(id, step.contract);
  }
  return { lifecycle, proof, claim: () => proof.claimBeforeSettlement(id, fulfill, current) };
}

for (const pathname of [path, "/integrations/oauth-providers", "/roles", "/nodes", "/workers"]) {
  test(`exact owned pre-send cancellation admits only its optional fixture: ${pathname}`, async () => {
    const { lifecycle, claim, proof } = fixture(steps(pathname), { pathname });
    let idle = false;
    const waiting = lifecycle.waitForIdle({ pathname, timeoutMs: 100 }).then(() => { idle = true; });
    await Promise.resolve();
    assert.equal(idle, false);
    const ticket = claim();
    assert.ok(ticket);
    assert.deepEqual(Object.keys(ticket), [], "witness must not expose raw request IDs, URL, or body");
    assert.equal(Object.isFrozen(ticket), true);
    assert.deepEqual(lifecycle.completePreSettlementCancellation(id, fulfill, ticket), { cancelled: true });
    await waiting;
    assert.equal(idle, true);
    assert.equal(lifecycle.activeCount, 0);
    assert.equal(lifecycle.safeCancellationCount, 1);
    assert.equal(lifecycle.preSettlementCancellationCount, 1);
    assert.equal(proof.claimBeforeSettlement(id, fulfill, current), undefined);
    assert.throws(() => lifecycle.completePreSettlementCancellation(id, fulfill, ticket), /Invalid pre-settlement/);
    assert.equal(lifecycle.safeCancellationCount, 1);
  });
}

test("Node configuration remains excluded even with an explicit optional cancellation witness", () => {
  const pathname = "/nodes/fixture-node/configuration";
  const { lifecycle, claim } = fixture(steps(pathname), { pathname });
  assert.equal(claim(), undefined);
  const original = new Error(invalidInterceptionIdMessage);
  assert.throws(() => lifecycle.handleSettlementError(lifecycle.beginSettlement(id, fulfill), original), error => error === original);
  assert.equal(lifecycle.activeCount, 1);
  assert.equal(lifecycle.safeCancellationCount, 0);
});

test("required Node GETs remain fatal and cannot consume an optional witness", () => {
  const pathname = "/nodes", input = steps(pathname);
  (input[2] as { contract: Contract }).contract.optionalFixture = false;
  const required = fixture(input, { pathname, required: true });
  assert.equal(required.claim(), undefined);
  const original = new Error(invalidInterceptionIdMessage);
  assert.throws(() => required.lifecycle.handleSettlementError(required.lifecycle.beginSettlement(id, fulfill), original), error => error === original);
  assert.equal(required.lifecycle.safeCancellationCount, 0);
  const inconsistent = fixture(steps(pathname), { pathname, required: true }), ticket = inconsistent.claim();
  assert.ok(ticket, "the independent lifecycle guard must still reject an inconsistent optional capability");
  assert.throws(() => inconsistent.lifecycle.completePreSettlementCancellation(id, fulfill, ticket), /Invalid pre-settlement/);
  assert.equal(inconsistent.lifecycle.activeCount, 1);
  assert.equal(inconsistent.lifecycle.preSettlementCancellationCount, 0);
});

test("non-GET Node requests retain the original current-generation fatal rule", () => {
  for (const method of ["HEAD", "POST", "PUT", "PATCH", "DELETE"]) {
    const pathname = "/nodes", input = steps(pathname);
    changeEvent(input, "Fetch.requestPaused", value => { request(value).method = method; });
    changeEvent(input, "Network.requestWillBeSent", value => { request(value).method = method; });
    const { lifecycle, claim } = fixture(input, { pathname, method });
    assert.equal(claim(), undefined);
    const original = new Error(invalidInterceptionIdMessage);
    assert.throws(() => lifecycle.handleSettlementError(lifecycle.beginSettlement(id, fulfill), original), error => error === original);
    assert.equal(lifecycle.activeCount, 1);
    assert.equal(lifecycle.safeCancellationCount, 0);
  }
});

test("required Worker GETs cannot become pre-settlement cancellations", () => {
  const pathname = "/workers", input = steps(pathname);
  (input[2] as { contract: Contract }).contract.optionalFixture = false;
  const required = fixture(input, { pathname, required: true });
  assert.equal(required.claim(), undefined);
  const original = new Error(invalidInterceptionIdMessage);
  assert.throws(() => required.lifecycle.handleSettlementError(required.lifecycle.beginSettlement(id, fulfill), original), error => error === original);
  assert.equal(required.lifecycle.safeCancellationCount, 0);
  const inconsistent = fixture(steps(pathname), { pathname, required: true }), ticket = inconsistent.claim();
  assert.ok(ticket);
  assert.throws(() => inconsistent.lifecycle.completePreSettlementCancellation(id, fulfill, ticket), /Invalid pre-settlement/);
  assert.equal(inconsistent.lifecycle.activeCount, 1);
  assert.equal(inconsistent.lifecycle.preSettlementCancellationCount, 0);
});

test("Worker POST retains the original current-generation fatal rule", () => {
  const pathname = "/workers", method = "POST", input = steps(pathname);
  changeEvent(input, "Fetch.requestPaused", value => { request(value).method = method; });
  changeEvent(input, "Network.requestWillBeSent", value => { request(value).method = method; });
  const { lifecycle, claim } = fixture(input, { pathname, method });
  assert.equal(claim(), undefined);
  const original = new Error(invalidInterceptionIdMessage);
  assert.throws(() => lifecycle.handleSettlementError(lifecycle.beginSettlement(id, fulfill), original), error => error === original);
  assert.equal(lifecycle.safeCancellationCount, 0);
});

test("unknown Worker subroutes remain fatal for both GET and POST", () => {
  for (const pathname of ["/workers/fixture-worker", "/workers/fixture-worker/configuration", "/workers-other"]) {
    for (const method of ["GET", "POST"]) {
      const input = steps(pathname);
      changeEvent(input, "Fetch.requestPaused", value => { request(value).method = method; });
      changeEvent(input, "Network.requestWillBeSent", value => { request(value).method = method; });
      const { lifecycle, claim } = fixture(input, { pathname, method });
      assert.equal(claim(), undefined);
      const original = new Error(invalidInterceptionIdMessage);
      assert.throws(() => lifecycle.handleSettlementError(lifecycle.beginSettlement(id, fulfill), original), error => error === original);
      assert.equal(lifecycle.activeCount, 1);
      assert.equal(lifecycle.safeCancellationCount, 0);
    }
  }
});

const negatives: [string, (input: Step[]) => void][] = [
  ["missing document scope", input => { input.shift(); }],
  ["subframe document cannot establish main scope", input => changeEvent(input, "Page.frameNavigated", value => { (value.params.frame as Record<string, unknown>).parentId = "foreign-parent"; })],
  ["missing loader in document", input => changeEvent(input, "Page.frameNavigated", value => { delete (value.params.frame as Record<string, unknown>).loaderId; })],
  ["missing pause", input => { input.splice(1, 1); }],
  ["missing Network start", input => { input.splice(3, 1); }],
  ["missing route contract", input => { input.splice(2, 1); }],
  ["missing cancellation", input => { input.pop(); }],
  ["foreign pause session", input => changeEvent(input, "Fetch.requestPaused", value => { value.sessionId = "foreign-session"; })],
  ["foreign Network session", input => changeEvent(input, "Network.requestWillBeSent", value => { value.sessionId = "foreign-session"; })],
  ["foreign cancellation session", input => changeEvent(input, "Network.loadingFailed", value => { value.sessionId = "foreign-session"; })],
  ["missing exact Network ID", input => changeEvent(input, "Fetch.requestPaused", value => { delete value.params.networkId; })],
  ["same URL on another Network ID", input => changeEvent(input, "Network.requestWillBeSent", value => { value.params.requestId = "another-network"; })],
  ["different cancellation Network ID", input => changeEvent(input, "Network.loadingFailed", value => { value.params.requestId = "another-network"; })],
  ["missing pause frame", input => changeEvent(input, "Fetch.requestPaused", value => { delete value.params.frameId; })],
  ["foreign pause frame", input => changeEvent(input, "Fetch.requestPaused", value => { value.params.frameId = "foreign-frame"; })],
  ["foreign Network frame", input => changeEvent(input, "Network.requestWillBeSent", value => { value.params.frameId = "foreign-frame"; })],
  ["foreign Network loader", input => changeEvent(input, "Network.requestWillBeSent", value => { value.params.loaderId = "foreign-loader"; })],
  ["missing Network loader", input => changeEvent(input, "Network.requestWillBeSent", value => { delete value.params.loaderId; })],
  ["wrong full URL despite same route", input => changeEvent(input, "Network.requestWillBeSent", value => { request(value).url = `http://fixture.invalid${path}?owned=two`; })],
  ["wrong origin despite same route", input => changeEvent(input, "Network.requestWillBeSent", value => { request(value).url = `http://foreign.invalid${path}?owned=one`; })],
  ["malformed pause URL", input => changeEvent(input, "Fetch.requestPaused", value => { request(value).url = "malformed"; })],
  ["non-HTTP URL", input => changeEvent(input, "Fetch.requestPaused", value => { request(value).url = `file://${path}`; })],
  ["oversized URL", input => changeEvent(input, "Fetch.requestPaused", value => { request(value).url = `http://fixture.invalid/${"x".repeat(4096)}`; })],
  ["non-GET pause", input => changeEvent(input, "Fetch.requestPaused", value => { request(value).method = "POST"; })],
  ["non-GET Network request", input => changeEvent(input, "Network.requestWillBeSent", value => { request(value).method = "POST"; })],
  ["Fetch pause differs from observed XHR domain", input => changeEvent(input, "Fetch.requestPaused", value => { value.params.resourceType = "Fetch"; })],
  ["Network XHR differs from observed Fetch domain", input => changeEvent(input, "Network.requestWillBeSent", value => { value.params.type = "XHR"; })],
  ["Document pause", input => changeEvent(input, "Fetch.requestPaused", value => { value.params.resourceType = "Document"; })],
  ["response-stage pause", input => changeEvent(input, "Fetch.requestPaused", value => { value.params.responseStatusCode = 200; })],
  ["error-stage pause", input => changeEvent(input, "Fetch.requestPaused", value => { value.params.responseErrorReason = "Aborted"; })],
  ["redirected Network start", input => changeEvent(input, "Network.requestWillBeSent", value => { value.params.redirectResponse = {}; })],
  ["duplicate start", input => { input.splice(4, 0, start()); }],
  ["duplicate pause", input => { input.splice(2, 0, pause()); }],
  ["two pause IDs for one Network request", input => { const other = pause(); other.params.requestId = "another-fetch"; input.splice(2, 0, other); }],
  ["duplicate cancellation", input => { input.push(cancelled()); }],
  ["duplicate contract", input => { input.splice(3, 0, contract()); }],
  ["canceled false", input => changeEvent(input, "Network.loadingFailed", value => { value.params.canceled = false; })],
  ["canceled string is not true", input => changeEvent(input, "Network.loadingFailed", value => { value.params.canceled = "true"; })],
  ["missing canceled", input => changeEvent(input, "Network.loadingFailed", value => { delete value.params.canceled; })],
  ["other error", input => changeEvent(input, "Network.loadingFailed", value => { value.params.errorText = "net::ERR_FAILED"; })],
  ["nonexact aborted error", input => changeEvent(input, "Network.loadingFailed", value => { value.params.errorText = "net::ERR_ABORTED "; })],
  ["missing error", input => changeEvent(input, "Network.loadingFailed", value => { delete value.params.errorText; })],
  ["response already received", input => { input.splice(4, 0, event("Network.responseReceived", { requestId: networkID })); }],
  ["already finished", input => { input.splice(4, 0, event("Network.loadingFinished", { requestId: networkID })); }],
  ["cancellation before Network start", input => { const failure = input.pop()!; input.splice(3, 0, failure); }],
  ["cancellation before contract", input => { const failure = input.pop()!; input.splice(2, 0, failure); }],
  ["document changes after pause", input => { input.splice(3, 0, doc()); }],
  ["main frame detached before cancellation", input => { input.splice(4, 0, event("Page.frameDetached", { frameId: frame })); }],
  ["unknown detached frame", input => { input.splice(4, 0, event("Page.frameDetached", {})); }],
  ["fresh document cannot rehabilitate a detached request", input => { input.splice(4, 0, event("Page.frameDetached", { frameId: frame }), doc()); }],
  ["wrong pause generation", input => changeEvent(input, "Fetch.requestPaused", value => { value.generation = 1; })],
  ["wrong Network generation", input => changeEvent(input, "Network.requestWillBeSent", value => { value.generation = 1; })],
  ["wrong cancellation generation", input => changeEvent(input, "Network.loadingFailed", value => { value.generation = 1; })],
  ["wrong scope generation", input => changeEvent(input, "Page.frameNavigated", value => { value.generation = 1; })],
  ["wrong contract generation", input => { (input[2] as { contract: Contract }).contract.generation = 1; }],
  ["wrong contract route", input => { (input[2] as { contract: Contract }).contract.pathname = "/roles"; }],
  ["nonoptional fixture contract", input => { (input[2] as { contract: Contract }).contract.optionalFixture = false; }],
];
for (const [name, mutate] of negatives) {
  test(`cancellation witness rejects ${name}`, () => {
    const input = steps(); mutate(input);
    const { lifecycle, claim } = fixture(input);
    assert.equal(claim(), undefined);
    assert.equal(lifecycle.activeCount, 1);
    assert.equal(lifecycle.preSettlementCancellationCount, 0);
  });
}

test("unlisted routes, unknown current frame/generation, and continue commands cannot claim", () => {
  for (const pathname of ["/auth", "/setup/status", "/service-health", "/roles/other"]) {
    assert.equal(fixture(steps(pathname), { pathname }).claim(), undefined);
  }
  for (const frameId of [undefined, "foreign-frame"]) {
    const { proof } = fixture();
    assert.equal(proof.claimBeforeSettlement(id, fulfill, { generation: 0, frameId }), undefined);
  }
  const { proof } = fixture();
  assert.equal(proof.claimBeforeSettlement(id, fulfill, { ...current, generation: 1 }), undefined);
  assert.equal(fixture().proof.claimBeforeSettlement(id, "Fetch.continueRequest", current), undefined);
});

test("late cancellation or a retried first boundary can never create a witness", () => {
  const { proof, lifecycle } = fixture(steps().slice(0, -1));
  assert.equal(proof.claimBeforeSettlement(id, fulfill, current), undefined);
  proof.observe(cancelled(), 0);
  assert.equal(proof.claimBeforeSettlement(id, fulfill, current), undefined);
  const attempt = lifecycle.beginSettlement(id, fulfill), error = new Error(invalidInterceptionIdMessage);
  assert.throws(() => lifecycle.handleSettlementError(attempt, error), value => value === error);
  assert.equal(lifecycle.safeCancellationCount, 0);
});

test("one-use tickets reject forged objects and wrong owner/request/generation/path/command", () => {
  const wrongContexts = [
    () => ({ owner: {}, id, generation: 0, path, command: fulfill }),
    (owner: object) => ({ owner, id: "another-fetch", generation: 0, path, command: fulfill }),
    (owner: object) => ({ owner, id, generation: 1, path, command: fulfill }),
    (owner: object) => ({ owner, id, generation: 0, path: "/roles", command: fulfill }),
    (owner: object) => ({ owner, id, generation: 0, path, command: "Fetch.continueRequest" as const }),
  ];
  for (const wrong of wrongContexts) {
    const { lifecycle, claim } = fixture(), ticket = claim(), value = wrong(lifecycle);
    assert.ok(ticket);
    assert.equal(takeNetworkCancellationTicket(ticket, value.owner, value.id, value.generation, value.command, value.path), false);
    assert.equal(takeNetworkCancellationTicket(ticket, lifecycle, id, 0, fulfill, path), false, "failed take burns ticket");
  }
  for (const forged of [undefined, null, true, "ticket", {}, Object.freeze({})]) {
    assert.throws(() => fixture().lifecycle.completePreSettlementCancellation(id, fulfill, forged), /Invalid pre-settlement/);
  }
});

test("intervening scope/network observations invalidate a claimed snapshot", () => {
  for (const after of [doc(), start(), cancelled(), event("Network.responseReceived", { requestId: networkID }), event("Page.frameDetached", { frameId: frame }), event("Page.frameDetached", {})]) {
    const { proof, lifecycle, claim } = fixture(), ticket = claim();
    assert.ok(ticket); proof.observe(after, 0);
    assert.throws(() => lifecycle.completePreSettlementCancellation(id, fulfill, ticket), /Invalid pre-settlement/);
    assert.equal(lifecycle.activeCount, 1);
  }
});

test("foreign-frame or foreign-session detach does not change the owned main document proof", () => {
  for (const foreign of [event("Page.frameDetached", { frameId: "owned-subframe" }), { ...event("Page.frameDetached", { frameId: frame }), sessionId: "foreign-session" }]) {
    const input = steps(); input.splice(4, 0, foreign);
    const { proof, lifecycle, claim } = fixture(input), ticket = claim();
    assert.ok(ticket); proof.observe(foreign, 0);
    assert.deepEqual(lifecycle.completePreSettlementCancellation(id, fulfill, ticket), { cancelled: true });
    assert.equal(lifecycle.preSettlementCancellationCount, 1);
  }
});

test("unknown frame-navigation shape invalidates scope before claim and after snapshot", () => {
  const malformed = [
    ...[undefined, null, [], "unknown"].map(value => event("Page.frameNavigated", { frame: value })),
    ...[null, 7, {}, false, []].map(parentId => event("Page.frameNavigated", { frame: { id: frame, loaderId: loader, parentId } })),
    event("Page.frameNavigated", { frame: { id: frame, loaderId: loader, parentId: "foreign-parent" } }),
    event("Page.frameNavigated", { frame: { loaderId: loader, parentId: frame } }),
    event("Page.frameNavigated", { frame: { id: "owned-subframe", loaderId: loader, parentId: "owned-subframe" } }),
    event("Page.frameNavigated", { frame: { id: "owned-subframe", loaderId: loader, parentId: "x".repeat(257) } }),
  ];
  for (const unknown of malformed) {
    const input = steps(); input.splice(4, 0, unknown);
    assert.equal(fixture(input).claim(), undefined);
    const { proof, lifecycle, claim } = fixture(), ticket = claim();
    assert.ok(ticket); proof.observe(unknown, 0);
    assert.throws(() => lifecycle.completePreSettlementCancellation(id, fulfill, ticket), /Invalid pre-settlement/);
    assert.equal(lifecycle.activeCount, 1);
  }
  const { proof, lifecycle, claim } = fixture(), ticket = claim();
  assert.ok(ticket);
  proof.observe(event("Page.frameNavigated", { frame: { id: "owned-subframe", loaderId: "subframe-loader", parentId: frame } }), 0);
  assert.deepEqual(lifecycle.completePreSettlementCancellation(id, fulfill, ticket), { cancelled: true });
});

test("lifecycle independently rejects required, non-GET, stale, attempted, closed, and fatal owners", () => {
  for (const options of [{ required: true }, { method: "POST" }]) {
    const { lifecycle, claim } = fixture(steps(), options), ticket = claim();
    assert.ok(ticket);
    assert.throws(() => lifecycle.completePreSettlementCancellation(id, fulfill, ticket), /Invalid pre-settlement/);
    assert.equal(lifecycle.preSettlementCancellationCount, 0);
  }
  for (const mutate of [
    (value: FetchRequestLifecycle) => value.beginNavigation("reload"),
    (value: FetchRequestLifecycle) => value.beginSettlement(id, fulfill),
    (value: FetchRequestLifecycle) => value.close(),
  ]) {
    const { lifecycle, claim } = fixture(), ticket = claim();
    assert.ok(ticket); mutate(lifecycle);
    assert.throws(() => lifecycle.completePreSettlementCancellation(id, fulfill, ticket), /Invalid pre-settlement/);
  }
  const { lifecycle, claim } = fixture(), fatal = new Error("original fixture failure");
  const ticket = claim(); lifecycle.fail(fatal);
  assert.throws(() => lifecycle.completePreSettlementCancellation(id, fulfill, ticket), value => value === fatal);
  assert.equal(fixtureWithFatal().claim(), undefined);
  function fixtureWithFatal() { const value = fixture(); value.lifecycle.fail(fatal); return value; }
});

test("bounded ledger overflow and malformed identities fail closed", () => {
  const { proof, claim } = fixture(steps(), { limit: 1 });
  proof.observe(start("/roles"), 0); // A duplicate cannot be rehabilitated.
  proof.observe(event("Network.requestWillBeSent", { ...start().params, requestId: "another-network" }), 0);
  assert.equal(proof.overflowed, true);
  assert.equal(claim(), undefined);
  for (const limit of [0, -1, 2049, NaN]) assert.throws(() => new BrowserNetworkCancelProof(new FetchRequestLifecycle(), session, limit), TypeError);
  for (const sessionID of ["", "x".repeat(257)]) assert.throws(() => new BrowserNetworkCancelProof(new FetchRequestLifecycle(), sessionID), TypeError);
  for (const idValue of [undefined, "", "x".repeat(257)]) {
    const input = steps(); changeEvent(input, "Fetch.requestPaused", value => { value.params.requestId = idValue; });
    assert.equal(fixture(input).claim(), undefined);
  }
});

test("terminal cancellation flushes only matching idle waiters and preserves other active requests", async () => {
  const { lifecycle, claim } = fixture();
  lifecycle.register({ requestId: "required-action", pathname: "/streams/one", method: "PUT", requiredResponse: true });
  const focused = lifecycle.waitForIdle({ pathname: path, timeoutMs: 100 });
  let allIdle = false;
  const all = lifecycle.waitForIdle({ timeoutMs: 100 }).then(() => { allIdle = true; });
  lifecycle.completePreSettlementCancellation(id, fulfill, claim());
  await focused; await Promise.resolve();
  assert.equal(allIdle, false);
  assert.equal(lifecycle.activeCount, 1);
  lifecycle.completeSettlement(lifecycle.beginSettlement("required-action", fulfill));
  await all;
  assert.equal(lifecycle.activeCount, 0);
});

test("actual harness receive/route/settlement path skips CDP send and response accounting only with pre-send proof", async () => {
  let release!: () => void;
  const gate = new Promise<void>(resolve => { release = resolve; });
  const lifecycle = new FetchRequestLifecycle(), proof = new BrowserNetworkCancelProof(lifecycle, session);
  const sends: string[] = [];
  const browser = Object.assign(Object.create(BrowserHarness.prototype), {
    sessionId: session, requestLifecycle: lifecycle, networkCancellation: proof,
    requests: new Map<string, number>(), responses: new Map<string, number>(), responseStatuses: new Map<string, number[]>(),
    eventWaiters: new RejectableEventWaiters(), pendingCommands: new Map(), topLevelNavigationEvents: 0,
    topLevelNavigationPending: false, closed: false, fatalError: undefined,
    routeResolver: () => ({ body: [], requiredResponse: false, waitUntil: gate }),
    send: async (command: string) => { sends.push(command); return {}; },
  }) as BrowserHarness;
  const receive = (value: Event) => (browser as unknown as { receive(raw: string): void }).receive(JSON.stringify(value));
  receive(doc()); receive(pause()); receive(start()); receive(cancelled());
  const idle = browser.waitForRequestHandlersIdle({ pathname: path, timeoutMs: 100 });
  release(); await idle;
  browser.assertNoFatalError();
  assert.deepEqual(sends, [], "browser-cancelled request must not reach CDP settlement");
  assert.equal(browser.requests.get(path), 1);
  assert.equal(browser.responses.get(path), undefined);
  assert.equal(browser.responseStatuses.get(path), undefined);
  assert.equal(browser.preSettlementFetchCancellationCount, 1);
  assert.equal(lifecycle.activeCount, 0);
});

test("initial readiness permits observed repeat GETs while exact waits and health failures retain their contracts", async () => {
  const counts = new Map([[path, 2]]), healthy = () => {};
  await waitForMinimumMapCount(counts, path, 1, "initial response", 10, healthy);
  await assert.rejects(waitForMapCount(counts, path, 1, "exact response", 1, healthy), /did not become 1; last count: 2/);
  await assert.rejects(waitForMinimumMapCount(new Map(), path, 1, "initial response", 1, healthy), /did not reach 1; last count: 0/);
  const fatal = new Error("original health failure");
  await assert.rejects(waitForMinimumMapCount(counts, path, 1, "initial response", 10, () => { throw fatal; }), value => value === fatal);
});
