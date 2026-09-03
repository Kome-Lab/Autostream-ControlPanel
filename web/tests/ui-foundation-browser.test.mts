import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import test from "node:test";

import { BrowserHarness, ensureWebServer, type StubResponse } from "./helpers/browser-harness.mts";
import {
  assertStreamsStartReadinessHandlerGuard,
  mutateStreamsStartReadinessHandlerGuard,
  type StreamsStartReadinessGuardSources,
  type StreamsStartReadinessGuardMutation,
} from "./helpers/streams-start-readiness-handler-guard.mts";
import { loginPathForLocation } from "../src/lib/auth/post-login-redirect.ts";
import {
  assertAuthMeExpiryOutcome,
  assertBrowserSuiteExecution,
  assertLoginNavigationOutcome,
  assertLogoutOutcome,
  assertNavigationBoundaryOutcome,
  assertNoBrowserConsoleErrors,
  authMeExpiryScenarioName,
  type BrowserSuiteSummary,
  type NavigationSnapshot,
} from "./helpers/ui-foundation-assertions.mts";

const webRoot = fileURLToPath(new URL("..", import.meta.url));
const requestedBaseUrl = process.env.AUTOSTREAM_BROWSER_BASE_URL || "http://127.0.0.1:3002";
const localeStorageKey = "autostream.controlPanel.locale";
const themeStorageKey = "autostream.theme";
const csrfStorageKey = "autostream.csrf_token";
const expectedAuthMeExpiryReturnURL = "/admin/streams/?view=active#preview";
const loginReturnParameterName = productionLoginReturnParameterName();

const currentUser = {
  user: { id: "ui-foundation-user", username: "ui-foundation-admin", email: "operator@example.test", roles: ["super_admin"] },
  permissions: ["*"],
};
const healthyRows = [
  { id: "worker-one", service_id: "worker-one", service_type: "worker", service_name: "Worker One", status: "online", health_status: "healthy", reported_capabilities: { scene_appearance_v1: true } },
  { id: "encoder-one", service_id: "encoder-one", service_type: "encoder_recorder", service_name: "Encoder One", status: "offline", health_status: "unhealthy", reported_capabilities: { live_video_cover_v1: true } },
];
const workerPilotRows = [
  { service_id: "worker-one", service_type: "worker", service_name: "Worker One", status: "online", health_status: "healthy" },
  { service_id: "worker-future", service_type: "worker", service_name: "Future Worker", status: "future_online_v2", health_status: "future_healthy_v2" },
  { service_id: "worker-assigned", service_type: "worker", service_name: "Assigned Worker", status: "assigned" },
  { service_id: "worker-former-alias", service_type: "worker", service_name: "Former Alias Worker", status: "future_connectivity", health_status: "ok" },
  { service_id: "worker-degraded", service_type: "worker", service_name: "Degraded Worker", status: "degraded", health_status: "future_health" },
];
const currentVersion = versionResponse({ latestVersion: "v1.2.4" });
const availableVersion = versionResponse({ latestVersion: "v1.3.0", updateAvailable: true });
const startReadinessStream = { id: "stream-permission-fixture", name: "権限検証配信", status: "ready" };
const startReadinessPath = `/streams/${startReadinessStream.id}/start-readiness`;
const controlPlatformStream = { id: "stream-control-platform", name: "ビジュアル確認配信", status: "ready", assigned_worker_id: "worker-one", assigned_encoder_id: "encoder-one" };
const controlPlatformVisualPath = `/streams/${controlPlatformStream.id}/visual-settings`;
const controlPlatformCoverPath = `/streams/${controlPlatformStream.id}/video-cover-state`;
const controlPlatformPipeline = ["base_or_worker_scene", "video_cover", "watermark", "video_encode", "tee_live_archive_preview"];

