import "./component-loader.mts";
import assert from "node:assert/strict";
import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import test from "node:test";

const { I18nProvider } = await import("../../src/components/admin/i18n-provider.tsx");
const { DashboardIncidentBanner, DashboardOutputs } = await import("../../src/features/dashboard/dashboard-panels.tsx");
const { dashboardStreamGroups } = await import("../../src/features/dashboard/dashboard-streams.tsx");

test("UI-DASH-001: active and waiting groups never turn unknown streams into ready", () => {
  const groups = dashboardStreamGroups(["live", "starting", "stopping", "draft", "ready", "failed", "future_state"].map((status) => ({ id: status, name: status, status })));
  assert.deepEqual(groups.active.map((row) => row.status), ["live", "starting", "stopping"]);
  assert.deepEqual(groups.waiting.map((row) => row.status), ["draft", "ready"]);
  assert.deepEqual(groups.issues.map((row) => row.status), ["failed"]);
});

test("UI-DASH-002: unavailable, unknown and unresolved incidents retain a visible notice", () => {
  for (const props of [
    { rows: undefined, unavailable: true, refreshing: false },
    { rows: [{ id: "one", status: "future_state" }], unavailable: false, refreshing: false },
    { rows: [{ id: "one", status: "open", severity: "critical" }], unavailable: false, refreshing: false },
  ]) {
    const html = renderToStaticMarkup(createElement(I18nProvider, null, createElement(DashboardIncidentBanner, props)));
    assert.match(html, /data-slot="dashboard-incidents"/);
    assert.doesNotMatch(html, /<button|healthy/);
  }
});

test("UI-DASH-003: output projection does not assert upload success or expose an unauthorized archive link", () => {
  const html = renderToStaticMarkup(createElement(I18nProvider, null, createElement(DashboardOutputs, {
    streams: [{ id: "one", name: "one", status: "live", archive_run_id: "run" }], canReadArchive: false,
  })));
  assert.doesNotMatch(html, /href="\/admin\/archive/);
  assert.match(html, /成功を示す件数ではありません|do not indicate/);
});
