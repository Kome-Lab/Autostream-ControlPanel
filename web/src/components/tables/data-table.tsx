"use client";

import { useEffect, useId } from "react";
import {
  type ColumnDef, flexRender, getCoreRowModel, getFilteredRowModel,
  getPaginationRowModel, getSortedRowModel, useReactTable,
} from "@tanstack/react-table";
import { ArrowDown, ArrowUp, ArrowUpDown } from "lucide-react";
import { useI18n } from "@/components/admin/i18n-provider";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { boundedPageIndex, type TableURLPolicy } from "@/lib/ui-v2/table-state";
import { useTableURL } from "@/lib/ui-v2/use-table-url";
import { TablePagination, TableToolbar, type TableFilter } from "./table-controls";
import { TableRecord } from "./table-record";

type DataTableProps<TData, TValue> = {
  columns: ColumnDef<TData, TValue>[];
  data: TData[];
  filterPlaceholder?: string;
  getRowId?: (row: TData, index: number) => string;
  dataReady?: boolean;
  minTableWidthClass?: string;
  responsive?: boolean;
  mode?: "client" | "server";
  filters?: readonly TableFilter[];
  density?: "compact" | "standard" | "comfortable";
  urlPolicy?: TableURLPolicy;
};

const noFilters: readonly TableFilter[] = [];

export function DataTable<TData, TValue>({
  columns, data, filterPlaceholder, getRowId, minTableWidthClass = "min-w-[980px]",
  responsive = true, mode = "client", filters = noFilters, density = "standard", urlPolicy, dataReady = true,
}: DataTableProps<TData, TValue>) {
  const { locale } = useI18n();
  const tableId = useId();
  const url = useTableURL(mode === "client" ? urlPolicy : undefined);
  // eslint-disable-next-line react-hooks/incompatible-library
  const table = useReactTable({
    data, columns, getRowId,
    getCoreRowModel: getCoreRowModel(),
    getFilteredRowModel: getFilteredRowModel(),
    getSortedRowModel: getSortedRowModel(),
    getPaginationRowModel: getPaginationRowModel(),
    manualPagination: mode === "server", manualSorting: mode === "server", manualFiltering: mode === "server",
    enableSorting: mode === "client",
    autoResetPageIndex: !urlPolicy,
    initialState: { pagination: { pageSize: 8 } },
    ...url.binding,
  });
  const { pageIndex, pageSize } = table.getState().pagination;
  const filteredCount = table.getFilteredRowModel().rows.length;
  useEffect(() => {
    const next = boundedPageIndex(pageIndex, filteredCount, pageSize);
    if (dataReady && url.ready && mode === "client" && pageIndex !== next) table.setPageIndex(next);
  }, [table, pageIndex, pageSize, filteredCount, mode, url.ready, dataReady]);

  return <div data-slot="data-table" data-responsive={responsive} data-density={density} className="min-w-0 space-y-3">
    <TableToolbar table={table} filters={filters} mode={mode} filterPlaceholder={filterPlaceholder} />
    <p className="text-xs text-muted-foreground" role="status">
      {mode === "server"
        ? (locale === "ja" ? "表示中 " + data.length + " 件・総数不明" : data.length + " rows loaded · total unknown")
        : (locale === "ja" ? filteredCount + " / " + data.length + " 件" : filteredCount + " of " + data.length + " rows")}
    </p>
    <div className="min-w-0 rounded-md border bg-card">
      <Table className={minTableWidthClass}>
        <TableHeader>
          {table.getHeaderGroups().map((group) => <TableRow key={group.id}>
            {group.headers.map((header) => {
              const sorting = header.column.getIsSorted();
              const sortable = header.column.getCanSort();
              const Icon = sorting === "desc" ? ArrowDown : sorting === "asc" ? ArrowUp : ArrowUpDown;
              return <TableHead key={header.id} id={tableId + "-" + header.column.id} scope="col"
                aria-sort={sortable ? (sorting === "desc" ? "descending" : sorting === "asc" ? "ascending" : "none") : undefined}>
                {header.isPlaceholder ? null : sortable ? <button type="button"
                  className="flex min-h-11 items-center gap-2 text-left focus-visible:outline-2 focus-visible:outline-ring"
                  onClick={header.column.getToggleSortingHandler()}>
                  {flexRender(header.column.columnDef.header, header.getContext())}<Icon className="size-3.5" aria-hidden="true" />
                </button> : flexRender(header.column.columnDef.header, header.getContext())}
              </TableHead>;
            })}
          </TableRow>)}
        </TableHeader>
        <TableBody>
          {table.getRowModel().rows.length ? table.getRowModel().rows.map((row) => <TableRecord key={row.id} row={row} tableId={tableId} />) :
            <TableRow><TableCell colSpan={table.getVisibleLeafColumns().length} className="h-24 text-center text-muted-foreground">{locale === "ja" ? "該当するデータがありません" : "No results."}</TableCell></TableRow>}
        </TableBody>
      </Table>
    </div>
    {mode === "client" ? <TablePagination table={table} /> : null}
  </div>;
}
