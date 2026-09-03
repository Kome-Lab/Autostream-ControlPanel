"use client";

import { useEffect, useMemo, useRef, useState, useSyncExternalStore, type ReactNode } from "react";
import { Download, ExternalLink, Link2, Pencil, PlayCircle, RefreshCw, Share2, Trash2 } from "lucide-react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useI18n } from "@/components/admin/i18n-provider";
import { ResourcePanel } from "@/features/resources/resource-page";
import { resourcePages } from "@/features/resources/resource-config";
import { useAppSettings, useArchiveProcessingStreams, useArchiveStreams, useCurrentUser, useResourceData } from "@/features/queries";
import { APIError, apiDelete, apiGet, apiPost, apiPut } from "@/lib/api/client";
import { hasPermission } from "@/lib/auth/permissions";
import { formatDateTimeInTimeZone } from "@/lib/timezone";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Checkbox } from "@/components/ui/checkbox";
import { Input } from "@/components/ui/input";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Skeleton } from "@/components/ui/skeleton";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { HighRiskConfirmation, type ConfirmationDialogState } from "@/components/foundation/confirmation/high-risk-confirmation";
import { ActionAvailabilityBoundary } from "@/components/foundation/permissions/action-availability-boundary";
import { OneTimeSecretReveal } from "@/components/foundation/secrets/one-time-secret-reveal";
import { RoleGuard, guardedButtonProps } from "@/components/admin/role-guard";
import type { Stream } from "@/types/domain";
import { archiveArtifactPollInterval } from "@/features/archive/archive-artifact-polling";
import { archiveRunStartedAt, effectiveArchiveStreamID, isArchiveRecordingArtifact, sortArchiveArtifactsNewest, visibleArchiveProcessingStreams } from "@/features/archive/archive-artifact";
import {
  buildArchiveActionDescriptor,
  createArchiveActionController,
  type ArchiveActionIntent,
  type ArchiveActionResult,
  type ArchiveOpenResult,
} from "@/features/archive/archive-action-policy";
import { adoptArchiveShareCapability, type ArchiveShareCapability } from "@/features/archive/archive-share-capability";
import { createOneTimeSecretLifecycleOwner } from "@/lib/foundation/secrets/lifecycle-owner";
import { aggregateRemainingQueries, remainingQuerySnapshot } from "@/features/remote-state/remaining-remote-state";
import { RemainingStateNotice } from "@/features/remote-state/remaining-state-notice";

type StreamArtifact = {
  id: string;
  stream_id: string;
  archive_run_id?: string;
  archive_started_at?: string | null;
  kind: string;
  name: string;
  relative_path: string;
  size_bytes: number;
  created_at: string;
};

type StreamArtifactShare = {
  id: string;
  stream_id: string;
  artifact_id: string;
  allow_download: boolean;
  expires_at: string;
  created_at: string;
  revoked_at?: string | null;
  status?: string;
  token?: string;
  url?: string;
  api_url?: string;
};

