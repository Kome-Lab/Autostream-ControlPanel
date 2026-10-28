"use client";
import NextImage from "next/image";
import { useUICopy } from "@/lib/i18n/ui-v2/use-ui-copy";
import { fixedPresentationText } from "@/lib/i18n/ui-v2/presentation-copy";

import { useCallback, useLayoutEffect, useId, useMemo, useRef, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Eye, EyeOff, LoaderCircle, RefreshCcw } from "lucide-react";

import { useI18n } from "@/components/admin/i18n-provider";
import { ConfirmationDialogFrame } from "@/components/foundation/confirmation/confirmation-dialog-frame";
import { Button } from "@/components/ui/button";
import { useCurrentUser, useServiceHealth } from "@/features/queries";
import { CoverCandidateGrid } from "@/features/streams/cover-candidate-grid";
import {
  buildVideoCoverAction,
  coverActionAvailability,
  createVideoCoverActionController,
  newVideoCoverIdempotencyKey,
  visualPresentation,
  type CoverCapabilitySnapshot,
  type CoverPermissionSnapshot,
  type StreamVisualSettings,
  type VideoCoverActionRequest,
  type VideoCoverState,
  type VideoCoverAuthorityProof,
  type VideoCoverAuthorityBaseline,
} from "@/features/streams/control-platform";
import { apiGet, apiPut } from "@/lib/api/client";
import { adaptAPIError } from "@/lib/foundation/api-errors/adapter";
import type { CurrentUser, Stream, WorkerNode } from "@/types/domain";
import { withBoundedActionRevalidation } from "@/lib/foundation/actions/bounded-revalidation";

class CoverActionUnavailable extends Error {}
type CoverAuthorityTarget = Pick<Stream, "id" | "status" | "assigned_encoder_id">;

export function StreamControlPlatformPanel({ stream }: { stream: Stream }) {
  return <StreamControlPlatformPanelContent key={stream.id} stream={stream} />;
}

