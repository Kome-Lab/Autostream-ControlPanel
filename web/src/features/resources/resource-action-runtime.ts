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
import { apiDelete, apiGet, apiPost, apiPut } from "@/lib/api/client";
import type { PermissionSnapshot } from "@/lib/foundation/permissions/evaluator";
import type { CurrentUser } from "@/types/domain";

export function resourcePermissionSnapshot(queryClient: QueryClient): PermissionSnapshot {
  const state = queryClient.getQueryState<CurrentUser>(["auth", "me"]);
  const current = queryClient.getQueryData<CurrentUser>(["auth", "me"]);
  if (state?.fetchStatus === "fetching") return Object.freeze({ kind: "refreshing" });
  if (state?.status !== "success" || !current || !Array.isArray(current.permissions)) return Object.freeze({ kind: "unavailable" });
  return permissionSnapshotFromUser(current);
}

export function resourceActionStateSnapshot(queryClient: QueryClient, intent: ResourceActionIntent): ResourceActionStateSnapshot {
  const sourcePath = resourceActionSourcePath(intent);
  if (!sourcePath) return Object.freeze({ kind: "unknown", freshness: "unavailable" });
  const state = queryClient.getQueryState<unknown>(["resource", sourcePath]);
  const data = queryClient.getQueryData<unknown>(["resource", sourcePath]);
  const freshness = state?.fetchStatus === "fetching"
    ? "refreshing" as const
    : state?.status === "error" && data !== undefined
      ? "stale" as const
      : state?.status === "success" && data !== undefined
        ? "fresh" as const
        : "unavailable" as const;
  return stateSnapshotFromData(intent, data, freshness);
}

export async function refreshResourceAction(queryClient: QueryClient, intent: ResourceActionIntent): Promise<ResourceActionAuthority> {
  const sourcePath = resourceActionSourcePath(intent);
  if (!sourcePath) throw new Error("resource action source is unavailable");
  const [data, currentUser] = await Promise.all([
    apiGet<unknown>(sourcePath),
    apiGet<CurrentUser>("/auth/me"),
  ]);
  queryClient.setQueryData(["resource", sourcePath], data);
  queryClient.setQueryData(["auth", "me"], currentUser);
  const freshIntent = freshenIntent(intent, data);
  return Object.freeze({
    intent: freshIntent,
    permissions: permissionSnapshotFromUser(currentUser),
    state: stateSnapshotFromData(freshIntent, data, "fresh"),
  });
}

export async function mutateResourceAction(request: ResourceActionRequest & Readonly<{ signal: AbortSignal }>) {
  if (request.signal.aborted) throw new DOMException("Cancelled", "AbortError");
  if (request.method === "DELETE") return apiDelete<unknown>(request.path);
  if (request.method === "PUT") return apiPut<unknown>(request.path, request.body);
  return apiPost<unknown>(request.path, request.body);
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
  if (!template?.route.includes("{id}") || !id) return intent;
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
    return isRecord(data)
      ? Object.freeze({ kind: "ready", freshness, fingerprint: safeFingerprint(data) })
      : Object.freeze({ kind: "unknown", freshness });
  }
  const rows = resourceRows(data);
  if (template.route.includes("{id}")) {
    const id = resourceRowID(intent.row);
    const freshRow = rows.find((candidate) => resourceRowID(candidate) === id);
    if (!freshRow) return Object.freeze({ kind: "missing", freshness });
    return Object.freeze({ kind: "ready", freshness, fingerprint: safeFingerprint(freshRow) });
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
  if (Array.isArray(value)) return value.map((item) => typeof item === "string" || typeof item === "number" || typeof item === "boolean" ? item : null);
  return typeof value === "string" || typeof value === "number" || typeof value === "boolean" || value === null ? value : null;
}

function compareRows(left: ResourceActionRow, right: ResourceActionRow) {
  return resourceRowID(left).localeCompare(resourceRowID(right));
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}
