import { useQuery } from "@tanstack/react-query";
import { apiGet } from "@/lib/api/client";
import type { AuditLog, CurrentUser, MetricPoint, Stream, WorkerNode } from "@/types/domain";

export function useCurrentUser() {
  return useQuery({
    queryKey: ["auth", "me"],
    queryFn: () => apiGet<CurrentUser>("/auth/me"),
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
  });
}

export function useServiceHealth() {
  return useQuery({
    queryKey: ["service-health"],
    queryFn: () => apiGet<WorkerNode[]>("/service-health"),
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
    queryFn: () => apiGet<MetricPoint[]>("/observability/metrics"),
  });
}
