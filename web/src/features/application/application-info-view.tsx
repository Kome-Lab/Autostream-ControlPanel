"use client";

import { type ReactNode, useEffect, useMemo, useRef, useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { Activity, Download, GitCommit, History, LoaderCircle, RefreshCcw, ServerCog, ShieldAlert, XCircle } from "lucide-react";
import { StatusBadge } from "@/components/admin/status-badge";
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogMedia,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { useAppSettings, useCurrentUser, useNodes, useServiceHealth, useSystemUpdates, useVersion } from "@/features/queries";
import { apiPost } from "@/lib/api/client";
import { hasPermission } from "@/lib/auth/permissions";
import {
  compareSystemUpdateVersions,
  isControlPanelUpdateTarget,
  isSystemUpdateJobActive,
  isSystemUpdateJobCancellable,
  requestSystemUpdateWithRecovery,
  runSystemUpdatesSequentially,
  systemUpdateDeploymentLabel,
  systemUpdateErrorMessage,
  systemUpdateConnectivity,
  systemUpdateHostReachabilityLabel,
  systemUpdateHostReachabilityMessage,
  systemUpdateJobStatusLabel,
  systemUpdateJobTone,
  systemUpdateJobFromResponse,
  systemUpdateMayDisconnectPanel,
  systemUpdateProgress,
  systemUpdateStrategyForTarget,
  systemUpdateTargetBlockedReason,
} from "@/lib/system-updates";
import { formatDateTimeInTimeZone } from "@/lib/timezone";
import type { AppVersion, ServiceUpdateInfo, SystemUpdateAgentStatus, SystemUpdateHostStatus, SystemUpdateJob, SystemUpdateTarget, SystemUpdatesResponse, WorkerNode } from "@/types/domain";

type Confirmation = { kind: "target"; target: SystemUpdateTarget } | { kind: "batch"; targets: SystemUpdateTarget[] };
type Feedback = { tone: "success" | "error"; message: string };
type SystemUpdateOperation = { target: SystemUpdateTarget; idempotencyKey: string };

export function ApplicationInfoView() {
  const currentUser = useCurrentUser();
  const appSettings = useAppSettings();
  const appVersion = useVersion();
  const queryClient = useQueryClient();
  const canReadRegisteredNodes = hasPermission(currentUser.data, "api_tokens.create");
  const canReadServiceHealth = hasPermission(currentUser.data, "service_health.read");
  const canViewNodeInfo = canReadRegisteredNodes || canReadServiceHealth;
  const canReadSystemUpdates = hasPermission(currentUser.data, "system_updates.read");
  const canExecuteSystemUpdates = hasPermission(currentUser.data, "system_updates.execute");
  const registeredNodes = useNodes(canReadRegisteredNodes);
  const serviceHealth = useServiceHealth(canReadServiceHealth);
  const systemUpdates = useSystemUpdates(canReadSystemUpdates);
  const timezone = appSettings.data?.timezone;
  const [confirmation, setConfirmation] = useState<Confirmation | null>(null);
  const [feedback, setFeedback] = useState<Feedback | null>(null);
  const [batchProgress, setBatchProgress] = useState<{ completed: number; total: number } | null>(null);
  const [selfUpdateJobID, setSelfUpdateJobID] = useState("");
  const scheduledReloadJobID = useRef("");
  const scheduledReloadTimer = useRef<number | undefined>(undefined);
  const nodeRows = useMemo(() => mergeRegisteredNodeRows(registeredNodes.data || [], serviceHealth.data || []).sort(compareServiceRows), [registeredNodes.data, serviceHealth.data]);
  const nodesFetching = (canReadRegisteredNodes && registeredNodes.isFetching) || (canReadServiceHealth && serviceHealth.isFetching);
  const nodesLoading = nodeRows.length === 0 && ((canReadRegisteredNodes && registeredNodes.isLoading) || (canReadServiceHealth && serviceHealth.isLoading));
  const nodesError = (canReadRegisteredNodes && registeredNodes.isError) || (canReadServiceHealth && serviceHealth.isError);
  const targets = useMemo(() => systemUpdates.data?.targets || [], [systemUpdates.data?.targets]);
  const updaters = useMemo(() => systemUpdates.data?.updaters || [], [systemUpdates.data?.updaters]);
  const hosts = useMemo(() => systemUpdates.data?.hosts || [], [systemUpdates.data?.hosts]);
  const jobs = useMemo(() => [...(systemUpdates.data?.jobs || [])].sort(compareUpdateJobs), [systemUpdates.data?.jobs]);
  const jobsByTarget = useMemo(() => latestJobsByTarget(jobs), [jobs]);
  const availableTargets = useMemo(
    () => orderBatchTargets(targets.filter((target) => updateCanStart(target, jobsByTarget.get(target.target_id), updaters, hosts))),
    [targets, jobsByTarget, updaters, hosts],
  );
  const selfUpdateJob = jobs.find((job) => job.id === selfUpdateJobID);
  const reconnecting = Boolean(selfUpdateJobID) && (systemUpdates.isError || !selfUpdateJob || systemUpdateMayDisconnectPanel(selfUpdateJob.status));
  const terminalSelfUpdateFeedback = selfUpdateTerminalFeedback(selfUpdateJob);
  const visibleFeedback = terminalSelfUpdateFeedback || feedback;
  const confirmationTargets = confirmation ? (confirmation.kind === "target" ? [confirmation.target] : confirmation.targets) : [];
  const confirmationIncludesControlPanel = confirmationTargets.some(isControlPanelUpdateTarget);

  useEffect(() => {
    if (!selfUpdateJob || !systemUpdateSucceeded(selfUpdateJob.status) || scheduledReloadJobID.current === selfUpdateJob.id) return;
    scheduledReloadJobID.current = selfUpdateJob.id;
    void queryClient.invalidateQueries({ queryKey: ["version"] });
    scheduledReloadTimer.current = window.setTimeout(() => window.location.reload(), 1_500);
  }, [queryClient, selfUpdateJob]);

  useEffect(() => () => {
    if (scheduledReloadTimer.current !== undefined) window.clearTimeout(scheduledReloadTimer.current);
  }, []);

  const clearTerminalSelfUpdate = () => {
    if (selfUpdateJob && !isSystemUpdateJobActive(selfUpdateJob.status)) setSelfUpdateJobID("");
  };

  const createUpdate = useMutation<SystemUpdateJob, Error, SystemUpdateOperation>({
    mutationFn: async ({ target, idempotencyKey }) => requestSystemUpdateWithRecovery(
      target,
      idempotencyKey,
      async (request) => apiPost<unknown>("/system-updates", request),
      async () => (await systemUpdates.refetch()).data?.jobs || [],
    ),
    retry: 1,
    onSuccess: async (job, { target }) => {
      if (isControlPanelUpdateTarget(target)) setSelfUpdateJobID(job.id);
      mergeSystemUpdateJob(queryClient.getQueryData<SystemUpdatesResponse>(["system-updates"]), job, queryClient);
      await queryClient.invalidateQueries({ queryKey: ["system-updates"] });
    },
  });

  const cancelUpdate = useMutation<SystemUpdateJob, Error, SystemUpdateJob>({
    mutationFn: async (job) => systemUpdateJobFromResponse(await apiPost<unknown>(`/system-updates/${encodeURIComponent(job.id)}/cancel`)),
    onSuccess: async (job) => {
      mergeSystemUpdateJob(queryClient.getQueryData<SystemUpdatesResponse>(["system-updates"]), job, queryClient);
      setFeedback({ tone: "success", message: "更新ジョブをキャンセルしました。" });
      await queryClient.invalidateQueries({ queryKey: ["system-updates"] });
    },
    onError: (error) => setFeedback({ tone: "error", message: systemUpdateErrorMessage(error, "更新ジョブをキャンセルできませんでした。") }),
  });

  const executeTarget = async (target: SystemUpdateTarget) => {
    clearTerminalSelfUpdate();
    setFeedback(null);
    try {
      await createUpdate.mutateAsync({ target, idempotencyKey: newIdempotencyKey(target.target_id) });
      const suffix = systemUpdateStrategyForTarget(target) === "when_idle" ? "配信終了後に更新を開始します。" : "更新ジョブを受け付けました。";
      setFeedback({ tone: "success", message: `${target.name || target.target_id}: ${suffix}` });
    } catch (error) {
      setFeedback({ tone: "error", message: systemUpdateErrorMessage(error) });
    }
  };

  const executeBatch = async (batchTargets: SystemUpdateTarget[]) => {
    clearTerminalSelfUpdate();
    setFeedback(null);
    setBatchProgress({ completed: 0, total: batchTargets.length });
    let completed = 0;
    let currentTarget: SystemUpdateTarget | undefined;
    try {
      await runSystemUpdatesSequentially(batchTargets, async (target, index) => {
        currentTarget = target;
        const job = await createUpdate.mutateAsync({ target, idempotencyKey: newIdempotencyKey(target.target_id) });
        completed = index + 1;
        setBatchProgress({ completed, total: batchTargets.length });
        return job;
      });
      setFeedback({ tone: "success", message: `${batchTargets.length}件の更新ジョブを順番に受け付けました。` });
    } catch (error) {
      const targetName = currentTarget?.name || currentTarget?.target_id || "不明な対象";
      setFeedback({ tone: "error", message: `${completed}/${batchTargets.length}件を受付済みです。${targetName} の受付で停止しました。${systemUpdateErrorMessage(error)}` });
    } finally {
      setBatchProgress(null);
    }
  };

  const requestTarget = (target: SystemUpdateTarget) => {
    if (isControlPanelUpdateTarget(target)) {
      setConfirmation({ kind: "target", target });
      return;
    }
    void executeTarget(target);
  };

  const requestBatch = () => {
    setConfirmation({ kind: "batch", targets: availableTargets });
  };

  const confirmUpdate = () => {
    const pending = confirmation;
    setConfirmation(null);
    if (!pending) return;
    if (pending.kind === "target") void executeTarget(pending.target);
    else void executeBatch(pending.targets);
  };

  const refreshInformation = () => {
    void appVersion.refetch();
    if (canReadSystemUpdates) void systemUpdates.refetch();
  };

  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div>
          <h1 className="text-2xl font-semibold tracking-normal">アプリケーション情報</h1>
          <p className="text-sm text-muted-foreground">Control Panelと登録済みサービスのバージョン確認、更新、進捗確認を行います。</p>
        </div>
        <div className="flex flex-wrap gap-2">
          <Button variant="outline" size="sm" onClick={refreshInformation} disabled={appVersion.isFetching || systemUpdates.isFetching}>
            <RefreshCcw className="size-4" />
            更新情報を再確認
          </Button>
          <Button
            variant="outline"
            size="sm"
            onClick={() => {
              if (canReadRegisteredNodes) void registeredNodes.refetch();
              if (canReadServiceHealth) void serviceHealth.refetch();
            }}
            disabled={!canViewNodeInfo || nodesFetching}
          >
            <RefreshCcw className="size-4" />
            情報を再取得
          </Button>
        </div>
      </div>

      {reconnecting ? (
        <div className="flex items-start gap-3 rounded-lg border border-blue-300 bg-blue-50 p-4 text-sm text-blue-950 dark:border-blue-900 dark:bg-blue-950/35 dark:text-blue-100" role="status">
          <LoaderCircle className="mt-0.5 size-4 shrink-0 animate-spin" />
          <div>
            <div className="font-medium">Control Panelを更新しています。再接続中です。</div>
            <div className="mt-1 text-xs opacity-80">再起動中は一時的にAPIへ接続できません。この画面は自動的に再確認します。</div>
          </div>
        </div>
      ) : null}

      {visibleFeedback ? (
        <div className={visibleFeedback.tone === "error" ? "rounded-md border border-red-300 bg-red-50 p-3 text-sm text-red-950 dark:border-red-900 dark:bg-red-950/35 dark:text-red-100" : "rounded-md border border-emerald-300 bg-emerald-50 p-3 text-sm text-emerald-950 dark:border-emerald-900 dark:bg-emerald-950/35 dark:text-emerald-100"} role={visibleFeedback.tone === "error" ? "alert" : "status"}>
          {visibleFeedback.message}
        </div>
      ) : null}

      <SystemUpdatesCard
        canRead={canReadSystemUpdates}
        canExecute={canExecuteSystemUpdates}
        updaters={updaters}
        hosts={hosts}
        targets={targets}
        jobs={jobs}
        jobsByTarget={jobsByTarget}
        isLoading={systemUpdates.isLoading}
        isError={systemUpdates.isError}
        error={systemUpdates.error}
        isCreating={createUpdate.isPending || Boolean(batchProgress)}
        cancellingJobID={cancelUpdate.isPending ? cancelUpdate.variables?.id : undefined}
        batchProgress={batchProgress}
        availableCount={availableTargets.length}
        timezone={timezone}
        onRefresh={() => void systemUpdates.refetch()}
        onRequestTarget={requestTarget}
        onRequestBatch={requestBatch}
        onCancel={(job) => { clearTerminalSelfUpdate(); setFeedback(null); cancelUpdate.mutate(job); }}
      />

      <div className="grid gap-4 2xl:grid-cols-[minmax(320px,0.85fr)_minmax(0,1.15fr)]">
        <Card>
          <CardHeader>
            <CardTitle className="flex items-center gap-2"><Activity className="size-5" />Control Panel</CardTitle>
            <CardDescription>管理画面とAPIサーバーのビルド情報です。</CardDescription>
          </CardHeader>
          <CardContent className="space-y-4">
            <div className="grid gap-3 sm:grid-cols-2">
              <InfoItem label="バージョン" value={appVersion.data?.version || "dev"} />
              <InfoItem label="コミット" value={shortCommit(appVersion.data?.commit)} monospace />
              <InfoItem label="ビルド日時" value={formatOptionalDate(appVersion.data?.build_date, timezone)} />
              <InfoItem label="更新確認" value={<UpdateStatusBadge state={controlPanelUpdateState(appVersion.data)} />} />
            </div>
            {appVersion.data?.update_check_error ? <p className="text-sm text-amber-700">更新確認エラー: {appVersion.data.update_check_error}</p> : null}
          </CardContent>
        </Card>

        <RegisteredServicesCard
          canViewNodeInfo={canViewNodeInfo}
          nodesError={nodesError}
          nodesLoading={nodesLoading}
          nodeRows={nodeRows}
          timezone={timezone}
          appVersion={appVersion.data}
          onRefresh={() => { if (canReadRegisteredNodes) void registeredNodes.refetch(); if (canReadServiceHealth) void serviceHealth.refetch(); }}
        />
      </div>

      <AlertDialog open={Boolean(confirmation)} onOpenChange={(open) => { if (!open) setConfirmation(null); }}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogMedia className="bg-amber-50 text-amber-700 dark:bg-amber-950 dark:text-amber-300"><ShieldAlert /></AlertDialogMedia>
            <AlertDialogTitle>{confirmationIncludesControlPanel ? "Control Panel自身を含む更新を開始しますか？" : `${confirmationTargets.length}件の更新を依頼しますか？`}</AlertDialogTitle>
            <AlertDialogDescription>
              {confirmationIncludesControlPanel
                ? `対象: ${confirmationTargets.map((target) => target.name || target.target_id).join("、")}。Control Panelは受付順の最後に配置し、再起動中は管理画面とAPI接続が一時的に切断されます。`
                : `対象: ${confirmationTargets.map((target) => target.name || target.target_id).join("、")}。更新ジョブの受付後、中央Updaterが各対象ホストの接続状態を確認して適用します。`}
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>キャンセル</AlertDialogCancel>
            <AlertDialogAction onClick={confirmUpdate}>更新を開始</AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </div>
  );
}

