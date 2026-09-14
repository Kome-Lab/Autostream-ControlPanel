import "./component-loader.mts";
import assert from "node:assert/strict";
import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import test from "node:test";
const { ResourceTable } = await import("../../src/features/resources/resource-table.tsx");
const { I18nProvider } = await import("../../src/components/admin/i18n-provider.tsx");

test("UI-RESOURCE-001: real resource table renders a single copy/action tree per record", () => {
  const html = renderToStaticMarkup(createElement(I18nProvider, null, createElement(ResourceTable, {
    rows: [{ id: "one", name: "One" }, { id: "two", name: "Two" }], columns: ["name", "id"],
    resource: { title: "Records", description: "Resource records", path: "/profiles/encoder" },
    canEdit: false, canDelete: false, canTest: false, currentUser: undefined,
  })));
  assert.equal((html.match(/aria-label="IDをコピー"/g) || []).length, 2);
  assert.equal((html.match(/<table\b/g) || []).length, 1);
  assert.match(html, /ui-record-label/);
});
test("UI-RESOURCE-002: real server history keeps every returned row and total unknown", () => {
  const rows = Array.from({ length: 12 }, (_, index) => ({ id: String(index), name: "Incident " + index, status: "open" }));
  const html = renderToStaticMarkup(createElement(I18nProvider, null, createElement(ResourceTable, {
    rows, columns: ["name", "status"], resource: { title: "Incidents", description: "Incident history", path: "/observability/incidents" },
    canEdit: false, canDelete: false, canTest: false, currentUser: undefined,
  })));
  assert.equal((html.match(/aria-label="IDをコピー"/g) || []).length, 12);
  assert.doesNotMatch(html, /type="search"|data-slot="table-pagination"/);
});
