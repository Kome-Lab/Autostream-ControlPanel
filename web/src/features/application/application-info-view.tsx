"use client";

import { Fragment, type ReactNode, useEffect, useId, useMemo, useRef, useState } from "react";
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
  AlertDialogTrigger,
} from "@/components/ui/alert-dialog";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { useAppSettings, useCurrentUser, useNodes, useServiceHealth, useSystemUpdates, useVersion } from "@/features/queries";
import { UpdaterSettingsPanel } from "@/features/application/updater-settings-panel";
import { apiPost } from "@/lib/api/client";
import { hasPermission } from "@/lib/auth/permissions";
import {
  acquireSystemUpdateTargetRequestLock,
  compareSystemUpdateVersions,
  isSystemUpdateEndpointRevisionConflict,
  isControlPanelUpdateTarget,
  isSystemUpdateJobActive,
  isSystemUpdateJobCancellable,
  requestSystemUpdatePortReconfigureWithRecovery,
  systemUpdateDockerPortReconfigureRequest,
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
  systemUpdatePolicyErrorMessage,
  systemUpdateProgress,
  systemUpdatePortReconfigureEligibility,
  systemUpdatePortReconfigureRequest,
  systemUpdatePortRequestMatchesJob,
  systemUpdatePortReconfigureResultLabel,
  systemUpdateStrategyForTarget,
  systemUpdateSoftwareOperationEligibility,
  systemUpdateTargetBlockedReason,
  systemUpdateUpdaterPolicyState,
  SystemUpdateRequestAmbiguousError,
} from "@/lib/system-updates";
import type { SystemUpdateRequestState } from "@/lib/system-updates";
import { nodeEndpointState } from "@/lib/node-registration";
import { formatDateTimeInTimeZone } from "@/lib/timezone";
import type { AppVersion, ServiceUpdateInfo, SystemUpdateAgentStatus, SystemUpdateHostStatus, SystemUpdateJob, SystemUpdatePortReconfigureCreateRequest, SystemUpdateTarget, SystemUpdatesResponse, WorkerNode } from "@/types/domain";

