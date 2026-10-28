import type { QueryClient } from "@tanstack/react-query";

import {
  resourceActionSourcePath,
  resourceActionTemplate,
  resourceRowID,
  type ResourceActionIntent,
  type ResourceActionRequest,
  type ResourceActionRow,
} from "@/features/resources/resource-action-descriptors";
import type { ResourceActionAuthority, ResourceActionStateSnapshot } from "@/features/resources/resource-action-controller";
import { apiDelete, apiDeleteJSON, apiGet, apiPost, apiPut } from "@/lib/api/client";
import type { PermissionSnapshot } from "@/lib/foundation/permissions/evaluator";
import type { CurrentUser } from "@/types/domain";

type ResourceCacheFence = { sequence: number; mutation: number; authRead: number; reads: Map<string, number>; writes: Map<string, number> };
const cacheFences = new WeakMap<QueryClient, ResourceCacheFence>();
function cacheFence(client: QueryClient) {
  let fence = cacheFences.get(client);
  if (!fence) { fence = { sequence: 0, mutation: 0, authRead: 0, reads: new Map(), writes: new Map() }; cacheFences.set(client, fence); }
  return fence;
}

export function resourcePermissionSnapshot(queryClient: QueryClient): PermissionSnapshot {
  const state = queryClient.getQueryState<CurrentUser>(["auth", "me"]);
  const current = queryClient.getQueryData<CurrentUser>(["auth", "me"]);
  if (state?.status !== "success" || !current || !Array.isArray(current.permissions)) return Object.freeze({ kind: "unavailable" });
  return permissionSnapshotFromUser(current);
}

export function resourceActionStateSnapshot(queryClient: QueryClient, intent: ResourceActionIntent): ResourceActionStateSnapshot {
  const sourcePath = resourceActionSourcePath(intent);
  if (!sourcePath) return Object.freeze({ kind: "unknown", freshness: "unavailable" });
  const state = queryClient.getQueryState<unknown>(["resource", sourcePath]);
  const data = queryClient.getQueryData<unknown>(["resource", sourcePath]);
  const freshness = state?.status === "error" && data !== undefined
      ? "stale" as const
      : state?.status === "success" && data !== undefined
        ? "fresh" as const
        : "unavailable" as const;
  return stateSnapshotFromData(intent, data, freshness);
}

export async function refreshResourceAction(queryClient: QueryClient, intent: ResourceActionIntent, signal?: AbortSignal): Promise<ResourceActionAuthority> {
  const sourcePath = resourceActionSourcePath(intent);
  if (!sourcePath) throw new Error("resource action source is unavailable");
  const fence = cacheFence(queryClient), read = ++fence.sequence;
  const write = fence.writes.get(sourcePath), mutation = fence.mutation;
  fence.reads.set(sourcePath, read); fence.authRead = read;
  await Promise.all([
    queryClient.cancelQueries({ queryKey: ["resource", sourcePath], exact: true }),
    queryClient.cancelQueries({ queryKey: ["auth", "me"], exact: true }),
  ]);
  const [data, currentUser] = await Promise.all([
    apiGet<unknown>(sourcePath, signal),
    apiGet<CurrentUser>("/auth/me", signal),
  ]);
  if (signal?.aborted) throw signal.reason;
  const resourceCurrent = () => fence.reads.get(sourcePath) === read && fence.writes.get(sourcePath) === write;
  const authCurrent = () => fence.authRead === read && fence.mutation === mutation;
  await Promise.all([
    ...(resourceCurrent() ? [queryClient.cancelQueries({ queryKey: ["resource", sourcePath], exact: true })] : []),
    ...(authCurrent() ? [queryClient.cancelQueries({ queryKey: ["auth", "me"], exact: true })] : []),
  ]);
  if (signal?.aborted) throw signal.reason;
  // An older direct GET may still serve its own intent, but must never replace
  // a newer read or a mutation's cache generation for another intent.
  if (resourceCurrent()) queryClient.setQueryData(["resource", sourcePath], data);
  if (authCurrent()) queryClient.setQueryData(["auth", "me"], currentUser);
  const freshIntent = freshenIntent(intent, data);
  return Object.freeze({
    intent: freshIntent,
    permissions: permissionSnapshotFromUser(currentUser),
    state: stateSnapshotFromData(freshIntent, data, "fresh"),
  });
}

export async function revalidateResourceAction(queryClient: QueryClient, intent: ResourceActionIntent, signal: AbortSignal) {
  const authority = await refreshResourceAction(queryClient, intent, signal);
  return { permissions: authority.permissions, state: authority.state };
}

export async function mutateResourceAction(request: ResourceActionRequest & Readonly<{ signal: AbortSignal }>, queryClient?: QueryClient) {
  if (request.signal.aborted) throw new DOMException("Cancelled", "AbortError");
  const sourcePath = resourceActionTemplate(request.id)?.sourcePath;
  const advance = () => {
    if (!queryClient || !sourcePath) return;
    const fence = cacheFence(queryClient);
    fence.writes.set(sourcePath, (fence.writes.get(sourcePath) ?? 0) + 1); fence.mutation += 1;
  };
  advance();
  try {
    if (request.method === "DELETE") return await (request.body === undefined
      ? apiDelete<unknown>(request.path, request.signal)
      : apiDeleteJSON<unknown>(request.path, request.body, request.signal));
    if (request.method === "PUT") return await apiPut<unknown>(request.path, request.body, request.signal);
    return await apiPost<unknown>(request.path, request.body, request.signal);
  } finally {
    advance();
    if (queryClient && sourcePath) await queryClient.cancelQueries({ queryKey: ["resource", sourcePath], exact: true });
  }
}

