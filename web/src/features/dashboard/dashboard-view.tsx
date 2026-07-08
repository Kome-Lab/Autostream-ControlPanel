"use client";

import { useMemo } from "react";
import { Clock, ShieldCheck } from "lucide-react";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Skeleton } from "@/components/ui/skeleton";
import { StatusBadge } from "@/components/admin/status-badge";
import { MetricCard } from "@/components/admin/metric-card";
import { EChartsPanel, type ChartOption } from "@/components/charts/echarts-panel";
import { useAuditLogs, useAppSettings, useServiceHealth, useStreams, useWorkerMetrics } from "@/features/queries";
import { useI18n } from "@/components/admin/i18n-provider";
import { formatDateTimeInTimeZone, formatTimeInTimeZone, normalizeTimeZone } from "@/lib/timezone";
import type { MetricSnapshot, Stream } from "@/types/domain";

export function DashboardView() {
  const { t } = useI18n();
  const streams = useStreams();
  const workers = useServiceHealth();
  const auditLogs = useAuditLogs();
  const workerMetrics = useWorkerMetrics();
  const appSettings = useAppSettings();
  const timezone = appSettings.data?.timezone;

  const streamRows = useMemo(() => streams.data || [], [streams.data]);
  const workerRows = useMemo(() => workers.data || [], [workers.data]);
  const active = streamRows.filter((stream) => ["live", "starting"].includes(stream.status)).length;
  const waiting = streamRows.filter((stream) => ["created", "scheduled", "ready", "draft"].includes(stream.status)).length;
  const attention = streamRows.filter((stream) => ["failed", "error"].includes(stream.status)).length;
  const online = workerRows.filter((worker) => worker.status === "online").length;
  const todayStreams = useMemo(() => streamRowsForTodaySchedule(streamRows, timezone), [streamRows, timezone]);
  const visibleMetrics = useMemo(() => latestNumericMetrics(workerMetrics.data || []), [workerMetrics.data]);
  const statusCounts = useMemo(() => countStreamStatus(streamRows), [streamRows]);
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
            { name: "予約・準備", value: statusCounts.waiting },
            { name: "要確認", value: statusCounts.attention },
            { name: "停止・完了", value: statusCounts.done },
          ],
        },
      ],
    };
  }, [statusCounts]);

  const loadOption = useMemo<ChartOption>(() => {
    return {
      tooltip: { trigger: "axis" },
      grid: { top: 18, right: 16, bottom: 78, left: 54 },
      xAxis: {
        type: "category",
        data: visibleMetrics.map(metricLabel),
        axisLabel: { interval: 0, rotate: 28 },
      },
      yAxis: { type: "value", min: 0 },
      series: [
        { name: "最新値", type: "bar", data: visibleMetrics.map((point) => point.value ?? 0) },
      ],
    } as ChartOption;
  }, [visibleMetrics]);

  if (streams.isLoading || workers.isLoading) {
    return <Skeleton className="h-[520px] w-full" />;
  }

  return (
    <div className="space-y-6">
      <section className="grid gap-4 md:grid-cols-2 xl:grid-cols-4">
        <MetricCard title={t("activeStreams")} value={active} detail="現在の本番配信" tone={active > 0 ? "ok" : "default"} />
        <MetricCard title={t("waitingStreams")} value={waiting} detail="予約・準備済み" />
        <MetricCard title={t("attentionRequired")} value={attention} detail="確認が必要な配信" tone={attention > 0 ? "danger" : "ok"} />
        <MetricCard title={t("onlineNodes")} value={`${online}/${workerRows.length}`} detail="接続中のNode" tone={workerRows.length > 0 && online === workerRows.length ? "ok" : "warning"} />
      </section>

      <section className="grid gap-4 xl:grid-cols-[1fr_1fr]">
        {showStatusBreakdown ? <EChartsPanel title={t("statusBreakdown")} option={statusOption} /> : <EmptyPanel title={t("statusBreakdown")} message="進行中または待機中の配信枠はありません。" />}
        {visibleMetrics.length > 0 ? <EChartsPanel title={t("workerLoad")} option={loadOption} /> : <EmptyPanel title={t("workerLoad")} message="Metricsはまだ受信されていません。" />}
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
            {todayStreams.length === 0 ? <div className="rounded-md border border-dashed p-4 text-sm text-muted-foreground">本日の配信枠はありません。</div> : null}
            {todayStreams.map((stream) => (
              <ScheduleRow key={stream.id} stream={stream} timezone={timezone} />
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
                  {event.actor_username || "-"} / {formatDateTime(event.timestamp, timezone)}
                </p>
              </div>
            ))}
          </CardContent>
        </Card>
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

function ScheduleRow({ stream, timezone }: { stream: Stream; timezone?: string }) {
  return (
    <div className="grid gap-3 rounded-md border p-3 md:grid-cols-[minmax(0,1fr)_140px_140px] md:items-center">
      <div>
        <div className="font-medium">{stream.name}</div>
        <div className="text-sm text-muted-foreground">
          {stream.input_source || "-"} -&gt; {stream.output_target || "-"}
        </div>
      </div>
      <div className="text-sm text-muted-foreground">{formatTimeRange(stream.scheduled_start_at, stream.scheduled_end_at, timezone)}</div>
      <StatusBadge status={stream.status} showDetail />
    </div>
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

function isTodaySchedule(stream: Stream, timezone?: string) {
  const reference = stream.scheduled_start_at || stream.started_at;
  if (!reference) return ["draft", "ready", "scheduled", "starting", "live"].includes(stream.status);
  return dateKeyInTimeZone(reference, timezone) === dateKeyInTimeZone(new Date().toISOString(), timezone);
}

function streamRowsForTodaySchedule(streams: Stream[], timezone?: string) {
  const today = streams.filter((stream) => isTodaySchedule(stream, timezone)).sort(compareScheduleStart);
  if (today.length > 0) return today;
  return streams.filter((stream) => ["draft", "created", "ready", "scheduled", "starting", "live"].includes(stream.status)).sort(compareScheduleStart).slice(0, 5);
}

function compareScheduleStart(a: Stream, b: Stream) {
  return scheduleTime(a) - scheduleTime(b);
}

function scheduleTime(stream: Stream) {
  const value = stream.scheduled_start_at || stream.started_at || "";
  const time = Date.parse(value);
  return Number.isNaN(time) ? Number.MAX_SAFE_INTEGER : time;
}

function latestNumericMetrics(metrics: MetricSnapshot[]) {
  return metrics
    .filter((metric) => typeof metric.value === "number" && Number.isFinite(metric.value))
    .sort((a, b) => metricRank(a) - metricRank(b) || Date.parse(b.updated_at) - Date.parse(a.updated_at))
    .slice(0, 10);
}

function metricRank(metric: MetricSnapshot) {
  const name = metric.name.toLowerCase();
  if (name.includes("cpu")) return 0;
  if (name.includes("memory") || name.includes("mem")) return 1;
  if (name.includes("process_alive") || name.includes("active")) return 2;
  if (name.startsWith("observability.")) return 3;
  return 4;
}

function metricLabel(metric: MetricSnapshot) {
  const service = metric.service_id || metric.service_type || "-";
  const name = metric.name.replace(/^observability\./, "o11y.");
  return `${service}\n${name}`;
}

function formatTimeRange(start?: string, end?: string, timezone?: string) {
  return `${formatTime(start, timezone)} - ${formatTime(end, timezone)}`;
}

function formatTime(value?: string, timezone?: string) {
  return formatTimeInTimeZone(value, timezone);
}

function formatDateTime(value?: string, timezone?: string) {
  return formatDateTimeInTimeZone(value, timezone, { month: "2-digit", day: "2-digit", hour: "2-digit", minute: "2-digit" });
}

function dateKeyInTimeZone(value: string, timezone?: string) {
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return "";
  const parts = new Intl.DateTimeFormat("en-CA", {
    year: "numeric",
    month: "2-digit",
    day: "2-digit",
    timeZone: normalizeTimeZone(timezone),
  }).formatToParts(date);
  const year = parts.find((part) => part.type === "year")?.value || "0000";
  const month = parts.find((part) => part.type === "month")?.value || "00";
  const day = parts.find((part) => part.type === "day")?.value || "00";
  return `${year}-${month}-${day}`;
}
