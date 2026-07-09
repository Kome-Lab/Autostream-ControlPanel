"use client";

import { useMemo, useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import type { ColumnDef } from "@tanstack/react-table";
import { Activity, AlertCircle, Check, Copy, FileCode2, KeyRound, Link, LockKeyhole, Pencil, RotateCw, Server, Trash2 } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Checkbox } from "@/components/ui/checkbox";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Textarea } from "@/components/ui/textarea";
import { DataTable } from "@/components/tables/data-table";
import { DangerConfirm } from "@/components/admin/danger-confirm";
import { RoleGuard, guardedButtonProps } from "@/components/admin/role-guard";
import { StatusBadge } from "@/components/admin/status-badge";
import { APIError, apiDelete, apiGet, apiPost, apiPut } from "@/lib/api/client";
import { hasPermission } from "@/lib/auth/permissions";
import { useAppSettings, useCurrentUser, useNodes } from "@/features/queries";
import { useI18n } from "@/components/admin/i18n-provider";
import { formatDateTimeInTimeZone } from "@/lib/timezone";
import type { NodeRegistrationResponse, WorkerNode } from "@/types/domain";

const nodeTypes = [
  { value: "worker", label: "Worker Node Agent", defaultPort: 8081 },
  { value: "encoder_recorder", label: "Encoder / Recorder Node Agent", defaultPort: 8082 },
  { value: "discord_bot", label: "Discord Bot Node Agent", defaultPort: 8083 },
  { value: "observability", label: "Observability Node Agent", defaultPort: 8084 },
];

type NodeConfigurationResponse = {
  node?: WorkerNode;
  node_api_url?: string;
  token?: string;
  configure_token?: string;
  configure_token_expires_at?: string;
  runtime_token_id?: string;
  runtime_token?: string;
  configure_command?: string;
  configuration_yaml?: string;
  systemd_unit?: string;
  scopes?: string[];
};

type NodeEditForm = {
  service_name: string;
  description: string;
  host: string;
  port: string;
  ssl_enabled: boolean;
};

type NodeRegistrationViewMode = "registration" | "registered" | "all";

