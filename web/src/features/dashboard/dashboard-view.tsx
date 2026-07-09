"use client";

import { useMemo } from "react";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Skeleton } from "@/components/ui/skeleton";
import { MetricCard } from "@/components/admin/metric-card";
import { EChartsPanel, type ChartOption } from "@/components/charts/echarts-panel";
import { useServiceHealth, useStreams } from "@/features/queries";
import { useI18n } from "@/components/admin/i18n-provider";
import type { Stream } from "@/types/domain";

export function DashboardView() {
  const { t } = useI18n();
  const streams = useStreams();
  const workers = useServiceHealth();

  const streamRows = useMemo(() => streams.data || [], [streams.data]);
  const workerRows = useMemo(() => workers.data || [], [workers.data]);
  const statusCounts = useMemo(() => countStreamStatus(streamRows), [streamRows]);
  const active = statusCounts.live;
  const waiting = statusCounts.waiting;
  const attention = statusCounts.attention;
  const online = workerRows.filter((worker) => worker.status === "online").length;
  const showStatusBreakdown = statusCounts.live + statusCounts.waiting + statusCounts.attention > 0;

  const statusOption = useMemo<ChartOption>(() => {
    return {
      tooltip: { trigger: "item" },
      legend: { bottom: 0 },
      series: [
        {
          type: "pie",
          radius: ["48%", "72%"],
          center: ["50%", "43%"],
          data: [
            { name: "配信中", value: statusCounts.live },
            { name: "待機中", value: statusCounts.waiting },
            { name: "要確認", value: statusCounts.attention },
            { name: "停止・完了", value: statusCounts.done },
          ],
        },
      ],
    };
  }, [statusCounts]);

  if (streams.isLoading || workers.isLoading) {
    return <Skeleton className="h-[520px] w-full" />;
  }

  return (
    <div className="space-y-6">
      <section className="grid gap-4 md:grid-cols-2 xl:grid-cols-4">
        <MetricCard title={t("activeStreams")} value={active} detail="現在の本番配信" tone={active > 0 ? "ok" : "default"} />
        <MetricCard title={t("waitingStreams")} value={waiting} detail="開始待ちの配信枠" />
        <MetricCard title={t("attentionRequired")} value={attention} detail="確認が必要な配信" tone={attention > 0 ? "danger" : "ok"} />
        <MetricCard title={t("onlineNodes")} value={`${online}/${workerRows.length}`} detail="接続中のNode" tone={workerRows.length > 0 && online === workerRows.length ? "ok" : "warning"} />
      </section>

      <section>
        {showStatusBreakdown ? <EChartsPanel title={t("statusBreakdown")} option={statusOption} /> : <EmptyPanel title={t("statusBreakdown")} message="進行中または待機中の配信枠はありません。" />}
      </section>
    </div>
  );
}

function EmptyPanel({ title, message }: { title: string; message: string }) {
  return (
    <Card>
      <CardHeader className="pb-2">
        <CardTitle className="text-base">{title}</CardTitle>
      </CardHeader>
      <CardContent>
        <div className="rounded-md border border-dashed p-6 text-sm text-muted-foreground">{message}</div>
      </CardContent>
    </Card>
  );
}

function countStreamStatus(streams: Stream[]) {
  return streams.reduce(
    (counts, stream) => {
      if (["live", "starting"].includes(stream.status)) counts.live += 1;
      else if (["created", "scheduled", "ready", "draft"].includes(stream.status)) counts.waiting += 1;
      else if (["failed", "error"].includes(stream.status)) counts.attention += 1;
      else counts.done += 1;
      return counts;
    },
    { live: 0, waiting: 0, attention: 0, done: 0 },
  );
}
