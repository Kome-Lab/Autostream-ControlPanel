"use client";
import { fixedPresentationText } from "@/lib/i18n/ui-v2/presentation-copy";

import { useUICopy } from "@/lib/i18n/ui-v2/use-ui-copy";


import { useEffect, useMemo, useRef, useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { Activity, Download, LoaderCircle, RefreshCcw } from "lucide-react";
import { Button } from "@/components/ui/button";
import { PageHeader } from "@/components/shell/page-header";
import { DetailSection, SectionNavigation } from "@/components/layout/detail-section";
import { useI18n } from "@/components/admin/i18n-provider";
import { useAppSettings, useCurrentUser, useNodes, useServiceHealth, useSystemUpdates, useVersion } from "@/features/queries";
import { UpdaterActionConfirmation } from "@/features/application/updater-action-confirmation";
import { createUpdaterActionController, type UpdaterActionAuthority, type UpdaterActionIntent } from "@/features/application/updater-action-policy";
import { apiPost } from "@/lib/api/client";
import { hasPermission } from "@/lib/auth/permissions";
import { acquireSystemUpdateTargetRequestLock, isControlPanelUpdateTarget, isSystemUpdateJobActive, systemUpdateMayDisconnectPanel, systemUpdateStrategyForTarget } from "@/lib/system-update-target-policy";
import { isSystemUpdateEndpointRevisionConflict, requestSystemUpdatePortReconfigureWithRecovery, systemUpdatePortRequestMatchesJob } from "@/lib/system-update-port-requests";
import { requestSystemUpdateWithRecovery, runSystemUpdatesSequentially, SystemUpdateRequestAmbiguousError } from "@/lib/system-update-requests";
import { systemUpdateErrorMessage } from "@/lib/system-update-presentation";
import { systemUpdateJobFromResponse } from "@/lib/system-updates";
import type { SystemUpdateJob, SystemUpdatePortReconfigureCreateRequest, SystemUpdateTarget, SystemUpdatesResponse } from "@/types/domain";
import { type Feedback, type SystemUpdateOperation, type PortReconfigureOperation, type PortReconfigureAuthorityContext } from "./application-operation-types";
import { mergeRegisteredNodeRows, compareServiceRows } from "./registered-services-model";
import { compareUpdateJobs, latestJobsByTarget, orderBatchTargets, updateCanStart } from "./system-update-selection";
import { selfUpdateTerminalFeedback, systemUpdateSucceeded } from "./system-update-job-presentation";
import { mergeSystemUpdateJob, newIdempotencyKey } from "./system-update-cache";
import { batchUpdateAuthoritySnapshot } from "./port-reconfigure-authority";
import { SystemUpdatesCard } from "./system-updates-card";
import { InfoItem } from "./service-endpoint-summary";
import { shortCommit, formatOptionalDate, UpdateStatusBadge, controlPanelUpdateState } from "./service-update-presentation";
import { RegisteredServicesCard } from "./registered-services-card";
import { createApplicationAuthorityReaders, refreshApplicationPortAuthority } from "./application-authority-readers";
import { createApplicationActionRenderers } from "./application-action-renderers";

export function ApplicationInfoView() {
  const uiText = useUICopy();
  const { locale } = useI18n();
  const ja = locale === "ja";
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
  const terminalSelfUpdateFeedback = selfUpdateTerminalFeedback(selfUpdateJob, uiText);
  const recoveredPortFeedback: Feedback | null = recoveredAmbiguousPortJob
    ? { tone: "success", message: uiText("{0}: 応答を確認できなかったポート変更ジョブを履歴から確認しました。", recoveredAmbiguousPortJob.target_id) }
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
      setFeedback({ tone: "success", message: uiText("更新ジョブをキャンセルしました。") });
      await queryClient.invalidateQueries({ queryKey: ["system-updates"] });
    },
    onError: (error) => setFeedback({ tone: "error", message: fixedPresentationText(systemUpdateErrorMessage(error, uiText("更新ジョブをキャンセルできませんでした。")), uiText) }),
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
      const suffix = systemUpdateStrategyForTarget(target) === "when_idle" ? uiText("配信終了後に更新を開始します。") : uiText("更新ジョブを受け付けました。");
      setFeedback({ tone: "success", message: `${target.name || target.target_id}: ${suffix}` });
      return job;
    } catch (error) {
      setFeedback({ tone: "error", message: fixedPresentationText(systemUpdateErrorMessage(error), uiText) });
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
      setFeedback({ tone: "success", message: uiText("{0}件の更新ジョブを順番に受け付けました。", batchTargets.length) });
      return accepted;
    } catch (error) {
      const targetName = currentTarget?.name || currentTarget?.target_id || uiText("不明な対象");
      setFeedback({ tone: "error", message: uiText("{0}/{1}件を受付済みです。{2} の受付で停止しました。{3}", completed, batchTargets.length, targetName, fixedPresentationText(systemUpdateErrorMessage(error), uiText)) });
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
      setFeedback({ tone: "success", message: uiText("{0}: ポート変更ジョブを受け付けました。現在適用中のendpointが更新されるまでお待ちください。", request.target_id) });
    } catch (error) {
      if (error instanceof SystemUpdateRequestAmbiguousError) {
        setAmbiguousPortRequest(error.request);
        setFeedback({ tone: "error", message: uiText("ポート変更要求の結果を確認できません。安全のため同じ対象への再送を停止し、更新履歴を自動確認します。") });
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
            ? uiText("Endpoint revisionが変わったため送信しませんでした。最新のNode状態を再取得しました。内容を確認してからやり直してください。")
            : uiText("Endpoint revisionが変わったため送信しませんでした。Node状態を再取得できなかったため、手動で再取得してからやり直してください。"),
        });
        throw error;
      }
      setFeedback({ tone: "error", message: fixedPresentationText(systemUpdateErrorMessage(error, uiText("ポート変更ジョブを開始できませんでした。")), uiText) });
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
  const { refreshTargetAuthority, refreshBatchAuthority, refreshCancelAuthority } = createApplicationAuthorityReaders({ systemUpdates, currentUser });
  const refreshPortAuthority = (context: PortReconfigureAuthorityContext) => refreshApplicationPortAuthority(context, {
    systemUpdates, currentUser, registeredNodes, serviceHealth, canReadRegisteredNodes, canReadServiceHealth,
    hasActivePortRequestTarget: (targetID) => activePortRequestTargets.current.has(targetID), unresolvedAmbiguousPortRequest,
  });
  const batchAuthoritySnapshot = batchUpdateAuthoritySnapshot(availableTargets, systemUpdates.data);
  const batchIntent: UpdaterActionIntent = Object.freeze({
    id: "UPD-03",
    resourceId: "fleet",
    authorityFingerprint: batchAuthoritySnapshot.fingerprint,
  });
  const { renderTargetAction, renderCancelAction } = createApplicationActionRenderers({
    updaterActionController, currentUpdaterAuthority, updates: systemUpdates.data,
    refreshTargetAuthority, executeTarget, creating: createUpdate.isPending, canExecuteSystemUpdates,
    refreshCancelAuthority, executeCancel, cancelling: cancelUpdate.isPending, cancellingJobID: cancelUpdate.variables?.id,
  }, uiText);

  const refreshInformation = () => {
    void appVersion.refetch();
    if (canReadSystemUpdates) void systemUpdates.refetch();
  };

  return (
    <div className="min-w-0 space-y-5" data-screen-family="application">
      <PageHeader title={ja ? "アプリケーション情報" : "Application"}
        description={ja ? "release bundle、各component、Host Agent、更新ジョブ、endpointの希望・適用・報告を分けて確認します。" : "Review release bundles, components, Host Agents, jobs, and desired, applied and reported endpoints separately."}
        actions={<div className="flex flex-wrap gap-2">
          <Button variant="outline" size="sm" onClick={refreshInformation} disabled={appVersion.isFetching || systemUpdates.isFetching}>
            <RefreshCcw className="size-4" />
            {uiText("更新情報を再確認")}</Button>
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
            {uiText("情報を再取得")}</Button>
        </div>} />
      <SectionNavigation label={ja ? "アプリケーションのセクション" : "Application sections"} items={[
        { id: "application-overview", label: "Overview" },
        { id: "system-update-targets", label: ja ? "更新対象・Host Agent" : "Targets / Host Agents" },
        { id: "system-update-history", label: ja ? "ジョブ履歴" : "Job history" },
        { id: "registered-services", label: ja ? "サービス・ポート" : "Services / ports" },
      ]} />

      {reconnecting ? (
        <div className="flex items-start gap-3 rounded-lg border border-blue-300 bg-blue-50 p-4 text-sm text-blue-950 dark:border-blue-900 dark:bg-blue-950/35 dark:text-blue-100" role="status">
          <LoaderCircle className="mt-0.5 size-4 shrink-0 animate-spin" />
          <div>
            <div className="font-medium">{uiText("Control Panelを更新しています。再接続中です。")}</div>
            <div className="mt-1 text-xs opacity-80">{uiText("再起動中は一時的にAPIへ接続できません。この画面は自動的に再確認します。")}</div>
          </div>
        </div>
      ) : null}

      {visibleFeedback ? (
        <div className={visibleFeedback.tone === "error" ? "rounded-md border border-red-300 bg-red-50 p-3 text-sm text-red-950 dark:border-red-900 dark:bg-red-950/35 dark:text-red-100" : "rounded-md border border-emerald-300 bg-emerald-50 p-3 text-sm text-emerald-950 dark:border-emerald-900 dark:bg-emerald-950/35 dark:text-emerald-100"} role={visibleFeedback.tone === "error" ? "alert" : "status"}>
          {visibleFeedback.message}
        </div>
      ) : null}

      {canReadSystemUpdates && systemUpdates.isFetching && systemUpdates.data !== undefined ? <p role="status" data-remote-freshness="refreshing">{uiText("取得済みデータを表示しながら更新中です。")}</p> : null}
      {canViewNodeInfo && nodesFetching && (registeredNodes.data !== undefined || serviceHealth.data !== undefined) ? <p role="status" data-remote-freshness="refreshing">{uiText("サービス情報を更新中です。")}</p> : null}
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
            label={batchProgress ? uiText("{0}/{1} 受付中", batchProgress.completed, batchProgress.total) : uiText("更新可能なものを順次受付（ホストごと並行）{0}", availableTargets.length ? ` (${availableTargets.length})` : "")}
            icon={createUpdate.isPending || batchProgress ? <LoaderCircle className="size-4 animate-spin" /> : <Download className="size-4" />}
            className="h-auto max-w-full whitespace-normal text-left sm:h-8 sm:whitespace-nowrap"
            disabled={createUpdate.isPending || Boolean(batchProgress) || availableTargets.length === 0}
          />
        )}
        renderTargetAction={renderTargetAction}
        renderCancelAction={renderCancelAction}
      />

      <div className="grid min-w-0 gap-4">
        <DetailSection id="application-overview" title={<span className="flex items-center gap-2"><Activity aria-hidden="true" className="size-5" />Control Panel</span>} description={ja ? "管理画面とAPIサーバーのビルド情報です。" : "Build information for the panel and API server."}>
            <div className="grid gap-3 sm:grid-cols-2">
              <InfoItem label={uiText("バージョン")} value={appVersion.data?.version || "dev"} />
              <InfoItem label={uiText("コミット")} value={shortCommit(appVersion.data?.commit)} monospace />
              <InfoItem label={uiText("ビルド日時")} value={formatOptionalDate(appVersion.data?.build_date, timezone)} />
              <InfoItem label={uiText("更新確認")} value={<UpdateStatusBadge state={controlPanelUpdateState(appVersion.data, uiText)} />} />
            </div>
            {appVersion.data?.update_check_error ? <p className="text-sm text-amber-700">{uiText("更新確認エラー:")}{appVersion.data.update_check_error}</p> : null}
        </DetailSection>

        <section id="registered-services" tabIndex={-1} className="min-w-0 scroll-mt-24" aria-label={ja ? "サービスとendpoint" : "Services and endpoints"}>
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
        </section>
      </div>

    </div>
  );
}
