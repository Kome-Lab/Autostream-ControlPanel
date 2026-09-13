import assert from "node:assert/strict";
import { fileURLToPath } from "node:url";
import { BrowserHarness, type StubResponse } from "./helpers/browser-harness.mts";
import { loginPathForLocation } from "../src/lib/auth/post-login-redirect.ts";


export const webRoot = fileURLToPath(new URL("..", import.meta.url));
export const requestedBaseUrl = process.env.AUTOSTREAM_BROWSER_BASE_URL || "http://127.0.0.1:3002";
export const localeStorageKey = "autostream.controlPanel.locale";
export const themeStorageKey = "autostream.ui_preference";
export const csrfStorageKey = "autostream.csrf_token";
export const expectedAuthMeExpiryReturnURL = "/admin/streams/?view=active#preview";
export const loginReturnParameterName = productionLoginReturnParameterName();

export const currentUser = {
  user: { id: "ui-foundation-user", username: "ui-foundation-admin", email: "operator@example.test", roles: ["super_admin"] },
  permissions: ["*"],
};
export const healthyRows = [
  { id: "worker-one", service_id: "worker-one", service_type: "worker", service_name: "Worker One", status: "online", health_status: "healthy", reported_capabilities: { scene_appearance_v1: true } },
  { id: "encoder-one", service_id: "encoder-one", service_type: "encoder_recorder", service_name: "Encoder One", status: "offline", health_status: "unhealthy", reported_capabilities: { live_video_cover_v1: true } },
];
export const workerPilotRows = [
  { service_id: "worker-one", service_type: "worker", service_name: "Worker One", status: "online", health_status: "healthy" },
  { service_id: "worker-future", service_type: "worker", service_name: "Future Worker", status: "future_online_v2", health_status: "future_healthy_v2" },
  { service_id: "worker-assigned", service_type: "worker", service_name: "Assigned Worker", status: "assigned" },
  { service_id: "worker-former-alias", service_type: "worker", service_name: "Former Alias Worker", status: "future_connectivity", health_status: "ok" },
  { service_id: "worker-degraded", service_type: "worker", service_name: "Degraded Worker", status: "degraded", health_status: "future_health" },
];
export const currentVersion = versionResponse({ latestVersion: "v1.2.4" });
export const availableVersion = versionResponse({ latestVersion: "v1.3.0", updateAvailable: true });
export const startReadinessStream = { id: "stream-permission-fixture", name: "権限検証配信", status: "ready" };
export const startReadinessPath = `/streams/${startReadinessStream.id}/start-readiness`;
export const controlPlatformStream = { id: "stream-control-platform", name: "ビジュアル確認配信", status: "ready", assigned_worker_id: "worker-one", assigned_encoder_id: "encoder-one" };
const controlPlatformVisualPath = `/streams/${controlPlatformStream.id}/visual-settings`;
export const controlPlatformCoverPath = `/streams/${controlPlatformStream.id}/video-cover-state`;
const controlPlatformPipeline = ["base_or_worker_scene", "video_cover", "watermark", "video_encode", "tee_live_archive_preview"];

export function permissionUser(permissions: string[]) {
  return {
    user: { id: "stream-permission-user", username: "stream-permission-operator", roles: [] },
    permissions,
  };
}

export function controlPlatformCoverState(
  desiredActive: boolean,
  desiredRevision: number,
  appliedActive: boolean | null,
  appliedRevision: number | null,
  status: "idle" | "confirming" | "applied" | "failed",
) {
  return {
    stream_id: controlPlatformStream.id,
    job_generation: 1,
    desired_active: desiredActive,
    desired_revision: desiredRevision,
    applied_active: appliedActive,
    applied_revision: appliedRevision,
    asset_variant_id: "variant-cover",
    last_error_code: status === "confirming" ? "transport_outcome_unknown" : "",
    status,
    pipeline_order: controlPlatformPipeline,
    cover_watermark_independent: true,
  };
}

export function workerConfiguration(id: string, yaml: string) {
  return {
    node: {
      service_id: id,
      service_type: "worker",
      service_name: id === "worker-one" ? "Worker One" : `Worker ${id}`,
      status: "online",
      health_status: "healthy",
    },
    node_api_url: `https://${id}.example.invalid`,
    configure_command: `configure ${id}`,
    configuration_yaml: yaml,
    systemd_unit: `[Unit]\nDescription=${id}`,
  };
}

