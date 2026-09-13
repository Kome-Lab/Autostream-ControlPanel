import assert from "node:assert/strict";
import { readBrowserSuiteSource } from "./read-browser-suite-source.mts";
import test from "node:test";
import ts from "typescript";
import { FetchRequestLifecycle, invalidInterceptionIdMessage } from "./browser-request-lifecycle.mts";
import { createHarnessFixture, settlePromptly } from "./browser-cdp-socket-fixture.mts";
import { accountSettlementHelper, assertAccountSettlementConnections, accountScenario } from "./browser-account-settlement-oracle.mts";
import { preferencePath, uiBrowserTestPath } from "./browser-lifecycle-source-paths.mts";
import { callsWithin } from "./browser-runner-preservation-fixture.mts";
import { identifierCall } from "./browser-worker-focus-expressions.mts";


export function registerAccountSettlementCases() {


for (const status of [200, 409]) {
  for (const boundary of ["settled", "arrival-only", "UI-only", "unawaited", "wrong-method", "empty-idle"] as const) {
    test(`Account PUT ${status}: ${boundary} boundary uses the real harness and scenario helper`, async (t) => {
      const { harness, socket } = createHarnessFixture();
      t.after(() => harness.close());
      const methods: string[] = [];
      const waitForPreferenceSettlement = accountSettlementHelper(harness, methods);
      let fixture = { status, body: { theme_id: "violet", color_mode: "light" } };
      harness.setRouteResolver(({ method }) => { methods.push(method); return fixture; });
      socket.hold("Fetch.fulfillRequest");
      socket.hold("Runtime.evaluate");
      const emptyIdle = boundary === "empty-idle"
        ? harness.waitForRequestHandlersIdle({ pathname: preferencePath, method: "PUT" }) : undefined;
      socket.emitEvent("Fetch.requestPaused", {
        requestId: "account-save", request: { method: "PUT", url: `http://fixture.test${preferencePath}` },
      });
      const fulfill = await socket.waitForCommand("Fetch.fulfillRequest");
      assert.equal(fulfill.params.responseCode, status);
      await harness.waitForRequestCount(preferencePath, 1);
      if (boundary !== "arrival-only") {
        const ui = harness.waitFor("document.documentElement.dataset.theme === 'violet'", Boolean, "controlled UI result missing");
        socket.respond(await socket.waitForCommand("Runtime.evaluate"), { result: { result: { value: true } } });
        await ui;
      }
      let changed = false;
      let unawaited: Promise<void> | undefined;
      const nextPhase = (async () => {
        if (boundary === "settled") await waitForPreferenceSettlement("PUT", 1);
        if (boundary === "unawaited") unawaited = waitForPreferenceSettlement("PUT", 1);
        if (boundary === "wrong-method") await harness.waitForRequestHandlersIdle({ pathname: preferencePath, method: "GET" });
        if (boundary === "empty-idle") await emptyIdle;
        fixture = { status: 200, body: { theme_id: "ocean", color_mode: "dark" } };
        changed = true;
        await harness.navigate("http://fixture.test/admin/account/");
      })();
      // Deliver the helper's request-observation evaluation without acknowledging Fetch.
      for (const evaluation of socket.commandsFor("Runtime.evaluate").slice(1)) {
        socket.respond(evaluation, { result: { result: { value: true } } });
      }
      await new Promise<void>((resolveTurn) => setImmediate(resolveTurn));
      assert.equal(changed, boundary !== "settled");
      assert.equal(socket.commandsFor("Page.navigate").length, boundary === "settled" ? 0 : 1);
      assert.equal(harness.responses.get(preferencePath) || 0, 0);
      socket.respond(fulfill, { result: {} });
      await nextPhase;
      await unawaited;
      await harness.waitForRequestHandlersIdle({ pathname: preferencePath, method: "PUT" });
      assert.equal(changed, true);
      assert.equal(socket.commandsFor("Page.navigate").length, 1);
      assert.equal(harness.responses.get(preferencePath), 1);
      assert.deepEqual(harness.responseStatuses.get(preferencePath), [status]);
      assert.equal(harness.safeFetchCancellationCount, 0);
      harness.assertNoFatalError();
    });
  }
}

test("Account settlement helper waits for a new request before accepting idle", async (t) => {
  const { harness, socket } = createHarnessFixture();
  t.after(() => harness.close());
  socket.hold("Runtime.evaluate");
  socket.hold("Fetch.fulfillRequest");
  const methods: string[] = [];
  const wait = accountSettlementHelper(harness, methods);
  await assert.rejects(wait("GET", 0), /observed request phase/);
  let completed = false;
  const pending = wait("GET", 1).then(() => { completed = true; });
  const observation = await socket.waitForCommand("Runtime.evaluate");
  assert.equal(completed, false);
  harness.setRouteResolver(({ method }) => { methods.push(method); return { body: {} }; });
  socket.emitEvent("Fetch.requestPaused", { requestId: "fresh-get", request: { method: "GET", url: `http://fixture.test${preferencePath}` } });
  const fulfill = await socket.waitForCommand("Fetch.fulfillRequest");
  socket.respond(observation, { result: { result: { value: true } } });
  await new Promise<void>((resolveTurn) => setImmediate(resolveTurn));
  assert.equal(completed, false, "arrival must not complete the phase before its Fetch acknowledgement");
  socket.respond(fulfill, { result: {} });
  await pending;
  assert.equal(completed, true);
});

test("actual Account scenario connects awaited settlement after UI and before every phase change", () => {
  const source = readBrowserSuiteSource(uiBrowserTestPath);
  assertAccountSettlementConnections(source);
  const { body } = accountScenario(source);
  const barriers = body.statements.filter(ts.isExpressionStatement)
    .filter((statement) => callsWithin(statement).some((call) => identifierCall(call, "waitForPreferenceSettlement")));
  for (const statement of barriers) {
    const start = statement.getStart();
    assert.throws(() => assertAccountSettlementConnections(source.slice(0, start) + source.slice(statement.end)), /Account/);
    assert.throws(() => assertAccountSettlementConnections(source.slice(0, start) + statement.getText().replace(/^await /, "") + source.slice(statement.end)), /Account/);
  }
  const saved = barriers.find((statement) => statement.getText().includes('("PUT", 1)'))!;
  const conflictFixture = body.statements.find((statement) => statement.getText().startsWith("fixture.uiPreferenceWriteResponse = { status: 409"))!;
  const without = source.slice(0, saved.getStart()) + source.slice(saved.end);
  const mutated = without.replace(conflictFixture.getText(), conflictFixture.getText() + "\n" + saved.getText());
  assert.throws(() => assertAccountSettlementConnections(mutated), /Account/);
  assert.throws(() => assertAccountSettlementConnections(source.replace('waitForPreferenceSettlement("PUT", 1)', 'waitForPreferenceSettlement("GET", 1)')), /Account/);
  assert.throws(() => assertAccountSettlementConnections(source.replace('const savedGet = preferenceRequestCount("GET") + 1', 'const savedGet = 1')), /Account/);
  assert.throws(() => assertAccountSettlementConnections(source.replace('pathname: "/account/preferences/ui", method', 'pathname: "/wrong", method')), /Account/);
  assert.throws(() => assertAccountSettlementConnections(`// ${source.replaceAll("\n", "\n// ")}`), /Account/);
  assert.throws(() => assertAccountSettlementConnections(""), /Account/);
});

for (const stale of [false, true]) {
  for (const diagnosticFailure of [false, true]) {
    test(`fatal Account diagnostic retains original error, counts and waiters: stale=${stale}, diagnosticFailure=${diagnosticFailure}`, async (t) => {
      const { harness, socket } = createHarnessFixture();
      t.after(() => harness.close());
      const lines: string[] = [];
      t.mock.method(console, "error", (line: string) => { lines.push(line); });
      const lifecycle = Reflect.get(harness, "requestLifecycle") as FetchRequestLifecycle;
      const diagnostic = lifecycle.settlementFailureDiagnostic.bind(lifecycle);
      t.mock.method(lifecycle, "settlementFailureDiagnostic", (...args: Parameters<typeof diagnostic>) => {
        const before = lifecycle.diagnostics();
        if (diagnosticFailure) throw new Error("diagnostic sentinel must not replace fatal");
        const result = diagnostic(...args);
        assert.equal(lifecycle.diagnostics(), before);
        assert.equal(lifecycle.activeCount, 2);
        assert.equal(lifecycle.safeCancellationCount, 0);
        return result;
      });
      socket.hold("Fetch.fulfillRequest");
      socket.autoLoadEvent = false;
      const navigations = [harness.navigate("http://fixture.test/pending")];
      harness.setRouteResolver(() => ({ body: { secret: "BODY_SENTINEL" }, requiredResponse: true }));
      for (const id of ["ID_SENTINEL_ONE", "ID_SENTINEL_TWO"]) {
        socket.emitEvent("Fetch.requestPaused", { requestId: id, request: { method: "PUT", url: `http://URL_SENTINEL.test${preferencePath}?token=TOKEN_SENTINEL#HASH_SENTINEL`, postData: "POST_SENTINEL" } });
      }
      if (stale) navigations.push(harness.navigate("http://fixture.test/next"));
      const idle = harness.waitForRequestHandlersIdle({ pathname: preferencePath, method: "PUT" });
      const navigationOutcomes = Promise.all(navigations.map(settlePromptly));
      const idleOutcome = settlePromptly(idle);
      for (const command of socket.commandsFor("Fetch.fulfillRequest")) socket.respond(command, { error: { message: invalidInterceptionIdMessage } });
      const outcomes = await navigationOutcomes;
      const fatal = outcomes[0];
      assert.ok(fatal instanceof Error);
      assert.equal(fatal.message, invalidInterceptionIdMessage);
      assert.equal(await idleOutcome, fatal);
      assert.ok(outcomes.every((outcome) => outcome === fatal));
      assert.throws(() => harness.assertNoFatalError(), (error) => error === fatal);
      await assert.rejects(harness.evaluate("true"), (error) => error === fatal);
      assert.equal(harness.requests.get(preferencePath), 2);
      assert.equal(harness.responses.size, 0);
      assert.equal(harness.responseStatuses.size, 0);
      assert.equal(harness.safeFetchCancellationCount, 0);
      assert.equal(lines.length, diagnosticFailure ? 0 : 1);
      for (const line of lines) {
        assert.ok(Buffer.byteLength(line) <= 2_048);
        assert.doesNotMatch(line, /SENTINEL|fixture\.test|Invalid InterceptionId\.|requestId|pathname|postData/);
        const data = JSON.parse(line.slice("BROWSER_FETCH_FAILURE ".length));
        assert.equal(data.route_class, "account-preferences-ui");
        assert.equal(data.required_response, true);
        assert.equal(data.command, "fulfill");
        assert.equal(data.error_category, "invalid_interception_id");
        assert.equal(data.cancellation_context_present, stale);
        assert.equal(data.request_generation < data.current_generation, stale);
      }
    });
  }
}

test("safe diagnostic classifies unknown inputs and clamps numbers without changing lifecycle results", () => {
  const lifecycle = new FetchRequestLifecycle();
  const unknown = lifecycle.settlementFailureDiagnostic({ requestId: "SECRET_ID", command: "SECRET_COMMAND" as "Fetch.continueRequest", attempt: Infinity }, new Error("SECRET_ERROR"));
  assert.deepEqual(unknown, { stage: "fetch-settlement", command: "unknown", method: "other", route_class: "other", request_known: false, required_response: "unknown", request_generation: "unknown", current_generation: 0, cancellation_context_present: false, navigation_reason: "none", settlement_attempt: 0, error_category: "other" });
  for (const [pathname, routeClass] of [["/auth/SECRET", "auth"], ["/setup/SECRET", "setup"], ["/SECRET", "other"]]) {
    const id = pathname;
    lifecycle.register({ requestId: id, method: "SECRET_METHOD", pathname, requiredResponse: false });
    const attempt = lifecycle.beginSettlement(id, "Fetch.continueRequest");
    lifecycle.beginNavigation("reload");
    const before = lifecycle.diagnostics();
    const data = lifecycle.settlementFailureDiagnostic({ ...attempt, attempt: Number.MAX_SAFE_INTEGER }, new Error(invalidInterceptionIdMessage));
    assert.equal(data.route_class, routeClass);
    assert.equal(data.method, "other");
    assert.equal(data.navigation_reason, "reload");
    assert.equal(data.settlement_attempt, 1_000_000);
    assert.doesNotMatch(JSON.stringify(data), /SECRET/);
    assert.equal(lifecycle.diagnostics(), before);
    assert.deepEqual(lifecycle.handleSettlementError(attempt, new Error(invalidInterceptionIdMessage)), { cancelled: true });
  }
  assert.equal(lifecycle.safeCancellationCount, 3);
  assert.equal(lifecycle.activeCount, 0);
  const missing = { requestId: "missing", command: "Fetch.continueRequest" as const, attempt: 1 };
  assert.throws(() => lifecycle.handleSettlementError(missing, new Error(invalidInterceptionIdMessage)), /Unknown Fetch request ID/);
  lifecycle.register({ requestId: "duplicate", method: "PUT", pathname: preferencePath, requiredResponse: true });
  const attempt = lifecycle.beginSettlement("duplicate", "Fetch.fulfillRequest");
  lifecycle.settlementFailureDiagnostic(attempt, new Error("other"));
  assert.throws(() => lifecycle.beginSettlement("duplicate", "Fetch.fulfillRequest"), /Duplicate Fetch settlement/);
  const original = new Error("other CDP failure");
  assert.throws(() => lifecycle.handleSettlementError(attempt, original), (error) => error === original);
  lifecycle.close();
});
}
