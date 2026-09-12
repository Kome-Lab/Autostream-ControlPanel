"use client";

import { useEffect, useMemo, useRef, useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { Activity, Download, LoaderCircle, RefreshCcw, XCircle } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { useAppSettings, useCurrentUser, useNodes, useServiceHealth, useSystemUpdates, useVersion } from "@/features/queries";
import { UpdaterActionConfirmation } from "@/features/application/updater-action-confirmation";
import { createUpdaterActionController, updaterAuthorityFingerprint, type UpdaterActionAuthority, type UpdaterActionIntent } from "@/features/application/updater-action-policy";
import { apiPost } from "@/lib/api/client";
import { hasPermission } from "@/lib/auth/permissions";
import { acquireSystemUpdateTargetRequestLock, isControlPanelUpdateTarget, isSystemUpdateJobActive, systemUpdateMayDisconnectPanel, systemUpdateStrategyForTarget } from "@/lib/system-update-target-policy";
import { isSystemUpdateEndpointRevisionConflict, requestSystemUpdatePortReconfigureWithRecovery, systemUpdatePortRequestMatchesJob } from "@/lib/system-update-port-requests";
import { requestSystemUpdateWithRecovery, runSystemUpdatesSequentially, SystemUpdateRequestAmbiguousError } from "@/lib/system-update-requests";
import { systemUpdateErrorMessage } from "@/lib/system-update-presentation";
import { systemUpdateJobFromResponse } from "@/lib/system-updates";
import type { SystemUpdateRequestState } from "@/lib/system-update-target-policy";
import type { SystemUpdateJob, SystemUpdatePortReconfigureCreateRequest, SystemUpdateTarget, SystemUpdatesResponse } from "@/types/domain";
import { type Feedback, type SystemUpdateOperation, type PortReconfigureOperation, type PortReconfigureAuthorityContext } from "./application-operation-types";
import { mergeRegisteredNodeRows, compareServiceRows, nodeIdentity } from "./registered-services-model";
import { compareUpdateJobs, latestJobsByTarget, orderBatchTargets, updateCanStart, availableSystemUpdateTargets } from "./system-update-selection";
import { selfUpdateTerminalFeedback, systemUpdateSucceeded } from "./system-update-job-presentation";
import { mergeSystemUpdateJob, newIdempotencyKey } from "./system-update-cache";
import { unavailableUpdaterAuthority, softwareUpdateAuthoritySnapshot, freshUpdaterAuthority, batchUpdateAuthoritySnapshot, cancelUpdateAuthoritySnapshot, portReconfigureAuthoritySnapshot } from "./port-reconfigure-authority";
import { SystemUpdatesCard } from "./system-updates-card";
import { InfoItem } from "./service-endpoint-summary";
import { shortCommit, formatOptionalDate, UpdateStatusBadge, controlPanelUpdateState } from "./service-update-presentation";
import { RegisteredServicesCard } from "./registered-services-card";

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
  const updaterActionController = useMemo(() => createUpdaterActionController(), []);
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
      const job = await createUpdate.mutateAsync({ target, idempotencyKey: newIdempotencyKey(target.target_id) });
      const suffix = systemUpdateStrategyForTarget(target) === "when_idle" ? "配信終了後に更新を開始します。" : "更新ジョブを受け付けました。";
      setFeedback({ tone: "success", message: `${target.name || target.target_id}: ${suffix}` });
      return job;
    } catch (error) {
      setFeedback({ tone: "error", message: systemUpdateErrorMessage(error) });
      throw error;
    }
  };

  const executeBatch = async (batchTargets: SystemUpdateTarget[]) => {
    clearTerminalSelfUpdate();
    setFeedback(null);
    setBatchProgress({ completed: 0, total: batchTargets.length });
    let completed = 0;
    let currentTarget: SystemUpdateTarget | undefined;
    try {
      const accepted = await runSystemUpdatesSequentially(batchTargets, async (target, index) => {
        currentTarget = target;
        const job = await createUpdate.mutateAsync({ target, idempotencyKey: newIdempotencyKey(target.target_id) });
        completed = index + 1;
        setBatchProgress({ completed, total: batchTargets.length });
        return job;
      });
      setFeedback({ tone: "success", message: `${batchTargets.length}件の更新ジョブを順番に受け付けました。` });
      return accepted;
    } catch (error) {
      const targetName = currentTarget?.name || currentTarget?.target_id || "不明な対象";
      setFeedback({ tone: "error", message: `${completed}/${batchTargets.length}件を受付済みです。${targetName} の受付で停止しました。${systemUpdateErrorMessage(error)}` });
      if (completed > 0) throw new Error("system_update_batch_partially_accepted", { cause: error });
      throw error;
    } finally {
      setBatchProgress(null);
    }
  };

  const executeCancel = async (job: SystemUpdateJob) => {
    clearTerminalSelfUpdate();
    setFeedback(null);
    return cancelUpdate.mutateAsync(job);
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
        throw error;
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
        throw error;
      }
      setFeedback({ tone: "error", message: systemUpdateErrorMessage(error, "ポート変更ジョブを開始できませんでした。") });
      throw error;
    } finally {
      activePortRequestTargets.current.delete(targetID);
      setPortRequestTargetID((current) => current === targetID ? "" : current);
    }
  };

  const updaterAuthorityFreshness: UpdaterActionAuthority["freshness"] = currentUser.isError || systemUpdates.isError
    ? "unavailable"
    : currentUser.isFetching || systemUpdates.isFetching
      ? "refreshing"
      : currentUser.data && systemUpdates.data
        ? "fresh"
        : "stale";
  const updaterAuthorityPermission: UpdaterActionAuthority["permission"] = currentUser.data
    ? (canExecuteSystemUpdates ? "allowed" : "denied")
    : "unknown";
  const currentUpdaterAuthority = (applicable: boolean, authorityFingerprint: string): UpdaterActionAuthority => Object.freeze({
    permission: updaterAuthorityPermission,
    freshness: updaterAuthorityFreshness,
    applicability: applicable ? "applicable" : "not-applicable",
    authorityFingerprint,
  });
  const refreshTargetAuthority = async (actionID: "UPD-01" | "UPD-02", targetID: string): Promise<UpdaterActionAuthority> => {
    const [refreshedUpdates, refreshedUser] = await Promise.all([systemUpdates.refetch(), currentUser.refetch()]);
    if (refreshedUpdates.isError || !refreshedUpdates.data || refreshedUser.isError || !refreshedUser.data) {
      return unavailableUpdaterAuthority(updaterAuthorityFingerprint([actionID, targetID, "unavailable"]));
    }
    const target = refreshedUpdates.data.targets.find((candidate) => candidate.target_id === targetID);
    const snapshot = target
      ? softwareUpdateAuthoritySnapshot(actionID, target, refreshedUpdates.data)
      : { applicable: false, fingerprint: updaterAuthorityFingerprint([actionID, targetID, "missing"]) };
    return freshUpdaterAuthority(hasPermission(refreshedUser.data, "system_updates.execute"), snapshot.applicable, snapshot.fingerprint);
  };
  const refreshBatchAuthority = async (): Promise<UpdaterActionAuthority> => {
    const [refreshedUpdates, refreshedUser] = await Promise.all([systemUpdates.refetch(), currentUser.refetch()]);
    if (refreshedUpdates.isError || !refreshedUpdates.data || refreshedUser.isError || !refreshedUser.data) {
      return unavailableUpdaterAuthority(updaterAuthorityFingerprint(["UPD-03", "fleet", "unavailable"]));
    }
    const refreshedTargets = availableSystemUpdateTargets(refreshedUpdates.data);
    const snapshot = batchUpdateAuthoritySnapshot(refreshedTargets, refreshedUpdates.data);
    return freshUpdaterAuthority(hasPermission(refreshedUser.data, "system_updates.execute"), snapshot.applicable, snapshot.fingerprint);
  };
  const refreshCancelAuthority = async (jobID: string): Promise<UpdaterActionAuthority> => {
    const [refreshedUpdates, refreshedUser] = await Promise.all([systemUpdates.refetch(), currentUser.refetch()]);
    if (refreshedUpdates.isError || !refreshedUpdates.data || refreshedUser.isError || !refreshedUser.data) {
      return unavailableUpdaterAuthority(updaterAuthorityFingerprint(["UPD-04", jobID, "unavailable"]));
    }
    const job = refreshedUpdates.data.jobs.find((candidate) => candidate.id === jobID);
    const snapshot = cancelUpdateAuthoritySnapshot(jobID, job);
    return freshUpdaterAuthority(hasPermission(refreshedUser.data, "system_updates.execute"), snapshot.applicable, snapshot.fingerprint);
  };
  const refreshPortAuthority = async (context: PortReconfigureAuthorityContext): Promise<UpdaterActionAuthority> => {
    const [refreshedUpdates, refreshedUser, refreshedRegisteredNodes, refreshedServiceHealth] = await Promise.all([
      systemUpdates.refetch(),
      currentUser.refetch(),
      canReadRegisteredNodes ? registeredNodes.refetch() : Promise.resolve(undefined),
      canReadServiceHealth ? serviceHealth.refetch() : Promise.resolve(undefined),
    ]);
    if (
      refreshedUpdates.isError
      || !refreshedUpdates.data
      || refreshedUser.isError
      || !refreshedUser.data
      || refreshedRegisteredNodes?.isError
      || refreshedServiceHealth?.isError
    ) {
      return unavailableUpdaterAuthority(updaterAuthorityFingerprint(["UPD-05", context.targetID, "unavailable"]));
    }
    const refreshedNodeRows = mergeRegisteredNodeRows(
      refreshedRegisteredNodes?.data || [],
      refreshedServiceHealth?.data || [],
    );
    const target = refreshedUpdates.data.targets.find((candidate) => candidate.target_id === context.targetID);
    const updater = target?.updater_id
      ? refreshedUpdates.data.updaters.find((candidate) => candidate.updater_id === target.updater_id)
      : undefined;
    const node = refreshedNodeRows.find((candidate) => nodeIdentity(candidate) === context.targetID);
    const latestJob = latestJobsByTarget([...refreshedUpdates.data.jobs].sort(compareUpdateJobs)).get(context.targetID);
    const requestState: SystemUpdateRequestState = activePortRequestTargets.current.has(context.targetID)
      ? "pending"
      : unresolvedAmbiguousPortRequest?.target_id === context.targetID
        ? "ambiguous"
        : "idle";
    const snapshot = portReconfigureAuthoritySnapshot({
      target,
      updater,
      node,
      latestJob,
      requestState,
      proposal: context.proposal,
    });
    return freshUpdaterAuthority(hasPermission(refreshedUser.data, "system_updates.execute"), snapshot.applicable, snapshot.fingerprint);
  };
  const batchAuthoritySnapshot = batchUpdateAuthoritySnapshot(availableTargets, systemUpdates.data);
  const batchIntent: UpdaterActionIntent = Object.freeze({
    id: "UPD-03",
    resourceId: "fleet",
    authorityFingerprint: batchAuthoritySnapshot.fingerprint,
  });
  const renderTargetAction = (target: SystemUpdateTarget, disabled: boolean) => {
    const actionID = isControlPanelUpdateTarget(target) ? "UPD-02" as const : "UPD-01" as const;
    const snapshot = softwareUpdateAuthoritySnapshot(actionID, target, systemUpdates.data);
    const intent: UpdaterActionIntent = Object.freeze({
      id: actionID,
      resourceId: target.target_id,
      ...(actionID === "UPD-02" ? { publicLabel: target.name || target.target_id } : {}),
      authorityFingerprint: snapshot.fingerprint,
    });
    const strategy = systemUpdateStrategyForTarget(target);
    return (
      <UpdaterActionConfirmation
        controller={updaterActionController}
        intent={intent}
        authority={currentUpdaterAuthority(snapshot.applicable, snapshot.fingerprint)}
        refreshAuthority={() => refreshTargetAuthority(actionID, target.target_id)}
        handler={() => executeTarget(target)}
        label={strategy === "when_idle" ? "空き次第更新" : "更新"}
        icon={createUpdate.isPending ? <LoaderCircle className="size-4 animate-spin" /> : <Download className="size-4" />}
        className="mt-3 w-full"
        disabled={disabled}
        title={!canExecuteSystemUpdates ? "system_updates.execute 権限が必要です。" : undefined}
      />
    );
  };
  const renderCancelAction = (job: SystemUpdateJob) => {
    const snapshot = cancelUpdateAuthoritySnapshot(job.id, job);
    const intent: UpdaterActionIntent = Object.freeze({
      id: "UPD-04",
      resourceId: job.id,
      authorityFingerprint: snapshot.fingerprint,
    });
    return (
      <UpdaterActionConfirmation
        controller={updaterActionController}
        intent={intent}
        authority={currentUpdaterAuthority(snapshot.applicable, snapshot.fingerprint)}
        refreshAuthority={() => refreshCancelAuthority(job.id)}
        handler={() => executeCancel(job)}
        label="キャンセル"
        icon={cancelUpdate.isPending && cancelUpdate.variables?.id === job.id ? <LoaderCircle className="size-4 animate-spin" /> : <XCircle className="size-4" />}
        variant="outline"
        disabled={cancelUpdate.isPending && cancelUpdate.variables?.id === job.id}
        title={!canExecuteSystemUpdates ? "system_updates.execute 権限が必要です。" : undefined}
      />
    );
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
        batchProgress={batchProgress}
        timezone={timezone}
        onRefresh={() => void systemUpdates.refetch()}
        batchAction={(
          <UpdaterActionConfirmation
            controller={updaterActionController}
            intent={batchIntent}
            authority={currentUpdaterAuthority(batchAuthoritySnapshot.applicable, batchAuthoritySnapshot.fingerprint)}
            refreshAuthority={refreshBatchAuthority}
            handler={() => executeBatch(availableTargets)}
            label={batchProgress ? `${batchProgress.completed}/${batchProgress.total} 受付中` : `更新可能なものを順次受付（ホストごと並行）${availableTargets.length ? ` (${availableTargets.length})` : ""}`}
            icon={createUpdate.isPending || batchProgress ? <LoaderCircle className="size-4 animate-spin" /> : <Download className="size-4" />}
            className="h-auto max-w-full whitespace-normal text-left sm:h-8 sm:whitespace-nowrap"
            disabled={createUpdate.isPending || Boolean(batchProgress) || availableTargets.length === 0}
          />
        )}
        renderTargetAction={renderTargetAction}
        renderCancelAction={renderCancelAction}
      />

      <div className="grid min-w-0 gap-4">
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
          updaterActionController={updaterActionController}
          updaterAuthorityPermission={updaterAuthorityPermission}
          updaterAuthorityFreshness={updaterAuthorityFreshness}
          portRequestTargetID={portRequestTargetID || undefined}
          ambiguousPortTargetID={unresolvedAmbiguousPortRequest?.target_id}
          onPortReconfigure={executePortReconfigure}
          onRefreshPortAuthority={refreshPortAuthority}
          onRefresh={() => { if (canReadRegisteredNodes) void registeredNodes.refetch(); if (canReadServiceHealth) void serviceHealth.refetch(); }}
        />
      </div>

    </div>
  );
}