test("UI Foundation runtime behavior", { timeout: 420_000 }, async (t) => {
  const server = await ensureWebServer(webRoot, requestedBaseUrl);
  const browserPromise = BrowserHarness.launch();
  t.after(async () => {
    const browserForCleanup = await browserPromise.catch(() => undefined);
    await browserForCleanup?.close();
    await server.close();
  });
  const browser = await browserPromise;

  let authResponse: StubResponse = { body: currentUser };
  let setupResponse: StubResponse = { body: { setup_enabled: true, setup_required: false } };
  let healthResponse: StubResponse = { body: healthyRows };
  let versionFixture: StubResponse = { body: currentVersion };
  let refreshResponse: StubResponse = { body: { status: "ok" } };
  let logoutResponse: StubResponse = { body: { status: "ok" } };
  let loginResponse: StubResponse = { body: { csrf_token: "browser-fixture-csrf" } };
  let streamsResponse: StubResponse = { body: [] };
  let startReadinessResponse: StubResponse = {
    body: { stream_id: startReadinessStream.id, ready: true, missing_service_types: [], issues: [], assigned_service_count: 2 },
    requiredResponse: true,
  };
  let startReadinessMethods: string[] = [];
  let workersResponse: StubResponse = { body: workerPilotRows };
  let nodesResponse: StubResponse = { body: workerPilotRows };
  let configurationResponse: StubResponse = { body: workerConfiguration("worker-one", "BROWSER-CONFIG-MARKER") };
  let restartResponse: StubResponse = { status: 202, body: { status: "accepted" } };
  let workerRestartMethods: string[] = [];
  let workerConfigurationMethods: string[] = [];
  let uiPreferenceResponse: StubResponse = { body: { theme_id: "autostream", color_mode: "light", revision: 4 } };
  let uiPreferenceWriteResponse: StubResponse = { body: { theme_id: "violet", color_mode: "light", revision: 5 } };
  let uiPreferenceMethods: string[] = [];
  let uiPreferenceBodies: unknown[] = [];
  let controlPlatformVisualResponse: StubResponse = { body: {
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
  } };
  let controlPlatformCoverResponse: StubResponse = { body: controlPlatformCoverState(false, 1, false, 1, "idle") };
	let controlPlatformCoverWriteResponse: StubResponse = { body: controlPlatformCoverState(true, 2, true, 2, "applied") };
	let controlPlatformCoverMethods: string[] = [];
	let controlPlatformCoverBodies: unknown[] = [];
  browser.setRouteResolver(({ method, url, postData }) => {
    const pathname = normalizePath(new URL(url).pathname);
    if (pathname === "/auth/me" && method === "GET") return { ...authResponse, requiredResponse: authResponse.requiredResponse ?? false };
    if (pathname === "/setup/status" && method === "GET") return { ...setupResponse, requiredResponse: setupResponse.requiredResponse ?? false };
    if (pathname === "/settings/app" && method === "GET") return { body: { app_name: "AutoStream", timezone: "Asia/Tokyo" }, requiredResponse: false };
    if (pathname === "/auth/oauth/providers" && method === "GET") return { body: [], requiredResponse: false };
    if (pathname === "/auth/mfa/status" && method === "GET") return { body: { enabled: false }, requiredResponse: false };
    if (pathname === "/auth/passkeys" && method === "GET") return { body: [], requiredResponse: false };
    if (pathname === "/auth/oauth-links" && method === "GET") return { body: [], requiredResponse: false };
    if (pathname === "/account/preferences/ui") {
      uiPreferenceMethods.push(method);
      if (method === "GET") return { ...uiPreferenceResponse, requiredResponse: uiPreferenceResponse.requiredResponse ?? false };
      if (method === "PUT") {
				uiPreferenceBodies.push(JSON.parse(postData || "null"));
				return uiPreferenceWriteResponse;
			}
      return { status: 405, body: { code: "method_not_allowed" } };
    }
    if (pathname === "/auth/login" && method === "POST") return loginResponse;
    if (pathname === "/auth/logout" && method === "POST") return logoutResponse;
    if (pathname === "/streams" && method === "GET") return { ...streamsResponse, requiredResponse: streamsResponse.requiredResponse ?? false };
    if (pathname === controlPlatformVisualPath && method === "GET") return { ...controlPlatformVisualResponse, requiredResponse: controlPlatformVisualResponse.requiredResponse ?? false };
		if (pathname === controlPlatformCoverPath) {
			controlPlatformCoverMethods.push(method);
			if (method === "GET") return { ...controlPlatformCoverResponse, requiredResponse: controlPlatformCoverResponse.requiredResponse ?? false };
			if (method === "PUT") {
				controlPlatformCoverBodies.push(JSON.parse(postData || "null"));
				return controlPlatformCoverWriteResponse;
			}
      return { status: 405, body: { code: "method_not_allowed" } };
    }
    if (pathname === "/workers" && method === "GET") return { ...workersResponse, requiredResponse: workersResponse.requiredResponse ?? false };
    if (pathname === "/nodes" && method === "GET") return { ...nodesResponse, requiredResponse: nodesResponse.requiredResponse ?? false };
    const configurationMatch = pathname.match(/^\/nodes\/([^/]+)\/configuration$/);
    if (configurationMatch && method === "GET") {
      workerConfigurationMethods.push(method);
      return configurationResponse;
    }
    const restartMatch = pathname.match(/^\/workers\/([^/]+)\/restart$/);
    if (restartMatch && method === "POST") {
      workerRestartMethods.push(method);
      return restartResponse;
    }
    if (pathname === startReadinessPath) {
      startReadinessMethods.push(method);
      return method === "POST" ? startReadinessResponse : { status: 405, body: { code: "method_not_allowed" } };
    }
    if (pathname === "/service-health" && method === "GET") return { ...healthResponse, requiredResponse: healthResponse.requiredResponse ?? false };
    if (pathname === "/version" && method === "GET") return { ...versionFixture, requiredResponse: versionFixture.requiredResponse ?? false };
    if (pathname === "/auth/session/refresh" && method === "POST") return refreshResponse;
    return null;
  });

  await t.test("query states distinguish loading, empty, unhealthy, error, stale, recovery, and update variants", async () => {
    try {
      authResponse = { body: currentUser };
      await browser.setViewport(1440, 900);
      healthResponse = { body: healthyRows, delayMs: 1_200 };
      versionFixture = { body: availableVersion, delayMs: 1_200 };
      await browser.navigate(`${server.baseUrl}/admin/streams/`);
      const initial = await browser.waitFor(
        statusSnapshotExpression,
        (value: StatusSnapshot) => value.health === "稼働状況を確認中" && value.update === "更新情報を確認中",
        "initial loading states did not render",
      );
      assertStatusSnapshot(initial, { health: "稼働状況を確認中", update: "更新情報を確認中" });
      await browser.waitFor(
        statusSnapshotExpression,
        (value: StatusSnapshot) => value.health === "1/2 サービス稼働" && value.update === "更新 v1.3.0 を利用できます",
        "ready states did not replace loading",
      );

      healthResponse = { body: [] };
      versionFixture = { body: currentVersion };
      await browser.reload();
      const empty = await waitForStatus(browser, "登録済みサービスなし", "最新版です");
      assertStatusSnapshot(empty, { health: "登録済みサービスなし", update: "最新版です" });

      healthResponse = { status: 503, body: { code: "service_health_unavailable" } };
      versionFixture = { status: 503, body: { code: "version_unavailable" } };
      browser.clearRequestCounts();
      await browser.reload();
      await browser.waitForResponseCount("/service-health", 1);
      await browser.waitForResponseCount("/version", 1);
      const failed = await browser.waitFor(
        statusSnapshotExpression,
        (value: StatusSnapshot) => value.health === "稼働状況を取得できません" && value.update === "更新情報を取得できません",
        "503 responses remained loading or disappeared",
        15_000,
      );
      assertStatusSnapshot(failed, { health: "稼働状況を取得できません", update: "更新情報を取得できません" });
      assert.throws(
        () => assertStatusSnapshot({ health: "稼働状況を確認中", update: "更新情報を確認中" }, {
          health: "稼働状況を取得できません",
          update: "更新情報を取得できません",
        }),
        /health status/,
        "negative fixture: projecting a 503 as loading must fail",
      );

      healthResponse = { body: healthyRows };
      versionFixture = { body: availableVersion };
      await browser.reload();
      await waitForStatus(browser, "1/2 サービス稼働", "更新 v1.3.0 を利用できます");
      healthResponse = { status: 503, body: { code: "service_health_unavailable" } };
      await triggerReconnect(browser);
      const stale = await browser.waitFor(
        statusSnapshotExpression,
        (value: StatusSnapshot) => value.health === "1/2 サービス稼働（更新失敗）",
        "cached health did not become stale after refresh failure",
        15_000,
      );
      assert.equal(stale.health, "1/2 サービス稼働（更新失敗）");

      healthResponse = { body: healthyRows };
      await triggerReconnect(browser);
      await browser.waitFor(
        statusSnapshotExpression,
        (value: StatusSnapshot) => value.health === "1/2 サービス稼働",
        "health status did not recover without a page reload",
        5_000,
      );

      versionFixture = { body: versionResponse({ source: "disabled" }) };
      await browser.reload();
      await waitForStatus(browser, "1/2 サービス稼働", "更新情報は未確認です");

      healthResponse = { body: healthyRows };
      versionFixture = { body: currentVersion };
      browser.clearRequestCounts();
      await browser.navigate(`${server.baseUrl}/admin/`);
      await waitForStatus(browser, "1/2 サービス稼働", "最新版です");
      assert.equal(browser.requests.get("/service-health"), 1, "Shell and Dashboard must share the service-health request");
    } finally {
      authResponse = { body: currentUser };
      healthResponse = { body: healthyRows };
      versionFixture = { body: currentVersion };
    }
  });

  await t.test("logout clears protected UI and sends exactly one mutation", async () => {
    const logoutRelease = deferred();
    try {
      authResponse = { body: currentUser };
      logoutResponse = { body: { status: "ok" }, waitUntil: logoutRelease.promise };
      await browser.setViewport(1440, 900);
      await setStoredDisplay(browser, "en", "light");
      await browser.navigate(`${server.baseUrl}/admin/streams/`);
      await waitForShell(browser, "Account menu");
      await browser.evaluate(`sessionStorage.setItem(${JSON.stringify(csrfStorageKey)}, 'logout-fixture'); true`);
      browser.clearRequestCounts();
      browser.clearConsoleErrors();

      await browser.clickSelector('button[aria-label="Account menu"]');
      await browser.clickRoleWithText("menuitem", "Log out");
      await browser.waitForRequestCount("/auth/logout", 1);

      await browser.clickSelector('button[aria-label="Account menu"]');
      await browser.clickRoleWithText("menuitem", "Log out");
      await waitForAnimationFrames(browser);
      assert.equal(browser.requests.get("/auth/logout"), 1, "duplicate logout selection must not send another mutation");

      logoutRelease.resolve();
      const outcome = await browser.waitFor<LogoutBrowserSnapshot>(
        logoutSnapshotExpression,
        (value) => value.pathname === "/login/" && !value.protectedAdminLandmarkPresent && !value.accountMenuPresent,
        "logout did not remove the authenticated shell",
        15_000,
      );
      assertLogoutOutcome({ ...outcome, logoutRequestCount: browser.requests.get("/auth/logout") || 0 });
      assertNoBrowserConsoleErrors(browser.consoleErrorCount);
    } finally {
      logoutRelease.resolve();
      logoutResponse = { body: { status: "ok" } };
    }
  });

  await t.test("session expiry keeps a validated same-origin return URL without a redirect loop", async () => {
    authResponse = { body: currentUser };
    refreshResponse = { status: 401, body: { code: "unauthorized" } };
    await browser.setViewport(1440, 900);
    await setStoredDisplay(browser, "en", "light");
    await browser.navigate(`${server.baseUrl}/admin/streams/?view=active`);
    await waitForShell(browser, "Account menu");
    await browser.evaluate(`sessionStorage.setItem(${JSON.stringify(csrfStorageKey)}, 'expired-fixture'); true`);
    browser.clearRequestCounts();
    browser.clearConsoleErrors();

    await browser.evaluate("window.dispatchEvent(new Event('focus')); true");
    const outcome = await browser.waitFor<SessionExpirySnapshot>(
      sessionExpirySnapshotExpression,
      (value) => value.pathname === "/login/" && value.reason === "session_expired",
      "session expiry did not reach the login page",
      15_000,
    );
    await browser.waitForRequestCount("/auth/session/refresh", 1);
    assertLoginNavigationOutcome({ href: outcome.href, expectedOrigin: server.baseUrl, expectedPathname: "/login/" });
    assert.equal(outcome.redirectAfter, "/admin/streams/?view=active");
    assert.equal(outcome.csrfTokenPresent, false);
    assert.equal(outcome.protectedAdminLandmarkPresent, false);

    await browser.evaluate("window.dispatchEvent(new Event('focus')); window.dispatchEvent(new Event('pointerdown')); true");
    await waitForAnimationFrames(browser);
    assert.equal(await browser.evaluate<string>("location.pathname"), "/login/", "login page must not redirect itself");
    assert.equal(browser.requests.get("/auth/session/refresh"), 1, "repeated activity after expiry must not refresh again");
    assertNoBrowserConsoleErrors(browser.consoleErrorCount);
    refreshResponse = { body: { status: "ok" } };
  });

  await t.test(authMeExpiryScenarioName, async () => {
    authResponse = { body: currentUser };
    refreshResponse = { body: { status: "ok" } };
    await browser.setViewport(1440, 900);
    await setStoredDisplay(browser, "en", "light");
    browser.clearRequestCounts();
    await browser.navigate(`${server.baseUrl}${expectedAuthMeExpiryReturnURL}`);
    await waitForShell(browser, "Account menu");
    await browser.waitForResponseCount("/auth/me", 1);
    assert.deepEqual(browser.responseStatuses.get("/auth/me"), [200], "initial /auth/me response status");
    const authenticatedShell = await browser.evaluate<{
      location: string;
      protectedAdminLandmarkPresent: boolean;
      accountMenuPresent: boolean;
    }>(`(() => ({
      location: location.pathname + location.search + location.hash,
      protectedAdminLandmarkPresent: Boolean(document.querySelector('nav[aria-label="Admin navigation"]')),
      accountMenuPresent: Boolean(document.querySelector('button[aria-label="Account menu"]')),
    }))()`);
    assert.equal(authenticatedShell.location, expectedAuthMeExpiryReturnURL);
    assert.equal(authenticatedShell.protectedAdminLandmarkPresent, true);
    assert.equal(authenticatedShell.accountMenuPresent, true);
    await browser.evaluate(`sessionStorage.setItem(${JSON.stringify(csrfStorageKey)}, 'auth-me-expiry-fixture'); true`);

    authResponse = { status: 401, body: { code: "unauthorized" } };
    browser.clearRequestCounts();
    browser.clearNavigationCount();
    browser.clearConsoleErrors();
    await browser.evaluate("document.dispatchEvent(new Event('visibilitychange', { bubbles: true })); true");

    await browser.waitForResponseCount("/auth/me", 1);
    await browser.waitForResponseCount("/auth/session/refresh", 1);
    const outcome = await browser.waitFor<AuthMeExpiryBrowserSnapshot>(
      authMeExpirySnapshotExpression(loginReturnParameterName),
      (value) => value.pathname === "/login/" && value.reason === "session_expired",
      "/auth/me 401 did not reach the session-expiry login page",
      15_000,
    );

    await browser.evaluate("document.dispatchEvent(new Event('visibilitychange')); window.dispatchEvent(new Event('focus')); true");
    await waitForAnimationFrames(browser);
    assertAuthMeExpiryOutcome({
      ...outcome,
      authMeRequestCount: browser.requests.get("/auth/me") || 0,
      authMeResponseStatuses: [...(browser.responseStatuses.get("/auth/me") || [])],
      expectedOrigin: server.baseUrl,
      expectedReturnURL: expectedAuthMeExpiryReturnURL,
      navigationCount: browser.navigationCount,
      navigationSucceeded: true,
      refreshResponseStatuses: [...(browser.responseStatuses.get("/auth/session/refresh") || [])],
    });
    assertNoBrowserConsoleErrors(browser.consoleErrorCount);

    authResponse = { body: currentUser };
    refreshResponse = { body: { status: "ok" } };
  });

  await t.test("stale refresh completion does not replace a newer authenticated session", async () => {
    const refreshRelease = deferred();
    try {
      const previousUser = {
        user: { id: "previous-session", username: "previous-operator", roles: ["super_admin"] },
        permissions: ["*"],
      };
      const newerUser = {
        user: { id: "newer-session", username: "newer-operator", roles: ["super_admin"] },
        permissions: ["*"],
      };
      authResponse = { body: previousUser };
      refreshResponse = { body: { status: "ok" }, waitUntil: refreshRelease.promise };
      loginResponse = { body: { csrf_token: "newer-session-csrf" } };
      await browser.setViewport(1440, 900);
      await browser.navigate(`${server.baseUrl}/admin/streams/`);
      await browser.waitFor(
        "document.querySelector('button[aria-label=\"Account menu\"]')?.textContent || ''",
        (value: string) => value.includes("previous-operator"),
        "previous authenticated session did not render",
      );
      browser.clearRequestCounts();
      browser.clearConsoleErrors();

      authResponse = { body: newerUser };
      await browser.evaluate("window.dispatchEvent(new Event('focus')); true");
      await browser.waitForRequestCount("/auth/session/refresh", 1);

      logoutResponse = { body: { status: "ok" } };
      await browser.clickSelector('button[aria-label="Account menu"]');
      await browser.clickRoleWithText("menuitem", "Log out");
      await browser.waitFor("location.pathname", (value: string) => value === "/login/", "logout did not expose the new-session login flow");
      await browser.waitFor(
        `Boolean(document.querySelector('input[autocomplete="username"]') && document.querySelector('button[type="submit"]:not([disabled])'))`,
        Boolean,
        "new-session login form did not become interactive",
      );
      await browser.fillSelector('input[autocomplete="username"]', "newer-operator");
      await browser.fillSelector('input[autocomplete="current-password"]', "browser-fixture-password");
      await browser.clickSelector('form button[type="submit"]:not([disabled])');
      await browser.waitFor("location.pathname", (value: string) => value === "/admin/", "new session did not reach the authenticated shell");
      const nextAuthResponseCount = (browser.responses.get("/auth/me") || 0) + 1;
      await triggerReconnect(browser);
      await browser.waitForResponseCount("/auth/me", nextAuthResponseCount, 25_000);
      await browser.waitFor(
        "document.querySelector('button[aria-label=\"Account menu\"]')?.textContent || ''",
        (value: string) => value.includes("newer-operator"),
        "new authenticated session did not replace the visible user",
      );

      refreshRelease.resolve();
      await browser.waitForResponseCount("/auth/session/refresh", 1);
      await waitForAnimationFrames(browser);
      const retained = await browser.evaluate<{ username: string; csrf: string | null; pathname: string }>(`(() => ({
        username: document.querySelector('button[aria-label="Account menu"]')?.textContent || '',
        csrf: sessionStorage.getItem(${JSON.stringify(csrfStorageKey)}),
        pathname: location.pathname,
      }))()`);
      assert.match(retained.username, /newer-operator/);
      assert.equal(retained.csrf, "newer-session-csrf");
      assert.equal(retained.pathname, "/admin/");
      assert.equal(browser.requests.get("/auth/session/refresh"), 1);
      assertNoBrowserConsoleErrors(browser.consoleErrorCount);
    } finally {
      refreshRelease.resolve();
      authResponse = { body: currentUser };
      refreshResponse = { body: { status: "ok" } };
      logoutResponse = { body: { status: "ok" } };
      loginResponse = { body: { csrf_token: "browser-fixture-csrf" } };
    }
  });

  await t.test("session guard ignores setup completion after unmount", async () => {
    const setupRelease = deferred();
    try {
      authResponse = { status: 401, body: { code: "unauthorized" } };
      setupResponse = { body: { setup_enabled: true, setup_required: true }, waitUntil: setupRelease.promise };
      await browser.waitForRequestHandlersIdle({ pathname: "/setup/status", method: "GET" });
      browser.clearRequestCounts("/setup/status");
      browser.clearConsoleErrors();
      await browser.navigate("about:blank");
      await browser.navigate(`${server.baseUrl}/admin/streams/?guard-unmount=1`);
      await browser.waitForResponseCount("/auth/me", 1);
      await browser.waitFor(
        `Boolean(document.querySelector('a[href="/login/"]'))`,
        Boolean,
        "authentication-pending login action did not render",
      );
      await browser.waitForRequestCount("/setup/status", 1);
      await browser.clickSelector('a[href="/login/"]');
      await browser.waitFor("location.pathname", (value: string) => value === "/login/", "login navigation did not unmount the guard");

      setupRelease.resolve();
      await browser.waitForRequestHandlersIdle({ pathname: "/setup/status", method: "GET" });
      assert.equal(
        browser.responses.get("/setup/status") || 0,
        browser.requests.get("/setup/status") || 0,
        "every observed setup request must settle before checking the unmounted guard",
      );
      await waitForAnimationFrames(browser);
      assert.equal(await browser.evaluate<string>("location.pathname"), "/login/", "unmounted guard must not apply a stale setup redirect");
      assertNoBrowserConsoleErrors(browser.consoleErrorCount);
    } finally {
      setupRelease.resolve();
      authResponse = { body: currentUser };
      setupResponse = { body: { setup_enabled: true, setup_required: false } };
    }
  });

  await t.test("login rejects external return URL variants", async () => {
    authResponse = { body: currentUser };
    loginResponse = { body: { csrf_token: "login-fixture-csrf" } };
    setupResponse = { body: { setup_enabled: true, setup_required: false } };
    const unsafeReturnURLs = [
      "https://attacker.example/",
      "//attacker.example/",
      "javascript:alert(1)",
      "https%3A%2F%2Fattacker.example%2Fadmin%2F",
      "/admin\\@attacker.example/",
    ];

    for (const unsafeReturnURL of unsafeReturnURLs) {
      const query = new URLSearchParams({ redirect_after: unsafeReturnURL });
      await browser.navigate(`${server.baseUrl}/login/?${query}`);
      await browser.waitFor(
        `Boolean(document.querySelector('form input[autocomplete="username"]') && document.querySelector('form button[type="submit"]:not([disabled])'))`,
        Boolean,
        "login form did not become interactive",
      );
      await browser.fillSelector('input[autocomplete="username"]', "browser-operator");
      await browser.fillSelector('input[autocomplete="current-password"]', "browser-fixture-password");
      browser.clearRequestCounts();
      browser.clearConsoleErrors();
      await browser.clickSelector('form button[type="submit"]:not([disabled])');
      const href = await browser.waitFor(
        "location.href",
        (value: string) => new URL(value).pathname === "/admin/",
        `unsafe return URL was not normalized: ${unsafeReturnURL}`,
        15_000,
      );
      assertLoginNavigationOutcome({ href, expectedOrigin: server.baseUrl, expectedPathname: "/admin/" });
      assert.equal(browser.requests.get("/auth/login"), 1);
      assertNoBrowserConsoleErrors(browser.consoleErrorCount);
    }
  });

  await t.test("Streams start-readiness follows streams.start at render and confirm time", async (t) => {
    const readinessSelector = `button[aria-label=${JSON.stringify(`${startReadinessStream.name} の開始準備を再確認`)}]`;
    const editSelector = `button[aria-label=${JSON.stringify(`${startReadinessStream.name} を編集`)}]`;
    const actionSnapshotExpression = `(() => {
      const visibleButton = (selector) => [...document.querySelectorAll(selector)]
        .find((element) => element instanceof HTMLButtonElement && element.getClientRects().length > 0);
      const readiness = visibleButton(${JSON.stringify(readinessSelector)});
      const edit = visibleButton(${JSON.stringify(editSelector)});
      const reasonId = readiness?.getAttribute("aria-describedby");
      return {
        readinessPresent: Boolean(readiness),
        readinessAvailable: readiness instanceof HTMLButtonElement && !readiness.disabled,
        editAvailable: edit instanceof HTMLButtonElement && !edit.disabled,
        readinessAvailabilityKind: readiness?.closest("[data-action-availability]")?.getAttribute("data-action-availability") || "allowed",
        readinessReason: reasonId ? document.getElementById(reasonId)?.textContent || "" : "",
      };
    })()`;
    const successNotice = `${startReadinessStream.name}の開始準備確認を受け付けました。最新状態を確認してください。`;
    const successResponse = {
      body: { stream_id: startReadinessStream.id, ready: true, missing_service_types: [], issues: [], assigned_service_count: 2 },
      requiredResponse: true,
    };
    const matrix = [
      { name: "start only", permissions: ["streams.read", "streams.start"], readinessAvailable: true, editAvailable: false, requestCount: 1 },
      { name: "update only", permissions: ["streams.read", "streams.update"], readinessAvailable: false, editAvailable: true, requestCount: 0 },
      { name: "both", permissions: ["streams.read", "streams.start", "streams.update"], readinessAvailable: true, editAvailable: true, requestCount: 1 },
      { name: "neither", permissions: ["streams.read"], readinessAvailable: false, editAvailable: false, requestCount: 0 },
      { name: "wildcard", permissions: ["*"], readinessAvailable: true, editAvailable: true, requestCount: 1 },
    ] as const;

    await t.test("handler guard structural oracle rejects in-memory regressions", () => {
      const sources: StreamsStartReadinessGuardSources = {
        view: readFileSync(new URL("../src/features/streams/streams-view.tsx", import.meta.url), "utf8"),
        controller: readFileSync(new URL("../src/features/streams/stream-action-controller.ts", import.meta.url), "utf8"),
        descriptors: readFileSync(new URL("../src/features/streams/stream-action-descriptors.ts", import.meta.url), "utf8"),
      };
      assert.doesNotThrow(
        () => assertStreamsStartReadinessHandlerGuard(sources),
        "actual start-readiness submit handler must retain its fresh permission guard",
      );
      const negativeFixtures: {
        name: string;
        mutation: StreamsStartReadinessGuardMutation;
        expectedError: RegExp;
      }[] = [
        { name: "current permission snapshot removed", mutation: "remove-current-permission-snapshot", expectedError: /current permission snapshot/ },
        { name: "streams.start replaced by streams.update", mutation: "use-streams-update-authority", expectedError: /descriptor authority must remain streams\.start/ },
        { name: "pre-submit evaluation removed", mutation: "remove-pre-submit-evaluation", expectedError: /pre-submit evaluator/ },
        { name: "mutation moved before guard", mutation: "move-mutation-before-guard", expectedError: /mutation must follow every pre-submit guard/ },
        { name: "alternate unguarded mutation added", mutation: "add-alternate-unguarded-mutation", expectedError: /exactly one guarded mutation path/ },
      ];
      for (const fixture of negativeFixtures) {
        const mutatedSources = mutateStreamsStartReadinessHandlerGuard(sources, fixture.mutation);
        assert.throws(
          () => assertStreamsStartReadinessHandlerGuard(mutatedSources),
          fixture.expectedError,
          `${fixture.name} must make the structural oracle Red`,
        );
      }
    });

    try {
      streamsResponse = { body: [startReadinessStream] };
      healthResponse = { body: healthyRows };
      versionFixture = { body: currentVersion };
      await browser.setViewport(1440, 900);
      await setStoredDisplay(browser, "ja", "light");

      for (const matrixCase of matrix) {
        await t.test(matrixCase.name, async () => {
          await waitForStartReadinessHandlersIdle(browser);
          authResponse = { body: permissionUser([...matrixCase.permissions]) };
          startReadinessResponse = successResponse;
          startReadinessMethods = [];
          browser.clearRequestCounts(startReadinessPath);
          browser.clearConsoleErrors();
          await browser.navigate(`${server.baseUrl}/admin/streams/`);
          await waitForShell(browser, "アカウントメニュー");
          const initial = await browser.waitFor<{ readinessPresent: boolean; readinessAvailable: boolean; editAvailable: boolean }>(
            actionSnapshotExpression,
            (value) => value.readinessPresent,
            `${matrixCase.name}: start-readiness action did not render`,
          );

          if (initial.readinessAvailable) {
            await browser.clickSelector(readinessSelector);
            await browser.waitFor(
              "Boolean(document.querySelector('[data-slot=\"alert-dialog-content\"][data-state=\"open\"]'))",
              Boolean,
              `${matrixCase.name}: start-readiness confirmation did not open`,
            );
            await browser.clickSelector('[data-confirm-action]');
            await browser.waitForRequestCount(startReadinessPath, 1);
            await browser.waitForResponseCount(startReadinessPath, 1);
            await browser.waitFor(
              "document.body.textContent || ''",
              (value: string) => value.includes(successNotice),
              `${matrixCase.name}: start-readiness mutation did not reach its public success boundary`,
            );
          } else {
            await browser.clickSelector(readinessSelector);
            await waitForAnimationFrames(browser);
          }
          await waitForStartReadinessHandlersIdle(browser);

          assert.deepEqual(
            {
              readinessAvailable: initial.readinessAvailable,
              editAvailable: initial.editAvailable,
              requestCount: browser.requests.get(startReadinessPath) || 0,
              responseCount: browser.responses.get(startReadinessPath) || 0,
              methods: startReadinessMethods,
            },
            {
              readinessAvailable: matrixCase.readinessAvailable,
              editAvailable: matrixCase.editAvailable,
              requestCount: matrixCase.requestCount,
              responseCount: matrixCase.requestCount,
              methods: matrixCase.requestCount === 1 ? ["POST"] : [],
            },
            `${matrixCase.name}: start-readiness permission authority`,
          );
          assertNoBrowserConsoleErrors(browser.consoleErrorCount);
        });
      }

      await t.test("permission changes before confirm", async () => {
        await waitForStartReadinessHandlersIdle(browser);
        authResponse = { body: permissionUser(["streams.read", "streams.start"]) };
        startReadinessResponse = successResponse;
        await browser.navigate(`${server.baseUrl}/admin/streams/`);
        await waitForShell(browser, "アカウントメニュー");
        await browser.waitFor(
          actionSnapshotExpression,
          (value: { readinessAvailable: boolean }) => value.readinessAvailable,
          "start-readiness action was not initially available",
        );
        await browser.clickSelector(readinessSelector);
        await browser.waitFor(
          "Boolean(document.querySelector('[data-slot=\"alert-dialog-content\"][data-state=\"open\"]'))",
          Boolean,
          "start-readiness confirmation did not open before permission change",
        );

        authResponse = { body: permissionUser(["streams.read"]) };
        await browser.waitForRequestHandlersIdle({ pathname: "/auth/me", method: "GET" });
        browser.clearRequestCounts("/auth/me");
        await browser.evaluate("document.dispatchEvent(new Event('visibilitychange', { bubbles: true })); true");
        await browser.waitForResponseCount("/auth/me", 1);
        await browser.waitForRequestHandlersIdle({ pathname: "/auth/me", method: "GET" });
        await browser.waitFor(
          actionSnapshotExpression,
          (value: { readinessAvailable: boolean }) => !value.readinessAvailable,
          "refetched permissions did not disable start-readiness",
        );

        await waitForStartReadinessHandlersIdle(browser);
        startReadinessMethods = [];
        browser.clearRequestCounts(startReadinessPath);
        assert.equal(
          await browser.evaluate("Boolean(document.querySelector('[data-slot=\"alert-dialog-content\"][data-state=\"open\"]'))"),
          false,
          "permission refresh must dismiss the stale start-readiness confirmation",
        );
        await browser.clickSelector(readinessSelector);
        await waitForAnimationFrames(browser);
        await waitForStartReadinessHandlersIdle(browser);
        assert.deepEqual(
          {
            requestCount: browser.requests.get(startReadinessPath) || 0,
            responseCount: browser.responses.get(startReadinessPath) || 0,
            methods: startReadinessMethods,
          },
          { requestCount: 0, responseCount: 0, methods: [] },
          "a permission change before confirmation must not send a mutation",
        );
      });

      await t.test("backend 403 retains the existing action error mapping", async () => {
        await waitForStartReadinessHandlersIdle(browser);
        authResponse = { body: permissionUser(["streams.read", "streams.start"]) };
        startReadinessResponse = { status: 403, body: { code: "permission_denied" }, requiredResponse: true };
        startReadinessMethods = [];
        browser.clearRequestCounts(startReadinessPath);
        await browser.navigate(`${server.baseUrl}/admin/streams/`);
        await waitForShell(browser, "アカウントメニュー");
        await browser.waitFor(
          actionSnapshotExpression,
          (value: { readinessAvailable: boolean }) => value.readinessAvailable,
          "403 fixture start-readiness action was not available",
        );
        await browser.clickSelector(readinessSelector);
        await browser.waitFor(
          "Boolean(document.querySelector('[data-slot=\"alert-dialog-content\"][data-state=\"open\"]'))",
          Boolean,
          "403 fixture confirmation did not open",
        );
        await browser.clickSelector('[data-confirm-action]');
        await browser.waitForRequestCount(startReadinessPath, 1);
        await browser.waitForResponseCount(startReadinessPath, 1);
        await browser.waitFor(
          "document.body.textContent || ''",
          (value: string) => value.includes("この操作を実行する権限がありません。") && !value.includes("permission_denied"),
          "backend 403 did not use the safe shared API error mapping",
        );
        await browser.waitFor(
          actionSnapshotExpression,
          (value: { readinessAvailable: boolean }) => value.readinessAvailable,
          "backend 403 mutation did not return to its terminal state",
        );
        await waitForStartReadinessHandlersIdle(browser);
        assert.equal(browser.requests.get(startReadinessPath), 1, "backend 403 must not be resent automatically");
        assert.equal(browser.responses.get(startReadinessPath), 1, "backend 403 must have exactly one response");
        assert.deepEqual(startReadinessMethods, ["POST"]);
      });

      await t.test("pending mutation keeps duplicate start-readiness blocked", async () => {
        const release = deferred();
        try {
          await waitForStartReadinessHandlersIdle(browser);
          authResponse = { body: permissionUser(["streams.read", "streams.start"]) };
          startReadinessResponse = { ...successResponse, waitUntil: release.promise };
          startReadinessMethods = [];
          browser.clearRequestCounts(startReadinessPath);
          await browser.navigate(`${server.baseUrl}/admin/streams/`);
          await waitForShell(browser, "アカウントメニュー");
          await browser.waitFor(
            actionSnapshotExpression,
            (value: { readinessAvailable: boolean }) => value.readinessAvailable,
            "pending fixture start-readiness action was not available",
          );
          await browser.clickSelector(readinessSelector);
          await browser.waitFor(
            "Boolean(document.querySelector('[data-slot=\"alert-dialog-content\"][data-state=\"open\"]'))",
            Boolean,
            "pending fixture confirmation did not open",
          );
          await browser.clickSelector('[data-confirm-action]');
          await browser.waitForRequestCount(startReadinessPath, 1);
          await browser.waitFor(
            actionSnapshotExpression,
            (value: { readinessAvailable: boolean }) => !value.readinessAvailable,
            "pending start-readiness mutation did not disable its trigger",
          );
          await browser.clickSelector(readinessSelector);
          await waitForAnimationFrames(browser);
          assert.equal(browser.requests.get(startReadinessPath), 1, "pending start-readiness must not send a duplicate request");
          assert.equal(browser.responses.get(startReadinessPath) || 0, 0, "deferred start-readiness must remain pending before release");
          assert.deepEqual(startReadinessMethods, ["POST"]);
        } finally {
          release.resolve();
          if ((browser.requests.get(startReadinessPath) || 0) > 0) {
            await browser.waitForResponseCount(startReadinessPath, 1);
            await browser.waitFor(
              "document.body.textContent || ''",
              (value: string) => value.includes(successNotice),
              "released start-readiness mutation did not reach its public success boundary",
            );
          }
          await waitForStartReadinessHandlersIdle(browser);
        }
      });
    } finally {
      authResponse = { body: currentUser };
      streamsResponse = { body: [] };
      startReadinessResponse = successResponse;
      startReadinessMethods = [];
    }
  });

  await t.test("Worker restart uses fresh canonical action policy and one POST per worker", async () => {
    const restartPath = "/workers/worker-one/restart";
    const permittedUser = permissionUser(["workers.read", "workers.restart"]);
    let authRefreshRelease: ReturnType<typeof deferred> | undefined;
    let restartRelease: ReturnType<typeof deferred> | undefined;
    try {
      await browser.setViewport(1440, 900);
      await browser.setMediaFeatures([]);
      browser.clearConsoleErrors();
      await setStoredDisplay(browser, "en", "light");
      workersResponse = { body: workerPilotRows };
      nodesResponse = { body: workerPilotRows };
      restartResponse = { status: 202, body: { status: "accepted" }, requiredResponse: true };
      authResponse = { body: permittedUser };
      await browser.navigate(`${server.baseUrl}/admin/workers/`);
      await waitForShell(browser, "Account menu");
      await waitForWorkerAction(browser, "Restart worker", "Worker One", (value) => value.disabled === false);

      await browser.waitForRequestHandlersIdle({ pathname: "/auth/me", method: "GET" });
      const actionOnlyAuthResponseCount = (browser.responses.get("/auth/me") || 0) + 1;
      authResponse = { body: permissionUser(["workers.restart"]) };
      await browser.waitForResponseCount("/auth/me", actionOnlyAuthResponseCount, 20_000);
      const actionOnly = await waitForWorkerAction(browser, "Restart worker", "Worker One", (value) => value.disabled === false);
      assert.equal(actionOnly.reason, "", "workers.restart must not depend on page or Configuration permissions after the row is loaded");

      await browser.waitForRequestHandlersIdle({ pathname: "/auth/me", method: "GET" });
      const refreshingAuthRequestCount = (browser.requests.get("/auth/me") || 0) + 1;
      authRefreshRelease = deferred();
      authResponse = {
        body: permissionUser(["workers.restart"]),
        waitUntil: authRefreshRelease.promise,
      };
      await browser.waitForRequestCount("/auth/me", refreshingAuthRequestCount, 20_000);
      browser.clearRequestCounts(restartPath);
      await clickWorkerAction(browser, "Restart worker");
      const unknown = await waitForWorkerAction(browser, "Restart worker", "Worker One", (value) => value.disabled && value.reason.length > 0);
      assert.match(unknown.reason, /permission could not be verified/i);
      assert.equal(await workerRestartDialogCount(browser), 0, "an unknown restart permission must not open a confirmation");
      assert.equal(browser.requests.get(restartPath) || 0, 0, "an unknown restart permission must not send POST");
      authRefreshRelease.resolve();
      authResponse = { body: permissionUser(["workers.restart"]) };
      await browser.waitForRequestHandlersIdle({ pathname: "/auth/me", method: "GET" });
      authRefreshRelease = undefined;

      authResponse = { body: permissionUser(["workers.read"]) };
      await browser.reload();
      const denied = await waitForWorkerAction(browser, "Restart worker", "Worker One", (value) => value.disabled && value.reason.length > 0);
      assert.match(denied.reason, /do not have permission to restart workers/i);
      browser.clearRequestCounts(restartPath);
      await clickWorkerAction(browser, "Restart worker");
      await waitForAnimationFrames(browser);
      assert.equal(await workerRestartDialogCount(browser), 0, "a visible denied restart must not open a confirmation");
      assert.equal(browser.requests.get(restartPath) || 0, 0, "a visible denied restart must not send POST");

      const permissionRestoredRows = workerPilotRows.map((row, index) => index === 0
        ? { ...row, service_name: "Worker One Permission Restored" }
        : row);
      workersResponse = { body: permissionRestoredRows };
      authResponse = { body: permittedUser };
      browser.clearRequestCounts("/workers");
      await browser.reload();
      await browser.waitForResponseCount("/workers", 1);
      await waitForWorkerRestartReady(browser, "Worker One Permission Restored");

      const trigger = await waitForWorkerAction(
        browser,
        "Restart worker",
        "Worker One Permission Restored",
        (value) => value.disabled === false && value.dataState === "closed",
      );
      assert.equal(trigger.targetCount, 1, "the allowed Worker row must expose exactly one restart trigger");
      assert.equal(trigger.tagName, "BUTTON", "the Radix trigger ref must target the actual Button element");
      assert.equal(trigger.interactiveButtonCount, 1, "the Worker restart trigger must expose exactly one button role");
      assert.equal(trigger.nestedButtonCount, 0, "the Worker restart trigger must not nest an interactive button");
      assert.equal(trigger.ariaHaspopup, "dialog", "the actual Button must receive Radix aria-haspopup");
      assert.equal(trigger.ariaExpanded, "false", "the closed actual Button must receive Radix aria-expanded");
      const inapplicableTriggerCount = await workerRestartTriggerCount(browser, "Encoder One");
      assert.equal(inapplicableTriggerCount, 0, "a non-Worker row must not render the restart trigger");

      browser.clearRequestCounts(restartPath);
      await clickWorkerAction(browser, "Restart worker", "Worker One Permission Restored");
      const pointerDialog = await waitForWorkerRestartDialog(browser, "Worker One Permission Restored");
      assertWorkerRestartSingleOpenEvidence(trigger, pointerDialog, "pointer");
      assert.ok(pointerDialog.title.length > 0, "the open Worker restart confirmation must expose its title");
      assert.ok(pointerDialog.description.length > 0, "the open Worker restart confirmation must expose its description");
      assert.equal(pointerDialog.activeInside, true, "pointer activation must move focus into the consequence dialog");
      assert.equal(pointerDialog.triggerDataState, "open", "the actual Button must receive Radix's open data-state");
      assert.equal(pointerDialog.triggerAriaExpanded, "true", "the actual Button must receive Radix's open aria-expanded state");
      assert.equal(pointerDialog.restartNotice, "", "opening the confirmation must not manufacture an outcome notice");
      assert.equal(browser.requests.get(restartPath) || 0, 0, "opening by pointer must not send POST before confirmation");
      await browser.pressNativeKey("Escape");
      await waitForWorkerRestartDialogClosed(browser);
      await waitForWorkerRestartTriggerFocus(browser, "Worker One Permission Restored", "Escape");

      await tabToWorkerAction(browser, "Restart worker", "Worker One Permission Restored");
      const enterTrigger = await waitForWorkerAction(
        browser,
        "Restart worker",
        "Worker One Permission Restored",
        (value) => value.dataState === "closed",
      );
      await browser.pressNativeKey("Enter");
      const enterDialog = await waitForWorkerRestartDialog(browser, "Worker One Permission Restored");
      assertWorkerRestartSingleOpenEvidence(enterTrigger, enterDialog, "Enter");
      assert.equal(enterDialog.activeInside, true, "Enter activation must move focus into the consequence dialog");
      assert.equal(browser.requests.get(restartPath) || 0, 0, "opening by Enter must not send POST before confirmation");
      await browser.pressNativeKey("Escape");
      await waitForWorkerRestartDialogClosed(browser);
      await waitForWorkerRestartTriggerFocus(browser, "Worker One Permission Restored", "Enter then Escape");

      const spaceTrigger = await waitForWorkerAction(
        browser,
        "Restart worker",
        "Worker One Permission Restored",
        (value) => value.dataState === "closed",
      );
      await browser.pressNativeKey("Space");
      const spaceDialog = await waitForWorkerRestartDialog(browser, "Worker One Permission Restored");
      assertWorkerRestartSingleOpenEvidence(spaceTrigger, spaceDialog, "Space");
      assert.equal(spaceDialog.activeInside, true, "Space activation must move focus into the consequence dialog");
      assert.equal(browser.requests.get(restartPath) || 0, 0, "opening by Space must not send POST before confirmation");
      await browser.clickSelector('[data-slot="alert-dialog-cancel"]');
      await waitForWorkerRestartDialogClosed(browser);
      await waitForWorkerRestartTriggerFocus(browser, "Worker One Permission Restored", "Cancel");

      await waitForWorkerRestartReady(browser);
      await clickWorkerAction(browser, "Restart worker");
      await waitForWorkerRestartDialog(browser);
      await browser.waitForRequestHandlersIdle({ pathname: "/auth/me", method: "GET" });
      const revokedAuthResponseCount = (browser.responses.get("/auth/me") || 0) + 1;
      authResponse = { body: permissionUser(["workers.read"]) };
      await browser.waitForResponseCount("/auth/me", revokedAuthResponseCount, 20_000);
      const revoked = await waitForWorkerAction(browser, "Restart worker", "Worker One", (value) => value.disabled && value.reason.length > 0);
      assert.match(revoked.reason, /do not have permission to restart workers/i);
      workerRestartMethods = [];
      browser.clearRequestCounts(restartPath);
      browser.clearRequestCounts("/workers");
      await browser.clickSelector('[data-confirm-action]');
      await browser.waitFor(
        "document.body.textContent || ''",
        (value: string) => value.includes("The action cannot be sent because the latest permissions or state could not be verified"),
        "a submit-time permission revoke was not blocked",
      );
      assert.equal(await workerRestartDialogCount(browser), 1, "submit-time permission revalidation must remain in the existing dialog");
      assert.equal(browser.requests.get(restartPath) || 0, 0, "a submit-time permission revoke must not send POST");
      assert.deepEqual(workerRestartMethods, []);
      await browser.pressNativeKey("Escape");
      await waitForWorkerRestartDialogClosed(browser);
      await browser.waitForRequestHandlersIdle({ pathname: "/auth/me", method: "GET" });
      const restoredAuthResponseCount = (browser.responses.get("/auth/me") || 0) + 1;
      authResponse = { body: permittedUser };
      await browser.waitForResponseCount("/auth/me", restoredAuthResponseCount, 20_000);
      workersResponse = { body: workerPilotRows };
      browser.clearRequestCounts("/workers");
      await browser.reload();
      await browser.waitForResponseCount("/workers", 1);
      await waitForWorkerRestartReady(browser);

      await clickWorkerAction(browser, "Restart worker");
      await waitForWorkerRestartDialog(browser);
      browser.clearRequestCounts("/workers");
      await browser.evaluate("window.dispatchEvent(new Event('focus')); true");
      await browser.waitForResponseCount("/workers", 1, 20_000);
      await browser.waitForRequestHandlersIdle({ pathname: "/workers", method: "GET" });
      workersResponse = { body: [] };
      workerRestartMethods = [];
      browser.clearRequestCounts(restartPath);
      browser.clearRequestCounts("/workers");
      await browser.clickSelector('[data-confirm-action]');
      await browser.waitForRequestCount("/workers", 1);
      await browser.waitFor(
        "document.body.textContent || ''",
        (value: string) => value.includes("The target changed, so the action was not sent"),
        "a removed target was not blocked at submit-time revalidation",
      );
      assert.equal(browser.requests.get(restartPath) || 0, 0, "a removed target must not send POST");
      assert.deepEqual(workerRestartMethods, []);
      workersResponse = { body: workerPilotRows };
      await browser.pressNativeKey("Escape");
      await waitForWorkerRestartDialogClosed(browser);
      browser.clearRequestCounts("/workers");
      await browser.reload();
      await browser.waitForResponseCount("/workers", 1);
      await waitForWorkerRestartReady(browser);

      await clickWorkerAction(browser, "Restart worker");
      await waitForWorkerRestartDialog(browser);
      workersResponse = { body: workerPilotRows.map((row, index) => index === 0 ? { ...row, service_type: "encoder_recorder" } : row) };
      workerRestartMethods = [];
      browser.clearRequestCounts(restartPath);
      browser.clearRequestCounts("/workers");
      await browser.clickSelector('[data-confirm-action]');
      await browser.waitForRequestCount("/workers", 1);
      await browser.waitFor(
        "document.body.textContent || ''",
        (value: string) => value.includes("The target changed, so the action was not sent"),
        "a changed service_type was not blocked at submit-time revalidation",
      );
      assert.equal(browser.requests.get(restartPath) || 0, 0, "a changed service_type must not send POST");
      assert.deepEqual(workerRestartMethods, []);
      workersResponse = { body: workerPilotRows };
      await browser.pressNativeKey("Escape");
      await waitForWorkerRestartDialogClosed(browser);
      browser.clearRequestCounts("/workers");
      await browser.reload();
      await browser.waitForResponseCount("/workers", 1);
      await waitForWorkerRestartReady(browser);

      restartRelease = deferred();
      restartResponse = {
        status: 202,
        body: { status: "accepted" },
        waitUntil: restartRelease.promise,
        requiredResponse: true,
      };
      workerRestartMethods = [];
      browser.clearRequestCounts(restartPath);
      await clickWorkerAction(browser, "Restart worker");
      await waitForWorkerRestartDialog(browser);
      await browser.clickSelector('[data-confirm-action]');
      await browser.waitForRequestCount(restartPath, 1);
      const pendingTrigger = await waitForWorkerAction(browser, "Restart worker", "Worker One", (value) => value.disabled);
      assert.equal(pendingTrigger.disabled, true, "the same Worker restart trigger must be unavailable while confirmation is pending");
      assert.equal(await workerRestartDialogCount(browser), 1, "pending duplicate activation must not create an additional dialog");
      await browser.clickSelector('[data-confirm-action]');
      await browser.pressNativeKey("Enter");
      await browser.pressNativeKey("Enter");
      await waitForAnimationFrames(browser);
      assert.equal(browser.requests.get(restartPath), 1, "double click and Enter repeat must share one latched POST");
      assert.deepEqual(workerRestartMethods, ["POST"]);
      const independentWorker = await waitForWorkerAction(browser, "Restart worker", "Future Worker", (value) => value.count >= 1);
      assert.equal(independentWorker.disabled, false, "a different worker must remain independently evaluated while the first worker is pending");
      restartRelease.resolve();
      await browser.waitForResponseCount(restartPath, 1);
      await browser.waitFor(
        "document.body.textContent || ''",
        (value: string) => value.includes("The worker restart request was accepted."),
        "restart success notice did not render",
      );
      await waitForWorkerRestartDialogClosed(browser);
      await waitForWorkerRestartTriggerFocus(browser, "Worker One", "completion");

      for (const failure of [
        {
          name: "403",
          response: { status: 403, body: { code: "permission_denied", message: "RAW-RESTART-403-MARKER" }, requiredResponse: true },
          publicText: "You do not have permission to perform this action.",
          expectedWorkerGets: 1,
        },
        {
          name: "409",
          response: { status: 409, body: { code: "worker_busy", message: "RAW-RESTART-409-MARKER" }, requiredResponse: true },
          publicText: "The resource has changed. Review the latest state.",
          expectedWorkerGets: 2,
        },
        {
          name: "outcome_unknown",
          response: { status: 503, body: { code: "worker_unavailable", message: "RAW-RESTART-503-MARKER" }, requiredResponse: true },
          publicText: "The result could not be confirmed. Do not resend the action",
          expectedWorkerGets: 1,
        },
      ] as const) {
        restartResponse = failure.response;
        workerRestartMethods = [];
        browser.clearRequestCounts(restartPath);
        browser.clearRequestCounts("/workers");
        await waitForWorkerRestartReady(browser);
        await clickWorkerAction(browser, "Restart worker");
        await waitForWorkerRestartDialog(browser);
        await browser.clickSelector('[data-confirm-action]');
        await browser.waitForResponseCount(restartPath, 1);
        await browser.waitForRequestCount("/workers", failure.expectedWorkerGets);
        await browser.waitFor(
          "document.body.textContent || ''",
          (value: string) => value.includes(failure.publicText),
          `${failure.name} did not reach its safe public state`,
        );
        const renderedOutcome = await browser.evaluate<string>("document.body.textContent || ''");
        assert.equal(renderedOutcome.includes(`RAW-RESTART-${failure.name === "outcome_unknown" ? "503" : failure.name}-MARKER`), false);
        if (failure.name === "outcome_unknown") {
          assert.equal(renderedOutcome.includes("The worker restart request was accepted."), false, "outcome_unknown must not claim success");
          assert.equal(renderedOutcome.includes("The worker restart request failed."), false, "outcome_unknown must not claim failure");
        }
        const outcomeFocus = await waitForWorkerRestartOutcomeFocus(
          browser,
          "Worker One",
          failure.publicText,
          failure.name,
        );
        assert.equal(outcomeFocus.dialogCount, 1, `${failure.name} must keep exactly one dialog open before Escape`);
        assert.equal(outcomeFocus.activeExists, true, `${failure.name} must retain an active element`);
        assert.equal(outcomeFocus.activeInside, true, `${failure.name} focus escaped the active dialog`);
        assert.equal(outcomeFocus.activeIsBody, false, `${failure.name} moved focus to body`);
        assert.equal(outcomeFocus.activeIsTrigger, false, `${failure.name} moved focus to the background restart trigger`);
        assert.equal(outcomeFocus.activeHiddenOrInert, false, `${failure.name} moved focus to a hidden or inert element`);
        assert.equal(outcomeFocus.activeVisible, true, `${failure.name} active element is not visibly focusable`);
        assert.equal(outcomeFocus.safeOutcomeTextVisible, true, `${failure.name} safe outcome text is not visible in the dialog`);
        const repeatActionPresent = await browser.evaluate<boolean>(
          "Boolean(document.querySelector('[data-confirm-action]'))",
        );
        if (repeatActionPresent) {
          await browser.clickSelector('[data-confirm-action]');
          await browser.pressNativeKey("Enter");
        } else {
          assert.equal(failure.name, "outcome_unknown", "only outcome_unknown may remove the repeat action");
        }
        await waitForAnimationFrames(browser);
        assert.equal(browser.requests.get(restartPath), 1, `${failure.name} must never be resent automatically or by repeated activation`);
        assert.deepEqual(workerRestartMethods, ["POST"], `${failure.name} request methods`);
        await browser.pressNativeKey("Escape");
        await waitForWorkerRestartDialogClosed(browser);
        await waitForWorkerRestartTriggerFocus(browser, "Worker One", `${failure.name} Escape`);
      }
      assertNoBrowserConsoleErrors(browser.consoleErrorCount);
    } finally {
      authRefreshRelease?.resolve();
      restartRelease?.resolve();
      authResponse = { body: currentUser };
      workersResponse = { body: workerPilotRows };
      nodesResponse = { body: workerPilotRows };
      restartResponse = { status: 202, body: { status: "accepted" } };
      workerRestartMethods = [];
      await browser.setMediaFeatures([]).catch(() => {});
    }
  });

  await t.test("Workers Configuration uses the server ANY permission and safe remote state", async () => {
    const configurationPath = "/nodes/worker-one/configuration";
    let authRefreshRelease: ReturnType<typeof deferred> | undefined;
    let configurationRefreshRelease: ReturnType<typeof deferred> | undefined;
    try {
      await browser.setViewport(1440, 900);
      await browser.setMediaFeatures([]);
      browser.clearConsoleErrors();
      await setStoredDisplay(browser, "en", "light");
      workersResponse = { body: workerPilotRows };
      nodesResponse = { body: workerPilotRows };
      healthResponse = { body: workerPilotRows };

      for (const permissionCase of [
        { name: "service_health.read only", permissions: ["workers.read", "service_health.read"], allowed: true },
        { name: "api_tokens.create only", permissions: ["workers.read", "api_tokens.create"], allowed: true },
        { name: "both", permissions: ["workers.read", "service_health.read", "api_tokens.create"], allowed: true },
        { name: "neither", permissions: ["workers.read"], allowed: false },
        { name: "wildcard", permissions: ["*"], allowed: true },
      ] as const) {
        authResponse = { body: permissionUser([...permissionCase.permissions]) };
        configurationResponse = { body: workerConfiguration("worker-one", `CONFIG-${permissionCase.name}-MARKER`) };
        workerConfigurationMethods = [];
        await browser.navigate(`${server.baseUrl}/admin/workers/`);
        await waitForShell(browser, "Account menu");
        const action = await waitForWorkerAction(
          browser,
          "Show configuration",
          "Worker One",
          (value) => permissionCase.allowed ? value.disabled === false : value.disabled && value.reason.length > 0,
        );
        browser.clearRequestCounts(configurationPath);
        await clickWorkerAction(browser, "Show configuration");
        if (permissionCase.allowed) {
          await browser.waitForResponseCount(configurationPath, 1);
          await browser.waitFor(
            "document.body.textContent || ''",
            (value: string) => value.includes(`CONFIG-${permissionCase.name}-MARKER`),
            `${permissionCase.name} did not render Configuration content`,
          );
          assert.equal(browser.requests.get(configurationPath), 1, `${permissionCase.name} GET count`);
          assert.deepEqual(workerConfigurationMethods, ["GET"]);
        } else {
          await waitForAnimationFrames(browser);
          assert.match(action.reason, /do not have permission to view this configuration/i);
          assert.equal(browser.requests.get(configurationPath) || 0, 0, "workers.read alone must not authorize Configuration GET");
          assert.deepEqual(workerConfigurationMethods, []);
        }
      }

      authResponse = { body: permissionUser(["workers.read", "service_health.read"]) };
      configurationResponse = { body: workerConfiguration("worker-one", "CACHED-CONFIGURATION-MARKER") };
      await browser.navigate(`${server.baseUrl}/admin/workers/`);
      await waitForShell(browser, "Account menu");
      await waitForWorkerAction(browser, "Show configuration", "Worker One", (value) => value.disabled === false);

      await browser.waitForRequestHandlersIdle({ pathname: "/auth/me", method: "GET" });
      const refreshingAuthRequestCount = (browser.requests.get("/auth/me") || 0) + 1;
      authRefreshRelease = deferred();
      authResponse = {
        body: permissionUser(["workers.read", "service_health.read"]),
        waitUntil: authRefreshRelease.promise,
      };
      await browser.waitForRequestCount("/auth/me", refreshingAuthRequestCount, 20_000);
      await waitForWorkerAction(browser, "Show configuration", "Worker One", (value) => value.count >= 1);
      browser.clearRequestCounts(configurationPath);
      await clickWorkerAction(browser, "Show configuration");
      const unknown = await waitForWorkerAction(browser, "Show configuration", "Worker One", (value) => value.disabled && value.reason.length > 0);
      assert.match(unknown.reason, /permission could not be verified/i);
      assert.equal(browser.requests.get(configurationPath) || 0, 0, "a refreshing permission snapshot must suppress Configuration GET");
      authRefreshRelease.resolve();
      authResponse = { body: permissionUser(["workers.read", "service_health.read"]) };
      await browser.waitForRequestHandlersIdle({ pathname: "/auth/me", method: "GET" });
      await browser.reload();
      await waitForShell(browser, "Account menu");
      await waitForWorkerAction(browser, "Show configuration", "Worker One", (value) => value.disabled === false);

      workerConfigurationMethods = [];
      browser.clearRequestCounts(configurationPath);
      await clickWorkerAction(browser, "Show configuration");
      await browser.waitForResponseCount(configurationPath, 1);
      await browser.waitFor(
        "document.body.textContent || ''",
        (value: string) => value.includes("CACHED-CONFIGURATION-MARKER")
          && value.includes("Node Agent API URL")
          && value.includes("Auto Configure command")
          && value.includes("systemd unit"),
        "successful Configuration content was incomplete",
      );
      assert.deepEqual(workerConfigurationMethods, ["GET"]);

      configurationRefreshRelease = deferred();
      configurationResponse = {
        status: 503,
        body: { code: "configuration_unavailable", message: "RAW-CONFIGURATION-REFRESH-MARKER" },
        waitUntil: configurationRefreshRelease.promise,
      };
      workerConfigurationMethods = [];
      browser.clearRequestCounts(configurationPath);
      await clickButtonWithText(browser, "Reload configuration");
      await browser.waitForRequestCount(configurationPath, 1);
      await browser.waitFor(
        "document.body.textContent || ''",
        (value: string) => value.includes("CACHED-CONFIGURATION-MARKER") && value.includes("Refreshing configuration"),
        "same-target pending refresh hid the cached Configuration",
      );
      configurationRefreshRelease.resolve();
      await browser.waitForResponseCount(configurationPath, 1);
      await browser.waitFor(
        "document.body.textContent || ''",
        (value: string) => value.includes("CACHED-CONFIGURATION-MARKER") && value.includes("The refresh failed"),
        "same-target refresh failure did not preserve stale Configuration",
      );
      assert.deepEqual(workerConfigurationMethods, ["GET"]);
      assert.equal(await browserContainsMarker(browser, "RAW-CONFIGURATION-REFRESH-MARKER"), false);

      configurationResponse = {
        status: 500,
        body: { code: "get_node_failed", message: "RAW-CONFIGURATION-BLOCKING-MARKER" },
      };
      workerConfigurationMethods = [];
      await browser.navigate(`${server.baseUrl}/admin/workers/`);
      await waitForShell(browser, "Account menu");
      await waitForWorkerAction(browser, "Show configuration", "Worker One", (value) => value.disabled === false);
      browser.clearRequestCounts(configurationPath);
      await clickWorkerAction(browser, "Show configuration");
      await browser.waitForResponseCount(configurationPath, 1);
      await browser.waitFor(
        "document.body.textContent || ''",
        (value: string) => value.includes("The data could not be loaded.") && value.includes("The service is temporarily unavailable."),
        "a no-data Configuration error did not render as a safe blocking state",
      );
      assert.equal(await browserContainsMarker(browser, "RAW-CONFIGURATION-BLOCKING-MARKER"), false, "raw error marker leaked into DOM or accessibility attributes");
      assert.deepEqual(workerConfigurationMethods, ["GET"]);

      configurationResponse = { body: workerConfiguration("worker-one", "STATUS-CONFIGURATION-MARKER") };
      authResponse = { body: currentUser };
      healthResponse = { body: [
        ...healthyRows.filter((row) => row.service_type === "worker"),
        { id: "worker-malformed", service_type: "worker", service_name: "Malformed Status Worker", status: "future_connectivity", health_status: { known: true, labelKey: "statusNodeHealthy" } },
      ] };
      await browser.navigate(`${server.baseUrl}/admin/workers/`);
      await waitForShell(browser, "Account menu");
      const healthyStatus = await waitForWorkerStatus(browser, "Worker One", (value) => value.text.includes("Healthy"));
      assert.deepEqual(
        { known: healthyStatus.known, tone: healthyStatus.tone, icon: healthyStatus.icon },
        { known: "true", tone: "success", icon: "heart-pulse" },
      );
      const unknownStatus = await waitForWorkerStatus(browser, "Future Worker", (value) => value.text.includes("Unknown status"));
      assert.deepEqual(
        { known: unknownStatus.known, tone: unknownStatus.tone, icon: unknownStatus.icon },
        { known: "false", tone: "unknown", icon: "circle-help" },
      );
      assert.equal(unknownStatus.rowText.includes("future_online_v2"), false);
      assert.equal(unknownStatus.rowText.includes("future_healthy_v2"), false);
      const assignedStatus = await waitForWorkerStatus(browser, "Assigned Worker", (value) => value.text.includes("Assigned"));
      assert.equal(assignedStatus.text.includes("Healthy"), false, "assignment must not be presented as health");
      assert.equal(assignedStatus.tone, "info");
      const formerAlias = await waitForWorkerStatus(browser, "Former Alias Worker", (value) => value.text.includes("Unknown status"));
      assert.equal(formerAlias.rowText.includes("ok"), false, "removed node-health alias must remain unknown and hidden");
      const restoredCanonical = await waitForWorkerStatus(browser, "Degraded Worker", (value) => value.text.includes("Degraded"));
      assert.deepEqual(
        { known: restoredCanonical.known, tone: restoredCanonical.tone, icon: restoredCanonical.icon },
        { known: "true", tone: "warning", icon: "triangle-alert" },
      );
      const malformedStatus = await waitForWorkerStatus(browser, "Malformed Status Worker", (value) => value.text.includes("Unknown status"));
      assert.equal(malformedStatus.text.includes("Healthy"), false, "a partial presentation-like object must contribute no positive status");
      assert.equal(
        await browser.evaluate("[...document.querySelectorAll('section')].some((section) => section.textContent?.includes('Online nodes') && section.textContent?.includes('1/6'))"),
        true,
        "unknown, removed alias, malformed, degraded and assigned rows must be excluded from the healthy numerator",
      );

      await browser.setMediaFeatures([{ name: "forced-colors", value: "active" }]);
      const forcedUnknown = await waitForWorkerStatus(browser, "Future Worker", (value) => value.text.includes("Unknown status"));
      assert.deepEqual(
        { known: forcedUnknown.known, tone: forcedUnknown.tone, icon: forcedUnknown.icon },
        { known: "false", tone: "unknown", icon: "circle-help" },
        "forced colors must preserve text, icon and semantic tone",
      );

      await browser.setMediaFeatures([{ name: "prefers-reduced-motion", value: "reduce" }]);
      await browser.evaluate("document.querySelector('button[aria-label=\"Show configuration\"]')?.focus(); true");
      const reduced = await waitForWorkerStatus(browser, "Future Worker", (value) => value.text.includes("Unknown status"));
      assert.equal(reduced.transitionDuration, "0s");
      assert.equal(await browser.evaluate("document.activeElement?.getAttribute('aria-label')"), "Show configuration");

      await setStoredDisplay(browser, "ja", "light");
      await browser.reload();
      await waitForShell(browser, "アカウントメニュー");
      await waitForWorkerAction(browser, "Configuration を表示", "Worker One", (value) => value.disabled === false);
      const japaneseUnknown = await waitForWorkerStatus(browser, "Future Worker", (value) => value.text.includes("不明な状態"));
      assert.equal(japaneseUnknown.rowText.includes("future_online_v2"), false);
      const japaneseAssigned = await waitForWorkerStatus(browser, "Assigned Worker", (value) => value.text.includes("割り当て済み"));
      assert.equal(japaneseAssigned.text.includes("正常"), false);
      assertNoBrowserConsoleErrors(browser.consoleErrorCount);
    } finally {
      authRefreshRelease?.resolve();
      configurationRefreshRelease?.resolve();
      authResponse = { body: currentUser };
      healthResponse = { body: healthyRows };
      workersResponse = { body: workerPilotRows };
      nodesResponse = { body: workerPilotRows };
      configurationResponse = { body: workerConfiguration("worker-one", "BROWSER-CONFIG-MARKER") };
      workerConfigurationMethods = [];
      await browser.setMediaFeatures([]).catch(() => {});
    }
  });

  await t.test("desktop/mobile navigation parity, active route, and permission visibility are runtime-enforced", async () => {
    authResponse = { body: currentUser };
    healthResponse = { body: healthyRows };
    versionFixture = { body: currentVersion };
    await setStoredDisplay(browser, "ja", "light");
    const routeMatrix = [
      ["/admin/streams/", "/admin/streams/"],
      ["/admin/streams/detail/", "/admin/streams/"],
      ["/admin/stream/", ""],
      ["/admin/streams-old/", ""],
      ["/admin/streams2/", ""],
      ["/admin/streams?x=1", "/admin/streams/"],
      ["/admin/streams/#fragment", "/admin/streams/"],
      ["/admin/service-health/", "/admin/service-health/"],
      ["/admin/", "/admin/"],
    ] as const;

    await browser.setViewport(1440, 900);
    await browser.navigate(`${server.baseUrl}/admin/streams/`);
    await waitForShell(browser, "アカウントメニュー");
    const serverNavigatedPaths = new Set([
      "/admin/streams/",
      "/admin/streams?x=1",
      "/admin/streams/#fragment",
      "/admin/service-health/",
      "/admin/",
    ]);
    for (const [requestedPath, expectedActive] of routeMatrix) {
      if (serverNavigatedPaths.has(requestedPath)) {
        await browser.navigate(`${server.baseUrl}${requestedPath}`);
        await waitForShell(browser, "アカウントメニュー");
      } else {
        await browser.evaluate(`history.pushState(null, '', ${JSON.stringify(requestedPath)}); true`);
      }
      const expectedBrowserLocation = requestedPath === "/admin/streams?x=1" ? "/admin/streams/?x=1" : requestedPath;
      await browser.waitFor(
        "location.pathname + location.search + location.hash",
        (value: string) => value === expectedBrowserLocation,
        `browser history did not expose ${requestedPath}`,
      );
      await waitForAnimationFrames(browser);
      await browser.setViewport(1440, 900);
      await expandNavigationSections(browser);
      const desktop = await browser.evaluate<NavigationSnapshot>(desktopNavigationSnapshotExpression);
      assert.ok(desktop.hrefs.length > 20, `${requestedPath} desktop navigation did not render`);

      await browser.setViewport(390, 844);
      await browser.clickSelector('button[aria-label="ナビゲーションを開く"]');
      await browser.waitFor("Boolean(document.querySelector('.mobile-navigation-sheet'))", Boolean, `mobile navigation did not open for ${requestedPath}`);
      const mobile = await browser.evaluate<NavigationSnapshot>(mobileNavigationSnapshotExpression);
      assertNavigationBoundaryOutcome({ requestedPath, expectedActive, desktop, mobile });
      await browser.pressKey("Escape");
      await browser.waitFor("!document.querySelector('.mobile-navigation-sheet')", Boolean, `mobile navigation did not close for ${requestedPath}`);
    }

    authResponse = {
      body: {
        user: { id: "limited-user", username: "limited-operator", roles: [] },
        permissions: ["streams.read"],
      },
    };
    await browser.setViewport(1440, 900);
    await browser.navigate(`${server.baseUrl}/admin/streams/`);
    await waitForShell(browser, "アカウントメニュー");
    const limited = await browser.evaluate<NavigationSnapshot>(desktopNavigationSnapshotExpression);
    assert.deepEqual(limited.hrefs, ["/admin/", "/admin/streams/"]);
    assert.equal(limited.active, "/admin/streams/");
    assert.deepEqual(limited.activeHrefs, ["/admin/streams/"]);

    await browser.setViewport(390, 844);
    await browser.clickSelector('button[aria-label="ナビゲーションを開く"]');
    await browser.waitFor("Boolean(document.querySelector('.mobile-navigation-sheet'))", Boolean, "limited mobile navigation did not open");
    const limitedMobile = await browser.evaluate<NavigationSnapshot>(mobileNavigationSnapshotExpression);
    assertNavigationBoundaryOutcome({
      requestedPath: "/admin/streams/ (limited permissions)",
      expectedActive: "/admin/streams/",
      desktop: limited,
      mobile: limitedMobile,
    });
    assert.equal(await browser.evaluate("Boolean(document.querySelector('a[href=\"/admin/streams/#create-stream\"]'))"), false);
    assert.equal(await browser.evaluate("Boolean(document.querySelector('a[href=\"/admin/users/\"]'))"), false);
    await browser.pressKey("Escape");
    await browser.waitFor("!document.querySelector('.mobile-navigation-sheet')", Boolean, "limited mobile navigation did not close");
    authResponse = { body: currentUser };
  });

  await t.test("same-route and cross-route mobile create release the navigation focus owner", async () => {
    authResponse = { body: currentUser };
    await browser.setMediaFeatures([]);
    await browser.setViewport(390, 844);
    healthResponse = { body: healthyRows };
    versionFixture = { body: currentVersion };
    await setStoredDisplay(browser, "ja", "light");
    await browser.navigate(`${server.baseUrl}/admin/streams/`);
    await waitForShell(browser, "ナビゲーションを開く");
    await browser.clickSelector('button[aria-label="ナビゲーションを開く"]');
    await browser.waitFor("Boolean(document.querySelector('.mobile-navigation-sheet'))", Boolean, "mobile navigation did not open");
    const normalMotion = await sheetMotion(browser);
    assert.notEqual(normalMotion.content.animationName, "none");
    assert.ok(seconds(normalMotion.content.animationDuration) > 0);
    await waitForSheetSettled(browser);

    await browser.clickSelector('.mobile-navigation-sheet a[href="/admin/streams/#create-stream"]');
    const sameRoute = await browser.waitFor(
      createSnapshotExpression,
      (value: CreateSnapshot) => value.dialogCount === 1 && value.createOpen && !value.navigationOpen && value.focusInsideCreate,
      "same-route create retained two focus owners",
    );
    assertCreateOutcome(sameRoute);
    assert.throws(
      () => assertCreateOutcome({ ...sameRoute, dialogCount: 2, navigationOpen: true }),
      /exactly one dialog/,
      "negative fixture: an unclosed navigation Sheet must fail",
    );
    await closeCreateAndAssertFocusReturn(browser, "ナビゲーションを開く");

    await browser.navigate(`${server.baseUrl}/admin/`);
    await waitForShell(browser, "ナビゲーションを開く");
    await browser.clickSelector('button[aria-label="ナビゲーションを開く"]');
    await browser.waitFor("Boolean(document.querySelector('.mobile-navigation-sheet'))", Boolean, "cross-route navigation did not open");
    await waitForSheetSettled(browser);
    await browser.clickSelector('.mobile-navigation-sheet a[href="/admin/streams/#create-stream"]');
    const crossRoute = await browser.waitFor(
      createSnapshotExpression,
      (value: CreateSnapshot) => value.dialogCount === 1 && value.createOpen && !value.navigationOpen && value.focusInsideCreate && value.url.includes("/admin/streams/#create-stream"),
      "cross-route create navigation did not settle on one dialog",
      10_000,
    );
    assertCreateOutcome(crossRoute);
    await closeCreateAndAssertFocusReturn(browser, "ナビゲーションを開く");

    await browser.setViewport(1440, 900);
    await browser.navigate(`${server.baseUrl}/admin/streams/`);
    await waitForShell(browser, "アカウントメニュー");
    await browser.clickSelector('header a[href="/admin/streams/#create-stream"]');
    const desktop = await browser.waitFor(
      createSnapshotExpression,
      (value: CreateSnapshot) => value.dialogCount === 1 && value.createOpen && value.focusInsideCreate,
      "desktop create behavior regressed",
    );
    assert.equal(desktop.navigationOpen, false);
    assertCreateOutcome(desktop);
    await browser.pressKey("Escape");
    await browser.waitFor("document.querySelectorAll('[role=dialog]').length", (value: number) => value === 0, "desktop create did not close");
  });

  await t.test("reduced motion removes Sheet animation while preserving close and focus", async () => {
    authResponse = { body: currentUser };
    await browser.setViewport(390, 844);
    await browser.setMediaFeatures([{ name: "prefers-reduced-motion", value: "reduce" }]);
    await browser.navigate(`${server.baseUrl}/admin/streams/`);
    await waitForShell(browser, "ナビゲーションを開く");
    await browser.clickSelector('button[aria-label="ナビゲーションを開く"]');
    await browser.waitFor("Boolean(document.querySelector('.mobile-navigation-sheet'))", Boolean, "reduced-motion navigation did not open");
    const reducedMotion = await sheetMotion(browser);
    for (const layer of [reducedMotion.content, reducedMotion.overlay]) {
      assert.equal(layer.animationName, "none");
      assert.equal(seconds(layer.animationDuration), 0);
      assert.equal(seconds(layer.transitionDuration), 0);
    }
    await browser.pressKey("Escape");
    await browser.waitFor("!document.querySelector('.mobile-navigation-sheet')", Boolean, "reduced-motion navigation did not close");
    await browser.waitFor(
      "document.activeElement?.getAttribute('aria-label')",
      (value: string | null) => value === "ナビゲーションを開く",
      "reduced-motion close did not return focus",
    );
    await browser.setMediaFeatures([]);
  });

  await t.test("locale and theme controls preserve route/session and expose translated accessible names", async () => {
    authResponse = { body: currentUser };
    await browser.setViewport(390, 844);
    await setStoredDisplay(browser, "ja", "light");
    await browser.navigate(`${server.baseUrl}/admin/streams/`);
    await waitForShell(browser, "ナビゲーションを開く");
    await browser.clickSelector('button[aria-label="ナビゲーションを開く"]');
    await browser.waitFor("Boolean(document.querySelector('.mobile-navigation-sheet'))", Boolean, "mobile navigation did not open for locale test");
    await waitForSheetSettled(browser);
    browser.clearRequestCounts();
    await browser.clickSelector('[role="combobox"][aria-label="言語"]');
    const englishOption = await browser.waitFor(
      `(() => { const option = [...document.querySelectorAll('[role="option"]')].find((element) => element.textContent?.trim() === 'English'); if (!(option instanceof HTMLElement)) return null; const rect = option.getBoundingClientRect(); return { x: rect.left + rect.width / 2, y: rect.top + rect.height / 2 }; })()`,
      (value: { x: number; y: number } | null) => value !== null,
      "English locale option did not open",
    );
    await browser.clickAt(englishOption.x, englishOption.y);
    await browser.waitFor("document.documentElement.lang", (value: string) => value === "en", "locale did not switch to English");
    const englishShell = await browser.evaluate<EnglishShellSnapshot>(`(() => {
      const sheet = document.querySelector('.mobile-navigation-sheet');
      return {
        url: location.pathname + location.hash,
        text: sheet?.textContent || '',
        closeLabel: sheet?.querySelector('button[aria-label="Close navigation"]')?.getAttribute('aria-label') || '',
        navigationLabel: sheet?.querySelector('nav')?.getAttribute('aria-label') || '',
        overflow: Math.max(0, document.documentElement.scrollWidth - document.documentElement.clientWidth),
        triggerLabel: document.querySelector('button[aria-label="Open navigation"]')?.getAttribute('aria-label') || '',
      };
    })()`);
    assert.equal(englishShell.url, "/admin/streams/");
    for (const expected of ["Create stream slot", "1/2 services available", "Up to date"]) {
      assert.match(englishShell.text, new RegExp(escapeRegExp(expected)));
    }
    assert.equal(englishShell.triggerLabel, "Open navigation");
    assert.equal(englishShell.closeLabel, "Close navigation");
    assert.equal(englishShell.navigationLabel, "Admin navigation");
    assert.equal(englishShell.overflow, 0);
    assert.equal(browser.requests.get("/auth/me") || 0, 0, "locale switching must not recreate the session query");

    await browser.waitFor(
      "[...document.querySelectorAll('[role=option]')].every((element) => element.getClientRects().length === 0)",
      Boolean,
      "locale option popup did not release pointer ownership",
    );
    await browser.clickSelector('.mobile-navigation-sheet button[aria-label="Close navigation"]');
    await browser.waitFor("!document.querySelector('.mobile-navigation-sheet')", Boolean, "translated close action did not close navigation");
    await browser.clickSelector('button[aria-label="Account menu"]');
    const accountText = await browser.waitFor(
      `(() => [...document.querySelectorAll('[role="menu"]')].find((element) => element.getClientRects().length)?.textContent || '')()`,
      (value: string) => value.includes("Account settings") && value.includes("Log out"),
      "account menu did not expose English actions",
    );
    assert.match(accountText, /Account settings/);
    assert.match(accountText, /Log out/);
    await browser.pressKey("Escape");

    const routeBeforeTheme = await browser.evaluate<string>("location.pathname + location.hash");
		const mirrorBeforeTheme = await browser.evaluate<string>(`localStorage.getItem('autostream.ui_preference') || ''`);
    await browser.clickSelector('button[aria-label="Theme"]');
    await browser.waitFor("document.documentElement.classList.contains('dark')", Boolean, "theme did not switch to dark");
    assert.equal(await browser.evaluate<string>("location.pathname + location.hash"), routeBeforeTheme);
    assert.equal(browser.requests.get("/auth/me") || 0, 0, "theme switching must not recreate the session query");
		assert.equal(await browser.evaluate<string>(`localStorage.getItem('autostream.ui_preference') || ''`), mirrorBeforeTheme, "authenticated unsaved theme preview must not replace the DB bootstrap mirror");
  });

  await t.test("Account appearance persists 12 themes and 3 modes with DB fallback and save rollback", async () => {
		authResponse = { status: 401, body: { code: "unauthorized" } };
		uiPreferenceMethods = [];
		uiPreferenceBodies = [];
		await browser.navigate(`${server.baseUrl}/login/`);
		await browser.waitFor(`location.pathname === '/login/'`, Boolean, "login route was not retained");
		assert.equal(uiPreferenceMethods.filter((method) => method === "GET").length, 0, "public login must not query authenticated UI preferences");

		authResponse = { body: currentUser };
		uiPreferenceResponse = { body: { theme_id: "autostream", color_mode: "system", revision: 0 } };
		uiPreferenceWriteResponse = { body: { theme_id: "autostream", color_mode: "dark", revision: 1 } };
		await browser.evaluate(`localStorage.removeItem('autostream.ui_preference'); localStorage.setItem('autostream.theme', 'dark'); true`);
		await browser.navigate(`${server.baseUrl}/admin/account/`);
		await browser.waitFor(
			`document.documentElement.dataset.theme + '/' + document.documentElement.dataset.colorMode`,
			(value: string) => value === "autostream/dark",
			"legacy local preference was not retained while its DB CAS migration completed",
		);
		await browser.waitFor(
			`(localStorage.getItem('autostream.ui_preference') || '').includes('"color_mode":"dark"')`,
			Boolean,
			"legacy preference migration did not persist the DB-backed bootstrap mirror",
		);
		assert.equal(uiPreferenceMethods.filter((method) => method === "PUT").length, 1, "legacy preference migration must send exactly one CAS PUT");
		assert.deepEqual(uiPreferenceBodies.at(-1), { theme_id: "autostream", color_mode: "dark", expected_revision: 0 });

		uiPreferenceResponse = { body: { theme_id: "ocean", color_mode: "dark", revision: 4 }, delayMs: 1_200 };
    uiPreferenceWriteResponse = { body: { theme_id: "violet", color_mode: "light", revision: 5 } };
    uiPreferenceMethods = [];
		uiPreferenceBodies = [];
    await browser.setViewport(1440, 1000);
    await setStoredDisplay(browser, "ja", "light");
		await browser.evaluate(`localStorage.setItem('autostream.ui_preference', JSON.stringify({ theme_id: 'cyan', color_mode: 'light' })); true`);
		await browser.navigate(`${server.baseUrl}/admin/account/`);
		assert.equal(
			await browser.evaluate<string>(`document.documentElement.dataset.theme + '/' + document.documentElement.dataset.colorMode`),
			"cyan/light",
			"external pre-hydration bootstrap did not apply the validated local mirror before the DB response",
		);
		await browser.waitFor(
      `document.documentElement.dataset.theme + '/' + document.documentElement.dataset.colorMode`,
      (value: string) => value === "ocean/dark",
			"DB appearance did not override the pre-hydration mirror",
		);
		uiPreferenceResponse = { body: { theme_id: "ocean", color_mode: "dark", revision: 4 } };
    await browser.clickRoleWithText("tab", "外観");
    const matrix = await browser.waitFor(
      `(() => ({ themes: document.querySelectorAll('[role="radiogroup"][aria-label="配色テーマ"] [role="radio"]').length, modes: document.querySelectorAll('[role="radiogroup"][aria-label="表示モード"] [role="radio"]').length }))()`,
      (value: { themes: number; modes: number }) => value.themes === 12 && value.modes === 3,
      "appearance matrix was not rendered",
    );
    assert.deepEqual(matrix, { themes: 12, modes: 3 });
    await browser.clickSelector('[aria-label="Violetテーマ"]');
    await browser.clickSelector('[aria-label="ライトモード"]');
    await browser.waitFor(
      `document.documentElement.dataset.theme + '/' + document.documentElement.dataset.colorMode + '/' + document.documentElement.classList.contains('dark')`,
      (value: string) => value === "violet/light/false",
      "appearance preview was not immediate",
    );
		assert.match(await browser.evaluate<string>(`localStorage.getItem('autostream.ui_preference') || ''`), /"theme_id":"ocean"/, "unsaved appearance preview replaced the DB bootstrap mirror");
    await browser.clickSelector('[aria-label="表示設定を保存"]');
    await browser.waitFor(
      `document.body.textContent?.includes('表示設定を保存しました。') === true`,
      Boolean,
      "appearance save did not complete",
    );
    assert.equal(uiPreferenceMethods.filter((method) => method === "PUT").length, 1, "appearance save must send exactly one PUT");
		assert.match(await browser.evaluate<string>(`localStorage.getItem('autostream.ui_preference') || ''`), /"theme_id":"violet"/, "saved DB appearance did not refresh the bootstrap mirror");

    uiPreferenceWriteResponse = { status: 409, body: { code: "revision_conflict" } };
    await browser.clickSelector('[aria-label="Oceanテーマ"]');
    await browser.clickSelector('[aria-label="ダークモード"]');
    await browser.clickSelector('[aria-label="表示設定を保存"]');
    await browser.waitFor(
      `document.documentElement.dataset.theme + '/' + document.documentElement.dataset.colorMode`,
      (value: string) => value === "violet/light",
      "failed save did not roll back the displayed preference",
    );
    assert.equal(uiPreferenceMethods.filter((method) => method === "PUT").length, 2, "failed save must not retry automatically");

    uiPreferenceResponse = { body: { theme_id: "violet", color_mode: "light", revision: 5 } };
    await browser.reload();
    await browser.waitFor(
      `document.documentElement.dataset.theme + '/' + document.documentElement.dataset.colorMode`,
      (value: string) => value === "violet/light",
      "saved DB appearance did not persist across reload",
    );
    const putsBeforeFallback = uiPreferenceMethods.filter((method) => method === "PUT").length;
    uiPreferenceResponse = { body: { theme_id: "future-theme", color_mode: "infrared", revision: 6, fallback: true } };
    await browser.reload();
    await browser.waitFor(
      `document.documentElement.dataset.theme + '/' + document.documentElement.dataset.colorMode`,
      (value: string) => value === "autostream/system",
      "unknown stored appearance did not render the safe fallback",
    );
    assert.equal(uiPreferenceMethods.filter((method) => method === "PUT").length, putsBeforeFallback, "safe fallback must not overwrite DB automatically");

		uiPreferenceResponse = { body: { theme_id: "violet", color_mode: "light", revision: 7 } };
		await setStoredDisplay(browser, "en", "light");
		await browser.reload();
		await browser.clickRoleWithText("tab", "外観");
		await browser.waitFor(`document.querySelector('[aria-label="Violet theme"]') !== null`, Boolean, "translated theme accessible name missing");
		await browser.evaluate(`document.querySelector('[aria-label="Ocean theme"]')?.focus(); true`);
		await browser.pressKey("ArrowRight");
		await browser.waitFor(
			`document.activeElement?.getAttribute('aria-label') + '/' + document.querySelector('[aria-label="Cyan theme"]')?.getAttribute('aria-checked')`,
			(value: string) => value === "Cyan theme/true",
			"theme radiogroup did not implement roving Arrow-key selection",
		);
		await browser.evaluate(`document.querySelector('[aria-label="System mode"]')?.focus(); true`);
		await browser.pressKey("End");
		await browser.waitFor(
			`document.activeElement?.getAttribute('aria-label') + '/' + document.querySelector('[aria-label="Dark mode"]')?.getAttribute('aria-checked')`,
			(value: string) => value === "Dark mode/true",
			"mode radiogroup did not implement roving Home/End selection",
		);
  });

  await t.test("Stream detail presents visual snapshots and cover actions preserve request-count and applied-state boundaries", async () => {
    const limitedUser = { user: { id: "visual-reader", username: "visual-reader", roles: ["viewer"] }, permissions: ["streams.read"] };
    healthResponse = { body: healthyRows };
    streamsResponse = { body: [controlPlatformStream] };
    controlPlatformVisualResponse = { body: {
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
    } };
    controlPlatformCoverResponse = { body: controlPlatformCoverState(false, 1, false, 1, "idle") };
		controlPlatformCoverMethods = [];
		controlPlatformCoverBodies = [];
    authResponse = { body: limitedUser };
    await setStoredDisplay(browser, "ja", "light");
    await browser.navigate(`${server.baseUrl}/admin/streams/`);
    await browser.waitFor(`document.body.textContent?.includes(${JSON.stringify(controlPlatformStream.name)}) === true`, Boolean, "control-platform stream row missing");
    await browser.clickSelector('button[aria-label="詳細"]');
    await browser.waitFor(`document.querySelector('section[aria-label="配信ビジュアルとVideo Cover"]') !== null`, Boolean, "visual detail panel missing");
    await browser.waitFor(
      `document.body.textContent?.includes('配信ビジュアル見出し') === true && document.body.textContent?.includes('保存済みsnapshotを継続します') === true`,
      Boolean,
      "saved visual snapshot did not finish rendering",
    );
    const visualSnapshot = await browser.evaluate<{ title: boolean; layers: boolean; warning: boolean; showDisabled: boolean }>(`(() => ({
      title: document.body.textContent?.includes('配信ビジュアル見出し') === true,
      layers: document.body.textContent?.includes('Base / Worker scene → Video Cover → Watermark → Encode → tee') === true,
      warning: document.body.textContent?.includes('保存済みsnapshotを継続します') === true,
      showDisabled: document.querySelector('button[aria-label="Video Coverを表示"]')?.hasAttribute('disabled') === true,
    }))()`);
    assert.deepEqual(visualSnapshot, { title: true, layers: true, warning: true, showDisabled: true });
    await scrollSelectorIntoView(browser, 'button[aria-label="Video Coverを表示"]');
    await browser.clickSelector('button[aria-label="Video Coverを表示"]');
    await waitForAnimationFrames(browser);
    assert.equal(controlPlatformCoverMethods.filter((method) => method === "PUT").length, 0, "permission denied UI must send zero cover requests");

    authResponse = { body: currentUser };
		controlPlatformCoverMethods = [];
		controlPlatformCoverBodies = [];
    controlPlatformCoverWriteResponse = { body: controlPlatformCoverState(true, 2, true, 2, "applied") };
		await browser.waitForRequestHandlersIdle({ pathname: "/auth/me", method: "GET" });
		browser.clearRequestCounts("/auth/me");
		await browser.reload();
		await browser.waitForResponseCount("/auth/me", 1, 20_000);
		await browser.waitForRequestHandlersIdle({ pathname: "/auth/me", method: "GET" });
    await browser.waitFor(`document.body.textContent?.includes(${JSON.stringify(controlPlatformStream.name)}) === true`, Boolean, "control-platform stream did not reload");
    await browser.clickSelector('button[aria-label="詳細"]');
    await browser.waitFor(`document.querySelector('button[aria-label="Video Coverを表示"]:not([disabled])') !== null`, Boolean, "show cover action did not become available");
    await browser.waitForRequestHandlersIdle({ pathname: "/auth/me", method: "GET" });
    browser.clearRequestCounts("/auth/me");
    const permissionRelease = deferred();
    authResponse = { body: currentUser, waitUntil: permissionRelease.promise };
    await browser.evaluate("window.dispatchEvent(new Event('focus')); true");
    await browser.waitForRequestCount("/auth/me", 1, 20_000);
    await browser.waitFor(`document.querySelector('button[aria-label="Video Coverを表示"]:disabled') !== null`, Boolean, "show cover action remained enabled while permission was refreshing");
    browser.clearRequestCounts(controlPlatformCoverPath);
    await browser.clickSelector('button[aria-label="Video Coverを表示"]');
    await waitForAnimationFrames(browser);
    assert.equal(controlPlatformCoverMethods.filter((method) => method === "PUT").length, 0, "refreshing permission must send zero cover requests");
    permissionRelease.resolve();
    authResponse = { body: currentUser };
    await browser.waitForResponseCount("/auth/me", 1, 20_000);
    await browser.waitForRequestHandlersIdle({ pathname: "/auth/me", method: "GET" });
    await browser.reload();
    await browser.waitFor(`document.body.textContent?.includes(${JSON.stringify(controlPlatformStream.name)}) === true`, Boolean, "control-platform stream did not reload after permission refresh");
    await browser.clickSelector('button[aria-label="詳細"]');
    await browser.waitFor(`document.querySelector('button[aria-label="Video Coverを表示"]:not([disabled])') !== null`, Boolean, "show cover action did not recover after permission refresh");
    await browser.waitForRequestHandlersIdle({ pathname: controlPlatformCoverPath, method: "GET" });
    await waitForAnimationFrames(browser);
    browser.clearRequestCounts(controlPlatformCoverPath);
		controlPlatformCoverMethods = [];
		controlPlatformCoverBodies = [];
    const showRelease = deferred();
    controlPlatformCoverWriteResponse = { body: controlPlatformCoverState(true, 2, true, 2, "applied"), waitUntil: showRelease.promise };
    await scrollSelectorIntoView(browser, 'button[aria-label="Video Coverを表示"]');
    await browser.clickSelector('button[aria-label="Video Coverを表示"]');
    await browser.waitForRequestCount(controlPlatformCoverPath, 1);
    await browser.clickSelector('button[aria-label="Video Coverを表示"]');
    await waitForAnimationFrames(browser);
    assert.equal(controlPlatformCoverMethods.filter((method) => method === "PUT").length, 1, "duplicate pending show must send zero additional requests");
    showRelease.resolve();
    await browser.waitFor(`document.querySelector('[data-video-cover="applied"]') !== null`, Boolean, "applied cover did not render after authoritative result");

    controlPlatformCoverWriteResponse = { body: controlPlatformCoverState(false, 3, true, 2, "confirming") };
    await scrollSelectorIntoView(browser, 'button[aria-label="Video Coverを非表示"]');
    await browser.clickSelector('button[aria-label="Video Coverを非表示"]');
    await waitForAnimationFrames(browser);
    assert.equal(controlPlatformCoverMethods.filter((method) => method === "PUT").length, 1, "hide trigger must not mutate before confirmation");
    const confirmed = await browser.evaluate<boolean>(`(() => {
      const dialog = document.querySelector('[role="alertdialog"]');
      const action = [...(dialog?.querySelectorAll('button') || [])].find((button) => button.textContent?.trim() === 'Coverを非表示');
      if (!(action instanceof HTMLButtonElement)) return false;
      action.click();
      return true;
    })()`);
    assert.equal(confirmed, true, "hide confirmation action missing");
    await browser.waitForRequestCount(controlPlatformCoverPath, 2);
    await browser.waitFor(`document.body.textContent?.includes('DesiredとAppliedは未一致です') === true`, Boolean, "ambiguous hide falsely appeared applied");
    assert.equal(controlPlatformCoverMethods.filter((method) => method === "PUT").length, 2);

    controlPlatformCoverResponse = { body: controlPlatformCoverState(false, 3, false, 3, "applied") };
    await browser.waitFor(
      `![...document.querySelectorAll('[role="alertdialog"]')].some((element) => element.getClientRects().length > 0)`,
      Boolean,
      "hide confirmation overlay did not finish closing before reconciliation",
    );
    await scrollSelectorIntoView(browser, 'button[aria-label="Coverの最新状態を確認"]');
    await browser.waitFor(`document.querySelector('button[aria-label="Coverの最新状態を確認"]:not([disabled])') !== null`, Boolean, "cover reconciliation remained disabled after the prior mutation settled");
    await browser.clickSelector('button[aria-label="Coverの最新状態を確認"]');
		await browser.waitForRequestCount(controlPlatformCoverPath, 3);
		assert.equal(controlPlatformCoverMethods.filter((method) => method === "PUT").length, 2, "reconciliation must refresh state without resending the mutation");
		assert.equal(controlPlatformCoverMethods.filter((method) => method === "GET").length, 1, "reconciliation must perform one read-only state request");

    controlPlatformCoverWriteResponse = { status: 403, body: { code: "permission_denied" } };
		await browser.waitFor(
			`document.querySelector('button[aria-label="Video Coverを表示"]:not([disabled])') !== null`,
			Boolean,
			"show cover action remained disabled after reconciliation settled",
		);
    await scrollSelectorIntoView(browser, 'button[aria-label="Video Coverを表示"]');
    await browser.clickSelector('button[aria-label="Video Coverを表示"]');
    await browser.waitForRequestCount(controlPlatformCoverPath, 4);
    await waitForAnimationFrames(browser);
    assert.equal(controlPlatformCoverMethods.filter((method) => method === "PUT").length, 3, "backend 403 must receive one request and no automatic resend");
  });

  await t.test("Bundle 7 affected surfaces are responsive at every canonical width", async () => {
    const affectedRoutes = [
      "/admin/",
      "/admin/streams/",
      "/admin/monitoring/",
      "/admin/metrics/",
      "/admin/encoder/",
      "/admin/audit-logs/",
      "/admin/archive/",
      "/admin/workers/",
      "/admin/nodes/",
      "/admin/settings/",
    ] as const;
    const viewports = [
      { width: 390, height: 844 },
      { width: 430, height: 932 },
      { width: 768, height: 1024 },
      { width: 1024, height: 768 },
      { width: 1440, height: 900 },
      { width: 1920, height: 1080 },
    ] as const;
    authResponse = { body: currentUser };
    streamsResponse = { body: [controlPlatformStream] };
    healthResponse = { body: healthyRows };
    workersResponse = { body: workerPilotRows };
    nodesResponse = { body: workerPilotRows };
    await setStoredDisplay(browser, "ja", "light");

    for (const route of affectedRoutes) {
      await browser.navigate(`${server.baseUrl}${route}`);
      await waitForShell(browser, "アカウントメニュー");
      await browser.waitFor(
        `document.querySelector('main')?.getClientRects().length || 0`,
        (value: number) => value > 0,
        `${route}: main content did not render`,
      );
      for (const viewport of viewports) {
        await browser.setViewport(viewport.width, viewport.height);
        await waitForAnimationFrames(browser);
        const snapshot = await browser.evaluate<{
          width: number;
          mainVisible: boolean;
          horizontalOverflow: boolean;
          visibleInteractiveCount: number;
        }>(`(() => {
          const main = document.querySelector('main');
          const visible = (element) => element instanceof HTMLElement && element.getClientRects().length > 0;
          return {
            width: window.innerWidth,
            mainVisible: visible(main),
            horizontalOverflow: document.documentElement.scrollWidth > document.documentElement.clientWidth + 1,
            visibleInteractiveCount: [...document.querySelectorAll('a,button,input,select,textarea')].filter(visible).length,
          };
        })()`);
        assert.equal(snapshot.width, viewport.width, `${route} viewport width`);
        assert.equal(snapshot.mainVisible, true, `${route} at ${viewport.width}px did not retain visible main content`);
        assert.equal(snapshot.horizontalOverflow, false, `${route} at ${viewport.width}px overflowed the document horizontally`);
        assert.ok(snapshot.visibleInteractiveCount > 0, `${route} at ${viewport.width}px exposed no usable controls`);
      }
    }
  });

  await t.test("status focus remains visible in normal and forced-colors modes", async () => {
    authResponse = { body: currentUser };
    await browser.setViewport(1440, 900);
    await browser.setMediaFeatures([]);
    healthResponse = { body: healthyRows };
    versionFixture = { body: currentVersion };
    await setStoredDisplay(browser, "en", "light");
    await browser.reload();
    await waitForStatus(browser, "1/2 services available", "Up to date");
    const normal = await browser.evaluate<FocusSnapshot>(focusSnapshotExpression);
    assert.equal(normal.focused, true);
    assert.notEqual(normal.outlineStyle, "none");
    assert.ok(Number.parseFloat(normal.outlineWidth) >= 2);
    assert.notEqual(normal.boxShadow, "none");

    await browser.setMediaFeatures([{ name: "forced-colors", value: "active" }]);
    const forced = await browser.evaluate<FocusSnapshot>(focusSnapshotExpression);
    assert.equal(forced.focused, true);
    assert.notEqual(forced.outlineStyle, "none");
    assert.ok(Number.parseFloat(forced.outlineWidth) >= 2);
    assert.equal(forced.boxShadow, "none");
    await browser.setMediaFeatures([]);
  });

  await t.test("false-positive guards reject invalid observable outcomes", () => {
    const validLogout = {
      logoutRequestCount: 1,
      pathname: "/login/",
      protectedAdminLandmarkPresent: false,
      accountMenuPresent: false,
      csrfTokenPresent: false,
    };
    assert.throws(
      () => assertLogoutOutcome({ ...validLogout, logoutRequestCount: 2 }),
      /logout mutation request count/,
      "two logout mutations must be rejected",
    );
    assert.throws(
      () => assertLogoutOutcome({ ...validLogout, protectedAdminLandmarkPresent: true }),
      /protected admin landmark/,
      "remaining protected UI must be rejected",
    );

    assert.throws(
      () => assertLoginNavigationOutcome({
        href: "https://attacker.example/admin/",
        expectedOrigin: server.baseUrl,
        expectedPathname: "/admin/",
      }),
      /same-origin/,
      "external login navigation must be rejected",
    );

    const validAuthMeExpiry = {
      accountMenuPresent: false,
      authMeRequestCount: 1,
      authMeResponseStatuses: [401],
      csrfTokenPresent: false,
      expectedOrigin: server.baseUrl,
      expectedReturnURL: expectedAuthMeExpiryReturnURL,
      href: `${server.baseUrl}/login/?redirect_after=%2Fadmin%2Fstreams%2F%3Fview%3Dactive%23preview&reason=session_expired`,
      navigationCount: 1,
      navigationSucceeded: true,
      pathname: "/login/",
      protectedAdminLandmarkPresent: false,
      reason: "session_expired",
      refreshResponseStatuses: [200],
      returnParameterValues: [expectedAuthMeExpiryReturnURL],
    };
    assert.doesNotThrow(() => assertAuthMeExpiryOutcome(validAuthMeExpiry));
    assert.throws(
      () => assertAuthMeExpiryOutcome({ ...validAuthMeExpiry, authMeResponseStatuses: [200], refreshResponseStatuses: [401] }),
      /auth\/me must produce exactly one observed 401 response/,
      "refresh-only 401 must not satisfy the auth-me expiry oracle",
    );
    assert.throws(
      () => assertAuthMeExpiryOutcome({ ...validAuthMeExpiry, returnParameterValues: ["/admin/streams/?view=active"] }),
      /return parameter/,
      "a return URL missing only the hash must be rejected",
    );
    assert.throws(
      () => assertAuthMeExpiryOutcome({ ...validAuthMeExpiry, returnParameterValues: ["/admin/streams/#preview"] }),
      /return parameter/,
      "a return URL missing only the query must be rejected",
    );
    assert.throws(
      () => assertAuthMeExpiryOutcome({ ...validAuthMeExpiry, returnParameterValues: ["https://attacker.example/"] }),
      /return parameter/,
      "an external return URL must be rejected",
    );
    assert.throws(
      () => assertAuthMeExpiryOutcome({ ...validAuthMeExpiry, navigationCount: 2 }),
      /navigation count/,
      "two login navigations must be rejected",
    );
    assert.throws(
      () => assertAuthMeExpiryOutcome({ ...validAuthMeExpiry, protectedAdminLandmarkPresent: true }),
      /protected admin landmark/,
      "protected UI remaining after /auth/me 401 must be rejected",
    );
    assert.throws(
      () => assertAuthMeExpiryOutcome({ ...validAuthMeExpiry, navigationSucceeded: false }),
      /complete successfully/,
      "a failed browser navigation must not be accepted",
    );

    const validNavigation = {
      requestedPath: "/admin/streams-old/",
      expectedActive: "",
      desktop: { hrefs: ["/admin/", "/admin/streams/"], active: "", activeHrefs: [] },
      mobile: { hrefs: ["/admin/", "/admin/streams/"], active: "", activeHrefs: [] },
    };
    assert.throws(
      () => assertNavigationBoundaryOutcome({
        ...validNavigation,
        desktop: { ...validNavigation.desktop, active: "/admin/streams/", activeHrefs: ["/admin/streams/"] },
      }),
      /desktop active route/,
      "streams-old must not be accepted as streams",
    );
    assert.throws(
      () => assertNavigationBoundaryOutcome({
        ...validNavigation,
        mobile: { ...validNavigation.mobile, active: "/admin/streams/", activeHrefs: ["/admin/streams/"] },
      }),
      /mobile active route/,
      "mobile navigation must not use a different matcher",
    );

    const passingSummary: BrowserSuiteSummary = {
      success: true,
      counts: { cancelled: 0, passed: 1, skipped: 0, suites: 0, tests: 1, todo: 0, topLevel: 1 },
    };
    assert.throws(
      () => assertBrowserSuiteExecution(
        { ...passingSummary, counts: { ...passingSummary.counts, passed: 0, tests: 0, topLevel: 0 } },
        [],
        [],
      ),
      /at least one test/,
      "zero-test success must be rejected",
    );
    assert.throws(
      () => assertBrowserSuiteExecution({ ...passingSummary, success: false }, [], []),
      /reported a failure/,
      "browser launch failure must be rejected",
    );
    assert.throws(
      () => assertBrowserSuiteExecution(
        { ...passingSummary, counts: { ...passingSummary.counts, skipped: 1 } },
        [{ name: "required", passed: true, skipped: true, todo: false }],
        ["required"],
      ),
      /must not skip/,
      "skipped browser scenario must be rejected",
    );
    assert.throws(
      () => assertBrowserSuiteExecution(
        passingSummary,
        [{ name: "unrelated scenario", passed: true, skipped: false, todo: false }],
        [authMeExpiryScenarioName],
      ),
      /required browser scenario did not run/,
      "inventory-only registration without execution must be rejected",
    );
    const startReadinessScenarioName = "Streams start-readiness follows streams.start at render and confirm time";
    const requiredStartReadinessResults = [
      { name: startReadinessScenarioName, passed: true, skipped: false, todo: false },
      { name: "another required scenario", passed: true, skipped: false, todo: false },
    ];
    assert.doesNotThrow(() => assertBrowserSuiteExecution(
      passingSummary,
      requiredStartReadinessResults,
      [startReadinessScenarioName],
    ));
    assert.throws(
      () => assertBrowserSuiteExecution(
        passingSummary,
        requiredStartReadinessResults.filter((result) => result.name !== startReadinessScenarioName),
        [startReadinessScenarioName],
      ),
      /required browser scenario did not run/,
      "removing the required start-readiness result must be rejected",
    );
  });

  authResponse = { body: currentUser };
});

