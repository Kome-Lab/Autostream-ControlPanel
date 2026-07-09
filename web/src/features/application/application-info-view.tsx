"use client";

import { type ReactNode, useMemo } from "react";
import { Activity, GitCommit, RefreshCcw, ServerCog } from "lucide-react";
import { StatusBadge } from "@/components/admin/status-badge";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { useAppSettings, useServiceHealth, useVersion } from "@/features/queries";
import { formatDateTimeInTimeZone } from "@/lib/timezone";
import type { WorkerNode } from "@/types/domain";

export function ApplicationInfoView() {
  const appSettings = useAppSettings();
  const appVersion = useVersion();
  const services = useServiceHealth();
  const timezone = appSettings.data?.timezone;
  const nodeRows = useMemo(() => [...(services.data || [])].sort(compareServiceRows), [services.data]);

  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div>
          <h1 className="text-2xl font-semibold tracking-normal">アプリケーション情報</h1>
          <p className="text-sm text-muted-foreground">Control Panelと登録済みNodeのビルド情報を確認します。</p>
        </div>
        <div className="flex gap-2">
          <Button variant="outline" size="sm" onClick={() => appVersion.refetch()} disabled={appVersion.isFetching}>
            <RefreshCcw className="size-4" />
            Panel更新確認
          </Button>
          <Button variant="outline" size="sm" onClick={() => services.refetch()} disabled={services.isFetching}>
            <RefreshCcw className="size-4" />
            Node更新
          </Button>
        </div>
      </div>

      <div className="grid gap-4 xl:grid-cols-[minmax(320px,0.85fr)_minmax(0,1.15fr)]">
        <Card>
          <CardHeader>
            <CardTitle className="flex items-center gap-2">
              <Activity className="size-5" />
              Control Panel
            </CardTitle>
            <CardDescription>管理画面とAPIサーバーのビルド情報です。</CardDescription>
          </CardHeader>
          <CardContent className="space-y-4">
            <div className="grid gap-3 sm:grid-cols-2">
              <InfoItem label="バージョン" value={appVersion.data?.version || "dev"} />
              <InfoItem label="コミット" value={shortCommit(appVersion.data?.commit)} monospace />
              <InfoItem label="ビルド日時" value={formatOptionalDate(appVersion.data?.build_date, timezone)} />
              <InfoItem label="更新確認" value={<UpdateStatusBadge state={controlPanelUpdateState(appVersion.data)} />} />
            </div>
            {appVersion.data?.update_check_error ? <p className="text-sm text-amber-700">更新確認エラー: {appVersion.data.update_check_error}</p> : null}
          </CardContent>
        </Card>

        <Card>
          <CardHeader>
            <CardTitle className="flex items-center gap-2">
              <ServerCog className="size-5" />
              登録済みサービス
            </CardTitle>
            <CardDescription>Worker、Encoder/Recorder、Discord Bot、Observabilityの報告バージョンです。</CardDescription>
          </CardHeader>
          <CardContent>
            {services.isLoading ? (
              <div className="rounded-md border border-dashed p-6 text-sm text-muted-foreground">読み込み中</div>
            ) : nodeRows.length === 0 ? (
              <div className="rounded-md border border-dashed p-6 text-sm text-muted-foreground">登録済みNodeがありません。</div>
            ) : (
              <>
                <div className="grid gap-3 md:hidden">
                  {nodeRows.map((node) => (
                    <ServiceInfoPanel key={node.service_id || node.id} node={node} timezone={timezone} latestVersion={appVersion.data} />
                  ))}
                </div>
                <div className="hidden overflow-x-auto rounded-md border md:block">
                  <Table>
                    <TableHeader>
                      <TableRow>
                        <TableHead>サービス</TableHead>
                        <TableHead>種別</TableHead>
                        <TableHead>バージョン</TableHead>
                        <TableHead>コミット</TableHead>
                        <TableHead>ビルド日時</TableHead>
                        <TableHead>状態</TableHead>
                        <TableHead>更新確認</TableHead>
                      </TableRow>
                    </TableHeader>
                    <TableBody>
                      {nodeRows.map((node) => (
                        <TableRow key={node.service_id || node.id}>
                          <TableCell>
                            <div className="font-medium">{node.service_name || node.service_id || "-"}</div>
                          </TableCell>
                          <TableCell>{serviceTypeLabel(node.service_type)}</TableCell>
                          <TableCell>{node.reported_version || node.version || "未報告"}</TableCell>
                          <TableCell>
                            <span className="inline-flex items-center gap-1 font-mono text-xs">
                              <GitCommit className="size-3.5 text-muted-foreground" />
                              {shortCommit(node.reported_commit)}
                            </span>
                          </TableCell>
                          <TableCell>{formatOptionalDate(node.reported_build_date, timezone)}</TableCell>
                          <TableCell>
                            <StatusBadge status={node.health_status || node.status || "-"} />
                          </TableCell>
                          <TableCell>
                            <UpdateStatusBadge state={nodeUpdateState(node, appVersion.data)} />
                          </TableCell>
                        </TableRow>
                      ))}
                    </TableBody>
                  </Table>
                </div>
                <p className="mt-3 text-xs text-muted-foreground">
                  Nodeの更新確認はControl Panelの更新確認ソースから取得した最新バージョンとの比較です。同じリリース系列で運用するNodeの目安として表示します。
                </p>
              </>
            )}
          </CardContent>
        </Card>
      </div>
    </div>
  );
}

