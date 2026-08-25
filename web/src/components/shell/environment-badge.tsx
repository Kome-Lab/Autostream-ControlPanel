import { Info } from "lucide-react";

export function EnvironmentBadge() {
  if (process.env.NEXT_PUBLIC_AUTOSTREAM_DEMO !== "true") return null;

  return (
    <span className="inline-flex items-center gap-1 rounded-full border border-status-info-border bg-status-info-subtle px-1.5 py-0.5 text-[0.65rem] font-semibold text-status-info-foreground">
      <Info className="size-3" aria-hidden="true" />
      Demo
    </span>
  );
}
