import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { register } from "node:module";
import test from "node:test";

const resolverSource = [
  "let webRootURL;",
  "export function initialize(data) { webRootURL = data.webRootURL; }",
  "export async function resolve(specifier, context, nextResolve) {",
  "  if (specifier.startsWith('@/')) {",
  "    const target = new URL('src/' + specifier.slice(2), webRootURL);",
  "    if (!/\\.[cm]?[jt]sx?$/.test(target.pathname)) target.pathname += '.ts';",
  "    return nextResolve(target.href, context);",
  "  }",
  "  return nextResolve(specifier, context);",
  "}",
].join("\n");
register(`data:text/javascript,${encodeURIComponent(resolverSource)}`, {
  parentURL: import.meta.url,
  data: { webRootURL: new URL("../", import.meta.url).href },
});

const stateModule = await import("../src/features/monitoring/operational-remote-state.ts");
const noticeSource = readFileSync(new URL("../src/features/monitoring/operational-state-notice.tsx", import.meta.url), "utf8");

const ready = (data: readonly unknown[], at = 10) => ({ status: "success" as const, isFetching: false, data, dataUpdatedAt: at });
const loading = () => ({ status: "pending" as const, isFetching: true, dataUpdatedAt: 0 });
const failed = () => ({ status: "error" as const, isFetching: false, error: new TypeError("raw transport"), dataUpdatedAt: 0 });
const stale = (data: readonly unknown[], at = 9) => ({ status: "error" as const, isFetching: false, error: new TypeError("raw refresh"), data, dataUpdatedAt: at });

test("B10B finite consumer manifest is exact and does not inflate the action denominator", () => {
  assert.deepEqual(stateModule.operationalConsumerManifest, {
    dashboard: ["streams", "services"],
    monitoring: ["services", "streams", "incidents", "diagnostics"],
    metrics: ["metrics", "services"],
  });
  assert.equal(JSON.stringify(stateModule.operationalConsumerManifest).includes("action"), false);
});

test("each Monitoring query can be explicitly missing without coercing it to empty", () => {
  for (const omitted of stateModule.operationalConsumerManifest.monitoring) {
    const queries = Object.fromEntries(stateModule.operationalConsumerManifest.monitoring
      .filter((id: string) => id !== omitted)
      .map((id: string) => [id, ready([{ id }])]));
    const state = stateModule.aggregateOperationalQueries("monitoring", queries);
    assert.equal(state.kind, "partial", omitted);
    if (state.kind === "partial") assert.deepEqual(state.missingSections, [omitted]);
  }
});

test("each failed section is partial when another section remains usable", () => {
  for (const broken of stateModule.operationalConsumerManifest.monitoring) {
    const queries = Object.fromEntries(stateModule.operationalConsumerManifest.monitoring.map((id: string) => [id, id === broken ? failed() : ready([{ id }]) ]));
    const state = stateModule.aggregateOperationalQueries("monitoring", queries);
    assert.equal(state.kind, "partial", broken);
    if (state.kind === "partial") {
      assert.deepEqual(state.missingSections, [broken]);
      assert.equal(state.sectionErrors[broken]?.kind, "network");
    }
  }
});

test("cached refresh failure remains visible as stale, including partial plus stale", () => {
  const cached = stateModule.aggregateOperationalQueries("dashboard", { streams: stale([{ id: "stream" }]), services: ready([]) });
  assert.equal(cached.kind, "ready");
  if (cached.kind === "ready") assert.equal(cached.freshness.kind, "stale");
  const partial = stateModule.aggregateOperationalQueries("monitoring", {
    services: stale([{ id: "worker" }]), streams: ready([{ id: "stream" }]), incidents: loading(),
  });
  assert.equal(partial.kind, "partial");
  if (partial.kind === "partial") {
    assert.equal(partial.freshness.kind, "stale");
    assert.deepEqual(partial.missingSections, ["diagnostics", "incidents"]);
  }
});

