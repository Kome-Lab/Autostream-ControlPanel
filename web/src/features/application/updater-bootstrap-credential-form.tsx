
import { useUICopy } from "@/lib/i18n/ui-v2/use-ui-copy";
import { KeyRound, LoaderCircle, ServerCog, ShieldCheck } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Textarea } from "@/components/ui/textarea";
import { UpdaterActionConfirmation } from "@/features/application/updater-action-confirmation";
import type { UpdaterSettingsHost } from "@/types/domain";



export function BootstrapCredentialForm({
  selectedHostCount,
  encryptionKeyFingerprint,
  selectedHosts,
  formID,
  administratorUser,
  privateKey,
  passphrase,
  setAdministratorUser,
  setPrivateKey,
  setPassphrase,
  busy,
  canEdit,
  hostKeysConfirmed,
  setConfirmedContext,
  confirmationContext,
  closeCredentialForm,
  confirmation,
  submitDisabled,
}: {
  selectedHostCount: number;
  encryptionKeyFingerprint: string | undefined;
  selectedHosts: UpdaterSettingsHost[];
  formID: string;
  administratorUser: string;
  privateKey: string;
  passphrase: string;
  setAdministratorUser: (value: string) => void;
  setPrivateKey: (value: string) => void;
  setPassphrase: (value: string) => void;
  busy: boolean;
  canEdit: boolean;
  hostKeysConfirmed: boolean;
  setConfirmedContext: (value: string) => void;
  confirmationContext: string;
  closeCredentialForm: () => void;
  confirmation: Pick<Parameters<typeof UpdaterActionConfirmation>[0], "controller" | "intent" | "authority" | "refreshAuthority" | "handler">;
  submitDisabled: boolean
}) {
  const uiText = useUICopy();
  return (
<div className="space-y-4 rounded-md border bg-background p-4">
          <div>
            <div className="font-medium">{selectedHostCount === 1 ? uiText("ホストをセットアップ") : uiText("{0}台を一括セットアップ", selectedHostCount)}</div>
            <p className="mt-1 text-xs text-muted-foreground">
              {uiText("秘密鍵とパスフレーズは今回のセットアップだけに使用し、保存・再表示しません。選択した全ホストで同じ認証情報を使用します。")}</p>
          </div>

          <div className="rounded-md border border-amber-300 bg-amber-50 p-3 text-xs leading-5 text-amber-950 dark:border-amber-900 dark:bg-amber-950/30 dark:text-amber-100">
            <div className="flex items-center gap-2 font-medium"><ShieldCheck className="size-4" />{uiText("接続鍵の確認")}</div>
            <div className="mt-1 break-all">
              {uiText("Updater暗号鍵:")}{encryptionKeyFingerprint || uiText("Fingerprint未報告")}
            </div>
            {selectedHosts.map((host) => (
              <div key={host.host_id} className="mt-1 break-all">
                {host.name || host.host_id}: {host.host_key_fingerprint || host.host_public_key_fingerprint || uiText("Fingerprint未報告")}
              </div>
            ))}
          </div>

          <div className="grid gap-3 sm:grid-cols-2">
            <label className="space-y-1.5 text-sm" htmlFor={`${formID}-administrator-user`}>
              <span className="font-medium">{uiText("一時管理者SSHユーザー")}</span>
              <Input
                id={`${formID}-administrator-user`}
                value={administratorUser}
                onChange={(event) => setAdministratorUser(event.target.value)}
                autoComplete="off"
                placeholder="deploy"
                disabled={busy || !canEdit}
              />
              <span className="block text-xs text-muted-foreground">{uiText("rootではなく、パスワード入力なしでsudoを実行できる既存ユーザーを指定します。")}</span>
            </label>
            <label className="space-y-1.5 text-sm" htmlFor={`${formID}-passphrase`}>
              <span className="font-medium">{uiText("秘密鍵パスフレーズ（任意）")}</span>
              <Input
                id={`${formID}-passphrase`}
                type="password"
                value={passphrase}
                onChange={(event) => setPassphrase(event.target.value)}
                autoComplete="off"
                disabled={busy || !canEdit}
              />
            </label>
          </div>

          <label className="space-y-1.5 text-sm" htmlFor={`${formID}-private-key`}>
            <span className="flex items-center gap-2 font-medium"><KeyRound className="size-4" />{uiText("一時SSH秘密鍵")}</span>
            <Textarea
              id={`${formID}-private-key`}
              value={privateKey}
              onChange={(event) => setPrivateKey(event.target.value)}
              autoComplete="off"
              autoCapitalize="none"
              spellCheck={false}
              rows={6}
              className="font-mono text-xs"
              placeholder="-----BEGIN OPENSSH PRIVATE KEY-----"
              disabled={busy || !canEdit}
            />
          </label>

          <label className="flex items-start gap-2 rounded-md border p-3 text-sm">
            <input
              type="checkbox"
              checked={hostKeysConfirmed}
              onChange={(event) => setConfirmedContext(event.target.checked ? confirmationContext : "")}
              disabled={busy || !canEdit}
              className="mt-0.5"
            />
            <span>{uiText("表示されたUpdater暗号鍵と各ホストのSSHホスト鍵Fingerprintを、独立した安全な経路で確認しました。")}</span>
          </label>

          <div className="flex flex-wrap justify-end gap-2">
            <Button type="button" variant="outline" onClick={closeCredentialForm} disabled={busy}>{uiText("キャンセル")}</Button>
            <UpdaterActionConfirmation
              controller={confirmation.controller}
              intent={confirmation.intent}
              authority={confirmation.authority}
              refreshAuthority={confirmation.refreshAuthority}
              handler={confirmation.handler}
              label={uiText("セットアップを開始")}
              icon={busy ? <LoaderCircle className="size-4 animate-spin" /> : <ServerCog className="size-4" />}
              size="default"
              disabled={
                submitDisabled
              }
            />
          </div>
        </div>
  );
}
