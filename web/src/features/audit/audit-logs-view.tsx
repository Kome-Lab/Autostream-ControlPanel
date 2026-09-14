"use client";
import { fixedPresentationText } from "@/lib/i18n/ui-v2/presentation-copy";

import { useUICopy } from "@/lib/i18n/ui-v2/use-ui-copy";
import { japaneseCopy, type UICopy } from "@/lib/i18n/ui-v2/copy";


import { useState } from "react";
import { useSearchParams } from "next/navigation";
import type { ColumnDef } from "@tanstack/react-table";
import { Check, Copy, Download, Search } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { DataTable } from "@/components/tables/data-table";
import { DomainStatusBadge } from "@/components/foundation/status/domain-status-badge";
import { presentAuditResultStatus } from "@/lib/foundation/status/observability-presenters";
import { PageHeader } from "@/components/shell/page-header";
import { Field } from "@/components/forms/field";
import { useAppSettings, useAuditLogs } from "@/features/queries";
import { useI18n } from "@/components/admin/i18n-provider";
import { auditActionLabel } from "@/lib/audit-action";
import { formatDateTimeInTimeZone } from "@/lib/timezone";
import { aggregateRemainingQueries, remainingQuerySnapshot } from "@/features/remote-state/remaining-remote-state";
import { RemainingStateNotice } from "@/features/remote-state/remaining-state-notice";
import type { AuditLog } from "@/types/domain";

const nodeActivityActionGroup = "node_activity";
type AuditView = "operations" | "node-activity";

