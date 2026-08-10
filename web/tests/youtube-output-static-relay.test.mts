import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";

const source = readFileSync(new URL("../src/features/resources/resource-page.tsx", import.meta.url), "utf8");
const componentStart = source.indexOf("function YouTubeOutputForm");
const componentEnd = source.indexOf("function CaptionProfileForm");
const formSource = source.slice(componentStart, componentEnd);

test("fixed Relay Live API output asks for only nonsecret binding fields", () => {
  assert.ok(componentStart >= 0, "YouTube output form must exist");
  assert.ok(componentEnd > componentStart, "YouTube output form must have a bounded source section");
  assert.match(formSource, /value: "live_api_relay_static", label: "固定Relay（YouTube Live API）"/);
  assert.match(formSource, /const staticRelayMode = mode === "live_api_relay_static";/);
  assert.match(formSource, /const requiresOAuth = mode === "live_api" \|\| mode === "live_api_dry_run" \|\| staticRelayMode;/);
  assert.match(formSource, /<TextField label="固定RelayバインディングID"[\s\S]*?required \/>/);
  assert.match(formSource, /<TextField label="再利用するYouTube Live Stream ID"[\s\S]*?required \/>/);
  assert.match(formSource, /watch_url: staticRelayMode \? "" : watchURL/);
  assert.match(formSource, /1つの固定Relayは同時に1配信枠だけを処理できます。/);
});

test("fixed Relay Live API output omits direct RTMP credentials and forces completion", () => {
  assert.match(formSource, /rtmp_url: staticRelayMode \? "" : rtmpURL,/);
  assert.match(formSource, /stream_key: staticRelayMode \? "" : streamKey,/);
  assert.match(formSource, /relay_binding_id: staticRelayMode \? relayBindingID\.trim\(\) : "",/);
  assert.match(formSource, /reusable_live_stream_id: staticRelayMode \? reusableLiveStreamID\.trim\(\) : "",/);
  assert.match(formSource, /complete_on_stop: staticRelayMode \? true : completeOnStop,/);
  assert.match(formSource, /!staticRelayMode \? <TextField label="RTMP URL"/);
  assert.match(formSource, /停止時に完了扱い: 常に有効/);
  assert.match(formSource, /staticRelayMode && !staticRelayReady/);
});
