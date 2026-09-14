"use client";

import { useMemo } from "react";
import Link from "next/link";
import { AlertTriangle, Plus, RadioTower, RefreshCcw } from "lucide-react";
import { useI18n } from "@/components/admin/i18n-provider";
import { DetailSection } from "@/components/layout/detail-section";
import { PageActions } from "@/components/shell/page-actions";
import { PageHeader } from "@/components/shell/page-header";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import { useCurrentUser, useResourceData, useServiceHealth, useStreams } from "@/features/queries";
import { OperationalStateNotice } from "@/features/monitoring/operational-state-notice";
import {
  aggregateOperationalQueries, countOperationalStreams, knownEmptyOperationalQuery,
  operationalQuerySnapshot, remoteStateAllowsPositiveSummary,
  serviceAvailabilityContribution, summarizeServiceAvailability,
} from "@/features/monitoring/operational-remote-state";
import { hasPermission } from "@/lib/auth/permissions";
import { DashboardStreams, dashboardStreamGroups } from "./dashboard-streams";
import { DashboardIncidentBanner, DashboardOutputs, DashboardServices, type DashboardIncident } from "./dashboard-panels";

export function DashboardView() {
  const { locale } = useI18n();
  const ja = locale === "ja";
  const currentUser = useCurrentUser();
  const canReadStreams = hasPermission(currentUser.data, "streams.read");
  const canCreateStreams = hasPermission(currentUser.data, "streams.create");
  const canReadServices = hasPermission(currentUser.data, "service_health.read");
  const canReadIncidents = hasPermission(currentUser.data, "incidents.read");
  const streams = useStreams(canReadStreams);
  const services = useServiceHealth(canReadServices);
  const incidents = useResourceData<DashboardIncident[]>("/observability/incidents", canReadIncidents);
  const streamRows = useMemo(() => streams.data || [], [streams.data]);
  const serviceRows = useMemo(() => services.data || [], [services.data]);
  const statusCounts = useMemo(() => countOperationalStreams(streamRows), [streamRows]);
  const serviceCoverage = useMemo(() => summarizeServiceAvailability(serviceRows), [serviceRows]);
  const groups = useMemo(() => dashboardStreamGroups(streamRows), [streamRows]);
  const serviceNames = useMemo(() => new Map(serviceRows.flatMap((row) =>
    [row.id, row.service_id].filter((id): id is string => Boolean(id)).map((id) => [id, row.service_name || id] as const))), [serviceRows]);
  const remoteState = aggregateOperationalQueries("dashboard", {
    streams: canReadStreams ? operationalQuerySnapshot(streams) : knownEmptyOperationalQuery(),
    services: canReadServices ? operationalQuerySnapshot(services) : knownEmptyOperationalQuery(),
  });
  const serviceIssues = serviceRows.filter((service) => {
    const contribution = serviceAvailabilityContribution(service);
    return contribution.kind === "known" && !contribution.positive;
  });
  const unknownIssueCount = statusCounts.unknown + serviceCoverage.unknownCount;
  const issueSummaryConfirmed = remoteStateAllowsPositiveSummary(remoteState, unknownIssueCount, canReadStreams || canReadServices);
  const refreshing = streams.isFetching || services.isFetching || (canReadIncidents && incidents.isFetching);
  const refresh = () => {
    if (canReadStreams) void streams.refetch();
    if (canReadServices) void services.refetch();
    if (canReadIncidents) void incidents.refetch();
  };
  const recent = [...streamRows].filter((row) => row.updated_at).sort((a, b) => String(b.updated_at).localeCompare(String(a.updated_at))).slice(0, 5);

  return <div className="space-y-5" data-screen-family="dashboard">
    <PageHeader title={ja ? "自動配信オペレーション" : "Live operations"}
      description={ja ? "配信中の枠を最初に確認し、サービス、出力、録画、要対応を続けて確認できます。" : "Review active streams first, then services, outputs, recording and action items."}
      breadcrumbs={[{ label: ja ? "管理" : "Admin", href: "/admin/" }, { label: ja ? "ダッシュボード" : "Dashboard" }]}
      eyebrow={<><RadioTower className="size-4" aria-hidden="true" />Discord VC</>}
      actions={<PageActions primary={canCreateStreams ? <Button asChild><Link href="/admin/streams/#create-stream"><Plus aria-hidden="true" />{ja ? "配信枠を作成" : "Create stream"}</Link></Button> : null}
        secondary={<Button variant="outline" disabled={refreshing} onClick={refresh}><RefreshCcw aria-hidden="true" />{ja ? "最新状態に更新" : "Refresh"}</Button>} />} />
    {canReadIncidents ? <DashboardIncidentBanner rows={incidents.data} unavailable={incidents.isError} refreshing={incidents.isFetching} /> : null}
    <OperationalStateNotice state={remoteState} consumer="dashboard" />
    {currentUser.isLoading || remoteState.kind === "initial-loading" ? <div role="status" aria-label={ja ? "読込中" : "Loading"}><Skeleton className="h-72 w-full" /></div> : <>
      <DetailSection id="dashboard-active" title={ja ? "配信中・開始中" : "Active and starting streams"}
        description={ja ? "停止処理中の枠も表示します。開始可否は配信詳細の最新Readinessで確認してください。" : "Includes streams that are stopping. Check fresh Readiness in stream details before starting."}
        actions={canReadStreams ? <Link className="text-sm text-primary underline" href="/admin/streams/">{ja ? "配信枠を開く" : "View streams"}</Link> : null}>
        {canReadStreams ? <DashboardStreams rows={groups.active} serviceNames={serviceNames} /> : <p>{ja ? "配信の参照権限がありません。" : "You do not have permission to read streams."}</p>}
      </DetailSection>
      <div className="grid min-w-0 gap-6 xl:grid-cols-2 min-[1800px]:grid-cols-3">
        {canReadServices ? <DashboardServices rows={serviceRows} /> : <DetailSection title={ja ? "サービス稼働" : "Service availability"}><p>{ja ? "サービス状態の参照権限がありません。" : "You do not have permission to read service health."}</p></DetailSection>}
        {canReadStreams ? <DashboardOutputs streams={streamRows} canReadArchive={hasPermission(currentUser.data, "archives.read")} /> : null}
        <DetailSection id="dashboard-attention" title={ja ? "要対応" : "Action items"}>
          {groups.issues.length || serviceIssues.length ? <ul className="space-y-3">
            {groups.issues.map((row) => <li key={row.id}><Link href="/admin/streams/" className="flex items-start gap-2 text-sm text-primary underline"><AlertTriangle className="size-4 shrink-0" aria-hidden="true" />{row.name}</Link></li>)}
            {serviceIssues.slice(0, 6).map((row) => <li key={row.id}><Link href="/admin/service-health/" className="text-sm text-primary underline">{row.service_name || row.service_id || row.id}</Link></li>)}
          </ul> : <p className="text-sm">{issueSummaryConfirmed ? (ja ? "参照可能な配信・サービスに対応待ちはありません。" : "No pending issues in accessible streams and services.") : (ja ? "要対応の有無を判定できません。" : "Cannot determine whether action is required.")}</p>}
          {unknownIssueCount > 0 ? <p className="mt-3 text-sm text-status-warning">{ja ? "状態不明: " + unknownIssueCount + " 件" : unknownIssueCount + " items have unknown state"}</p> : null}
        </DetailSection>
      </div>
      {canReadStreams ? <DetailSection id="dashboard-waiting" title={ja ? "待機中・下書き" : "Waiting and draft streams"}
        description={ja ? "VC参加または手動開始を待つ配信枠です。待機状態は開始可能の保証ではありません。" : "Slots awaiting voice participation or a manual start. Waiting does not guarantee readiness."}>
        <DashboardStreams rows={groups.waiting} serviceNames={serviceNames} />
      </DetailSection> : null}
      <DetailSection id="dashboard-recent" title={ja ? "最近の更新と運用記録" : "Recent updates and operational records"}>
        {canReadStreams ? <ul className="space-y-2 text-sm">{recent.map((row) => <li key={row.id} className="flex flex-wrap justify-between gap-2"><Link href="/admin/streams/" className="text-primary underline">{row.name}</Link><time dateTime={row.updated_at}>{row.updated_at}</time></li>)}</ul> : null}
        <div className="mt-4 flex flex-wrap gap-4 text-sm">
          {hasPermission(currentUser.data, "audit_logs.read") ? <Link className="text-primary underline" href="/admin/audit-logs/">{ja ? "監査ログ" : "Audit logs"}</Link> : null}
          {hasPermission(currentUser.data, "archives.read") ? <Link className="text-primary underline" href="/admin/archive/">{ja ? "録画・アーカイブ" : "Recording and archive"}</Link> : null}
          {hasPermission(currentUser.data, "system_settings.read") ? <Link className="text-primary underline" href="/admin/security/">{ja ? "セキュリティ" : "Security"}</Link> : null}
        </div>
      </DetailSection>
    </>}
  </div>;
}
