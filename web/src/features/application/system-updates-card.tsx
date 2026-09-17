"use client";
import { fixedPresentationText } from "@/lib/i18n/ui-v2/presentation-copy";

import { useUICopy } from "@/lib/i18n/ui-v2/use-ui-copy";


import { type ReactNode } from "react";
import { Download, History, RefreshCcw } from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { useI18n } from "@/components/admin/i18n-provider";
import { DetailSection } from "@/components/layout/detail-section";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { UpdaterSettingsPanel } from "@/features/application/updater-settings-panel";
import { isSystemUpdateJobActive, isSystemUpdateJobCancellable, systemUpdateStrategyForTarget, systemUpdateSoftwareOperationEligibility } from "@/lib/system-update-target-policy";
import { systemUpdateDeploymentLabel, systemUpdateErrorMessage, systemUpdateConnectivity, systemUpdateHostReachabilityLabel, systemUpdateHostReachabilityMessage, systemUpdateJobStatusLabel, systemUpdateJobTone, systemUpdatePolicyErrorMessage, systemUpdateProgress, systemUpdateTargetBlockedReason, systemUpdateUpdaterPolicyState } from "@/lib/system-update-presentation";
import type { SystemUpdateAgentStatus, SystemUpdateHostStatus, SystemUpdateJob, SystemUpdateTarget } from "@/types/domain";
import { systemUpdateJobMessage, targetDisplayName, systemUpdateJobOperationLabel, systemUpdateJobChangeSummary, systemUpdateJobDisplayStatus } from "./system-update-job-presentation";
import { formatOptionalDate, serviceTypeLabel, UpdateStatusBadge, systemUpdateTargetState } from "./service-update-presentation";
import { InfoItem } from "./service-endpoint-summary";

