import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { register } from "node:module";
import { join, resolve } from "node:path";
import test from "node:test";
import { fileURLToPath } from "node:url";

import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import ts from "typescript";

import type {
  ActionDescriptor,
  ActionEvaluation,
  ActionRisk,
  ActionTarget,
  ConfirmationPolicy,
  MutationOutcome,
  RetryPolicy,
  RevalidationPolicy,
} from "../src/lib/foundation/actions/contracts.ts";
import type { ConfirmationOutcomePresentation } from "../src/lib/foundation/actions/confirmation-policy.ts";
import type { AdaptedAPIError } from "../src/lib/foundation/api-errors/contracts.ts";
import type {
  ConfirmationAuthorityEvidence,
  ConfirmationGateResult,
} from "../src/lib/foundation/actions/confirmation-revalidation.ts";
import type { RemoteState } from "../src/lib/foundation/remote-state/contracts.ts";
import type { TranslationKey, TranslationValues } from "../src/lib/i18n.ts";
import { assertConfirmationFoundationBoundaries } from "./helpers/ui-foundation-confirmation-imports.mts";

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

type PolicyModule = typeof import("../src/lib/foundation/actions/confirmation-policy.ts");
type RevalidationModule = typeof import("../src/lib/foundation/actions/confirmation-revalidation.ts");
type RendererModule = typeof import("../src/components/foundation/confirmation/high-risk-confirmation.ts");
type I18nModule = typeof import("../src/lib/i18n.ts");

const webRoot = fileURLToPath(new URL("..", import.meta.url));
const allowedEvaluation = frozenEvaluation("allowed");
const deniedEvaluation = frozenEvaluation("denied");
const unknownEvaluation = Object.freeze({
  visibility: Object.freeze({ kind: "visible" as const }),
  availability: Object.freeze({ kind: "unknown" as const, reasonKey: "actionStateBlocked" as const }),
});
const conflictError = Object.freeze({
  kind: "conflict",
  messageKey: "apiErrorConflict",
  diagnosticCode: "resource_changed",
} as const satisfies AdaptedAPIError);
const protectedError = Object.freeze({
  kind: "protected_state",
  messageKey: "apiErrorProtectedState",
} as const satisfies AdaptedAPIError);
const networkError = Object.freeze({
  kind: "network",
  messageKey: "apiErrorNetwork",
} as const satisfies AdaptedAPIError);

let policyPromise: Promise<PolicyModule> | undefined;
let revalidationPromise: Promise<RevalidationModule> | undefined;
let rendererPromise: Promise<RendererModule> | undefined;
let i18nPromise: Promise<I18nModule> | undefined;

function loadPolicy() {
  policyPromise ??= import("../src/lib/foundation/actions/confirmation-policy.ts");
  return policyPromise;
}

function loadRevalidation() {
  revalidationPromise ??= import("../src/lib/foundation/actions/confirmation-revalidation.ts");
  return revalidationPromise;
}

function loadRenderer() {
  rendererPromise ??= import("../src/components/foundation/confirmation/high-risk-confirmation.ts");
  return rendererPromise;
}

function loadI18n() {
  i18nPromise ??= import("../src/lib/i18n.ts");
  return i18nPromise;
}

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

