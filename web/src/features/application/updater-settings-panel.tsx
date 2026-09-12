"use client";

import { useMemo, useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { KeyRound, LoaderCircle, Settings2 } from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle, DialogTrigger } from "@/components/ui/dialog";
import { UpdaterActionConfirmation } from "@/features/application/updater-action-confirmation";
import { createUpdaterActionController, updaterAuthorityFingerprint, type UpdaterActionAuthority, type UpdaterActionIntent } from "@/features/application/updater-action-policy";
import { useCurrentUser, useUpdaterSettings } from "@/features/queries";
import { apiGet, apiPost } from "@/lib/api/client";
import { hasPermission } from "@/lib/auth/permissions";
import { normalizePullUpdaterOwnershipActivationResponse, normalizePullUpdaterOwnershipDeactivationResponse, pullUpdaterOwnershipActivationEligibility, pullUpdaterOwnershipActivationRequest, pullUpdaterOwnershipDeactivationEligibility, pullUpdaterOwnershipDeactivationRequest, pullOwnershipMutationFenceAdvanced } from "@/lib/updater-ownership";
import { normalizeSystemUpdatesResponse } from "@/lib/system-updates";
import { systemUpdateErrorMessage, systemUpdatePolicyErrorMessage, systemUpdateUpdaterPolicyState } from "@/lib/system-update-presentation";
import type { PullUpdaterOwnershipActivationRequest, PullUpdaterOwnershipActivationResponse, PullUpdaterOwnershipDeactivationResponse, SystemUpdateAgentStatus, SystemUpdateJob, SystemUpdateTarget } from "@/types/domain";
import { type PullOwnershipDeactivationAttempt } from "./updater-settings-form-model";
import { pullOwnershipMutationErrorIsAmbiguous, ownershipActionAuthoritySnapshot, unavailableUpdaterAuthority, ownershipEligibilityMessage } from "./updater-settings-authority";
import { OwnershipStateItem, UpdaterSettingsForm } from "./updater-settings-form";

type UpdaterSettingsPanelProps = {
  updater: SystemUpdateAgentStatus;
  availableTargets: SystemUpdateTarget[];
  jobs: SystemUpdateJob[];
  canEdit: boolean;
  canManageSecrets: boolean;
};

