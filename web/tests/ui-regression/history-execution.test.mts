import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import { createHash } from "node:crypto";
import { mkdtempSync, readFileSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { resolve } from "node:path";
import test, { after } from "node:test";
import { pathToFileURL } from "node:url";
import {
  historicalAfter, historicalScenarioPath, historicalOwnershipEdits,
  prepareHistoricalExecution, transformHistoricalScenario, verifyHistoricalDerivative, compareHistoricalCaptures,
} from "../../../scripts/ci/ui-regression/historical-execution.mts";
import { historicalScenarioPlan, HistoricalScenarioOwners, assertHistoricalFreshDocument, closeHistoricalOwner } from "./history-scenario-owner.mts";
import { registerHistoricalSocketCases } from "./history-socket-regressions.mts";

const root = resolve(process.env.AUTOSTREAM_UI_HISTORY_ROOT || ".");
const temporary = mkdtempSync(resolve(tmpdir(), "autostream-history-execution-"));
const execution = prepareHistoricalExecution(root, resolve(temporary, "execution"));
const original = readFileSync(resolve(root, historicalScenarioPath));
const derivative = readFileSync(resolve(temporary, "execution", historicalScenarioPath));
const fixed = await import(pathToFileURL(resolve(root, "web/tests/helpers/bundle9-browser-contract.mts")).href);
after(() => rmSync(temporary, { recursive: true, force: false }));

test("history execution copy is reversible to the fixed original with only explicit ownership deltas", () => {
  assert.equal(verifyHistoricalDerivative(original, derivative), true);
  assert.equal(execution.proof.files.filter((row: { changed: boolean }) => row.changed).length, 1);
  for (const row of execution.proof.files) {
    if (!row.changed) assert.equal(row.originalSHA256, row.executionSHA256);
    assert.equal(createHash("sha256").update(readFileSync(resolve(root, row.path))).digest("hex"), row.originalSHA256);
  }
  assert.deepEqual(execFileSync("git", ["-C", root, "cat-file", "blob", `${historicalAfter}:${historicalScenarioPath}`]), original);
  assert.throws(() => transformHistoricalScenario(Buffer.concat([original, Buffer.from("\n")])), /fixed historical scenario raw hash/);
  assert.throws(() => verifyHistoricalDerivative(original, Buffer.from(derivative.toString().replace("assert.equal(browser.consoleErrorCount, 0", "assert.equal(browser.consoleErrorCount, 1"))), /all bytes outside ownership/);
  for (const edit of historicalOwnershipEdits()) {
    assert.throws(() => verifyHistoricalDerivative(original, Buffer.from(derivative.toString().replace(edit.to, edit.from))), /exact transform site|all bytes outside ownership/);
  }
  assert.throws(() => prepareHistoricalExecution(root, resolve(temporary, "execution")), /EEXIST/);
  const currentCaller = readFileSync(new URL("../../../scripts/ci/ui-regression/historical-comparison.mjs", import.meta.url), "utf8");
  assert.match(currentCaller, /prepareHistoricalExecution\(root, resolve\(output, "history-execution"\)\)/);
  assert.match(currentCaller, /await import\(execution.moduleURL\)/);
  assert.match(currentCaller, /captures\[side\] = await captureBundle9Source\(server.baseURL, resolve\(output, side\)\)/);
  assert.doesNotMatch(currentCaller, /await import\(pathToFileURL\(resolve\(root, "web\/tests\/helpers\/bundle9-browser-scenarios/);
});

test("history retains all 41 ordered purposes and every stateful operation sequence verbatim", () => {
  assert.equal(historicalScenarioPlan.length, 32);
  assert.deepEqual(historicalScenarioPlan.flatMap(row => [...row.captures]), fixed.bundle9ExpectedCaptureNames);
  // Separately compare the complete original bodies, not merely two sides of a
  // transformed run. Only the three viewport owner calls differ in this range.
  const body = (source: string) => source.slice(source.indexOf("    for (const surface of"), source.indexOf("    assert.equal(failures.length"));
  let restored = body(derivative.toString());
  for (const edit of historicalOwnershipEdits().filter((row: { id: string }) => row.id.endsWith("viewport-owner"))) restored = restored.replaceAll(edit.to, edit.from);
  assert.equal(createHash("sha256").update(body(original.toString())).digest("hex"), "8e0bf708211522e9962b9cf465dd9fb4bae58675c564f07b7899e86290b8c6d6", "independently fixed operation bodies");
  assert.equal(restored, body(original.toString()));
  const retained = ["const initialDocumentScript", "const observationExpression", "async function paintBarrier"];
  for (const marker of retained) {
    assert.ok(original.includes(marker));
    assert.equal(derivative.toString().slice(derivative.toString().indexOf(marker)), original.toString().slice(original.toString().indexOf(marker)));
  }
  assert.deepEqual(historicalScenarioPlan.find(row => row.name === "account-secret-owner")?.captures, ["account-security", "account-secret-concealed", "account-secret-disposed"]);
  assert.deepEqual(historicalScenarioPlan.find(row => row.name === "monitoring-error-and-recovery")?.captures, ["monitoring-error", "monitoring-recovered"]);
});

test("history comparison rejects missing, duplicate, reordered, API, DOM, browser and pixel differences", async t => {
  const make = () => ({ browserVersion: { product: "fixed-browser" }, captures: fixed.bundle9ExpectedCaptureNames.map((name: string) => ({ name, observation: { text: name, api: { total: 1, orderedMutations: [] } } })) });
  const pixel = () => ({ width: 1, height: 1, channels: 4, data: Buffer.from([0, 0, 0, 255]) });
  assert.equal((await compareHistoricalCaptures(make(), make(), fixed, async () => pixel())).length, 41);
  for (const mutation of ["missing", "duplicate", "reorder", "api", "dom", "version", "pixel", "dimensions"]) {
    await t.test(mutation, async () => {
      const left = make(), right = make();
      if (mutation === "missing") right.captures.pop();
      if (mutation === "duplicate") right.captures[1] = right.captures[0];
      if (mutation === "reorder") [right.captures[0], right.captures[1]] = [right.captures[1], right.captures[0]];
      if (mutation === "api") right.captures[0].observation.api.total = 0;
      if (mutation === "dom") right.captures[0].observation.text = "different";
      if (mutation === "version") right.browserVersion.product = "other";
      await assert.rejects(compareHistoricalCaptures(left, right, fixed, async (side: string) => {
        const data = pixel();
        if (side === "after" && mutation === "pixel") data.data[0] = 1;
        if (side === "after" && mutation === "dimensions") data.width = 2;
        return data;
      }));
    });
  }
});

// Small ownership fault injection is supplementary to the real fixed-class
// socket cases below. It specifically proves identity and cleanup error paths.
function ownedStub() {
  return {
    closed: 0, blank: true, fatal: undefined as unknown,
    async evaluate<T>() { return this.blank as T; },
    async setViewport() {}, async waitForRequestHandlersIdle() {},
    assertNoFatalError() { if (this.fatal) throw this.fatal; },
    async close() { this.closed += 1; },
  };
}

test("history ownership rejects unowned, reused, nonblank, overlapping and second-navigation targets", async () => {
  const first = ownedStub();
  await assert.rejects(assertHistoricalFreshDocument(first), /fresh and owned/);
  const owner = new HistoricalScenarioOwners(first, async () => first, async () => {});
  await owner.run("streams-1440", async browser => {
    await assertHistoricalFreshDocument(browser);
    await assert.rejects(assertHistoricalFreshDocument(browser), /fresh and owned/);
    await assert.rejects(owner.run("streams-390", async () => {}), /cannot overlap/);
  });
  assert.equal(first.closed, 1);
  await assert.rejects(owner.run("streams-390", async () => {}), /cannot be reused/);
  const nonblank = ownedStub(); nonblank.blank = false;
  const second = new HistoricalScenarioOwners(nonblank, async () => ownedStub(), async () => {});
  await assert.rejects(second.run("streams-1440", assertHistoricalFreshDocument), /untouched blank/);
  assert.equal(nonblank.closed, 1);
  await assert.rejects(second.run("streams-390", async () => {}), /cannot be retried/);
  assert.throws(() => second.finish(), /all historical owners/);
});

test("history cleanup cannot be hidden and preserves primary cause and one close attempt", async () => {
  for (const primary of [undefined, new Error("original operation failure")]) {
    const browser = ownedStub(), cleanup = new Error("owned cleanup failure");
    browser.close = async () => { browser.closed += 1; throw cleanup; };
    const owner = new HistoricalScenarioOwners(browser, async () => ownedStub(), async () => {});
    let failure: unknown;
    await assert.rejects(owner.run("streams-1440", async () => { if (primary) throw primary; }), error => {
      failure = error;
      if (!primary) return error === cleanup;
      assert.ok(error instanceof AggregateError);
      assert.equal(error.cause, primary);
      assert.deepEqual(error.errors, [primary, cleanup]);
      return true;
    });
    await assert.rejects(closeHistoricalOwner(owner, browser, { error: failure }), error => error === failure);
    assert.equal(browser.closed, 1);
    await assert.rejects(owner.run("streams-390", async () => {}), /cannot be retried/);
  }
  const browser = ownedStub(), setup = new Error("configure failed"), cleanup = new Error("close failed");
  browser.close = async () => { throw cleanup; };
  await assert.rejects(closeHistoricalOwner(undefined, browser, { error: setup }), error => error instanceof AggregateError && error.cause === setup && error.errors[1] === cleanup);
});

const turn = () => new Promise<void>(done => setImmediate(done));
function deferred() {
  let resolve!: () => void, reject!: (error: unknown) => void;
  const promise = new Promise<void>((yes, no) => { resolve = yes; reject = no; });
  return { promise, resolve, reject };
}
function observe(promise: Promise<void>) {
  const state = { settled: false, error: undefined as unknown, failed: false };
  const done = promise.then(() => { state.settled = true; }, error => { state.settled = true; state.failed = true; state.error = error; });
  return { state, done };
}

test("R028-OWNER-01 close pending excludes the following owner until cleanup completes", async () => {
  const gate = deferred(), first = ownedStub(), second = ownedStub(); let launches = 0, executed = 0;
  first.close = async () => { first.closed += 1; await gate.promise; };
  const owners = new HistoricalScenarioOwners(first, async () => { launches += 1; return second; }, async () => {});
  const running = observe(owners.run("streams-1440", async browser => { await assertHistoricalFreshDocument(browser); owners.recordCapture("streams-1440"); }));
  try {
    await turn(); assert.equal(first.closed, 1); assert.equal(running.state.settled, false);
    await assert.rejects(owners.run("streams-390", async () => { executed += 1; }), /cannot overlap/);
    assert.equal(launches, 0); assert.equal(executed, 0); assert.throws(() => owners.finish());
    gate.resolve(); await running.done; assert.equal(running.state.failed, false);
    await owners.run("streams-390", async browser => { executed += 1; await owners.setViewport(390, 844); await assertHistoricalFreshDocument(browser); owners.recordCapture("streams-390"); });
    assert.equal(launches, 1); assert.equal(executed, 1); assert.equal(second.closed, 1);
  } finally { gate.resolve(); await running.done; await owners.close(); }
});

test("R028-OWNER-02 pending cleanup rejection blocks successors and remains the same failed close", async () => {
  const gate = deferred(), first = ownedStub(), cleanup = new Error("owned cleanup rejected"); let launches = 0, executed = 0;
  first.close = async () => { first.closed += 1; await gate.promise; };
  const owners = new HistoricalScenarioOwners(first, async () => { launches += 1; return ownedStub(); }, async () => {});
  const running = observe(owners.run("streams-1440", async () => {}));
  try {
    await turn();
    await assert.rejects(owners.run("streams-390", async () => { executed += 1; }), /cannot overlap/);
    const closing = observe(owners.close()), again = observe(owners.close());
    await turn(); assert.equal(closing.state.settled, false); assert.equal(again.state.settled, false);
    gate.reject(cleanup); await Promise.all([running.done, closing.done, again.done]);
    for (const result of [running, closing, again]) { assert.equal(result.state.failed, true); assert.equal(result.state.error, cleanup); }
    await assert.rejects(owners.close(), error => error === cleanup);
    await assert.rejects(owners.run("streams-390", async () => { executed += 1; }), /cannot be retried/);
    assert.equal(launches, 0); assert.equal(executed, 0); assert.equal(first.closed, 1);
  } finally { gate.resolve(); await running.done; await owners.close().catch(() => {}); }
});

test("R028-OWNER-03 pending launch rejects a duplicate before any second launch", async () => {
  const gate = deferred(), first = ownedStub(), next = ownedStub(); let launches = 0, executed = 0;
  const owners = new HistoricalScenarioOwners(first, async () => { launches += 1; await gate.promise; return next; }, async () => {});
  await owners.run("streams-1440", async () => {});
  const running = observe(owners.run("streams-390", async () => { executed += 1; }));
  const duplicate = observe(owners.run("streams-390", async () => { executed += 1; }));
  try {
    await turn(); assert.equal(duplicate.state.settled, true); assert.equal(duplicate.state.failed, true);
    assert.match(String(duplicate.state.error), /cannot overlap/);
    assert.equal(launches, 1); assert.equal(executed, 0); assert.equal(next.closed, 0);
    gate.resolve(); await running.done; assert.equal(running.state.failed, false);
    assert.equal(executed, 1); assert.equal(next.closed, 1);
  } finally { gate.resolve(); await Promise.all([running.done, duplicate.done]); await owners.close(); }
});

test("R028-OWNER-04 launch rejection is terminal with the original cause and no adapter retry", async () => {
  const first = ownedStub(), next = ownedStub(), cause = new Error("launch rejected"); let launches = 0, executed = 0;
  const owners = new HistoricalScenarioOwners(first, async () => { launches += 1; if (launches === 1) throw cause; return next; }, async () => {});
  await owners.run("streams-1440", async () => {});
  await assert.rejects(owners.run("streams-390", async () => { executed += 1; }), error => error === cause);
  await assert.rejects(owners.run("streams-390", async () => { executed += 1; }), /cannot be retried/);
  await assert.rejects(owners.close(), error => error === cause);
  assert.equal(launches, 1); assert.equal(executed, 0); assert.equal(first.closed, 1); assert.equal(next.closed, 0);
});

test("R028-OWNER-05 shutdown owns late launch and cleanup without handing it to execute", async () => {
  const launch = deferred(), cleanup = deferred(), first = ownedStub(), next = ownedStub(); let launches = 0, configured = 0, executed = 0;
  next.close = async () => { next.closed += 1; await cleanup.promise; };
  const owners = new HistoricalScenarioOwners(first, async () => { launches += 1; await launch.promise; return next; }, async () => { configured += 1; });
  await owners.run("streams-1440", async () => {});
  const running = observe(owners.run("streams-390", async () => { executed += 1; }));
  await turn(); const closing = observe(owners.close()), again = observe(owners.close());
  try {
    await turn(); assert.equal(closing.state.settled, false); assert.equal(again.state.settled, false);
    launch.resolve(); await turn();
    assert.equal(next.closed, 1); assert.equal(configured, 0); assert.equal(executed, 0);
    assert.equal(closing.state.settled, false); assert.equal(running.state.settled, false);
    cleanup.resolve(); await Promise.all([running.done, closing.done, again.done]);
    assert.equal(running.state.failed, true); assert.match(String(running.state.error), /closed before execution/);
    assert.equal(closing.state.error, running.state.error); assert.equal(again.state.error, running.state.error);
    await assert.rejects(owners.run("streams-390", async () => { executed += 1; }), /cannot be retried|closed/);
    assert.equal(launches, 1); assert.equal(next.closed, 1); assert.equal(executed, 0);
  } finally { launch.resolve(); cleanup.resolve(); await Promise.all([running.done, closing.done, again.done]); }
});

test("R028-OWNER-CONTROL sequential32 owners retain41 purposes, unique targets and one completed cleanup", async () => {
  const browsers: ReturnType<typeof ownedStub>[] = [];
  const make = () => { const browser = ownedStub(); browsers.push(browser); return browser; };
  const owners = new HistoricalScenarioOwners(make(), async () => make(), async () => {});
  for (const plan of historicalScenarioPlan) await owners.run(plan.name, async browser => {
    await owners.setViewport(plan.width, plan.height); await assertHistoricalFreshDocument(browser);
    for (const name of plan.captures) owners.recordCapture(name);
  });
  const records = owners.finish(); await owners.close(); await owners.close();
  assert.equal(records.length, 32); assert.equal(records.flatMap(r => r.captures).length, 41);
  assert.ok(records.every(r => r.closed)); assert.ok(browsers.every(b => b.closed === 1));
});

test("R028 shutdown before first run awaits shared cleanup and permanently rejects execution", async () => {
  const gate = deferred(), first = ownedStub(); let launches = 0, executed = 0;
  first.close = async () => { first.closed += 1; await gate.promise; };
  const owners = new HistoricalScenarioOwners(first, async () => { launches += 1; return ownedStub(); }, async () => {});
  const closing = observe(owners.close()), again = observe(owners.close());
  try {
    await turn(); assert.equal(first.closed, 1); assert.equal(closing.state.settled, false); assert.equal(again.state.settled, false);
    await assert.rejects(owners.run("streams-1440", async () => { executed += 1; }), /closed/);
    gate.resolve(); await Promise.all([closing.done, again.done]);
    assert.equal(closing.state.failed, false); assert.equal(again.state.failed, false);
    await assert.rejects(owners.run("streams-1440", async () => { executed += 1; }), /closed/);
    await owners.close(); assert.equal(first.closed, 1); assert.equal(launches, 0); assert.equal(executed, 0);
  } finally { gate.resolve(); await Promise.all([closing.done, again.done]); }
});

test("R028 shutdown owns configure, viewport, body and settlement without premature cleanup", async t => {
  for (const phase of ["configure", "viewport", "body", "settlement"]) await t.test(phase, async () => {
    const gate = deferred(), entered = deferred(), first = ownedStub(), next = ownedStub(); let executed = 0;
    const pause = async () => { entered.resolve(); await gate.promise; };
    if (phase === "viewport") next.setViewport = pause;
    if (phase === "settlement") next.waitForRequestHandlersIdle = pause;
    const owners = new HistoricalScenarioOwners(first, async () => next, async () => { if (phase === "configure") await pause(); });
    await owners.run("streams-1440", async () => {});
    const running = observe(owners.run("streams-390", async () => { executed += 1; if (phase === "body") await pause(); }));
    await entered.promise; const closing = observe(owners.close());
    try {
      await turn(); assert.equal(closing.state.settled, false); assert.equal(next.closed, 0);
      await assert.rejects(owners.run("streams-390", async () => { executed += 1; }), /closed/);
      gate.resolve(); await Promise.all([running.done, closing.done]);
      const cancelledBeforeExecute = phase === "configure" || phase === "viewport";
      assert.equal(executed, cancelledBeforeExecute ? 0 : 1);
      assert.equal(running.state.failed, cancelledBeforeExecute); assert.equal(closing.state.failed, cancelledBeforeExecute);
      if (cancelledBeforeExecute) { assert.match(String(running.state.error), /closed before execution/); assert.equal(closing.state.error, running.state.error); }
      assert.equal(next.closed, 1);
    } finally { gate.resolve(); await Promise.all([running.done, closing.done]); }
  });
});

test("R028 shutdown preserves pending launch rejection and simultaneous body/cleanup identities", async t => {
  await t.test("late launch rejection", async () => {
    const gate = deferred(), cause = new Error("late launch rejection"); let launches = 0, executed = 0;
    const owners = new HistoricalScenarioOwners(ownedStub(), async () => { launches += 1; await gate.promise; return ownedStub(); }, async () => {});
    await owners.run("streams-1440", async () => {});
    const running = observe(owners.run("streams-390", async () => { executed += 1; })), closing = observe(owners.close());
    gate.reject(cause); await Promise.all([running.done, closing.done]);
    assert.equal(running.state.error, cause); assert.equal(closing.state.error, cause);
    await assert.rejects(owners.close(), error => error === cause);
    assert.equal(launches, 1); assert.equal(executed, 0);
  });
  await t.test("body and cleanup rejection", async () => {
    const gate = deferred(), entered = deferred(), browser = ownedStub(), primary = new Error("body rejected"), cleanup = new Error("cleanup rejected");
    browser.close = async () => { browser.closed += 1; throw cleanup; };
    const owners = new HistoricalScenarioOwners(browser, async () => ownedStub(), async () => {});
    const running = observe(owners.run("streams-1440", async () => { entered.resolve(); await gate.promise; throw primary; }));
    await entered.promise; const closing = observe(owners.close()); gate.resolve();
    await Promise.all([running.done, closing.done]); const failure = running.state.error;
    assert.ok(failure instanceof AggregateError); assert.equal(failure.cause, primary); assert.deepEqual(failure.errors, [primary, cleanup]);
    assert.equal(closing.state.error, failure);
    await assert.rejects(closeHistoricalOwner(owners, browser, { error: failure }), error => error === failure);
    assert.equal(browser.closed, 1);
  });
});

await registerHistoricalSocketCases(root, resolve(temporary, "execution"));