export function AuditLogsView() {
  const uiText = useUICopy();
  const { t, locale } = useI18n();
  const ja = locale === "ja";
  const searchParams = useSearchParams();
  const [from, setFrom] = useState("");
  const [to, setTo] = useState("");
  const [result, setResult] = useState("all");
  const [queryInput, setQueryInput] = useState(() => searchParams.get("q") || "");
  const [query, setQuery] = useState(() => searchParams.get("q") || "");
  const [view, setView] = useState<AuditView>(() => ["node-activity", "service-runtime-reads"].includes(searchParams.get("tab") || "") ? "node-activity" : "operations");
  const nodeActivityView = view === "node-activity";
  const auditLogs = useAuditLogs({
    from,
    to,
    result,
    q: query,
    ...(nodeActivityView
      ? { actionGroup: nodeActivityActionGroup }
      : { excludeActionGroup: nodeActivityActionGroup }),
  });
  const appSettings = useAppSettings();
  const remoteState = aggregateRemainingQueries("audit", { "audit-logs": remainingQuerySnapshot(auditLogs) });
  const timezone = appSettings.data?.timezone;
  const [copiedResourceID, setCopiedResourceID] = useState("");

  const copyResourceID = async (id: string) => {
    if (!id || typeof navigator === "undefined" || !navigator.clipboard) return;
    try {
      await navigator.clipboard.writeText(id);
    } catch {
      return;
    }
    setCopiedResourceID(id);
    window.setTimeout(() => setCopiedResourceID((current) => (current === id ? "" : current)), 1500);
  };

  const columns: ColumnDef<AuditLog>[] = [
    {
      accessorKey: "timestamp",
      header: t("time"),
      cell: ({ row }) => formatDateTime(row.original.timestamp, timezone),
    },
    { accessorKey: "actor_username", header: t("actor") },
    {
      accessorKey: "action",
      header: t("action"),
      cell: ({ row }) => fixedPresentationText(auditActionLabel(row.original.action), uiText),
    },
    {
      accessorKey: "result",
      meta: { required: true, priority: 0 },
      header: t("result"),
      cell: ({ row }) => <DomainStatusBadge presentation={presentAuditResultStatus(row.original.result)} translate={t} />,
    },
    {
      id: "resource",
      header: t("resource"),
      cell: ({ row }) => {
        const resourceID = row.original.resource_id || "";
        return (
          <div className="flex items-center gap-2 text-sm">
            <span>{resourceTypeLabel(row.original.resource_type, uiText)}</span>
            {resourceID ? (
              <Button variant="outline" size="icon-sm" aria-label={uiText("対象IDをコピー")} onClick={() => void copyResourceID(resourceID)}>
                {copiedResourceID === resourceID ? <Check className="size-4" /> : <Copy className="size-4" />}
              </Button>
            ) : null}
          </div>
        );
      },
    },
    { accessorKey: "actor_ip", header: "IP", meta: { priority: 2 } },
    { accessorKey: "user_agent", header: t("userAgent"), meta: { priority: 3 } },
  ];

  const exportParams = new URLSearchParams({
    ...(from ? { from } : {}),
    ...(to ? { to } : {}),
    ...(result !== "all" ? { result } : {}),
    ...(query ? { q: query } : {}),
    ...(nodeActivityView
      ? { action_group: nodeActivityActionGroup }
      : { exclude_action_group: nodeActivityActionGroup }),
  });
  const exportURL = `/audit-logs/export?${exportParams.toString()}`;

  return (
    <div className="space-y-5" data-screen-family="audit">
      <PageHeader title={ja ? "監査ログ" : "Audit logs"} description={ja ? "担当者の操作とNodeの報告・通信を分けて確認します。検索条件はサーバーへ送信し、取得した結果をその順序で表示します。" : "Review operator actions separately from Node reports and communication. Filters are applied by the server; results retain their received order."}
        actions={<Button variant="outline" disabled={auditLogs.isFetching} onClick={() => void auditLogs.refetch()}>{ja ? "更新" : "Refresh"}</Button>} />
      {remoteState.kind !== "ready" || remoteState.freshness.kind !== "fresh" ? <RemainingStateNotice state={remoteState} consumer="audit" /> : null}
      <Tabs value={view} onValueChange={(value) => setView(value as AuditView)} className="space-y-4">
        <TabsList variant="line" className="h-auto w-full justify-start border-b pb-1">
          <TabsTrigger value="operations">{uiText("操作履歴")}</TabsTrigger>
          <TabsTrigger value="node-activity">{uiText("Node報告・通信")}</TabsTrigger>
        </TabsList>
        <TabsContent value={view}>
          <Card>
            <CardHeader className="gap-3 border-b md:flex-row md:items-center md:justify-between">
              <div>
                <CardTitle>{nodeActivityView ? uiText("Node報告・通信") : uiText("操作履歴")}</CardTitle>
                <CardDescription className="mt-1">{nodeActivityView ? uiText("Node / Host Agentの登録、Heartbeat、設定参照、Observability Signals送信の記録です。") : uiText("担当者とシステムによる変更・操作の記録です。")}</CardDescription>
              </div>
              <Button asChild variant="outline" size="sm"><a href={exportURL}><Download />CSV</a></Button>
            </CardHeader>
            <CardContent className="space-y-4">
              <form className="grid gap-3 lg:grid-cols-[minmax(220px,1fr)_180px_180px_160px_auto]" onSubmit={(event) => { event.preventDefault(); setQuery(queryInput.trim()); }}>
                <label className="relative"><Search className="pointer-events-none absolute left-3 top-1/2 size-4 -translate-y-1/2 text-muted-foreground" /><Input value={queryInput} onChange={(event) => setQueryInput(event.target.value)} placeholder={nodeActivityView ? uiText("Node ID・シグナル・結果") : uiText("配信枠ID・操作名・ユーザー")} className="pl-9" aria-label={uiText("監査ログの検索語")} /></label>
                <Field label={ja ? "開始日時" : "From"}><Input type="datetime-local" value={from} onChange={(event) => setFrom(event.target.value)} aria-label={uiText("開始日時")} /></Field>
                <Field label={ja ? "終了日時" : "To"}><Input type="datetime-local" value={to} onChange={(event) => setTo(event.target.value)} aria-label={uiText("終了日時")} /></Field>
                <Select value={result} onValueChange={setResult}><SelectTrigger aria-label={uiText("操作結果")}><SelectValue /></SelectTrigger><SelectContent><SelectItem value="all">{uiText("すべての結果")}</SelectItem><SelectItem value="success">{uiText("成功")}</SelectItem><SelectItem value="failure">{uiText("失敗")}</SelectItem></SelectContent></Select>
                <Button type="submit" variant="outline"><Search className="size-4" />{uiText("検索")}</Button>
              </form>
              {query ? <div className="text-xs text-muted-foreground">「{query}{uiText("」に一致する履歴を表示しています。")}</div> : null}
              <DataTable columns={columns} data={auditLogs.data || []} mode="server" density="compact" getRowId={(row) => row.id} minTableWidthClass="min-w-[1040px]" />
            </CardContent>
          </Card>
        </TabsContent>
      </Tabs>
    </div>
  );
}

function formatDateTime(value?: string, timezone?: string) {
  return formatDateTimeInTimeZone(value, timezone, { year: "numeric", month: "2-digit", day: "2-digit", hour: "2-digit", minute: "2-digit" });
}

function resourceTypeLabel(value?: string, uiText: UICopy = japaneseCopy) {
  const raw = (value || "").trim();
  if (!raw) return "-";
  const labels: Record<string, string> = {
    archive_artifact: uiText("録画ファイル"),
    archive_share: uiText("共有リンク"),
    archive_destination: uiText("Drive保存先"),
    audit_log: uiText("監査ログ"),
    discord_config: uiText("Discord BOT設定"),
    notification_channel: uiText("通知先"),
    oauth_account: uiText("OAuth接続アカウント"),
    oauth_provider: uiText("OAuthプロバイダ"),
    profile: uiText("プロファイル"),
    role: uiText("ロール"),
    secret: uiText("シークレット"),
    service: "Node",
    stream: uiText("配信枠"),
    user: uiText("ユーザー"),
    worker: "Worker Node",
    node: "Node",
    youtube_output: uiText("YouTube出力"),
  };
  return labels[raw] || raw.replace(/_/g, " ");
}
