import assert from "node:assert/strict";
import test from "node:test";
import { OneTimeSecretFakeClock } from "./helpers/ui-foundation-secret-fake-clock.mts";
import { loadOwner, clearedSnapshot, marker, activeSnapshot, terminalSnapshot, replacementMarker, assertReplacementOrdering, assertStaleTimerFence, assertLateCopyFence, assertSessionLossCleanup, loadOwnerMutant, deferred, runtimeReplace } from "./secret-lifecycle-fixture.mts";


export function registerSecretLifecycleCases() {


test("owner starts source-free and enforces reveal, conceal, warning, and exact expiry", async () => {
  const { createOneTimeSecretLifecycleOwner } = await loadOwner();
  const clock = new OneTimeSecretFakeClock();
  const owner = createOneTimeSecretLifecycleOwner<string>(clock);
  assert.deepEqual(owner.getSnapshot(), clearedSnapshot());
  assert.equal(Object.isFrozen(owner.getSnapshot()), true);
  assert.equal(owner.readRevealedValue(), undefined);
  assert.equal(clock.pendingTimerCount(), 0);

  assert.equal(owner.replace({ value: marker }), true);
  assert.deepEqual(owner.getSnapshot(), activeSnapshot(1, "concealed"));
  assert.equal(owner.readRevealedValue(), undefined);
  assert.equal(clock.pendingTimerCount(), 2);
  assert.deepEqual(clock.pendingDeadlines(), [550_000, 610_000]);
  assert.equal(owner.reveal(), true);
  assert.equal(owner.readRevealedValue(), marker);
  assert.equal(owner.conceal(), true);
  assert.equal(owner.readRevealedValue(), undefined);
  assert.equal(owner.reveal(), true);

  clock.advanceEpochBy(9_000_000);
  assert.equal(owner.getSnapshot().warningActive, false, "wall clock jumps do not affect the active deadline");
  clock.advanceMonotonicBy(539_999);
  assert.equal(owner.getSnapshot().warningActive, false);
  assert.equal(owner.readRevealedValue(), marker);
  clock.advanceMonotonicBy(1);
  assert.equal(owner.getSnapshot().warningActive, true);
  assert.equal(owner.readRevealedValue(), marker);
  clock.advanceMonotonicBy(59_999);
  assert.equal(owner.readRevealedValue(), marker);
  clock.advanceMonotonicBy(1);
  assert.deepEqual(owner.getSnapshot(), terminalSnapshot(1, "cleared", "expired"));
  assert.equal(owner.readRevealedValue(), undefined);
  assert.equal(clock.pendingTimerCount(), 0);
});

test("short backend lifetimes warn immediately and always win before the hard maximum", async () => {
  const { createOneTimeSecretLifecycleOwner } = await loadOwner();
  const clock = new OneTimeSecretFakeClock(1_000_000, 20_000);
  const owner = createOneTimeSecretLifecycleOwner<string>(clock);
  assert.equal(owner.replace({
    value: marker,
    backendExpiresAtEpochMs: 1_030_000,
    initialVisibility: "revealed",
  }), true);
  assert.deepEqual(owner.getSnapshot(), { ...activeSnapshot(1, "revealed"), warningActive: true });
  assert.equal(clock.pendingTimerCount(), 1);
  assert.deepEqual(clock.pendingDeadlines(), [50_000]);
  clock.advanceMonotonicBy(29_999);
  assert.equal(owner.readRevealedValue(), marker);
  clock.advanceMonotonicBy(1);
  assert.equal(owner.readRevealedValue(), undefined);
  assert.equal(owner.getSnapshot().clearReason, "expired");
});

test("replacement publishes old cleared state before new source availability and fences old timers", async () => {
  const { createOneTimeSecretLifecycleOwner } = await loadOwner();
  const clock = new OneTimeSecretFakeClock();
  const owner = createOneTimeSecretLifecycleOwner<string>(clock);
  assert.equal(owner.replace({ value: marker, initialVisibility: "revealed" }), true);
  const events: Array<Readonly<{
    generation: number;
    phase: string;
    reason?: string;
    available: boolean;
  }>> = [];
  owner.subscribe(() => {
    const snapshot = owner.getSnapshot();
    events.push(Object.freeze({
      generation: snapshot.generation,
      phase: snapshot.phase,
      ...(snapshot.clearReason ? { reason: snapshot.clearReason } : {}),
      available: owner.readRevealedValue() !== undefined,
    }));
  });

  clock.advanceMonotonicBy(100_000);
  assert.equal(owner.replace({ value: replacementMarker, initialVisibility: "revealed" }), true);
  assert.deepEqual(events, [
    { generation: 1, phase: "cleared", reason: "replaced", available: false },
    { generation: 2, phase: "revealed", available: true },
  ]);
  assert.equal(owner.readRevealedValue(), replacementMarker);
  assert.deepEqual(clock.pendingDeadlines(), [650_000, 710_000]);
  clock.advanceMonotonicBy(500_000);
  assert.equal(owner.readRevealedValue(), replacementMarker, "the canceled generation-one expiry cannot clear generation two");
});

test("stale callbacks from a non-cooperative scheduler cannot disarm the newer generation", async () => {
  const { createOneTimeSecretLifecycleOwner } = await loadOwner();
  const clock = new OneTimeSecretFakeClock();
  const owner = createOneTimeSecretLifecycleOwner<string>({
    epochNowMs: clock.epochNowMs,
    monotonicNowMs: clock.monotonicNowMs,
    schedule: clock.schedule,
    cancel: () => {},
  });
  assert.equal(owner.replace({ value: marker, initialVisibility: "revealed" }), true);
  clock.advanceMonotonicBy(100_000);
  assert.equal(owner.replace({ value: replacementMarker, initialVisibility: "revealed" }), true);
  assert.equal(clock.pendingTimerCount(), 4, "the runtime deliberately ignores cancellation");

  clock.advanceMonotonicBy(440_000);
  assert.equal(owner.getSnapshot().warningActive, false, "the stale warning cannot mark the new generation");
  clock.advanceMonotonicBy(60_000);
  assert.equal(owner.readRevealedValue(), replacementMarker, "the stale expiry cannot clear the new generation");
  clock.advanceMonotonicBy(40_000);
  assert.equal(owner.getSnapshot().warningActive, true, "the new warning callback remains armed");
  clock.advanceMonotonicBy(60_000);
  assert.equal(owner.getSnapshot().clearReason, "expired", "the new expiry callback remains armed");
  assert.equal(owner.readRevealedValue(), undefined);
  assert.equal(clock.pendingTimerCount(), 0);
});

test("owner behavior oracles reject executable ordering, timer, copy, and session mutants", async () => {
  const canonical = await loadOwner();
  assert.doesNotThrow(() => assertReplacementOrdering(canonical.createOneTimeSecretLifecycleOwner));
  assert.doesNotThrow(() => assertStaleTimerFence(canonical.createOneTimeSecretLifecycleOwner));
  await assert.doesNotReject(() => assertLateCopyFence(canonical.createOneTimeSecretLifecycleOwner));
  assert.doesNotThrow(() => assertSessionLossCleanup(canonical.createOneTimeSecretLifecycleOwner));

  const replacementMutant = await loadOwnerMutant((source) => source.replace(
    'if (active) clearActive("replaced");',
    "if (active) cancelAllTimers();",
  ));
  assert.throws(
    () => assertReplacementOrdering(replacementMutant.createOneTimeSecretLifecycleOwner),
    undefined,
    "new adoption before old terminal publication",
  );

  const timerMutant = await loadOwnerMutant((source) => source
    .replaceAll(
      "if (!scheduled || scheduled.generation !== generation) return;",
      "if (!scheduled) return;",
    )
    .replaceAll("if (active?.generation !== generation) return;", ""));
  assert.throws(
    () => assertStaleTimerFence(timerMutant.createOneTimeSecretLifecycleOwner),
    undefined,
    "old timer clears a newer generation",
  );

  const copyMutant = await loadOwnerMutant((source) => source.replace(
    "      || active.generation !== generation",
    "      || false",
  ));
  await assert.rejects(
    () => assertLateCopyFence(copyMutant.createOneTimeSecretLifecycleOwner),
    undefined,
    "late copy marks the replacement copied",
  );

  const sessionMutant = await loadOwnerMutant((source) => source.replace(
    'clearForSessionLoss: () => terminal("session-lost"),',
    "clearForSessionLoss: () => false,",
  ));
  assert.throws(
    () => assertSessionLossCleanup(sessionMutant.createOneTimeSecretLifecycleOwner),
    undefined,
    "session loss leaves the source active",
  );
});

test("copy is explicit, safe on failure, and late completions cannot mutate a newer generation", async () => {
  const { createOneTimeSecretLifecycleOwner } = await loadOwner();
  const clock = new OneTimeSecretFakeClock();
  const owner = createOneTimeSecretLifecycleOwner<string>(clock);
  let writerCalls = 0;
  assert.equal(await owner.copyWith(() => { writerCalls += 1; }), "unavailable");
  assert.equal(writerCalls, 0);
  owner.replace({ value: marker });
  assert.equal(await owner.copyWith(() => { writerCalls += 1; }), "unavailable");
  assert.equal(writerCalls, 0);
  owner.reveal();
  assert.equal(await owner.copyWith((value) => {
    writerCalls += 1;
    assert.equal(value, marker);
  }), "copied");
  assert.equal(writerCalls, 1);
  assert.deepEqual(owner.getSnapshot(), activeSnapshot(1, "copied", "copied"));
  assert.equal(await owner.copyWith((value) => {
    writerCalls += 1;
    assert.equal(value, marker);
  }), "copied");
  assert.equal(writerCalls, 2);
  assert.deepEqual(owner.getSnapshot(), activeSnapshot(1, "copied", "copied"));

  const originalConsole = console.error;
  let consoleMarkerCalls = 0;
  console.error = (...values: readonly unknown[]) => {
    if (values.some((value) => String(value).includes(marker))) consoleMarkerCalls += 1;
  };
  try {
    assert.equal(await owner.copyWith(() => { throw new Error(marker); }), "failed");
  } finally {
    console.error = originalConsole;
  }
  assert.equal(consoleMarkerCalls, 0);
  assert.deepEqual(owner.getSnapshot(), activeSnapshot(1, "revealed", "failed"));
  assert.equal(clock.pendingTimerCount(), 2, "copy failure preserves lifecycle timers");

  const pending = deferred<void>();
  const oldCopy = owner.copyWith(() => pending.promise);
  owner.replace({ value: replacementMarker, initialVisibility: "revealed" });
  pending.resolve();
  assert.equal(await oldCopy, "stale-generation");
  assert.equal(owner.getSnapshot().generation, 2);
  assert.equal(owner.getSnapshot().phase, "revealed");
  assert.equal(owner.readRevealedValue(), replacementMarker);
});

test("pending copy cannot survive expiry or session loss, and failure still expires", async () => {
  const { createOneTimeSecretLifecycleOwner } = await loadOwner();

  const expiryClock = new OneTimeSecretFakeClock(1_000_000, 0);
  const expiryOwner = createOneTimeSecretLifecycleOwner<string>(expiryClock);
  expiryOwner.replace({ value: marker, backendExpiresAtEpochMs: 1_030_000, initialVisibility: "revealed" });
  const expiryDeferred = deferred<void>();
  const expiryCopy = expiryOwner.copyWith(() => expiryDeferred.promise);
  expiryClock.advanceMonotonicBy(30_000);
  expiryDeferred.resolve();
  assert.equal(await expiryCopy, "stale-generation");
  assert.equal(expiryOwner.readRevealedValue(), undefined);
  assert.equal(expiryOwner.getSnapshot().clearReason, "expired");

  const sessionClock = new OneTimeSecretFakeClock();
  const sessionOwner = createOneTimeSecretLifecycleOwner<string>(sessionClock);
  sessionOwner.replace({ value: marker, initialVisibility: "revealed" });
  const sessionDeferred = deferred<void>();
  const sessionCopy = sessionOwner.copyWith(() => sessionDeferred.promise);
  assert.equal(sessionOwner.clearForSessionLoss(), true);
  sessionDeferred.resolve();
  assert.equal(await sessionCopy, "stale-generation");
  assert.equal(sessionOwner.getSnapshot().clearReason, "session-lost");

  const failureClock = new OneTimeSecretFakeClock(2_000_000, 0);
  const failureOwner = createOneTimeSecretLifecycleOwner<string>(failureClock);
  failureOwner.replace({ value: marker, backendExpiresAtEpochMs: 2_120_000, initialVisibility: "revealed" });
  assert.equal(await failureOwner.copyWith(() => Promise.reject(new Error(marker))), "failed");
  failureClock.advanceMonotonicBy(120_000);
  assert.equal(failureOwner.getSnapshot().clearReason, "expired");
  assert.equal(failureClock.pendingTimerCount(), 0);
});

test("every terminal trigger clears synchronously, cancels timers, and is idempotent", async () => {
  const { createOneTimeSecretLifecycleOwner } = await loadOwner();
  const cases = [
    ["acknowledge", "acknowledged", "acknowledged", (owner: ReturnType<typeof createOneTimeSecretLifecycleOwner<string>>) => owner.acknowledge()],
    ["dismiss", "cleared", "dismissed", (owner: ReturnType<typeof createOneTimeSecretLifecycleOwner<string>>) => owner.dismiss()],
    ["navigation", "cleared", "navigation", (owner: ReturnType<typeof createOneTimeSecretLifecycleOwner<string>>) => owner.clearForNavigation()],
    ["session", "cleared", "session-lost", (owner: ReturnType<typeof createOneTimeSecretLifecycleOwner<string>>) => owner.clearForSessionLoss()],
    ["unmount", "cleared", "unmounted", (owner: ReturnType<typeof createOneTimeSecretLifecycleOwner<string>>) => owner.dispose()],
  ] as const;
  for (const [label, phase, reason, clear] of cases) {
    const clock = new OneTimeSecretFakeClock();
    const owner = createOneTimeSecretLifecycleOwner<string>(clock);
    owner.replace({ value: marker, initialVisibility: "revealed" });
    let notifications = 0;
    let listenerArgumentCount = -1;
    owner.subscribe((...args: readonly unknown[]) => {
      notifications += 1;
      listenerArgumentCount = args.length;
    });
    assert.equal(clear(owner), true, label);
    assert.equal(owner.readRevealedValue(), undefined, label);
    assert.equal(clock.pendingTimerCount(), 0, label);
    assert.deepEqual(owner.getSnapshot(), terminalSnapshot(1, phase, reason), label);
    assert.equal(listenerArgumentCount, 0, label);
    assert.equal(notifications, 1, label);
    assert.equal(clear(owner), false, `${label} repeated`);
    assert.equal(notifications, 1, `${label} repeated notification`);
  }
});

test("invalid adoption clears an active source and hostile bundles never throw or log", async () => {
  const { createOneTimeSecretLifecycleOwner } = await loadOwner();
  const clock = new OneTimeSecretFakeClock();
  const owner = createOneTimeSecretLifecycleOwner<string>(clock);
  owner.replace({ value: marker, initialVisibility: "revealed" });
  const hostile = new Proxy({ value: replacementMarker }, { get: () => { throw new Error(marker); } });
  let result: unknown;
  assert.doesNotThrow(() => { result = runtimeReplace(owner.replace, hostile); });
  assert.equal(result, false);
  assert.equal(owner.readRevealedValue(), undefined);
  assert.equal(owner.getSnapshot().clearReason, "invalid");
  assert.equal(clock.pendingTimerCount(), 0);

  assert.equal(runtimeReplace(owner.replace, {
    value: replacementMarker,
    backendExpiresAtEpochMs: -1,
  }), false);
  assert.equal(owner.getSnapshot().clearReason, "invalid");
});
}
