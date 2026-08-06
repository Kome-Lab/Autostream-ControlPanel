import assert from "node:assert/strict";
import test from "node:test";

import { isServiceAvailable } from "../src/lib/service-health.ts";

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
