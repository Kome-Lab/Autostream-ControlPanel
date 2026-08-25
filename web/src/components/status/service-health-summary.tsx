"use client";

import Link from "next/link";
import { Activity } from "lucide-react";
import { useI18n } from "@/components/admin/i18n-provider";
import { isServiceAvailable } from "@/lib/service-health";
import { cn } from "@/lib/utils";
import type { WorkerNode } from "@/types/domain";

export type ServiceHealthStatus =
  | { kind: "loading" }
  | { kind: "ready"; rows: WorkerNode[]; refreshing: boolean }
  | { kind: "empty"; refreshing: boolean }
  | { kind: "stale"; rows: WorkerNode[] }
  | { kind: "error" };

export function ServiceHealthSummary({ status, className }: { status: ServiceHealthStatus; className?: string }) {
  const { t } = useI18n();
  const rows = status.kind === "ready" || status.kind === "stale" ? status.rows : [];
  const healthyServices = rows.filter(isServiceAvailable).length;
  const healthy = status.kind === "ready" && healthyServices === rows.length;
  const warning = status.kind === "stale" || (status.kind === "ready" && !healthy);
  const failed = status.kind === "error";
  const refreshing = (status.kind === "ready" || status.kind === "empty") && status.refreshing;
  const label = serviceHealthLabel(status, healthyServices, t);
  const accessibleLabel = refreshing ? `${label}. ${t("serviceHealthRefreshing")}` : label;

  return (
    <Link
      href="/admin/service-health/"
      aria-label={accessibleLabel}
      className={cn(
        "semantic-status-focus flex min-h-9 min-w-0 items-center gap-2 rounded-md border px-3 text-xs font-medium shadow-xs outline-none transition-colors hover:brightness-95 focus-visible:ring-2 focus-visible:ring-offset-2 focus-visible:ring-offset-background",
        healthy && "border-status-healthy-border bg-status-healthy-subtle text-status-healthy-foreground focus-visible:ring-status-healthy-focus",
        warning && "border-status-warning-border bg-status-warning-subtle text-status-warning-foreground focus-visible:ring-status-warning-focus",
        failed && "border-status-critical-border bg-status-critical-subtle text-status-critical-foreground focus-visible:ring-status-critical-focus",
        !healthy && !warning && !failed && "border-status-pending-border bg-status-pending-subtle text-status-pending-foreground focus-visible:ring-status-pending-focus",
        className,
      )}
    >
      <Activity className="size-3.5 shrink-0" aria-hidden="true" />
      <span className="min-w-0 text-center">{label}</span>
      {refreshing ? <span className="sr-only">{t("serviceHealthRefreshing")}</span> : null}
    </Link>
  );
}

type Translate = ReturnType<typeof useI18n>["t"];

function serviceHealthLabel(status: ServiceHealthStatus, healthyServices: number, t: Translate) {
  switch (status.kind) {
    case "loading":
      return t("serviceHealthLoading");
    case "empty":
      return t("serviceHealthEmpty");
    case "error":
      return t("serviceHealthError");
    case "stale":
      return t("serviceHealthStale", { healthy: healthyServices, total: status.rows.length });
    case "ready":
      return t("serviceHealthReady", { healthy: healthyServices, total: status.rows.length });
  }
}
