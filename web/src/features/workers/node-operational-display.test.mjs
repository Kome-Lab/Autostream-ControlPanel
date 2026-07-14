import assert from "node:assert/strict";
import test from "node:test";
import { formatNodeMetricPercent, formatWorkerHeartbeat } from "./node-operational-display.ts";

test("formatWorkerHeartbeat prefers an API-provided age", () => {
  assert.equal(
    formatWorkerHeartbeat({ heartbeat_age_sec: 7, last_heartbeat_at: "2026-07-14T00:00:00Z" }, Date.parse("2026-07-14T00:01:00Z")),
    "7 sec",
  );
});

test("formatWorkerHeartbeat derives elapsed seconds from last_heartbeat_at", () => {
  assert.equal(formatWorkerHeartbeat({ last_heartbeat_at: "2026-07-14T00:00:00Z" }, Date.parse("2026-07-14T00:00:12.900Z")), "12 sec");
  assert.equal(formatWorkerHeartbeat({ last_heartbeat_at: "2026-07-14T00:00:01Z" }, Date.parse("2026-07-14T00:00:00Z")), "0 sec");
});

test("formatWorkerHeartbeat labels missing or invalid timestamps as unavailable", () => {
  assert.equal(formatWorkerHeartbeat({}, Date.parse("2026-07-14T00:00:00Z")), "未取得");
  assert.equal(formatWorkerHeartbeat({ last_heartbeat_at: "invalid" }, Date.parse("2026-07-14T00:00:00Z")), "未取得");
});

test("formatNodeMetricPercent reads current node metric keys", () => {
  const metrics = {
    "node.cpu.used_percent": "27.26",
    "node.memory.used_percent": 42.04,
    cpu_percent: 99,
  };

  assert.equal(formatNodeMetricPercent(metrics, "cpu"), "27.3%");
  assert.equal(formatNodeMetricPercent(metrics, "memory"), "42%");
});

test("formatNodeMetricPercent supports contract and compatibility keys", () => {
  assert.equal(formatNodeMetricPercent({ "host.cpu_percent": 0 }, "cpu"), "0%");
  assert.equal(formatNodeMetricPercent({ memoryUsage: "51.55" }, "memory"), "51.6%");
});

test("formatNodeMetricPercent labels absent or non-numeric values as unreported", () => {
  assert.equal(formatNodeMetricPercent(undefined, "cpu"), "未報告");
  assert.equal(formatNodeMetricPercent({ "node.memory.used_percent": "" }, "memory"), "未報告");
});