function StreamControlPlatformPanelContent({ stream }: { stream: Stream }) {
  const streamRef = useRef(stream);
  useLayoutEffect(() => { streamRef.current = stream; }, [stream]);
  const { locale, t } = useI18n();
  const queryClient = useQueryClient();
  const currentUser = useCurrentUser();
  const serviceHealth = useServiceHealth();
  const [notice, setNotice] = useState("");
  const [selectedCandidate,setSelectedCandidate]=useState("");
  const [needsReconciliation, setNeedsReconciliation] = useState(false);
  const [hideOpen, setHideOpen] = useState(false);
  const uiText = useUICopy();
  const showReasonID = useId();
  const hideReasonID = useId();
  const visual = useQuery({
    queryKey: ["streams", stream.id, "visual-settings"],
    queryFn: ({ signal }) => apiGet<StreamVisualSettings>(`/streams/${encodeURIComponent(stream.id)}/visual-settings`, signal),
    retry: false,
  });
  const cover = useQuery({
    queryKey: ["streams", stream.id, "video-cover-state"],
    queryFn: ({ signal }) => apiGet<VideoCoverState>(`/streams/${encodeURIComponent(stream.id)}/video-cover-state`, signal),
    retry: false,
  });

  const permissionSnapshot = useCallback((): CoverPermissionSnapshot => {
    const state = queryClient.getQueryState<CurrentUser>(["auth", "me"]);
    const data = queryClient.getQueryData<CurrentUser>(["auth", "me"]);
    if (!data || state?.status !== "success") {
      return { status: state?.status === "error" ? "error" : "loading", permissions: [] };
    }
    return { status: "ready", permissions: data.permissions || [] };
  }, [queryClient]);
  const capabilitySnapshot = useCallback(
    () => {
      const target = queryClient.getQueryData<Stream[]>(["streams"])?.find(row => row.id === stream.id);
      return videoCoverCapabilitySnapshot(queryClient, target?.assigned_encoder_id);
    },
    [queryClient, stream.id],
  );
  const controller = useMemo(() => createVideoCoverActionController(
    permissionSnapshot,
    (request, signal) => apiPut<VideoCoverState>(`/streams/${encodeURIComponent(stream.id)}/video-cover-state`, request, signal),
    capabilitySnapshot,
    (request, signal, baseline) => {
      const target: CoverAuthorityTarget | undefined = baseline
        ? { id: stream.id, status: baseline.streamStatus, assigned_encoder_id: baseline.encoderID }
        : queryClient.getQueryData<Stream[]>(["streams"])?.find(row => row.id === stream.id);
      if (!target) return Promise.resolve<VideoCoverAuthorityProof>({ permission: { status: "error", permissions: [] }, capability: { status: "error", supported: false }, valid: false });
      const observed = baseline ?? captureVideoCoverAuthority(queryClient, target);
      return withBoundedActionRevalidation((freshSignal) => revalidateVideoCoverAuthority(queryClient, target, request, freshSignal, observed), signal);
    },
  ), [capabilitySnapshot, permissionSnapshot, queryClient, stream.id]);
  const controllerRef = useRef<typeof controller | null>(controller);
  type CoverIntent = Readonly<{ request: VideoCoverActionRequest; streamID: string; owner: typeof controller; baseline?: VideoCoverAuthorityBaseline }>;
  const hideIntent = useRef<CoverIntent | null>(null);
  useLayoutEffect(() => {
    controllerRef.current = controller;
    return () => { controllerRef.current = null; hideIntent.current = null; controller.cancel(); };
  }, [controller]);

  const action = useMutation({
    mutationFn: async (input: CoverIntent) => {
      if (input.streamID !== streamRef.current.id || input.owner !== controllerRef.current) throw new CoverActionUnavailable("対象の設定または担当Encoderが変更されました。最新状態を確認してください。");
      const result = await input.owner.issueRequest(input.request, input.baseline);
      if (!result.sent) throw new CoverActionUnavailable(result.reason);
      if (result.value?.stream_id !== input.streamID) throw new Error("video_cover_response_identity_invalid");
      return result.value;
    },
    onMutate: (input) => { if (input.streamID === streamRef.current.id && input.owner === controllerRef.current) setNotice(""); },
    onSuccess: async (state, input) => {
      if (input.streamID !== streamRef.current.id || input.owner !== controllerRef.current || state.stream_id !== input.streamID) return;
      const key = ["streams", input.streamID, "video-cover-state"];
      await queryClient.cancelQueries({ queryKey: key, exact: true });
      if (input.streamID !== streamRef.current.id || input.owner !== controllerRef.current) return;
      queryClient.setQueryData(key, state);
      if (state.status === "confirming") {
        input.owner.holdForReconciliation(state);
        setNeedsReconciliation(true);
        setNotice(locale === "ja" ? "Encoderの適用結果は未確定です。再送せず最新状態を確認してください。" : "The Encoder result is unconfirmed. Do not resend; refresh the latest state.");
      } else {
        setNeedsReconciliation(false);
        setNotice(locale === "ja" ? "フタ画像の適用結果を確認しました。" : "The Video Cover result is confirmed.");
      }
    },
    onError: (error, input) => {
      if (!(error instanceof CoverActionUnavailable)) input.owner.holdForReconciliation();
      if (input.streamID !== streamRef.current.id || input.owner !== controllerRef.current) return;
      setNeedsReconciliation(!(error instanceof CoverActionUnavailable));
      setNotice(error instanceof CoverActionUnavailable ? fixedPresentationText(error.message, uiText) : t(adaptAPIError(error).messageKey));
    },
  });
  const coverPending = action.isPending && action.variables?.streamID === stream.id && action.variables?.owner === controller;

  const permission: CoverPermissionSnapshot = currentUser.isError
    ? { status: "error", permissions: [] }
    : currentUser.isLoading
      ? { status: "loading", permissions: [] }
      : { status: "ready", permissions: currentUser.data?.permissions || [] };
  const capability: CoverCapabilitySnapshot = serviceHealth.isError
    ? { status: "error", supported: false }
    : serviceHealth.isLoading
      ? { status: "loading", supported: false }
      : coverCapabilityFromRows(serviceHealth.data || [], stream.assigned_encoder_id);
  const reconcileRequired = needsReconciliation || cover.data?.status === "confirming";
  const show = coverActionAvailability(true, permission, coverPending, capability, reconcileRequired);
  const hide = coverActionAvailability(false, permission, coverPending, capability, reconcileRequired);
  const coverStateUnavailableReason = locale === "ja" ? "フタ画像の状態を取得できるまで操作できません。" : "Video Cover controls are unavailable until its state is loaded.";
  const presentation = visualPresentation(visual.data, stream.name, locale);
  const candidates=visual.data?.cover_candidates || [];
  const selectedCandidateMissing = Boolean(selectedCandidate && !candidates.some(candidate => candidate.variant_id === selectedCandidate));
  const candidateID=selectedCandidate || [cover.data?.asset_variant_id,visual.data?.cover_variant_id].find(id=>id && candidates.some(candidate=>candidate.variant_id===id)) || "";
  const coverRuntimeReady = Boolean(cover.data && cover.data.job_generation > 0 && cover.data.desired_revision > 0);
  const runtimeUnavailableReason = locale === "ja" ? "配信が開始されるまでフタ画像は切り替えできません。" : "Cover controls are available after the stream starts.";
  const candidateMissingReason = locale === "ja" ? "選択していたフタ画像は現在の候補にありません。選び直してください。" : "The selected cover is no longer available. Choose a cover again.";
  const showReason = !cover.data ? coverStateUnavailableReason : !coverRuntimeReady ? runtimeUnavailableReason : !show.allowed ? fixedPresentationText(show.reason, uiText) : selectedCandidateMissing ? candidateMissingReason : "";
  const hideReason = !cover.data ? coverStateUnavailableReason : !coverRuntimeReady ? runtimeUnavailableReason : !hide.allowed ? fixedPresentationText(hide.reason, uiText) : "";
  const mediaURL=(variant:string)=>`/streams/${encodeURIComponent(stream.id)}/media-assets/${encodeURIComponent(variant)}`;
  const mismatch = cover.data && (cover.data.applied_revision !== cover.data.desired_revision || cover.data.applied_active !== cover.data.desired_active || (cover.data.applied_active && cover.data.applied_asset_variant_id !== cover.data.asset_variant_id));
  const issue = (active: boolean, hideConfirmed = false) => {
    if (!cover.data || !coverRuntimeReady || reconcileRequired || coverPending || active && selectedCandidateMissing) return;
    const baseline = captureVideoCoverAuthority(queryClient, stream, { health: serviceHealth.data, visual: visual.data });
    if (!baseline) { setNotice(t("confirmationRevalidationUnavailable")); return; }
    const request=buildVideoCoverAction(active, cover.data, newVideoCoverIdempotencyKey(), hideConfirmed);
    action.mutate({ request: active && candidateID ? {...request,candidate_variant_id:candidateID,expected_visual_revision:baseline.visualRevision} : request, streamID: stream.id, owner: controller, baseline });
  };
  const openHide = (open: boolean) => {
    hideIntent.current = null;
    if (!open) { setHideOpen(false); return; }
    const baseline = captureVideoCoverAuthority(queryClient, stream, { health: serviceHealth.data, visual: visual.data });
    if (!cover.data || !coverRuntimeReady || reconcileRequired || coverPending || !hide.allowed || !baseline) { setHideOpen(false); setNotice(t("confirmationRevalidationUnavailable")); return; }
    hideIntent.current = Object.freeze({ request: Object.freeze(buildVideoCoverAction(false, cover.data, newVideoCoverIdempotencyKey(), true)), streamID: stream.id, owner: controller, baseline });
    setHideOpen(true);
  };
  const confirmHide = () => {
    const intent = hideIntent.current;
    hideIntent.current = null;
    setHideOpen(false);
    if (!intent || intent.streamID !== streamRef.current.id || intent.owner !== controllerRef.current) return;
    action.mutate(intent);
  };
  const refresh = async () => {
    const targetID = stream.id;
    const owner = controller;
    const [coverResult] = await Promise.all([cover.refetch(), visual.refetch(), serviceHealth.refetch()]);
    if (targetID !== streamRef.current.id || owner !== controllerRef.current) return;
    if (coverResult.isSuccess && coverResult.data?.stream_id === targetID) {
      const reconciled = owner.reconcile(coverResult.data);
      setNeedsReconciliation(!reconciled || coverResult.data?.status === "confirming");
      setNotice(reconciled ? locale === "ja" ? "要求状態とEncoderの反映確認を読み込みました。" : "Loaded the latest desired and applied state." : t("confirmationOutcomeUnknown"));
    }
  };

  return (
    <section className="space-y-3 rounded-lg border p-4" aria-label={locale === "ja" ? "配信ビジュアルとVideo Cover" : "Stream visuals and Video Cover"}>
      <div className="flex flex-wrap items-center justify-between gap-2"><div><h3 className="text-sm font-semibold">{locale === "ja" ? "配信ビジュアル" : "Stream visuals"}</h3><p className="mt-1 text-xs text-muted-foreground">{locale==="ja"?"背景・参加者 → フタ画像 → ウォーターマーク → 配信・録画・プレビュー":"Scene → Cover → Watermark → live, recording and preview"}</p></div><Button type="button" variant="outline" size="sm" onClick={() => void refresh()} disabled={visual.isFetching || cover.isFetching || serviceHealth.isFetching}><RefreshCcw className={visual.isFetching || cover.isFetching ? "size-4 animate-spin" : "size-4"} />{locale === "ja" ? "更新" : "Refresh"}</Button></div>
      <div className="relative aspect-video overflow-hidden rounded-md border bg-muted/40" data-background-mode={visual.data?.background_mode || "default"}>
        {visual.data?.background_variant_id ? <NextImage unoptimized loading="eager" width={1920} height={1080} src={mediaURL(visual.data.background_variant_id)} alt={locale==="ja"?"保存済みの背景画像":"Saved background image"} className="absolute inset-0 size-full object-cover" /> : null}
        <div className="absolute inset-x-0 top-0 bg-black/65 px-3 py-2 text-sm font-semibold text-white">{presentation.title}</div>
        <div className="absolute inset-x-4 top-1/3 grid grid-cols-3 gap-3" aria-label={locale==="ja"?"参加者の配置見本":"Participant layout example"}>{[1,2,3].map((item)=><div key={item} className="aspect-square rounded-full border-2 border-white bg-black/30" />)}</div>
        {candidateID && !selectedCandidateMissing ? <NextImage unoptimized loading="eager" width={1920} height={1080} src={mediaURL(candidateID)} alt={locale==="ja"?"選択したフタ画像の設定プレビュー":"Selected cover settings preview"} className="absolute inset-0 size-full object-contain" /> : null}
        <div className="absolute left-2 top-2 z-20 rounded bg-black/75 px-2 py-1 text-xs text-white">{locale==="ja"?"設定プレビュー（実映像は配信プレビューで確認）":"Settings preview; check stream preview for actual video"}</div>
        <div className="absolute bottom-2 right-2 z-10 rounded bg-black/70 px-2 py-1 text-[11px] text-white" data-watermark-layer="topmost">{locale === "ja" ? "ウォーターマーク位置の見本" : "Watermark position example"}</div>
      </div>
      {visual.isError ? <p role="alert" className="text-sm text-destructive">{locale === "ja" ? "ビジュアル設定を読み込めませんでした。" : "Visual settings could not be loaded."}</p> : null}
      {presentation.warning ? <p role="status" className="text-sm text-amber-700 dark:text-amber-300">{presentation.warning}</p> : null}
      <dl className="grid gap-2 text-sm sm:grid-cols-2">
        <div><dt className="text-muted-foreground">Discord</dt><dd>{presentation.discord}</dd></div><div><dt className="text-muted-foreground">{locale === "ja" ? "開始時Cover" : "Start cover"}</dt><dd>{presentation.cover}</dd></div>
        <div><dt className="text-muted-foreground">{locale==="ja"?"要求した表示状態":"Requested state"}</dt><dd>{cover.data ? `${cover.data.desired_active ? "ON" : "OFF"} r${cover.data.desired_revision}` : locale === "ja" ? "未初期化" : "Not initialized"}</dd></div>
        <div><dt className="text-muted-foreground">{locale==="ja"?"Encoderの反映確認":"Encoder confirmed state"}</dt><dd>{cover.data?.applied_revision == null ? locale === "ja" ? "未確認" : "Unconfirmed" : `${cover.data.applied_active ? "ON" : "OFF"} r${cover.data.applied_revision}`}</dd></div>
        <div><dt className="text-muted-foreground">{locale==="ja"?"実行世代・確認状況":"Execution and confirmation"}</dt><dd>{cover.data ? `${locale==="ja"?"実行世代":"Generation"} ${cover.data.job_generation} / ${locale==="ja"?({idle:"未反映",confirming:"反映を照合中",applied:"Encoderで反映確認済み",failed:"反映失敗"}[cover.data.status]||"状態不明"):cover.data.status}` : "—"}</dd></div><div><dt className="text-muted-foreground">{locale === "ja" ? "最終確認" : "Last confirmed"}</dt><dd>{cover.data?.applied_revision == null ? "—" : `r${cover.data.applied_revision}`}</dd></div>
      </dl>
      {mismatch || reconcileRequired ? <p role="status" className="rounded-md border border-amber-300 bg-amber-50 px-3 py-2 text-sm text-amber-900 dark:bg-amber-950/30 dark:text-amber-200">{locale === "ja" ? "要求した状態の反映は未確認です。現在の画像を保持し、再送せずEncoderの状態を読み取ります。" : "Desired and applied differ. Desired is not shown as applied; refresh the confirmed Encoder state without resending."}</p> : null}
      {notice ? <p role="status" aria-live="polite" className="text-sm text-muted-foreground">{notice}</p> : null}
      {candidates.length ? <CoverCandidateGrid candidates={candidates} locale={locale} imageURL={(candidate) => mediaURL(candidate.variant_id)} startVariantID={visual.data?.cover_variant_id} selectedVariantID={candidateID} appliedVariantID={cover.data?.applied_active ? cover.data.applied_asset_variant_id : undefined} actions={(candidate) => <Button type="button" variant="outline" size="sm" className="h-auto min-h-10 min-w-0 whitespace-normal [overflow-wrap:anywhere]" disabled={coverPending || reconcileRequired || candidateID === candidate.variant_id} onClick={() => setSelectedCandidate(candidate.variant_id)}>{candidateID === candidate.variant_id ? locale === "ja" ? "切替対象" : "Selected for switching" : locale === "ja" ? "切替対象にする" : "Select for switching"}</Button>} /> : null}
      {candidates.length || selectedCandidateMissing ? <label className="block space-y-2 text-sm"><span>{locale==="ja"?"表示・切替するフタ画像":"Cover image to display"}</span><select aria-label={locale==="ja"?"切替するフタ画像":"Cover image to switch"} className="w-full rounded border bg-background p-2" value={candidateID} onChange={(event)=>setSelectedCandidate(event.target.value)} disabled={coverPending||reconcileRequired}>{selectedCandidateMissing ? <option value={selectedCandidate} disabled>{candidateMissingReason}</option> : null}{candidates.map((candidate)=><option key={candidate.variant_id} value={candidate.variant_id}>{candidate.name}</option>)}</select></label> : null}
      {cover.data?.applied_active && cover.data.applied_asset_variant_id ? <figure className="space-y-1"><NextImage unoptimized loading="eager" width={1920} height={1080} src={mediaURL(cover.data.applied_asset_variant_id)} alt={locale==="ja"?"Encoderで反映確認済みのフタ画像":"Encoder confirmed cover"} className="aspect-video w-full rounded border object-contain"/><figcaption className="text-xs text-muted-foreground">{locale==="ja"?"Encoderが反映を確認した画像（実映像の取得結果ではありません）":"Encoder confirmed image; this is not a captured video frame"}</figcaption></figure> : null}
      <div className="flex flex-wrap gap-2">
        <Button type="button" size="sm" className="h-auto min-w-0 max-w-full whitespace-normal [overflow-wrap:anywhere]" aria-label={locale === "ja" ? "Video Coverを表示" : "Show Video Cover"} aria-describedby={showReason ? showReasonID : undefined} onClick={() => issue(true)} disabled={!cover.data || !coverRuntimeReady || !show.allowed || !candidateID || selectedCandidateMissing}>{coverPending ? <LoaderCircle className="size-4 animate-spin" /> : <Eye className="size-4" />}{locale === "ja" ? "選択したフタを表示・切替" : "Show or switch selected cover"}</Button>
        <ConfirmationDialogFrame open={hideOpen} onOpenChange={openHide} trigger={<Button type="button" variant="outline" size="sm" aria-label={locale === "ja" ? "Video Coverを非表示" : "Hide Video Cover"} aria-describedby={hideReason ? hideReasonID : undefined} disabled={!cover.data || !coverRuntimeReady || !hide.allowed}><EyeOff className="size-4" />{locale === "ja" ? "Coverを非表示" : "Hide cover"}</Button>} title={locale === "ja" ? "Video Coverを非表示" : "Hide Video Cover"} description={locale === "ja" ? "背後の映像が公開される可能性があります。Watermark設定やrevisionは変更されません。" : "The underlying video may become public. Watermark configuration and revision are not changed."} cancelLabel={locale === "ja" ? "キャンセル" : "Cancel"} actionLabel={locale === "ja" ? "Coverを非表示" : "Hide cover"} actionDisabled={coverPending} actionClosesDialog onConfirm={confirmHide} />
        {reconcileRequired ? <Button type="button" variant="outline" size="sm" aria-label={locale === "ja" ? "Coverの最新状態を確認" : "Refresh cover state"} onClick={() => void refresh()} disabled={cover.isFetching}><RefreshCcw className="size-4" />{locale === "ja" ? "再送せず最新状態を確認" : "Refresh without resending"}</Button> : null}
      </div>
      {showReason ? <p id={showReasonID} className="text-xs text-muted-foreground">{locale === "ja" ? "表示" : "Show"}: {showReason}</p> : null}
      {hideReason ? <p id={hideReasonID} className="text-xs text-muted-foreground">{locale === "ja" ? "非表示" : "Hide"}: {hideReason}</p> : null}
    </section>
  );
}

