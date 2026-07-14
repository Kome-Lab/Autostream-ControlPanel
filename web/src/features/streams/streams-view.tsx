"use client";

import { type ReactNode, useEffect, useMemo, useState } from "react";
import Link from "next/link";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import type { ColumnDef } from "@tanstack/react-table";
import { AlertCircle, Check, Copy, Eye, Play, Plus, RadioTower, RotateCw, Square, Shuffle, Video } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Checkbox } from "@/components/ui/checkbox";
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Sheet, SheetContent, SheetDescription, SheetHeader, SheetTitle } from "@/components/ui/sheet";
import { DataTable } from "@/components/tables/data-table";
import { DangerConfirm } from "@/components/admin/danger-confirm";
import { RoleGuard, guardedButtonProps } from "@/components/admin/role-guard";
import { StatusBadge } from "@/components/admin/status-badge";
import { APIError, apiPost } from "@/lib/api/client";
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
import { formatDateTimeInTimeZone } from "@/lib/timezone";
import { cn } from "@/lib/utils";
import type { Stream } from "@/types/domain";

type ResourceRow = Record<string, unknown>;
type SelectOption = { value: string; label: string; description?: string };

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
  const [selectedStream, setSelectedStream] = useState<Stream | null>(null);
  const [actionNotice, setActionNotice] = useState<{ tone: "success" | "error"; message: string } | null>(null);

  useEffect(() => {
    const syncFromHash = () => setCreateOpen(window.location.hash === "#create-stream");
    syncFromHash();
    window.addEventListener("hashchange", syncFromHash);
    return () => window.removeEventListener("hashchange", syncFromHash);
  }, []);

  const actionMutation = useMutation<unknown, Error, { path: string; streamName: string; actionLabel: string }>({
    mutationFn: ({ path }) => apiPost(path),
    onMutate: () => setActionNotice(null),
    onSuccess: async (_, action) => {
      await queryClient.invalidateQueries({ queryKey: ["streams"] });
      setActionNotice({ tone: "success", message: `${action.streamName}の${action.actionLabel}を受け付けました。状態が更新されるまでしばらくお待ちください。` });
    },
    onError: (error, action) => setActionNotice({ tone: "error", message: streamActionErrorMessage(error, action.actionLabel) }),
  });

  const superAdmin = currentUser.data?.user.roles?.includes("super_admin") === true;
  const can = (permission: string) => superAdmin || hasPermission(currentUser.data, permission);
  const canCreate = can("streams.create");
  const canStart = can("streams.start");
  const canStop = can("streams.stop");
  const canUpdate = can("streams.update");
  const streamRows = useMemo(
    () => [...createdStreams, ...(streams.data || []).filter((stream) => !createdStreams.some((created) => created.id === stream.id))],
    [createdStreams, streams.data],
  );
  const discordLabels = useOptionLabelMap(useResourceOptions("/discord/configs", ["name", "service_id", "id"]));
  const youtubeOutputLabels = useOptionLabelMap(useResourceOptions("/youtube/outputs", ["name", "id"]));
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
          {streamStatusAllowsStart(row.original.status) ? <RoleGuard allowed={canStart}>
            <DangerConfirm
              title={`${row.original.name} を開始しますか`}
              description="配信出力と録画を開始し、担当Nodeへ処理を送ります。対象の配信枠と出力先を確認してから実行してください。"
              onConfirm={() => actionMutation.mutate({ path: `/streams/${row.original.id}/start`, streamName: row.original.name, actionLabel: "開始" })}
              actionLabel="配信を開始"
            >
              <Button variant="outline" size="icon-sm" aria-label={t("start")} {...guardedButtonProps(canStart)} disabled={!canStart || actionMutation.isPending}><Play /></Button>
            </DangerConfirm>
          </RoleGuard> : null}
          {streamStatusAllowsStop(row.original.status) ? <RoleGuard allowed={canStop}>
            <DangerConfirm title={`${row.original.name} を停止しますか`} description="配信と録画を停止し、録画ファイルの保存処理へ進みます。視聴者への影響を確認してから実行してください。" onConfirm={() => actionMutation.mutate({ path: `/streams/${row.original.id}/stop`, streamName: row.original.name, actionLabel: "停止" })} actionLabel="配信を停止">
              <Button variant="outline" size="icon-sm" aria-label={t("stop")} {...guardedButtonProps(canStop)} disabled={!canStop || actionMutation.isPending}>
                <Square />
              </Button>
            </DangerConfirm>
          </RoleGuard> : null}
          <RoleGuard allowed={canUpdate}>
            <DangerConfirm title={`${row.original.name} の開始準備を再確認しますか`} description="担当Node、Discord、出力先、録画設定をもう一度確認します。現在の配信は停止しません。" onConfirm={() => actionMutation.mutate({ path: `/streams/${row.original.id}/start-readiness`, streamName: row.original.name, actionLabel: "開始準備の確認" })} actionLabel="準備を再確認">
              <Button variant="outline" size="icon-sm" aria-label="開始準備を再確認" {...guardedButtonProps(canUpdate)} disabled={!canUpdate || actionMutation.isPending}>
                <RotateCw />
              </Button>
            </DangerConfirm>
          </RoleGuard>
          <RoleGuard allowed={canUpdate}>
            <DangerConfirm title={`${row.original.name} のWorkerテストを実行しますか`} description="担当Workerへテストイベントを送ります。本番配信中は実行前に運用担当者へ確認してください。" onConfirm={() => actionMutation.mutate({ path: `/streams/${row.original.id}/worker-events/test`, streamName: row.original.name, actionLabel: "Workerテスト" })} actionLabel="テストを実行">
              <Button variant="outline" size="icon-sm" aria-label="Workerテストを実行" {...guardedButtonProps(canUpdate)} disabled={!canUpdate || actionMutation.isPending}>
                <Shuffle />
              </Button>
            </DangerConfirm>
          </RoleGuard>
        </div>
      ),
    },

    {
      id: "route",
      accessorFn: (stream) => compactList([stream.encoder_input_url, stream.input_source, stream.output_target, stream.youtube_output_id]).join(" "),
      header: "配信経路",
      cell: ({ row }) => (
        <div className="min-w-56 max-w-80 text-sm">
          <div className="flex items-center gap-1.5"><RadioTower className="size-3.5 shrink-0 text-muted-foreground" /><span className="truncate" title={safeDisplayURL(row.original.encoder_input_url || row.original.input_source)}>{safeDisplayURL(row.original.encoder_input_url || row.original.input_source) || "入力未設定"}</span></div>
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
          <DataTable columns={columns} data={streamRows} filterPlaceholder="配信名・状態・VC・URL・録画保存先で絞り込み" getRowId={(row) => row.id} minTableWidthClass="min-w-[1240px]" />
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
            canCreate={canCreate}
            canAssignEncoder={can("services.assign")}
            canAssignWorker={can("workers.assign")}
            onCreated={(stream) => {
              setCreatedStreams((current) => [stream, ...current.filter((item) => item.id !== stream.id)]);
              setActionNotice({ tone: "success", message: `${stream.name} を作成し、開始トリガーの待機を開始しました。VC、配信経路、録画設定を確認してください。` });
              setCreateOpen(false);
            }}
          />
        </SheetContent>
      </Sheet>

      <StreamDetailsDialog
        stream={selectedStream}
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

function StreamDetailsDialog({ stream, onOpenChange, discordLabels, youtubeOutputLabels, archiveAccountLabels, archiveDestinationLabels, archiveProfileLabels, overlayProfileLabels }: { stream: Stream | null; onOpenChange: (open: boolean) => void; discordLabels: Map<string, string>; youtubeOutputLabels: Map<string, string>; archiveAccountLabels: Map<string, string>; archiveDestinationLabels: Map<string, string>; archiveProfileLabels: Map<string, string>; overlayProfileLabels: Map<string, string> }) {
  if (!stream) return null;
  const recording = recordingDescriptor(stream);
  return (
    <Dialog open onOpenChange={onOpenChange}>
      <DialogContent className="max-h-[88vh] overflow-y-auto sm:max-w-3xl">
        <DialogHeader><DialogTitle>{stream.name}</DialogTitle><DialogDescription>配信前の確認と、配信中・終了後の状況確認に使う情報です。</DialogDescription></DialogHeader>
        <div className="grid gap-3 sm:grid-cols-2">
          <DetailGroup title="状態"><div className="flex flex-wrap items-center gap-2"><StatusBadge status={stream.status} /><span className={cn("inline-flex rounded-md border px-2 py-1 text-xs font-medium", recording.className)}>{recording.label}</span></div><p className="mt-2 text-xs text-muted-foreground">{recording.detail}</p></DetailGroup>
          <DetailGroup title="配信経路"><DetailLine label="入力URL" value={safeDisplayURL(stream.encoder_input_url || stream.input_source) || "未設定"} mono /><DetailLine label="YouTube出力" value={optionLabel(youtubeOutputLabels, stream.youtube_output_id) || stream.output_target || "未設定"} /></DetailGroup>
          <DetailGroup title="録画保存"><DetailLine label="設定" value={optionLabel(archiveProfileLabels, stream.archive_profile_id) || "未設定"} /><DetailLine label="保存先" value={optionLabel(archiveDestinationLabels, stream.archive_drive_destination_id) || optionLabel(archiveAccountLabels, stream.archive_oauth_account_id) || "未設定"} /><DetailLine label="ファイル名" value={stream.archive_file_name || "自動命名"} /><DetailLine label="フォルダー" value={stream.archive_folder_id_configured ? stream.archive_masked_folder_id || "設定済み" : "未設定"} /></DetailGroup>
          <DetailGroup title="自動開始"><DetailLine label="方式" value={stream.auto_start_trigger === "discord_voice_join" ? "Discord VC参加で自動開始" : "手動開始"} /><DetailLine label="BOT" value={optionLabel(discordLabels, stream.discord_config_id) || "未設定"} /><DetailLine label="VC" value={stream.discord_voice_channel_id || "未設定"} /><DetailLine label="Chat" value={stream.discord_text_channel_id || "未設定"} /></DetailGroup>
          <DetailGroup title="担当Node・映像設定"><DetailLine label="Worker" value={stream.assigned_worker_id || "未割当"} /><DetailLine label="Encoder" value={stream.assigned_encoder_id || "未割当"} /><DetailLine label="Watermark" value={optionLabel(overlayProfileLabels, stream.overlay_profile_id) || "OFF"} /></DetailGroup>
        </div>
        <div className="flex justify-end"><Button asChild variant="outline" size="sm"><Link href={`/admin/audit-logs/?q=${encodeURIComponent(stream.id)}`}>この配信枠の操作履歴を確認</Link></Button></div>
      </DialogContent>
    </Dialog>
  );
}

function DetailGroup({ title, children }: { title: string; children: ReactNode }) { return <section className="rounded-lg border bg-muted/15 p-4"><h3 className="mb-3 text-sm font-semibold">{title}</h3>{children}</section>; }
function DetailLine({ label, value, mono = false }: { label: string; value: string; mono?: boolean }) { return <div className="grid grid-cols-[6rem_minmax(0,1fr)] gap-2 border-b py-2 text-sm last:border-b-0"><span className="text-muted-foreground">{label}</span><span className={cn("min-w-0 break-all", mono && "font-mono text-xs")}>{value}</span></div>; }

function StreamSlotForm({
  className,
  canCreate,
  canAssignEncoder,
  canAssignWorker,
  onCreated,
}: {
  className?: string;
  canCreate: boolean;
  canAssignEncoder: boolean;
  canAssignWorker: boolean;
  onCreated: (stream: Stream) => void;
}) {
  const discordConfigs = useResourceOptions("/discord/configs", ["name", "service_id", "id"]);
  const youtubeOutputs = useResourceOptions("/youtube/outputs", ["name", "id"]);
  const oauthAccounts = useOAuthAccountOptions("drive");
  const encoderProfiles = useResourceOptions("/profiles/encoder", ["name", "id"]);
  const captionProfiles = useResourceOptions("/profiles/caption", ["name", "id"]);
  const overlayProfiles = useResourceOptions("/profiles/overlay", ["name", "id"]);
  const encoderNodes = useServiceOptions("encoder_recorder");
  const workerNodes = useServiceOptions("worker");
  const [name, setName] = useState("");
  const [discordConfigID, setDiscordConfigID] = useState(noneValue);
  const [guildID, setGuildID] = useState("");
  const [voiceChannelID, setVoiceChannelID] = useState("");
  const [textChannelID, setTextChannelID] = useState("");
  const [autoStartFromDiscord, setAutoStartFromDiscord] = useState(true);
  const [youtubeOutputID, setYouTubeOutputID] = useState(noneValue);
  const [archiveOAuthAccountID, setArchiveOAuthAccountID] = useState(noneValue);
  const [archiveFolderID, setArchiveFolderID] = useState("");
  const [archiveSharedDrive, setArchiveSharedDrive] = useState(false);
  const [archiveSharedDriveID, setArchiveSharedDriveID] = useState("");
  const [archiveFileName, setArchiveFileName] = useState("");
  const [archiveRetentionDays, setArchiveRetentionDays] = useState("30");
  const [encoderProfileID, setEncoderProfileID] = useState(noneValue);
  const [captionProfileID, setCaptionProfileID] = useState(noneValue);
  const [watermarkEnabled, setWatermarkEnabled] = useState(false);
  const [overlayProfileID, setOverlayProfileID] = useState(noneValue);
  const [encoderServiceID, setEncoderServiceID] = useState<string | null>(null);
  const [workerServiceID, setWorkerServiceID] = useState<string | null>(null);
  const [encoderInputURL, setEncoderInputURL] = useState("");
  const [message, setMessage] = useState("");

  const effectiveEncoderServiceID = encoderServiceID ?? singleOptionValue(encoderNodes);
  const effectiveWorkerServiceID = workerServiceID ?? singleOptionValue(workerNodes);

  const createStream = useMutation<Stream, Error, Record<string, unknown>>({
    mutationFn: (payload) => apiPost<Stream>("/streams", payload),
    onSuccess: (stream) => {
      setMessage(`${stream.name} を配信枠として作成しました。`);
      onCreated(stream);
    },
    onError: (error) => {
      setMessage(streamCreateErrorMessage(error));
    },
  });

  const payload = useMemo(
    () =>
      compactRecord({
        name,
        discord_config_id: selectedValue(discordConfigID),
        discord_guild_id: guildID,
        discord_voice_channel_id: voiceChannelID,
        discord_text_channel_id: textChannelID,
        auto_start_trigger: autoStartFromDiscord ? "discord_voice_join" : "",
        youtube_output_id: selectedValue(youtubeOutputID),
        archive_oauth_account_id: selectedValue(archiveOAuthAccountID),
        archive_folder_id: archiveFolderID,
        archive_shared_drive: archiveSharedDrive,
        archive_shared_drive_id: archiveSharedDriveID,
        archive_file_name: archiveFileName,
        archive_retention_days: positiveIntOrUndefined(archiveRetentionDays),
        encoder_profile_id: selectedValue(encoderProfileID),
        caption_profile_id: selectedValue(captionProfileID),
        overlay_profile_id: watermarkEnabled ? selectedValue(overlayProfileID) : "",
        encoder_service_id: canAssignEncoder ? selectedValue(effectiveEncoderServiceID) : "",
        worker_service_id: canAssignWorker ? selectedValue(effectiveWorkerServiceID) : "",
        encoder_input_url: encoderInputURL,
      }),
    [archiveFileName, archiveFolderID, archiveOAuthAccountID, archiveRetentionDays, archiveSharedDrive, archiveSharedDriveID, autoStartFromDiscord, canAssignEncoder, canAssignWorker, captionProfileID, discordConfigID, effectiveEncoderServiceID, effectiveWorkerServiceID, encoderInputURL, encoderProfileID, guildID, name, overlayProfileID, textChannelID, voiceChannelID, watermarkEnabled, youtubeOutputID],
  );
  const hasDiscordTarget = guildID.trim() !== "" || voiceChannelID.trim() !== "" || textChannelID.trim() !== "";
  const discordReady = !hasDiscordTarget || selectedValue(discordConfigID) !== "";
  const autoStartReady = !autoStartFromDiscord || (selectedValue(discordConfigID) !== "" && guildID.trim() !== "" && voiceChannelID.trim() !== "");
  const hasArchiveUploadTarget = selectedValue(archiveOAuthAccountID) !== "" || archiveFolderID.trim() !== "" || archiveSharedDrive || archiveSharedDriveID.trim() !== "" || archiveFileName.trim() !== "";
  const archiveReady = !hasArchiveUploadTarget || (selectedValue(archiveOAuthAccountID) !== "" && archiveFolderID.trim() !== "" && (!archiveSharedDrive || archiveSharedDriveID.trim() !== ""));
  const watermarkReady = !watermarkEnabled || selectedValue(overlayProfileID) !== "";
  const nodeAssignmentReady = !autoStartFromDiscord || ((!canAssignEncoder || selectedValue(effectiveEncoderServiceID) !== "") && (!canAssignWorker || selectedValue(effectiveWorkerServiceID) !== ""));
  const nodeAssignmentPermissionLimited = autoStartFromDiscord && (!canAssignEncoder || !canAssignWorker);
  const inputURLReady = externalInputURLIsValid(encoderInputURL);
  const archiveFileNameReady = !/[\\/]/.test(archiveFileName);
  const retentionDays = Number.parseInt(archiveRetentionDays, 10);
  const retentionReady = Number.isFinite(retentionDays) && retentionDays >= 1 && retentionDays <= 3650;

  return (
    <Card id="create-stream" className={className}>
      <CardHeader className="border-b">
        <CardTitle className="flex items-center gap-2">
          <Plus className="size-5" />
          配信枠を作成
        </CardTitle>
        <CardDescription>Discord VCの開始条件、配信経路、録画保存先を確認してから待機を開始します。</CardDescription>
      </CardHeader>
      <CardContent>
        <form
          className="space-y-4"
          onSubmit={(event) => {
            event.preventDefault();
            setMessage("");
            createStream.mutate(payload);
          }}
        >
          <FormSection title="基本情報" description="運用中に識別する配信枠名">
            <div className="max-w-xl">
              <TextField label="配信枠名" value={name} onChange={setName} placeholder="例: 商品発表会 メイン配信" required />
            </div>
          </FormSection>

          <FormSection title="開始条件と配信入力" description="自動開始に使うDiscordと担当Node">
            <label className="mb-3 flex min-h-10 items-center gap-2 rounded-md border bg-muted/20 px-3 text-sm">
              <Checkbox checked={autoStartFromDiscord} onCheckedChange={(value) => setAutoStartFromDiscord(value === true)} />
              Discord VCへの参加を検知して自動開始
            </label>
            <div className="grid gap-3 md:grid-cols-2">
              <SelectField label="Discord BOT設定" value={discordConfigID} onChange={setDiscordConfigID} options={[{ value: noneValue, label: "未選択" }, ...discordConfigs]} />
              <TextField label="DiscordサーバーID" value={guildID} onChange={setGuildID} />
              <TextField label="ボイスチャンネルID" value={voiceChannelID} onChange={setVoiceChannelID} />
              <TextField label="チャットチャンネルID" value={textChannelID} onChange={setTextChannelID} />
              {canAssignWorker ? <SelectField label="担当Worker Node" value={effectiveWorkerServiceID} onChange={setWorkerServiceID} options={[{ value: noneValue, label: "未選択" }, ...workerNodes]} /> : null}
              {canAssignEncoder ? <SelectField label="担当Encoder Node" value={effectiveEncoderServiceID} onChange={setEncoderServiceID} options={[{ value: noneValue, label: "未選択" }, ...encoderNodes]} /> : null}
              <TextField label="配信入力URL" value={encoderInputURL} onChange={setEncoderInputURL} placeholder="srt://source.example.com:9000" error={inputURLReady ? undefined : "SRT、RTMP、RTMPS、HTTP、HTTPSの公開URLを入力してください。"} />
            </div>
          </FormSection>

          <FormSection title="出力と録画" description="視聴先と録画ファイルの保存設定">
            <div className="grid gap-3 md:grid-cols-2">
              <SelectField label="YouTube出力" value={youtubeOutputID} onChange={setYouTubeOutputID} options={[{ value: noneValue, label: "未選択" }, ...youtubeOutputs]} />
              <SelectField label="録画用Googleアカウント" value={archiveOAuthAccountID} onChange={setArchiveOAuthAccountID} options={[{ value: noneValue, label: "未選択" }, ...oauthAccounts]} />
              <TextField label="Drive保存先フォルダーID" value={archiveFolderID} onChange={setArchiveFolderID} />
              <TextField label="録画ファイル名" value={archiveFileName} onChange={setArchiveFileName} placeholder="未入力なら 配信枠名-年月日.mp4" error={archiveFileNameReady ? undefined : "ファイル名に / または \\ は使えません。"} />
              <TextField label="Encoder内の保持日数" value={archiveRetentionDays} onChange={setArchiveRetentionDays} type="number" error={retentionReady ? undefined : "1日から3650日の範囲で入力してください。"} />
              <label className="flex min-h-10 items-center gap-2 self-end rounded-md border bg-muted/20 px-3 text-sm"><Checkbox checked={archiveSharedDrive} onCheckedChange={(value) => setArchiveSharedDrive(value === true)} />共有ドライブへ保存</label>
              {archiveSharedDrive ? <TextField label="共有ドライブID" value={archiveSharedDriveID} onChange={setArchiveSharedDriveID} /> : null}
            </div>
            {oauthAccounts.length === 0 ? <p className="mt-3 text-sm text-muted-foreground">Drive保存用途で接続されたGoogleアカウントがありません。</p> : null}
          </FormSection>

          <FormSection title="映像・字幕" description="配信品質と映像に適用する設定">
            <div className="grid gap-3 md:grid-cols-2">
              <SelectField label="エンコード設定" value={encoderProfileID} onChange={setEncoderProfileID} options={[{ value: noneValue, label: "未選択" }, ...encoderProfiles]} />
              <SelectField label="字幕設定" value={captionProfileID} onChange={setCaptionProfileID} options={[{ value: noneValue, label: "未選択" }, ...captionProfiles]} />
              <label className="flex min-h-10 items-center gap-2 rounded-md border bg-muted/20 px-3 text-sm"><Checkbox checked={watermarkEnabled} onCheckedChange={(value) => setWatermarkEnabled(value === true)} />ウォーターマークを使用</label>
              <SelectField label="ウォーターマーク設定" value={overlayProfileID} onChange={setOverlayProfileID} options={[{ value: noneValue, label: "未選択" }, ...overlayProfiles]} disabled={!watermarkEnabled} />
            </div>
          </FormSection>

          {hasDiscordTarget && !discordReady ? (
            <div className="flex gap-2 rounded-md border border-amber-200 bg-amber-50 p-3 text-sm text-amber-800">
              <AlertCircle className="mt-0.5 size-4 shrink-0" />
              Discordのサーバーやチャンネルを指定する場合は、使用するDiscord BOT設定も選択してください。
            </div>
          ) : null}
          {autoStartFromDiscord && !autoStartReady ? (
            <div className="flex gap-2 rounded-md border border-amber-200 bg-amber-50 p-3 text-sm text-amber-800">
              <AlertCircle className="mt-0.5 size-4 shrink-0" />
              自動開始を使うには、Discord BOT設定、サーバーID、ボイスチャンネルIDが必要です。
            </div>
          ) : null}
          {hasArchiveUploadTarget && !archiveReady ? (
            <div className="flex gap-2 rounded-md border border-amber-200 bg-amber-50 p-3 text-sm text-amber-800">
              <AlertCircle className="mt-0.5 size-4 shrink-0" />
              Driveへ録画を保存するには、Googleアカウントと保存先フォルダーIDが必要です。共有ドライブを使う場合は共有ドライブIDも入力してください。
            </div>
          ) : null}
          {watermarkEnabled && !watermarkReady ? (
            <div className="flex gap-2 rounded-md border border-amber-200 bg-amber-50 p-3 text-sm text-amber-800">
              <AlertCircle className="mt-0.5 size-4 shrink-0" />
              ウォーターマークを使う場合は、ウォーターマーク設定を選択してください。
            </div>
          ) : null}
          {autoStartFromDiscord && canAssignEncoder && canAssignWorker && !nodeAssignmentReady ? (
            <div className="flex gap-2 rounded-md border border-amber-200 bg-amber-50 p-3 text-sm text-amber-800">
              <AlertCircle className="mt-0.5 size-4 shrink-0" />
              自動開始する配信枠には、担当Encoder Nodeと担当Worker Nodeを選択してください。
            </div>
          ) : null}
          {nodeAssignmentPermissionLimited ? (
            <div className="flex gap-2 rounded-md border border-amber-200 bg-amber-50 p-3 text-sm text-amber-800">
              <AlertCircle className="mt-0.5 size-4 shrink-0" />
              Nodeを割り当てる権限がありません。管理者に依頼し、サービス稼働画面で割り当てを確認してください。
            </div>
          ) : null}
          {message ? <div aria-live="polite" className="rounded-md border bg-muted/30 p-3 text-sm text-muted-foreground">{message}</div> : null}
          {!canCreate ? <p className="text-sm text-red-600">配信枠を作成する権限がありません。</p> : null}
          <div className="flex justify-end">
            <Button type="submit" disabled={!canCreate || createStream.isPending || name.trim() === "" || !discordReady || !autoStartReady || !archiveReady || !watermarkReady || !nodeAssignmentReady || !inputURLReady || !archiveFileNameReady || !retentionReady}>
              <Plus className="size-4" />
              {createStream.isPending ? "作成中..." : "配信枠を作成"}
            </Button>
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
}: {
  label: string;
  value: string;
  onChange: (value: string) => void;
  placeholder?: string;
  type?: string;
  required?: boolean;
  error?: string;
}) {
  return (
    <label className="grid gap-1.5 text-sm">
      <span className="font-medium">{label}</span>
      <Input type={type} value={value} onChange={(event) => onChange(event.target.value)} placeholder={placeholder} required={required} aria-invalid={Boolean(error)} />
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
            <SelectItem key={option.value} value={option.value} textValue={option.label}>
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

function useServiceOptions(serviceType: string) {
  const query = useServiceHealth();
  const rows = useMemo(() => query.data || [], [query.data]);
  return useMemo(
    () =>
      rows
        .filter((row) => row.service_type === serviceType)
        .map((row) => {
          const value = row.service_id || row.id;
          const label = firstNonEmpty(row.service_name, row.service_id || row.id);
          return { value, label };
        })
        .filter((option) => option.value),
    [rows, serviceType],
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

function compactRecord(record: Record<string, unknown>) {
  return Object.fromEntries(Object.entries(record).filter(([, value]) => value !== "" && value !== undefined));
}

function selectedValue(value: string) {
  return value === noneValue ? "" : value;
}

function positiveIntOrUndefined(value: string) {
  const parsed = Number.parseInt(value, 10);
  return Number.isFinite(parsed) && parsed > 0 ? parsed : undefined;
}

function singleOptionValue(options: SelectOption[]) {
  return options.length === 1 ? options[0].value : noneValue;
}

function streamCreateErrorMessage(error: unknown) {
  if (error instanceof APIError) {
    const messages: Record<string, string> = {
      name_required: "配信枠名を入力してください。",
      auto_start_trigger_invalid: "自動開始条件が無効です。Discord VC参加で自動開始を使う場合は画面のチェック項目から設定してください。",
      auto_start_discord_required: "Discord VC参加で自動開始する場合は、Discord BOT設定、Guild ID、VC Channel IDを指定してください。",
      discord_config_required: "Discord Guild/VC/Chatを指定する場合はDiscord BOT設定を選択してください。",
      discord_config_not_found: "選択したDiscord BOT設定が見つかりません。",
      encoder_input_url_blocked: "外部入力URLが許可されていません。公開されたSRT/RTMP/HTTPS入力を指定してください。",
      archive_oauth_account_required: "Archiveを設定する場合はOAuth accountを選択してください。",
      archive_folder_id_required: "Archiveを設定する場合はDrive Folder IDを入力してください。",
      archive_shared_drive_id_required: "共有ドライブを使う場合は共有ドライブIDを入力してください。",
      drive_oauth_account_unavailable: "選択したOAuth accountがGoogle Drive保存に利用できません。接続状態とDrive scopeを確認してください。",
      secret_encryption_key_required: "Control Panelの暗号化キーが未設定のため、Drive Folder IDを保存できません。",
      encoder_service_not_found: "選択したEncoder Nodeが見つかりません。",
      worker_service_not_found: "選択したWorker Nodeが見つかりません。",
      encoder_service_type_invalid: "選択したEncoder Nodeの種別が正しくありません。",
      worker_service_type_invalid: "選択したWorker Nodeの種別が正しくありません。",
      service_registry_not_configured: "Node登録情報を取得できませんでした。",
      assign_service_failed: "Primary Nodeの割り当てに失敗しました。",
      permission_denied: "Primary Nodeを保存する権限がありません。",
    };
    return messages[error.code || ""] || `作成に失敗しました。${error.code || error.message}`;
  }
  if (error instanceof Error) return `作成に失敗しました。${error.message}`;
  return "作成に失敗しました。";
}

function streamActionErrorMessage(error: unknown, actionLabel: string) {
  if (error instanceof APIError) {
    if (error.status === 403) return `${actionLabel}を実行する権限がありません。管理者に操作権限を確認してください。`;
    if (error.status === 404) return `対象の配信枠が見つかりません。一覧を更新してからもう一度確認してください。`;
    if (error.status === 409) return `${actionLabel}できない状態です。配信状態を更新し、開始中・停止中の処理が終わってから再試行してください。`;
    if (error.status >= 500) return `${actionLabel}を完了できませんでした。担当Nodeの接続状態を確認し、配信ログを確認してから再試行してください。`;
  }
  return `${actionLabel}を完了できませんでした。通信状態と配信ログを確認してから再試行してください。`;
}

function streamStatusAllowsStart(status: Stream["status"]) {
  return ["created", "draft", "scheduled", "ready", "failed"].includes(String(status).toLowerCase());
}

function streamStatusAllowsStop(status: Stream["status"]) {
  return ["starting", "live", "failed"].includes(String(status).toLowerCase());
}

function externalInputURLIsValid(value: string) {
  const input = value.trim();
  if (!input) return true;
  try {
    const url = new URL(input);
    return ["srt:", "rtmp:", "rtmps:", "http:", "https:"].includes(url.protocol) && Boolean(url.hostname) && url.hostname.toLowerCase() !== "localhost" && !url.username && !url.password && !url.hash;
  } catch {
    return false;
  }
}

function formatDateTime(value?: string, timezone?: string) {
  return formatDateTimeInTimeZone(value, timezone, { month: "2-digit", day: "2-digit", hour: "2-digit", minute: "2-digit" });
}
