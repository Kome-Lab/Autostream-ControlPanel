"use client";

import { type ReactNode, useId, useMemo, useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { Check, Copy, KeyRound, LoaderCircle, Plus, Settings2, Trash2 } from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { DangerConfirm } from "@/components/admin/danger-confirm";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Textarea } from "@/components/ui/textarea";
import { UpdaterHostBootstrapPanel } from "@/features/application/updater-host-bootstrap-panel";
import { UpdaterActionConfirmation } from "@/features/application/updater-action-confirmation";
import {
  createUpdaterActionController,
  updaterAuthorityFingerprint,
  type UpdaterActionAuthority,
  type UpdaterActionIntent,
} from "@/features/application/updater-action-policy";
import { useCurrentUser, useUpdaterSettings } from "@/features/queries";
import { apiGet, apiPost, apiPut } from "@/lib/api/client";
import { hasPermission } from "@/lib/auth/permissions";
import {
  applyUpdaterSettingsTargetSelection,
  applyUpdaterSettingsTargetPatch,
  firstUnusedUpdaterSettingsTarget,
  isUpdaterPolicyHostID,
  normalizeUpdaterSettingsTargetDatabaseName,
  normalizeUpdaterSettingsTargetLocalListenPort,
  normalizePullUpdaterOwnershipActivationResponse,
  normalizePullUpdaterOwnershipDeactivationResponse,
  normalizeUpdaterSettingsResponse,
  normalizeSystemUpdatesResponse,
  pullUpdaterOwnershipActivationEligibility,
  pullUpdaterOwnershipActivationRequest,
  pullUpdaterOwnershipDeactivationEligibility,
  pullUpdaterOwnershipDeactivationRequest,
  pullOwnershipMutationFenceAdvanced,
  systemUpdateErrorMessage,
  systemUpdatePolicyErrorMessage,
  systemUpdateUpdaterPolicyState,
  updaterSettingsTargetOptions,
  updaterSettingsTargetRequiresDatabase,
  updaterSettingsTargetRequiresLocalListenPort,
} from "@/lib/system-updates";
import type {
  PullUpdaterOwnershipActivationRequest,
  PullUpdaterOwnershipActivationResponse,
  PullUpdaterOwnershipDeactivationRequest,
  PullUpdaterOwnershipDeactivationResponse,
  SystemUpdateAgentStatus,
  SystemUpdateJob,
  SystemUpdateTarget,
  UpdaterSettings,
  UpdaterSettingsHost,
  UpdaterSettingsTarget,
  UpdaterSettingsUpdate,
} from "@/types/domain";

type UpdaterSettingsPanelProps = {
  updater: SystemUpdateAgentStatus;
  availableTargets: SystemUpdateTarget[];
  jobs: SystemUpdateJob[];
  canEdit: boolean;
  canManageSecrets: boolean;
};

type UpdaterSettingsFormState = {
  apiPort: string;
  pollInterval: string;
  heartbeatInterval: string;
  localExecutorPolicySHA256: string;
  hosts: UpdaterSettingsHost[];
  targets: UpdaterSettingsTarget[];
};

type PullOwnershipDeactivationAttempt = {
  request: PullUpdaterOwnershipDeactivationRequest;
  legacyAgentServiceID: string;
};

const serviceTypes = [
  { value: "control_panel", label: "Control Panel" },
  { value: "observability", label: "Observability" },
  { value: "worker", label: "Worker" },
  { value: "encoder_recorder", label: "Encoder / Recorder" },
  { value: "discord_bot", label: "Discord Bot" },
] as const;

