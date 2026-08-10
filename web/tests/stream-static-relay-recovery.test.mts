import assert from "node:assert/strict";
import test from "node:test";
import {
  staticRelayRecoveryConfirmation,
  staticRelayRecoveryActionAvailable,
  staticRelayRecoveryErrorMessage,
} from "../src/lib/stream-static-relay.ts";

test("fixed-relay recovery is offered only for inactive streams using the fixed relay mode", () => {
  assert.equal(staticRelayRecoveryActionAvailable("live_api_relay_static", "failed"), true);
  assert.equal(staticRelayRecoveryActionAvailable("live_api_relay_static", "completed"), true);
  assert.equal(staticRelayRecoveryActionAvailable("live_api_relay_static", "live"), false);
  assert.equal(staticRelayRecoveryActionAvailable("live_api", "failed"), false);
  assert.equal(staticRelayRecoveryActionAvailable("stream_key", "completed"), false);
});

test("fixed-relay recovery always sends the explicit external-cleanup confirmation", () => {
  assert.deepEqual(staticRelayRecoveryConfirmation(), { confirm_external_cleanup: true });
});

test("fixed-relay recovery errors direct the operator to the safe next action", () => {
  assert.match(staticRelayRecoveryErrorMessage("youtube_relay_static_recovery_required") || "", /固定Relay回復/);
  assert.match(staticRelayRecoveryErrorMessage("stream_relay_recovery_not_safe_while_active") || "", /停止/);
  assert.equal(staticRelayRecoveryErrorMessage("unrelated_error"), "");
});
