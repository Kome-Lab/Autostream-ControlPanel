"use client";
import { useUICopy } from "@/lib/i18n/ui-v2/use-ui-copy";
import { japaneseCopy, type UICopy } from "@/lib/i18n/ui-v2/copy";


import { useMemo, useState } from "react";
import { Activity, AlertCircle, CheckCircle2, Database, Gauge, HardDrive, Network, RadioTower, RefreshCw, Server } from "lucide-react";
import { useI18n } from "@/components/admin/i18n-provider";
import { PageHeader } from "@/components/shell/page-header";
import { Badge } from "@/components/ui/badge";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Skeleton } from "@/components/ui/skeleton";
import { Button } from "@/components/ui/button";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { MetricCard } from "@/components/admin/metric-card";
import { EChartsPanel, type ChartOption } from "@/components/charts/echarts-panel";
import { metricGroup, type MetricUnit } from "@/features/metrics/metric-classification";
import { OperationalStateNotice } from "@/features/monitoring/operational-state-notice";
import { aggregateOperationalQueries, operationalQuerySnapshot } from "@/features/monitoring/operational-remote-state";
import { useAppSettings, useServiceHealth, useWorkerMetrics } from "@/features/queries";
import { formatTimeInTimeZone } from "@/lib/timezone";
import type { MetricSnapshot, WorkerNode } from "@/types/domain";

type MetricPoint = {
  time: number;
  value: number;
  updatedAt: string;
};

type MetricSeries = {
  key: string;
  label: string;
  serviceID: string;
  serviceLabel: string;
  serviceType: string;
  name: string;
  unit: MetricUnit;
  status?: string;
  points: MetricPoint[];
};

const historyWindowMs = 3 * 60 * 60 * 1000;
const maxPointsPerSeries = 360;
function timeRangeOptions(uiText: UICopy = japaneseCopy) { return [
  { value: String(15 * 60 * 1000), label: uiText("直近15分") },
  { value: String(30 * 60 * 1000), label: uiText("直近30分") },
  { value: String(60 * 60 * 1000), label: uiText("直近1時間") },
  { value: String(3 * 60 * 60 * 1000), label: uiText("直近3時間") },
]; }