export function UpdaterSettingsPanel({ updater, availableTargets, jobs, canEdit, canManageSecrets }: UpdaterSettingsPanelProps) {
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
  const activateOwnership = useMutation<
    PullUpdaterOwnershipActivationResponse,
    Error,
    PullUpdaterOwnershipActivationRequest
  >({
    mutationFn: async (request) => {
      const response = normalizePullUpdaterOwnershipActivationResponse(await apiPost<unknown>(
        `/system-updates/updaters/${encodeURIComponent(updater.updater_id)}/pull-ownership/activate`,
        request,
      ));
      if (
        response.updater_id !== updater.updater_id
        || response.execution_host_id !== request.expected_execution_host_id
        || response.agent_service_id !== updater.updater_id
        || response.ownership_epoch <= request.expected_ownership_epoch
        || response.source_policy_revision !== request.expected_source_policy_revision
        || response.projection_revision !== request.expected_projection_revision
        || response.local_executor_policy_revision !== request.expected_local_executor_policy_revision
        || response.local_executor_policy_sha256 !== request.expected_local_executor_policy_sha256
      ) {
        throw new Error("invalid_pull_ownership_activation_response");
      }
      return response;
    },
    retry: false,
    onSuccess: async (response) => {
      setAmbiguousOwnershipRequest(null);
      setAmbiguousDeactivationAttempt(null);
      setOwnershipFeedback({ tone: "success", message: `実行権限をHost Agentへ切り替えました。Ownership epoch: ${response.ownership_epoch}` });
      await Promise.all([
        queryClient.invalidateQueries({ queryKey: ["system-updates"] }),
        queryClient.invalidateQueries({ queryKey: ["system-updates", "updaters", updater.updater_id, "settings"] }),
      ]);
    },
    onError: (error, request) => {
      if (pullOwnershipMutationErrorIsAmbiguous(error)) {
        setAmbiguousOwnershipRequest(request);
        setOwnershipFeedback({ tone: "error", message: "切替結果を確認できません。安全のため再送せず、Updater状態を再取得してください。" });
        return;
      }
      setOwnershipFeedback({ tone: "error", message: systemUpdateErrorMessage(error, "実行権限を切り替えられませんでした。最新状態を再取得して確認してください。") });
    },
  });
  const deactivateOwnership = useMutation<
    PullUpdaterOwnershipDeactivationResponse,
    Error,
    PullOwnershipDeactivationAttempt
  >({
    mutationFn: async (attempt) => {
      const response = normalizePullUpdaterOwnershipDeactivationResponse(await apiPost<unknown>(
        `/system-updates/updaters/${encodeURIComponent(updater.updater_id)}/pull-ownership/deactivate`,
        attempt.request,
      ));
      if (
        response.updater_id !== updater.updater_id
        || response.execution_host_id !== attempt.request.expected_execution_host_id
        || response.agent_service_id !== updater.updater_id
        || response.ownership_epoch <= attempt.request.expected_ownership_epoch
        || response.agent_ownership_epoch !== 0
        || response.source_policy_revision !== attempt.request.expected_source_policy_revision
        || response.projection_revision !== attempt.request.expected_projection_revision
        || response.local_executor_policy_revision !== attempt.request.expected_local_executor_policy_revision
        || response.local_executor_policy_sha256 !== attempt.request.expected_local_executor_policy_sha256
      ) {
        throw new Error("invalid_pull_ownership_deactivation_response");
      }
      return response;
    },
    retry: false,
    onSuccess: async (response) => {
      setAmbiguousDeactivationAttempt(null);
      setAmbiguousOwnershipRequest(null);
      setOwnershipFeedback({
        tone: "success",
        message: `Host Agentの更新実行権限を解除しました。Ownership epoch: ${response.ownership_epoch} / Agent epoch: ${response.agent_ownership_epoch}`,
      });
      await Promise.all([
        queryClient.invalidateQueries({ queryKey: ["system-updates"] }),
        queryClient.invalidateQueries({ queryKey: ["system-updates", "updaters", updater.updater_id, "settings"] }),
      ]);
    },
    onError: (error, attempt) => {
      if (pullOwnershipMutationErrorIsAmbiguous(error)) {
        setAmbiguousDeactivationAttempt(attempt);
        setOwnershipFeedback({
          tone: "error",
          message: "実行権限解除の結果を確認できません。安全のため再送せず、Updater状態を再取得してください。",
        });
        return;
      }
      setOwnershipFeedback({
        tone: "error",
        message: systemUpdateErrorMessage(error, "実行権限を解除できませんでした。最新状態を再取得してください。"),
      });
    },
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
  const setDialogOpen = (nextOpen: boolean) => {
    if (!nextOpen && (bootstrapCloseBlocked || ownershipMutationPending)) return;
    setOpen(nextOpen);
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
    <Dialog open={open} onOpenChange={setDialogOpen}>
      <DialogTrigger asChild>
        <Button variant="outline" size="sm" aria-label={`${updater.name || updater.updater_id} の設定`}>
          <Settings2 className="size-4" />
          設定
        </Button>
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
          <DialogTitle>{updater.name || updater.updater_id} の設定</DialogTitle>
          <DialogDescription>
            このホストで管理するサービスをControl Panel上で設定します。Host Agentが外向き接続で設定を取得します。
          </DialogDescription>
        </DialogHeader>

        <div className="flex flex-wrap items-center gap-2 rounded-md border bg-muted/30 p-3 text-xs">
          <Badge variant={policyState.tone}>{policyState.label}</Badge>
          <span>設定Revision: {updater.desired_revision ?? settingsData?.revision ?? 0}</span>
          <span>反映済みRevision: {updater.applied_revision ?? 0}</span>
          {updater.policy_error_code || updater.policy_error ? (
            <span className="break-words text-destructive">反映情報: {systemUpdatePolicyErrorMessage(updater.policy_error_code || updater.policy_error)}</span>
          ) : null}
        </div>

        {settingsData?.transport_mode === "pull_v2" ? (
          <section className="space-y-3 rounded-md border border-blue-200 bg-blue-50/40 p-4 dark:border-blue-900 dark:bg-blue-950/20" aria-labelledby={`${updater.updater_id}-ownership-heading`}>
            <div className="flex flex-wrap items-start justify-between gap-3">
              <div>
                <h3 id={`${updater.updater_id}-ownership-heading`} className="font-medium">更新実行権限の切替</h3>
                <p className="mt-1 text-xs text-muted-foreground">Host Agentの更新実行権限をCASで切り替えます。observer状態では更新を実行しません。</p>
              </div>
              <Badge variant={Number(updater.ownership_epoch) > 0 ? "default" : "secondary"}>
                {Number(updater.ownership_epoch) > 0 ? `Host Agent active · epoch ${updater.ownership_epoch}` : "Observer only"}
              </Badge>
            </div>
            <div className="grid gap-2 text-xs sm:grid-cols-2 lg:grid-cols-4">
              <OwnershipStateItem label="実行ホスト" value={settingsData.execution_host_id || "未報告"} />
              <OwnershipStateItem
                label="現在のOwner"
                value={settingsData.execution_host_ownership
                  ? `${settingsData.execution_host_ownership.transport_mode} / ${settingsData.execution_host_ownership.agent_service_id || "未割当"}`
                  : "未報告"}
              />
              <OwnershipStateItem label="現在のOwnership epoch" value={settingsData.execution_host_ownership?.ownership_epoch ?? "未報告"} />
              <OwnershipStateItem
                label={activePullOwner ? "実行権限解除" : "Observer readiness"}
                value={activePullOwner
                  ? (deactivationEligibility.ready ? "解除可能" : ownershipEligibilityMessage(deactivationEligibility.reason))
                  : (ownershipEligibility.ready ? "切替可能" : ownershipEligibilityMessage(ownershipEligibility.reason))}
              />
            </div>
            <div className="text-xs text-muted-foreground">
              Source / projection / executor revision: {settingsData.revision} / {settingsData.projection_revision ?? "未報告"} / {settingsData.local_executor_policy_revision ?? "未報告"}
            </div>
            <div className="text-xs text-muted-foreground">
              Observer: {settingsData.pull_activation?.status || "未報告"} · heartbeat {settingsData.pull_activation?.last_heartbeat_at || "未報告"} ·
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
                  ? "実行権限解除を最新状態で確認しました。Host Agentはobserver epoch 0です。"
                  : ownershipTransitionObserved
                    ? "切替結果を最新状態で確認しました。Host Agentが更新実行権限を所有しています。"
                    : ownershipFeedback?.message}
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
                  label="Host Agentへ切り替え"
                  icon={activateOwnership.isPending ? <LoaderCircle className="size-4 animate-spin" /> : <KeyRound className="size-4" />}
                  aria-busy={activateOwnership.isPending}
                  variant="outline"
                  disabled={!canEdit || !ownershipEligibility.ready || ownershipMutationPending}
                  title={!canEdit ? "system_updates.execute 権限が必要です。" : ownershipEligibilityMessage(ownershipEligibility.reason) || undefined}
                />
              ) : null}
              {activePullOwner ? (
                <UpdaterActionConfirmation
                  controller={updaterActionController}
                  intent={deactivationIntent}
                  authority={currentFoundationAuthority(deactivationSnapshot)}
                  refreshAuthority={() => refreshOwnershipAuthority("UPD-07")}
                  handler={requestOwnershipDeactivation}
                  label="実行権限を解除"
                  icon={deactivateOwnership.isPending ? <LoaderCircle className="size-4 animate-spin" /> : <KeyRound className="size-4" />}
                  aria-busy={deactivateOwnership.isPending}
                  variant="destructive"
                  disabled={!canEdit || !deactivationEligibility.ready || ownershipMutationPending}
                  title={!canEdit ? "system_updates.execute 権限が必要です。" : ownershipEligibilityMessage(deactivationEligibility.reason) || undefined}
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
                  状態だけ再取得
                </Button>
              ) : null}
            </div>
            {!canEdit ? <p className="text-xs text-muted-foreground">切替には system_updates.execute 権限が必要です。</p> : null}
          </section>
        ) : null}

        {settings.isLoading ? (
          <div className="flex items-center gap-2 rounded-md border border-dashed p-6 text-sm text-muted-foreground" role="status">
            <LoaderCircle className="size-4 animate-spin" />
            Updater設定を読み込んでいます。
          </div>
        ) : settings.isError ? (
          <div className="rounded-md border border-destructive/40 bg-destructive/5 p-4 text-sm text-destructive">
            {systemUpdateErrorMessage(settings.error, "Updater設定を取得できませんでした。")}
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
    </Dialog>
  );
}
