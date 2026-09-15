"use client";
import { useUICopy } from "@/lib/i18n/ui-v2/use-ui-copy";


import { useDraftExit, useNonSecretDraft, useExistingDraft } from "@/components/forms/draft-exit";
import { useId, useMemo, useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { AlertCircle, KeyRound, RotateCw, Server } from "lucide-react";
import { PageHeader } from "@/components/shell/page-header";
import { NodeWorkspaceNavigation } from "./node-workspace-navigation";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Checkbox } from "@/components/ui/checkbox";
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle, DialogTrigger } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Textarea } from "@/components/ui/textarea";
import { hasPermission } from "@/lib/auth/permissions";
import { useAppSettings, useCurrentUser, useNodes } from "@/features/queries";
import { useI18n } from "@/components/admin/i18n-provider";
import { canIssueNodeConfiguration } from "@/lib/node-configuration";
import { nodeRegistrationDraftValid } from "@/lib/node-registration";
import type { WorkerNode } from "@/types/domain";
import { NODE_FOUNDATION_SOURCE_ENABLED } from "@/features/nodes/node-action-descriptors";
import { NodeFoundationRegistrationArtifact } from "@/features/nodes/node-foundation-artifact";
import { aggregateRemainingQueries, remainingQuerySnapshot } from "@/features/remote-state/remaining-remote-state";
import { RemainingStateNotice } from "@/features/remote-state/remaining-state-notice";
import { type NodeRegistrationViewMode, nodeTypes, type NodeConfigurationResponse, type NodeEditForm, nodeIdentity, nodeRegistrationErrorMessage, nodeEditDefaults, isPullNode } from "./node-registration-model";
import { RegisteredNodeGroup } from "./registered-node-group";
import { NodeConfigurationCard } from "./node-configuration-card";
import { NodeEditDialog } from "./node-edit-dialog";
import { useNodeRegistrationMutations } from "./use-node-registration-mutations";
import { createRegisteredNodeColumns } from "./registered-node-columns";


export function NodeRegistrationView({ mode = "registration" }: { mode?: NodeRegistrationViewMode }) {
  return NODE_FOUNDATION_SOURCE_ENABLED
    ? <NodeFoundationRegistrationArtifact mode={mode} />
    : <LegacyNodeRegistrationView mode={mode} />;
}

