import type { QueryClient } from "@tanstack/react-query";

import { apiGet } from "@/lib/api/client";
import type { ActionEvaluation } from "@/lib/foundation/actions/contracts";
import { adaptAPIError } from "@/lib/foundation/api-errors/adapter";
import type { AdaptedAPIError } from "@/lib/foundation/api-errors/contracts";
import {
  evaluateActionPermission,
  type PermissionSnapshot,
} from "@/lib/foundation/permissions/evaluator";
import type { RemoteState } from "@/lib/foundation/remote-state/contracts";
import type { CurrentUser, WorkerNode } from "@/types/domain";
import { workerConfigurationDescriptor } from "@/features/workers/workers-configuration-descriptor";
import { copyCanonicalWorkerWireValue } from "@/features/workers/workers-wire-normalizer";

export type WorkerConfigurationData = Readonly<{
  node: WorkerNode;
  node_api_url?: string;
  configuration_yaml?: string;
  configure_command?: string;
  systemd_unit?: string;
}>;

export type WorkerConfigurationSnapshot = Readonly<{
  targetId?: string;
  state: RemoteState<WorkerConfigurationData>;
  evaluation: ActionEvaluation;
  pending: boolean;
}>;

export type WorkerConfigurationControllerDependencies = Readonly<{
  queryClient: QueryClient;
  getConfiguration?: (path: string) => Promise<unknown>;
}>;

export type WorkerConfigurationController = Readonly<{
  evaluate: () => ActionEvaluation;
  getSnapshot: () => WorkerConfigurationSnapshot;
  select: (targetId: string) => Promise<WorkerConfigurationSnapshot>;
  refresh: () => Promise<WorkerConfigurationSnapshot>;
  subscribe: (listener: (snapshot: WorkerConfigurationSnapshot) => void) => () => void;
}>;

const authQueryKey = Object.freeze(["auth", "me"] as const);
const initialState = Object.freeze({ kind: "initial-loading" } as const);

export function createWorkerConfigurationController({
  queryClient,
  getConfiguration = (path) => apiGet<unknown>(path),
}: WorkerConfigurationControllerDependencies): WorkerConfigurationController {
  const listeners = new Set<(snapshot: WorkerConfigurationSnapshot) => void>();
  const cache = new Map<string, Readonly<{ data: WorkerConfigurationData; lastSuccessAt: number }>>();
  let generation = 0;
  let current = freezeSnapshot({
    state: initialState,
    evaluation: configurationEvaluation(queryClient),
    pending: false,
  });

  const emit = (snapshot: WorkerConfigurationSnapshot) => {
    current = snapshot;
    for (const listener of listeners) listener(snapshot);
  };

  const load = async (targetId: string, sameTarget: boolean): Promise<WorkerConfigurationSnapshot> => {
    const requestGeneration = generation + 1;
    generation = requestGeneration;
    const evaluation = configurationEvaluation(queryClient);
    const cached = sameTarget ? cache.get(targetId) : undefined;
    if (evaluation.availability.kind !== "allowed") {
      emit(freezeSnapshot({
        targetId,
        state: Object.freeze({
          kind: "blocking-error",
          error: permissionError(evaluation),
        }),
        evaluation,
        pending: false,
      }));
      return current;
    }

    emit(freezeSnapshot({
      targetId,
      state: cached
        ? Object.freeze({
            kind: "ready",
            data: cached.data,
            freshness: Object.freeze({ kind: "refreshing", lastSuccessAt: cached.lastSuccessAt }),
          })
        : initialState,
      evaluation,
      pending: true,
    }));

    let response: unknown;
    try {
      response = await getConfiguration(`/nodes/${encodeURIComponent(targetId)}/configuration`);
    } catch (error) {
      if (generation !== requestGeneration || current.targetId !== targetId) return current;
      const adapted = adaptAPIError(error);
      emit(freezeSnapshot({
        targetId,
        state: cached
          ? Object.freeze({
              kind: "ready",
              data: cached.data,
              freshness: Object.freeze({
                kind: "stale",
                lastSuccessAt: cached.lastSuccessAt,
                error: adapted,
              }),
            })
          : Object.freeze({ kind: "blocking-error", error: adapted }),
        evaluation: configurationEvaluation(queryClient),
        pending: false,
      }));
      return current;
    }

    if (generation !== requestGeneration || current.targetId !== targetId) return current;
    const decoded = decodeConfiguration(response, targetId);
    const completedAt = Date.now();
    if (decoded.kind === "malformed") {
      const error = protocolError();
      emit(freezeSnapshot({
        targetId,
        state: cached
          ? Object.freeze({
              kind: "ready",
              data: cached.data,
              freshness: Object.freeze({
                kind: "stale",
                lastSuccessAt: cached.lastSuccessAt,
                error,
              }),
            })
          : Object.freeze({ kind: "blocking-error", error }),
        evaluation: configurationEvaluation(queryClient),
        pending: false,
      }));
      return current;
    }
    if (decoded.kind === "empty") {
      cache.delete(targetId);
      emit(freezeSnapshot({
        targetId,
        state: Object.freeze({
          kind: "empty",
          freshness: Object.freeze({ kind: "fresh", lastSuccessAt: completedAt }),
        }),
        evaluation: configurationEvaluation(queryClient),
        pending: false,
      }));
      return current;
    }

    cache.set(targetId, Object.freeze({ data: decoded.data, lastSuccessAt: completedAt }));
    emit(freezeSnapshot({
      targetId,
      state: Object.freeze({
        kind: "ready",
        data: decoded.data,
        freshness: Object.freeze({ kind: "fresh", lastSuccessAt: completedAt }),
      }),
      evaluation: configurationEvaluation(queryClient),
      pending: false,
    }));
    return current;
  };

  return Object.freeze({
    evaluate: () => configurationEvaluation(queryClient),
    getSnapshot: () => current,
    select: (targetId) => load(targetId, current.targetId === targetId),
    refresh: () => current.targetId
      ? load(current.targetId, true)
      : Promise.resolve(current),
    subscribe: (listener) => {
      listeners.add(listener);
      return () => listeners.delete(listener);
    },
  });
}

