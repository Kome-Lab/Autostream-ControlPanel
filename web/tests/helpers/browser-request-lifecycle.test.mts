import assert from "node:assert/strict";
import { existsSync, mkdtempSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";

import { BrowserHarness } from "./browser-harness.mts";
import {
  FetchRequestLifecycle,
  RejectableEventWaiters,
  invalidInterceptionIdMessage,
} from "./browser-request-lifecycle.mts";

const actionPath = "/streams/fixture/start-readiness";

test("known stale interception cancelled by a newer navigation is nonfatal", () => {
  const lifecycle = new FetchRequestLifecycle();
  lifecycle.register({ requestId: "stale-1", method: "GET", pathname: "/old-document", requiredResponse: false });
  const attempt = lifecycle.beginSettlement("stale-1", "Fetch.continueRequest");
  lifecycle.beginNavigation("navigate");

  assert.deepEqual(lifecycle.handleSettlementError(attempt, new Error(invalidInterceptionIdMessage)), { cancelled: true });
  lifecycle.assertHealthy();
  assert.equal(lifecycle.activeCount, 0);
  assert.equal(lifecycle.safeCancellationCount, 1);
});

test("required current interception cancellation fails the scenario", () => {
  const lifecycle = new FetchRequestLifecycle();
  lifecycle.register({ requestId: "required-1", method: "POST", pathname: actionPath, requiredResponse: true });
  const attempt = lifecycle.beginSettlement("required-1", "Fetch.fulfillRequest");

  assert.throws(
    () => lifecycle.handleSettlementError(attempt, new Error(invalidInterceptionIdMessage)),
    /Invalid InterceptionId\./,
  );
});

test("required stale interception cancellation remains fatal", () => {
  const lifecycle = new FetchRequestLifecycle();
  lifecycle.register({ requestId: "required-stale-1", method: "POST", pathname: actionPath, requiredResponse: true });
  const attempt = lifecycle.beginSettlement("required-stale-1", "Fetch.fulfillRequest");
  lifecycle.beginNavigation("navigate");

  assert.throws(
    () => lifecycle.handleSettlementError(attempt, new Error(invalidInterceptionIdMessage)),
    /Invalid InterceptionId\./,
  );
});

test("known stale non-required fulfill cancellation is nonfatal", () => {
  const lifecycle = new FetchRequestLifecycle();
  lifecycle.register({ requestId: "optional-stale-1", method: "GET", pathname: "/optional", requiredResponse: false });
  const attempt = lifecycle.beginSettlement("optional-stale-1", "Fetch.fulfillRequest");
  lifecycle.beginNavigation("reload");

  assert.deepEqual(lifecycle.handleSettlementError(attempt, new Error(invalidInterceptionIdMessage)), { cancelled: true });
  lifecycle.assertHealthy();
  assert.equal(lifecycle.activeCount, 0);
});

test("unknown request IDs and duplicate settlement attempts are fatal", () => {
  const lifecycle = new FetchRequestLifecycle();
  assert.throws(
    () => lifecycle.beginSettlement("missing", "Fetch.continueRequest"),
    /Unknown Fetch request ID: missing/,
  );

  lifecycle.register({ requestId: "duplicate-1", method: "GET", pathname: "/fixture", requiredResponse: false });
  lifecycle.beginSettlement("duplicate-1", "Fetch.continueRequest");
  assert.throws(
    () => lifecycle.beginSettlement("duplicate-1", "Fetch.continueRequest"),
    /Duplicate Fetch settlement attempt: duplicate-1/,
  );
  assert.throws(
    () => lifecycle.register({ requestId: "duplicate-1", method: "GET", pathname: "/fixture", requiredResponse: false }),
    /Duplicate Fetch request ID: duplicate-1/,
  );
});

test("non-Invalid-InterceptionId CDP errors remain fatal", () => {
  const lifecycle = new FetchRequestLifecycle();
  lifecycle.register({ requestId: "cdp-1", method: "GET", pathname: "/fixture", requiredResponse: false });
  const attempt = lifecycle.beginSettlement("cdp-1", "Fetch.continueRequest");
  lifecycle.beginNavigation("reload");
  assert.throws(() => lifecycle.handleSettlementError(attempt, new Error("Target closed")), /Target closed/);
});

test("malformed Invalid InterceptionId errors remain fatal", () => {
  const lifecycle = new FetchRequestLifecycle();
  lifecycle.register({ requestId: "malformed-1", method: "GET", pathname: "/fixture", requiredResponse: false });
  const attempt = lifecycle.beginSettlement("malformed-1", "Fetch.continueRequest");
  lifecycle.beginNavigation("navigate");
  assert.throws(
    () => lifecycle.handleSettlementError(attempt, new Error("Invalid InterceptionId")),
    /^Error: Invalid InterceptionId$/,
  );
});

test("Page.loadEventFired waiter rejects immediately with the fatal cause", async () => {
  const waiters = new RejectableEventWaiters();
  const fatal = new Error("fatal fetch failure");
  const waiter = waiters.wait("Page.loadEventFired", 1_000, "load timeout");
  waiters.rejectAll(fatal);

  const outcome = await settlePromptly(waiter.promise);
  if (outcome === "pending") waiter.cancel(new Error("Red fixture cleanup"));
  assert.notEqual(outcome, "pending", "fatal waiter stayed pending until its timeout");
  assert.equal(outcome, fatal);
  assert.equal(waiters.pendingCount, 0);
});

test("Page.loadEventFired waiter rejects immediately on socket close", async () => {
  const waiters = new RejectableEventWaiters();
  const closed = new Error("Browser CDP connection closed");
  const waiter = waiters.wait("Page.loadEventFired", 1_000, "load timeout");
  waiters.rejectAll(closed);

  const outcome = await settlePromptly(waiter.promise);
  if (outcome === "pending") waiter.cancel(new Error("Red fixture cleanup"));
  assert.notEqual(outcome, "pending", "socket-close waiter stayed pending until its timeout");
  assert.equal(outcome, closed);
  assert.equal(waiters.pendingCount, 0);
});

test("successful event resolves once and removes its waiter", async () => {
  const waiters = new RejectableEventWaiters();
  const waiter = waiters.wait("Page.loadEventFired", 1_000, "load timeout");
  waiters.resolve("Page.loadEventFired", { frameId: "main" });
  waiters.resolve("Page.loadEventFired", { frameId: "duplicate" });
  assert.deepEqual(await waiter.promise, { frameId: "main" });
  assert.equal(waiters.pendingCount, 0);
});

test("event timeout removes its waiter", async () => {
  const waiters = new RejectableEventWaiters();
  await assert.rejects(waiters.wait("Page.loadEventFired", 5, "bounded load timeout").promise, /bounded load timeout/);
  assert.equal(waiters.pendingCount, 0);
});

test("action-path settlement wait ignores background polling", async () => {
  const lifecycle = new FetchRequestLifecycle();
  lifecycle.register({ requestId: "poll-1", method: "GET", pathname: "/service-health", requiredResponse: false });
  lifecycle.register({ requestId: "action-1", method: "POST", pathname: actionPath, requiredResponse: true });
  const actionAttempt = lifecycle.beginSettlement("action-1", "Fetch.fulfillRequest");
  const actionIdle = lifecycle.waitForIdle({ pathname: actionPath, method: "POST", timeoutMs: 100 });

  lifecycle.completeSettlement(actionAttempt);
  await actionIdle;
  assert.equal(lifecycle.activeCount, 1, "background polling must not block the focused action wait");
  lifecycle.close();
  assert.equal(lifecycle.activeCount, 0);
});

test("pending deferred request is not idle and becomes idle only after settlement", async () => {
  const lifecycle = new FetchRequestLifecycle();
  lifecycle.register({ requestId: "deferred-1", method: "POST", pathname: actionPath, requiredResponse: true });
  const attempt = lifecycle.beginSettlement("deferred-1", "Fetch.fulfillRequest");
  let settled = false;
  const idle = lifecycle.waitForIdle({ pathname: actionPath, method: "POST", timeoutMs: 100 }).then(() => { settled = true; });
  await Promise.resolve();
  assert.equal(settled, false, "pending deferred request was reported idle");

  lifecycle.completeSettlement(attempt);
  await idle;
  assert.equal(settled, true);
  assert.equal(lifecycle.activeCount, 0);
});

test("settlement timeout fails with bounded diagnostics", async () => {
  const lifecycle = new FetchRequestLifecycle();
  for (let index = 0; index < 8; index += 1) {
    lifecycle.register({ requestId: `diagnostic-${index}`, method: "POST", pathname: actionPath, requiredResponse: true });
  }
  await assert.rejects(
    lifecycle.waitForIdle({ pathname: actionPath, method: "POST", timeoutMs: 5 }),
    (error: Error) => {
      assert.match(error.message, /Timed out waiting for request handlers to settle/);
      assert.match(error.message, /"activeCount":8/);
      assert.match(error.message, /"omitted":3/);
      assert.doesNotMatch(error.message, /diagnostic-5/);
      return true;
    },
  );
  lifecycle.close();
  assert.equal(lifecycle.activeCount, 0);
});

test("fatal error rejects a pending settlement wait with the original cause", async () => {
  const lifecycle = new FetchRequestLifecycle();
  lifecycle.register({ requestId: "fatal-1", method: "POST", pathname: actionPath, requiredResponse: true });
  const idle = lifecycle.waitForIdle({ pathname: actionPath, timeoutMs: 1_000 });
  const fatal = new Error("fatal request handler");
  lifecycle.fail(fatal);
  await assert.rejects(idle, (error) => error === fatal);
  assert.throws(() => lifecycle.assertHealthy(), (error) => error === fatal);
  lifecycle.close();
});

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

async function settlePromptly(promise: Promise<unknown>) {
  return Promise.race([
    promise.then(
      () => new Error("waiter unexpectedly resolved"),
      (error) => error,
    ),
    new Promise<"pending">((resolvePending) => setTimeout(() => resolvePending("pending"), 20)),
  ]);
}

type FakeCDPCommand = {
  id: number;
  method: string;
  params: Record<string, unknown>;
  sessionId?: string;
};

class FakeCDPSocket extends EventTarget {
  autoLoadEvent = true;
  private readonly commands: FakeCDPCommand[] = [];
  private readonly heldMethods = new Set<string>();
  private readonly commandWaiters = new Map<string, Array<(command: FakeCDPCommand) => void>>();

  hold(method: string) {
    this.heldMethods.add(method);
  }

  send(rawMessage: string) {
    const command = JSON.parse(rawMessage) as FakeCDPCommand;
    this.commands.push(command);
    const waiter = this.commandWaiters.get(command.method)?.shift();
    if (waiter) waiter(command);
    if (this.heldMethods.has(command.method)) return;
    queueMicrotask(() => {
      this.respond(command, { result: {} });
      if (command.method === "Page.navigate" && this.autoLoadEvent) {
        this.emitEvent("Page.loadEventFired", { timestamp: 1 });
      }
    });
  }

  close() {
    this.dispatchEvent(new Event("close"));
  }

  emitEvent(method: string, params: Record<string, unknown>) {
    this.emitMessage({ method, params, sessionId: "test-session" });
  }

  respond(command: FakeCDPCommand, response: { result?: Record<string, unknown>; error?: { message: string } }) {
    this.emitMessage({ id: command.id, sessionId: command.sessionId, ...response });
  }

  waitForCommand(method: string) {
    const existing = this.commands.find((command) => command.method === method);
    if (existing) return Promise.resolve(existing);
    return new Promise<FakeCDPCommand>((resolveCommand) => {
      const waiters = this.commandWaiters.get(method) || [];
      waiters.push(resolveCommand);
      this.commandWaiters.set(method, waiters);
    });
  }

  private emitMessage(message: Record<string, unknown>) {
    this.dispatchEvent(new MessageEvent("message", { data: JSON.stringify(message) }));
  }
}

function createHarnessFixture() {
  const socket = new FakeCDPSocket();
  const profile = mkdtempSync(join(tmpdir(), "autostream-ui-browser-"));
  const browserProcess = { exitCode: 0, signalCode: null };
  const harness = Reflect.construct(
    BrowserHarness,
    [browserProcess, profile, socket as unknown as WebSocket, "test-session"],
  ) as BrowserHarness;
  return { harness, profile, socket };
}