function LegacyNodeRegistrationView({ mode = "registration" }: { mode?: NodeRegistrationViewMode }) {
  const uiText = useUICopy();
  const { t, locale } = useI18n();
  const inputID = useId();
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
  const { createToken, loadConfiguration, regenerateConfigureToken, rotateRuntimeToken, updateNode, deleteNode } = useNodeRegistrationMutations({
    registrationDraft, invalidateNodeQueries, setConfiguration, setCreateOpen: (open) => { if (!open) createDraft.saved(); setCreateOpen(open); }, setEditingNode,
  });
  const createDraftExit = useDraftExit({ enabled: createOpen && canCreateNode, pending: createToken.isPending });
  const createDraft = useNonSecretDraft([nodeType, nodeID, name, host, port, sslEnabled, executionHostID, description, allowRuntimeSecrets, allowRemediation], createDraftExit);
  const editDraftExit = useDraftExit({ enabled: editingNode !== null && allowed, pending: updateNode.isPending });
  useExistingDraft(Boolean(editingNode) && JSON.stringify(editForm) !== JSON.stringify(editingNode ? nodeEditDefaults(editingNode) : editForm), updateNode.isPending, editDraftExit);
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
    editDraftExit.request(() => { setEditingNode(node); setEditForm(nodeEditDefaults(node)); });
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

  const registeredColumns = createRegisteredNodeColumns({
    t, copyValue, copied, timezone, allowed, canRevokeRuntimeToken, canResolveRuntimeSecrets, canExecuteSystemUpdates, canDeleteNode,
    actions: { loadConfiguration, regenerateConfigureToken, rotateRuntimeToken, deleteNode }, openEditNode,
  }, uiText);

  return (
    <div className="space-y-5" data-screen-family={mode === "registered" ? "registered-nodes" : "nodes"}>
      <PageHeader title={mode === "registered" ? t("registeredNodes") : t("nodeRegistration")}
        description={locale === "ja" ? "登録、接続、担当配信、更新処理を分けて確認します。" : "Review registration, connectivity, stream assignment and updates separately."} />
      <NodeWorkspaceNavigation active={mode === "registered" ? "registered" : "registration"} canRegister={allowed}
        canOperate={allowed || hasPermission(currentUser.data, "workers.read") || hasPermission(currentUser.data, "service_health.read")} />
      {showRegistration ? (
        <Dialog open={createOpen} onOpenChange={(open) => { if (open) setCreateOpen(true); else createDraftExit.request(() => setCreateOpen(false)); }}>
          <div className="flex justify-end">
            <DialogTrigger asChild>
              <Button disabled={!allowed}>
                <Server className="size-4" />
                {uiText("Nodeを新規作成")}</Button>
            </DialogTrigger>
          </div>
          <DialogContent className="max-h-[90vh] overflow-y-auto sm:max-w-2xl">
            <DialogHeader>
              <DialogTitle>{t("nodeRegistration")}</DialogTitle>
              <DialogDescription>{uiText("PanelでNodeを作成し、Node Agentへ配置する設定ファイルを発行します。")}</DialogDescription>
            </DialogHeader>
            <div className="space-y-4">
          <div className="grid gap-2">
            <label htmlFor={`${inputID}-type`} className="text-sm font-medium">{t("nodeType")}</label>
            <Select value={nodeType} onValueChange={handleTypeChange}>
              <SelectTrigger id={`${inputID}-type`}>
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
            <label htmlFor={`${inputID}-node-id`} className="text-sm font-medium">{t("nodeId")}</label>
            <Input id={`${inputID}-node-id`} value={nodeID} onChange={(event) => setNodeID(event.target.value)} />
          </div>
          <div className="grid gap-2">
            <label htmlFor={`${inputID}-name`} className="text-sm font-medium">{t("name")}</label>
            <Input id={`${inputID}-name`} value={name} onChange={(event) => setName(event.target.value)} />
          </div>
          {nodeType === "update_agent" ? (
            <div className="grid gap-4 rounded-md border bg-muted/30 p-3">
              <div className="flex items-center gap-2 text-sm font-medium"><Badge variant="secondary">pull_v2</Badge>Host Pull Agent</div>
              <div className="grid gap-2">
                <label htmlFor={`${inputID}-execution-host`} className="text-sm font-medium">Execution Host ID</label>
                <Input id={`${inputID}-execution-host`} value={executionHostID} onChange={(event) => setExecutionHostID(event.target.value)} />
                <p className="text-xs text-muted-foreground">
                  {uiText("物理ホストごとに一意のIDです。Host AgentはControl Panelへ外向き接続するため、APIポート・SSL・SSH設定は不要です。")}</p>
              </div>
            </div>
          ) : null}
          {!isPullHostAgent ? (
            <>
              <div className="grid gap-2 sm:grid-cols-[minmax(0,1fr)_120px]">
                <div className="grid gap-2">
                  <label htmlFor={`${inputID}-host`} className="text-sm font-medium">Host / FQDN / IP</label>
                  <Input id={`${inputID}-host`} value={host} onChange={(event) => setHost(event.target.value)} />
                </div>
                <div className="grid gap-2">
                  <label htmlFor={`${inputID}-port`} className="text-sm font-medium">Port</label>
                  <Input id={`${inputID}-port`} type="number" inputMode="numeric" min={1024} max={65535} value={port} onChange={(event) => setPort(event.target.value)} />
                </div>
              </div>
              <label className="flex items-center gap-2 text-sm">
                <Checkbox checked={sslEnabled} onCheckedChange={(value) => setSslEnabled(value === true)} />
                {uiText("SSLを有効化してHTTPSを使用")}</label>
              <div className="rounded-md border bg-muted/40 p-3 text-sm">
                <div className="font-medium">Node Agent API URL</div>
                <div className="mt-1 break-all text-muted-foreground">{nodeApiUrl || uiText("Hostと1024〜65535のPortを入力してください")}</div>
              </div>
            </>
          ) : (
            <div className="rounded-md border border-blue-500/30 bg-blue-500/10 p-3 text-sm text-muted-foreground">
              {uiText("受信listenerは作成しません。登録・Heartbeat・Policy取得はControl Panelの既存HTTPS APIを使用します。")}</div>
          )}
          <div className="grid gap-2">
            <label htmlFor={`${inputID}-description`} className="text-sm font-medium">{uiText("説明")}</label>
            <Textarea id={`${inputID}-description`} value={description} onChange={(event) => setDescription(event.target.value)} rows={3} />
          </div>
          <div className="grid gap-2 rounded-md border bg-muted/30 p-3 text-sm">
            <div className="font-medium">{uiText("Node Agentが自動報告する項目")}</div>
            <div className="text-muted-foreground">{uiText("バージョン、OS、ArchitectureはConfigure実行時または起動後のHeartbeatで報告されます。CapabilityとメトリクスはHeartbeatで更新されます。")}</div>
          </div>
          <label className="flex items-center gap-2 text-sm">
            <Checkbox checked={runtimeSecretsRequired || allowRuntimeSecrets} disabled={runtimeSecretsRequired} onCheckedChange={(value) => setAllowRuntimeSecrets(value === true)} />
            {runtimeSecretsRequired ? uiText("実行時シークレットを自動付与（Encoder / Recorder必須）") : t("runtimeSecrets")}
          </label>
          <label className="flex items-center gap-2 text-sm">
            <Checkbox checked={allowRemediation} onCheckedChange={(value) => setAllowRemediation(value === true)} />
            {t("remediation")}
          </label>
          <Button className="w-full" disabled={!canCreateNode || !createFormValid || createToken.isPending} onClick={() => createToken.mutate()}>
            <KeyRound className="size-4" />
            {createToken.isPending ? uiText("Node設定を発行中...") : uiText("Nodeを作成して設定を発行")}
          </Button>
          {!allowed ? <p className="text-sm text-red-600">{t("roleLimited")}</p> : null}
          {allowed && createIncludesManagedSecret && !canResolveRuntimeSecrets ? <p className="text-sm text-red-600">{uiText("Worker / Encoderの署名鍵または実行時シークレットを発行するには、シークレット更新権限が必要です。")}</p> : null}
          {allowed && nodeType === "update_agent" && !canResolveRuntimeSecrets ? <p className="text-sm text-red-600">{uiText("Updaterの登録とRuntime Tokenの発行には、secrets.update 権限が必要です。")}</p> : null}
          {allowed && nodeType === "update_agent" && !canExecuteSystemUpdates ? <p className="text-sm text-red-600">{uiText("Updaterの登録と更新用scopeの発行には、system_updates.execute 権限が必要です。")}</p> : null}
          {createError ? (
            <div className="flex gap-2 rounded-md border border-red-200 bg-red-50 p-3 text-sm text-red-700" role="alert" aria-live="polite">
              <AlertCircle className="mt-0.5 size-4 shrink-0" />
              <div>
                <div className="font-medium">{uiText("Node設定を発行できませんでした")}</div>
                <div className="mt-1">{createError}</div>
              </div>
            </div>
          ) : null}
            </div>
          </DialogContent>
        </Dialog>
      ) : null}

      <div className="grid gap-4">
        <NodeConfigurationCard
          configuration={configuration} configurationIsHostAgent={configurationIsHostAgent}
          updaterConfigureCommandAvailable={updaterConfigureCommandAvailable} updaterConfigureTokenRequired={updaterConfigureTokenRequired}
          timezone={timezone} copied={copied} copyValue={copyValue} t={t}
        />
      </div>

      {showRegistered ? (
      <Card>
        <CardHeader>
          <div className="flex flex-col gap-3 sm:flex-row sm:items-start sm:justify-between">
            <div>
              <CardTitle>{uiText("登録済みNode")}</CardTitle>
              <CardDescription>{uiText("作成済みNode、Configure実行状況、最終Heartbeatを確認できます。")}</CardDescription>
            </div>
            <Button variant="outline" size="sm" onClick={() => registeredNodes.refetch()} disabled={registeredNodes.isFetching}>
              <RotateCw className="size-4" />
              {registeredNodes.isFetching ? uiText("更新中") : locale === "ja" ? "更新" : "Refresh"}
            </Button>
          </div>
        </CardHeader>
        <CardContent className="space-y-3">
          {registeredRemoteState.kind !== "ready" || registeredRemoteState.freshness.kind !== "fresh" ? (
            <RemainingStateNotice state={registeredRemoteState} consumer="nodes" />
          ) : null}
          {createToken.data?.node ? (
            <div className="rounded-md border border-emerald-200 bg-emerald-50 p-3 text-sm text-emerald-800" role="status">
              {createToken.data.node.service_name} {uiText("を登録しました。一覧に表示されない場合は「更新」を押してください。")}</div>
          ) : null}
          {actionError ? (
            <div className="rounded-md border border-red-200 bg-red-50 p-3 text-sm text-red-700" role="alert" aria-live="polite">
              {actionError}
            </div>
          ) : null}
          <div className="text-sm text-muted-foreground">{uiText("登録済み:")}{registeredRows.length} Node</div>
          <div className="grid gap-4">
            <RegisteredNodeGroup
              title={uiText("Nodeサービス")}
              description={uiText("Worker、Encoder / Recorder、Discord BOT、Observabilityの登録・稼働情報")}
              rows={operationalRegisteredRows}
              columns={registeredColumns}
            />
            {updaterRegisteredRows.length > 0 ? (
              <RegisteredNodeGroup
                title="Updater / Host Agent"
                description={uiText("ホスト単位の更新専用。通常のNodeサービスとは別の管理経路です。")}
                rows={updaterRegisteredRows}
                columns={registeredColumns}
                filterPlaceholder={uiText("Updater名、Host ID、状態で検索")}
              />
            ) : null}
          </div>
        </CardContent>
      </Card>
      ) : null}
      {showRegistered ? (
      <NodeEditDialog
        editingNode={editingNode} setEditingNode={(node) => editDraftExit.request(() => setEditingNode(node))} editForm={editForm} setEditForm={setEditForm}
        editingPullHostAgent={editingPullHostAgent} allowed={allowed} editFormValid={editFormValid}
        updatePending={updateNode.isPending} submitEditNode={submitEditNode} t={t}
      />
      ) : null}
    </div>
  );
}