type StatusSnapshot = { health: string; update: string };
type CreateSnapshot = {
  dialogCount: number;
  navigationOpen: boolean;
  createOpen: boolean;
  focusInsideCreate: boolean;
  activeTag: string;
  url: string;
};
type MotionSnapshot = { animationName: string; animationDuration: string; transitionDuration: string };
type EnglishShellSnapshot = { url: string; text: string; closeLabel: string; navigationLabel: string; overflow: number; triggerLabel: string };
type FocusSnapshot = { focused: boolean; outlineStyle: string; outlineWidth: string; outlineColor: string; boxShadow: string };
type LogoutBrowserSnapshot = {
  pathname: string;
  protectedAdminLandmarkPresent: boolean;
  accountMenuPresent: boolean;
  csrfTokenPresent: boolean;
};
type SessionExpirySnapshot = {
  href: string;
  pathname: string;
  reason: string | null;
  redirectAfter: string | null;
  csrfTokenPresent: boolean;
  protectedAdminLandmarkPresent: boolean;
};
type AuthMeExpiryBrowserSnapshot = {
  accountMenuPresent: boolean;
  csrfTokenPresent: boolean;
  href: string;
  pathname: string;
  protectedAdminLandmarkPresent: boolean;
  reason: string | null;
  returnParameterValues: string[];
};

