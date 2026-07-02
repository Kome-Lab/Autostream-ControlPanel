"use client";

import { useMutation, useQueryClient } from "@tanstack/react-query";
import type { ColumnDef } from "@tanstack/react-table";
import { RotateCw } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { DataTable } from "@/components/tables/data-table";
import { DangerConfirm } from "@/components/admin/danger-confirm";
import { MetricCard } from "@/components/admin/metric-card";
import { RoleGuard, guardedButtonProps } from "@/components/admin/role-guard";
import { StatusBadge } from "@/components/admin/status-badge";
import { apiPost } from "@/lib/api/client";
import { hasPermission } from "@/lib/auth/permissions";
import { useCurrentUser, useWorkers } from "@/features/queries";
import { useI18n } from "@/components/admin/i18n-provider";
import type { WorkerNode } from "@/types/domain";

export function WorkersView() {
  const { t } = useI18n();
  const workers = useWorkers();
  const currentUser = useCurrentUser();
  const queryClient = useQueryClient();
  const canRestart = hasPermission(currentUser.data, "workers.restart");

  const restart = useMutation({
    mutationFn: (workerID: string) => apiPost(`/workers/${workerID}/restart`),
    onSuccess: async () => {
      await queryClient.invalidateQueries({ queryKey: ["workers"] });
    },
  });

  const rows = workers.data || [];
  const online = rows.filter((node) => node.status === "online").length;
  const activeJobs = rows.reduce((sum, node) => sum + Number(node.metrics?.active_jobs || 0), 0);
  const warning = rows.filter((node) => ["degraded", "warning", "offline"].includes(node.status) || node.health_status === "warning").length;

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
      accessorKey: "status",
      header: t("status"),
      cell: ({ row }) => <StatusBadge status={row.original.status} showDetail />,
    },
    { accessorKey: "assignment_role", header: "役割" },
    { accessorKey: "current_stream_id", header: "担当配信" },
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
          <div>CPU {row.original.metrics?.cpu_percent ?? "-"}%</div>
          <div className="text-muted-foreground">MEM {row.original.metrics?.memory_percent ?? "-"}%</div>
        </div>
      ),
    },
    {
      id: "actions",
      header: t("actions"),
      cell: ({ row }) => (
        <RoleGuard allowed={canRestart}>
          <DangerConfirm title={`${row.original.service_name} を再起動しますか`} onConfirm={() => restart.mutate(row.original.id)} actionLabel={t("restart")}>
            <Button variant="outline" size="icon-sm" aria-label={t("restart")} {...guardedButtonProps(canRestart)}>
              <RotateCw />
            </Button>
          </DangerConfirm>
        </RoleGuard>
      ),
    },
  ];

  return (
    <div className="space-y-4">
      <section className="grid gap-4 md:grid-cols-3">
        <MetricCard title={t("onlineNodes")} value={`${online}/${rows.length}`} detail="登録済みNode" tone={warning > 0 ? "warning" : "ok"} />
        <MetricCard title="稼働ジョブ" value={activeJobs} detail="現在処理中" />
        <MetricCard title={t("attentionRequired")} value={warning} detail="Heartbeatまたは状態に注意" tone={warning > 0 ? "danger" : "ok"} />
      </section>
      <Card>
        <CardHeader>
          <CardTitle>{t("workers")}</CardTitle>
        </CardHeader>
        <CardContent>
          <DataTable columns={columns} data={rows} filterPlaceholder="Node名・種別・状態で絞り込み" getRowId={(row) => row.id} />
        </CardContent>
      </Card>
    </div>
  );
}