export function ArchiveView() {
  const { t } = useI18n();
  const currentUser = useCurrentUser();
  const page = resourcePages.archive;
  const can = (permission: string) => hasPermission(currentUser.data, permission);
  const canRead = can("archives.read");
  const streams = useArchiveStreams(canRead);
  const processingStreams = useArchiveProcessingStreams(canRead);
  const [selectedStreamID, setSelectedStreamID] = useState("");
  const streamRows = useMemo(() => streams.data || [], [streams.data]);
  const processingRows = useMemo(
    () => visibleArchiveProcessingStreams(processingStreams.data || [], streamRows),
    [processingStreams.data, streamRows],
  );
  const remoteState = aggregateRemainingQueries("archive", {
    streams: remainingQuerySnapshot(streams),
    "processing-streams": remainingQuerySnapshot(processingStreams),
  });
  const selected = effectiveArchiveStreamID(streamRows, selectedStreamID);
  const refreshArchiveState = () => {
    void Promise.all([streams.refetch(), processingStreams.refetch()]);
  };

  return (
    <div className="space-y-6">
      <section>
        <h1 className="text-2xl font-semibold tracking-normal">{t(page.titleKey)}</h1>
        <p className="mt-2 max-w-3xl text-sm text-muted-foreground">録画設定、Drive保存先、Encoderに残る録画成果物をそれぞれ管理します。</p>
      </section>
      <Tabs defaultValue={page.resources[0]?.path || "local-archives"} className="space-y-4">
        <TabsList className="max-w-full flex-wrap justify-start">
          {page.resources.map((resource) => <TabsTrigger key={resource.path} value={resource.path}>{resource.title}</TabsTrigger>)}
          <TabsTrigger value="local-archives">ローカル録画アーカイブ</TabsTrigger>
        </TabsList>
        {page.resources.map((resource) => (
          <TabsContent key={resource.path} value={resource.path}>
            <ResourcePanel resource={resource} currentUser={currentUser.data} />
          </TabsContent>
        ))}
        <TabsContent value="local-archives">
          <Card>
            <CardHeader className="border-b">
              <CardTitle>ローカル録画アーカイブ</CardTitle>
              <CardDescription>Encoderに一定期間残る録画成果物を、配信枠・配信回ごとに管理します。</CardDescription>
            </CardHeader>
            <CardContent className="space-y-4">
              {!canRead ? (
                <div className="rounded-md border border-dashed p-4 text-sm text-muted-foreground">録画成果物を確認する権限がありません。管理者に「録画の閲覧」権限を依頼してください。</div>
              ) : (
                <div className="space-y-4">
                  {remoteState.kind !== "ready" || remoteState.freshness.kind !== "fresh" ? (
                    <RemainingStateNotice state={remoteState} consumer="archive" />
                  ) : null}
                  <ArchiveProcessingNotice
                    items={processingRows}
                    isLoading={processingStreams.isLoading}
                    isError={processingStreams.isError}
                    isFetching={processingStreams.isFetching || streams.isFetching}
                    onRefresh={refreshArchiveState}
                  />
                  {streams.isError && streamRows.length === 0 ? (
                    <div className="flex flex-wrap items-center justify-between gap-3 rounded-md border border-amber-300 bg-amber-50 p-4 text-sm text-amber-900 dark:border-amber-900 dark:bg-amber-950/35 dark:text-amber-100"><span>配信枠を取得できませんでした。通信状態を確認して再試行してください。</span><Button variant="outline" size="sm" onClick={refreshArchiveState}>再試行</Button></div>
                  ) : streams.isLoading && streamRows.length === 0 ? (
                    <Skeleton className="h-12 w-full" />
                  ) : streamRows.length === 0 ? (
                    <div className="rounded-md border border-dashed p-4 text-sm text-muted-foreground">
                      {processingRows.length > 0 ? "完成済みのローカル録画アーカイブはまだありません。処理完了後に自動で表示されます。" : "ローカル録画アーカイブはまだありません。"}
                    </div>
                  ) : (
                    <>
                      <StreamSelect streams={streamRows} value={selected} onChange={setSelectedStreamID} />
                      <ArchiveArtifacts
                        key={selected}
                        streamID={selected}
                        canDownload={can("archives.download")}
                        canModify={can("archives.delete")}
                      />
                    </>
                  )}
                </div>
              )}
            </CardContent>
          </Card>
        </TabsContent>
      </Tabs>
    </div>
  );
}

