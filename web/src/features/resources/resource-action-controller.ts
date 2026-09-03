import {
  buildResourceActionDescriptor,
  resourceActionApplicable,
  resourceActionRequest,
  resourceActionScope,
  type ResourceActionIntent,
  type ResourceActionRequest,
} from "@/features/resources/resource-action-descriptors";
import type { ActionEvaluation } from "@/lib/foundation/actions/contracts";
import { adaptAPIError } from "@/lib/foundation/api-errors/adapter";
import type { AdaptedAPIError } from "@/lib/foundation/api-errors/contracts";
import { evaluateActionPermission, type PermissionSnapshot } from "@/lib/foundation/permissions/evaluator";

export type ResourceActionStateSnapshot = Readonly<{
  kind: "ready" | "missing" | "unknown";
  freshness: "fresh" | "refreshing" | "stale" | "unavailable";
  fingerprint?: string;
}>;

export type ResourceActionAuthority = Readonly<{
  intent: ResourceActionIntent;
  permissions: PermissionSnapshot;
  state: ResourceActionStateSnapshot;
}>;

export type ResourceActionOpenResult =
  | Readonly<{
      kind: "allowed";
      intent: ResourceActionIntent;
      descriptor: NonNullable<ReturnType<typeof buildResourceActionDescriptor>>;
      authority: string;
      typedToken?: string;
    }>
  | Readonly<{ kind: "blocked"; reason: ResourceActionBlockedReason }>;

export type AllowedResourceActionOpen = Extract<ResourceActionOpenResult, { kind: "allowed" }>;

export type ResourceActionExecutionResult =
  | Readonly<{ kind: "succeeded"; value: unknown }>
  | Readonly<{ kind: "failed"; error: AdaptedAPIError }>
  | Readonly<{ kind: "outcome_unknown"; nextAction: "refresh-resource" | "inspect-audit" }>
  | Readonly<{ kind: "blocked"; reason: ResourceActionBlockedReason }>;

export type ResourceActionBlockedReason =
  | "invalid-intent" | "permission-denied" | "permission-unknown" | "not-applicable"
  | "state-unavailable" | "duplicate" | "confirmation-required" | "typed-target-mismatch"
  | "authority-changed" | "reconciliation-required" | "cancelled";

export type ResourceActionController = Readonly<{
  evaluate: (intent: ResourceActionIntent) => ActionEvaluation;
  open: (intent: ResourceActionIntent) => Promise<ResourceActionOpenResult>;
  submit: (
    opened: AllowedResourceActionOpen,
    confirmation: Readonly<{ confirmed: boolean; typedValue?: string }>,
    lifecycle?: ResourceActionSubmitLifecycle,
  ) => Promise<ResourceActionExecutionResult>;
  cancel: (intent?: ResourceActionIntent) => void;
  reconcile: (intent?: ResourceActionIntent) => void;
  isPending: (intent: ResourceActionIntent) => boolean;
}>;

export type ResourceActionSubmitLifecycle = Readonly<{
  onDispatch?: () => void;
}>;

export type ResourceActionControllerDependencies = Readonly<{
  getPermissions: () => PermissionSnapshot;
  getState: (intent: ResourceActionIntent) => ResourceActionStateSnapshot;
  refresh: (intent: ResourceActionIntent) => Promise<ResourceActionAuthority>;
  mutate: (request: ResourceActionRequest & Readonly<{ signal: AbortSignal }>) => Promise<unknown>;
}>;

type ActiveOperation = { generation: number; abort: AbortController; released: boolean };

