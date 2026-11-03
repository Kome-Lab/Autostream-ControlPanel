import assert from "node:assert/strict";
import test from "node:test";
import { createHarnessFixture } from "./browser-cdp-socket-fixture.mts";
import { invalidInterceptionIdMessage, type FetchRequestLifecycle, type RejectableEventWaiters } from "./browser-request-lifecycle.mts";
import type { PendingCommand } from "./browser-command-deadline.mts";
import { createBrowserRouteFixture } from "../ui-browser-fixture.mts";
import type { RouteResolver } from "./browser-harness.mts";

const frame = "read-frame", loader = "read-loader", fetchID = "read-fetch", networkID = "read-network";
const origin = "http://127.0.0.1:3002";
type Options = { path?: string; method?: string; required?: boolean; optedIn?: boolean; lateStart?: boolean; noDocument?: boolean; noNetworkID?: boolean; wrongLoader?: boolean };
function fixture(options: Options = {}) {
  const f = createHarnessFixture(), path = options.path ?? "/streams", method = options.method ?? "GET", url = origin + path;
  const lifecycle = (f.harness as unknown as { requestLifecycle: FetchRequestLifecycle }).requestLifecycle;
  f.socket.hold("Fetch.fulfillRequest");
  f.harness.setRouteResolver(() => ({ body: [], requiredResponse: options.required ?? false, observeInFlightReadCancellation: options.optedIn ?? true }));
  const document = { frame: { id: frame, loaderId: loader, url: origin + "/admin/" } };
  const start = { requestId: networkID, frameId: frame, loaderId: options.wrongLoader ? "foreign-loader" : loader, type: "Fetch", request: { method, url } };
  const pause = { requestId: fetchID, networkId: options.noNetworkID ? undefined : networkID, frameId: frame, resourceType: "XHR", request: { method, url } };
  const cancel = { requestId: networkID, canceled: true, errorText: "net::ERR_ABORTED", type: "Fetch" };
  const begin = () => {
    if (!options.noDocument) f.socket.emitEvent("Page.frameNavigated", document);
    if (!options.lateStart) f.socket.emitEvent("Network.requestWillBeSent", start);
    f.socket.emitEvent("Fetch.requestPaused", pause);
  };
  return { ...f, lifecycle, path, document, start, pause, cancel, begin };
}

