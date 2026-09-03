import type { QueryClient } from "@tanstack/react-query";

import type {
  AppSettingsActionAuthority,
  AppSettingsActionIntent,
  AppSettingsActionRequest,
  AppSettingsActionState,
} from "@/features/settings/app-settings-action-policy";
import { apiGet, apiPost, apiPut } from "@/lib/api/client";
import type { PermissionSnapshot } from "@/lib/foundation/permissions/evaluator";
import { projectRemoteState, type QueryProjectionSnapshot } from "@/lib/foundation/remote-state/projector";
import type { RemoteState } from "@/lib/foundation/remote-state/contracts";
import type { CurrentUser, ManagedAppSettings } from "@/types/domain";

const queryKey = ["settings", "app", "manage"] as const;

export function appSettingsPermissionSnapshot(queryClient: QueryClient): PermissionSnapshot {
  const state = queryClient.getQueryState<CurrentUser>(["auth", "me"]);
  const value = queryClient.getQueryData<CurrentUser>(["auth", "me"]);
  if (state?.fetchStatus === "fetching") return Object.freeze({ kind: "refreshing" });
  return state?.status === "success" && value && Array.isArray(value.permissions)
    ? Object.freeze({ kind: "ready", permissions: Object.freeze([...value.permissions]) })
    : Object.freeze({ kind: "unavailable" });
}

export function appSettingsStateSnapshot(queryClient: QueryClient): AppSettingsActionState {
  const state = queryClient.getQueryState<ManagedAppSettings>(queryKey);
  const data = queryClient.getQueryData<ManagedAppSettings>(queryKey);
  const freshness = state?.fetchStatus === "fetching"
    ? "refreshing" as const
    : state?.status === "error" && data
      ? "stale" as const
      : state?.status === "success" && data
        ? "fresh" as const
        : "unavailable" as const;
  return data
    ? Object.freeze({ kind: "ready", freshness, fingerprint: settingsFingerprint(data) })
    : Object.freeze({ kind: "unknown", freshness });
}

export async function refreshAppSettingsAction(queryClient: QueryClient): Promise<AppSettingsActionAuthority> {
  const [settings, currentUser] = await Promise.all([
    apiGet<ManagedAppSettings>("/settings/app/manage"),
    apiGet<CurrentUser>("/auth/me"),
  ]);
  queryClient.setQueryData(queryKey, settings);
  queryClient.setQueryData(["auth", "me"], currentUser);
  return Object.freeze({
    permissions: Array.isArray(currentUser.permissions)
      ? Object.freeze({ kind: "ready", permissions: Object.freeze([...currentUser.permissions]) })
      : Object.freeze({ kind: "unavailable" }),
    state: Object.freeze({ kind: "ready", freshness: "fresh", fingerprint: settingsFingerprint(settings) }),
  });
}

export async function mutateAppSettingsAction(request: AppSettingsActionRequest & Readonly<{ signal: AbortSignal }>) {
  if (request.signal.aborted) throw new DOMException("Cancelled", "AbortError");
  return request.method === "PUT"
    ? apiPut<unknown>(request.path, request.body)
    : apiPost<unknown>(request.path, request.body);
}

export function appSettingsIntent(id: AppSettingsActionIntent["id"], payload: Record<string, unknown>): AppSettingsActionIntent {
  return Object.freeze({ id, payload: Object.freeze({ ...payload }) });
}

export function managedAppSettingsRemoteState(query: Readonly<{
  status: "pending" | "success" | "error";
  isFetching: boolean;
  data?: ManagedAppSettings;
  error?: unknown;
  dataUpdatedAt: number;
}>): RemoteState<ManagedAppSettings> {
  const snapshot: QueryProjectionSnapshot<ManagedAppSettings> = query.status === "pending"
    ? { status: "pending", fetching: query.isFetching }
    : query.status === "success"
      ? { status: "success", fetching: query.isFetching, data: query.data as ManagedAppSettings, dataUpdatedAt: query.dataUpdatedAt }
      : query.data === undefined
        ? { status: "error", fetching: query.isFetching, error: query.error }
        : { status: "error", fetching: query.isFetching, error: query.error, data: query.data, dataUpdatedAt: query.dataUpdatedAt };
  return projectRemoteState(snapshot, { classifyData: () => "ready" });
}

function settingsFingerprint(settings: ManagedAppSettings) {
  const value = settings as ManagedAppSettings & Record<string, unknown>;
  const safe: Record<string, unknown> = {};
  for (const key of [
    "app_name", "timezone", "smtp_enabled", "smtp_host", "smtp_port", "smtp_starttls", "smtp_from", "smtp_username",
    "smtp_password_configured", "turnstile_enabled", "turnstile_site_key", "turnstile_configured",
    "google_analytics_enabled", "google_analytics_measurement_id", "updated_at",
  ]) {
    const candidate = value[key];
    if (candidate === null || ["string", "number", "boolean"].includes(typeof candidate)) safe[key] = candidate;
  }
  return JSON.stringify(safe);
}