function InfoItem({ label, value, monospace = false }: { label: string; value: ReactNode; monospace?: boolean }) {
  return (
    <div className="rounded-md border bg-muted/20 px-3 py-2">
      <div className="text-xs text-muted-foreground">{label}</div>
      <div className={monospace ? "font-mono text-sm" : "text-sm"}>{value}</div>
    </div>
  );
}

function ServiceInfoPanel({ node, timezone, latestVersion }: { node: WorkerNode; timezone?: string; latestVersion?: VersionInfo }) {
  return (
    <div className="rounded-md border bg-muted/20 p-3 text-sm">
      <div className="flex items-start justify-between gap-3">
        <div className="min-w-0">
          <div className="truncate font-medium">{node.service_name || node.service_id || "-"}</div>
          <div className="text-xs text-muted-foreground">{serviceTypeLabel(node.service_type)}</div>
        </div>
        <StatusBadge status={node.health_status || node.status || "-"} />
      </div>
      <div className="mt-3 grid gap-2">
        <ServiceInfoLine label="バージョン" value={node.reported_version || node.version || "未報告"} />
        <ServiceInfoLine label="コミット" value={shortCommit(node.reported_commit)} monospace />
        <ServiceInfoLine label="ビルド日時" value={formatOptionalDate(node.reported_build_date, timezone)} />
        <ServiceInfoLine label="更新確認" value={<UpdateStatusBadge state={nodeUpdateState(node, latestVersion)} />} />
      </div>
    </div>
  );
}

function ServiceInfoLine({ label, value, monospace = false }: { label: string; value: ReactNode; monospace?: boolean }) {
  return (
    <div className="grid grid-cols-[88px_minmax(0,1fr)] gap-2">
      <span className="text-muted-foreground">{label}</span>
      <span className={monospace ? "truncate font-mono text-xs" : "truncate"}>{value}</span>
    </div>
  );
}

function compareServiceRows(a: WorkerNode, b: WorkerNode) {
  const type = serviceTypeLabel(a.service_type).localeCompare(serviceTypeLabel(b.service_type), "ja");
  if (type !== 0) return type;
  return (a.service_name || a.service_id || "").localeCompare(b.service_name || b.service_id || "", "ja");
}

function shortCommit(value?: string) {
  const commit = value?.trim() || "";
  if (!commit || commit === "unknown") return "-";
  return commit.length > 12 ? commit.slice(0, 12) : commit;
}

function formatOptionalDate(value?: string, timezone?: string) {
  const raw = value?.trim() || "";
  if (!raw || raw === "unknown") return "-";
  if (Number.isNaN(Date.parse(raw))) return raw;
  return formatDateTimeInTimeZone(raw, timezone, { year: "numeric", month: "2-digit", day: "2-digit", hour: "2-digit", minute: "2-digit" });
}

type VersionInfo = {
  version?: string;
  latest_version?: string;
  update_available?: boolean;
  update_check_source?: string;
};

type UpdateState = {
  label: string;
  tone: "default" | "warning" | "muted" | "ok";
};

function controlPanelUpdateState(version?: VersionInfo): UpdateState {
  if (!version) return { label: "確認中", tone: "muted" };
  if (version.update_available && version.latest_version) return { label: `更新あり ${version.latest_version}`, tone: "warning" };
  if (version.update_check_source === "disabled") return { label: "更新確認なし", tone: "muted" };
  return { label: "更新なし", tone: "ok" };
}

function nodeUpdateState(node: WorkerNode, version?: VersionInfo): UpdateState {
  if (!(node.reported_version || node.version)) return { label: "未報告", tone: "muted" };
  const current = (node.reported_version || node.version || "").trim();
  const latest = version?.latest_version?.trim() || "";
  if (!latest) {
    return version?.update_check_source === "disabled" ? { label: "更新確認なし", tone: "muted" } : { label: "確認ソース未設定", tone: "muted" };
  }
  const comparison = compareVersions(current, latest);
  if (comparison < 0) {
    return { label: `更新候補 ${latest}`, tone: "warning" };
  }
  if (comparison > 0) {
    return { label: "報告バージョンが新しい", tone: "muted" };
  }
  return { label: "更新なし", tone: "ok" };
}

function compareVersions(current: string, latest: string) {
  const currentParts = semanticVersionParts(current);
  const latestParts = semanticVersionParts(latest);
  if (currentParts && latestParts) {
    for (let index = 0; index < Math.max(currentParts.length, latestParts.length); index += 1) {
      const left = currentParts[index] || 0;
      const right = latestParts[index] || 0;
      if (left !== right) return left < right ? -1 : 1;
    }
    return 0;
  }
  const normalizedCurrent = normalizeVersionText(current);
  const normalizedLatest = normalizeVersionText(latest);
  if (normalizedCurrent === normalizedLatest) return 0;
  return normalizedCurrent < normalizedLatest ? -1 : 1;
}

function semanticVersionParts(value: string) {
  const match = normalizeVersionText(value).match(/^(\d+)(?:\.(\d+))?(?:\.(\d+))?/);
  if (!match) return null;
  return match.slice(1).filter(Boolean).map((part) => Number.parseInt(part, 10));
}

function normalizeVersionText(value: string) {
  return value.trim().replace(/^v/i, "");
}

function UpdateStatusBadge({ state }: { state: UpdateState }) {
  const variant = state.tone === "warning" ? "destructive" : state.tone === "muted" ? "secondary" : "default";
  return <Badge variant={variant}>{state.label}</Badge>;
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