function SystemUpdatesCard({
  canRead,
  canExecute,
  updaters,
  hosts,
  targets,
  jobs,
  jobsByTarget,
  isLoading,
  isError,
  error,
  isCreating,
  cancellingJobID,
  batchProgress,
  availableCount,
  timezone,
  onRefresh,
  onRequestTarget,
  onRequestBatch,
  onCancel,
}: {
  canRead: boolean;
  canExecute: boolean;
  updaters: SystemUpdateAgentStatus[];
  hosts: SystemUpdateHostStatus[];
  targets: SystemUpdateTarget[];
  jobs: SystemUpdateJob[];
  jobsByTarget: Map<string, SystemUpdateJob>;
  isLoading: boolean;
  isError: boolean;
  error: unknown;
  isCreating: boolean;
  cancellingJobID?: string;
  batchProgress: { completed: number; total: number } | null;
  availableCount: number;
  timezone?: string;
  onRefresh: () => void;
  onRequestTarget: (target: SystemUpdateTarget) => void;
  onRequestBatch: () => void;
  onCancel: (job: SystemUpdateJob) => void;
}) {
  return (
    <Card>
      <CardHeader className="gap-3 sm:flex-row sm:items-start sm:justify-between">
        <div>
          <CardTitle className="flex items-center gap-2"><Download className="size-5" />システム更新</CardTitle>
          <CardDescription className="mt-1">Control Panelから中央Updaterへ更新を依頼し、登録済みホストへ安全に適用します。</CardDescription>
        </div>
        <div className="flex flex-wrap gap-2">
          <Button variant="outline" size="sm" onClick={onRefresh} disabled={!canRead || isLoading}><RefreshCcw className="size-4" />再取得</Button>
          <Button size="sm" onClick={onRequestBatch} disabled={!canExecute || availableCount === 0 || isCreating} title={!canExecute ? "system_updates.execute 権限が必要です。" : undefined}>
            {isCreating ? <LoaderCircle className="size-4 animate-spin" /> : <Download className="size-4" />}
            {batchProgress ? `${batchProgress.completed}/${batchProgress.total} 受付中` : `更新可能なものを順次受付（ホストごと並行）${availableCount ? ` (${availableCount})` : ""}`}
          </Button>
          {batchProgress ? <span className="sr-only" role="status" aria-live="polite">{batchProgress.completed}/{batchProgress.total}件の更新ジョブを受付済みです。</span> : null}
        </div>
      </CardHeader>
      <CardContent className="space-y-5">
        <div className="rounded-md border border-blue-200 bg-blue-50/70 p-3 text-xs leading-5 text-blue-950 dark:border-blue-900 dark:bg-blue-950/30 dark:text-blue-100">
          Docker配備では、Docker Bundleのバージョンと各サービスのバージョンは別に管理されます。表示が異なっていても異常ではなく、中央Updaterが対象サービスとBundle設定を照合して更新します。
          中央Updaterの稼働状態と、そこから各対象ホストへの接続状態は別々に表示されます。
        </div>

        {canRead && !isError && !isLoading ? <CentralUpdaterStatus updaters={updaters} timezone={timezone} /> : null}

        {!canRead ? (
          <div className="rounded-md border border-dashed p-6 text-sm text-muted-foreground">更新対象と履歴を確認するには「system_updates.read」権限が必要です。</div>
        ) : isError ? (
          <div className="flex flex-wrap items-center justify-between gap-3 rounded-md border border-amber-300 bg-amber-50 p-4 text-sm text-amber-950 dark:border-amber-900 dark:bg-amber-950/35 dark:text-amber-100">
            <span>{systemUpdateErrorMessage(error, "更新対象を取得できませんでした。Control Panelと中央Updaterの接続状態を確認してください。")}</span>
            <Button variant="outline" size="sm" onClick={onRefresh}>再試行</Button>
          </div>
        ) : isLoading ? (
          <div className="rounded-md border border-dashed p-6 text-sm text-muted-foreground">更新対象を読み込み中です。</div>
        ) : targets.length === 0 ? (
          <div className="rounded-md border border-dashed p-6 text-sm text-muted-foreground">更新対象が未設定です。中央Updaterに対象ホストとサービスを登録してください。各ホストへのUpdater導入は不要です。</div>
        ) : (
          <div className="grid gap-3 lg:grid-cols-2 2xl:grid-cols-3">
            {targets.map((target) => (
              <SystemUpdateTargetPanel
                key={target.target_id}
                target={target}
                updaters={updaters}
                hosts={hosts}
                timezone={timezone}
                activeJob={jobsByTarget.get(target.target_id)}
                canExecute={canExecute}
                disabled={isCreating}
                onRequest={() => onRequestTarget(target)}
              />
            ))}
          </div>
        )}

        <div>
          <div className="mb-2 flex items-center gap-2"><History className="size-4" /><h3 className="text-sm font-medium">更新履歴</h3></div>
          {canRead && jobs.length > 0 ? (
            <div className="overflow-x-auto rounded-md border">
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead>対象</TableHead><TableHead>バージョン</TableHead><TableHead>状態</TableHead><TableHead>進捗</TableHead><TableHead>メッセージ</TableHead><TableHead>依頼者 / 日時</TableHead><TableHead className="text-right">操作</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {jobs.map((job) => {
                    const progress = systemUpdateProgress(job);
                    const jobMessage = systemUpdateJobMessage(job);
                    const [jobMessageSummary, ...jobMessageDetails] = jobMessage.split("\n");
                    return (
                      <TableRow key={job.id}>
                        <TableCell><div className="font-medium">{targetDisplayName(job, targets)}</div><div className="text-xs text-muted-foreground">{systemUpdateDeploymentLabel(job.deployment_mode)}</div></TableCell>
                        <TableCell className="whitespace-nowrap text-xs"><span>{job.current_version || "-"}</span><span className="px-1 text-muted-foreground">→</span><span>{job.target_version || "-"}</span></TableCell>
                        <TableCell><Badge variant={systemUpdateJobTone(job.status)}>{systemUpdateJobDisplayStatus(job)}</Badge></TableCell>
                        <TableCell className="min-w-32"><div className="h-2 overflow-hidden rounded-full bg-muted" role="progressbar" aria-label={`${targetDisplayName(job, targets)} の更新進捗`} aria-valuemin={0} aria-valuemax={100} aria-valuenow={progress}><div className="h-full rounded-full bg-primary transition-[width]" style={{ width: `${progress}%` }} /></div><div className="mt-1 text-right text-xs text-muted-foreground">{progress}%</div></TableCell>
                        <TableCell className="max-w-72 text-xs">
                          {jobMessageDetails.length > 0 ? (
                            <details>
                              <summary className="cursor-pointer break-words" title={jobMessage}>{jobMessageSummary}</summary>
                              <div className="mt-1 space-y-1 break-words text-muted-foreground">{jobMessageDetails.map((line, index) => <div key={`${job.id}-message-${index}`}>{line}</div>)}</div>
                            </details>
                          ) : <span className="break-words" title={jobMessage}>{jobMessageSummary}</span>}
                        </TableCell>
                        <TableCell className="whitespace-nowrap text-xs"><div>{job.requested_by || "-"}</div><div className="text-muted-foreground">{formatOptionalDate(job.created_at, timezone)}</div></TableCell>
                        <TableCell className="text-right">
                          {isSystemUpdateJobCancellable(job.status) ? <Button variant="outline" size="sm" aria-label={`${targetDisplayName(job, targets)} の更新ジョブをキャンセル`} onClick={() => onCancel(job)} disabled={!canExecute || cancellingJobID === job.id}>{cancellingJobID === job.id ? <LoaderCircle className="size-4 animate-spin" /> : <XCircle className="size-4" />}キャンセル</Button> : <span className="text-xs text-muted-foreground">-</span>}
                        </TableCell>
                      </TableRow>
                    );
                  })}
                </TableBody>
              </Table>
            </div>
          ) : canRead ? <div className="rounded-md border border-dashed p-4 text-sm text-muted-foreground">更新履歴はまだありません。</div> : null}
        </div>
      </CardContent>
    </Card>
  );
}

