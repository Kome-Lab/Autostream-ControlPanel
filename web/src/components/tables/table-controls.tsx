"use client";

import type { Column, Table } from "@tanstack/react-table";
import { ChevronLeft, ChevronRight, Search } from "lucide-react";
import { useI18n } from "@/components/admin/i18n-provider";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { tablePageSizes, tablePageSize } from "@/lib/ui-v2/table-state";

export type TableFilter = { id: string; label: string; options: readonly { value: string; label: string }[] };
export type ColumnPresentation = { label?: string; priority?: 0 | 1 | 2 | 3; required?: boolean };

export function columnLabel<T>(column: Column<T, unknown>): string {
  const meta = column.columnDef.meta as ColumnPresentation | undefined;
  return meta?.label || (typeof column.columnDef.header === "string" ? column.columnDef.header : column.id);
}

export function requiredColumn<T>(column: Column<T, unknown>, first: string): boolean {
  const meta = column.columnDef.meta as ColumnPresentation | undefined;
  return meta?.required === true || column.id === first || ["name", "id", "status", "actions"].includes(column.id);
}

const selectClass = "h-10 max-w-full min-w-0 rounded-md border border-input bg-background px-2 text-sm focus-visible:outline-2 focus-visible:outline-ring";

export function TableToolbar<T>({ table, filterPlaceholder, filters, mode }: {
  table: Table<T>; filterPlaceholder?: string; filters: readonly TableFilter[]; mode: "client" | "server";
}) {
  const { locale, t } = useI18n();
  const ja = locale === "ja";
  const columns = table.getAllLeafColumns();
  const first = columns[0]?.id || "";
  const sort = table.getState().sorting[0];
  return (
    <div data-slot="table-toolbar" className="flex min-w-0 flex-wrap items-end gap-3">
      {mode === "client" ? <>
        <label className="relative min-w-48 max-w-sm flex-1">
          <span className="sr-only">{ja ? "一覧を検索" : "Search table"}</span>
          <Search className="pointer-events-none absolute top-1/2 left-3 size-4 -translate-y-1/2 text-muted-foreground" aria-hidden="true" />
          <Input type="search" value={(table.getState().globalFilter as string) ?? ""}
            onChange={(event) => { table.setGlobalFilter(event.target.value); table.setPageIndex(0); }}
            placeholder={filterPlaceholder || t("filter")} className="pl-9" />
        </label>
        {filters.map((filter) => <label key={filter.id} className="grid min-w-0 gap-1 text-xs text-muted-foreground">
          {filter.label}
          <select className={selectClass} value={String(table.getColumn(filter.id)?.getFilterValue() ?? "")}
            onChange={(event) => { table.getColumn(filter.id)?.setFilterValue(event.target.value || undefined); table.setPageIndex(0); }}>
            <option value="">{ja ? "すべて" : "All"}</option>
            {filter.options.map((option) => <option key={option.value} value={option.value}>{option.label}</option>)}
          </select>
        </label>)}
        <label className="grid min-w-0 gap-1 text-xs text-muted-foreground">
          {ja ? "並べ替え" : "Sort"}
          <select className={selectClass} value={sort?.id || ""}
            onChange={(event) => { table.setSorting(event.target.value ? [{ id: event.target.value, desc: false }] : []); table.setPageIndex(0); }}>
            <option value="">{ja ? "元の順序" : "Original order"}</option>
            {columns.filter((column) => column.getCanSort()).map((column) => <option key={column.id} value={column.id}>{columnLabel(column)}</option>)}
          </select>
        </label>
        {sort ? <Button type="button" variant="outline" onClick={() => table.setSorting([{ ...sort, desc: !sort.desc }])}>
          {sort.desc ? (ja ? "降順" : "Descending") : (ja ? "昇順" : "Ascending")}
        </Button> : null}
      </> : null}
      <details data-slot="column-visibility" className="relative max-w-full">
        <summary className="min-h-10 cursor-pointer rounded-md border px-3 py-2 text-sm focus-visible:outline-2 focus-visible:outline-ring">{ja ? "表示する列" : "Columns"}</summary>
        <div className="absolute right-0 z-10 mt-1 max-h-80 w-60 max-w-[80vw] overflow-y-auto rounded-md border bg-popover p-2 text-popover-foreground shadow-md">
          {columns.map((column) => <label key={column.id} className="flex min-h-11 items-center gap-2 px-2 text-sm">
            <input type="checkbox" checked={column.getIsVisible()} disabled={requiredColumn(column, first) || !column.getCanHide()}
              onChange={(event) => column.toggleVisibility(event.target.checked)} />
            {columnLabel(column)}
          </label>)}
        </div>
      </details>
    </div>
  );
}

export function TablePagination<T>({ table }: { table: Table<T> }) {
  const { locale } = useI18n();
  const ja = locale === "ja";
  return <div data-slot="table-pagination" className="flex flex-wrap items-center justify-between gap-3">
    <label className="flex items-center gap-2 text-sm">
      {ja ? "表示件数" : "Rows per page"}
      <select className={selectClass} value={table.getState().pagination.pageSize}
        onChange={(event) => table.setPageSize(tablePageSize(Number(event.target.value)))}>
        {tablePageSizes.map((size) => <option key={size} value={size}>{size}</option>)}
      </select>
    </label>
    <span className="text-xs text-muted-foreground" aria-live="polite">
      {ja ? `${table.getState().pagination.pageIndex + 1} / ${table.getPageCount() || 1} ページ` : `Page ${table.getState().pagination.pageIndex + 1} of ${table.getPageCount() || 1}`}
    </span>
    <div className="flex gap-2">
      <Button type="button" variant="outline" size="icon" onClick={() => table.previousPage()} disabled={!table.getCanPreviousPage()} aria-label={ja ? "前のページ" : "Previous page"}><ChevronLeft /></Button>
      <Button type="button" variant="outline" size="icon" onClick={() => table.nextPage()} disabled={!table.getCanNextPage()} aria-label={ja ? "次のページ" : "Next page"}><ChevronRight /></Button>
    </div>
  </div>;
}
