"use client";
import { createContext, useContext } from "react";
import type { CellContext, ColumnDef } from "@tanstack/react-table";
import { AlertCircle, Check, Copy, Eye, Pencil, Play, RadioTower, RotateCw, SlidersHorizontal, Square, Shuffle, Trash2, Video } from "lucide-react";
import { Button } from "@/components/ui/button";
import { StreamActionControl } from "./stream-action-control";
import type { createStreamActionController } from "./stream-action-controller";
import { compactList, optionLabel, streamInputPresentation } from "./stream-view-options";
import { fixedPresentationText } from "@/lib/i18n/ui-v2/presentation-copy";
import type { UICopy } from "@/lib/i18n/ui-v2/copy";
import type { useI18n } from "@/components/admin/i18n-provider";
import { recordingDescriptor } from "@/lib/stream-presentation";
import { staticRelayRecoveryActionAvailable } from "@/lib/stream-static-relay";
import { formatDateTimeInTimeZone } from "@/lib/timezone";
import { cn } from "@/lib/utils";
import type { Stream } from "@/types/domain";

export type StreamTablePresentation = {
  renderStreamStatus: (stream: Stream) => React.ReactNode;
  lifecycle: Record<"streamStatusAllowsDelete" | "streamStatusAllowsEdit" | "streamStatusAllowsForceStop" | "streamStatusAllowsStart" | "streamStatusAllowsStop", (status: Stream["status"]) => boolean>;
  uiText: UICopy; t: ReturnType<typeof useI18n>["t"]; ja: boolean;
  copiedStreamID: string; copyStreamID: (id: string) => Promise<void>;
  onDetails: (stream: Stream, trigger: HTMLButtonElement) => void;
  onEdit: (stream: Stream, trigger: HTMLButtonElement) => void;
  canUpdate: boolean; actionController: ReturnType<typeof createStreamActionController>;
  handleStreamActionResult: React.ComponentProps<typeof StreamActionControl>["onResult"];
  staticRelayOutputIDs: ReadonlySet<string>;
  youtubeOutputLabels: Map<string, string>; archiveDestinationLabels: Map<string, string>;
  archiveProfileLabels: Map<string, string>; discordLabels: Map<string, string>; timezone?: string;
};
export const StreamTableContext = createContext<StreamTablePresentation | null>(null);
function useStreamTablePresentation() {
  const value = useContext(StreamTableContext);
  if (!value) throw new Error("Stream table requires its existing presentation owner");
  return value;
}
export const streamRowID = (stream: Stream) => stream.id;
export function streamTableColumns({ t, ja, uiText, discordLabels }: Pick<StreamTablePresentation, "t" | "ja" | "uiText" | "discordLabels">): ColumnDef<Stream>[] {
  return [
    {
      accessorKey: "name",
      header: t("name"),
      meta: { className: "min-w-56" },
      cell: StreamNameCell,
    },
    { accessorKey: "status", header: t("status"), meta: { className: "min-w-56 max-w-72" }, cell: StreamStatusCell },
    { id: "readiness", header: "Readiness", meta: { priority: 0, required: true, className: "min-w-40" }, cell: StreamReadinessCell },
    {
      id: "actions",
      header: t("actions"),
      meta: { className: "min-w-36" },
      cell: StreamActionsCell,
    },
    {
      id: "route",
      accessorFn: (stream) => compactList([stream.encoder_input_url, stream.input_source, stream.output_target, stream.youtube_output_id]).join(" "),
      header: ja ? "入力 / YouTube" : "Input / YouTube",
      meta: { priority: 1, className: "min-w-56" },
      cell: StreamRouteCell,
    },
    {
      id: "recording",
      accessorFn: (stream) => compactList([fixedPresentationText(recordingDescriptor(stream).label, uiText), stream.archive_file_name, stream.archive_masked_folder_id]).join(" "),
      header: ja ? "録画・保存" : "Recording / archive",
      meta: { priority: 1, className: "min-w-44" },
      cell: StreamRecordingCell,
    },
    {
      id: "discord",
      accessorFn: (stream) => compactList([optionLabel(discordLabels, stream.discord_config_id), stream.discord_config_id, stream.auto_start_trigger === "discord_voice_join" ? uiText("VC参加で自動開始") : uiText("手動開始")]).join(" "),
      header: ja ? "開始条件" : "Trigger",
      meta: { priority: 1, className: "min-w-40" },
      cell: StreamDiscordCell,
    },
    {
      id: "nodes",
      accessorFn: (stream) => compactList([stream.assigned_worker_id, stream.assigned_encoder_id]).join(" "),
      header: ja ? "担当Node" : "Assignments",
      meta: { priority: 1, className: "min-w-36" },
      cell: StreamNodesCell,
    },
    { id: "updated", accessorFn: (stream) => stream.updated_at || stream.created_at || "", header: ja ? "更新" : "Updated", meta: { priority: 1, className: "min-w-28" }, cell: StreamUpdatedCell },
  ];
}
export function StreamNameCell(props: CellContext<Stream, unknown>) {
  const { uiText, copiedStreamID, copyStreamID, onDetails } = useStreamTablePresentation();
  return (({ row }) => (
        <div className="min-w-52"><div className="flex items-center gap-2"><button data-slot="stream-primary-trigger" data-stream-id={row.original.id} type="button" className="min-h-11 text-left font-medium text-primary underline underline-offset-4" onClick={(event) => { onDetails(row.original, event.currentTarget); }}>{row.original.name}</button><Button variant="outline" size="icon-sm" aria-label={uiText("配信IDをコピー")} onClick={() => void copyStreamID(row.original.id)}>{copiedStreamID === row.original.id ? <Check className="size-4" /> : <Copy className="size-4" />}</Button></div></div>
      ))(props);
}

