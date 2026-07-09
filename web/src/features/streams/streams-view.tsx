"use client";

import { useMemo, useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import type { ColumnDef } from "@tanstack/react-table";
import { AlertCircle, Check, Copy, Eye, Play, Plus, RotateCw, Square, Shuffle } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Checkbox } from "@/components/ui/checkbox";
import { Input } from "@/components/ui/input";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { DataTable } from "@/components/tables/data-table";
import { DangerConfirm } from "@/components/admin/danger-confirm";
import { RoleGuard, guardedButtonProps } from "@/components/admin/role-guard";
import { StatusBadge } from "@/components/admin/status-badge";
import { APIError, apiPost } from "@/lib/api/client";
import { hasPermission } from "@/lib/auth/permissions";
import { useAppSettings, useCurrentUser, useResourceData, useServiceHealth, useStreams } from "@/features/queries";
import { useI18n } from "@/components/admin/i18n-provider";
import { formatDateTimeInTimeZone } from "@/lib/timezone";
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

  const actionMutation = useMutation({
    mutationFn: ({ path }: { path: string }) => apiPost(path),
    onSuccess: async () => {
      await queryClient.invalidateQueries({ queryKey: ["streams"] });
    },
  });

  const canCreate = hasPermission(currentUser.data, "streams.create");
  const canStart = hasPermission(currentUser.data, "streams.start");
  const canStop = hasPermission(currentUser.data, "streams.stop");
  const canUpdate = hasPermission(currentUser.data, "streams.update");
  const streamRows = useMemo(
    () => [...createdStreams, ...(streams.data || []).filter((stream) => !createdStreams.some((created) => created.id === stream.id))],
    [createdStreams, streams.data],
  );
  const discordLabels = useOptionLabelMap(useResourceOptions("/discord/configs", ["name", "service_id", "id"]));
  const youtubeOutputLabels = useOptionLabelMap(useResourceOptions("/youtube/outputs", ["name", "id"]));
  const archiveAccountLabels = useOptionLabelMap(useOAuthAccountOptions());
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
        <div className="min-w-56">
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
      header: "Discord",
      cell: ({ row }) => (
        <div className="text-sm">
          <div>{optionLabel(discordLabels, row.original.discord_config_id) || "-"}</div>
          <div className="text-muted-foreground">VC {row.original.discord_voice_channel_id || "-"}</div>
          <div className="text-muted-foreground">Chat {row.original.discord_text_channel_id || "-"}</div>
          <div className="text-muted-foreground">{row.original.auto_start_trigger === "discord_voice_join" ? "VC参加で自動開始" : "手動開始"}</div>
        </div>
      ),
    },
    {
      id: "outputs",
      accessorFn: (stream) =>
        compactList([
          optionLabel(youtubeOutputLabels, stream.youtube_output_id),
          stream.youtube_output_id,
          stream.archive_file_name,
          optionLabel(archiveDestinationLabels, stream.archive_drive_destination_id),
          stream.archive_drive_destination_id,
          optionLabel(archiveAccountLabels, stream.archive_oauth_account_id),
          stream.archive_oauth_account_id,
          optionLabel(archiveProfileLabels, stream.archive_profile_id),
          stream.archive_profile_id,
          stream.archive_masked_folder_id,
          optionLabel(overlayProfileLabels, stream.overlay_profile_id),
          stream.overlay_profile_id,
        ]).join(" "),
      header: "出力 / 保存",
      cell: ({ row }) => (
        <div className="text-sm">
          <div>YouTube {optionLabel(youtubeOutputLabels, row.original.youtube_output_id) || "-"}</div>
          <div className="text-muted-foreground">
            Archive{" "}
            {row.original.archive_file_name ||
              optionLabel(archiveDestinationLabels, row.original.archive_drive_destination_id) ||
              optionLabel(archiveAccountLabels, row.original.archive_oauth_account_id) ||
              optionLabel(archiveProfileLabels, row.original.archive_profile_id) ||
              "-"}
          </div>
          <div className="text-muted-foreground">Drive {row.original.archive_folder_id_configured ? row.original.archive_masked_folder_id || "設定済み" : "-"}</div>
          <div className="text-muted-foreground">Watermark {optionLabel(overlayProfileLabels, row.original.overlay_profile_id) || "OFF"}</div>
        </div>
      ),
    },
    {
      id: "input",
      header: t("input"),
      cell: ({ row }) => <span className="break-all text-sm">{row.original.encoder_input_url || row.original.input_source || "-"}</span>,
    },
    {
      id: "schedule",
      header: t("scheduledTime"),
      cell: ({ row }) => <span className="text-sm">{formatTimeRange(row.original.scheduled_start_at, row.original.scheduled_end_at, timezone)}</span>,
    },
    {
      id: "updated",
      header: "更新",
      cell: ({ row }) => <span className="text-sm">{formatDateTime(row.original.updated_at || row.original.created_at, timezone)}</span>,
    },
    {
      id: "actions",
      header: t("actions"),
      cell: ({ row }) => (
        <div className="flex min-w-44 flex-nowrap gap-1">
          <Button variant="outline" size="icon-sm" aria-label={t("details")}>
            <Eye />
          </Button>
          <RoleGuard allowed={canStart}>
            <Button
              variant="outline"
              size="icon-sm"
              aria-label={t("start")}
              {...guardedButtonProps(canStart)}
              onClick={() => actionMutation.mutate({ path: `/streams/${row.original.id}/start` })}
            >
              <Play />
            </Button>
          </RoleGuard>
          <RoleGuard allowed={canStop}>
            <DangerConfirm title={`${row.original.name} を停止しますか`} onConfirm={() => actionMutation.mutate({ path: `/streams/${row.original.id}/stop` })} actionLabel={t("stop")}>
              <Button variant="outline" size="icon-sm" aria-label={t("stop")} {...guardedButtonProps(canStop)}>
                <Square />
              </Button>
            </DangerConfirm>
          </RoleGuard>
          <RoleGuard allowed={canUpdate}>
            <DangerConfirm title={`${row.original.name} の開始準備を再実行しますか`} onConfirm={() => actionMutation.mutate({ path: `/streams/${row.original.id}/start-readiness` })} actionLabel={t("restart")}>
              <Button variant="outline" size="icon-sm" aria-label={t("restart")} {...guardedButtonProps(canUpdate)}>
                <RotateCw />
              </Button>
            </DangerConfirm>
          </RoleGuard>
          <RoleGuard allowed={canUpdate}>
            <DangerConfirm title={`${row.original.name} のWorkerイベントを送信しますか`} onConfirm={() => actionMutation.mutate({ path: `/streams/${row.original.id}/worker-events/test` })} actionLabel={t("switchWorker")}>
              <Button variant="outline" size="icon-sm" aria-label={t("switchWorker")} {...guardedButtonProps(canUpdate)}>
                <Shuffle />
              </Button>
            </DangerConfirm>
          </RoleGuard>
        </div>
      ),
    },
  ];

  return (
    <div className="space-y-4">
      <StreamSlotForm
        canCreate={canCreate}
        canAssignEncoder={hasPermission(currentUser.data, "services.assign")}
        canAssignWorker={hasPermission(currentUser.data, "workers.assign")}
        onCreated={(stream) => setCreatedStreams((current) => [stream, ...current.filter((item) => item.id !== stream.id)])}
      />
      <Card>
        <CardHeader>
          <CardTitle>{t("streams")}</CardTitle>
          <CardDescription>Discord VC待機、YouTube出力、録画保存を紐づけた配信枠を確認します。</CardDescription>
        </CardHeader>
        <CardContent>
          <DataTable columns={columns} data={streamRows} filterPlaceholder="配信名・Discord・YouTube・Archive・状態で絞り込み" getRowId={(row) => row.id} />
        </CardContent>
      </Card>
    </div>
  );
}

