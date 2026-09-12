"use client";

import { type ReactNode, useId, useMemo, useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { Check, Copy, KeyRound, LoaderCircle, Plus, Settings2, Trash2 } from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { DialogFooter } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Textarea } from "@/components/ui/textarea";
import { DeferredUpdaterHostConfirmation } from "@/features/application/deferred-updater-host-confirmation";
import { UpdaterHostBootstrapPanel } from "@/features/application/updater-host-bootstrap-panel";
import { UpdaterActionConfirmation } from "@/features/application/updater-action-confirmation";
import { createUpdaterActionController, updaterAuthorityFingerprint, type UpdaterActionAuthority, type UpdaterActionIntent } from "@/features/application/updater-action-policy";
import { useCurrentUser, useUpdaterSettings } from "@/features/queries";
import { apiPut } from "@/lib/api/client";
import { hasPermission } from "@/lib/auth/permissions";
import { applyUpdaterSettingsTargetSelection, applyUpdaterSettingsTargetPatch, firstUnusedUpdaterSettingsTarget, normalizeUpdaterSettingsResponse, updaterSettingsTargetOptions, updaterSettingsTargetRequiresDatabase, updaterSettingsTargetRequiresLocalListenPort } from "@/lib/updater-settings-model";
import type { SystemUpdateAgentStatus, SystemUpdateTarget, UpdaterSettings, UpdaterSettingsHost, UpdaterSettingsTarget } from "@/types/domain";
import { type UpdaterSettingsFormState, settingsToForm, buildUpdaterSettingsPayload, updaterSettingsErrorMessage, newHost, serviceTypeLabel, selectOptionsWithCurrent, deploymentModes } from "./updater-settings-form-model";
import { updaterSettingsFormFingerprint, updaterSettingsActionAuthoritySnapshot, unavailableUpdaterAuthority } from "./updater-settings-authority";

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
      setFeedback({ tone: "success", message: "設定を保存しました。Host Agentのconfigureを再実行すると反映されます。反映済みになるまで更新操作は安全のため停止します。" });
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
        {!canEdit ? (
          <div className="rounded-md border border-amber-300 bg-amber-50 p-3 text-xs text-amber-950 dark:border-amber-900 dark:bg-amber-950/35 dark:text-amber-100">
            設定の変更には system_updates.execute 権限が必要です。現在は内容の確認だけできます。
          </div>
        ) : (
          <div className="rounded-md border border-blue-300 bg-blue-50 p-3 text-xs leading-5 text-blue-950 dark:border-blue-900 dark:bg-blue-950/35 dark:text-blue-100">
            「設定を保存」を押した後、Host Agentのconfigureを再実行すると反映されます。反映済みになるまで更新操作は安全のため停止します。
          </div>
        )}

        <section className="space-y-3" aria-labelledby={`${formID}-runtime-heading`}>
          <div>
            <h3 id={`${formID}-runtime-heading`} className="font-medium">Host Agentの動作</h3>
            <p className="text-xs text-muted-foreground">
              Host AgentからControl Panelへoutbound HTTPSで接続します。受信APIや管理用ポートは使用しません。
            </p>
          </div>
          <div className="flex flex-wrap gap-2 rounded-md border bg-muted/30 p-3 text-xs">
            <Badge variant="secondary">pull_v2</Badge>
            <span>実行ホスト: {executionHostID || "未割り当て"}</span>
            <span>受信ポート: なし</span>
          </div>
          <div className="grid gap-3 sm:grid-cols-3">
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
          </div>
        </section>

        <section className="space-y-3" aria-labelledby={`${formID}-hosts-heading`}>
          <div className="flex flex-wrap items-start justify-between gap-3">
            <div>
              <h3 id={`${formID}-hosts-heading`} className="font-medium">bootstrap対象ホスト</h3>
              <p className="max-w-3xl text-xs leading-5 text-muted-foreground">
                この一覧は独立UpdaterがHost Agentを初期導入するためだけに使用します。pull_v2の実行対象・所有権とは分離されています。対象サーバーで確認したssh-ed25519ホスト公開鍵の全文を、別の安全な経路で受け取って入力してください。初回接続時の自動信頼は行いません。
              </p>
            </div>
            {canEdit ? (
              <Button
                type="button"
                variant="outline"
                size="sm"
                onClick={() => setForm((current) => ({
                  ...current,
                  hosts: [...current.hosts, newHost(current.hosts.length, current.hosts.length === 0 ? executionHostID : "")],
                }))}
              >
                <Plus className="size-4" />
                ホストを追加
              </Button>
            ) : null}
          </div>

          {form.hosts.length === 0 ? (
            <div className="rounded-md border border-dashed p-5 text-sm text-muted-foreground">bootstrap対象ホストはまだありません。「ホストを追加」から登録してください。</div>
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
                        <DeferredUpdaterHostConfirmation
                          title={`「${host.name || host.host_id || `ホスト ${index + 1}`}」を削除しますか？`}
                          description="このbootstrap用ホスト定義をフォームから削除します。pull_v2の実行対象・所有権は変更しません。「設定を保存」で反映した後、必要に応じて対象ホストのbootstrap用authorized_keysを撤去してください。"
                          actionLabel="ホストを削除"
                          onConfirm={() => removeHost(index)}
                        >
                          <Button type="button" variant="ghost" size="sm" aria-label={`ホスト ${index + 1} を削除`}>
                            <Trash2 className="size-4" />
                            削除
                          </Button>
                        </DeferredUpdaterHostConfirmation>
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
                        hint="自動bootstrapでは autostream-update-host 固定です。一時管理者ユーザーとは別です。"
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
                      <div className="flex items-center gap-2 text-sm font-medium"><KeyRound className="size-4" />対象ホストへ登録する独立Updater公開鍵</div>
                      <p className="text-xs text-muted-foreground">設定保存後に独立Updaterが生成し、bootstrap時に対象ホストへ登録します。</p>
                      {clientPublicKey ? (
                        <div className="flex items-start gap-2">
                          <Textarea readOnly value={clientPublicKey} rows={2} aria-label={`${host.name || host.host_id} 用Updater SSHクライアント公開鍵`} className="font-mono text-xs" />
                          <Button type="button" variant="outline" size="icon-sm" onClick={() => void copyClientPublicKey(host.host_id, clientPublicKey)} aria-label={`${host.name || host.host_id} 用公開鍵をコピー`}>
                            {copiedHostID === host.host_id ? <Check className="size-4" /> : <Copy className="size-4" />}
                          </Button>
                        </div>
                      ) : (
                        <div className="text-xs text-amber-700 dark:text-amber-300">まだ生成されていません。設定を保存し、独立Updaterが反映するまでお待ちください。</div>
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
        </section>

        <section className="space-y-3" aria-labelledby={`${formID}-targets-heading`}>
          <div className="flex flex-wrap items-start justify-between gap-3">
            <div>
              <h3 id={`${formID}-targets-heading`} className="font-medium">更新するサービス</h3>
              <p className="text-xs text-muted-foreground">
                {`実行ホスト ${executionHostID || "未割り当て"} 上で管理するAutoStreamサービスを指定します。ホスト割り当てはControl Panelが管理します。`}
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
                  const hostID = String(executionHostID).trim();
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
                    <Field label="実行ホスト（サーバー管理）" htmlFor={`${formID}-target-${index}-host`}>
                      <Select value={target.host_id || executionHostID} onValueChange={(value) => updateTarget(index, { host_id: value })} disabled>
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

        <section className="space-y-3" aria-labelledby={`${formID}-github-heading`}>
          <div>
            <h3 id={`${formID}-github-heading`} className="font-medium">GitHub Release Token</h3>
            <p className="text-xs text-muted-foreground">
              bootstrap artifact取得用TokenはControl Panelの暗号化secretにだけ保存され、値は再表示されません。独立Updaterがbootstrap jobをclaimした時だけ一回限りで渡し、Host Agentのpolicyや応答には含めません。
            </p>
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
        </section>

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

export function OwnershipStateItem({ label, value }: { label: string; value: ReactNode }) {
  return <div className="rounded-md border bg-background/70 px-3 py-2"><div className="text-muted-foreground">{label}</div><div className="mt-0.5 break-all font-medium">{value}</div></div>;
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
