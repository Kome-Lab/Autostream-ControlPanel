"use client";

import { AlertTriangle, ClipboardCheck, Network, ShieldAlert } from "lucide-react";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Skeleton } from "@/components/ui/skeleton";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { MetricCard } from "@/components/admin/metric-card";
import { StatusBadge } from "@/components/admin/status-badge";
import { useResourceData, useServiceHealth } from "@/features/queries";
import type { WorkerNode } from "@/types/domain";

type MonitoringRow = Record<string, unknown>;

export function MonitoringView() {
  const services = useServiceHealth();
  const incidents = useResourceData<MonitoringRow[]>("/observability/incidents");
  const diagnostics = useResourceData<MonitoringRow[]>("/observability/diagnostics");

  const serviceRows = services.data || [];
  const incidentRows = incidents.data || [];
  const diagnosticRows = diagnostics.data || [];
  const online = serviceRows.filter((service) => service.status === "online").length;
  const unhealthy = serviceRows.filter((service) => ["offline", "warning", "unconfigured"].includes(service.health_status || service.status)).length;
  const openIncidents = incidentRows.filter((row) => !["resolved", "closed"].includes(rowString(row, "status"))).length;
  const warningDiagnostics = diagnosticRows.filter((row) => !["pass", "ok", "success"].includes(rowString(row, "status"))).length;

  if (services.isLoading && incidents.isLoading && diagnostics.isLoading) {
    return <Skeleton className="h-[520px] w-full" />;
  }

  return (
    <div className="space-y-6">
      <section className="grid gap-4 md:grid-cols-2 xl:grid-cols-4">
        <MetricCard title="オンラインNode" value={`${online}/${serviceRows.length}`} detail="Control Panelに接続中" tone={serviceRows.length > 0 && online === serviceRows.length ? "ok" : "warning"} />
        <MetricCard title="Node要確認" value={unhealthy} detail="heartbeatまたは登録状態" tone={unhealthy > 0 ? "warning" : "ok"} />
        <MetricCard title="未解決インシデント" value={openIncidents} detail="対応または確認が必要" tone={openIncidents > 0 ? "danger" : "ok"} />
        <MetricCard title="診断警告" value={warningDiagnostics} detail="直近の疎通・配信前確認" tone={warningDiagnostics > 0 ? "warning" : "ok"} />
      </section>

      <section className="grid gap-4 xl:grid-cols-[1.1fr_0.9fr]">
        <ServiceHealthPanel services={serviceRows} loading={services.isLoading} />
        <IncidentPanel incidents={incidentRows} loading={incidents.isLoading} />
      </section>

      <section className="grid gap-4 xl:grid-cols-[1fr_1fr]">
        <DiagnosticsPanel diagnostics={diagnosticRows} loading={diagnostics.isLoading} />
        <OperationalFocus services={serviceRows} incidents={incidentRows} diagnostics={diagnosticRows} />
      </section>
    </div>
  );
}

function ServiceHealthPanel({ services, loading }: { services: WorkerNode[]; loading: boolean }) {
  return (
    <Card>
      <CardHeader className="pb-2">
        <CardTitle className="flex items-center gap-2 text-base">
          <Network className="size-4" />
          Node監視
        </CardTitle>
      </CardHeader>
      <CardContent>
        {loading ? (
          <Skeleton className="h-44 w-full" />
        ) : services.length === 0 ? (
          <EmptyState message="登録済みNodeがありません。" />
        ) : (
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Node</TableHead>
                <TableHead>状態</TableHead>
                <TableHead>Heartbeat</TableHead>
                <TableHead>配信</TableHead>
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
                  <TableCell className="text-muted-foreground">{formatHeartbeat(service.heartbeat_age_sec)}</TableCell>
                  <TableCell className="text-muted-foreground">{service.current_stream_id || "-"}</TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        )}
      </CardContent>
    </Card>
  );
}

function IncidentPanel({ incidents, loading }: { incidents: MonitoringRow[]; loading: boolean }) {
  return (
    <Card>
      <CardHeader className="pb-2">
        <CardTitle className="flex items-center gap-2 text-base">
          <ShieldAlert className="size-4" />
          インシデント
        </CardTitle>
      </CardHeader>
      <CardContent className="space-y-3">
        {loading ? <Skeleton className="h-36 w-full" /> : null}
        {!loading && incidents.length === 0 ? <EmptyState message="現在検知されている問題はありません。" /> : null}
        {incidents.slice(0, 6).map((row, index) => (
          <div key={rowString(row, "id") || index} className="rounded-md border p-3">
            <div className="flex flex-wrap items-center justify-between gap-2">
              <div className="font-medium">{rowString(row, "title") || rowString(row, "rule") || "インシデント"}</div>
              <StatusBadge status={rowString(row, "status") || rowString(row, "severity")} />
            </div>
            <div className="mt-1 text-sm text-muted-foreground">
              {rowString(row, "service_id") || "-"} / {formatTimestamp(rowString(row, "updated_at") || rowString(row, "created_at"))}
            </div>
          </div>
        ))}
      </CardContent>
    </Card>
  );
}

