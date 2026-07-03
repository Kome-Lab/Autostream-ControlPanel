"use client";

import { useMemo, useState } from "react";
import { useMutation } from "@tanstack/react-query";
import { Check, Copy, FileCode2, KeyRound, LockKeyhole, RotateCw, Server } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Checkbox } from "@/components/ui/checkbox";
import { Input } from "@/components/ui/input";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Textarea } from "@/components/ui/textarea";
import { apiPost } from "@/lib/api/client";
import { hasAnyPermission } from "@/lib/auth/permissions";
import { useCurrentUser } from "@/features/queries";
import { useI18n } from "@/components/admin/i18n-provider";
import type { NodeRegistrationResponse } from "@/types/domain";

const nodeTypes = [
  { value: "worker", label: "Worker Node Agent", defaultPort: 8081 },
  { value: "encoder_recorder", label: "Encoder / Recorder Node Agent", defaultPort: 8082 },
  { value: "discord_bot", label: "Discord Bot Node Agent", defaultPort: 8083 },
  { value: "observability", label: "Observability Node Agent", defaultPort: 8084 },
];

export function NodeRegistrationView() {
  const { t } = useI18n();
  const currentUser = useCurrentUser();
  const [nodeType, setNodeType] = useState("worker");
  const selectedType = nodeTypes.find((type) => type.value === nodeType) ?? nodeTypes[0];
  const [nodeID, setNodeID] = useState("worker-tokyo-01");
  const [name, setName] = useState("東京本社 Worker 01");
  const [host, setHost] = useState("worker-tokyo-01.example.jp");
  const [port, setPort] = useState(String(selectedType.defaultPort));
  const [sslEnabled, setSslEnabled] = useState(true);
  const [description, setDescription] = useState("番組配信と録画を担当する東京本社のNode Agent");
  const [allowRuntimeSecrets, setAllowRuntimeSecrets] = useState(false);
  const [allowRemediation, setAllowRemediation] = useState(false);
  const [copied, setCopied] = useState("");

  const allowed = hasAnyPermission(currentUser.data, ["api_tokens.create"]);
  const nodeApiUrl = useMemo(() => {
    const scheme = sslEnabled ? "https" : "http";
    const normalizedHost = host.trim();
    const normalizedPort = Number.parseInt(port, 10);
    if (!normalizedHost || !Number.isFinite(normalizedPort) || normalizedPort <= 0) return "";
    return `${scheme}://${normalizedHost}:${normalizedPort}`;
  }, [host, port, sslEnabled]);

  const createToken = useMutation({
    mutationFn: () =>
      apiPost<NodeRegistrationResponse>("/nodes/registration-tokens", {
        node_type: nodeType,
        node_id: nodeID,
        name,
        description,
        host,
        port: Number.parseInt(port, 10),
        ssl_enabled: sslEnabled,
        allow_runtime_secrets: allowRuntimeSecrets,
        allow_remediation: allowRemediation,
      }),
  });

  const handleTypeChange = (value: string) => {
    setNodeType(value);
    const nextType = nodeTypes.find((type) => type.value === value);
    if (nextType) {
      setPort(String(nextType.defaultPort));
    }
  };

  const copyValue = async (key: string, value?: string) => {
    if (!value) return;
    await navigator.clipboard.writeText(value);
    setCopied(key);
    window.setTimeout(() => setCopied(""), 1200);
  };

  return (
    <div className="grid gap-4 xl:grid-cols-[minmax(360px,0.9fr)_minmax(0,1.1fr)]">
      <Card className="min-w-0">
        <CardHeader>
          <CardTitle className="flex items-center gap-2">
            <Server className="size-5" />
            {t("nodeRegistration")}
          </CardTitle>
          <CardDescription>PanelでNodeを作成し、Node Agentへ配置する設定ファイルを発行します。</CardDescription>
        </CardHeader>
        <CardContent className="space-y-4">
          <div className="grid gap-2">
            <label className="text-sm font-medium">{t("nodeType")}</label>
            <Select value={nodeType} onValueChange={handleTypeChange}>
              <SelectTrigger>
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {nodeTypes.map((type) => (
                  <SelectItem key={type.value} value={type.value}>
                    {type.label}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>
          <div className="grid gap-2">
            <label className="text-sm font-medium">{t("nodeId")}</label>
            <Input value={nodeID} onChange={(event) => setNodeID(event.target.value)} />
          </div>
          <div className="grid gap-2">
            <label className="text-sm font-medium">{t("name")}</label>
            <Input value={name} onChange={(event) => setName(event.target.value)} />
          </div>
          <div className="grid gap-2 sm:grid-cols-[minmax(0,1fr)_120px]">
            <div className="grid gap-2">
              <label className="text-sm font-medium">Host / FQDN / IP</label>
              <Input value={host} onChange={(event) => setHost(event.target.value)} />
            </div>
            <div className="grid gap-2">
              <label className="text-sm font-medium">Port</label>
              <Input inputMode="numeric" value={port} onChange={(event) => setPort(event.target.value)} />
            </div>
          </div>
          <label className="flex items-center gap-2 text-sm">
            <Checkbox checked={sslEnabled} onCheckedChange={(value) => setSslEnabled(value === true)} />
            SSLを有効化してHTTPSを使用
          </label>
          <div className="rounded-md border bg-muted/40 p-3 text-sm">
            <div className="font-medium">Node Agent API URL</div>
            <div className="mt-1 break-all text-muted-foreground">{nodeApiUrl || "HostとPortを入力してください"}</div>
          </div>
          <div className="grid gap-2">
            <label className="text-sm font-medium">説明</label>
            <Textarea value={description} onChange={(event) => setDescription(event.target.value)} rows={3} />
          </div>
          <div className="grid gap-2 rounded-md border bg-muted/30 p-3 text-sm">
            <div className="font-medium">Node Agentが自動報告する項目</div>
            <div className="text-muted-foreground">バージョン、Capability、OS、Architecture、メトリクスは登録時に入力しません。</div>
          </div>
          <label className="flex items-center gap-2 text-sm">
            <Checkbox checked={allowRuntimeSecrets} onCheckedChange={(value) => setAllowRuntimeSecrets(value === true)} />
            {t("runtimeSecrets")}
          </label>
          <label className="flex items-center gap-2 text-sm">
            <Checkbox checked={allowRemediation} onCheckedChange={(value) => setAllowRemediation(value === true)} />
            {t("remediation")}
          </label>
          <Button className="w-full" disabled={!allowed || createToken.isPending} onClick={() => createToken.mutate()}>
            <KeyRound className="size-4" />
            Nodeを作成して設定を発行
          </Button>
          {!allowed ? <p className="text-sm text-red-600">{t("roleLimited")}</p> : null}
        </CardContent>
      </Card>

      <Card className="min-w-0">
        <CardHeader>
          <CardTitle className="flex items-center gap-2">
            <FileCode2 className="size-5" />
            Configuration
          </CardTitle>
          <CardDescription>Configure TokenとRuntime Tokenは生成直後だけ表示されます。</CardDescription>
        </CardHeader>
        <CardContent className="space-y-4">
          {createToken.data ? (
            <>
              <div className="grid gap-2 rounded-md border bg-muted/40 p-3 text-sm">
                <div className="font-medium">接続状態</div>
                <div className="text-muted-foreground">
                  {createToken.data.node?.status ?? "pending"} / 報告バージョン: {createToken.data.node?.reported_version || "未取得"} / Capability:{" "}
                  {Object.keys(createToken.data.node?.reported_capabilities ?? {}).length > 0 ? "報告済み" : "未取得"}
                </div>
              </div>
              <SecretBlock
                label="Configure Token"
                value={createToken.data.configure_token ?? createToken.data.token}
                copied={copied === "configure-token"}
                onCopy={() => copyValue("configure-token", createToken.data?.configure_token ?? createToken.data?.token)}
              />
              {createToken.data.runtime_token ? (
                <SecretBlock
                  label="Node Runtime Token"
                  value={createToken.data.runtime_token}
                  copied={copied === "runtime-token"}
                  onCopy={() => copyValue("runtime-token", createToken.data?.runtime_token)}
                />
              ) : null}
              <SecretBlock
                label={t("configureCommand")}
                value={createToken.data.configure_command}
                copied={copied === "command"}
                onCopy={() => copyValue("command", createToken.data?.configure_command)}
              />
              {createToken.data.configuration_yaml ? (
                <SecretBlock
                  label="config.yml"
                  value={createToken.data.configuration_yaml}
                  copied={copied === "yaml"}
                  onCopy={() => copyValue("yaml", createToken.data?.configuration_yaml)}
                />
              ) : null}
              {createToken.data.systemd_unit ? (
                <SecretBlock
                  label="systemd"
                  value={createToken.data.systemd_unit}
                  copied={copied === "systemd"}
                  onCopy={() => copyValue("systemd", createToken.data?.systemd_unit)}
                />
              ) : null}
              <div className="rounded-md border bg-muted/40 p-3 text-sm">
                <div className="font-medium">Scopes</div>
                <div className="mt-2 flex flex-wrap gap-2">
                  {createToken.data.scopes.map((scope) => (
                    <span key={scope} className="rounded-md bg-background px-2 py-1 text-xs">
                      {scope}
                    </span>
                  ))}
                </div>
              </div>
            </>
          ) : (
            <div className="rounded-md border border-dashed p-8 text-center text-sm text-muted-foreground">
              Nodeを作成すると、config.yml、Auto Configureコマンド、Tokenがここに一度だけ表示されます。
            </div>
          )}
          <div className="grid gap-2 rounded-md border bg-muted/30 p-3 text-sm">
            <div className="flex items-center gap-2 font-medium">
              <LockKeyhole className="size-4" />
              Token運用
            </div>
            <div className="text-muted-foreground">
              Configure Tokenは設定取得用、Node Runtime TokenはPanelとNode Agent間の通常通信認証用です。再表示できない場合は再生成してください。
            </div>
            <div className="flex items-center gap-2 text-muted-foreground">
              <RotateCw className="size-4" />
              ConfigurationタブからConfigure TokenとRuntime Tokenを再生成します。
            </div>
          </div>
        </CardContent>
      </Card>
    </div>
  );
}

function SecretBlock({ label, value, copied, onCopy }: { label: string; value: string; copied: boolean; onCopy: () => void }) {
  return (
    <div className="space-y-2">
      <div className="flex items-center justify-between gap-2">
        <label className="text-sm font-medium">{label}</label>
        <Button variant="outline" size="sm" onClick={onCopy}>
          {copied ? <Check className="size-4" /> : <Copy className="size-4" />}
          {copied ? "コピー済み" : "コピー"}
        </Button>
      </div>
      <pre className="max-h-56 overflow-auto whitespace-pre-wrap break-all rounded-md border bg-muted p-3 text-xs leading-relaxed">{value}</pre>
    </div>
  );
}