function videoCoverCapabilitySnapshot(queryClient: ReturnType<typeof useQueryClient>, assignedEncoderID?: string): CoverCapabilitySnapshot {
  const state = queryClient.getQueryState<WorkerNode[]>(["service-health"]);
  const rows = queryClient.getQueryData<WorkerNode[]>(["service-health"]);
  if (state?.status !== "success" || !rows) return { status: state?.status === "error" ? "error" : "loading", supported: false };
  return coverCapabilityFromRows(rows, assignedEncoderID);
}
function coverCapabilityFromRows(rows: WorkerNode[], assignedEncoderID?: string): CoverCapabilitySnapshot {
  const encoder = rows.find((row) => row.service_type === "encoder_recorder" && (row.service_id || row.id) === assignedEncoderID);
  return { status: "ready", supported: encoder?.reported_capabilities?.live_video_cover_v1 === true };
}

function videoCoverEncoderAuthority(row: WorkerNode) {
  return JSON.stringify([row.service_id || row.id, row.service_type, row.current_stream_id || "", row.ownership_epoch || 0, row.execution_host_id || "", row.transport_mode || ""]);
}

function captureVideoCoverAuthority(
  queryClient: ReturnType<typeof useQueryClient>, stream: CoverAuthorityTarget,
  observed: Readonly<{ health?: WorkerNode[]; visual?: StreamVisualSettings }> = {
    health: queryClient.getQueryData<WorkerNode[]>(["service-health"]),
    visual: queryClient.getQueryData<StreamVisualSettings>(["streams", stream.id, "visual-settings"]),
  },
): VideoCoverAuthorityBaseline | undefined {
  const encoder = Array.isArray(observed.health) ? observed.health.find((row) => (row.service_id || row.id) === stream.assigned_encoder_id) : undefined;
  const visual = observed.visual;
  if (!stream.assigned_encoder_id || !encoder || !visual || visual.stream_id !== stream.id || !Number.isSafeInteger(visual.revision) || visual.revision < 0) return undefined;
  return Object.freeze({ streamID: stream.id, streamStatus: stream.status, encoderID: stream.assigned_encoder_id, encoderAuthority: videoCoverEncoderAuthority(encoder), visualRevision: visual.revision });
}

