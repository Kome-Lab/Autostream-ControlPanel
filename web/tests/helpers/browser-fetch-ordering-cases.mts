import assert from "node:assert/strict";
import test from "node:test";
import { createHarnessFixture } from "./browser-cdp-socket-fixture.mts";
import { invalidInterceptionIdMessage } from "./browser-request-lifecycle.mts";
import type { PendingCommand } from "./browser-command-deadline.mts";

const fetchID = "ordering-fetch", networkID = "ordering-network", frame = "ordering-frame";
const pathname = "/integrations/oauth-accounts", url = `http://127.0.0.1:3002${pathname}`;

function events(method = "GET") {
  return {
    document: { frame: { id: frame, loaderId: "ordering-loader", url: "http://127.0.0.1:3002/admin/" } },
    pause: { requestId: fetchID, networkId: networkID, frameId: frame, resourceType: "XHR", request: { method, url } },
    start: { requestId: networkID, frameId: frame, loaderId: "ordering-loader", type: "Fetch", request: { method, url } },
    cancel: { requestId: networkID, canceled: true, errorText: "net::ERR_ABORTED", type: "Fetch" },
  };
}

export function registerFetchOrderingCases() {
  for (const variant of [
    { name: "optional GET, Network start before pause", method: "GET", required: false, pauseFirst: false },
    { name: "optional GET, Network start after first send", method: "GET", required: false, pauseFirst: true },
    { name: "required GET", method: "GET", required: true, pauseFirst: false },
    { name: "non-GET", method: "POST", required: false, pauseFirst: false },
  ]) {
    test(`same-document in-flight cancellation preserves fatal refusal: ${variant.name}`, async () => {
      const { harness, socket } = createHarnessFixture();
      const e = events(variant.method);
      socket.hold("Fetch.fulfillRequest");
      harness.setRouteResolver(() => ({ body: [], requiredResponse: variant.required }));
      try {
        socket.emitEvent("Page.frameNavigated", e.document);
        if (!variant.pauseFirst) socket.emitEvent("Network.requestWillBeSent", e.start);
        socket.emitEvent("Fetch.requestPaused", e.pause);
        const wire = await socket.waitForCommand("Fetch.fulfillRequest");
        assert.equal(wire.params.requestId, fetchID, "settlement must use Fetch ID, never Network ID");
        if (variant.pauseFirst) socket.emitEvent("Network.requestWillBeSent", e.start);
        const idle = harness.waitForRequestHandlersIdle({ timeoutMs: 1_000 });
        const pending = (harness as unknown as { pendingCommands: Map<number, PendingCommand> }).pendingCommands.get(wire.id);
        assert.ok(pending);
        let original: Error | undefined;
        const reject = pending.reject;
        pending.reject = error => { original = error; reject(error); };
        const rejected = assert.rejects(idle, error => error === original);
        socket.emitEvent("Network.loadingFailed", e.cancel);
        socket.respond(wire, { error: { message: invalidInterceptionIdMessage } });
        await rejected;
        assert.ok(original);
        assert.throws(() => harness.assertNoFatalError(), error => error === original);
        assert.equal(socket.commandsFor("Fetch.fulfillRequest").length, 1);
        assert.equal(socket.commandsFor("Fetch.continueRequest").length, 0);
        assert.equal(harness.requests.get(pathname), 1);
        assert.equal(harness.responses.get(pathname) || 0, 0);
        const diagnostic = JSON.parse(harness.fetchFailureDiagnosticJSON!);
        assert.equal(diagnostic.request_generation, 0);
        assert.equal(diagnostic.current_generation, 0);
        assert.equal(diagnostic.cancellation_context_present, false);
        assert.equal(diagnostic.navigation_reason, "none");
        assert.equal(diagnostic.settlement_attempt, 1);
        assert.equal(diagnostic.network_cancellation_diagnostic.route, "OAUTH_ACCOUNTS");
      } finally { await harness.close(); }
    });
  }

  test("duplicate requestPaused cannot reuse an interception ID for a second send", async () => {
    const { harness, socket } = createHarnessFixture();
    const e = events();
    socket.hold("Fetch.fulfillRequest");
    harness.setRouteResolver(() => ({ body: [], requiredResponse: false }));
    try {
      socket.emitEvent("Page.frameNavigated", e.document);
      socket.emitEvent("Network.requestWillBeSent", e.start);
      socket.emitEvent("Fetch.requestPaused", e.pause);
      await socket.waitForCommand("Fetch.fulfillRequest");
      const rejected = assert.rejects(harness.waitForRequestHandlersIdle({ timeoutMs: 1_000 }), /Duplicate Fetch request ID/);
      socket.emitEvent("Fetch.requestPaused", e.pause);
      await rejected;
      assert.equal(socket.commandsFor("Fetch.fulfillRequest").length, 1);
      assert.equal(harness.responses.get(pathname) || 0, 0);
    } finally { await harness.close(); }
  });

  test("a proved cancellation before the first send never sends the expired Fetch ID", async () => {
    const { harness, socket } = createHarnessFixture();
    const e = events();
    let release!: () => void;
    const held = new Promise<void>(resolve => { release = resolve; });
    harness.setRouteResolver(() => ({ body: [], requiredResponse: false, waitUntil: held }));
    try {
      socket.emitEvent("Page.frameNavigated", e.document);
      socket.emitEvent("Network.requestWillBeSent", e.start);
      socket.emitEvent("Fetch.requestPaused", e.pause);
      socket.emitEvent("Network.loadingFailed", e.cancel);
      const idle = harness.waitForRequestHandlersIdle({ timeoutMs: 1_000 });
      release();
      await idle;
      harness.assertNoFatalError();
      assert.equal(socket.commandsFor("Fetch.fulfillRequest").length, 0);
      assert.equal(socket.commandsFor("Fetch.continueRequest").length, 0);
      assert.equal(harness.responses.get(pathname) || 0, 0);
    } finally { release(); await harness.close(); }
  });
}