export function versionResponse({
  latestVersion,
  updateAvailable = false,
  source = "github",
}: {
  latestVersion?: string;
  updateAvailable?: boolean;
  source?: string;
}) {
  return {
    service: "control-panel",
    version: "1.2.4",
    commit: "ui-foundation-test",
    build_date: "2026-08-25T00:00:00Z",
    update_available: updateAvailable,
    latest_version: latestVersion,
    update_check_source: source,
    service_updates: {},
  };
}

function normalizePath(pathname: string) {
  return pathname.length > 1 ? pathname.replace(/\/+$/, "") : pathname;
}

function productionLoginReturnParameterName() {
  const contractURL = new URL(loginPathForLocation({ pathname: "/admin/" }), "https://control-panel.test");
  const parameterNames = [...contractURL.searchParams.keys()];
  assert.equal(parameterNames.length, 1, "production login path must expose one return parameter");
  return parameterNames[0];
}

export function escapeRegExp(value: string) {
  return value.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
}
export type BrowserRouteFixture = {
  authResponse: StubResponse;
  setupResponse: StubResponse;
  healthResponse: StubResponse;
  versionFixture: StubResponse;
  refreshResponse: StubResponse;
  logoutResponse: StubResponse;
  loginResponse: StubResponse;
  streamsResponse: StubResponse;
  startReadinessResponse: StubResponse;
  startReadinessMethods: string[];
  workersResponse: StubResponse;
  nodesResponse: StubResponse;
  configurationResponse: StubResponse;
  restartResponse: StubResponse;
  workerRestartMethods: string[];
  workerConfigurationMethods: string[];
  uiPreferenceResponse: StubResponse;
  uiPreferenceWriteResponse: StubResponse;
  uiPreferenceMethods: string[];
  uiPreferenceBodies: unknown[];
  controlPlatformVisualResponse: StubResponse;
  controlPlatformCoverResponse: StubResponse;
  controlPlatformCoverWriteResponse: StubResponse;
  controlPlatformCoverMethods: string[];
  controlPlatformCoverBodies: unknown[];
};

