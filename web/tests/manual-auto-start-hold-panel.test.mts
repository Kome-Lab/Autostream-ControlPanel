import "./ui-regression/component-loader.mts";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { createElement } from "react";
import test from "node:test";
import { renderUI } from "./ui-regression/render-ui.mts";
const { StreamAutoStartHoldPanel } = await import("../src/features/streams/stream-auto-start-hold-panel.tsx");
const stream = { id: "msr-stream", name: "Synthetic stream", status: "ready", updated_at: "2026-11-16T01:00:00.000001Z", settings_revision: "settings-17", archive_run_id: "run-4", auto_start_trigger: "discord_voice_join" };
const hold = { stream_id: stream.id, known: true, held: true, revision: 7, reason: "manual_stop", actor_user_id: "operator", original_attempt_id: "stop-1", pending_attempt_id: "", created_at: "2026-11-16T00:00:00Z", updated_at: "2026-11-16T01:00:00Z" };

test("MSR-UI-PANEL-01: actual normal Stream panel shows held/clear/UNKNOWN and current permission in ja/en", () => {
  for (const locale of ["ja", "en"] as const) for (const permissions of [["streams.start"], []]) for (const state of ["held", "clear", "unknown"] as const) {
    const current = { ...hold, held: state === "held", known: state !== "unknown" };
    const html = renderUI(createElement(StreamAutoStartHoldPanel, { stream }), locale, "/streams", client => {
      client.setQueryData(["auth", "me"], { user: { id: "operator", username: "Synthetic" }, permissions });
      client.setQueryData(["streams", stream.id, "auto-start-hold", stream.updated_at, stream.settings_revision, stream.archive_run_id, stream.status], { stream, hold: current });
    });
    assert.match(html, /data-manual-auto-start-hold/); assert.ok(html.includes(`data-hold-state="${state}"`));
    assert.match(html, /Hold Revision/); assert.match(html, /run-4/);
    assert.match(html, locale === "ja" ? /手動で配信を開始しても、この保留は解除されません/ : /Starting manually does not clear this hold/);
    const resume = (html.match(/<button[^>]*>[^<]*(?:自動開始を再開|Resume automatic start)<\/button>/g) || [])[0];
    assert.ok(resume);
    assert.equal(resume.includes('disabled=""'), !permissions.length || state === "clear");
  }
});

test("MSR-UI-PANEL-02: untrusted reason/body is not echoed and normal detail owns the feature", () => {
  const html = renderUI(createElement(StreamAutoStartHoldPanel, { stream }), "ja", "/streams", client => {
    client.setQueryData(["auth", "me"], { user: { id: "operator", username: "Synthetic" }, permissions: ["streams.start"] });
    client.setQueryData(["streams", stream.id, "auto-start-hold", stream.updated_at, stream.settings_revision, stream.archive_run_id, stream.status], { stream, hold: { ...hold, reason: "PRIVATE_PROVIDER_SECRET", actor_user_id: "RAW_ACTOR_NOT_FOR_UI" } });
  });
  assert.doesNotMatch(html, /PRIVATE_PROVIDER_SECRET|RAW_ACTOR_NOT_FOR_UI/);
  const normal = readFileSync(new URL("../src/features/streams/stream-control-platform-panel.tsx", import.meta.url), "utf8");
  const detail = readFileSync(new URL("../src/features/streams/stream-details-dialog.tsx", import.meta.url), "utf8");
  assert.match(normal, /<StreamAutoStartHoldPanel stream=\{stream\}/);
  assert.match(detail, /<StreamControlPlatformPanel stream=\{stream\}/);
  const component = readFileSync(new URL("../src/features/streams/stream-auto-start-hold-panel.tsx", import.meta.url), "utf8");
  assert.match(component, /開始条件が成立している場合、配信が開始される/);
  assert.doesNotMatch(component, /console\.(?:log|warn|error)|JSON\.stringify\(.*(?:result|attempt)/);
});

test("MSR-UI-PANEL-03: a newer normal Stream frame cannot present an old cached clear hold as current", () => {
  const changed = { ...stream, status: "completed", updated_at: "2026-11-16T01:01:00Z" };
  const html = renderUI(createElement(StreamAutoStartHoldPanel, { stream: changed }), "ja", "/streams", client => {
    client.setQueryData(["auth", "me"], { user: { id: "operator", username: "Synthetic" }, permissions: ["streams.start"] });
    client.setQueryData(["streams", stream.id, "auto-start-hold", stream.updated_at, stream.settings_revision, stream.archive_run_id, stream.status], { stream, hold: { ...hold, held: false } });
  });
  assert.match(html, /data-hold-state="unknown"/);
  assert.doesNotMatch(html, /data-hold-state="clear"|run-4|未開始/);
});

test("MSR-UI-PANEL-04: a server-owned pending Hold exposes a read-only inspector without Resume permission", () => {
  const html = renderUI(createElement(StreamAutoStartHoldPanel, { stream }), "ja", "/streams", client => {
    client.setQueryData(["auth", "me"], { user: { id: "reader", username: "Synthetic reader" }, permissions: ["streams.read"] });
    client.setQueryData(["streams", stream.id, "auto-start-hold", stream.updated_at, stream.settings_revision, stream.archive_run_id, stream.status], { stream, hold: { ...hold, pending_attempt_id: "server-force-stop-original" } });
  });
  const inspect = (html.match(/<button[^>]*data-manual-hold-attempt-inspect[^>]*>[^<]*<\/button>/g) || [])[0];
  assert.ok(inspect); assert.equal(inspect.includes('disabled=""'), false);
  assert.ok(html.includes("保留元の要求を照会"));
  const resume = (html.match(/<button[^>]*>[^<]*自動開始を再開<\/button>/g) || [])[0];
  assert.ok(resume); assert.equal(resume.includes('disabled=""'), true);
});
