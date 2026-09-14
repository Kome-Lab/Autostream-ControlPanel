import "./component-loader.mts";
import assert from "node:assert/strict";
import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import test from "node:test";

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
