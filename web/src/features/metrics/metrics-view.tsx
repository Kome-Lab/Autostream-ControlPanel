"use client";

import { useEffect, useMemo, useRef, useState } from "react";
import { Activity, Database, RadioTower, Server } from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Skeleton } from "@/components/ui/skeleton";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { MetricCard } from "@/components/admin/metric-card";
import { EChartsPanel, type ChartOption } from "@/components/charts/echarts-panel";
import { useWorkerMetrics } from "@/features/queries";
import type { MetricSnapshot } from "@/types/domain";

type MetricPoint = {
  time: number;
  value: number;
  updatedAt: string;
};

type MetricSeries = {
  key: string;
  label: string;
  serviceID: string;
  serviceType: string;
  name: string;
  unit: MetricUnit;
  status?: string;
  points: MetricPoint[];
};

type MetricUnit = "percent" | "kbps" | "bytes" | "seconds" | "count" | "flag" | "number";

const historyWindowMs = 30 * 60 * 1000;
const maxPointsPerSeries = 90;

export function MetricsView() {
  const metrics = useWorkerMetrics();
  const numericMetrics = useMemo(() => numericMetricSnapshots(metrics.data || []), [metrics.data]);
  const history = useMetricHistory(numericMetrics);
  const latest = useMemo(() => latestSeries(history), [history]);
  const cpuSeries = useMemo(() => latest.filter((series) => metricGroup(series.name) === "cpu"), [latest]);
  const memorySeries = useMemo(() => latest.filter((series) => metricGroup(series.name) === "memory"), [latest]);
  const workloadSeries = useMemo(() => latest.filter((series) => metricGroup(series.name) === "workload"), [latest]);
  const runtimeSeries = useMemo(() => latest.filter((series) => metricGroup(series.name) === "runtime"), [latest]);

  const maxCPU = maxLatestValue(cpuSeries);
  const maxMemory = maxLatestValue(memorySeries);
  const serviceCount = new Set(latest.map((series) => series.serviceID || series.serviceType).filter(Boolean)).size;
  const staleCount = latest.filter((series) => series.status && !["online", "healthy", "registered"].includes(series.status)).length;

  if (metrics.isLoading && latest.length === 0) {
    return <Skeleton className="h-[520px] w-full" />;
  }

  return (
    <div className="space-y-6">
      <section className="grid gap-4 md:grid-cols-2 xl:grid-cols-4">
        <MetricCard title="最大CPU" value={formatStat(maxCPU, "percent")} detail="Node別最新値の最大" tone={thresholdTone(maxCPU, 80, 95)} />
        <MetricCard title="最大メモリ" value={formatStat(maxMemory, "percent")} detail="Node別最新値の最大" tone={thresholdTone(maxMemory, 75, 90)} />
        <MetricCard title="受信Node" value={serviceCount} detail="メトリクスを報告中" tone={serviceCount > 0 ? "ok" : "warning"} />
        <MetricCard title="状態注意" value={staleCount} detail="online以外の報告" tone={staleCount > 0 ? "warning" : "ok"} />
      </section>

      {latest.length === 0 ? (
        <Card>
          <CardHeader className="pb-2">
            <CardTitle className="text-base">メトリクス</CardTitle>
          </CardHeader>
          <CardContent>
            <div className="rounded-md border border-dashed p-6 text-sm text-muted-foreground">まだメトリクスを受信していません。</div>
          </CardContent>
        </Card>
      ) : (
        <>
          <section className="grid gap-4 xl:grid-cols-2">
            <EChartsPanel title="CPU使用率" option={lineChartOption(cpuSeries, "percent")} height={300} />
            <EChartsPanel title="メモリ使用率" option={lineChartOption(memorySeries, "percent")} height={300} />
          </section>
          <section className="grid gap-4 xl:grid-cols-2">
            <EChartsPanel title="配信・エンコード負荷" option={lineChartOption(workloadSeries, "number")} height={300} />
            <EChartsPanel title="ランタイム指標" option={lineChartOption(runtimeSeries, "number")} height={300} />
          </section>
          <section className="grid gap-4 xl:grid-cols-[1.15fr_0.85fr]">
            <LatestMetricsTable series={latest} />
            <ServiceMetricSummary series={latest} />
          </section>
        </>
      )}
    </div>
  );
}

