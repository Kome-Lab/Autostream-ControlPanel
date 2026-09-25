"use client";
import { useUICopy } from "@/lib/i18n/ui-v2/use-ui-copy";
import { japaneseCopy, type UICopy } from "@/lib/i18n/ui-v2/copy";


import { useMemo } from "react";
import { AlertCircle, AlertTriangle, CheckCircle2, ClipboardCheck, Network, RefreshCw, ShieldAlert } from "lucide-react";
import { PageHeader } from "@/components/shell/page-header";
import { DetailSection } from "@/components/layout/detail-section";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Skeleton } from "@/components/ui/skeleton";
import { Button } from "@/components/ui/button";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { MonitoringMetric } from "./monitoring-metric";
import { StatusBadge } from "@/components/admin/status-badge";
import { useI18n } from "@/components/admin/i18n-provider";
import { useAppSettings, useResourceData, useServiceHealth, useStreams } from "@/features/queries";
import { OperationalStateNotice } from "@/features/monitoring/operational-state-notice";
import {
  aggregateOperationalQueries,
  operationalQuerySnapshot,
  projectOperationalQuery,
  remoteStateAllowsPositiveSummary,
  serviceAvailabilityContribution,
  summarizeKnownStatuses,
  summarizeServiceAvailability,
} from "@/features/monitoring/operational-remote-state";
import { formatDateTimeInTimeZone } from "@/lib/timezone";
import type { Stream, WorkerNode } from "@/types/domain";

type MonitoringRow = Record<string, unknown>;