const statusSnapshotExpression = `(() => {
  const health = [...document.querySelectorAll('a[href="/admin/service-health/"].semantic-status-focus')].find((element) => element.getClientRects().length > 0);
  const update = [...document.querySelectorAll('[role="status"]')].find((element) => element.getClientRects().length > 0);
  return { health: health?.getAttribute('aria-label') || '', update: update?.textContent?.trim() || '' };
})()`;

const createSnapshotExpression = `(() => {
  const create = document.querySelector('#create-stream')?.closest('[role="dialog"]');
  return {
    dialogCount: document.querySelectorAll('[role="dialog"]').length,
    navigationOpen: Boolean(document.querySelector('.mobile-navigation-sheet')),
    createOpen: Boolean(create),
    focusInsideCreate: Boolean(create?.contains(document.activeElement)),
    activeTag: document.activeElement?.tagName || '',
    url: location.href,
  };
})()`;

const focusSnapshotExpression = `(() => {
  const element = [...document.querySelectorAll('a[href="/admin/service-health/"].semantic-status-focus')].find((candidate) => candidate.getClientRects().length);
  if (!(element instanceof HTMLElement)) throw new Error('visible health status link is missing');
  element.focus();
  const style = getComputedStyle(element);
  return {
    focused: document.activeElement === element,
    outlineStyle: style.outlineStyle,
    outlineWidth: style.outlineWidth,
    outlineColor: style.outlineColor,
    boxShadow: style.boxShadow,
  };
})()`;