function ArchiveProcessingNotice({
  items,
  isLoading,
  isError,
  isFetching,
  onRefresh,
}: {
  items: Stream[];
  isLoading: boolean;
  isError: boolean;
  isFetching: boolean;
  onRefresh: () => void;
}) {
  if (isLoading && items.length === 0) return <Skeleton className="h-20 w-full" />;
  if (isError && items.length === 0) {
    return (
      <div className="flex flex-wrap items-center justify-between gap-3 rounded-md border border-amber-300 bg-amber-50 p-4 text-sm text-amber-900 dark:border-amber-900 dark:bg-amber-950/35 dark:text-amber-100">
        <span>アーカイブの処理状況を取得できませんでした。完成済み成果物の一覧は引き続き利用できます。</span>
        <Button variant="outline" size="sm" onClick={onRefresh} disabled={isFetching}>再試行</Button>
      </div>
    );
  }
  if (items.length === 0) return null;

  return (
    <section className="rounded-md border border-sky-300 bg-sky-50 p-4 text-sky-950 dark:border-sky-800 dark:bg-sky-950/35 dark:text-sky-100" aria-live="polite">
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div className="flex min-w-0 items-start gap-3">
          <RefreshCw className="mt-0.5 size-4 shrink-0 animate-spin" aria-hidden="true" />
          <div>
            <div className="text-sm font-medium">アーカイブ処理中 {items.length}件</div>
            <p className="mt-1 text-xs text-sky-800 dark:text-sky-200">Encoderで録画成果物を生成・報告しています。完了すると下の選択肢に自動で追加されます。</p>
          </div>
        </div>
        <Button variant="outline" size="sm" onClick={onRefresh} disabled={isFetching}>
          <RefreshCw className={`size-4 ${isFetching ? "animate-spin" : ""}`} />
          更新
        </Button>
      </div>
      <div className="mt-3 grid gap-2 sm:grid-cols-2">
        {items.map((stream) => (
          <div key={`${stream.id}:${stream.archive_run_id || stream.archive_started_at || "legacy"}`} className="flex items-center justify-between gap-3 rounded-md border border-sky-200 bg-background/70 px-3 py-2 text-sm dark:border-sky-900">
            <div className="min-w-0">
              <div className="truncate font-medium" title={stream.name || stream.id}>{stream.name || stream.id}</div>
              {stream.archive_started_at ? <div className="mt-0.5 text-xs text-muted-foreground">配信開始 {formatDateTime(stream.archive_started_at)}</div> : null}
            </div>
            <span className="shrink-0 text-xs text-muted-foreground">{archiveProcessingStateLabel(stream.status)}</span>
          </div>
        ))}
      </div>
    </section>
  );
}

function StreamSelect({ streams, value, onChange }: { streams: Stream[]; value: string; onChange: (value: string) => void }) {
  return (
    <div className="max-w-xl">
      <Select value={value} onValueChange={onChange}>
        <SelectTrigger>
          <SelectValue />
        </SelectTrigger>
        <SelectContent>
          {streams.map((stream) => (
            <SelectItem key={stream.id} value={stream.id}>
              {stream.name || stream.id}{stream.deleted_at ? "（枠削除済み）" : ""}
            </SelectItem>
          ))}
        </SelectContent>
      </Select>
    </div>
  );
}