test("pre-open and pre-submit gates independently re-evaluate permission, freshness, target, and authority", async () => {
  const revalidation = await loadRevalidation();
  const action = descriptor();
  const opened = revalidation.evaluateConfirmationOpen(
    action,
    readyState(),
    allowedEvaluation,
    { kind: "revision", value: 10 },
  );
  assert.equal(opened.kind, "allowed");
  if (opened.kind !== "allowed") return;

  assert.equal(revalidation.evaluateConfirmationOpen(
    action,
    staleState(),
    allowedEvaluation,
    { kind: "revision", value: 10 },
  ).kind, "blocked");
  assert.deepEqual(revalidation.evaluateConfirmationOpen(
    action,
    readyState(),
    deniedEvaluation,
    { kind: "revision", value: 10 },
  ), { kind: "blocked", reason: "not-allowed" });

  const stable = revalidation.evaluateConfirmationSubmit(
    action,
    opened.snapshot,
    readyState(),
    allowedEvaluation,
    { kind: "revision", value: 10 },
  );
  assert.equal(stable.kind, "allowed");
  assert.deepEqual(revalidation.evaluateConfirmationSubmit(
    action,
    opened.snapshot,
    readyState(),
    allowedEvaluation,
    { kind: "revision", value: 11 },
  ), { kind: "blocked", reason: "authority-changed" });
  assert.deepEqual(revalidation.evaluateConfirmationSubmit(
    descriptor({ target: { ...target(), resourceId: "worker-beta" } }),
    opened.snapshot,
    readyState(),
    allowedEvaluation,
    { kind: "revision", value: 10 },
  ), { kind: "blocked", reason: "target-changed" });
  assert.deepEqual(revalidation.evaluateConfirmationSubmit(
    action,
    opened.snapshot,
    readyState(),
    deniedEvaluation,
    { kind: "revision", value: 10 },
  ), { kind: "blocked", reason: "not-allowed" });
  assert.deepEqual(revalidation.evaluateConfirmationSubmit(
    action,
    opened.snapshot,
    refreshingState(),
    allowedEvaluation,
    { kind: "revision", value: 10 },
  ), { kind: "blocked", reason: "freshness-unavailable" });

  assert.deepEqual(revalidation.evaluateConfirmationSubmit(
    { ...action, id: "WKR-02" },
    opened.snapshot,
    readyState(),
    allowedEvaluation,
    { kind: "revision", value: 10 },
  ), { kind: "blocked", reason: "target-changed" });

  const fabricatedStaleOpened = {
    ...opened.snapshot,
    freshness: { kind: "stale" as const },
  };
  assert.deepEqual(revalidation.evaluateConfirmationSubmit(
    action,
    fabricatedStaleOpened,
    readyState(),
    allowedEvaluation,
    { kind: "revision", value: 10 },
  ), { kind: "blocked", reason: "freshness-unavailable" });

  const fingerprintAction = descriptor({
    revalidation: { kind: "safe-fingerprint", fieldIds: ["status", "revision"] },
  });
  const fingerprintOpened = revalidation.evaluateConfirmationOpen(
    fingerprintAction,
    readyState(),
    allowedEvaluation,
    { kind: "safe-fingerprint", fieldIds: ["status", "revision"], value: "fingerprint-a" },
  );
  assert.equal(fingerprintOpened.kind, "allowed");
  if (fingerprintOpened.kind === "allowed") {
    assert.deepEqual(revalidation.evaluateConfirmationSubmit(
      fingerprintAction,
      fingerprintOpened.snapshot,
      readyState(),
      allowedEvaluation,
      { kind: "safe-fingerprint", fieldIds: ["revision", "status"], value: "fingerprint-a" },
    ), { kind: "blocked", reason: "authority-unavailable" });
    assert.deepEqual(revalidation.evaluateConfirmationSubmit(
      fingerprintAction,
      fingerprintOpened.snapshot,
      readyState(),
      allowedEvaluation,
      { kind: "safe-fingerprint", fieldIds: ["status", "revision"], value: "fingerprint-b" },
    ), { kind: "blocked", reason: "authority-changed" });
  }
});