export function SystemUpdatesCard({
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
  batchProgress,
  timezone,
  onRefresh,
  batchAction,
  renderTargetAction,
  renderCancelAction,
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
  batchProgress: { completed: number; total: number } | null;
  timezone?: string;
  onRefresh: () => void;
  batchAction: ReactNode;
  renderTargetAction: (target: SystemUpdateTarget, disabled: boolean) => ReactNode;
  renderCancelAction: (job: SystemUpdateJob) => ReactNode;
}) {
  const uiText = useUICopy();
  const { locale } = useI18n();
  return (
    <Card id="system-update-targets" tabIndex={-1} className="min-w-0 scroll-mt-24 shadow-none">
      <CardHeader className="min-w-0 gap-3 sm:flex-row sm:items-start sm:justify-between">
        <div className="min-w-0">
          <CardTitle className="flex items-center gap-2"><Download className="size-5" />{uiText("システム更新")}</CardTitle>
          <CardDescription className="mt-1">{uiText("Control Panelで更新ジョブを作成し、各ホストのHost Agentがoutbound通信で受け取って安全に適用します。")}</CardDescription>
        </div>
        <div className="flex min-w-0 flex-wrap gap-2">
          <Button variant="outline" size="sm" onClick={onRefresh} disabled={!canRead || isLoading}><RefreshCcw className="size-4" />{uiText("再取得")}</Button>
          {batchAction}
          {batchProgress ? <span className="sr-only" role="status" aria-live="polite">{batchProgress.completed}/{batchProgress.total}{uiText("件の更新ジョブを受付済みです。")}</span> : null}
        </div>
      </CardHeader>
      <CardContent className="space-y-5">
        <div className="rounded-md border border-blue-200 bg-blue-50/70 p-3 text-xs leading-5 text-blue-950 dark:border-blue-900 dark:bg-blue-950/30 dark:text-blue-100">
          {uiText("Docker配備では、Docker Bundleのバージョンと各サービスのバージョンは別に管理されます。表示が異なっていても異常ではなく、対象ホストのHost AgentがBundle設定を照合して更新します。 pull_v2ではHost AgentからControl Panelへ接続するため、Control Panelから各ホストへのSSH接続やUpdater用TCP受信ポートは使いません。")}</div>

        {canRead && isError && (targets.length > 0 || updaters.length > 0) ? <p role="alert">{locale === "ja" ? "最新状態を取得できません。前回の情報を表示しています。" : "Current state is unavailable. Showing previous information."}</p> : null}
        {canRead && (!isLoading || updaters.length > 0) ? (
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
          <div className="rounded-md border border-dashed p-6 text-sm text-muted-foreground">{uiText("更新対象と履歴を確認するには「system_updates.read」権限が必要です。")}</div>
        ) : isError && targets.length === 0 ? (
          <div className="flex flex-wrap items-center justify-between gap-3 rounded-md border border-amber-300 bg-amber-50 p-4 text-sm text-amber-950 dark:border-amber-900 dark:bg-amber-950/35 dark:text-amber-100">
            <span>{fixedPresentationText(systemUpdateErrorMessage(error, uiText("更新対象を取得できませんでした。Control Panelと各Host AgentのHeartbeatを確認してください。")), uiText)}</span>
            <Button variant="outline" size="sm" onClick={onRefresh}>{uiText("再試行")}</Button>
          </div>
        ) : isLoading ? (
          <div className="rounded-md border border-dashed p-6 text-sm text-muted-foreground">{uiText("更新対象を読み込み中です。")}</div>
        ) : targets.length === 0 ? (
          <div className="rounded-md border border-dashed p-6 text-sm text-muted-foreground">{uiText("更新対象が未設定です。各ホストのHost Agentと対象サービスを登録してください。pull_v2ではSSH設定やUpdater用TCP受信ポートは不要です。")}</div>
        ) : (
          <div className="grid gap-6 xl:grid-cols-2 min-[1800px]:grid-cols-3">
            {targets.map((target) => (
              <SystemUpdateTargetPanel
                key={target.target_id}
                target={target}
                updaters={updaters}
                hosts={hosts}
                timezone={timezone}
                activeJob={jobsByTarget.get(target.target_id)}
                canExecute={canExecute}
                action={renderTargetAction(target, isCreating)}
              />
            ))}
          </div>
        )}

        <DetailSection id="system-update-history" title={<span className="flex items-center gap-2"><History aria-hidden="true" className="size-4" />{locale === "ja" ? "更新履歴・実行中ジョブ" : "History and active jobs"}</span>}>
          {canRead && jobs.length > 0 ? (
            <div className="overflow-x-auto rounded-md border">
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead>{uiText("対象 / 操作")}</TableHead><TableHead>{uiText("バージョン / ポート")}</TableHead><TableHead>{uiText("状態")}</TableHead><TableHead>{uiText("進捗")}</TableHead><TableHead>{uiText("メッセージ")}</TableHead><TableHead>{uiText("依頼者 / 日時")}</TableHead><TableHead className="text-right">{uiText("操作")}</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {jobs.map((job) => {
                    const progress = systemUpdateProgress(job);
                    const jobMessage = systemUpdateJobMessage(job, uiText);
                    const [jobMessageSummary, ...jobMessageDetails] = jobMessage.split("\n");
                    return (
                      <TableRow key={job.id} data-system-update-job-id={job.id}>
                        <TableCell><div className="font-medium">{targetDisplayName(job, targets)}</div><div className="text-xs text-muted-foreground">{systemUpdateJobOperationLabel(job, uiText)} · {fixedPresentationText(systemUpdateDeploymentLabel(job.deployment_mode), uiText)}</div></TableCell>
                        <TableCell className="whitespace-nowrap text-xs">{systemUpdateJobChangeSummary(job, uiText)}</TableCell>
                        <TableCell><Badge variant={systemUpdateJobTone(job.status)}>{systemUpdateJobDisplayStatus(job, uiText)}</Badge></TableCell>
                        <TableCell className="min-w-32"><div className="h-2 overflow-hidden rounded-full bg-muted" role="progressbar" aria-label={uiText("{0} の更新進捗", targetDisplayName(job, targets))} aria-valuemin={0} aria-valuemax={100} aria-valuenow={progress}><div className="h-full rounded-full bg-primary transition-[width]" style={{ width: `${progress}%` }} /></div><div className="mt-1 text-right text-xs text-muted-foreground">{progress}%</div></TableCell>
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
                          {isSystemUpdateJobCancellable(job.status) ? renderCancelAction(job) : <span className="text-xs text-muted-foreground">-</span>}
                        </TableCell>
                      </TableRow>
                    );
                  })}
                </TableBody>
              </Table>
            </div>
          ) : canRead ? <div className="rounded-md border border-dashed p-4 text-sm text-muted-foreground">{uiText("更新履歴はまだありません。")}</div> : null}
        </DetailSection>
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
  const uiText = useUICopy();
  return (
    <div className="rounded-lg border bg-muted/15 p-4">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <div>
          <div className="text-sm font-medium">Host Agent</div>
          <div className="mt-0.5 text-xs text-muted-foreground">{uiText("pull_v2 Host Agentは各ホストからControl Panelへoutbound接続して更新ジョブを受け取ります。")}</div>
        </div>
        {updaters.length === 0 ? <Badge variant="secondary">{uiText("未登録")}</Badge> : null}
      </div>
      {updaters.length === 0 ? (
        <p className="mt-3 text-xs text-amber-700 dark:text-amber-300">{uiText("Host Agentが登録されていません。更新ジョブは開始できません。")}</p>
      ) : (
        <div className="mt-3 grid gap-2 lg:grid-cols-2 [&>*:only-child]:col-span-full">
          {updaters.map((updater) => {
            const policy = systemUpdateUpdaterPolicyState(updater);
            return (
              <div key={updater.updater_id} className="flex flex-wrap items-center justify-between gap-3 rounded-md border bg-background/70 p-3 text-xs">
                <div className="min-w-0">
                  <div className="truncate font-medium">{updater.name || updater.updater_id}</div>
                  <div className="mt-0.5 text-muted-foreground">{updater.updater_id}{updater.version ? ` · ${updater.version}` : ""}</div>
                  <div className="mt-0.5 text-muted-foreground">{uiText("最終Heartbeat:")}{formatOptionalDate(updater.last_heartbeat_at, timezone)}</div>
                  {updater.policy_error_code || updater.policy_error ? <div className="mt-1 break-words text-destructive">{uiText("反映情報:")}{fixedPresentationText(systemUpdatePolicyErrorMessage(updater.policy_error_code || updater.policy_error), uiText)}</div> : null}
                </div>
                <div className="flex min-w-0 max-w-full flex-wrap items-center gap-2">
                  <Badge variant={policy.tone} className="max-w-full whitespace-normal [overflow-wrap:anywhere]">{updater.status && !["online", "offline"].includes(updater.status) ? uiText("不明") : fixedPresentationText(policy.label, uiText)}</Badge>
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

function SystemUpdateTargetPanel({ target, updaters, hosts, timezone, activeJob, canExecute, action }: { target: SystemUpdateTarget; updaters: SystemUpdateAgentStatus[]; hosts: SystemUpdateHostStatus[]; timezone?: string; activeJob?: SystemUpdateJob; canExecute: boolean; action: ReactNode }) {
  const uiText = useUICopy();
  const strategy = systemUpdateStrategyForTarget(target);
  const connectivity = systemUpdateConnectivity(target, updaters, hosts);
  const updaterPolicy = connectivity.updater ? systemUpdateUpdaterPolicyState(connectivity.updater) : null;
  const operationEligibility = systemUpdateSoftwareOperationEligibility(target);
  const hostName = connectivity.host?.name || target.host_id || uiText("ホスト未設定");
  const reachabilityLabel = fixedPresentationText(systemUpdateHostReachabilityLabel(connectivity.reachability), uiText);
  const reachabilityMessage = fixedPresentationText(systemUpdateHostReachabilityMessage(connectivity.host?.reachability_code), uiText);
  const blockedReason = !operationEligibility.ready ? operationEligibility.reason : target.blocked_reason;
  const blocked = blockedReason
    ? fixedPresentationText(systemUpdateTargetBlockedReason(blockedReason), uiText)
    : !connectivity.updater
      ? fixedPresentationText(systemUpdateTargetBlockedReason("updater_not_configured"), uiText)
      : !connectivity.agentOnline
        ? fixedPresentationText(systemUpdateTargetBlockedReason("updater_offline"), uiText)
        : !updaterPolicy?.ready
          ? updaterPolicy?.label === "反映失敗"
            ? uiText("更新エージェントの設定反映に失敗しています。設定画面でエラーを確認してください。")
            : updaterPolicy?.label === "未設定"
              ? uiText("更新エージェントの設定が未設定です。設定を保存してください。")
              : uiText("更新エージェントが新しい設定を反映中です。反映済みになるまでお待ちください。")
        : connectivity.reachability === "unreachable"
          ? fixedPresentationText(systemUpdateTargetBlockedReason("target_unreachable"), uiText)
          : connectivity.reachability === "unknown"
            ? fixedPresentationText(systemUpdateTargetBlockedReason("target_reachability_unknown"), uiText)
            : !target.update_available
              ? uiText("現在は更新不要です。")
              : !target.eligible
                ? uiText("更新条件を満たしていません。")
                : "";
  return (
    <div className="rounded-lg border bg-muted/15 p-4">
      <div className="flex items-start justify-between gap-3">
        <div className="min-w-0"><div className="truncate font-medium">{target.name || target.target_id}</div><div className="mt-0.5 text-xs text-muted-foreground">{serviceTypeLabel(target.target_type)} · {fixedPresentationText(systemUpdateDeploymentLabel(target.deployment_mode), uiText)}</div></div>
        <UpdateStatusBadge state={systemUpdateTargetState(target, uiText)} />
      </div>
      <div className="mt-3 grid grid-cols-2 gap-2 text-sm">
        <InfoItem label={uiText("現在")} value={target.current_version || uiText("未報告")} />
        <InfoItem label={uiText("更新先")} value={target.latest_version || uiText("未確認")} />
      </div>
      <div className="mt-3 space-y-1 text-xs text-muted-foreground">
        <div className="flex items-center justify-between gap-2"><span>{uiText("対象ホスト")}</span><span className="truncate font-medium text-foreground" title={hostName}>{hostName}</span></div>
        <div className="flex items-center justify-between gap-2"><span>{uiText("接続状態")}</span><span className={connectivity.reachability === "reachable" ? "text-emerald-700 dark:text-emerald-300" : connectivity.reachability === "unreachable" ? "text-red-700 dark:text-red-300" : "text-amber-700 dark:text-amber-300"}>{reachabilityLabel}</span></div>
        <div className="flex items-center justify-between gap-2"><span>{uiText("最終接続確認")}</span><span>{formatOptionalDate(connectivity.host?.reachability_checked_at, timezone)}</span></div>
        <div className="flex items-center justify-between gap-2"><span>{uiText("実行方法")}</span><span>{strategy === "when_idle" ? uiText("空き次第（配信 {0} の終了後）", target.current_stream_id || "実行中") : uiText("メンテナンス更新")}</span></div>
      </div>
      {activeJob && isSystemUpdateJobActive(activeJob.status) ? <div className="mt-3 rounded-md bg-muted p-2 text-xs">{fixedPresentationText(systemUpdateJobStatusLabel(activeJob.status), uiText)} · {systemUpdateProgress(activeJob)}%</div> : null}
      {target.update_check_error ? <p className="mt-3 break-words text-xs text-amber-700 dark:text-amber-300">{uiText("確認エラー:")}{target.update_check_error}</p> : null}
      {reachabilityMessage ? <p className="mt-3 text-xs text-red-700 dark:text-red-300">{uiText("接続エラー:")}{reachabilityMessage}</p> : null}
      {blocked ? <p className={target.update_check_error ? "mt-1 text-xs text-amber-700 dark:text-amber-300" : "mt-3 text-xs text-amber-700 dark:text-amber-300"}>{blocked}</p> : null}
      {!canExecute ? <p className="mt-1 text-xs text-muted-foreground">{uiText("更新の実行には system_updates.execute 権限が必要です。")}</p> : null}
      {action}
    </div>
  );
}
