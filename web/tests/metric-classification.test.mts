import assert from "node:assert/strict";
import test from "node:test";

import { metricGroup } from "../src/features/metrics/metric-classification.ts";

test("CPU usage percentages are assigned to the CPU chart", () => {
  assert.equal(metricGroup("node.cpu.used_percent", "percent"), "cpu");
  assert.equal(metricGroup("worker.cpu_percent", "percent"), "cpu");
  assert.equal(metricGroup("host.cpu_percent", "percent"), "cpu");
});

test("CPU capacity and load metrics are not treated as CPU usage", () => {
  assert.equal(metricGroup("node.cpu_count", "count"), "runtime");
  assert.equal(metricGroup("node.load1", "number"), "runtime");
});