function ArchiveArtifacts({
  streamID,
  canDownload,
  canModify,
}: {
  streamID: string;
  canDownload: boolean;
  canModify: boolean;
}) {
  const emptyPollAttempts = useRef(0);
  const artifactsPath = `/streams/${encodeURIComponent(streamID)}/artifacts`;

  const query = useQuery({
    queryKey: ["resource", artifactsPath],
    queryFn: async () => {
      const nextArtifacts = (await apiGet<StreamArtifact[]>(artifactsPath)).filter(isArchiveRecordingArtifact);
      emptyPollAttempts.current = nextArtifacts.length === 0 ? emptyPollAttempts.current + 1 : 0;
      return nextArtifacts;
    },
    refetchInterval: (currentQuery) => archiveArtifactPollInterval({
      artifactCount: currentQuery.state.status === "success" && Array.isArray(currentQuery.state.data)
        ? currentQuery.state.data.length
        : undefined,
      emptyPollAttempts: emptyPollAttempts.current,
    }),
  });
  const appSettings = useAppSettings();
  const timezone = appSettings.data?.timezone;
  const artifacts = useMemo(() => sortArchiveArtifactsNewest(query.data || []), [query.data]);
  const refreshArtifacts = () => {
    emptyPollAttempts.current = 0;
    void query.refetch();
  };

  if (query.isLoading && artifacts.length === 0) return <Skeleton className="h-36 w-full" />;
  if (query.isError && artifacts.length === 0) return <div className="flex flex-wrap items-center justify-between gap-3 rounded-md border border-amber-300 bg-amber-50 p-4 text-sm text-amber-900 dark:border-amber-900 dark:bg-amber-950/35 dark:text-amber-100"><span>{archiveListErrorMessage(query.error)}</span><Button variant="outline" size="sm" onClick={refreshArtifacts}>再試行</Button></div>;
  if (artifacts.length === 0) {
    return (
      <div className="space-y-3 rounded-md border border-dashed p-4 text-sm text-muted-foreground">
        <div>
          <div>この配信枠のローカル録画アーカイブはまだ報告されていません。</div>
          <div className="mt-1 text-xs">完了直後は成果物の生成と報告に時間がかかるため、最初の約1分間は5秒ごと、その後も30秒ごとに自動更新します。</div>
        </div>
        <div className="flex flex-wrap items-center gap-2">
          <Button variant="outline" size="sm" onClick={refreshArtifacts} disabled={query.isFetching}>
            <RefreshCw className={`size-4 ${query.isFetching ? "animate-spin" : ""}`} />
            更新
          </Button>
        </div>
      </div>
    );
  }

  return (
    <div className="space-y-3">
      {query.isError ? (
        <div role="status" aria-live="polite" data-artifact-state="stale" className="forced-color-adjust-auto rounded-md border border-amber-300 bg-amber-50 p-3 text-sm text-amber-950 dark:border-amber-900 dark:bg-amber-950/35 dark:text-amber-100">
          更新に失敗したため、取得済みの録画成果物を表示しています。
        </div>
      ) : null}
      {artifacts.map((artifact) => (
        <ArchiveArtifactRow key={artifact.id} streamID={streamID} artifact={artifact} timezone={timezone} canDownload={canDownload} canModify={canModify} />
      ))}
    </div>
  );
}

