"use client";

import { useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import type { ColumnDef } from "@tanstack/react-table";
import { Check, Copy, FileCode2, Link, RotateCw } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { DataTable } from "@/components/tables/data-table";
import { DangerConfirm } from "@/components/admin/danger-confirm";
import { MetricCard } from "@/components/admin/metric-card";
import { RoleGuard, guardedButtonProps } from "@/components/admin/role-guard";
import { StatusBadge } from "@/components/admin/status-badge";
import { apiGet, apiPost } from "@/lib/api/client";
import { hasPermission } from "@/lib/auth/permissions";
import { isServiceAvailable } from "@/lib/service-health";
import { useCurrentUser, useNodes, useServiceHealth, useWorkers } from "@/features/queries";
import { useI18n } from "@/components/admin/i18n-provider";
import type { WorkerNode } from "@/types/domain";
import { formatNodeMetricPercent, formatWorkerHeartbeat } from "./node-operational-display";

type NodeConfigurationResponse = {
  node: WorkerNode;
  node_api_url: string;
  configuration_yaml: string;
  configure_command: string;
  systemd_unit?: string;
};

export function WorkersView() {
  const { t } = useI18n();
  const currentUser = useCurrentUser();
  const canReadWorkers = hasPermission(currentUser.data, "workers.read");
  const workers = useWorkers(canReadWorkers);
  const canReadRegisteredNodes = hasPermission(currentUser.data, "api_tokens.create");
  const canReadServiceHealth = hasPermission(currentUser.data, "service_health.read");
  const registeredNodes = useNodes(canReadRegisteredNodes);
  const serviceHealth = useServiceHealth(canReadServiceHealth);
  const queryClient = useQueryClient();
  const canRestart = hasPermission(currentUser.data, "workers.restart");
  const [configuration, setConfiguration] = useState<NodeConfigurationResponse | null>(null);
  const [copied, setCopied] = useState("");

  const restart = useMutation({
    mutationFn: (workerID: string) => apiPost(`/workers/${workerID}/restart`),
    onSuccess: async () => {
      await queryClient.invalidateQueries({ queryKey: ["workers"] });
    },
  });

  const loadConfiguration = useMutation({
    mutationFn: (nodeID: string) => apiGet<NodeConfigurationResponse>(`/nodes/${encodeURIComponent(nodeID)}/configuration`),
    onSuccess: (data) => setConfiguration(data),
  });

  const rows = mergeOperationalNodes(workers.data || [], registeredNodes.data || [], serviceHealth.data || []);
  const online = rows.filter(isNodeOnline).length;
  const activeJobs = rows.reduce((sum, node) => sum + Number(node.metrics?.active_jobs || node.metrics?.runningJobs || 0), 0);
  const warning = rows.filter((node) => !isNodeOnline(node)).length;

  const copyValue = async (key: string, value?: string) => {
    if (!value) return;
    await navigator.clipboard.writeText(value);
    setCopied(key);
    window.setTimeout(() => setCopied(""), 1200);
  };

  const columns: ColumnDef<WorkerNode>[] = [
    {
      accessorKey: "service_name",
      header: t("name"),
      cell: ({ row }) => {
        const nodeID = row.original.service_id || row.original.id;
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
    { accessorKey: "service_type", header: t("nodeType"), cell: ({ row }) => serviceTypeLabel(row.original.service_type) },
    {
      id: "endpoint",
      header: "接続先",
      cell: ({ row }) => {
        const node = row.original;
        const url = nodeEndpoint(node);
        return (
          <div className="flex items-center gap-2 text-sm">
            <span className="text-muted-foreground">{url ? "設定済み" : "未設定"}</span>
            {url ? (
              <Button variant="outline" size="icon-sm" aria-label="Node Agent API URLをコピー" onClick={() => copyValue(`endpoint-${node.service_id || node.id}`, url)}>
                {copied === `endpoint-${node.service_id || node.id}` ? <Check className="size-4" /> : <Link className="size-4" />}
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
      id: "reported",
      header: "報告情報",
      cell: ({ row }) => (
        <div className="text-sm">
          <div>Version {row.original.reported_version || row.original.version || "未取得"}</div>
          <div className="text-muted-foreground">
            {row.original.reported_os || "OS未取得"} / {row.original.reported_arch || "Arch未取得"}
          </div>
          <div className="text-muted-foreground">Capability {capabilityCount(row.original)}件</div>
        </div>
      ),
    },
    {
      accessorKey: "heartbeat_age_sec",
      header: "Heartbeat",
      cell: ({ row }) => formatWorkerHeartbeat(row.original),
    },
    {
      id: "load",
      header: "負荷",
      cell: ({ row }) => (
        <div className="text-sm">
          <div>CPU {formatNodeMetricPercent(row.original.metrics, "cpu")}</div>
          <div className="text-muted-foreground">MEM {formatNodeMetricPercent(row.original.metrics, "memory")}</div>
        </div>
      ),
    },
    {
      id: "actions",
      header: t("actions"),
      cell: ({ row }) => {
        const nodeID = row.original.service_id || row.original.id;
        return (
          <div className="flex items-center gap-2">
            <Button variant="outline" size="sm" aria-label="Configurationを表示" title="Configurationを表示" onClick={() => loadConfiguration.mutate(nodeID)} disabled={loadConfiguration.isPending}>
              <FileCode2 />
              <span>設定</span>
            </Button>
            {row.original.service_type === "worker" ? <RoleGuard allowed={canRestart}>
               <DangerConfirm title={`${row.original.service_name} を再起動しますか`} onConfirm={() => restart.mutate(nodeID)} actionLabel={t("restart")}>
                <Button variant="outline" size="sm" aria-label={t("restart")} title={t("restart")} {...guardedButtonProps(canRestart)}>
                  <RotateCw />
                  <span>再起動</span>
                </Button>
              </DangerConfirm>
            </RoleGuard> : null}
          </div>
        );
      },
    },
  ];

  return (
    <div className="space-y-4">
      <section className="grid gap-4 md:grid-cols-3">
        <MetricCard title={t("onlineNodes")} value={`${online}/${rows.length}`} detail="登録済みNode" tone={warning > 0 ? "warning" : "ok"} />
        <MetricCard title="実行中ジョブ" value={activeJobs} detail="現在処理中" />
        <MetricCard title={t("attentionRequired")} value={warning} detail="Heartbeatまたは状態に注意" tone={warning > 0 ? "danger" : "ok"} />
      </section>

      {configuration ? (
        <Card>
          <CardHeader>
            <CardTitle>Configuration: {configuration.node.service_name}</CardTitle>
          </CardHeader>
          <CardContent className="grid gap-4 lg:grid-cols-2">
            <SecretBlock label="Node Agent API URL" value={configuration.node_api_url || "-"} copied={copied === "api"} onCopy={() => copyValue("api", configuration.node_api_url)} />
            <SecretBlock label="Auto Configure" value={configuration.configure_command} copied={copied === "command"} onCopy={() => copyValue("command", configuration.configure_command)} />
            <SecretBlock label="config.yml" value={configuration.configuration_yaml} copied={copied === "yaml"} onCopy={() => copyValue("yaml", configuration.configuration_yaml)} />
            {configuration.systemd_unit ? (
              <SecretBlock label="systemd" value={configuration.systemd_unit} copied={copied === "systemd"} onCopy={() => copyValue("systemd", configuration.systemd_unit)} />
            ) : null}
          </CardContent>
        </Card>
      ) : null}

      <Card className="min-w-0">
        <CardHeader>
          <CardTitle>{t("workers")}</CardTitle>
          <p className="text-sm text-muted-foreground">Worker、Encoder / Recorder、Discord BOT、Observabilityの運用状態をまとめて確認します。</p>
        </CardHeader>
        <CardContent>
          {registeredNodes.isError || serviceHealth.isError ? (
            <div className="mb-3 rounded-md border border-amber-300 bg-amber-50 p-3 text-sm text-amber-950 dark:border-amber-900 dark:bg-amber-950/35 dark:text-amber-100" role="status">
              一部のNode情報を取得できませんでした。登録済みNodeまたはサービス稼働ページの権限と接続状態を確認してください。
            </div>
          ) : null}
          <DataTable columns={columns} data={rows} filterPlaceholder="Node名、種類、状態で絞り込み" getRowId={(row) => row.service_id || row.id} minTableWidthClass="min-w-[980px]" />
        </CardContent>
      </Card>
    </div>
  );
}

function capabilityCount(node: WorkerNode) {
  const capabilities = node.reported_capabilities && Object.keys(node.reported_capabilities).length > 0 ? node.reported_capabilities : node.capabilities;
  return Object.keys(capabilities ?? {}).length;
}

const operationalNodeTypes = new Set(["worker", "encoder_recorder", "discord_bot", "observability"]);

function mergeOperationalNodes(...sources: WorkerNode[][]) {
  const merged = new Map<string, WorkerNode>();
  for (const source of sources) {
    for (const node of source) {
      if (!operationalNodeTypes.has(node.service_type)) continue;
      const id = node.service_id || node.id;
      if (!id) continue;
      const current = merged.get(id);
      merged.set(id, current ? {
        ...current,
        ...node,
        service_id: current.service_id || node.service_id,
        id: current.id || node.id,
        service_type: current.service_type || node.service_type,
        service_name: current.service_name || node.service_name,
        reported_version: node.reported_version || current.reported_version,
        reported_commit: node.reported_commit || current.reported_commit,
        reported_build_date: node.reported_build_date || current.reported_build_date,
        status: node.status || current.status,
        health_status: node.health_status || current.health_status,
      } : node);
    }
  }
  return Array.from(merged.values()).sort((a, b) => {
    const type = serviceTypeLabel(a.service_type).localeCompare(serviceTypeLabel(b.service_type), "ja");
    return type || (a.service_name || a.service_id || a.id).localeCompare(b.service_name || b.service_id || b.id, "ja");
  });
}

function isNodeOnline(node: WorkerNode) {
  return isServiceAvailable(node);
}

function serviceTypeLabel(type?: string) {
  const labels: Record<string, string> = {
    worker: "Worker",
    encoder_recorder: "Encoder / Recorder",
    discord_bot: "Discord BOT",
    observability: "Observability",
  };
  return labels[type || ""] || type || "Node";
}

function nodeDisplayName(node: WorkerNode) {
  return node.service_name || "未設定のNode名";
}

function nodeEndpoint(node: WorkerNode) {
  if (node.host && node.port) {
    return `${node.ssl_enabled ? "https" : "http"}://${node.host}:${node.port}`;
  }
  return node.public_url || "";
}

function SecretBlock({ label, value, copied, onCopy }: { label: string; value: string; copied: boolean; onCopy: () => void }) {
  return (
    <div className="min-w-0 space-y-2">
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
