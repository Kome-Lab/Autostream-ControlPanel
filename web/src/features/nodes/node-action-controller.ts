import {
  buildNodeActionDescriptor,
  nodeActionFingerprint,
  nodeActionMutationPath,
  nodeActionProjectionInput,
  nodeActionScope,
  nodeActionTemplate,
  type NodeActionID,
  type NodeActionIntent,
} from "@/features/nodes/node-action-descriptors";
import type { NodeActionProjection } from "@/features/nodes/node-permission-projection";
import { adaptAPIError } from "@/lib/foundation/api-errors/adapter";
import type { AdaptedAPIError } from "@/lib/foundation/api-errors/contracts";
import type { ActionEvaluation } from "@/lib/foundation/actions/contracts";
import type { PermissionSnapshot } from "@/lib/foundation/permissions/evaluator";

export type { NodeActionProjection } from "@/features/nodes/node-permission-projection";

export type NodeActionStateSnapshot = Readonly<{
  kind: "ready" | "missing" | "unknown";
  freshness: "fresh" | "refreshing" | "stale" | "unavailable";
  fingerprint?: string;
}>;

export type NodeActionOpenResult =
  | Readonly<{
      kind: "allowed";
      intent: NodeActionIntent;
      projection?: NodeActionProjection;
      authority: string;
      typedToken?: string;
    }>
  | Readonly<{
      kind: "blocked";
      reason:
        | "invalid-intent"
        | "base-permission-missing"
        | "permission-unknown"
        | "not-applicable"
        | "state-unavailable"
        | "projection-unavailable"
        | "projection-denied"
        | "duplicate"
        | "cancelled";
    }>;

type NodeActionOpenBlockedReason = Extract<NodeActionOpenResult, { kind: "blocked" }>["reason"];
type NodeActionExecutionBlockedReason = Extract<NodeActionExecutionResult, { kind: "blocked" }>["reason"];

export type AllowedNodeActionOpen = Extract<NodeActionOpenResult, { kind: "allowed" }>;

export type NodeActionExecutionResult =
  | Readonly<{ kind: "succeeded"; value: unknown }>
  | Readonly<{ kind: "failed"; error: AdaptedAPIError }>
  | Readonly<{ kind: "outcome_unknown"; nextAction: "refresh-resource" | "inspect-audit" }>
  | Readonly<{
      kind: "blocked";
      reason:
        | "invalid-intent"
        | "base-permission-missing"
        | "permission-unknown"
        | "not-applicable"
        | "state-unavailable"
        | "projection-unavailable"
        | "projection-denied"
        | "duplicate"
        | "confirmation-required"
        | "typed-target-mismatch"
        | "authority-changed"
        | "reconciliation-required"
        | "cancelled";
    }>;

export type NodeActionController = Readonly<{
  evaluate: (intent: NodeActionIntent) => ActionEvaluation;
  prepare: (intent: NodeActionIntent) => Promise<ActionEvaluation>;
  open: (intent: NodeActionIntent) => Promise<NodeActionOpenResult>;
  submit: (
    opened: AllowedNodeActionOpen,
    confirmation: Readonly<{ confirmed: boolean; typedValue?: string }>,
  ) => Promise<NodeActionExecutionResult>;
  cancel: (intent?: NodeActionIntent) => void;
  reconcile: (intent: NodeActionIntent) => void;
  isPending: (intent: NodeActionIntent) => boolean;
}>;

export type NodeActionControllerDependencies = Readonly<{
  getPermissions: () => PermissionSnapshot;
  getNodeState: (intent: NodeActionIntent) => NodeActionStateSnapshot;
  fetchProjection: (
    input: NonNullable<ReturnType<typeof nodeActionProjectionInput>>,
    signal: AbortSignal,
  ) => Promise<NodeActionProjection | undefined>;
  mutate: (request: Readonly<{
    id: NodeActionID;
    method: "POST" | "PUT" | "DELETE";
    path: string;
    body?: unknown;
    signal: AbortSignal;
  }>) => Promise<unknown>;
}>;

type ActiveOperation = {
  generation: number;
  abort: AbortController;
  released: boolean;
};

const hiddenEvaluation = Object.freeze({
  visibility: Object.freeze({ kind: "hidden", reason: "security-sensitive" }),
  availability: Object.freeze({ kind: "denied", reasonKey: "actionPermissionDenied" }),
} as const satisfies ActionEvaluation);
const notApplicableEvaluation = Object.freeze({
  visibility: Object.freeze({ kind: "hidden", reason: "not-applicable" }),
  availability: Object.freeze({ kind: "blocked", reasonKey: "actionStateBlocked" }),
} as const satisfies ActionEvaluation);

