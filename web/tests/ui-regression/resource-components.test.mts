import "./component-loader.mts";
import assert from "node:assert/strict";
import { createElement, Fragment } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import test from "node:test";
import { renderUI } from "./render-ui.mts";
import { readFileSync } from "node:fs";
import { renderedSource } from "./source-render.mts";
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

test("UI-RESOURCE-IDENTITY-034: family identity stays primary and cannot be hidden independently of action permission", async () => {
  const { resourcePages } = await import("../../src/features/resources/resource-config.ts");
  const { visibleColumns, formatResourceCell } = await import("../../src/features/resources/resource-presentation.tsx");
  const { createUICopy } = await import("../../src/lib/i18n/ui-v2/copy.ts");
  const cases = [
    [resourcePages.users.resources[0], "username"], [resourcePages.incidents.resources[0], "title"],
    [resourcePages.diagnostics.resources[0], "rule"], [resourcePages.remediation.resources[0], "action"],
    [resourcePages.notifications.resources[0], "event_name"], [resourcePages.notifications.resources[1], "name"],
    [resourcePages["service-health"].resources[0], "service_name"], [resourcePages.roles.resources[0], "name"],
    [resourcePages.integrations.resources[1], "oauth_account_display_name"],
  ] as const;
  for (const [resource, key] of cases) for (const locale of ["ja", "en"] as const) for (const canEdit of [false, true]) {
    const row = { id: "record-id", [key]: "IDENTITY-公開名-"+"a".repeat(180), status: "open", updated_at: "2026-09-01T00:00:00Z" };
    const props = { rows: [row], columns: visibleColumns([row], resource), resource, canEdit, canDelete: false, canTest: false, currentUser: undefined };
    const check = (html: string) => {
      const cell = html.match(new RegExp(`<td[^>]*headers="[^"]*-${key}"[^>]*>[\\s\\S]*?</td>`))?.[0];
      assert.ok(cell, resource.path+": real identity column");
      assert.match(cell, /data-priority="primary"/, resource.path+": collapsed identity");
      const expected = renderToStaticMarkup(createElement(Fragment, null, formatResourceCell(resource, row[key], key, undefined, createUICopy(locale))));
      assert.ok(cell.includes(expected), "complete identity uses the unchanged safe formatter");
      assert.match(html, new RegExp(`<input[^>]*data-column-id="${key}"[^>]*disabled`));
      assert.equal((html.match(/aria-label="(?:IDをコピー|Copy ID)"/g) || []).length, 1);
    };
    const html = renderUI(createElement(ResourceTable, props), locale);
    check(html);
    assert.throws(() => check(html.replace(new RegExp(`(headers="[^"]*-${key}"[^>]*data-priority=")primary`), "$1secondary")), /collapsed identity/);
  }
  const css = readFileSync(new URL("../../src/app/globals.css", import.meta.url), "utf8");
  assert.match(css, /tr:not\(\[data-record-expanded="true"\]\) td\[data-priority="secondary"\] \{ display: none; \}/);
  assert.match(css, /td\[data-priority="secondary"\]:focus-within \{ display: block; \}/);
  const unknown = { title: "Unknown", description: "Unknown resource", path: "/future" };
  for (const row of [{ id: "safe-id", status: "unknown" }, { status: "unknown", api_secret: "MUST-NOT-BECOME-IDENTITY" }]) {
    const html = renderUI(createElement(ResourceTable, { rows: [row], columns: ["status"], resource: unknown, canEdit: false, canDelete: false, canTest: false, currentUser: undefined }), "en");
    assert.match(html, /data-column-id="record-identity"[^>]*disabled/);
    assert.match(html, /safe-id|this item/i);
    assert.doesNotMatch(html, /MUST-NOT-BECOME-IDENTITY/);
  }
});

test("UI-RESOURCE-PERMISSION-COPY-034: real denied notice uses one localized sentence with action spacing", async () => {
  const { PermissionNotice } = await import("../../src/features/resources/resource-notices.tsx");
  const { resourcePages } = await import("../../src/features/resources/resource-config.ts");
  const props = { resource: resourcePages.youtube.resources[0], action: "View", permission: "youtube_outputs.read" };
  const check = (html: string) => { assert.match(html, /to View this item/); assert.doesNotMatch(html, /toViewthis/); };
  check(renderUI(createElement(PermissionNotice, props), "en"));
  const url = new URL("../../src/features/resources/resource-notices.tsx", import.meta.url);
  const source = readFileSync(url, "utf8");
  const mutant = await renderedSource(source.replace('uiText("この項目を{0}する権限がありません。", action)', 'uiText("この項目を")}{action}{uiText("する権限がありません。")'), url);
  assert.throws(() => check(renderUI(createElement(mutant.PermissionNotice, props), "en")));
  assert.match(renderUI(createElement(PermissionNotice, { ...props, action: "参照" }), "ja"), /この項目を参照する権限がありません。/);
});