export function StreamStatusCell(props: CellContext<Stream, unknown>) {
  const { renderStreamStatus } = useStreamTablePresentation();
  return renderStreamStatus(props.row.original);
}

export function StreamReadinessCell(props: CellContext<Stream, unknown>) {
  const { ja, onDetails } = useStreamTablePresentation();
  return (({ row }) => <button type="button" className="text-left text-sm text-primary underline" onClick={(event) => { onDetails(row.original, event.currentTarget); }}>{ja ? "開始前に再確認" : "Review before starting"}</button>)(props);
}

export function StreamActionsCell(props: CellContext<Stream, unknown>) {
  const { uiText, t, onDetails, onEdit, canUpdate, actionController, handleStreamActionResult, staticRelayOutputIDs, lifecycle } = useStreamTablePresentation();
  const { streamStatusAllowsDelete, streamStatusAllowsEdit, streamStatusAllowsForceStop, streamStatusAllowsStart, streamStatusAllowsStop } = lifecycle;
  return (({ row }) => (
        <div className="flex min-w-0 flex-wrap gap-1">
          <Button variant="outline" size="icon-sm" aria-label={t("details")} onClick={(event) => { onDetails(row.original, event.currentTarget); }}><Eye /></Button>
          {String(row.original.status).toLowerCase() === "live" ? <Button variant="outline" size="icon-sm" aria-label={uiText("{0} ライブ調整", row.original.name)} title={uiText("ライブ調整")} onClick={(event) => { onEdit(row.original, event.currentTarget); }} disabled={!canUpdate}><SlidersHorizontal /></Button> : null}
          {streamStatusAllowsEdit(row.original.status) ? <Button variant="outline" size="icon-sm" aria-label={uiText("{0} を編集", row.original.name)} onClick={(event) => { onEdit(row.original, event.currentTarget); }} disabled={!canUpdate}><Pencil /></Button> : null}
          {streamStatusAllowsStart(row.original.status) ? <StreamActionControl controller={actionController} intent={{ id: "STR-04", stream: row.original }} label={uiText("{0} を開始", row.original.name)} buttonProps={{ variant: "outline", size: "icon-sm" }} onResult={handleStreamActionResult}><Play /></StreamActionControl> : null}
          {streamStatusAllowsStop(row.original.status) ? <StreamActionControl controller={actionController} intent={{ id: "STR-05", stream: row.original }} label={uiText("{0} を停止", row.original.name)} buttonProps={{ variant: "outline", size: "icon-sm" }} onResult={handleStreamActionResult}><Square /></StreamActionControl> : null}
          {streamStatusAllowsForceStop(row.original.status) ? <StreamActionControl controller={actionController} intent={{ id: "STR-06", stream: row.original }} label={uiText("{0} を強制停止", row.original.name)} buttonProps={{ variant: "destructive", size: "icon-sm" }} onResult={handleStreamActionResult}><AlertCircle /></StreamActionControl> : null}
          {staticRelayRecoveryActionAvailable(staticRelayOutputIDs.has(row.original.youtube_output_id || "") ? "live_api_relay_static" : "", row.original.status) ? <StreamActionControl controller={actionController} intent={{ id: "STR-07", stream: row.original, staticRelayRecoveryAvailable: true }} label={uiText("{0} の固定Relay回復を実行", row.original.name)} buttonProps={{ variant: "outline", size: "icon-sm" }} onResult={handleStreamActionResult}><RadioTower /></StreamActionControl> : null}
          <StreamActionControl controller={actionController} intent={{ id: "STR-08", stream: row.original }} label={uiText("{0} の開始準備を再確認", row.original.name)} buttonProps={{ variant: "outline", size: "icon-sm" }} onResult={handleStreamActionResult}><RotateCw /></StreamActionControl>
          <StreamActionControl controller={actionController} intent={{ id: "STR-09", stream: row.original }} label={uiText("{0} のWorkerテストを実行", row.original.name)} buttonProps={{ variant: "outline", size: "icon-sm" }} onResult={handleStreamActionResult}><Shuffle /></StreamActionControl>
          {streamStatusAllowsDelete(row.original.status) ? <StreamActionControl controller={actionController} intent={{ id: "STR-10", stream: row.original }} label={uiText("{0} を削除", row.original.name)} buttonProps={{ variant: "destructive", size: "icon-sm" }} onResult={handleStreamActionResult}><Trash2 /></StreamActionControl> : null}
        </div>
      ))(props);
}

