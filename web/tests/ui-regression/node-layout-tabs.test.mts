import "./component-loader.mts";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { createElement, Fragment } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import test from "node:test";
import postcss, { type Root } from "postcss";
import tailwind from "@tailwindcss/postcss";
import { renderUI } from "./render-ui.mts";
import type { WorkerNode } from "../../src/types/domain.ts";

const { Tabs, TabsList, TabsTrigger, TabsContent } = await import("../../src/components/ui/tabs.tsx");
const { NodeEndpointStateView } = await import("../../src/features/nodes/node-endpoint-state-view.tsx");
const { NodeStateDetails } = await import("../../src/features/nodes/node-state-details.tsx");
const { RegisteredNodeGroup } = await import("../../src/features/nodes/registered-node-group.tsx");
const { WorkersView } = await import("../../src/features/workers/workers-view.tsx");
const { AuditLogsView } = await import("../../src/features/audit/audit-logs-view.tsx");
const { formatResourceCell } = await import("../../src/features/resources/resource-presentation.tsx");

const cssPath = fileURLToPath(new URL("../../src/app/globals.css", import.meta.url));
const css = (await postcss([tailwind({ base: fileURLToPath(new URL("../../", import.meta.url)), optimize: false })])
  .process(readFileSync(cssPath, "utf8"), { from: cssPath })).root;

function classes(html: string) {
  return [...html.matchAll(/class="([^"]*)"/g)].flatMap(match => match[1]
    .replaceAll("&gt;", ">").replaceAll("&lt;", "<").replaceAll("&amp;", "&").split(" "));
}

// Inspect declarations emitted for the real rendered classes, including group
// variants. A plain h-auto class cannot neutralize a fixed group-variant height.
function declarations(root: Root, html: string, property: string, elementOnly = false) {
  const selected = new Set(classes(html).map(value => "." + value));
  const values: string[] = [];
  root.walkRules(rule => {
    const selector = rule.selector.replace(/\\(.)/g, "$1");
    if ([...selected].some(candidate => {
      if (!(selector === candidate || selector.startsWith(candidate + " ") || selector.startsWith(candidate + ":"))) return false;
      const suffix = selector.slice(candidate.length);
      return !elementOnly || (!suffix.startsWith(" ") && !suffix.includes("::"));
    })) {
      rule.walkDecls(property, declaration => { values.push(declaration.value); });
    }
  });
  return values;
}

const node: WorkerNode = {
  id: "layout-node", service_id: "enc-rec-layout", service_type: "encoder",
  service_name: "Encoder layout fixture", status: "online", health_status: "healthy",
  public_url: "https://encoder.example.test:51378", metrics: { active_jobs: 0 },
};

test("UI-LAYOUT-174: tabs grow with their labels in both orientations and preserve Radix tab/panel references", t => {
  for (const orientation of ["horizontal", "vertical"] as const) for (const variant of ["default", "line"] as const) {
    const html = renderToStaticMarkup(createElement(Tabs, { defaultValue: "first", orientation },
      createElement(TabsList, { variant, className: "h-auto flex-wrap" },
        createElement(TabsTrigger, { value: "first" }, "Discord BOT設定"),
        createElement(TabsTrigger, { value: "second" }, "通知ターゲット・接続先の長いタブラベル")),
      createElement(TabsContent, { value: "first" }, "Visible panel"),
      createElement(TabsContent, { value: "second" }, "Other panel")));
    const list = html.match(/<div[^>]*data-slot="tabs-list"[^>]*>/)?.[0];
    const triggers = [...html.matchAll(/<button[^>]*data-slot="tabs-trigger"[^>]*>/g)].map(match => match[0]);
    assert.ok(list); assert.equal(triggers.length, 2);
    assert.match(list, new RegExp(`aria-orientation="${orientation}"`));
    assert.match(list, new RegExp(`data-variant="${variant}"`));
    for (const element of [list, ...triggers]) {
      const heights = declarations(css, element, "height", true);
      assert.ok(heights.length > 0);
      assert.ok(heights.every(value => ["auto", "fit-content"].includes(value)), "multiline tabs cannot inherit a fixed or percentage height");
    }
    for (const trigger of triggers) {
      assert.ok(declarations(css, trigger, "flex", true).every(value => value !== "1" && value !== "1 1 0%"), "labels must retain their own intrinsic width");
      const id = trigger.match(/\bid="([^"]+)"/)?.[1];
      const panel = trigger.match(/aria-controls="([^"]+)"/)?.[1];
      assert.ok(id && panel);
      assert.match(html, new RegExp(`<div[^>]*role="tabpanel"[^>]*aria-labelledby="${id}"[^>]*id="${panel}"`));
    }
  }
  t.diagnostic("Actual Radix SSR and emitted height/flex declarations; browser wrapping and keyboard movement are integration checks.");
});