export function createNodeActionController(
  dependencies: NodeActionControllerDependencies,
): NodeActionController {
  const active = new Map<string, ActiveOperation>();
  const generations = new Map<string, number>();
  const unresolved = new Set<string>();
  const projections = new Map<string, NodeActionProjection | null>();

  const evaluate = (intent: NodeActionIntent): ActionEvaluation => {
    const template = nodeActionTemplate(intent.id);
    const descriptor = buildNodeActionDescriptor(intent);
    if (!template || !descriptor || !nodeActionMutationPath(intent)) return notApplicableEvaluation;
    if (intent.id === "NOD-03" && intent.node?.service_type === "update_agent" && intent.node.transport_mode === "pull_v2") {
      return notApplicableEvaluation;
    }
    const permissions = dependencies.getPermissions();
    if (permissions.kind !== "ready") {
      return Object.freeze({
        visibility: Object.freeze({ kind: "visible" }),
        availability: Object.freeze({ kind: "unknown", reasonKey: "actionPermissionUnknown" }),
      });
    }
    if (!hasPermission(permissions.permissions, template.basePermission)) {
      return template.projectionAction
        ? hiddenEvaluation
        : Object.freeze({
            visibility: Object.freeze({ kind: "visible" }),
            availability: Object.freeze({ kind: "denied", reasonKey: "actionPermissionDenied" }),
          });
    }
    const state = dependencies.getNodeState(intent);
    if (state.kind === "missing") return notApplicableEvaluation;
    if (state.kind !== "ready" || state.freshness !== "fresh") {
      return Object.freeze({
        visibility: Object.freeze({ kind: "visible" }),
        availability: Object.freeze({ kind: "unknown", reasonKey: "actionStateBlocked" }),
      });
    }
    const scope = nodeActionScope(intent);
    if (template.projectionAction && scope) {
      const projection = projections.get(scope);
      if (projection === null || projection === undefined) {
        return Object.freeze({
          visibility: Object.freeze({ kind: "visible" }),
          availability: Object.freeze({ kind: "unknown", reasonKey: "actionPermissionUnknown" }),
        });
      }
      const reason = validateProjection(intent, projection);
      if (reason === "not-applicable") return notApplicableEvaluation;
      if (reason === "projection-denied") {
        return Object.freeze({
          visibility: Object.freeze({ kind: "visible" }),
          availability: Object.freeze({ kind: "denied", reasonKey: "actionPermissionDenied" }),
        });
      }
      if (reason) {
        return Object.freeze({
          visibility: Object.freeze({ kind: "visible" }),
          availability: Object.freeze({ kind: "unknown", reasonKey: "actionPermissionUnknown" }),
        });
      }
    }
    return Object.freeze({
      visibility: Object.freeze({ kind: "visible" }),
      availability: scope && active.has(scope)
        ? Object.freeze({ kind: "pending", reasonKey: "actionAlreadyPending" })
        : Object.freeze({ kind: "allowed" }),
    });
  };

  const open = async (intent: NodeActionIntent): Promise<NodeActionOpenResult> => {
    const preflight = preflightReason(intent, evaluateBase(intent));
    if (preflight) return blocked(preflight);
    const scope = nodeActionScope(intent);
    const descriptor = buildNodeActionDescriptor(intent);
    const fingerprint = nodeActionFingerprint(intent);
    if (!scope || !descriptor || !fingerprint) return blocked("invalid-intent");
    const operation = acquire(scope);
    if (!operation) return blocked("duplicate");
    try {
      const projectionInput = nodeActionProjectionInput(intent);
      if (!projectionInput) {
        const state = dependencies.getNodeState(intent);
        return allowed(intent, state.fingerprint ?? fingerprint, descriptor.confirmation.mode === "typed-target"
          ? typedToken(descriptor)
          : undefined);
      }
      const projection = await dependencies.fetchProjection(projectionInput, operation.abort.signal);
      if (!current(scope, operation)) return blocked("cancelled");
      projections.set(scope, projection ?? null);
      const projectionReason = validateProjection(intent, projection);
      if (projectionReason) return blocked(projectionReason);
      return allowed(
        intent,
        projection!.projectionRevision,
        typedToken(descriptor),
        projection,
      );
    } catch {
      return current(scope, operation) ? blocked("projection-unavailable") : blocked("cancelled");
    } finally {
      release(scope, operation);
    }
  };

  const prepare = async (intent: NodeActionIntent): Promise<ActionEvaluation> => {
    const scope = nodeActionScope(intent);
    const projectionInput = nodeActionProjectionInput(intent);
    const base = evaluateBase(intent);
    if (!scope || !projectionInput || preflightReason(intent, base)) return base;
    const operation = acquire(scope);
    if (!operation) return evaluate(intent);
    try {
      let projection: NodeActionProjection | undefined;
      try {
        projection = await dependencies.fetchProjection(projectionInput, operation.abort.signal);
      } catch {
        projection = undefined;
      }
      if (current(scope, operation)) projections.set(scope, projection ?? null);
      return evaluate(intent);
    } finally {
      release(scope, operation);
    }
  };

  const submit = async (
    opened: AllowedNodeActionOpen,
    confirmation: Readonly<{ confirmed: boolean; typedValue?: string }>,
  ): Promise<NodeActionExecutionResult> => {
    const scope = nodeActionScope(opened.intent);
    if (!scope) return blocked("invalid-intent");
    if (unresolved.has(scope)) return blocked("reconciliation-required");
    if (!confirmation.confirmed) return blocked("confirmation-required");
    if (opened.typedToken !== undefined && confirmation.typedValue !== opened.typedToken) {
      return blocked("typed-target-mismatch");
    }
    const operation = acquire(scope);
    if (!operation) return blocked("duplicate");
    try {
      const evaluation = evaluateWithoutPending(opened.intent, evaluateBase);
      const preflight = preflightReason(opened.intent, evaluation);
      if (preflight) return blocked(preflight);
      const fingerprint = nodeActionFingerprint(opened.intent);
      if (!fingerprint) return blocked("invalid-intent");
      const projectionInput = nodeActionProjectionInput(opened.intent);
      if (projectionInput) {
        let projection: NodeActionProjection | undefined;
        try {
          projection = await dependencies.fetchProjection(projectionInput, operation.abort.signal);
        } catch {
          return current(scope, operation) ? blocked("projection-unavailable") : blocked("cancelled");
        }
        if (!current(scope, operation)) return blocked("cancelled");
        projections.set(scope, projection ?? null);
        const projectionReason = validateProjection(opened.intent, projection);
        if (projectionReason) return blocked(projectionReason);
        if (projection!.projectionRevision !== opened.authority) return blocked("authority-changed");
      } else {
        const state = dependencies.getNodeState(opened.intent);
        if ((state.fingerprint ?? fingerprint) !== opened.authority) return blocked("authority-changed");
      }

      const template = nodeActionTemplate(opened.intent.id);
      const path = nodeActionMutationPath(opened.intent);
      if (!template || !path || !current(scope, operation)) return blocked("cancelled");
      try {
        const value = await dependencies.mutate({
          id: opened.intent.id,
          method: template.method,
          path,
          ...(opened.intent.body === undefined ? {} : { body: opened.intent.body }),
          signal: operation.abort.signal,
        });
        if (!current(scope, operation)) return blocked("cancelled");
        return Object.freeze({ kind: "succeeded", value });
      } catch (error) {
        if (!current(scope, operation)) return blocked("cancelled");
        const adapted = adaptAPIError(error);
        unresolved.add(scope);
        if (["network", "timeout", "unavailable", "protocol", "unknown"].includes(adapted.kind)) {
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
    const operation: ActiveOperation = {
      generation: (generations.get(scope) ?? 0) + 1,
      abort: new AbortController(),
      released: false,
    };
    generations.set(scope, operation.generation);
    active.set(scope, operation);
    return operation;
  }

  function release(scope: string, operation: ActiveOperation) {
    if (operation.released) return;
    operation.released = true;
    if (active.get(scope) === operation) active.delete(scope);
  }

  function current(scope: string, operation: ActiveOperation) {
    return !operation.abort.signal.aborted
      && active.get(scope) === operation
      && generations.get(scope) === operation.generation;
  }

  return Object.freeze({
    evaluate,
    prepare,
    open,
    submit,
    cancel: (intent) => {
      const scopes = intent ? [nodeActionScope(intent)].filter((value): value is string => Boolean(value)) : [...active.keys()];
      for (const scope of scopes) {
        const operation = active.get(scope);
        if (!operation) continue;
        operation.abort.abort();
        generations.set(scope, operation.generation + 1);
        release(scope, operation);
      }
    },
    reconcile: (intent) => {
      const scope = nodeActionScope(intent);
      if (scope) {
        unresolved.delete(scope);
        projections.delete(scope);
      }
    },
    isPending: (intent) => {
      const scope = nodeActionScope(intent);
      return Boolean(scope && active.has(scope));
    },
  });

  function evaluateBase(intent: NodeActionIntent): ActionEvaluation {
    const template = nodeActionTemplate(intent.id);
    const descriptor = buildNodeActionDescriptor(intent);
    if (!template || !descriptor || !nodeActionMutationPath(intent)) return notApplicableEvaluation;
    if (intent.id === "NOD-03" && intent.node?.service_type === "update_agent" && intent.node.transport_mode === "pull_v2") {
      return notApplicableEvaluation;
    }
    const permissions = dependencies.getPermissions();
    if (permissions.kind !== "ready") {
      return Object.freeze({
        visibility: Object.freeze({ kind: "visible" }),
        availability: Object.freeze({ kind: "unknown", reasonKey: "actionPermissionUnknown" }),
      });
    }
    if (!hasPermission(permissions.permissions, template.basePermission)) {
      return template.projectionAction
        ? hiddenEvaluation
        : Object.freeze({
            visibility: Object.freeze({ kind: "visible" }),
            availability: Object.freeze({ kind: "denied", reasonKey: "actionPermissionDenied" }),
          });
    }
    const state = dependencies.getNodeState(intent);
    if (state.kind === "missing") return notApplicableEvaluation;
    if (state.kind !== "ready" || state.freshness !== "fresh") {
      return Object.freeze({
        visibility: Object.freeze({ kind: "visible" }),
        availability: Object.freeze({ kind: "unknown", reasonKey: "actionStateBlocked" }),
      });
    }
    return Object.freeze({
      visibility: Object.freeze({ kind: "visible" }),
      availability: Object.freeze({ kind: "allowed" }),
    });
  }
}

function allowed(
  intent: NodeActionIntent,
  authority: string,
  typedToken?: string,
  projection?: NodeActionProjection,
): AllowedNodeActionOpen {
  return Object.freeze({
    kind: "allowed",
    intent,
    authority,
    ...(typedToken === undefined ? {} : { typedToken }),
    ...(projection === undefined ? {} : { projection }),
  });
}

function blocked<const Reason extends NodeActionOpenBlockedReason | NodeActionExecutionBlockedReason>(reason: Reason) {
  return Object.freeze({ kind: "blocked", reason } as const);
}

function validateProjection(
  intent: NodeActionIntent,
  projection: NodeActionProjection | undefined,
): NodeActionOpenBlockedReason | undefined {
  const input = nodeActionProjectionInput(intent);
  if (!input || !projection || projection.action !== input.action) return "projection-unavailable";
  if (projection.availability === "not_applicable") return "not-applicable";
  if (projection.availability === "denied") return "projection-denied";
  if (projection.availability !== "allowed") return "projection-unavailable";
  const minimum = input.action === "registration_token"
    ? ["api_tokens.create"]
    : ["api_tokens.create", "api_tokens.revoke"];
  if (minimum.some((permission) => !projection.requiredPermissions.includes(permission as never))) {
    return "projection-unavailable";
  }
  return undefined;
}

function typedToken(descriptor: NonNullable<ReturnType<typeof buildNodeActionDescriptor>>) {
  if (descriptor.confirmation.mode !== "typed-target") return undefined;
  return descriptor.confirmation.typedToken.kind === "public-stable-id"
    ? descriptor.target.publicStableId
    : descriptor.target.publicLabel;
}

function preflightReason(
  intent: NodeActionIntent,
  evaluation: ActionEvaluation,
): NodeActionOpenBlockedReason | undefined {
  if (evaluation.visibility.kind === "hidden") {
    return evaluation.visibility.reason === "not-applicable" ? "not-applicable" : "base-permission-missing";
  }
  switch (evaluation.availability.kind) {
    case "allowed":
      return undefined;
    case "pending":
      return "duplicate";
    case "denied":
      return nodeActionTemplate(intent.id)?.projectionAction ? "base-permission-missing" : "base-permission-missing";
    case "unknown":
      return "permission-unknown";
    case "blocked":
      return "state-unavailable";
  }
}

function evaluateWithoutPending(
  intent: NodeActionIntent,
  evaluate: (intent: NodeActionIntent) => ActionEvaluation,
): ActionEvaluation {
  const value = evaluate(intent);
  return value.availability.kind === "pending"
    ? Object.freeze({ visibility: value.visibility, availability: Object.freeze({ kind: "allowed" }) })
    : value;
}

function hasPermission(permissions: readonly string[], permission: string) {
  return permissions.includes("*") || permissions.includes(permission);
}
