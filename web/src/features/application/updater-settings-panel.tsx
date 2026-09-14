"use client";
import { fixedPresentationText } from "@/lib/i18n/ui-v2/presentation-copy";

import { useUICopy } from "@/lib/i18n/ui-v2/use-ui-copy";


import { DraftExitContext, useDraftExit } from "@/components/forms/draft-exit";
import { useMemo, useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { KeyRound, LoaderCircle, Settings2 } from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle, DialogTrigger } from "@/components/ui/dialog";
import { UpdaterActionConfirmation } from "@/features/application/updater-action-confirmation";
import { createUpdaterActionController, updaterAuthorityFingerprint, type UpdaterActionAuthority, type UpdaterActionIntent } from "@/features/application/updater-action-policy";
import { useCurrentUser, useUpdaterSettings } from "@/features/queries";
import { apiGet } from "@/lib/api/client";
import { hasPermission } from "@/lib/auth/permissions";
import { pullUpdaterOwnershipActivationEligibility, pullUpdaterOwnershipActivationRequest, pullUpdaterOwnershipDeactivationEligibility, pullUpdaterOwnershipDeactivationRequest, pullOwnershipMutationFenceAdvanced } from "@/lib/updater-ownership";
import { normalizeSystemUpdatesResponse } from "@/lib/system-updates";
import { systemUpdateErrorMessage, systemUpdatePolicyErrorMessage, systemUpdateUpdaterPolicyState } from "@/lib/system-update-presentation";
import type { PullUpdaterOwnershipActivationRequest, SystemUpdateAgentStatus, SystemUpdateJob, SystemUpdateTarget } from "@/types/domain";
import { type PullOwnershipDeactivationAttempt } from "./updater-settings-form-model";
import { ownershipActionAuthoritySnapshot, unavailableUpdaterAuthority, ownershipEligibilityMessage } from "./updater-settings-authority";
import { OwnershipStateItem, UpdaterSettingsForm } from "./updater-settings-form";
import { usePullOwnershipMutations } from "./use-pull-ownership-mutations";

type UpdaterSettingsPanelProps = {
  updater: SystemUpdateAgentStatus;
  availableTargets: SystemUpdateTarget[];
  jobs: SystemUpdateJob[];
  canEdit: boolean;
  canManageSecrets: boolean;
};

