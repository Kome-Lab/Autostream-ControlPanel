
import { useUICopy } from "@/lib/i18n/ui-v2/use-ui-copy";
import { FileCode2, LockKeyhole, RotateCw } from "lucide-react";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { statusDescriptor } from "@/components/admin/status-badge";
import { useI18n } from "@/components/admin/i18n-provider";
import { type NodeConfigurationResponse } from "./node-registration-model";
import { NodeEndpointStateView } from "./node-endpoint-state-view";
import { formatNodeDateTime } from "./node-metrics-summary";
import { SecretBlock } from "./node-configuration-secret-block";


export function NodeConfigurationCard({
  configuration,
  configurationIsHostAgent,
  updaterConfigureCommandAvailable,
  updaterConfigureTokenRequired,
  timezone,
  copied,
  copyValue,
  t,
}: {
  configuration: NodeConfigurationResponse | null;
  configurationIsHostAgent: boolean;
  updaterConfigureCommandAvailable: boolean;
  updaterConfigureTokenRequired: boolean;
  timezone: string | undefined;
  copied: string;
  copyValue: (key: string, value?: string) => Promise<void>;
  t: ReturnType<typeof useI18n>["t"]
}) {
  const uiText = useUICopy();
  return (
<Card className="min-w-0">
        <CardHeader>
          <CardTitle className="flex items-center gap-2">
            <FileCode2 className="size-5" />
            Configuration
          </CardTitle>
          <CardDescription>{uiText("Node種別に応じた設定ファイルと、生成直後だけ表示されるTokenを安全にNodeへ反映してください。")}</CardDescription>
        </CardHeader>
        <CardContent className="space-y-4">
          {configuration ? (
            <>
              <div className="grid gap-2 rounded-md border bg-muted/40 p-3 text-sm">
                <div className="font-medium">{uiText("接続状態")}</div>
                <div className="text-muted-foreground">
                  {configuration.node?.service_name || uiText("選択中のNode")} / {statusDescriptor(configuration.node?.status).label} {uiText("/ 報告バージョン:")}{" "}
                  {configuration.node?.reported_version || uiText("未取得")} / Capability: {Object.keys(configuration.node?.reported_capabilities ?? {}).length > 0 ? uiText("報告済み") : uiText("未取得")}
                </div>
                {configuration.configure_token_expires_at ? <div className="text-xs text-muted-foreground">{uiText("Configure Token期限:")}{formatNodeDateTime(configuration.configure_token_expires_at, timezone)}</div> : null}
              </div>
              {configuration.node?.service_type === "discord_bot" ? (
                <div className="space-y-2 rounded-md border border-amber-500/30 bg-amber-500/10 p-3 text-sm">
                  <div className="font-medium">{uiText("Discord VC空室時の自動停止を有効化")}</div>
                  <p className="text-muted-foreground">
                    {uiText("既存のDiscord Botでは、ここでConfigure Tokenを再生成し、表示された設定をBotホストへ適用してからサービスを再起動してください。 この権限追加の対象になる、更新前に発行した未使用のConfigure Tokenは使えないため、再発行が必要です。実行完了時にだけ、既存の")}<code>streams.start</code> {uiText("権限へ対応する")}<code>streams.stop</code> {uiText("権限が新しいNode Runtime Tokenへ追加されます。Tokenを発行しただけでは切り替わりません。")}</p>
                </div>
              ) : null}
              {configuration.node ? (
                <NodeEndpointStateView
                  node={configuration.node}
                  copied={copied}
                  onCopy={copyValue}
                />
              ) : null}
              {configuration.node?.service_type !== "update_agent" && configuration.node_api_url ? (
                <SecretBlock label={uiText("Applied Node Agent API URL（後方互換）")} value={configuration.node_api_url} copied={copied === "api-url"} onCopy={() => copyValue("api-url", configuration.node_api_url)} />
              ) : null}
              {configuration.configure_token || configuration.token ? (
                <SecretBlock
                  label="Configure Token"
                  value={configuration.configure_token ?? configuration.token ?? ""}
                  copied={copied === "configure-token"}
                  onCopy={() => copyValue("configure-token", configuration.configure_token ?? configuration.token)}
                />
              ) : null}
              {configuration.runtime_token ? (
                <SecretBlock
                  label="Node Runtime Token"
                  value={configuration.runtime_token}
                  copied={copied === "runtime-token"}
                  onCopy={() => copyValue("runtime-token", configuration.runtime_token)}
                />
              ) : null}
              {updaterConfigureCommandAvailable ? (
                <div className="space-y-2 rounded-md border border-blue-500/30 bg-blue-500/10 p-4 text-sm">
                  <div className="font-medium">{configurationIsHostAgent ? uiText("Host Agentの初期設定") : uiText("Node Agentの初期設定")}</div>
                  <p className="text-muted-foreground">
                    {configurationIsHostAgent
                      ? uiText("表示された手順を、このHost Agentを稼働させる対象ホストで1回実行すると、Control Panelへの外向き接続を初期設定します。受信API endpointや専用portは作成しません。")
                      : uiText("表示されたコマンドを対象ホストで1回実行すると、Panel接続情報を安全に初期設定します。")}
                    {uiText("コマンド自体にConfigure Tokenは含まれず、実行時にTTYまたは標準入力から読み取ります。 設定処理が失敗または結果不確定の場合は、対象サービスを再起動しないでください。Configurationで新しいConfigure Tokenを発行し、同じtoken-free commandを新しいTokenで再実行してください。")}</p>
                </div>
              ) : null}
              {updaterConfigureTokenRequired ? (
                <div className="space-y-2 rounded-md border border-blue-500/30 bg-blue-500/10 p-4 text-sm">
                  <div className="font-medium">{uiText("Configure Tokenを再生成してください")}</div>
                  <p className="text-muted-foreground">
                    {uiText("Configure Tokenと実行手順は再表示されません。一覧の鍵ボタンから新しいTokenを発行すると、この画面に対象サービスの初期設定手順が一度だけ表示されます。")}</p>
                </div>
              ) : null}
              {configuration.configure_command ? (
                <SecretBlock
                  label={t("configureCommand")}
                  value={configuration.configure_command}
                  copied={copied === "command"}
                  onCopy={() => copyValue("command", configuration.configure_command)}
                />
              ) : null}
              {configuration.configuration_yaml ? (
                <SecretBlock
                  label="config.yml"
                  value={configuration.configuration_yaml}
                  copied={copied === "yaml"}
                  onCopy={() => copyValue("yaml", configuration.configuration_yaml)}
                />
              ) : null}
              {configuration.systemd_unit ? (
                <SecretBlock
                  label="systemd"
                  value={configuration.systemd_unit}
                  copied={copied === "systemd"}
                  onCopy={() => copyValue("systemd", configuration.systemd_unit)}
                />
              ) : null}
              {configuration.scopes?.length ? (
                <div className="rounded-md border bg-muted/40 p-3 text-sm">
                  <div className="font-medium">Scopes</div>
                  <div className="mt-2 flex flex-wrap gap-2">
                    {configuration.scopes.map((scope) => (
                      <span key={scope} className="rounded-md bg-background px-2 py-1 text-xs">
                        {scope}
                      </span>
                    ))}
                  </div>
                </div>
              ) : null}
            </>
          ) : (
            <div className="rounded-md border border-dashed p-8 text-center text-sm text-muted-foreground">
              {uiText("Nodeを作成、または登録済みNodeのConfiguration・Token再生成を実行するとここに表示されます。")}</div>
          )}
          <div className="grid gap-2 rounded-md border bg-muted/30 p-3 text-sm">
            <div className="flex items-center gap-2 font-medium">
              <LockKeyhole className="size-4" />
              {uiText("Token運用")}</div>
            <div className="text-muted-foreground">
              {uiText("Configure Tokenは1回限りの初期設定用、Node Runtime TokenはPanelとNode Agent間の通常通信認証用です。Host Agentの実行対象とpolicyは「アプリケーション情報」で設定します。")}</div>
            <div className="flex items-center gap-2 text-muted-foreground">
              <RotateCw className="size-4" />
              {uiText("Updater / Host AgentのPanel接続情報を更新する場合はConfigure Tokenを再生成し、表示された手順を対象サービスを稼働させるホストで実行します。")}</div>
          </div>
        </CardContent>
        </Card>
  );
}
