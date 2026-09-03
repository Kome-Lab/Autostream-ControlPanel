"use client";

import { useCallback, useId, useMemo, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Eye, EyeOff, LoaderCircle, RefreshCcw } from "lucide-react";

import { useI18n } from "@/components/admin/i18n-provider";
import { ConfirmationDialogFrame } from "@/components/foundation/confirmation/confirmation-dialog-frame";
import { Button } from "@/components/ui/button";
import { useCurrentUser, useServiceHealth } from "@/features/queries";
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
} from "@/features/streams/control-platform";
import { apiGet, apiPut } from "@/lib/api/client";
import { adaptAPIError } from "@/lib/foundation/api-errors/adapter";
import type { CurrentUser, Stream, WorkerNode } from "@/types/domain";

class CoverActionUnavailable extends Error {}

export function StreamControlPlatformPanel({ stream }: { stream: Stream }) {
  const { locale, t } = useI18n();
  const queryClient = useQueryClient();
  const currentUser = useCurrentUser();
  const serviceHealth = useServiceHealth();
  const [notice, setNotice] = useState("");
  const [needsReconciliation, setNeedsReconciliation] = useState(false);
  const showReasonID = useId();
  const hideReasonID = useId();
  const visual = useQuery({
    queryKey: ["streams", stream.id, "visual-settings"],
    queryFn: () => apiGet<StreamVisualSettings>(`/streams/${encodeURIComponent(stream.id)}/visual-settings`),
    retry: false,
  });
  const cover = useQuery({
    queryKey: ["streams", stream.id, "video-cover-state"],
    queryFn: () => apiGet<VideoCoverState>(`/streams/${encodeURIComponent(stream.id)}/video-cover-state`),
    retry: false,
  });

  const permissionSnapshot = useCallback((): CoverPermissionSnapshot => {
    const state = queryClient.getQueryState<CurrentUser>(["auth", "me"]);
    const data = queryClient.getQueryData<CurrentUser>(["auth", "me"]);
    if (!data || state?.status !== "success" || state.fetchStatus !== "idle") {
      return { status: state?.status === "error" ? "error" : "loading", permissions: [] };
    }
    return { status: "ready", permissions: data.permissions || [] };
  }, [queryClient]);
  const capabilitySnapshot = useCallback(
    () => videoCoverCapabilitySnapshot(queryClient, stream.assigned_encoder_id),
    [queryClient, stream.assigned_encoder_id],
  );
  const controller = useMemo(() => createVideoCoverActionController(
    permissionSnapshot,
    (request) => apiPut<VideoCoverState>(`/streams/${encodeURIComponent(stream.id)}/video-cover-state`, request),
    capabilitySnapshot,
  ), [capabilitySnapshot, permissionSnapshot, stream.id]);

  const action = useMutation({
    mutationFn: async (input: VideoCoverActionRequest) => {
      const result = await controller.issueRequest(input);
      if (!result.sent) throw new CoverActionUnavailable(result.reason);
      return result.value;
    },
    onMutate: () => setNotice(""),
    onSuccess: (state) => {
      queryClient.setQueryData(["streams", stream.id, "video-cover-state"], state);
      if (state.status === "confirming") {
        controller.holdForReconciliation();
        setNeedsReconciliation(true);
        setNotice(locale === "ja" ? "Encoderの適用結果は未確定です。再送せず最新状態を確認してください。" : "The Encoder result is unconfirmed. Do not resend; refresh the latest state.");
      } else {
        setNeedsReconciliation(false);
        setNotice(locale === "ja" ? "Video Coverの適用結果を確認しました。" : "The Video Cover result is confirmed.");
      }
    },
    onError: (error) => {
      setNeedsReconciliation(!(error instanceof CoverActionUnavailable));
      setNotice(error instanceof CoverActionUnavailable ? error.message : t(adaptAPIError(error).messageKey));
    },
  });

  const permission: CoverPermissionSnapshot = currentUser.isError
    ? { status: "error", permissions: [] }
    : currentUser.isLoading || currentUser.isFetching
      ? { status: "loading", permissions: [] }
      : { status: "ready", permissions: currentUser.data?.permissions || [] };
  const capability: CoverCapabilitySnapshot = serviceHealth.isError
    ? { status: "error", supported: false }
    : serviceHealth.isLoading || serviceHealth.isFetching
      ? { status: "loading", supported: false }
      : coverCapabilityFromRows(serviceHealth.data || [], stream.assigned_encoder_id);
  const reconcileRequired = needsReconciliation || cover.data?.status === "confirming";
  const show = coverActionAvailability(true, permission, action.isPending, capability, reconcileRequired);
  const hide = coverActionAvailability(false, permission, action.isPending, capability, reconcileRequired);
  const coverStateUnavailableReason = locale === "ja" ? "Video Coverの状態を取得できるまで操作できません。" : "Video Cover controls are unavailable until its state is loaded.";
  const showReason = !cover.data ? coverStateUnavailableReason : !show.allowed ? show.reason : "";
  const hideReason = !cover.data ? coverStateUnavailableReason : !hide.allowed ? hide.reason : "";
  const presentation = visualPresentation(visual.data, stream.name, locale);
  const mismatch = cover.data && (cover.data.applied_revision !== cover.data.desired_revision || cover.data.applied_active !== cover.data.desired_active);
  const issue = (active: boolean, hideConfirmed = false) => {
    if (!cover.data || reconcileRequired) return;
    action.mutate(buildVideoCoverAction(active, cover.data, newVideoCoverIdempotencyKey(), hideConfirmed));
  };
  const refresh = async () => {
    const [coverResult] = await Promise.all([cover.refetch(), visual.refetch(), serviceHealth.refetch()]);
    if (coverResult.isSuccess) {
      controller.reconcile();
      setNeedsReconciliation(coverResult.data?.status === "confirming");
      setNotice(locale === "ja" ? "最新のDesired / Applied状態を読み込みました。" : "Loaded the latest desired and applied state.");
    }
  };

  return (
    <section className="space-y-3 rounded-lg border p-4" aria-label={locale === "ja" ? "配信ビジュアルとVideo Cover" : "Stream visuals and Video Cover"}>
      <div className="flex flex-wrap items-center justify-between gap-2"><div><h3 className="text-sm font-semibold">{locale === "ja" ? "配信ビジュアル" : "Stream visuals"}</h3><p className="mt-1 text-xs text-muted-foreground">Base / Worker scene → Video Cover → Watermark → Encode → tee</p></div><Button type="button" variant="outline" size="sm" onClick={() => void refresh()} disabled={visual.isFetching || cover.isFetching || serviceHealth.isFetching}><RefreshCcw className={visual.isFetching || cover.isFetching ? "size-4 animate-spin" : "size-4"} />{locale === "ja" ? "更新" : "Refresh"}</Button></div>
      <div className="relative aspect-video overflow-hidden rounded-md border bg-muted/40" data-background-mode={visual.data?.background_mode || "default"}>
        <div className="absolute inset-0 grid place-items-center px-6 text-center"><div><div className="text-lg font-semibold">{presentation.title}</div><div className="mt-1 text-xs text-muted-foreground">{presentation.background}</div></div></div>
        {cover.data?.applied_active === true ? <div className="absolute inset-0 grid place-items-center bg-black/65 text-sm font-semibold text-white" data-video-cover="applied">Video Cover {locale === "ja" ? "適用確認済み" : "confirmed applied"}</div> : null}
        <div className="absolute bottom-2 right-2 z-10 rounded bg-black/70 px-2 py-1 text-[11px] text-white" data-watermark-layer="topmost">Watermark: {locale === "ja" ? "Coverと独立した後段レイヤー" : "independent layer after Cover"}</div>
      </div>
      {visual.isError ? <p role="alert" className="text-sm text-destructive">{locale === "ja" ? "ビジュアル設定を読み込めませんでした。" : "Visual settings could not be loaded."}</p> : null}
      {presentation.warning ? <p role="status" className="text-sm text-amber-700 dark:text-amber-300">{presentation.warning}</p> : null}
      <dl className="grid gap-2 text-sm sm:grid-cols-2">
        <div><dt className="text-muted-foreground">Discord</dt><dd>{presentation.discord}</dd></div><div><dt className="text-muted-foreground">{locale === "ja" ? "開始時Cover" : "Start cover"}</dt><dd>{presentation.cover}</dd></div>
        <div><dt className="text-muted-foreground">Desired</dt><dd>{cover.data ? `${cover.data.desired_active ? "ON" : "OFF"} r${cover.data.desired_revision}` : locale === "ja" ? "未初期化" : "Not initialized"}</dd></div>
        <div><dt className="text-muted-foreground">Applied</dt><dd>{cover.data?.applied_revision == null ? locale === "ja" ? "未確認" : "Unconfirmed" : `${cover.data.applied_active ? "ON" : "OFF"} r${cover.data.applied_revision}`}</dd></div>
        <div><dt className="text-muted-foreground">Generation</dt><dd>{cover.data ? `g${cover.data.job_generation} / ${cover.data.status}` : "—"}</dd></div><div><dt className="text-muted-foreground">{locale === "ja" ? "最終確認" : "Last confirmed"}</dt><dd>{cover.data?.applied_revision == null ? "—" : `r${cover.data.applied_revision}`}</dd></div>
      </dl>
      {mismatch || reconcileRequired ? <p role="status" className="rounded-md border border-amber-300 bg-amber-50 px-3 py-2 text-sm text-amber-900 dark:bg-amber-950/30 dark:text-amber-200">{locale === "ja" ? "DesiredとAppliedは未一致です。Desiredを適用済みとして表示せず、再送せずにEncoderの確定状態を確認します。" : "Desired and applied differ. Desired is not shown as applied; refresh the confirmed Encoder state without resending."}</p> : null}
      {notice ? <p role="status" aria-live="polite" className="text-sm text-muted-foreground">{notice}</p> : null}
      <div className="flex flex-wrap gap-2">
        <Button type="button" size="sm" aria-label={locale === "ja" ? "Video Coverを表示" : "Show Video Cover"} aria-describedby={showReason ? showReasonID : undefined} onClick={() => issue(true)} disabled={!cover.data || !show.allowed}>{action.isPending ? <LoaderCircle className="size-4 animate-spin" /> : <Eye className="size-4" />}{locale === "ja" ? "Coverを表示" : "Show cover"}</Button>
        <ConfirmationDialogFrame trigger={<Button type="button" variant="outline" size="sm" aria-label={locale === "ja" ? "Video Coverを非表示" : "Hide Video Cover"} aria-describedby={hideReason ? hideReasonID : undefined} disabled={!cover.data || !hide.allowed}><EyeOff className="size-4" />{locale === "ja" ? "Coverを非表示" : "Hide cover"}</Button>} title={locale === "ja" ? "Video Coverを非表示" : "Hide Video Cover"} description={locale === "ja" ? "背後の映像が公開される可能性があります。Watermark設定やrevisionは変更されません。" : "The underlying video may become public. Watermark configuration and revision are not changed."} cancelLabel={locale === "ja" ? "キャンセル" : "Cancel"} actionLabel={locale === "ja" ? "Coverを非表示" : "Hide cover"} actionClosesDialog onConfirm={() => issue(false, true)} />
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
  if (state?.fetchStatus === "fetching") return { status: "loading", supported: false };
  if (state?.status !== "success" || !rows) return { status: state?.status === "error" ? "error" : "loading", supported: false };
  return coverCapabilityFromRows(rows, assignedEncoderID);
}
function coverCapabilityFromRows(rows: WorkerNode[], assignedEncoderID?: string): CoverCapabilitySnapshot {
  const encoder = rows.find((row) => row.service_type === "encoder_recorder" && (row.service_id || row.id) === assignedEncoderID);
  return { status: "ready", supported: encoder?.reported_capabilities?.live_video_cover_v1 === true };
}
