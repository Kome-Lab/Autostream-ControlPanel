"use client";
import { useUICopy } from "@/lib/i18n/ui-v2/use-ui-copy";


import { useNonSecretDraft, useExistingDraft } from "@/components/forms/draft-exit";
import { type ReactNode, useCallback, useLayoutEffect, useMemo, useRef, useState } from "react";
import Link from "next/link";
import { AlertCircle, Pencil, Plus } from "lucide-react";

import { useI18n } from "@/components/admin/i18n-provider";
import { Field, FieldGroup } from "@/components/forms/field";
import { FormFooter } from "@/components/forms/form-footer";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Checkbox } from "@/components/ui/checkbox";
import { Input } from "@/components/ui/input";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { StreamActionControl, type StreamActionControlHandle } from "@/features/streams/stream-action-control";
import type { StreamActionController, StreamActionExecutionResult } from "@/features/streams/stream-action-controller";
import type { StreamActionIntent } from "@/features/streams/stream-action-descriptors";
import { isStreamValue, streamActionBlockedMessage } from "@/features/streams/stream-action-feedback";
import { StreamVisualSettingsSection, type StreamCreateVisualState } from "@/features/streams/stream-visual-settings-section";
import {
  noneValue,
  optionOrNone,
  selectedValue,
  useResourceOptions,
  useServiceOptions,
  type StreamSelectOption,
} from "@/features/streams/stream-view-options";
import { buildStreamCreatePayload, buildStreamSettingsPayload, streamScheduleInputValue, streamScheduleRFC3339 } from "@/lib/stream-create";
import type { Stream } from "@/types/domain";

type Props = {
  stream?: Stream | null;
  className?: string;
  actionController: StreamActionController;
  onActionResult: (result: StreamActionExecutionResult, intent: StreamActionIntent) => void;
  canCreate: boolean;
  canUpdate: boolean;
  canAssignEncoder: boolean;
  canAssignWorker: boolean;
  onSaved: (stream: Stream) => void;
};

