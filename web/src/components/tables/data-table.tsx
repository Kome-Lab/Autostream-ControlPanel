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
  responsive?: boolean;
};

export function DataTable<TData, TValue>({
  columns,
  data,
  filterPlaceholder,
  getRowId,
  minTableWidthClass = "min-w-[1180px]",
  responsive = false,
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
    <div className="min-w-0 space-y-3">
      <div className="flex flex-wrap items-center gap-2">
        <div className="relative min-w-0 max-w-sm flex-1">
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
      {responsive ? (
        <div className="grid min-w-0 gap-3">
          {table.getRowModel().rows.length ? (
            table.getRowModel().rows.map((row) => (
              <article key={row.id} className="min-w-0 rounded-md border bg-card p-3 sm:p-4">
                <dl className="grid min-w-0 gap-x-4 gap-y-3 sm:grid-cols-2 xl:grid-cols-3">
                  {row.getVisibleCells().map((cell) => {
                    const header = table.getFlatHeaders().find((candidate) => candidate.column.id === cell.column.id);
                    const wide = cell.column.id === "endpoint" || cell.column.id === "actions";
                    return (
                      <div key={cell.id} className={`min-w-0 ${wide ? "sm:col-span-2 xl:col-span-3" : ""}`}>
                        <dt className="text-xs font-medium text-muted-foreground">
                          {header && !header.isPlaceholder ? flexRender(header.column.columnDef.header, header.getContext()) : null}
                        </dt>
                        <dd className="mt-1 min-w-0 break-words">
                          {flexRender(cell.column.columnDef.cell, cell.getContext())}
                        </dd>
                      </div>
                    );
                  })}
                </dl>
              </article>
            ))
          ) : (
            <div className="rounded-md border border-dashed p-6 text-center text-sm text-muted-foreground">
              {locale === "ja" ? "該当するデータがありません" : "No results."}
            </div>
          )}
        </div>
      ) : (
      <div className="min-w-0 overflow-hidden rounded-md border bg-card">
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
      )}
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
