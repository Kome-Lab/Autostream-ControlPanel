"use client";
import { fixedPresentationText } from "@/lib/i18n/ui-v2/presentation-copy";
import { useUICopy } from "@/lib/i18n/ui-v2/use-ui-copy";
import { japaneseCopy, type UICopy } from "@/lib/i18n/ui-v2/copy";


import { useExistingDraft } from "@/components/forms/draft-exit";
import { useCallback, useEffect, useId, useMemo, useRef, useState } from "react";
import { ExternalLink, ImageUp, LoaderCircle, RefreshCcw, Save } from "lucide-react";

import { useI18n } from "@/components/admin/i18n-provider";
import { ConfirmationDialogFrame } from "@/components/foundation/confirmation/confirmation-dialog-frame";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { Input } from "@/components/ui/input";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { useCurrentUser, useResourceData, useServiceHealth } from "@/features/queries";
import type { StreamVisualSettings } from "@/features/streams/control-platform";
import { uploadDraftMediaAsset } from "@/features/streams/media-asset-client";
import {
  createStreamVisualActionController,
  visualSettingsFingerprint,
  type VisualActionPermissionSnapshot,
  type VisualActionStateSnapshot,
} from "@/features/streams/stream-visual-action-controller";
import {
  buildStreamCreateVisualExtension,
  buildStreamVisualFields,
  createStreamVisualPreviewOwner,
  defaultStreamVisualDraft,
  streamVisualDraftFromSettings,
  validateStreamVisualDraft,
  visualCapabilityWarnings,
  type StreamVisualDraft,
  type StreamVisualSection,
} from "@/features/streams/stream-visual-draft";
import { normalizeRows, rowString } from "@/features/streams/stream-view-options";
import { apiGet, apiPut } from "@/lib/api/client";
import { hasPermission } from "@/lib/auth/permissions";
import { adaptAPIError } from "@/lib/foundation/api-errors/adapter";
import type { CurrentUser, Stream } from "@/types/domain";
import { useQuery, useQueryClient } from "@tanstack/react-query";

export type StreamCreateVisualState = Readonly<{
  extension: Record<string, unknown>;
  ready: boolean;
  discordTargetReady: boolean;
  acknowledgeSubmitted?: () => void;
}>;

type Props = {
  stream?: Stream | null;
  canUpdate: boolean;
  onCreateState: (state: StreamCreateVisualState) => void;
};