export function StreamRouteCell(props: CellContext<Stream, unknown>) {
  const { uiText, youtubeOutputLabels } = useStreamTablePresentation();
  return (({ row }) => <div className="min-w-56 max-w-80 text-sm"><div className="flex items-center gap-1.5"><RadioTower className="size-3.5 shrink-0 text-muted-foreground" /><span className="truncate" title={streamInputPresentation(row.original, uiText)}>{streamInputPresentation(row.original, uiText)}</span></div><div className="mt-1 flex items-center gap-1.5 text-muted-foreground"><Video className="size-3.5 shrink-0" /><span className="truncate">{optionLabel(youtubeOutputLabels, row.original.youtube_output_id) || row.original.output_target || uiText("出力未設定")}</span></div></div>)(props);
}

export function StreamRecordingCell(props: CellContext<Stream, unknown>) {
  const { uiText, archiveDestinationLabels, archiveProfileLabels } = useStreamTablePresentation();
  return (({ row }) => {
        const recording = recordingDescriptor(row.original);
        return <div className="min-w-44 max-w-64 text-sm"><span className={cn("inline-flex rounded-md border px-2 py-0.5 text-xs font-medium", recording.className)}>{fixedPresentationText(recording.label, uiText)}</span><div className="mt-1 truncate text-muted-foreground" title={row.original.archive_file_name}>{row.original.archive_file_name || optionLabel(archiveDestinationLabels, row.original.archive_drive_destination_id) || optionLabel(archiveProfileLabels, row.original.archive_profile_id) || uiText("保存先未設定")}</div>{row.original.archive_folder_id_configured ? <div className="truncate text-xs text-muted-foreground">{uiText("フォルダー")}{row.original.archive_masked_folder_id || uiText("設定済み")}</div> : null}</div>;
      })(props);
}

export function StreamDiscordCell(props: CellContext<Stream, unknown>) {
  const { uiText, discordLabels } = useStreamTablePresentation();
  return (({ row }) => <div className="min-w-40 text-sm"><div>{row.original.auto_start_trigger === "discord_voice_join" ? uiText("VC参加で自動開始") : uiText("手動開始")}</div><div className="mt-1 truncate text-muted-foreground">{optionLabel(discordLabels, row.original.discord_config_id) || uiText("Discord未設定")}</div><div className="truncate text-xs text-muted-foreground">{uiText("配信先はv2 snapshotで管理")}</div></div>)(props);
}

export function StreamNodesCell(props: CellContext<Stream, unknown>) {
  const { uiText } = useStreamTablePresentation();
  return (({ row }) => <div className="min-w-36 text-sm text-muted-foreground"><div className="truncate">Worker {row.original.assigned_worker_id || uiText("未割当")}</div><div className="truncate">Encoder {row.original.assigned_encoder_id || uiText("未割当")}</div></div>)(props);
}

export function StreamUpdatedCell(props: CellContext<Stream, unknown>) {
  const { timezone } = useStreamTablePresentation();
  return (({ row }) => <span className="text-sm">{formatDateTime(row.original.updated_at || row.original.created_at, timezone)}</span>)(props);
}
function formatDateTime(value?: string, timezone?: string) {
  return formatDateTimeInTimeZone(value, timezone, { month: "2-digit", day: "2-digit", hour: "2-digit", minute: "2-digit" });
}