export function MetricsView() {
  const uiText = useUICopy();
  const { locale } = useI18n();
  const ja = locale === "ja";
  const appSettings = useAppSettings();
  const [selectedNode, setSelectedNode] = useState("");
  const [timeRange, setTimeRange] = useState(String(3 * 60 * 60 * 1000));
  const metrics = useWorkerMetrics(Number.parseInt(timeRange, 10));
  const services = useServiceHealth();
  const remoteState = aggregateOperationalQueries("metrics", {
    metrics: operationalQuerySnapshot(metrics),
    services: operationalQuerySnapshot(services),
  });
  const timezone = appSettings.data?.timezone;
  const numericMetrics = useMemo(() => numericMetricSnapshots(metrics.data || []), [metrics.data]);
  const serviceLabels = useMemo(() => serviceLabelMap(services.data || []), [services.data]);
  const history = useMemo(() => metricSeriesFromSnapshots(numericMetrics, serviceLabels, uiText), [numericMetrics, serviceLabels, uiText]);
  const allSeries = useMemo(() => latestSeries(history), [history]);
  const nodeOptions = useMemo(() => metricNodeOptions(allSeries), [allSeries]);
  const selectedNodeIsValid = nodeOptions.some((option) => option.value === selectedNode);
  const effectiveNode = selectedNodeIsValid ? selectedNode : nodeOptions[0]?.value || "";
  const effectiveNodeLabel = nodeOptions.find((option) => option.value === effectiveNode)?.label || "";
  const latest = useMemo(() => filterMetricSeries(allSeries, effectiveNode, Number.parseInt(timeRange, 10)), [allSeries, effectiveNode, timeRange]);

  const cpuSeries = useMemo(() => latest.filter((series) => metricGroup(series.name, series.unit) === "cpu"), [latest]);
  const memorySeries = useMemo(() => latest.filter((series) => metricGroup(series.name, series.unit) === "memory"), [latest]);
  const diskSeries = useMemo(() => latest.filter((series) => metricGroup(series.name, series.unit) === "disk"), [latest]);
  const networkSeries = useMemo(() => latest.filter((series) => metricGroup(series.name, series.unit) === "network"), [latest]);
  const networkThroughputSeries = useMemo(() => {
    const throughput = networkSeries.filter((series) => isNetworkThroughputMetric(series.name));
    return throughput.length > 0 ? throughput : networkSeries;
  }, [networkSeries]);
  const networkUnit = useMemo(() => chartUnit(networkThroughputSeries, "kbps"), [networkThroughputSeries]);
  const heapSeries = useMemo(() => latest.filter((series) => metricGroup(series.name, series.unit) === "heap"), [latest]);
  const workloadSeries = useMemo(() => latest.filter((series) => metricGroup(series.name, series.unit) === "workload"), [latest]);
  const runtimeSeries = useMemo(() => latest.filter((series) => metricGroup(series.name, series.unit) === "runtime"), [latest]);
  const operationSeries = useMemo(() => [...workloadSeries, ...runtimeSeries].filter(isOperationChartMetric), [workloadSeries, runtimeSeries]);

  const maxCPU = maxLatestValue(cpuSeries);
  const maxMemory = maxLatestValue(memorySeries);
  const maxDisk = maxLatestValue(diskSeries);
  const maxNetwork = maxLatestValue(networkThroughputSeries);
  const serviceCount = new Set(allSeries.map((series) => series.serviceID || series.serviceType).filter(Boolean)).size;
  const lastUpdated = metrics.dataUpdatedAt ? formatTime(metrics.dataUpdatedAt, timezone) : uiText("未取得");
  const hasMetricData = allSeries.length > 0;
  const metricsStatus = metrics.isError ? hasMetricData ? uiText("更新失敗（取得済みデータ）") : uiText("取得失敗") : hasMetricData ? uiText("正常") : metrics.isLoading ? uiText("取得中") : uiText("データなし");
  const metricsStatusTone = metrics.isError ? hasMetricData ? "warning" : "danger" : hasMetricData ? "ok" : "warning";

  if (remoteState.kind === "initial-loading") {
    return <div className="space-y-5"><PageHeader title="Metrics" /><div role="status" aria-label={ja ? "読込中" : "Loading"}><Skeleton className="h-[520px] w-full" /></div></div>;
  }

  return (
    <div className="space-y-5" data-screen-family="metrics">
      <PageHeader title="Metrics" description={ja ? "Node、期間、単位を確認して、負荷と通信の推移を調べます。" : "Review the node, time range and units when investigating load and traffic."} actions={<Button variant="outline" disabled={metrics.isFetching} onClick={() => void metrics.refetch()}><RefreshCw aria-hidden="true" />{ja ? "更新" : "Refresh"}</Button>} />
      <section className="rounded-md border bg-muted/20 p-4">
        <div className="flex flex-wrap items-start justify-between gap-4">
          <div className="min-w-0">
            <p className="text-xs font-medium uppercase tracking-normal text-muted-foreground">{uiText("管理者向け要約")}</p>
            <h2 className="mt-1 text-lg font-semibold">{uiText("配信基盤の負荷と通信を確認")}</h2>
            <p className="mt-1 max-w-3xl text-sm text-muted-foreground">{uiText("CPU・メモリ・ディスクは使用率、ネットワークは送受信量を表示します。高負荷のNodeは下のグラフと一覧で切り分けてください。")}</p>
          </div>
          <div className="flex min-w-56 flex-col items-start gap-1 text-sm sm:items-end">
            <div className={`flex items-center gap-2 font-medium ${statusTextClass(metricsStatusTone)}`}>
              {metrics.isError ? <AlertCircle className="size-4" /> : <CheckCircle2 className="size-4" />}
              {metricsStatus}
            </div>
            <div className="text-muted-foreground">{uiText("最終更新:")}{lastUpdated}</div>
            <div className="text-muted-foreground">{uiText("自動更新: 10秒ごと")}{metrics.isFetching ? uiText("（更新中）") : ""}</div>
          </div>
        </div>
      </section>
      <OperationalStateNotice state={remoteState} consumer="metrics" />
      <section className="grid gap-4 sm:grid-cols-2 xl:grid-cols-3 2xl:grid-cols-5">
        <MetricCard title={uiText("CPU使用率")} value={formatStat(maxCPU, "percent")} detail={resourceDetail(effectiveNodeLabel, maxCPU, 80, 95, "CPU", uiText)} tone={thresholdTone(maxCPU, 80, 95)} />
        <MetricCard title={uiText("メモリ使用率")} value={formatStat(maxMemory, "percent")} detail={resourceDetail(effectiveNodeLabel, maxMemory, 75, 90, uiText("メモリ"), uiText)} tone={thresholdTone(maxMemory, 75, 90)} />
        <MetricCard title={uiText("ディスク使用率")} value={formatStat(maxDisk, "percent")} detail={resourceDetail(effectiveNodeLabel, maxDisk, 80, 92, uiText("rootディスク"), uiText)} tone={thresholdTone(maxDisk, 80, 92)} />
        <MetricCard title={uiText("ネットワーク")} value={formatStat(maxNetwork, networkUnit)} detail={resourceDetail(effectiveNodeLabel, maxNetwork, undefined, undefined, uiText("送受信スループット"), uiText)} tone="default" />
        <MetricCard title={uiText("受信Node")} value={serviceCount} detail={effectiveNodeLabel ? uiText("表示中: {0}", effectiveNodeLabel) : uiText("メトリクスを報告中")} tone={serviceCount > 0 ? "ok" : "warning"} />
      </section>

      <section className="flex flex-wrap items-end gap-4 rounded-md border bg-muted/20 p-4">
        <div className="min-w-0 flex-[1_1_16rem] space-y-2">
          <label className="text-sm font-medium" htmlFor="metrics-node">{ja ? "表示Node" : "Node"}</label>
          <Select value={effectiveNode || "__none__"} onValueChange={setSelectedNode}>
            <SelectTrigger id="metrics-node" className="w-full min-w-0 [&>span]:truncate">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              {nodeOptions.length === 0 ? <SelectItem value="__none__">{uiText("Nodeなし")}</SelectItem> : null}
              {nodeOptions.map((option) => (
                <SelectItem key={option.value} value={option.value}>
                  {option.label}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </div>
        <div className="w-48 min-w-0 max-w-full space-y-2">
          <label className="text-sm font-medium" htmlFor="metrics-range">{ja ? "表示範囲" : "Time range"}</label>
          <Select value={timeRange} onValueChange={setTimeRange}>
            <SelectTrigger id="metrics-range" className="w-full min-w-0 max-w-full">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              {timeRangeOptions(uiText).map((option) => (
                <SelectItem key={option.value} value={option.value}>
                  {option.label}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </div>
        <p className="max-w-2xl text-sm text-muted-foreground">{ja ? "リアルタイム更新は維持し、選択したNodeの時系列だけを表示します。" : "Live refresh continues for the selected node."} {timezone || "UTC"}</p>
      </section>

      {latest.length === 0 ? (
        <Card>
          <CardHeader className="pb-2">
            <CardTitle className="text-base">{uiText("メトリクス")}</CardTitle>
          </CardHeader>
          <CardContent>
            <div className="flex flex-wrap items-center justify-between gap-3 rounded-md border border-dashed p-6 text-sm">
              <div className="text-muted-foreground">{metrics.isError ? uiText("メトリクスを取得できませんでした。") : uiText("まだメトリクスを受信していません。")}</div>
              {metrics.isError ? <Button variant="outline" size="sm" onClick={() => void metrics.refetch()} disabled={metrics.isFetching}><RefreshCw className="size-4" />{uiText("再試行")}</Button> : null}
            </div>
          </CardContent>
        </Card>
      ) : (
        <>
          <section className="grid min-w-0 gap-4 2xl:grid-cols-2 [&>*]:min-w-0">
            <EChartsPanel title={uiText("CPU使用率")} option={lineChartOption(cpuSeries, "percent", timezone, uiText)} height={320} />
            <EChartsPanel title={uiText("メモリ使用率")} option={lineChartOption(memorySeries, "percent", timezone, uiText)} height={320} />
          </section>
          <section className="grid min-w-0 gap-4 2xl:grid-cols-2 [&>*]:min-w-0">
            <EChartsPanel title={uiText("ディスク使用率")} option={lineChartOption(diskSeries, "percent", timezone, uiText)} height={320} />
            <EChartsPanel title={uiText("ネットワーク送受信")} option={lineChartOption(networkThroughputSeries, networkUnit, timezone, uiText)} height={320} />
          </section>
          <section className="grid min-w-0 gap-4 2xl:grid-cols-2 [&>*]:min-w-0">
            <EChartsPanel title={uiText("Heap使用量")} option={lineChartOption(heapSeries, "bytes", timezone, uiText)} height={320} />
            <EChartsPanel title={uiText("処理・ランタイム指標")} option={lineChartOption(operationSeries, "number", timezone, uiText)} height={320} />
          </section>
          <section className="grid min-w-0 gap-4 2xl:grid-cols-[minmax(0,1.15fr)_minmax(360px,0.85fr)] [&>*]:min-w-0">
            <LatestMetricsTable series={latest} timezone={timezone} />
            <ServiceMetricSummary series={latest} />
          </section>
        </>
      )}
    </div>
  );
}

function metricSeriesFromSnapshots(metrics: MetricSnapshot[], serviceLabels: Map<string, string>, uiText: UICopy = japaneseCopy) {
  const now = Date.now();
  const cutoff = now - historyWindowMs;
  const series = new Map<string, MetricSeries>();
  for (const metric of metrics) {
    if (typeof metric.value !== "number" || !Number.isFinite(metric.value)) continue;
    const key = metricKey(metric);
    const time = normalizedMetricTime(metric.updated_at, now);
    if (time < cutoff) continue;
    const existing = series.get(key);
    const points = new Map<number, MetricPoint>();
    for (const point of existing?.points || []) points.set(point.time, point);
    points.set(time, { time, value: metric.value, updatedAt: metric.updated_at });
    series.set(key, {
      key,
      label: metricSeriesLabel(metric, serviceLabels, uiText),
      serviceID: metric.service_id,
      serviceLabel: serviceLabel(metric.service_id, metric.service_type, serviceLabels),
      serviceType: metric.service_type,
      name: metric.name,
      unit: metricUnit(metric.name),
      status: metric.status,
      points: Array.from(points.values()).sort((a, b) => a.time - b.time).slice(-maxPointsPerSeries),
    });
  }
  return Array.from(series.values());
}

function LatestMetricsTable({ series, timezone }: { series: MetricSeries[]; timezone?: string }) {
  const uiText = useUICopy();
  return (
    <Card className="min-w-0">
      <CardHeader className="pb-2">
        <CardTitle className="flex items-center gap-2 text-base">
          <Activity className="size-4" />
          {uiText("最新メトリクス")}</CardTitle>
      </CardHeader>
      <CardContent className="min-w-0">
        <div className="overflow-x-auto">
        <Table className="min-w-[760px]">
          <TableHeader>
            <TableRow>
              <TableHead>Node</TableHead>
              <TableHead>{uiText("指標")}</TableHead>
              <TableHead>{uiText("値")}</TableHead>
              <TableHead>{uiText("更新")}</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {series.slice(0, 18).map((item) => {
              const latest = latestPoint(item);
              return (
                <TableRow key={item.key}>
                  <TableCell className="min-w-36">
                    <div className="font-medium">{item.serviceLabel}</div>
                    <div className="text-xs text-muted-foreground">{serviceTypeLabel(item.serviceType)}</div>
                  </TableCell>
                  <TableCell className="min-w-48 whitespace-normal break-words">{metricNameLabel(item.name, uiText)}</TableCell>
                  <TableCell className="font-medium">{latest ? formatMetricValue(latest.value, item.unit) : "-"}</TableCell>
                  <TableCell className="text-muted-foreground">{latest ? formatTime(latest.time, timezone) : "-"}</TableCell>
                </TableRow>
              );
            })}
          </TableBody>
        </Table>
        </div>
      </CardContent>
    </Card>
  );
}

function ServiceMetricSummary({ series }: { series: MetricSeries[] }) {
  const uiText = useUICopy();
  const rows = useMemo(() => serviceMetricRows(series), [series]);
  return (
    <Card className="min-w-0">
      <CardHeader className="pb-2">
        <CardTitle className="flex items-center gap-2 text-base">
          <Server className="size-4" />
          {uiText("Node別サマリー")}</CardTitle>
      </CardHeader>
      <CardContent className="min-w-0 space-y-3">
        {rows.map((row) => (
          <div key={row.id} className="rounded-md border p-3">
            <div className="flex flex-wrap items-center justify-between gap-2">
              <div>
                <div className="font-medium">{row.label}</div>
                <div className="text-xs text-muted-foreground">{serviceTypeLabel(row.type)}</div>
              </div>
              <Badge variant="outline">{row.count} {uiText("指標")}</Badge>
            </div>
            <div className="mt-3 grid gap-2 text-sm sm:grid-cols-2 lg:grid-cols-3">
              <SummaryItem icon={Activity} label="CPU" value={formatOptional(row.cpu, "percent")} />
              <SummaryItem icon={Database} label={uiText("メモリ")} value={formatOptional(row.memory, "percent")} />
              <SummaryItem icon={HardDrive} label={uiText("ディスク")} value={formatOptional(row.disk, "percent")} />
              <SummaryItem icon={Network} label={uiText("ネットワーク")} value={formatOptional(row.network, row.networkUnit || "kbps")} />
              <SummaryItem icon={Gauge} label="Heap" value={formatOptional(row.heap, "bytes")} />
              <SummaryItem icon={RadioTower} label={uiText("状態")} value={serviceStatusLabel(row.status, uiText)} />
            </div>
          </div>
        ))}
      </CardContent>
    </Card>
  );
}

function SummaryItem({ icon: Icon, label, value }: { icon: typeof Activity; label: string; value: string }) {
  return (
    <div className="flex min-h-16 items-center gap-3 rounded-md bg-muted/35 px-3 py-2">
      <Icon className="size-4 shrink-0 text-muted-foreground" />
      <div className="min-w-0">
        <div className="text-xs text-muted-foreground">{label}</div>
        <div className="break-words font-medium">{value}</div>
      </div>
    </div>
  );
}

function lineChartOption(series: MetricSeries[], preferredUnit: MetricUnit, timezone?: string, uiText: UICopy = japaneseCopy): ChartOption {
  if (series.length === 0) {
    return {
      grid: { top: 18, right: 16, bottom: 40, left: 48 },
      xAxis: { type: "category", data: [] },
      yAxis: { type: "value" },
      series: [],
    } as ChartOption;
  }
  const times = Array.from(new Set(series.flatMap((item) => item.points.map((point) => point.time)))).sort((a, b) => a - b);
  const unit = chartUnit(series, preferredUnit);
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
      data: times.map((time) => formatTime(time, timezone)),
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
      name: chartSeriesName(item, series, uiText),
      type: "line",
      smooth: true,
      showSymbol: false,
      connectNulls: true,
      emphasis: { focus: "series" },
      data: times.map((time) => item.points.find((point) => point.time === time)?.value ?? null),
    })),
  } as ChartOption;
}

function chartUnit(series: MetricSeries[], preferredUnit: MetricUnit) {
  if (preferredUnit !== "number" && series.some((item) => item.unit === preferredUnit)) return preferredUnit;
  return dominantUnit(series);
}

function chartSeriesName(item: MetricSeries, series: MetricSeries[], uiText: UICopy = japaneseCopy) {
  const serviceIDs = new Set(series.map((candidate) => candidate.serviceID).filter(Boolean));
  return serviceIDs.size <= 1 ? metricNameLabel(item.name, uiText) : item.label;
}

function numericMetricSnapshots(metrics: MetricSnapshot[]) {
  return metrics
    .filter((metric) => typeof metric.value === "number" && Number.isFinite(metric.value))
    .sort((a, b) => metricSortRank(a.name, metricUnit(a.name)) - metricSortRank(b.name, metricUnit(b.name)) || String(a.service_id).localeCompare(String(b.service_id)));
}

function latestSeries(series: MetricSeries[]) {
  return [...series].sort((a, b) => metricSortRank(a.name, a.unit) - metricSortRank(b.name, b.unit) || a.label.localeCompare(b.label));
}

function metricNodeOptions(series: MetricSeries[]) {
  const seen = new Map<string, string>();
  for (const item of series) {
    const value = item.serviceID || item.serviceType;
    if (!value || seen.has(value)) continue;
    seen.set(value, `${item.serviceLabel} (${serviceTypeLabel(item.serviceType)})`);
  }
  return Array.from(seen.entries())
    .map(([value, label]) => ({ value, label }))
    .sort((a, b) => a.label.localeCompare(b.label));
}

function filterMetricSeries(series: MetricSeries[], serviceID: string, rangeMs: number) {
  const cutoff = Date.now() - (Number.isFinite(rangeMs) ? rangeMs : historyWindowMs);
  return series
    .filter((item) => !serviceID || item.serviceID === serviceID)
    .map((item) => ({ ...item, points: item.points.filter((point) => point.time >= cutoff) }))
    .filter((item) => item.points.length > 0);
}

function serviceMetricRows(series: MetricSeries[]) {
  const rows = new Map<
    string,
    { id: string; label: string; type: string; count: number; cpu?: number; memory?: number; disk?: number; network?: number; networkUnit?: MetricUnit; heap?: number; status?: string }
  >();
  for (const item of series) {
    const id = item.serviceID || item.serviceType || "-";
    const row = rows.get(id) || { id, label: item.serviceLabel, type: item.serviceType, count: 0 };
    row.count += 1;
    row.status = item.status || row.status;
    const latest = latestPoint(item)?.value;
    if (typeof latest === "number") {
      const group = metricGroup(item.name, item.unit);
      if (group === "cpu") row.cpu = latest;
      if (group === "memory") row.memory = latest;
      if (group === "disk" && item.name.endsWith("used_percent")) row.disk = latest;
      if (group === "network" && isNetworkThroughputMetric(item.name)) {
        row.network = Math.max(row.network ?? latest, latest);
        row.networkUnit = item.unit;
      }
      if (group === "heap" && (item.name.endsWith("heap_alloc_bytes") || row.heap === undefined)) row.heap = latest;
    }
    rows.set(id, row);
  }
  return Array.from(rows.values()).sort((a, b) => a.id.localeCompare(b.id));
}

function metricKey(metric: MetricSnapshot) {
  return `${metric.service_type}:${metric.service_id}:${metric.stream_id || ""}:${metric.name}`;
}

function metricSeriesLabel(metric: MetricSnapshot, serviceLabels: Map<string, string>, uiText: UICopy = japaneseCopy) {
  return `${serviceLabel(metric.service_id, metric.service_type, serviceLabels)} / ${metricNameLabel(metric.name, uiText)}`;
}

function serviceLabelMap(nodes: WorkerNode[]) {
  const labels = new Map<string, string>();
  for (const node of nodes) {
    const id = node.service_id || node.id;
    const label = node.service_name || id;
    if (id && label) labels.set(id, label);
  }
  return labels;
}

function serviceLabel(serviceID: string, serviceType: string, labels: Map<string, string>) {
  return labels.get(serviceID) || serviceID || serviceTypeLabel(serviceType);
}

function isNetworkThroughputMetric(name: string) {
  const lower = name.toLowerCase();
  return lower.includes("kbps") || lower.includes("bytes_per_sec") || lower.includes("rx_rate") || lower.includes("tx_rate");
}

function isOperationChartMetric(series: MetricSeries) {
  if (series.unit === "bytes" || series.unit === "seconds") return false;
  return !series.name.toLowerCase().includes("memory.used");
}

function metricUnit(name: string): MetricUnit {
  const lower = name.toLowerCase();
  if (lower.includes("percent")) return "percent";
  if (lower.includes("kbps") || lower.includes("bitrate") || lower.includes("bps")) return "kbps";
  if (lower.includes("heap_objects") || lower.includes("objects")) return "count";
  if (lower.includes("bytes")) return "bytes";
  if (lower.includes("sec") || lower.includes("duration") || lower.includes("uptime")) return "seconds";
  if (lower.includes("count") || lower.includes("total") || lower.includes("goroutine")) return "count";
  if (lower.includes("alive") || lower.endsWith("_active") || lower.endsWith("_enabled") || lower.endsWith("_connected") || lower.endsWith("_exists") || lower.endsWith("_status")) return "flag";
  return "number";
}

function metricNameLabel(name: string, uiText: UICopy = japaneseCopy) {
  const labels: Record<string, string> = {
    "worker.cpu_percent": uiText("Worker CPU使用率"),
    "worker.memory_percent": uiText("Worker メモリ使用率"),
    "worker.active_jobs": uiText("Worker 実行中ジョブ"),
    "encoder.output_bitrate_kbps": uiText("Encoder 出力ビットレート"),
    "encoder.process_alive": uiText("Encoderプロセス"),
    "discord.audio_forward_active": uiText("Discord音声転送"),
    "observability.goroutines": uiText("Observability goroutine数"),
    "observability.heap_alloc_bytes": uiText("Observability heap使用量"),
    "observability.heap_sys_bytes": uiText("Observability heap予約量"),
    "observability.heap_objects": uiText("Observability heap object数"),
    "observability.uptime_seconds": uiText("Observability稼働秒数"),
    "node.cpu.used_percent": uiText("CPU使用率"),
    "node.cpu_count": uiText("CPUコア数"),
    "node.load1": uiText("ロードアベレージ 1分"),
    "node.load5": uiText("ロードアベレージ 5分"),
    "node.load15": uiText("ロードアベレージ 15分"),
    "node.memory.total_bytes": uiText("メモリ総量"),
    "node.memory.free_bytes": uiText("メモリ空き容量"),
    "node.memory.available_bytes": uiText("メモリ利用可能容量"),
    "node.memory.buffers_bytes": uiText("メモリ buffers"),
    "node.memory.cached_bytes": uiText("メモリ cached"),
    "node.memory.used_bytes": uiText("メモリ使用量"),
    "node.memory.used_percent": uiText("メモリ使用率"),
    "node.filesystem.root.size_bytes": uiText("rootディスク総量"),
    "node.filesystem.root.free_bytes": uiText("rootディスク空き容量"),
    "node.filesystem.root.used_bytes": uiText("rootディスク使用量"),
    "node.filesystem.root.used_percent": uiText("rootディスク使用率"),
    "node.filesystem.root.files": uiText("root inode総数"),
    "node.filesystem.root.files_free": uiText("root inode空き数"),
    "node.filesystem.root.files_percent": uiText("root inode使用率"),
    "node.network.rx_bytes_total": uiText("ネットワーク受信累計"),
    "node.network.tx_bytes_total": uiText("ネットワーク送信累計"),
    "node.network.rx_bytes_per_sec": uiText("ネットワーク受信量"),
    "node.network.tx_bytes_per_sec": uiText("ネットワーク送信量"),
    "node.network.rx_kbps": uiText("ネットワーク受信 kbps"),
    "node.network.tx_kbps": uiText("ネットワーク送信 kbps"),
    "process.goroutines": uiText("プロセス goroutine数"),
    "process.heap_alloc_bytes": uiText("プロセス heap使用量"),
    "process.heap_sys_bytes": uiText("プロセス heap予約量"),
    "process.heap_objects": uiText("プロセス heap object数"),
    "process.uptime_seconds": uiText("プロセス稼働秒数"),
    "process.gc_pause_seconds_total": uiText("GC pause累計秒数"),
  };
  return labels[name] || name.replace(/^observability\./, "Observability ").replace(/^node\./, "Node ").replace(/^process\./, "Process ").replace(/[._]/g, " ");
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

function serviceStatusLabel(status?: string, uiText: UICopy = japaneseCopy) {
  const labels: Record<string, string> = {
    online: uiText("オンライン"),
    offline: uiText("オフライン"),
    degraded: uiText("注意"),
    pending: uiText("未設定"),
  };
  return status ? labels[status] || uiText("不明") : uiText("未確認");
}

function resourceDetail(nodeLabel: string, value: number | undefined, warning: number | undefined, danger: number | undefined, target: string, uiText: UICopy = japaneseCopy) {
  const prefix = nodeLabel ? uiText("{0} の", nodeLabel) : "";
  if (typeof value !== "number") return uiText("{0}{1}: データなし", prefix, target);
  if (typeof danger === "number" && value >= danger) return uiText("{0}{1}: 危険", prefix, target);
  if (typeof warning === "number" && value >= warning) return uiText("{0}{1}: 注意", prefix, target);
  return uiText("{0}{1}: 正常", prefix, target);
}

function statusTextClass(tone: "default" | "ok" | "warning" | "danger") {
  if (tone === "danger") return "text-red-700 dark:text-red-300";
  if (tone === "warning") return "text-amber-700 dark:text-amber-300";
  if (tone === "ok") return "text-emerald-700 dark:text-emerald-300";
  return "text-muted-foreground";
}

function metricSortRank(name: string, unit: MetricUnit) {
  const group = metricGroup(name, unit);
  if (group === "cpu") return 0;
  if (group === "memory") return 1;
  if (group === "disk") return 2;
  if (group === "network") return 3;
  if (group === "heap") return 4;
  if (group === "workload") return 5;
  return 6;
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
  const priority: MetricUnit[] = ["number", "count", "flag", "seconds", "kbps", "bytes", "percent"];
  return priority.find((unit) => series.some((item) => item.unit === unit)) || "number";
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

function formatTime(time: number, timezone?: string) {
  return formatTimeInTimeZone(new Date(time).toISOString(), timezone);
}
