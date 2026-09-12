import type { RouteResolver, StubResponse } from "./browser-harness.mts";
import { BUNDLE9_BROWSER_CLOCK, type APIObservation } from "./bundle9-browser-contract.mts";

// Public synthetic inputs derived from the current UI Foundation fixtures.
// No service, provider, user, or signing credentials are used by this harness.
export const bundle9Stream = Object.freeze({ id: "stream-control-platform", name: "B9 Browser Stream", status: "ready", assigned_worker_id: "worker-one", assigned_encoder_id: "encoder-one" });
export const bundle9ReadinessPath = `/streams/${bundle9Stream.id}/start-readiness`;
export const bundle9ReadinessSelector = `button[aria-label="${bundle9Stream.name} の開始準備を再確認"]`;
export const bundle9SyntheticMFASecret = "JBSWY3DPEHPK3PXP";

const healthyRows = [
  { id: "worker-one", service_id: "worker-one", service_type: "worker", service_name: "Worker One", status: "online", health_status: "healthy", heartbeat_age_sec: 2, reported_capabilities: { scene_appearance_v1: true } },
  { id: "encoder-one", service_id: "encoder-one", service_type: "encoder_recorder", service_name: "Encoder One", status: "offline", health_status: "unhealthy", heartbeat_age_sec: 180, reported_capabilities: { live_video_cover_v1: true } },
];
const updater = { updater_id: "host-agent-main", name: "B9 Host Agent", status: "online", online: true, version: "v2.0.0", transport_mode: "pull_v2", execution_host_id: "host-main", ownership_epoch: 0, desired_revision: 9, applied_revision: 9 };
const updaterSettings = {
  updater_id: updater.updater_id, revision: 9, projection_revision: 4,
  local_executor_policy_revision: 6, transport_mode: "pull_v2", execution_host_id: "host-main",
  execution_host_ownership: { transport_mode: "pull_v2", agent_service_id: "", ownership_epoch: 12, policy_revision: 8 },
  pull_activation: { ready: true, status: "online", last_heartbeat_at: BUNDLE9_BROWSER_CLOCK, observe_only: true, update_executor: true, mutation_enabled: false, recovery_pending: false, reported_ownership_epoch: 0, reported_projection_revision: 4 },
  local_executor_policy_sha256: `sha256:${"a".repeat(64)}`,
  api: { bind_host: "127.0.0.1", host: "127.0.0.1", port: 8090, ssl_enabled: false },
  poll_interval_seconds: 15, heartbeat_interval_seconds: 30, hosts: [], targets: [], github_token_configured: false,
};

function observableRow(id: string, name: string, status: string) {
  return { id, name, title: name, summary: name, message: name, subject: name, status, severity: "warning", service_id: "worker-one", service_type: "worker", created_at: BUNDLE9_BROWSER_CLOCK, updated_at: BUNDLE9_BROWSER_CLOCK };
}

