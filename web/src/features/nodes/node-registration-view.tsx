"use client";

import { useMemo, useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import type { ColumnDef } from "@tanstack/react-table";
import { AlertCircle, Check, Copy, FileCode2, KeyRound, LockKeyhole, Pencil, RotateCw, Server, Trash2 } from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Checkbox } from "@/components/ui/checkbox";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Textarea } from "@/components/ui/textarea";
import { DeferredNodeConfirmation as DangerConfirm } from "@/features/nodes/deferred-node-confirmation";
import { RoleGuard, guardedButtonProps } from "@/components/admin/role-guard";
import { statusDescriptor } from "@/components/admin/status-badge";
import { apiDelete, apiGet, apiPost, apiPut } from "@/lib/api/client";
import { hasPermission } from "@/lib/auth/permissions";
import { useAppSettings, useCurrentUser, useNodes } from "@/features/queries";
import { useI18n } from "@/components/admin/i18n-provider";
import { canIssueNodeConfiguration, canRegenerateNodeConfigureToken, canRotateNodeRuntimeToken } from "@/lib/node-configuration";
import { buildNodeRegistrationRequest, nodeRegistrationDraftValid } from "@/lib/node-registration";
import type { NodeRegistrationResponse, WorkerNode } from "@/types/domain";
import { NODE_FOUNDATION_SOURCE_ENABLED } from "@/features/nodes/node-action-descriptors";
import { NodeFoundationRegistrationArtifact } from "@/features/nodes/node-foundation-artifact";
import { aggregateRemainingQueries, remainingQuerySnapshot } from "@/features/remote-state/remaining-remote-state";
import { RemainingStateNotice } from "@/features/remote-state/remaining-state-notice";
import { type NodeRegistrationViewMode, nodeTypes, type NodeConfigurationResponse, type NodeEditForm, nodeIdentity, nodeRegistrationErrorMessage, nodeEditDefaults, isPullNode, nodeDisplayName, editNodeApiURL } from "./node-registration-model";
import { NodeTypeSummary, NodeStatusSummary, NodeReportSummary } from "./node-status-summary";
import { NodeEndpointStateView } from "./node-endpoint-state-view";
import { formatHeartbeat, formatNodeDateTime } from "./node-metrics-summary";
import { SecretBlock } from "./node-configuration-secret-block";
import { RegisteredNodeGroup } from "./registered-node-group";

export function NodeRegistrationView({ mode = "registration" }: { mode?: NodeRegistrationViewMode }) {
  return NODE_FOUNDATION_SOURCE_ENABLED
    ? <NodeFoundationRegistrationArtifact mode={mode} />
    : <LegacyNodeRegistrationView mode={mode} />;
}

