import "./component-loader.mts";
import assert from "node:assert/strict";
import test from "node:test";
import { createElement } from "react";
import type { QueryClient } from "@tanstack/react-query";
import type { RemoteState } from "../../src/lib/foundation/remote-state/contracts.ts";
import { renderUI } from "./render-ui.mts";

const { OperationalStateNotice } = await import("../../src/features/monitoring/operational-state-notice.tsx");
const { MonitoringMetric } = await import("../../src/features/monitoring/monitoring-metric.tsx");
const { MonitoringView } = await import("../../src/features/monitoring/monitoring-view.tsx");
const { DashboardView } = await import("../../src/features/dashboard/dashboard-view.tsx");
const { DashboardIncidentBanner } = await import("../../src/features/dashboard/dashboard-panels.tsx");
const { WorkersView } = await import("../../src/features/workers/workers-view.tsx");
const { NodeRegistrationView } = await import("../../src/features/nodes/node-registration-view.tsx");
const { GenericResourcePanel } = await import("../../src/features/resources/generic-resource-panel.tsx");
const { APIError } = await import("../../src/lib/api/client.ts");

type QueryMode = "fresh" | "refreshing" | "stale" | "missing" | "loading";
function setMode(client: QueryClient, key: readonly string[], mode: QueryMode) {
  const query = client.getQueryCache().find({ queryKey: key, exact: true });
  assert.ok(query, `fixture query exists: ${key.join("/")}`);
  query.setState({
    status: mode === "loading" ? "pending" : mode === "stale" || mode === "missing" ? "error" : "success",
    fetchStatus: mode === "refreshing" || mode === "loading" ? "fetching" : "idle",
    data: mode === "missing" || mode === "loading" ? undefined : query.state.data,
    error: mode === "stale" || mode === "missing" ? new APIError("Synthetic read failure", 503, "unavailable") : null,
  });
}
const worker = { id: "refresh-worker", service_id: "refresh-worker", service_name: "Refresh Worker", service_type: "worker", host: "worker.invalid", port: 51377, status: "online", health_status: "healthy" };

test("monitoring metric details retain the successful snapshot copy during polling", () => {
  for (const locale of ["ja", "en"] as const) {
    for (const kind of ["ready", "empty"] as const) {
      const render = (refreshing: boolean) => renderUI(createElement(MonitoringMetric, {
        title: "Known nodes", value: 0, detail: "Known snapshot", tone: "ok",
        state: { kind, ...(kind === "ready" ? { data: [] } : {}), freshness: { kind: refreshing ? "refreshing" : "fresh", lastSuccessAt: 1 } } as RemoteState<unknown>,
      }), locale);
      const detail = (html: string) => html.match(/<p class="mt-1 text-sm text-muted-foreground">(.*?)<\/p>/)?.[1];
      assert.ok(detail(render(false))?.includes("Known snapshot"));
      assert.doesNotMatch(render(false), /<span class="sr-only" role="status"/);
      assert.equal(detail(render(true)), detail(render(false)), "regular polling must not insert detail text that resizes the card");
    }
  }
});

test("monitoring and dashboard keep loaded summaries stable during successful polling", () => {
  const visibleText = (html: string) => html.replace(/<span class="sr-only"[^>]*>[\s\S]*?<\/span>/g, "").replace(/<[^>]*>/g, "");
  const snapshotAt = Date.parse("2026-09-01T01:00:00Z");
  for (const locale of ["ja", "en"] as const) {
    for (const View of [MonitoringView, DashboardView]) {
      const render = (mode: QueryMode, updatedAt = snapshotAt) => renderUI(createElement(View), locale, "/admin/", client => {
        const sections: Array<[readonly string[], unknown[]]> = [
          [["service-health"], [worker]], [["streams"], []],
          [["resource", "/observability/incidents"], []], [["resource", "/observability/diagnostics"], []],
        ];
        // Compare the same acquired snapshot, independent of wall-clock minute boundaries.
        for (const [key, rows] of sections) { client.setQueryData(key, rows, { updatedAt }); setMode(client, key, mode); }
      });
      assert.equal(visibleText(render("refreshing")), visibleText(render("fresh")), `${View.name}/${locale}: successful snapshot text must not change during a poll`);
      assert.doesNotMatch(render("fresh"), /<span class="sr-only" role="status"/);
      assert.notEqual(visibleText(render("stale")), visibleText(render("fresh")), "failed polling must still disclose stale data");
      if (View === MonitoringView) {
        assert.match(visibleText(render("fresh")), /09\/01 10:00/, "the acquired snapshot timestamp remains visible");
        assert.match(visibleText(render("fresh", snapshotAt + 60_000)), /09\/01 10:01/, "a newly acquired snapshot advances the displayed timestamp");
      }
    }
  }
});