export function StreamSlotForm({ stream, className, actionController, onActionResult, canCreate, canUpdate, canAssignEncoder, canAssignWorker, onSaved }: Props) {
  const uiText = useUICopy();
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
  const [autoStartFromDiscord, setAutoStartFromDiscord] = useState(editing ? stream?.auto_start_trigger === "discord_voice_join" : true);
  const [youtubeOutputID, setYouTubeOutputID] = useState(optionOrNone(stream?.youtube_output_id));
  const [archiveProfileID, setArchiveProfileID] = useState(optionOrNone(stream?.archive_profile_id));
  const [encoderProfileID, setEncoderProfileID] = useState(optionOrNone(stream?.encoder_profile_id));
  const [captionProfileID, setCaptionProfileID] = useState(optionOrNone(stream?.caption_profile_id));
  const [watermarkEnabled, setWatermarkEnabled] = useState(Boolean(stream?.overlay_profile_id));
  const [overlayProfileID, setOverlayProfileID] = useState(optionOrNone(stream?.overlay_profile_id));
  const [encoderAudioGainDB, setEncoderAudioGainDB] = useState(String(stream?.encoder_audio_gain_db ?? 0));
  // null preserves backend ownership; choosing the empty option is an explicit unassign.
  const [encoderServiceID, setEncoderServiceID] = useState<string | null>(null);
  const [workerServiceID, setWorkerServiceID] = useState<string | null>(null);
  const [scheduledStartAt, setScheduledStartAt] = useState(() => streamScheduleInputValue(stream?.scheduled_start_at));
  const [createVisualState, setCreateVisualState] = useState<StreamCreateVisualState>({ extension: {}, ready: false, discordTargetReady: false });
  const [message, setMessage] = useState("");
  const effectiveEncoderServiceID = encoderServiceID ?? optionOrNone(stream?.assigned_encoder_id);
  const effectiveWorkerServiceID = workerServiceID ?? optionOrNone(stream?.assigned_worker_id);

  const payload = useMemo(
    () => liveEditing ? {
      encoder_audio_gain_db: Number(encoderAudioGainDB),
      overlay_profile_id: watermarkEnabled ? selectedValue(overlayProfileID) : "",
    } : {
      ...(editing ? buildStreamSettingsPayload : buildStreamCreatePayload)({
      name,
      discordConfigID: selectedValue(discordConfigID),
      autoStartFromDiscord,
      youtubeOutputID: selectedValue(youtubeOutputID),
      archiveProfileID: selectedValue(archiveProfileID),
      encoderProfileID: selectedValue(encoderProfileID),
      captionProfileID: selectedValue(captionProfileID),
      watermarkEnabled,
      overlayProfileID: selectedValue(overlayProfileID),
      encoderAudioGainDB: Number(encoderAudioGainDB),
      encoderServiceID: canAssignEncoder && encoderServiceID !== null ? selectedValue(effectiveEncoderServiceID) : undefined,
      workerServiceID: canAssignWorker && workerServiceID !== null ? selectedValue(effectiveWorkerServiceID) : undefined,
      scheduledStartAt: streamScheduleRFC3339(scheduledStartAt),
      scheduledEndAt: stream?.scheduled_end_at || "",
        encoderInputURL: stream?.encoder_input_url || "",
      }),
      ...(!editing ? createVisualState.extension : {}),
    },
    [archiveProfileID, autoStartFromDiscord, canAssignEncoder, canAssignWorker, captionProfileID, createVisualState.extension, discordConfigID, editing, effectiveEncoderServiceID, effectiveWorkerServiceID, encoderAudioGainDB, encoderProfileID, encoderServiceID, liveEditing, name, overlayProfileID, scheduledStartAt, stream?.encoder_input_url, stream?.scheduled_end_at, watermarkEnabled, workerServiceID, youtubeOutputID],
  );
  const autoStartReady = editing || !autoStartFromDiscord || (selectedValue(discordConfigID) !== "" && createVisualState.discordTargetReady);
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
    && (liveEditing || (autoStartReady && nodeAssignmentReady))
    && (editing || createVisualState.ready)
    && watermarkReady
    && encoderAudioGainReady;
  const draft = useNonSecretDraft([name, discordConfigID, autoStartFromDiscord, youtubeOutputID, archiveProfileID, encoderProfileID, captionProfileID, watermarkEnabled, overlayProfileID, encoderAudioGainDB, encoderServiceID, workerServiceID, scheduledStartAt]);
  useExistingDraft(false, () => actionController.isPending(actionIntent));
  const saveAcknowledgements = useRef(new WeakMap<StreamActionIntent, () => boolean>());
  useLayoutEffect(() => {
    let acknowledged = false;
    saveAcknowledgements.current.set(actionIntent, () => {
      if (acknowledged) return false;
      acknowledged = true;
      draft.acknowledgeSubmitted();
      if (actionIntent.id === "STR-01" && actionIntent.payload?.visual_settings === createVisualState.extension.visual_settings) {
        createVisualState.acknowledgeSubmitted?.();
      }
      return true;
    });
  }, [actionIntent, createVisualState, draft]);
  const handleSaveResult = useCallback((result: StreamActionExecutionResult, intent: StreamActionIntent) => {
    onActionResult(result, intent);
    if (result.kind === "succeeded" && isStreamValue(result.value)) {
      setMessage(liveEditing ? uiText("{0} のライブ設定を停止せず反映しました。", result.value.name) : editing ? uiText("{0} の設定を更新しました。", result.value.name) : uiText("{0} を配信枠として作成しました。", result.value.name));
      if (saveAcknowledgements.current.get(intent)?.() !== true) return;
      onSaved(result.value);
      return;
    }
    if (result.kind === "failed") setMessage(t(result.error.messageKey));
    else if (result.kind === "outcome_unknown") setMessage(t("confirmationOutcomeUnknown"));
    else if (result.kind === "blocked") setMessage(streamActionBlockedMessage(result.reason, uiText));
  }, [editing, liveEditing, onActionResult, onSaved, t, uiText]);

  return (
    <Card id="create-stream" className={className}>
      <CardHeader className="border-b">
        <CardTitle className="flex items-center gap-2">{editing ? <Pencil className="size-5" /> : <Plus className="size-5" />}{editing ? uiText("配信枠を編集") : uiText("配信枠を作成")}</CardTitle>
        <CardDescription>{liveEditing ? uiText("配信を止めずにEncoder音量とウォーターマークを変更できます。") : editing ? uiText("待機中または終了済みの枠設定を編集します。") : uiText("Discord VCの開始条件、配信経路、録画保存先を設定します。Node割り当ては作成後に明示的に行います。")}</CardDescription>
      </CardHeader>
      <CardContent>
        <form className="space-y-4" onSubmit={(event) => { event.preventDefault(); setMessage(""); if (formReady) saveControl.current?.open(); }}>
          {!liveEditing ? <>
            <FormSection title={uiText("基本情報")} description={uiText("運用中に識別する配信枠名")}><div className="max-w-xl"><TextField label={uiText("配信枠名")} value={name} onChange={setName} placeholder={uiText("例: 商品発表会 メイン配信")} required /></div></FormSection>
            <FormSection title={uiText("YouTube開始予定")} description={uiText("空欄なら、配信開始時にYouTubeの枠をすぐ開始します。日時を指定した場合だけ予定配信になります。")}><div className="max-w-xl"><TextField label={uiText("開始予定（任意）")} type="datetime-local" value={scheduledStartAt} onChange={setScheduledStartAt} /><p className="mt-2 text-xs text-muted-foreground">{uiText("日時はこのブラウザのタイムゾーンで扱います。既存の予定を消して保存すると、次回の開始は即時になります。")}</p></div></FormSection>
            <FormSection title={uiText("開始条件")} description={editing ? uiText("自動開始に使うDiscordと担当Node") : uiText("自動開始に使うDiscord。担当Nodeは作成後の編集で割り当てます。")}>
              <label className="mb-3 flex min-h-10 items-center gap-2 rounded-md border bg-muted/20 px-3 text-sm"><Checkbox checked={autoStartFromDiscord} onCheckedChange={(value) => setAutoStartFromDiscord(value === true)} />{uiText("Discord VCへの参加を検知して自動開始")}</label>
              <div className="grid gap-3 md:grid-cols-2">
                <SelectField label={uiText("Discord BOT設定")} value={discordConfigID} onChange={setDiscordConfigID} options={[{ value: noneValue, label: uiText("未選択") }, ...discordConfigs]} />
                {editing && canAssignWorker ? <SelectField label={uiText("担当Worker Node")} value={effectiveWorkerServiceID} onChange={setWorkerServiceID} options={[{ value: noneValue, label: uiText("未選択") }, ...workerNodes]} /> : null}
                {editing && canAssignEncoder ? <SelectField label={uiText("担当Encoder Node")} value={effectiveEncoderServiceID} onChange={setEncoderServiceID} options={[{ value: noneValue, label: uiText("未選択") }, ...encoderNodes]} /> : null}
              </div>
            </FormSection>
            {!editing ? <div className="flex gap-2 rounded-md border bg-muted/30 p-3 text-sm text-muted-foreground"><AlertCircle className="mt-0.5 size-4 shrink-0" />{uiText("新規作成では既存配信のNode割り当てを変更しません。作成後に配信枠を編集し、開始前に担当Nodeを割り当ててください。")}</div> : null}
            <FormSection title={uiText("出力と録画")} description={uiText("視聴先と事前登録した録画設定")}><div className="grid gap-3 md:grid-cols-2"><SelectField label={uiText("YouTube出力")} value={youtubeOutputID} onChange={setYouTubeOutputID} options={[{ value: noneValue, label: uiText("未選択") }, ...youtubeOutputs]} /><SelectField label={uiText("録画プロファイル")} value={archiveProfileID} onChange={setArchiveProfileID} options={[{ value: noneValue, label: uiText("録画しない") }, ...archiveProfiles]} /></div>{archiveProfiles.length === 0 ? <p className="mt-3 text-sm text-muted-foreground">{uiText("録画する場合は、先に")}<Link href="/admin/archive/" className="mx-1 underline underline-offset-2">{uiText("録画・アーカイブ")}</Link>{uiText("で録画プロファイルと保存先を作成してください。")}</p> : null}</FormSection>
            <StreamVisualSettingsSection stream={stream} canUpdate={canUpdate} onCreateState={setCreateVisualState} />
          </> : null}
          <FormSection title={uiText("Encoderライブ調整")} description={uiText("音量とウォーターマークは配信中でも停止せず反映されます。")}><div className="grid gap-3 md:grid-cols-2">{!liveEditing ? <SelectField label={uiText("エンコード設定")} value={encoderProfileID} onChange={setEncoderProfileID} options={[{ value: noneValue, label: uiText("未選択") }, ...encoderProfiles]} /> : null}{!liveEditing ? <SelectField label={uiText("字幕設定")} value={captionProfileID} onChange={setCaptionProfileID} options={[{ value: noneValue, label: uiText("未選択") }, ...captionProfiles]} /> : null}<TextField label={uiText("Encoder音量（dB）")} type="number" value={encoderAudioGainDB} onChange={setEncoderAudioGainDB} min="-60" max="24" step="0.1" /><label className="flex min-h-10 items-center gap-2 rounded-md border bg-muted/20 px-3 text-sm"><Checkbox checked={watermarkEnabled} onCheckedChange={(value) => setWatermarkEnabled(value === true)} />{uiText("ウォーターマークを使用")}</label><SelectField label={uiText("ウォーターマーク設定")} value={overlayProfileID} onChange={setOverlayProfileID} options={[{ value: noneValue, label: uiText("未選択") }, ...overlayProfiles]} disabled={!watermarkEnabled} /></div></FormSection>
          {!encoderAudioGainReady ? <Warning>{uiText("Encoder音量は -60 dB から +24 dB の範囲で指定してください。")}</Warning> : null}
          {!liveEditing && autoStartFromDiscord && !autoStartReady ? <Warning>{uiText("自動開始を使うには、Discord BOT設定とv2 Discord配信先を指定してください。")}</Warning> : null}
          {watermarkEnabled && !watermarkReady ? <Warning>{uiText("ウォーターマークを使う場合は、ウォーターマーク設定を選択してください。")}</Warning> : null}
          {!liveEditing && autoStartFromDiscord && canAssignEncoder && canAssignWorker && !nodeAssignmentReady ? <Warning>{uiText("自動開始する配信枠には、担当Encoder Nodeと担当Worker Nodeを選択してください。")}</Warning> : null}
          {!liveEditing && nodeAssignmentPermissionLimited ? <Warning>{uiText("Nodeを割り当てる権限がありません。管理者に依頼し、サービス稼働画面で割り当てを確認してください。")}</Warning> : null}
          {editing && !canUpdate ? <p className="text-sm text-red-600">{uiText("配信枠を更新する権限がありません。")}</p> : null}{!editing && !canCreate ? <p className="text-sm text-red-600">{uiText("配信枠を作成する権限がありません。")}</p> : null}
          <FormFooter feedback={message}><StreamActionControl ref={saveControl} controller={actionController} intent={actionIntent} label={liveEditing ? uiText("ライブ設定を反映") : editing ? uiText("設定を保存") : uiText("配信枠を作成")} buttonProps={{ type: "submit" }} disabled={!formReady} onResult={handleSaveResult}>{editing ? <Pencil className="size-4" /> : <Plus className="size-4" />}{liveEditing ? uiText("ライブ設定を反映") : editing ? uiText("設定を保存") : uiText("配信枠を作成")}</StreamActionControl></FormFooter>
        </form>
      </CardContent>
    </Card>
  );
}