function StreamSlotForm({
  canCreate,
  canAssignEncoder,
  canAssignWorker,
  onCreated,
}: {
  canCreate: boolean;
  canAssignEncoder: boolean;
  canAssignWorker: boolean;
  onCreated: (stream: Stream) => void;
}) {
  const discordConfigs = useResourceOptions("/discord/configs", ["name", "service_id", "id"]);
  const youtubeOutputs = useResourceOptions("/youtube/outputs", ["name", "id"]);
  const oauthAccounts = useOAuthAccountOptions();
  const encoderProfiles = useResourceOptions("/profiles/encoder", ["name", "id"]);
  const captionProfiles = useResourceOptions("/profiles/caption", ["name", "id"]);
  const overlayProfiles = useResourceOptions("/profiles/overlay", ["name", "id"]);
  const encoderNodes = useServiceOptions("encoder_recorder");
  const workerNodes = useServiceOptions("worker");
  const [name, setName] = useState("朝の地域情報");
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
  const [scheduledStartAt, setScheduledStartAt] = useState("");
  const [scheduledEndAt, setScheduledEndAt] = useState("");
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
        scheduled_start_at: dateTimeLocalToISO(scheduledStartAt),
        scheduled_end_at: dateTimeLocalToISO(scheduledEndAt),
      }),
    [archiveFileName, archiveFolderID, archiveOAuthAccountID, archiveRetentionDays, archiveSharedDrive, archiveSharedDriveID, autoStartFromDiscord, canAssignEncoder, canAssignWorker, captionProfileID, discordConfigID, effectiveEncoderServiceID, effectiveWorkerServiceID, encoderInputURL, encoderProfileID, guildID, name, overlayProfileID, scheduledEndAt, scheduledStartAt, textChannelID, voiceChannelID, watermarkEnabled, youtubeOutputID],
  );
  const hasDiscordTarget = guildID.trim() !== "" || voiceChannelID.trim() !== "" || textChannelID.trim() !== "";
  const discordReady = !hasDiscordTarget || selectedValue(discordConfigID) !== "";
  const autoStartReady = !autoStartFromDiscord || (selectedValue(discordConfigID) !== "" && guildID.trim() !== "" && voiceChannelID.trim() !== "");
  const hasArchiveUploadTarget = selectedValue(archiveOAuthAccountID) !== "" || archiveFolderID.trim() !== "" || archiveSharedDrive || archiveSharedDriveID.trim() !== "" || archiveFileName.trim() !== "";
  const archiveReady = !hasArchiveUploadTarget || (selectedValue(archiveOAuthAccountID) !== "" && archiveFolderID.trim() !== "" && (!archiveSharedDrive || archiveSharedDriveID.trim() !== ""));
  const watermarkReady = !watermarkEnabled || selectedValue(overlayProfileID) !== "";
  const nodeAssignmentReady = !autoStartFromDiscord || ((!canAssignEncoder || selectedValue(effectiveEncoderServiceID) !== "") && (!canAssignWorker || selectedValue(effectiveWorkerServiceID) !== ""));
  const nodeAssignmentPermissionLimited = autoStartFromDiscord && (!canAssignEncoder || !canAssignWorker);

  return (
    <Card>
      <CardHeader>
        <CardTitle className="flex items-center gap-2">
          <Plus className="size-5" />
          配信枠を作成
        </CardTitle>
        <CardDescription>Discord VCへの参加を起点に自動開始する前提で、必要なNode設定と出力先を配信枠にまとめます。</CardDescription>
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
          <div className="grid gap-3 md:grid-cols-2 xl:grid-cols-3">
            <TextField label="配信枠名" value={name} onChange={setName} required />
            <TextField label="予定開始" type="datetime-local" value={scheduledStartAt} onChange={setScheduledStartAt} />
            <TextField label="予定終了" type="datetime-local" value={scheduledEndAt} onChange={setScheduledEndAt} />
            <SelectField label="Discord BOT設定" value={discordConfigID} onChange={setDiscordConfigID} options={[{ value: noneValue, label: "未選択" }, ...discordConfigs]} />
            <TextField label="Discord Guild ID" value={guildID} onChange={setGuildID} />
            <TextField label="VC Channel ID" value={voiceChannelID} onChange={setVoiceChannelID} />
            <TextField label="Chat Channel ID" value={textChannelID} onChange={setTextChannelID} />
            <SelectField label="YouTube output" value={youtubeOutputID} onChange={setYouTubeOutputID} options={[{ value: noneValue, label: "未選択" }, ...youtubeOutputs]} />
            <SelectField label="Archive OAuth account" value={archiveOAuthAccountID} onChange={setArchiveOAuthAccountID} options={[{ value: noneValue, label: "未選択" }, ...oauthAccounts]} />
            <TextField label="Drive Folder ID" value={archiveFolderID} onChange={setArchiveFolderID} />
            <TextField label="保存ファイル名" value={archiveFileName} onChange={setArchiveFileName} placeholder="未入力なら 配信枠名-年月日.mp4" />
            <TextField label="ローカル保持日数" value={archiveRetentionDays} onChange={setArchiveRetentionDays} type="number" />
            <SelectField label="Encoder profile" value={encoderProfileID} onChange={setEncoderProfileID} options={[{ value: noneValue, label: "未選択" }, ...encoderProfiles]} />
            <SelectField label="Caption profile" value={captionProfileID} onChange={setCaptionProfileID} options={[{ value: noneValue, label: "未選択" }, ...captionProfiles]} />
            <SelectField label="ウォーターマーク設定" value={overlayProfileID} onChange={setOverlayProfileID} options={[{ value: noneValue, label: "未選択" }, ...overlayProfiles]} disabled={!watermarkEnabled} />
            {canAssignEncoder ? <SelectField label="Primary Encoder Node" value={effectiveEncoderServiceID} onChange={setEncoderServiceID} options={[{ value: noneValue, label: "未選択" }, ...encoderNodes]} /> : null}
            {canAssignWorker ? <SelectField label="Primary Worker Node" value={effectiveWorkerServiceID} onChange={setWorkerServiceID} options={[{ value: noneValue, label: "未選択" }, ...workerNodes]} /> : null}
            <TextField label="外部入力URL" value={encoderInputURL} onChange={setEncoderInputURL} placeholder="srt://source.example.com:9000" />
            <label className="flex min-h-10 items-center gap-2 self-end text-sm">
              <Checkbox checked={autoStartFromDiscord} onCheckedChange={(value) => setAutoStartFromDiscord(value === true)} />
              Discord VC参加で自動開始
            </label>
            <label className="flex min-h-10 items-center gap-2 self-end text-sm">
              <Checkbox checked={archiveSharedDrive} onCheckedChange={(value) => setArchiveSharedDrive(value === true)} />
              共有ドライブIDを使う
            </label>
            <label className="flex min-h-10 items-center gap-2 self-end text-sm">
              <Checkbox checked={watermarkEnabled} onCheckedChange={(value) => setWatermarkEnabled(value === true)} />
              ウォーターマークを使う
            </label>
            <TextField label="共有ドライブID" value={archiveSharedDriveID} onChange={setArchiveSharedDriveID} />
          </div>

          {hasDiscordTarget && !discordReady ? (
            <div className="flex gap-2 rounded-md border border-amber-200 bg-amber-50 p-3 text-sm text-amber-800">
              <AlertCircle className="mt-0.5 size-4 shrink-0" />
              Discord Guild/VC/Chatを指定する場合は、Discord BOT設定も選択してください。
            </div>
          ) : null}
          {autoStartFromDiscord && !autoStartReady ? (
            <div className="flex gap-2 rounded-md border border-amber-200 bg-amber-50 p-3 text-sm text-amber-800">
              <AlertCircle className="mt-0.5 size-4 shrink-0" />
              Discord VC参加で自動開始する場合は、Discord BOT設定、Guild ID、VC Channel IDを指定してください。
            </div>
          ) : null}
          {hasArchiveUploadTarget && !archiveReady ? (
            <div className="flex gap-2 rounded-md border border-amber-200 bg-amber-50 p-3 text-sm text-amber-800">
              <AlertCircle className="mt-0.5 size-4 shrink-0" />
              Archiveを設定する場合は、OAuth accountとDrive Folder IDを指定してください。共有ドライブを使う場合は共有ドライブIDも必要です。
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
              自動開始する待機枠にはPrimary Encoder NodeとPrimary Worker Nodeを選択してください。
            </div>
          ) : null}
          {nodeAssignmentPermissionLimited ? (
            <div className="flex gap-2 rounded-md border border-amber-200 bg-amber-50 p-3 text-sm text-amber-800">
              <AlertCircle className="mt-0.5 size-4 shrink-0" />
              Node割当権限がないため、この画面ではPrimary Nodeを保存できません。Service Healthで割り当てを確認してください。
            </div>
          ) : null}
          {message ? <div className="rounded-md border bg-muted/30 p-3 text-sm text-muted-foreground">{message}</div> : null}
          {!canCreate ? <p className="text-sm text-red-600">配信枠を作成する権限がありません。</p> : null}
          <div className="flex justify-end">
            <Button type="submit" disabled={!canCreate || createStream.isPending || name.trim() === "" || !discordReady || !autoStartReady || !archiveReady || !watermarkReady || !nodeAssignmentReady}>
              <Plus className="size-4" />
              {createStream.isPending ? "作成中..." : "待機用の配信枠を作成"}
            </Button>
          </div>
        </form>
      </CardContent>
    </Card>
  );
}

