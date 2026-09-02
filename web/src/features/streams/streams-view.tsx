"use client";

import { type ReactNode, useCallback, useEffect, useMemo, useRef, useState } from "react";
import Link from "next/link";
import { useQueryClient } from "@tanstack/react-query";
import type { ColumnDef } from "@tanstack/react-table";
import { AlertCircle, Check, Copy, Eye, Pencil, Play, Plus, RadioTower, RotateCw, SlidersHorizontal, Square, Shuffle, Trash2, Video } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Checkbox } from "@/components/ui/checkbox";
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Sheet, SheetContent, SheetDescription, SheetHeader, SheetTitle } from "@/components/ui/sheet";
import { DataTable } from "@/components/tables/data-table";
import { StatusBadge } from "@/components/admin/status-badge";
import { hasPermission } from "@/lib/auth/permissions";
import {
  oauthAccountDisplayName as oauthAccountLabel,
  oauthAccountPurposeLabel,
  oauthAccountSupportsPurpose,
  oauthProviderTypeLabel as providerTypeLabel,
  type OAuthAccountPurpose,
} from "@/lib/oauth-account";
import { useAppSettings, useCurrentUser, useResourceData, useServiceHealth, useStreams } from "@/features/queries";
import { useI18n } from "@/components/admin/i18n-provider";
import { recordingDescriptor, safeDisplayURL } from "@/lib/stream-presentation";
import { buildStreamCreatePayload, buildStreamSettingsPayload, streamScheduleInputValue, streamScheduleRFC3339, streamServiceAssignmentOption } from "@/lib/stream-create";
import { staticRelayRecoveryActionAvailable } from "@/lib/stream-static-relay";
import { formatDateTimeInTimeZone } from "@/lib/timezone";
import { cn } from "@/lib/utils";
import { StreamPreview } from "@/features/streams/stream-preview";
import { StreamControlPlatformPanel } from "@/features/streams/stream-control-platform-panel";
import { StreamActionControl, type StreamActionControlHandle } from "@/features/streams/stream-action-control";
import { createStreamActionController, type StreamActionExecutionResult } from "@/features/streams/stream-action-controller";
import { mutateStreamAction, streamActionStateSnapshot, streamPermissionSnapshot } from "@/features/streams/stream-action-runtime";
import type { StreamActionIntent } from "@/features/streams/stream-action-descriptors";
import type { Stream } from "@/types/domain";

type ResourceRow = Record<string, unknown>;
type SelectOption = { value: string; label: string; description?: string; disabled?: boolean };
const noneValue = "__none__";