test("UI-LAYOUT-174: compact endpoints have no viewport-based label column and keep complete values and copy ownership", () => {
  for (const locale of ["ja", "en"] as const) {
    const html = renderUI(createElement(NodeEndpointStateView, { node, compact: true, copied: "", onCopy: async () => { assert.fail("render cannot copy"); } }), locale);
    assert.ok(html.includes(node.public_url!));
    assert.equal((html.match(/<button\b/g) || []).length, 1);
    assert.match(html, /aria-label="[^"]*(?:コピー|Copy)[^"]*"/);
    assert.ok(declarations(css, html, "grid-template-columns").every(value => !value.includes("7rem")), "compact cell must not reserve seven rem for labels at a wide viewport");
    assert.match(html, /shrink-0/);
    assert.match(html, locale === "ja" ? /希望値/ : /Desired/);
    assert.match(html, locale === "ja" ? /Node報告/ : /Node report/);
  }
  const missing = renderUI(createElement(NodeEndpointStateView, { node: { ...node, public_url: "" }, compact: true, copied: "", onCopy: async () => {} }), "ja");
  assert.equal((missing.match(/<button\b/g) || []).length, 0);
});

test("UI-LAYOUT-174: node state fields retain all five meanings without a viewport-driven two-column squeeze", () => {
  for (const locale of ["ja", "en"] as const) {
    const html = renderUI(createElement(NodeStateDetails, { node }), locale);
    assert.equal((html.match(/<dt\b/g) || []).length, 5);
    assert.equal((html.match(/<dd\b/g) || []).length, 5);
    assert.ok(declarations(css, html, "grid-template-columns").every(value => value !== "repeat(2, minmax(0, 1fr))"));
    assert.ok(declarations(css, html, "flex-wrap").includes("wrap"));
    assert.match(html, locale === "ja" ? /オンライン/ : /Online/);
    assert.match(html, locale === "ja" ? /担当なし/ : /Unassigned/);
    assert.match(html, /<dd[^>]*>0<\/dd>/, "zero active jobs remains a reported zero");
  }
});

test("UI-LAYOUT-174: only opted-in node tables reflow by content width before their readable column minima overflow", t => {
  const workerHTML = renderUI(createElement(WorkersView), "ja", "/admin/workers/");
  const groupHTML = renderUI(createElement(RegisteredNodeGroup, {
    title: "Nodes", description: "Layout", rows: [node], columns: [{ accessorKey: "service_name", header: "Name" }],
  }), "ja");
  for (const html of [workerHTML, groupHTML]) assert.match(html, /data-slot="data-table"[^>]*class="[^"]*node-data-table/);
  const query = css.nodes.find(value => value.type === "atrule" && value.name === "container" && value.params.includes("108rem"));
  assert.ok(query && query.type === "atrule");
  assert.equal(query.params, "node-table (max-width: 108rem)");
  const properties = new Map<string, string>();
  query.walkRules(rule => {
    assert.ok(rule.selector.split(",").every(selector => selector.trim().startsWith(".node-data-table")), "content reflow cannot apply to every table");
    rule.walkDecls(declaration => { properties.set(rule.selector + "/" + declaration.prop, declaration.value); });
  });
  assert.equal(properties.get('.node-data-table[data-responsive="true"] table/min-width'), "0");
  assert.equal(properties.get('.node-data-table[data-responsive="true"] td/max-width'), "none");
  assert.equal(properties.get('.node-data-table[data-responsive="true"] tbody tr/display'), "grid");
  const firstRow = workerHTML.match(/<tbody[^>]*>[\s\S]*?<tr[^>]*>([\s\S]*?)<\/tr>/)?.[1];
  assert.ok(firstRow);
  const minima = [...firstRow.matchAll(/<td[^>]*>/g)].map(match => Math.max(0, ...declarations(css, match[0], "min-width").map(value => {
    const spacing = value.match(/^calc\(var\(--spacing\) \* (\d+)\)$/);
    return spacing ? Number(spacing[1]) / 4 : 0;
  })));
  assert.ok(minima.reduce((sum, value) => sum + value, 0) <= 108, "content breakpoint must cover the sum of actual operational column minima");
  assert.ok(css.nodes.some(value => value.type === "atrule" && value.name === "container" && value.params === "node-table (max-width: 38rem)"));
  assert.match(workerHTML, /data-slot="table-container"[^>]*overflow-x-auto/);
  t.diagnostic("Scoped emitted container rules and actual column minima; geometry at mobile/desktop/zoom remains a browser integration check.");
});