function ArchiveArtifactRow({ streamID, artifact, timezone, canDownload, canModify }: { streamID: string; artifact: StreamArtifact; timezone?: string; canDownload: boolean; canModify: boolean }) {
  const { t } = useI18n();
  const queryClient = useQueryClient();
  const [name, setName] = useState(artifact.name);
  const [message, setMessage] = useState("");
  const [shareHours, setShareHours] = useState("24");
  const [shareAllowDownload, setShareAllowDownload] = useState(true);
  const [shareOwner] = useState(() => createOneTimeSecretLifecycleOwner<ArchiveShareCapability>({
    epochNowMs: () => Date.now(),
    monotonicNowMs: () => Math.floor(typeof performance === "undefined" ? Date.now() : performance.now()),
    schedule: (callback, delayMs) => setTimeout(callback, delayMs),
    cancel: (handle) => clearTimeout(handle as ReturnType<typeof setTimeout>),
  }));
  const shareSnapshot = useSyncExternalStore(shareOwner.subscribe, shareOwner.getSnapshot, shareOwner.getSnapshot);
  const artifactPath = `/streams/${encodeURIComponent(streamID)}/artifacts/${encodeURIComponent(artifact.id)}`;
  const sharesPath = `${artifactPath}/shares`;
  const playable = isArchiveRecordingArtifact(artifact);
  const nameReady = name.trim() !== "" && !/[\\/]/.test(name);
  const shareHoursValue = Number.parseInt(shareHours, 10);
  const shareHoursReady = Number.isFinite(shareHoursValue) && shareHoursValue >= 1 && shareHoursValue <= 720;
  const shares = useResourceData<StreamArtifactShare[]>(sharesPath);
  const activeShares = useMemo(() => (shares.data || []).filter((share) => shareStatus(share) === "active"), [shares.data]);
  const invalidateArtifacts = () => queryClient.invalidateQueries({ queryKey: ["resource", `/streams/${encodeURIComponent(streamID)}/artifacts`] });
  const invalidateArchiveStreams = () => queryClient.invalidateQueries({ queryKey: ["archive-streams"] });
  const invalidateShares = () => queryClient.invalidateQueries({ queryKey: ["resource", sharesPath] });
  const artifactRevision = archiveArtifactRevision(artifact);
  const actionController = useMemo(() => createArchiveActionController({
    readAuthority: (intent) => {
      const permission = intent.id === "ARC-03" || intent.id === "ARC-05" ? canDownload : canModify;
      return Object.freeze({
        permission: permission ? "allowed" as const : "denied" as const,
        freshness: "fresh" as const,
        applicability: playable ? "applicable" as const : "not-applicable" as const,
        revision: artifactRevision,
      });
    },
  }), [artifactRevision, canDownload, canModify, playable]);

  useEffect(() => {
    const clearForNavigation = () => { shareOwner.clearForNavigation(); };
    window.addEventListener("pagehide", clearForNavigation);
    return () => {
      window.removeEventListener("pagehide", clearForNavigation);
      shareOwner.dispose();
    };
  }, [shareOwner]);

  const baseIntent = (id: ArchiveActionIntent["id"], shareId?: string): ArchiveActionIntent => ({
    id,
    streamId: streamID,
    artifactId: artifact.id,
    artifactLabel: artifact.name,
    ...(shareId ? { shareId } : {}),
  });

  const handleResult = async (result: ArchiveActionResult, successMessage: string, refresh: () => Promise<unknown> | Promise<unknown>[]) => {
    if (result.kind === "succeeded") {
      setMessage(successMessage);
      const pendingRefresh = refresh();
      await (Array.isArray(pendingRefresh) ? Promise.all(pendingRefresh) : pendingRefresh);
      return;
    }
    if (result.kind === "outcome_unknown") {
      setMessage("操作結果を確認できません。再送せず、最新状態または監査ログを確認してください。");
      return;
    }
    if (result.kind === "failed") {
      setMessage(t(result.error.messageKey));
      return;
    }
    setMessage("最新の権限または状態を確認できないため、操作を送信しませんでした。");
  };

  return (
    <div className="rounded-md border p-4">
      <div className="grid gap-4 xl:grid-cols-[minmax(0,1fr)_auto]">
        <div className="min-w-0">
          <div className="font-medium">{artifact.name}</div>
          <div className="mt-2 flex flex-wrap gap-2 text-xs text-muted-foreground">
            <span className="rounded-md bg-muted px-2 py-1">{artifactKindLabel(artifact.kind)}</span>
            <span className="rounded-md bg-muted px-2 py-1">{formatBytes(artifact.size_bytes)}</span>
            <span className="rounded-md bg-muted px-2 py-1">配信開始 {formatDateTime(archiveRunStartedAt(artifact), timezone)}</span>
            <span className="rounded-md bg-muted px-2 py-1">成果物作成 {formatDateTime(artifact.created_at, timezone)}</span>
            <span className="rounded-md bg-muted px-2 py-1" title={artifact.archive_run_id || "従来形式"}>{artifact.archive_run_id ? "配信回別保存" : "従来形式"}</span>
          </div>
          {message ? <div className="mt-2 text-xs text-muted-foreground">{message}</div> : null}
        </div>
        <div className="flex flex-wrap items-center gap-2 xl:justify-end">
          <Button asChild size="sm" variant="outline">
            <a href={`/admin/archive/player/?stream=${encodeURIComponent(streamID)}&artifact=${encodeURIComponent(artifact.id)}`}>
              <PlayCircle className="size-4" />
              {playable ? "再生" : "表示"}
            </a>
          </Button>
          <RoleGuard allowed={canDownload}>{canDownload ? <Button asChild size="sm" variant="outline"><a href={`${artifactPath}/download`} onClick={(event) => {
            const handoff = actionController.downloadHandoff(baseIntent("ARC-05"));
            if (handoff.kind !== "handoff-ready") {
              event.preventDefault();
              setMessage("最新の権限または状態を確認できないため、ダウンロードを開始しませんでした。");
              return;
            }
            setMessage("ブラウザへのダウンロード引き渡しを開始しました。転送完了はブラウザで確認してください。");
          }}><Download className="size-4" />ダウンロード</a></Button> : <Button size="sm" variant="outline" {...guardedButtonProps(false)}><Download className="size-4" />ダウンロード</Button>}</RoleGuard>
          <div className="grid gap-1"><Input className="h-9 w-full sm:w-44" value={name} onChange={(event) => setName(event.target.value)} aria-label="アーカイブ名" aria-invalid={!nameReady} />{name && !nameReady ? <span className="text-xs text-red-600 dark:text-red-300">ファイル名に / または \\ は使えません。</span> : null}</div>
          <RoleGuard allowed={canModify}>
            <ArchiveActionConfirmation
              controller={actionController}
              intent={baseIntent("ARC-01")}
              label="リネーム"
              icon={<Pencil className="size-4" />}
              disabled={!nameReady || name === artifact.name}
              submit={(opened) => actionController.submit(opened, { confirmed: true }, () => apiPut<StreamArtifact>(artifactPath, { name }))}
              onResult={(result) => handleResult(result, "リネームしました。", invalidateArtifacts)}
            />
          </RoleGuard>
          <RoleGuard allowed={canModify}>
            <ArchiveActionConfirmation
              controller={actionController}
              intent={baseIntent("ARC-02")}
              label="削除"
              icon={<Trash2 className="size-4" />}
              variant="destructive"
              submit={(opened) => actionController.submit(opened, { confirmed: true, typedValue: artifact.name }, () => apiDelete<{ status: string }>(artifactPath))}
              onResult={(result) => handleResult(result, "削除しました。", () => [invalidateArtifacts(), invalidateArchiveStreams()])}
            />
          </RoleGuard>
        </div>
      </div>

      <div className="mt-3 rounded-md border bg-muted/20 p-3">
        <div className="flex flex-wrap items-center gap-2">
          <Input className="h-9 w-24" inputMode="numeric" value={shareHours} onChange={(event) => setShareHours(event.target.value)} aria-label="共有期限時間" aria-invalid={!shareHoursReady} />
          <span className="text-xs text-muted-foreground">時間有効</span>
          <label className="flex items-center gap-2 text-sm">
            <Checkbox checked={shareAllowDownload} onCheckedChange={(checked) => setShareAllowDownload(checked === true)} />
            ダウンロード許可
          </label>
          <ArchiveActionConfirmation
            controller={actionController}
            intent={baseIntent("ARC-03")}
            label="共有リンク作成"
            icon={<Share2 className="size-4" />}
            disabled={!shareHoursReady}
            submit={(opened) => actionController.submit(opened, { confirmed: true }, async () => {
              const share = await apiPost<StreamArtifactShare>(sharesPath, {
                expires_in_hours: normalizedShareHours(shareHours),
                allow_download: shareAllowDownload,
              });
              const adoption = adoptArchiveShareCapability(shareOwner, share, window.location.origin);
              if (!adoption.adopted) throw new APIError("Invalid archive share response.", 502, "invalid_archive_share_response");
              return adoption.publicResult;
            })}
            onResult={(result) => handleResult(result, "共有リンクを作成しました。明示的に表示した場合だけ確認できます。", invalidateShares)}
          />
        </div>
        {!shareHoursReady ? <p className="mt-2 text-xs text-red-600 dark:text-red-300">共有期限は1時間から720時間の範囲で入力してください。</p> : null}
        {shareSnapshot.generation > 0 ? (
          <div className="mt-3">
            <OneTimeSecretReveal
              snapshot={shareSnapshot}
              translate={t}
              renderRevealedContent={() => {
                const capability = shareOwner.readRevealedValue();
                return capability ? (
                  <div className="space-y-2">
                    <Input className="h-9" value={capability.url} readOnly aria-label="今回作成した共有URL" />
                    <Button asChild size="sm" variant="outline"><a href={capability.url} target="_blank" rel="noreferrer"><ExternalLink className="size-4" />開く</a></Button>
                  </div>
                ) : <span />;
              }}
              canCopy
              onRevealIntent={() => { shareOwner.reveal(); }}
              onConcealIntent={() => { shareOwner.conceal(); }}
              onCopyIntent={() => { void shareOwner.copyWith((capability) => navigator.clipboard.writeText(capability.url)); }}
              onAcknowledgeIntent={() => { shareOwner.acknowledge(); }}
              onDismissIntent={() => { shareOwner.dismiss(); }}
              onUnmountIntent={() => { shareOwner.dispose(); }}
            />
          </div>
        ) : null}
        <div className="mt-3 space-y-2">
          {shares.isLoading ? (
            <Skeleton className="h-10 w-full" />
          ) : activeShares.length === 0 ? (
            <div className="text-xs text-muted-foreground">有効な共有リンクはありません。</div>
          ) : (
            activeShares.map((share) => (
              <div key={share.id} className="flex flex-wrap items-center justify-between gap-2 rounded-md border bg-background px-3 py-2 text-xs">
                <div className="flex min-w-0 items-center gap-2">
                  <Link2 className="size-3.5 shrink-0 text-muted-foreground" />
                  <span className="truncate">期限: {formatDateTime(share.expires_at, timezone)}</span>
                  <span className="text-muted-foreground">{share.allow_download ? "DL許可" : "再生のみ"}</span>
                </div>
                <RoleGuard allowed={canModify}>
                  <ArchiveActionConfirmation
                    controller={actionController}
                    intent={baseIntent("ARC-04", share.id)}
                    label="共有を停止"
                    submit={(opened) => actionController.submit(opened, { confirmed: true }, () => apiDelete<{ status: string }>(`${sharesPath}/${encodeURIComponent(share.id)}`))}
                    onResult={(result) => handleResult(result, "共有リンクを停止しました。", invalidateShares)}
                  />
                </RoleGuard>
              </div>
            ))
          )}
        </div>
      </div>
    </div>
  );
}

