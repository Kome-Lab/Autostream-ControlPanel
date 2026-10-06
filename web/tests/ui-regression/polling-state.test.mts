import "./component-loader.mts";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import test from "node:test";
import { createTable, functionalUpdate, type ColumnDef, type TableOptions, type TableState } from "@tanstack/react-table";
import { renderedSource } from "./source-render.mts";
import { renderUI } from "./render-ui.mts";
import type { Stream } from "../../src/types/domain.ts";

const { resourceTableColumns, ResourceTable } = await import("../../src/features/resources/resource-table.tsx");
const { dashboardTableColumns, DashboardStreams } = await import("../../src/features/dashboard/dashboard-streams.tsx");
const { workerTableColumns, WorkersView } = await import("../../src/features/workers/workers-view.tsx");
const { createRegisteredNodeColumns } = await import("../../src/features/nodes/registered-node-columns.tsx");
const { I18nProvider } = await import("../../src/components/admin/i18n-provider.tsx");
const { createUICopy } = await import("../../src/lib/i18n/ui-v2/copy.ts");
const translate = (locale: string) => (key: string) => `${locale}:${key}`;

function sameCells<T>(before: ColumnDef<T>[], after: ColumnDef<T>[]) {
  assert.equal(after.length, before.length);
  before.forEach((column, index) => assert.equal(after[index].cell, column.cell, `stable React cell component at ${index}`));
}

test("polling keeps all resource, dashboard, worker and registered-node cell component identities", () => {
  const ja = createUICopy("ja"), en = createUICopy("en");
  const pairs = [
    [resourceTableColumns(["name", "status"], "name", "ja", ja, translate("ja"), true), resourceTableColumns(["name", "status"], "name", "en", en, translate("en"), true)],
    [dashboardTableColumns(translate("ja"), true), dashboardTableColumns(translate("en"), false)],
    [workerTableColumns(translate("ja"), "ja"), workerTableColumns(translate("en"), "en")],
    [createRegisteredNodeColumns({ t: translate("ja") }, ja), createRegisteredNodeColumns({ t: translate("en") }, en)],
  ];
  for (const [before, after] of pairs) {
    sameCells(before as ColumnDef<unknown>[], after as ColumnDef<unknown>[]);
    assert.notEqual(before[0].header, after[0].header, "presentation changes remain live");
    assert.throws(() => sameCells(before as ColumnDef<unknown>[], after.map(column => ({ ...column, cell: () => null })) as ColumnDef<unknown>[]), /stable React cell/);
  }
  const fallback = resourceTableColumns(["status"], undefined, "ja", ja, translate("ja"), true);
  sameCells(fallback, resourceTableColumns(["status"], undefined, "en", en, translate("en"), true));
});

test("stable resource and dashboard cells render current row, labels and permissions without freezing old closures", () => {
  const resource = { title: "Profiles", description: "Profiles", path: "/profiles/encoder", form: "encoder-profile" as const, permissions: { read: "encoder_profiles.read", update: "encoder_profiles.update" } };
  // A controller object is sufficient for rendering closed edit controls; no operation is dispatched.
  const controller = {} as Parameters<typeof ResourceTable>[0]["resourceActionController"];
  const props = { resource, columns: ["name", "status"], rows: [{ id: "stable-row", name: "Before", status: "ready" }], canEdit: true, canDelete: false, canTest: false, currentUser: undefined, resourceActionController: controller };
  const before = renderUI(createElement(ResourceTable, props), "ja");
  const after = renderUI(createElement(ResourceTable, { ...props, canEdit: false, rows: [{ ...props.rows[0], name: "After" }] }), "en");
  assert.match(before, /Before/); assert.doesNotMatch(after, /Before/); assert.match(after, /After/);
  const edit = after.match(/<button[^>]*aria-label="Edit After"[^>]*>/)?.[0];
  assert.ok(edit, "current row edit label"); assert.match(edit, /disabled/);
  const stream = { id: "stream-1", name: "Before stream", status: "ready", assigned_worker_id: "worker-1" } as Stream;
  const dashboard = renderUI(createElement(DashboardStreams, { rows: [{ ...stream, name: "After stream" }], serviceNames: new Map([["worker-1", "Current worker"]]) }), "en");
  assert.match(dashboard, /After stream/); assert.match(dashboard, /Current worker/); assert.doesNotMatch(dashboard, /Before stream/);
});

