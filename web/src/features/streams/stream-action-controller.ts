import {
  buildStreamActionDescriptor,
  streamActionApplicable,
  streamActionRequest,
  streamActionScope,
  type StreamActionIntent,
  type StreamActionRequest,
} from "@/features/streams/stream-action-descriptors";
import type { ActionEvaluation } from "@/lib/foundation/actions/contracts";
import { adaptAPIError } from "@/lib/foundation/api-errors/adapter";
import type { AdaptedAPIError } from "@/lib/foundation/api-errors/contracts";
import { defaultAPIErrorRegistry, defineAPIErrorRegistry } from "@/lib/foundation/api-errors/registry";
import { evaluateActionPermission, type PermissionSnapshot } from "@/lib/foundation/permissions/evaluator";
import { isStreamValue, isConfirmedStreamStopOutcome } from "@/features/streams/stream-action-response";

export type StreamActionStateSnapshot = Readonly<{
  kind: "ready" | "missing" | "unknown";
  freshness: "fresh" | "refreshing" | "stale" | "unavailable";
  fingerprint?: string;
}>;

export type StreamActionOpenResult =
  | Readonly<{ kind: "allowed"; intent: StreamActionIntent; descriptor: NonNullable<ReturnType<typeof buildStreamActionDescriptor>>; authority: string; typedToken?: string }>
  | Readonly<{ kind: "blocked"; reason: StreamActionBlockedReason }>;

export type AllowedStreamActionOpen = Extract<StreamActionOpenResult, { kind: "allowed" }>;

export type StreamActionExecutionResult =
  | Readonly<{ kind: "succeeded"; value: unknown }>
  | Readonly<{ kind: "failed"; error: AdaptedAPIError }>
  | Readonly<{ kind: "outcome_unknown"; nextAction: "refresh-resource" | "inspect-audit" }>
  | Readonly<{ kind: "blocked"; reason: StreamActionBlockedReason }>;

export type StreamActionBlockedReason =
  | "invalid-intent" | "permission-denied" | "permission-unknown" | "not-applicable"
  | "state-unavailable" | "duplicate" | "confirmation-required" | "typed-target-mismatch"
  | "authority-changed" | "reconciliation-required" | "cancelled";

export type StreamActionController = Readonly<{
  evaluate: (intent: StreamActionIntent) => ActionEvaluation;
  open: (intent: StreamActionIntent) => Promise<StreamActionOpenResult>;
  submit: (opened: AllowedStreamActionOpen, confirmation: Readonly<{ confirmed: boolean; typedValue?: string }>) => Promise<StreamActionExecutionResult>;
  cancel: (intent?: StreamActionIntent) => void;
  reconcile: (intent: StreamActionIntent) => void;
  reconcileStop: (intent: StreamActionIntent) => Promise<StreamActionExecutionResult>;
  isPending: (intent: StreamActionIntent) => boolean;
}>;

export type StreamActionControllerDependencies = Readonly<{
  getPermissions: () => PermissionSnapshot;
  getState: (intent: StreamActionIntent) => StreamActionStateSnapshot;
  mutate: (request: StreamActionRequest & Readonly<{ signal: AbortSignal }>) => Promise<unknown>;
  readStopOutcome?: (intent: StreamActionIntent) => Promise<unknown>;
}>;

type ActiveOperation = { generation: number; abort: AbortController; released: boolean };

const deleteErrorRegistry = defineAPIErrorRegistry({
  codes: {
    ...defaultAPIErrorRegistry.codes,
    service_unassign_protected_stream: {
      kind: "protected_state",
      messageKey: "streamDeleteFinalizationPending",
      statuses: [409],
    },
  },
});