type ArchiveActionController = ReturnType<typeof createArchiveActionController>;

function ArchiveActionConfirmation({
  controller,
  intent,
  label,
  icon,
  variant = "outline",
  disabled = false,
  submit,
  onResult,
}: Readonly<{
  controller: ArchiveActionController;
  intent: ArchiveActionIntent;
  label: string;
  icon?: ReactNode;
  variant?: "outline" | "destructive";
  disabled?: boolean;
  submit: (opened: Extract<ArchiveOpenResult, { kind: "allowed" }>) => Promise<ArchiveActionResult>;
  onResult: (result: ArchiveActionResult) => void | Promise<void>;
}>) {
  const { t } = useI18n();
  const descriptor = buildArchiveActionDescriptor(intent);
  const initial = controller.open(intent);
  const evaluation = archiveEvaluation(initial);
  const [opened, setOpened] = useState<Extract<ArchiveOpenResult, { kind: "allowed" }>>();
  const [state, setState] = useState<ConfirmationDialogState>({ kind: "ready" });
  if (!descriptor) return null;
  const handleConfirm = async () => {
    if (!opened) return;
    setState({ kind: "submitting" });
    const result = await submit(opened);
    await onResult(result);
    if (result.kind === "succeeded") {
      setOpened(undefined);
      setState({ kind: "ready" });
    } else if (result.kind === "outcome_unknown") {
      setState({ kind: "outcome-unknown", nextAction: result.nextAction });
    } else if (result.kind === "failed") {
      setState(result.error.kind === "conflict"
        ? { kind: "conflict", error: result.error }
        : { kind: "failed", error: result.error });
    } else {
      setState(result.reason === "authority-changed" ? { kind: "stale-blocked" } : { kind: "revalidation-unavailable" });
    }
  };
  return (
    <ActionAvailabilityBoundary evaluation={evaluation} translate={t} reasonPresentation="sr-only">
      {(availabilityProps) => (
        <HighRiskConfirmation
          descriptor={descriptor}
          open={opened !== undefined}
          evaluation={evaluation}
          state={state}
          translate={t}
          trigger={(confirmationProps) => (
            <Button type="button" size="sm" variant={variant} {...availabilityProps} disabled={disabled || availabilityProps.disabled || confirmationProps.disabled}>
              {icon}{label}
            </Button>
          )}
          onOpenIntent={() => {
            const next = controller.open(intent);
            if (next.kind === "allowed") {
              setOpened(next);
              setState({ kind: "ready" });
            } else {
              setState({ kind: "revalidation-unavailable" });
            }
          }}
          onCloseIntent={() => {
            setOpened(undefined);
            setState({ kind: "ready" });
          }}
          onConfirmIntent={() => { void handleConfirm(); }}
        />
      )}
    </ActionAvailabilityBoundary>
  );
}

