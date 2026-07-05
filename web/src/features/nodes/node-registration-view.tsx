"use client";

import { useMemo, useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import type { ColumnDef } from "@tanstack/react-table";
import { AlertCircle, Check, Copy, FileCode2, KeyRound, LockKeyhole, RotateCw, Server } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Checkbox } from "@/components/ui/checkbox";
import { Input } from "@/components/ui/input";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Textarea } from "@/components/ui/textarea";
import { DataTable } from "@/components/tables/data-table";
import { StatusBadge } from "@/components/admin/status-badge";
import { APIError, apiPost } from "@/lib/api/client";
import { hasAnyPermission } from "@/lib/auth/permissions";
import { useCurrentUser, useServiceHealth } from "@/features/queries";
import { useI18n } from "@/components/admin/i18n-provider";
import type { NodeRegistrationResponse, WorkerNode } from "@/types/domain";

const nodeTypes = [
  { value: "worker", label: "Worker Node Agent", defaultPort: 8081 },
  { value: "encoder_recorder", label: "Encoder / Recorder Node Agent", defaultPort: 8082 },
  { value: "discord_bot", label: "Discord Bot Node Agent", defaultPort: 8083 },
  { value: "observability", label: "Observability Node Agent", defaultPort: 8084 },
];

export function NodeRegistrationView() {
  const { t } = useI18n();
  const currentUser = useCurrentUser();
  const registeredNodes = useServiceHealth();
  const queryClient = useQueryClient();
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
    onSuccess: async () => {
      await Promise.all([queryClient.invalidateQueries({ queryKey: ["service-health"] }), queryClient.invalidateQueries({ queryKey: ["workers"] })]);
    },
  });
  const createError = nodeRegistrationErrorMessage(createToken.error);
  const registeredRows = registeredNodes.data || [];

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

  const registeredColumns: ColumnDef<WorkerNode>[] = [
    {
      accessorKey: "service_name",
      header: t("name"),
      cell: ({ row }) => (
        <div className="min-w-56">
          <div className="font-medium">{row.original.service_name}</div>
          <div className="text-xs text-muted-foreground">{row.original.service_id || row.original.id}</div>
        </div>
      ),
    },
    {
      accessorKey: "service_type",
      header: t("nodeType"),
      cell: ({ row }) => nodeTypeLabel(row.original.service_type),
    },
    {
      id: "endpoint",
      header: "Node Agent API",
      cell: ({ row }) => <span className="break-all text-sm">{nodeEndpoint(row.original)}</span>,
    },
    {
      accessorKey: "status",
      header: t("status"),
      cell: ({ row }) => <StatusBadge status={row.original.health_status || row.original.status} showDetail />,
    },
    {
      id: "registration",
      header: "登録状態",
      cell: ({ row }) => (
        <div className="text-sm">
          <div>{row.original.last_heartbeat_at ? "接続済み" : "接続待ち"}</div>
          <div className="text-xs text-muted-foreground">{row.original.configure_token_used_at ? "Configure済み" : "Configure未実行"}</div>
        </div>
      ),
    },
    {
      id: "reported",
      header: "報告情報",
      cell: ({ row }) => (
        <div className="text-sm">
          <div>Version {row.original.reported_version || row.original.version || "-"}</div>
          <div className="text-xs text-muted-foreground">
            {row.original.reported_os || "OS未取得"} / {row.original.reported_arch || "Arch未取得"}
          </div>
        </div>
      ),
    },
    {
      id: "heartbeat",
      header: "Heartbeat",
      cell: ({ row }) => formatHeartbeat(row.original),
    },
  ];

  return (
    <div className="space-y-4">
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
            {createToken.isPending ? "Node設定を発行中..." : "Nodeを作成して設定を発行"}
          </Button>
          {!allowed ? <p className="text-sm text-red-600">{t("roleLimited")}</p> : null}
          {createError ? (
            <div className="flex gap-2 rounded-md border border-red-200 bg-red-50 p-3 text-sm text-red-700" role="alert" aria-live="polite">
              <AlertCircle className="mt-0.5 size-4 shrink-0" />
              <div>
                <div className="font-medium">Node設定を発行できませんでした</div>
                <div className="mt-1">{createError}</div>
              </div>
            </div>
          ) : null}
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

      <Card>
        <CardHeader>
          <div className="flex flex-col gap-3 sm:flex-row sm:items-start sm:justify-between">
            <div>
              <CardTitle>登録済みNode</CardTitle>
              <CardDescription>作成済みNode、Configure実行状況、最終Heartbeatを確認できます。</CardDescription>
            </div>
            <Button variant="outline" size="sm" onClick={() => registeredNodes.refetch()} disabled={registeredNodes.isFetching}>
              <RotateCw className="size-4" />
              {registeredNodes.isFetching ? "更新中" : "更新"}
            </Button>
          </div>
        </CardHeader>
        <CardContent className="space-y-3">
          {createToken.data?.node ? (
            <div className="rounded-md border border-emerald-200 bg-emerald-50 p-3 text-sm text-emerald-800" role="status">
              {createToken.data.node.service_name} を登録しました。一覧に表示されない場合は「更新」を押してください。
            </div>
          ) : null}
          {registeredNodes.isError ? (
            <div className="rounded-md border border-red-200 bg-red-50 p-3 text-sm text-red-700" role="alert">
              登録済みNodeを取得できませんでした。service_health.read 権限とControl Panelのログを確認してください。
            </div>
          ) : null}
          <div className="text-sm text-muted-foreground">登録済み: {registeredRows.length} Node</div>
          <DataTable columns={registeredColumns} data={registeredRows} filterPlaceholder="Node名、Node ID、種別、状態で検索" getRowId={(row) => row.service_id || row.id} />
        </CardContent>
      </Card>
    </div>
  );
}