test("controller oracle keeps pre-send mismatch at zero and separates 409 and outcome-unknown counts", async () => {
  const policy = await loadPolicy();
  const revalidation = await loadRevalidation();
  const [{ APIError }, { adaptAPIError }, { defineAPIErrorRegistry }] = await Promise.all([
    import("../src/lib/api/client.ts"),
    import("../src/lib/foundation/api-errors/adapter.ts"),
    import("../src/lib/foundation/api-errors/registry.ts"),
  ]);
  const action = descriptor();
  const adaptedConflict = adaptAPIError(new APIError("raw conflict detail", 409, "state_changed"));
  assert.deepEqual(adaptedConflict, { kind: "conflict", messageKey: "apiErrorConflict" });
  const protectedRegistry = defineAPIErrorRegistry({
    codes: {
      protected_resource: {
        kind: "protected_state",
        messageKey: "apiErrorProtectedState",
        statuses: [409],
      },
    },
  });
  const adaptedProtected = adaptAPIError(
    new APIError("raw protected detail", 409, "protected_resource"),
    { registry: protectedRegistry },
  );
  assert.deepEqual(adaptedProtected, {
    kind: "protected_state",
    messageKey: "apiErrorProtectedState",
    diagnosticCode: "protected_resource",
  });

  for (const [label, openState, openEvaluation] of [
    ["denied", readyState(), deniedEvaluation],
    ["unknown", readyState(), unknownEvaluation],
    ["stale", staleState(), allowedEvaluation],
  ] as const) {
    const scenario = createControllerScenario({
      descriptor: action,
      openAuthority: authority(openState, openEvaluation, { kind: "revision", value: 1 }),
      submitAuthority: authority(readyState(), allowedEvaluation, { kind: "revision", value: 1 }),
      outcome: { kind: "succeeded", value: "unexpected" },
    });
    const run = await runControllerOracle(policy, revalidation, scenario);
    assertControllerRun(run, {
      label: `pre-open ${label}`,
      events: ["open-authority-read"],
      dialogOpened: false,
      gateKind: "blocked",
      mutations: 0,
      refreshes: 0,
      resends: 0,
    });
  }

  const fingerprintAction = descriptor({
    revalidation: { kind: "safe-fingerprint", fieldIds: ["status", "revision"] },
  });
  for (const [label, scenario] of [
    ["revision mismatch", createControllerScenario({
      descriptor: action,
      openAuthority: authority(readyState(), allowedEvaluation, { kind: "revision", value: 1 }),
      submitAuthority: authority(readyState(), allowedEvaluation, { kind: "revision", value: 2 }),
      outcome: { kind: "succeeded", value: "unexpected" },
    })],
    ["fingerprint mismatch", createControllerScenario({
      descriptor: fingerprintAction,
      openAuthority: authority(readyState(), allowedEvaluation, {
        kind: "safe-fingerprint",
        fieldIds: ["status", "revision"],
        value: "fingerprint-a",
      }),
      submitAuthority: authority(readyState(), allowedEvaluation, {
        kind: "safe-fingerprint",
        fieldIds: ["status", "revision"],
        value: "fingerprint-b",
      }),
      outcome: { kind: "succeeded", value: "unexpected" },
    })],
    ["permission mismatch", createControllerScenario({
      descriptor: action,
      openAuthority: authority(readyState(), allowedEvaluation, { kind: "revision", value: 1 }),
      submitAuthority: authority(readyState(), deniedEvaluation, { kind: "revision", value: 1 }),
      outcome: { kind: "succeeded", value: "unexpected" },
    })],
    ["freshness mismatch", createControllerScenario({
      descriptor: action,
      openAuthority: authority(readyState(), allowedEvaluation, { kind: "revision", value: 1 }),
      submitAuthority: authority(staleState(), allowedEvaluation, { kind: "revision", value: 1 }),
      outcome: { kind: "succeeded", value: "unexpected" },
    })],
  ] as const) {
    const run = await runControllerOracle(policy, revalidation, scenario);
    assertControllerRun(run, {
      label,
      events: ["open-authority-read", "submit-authority-read"],
      dialogOpened: true,
      gateKind: "blocked",
      mutations: 0,
      refreshes: 0,
      resends: 0,
    });
  }

  for (const [label, error] of [
    ["ordinary conflict", adaptedConflict],
    ["protected state", adaptedProtected],
  ] as const) {
    const scenario = createControllerScenario({
      descriptor: action,
      openAuthority: matchingAuthority(),
      submitAuthority: matchingAuthority(),
      outcome: { kind: "failed", error },
    });
    const run = await runControllerOracle(policy, revalidation, scenario);
    assertControllerRun(run, {
      label,
      events: ["open-authority-read", "submit-authority-read", "mutation", "resource-refresh"],
      dialogOpened: true,
      gateKind: "allowed",
      outcomeKind: "conflict",
      mutations: 1,
      refreshes: 1,
      resends: 0,
    });
    assert.equal(run.outcome?.kind === "conflict" ? run.outcome.error.kind : undefined, error.kind);
  }

  for (const nextAction of ["refresh-resource", "inspect-audit", "contact-operator"] as const) {
    const scenario = createControllerScenario({
      descriptor: action,
      openAuthority: matchingAuthority(),
      submitAuthority: matchingAuthority(),
      outcome: { kind: "outcome_unknown", safeReference: "must-not-render", nextAction },
    });
    const run = await runControllerOracle(policy, revalidation, scenario);
    assertControllerRun(run, {
      label: `outcome unknown ${nextAction}`,
      events: ["open-authority-read", "submit-authority-read", "mutation"],
      dialogOpened: true,
      gateKind: "allowed",
      outcomeKind: "outcome-unknown",
      nextAction,
      mutations: 1,
      refreshes: 0,
      resends: 0,
    });
  }

  const ordinaryFailure = adaptAPIError(new TypeError("raw native network message"));
  for (const [label, outcome, outcomeKind] of [
    ["success", { kind: "succeeded", value: "ok" } as const, "succeeded"],
    ["ordinary failure", { kind: "failed", error: ordinaryFailure } as const, "failed"],
  ] as const) {
    const scenario = createControllerScenario({
      descriptor: action,
      openAuthority: matchingAuthority(),
      submitAuthority: matchingAuthority(),
      outcome,
    });
    const run = await runControllerOracle(policy, revalidation, scenario);
    assertControllerRun(run, {
      label,
      events: ["open-authority-read", "submit-authority-read", "mutation"],
      dialogOpened: true,
      gateKind: "allowed",
      outcomeKind,
      mutations: 1,
      refreshes: 0,
      resends: 0,
    });
  }

  for (const [label, mutant, outcome, expectedEvents, expectedOutcome] of [
    ["claimed mutation without callback", "claim-mutation", { kind: "succeeded", value: "ok" },
      ["open-authority-read", "submit-authority-read", "mutation"], "succeeded"],
    ["mutation called twice", "double-mutation", { kind: "succeeded", value: "ok" },
      ["open-authority-read", "submit-authority-read", "mutation"], "succeeded"],
    ["conflict refresh omitted", "omit-conflict-refresh", { kind: "failed", error: adaptedConflict },
      ["open-authority-read", "submit-authority-read", "mutation", "resource-refresh"], "conflict"],
    ["conflict refresh called twice", "double-conflict-refresh", { kind: "failed", error: adaptedConflict },
      ["open-authority-read", "submit-authority-read", "mutation", "resource-refresh"], "conflict"],
    ["mutation called again after conflict", "retry-conflict", { kind: "failed", error: adaptedConflict },
      ["open-authority-read", "submit-authority-read", "mutation", "resource-refresh"], "conflict"],
    ["outcome unknown automatically retried", "retry-outcome-unknown", {
      kind: "outcome_unknown", safeReference: "must-not-render", nextAction: "inspect-audit",
    }, ["open-authority-read", "submit-authority-read", "mutation"], "outcome-unknown"],
    ["cached open result skips submit authority", "cached-open-submit", { kind: "succeeded", value: "ok" },
      ["open-authority-read", "submit-authority-read", "mutation"], "succeeded"],
  ] as const) {
    const scenario = createControllerScenario({
      descriptor: action,
      openAuthority: matchingAuthority(),
      submitAuthority: matchingAuthority(),
      outcome,
    });
    const run = await runControllerMutant(policy, revalidation, scenario, mutant);
    assert.throws(() => assertControllerRun(run, {
      label,
      events: expectedEvents,
      dialogOpened: true,
      gateKind: "allowed",
      outcomeKind: expectedOutcome,
      mutations: 1,
      refreshes: expectedOutcome === "conflict" ? 1 : 0,
      resends: 0,
    }), /controller events/);
  }
});

