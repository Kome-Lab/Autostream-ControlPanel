import assert from "node:assert/strict";
import { resolve } from "node:path";
import test from "node:test";
import { createNextReadinessFixture, observeNextStartup, assertNextReady, assertNextCloseRestores, assertNextFailed, assertNextRestored, assertNextFailureDiagnostic, assertNextListenersRemoved } from "./browser-next-readiness-fixture.mts";


export function registerNextReadinessCases() {


test("Next readiness accepts one HTTP response after 1 second within the original deadline", async () => {
  const fixture = createNextReadinessFixture({ probe: () => ({ afterMs: 1_500, status: 200 }) });
  const outcome = observeNextStartup(fixture.start());
  await fixture.clock.advance(1_001);
  assert.equal(outcome.state, "pending");
  assert.equal(fixture.probes.length, 1);
  assert.equal(fixture.probes[0].signal.aborted, false, "the in-flight HTTP request was cut off at one second");
  await fixture.clock.advance(499);
  const server = assertNextReady(fixture, outcome);
  assert.equal(fixture.clock.now, 1_500);
  assert.equal(fixture.probes.length, 1);
  assert.equal(fixture.maximumConcurrentProbes, 1);
  assert.deepEqual(fixture.preflightTimeouts, [1_000]);
  await assertNextCloseRestores(fixture, server);
});

test("Next readiness keeps the first successful response without a confirmation GET", async () => {
  const fixture = createNextReadinessFixture({ probe: (index) => ({ status: index === 0 ? 200 : 503 }) });
  const outcome = observeNextStartup(fixture.start());
  await fixture.clock.flush();
  const server = assertNextReady(fixture, outcome);
  assert.equal(fixture.probes.length, 1, "a successful probe must not be replaced by a later failure");
  await assertNextCloseRestores(fixture, server);
});

test("Next readiness retries connection refusal sequentially without respawning or resetting the deadline", async () => {
  const fixture = createNextReadinessFixture({
    probe: (index) => index === 0 ? { error: new Error("controlled connection refusal") } : { afterMs: 1_500, status: 200 },
  });
  const outcome = observeNextStartup(fixture.start());
  await fixture.clock.advance(1_600);
  const server = assertNextReady(fixture, outcome);
  assert.deepEqual(fixture.probes.map((probe) => probe.startedAt), [0, 100]);
  assert.equal(fixture.spawns.length, 1);
  assert.equal(fixture.maximumConcurrentProbes, 1);
  await assertNextCloseRestores(fixture, server);
});

for (const mode of ["never-response", "continuous-5xx"] as const) {
  test(`Next readiness ${mode} fails at the fixed deadline without a final probe`, async () => {
    const fixture = createNextReadinessFixture({ probe: () => mode === "never-response" ? {} : { status: 503 } });
    const outcome = observeNextStartup(fixture.start());
    await fixture.clock.advance(29_999);
    assert.equal(outcome.state, "pending");
    await fixture.clock.advance(1);
    const failure = assertNextFailed(fixture, outcome, "deadline");
    assert.match(failure.message, /Next server did not become ready/);
    assert.equal(fixture.clock.now, 30_000);
    assert.equal(fixture.probes.length, mode === "never-response" ? 1 : 300);
    assert.ok(fixture.probes.every((probe) => probe.startedAt < 30_000));
    assert.equal(fixture.maximumConcurrentProbes, 1);
    const count = fixture.probes.length;
    await fixture.clock.advance(30_000);
    assert.equal(fixture.probes.length, count);
    assert.equal(outcome.error, failure);
    assert.equal(outcome.settlements, 1);
  });
}

test("Next readiness rejects a late success when the deadline callback has not run yet", async () => {
  const fixture = createNextReadinessFixture();
  const outcome = observeNextStartup(fixture.start());
  await fixture.clock.flush();
  fixture.clock.elapseWithoutCallbacks(30_001);
  fixture.probes[0].respond(200);
  await fixture.clock.flush();
  assertNextFailed(fixture, outcome, "deadline");
  assert.equal(fixture.probes.length, 1);
  await fixture.clock.advance(0);
  assert.equal(outcome.settlements, 1);
});

test("Next readiness does not resettle when an aborted HTTP request resolves late", async () => {
  const fixture = createNextReadinessFixture({ probe: () => ({ afterMs: 30_001, status: 200, ignoreAbort: true }) });
  const outcome = observeNextStartup(fixture.start());
  await fixture.clock.advance(30_000);
  const failure = assertNextFailed(fixture, outcome, "deadline");
  assert.equal(fixture.probes[0].abortCount, 1);
  await fixture.clock.advance(1);
  assert.equal(outcome.error, failure);
  assert.equal(outcome.settlements, 1);
  assert.equal(fixture.probes.length, 1);
  assert.equal(fixture.clock.pendingCount, 0);
});

test("Next readiness requires HTTP despite Ready output and retains only the existing bounded output tail", async () => {
  const fixture = createNextReadinessFixture();
  const outcome = observeNextStartup(fixture.start());
  await fixture.clock.flush();
  fixture.child.stdout.emit("data", Buffer.from(`DISCARDED_OUTPUT_START${"x".repeat(8_200)}\nReady in 1ms\n`));
  fixture.child.stderr.emit("data", Buffer.from("CONTROLLED_PRIVATE_OUTPUT_END"));
  await fixture.clock.advance(30_000);
  const failure = assertNextFailed(fixture, outcome, "deadline");
  assert.match(failure.message, /Ready in 1ms/);
  assert.match(failure.message, /CONTROLLED_PRIVATE_OUTPUT_END/);
  assert.doesNotMatch(failure.message, /DISCARDED_OUTPUT_START/);
  assert.ok(failure.message.length <= 8_100);
  assert.equal(fixture.probes.length, 1);
});

for (const event of ["error", "exit", "signal"] as const) {
  test(`Next readiness rejects child ${event} during pending HTTP and restores owned files immediately`, async () => {
    const fixture = createNextReadinessFixture();
    const outcome = observeNextStartup(fixture.start());
    await fixture.clock.advance(250);
    const original = new Error("CONTROLLED_PRIVATE_CHILD_ERROR");
    if (event === "error") fixture.child.emit("error", original);
    else fixture.child.finish(event === "exit" ? 23 : null, event === "signal" ? "SIGTERM" : null);
    await fixture.clock.flush();
    const failure = assertNextFailed(fixture, outcome, `child_${event}`);
    if (event === "error") assert.equal(failure, original, "spawn error identity was replaced");
    assert.equal(fixture.clock.now, 250, "child failure waited for the readiness deadline");
    assert.equal(fixture.probes[0].abortCount, 1);
    assert.equal(fixture.child.kills.length, event === "error" ? 1 : 0);
    fixture.child.finish(0, null);
    await fixture.clock.advance(30_000);
    assert.equal(outcome.error, failure);
    assert.equal(outcome.settlements, 1);
    assert.equal(fixture.lines.length, 1);
  });
}

test("Next readiness preserves the original failure if startup diagnostics throw", async () => {
  const fixture = createNextReadinessFixture({ diagnosticError: new Error("controlled diagnostic sink failure") });
  const outcome = observeNextStartup(fixture.start());
  await fixture.clock.flush();
  const original = new Error("CONTROLLED_PRIVATE_ORIGINAL_ERROR");
  fixture.child.emit("error", original);
  await fixture.clock.flush();
  assert.equal(assertNextFailed(fixture, outcome, "child_error"), original);
  assert.equal(fixture.lines.length, 1);
});

test("Next readiness preserves startup and cleanup failures together and removes wait resources", async () => {
  const cleanupError = new Error("controlled termination failure");
  const original = new Error("controlled original startup failure");
  const fixture = createNextReadinessFixture({ killError: cleanupError });
  const outcome = observeNextStartup(fixture.start());
  await fixture.clock.flush();
  fixture.child.emit("error", original);
  await fixture.clock.flush();
  assert.equal(outcome.state, "rejected");
  assert.ok(outcome.error instanceof AggregateError);
  assert.equal(outcome.error.cause, original);
  assert.deepEqual(outcome.error.errors, [original, cleanupError]);
  assertNextRestored(fixture);
  assertNextFailureDiagnostic(fixture, "child_error");
  assert.equal(outcome.settlements, 1);
});

test("Next server close shares its cleanup failure without repeating termination or losing file restoration", async () => {
  const cleanupError = new Error("controlled close failure");
  const fixture = createNextReadinessFixture({ probe: () => ({ status: 200 }), killError: cleanupError });
  const outcome = observeNextStartup(fixture.start());
  await fixture.clock.flush();
  const server = assertNextReady(fixture, outcome);
  const first = server.close();
  assert.equal(server.close(), first);
  const closed = observeNextStartup(first);
  await fixture.clock.flush();
  assert.equal(closed.state, "rejected");
  assert.equal(closed.error, cleanupError);
  assert.equal(server.close(), first);
  assert.equal(fixture.child.kills.length, 1);
  assertNextRestored(fixture);
});

test("Next server close reports generated-file restoration failures", async () => {
  const restoreError = new Error("controlled generated-file restoration failure");
  const fixture = createNextReadinessFixture({ probe: () => ({ status: 200 }), restoreError });
  const outcome = observeNextStartup(fixture.start());
  await fixture.clock.flush();
  const server = assertNextReady(fixture, outcome);
  const closed = observeNextStartup(server.close());
  await fixture.clock.flush();
  assert.equal(closed.state, "rejected");
  assert.equal(closed.error, restoreError);
  assert.equal(fixture.clock.harnessTimerCount, 0);
  assertNextListenersRemoved(fixture);
});

test("Next readiness cleanup keeps the existing termination budgets and reports a child that never exits", async () => {
  const fixture = createNextReadinessFixture({ blockTermination: true });
  const outcome = observeNextStartup(fixture.start());
  await fixture.clock.advance(30_000);
  assert.equal(outcome.state, "pending", "the owned child cleanup is still pending");
  assert.deepEqual(fixture.child.kills, ["SIGTERM"]);
  await fixture.clock.advance(2_999);
  assert.deepEqual(fixture.child.kills, ["SIGTERM"]);
  await fixture.clock.advance(1);
  assert.deepEqual(fixture.child.kills, ["SIGTERM", "SIGKILL"]);
  await fixture.clock.advance(2_000);
  assert.equal(outcome.state, "rejected");
  assert.ok(outcome.error instanceof AggregateError);
  assert.ok(outcome.error.cause instanceof Error);
  assert.match(outcome.error.cause.message, /Next server did not become ready/);
  assert.equal(outcome.error.errors.length, 2);
  assertNextRestored(fixture);
  assertNextFailureDiagnostic(fixture, "deadline");
});

test("Next readiness reuses the existing server without creating, stopping, or changing files", async () => {
  const fixture = createNextReadinessFixture({ preflightStatus: 200 });
  const outcome = observeNextStartup(fixture.start());
  await fixture.clock.flush();
  assert.equal(outcome.state, "fulfilled");
  assert.ok(outcome.value);
  await outcome.value.close();
  assert.equal(fixture.spawns.length, 0);
  assert.equal(fixture.taskkills.length, 0);
  assert.equal(fixture.child.kills.length, 0);
  assert.equal(fixture.probes.length, 0);
  assertNextRestored(fixture);
});

test("Next readiness preserves the missing-binary failure without spawning or altering generated files", async () => {
  const fixture = createNextReadinessFixture({ missingNext: true });
  const outcome = observeNextStartup(fixture.start());
  await fixture.clock.flush();
  assert.equal(outcome.state, "rejected");
  assert.ok(outcome.error instanceof Error);
  assert.match(outcome.error.message, /Next binary is missing:/);
  assert.equal(fixture.spawns.length, 0);
  assert.equal(fixture.probes.length, 0);
  assert.equal(fixture.lines.length, 0, "preflight is outside spawned readiness diagnostics");
  assertNextRestored(fixture);
});

test("Next readiness retains HTTP status below 500 and the existing command, environment, and Windows ownership boundary", async () => {
  const fixture = createNextReadinessFixture({ platform: "win32", probe: () => ({ status: 404 }) });
  const outcome = observeNextStartup(fixture.start());
  await fixture.clock.flush();
  const server = assertNextReady(fixture, outcome);
  assert.deepEqual(fixture.spawns, [{
    command: "controlled-node",
    args: [resolve(fixture.webRoot, "node_modules", "next", "dist", "bin", "next"), "dev", "--hostname", "127.0.0.1", "--port", "3002"],
    options: { cwd: fixture.webRoot, env: { PATH: "CONTROLLED_PATH", NEXT_PUBLIC_AUTOSTREAM_DEMO: "false", NEXT_TELEMETRY_DISABLED: "1" }, stdio: "pipe", windowsHide: true },
  }]);
  const closed = observeNextStartup(server.close());
  await fixture.clock.flush();
  assert.equal(closed.state, "fulfilled");
  assert.deepEqual(fixture.taskkills, [{ command: "taskkill.exe", args: ["/PID", "9001", "/T", "/F"], options: { stdio: "ignore", windowsHide: true, timeout: 3_000 } }]);
  assert.deepEqual(fixture.child.kills, []);
  assertNextRestored(fixture);
});
}
