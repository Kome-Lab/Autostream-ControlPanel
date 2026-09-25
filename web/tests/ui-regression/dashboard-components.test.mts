import "./component-loader.mts";
import assert from "node:assert/strict";
import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import test from "node:test";
import { renderUI } from "./render-ui.mts";
import { renderedSource } from "./source-render.mts";
import { readFileSync } from "node:fs";

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

test("UI-MONITORING-AVAILABILITY-034: actual view binds each metric to its own available data and freshness", async () => {
  const { MonitoringView } = await import("../../src/features/monitoring/monitoring-view.tsx");
  const { APIError } = await import("../../src/lib/api/client.ts");
  const sources = [["service-health"], ["resource", "/observability/incidents"], ["resource", "/observability/diagnostics"]];
  type Mode = "empty" | "zero" | "nonempty" | "loading" | "error" | "denied" | "denied-cached" | "stale" | "stale-empty" | "refresh";
  const data = [[{ service_id: "service", service_name: "Service", health_status: "offline" }], [{ id: "incident", title: "Incident", status: "open" }], [{ id: "diagnostic", check: "Probe", status: "fail" }]];
  function render(locale: "ja" | "en", modes: Mode[], View = MonitoringView) {
    return renderUI(createElement(View), locale, "/admin/monitoring/", client => {
      modes.forEach((mode, index) => {
        client.setQueryData(sources[index], mode === "empty" || mode === "stale-empty" ? [] : mode === "zero" ? data[index].map(row => ({ ...row, status: index === 1 ? "resolved" : "pass", health_status: "healthy" })) : data[index]);
        const query = client.getQueryCache().find({ queryKey: sources[index], exact: true })!;
        const unavailable = ["loading", "error", "denied"].includes(mode);
        query.setState({ data: unavailable ? undefined : query.state.data, status: mode === "loading" ? "pending" : ["error", "denied", "denied-cached", "stale", "stale-empty"].includes(mode) ? "error" : "success", fetchStatus: mode === "refresh" || mode === "loading" ? "fetching" : "idle", dataUpdatedAt: unavailable ? 0 : 1_788_221_000_000, error: ["error", "stale", "stale-empty"].includes(mode) ? new APIError("synthetic", 503, "unavailable") : mode.startsWith("denied") ? new APIError("synthetic", 403, "forbidden") : null });
      });
    });
  }
  function metric(html: string, title: string) {
    const start = html.indexOf('data-slot="card-title"', html.indexOf('<section class="grid gap-4'));
    const cards = html.slice(start).split('data-slot="card"').slice(0, 4);
    const card = cards.find(part => part.includes('>'+title+'</'));
    assert.ok(card, title+": actual metric card"); return card;
  }
  for (const locale of ["ja", "en"] as const) {
    const labels = locale === "ja" ? ["オンラインNode", "Node要確認", "未解決インシデント", "診断警告"] : ["Online nodes", "Nodes needing attention", "Unresolved incidents", "Diagnostic warnings"];
    for (const mode of ["empty", "zero", "nonempty", "loading", "error", "denied", "denied-cached", "stale", "stale-empty", "refresh"] as const) {
      for (let source = 0; source < 3; source++) {
        const modes: Mode[] = ["nonempty", "nonempty", "nonempty"]; modes[source] = mode;
        const html = render(locale, modes);
        for (let index = 0; index < 4; index++) {
          const affected = (index < 2 ? 0 : index - 1) === source;
          const card = metric(html, labels[index]);
          if (affected && ["loading", "error", "denied", "denied-cached"].includes(mode)) {
            assert.match(card, />—</, mode+" must not fabricate zero");
            assert.doesNotMatch(card, /text-emerald/);
            assert.match(card, locale === "ja" ? /取得中|取得でき|権限/ : /Loading|unavailable|Permission|permission/);
          } else {
            assert.doesNotMatch(card, />—</, "valid other source or measured data remains available");
            if (affected && ["empty", "zero", "stale-empty"].includes(mode)) assert.match(card, index === 0 ? />[01]\/[01]</ : />0</, "known zero is measured");
            if (affected && (mode.startsWith("stale") || mode === "refresh")) {
              assert.doesNotMatch(card, /text-emerald/);
              assert.match(card, locale === "ja" ? /前回|更新中/ : /previous|Refreshing/);
            }
          }
        }
      }
    }
    const html = render(locale, ["error", "error", "error"]);
    assert.doesNotMatch(html, /優先対応が必要な項目はありません|No items require priority action/);
    assert.equal((html.match(/>—</g) || []).length, 4);
    const loading = renderUI(createElement(MonitoringView), locale, "/admin/monitoring/", client => {
      for (const key of [...sources, ["streams"]]) {
        const query = client.getQueryCache().find({ queryKey: key, exact: true })!;
        query.setState({ data: undefined, status: "pending", fetchStatus: "fetching", dataUpdatedAt: 0, error: null });
      }
    });
    assert.match(loading, /role="status"/);
    assert.doesNotMatch(loading, /text-emerald|>0\/0<|>0</, "first loading is not a measured summary");
  }
  const url = new URL("../../src/features/monitoring/monitoring-view.tsx", import.meta.url), source = readFileSync(url, "utf8");
  const mutant = await renderedSource(source.replaceAll('state={serviceState}', 'state={{ kind: "empty", freshness: { kind: "fresh", lastSuccessAt: 0 } }}'), url);
  assert.notEqual(mutant.MonitoringView, MonitoringView);
  assert.throws(() => assert.match(metric(render("en", ["error", "nonempty", "nonempty"], mutant.MonitoringView), "Online nodes"), />—</), "failed data cannot default to healthy empty");
  const metricURL = new URL("../../src/features/monitoring/monitoring-metric.tsx", import.meta.url);
  const { MonitoringMetric } = await import("../../src/features/monitoring/monitoring-metric.tsx");
  const empty = { title: "Measured", value: 0, detail: "Measured from the successful source", state: { kind: "empty", freshness: { kind: "fresh", lastSuccessAt: 0 } } } as const;
  assert.match(renderUI(createElement(MonitoringMetric, empty), "en"), />0</);
  const unavailableEmpty = await renderedSource(readFileSync(metricURL, "utf8").replace('(state.kind === "ready" || state.kind === "empty")', '(state.kind === "ready")'), metricURL);
  assert.throws(() => assert.match(renderUI(createElement(unavailableEmpty.MonitoringMetric, empty), "en"), />0</), "normal empty cannot become unknown");
});
