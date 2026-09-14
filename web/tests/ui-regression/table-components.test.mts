import "./component-loader.mts";
import assert from "node:assert/strict";
import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import test from "node:test";
import type { Stream } from "../../src/types/domain.ts";
import type { StreamTablePresentation } from "../../src/features/streams/stream-table-cells.tsx";

const { DataTable } = await import("../../src/components/tables/data-table.tsx");
const { I18nProvider } = await import("../../src/components/admin/i18n-provider.tsx");

function renderTable(props: Parameters<typeof DataTable>[0]) {
  return renderToStaticMarkup(createElement(I18nProvider, null, createElement(DataTable, props)));
}

const columns = [
  { accessorKey: "name", header: "Name" },
  { accessorKey: "status", header: "Status" },
  { id: "actions", header: "Actions", cell: () => createElement("button", { "data-action-owner": "open" }, "Open") },
];
const data = Array.from({ length: 9 }, (_, index) => ({ name: `Stream ${index + 1}`, status: "ready" }));

test("UI-TABLE-001: actual table exposes sorting, column visibility and all specified page sizes", () => {
  const html = renderTable({ columns, data });
  assert.match(html, /aria-sort="none"/);
  assert.match(html, /data-slot="column-visibility"/);
  for (const size of [8, 20, 50, 100]) assert.match(html, new RegExp(`<option value="${size}"`));
  assert.equal((html.match(/data-action-owner="open"/g) || []).length, 8);
  assert.doesNotMatch(html, /Stream 9/);
});

test("UI-TABLE-002: mobile labels and desktop cells share one real action owner per row", () => {
  const html = renderTable({ columns, data: data.slice(0, 2), responsive: true });
  assert.match(html, /data-responsive="true"/);
  assert.match(html, /ui-record-label/);
  assert.equal((html.match(/data-action-owner="open"/g) || []).length, 2);
  assert.equal((html.match(/<table\b/g) || []).length, 1);
});

test("UI-TABLE-003: server mode never sorts, searches or repaginates the returned page", () => {
  const html = renderTable({ columns, data, mode: "server" });
  assert.equal((html.match(/data-action-owner="open"/g) || []).length, 9);
  assert.doesNotMatch(html, /type="search"|data-slot="table-pagination"|aria-sort=/);
  assert.match(html, /総数不明|total unknown/);
});

test("UI-STREAM-TABLE-001: actual stream cells retain component/row identity while locale, data and permission inputs stay live", async () => {
  const { StreamTableContext, streamTableColumns, streamRowID } = await import("../../src/features/streams/stream-table-cells.tsx");
  const { createStreamActionController } = await import("../../src/features/streams/stream-action-controller.ts");
  const { createUICopy } = await import("../../src/lib/i18n/ui-v2/copy.ts");
  let permissions = ["*"], mutationCalls = 0;
  const controller = createStreamActionController({ getPermissions: () => ({ kind: "ready", permissions }), getState: () => ({ kind: "ready", freshness: "fresh", fingerprint: "same-owner" }), mutate: async () => { mutationCalls++; } });
  const lifecycle = await import("../../src/features/streams/stream-lifecycle.ts");
  const presentation: StreamTablePresentation = { renderStreamStatus: row => createElement("span", null, row.status), lifecycle, uiText: createUICopy("ja"), t: key => key, ja: true, copiedStreamID: "", copyStreamID: async () => {},
    onDetails() {}, onEdit() {}, canUpdate: true, actionController: controller, handleStreamActionResult() {}, staticRelayOutputIDs: new Set(),
    youtubeOutputLabels: new Map(), archiveDestinationLabels: new Map(), archiveProfileLabels: new Map(), discordLabels: new Map() };
  const fresh: StreamTablePresentation = { ...presentation, uiText: createUICopy("en"), ja: false, canUpdate: false, copiedStreamID: "stable-stream", youtubeOutputLabels: new Map([["output-one", "Fresh output label"]]) };
  const a = streamTableColumns(presentation), b = streamTableColumns(fresh);
  const identity = (next: typeof a) => { assert.equal(next.length, 9); next.forEach((column, index) => assert.equal(column.cell, a[index].cell, "React cell component type must remain stable")); };
  identity(b); assert.throws(() => identity(b.map(column => ({ ...column, cell: () => null }))), /remain stable/);
  const stream = { id: "stable-stream", name: "Original name", status: "ready", youtube_output_id: "output-one" };
  const render = (owner: StreamTablePresentation, rows: typeof stream[]) => renderToStaticMarkup(createElement(I18nProvider, null,
    createElement(StreamTableContext.Provider, { value: owner }, createElement(DataTable<Stream, unknown>, { columns: streamTableColumns(owner), data: rows, getRowId: streamRowID }))));
  const initial = render(presentation, [stream]);
  permissions = []; const current = { ...stream, name: "Current row name" }, html = render(fresh, [current]);
  assert.equal(streamRowID(stream), streamRowID(current)); assert.match(html, /Current row name/); assert.doesNotMatch(html, /Original name/);
  assert.match(initial, /開始前に再確認/); assert.match(html, /Review before starting/); assert.match(html, /Fresh output label/);
  assert.equal((html.match(/data-slot="stream-primary-trigger"/g) || []).length, 1); assert.match(html, /data-stream-id="stable-stream"/);
  assert.notEqual(initial, html); assert.doesNotMatch(html, /aria-label="Current row name を開始"/);
  assert.equal(mutationCalls, 0, "render and permission refresh cannot create a mutation owner");
});