export function UpdaterSettingsPanel({ updater, availableTargets, jobs, canEdit, canManageSecrets }: UpdaterSettingsPanelProps) {
  const uiText = useUICopy();
  const [open, setOpen] = useState(false);
  const [bootstrapCloseBlocked, setBootstrapCloseBlocked] = useState(false);
  const [ambiguousOwnershipRequest, setAmbiguousOwnershipRequest] = useState<PullUpdaterOwnershipActivationRequest | null>(null);
  const [ambiguousDeactivationAttempt, setAmbiguousDeactivationAttempt] = useState<PullOwnershipDeactivationAttempt | null>(null);
  const [ownershipFeedback, setOwnershipFeedback] = useState<{ tone: "success" | "error"; message: string } | null>(null);
  const queryClient = useQueryClient();
  const currentUser = useCurrentUser();
  const updaterActionController = useMemo(() => createUpdaterActionController(), []);
  const settings = useUpdaterSettings(updater.updater_id, open);
  const settingsData = useMemo(() => {
    if (!settings.data || updater.transport_mode !== "pull_v2") {
      return settings.data;
    }
    return {
      ...settings.data,
      transport_mode: "pull_v2" as const,
      execution_host_id: updater.execution_host_id || "",
    };
  }, [settings.data, updater.execution_host_id, updater.transport_mode]);
  const policyState = systemUpdateUpdaterPolicyState(updater);
  const observedOwnership = settingsData?.execution_host_ownership;
  const ownershipTransitionObserved = Boolean(
    ambiguousOwnershipRequest
    && updater.transport_mode === "pull_v2"
    && settingsData?.execution_host_id === ambiguousOwnershipRequest.expected_execution_host_id
    && observedOwnership?.transport_mode === "pull_v2"
    && observedOwnership.agent_service_id === updater.updater_id
    && observedOwnership.ownership_epoch > ambiguousOwnershipRequest.expected_ownership_epoch
    && observedOwnership.policy_revision === ambiguousOwnershipRequest.expected_source_policy_revision
    && Number(updater.ownership_epoch) === observedOwnership.ownership_epoch,
  );
  const deactivationTransitionObserved = Boolean(
    ambiguousDeactivationAttempt
    && settingsData?.execution_host_id === ambiguousDeactivationAttempt.request.expected_execution_host_id
    && observedOwnership?.transport_mode === "pull_v2"
    && observedOwnership.agent_service_id === updater.updater_id
    && observedOwnership.ownership_epoch > ambiguousDeactivationAttempt.request.expected_ownership_epoch
    && Number(updater.ownership_epoch) === 0,
  );
  const ownershipAttemptFenceAdvanced = Boolean(
    ambiguousOwnershipRequest
    && settingsData
    && pullOwnershipMutationFenceAdvanced(ambiguousOwnershipRequest, settingsData),
  );
  const deactivationAttemptFenceAdvanced = Boolean(
    ambiguousDeactivationAttempt
    && settingsData
    && pullOwnershipMutationFenceAdvanced(
      ambiguousDeactivationAttempt.request,
      settingsData,
    ),
  );
  const resolvedOwnershipTransitionObserved = ownershipTransitionObserved || deactivationTransitionObserved;
  const { activateOwnership, deactivateOwnership } = usePullOwnershipMutations({
    updaterID: updater.updater_id, queryClient,
    setAmbiguousOwnershipRequest, setAmbiguousDeactivationAttempt, setOwnershipFeedback,
  });
  const ownershipRequestState = activateOwnership.isPending
    ? "pending"
    : ambiguousOwnershipRequest && !ownershipAttemptFenceAdvanced
      ? "ambiguous"
      : "idle";
  const ownershipEligibility = settingsData
    ? pullUpdaterOwnershipActivationEligibility({
        updater,
        settings: settingsData,
        jobs,
        requestState: ownershipRequestState,
      })
    : { ready: false, reason: "pull_ownership_contract_unavailable" };
  const deactivationRequestState = deactivateOwnership.isPending
    ? "pending"
    : ambiguousDeactivationAttempt && !deactivationAttemptFenceAdvanced
      ? "ambiguous"
      : "idle";
  const deactivationEligibility = settingsData
    ? pullUpdaterOwnershipDeactivationEligibility({
        updater,
        settings: settingsData,
        jobs,
        requestState: deactivationRequestState,
      })
    : { ready: false, reason: "pull_rollback_contract_unavailable" };
  const activePullOwner = Boolean(
    settingsData
    && updater.transport_mode === "pull_v2"
    && settingsData.transport_mode === "pull_v2"
    && updater.execution_host_id === settingsData.execution_host_id
    && observedOwnership?.transport_mode === "pull_v2"
    && observedOwnership.agent_service_id === updater.updater_id
    && Number.isSafeInteger(observedOwnership.ownership_epoch)
    && observedOwnership.ownership_epoch > 0
    && Number(updater.ownership_epoch) === observedOwnership.ownership_epoch,
  );
  const ownershipMutationPending = activateOwnership.isPending || deactivateOwnership.isPending;
  const draftExit = useDraftExit({ enabled: open && canEdit, pending: bootstrapCloseBlocked || ownershipMutationPending });
  const setDialogOpen = (nextOpen: boolean) => {
    if (!nextOpen && (bootstrapCloseBlocked || ownershipMutationPending)) return;
    if (nextOpen) setOpen(true); else draftExit.request(() => setOpen(false));
  };
  const requestOwnershipActivation = async () => {
    if (!settingsData || !ownershipEligibility.ready || !canEdit) return;
    setOwnershipFeedback(null);
    return activateOwnership.mutateAsync(pullUpdaterOwnershipActivationRequest(updater, settingsData));
  };
  const requestOwnershipDeactivation = async () => {
    if (!settingsData || !deactivationEligibility.ready || !activePullOwner || !canEdit) return;
    setOwnershipFeedback(null);
    return deactivateOwnership.mutateAsync({
      request: pullUpdaterOwnershipDeactivationRequest(updater, settingsData),
    });
  };
  const updaterLabel = updater.name || updater.updater_id;
  const foundationFreshness: UpdaterActionAuthority["freshness"] = settings.isError || currentUser.isError
    ? "unavailable"
    : settings.isFetching || currentUser.isFetching
      ? "refreshing"
      : settingsData && currentUser.data
        ? "fresh"
        : "stale";
  const foundationPermission: UpdaterActionAuthority["permission"] = currentUser.data
    ? (hasPermission(currentUser.data, "system_updates.execute") ? "allowed" : "denied")
    : "unknown";
  const activationSnapshot = ownershipActionAuthoritySnapshot(
    "UPD-06",
    updater,
    settingsData,
    jobs,
    Boolean(settingsData && ownershipEligibility.ready && !activePullOwner),
  );
  const deactivationSnapshot = ownershipActionAuthoritySnapshot(
    "UPD-07",
    updater,
    settingsData,
    jobs,
    Boolean(settingsData && deactivationEligibility.ready && activePullOwner),
  );
  const activationIntent: UpdaterActionIntent = Object.freeze({
    id: "UPD-06",
    resourceId: updater.updater_id,
    publicLabel: updaterLabel,
    authorityFingerprint: activationSnapshot.fingerprint,
  });
  const deactivationIntent: UpdaterActionIntent = Object.freeze({
    id: "UPD-07",
    resourceId: updater.updater_id,
    authorityFingerprint: deactivationSnapshot.fingerprint,
  });
  const currentFoundationAuthority = (snapshot: ReturnType<typeof ownershipActionAuthoritySnapshot>): UpdaterActionAuthority => Object.freeze({
    permission: foundationPermission,
    freshness: foundationFreshness,
    applicability: snapshot.applicable ? "applicable" : "not-applicable",
    authorityFingerprint: snapshot.fingerprint,
  });
  const refreshOwnershipAuthority = async (actionID: "UPD-06" | "UPD-07"): Promise<UpdaterActionAuthority> => {
    const [systemUpdatesRaw, refreshedSettings, refreshedUser] = await Promise.all([
      apiGet<unknown>("/system-updates"),
      settings.refetch(),
      currentUser.refetch(),
    ]);
    if (refreshedSettings.isError || !refreshedSettings.data || refreshedUser.isError || !refreshedUser.data) {
      return unavailableUpdaterAuthority(updaterAuthorityFingerprint([actionID, updater.updater_id, "unavailable"]));
    }
    const systemUpdates = normalizeSystemUpdatesResponse(systemUpdatesRaw);
    queryClient.setQueryData(["system-updates"], systemUpdates);
    const refreshedUpdater = systemUpdates.updaters.find((candidate) => candidate.updater_id === updater.updater_id);
    if (!refreshedUpdater) {
      return unavailableUpdaterAuthority(updaterAuthorityFingerprint([actionID, updater.updater_id, "missing"]));
    }
    const refreshedSettingsData = refreshedUpdater.transport_mode === "pull_v2"
      ? { ...refreshedSettings.data, transport_mode: "pull_v2" as const, execution_host_id: refreshedUpdater.execution_host_id || "" }
      : refreshedSettings.data;
    const activation = actionID === "UPD-06";
    const eligibility = activation
      ? pullUpdaterOwnershipActivationEligibility({ updater: refreshedUpdater, settings: refreshedSettingsData, jobs: systemUpdates.jobs, requestState: "idle" })
      : pullUpdaterOwnershipDeactivationEligibility({ updater: refreshedUpdater, settings: refreshedSettingsData, jobs: systemUpdates.jobs, requestState: "idle" });
    const snapshot = ownershipActionAuthoritySnapshot(actionID, refreshedUpdater, refreshedSettingsData, systemUpdates.jobs, eligibility.ready);
    return Object.freeze({
      permission: hasPermission(refreshedUser.data, "system_updates.execute") ? "allowed" : "denied",
      freshness: "fresh",
      applicability: snapshot.applicable ? "applicable" : "not-applicable",
      authorityFingerprint: snapshot.fingerprint,
    });
  };

  return (
    <DraftExitContext.Provider value={draftExit}><Dialog open={open} onOpenChange={setDialogOpen}>
      <DialogTrigger asChild>
        <Button variant="outline" size="sm" aria-label={uiText("{0} の設定", updater.name || updater.updater_id)}>
          <Settings2 className="size-4" />
          {uiText("設定")}</Button>
      </DialogTrigger>
      <DialogContent
        className="max-h-[92vh] overflow-y-auto sm:max-w-5xl"
        showCloseButton={!bootstrapCloseBlocked && !ownershipMutationPending}
        onEscapeKeyDown={(event) => {
          if (bootstrapCloseBlocked || ownershipMutationPending) event.preventDefault();
        }}
        onPointerDownOutside={(event) => {
          if (bootstrapCloseBlocked || ownershipMutationPending) event.preventDefault();
        }}
      >
        <DialogHeader>
          <DialogTitle>{updater.name || updater.updater_id} {uiText("の設定")}</DialogTitle>
          <DialogDescription>
            {uiText("このホストで管理するサービスをControl Panel上で設定します。Host Agentが外向き接続で設定を取得します。")}</DialogDescription>
        </DialogHeader>

        <div className="flex flex-wrap items-center gap-2 rounded-md border bg-muted/30 p-3 text-xs">
          <Badge variant={policyState.tone}>{policyState.label}</Badge>
          <span>{uiText("設定Revision:")}{updater.desired_revision ?? settingsData?.revision ?? 0}</span>
          <span>{uiText("反映済みRevision:")}{updater.applied_revision ?? 0}</span>
          {updater.policy_error_code || updater.policy_error ? (
            <span className="break-words text-destructive">{uiText("反映情報:")}{fixedPresentationText(systemUpdatePolicyErrorMessage(updater.policy_error_code || updater.policy_error), uiText)}</span>
          ) : null}
        </div>

        {settingsData?.transport_mode === "pull_v2" ? (
          <section className="space-y-3 rounded-md border border-blue-200 bg-blue-50/40 p-4 dark:border-blue-900 dark:bg-blue-950/20" aria-labelledby={`${updater.updater_id}-ownership-heading`}>
            <div className="flex flex-wrap items-start justify-between gap-3">
              <div>
                <h3 id={`${updater.updater_id}-ownership-heading`} className="font-medium">{uiText("更新実行権限の切替")}</h3>
                <p className="mt-1 text-xs text-muted-foreground">{uiText("Host Agentの更新実行権限をCASで切り替えます。observer状態では更新を実行しません。")}</p>
              </div>
              <Badge variant={Number(updater.ownership_epoch) > 0 ? "default" : "secondary"}>
                {Number(updater.ownership_epoch) > 0 ? `Host Agent active · epoch ${updater.ownership_epoch}` : "Observer only"}
              </Badge>
            </div>
            <div className="grid gap-2 text-xs sm:grid-cols-2 lg:grid-cols-4">
              <OwnershipStateItem label={uiText("実行ホスト")} value={settingsData.execution_host_id || uiText("未報告")} />
              <OwnershipStateItem
                label={uiText("現在のOwner")}
                value={settingsData.execution_host_ownership
                  ? `${settingsData.execution_host_ownership.transport_mode} / ${settingsData.execution_host_ownership.agent_service_id || uiText("未割当")}`
                  : uiText("未報告")}
              />
              <OwnershipStateItem label={uiText("現在のOwnership epoch")} value={settingsData.execution_host_ownership?.ownership_epoch ?? uiText("未報告")} />
              <OwnershipStateItem
                label={activePullOwner ? uiText("実行権限解除") : "Observer readiness"}
                value={activePullOwner
                  ? (deactivationEligibility.ready ? uiText("解除可能") : ownershipEligibilityMessage(deactivationEligibility.reason))
                  : (ownershipEligibility.ready ? uiText("切替可能") : ownershipEligibilityMessage(ownershipEligibility.reason))}
              />
            </div>
            <div className="text-xs text-muted-foreground">
              Source / projection / executor revision: {settingsData.revision} / {settingsData.projection_revision ?? uiText("未報告")} / {settingsData.local_executor_policy_revision ?? uiText("未報告")}
            </div>
            <div className="text-xs text-muted-foreground">
              Observer: {settingsData.pull_activation?.status || uiText("未報告")} · heartbeat {settingsData.pull_activation?.last_heartbeat_at || uiText("未報告")} ·
              observe-only {settingsData.pull_activation?.observe_only === true ? "yes" : "no"} · executor {settingsData.pull_activation?.update_executor === true ? "ready" : "not ready"}
            </div>
            {ownershipFeedback || resolvedOwnershipTransitionObserved ? (
              <div
                className={(ownershipFeedback?.tone === "error" && !resolvedOwnershipTransitionObserved)
                  ? "rounded-md border border-destructive/40 bg-destructive/5 p-3 text-sm text-destructive"
                  : "rounded-md border border-emerald-300 bg-emerald-50 p-3 text-sm text-emerald-950 dark:border-emerald-900 dark:bg-emerald-950/35 dark:text-emerald-100"}
                role={ownershipFeedback?.tone === "error" && !resolvedOwnershipTransitionObserved ? "alert" : "status"}
              >
                {deactivationTransitionObserved
                  ? uiText("実行権限解除を最新状態で確認しました。Host Agentはobserver epoch 0です。")
                  : ownershipTransitionObserved
                    ? uiText("切替結果を最新状態で確認しました。Host Agentが更新実行権限を所有しています。")
                    : ownershipFeedback?.message ? fixedPresentationText(ownershipFeedback.message, uiText) : null}
              </div>
            ) : null}
            <div className="flex flex-wrap gap-2">
              {!activePullOwner ? (
                <UpdaterActionConfirmation
                  controller={updaterActionController}
                  intent={activationIntent}
                  authority={currentFoundationAuthority(activationSnapshot)}
                  refreshAuthority={() => refreshOwnershipAuthority("UPD-06")}
                  handler={requestOwnershipActivation}
                  label={uiText("Host Agentへ切り替え")}
                  icon={activateOwnership.isPending ? <LoaderCircle className="size-4 animate-spin" /> : <KeyRound className="size-4" />}
                  aria-busy={activateOwnership.isPending}
                  variant="outline"
                  disabled={!canEdit || !ownershipEligibility.ready || ownershipMutationPending}
                  title={!canEdit ? uiText("system_updates.execute 権限が必要です。") : ownershipEligibilityMessage(ownershipEligibility.reason) || undefined}
                />
              ) : null}
              {activePullOwner ? (
                <UpdaterActionConfirmation
                  controller={updaterActionController}
                  intent={deactivationIntent}
                  authority={currentFoundationAuthority(deactivationSnapshot)}
                  refreshAuthority={() => refreshOwnershipAuthority("UPD-07")}
                  handler={requestOwnershipDeactivation}
                  label={uiText("実行権限を解除")}
                  icon={deactivateOwnership.isPending ? <LoaderCircle className="size-4 animate-spin" /> : <KeyRound className="size-4" />}
                  aria-busy={deactivateOwnership.isPending}
                  variant="destructive"
                  disabled={!canEdit || !deactivationEligibility.ready || ownershipMutationPending}
                  title={!canEdit ? uiText("system_updates.execute 権限が必要です。") : ownershipEligibilityMessage(deactivationEligibility.reason) || undefined}
                />
              ) : null}
              {ownershipRequestState === "ambiguous" || deactivationRequestState === "ambiguous" ? (
                <Button
                  type="button"
                  variant="outline"
                  onClick={() => {
                    void settings.refetch();
                    void queryClient.invalidateQueries({ queryKey: ["system-updates"] });
                  }}
                >
                  {uiText("状態だけ再取得")}</Button>
              ) : null}
            </div>
            {!canEdit ? <p className="text-xs text-muted-foreground">{uiText("切替には system_updates.execute 権限が必要です。")}</p> : null}
          </section>
        ) : null}

        {settings.isLoading ? (
          <div className="flex items-center gap-2 rounded-md border border-dashed p-6 text-sm text-muted-foreground" role="status">
            <LoaderCircle className="size-4 animate-spin" />
            {uiText("Updater設定を読み込んでいます。")}</div>
        ) : settings.isError ? (
          <div className="rounded-md border border-destructive/40 bg-destructive/5 p-4 text-sm text-destructive">
            {fixedPresentationText(systemUpdateErrorMessage(settings.error, uiText("Updater設定を取得できませんでした。")), uiText)}
          </div>
        ) : settingsData ? (
          <UpdaterSettingsForm
            key={`${settingsData.updater_id}:${settingsData.transport_mode}`}
            updater={updater}
            availableTargets={availableTargets}
            settings={settingsData}
            canEdit={canEdit}
            canManageSecrets={canManageSecrets}
            updaterActionController={updaterActionController}
            ownershipOperationBlocked={ownershipMutationPending || ownershipRequestState === "ambiguous" || deactivationRequestState === "ambiguous"}
            onBootstrapCloseBlockedChange={setBootstrapCloseBlocked}
          />
        ) : null}
      </DialogContent>
    </Dialog></DraftExitContext.Provider>
  );
}
