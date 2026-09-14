
import { useUICopy } from "@/lib/i18n/ui-v2/use-ui-copy";
import { Badge } from "@/components/ui/badge";
import { Input } from "@/components/ui/input";
import { Field } from "./updater-settings-field";


export function UpdaterRuntimeSettingsSection({
  formID,
  executionHostID,
  canEdit,
  pollInterval,
  heartbeatInterval,
  localExecutorPolicySHA256,
  changePollInterval,
  changeHeartbeatInterval,
  changePolicyDigest,
}: {
  formID: string;
  executionHostID: string;
  canEdit: boolean;
  pollInterval: string;
  heartbeatInterval: string;
  localExecutorPolicySHA256: string;
  changePollInterval: (event: { target: { value: string } }) => void;
  changeHeartbeatInterval: (event: { target: { value: string } }) => void;
  changePolicyDigest: (event: { target: { value: string } }) => void
}) {
  const uiText = useUICopy();
  return (
<section className="space-y-3" aria-labelledby={`${formID}-runtime-heading`}>
          <div>
            <h3 id={`${formID}-runtime-heading`} className="font-medium">{uiText("Host Agentの動作")}</h3>
            <p className="text-xs text-muted-foreground">
              {uiText("Host AgentからControl Panelへoutbound HTTPSで接続します。受信APIや管理用ポートは使用しません。")}</p>
          </div>
          <div className="flex flex-wrap gap-2 rounded-md border bg-muted/30 p-3 text-xs">
            <Badge variant="secondary">pull_v2</Badge>
            <span>{uiText("実行ホスト:")}{executionHostID || uiText("未割り当て")}</span>
            <span>{uiText("受信ポート: なし")}</span>
          </div>
          <div className="grid gap-3 sm:grid-cols-3">
            <Field label={uiText("更新確認間隔（秒）")} htmlFor={`${formID}-poll-interval`}>
              <Input
                id={`${formID}-poll-interval`}
                type="number"
                min={5}
                max={3600}
                inputMode="numeric"
                value={pollInterval}
                onChange={changePollInterval}
                disabled={!canEdit}
              />
            </Field>
            <Field
              label={uiText("Heartbeat間隔（秒）")}
              htmlFor={`${formID}-heartbeat-interval`}
              hint={uiText("5〜60秒の範囲で設定してください。")}
            >
              <Input
                id={`${formID}-heartbeat-interval`}
                type="number"
                min={5}
                max={60}
                inputMode="numeric"
                value={heartbeatInterval}
                onChange={changeHeartbeatInterval}
                disabled={!canEdit}
              />
            </Field>
            <Field
              label="Local Executor policy SHA-256"
              htmlFor={`${formID}-executor-policy-sha256`}
              hint={uiText("root所有policyを固定するdigestです。未設定時はobserve結果を信頼せず不明として扱います。")}
            >
              <Input
                id={`${formID}-executor-policy-sha256`}
                value={localExecutorPolicySHA256}
                onChange={changePolicyDigest}
                disabled={!canEdit}
                placeholder="sha256:..."
                spellCheck={false}
                className="font-mono text-xs"
              />
            </Field>
          </div>
        </section>
  );
}