export function NodeRegistrationView({ mode = "registration" }: { mode?: NodeRegistrationViewMode }) {
  const { t } = useI18n();
  const currentUser = useCurrentUser();
  const appSettings = useAppSettings();
  const registeredNodes = useNodes();
  const queryClient = useQueryClient();
  const timezone = appSettings.data?.timezone;
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
  const [configuration, setConfiguration] = useState<NodeConfigurationResponse | null>(null);
  const [editingNode, setEditingNode] = useState<WorkerNode | null>(null);
  const [editForm, setEditForm] = useState<NodeEditForm>({ service_name: "", description: "", host: "", port: "", ssl_enabled: true });

  const allowed = hasPermission(currentUser.data, "api_tokens.create");
  const canRotateRuntimeToken = hasPermission(currentUser.data, "api_tokens.revoke");
  const canDeleteNode = hasPermission(currentUser.data, "services.disable");
  const nodeApiUrl = useMemo(() => {
    const scheme = sslEnabled ? "https" : "http";
    const normalizedHost = host.trim();
    const normalizedPort = Number.parseInt(port, 10);
    if (!normalizedHost || !Number.isFinite(normalizedPort) || normalizedPort <= 0) return "";
    return `${scheme}://${normalizedHost}:${normalizedPort}`;
  }, [host, port, sslEnabled]);

  const invalidateNodeQueries = async () => {
    await Promise.all([
      queryClient.invalidateQueries({ queryKey: ["nodes"] }),
      queryClient.invalidateQueries({ queryKey: ["service-health"] }),
      queryClient.invalidateQueries({ queryKey: ["workers"] }),
    ]);
  };

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
    onSuccess: async (data) => {
      setConfiguration(data);
      await invalidateNodeQueries();
    },
  });
  const loadConfiguration = useMutation({
    mutationFn: (nodeID: string) => apiGet<NodeConfigurationResponse>(`/nodes/${encodeURIComponent(nodeID)}/configuration`),
    onSuccess: (data) => setConfiguration(data),
  });
  const regenerateConfigureToken = useMutation({
    mutationFn: (nodeID: string) => apiPost<NodeConfigurationResponse>(`/nodes/${encodeURIComponent(nodeID)}/configure-token`),
    onSuccess: async (data) => {
      setConfiguration(data);
      await invalidateNodeQueries();
    },
  });
  const rotateRuntimeToken = useMutation({
    mutationFn: (nodeID: string) => apiPost<NodeConfigurationResponse>(`/nodes/${encodeURIComponent(nodeID)}/rotate-token`),
    onSuccess: async (data) => {
      setConfiguration(data);
      await invalidateNodeQueries();
    },
  });
  const updateNode = useMutation({
    mutationFn: ({ nodeID, values }: { nodeID: string; values: NodeEditForm }) =>
      apiPut<WorkerNode>(`/nodes/${encodeURIComponent(nodeID)}`, {
        service_name: values.service_name,
        description: values.description,
        host: values.host,
        port: Number.parseInt(values.port, 10),
        ssl_enabled: values.ssl_enabled,
      }),
    onSuccess: async (node) => {
      setEditingNode(null);
      setConfiguration((current) => (current?.node && nodeIdentity(current.node) === nodeIdentity(node) ? { ...current, node } : current));
      await invalidateNodeQueries();
    },
  });
  const deleteNode = useMutation({
    mutationFn: (nodeID: string) => apiDelete<{ status: string }>(`/services/${encodeURIComponent(nodeID)}`),
    onSuccess: async (_data, nodeID) => {
      setConfiguration((current) => (current?.node && nodeIdentity(current.node) === nodeID ? null : current));
      await invalidateNodeQueries();
    },
  });
  const createError = nodeRegistrationErrorMessage(createToken.error);
  const actionError = nodeRegistrationErrorMessage(updateNode.error || deleteNode.error || loadConfiguration.error || regenerateConfigureToken.error || rotateRuntimeToken.error);
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

  const openEditNode = (node: WorkerNode) => {
    setEditingNode(node);
    setEditForm(nodeEditDefaults(node));
  };

  const submitEditNode = () => {
    if (!editingNode) return;
    updateNode.mutate({ nodeID: nodeIdentity(editingNode), values: editForm });
  };

  const editPortNumber = Number.parseInt(editForm.port, 10);
  const editFormValid = editForm.service_name.trim() !== "" && editForm.host.trim() !== "" && Number.isFinite(editPortNumber) && editPortNumber > 0 && editPortNumber <= 65535;
  const showRegistration = mode !== "registered";
  const showRegistered = mode !== "registration";

  const registeredColumns: ColumnDef<WorkerNode>[] = [
    {
      accessorKey: "service_name",
      header: t("name"),
      cell: ({ row }) => {
        const nodeID = nodeIdentity(row.original);
        return (
          <div className="min-w-56">
            <div className="flex items-center gap-2">
              <div className="font-medium">{nodeDisplayName(row.original)}</div>
              <Button variant="outline" size="icon-sm" aria-label="Node IDをコピー" onClick={() => copyValue(`node-id-${nodeID}`, nodeID)}>
                {copied === `node-id-${nodeID}` ? <Check className="size-4" /> : <Copy className="size-4" />}
              </Button>
            </div>
          </div>
        );
      },
    },
    {
      accessorKey: "service_type",
      header: t("nodeType"),
      cell: ({ row }) => nodeTypeLabel(row.original.service_type),
    },
    {
      id: "endpoint",
      header: "接続先",
      cell: ({ row }) => {
        const node = row.original;
        const endpoint = nodeEndpoint(node);
        return (
          <div className="flex items-center gap-2 text-sm">
            <span className="text-muted-foreground">{endpoint ? "設定済み" : "未設定"}</span>
            {endpoint ? (
              <Button variant="outline" size="icon-sm" aria-label="Node Agent API URLをコピー" onClick={() => copyValue(`endpoint-${nodeIdentity(node)}`, endpoint)}>
                {copied === `endpoint-${nodeIdentity(node)}` ? <Check className="size-4" /> : <Link className="size-4" />}
              </Button>
            ) : null}
          </div>
        );
      },
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
            {nodeReportedPlatform(row.original)}
          </div>
        </div>
      ),
    },
    {
      id: "metrics",
      header: "Metrics",
      cell: ({ row }) => <NodeMetricsSummary node={row.original} />,
    },
    {
      id: "heartbeat",
      header: "Heartbeat",
      cell: ({ row }) => formatHeartbeat(row.original, timezone),
    },
    {
      id: "actions",
      header: t("actions"),
      cell: ({ row }) => {
        const node = row.original;
        const nodeID = nodeIdentity(node);
        return (
          <div className="flex min-w-52 flex-wrap items-center gap-2">
            <Button variant="outline" size="icon-sm" aria-label="Configurationを表示" onClick={() => loadConfiguration.mutate(nodeID)} disabled={loadConfiguration.isPending}>
              <FileCode2 />
            </Button>
            <Button variant="outline" size="icon-sm" aria-label="Configure Tokenを再生成" onClick={() => regenerateConfigureToken.mutate(nodeID)} disabled={!allowed || regenerateConfigureToken.isPending}>
              <KeyRound />
            </Button>
            <RoleGuard allowed={canRotateRuntimeToken}>
              <DangerConfirm title={`${node.service_name} のRuntime Tokenを再生成しますか`} description="既存のRuntime Tokenは無効になります。Node Agentへ新しいconfig.ymlまたはTokenを反映してください。" onConfirm={() => rotateRuntimeToken.mutate(nodeID)} actionLabel="再生成">
                <Button variant="outline" size="icon-sm" aria-label="Runtime Tokenを再生成" {...guardedButtonProps(canRotateRuntimeToken)} disabled={!canRotateRuntimeToken || rotateRuntimeToken.isPending}>
                  <RotateCw />
                </Button>
              </DangerConfirm>
            </RoleGuard>
            <Button variant="outline" size="icon-sm" aria-label="Nodeを編集" onClick={() => openEditNode(node)} disabled={!allowed}>
              <Pencil />
            </Button>
            <RoleGuard allowed={canDeleteNode}>
              <DangerConfirm title={`${node.service_name} を削除しますか`} description="Node登録、割り当て、Runtime Tokenを無効化します。この操作は取り消せません。" onConfirm={() => deleteNode.mutate(nodeID)} actionLabel="削除">
                <Button variant="destructive" size="icon-sm" aria-label="Nodeを削除" {...guardedButtonProps(canDeleteNode)} disabled={!canDeleteNode || deleteNode.isPending}>
                  <Trash2 />
                </Button>
              </DangerConfirm>
            </RoleGuard>
          </div>
        );
      },
    },
  ];

  return (
    <div className="space-y-4">
      <div className={showRegistration ? "grid gap-4 xl:grid-cols-[minmax(360px,0.9fr)_minmax(0,1.1fr)]" : "grid gap-4"}>
        {showRegistration ? (
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
            <div className="text-muted-foreground">バージョン、OS、ArchitectureはConfigure実行時または起動後のHeartbeatで報告されます。CapabilityとメトリクスはHeartbeatで更新されます。</div>
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
        ) : null}

        <Card className="min-w-0">
        <CardHeader>
          <CardTitle className="flex items-center gap-2">
            <FileCode2 className="size-5" />
            Configuration
          </CardTitle>
          <CardDescription>Configure TokenとRuntime Tokenは生成直後だけ表示されます。</CardDescription>
        </CardHeader>
        <CardContent className="space-y-4">
          {configuration ? (
            <>
              <div className="grid gap-2 rounded-md border bg-muted/40 p-3 text-sm">
                <div className="font-medium">接続状態</div>
                <div className="text-muted-foreground">
                  {configuration.node?.service_name || "選択中のNode"} / {configuration.node?.status ?? "pending"} / 報告バージョン:{" "}
                  {configuration.node?.reported_version || "未取得"} / Capability: {Object.keys(configuration.node?.reported_capabilities ?? {}).length > 0 ? "報告済み" : "未取得"}
                </div>
                {configuration.configure_token_expires_at ? <div className="text-xs text-muted-foreground">Configure Token期限: {formatNodeDateTime(configuration.configure_token_expires_at, timezone)}</div> : null}
              </div>
              {configuration.node_api_url ? <SecretBlock label="Node Agent API URL" value={configuration.node_api_url} copied={copied === "api-url"} onCopy={() => copyValue("api-url", configuration.node_api_url)} /> : null}
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
              Nodeを作成、または登録済みNodeのConfiguration・Token再生成を実行するとここに表示されます。
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

      {showRegistered ? (
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
              登録済みNodeを取得できませんでした。api_tokens.create 権限とControl Panelのログを確認してください。
            </div>
          ) : null}
          {actionError ? (
            <div className="rounded-md border border-red-200 bg-red-50 p-3 text-sm text-red-700" role="alert" aria-live="polite">
              {actionError}
            </div>
          ) : null}
          <div className="text-sm text-muted-foreground">登録済み: {registeredRows.length} Node</div>
          <DataTable columns={registeredColumns} data={registeredRows} filterPlaceholder="Node名、種別、状態で検索" getRowId={(row) => row.service_id || row.id} />
        </CardContent>
      </Card>
      ) : null}
      {showRegistered ? (
      <Dialog open={Boolean(editingNode)} onOpenChange={(open) => (!open ? setEditingNode(null) : undefined)}>
        <DialogContent className="sm:max-w-2xl">
          <DialogHeader>
            <DialogTitle>Nodeを編集</DialogTitle>
            <DialogDescription>Node IDとNode typeは変更できません。接続先を変えた場合は必要に応じてNode Agent側の設定も更新してください。</DialogDescription>
          </DialogHeader>
          <div className="grid gap-4">
            <div className="grid gap-2">
              <label className="text-sm font-medium">Node ID</label>
              <Input value={editingNode ? nodeIdentity(editingNode) : ""} disabled />
            </div>
            <div className="grid gap-2">
              <label className="text-sm font-medium">{t("name")}</label>
              <Input value={editForm.service_name} onChange={(event) => setEditForm((current) => ({ ...current, service_name: event.target.value }))} />
            </div>
            <div className="grid gap-2 sm:grid-cols-[minmax(0,1fr)_120px]">
              <div className="grid gap-2">
                <label className="text-sm font-medium">Host / FQDN / IP</label>
                <Input value={editForm.host} onChange={(event) => setEditForm((current) => ({ ...current, host: event.target.value }))} />
              </div>
              <div className="grid gap-2">
                <label className="text-sm font-medium">Port</label>
                <Input inputMode="numeric" value={editForm.port} onChange={(event) => setEditForm((current) => ({ ...current, port: event.target.value }))} />
              </div>
            </div>
            <label className="flex items-center gap-2 text-sm">
              <Checkbox checked={editForm.ssl_enabled} onCheckedChange={(value) => setEditForm((current) => ({ ...current, ssl_enabled: value === true }))} />
              SSLを有効化してHTTPSを使用
            </label>
            <div className="rounded-md border bg-muted/40 p-3 text-sm">
              <div className="font-medium">Node Agent API URL</div>
              <div className="mt-1 break-all text-muted-foreground">{editNodeApiURL(editForm) || "HostとPortを入力してください"}</div>
            </div>
            <div className="grid gap-2">
              <label className="text-sm font-medium">説明</label>
              <Textarea value={editForm.description} onChange={(event) => setEditForm((current) => ({ ...current, description: event.target.value }))} rows={3} />
            </div>
          </div>
          <DialogFooter>
            <Button variant="outline" onClick={() => setEditingNode(null)}>
              {t("cancel")}
            </Button>
            <Button onClick={submitEditNode} disabled={!allowed || !editFormValid || updateNode.isPending}>
              {updateNode.isPending ? "保存中" : "保存"}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
      ) : null}
    </div>
  );
}