function useMetricHistory(metrics: MetricSnapshot[]) {
  const historyRef = useRef<Map<string, MetricSeries>>(new Map());
  const [history, setHistory] = useState<MetricSeries[]>([]);

  useEffect(() => {
    if (metrics.length === 0) return;
    const now = Date.now();
    const next = new Map(historyRef.current);
    for (const metric of metrics) {
      if (typeof metric.value !== "number" || !Number.isFinite(metric.value)) continue;
      const key = metricKey(metric);
      const time = normalizedMetricTime(metric.updated_at, now);
      const existing = next.get(key);
      const point = { time, value: metric.value, updatedAt: metric.updated_at };
      const previousPoints = existing?.points || [];
      const lastPoint = previousPoints[previousPoints.length - 1];
      const cutoff = Math.max(lastPoint?.time || time, time) - historyWindowMs;
      const points =
        lastPoint && lastPoint.time === point.time && lastPoint.value === point.value
          ? previousPoints
          : [...previousPoints, point].filter((item) => item.time >= cutoff).slice(-maxPointsPerSeries);
      next.set(key, {
        key,
        label: metricSeriesLabel(metric),
        serviceID: metric.service_id,
        serviceType: metric.service_type,
        name: metric.name,
        unit: metricUnit(metric.name),
        status: metric.status,
        points,
      });
    }
    historyRef.current = next;
    const handle = window.setTimeout(() => setHistory(Array.from(next.values())), 0);
    return () => window.clearTimeout(handle);
  }, [metrics]);

  return history;
}

function LatestMetricsTable({ series }: { series: MetricSeries[] }) {
  return (
    <Card>
      <CardHeader className="pb-2">
        <CardTitle className="flex items-center gap-2 text-base">
          <Activity className="size-4" />
          最新メトリクス
        </CardTitle>
      </CardHeader>
      <CardContent>
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>Node</TableHead>
              <TableHead>指標</TableHead>
              <TableHead>値</TableHead>
              <TableHead>更新</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {series.slice(0, 18).map((item) => {
              const latest = latestPoint(item);
              return (
                <TableRow key={item.key}>
                  <TableCell className="min-w-36">
                    <div className="font-medium">{item.serviceID || "-"}</div>
                    <div className="text-xs text-muted-foreground">{serviceTypeLabel(item.serviceType)}</div>
                  </TableCell>
                  <TableCell>{metricNameLabel(item.name)}</TableCell>
                  <TableCell className="font-medium">{latest ? formatMetricValue(latest.value, item.unit) : "-"}</TableCell>
                  <TableCell className="text-muted-foreground">{latest ? formatTime(latest.time) : "-"}</TableCell>
                </TableRow>
              );
            })}
          </TableBody>
        </Table>
      </CardContent>
    </Card>
  );
}

function ServiceMetricSummary({ series }: { series: MetricSeries[] }) {
  const rows = useMemo(() => serviceMetricRows(series), [series]);
  return (
    <Card>
      <CardHeader className="pb-2">
        <CardTitle className="flex items-center gap-2 text-base">
          <Server className="size-4" />
          Node別サマリー
        </CardTitle>
      </CardHeader>
      <CardContent className="space-y-3">
        {rows.map((row) => (
          <div key={row.id} className="rounded-md border p-3">
            <div className="flex flex-wrap items-center justify-between gap-2">
              <div>
                <div className="font-medium">{row.id}</div>
                <div className="text-xs text-muted-foreground">{serviceTypeLabel(row.type)}</div>
              </div>
              <Badge variant="outline">{row.count} 指標</Badge>
            </div>
            <div className="mt-3 grid gap-2 text-sm sm:grid-cols-3">
              <SummaryItem icon={Activity} label="CPU" value={formatOptional(row.cpu, "percent")} />
              <SummaryItem icon={Database} label="メモリ" value={formatOptional(row.memory, "percent")} />
              <SummaryItem icon={RadioTower} label="状態" value={row.status || "-"} />
            </div>
          </div>
        ))}
      </CardContent>
    </Card>
  );
}

