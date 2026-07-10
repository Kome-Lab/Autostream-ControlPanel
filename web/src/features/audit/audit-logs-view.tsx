"use client";

import { useState } from "react";
import { useSearchParams } from "next/navigation";
import type { ColumnDef } from "@tanstack/react-table";
import { Check, Copy, Download, RefreshCcw, Search } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { DataTable } from "@/components/tables/data-table";
import { StatusBadge } from "@/components/admin/status-badge";
import { useAppSettings, useAuditLogs } from "@/features/queries";
import { useI18n } from "@/components/admin/i18n-provider";
import { formatDateTimeInTimeZone } from "@/lib/timezone";
import type { AuditLog } from "@/types/domain";

export function AuditLogsView() {
  const { t } = useI18n();
  const searchParams = useSearchParams();
  const [from, setFrom] = useState("");
  const [to, setTo] = useState("");
  const [result, setResult] = useState("all");
  const [queryInput, setQueryInput] = useState(() => searchParams.get("q") || "");
  const [query, setQuery] = useState(() => searchParams.get("q") || "");
  const auditLogs = useAuditLogs({ from, to, result, q: query });
  const appSettings = useAppSettings();
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
      cell: ({ row }) => auditActionLabel(row.original.action),
    },
    {
      accessorKey: "result",
      header: t("result"),
      cell: ({ row }) => <StatusBadge status={row.original.result} />,
    },
    {
      id: "resource",
      header: t("resource"),
      cell: ({ row }) => {
        const resourceID = row.original.resource_id || "";
        return (
          <div className="flex items-center gap-2 text-sm">
            <span>{resourceTypeLabel(row.original.resource_type)}</span>
            {resourceID ? (
              <Button variant="outline" size="icon-sm" aria-label="対象IDをコピー" onClick={() => void copyResourceID(resourceID)}>
                {copiedResourceID === resourceID ? <Check className="size-4" /> : <Copy className="size-4" />}
              </Button>
            ) : null}
          </div>
        );
      },
    },
    { accessorKey: "actor_ip", header: "IP" },
    { accessorKey: "user_agent", header: t("userAgent") },
  ];

  const exportURL = `/audit-logs/export?${new URLSearchParams({
    ...(from ? { from } : {}),
    ...(to ? { to } : {}),
    ...(result !== "all" ? { result } : {}),
    ...(query ? { q: query } : {}),
  }).toString()}`;

  return (
    <div className="space-y-5">
      <section className="border-b pb-5">
        <div className="text-sm font-medium text-primary">変更履歴</div>
        <h1 className="mt-1 text-xl font-semibold">誰が、いつ、何を操作したかを確認</h1>
        <p className="mt-1 text-sm text-muted-foreground">配信枠IDや操作名で絞り込み、社内報告や原因調査に利用できます。</p>
      </section>
      {auditLogs.isError ? (
        <div className="flex flex-col gap-3 rounded-lg border border-amber-300 bg-amber-50 p-4 text-amber-900 dark:border-amber-900 dark:bg-amber-950/35 dark:text-amber-100 sm:flex-row sm:items-center sm:justify-between">
          <div><div className="text-sm font-semibold">操作履歴を取得できませんでした</div><p className="mt-0.5 text-xs">通信状態と権限を確認し、再試行してください。</p></div>
          <Button variant="outline" size="sm" onClick={() => auditLogs.refetch()}><RefreshCcw className="size-4" />再試行</Button>
        </div>
      ) : null}
      <Card>
        <CardHeader className="gap-3 border-b md:flex-row md:items-center md:justify-between">
          <CardTitle>{t("auditLogs")}</CardTitle>
          <Button asChild variant="outline" size="sm"><a href={exportURL}><Download />CSV</a></Button>
        </CardHeader>
        <CardContent className="space-y-4">
          <form className="grid gap-3 lg:grid-cols-[minmax(220px,1fr)_180px_180px_160px_auto]" onSubmit={(event) => { event.preventDefault(); setQuery(queryInput.trim()); }}>
            <label className="relative"><Search className="pointer-events-none absolute left-3 top-1/2 size-4 -translate-y-1/2 text-muted-foreground" /><Input value={queryInput} onChange={(event) => setQueryInput(event.target.value)} placeholder="配信枠ID・操作名・ユーザー" className="pl-9" aria-label="監査ログの検索語" /></label>
            <Input type="datetime-local" value={from} onChange={(event) => setFrom(event.target.value)} aria-label="開始日時" />
            <Input type="datetime-local" value={to} onChange={(event) => setTo(event.target.value)} aria-label="終了日時" />
            <Select value={result} onValueChange={setResult}><SelectTrigger aria-label="操作結果"><SelectValue /></SelectTrigger><SelectContent><SelectItem value="all">すべての結果</SelectItem><SelectItem value="success">成功</SelectItem><SelectItem value="failure">失敗</SelectItem></SelectContent></Select>
            <Button type="submit" variant="outline"><Search className="size-4" />検索</Button>
          </form>
          {query ? <div className="text-xs text-muted-foreground">「{query}」に一致する操作履歴を表示しています。</div> : null}
          <DataTable columns={columns} data={auditLogs.data || []} filterPlaceholder="表示中の履歴をさらに絞り込み" getRowId={(row) => row.id} minTableWidthClass="min-w-[1040px]" />
        </CardContent>
      </Card>
    </div>
  );
}

