"use client";
import { fixedPresentationText } from "@/lib/i18n/ui-v2/presentation-copy";
import { useUICopy } from "@/lib/i18n/ui-v2/use-ui-copy";


import Link from "next/link";
import { useI18n } from "@/components/admin/i18n-provider";
import { DomainStatusBadge } from "@/components/foundation/status/domain-status-badge";
import { DefinitionList } from "@/components/data-display/definition-list";
import { DetailSection, SectionNavigation } from "@/components/layout/detail-section";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { StreamControlPlatformPanel } from "@/features/streams/stream-control-platform-panel";
import { StreamPreview } from "@/features/streams/stream-preview";
import { StreamDetailOperations, type StreamOperationNotice } from "./stream-detail-operations";
import type { ComponentProps } from "react";
import type { StreamActionController } from "@/features/streams/stream-action-controller";
import { isPreviewableStreamStatus } from "@/features/streams/stream-lifecycle";
import { optionLabel, streamInputPresentation } from "@/features/streams/stream-view-options";
import { presentStreamLifecycleStatus } from "@/lib/foundation/status/lifecycle-presenters";
import { recordingDescriptor } from "@/lib/stream-presentation";
import type { Stream } from "@/types/domain";

type Props = {
  returnFocus: () => void;
  stream: Stream | null;
  actionController: StreamActionController;
  onActionResult: ComponentProps<typeof StreamDetailOperations>["onResult"];
  actionNotice: StreamOperationNotice | null;
  onOpenChange: (open: boolean) => void;
  discordLabels: Map<string, string>;
  youtubeOutputLabels: Map<string, string>;
  archiveAccountLabels: Map<string, string>;
  archiveDestinationLabels: Map<string, string>;
  archiveProfileLabels: Map<string, string>;
  overlayProfileLabels: Map<string, string>;
};