function LegacyNodeRegistrationView({ mode = "registration" }: { mode?: NodeRegistrationViewMode }) {
  const { t } = useI18n();
  const currentUser = useCurrentUser();
  const appSettings = useAppSettings();
  const registeredNodes = useNodes();
  const queryClient = useQueryClient();
  const timezone = appSettings.data?.timezone;
  const [nodeType, setNodeType] = useState("worker");
  const selectedType = nodeTypes.find((type) => type.value === nodeType) ?? nodeTypes[0];
  const runtimeSecretsRequired = selectedType.runtimeSecretsRequired;
  const [nodeID, setNodeID] = useState("worker-tokyo-01");
  const [name, setName] = useState("東京本社 Worker 01");
  const [host, setHost] = useState("worker-tokyo-01.example.jp");
  const [port, setPort] = useState(String(selectedType.defaultPort));
  const [sslEnabled, setSslEnabled] = useState(true);
  const [executionHostID, setExecutionHostID] = useState("host-tokyo-01");
  const [description, setDescription] = useState("番組配信と録画を担当する東京本社のNode Agent");
  const [allowRuntimeSecrets, setAllowRuntimeSecrets] = useState(false);
  const [allowRemediation, setAllowRemediation] = useState(false);
  const [createOpen, setCreateOpen] = useState(false);
  const [copied, setCopied] = useState("");
  const [configuration, setConfiguration] = useState<NodeConfigurationResponse | null>(null);
  const [editingNode, setEditingNode] = useState<WorkerNode | null>(null);
  const [editForm, setEditForm] = useState<NodeEditForm>({ service_name: "", description: "", host: "", port: "", ssl_enabled: true });

  const allowed = hasPermission(currentUser.data, "api_tokens.create");
  const canRevokeRuntimeToken = hasPermission(currentUser.data, "api_tokens.revoke");
  const canResolveRuntimeSecrets = hasPermission(currentUser.data, "secrets.update");
  const canExecuteSystemUpdates = hasPermission(currentUser.data, "system_updates.execute");
  const canDeleteNode = hasPermission(currentUser.data, "services.disable");
  const createIncludesManagedSecret = nodeType === "worker" || nodeType === "encoder_recorder" || allowRuntimeSecrets;
  const createRequiresSecretUpdate = createIncludesManagedSecret || nodeType === "update_agent";
  const canCreateNode = canIssueNodeConfiguration({
    serviceType: nodeType,
    canCreateTokens: allowed,
    canResolveManagedSecret: canResolveRuntimeSecrets,
    requiresManagedSecret: createRequiresSecretUpdate,
    canExecuteSystemUpdates,
  });
  const isPullHostAgent = nodeType === "update_agent";
  const registrationDraft = {
    nodeType,
    nodeID,
    name,
    description,
    host,
    port,
    sslEnabled,
    allowRuntimeSecrets: runtimeSecretsRequired || allowRuntimeSecrets,
    allowRemediation,
    transportMode: "pull_v2" as const,
    executionHostID,
  };
  const createFormValid = nodeRegistrationDraftValid(registrationDraft);
  const nodeApiUrl = useMemo(() => {
    if (isPullHostAgent) return "";
    const scheme = sslEnabled ? "https" : "http";
    const normalizedHost = host.trim();
    const normalizedPort = Number.parseInt(port, 10);
    if (!normalizedHost || !Number.isFinite(normalizedPort) || normalizedPort <= 0) return "";
    return `${scheme}://${normalizedHost}:${normalizedPort}`;
  }, [host, isPullHostAgent, port, sslEnabled]);
  const configurationIsHostAgent = configuration?.node?.service_type === "update_agent";
  const updaterConfigureCommandAvailable = configuration?.node?.service_type === "update_agent" && Boolean(configuration.configure_command?.trim());
  const updaterConfigureTokenRequired = configuration?.node?.service_type === "update_agent" && !updaterConfigureCommandAvailable;

  const invalidateNodeQueries = async () => {
    await Promise.all([
      queryClient.invalidateQueries({ queryKey: ["nodes"] }),
      queryClient.invalidateQueries({ queryKey: ["service-health"] }),
      queryClient.invalidateQueries({ queryKey: ["workers"] }),
    ]);
  };

  const createToken = useMutation({
    mutationFn: () =>
      apiPost<NodeRegistrationResponse>("/nodes/registration-tokens", buildNodeRegistrationRequest(registrationDraft)),
    onSuccess: async (data) => {
      setConfiguration(data);
      setCreateOpen(false);
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
    mutationFn: ({ nodeID, values, endpointless }: { nodeID: string; values: NodeEditForm; endpointless: boolean }) =>
      apiPut<WorkerNode>(`/nodes/${encodeURIComponent(nodeID)}`, {
        service_name: values.service_name,
        description: values.description,
        ...(endpointless
          ? {}
          : {
              host: values.host,
              port: Number.parseInt(values.port, 10),
              ssl_enabled: values.ssl_enabled,
            }),
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
  const registeredRemoteState = aggregateRemainingQueries("nodes", {
    "registered-nodes": remainingQuerySnapshot(registeredNodes),
  });
  const operationalRegisteredRows = registeredRows.filter((node) => node.service_type !== "update_agent");
  const updaterRegisteredRows = registeredRows.filter((node) => node.service_type === "update_agent");

  const handleTypeChange = (value: string) => {
    setNodeType(value);
    const nextType = nodeTypes.find((type) => type.value === value);
    if (nextType) {
      setAllowRuntimeSecrets(nextType.runtimeSecretsRequired);
      setPort(String(nextType.defaultPort));
      setDescription(nextType.description);
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
    updateNode.mutate({
      nodeID: nodeIdentity(editingNode),
      values: editForm,
      endpointless: isPullNode(editingNode),
    });
  };

  const editPortNumber = Number.parseInt(editForm.port, 10);
  const editingPullHostAgent = Boolean(editingNode && isPullNode(editingNode));
  const editFormValid = editForm.service_name.trim() !== "" && (
    editingPullHostAgent ||
    (editForm.host.trim() !== "" && Number.isFinite(editPortNumber) && editPortNumber >= 1024 && editPortNumber <= 65535)
  );
  const showRegistration = mode !== "registered";
  const showRegistered = mode !== "registration";

  const registeredColumns: ColumnDef<WorkerNode>[] = [
    {
      accessorKey: "service_name",
      header: t("name"),
      cell: ({ row }) => {
        const nodeID = nodeIdentity(row.original);
        return (
          <div className="min-w-0">
            <div className="flex items-start gap-2">
              <div className="min-w-0">
                <div className="break-words font-medium">{nodeDisplayName(row.original)}</div>
                <div className="mt-1 break-all font-mono text-xs text-muted-foreground">{nodeID}</div>
              </div>
              <Button variant="outline" size="icon-sm" aria-label="Node IDをコピー" title="Node IDをコピー" onClick={() => copyValue(`node-id-${nodeID}`, nodeID)}>
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
      cell: ({ row }) => <NodeTypeSummary node={row.original} />,
    },
    {
      id: "endpoint",
      header: "Endpoint",
      cell: ({ row }) => (
        <NodeEndpointStateView
          node={row.original}
          compact
          copied={copied}
          onCopy={copyValue}
        />
      ),
    },
    {
      id: "status",
      header: "状態 / 登録",
      cell: ({ row }) => <NodeStatusSummary node={row.original} />,
    },
    {
      id: "reported",
      header: "報告 / 負荷",
      cell: ({ row }) => <NodeReportSummary node={row.original} />,
    },
    {
      id: "heartbeat",
      header: "最終Heartbeat",
      cell: ({ row }) => <span className="whitespace-nowrap text-sm">{formatHeartbeat(row.original, timezone)}</span>,
    },
    {
      id: "actions",
      header: t("actions"),
      cell: ({ row }) => {
        const node = row.original;
        const nodeID = nodeIdentity(node);
        const nodeConfigurationIncludesSigningKey = node.service_type === "worker" || node.service_type === "encoder_recorder";
        const tokenPermissions = {
          serviceType: node.service_type,
          canCreateTokens: allowed,
          canRevokeTokens: canRevokeRuntimeToken,
          canResolveManagedSecret: canResolveRuntimeSecrets,
          requiresManagedSecret: nodeConfigurationIncludesSigningKey,
          canExecuteSystemUpdates,
        };
        const canManageNodeTokens = canRotateNodeRuntimeToken(tokenPermissions);
        const canRegenerateConfigureToken = canRegenerateNodeConfigureToken(tokenPermissions);
        const configureTokenPermissionMessage = !allowed
          ? "Configure Token再生成には api_tokens.create 権限が必要です。"
          : !canRevokeRuntimeToken
            ? "Configure Token再生成には api_tokens.revoke 権限が必要です。"
            : nodeConfigurationIncludesSigningKey && !canResolveRuntimeSecrets
              ? "Worker / EncoderのConfigure Token再生成には secrets.update 権限が必要です。"
              : node.service_type === "update_agent" && !canResolveRuntimeSecrets
                ? "UpdaterのConfigure Token再生成には secrets.update 権限が必要です。"
                : node.service_type === "update_agent" && !canExecuteSystemUpdates
                  ? "UpdaterのConfigure Token再生成には system_updates.execute 権限が必要です。"
                  : "Configure Tokenを再生成する権限がありません。";
        const runtimeTokenPermissionMessage = node.service_type === "update_agent" && !canResolveRuntimeSecrets
          ? "UpdaterのRuntime Token再生成には secrets.update 権限が必要です。"
          : node.service_type === "update_agent" && !canExecuteSystemUpdates
            ? "UpdaterのRuntime Token再生成には system_updates.execute 権限が必要です。"
            : "Runtime Token再生成には api_tokens.create と api_tokens.revoke 権限が必要です。";
        return (
          <div className="flex min-w-0 flex-wrap items-center gap-1.5">
            <Button variant="outline" size="sm" className="px-2" aria-label="Configurationを表示" title="Configurationを表示" onClick={() => loadConfiguration.mutate(nodeID)} disabled={loadConfiguration.isPending}>
              <FileCode2 />
              <span>設定</span>
            </Button>
            <RoleGuard allowed={canRegenerateConfigureToken} message={configureTokenPermissionMessage}>
              <Button variant="outline" size="sm" className="px-2" aria-label="Configure Tokenを再生成" title="Configure Tokenを再生成" onClick={() => regenerateConfigureToken.mutate(nodeID)} {...guardedButtonProps(canRegenerateConfigureToken)} disabled={!canRegenerateConfigureToken || regenerateConfigureToken.isPending}>
                <KeyRound />
                <span>初期化</span>
              </Button>
            </RoleGuard>
            <RoleGuard allowed={canManageNodeTokens} message={runtimeTokenPermissionMessage}>
              <DangerConfirm
                title={`${node.service_name} のRuntime Tokenを再生成しますか`}
                description={node.service_type === "update_agent"
                  ? "既存のRuntime Tokenは無効になります。通常はこの操作ではなくConfigure Tokenを再生成し、表示された手順をこのHost Agentを稼働させる対象ホストで実行してください。"
                  : "既存のRuntime Tokenは無効になります。Node Agentへ新しいconfig.ymlまたはTokenを反映してください。"}
                onConfirm={() => rotateRuntimeToken.mutate(nodeID)}
                actionLabel="再生成"
              >
                <Button variant="outline" size="sm" className="px-2" aria-label="Runtime Tokenを再生成" title="Runtime Tokenを再生成" {...guardedButtonProps(canManageNodeTokens)} disabled={!canManageNodeTokens || rotateRuntimeToken.isPending}>
                  <RotateCw />
                  <span>Token</span>
                </Button>
              </DangerConfirm>
            </RoleGuard>
            <Button variant="outline" size="sm" className="px-2" aria-label="Nodeを編集" title="Nodeを編集" onClick={() => openEditNode(node)} disabled={!allowed}>
              <Pencil />
              <span>編集</span>
            </Button>
            <RoleGuard allowed={canDeleteNode}>
              <DangerConfirm title={`${node.service_name} を削除しますか`} description="Node登録、割り当て、Runtime Tokenを無効化します。この操作は取り消せません。" onConfirm={() => deleteNode.mutate(nodeID)} actionLabel="削除">
                <Button variant="destructive" size="sm" className="px-2" aria-label="Nodeを削除" title="Nodeを削除" {...guardedButtonProps(canDeleteNode)} disabled={!canDeleteNode || deleteNode.isPending}>
                  <Trash2 />
                  <span>削除</span>
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
      {showRegistration ? (
        <div className="flex justify-end">
          <Button onClick={() => setCreateOpen(true)} disabled={!allowed}>
            <Server className="size-4" />
            Nodeを新規作成
          </Button>
        </div>
      ) : null}
      {showRegistration ? (
        <Dialog open={createOpen} onOpenChange={setCreateOpen}>
          <DialogContent className="max-h-[90vh] overflow-y-auto sm:max-w-2xl">
            <DialogHeader>
              <DialogTitle>{t("nodeRegistration")}</DialogTitle>
              <DialogDescription>PanelでNodeを作成し、Node Agentへ配置する設定ファイルを発行します。</DialogDescription>
            </DialogHeader>
            <div className="space-y-4">
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
          {nodeType === "update_agent" ? (
            <div className="grid gap-4 rounded-md border bg-muted/30 p-3">
              <div className="flex items-center gap-2 text-sm font-medium"><Badge variant="secondary">pull_v2</Badge>Host Pull Agent</div>
              <div className="grid gap-2">
                <label className="text-sm font-medium">Execution Host ID</label>
                <Input value={executionHostID} onChange={(event) => setExecutionHostID(event.target.value)} />
                <p className="text-xs text-muted-foreground">
                  物理ホストごとに一意のIDです。Host AgentはControl Panelへ外向き接続するため、APIポート・SSL・SSH設定は不要です。
                </p>
              </div>
            </div>
          ) : null}
          {!isPullHostAgent ? (
            <>
              <div className="grid gap-2 sm:grid-cols-[minmax(0,1fr)_120px]">
                <div className="grid gap-2">
                  <label className="text-sm font-medium">Host / FQDN / IP</label>
                  <Input value={host} onChange={(event) => setHost(event.target.value)} />
                </div>
                <div className="grid gap-2">
                  <label className="text-sm font-medium">Port</label>
                  <Input type="number" inputMode="numeric" min={1024} max={65535} value={port} onChange={(event) => setPort(event.target.value)} />
                </div>
              </div>
              <label className="flex items-center gap-2 text-sm">
                <Checkbox checked={sslEnabled} onCheckedChange={(value) => setSslEnabled(value === true)} />
                SSLを有効化してHTTPSを使用
              </label>
              <div className="rounded-md border bg-muted/40 p-3 text-sm">
                <div className="font-medium">Node Agent API URL</div>
                <div className="mt-1 break-all text-muted-foreground">{nodeApiUrl || "Hostと1024〜65535のPortを入力してください"}</div>
              </div>
            </>
          ) : (
            <div className="rounded-md border border-blue-500/30 bg-blue-500/10 p-3 text-sm text-muted-foreground">
              受信listenerは作成しません。登録・Heartbeat・Policy取得はControl Panelの既存HTTPS APIを使用します。
            </div>
          )}
          <div className="grid gap-2">
            <label className="text-sm font-medium">説明</label>
            <Textarea value={description} onChange={(event) => setDescription(event.target.value)} rows={3} />
          </div>
          <div className="grid gap-2 rounded-md border bg-muted/30 p-3 text-sm">
            <div className="font-medium">Node Agentが自動報告する項目</div>
            <div className="text-muted-foreground">バージョン、OS、ArchitectureはConfigure実行時または起動後のHeartbeatで報告されます。CapabilityとメトリクスはHeartbeatで更新されます。</div>
          </div>
          <label className="flex items-center gap-2 text-sm">
            <Checkbox checked={runtimeSecretsRequired || allowRuntimeSecrets} disabled={runtimeSecretsRequired} onCheckedChange={(value) => setAllowRuntimeSecrets(value === true)} />
            {runtimeSecretsRequired ? "実行時シークレットを自動付与（Encoder / Recorder必須）" : t("runtimeSecrets")}
          </label>
          <label className="flex items-center gap-2 text-sm">
            <Checkbox checked={allowRemediation} onCheckedChange={(value) => setAllowRemediation(value === true)} />
            {t("remediation")}
          </label>
          <Button className="w-full" disabled={!canCreateNode || !createFormValid || createToken.isPending} onClick={() => createToken.mutate()}>
            <KeyRound className="size-4" />
            {createToken.isPending ? "Node設定を発行中..." : "Nodeを作成して設定を発行"}
          </Button>
          {!allowed ? <p className="text-sm text-red-600">{t("roleLimited")}</p> : null}
          {allowed && createIncludesManagedSecret && !canResolveRuntimeSecrets ? <p className="text-sm text-red-600">Worker / Encoderの署名鍵または実行時シークレットを発行するには、シークレット更新権限が必要です。</p> : null}
          {allowed && nodeType === "update_agent" && !canResolveRuntimeSecrets ? <p className="text-sm text-red-600">Updaterの登録とRuntime Tokenの発行には、secrets.update 権限が必要です。</p> : null}
          {allowed && nodeType === "update_agent" && !canExecuteSystemUpdates ? <p className="text-sm text-red-600">Updaterの登録と更新用scopeの発行には、system_updates.execute 権限が必要です。</p> : null}
          {createError ? (
            <div className="flex gap-2 rounded-md border border-red-200 bg-red-50 p-3 text-sm text-red-700" role="alert" aria-live="polite">
              <AlertCircle className="mt-0.5 size-4 shrink-0" />
              <div>
                <div className="font-medium">Node設定を発行できませんでした</div>
                <div className="mt-1">{createError}</div>
              </div>
            </div>
          ) : null}
            </div>
          </DialogContent>
        </Dialog>
      ) : null}

      <div className="grid gap-4">
        <Card className="min-w-0">
        <CardHeader>
          <CardTitle className="flex items-center gap-2">
            <FileCode2 className="size-5" />
            Configuration
          </CardTitle>
          <CardDescription>Node種別に応じた設定ファイルと、生成直後だけ表示されるTokenを安全にNodeへ反映してください。</CardDescription>
        </CardHeader>
        <CardContent className="space-y-4">
          {configuration ? (
            <>
              <div className="grid gap-2 rounded-md border bg-muted/40 p-3 text-sm">
                <div className="font-medium">接続状態</div>
                <div className="text-muted-foreground">
                  {configuration.node?.service_name || "選択中のNode"} / {statusDescriptor(configuration.node?.status).label} / 報告バージョン:{" "}
                  {configuration.node?.reported_version || "未取得"} / Capability: {Object.keys(configuration.node?.reported_capabilities ?? {}).length > 0 ? "報告済み" : "未取得"}
                </div>
                {configuration.configure_token_expires_at ? <div className="text-xs text-muted-foreground">Configure Token期限: {formatNodeDateTime(configuration.configure_token_expires_at, timezone)}</div> : null}
              </div>
              {configuration.node?.service_type === "discord_bot" ? (
                <div className="space-y-2 rounded-md border border-amber-500/30 bg-amber-500/10 p-3 text-sm">
                  <div className="font-medium">Discord VC空室時の自動停止を有効化</div>
                  <p className="text-muted-foreground">
                    既存のDiscord Botでは、ここでConfigure Tokenを再生成し、表示された設定をBotホストへ適用してからサービスを再起動してください。
                    この権限追加の対象になる、更新前に発行した未使用のConfigure Tokenは使えないため、再発行が必要です。実行完了時にだけ、既存の <code>streams.start</code> 権限へ対応する <code>streams.stop</code> 権限が新しいNode Runtime Tokenへ追加されます。Tokenを発行しただけでは切り替わりません。
                  </p>
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
                <SecretBlock label="Applied Node Agent API URL（後方互換）" value={configuration.node_api_url} copied={copied === "api-url"} onCopy={() => copyValue("api-url", configuration.node_api_url)} />
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
                  <div className="font-medium">{configurationIsHostAgent ? "Host Agentの初期設定" : "Node Agentの初期設定"}</div>
                  <p className="text-muted-foreground">
                    {configurationIsHostAgent
                      ? "表示された手順を、このHost Agentを稼働させる対象ホストで1回実行すると、Control Panelへの外向き接続を初期設定します。受信API endpointや専用portは作成しません。"
                      : "表示されたコマンドを対象ホストで1回実行すると、Panel接続情報を安全に初期設定します。"}
                    コマンド自体にConfigure Tokenは含まれず、実行時にTTYまたは標準入力から読み取ります。
                    設定処理が失敗または結果不確定の場合は、対象サービスを再起動しないでください。Configurationで新しいConfigure Tokenを発行し、同じtoken-free commandを新しいTokenで再実行してください。
                  </p>
                </div>
              ) : null}
              {updaterConfigureTokenRequired ? (
                <div className="space-y-2 rounded-md border border-blue-500/30 bg-blue-500/10 p-4 text-sm">
                  <div className="font-medium">Configure Tokenを再生成してください</div>
                  <p className="text-muted-foreground">
                    Configure Tokenと実行手順は再表示されません。一覧の鍵ボタンから新しいTokenを発行すると、この画面に対象サービスの初期設定手順が一度だけ表示されます。
                  </p>
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
              Nodeを作成、または登録済みNodeのConfiguration・Token再生成を実行するとここに表示されます。
            </div>
          )}
          <div className="grid gap-2 rounded-md border bg-muted/30 p-3 text-sm">
            <div className="flex items-center gap-2 font-medium">
              <LockKeyhole className="size-4" />
              Token運用
            </div>
            <div className="text-muted-foreground">
              Configure Tokenは1回限りの初期設定用、Node Runtime TokenはPanelとNode Agent間の通常通信認証用です。Host Agentの実行対象とpolicyは「アプリケーション情報」で設定します。
            </div>
            <div className="flex items-center gap-2 text-muted-foreground">
              <RotateCw className="size-4" />
              Updater / Host AgentのPanel接続情報を更新する場合はConfigure Tokenを再生成し、表示された手順を対象サービスを稼働させるホストで実行します。
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
          {registeredRemoteState.kind !== "ready" || registeredRemoteState.freshness.kind !== "fresh" ? (
            <RemainingStateNotice state={registeredRemoteState} consumer="nodes" />
          ) : null}
          {createToken.data?.node ? (
            <div className="rounded-md border border-emerald-200 bg-emerald-50 p-3 text-sm text-emerald-800" role="status">
              {createToken.data.node.service_name} を登録しました。一覧に表示されない場合は「更新」を押してください。
            </div>
          ) : null}
          {actionError ? (
            <div className="rounded-md border border-red-200 bg-red-50 p-3 text-sm text-red-700" role="alert" aria-live="polite">
              {actionError}
            </div>
          ) : null}
          <div className="text-sm text-muted-foreground">登録済み: {registeredRows.length} Node</div>
          <div className="grid gap-4">
            <RegisteredNodeGroup
              title="Nodeサービス"
              description="Worker、Encoder / Recorder、Discord BOT、Observabilityの登録・稼働情報"
              rows={operationalRegisteredRows}
              columns={registeredColumns}
            />
            {updaterRegisteredRows.length > 0 ? (
              <RegisteredNodeGroup
                title="Updater / Host Agent"
                description="ホスト単位の更新専用。通常のNodeサービスとは別の管理経路です。"
                rows={updaterRegisteredRows}
                columns={registeredColumns}
                filterPlaceholder="Updater名、Host ID、状態で検索"
              />
            ) : null}
          </div>
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
            {!editingPullHostAgent ? (
              <>
                <div className="grid gap-2 sm:grid-cols-[minmax(0,1fr)_120px]">
                  <div className="grid gap-2">
                    <label className="text-sm font-medium">Host / FQDN / IP</label>
                    <Input value={editForm.host} onChange={(event) => setEditForm((current) => ({ ...current, host: event.target.value }))} />
                  </div>
                  <div className="grid gap-2">
                    <label className="text-sm font-medium">Port</label>
                    <Input type="number" inputMode="numeric" min={1024} max={65535} value={editForm.port} onChange={(event) => setEditForm((current) => ({ ...current, port: event.target.value }))} />
                  </div>
                </div>
                <label className="flex items-center gap-2 text-sm">
                  <Checkbox checked={editForm.ssl_enabled} onCheckedChange={(value) => setEditForm((current) => ({ ...current, ssl_enabled: value === true }))} />
                  SSLを有効化してHTTPSを使用
                </label>
                <div className="rounded-md border bg-muted/40 p-3 text-sm">
                  <div className="font-medium">Node Agent API URL</div>
                  <div className="mt-1 break-all text-muted-foreground">{editNodeApiURL(editForm) || "Hostと1024〜65535のPortを入力してください"}</div>
                </div>
              </>
            ) : (
              <div className="rounded-md border bg-muted/40 p-3 text-sm text-muted-foreground">
                Host Pull Agentは受信endpointを持ちません。Execution Host IDとtransport ownershipは別の移行操作で管理されます。
              </div>
            )}
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