function SummaryItem({ icon: Icon, label, value }: { icon: typeof Activity; label: string; value: string }) {
  return (
    <div className="flex items-center gap-2 rounded-md bg-muted/35 px-2 py-2">
      <Icon className="size-4 text-muted-foreground" />
      <div className="min-w-0">
        <div className="text-xs text-muted-foreground">{label}</div>
        <div className="truncate font-medium">{value}</div>
      </div>
    </div>
  );
}

function lineChartOption(series: MetricSeries[], preferredUnit: MetricUnit): ChartOption {
  if (series.length === 0) {
    return {
      grid: { top: 18, right: 16, bottom: 40, left: 48 },
      xAxis: { type: "category", data: [] },
      yAxis: { type: "value" },
      series: [],
    } as ChartOption;
  }
  const times = Array.from(new Set(series.flatMap((item) => item.points.map((point) => point.time)))).sort((a, b) => a - b);
  const unit = preferredUnit === "number" ? dominantUnit(series) : preferredUnit;
  return {
    color: ["#2563eb", "#16a34a", "#f59e0b", "#dc2626", "#7c3aed", "#0891b2", "#475569"],
    tooltip: {
      trigger: "axis",
      valueFormatter: (value) => formatMetricValue(Number(value), unit),
    },
    legend: { bottom: 0, type: "scroll" },
    grid: { top: 20, right: 22, bottom: 58, left: 58 },
    xAxis: {
      type: "category",
      data: times.map(formatTime),
      boundaryGap: false,
      axisLabel: { hideOverlap: true },
    },
    yAxis: {
      type: "value",
      min: unit === "percent" ? 0 : undefined,
      max: unit === "percent" ? 100 : undefined,
      axisLabel: { formatter: (value: number) => formatAxisValue(value, unit) },
      splitLine: { lineStyle: { color: "rgba(148, 163, 184, 0.22)" } },
    },
    series: series.slice(0, 8).map((item) => ({
      name: item.label,
      type: "line",
      smooth: true,
      showSymbol: false,
      connectNulls: true,
      emphasis: { focus: "series" },
      data: times.map((time) => item.points.find((point) => point.time === time)?.value ?? null),
    })),
  } as ChartOption;
}

function numericMetricSnapshots(metrics: MetricSnapshot[]) {
  return metrics
    .filter((metric) => typeof metric.value === "number" && Number.isFinite(metric.value))
    .sort((a, b) => metricSortRank(a.name) - metricSortRank(b.name) || String(a.service_id).localeCompare(String(b.service_id)));
}

function latestSeries(series: MetricSeries[]) {
  return [...series].sort((a, b) => metricSortRank(a.name) - metricSortRank(b.name) || a.label.localeCompare(b.label));
}

function serviceMetricRows(series: MetricSeries[]) {
  const rows = new Map<string, { id: string; type: string; count: number; cpu?: number; memory?: number; status?: string }>();
  for (const item of series) {
    const id = item.serviceID || item.serviceType || "-";
    const row = rows.get(id) || { id, type: item.serviceType, count: 0 };
    row.count += 1;
    row.status = item.status || row.status;
    const latest = latestPoint(item)?.value;
    if (typeof latest === "number") {
      if (metricGroup(item.name) === "cpu") row.cpu = latest;
      if (metricGroup(item.name) === "memory") row.memory = latest;
    }
    rows.set(id, row);
  }
  return Array.from(rows.values()).sort((a, b) => a.id.localeCompare(b.id));
}

function metricKey(metric: MetricSnapshot) {
  return `${metric.service_type}:${metric.service_id}:${metric.stream_id || ""}:${metric.name}`;
}

function metricSeriesLabel(metric: MetricSnapshot) {
  return `${metric.service_id || serviceTypeLabel(metric.service_type)} / ${metricNameLabel(metric.name)}`;
}

function metricGroup(name: string) {
  const lower = name.toLowerCase();
  if (lower.includes("cpu")) return "cpu";
  if (lower.includes("memory") || lower.includes("mem") || lower.includes("heap")) return "memory";
  if (lower.includes("bitrate") || lower.includes("fps") || lower.includes("active") || lower.includes("process_alive") || lower.includes("audio")) return "workload";
  return "runtime";
}