// SSR checks the actual flow elements and copy. Geometry and focus are checked
// separately by ui-refresh-layout-browser.test.mts using live polling in Chrome.
test("operational notice keeps snapshot copy stable while announcing actual refresh state", () => {
  for (const locale of ["ja", "en"] as const) {
    for (const kind of ["ready", "empty"] as const) {
      const render = (refreshing: boolean) => renderUI(createElement(OperationalStateNotice, {
        consumer: "dashboard", state: { kind, ...(kind === "ready" ? { data: [] } : {}), freshness: { kind: refreshing ? "refreshing" : "fresh", lastSuccessAt: 1 } } as RemoteState<unknown>,
      }), locale);
      const fresh = render(false), refreshing = render(true);
      assert.equal(fresh.match(/<span>(.*?)<\/span>/)?.[1], refreshing.match(/<span>(.*?)<\/span>/)?.[1], "visible snapshot wording does not rewrap on polling");
      assert.match(refreshing, /data-freshness="refreshing"/);
      assert.match(refreshing, /aria-busy="true"/);
      assert.match(refreshing, locale === "ja" ? /aria-label="更新中"/ : /aria-label="Refreshing"/);
      assert.match(refreshing, /size-4 shrink-0/);
      assert.doesNotMatch(refreshing, /border-emerald|Required data is available|必要なデータを取得済み/);
    }
    const error = { kind: "unavailable", messageKey: "apiErrorUnavailable" } as const;
    const states: RemoteState<unknown>[] = [
      { kind: "initial-loading" },
      { kind: "blocking-error", error },
      { kind: "ready", data: [], freshness: { kind: "stale", lastSuccessAt: 1, error } },
      { kind: "partial", data: [], missingSections: ["streams"], sectionErrors: { streams: error }, freshness: { kind: "refreshing", lastSuccessAt: 1 } },
    ];
    for (const state of states) {
      const html = renderUI(createElement(OperationalStateNotice, { consumer: "dashboard", state }), locale);
      assert.match(html, new RegExp(`data-remote-state="${state.kind}"`));
      assert.match(html, locale === "ja" ? /初期データを取得中|取得できません|古いデータ|一部未取得/ : /Loading initial|unavailable|stale data|Unavailable sections/);
      assert.doesNotMatch(html, /Showing the loaded data snapshot|取得済みのデータを表示しています/);
    }
  }
});

test("incident polling adds no banner or paragraph for known data and preserves unknown and error notices", () => {
  const render = (rows: Parameters<typeof DashboardIncidentBanner>[0]["rows"], refreshing: boolean, unavailable = false) => renderUI(createElement(DashboardIncidentBanner, { rows, refreshing, unavailable }), "en");
  for (const refreshing of [false, true]) assert.doesNotMatch(render([], refreshing), /<aside|<p/);
  assert.doesNotMatch(render([], false), /role="status"/);
  assert.match(render([], true), /class="sr-only" role="status" aria-live="polite">Refreshing incidents/);
  const open = [{ id: "incident", status: "open", title: "Still unresolved", severity: "critical" }];
  const fresh = render(open, false), refreshing = render(open, true);
  assert.equal((fresh.match(/<p[ >]/g) || []).length, (refreshing.match(/<p[ >]/g) || []).length);
  assert.match(refreshing, /Still unresolved/); assert.match(refreshing, /aria-busy="true"/);
  for (const html of [render(undefined, true), render(open, true, true), render([{ id: "unknown", status: "future_status" }], true)]) {
    assert.match(html, /data-slot="dashboard-incidents"/);
    assert.match(html, /Checking incident status|Latest status is unavailable|unknown state/);
  }
});

