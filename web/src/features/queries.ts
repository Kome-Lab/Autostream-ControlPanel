import { useQuery } from "@tanstack/react-query";
import { apiGet } from "@/lib/api/client";
import type { AppSettings, AppVersion, AuditLog, CurrentUser, MetricSnapshot, SetupStatus, Stream, WorkerNode } from "@/types/domain";

export function useCurrentUser() {
  return useQuery({
    queryKey: ["auth", "me"],
    queryFn: () => apiGet<CurrentUser>("/auth/me"),
    retry: false,
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

export function useStreams() {
  return useQuery({
    queryKey: ["streams"],
    queryFn: () => apiGet<Stream[]>("/streams"),
  });
}

export function useWorkers() {
  return useQuery({
    queryKey: ["workers"],
    queryFn: () => apiGet<WorkerNode[]>("/workers"),
    refetchInterval: 10_000,
  });
}

export function useServiceHealth() {
  return useQuery({
    queryKey: ["service-health"],
    queryFn: () => apiGet<WorkerNode[]>("/service-health"),
    refetchInterval: 10_000,
  });
}

export function useNodes() {
  return useQuery({
    queryKey: ["nodes"],
    queryFn: () => apiGet<WorkerNode[]>("/nodes"),
    refetchInterval: 10_000,
  });
}

export function useAuditLogs(params?: { from?: string; to?: string; action?: string; result?: string }) {
  const search = new URLSearchParams();
  if (params?.from) search.set("from", params.from);
  if (params?.to) search.set("to", params.to);
  if (params?.action) search.set("action", params.action);
  if (params?.result && params.result !== "all") search.set("result", params.result);
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

export function useResourceData<T = unknown>(path: string) {
  return useQuery({
    queryKey: ["resource", path],
    queryFn: () => apiGet<T>(path),
  });
}