export function createBrowserRouteFixture(browser: BrowserHarness): BrowserRouteFixture {
  const fixture: BrowserRouteFixture = {
    authResponse: { body: currentUser },
    setupResponse: { body: { setup_enabled: true, setup_required: false } },
    healthResponse: { body: healthyRows },
    versionFixture: { body: currentVersion },
    refreshResponse: { body: { status: "ok" } },
    logoutResponse: { body: { status: "ok" } },
    loginResponse: { body: { csrf_token: "browser-fixture-csrf" } },
    streamsResponse: { body: [] },
    startReadinessResponse: {
    body: { stream_id: startReadinessStream.id, ready: true, missing_service_types: [], issues: [], assigned_service_count: 2 },
    requiredResponse: true,
  },
    startReadinessMethods: [],
    workersResponse: { body: workerPilotRows },
    nodesResponse: { body: workerPilotRows },
    configurationResponse: { body: workerConfiguration("worker-one", "BROWSER-CONFIG-MARKER") },
    restartResponse: { status: 202, body: { status: "accepted" } },
    workerRestartMethods: [],
    workerConfigurationMethods: [],
    uiPreferenceResponse: { body: { theme_id: "autostream", color_mode: "light", revision: 4 } },
    uiPreferenceWriteResponse: { body: { theme_id: "violet", color_mode: "light", revision: 5 } },
    uiPreferenceMethods: [],
    uiPreferenceBodies: [],
    controlPlatformVisualResponse: { body: {
    stream_id: controlPlatformStream.id,
    background_mode: "image",
    header_title_mode: "custom",
    header_title_value: "配信ビジュアル見出し",
    discord_target_mode: "preset",
    discord_target_preset_revision: 3,
    discord_snapshot_revision: 5,
    discord_preset_deleted: true,
    cover_source: "upload",
    cover_start_active: false,
    revision: 2,
  } },
    controlPlatformCoverResponse: { body: controlPlatformCoverState(false, 1, false, 1, "idle") },
    controlPlatformCoverWriteResponse: { body: controlPlatformCoverState(true, 2, true, 2, "applied") },
    controlPlatformCoverMethods: [],
    controlPlatformCoverBodies: [],
  };
  browser.setRouteResolver(({ method, url, postData }) => {
    const pathname = normalizePath(new URL(url).pathname);
    if (pathname === "/auth/me" && method === "GET") return { ...fixture.authResponse, requiredResponse: fixture.authResponse.requiredResponse ?? false };
    if (pathname === "/setup/status" && method === "GET") return { ...fixture.setupResponse, requiredResponse: fixture.setupResponse.requiredResponse ?? false };
    if (pathname === "/settings/app" && method === "GET") return { body: { app_name: "AutoStream", timezone: "Asia/Tokyo" }, requiredResponse: false };
    if (pathname === "/auth/oauth/providers" && method === "GET") return { body: [], requiredResponse: false };
    if (pathname === "/auth/mfa/status" && method === "GET") return { body: { enabled: false }, requiredResponse: false };
    if (pathname === "/auth/passkeys" && method === "GET") return { body: [], requiredResponse: false };
    if (pathname === "/auth/oauth-links" && method === "GET") return { body: [], requiredResponse: false };
    if (pathname === "/account/preferences/ui") {
      fixture.uiPreferenceMethods.push(method);
      if (method === "GET") return { ...fixture.uiPreferenceResponse, requiredResponse: fixture.uiPreferenceResponse.requiredResponse ?? false };
      if (method === "PUT") {
				fixture.uiPreferenceBodies.push(JSON.parse(postData || "null"));
				return fixture.uiPreferenceWriteResponse;
			}
      return { status: 405, body: { code: "method_not_allowed" } };
    }
    if (pathname === "/auth/login" && method === "POST") return fixture.loginResponse;
    if (pathname === "/auth/logout" && method === "POST") return fixture.logoutResponse;
    if (pathname === "/streams" && method === "GET") return { ...fixture.streamsResponse, requiredResponse: fixture.streamsResponse.requiredResponse ?? false };
    if (pathname === controlPlatformVisualPath && method === "GET") return { ...fixture.controlPlatformVisualResponse, requiredResponse: fixture.controlPlatformVisualResponse.requiredResponse ?? false };
		if (pathname === controlPlatformCoverPath) {
			fixture.controlPlatformCoverMethods.push(method);
			if (method === "GET") return { ...fixture.controlPlatformCoverResponse, requiredResponse: fixture.controlPlatformCoverResponse.requiredResponse ?? false };
			if (method === "PUT") {
				fixture.controlPlatformCoverBodies.push(JSON.parse(postData || "null"));
				return fixture.controlPlatformCoverWriteResponse;
			}
      return { status: 405, body: { code: "method_not_allowed" } };
    }
    if (pathname === "/workers" && method === "GET") return { ...fixture.workersResponse, requiredResponse: fixture.workersResponse.requiredResponse ?? false };
    if (pathname === "/nodes" && method === "GET") return { ...fixture.nodesResponse, requiredResponse: fixture.nodesResponse.requiredResponse ?? false };
    const configurationMatch = pathname.match(/^\/nodes\/([^/]+)\/configuration$/);
    if (configurationMatch && method === "GET") {
      fixture.workerConfigurationMethods.push(method);
      return fixture.configurationResponse;
    }
    const restartMatch = pathname.match(/^\/workers\/([^/]+)\/restart$/);
    if (restartMatch && method === "POST") {
      fixture.workerRestartMethods.push(method);
      return fixture.restartResponse;
    }
    if (pathname === startReadinessPath) {
      fixture.startReadinessMethods.push(method);
      return method === "POST" ? fixture.startReadinessResponse : { status: 405, body: { code: "method_not_allowed" } };
    }
    if (pathname === "/service-health" && method === "GET") return { ...fixture.healthResponse, requiredResponse: fixture.healthResponse.requiredResponse ?? false };
    if (pathname === "/version" && method === "GET") return { ...fixture.versionFixture, requiredResponse: fixture.versionFixture.requiredResponse ?? false };
    if (pathname === "/auth/session/refresh" && method === "POST") return fixture.refreshResponse;
    return null;
  });
  return fixture;
}
