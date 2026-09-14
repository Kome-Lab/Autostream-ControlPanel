
import { useUICopy } from "@/lib/i18n/ui-v2/use-ui-copy";
import { Badge } from "@/components/ui/badge";
import { Input } from "@/components/ui/input";
import { Field } from "./updater-settings-field";



export function UpdaterReleaseTokenSection({
  formID,
  tokenConfigured,
  tokenFingerprint,
  canManageSecrets,
  canEdit,
  githubToken,
  deleteGitHubToken,
  setGithubToken,
  setDeleteGitHubToken,
}: {
  formID: string;
  tokenConfigured: boolean;
  tokenFingerprint: string | undefined;
  canManageSecrets: boolean;
  canEdit: boolean;
  githubToken: string;
  deleteGitHubToken: boolean;
  setGithubToken: (value: string) => void;
  setDeleteGitHubToken: (value: boolean) => void
}) {
  const uiText = useUICopy();
  return (
<section className="space-y-3" aria-labelledby={`${formID}-github-heading`}>
          <div>
            <h3 id={`${formID}-github-heading`} className="font-medium">GitHub Release Token</h3>
            <p className="text-xs text-muted-foreground">
              {uiText("bootstrap artifact取得用TokenはControl Panelの暗号化secretにだけ保存され、値は再表示されません。独立Updaterがbootstrap jobをclaimした時だけ一回限りで渡し、Host Agentのpolicyや応答には含めません。")}</p>
          </div>
          <div className="flex flex-wrap items-center gap-2 text-xs">
            <Badge variant={tokenConfigured ? "default" : "outline"}>{tokenConfigured ? uiText("設定済み") : uiText("未設定")}</Badge>
            {tokenFingerprint ? <span className="text-muted-foreground">Fingerprint: {tokenFingerprint}</span> : null}
          </div>
          {canManageSecrets && canEdit ? (
            <div className="grid gap-3 sm:grid-cols-2">
              <Field label={uiText("新しいGitHub Release Token")} htmlFor={`${formID}-github-token`} hint={uiText("空欄のまま保存すると現在のTokenを維持")}>
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
              {tokenConfigured ? (
                <label className="flex items-center gap-2 self-end rounded-md border p-3 text-sm">
                  <input
                    type="checkbox"
                    checked={deleteGitHubToken}
                    onChange={(event) => {
                      setDeleteGitHubToken(event.target.checked);
                      if (event.target.checked) setGithubToken("");
                    }}
                  />
                  {uiText("登録済みTokenを削除する")}</label>
              ) : null}
            </div>
          ) : (
            <p className="text-xs text-muted-foreground">{uiText("Tokenの登録・変更には system_updates.execute と secrets.update の両方の権限が必要です。")}</p>
          )}
        </section>
  );
}