function DiagnosticsPanel({ diagnostics, loading }: { diagnostics: MonitoringRow[]; loading: boolean }) {
  return (
    <Card>
      <CardHeader className="pb-2">
        <CardTitle className="flex items-center gap-2 text-base">
          <ClipboardCheck className="size-4" />
          診断結果
        </CardTitle>
      </CardHeader>
      <CardContent className="space-y-3">
        {loading ? <Skeleton className="h-36 w-full" /> : null}
        {!loading && diagnostics.length === 0 ? <EmptyState message="診断結果はまだありません。" /> : null}
        {diagnostics.slice(0, 6).map((row, index) => (
          <div key={rowString(row, "id") || index} className="grid gap-3 rounded-md border p-3 sm:grid-cols-[minmax(0,1fr)_128px] sm:items-center">
            <div>
              <div className="font-medium">{diagnosticLabel(rowString(row, "check") || rowString(row, "rule"))}</div>
              <div className="text-sm text-muted-foreground">{rowString(row, "target") || rowString(row, "service_id") || "-"}</div>
            </div>
            <StatusBadge status={rowString(row, "status")} showDetail />
          </div>
        ))}
      </CardContent>
    </Card>
  );
}

function OperationalFocus({ services, incidents, diagnostics }: { services: WorkerNode[]; incidents: MonitoringRow[]; diagnostics: MonitoringRow[] }) {
  const offlineServices = services.filter((service) => ["offline", "warning", "unconfigured"].includes(service.health_status || service.status));
  const openIncidents = incidents.filter((row) => !["resolved", "closed"].includes(rowString(row, "status")));
  const failedDiagnostics = diagnostics.filter((row) => !["pass", "ok", "success"].includes(rowString(row, "status")));
  const hasAttention = offlineServices.length > 0 || openIncidents.length > 0 || failedDiagnostics.length > 0;

  return (
    <Card>
      <CardHeader className="pb-2">
        <CardTitle className="flex items-center gap-2 text-base">
          <AlertTriangle className="size-4" />
          確認対象
        </CardTitle>
      </CardHeader>
      <CardContent className="space-y-3">
        {!hasAttention ? <EmptyState message="優先対応が必要な項目はありません。" /> : null}
        {offlineServices.slice(0, 4).map((service) => (
          <AttentionRow key={service.id || service.service_id || service.service_name} title={service.service_name || service.service_id || "-"} detail={`${serviceTypeLabel(service.service_type)} / ${formatHeartbeat(service.heartbeat_age_sec)}`} status={service.health_status || service.status || "-"} />
        ))}
        {openIncidents.slice(0, 4).map((row, index) => (
          <AttentionRow key={rowString(row, "id") || `incident-${index}`} title={rowString(row, "title") || "インシデント"} detail={rowString(row, "service_id") || "-"} status={rowString(row, "status") || rowString(row, "severity")} />
        ))}
        {failedDiagnostics.slice(0, 4).map((row, index) => (
          <AttentionRow key={rowString(row, "id") || `diagnostic-${index}`} title={diagnosticLabel(rowString(row, "check"))} detail={rowString(row, "target") || "-"} status={rowString(row, "status")} />
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

function rowString(row: MonitoringRow, key: string) {
  const value = row[key];
  return typeof value === "string" ? value : "";
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

function diagnosticLabel(value: string) {
  const labels: Record<string, string> = {
    audio_status: "音声状態",
    encoder_preflight: "Encoder事前診断",
    worker_events: "映像生成イベント",
    google_drive: "Google Drive接続",
  };
  return labels[value] || value.replace(/[._]/g, " ") || "診断";
}

function formatHeartbeat(value?: number) {
  if (typeof value !== "number") return "-";
  if (value < 60) return `${value}秒前`;
  if (value < 3600) return `${Math.round(value / 60)}分前`;
  return `${Math.round(value / 3600)}時間前`;
}

function formatTimestamp(value: string) {
  const time = Date.parse(value);
  if (Number.isNaN(time)) return "-";
  return new Intl.DateTimeFormat("ja-JP", { month: "2-digit", day: "2-digit", hour: "2-digit", minute: "2-digit" }).format(time);
}
