"use client";

import { useMemo } from "react";
import { Clock, Radio, ShieldCheck } from "lucide-react";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Skeleton } from "@/components/ui/skeleton";
import { StatusBadge } from "@/components/admin/status-badge";
import { MetricCard } from "@/components/admin/metric-card";
import { EChartsPanel, type ChartOption } from "@/components/charts/echarts-panel";
import { useAuditLogs, useServiceHealth, useStreams, useWorkerMetrics } from "@/features/queries";
import { useI18n } from "@/components/admin/i18n-provider";
import type { Stream } from "@/types/domain";

export function DashboardView() {
  const { t } = useI18n();
  const streams = useStreams();
  const workers = useServiceHealth();
  const auditLogs = useAuditLogs();
  const workerMetrics = useWorkerMetrics();

  const streamRows = useMemo(() => streams.data || [], [streams.data]);
  const workerRows = useMemo(() => workers.data || [], [workers.data]);
  const active = streamRows.filter((stream) => ["live", "starting"].includes(stream.status)).length;
  const waiting = streamRows.filter((stream) => ["scheduled", "ready", "draft"].includes(stream.status)).length;
  const attention = streamRows.filter((stream) => ["failed", "error"].includes(stream.status)).length;
  const online = workerRows.filter((worker) => worker.status === "online").length;

  const statusOption = useMemo<ChartOption>(() => {
    const counts = countStreamStatus(streamRows);
    return {
      tooltip: { trigger: "item" },
      legend: { bottom: 0 },
      series: [
        {
          type: "pie",
          radius: ["48%", "72%"],
          center: ["50%", "43%"],
          data: [
            { name: "配信中", value: counts.live },
            { name: "予約・準備", value: counts.waiting },
            { name: "要確認", value: counts.attention },
            { name: "停止・完了", value: counts.done },
          ],
        },
      ],
    };
  }, [streamRows]);

  const loadOption = useMemo<ChartOption>(() => {
    const metrics = workerMetrics.data || [];
    return {
      tooltip: { trigger: "axis" },
      legend: { top: 0 },
      grid: { top: 38, right: 16, bottom: 28, left: 38 },
      xAxis: { type: "category", data: metrics.map((point) => point.timestamp) },
      yAxis: { type: "value", min: 0, max: 100 },
      series: [
        { name: "CPU %", type: "line", smooth: true, data: metrics.map((point) => point.cpu_percent) },
        { name: "Memory %", type: "line", smooth: true, data: metrics.map((point) => point.memory_percent) },
      ],
    } as ChartOption;
  }, [workerMetrics.data]);

  if (streams.isLoading || workers.isLoading) {
    return <Skeleton className="h-[520px] w-full" />;
  }

  return (
    <div className="space-y-6">
      <section className="grid gap-4 md:grid-cols-2 xl:grid-cols-4">
        <MetricCard title={t("activeStreams")} value={active} detail="現在の本番配信" tone={active > 0 ? "ok" : "default"} />
        <MetricCard title={t("waitingStreams")} value={waiting} detail="予約・準備済み" />
        <MetricCard title={t("attentionRequired")} value={attention} detail="確認が必要な配信" tone={attention > 0 ? "danger" : "ok"} />
        <MetricCard title={t("onlineNodes")} value={`${online}/${workerRows.length}`} detail="接続中のNode" tone={online === workerRows.length ? "ok" : "warning"} />
      </section>

      <section className="grid gap-4 xl:grid-cols-[1fr_1fr]">
        <EChartsPanel title={t("statusBreakdown")} option={statusOption} />
        <EChartsPanel title={t("workerLoad")} option={loadOption} />
      </section>

      <section className="grid gap-4 xl:grid-cols-[1.2fr_0.8fr]">
        <Card>
          <CardHeader className="pb-2">
            <CardTitle className="flex items-center gap-2 text-base">
              <Clock className="size-4" />
              {t("todaySchedule")}
            </CardTitle>
          </CardHeader>
          <CardContent className="space-y-3">
            {streamRows.map((stream) => (
              <ScheduleRow key={stream.id} stream={stream} />
            ))}
          </CardContent>
        </Card>
        <Card>
          <CardHeader className="pb-2">
            <CardTitle className="flex items-center gap-2 text-base">
              <ShieldCheck className="size-4" />
              {t("recentAudit")}
            </CardTitle>
          </CardHeader>
          <CardContent className="space-y-3">
            {(auditLogs.data || []).slice(0, 5).map((event) => (
              <div key={event.id} className="rounded-md border p-3">
                <div className="flex items-center justify-between gap-3">
                  <span className="font-medium">{event.action}</span>
                  <StatusBadge status={event.result} />
                </div>
                <p className="mt-1 text-sm text-muted-foreground">
                  {event.actor_username || "-"} / {formatDateTime(event.timestamp)}
                </p>
              </div>
            ))}
          </CardContent>
        </Card>
      </section>

      <section className="rounded-lg border bg-card p-4">
        <div className="flex items-start gap-3">
          <div className="mt-0.5 rounded-md bg-emerald-50 p-2 text-emerald-700">
            <Radio className="size-5" />
          </div>
          <div>
            <h2 className="font-semibold">{t("systemReady")}</h2>
            <p className="mt-1 text-sm text-muted-foreground">{t("noSpecializedTerms")}</p>
          </div>
        </div>
      </section>
    </div>
  );
}

function ScheduleRow({ stream }: { stream: Stream }) {
  return (
    <div className="grid gap-3 rounded-md border p-3 md:grid-cols-[minmax(0,1fr)_140px_140px] md:items-center">
      <div>
        <div className="font-medium">{stream.name}</div>
        <div className="text-sm text-muted-foreground">
          {stream.input_source || "-"} -&gt; {stream.output_target || "-"}
        </div>
      </div>
      <div className="text-sm text-muted-foreground">{formatTimeRange(stream.scheduled_start_at, stream.scheduled_end_at)}</div>
      <StatusBadge status={stream.status} showDetail />
    </div>
  );
}

function countStreamStatus(streams: Stream[]) {
  return streams.reduce(
    (counts, stream) => {
      if (["live", "starting"].includes(stream.status)) counts.live += 1;
      else if (["scheduled", "ready", "draft"].includes(stream.status)) counts.waiting += 1;
      else if (["failed", "error"].includes(stream.status)) counts.attention += 1;
      else counts.done += 1;
      return counts;
    },
    { live: 0, waiting: 0, attention: 0, done: 0 },
  );
}

function formatTimeRange(start?: string, end?: string) {
  return `${formatTime(start)} - ${formatTime(end)}`;
}

function formatTime(value?: string) {
  if (!value) return "--:--";
  return new Intl.DateTimeFormat("ja-JP", { hour: "2-digit", minute: "2-digit" }).format(new Date(value));
}

function formatDateTime(value?: string) {
  if (!value) return "-";
  return new Intl.DateTimeFormat("ja-JP", { month: "2-digit", day: "2-digit", hour: "2-digit", minute: "2-digit" }).format(new Date(value));
}
