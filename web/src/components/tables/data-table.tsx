"use client";

import {
  type ColumnDef,
  flexRender,
  getCoreRowModel,
  getFilteredRowModel,
  getPaginationRowModel,
  getSortedRowModel,
  useReactTable,
} from "@tanstack/react-table";
import { ChevronLeft, ChevronRight, Search } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { useI18n } from "@/components/admin/i18n-provider";

type DataTableProps<TData, TValue> = {
  columns: ColumnDef<TData, TValue>[];
  data: TData[];
  filterPlaceholder?: string;
  getRowId?: (row: TData) => string;
  minTableWidthClass?: string;
};

export function DataTable<TData, TValue>({
  columns,
  data,
  filterPlaceholder,
  getRowId,
  minTableWidthClass = "min-w-[1180px]",
}: DataTableProps<TData, TValue>) {
  const { locale, t } = useI18n();
  // eslint-disable-next-line react-hooks/incompatible-library
  const table = useReactTable({
    data,
    columns,
    getRowId,
    getCoreRowModel: getCoreRowModel(),
    getFilteredRowModel: getFilteredRowModel(),
    getSortedRowModel: getSortedRowModel(),
    getPaginationRowModel: getPaginationRowModel(),
    initialState: { pagination: { pageSize: 8 } },
  });

  return (
    <div className="space-y-3">
      <div className="flex items-center gap-2">
        <div className="relative max-w-sm flex-1">
          <Search className="pointer-events-none absolute top-1/2 left-3 size-4 -translate-y-1/2 text-muted-foreground" aria-hidden="true" />
          <Input
            value={(table.getState().globalFilter as string) ?? ""}
            onChange={(event) => table.setGlobalFilter(event.target.value)}
            placeholder={filterPlaceholder || t("filter")}
            aria-label={locale === "ja" ? "一覧を検索" : "Search table"}
            className="pl-9"
          />
        </div>
        <span className="text-xs whitespace-nowrap text-muted-foreground">
          {locale === "ja" ? `${table.getFilteredRowModel().rows.length} / ${data.length} 件` : `${table.getFilteredRowModel().rows.length} of ${data.length} rows`}
        </span>
      </div>
      <div className="overflow-hidden rounded-md border bg-card">
        <Table className={minTableWidthClass}>
          <TableHeader>
            {table.getHeaderGroups().map((headerGroup) => (
              <TableRow key={headerGroup.id}>
                {headerGroup.headers.map((header) => (
                  <TableHead key={header.id}>
                    {header.isPlaceholder ? null : flexRender(header.column.columnDef.header, header.getContext())}
                  </TableHead>
                ))}
              </TableRow>
            ))}
          </TableHeader>
          <TableBody>
            {table.getRowModel().rows.length ? (
              table.getRowModel().rows.map((row) => (
                <TableRow key={row.id}>
                  {row.getVisibleCells().map((cell) => (
                    <TableCell key={cell.id}>{flexRender(cell.column.columnDef.cell, cell.getContext())}</TableCell>
                  ))}
                </TableRow>
              ))
            ) : (
              <TableRow>
                <TableCell colSpan={columns.length} className="h-24 text-center text-muted-foreground">
                  {locale === "ja" ? "該当するデータがありません" : "No results."}
                </TableCell>
              </TableRow>
            )}
          </TableBody>
        </Table>
      </div>
      <div className="flex items-center justify-between gap-2">
        <span className="text-xs text-muted-foreground">
          {locale === "ja" ? `${table.getState().pagination.pageIndex + 1} / ${table.getPageCount() || 1} ページ` : `Page ${table.getState().pagination.pageIndex + 1} of ${table.getPageCount() || 1}`}
        </span>
        <div className="flex items-center gap-1">
          <Button variant="outline" size="icon-sm" onClick={() => table.previousPage()} disabled={!table.getCanPreviousPage()} aria-label={locale === "ja" ? "前のページ" : "Previous page"}>
            <ChevronLeft />
          </Button>
          <Button variant="outline" size="icon-sm" onClick={() => table.nextPage()} disabled={!table.getCanNextPage()} aria-label={locale === "ja" ? "次のページ" : "Next page"}>
            <ChevronRight />
          </Button>
        </div>
      </div>
    </div>
  );
}