function metricUnit(name: string): MetricUnit {
  const lower = name.toLowerCase();
  if (lower.includes("percent")) return "percent";
  if (lower.includes("kbps") || lower.includes("bitrate")) return "kbps";
  if (lower.includes("bytes") || lower.includes("heap")) return "bytes";
  if (lower.includes("sec") || lower.includes("duration")) return "seconds";
  if (lower.includes("alive") || lower.includes("active") || lower.endsWith("_exists") || lower.endsWith("_status")) return "flag";
  if (lower.includes("count") || lower.includes("total") || lower.includes("goroutines")) return "count";
  return "number";
}

function metricNameLabel(name: string) {
  const labels: Record<string, string> = {
    "worker.cpu_percent": "CPU使用率",
    "worker.memory_percent": "メモリ使用率",
    "encoder.process_alive": "Encoderプロセス",
    "discord.audio_forward_active": "Discord音声転送",
    "observability.goroutines": "Observability goroutine",
    "observability.heap_alloc_bytes": "Observability heap使用量",
    "observability.heap_sys_bytes": "Observability heap予約量",
    "observability.heap_objects": "Observability heap object",
    "observability.uptime_seconds": "Observability稼働秒数",
  };
  return labels[name] || name.replace(/^observability\./, "Observability ").replace(/[._]/g, " ");
}

function serviceTypeLabel(type: string) {
  const labels: Record<string, string> = {
    discord_bot: "Discord Bot",
    encoder_recorder: "Encoder/Recorder",
    observability: "Observability",
    worker: "Worker",
  };
  return labels[type] || type || "-";
}

function metricSortRank(name: string) {
  const group = metricGroup(name);
  if (group === "cpu") return 0;
  if (group === "memory") return 1;
  if (group === "workload") return 2;
  return 3;
}

function normalizedMetricTime(value: string, fallback: number) {
  const parsed = Date.parse(value);
  return Number.isNaN(parsed) ? fallback : parsed;
}

function latestPoint(series: MetricSeries) {
  return series.points[series.points.length - 1];
}

function maxLatestValue(series: MetricSeries[]) {
  const values = series.map((item) => latestPoint(item)?.value).filter((value): value is number => typeof value === "number" && Number.isFinite(value));
  if (values.length === 0) return undefined;
  return Math.max(...values);
}

function dominantUnit(series: MetricSeries[]): MetricUnit {
  const first = series.find((item) => item.unit !== "flag");
  return first?.unit || "number";
}

function formatMetricValue(value: number, unit: MetricUnit) {
  if (!Number.isFinite(value)) return "-";
  if (unit === "percent") return `${value.toFixed(value % 1 === 0 ? 0 : 1)}%`;
  if (unit === "kbps") return `${Math.round(value).toLocaleString()} kbps`;
  if (unit === "bytes") return formatBytes(value);
  if (unit === "seconds") return `${Math.round(value).toLocaleString()} sec`;
  if (unit === "flag") return value > 0 ? "ON" : "OFF";
  return value.toLocaleString(undefined, { maximumFractionDigits: 2 });
}

function formatAxisValue(value: number, unit: MetricUnit) {
  if (unit === "percent") return `${value}%`;
  if (unit === "bytes") return formatBytes(value);
  if (unit === "kbps") return `${value}`;
  return String(value);
}

function formatBytes(value: number) {
  const units = ["B", "KB", "MB", "GB", "TB"];
  let size = value;
  let unitIndex = 0;
  while (size >= 1024 && unitIndex < units.length - 1) {
    size /= 1024;
    unitIndex += 1;
  }
  return `${size.toFixed(size >= 10 || unitIndex === 0 ? 0 : 1)} ${units[unitIndex]}`;
}

function formatStat(value: number | undefined, unit: MetricUnit) {
  return typeof value === "number" ? formatMetricValue(value, unit) : "-";
}

function formatOptional(value: number | undefined, unit: MetricUnit) {
  return typeof value === "number" ? formatMetricValue(value, unit) : "-";
}

function thresholdTone(value: number | undefined, warning: number, danger: number) {
  if (typeof value !== "number") return "default";
  if (value >= danger) return "danger";
  if (value >= warning) return "warning";
  return "ok";
}

function formatTime(time: number) {
  return new Intl.DateTimeFormat("ja-JP", { hour: "2-digit", minute: "2-digit", second: "2-digit" }).format(time);
}