export function createResourceActionController(dependencies: ResourceActionControllerDependencies): ResourceActionController {
  const active = new Map<string, ActiveOperation>();
  const generations = new Map<string, number>();
  const unresolved = new Set<string>();

  const evaluateBase = (
    intent: ResourceActionIntent,
    authority?: Pick<ResourceActionAuthority, "permissions" | "state">,
  ): ActionEvaluation => {
    const descriptor = buildResourceActionDescriptor(intent);
    if (!descriptor || !resourceActionRequest(intent) || !resourceActionApplicable(intent)) return notApplicable();
    const state = authority?.state ?? dependencies.getState(intent);
    const constraint = state.kind === "missing"
      ? { kind: "not-applicable" as const, reasonKey: "actionStateBlocked" as const }
      : state.kind !== "ready" || state.freshness !== "fresh" || !state.fingerprint
        ? { kind: "unknown" as const, reasonKey: "actionStateBlocked" as const }
        : { kind: "ready" as const };
    return evaluateActionPermission({
      requirement: descriptor.permissions ?? { kind: "none" },
      snapshot: authority?.permissions ?? dependencies.getPermissions(),
      disclosure: "visible-denied",
      constraint,
    });
  };

  const evaluate = (intent: ResourceActionIntent): ActionEvaluation => {
    const value = evaluateBase(intent);
    const scope = resourceActionScope(intent);
    if (!scope || value.availability.kind !== "allowed") return value;
    if (active.has(scope)) return pending(value);
    if (unresolved.has(scope)) return reconciliationBlocked(value);
    return value;
  };

  const open = async (intent: ResourceActionIntent): Promise<ResourceActionOpenResult> => {
    const scope = resourceActionScope(intent);
    if (!scope) return blocked("invalid-intent");
    if (active.has(scope)) return blocked("duplicate");
    if (unresolved.has(scope)) return blocked("reconciliation-required");
    let authority: ResourceActionAuthority;
    try {
      authority = await dependencies.refresh(intent);
    } catch {
      return blocked("state-unavailable");
    }
    const reason = evaluationReason(evaluateBase(authority.intent, authority));
    if (reason) return blocked(reason);
    const descriptor = buildResourceActionDescriptor(authority.intent);
    if (!descriptor || authority.state.kind !== "ready" || authority.state.freshness !== "fresh" || !authority.state.fingerprint) {
      return blocked("state-unavailable");
    }
    const typedToken = descriptor.confirmation.mode === "typed-target"
      ? descriptor.confirmation.typedToken.kind === "fixed-ascii"
        ? descriptor.confirmation.typedToken.value
        : descriptor.target.publicLabel
      : undefined;
    if (descriptor.confirmation.mode === "typed-target" && !typedToken) return blocked("invalid-intent");
    return Object.freeze({
      kind: "allowed",
      intent: authority.intent,
      descriptor,
      authority: authority.state.fingerprint,
      ...(typedToken === undefined ? {} : { typedToken }),
    });
  };

  const submit = async (
    opened: AllowedResourceActionOpen,
    confirmation: Readonly<{ confirmed: boolean; typedValue?: string }>,
    lifecycle?: ResourceActionSubmitLifecycle,
  ): Promise<ResourceActionExecutionResult> => {
    const scope = resourceActionScope(opened.intent);
    if (!scope) return blocked("invalid-intent");
    if (unresolved.has(scope)) return blocked("reconciliation-required");
    if (opened.descriptor.confirmation.mode !== "none" && !confirmation.confirmed) return blocked("confirmation-required");
    if (opened.typedToken !== undefined && confirmation.typedValue !== opened.typedToken) return blocked("typed-target-mismatch");
    const operation = acquire(scope);
    if (!operation) return blocked("duplicate");
    try {
      let authority: ResourceActionAuthority;
      try {
        authority = await dependencies.refresh(opened.intent);
      } catch {
        return blocked("state-unavailable");
      }
      const reason = evaluationReason(evaluateBase(authority.intent, authority));
      if (reason) return blocked(reason);
      if (authority.state.kind !== "ready" || authority.state.freshness !== "fresh" || !authority.state.fingerprint) {
        return blocked("state-unavailable");
      }
      if (authority.state.fingerprint !== opened.authority) return blocked("authority-changed");
      const request = resourceActionRequest(authority.intent);
      if (!request || !current(scope, operation)) return blocked("cancelled");
      try {
        const execution = dependencies.mutate(Object.freeze({ ...request, signal: operation.abort.signal }));
        try {
          lifecycle?.onDispatch?.();
        } catch {
          // A UI cleanup callback must not alter the mutation outcome after dispatch.
        }
        const value = await execution;
        if (!current(scope, operation)) return blocked("cancelled");
        return Object.freeze({ kind: "succeeded", value });
      } catch (error) {
        if (!current(scope, operation)) return blocked("cancelled");
        const adapted = adaptAPIError(error);
        unresolved.add(scope);
        if (ambiguous(adapted)) {
          return Object.freeze({ kind: "outcome_unknown", nextAction: "inspect-audit" });
        }
        return Object.freeze({ kind: "failed", error: adapted });
      }
    } finally {
      release(scope, operation);
    }
  };

  function acquire(scope: string): ActiveOperation | undefined {
    if (active.has(scope)) return undefined;
    const operation = { generation: (generations.get(scope) ?? 0) + 1, abort: new AbortController(), released: false };
    generations.set(scope, operation.generation);
    active.set(scope, operation);
    return operation;
  }

  function current(scope: string, operation: ActiveOperation) {
    return !operation.abort.signal.aborted && active.get(scope) === operation && generations.get(scope) === operation.generation;
  }

  function release(scope: string, operation: ActiveOperation) {
    if (operation.released) return;
    operation.released = true;
    if (active.get(scope) === operation) active.delete(scope);
  }

  return Object.freeze({
    evaluate,
    open,
    submit,
    cancel: (intent) => {
      const scopes = intent ? [resourceActionScope(intent)].filter((value): value is string => Boolean(value)) : [...active.keys()];
      for (const scope of scopes) {
        const operation = active.get(scope);
        if (!operation) continue;
        operation.abort.abort();
        generations.set(scope, operation.generation + 1);
        release(scope, operation);
      }
    },
    reconcile: (intent) => {
      if (!intent) {
        unresolved.clear();
        return;
      }
      const scope = resourceActionScope(intent);
      if (scope) unresolved.delete(scope);
    },
    isPending: (intent) => {
      const scope = resourceActionScope(intent);
      return Boolean(scope && active.has(scope));
    },
  });
}