async function revalidateVideoCoverAuthority(
  queryClient: ReturnType<typeof useQueryClient>, stream: CoverAuthorityTarget, request: VideoCoverActionRequest, signal: AbortSignal,
  baseline: VideoCoverAuthorityBaseline | undefined = captureVideoCoverAuthority(queryClient, stream),
): Promise<VideoCoverAuthorityProof> {
  if (!baseline || baseline.streamID !== stream.id) return { permission: { status: "error", permissions: [] }, capability: { status: "error", supported: false }, valid: false };
  const encoderID = baseline.encoderID;
  const expectedVisual = request.expected_visual_revision ?? baseline.visualRevision;
  const [user, current, visual, health, cover] = await Promise.all([
    apiGet<CurrentUser>("/auth/me", signal),
    apiGet<Stream>(`/streams/${encodeURIComponent(stream.id)}`, signal),
    apiGet<StreamVisualSettings>(`/streams/${encodeURIComponent(stream.id)}/visual-settings`, signal),
    apiGet<WorkerNode[]>("/service-health", signal),
    apiGet<VideoCoverState>(`/streams/${encodeURIComponent(stream.id)}/video-cover-state`, signal),
  ]);
  const permission: CoverPermissionSnapshot = Array.isArray(user?.permissions) ? { status: "ready", permissions: user.permissions } : { status: "error", permissions: [] };
  const capability: CoverCapabilitySnapshot = Array.isArray(health) ? coverCapabilityFromRows(health, encoderID) : { status: "error", supported: false };
  const currentEncoder = Array.isArray(health) ? health.find((row) => (row.service_id || row.id) === encoderID) : undefined;
  const candidateMatches = !request.candidate_variant_id || visual.cover_variant_id === request.candidate_variant_id || visual.cover_candidates?.some((candidate) => candidate.variant_id === request.candidate_variant_id);
  const valid = !signal.aborted && current?.id === baseline.streamID && current.status === baseline.streamStatus && current.assigned_encoder_id === encoderID
    && Boolean(currentEncoder && videoCoverEncoderAuthority(currentEncoder) === baseline.encoderAuthority)
    && visual.stream_id === stream.id && Number.isSafeInteger(expectedVisual) && expectedVisual === baseline.visualRevision && visual.revision === expectedVisual && Boolean(candidateMatches)
    && cover.stream_id === stream.id && Number.isSafeInteger(request.expected_job_generation) && request.expected_job_generation >= 1 && Number.isSafeInteger(request.expected_revision) && request.expected_revision >= 1
    && cover.job_generation === request.expected_job_generation && cover.desired_revision === request.expected_revision && cover.status !== "confirming";
  // A later observer refresh cannot invalidate this one proof. No direct-read
  // cache publication can overwrite a newer mutation response.
  return { permission, capability, valid, reason: valid ? "" : "対象の設定または担当Encoderが変更されました。最新状態を確認してください。" };
}