const desktopNavigationSnapshotExpression = navigationSnapshotExpression("aside nav");
const mobileNavigationSnapshotExpression = navigationSnapshotExpression(".mobile-navigation-sheet nav");

function navigationSnapshotExpression(selector: string) {
  return `(() => {
  const navigation = document.querySelector(${JSON.stringify(selector)});
  if (!navigation) throw new Error('visible admin navigation is missing');
  const activeHrefs = [...navigation.querySelectorAll('a[aria-current="page"]')].map((link) => link.getAttribute('href'));
  return {
    hrefs: [...navigation.querySelectorAll('a[href^="/admin/"]')].map((link) => link.getAttribute('href')),
    active: activeHrefs[0] || '',
    activeHrefs,
  };
})()`;
}

const logoutSnapshotExpression = `(() => ({
  pathname: location.pathname,
  protectedAdminLandmarkPresent: Boolean(document.querySelector('nav[aria-label="Admin navigation"]')),
  accountMenuPresent: Boolean(document.querySelector('button[aria-label="Account menu"]')),
  csrfTokenPresent: sessionStorage.getItem(${JSON.stringify(csrfStorageKey)}) !== null,
}))()`;

const sessionExpirySnapshotExpression = `(() => {
  const url = new URL(location.href);
  return {
    href: url.href,
    pathname: url.pathname,
    reason: url.searchParams.get('reason'),
    redirectAfter: url.searchParams.get('redirect_after'),
    csrfTokenPresent: sessionStorage.getItem(${JSON.stringify(csrfStorageKey)}) !== null,
    protectedAdminLandmarkPresent: Boolean(document.querySelector('nav[aria-label="Admin navigation"]')),
  };
})()`;