function CentralUpdaterStatus({ updaters, timezone }: { updaters: SystemUpdateAgentStatus[]; timezone?: string }) {
  return (
    <div className="rounded-lg border bg-muted/15 p-4">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <div>
          <div className="text-sm font-medium">中央Updater</div>
          <div className="mt-0.5 text-xs text-muted-foreground">Control Panelから更新ジョブを受け取り、各対象ホストへ接続します。</div>
        </div>
        {updaters.length === 0 ? <Badge variant="secondary">未登録</Badge> : null}
      </div>
      {updaters.length === 0 ? (
        <p className="mt-3 text-xs text-amber-700 dark:text-amber-300">中央Updaterが登録されていません。更新ジョブは開始できません。</p>
      ) : (
        <div className="mt-3 grid gap-2 lg:grid-cols-2">
          {updaters.map((updater) => (
            <div key={updater.updater_id} className="flex flex-wrap items-center justify-between gap-3 rounded-md border bg-background/70 p-3 text-xs">
              <div className="min-w-0">
                <div className="truncate font-medium">{updater.name || updater.updater_id}</div>
                <div className="mt-0.5 text-muted-foreground">{updater.updater_id}{updater.version ? ` · ${updater.version}` : ""}</div>
                <div className="mt-0.5 text-muted-foreground">最終Heartbeat: {formatOptionalDate(updater.last_heartbeat_at, timezone)}</div>
              </div>
              <Badge variant={updater.online ? "default" : "destructive"}>{updater.online ? (String(updater.status).toLowerCase() === "updating" ? "オンライン・更新処理中" : "オンライン") : "オフライン"}</Badge>
            </div>
          ))}
        </div>
      )}
    </div>
  );
}