test("outcome presentation is pure, bounded, and never propagates safeReference", async () => {
  const { confirmationOutcomePresentation } = await loadPolicy();
  assert.deepEqual(confirmationOutcomePresentation({ kind: "succeeded", value: "ok" }), { kind: "succeeded" });
  assert.deepEqual(confirmationOutcomePresentation({ kind: "failed", error: conflictError }), {
    kind: "conflict",
    error: conflictError,
    refreshRequired: true,
  });
  assert.deepEqual(confirmationOutcomePresentation({ kind: "failed", error: protectedError }), {
    kind: "conflict",
    error: protectedError,
    refreshRequired: true,
  });
  assert.deepEqual(confirmationOutcomePresentation({ kind: "failed", error: networkError }), {
    kind: "failed",
    error: networkError,
  });
  for (const nextAction of ["refresh-resource", "inspect-audit", "contact-operator"] as const) {
    const result = confirmationOutcomePresentation({
      kind: "outcome_unknown",
      safeReference: "secret-adjacent-reference",
      nextAction,
    });
    assert.deepEqual(result, { kind: "outcome-unknown", nextAction });
    assert.equal(JSON.stringify(result).includes("safeReference"), false);
    assert.equal(JSON.stringify(result).includes("secret-adjacent"), false);
  }
});