export function StreamsView() {
  const { t } = useI18n();
  const streams = useStreams();
  const currentUser = useCurrentUser();
  const appSettings = useAppSettings();
  const timezone = appSettings.data?.timezone;
  const queryClient = useQueryClient();
  const [createdStreams, setCreatedStreams] = useState<Stream[]>([]);
  const [copiedStreamID, setCopiedStreamID] = useState("");
  const [createOpen, setCreateOpen] = useState(false);
  const [editingStream, setEditingStream] = useState<Stream | null>(null);
  const [selectedStream, setSelectedStream] = useState<Stream | null>(null);
  const [actionNotice, setActionNotice] = useState<{ tone: "success" | "error"; message: string } | null>(null);

  useEffect(() => {
    const syncFromHash = () => setCreateOpen(window.location.hash === "#create-stream");
    syncFromHash();
    window.addEventListener("hashchange", syncFromHash);
    return () => window.removeEventListener("hashchange", syncFromHash);
  }, []);

  const actionController = useMemo(() => createStreamActionController({
    getPermissions: () => streamPermissionSnapshot(queryClient),
    getState: (intent) => streamActionStateSnapshot(queryClient, intent),
    mutate: mutateStreamAction,
  }), [queryClient]);
  const can = (permission: string) => hasPermission(currentUser.data, permission);
  const canCreate = can("streams.create");
  const canUpdate = can("streams.update");
  const handleStreamActionResult = useCallback((result: StreamActionExecutionResult, intent: StreamActionIntent) => {
    if (result.kind === "succeeded") {
      if (intent.id === "STR-10" && intent.stream) {
        setCreatedStreams((current) => current.filter((stream) => stream.id !== intent.stream?.id));
      }
      void queryClient.invalidateQueries({ queryKey: ["streams"] });
      setActionNotice({ tone: "success", message: `${intent.stream?.name || intent.publicLabel || "配信枠"}の${streamActionLabel(intent.id)}を受け付けました。最新状態を確認してください。` });
      return;
    }
    if (result.kind === "outcome_unknown") {
      setActionNotice({ tone: "error", message: "操作結果を確認できません。再送せず、配信枠の最新状態または監査ログを確認してください。" });
    } else if (result.kind === "failed") {
      setActionNotice({ tone: "error", message: `${t(result.error.messageKey)} 再送せず、最新状態を確認してください。` });
    } else {
      setActionNotice({ tone: "error", message: streamActionBlockedMessage(result.reason) });
    }
    // Reconciliation fetches are safe and do not resend the mutation. The
    // controller latch remains closed until an explicit new page lifecycle.
    void queryClient.invalidateQueries({ queryKey: ["auth", "me"] });
    void queryClient.invalidateQueries({ queryKey: ["streams"] });
  }, [queryClient, t]);
  const streamRows = useMemo(
    () => [...createdStreams, ...(streams.data || []).filter((stream) => !createdStreams.some((created) => created.id === stream.id))],
    [createdStreams, streams.data],
  );
  const currentSelectedStream = selectedStream ? streamRows.find((stream) => stream.id === selectedStream.id) || selectedStream : null;
  const discordLabels = useOptionLabelMap(useResourceOptions("/discord/configs", ["name", "service_id", "id"]));
  const youtubeOutputLabels = useOptionLabelMap(useResourceOptions("/youtube/outputs", ["name", "id"]));
  const youtubeOutputs = useResourceData<unknown>("/youtube/outputs");
  const staticRelayOutputIDs = useMemo(
    () => new Set(normalizeRows(youtubeOutputs.data).filter((row) => rowString(row, ["mode"]) === "live_api_relay_static").map((row) => rowString(row, ["id"])).filter(Boolean)),
    [youtubeOutputs.data],
  );
  const archiveAccountLabels = useOptionLabelMap(useOAuthAccountOptions("drive"));
  const archiveDestinationLabels = useOptionLabelMap(useResourceOptions("/archive/destinations", ["name", "id"]));
  const archiveProfileLabels = useOptionLabelMap(useResourceOptions("/profiles/archive", ["name", "id"]));
  const overlayProfileLabels = useOptionLabelMap(useResourceOptions("/profiles/overlay", ["name", "id"]));
  const copyStreamID = async (id: string) => {
    if (!id || typeof navigator === "undefined" || !navigator.clipboard) return;
    await navigator.clipboard.writeText(id);
    setCopiedStreamID(id);
    window.setTimeout(() => setCopiedStreamID((current) => (current === id ? "" : current)), 1200);
  };

  const columns: ColumnDef<Stream>[] = [
    {
      accessorKey: "name",
      header: t("name"),
      cell: ({ row }) => (
        <div className="min-w-52">
          <div className="flex items-center gap-2">
            <div className="font-medium">{row.original.name}</div>
            <Button variant="outline" size="icon-sm" aria-label="配信IDをコピー" onClick={() => void copyStreamID(row.original.id)}>
              {copiedStreamID === row.original.id ? <Check className="size-4" /> : <Copy className="size-4" />}
            </Button>
          </div>
        </div>
      ),
    },
    {
      accessorKey: "status",
      header: t("status"),
      cell: ({ row }) => <StatusBadge status={row.original.status} showDetail />,
    },
    {
      id: "actions",
      header: t("actions"),
      cell: ({ row }) => (
        <div className="flex min-w-44 flex-nowrap gap-1">
          <Button variant="outline" size="icon-sm" aria-label={t("details")} onClick={() => setSelectedStream(row.original)}>
            <Eye />
          </Button>
          {String(row.original.status).toLowerCase() === "live" ? (
            <Button variant="outline" size="icon-sm" aria-label={`${row.original.name} ライブ調整`} title="ライブ調整" onClick={() => setEditingStream(row.original)} disabled={!canUpdate}>
              <SlidersHorizontal />
            </Button>
          ) : null}
          {streamStatusAllowsEdit(row.original.status) ? (
            <Button variant="outline" size="icon-sm" aria-label={`${row.original.name} を編集`} onClick={() => setEditingStream(row.original)} disabled={!canUpdate}>
              <Pencil />
            </Button>
          ) : null}
          {streamStatusAllowsStart(row.original.status) ? (
            <StreamActionControl
              controller={actionController}
              intent={{ id: "STR-04", stream: row.original }}
              label={`${row.original.name} を開始`}
              buttonProps={{ variant: "outline", size: "icon-sm" }}
              onResult={handleStreamActionResult}
            ><Play /></StreamActionControl>
          ) : null}
          {streamStatusAllowsStop(row.original.status) ? (
            <StreamActionControl
              controller={actionController}
              intent={{ id: "STR-05", stream: row.original }}
              label={`${row.original.name} を停止`}
              buttonProps={{ variant: "outline", size: "icon-sm" }}
              onResult={handleStreamActionResult}
            ><Square /></StreamActionControl>
          ) : null}
          {streamStatusAllowsForceStop(row.original.status) ? (
            <StreamActionControl
              controller={actionController}
              intent={{ id: "STR-06", stream: row.original }}
              label={`${row.original.name} を強制停止`}
              buttonProps={{ variant: "destructive", size: "icon-sm" }}
              onResult={handleStreamActionResult}
            ><AlertCircle /></StreamActionControl>
          ) : null}
          {staticRelayRecoveryActionAvailable(
            staticRelayOutputIDs.has(row.original.youtube_output_id || "") ? "live_api_relay_static" : "",
            row.original.status,
          ) ? (
            <StreamActionControl
              controller={actionController}
              intent={{ id: "STR-07", stream: row.original, staticRelayRecoveryAvailable: true }}
              label={`${row.original.name} の固定Relay回復を実行`}
              buttonProps={{ variant: "outline", size: "icon-sm" }}
              onResult={handleStreamActionResult}
            ><RadioTower /></StreamActionControl>
          ) : null}
          <StreamActionControl
            controller={actionController}
            intent={{ id: "STR-08", stream: row.original }}
            label={`${row.original.name} の開始準備を再確認`}
            buttonProps={{ variant: "outline", size: "icon-sm" }}
            onResult={handleStreamActionResult}
          ><RotateCw /></StreamActionControl>
          <StreamActionControl
            controller={actionController}
            intent={{ id: "STR-09", stream: row.original }}
            label={`${row.original.name} のWorkerテストを実行`}
            buttonProps={{ variant: "outline", size: "icon-sm" }}
            onResult={handleStreamActionResult}
          ><Shuffle /></StreamActionControl>
          {streamStatusAllowsDelete(row.original.status) ? (
            <StreamActionControl
              controller={actionController}
              intent={{ id: "STR-10", stream: row.original }}
              label={`${row.original.name} を削除`}
              buttonProps={{ variant: "destructive", size: "icon-sm" }}
              onResult={handleStreamActionResult}
            ><Trash2 /></StreamActionControl>
          ) : null}
        </div>
      ),
    },

    {
      id: "route",
      accessorFn: (stream) => compactList([stream.encoder_input_url, stream.input_source, stream.output_target, stream.youtube_output_id]).join(" "),
      header: "配信経路",
      cell: ({ row }) => (
        <div className="min-w-56 max-w-80 text-sm">
          <div className="flex items-center gap-1.5"><RadioTower className="size-3.5 shrink-0 text-muted-foreground" /><span className="truncate" title={streamInputPresentation(row.original)}>{streamInputPresentation(row.original)}</span></div>
          <div className="mt-1 flex items-center gap-1.5 text-muted-foreground"><Video className="size-3.5 shrink-0" /><span className="truncate">{optionLabel(youtubeOutputLabels, row.original.youtube_output_id) || row.original.output_target || "出力未設定"}</span></div>
        </div>
      ),
    },
    {
      id: "recording",
      accessorFn: (stream) => compactList([recordingDescriptor(stream).label, stream.archive_file_name, stream.archive_masked_folder_id]).join(" "),
      header: "録画・保存",
      cell: ({ row }) => {
        const recording = recordingDescriptor(row.original);
        return (
          <div className="min-w-44 max-w-64 text-sm">
            <span className={cn("inline-flex rounded-md border px-2 py-0.5 text-xs font-medium", recording.className)}>{recording.label}</span>
            <div className="mt-1 truncate text-muted-foreground" title={row.original.archive_file_name}>{row.original.archive_file_name || optionLabel(archiveDestinationLabels, row.original.archive_drive_destination_id) || optionLabel(archiveProfileLabels, row.original.archive_profile_id) || "保存先未設定"}</div>
            {row.original.archive_folder_id_configured ? <div className="truncate text-xs text-muted-foreground">フォルダー {row.original.archive_masked_folder_id || "設定済み"}</div> : null}
          </div>
        );
      },
    },
    {
      id: "discord",
      accessorFn: (stream) =>
        compactList([
          optionLabel(discordLabels, stream.discord_config_id),
          stream.discord_config_id,
          stream.discord_guild_id,
          stream.discord_voice_channel_id,
          stream.discord_text_channel_id,
          stream.auto_start_trigger === "discord_voice_join" ? "VC参加で自動開始" : "手動開始",
        ]).join(" "),
      header: "自動開始",
      cell: ({ row }) => (
        <div className="min-w-40 text-sm">
          <div>{row.original.auto_start_trigger === "discord_voice_join" ? "VC参加で自動開始" : "手動開始"}</div>
          <div className="mt-1 truncate text-muted-foreground">{optionLabel(discordLabels, row.original.discord_config_id) || "Discord未設定"}</div>
          <div className="truncate text-xs text-muted-foreground">VC {row.original.discord_voice_channel_id || "未設定"}</div>
        </div>
      ),
    },
    {
      id: "nodes",
      accessorFn: (stream) => compactList([stream.assigned_worker_id, stream.assigned_encoder_id]).join(" "),
      header: "担当Node",
      cell: ({ row }) => (
        <div className="min-w-36 text-sm text-muted-foreground">
          <div className="truncate">Worker {row.original.assigned_worker_id || "未割当"}</div>
          <div className="truncate">Encoder {row.original.assigned_encoder_id || "未割当"}</div>
        </div>
      ),
    },
    {
      id: "updated",
      header: "更新",
      cell: ({ row }) => <span className="text-sm">{formatDateTime(row.original.updated_at || row.original.created_at, timezone)}</span>,
    },
  ];

  return (
    <div className="space-y-5">
      <section className="flex flex-col gap-3 border-b pb-5 sm:flex-row sm:items-end sm:justify-between">
        <div>
          <div className="flex items-center gap-2 text-sm font-medium text-primary"><RadioTower className="size-4" />Discord VC連動</div>
          <h1 className="mt-1 text-xl font-semibold">待機開始から終了後の録画まで一元管理</h1>
          <p className="mt-1 text-sm text-muted-foreground">VC参加を待機する配信枠、配信経路、録画状態、担当Nodeを一元管理します。</p>
        </div>
        {canCreate ? <Button onClick={() => setCreateOpen(true)}><Plus className="size-4" />配信枠を作成</Button> : null}
      </section>

      <StreamSummary rows={streamRows} />

      {streams.isError ? (
        <div className="flex flex-col gap-3 rounded-lg border border-amber-300 bg-amber-50 p-4 text-amber-900 dark:border-amber-900 dark:bg-amber-950/35 dark:text-amber-100 sm:flex-row sm:items-center sm:justify-between">
          <div className="flex gap-3"><AlertCircle className="mt-0.5 size-5 shrink-0" /><div><div className="text-sm font-semibold">配信枠を取得できませんでした</div><p className="mt-0.5 text-xs">通信状態を確認して再試行してください。新しい操作は一覧が更新されてから行ってください。</p></div></div>
          <Button variant="outline" size="sm" onClick={() => streams.refetch()}><RotateCw className="size-4" />再試行</Button>
        </div>
      ) : null}

      {actionNotice ? <div className={cn("rounded-lg border p-3 text-sm", actionNotice.tone === "success" ? "border-emerald-200 bg-emerald-50 text-emerald-800 dark:border-emerald-900 dark:bg-emerald-950/35 dark:text-emerald-200" : "border-red-200 bg-red-50 text-red-800 dark:border-red-900 dark:bg-red-950/35 dark:text-red-200")}>{actionNotice.message}</div> : null}

      <Card>
        <CardHeader className="border-b">
          <CardTitle>配信枠</CardTitle>
          <CardDescription>VC待機中・配信中・要対応の状態を確認し、必要な操作を実行できます。</CardDescription>
        </CardHeader>
        <CardContent>
          <DataTable columns={columns} data={streamRows} filterPlaceholder="配信名・状態・VC・URL・録画保存先で絞り込み" getRowId={(row) => row.id} responsive />
        </CardContent>
      </Card>

      <Sheet open={createOpen} onOpenChange={(open) => {
        setCreateOpen(open);
        if (!open && window.location.hash === "#create-stream") window.history.replaceState(null, "", window.location.pathname + window.location.search);
      }}>
        <SheetContent side="right" className="w-full overflow-y-auto p-0 sm:max-w-3xl">
          <SheetHeader className="sr-only"><SheetTitle>配信枠を作成</SheetTitle><SheetDescription>Discord VCの開始条件、入力、出力、録画を設定します。</SheetDescription></SheetHeader>
          <StreamSlotForm
            className="min-h-full rounded-none border-0 shadow-none"
            actionController={actionController}
            onActionResult={handleStreamActionResult}
            canCreate={canCreate}
            canUpdate={canUpdate}
            canAssignEncoder={can("services.assign")}
            canAssignWorker={can("workers.assign")}
            onSaved={(stream) => {
              setCreatedStreams((current) => [stream, ...current.filter((item) => item.id !== stream.id)]);
              setActionNotice({ tone: "success", message: `${stream.name} を作成しました。稼働中の配信を保護するためNode割り当ては変更していません。開始前にこの配信枠を編集し、担当Nodeを明示的に割り当ててください。` });
              setCreateOpen(false);
            }}
          />
        </SheetContent>
      </Sheet>

      <Sheet open={editingStream !== null} onOpenChange={(open) => { if (!open) setEditingStream(null); }}>
        <SheetContent side="right" className="w-full overflow-y-auto p-0 sm:max-w-3xl">
          <SheetHeader className="sr-only"><SheetTitle>配信枠を編集</SheetTitle><SheetDescription>待機中または終了済みの配信枠の設定を変更します。</SheetDescription></SheetHeader>
          {editingStream ? <StreamSlotForm
            key={editingStream.id}
            stream={editingStream}
            className="min-h-full rounded-none border-0 shadow-none"
            actionController={actionController}
            onActionResult={handleStreamActionResult}
            canCreate={canCreate}
            canUpdate={canUpdate}
            canAssignEncoder={can("services.assign")}
            canAssignWorker={can("workers.assign")}
            onSaved={(stream) => {
              setCreatedStreams((current) => [stream, ...current.filter((item) => item.id !== stream.id)]);
              setActionNotice({ tone: "success", message: `${stream.name} の設定を更新しました。開始前に担当Nodeと出力先を確認してください。` });
              setEditingStream(null);
            }}
          /> : null}
        </SheetContent>
      </Sheet>

      <StreamDetailsDialog
        stream={currentSelectedStream}
        actionController={actionController}
        onOpenChange={(open) => { if (!open) setSelectedStream(null); }}
        discordLabels={discordLabels}
        youtubeOutputLabels={youtubeOutputLabels}
        archiveAccountLabels={archiveAccountLabels}
        archiveDestinationLabels={archiveDestinationLabels}
        archiveProfileLabels={archiveProfileLabels}
        overlayProfileLabels={overlayProfileLabels}
      />
    </div>
  );
}