export function MonitoringView() {
  const uiText = useUICopy();
  const { locale } = useI18n();
  const appSettings = useAppSettings();
  const services = useServiceHealth();
  const streams = useStreams();
  const incidents = useResourceData<MonitoringRow[]>("/observability/incidents");
  const diagnostics = useResourceData<MonitoringRow[]>("/observability/diagnostics");
  const timezone = appSettings.data?.timezone;

  const serviceRows = useMemo(() => services.data || [], [services.data]);
  const streamRows = useMemo(() => streams.data || [], [streams.data]);
  const entityLabels = useMemo(() => buildEntityLabels(serviceRows, streamRows), [serviceRows, streamRows]);
  const incidentRows = useMemo(() => incidents.data || [], [incidents.data]);
  const diagnosticRows = useMemo(() => diagnostics.data || [], [diagnostics.data]);
  const serviceCoverage = useMemo(() => summarizeServiceAvailability(serviceRows), [serviceRows]);
  const incidentCoverage = useMemo(() => summarizeKnownStatuses(incidentRows.map((row) => rowString(row, "status")), ["open", "active", "firing", "warning", "critical"], ["resolved", "closed"]), [incidentRows]);
  const diagnosticCoverage = useMemo(() => summarizeKnownStatuses(diagnosticRows.map((row) => rowString(row, "status")), ["fail", "failed", "warning", "error"], ["pass", "ok", "success"]), [diagnosticRows]);
  const serviceState = projectOperationalQuery(operationalQuerySnapshot(services));
  const incidentState = projectOperationalQuery(operationalQuerySnapshot(incidents));
  const diagnosticState = projectOperationalQuery(operationalQuerySnapshot(diagnostics));
  const remoteState = aggregateOperationalQueries("monitoring", {
    services: operationalQuerySnapshot(services),
    streams: operationalQuerySnapshot(streams),
    incidents: operationalQuerySnapshot(incidents),
    diagnostics: operationalQuerySnapshot(diagnostics),
  });
  const hasError = remoteState.kind === "blocking-error" || remoteState.kind === "partial" || ((remoteState.kind === "ready" || remoteState.kind === "empty") && remoteState.freshness.kind === "stale");
  const lastUpdatedAt = Math.max(services.dataUpdatedAt, incidents.dataUpdatedAt, diagnostics.dataUpdatedAt, streams.dataUpdatedAt);
  const lastUpdated = lastUpdatedAt > 0 ? formatTimestamp(new Date(lastUpdatedAt).toISOString(), timezone) : uiText("未取得");
  const retry = () => Promise.all([services.refetch(), incidents.refetch(), diagnostics.refetch(), streams.refetch()]);

  if (remoteState.kind === "initial-loading") {
    return <div className="space-y-5"><PageHeader title="Monitoring" /><div role="status" aria-label={locale === "ja" ? "読込中" : "Loading"}><Skeleton className="h-[520px] w-full" /></div></div>;
  }

  return (
    <div className="space-y-5" data-screen-family="monitoring">
      <PageHeader title="Monitoring" description={locale === "ja" ? "現在の問題・稼働・診断を個別に確認します。時系列の調査はMetricsへ進んでください。" : "Review issues, availability and diagnostics separately. Use Metrics for time-series analysis."} actions={<Button variant="outline" onClick={() => void retry()} disabled={services.isFetching || incidents.isFetching || diagnostics.isFetching || streams.isFetching}><RefreshCw aria-hidden="true" />{locale === "ja" ? "更新" : "Refresh"}</Button>} />
      <section className="rounded-md border bg-muted/20 p-4">
        <div className="flex flex-wrap items-start justify-between gap-4">
          <div className="min-w-0">
            <p className="text-xs font-medium uppercase tracking-normal text-muted-foreground">{uiText("管理者向け要約")}</p>
            <h2 className="mt-1 text-lg font-semibold">{uiText("現在の問題・Node稼働・診断を分けて確認")}</h2>
            <p className="mt-1 max-w-3xl text-sm text-muted-foreground">{uiText("Monitoringは障害対応と稼働状況を確認する画面です。CPUやメモリなどの時系列分析はMetricsで確認します。")}</p>
          </div>
          <div className="flex min-w-56 flex-col items-start gap-1 text-sm sm:items-end">
            <div className={`flex items-center gap-2 font-medium ${hasError ? "text-red-700 dark:text-red-300" : "text-emerald-700 dark:text-emerald-300"}`}>
              {hasError ? <AlertCircle className="size-4" /> : <CheckCircle2 className="size-4" />}
              {hasError ? uiText("一部の情報を取得できません") : remoteState.freshness?.kind === "refreshing" ? uiText("取得済みの値を表示しながら更新中です。") : uiText("監視情報は正常に取得済み")}
            </div>
            <div className="text-muted-foreground">{uiText("最終更新:")}{lastUpdated}</div>
            <div className="text-muted-foreground">{uiText("自動更新: Nodeは10秒ごと")}</div>
            {hasError ? <Button variant="outline" size="sm" onClick={() => void retry()} disabled={services.isFetching || incidents.isFetching || diagnostics.isFetching || streams.isFetching}><RefreshCw className="size-4" />{uiText("再試行")}</Button> : null}
          </div>
        </div>
      </section>
      <OperationalStateNotice state={remoteState} consumer="monitoring" />
      <section className="grid gap-4 md:grid-cols-2 xl:grid-cols-4">
        <MonitoringMetric state={serviceState} title={uiText("オンラインNode")} value={`${serviceCoverage.positiveCount}/${serviceCoverage.knownCount}`} detail={serviceCoverage.unknownCount > 0 ? uiText("{0}件は状態不明", serviceCoverage.unknownCount) : uiText("Control Panelに接続中")} tone={serviceCoverage.totalCount > 0 && serviceCoverage.unknownCount === 0 && serviceCoverage.positiveCount === serviceCoverage.knownCount ? "ok" : "warning"} />
        <MonitoringMetric state={serviceState} title={uiText("Node要確認")} value={serviceCoverage.negativeCount} detail={serviceCoverage.unknownCount > 0 ? uiText("{0}件を分母から除外", serviceCoverage.unknownCount) : uiText("heartbeatまたは登録状態")} tone={serviceCoverage.negativeCount > 0 ? "warning" : serviceCoverage.unknownCount > 0 ? "warning" : "ok"} />
        <MonitoringMetric state={incidentState} title={uiText("未解決インシデント")} value={incidentCoverage.positiveCount} detail={incidentCoverage.unknownCount > 0 ? uiText("{0}件は判定不能", incidentCoverage.unknownCount) : uiText("対応または確認が必要")} tone={incidentCoverage.positiveCount > 0 ? "danger" : incidentCoverage.unknownCount > 0 ? "warning" : "ok"} />
        <MonitoringMetric state={diagnosticState} title={uiText("診断警告")} value={diagnosticCoverage.positiveCount} detail={diagnosticCoverage.unknownCount > 0 ? uiText("{0}件は判定不能", diagnosticCoverage.unknownCount) : uiText("直近の疎通・配信前確認")} tone={diagnosticCoverage.positiveCount > 0 || diagnosticCoverage.unknownCount > 0 ? "warning" : "ok"} />
      </section>

      <DetailSection title={locale === "ja" ? "Node稼働とインシデント" : "Node health and incidents"}>
      <section className="grid gap-6 xl:grid-cols-2">
        <ServiceHealthPanel services={serviceRows} loading={services.isLoading} error={services.isError} onRetry={() => void services.refetch()} entityLabels={entityLabels} />
        <IncidentPanel incidents={incidentRows} loading={incidents.isLoading} error={incidents.isError} onRetry={() => void incidents.refetch()} timezone={timezone} entityLabels={entityLabels} />
      </section>

      </DetailSection>
      <DetailSection title={locale === "ja" ? "診断と要対応" : "Diagnostics and action items"}>
      <section className="grid gap-6 xl:grid-cols-2">
        <DiagnosticsPanel diagnostics={diagnosticRows} loading={diagnostics.isLoading} error={diagnostics.isError} onRetry={() => void diagnostics.refetch()} entityLabels={entityLabels} />
        <OperationalFocus services={serviceRows} incidents={incidentRows} diagnostics={diagnosticRows} entityLabels={entityLabels} locale={locale}
          summaryConfirmed={[serviceState, incidentState, diagnosticState].every((state) => remoteStateAllowsPositiveSummary(state, 0, true))} />
      </section>
      </DetailSection>
    </div>
  );
}

