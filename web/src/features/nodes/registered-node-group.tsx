"use client";

import type { ColumnDef } from "@tanstack/react-table";
import { Badge } from "@/components/ui/badge";
import { DataTable } from "@/components/tables/data-table";
import type { WorkerNode } from "@/types/domain";

export function RegisteredNodeGroup({
  title,
  description,
  rows,
  columns,
  filterPlaceholder = "Node名、種別、状態で検索",
}: {
  title: string;
  description: string;
  rows: WorkerNode[];
  columns: ColumnDef<WorkerNode>[];
  filterPlaceholder?: string;
}) {
  return (
    <section className="min-w-0 rounded-lg border bg-muted/10 p-3 sm:p-4" aria-label={title}>
      <div className="mb-3 flex flex-wrap items-start justify-between gap-2">
        <div className="min-w-0">
          <h3 className="font-medium">{title}</h3>
          <p className="mt-1 break-words text-xs text-muted-foreground">{description}</p>
        </div>
        <Badge variant="outline">{rows.length}件</Badge>
      </div>
      {rows.length > 0 ? (
        <DataTable
          columns={columns}
          data={rows}
          filterPlaceholder={filterPlaceholder}
          getRowId={(row) => row.service_id || row.id}
          responsive
        />
      ) : (
        <div className="rounded-md border border-dashed p-4 text-sm text-muted-foreground">対象のNodeは登録されていません。</div>
      )}
    </section>
  );
}