function Warning({ children }: { children: ReactNode }) { return <div className="flex gap-2 rounded-md border border-amber-200 bg-amber-50 p-3 text-sm text-amber-800"><AlertCircle className="mt-0.5 size-4 shrink-0" />{children}</div>; }
function FormSection({ title, description, children }: { title: string; description: string; children: ReactNode }) { return <FieldGroup title={title} description={description}><div className="min-w-0 sm:col-span-2">{children}</div></FieldGroup>; }
function TextField({ label, value, onChange, placeholder, type = "text", required, error, min, max, step }: { label: string; value: string; onChange: (value: string) => void; placeholder?: string; type?: string; required?: boolean; error?: string; min?: string; max?: string; step?: string }) { return <Field label={label} error={error} required={required}><Input type={type} value={value} onChange={(event) => onChange(event.target.value)} placeholder={placeholder} required={required} min={min} max={max} step={step} /></Field>; }
function SelectField({ label, value, onChange, options, disabled }: { label: string; value: string; onChange: (value: string) => void; options: StreamSelectOption[]; disabled?: boolean }) { const selected = options.find((option) => option.value === value); return <label className="grid gap-1.5 text-sm"><span className="font-medium">{label}</span><Select value={value} onValueChange={onChange} disabled={disabled}><SelectTrigger className="w-full" disabled={disabled}><span className="min-w-0 truncate">{selected?.label || <SelectValue />}</span></SelectTrigger><SelectContent>{options.map((option) => <SelectItem key={option.value} value={option.value} textValue={option.label} disabled={option.disabled}><span className="min-w-0 truncate">{option.label}</span></SelectItem>)}</SelectContent></Select>{selected?.description ? <span className="text-xs text-muted-foreground">{selected.description}</span> : null}</label>; }
