"use client";

import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { ImageUp, LoaderCircle, RefreshCcw, Save } from "lucide-react";

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
import { adaptAPIError } from "@/lib/foundation/api-errors/adapter";
import type { CurrentUser, Stream } from "@/types/domain";
import { useQuery, useQueryClient } from "@tanstack/react-query";

export type StreamCreateVisualState = Readonly<{
  extension: Record<string, unknown>;
  ready: boolean;
  discordTargetReady: boolean;
}>;

type Props = {
  stream?: Stream | null;
  canUpdate: boolean;
  onCreateState: (state: StreamCreateVisualState) => void;
};

export function StreamVisualSettingsSection({ stream, canUpdate, onCreateState }: Props) {
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
  const discordPresets = useResourceData<unknown>("/discord/target-presets");
  const coverPresets = useResourceData<unknown>("/video-cover-presets");
  const serviceHealth = useServiceHealth(editing);
  useCurrentUser();
  const [draft, setDraft] = useState(() => defaultStreamVisualDraft());
  const [dirtySections, setDirtySections] = useState<ReadonlySet<StreamVisualSection>>(() => new Set());
  const [uploadedBackground, setUploadedBackground] = useState(false);
  const [uploadedCover, setUploadedCover] = useState(false);
  const [backgroundPreview, setBackgroundPreview] = useState("");
  const [coverPreview, setCoverPreview] = useState("");
  const [uploading, setUploading] = useState<"background" | "cover" | "">("");
  const [saving, setSaving] = useState(false);
  const [needsRefresh, setNeedsRefresh] = useState(false);
  const [message, setMessage] = useState("");
  const initialized = useRef(false);
  const previewOwner = useMemo(() => createStreamVisualPreviewOwner((url) => URL.revokeObjectURL(url)), []);

  useEffect(() => {
    if (!editing || initialized.current || !visual.data) return;
    initialized.current = true;
    setDraft(streamVisualDraftFromSettings(visual.data));
  }, [editing, visual.data]);
  useEffect(() => () => previewOwner.release(), [previewOwner]);

  const validation = useMemo(() => validateStreamVisualDraft(draft), [draft]);
  const createState = useMemo<StreamCreateVisualState>(() => ({
    extension: buildStreamCreateVisualExtension(draft, dirtySections),
    ready: validation.ready,
    discordTargetReady: draft.discordTargetMode === "preset"
      ? draft.discordTargetPresetID.trim() !== "" && draft.discordTargetPresetRevision > 0
      : draft.discordTargetMode === "manual"
        ? [draft.discordGuildID, draft.discordTextChannelID, draft.discordVoiceChannelID].every((value) => value.trim() !== "")
        : false,
  }), [dirtySections, draft, validation.ready]);
  useEffect(() => { if (!editing) onCreateState(createState); }, [createState, editing, onCreateState]);

  const discordRows = useMemo(() => normalizeRows(discordPresets.data), [discordPresets.data]);
  const coverRows = useMemo(() => normalizeRows(coverPresets.data).filter((row) => row.enabled !== false), [coverPresets.data]);
  const selectedDiscord = discordRows.find((row) => rowString(row, ["id"]) === draft.discordTargetPresetID);
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
    setDirtySections((current) => new Set([...current, section]));
    setDraft((current) => ({ ...current, ...fields }));
  }, []);

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
      setMessage(locale === "ja" ? "画像を検証し、1920x1080 variant を準備しました。保存時に配信枠へclaimします。" : "The image is verified and its 1920x1080 variant will be claimed when you save.");
    } catch (error) {
      setMessage(t(adaptAPIError(error).messageKey));
    } finally {
      setUploading("");
    }
  };

  const save = async () => {
    if (!visual.data || !validation.ready || saving || needsRefresh) return;
    setSaving(true);
    setMessage("");
    const result = await controller.issue(buildStreamVisualFields(draft, dirtySections), draft.uploadSessionID || undefined);
    setSaving(false);
    if (result.kind === "succeeded") {
      queryClient.setQueryData(queryKey, result.value);
      setDraft(streamVisualDraftFromSettings(result.value));
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
      setMessage(visualBlockedMessage(result.reason, locale));
    }
  };
  const refresh = async () => {
    const result = await visual.refetch();
    if (result.data) {
      controller.reconcile();
      setNeedsRefresh(false);
      setDraft(streamVisualDraftFromSettings(result.data));
      setDirtySections(new Set());
      setUploadedBackground(false);
      setUploadedCover(false);
      setMessage(locale === "ja" ? "サーバーの最新設定を読み込みました。" : "Loaded the latest server settings.");
    }
  };

  const controlsDisabled = editing && (!visual.data || visual.isFetching);
  const saveDisabled = !canUpdate || controlsDisabled || dirtySections.size === 0 || !validation.ready || saving || needsRefresh;
  return (
    <fieldset className="space-y-4 rounded-lg border p-4" aria-describedby="stream-visual-help">
      <legend className="px-1 text-sm font-semibold">{locale === "ja" ? "背景・タイトル・Discord・Video Cover" : "Background, title, Discord, and Video Cover"}</legend>
      <p id="stream-visual-help" className="text-xs text-muted-foreground">{locale === "ja" ? "既定値は従来動作を維持します。カスタム設定は対応capabilityを報告したNodeだけへ開始時に送信されます。" : "Defaults preserve legacy behavior. Custom settings are sent only to assigned nodes reporting the required capability."}</p>
      {visual.isError ? <p role="alert" className="text-sm text-destructive">{locale === "ja" ? "ビジュアル設定を取得できません。保存せず再読込してください。" : "Visual settings are unavailable. Reload before saving."}</p> : null}
      <div className="grid gap-4 xl:grid-cols-2">
        <VisualGroup title={locale === "ja" ? "シーン背景" : "Scene background"}>
          <ModeSelect value={draft.backgroundMode} disabled={controlsDisabled || uploadedBackground} onChange={(value) => update("background", { backgroundMode: value as StreamVisualDraft["backgroundMode"] })} options={[["default", locale === "ja" ? "既定" : "Default"], ["image", locale === "ja" ? "画像（cover / center crop）" : "Image (cover / center crop)"]]} />
          {draft.backgroundMode === "image" ? <UploadControl label={locale === "ja" ? "背景画像をアップロード" : "Upload background image"} busy={uploading === "background"} disabled={controlsDisabled || uploadedBackground} onFile={(file) => void upload("background", file)} /> : null}
          <VisualPreview label={locale === "ja" ? "背景 center crop preview" : "Background center-crop preview"} imageURL={backgroundPreview} />
        </VisualGroup>
        <VisualGroup title={locale === "ja" ? "ヘッダータイトル" : "Header title"}>
          <ModeSelect value={draft.headerTitleMode} disabled={controlsDisabled} onChange={(value) => update("title", { headerTitleMode: value as StreamVisualDraft["headerTitleMode"] })} options={[["default", locale === "ja" ? "配信枠名を使用" : "Use stream name"], ["custom", locale === "ja" ? "カスタム" : "Custom"]]} />
          {draft.headerTitleMode === "custom" ? <Input aria-label={locale === "ja" ? "カスタムヘッダータイトル" : "Custom header title"} maxLength={80} value={draft.headerTitleValue} disabled={controlsDisabled} onChange={(event) => update("title", { headerTitleValue: event.target.value })} /> : null}
        </VisualGroup>
        <VisualGroup title={locale === "ja" ? "Discord配信先" : "Discord target"}>
          <ModeSelect value={draft.discordTargetMode} disabled={controlsDisabled} onChange={(value) => update("discord", { discordTargetMode: value as StreamVisualDraft["discordTargetMode"] })} options={[["inherit", locale === "ja" ? "従来設定を継承" : "Inherit legacy target"], ["preset", locale === "ja" ? "プリセット snapshot" : "Preset snapshot"], ["manual", locale === "ja" ? "手動" : "Manual"]]} />
          {draft.discordTargetMode === "preset" ? <ModeSelect value={draft.discordTargetPresetID} disabled={controlsDisabled} onChange={(value) => { const row = discordRows.find((item) => rowString(item, ["id"]) === value); update("discord", { discordTargetPresetID: value, discordTargetPresetRevision: Number(rowString(row || {}, ["revision"])) || 0 }); }} options={[["", locale === "ja" ? "選択してください" : "Select a preset"], ...discordRows.map((row) => [rowString(row, ["id"]), `${rowString(row, ["name", "id"])} (r${rowString(row, ["revision"])})`] as [string, string])]} /> : null}
          {draft.discordTargetMode === "manual" ? <div className="grid gap-2 sm:grid-cols-3"><Input aria-label="Discord Guild ID" value={draft.discordGuildID} disabled={controlsDisabled} onChange={(event) => update("discord", { discordGuildID: event.target.value })} /><Input aria-label="Discord Text Channel ID" value={draft.discordTextChannelID} disabled={controlsDisabled} onChange={(event) => update("discord", { discordTextChannelID: event.target.value })} /><Input aria-label="Discord Voice Channel ID" value={draft.discordVoiceChannelID} disabled={controlsDisabled} onChange={(event) => update("discord", { discordVoiceChannelID: event.target.value })} /></div> : null}
          {visual.data?.discord_preset_deleted ? <p role="status" className="text-xs text-amber-700 dark:text-amber-300">{locale === "ja" ? "元のプリセットは削除済みです。保存済みsnapshotは変更されません。" : "The source preset was deleted; the saved snapshot remains unchanged."}</p> : null}
          {selectedDiscord ? <p className="text-xs text-muted-foreground">{locale === "ja" ? "保存時にサーバーが現在のプリセットをsnapshotします。" : "The server snapshots the current preset when saved."}</p> : null}
        </VisualGroup>
        <VisualGroup title="Video Cover">
          <ModeSelect value={draft.coverSource} disabled={controlsDisabled || uploadedCover} onChange={(value) => update("cover", { coverSource: value as StreamVisualDraft["coverSource"], ...(value === "none" ? { coverStartActive: false } : {}) })} options={[["none", "OFF"], ["preset", locale === "ja" ? "プリセット" : "Preset"], ["upload", locale === "ja" ? "アップロード" : "Upload"]]} />
          {draft.coverSource === "preset" ? <ModeSelect value={draft.coverPresetID} disabled={controlsDisabled} onChange={(value) => update("cover", { coverPresetID: value })} options={[["", locale === "ja" ? "選択してください" : "Select a preset"], ...coverRows.map((row) => [rowString(row, ["id"]), `${rowString(row, ["name", "id"])} (r${rowString(row, ["revision"])})`] as [string, string])]} /> : null}
          {draft.coverSource === "upload" ? <><UploadControl label={locale === "ja" ? "Cover画像をアップロード" : "Upload cover image"} busy={uploading === "cover"} disabled={controlsDisabled || uploadedCover} onFile={(file) => void upload("cover", file)} /><VisualPreview label="Video Cover 16:9 preview" imageURL={coverPreview} /></> : null}
          {draft.coverSource !== "none" ? <label className="flex min-h-10 items-center gap-2 rounded-md border px-3 text-sm"><Checkbox checked={draft.coverStartActive} disabled={controlsDisabled} onCheckedChange={(value) => update("cover", { coverStartActive: value === true })} />{locale === "ja" ? "配信開始時からCoverを表示" : "Show cover when the stream starts"}</label> : null}
          <p className="text-xs text-muted-foreground">Base / Worker scene → Video Cover → Watermark → Encode → tee</p>
        </VisualGroup>
      </div>
      {[...validation.issues, ...capabilityWarnings].map((issue) => <p key={issue} role="status" className="rounded-md border border-amber-300 bg-amber-50 p-2 text-xs text-amber-900 dark:bg-amber-950/30 dark:text-amber-200">{issue}</p>)}
      {message ? <p aria-live="polite" className="text-sm text-muted-foreground">{message}</p> : null}
      {editing ? <div className="flex flex-wrap justify-end gap-2">
        <Button type="button" variant="outline" onClick={() => void refresh()} disabled={visual.isFetching}><RefreshCcw className="size-4" />{locale === "ja" ? "最新設定を再読込" : "Reload latest"}</Button>
        <ConfirmationDialogFrame trigger={<Button type="button" disabled={saveDisabled}><Save className="size-4" />{locale === "ja" ? "ビジュアル設定を保存" : "Save visual settings"}</Button>} title={locale === "ja" ? "ビジュアル設定を保存" : "Save visual settings"} description={locale === "ja" ? "開始時のシーン、Discord snapshot、Cover設定が変わります。現在の配信中には適用されません。" : "This changes the next-start scene, Discord snapshot, and cover settings; it does not alter the active stream."} cancelLabel={locale === "ja" ? "キャンセル" : "Cancel"} actionLabel={locale === "ja" ? "保存" : "Save"} actionClosesDialog onConfirm={() => void save()} />
      </div> : null}
    </fieldset>
  );
}