type Confirmation = { kind: "target"; target: SystemUpdateTarget } | { kind: "batch"; targets: SystemUpdateTarget[] };
type Feedback = { tone: "success" | "error"; message: string };
type SystemUpdateOperation = { target: SystemUpdateTarget; idempotencyKey: string };
type PortReconfigureOperation = { request: SystemUpdatePortReconfigureCreateRequest };
type RegisteredServiceOperation = {
  target?: SystemUpdateTarget;
  updater?: SystemUpdateAgentStatus;
  latestJob?: SystemUpdateJob;
  requestState: SystemUpdateRequestState;
};

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
  const [ambiguousPortRequest, setAmbiguousPortRequest] = useState<SystemUpdatePortReconfigureCreateRequest | null>(null);
  const [portRequestTargetID, setPortRequestTargetID] = useState("");
  const activePortRequestTargets = useRef(new Set<string>());
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
  const recoveredAmbiguousPortJob = ambiguousPortRequest
    ? jobs.find((job) => systemUpdatePortRequestMatchesJob(ambiguousPortRequest, job))
    : undefined;
  const unresolvedAmbiguousPortRequest = recoveredAmbiguousPortJob ? null : ambiguousPortRequest;
  const availableTargets = useMemo(
    () => orderBatchTargets(targets.filter((target) => updateCanStart(target, jobsByTarget.get(target.target_id), updaters, hosts))),
    [targets, jobsByTarget, updaters, hosts],
  );
  const selfUpdateJob = jobs.find((job) => job.id === selfUpdateJobID);
  const reconnecting = Boolean(selfUpdateJobID) && (systemUpdates.isError || !selfUpdateJob || systemUpdateMayDisconnectPanel(selfUpdateJob.status));
  const terminalSelfUpdateFeedback = selfUpdateTerminalFeedback(selfUpdateJob);
  const recoveredPortFeedback: Feedback | null = recoveredAmbiguousPortJob
    ? { tone: "success", message: `${recoveredAmbiguousPortJob.target_id}: 応答を確認できなかったポート変更ジョブを履歴から確認しました。` }
    : null;
  const visibleFeedback = terminalSelfUpdateFeedback || recoveredPortFeedback || feedback;
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

  const createPortReconfigure = useMutation<SystemUpdateJob, Error, PortReconfigureOperation>({
    mutationFn: async ({ request }) => requestSystemUpdatePortReconfigureWithRecovery(
      request,
      async (payload) => apiPost<unknown>("/system-updates", payload),
      async () => (await systemUpdates.refetch()).data?.jobs || [],
    ),
    retry: false,
    onSuccess: async (job) => {
      setAmbiguousPortRequest(null);
      mergeSystemUpdateJob(queryClient.getQueryData<SystemUpdatesResponse>(["system-updates"]), job, queryClient);
      await Promise.all([
        queryClient.invalidateQueries({ queryKey: ["system-updates"] }),
        queryClient.invalidateQueries({ queryKey: ["nodes"] }),
        queryClient.invalidateQueries({ queryKey: ["service-health"] }),
      ]);
    },
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

  const executePortReconfigure = async (request: SystemUpdatePortReconfigureCreateRequest) => {
    const targetID = request.target_id.trim();
    if (
      activePortRequestTargets.current.size > 0
      || !acquireSystemUpdateTargetRequestLock(activePortRequestTargets.current, targetID)
    ) {
      return;
    }
    setPortRequestTargetID(targetID);
    setAmbiguousPortRequest(null);
    setFeedback(null);
    try {
      await createPortReconfigure.mutateAsync({ request });
      setFeedback({ tone: "success", message: `${request.target_id}: ポート変更ジョブを受け付けました。現在適用中のendpointが更新されるまでお待ちください。` });
    } catch (error) {
      if (error instanceof SystemUpdateRequestAmbiguousError) {
        setAmbiguousPortRequest(error.request);
        setFeedback({ tone: "error", message: "ポート変更要求の結果を確認できません。安全のため同じ対象への再送を停止し、更新履歴を自動確認します。" });
        return;
      }
      if (isSystemUpdateEndpointRevisionConflict(error)) {
        const refreshes: Promise<unknown>[] = [systemUpdates.refetch()];
        if (canReadRegisteredNodes) refreshes.push(registeredNodes.refetch());
        if (canReadServiceHealth) refreshes.push(serviceHealth.refetch());
        const refreshed = (await Promise.allSettled(refreshes)).every((result) => result.status === "fulfilled");
        setFeedback({
          tone: "error",
          message: refreshed
            ? "Endpoint revisionが変わったため送信しませんでした。最新のNode状態を再取得しました。内容を確認してからやり直してください。"
            : "Endpoint revisionが変わったため送信しませんでした。Node状態を再取得できなかったため、手動で再取得してからやり直してください。",
        });
        return;
      }
      setFeedback({ tone: "error", message: systemUpdateErrorMessage(error, "ポート変更ジョブを開始できませんでした。") });
    } finally {
      activePortRequestTargets.current.delete(targetID);
      setPortRequestTargetID((current) => current === targetID ? "" : current);
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
    <div className="min-w-0 space-y-4">
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
        canManageUpdaterSecrets={hasPermission(currentUser.data, "secrets.update")}
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

      <div className="grid min-w-0 gap-4 2xl:grid-cols-[minmax(320px,0.85fr)_minmax(0,1.15fr)]">
        <Card className="min-w-0">
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
          targets={targets}
          updaters={updaters}
          jobsByTarget={jobsByTarget}
          canExecuteSystemUpdates={canExecuteSystemUpdates}
          portRequestTargetID={portRequestTargetID || undefined}
          ambiguousPortTargetID={unresolvedAmbiguousPortRequest?.target_id}
          onPortReconfigure={executePortReconfigure}
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
                : `対象: ${confirmationTargets.map((target) => target.name || target.target_id).join("、")}。更新ジョブの受付後、対象の更新エージェントが接続状態を確認して適用します。`}
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
  canManageUpdaterSecrets,
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
  canManageUpdaterSecrets: boolean;
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
    <Card className="min-w-0">
      <CardHeader className="min-w-0 gap-3 sm:flex-row sm:items-start sm:justify-between">
        <div className="min-w-0">
          <CardTitle className="flex items-center gap-2"><Download className="size-5" />システム更新</CardTitle>
          <CardDescription className="mt-1">Control Panelで更新ジョブを作成し、各ホストのHost Agentがoutbound通信で受け取って安全に適用します。</CardDescription>
        </div>
        <div className="flex min-w-0 flex-wrap gap-2">
          <Button variant="outline" size="sm" onClick={onRefresh} disabled={!canRead || isLoading}><RefreshCcw className="size-4" />再取得</Button>
          <Button className="h-auto max-w-full whitespace-normal text-left sm:h-8 sm:whitespace-nowrap" size="sm" onClick={onRequestBatch} disabled={!canExecute || availableCount === 0 || isCreating} title={!canExecute ? "system_updates.execute 権限が必要です。" : undefined}>
            {isCreating ? <LoaderCircle className="size-4 animate-spin" /> : <Download className="size-4" />}
            {batchProgress ? `${batchProgress.completed}/${batchProgress.total} 受付中` : `更新可能なものを順次受付（ホストごと並行）${availableCount ? ` (${availableCount})` : ""}`}
          </Button>
          {batchProgress ? <span className="sr-only" role="status" aria-live="polite">{batchProgress.completed}/{batchProgress.total}件の更新ジョブを受付済みです。</span> : null}
        </div>
      </CardHeader>
      <CardContent className="space-y-5">
        <div className="rounded-md border border-blue-200 bg-blue-50/70 p-3 text-xs leading-5 text-blue-950 dark:border-blue-900 dark:bg-blue-950/30 dark:text-blue-100">
          Docker配備では、Docker Bundleのバージョンと各サービスのバージョンは別に管理されます。表示が異なっていても異常ではなく、対象ホストのHost AgentがBundle設定を照合して更新します。
          pull_v2ではHost AgentからControl Panelへ接続するため、Control Panelから各ホストへのSSH接続やUpdater用TCP受信ポートは使いません。
        </div>

        {canRead && !isError && !isLoading ? (
          <UpdateAgentStatus
            updaters={updaters}
            targets={targets}
            jobs={jobs}
            timezone={timezone}
            canEdit={canExecute}
            canManageSecrets={canManageUpdaterSecrets}
          />
        ) : null}

        {!canRead ? (
          <div className="rounded-md border border-dashed p-6 text-sm text-muted-foreground">更新対象と履歴を確認するには「system_updates.read」権限が必要です。</div>
        ) : isError ? (
          <div className="flex flex-wrap items-center justify-between gap-3 rounded-md border border-amber-300 bg-amber-50 p-4 text-sm text-amber-950 dark:border-amber-900 dark:bg-amber-950/35 dark:text-amber-100">
            <span>{systemUpdateErrorMessage(error, "更新対象を取得できませんでした。Control Panelと各Host AgentのHeartbeatを確認してください。")}</span>
            <Button variant="outline" size="sm" onClick={onRefresh}>再試行</Button>
          </div>
        ) : isLoading ? (
          <div className="rounded-md border border-dashed p-6 text-sm text-muted-foreground">更新対象を読み込み中です。</div>
        ) : targets.length === 0 ? (
          <div className="rounded-md border border-dashed p-6 text-sm text-muted-foreground">更新対象が未設定です。各ホストのHost Agentと対象サービスを登録してください。pull_v2ではSSH設定やUpdater用TCP受信ポートは不要です。</div>
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
                    <TableHead>対象 / 操作</TableHead><TableHead>バージョン / ポート</TableHead><TableHead>状態</TableHead><TableHead>進捗</TableHead><TableHead>メッセージ</TableHead><TableHead>依頼者 / 日時</TableHead><TableHead className="text-right">操作</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {jobs.map((job) => {
                    const progress = systemUpdateProgress(job);
                    const jobMessage = systemUpdateJobMessage(job);
                    const [jobMessageSummary, ...jobMessageDetails] = jobMessage.split("\n");
                    return (
                      <TableRow key={job.id}>
                        <TableCell><div className="font-medium">{targetDisplayName(job, targets)}</div><div className="text-xs text-muted-foreground">{systemUpdateJobOperationLabel(job)} · {systemUpdateDeploymentLabel(job.deployment_mode)}</div></TableCell>
                        <TableCell className="whitespace-nowrap text-xs">{systemUpdateJobChangeSummary(job)}</TableCell>
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

function UpdateAgentStatus({
  updaters,
  targets,
  jobs,
  timezone,
  canEdit,
  canManageSecrets,
}: {
  updaters: SystemUpdateAgentStatus[];
  targets: SystemUpdateTarget[];
  jobs: SystemUpdateJob[];
  timezone?: string;
  canEdit: boolean;
  canManageSecrets: boolean;
}) {
  return (
    <div className="rounded-lg border bg-muted/15 p-4">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <div>
          <div className="text-sm font-medium">Host Agent / 互換Updater</div>
          <div className="mt-0.5 text-xs text-muted-foreground">pull_v2 Host Agentは各ホストからControl Panelへoutbound接続して更新ジョブを受け取ります。</div>
        </div>
        {updaters.length === 0 ? <Badge variant="secondary">未登録</Badge> : null}
      </div>
      {updaters.length === 0 ? (
        <p className="mt-3 text-xs text-amber-700 dark:text-amber-300">Host Agentまたは互換Updaterが登録されていません。更新ジョブは開始できません。</p>
      ) : (
        <div className="mt-3 grid gap-2 lg:grid-cols-2 [&>*:only-child]:col-span-full">
          {updaters.map((updater) => {
            const policy = systemUpdateUpdaterPolicyState(updater);
            return (
              <div key={updater.updater_id} className="flex flex-wrap items-center justify-between gap-3 rounded-md border bg-background/70 p-3 text-xs">
                <div className="min-w-0">
                  <div className="truncate font-medium">{updater.name || updater.updater_id}</div>
                  <div className="mt-0.5 text-muted-foreground">{updater.updater_id}{updater.version ? ` · ${updater.version}` : ""}</div>
                  <div className="mt-0.5 text-muted-foreground">最終Heartbeat: {formatOptionalDate(updater.last_heartbeat_at, timezone)}</div>
                  {updater.policy_error_code || updater.policy_error ? <div className="mt-1 break-words text-destructive">反映情報: {systemUpdatePolicyErrorMessage(updater.policy_error_code || updater.policy_error)}</div> : null}
                </div>
                <div className="flex items-center gap-2">
                  <Badge variant={policy.tone}>{policy.label}</Badge>
                  <UpdaterSettingsPanel updater={updater} availableTargets={targets} jobs={jobs} canEdit={canEdit} canManageSecrets={canManageSecrets} />
                </div>
              </div>
            );
          })}
        </div>
      )}
    </div>
  );
}

function SystemUpdateTargetPanel({ target, updaters, hosts, timezone, activeJob, canExecute, disabled, onRequest }: { target: SystemUpdateTarget; updaters: SystemUpdateAgentStatus[]; hosts: SystemUpdateHostStatus[]; timezone?: string; activeJob?: SystemUpdateJob; canExecute: boolean; disabled: boolean; onRequest: () => void }) {
  const strategy = systemUpdateStrategyForTarget(target);
  const connectivity = systemUpdateConnectivity(target, updaters, hosts);
  const updaterPolicy = connectivity.updater ? systemUpdateUpdaterPolicyState(connectivity.updater) : null;
  const operationEligibility = systemUpdateSoftwareOperationEligibility(target);
  const canStart = updateCanStart(target, activeJob, updaters, hosts);
  const hostName = connectivity.host?.name || target.host_id || "ホスト未設定";
  const reachabilityLabel = systemUpdateHostReachabilityLabel(connectivity.reachability);
  const reachabilityMessage = systemUpdateHostReachabilityMessage(connectivity.host?.reachability_code);
  const blockedReason = !operationEligibility.ready ? operationEligibility.reason : target.blocked_reason;
  const blocked = blockedReason
    ? systemUpdateTargetBlockedReason(blockedReason)
    : !connectivity.updater
      ? systemUpdateTargetBlockedReason("updater_not_configured")
      : !connectivity.agentOnline
        ? systemUpdateTargetBlockedReason("updater_offline")
        : !updaterPolicy?.ready
          ? updaterPolicy?.label === "反映失敗"
            ? "更新エージェントの設定反映に失敗しています。設定画面でエラーを確認してください。"
            : updaterPolicy?.label === "未設定"
              ? "更新エージェントの設定が未設定です。設定を保存してください。"
              : "更新エージェントが新しい設定を反映中です。反映済みになるまでお待ちください。"
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

function RegisteredServicesCard({
  canViewNodeInfo,
  nodesError,
  nodesLoading,
  nodeRows,
  timezone,
  appVersion,
  targets,
  updaters,
  jobsByTarget,
  canExecuteSystemUpdates,
  portRequestTargetID,
  ambiguousPortTargetID,
  onPortReconfigure,
  onRefresh,
}: {
  canViewNodeInfo: boolean;
  nodesError: boolean;
  nodesLoading: boolean;
  nodeRows: WorkerNode[];
  timezone?: string;
  appVersion?: AppVersion;
  targets: SystemUpdateTarget[];
  updaters: SystemUpdateAgentStatus[];
  jobsByTarget: Map<string, SystemUpdateJob>;
  canExecuteSystemUpdates: boolean;
  portRequestTargetID?: string;
  ambiguousPortTargetID?: string;
  onPortReconfigure: (request: SystemUpdatePortReconfigureCreateRequest) => Promise<void>;
  onRefresh: () => void;
}) {
  const nodeOperation = (node: WorkerNode) => {
    const nodeID = nodeIdentity(node);
    const target = targets.find((candidate) => candidate.target_id === nodeID);
    const updater = target?.updater_id
      ? updaters.find((candidate) => candidate.updater_id === target.updater_id)
      : undefined;
    const requestState: SystemUpdateRequestState = portRequestTargetID === nodeID
      ? "pending"
      : ambiguousPortTargetID === nodeID
        ? "ambiguous"
        : "idle";
    return {
      target,
      updater,
      latestJob: target ? jobsByTarget.get(target.target_id) : undefined,
      requestState,
    };
  };
  const operationalRows = nodeRows.filter((node) => node.service_type !== "update_agent");
  const updaterRows = nodeRows.filter((node) => node.service_type === "update_agent");
  return (
    <Card className="min-w-0">
      <CardHeader><CardTitle className="flex items-center gap-2"><ServerCog className="size-5" />登録済みサービス</CardTitle><CardDescription>報告バージョンと、希望・適用・Node報告のendpointを区別して表示します。</CardDescription></CardHeader>
      <CardContent>
        {!canViewNodeInfo ? <div className="rounded-md border border-dashed p-6 text-sm text-muted-foreground">登録済みNodeの情報を確認する権限がありません。管理者にNode情報の閲覧権限を依頼してください。</div>
          : nodesError ? <div className="flex flex-wrap items-center justify-between gap-3 rounded-md border border-amber-300 bg-amber-50 p-4 text-sm text-amber-950 dark:border-amber-900 dark:bg-amber-950/35 dark:text-amber-100"><span>登録済みNodeの情報を取得できませんでした。通信状態とControl Panelのログを確認してください。</span><Button variant="outline" size="sm" onClick={onRefresh}>再試行</Button></div>
            : nodesLoading ? <div className="rounded-md border border-dashed p-6 text-sm text-muted-foreground">読み込み中</div>
              : nodeRows.length === 0 ? <div className="rounded-md border border-dashed p-6 text-sm text-muted-foreground">登録済みNodeがありません。Node登録ページで作成したNodeがある場合は、ページを更新してください。</div>
                 : <>
                      <div className="grid gap-4 2xl:hidden">
                        <div className="space-y-2">
                          <SectionLabel title="Nodeサービス" description="Worker、Encoder / Recorder、その他のサービス" />
                          {operationalRows.length > 0 ? (
                            <div className="grid gap-3">
                              {operationalRows.map((node) => (
                                <RegisteredServiceMobileCard
                                  key={node.service_id || node.id}
                                  node={node}
                                  operation={nodeOperation(node)}
                                  timezone={timezone}
                                  appVersion={appVersion}
                                  canExecute={canExecuteSystemUpdates}
                                  onRequest={onPortReconfigure}
                                />
                              ))}
                            </div>
                          ) : <div className="rounded-md border border-dashed p-4 text-sm text-muted-foreground">Nodeサービスは登録されていません。</div>}
                        </div>
                        {updaterRows.length > 0 ? (
                          <div className="space-y-2">
                            <SectionLabel title="Updater / Host Agent" description="ホスト単位の更新専用。通常のNode endpointとは別に管理します。" />
                            <div className="grid gap-3">
                              {updaterRows.map((node) => (
                                <RegisteredServiceMobileCard
                                  key={node.service_id || node.id}
                                  node={node}
                                  operation={nodeOperation(node)}
                                  timezone={timezone}
                                  appVersion={appVersion}
                                  canExecute={canExecuteSystemUpdates}
                                  onRequest={onPortReconfigure}
                                />
                              ))}
                            </div>
                          </div>
                        ) : null}
                      </div>
                     <div className="hidden overflow-x-auto rounded-md border 2xl:block">
                       <Table className="min-w-[980px]">
                         <TableHeader>
                           <TableRow>
                             <TableHead>サービス</TableHead><TableHead>種別 / バージョン</TableHead><TableHead>状態</TableHead><TableHead className="min-w-80">Endpoint / ポート変更</TableHead>
                          </TableRow>
                        </TableHeader>
                        <TableBody>
                           {nodeRows.map((node, index) => {
                             const operation = nodeOperation(node);
                             return (
                               <Fragment key={node.service_id || node.id}>
                               {(index === 0 || (nodeRows[index - 1]?.service_type === "update_agent") !== (node.service_type === "update_agent")) ? (
                                 <TableRow className="bg-muted/25 hover:bg-muted/25">
                                   <TableCell colSpan={4}>
                                     <SectionLabel title={node.service_type === "update_agent" ? "Updater / Host Agent" : "Nodeサービス"} description={node.service_type === "update_agent" ? "ホスト単位の更新専用" : "Worker、Encoder / Recorder、その他のサービス"} />
                                   </TableCell>
                                 </TableRow>
                               ) : null}
                               <TableRow>
                                 <TableCell>
                                  <div className="font-medium">{node.service_name || node.service_id || "-"}</div>
                                  <div className="mt-1 font-mono text-xs text-muted-foreground">{node.service_id || node.id}</div>
                                </TableCell>
                                <TableCell>
                                  <div>{serviceTypeLabel(node.service_type)}</div>
                                  <div className="mt-1">{node.reported_version || node.version || "未報告"}</div>
                                  <div className="mt-1 inline-flex items-center gap-1 font-mono text-xs text-muted-foreground"><GitCommit className="size-3.5" />{shortCommit(node.reported_commit)}</div>
                                  <div className="mt-1 text-xs text-muted-foreground">{formatOptionalDate(node.reported_build_date, timezone)}</div>
                                </TableCell>
                                <TableCell className="space-y-2">
                                  <StatusBadge status={node.health_status || node.status || "-"} />
                                  <div><UpdateStatusBadge state={nodeUpdateState(node, serviceUpdateForNode(node, appVersion))} /></div>
                                </TableCell>
                                <TableCell>
                                  <ServiceEndpointSummary node={node} />
                                  <PortReconfigureControl
                                    key={portControlKey(node, operation.target)}
                                    node={node}
                                    target={operation.target}
                                    updater={operation.updater}
                                    latestJob={operation.latestJob}
                                    requestState={operation.requestState}
                                    canExecute={canExecuteSystemUpdates}
                                    onRequest={onPortReconfigure}
                                  />
                                </TableCell>
                               </TableRow>
                               </Fragment>
                             );
                           })}
                         </TableBody>
                       </Table>
                     </div>
                     <p className="mt-3 text-xs text-muted-foreground">「現在適用中」が実際の接続先です。「希望」は未適用の値を含み、Node報告と異なる場合があります。</p>
                   </>}
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

function RegisteredServiceMobileCard({
  node,
  operation,
  timezone,
  appVersion,
  canExecute,
  onRequest,
}: {
  node: WorkerNode;
  operation: RegisteredServiceOperation;
  timezone?: string;
  appVersion?: AppVersion;
  canExecute: boolean;
  onRequest: (request: SystemUpdatePortReconfigureCreateRequest) => Promise<void>;
}) {
  return (
    <article className="min-w-0 overflow-hidden rounded-md border bg-background/60 p-3">
      <div className="min-w-0">
        <div className="break-words font-medium">{node.service_name || node.service_id || "-"}</div>
        <div className="mt-1 break-all font-mono text-xs text-muted-foreground">{node.service_id || node.id}</div>
      </div>
      <div className="mt-3 grid min-w-0 gap-3 sm:grid-cols-2">
        <div className="min-w-0">
          <div className="text-xs text-muted-foreground">種別 / バージョン</div>
          <div className="mt-1 break-words">{serviceTypeLabel(node.service_type)}</div>
          <div className="mt-1 break-words">{node.reported_version || node.version || "未報告"}</div>
          <div className="mt-1 break-all font-mono text-xs text-muted-foreground"><GitCommit className="mr-1 inline-block size-3.5" />{shortCommit(node.reported_commit)}</div>
          <div className="mt-1 text-xs text-muted-foreground">{formatOptionalDate(node.reported_build_date, timezone)}</div>
        </div>
        <div className="min-w-0 space-y-2">
          <div className="text-xs text-muted-foreground">状態</div>
          <StatusBadge status={node.health_status || node.status || "-"} />
          <div><UpdateStatusBadge state={nodeUpdateState(node, serviceUpdateForNode(node, appVersion))} /></div>
        </div>
      </div>
      <div className="mt-3 min-w-0 rounded-md border bg-muted/10 p-3">
        <div className="text-xs font-medium">Endpoint / ポート変更</div>
        <div className="mt-2 min-w-0">
          <ServiceEndpointSummary node={node} />
          <PortReconfigureControl
            node={node}
            target={operation.target}
            updater={operation.updater}
            latestJob={operation.latestJob}
            requestState={operation.requestState}
            canExecute={canExecute}
            onRequest={onRequest}
          />
        </div>
      </div>
    </article>
  );
}

function nodeIdentity(node: WorkerNode) { return node.service_id || node.id || ""; }

function InfoItem({ label, value, monospace = false }: { label: string; value: ReactNode; monospace?: boolean }) {
  return <div className="rounded-md border bg-muted/20 px-3 py-2"><div className="text-xs text-muted-foreground">{label}</div><div className={monospace ? "font-mono text-sm" : "text-sm"}>{value}</div></div>;
}

function SectionLabel({ title, description }: { title: string; description: string }) {
  return (
    <div className="rounded-md border bg-muted/20 px-3 py-2">
      <div className="text-sm font-medium">{title}</div>
      <div className="mt-0.5 text-xs text-muted-foreground">{description}</div>
    </div>
  );
}

function ServiceEndpointSummary({ node }: { node: WorkerNode }) {
  const state = nodeEndpointState(node);
  if (node.service_type === "update_agent") {
    return <UpdaterTransportSummary node={node} state={state} />;
  }
  if (state.kind === "pull_v2") {
    return (
      <div className="space-y-1 text-xs">
        <div className="font-medium">Host Agent（受信ポートなし）</div>
        <div className="break-words text-muted-foreground">実行ホスト: {state.executionHostID || "未割り当て"} · Ownership epoch: {state.ownershipEpoch ?? 0}</div>
      </div>
    );
  }
  return (
    <div className="space-y-1.5 text-xs" aria-label={`${node.service_name || node.service_id || node.id} のendpoint状態`}>
      <EndpointStateLine label="希望endpoint（未適用を含む）" value={state.desired.url || "未設定"} />
      <EndpointStateLine label="現在適用中のendpoint" value={state.applied.url || "未報告"} effective />
      <EndpointStateLine label="Node報告endpoint" value={state.reported.url || "未報告"} />
      <div className="flex flex-wrap items-center gap-2 pt-1">
        <Badge variant={state.status.tone}>{state.status.label}</Badge>
        <span className="text-muted-foreground">Endpoint revision: {state.revision ?? "未報告"}</span>
      </div>
      <p className="text-muted-foreground">{state.status.detail}</p>
    </div>
  );
}

function UpdaterTransportSummary({
  node,
  state,
}: {
  node: WorkerNode;
  state: ReturnType<typeof nodeEndpointState>;
}) {
  const transportMode = state.transportMode || "未報告";
  const isPull = transportMode === "pull_v2";
  const managementEndpoint = state.applied.url || state.desired.url || state.reported.url;
  return (
    <div className="space-y-1.5 text-xs" aria-label={`${node.service_name || node.service_id || node.id} のUpdater transport状態`}>
      <div className="flex flex-wrap items-center gap-2">
        <Badge variant="secondary">Updater / Host Agent</Badge>
        <span className="font-medium">{isPull ? "受信ポートなし（Outbound HTTPS）" : "SSH（legacy）"}</span>
      </div>
      <div className="text-muted-foreground">transport_mode: {transportMode}</div>
      {isPull ? (
        <>
          <div className="break-words text-muted-foreground">実行ホスト: {state.executionHostID || "未割り当て"}</div>
          <div className="text-muted-foreground">Ownership epoch: {state.ownershipEpoch ?? "未報告"}</div>
        </>
      ) : (
        <div className="break-all text-muted-foreground">管理endpoint: {managementEndpoint || "未報告"}</div>
      )}
      <div className="text-muted-foreground">通常のNode endpointとは別の更新管理経路</div>
    </div>
  );
}

function EndpointStateLine({ label, value, effective = false }: { label: string; value: string; effective?: boolean }) {
  return (
    <div>
      <div className="text-muted-foreground">{label}</div>
      <div className={effective ? "break-all font-medium text-foreground" : "break-all text-muted-foreground"}>{value}</div>
    </div>
  );
}

function PortReconfigureControl({
  node,
  target,
  updater,
  latestJob,
  requestState,
  canExecute,
  onRequest,
}: {
  node: WorkerNode;
  target?: SystemUpdateTarget;
  updater?: SystemUpdateAgentStatus;
  latestJob?: SystemUpdateJob;
  requestState: "idle" | "pending" | "ambiguous";
  canExecute: boolean;
  onRequest: (request: SystemUpdatePortReconfigureCreateRequest) => Promise<void>;
}) {
  const inputID = useId();
  const reasonID = `${inputID}-reason`;
  const advertisedInputID = `${inputID}-advertised`;
  const publishedInputID = `${inputID}-published`;
  const containerInputID = `${inputID}-container`;
  const [newPort, setNewPort] = useState(String(node.applied_endpoint?.port || ""));
  const [newAdvertisedPort, setNewAdvertisedPort] = useState(String(target?.port_mapping?.advertised_port || node.applied_endpoint?.port || ""));
  const [newPublishedPort, setNewPublishedPort] = useState(String(target?.port_mapping?.published_port || ""));
  const [newContainerPort, setNewContainerPort] = useState(String(target?.port_mapping?.container_port || ""));
  const [advancedOpen, setAdvancedOpen] = useState(false);
  const [confirmationOpen, setConfirmationOpen] = useState(false);
  const [locallySubmitting, setLocallySubmitting] = useState(false);
  const submittingRef = useRef(false);
  const eligibility = systemUpdatePortReconfigureEligibility({ target, updater, node, latestJob, requestState });
  const hiddenReasons = new Set([
    "unsupported_target",
    "unsupported_transport",
  ]);
  if (
    hiddenReasons.has(eligibility.reason)
    || (eligibility.reason === "port_contract_unavailable" && (!target || !updater))
  ) return null;

  const dockerMode = eligibility.deploymentMode === "docker";
  const parsedPort = Number(newPort);
  const parsedAdvertisedPort = Number(newAdvertisedPort);
  const parsedPublishedPort = Number(newPublishedPort);
  const parsedContainerPort = Number(newContainerPort);
  const validPort = validPortInput(newPort, 1024);
  const validAdvertisedPort = validPortInput(newAdvertisedPort, 1);
  const validPublishedPort = validPortInput(newPublishedPort, 1024);
  const validContainerPort = validPortInput(newContainerPort, 1024);
  const validDockerPorts = validAdvertisedPort && validPublishedPort && validContainerPort;
  const unchanged = dockerMode
    ? validDockerPorts
      && parsedAdvertisedPort === eligibility.dockerMapping?.advertised_port
      && parsedPublishedPort === eligibility.dockerMapping?.published_port
      && parsedContainerPort === eligibility.dockerMapping?.container_port
    : validPort && parsedPort === eligibility.currentPort;
  const reason = !canExecute
    ? "permission_denied"
    : !eligibility.ready
      ? eligibility.reason
      : dockerMode && !advancedOpen
        ? "advanced_mode_required"
      : dockerMode && !validAdvertisedPort
        ? "invalid_advertised_port"
      : dockerMode && !validPublishedPort
        ? "invalid_published_port"
      : dockerMode && !validContainerPort
        ? "invalid_container_port"
      : !dockerMode && !validPort
        ? "invalid_service_port"
        : unchanged
          ? "port_unchanged"
          : "";
  const submitting = requestState === "pending" || locallySubmitting;
  const ready = canExecute
    && eligibility.ready
    && (dockerMode ? advancedOpen && validDockerPorts : validPort)
    && !unchanged
    && !submitting;
  const reasonMessage = portReconfigureReasonMessage(reason);
  const operationResult = latestJob?.operation === "port_reconfigure"
    ? systemUpdatePortReconfigureResultLabel(latestJob.port_reconfigure?.result)
    : "";

  const confirm = async () => {
    if (
      !ready
      || submittingRef.current
      || !target
      || eligibility.currentPort === undefined
      || eligibility.endpointRevision === undefined
    ) return;
    const idempotencyKey = newIdempotencyKey(`port-${target.target_id}`);
    const request = dockerMode && eligibility.dockerMapping
      ? systemUpdateDockerPortReconfigureRequest({
          targetID: target.target_id,
          currentMapping: eligibility.dockerMapping,
          newAdvertisedPort: parsedAdvertisedPort,
          newPublishedPort: parsedPublishedPort,
          newContainerPort: parsedContainerPort,
          expectedEndpointRevision: eligibility.endpointRevision,
          idempotencyKey,
        })
      : systemUpdatePortReconfigureRequest({
          targetID: target.target_id,
          currentPort: eligibility.currentPort,
          newPort: parsedPort,
          expectedEndpointRevision: eligibility.endpointRevision,
          idempotencyKey,
        });
    submittingRef.current = true;
    setLocallySubmitting(true);
    setConfirmationOpen(false);
    try {
      await onRequest(request);
    } finally {
      submittingRef.current = false;
      setLocallySubmitting(false);
    }
  };

  return (
    <div className="mt-3 space-y-2 rounded-md border bg-background/70 p-3">
      <div>
        <div className="flex flex-wrap items-center justify-between gap-2">
          <div className="text-xs font-medium">サービスのポート変更</div>
          {dockerMode ? <Badge variant={dockerPortMappingTone(target?.port_mapping?.state)}>{dockerPortMappingLabel(target?.port_mapping?.state)}</Badge> : null}
        </div>
        <p className="text-xs text-muted-foreground">
          現在適用中: {eligibility.currentPort ?? "未報告"} · endpoint revision {eligibility.endpointRevision ?? "未報告"}
        </p>
      </div>
      {dockerMode ? (
        <div className="space-y-2 rounded-md border border-blue-200 bg-blue-50/50 p-2 text-xs text-blue-950 dark:border-blue-900 dark:bg-blue-950/20 dark:text-blue-100">
          <div className="grid gap-1 sm:grid-cols-3">
            <PortMappingValue label="広告endpoint" value={formatPort(eligibility.dockerMapping?.advertised_port)} />
            <PortMappingValue label="localhost公開" value={`${eligibility.dockerMapping?.published_host_ip || "127.0.0.1"}:${formatPort(eligibility.dockerMapping?.published_port)}`} />
            <PortMappingValue label="container待受" value={formatPort(eligibility.dockerMapping?.container_port)} />
          </div>
          <p>
            Docker published portは127.0.0.1固定です。公開originやreverse proxy設定は自動変更しません。広告endpointを別のportにする場合は、既存proxyの転送先を別途確認してください。
          </p>
          <Button
            type="button"
            size="sm"
            variant="outline"
            aria-expanded={advancedOpen}
            aria-controls={`${inputID}-advanced`}
            onClick={() => setAdvancedOpen((open) => !open)}
            disabled={!canExecute || !eligibility.ready || submitting}
          >
            {advancedOpen ? "詳細設定を閉じる" : "Docker詳細設定を開く"}
          </Button>
        </div>
      ) : null}
      <div id={`${inputID}-advanced`} className={dockerMode && !advancedOpen ? "hidden" : "space-y-2"}>
        <div className={dockerMode ? "grid gap-2 sm:grid-cols-3" : "flex flex-wrap items-end gap-2"}>
          {dockerMode ? (
            <>
              <PortInput
                id={advertisedInputID}
                label="公開endpointポート"
                help="Control Panelと他サービスへ広告する到達先"
                value={newAdvertisedPort}
                onChange={setNewAdvertisedPort}
                valid={validAdvertisedPort}
                minimum={1}
                describedBy={reasonID}
                disabled={!canExecute || !eligibility.ready || submitting}
              />
              <PortInput
                id={publishedInputID}
                label="localhost publishedポート"
                help="Hostの127.0.0.1でDockerが公開するport"
                value={newPublishedPort}
                onChange={setNewPublishedPort}
                valid={validPublishedPort}
                minimum={1024}
                describedBy={reasonID}
                disabled={!canExecute || !eligibility.ready || submitting}
              />
              <PortInput
                id={containerInputID}
                label="container待受ポート"
                help="Nodeプロセスがcontainer内でlistenするport"
                value={newContainerPort}
                onChange={setNewContainerPort}
                valid={validContainerPort}
                minimum={1024}
                describedBy={reasonID}
                disabled={!canExecute || !eligibility.ready || submitting}
              />
            </>
          ) : (
            <div className="min-w-36 flex-1 space-y-1">
              <label className="text-xs font-medium" htmlFor={inputID}>新しいサービスポート</label>
              <Input
                id={inputID}
                type="number"
                inputMode="numeric"
                min={1024}
                max={65535}
                step={1}
                value={newPort}
                onChange={(event) => setNewPort(event.target.value)}
                aria-invalid={!validPort}
                aria-describedby={reasonID}
                disabled={!canExecute || !eligibility.ready || submitting}
              />
            </div>
          )}
        </div>
        <AlertDialog open={confirmationOpen} onOpenChange={setConfirmationOpen}>
          <AlertDialogTrigger asChild>
            <Button
              type="button"
              size="sm"
              disabled={!ready}
              aria-busy={submitting}
              title={reasonMessage || undefined}
            >
              {submitting ? <LoaderCircle className="size-4 animate-spin" /> : <ServerCog className="size-4" />}
              ポート変更
            </Button>
          </AlertDialogTrigger>
          <AlertDialogContent>
            <AlertDialogHeader>
              <AlertDialogMedia className="bg-amber-50 text-amber-700 dark:bg-amber-950 dark:text-amber-300"><ShieldAlert /></AlertDialogMedia>
              <AlertDialogTitle>{target?.name || target?.target_id} のポートを変更しますか？</AlertDialogTitle>
              <AlertDialogDescription>
                {dockerMode
                  ? `広告 ${eligibility.dockerMapping?.advertised_port} → ${parsedAdvertisedPort}、localhost published ${eligibility.dockerMapping?.published_port} → ${parsedPublishedPort}、container待受 ${eligibility.dockerMapping?.container_port} → ${parsedContainerPort} に変更します。reverse proxy設定は変更しません。`
                  : `現在適用中の ${eligibility.currentPort} から ${parsedPort} へ変更します。`}
                失敗時は以前のポートへロールバックし、結果を更新履歴とendpoint状態に表示します。
              </AlertDialogDescription>
            </AlertDialogHeader>
            <AlertDialogFooter>
              <AlertDialogCancel>キャンセル</AlertDialogCancel>
              <AlertDialogAction onClick={() => void confirm()} disabled={submitting}>ポート変更を開始</AlertDialogAction>
            </AlertDialogFooter>
          </AlertDialogContent>
        </AlertDialog>
      </div>
      <div id={reasonID} className={reason === "request_ambiguous" || reason === "recovery_required" ? "text-xs text-destructive" : "text-xs text-muted-foreground"} role="status" aria-live="polite">
        {reasonMessage || "変更前に現在のendpoint revisionを固定して送信します。"}
      </div>
      {operationResult ? <div className="text-xs font-medium">直近のポート変更結果: {operationResult}</div> : null}
    </div>
  );
}

function PortInput({
  id,
  label,
  help,
  value,
  onChange,
  valid,
  minimum,
  describedBy,
  disabled,
}: {
  id: string;
  label: string;
  help: string;
  value: string;
  onChange: (value: string) => void;
  valid: boolean;
  minimum: number;
  describedBy: string;
  disabled: boolean;
}) {
  const helpID = `${id}-help`;
  return (
    <div className="space-y-1">
      <label className="text-xs font-medium" htmlFor={id}>{label}</label>
      <Input
        id={id}
        type="number"
        inputMode="numeric"
        min={minimum}
        max={65535}
        step={1}
        value={value}
        onChange={(event) => onChange(event.target.value)}
        aria-invalid={!valid}
        aria-describedby={`${helpID} ${describedBy}`}
        disabled={disabled}
      />
      <p id={helpID} className="text-[11px] leading-4 text-muted-foreground">{help}</p>
    </div>
  );
}

function PortMappingValue({ label, value }: { label: string; value: string }) {
  return <div><span className="text-muted-foreground">{label}: </span><span className="font-mono">{value}</span></div>;
}

function validPortInput(value: string, minimum: number) {
  const parsed = Number(value);
  return /^[0-9]+$/.test(value.trim()) && Number.isSafeInteger(parsed) && parsed >= minimum && parsed <= 65535;
}

function formatPort(port?: number) {
  return Number.isSafeInteger(port) && Number(port) > 0 ? String(port) : "未報告";
}

function dockerPortMappingLabel(state?: string) {
  if (state === "applied") return "mapping反映済み";
  if (state === "drifted") return "mapping差分あり";
  return "mapping未確認";
}

function dockerPortMappingTone(state?: string): "default" | "secondary" | "destructive" | "outline" {
  if (state === "applied") return "default";
  if (state === "drifted") return "destructive";
  return "secondary";
}

function portReconfigureReasonMessage(reason: string) {
  const messages: Record<string, string> = {
    permission_denied: "ポート変更には system_updates.execute 権限が必要です。",
    invalid_service_port: "1024〜65535の整数を入力してください。",
    invalid_advertised_port: "公開endpointには1〜65535の整数を入力してください。1024未満は既存reverse proxyの公開portとしてのみ使用してください。",
    invalid_published_port: "localhost publishedポートには1024〜65535の整数を入力してください。",
    invalid_container_port: "container待受ポートには1024〜65535の整数を入力してください。",
    port_unchanged: "現在適用中のポートとは異なる値を入力してください。",
    advanced_mode_required: "Dockerの3つのポートを区別して確認するため、詳細設定を開いてください。",
    unsupported_deployment: "この配備方式のポート変更は利用できません。",
    docker_mapping_drifted: "Docker mappingに差分があります。安全な現在値を確認できるまでポート変更を開始できません。",
    docker_mapping_unavailable: "Host Agentから検証済みDocker mappingが報告されていません。Agent・Executor・Compose設定を更新して再取得してください。",
    request_pending: "ポート変更要求を送信中です。",
    request_ambiguous: "前回要求の結果が不明です。履歴で確認できるまで再送しません。",
    target_busy: "サービスが使用中のためポートを変更できません。",
    active_job: "このサービスでは別の更新ジョブが進行中です。",
    recovery_required: "以前の更新結果を復旧・確認中です。",
    endpoint_recovery: "Endpointをロールバック中です。",
    endpoint_not_applied: "Endpointが反映済みになるまで変更できません。",
    updater_not_ready: "Host Agentまたは設定が反映済みになるまで変更できません。",
    operation_eligibility_unavailable: "Control Panelからポート変更の適格性が報告されていません。APIとHost Agentを更新してから再取得してください。",
    updater_missing: "このサービスを管理するHost Agentが割り当てられていません。",
    updater_offline: "Host Agentがオフラインです。Heartbeatを確認してください。",
    target_unreachable: "Host Agentが対象ホストの到達状態を確認できません。",
    target_reachability_unknown: "Host Agentによる対象ホストの到達確認を待っています。",
    updater_policy_pending: "Host Agentが保存済み設定を反映するまで待っています。",
    updater_policy_failed: "Host Agentが保存済み設定を反映できませんでした。設定画面のエラーを確認してください。",
    updater_policy_mismatch: "Host Agentの設定revisionが一致していません。反映完了を待ってください。",
    updater_policy_target_type_mismatch: "サービス種別がHost Agentの対象設定と一致していません。",
    system_update_target_busy: "サービスが配信処理で使用中のため、ポートを変更できません。",
    system_update_port_reconfigure_not_ready: "このサービスは現在ポートを変更できません。EndpointとHost Agentの状態を確認してください。",
    service_port_reserved: "同じホストで指定したポートが既に使用または予約されています。",
    system_update_endpoint_revision_conflict: "Endpoint revisionが変わりました。Node情報を再取得してください。",
  };
  return messages[reason] || (reason ? systemUpdateTargetBlockedReason(reason) : "");
}

function compareServiceRows(a: WorkerNode, b: WorkerNode) { const type = serviceTypeLabel(a.service_type).localeCompare(serviceTypeLabel(b.service_type), "ja"); return type !== 0 ? type : (a.service_name || a.service_id || "").localeCompare(b.service_name || b.service_id || "", "ja"); }
function portControlKey(node: WorkerNode, target?: SystemUpdateTarget) {
  const mapping = target?.port_mapping;
  return [
    nodeIdentity(node),
    node.endpoint_revision || 0,
    node.applied_endpoint?.port || 0,
    mapping?.state || "",
    mapping?.advertised_port || 0,
    mapping?.published_port || 0,
    mapping?.container_port || 0,
    mapping?.config_revision || 0,
  ].join(":");
}
function compareUpdateJobs(a: SystemUpdateJob, b: SystemUpdateJob) { return Date.parse(b.created_at || b.updated_at || "") - Date.parse(a.created_at || a.updated_at || ""); }
function latestJobsByTarget(jobs: SystemUpdateJob[]) { const result = new Map<string, SystemUpdateJob>(); for (const job of jobs) if (!result.has(job.target_id)) result.set(job.target_id, job); return result; }
function updateCanStart(target: SystemUpdateTarget, latestJob: SystemUpdateJob | undefined, updaters: SystemUpdateAgentStatus[], hosts: SystemUpdateHostStatus[]) {
  const operationEligibility = systemUpdateSoftwareOperationEligibility(target);
  const eligibleForStrategy = target.eligible || (systemUpdateStrategyForTarget(target) === "when_idle" && target.blocked_reason === "stream_active");
  return operationEligibility.ready
    && target.update_available
    && eligibleForStrategy
    && systemUpdateConnectivity(target, updaters, hosts).ready
    && !(latestJob && isSystemUpdateJobActive(latestJob.status));
}
function orderBatchTargets(targets: SystemUpdateTarget[]) { return [...targets].sort((a, b) => Number(isControlPanelUpdateTarget(a)) - Number(isControlPanelUpdateTarget(b))); }
function targetDisplayName(job: SystemUpdateJob, targets: SystemUpdateTarget[]) { return targets.find((target) => target.target_id === job.target_id)?.name || job.target_id; }
function systemUpdateJobMessage(job: SystemUpdateJob) {
  const fallback = systemUpdateJobStatusLabel(job.status);
  const summary = job.code ? systemUpdateErrorMessage({ code: job.code }, fallback) : fallback;
  const detail = String(job.message || "").replace(/[\u0000-\u001f\u007f]+/g, " ").replace(/\s+/g, " ").trim().slice(0, 500);
  const result = job.operation === "port_reconfigure" ? systemUpdatePortReconfigureResultLabel(job.port_reconfigure?.result) : "";
  const lines = [result ? `${summary} · ${result}` : summary];
  if (job.code) lines.push(`code: ${job.code}`);
  if (detail && detail !== summary && detail !== fallback && detail !== job.code) lines.push(detail);
  return lines.join("\n");
}
function systemUpdateJobDisplayStatus(job: SystemUpdateJob) {
  const status = job.status === "queued" && job.strategy === "when_idle" ? "配信終了待ち" : systemUpdateJobStatusLabel(job.status);
  const result = job.operation === "port_reconfigure" ? systemUpdatePortReconfigureResultLabel(job.port_reconfigure?.result) : "";
  return result ? `${status} · ${result}` : status;
}
function systemUpdateJobOperationLabel(job: SystemUpdateJob) { return job.operation === "port_reconfigure" ? "ポート変更" : "ソフトウェア更新"; }
function systemUpdateJobChangeSummary(job: SystemUpdateJob) {
  if (job.operation === "port_reconfigure") {
    const docker = job.port_reconfigure?.docker;
    if (docker) {
      return `広告 ${job.port_reconfigure?.old_port ?? "-"} → ${job.port_reconfigure?.new_port ?? "-"} / published ${docker.old_published_port} → ${docker.new_published_port} / container ${docker.old_container_port} → ${docker.new_container_port}`;
    }
    return `port ${job.port_reconfigure?.old_port ?? "-"} → ${job.port_reconfigure?.new_port ?? "-"}`;
  }
  return `${job.current_version || "-"} → ${job.target_version || "-"}`;
}
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
