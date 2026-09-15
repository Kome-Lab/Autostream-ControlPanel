import assert from "node:assert/strict";
import { statePaths, sectionPaths, loadingPaths } from "./state-drivers.mts";
import { canonicalFixturePath, contentInput, emptyInput, unknownInput } from "./fixture-inputs.mts";
import type { RouteResolver, StubResponse } from "../helpers/browser-harness.mts";
import { createBundle9Fixture } from "../helpers/bundle9-browser-fixtures.mts";
import type { Condition } from "./matrix.mts";

// Reuse immutable public synthetic API shapes, without changing the historical resolver.
export function createUIFixture(baseURL: string) {
  const inherited = createBundle9Fixture(baseURL);
  const origin = new URL(baseURL).origin;
  const trace: { method: string; path: string; status: number; body: unknown }[] = [];
  const unexpected: string[] = [];
  let condition: Condition;
  let primary = "";
  let phase = "ready";
  let unblock: () => void = () => {};
  let pending = Promise.resolve();
  const row = { id: "ui-record-one", name: "UI regression record", title: "UI regression record", status: "active", config: {}, created_at: "2026-09-01T00:00:00Z", updated_at: "2026-09-01T00:00:00Z" };
  const extras: Record<string, unknown> = {
    "/profiles/caption": [{ ...row, name: "UI caption profile" }],
    "/profiles/overlay": [{ ...row, name: "UI watermark" }],
    "/discord/configs": [{ ...row, name: "UI Discord configuration", token_configured: true }],
    "/discord/target-presets": [],
    "/youtube/outputs": [{ ...row, name: "UI YouTube output", stream_key_configured: true }],
    "/integrations/oauth-providers": [{ ...row, provider: "google", client_secret_configured: true }],
    "/integrations/oauth-accounts": [],
    "/users": [{ ...row, username: "ui-user", roles: ["viewer"] }],
    "/roles": [{ ...row, name: "viewer", permissions: ["streams.read"] }],
    "/permissions": [{ id: "streams.read", name: "streams.read" }],
    "/security/settings": { password_min_length: 12, password_hash: "argon2id", login_lockout_threshold: 5, session_idle_timeout_min: 30, session_absolute_lifetime_h: 12, remember_me_enabled: false, mfa_mode: "totp", mfa_required_roles: ["viewer"] },
    "/secrets/status": [{ name: "smtp", configured: true }],
    "/audit-logs": [{ id: "ui-audit", timestamp: "2026-09-01T00:00:00Z", action: "stream.update", result: "success", actor_username: "ui-operator", resource_type: "stream", resource_id: "ui-stream" }],
    "/stream-logs": [{ ...row, message: "Synthetic stream event", level: "info" }],
    "/observability/metrics": [{ name: "node.cpu.used_percent", service_id: "worker-one", service_type: "worker", value: 24, status: "healthy", updated_at: "2026-09-01T00:00:00Z" }],
    "/archive/streams": [{ id: "ui-archive", name: "UI recorded stream", status: "completed", archive_run_id: "ui-run" }],
    "/archive/processing-streams": [],
    "/streams/ui-archive/artifacts": [{ id: "ui-artifact", kind: "archive", name: "UI recording.mp4", size_bytes: 4096, status: "ready", created_at: "2026-09-01T00:00:00Z" }],
    "/streams/ui-archive/artifacts/ui-artifact/shares": [],
    "/archive-shares/ui-synthetic-share": { stream_name: "UI recorded stream", artifact_name: "UI recording.mp4", artifact_kind: "archive", size_bytes: 4096, created_at: "2026-09-01T00:00:00Z", allow_download: true, expires_at: "2099-01-01T00:00:00Z", playback_url: "data:video/mp4;base64,AAAAHGZ0eXBtcDQyAAAAAG1wNDJpc29t", download_url: "/archive-shares/ui-synthetic-share/download" },
  };
  const streams = Array.from({ length: 25 }, (_, index) => ({
    id: index === 0 ? "stream-control-platform" : "ui-stream-" + index,
    name: index === 0 ? "B9 Browser Stream" : "UI Stream " + String(index).padStart(2, "0"),
    status: index % 3 === 0 ? "ready" : index % 3 === 1 ? "live" : "failed",
    assigned_worker_id: "worker-one", assigned_encoder_id: "encoder-one",
  }));
  function reset(next: Condition, path: string) {
    unblock(); condition = next; primary = path;
    phase = ["background-refresh", "stale"].includes(next.state) ? "ready" : next.state;
    pending = new Promise<void>((resolve) => { unblock = resolve; });
    trace.length = 0; unexpected.length = 0; inherited.resetTrace();
    inherited.state.permissions = next.state === "permission-denied" ? [] : next.exercise === "denied-read" ? ["streams.read", "service_health.read"] : ["*"];
  }
  const resolver: RouteResolver = (request) => {
    assert.ok(condition, "fixture must be configured before its first request");
    const url = new URL(request.url);
    const path = canonicalFixturePath(url.pathname.replace(/\/+$/, "") || "/");
    let response: StubResponse | null;
    if (url.origin !== origin) {
      unexpected.push(request.method + " " + url.origin + path);
      return { status: 502, body: { code: "ui_external_request_forbidden" } };
    }
    if (request.method === "GET" && ["/archive/share", "/setup", "/auth/email/confirm"].includes(path)) return null;
    if (request.method === "POST" && /^\/streams\/[^/]+\/preview-links$/.test(path)) {
      response = { status: 403, body: { code: "forbidden" } };
    } else if (path === "/account/preferences/ui") {
      response = { body: { theme_id: condition?.theme || "autostream", color_mode: condition?.exercise === "system-mode" ? "system" : condition?.mode || "light", revision: 4 } };
    } else if (request.method === "GET" && path === "/streams") {
      response = { body: structuredClone(streams) };
    } else if (request.method === "GET" && url.pathname === "/video-cover-presets" && url.search === "") {
      response = { body: { items: [] } };
    } else if (request.method === "GET" && Object.hasOwn(extras, path)) {
      response = { body: structuredClone(extras[path]) };
    } else {
      response = inherited.resolver({ ...request, url: url.origin + path.replace("/streams/ui-stream-1/", "/streams/stream-control-platform/") + url.search });
      if (path.startsWith("/streams/ui-stream-1/") && response?.body && typeof response.body === "object") response = { ...response, body: { ...response.body, stream_id: "ui-stream-1" } };
      if (inherited.unexpected.length) unexpected.push(...inherited.unexpected.splice(0));
    }
    if (!response) return null;
    if (condition?.family === "login" && path === "/auth/me") response = { status: 401, body: { code: "unauthorized" } };
    if (path === "/auth/me" && condition.family !== "login" && response.body && typeof response.body === "object") {
      const me = response.body as { user: Record<string, unknown>; permissions: string[] };
      if (condition.state === "permission-denied" || condition.exercise === "denied-read") response = { ...response, body: { ...me, user: { ...me.user, roles: ["viewer"] }, permissions: [...inherited.state.permissions] } };
    }
    if (path === "/auth/mfa/status") response = { ...response, body: { enabled: true, available: true, policy_mode: "totp", pending_enrollment: false } };
    if (path === "/auth/oauth/providers") response = { ...response, body: [{ id: "ui-provider", name: "UI Identity", provider: "google", login_url: "/auth/oauth/ui-provider/login" }] };
    if (response.body && Array.isArray(response.body)) {
      const displayRows = response.body as Record<string, unknown>[];
      if (path === "/observability/diagnostics") response = { ...response, body: displayRows.map(row => ({ ...row, rule: "ui.diagnostic", report: { summary: row.name } })) };
      if (path === "/observability/remediation-actions") response = { ...response, body: displayRows.map(row => ({ ...row, action: "restart_worker", mode: "approval", result: { summary: row.name } })) };
      if (path === "/observability/notification-deliveries") response = { ...response, body: displayRows.map(row => ({ ...row, event_type: "admin.audit", metadata: { action: "streams.update", summary: row.name }, channel: "email" })) };
    }
    const paths = statePaths(condition, primary);
    if (phase === "initial-loading" && loadingPaths(condition, primary).includes(path) || phase === "background-refresh" && path === primary) response = { ...response, waitUntil: pending };
    if (["blocking-error", "partial", "stale"].includes(phase) && paths.failure.includes(path)) response = { status: 503, body: { code: "temporarily_unavailable", detail: "UI-HIDDEN-DIAGNOSTIC" } };
    if (phase === "permission-denied" && path === primary) response = { status: 403, body: { code: "forbidden" } };
    if (phase === "empty" && (sectionPaths[condition.family] || [primary]).includes(path)) response = { body: emptyInput(path, response.body) };
    if (phase === "unknown" && path === primary) response = { body: unknownInput(path, response.body) };
    if ((response.status ?? 200) === 200) response = { ...response, body: contentInput(response.body, condition.exercise, path) };
    trace.push({ method: request.method, path: path + url.search, status: response.status ?? 200, body: request.postData ? JSON.parse(request.postData) : null });
    return { ...response, requiredResponse: true };
  };
  return { resolver, trace, unexpected, reset, release() { unblock(); }, refresh() { phase = condition.state; }, get primary() { return primary; },
    workerRestartTarget() {
      const response = inherited.resolver({ method: "GET", url: origin + "/workers" });
      assert.ok(Array.isArray(response?.body));
      const rows = response.body as { id: string; service_id: string; service_type: string; service_name: string }[];
      const matches = rows.filter(row => row.service_id === "worker-one" && row.id === "worker-one" && row.service_type === "worker");
      assert.equal(matches.length, 1, "the inherited restart fixture requires the exact eligible Worker");
      assert.equal(rows.filter(row => row.service_name === matches[0].service_name).length, 1, "fixture display identity must be unique");
      return { id: matches[0].service_id, type: matches[0].service_type, name: matches[0].service_name };
    },
    detailStream() {
      const selected = streams[phase === "unknown" ? 0 : 1];
      return contentInput(phase === "unknown" ? unknownInput("/streams", selected) : selected, condition.exercise, "/streams") as typeof selected;
    },
  };
}