test("real confirmation body renders only safe translated presentation data", async () => {
  const { HighRiskConfirmationBody, HighRiskConfirmation } = await loadRenderer();
  const { translate } = await loadI18n();
  const action = descriptor({
    risk: "critical",
    confirmation: typed({ kind: "target-label" }),
    target: {
      resourceType: "worker",
      resourceId: "private-worker-id-9f",
      publicLabel: "Worker Alpha",
      publicStableId: "worker-alpha",
    },
  });
  const markup = renderToStaticMarkup(createElement(HighRiskConfirmationBody, {
    descriptor: action,
    state: { kind: "ready" },
    context: {
      currentState: { key: "status" },
      impact: { key: "dangerousNotice" },
      rollback: { key: "confirmationRefreshRequired" },
      credentialEffect: { key: "confirmationCredentialEffectHeading" },
    },
    translate: (key: TranslationKey, values?: TranslationValues) => translate("en", key, values),
    typedInput: "wrong",
    onTypedInputChange: () => {},
  }));
  assert.match(markup, /Worker Alpha/);
  assert.match(markup, /Type &quot;Worker Alpha&quot; to confirm\./);
  assert.match(markup, /Impact/);
  assert.match(markup, /Recovery and rollback/);
  assert.match(markup, /Credential and capability impact/);
  assert.match(markup, /Audit record/);
  assert.match(markup, /aria-describedby=/);
  assert.match(markup, /autoComplete="off"/);
  assert.match(markup, /spellCheck="false"/);
  assert.equal(markup.includes("private-worker-id-9f"), false);
  assert.equal(markup.includes("resource_changed"), false);
  assert.equal(markup.includes("revision"), false);

  const invalidMarkup = renderToStaticMarkup(createElement(HighRiskConfirmation, {
    descriptor: descriptor({ risk: "critical", confirmation: consequence(true) }),
    open: false,
    evaluation: allowedEvaluation,
    state: { kind: "ready" },
    translate: (key: TranslationKey, values?: TranslationValues) => translate("en", key, values),
    trigger: (props: Readonly<{ disabled: boolean; "aria-disabled"?: true }>) =>
      createElement("button", { type: "button", ...props }, "Open"),
    onOpenIntent: () => { throw new Error("invalid plan opened"); },
    onCloseIntent: () => {},
    onConfirmIntent: () => { throw new Error("invalid plan confirmed"); },
  }));
  assert.match(invalidMarkup, /disabled=""/);
  assert.match(invalidMarkup, /aria-disabled="true"/);
});

test("B-05 i18n copy is exact, parallel, placeholder-bounded, and token-literal", async () => {
  const { translations, translate } = await loadI18n();
  const expected = {
    confirmationImpactHeading: ["影響範囲", "Impact"],
    confirmationRollbackHeading: ["復旧・取り消し", "Recovery and rollback"],
    confirmationCredentialEffectHeading: ["認証情報・公開リンクへの影響", "Credential and capability impact"],
    confirmationAuditHeading: ["監査記録", "Audit record"],
    confirmationTypeTokenInstruction: ["確認のため「{token}」と入力してください。", "Type \"{token}\" to confirm."],
    confirmationTokenInputLabel: ["確認用文字列", "Confirmation text"],
    confirmationTypedTokenMismatch: ["入力内容が確認用文字列と一致しません。", "The entered text does not match the confirmation text."],
    confirmationRevalidating: ["最新の権限と状態を確認しています。", "Checking the latest permissions and state."],
    confirmationStaleBlocked: ["対象の状態が変更されたため、操作を実行しませんでした。最新情報を確認してください。", "The target changed, so the action was not sent. Review the latest state."],
    confirmationRevalidationUnavailable: ["最新の権限または状態を確認できないため、操作を実行できません。", "The action cannot be sent because the latest permissions or state could not be verified."],
    confirmationSubmitting: ["操作を実行しています。", "Performing the action."],
    confirmationOutcomeUnknown: ["操作結果を確認できません。再送せず、対象の最新状態または監査ログを確認してください。", "The result could not be confirmed. Do not resend the action; check the latest target state or audit log."],
    confirmationRefreshRequired: ["最新情報を確認してから、もう一度操作してください。", "Review the latest information before trying again."],
  } as const;
  for (const [key, [ja, en]] of Object.entries(expected)) {
    assert.equal(translations.ja[key as TranslationKey], ja);
    assert.equal(translations.en[key as TranslationKey], en);
    const placeholders = [...ja.matchAll(/\{([a-zA-Z0-9_]+)\}/g), ...en.matchAll(/\{([a-zA-Z0-9_]+)\}/g)]
      .map((match) => match[1]);
    assert.equal(placeholders.every((placeholder) => placeholder === "token"), true, key);
  }
  assert.equal(Object.keys(expected).length, 13);
  assert.equal(translate("ja", "confirmationTypeTokenInstruction", { token: "DELETE NODE_1" }), "確認のため「DELETE NODE_1」と入力してください。");
  assert.equal(translate("en", "confirmationTypeTokenInstruction", { token: "DELETE NODE_1" }), "Type \"DELETE NODE_1\" to confirm.");
});

