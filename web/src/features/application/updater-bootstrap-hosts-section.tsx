
import { useUICopy } from "@/lib/i18n/ui-v2/use-ui-copy";
import { type ReactNode } from "react";
import { Check, Copy, KeyRound, Plus, Trash2 } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Textarea } from "@/components/ui/textarea";
import { DeferredUpdaterHostConfirmation } from "@/features/application/deferred-updater-host-confirmation";
import type { SystemUpdateAgentStatus, UpdaterSettingsHost } from "@/types/domain";
import { Field } from "./updater-settings-field";



export function UpdaterBootstrapHostsSection({
  formID,
  hosts,
  canEdit,
  clientPublicKeys,
  clientKeyFingerprints,
  copiedHostID,
  addHost,
  updateHost,
  removeHost,
  copyClientPublicKey,
  bootstrapPanel,
}: {
  formID: string;
  hosts: UpdaterSettingsHost[];
  canEdit: boolean;
  clientPublicKeys: SystemUpdateAgentStatus["ssh_client_public_keys"];
  clientKeyFingerprints: SystemUpdateAgentStatus["ssh_client_key_fingerprints"];
  copiedHostID: string;
  addHost: () => void;
  updateHost: (index: number, patch: Partial<UpdaterSettingsHost>) => void;
  removeHost: (index: number) => void;
  copyClientPublicKey: (hostID: string, publicKey: string) => Promise<void>;
  bootstrapPanel: ReactNode
}) {
  const uiText = useUICopy();
  return (
<section className="space-y-3" aria-labelledby={`${formID}-hosts-heading`}>
          <div className="flex flex-wrap items-start justify-between gap-3">
            <div>
              <h3 id={`${formID}-hosts-heading`} className="font-medium">{uiText("bootstrap対象ホスト")}</h3>
              <p className="max-w-3xl text-xs leading-5 text-muted-foreground">
                {uiText("この一覧は独立UpdaterがHost Agentを初期導入するためだけに使用します。pull_v2の実行対象・所有権とは分離されています。対象サーバーで確認したssh-ed25519ホスト公開鍵の全文を、別の安全な経路で受け取って入力してください。初回接続時の自動信頼は行いません。")}</p>
            </div>
            {canEdit ? (
              <Button
                type="button"
                variant="outline"
                size="sm"
                onClick={addHost}
              >
                <Plus className="size-4" />
                {uiText("ホストを追加")}</Button>
            ) : null}
          </div>

          {hosts.length === 0 ? (
            <div className="rounded-md border border-dashed p-5 text-sm text-muted-foreground">{uiText("bootstrap対象ホストはまだありません。「ホストを追加」から登録してください。")}</div>
          ) : (
            <div className="space-y-4">
              {hosts.map((host, index) => {
                const clientPublicKey = clientPublicKeys?.[host.host_id] || "";
                const clientKeyFingerprint = clientKeyFingerprints?.[host.host_id] || "";
                return (
                  <div key={index} className="space-y-3 rounded-md border p-4">
                    <div className="flex items-center justify-between gap-3">
                      <div className="text-sm font-medium">{uiText("ホスト")}{index + 1}</div>
                      {canEdit ? (
                        <DeferredUpdaterHostConfirmation
                          title={uiText("「{0}」を削除しますか？", host.name || host.host_id || `ホスト ${index + 1}`)}
                          description={uiText("このbootstrap用ホスト定義をフォームから削除します。pull_v2の実行対象・所有権は変更しません。「設定を保存」で反映した後、必要に応じて対象ホストのbootstrap用authorized_keysを撤去してください。")}
                          actionLabel={uiText("ホストを削除")}
                          onConfirm={() => removeHost(index)}
                        >
                          <Button type="button" variant="ghost" size="sm" aria-label={uiText("ホスト {0} を削除", index + 1)}>
                            <Trash2 className="size-4" />
                            {uiText("削除")}</Button>
                        </DeferredUpdaterHostConfirmation>
                      ) : null}
                    </div>
                    <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-4">
                      <Field label={uiText("ホストID")} htmlFor={`${formID}-host-${index}-id`}>
                        <Input id={`${formID}-host-${index}-id`} value={host.host_id} onChange={(event) => updateHost(index, { host_id: event.target.value })} disabled={!canEdit} placeholder="host-main" />
                      </Field>
                      <Field label={uiText("表示名")} htmlFor={`${formID}-host-${index}-name`}>
                        <Input id={`${formID}-host-${index}-name`} value={host.name} onChange={(event) => updateHost(index, { name: event.target.value })} disabled={!canEdit} placeholder={uiText("配信サーバー1")} />
                      </Field>
                      <Field label={uiText("IPアドレス / ホスト名")} htmlFor={`${formID}-host-${index}-address`}>
                        <Input id={`${formID}-host-${index}-address`} value={host.address} onChange={(event) => updateHost(index, { address: event.target.value })} disabled={!canEdit} placeholder="192.0.2.10" />
                      </Field>
                      <Field label={uiText("SSHポート")} htmlFor={`${formID}-host-${index}-port`}>
                        <Input id={`${formID}-host-${index}-port`} type="number" min={1} max={65535} inputMode="numeric" value={host.port} onChange={(event) => updateHost(index, { port: Number(event.target.value) })} disabled={!canEdit} />
                      </Field>
                      <Field
                        label={uiText("SSHユーザー")}
                        htmlFor={`${formID}-host-${index}-user`}
                        hint={uiText("自動bootstrapでは autostream-update-host 固定です。一時管理者ユーザーとは別です。")}
                      >
                        <Input id={`${formID}-host-${index}-user`} value={host.user} onChange={(event) => updateHost(index, { user: event.target.value })} disabled={!canEdit} placeholder="autostream-update-host" />
                      </Field>
                      <Field label={uiText("CPUアーキテクチャ")} htmlFor={`${formID}-host-${index}-arch`}>
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
                      label={uiText("確認済みSSHホスト公開鍵（OpenSSH形式・全文）")}
                      htmlFor={`${formID}-host-${index}-public-key`}
                      hint={host.host_key_fingerprint || host.host_public_key_fingerprint ? uiText("確認結果: {0}", host.host_key_fingerprint || host.host_public_key_fingerprint) : uiText("例: ssh-ed25519 AAAA... server-name")}
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
                      <div className="flex items-center gap-2 text-sm font-medium"><KeyRound className="size-4" />{uiText("対象ホストへ登録する独立Updater公開鍵")}</div>
                      <p className="text-xs text-muted-foreground">{uiText("設定保存後に独立Updaterが生成し、bootstrap時に対象ホストへ登録します。")}</p>
                      {clientPublicKey ? (
                        <div className="flex items-start gap-2">
                          <Textarea readOnly value={clientPublicKey} rows={2} aria-label={uiText("{0} 用Updater SSHクライアント公開鍵", host.name || host.host_id)} className="font-mono text-xs" />
                          <Button type="button" variant="outline" size="icon-sm" onClick={() => void copyClientPublicKey(host.host_id, clientPublicKey)} aria-label={uiText("{0} 用公開鍵をコピー", host.name || host.host_id)}>
                            {copiedHostID === host.host_id ? <Check className="size-4" /> : <Copy className="size-4" />}
                          </Button>
                        </div>
                      ) : (
                        <div className="text-xs text-amber-700 dark:text-amber-300">{uiText("まだ生成されていません。設定を保存し、独立Updaterが反映するまでお待ちください。")}</div>
                      )}
                      {clientKeyFingerprint ? <div className="break-all text-xs text-muted-foreground">Fingerprint: {clientKeyFingerprint}</div> : null}
                    </div>
                  </div>
                );
              })}
            </div>
          )}

          {bootstrapPanel}
        </section>
  );
}