export function StreamVisualSettingsSection({ stream, canUpdate, onCreateState }: Props) {
  const uiText = useUICopy();
  const { locale, t } = useI18n();
  const queryClient = useQueryClient();
  const editing = Boolean(stream);
  const queryKey = useMemo(() => ["streams", stream?.id || "create", "visual-settings"] as const, [stream?.id]);
  const visual = useQuery({
    queryKey,
    queryFn: () => apiGet<StreamVisualSettings>(`/streams/${encodeURIComponent(stream?.id || "")}/visual-settings`),
    enabled: editing,
    retry: false,
  });
  const currentUser = useCurrentUser();
  const canReadDiscordPresets = currentUser.isSuccess && hasPermission(currentUser.data, "discord_target_presets.read");
  const canReadCoverPresets = currentUser.isSuccess && hasPermission(currentUser.data, "video_cover_presets.read");
  const discordPresets = useResourceData<unknown>("/discord/target-presets", canReadDiscordPresets);
  const coverPresets = useResourceData<unknown>("/video-cover-presets", canReadCoverPresets);
  const serviceHealth = useServiceHealth(editing);
  const [draft, setDraft] = useState(() => editing && visual.data ? streamVisualDraftFromSettings(visual.data) : defaultStreamVisualDraft());
  const [draftRevision, setDraftRevision] = useState(() => editing ? visual.data?.revision : undefined);
  const [dirtySections, setDirtySections] = useState<ReadonlySet<StreamVisualSection>>(() => new Set());
  const [coverSelectionDirty, setCoverSelectionDirty] = useState(false);
  const [coverPresetRevision, setCoverPresetRevision] = useState(() => editing ? visual.data?.cover_preset_revision || 0 : 0);
  const [uploadedBackground, setUploadedBackground] = useState(false);
  const [uploadedCover, setUploadedCover] = useState(false);
  const [backgroundPreview, setBackgroundPreview] = useState("");
  const [coverPreview, setCoverPreview] = useState("");
  const [uploading, setUploading] = useState<"background" | "cover" | "">("");
  const [saving, setSaving] = useState(false);
  const [needsRefresh, setNeedsRefresh] = useState(false);
  const [message, setMessage] = useState("");
  // Comparison markers belong to this visual owner; they hold no input or upload credentials.
  const [createRevision, setCreateRevision] = useState(0);
  const currentCreateRevision = useRef(0);
  const savedCreateRevision = useRef(0);
  const draftExit = useExistingDraft(() => editing ? dirtySections.size > 0 : currentCreateRevision.current !== savedCreateRevision.current, Boolean(uploading) || saving);
  const initialized = useRef(Boolean(editing && visual.data));
  const previewOwner = useMemo(() => createStreamVisualPreviewOwner((url) => URL.revokeObjectURL(url)), []);

  useEffect(() => {
    if (!editing || initialized.current || !visual.data) return;
    initialized.current = true;
    setDraft(streamVisualDraftFromSettings(visual.data));
    setDraftRevision(visual.data.revision);
    setCoverPresetRevision(visual.data.cover_preset_revision || 0);
  }, [editing, visual.data]);
  useEffect(() => () => previewOwner.release(), [previewOwner]);

  const discordRows = useMemo(() => normalizeRows(discordPresets.data), [discordPresets.data]);
  const coverRows = useMemo(() => normalizeRows(coverPresets.data).filter((row) => row.enabled !== false), [coverPresets.data]);
  const selectedDiscord = discordRows.find((row) => rowString(row, ["id"]) === draft.discordTargetPresetID);
  const selectedCover = coverRows.find((row) => rowString(row, ["id"]) === draft.coverPresetID);
  const selectedDiscordRevision = Number(selectedDiscord?.revision || 0);
  const selectedCoverRevision = Number(selectedCover?.revision || 0);
  const canSelectDiscordPreset = canReadDiscordPresets && discordPresets.isSuccess && !discordPresets.isFetching;
  const canSelectCoverPreset = canReadCoverPresets && coverPresets.isSuccess && !coverPresets.isFetching;
  const discordPresetRequired = (!editing || dirtySections.has("discord")) && draft.discordTargetMode === "preset";
  const coverPresetRequired = (!editing || coverSelectionDirty) && draft.coverSource === "preset";
  const presetSelectionsReady = (!discordPresetRequired || (canSelectDiscordPreset && Boolean(selectedDiscord) && selectedDiscordRevision === draft.discordTargetPresetRevision))
    && (!coverPresetRequired || (canSelectCoverPreset && Boolean(selectedCover) && selectedCoverRevision === coverPresetRevision));
  const discordPresetUpdated = draft.discordTargetMode === "preset" && draft.discordTargetPresetRevision > 0 && selectedDiscordRevision > draft.discordTargetPresetRevision;
  const coverPresetUpdated = draft.coverSource === "preset" && coverPresetRevision > 0 && selectedCoverRevision > coverPresetRevision;
  const serverSettingsChanged = editing && draftRevision !== undefined && visual.data?.revision !== draftRevision;
  const validation = useMemo(() => validateStreamVisualDraft(draft), [draft]);
  const createState = useMemo<StreamCreateVisualState>(() => ({
    extension: buildStreamCreateVisualExtension(draft),
    ready: validation.ready && presetSelectionsReady && !uploading,
    acknowledgeSubmitted: () => {
      if (editing || currentCreateRevision.current !== createRevision) return;
      savedCreateRevision.current = createRevision;
      setDirtySections(new Set());
    },
    discordTargetReady: draft.discordTargetMode === "preset"
      ? draft.discordTargetPresetID.trim() !== "" && draft.discordTargetPresetRevision > 0 && presetSelectionsReady
      : draft.discordTargetMode === "manual"
        ? [draft.discordGuildID, draft.discordTextChannelID, draft.discordVoiceChannelID].every((value) => value.trim() !== "")
        : false,
  }), [createRevision, draft, editing, presetSelectionsReady, uploading, validation.ready]);
  useEffect(() => { if (!editing) onCreateState(createState); }, [createState, editing, onCreateState]);

  const capabilities = useMemo(() => {
    if (!editing || serviceHealth.status !== "success" || serviceHealth.fetchStatus !== "idle") return undefined;
    const rows = serviceHealth.data || [];
    const worker = rows.find((row) => (row.service_id || row.id) === stream?.assigned_worker_id);
    const encoder = rows.find((row) => (row.service_id || row.id) === stream?.assigned_encoder_id);
    return {
      sceneAppearance: worker?.reported_capabilities?.scene_appearance_v1 === true,
      videoCover: encoder?.reported_capabilities?.live_video_cover_v1 === true,
    };
  }, [editing, serviceHealth.data, serviceHealth.fetchStatus, serviceHealth.status, stream?.assigned_encoder_id, stream?.assigned_worker_id]);
  const capabilityWarnings = useMemo(() => visualCapabilityWarnings(draft, capabilities), [capabilities, draft]);

  const controller = useMemo(() => createStreamVisualActionController({
    getPermission: () => visualPermissionSnapshot(queryClient),
    getState: () => visualStateSnapshot(queryClient, queryKey),
    mutate: (request) => apiPut<StreamVisualSettings>(`/streams/${encodeURIComponent(stream?.id || "")}/visual-settings`, request),
  }), [queryClient, queryKey, stream?.id]);
  const update = useCallback((section: StreamVisualSection, fields: Partial<StreamVisualDraft>) => {
    if (!editing) { currentCreateRevision.current += 1; setCreateRevision(currentCreateRevision.current); }
    if (section === "cover" && ["coverSource", "coverPresetID", "coverAssetID", "coverVariantID"].some((key) => Object.prototype.hasOwnProperty.call(fields, key))) setCoverSelectionDirty(true);
    setDirtySections((current) => new Set([...current, section]));
    setDraft((current) => ({ ...current, ...fields }));
  }, [editing]);
  const selectDiscordPreset = (id: string) => {
    if (!canSelectDiscordPreset) return;
    const row = discordRows.find((item) => rowString(item, ["id"]) === id);
    if (!row) return;
    update("discord", {
      discordTargetPresetID: id,
      discordTargetPresetRevision: Number(row.revision) || 0,
      discordGuildID: rowString(row, ["guild_id"]),
      discordTextChannelID: rowString(row, ["text_channel_id"]),
      discordVoiceChannelID: rowString(row, ["voice_channel_id"]),
    });
  };
  const selectCoverPreset = (id: string) => {
    if (!canSelectCoverPreset) return;
    const row = coverRows.find((item) => rowString(item, ["id"]) === id);
    if (!row) return;
    setCoverPresetRevision(Number(row.revision) || 0);
    update("cover", { coverPresetID: id });
  };

  const upload = async (kind: "background" | "cover", file?: File) => {
    if (!file || uploading) return;
    setUploading(kind);
    setMessage("");
    try {
      const result = await uploadDraftMediaAsset(file, kind === "background" ? "scene_background" : "video_cover", draft.uploadSessionID ? { id: draft.uploadSessionID } : undefined);
      const preview = URL.createObjectURL(file);
      if (kind === "background") {
        setBackgroundPreview(previewOwner.replace("background", preview));
        setUploadedBackground(true);
        update("background", { backgroundMode: "image", backgroundAssetID: result.asset.id, backgroundVariantID: result.variant.id, uploadSessionID: result.session.id });
      } else {
        setCoverPreview(previewOwner.replace("cover", preview));
        setUploadedCover(true);
        update("cover", { coverSource: "upload", coverAssetID: result.asset.id, coverVariantID: result.variant.id, uploadSessionID: result.session.id });
      }
      setMessage(locale === "ja" ? "画像の検証と処理が完了しました。設定を保存すると配信枠に反映されます。" : "The image is ready. Save the settings to use it in this stream slot.");
    } catch (error) {
      setMessage(t(adaptAPIError(error).messageKey));
    } finally {
      setUploading("");
    }
  };

  const save = async () => {
    if (!visual.data || draftRevision === undefined || !validation.ready || !presetSelectionsReady || saving || uploading || needsRefresh || serverSettingsChanged) return;
    setSaving(true);
    setMessage("");
    const result = await controller.issue(buildStreamVisualSaveFields(draft, dirtySections, editing && !coverSelectionDirty), draftRevision, draft.uploadSessionID || undefined);
    setSaving(false);
    if (result.kind === "succeeded") {
      queryClient.setQueryData(queryKey, result.value);
      setDraft(streamVisualDraftFromSettings(result.value));
      setDraftRevision(result.value.revision);
      setCoverPresetRevision(result.value.cover_preset_revision || 0);
      setCoverSelectionDirty(false);
      setDirtySections(new Set());
      setUploadedBackground(false);
      setUploadedCover(false);
      setMessage(locale === "ja" ? "ビジュアル設定を保存しました。" : "Visual settings were saved.");
    } else if (result.kind === "outcome_unknown") {
      setNeedsRefresh(true);
      setMessage(t("confirmationOutcomeUnknown"));
    } else if (result.kind === "failed") {
      setNeedsRefresh(true);
      setMessage(t(result.error.messageKey));
    } else {
      setMessage(visualBlockedMessage(result.reason, locale, uiText));
    }
  };
  const refresh = async () => {
    const result = await visual.refetch();
    if (result.isSuccess && result.data) {
      controller.reconcile();
      setNeedsRefresh(false);
      setDraft(streamVisualDraftFromSettings(result.data));
      setDraftRevision(result.data.revision);
      setCoverPresetRevision(result.data.cover_preset_revision || 0);
      setCoverSelectionDirty(false);
      setDirtySections(new Set());
      setUploadedBackground(false);
      setUploadedCover(false);
      previewOwner.release();
      setBackgroundPreview("");
      setCoverPreview("");
      setMessage(locale === "ja" ? "サーバーの最新設定を読み込みました。" : "Loaded the latest server settings.");
    } else {
      setMessage(locale === "ja" ? "設定を再取得できませんでした。編集中の内容は保持しています。" : "The settings could not be reloaded. Your draft is unchanged.");
    }
  };

  const controlsDisabled = !canUpdate || saving || (editing && !visual.data);
  const saveDisabled = controlsDisabled || dirtySections.size === 0 || !validation.ready || !presetSelectionsReady || Boolean(uploading) || visual.isFetching || needsRefresh || serverSettingsChanged;
  return (
    <fieldset className="space-y-4 rounded-lg border p-4" aria-describedby="stream-visual-help">
      <legend className="px-1 text-sm font-semibold">{locale === "ja" ? "背景・表示名・Discord配信先・蓋画像（Video Cover）" : "Background, display title, Discord target, and video cover"}</legend>
      <p id="stream-visual-help" className="text-xs text-muted-foreground">{locale === "ja" ? "背景・表示名・Discord配信先は次回の配信開始から適用されます。配信中の蓋画像の表示・解除は配信枠の詳細画面から操作できます。" : "Background, display title, and Discord target changes apply at the next start. Show or hide the cover during a stream from its detail view."}</p>
      {visual.isError ? <p role="alert" className="text-sm text-destructive">{locale === "ja" ? "ビジュアル設定を取得できません。保存せず再読込してください。" : "Visual settings are unavailable. Reload before saving."}</p> : null}
      {serverSettingsChanged ? <p role="alert" className="text-sm text-amber-700 dark:text-amber-300">{locale === "ja" ? "編集中にサーバーの設定が更新されました。入力内容は保持しています。最新設定を再読込してから保存してください。" : "The server settings changed while you were editing. Your draft is preserved. Reload the latest settings before saving."}</p> : null}
      <div className="grid gap-4 xl:grid-cols-2">
        <VisualGroup title={locale === "ja" ? "シーン背景" : "Scene background"}>
          <ModeSelect purpose={locale === "ja" ? "シーン背景モード" : "Scene background mode"} value={draft.backgroundMode} disabled={controlsDisabled || uploadedBackground} onChange={(value) => update("background", { backgroundMode: value as StreamVisualDraft["backgroundMode"] })} options={[["default", locale === "ja" ? "既定" : "Default"], ["image", locale === "ja" ? "画像（cover / center crop）" : "Image (cover / center crop)"]]} />
          {draft.backgroundMode === "image" ? <UploadControl label={locale === "ja" ? "背景画像をアップロード" : "Upload background image"} busy={uploading === "background"} disabled={controlsDisabled || uploadedBackground} onFile={(file) => void upload("background", file)} /> : null}
          {draft.backgroundMode === "image" ? <VisualPreview label={locale === "ja" ? "背景画像のプレビュー" : "Background image preview"} imageURL={backgroundPreview} emptyLabel={draft.backgroundAssetID ? locale === "ja" ? "保存済みの背景画像を使用します。" : "The saved background image is selected." : locale === "ja" ? "背景画像を選択してください。" : "Select a background image."} /> : null}
        </VisualGroup>
        <VisualGroup title={locale === "ja" ? "ヘッダータイトル" : "Header title"}>
          <ModeSelect purpose={locale === "ja" ? "ヘッダータイトルモード" : "Header title mode"} value={draft.headerTitleMode} disabled={controlsDisabled} onChange={(value) => update("title", { headerTitleMode: value as StreamVisualDraft["headerTitleMode"] })} options={[["default", locale === "ja" ? "配信枠名を使用" : "Use stream name"], ["custom", locale === "ja" ? "カスタム" : "Custom"]]} />
          {draft.headerTitleMode === "custom" ? <Input aria-label={locale === "ja" ? "カスタムヘッダータイトル" : "Custom header title"} maxLength={80} value={draft.headerTitleValue} disabled={controlsDisabled} onChange={(event) => update("title", { headerTitleValue: event.target.value })} /> : null}
        </VisualGroup>
        <VisualGroup title={locale === "ja" ? "Discord配信先" : "Discord target"}>
          <ModeSelect purpose={locale === "ja" ? "Discord配信先モード" : "Discord target mode"} value={draft.discordTargetMode} disabled={controlsDisabled} onChange={(value) => update("discord", { discordTargetMode: value as StreamVisualDraft["discordTargetMode"] })} options={[["inherit", locale === "ja" ? "従来設定を継承" : "Inherit legacy target"], ["preset", locale === "ja" ? "プリセット" : "Preset", !canReadDiscordPresets], ["manual", locale === "ja" ? "手動" : "Manual"]]} />
          {draft.discordTargetMode === "preset" ? <ModeSelect purpose={locale === "ja" ? "Discord配信先プリセット" : "Discord target preset"} value={draft.discordTargetPresetID} disabled={controlsDisabled || !canSelectDiscordPreset} onChange={selectDiscordPreset} options={[["", locale === "ja" ? "選択してください" : "Select a preset"], ...visualPresetOptions(discordRows, draft.discordTargetPresetID, locale === "ja" ? "設定済みプリセット（一覧で未確認）" : "Saved preset (not in the current list)", draft.discordTargetPresetRevision)]} /> : null}
          {draft.discordTargetMode === "manual" ? <div className="grid gap-3 sm:grid-cols-3">
            <label className="space-y-1 text-xs"><span>{locale === "ja" ? "DiscordサーバーID" : "Discord server ID"}</span><Input aria-label="Discord Guild ID" value={draft.discordGuildID} disabled={controlsDisabled} onChange={(event) => update("discord", { discordGuildID: event.target.value })} /></label>
            <label className="space-y-1 text-xs"><span>{locale === "ja" ? "チャットチャンネルID" : "Text channel ID"}</span><Input aria-label="Discord Text Channel ID" value={draft.discordTextChannelID} disabled={controlsDisabled} onChange={(event) => update("discord", { discordTextChannelID: event.target.value })} /></label>
            <label className="space-y-1 text-xs"><span>{locale === "ja" ? "ボイスチャンネルID" : "Voice channel ID"}</span><Input aria-label="Discord Voice Channel ID" value={draft.discordVoiceChannelID} disabled={controlsDisabled} onChange={(event) => update("discord", { discordVoiceChannelID: event.target.value })} /></label>
          </div> : null}
          {draft.discordTargetMode === "preset" && draft.discordTargetPresetID ? <dl className="grid gap-2 rounded-md border p-2 text-xs sm:grid-cols-3">
            <div className="min-w-0"><dt className="text-muted-foreground">{locale === "ja" ? "サーバーID" : "Server ID"}</dt><dd className="break-all">{draft.discordGuildID || "—"}</dd></div>
            <div className="min-w-0"><dt className="text-muted-foreground">{locale === "ja" ? "チャットID" : "Text channel ID"}</dt><dd className="break-all">{draft.discordTextChannelID || "—"}</dd></div>
            <div className="min-w-0"><dt className="text-muted-foreground">{locale === "ja" ? "ボイスID" : "Voice channel ID"}</dt><dd className="break-all">{draft.discordVoiceChannelID || "—"}</dd></div>
          </dl> : null}
          {discordPresetUpdated ? <div className="space-y-2 rounded-md border border-amber-300 p-2 text-xs">
            <p role="status">{locale === "ja" ? `プリセット更新あり：選択済み r${draft.discordTargetPresetRevision}、最新 r${selectedDiscordRevision}。保存済みの配信先は自動変更されません。` : `Preset update available: selected r${draft.discordTargetPresetRevision}, latest r${selectedDiscordRevision}. Saved targets are not changed automatically.`}</p>
            <Button type="button" size="sm" variant="outline" disabled={controlsDisabled || !canSelectDiscordPreset} onClick={() => selectDiscordPreset(draft.discordTargetPresetID)}>{locale === "ja" ? "最新値を適用" : "Use latest preset values"}</Button>
          </div> : null}
          {visual.data?.discord_preset_deleted && draft.discordTargetMode === "preset" && draft.discordTargetPresetID === visual.data.discord_target_preset_id ? <p role="status" className="text-xs text-amber-700 dark:text-amber-300">{locale === "ja" ? "元のプリセットは削除済みです。保存済みの配信先を保持しています。" : "The source preset was deleted. The saved target is retained."}</p> : null}
          <PresetListStatus locale={locale} allowed={canReadDiscordPresets} authorityStatus={currentUser.status} status={discordPresets.status} fetching={discordPresets.isFetching} count={discordRows.length} onRefresh={() => void discordPresets.refetch()} manageHref="/admin/discord/#target-presets" manageLabel={locale === "ja" ? "Discordプリセット管理（別タブ）" : "Manage Discord presets (new tab)"} />
        </VisualGroup>
        <VisualGroup title={locale === "ja" ? "蓋画像（Video Cover）" : "Video cover"}>
          <ModeSelect purpose={locale === "ja" ? "蓋画像のソース" : "Video cover source"} value={draft.coverSource} disabled={controlsDisabled || uploadedCover} onChange={(value) => update("cover", { coverSource: value as StreamVisualDraft["coverSource"], ...(value === "none" ? { coverStartActive: false } : {}) })} options={[["none", "OFF"], ["preset", locale === "ja" ? "プリセット" : "Preset", !canReadCoverPresets], ["upload", locale === "ja" ? "アップロード" : "Upload"]]} />
          {draft.coverSource === "preset" ? <ModeSelect purpose={locale === "ja" ? "蓋画像プリセット" : "Video cover preset"} value={draft.coverPresetID} disabled={controlsDisabled || !canSelectCoverPreset} onChange={selectCoverPreset} options={[["", locale === "ja" ? "選択してください" : "Select a preset"], ...visualPresetOptions(coverRows, draft.coverPresetID, locale === "ja" ? "設定済みプリセット（現在は選択不可）" : "Saved preset (currently unavailable)", coverPresetRevision)]} /> : null}
          {coverPresetUpdated ? <div className="space-y-2 rounded-md border border-amber-300 p-2 text-xs">
            <p role="status">{locale === "ja" ? `蓋画像プリセット更新あり：選択済み r${coverPresetRevision}、最新 r${selectedCoverRevision}。開始時の表示設定だけを変更しても保存済み画像は変わりません。` : `Video cover preset update available: selected r${coverPresetRevision}, latest r${selectedCoverRevision}. Changing only the start visibility keeps the saved image.`}</p>
            <Button type="button" size="sm" variant="outline" disabled={controlsDisabled || !canSelectCoverPreset} onClick={() => selectCoverPreset(draft.coverPresetID)}>{locale === "ja" ? "最新の蓋画像を適用" : "Use the latest cover image"}</Button>
          </div> : null}
          {draft.coverSource === "upload" ? <><UploadControl label={locale === "ja" ? "蓋画像をアップロード" : "Upload cover image"} busy={uploading === "cover"} disabled={controlsDisabled || uploadedCover} onFile={(file) => void upload("cover", file)} /><VisualPreview label={locale === "ja" ? "蓋画像のプレビュー" : "Video cover preview"} imageURL={coverPreview} emptyLabel={draft.coverAssetID ? locale === "ja" ? "保存済みの蓋画像を使用します。" : "The saved cover image is selected." : locale === "ja" ? "蓋画像を選択してください。" : "Select a video cover image."} /></> : null}
          {draft.coverSource !== "none" ? <label className="flex min-h-10 items-center gap-2 rounded-md border px-3 text-sm"><Checkbox checked={draft.coverStartActive} disabled={controlsDisabled} onCheckedChange={(value) => update("cover", { coverStartActive: value === true })} />{locale === "ja" ? "配信開始時からCoverを表示" : "Show cover when the stream starts"}</label> : null}
          <p className="text-xs text-muted-foreground">{locale === "ja" ? "蓋画像を表示中も音声は継続し、ウォーターマークはその上に表示されます。配信・録画・プレビューには同じ映像が送られます。" : "Audio continues while the cover is shown. The watermark stays above it, and live, recording, and preview outputs use the same composed image."}</p>
          <PresetListStatus locale={locale} allowed={canReadCoverPresets} authorityStatus={currentUser.status} status={coverPresets.status} fetching={coverPresets.isFetching} count={coverRows.length} onRefresh={() => void coverPresets.refetch()} manageHref="/admin/overlay/#video-cover-presets" manageLabel={locale === "ja" ? "蓋画像プリセット管理（別タブ）" : "Manage video cover presets (new tab)"} />
        </VisualGroup>
      </div>
      {[...validation.issues, ...capabilityWarnings].map((issue) => <p key={issue} role="status" className="rounded-md border border-amber-300 bg-amber-50 p-2 text-xs text-amber-900 dark:bg-amber-950/30 dark:text-amber-200">{fixedPresentationText(issue, uiText)}</p>)}
      {!presetSelectionsReady && validation.ready ? <p role="status" className="text-sm text-amber-700 dark:text-amber-300">{locale === "ja" ? "変更するプリセットの最新状態を確認できていません。プリセット一覧を再取得して選択を確認してください。" : "The changed preset selection is not verified. Refresh its list and confirm the selection before saving."}</p> : null}
      {message ? <p aria-live="polite" className="text-sm text-muted-foreground">{message}</p> : null}
      {editing ? <div className="flex flex-wrap justify-end gap-2">
        <Button type="button" variant="outline" onClick={() => { if (draftExit) draftExit.request(() => void refresh()); else void refresh(); }} disabled={visual.isFetching}><RefreshCcw className="size-4" />{locale === "ja" ? "最新設定を再読込" : "Reload latest"}</Button>
        <ConfirmationDialogFrame trigger={<Button type="button" disabled={saveDisabled}><Save className="size-4" />{locale === "ja" ? "ビジュアル設定を保存" : "Save visual settings"}</Button>} title={locale === "ja" ? "ビジュアル設定を保存" : "Save visual settings"} description={locale === "ja" ? "開始時のシーン、Discord snapshot、Cover設定が変わります。現在の配信中には適用されません。" : "This changes the next-start scene, Discord snapshot, and cover settings; it does not alter the active stream."} cancelLabel={locale === "ja" ? "キャンセル" : "Cancel"} actionLabel={locale === "ja" ? "保存" : "Save"} actionClosesDialog onConfirm={() => void save()} />
      </div> : null}
    </fieldset>
  );
}

