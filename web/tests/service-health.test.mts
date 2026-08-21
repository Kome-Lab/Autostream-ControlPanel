import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";

import { isLiveResourcePath, liveStatusRefreshIntervalMs } from "../src/lib/resource-query-refresh.ts";
import { isServiceAvailable } from "../src/lib/service-health.ts";

const queriesSource = readFileSync(new URL("../src/features/queries.ts", import.meta.url), "utf8");

test("an assigned service with a healthy heartbeat is available", () => {
  assert.equal(isServiceAvailable({ status: "assigned", health_status: "healthy" }), true);
});

test("assignment is not available when the heartbeat is unhealthy", () => {
  assert.equal(isServiceAvailable({ status: "assigned", health_status: "offline" }), false);
});

test("online remains available without a separate health value", () => {
  assert.equal(isServiceAvailable({ status: "online" }), true);
});

test("legacy healthy and ok statuses remain available", () => {
  assert.equal(isServiceAvailable({ status: "healthy" }), true);
  assert.equal(isServiceAvailable({ status: "ok" }), true);
});

test("live resource status queries opt into periodic and focus refresh", () => {
  assert.match(queriesSource, /useResourceData[\s\S]*isLiveResourcePath\(path\)/);
  assert.match(queriesSource, /refetchIntervalInBackground/);
  assert.match(queriesSource, /refetchOnWindowFocus/);
  assert.equal(liveStatusRefreshIntervalMs, 10_000);
  for (const path of [
    "/streams",
    "/stream-logs",
    "/service-health",
    "/integrations/oauth-accounts",
    "/observability/incidents?status=open",
    "/observability/diagnostics/",
    "/observability/remediation-actions",
    "/observability/notification-deliveries",
    "/observability/metrics",
  ]) {
    assert.equal(isLiveResourcePath(path), true, `${path} should refresh automatically`);
  }
  for (const path of ["/profiles/encoder", "/youtube/outputs", "/observability/notification-channels"]) {
    assert.equal(isLiveResourcePath(path), false, `${path} should not be polled as live status`);
  }
});