function authMeExpirySnapshotExpression(returnParameterName: string) {
  return `(() => {
    const url = new URL(location.href);
    return {
      accountMenuPresent: Boolean(document.querySelector('button[aria-label="Account menu"]')),
      csrfTokenPresent: sessionStorage.getItem(${JSON.stringify(csrfStorageKey)}) !== null,
      href: url.href,
      pathname: url.pathname,
      protectedAdminLandmarkPresent: Boolean(document.querySelector('nav[aria-label="Admin navigation"]')),
      reason: url.searchParams.get('reason'),
      returnParameterValues: url.searchParams.getAll(${JSON.stringify(returnParameterName)}),
    };
  })()`;
}

async function waitForStartReadinessHandlersIdle(browser: BrowserHarness) {
  await browser.waitForRequestHandlersIdle({ pathname: startReadinessPath, method: "POST" });
  await browser.waitForRequestHandlersIdle({ pathname: "/streams", method: "GET" });
  browser.assertNoFatalError();
}

async function waitForStatus(browser: BrowserHarness, health: string, update: string) {
  return browser.waitFor(
    statusSnapshotExpression,
    (value: StatusSnapshot) => value.health === health && value.update === update,
    `status did not become ${health} / ${update}`,
    15_000,
  );
}

function assertStatusSnapshot(actual: StatusSnapshot, expected: StatusSnapshot) {
  assert.equal(actual.health, expected.health, "health status projection");
  assert.equal(actual.update, expected.update, "update status projection");
}