function ServiceHealthPanel({ services, loading, error, onRetry, entityLabels }: { services: WorkerNode[]; loading: boolean; error: boolean; onRetry: () => void; entityLabels: Map<string, string> }) {
  const uiText = useUICopy();
  return (
    <Card>
      <CardHeader className="pb-2">
        <CardTitle className="flex items-center gap-2 text-base">
          <Network className="size-4" />
          {uiText("Node監視")}</CardTitle>
      </CardHeader>
      <CardContent>
        {error && services.length === 0 ? <ErrorState message={uiText("Nodeの稼働状況を取得できませんでした。")} onRetry={onRetry} /> : loading && services.length === 0 ? (
          <Skeleton className="h-44 w-full" />
        ) : services.length === 0 ? (
          <EmptyState message={uiText("登録済みNodeがありません。")} />
        ) : (
          <>{error ? <StaleState message={uiText("更新に失敗しました。直前に取得したNode状態を表示しています。")} onRetry={onRetry} /> : null}<Table>
            <TableHeader>
              <TableRow>
                <TableHead>Node</TableHead>
                <TableHead>{uiText("状態")}</TableHead>
                <TableHead>Heartbeat</TableHead>
                <TableHead>{uiText("配信")}</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {services.map((service) => (
                <TableRow key={service.id || service.service_id}>
                  <TableCell>
                    <div className="font-medium">{service.service_name || service.service_id}</div>
                    <div className="text-xs text-muted-foreground">{serviceTypeLabel(service.service_type)}</div>
                  </TableCell>
                  <TableCell>
                    <StatusBadge status={service.health_status || service.status} showDetail />
                  </TableCell>
                  <TableCell className="text-muted-foreground">{formatHeartbeat(service.heartbeat_age_sec, uiText)}</TableCell>
                  <TableCell className="text-muted-foreground">{displayReference(service.current_stream_id || "", entityLabels, uiText)}</TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table></>
        )}
      </CardContent>
    </Card>
  );
}

function IncidentPanel({ incidents, loading, error, onRetry, timezone, entityLabels }: { incidents: MonitoringRow[]; loading: boolean; error: boolean; onRetry: () => void; timezone?: string; entityLabels: Map<string, string> }) {
  const uiText = useUICopy();
  return (
    <Card>
      <CardHeader className="pb-2">
        <CardTitle className="flex items-center gap-2 text-base">
          <ShieldAlert className="size-4" />
          {uiText("インシデント")}</CardTitle>
      </CardHeader>
      <CardContent className="space-y-3">
        {error && incidents.length === 0 ? <ErrorState message={uiText("現在の問題を取得できませんでした。")} onRetry={onRetry} /> : error ? <StaleState message={uiText("更新に失敗しました。直前のインシデントを表示しています。")} onRetry={onRetry} /> : loading && incidents.length === 0 ? <Skeleton className="h-36 w-full" /> : null}
        {!loading && !error && incidents.length === 0 ? <EmptyState message={uiText("現在検知されている問題はありません。")} /> : null}
        {incidents.slice(0, 6).map((row, index) => (
          <div key={rowString(row, "id") || index} className="rounded-md border p-3">
            <div className="flex flex-wrap items-center justify-between gap-2">
              <div className="font-medium">{rowString(row, "title") || rowString(row, "rule") || uiText("インシデント")}</div>
              <StatusBadge status={rowString(row, "status") || rowString(row, "severity")} />
            </div>
            <div className="mt-1 text-sm text-muted-foreground">
              {displayReference(rowString(row, "service_id"), entityLabels, uiText)} / {formatTimestamp(rowString(row, "updated_at") || rowString(row, "created_at"), timezone)}
            </div>
          </div>
        ))}
      </CardContent>
    </Card>
  );
}

function DiagnosticsPanel({ diagnostics, loading, error, onRetry, entityLabels }: { diagnostics: MonitoringRow[]; loading: boolean; error: boolean; onRetry: () => void; entityLabels: Map<string, string> }) {
  const uiText = useUICopy();
  return (
    <Card>
      <CardHeader className="pb-2">
        <CardTitle className="flex items-center gap-2 text-base">
          <ClipboardCheck className="size-4" />
          {uiText("診断結果")}</CardTitle>
      </CardHeader>
      <CardContent className="space-y-3">
        {error && diagnostics.length === 0 ? <ErrorState message={uiText("診断結果を取得できませんでした。")} onRetry={onRetry} /> : error ? <StaleState message={uiText("更新に失敗しました。直前の診断結果を表示しています。")} onRetry={onRetry} /> : loading && diagnostics.length === 0 ? <Skeleton className="h-36 w-full" /> : null}
        {!loading && !error && diagnostics.length === 0 ? <EmptyState message={uiText("診断結果はまだありません。")} /> : null}
        {diagnostics.slice(0, 6).map((row, index) => (
          <div key={rowString(row, "id") || index} className="grid gap-3 rounded-md border p-3 sm:grid-cols-[minmax(0,1fr)_128px] sm:items-center">
            <div>
              <div className="font-medium">{diagnosticLabel(rowString(row, "check") || rowString(row, "rule"), uiText)}</div>
              <div className="text-sm text-muted-foreground">{displayReference(rowString(row, "target") || rowString(row, "service_id"), entityLabels, uiText)}</div>
            </div>
            <StatusBadge status={rowString(row, "status")} showDetail />
          </div>
        ))}
      </CardContent>
    </Card>
  );
}

function OperationalFocus({ services, incidents, diagnostics, entityLabels, locale, summaryConfirmed }: { services: WorkerNode[]; incidents: MonitoringRow[]; diagnostics: MonitoringRow[]; entityLabels: Map<string, string>; locale: "ja" | "en"; summaryConfirmed: boolean }) {
  const uiText = useUICopy();
  const offlineServices = services.filter((service) => { const contribution = serviceAvailabilityContribution(service); return contribution.kind === "known" && !contribution.positive; });
  const openStatuses = new Set(["open", "active", "firing", "warning", "critical"]);
  const failedStatuses = new Set(["fail", "failed", "warning", "error"]);
  const openIncidents = incidents.filter((row) => openStatuses.has(rowString(row, "status").trim().toLowerCase()));
  const failedDiagnostics = diagnostics.filter((row) => failedStatuses.has(rowString(row, "status").trim().toLowerCase()));
  const unknownCount = services.length + incidents.length + diagnostics.length - summarizeServiceAvailability(services).knownCount - summarizeKnownStatuses(incidents.map((row) => rowString(row, "status")), [...openStatuses], ["resolved", "closed"]).knownCount - summarizeKnownStatuses(diagnostics.map((row) => rowString(row, "status")), [...failedStatuses], ["pass", "ok", "success"]).knownCount;
  const hasAttention = offlineServices.length > 0 || openIncidents.length > 0 || failedDiagnostics.length > 0;

  return (
    <Card>
      <CardHeader className="pb-2">
        <CardTitle className="flex items-center gap-2 text-base">
          <AlertTriangle className="size-4" />
          {uiText("確認対象")}</CardTitle>
      </CardHeader>
      <CardContent className="space-y-3">
        {!summaryConfirmed ? <p role="status">{uiText("未取得または更新前の情報があるため、要対応の全件数は確定していません。")}</p> : !hasAttention && unknownCount === 0 ? <EmptyState message={uiText("優先対応が必要な項目はありません。")} /> : null}
        {unknownCount > 0 ? <div role="status" className="rounded-md border border-amber-300 bg-amber-50 p-3 text-sm text-amber-900 dark:border-amber-900 dark:bg-amber-950/30 dark:text-amber-200">{locale === "ja" ? `${unknownCount}件は状態不明のため、正常・要対応のどちらにも数えていません。` : `${unknownCount} items have unknown status and are excluded from healthy and attention counts.`}</div> : null}
        {offlineServices.slice(0, 4).map((service) => (
          <AttentionRow key={service.id || service.service_id || service.service_name} title={service.service_name || service.service_id || "-"} detail={`${serviceTypeLabel(service.service_type)} / ${formatHeartbeat(service.heartbeat_age_sec, uiText)}`} status={service.health_status || service.status || "-"} />
        ))}
        {openIncidents.slice(0, 4).map((row, index) => (
          <AttentionRow key={rowString(row, "id") || `incident-${index}`} title={rowString(row, "title") || uiText("インシデント")} detail={displayReference(rowString(row, "service_id"), entityLabels, uiText)} status={rowString(row, "status") || rowString(row, "severity")} />
        ))}
        {failedDiagnostics.slice(0, 4).map((row, index) => (
          <AttentionRow key={rowString(row, "id") || `diagnostic-${index}`} title={diagnosticLabel(rowString(row, "check"), uiText)} detail={displayReference(rowString(row, "target"), entityLabels, uiText)} status={rowString(row, "status")} />
        ))}
      </CardContent>
    </Card>
  );
}

function AttentionRow({ title, detail, status }: { title: string; detail: string; status: string }) {
  return (
    <div className="grid gap-3 rounded-md border p-3 sm:grid-cols-[minmax(0,1fr)_128px] sm:items-center">
      <div>
        <div className="font-medium">{title}</div>
        <div className="text-sm text-muted-foreground">{detail}</div>
      </div>
      <StatusBadge status={status} showDetail />
    </div>
  );
}

function EmptyState({ message }: { message: string }) {
  return <div className="rounded-md border border-dashed p-4 text-sm text-muted-foreground">{message}</div>;
}

function ErrorState({ message, onRetry }: { message: string; onRetry: () => void }) {
  const uiText = useUICopy();
  return <div className="flex flex-wrap items-center justify-between gap-3 rounded-md border border-red-200 bg-red-50/50 p-4 text-sm dark:border-red-900 dark:bg-red-950/20"><span className="text-red-700 dark:text-red-300">{message}</span><Button variant="outline" size="sm" onClick={onRetry}><RefreshCw className="size-4" />{uiText("再試行")}</Button></div>;
}

function StaleState({ message, onRetry }: { message: string; onRetry: () => void }) {
  const uiText = useUICopy();
  return <div role="status" className="mb-3 flex flex-wrap items-center justify-between gap-3 rounded-md border border-amber-300 bg-amber-50/70 p-3 text-sm text-amber-900 dark:border-amber-900 dark:bg-amber-950/25 dark:text-amber-200"><span>{message}</span><Button variant="outline" size="sm" onClick={onRetry}><RefreshCw className="size-4" />{uiText("再試行")}</Button></div>;
}

function rowString(row: MonitoringRow, key: string) {
  const value = row[key];
  return typeof value === "string" ? value : "";
}

function buildEntityLabels(services: WorkerNode[], streams: Stream[]) {
  const labels = new Map<string, string>();
  for (const service of services) {
    const label = service.service_name || service.service_id || service.id || "";
    for (const key of [service.id, service.service_id]) {
      if (key && label) labels.set(key, label);
    }
  }
  for (const stream of streams) {
    if (stream.id && stream.name) labels.set(stream.id, stream.name);
  }
  return labels;
}

function displayReference(value: string, labels: Map<string, string>, uiText: UICopy = japaneseCopy) {
  const raw = value.trim();
  if (!raw) return "-";
  if (labels.has(raw)) return labels.get(raw) || raw;
  if (looksLikeInternalID(raw)) return uiText("未登録の対象");
  return raw;
}

function looksLikeInternalID(value: string) {
  if (/^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i.test(value)) return true;
  return /^(stream|worker|encoder|discord|observability|node)-[a-z0-9][a-z0-9-]*$/i.test(value);
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

function diagnosticLabel(value: string, uiText: UICopy = japaneseCopy) {
  const labels: Record<string, string> = {
    audio_status: uiText("音声状態"),
    encoder_preflight: uiText("Encoder事前診断"),
    worker_events: uiText("映像生成イベント"),
    google_drive: uiText("Google Drive接続"),
  };
  return labels[value] || value.replace(/[._]/g, " ") || uiText("診断");
}

function formatHeartbeat(value?: number, uiText: UICopy = japaneseCopy) {
  if (typeof value !== "number") return "-";
  if (value < 60) return uiText("{0}秒前", value);
  if (value < 3600) return uiText("{0}分前", Math.round(value / 60));
  return uiText("{0}時間前", Math.round(value / 3600));
}

function formatTimestamp(value: string, timezone?: string) {
  return formatDateTimeInTimeZone(value, timezone, { month: "2-digit", day: "2-digit", hour: "2-digit", minute: "2-digit" });
}
