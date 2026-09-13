import assert from "node:assert/strict";
import test from "node:test";
import type { ActionDescriptor, ActionEvaluation, ActionRisk } from "../src/lib/foundation/actions/contracts.ts";
import type { RemoteState } from "../src/lib/foundation/remote-state/contracts.ts";
import { loadPolicy, runtimeRiskDefault, descriptor, consequence, typed, runtimePlan, target, assertLiteralOracleRejects, loadRevalidation, networkError, readyState, refreshingState, staleState, freshness, runtimeFreshness, runtimeSnapshot } from "./confirmation-fixture.mts";


export function registerConfirmationPolicyCases() {


test("canonical risk defaults are frozen and malformed runtime risks fail closed", async () => {
  const { confirmationRiskDefault, confirmationRiskDefaults } = await loadPolicy();
  const expected = {
    routine: {
      minimumMode: "none",
      submitRevalidation: "normally-not-required",
      ambiguousRetry: "server-idempotent-only",
    },
    guarded: {
      minimumMode: "consequence",
      submitRevalidation: "required-when-state-dependent",
      ambiguousRetry: "manual-after-refresh",
    },
    high: {
      minimumMode: "consequence",
      submitRevalidation: "required",
      ambiguousRetry: "never",
    },
    critical: {
      minimumMode: "typed-target",
      submitRevalidation: "required",
      ambiguousRetry: "server-idempotent-only",
    },
  } as const;
  assert.deepEqual(confirmationRiskDefaults, expected);
  assert.equal(Object.isFrozen(confirmationRiskDefaults), true);
  for (const risk of Object.keys(expected) as ActionRisk[]) {
    const value = confirmationRiskDefault(risk);
    assert.deepEqual(value, expected[risk]);
    assert.equal(Object.isFrozen(value), true, risk);
  }
  assert.equal(runtimeRiskDefault(confirmationRiskDefault, "unknown"), undefined);
  assert.doesNotThrow(() => runtimeRiskDefault(confirmationRiskDefault, null));
});

test("confirmation plan validation enforces strength, revalidation, retry, and hostile-input boundaries", async () => {
  const { resolveHighRiskConfirmationPlan } = await loadPolicy();
  const cases: readonly [string, ActionDescriptor, string, string?][] = [
    ["routine consequence", descriptor({
      risk: "routine",
      confirmation: consequence(false),
      stateIndependent: true,
      revalidation: { kind: "none" },
    }), "consequence"],
    ["routine none is outside the B-05 renderer", descriptor({
      risk: "routine",
      confirmation: { mode: "none", requireSubmitRevalidation: false },
      stateIndependent: true,
      revalidation: { kind: "none" },
    }), "invalid", "risk-policy-mismatch"],
    ["routine stronger typed", descriptor({
      risk: "routine",
      confirmation: typed({ kind: "fixed-ascii", value: "CONFIRM" }),
      stateIndependent: true,
      revalidation: { kind: "revision" },
    }), "typed-target"],
    ["guarded independent", descriptor({
      risk: "guarded",
      confirmation: consequence(false),
      stateIndependent: true,
      revalidation: { kind: "none" },
      retry: { kind: "manual-after-refresh" },
      requiredSections: [],
    }), "consequence"],
    ["guarded stronger typed", descriptor({
      risk: "guarded",
      confirmation: typed({ kind: "fixed-ascii", value: "CONFIRM" }),
      stateIndependent: true,
      revalidation: { kind: "revision" },
      retry: { kind: "manual-after-refresh" },
      requiredSections: [],
    }), "typed-target"],
    ["guarded dependent no submit check", descriptor({
      risk: "guarded",
      confirmation: consequence(false),
      stateIndependent: false,
      retry: { kind: "manual-after-refresh" },
    }), "invalid", "risk-policy-mismatch"],
    ["high valid", descriptor(), "consequence"],
    ["high without revalidation", descriptor({ revalidation: undefined }), "invalid", "missing-revalidation-policy"],
    ["high stale retry claim", descriptor({ retry: { kind: "manual-after-refresh" } }), "invalid", "risk-policy-mismatch"],
    ["high idempotent retry claim", descriptor({ retry: { kind: "server-idempotent", maxAttempts: 2 } }), "invalid", "risk-policy-mismatch"],
    ["high lookup only", descriptor({ retry: { kind: "lookup-only" } }), "consequence"],
    ["high stronger typed", descriptor({ confirmation: typed({ kind: "public-stable-id" }) }), "typed-target"],
    ["critical typed label", descriptor({ risk: "critical", confirmation: typed({ kind: "target-label" }) }), "typed-target"],
    ["critical consequence", descriptor({ risk: "critical" }), "invalid", "risk-policy-mismatch"],
    ["critical manual retry", descriptor({
      risk: "critical",
      confirmation: typed({ kind: "target-label" }),
      retry: { kind: "manual-after-refresh" },
    }), "invalid", "risk-policy-mismatch"],
    ["critical explicit idempotent authority", descriptor({
      risk: "critical",
      confirmation: typed({ kind: "target-label" }),
      retry: { kind: "server-idempotent", maxAttempts: 2 },
    }), "typed-target"],
  ];
  for (const [label, value, expectedKind, expectedReason] of cases) {
    const plan = resolveHighRiskConfirmationPlan(value);
    assert.equal(plan.kind, expectedKind, label);
    if (plan.kind === "invalid") assert.equal(plan.reason, expectedReason, label);
  }

  const critical = resolveHighRiskConfirmationPlan(descriptor({
    risk: "critical",
    confirmation: typed({ kind: "target-label" }),
  }));
  assert.deepEqual(critical, {
    kind: "typed-target",
    requireSubmitRevalidation: true,
    token: "Worker Alpha",
    tokenSource: "target-label",
  });

  const hostile = new Proxy(descriptor(), { get: () => { throw new Error("hostile descriptor"); } });
  const revoked = Proxy.revocable(descriptor(), {});
  revoked.revoke();
  for (const value of [hostile, revoked.proxy, null, {}, { risk: "high" }] as const) {
    assert.doesNotThrow(() => runtimePlan(resolveHighRiskConfirmationPlan, value));
    assert.equal(runtimePlan(resolveHighRiskConfirmationPlan, value).kind, "invalid");
  }
});

test("typed tokens use only the declared safe literal and compare exact code units", async () => {
  const { resolveTypedConfirmationToken, typedConfirmationMatches } = await loadPolicy();
  const resolved = [
    descriptor({ risk: "critical", confirmation: typed({ kind: "target-label" }) }),
    descriptor({ risk: "critical", confirmation: typed({ kind: "public-stable-id" }) }),
    descriptor({ risk: "critical", confirmation: typed({ kind: "fixed-ascii", value: "DELETE NODE_1" }) }),
  ].map(resolveTypedConfirmationToken);
  assert.deepEqual(resolved, [
    { kind: "resolved", value: "Worker Alpha", source: "target-label" },
    { kind: "resolved", value: "worker-alpha", source: "public-stable-id" },
    { kind: "resolved", value: "DELETE NODE_1", source: "fixed-ascii" },
  ]);

  const unsafePublic = [
    "",
    " Worker Alpha",
    "Worker Alpha ",
    "https://public.example",
    "//public.example/path",
    "operator@example.com",
    "operator@localhost",
    "line\nbreak",
    `line${String.fromCodePoint(0x2028)}break`,
    `left${String.fromCodePoint(0x202e)}right`,
    "a".repeat(129),
  ];
  for (const value of unsafePublic) {
    const result = resolveTypedConfirmationToken(descriptor({
      risk: "critical",
      target: { ...target(), publicLabel: value },
      confirmation: typed({ kind: "target-label" }),
    }));
    assert.equal(result.kind, "invalid", JSON.stringify(value));
  }
  for (const value of ["confirm", "HTTPS://EXAMPLE.COM", "A@B.COM", "BAD\nTOKEN", " TOKEN", "TOKEN "]) {
    const result = resolveTypedConfirmationToken(descriptor({
      risk: "critical",
      confirmation: typed({ kind: "fixed-ascii", value }),
    }));
    assert.equal(result.kind, "invalid", value);
  }
  const noFallback = resolveTypedConfirmationToken(descriptor({
    risk: "critical",
    target: { resourceType: "worker", resourceId: "private-resource-id" },
    confirmation: typed({ kind: "target-label" }),
  }));
  assert.equal(noFallback.kind, "invalid");
  assert.equal(JSON.stringify(noFallback).includes("private-resource-id"), false);

  assert.equal(typedConfirmationMatches("Worker Alpha", "Worker Alpha"), true);
  assert.equal(typedConfirmationMatches("worker alpha", "Worker Alpha"), false);
  assert.equal(typedConfirmationMatches("Worker Alpha ", "Worker Alpha"), false);
  assert.equal(typedConfirmationMatches("é", "e\u0301"), false, "Unicode normalization is forbidden");
  assertLiteralOracleRejects((input, token) => input.trim() === token, "Worker Alpha ", "Worker Alpha");
  assertLiteralOracleRejects((input, token) => input.toLowerCase() === token.toLowerCase(), "worker alpha", "Worker Alpha");
});

test("remote-state freshness is data-free, required-section aware, and malformed-safe", async () => {
  const { confirmationFreshnessFromRemoteState } = await loadRevalidation();
  const action = descriptor({ requiredSections: ["health"] });
  const cases: readonly [string, RemoteState<unknown>, object][] = [
    ["initial", { kind: "initial-loading" }, { kind: "unknown", reason: "initial" }],
    ["blocking", { kind: "blocking-error", error: networkError }, { kind: "unknown", reason: "blocking-error" }],
    ["ready fresh", readyState(), { kind: "fresh" }],
    ["ready refreshing", refreshingState(), { kind: "refreshing" }],
    ["ready stale", staleState(), { kind: "stale" }],
    ["empty fresh", { kind: "empty", freshness: freshness("fresh") }, { kind: "fresh" }],
    ["empty refreshing", { kind: "empty", freshness: freshness("refreshing") }, { kind: "refreshing" }],
    ["empty stale", { kind: "empty", freshness: freshness("stale") }, { kind: "stale" }],
    ["partial required missing", {
      kind: "partial",
      data: { private: "not retained" },
      missingSections: ["health"],
      sectionErrors: { health: networkError },
      freshness: freshness("fresh"),
    }, { kind: "unknown", reason: "required-section-missing" }],
    ["partial unrelated missing", {
      kind: "partial",
      data: { private: "not retained" },
      missingSections: ["metrics"],
      sectionErrors: { metrics: networkError },
      freshness: freshness("refreshing"),
    }, { kind: "refreshing" }],
  ];
  for (const [label, state, expected] of cases) {
    const result = confirmationFreshnessFromRemoteState(action, state);
    assert.deepEqual(result, expected, label);
    assert.equal(JSON.stringify(result).includes("private"), false, label);
    assert.equal(JSON.stringify(result).includes("health"), false, label);
  }
  const hostile = new Proxy(readyState(), { get: () => { throw new Error("hostile remote state"); } });
  assert.doesNotThrow(() => runtimeFreshness(confirmationFreshnessFromRemoteState, action, hostile));
  assert.deepEqual(runtimeFreshness(confirmationFreshnessFromRemoteState, action, hostile), {
    kind: "unknown",
    reason: "malformed",
  });
  const malformedStates = [
    { kind: "initial-loading", data: { shouldNotExist: true } },
    { kind: "blocking-error" },
    { kind: "blocking-error", error: {} },
    {
      kind: "ready",
      data: {},
      freshness: { kind: "fresh", lastSuccessAt: 1, error: networkError },
    },
    {
      kind: "ready",
      data: {},
      freshness: { kind: "stale", lastSuccessAt: 1, error: {} },
    },
    {
      kind: "partial",
      data: {},
      missingSections: ["metrics"],
      sectionErrors: [],
      freshness: freshness("fresh"),
    },
    {
      kind: "partial",
      data: {},
      missingSections: ["metrics"],
      sectionErrors: { metrics: {} },
      freshness: freshness("fresh"),
    },
  ];
  for (const state of malformedStates) {
    assert.deepEqual(runtimeFreshness(confirmationFreshnessFromRemoteState, action, state), {
      kind: "unknown",
      reason: "malformed",
    });
  }
});

test("authority snapshots copy, freeze, validate, and retain no hostile source identity", async () => {
  const { copyConfirmationAuthoritySnapshot } = await loadRevalidation();
  const sourceEvaluation = {
    visibility: { kind: "visible" },
    availability: { kind: "allowed" },
  } as const satisfies ActionEvaluation;
  const fieldIds = ["status", "revision"] as [string, ...string[]];
  const source = {
    actionId: "WKR-01",
    targetResourceType: "worker",
    targetResourceId: "worker-alpha",
    evaluation: sourceEvaluation,
    freshness: { kind: "fresh" } as const,
    evidence: { kind: "safe-fingerprint", fieldIds, value: "opaque-value" } as const,
  };
  const copy = copyConfirmationAuthoritySnapshot(source);
  assert.ok(copy);
  assert.deepEqual(copy, source);
  assert.notEqual(copy, source);
  assert.notEqual(copy.evaluation, sourceEvaluation);
  assert.notEqual(copy.evidence, source.evidence);
  if (copy.evidence.kind === "safe-fingerprint") {
    assert.notEqual(copy.evidence.fieldIds, fieldIds);
    assert.equal(Object.isFrozen(copy.evidence.fieldIds), true);
  }
  assert.equal(Object.isFrozen(copy), true);
  assert.equal(Object.isFrozen(copy.evaluation), true);
  assert.equal(Object.isFrozen(copy.freshness), true);
  assert.equal(Object.isFrozen(copy.evidence), true);

  for (const evidence of [
    { kind: "none" } as const,
    { kind: "revision", value: "revision-a" } as const,
  ]) {
    const copied = runtimeSnapshot(copyConfirmationAuthoritySnapshot, { ...source, evidence });
    assert.ok(copied);
    assert.deepEqual(copied.evidence, evidence);
    assert.notEqual(copied.evidence, evidence);
    assert.equal(Object.isFrozen(copied.evidence), true);
  }

  const invalid = [
    { ...source, actionId: "" },
    { ...source, targetResourceId: "bad\nvalue" },
    { ...source, evidence: { kind: "revision", value: Number.NaN } },
    { ...source, evidence: { kind: "safe-fingerprint", fieldIds: [], value: "opaque" } },
    { ...source, evidence: { kind: "safe-fingerprint", fieldIds: ["status", "status"], value: "opaque" } },
    { ...source, evaluation: { visibility: { kind: "visible" }, availability: { kind: "retrying" } } },
  ];
  for (const value of invalid) {
    assert.equal(runtimeSnapshot(copyConfirmationAuthoritySnapshot, value), undefined);
  }
  const hostile = new Proxy(source, { get: () => { throw new Error("hostile snapshot" ); } });
  const revoked = Proxy.revocable(source, {});
  revoked.revoke();
  for (const value of [hostile, revoked.proxy]) {
    assert.doesNotThrow(() => runtimeSnapshot(copyConfirmationAuthoritySnapshot, value));
    assert.equal(runtimeSnapshot(copyConfirmationAuthoritySnapshot, value), undefined);
  }
});
}
