"use client";

import { useCallback, useEffect, useMemo, useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import type { ColumnDef } from "@tanstack/react-table";
import { AlertCircle, Check, Copy, Eye, Pencil, Play, Plus, RadioTower, RotateCw, SlidersHorizontal, Square, Shuffle, Trash2, Video } from "lucide-react";

import { StatusBadge } from "@/components/admin/status-badge";
import { useI18n } from "@/components/admin/i18n-provider";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Sheet, SheetContent, SheetDescription, SheetHeader, SheetTitle } from "@/components/ui/sheet";
import { DataTable } from "@/components/tables/data-table";
import { useAppSettings, useCurrentUser, useResourceData, useStreams } from "@/features/queries";
import { StreamActionControl } from "@/features/streams/stream-action-control";
import { createStreamActionController, type StreamActionExecutionResult } from "@/features/streams/stream-action-controller";
import type { StreamActionIntent } from "@/features/streams/stream-action-descriptors";
import { streamActionBlockedMessage, streamActionLabel } from "@/features/streams/stream-action-feedback";
import { mutateStreamAction, refreshStreamActionAuthority, streamActionStateSnapshot, streamPermissionSnapshot } from "@/features/streams/stream-action-runtime";
import { StreamDetailsDialog } from "@/features/streams/stream-details-dialog";
import { streamStatusAllowsDelete, streamStatusAllowsEdit, streamStatusAllowsForceStop, streamStatusAllowsStart, streamStatusAllowsStop } from "@/features/streams/stream-lifecycle";
import { StreamSlotForm } from "@/features/streams/stream-slot-form";
import { StreamSummary } from "@/features/streams/stream-summary";
import { compactList, normalizeRows, optionLabel, rowString, streamInputPresentation, useOAuthAccountOptions, useOptionLabelMap, useResourceOptions } from "@/features/streams/stream-view-options";
import { hasPermission } from "@/lib/auth/permissions";
import { recordingDescriptor } from "@/lib/stream-presentation";
import { staticRelayRecoveryActionAvailable } from "@/lib/stream-static-relay";
import { formatDateTimeInTimeZone } from "@/lib/timezone";
import { cn } from "@/lib/utils";
import type { Stream } from "@/types/domain";

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
  const [, setActionAuthorityRevision] = useState(0);

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
    if (result.kind === "failed" && intent.id === "STR-08") {
      // The control clears its latch only when both permission and resource
      // authorities have completed a safe read-only refresh.
      return refreshStreamActionAuthority(queryClient).then((refreshed) => {
        if (refreshed) {
          actionController.reconcile(intent);
          // Query data replacement can remount a row control. Re-render the
          // feature owner so the current control evaluates the cleared latch.
          setActionAuthorityRevision((revision) => revision + 1);
        }
        return refreshed;
      });
    }
    // Reconciliation fetches are safe and never resend the mutation.
    void queryClient.invalidateQueries({ queryKey: ["auth", "me"] });
    void queryClient.invalidateQueries({ queryKey: ["streams"] });
  }, [actionController, queryClient, t]);
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
        <div className="min-w-52"><div className="flex items-center gap-2"><div className="font-medium">{row.original.name}</div><Button variant="outline" size="icon-sm" aria-label="配信IDをコピー" onClick={() => void copyStreamID(row.original.id)}>{copiedStreamID === row.original.id ? <Check className="size-4" /> : <Copy className="size-4" />}</Button></div></div>
      ),
    },
    { accessorKey: "status", header: t("status"), cell: ({ row }) => <StatusBadge status={row.original.status} showDetail /> },
    {
      id: "actions",
      header: t("actions"),
      cell: ({ row }) => (
        <div className="flex min-w-44 flex-nowrap gap-1">
          <Button variant="outline" size="icon-sm" aria-label={t("details")} onClick={() => setSelectedStream(row.original)}><Eye /></Button>
          {String(row.original.status).toLowerCase() === "live" ? <Button variant="outline" size="icon-sm" aria-label={`${row.original.name} ライブ調整`} title="ライブ調整" onClick={() => setEditingStream(row.original)} disabled={!canUpdate}><SlidersHorizontal /></Button> : null}
          {streamStatusAllowsEdit(row.original.status) ? <Button variant="outline" size="icon-sm" aria-label={`${row.original.name} を編集`} onClick={() => setEditingStream(row.original)} disabled={!canUpdate}><Pencil /></Button> : null}
          {streamStatusAllowsStart(row.original.status) ? <StreamActionControl controller={actionController} intent={{ id: "STR-04", stream: row.original }} label={`${row.original.name} を開始`} buttonProps={{ variant: "outline", size: "icon-sm" }} onResult={handleStreamActionResult}><Play /></StreamActionControl> : null}
          {streamStatusAllowsStop(row.original.status) ? <StreamActionControl controller={actionController} intent={{ id: "STR-05", stream: row.original }} label={`${row.original.name} を停止`} buttonProps={{ variant: "outline", size: "icon-sm" }} onResult={handleStreamActionResult}><Square /></StreamActionControl> : null}
          {streamStatusAllowsForceStop(row.original.status) ? <StreamActionControl controller={actionController} intent={{ id: "STR-06", stream: row.original }} label={`${row.original.name} を強制停止`} buttonProps={{ variant: "destructive", size: "icon-sm" }} onResult={handleStreamActionResult}><AlertCircle /></StreamActionControl> : null}
          {staticRelayRecoveryActionAvailable(staticRelayOutputIDs.has(row.original.youtube_output_id || "") ? "live_api_relay_static" : "", row.original.status) ? <StreamActionControl controller={actionController} intent={{ id: "STR-07", stream: row.original, staticRelayRecoveryAvailable: true }} label={`${row.original.name} の固定Relay回復を実行`} buttonProps={{ variant: "outline", size: "icon-sm" }} onResult={handleStreamActionResult}><RadioTower /></StreamActionControl> : null}
          <StreamActionControl controller={actionController} intent={{ id: "STR-08", stream: row.original }} label={`${row.original.name} の開始準備を再確認`} buttonProps={{ variant: "outline", size: "icon-sm" }} onResult={handleStreamActionResult}><RotateCw /></StreamActionControl>
          <StreamActionControl controller={actionController} intent={{ id: "STR-09", stream: row.original }} label={`${row.original.name} のWorkerテストを実行`} buttonProps={{ variant: "outline", size: "icon-sm" }} onResult={handleStreamActionResult}><Shuffle /></StreamActionControl>
          {streamStatusAllowsDelete(row.original.status) ? <StreamActionControl controller={actionController} intent={{ id: "STR-10", stream: row.original }} label={`${row.original.name} を削除`} buttonProps={{ variant: "destructive", size: "icon-sm" }} onResult={handleStreamActionResult}><Trash2 /></StreamActionControl> : null}
        </div>
      ),
    },
    {
      id: "route",
      accessorFn: (stream) => compactList([stream.encoder_input_url, stream.input_source, stream.output_target, stream.youtube_output_id]).join(" "),
      header: "配信経路",
      cell: ({ row }) => <div className="min-w-56 max-w-80 text-sm"><div className="flex items-center gap-1.5"><RadioTower className="size-3.5 shrink-0 text-muted-foreground" /><span className="truncate" title={streamInputPresentation(row.original)}>{streamInputPresentation(row.original)}</span></div><div className="mt-1 flex items-center gap-1.5 text-muted-foreground"><Video className="size-3.5 shrink-0" /><span className="truncate">{optionLabel(youtubeOutputLabels, row.original.youtube_output_id) || row.original.output_target || "出力未設定"}</span></div></div>,
    },
    {
      id: "recording",
      accessorFn: (stream) => compactList([recordingDescriptor(stream).label, stream.archive_file_name, stream.archive_masked_folder_id]).join(" "),
      header: "録画・保存",
      cell: ({ row }) => {
        const recording = recordingDescriptor(row.original);
        return <div className="min-w-44 max-w-64 text-sm"><span className={cn("inline-flex rounded-md border px-2 py-0.5 text-xs font-medium", recording.className)}>{recording.label}</span><div className="mt-1 truncate text-muted-foreground" title={row.original.archive_file_name}>{row.original.archive_file_name || optionLabel(archiveDestinationLabels, row.original.archive_drive_destination_id) || optionLabel(archiveProfileLabels, row.original.archive_profile_id) || "保存先未設定"}</div>{row.original.archive_folder_id_configured ? <div className="truncate text-xs text-muted-foreground">フォルダー {row.original.archive_masked_folder_id || "設定済み"}</div> : null}</div>;
      },
    },
    {
      id: "discord",
      accessorFn: (stream) => compactList([optionLabel(discordLabels, stream.discord_config_id), stream.discord_config_id, stream.discord_guild_id, stream.discord_voice_channel_id, stream.discord_text_channel_id, stream.auto_start_trigger === "discord_voice_join" ? "VC参加で自動開始" : "手動開始"]).join(" "),
      header: "自動開始",
      cell: ({ row }) => <div className="min-w-40 text-sm"><div>{row.original.auto_start_trigger === "discord_voice_join" ? "VC参加で自動開始" : "手動開始"}</div><div className="mt-1 truncate text-muted-foreground">{optionLabel(discordLabels, row.original.discord_config_id) || "Discord未設定"}</div><div className="truncate text-xs text-muted-foreground">VC {row.original.discord_voice_channel_id || "未設定"}</div></div>,
    },
    {
      id: "nodes",
      accessorFn: (stream) => compactList([stream.assigned_worker_id, stream.assigned_encoder_id]).join(" "),
      header: "担当Node",
      cell: ({ row }) => <div className="min-w-36 text-sm text-muted-foreground"><div className="truncate">Worker {row.original.assigned_worker_id || "未割当"}</div><div className="truncate">Encoder {row.original.assigned_encoder_id || "未割当"}</div></div>,
    },
    { id: "updated", header: "更新", cell: ({ row }) => <span className="text-sm">{formatDateTime(row.original.updated_at || row.original.created_at, timezone)}</span> },
  ];

  return (
    <div className="space-y-5">
      <section className="flex flex-col gap-3 border-b pb-5 sm:flex-row sm:items-end sm:justify-between"><div><div className="flex items-center gap-2 text-sm font-medium text-primary"><RadioTower className="size-4" />Discord VC連動</div><h1 className="mt-1 text-xl font-semibold">待機開始から終了後の録画まで一元管理</h1><p className="mt-1 text-sm text-muted-foreground">VC参加を待機する配信枠、配信経路、録画状態、担当Nodeを一元管理します。</p></div>{canCreate ? <Button onClick={() => setCreateOpen(true)}><Plus className="size-4" />配信枠を作成</Button> : null}</section>
      <StreamSummary rows={streamRows} />
      {streams.isError ? <div className="flex flex-col gap-3 rounded-lg border border-amber-300 bg-amber-50 p-4 text-amber-900 dark:border-amber-900 dark:bg-amber-950/35 dark:text-amber-100 sm:flex-row sm:items-center sm:justify-between"><div className="flex gap-3"><AlertCircle className="mt-0.5 size-5 shrink-0" /><div><div className="text-sm font-semibold">配信枠を取得できませんでした</div><p className="mt-0.5 text-xs">通信状態を確認して再試行してください。新しい操作は一覧が更新されてから行ってください。</p></div></div><Button variant="outline" size="sm" onClick={() => streams.refetch()}><RotateCw className="size-4" />再試行</Button></div> : null}
      {actionNotice ? <div className={cn("rounded-lg border p-3 text-sm", actionNotice.tone === "success" ? "border-emerald-200 bg-emerald-50 text-emerald-800 dark:border-emerald-900 dark:bg-emerald-950/35 dark:text-emerald-200" : "border-red-200 bg-red-50 text-red-800 dark:border-red-900 dark:bg-red-950/35 dark:text-red-200")}>{actionNotice.message}</div> : null}
      <Card><CardHeader className="border-b"><CardTitle>配信枠</CardTitle><CardDescription>VC待機中・配信中・要対応の状態を確認し、必要な操作を実行できます。</CardDescription></CardHeader><CardContent><DataTable columns={columns} data={streamRows} filterPlaceholder="配信名・状態・VC・URL・録画保存先で絞り込み" getRowId={(row) => row.id} responsive /></CardContent></Card>
      <Sheet open={createOpen} onOpenChange={(open) => { setCreateOpen(open); if (!open && window.location.hash === "#create-stream") window.history.replaceState(null, "", window.location.pathname + window.location.search); }}>
        <SheetContent side="right" className="w-full overflow-y-auto p-0 sm:max-w-3xl"><SheetHeader className="sr-only"><SheetTitle>配信枠を作成</SheetTitle><SheetDescription>Discord VCの開始条件、入力、出力、録画を設定します。</SheetDescription></SheetHeader><StreamSlotForm className="min-h-full rounded-none border-0 shadow-none" actionController={actionController} onActionResult={handleStreamActionResult} canCreate={canCreate} canUpdate={canUpdate} canAssignEncoder={can("services.assign")} canAssignWorker={can("workers.assign")} onSaved={(stream) => { setCreatedStreams((current) => [stream, ...current.filter((item) => item.id !== stream.id)]); setActionNotice({ tone: "success", message: `${stream.name} を作成しました。稼働中の配信を保護するためNode割り当ては変更していません。開始前にこの配信枠を編集し、担当Nodeを明示的に割り当ててください。` }); setCreateOpen(false); }} /></SheetContent>
      </Sheet>
      <Sheet open={editingStream !== null} onOpenChange={(open) => { if (!open) setEditingStream(null); }}>
        <SheetContent side="right" className="w-full overflow-y-auto p-0 sm:max-w-3xl"><SheetHeader className="sr-only"><SheetTitle>配信枠を編集</SheetTitle><SheetDescription>待機中または終了済みの配信枠の設定を変更します。</SheetDescription></SheetHeader>{editingStream ? <StreamSlotForm key={editingStream.id} stream={editingStream} className="min-h-full rounded-none border-0 shadow-none" actionController={actionController} onActionResult={handleStreamActionResult} canCreate={canCreate} canUpdate={canUpdate} canAssignEncoder={can("services.assign")} canAssignWorker={can("workers.assign")} onSaved={(stream) => { setCreatedStreams((current) => [stream, ...current.filter((item) => item.id !== stream.id)]); setActionNotice({ tone: "success", message: `${stream.name} の設定を更新しました。開始前に担当Nodeと出力先を確認してください。` }); setEditingStream(null); }} /> : null}</SheetContent>
      </Sheet>
      <StreamDetailsDialog stream={currentSelectedStream} actionController={actionController} onOpenChange={(open) => { if (!open) setSelectedStream(null); }} discordLabels={discordLabels} youtubeOutputLabels={youtubeOutputLabels} archiveAccountLabels={archiveAccountLabels} archiveDestinationLabels={archiveDestinationLabels} archiveProfileLabels={archiveProfileLabels} overlayProfileLabels={overlayProfileLabels} />
    </div>
  );
}

function formatDateTime(value?: string, timezone?: string) {
  return formatDateTimeInTimeZone(value, timezone, { month: "2-digit", day: "2-digit", hour: "2-digit", minute: "2-digit" });
}
