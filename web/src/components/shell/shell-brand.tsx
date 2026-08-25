import { RadioTower } from "lucide-react";
import { EnvironmentBadge } from "@/components/shell/environment-badge";

export function ShellBrand({ appName, versionLabel }: { appName: string; versionLabel: string }) {
  return (
    <div className="flex min-w-0 items-center gap-3">
      <div className="flex size-9 shrink-0 items-center justify-center rounded-lg bg-sidebar-primary text-sidebar-primary-foreground shadow-sm">
        <RadioTower className="size-5" aria-hidden="true" />
      </div>
      <div className="min-w-0 flex-1">
        <div className="line-clamp-2 text-sm font-semibold leading-tight [overflow-wrap:anywhere]" title={appName}>{appName}</div>
        <div className="mt-0.5 flex flex-wrap items-center gap-1.5 text-xs text-sidebar-foreground/58">
          <span>Live Operations</span>
          <span className="h-3 border-l border-sidebar-border" aria-hidden="true" />
          <span className="shrink-0 font-medium text-sidebar-foreground/80">{versionLabel}</span>
          <EnvironmentBadge />
        </div>
      </div>
    </div>
  );
}
