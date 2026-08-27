import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { register } from "node:module";
import { join, resolve } from "node:path";
import test from "node:test";
import { fileURLToPath } from "node:url";

import { createElement, type ReactElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import ts from "typescript";

import type { OneTimeSecretSnapshot } from "../src/lib/foundation/secrets/contracts.ts";
import type { OneTimeSecretRevealProps } from "../src/components/foundation/secrets/one-time-secret-reveal.ts";
import type { TranslationKey } from "../src/lib/i18n.ts";
import { OneTimeSecretFakeClock } from "./helpers/ui-foundation-secret-fake-clock.mts";
import { assertSecretFoundationBoundaries } from "./helpers/ui-foundation-secret-imports.mts";

const resolverSource = [
  "let webRootURL;",
  "let typescriptURL;",
  "export function initialize(data) { webRootURL = data.webRootURL; typescriptURL = data.typescriptURL; }",
  "export async function resolve(specifier, context, nextResolve) {",
  "  if (specifier.startsWith('@/')) {",
  "    const target = new URL('src/' + specifier.slice(2), webRootURL);",
  "    if (/\\.[cm]?[jt]sx?$/.test(target.pathname)) return nextResolve(target.href, context);",
  "    const typeScriptTarget = new URL(target);",
  "    typeScriptTarget.pathname += '.ts';",
  "    try { return await nextResolve(typeScriptTarget.href, context); } catch {",
  "      target.pathname += '.tsx';",
  "      return nextResolve(target.href, context);",
  "    }",
  "  }",
  "  return nextResolve(specifier, context);",
  "}",
  "export async function load(url, context, nextLoad) {",
  "  if (!url.endsWith('.tsx')) return nextLoad(url, context);",
  "  const { readFile } = await import('node:fs/promises');",
  "  const ts = (await import(typescriptURL)).default;",
  "  const source = await readFile(new URL(url), 'utf8');",
  "  const output = ts.transpileModule(source, { compilerOptions: {",
  "    jsx: ts.JsxEmit.ReactJSX, module: ts.ModuleKind.ESNext, target: ts.ScriptTarget.ES2022,",
  "  }}).outputText;",
  "  return { format: 'module', shortCircuit: true, source: output };",
  "}",
].join("\n");

register(`data:text/javascript,${encodeURIComponent(resolverSource)}`, {
  parentURL: import.meta.url,
  data: {
    webRootURL: new URL("../", import.meta.url).href,
    typescriptURL: import.meta.resolve("typescript"),
  },
});

type TimingModule = typeof import("../src/lib/foundation/secrets/timing-policy.ts");
type OwnerModule = typeof import("../src/lib/foundation/secrets/lifecycle-owner.ts");
type ComponentModule = typeof import("../src/components/foundation/secrets/one-time-secret-reveal.ts");
type I18nModule = typeof import("../src/lib/i18n.ts");

const webRoot = fileURLToPath(new URL("..", import.meta.url));
const marker = "B06-UNIQUE-SECRET-MARKER-7d20b5";
const replacementMarker = "B06-REPLACEMENT-MARKER-a1f942";

let timingPromise: Promise<TimingModule> | undefined;
let ownerPromise: Promise<OwnerModule> | undefined;
let componentPromise: Promise<ComponentModule> | undefined;
let i18nPromise: Promise<I18nModule> | undefined;

function loadTiming() {
  timingPromise ??= import("../src/lib/foundation/secrets/timing-policy.ts");
  return timingPromise;
}

function loadOwner() {
  ownerPromise ??= import("../src/lib/foundation/secrets/lifecycle-owner.ts");
  return ownerPromise;
}

function loadComponent() {
  componentPromise ??= import("../src/components/foundation/secrets/one-time-secret-reveal.ts");
  return componentPromise;
}

function loadI18n() {
  i18nPromise ??= import("../src/lib/i18n.ts");
  return i18nPromise;
}

test("leakage oracle rejects an intentionally unsafe runtime owner and renderer", () => {
  const unsafeOwner = {
    getSnapshot: () => ({ value: marker }),
    read: () => marker,
  };
  const unsafeRenderer = () => ({
    domText: unsafeOwner.read(),
    ariaLabel: unsafeOwner.read(),
    liveText: unsafeOwner.read(),
    dataAttribute: unsafeOwner.read(),
    storageWrites: [unsafeOwner.read()],
    urlWrites: [unsafeOwner.read()],
    logWrites: [unsafeOwner.read()],
    snapshot: unsafeOwner.getSnapshot(),
  });
  const unsafeObservation = unsafeRenderer();
  assert.throws(
    () => assertLeakageFree(unsafeObservation, marker),
    /secret marker reached/,
  );
  assert.doesNotThrow(() => assertLeakageFree({
    domText: "",
    ariaLabel: "Reveal",
    liveText: "Information will be cleared soon.",
    dataAttribute: "",
    storageWrites: [],
    urlWrites: [],
    logWrites: [],
    snapshot: { phase: "concealed" },
  }, marker));
});

test("timing policy freezes the 10-minute hard max and shorter backend deadlines", async () => {
  const {
    ONE_TIME_SECRET_HARD_MAX_MS,
    ONE_TIME_SECRET_WARNING_LEAD_MS,
    planOneTimeSecretTiming,
  } = await loadTiming();
  assert.equal(ONE_TIME_SECRET_HARD_MAX_MS, 600_000);
  assert.equal(ONE_TIME_SECRET_WARNING_LEAD_MS, 60_000);

  const adoptedAtEpochMs = 1_000_000;
  const adoptedAtMonotonicMs = 50_000;
  const cases = [
    ["absent", undefined, 600_000, 590_000, 650_000],
    ["longer backend", adoptedAtEpochMs + 700_000, 600_000, 590_000, 650_000],
    ["shorter backend", adoptedAtEpochMs + 120_000, 120_000, 110_000, 170_000],
    ["thirty seconds", adoptedAtEpochMs + 30_000, 30_000, 50_000, 80_000],
    ["exact warning lead", adoptedAtEpochMs + 60_000, 60_000, 50_000, 110_000],
  ] as const;
  for (const [label, backendExpiresAtEpochMs, effectiveLifetimeMs, warningAtMonotonicMs, expiresAtMonotonicMs] of cases) {
    const plan = planOneTimeSecretTiming({
      adoptedAtEpochMs,
      adoptedAtMonotonicMs,
      ...(backendExpiresAtEpochMs === undefined ? {} : { backendExpiresAtEpochMs }),
    });
    assert.deepEqual(plan, { effectiveLifetimeMs, warningAtMonotonicMs, expiresAtMonotonicMs }, label);
    assert.equal(Object.isFrozen(plan), true, label);
  }
});

test("timing policy rejects hostile, elapsed, fractional, and overflow inputs without throwing", async () => {
  const { planOneTimeSecretTiming } = await loadTiming();
  const valid = { adoptedAtEpochMs: 1_000, adoptedAtMonotonicMs: 2_000 };
  const revoked = Proxy.revocable(valid, {});
  revoked.revoke();
  const hostile = new Proxy(valid, { get: () => { throw new Error(marker); } });
  const invalidInputs: readonly unknown[] = [
    null,
    {},
    { ...valid, adoptedAtEpochMs: Number.NaN },
    { ...valid, adoptedAtEpochMs: Number.POSITIVE_INFINITY },
    { ...valid, adoptedAtEpochMs: -1 },
    { ...valid, adoptedAtMonotonicMs: 1.5 },
    { ...valid, backendExpiresAtEpochMs: 1_000 },
    { ...valid, backendExpiresAtEpochMs: 999 },
    { ...valid, backendExpiresAtEpochMs: Number.MAX_SAFE_INTEGER + 1 },
    { adoptedAtEpochMs: 1_000, adoptedAtMonotonicMs: Number.MAX_SAFE_INTEGER - 100 },
    hostile,
    revoked.proxy,
  ];
  for (const value of invalidInputs) {
    let result: unknown = Symbol("not-called");
    assert.doesNotThrow(() => { result = runtimePlan(planOneTimeSecretTiming, value); });
    assert.equal(result, undefined);
  }
});

test("timing oracle rejects deadline-extension, late-warning, and wall-clock mutants", async () => {
  const { planOneTimeSecretTiming } = await loadTiming();
  const input = { adoptedAtEpochMs: 5_000, adoptedAtMonotonicMs: 9_000 };
  const actual = planOneTimeSecretTiming(input);
  assert.ok(actual);
  assertTimingPlan(actual, { lifetime: 600_000, warning: 549_000, expiry: 609_000 });
  assert.throws(
    () => assertTimingPlan({ ...actual, effectiveLifetimeMs: 700_000, expiresAtMonotonicMs: 709_000 }, { lifetime: 600_000, warning: 549_000, expiry: 609_000 }),
    /lifetime/,
  );
  assert.throws(
    () => assertTimingPlan({ ...actual, warningAtMonotonicMs: 669_000 }, { lifetime: 600_000, warning: 549_000, expiry: 609_000 }),
    /warning/,
  );
  const afterWallJump = planOneTimeSecretTiming({ ...input, adoptedAtEpochMs: 50_000 });
  assert.ok(afterWallJump);
  assert.equal(afterWallJump.expiresAtMonotonicMs, actual.expiresAtMonotonicMs);
});

test("canonical timing oracle rejects executable in-memory policy mutants", async () => {
  const canonical = await loadTiming();
  assert.doesNotThrow(() => assertCanonicalTimingPolicy(canonical));
  const mutants = [
    [
      "backend expiry ignored",
      (source: string) => source.replace(
        "effectiveLifetimeMs = Math.min(backendLifetimeMs, ONE_TIME_SECRET_HARD_MAX_MS);",
        "effectiveLifetimeMs = ONE_TIME_SECRET_HARD_MAX_MS;",
      ),
    ],
    [
      "hard maximum extended",
      (source: string) => source.replace("10 * 60 * 1000", "11 * 60 * 1000"),
    ],
    [
      "warning after expiry",
      (source: string) => source.replace(
        "expiresAtMonotonicMs - ONE_TIME_SECRET_WARNING_LEAD_MS",
        "expiresAtMonotonicMs + ONE_TIME_SECRET_WARNING_LEAD_MS",
      ),
    ],
    [
      "wall clock controls monotonic deadline",
      (source: string) => source.replace(
        "const expiresAtMonotonicMs = adoptedAtMonotonicMs + effectiveLifetimeMs;",
        "const expiresAtMonotonicMs = adoptedAtEpochMs + effectiveLifetimeMs;",
      ),
    ],
  ] as const;
  for (const [label, mutate] of mutants) {
    const mutant = await loadTimingMutant(mutate);
    assert.throws(() => assertCanonicalTimingPolicy(mutant), undefined, label);
  }
});

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

test("every clear trigger removes the old marker from the owner and controlled renderer", async () => {
  const { createOneTimeSecretLifecycleOwner } = await loadOwner();
  const { OneTimeSecretReveal } = await loadComponent();
  const cases = [
    "acknowledge",
    "dismiss",
    "timeout",
    "navigation",
    "unmount",
    "session loss",
    "replacement",
    "invalid adoption",
  ] as const;
  for (const label of cases) {
    const clock = new OneTimeSecretFakeClock();
    const owner = createOneTimeSecretLifecycleOwner<string>(clock);
    const bundle = label === "timeout"
      ? { value: marker, backendExpiresAtEpochMs: clock.epochNowMs() + 30_000, initialVisibility: "revealed" as const }
      : { value: marker, initialVisibility: "revealed" as const };
    assert.equal(owner.replace(bundle), true, label);
    switch (label) {
      case "acknowledge": owner.acknowledge(); break;
      case "dismiss": owner.dismiss(); break;
      case "timeout": clock.advanceMonotonicBy(30_000); break;
      case "navigation": owner.clearForNavigation(); break;
      case "unmount": owner.dispose(); break;
      case "session loss": owner.clearForSessionLoss(); break;
      case "replacement": owner.replace({ value: replacementMarker }); break;
      case "invalid adoption": runtimeReplace(owner.replace, { value: replacementMarker, backendExpiresAtEpochMs: -1 }); break;
    }
    assert.equal(owner.readRevealedValue(), undefined, label);
    const markup = renderToStaticMarkup(createElement(OneTimeSecretReveal, componentProps(
      owner.getSnapshot(),
      () => createElement("code", { "data-secret-marker": "" }, marker),
    )));
    assert.equal(markup.includes(marker), false, label);
    assertSecretAbsentFromAutomaticSurfaces(markup, marker);
    assert.equal(clock.pendingTimerCount(), label === "replacement" ? 2 : 0, label);
  }
});

test("controlled renderer calls the lazy source boundary only while revealed or copied", async () => {
  const { OneTimeSecretReveal } = await loadComponent();
  const states = [
    [activeSnapshot(1, "concealed"), 0, false],
    [activeSnapshot(1, "revealed"), 1, true],
    [activeSnapshot(1, "copied", "copied"), 1, true],
    [terminalSnapshot(1, "acknowledged", "acknowledged"), 0, false],
    [terminalSnapshot(1, "cleared", "dismissed"), 0, false],
    [terminalSnapshot(1, "cleared", "expired"), 0, false],
  ] as const;
  for (const [snapshot, expectedCalls, markerVisible] of states) {
    let calls = 0;
    const markup = renderToStaticMarkup(createElement(OneTimeSecretReveal, componentProps(
      snapshot,
      () => {
        calls += 1;
        return createElement("code", { "data-secret-marker": "" }, marker);
      },
    )));
    assert.equal(calls, expectedCalls, snapshot.phase);
    assert.equal(markup.includes(marker), markerVisible, snapshot.phase);
    assert.equal(markup.includes("data-secret-marker"), markerVisible, snapshot.phase);
    assertSecretAbsentFromAutomaticSurfaces(markup, marker);
  }
});

test("controlled renderer exposes only generic warning, copy, acknowledgement, and clear messages", async () => {
  const { OneTimeSecretReveal } = await loadComponent();
  const revealedWarning = renderToStaticMarkup(createElement(OneTimeSecretReveal, componentProps(
    { ...activeSnapshot(1, "revealed"), warningActive: true },
    () => createElement("code", null, marker),
  )));
  assert.match(revealedWarning, /This information will be cleared automatically soon/);
  assert.match(revealedWarning, /role="status"/);
  assert.match(revealedWarning, /aria-live="polite"/);
  assertSecretAbsentFromAutomaticSurfaces(revealedWarning, marker);

  const failed = renderToStaticMarkup(createElement(OneTimeSecretReveal, componentProps(
    activeSnapshot(1, "revealed", "failed"),
    () => createElement("code", null, marker),
  )));
  assert.match(failed, /could not be copied/);
  assert.equal(failed.includes("Error"), false);
  assert.equal(failed.includes("clipboard exception"), false);

  const acknowledged = renderToStaticMarkup(createElement(OneTimeSecretReveal, componentProps(
    terminalSnapshot(1, "acknowledged", "acknowledged"),
    () => createElement("code", null, marker),
  )));
  assert.match(acknowledged, /acknowledged and cleared/);
  assert.equal(acknowledged.includes(marker), false);

  const expired = renderToStaticMarkup(createElement(OneTimeSecretReveal, componentProps(
    terminalSnapshot(1, "cleared", "expired"),
    () => createElement("code", null, marker),
  )));
  assert.match(expired, /display period expired/);
  assert.equal(expired.includes(marker), false);
});

test("component, unmount, copy-error, and persistence oracles reject executable runtime mutants", () => {
  const safeRenderer: RuntimeSecretRenderer = (phase, render) => ({
    domText: phase === "revealed" ? render() : "",
    automatic: Object.freeze({ attributes: [], liveText: "" }),
  });
  assert.doesNotThrow(() => assertRuntimeSecretRenderer(safeRenderer, "concealed"));
  assert.doesNotThrow(() => assertRuntimeSecretRenderer(safeRenderer, "cleared"));
  assert.doesNotThrow(() => assertRuntimeSecretRenderer(safeRenderer, "revealed"));

  const concealedRenderMutant: RuntimeSecretRenderer = (_phase, render) => {
    render();
    return { domText: "", automatic: Object.freeze({ attributes: [], liveText: "" }) };
  };
  assert.throws(() => assertRuntimeSecretRenderer(concealedRenderMutant, "concealed"), /lazy render count/);

  const clearedDOMMutant: RuntimeSecretRenderer = (_phase, render) => ({
    domText: render(),
    automatic: Object.freeze({ attributes: [], liveText: "" }),
  });
  assert.throws(() => assertRuntimeSecretRenderer(clearedDOMMutant, "cleared"), /lazy render count|secret marker/);

  const automaticSurfaceMutant: RuntimeSecretRenderer = (_phase, render) => {
    const value = render();
    return {
      domText: value,
      automatic: Object.freeze({ attributes: [value], liveText: value, dataValue: value }),
    };
  };
  assert.throws(() => assertRuntimeSecretRenderer(automaticSurfaceMutant, "revealed"), /secret marker/);

  assert.doesNotThrow(() => assertUnmountRegistration((callback) => {
    let called = false;
    return () => {
      if (called) return;
      called = true;
      callback();
    };
  }));
  assert.throws(() => assertUnmountRegistration(() => () => {}), /unmount callback/);

  assert.doesNotThrow(() => assertSilentCopyFailure((writer) => {
    try { writer(); } catch { /* raw copy failure intentionally discarded */ }
  }));
  assert.throws(() => assertSilentCopyFailure((writer, log) => {
    try { writer(); } catch (error) { log(String(error)); }
  }), /secret marker/);

  assert.doesNotThrow(() => assertNoPersistenceWrite((value, write) => {
    void value;
    void write;
  }));
  assert.throws(() => assertNoPersistenceWrite((value, write) => write("secret", value)), /secret marker/);
});

test("exact B-06 translations have ja/en parity, no placeholders, and no false erasure guarantee", async () => {
  const { translations } = await loadI18n();
  const expected = {
    oneTimeSecretReady: ["一度だけ表示される機密情報を受け取りました。", "One-time sensitive information is ready."],
    oneTimeSecretReveal: ["表示", "Reveal"],
    oneTimeSecretConceal: ["隠す", "Conceal"],
    oneTimeSecretCopy: ["コピー", "Copy"],
    oneTimeSecretCopied: ["コピーしました。", "Copied."],
    oneTimeSecretCopyFailed: ["コピーできませんでした。必要な内容を手動で選択してください。", "The information could not be copied. Select it manually if needed."],
    oneTimeSecretAcknowledge: ["内容を確認して消去", "Confirm and clear"],
    oneTimeSecretDismiss: ["消去して閉じる", "Clear and close"],
    oneTimeSecretExpiringSoon: ["まもなくこの情報を自動的に消去します。", "This information will be cleared automatically soon."],
    oneTimeSecretExpired: ["有効期限により、この情報を消去しました。", "This information was cleared when its display period expired."],
    oneTimeSecretCleared: ["この情報を消去しました。", "This information has been cleared."],
    oneTimeSecretAcknowledged: ["確認済みとして、この情報を消去しました。", "This information was acknowledged and cleared."],
    oneTimeSecretExposureWarning: ["表示中の情報は、画面共有、スクリーンショット、ブラウザー拡張機能、クリップボードに残る可能性があります。", "Revealed information may remain in screen shares, screenshots, browser extensions, or the clipboard."],
  } as const;
  for (const [key, [ja, en]] of Object.entries(expected)) {
    assert.equal(translations.ja[key as TranslationKey], ja);
    assert.equal(translations.en[key as TranslationKey], en);
    assert.equal(/\{[a-zA-Z0-9_]+\}/.test(ja), false, key);
    assert.equal(/\{[a-zA-Z0-9_]+\}/.test(en), false, key);
  }
  assert.equal(Object.keys(expected).length, 13);
  assert.equal(Object.values(expected).flat().some((message) => /clipboard (?:is|will be) (?:cleared|erased)/i.test(message)), false);
  assert.equal(Object.values(expected).flat().some((message) => message.includes(marker)), false);
});

test("AST dependency and type guard rejects broad assertions and preserves zero consumers", () => {
  assert.deepEqual(assertSecretFoundationBoundaries(webRoot), {
    productionConsumerCount: 0,
    reviewedFileCount: 4,
  });
  const ownerPath = join(webRoot, "src", "lib", "foundation", "secrets", "lifecycle-owner.ts");
  const ownerSource = readFileSync(ownerPath, "utf8");
  assert.throws(() => assertSecretFoundationBoundaries(webRoot, new Map([[
    "src/lib/foundation/secrets/lifecycle-owner.ts",
    `${ownerSource}\nconst leaked = localStorage;\nvoid leaked;\n`,
  ]])), /forbidden global/);
  assert.throws(() => assertSecretFoundationBoundaries(webRoot, new Map([[
    "src/features/synthetic/secret-consumer.ts",
    'import { createOneTimeSecretLifecycleOwner } from "@/lib/foundation/secrets/lifecycle-owner";\nvoid createOneTimeSecretLifecycleOwner;\n',
  ]])), /zero production consumers/);
  const componentPath = join(webRoot, "src", "components", "foundation", "secrets", "one-time-secret-reveal.ts");
  const componentSource = readFileSync(componentPath, "utf8");
  const rawPropMutant = componentSource.replace(
    "snapshot: OneTimeSecretSnapshot;",
    "snapshot: OneTimeSecretSnapshot;\n  secret: string;",
  );
  assert.notEqual(rawPropMutant, componentSource);
  assert.throws(() => assertSecretFoundationBoundaries(webRoot, new Map([[
    "src/components/foundation/secrets/one-time-secret-reveal.ts",
    rawPropMutant,
  ]])), /raw source prop secret/);

  const broadAssertionMutants = [
    ["direct never", "function broadNever(input: unknown) { return input as never; }"],
    ["direct any", "function broadAny(input: unknown) { return input as any; }"],
    ["chained timing input", "function broadTiming(input: unknown) { return input as unknown as OneTimeSecretTimingInput; }"],
    ["angle never", "function angleNever(input: unknown) { return <never>input; }"],
    ["angle any", "function angleAny(input: unknown) { return <any>input; }"],
    ["aliased never", "type Bottom = never;\nfunction aliasedNever(input: unknown) { return input as Bottom; }"],
    ["aliased any", "type Unsafe = any;\nfunction aliasedAny(input: unknown) { return input as Unsafe; }"],
    [
      "hidden helper",
      "function coerce(value: unknown): OneTimeSecretTimingInput {\n  return value as unknown as OneTimeSecretTimingInput;\n}",
    ],
  ] as const;
  for (const [name, fixture] of broadAssertionMutants) {
    assert.throws(() => assertSecretFoundationBoundaries(webRoot, new Map([[
      "src/lib/foundation/secrets/lifecycle-owner.ts",
      `${ownerSource}\n${fixture}\n`,
    ]])), /production broad assertion found/, name);
  }

  const allowedConstAssertion = `${ownerSource}\nconst literalInference = { phase: "concealed" } as const;\nvoid literalInference;\n`;
  assert.deepEqual(assertSecretFoundationBoundaries(webRoot, new Map([[
    "src/lib/foundation/secrets/lifecycle-owner.ts",
    allowedConstAssertion,
  ]])), {
    productionConsumerCount: 0,
    reviewedFileCount: 4,
  });
  assertRuntimeTimingPlanTypeBoundary(ownerSource, ownerPath);
});

test("type negative matrix is mutation-sensitive and reports TS2578 for a valid conversion", () => {
  const configPath = join(webRoot, "tsconfig.json");
  const typeTestPath = resolve(webRoot, "tests", "ui-foundation-secrets.type-test.ts");
  const configRead = ts.readConfigFile(configPath, ts.sys.readFile);
  assert.equal(configRead.error, undefined);
  const config = ts.parseJsonConfigFileContent(configRead.config, ts.sys, webRoot);
  const original = readFileSync(typeTestPath, "utf8");
  const mutant = original.replace(
    'invalidPhase: OneTimeSecretPhase = "gone"',
    'invalidPhase: OneTimeSecretPhase = "cleared"',
  );
  assert.notEqual(mutant, original);
  const canonicalTarget = resolve(typeTestPath);
  const host = ts.createCompilerHost(config.options);
  const originalGetSourceFile = host.getSourceFile.bind(host);
  host.getSourceFile = (fileName, languageVersion, onError, shouldCreateNewSourceFile) =>
    resolve(fileName) === canonicalTarget
      ? ts.createSourceFile(fileName, mutant, languageVersion, true, ts.ScriptKind.TS)
      : originalGetSourceFile(fileName, languageVersion, onError, shouldCreateNewSourceFile);
  const program = ts.createProgram({ rootNames: config.fileNames, options: config.options, host });
  const diagnostics = ts.getPreEmitDiagnostics(program);
  assert.equal(
    diagnostics.some((diagnostic) => diagnostic.code === 2578 && resolve(diagnostic.file?.fileName ?? "") === canonicalTarget),
    true,
    "a valid conversion must leave an unused @ts-expect-error diagnostic",
  );
});

function assertRuntimeTimingPlanTypeBoundary(ownerSource: string, ownerPath: string) {
  const sourceFile = ts.createSourceFile(
    ownerPath,
    ownerSource,
    ts.ScriptTarget.Latest,
    true,
    ts.ScriptKind.TS,
  );
  const declaration = sourceFile.statements.find((statement): statement is ts.FunctionDeclaration =>
    ts.isFunctionDeclaration(statement) && statement.name?.text === "runtimeTimingPlan");
  assert.ok(declaration, "runtimeTimingPlan declaration is missing");
  assert.equal(declaration.parameters.length, 1, "runtimeTimingPlan accepts only the typed timing input");
  assert.equal(declaration.parameters[0]?.name.getText(sourceFile), "input");
  assert.equal(declaration.parameters[0]?.type?.getText(sourceFile), "OneTimeSecretTimingInput");
  assert.equal(declaration.type?.getText(sourceFile), "OneTimeSecretTimingPlan | undefined");

  const plannerCalls = collectSyntaxNodes(
    declaration,
    (node): node is ts.CallExpression => ts.isCallExpression(node),
  ).filter((call) => ts.isIdentifier(call.expression) && call.expression.text === "planOneTimeSecretTiming");
  assert.equal(plannerCalls.length, 1, "runtimeTimingPlan calls the canonical planner exactly once");
  assert.equal(plannerCalls[0]?.arguments.length, 1);
  const plannerInput = plannerCalls[0]?.arguments[0];
  assert.ok(plannerInput && ts.isIdentifier(plannerInput) && plannerInput.text === "input", "planner receives the typed input directly");

  const boundaryCalls = collectSyntaxNodes(
    sourceFile,
    (node): node is ts.CallExpression => ts.isCallExpression(node),
  ).filter((call) => ts.isIdentifier(call.expression) && call.expression.text === "runtimeTimingPlan");
  assert.equal(boundaryCalls.length, 1, "owner assembles one timing input at the lifecycle boundary");
  assert.equal(boundaryCalls[0]?.arguments.length, 1);
  assert.equal(ts.isObjectLiteralExpression(boundaryCalls[0]?.arguments[0] as ts.Expression), true, "caller constructs the typed object explicitly");

  const malformedFixture = [
    ownerSource,
    "// @ts-expect-error -- runtime timing boundary rejects malformed epoch values",
    'runtimeTimingPlan({ adoptedAtEpochMs: "compile-time-invalid", adoptedAtMonotonicMs: 0 });',
    "",
  ].join("\n");
  assert.deepEqual(ownerDiagnostics(ownerPath, malformedFixture), [], "malformed timing input must consume the expected type error");
  const validFixture = malformedFixture.replace(
    'adoptedAtEpochMs: "compile-time-invalid"',
    "adoptedAtEpochMs: 0",
  );
  assert.notEqual(validFixture, malformedFixture);
  assert.equal(
    ownerDiagnostics(ownerPath, validFixture).some((diagnostic) => diagnostic.code === 2578),
    true,
    "a valid timing input must expose an unused @ts-expect-error",
  );
}

function ownerDiagnostics(ownerPath: string, source: string) {
  const configRead = ts.readConfigFile(join(webRoot, "tsconfig.json"), ts.sys.readFile);
  assert.equal(configRead.error, undefined);
  const config = ts.parseJsonConfigFileContent(configRead.config, ts.sys, webRoot);
  const canonicalTarget = resolve(ownerPath);
  const host = ts.createCompilerHost(config.options);
  const originalGetSourceFile = host.getSourceFile.bind(host);
  host.getSourceFile = (fileName, languageVersion, onError, shouldCreateNewSourceFile) =>
    resolve(fileName) === canonicalTarget
      ? ts.createSourceFile(fileName, source, languageVersion, true, ts.ScriptKind.TS)
      : originalGetSourceFile(fileName, languageVersion, onError, shouldCreateNewSourceFile);
  const program = ts.createProgram({
    rootNames: [canonicalTarget],
    options: config.options,
    host,
  });
  return ts.getPreEmitDiagnostics(program)
    .filter((diagnostic) => resolve(diagnostic.file?.fileName ?? "") === canonicalTarget);
}

function collectSyntaxNodes<T extends ts.Node>(
  root: ts.Node,
  predicate: (node: ts.Node) => node is T,
): T[] {
  const matches: T[] = [];
  const visit = (node: ts.Node) => {
    if (predicate(node)) matches.push(node);
    ts.forEachChild(node, visit);
  };
  visit(root);
  return matches;
}

function componentProps(
  snapshot: OneTimeSecretSnapshot,
  renderRevealedContent: () => ReactElement,
): OneTimeSecretRevealProps {
  return {
    snapshot,
    translate: englishTranslation,
    renderRevealedContent,
    canCopy: true,
    onRevealIntent: () => {},
    onConcealIntent: () => {},
    onCopyIntent: () => {},
    onAcknowledgeIntent: () => {},
    onDismissIntent: () => {},
    onUnmountIntent: () => {},
  };
}

function englishTranslation(key: TranslationKey) {
  const messages: Partial<Record<TranslationKey, string>> = {
    oneTimeSecretReady: "One-time sensitive information is ready.",
    oneTimeSecretReveal: "Reveal",
    oneTimeSecretConceal: "Conceal",
    oneTimeSecretCopy: "Copy",
    oneTimeSecretCopied: "Copied.",
    oneTimeSecretCopyFailed: "The information could not be copied. Select it manually if needed.",
    oneTimeSecretAcknowledge: "Confirm and clear",
    oneTimeSecretDismiss: "Clear and close",
    oneTimeSecretExpiringSoon: "This information will be cleared automatically soon.",
    oneTimeSecretExpired: "This information was cleared when its display period expired.",
    oneTimeSecretCleared: "This information has been cleared.",
    oneTimeSecretAcknowledged: "This information was acknowledged and cleared.",
    oneTimeSecretExposureWarning: "Revealed information may remain in screen shares, screenshots, browser extensions, or the clipboard.",
  };
  return messages[key] ?? key;
}

function clearedSnapshot(): OneTimeSecretSnapshot {
  return Object.freeze({
    generation: 0,
    phase: "cleared",
    warningActive: false,
    copyStatus: "idle",
  });
}

function activeSnapshot(
  generation: number,
  phase: "concealed" | "revealed" | "copied",
  copyStatus: "idle" | "copied" | "failed" = "idle",
): OneTimeSecretSnapshot {
  return Object.freeze({ generation, phase, warningActive: false, copyStatus });
}

function terminalSnapshot(
  generation: number,
  phase: "acknowledged" | "cleared" | string,
  clearReason: string,
) {
  return Object.freeze({ generation, phase, warningActive: false, copyStatus: "idle", clearReason });
}

function runtimePlan(
  plan: (input: never) => unknown,
  input: unknown,
) {
  return plan(input as never);
}

function runtimeReplace(
  replace: (bundle: never) => boolean,
  bundle: unknown,
) {
  return replace(bundle as never);
}

function assertTimingPlan(
  actual: Readonly<{
    effectiveLifetimeMs: number;
    warningAtMonotonicMs: number;
    expiresAtMonotonicMs: number;
  }>,
  expected: Readonly<{ lifetime: number; warning: number; expiry: number }>,
) {
  assert.equal(actual.effectiveLifetimeMs, expected.lifetime, "lifetime");
  assert.equal(actual.warningAtMonotonicMs, expected.warning, "warning");
  assert.equal(actual.expiresAtMonotonicMs, expected.expiry, "expiry");
}

function assertCanonicalTimingPolicy(module: TimingModule) {
  assert.equal(module.ONE_TIME_SECRET_HARD_MAX_MS, 600_000, "hard maximum");
  assert.equal(module.ONE_TIME_SECRET_WARNING_LEAD_MS, 60_000, "warning lead");
  const short = module.planOneTimeSecretTiming({
    adoptedAtEpochMs: 1_000_000,
    adoptedAtMonotonicMs: 10_000,
    backendExpiresAtEpochMs: 1_120_000,
  });
  assert.ok(short);
  assertTimingPlan(short, { lifetime: 120_000, warning: 70_000, expiry: 130_000 });
  const firstWall = module.planOneTimeSecretTiming({
    adoptedAtEpochMs: 1_000_000,
    adoptedAtMonotonicMs: 10_000,
  });
  const jumpedWall = module.planOneTimeSecretTiming({
    adoptedAtEpochMs: 9_000_000,
    adoptedAtMonotonicMs: 10_000,
  });
  assert.ok(firstWall);
  assert.ok(jumpedWall);
  assert.equal(jumpedWall.expiresAtMonotonicMs, firstWall.expiresAtMonotonicMs, "wall-clock independence");
}

function assertReplacementOrdering(
  createOwner: OwnerModule["createOneTimeSecretLifecycleOwner"],
) {
  const clock = new OneTimeSecretFakeClock();
  const owner = createOwner<string>(clock);
  assert.equal(owner.replace({ value: marker, initialVisibility: "revealed" }), true);
  const events: Array<Readonly<{ generation: number; phase: string; available: boolean }>> = [];
  owner.subscribe(() => {
    const snapshot = owner.getSnapshot();
    events.push(Object.freeze({
      generation: snapshot.generation,
      phase: snapshot.phase,
      available: owner.readRevealedValue() !== undefined,
    }));
  });
  assert.equal(owner.replace({ value: replacementMarker, initialVisibility: "revealed" }), true);
  assert.deepEqual(events, [
    { generation: 1, phase: "cleared", available: false },
    { generation: 2, phase: "revealed", available: true },
  ]);
}

function assertStaleTimerFence(
  createOwner: OwnerModule["createOneTimeSecretLifecycleOwner"],
) {
  const clock = new OneTimeSecretFakeClock();
  const owner = createOwner<string>({
    epochNowMs: clock.epochNowMs,
    monotonicNowMs: clock.monotonicNowMs,
    schedule: clock.schedule,
    cancel: () => {},
  });
  assert.equal(owner.replace({ value: marker, initialVisibility: "revealed" }), true);
  clock.advanceMonotonicBy(100_000);
  assert.equal(owner.replace({ value: replacementMarker, initialVisibility: "revealed" }), true);
  clock.advanceMonotonicBy(500_000);
  assert.equal(owner.readRevealedValue(), replacementMarker, "new generation survives the old expiry");
  clock.advanceMonotonicBy(100_000);
  assert.equal(owner.readRevealedValue(), undefined, "new generation still expires at its own deadline");
  assert.equal(owner.getSnapshot().clearReason, "expired");
}

async function assertLateCopyFence(
  createOwner: OwnerModule["createOneTimeSecretLifecycleOwner"],
) {
  const owner = createOwner<string>(new OneTimeSecretFakeClock());
  owner.replace({ value: marker, initialVisibility: "revealed" });
  const pending = deferred<void>();
  const oldCopy = owner.copyWith(() => pending.promise);
  owner.replace({ value: replacementMarker, initialVisibility: "revealed" });
  pending.resolve();
  assert.equal(await oldCopy, "stale-generation");
  assert.equal(owner.getSnapshot().phase, "revealed");
  assert.equal(owner.readRevealedValue(), replacementMarker);
}

function assertSessionLossCleanup(
  createOwner: OwnerModule["createOneTimeSecretLifecycleOwner"],
) {
  const clock = new OneTimeSecretFakeClock();
  const owner = createOwner<string>(clock);
  owner.replace({ value: marker, initialVisibility: "revealed" });
  assert.equal(owner.clearForSessionLoss(), true);
  assert.equal(owner.readRevealedValue(), undefined);
  assert.equal(owner.getSnapshot().clearReason, "session-lost");
  assert.equal(clock.pendingTimerCount(), 0);
}

type RuntimeSecretRenderer = (
  phase: "concealed" | "revealed" | "cleared",
  render: () => string,
) => Readonly<{ domText: string; automatic: unknown }>;

function assertRuntimeSecretRenderer(
  renderer: RuntimeSecretRenderer,
  phase: "concealed" | "revealed" | "cleared",
) {
  let renderCalls = 0;
  const observation = renderer(phase, () => {
    renderCalls += 1;
    return marker;
  });
  assert.equal(renderCalls, phase === "revealed" ? 1 : 0, "lazy render count");
  if (phase !== "revealed") assertLeakageFree(observation.domText, marker);
  assertLeakageFree(observation.automatic, marker);
}

function assertUnmountRegistration(
  register: (callback: () => void) => () => void,
) {
  let calls = 0;
  const teardown = register(() => { calls += 1; });
  teardown();
  teardown();
  assert.equal(calls, 1, "unmount callback exactly once");
}

function assertSilentCopyFailure(
  copy: (writer: () => void, log: (value: unknown) => void) => void,
) {
  const logs: unknown[] = [];
  copy(() => { throw new Error(marker); }, (value) => logs.push(value));
  assertLeakageFree(logs, marker);
}

function assertNoPersistenceWrite(
  adopt: (value: string, write: (key: string, value: string) => void) => void,
) {
  const writes: Array<Readonly<{ key: string; value: string }>> = [];
  adopt(marker, (key, value) => writes.push(Object.freeze({ key, value })));
  assertLeakageFree(writes, marker);
}

async function loadTimingMutant(mutate: (source: string) => string) {
  const path = join(webRoot, "src", "lib", "foundation", "secrets", "timing-policy.ts");
  const source = readFileSync(path, "utf8");
  const mutant = mutate(source);
  assert.notEqual(mutant, source, "timing mutant rewrite must change source");
  return importTypeScriptDataModule<TimingModule>(mutant, path);
}

async function loadOwnerMutant(mutate: (source: string) => string) {
  const timingPath = join(webRoot, "src", "lib", "foundation", "secrets", "timing-policy.ts");
  const timingURL = typeScriptDataURL(readFileSync(timingPath, "utf8"), timingPath);
  const ownerPath = join(webRoot, "src", "lib", "foundation", "secrets", "lifecycle-owner.ts");
  const source = readFileSync(ownerPath, "utf8");
  const mutant = mutate(source);
  assert.notEqual(mutant, source, "owner mutant rewrite must change source");
  const linked = mutant.replace(
    '"@/lib/foundation/secrets/timing-policy"',
    JSON.stringify(timingURL),
  );
  assert.notEqual(linked, mutant, "owner mutant timing import must be linked");
  return importTypeScriptDataModule<OwnerModule>(linked, ownerPath);
}

async function importTypeScriptDataModule<Module>(source: string, fileName: string) {
  return import(typeScriptDataURL(source, fileName)) as Promise<Module>;
}

let dataModuleSequence = 0;

function typeScriptDataURL(source: string, fileName: string) {
  dataModuleSequence += 1;
  const transpiled = ts.transpileModule(
    `${source}\n// in-memory-mutant-${dataModuleSequence}\n`,
    {
      fileName,
      compilerOptions: {
        module: ts.ModuleKind.ESNext,
        target: ts.ScriptTarget.ES2022,
      },
    },
  ).outputText;
  return `data:text/javascript;base64,${Buffer.from(transpiled).toString("base64")}`;
}

function assertLeakageFree(observation: unknown, sourceMarker: string) {
  if (deepContains(observation, sourceMarker)) {
    throw new Error("secret marker reached a forbidden observation surface");
  }
}

function deepContains(value: unknown, sourceMarker: string, seen = new Set<object>()): boolean {
  if (typeof value === "string") return value.includes(sourceMarker);
  if (typeof value !== "object" || value === null || seen.has(value)) return false;
  seen.add(value);
  return Object.entries(value).some(([key, entry]) => key.includes(sourceMarker) || deepContains(entry, sourceMarker, seen));
}

function assertSecretAbsentFromAutomaticSurfaces(markup: string, sourceMarker: string) {
  const tags = markup.match(/<[^>]+>/g) ?? [];
  assert.equal(tags.some((tag) => tag.includes(sourceMarker)), false, "secret absent from attributes");
  const liveRegions = markup.match(/<[^>]+(?:role="status"|aria-live="polite")[^>]*>.*?<\/[^>]+>/g) ?? [];
  assert.equal(liveRegions.some((region) => region.includes(sourceMarker)), false, "secret absent from live regions");
}

function deferred<T>() {
  let resolvePromise: (value: T | PromiseLike<T>) => void = () => {};
  let rejectPromise: (reason?: unknown) => void = () => {};
  const promise = new Promise<T>((resolveDeferred, rejectDeferred) => {
    resolvePromise = resolveDeferred;
    rejectPromise = rejectDeferred;
  });
  return Object.freeze({ promise, resolve: resolvePromise, reject: rejectPromise });
}
