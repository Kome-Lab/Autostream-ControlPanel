import assert from "node:assert/strict";
import test from "node:test";
import { loadRevalidation, descriptor, readyState, allowedEvaluation, staleState, deniedEvaluation, target, refreshingState, loadPolicy, unknownEvaluation, createControllerScenario, authority, runControllerOracle, assertControllerRun, matchingAuthority, runControllerMutant } from "./confirmation-fixture.mts";


export function registerConfirmationControllerCases() {


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
}