function nodeTypeLabel(type?: string) {
  return nodeTypes.find((item) => item.value === type)?.label || type || "-";
}

function nodeIdentity(node: WorkerNode) {
  return node.service_id || node.id;
}

function nodeDisplayName(node: WorkerNode) {
  return node.service_name || "未設定のNode名";
}

function nodeEditDefaults(node: WorkerNode): NodeEditForm {
  const parsed = parseNodePublicURL(node.public_url);
  return {
    service_name: node.service_name || "",
    description: node.description || "",
    host: node.host || parsed.host,
    port: node.port ? String(node.port) : parsed.port,
    ssl_enabled: node.ssl_enabled ?? parsed.ssl_enabled ?? true,
  };
}

function parseNodePublicURL(publicURL?: string) {
  if (!publicURL) return { host: "", port: "", ssl_enabled: true };
  try {
    const url = new URL(publicURL);
    const sslEnabled = url.protocol === "https:";
    return {
      host: url.hostname,
      port: url.port || (sslEnabled ? "443" : "80"),
      ssl_enabled: sslEnabled,
    };
  } catch {
    return { host: "", port: "", ssl_enabled: true };
  }
}

function editNodeApiURL(form: NodeEditForm) {
  const host = form.host.trim();
  const port = Number.parseInt(form.port, 10);
  if (!host || !Number.isFinite(port) || port <= 0) return "";
  return `${form.ssl_enabled ? "https" : "http"}://${host}:${port}`;
}