export function resourceRows(data: unknown): ResourceActionRow[] {
  if (Array.isArray(data)) return data.filter(isRecord);
  if (!isRecord(data)) return [];
  for (const key of ["items", "data", "results", "secrets"]) {
    const value = data[key];
    if (Array.isArray(value)) return value.filter(isRecord);
  }
  return [];
}

function permissionSnapshotFromUser(currentUser: CurrentUser): PermissionSnapshot {
  return Array.isArray(currentUser.permissions)
    ? Object.freeze({ kind: "ready", permissions: Object.freeze([...currentUser.permissions]) })
    : Object.freeze({ kind: "unavailable" });
}

function freshenIntent(intent: ResourceActionIntent, data: unknown): ResourceActionIntent {
  const template = resourceActionTemplate(intent.id);
  const id = resourceRowID(intent.row);
  if (!template?.route.includes("{id}") || !id || template.operation === "update") return intent;
  const freshRow = resourceRows(data).find((candidate) => resourceRowID(candidate) === id);
  return freshRow ? Object.freeze({
    id: intent.id,
    ...(intent.payload ? { payload: intent.payload } : {}),
    row: Object.freeze({ ...freshRow }),
  }) : intent;
}

function stateSnapshotFromData(
  intent: ResourceActionIntent,
  data: unknown,
  freshness: ResourceActionStateSnapshot["freshness"],
): ResourceActionStateSnapshot {
  const template = resourceActionTemplate(intent.id);
  if (!template || data === undefined) return Object.freeze({ kind: "unknown", freshness });
  if (intent.id === "RES-40") {
    const current = securityAuthority(data), baseline = intent.row ? securityAuthority(intent.row) : undefined;
    if (!current || intent.row && !baseline) return Object.freeze({ kind: "unknown", freshness });
    return Object.freeze({ kind: "ready", freshness, fingerprint: safeFingerprint(current),
      ...(baseline ? { baselineFingerprint: safeFingerprint(baseline) } : {}) });
  }
  const rows = resourceRows(data);
  if (template.route.includes("{id}")) {
    const id = resourceRowID(intent.row);
    const freshRow = rows.find((candidate) => resourceRowID(candidate) === id);
    if (!freshRow) return Object.freeze({ kind: "missing", freshness });
    return Object.freeze({
      kind: "ready", freshness, fingerprint: safeFingerprint(freshRow),
      ...(template.operation === "update" ? { baselineFingerprint: safeFingerprint(intent.row) } : {}),
    });
  }
  if (template.operation === "create" || template.operation === "connect") {
    return Object.freeze({ kind: "ready", freshness, fingerprint: JSON.stringify([template.sourcePath, template.id]) });
  }
  return Object.freeze({
    kind: "ready",
    freshness,
    fingerprint: safeFingerprint(rows.length > 0 ? [...rows].sort(compareRows) : data),
  });
}

function safeFingerprint(value: unknown) {
  if (Array.isArray(value)) return JSON.stringify(value.map(safeFingerprintValue));
  return JSON.stringify(safeFingerprintValue(value));
}

function safeFingerprintValue(value: unknown): unknown {
  if (!isRecord(value)) return value === null || ["string", "number", "boolean"].includes(typeof value) ? value : null;
  const output: Record<string, unknown> = {};
  for (const key of [
    "id", "name", "username", "provider_type", "status", "enabled", "configured", "revision", "updated_at", "created_at",
    "default_role_ids", "role_ids", "password_min_length", "password_hash", "login_lockout_threshold",
    "session_idle_timeout_min", "session_absolute_lifetime_h", "remember_me_enabled", "mfa_mode", "mfa_required_roles",
  ]) {
    if (Object.prototype.hasOwnProperty.call(value, key)) output[key] = safeScalarOrArray(value[key]);
  }
  return output;
}

function safeScalarOrArray(value: unknown) {
  if (Array.isArray(value)) return value.map((item) => typeof item === "string" || typeof item === "number" || typeof item === "boolean" ? item : null)
    .sort((left, right) => JSON.stringify(left).localeCompare(JSON.stringify(right)));
  return typeof value === "string" || typeof value === "number" || typeof value === "boolean" || value === null ? value : null;
}

export function resourceSecurityMetadata(data: unknown): ResourceActionRow | undefined {
  if (!isRecord(data)) return;
  const output: Record<string, unknown> = {};
  for (const key of ["password_min_length", "login_lockout_threshold", "session_idle_timeout_min", "session_absolute_lifetime_h", "remember_me_enabled", "mfa_mode", "mfa_required_roles"]) {
    if (Object.prototype.hasOwnProperty.call(data, key)) output[key] = safeScalarOrArray(data[key]);
  }
  return Object.freeze(output);
}

function securityAuthority(data: unknown) {
  if (!isRecord(data) || typeof data.updated_at !== "string" || !data.updated_at) return;
  return { ...resourceSecurityMetadata(data), updated_at: data.updated_at };
}

function compareRows(left: ResourceActionRow, right: ResourceActionRow) {
  return resourceRowID(left).localeCompare(resourceRowID(right));
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}
