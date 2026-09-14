"use client";

import Link from "next/link";
import type { ColumnDef } from "@tanstack/react-table";
import { useI18n } from "@/components/admin/i18n-provider";
import { DefinitionList } from "@/components/data-display/definition-list";
import { DomainStatusBadge } from "@/components/foundation/status/domain-status-badge";
import { DataTable } from "@/components/tables/data-table";
import { presentStreamLifecycleStatus } from "@/lib/foundation/status/lifecycle-presenters";
import { hasRecordingConfiguration } from "@/lib/stream-presentation";
import type { Stream } from "@/types/domain";

export function dashboardStreamGroups(rows: readonly Stream[]) {
  return {
    active: rows.filter((row) => ["live", "starting", "stopping"].includes(String(row.status).toLowerCase())),
    waiting: rows.filter((row) => ["draft", "created", "ready", "scheduled"].includes(String(row.status).toLowerCase())),
    issues: rows.filter((row) => ["failed", "error"].includes(String(row.status).toLowerCase())),
  };
}

export function DashboardStreams({ rows, serviceNames }: { rows: Stream[]; serviceNames: ReadonlyMap<string, string> }) {
  const { locale, t } = useI18n();
  const ja = locale === "ja";
  const unassigned = ja ? "未割当" : "Unassigned";
  const columns: ColumnDef<Stream>[] = [
    { accessorKey: "name", header: t("name"), cell: ({ row }) => <Link href="/admin/streams/" className="font-medium text-primary underline underline-offset-4">{row.original.name}</Link> },
    { accessorKey: "status", header: t("status"), cell: ({ row }) => <DomainStatusBadge presentation={presentStreamLifecycleStatus(row.original.status)} translate={t} /> },
    { id: "readiness", header: "Readiness", cell: () => <Link className="text-primary underline underline-offset-4" href="/admin/streams/">{ja ? "開始前に詳細を確認" : "Review before starting"}</Link> },
    { id: "trigger", header: ja ? "開始条件" : "Trigger", cell: ({ row }) => row.original.auto_start_trigger === "discord_voice_join" ? (ja ? "Discord VC参加" : "Discord voice join") : (ja ? "手動" : "Manual") },
    { id: "assignment", header: ja ? "割当" : "Assignments", cell: ({ row }) => <DefinitionList className="sm:grid-cols-1" items={[
      { label: "Worker", value: serviceNames.get(row.original.assigned_worker_id || "") || row.original.assigned_worker_id || unassigned },
      { label: "Encoder", value: serviceNames.get(row.original.assigned_encoder_id || "") || row.original.assigned_encoder_id || unassigned },
    ]} /> },
    { id: "outputs", header: ja ? "YouTube・出力" : "YouTube / output", cell: ({ row }) => <div>
      <p>{row.original.youtube_output_id ? (ja ? "YouTube設定あり" : "YouTube configured") : row.original.output_target || (ja ? "出力未設定" : "Output not configured")}</p>
      <span className="text-xs text-muted-foreground">{ja ? "実出力状態は詳細で確認" : "Inspect actual output state in details"}</span>
    </div> },
    { id: "recording", header: ja ? "録画・Archive" : "Recording / archive", cell: ({ row }) => <div>
      <p>{hasRecordingConfiguration(row.original) ? (ja ? "録画設定あり" : "Recording configured") : (ja ? "録画しない" : "No recording")}</p>
      {row.original.archive_run_id ? <p className="text-xs text-muted-foreground">Run: {row.original.archive_run_id}</p> : null}
    </div> },
    { id: "updated", header: t("updatedAt"), cell: ({ row }) => {
      const timestamp = row.original.updated_at || row.original.created_at;
      return timestamp ? <time dateTime={timestamp} className="text-xs">{timestamp}</time> : <span>{ja ? "未取得" : "Unavailable"}</span>;
    } },
  ];
  return <DataTable columns={columns} data={rows} getRowId={(row) => row.id} minTableWidthClass="min-w-[1040px]" density="compact" />;
}