test("initial loading and no-data failure remain distinct blocking states", () => {
  assert.equal(stateModule.aggregateOperationalQueries("metrics", { metrics: loading(), services: loading() }).kind, "initial-loading");
  assert.equal(stateModule.aggregateOperationalQueries("metrics", { metrics: failed(), services: loading() }).kind, "blocking-error");
  assert.equal(stateModule.aggregateOperationalQueries("metrics", { metrics: ready([]), services: ready([]) }).kind, "empty");
});

test("unknown stream status is not counted as live, waiting, attention, or done", () => {
  const counts = stateModule.countOperationalStreams([
    { id: "a", name: "a", status: "live" },
    { id: "b", name: "b", status: "future_state" },
  ]);
  assert.deepEqual(counts, { live: 1, waiting: 0, attention: 0, done: 0, unknown: 1 });
});

test("assignment is not health and coverage invariants exclude unknown from positive", () => {
  const summary = stateModule.summarizeServiceAvailability([
    { id: "healthy", service_type: "worker", service_name: "healthy", status: "online" },
    { id: "assigned", service_type: "worker", service_name: "assigned", status: "assigned" },
    { id: "offline", service_type: "worker", service_name: "offline", status: "offline" },
  ]);
  assert.deepEqual(summary, { totalCount: 3, knownCount: 2, positiveCount: 1, negativeCount: 1, unknownCount: 1 });
  assert.equal(summary.positiveCount + summary.negativeCount, summary.knownCount);
  assert.equal(summary.knownCount + summary.unknownCount, summary.totalCount);
});

test("unknown recording lifecycle remains unknown", () => {
  assert.deepEqual(stateModule.recordingStateContribution({ id: "a", name: "a", status: "future", archive_profile_id: "profile" }), { kind: "unknown" });
  assert.deepEqual(stateModule.recordingStateContribution({ id: "b", name: "b", status: "completed", archive_profile_id: "profile" }), { kind: "known", positive: true });
  assert.deepEqual(stateModule.recordingStateContribution({ id: "c", name: "c", status: "future" }), { kind: "known", positive: false });
});

test("positive summaries require complete fresh authority and zero unknown rows", () => {
  const fresh = stateModule.aggregateOperationalQueries("dashboard", { streams: ready([]), services: ready([]) });
  const partial = stateModule.aggregateOperationalQueries("dashboard", { streams: ready([]) });
  const old = stateModule.aggregateOperationalQueries("dashboard", { streams: stale([]), services: ready([]) });
  assert.equal(stateModule.remoteStateAllowsPositiveSummary(fresh, 0, true), true);
  assert.equal(stateModule.remoteStateAllowsPositiveSummary(fresh, 1, true), false);
  assert.equal(stateModule.remoteStateAllowsPositiveSummary(fresh, 0, false), false);
  assert.equal(stateModule.remoteStateAllowsPositiveSummary(partial, 0, true), false);
  assert.equal(stateModule.remoteStateAllowsPositiveSummary(old, 0, true), false);
});

test("partial and stale reasons are explicit in Japanese and English", () => {
  const state = stateModule.aggregateOperationalQueries("dashboard", { streams: stale([{ id: "stream" }]) });
  assert.equal(state.kind, "partial");
  const ja = stateModule.operationalStatePresentation(state, "dashboard", "ja");
  const en = stateModule.operationalStatePresentation(state, "dashboard", "en");
  assert.match(ja.text, /一部未取得/);
  assert.match(en.text, /Unavailable sections/);
  assert.deepEqual(ja.missing, ["Node状態"]);
});

test("state presenter is read-only, screen-reader associated, and forced-colors visible", () => {
  assert.match(noticeSource, /role=\{presentation\.tone === "error" \? "alert" : "status"\}/);
  assert.match(noticeSource, /aria-live=/);
  assert.match(noticeSource, /forced-colors:border-\[CanvasText\]/);
  assert.doesNotMatch(noticeSource, /allowed|availability|onClick|mutat/i);
});
