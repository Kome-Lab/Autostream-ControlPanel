import assert from "node:assert/strict";
import test from "node:test";
import { createHarnessFixture } from "./browser-cdp-socket-fixture.mts";
import { invalidInterceptionIdMessage, FetchRequestLifecycle } from "./browser-request-lifecycle.mts";
import type { BrowserNetworkCancelProof } from "./browser-network-cancel-proof.mts";
import type { PendingCommand } from "./browser-command-deadline.mts";

export function registerLateDocumentCases() {
  for (const name of ["proved old document", "commit after pause", "Network start before navigation then commit after pause", "required GET", "non-GET", "default opt-out", "foreign loader", "duplicate Network start", "new document already committed"]) {
    test(`late requestPaused during navigation: ${name}`, async () => {
      const { harness, socket } = createHarnessFixture();
      const internal = harness as unknown as { requestLifecycle: FetchRequestLifecycle; pendingCommands: Map<number, PendingCommand> };
      const path = "/settings/app", url = "http://127.0.0.1:3002" + path;
      const method = name === "non-GET" ? "POST" : "GET";
      const proved = ["proved old document", "commit after pause", "Network start before navigation then commit after pause"].includes(name);
      const frame = "late-frame", oldLoader = "late-old-loader", fetchID = "late-fetch", networkID = "late-network";
      socket.autoLoadEvent = false;
      socket.hold("Page.navigate"); socket.hold("Fetch.fulfillRequest");
      harness.setRouteResolver(() => ({ body: [], requiredResponse: name === "required GET", observeInFlightReadCancellation: name !== "default opt-out" }));
      let navigation: Promise<unknown> | undefined;
      try {
        socket.emitEvent("Page.frameNavigated", { frame: { id: frame, loaderId: oldLoader, url: "http://127.0.0.1:3002/admin/" } });
        const start = { requestId: networkID, frameId: frame, loaderId: name === "foreign loader" ? "foreign-loader" : oldLoader, type: "Fetch", request: { method, url } };
        if (name.startsWith("Network start before navigation")) socket.emitEvent("Network.requestWillBeSent", start);
        navigation = harness.navigate("http://127.0.0.1:3002/admin/streams/").catch(error => error);
        const navigate = await socket.waitForCommand("Page.navigate");
        if (name === "new document already committed") socket.emitEvent("Page.frameNavigated", { frame: { id: frame, loaderId: "new-loader", url } });
        if (!name.startsWith("Network start before navigation")) socket.emitEvent("Network.requestWillBeSent", start);
        if (name === "duplicate Network start") socket.emitEvent("Network.requestWillBeSent", start);
        socket.emitEvent("Fetch.requestPaused", { requestId: fetchID, networkId: networkID, frameId: frame, resourceType: "XHR", request: { method, url } });
        if (name.includes("commit after pause")) socket.emitEvent("Page.frameNavigated", { frame: { id: frame, loaderId: "new-loader", url } });
        const wire = await socket.waitForCommand("Fetch.fulfillRequest");
        const pending = internal.pendingCommands.get(wire.id); assert.ok(pending);
        let original: Error | undefined;
        const reject = pending.reject; pending.reject = error => { original = error; reject(error); };
        const idle = harness.waitForRequestHandlersIdle({ timeoutMs: 1_000 });
        const outcome = proved ? idle : assert.rejects(idle, error => error === original);
        socket.emitEvent("Network.loadingFailed", { requestId: networkID, canceled: true, errorText: "net::ERR_ABORTED" });
        socket.respond(wire, { error: { message: invalidInterceptionIdMessage } });
        await outcome;
        if (proved) {
          harness.assertNoFatalError();
          assert.equal(internal.requestLifecycle.safeCancellationCount, 1);
          assert.equal(internal.requestLifecycle.observedInFlightOptionalReadCancellationCount, 0, "old-document invalidation is not same-document retirement");
          socket.respond(navigate, { result: {} });
          socket.emitEvent("Page.frameNavigated", { frame: { id: frame, loaderId: "new-loader", url } });
          socket.emitEvent("Page.loadEventFired", {});
          assert.equal(await navigation, undefined);
        } else {
          assert.throws(() => harness.assertNoFatalError(), error => error === original);
          assert.equal(internal.requestLifecycle.safeCancellationCount, 0);
        }
        assert.equal(harness.responses.get(path) || 0, 0);
        assert.equal(socket.commandsFor("Fetch.fulfillRequest").length, 1);
      } finally { await harness.close(); await navigation; }
    });
  }

  for (const mode of ["single-use and unforgeable", "wrong owner burns receipt", "intervening event expires receipt"]) {
    test(`old-document registration receipt is bounded: ${mode}`, async () => {
      const { harness, socket } = createHarnessFixture();
      const { requestLifecycle: lifecycle, networkCancellation: proof } = harness as unknown as { requestLifecycle: FetchRequestLifecycle; networkCancellation: BrowserNetworkCancelProof };
      const frame = "receipt-frame", loader = "receipt-loader", fetch = "receipt-fetch", network = "receipt-network", url = "http://127.0.0.1:3002/settings/app";
      let release!: () => void;
      const held = new Promise<void>(resolve => { release = resolve; });
      harness.setRouteResolver(() => ({ body: [], requiredResponse: false, waitUntil: held }));
      socket.autoLoadEvent = false; socket.hold("Page.navigate"); socket.hold("Fetch.fulfillRequest");
      let navigation: Promise<unknown> | undefined;
      try {
        socket.emitEvent("Page.frameNavigated", { frame: { id: frame, loaderId: loader, url } });
        navigation = harness.navigate("http://127.0.0.1:3002/admin/").catch(error => error);
        const navigate = await socket.waitForCommand("Page.navigate");
        socket.emitEvent("Network.requestWillBeSent", { requestId: network, frameId: frame, loaderId: loader, type: "Fetch", request: { method: "GET", url } });
        socket.emitEvent("Fetch.requestPaused", { requestId: fetch, networkId: network, frameId: frame, resourceType: "XHR", request: { method: "GET", url } });
        const ticket = proof.claimLateDocumentRegistration(fetch, { generation: lifecycle.generation, frameId: frame });
        assert.ok(ticket);
        assert.deepEqual(Object.keys(ticket), []);
        assert.throws(() => lifecycle.bindLateDocumentRegistration(fetch, { ...ticket }), /Invalid old-document/);
        if (mode === "single-use and unforgeable") {
          lifecycle.bindLateDocumentRegistration(fetch, ticket);
          assert.throws(() => lifecycle.bindLateDocumentRegistration(fetch, ticket), /Invalid old-document/);
          assert.equal(JSON.parse(lifecycle.diagnostics()).active[0].generation, 0);
        } else {
          if (mode === "wrong owner burns receipt") {
            assert.throws(() => new FetchRequestLifecycle().bindLateDocumentRegistration(fetch, ticket), /Invalid old-document/);
          } else {
            socket.emitEvent("Network.requestWillBeSent", { requestId: "other-network", frameId: frame, loaderId: loader, type: "Fetch", request: { method: "GET", url } });
          }
          assert.throws(() => lifecycle.bindLateDocumentRegistration(fetch, ticket), /Invalid old-document/);
          assert.equal(JSON.parse(lifecycle.diagnostics()).active[0].generation, 1);
        }
        release();
        const wire = await socket.waitForCommand("Fetch.fulfillRequest");
        const idle = harness.waitForRequestHandlersIdle({ timeoutMs: 1_000 });
        socket.respond(wire, { result: {} }); await idle;
        socket.respond(navigate, { result: {} }); socket.emitEvent("Page.loadEventFired", {}); await navigation;
        harness.assertNoFatalError();
      } finally { release(); await harness.close(); await navigation; }
    });
  }
}
