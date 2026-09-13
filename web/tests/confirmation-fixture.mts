import assert from "node:assert/strict";
import { register } from "node:module";
import { fileURLToPath } from "node:url";
import type { ActionDescriptor, ActionEvaluation, ActionRisk, ActionTarget, ConfirmationPolicy, MutationOutcome, RetryPolicy, RevalidationPolicy } from "../src/lib/foundation/actions/contracts.ts";
import type { ConfirmationOutcomePresentation } from "../src/lib/foundation/actions/confirmation-policy.ts";
import type { AdaptedAPIError } from "../src/lib/foundation/api-errors/contracts.ts";
import type { ConfirmationAuthorityEvidence, ConfirmationGateResult } from "../src/lib/foundation/actions/confirmation-revalidation.ts";
import type { RemoteState } from "../src/lib/foundation/remote-state/contracts.ts";


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
  "  if (specifier.startsWith('.') && context.parentURL?.startsWith(webRootURL)) {",
  "    const target = new URL(specifier, context.parentURL);",
  "    if (!/\\.[cm]?[jt]sx?$/.test(target.pathname)) {",
  "      target.pathname += '.ts';",
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

export const webRoot = fileURLToPath(new URL("..", import.meta.url));
export const allowedEvaluation = frozenEvaluation("allowed");
export const deniedEvaluation = frozenEvaluation("denied");
export const unknownEvaluation = Object.freeze({
  visibility: Object.freeze({ kind: "visible" as const }),
  availability: Object.freeze({ kind: "unknown" as const, reasonKey: "actionStateBlocked" as const }),
});
export const conflictError = Object.freeze({
  kind: "conflict",
  messageKey: "apiErrorConflict",
  diagnosticCode: "resource_changed",
} as const satisfies AdaptedAPIError);
export const protectedError = Object.freeze({
  kind: "protected_state",
  messageKey: "apiErrorProtectedState",
} as const satisfies AdaptedAPIError);
export const networkError = Object.freeze({
  kind: "network",
  messageKey: "apiErrorNetwork",
} as const satisfies AdaptedAPIError);

let policyPromise: Promise<PolicyModule> | undefined;
let revalidationPromise: Promise<RevalidationModule> | undefined;
let rendererPromise: Promise<RendererModule> | undefined;
let i18nPromise: Promise<I18nModule> | undefined;

export function loadPolicy() {
  policyPromise ??= import("../src/lib/foundation/actions/confirmation-policy.ts");
  return policyPromise;
}

export function loadRevalidation() {
  revalidationPromise ??= import("../src/lib/foundation/actions/confirmation-revalidation.ts");
  return revalidationPromise;
}

export function loadRenderer() {
  rendererPromise ??= import("../src/components/foundation/confirmation/high-risk-confirmation.ts");
  return rendererPromise;
}

export function loadI18n() {
  i18nPromise ??= import("../src/lib/i18n.ts");
  return i18nPromise;
}

type DescriptorOptions = Readonly<{
  risk?: ActionRisk;
  target?: ActionTarget;
  confirmation?: ConfirmationPolicy;
  retry?: RetryPolicy;
  revalidation?: RevalidationPolicy;
  stateIndependent?: boolean;
  requiredSections?: readonly string[];
}>;

export function descriptor(options: DescriptorOptions = {}): ActionDescriptor {
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

export function target(): ActionTarget {
  return {
    resourceType: "worker",
    resourceId: "worker-alpha",
    publicLabel: "Worker Alpha",
    publicStableId: "worker-alpha",
  };
}

export function consequence(requireSubmitRevalidation: boolean): ConfirmationPolicy {
  return {
    mode: "consequence",
    consequenceKey: "dangerousNotice",
    requireSubmitRevalidation,
  };
}

export function typed(typedToken: Extract<ConfirmationPolicy, { mode: "typed-target" }>["typedToken"]): ConfirmationPolicy {
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

export function freshness(kind: "fresh" | "refreshing" | "stale") {
  if (kind === "stale") return { kind, lastSuccessAt: 1, error: networkError } as const;
  return { kind, lastSuccessAt: 1 } as const;
}

export function readyState(): RemoteState<unknown> {
  return { kind: "ready", data: { value: "not retained" }, freshness: freshness("fresh") };
}

export function refreshingState(): RemoteState<unknown> {
  return { kind: "ready", data: { value: "not retained" }, freshness: freshness("refreshing") };
}

export function staleState(): RemoteState<unknown> {
  return { kind: "ready", data: { value: "not retained" }, freshness: freshness("stale") };
}

export function runtimeRiskDefault(
  fn: PolicyModule["confirmationRiskDefault"],
  value: unknown,
) {
  return Reflect.apply(fn, undefined, [value]);
}

export function runtimePlan(
  fn: PolicyModule["resolveHighRiskConfirmationPlan"],
  value: unknown,
) {
  return Reflect.apply(fn, undefined, [value]);
}

export function runtimeFreshness(
  fn: RevalidationModule["confirmationFreshnessFromRemoteState"],
  action: ActionDescriptor,
  value: unknown,
) {
  return Reflect.apply(fn, undefined, [action, value]);
}

export function runtimeSnapshot(
  fn: RevalidationModule["copyConfirmationAuthoritySnapshot"],
  value: unknown,
) {
  return Reflect.apply(fn, undefined, [value]);
}

export function assertLiteralOracleRejects(
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

export function authority(
  state: RemoteState<unknown>,
  evaluation: ActionEvaluation,
  evidence: ConfirmationAuthorityEvidence,
): ControllerAuthority {
  return Object.freeze({ state, evaluation, evidence });
}

export function matchingAuthority() {
  return authority(readyState(), allowedEvaluation, { kind: "revision", value: 1 });
}

export function createControllerScenario<T>(input: ControllerScenarioInput<T>): ControllerScenario<T> {
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

export async function runControllerOracle<T>(
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

export async function runControllerMutant<T>(
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

export function assertControllerRun(actual: ControllerRun, expected: ExpectedControllerRun) {
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