function nodeEndpoint(node: WorkerNode) {
  if (node.host && node.port) {
    return `${node.ssl_enabled ? "https" : "http"}://${node.host}:${node.port}`;
  }
  return node.public_url || "-";
}

function formatHeartbeat(node: WorkerNode, timezone?: string) {
  if (typeof node.heartbeat_age_sec === "number") return `${node.heartbeat_age_sec} sec`;
  if (node.last_heartbeat_at) return formatNodeDateTime(node.last_heartbeat_at, timezone);
  return "-";
}

function formatNodeDateTime(value?: string, timezone?: string) {
  return formatDateTimeInTimeZone(value, timezone, { year: "numeric", month: "2-digit", day: "2-digit", hour: "2-digit", minute: "2-digit" });
}

function nodeReportedPlatform(node: WorkerNode) {
  const os = node.reported_os || (node.configure_token_used_at ? "OS未取得" : "OS未取得（Configure待ち）");
  const arch = node.reported_arch || (node.configure_token_used_at ? "Arch未取得" : "Arch未取得（Configure待ち）");
  return `${os} / ${arch}`;
}

function NodeMetricsSummary({ node }: { node: WorkerNode }) {
  const metrics = node.metrics || {};
  const entries = Object.entries(metrics).filter(([, value]) => value !== "" && value !== null && value !== undefined);
  if (node.service_type === "observability") {
    const uptime = metricValue(metrics, ["observability.uptime_seconds"]);
    const goroutines = metricValue(metrics, ["observability.goroutines"]);
    const heap = metricValue(metrics, ["observability.heap_alloc_bytes", "observability.heap_sys_bytes"]);
    return (
      <div className="min-w-36 text-sm">
        <div className="flex items-center gap-1.5">
          <Activity className="size-3.5 text-muted-foreground" />
          {entries.length > 0 ? `${entries.length}項目` : "未受信"}
        </div>
        <div className="text-xs text-muted-foreground">
          UP {formatMetricDuration(uptime)} / Go {formatMetricCount(goroutines)}
        </div>
        <div className="text-xs text-muted-foreground">Heap {formatMetricBytes(heap)}</div>
      </div>
    );
  }
  const cpu = metricValue(metrics, ["cpu_percent", "cpuUsage", "process.cpu_percent"]);
  const memory = metricValue(metrics, ["memory_percent", "memoryUsage", "process.memory_percent"]);
  return (
    <div className="min-w-36 text-sm">
      <div className="flex items-center gap-1.5">
        <Activity className="size-3.5 text-muted-foreground" />
        {entries.length > 0 ? `${entries.length}項目` : "未受信"}
      </div>
      <div className="text-xs text-muted-foreground">
        CPU {formatMetricPercent(cpu)} / MEM {formatMetricPercent(memory)}
      </div>
    </div>
  );
}

