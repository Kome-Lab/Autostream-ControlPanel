"use client";

import { AlertTriangle, CheckCircle2, LoaderCircle } from "lucide-react";

import { useI18n } from "@/components/admin/i18n-provider";
import { operationalStatePresentation, type OperationalConsumer } from "@/features/monitoring/operational-remote-state";
import type { RemoteState } from "@/lib/foundation/remote-state/contracts";
import { cn } from "@/lib/utils";

export function OperationalStateNotice({ state, consumer }: { state: RemoteState<unknown>; consumer: OperationalConsumer }) {
  const { locale } = useI18n();
  const presentation = operationalStatePresentation(state, consumer, locale);
  const id = `${consumer}-remote-state`;
  const Icon = presentation.tone === "loading" ? LoaderCircle : presentation.tone === "ready" ? CheckCircle2 : AlertTriangle;
  return (
    <div
      id={id}
      role={presentation.tone === "error" ? "alert" : "status"}
      aria-live={presentation.tone === "error" ? "assertive" : "polite"}
      data-remote-state={state.kind}
      data-freshness={state.kind === "ready" || state.kind === "empty" || state.kind === "partial" ? state.freshness.kind : "none"}
      className={cn(
        "flex items-start gap-2 rounded-md border px-3 py-2 text-sm forced-colors:border-[CanvasText]",
        presentation.tone === "ready" && "border-emerald-200 bg-emerald-50/60 text-emerald-800 dark:border-emerald-900 dark:bg-emerald-950/25 dark:text-emerald-200",
        presentation.tone === "warning" && "border-amber-300 bg-amber-50 text-amber-900 dark:border-amber-900 dark:bg-amber-950/30 dark:text-amber-200",
        presentation.tone === "error" && "border-red-300 bg-red-50 text-red-900 dark:border-red-900 dark:bg-red-950/30 dark:text-red-200",
        presentation.tone === "loading" && "bg-muted/30 text-muted-foreground",
      )}
    >
      <Icon className={cn("mt-0.5 size-4 shrink-0", presentation.tone === "loading" && "animate-spin motion-reduce:animate-none")} aria-hidden="true" />
      <span>{presentation.text}</span>
    </div>
  );
}