function archiveEvaluation(result: ArchiveOpenResult) {
  if (result.kind === "allowed") return Object.freeze({ visibility: Object.freeze({ kind: "visible" as const }), availability: Object.freeze({ kind: "allowed" as const }) });
  if (result.reason === "not-applicable" || result.reason === "invalid-intent") {
    return Object.freeze({ visibility: Object.freeze({ kind: "hidden" as const, reason: "not-applicable" as const }), availability: Object.freeze({ kind: "blocked" as const, reasonKey: "actionStateBlocked" as const }) });
  }
  if (result.reason === "permission-denied") {
    return Object.freeze({ visibility: Object.freeze({ kind: "visible" as const }), availability: Object.freeze({ kind: "denied" as const, reasonKey: "actionPermissionDenied" as const }) });
  }
  return Object.freeze({ visibility: Object.freeze({ kind: "visible" as const }), availability: Object.freeze({ kind: "unknown" as const, reasonKey: "actionPermissionUnknown" as const }) });
}

function archiveArtifactRevision(artifact: StreamArtifact) {
  return JSON.stringify([artifact.id, artifact.name, artifact.size_bytes, artifact.created_at, artifact.archive_run_id ?? null]);
}

function archiveListErrorMessage(error: unknown) {
  if (error instanceof APIError) {
    if (error.code === "not_found" || error.status === 404) return "配信枠が見つかりません。配信枠一覧を更新してから再試行してください。";
    if (error.code === "unauthorized" || error.status === 401) return "ログインセッションが切れています。再ログインしてから再試行してください。";
    if (error.code === "permission_denied" || error.status === 403) return "録画成果物を表示する権限がありません。管理者に権限を確認してください。";
    if (error.code === "list_stream_artifacts_failed" || error.status >= 500) return "録画成果物を取得できませんでした。Control PanelまたはMariaDBの状態を確認して再試行してください。";
    return "録画成果物を取得できませんでした。最新状態を確認して再試行してください。";
  }
  return "録画成果物を取得できませんでした。通信状態を確認して再試行してください。";
}

