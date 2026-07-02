"use client";

import { useState } from "react";
import type { ColumnDef } from "@tanstack/react-table";
import { Download } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { DataTable } from "@/components/tables/data-table";
import { StatusBadge } from "@/components/admin/status-badge";
import { useAuditLogs } from "@/features/queries";
import { useI18n } from "@/components/admin/i18n-provider";
import type { AuditLog } from "@/types/domain";

export function AuditLogsView() {
  const { t } = useI18n();
  const [from, setFrom] = useState("");
  const [to, setTo] = useState("");
  const [result, setResult] = useState("all");
  const auditLogs = useAuditLogs({ from, to, result });

  const columns: ColumnDef<AuditLog>[] = [
    {
      accessorKey: "timestamp",
      header: t("time"),
      cell: ({ row }) => formatDateTime(row.original.timestamp),
    },
    { accessorKey: "actor_username", header: t("actor") },
    { accessorKey: "action", header: t("action") },
    {
      accessorKey: "result",
      header: t("result"),
      cell: ({ row }) => <StatusBadge status={row.original.result} />,
    },
    {
      id: "resource",
      header: t("resource"),
      cell: ({ row }) => (
        <div className="text-sm">
          <div>{row.original.resource_type || "-"}</div>
          <div className="text-muted-foreground">{row.original.resource_id || "-"}</div>
        </div>
      ),
    },
    { accessorKey: "actor_ip", header: "IP" },
    { accessorKey: "user_agent", header: t("userAgent") },
  ];

  const exportURL = `/audit-logs/export?${new URLSearchParams({
    ...(from ? { from } : {}),
    ...(to ? { to } : {}),
    ...(result !== "all" ? { result } : {}),
  }).toString()}`;

  return (
    <Card>
      <CardHeader className="gap-3 md:flex-row md:items-center md:justify-between">
        <CardTitle>{t("auditLogs")}</CardTitle>
        <Button asChild variant="outline" size="sm">
          <a href={exportURL}>
            <Download />
            CSV
          </a>
        </Button>
      </CardHeader>
      <CardContent className="space-y-4">
        <div className="grid gap-3 md:grid-cols-[180px_180px_160px]">
          <Input type="datetime-local" value={from} onChange={(event) => setFrom(event.target.value)} aria-label="from" />
          <Input type="datetime-local" value={to} onChange={(event) => setTo(event.target.value)} aria-label="to" />
          <Select value={result} onValueChange={setResult}>
            <SelectTrigger>
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="all">すべて</SelectItem>
              <SelectItem value="success">成功</SelectItem>
              <SelectItem value="failure">失敗</SelectItem>
            </SelectContent>
          </Select>
        </div>
        <DataTable columns={columns} data={auditLogs.data || []} filterPlaceholder="ユーザー・操作・対象で絞り込み" getRowId={(row) => row.id} />
      </CardContent>
    </Card>
  );
}

function formatDateTime(value?: string) {
  if (!value) return "-";
  return new Intl.DateTimeFormat("ja-JP", { year: "numeric", month: "2-digit", day: "2-digit", hour: "2-digit", minute: "2-digit" }).format(new Date(value));
}