function SystemUpdateTargetPanel({ target, updaters, hosts, timezone, activeJob, canExecute, disabled, onRequest }: { target: SystemUpdateTarget; updaters: SystemUpdateAgentStatus[]; hosts: SystemUpdateHostStatus[]; timezone?: string; activeJob?: SystemUpdateJob; canExecute: boolean; disabled: boolean; onRequest: () => void }) {
  const strategy = systemUpdateStrategyForTarget(target);
  const connectivity = systemUpdateConnectivity(target, updaters, hosts);
  const canStart = updateCanStart(target, activeJob, updaters, hosts);
  const hostName = connectivity.host?.name || target.host_id || "ホスト未設定";
  const reachabilityLabel = systemUpdateHostReachabilityLabel(connectivity.reachability);
  const reachabilityMessage = systemUpdateHostReachabilityMessage(connectivity.host?.reachability_code);
  const blocked = target.blocked_reason
    ? systemUpdateTargetBlockedReason(target.blocked_reason)
    : !connectivity.updater
      ? systemUpdateTargetBlockedReason("updater_not_configured")
      : !connectivity.agentOnline
        ? systemUpdateTargetBlockedReason("updater_offline")
        : connectivity.reachability === "unreachable"
          ? systemUpdateTargetBlockedReason("target_unreachable")
          : connectivity.reachability === "unknown"
            ? systemUpdateTargetBlockedReason("target_reachability_unknown")
            : !target.update_available
              ? "現在は更新不要です。"
              : !target.eligible
                ? "更新条件を満たしていません。"
                : "";
  return (
    <div className="rounded-lg border bg-muted/15 p-4">
      <div className="flex items-start justify-between gap-3">
        <div className="min-w-0"><div className="truncate font-medium">{target.name || target.target_id}</div><div className="mt-0.5 text-xs text-muted-foreground">{serviceTypeLabel(target.target_type)} · {systemUpdateDeploymentLabel(target.deployment_mode)}</div></div>
        <UpdateStatusBadge state={systemUpdateTargetState(target)} />
      </div>
      <div className="mt-3 grid grid-cols-2 gap-2 text-sm">
        <InfoItem label="現在" value={target.current_version || "未報告"} />
        <InfoItem label="更新先" value={target.latest_version || "未確認"} />
      </div>
      <div className="mt-3 space-y-1 text-xs text-muted-foreground">
        <div className="flex items-center justify-between gap-2"><span>対象ホスト</span><span className="truncate font-medium text-foreground" title={hostName}>{hostName}</span></div>
        <div className="flex items-center justify-between gap-2"><span>接続状態</span><span className={connectivity.reachability === "reachable" ? "text-emerald-700 dark:text-emerald-300" : connectivity.reachability === "unreachable" ? "text-red-700 dark:text-red-300" : "text-amber-700 dark:text-amber-300"}>{reachabilityLabel}</span></div>
        <div className="flex items-center justify-between gap-2"><span>最終接続確認</span><span>{formatOptionalDate(connectivity.host?.reachability_checked_at, timezone)}</span></div>
        <div className="flex items-center justify-between gap-2"><span>実行方法</span><span>{strategy === "when_idle" ? `空き次第（配信 ${target.current_stream_id || "実行中"} の終了後）` : "メンテナンス更新"}</span></div>
      </div>
      {activeJob && isSystemUpdateJobActive(activeJob.status) ? <div className="mt-3 rounded-md bg-muted p-2 text-xs">{systemUpdateJobStatusLabel(activeJob.status)} · {systemUpdateProgress(activeJob)}%</div> : null}
      {target.update_check_error ? <p className="mt-3 break-words text-xs text-amber-700 dark:text-amber-300">確認エラー: {target.update_check_error}</p> : null}
      {reachabilityMessage ? <p className="mt-3 text-xs text-red-700 dark:text-red-300">接続エラー: {reachabilityMessage}</p> : null}
      {blocked ? <p className={target.update_check_error ? "mt-1 text-xs text-amber-700 dark:text-amber-300" : "mt-3 text-xs text-amber-700 dark:text-amber-300"}>{blocked}</p> : null}
      {!canExecute ? <p className="mt-1 text-xs text-muted-foreground">更新の実行には system_updates.execute 権限が必要です。</p> : null}
      <Button className="mt-3 w-full" size="sm" aria-label={`${target.name || target.target_id} を${strategy === "when_idle" ? "空き次第更新" : "更新"}`} onClick={onRequest} disabled={!canExecute || !canStart || disabled} title={!canExecute ? "system_updates.execute 権限が必要です。" : blocked || undefined}>
        <Download className="size-4" />{strategy === "when_idle" ? "空き次第更新" : "更新"}
      </Button>
    </div>
  );
}