function VisualGroup({ title, children }: { title: string; children: React.ReactNode }) { return <section className="space-y-3 rounded-md border bg-muted/10 p-3"><h3 className="text-sm font-medium">{title}</h3>{children}</section>; }
type VisualSelectOption = readonly [value: string, label: string, disabled?: boolean];

export function buildStreamVisualSaveFields(draft: StreamVisualDraft, sections: ReadonlySet<StreamVisualSection>, preserveCoverSelection: boolean) {
  if (!preserveCoverSelection || !sections.has("cover")) return buildStreamVisualFields(draft, sections);
  const fields = buildStreamVisualFields(draft, new Set([...sections].filter((section) => section !== "cover")));
  return { ...fields, cover_start_active: draft.coverSource !== "none" && draft.coverStartActive };
}

// Keep a saved reference visible even when a list is unavailable or no longer
// contains that record. Refetching options never chooses a replacement value.
export function visualPresetOptions(rows: Record<string, unknown>[], selectedID: string, retainedLabel: string, selectedRevision?: number): VisualSelectOption[] {
  const options: VisualSelectOption[] = rows.map((row) => {
    const id = rowString(row, ["id"]);
    const revision = id === selectedID && selectedRevision ? selectedRevision : Number(row.revision || 0);
    return [id, `${rowString(row, ["name", "id"])}${revision > 0 ? ` (r${revision})` : ""}`];
  });
  if (selectedID && !options.some(([id]) => id === selectedID)) options.push([selectedID, retainedLabel, true]);
  return options;
}