function assertCreateOutcome(snapshot: CreateSnapshot) {
  assert.equal(snapshot.dialogCount, 1, "create flow must leave exactly one dialog");
  assert.equal(snapshot.navigationOpen, false, "navigation Sheet must release its focus owner");
  assert.equal(snapshot.createOpen, true, "create dialog must be open");
  assert.equal(snapshot.focusInsideCreate, true, "focus must enter the create dialog");
  assert.match(snapshot.activeTag, /^(?:INPUT|BUTTON|TEXTAREA|SELECT)$/);
}

async function closeCreateAndAssertFocusReturn(browser: BrowserHarness, triggerLabel: string) {
  await browser.pressKey("Escape");
  await browser.waitFor("document.querySelectorAll('[role=dialog]').length", (value: number) => value === 0, "Escape did not close create dialog");
  await browser.waitFor(
    "document.activeElement?.getAttribute('aria-label')",
    (value: string | null) => value === triggerLabel,
    "focus did not return to the mobile navigation trigger",
  );
}

async function waitForShell(browser: BrowserHarness, accessibleName: string) {
  await browser.waitFor(
    `Boolean(document.querySelector('button[aria-label=${JSON.stringify(accessibleName)}]'))`,
    Boolean,
    `shell control ${accessibleName} did not render`,
  );
}

async function triggerReconnect(browser: BrowserHarness) {
  await browser.evaluate("window.dispatchEvent(new Event('offline')); window.dispatchEvent(new Event('online')); true");
}

type WorkerActionSnapshot = {
  ariaControls: string | null;
  ariaExpanded: string | null;
  ariaHaspopup: string | null;
  count: number;
  dataState: string | null;
  disabled: boolean;
  interactiveButtonCount: number;
  nestedButtonCount: number;
  reason: string;
  tagName: string;
  targetCount: number;
};

type WorkerRestartDialogSnapshot = {
  activeInside: boolean;
  activeLabel: string;
  description: string;
  dialogCount: number;
  restartNotice: string;
  title: string;
  triggerAriaExpanded: string | null;
  triggerDataState: string | null;
};

type WorkerRestartOutcomeFocusSnapshot = {
  activeExists: boolean;
  activeHiddenOrInert: boolean;
  activeInside: boolean;
  activeIsBody: boolean;
  activeIsTrigger: boolean;
  activeVisible: boolean;
  dialogCount: number;
  safeOutcomeTextVisible: boolean;
};