function VisualGroup({ title, children }: { title: string; children: React.ReactNode }) { return <section className="space-y-3 rounded-md border bg-muted/10 p-3"><h3 className="text-sm font-medium">{title}</h3>{children}</section>; }
function ModeSelect({ value, options, disabled, onChange }: { value: string; options: Array<[string, string]>; disabled?: boolean; onChange: (value: string) => void }) { return <Select value={value} onValueChange={onChange} disabled={disabled}><SelectTrigger className="w-full"><SelectValue /></SelectTrigger><SelectContent>{options.map(([optionValue, label]) => <SelectItem key={optionValue || "empty"} value={optionValue || "__select__"} disabled={!optionValue}>{label}</SelectItem>)}</SelectContent></Select>; }
function UploadControl({ label, busy, disabled, onFile }: { label: string; busy: boolean; disabled?: boolean; onFile: (file?: File) => void }) { return <label className="inline-flex min-h-10 cursor-pointer items-center gap-2 rounded-md border px-3 text-sm has-[:disabled]:cursor-not-allowed has-[:disabled]:opacity-50">{busy ? <LoaderCircle className="size-4 animate-spin" /> : <ImageUp className="size-4" />}{label}<input className="sr-only" type="file" accept="image/png,image/jpeg,image/webp" disabled={disabled || busy} onChange={(event) => onFile(event.target.files?.[0])} /></label>; }
function VisualPreview({ label, imageURL }: { label: string; imageURL: string }) { return <div className="relative aspect-video overflow-hidden rounded-md border bg-muted/40" role="img" aria-label={label} data-crop="cover-center" style={imageURL ? { backgroundImage: `url(${JSON.stringify(imageURL)})`, backgroundPosition: "center", backgroundSize: "cover" } : undefined}><span className="absolute bottom-1 left-1 rounded bg-background/85 px-2 py-1 text-[11px]">{imageURL ? label : `${label}: pending selection`}</span></div>; }

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
function visualBlockedMessage(reason: string, locale: "ja" | "en") {
  if (locale === "en") return reason === "authority-changed" ? "The settings changed. Reload before saving." : reason === "reconciliation-required" ? "Reload the latest state before another save." : "The visual settings cannot be saved with the current authority state.";
  return reason === "authority-changed" ? "設定が変わりました。再読込してから保存してください。" : reason === "reconciliation-required" ? "再操作せず、最新状態を再読込してください。" : "現在の権限または状態ではビジュアル設定を保存できません。";
}
