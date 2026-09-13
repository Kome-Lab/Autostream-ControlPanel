import assert from "node:assert/strict";
import { existsSync } from "node:fs";
import test from "node:test";
import { invalidInterceptionIdMessage } from "./browser-request-lifecycle.mts";
import { createHarnessFixture, settlePromptly } from "./browser-cdp-socket-fixture.mts";
import { actionPath } from "./browser-lifecycle-source-paths.mts";


export function registerFatalLifecycleCases() {


test("BrowserHarness accepts only a known stale continue cancellation", async (t) => {
  const { harness, socket } = createHarnessFixture();
  t.after(() => harness.close());
  socket.hold("Fetch.continueRequest");
  socket.emitEvent("Fetch.requestPaused", {
    requestId: "stale-integration-1",
    request: { method: "GET", url: "http://fixture.test/old-document" },
  });
  const continueRequest = await socket.waitForCommand("Fetch.continueRequest");
  let idle = false;
  const handlerIdle = harness.waitForRequestHandlersIdle({ pathname: "/old-document", method: "GET", timeoutMs: 100 }).then(() => { idle = true; });
  await Promise.resolve();
  assert.equal(idle, false, "held continue request was reported idle");

  await harness.navigate("http://fixture.test/new-document");
  socket.respond(continueRequest, { error: { message: invalidInterceptionIdMessage } });
  await handlerIdle;
  harness.assertNoFatalError();
  assert.equal(harness.safeFetchCancellationCount, 1);
  assert.equal(harness.responses.get("/old-document") || 0, 0, "cancelled interception must not be counted as a response");
});

test("BrowserHarness fatal Fetch failure rejects Page.loadEventFired with the original cause", async (t) => {
  const { harness, socket } = createHarnessFixture();
  t.after(() => harness.close());
  socket.autoLoadEvent = false;
  socket.hold("Fetch.fulfillRequest");
  harness.setRouteResolver(() => ({ body: { ok: true }, requiredResponse: true }));

  const navigation = harness.navigate("http://fixture.test/current-document");
  await socket.waitForCommand("Page.navigate");
  socket.emitEvent("Fetch.requestPaused", {
    requestId: "required-integration-1",
    request: { method: "POST", url: `http://fixture.test${actionPath}` },
  });
  const fulfillRequest = await socket.waitForCommand("Fetch.fulfillRequest");
  socket.respond(fulfillRequest, { error: { message: invalidInterceptionIdMessage } });

  const outcome = await settlePromptly(navigation);
  assert.notEqual(outcome, "pending", "fatal navigation waited for the generic load timeout");
  assert.equal((outcome as Error).message, invalidInterceptionIdMessage);
  assert.throws(() => harness.assertNoFatalError(), /Invalid InterceptionId\./);
});

test("BrowserHarness socket close rejects Page.loadEventFired immediately", async (t) => {
  const { harness, socket } = createHarnessFixture();
  t.after(() => harness.close());
  socket.autoLoadEvent = false;
  const navigation = harness.navigate("http://fixture.test/socket-close");
  await socket.waitForCommand("Page.navigate");
  socket.close();

  const outcome = await settlePromptly(navigation);
  assert.notEqual(outcome, "pending", "socket-close navigation waited for the generic load timeout");
  assert.equal((outcome as Error).message, "Browser CDP connection closed");
});

test("unsafe matrix navigation before required POST settlement poisons later commands", async (t) => {
  const { harness, socket } = createHarnessFixture();
  t.after(() => harness.close());
  socket.hold("Fetch.fulfillRequest");
  harness.setRouteResolver(() => ({ body: { ready: true }, requiredResponse: true }));
  socket.emitEvent("Fetch.requestPaused", {
    requestId: "unsafe-matrix-1",
    request: { method: "POST", url: `http://fixture.test${actionPath}` },
  });
  const fulfillRequest = await socket.waitForCommand("Fetch.fulfillRequest");
  await harness.waitForRequestCount(actionPath, 1);
  const handlerIdle = harness.waitForRequestHandlersIdle({ pathname: actionPath, method: "POST", timeoutMs: 100 });

  await harness.navigate("http://fixture.test/admin/streams/");
  socket.respond(fulfillRequest, { error: { message: invalidInterceptionIdMessage } });
  await assert.rejects(handlerIdle, /Invalid InterceptionId\./);
  await assert.rejects(harness.evaluate("true"), /Invalid InterceptionId\./);
});

test("BrowserHarness close rejects a pending load waiter and removes its profile", async () => {
  const { harness, profile, socket } = createHarnessFixture();
  socket.autoLoadEvent = false;
  const navigation = harness.navigate("http://fixture.test/harness-close");
  await socket.waitForCommand("Page.navigate");
  await harness.close();

  const outcome = await settlePromptly(navigation);
  assert.notEqual(outcome, "pending", "harness close left the load waiter pending");
  assert.equal((outcome as Error).message, "Browser harness closed");
  assert.equal(existsSync(profile), false, "harness close left its owned profile behind");
});
}