function TextField({
  label,
  value,
  onChange,
  placeholder,
  type = "text",
  required,
}: {
  label: string;
  value: string;
  onChange: (value: string) => void;
  placeholder?: string;
  type?: string;
  required?: boolean;
}) {
  return (
    <label className="grid gap-1.5 text-sm">
      <span className="font-medium">{label}</span>
      <Input type={type} value={value} onChange={(event) => onChange(event.target.value)} placeholder={placeholder} required={required} />
    </label>
  );
}

function SelectField({ label, value, onChange, options, disabled }: { label: string; value: string; onChange: (value: string) => void; options: SelectOption[]; disabled?: boolean }) {
  return (
    <label className="grid gap-1.5 text-sm">
      <span className="font-medium">{label}</span>
      <Select value={value} onValueChange={onChange} disabled={disabled}>
        <SelectTrigger className="w-full" disabled={disabled}>
          <SelectValue />
        </SelectTrigger>
        <SelectContent>
          {options.map((option) => (
            <SelectItem key={option.value} value={option.value} textValue={option.label}>
              <span className="grid gap-0.5">
                <span>{option.label}</span>
                {option.description ? <span className="text-xs text-muted-foreground">{option.description}</span> : null}
              </span>
            </SelectItem>
          ))}
        </SelectContent>
      </Select>
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

function useOAuthAccountOptions() {
  const query = useResourceData<unknown>("/integrations/oauth-accounts");
  const rows = useMemo(() => normalizeRows(query.data), [query.data]);
  return useMemo(
    () =>
      rows
        .map((row) => {
          const value = rowString(row, ["id"]);
          const provider = rowString(row, ["provider_type"]);
          return {
            value,
            label: oauthAccountLabel(row),
            description: provider ? providerTypeLabel(provider) : undefined,
          };
        })
        .filter((option) => option.value),
    [rows],
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

function oauthAccountLabel(row: ResourceRow) {
  const email = rowString(row, ["email"]).toLowerCase();
  for (const key of ["account_label", "display_name"]) {
    const value = rowString(row, [key]);
    if (value && value.toLowerCase() !== email) return value;
  }
  return `${providerTypeLabel(rowString(row, ["provider_type"]))}接続アカウント`;
}

function providerTypeLabel(providerType: string) {
  switch (providerType.trim().toLowerCase()) {
    case "google":
      return "Google";
    case "github":
      return "GitHub";
    case "discord":
      return "Discord";
    default:
      return providerType.trim() || "OAuth";
  }
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

function dateTimeLocalToISO(value: string) {
  if (!value.trim()) return "";
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return value;
  return date.toISOString();
}

function streamCreateErrorMessage(error: unknown) {
  if (error instanceof APIError) {
    const messages: Record<string, string> = {
      name_required: "配信枠名を入力してください。",
      schedule_time_invalid: "予定日時の形式が正しくありません。",
      schedule_end_before_start: "予定終了は予定開始より後にしてください。",
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

function formatTimeRange(start?: string, end?: string, timezone?: string) {
  if (!start && !end) return "-";
  return `${formatDateTime(start, timezone)} - ${formatDateTime(end, timezone)}`;
}

function formatDateTime(value?: string, timezone?: string) {
  return formatDateTimeInTimeZone(value, timezone, { month: "2-digit", day: "2-digit", hour: "2-digit", minute: "2-digit" });
}
