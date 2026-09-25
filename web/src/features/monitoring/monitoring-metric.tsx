"use client";

import type { ComponentProps } from "react";
import { MetricCard } from "@/components/admin/metric-card";
import type { RemoteState } from "@/lib/foundation/remote-state/contracts";
import { useUICopy } from "@/lib/i18n/ui-v2/use-ui-copy";

// Presentation only: the existing query projection and domain counters remain
// the authorities for availability, freshness and measured values.
export function MonitoringMetric({ state, value, detail, tone, ...props }: ComponentProps<typeof MetricCard> & { state: RemoteState<unknown> }) {
  const uiText = useUICopy();
  const error = state.kind === "blocking-error" ? state.error
    : state.freshness?.kind === "stale" ? state.freshness.error : undefined;
  const denied = error?.kind === "forbidden" || error?.kind === "unauthenticated";
  const available = !denied && (state.kind === "ready" || state.kind === "empty");
  const explanation = denied ? uiText("この情報を参照する権限がありません。")
    : state.kind === "initial-loading" ? uiText("この情報は取得中です。")
      : !available ? uiText("この情報を取得できません。")
        : state.freshness?.kind === "stale" ? uiText("前回取得した値です。更新に失敗しました。")
          : state.freshness?.kind === "refreshing" ? uiText("取得済みの値を表示しながら更新中です。")
            : state.kind === "empty" ? uiText("取得済みですが、対象データはありません。") : "";
  return <MetricCard {...props} value={available ? value : "—"}
    tone={!available ? "default" : state.freshness?.kind === "fresh" ? tone : tone === "danger" ? "danger" : "warning"}
    detail={available ? [explanation, detail].filter(Boolean).join(" ") : explanation} />;
}
