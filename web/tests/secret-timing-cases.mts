import assert from "node:assert/strict";
import test from "node:test";
import { marker, assertLeakageFree, loadTiming, runtimePlan, assertTimingPlan, assertCanonicalTimingPolicy, loadTimingMutant } from "./secret-lifecycle-fixture.mts";


export function registerSecretTimingCases() {


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
}
