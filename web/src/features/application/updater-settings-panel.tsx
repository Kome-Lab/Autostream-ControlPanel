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
import { useUpdaterSettings } from "@/features/queries";
import { apiPut } from "@/lib/api/client";
import { isUpdaterPolicyHostID, normalizeUpdaterSettingsResponse, systemUpdateErrorMessage, systemUpdatePolicyErrorMessage, systemUpdateUpdaterPolicyState } from "@/lib/system-updates";
import type {
  SystemUpdateAgentStatus,
  UpdaterSettings,
  UpdaterSettingsHost,
  UpdaterSettingsTarget,
  UpdaterSettingsUpdate,
} from "@/types/domain";

type UpdaterSettingsPanelProps = {
  updater: SystemUpdateAgentStatus;
  canEdit: boolean;
  canManageSecrets: boolean;
};

type UpdaterSettingsFormState = {
  apiPort: string;
  pollInterval: string;
  heartbeatInterval: string;
  hosts: UpdaterSettingsHost[];
  targets: UpdaterSettingsTarget[];
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

export function UpdaterSettingsPanel({ updater, canEdit, canManageSecrets }: UpdaterSettingsPanelProps) {
  const [open, setOpen] = useState(false);
  const [bootstrapCloseBlocked, setBootstrapCloseBlocked] = useState(false);
  const settings = useUpdaterSettings(updater.updater_id, open);
  const policyState = systemUpdateUpdaterPolicyState(updater);
  const setDialogOpen = (nextOpen: boolean) => {
    if (!nextOpen && bootstrapCloseBlocked) return;
    setOpen(nextOpen);
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
        showCloseButton={!bootstrapCloseBlocked}
        onEscapeKeyDown={(event) => {
          if (bootstrapCloseBlocked) event.preventDefault();
        }}
        onPointerDownOutside={(event) => {
          if (bootstrapCloseBlocked) event.preventDefault();
        }}
      >
        <DialogHeader>
          <DialogTitle>{updater.name || updater.updater_id} の設定</DialogTitle>
          <DialogDescription>
            中央Updaterが管理するホストとサービスをControl Panel上で設定します。保存後はUpdaterが設定を取得し、自動で反映します。
          </DialogDescription>
        </DialogHeader>

        <div className="flex flex-wrap items-center gap-2 rounded-md border bg-muted/30 p-3 text-xs">
          <Badge variant={policyState.tone}>{policyState.label}</Badge>
          <span>設定Revision: {updater.desired_revision ?? settings.data?.revision ?? 0}</span>
          <span>反映済みRevision: {updater.applied_revision ?? 0}</span>
          {updater.policy_error_code || updater.policy_error ? (
            <span className="break-words text-destructive">反映情報: {systemUpdatePolicyErrorMessage(updater.policy_error_code || updater.policy_error)}</span>
          ) : null}
        </div>

        {settings.isLoading ? (
          <div className="flex items-center gap-2 rounded-md border border-dashed p-6 text-sm text-muted-foreground" role="status">
            <LoaderCircle className="size-4 animate-spin" />
            Updater設定を読み込んでいます。
          </div>
        ) : settings.isError ? (
          <div className="rounded-md border border-destructive/40 bg-destructive/5 p-4 text-sm text-destructive">
            {systemUpdateErrorMessage(settings.error, "Updater設定を取得できませんでした。")}
          </div>
        ) : settings.data ? (
          <UpdaterSettingsForm
            key={settings.data.updater_id}
            updater={updater}
            settings={settings.data}
            canEdit={canEdit}
            canManageSecrets={canManageSecrets}
            onBootstrapCloseBlockedChange={setBootstrapCloseBlocked}
          />
        ) : null}
      </DialogContent>
    </Dialog>
  );
}

function UpdaterSettingsForm({
  updater,
  settings,
  canEdit,
  canManageSecrets,
  onBootstrapCloseBlockedChange,
}: {
  updater: SystemUpdateAgentStatus;
  settings: UpdaterSettings;
  canEdit: boolean;
  canManageSecrets: boolean;
  onBootstrapCloseBlockedChange: (blocked: boolean) => void;
}) {
  const queryClient = useQueryClient();
  const formID = useId();
  const [form, setForm] = useState<UpdaterSettingsFormState>(() => settingsToForm(settings));
  const [baseRevision, setBaseRevision] = useState(settings.revision);
  const [githubToken, setGithubToken] = useState("");
  const [deleteGitHubToken, setDeleteGitHubToken] = useState(false);
  const [feedback, setFeedback] = useState<{ tone: "success" | "error"; message: string } | null>(null);
  const [copiedHostID, setCopiedHostID] = useState("");
  const [bootstrapActive, setBootstrapActive] = useState(false);

  const hostOptions = useMemo(() => form.hosts.map((host) => ({
    value: host.host_id,
    label: host.name || host.host_id || "ID未入力",
  })), [form.hosts]);

  const saveSettings = useMutation({
    mutationFn: async () => {
      if (bootstrapActive) {
        throw Object.assign(new Error("updater_host_bootstrap_in_progress"), {
          code: "updater_host_bootstrap_in_progress",
          status: 409,
        });
      }
      const payload = buildUpdaterSettingsPayload(baseRevision, form, canManageSecrets ? {
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
      setFeedback({ tone: "success", message: "設定を保存しました。Updaterが自動で反映します。反映済みになるまで更新操作は安全のため停止します。" });
      queryClient.setQueryData(["system-updates", "updaters", updater.updater_id, "settings"], saved);
      await Promise.all([
        queryClient.invalidateQueries({ queryKey: ["system-updates"] }),
        queryClient.invalidateQueries({ queryKey: ["system-updates", "updaters", updater.updater_id, "settings"] }),
      ]);
    },
    onError: (error) => {
      setFeedback({ tone: "error", message: updaterSettingsErrorMessage(error) });
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
      targets: current.targets.map((target, targetIndex) => targetIndex === index ? { ...target, ...patch } : target),
    }));
  };

  const copyClientPublicKey = async (hostID: string, publicKey: string) => {
    if (!publicKey || typeof navigator === "undefined" || !navigator.clipboard) return;
    await navigator.clipboard.writeText(publicKey);
    setCopiedHostID(hostID);
    window.setTimeout(() => setCopiedHostID((current) => current === hostID ? "" : current), 2_000);
  };

  return (
    <>
      <div className="space-y-6">
        {!canEdit ? (
          <div className="rounded-md border border-amber-300 bg-amber-50 p-3 text-xs text-amber-950 dark:border-amber-900 dark:bg-amber-950/35 dark:text-amber-100">
            設定の変更には system_updates.execute と secrets.update の両方の権限が必要です。現在は内容の確認だけできます。
          </div>
        ) : (
          <div className="rounded-md border border-blue-300 bg-blue-50 p-3 text-xs leading-5 text-blue-950 dark:border-blue-900 dark:bg-blue-950/35 dark:text-blue-100">
            「設定を保存」を押すと、この内容をUpdaterが自動で取得して反映します。反映に失敗した場合は更新操作を安全のため停止し、旧設定への復帰または自動再試行の状態をここに表示します。
          </div>
        )}

        <section className="space-y-3" aria-labelledby={`${formID}-runtime-heading`}>
          <div>
            <h3 id={`${formID}-runtime-heading`} className="font-medium">Updaterの動作</h3>
            <p className="text-xs text-muted-foreground">APIは中央ホスト内だけで待ち受けます。通常は初期値のままで使用できます。</p>
          </div>
          <div className="grid gap-3 sm:grid-cols-3">
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
          </div>
        </section>

        <section className="space-y-3" aria-labelledby={`${formID}-hosts-heading`}>
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
              onActiveChange={setBootstrapActive}
              onCloseBlockedChange={onBootstrapCloseBlockedChange}
            />
          ) : null}
        </section>

        <section className="space-y-3" aria-labelledby={`${formID}-targets-heading`}>
          <div className="flex flex-wrap items-start justify-between gap-3">
            <div>
              <h3 id={`${formID}-targets-heading`} className="font-medium">更新するサービス</h3>
              <p className="text-xs text-muted-foreground">どのホスト上の、どのAutoStreamサービスを更新するか指定します。</p>
            </div>
            {canEdit ? (
              <Button
                type="button"
                variant="outline"
                size="sm"
                disabled={form.hosts.length === 0}
                onClick={() => setForm((current) => ({ ...current, targets: [...current.targets, newTarget(current.targets.length, current.hosts[0]?.host_id || "")] }))}
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
              {form.targets.map((target, index) => (
                <div key={index} className="grid gap-3 rounded-md border p-4 sm:grid-cols-2 lg:grid-cols-[1.2fr_1fr_1fr_1fr_auto] lg:items-end">
                  <Field label="対象ID" htmlFor={`${formID}-target-${index}-id`}>
                    <Input id={`${formID}-target-${index}-id`} value={target.target_id} onChange={(event) => updateTarget(index, { target_id: event.target.value })} disabled={!canEdit} placeholder="worker-main" />
                  </Field>
                  <Field label="ホスト" htmlFor={`${formID}-target-${index}-host`}>
                    <Select value={target.host_id} onValueChange={(value) => updateTarget(index, { host_id: value })} disabled={!canEdit || hostOptions.length === 0}>
                      <SelectTrigger id={`${formID}-target-${index}-host`}><SelectValue placeholder="ホストを選択" /></SelectTrigger>
                      <SelectContent>
                        {hostOptions.map((host) => <SelectItem key={host.value} value={host.value}>{host.label}</SelectItem>)}
                      </SelectContent>
                    </Select>
                  </Field>
                  <Field label="サービス種別" htmlFor={`${formID}-target-${index}-service`}>
                    <Select value={target.service_type || "worker"} onValueChange={(value) => updateTarget(index, { service_type: value })} disabled={!canEdit}>
                      <SelectTrigger id={`${formID}-target-${index}-service`}><SelectValue /></SelectTrigger>
                      <SelectContent>
                        {selectOptionsWithCurrent(serviceTypes, target.service_type).map((option) => <SelectItem key={option.value} value={option.value}>{option.label}</SelectItem>)}
                      </SelectContent>
                    </Select>
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
                </div>
              ))}
            </div>
          )}
        </section>

        <section className="space-y-3" aria-labelledby={`${formID}-github-heading`}>
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
        {bootstrapActive ? (
          <div className="rounded-md border border-amber-300 bg-amber-50 p-3 text-sm text-amber-950 dark:border-amber-900 dark:bg-amber-950/35 dark:text-amber-100" role="status">
            ホストの自動セットアップ中はUpdater設定を保存できません。完了後に再試行してください。
          </div>
        ) : null}
      </div>

      <DialogFooter className="mt-6">
        {canEdit ? (
          <Button
            type="button"
            onClick={() => saveSettings.mutate()}
            disabled={saveSettings.isPending || bootstrapActive}
            title={bootstrapActive ? "ホストの自動セットアップ完了後に保存できます。" : undefined}
          >
            {saveSettings.isPending ? <LoaderCircle className="size-4 animate-spin" /> : <Settings2 className="size-4" />}
            設定を保存
          </Button>
        ) : null}
      </DialogFooter>
    </>
  );
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
    hosts: settings.hosts.map((host) => ({ ...host })),
    targets: settings.targets.map((target) => ({ ...target })),
  };
}

