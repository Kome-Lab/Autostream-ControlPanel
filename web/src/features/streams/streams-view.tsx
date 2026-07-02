"use client";

import { useMutation, useQueryClient } from "@tanstack/react-query";
import type { ColumnDef } from "@tanstack/react-table";
import { Eye, Play, RotateCw, Square, Shuffle } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { DataTable } from "@/components/tables/data-table";
import { DangerConfirm } from "@/components/admin/danger-confirm";
import { RoleGuard, guardedButtonProps } from "@/components/admin/role-guard";
import { StatusBadge } from "@/components/admin/status-badge";
import { apiPost } from "@/lib/api/client";
import { hasPermission } from "@/lib/auth/permissions";
import { useCurrentUser, useStreams } from "@/features/queries";
import { useI18n } from "@/components/admin/i18n-provider";
import type { Stream } from "@/types/domain";

export function StreamsView() {
  const { t } = useI18n();
  const streams = useStreams();
  const currentUser = useCurrentUser();
  const queryClient = useQueryClient();

  const mutation = useMutation({
    mutationFn: ({ path }: { path: string }) => apiPost(path),
    onSuccess: async () => {
      await queryClient.invalidateQueries({ queryKey: ["streams"] });
    },
  });

  const canStart = hasPermission(currentUser.data, "streams.start");
  const canStop = hasPermission(currentUser.data, "streams.stop");
  const canUpdate = hasPermission(currentUser.data, "streams.update");

  const columns: ColumnDef<Stream>[] = [
    {
      accessorKey: "name",
      header: t("name"),
      cell: ({ row }) => (
        <div className="min-w-56">
          <div className="font-medium">{row.original.name}</div>
          <div className="text-xs text-muted-foreground">{row.original.id}</div>
        </div>
      ),
    },
    {
      accessorKey: "status",
      header: t("status"),
      cell: ({ row }) => <StatusBadge status={row.original.status} showDetail />,
    },
    { accessorKey: "input_source", header: t("input") },
    { accessorKey: "output_target", header: t("output") },
    {
      id: "assigned",
      header: t("assignedNode"),
      cell: ({ row }) => (
        <div className="text-sm">
          <div>{row.original.assigned_worker_id || "-"}</div>
          <div className="text-muted-foreground">{row.original.assigned_encoder_id || "-"}</div>
        </div>
      ),
    },
    {
      id: "schedule",
      header: t("scheduledTime"),
      cell: ({ row }) => <span className="text-sm">{formatTimeRange(row.original.scheduled_start_at, row.original.scheduled_end_at)}</span>,
    },
    {
      accessorKey: "started_at",
      header: t("startedAt"),
      cell: ({ row }) => <span className="text-sm">{formatDateTime(row.original.started_at)}</span>,
    },
    {
      id: "actions",
      header: t("actions"),
      cell: ({ row }) => (
        <div className="flex min-w-44 flex-nowrap gap-1">
          <Button variant="outline" size="icon-sm" aria-label={t("details")}>
            <Eye />
          </Button>
          <RoleGuard allowed={canStart}>
            <Button
              variant="outline"
              size="icon-sm"
              aria-label={t("start")}
              {...guardedButtonProps(canStart)}
              onClick={() => mutation.mutate({ path: `/streams/${row.original.id}/start` })}
            >
              <Play />
            </Button>
          </RoleGuard>
          <RoleGuard allowed={canStop}>
            <DangerConfirm title={`${row.original.name} を停止しますか`} onConfirm={() => mutation.mutate({ path: `/streams/${row.original.id}/stop` })} actionLabel={t("stop")}>
              <Button variant="outline" size="icon-sm" aria-label={t("stop")} {...guardedButtonProps(canStop)}>
                <Square />
              </Button>
            </DangerConfirm>
          </RoleGuard>
          <RoleGuard allowed={canUpdate}>
            <DangerConfirm title={`${row.original.name} を再起動しますか`} onConfirm={() => mutation.mutate({ path: `/streams/${row.original.id}/start-readiness` })} actionLabel={t("restart")}>
              <Button variant="outline" size="icon-sm" aria-label={t("restart")} {...guardedButtonProps(canUpdate)}>
                <RotateCw />
              </Button>
            </DangerConfirm>
          </RoleGuard>
          <RoleGuard allowed={canUpdate}>
            <DangerConfirm title={`${row.original.name} のWorkerを切り替えますか`} onConfirm={() => mutation.mutate({ path: `/streams/${row.original.id}/worker-events/test` })} actionLabel={t("switchWorker")}>
              <Button variant="outline" size="icon-sm" aria-label={t("switchWorker")} {...guardedButtonProps(canUpdate)}>
                <Shuffle />
              </Button>
            </DangerConfirm>
          </RoleGuard>
        </div>
      ),
    },
  ];

  return (
    <Card>
      <CardHeader>
        <CardTitle>{t("streams")}</CardTitle>
      </CardHeader>
      <CardContent>
        <DataTable columns={columns} data={streams.data || []} filterPlaceholder="配信名・Node・状態で絞り込み" getRowId={(row) => row.id} />
      </CardContent>
    </Card>
  );
}

function formatTimeRange(start?: string, end?: string) {
  return `${formatDateTime(start)} - ${formatDateTime(end)}`;
}

function formatDateTime(value?: string) {
  if (!value) return "-";
  return new Intl.DateTimeFormat("ja-JP", { month: "2-digit", day: "2-digit", hour: "2-digit", minute: "2-digit" }).format(new Date(value));
}
