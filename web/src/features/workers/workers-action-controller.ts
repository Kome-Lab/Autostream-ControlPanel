import type { QueryClient } from "@tanstack/react-query";

import { apiGet, apiPost } from "@/lib/api/client";
import type {
  ActionEvaluation,
  MutationOutcome,
} from "@/lib/foundation/actions/contracts";
import {
  evaluateConfirmationOpen,
  evaluateConfirmationSubmit,
  type ConfirmationAuthoritySnapshot,
} from "@/lib/foundation/actions/confirmation-revalidation";
import { adaptAPIError } from "@/lib/foundation/api-errors/adapter";
import type { AdaptedAPIError } from "@/lib/foundation/api-errors/contracts";
import {
  evaluateActionPermission,
  type PermissionSnapshot,
} from "@/lib/foundation/permissions/evaluator";
import type { RemoteState } from "@/lib/foundation/remote-state/contracts";
import type { CurrentUser, WorkerNode } from "@/types/domain";
import {
  buildWorkerRestartDescriptor,
  canonicalWorkerID,
  workerRestartDuplicateKey,
  workerRestartFingerprint,
  type WorkerRestartDescriptor,
} from "@/features/workers/workers-action-descriptors";
import {
  copyCanonicalWorkerWireList,
  copyCanonicalWorkerWireValue,
  type CanonicalWorkerNode,
} from "@/features/workers/workers-wire-normalizer";

export type WorkerRestartDialogState =
  | Readonly<{ kind: "ready" }>
  | Readonly<{ kind: "stale-blocked" }>
  | Readonly<{ kind: "revalidation-unavailable" }>
  | Readonly<{ kind: "conflict"; error: AdaptedAPIError }>
  | Readonly<{ kind: "failed"; error: AdaptedAPIError }>
  | Readonly<{
      kind: "outcome-unknown";
      nextAction: "refresh-resource" | "inspect-audit" | "contact-operator";
    }>;

export type WorkerRestartOpenResult =
  | Readonly<{
      kind: "allowed";
      descriptor: WorkerRestartDescriptor;
      evaluation: ActionEvaluation;
      snapshot: ConfirmationAuthoritySnapshot;
    }>
  | Readonly<{
      kind: "blocked";
      descriptor: WorkerRestartDescriptor;
      evaluation: ActionEvaluation;
      reason: string;
    }>;

export type AllowedWorkerRestartOpen = Extract<WorkerRestartOpenResult, { kind: "allowed" }>;

export type WorkerRestartSubmitResult = Readonly<{
  state: WorkerRestartDialogState;
  outcome?: MutationOutcome<unknown>;
}>;

export type WorkerRestartControllerDependencies = Readonly<{
  queryClient: QueryClient;
  fetchWorkers?: () => Promise<unknown>;
  postRestart?: (path: string) => Promise<unknown>;
}>;

export type WorkerRestartController = Readonly<{
  evaluate: (worker: WorkerNode) => ActionEvaluation;
  open: (worker: WorkerNode) => WorkerRestartOpenResult;
  submit: (
    opened: AllowedWorkerRestartOpen,
    onMutationStart?: () => void,
  ) => Promise<WorkerRestartSubmitResult>;
  isPending: (worker: WorkerNode) => boolean;
}>;

const authQueryKey = Object.freeze(["auth", "me"] as const);
const workersQueryKey = Object.freeze(["workers"] as const);
const readyState = Object.freeze({ kind: "ready" } as const);
const staleBlockedState = Object.freeze({ kind: "stale-blocked" } as const);
const revalidationUnavailableState = Object.freeze({ kind: "revalidation-unavailable" } as const);
const invalidWorkerTarget = Object.freeze({
  id: "",
  service_id: "",
  service_type: "",
  service_name: "Worker",
  status: "",
});

