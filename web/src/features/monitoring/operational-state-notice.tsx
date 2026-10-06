"use client";

import { AlertTriangle, CheckCircle2, LoaderCircle } from "lucide-react";

import { useI18n } from "@/components/admin/i18n-provider";
import { operationalStatePresentation, type OperationalConsumer } from "@/features/monitoring/operational-remote-state";
import type { RemoteState } from "@/lib/foundation/remote-state/contracts";
import { cn } from "@/lib/utils";

export function OperationalStateNotice({ state, consumer }: { state: RemoteState<unknown>; consumer: OperationalConsumer }) {
  const { locale } = useI18n();
  const presentation = operationalStatePresentation(state, consumer, locale);
  const loadedSnapshot = (state.kind === "ready" || state.kind === "empty") && state.freshness.kind !== "stale";
  const refreshing = loadedSnapshot && state.freshness.kind === "refreshing";
  const tone = refreshing ? "loading" : presentation.tone;
  const text = loadedSnapshot
    ? state.kind === "empty"
      ? (locale === "ja" ? "取得済みの結果に対象データはありません。" : "There is no data in the loaded results.")
      : (locale === "ja" ? "取得済みのデータを表示しています。" : "Showing the loaded data snapshot.")
    : presentation.text;
  const id = `${consumer}-remote-state`;
  const Icon = tone === "loading" ? LoaderCircle : tone === "ready" ? CheckCircle2 : AlertTriangle;
  return (
    <div
      id={id}
      role={presentation.tone === "error" ? "alert" : "status"}
      aria-live={presentation.tone === "error" ? "assertive" : "polite"}
      aria-busy={refreshing}
      data-remote-state={state.kind}
      data-freshness={state.kind === "ready" || state.kind === "empty" || state.kind === "partial" ? state.freshness.kind : "none"}
      className={cn(
        "flex items-start gap-2 rounded-md border px-3 py-2 text-sm forced-colors:border-[CanvasText]",
        tone === "ready" && "border-emerald-200 bg-emerald-50/60 text-emerald-800 dark:border-emerald-900 dark:bg-emerald-950/25 dark:text-emerald-200",
        tone === "warning" && "border-amber-300 bg-amber-50 text-amber-900 dark:border-amber-900 dark:bg-amber-950/30 dark:text-amber-200",
        tone === "error" && "border-red-300 bg-red-50 text-red-900 dark:border-red-900 dark:bg-red-950/30 dark:text-red-200",
        tone === "loading" && "bg-muted/30 text-muted-foreground",
      )}
    >
      <Icon className={cn("mt-0.5 size-4 shrink-0", tone === "loading" && "animate-spin motion-reduce:animate-none")} aria-hidden={refreshing ? undefined : true} role={refreshing ? "img" : undefined} aria-label={refreshing ? (locale === "ja" ? "更新中" : "Refreshing") : undefined} />
      <span>{text}</span>
    </div>
  );
}