export function PresetListStatus({ locale, allowed, authorityStatus, status, fetching, count, onRefresh, manageHref, manageLabel }: {
  locale: "ja" | "en";
  allowed: boolean;
  authorityStatus: "pending" | "error" | "success";
  status: "pending" | "error" | "success";
  fetching: boolean;
  count: number;
  onRefresh: () => void;
  manageHref: string;
  manageLabel: string;
}) {
  const message = authorityStatus === "pending"
    ? locale === "ja" ? "プリセットの参照権限を確認しています。" : "Checking preset read permission."
    : authorityStatus === "error"
      ? locale === "ja" ? "プリセットの参照権限を確認できません。ログイン状態を確認してください。" : "Preset read permission is unavailable. Check your sign-in state."
      : !allowed
        ? locale === "ja" ? "プリセットを参照する権限がありません。現在の選択は保持しています。" : "You do not have preset read permission. The current selection is retained."
        : status === "error"
          ? locale === "ja" ? "プリセット一覧を取得できません。現在の選択と入力内容は保持しています。" : "The preset list could not be loaded. Your selection and input are unchanged."
          : status === "pending" || fetching
            ? locale === "ja" ? "プリセット一覧を取得しています。" : "Loading presets."
            : count === 0
              ? locale === "ja" ? "選択できるプリセットはまだありません。管理画面で登録・有効化してください。" : "No presets are available. Create or enable one in the management page."
              : "";
  return <div className="space-y-2">
    {message ? <p role={authorityStatus === "error" || (allowed && status === "error") ? "alert" : "status"} className={allowed && status === "error" ? "text-xs text-destructive" : "text-xs text-muted-foreground"}>{message}</p> : null}
    {allowed && authorityStatus === "success" ? <div className="flex flex-wrap gap-2">
      <Button type="button" variant="outline" size="sm" disabled={fetching} onClick={onRefresh}><RefreshCcw aria-hidden="true" className="size-4" />{locale === "ja" ? "プリセット一覧を再取得" : "Refresh preset list"}</Button>
      <Button asChild type="button" variant="ghost" size="sm"><a href={manageHref} target="_blank" rel="noopener noreferrer"><ExternalLink aria-hidden="true" className="size-4" />{manageLabel}</a></Button>
    </div> : null}
  </div>;
}

