"use client";

import { useMemo, useRef, useState } from "react";
import { Copy, Download, ExternalLink, Link2, Pencil, PlayCircle, RefreshCw, Share2, Trash2 } from "lucide-react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
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
import { DangerConfirm } from "@/components/admin/danger-confirm";
import { RoleGuard, guardedButtonProps } from "@/components/admin/role-guard";
import type { Stream } from "@/types/domain";
import { archiveArtifactPollInterval } from "@/features/archive/archive-artifact-polling";
import { effectiveArchiveStreamID, isArchiveRecordingArtifact } from "@/features/archive/archive-artifact";

type StreamArtifact = {
  id: string;
  stream_id: string;
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
  const superAdmin = currentUser.data?.user.roles?.includes("super_admin") === true;
  const can = (permission: string) => superAdmin || hasPermission(currentUser.data, permission);
  const canRead = can("archives.read");
  const streams = useArchiveStreams(canRead);
  const processingStreams = useArchiveProcessingStreams(canRead);
  const [selectedStreamID, setSelectedStreamID] = useState("");
  const streamRows = useMemo(() => streams.data || [], [streams.data]);
  const processingRows = useMemo(() => {
    const completedStreamIDs = new Set(streamRows.map((stream) => stream.id));
    return (processingStreams.data || []).filter((stream) => !completedStreamIDs.has(stream.id));
  }, [processingStreams.data, streamRows]);
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
              <CardDescription>Encoderに一定期間残る録画成果物を、配信枠ごとに管理します。</CardDescription>
            </CardHeader>
            <CardContent className="space-y-4">
              {!canRead ? (
                <div className="rounded-md border border-dashed p-4 text-sm text-muted-foreground">録画成果物を確認する権限がありません。管理者に「録画の閲覧」権限を依頼してください。</div>
              ) : (
                <div className="space-y-4">
                  <ArchiveProcessingNotice
                    items={processingRows}
                    isLoading={processingStreams.isLoading}
                    isError={processingStreams.isError}
                    isFetching={processingStreams.isFetching || streams.isFetching}
                    onRefresh={refreshArchiveState}
                  />
                  {streams.isError ? (
                    <div className="flex flex-wrap items-center justify-between gap-3 rounded-md border border-amber-300 bg-amber-50 p-4 text-sm text-amber-900 dark:border-amber-900 dark:bg-amber-950/35 dark:text-amber-100"><span>配信枠を取得できませんでした。通信状態を確認して再試行してください。</span><Button variant="outline" size="sm" onClick={refreshArchiveState}>再試行</Button></div>
                  ) : streams.isLoading ? (
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
  if (isLoading) return <Skeleton className="h-20 w-full" />;
  if (isError) {
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
          <div key={stream.id} className="flex items-center justify-between gap-3 rounded-md border border-sky-200 bg-background/70 px-3 py-2 text-sm dark:border-sky-900">
            <span className="min-w-0 truncate font-medium" title={stream.name || stream.id}>{stream.name || stream.id}</span>
            <span className="shrink-0 text-xs text-muted-foreground">{String(stream.status).toLowerCase() === "stopping" ? "停止処理中" : "成果物の報告待ち"}</span>
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
  const artifacts = useMemo(() => query.data || [], [query.data]);
  const refreshArtifacts = () => {
    emptyPollAttempts.current = 0;
    void query.refetch();
  };

  if (query.isLoading) return <Skeleton className="h-36 w-full" />;
  if (query.isError) return <div className="flex flex-wrap items-center justify-between gap-3 rounded-md border border-amber-300 bg-amber-50 p-4 text-sm text-amber-900 dark:border-amber-900 dark:bg-amber-950/35 dark:text-amber-100"><span>{archiveListErrorMessage(query.error)}</span><Button variant="outline" size="sm" onClick={refreshArtifacts}>再試行</Button></div>;
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
      {artifacts.map((artifact) => (
        <ArchiveArtifactRow key={artifact.id} streamID={streamID} artifact={artifact} timezone={timezone} canDownload={canDownload} canModify={canModify} />
      ))}
    </div>
  );
}

function ArchiveArtifactRow({ streamID, artifact, timezone, canDownload, canModify }: { streamID: string; artifact: StreamArtifact; timezone?: string; canDownload: boolean; canModify: boolean }) {
  const queryClient = useQueryClient();
  const [name, setName] = useState(artifact.name);
  const [message, setMessage] = useState("");
  const [shareHours, setShareHours] = useState("24");
  const [shareAllowDownload, setShareAllowDownload] = useState(true);
  const [latestShareURL, setLatestShareURL] = useState("");
  const [copied, setCopied] = useState(false);
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

  const rename = useMutation({
    mutationFn: () => apiPut<StreamArtifact>(artifactPath, { name }),
    onSuccess: async () => {
      setMessage("リネームしました。");
      await invalidateArtifacts();
    },
    onError: (error) => setMessage(archiveErrorMessage(error, "リネームできませんでした。")),
  });
  const remove = useMutation({
    mutationFn: () => apiDelete<{ status: string }>(artifactPath),
    onSuccess: async () => {
      setMessage("削除しました。");
      await invalidateArtifacts();
      await invalidateArchiveStreams();
    },
    onError: (error) => setMessage(archiveErrorMessage(error, "削除できませんでした。")),
  });
  const createShare = useMutation({
    mutationFn: () =>
      apiPost<StreamArtifactShare>(sharesPath, {
        expires_in_hours: normalizedShareHours(shareHours),
        allow_download: shareAllowDownload,
    }),
    onSuccess: async (share) => {
      const url = share.url || (share.token ? `${window.location.origin}/archive/share/?token=${encodeURIComponent(share.token)}` : "");
      setLatestShareURL(url);
      setCopied(false);
      setMessage("共有リンクを作成しました。URLはこの画面で一度だけ表示されます。");
      await invalidateShares();
    },
    onError: (error) => setMessage(archiveErrorMessage(error, "共有リンクを作成できませんでした。")),
  });
  const revokeShare = useMutation({
    mutationFn: (shareID: string) => apiDelete<{ status: string }>(`${sharesPath}/${encodeURIComponent(shareID)}`),
    onSuccess: async () => {
      setMessage("共有リンクを停止しました。");
      await invalidateShares();
    },
    onError: (error) => setMessage(archiveErrorMessage(error, "共有リンクを停止できませんでした。")),
  });

  const copyLatestShareURL = async () => {
    if (!latestShareURL || typeof navigator === "undefined") return;
    await navigator.clipboard.writeText(latestShareURL);
    setCopied(true);
  };

  return (
    <div className="rounded-md border p-4">
      <div className="grid gap-4 xl:grid-cols-[minmax(0,1fr)_auto]">
        <div className="min-w-0">
          <div className="font-medium">{artifact.name}</div>
          <div className="mt-2 flex flex-wrap gap-2 text-xs text-muted-foreground">
            <span className="rounded-md bg-muted px-2 py-1">{artifactKindLabel(artifact.kind)}</span>
            <span className="rounded-md bg-muted px-2 py-1">{formatBytes(artifact.size_bytes)}</span>
            <span className="rounded-md bg-muted px-2 py-1">{formatDateTime(artifact.created_at, timezone)}</span>
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
          <RoleGuard allowed={canDownload}>{canDownload ? <Button asChild size="sm" variant="outline"><a href={`${artifactPath}/download`}><Download className="size-4" />ダウンロード</a></Button> : <Button size="sm" variant="outline" {...guardedButtonProps(false)}><Download className="size-4" />ダウンロード</Button>}</RoleGuard>
          <div className="grid gap-1"><Input className="h-9 w-full sm:w-44" value={name} onChange={(event) => setName(event.target.value)} aria-label="アーカイブ名" aria-invalid={!nameReady} />{name && !nameReady ? <span className="text-xs text-red-600 dark:text-red-300">ファイル名に / または \\ は使えません。</span> : null}</div>
          <RoleGuard allowed={canModify}><Button size="sm" variant="outline" onClick={() => rename.mutate()} disabled={!canModify || rename.isPending || !nameReady || name === artifact.name}><Pencil className="size-4" />リネーム</Button></RoleGuard>
          <RoleGuard allowed={canModify}>
            <DangerConfirm title={`${artifact.name} を削除しますか`} description="Encoderに保管されている録画成果物を削除します。削除後はこの管理画面から復元できません。" onConfirm={() => remove.mutate()} actionLabel="録画を削除">
              <Button size="sm" variant="destructive" disabled={!canModify || remove.isPending}><Trash2 className="size-4" />削除</Button>
            </DangerConfirm>
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
          <Button size="sm" onClick={() => createShare.mutate()} disabled={!canDownload || createShare.isPending || !shareHoursReady}>
            <Share2 className="size-4" />
            共有リンク作成
          </Button>
        </div>
        {!shareHoursReady ? <p className="mt-2 text-xs text-red-600 dark:text-red-300">共有期限は1時間から720時間の範囲で入力してください。</p> : null}
        {latestShareURL ? (
          <div className="mt-3 flex flex-wrap items-center gap-2">
            <Input className="h-9 min-w-0 flex-[1_1_22rem]" value={latestShareURL} readOnly aria-label="作成した共有URL" />
            <Button size="sm" variant="outline" onClick={copyLatestShareURL}>
              <Copy className="size-4" />
              {copied ? "コピー済み" : "コピー"}
            </Button>
            <Button asChild size="sm" variant="outline">
              <a href={latestShareURL} target="_blank" rel="noreferrer">
                <ExternalLink className="size-4" />
                開く
              </a>
            </Button>
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
                <RoleGuard allowed={canModify}><DangerConfirm title="この共有リンクを停止しますか" description="停止すると、このリンクを知っている利用者も録画を開けなくなります。" onConfirm={() => revokeShare.mutate(share.id)} actionLabel="共有を停止"><Button size="sm" variant="outline" disabled={!canModify || revokeShare.isPending}>停止</Button></DangerConfirm></RoleGuard>
              </div>
            ))
          )}
        </div>
      </div>
    </div>
  );
}

function archiveErrorMessage(error: Error, fallback: string) {
  if (error instanceof APIError) {
    const messages: Record<string, string> = {
      invalid_stream_artifact: "ファイル名に使えない文字または拡張子があります。",
      stream_artifact_exists: "同じ名前のアーカイブが既にあります。",
      missing_stream_assignments: "Primary Encoder Nodeが配信枠に割り当てられていません。",
      archive_artifact_delete_failed: "Encoder側の削除に失敗しました。",
      archive_artifact_rename_failed: "Encoder側のリネームに失敗しました。",
      archive_share_download_disabled: "この共有リンクではダウンロードが許可されていません。",
      archive_share_expired: "共有リンクの期限が切れています。",
      archive_share_revoked: "共有リンクは停止済みです。",
    };
    return messages[error.code || ""] || fallback;
  }
  return fallback;
}

function archiveListErrorMessage(error: unknown) {
  if (error instanceof APIError) {
    if (error.code === "not_found" || error.status === 404) return "配信枠が見つかりません。配信枠一覧を更新してから再試行してください。";
    if (error.code === "unauthorized" || error.status === 401) return "ログインセッションが切れています。再ログインしてから再試行してください。";
    if (error.code === "permission_denied" || error.status === 403) return "録画成果物を表示する権限がありません。管理者に権限を確認してください。";
    if (error.code === "list_stream_artifacts_failed" || error.status >= 500) return "録画成果物を取得できませんでした。Control PanelまたはMariaDBの状態を確認して再試行してください。";
    return `録画成果物を取得できませんでした。(code: ${error.code || "api_error"})`;
  }
  return "録画成果物を取得できませんでした。通信状態を確認して再試行してください。";
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