test("Workers normal data and permission polls keep metric copy and notice flow stable, including measured empty", () => {
  const render = (mode: QueryMode, authorityRefresh = false, empty = false) => renderUI(createElement(WorkersView), "en", "/admin/workers/", client => {
    client.setQueryData(["workers"], empty ? [] : [worker]);
    client.setQueryData(["nodes"], []); client.setQueryData(["service-health"], []);
    setMode(client, ["workers"], mode);
    if (authorityRefresh) setMode(client, ["auth", "me"], "refreshing");
  });
  for (const empty of [false, true]) {
    const fresh = render("fresh", false, empty);
    assert.doesNotMatch(fresh, /<span class="sr-only" role="status"/);
    const details = (html: string) => [...html.matchAll(/<p class="mt-1 text-sm text-muted-foreground">(.*?)<\/p>/g)].map(match => match[1]);
    assert.equal(details(fresh).length, 3, "three real MetricCard details");
    for (const html of [render("refreshing", false, empty), render("fresh", true, empty)]) {
      assert.deepEqual(details(html), details(fresh));
      assert.doesNotMatch(html, /data-remote-consumer="workers"|<p[^>]*class="mb-3"/);
      assert.match(html, /aria-busy="true"/); assert.match(html, /class="sr-only" role="status" aria-live="polite">(?:Refreshing|Rechecking)/);
      assert.doesNotMatch(html, /text-emerald-700/, "polling is not asserted to be freshly confirmed healthy");
      if (empty) assert.match(html, />0\/0</, "cached successful empty remains measured");
    }
  }
  assert.match(render("stale"), /data-remote-freshness="stale"[^>]*>Refresh failed/);
  assert.match(render("missing"), /data-remote-state="partial"[^>]*>Unavailable: workers/);
  assert.match(render("loading"), /data-remote-state="partial"/);
});

test("registered node normal polling uses the existing refresh button and keeps failed or initial notices", () => {
  const render = (mode: QueryMode, empty = false) => renderUI(createElement(NodeRegistrationView, { mode: "registered" }), "en", "/admin/registered-nodes/", client => {
    client.setQueryData(["nodes"], empty ? [] : [worker]); setMode(client, ["nodes"], mode);
  });
  for (const empty of [false, true]) {
    const fresh = render("fresh", empty), refreshing = render("refreshing", empty);
    assert.doesNotMatch(fresh, /data-remote-consumer="nodes"/);
    assert.doesNotMatch(refreshing, /data-remote-consumer="nodes"/);
    assert.match(refreshing, /aria-busy="true"/);
    assert.doesNotMatch(fresh, /<span class="sr-only" role="status"/);
    assert.match(refreshing, /class="sr-only" role="status" aria-live="polite">Refreshing/);
  }
  for (const mode of ["stale", "missing", "loading"] as const) {
    const html = render(mode);
    assert.match(html, /data-remote-consumer="nodes"/);
    assert.match(html, /stale data|node data is unavailable|Loading node data/);
  }
});

test("resource normal polling adds no paragraph while read failure, initial loading and permission notices remain", () => {
  const resource = { title: "Polling resource", description: "Cached records", path: "/polling-test", permissions: { read: "poll.read" } };
  const access = { read: true, create: false, update: false, delete: false, test: false };
  const render = (mode: QueryMode, read = true) => renderUI(createElement(GenericResourcePanel, { resource, access: { ...access, read }, currentUser: undefined }), "en", "/admin/", client => {
    client.setQueryData(["resource", resource.path], [{ id: "record", name: "Existing record" }]);
    setMode(client, ["resource", resource.path], mode);
  });
  const fresh = render("fresh"), refreshing = render("refreshing");
  assert.equal((fresh.match(/<p[ >]/g) || []).length, (refreshing.match(/<p[ >]/g) || []).length);
  assert.match(refreshing, /Existing record/); assert.match(refreshing, /aria-busy="true"/);
  assert.doesNotMatch(fresh, /<span class="sr-only" role="status"/);
  assert.match(refreshing, /class="sr-only" role="status" aria-live="polite">Refreshing/);
  assert.match(render("stale"), /role="alert"/);
  assert.match(render("loading"), /aria-label="Loading"/);
  assert.match(render("fresh", false), /permission|Permission/);
});