function ModeSelect({ purpose, value, options, disabled, onChange }: { purpose: string; value: string; options: readonly VisualSelectOption[]; disabled?: boolean; onChange: (value: string) => void }) {
  const id = useId();
  return <div className="space-y-2"><label id={`${id}-label`} htmlFor={id} className="text-sm font-medium">{purpose}</label><Select value={value || "__select__"} onValueChange={onChange} disabled={disabled}><SelectTrigger id={id} aria-labelledby={`${id}-label`} className="w-full"><SelectValue /></SelectTrigger><SelectContent>{options.map(([optionValue, label, optionDisabled]) => <SelectItem key={optionValue || "empty"} value={optionValue || "__select__"} disabled={!optionValue || optionDisabled}>{label}</SelectItem>)}</SelectContent></Select></div>;
}
function UploadControl({ label, busy, disabled, onFile }: { label: string; busy: boolean; disabled?: boolean; onFile: (file?: File) => void }) { return <label className="inline-flex min-h-10 cursor-pointer items-center gap-2 rounded-md border px-3 text-sm has-[:disabled]:cursor-not-allowed has-[:disabled]:opacity-50">{busy ? <LoaderCircle className="size-4 animate-spin" /> : <ImageUp className="size-4" />}{label}<input className="sr-only" type="file" accept="image/png,image/jpeg,image/webp" disabled={disabled || busy} onChange={(event) => { const file = event.target.files?.[0]; event.target.value = ""; onFile(file); }} /></label>; }
function VisualPreview({ label, imageURL, emptyLabel }: { label: string; imageURL: string; emptyLabel: string }) { return <div className="relative aspect-video overflow-hidden rounded-md border bg-muted/40" role="img" aria-label={imageURL ? label : emptyLabel} data-crop="cover-center" style={imageURL ? { backgroundImage: `url(${JSON.stringify(imageURL)})`, backgroundPosition: "center", backgroundSize: "cover" } : undefined}><span className="absolute bottom-1 left-1 rounded bg-background/85 px-2 py-1 text-[11px]">{imageURL ? label : emptyLabel}</span></div>; }

