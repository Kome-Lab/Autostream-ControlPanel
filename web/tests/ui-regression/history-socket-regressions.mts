import assert from "node:assert/strict";
import { existsSync, readFileSync } from "node:fs";
import { resolve } from "node:path";
import test from "node:test";
import { pathToFileURL } from "node:url";
import { HistoricalScenarioOwners, historicalScenarioPlan, nextHistoricalFixture } from "./history-scenario-owner.mts";
import type { createHarnessFixture as currentFixture } from "../helpers/browser-cdp-socket-fixture.mts";

type Fixture = ReturnType<typeof currentFixture>;
type Command = ReturnType<Fixture["socket"]["commandsFor"]>[number];
const baseURL = "http://history-fixture.invalid";
const turn = () => new Promise<void>(done => setImmediate(done));

// Uses the fixed fixture's real socket send/receive and fixed harness. Only the
// browser endpoint replies are controlled; no harness/controller method is replaced.
function answerEvaluation(fixture: Fixture, blank = true) {
  fixture.socket.hold("Runtime.evaluate");
  const send = fixture.socket.send.bind(fixture.socket);
  fixture.socket.send = raw => {
    send(raw);
    const command = JSON.parse(raw) as Command;
    if (command.method === "Runtime.evaluate") queueMicrotask(() => fixture.socket.respond(command, { result: { result: { value: command.params.expression === "location.href === 'about:blank'" ? blank : true } } }));
  };
}

