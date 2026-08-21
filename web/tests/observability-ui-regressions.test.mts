import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";

const queriesSource = readFileSync(new URL("../src/features/queries.ts", import.meta.url), "utf8");
const metricsSource = readFileSync(new URL("../src/features/metrics/metrics-view.tsx", import.meta.url), "utf8");
const auditSource = readFileSync(new URL("../src/features/audit/audit-logs-view.tsx", import.meta.url), "utf8");
const resourcesSource = readFileSync(new URL("../src/features/resources/resource-config.ts", import.meta.url), "utf8");
const resourcePageSource = readFileSync(new URL("../src/features/resources/resource-page.tsx", import.meta.url), "utf8");

test("metric range is requested from the server and participates in the query key", () => {
  assert.match(queriesSource, /range_sec/);
  assert.match(queriesSource, /\["observability",\s*"metrics",\s*rangeSeconds\]/);
  assert.doesNotMatch(metricsSource, /useMetricHistory|historyRef|useRef/);
});

test("long node labels cannot cover the fixed range selector", () => {
  assert.match(metricsSource, /min-w-0/);
  assert.match(metricsSource, /truncate/);
  assert.match(metricsSource, /shrink-0/);
});

test("node reports have their own audit tab and stream logs use historical endpoint", () => {
  assert.match(auditSource, /node_activity/);
  assert.match(auditSource, /Node報告・通信/);
  assert.match(resourcesSource, /path:\s*"\/stream-logs"/);
});

test("incident and stream-log history can load pages older than the initial API limit", () => {
  assert.match(resourcePageSource, /resourceHistoryConfig/);
  assert.match(resourcePageSource, /before_id/);
  assert.match(resourcePageSource, /さらに過去の履歴を読み込む/);
  assert.match(resourcePageSource, /path === "\/stream-logs"/);
  assert.match(resourcePageSource, /path === "\/observability\/incidents"/);
});