function RegisteredServicesCard({ canViewNodeInfo, nodesError, nodesLoading, nodeRows, timezone, appVersion, onRefresh }: { canViewNodeInfo: boolean; nodesError: boolean; nodesLoading: boolean; nodeRows: WorkerNode[]; timezone?: string; appVersion?: AppVersion; onRefresh: () => void }) {
  return (
    <Card>
      <CardHeader><CardTitle className="flex items-center gap-2"><ServerCog className="size-5" />登録済みサービス</CardTitle><CardDescription>Worker、Encoder/Recorder、Discord Bot、Observabilityの報告バージョンです。</CardDescription></CardHeader>
      <CardContent>
        {!canViewNodeInfo ? <div className="rounded-md border border-dashed p-6 text-sm text-muted-foreground">登録済みNodeの情報を確認する権限がありません。管理者にNode情報の閲覧権限を依頼してください。</div>
          : nodesError ? <div className="flex flex-wrap items-center justify-between gap-3 rounded-md border border-amber-300 bg-amber-50 p-4 text-sm text-amber-950 dark:border-amber-900 dark:bg-amber-950/35 dark:text-amber-100"><span>登録済みNodeの情報を取得できませんでした。通信状態とControl Panelのログを確認してください。</span><Button variant="outline" size="sm" onClick={onRefresh}>再試行</Button></div>
            : nodesLoading ? <div className="rounded-md border border-dashed p-6 text-sm text-muted-foreground">読み込み中</div>
              : nodeRows.length === 0 ? <div className="rounded-md border border-dashed p-6 text-sm text-muted-foreground">登録済みNodeがありません。Node登録ページで作成したNodeがある場合は、ページを更新してください。</div>
                : <><div className="grid gap-3 md:hidden">{nodeRows.map((node) => <ServiceInfoPanel key={node.service_id || node.id} node={node} timezone={timezone} updateInfo={serviceUpdateForNode(node, appVersion)} />)}</div><div className="hidden overflow-x-auto rounded-md border md:block"><Table><TableHeader><TableRow><TableHead>サービス</TableHead><TableHead>種別</TableHead><TableHead>バージョン</TableHead><TableHead>コミット</TableHead><TableHead>ビルド日時</TableHead><TableHead>状態</TableHead><TableHead>更新確認</TableHead></TableRow></TableHeader><TableBody>{nodeRows.map((node) => <TableRow key={node.service_id || node.id}><TableCell><div className="font-medium">{node.service_name || node.service_id || "-"}</div></TableCell><TableCell>{serviceTypeLabel(node.service_type)}</TableCell><TableCell>{node.reported_version || node.version || "未報告"}</TableCell><TableCell><span className="inline-flex items-center gap-1 font-mono text-xs"><GitCommit className="size-3.5 text-muted-foreground" />{shortCommit(node.reported_commit)}</span></TableCell><TableCell>{formatOptionalDate(node.reported_build_date, timezone)}</TableCell><TableCell><StatusBadge status={node.health_status || node.status || "-"} /></TableCell><TableCell><UpdateStatusBadge state={nodeUpdateState(node, serviceUpdateForNode(node, appVersion))} /></TableCell></TableRow>)}</TableBody></Table></div><p className="mt-3 text-xs text-muted-foreground">Nodeごとに、対応するサービスの最新リリースと報告バージョンを比較しています。</p></>}
      </CardContent>
    </Card>
  );
}