test("AST guard rejects retired confirmation definitions and callers while preserving Foundation owners", () => {
  assert.deepEqual(assertConfirmationFoundationBoundaries(webRoot), {
    dangerConsumerCount: 0,
    frameConsumerCount: 5,
    rendererConsumerCount: 9,
    reviewedFileCount: 4,
  });
  assert.throws(() => assertConfirmationFoundationBoundaries(webRoot, new Map([
    ["src/components/admin/danger-confirm.tsx", "export function DangerConfirm() { return null; }"],
  ])), /definition must remain removed/);
  assert.throws(() => assertConfirmationFoundationBoundaries(webRoot, new Map([
    ["src/features/synthetic/new-action.ts", 'import { DangerConfirm } from "@/components/admin/danger-confirm";\nvoid DangerConfirm;\n'],
  ])), /DangerConfirm|consumers/);
  assert.throws(() => assertConfirmationFoundationBoundaries(webRoot, new Map([
    ["src/features/synthetic/new-action.ts", 'import { ConfirmationDialogFrame } from "@/components/foundation/confirmation/confirmation-dialog-frame";\nvoid ConfirmationDialogFrame;\n'],
  ])), /exactly the reviewed owners/);
  assert.throws(() => assertConfirmationFoundationBoundaries(webRoot, new Map([
    ["src/features/synthetic/new-action.ts", 'import { HighRiskConfirmation } from "@/components/foundation/confirmation/high-risk-confirmation";\nvoid HighRiskConfirmation;\n'],
  ])), /exactly the reviewed migrated consumers/);
  const rendererPath = join(webRoot, "src", "components", "foundation", "confirmation", "high-risk-confirmation.ts");
  const rendererSource = readFileSync(rendererPath, "utf8");
  assert.throws(() => assertConfirmationFoundationBoundaries(webRoot, new Map([
    ["src/components/foundation/confirmation/high-risk-confirmation.ts", `${rendererSource}\nimport { apiClient } from "@/lib/api/client";\nvoid apiClient;\n`],
  ])), /forbidden|API\/query\/router/);
});