function configurationEvaluation(queryClient: QueryClient): ActionEvaluation {
  return evaluateActionPermission({
    requirement: workerConfigurationDescriptor.permissions,
    snapshot: permissionSnapshot(queryClient),
    disclosure: workerConfigurationDescriptor.disclosure,
    deniedReasonKey: "workerConfigurationPermissionDenied",
    unknownReasonKey: "workerConfigurationPermissionUnknown",
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

function permissionError(evaluation: ActionEvaluation): AdaptedAPIError {
  if (evaluation.availability.kind === "denied") {
    return Object.freeze({ kind: "forbidden", messageKey: "workerConfigurationPermissionDenied" });
  }
  return Object.freeze({ kind: "unknown", messageKey: "workerConfigurationPermissionUnknown" });
}

function protocolError(): AdaptedAPIError {
  return Object.freeze({ kind: "protocol", messageKey: "apiErrorProtocol" });
}

function freezeSnapshot(snapshot: WorkerConfigurationSnapshot): WorkerConfigurationSnapshot {
  return Object.freeze(snapshot);
}

function decodeConfiguration(value: unknown, expectedTargetId: string):
  | Readonly<{ kind: "ready"; data: WorkerConfigurationData }>
  | Readonly<{ kind: "empty" }>
  | Readonly<{ kind: "malformed" }> {
  if (value === null || typeof value !== "object" || Array.isArray(value)) {
    return Object.freeze({ kind: "malformed" });
  }
  try {
    const node = copyCanonicalWorkerWireValue(Reflect.get(value, "node"));
    if (!node || node.service_id !== expectedTargetId) return Object.freeze({ kind: "malformed" });
    const fields = ["node_api_url", "configuration_yaml", "configure_command", "systemd_unit"] as const;
    const copied: Partial<Record<(typeof fields)[number], string>> = {};
    let usable = false;
    for (const field of fields) {
      const fieldValue = Reflect.get(value, field);
      if (fieldValue === undefined) continue;
      if (typeof fieldValue !== "string") return Object.freeze({ kind: "malformed" });
      copied[field] = fieldValue;
      if (fieldValue.length > 0) usable = true;
    }
    if (!usable) return Object.freeze({ kind: "empty" });
    return Object.freeze({
      kind: "ready",
      data: Object.freeze({ node, ...copied }),
    });
  } catch {
    return Object.freeze({ kind: "malformed" });
  }
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