function buildUpdaterSettingsPayload(
  expectedRevision: number,
  form: UpdaterSettingsFormState,
  secretUpdate?: { githubToken: string; deleteGitHubToken: boolean },
): UpdaterSettingsUpdate {
  const apiPort = requiredPort(form.apiPort, "APIポート");
  const pollInterval = requiredInterval(form.pollInterval, "更新確認間隔");
  const heartbeatInterval = requiredHeartbeatInterval(form.heartbeatInterval);
  if (form.hosts.length === 0) throw new Error("管理対象ホストを1件以上追加してください。");
  if (form.hosts.length > 128) throw new Error("管理対象ホストは128件まで登録できます。");
  const hosts = form.hosts.map((host, index) => normalizeHostForSave(host, index));
  const hostIDs = new Set(hosts.map((host) => host.host_id));
  if (hostIDs.size !== hosts.length) throw new Error("ホストIDが重複しています。");
  if (form.targets.length === 0) throw new Error("更新するサービスを1件以上追加してください。");
  if (form.targets.length > 1024) throw new Error("更新するサービスは1024件まで登録できます。");
  const targets = form.targets.map((target, index) => normalizeTargetForSave(target, index, hostIDs));
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

function normalizeTargetForSave(target: UpdaterSettingsTarget, index: number, hostIDs: Set<string>): UpdaterSettingsTarget {
  const prefix = `サービス ${index + 1}`;
  const hostID = requiredText(target.host_id, `${prefix}のホスト`);
  if (!hostIDs.has(hostID)) throw new Error(`${prefix}で選択したホストが見つかりません。`);
  const targetID = requiredText(target.target_id, `${prefix}の対象ID`);
  if (!validPolicyIdentifier(targetID)) throw new Error(`${prefix}の対象IDは英数字で始まり、英数字・.・_・:・-のみで入力してください。`);
  return {
    target_id: targetID,
    host_id: hostID,
    service_type: requiredText(target.service_type, `${prefix}のサービス種別`),
    deployment_mode: requiredText(target.deployment_mode, `${prefix}の配備方式`),
  };
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

function newTarget(index: number, hostID: string): UpdaterSettingsTarget {
  return {
    target_id: `target-${index + 1}`,
    host_id: hostID,
    service_type: "worker",
    deployment_mode: "systemd",
  };
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
