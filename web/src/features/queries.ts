import { useQuery } from "@tanstack/react-query";
import { apiGet } from "@/lib/api/client";
import type { AppSettings, AppVersion, AuditLog, CurrentUser, MetricSnapshot, SetupStatus, Stream, WorkerNode } from "@/types/domain";

export function useCurrentUser() {
  return useQuery({
    queryKey: ["auth", "me"],
    queryFn: () => apiGet<CurrentUser>("/auth/me"),
    retry: false,
    refetchInterval: 15_000,
    refetchIntervalInBackground: true,
    refetchOnWindowFocus: "always",
  });
}

export function useSetupStatus() {
  return useQuery({
    queryKey: ["setup", "status"],
    queryFn: () => apiGet<SetupStatus>("/setup/status"),
    retry: false,
  });
}

export function useAppSettings() {
  return useQuery({
    queryKey: ["settings", "app"],
    queryFn: () => apiGet<AppSettings>("/settings/app"),
  });
}

export function useVersion() {
  return useQuery({
    queryKey: ["version"],
    queryFn: () => apiGet<AppVersion>("/version"),
  });
}

export function useStreams(enabled = true) {
  return useQuery({
    queryKey: ["streams"],
    queryFn: () => apiGet<Stream[]>("/streams"),
    enabled,
  });
}

export function useWorkers() {
  return useQuery({
    queryKey: ["workers"],
    queryFn: () => apiGet<WorkerNode[]>("/workers"),
    refetchInterval: 10_000,
  });
}

export function useServiceHealth(enabled = true) {
  return useQuery({
    queryKey: ["service-health"],
    queryFn: () => apiGet<WorkerNode[]>("/service-health"),
    refetchInterval: 10_000,
    enabled,
  });
}

export function useNodes(enabled = true) {
  return useQuery({
    queryKey: ["nodes"],
    queryFn: () => apiGet<WorkerNode[]>("/nodes"),
    refetchInterval: 10_000,
    enabled,
  });
}

export function useAuditLogs(params?: { from?: string; to?: string; action?: string; actionGroup?: string; excludeActionGroup?: string; result?: string; q?: string }) {
  const search = new URLSearchParams();
  if (params?.from) search.set("from", params.from);
  if (params?.to) search.set("to", params.to);
  if (params?.action) search.set("action", params.action);
  if (params?.actionGroup) search.set("action_group", params.actionGroup);
  if (params?.excludeActionGroup) search.set("exclude_action_group", params.excludeActionGroup);
  if (params?.result && params.result !== "all") search.set("result", params.result);
  if (params?.q) search.set("q", params.q);
  const suffix = search.toString() ? `?${search}` : "";
  return useQuery({
    queryKey: ["audit-logs", params],
    queryFn: () => apiGet<AuditLog[]>(`/audit-logs${suffix}`),
  });
}

export function useWorkerMetrics() {
  return useQuery({
    queryKey: ["observability", "metrics"],
    queryFn: () => apiGet<MetricSnapshot[]>("/observability/metrics"),
    refetchInterval: 10_000,
  });
}

export function useResourceData<T = unknown>(path: string, enabled = true) {
  return useQuery({
    queryKey: ["resource", path],
    queryFn: () => apiGet<T>(path),
    enabled,
  });
}
