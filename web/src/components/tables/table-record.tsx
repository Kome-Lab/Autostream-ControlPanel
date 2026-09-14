"use client";

import { useState } from "react";
import { flexRender, type Row } from "@tanstack/react-table";
import { useI18n } from "@/components/admin/i18n-provider";
import { TableCell, TableRow } from "@/components/ui/table";
import { columnLabel, type ColumnPresentation } from "./table-controls";

export function TableRecord<T>({ row, tableId }: { row: Row<T>; tableId: string }) {
  const { locale } = useI18n();
  const [expanded, setExpanded] = useState(false);
  const cells = row.getVisibleCells();
  const hasSecondary = cells.some((cell) => ((cell.column.columnDef.meta as ColumnPresentation | undefined)?.priority ?? 1) > 1);
  return <TableRow data-record-expanded={expanded}>
    {cells.map((cell) => {
      const priority = (cell.column.columnDef.meta as ColumnPresentation | undefined)?.priority ?? 1;
      return <TableCell key={cell.id} headers={`${tableId}-${cell.column.id}`} data-priority={priority > 1 ? "secondary" : "primary"}>
        <span className="ui-record-label">{columnLabel(cell.column)}</span>
        <div className="min-w-0 [overflow-wrap:anywhere]">{flexRender(cell.column.columnDef.cell, cell.getContext())}</div>
      </TableCell>;
    })}
    {hasSecondary ? <TableCell className="ui-record-disclosure">
      <button type="button" className="min-h-11 text-sm text-primary underline underline-offset-4" aria-expanded={expanded} onClick={() => setExpanded(!expanded)}>
        {expanded ? (locale === "ja" ? "補足情報を閉じる" : "Hide secondary fields") : (locale === "ja" ? "補足情報を表示" : "Show secondary fields")}
      </button>
    </TableCell> : null}
  </TableRow>;
}