export async function registerHistoricalSocketCases(root: string, execution: string) {
  const load = (folder: string, name: string) => import(pathToFileURL(resolve(folder, "web/tests/helpers", name)).href);
  const baseline = await load(root, "bundle9-browser-scenarios.mts");
  const changed = await load(execution, "bundle9-browser-scenarios.mts");
  const { createHarnessFixture } = await load(execution, "browser-cdp-socket-fixture.mts") as { createHarnessFixture: () => Fixture };
  const { createBundle9Fixture } = await load(execution, "bundle9-browser-fixtures.mts");
  const fixedLifecycle = await load(execution, "browser-request-lifecycle.mts");

  test("actual capture caller completes original first observations before closing and never hides a following startup failure", async t => {
    const { BrowserHarness: FixedHarness } = await load(execution, "browser-harness.mts");
    const f = createHarnessFixture();
    const stop = new Error("controlled next-owner startup failure");
    let launches = 0;
    t.mock.method(FixedHarness, "launch", async () => {
      launches += 1;
      if (launches === 1) return f.harness;
      assert.equal(f.socket.commandsFor("Browser.close").length, 1);
      assert.equal(existsSync(f.profile), false);
      throw stop;
    });
    const requests = ["/auth/me", "/settings/app", "/version", "/streams", "/youtube/outputs"];
    for (const method of ["Runtime.evaluate", "Page.getLayoutMetrics", "Page.captureScreenshot"]) f.socket.hold(method);
    const send = f.socket.send.bind(f.socket);
    f.socket.send = raw => {
      send(raw);
      const command = JSON.parse(raw) as Command;
      if (command.method === "Runtime.evaluate") {
        const expression = String(command.params.expression);
        const value = expression.includes("const markup = document.documentElement.outerHTML")
          ? { text: "controlled first historical capture", hiddenDiagnostic: false, secretLeak: false }
          : expression === "location.pathname + location.search + location.hash" ? "/admin/streams/?view=active#preview"
            : expression.includes("main')?.textContent") ? "B9 Browser Stream" : true;
        queueMicrotask(() => f.socket.respond(command, { result: { result: { value } } }));
      }
      if (command.method === "Page.getLayoutMetrics") queueMicrotask(() => f.socket.respond(command, { result: { cssContentSize: { width: 1, height: 1 } } }));
      if (command.method === "Page.captureScreenshot") queueMicrotask(() => f.socket.respond(command, { result: { data: Buffer.from("controlled capture bytes, not native image evidence").toString("base64") } }));
      if (command.method === "Page.navigate") queueMicrotask(() => {
        for (const [index, path] of requests.entries()) f.socket.emitEvent("Fetch.requestPaused", { requestId: `required-${index}`, request: { method: "GET", url: baseURL + path } });
      });
    };
    const output = resolve(execution, "controlled-capture");
    f.socket.hold("Browser.close");
    const capture = changed.captureBundle9Source(baseURL, output);
    const failed = assert.rejects(capture, error => error === stop);
    try {
      const closing = await f.socket.waitForCommand("Browser.close");
      await turn();
      assert.equal(launches, 1, "actual caller must wait for the previous owner cleanup");
      assert.equal(f.socket.commandsFor("Page.captureScreenshot").length, 1);
      assert.equal(f.socket.commandsFor("Fetch.fulfillRequest").length, 5);
      assert.equal(existsSync(f.profile), true, "cleanup is pending, not complete");
      f.socket.respond(closing, { result: {} });
      await failed;
      const result = JSON.parse(readFileSync(resolve(output, "execution.json"), "utf8"));
      assert.equal(result.status, "fail"); assert.equal(result.captures, 1);
      assert.equal(launches, 2);
      const observation = JSON.parse(readFileSync(resolve(output, "streams-1440.json"), "utf8"));
      assert.equal(observation.api.total, 5);
      assert.equal(f.socket.commandsFor("Page.captureScreenshot").length, 1);
      assert.equal(f.socket.commandsFor("Fetch.fulfillRequest").length, 5);
      assert.equal(f.socket.commandsFor("Page.navigate").length, 1);
      assert.equal(f.socket.commandsFor("Browser.close").length, 1);
    } finally {
      for (const command of f.socket.commandsFor("Browser.close")) f.socket.respond(command, { result: {} });
      await failed; t.mock.restoreAll(); await f.harness.close();
    }
  });

  test("fixed original helper reproduces generation25 Script GET fatal after to-blank navigation response", async () => {
    const f = createHarnessFixture(); answerEvaluation(f);
    const fixture = createBundle9Fixture(baseURL); f.harness.setRouteResolver(fixture.resolver);
    try {
      for (let index = 0; index < 12; index += 1) {
        await f.harness.navigate("about:blank");
        await f.harness.navigate(`${baseURL}/admin/streams/`);
      }
      f.socket.hold("Page.navigate"); f.socket.hold("Fetch.continueRequest"); f.socket.autoLoadEvent = false;
      let failure: unknown;
      const navigating = baseline.navigateBundle9Document(f.harness, fixture, `${baseURL}/admin/incidents/`).catch((error: unknown) => { failure = error; });
      await turn();
      const navigation = f.socket.commandsFor("Page.navigate").at(-1)!;
      assert.equal(navigation.params.url, "about:blank");
      f.socket.respond(navigation, { result: {} });
      f.socket.emitEvent("Fetch.requestPaused", { requestId: "late-script", resourceType: "Script", frameId: "main", request: { method: "GET", url: `${baseURL}/_next/static/late.js` } });
      const settlement = await f.socket.waitForCommand("Fetch.continueRequest");
      f.socket.respond(settlement, { error: { message: "Invalid InterceptionId." } });
      await navigating;
      assert.ok(failure instanceof Error); assert.equal(failure.message, "Invalid InterceptionId.");
      assert.throws(() => f.harness.assertNoFatalError(), error => error === failure);
      assert.equal(f.harness.safeFetchCancellationCount, 0);
      assert.equal(f.socket.commandsFor("Fetch.continueRequest").length, 1);
      const diagnostic = JSON.parse(f.harness.fetchFailureDiagnosticJSON!);
      assert.equal(diagnostic.request_generation, 25); assert.equal(diagnostic.current_generation, 25);
      assert.equal(diagnostic.cancellation_context_present, false);
      assert.equal(diagnostic.generation_diagnostic.resource.network_id_present, false);
      assert.equal(diagnostic.generation_diagnostic.paused.phase, "to-blank");
      assert.ok(diagnostic.generation_diagnostic.paused.order < diagnostic.generation_diagnostic.settlement.order);
      assert.ok(diagnostic.generation_diagnostic.settlement.order < diagnostic.generation_diagnostic.failure.order);
      assert.match(f.harness.fetchFailureDiagnosticJSON!, /"page_navigate_response_received":true/);
      assert.equal(f.socket.commandsFor("Page.navigate").filter(command => command.params.url === `${baseURL}/admin/incidents/`).length, 0);
    } finally { await f.harness.close(); assert.equal(existsSync(f.profile), false); }
  });

  test("actual transformed helper gives all32 owners one fresh target and preserves41 capture groups, fixture state and viewport", async () => {
    const fixtures: Fixture[] = [];
    let fixture = createBundle9Fixture(baseURL);
    const launch = async () => { const f = createHarnessFixture(); answerEvaluation(f); f.harness.setRouteResolver(fixture.resolver); fixtures.push(f); return f.harness; };
    const first = await launch();
    const owners = new HistoricalScenarioOwners(first, launch, async browser => {
      fixture = nextHistoricalFixture(fixture, () => createBundle9Fixture(baseURL));
      browser.setRouteResolver(fixture.resolver);
    });
    try {
      for (const plan of historicalScenarioPlan) {
        await owners.run(plan.name, async browser => {
          await owners.setViewport(plan.width, plan.height);
          await changed.navigateBundle9Document(browser, fixture, `${baseURL}/admin/streams/`);
          const f = fixtures.at(-1)!;
          assert.equal(browser, f.harness);
          assert.deepEqual(f.socket.commandsFor("Page.navigate").map(command => command.params.url), [`${baseURL}/admin/streams/`]);
          assert.deepEqual(f.socket.commandsFor("Emulation.setDeviceMetricsOverride").at(-1)?.params.width, plan.width);
          // Server state is deliberately retained across fresh fixture owners;
          // the original helper alone resets per-document request evidence.
          if (plan.name === "account-secret-owner") fixture.state.mfaPending = true;
          if (plan.name === "archive-local") assert.equal(fixture.state.mfaPending, true);
          for (const capture of plan.captures) {
            assert.equal(f.socket.commandsFor("Browser.close").length, 0);
            owners.recordCapture(capture);
          }
        });
        assert.equal(fixtures.at(-1)!.socket.commandsFor("Browser.close").length, 1);
        assert.equal(existsSync(fixtures.at(-1)!.profile), false);
      }
      assert.equal(owners.finish().length, 32);
      assert.equal(new Set(fixtures.map(f => f.harness)).size, 32);
      assert.equal(fixtures.reduce((count, f) => count + f.harness.safeFetchCancellationCount, 0), 0);
    } finally { await owners.close(); }
  });

  test("real fixed fixture retains state but old late requests cannot change the next owner evidence", () => {
    const old = createBundle9Fixture(baseURL);
    old.state.permissions = ["streams.read"]; old.state.healthError = true;
    old.state.readinessError = true; old.state.mfaPending = true;
    old.resolver({ method: "GET", url: baseURL + "/auth/me" });
    const next = nextHistoricalFixture(old, () => createBundle9Fixture(baseURL));
    assert.deepEqual(next.state, old.state); assert.notEqual(next.state, old.state);
    assert.deepEqual(next.trace, []); assert.deepEqual(next.unexpected, []);
    old.resolver({ method: "GET", url: baseURL + "/unknown-late" });
    old.state.permissions.push("unrelated-old-owner"); old.state.mfaPending = false;
    assert.deepEqual(next.trace, []); assert.deepEqual(next.unexpected, []);
    assert.deepEqual(next.state.permissions, ["streams.read"]); assert.equal(next.state.mfaPending, true);
    assert.throws(() => nextHistoricalFixture(old, () => old), /must be new/);
    assert.throws(() => nextHistoricalFixture(old, () => ({ ...old, state: old.state })), /must not alias/);
    next.resolver({ method: "GET", url: baseURL + "/auth/me" });
    assert.equal(next.trace.length, 1); next.resetTrace(); assert.equal(next.trace.length, 0);
    assert.deepEqual(next.state.permissions, ["streams.read"]);
  });

  test("owner waits for actual required handler settlement before close and preserves the response count", async () => {
    const f = createHarnessFixture(); answerEvaluation(f);
    const fixture = createBundle9Fixture(baseURL);
    let release!: () => void;
    const gate = new Promise<void>(done => { release = done; });
    f.harness.setRouteResolver(request => ({ ...fixture.resolver(request), waitUntil: gate }));
    const owners = new HistoricalScenarioOwners(f.harness, async () => { throw new Error("unexpected next owner"); }, async () => {});
    const execution = owners.run("streams-1440", async browser => {
      await changed.navigateBundle9Document(browser, fixture, `${baseURL}/admin/streams/`);
      f.socket.emitEvent("Fetch.requestPaused", { requestId: "required-delayed", request: { method: "GET", url: baseURL + "/auth/me" } });
    });
    try {
      await turn();
      assert.equal(f.harness.requests.get("/auth/me"), 1);
      assert.equal(f.harness.responses.get("/auth/me") || 0, 0);
      assert.equal(f.socket.commandsFor("Browser.close").length, 0);
      release(); await execution;
      assert.equal(f.harness.responses.get("/auth/me"), 1);
      assert.deepEqual(f.harness.responseStatuses.get("/auth/me"), [200]);
      assert.equal(f.socket.commandsFor("Browser.close").length, 1);
      assert.equal(existsSync(f.profile), false);
    } finally { release(); await owners.close(); }
  });

  test("separated owner still fails current Script, required GET, unknown API, POST, and nonexact errors without retry", async t => {
    for (const [path, method, errorText] of [
      ["/_next/static/late.js", "GET", "Invalid InterceptionId."],
      ["/auth/me", "GET", "Invalid InterceptionId."],
      ["/unknown-api", "GET", "Invalid InterceptionId."],
      ["/streams/stream-1/restart", "POST", "Invalid InterceptionId."],
      ["/_next/static/late.js", "GET", "Target closed"],
    ]) await t.test(`${method} ${path} ${errorText}`, async () => {
      const f = createHarnessFixture(); answerEvaluation(f);
      const fixture = createBundle9Fixture(baseURL); f.harness.setRouteResolver(fixture.resolver);
      f.socket.hold("Fetch.continueRequest"); f.socket.hold("Fetch.fulfillRequest");
      const owners = new HistoricalScenarioOwners(f.harness, async () => { throw new Error("unexpected next owner"); }, async () => {});
      let originalCause: unknown;
      await assert.rejects(owners.run("streams-1440", async browser => {
        await changed.navigateBundle9Document(browser, fixture, `${baseURL}/admin/streams/`);
        f.socket.emitEvent("Fetch.requestPaused", { requestId: "invalid", resourceType: "Script", request: { method, url: baseURL + path } });
        const settlementMethod = path.startsWith("/_next/") ? "Fetch.continueRequest" : "Fetch.fulfillRequest";
        const command = await f.socket.waitForCommand(settlementMethod);
        f.socket.respond(command, { error: { message: errorText } });
        await turn();
        try { browser.assertNoFatalError(); } catch (error) { originalCause = error; throw error; }
      }), error => error === originalCause && error instanceof Error && error.message === errorText);
      assert.equal(f.harness.safeFetchCancellationCount, 0);
      assert.equal(f.socket.commandsFor("Fetch.continueRequest").length + f.socket.commandsFor("Fetch.fulfillRequest").length, 1);
      assert.equal(existsSync(f.profile), false);
      assert.equal(f.socket.commandsFor("Page.navigate").length, 1);
      await assert.rejects(owners.run("streams-390", async () => {}), /cannot be retried/);
    });
  });

  test("fixed lifecycle unknown and duplicate settlement stay fatal and registered stale contract stays unchanged", () => {
    const lifecycle = new fixedLifecycle.FetchRequestLifecycle();
    assert.throws(() => lifecycle.beginSettlement("missing", "Fetch.continueRequest"), /Unknown Fetch/);
    lifecycle.register({ requestId: "duplicate", method: "GET", pathname: "/static.js", requiredResponse: false });
    lifecycle.beginSettlement("duplicate", "Fetch.continueRequest");
    assert.throws(() => lifecycle.beginSettlement("duplicate", "Fetch.continueRequest"), /Duplicate Fetch/);
    const stale = new fixedLifecycle.FetchRequestLifecycle();
    stale.register({ requestId: "known", method: "GET", pathname: "/static.js", requiredResponse: false });
    const attempt = stale.beginSettlement("known", "Fetch.continueRequest"); stale.beginNavigation("navigate");
    assert.deepEqual(stale.handleSettlementError(attempt, new Error("Invalid InterceptionId.")), { cancelled: true });
    assert.equal(stale.safeCancellationCount, 1);
    lifecycle.close(); stale.close();
  });
}