type WorkerStatusSnapshot = {
  icon: string | null;
  known: string | null;
  rowText: string;
  text: string;
  tone: string | null;
  transitionDuration: string;
};

async function waitForWorkerAction(
  browser: BrowserHarness,
  label: string,
  target: number | string,
  accept: (value: WorkerActionSnapshot) => boolean,
) {
  return browser.waitFor<WorkerActionSnapshot>(
    workerActionSnapshotExpression(label, target),
    (value) => value !== null && accept(value),
    `worker action ${label} for ${target} did not reach the expected state`,
    15_000,
  );
}

async function waitForWorkerRestartReady(browser: BrowserHarness, target = "Worker One") {
  await browser.waitForRequestHandlersIdle({ pathname: "/workers", method: "GET" });
  await waitForWorkerAction(browser, "Restart worker", target, (value) => value.disabled === false);
}

async function clickWorkerAction(browser: BrowserHarness, label: string, target: number | string = "Worker One") {
  const present = await browser.evaluate<boolean>(`(() => {
    const target = ${JSON.stringify(target)};
    const candidates = [...document.querySelectorAll('button[aria-label=${JSON.stringify(label)}]')];
    const button = typeof target === 'number'
      ? candidates[target]
      : candidates.find((candidate) => candidate.closest('tr')?.textContent?.includes(target));
    if (!(button instanceof HTMLButtonElement)) return false;
    button.scrollIntoView({ block: 'center', inline: 'nearest' });
    return true;
  })()`);
  assert.equal(present, true, `worker action ${label} for ${target} was not present`);
  await waitForAnimationFrames(browser);
  const point = await browser.evaluate<{ disabled: boolean; hit: boolean; x: number; y: number } | null>(`(() => {
    const target = ${JSON.stringify(target)};
    const candidates = [...document.querySelectorAll('button[aria-label=${JSON.stringify(label)}]')];
    const button = typeof target === 'number'
      ? candidates[target]
      : candidates.find((candidate) => candidate.closest('tr')?.textContent?.includes(target));
    if (!(button instanceof HTMLButtonElement) || button.getClientRects().length === 0) return null;
    const rect = button.getBoundingClientRect();
    const x = rect.left + rect.width / 2;
    const y = rect.top + rect.height / 2;
    return {
      disabled: button.disabled || button.getAttribute('aria-disabled') === 'true',
      hit: button.contains(document.elementFromPoint(x, y)),
      x,
      y,
    };
  })()`);
  if (!point) assert.fail(`worker action ${label} for ${target} was not present`);
  if (!point.disabled) {
    assert.equal(point.hit, true, `enabled worker action ${label} for ${target} did not own its click point`);
  }
  await browser.clickAt(point.x, point.y);
}

async function clickButtonWithText(browser: BrowserHarness, text: string) {
  const clicked = await browser.evaluate<boolean>(`(() => {
    const button = [...document.querySelectorAll('button')].find((candidate) => candidate.textContent?.trim() === ${JSON.stringify(text)});
    if (!(button instanceof HTMLButtonElement)) return false;
    button.click();
    return true;
  })()`);
  assert.equal(clicked, true, `button ${text} was not present`);
}

function workerActionSnapshotExpression(label: string, target: number | string) {
  return `(() => {
    const target = ${JSON.stringify(target)};
    const buttons = [...document.querySelectorAll('button[aria-label=${JSON.stringify(label)}]')];
    const targetButtons = typeof target === 'number'
      ? (buttons[target] ? [buttons[target]] : [])
      : buttons.filter((candidate) => candidate.closest('tr')?.textContent?.includes(target));
    const button = targetButtons[0];
    if (!(button instanceof HTMLButtonElement)) return null;
    const reasonId = button.getAttribute('aria-describedby');
    return {
      ariaControls: button.getAttribute('aria-controls'),
      ariaExpanded: button.getAttribute('aria-expanded'),
      ariaHaspopup: button.getAttribute('aria-haspopup'),
      count: buttons.length,
      dataState: button.getAttribute('data-state'),
      disabled: button.disabled || button.getAttribute('aria-disabled') === 'true',
      interactiveButtonCount: 1 + button.querySelectorAll('button, [role="button"]').length,
      nestedButtonCount: button.querySelectorAll('button').length,
      reason: reasonId ? document.getElementById(reasonId)?.textContent || '' : '',
      tagName: button.tagName,
      targetCount: targetButtons.length,
    };
  })()`;
}

async function waitForWorkerRestartDialog(browser: BrowserHarness, target = "Worker One") {
  return browser.waitFor<WorkerRestartDialogSnapshot>(
    `(() => {
      const content = document.querySelector('[data-slot="alert-dialog-content"][data-state="open"]');
      const buttons = [...document.querySelectorAll('button[aria-label="Restart worker"]')];
      const trigger = buttons.find((candidate) => candidate.closest('tr')?.textContent?.includes(${JSON.stringify(target)}));
      return {
        activeInside: content instanceof HTMLElement && content.contains(document.activeElement),
        activeLabel: document.activeElement?.getAttribute('aria-label') || '',
        description: content?.querySelector('[data-slot="alert-dialog-description"]')?.textContent?.trim() || '',
        dialogCount: document.querySelectorAll('[data-slot="alert-dialog-content"][data-state="open"]').length,
        restartNotice: [...document.querySelectorAll('[role="status"]')]
          .map((element) => element.textContent?.trim() || '')
          .find((text) => text.includes('worker') || text.includes('Worker')) || '',
        title: content?.querySelector('[data-slot="alert-dialog-title"]')?.textContent?.trim() || '',
        triggerAriaExpanded: trigger?.getAttribute('aria-expanded') || null,
        triggerDataState: trigger?.getAttribute('data-state') || null,
      };
    })()`,
    (value) => value.dialogCount === 1
      && value.title.length > 0
      && value.description.length > 0
      && value.activeInside
      && value.triggerDataState === "open",
    "Worker restart confirmation did not open",
  );
}

function assertWorkerRestartSingleOpenEvidence(
  before: WorkerActionSnapshot,
  after: WorkerRestartDialogSnapshot,
  activation: string,
) {
  assert.equal(before.targetCount, 1, `${activation} must begin with one public restart trigger`);
  assert.equal(before.dataState, "closed", `${activation} must begin from the public closed trigger state`);
  assert.equal(before.ariaExpanded, "false", `${activation} must begin with aria-expanded=false`);
  assert.equal(after.dialogCount, 1, `${activation} must produce one visible Worker restart dialog`);
  assert.equal(after.triggerDataState, "open", `${activation} must produce one public closed-to-open state transition`);
  assert.equal(after.triggerAriaExpanded, "true", `${activation} must produce aria-expanded=true`);
}

async function waitForWorkerRestartOutcomeFocus(
  browser: BrowserHarness,
  target: string,
  safeOutcomeText: string,
  outcomeName: string,
) {
  return browser.waitFor<WorkerRestartOutcomeFocusSnapshot>(
    `(() => {
      const content = document.querySelector('[data-slot="alert-dialog-content"][data-state="open"]');
      const active = document.activeElement;
      const trigger = [...document.querySelectorAll('button[aria-label="Restart worker"]')]
        .find((candidate) => candidate.closest('tr')?.textContent?.includes(${JSON.stringify(target)}));
      const style = active instanceof HTMLElement ? getComputedStyle(active) : null;
      const activeHiddenOrInert = active instanceof HTMLElement && (
        Boolean(active.closest('[hidden], [aria-hidden="true"], [inert]'))
        || style?.display === 'none'
        || style?.visibility === 'hidden'
      );
      return {
        activeExists: active instanceof Element,
        activeHiddenOrInert,
        activeInside: content instanceof HTMLElement && active instanceof Element && content.contains(active),
        activeIsBody: active === document.body,
        activeIsTrigger: active === trigger,
        activeVisible: active instanceof HTMLElement && active.getClientRects().length > 0 && !activeHiddenOrInert,
        dialogCount: document.querySelectorAll('[data-slot="alert-dialog-content"][data-state="open"]').length,
        safeOutcomeTextVisible: content instanceof HTMLElement
          && content.getClientRects().length > 0
          && (content.textContent || '').includes(${JSON.stringify(safeOutcomeText)}),
      };
    })()`,
    (value) => value.dialogCount === 1
      && value.activeExists
      && value.activeInside
      && !value.activeIsBody
      && !value.activeIsTrigger
      && !value.activeHiddenOrInert
      && value.activeVisible
      && value.safeOutcomeTextVisible,
    `${outcomeName} did not retain safe focus inside the active Worker restart dialog`,
  );
}

async function workerRestartDialogCount(browser: BrowserHarness) {
  return browser.evaluate<number>(
    "document.querySelectorAll('[data-slot=\"alert-dialog-content\"][data-state=\"open\"]').length",
  );
}

async function workerRestartTriggerCount(browser: BrowserHarness, target: string) {
  return browser.evaluate<number>(`(() => {
    const row = [...document.querySelectorAll('tr')].find((candidate) => candidate.textContent?.includes(${JSON.stringify(target)}));
    return row?.querySelectorAll('button[aria-label="Restart worker"]').length || 0;
  })()`);
}

async function waitForWorkerRestartTriggerFocus(browser: BrowserHarness, target: string, action: string) {
  await browser.waitFor<{
    activeLabel: string;
    activeRow: string;
    activeTag: string;
    focused: boolean;
    targetCount: number;
  }>(
    `(() => {
      const buttons = [...document.querySelectorAll('button[aria-label="Restart worker"]')]
        .filter((candidate) => candidate.closest('tr')?.textContent?.includes(${JSON.stringify(target)}));
      const button = buttons[0];
      return {
        activeLabel: document.activeElement?.getAttribute('aria-label') || '',
        activeRow: document.activeElement?.closest('tr')?.textContent?.trim() || '',
        activeTag: document.activeElement?.tagName || '',
        focused: button instanceof HTMLButtonElement && document.activeElement === button,
        targetCount: buttons.length,
      };
    })()`,
    (value) => value.focused,
    `${action} did not return focus to the exact restart trigger`,
  );
}

async function tabToWorkerAction(browser: BrowserHarness, label: string, target: string) {
  const prepared = await browser.evaluate<boolean>(`(() => {
    const button = [...document.querySelectorAll('button[aria-label=${JSON.stringify(label)}]')]
      .find((candidate) => candidate.closest('tr')?.textContent?.includes(${JSON.stringify(target)}));
    if (!(button instanceof HTMLButtonElement)) return false;
    const tabbable = [...new Set(document.querySelectorAll('a[href], button, input, select, textarea, [tabindex]'))]
      .filter((candidate) => candidate instanceof HTMLElement
        && candidate.tabIndex >= 0
        && candidate.getClientRects().length > 0
        && !candidate.hasAttribute('disabled'));
    const index = tabbable.indexOf(button);
    const previous = index > 0 ? tabbable[index - 1] : null;
    if (!(previous instanceof HTMLElement)) return false;
    previous.focus();
    return document.activeElement === previous;
  })()`);
  assert.equal(prepared, true, `could not prepare Tab navigation to Worker action ${label} for ${target}`);
  await browser.pressKey("Tab");
  await waitForWorkerRestartTriggerFocus(browser, target, "Tab");
}

async function waitForWorkerRestartDialogClosed(browser: BrowserHarness) {
  await browser.waitFor(
    `(() => !document.querySelector('[data-slot="alert-dialog-content"]')
      && !document.querySelector('[data-slot="alert-dialog-overlay"]'))()`,
    Boolean,
    "Worker restart confirmation did not close and release its portal",
  );
}

async function waitForWorkerStatus(
  browser: BrowserHarness,
  workerName: string,
  accept: (value: WorkerStatusSnapshot) => boolean,
) {
  return browser.waitFor<WorkerStatusSnapshot>(
    `(() => {
      const row = [...document.querySelectorAll('tr')].find((candidate) => candidate.textContent?.includes(${JSON.stringify(workerName)}));
      const badge = row?.querySelector('[data-status-known]');
      if (!(row instanceof HTMLElement) || !(badge instanceof HTMLElement)) return null;
      return {
        icon: badge.querySelector('[data-status-icon]')?.getAttribute('data-status-icon') || null,
        known: badge.getAttribute('data-status-known'),
        rowText: row.textContent || '',
        text: badge.textContent || '',
        tone: badge.getAttribute('data-status-tone'),
        transitionDuration: getComputedStyle(badge).transitionDuration,
      };
    })()`,
    (value) => value !== null && accept(value),
    `Worker status for ${workerName} did not reach the expected state`,
    15_000,
  );
}

async function browserContainsMarker(browser: BrowserHarness, marker: string) {
  return browser.evaluate<boolean>(`(() => {
    const marker = ${JSON.stringify(marker)};
    if ((document.body.textContent || '').includes(marker)) return true;
    return [...document.querySelectorAll('[aria-label], [aria-description], [aria-describedby], [title]')].some((element) =>
      ['aria-label', 'aria-description', 'title'].some((attribute) => (element.getAttribute(attribute) || '').includes(marker))
      || (element.getAttribute('aria-describedby') || '').split(/\\s+/).some((id) => (document.getElementById(id)?.textContent || '').includes(marker))
    );
  })()`);
}

async function expandNavigationSections(browser: BrowserHarness) {
  await browser.evaluate(`(() => {
    const navigation = [...document.querySelectorAll('nav')].find((element) => element.getClientRects().length > 0 && element.querySelector('a[href^="/admin/"]'));
    if (!navigation) throw new Error('visible admin navigation is missing');
    for (const button of navigation.querySelectorAll('button[aria-expanded="false"]')) {
      if (button instanceof HTMLElement) button.click();
    }
    return true;
  })()`);
  await browser.waitFor(
    `(() => { const navigation = [...document.querySelectorAll('nav')].find((element) => element.getClientRects().length > 0 && element.querySelector('a[href^="/admin/"]')); return navigation?.querySelectorAll('a[href^="/admin/"]').length || 0; })()`,
    (value: number) => value === 26,
    "not all navigation sections expanded",
  );
}

async function setStoredDisplay(browser: BrowserHarness, locale: "ja" | "en", theme: "light" | "dark") {
  await browser.evaluate(`localStorage.setItem(${JSON.stringify(localeStorageKey)}, ${JSON.stringify(locale)}); localStorage.setItem(${JSON.stringify(themeStorageKey)}, ${JSON.stringify(theme)}); true`);
}

async function sheetMotion(browser: BrowserHarness) {
  return browser.evaluate<{ content: MotionSnapshot; overlay: MotionSnapshot }>(`(() => {
    const content = document.querySelector('.mobile-navigation-sheet');
    const overlay = content?.previousElementSibling;
    if (!(content instanceof HTMLElement) || !(overlay instanceof HTMLElement)) throw new Error('Sheet layers are missing');
    const motion = (element) => {
      const style = getComputedStyle(element);
      return { animationName: style.animationName, animationDuration: style.animationDuration, transitionDuration: style.transitionDuration };
    };
    return { content: motion(content), overlay: motion(overlay) };
  })()`);
}

async function waitForSheetSettled(browser: BrowserHarness) {
  await browser.waitFor(
    `(() => { const sheet = document.querySelector('.mobile-navigation-sheet'); if (!(sheet instanceof HTMLElement)) return false; const rect = sheet.getBoundingClientRect(); return rect.left >= -1 && rect.right > rect.left; })()`,
    Boolean,
    "mobile navigation did not finish entering",
  );
}

async function waitForAnimationFrames(browser: BrowserHarness) {
  await browser.evaluate("new Promise((resolve) => requestAnimationFrame(() => requestAnimationFrame(() => resolve(true))))");
}

async function scrollSelectorIntoView(browser: BrowserHarness, selector: string) {
  const found = await browser.evaluate<boolean>(`(() => {
    const element = document.querySelector(${JSON.stringify(selector)});
    if (!(element instanceof HTMLElement)) return false;
    element.scrollIntoView({ block: 'center', inline: 'nearest' });
    return true;
  })()`);
  assert.equal(found, true, `could not scroll action into view: ${selector}`);
  await waitForAnimationFrames(browser);
}

function deferred() {
  let settled = false;
  let resolvePromise!: () => void;
  const promise = new Promise<void>((resolve) => {
    resolvePromise = resolve;
  });
  return {
    promise,
    resolve: () => {
      if (settled) return;
      settled = true;
      resolvePromise();
    },
  };
}

function permissionUser(permissions: string[]) {
  return {
    user: { id: "stream-permission-user", username: "stream-permission-operator", roles: [] },
    permissions,
  };
}

function controlPlatformCoverState(
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

function workerConfiguration(id: string, yaml: string) {
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

function seconds(value: string) {
  return Math.max(...value.split(",").map((part) => {
    const normalized = part.trim();
    return normalized.endsWith("ms") ? Number.parseFloat(normalized) / 1_000 : Number.parseFloat(normalized) || 0;
  }));
}

function versionResponse({
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

function escapeRegExp(value: string) {
  return value.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
}