function nodeTypeLabel(type?: string) {
  return nodeTypes.find((item) => item.value === type)?.label || type || "-";
}

function nodeEndpoint(node: WorkerNode) {
  if (node.host && node.port) {
    return `${node.ssl_enabled ? "https" : "http"}://${node.host}:${node.port}`;
  }
  return node.public_url || "-";
}

function formatHeartbeat(node: WorkerNode) {
  if (typeof node.heartbeat_age_sec === "number") return `${node.heartbeat_age_sec} sec`;
  if (node.last_heartbeat_at) return node.last_heartbeat_at;
  return "-";
}

function nodeRegistrationErrorMessage(error: unknown) {
  if (!error) return "";
  if (error instanceof APIError) {
    const messages: Record<string, string> = {
      csrf_failed: "ログイン状態またはCSRF tokenが古くなっています。ページを再読み込みして、もう一度実行してください。",
      invalid_node_scope: "選択したNode権限の組み合わせが無効です。Runtime SecretsやRemediationのチェックを見直してください。",
      permission_escalation: "現在の権限では、このNodeに必要なscopeを発行できません。管理者権限または必要な個別権限を付与してください。",
      node_already_exists: "同じNode IDが既に存在します。別のNode IDにするか、既存NodeのConfigurationから再発行してください。",
      invalid_node_endpoint: "HostまたはPortが無効です。HostはURL全体ではなくFQDNまたはIPだけを入力してください。",
      node_endpoint_blocked: "Node Agent API URLがControl Panelのoutbound allowlistに入っていません。Control Panel envの AUTOSTREAM_SERVICE_PUBLIC_ALLOWED_HOSTS にこのHost、または *.example.jp のようなwildcardを追加して再起動してください。",
      invalid_node_registration: "Node ID、名前、Host、Portのいずれかが無効です。HostはURL全体ではなくFQDNまたはIPだけを入力し、Control Panelのoutbound allowlistも確認してください。",
      node_type_mismatch: "既存Nodeと異なるNode typeでは発行できません。Node typeとNode IDの組み合わせを確認してください。",
      store_node_runtime_token_failed: "Control Panelのenvに AUTOSTREAM_SECRET_ENCRYPTION_KEY が設定されていない、または暗号化設定が不正です。設定後にControl Panelを再起動してください。",
      create_node_configure_token_failed: "Configure Tokenの保存に失敗しました。database接続とControl Panelのログを確認してください。",
      create_node_registration_token_failed: "Node Runtime Tokenの作成に失敗しました。Control Panelのログを確認してください。",
      precreate_node_failed: "Nodeの作成に失敗しました。database接続とControl Panelのログを確認してください。",
    };
    return messages[error.code || ""] || `API error: ${error.code || error.message} (HTTP ${error.status})`;
  }
  if (error instanceof Error) return error.message;
  return "不明なエラーが発生しました。Control Panelのログを確認してください。";
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
