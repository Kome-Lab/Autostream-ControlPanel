"use client";

import { useSyncExternalStore } from "react";
import type { ColumnFiltersState, OnChangeFn, PaginationState, SortingState } from "@tanstack/react-table";
import { parseTableURL, type TableURLPolicy, type TableURLState } from "./table-state";
import { currentTableSearch, subscribeTableLocation, updateTableLocation } from "./table-location";

const initial: TableURLState = { pageIndex: 0, pageSize: 8, filters: [] };
const serverSearch = () => null;

// The URL owns only allowed presentation state. React hydrates from the server
// snapshot, then subscribes to real back/forward navigation without a second store.
export function useTableURL(policy?: TableURLPolicy) {
  const search = useSyncExternalStore(subscribeTableLocation, currentTableSearch, serverSearch);
  const value = policy ? parseTableURL(search || "", policy) : initial;
  const update = (change: (current: TableURLState) => TableURLState) => {
    if (policy) updateTableLocation(policy, change);
  };
  const onPaginationChange: OnChangeFn<PaginationState> = (updater) => update((current) => {
    const next = typeof updater === "function" ? updater(current) : updater;
    return { ...current, ...next };
  });
  const onSortingChange: OnChangeFn<SortingState> = (updater) => update((current) => {
    const next = typeof updater === "function" ? updater(current.sort ? [current.sort] : []) : updater;
    return { ...current, pageIndex: 0, sort: next[0] };
  });
  const onColumnFiltersChange: OnChangeFn<ColumnFiltersState> = (updater) => update((current) => {
    const next = typeof updater === "function" ? updater(current.filters) : updater;
    return { ...current, pageIndex: 0, filters: next.filter((filter): filter is { id: string; value: string } => typeof filter.value === "string") };
  });
  return {
    ready: !policy || search !== null,
    binding: policy ? {
      state: { pagination: { pageIndex: value.pageIndex, pageSize: value.pageSize }, sorting: value.sort ? [value.sort] : [], columnFilters: value.filters },
      onPaginationChange, onSortingChange, onColumnFiltersChange,
    } : {},
  };
}