function visualPermissionSnapshot(queryClient: ReturnType<typeof useQueryClient>): VisualActionPermissionSnapshot {
  const state = queryClient.getQueryState<CurrentUser>(["auth", "me"]);
  const current = queryClient.getQueryData<CurrentUser>(["auth", "me"]);
  if (state?.fetchStatus === "fetching") return { kind: "refreshing", permissions: [] };
  if (state?.status !== "success" || !current) return { kind: "unavailable", permissions: [] };
  return { kind: "ready", permissions: current.permissions || [] };
}
function visualStateSnapshot(queryClient: ReturnType<typeof useQueryClient>, key: readonly string[]): VisualActionStateSnapshot {
  const state = queryClient.getQueryState<StreamVisualSettings>(key);
  const current = queryClient.getQueryData<StreamVisualSettings>(key);
  const freshness = state?.fetchStatus === "fetching" ? "refreshing" : state?.status === "error" && current ? "stale" : state?.status === "success" && current ? "fresh" : "unavailable";
  return current ? { kind: "ready", freshness, revision: current.revision, fingerprint: visualSettingsFingerprint(current) } : { kind: "unknown", freshness };
}
function visualBlockedMessage(reason: string, locale: "ja" | "en", uiText: UICopy = japaneseCopy) {
  if (locale === "en") return reason === "authority-changed" ? "The settings changed. Reload before saving." : reason === "reconciliation-required" ? "Reload the latest state before another save." : "The visual settings cannot be saved with the current authority state.";
  return reason === "authority-changed" ? uiText("設定が変わりました。再読込してから保存してください。") : reason === "reconciliation-required" ? uiText("再操作せず、最新状態を再読込してください。") : uiText("現在の権限または状態ではビジュアル設定を保存できません。");
}
