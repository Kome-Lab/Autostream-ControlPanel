"use client";

import { useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import type { ColumnDef } from "@tanstack/react-table";
import { Check, Copy, FileCode2, RotateCw } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { DataTable } from "@/components/tables/data-table";
import { DangerConfirm } from "@/components/admin/danger-confirm";
import { MetricCard } from "@/components/admin/metric-card";
import { RoleGuard, guardedButtonProps } from "@/components/admin/role-guard";
import { StatusBadge } from "@/components/admin/status-badge";
import { apiGet, apiPost } from "@/lib/api/client";
import { hasPermission } from "@/lib/auth/permissions";
import { useCurrentUser, useWorkers } from "@/features/queries";
import { useI18n } from "@/components/admin/i18n-provider";
import type { WorkerNode } from "@/types/domain";

type NodeConfigurationResponse = {
  node: WorkerNode;
  node_api_url: string;
  configuration_yaml: string;
  configure_command: string;
  systemd_unit: string;
};

export function WorkersView() {
  const { t } = useI18n();
  const workers = useWorkers();
  const currentUser = useCurrentUser();
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

  const rows = workers.data || [];
  const online = rows.filter((node) => node.status === "online").length;
  const activeJobs = rows.reduce((sum, node) => sum + Number(node.metrics?.active_jobs || node.metrics?.runningJobs || 0), 0);
  const warning = rows.filter((node) => ["degraded", "warning", "offline"].includes(node.status) || ["warning", "offline"].includes(node.health_status || "")).length;

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
      cell: ({ row }) => (
        <div className="min-w-56">
          <div className="font-medium">{row.original.service_name}</div>
          <div className="text-xs text-muted-foreground">{row.original.service_id || row.original.id}</div>
        </div>
      ),
    },
    { accessorKey: "service_type", header: t("nodeType") },
    {
      id: "endpoint",
      header: "Node Agent API",
      cell: ({ row }) => {
        const node = row.original;
        const url = node.host && node.port ? `${node.ssl_enabled ? "https" : "http"}://${node.host}:${node.port}` : node.public_url || "-";
        return <span className="break-all text-sm">{url}</span>;
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
      cell: ({ row }) => `${row.original.heartbeat_age_sec ?? "-"} sec`,
    },
    {
      id: "load",
      header: "負荷",
      cell: ({ row }) => (
        <div className="text-sm">
          <div>CPU {row.original.metrics?.cpu_percent ?? row.original.metrics?.cpuUsage ?? "-"}%</div>
          <div className="text-muted-foreground">MEM {row.original.metrics?.memory_percent ?? row.original.metrics?.memoryUsage ?? "-"}%</div>
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
            <Button variant="outline" size="icon-sm" aria-label="Configuration" onClick={() => loadConfiguration.mutate(nodeID)} disabled={loadConfiguration.isPending}>
              <FileCode2 />
            </Button>
            <RoleGuard allowed={canRestart}>
              <DangerConfirm title={`${row.original.service_name} を再起動しますか`} onConfirm={() => restart.mutate(nodeID)} actionLabel={t("restart")}>
                <Button variant="outline" size="icon-sm" aria-label={t("restart")} {...guardedButtonProps(canRestart)}>
                  <RotateCw />
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
            <SecretBlock label="systemd" value={configuration.systemd_unit} copied={copied === "systemd"} onCopy={() => copyValue("systemd", configuration.systemd_unit)} />
          </CardContent>
        </Card>
      ) : null}

      <Card>
        <CardHeader>
          <CardTitle>{t("workers")}</CardTitle>
        </CardHeader>
        <CardContent>
          <DataTable columns={columns} data={rows} filterPlaceholder="Node名、種類、状態で絞り込み" getRowId={(row) => row.service_id || row.id} />
        </CardContent>
      </Card>
    </div>
  );
}

function capabilityCount(node: WorkerNode) {
  const capabilities = node.reported_capabilities && Object.keys(node.reported_capabilities).length > 0 ? node.reported_capabilities : node.capabilities;
  return Object.keys(capabilities ?? {}).length;
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