test("UI-LAYOUT-174: audit dates remain whole segments and preserve resource copy, full user agent and server rows", () => {
  const agent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 Chrome/154.0.0.0 Safari/537.36";
  const html = renderUI(createElement(AuditLogsView), "ja", "/admin/audit-logs/", client => {
    client.setQueriesData({ queryKey: ["audit-logs"] }, [{ id: "audit-layout", timestamp: "2026-10-06T02:07:00Z", actor_username: "update_agent", action: "system_updates.pull_ownership.activate", result: "success", resource_type: "update_agent", resource_id: "agent-copy-id", user_agent: agent, actor_ip: "2602:fd6f:100:30::a" }]);
  });
  assert.match(html, /<time dateTime="2026-10-06T02:07:00Z"[^>]*><span class="inline-block whitespace-nowrap">2026\/10\/06<\/span> <span class="inline-block whitespace-nowrap">11:07<\/span><\/time>/);
  assert.match(html, /update agent/);
  assert.equal((html.match(/aria-label="対象IDをコピー"/g) || []).length, 1);
  assert.ok(html.includes(agent));
  assert.ok(html.includes("2602:fd6f:100:30::a"));
  assert.doesNotMatch(html, /data-slot="table-pagination"/);
});

test("UI-LAYOUT-174: OAuth refresh labels, dates, state and missing values stay complete", () => {
  const resource = { path: "/integrations/oauth-accounts", title: "OAuth", description: "OAuth" };
  const value = { attempted_at: "2026-10-06T02:07:00Z", failed_at: "2026-10-06T02:07:00Z", failure_code: "reauthorization_required", relink_required: true };
  const html = renderUI(createElement(Fragment, null, formatResourceCell(resource, value, "oauth_refresh_status", "Asia/Tokyo")), "ja");
  assert.equal((html.match(/<time\b/g) || []).length, 2);
  assert.equal((html.match(/>2026\/10\/06<\/span> <span class="inline-block whitespace-nowrap">11:07<\/span>/g) || []).length, 2);
  assert.match(html, /class="whitespace-nowrap text-muted-foreground">最終試行:/);
  assert.match(html, /認可のやり直しが必要です/);
  assert.match(html, /text-destructive">必要/);
  const missing = renderUI(createElement(Fragment, null, formatResourceCell(resource, "", "refresh_token_updated_at")), "ja");
  assert.match(missing, /未記録（既存連携では不明）/);
  const invalid = renderUI(createElement(Fragment, null, formatResourceCell(resource, "invalid-timestamp", "access_token_refreshed_at")), "ja");
  assert.equal(invalid, "invalid-timestamp", "invalid timestamps keep the existing plain-text fallback");
  assert.equal(formatResourceCell({ ...resource, path: "/other" }, "2026-10-06T02:07:00Z", "updated_at", "Asia/Tokyo"), "2026/10/06 11:07", "other resource formatting remains unchanged");
});
