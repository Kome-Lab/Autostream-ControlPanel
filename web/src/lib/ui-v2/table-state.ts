export const tablePageSizes = [8, 20, 50, 100] as const;

export function tablePageSize(value: number): number {
  return tablePageSizes.some((size) => size === value) ? value : 8;
}

export function boundedPageIndex(index: number, rows: number, size: number): number {
  const last = Math.max(0, Math.ceil(Math.max(0, rows) / tablePageSize(size)) - 1);
  return Number.isSafeInteger(index) ? Math.max(0, Math.min(index, last)) : 0;
}

export type TableURLPolicy = {
  key: string;
  sorts: readonly string[];
  filters: Readonly<Record<string, readonly string[]>>;
};

export type TableURLState = {
  pageIndex: number;
  pageSize: number;
  sort?: { id: string; desc: boolean };
  filters: { id: string; value: string }[];
};

function finiteIndex(raw: string | null) {
  if (!raw || !/^\d{1,5}$/.test(raw)) return 0;
  return Math.min(10000, Number(raw));
}

export function parseTableURL(search: string, policy: TableURLPolicy): TableURLState {
  const params = new URLSearchParams(search);
  const prefix = policy.key + ".";
  const sort = params.get(prefix + "sort");
  return {
    pageIndex: finiteIndex(params.get(prefix + "page")),
    pageSize: tablePageSize(Number(params.get(prefix + "size"))),
    sort: sort && policy.sorts.includes(sort) ? { id: sort, desc: params.get(prefix + "order") === "desc" } : undefined,
    filters: Object.entries(policy.filters).flatMap(([id, allowed]) => {
      const value = params.get(prefix + "filter." + id);
      return value && allowed.includes(value) ? [{ id, value }] : [];
    }),
  };
}

export function writeTableURL(search: string, state: TableURLState, policy: TableURLPolicy): string {
  const params = new URLSearchParams(search);
  const prefix = policy.key + ".";
  for (const key of ["page", "size", "sort", "order", ...Object.keys(policy.filters).map((id) => "filter." + id)]) params.delete(prefix + key);
  const index = finiteIndex(String(state.pageIndex));
  if (index) params.set(prefix + "page", String(index));
  const size = tablePageSize(state.pageSize);
  if (size !== 8) params.set(prefix + "size", String(size));
  if (state.sort && policy.sorts.includes(state.sort.id)) {
    params.set(prefix + "sort", state.sort.id);
    params.set(prefix + "order", state.sort.desc ? "desc" : "asc");
  }
  for (const filter of state.filters) {
    if (policy.filters[filter.id]?.includes(filter.value)) params.set(prefix + "filter." + filter.id, filter.value);
  }
  return params.toString();
}
