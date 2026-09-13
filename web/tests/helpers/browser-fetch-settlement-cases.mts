import assert from "node:assert/strict";
import test from "node:test";
import { FetchRequestLifecycle, RejectableEventWaiters, invalidInterceptionIdMessage } from "./browser-request-lifecycle.mts";
import { actionPath } from "./browser-lifecycle-source-paths.mts";
import { settlePromptly } from "./browser-cdp-socket-fixture.mts";


export function registerFetchSettlementCases() {


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
}