test("type negative matrix is mutation-sensitive and reports TS2578 when an invalid use becomes valid", () => {
  const configPath = join(webRoot, "tsconfig.json");
  const typeTestPath = resolve(webRoot, "tests", "ui-foundation-confirmation.type-test.ts");
  const configRead = ts.readConfigFile(configPath, ts.sys.readFile);
  assert.equal(configRead.error, undefined);
  const config = ts.parseJsonConfigFileContent(configRead.config, ts.sys, webRoot);
  const original = readFileSync(typeTestPath, "utf8");
  const mutant = original.replace(
    'confirmationRiskDefault("severe")',
    'confirmationRiskDefault("high")',
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

type DescriptorOptions = Readonly<{
  risk?: ActionRisk;
  target?: ActionTarget;
  confirmation?: ConfirmationPolicy;
  retry?: RetryPolicy;
  revalidation?: RevalidationPolicy;
  stateIndependent?: boolean;
  requiredSections?: readonly string[];
}>;

function descriptor(options: DescriptorOptions = {}): ActionDescriptor {
  return {
    id: "WKR-01",
    labelKey: "restart",
    risk: options.risk ?? "high",
    target: options.target ?? target(),
    permissions: { kind: "all", permissions: ["workers.restart"] },
    applicability: {
      ruleIds: ["worker-restartable"],
      requiredSections: options.requiredSections ?? ["health"],
    },
    confirmation: options.confirmation ?? consequence(true),
    duplicate: { scope: "resource-action", whilePending: "block" },
    retry: options.retry ?? { kind: "never" },
    audit: { action: "workers.restart", labelKey: "auditLogs", safeReferenceFieldIds: [] },
    stateIndependent: options.stateIndependent ?? false,
    ...(Object.prototype.hasOwnProperty.call(options, "revalidation")
      ? { revalidation: options.revalidation }
      : { revalidation: { kind: "revision" } }),
  };
}

function target(): ActionTarget {
  return {
    resourceType: "worker",
    resourceId: "worker-alpha",
    publicLabel: "Worker Alpha",
    publicStableId: "worker-alpha",
  };
}

function consequence(requireSubmitRevalidation: boolean): ConfirmationPolicy {
  return {
    mode: "consequence",
    consequenceKey: "dangerousNotice",
    requireSubmitRevalidation,
  };
}

function typed(typedToken: Extract<ConfirmationPolicy, { mode: "typed-target" }>["typedToken"]): ConfirmationPolicy {
  return {
    mode: "typed-target",
    consequenceKey: "dangerousNotice",
    typedToken,
    requireSubmitRevalidation: true,
  };
}

function frozenEvaluation(kind: "allowed" | "denied"): ActionEvaluation {
  return kind === "allowed"
    ? Object.freeze({
        visibility: Object.freeze({ kind: "visible" as const }),
        availability: Object.freeze({ kind: "allowed" as const }),
      })
    : Object.freeze({
        visibility: Object.freeze({ kind: "visible" as const }),
        availability: Object.freeze({ kind: "denied" as const, reasonKey: "actionPermissionDenied" as const }),
      });
}

function freshness(kind: "fresh" | "refreshing" | "stale") {
  if (kind === "stale") return { kind, lastSuccessAt: 1, error: networkError } as const;
  return { kind, lastSuccessAt: 1 } as const;
}

function readyState(): RemoteState<unknown> {
  return { kind: "ready", data: { value: "not retained" }, freshness: freshness("fresh") };
}

function refreshingState(): RemoteState<unknown> {
  return { kind: "ready", data: { value: "not retained" }, freshness: freshness("refreshing") };
}

function staleState(): RemoteState<unknown> {
  return { kind: "ready", data: { value: "not retained" }, freshness: freshness("stale") };
}

function runtimeRiskDefault(
  fn: PolicyModule["confirmationRiskDefault"],
  value: unknown,
) {
  return Reflect.apply(fn, undefined, [value]);
}

function runtimePlan(
  fn: PolicyModule["resolveHighRiskConfirmationPlan"],
  value: unknown,
) {
  return Reflect.apply(fn, undefined, [value]);
}

function runtimeFreshness(
  fn: RevalidationModule["confirmationFreshnessFromRemoteState"],
  action: ActionDescriptor,
  value: unknown,
) {
  return Reflect.apply(fn, undefined, [action, value]);
}

function runtimeSnapshot(
  fn: RevalidationModule["copyConfirmationAuthoritySnapshot"],
  value: unknown,
) {
  return Reflect.apply(fn, undefined, [value]);
}

function assertLiteralOracleRejects(
  mutant: (input: string, requiredToken: string) => boolean,
  input: string,
  requiredToken: string,
) {
  assert.throws(() => {
    assert.equal(mutant(input, requiredToken), false, "mutant accepted a non-literal confirmation");
  }, /mutant accepted/);
}

type ControllerEvent =
  | "open-authority-read"
  | "submit-authority-read"
  | "mutation"
  | "resource-refresh"
  | "resend";

type ControllerAuthority = Readonly<{
  state: RemoteState<unknown>;
  evaluation: ActionEvaluation;
  evidence: ConfirmationAuthorityEvidence;
}>;

type ControllerScenario<T> = Readonly<{
  events: readonly ControllerEvent[];
  descriptor: ActionDescriptor;
  readOpenAuthority: () => ControllerAuthority;
  readSubmitAuthority: () => ControllerAuthority;
  invokeMutation: () => Promise<MutationOutcome<T>>;
  refreshResource: () => Promise<void>;
  invokeResend: () => Promise<void>;
}>;

type ControllerScenarioInput<T> = Readonly<{
  descriptor: ActionDescriptor;
  openAuthority: ControllerAuthority;
  submitAuthority: ControllerAuthority;
  outcome: MutationOutcome<T>;
}>;

type ControllerRun = Readonly<{
  events: readonly ControllerEvent[];
  dialogOpened: boolean;
  gate: ConfirmationGateResult;
  outcome?: ConfirmationOutcomePresentation;
}>;

type ExpectedControllerRun = Readonly<{
  label: string;
  events: readonly ControllerEvent[];
  dialogOpened: boolean;
  gateKind: ConfirmationGateResult["kind"];
  outcomeKind?: ConfirmationOutcomePresentation["kind"];
  nextAction?: Extract<ConfirmationOutcomePresentation, { kind: "outcome-unknown" }>["nextAction"];
  mutations: number;
  refreshes: number;
  resends: number;
}>;

type ControllerMutant =
  | "claim-mutation"
  | "double-mutation"
  | "omit-conflict-refresh"
  | "double-conflict-refresh"
  | "retry-conflict"
  | "retry-outcome-unknown"
  | "cached-open-submit";

function authority(
  state: RemoteState<unknown>,
  evaluation: ActionEvaluation,
  evidence: ConfirmationAuthorityEvidence,
): ControllerAuthority {
  return Object.freeze({ state, evaluation, evidence });
}

function matchingAuthority() {
  return authority(readyState(), allowedEvaluation, { kind: "revision", value: 1 });
}

function createControllerScenario<T>(input: ControllerScenarioInput<T>): ControllerScenario<T> {
  const events: ControllerEvent[] = [];
  return Object.freeze({
    events,
    descriptor: input.descriptor,
    readOpenAuthority: () => {
      events.push("open-authority-read");
      return input.openAuthority;
    },
    readSubmitAuthority: () => {
      events.push("submit-authority-read");
      return input.submitAuthority;
    },
    invokeMutation: async () => {
      events.push("mutation");
      return input.outcome;
    },
    refreshResource: async () => {
      events.push("resource-refresh");
    },
    invokeResend: async () => {
      events.push("resend");
    },
  });
}

async function runControllerOracle<T>(
  policy: PolicyModule,
  revalidation: RevalidationModule,
  scenario: ControllerScenario<T>,
): Promise<ControllerRun> {
  const openAuthority = scenario.readOpenAuthority();
  const opened = revalidation.evaluateConfirmationOpen(
    scenario.descriptor,
    openAuthority.state,
    openAuthority.evaluation,
    openAuthority.evidence,
  );
  if (opened.kind === "blocked") {
    return finishControllerRun(scenario, false, opened);
  }
  const submitAuthority = scenario.readSubmitAuthority();
  const submitted = revalidation.evaluateConfirmationSubmit(
    scenario.descriptor,
    opened.snapshot,
    submitAuthority.state,
    submitAuthority.evaluation,
    submitAuthority.evidence,
  );
  if (submitted.kind === "blocked") {
    return finishControllerRun(scenario, true, submitted);
  }
  const presentation = policy.confirmationOutcomePresentation(await scenario.invokeMutation());
  if (presentation.kind === "conflict") await scenario.refreshResource();
  return finishControllerRun(scenario, true, submitted, presentation);
}

async function runControllerMutant<T>(
  policy: PolicyModule,
  revalidation: RevalidationModule,
  scenario: ControllerScenario<T>,
  mutant: ControllerMutant,
): Promise<ControllerRun & Readonly<{ claimedMutationCount?: number }>> {
  const openAuthority = scenario.readOpenAuthority();
  const opened = revalidation.evaluateConfirmationOpen(
    scenario.descriptor,
    openAuthority.state,
    openAuthority.evaluation,
    openAuthority.evidence,
  );
  if (opened.kind === "blocked") return finishControllerRun(scenario, false, opened);

  let submitted: ConfirmationGateResult = opened;
  if (mutant !== "cached-open-submit") {
    const submitAuthority = scenario.readSubmitAuthority();
    submitted = revalidation.evaluateConfirmationSubmit(
      scenario.descriptor,
      opened.snapshot,
      submitAuthority.state,
      submitAuthority.evaluation,
      submitAuthority.evidence,
    );
  }
  if (submitted.kind === "blocked") return finishControllerRun(scenario, true, submitted);
  if (mutant === "claim-mutation") {
    return Object.freeze({ ...finishControllerRun(scenario, true, submitted), claimedMutationCount: 1 });
  }

  const presentation = policy.confirmationOutcomePresentation(await scenario.invokeMutation());
  if (mutant === "double-mutation") await scenario.invokeMutation();
  if (presentation.kind === "conflict" && mutant !== "omit-conflict-refresh") {
    await scenario.refreshResource();
  }
  if (mutant === "double-conflict-refresh") await scenario.refreshResource();
  if (mutant === "retry-conflict" || mutant === "retry-outcome-unknown") {
    await scenario.invokeResend();
    await scenario.invokeMutation();
  }
  return finishControllerRun(scenario, true, submitted, presentation);
}

function finishControllerRun<T>(
  scenario: ControllerScenario<T>,
  dialogOpened: boolean,
  gate: ConfirmationGateResult,
  outcome?: ConfirmationOutcomePresentation,
): ControllerRun {
  return Object.freeze({
    events: Object.freeze([...scenario.events]),
    dialogOpened,
    gate,
    ...(outcome ? { outcome } : {}),
  });
}

function assertControllerRun(actual: ControllerRun, expected: ExpectedControllerRun) {
  assert.deepEqual(actual.events, expected.events, `${expected.label} controller events`);
  assert.equal(actual.dialogOpened, expected.dialogOpened, `${expected.label} dialog state`);
  assert.equal(actual.gate.kind, expected.gateKind, `${expected.label} gate`);
  assert.equal(actual.outcome?.kind, expected.outcomeKind, `${expected.label} outcome`);
  if (expected.nextAction) {
    assert.equal(
      actual.outcome?.kind === "outcome-unknown" ? actual.outcome.nextAction : undefined,
      expected.nextAction,
      `${expected.label} next action`,
    );
  }
  assert.equal(eventCount(actual.events, "mutation"), expected.mutations, `${expected.label} mutation callbacks`);
  assert.equal(eventCount(actual.events, "resource-refresh"), expected.refreshes, `${expected.label} refresh callbacks`);
  assert.equal(eventCount(actual.events, "resend"), expected.resends, `${expected.label} resend callbacks`);
}

function eventCount(events: readonly ControllerEvent[], expected: ControllerEvent) {
  return events.filter((event) => event === expected).length;
}
