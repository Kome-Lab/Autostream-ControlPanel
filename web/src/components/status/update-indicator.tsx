import { AlertTriangle } from "lucide-react";
import type { AppVersion } from "@/types/domain";

export function UpdateIndicator({ version }: { version?: AppVersion }) {
  const updateAvailable = Boolean(version?.update_available && version.latest_version);
  const updateCheckFailed = !updateAvailable && Boolean(version?.update_check_error);
  if (!updateAvailable && !updateCheckFailed) return null;

  return (
    <div
      role="status"
      className="mt-2 flex items-start gap-1.5 rounded-md border border-status-warning-border bg-status-warning-subtle px-2 py-1.5 text-xs text-status-warning-foreground"
    >
      <AlertTriangle className="mt-0.5 size-3.5 shrink-0" aria-hidden="true" />
      <span>{updateAvailable ? `更新 ${formatVersion(version?.latest_version)} を利用できます` : "更新情報を確認できません"}</span>
    </div>
  );
}

export function formatVersion(value: string | null | undefined) {
  const normalized = String(value || "dev").trim();
  return normalized.toLowerCase().startsWith("v") ? normalized : `v${normalized}`;
}
