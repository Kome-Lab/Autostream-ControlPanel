"use client";
import { fixedPresentationText } from "@/lib/i18n/ui-v2/presentation-copy";
import { useUICopy } from "@/lib/i18n/ui-v2/use-ui-copy";


import { useExistingDraft } from "@/components/forms/draft-exit";
import { type ReactNode, useId, useMemo, useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { LoaderCircle, Settings2 } from "lucide-react";
import { FormFooter } from "@/components/forms/form-footer";
import { SectionNavigation } from "@/components/layout/detail-section";
import { useI18n } from "@/components/admin/i18n-provider";
import { UpdaterHostBootstrapPanel } from "@/features/application/updater-host-bootstrap-panel";
import { UpdaterActionConfirmation } from "@/features/application/updater-action-confirmation";
import { createUpdaterActionController, updaterAuthorityFingerprint, type UpdaterActionAuthority, type UpdaterActionIntent } from "@/features/application/updater-action-policy";
import { useCurrentUser, useUpdaterSettings } from "@/features/queries";
import { apiPut } from "@/lib/api/client";
import { hasPermission } from "@/lib/auth/permissions";
import { applyUpdaterSettingsTargetSelection, applyUpdaterSettingsTargetPatch, firstUnusedUpdaterSettingsTarget, normalizeUpdaterSettingsResponse } from "@/lib/updater-settings-model";
import type { SystemUpdateAgentStatus, SystemUpdateTarget, UpdaterSettings, UpdaterSettingsHost, UpdaterSettingsTarget } from "@/types/domain";
import { type UpdaterSettingsFormState, settingsToForm, buildUpdaterSettingsPayload, updaterSettingsErrorMessage, newHost } from "./updater-settings-form-model";
import { updaterSettingsFormFingerprint, updaterSettingsActionAuthoritySnapshot, unavailableUpdaterAuthority } from "./updater-settings-authority";
import { UpdaterRuntimeSettingsSection } from "./updater-runtime-settings-section";
import { UpdaterBootstrapHostsSection } from "./updater-bootstrap-hosts-section";
import { UpdaterTargetsSettingsSection } from "./updater-targets-settings-section";
import { UpdaterReleaseTokenSection } from "./updater-release-token-section";

export function UpdaterSettingsForm({
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
  const uiText = useUICopy();
  const { locale } = useI18n();
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
  const executionHostID = settings.execution_host_id || "";

  const hostOptions = useMemo(
    () => executionHostID ? [{ value: executionHostID, label: executionHostID }] : [],
    [executionHostID],
  );
  const nextTargetHostID = (hostOptions[0]?.value || "").trim();
  const canAddRegisteredTarget = Boolean(
    nextTargetHostID
    && firstUnusedUpdaterSettingsTarget(settings.transport_mode, availableTargets, form.targets, nextTargetHostID),
  );

  const saveSettings = useMutation({
    mutationFn: async () => {
      if (bootstrapActive || ownershipOperationBlocked) {
        const code = bootstrapActive
          ? "updater_host_bootstrap_in_progress"
          : "updater_ownership_transition_in_progress";
        throw Object.assign(new Error(code), {
          code,
          status: 409,
        });
      }
      const payload = buildUpdaterSettingsPayload(baseRevision, form, settings, canManageSecrets ? {
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
      setFeedback({ tone: "success", message: uiText("設定を保存しました。Host Agentのconfigureを再実行すると反映されます。反映済みになるまで更新操作は安全のため停止します。") });
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

  const savedForm = settingsToForm(settingsAuthorityQuery.data || settings);
  useExistingDraft(JSON.stringify(updaterDraftValues(form)) !== JSON.stringify(updaterDraftValues(savedForm)) || deleteGitHubToken,
    saveSettings.isPending || bootstrapActive || ownershipOperationBlocked);

  const updateHost = (index: number, patch: Partial<UpdaterSettingsHost>) => {
    setForm((current) => ({
      ...current,
      hosts: current.hosts.map((host, hostIndex) => hostIndex === index ? { ...host, ...patch } : host),
    }));
  };

  const removeHost = (index: number) => {
    setForm((current) => ({
      ...current,
      hosts: current.hosts.filter((_, hostIndex) => hostIndex !== index),
    }));
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
        <SectionNavigation label={locale === "ja" ? "Updater設定のセクション" : "Updater settings sections"} items={[
          { id: formID + "-runtime-section", label: "Runtime / policy" },
          { id: formID + "-hosts-section", label: "Host / bootstrap" },
          { id: formID + "-targets-section", label: "Targets" },
          { id: formID + "-token-section", label: "Release credentials" },
        ]} />
        {!canEdit ? (
          <div className="rounded-md border border-amber-300 bg-amber-50 p-3 text-xs text-amber-950 dark:border-amber-900 dark:bg-amber-950/35 dark:text-amber-100">
            {uiText("設定の変更には system_updates.execute 権限が必要です。現在は内容の確認だけできます。")}</div>
        ) : (
          <div className="rounded-md border border-blue-300 bg-blue-50 p-3 text-xs leading-5 text-blue-950 dark:border-blue-900 dark:bg-blue-950/35 dark:text-blue-100">
            {uiText("「設定を保存」を押した後、Host Agentのconfigureを再実行すると反映されます。反映済みになるまで更新操作は安全のため停止します。")}</div>
        )}

        <section id={formID + "-runtime-section"} tabIndex={-1} className="min-w-0 scroll-mt-24">
        <UpdaterRuntimeSettingsSection
          formID={formID} executionHostID={executionHostID} canEdit={canEdit}
          pollInterval={form.pollInterval} heartbeatInterval={form.heartbeatInterval} localExecutorPolicySHA256={form.localExecutorPolicySHA256}
          changePollInterval={(event) => setForm((current) => ({ ...current, pollInterval: event.target.value }))}
          changeHeartbeatInterval={(event) => setForm((current) => ({ ...current, heartbeatInterval: event.target.value }))}
          changePolicyDigest={(event) => setForm((current) => ({ ...current, localExecutorPolicySHA256: event.target.value }))}
        />
        </section>

        <section id={formID + "-hosts-section"} tabIndex={-1} className="min-w-0 scroll-mt-24">
        <UpdaterBootstrapHostsSection
          formID={formID} hosts={form.hosts} canEdit={canEdit}
          clientPublicKeys={updater.ssh_client_public_keys} clientKeyFingerprints={updater.ssh_client_key_fingerprints}
          copiedHostID={copiedHostID} updateHost={updateHost} removeHost={removeHost} copyClientPublicKey={copyClientPublicKey}
          addHost={() => setForm((current) => ({
                  ...current,
                  hosts: [...current.hosts, newHost(current.hosts.length, current.hosts.length === 0 ? executionHostID : "")],
                }))}
          bootstrapPanel={form.hosts.length > 0 || settings.hosts.length > 0 ? (
            <UpdaterHostBootstrapPanel
              key={canEdit ? "bootstrap-edit" : "bootstrap-readonly"}
              updater={updater}
              expectedRevision={baseRevision}
              expectedAppliedRevision={settings.projection_revision ?? settings.revision}
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
        />
        </section>

        <section id={formID + "-targets-section"} tabIndex={-1} className="min-w-0 scroll-mt-24">
        <UpdaterTargetsSettingsSection
          formID={formID} executionHostID={executionHostID} canEdit={canEdit}
          canAddRegisteredTarget={canAddRegisteredTarget} nextTargetHostID={nextTargetHostID}
          targets={form.targets} availableTargets={availableTargets} hostOptions={hostOptions} transportMode={settings.transport_mode}
          updateTarget={updateTarget} selectTarget={selectTarget}
          addTarget={() => setForm((current) => {
                  const hostID = String(executionHostID).trim();
                  const target = firstUnusedUpdaterSettingsTarget(settings.transport_mode, availableTargets, current.targets, hostID);
                  if (!hostID || !target) return current;
                  return { ...current, targets: [...current.targets, target] };
                })}
          removeTarget={(index) => setForm((current) => ({ ...current, targets: current.targets.filter((_, targetIndex) => targetIndex !== index) }))}
        />
        </section>

        <section id={formID + "-token-section"} tabIndex={-1} className="min-w-0 scroll-mt-24">
        <UpdaterReleaseTokenSection
          formID={formID} tokenConfigured={settings.github_token_configured} tokenFingerprint={settings.github_token_fingerprint}
          canManageSecrets={canManageSecrets} canEdit={canEdit} githubToken={githubToken} deleteGitHubToken={deleteGitHubToken}
          setGithubToken={setGithubToken} setDeleteGitHubToken={setDeleteGitHubToken}
        />
        </section>

        {feedback ? (
          <div
            className={feedback.tone === "success"
              ? "rounded-md border border-emerald-300 bg-emerald-50 p-3 text-sm text-emerald-950 dark:border-emerald-900 dark:bg-emerald-950/35 dark:text-emerald-100"
              : "rounded-md border border-destructive/40 bg-destructive/5 p-3 text-sm text-destructive"}
            role={feedback.tone === "error" ? "alert" : "status"}
          >
            {fixedPresentationText(feedback.message, uiText)}
          </div>
        ) : null}
      </div>

      <FormFooter pending={saveSettings.isPending || bootstrapActive || ownershipOperationBlocked}>
        {canEdit ? (
          <UpdaterActionConfirmation
            controller={updaterActionController}
            intent={settingsActionIntent}
            authority={settingsActionAuthority}
            refreshAuthority={refreshSettingsActionAuthority}
            handler={() => saveSettings.mutateAsync()}
            label={uiText("設定を保存")}
            icon={saveSettings.isPending ? <LoaderCircle className="size-4 animate-spin" /> : <Settings2 className="size-4" />}
            size="default"
            disabled={saveSettings.isPending || bootstrapActive || ownershipOperationBlocked}
            title={bootstrapActive
              ? uiText("ホストの自動セットアップ完了後に保存できます。")
              : ownershipOperationBlocked
                ? uiText("更新実行権限の切替状態を確認してから保存できます。")
                : undefined}
          />
        ) : null}
      </FormFooter>
    </>
  );
}

export function OwnershipStateItem({ label, value }: { label: string; value: ReactNode }) {
  return <div className="rounded-md border bg-background/70 px-3 py-2"><div className="text-muted-foreground">{label}</div><div className="mt-0.5 break-all font-medium">{value}</div></div>;
}

function updaterDraftValues(form: UpdaterSettingsFormState) {
  return [form.pollInterval, form.heartbeatInterval, form.localExecutorPolicySHA256,
    form.hosts.map((host) => [host.host_id, host.name, host.address, host.port, host.user, host.arch, host.host_public_key]),
    form.targets.map((target) => [target.target_id, target.service_id, target.host_id, target.service_type, target.deployment_mode, target.database_name, target.local_listen_port])];
}
