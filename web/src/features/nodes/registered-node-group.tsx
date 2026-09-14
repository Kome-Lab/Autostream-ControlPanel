"use client";

import type { ColumnDef } from "@tanstack/react-table";
import { DetailSection } from "@/components/layout/detail-section";
import { useI18n } from "@/components/admin/i18n-provider";
import { Badge } from "@/components/ui/badge";
import { DataTable } from "@/components/tables/data-table";
import type { WorkerNode } from "@/types/domain";

export function RegisteredNodeGroup({
  title,
  description,
  rows,
  columns,
  filterPlaceholder,
}: {
  title: string;
  description: string;
  rows: WorkerNode[];
  columns: ColumnDef<WorkerNode>[];
  filterPlaceholder?: string;
}) {
  const { locale } = useI18n();
  return (
    <DetailSection title={title} description={description} actions={<Badge variant="outline">{rows.length}</Badge>}>
      {rows.length > 0 ? (
        <DataTable
          columns={columns}
          data={rows}
          filterPlaceholder={filterPlaceholder ?? (locale === "ja" ? "Node名、種別、状態で検索" : "Search node name, type or status")}
          getRowId={(row) => row.service_id || row.id}
          responsive
        />
      ) : (
        <div className="rounded-md border border-dashed p-4 text-sm text-muted-foreground">{locale === "ja" ? "対象のNodeは登録されていません。" : "No registered nodes in this group."}</div>
      )}
    </DetailSection>
  );
}