function mergeRegisteredNodeRows(registeredNodes: WorkerNode[], serviceHealthRows: WorkerNode[]) {
  const merged = new Map<string, WorkerNode>();
  for (const node of registeredNodes) { const key = nodeIdentity(node); if (key) merged.set(key, node); }
  for (const health of serviceHealthRows) { const key = nodeIdentity(health); if (!key) continue; const current = merged.get(key); merged.set(key, current ? mergeNodeRow(current, health) : health); }
  return Array.from(merged.values());
}

function mergeNodeRow(registered: WorkerNode, health: WorkerNode): WorkerNode {
  return { ...registered, ...health, service_id: registered.service_id || health.service_id, id: registered.id || health.id, service_type: registered.service_type || health.service_type, service_name: registered.service_name || health.service_name, description: registered.description || health.description, reported_version: health.reported_version || registered.reported_version, reported_commit: health.reported_commit || registered.reported_commit, reported_build_date: health.reported_build_date || registered.reported_build_date, version: health.version || registered.version, status: health.status || registered.status, health_status: health.health_status || registered.health_status };
}

function nodeIdentity(node: WorkerNode) { return node.service_id || node.id || ""; }

function InfoItem({ label, value, monospace = false }: { label: string; value: ReactNode; monospace?: boolean }) {
  return <div className="rounded-md border bg-muted/20 px-3 py-2"><div className="text-xs text-muted-foreground">{label}</div><div className={monospace ? "font-mono text-sm" : "text-sm"}>{value}</div></div>;
}