export function createWorkerRestartController({
  queryClient,
  fetchWorkers = () => apiGet<unknown>("/workers"),
  postRestart = (path) => apiPost(path),
}: WorkerRestartControllerDependencies): WorkerRestartController {
  const activeDuplicateKeys = new Set<string>();

  const evaluate = (worker: WorkerNode): ActionEvaluation => {
    const canonicalWorker = copyCanonicalWorkerWireValue(worker);
    return evaluateCanonical(canonicalWorker);
  };

  const evaluateCanonical = (worker: CanonicalWorkerNode | undefined): ActionEvaluation => {
    const descriptor = buildWorkerRestartDescriptor(worker ?? invalidWorkerTarget);
    const current = worker ? cachedWorker(queryClient, worker) : undefined;
    return evaluateActionPermission({
      requirement: descriptor.permissions,
      snapshot: permissionSnapshot(queryClient),
      disclosure: "visible-denied",
      constraint: current
        ? { kind: "ready" }
        : { kind: "unknown", reasonKey: "workerRestartStateUnavailable" },
      pending: worker && activeDuplicateKeys.has(workerRestartDuplicateKey(worker))
        ? { kind: "pending", reasonKey: "workerRestartPending" }
        : { kind: "idle" },
      deniedReasonKey: "workerRestartPermissionDenied",
      unknownReasonKey: "workerRestartPermissionUnknown",
    });
  };

  const open = (worker: WorkerNode): WorkerRestartOpenResult => {
    const canonicalWorker = copyCanonicalWorkerWireValue(worker);
    const descriptor = buildWorkerRestartDescriptor(canonicalWorker ?? invalidWorkerTarget);
    const current = canonicalWorker ? cachedWorker(queryClient, canonicalWorker) : undefined;
    const evaluation = evaluateCanonical(canonicalWorker);
    if (!current) return blockedOpen(descriptor, evaluation, "target-unavailable");
    const gate = evaluateConfirmationOpen(
      descriptor,
      workerRemoteState(queryClient, current),
      evaluation,
      fingerprintEvidence(current),
    );
    return gate.kind === "allowed"
      ? Object.freeze({ kind: "allowed", descriptor, evaluation, snapshot: gate.snapshot })
      : blockedOpen(descriptor, evaluation, gate.reason);
  };

  const submit = async (
    opened: AllowedWorkerRestartOpen,
    onMutationStart?: () => void,
  ): Promise<WorkerRestartSubmitResult> => {
    const duplicateKey = `worker:${opened.descriptor.target.resourceId}:restart`;
    if (activeDuplicateKeys.has(duplicateKey)) {
      return Object.freeze({ state: revalidationUnavailableState });
    }
    activeDuplicateKeys.add(duplicateKey);
    try {
      const authEvaluation = evaluateActionPermission({
        requirement: opened.descriptor.permissions,
        snapshot: permissionSnapshot(queryClient),
        disclosure: "visible-denied",
        deniedReasonKey: "workerRestartPermissionDenied",
        unknownReasonKey: "workerRestartPermissionUnknown",
      });
      if (authEvaluation.availability.kind !== "allowed") {
        return Object.freeze({ state: revalidationUnavailableState });
      }

      let freshWorkers: readonly CanonicalWorkerNode[];
      try {
        freshWorkers = copyCanonicalWorkerWireList(await fetchWorkers()) ?? [];
      } catch {
        return Object.freeze({ state: revalidationUnavailableState });
      }
      const current = findWorker(freshWorkers, opened.descriptor.target.resourceId);
      if (!current || current.service_type !== "worker") {
        return Object.freeze({ state: staleBlockedState });
      }
      queryClient.setQueryData(workersQueryKey, freshWorkers);
      const currentDescriptor = buildWorkerRestartDescriptor(current);
      const currentEvaluation = evaluateActionPermission({
        requirement: currentDescriptor.permissions,
        snapshot: permissionSnapshot(queryClient),
        disclosure: "visible-denied",
        constraint: { kind: "ready" },
        pending: { kind: "idle" },
        deniedReasonKey: "workerRestartPermissionDenied",
        unknownReasonKey: "workerRestartPermissionUnknown",
      });
      const gate = evaluateConfirmationSubmit(
        currentDescriptor,
        opened.snapshot,
        freshWorkerRemoteState(current),
        currentEvaluation,
        fingerprintEvidence(current),
      );
      if (gate.kind !== "allowed") {
        return Object.freeze({
          state: gate.reason === "target-changed" || gate.reason === "authority-changed"
            ? staleBlockedState
            : revalidationUnavailableState,
        });
      }

      try {
        onMutationStart?.();
        const value = await postRestart(`/workers/${encodeURIComponent(canonicalWorkerID(current))}/restart`);
        try {
          await queryClient.invalidateQueries({ queryKey: workersQueryKey });
        } catch {
          // The accepted POST remains authoritative even when the follow-up cache refresh fails.
        }
        return Object.freeze({
          state: readyState,
          outcome: Object.freeze({
            kind: "succeeded",
            value,
          } as const satisfies MutationOutcome<unknown>),
        });
      } catch (error) {
        const adapted = adaptAPIError(error);
        if (adapted.kind === "conflict" || adapted.kind === "protected_state") {
          await refreshWorkersAfterConflict(queryClient, fetchWorkers);
          return Object.freeze({
            state: Object.freeze({ kind: "conflict", error: adapted }),
            outcome: Object.freeze({ kind: "failed", error: adapted }),
          });
        }
        if (isAmbiguousMutationError(adapted)) {
          const outcome = Object.freeze({
            kind: "outcome_unknown",
            nextAction: "inspect-audit",
          } as const satisfies MutationOutcome<unknown>);
          return Object.freeze({
            state: Object.freeze({ kind: "outcome-unknown", nextAction: outcome.nextAction }),
            outcome,
          });
        }
        return Object.freeze({
          state: Object.freeze({ kind: "failed", error: adapted }),
          outcome: Object.freeze({ kind: "failed", error: adapted }),
        });
      }
    } finally {
      activeDuplicateKeys.delete(duplicateKey);
    }
  };

  return Object.freeze({
    evaluate,
    open,
    submit,
    isPending: (worker) => {
      const canonicalWorker = copyCanonicalWorkerWireValue(worker);
      return canonicalWorker ? activeDuplicateKeys.has(workerRestartDuplicateKey(canonicalWorker)) : false;
    },
  });
}