export function createStreamActionController(dependencies: StreamActionControllerDependencies): StreamActionController {
  const active = new Map<string, ActiveOperation>();
  const generations = new Map<string, number>();
  const unresolved = new Set<string>();

  const evaluateBase = (intent: StreamActionIntent): ActionEvaluation => {
    const descriptor = buildStreamActionDescriptor(intent);
    if (!descriptor || !streamActionRequest(intent)) return notApplicable();
    if (!streamActionApplicable(intent)) return notApplicable();
    const state = dependencies.getState(intent);
    const constraint = state.kind === "missing"
      ? { kind: "not-applicable" as const, reasonKey: "actionStateBlocked" as const }
      : state.kind !== "ready" || state.freshness !== "fresh" || !state.fingerprint
        ? { kind: "unknown" as const, reasonKey: "actionStateBlocked" as const }
        : { kind: "ready" as const };
    return evaluateActionPermission({
      requirement: descriptor.permissions ?? { kind: "none" },
      snapshot: dependencies.getPermissions(),
      disclosure: "visible-denied",
      constraint,
    });
  };

  const evaluate = (intent: StreamActionIntent): ActionEvaluation => {
    const value = evaluateBase(intent);
    const scope = streamActionScope(intent);
    if (!scope || value.availability.kind !== "allowed") return value;
    if (active.has(scope)) {
      return Object.freeze({
        visibility: value.visibility,
        availability: Object.freeze({ kind: "pending" as const, reasonKey: "actionAlreadyPending" as const }),
      });
    }
    if (unresolved.has(scope)) {
      return Object.freeze({
        visibility: value.visibility,
        availability: Object.freeze({ kind: "blocked" as const, reasonKey: "confirmationRefreshRequired" as const }),
      });
    }
    return value;
  };

  const open = async (intent: StreamActionIntent): Promise<StreamActionOpenResult> => {
    const reason = evaluationReason(evaluate(intent));
    if (reason) return blocked(reason);
    const descriptor = buildStreamActionDescriptor(intent);
    const state = dependencies.getState(intent);
    if (!descriptor || state.kind !== "ready" || state.freshness !== "fresh" || !state.fingerprint) {
      return blocked("state-unavailable");
    }
    return Object.freeze({
      kind: "allowed",
      intent,
      descriptor,
      authority: state.fingerprint,
      ...(descriptor.confirmation.mode === "typed-target"
        ? { typedToken: descriptor.target.publicLabel }
        : {}),
    });
  };

  const submit = async (
    opened: AllowedStreamActionOpen,
    confirmation: Readonly<{ confirmed: boolean; typedValue?: string }>,
  ): Promise<StreamActionExecutionResult> => {
    const scope = streamActionScope(opened.intent);
    if (!scope) return blocked("invalid-intent");
    if (unresolved.has(scope)) return blocked("reconciliation-required");
    if (opened.descriptor.confirmation.mode !== "none" && !confirmation.confirmed) return blocked("confirmation-required");
    if (opened.typedToken !== undefined && confirmation.typedValue !== opened.typedToken) return blocked("typed-target-mismatch");
    const operation = acquire(scope);
    if (!operation) return blocked("duplicate");
    try {
      const reason = evaluationReason(evaluateIgnoringPending(opened.intent));
      if (reason) return blocked(reason);
      const state = dependencies.getState(opened.intent);
      if (state.kind !== "ready" || state.freshness !== "fresh" || !state.fingerprint) return blocked("state-unavailable");
      if (state.fingerprint !== opened.authority) return blocked("authority-changed");
      const requestValue = streamActionRequest(opened.intent);
      if (!requestValue || !current(scope, operation)) return blocked("cancelled");
      try {
        const value = await dependencies.mutate(Object.freeze({ ...requestValue, signal: operation.abort.signal }));
        if (!current(scope, operation)) return blocked("cancelled");
        if (opened.intent.id === "STR-05") {
          const stream = value && typeof value === "object" && "stream" in value ? value.stream : value;
          if (!isStreamValue(stream) || stream.id !== opened.intent.stream?.id || stream.status !== "completed") {
            throw new Error("Stop result requires state reconciliation");
          }
        }
        return Object.freeze({ kind: "succeeded", value });
      } catch (error) {
        if (!current(scope, operation)) return blocked("cancelled");
        const adapted = adaptAPIError(error, opened.intent.id === "STR-10" ? { registry: deleteErrorRegistry } : undefined);
        unresolved.add(scope);
        if (["network", "timeout", "unavailable", "protocol", "unknown"].includes(adapted.kind)) {
          // A lost Stop response is reconciled by reads only. A completed row
          // is written by the server after the assigned services acknowledge
          // Stop; a stale/live/failed row is not a successful Stop receipt.
          if (opened.intent.id === "STR-05" && dependencies.readStopOutcome) {
            try {
              const value = await dependencies.readStopOutcome(opened.intent);
              if (!current(scope, operation)) return blocked("cancelled");
              if (isConfirmedStreamStopOutcome(value, opened.intent)) {
                unresolved.delete(scope);
                return Object.freeze({ kind: "succeeded", value });
              }
            } catch { /* Keep the unresolved latch when the read is unavailable. */ }
          }
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
      const scopes = intent ? [streamActionScope(intent)].filter((value): value is string => Boolean(value)) : [...active.keys()];
      for (const scope of scopes) {
        const operation = active.get(scope);
        if (!operation) continue;
        operation.abort.abort();
        generations.set(scope, operation.generation + 1);
        release(scope, operation);
      }
    },
    reconcile: (intent) => {
      const scope = streamActionScope(intent);
      if (scope && intent.id !== "STR-05") {
        unresolved.delete(scope);
      }
    },
    reconcileStop: async (intent) => {
      const scope = streamActionScope(intent);
      if (!scope || intent.id !== "STR-05" || !dependencies.readStopOutcome) return blocked("invalid-intent");
      const operation = acquire(scope);
      if (!operation) return blocked("duplicate");
      try {
        const value = await dependencies.readStopOutcome(intent);
        if (!current(scope, operation)) return blocked("cancelled");
        if (isConfirmedStreamStopOutcome(value, intent)) {
          unresolved.delete(scope);
          return Object.freeze({ kind: "succeeded", value });
        }
        return Object.freeze({ kind: "outcome_unknown", nextAction: "refresh-resource" });
      } catch { return Object.freeze({ kind: "outcome_unknown", nextAction: "refresh-resource" }); }
      finally { release(scope, operation); }
    },
    isPending: (intent) => {
      const scope = streamActionScope(intent);
      return Boolean(scope && active.has(scope));
    },
  });

  function evaluateIgnoringPending(intent: StreamActionIntent) {
    const value = evaluateBase(intent);
    const scope = streamActionScope(intent);
    if (scope && unresolved.has(scope)) {
      return Object.freeze({
        visibility: value.visibility,
        availability: Object.freeze({ kind: "blocked" as const, reasonKey: "confirmationRefreshRequired" as const }),
      });
    }
    return value;
  }
}

function evaluationReason(evaluation: ActionEvaluation): StreamActionBlockedReason | undefined {
  if (evaluation.visibility.kind === "hidden") return "not-applicable";
  switch (evaluation.availability.kind) {
    case "allowed": return undefined;
    case "denied": return "permission-denied";
    case "unknown": return "permission-unknown";
    case "pending": return "duplicate";
    case "blocked": return "state-unavailable";
  }
}

function notApplicable(): ActionEvaluation {
  return Object.freeze({
    visibility: Object.freeze({ kind: "hidden" as const, reason: "not-applicable" as const }),
    availability: Object.freeze({ kind: "blocked" as const, reasonKey: "actionStateBlocked" as const }),
  });
}

function blocked(reason: StreamActionBlockedReason) {
  return Object.freeze({ kind: "blocked" as const, reason });
}