function ServiceInfoPanel({ node, timezone, updateInfo }: { node: WorkerNode; timezone?: string; updateInfo?: ServiceUpdateInfo }) {
  return <div className="rounded-md border bg-muted/20 p-3 text-sm"><div className="flex items-start justify-between gap-3"><div className="min-w-0"><div className="truncate font-medium">{node.service_name || node.service_id || "-"}</div><div className="text-xs text-muted-foreground">{serviceTypeLabel(node.service_type)}</div></div><StatusBadge status={node.health_status || node.status || "-"} /></div><div className="mt-3 grid gap-2"><ServiceInfoLine label="バージョン" value={node.reported_version || node.version || "未報告"} /><ServiceInfoLine label="コミット" value={shortCommit(node.reported_commit)} monospace /><ServiceInfoLine label="ビルド日時" value={formatOptionalDate(node.reported_build_date, timezone)} /><ServiceInfoLine label="更新確認" value={<UpdateStatusBadge state={nodeUpdateState(node, updateInfo)} />} /></div></div>;
}

function ServiceInfoLine({ label, value, monospace = false }: { label: string; value: ReactNode; monospace?: boolean }) {
  return <div className="grid grid-cols-[88px_minmax(0,1fr)] gap-2"><span className="text-muted-foreground">{label}</span><span className={monospace ? "truncate font-mono text-xs" : "truncate"}>{value}</span></div>;
}

