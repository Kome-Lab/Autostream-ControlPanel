import Link from "next/link";
import { Activity } from "lucide-react";
import { isServiceAvailable } from "@/lib/service-health";
import { cn } from "@/lib/utils";
import type { WorkerNode } from "@/types/domain";

export function ServiceHealthSummary({ rows = [], className }: { rows?: WorkerNode[]; className?: string }) {
  const healthyServices = rows.filter(isServiceAvailable).length;
  const healthy = rows.length > 0 && healthyServices === rows.length;
  const warning = rows.length > 0 && !healthy;
  const label = rows.length > 0 ? `${healthyServices}/${rows.length} サービス稼働` : "稼働状況を確認中";

  return (
    <Link
      href="/admin/service-health/"
      className={cn(
        "flex min-h-9 items-center gap-2 rounded-md border px-3 text-xs font-medium shadow-xs outline-none transition-colors hover:brightness-95 focus-visible:ring-2",
        healthy && "border-status-healthy-border bg-status-healthy-subtle text-status-healthy-foreground focus-visible:ring-status-healthy-focus",
        warning && "border-status-warning-border bg-status-warning-subtle text-status-warning-foreground focus-visible:ring-status-warning-focus",
        !healthy && !warning && "border-status-pending-border bg-status-pending-subtle text-status-pending-foreground focus-visible:ring-status-pending-focus",
        className,
      )}
    >
      <Activity className="size-3.5 shrink-0" aria-hidden="true" />
      <span>{label}</span>
    </Link>
  );
}