function archiveProcessingStateLabel(status?: string) {
  const normalized = String(status || "").trim().toLowerCase();
  if (normalized === "stopping") return "停止処理中";
  if (normalized === "stopped" || normalized === "completed") return "成果物の報告待ち";
  if (normalized === "failed" || normalized === "error") return "要確認";
  return "状態不明";
}

function artifactKindLabel(kind: string) {
  const labels: Record<string, string> = {
    archive: "録画",
    caption: "字幕",
    transcript: "文字起こし",
    metadata: "メタデータ",
    logs: "ログ",
  };
  return labels[kind] || kind;
}

function formatBytes(value: number) {
  if (!Number.isFinite(value) || value < 0) return "-";
  const units = ["B", "KB", "MB", "GB", "TB"];
  let size = value;
  let unit = 0;
  while (size >= 1024 && unit < units.length - 1) {
    size /= 1024;
    unit += 1;
  }
  return `${size.toFixed(size >= 10 || unit === 0 ? 0 : 1)} ${units[unit]}`;
}

function formatDateTime(value: string, timezone?: string) {
  return formatDateTimeInTimeZone(value, timezone, { month: "2-digit", day: "2-digit", hour: "2-digit", minute: "2-digit" });
}

function normalizedShareHours(value: string) {
  const parsed = Number.parseInt(value, 10);
  if (!Number.isFinite(parsed)) return 24;
  return Math.min(24 * 30, Math.max(1, parsed));
}

function shareStatus(share: StreamArtifactShare) {
  if (share.revoked_at) return "revoked";
  if (share.status) return share.status;
  return Date.parse(share.expires_at) > Date.now() ? "active" : "expired";
}
