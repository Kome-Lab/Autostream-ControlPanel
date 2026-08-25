"use client";

import { AlertTriangle, CheckCircle2, CircleHelp, LoaderCircle } from "lucide-react";
import { useI18n } from "@/components/admin/i18n-provider";
import { cn } from "@/lib/utils";
import type { AppVersion } from "@/types/domain";

export type UpdateStatus =
  | { kind: "loading" }
  | { kind: "ready"; version: AppVersion; refreshing: boolean }
  | { kind: "stale"; version: AppVersion }
  | { kind: "error" };

export function UpdateIndicator({ status }: { status: UpdateStatus }) {
  const { t } = useI18n();
  const presentation = updatePresentation(status, t);
  const Icon = presentation.icon;

  return (
    <div
      role="status"
      className={cn(
        "mt-2 flex min-w-0 items-start gap-1.5 rounded-md border px-2 py-1.5 text-xs",
        presentation.tone === "healthy" && "border-status-healthy-border bg-status-healthy-subtle text-status-healthy-foreground",
        presentation.tone === "pending" && "border-status-pending-border bg-status-pending-subtle text-status-pending-foreground",
        presentation.tone === "warning" && "border-status-warning-border bg-status-warning-subtle text-status-warning-foreground",
        presentation.tone === "critical" && "border-status-critical-border bg-status-critical-subtle text-status-critical-foreground",
      )}
    >
      <Icon className={cn("mt-0.5 size-3.5 shrink-0", status.kind === "ready" && status.refreshing && "animate-spin")} aria-hidden="true" />
      <span className="min-w-0">{presentation.label}</span>
    </div>
  );
}

type Translate = ReturnType<typeof useI18n>["t"];

function updatePresentation(status: UpdateStatus, t: Translate) {
  if (status.kind === "loading") return { label: t("updateLoading"), tone: "pending" as const, icon: LoaderCircle };
  if (status.kind === "error") return { label: t("updateError"), tone: "critical" as const, icon: AlertTriangle };
  if (status.kind === "stale") {
    const available = status.version.update_available && status.version.latest_version;
    const label = available
      ? `${t("updateAvailable", { version: formatVersion(status.version.latest_version) })}. ${t("updateStale")}`
      : t("updateStale");
    return { label, tone: "warning" as const, icon: AlertTriangle };
  }
  if (status.refreshing) return { label: t("updateRefreshing"), tone: "pending" as const, icon: LoaderCircle };
  if (status.version.update_check_error) return { label: t("updateError"), tone: "warning" as const, icon: AlertTriangle };
  if (status.version.update_available && status.version.latest_version) {
    return { label: t("updateAvailable", { version: formatVersion(status.version.latest_version) }), tone: "warning" as const, icon: AlertTriangle };
  }
  if (status.version.update_check_source === "disabled" || !status.version.latest_version) {
    return { label: t("updateUnknown"), tone: "pending" as const, icon: CircleHelp };
  }
  return { label: t("updateCurrent"), tone: "healthy" as const, icon: CheckCircle2 };
}

export function formatVersion(value: string | null | undefined) {
  const normalized = String(value || "dev").trim();
  return normalized.toLowerCase().startsWith("v") ? normalized : `v${normalized}`;
}