function compareServiceRows(a: WorkerNode, b: WorkerNode) { const type = serviceTypeLabel(a.service_type).localeCompare(serviceTypeLabel(b.service_type), "ja"); return type !== 0 ? type : (a.service_name || a.service_id || "").localeCompare(b.service_name || b.service_id || "", "ja"); }
function compareUpdateJobs(a: SystemUpdateJob, b: SystemUpdateJob) { return Date.parse(b.created_at || b.updated_at || "") - Date.parse(a.created_at || a.updated_at || ""); }
function latestJobsByTarget(jobs: SystemUpdateJob[]) { const result = new Map<string, SystemUpdateJob>(); for (const job of jobs) if (!result.has(job.target_id)) result.set(job.target_id, job); return result; }
function updateCanStart(target: SystemUpdateTarget, latestJob: SystemUpdateJob | undefined, updaters: SystemUpdateAgentStatus[], hosts: SystemUpdateHostStatus[]) { const eligibleForStrategy = target.eligible || (systemUpdateStrategyForTarget(target) === "when_idle" && target.blocked_reason === "stream_active"); return target.update_available && eligibleForStrategy && systemUpdateConnectivity(target, updaters, hosts).ready && !(latestJob && isSystemUpdateJobActive(latestJob.status)); }
function orderBatchTargets(targets: SystemUpdateTarget[]) { return [...targets].sort((a, b) => Number(isControlPanelUpdateTarget(a)) - Number(isControlPanelUpdateTarget(b))); }
function targetDisplayName(job: SystemUpdateJob, targets: SystemUpdateTarget[]) { return targets.find((target) => target.target_id === job.target_id)?.name || job.target_id; }
function systemUpdateJobMessage(job: SystemUpdateJob) {
  const fallback = systemUpdateJobStatusLabel(job.status);
  const summary = job.code ? systemUpdateErrorMessage({ code: job.code }, fallback) : fallback;
  const detail = String(job.message || "").replace(/[\u0000-\u001f\u007f]+/g, " ").replace(/\s+/g, " ").trim().slice(0, 500);
  const lines = [summary];
  if (job.code) lines.push(`code: ${job.code}`);
  if (detail && detail !== summary && detail !== fallback && detail !== job.code) lines.push(detail);
  return lines.join("\n");
}
function systemUpdateJobDisplayStatus(job: SystemUpdateJob) { return job.status === "queued" && job.strategy === "when_idle" ? "配信終了待ち" : systemUpdateJobStatusLabel(job.status); }
function systemUpdateSucceeded(status?: string) { return ["succeeded", "success", "completed"].includes(String(status || "").toLowerCase()); }
function selfUpdateTerminalFeedback(job?: SystemUpdateJob): Feedback | null { if (!job || isSystemUpdateJobActive(job.status)) return null; if (systemUpdateSucceeded(job.status)) return { tone: "success", message: "Control Panelの更新が完了しました。新しい管理画面へ再読み込みします。" }; if (["failed", "rolled_back", "cancelled", "canceled"].includes(String(job.status || "").toLowerCase())) return { tone: "error", message: `Control Panelの更新は完了しませんでした。${systemUpdateJobMessage(job)}` }; return null; }
function newIdempotencyKey(targetID: string) { const random = typeof crypto !== "undefined" && "randomUUID" in crypto ? crypto.randomUUID() : Math.random().toString(36).slice(2); const safeTargetID = targetID.replace(/[^a-zA-Z0-9_-]/g, "-").slice(0, 48); return `web-${safeTargetID}-${random}`; }

function mergeSystemUpdateJob(current: SystemUpdatesResponse | undefined, job: SystemUpdateJob, queryClient: ReturnType<typeof useQueryClient>) {
  if (!current) return;
  queryClient.setQueryData<SystemUpdatesResponse>(["system-updates"], { ...current, jobs: [job, ...current.jobs.filter((item) => item.id !== job.id)] });
}

function shortCommit(value?: string) { const commit = value?.trim() || ""; if (!commit || commit === "unknown") return "-"; return commit.length > 12 ? commit.slice(0, 12) : commit; }
function formatOptionalDate(value?: string, timezone?: string) { const raw = value?.trim() || ""; if (!raw || raw === "unknown") return "-"; if (Number.isNaN(Date.parse(raw))) return raw; return formatDateTimeInTimeZone(raw, timezone, { year: "numeric", month: "2-digit", day: "2-digit", hour: "2-digit", minute: "2-digit" }); }

type UpdateState = { label: string; tone: "default" | "warning" | "muted" | "ok"; title?: string };
function systemUpdateTargetState(target: SystemUpdateTarget): UpdateState { if (target.update_check_error) return { label: "確認失敗", tone: "warning", title: target.update_check_error }; if (target.update_check_source === "disabled") return { label: "更新確認なし", tone: "muted" }; if (!target.latest_version) return { label: "未確認", tone: "muted" }; return target.update_available ? { label: `更新あり ${target.latest_version}`, tone: "warning" } : { label: "更新なし", tone: "ok" }; }
function controlPanelUpdateState(version?: AppVersion): UpdateState { if (!version) return { label: "確認中", tone: "muted" }; if (version.update_check_error) return { label: "確認失敗", tone: "warning", title: version.update_check_error }; if (version.update_available && version.latest_version) return { label: `更新あり ${version.latest_version}`, tone: "warning" }; if (version.update_check_source === "disabled") return { label: "更新確認なし", tone: "muted" }; return { label: "更新なし", tone: "ok" }; }
function serviceUpdateForNode(node: WorkerNode, version?: AppVersion) { return version?.service_updates?.[node.service_type]; }
function nodeUpdateState(node: WorkerNode, version?: ServiceUpdateInfo): UpdateState { if (!(node.reported_version || node.version)) return { label: "未報告", tone: "muted" }; if (version?.update_check_error) return { label: "確認失敗", tone: "warning", title: version.update_check_error }; const current = (node.reported_version || node.version || "").trim(); const latest = version?.latest_version?.trim() || ""; if (!latest) return version?.update_check_source === "disabled" ? { label: "更新確認なし", tone: "muted" } : { label: "確認ソース未設定", tone: "muted" }; const comparison = compareSystemUpdateVersions(current, latest); if (comparison === null) return { label: "比較不能", tone: "muted", title: `報告バージョン ${current} をSemVerとして比較できません。` }; if (comparison < 0) return { label: `更新候補 ${latest}`, tone: "warning" }; if (comparison > 0) return { label: "報告バージョンが新しい", tone: "muted" }; return { label: "更新なし", tone: "ok" }; }
function UpdateStatusBadge({ state }: { state: UpdateState }) { const variant = state.tone === "warning" ? "destructive" : state.tone === "muted" ? "secondary" : "default"; return <Badge variant={variant} title={state.title} aria-label={state.title ? `${state.label}: ${state.title}` : state.label} tabIndex={state.title ? 0 : undefined}>{state.label}</Badge>; }
function serviceTypeLabel(type: string) { const labels: Record<string, string> = { control_panel: "Control Panel", discord_bot: "Discord Bot", encoder_recorder: "Encoder/Recorder", observability: "Observability", update_agent: "AutoStream Updater", worker: "Worker" }; return labels[type] || type || "-"; }