function formatDateTime(value?: string, timezone?: string) {
  return formatDateTimeInTimeZone(value, timezone, { year: "numeric", month: "2-digit", day: "2-digit", hour: "2-digit", minute: "2-digit" });
}

function resourceTypeLabel(value?: string) {
  const raw = (value || "").trim();
  if (!raw) return "-";
  const labels: Record<string, string> = {
    archive_artifact: "録画ファイル",
    archive_share: "共有リンク",
    archive_destination: "Drive保存先",
    audit_log: "監査ログ",
    discord_config: "Discord BOT設定",
    notification_channel: "通知先",
    oauth_account: "OAuth接続アカウント",
    oauth_provider: "OAuthプロバイダ",
    profile: "プロファイル",
    role: "ロール",
    secret: "シークレット",
    service: "Node",
    stream: "配信枠",
    user: "ユーザー",
    worker: "Worker Node",
    node: "Node",
    youtube_output: "YouTube出力",
  };
  return labels[raw] || raw.replace(/_/g, " ");
}

function auditActionLabel(value?: string) {
  const raw = (value || "").trim();
  if (!raw) return "-";
  const labels: Record<string, string> = {
    "app.settings.update": "アプリ設定を更新",
    "archive_destinations.create": "Drive保存先を作成",
    "archive_destinations.delete": "Drive保存先を削除",
    "archive_destinations.update": "Drive保存先を更新",
    "discord_configs.create": "Discord BOT設定を作成",
    "discord_configs.delete": "Discord BOT設定を削除",
    "discord_configs.update": "Discord BOT設定を更新",
    "nodes.delete": "Nodeを削除",
    "nodes.registration_token.create": "Node設定を発行",
    "nodes.runtime_token.rotate": "Node Runtime Tokenを再生成",
    "nodes.update": "Nodeを更新",
    "notification_channels.create": "通知先を作成",
    "notification_channels.delete": "通知先を削除",
    "notification_channels.test": "通知テストを送信",
    "notification_channels.update": "通知先を更新",
    "oauth_accounts.create": "OAuth接続アカウントを作成",
    "oauth_accounts.delete": "OAuth接続アカウントを削除",
    "oauth_accounts.update": "OAuth接続アカウントを更新",
    "oauth_providers.create": "OAuthプロバイダを作成",
    "oauth_providers.delete": "OAuthプロバイダを削除",
    "oauth_providers.update": "OAuthプロバイダを更新",
    "roles.create": "ロールを作成",
    "roles.delete": "ロールを削除",
    "roles.update": "ロールを更新",
    "secrets.update": "シークレットを更新",
    "streams.create": "配信枠を作成",
    "streams.start": "配信を開始",
    "streams.stop": "配信を停止",
    "streams.update": "配信枠を更新",
    "users.create": "ユーザーを作成",
    "users.delete": "ユーザーを削除",
    "users.update": "ユーザーを更新",
    "workers.restart": "Workerを再起動",
    "youtube_outputs.create": "YouTube出力を作成",
    "youtube_outputs.delete": "YouTube出力を削除",
    "youtube_outputs.update": "YouTube出力を更新",
  };
  if (labels[raw]) return labels[raw];
  return raw
    .replace(/[_\-.]+/g, " ")
    .replace(/\b\w/g, (letter) => letter.toUpperCase());
}
