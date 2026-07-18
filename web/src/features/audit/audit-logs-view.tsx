"use client";

import { useState } from "react";
import { useSearchParams } from "next/navigation";
import type { ColumnDef } from "@tanstack/react-table";
import { Check, Copy, Download, RefreshCcw, Search } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { DataTable } from "@/components/tables/data-table";
import { StatusBadge } from "@/components/admin/status-badge";
import { useAppSettings, useAuditLogs } from "@/features/queries";
import { useI18n } from "@/components/admin/i18n-provider";
import { auditActionLabel } from "@/lib/audit-action";
import { formatDateTimeInTimeZone } from "@/lib/timezone";
import type { AuditLog } from "@/types/domain";

const serviceRuntimeReadActionGroup = "service_runtime_reads";
type AuditView = "operations" | "service-runtime-reads";

export function AuditLogsView() {
  const { t } = useI18n();
  const searchParams = useSearchParams();
  const [from, setFrom] = useState("");
  const [to, setTo] = useState("");
  const [result, setResult] = useState("all");
  const [queryInput, setQueryInput] = useState(() => searchParams.get("q") || "");
  const [query, setQuery] = useState(() => searchParams.get("q") || "");
  const [view, setView] = useState<AuditView>(() => searchParams.get("tab") === "service-runtime-reads" ? "service-runtime-reads" : "operations");
  const serviceRuntimeReadView = view === "service-runtime-reads";
  const auditLogs = useAuditLogs({
    from,
    to,
    result,
    q: query,
    ...(serviceRuntimeReadView
      ? { actionGroup: serviceRuntimeReadActionGroup }
      : { excludeActionGroup: serviceRuntimeReadActionGroup }),
  });
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

  const exportParams = new URLSearchParams({
    ...(from ? { from } : {}),
    ...(to ? { to } : {}),
    ...(result !== "all" ? { result } : {}),
    ...(query ? { q: query } : {}),
    ...(serviceRuntimeReadView
      ? { action_group: serviceRuntimeReadActionGroup }
      : { exclude_action_group: serviceRuntimeReadActionGroup }),
  });
  const exportURL = `/audit-logs/export?${exportParams.toString()}`;

  return (
    <div className="space-y-5">
      <section className="border-b pb-5">
        <div className="text-sm font-medium text-primary">監視・対応</div>
        <h1 className="mt-1 text-xl font-semibold">監査ログ</h1>
        <p className="mt-1 text-sm text-muted-foreground">管理操作とNodeによる定期的な設定参照を分けて確認できます。</p>
      </section>
      {auditLogs.isError ? (
        <div className="flex flex-col gap-3 rounded-lg border border-amber-300 bg-amber-50 p-4 text-amber-900 dark:border-amber-900 dark:bg-amber-950/35 dark:text-amber-100 sm:flex-row sm:items-center sm:justify-between">
          <div><div className="text-sm font-semibold">{serviceRuntimeReadView ? "Node設定参照を取得できませんでした" : "操作履歴を取得できませんでした"}</div><p className="mt-0.5 text-xs">通信状態と権限を確認し、再試行してください。</p></div>
          <Button variant="outline" size="sm" onClick={() => auditLogs.refetch()}><RefreshCcw className="size-4" />再試行</Button>
        </div>
      ) : null}
      <Tabs value={view} onValueChange={(value) => setView(value as AuditView)} className="space-y-4">
        <TabsList variant="line" className="h-auto w-full justify-start border-b pb-1">
          <TabsTrigger value="operations">操作履歴</TabsTrigger>
          <TabsTrigger value="service-runtime-reads">Node設定参照</TabsTrigger>
        </TabsList>
        <TabsContent value={view}>
          <Card>
            <CardHeader className="gap-3 border-b md:flex-row md:items-center md:justify-between">
              <div>
                <CardTitle>{serviceRuntimeReadView ? "Node設定参照" : "操作履歴"}</CardTitle>
                <CardDescription className="mt-1">{serviceRuntimeReadView ? "Nodeが実行設定を取得した記録です。" : "担当者とシステムによる変更・操作の記録です。"}</CardDescription>
              </div>
              <Button asChild variant="outline" size="sm"><a href={exportURL}><Download />CSV</a></Button>
            </CardHeader>
            <CardContent className="space-y-4">
              <form className="grid gap-3 lg:grid-cols-[minmax(220px,1fr)_180px_180px_160px_auto]" onSubmit={(event) => { event.preventDefault(); setQuery(queryInput.trim()); }}>
                <label className="relative"><Search className="pointer-events-none absolute left-3 top-1/2 size-4 -translate-y-1/2 text-muted-foreground" /><Input value={queryInput} onChange={(event) => setQueryInput(event.target.value)} placeholder={serviceRuntimeReadView ? "Node ID・結果" : "配信枠ID・操作名・ユーザー"} className="pl-9" aria-label="監査ログの検索語" /></label>
                <Input type="datetime-local" value={from} onChange={(event) => setFrom(event.target.value)} aria-label="開始日時" />
                <Input type="datetime-local" value={to} onChange={(event) => setTo(event.target.value)} aria-label="終了日時" />
                <Select value={result} onValueChange={setResult}><SelectTrigger aria-label="操作結果"><SelectValue /></SelectTrigger><SelectContent><SelectItem value="all">すべての結果</SelectItem><SelectItem value="success">成功</SelectItem><SelectItem value="failure">失敗</SelectItem></SelectContent></Select>
                <Button type="submit" variant="outline"><Search className="size-4" />検索</Button>
              </form>
              {query ? <div className="text-xs text-muted-foreground">「{query}」に一致する履歴を表示しています。</div> : null}
              <DataTable columns={columns} data={auditLogs.data || []} filterPlaceholder="表示中の履歴をさらに絞り込み" getRowId={(row) => row.id} minTableWidthClass="min-w-[1040px]" />
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