export function StreamDetailsDialog({ returnFocus, stream, actionController, onActionResult, actionNotice, onOpenChange, discordLabels, youtubeOutputLabels, archiveAccountLabels, archiveDestinationLabels, archiveProfileLabels, overlayProfileLabels }: Props) {
  const uiText = useUICopy();
  const { locale, t } = useI18n();
  const ja = locale === "ja";
  if (!stream) return null;
  const recording = recordingDescriptor(stream);
  const unset = ja ? "未設定" : "Not configured";
  const sections = [
    { id: "stream-overview", label: ja ? "概要・開始準備" : "Overview / Readiness" },
    { id: "stream-assignment", label: ja ? "担当" : "Assignments" },
    { id: "stream-output", label: ja ? "出力" : "Outputs" },
    { id: "stream-recording", label: ja ? "録画・Archive" : "Recording / Archive" },
    { id: "stream-runtime", label: ja ? "実行状態・操作" : "Runtime / actions" },
    ...(isPreviewableStreamStatus(stream.status) ? [{ id: "stream-preview", label: "Preview" }] : []),
    { id: "stream-events", label: ja ? "操作履歴" : "Events" },
  ];
  return (
    <Dialog open onOpenChange={onOpenChange}>
      <DialogContent onCloseAutoFocus={(event) => { event.preventDefault(); returnFocus(); }} className="max-h-[90dvh] overflow-y-auto sm:max-w-5xl" data-screen-family="stream-detail">
        <DialogHeader>
          <DialogTitle className="text-xl [overflow-wrap:anywhere]">{stream.name}</DialogTitle>
          <DialogDescription>{ja ? "開始準備、担当、出力、現在のRunを順に確認します。" : "Review readiness, assignments, outputs and the current run."}</DialogDescription>
        </DialogHeader>
        <div className="flex flex-wrap items-center justify-between gap-3">
          <DomainStatusBadge presentation={presentStreamLifecycleStatus(stream.status)} translate={t} showDetail />
          {stream.updated_at ? <time className="text-xs text-muted-foreground" dateTime={stream.updated_at}>{stream.updated_at}</time> : null}
        </div>
        <StreamDetailOperations stream={stream} controller={actionController} onResult={onActionResult} notice={actionNotice} />
        <SectionNavigation label={ja ? "配信詳細のセクション" : "Stream detail sections"} items={sections} />
        <DetailSection id="stream-overview" title={sections[0].label}
          description={ja ? "開始操作の前に「開始準備を再確認」で最新Readinessを確認してください。作成成功は開始可能を意味しません。" : "Use the readiness check before starting. Successful creation does not guarantee readiness."}>
          <DefinitionList items={[
            { label: ja ? "開始条件" : "Trigger", value: stream.auto_start_trigger === "discord_voice_join" ? (ja ? "Discord VC参加" : "Discord voice participation") : (ja ? "手動" : "Manual") },
            { label: "Discord BOT", value: optionLabel(discordLabels, stream.discord_config_id) || unset },
            { label: ja ? "配信ID" : "Stream ID", value: <code>{stream.id}</code> },
          ]} />
        </DetailSection>
        <div className="grid min-w-0 gap-4 lg:grid-cols-2">
          <DetailSection id="stream-assignment" title={sections[1].label}>
            <DefinitionList items={[
              { label: "Worker", value: stream.assigned_worker_id || (ja ? "未割当" : "Unassigned") },
              { label: "Encoder", value: stream.assigned_encoder_id || (ja ? "未割当" : "Unassigned") },
              { label: ja ? "Encoder音量" : "Encoder gain", value: `${stream.encoder_audio_gain_db ?? 0} dB` },
              { label: "Watermark", value: optionLabel(overlayProfileLabels, stream.overlay_profile_id) || "OFF" },
            ]} />
          </DetailSection>
          <DetailSection id="stream-output" title={sections[2].label}>
            <DefinitionList items={[
              { label: ja ? "入力" : "Input", value: streamInputPresentation(stream, uiText) },
              { label: "YouTube", value: optionLabel(youtubeOutputLabels, stream.youtube_output_id) || stream.output_target || unset },
            ]} />
          </DetailSection>
        </div>
        <DetailSection id="stream-recording" title={sections[3].label} description={fixedPresentationText(recording.detail, uiText)}>
          <DefinitionList items={[
            { label: ja ? "録画状態" : "Recording state", value: fixedPresentationText(recording.label, uiText) },
            { label: ja ? "プロファイル" : "Profile", value: optionLabel(archiveProfileLabels, stream.archive_profile_id) || unset },
            { label: ja ? "保存先" : "Destination", value: optionLabel(archiveDestinationLabels, stream.archive_drive_destination_id) || optionLabel(archiveAccountLabels, stream.archive_oauth_account_id) || unset },
            { label: ja ? "ファイル名" : "File name", value: stream.archive_file_name || (ja ? "自動命名" : "Automatic") },
            { label: ja ? "フォルダー" : "Folder", value: stream.archive_folder_id_configured ? stream.archive_masked_folder_id || (ja ? "設定済み" : "Configured") : unset },
            { label: "Run ID", value: stream.archive_run_id || (ja ? "未報告" : "Not reported") },
          ]} />
        </DetailSection>
        <DetailSection id="stream-runtime" title={sections[4].label}><StreamControlPlatformPanel stream={stream} /></DetailSection>
        {isPreviewableStreamStatus(stream.status) ? <DetailSection id="stream-preview" title="Preview"><StreamPreview stream={stream} controller={actionController} /></DetailSection> : null}
        <DetailSection id="stream-events" title={ja ? "操作履歴" : "Events"}>
          <Button asChild variant="outline" size="sm"><Link href={`/admin/audit-logs/?q=${encodeURIComponent(stream.id)}`}>{ja ? "この配信枠の操作履歴を確認" : "View stream audit history"}</Link></Button>
        </DetailSection>
      </DialogContent>
    </Dialog>
  );
}