const deploymentModes = [
  { value: "systemd", label: "systemd" },
  { value: "docker", label: "Docker" },
] as const;

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
    && observedOwnership?.transport_mode === "ssh_v1"
    && observedOwnership.agent_service_id === ambiguousDeactivationAttempt.legacyAgentServiceID
    && observedOwnership.legacy_agent_service_id === ambiguousDeactivationAttempt.legacyAgentServiceID
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
        || response.agent_service_id !== attempt.legacyAgentServiceID
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
        message: `Bridge rollbackでSSH Updaterへ戻しました。Ownership epoch: ${response.ownership_epoch} / Host Agent epoch: ${response.agent_ownership_epoch}`,
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
          message: "Bridge rollback結果を確認できません。安全のため再送せず、Updater状態を再取得してください。",
        });
        return;
      }
      setOwnershipFeedback({
        tone: "error",
        message: systemUpdateErrorMessage(error, "Bridge rollbackを実行できませんでした。最新状態とSSH更新経路を確認してください。"),
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
  const rollbackLegacyAgentServiceID = String(observedOwnership?.legacy_agent_service_id || "").trim();
  const activePullOwner = Boolean(
    settingsData
    && updater.transport_mode === "pull_v2"
    && settingsData.transport_mode === "pull_v2"
    && updater.execution_host_id === settingsData.execution_host_id
    && observedOwnership?.transport_mode === "pull_v2"
    && observedOwnership.agent_service_id === updater.updater_id
    && rollbackLegacyAgentServiceID
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
      legacyAgentServiceID: rollbackLegacyAgentServiceID,
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
            {updater.transport_mode === "pull_v2"
              ? "このホストで管理するサービスをControl Panel上で設定します。Host Agentが外向き接続で設定を取得します。"
              : "中央Updaterが管理するホストとサービスをControl Panel上で設定します。保存後はUpdaterが設定を取得し、自動で反映します。"}
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
                <p className="mt-1 text-xs text-muted-foreground">Bridge期間中はSSH UpdaterとHost Agentの実行権限をCASで切り替えます。同じホストで同時に更新を実行することはありません。</p>
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
                  ? `${settingsData.execution_host_ownership.transport_mode} / ${settingsData.execution_host_ownership.agent_service_id || "legacy"}`
                  : "未報告"}
              />
              <OwnershipStateItem label="現在のOwnership epoch" value={settingsData.execution_host_ownership?.ownership_epoch ?? "未報告"} />
              <OwnershipStateItem
                label={activePullOwner ? "Bridge rollback" : "Observer readiness"}
                value={activePullOwner
                  ? (deactivationEligibility.ready ? `SSHへ復元可能 (${rollbackLegacyAgentServiceID})` : ownershipEligibilityMessage(deactivationEligibility.reason))
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
                  ? "Bridge rollback結果を最新状態で確認しました。SSH Updaterが更新実行権限を所有し、Host Agentはobserver epoch 0です。"
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
                  label="緊急Bridge rollback"
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

function UpdaterSettingsForm({
  updater,
  availableTargets,
  settings,
  canEdit,
  canManageSecrets,
  updaterActionController,
  ownershipOperationBlocked,
  onBootstrapCloseBlockedChange,
}: {
  updater: SystemUpdateAgentStatus;
  availableTargets: SystemUpdateTarget[];
  settings: UpdaterSettings;
  canEdit: boolean;
  canManageSecrets: boolean;
  updaterActionController: ReturnType<typeof createUpdaterActionController>;
  ownershipOperationBlocked: boolean;
  onBootstrapCloseBlockedChange: (blocked: boolean) => void;
}) {
  const queryClient = useQueryClient();
  const currentUser = useCurrentUser();
  const settingsAuthorityQuery = useUpdaterSettings(updater.updater_id, true);
  const formID = useId();
  const [form, setForm] = useState<UpdaterSettingsFormState>(() => settingsToForm(settings));
  const [baseRevision, setBaseRevision] = useState(settings.revision);
  const [githubToken, setGithubToken] = useState("");
  const [deleteGitHubToken, setDeleteGitHubToken] = useState(false);
  const [feedback, setFeedback] = useState<{ tone: "success" | "error"; message: string } | null>(null);
  const [copiedHostID, setCopiedHostID] = useState("");
  const [bootstrapActive, setBootstrapActive] = useState(false);
  const pullMode = settings.transport_mode === "pull_v2";
  const executionHostID = settings.execution_host_id || "";

  const hostOptions = useMemo(() => pullMode
    ? (executionHostID ? [{ value: executionHostID, label: executionHostID }] : [])
    : form.hosts.map((host) => ({
      value: host.host_id,
      label: host.name || host.host_id || "ID未入力",
    })), [executionHostID, form.hosts, pullMode]);
  const nextTargetHostID = (hostOptions[0]?.value || "").trim();
  const canAddRegisteredTarget = Boolean(
    nextTargetHostID
    && firstUnusedUpdaterSettingsTarget(settings.transport_mode, availableTargets, form.targets, nextTargetHostID),
  );

  const saveSettings = useMutation({
    mutationFn: async () => {
      if (bootstrapActive || ownershipOperationBlocked) {
        throw Object.assign(new Error("updater_host_bootstrap_in_progress"), {
          code: "updater_host_bootstrap_in_progress",
          status: 409,
        });
      }
      const payload = buildUpdaterSettingsPayload(baseRevision, form, settings, !pullMode && canManageSecrets ? {
        githubToken,
        deleteGitHubToken,
      } : undefined);
      const response = await apiPut<unknown>(
        `/system-updates/updaters/${encodeURIComponent(updater.updater_id)}/settings`,
        payload,
      );
      return normalizeUpdaterSettingsResponse(response, updater.updater_id);
    },
    onSuccess: async (saved) => {
      setBaseRevision(saved.revision);
      setForm(settingsToForm(saved));
      setGithubToken("");
      setDeleteGitHubToken(false);
      setFeedback({ tone: "success", message: pullMode
        ? "設定を保存しました。Host Agentのconfigureを再実行すると反映されます。反映済みになるまで更新操作は安全のため停止します。"
        : "設定を保存しました。Updaterが自動で反映します。反映済みになるまで更新操作は安全のため停止します。" });
      queryClient.setQueryData(["system-updates", "updaters", updater.updater_id, "settings"], saved);
      await Promise.all([
        queryClient.invalidateQueries({ queryKey: ["system-updates"] }),
        queryClient.invalidateQueries({ queryKey: ["system-updates", "updaters", updater.updater_id, "settings"] }),
      ]);
    },
    onError: (error) => {
      setFeedback({ tone: "error", message: updaterSettingsErrorMessage(error) });
    },
    onSettled: () => {
      setGithubToken("");
    },
  });

  const updateHost = (index: number, patch: Partial<UpdaterSettingsHost>) => {
    setForm((current) => {
      const previousHostID = current.hosts[index]?.host_id;
      const nextHostID = patch.host_id;
      return {
        ...current,
        hosts: current.hosts.map((host, hostIndex) => hostIndex === index ? { ...host, ...patch } : host),
        targets: nextHostID !== undefined && previousHostID !== nextHostID
          ? current.targets.map((target) => target.host_id === previousHostID ? { ...target, host_id: nextHostID } : target)
          : current.targets,
      };
    });
  };

  const removeHost = (index: number) => {
    setForm((current) => {
      const removedHostID = current.hosts[index]?.host_id;
      return {
        ...current,
        hosts: current.hosts.filter((_, hostIndex) => hostIndex !== index),
        targets: current.targets.filter((target) => !removedHostID || target.host_id !== removedHostID),
      };
    });
  };

  const updateTarget = (index: number, patch: Partial<UpdaterSettingsTarget>) => {
    setForm((current) => ({
      ...current,
      targets: current.targets.map((target, targetIndex) => targetIndex === index
        ? applyUpdaterSettingsTargetPatch(settings.transport_mode, target, patch)
        : target),
    }));
  };

  const selectTarget = (index: number, targetID: string, serviceType: string) => {
    setForm((current) => ({
      ...current,
      targets: current.targets.map((target, targetIndex) => targetIndex === index
        ? applyUpdaterSettingsTargetSelection(settings.transport_mode, target, {
          target_id: targetID,
          target_type: serviceType,
        })
        : target),
    }));
  };

  const copyClientPublicKey = async (hostID: string, publicKey: string) => {
    if (!publicKey || typeof navigator === "undefined" || !navigator.clipboard) return;
    await navigator.clipboard.writeText(publicKey);
    setCopiedHostID(hostID);
    window.setTimeout(() => setCopiedHostID((current) => current === hostID ? "" : current), 2_000);
  };
  const formAuthorityFingerprint = updaterSettingsFormFingerprint(
    baseRevision,
    form,
    deleteGitHubToken,
    Boolean(githubToken),
  );
  const settingsActionSnapshot = updaterSettingsActionAuthoritySnapshot(
    updater,
    settingsAuthorityQuery.data,
    formAuthorityFingerprint,
    Boolean(
      settingsAuthorityQuery.data
      && settingsAuthorityQuery.data.revision === baseRevision
      && !bootstrapActive
      && !ownershipOperationBlocked
    ),
  );
  const settingsActionIntent: UpdaterActionIntent = Object.freeze({
    id: "UPD-08",
    resourceId: updater.updater_id,
    publicLabel: updater.name || updater.updater_id,
    authorityFingerprint: settingsActionSnapshot.fingerprint,
  });
  const settingsActionFreshness: UpdaterActionAuthority["freshness"] = settingsAuthorityQuery.isError || currentUser.isError
    ? "unavailable"
    : settingsAuthorityQuery.isFetching || currentUser.isFetching
      ? "refreshing"
      : settingsAuthorityQuery.data && currentUser.data
        ? "fresh"
        : "stale";
  const settingsActionAuthority: UpdaterActionAuthority = Object.freeze({
    permission: currentUser.data
      ? (hasPermission(currentUser.data, "system_updates.execute") ? "allowed" : "denied")
      : "unknown",
    freshness: settingsActionFreshness,
    applicability: settingsActionSnapshot.applicable ? "applicable" : "not-applicable",
    authorityFingerprint: settingsActionSnapshot.fingerprint,
  });
  const refreshSettingsActionAuthority = async (): Promise<UpdaterActionAuthority> => {
    const [refreshedSettings, refreshedUser] = await Promise.all([
      settingsAuthorityQuery.refetch(),
      currentUser.refetch(),
    ]);
    if (refreshedSettings.isError || !refreshedSettings.data || refreshedUser.isError || !refreshedUser.data) {
      return unavailableUpdaterAuthority(updaterAuthorityFingerprint(["UPD-08", updater.updater_id, "unavailable"]));
    }
    const snapshot = updaterSettingsActionAuthoritySnapshot(
      updater,
      refreshedSettings.data,
      formAuthorityFingerprint,
      refreshedSettings.data.revision === baseRevision && !bootstrapActive && !ownershipOperationBlocked,
    );
    return Object.freeze({
      permission: hasPermission(refreshedUser.data, "system_updates.execute") ? "allowed" : "denied",
      freshness: "fresh",
      applicability: snapshot.applicable ? "applicable" : "not-applicable",
      authorityFingerprint: snapshot.fingerprint,
    });
  };

  return (
    <>
      <div className="space-y-6">
        {!canEdit ? (
          <div className="rounded-md border border-amber-300 bg-amber-50 p-3 text-xs text-amber-950 dark:border-amber-900 dark:bg-amber-950/35 dark:text-amber-100">
            設定の変更には system_updates.execute 権限が必要です。現在は内容の確認だけできます。
          </div>
        ) : (
          <div className="rounded-md border border-blue-300 bg-blue-50 p-3 text-xs leading-5 text-blue-950 dark:border-blue-900 dark:bg-blue-950/35 dark:text-blue-100">
            {pullMode
              ? "「設定を保存」を押した後、Host Agentのconfigureを再実行すると反映されます。反映済みになるまで更新操作は安全のため停止します。"
              : "「設定を保存」を押すと、この内容をUpdaterが自動で取得して反映します。反映に失敗した場合は更新操作を安全のため停止し、旧設定への復帰または自動再試行の状態をここに表示します。"}
          </div>
        )}

        <section className="space-y-3" aria-labelledby={`${formID}-runtime-heading`}>
          <div>
            <h3 id={`${formID}-runtime-heading`} className="font-medium">{pullMode ? "Host Agentの動作" : "Updaterの動作"}</h3>
            <p className="text-xs text-muted-foreground">
              {pullMode
                ? "Host AgentからControl Panelへoutbound HTTPSで接続します。SSH、受信API、8090ポートは使用しません。"
                : "APIは中央ホスト内だけで待ち受けます。通常は初期値のままで使用できます。"}
            </p>
          </div>
          {pullMode ? (
            <div className="flex flex-wrap gap-2 rounded-md border bg-muted/30 p-3 text-xs">
              <Badge variant="secondary">pull_v2</Badge>
              <span>実行ホスト: {executionHostID || "未割り当て"}</span>
              <span>受信ポート: なし</span>
            </div>
          ) : null}
          <div className="grid gap-3 sm:grid-cols-3">
            {!pullMode ? (
              <>
                <Field label="API接続先" htmlFor={`${formID}-api-host`} hint="安全のためループバック固定">
                  <Input id={`${formID}-api-host`} value="127.0.0.1" readOnly disabled />
                </Field>
                <Field label="APIポート" htmlFor={`${formID}-api-port`} hint="初期値 8090">
                  <Input
                    id={`${formID}-api-port`}
                    type="number"
                    min={1}
                    max={65535}
                    inputMode="numeric"
                    value={form.apiPort}
                    onChange={(event) => setForm((current) => ({ ...current, apiPort: event.target.value }))}
                    disabled={!canEdit}
                  />
                </Field>
              </>
            ) : null}
            <Field label="更新確認間隔（秒）" htmlFor={`${formID}-poll-interval`}>
              <Input
                id={`${formID}-poll-interval`}
                type="number"
                min={5}
                max={3600}
                inputMode="numeric"
                value={form.pollInterval}
                onChange={(event) => setForm((current) => ({ ...current, pollInterval: event.target.value }))}
                disabled={!canEdit}
              />
            </Field>
            <Field
              label="Heartbeat間隔（秒）"
              htmlFor={`${formID}-heartbeat-interval`}
              hint="5〜60秒の範囲で設定してください。"
            >
              <Input
                id={`${formID}-heartbeat-interval`}
                type="number"
                min={5}
                max={60}
                inputMode="numeric"
                value={form.heartbeatInterval}
                onChange={(event) => setForm((current) => ({ ...current, heartbeatInterval: event.target.value }))}
                disabled={!canEdit}
              />
            </Field>
            {pullMode ? (
              <Field
                label="Local Executor policy SHA-256"
                htmlFor={`${formID}-executor-policy-sha256`}
                hint="root所有policyを固定するdigestです。未設定時はobserve結果を信頼せず不明として扱います。"
              >
                <Input
                  id={`${formID}-executor-policy-sha256`}
                  value={form.localExecutorPolicySHA256}
                  onChange={(event) => setForm((current) => ({ ...current, localExecutorPolicySHA256: event.target.value }))}
                  disabled={!canEdit}
                  placeholder="sha256:..."
                  spellCheck={false}
                  className="font-mono text-xs"
                />
              </Field>
            ) : null}
          </div>
        </section>

        {!pullMode ? <section className="space-y-3" aria-labelledby={`${formID}-hosts-heading`}>
          <div className="flex flex-wrap items-start justify-between gap-3">
            <div>
              <h3 id={`${formID}-hosts-heading`} className="font-medium">管理対象ホスト</h3>
              <p className="max-w-3xl text-xs leading-5 text-muted-foreground">
                対象サーバーで確認したssh-ed25519ホスト公開鍵の全文を、管理者など別の安全な経路から受け取って入力してください。保存後に表示されるfingerprintも対象サーバーの値と照合してください。初回接続時の自動信頼は行いません。
              </p>
            </div>
            {canEdit ? (
              <Button
                type="button"
                variant="outline"
                size="sm"
                onClick={() => setForm((current) => ({ ...current, hosts: [...current.hosts, newHost(current.hosts.length)] }))}
              >
                <Plus className="size-4" />
                ホストを追加
              </Button>
            ) : null}
          </div>

          {form.hosts.length === 0 ? (
            <div className="rounded-md border border-dashed p-5 text-sm text-muted-foreground">管理対象ホストはまだありません。「ホストを追加」から登録してください。</div>
          ) : (
            <div className="space-y-4">
              {form.hosts.map((host, index) => {
                const clientPublicKey = updater.ssh_client_public_keys?.[host.host_id] || "";
                const clientKeyFingerprint = updater.ssh_client_key_fingerprints?.[host.host_id] || "";
                return (
                  <div key={index} className="space-y-3 rounded-md border p-4">
                    <div className="flex items-center justify-between gap-3">
                      <div className="text-sm font-medium">ホスト {index + 1}</div>
                      {canEdit ? (
                        <DangerConfirm
                          title={`「${host.name || host.host_id || `ホスト ${index + 1}`}」を削除しますか？`}
                          description="このホストと紐づく更新対象もフォームから削除します。「設定を保存」で反映すると、中央UpdaterのSSH秘密鍵は廃棄され、再追加時は新しい鍵になります。更新中のジョブがないことを確認し、反映後に対象ホストのauthorized_keys・sudoers・helper設定を撤去してください。"
                          actionLabel="ホストを削除"
                          onConfirm={() => removeHost(index)}
                        >
                          <Button type="button" variant="ghost" size="sm" aria-label={`ホスト ${index + 1} を削除`}>
                            <Trash2 className="size-4" />
                            削除
                          </Button>
                        </DangerConfirm>
                      ) : null}
                    </div>
                    <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-4">
                      <Field label="ホストID" htmlFor={`${formID}-host-${index}-id`}>
                        <Input id={`${formID}-host-${index}-id`} value={host.host_id} onChange={(event) => updateHost(index, { host_id: event.target.value })} disabled={!canEdit} placeholder="host-main" />
                      </Field>
                      <Field label="表示名" htmlFor={`${formID}-host-${index}-name`}>
                        <Input id={`${formID}-host-${index}-name`} value={host.name} onChange={(event) => updateHost(index, { name: event.target.value })} disabled={!canEdit} placeholder="配信サーバー1" />
                      </Field>
                      <Field label="IPアドレス / ホスト名" htmlFor={`${formID}-host-${index}-address`}>
                        <Input id={`${formID}-host-${index}-address`} value={host.address} onChange={(event) => updateHost(index, { address: event.target.value })} disabled={!canEdit} placeholder="192.0.2.10" />
                      </Field>
                      <Field label="SSHポート" htmlFor={`${formID}-host-${index}-port`}>
                        <Input id={`${formID}-host-${index}-port`} type="number" min={1} max={65535} inputMode="numeric" value={host.port} onChange={(event) => updateHost(index, { port: Number(event.target.value) })} disabled={!canEdit} />
                      </Field>
                      <Field
                        label="SSHユーザー"
                        htmlFor={`${formID}-host-${index}-user`}
                        hint="helper自動セットアップでは autostream-update-host 固定です。一時管理者ユーザーとは別です。"
                      >
                        <Input id={`${formID}-host-${index}-user`} value={host.user} onChange={(event) => updateHost(index, { user: event.target.value })} disabled={!canEdit} placeholder="autostream-update-host" />
                      </Field>
                      <Field label="CPUアーキテクチャ" htmlFor={`${formID}-host-${index}-arch`}>
                        <Select value={host.arch || "amd64"} onValueChange={(value) => updateHost(index, { arch: value })} disabled={!canEdit}>
                          <SelectTrigger id={`${formID}-host-${index}-arch`}><SelectValue /></SelectTrigger>
                          <SelectContent>
                            <SelectItem value="amd64">amd64（x86_64）</SelectItem>
                            <SelectItem value="arm64">arm64（aarch64）</SelectItem>
                          </SelectContent>
                        </Select>
                      </Field>
                    </div>
                    <Field
                      label="確認済みSSHホスト公開鍵（OpenSSH形式・全文）"
                      htmlFor={`${formID}-host-${index}-public-key`}
                      hint={host.host_key_fingerprint || host.host_public_key_fingerprint ? `確認結果: ${host.host_key_fingerprint || host.host_public_key_fingerprint}` : "例: ssh-ed25519 AAAA... server-name"}
                    >
                      <Textarea
                        id={`${formID}-host-${index}-public-key`}
                        value={host.host_public_key}
                        onChange={(event) => updateHost(index, { host_public_key: event.target.value })}
                        disabled={!canEdit}
                        rows={3}
                        className="font-mono text-xs"
                        spellCheck={false}
                        placeholder="ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAA... server-name"
                      />
                    </Field>

                    <div className="space-y-2 rounded-md bg-muted/40 p-3">
                      <div className="flex items-center gap-2 text-sm font-medium"><KeyRound className="size-4" />対象ホストへ登録するUpdater公開鍵</div>
                      <p className="text-xs text-muted-foreground">
                        設定保存後にUpdaterが生成し、helper自動セットアップ時に対象ホストへ登録します。通常は手動で登録する必要はありません。
                      </p>
                      {clientPublicKey ? (
                        <div className="flex items-start gap-2">
                          <Textarea readOnly value={clientPublicKey} rows={2} aria-label={`${host.name || host.host_id} 用Updater SSHクライアント公開鍵`} className="font-mono text-xs" />
                          <Button type="button" variant="outline" size="icon-sm" onClick={() => void copyClientPublicKey(host.host_id, clientPublicKey)} aria-label={`${host.name || host.host_id} 用公開鍵をコピー`}>
                            {copiedHostID === host.host_id ? <Check className="size-4" /> : <Copy className="size-4" />}
                          </Button>
                        </div>
                      ) : (
                        <div className="text-xs text-amber-700 dark:text-amber-300">まだ生成されていません。設定を保存し、Updaterが反映するまでお待ちください。</div>
                      )}
                      {clientKeyFingerprint ? <div className="break-all text-xs text-muted-foreground">Fingerprint: {clientKeyFingerprint}</div> : null}
                    </div>
                  </div>
                );
              })}
            </div>
          )}

          {form.hosts.length > 0 || settings.hosts.length > 0 ? (
            <UpdaterHostBootstrapPanel
              key={canEdit ? "bootstrap-edit" : "bootstrap-readonly"}
              updater={updater}
              expectedRevision={baseRevision}
              savedHosts={settings.hosts}
              currentHosts={form.hosts}
              savedTargets={settings.targets}
              currentTargets={form.targets}
              releaseTokenConfigured={settings.github_token_configured}
              canEdit={canEdit}
              updaterActionController={updaterActionController}
              onActiveChange={setBootstrapActive}
              onCloseBlockedChange={onBootstrapCloseBlockedChange}
            />
          ) : null}
        </section> : null}

        <section className="space-y-3" aria-labelledby={`${formID}-targets-heading`}>
          <div className="flex flex-wrap items-start justify-between gap-3">
            <div>
              <h3 id={`${formID}-targets-heading`} className="font-medium">更新するサービス</h3>
              <p className="text-xs text-muted-foreground">
                {pullMode
                  ? `実行ホスト ${executionHostID || "未割り当て"} 上で管理するAutoStreamサービスを指定します。ホスト割り当てはControl Panelが管理します。`
                  : "どのホスト上の、どのAutoStreamサービスを更新するか指定します。"}
              </p>
            </div>
            {canEdit ? (
              <Button
                type="button"
                variant="outline"
                size="sm"
                disabled={!canAddRegisteredTarget}
                title={!nextTargetHostID
                  ? "先にホストを設定してください。"
                  : !canAddRegisteredTarget ? "追加できる未使用の登録サービスがありません。" : undefined}
                onClick={() => setForm((current) => {
                  const hostID = String(pullMode ? executionHostID : current.hosts[0]?.host_id || "").trim();
                  const target = firstUnusedUpdaterSettingsTarget(settings.transport_mode, availableTargets, current.targets, hostID);
                  if (!hostID || !target) return current;
                  return { ...current, targets: [...current.targets, target] };
                })}
              >
                <Plus className="size-4" />
                サービスを追加
              </Button>
            ) : null}
          </div>

          {form.targets.length === 0 ? (
            <div className="rounded-md border border-dashed p-5 text-sm text-muted-foreground">更新対象サービスはまだありません。</div>
          ) : (
            <div className="space-y-3">
              {form.targets.map((target, index) => {
                const targetOptions = updaterSettingsTargetOptions(availableTargets, form.targets, index);
                const selectedTargetID = String(target.service_id || target.target_id || "").trim();
                return (
                  <div key={index} className="grid gap-3 rounded-md border p-4 sm:grid-cols-2 lg:grid-cols-[1.2fr_1fr_1fr_1fr_auto] lg:items-end">
                    <Field label="NodeサービスID" htmlFor={`${formID}-target-${index}-id`}>
                      <Select
                        value={selectedTargetID}
                        onValueChange={(value) => {
                          const selected = targetOptions.find((option) => option.value === value);
                          if (!selected || selected.stale) return;
                          selectTarget(index, selected.value, selected.serviceType);
                        }}
                        disabled={!canEdit || !targetOptions.some((option) => !option.stale)}
                      >
                        <SelectTrigger id={`${formID}-target-${index}-id`}><SelectValue placeholder="登録サービスを選択" /></SelectTrigger>
                        <SelectContent>
                          {targetOptions.map((option) => <SelectItem key={option.value} value={option.value}>{option.label}</SelectItem>)}
                        </SelectContent>
                      </Select>
                    </Field>
                    <Field label={pullMode ? "実行ホスト（サーバー管理）" : "ホスト"} htmlFor={`${formID}-target-${index}-host`}>
                      <Select value={target.host_id || executionHostID} onValueChange={(value) => updateTarget(index, { host_id: value })} disabled={pullMode || !canEdit || hostOptions.length === 0}>
                        <SelectTrigger id={`${formID}-target-${index}-host`}><SelectValue placeholder="ホストを選択" /></SelectTrigger>
                        <SelectContent>
                          {hostOptions.map((host) => <SelectItem key={host.value} value={host.value}>{host.label}</SelectItem>)}
                        </SelectContent>
                      </Select>
                    </Field>
                    <Field label="サービス種別（自動）" htmlFor={`${formID}-target-${index}-service`}>
                      <Input
                        id={`${formID}-target-${index}-service`}
                        value={serviceTypeLabel(target.service_type)}
                        readOnly
                        aria-readonly="true"
                      />
                    </Field>
                    <Field label="配備方式" htmlFor={`${formID}-target-${index}-mode`}>
                      <Select value={target.deployment_mode || "systemd"} onValueChange={(value) => updateTarget(index, { deployment_mode: value })} disabled={!canEdit}>
                        <SelectTrigger id={`${formID}-target-${index}-mode`}><SelectValue /></SelectTrigger>
                        <SelectContent>
                          {selectOptionsWithCurrent(deploymentModes, target.deployment_mode).map((option) => <SelectItem key={option.value} value={option.value}>{option.label}</SelectItem>)}
                        </SelectContent>
                      </Select>
                    </Field>
                    {canEdit ? (
                      <Button type="button" variant="ghost" size="icon-sm" onClick={() => setForm((current) => ({ ...current, targets: current.targets.filter((_, targetIndex) => targetIndex !== index) }))} aria-label={`サービス ${index + 1} を削除`}>
                        <Trash2 className="size-4" />
                      </Button>
                    ) : null}
                    {updaterSettingsTargetRequiresDatabase(settings.transport_mode, target) ? (
                      <div className="sm:col-span-2 lg:col-span-full">
                        <Field
                          label="MariaDBデータベース名"
                          htmlFor={`${formID}-target-${index}-database-name`}
                          hint="このサービスが実際に使用しているデータベース名です。ユーザー名・パスワード・DSNは入力しません。"
                        >
                          <Input
                            id={`${formID}-target-${index}-database-name`}
                            value={target.database_name || ""}
                            onChange={(event) => updateTarget(index, { database_name: event.target.value })}
                            disabled={!canEdit}
                            maxLength={64}
                            spellCheck={false}
                            autoCapitalize="none"
                            placeholder={target.service_type === "control_panel"
                              ? "autostream_control_panel"
                              : "autostream_observability"}
                          />
                        </Field>
                      </div>
                    ) : null}
                    {updaterSettingsTargetRequiresLocalListenPort(settings.transport_mode, target) ? (
                      <div className="sm:col-span-2 lg:col-span-full">
                        <Field
                          label="ローカル待受ポート"
                          htmlFor={`${formID}-target-${index}-local-listen-port`}
                          hint="systemdサービスがこのホストの127.0.0.1で実際に待ち受けるポートです。Cloudflare Tunnelなどの公開HTTPSポート443とは分けて指定します。"
                        >
                          <Input
                            id={`${formID}-target-${index}-local-listen-port`}
                            type="number"
                            min={1024}
                            max={65535}
                            value={target.local_listen_port ?? ""}
                            onChange={(event) => updateTarget(index, {
                              local_listen_port: event.target.value === ""
                                ? undefined
                                : Number(event.target.value),
                            })}
                            disabled={!canEdit}
                            inputMode="numeric"
                            placeholder={target.service_type === "observability" ? "8082" : "8084"}
                          />
                        </Field>
                      </div>
                    ) : null}
                  </div>
                );
              })}
            </div>
          )}
        </section>

        {!pullMode ? <section className="space-y-3" aria-labelledby={`${formID}-github-heading`}>
          <div>
            <h3 id={`${formID}-github-heading`} className="font-medium">GitHub Release Token</h3>
            <p className="text-xs text-muted-foreground">Managed更新では公開中のControl Panel repositoryにもTokenを必須としています。当該repositoryの Contents (read) と Attestations (read) だけを付与してください。値は保存後に再表示されず、更新jobの実行時だけUpdaterへ渡されます。private repositoryへ変更した場合、この公開repository用の証明検証では更新できません。</p>
          </div>
          <div className="flex flex-wrap items-center gap-2 text-xs">
            <Badge variant={settings.github_token_configured ? "default" : "outline"}>{settings.github_token_configured ? "設定済み" : "未設定"}</Badge>
            {settings.github_token_fingerprint ? <span className="text-muted-foreground">Fingerprint: {settings.github_token_fingerprint}</span> : null}
          </div>
          {canManageSecrets && canEdit ? (
            <div className="grid gap-3 sm:grid-cols-2">
              <Field label="新しいGitHub Release Token" htmlFor={`${formID}-github-token`} hint="空欄のまま保存すると現在のTokenを維持">
                <Input
                  id={`${formID}-github-token`}
                  type="password"
                  autoComplete="new-password"
                  value={githubToken}
                  onChange={(event) => {
                    setGithubToken(event.target.value);
                    if (event.target.value) setDeleteGitHubToken(false);
                  }}
                  disabled={deleteGitHubToken}
                  placeholder="github_pat_..."
                />
              </Field>
              {settings.github_token_configured ? (
                <label className="flex items-center gap-2 self-end rounded-md border p-3 text-sm">
                  <input
                    type="checkbox"
                    checked={deleteGitHubToken}
                    onChange={(event) => {
                      setDeleteGitHubToken(event.target.checked);
                      if (event.target.checked) setGithubToken("");
                    }}
                  />
                  登録済みTokenを削除する
                </label>
              ) : null}
            </div>
          ) : (
            <p className="text-xs text-muted-foreground">Tokenの登録・変更には system_updates.execute と secrets.update の両方の権限が必要です。</p>
          )}
        </section> : (
          <div className="rounded-md border bg-muted/30 p-3 text-xs leading-5 text-muted-foreground">
            pull_v2では長期GitHub Release TokenをHost Agentへ配布しません。更新artifactはジョブに限定した短期資格情報またはControl Panel経由で取得します。
          </div>
        )}

        {feedback ? (
          <div
            className={feedback.tone === "success"
              ? "rounded-md border border-emerald-300 bg-emerald-50 p-3 text-sm text-emerald-950 dark:border-emerald-900 dark:bg-emerald-950/35 dark:text-emerald-100"
              : "rounded-md border border-destructive/40 bg-destructive/5 p-3 text-sm text-destructive"}
            role={feedback.tone === "error" ? "alert" : "status"}
          >
            {feedback.message}
          </div>
        ) : null}
        {bootstrapActive ? (
          <div className="rounded-md border border-amber-300 bg-amber-50 p-3 text-sm text-amber-950 dark:border-amber-900 dark:bg-amber-950/35 dark:text-amber-100" role="status">
            ホストの自動セットアップ中はUpdater設定を保存できません。完了後に再試行してください。
          </div>
        ) : null}
      </div>

      <DialogFooter className="mt-6">
        {canEdit ? (
          <UpdaterActionConfirmation
            controller={updaterActionController}
            intent={settingsActionIntent}
            authority={settingsActionAuthority}
            refreshAuthority={refreshSettingsActionAuthority}
            handler={() => saveSettings.mutateAsync()}
            label="設定を保存"
            icon={saveSettings.isPending ? <LoaderCircle className="size-4 animate-spin" /> : <Settings2 className="size-4" />}
            size="default"
            disabled={saveSettings.isPending || bootstrapActive || ownershipOperationBlocked}
            title={bootstrapActive
              ? "ホストの自動セットアップ完了後に保存できます。"
              : ownershipOperationBlocked
                ? "更新実行権限の切替状態を確認してから保存できます。"
                : undefined}
          />
        ) : null}
      </DialogFooter>
    </>
  );
}

function OwnershipStateItem({ label, value }: { label: string; value: ReactNode }) {
  return <div className="rounded-md border bg-background/70 px-3 py-2"><div className="text-muted-foreground">{label}</div><div className="mt-0.5 break-all font-medium">{value}</div></div>;
}

function ownershipActionAuthoritySnapshot(
  actionID: "UPD-06" | "UPD-07",
  updater: SystemUpdateAgentStatus,
  settings: UpdaterSettings | undefined,
  jobs: SystemUpdateJob[],
  applicable: boolean,
) {
  const ownership = settings?.execution_host_ownership;
  const activation = settings?.pull_activation;
  const relevantJobs = jobs
    .filter((job) => job.updater_id === updater.updater_id || settings?.targets.some((target) => target.target_id === job.target_id))
    .sort((left, right) => left.id.localeCompare(right.id));
  return {
    applicable,
    fingerprint: updaterAuthorityFingerprint([
      actionID,
      updater.updater_id,
      updater.transport_mode,
      updater.execution_host_id,
      updater.ownership_epoch,
      updater.online,
      updater.last_heartbeat_at,
      updater.desired_revision,
      updater.applied_revision,
      updater.policy_status,
      settings?.revision,
      settings?.projection_revision,
      settings?.local_executor_policy_revision,
      settings?.local_executor_policy_sha256,
      settings?.execution_host_id,
      ownership?.transport_mode,
      ownership?.agent_service_id,
      ownership?.legacy_agent_service_id,
      ownership?.ownership_epoch,
      ownership?.policy_revision,
      activation?.ready,
      activation?.status,
      activation?.last_heartbeat_at,
      activation?.observe_only,
      activation?.update_executor,
      activation?.mutation_enabled,
      activation?.recovery_pending,
      activation?.reported_ownership_epoch,
      activation?.reported_projection_revision,
      ...relevantJobs.flatMap((job) => [job.id, job.status, job.updated_at, job.recovery_required]),
    ]),
  };
}

function updaterSettingsFormFingerprint(
  baseRevision: number,
  form: UpdaterSettingsFormState,
  deleteGitHubToken: boolean,
  githubTokenPresent: boolean,
) {
  return updaterAuthorityFingerprint([
    "UPD-08-form",
    baseRevision,
    form.apiPort,
    form.pollInterval,
    form.heartbeatInterval,
    form.localExecutorPolicySHA256,
    deleteGitHubToken,
    githubTokenPresent,
    ...form.hosts.flatMap((host) => [
      host.host_id,
      host.name,
      host.address,
      host.port,
      host.user,
      host.arch,
      host.host_public_key,
    ]),
    ...form.targets.flatMap((target) => [
      target.target_id,
      target.service_id,
      target.host_id,
      target.service_type,
      target.deployment_mode,
      target.database_name,
      target.local_listen_port,
    ]),
  ]);
}

function updaterSettingsActionAuthoritySnapshot(
  updater: SystemUpdateAgentStatus,
  settings: UpdaterSettings | undefined,
  formFingerprint: string,
  applicable: boolean,
) {
  return {
    applicable,
    fingerprint: updaterAuthorityFingerprint([
      "UPD-08",
      updater.updater_id,
      updater.transport_mode,
      updater.execution_host_id,
      updater.ownership_epoch,
      settings?.revision,
      settings?.projection_revision,
      settings?.local_executor_policy_revision,
      settings?.local_executor_policy_sha256,
      settings?.github_token_configured,
      settings?.transport_mode,
      settings?.execution_host_id,
      formFingerprint,
    ]),
  };
}

function unavailableUpdaterAuthority(authorityFingerprint: string): UpdaterActionAuthority {
  return Object.freeze({
    permission: "unknown",
    freshness: "unavailable",
    applicability: "unknown",
    authorityFingerprint,
  });
}

function ownershipEligibilityMessage(reason: string) {
  const messages: Record<string, string> = {
    unsupported_transport: "pull_v2ではありません",
    pull_ownership_contract_unavailable: "切替用revisionまたは現在Ownerが未報告です",
    pull_rollback_contract_unavailable: "保存済みSSH ownerまたは現在のpull owner fenceが未報告です",
    request_pending: "切替要求を送信中です",
    request_ambiguous: "切替結果を確認中です",
    observer_offline: "Host Agentがオフラインです",
    recovery_required: "Host Agentがrecovery中です",
    active_job: "対象ホストで更新ジョブが進行中です",
    observer_not_ready: "ObserverまたはLocal Executorの準備が完了していません",
  };
  return messages[reason] || "";
}

function pullOwnershipMutationErrorIsAmbiguous(error: unknown) {
  if (!error || typeof error !== "object" || !("status" in error)) return true;
  const status = Number((error as { status?: unknown }).status);
  return !Number.isInteger(status) || status < 400 || status >= 500;
}

function Field({ label, htmlFor, hint, children }: { label: string; htmlFor: string; hint?: string; children: ReactNode }) {
  return (
    <div className="space-y-1.5">
      <label htmlFor={htmlFor} className="text-sm font-medium">{label}</label>
      {children}
      {hint ? <p className="text-xs text-muted-foreground">{hint}</p> : null}
    </div>
  );
}

function settingsToForm(settings: UpdaterSettings): UpdaterSettingsFormState {
  return {
    apiPort: String(settings.api.port || 8090),
    pollInterval: String(settings.poll_interval_seconds || 15),
    heartbeatInterval: String(settings.heartbeat_interval_seconds || 30),
    localExecutorPolicySHA256: settings.local_executor_policy_sha256 || "",
    hosts: settings.hosts.map((host) => ({ ...host })),
    targets: settings.targets.map((target) => ({ ...target })),
  };
}

function buildUpdaterSettingsPayload(
  expectedRevision: number,
  form: UpdaterSettingsFormState,
  settings: UpdaterSettings,
  secretUpdate?: { githubToken: string; deleteGitHubToken: boolean },
): UpdaterSettingsUpdate {
  const pollInterval = requiredInterval(form.pollInterval, "更新確認間隔");
  const heartbeatInterval = requiredHeartbeatInterval(form.heartbeatInterval);
  const pullMode = settings.transport_mode === "pull_v2";
  const executionHostID = String(settings.execution_host_id || "").trim();
  if (pullMode && !executionHostID) throw new Error("Host Agentの実行ホスト割り当てがありません。Nodeを再登録してください。");
  if (form.targets.length === 0) throw new Error("更新するサービスを1件以上追加してください。");
  if (form.targets.length > 1024) throw new Error("更新するサービスは1024件まで登録できます。");

  if (pullMode) {
    const hostIDs = new Set([executionHostID]);
    const targets = form.targets.map((target, index) => normalizeTargetForSave(
      { ...target, host_id: executionHostID },
      index,
      hostIDs,
      "pull_v2",
    ));
    if (new Set(targets.map((target) => target.target_id)).size !== targets.length) throw new Error("更新対象IDが重複しています。");
    if (new Set(targets.map((target) => target.service_id)).size !== targets.length) throw new Error("NodeサービスIDが重複しています。");
    const digest = form.localExecutorPolicySHA256.trim().toLowerCase();
    if (digest && !/^sha256:[0-9a-f]{64}$/.test(digest)) {
      throw new Error("Local Executor policy SHA-256は sha256: に続く64桁の16進数で入力してください。");
    }
    return {
      expected_revision: expectedRevision,
      poll_interval_seconds: pollInterval,
      heartbeat_interval_seconds: heartbeatInterval,
      targets,
      local_executor_policy_sha256: digest,
    };
  }

  const apiPort = requiredPort(form.apiPort, "APIポート");
  if (form.hosts.length === 0) throw new Error("管理対象ホストを1件以上追加してください。");
  if (form.hosts.length > 128) throw new Error("管理対象ホストは128件まで登録できます。");
  const hosts = form.hosts.map((host, index) => normalizeHostForSave(host, index));
  const hostIDs = new Set(hosts.map((host) => host.host_id));
  if (hostIDs.size !== hosts.length) throw new Error("ホストIDが重複しています。");
  const targets = form.targets.map((target, index) => normalizeTargetForSave(target, index, hostIDs, "ssh_v1"));
  if (new Set(targets.map((target) => target.target_id)).size !== targets.length) throw new Error("対象IDが重複しています。");
  const referencedHostIDs = new Set(targets.map((target) => target.host_id));
  const unusedHost = hosts.find((host) => !referencedHostIDs.has(host.host_id));
  if (unusedHost) throw new Error(`${unusedHost.name} に更新するサービスを1件以上追加してください。`);

  const payload: UpdaterSettingsUpdate = {
    expected_revision: expectedRevision,
    api: {
      bind_host: "127.0.0.1",
      host: "127.0.0.1",
      port: apiPort,
      ssl_enabled: false,
      tls_cert_file: "",
      tls_key_file: "",
    },
    poll_interval_seconds: pollInterval,
    heartbeat_interval_seconds: heartbeatInterval,
    hosts,
    targets,
  };
  if (secretUpdate?.deleteGitHubToken) payload.github_token = "";
  else if (secretUpdate?.githubToken.trim()) payload.github_token = secretUpdate.githubToken.trim();
  return payload;
}

function normalizeHostForSave(host: UpdaterSettingsHost, index: number): UpdaterSettingsHost {
  const prefix = `ホスト ${index + 1}`;
  const hostID = requiredText(host.host_id, `${prefix}のホストID`);
  if (!isUpdaterPolicyHostID(hostID)) throw new Error(`${prefix}のホストIDは英数字で始まり、英数字・.・_・-のみで入力してください。`);
  const address = requiredText(host.address, `${prefix}のIPアドレス / ホスト名`);
  if (address.includes("://")) throw new Error(`${prefix}の接続先にはURLではなくIPアドレスまたはホスト名を入力してください。`);
  const hostPublicKey = requiredText(host.host_public_key, `${prefix}のSSHホスト公開鍵`);
  if (!/^ssh-ed25519\s+[A-Za-z0-9+/=]+(?:\s+.*)?$/.test(hostPublicKey)) {
    throw new Error(`${prefix}のSSHホスト公開鍵はssh-ed25519のOpenSSH形式全文を入力してください。`);
  }
  const name = requiredText(host.name, `${prefix}の表示名`);
  if ([...name].length > 128) throw new Error(`${prefix}の表示名は128文字以内で入力してください。`);
  const user = requiredText(host.user, `${prefix}のSSHユーザー`);
  if (!/^[a-z_][a-z0-9_-]{0,31}$/.test(user) || user === "root") {
    throw new Error(`${prefix}のSSHユーザーにはroot以外のLinuxユーザー名を入力してください。`);
  }
  return {
    host_id: hostID,
    name,
    address,
    port: requiredPort(host.port, `${prefix}のSSHポート`),
    user,
    arch: requiredText(host.arch, `${prefix}のCPUアーキテクチャ`),
    host_public_key: hostPublicKey,
  };
}

function normalizeTargetForSave(
  target: UpdaterSettingsTarget,
  index: number,
  hostIDs: Set<string>,
  transportMode: UpdaterSettings["transport_mode"],
): UpdaterSettingsTarget {
  const prefix = `サービス ${index + 1}`;
  const hostID = requiredText(target.host_id, `${prefix}のホスト`);
  if (!hostIDs.has(hostID)) throw new Error(`${prefix}で選択したホストが見つかりません。`);
  const targetID = requiredText(target.target_id, `${prefix}の対象ID`);
  if (!validPolicyIdentifier(targetID)) throw new Error(`${prefix}の対象IDは英数字で始まり、英数字・.・_・:・-のみで入力してください。`);
  const serviceID = requiredText(target.service_id || targetID, `${prefix}のNodeサービスID`);
  if (!validPolicyIdentifier(serviceID)) throw new Error(`${prefix}のNodeサービスIDは英数字で始まり、英数字・.・_・:・-のみで入力してください。`);
  const normalizedTarget: UpdaterSettingsTarget = {
    target_id: targetID,
    service_id: serviceID,
    host_id: hostID,
    service_type: requiredText(target.service_type, `${prefix}のサービス種別`),
    deployment_mode: requiredText(target.deployment_mode, `${prefix}の配備方式`),
  };
  const databaseName = normalizeUpdaterSettingsTargetDatabaseName(
    transportMode,
    { ...normalizedTarget, database_name: target.database_name },
    prefix,
  );
  if (databaseName) normalizedTarget.database_name = databaseName;
  const localListenPort = normalizeUpdaterSettingsTargetLocalListenPort(
    transportMode,
    { ...normalizedTarget, local_listen_port: target.local_listen_port },
    prefix,
  );
  if (localListenPort !== undefined) normalizedTarget.local_listen_port = localListenPort;
  return normalizedTarget;
}

function requiredText(value: unknown, label: string) {
  const text = String(value || "").trim();
  if (!text) throw new Error(`${label}を入力してください。`);
  return text;
}

function validPolicyIdentifier(value: string) {
  return /^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$/.test(value);
}

function requiredPort(value: unknown, label: string) {
  const number = Number(value);
  if (!Number.isInteger(number) || number < 1 || number > 65535) throw new Error(`${label}は1〜65535の整数で入力してください。`);
  return number;
}

function requiredInterval(value: unknown, label: string) {
  const number = Number(value);
  if (!Number.isInteger(number) || number < 5 || number > 3600) throw new Error(`${label}は5〜3600秒の整数で入力してください。`);
  return number;
}

function requiredHeartbeatInterval(value: unknown) {
  const number = Number(value);
  if (!Number.isInteger(number) || number < 5 || number > 60) {
    throw new Error("Heartbeat間隔は5〜60秒の整数で入力してください。現在の値を5〜60秒に変更してから保存してください。");
  }
  return number;
}

function newHost(index: number): UpdaterSettingsHost {
  return {
    host_id: `host-${index + 1}`,
    name: `ホスト ${index + 1}`,
    address: "",
    port: 22,
    user: "autostream-update-host",
    arch: "amd64",
    host_public_key: "",
  };
}

function serviceTypeLabel(value: string) {
  return serviceTypes.find((option) => option.value === value)?.label || value || "未設定";
}

function selectOptionsWithCurrent(
  options: readonly { value: string; label: string }[],
  current: string,
) {
  if (!current || options.some((option) => option.value === current)) return options;
  return [...options, { value: current, label: current }];
}

function updaterSettingsErrorMessage(error: unknown) {
  if (error instanceof Error && !("status" in error)) return error.message;
  return systemUpdateErrorMessage(error, "Updater設定を保存できませんでした。入力内容と権限を確認してください。");
}