function permissionSnapshot(queryClient: QueryClient): PermissionSnapshot {
  const state = queryClient.getQueryState(authQueryKey);
  if (state?.fetchStatus === "fetching") return Object.freeze({ kind: "refreshing" });
  const current = queryClient.getQueryData<CurrentUser>(authQueryKey);
  const permissions = copyStringArray(current?.permissions);
  return permissions
    ? Object.freeze({ kind: "ready", permissions })
    : Object.freeze({ kind: "unavailable" });
}

function cachedWorker(queryClient: QueryClient, target: CanonicalWorkerNode): CanonicalWorkerNode | undefined {
  const rows = copyCanonicalWorkerWireList(queryClient.getQueryData(workersQueryKey));
  const current = rows ? findWorker(rows, canonicalWorkerID(target)) : undefined;
  return current
    && current.service_type === "worker"
    && workerRestartFingerprint(current) === workerRestartFingerprint(target)
    ? current
    : undefined;
}

function findWorker(rows: readonly CanonicalWorkerNode[], resourceId: string): CanonicalWorkerNode | undefined {
  return rows.find((worker) => canonicalWorkerID(worker) === resourceId);
}

function workerRemoteState(queryClient: QueryClient, worker: WorkerNode): RemoteState<readonly WorkerNode[]> {
  const state = queryClient.getQueryState(workersQueryKey);
  const lastSuccessAt = state?.dataUpdatedAt && state.dataUpdatedAt >= 0 ? state.dataUpdatedAt : 0;
  return Object.freeze({
    kind: "ready",
    data: Object.freeze([worker]),
    freshness: Object.freeze({
      kind: state?.fetchStatus === "fetching" ? "refreshing" : "fresh",
      lastSuccessAt,
    }),
  });
}

function freshWorkerRemoteState(worker: WorkerNode): RemoteState<readonly WorkerNode[]> {
  return Object.freeze({
    kind: "ready",
    data: Object.freeze([worker]),
    freshness: Object.freeze({ kind: "fresh", lastSuccessAt: Date.now() }),
  });
}

function fingerprintEvidence(worker: WorkerNode) {
  return Object.freeze({
    kind: "safe-fingerprint" as const,
    fieldIds: Object.freeze(["canonicalWorkerId", "serviceType"] as const),
    value: workerRestartFingerprint(worker),
  });
}

function blockedOpen(
  descriptor: WorkerRestartDescriptor,
  evaluation: ActionEvaluation,
  reason: string,
): WorkerRestartOpenResult {
  return Object.freeze({ kind: "blocked", descriptor, evaluation, reason });
}

async function refreshWorkersAfterConflict(
  queryClient: QueryClient,
  fetchWorkers: () => Promise<unknown>,
) {
  try {
    const rows = copyCanonicalWorkerWireList(await fetchWorkers());
    if (rows) queryClient.setQueryData(workersQueryKey, rows);
  } catch {
    // The canonical conflict remains the safe outcome when reconciliation fails.
  }
}

function isAmbiguousMutationError(error: AdaptedAPIError): boolean {
  return error.kind === "network"
    || error.kind === "timeout"
    || error.kind === "unavailable"
    || error.kind === "protocol";
}

function copyStringArray(value: unknown): readonly string[] | undefined {
  if (!Array.isArray(value)) return undefined;
  const copy: string[] = [];
  for (const entry of value) {
    if (typeof entry !== "string" || copy.includes(entry)) return undefined;
    copy.push(entry);
  }
  return Object.freeze(copy);
}