function StreamSummary({ rows }: { rows: Stream[] }) {
  const counts = rows.reduce((value, stream) => {
    const status = String(stream.status).toLowerCase();
    if (["live", "starting"].includes(status)) value.live += 1;
    else if (["failed", "error"].includes(status)) value.attention += 1;
    else if (["completed", "stopped"].includes(status)) value.completed += 1;
    else value.waiting += 1;
    if (recordingDescriptor(stream).label === "録画中") value.recording += 1;
    return value;
  }, { live: 0, waiting: 0, recording: 0, attention: 0, completed: 0 });
  const items = [
    { label: "配信中", value: counts.live, tone: "text-emerald-700 dark:text-emerald-300" },
    { label: "待機中", value: counts.waiting, tone: "text-blue-700 dark:text-blue-300" },
    { label: "録画中", value: counts.recording, tone: "text-red-700 dark:text-red-300" },
    { label: "要対応", value: counts.attention, tone: "text-red-700 dark:text-red-300" },
    { label: "終了", value: counts.completed, tone: "text-muted-foreground" },
  ];
  return <section className="grid grid-cols-2 overflow-hidden rounded-lg border bg-card sm:grid-cols-5" aria-label="配信状態の集計">{items.map((item) => <div key={item.label} className="border-b border-r p-3 last:border-r-0 sm:border-b-0"><div className="text-xs text-muted-foreground">{item.label}</div><div className={cn("mt-1 text-xl font-semibold tabular-nums", item.tone)}>{item.value}</div></div>)}</section>;
}