function metricValue(metrics: Record<string, number | string>, keys: string[]) {
  for (const key of keys) {
    const value = metrics[key];
    if (typeof value === "number" && Number.isFinite(value)) return value;
    if (typeof value === "string" && value.trim() !== "" && Number.isFinite(Number(value))) return Number(value);
  }
  return undefined;
}

function formatMetricPercent(value?: number) {
  if (typeof value !== "number") return "-";
  return `${Math.round(value * 10) / 10}%`;
}

function formatMetricCount(value?: number) {
  if (typeof value !== "number") return "-";
  return String(Math.round(value));
}

function formatMetricDuration(value?: number) {
  if (typeof value !== "number") return "-";
  if (value < 60) return `${Math.round(value)}s`;
  if (value < 3600) return `${Math.round(value / 60)}m`;
  return `${Math.round(value / 3600)}h`;
}

function formatMetricBytes(value?: number) {
  if (typeof value !== "number") return "-";
  if (value < 1024 * 1024) return `${Math.round(value / 1024)}KiB`;
  if (value < 1024 * 1024 * 1024) return `${Math.round((value / 1024 / 1024) * 10) / 10}MiB`;
  return `${Math.round((value / 1024 / 1024 / 1024) * 10) / 10}GiB`;
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
      not_found: "対象のNodeが見つかりません。一覧を更新してください。",
      service_not_found: "対象のNodeが見つかりません。一覧を更新してください。",
      permission_denied: "この操作に必要な権限がありません。Runtime Token再生成には api_tokens.revoke 権限が必要です。",
      store_node_runtime_token_failed: "Control Panelのenvに AUTOSTREAM_SECRET_ENCRYPTION_KEY が設定されていない、または暗号化設定が不正です。設定後にControl Panelを再起動してください。",
      create_node_configure_token_failed: "Configure Tokenの保存に失敗しました。database接続とControl Panelのログを確認してください。",
      create_node_registration_token_failed: "Node Runtime Tokenの作成に失敗しました。Control Panelのログを確認してください。",
      rotate_node_runtime_token_failed: "Node Runtime Tokenの再生成に失敗しました。Control Panelのログを確認してください。",
      runtime_token_not_found: "現在のRuntime Tokenが見つかりません。Nodeを再作成するか、Control Panelのログを確認してください。",
      update_node_failed: "Nodeの更新に失敗しました。Control Panelのログを確認してください。",
      delete_service_failed: "Nodeの削除に失敗しました。割り当て状態とControl Panelのログを確認してください。",
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
