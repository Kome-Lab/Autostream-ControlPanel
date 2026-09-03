import type { ReactNode } from "react";
import Link from "next/link";

import { StatusBadge } from "@/components/admin/status-badge";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { StreamControlPlatformPanel } from "@/features/streams/stream-control-platform-panel";
import { StreamPreview } from "@/features/streams/stream-preview";
import type { StreamActionController } from "@/features/streams/stream-action-controller";
import { isPreviewableStreamStatus } from "@/features/streams/stream-lifecycle";
import { optionLabel, streamInputPresentation } from "@/features/streams/stream-view-options";
import { recordingDescriptor } from "@/lib/stream-presentation";
import { cn } from "@/lib/utils";
import type { Stream } from "@/types/domain";

type Props = {
  stream: Stream | null;
  actionController: StreamActionController;
  onOpenChange: (open: boolean) => void;
  discordLabels: Map<string, string>;
  youtubeOutputLabels: Map<string, string>;
  archiveAccountLabels: Map<string, string>;
  archiveDestinationLabels: Map<string, string>;
  archiveProfileLabels: Map<string, string>;
  overlayProfileLabels: Map<string, string>;
};

export function StreamDetailsDialog({ stream, actionController, onOpenChange, discordLabels, youtubeOutputLabels, archiveAccountLabels, archiveDestinationLabels, archiveProfileLabels, overlayProfileLabels }: Props) {
  if (!stream) return null;
  const recording = recordingDescriptor(stream);
  return (
    <Dialog open onOpenChange={onOpenChange}>
      <DialogContent className="max-h-[88vh] overflow-y-auto sm:max-w-5xl">
        <DialogHeader><DialogTitle>{stream.name}</DialogTitle><DialogDescription>配信前の確認と、配信中・終了後の状況確認に使う情報です。</DialogDescription></DialogHeader>
        <div className="grid gap-3 sm:grid-cols-2">
          <DetailGroup title="状態"><div className="flex flex-wrap items-center gap-2"><StatusBadge status={stream.status} /><span className={cn("inline-flex rounded-md border px-2 py-1 text-xs font-medium", recording.className)}>{recording.label}</span></div><p className="mt-2 text-xs text-muted-foreground">{recording.detail}</p></DetailGroup>
          <DetailGroup title="配信経路"><DetailLine label="入力URL" value={streamInputPresentation(stream)} mono /><DetailLine label="YouTube出力" value={optionLabel(youtubeOutputLabels, stream.youtube_output_id) || stream.output_target || "未設定"} /></DetailGroup>
          <DetailGroup title="録画保存"><DetailLine label="設定" value={optionLabel(archiveProfileLabels, stream.archive_profile_id) || "未設定"} /><DetailLine label="保存先" value={optionLabel(archiveDestinationLabels, stream.archive_drive_destination_id) || optionLabel(archiveAccountLabels, stream.archive_oauth_account_id) || "未設定"} /><DetailLine label="ファイル名" value={stream.archive_file_name || "自動命名"} /><DetailLine label="フォルダー" value={stream.archive_folder_id_configured ? stream.archive_masked_folder_id || "設定済み" : "未設定"} /></DetailGroup>
          <DetailGroup title="自動開始"><DetailLine label="方式" value={stream.auto_start_trigger === "discord_voice_join" ? "Discord VC参加で自動開始" : "手動開始"} /><DetailLine label="BOT" value={optionLabel(discordLabels, stream.discord_config_id) || "未設定"} /><DetailLine label="VC" value={stream.discord_voice_channel_id || "未設定"} /><DetailLine label="Chat" value={stream.discord_text_channel_id || "未設定"} /></DetailGroup>
          <DetailGroup title="担当Node・映像設定"><DetailLine label="Worker" value={stream.assigned_worker_id || "未割当"} /><DetailLine label="Encoder" value={stream.assigned_encoder_id || "未割当"} /><DetailLine label="Encoder音量" value={`${stream.encoder_audio_gain_db ?? 0} dB`} /><DetailLine label="Watermark" value={optionLabel(overlayProfileLabels, stream.overlay_profile_id) || "OFF"} /></DetailGroup>
        </div>
        <StreamControlPlatformPanel stream={stream} />
        {isPreviewableStreamStatus(stream.status) ? <StreamPreview stream={stream} controller={actionController} /> : null}
        <div className="flex justify-end"><Button asChild variant="outline" size="sm"><Link href={`/admin/audit-logs/?q=${encodeURIComponent(stream.id)}`}>この配信枠の操作履歴を確認</Link></Button></div>
      </DialogContent>
    </Dialog>
  );
}

function DetailGroup({ title, children }: { title: string; children: ReactNode }) { return <section className="rounded-lg border bg-muted/15 p-4"><h3 className="mb-3 text-sm font-semibold">{title}</h3>{children}</section>; }
function DetailLine({ label, value, mono = false }: { label: string; value: string; mono?: boolean }) { return <div className="grid grid-cols-[6rem_minmax(0,1fr)] gap-2 border-b py-2 text-sm last:border-b-0"><span className="text-muted-foreground">{label}</span><span className={cn("min-w-0 break-all", mono && "font-mono text-xs")}>{value}</span></div>; }