function evaluationReason(evaluation: ActionEvaluation): ResourceActionBlockedReason | undefined {
  if (evaluation.visibility.kind === "hidden") return "not-applicable";
  switch (evaluation.availability.kind) {
    case "allowed": return undefined;
    case "denied": return "permission-denied";
    case "unknown": return evaluation.availability.reasonKey === "actionPermissionUnknown" ? "permission-unknown" : "state-unavailable";
    case "pending": return "duplicate";
    case "blocked": return "state-unavailable";
  }
}

function ambiguous(error: AdaptedAPIError) {
  return ["network", "timeout", "unavailable", "protocol", "unknown"].includes(error.kind);
}

function notApplicable(): ActionEvaluation {
  return Object.freeze({
    visibility: Object.freeze({ kind: "hidden" as const, reason: "not-applicable" as const }),
    availability: Object.freeze({ kind: "blocked" as const, reasonKey: "actionStateBlocked" as const }),
  });
}

function pending(value: ActionEvaluation): ActionEvaluation {
  return Object.freeze({
    visibility: value.visibility,
    availability: Object.freeze({ kind: "pending" as const, reasonKey: "actionAlreadyPending" as const }),
  });
}

function reconciliationBlocked(value: ActionEvaluation): ActionEvaluation {
  return Object.freeze({
    visibility: value.visibility,
    availability: Object.freeze({ kind: "blocked" as const, reasonKey: "confirmationRefreshRequired" as const }),
  });
}

function blocked(reason: ResourceActionBlockedReason): ResourceActionExecutionResult & ResourceActionOpenResult {
  return Object.freeze({ kind: "blocked", reason });
}
