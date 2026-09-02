import assert from "node:assert/strict";
import { register } from "node:module";
import test from "node:test";

const resolverSource = [
  "let webRootURL;",
  "export function initialize(data) { webRootURL = data.webRootURL; }",
  "export async function resolve(specifier, context, nextResolve) {",
  "  if (specifier.startsWith('@/')) {",
  "    const target = new URL('src/' + specifier.slice(2), webRootURL);",
  "    if (!/\\.[cm]?[jt]sx?$/.test(target.pathname)) target.pathname += '.ts';",
  "    return nextResolve(target.href, context);",
  "  }",
  "  return nextResolve(specifier, context);",
  "}",
].join("\n");
register(`data:text/javascript,${encodeURIComponent(resolverSource)}`, {
  parentURL: import.meta.url,
  data: { webRootURL: new URL("../", import.meta.url).href },
});

const {
  buildObservabilityActionDescriptor,
  createObservabilityActionController,
  findRefreshedObservabilityPlan,
  observabilityActionPlans,
} = await import("../src/features/observability/action-policy.ts");

test("observability action policy preserves all five exact authorities and state applicability", () => {
  const incident = observabilityActionPlans("/observability/incidents", {
    id: "incident-1",
    status: "open",
    revision: 3,
  });
  const diagnostics = observabilityActionPlans("/observability/diagnostics", {
    id: "diagnostic-1",
    incident_id: "incident-1",
    status: "ready",
    revision: 4,
  });
  const approval = observabilityActionPlans("/observability/remediation-actions", {
    id: "remediation-1",
    incident_id: "incident-1",
    action: "restart encoder",
    mode: "manual",
    status: "pending_approval",
    requires_approval: true,
    revision: 5,
  });
  const execution = observabilityActionPlans("/observability/remediation-actions", {
    id: "remediation-1",
    incident_id: "incident-1",
    action: "restart encoder",
    mode: "manual",
    status: "approved",
    revision: 6,
  });
  const actions = [...incident, ...diagnostics, ...approval, ...execution];

  assert.deepEqual(actions.map((action) => action.id), ["OBS-01", "OBS-02", "OBS-03", "OBS-04", "OBS-05"]);
  assert.deepEqual(actions.map((action) => action.permission), [
    "incidents.acknowledge",
    "incidents.resolve",
    "diagnostics.run",
    "remediation.approve",
    "remediation.execute",
  ]);
  assert.deepEqual(actions.map((action) => action.risk), ["guarded", "high", "guarded", "high", "critical"]);
  assert.deepEqual(actions.map((action) => action.auditAction), [
    "incidents.acknowledge",
    "incidents.resolve",
    "diagnostics.run",
    "remediation.approve",
    "remediation.execute",
  ]);
  assert.equal(execution[0]?.confirmation, "typed-target");
  assert.equal(execution[0]?.targetLabel, "restart encoder");
  assert.equal(buildObservabilityActionDescriptor(execution[0]!).confirmation.mode, "typed-target");
  assert.deepEqual(observabilityActionPlans("/observability/incidents", { id: "incident-1", status: "resolved" }), []);
  assert.deepEqual(observabilityActionPlans("/observability/remediation-actions", {
    id: "remediation-1",
    action: "restart encoder",
    mode: "suggest_only",
    status: "approved",
  }), []);
});

test("observability duplicate and ambiguous outcomes never resend a mutation", async () => {
  const [plan] = observabilityActionPlans("/observability/remediation-actions", {
    id: "remediation-1",
    action: "restart encoder",
    mode: "manual",
    status: "approved",
    revision: 6,
  });
  assert.ok(plan);
  let refreshes = 0;
  let mutations = 0;
  let releaseRefresh!: () => void;
  const refreshGate = new Promise<void>((resolve) => { releaseRefresh = resolve; });
  const controller = createObservabilityActionController({
    refresh: async (candidate) => {
      refreshes += 1;
      await refreshGate;
      return { plan: candidate, evaluation: "allowed", freshness: "fresh" };
    },
    mutate: async () => {
      mutations += 1;
      throw new Error("response lost");
    },
  });

  const first = controller.execute(plan, { confirmed: true, typedValue: plan.targetLabel });
  await new Promise((resolve) => setTimeout(resolve, 0));
  assert.deepEqual(await controller.execute(plan, { confirmed: true, typedValue: plan.targetLabel }), {
    kind: "blocked",
    reason: "duplicate",
  });
  assert.equal(refreshes, 1);
  assert.equal(mutations, 0);
  releaseRefresh();
  assert.deepEqual(await first, { kind: "outcome_unknown" });
  assert.equal(mutations, 1);
  assert.deepEqual(await controller.execute(plan, { confirmed: true, typedValue: plan.targetLabel }), {
    kind: "blocked",
    reason: "outcome-unknown",
  });
  assert.equal(refreshes, 1);
  assert.equal(mutations, 1);

  let mismatchMutations = 0;
  const mismatch = createObservabilityActionController({
    refresh: async (candidate) => ({
      plan: { ...candidate, authorityFingerprint: "changed" },
      evaluation: "allowed",
      freshness: "fresh",
    }),
    mutate: async () => { mismatchMutations += 1; },
  });
  assert.deepEqual(await mismatch.execute(plan, { confirmed: true, typedValue: plan.targetLabel }), {
    kind: "blocked",
    reason: "authority-changed",
  });
  assert.equal(mismatchMutations, 0);
});

test("observability refresh lookup fails closed when state or source identity changes", () => {
  const [original] = observabilityActionPlans("/observability/remediation-actions", {
    id: "remediation-1",
    action: "restart encoder",
    mode: "manual",
    status: "approved",
    revision: 6,
  });
  assert.ok(original);
  assert.equal(findRefreshedObservabilityPlan({ items: [{
    id: "remediation-1",
    action: "restart encoder",
    mode: "manual",
    status: "executed",
    revision: 7,
  }] }, original), undefined);
  assert.equal(findRefreshedObservabilityPlan({ items: [{
    id: "remediation-other",
    action: "restart encoder",
    mode: "manual",
    status: "approved",
    revision: 6,
  }] }, original), undefined);
});