export function registerOptionalReadCancellationCases() {
  for (const mode of ["ACK succeeds", "late facts cannot repair preparation", "fatal while observing propagates"]) {
    test(`optional Network observation deadline grants no cancellation permission: ${mode}`, async () => {
      const f = fixture({ lateStart: true });
      const waiters = (f.harness as unknown as { eventWaiters: RejectableEventWaiters }).eventWaiters;
      const wait = waiters.wait.bind(waiters), fatal = new Error("original fatal during optional observation");
      waiters.wait = (event, timeout, reason) => event.startsWith("owned-optional-read-start:")
        ? { promise: Promise.reject(mode === "fatal while observing propagates" ? fatal : reason), cancel: () => {} }
        : wait(event, timeout, reason);
      try {
        f.begin();
        if (mode === "fatal while observing propagates") {
          await assert.rejects(f.harness.waitForRequestHandlersIdle({ timeoutMs: 1_000 }), error => error === fatal);
          assert.equal(f.socket.commandsFor("Fetch.fulfillRequest").length, 0);
        } else {
          const wire = await f.socket.waitForCommand("Fetch.fulfillRequest");
          const idle = f.harness.waitForRequestHandlersIdle({ timeoutMs: 1_000 });
          if (mode === "ACK succeeds") {
            f.socket.respond(wire, { result: {} }); await idle;
            f.harness.assertNoFatalError();
            assert.equal(f.harness.responses.get(f.path), 1);
          } else {
            const rejected = assert.rejects(idle, /Invalid InterceptionId\./);
            f.socket.emitEvent("Network.requestWillBeSent", f.start);
            f.socket.emitEvent("Network.loadingFailed", f.cancel);
            f.socket.respond(wire, { error: { message: invalidInterceptionIdMessage } });
            await rejected;
            assert.equal(f.harness.responses.get(f.path) || 0, 0);
          }
        }
        assert.equal(f.lifecycle.observedInFlightOptionalReadCancellationCount, 0);
        assert.equal(f.lifecycle.preSettlementCancellationCount, 0);
      } finally { await f.harness.close(); }
    });
  }
  test("responsive array-read fixtures are exact GETs and require explicit cancellation opt-in", async () => {
    const { harness } = createHarnessFixture();
    try {
      const f = createBrowserRouteFixture(harness);
      const resolve = (harness as unknown as { routeResolver: RouteResolver }).routeResolver;
      for (const path of ["/archive/streams", "/archive/processing-streams", "/observability/diagnostics", "/observability/metrics", "/audit-logs"]) {
        const request = { method: "GET", url: origin + path };
        assert.deepEqual(resolve(request)?.body, []);
        assert.equal(resolve(request)?.requiredResponse, false);
        assert.equal(resolve(request)?.observeInFlightReadCancellation, undefined);
        f.observeInFlightReadCancellation = true;
        assert.equal(resolve(request)?.observeInFlightReadCancellation, true);
        f.observeInFlightReadCancellation = false;
        assert.equal(resolve({ ...request, method: "POST" }), null);
        assert.equal(resolve({ method: "GET", url: origin + path + "-unknown" }), null);
      }
    } finally { await harness.close(); }
  });
  for (const path of ["/streams", "/archive/destinations", "/integrations/oauth-accounts", "/settings/app"]) {
    for (const lateStart of [false, true]) {
      test(`opted-in optional read has one owned in-flight retirement: ${path}, late Network start=${lateStart}`, async () => {
        const f = fixture({ path, lateStart });
        try {
          f.begin();
          if (lateStart) {
            await Promise.resolve();
            assert.equal(f.socket.commandsFor("Fetch.fulfillRequest").length, 0, "pause alone must not prepare a receipt");
            f.socket.emitEvent("Network.requestWillBeSent", f.start);
          }
          const wire = await f.socket.waitForCommand("Fetch.fulfillRequest");
          const idle = f.harness.waitForRequestHandlersIdle({ timeoutMs: 1_000 });
          f.socket.emitEvent("Network.loadingFailed", f.cancel);
          f.socket.respond(wire, { error: { message: invalidInterceptionIdMessage } });
          await idle;
          f.harness.assertNoFatalError();
          assert.equal(f.lifecycle.observedInFlightOptionalReadCancellationCount, 1);
          assert.equal(f.lifecycle.observedInFlightOAuthCancellationCount, 0);
          assert.equal(f.socket.commandsFor("Fetch.fulfillRequest").length, 1);
          assert.equal(f.socket.commandsFor("Fetch.continueRequest").length, 0);
          assert.equal(f.harness.responses.get(path) || 0, 0, "cancelled reads never become successful response receipts");
          assert.deepEqual(f.harness.responseStatuses.get(path) || [], []);
          assert.equal(f.lifecycle.activeCount, 0);
          assert.equal(f.lifecycle.generation, 0, "same-document retirement is not navigation");
        } finally { await f.harness.close(); }
      });
    }
  }

  const refusals = [
    { name: "default observation absent", options: { optedIn: false } },
    { name: "required GET", options: { required: true } },
    { name: "non-GET", options: { method: "POST" } },
    { name: "unsupported path", options: { path: "/unsupported-read" } },
    { name: "missing document", options: { noDocument: true } },
    { name: "missing Network link", options: { noNetworkID: true } },
    { name: "foreign loader", options: { wrongLoader: true } },
    ...["no cancellation", "not canceled", "response received", "finished", "new document", "duplicate start", "duplicate cancellation", "late cancellation", "duplicate error", "wrong error session", "other error", "malformed error", "other receipt family"].map(name => ({ name, options: {} })),
  ];
  for (const { name, options } of refusals) {
    test(`optional read refuses incomplete or conflicting evidence: ${name}`, async () => {
      const f = fixture(name === "other receipt family" ? { path: "/integrations/oauth-accounts" } : options);
      try {
        f.begin();
        const wire = await f.socket.waitForCommand("Fetch.fulfillRequest");
        if (name === "other receipt family") {
          f.lifecycle.completeObservedInFlightOptionalReadCancellation = (attempt, error) => f.lifecycle.completeObservedInFlightOAuthCancellation(attempt, error);
        }
        const pending = (f.harness as unknown as { pendingCommands: Map<number, PendingCommand> }).pendingCommands.get(wire.id);
        assert.ok(pending);
        let original: Error | undefined;
        const reject = pending.reject;
        pending.reject = error => { original = error; reject(error); };
        const rejected = assert.rejects(f.harness.waitForRequestHandlersIdle({ timeoutMs: 1_000 }), error => error === original);
        if (name === "response received") f.socket.emitEvent("Network.responseReceived", { requestId: networkID });
        if (name === "finished") f.socket.emitEvent("Network.loadingFinished", { requestId: networkID });
        if (name === "new document") f.socket.emitEvent("Page.frameNavigated", { frame: { ...f.document.frame, loaderId: "new-loader" } });
        if (name === "duplicate start") f.socket.emitEvent("Network.requestWillBeSent", f.start);
        if (!["no cancellation", "late cancellation"].includes(name)) f.socket.emitEvent("Network.loadingFailed", { ...f.cancel, canceled: name !== "not canceled" });
        if (name === "duplicate cancellation") f.socket.emitEvent("Network.loadingFailed", f.cancel);
        const response = { error: { message: name === "other error" ? "Target closed" : name === "malformed error" ? "Invalid InterceptionId" : invalidInterceptionIdMessage } };
        f.socket.respond(name === "wrong error session" ? { ...wire, sessionId: "foreign-session" } : wire, response);
        if (name === "late cancellation") f.socket.emitEvent("Network.loadingFailed", f.cancel);
        if (name === "duplicate error") f.socket.respond(wire, response);
        await rejected;
        assert.ok(original);
        assert.throws(() => f.harness.assertNoFatalError(), error => error === original);
        assert.equal(f.lifecycle.observedInFlightOptionalReadCancellationCount, 0);
        assert.equal(f.harness.responses.get(f.path) || 0, 0);
        assert.equal(f.socket.commandsFor("Fetch.fulfillRequest").length, 1);
      } finally { await f.harness.close(); }
    });
  }

  test("optional read ACK is success and never a cancellation", async () => {
    const f = fixture();
    try {
      f.begin();
      const wire = await f.socket.waitForCommand("Fetch.fulfillRequest");
      const idle = f.harness.waitForRequestHandlersIdle({ timeoutMs: 1_000 });
      f.socket.respond(wire, { result: {} });
      await idle;
      f.harness.assertNoFatalError();
      assert.equal(f.harness.responses.get(f.path), 1);
      assert.deepEqual(f.harness.responseStatuses.get(f.path), [200]);
      assert.equal(f.lifecycle.observedInFlightOptionalReadCancellationCount, 0);
    } finally { await f.harness.close(); }
  });

  test("opted-in read canceled before preparation retires without sending an expired ID", async () => {
    const f = fixture();
    let release!: () => void;
    const held = new Promise<void>(resolve => { release = resolve; });
    f.harness.setRouteResolver(() => ({ body: [], requiredResponse: false, observeInFlightReadCancellation: true, waitUntil: held }));
    try {
      f.begin();
      f.socket.emitEvent("Network.loadingFailed", f.cancel);
      const idle = f.harness.waitForRequestHandlersIdle({ timeoutMs: 1_000 });
      release();
      await idle;
      f.harness.assertNoFatalError();
      assert.equal(f.lifecycle.preSettlementCancellationCount, 1);
      assert.equal(f.lifecycle.observedInFlightOptionalReadCancellationCount, 0);
      assert.equal(f.socket.commandsFor("Fetch.fulfillRequest").length, 0);
      assert.equal(f.harness.responses.get(f.path) || 0, 0);
    } finally { release(); await f.harness.close(); }
  });

  test("opted-in terminal Network cancellation reported before pause never sends the expired ID", async () => {
    const f = fixture({ path: "/settings/app" });
    try {
      f.socket.emitEvent("Page.frameNavigated", f.document);
      f.socket.emitEvent("Network.requestWillBeSent", f.start);
      f.socket.emitEvent("Network.loadingFailed", f.cancel);
      f.socket.emitEvent("Fetch.requestPaused", f.pause);
      await f.harness.waitForRequestHandlersIdle({ timeoutMs: 1_000 });
      f.harness.assertNoFatalError();
      assert.equal(f.lifecycle.preSettlementCancellationCount, 1);
      assert.equal(f.socket.commandsFor("Fetch.fulfillRequest").length, 0);
      assert.equal(f.harness.responses.get(f.path) || 0, 0);
    } finally { await f.harness.close(); }
  });
}
