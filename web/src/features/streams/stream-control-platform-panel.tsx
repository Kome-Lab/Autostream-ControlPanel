"use client";

import { useCallback, useMemo, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Eye, EyeOff, LoaderCircle, RefreshCcw } from "lucide-react";
import { DangerConfirm } from "@/components/admin/danger-confirm";
import { Button } from "@/components/ui/button";
import { apiGet, apiPut } from "@/lib/api/client";
import { useCurrentUser } from "@/features/queries";
import {
	coverActionAvailability,
	buildVideoCoverAction,
	createVideoCoverActionController,
  newVideoCoverIdempotencyKey,
  visualPresentation,
  type CoverPermissionSnapshot,
  type StreamVisualSettings,
	type VideoCoverState,
	type VideoCoverActionRequest,
} from "@/features/streams/control-platform";
import type { CurrentUser, Stream } from "@/types/domain";

class CoverActionUnavailable extends Error {}

export function StreamControlPlatformPanel({ stream }: { stream: Stream }) {
  const queryClient = useQueryClient();
  const currentUser = useCurrentUser();
  const [notice, setNotice] = useState("");
	const [lastAction, setLastAction] = useState<VideoCoverActionRequest | null>(null);
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
    return { status: "ready", permissions: data.permissions, superAdmin: data.user.roles?.includes("super_admin") === true };
  }, [queryClient]);
  const controller = useMemo(() => createVideoCoverActionController(
    permissionSnapshot,
    (request) => apiPut<VideoCoverState>(`/streams/${encodeURIComponent(stream.id)}/video-cover-state`, request),
  ), [permissionSnapshot, stream.id]);
  const action = useMutation({
		mutationFn: async (input: VideoCoverActionRequest) => {
			const result = await controller.issueRequest(input);
      if (!result.sent) throw new CoverActionUnavailable(result.reason);
      return result.value;
    },
    onMutate: (input) => {
      setNotice("");
      setLastAction(input);
    },
    onSuccess: async (state) => {
      queryClient.setQueryData(["streams", stream.id, "video-cover-state"], state);
      setNotice(state.status === "confirming" ? "Encoderの適用結果を確認中です。自動再送はしません。" : "Video Coverの状態を更新しました。");
    },
    onError: (error) => {
      setNotice(error instanceof CoverActionUnavailable ? error.message : "操作結果を確認できません。自動再送せず、同じ操作キーで状態確認してください。");
    },
  });

  const presentation = visualPresentation(visual.data, stream.name);
  const permission = currentUser.isError
    ? { status: "error", permissions: [] } as CoverPermissionSnapshot
    : currentUser.isLoading || currentUser.isFetching
      ? { status: "loading", permissions: [] } as CoverPermissionSnapshot
      : { status: "ready", permissions: currentUser.data?.permissions || [], superAdmin: currentUser.data?.user.roles?.includes("super_admin") === true } as CoverPermissionSnapshot;
  const show = coverActionAvailability(true, permission, action.isPending);
  const hide = coverActionAvailability(false, permission, action.isPending);
	const reconcile = lastAction ? coverActionAvailability(lastAction.active, permission, action.isPending) : null;
	const issue = (active: boolean, hideConfirmed = false) => {
		if (!cover.data) return;
		action.mutate(buildVideoCoverAction(active, cover.data, newVideoCoverIdempotencyKey(), hideConfirmed));
	};
  const mismatch = cover.data && (cover.data.applied_revision !== cover.data.desired_revision || cover.data.applied_active !== cover.data.desired_active);

  return (
    <section className="space-y-3 rounded-lg border p-4" aria-label="配信ビジュアルとVideo Cover">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <div>
          <h3 className="text-sm font-semibold">配信ビジュアル</h3>
          <p className="mt-1 text-xs text-muted-foreground">Base / Worker scene → Video Cover → Watermark → Encode → tee</p>
        </div>
        <Button type="button" variant="outline" size="sm" onClick={() => { void visual.refetch(); void cover.refetch(); }} disabled={visual.isFetching || cover.isFetching}>
          <RefreshCcw className={visual.isFetching || cover.isFetching ? "size-4 animate-spin" : "size-4"} />更新
        </Button>
      </div>
      <div className="relative aspect-video overflow-hidden rounded-md border bg-muted/40" data-background-mode={visual.data?.background_mode || "default"}>
        <div className="absolute inset-0 grid place-items-center px-6 text-center">
          <div>
            <div className="text-lg font-semibold">{presentation.title}</div>
            <div className="mt-1 text-xs text-muted-foreground">{presentation.background}</div>
          </div>
        </div>
        {cover.data?.applied_active === true ? <div className="absolute inset-0 grid place-items-center bg-black/65 text-sm font-semibold text-white" data-video-cover="applied">Video Cover 適用中</div> : null}
        <div className="absolute bottom-2 right-2 rounded bg-black/70 px-2 py-1 text-[11px] text-white">Watermark: 独立レイヤー</div>
      </div>
      {visual.isError ? <p role="alert" className="text-sm text-destructive">ビジュアル設定を読み込めませんでした。</p> : null}
      {presentation.warning ? <p role="status" className="text-sm text-amber-700 dark:text-amber-300">{presentation.warning}</p> : null}
      <dl className="grid gap-2 text-sm sm:grid-cols-2">
        <div><dt className="text-muted-foreground">Discord</dt><dd>{presentation.discord}</dd></div>
        <div><dt className="text-muted-foreground">開始時Cover</dt><dd>{presentation.cover}</dd></div>
        <div><dt className="text-muted-foreground">Desired</dt><dd>{cover.data ? `${cover.data.desired_active ? "表示" : "非表示"} r${cover.data.desired_revision}` : "未初期化"}</dd></div>
        <div><dt className="text-muted-foreground">Applied</dt><dd>{cover.data?.applied_revision == null ? "未確認" : `${cover.data.applied_active ? "表示" : "非表示"} r${cover.data.applied_revision}`}</dd></div>
      </dl>
      {mismatch ? <p role="status" className="rounded-md border border-amber-300 bg-amber-50 px-3 py-2 text-sm text-amber-900 dark:bg-amber-950/30 dark:text-amber-200">DesiredとAppliedが一致していません。適用済みとして扱わず、Encoderからの確定結果を待ちます。</p> : null}
      {notice ? <p role="status" aria-live="polite" className="text-sm text-muted-foreground">{notice}</p> : null}
      <div className="flex flex-wrap gap-2">
        <Button type="button" size="sm" aria-label="Video Coverを表示" onClick={() => issue(true)} disabled={!cover.data || !show.allowed} title={show.reason || undefined}>
          {action.isPending ? <LoaderCircle className="size-4 animate-spin" /> : <Eye className="size-4" />}Coverを表示
        </Button>
        <DangerConfirm title="Video Coverを非表示" description="非表示にすると、背後の映像が公開される可能性があります。現在の配信内容を確認してから実行してください。" actionLabel="Coverを非表示" onConfirm={() => issue(false, true)}>
          <Button type="button" variant="outline" size="sm" aria-label="Video Coverを非表示" disabled={!cover.data || !hide.allowed} title={hide.reason || undefined}><EyeOff className="size-4" />Coverを非表示</Button>
        </DangerConfirm>
        {lastAction && (cover.data?.status === "confirming" || notice.includes("確認できません")) ? (
			<Button type="button" variant="outline" size="sm" aria-label="同じ操作キーで状態確認" onClick={() => action.mutate(lastAction)} disabled={!reconcile?.allowed} title={reconcile?.reason || undefined}>
            <RefreshCcw className="size-4" />同じ操作キーで状態確認
          </Button>
        ) : null}
      </div>
      {!show.allowed && show.reason ? <p className="text-xs text-muted-foreground">{show.reason}</p> : null}
    </section>
  );
}