function StreamDetailsDialog({ stream, actionController, onOpenChange, discordLabels, youtubeOutputLabels, archiveAccountLabels, archiveDestinationLabels, archiveProfileLabels, overlayProfileLabels }: { stream: Stream | null; actionController: ReturnType<typeof createStreamActionController>; onOpenChange: (open: boolean) => void; discordLabels: Map<string, string>; youtubeOutputLabels: Map<string, string>; archiveAccountLabels: Map<string, string>; archiveDestinationLabels: Map<string, string>; archiveProfileLabels: Map<string, string>; overlayProfileLabels: Map<string, string> }) {
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

function isPreviewableStreamStatus(status: string) {
  return ["starting", "live", "stopping"].includes(String(status).trim().toLowerCase());
}

function DetailGroup({ title, children }: { title: string; children: ReactNode }) { return <section className="rounded-lg border bg-muted/15 p-4"><h3 className="mb-3 text-sm font-semibold">{title}</h3>{children}</section>; }
function DetailLine({ label, value, mono = false }: { label: string; value: string; mono?: boolean }) { return <div className="grid grid-cols-[6rem_minmax(0,1fr)] gap-2 border-b py-2 text-sm last:border-b-0"><span className="text-muted-foreground">{label}</span><span className={cn("min-w-0 break-all", mono && "font-mono text-xs")}>{value}</span></div>; }

function StreamSlotForm({
  stream,
  className,
  actionController,
  onActionResult,
  canCreate,
  canUpdate,
  canAssignEncoder,
  canAssignWorker,
  onSaved,
}: {
  stream?: Stream | null;
  className?: string;
  actionController: ReturnType<typeof createStreamActionController>;
  onActionResult: (result: StreamActionExecutionResult, intent: StreamActionIntent) => void;
  canCreate: boolean;
  canUpdate: boolean;
  canAssignEncoder: boolean;
  canAssignWorker: boolean;
  onSaved: (stream: Stream) => void;
}) {
  const { t } = useI18n();
  const saveControl = useRef<StreamActionControlHandle>(null);
  const discordConfigs = useResourceOptions("/discord/configs", ["name", "service_id", "id"]);
  const youtubeOutputs = useResourceOptions("/youtube/outputs", ["name", "id"]);
  const encoderProfiles = useResourceOptions("/profiles/encoder", ["name", "id"]);
  const captionProfiles = useResourceOptions("/profiles/caption", ["name", "id"]);
  const overlayProfiles = useResourceOptions("/profiles/overlay", ["name", "id"]);
  const archiveProfiles = useResourceOptions("/profiles/archive", ["name", "id"]);
  const encoderNodes = useServiceOptions("encoder_recorder", stream?.id);
  const workerNodes = useServiceOptions("worker", stream?.id);
  const editing = stream !== undefined && stream !== null;
  const liveEditing = editing && stream?.status === "live";
  const [name, setName] = useState(stream?.name || "");
  const [discordConfigID, setDiscordConfigID] = useState(optionOrNone(stream?.discord_config_id));
  const [guildID, setGuildID] = useState(stream?.discord_guild_id || "");
  const [voiceChannelID, setVoiceChannelID] = useState(stream?.discord_voice_channel_id || "");
  const [textChannelID, setTextChannelID] = useState(stream?.discord_text_channel_id || "");
  const [autoStartFromDiscord, setAutoStartFromDiscord] = useState(editing ? stream?.auto_start_trigger === "discord_voice_join" : true);
  const [youtubeOutputID, setYouTubeOutputID] = useState(optionOrNone(stream?.youtube_output_id));
  const [archiveProfileID, setArchiveProfileID] = useState(optionOrNone(stream?.archive_profile_id));
  const [encoderProfileID, setEncoderProfileID] = useState(optionOrNone(stream?.encoder_profile_id));
  const [captionProfileID, setCaptionProfileID] = useState(optionOrNone(stream?.caption_profile_id));
  const [watermarkEnabled, setWatermarkEnabled] = useState(Boolean(stream?.overlay_profile_id));
  const [overlayProfileID, setOverlayProfileID] = useState(optionOrNone(stream?.overlay_profile_id));
  const [encoderAudioGainDB, setEncoderAudioGainDB] = useState(String(stream?.encoder_audio_gain_db ?? 0));
  // null means the operator has not touched this assignment control. It keeps
  // the field omitted so the backend preserves ownership. Selecting
  // "未選択" changes the state to an explicit empty assignment request.
  const [encoderServiceID, setEncoderServiceID] = useState<string | null>(null);
  const [workerServiceID, setWorkerServiceID] = useState<string | null>(null);
  const [scheduledStartAt, setScheduledStartAt] = useState(() => streamScheduleInputValue(stream?.scheduled_start_at));
  const [message, setMessage] = useState("");

  const effectiveEncoderServiceID = encoderServiceID ?? optionOrNone(stream?.assigned_encoder_id);
  const effectiveWorkerServiceID = workerServiceID ?? optionOrNone(stream?.assigned_worker_id);

  const payload = useMemo(
    () =>
      liveEditing ? {
        encoder_audio_gain_db: Number(encoderAudioGainDB),
        overlay_profile_id: watermarkEnabled ? selectedValue(overlayProfileID) : "",
      } : (editing ? buildStreamSettingsPayload : buildStreamCreatePayload)({
        name,
        discordConfigID: selectedValue(discordConfigID),
        discordGuildID: guildID,
        discordVoiceChannelID: voiceChannelID,
        discordTextChannelID: textChannelID,
        autoStartFromDiscord,
        youtubeOutputID: selectedValue(youtubeOutputID),
        archiveProfileID: selectedValue(archiveProfileID),
        encoderProfileID: selectedValue(encoderProfileID),
        captionProfileID: selectedValue(captionProfileID),
        watermarkEnabled,
        overlayProfileID: selectedValue(overlayProfileID),
        encoderAudioGainDB: Number(encoderAudioGainDB),
        // Omit assignments that this editor cannot manage. A selectable
        // "未選択" remains an explicit empty value and therefore unassigns.
        encoderServiceID: canAssignEncoder && encoderServiceID !== null ? selectedValue(effectiveEncoderServiceID) : undefined,
        workerServiceID: canAssignWorker && workerServiceID !== null ? selectedValue(effectiveWorkerServiceID) : undefined,
        scheduledStartAt: streamScheduleRFC3339(scheduledStartAt),
        scheduledEndAt: stream?.scheduled_end_at || "",
        encoderInputURL: stream?.encoder_input_url || "",
      }),
    [archiveProfileID, autoStartFromDiscord, canAssignEncoder, canAssignWorker, captionProfileID, discordConfigID, editing, effectiveEncoderServiceID, effectiveWorkerServiceID, encoderAudioGainDB, encoderProfileID, encoderServiceID, guildID, liveEditing, name, overlayProfileID, scheduledStartAt, stream?.encoder_input_url, stream?.scheduled_end_at, textChannelID, voiceChannelID, watermarkEnabled, workerServiceID, youtubeOutputID],
  );
  const hasDiscordTarget = guildID.trim() !== "" || voiceChannelID.trim() !== "" || textChannelID.trim() !== "";
  const discordReady = !hasDiscordTarget || selectedValue(discordConfigID) !== "";
  const autoStartReady = !autoStartFromDiscord || (selectedValue(discordConfigID) !== "" && guildID.trim() !== "" && voiceChannelID.trim() !== "");
  const watermarkReady = !watermarkEnabled || selectedValue(overlayProfileID) !== "";
  const encoderAudioGainValue = Number(encoderAudioGainDB);
  const encoderAudioGainReady = Number.isFinite(encoderAudioGainValue) && encoderAudioGainValue >= -60 && encoderAudioGainValue <= 24;
  const nodeAssignmentReady = !editing || !autoStartFromDiscord || ((!canAssignEncoder || selectedValue(effectiveEncoderServiceID) !== "") && (!canAssignWorker || selectedValue(effectiveWorkerServiceID) !== ""));
  const nodeAssignmentPermissionLimited = editing && autoStartFromDiscord && (!canAssignEncoder || !canAssignWorker);
  const actionIntent = useMemo<StreamActionIntent>(() => ({
    id: liveEditing ? "STR-03" : editing ? "STR-02" : "STR-01",
    ...(stream ? { stream } : {}),
    payload,
    publicLabel: name.trim(),
  }), [editing, liveEditing, name, payload, stream]);
  const formReady = (editing ? canUpdate : canCreate)
    && (liveEditing || name.trim() !== "")
    && (liveEditing || (discordReady && autoStartReady && nodeAssignmentReady))
    && watermarkReady
    && encoderAudioGainReady;
  const handleSaveResult = useCallback((result: StreamActionExecutionResult, intent: StreamActionIntent) => {
    onActionResult(result, intent);
    if (result.kind === "succeeded" && isStreamValue(result.value)) {
      setMessage(liveEditing ? `${result.value.name} のライブ設定を停止せず反映しました。` : editing ? `${result.value.name} の設定を更新しました。` : `${result.value.name} を配信枠として作成しました。`);
      onSaved(result.value);
      return;
    }
    if (result.kind === "failed") setMessage(t(result.error.messageKey));
    else if (result.kind === "outcome_unknown") setMessage(t("confirmationOutcomeUnknown"));
    else if (result.kind === "blocked") setMessage(streamActionBlockedMessage(result.reason));
  }, [editing, liveEditing, onActionResult, onSaved, t]);

  return (
    <Card id="create-stream" className={className}>
      <CardHeader className="border-b">
        <CardTitle className="flex items-center gap-2">
          {editing ? <Pencil className="size-5" /> : <Plus className="size-5" />}
          {editing ? "配信枠を編集" : "配信枠を作成"}
        </CardTitle>
        <CardDescription>{liveEditing ? "配信を止めずにEncoder音量とウォーターマークを変更できます。" : editing ? "待機中または終了済みの枠設定を編集します。" : "Discord VCの開始条件、配信経路、録画保存先を設定します。Node割り当ては作成後に明示的に行います。"}</CardDescription>
      </CardHeader>
      <CardContent>
        <form
          className="space-y-4"
          onSubmit={(event) => {
            event.preventDefault();
            setMessage("");
            if (formReady) saveControl.current?.open();
          }}
        >
          {!liveEditing ? <><FormSection title="基本情報" description="運用中に識別する配信枠名">
            <div className="max-w-xl">
              <TextField label="配信枠名" value={name} onChange={setName} placeholder="例: 商品発表会 メイン配信" required />
            </div>
          </FormSection>

          <FormSection title="YouTube開始予定" description="空欄なら、配信開始時にYouTubeの枠をすぐ開始します。日時を指定した場合だけ予定配信になります。">
            <div className="max-w-xl">
              <TextField label="開始予定（任意）" type="datetime-local" value={scheduledStartAt} onChange={setScheduledStartAt} />
              <p className="mt-2 text-xs text-muted-foreground">日時はこのブラウザのタイムゾーンで扱います。既存の予定を消して保存すると、次回の開始は即時になります。</p>
            </div>
          </FormSection>

          <FormSection title="開始条件" description={editing ? "自動開始に使うDiscordと担当Node" : "自動開始に使うDiscord。担当Nodeは作成後の編集で割り当てます。"}>
            <label className="mb-3 flex min-h-10 items-center gap-2 rounded-md border bg-muted/20 px-3 text-sm">
              <Checkbox checked={autoStartFromDiscord} onCheckedChange={(value) => setAutoStartFromDiscord(value === true)} />
              Discord VCへの参加を検知して自動開始
            </label>
            <div className="grid gap-3 md:grid-cols-2">
              <SelectField label="Discord BOT設定" value={discordConfigID} onChange={setDiscordConfigID} options={[{ value: noneValue, label: "未選択" }, ...discordConfigs]} />
              <TextField label="DiscordサーバーID" value={guildID} onChange={setGuildID} />
              <TextField label="ボイスチャンネルID" value={voiceChannelID} onChange={setVoiceChannelID} />
              <TextField label="チャットチャンネルID" value={textChannelID} onChange={setTextChannelID} />
              {editing && canAssignWorker ? <SelectField label="担当Worker Node" value={effectiveWorkerServiceID} onChange={setWorkerServiceID} options={[{ value: noneValue, label: "未選択" }, ...workerNodes]} /> : null}
              {editing && canAssignEncoder ? <SelectField label="担当Encoder Node" value={effectiveEncoderServiceID} onChange={setEncoderServiceID} options={[{ value: noneValue, label: "未選択" }, ...encoderNodes]} /> : null}
            </div>
          </FormSection>

          {!editing ? <div className="flex gap-2 rounded-md border bg-muted/30 p-3 text-sm text-muted-foreground"><AlertCircle className="mt-0.5 size-4 shrink-0" />新規作成では既存配信のNode割り当てを変更しません。作成後に配信枠を編集し、開始前に担当Nodeを割り当ててください。</div> : null}

          <FormSection title="出力と録画" description="視聴先と事前登録した録画設定">
            <div className="grid gap-3 md:grid-cols-2">
              <SelectField label="YouTube出力" value={youtubeOutputID} onChange={setYouTubeOutputID} options={[{ value: noneValue, label: "未選択" }, ...youtubeOutputs]} />
              <SelectField label="録画プロファイル" value={archiveProfileID} onChange={setArchiveProfileID} options={[{ value: noneValue, label: "録画しない" }, ...archiveProfiles]} />
            </div>
            {archiveProfiles.length === 0 ? <p className="mt-3 text-sm text-muted-foreground">録画する場合は、先に<Link href="/admin/archive/" className="mx-1 underline underline-offset-2">録画・アーカイブ</Link>で録画プロファイルと保存先を作成してください。</p> : null}
          </FormSection>

          </> : null}

          <FormSection title="Encoderライブ調整" description="音量とウォーターマークは配信中でも停止せず反映されます。">
            <div className="grid gap-3 md:grid-cols-2">
              {!liveEditing ? <SelectField label="エンコード設定" value={encoderProfileID} onChange={setEncoderProfileID} options={[{ value: noneValue, label: "未選択" }, ...encoderProfiles]} /> : null}
              {!liveEditing ? <SelectField label="字幕設定" value={captionProfileID} onChange={setCaptionProfileID} options={[{ value: noneValue, label: "未選択" }, ...captionProfiles]} /> : null}
              <TextField label="Encoder音量（dB）" type="number" value={encoderAudioGainDB} onChange={setEncoderAudioGainDB} min="-60" max="24" step="0.1" />
              <label className="flex min-h-10 items-center gap-2 rounded-md border bg-muted/20 px-3 text-sm"><Checkbox checked={watermarkEnabled} onCheckedChange={(value) => setWatermarkEnabled(value === true)} />ウォーターマークを使用</label>
              <SelectField label="ウォーターマーク設定" value={overlayProfileID} onChange={setOverlayProfileID} options={[{ value: noneValue, label: "未選択" }, ...overlayProfiles]} disabled={!watermarkEnabled} />
            </div>
          </FormSection>

          {!encoderAudioGainReady ? <div className="flex gap-2 rounded-md border border-amber-200 bg-amber-50 p-3 text-sm text-amber-800"><AlertCircle className="mt-0.5 size-4 shrink-0" />Encoder音量は -60 dB から +24 dB の範囲で指定してください。</div> : null}

          {!liveEditing && hasDiscordTarget && !discordReady ? (
            <div className="flex gap-2 rounded-md border border-amber-200 bg-amber-50 p-3 text-sm text-amber-800">
              <AlertCircle className="mt-0.5 size-4 shrink-0" />
              Discordのサーバーやチャンネルを指定する場合は、使用するDiscord BOT設定も選択してください。
            </div>
          ) : null}
          {!liveEditing && autoStartFromDiscord && !autoStartReady ? (
            <div className="flex gap-2 rounded-md border border-amber-200 bg-amber-50 p-3 text-sm text-amber-800">
              <AlertCircle className="mt-0.5 size-4 shrink-0" />
              自動開始を使うには、Discord BOT設定、サーバーID、ボイスチャンネルIDが必要です。
            </div>
          ) : null}
          {watermarkEnabled && !watermarkReady ? (
            <div className="flex gap-2 rounded-md border border-amber-200 bg-amber-50 p-3 text-sm text-amber-800">
              <AlertCircle className="mt-0.5 size-4 shrink-0" />
              ウォーターマークを使う場合は、ウォーターマーク設定を選択してください。
            </div>
          ) : null}
          {!liveEditing && autoStartFromDiscord && canAssignEncoder && canAssignWorker && !nodeAssignmentReady ? (
            <div className="flex gap-2 rounded-md border border-amber-200 bg-amber-50 p-3 text-sm text-amber-800">
              <AlertCircle className="mt-0.5 size-4 shrink-0" />
              自動開始する配信枠には、担当Encoder Nodeと担当Worker Nodeを選択してください。
            </div>
          ) : null}
          {!liveEditing && nodeAssignmentPermissionLimited ? (
            <div className="flex gap-2 rounded-md border border-amber-200 bg-amber-50 p-3 text-sm text-amber-800">
              <AlertCircle className="mt-0.5 size-4 shrink-0" />
              Nodeを割り当てる権限がありません。管理者に依頼し、サービス稼働画面で割り当てを確認してください。
            </div>
          ) : null}
          {message ? <div aria-live="polite" className="rounded-md border bg-muted/30 p-3 text-sm text-muted-foreground">{message}</div> : null}
          {editing && !canUpdate ? <p className="text-sm text-red-600">配信枠を更新する権限がありません。</p> : null}
          {!editing && !canCreate ? <p className="text-sm text-red-600">配信枠を作成する権限がありません。</p> : null}
          <div className="flex justify-end">
            <StreamActionControl
              ref={saveControl}
              controller={actionController}
              intent={actionIntent}
              label={liveEditing ? "ライブ設定を反映" : editing ? "設定を保存" : "配信枠を作成"}
              buttonProps={{ type: "submit" }}
              disabled={!formReady}
              onResult={handleSaveResult}
            >
              {editing ? <Pencil className="size-4" /> : <Plus className="size-4" />}
              {liveEditing ? "ライブ設定を反映" : editing ? "設定を保存" : "配信枠を作成"}
            </StreamActionControl>
          </div>
        </form>
      </CardContent>
    </Card>
  );
}

function FormSection({ title, description, children }: { title: string; description: string; children: ReactNode }) {
  return (
    <fieldset className="rounded-lg border p-4">
      <legend className="px-1 text-sm font-semibold">{title}</legend>
      <p className="mb-3 text-xs text-muted-foreground">{description}</p>
      {children}
    </fieldset>
  );
}

function TextField({
  label,
  value,
  onChange,
  placeholder,
  type = "text",
  required,
  error,
  min,
  max,
  step,
}: {
  label: string;
  value: string;
  onChange: (value: string) => void;
  placeholder?: string;
  type?: string;
  required?: boolean;
  error?: string;
  min?: string;
  max?: string;
  step?: string;
}) {
  return (
    <label className="grid gap-1.5 text-sm">
      <span className="font-medium">{label}</span>
      <Input type={type} value={value} onChange={(event) => onChange(event.target.value)} placeholder={placeholder} required={required} min={min} max={max} step={step} aria-invalid={Boolean(error)} />
      {error ? <span className="text-xs text-red-600 dark:text-red-300">{error}</span> : null}
    </label>
  );
}

function SelectField({ label, value, onChange, options, disabled }: { label: string; value: string; onChange: (value: string) => void; options: SelectOption[]; disabled?: boolean }) {
  const selected = options.find((option) => option.value === value);
  return (
    <label className="grid gap-1.5 text-sm">
      <span className="font-medium">{label}</span>
      <Select value={value} onValueChange={onChange} disabled={disabled}>
        <SelectTrigger className="w-full" disabled={disabled}>
          <span className="min-w-0 truncate">{selected?.label || <SelectValue />}</span>
        </SelectTrigger>
        <SelectContent>
          {options.map((option) => (
            <SelectItem key={option.value} value={option.value} textValue={option.label} disabled={option.disabled}>
              <span className="min-w-0 truncate">{option.label}</span>
            </SelectItem>
          ))}
        </SelectContent>
      </Select>
      {selected?.description ? <span className="text-xs text-muted-foreground">{selected.description}</span> : null}
    </label>
  );
}

function useResourceOptions(path: string, labelKeys: string[], detailKeys: string[] = []) {
  const query = useResourceData<unknown>(path);
  const rows = useMemo(() => normalizeRows(query.data), [query.data]);
  return useMemo(
    () =>
      rows
        .map((row) => {
          const value = rowString(row, ["id"]);
          const label = firstNonEmpty(rowString(row, labelKeys), value);
          const description = compactList(detailKeys.map((key) => rowString(row, [key]))).join(" / ");
          return { value, label, description };
        })
        .filter((option) => option.value),
    [detailKeys, labelKeys, rows],
  );
}

function useOAuthAccountOptions(purpose: OAuthAccountPurpose) {
  const query = useResourceData<unknown>("/integrations/oauth-accounts");
  const rows = useMemo(() => normalizeRows(query.data), [query.data]);
  return useMemo(
    () =>
      rows
        .filter((row) => oauthAccountSupportsPurpose(row, purpose))
        .map((row) => {
          const value = rowString(row, ["id"]);
          const provider = rowString(row, ["provider_type"]);
          return {
            value,
            label: oauthAccountLabel(row),
            description: compactList([provider ? providerTypeLabel(provider) : "", oauthAccountPurposeLabel(row)]).join(" / "),
          };
        })
        .filter((option) => option.value),
    [purpose, rows],
  );
}

function useServiceOptions(serviceType: string, editingStreamID?: string) {
  const query = useServiceHealth();
  const rows = useMemo(() => query.data || [], [query.data]);
  return useMemo(
    () =>
      rows
        .filter((row) => row.service_type === serviceType)
        .map((row) => {
          const value = row.service_id || row.id;
          const label = firstNonEmpty(row.service_name, row.service_id || row.id);
          return streamServiceAssignmentOption({ value, label, currentStreamID: row.current_stream_id }, editingStreamID);
        })
        .filter((option) => option.value),
    [editingStreamID, rows, serviceType],
  );
}

function useOptionLabelMap(options: SelectOption[]) {
  return useMemo(() => new Map(options.map((option) => [option.value, option.label])), [options]);
}

function optionLabel(labels: Map<string, string>, value?: string) {
  const id = value?.trim() || "";
  if (!id) return "";
  return labels.get(id) || id;
}

function compactList(values: Array<string | undefined>) {
  return values.map((value) => value?.trim() || "").filter(Boolean);
}

function streamInputPresentation(stream: Stream) {
  const configured = safeDisplayURL(stream.encoder_input_url || stream.input_source);
  if (configured) return configured;
  if (stream.assigned_encoder_id) return "Node側で開始時に自動生成";
  return "入力未設定";
}

function normalizeRows(data: unknown): ResourceRow[] {
  if (!data) return [];
  if (Array.isArray(data)) return data.filter(isRecord);
  if (isRecord(data)) {
    for (const key of ["items", "data", "results"]) {
      const value = data[key];
      if (Array.isArray(value)) return value.filter(isRecord);
    }
  }
  return [];
}

function rowString(row: ResourceRow, keys: string[]) {
  for (const key of keys) {
    const value = row[key];
    if (typeof value === "string" && value.trim() !== "") return value;
    if (typeof value === "number") return String(value);
  }
  return "";
}

function firstNonEmpty(...values: string[]) {
  return values.find((value) => value.trim() !== "") || "";
}

function isRecord(value: unknown): value is ResourceRow {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

function selectedValue(value: string) {
  return value === noneValue ? "" : value;
}

function optionOrNone(value?: string) {
  return value?.trim() || noneValue;
}

function streamActionLabel(id: StreamActionIntent["id"]) {
  const labels: Record<StreamActionIntent["id"], string> = {
    "STR-01": "作成", "STR-02": "設定更新", "STR-03": "ライブ設定更新",
    "STR-04": "開始", "STR-05": "停止", "STR-06": "強制停止",
    "STR-07": "固定Relay回復", "STR-08": "開始準備確認", "STR-09": "Workerテスト",
    "STR-10": "削除", "STR-11": "プレビューURL発行",
  };
  return labels[id];
}

function streamActionBlockedMessage(reason: Extract<StreamActionExecutionResult, { kind: "blocked" }>["reason"]) {
  if (reason === "permission-denied") return "この操作を実行する権限がありません。";
  if (reason === "authority-changed") return "対象の状態が変更されたため送信しませんでした。最新状態を確認してください。";
  if (reason === "duplicate") return "同じ操作を処理中です。";
  if (reason === "reconciliation-required") return "前回の結果を確認できないため再送を抑止しています。最新状態または監査ログを確認してください。";
  return "最新の権限または配信状態を確認できないため、操作を送信しませんでした。";
}

function isStreamValue(value: unknown): value is Stream {
  return isRecord(value)
    && typeof value.id === "string"
    && typeof value.name === "string"
    && typeof value.status === "string";
}

function streamStatusAllowsStart(status: Stream["status"]) {
  return ["created", "draft", "scheduled", "ready", "failed"].includes(String(status).toLowerCase());
}

function streamStatusAllowsEdit(status: Stream["status"]) {
  return !["starting", "live", "stopping"].includes(String(status).toLowerCase());
}

function streamStatusAllowsStop(status: Stream["status"]) {
  return ["starting", "live", "failed"].includes(String(status).toLowerCase());
}

function streamStatusAllowsForceStop(status: Stream["status"]) {
  return ["starting", "live", "stopping", "failed"].includes(String(status).toLowerCase());
}

function streamStatusAllowsDelete(status: Stream["status"]) {
  return !["starting", "live", "stopping"].includes(String(status).toLowerCase());
}

function formatDateTime(value?: string, timezone?: string) {
  return formatDateTimeInTimeZone(value, timezone, { month: "2-digit", day: "2-digit", hour: "2-digit", minute: "2-digit" });
}