export function createBundle9Fixture(baseURL: string) {
  const origin = new URL(baseURL).origin;
  const trace: APIObservation[] = [];
  const unexpected: string[] = [];
  const state = { permissions: ["*"], healthError: false, readinessError: false, mfaPending: false };
  const getBodies: Record<string, unknown> = {
    "/setup/status": { setup_enabled: true, setup_required: false },
    "/settings/app": { app_name: "AutoStream", timezone: "Asia/Tokyo", google_analytics_enabled: false },
    "/version": { service: "control-panel", version: "1.2.4", commit: "ui-foundation-test", build_date: "2026-08-25T00:00:00Z", update_available: false, latest_version: "v1.2.4", update_check_source: "github", service_updates: {} },
    "/account/preferences/ui": { theme_id: "autostream", color_mode: "light", revision: 4 },
    "/auth/passkeys": [], "/auth/oauth-links": [], "/auth/oauth/providers": [],
    "/streams": [bundle9Stream], "/workers": healthyRows, "/nodes": healthyRows,
    [`/streams/${bundle9Stream.id}/visual-settings`]: { stream_id: bundle9Stream.id, background_mode: "image", header_title_mode: "stream_name", discord_target_mode: "manual", revision: 2, cover_source: "upload", cover_start_active: false },
    [`/streams/${bundle9Stream.id}/video-cover-state`]: { stream_id: bundle9Stream.id, desired_active: false, desired_revision: 1, applied_active: false, applied_revision: 1, transition_state: "idle", output_pipeline: ["base_or_worker_scene", "video_cover", "watermark", "video_encode", "tee_live_archive_preview"] },
    "/service-health": healthyRows,
    "/profiles/encoder": [{ id: "encoder-profile", name: "B9 Encoder Profile", config: { width: 1920, height: 1080, fps: 60, video_bitrate_kbps: 8000 } }],
    "/profiles/archive": [{ id: "recording-profile", name: "B9 Recording Profile", config: { format: "mp4", retention_days: 180, upload_enabled: false } }],
    "/archive/streams": [], "/archive/processing-streams": [], "/archive/destinations": [],
    "/profiles/caption": [], "/profiles/overlay": [], "/youtube/outputs": [],
    "/discord/configs": [], "/discord/target-presets": [],
    "/integrations/oauth-accounts": [], "/integrations/oauth-providers": [],
    "/observability/incidents": [observableRow("incident-one", "B9 Incident", "open")],
    "/observability/diagnostics": [observableRow("diagnostic-one", "B9 Diagnostic", "pass")],
    "/observability/remediation-actions": [observableRow("remediation-one", "B9 Remediation", "pending")],
    "/observability/notification-deliveries": [observableRow("notification-one", "B9 Notification", "delivered")],
    "/observability/notification-channels": [],
    "/system-updates": { targets: [], updaters: [updater], hosts: [], jobs: [] },
    "/system-updates/updaters/host-agent-main/settings": updaterSettings,
    "/system-updates/updaters/host-agent-main/bootstrap-jobs": { updater_id: "host-agent-main", jobs: [] },
  };
  const resolver: RouteResolver = ({ method, url, postData }) => {
    const parsed = new URL(url);
    const pathname = parsed.pathname.replace(/\/+$/, "") || "/";
    let response: StubResponse | undefined;
    if (parsed.origin !== origin) {
      unexpected.push(`external ${method} ${parsed.origin}${pathname}`);
      return { status: 502, body: { code: "bundle9_external_request_forbidden" }, requiredResponse: true };
    }
    if (method === "GET" && pathname === "/auth/me") {
      response = { body: { user: { id: "ui-foundation-user", username: "ui-foundation-admin", email: "operator@example.test", status: "active", roles: ["super_admin"] }, permissions: [...state.permissions] } };
    } else if (method === "GET" && pathname === "/auth/mfa/status") {
      response = { body: { enabled: false, available: true, policy_mode: "totp", pending_enrollment: state.mfaPending } };
    } else if (method === "GET" && pathname === "/service-health" && state.healthError) {
      response = { status: 503, body: { code: "service_health_unavailable", detail: "B9-HIDDEN-DIAGNOSTIC" } };
    } else if (method === "GET" && Object.hasOwn(getBodies, pathname)) {
      response = { body: getBodies[pathname] };
    } else if (method === "POST" && pathname === "/auth/session/refresh") {
      response = { body: { status: "ok" } };
    } else if (method === "POST" && pathname === bundle9ReadinessPath) {
      response = state.readinessError
        ? { status: 403, body: { code: "forbidden", detail: "B9-HIDDEN-DIAGNOSTIC" } }
        : { body: { stream_id: bundle9Stream.id, ready: true, missing_service_types: [], issues: [], assigned_service_count: 2 } };
    } else if (method === "POST" && pathname === "/auth/mfa/enroll") {
      state.mfaPending = true;
      response = { body: { secret: bundle9SyntheticMFASecret, provisioning_uri: `otpauth://totp/AutoStream:fixture?secret=${bundle9SyntheticMFASecret}&issuer=AutoStream`, recovery_codes: ["B9-SYNTHETIC-RECOVERY"], enrollment_pending: true } };
    }
    if (response) {
      trace.push({ method, path: `${parsed.pathname}${parsed.search}`, body: postData === undefined || postData === "" ? null : JSON.parse(postData), status: response.status ?? 200 });
      return { ...response, requiredResponse: true };
    }
    // Pass only same-origin export documents/assets to the owned static server.
    // An unknown API or a non-GET asset request is a failing observation.
    if ((method === "GET" || method === "HEAD") && (pathname === "/admin" || pathname.startsWith("/admin/") || pathname.startsWith("/_next/") || pathname === "/login" || pathname === "/" || /\.(?:js|css|svg|png|ico|woff2?|txt)$/.test(pathname))) return null;
    unexpected.push(`${method} ${parsed.pathname}${parsed.search}`);
    return { status: 404, body: { code: "bundle9_unexpected_request" }, requiredResponse: true };
  };
  return { resolver, trace, unexpected, state, resetTrace() { trace.length = 0; unexpected.length = 0; } };
}