test("Workers retains cached readable rows after permission refresh failure, and removes rows after confirmed revocation", () => {
  const render = (state: "ready" | "refreshing" | "error" | "revoked") => renderUI(createElement(WorkersView), "en", "/admin/workers/", client => {
    client.setQueryData(["workers"], [{ id: "cached-worker", service_id: "cached-worker", service_name: "Cached readable worker", service_type: "worker", status: "online" }]);
    client.setQueryData(["nodes"], []); client.setQueryData(["service-health"], []);
    const authority = client.getQueryCache().find({ queryKey: ["auth", "me"] });
    assert.ok(authority);
    if (state === "error") authority.setState({ status: "error", error: new Error("Synthetic refresh failure") });
    if (state === "refreshing") authority.setState({ fetchStatus: "fetching" });
    if (state === "revoked") client.setQueryData(["auth", "me"], { ...(authority.state.data as object), permissions: [] });
  });
  for (const state of ["ready", "refreshing", "error"] as const) assert.match(render(state), /Cached readable worker/, state);
  assert.doesNotMatch(render("revoked"), /Cached readable worker/);
  assert.match(render("error"), /read permissions are unavailable/);
});

// Capture the actual DataTable options during SSR, then exercise the real TanStack
// state owner. This does not simulate DOM focus or claim browser dialog coverage.
async function tableOptions(resetMutant = false): Promise<TableOptions<{ id: string; name: string }>> {
  const url = new URL("../../src/components/tables/data-table.tsx", import.meta.url);
  let source = readFileSync(url, "utf8");
  if (resetMutant) source = source.replace("autoResetPageIndex: false", "autoResetPageIndex: true");
  source = source.replace("useReactTable({", "useReactTable(captureOptions({")
    .replace("    ...url.binding,\n  });", "    ...url.binding,\n  }));")
    + "\nlet capturedOptions: unknown; function captureOptions<T>(options: T): T { capturedOptions = options; return options; } export function readOptions() { return capturedOptions; }\n";
  const renderedModule = await renderedSource(source, url);
  renderToStaticMarkup(createElement(I18nProvider, null, createElement(renderedModule.DataTable, {
    columns: [{ accessorKey: "name", header: "Name" }],
    data: Array.from({ length: 25 }, (_, i) => ({ id: String(i), name: "Row " + i })),
    getRowId: (row: { id: string }) => row.id,
  })));
  return renderedModule.readOptions();
}
async function pollPages(options: Awaited<ReturnType<typeof tableOptions>>) {
  let state = {} as TableState;
  const table = createTable({ ...options, state, renderFallbackValue: null, onStateChange: updater => {
    state = functionalUpdate(updater, state);
    table.setOptions(current => ({ ...current, state }));
  } });
  state = table.initialState; table.setOptions(current => ({ ...current, state }));
  const settle = async () => { table.getRowModel(); await new Promise(resolve => setImmediate(resolve)); };
  await settle(); table.setPageIndex(2); await settle();
  const pages: number[] = [];
  for (const event of ["query-start", "query-success", "query-error-retained-data", "permission-poll"]) {
    table.setOptions(current => ({ ...current, data: options.data.map(row => ({ ...row, name: row.name + event })) }));
    await settle(); pages.push(table.getState().pagination.pageIndex);
  }
  // Existing user controls explicitly reset filters, while setPageSize preserves
  // the first visible record under the new size using TanStack's normal behavior.
  table.setGlobalFilter("Row"); table.setPageIndex(0); await settle();
  assert.equal(table.getState().pagination.pageIndex, 0);
  table.setPageIndex(2); table.setPageSize(20); await settle();
  assert.equal(table.getState().pagination.pageSize, 20);
  assert.equal(table.getState().pagination.pageIndex, 0);
  return pages;
}

test("actual non-URL table pagination survives polling data references; old auto-reset reproduces the loss", async () => {
  assert.deepEqual(await pollPages(await tableOptions()), [2, 2, 2, 2]);
  assert.deepEqual(await pollPages(await tableOptions(true)), [0, 0, 0, 0], "negative control reproduces the previous automatic reset");
});
