"use client";

import { Activity } from "lucide-react";
import { formatDateTimeInTimeZone } from "@/lib/timezone";
import type { WorkerNode } from "@/types/domain";

export function formatHeartbeat(node: WorkerNode, timezone?: string) {
  if (typeof node.heartbeat_age_sec === "number") return `${node.heartbeat_age_sec} sec`;
  if (node.last_heartbeat_at) return formatNodeDateTime(node.last_heartbeat_at, timezone);
  return "-";
}

export function formatNodeDateTime(value?: string, timezone?: string) {
  return formatDateTimeInTimeZone(value, timezone, { year: "numeric", month: "2-digit", day: "2-digit", hour: "2-digit", minute: "2-digit" });
}

export function nodeReportedPlatform(node: WorkerNode) {
  const os = node.reported_os || (node.configure_token_used_at ? "OS未取得" : "OS未取得（Configure待ち）");
  const arch = node.reported_arch || (node.configure_token_used_at ? "Arch未取得" : "Arch未取得（Configure待ち）");
  return `${os} / ${arch}`;
}

export function NodeMetricsSummary({ node }: { node: WorkerNode }) {
  const metrics = node.metrics || {};
  const entries = Object.entries(metrics).filter(([, value]) => value !== "" && value !== null && value !== undefined);
  if (node.service_type === "observability") {
    const uptime = metricValue(metrics, ["observability.uptime_seconds"]);
    const goroutines = metricValue(metrics, ["observability.goroutines"]);
    const heap = metricValue(metrics, ["observability.heap_alloc_bytes", "observability.heap_sys_bytes"]);
    return (
      <div className="min-w-0 text-sm">
        <div className="flex items-center gap-1.5">
          <Activity className="size-3.5 text-muted-foreground" />
          {entries.length > 0 ? `${entries.length}項目` : "未受信"}
        </div>
        <div className="text-xs text-muted-foreground">
          UP {formatMetricDuration(uptime)} / Go {formatMetricCount(goroutines)}
        </div>
        <div className="text-xs text-muted-foreground">Heap {formatMetricBytes(heap)}</div>
      </div>
    );
  }
  const cpu = metricValue(metrics, ["cpu_percent", "cpuUsage", "process.cpu_percent"]);
  const memory = metricValue(metrics, ["memory_percent", "memoryUsage", "process.memory_percent"]);
  return (
    <div className="min-w-0 text-sm">
      <div className="flex items-center gap-1.5">
        <Activity className="size-3.5 text-muted-foreground" />
        {entries.length > 0 ? `${entries.length}項目` : "未受信"}
      </div>
      <div className="text-xs text-muted-foreground">
        CPU {formatMetricPercent(cpu)} / MEM {formatMetricPercent(memory)}
      </div>
    </div>
  );
}

function metricValue(metrics: Record<string, number | string>, keys: string[]) {
  for (const key of keys) {
    const value = metrics[key];
    if (typeof value === "number" && Number.isFinite(value)) return value;
    if (typeof value === "string" && value.trim() !== "" && Number.isFinite(Number(value))) return Number(value);
  }
  return undefined;
}

function formatMetricPercent(value?: number) {
  if (typeof value !== "number") return "-";
  return `${Math.round(value * 10) / 10}%`;
}

function formatMetricCount(value?: number) {
  if (typeof value !== "number") return "-";
  return String(Math.round(value));
}

function formatMetricDuration(value?: number) {
  if (typeof value !== "number") return "-";
  if (value < 60) return `${Math.round(value)}s`;
  if (value < 3600) return `${Math.round(value / 60)}m`;
  return `${Math.round(value / 3600)}h`;
}

function formatMetricBytes(value?: number) {
  if (typeof value !== "number") return "-";
  if (value < 1024 * 1024) return `${Math.round(value / 1024)}KiB`;
  if (value < 1024 * 1024 * 1024) return `${Math.round((value / 1024 / 1024) * 10) / 10}MiB`;
  return `${Math.round((value / 1024 / 1024 / 1024) * 10) / 10}GiB`;
}
