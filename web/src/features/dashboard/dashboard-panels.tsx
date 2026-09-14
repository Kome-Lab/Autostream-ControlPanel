"use client";

import Link from "next/link";
import { AlertTriangle } from "lucide-react";
import { useI18n } from "@/components/admin/i18n-provider";
import { DefinitionList } from "@/components/data-display/definition-list";
import { DomainStatusBadge } from "@/components/foundation/status/domain-status-badge";
import { DetailSection } from "@/components/layout/detail-section";
import { presentNodeConnectivityStatus, presentNodeHealthStatus } from "@/lib/foundation/status/node-presenters";
import { hasRecordingConfiguration } from "@/lib/stream-presentation";
import type { Stream, WorkerNode } from "@/types/domain";

export type DashboardIncident = { id: string; title?: string; severity?: string; status?: string };

export function DashboardIncidentBanner({ rows, unavailable, refreshing }: {
  rows: readonly DashboardIncident[] | undefined; unavailable: boolean; refreshing: boolean;
}) {
  const { locale } = useI18n();
  const ja = locale === "ja";
  const open = (rows || []).filter((row) => ["open", "active", "firing", "warning", "critical", "acknowledged"].includes(row.status || ""));
  const unknown = (rows || []).filter((row) => !["open", "active", "firing", "warning", "critical", "acknowledged", "resolved", "closed", "suppressed"].includes(row.status || ""));
  if (!open.length && !unknown.length && !unavailable && rows !== undefined && !refreshing) return null;
  return <aside data-slot="dashboard-incidents" role="status" className="flex flex-wrap items-center gap-3 rounded-md border border-status-warning-border bg-status-warning-subtle px-4 py-3 text-status-warning-foreground">
    <AlertTriangle className="size-4 shrink-0" aria-hidden="true" />
    <div className="min-w-0 flex-1 text-sm">
      <strong>{open.length ? (ja ? `取得済みの未解決インシデント: ${open.length} 件` : `${open.length} unresolved incidents in loaded results`) : (ja ? "インシデントの状態を確認中" : "Checking incident status")}</strong>
      {open.slice(0, 2).map((row) => <p key={row.id} className="[overflow-wrap:anywhere]">{row.severity || (ja ? "重大度不明" : "Unknown severity")} · {row.title || row.id}</p>)}
      {unknown.length ? <p>{ja ? `状態不明: ${unknown.length} 件。専用画面で確認してください。` : `${unknown.length} incidents have unknown state. Review them in the incident workspace.`}</p> : null}
      {unavailable ? <p>{ja ? "最新状態を取得できません。前回の表示を含む場合があります。" : "Latest status is unavailable. Displayed data may be stale."}</p> : refreshing ? <p>{ja ? "更新中" : "Refreshing"}</p> : null}
    </div>
    <Link href="/admin/incidents/" className="min-h-11 py-3 text-sm underline underline-offset-4">{ja ? "インシデントを開く" : "View incidents"}</Link>
  </aside>;
}

export function DashboardServices({ rows }: { rows: readonly WorkerNode[] }) {
  const { locale, t } = useI18n();
  return <DetailSection id="dashboard-services" title={locale === "ja" ? "サービス稼働" : "Service availability"}
    actions={<Link href="/admin/service-health/" className="text-sm text-primary underline">{locale === "ja" ? "すべて表示" : "View all"}</Link>}>
    {rows.length ? <div className="divide-y">{rows.slice(0, 6).map((row) => <article key={row.service_id || row.id} className="space-y-3 py-3 first:pt-0">
      <h3 className="text-sm font-medium [overflow-wrap:anywhere]">{row.service_name || row.service_id || row.id}</h3>
      <DefinitionList items={[
        { label: locale === "ja" ? "登録・接続" : "Registration / connection", value: <DomainStatusBadge presentation={presentNodeConnectivityStatus(row.status)} translate={t} /> },
        { label: locale === "ja" ? "プロセス稼働" : "Process health", value: <DomainStatusBadge presentation={presentNodeHealthStatus(row.health_status)} translate={t} /> },
        { label: locale === "ja" ? "担当配信" : "Current stream", value: row.current_stream_id || (locale === "ja" ? "担当なし" : "Unassigned") },
        { label: locale === "ja" ? "種類" : "Type", value: row.service_type },
      ]} />
    </article>)}</div> : <p className="text-sm text-muted-foreground">{locale === "ja" ? "取得済みのサービスはありません。" : "No services in loaded results."}</p>}
  </DetailSection>;
}

export function DashboardOutputs({ streams, canReadArchive }: { streams: readonly Stream[]; canReadArchive: boolean }) {
  const { locale } = useI18n();
  const ja = locale === "ja";
  return <DetailSection id="dashboard-outputs" title={ja ? "YouTube・録画・Archive" : "YouTube, recording and archive"}
    description={ja ? "取得済み配信枠の設定です。成果物の完了やアップロード成功を示す件数ではありません。" : "Configuration in loaded stream slots. These counts do not indicate completed artifacts or successful uploads."}>
    <DefinitionList items={[
      { label: ja ? "YouTube出力設定" : "YouTube configured", value: streams.filter((row) => Boolean(row.youtube_output_id)).length },
      { label: ja ? "録画設定" : "Recording configured", value: streams.filter(hasRecordingConfiguration).length },
      { label: ja ? "Run ID報告あり" : "Archive run reported", value: streams.filter((row) => Boolean(row.archive_run_id)).length },
    ]} />
    {canReadArchive ? <Link href="/admin/archive/" className="mt-4 inline-flex min-h-11 items-center text-sm text-primary underline">{ja ? "処理状態と成果物を確認" : "Review processing and artifacts"}</Link> : null}
  </DetailSection>;
}
