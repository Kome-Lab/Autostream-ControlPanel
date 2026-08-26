import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import test from "node:test";

import { BrowserHarness, ensureWebServer, type StubResponse } from "./helpers/browser-harness.mts";
import {
  assertStreamsStartReadinessHandlerGuard,
  mutateStreamsStartReadinessHandlerGuard,
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
  { id: "worker-one", service_type: "worker", service_name: "Worker One", status: "online", health_status: "healthy" },
  { id: "encoder-one", service_type: "encoder", service_name: "Encoder One", status: "offline", health_status: "unhealthy" },
];
const currentVersion = versionResponse({ latestVersion: "v1.2.4" });
const availableVersion = versionResponse({ latestVersion: "v1.3.0", updateAvailable: true });
const startReadinessStream = { id: "stream-permission-fixture", name: "権限検証配信", status: "ready" };
const startReadinessPath = `/streams/${startReadinessStream.id}/start-readiness`;

test("UI Foundation runtime behavior", { timeout: 330_000 }, async (t) => {
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
  let startReadinessResponse: StubResponse = { body: { stream_id: startReadinessStream.id, ready: true, missing_service_types: [], issues: [], assigned_service_count: 2 } };
  let startReadinessMethods: string[] = [];
  browser.setRouteResolver(({ method, url }) => {
    const pathname = normalizePath(new URL(url).pathname);
    if (pathname === "/auth/me") return authResponse;
    if (pathname === "/setup/status") return setupResponse;
    if (pathname === "/settings/app") return { body: { app_name: "AutoStream", timezone: "Asia/Tokyo" } };
    if (pathname === "/auth/oauth/providers") return { body: [] };
    if (pathname === "/auth/login" && method === "POST") return loginResponse;
    if (pathname === "/auth/logout" && method === "POST") return logoutResponse;
    if (pathname === "/streams") return streamsResponse;
    if (pathname === startReadinessPath) {
      startReadinessMethods.push(method);
      return method === "POST" ? startReadinessResponse : { status: 405, body: { code: "method_not_allowed" } };
    }
    if (pathname === "/service-health") return healthResponse;
    if (pathname === "/version") return versionFixture;
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
      browser.clearRequestCounts();
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
      await browser.waitForResponseCount("/setup/status", 1);
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
    const readinessSelector = 'button[aria-label="開始準備を再確認"]';
    const editSelector = `button[aria-label=${JSON.stringify(`${startReadinessStream.name} を編集`)}]`;
    const actionSnapshotExpression = `(() => {
      const visibleButton = (selector) => [...document.querySelectorAll(selector)]
        .find((element) => element instanceof HTMLButtonElement && element.getClientRects().length > 0);
      const readiness = visibleButton(${JSON.stringify(readinessSelector)});
      const edit = visibleButton(${JSON.stringify(editSelector)});
      return {
        readinessPresent: Boolean(readiness),
        readinessAvailable: readiness instanceof HTMLButtonElement && !readiness.disabled,
        editAvailable: edit instanceof HTMLButtonElement && !edit.disabled,
      };
    })()`;
    const successResponse = { body: { stream_id: startReadinessStream.id, ready: true, missing_service_types: [], issues: [], assigned_service_count: 2 } };
    const matrix = [
      { name: "start only", permissions: ["streams.read", "streams.start"], readinessAvailable: true, editAvailable: false, requestCount: 1 },
      { name: "update only", permissions: ["streams.read", "streams.update"], readinessAvailable: false, editAvailable: true, requestCount: 0 },
      { name: "both", permissions: ["streams.read", "streams.start", "streams.update"], readinessAvailable: true, editAvailable: true, requestCount: 1 },
      { name: "neither", permissions: ["streams.read"], readinessAvailable: false, editAvailable: false, requestCount: 0 },
      { name: "wildcard", permissions: ["*"], readinessAvailable: true, editAvailable: true, requestCount: 1 },
    ] as const;

    await t.test("handler guard structural oracle rejects in-memory regressions", () => {
      const source = readFileSync(new URL("../src/features/streams/streams-view.tsx", import.meta.url), "utf8");
      assert.doesNotThrow(
        () => assertStreamsStartReadinessHandlerGuard(source),
        "actual start-readiness submit handler must retain its fresh permission guard",
      );
      const negativeFixtures: {
        name: string;
        mutation: StreamsStartReadinessGuardMutation;
        expectedError: RegExp;
      }[] = [
        { name: "fresh cache read removed", mutation: "remove-fresh-cache-read", expectedError: /latest auth query cache/ },
        { name: "streams.start replaced by streams.update", mutation: "use-streams-update-authority", expectedError: /authority must be streams\.start/ },
        { name: "early return removed", mutation: "remove-early-return", expectedError: /must return before/ },
        { name: "mutation moved before guard", mutation: "move-mutation-before-guard", expectedError: /must follow the fresh permission guard/ },
        { name: "RoleGuard remains but handler guard is removed", mutation: "remove-handler-guard", expectedError: /existing permission helper/ },
      ];
      for (const fixture of negativeFixtures) {
        const mutatedSource = mutateStreamsStartReadinessHandlerGuard(source, fixture.mutation);
        assert.throws(
          () => assertStreamsStartReadinessHandlerGuard(mutatedSource),
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
          authResponse = { body: permissionUser([...matrixCase.permissions]) };
          startReadinessResponse = successResponse;
          startReadinessMethods = [];
          browser.clearRequestCounts();
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
            await browser.clickSelector('[data-slot="alert-dialog-action"]');
            await browser.waitForRequestCount(startReadinessPath, 1);
          } else {
            await browser.clickSelector(readinessSelector);
            await waitForAnimationFrames(browser);
          }

          assert.deepEqual(
            {
              readinessAvailable: initial.readinessAvailable,
              editAvailable: initial.editAvailable,
              requestCount: browser.requests.get(startReadinessPath) || 0,
              methods: startReadinessMethods,
            },
            {
              readinessAvailable: matrixCase.readinessAvailable,
              editAvailable: matrixCase.editAvailable,
              requestCount: matrixCase.requestCount,
              methods: matrixCase.requestCount === 1 ? ["POST"] : [],
            },
            `${matrixCase.name}: start-readiness permission authority`,
          );
          assertNoBrowserConsoleErrors(browser.consoleErrorCount);
        });
      }

      await t.test("permission changes before confirm", async () => {
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
        browser.clearRequestCounts();
        await browser.evaluate("document.dispatchEvent(new Event('visibilitychange', { bubbles: true })); true");
        await browser.waitForResponseCount("/auth/me", 1);
        await browser.waitFor(
          actionSnapshotExpression,
          (value: { readinessAvailable: boolean }) => !value.readinessAvailable,
          "refetched permissions did not disable start-readiness",
        );

        startReadinessMethods = [];
        browser.clearRequestCounts();
        assert.equal(
          await browser.evaluate("Boolean(document.querySelector('[data-slot=\"alert-dialog-content\"][data-state=\"open\"]'))"),
          false,
          "permission refresh must dismiss the stale start-readiness confirmation",
        );
        await browser.clickSelector(readinessSelector);
        await waitForAnimationFrames(browser);
        assert.deepEqual(
          { requestCount: browser.requests.get(startReadinessPath) || 0, methods: startReadinessMethods },
          { requestCount: 0, methods: [] },
          "a permission change before confirmation must not send a mutation",
        );
      });

      await t.test("backend 403 retains the existing action error mapping", async () => {
        authResponse = { body: permissionUser(["streams.read", "streams.start"]) };
        startReadinessResponse = { status: 403, body: { code: "permission_denied" } };
        startReadinessMethods = [];
        browser.clearRequestCounts();
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
        await browser.clickSelector('[data-slot="alert-dialog-action"]');
        await browser.waitForRequestCount(startReadinessPath, 1);
        await browser.waitFor(
          "document.body.textContent || ''",
          (value: string) => value.includes("開始準備の確認を実行する権限がありません") && value.includes("permission_denied"),
          "backend 403 no longer used the existing stream action error mapping",
        );
        assert.deepEqual(startReadinessMethods, ["POST"]);
      });

      await t.test("pending mutation keeps duplicate start-readiness blocked", async () => {
        const release = deferred();
        try {
          authResponse = { body: permissionUser(["streams.read", "streams.start"]) };
          startReadinessResponse = { ...successResponse, waitUntil: release.promise };
          startReadinessMethods = [];
          browser.clearRequestCounts();
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
          await browser.clickSelector('[data-slot="alert-dialog-action"]');
          await browser.waitForRequestCount(startReadinessPath, 1);
          await browser.waitFor(
            actionSnapshotExpression,
            (value: { readinessAvailable: boolean }) => !value.readinessAvailable,
            "pending start-readiness mutation did not disable its trigger",
          );
          await browser.clickSelector(readinessSelector);
          await waitForAnimationFrames(browser);
          assert.equal(browser.requests.get(startReadinessPath), 1, "pending start-readiness must not send a duplicate request");
          assert.deepEqual(startReadinessMethods, ["POST"]);
        } finally {
          release.resolve();
          if ((browser.requests.get(startReadinessPath) || 0) > 0) await browser.waitForResponseCount(startReadinessPath, 1);
        }
      });
    } finally {
      authResponse = { body: currentUser };
      streamsResponse = { body: [] };
      startReadinessResponse = successResponse;
      startReadinessMethods = [];
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
    await browser.clickSelector('button[aria-label="Theme"]');
    await browser.waitFor("document.documentElement.classList.contains('dark')", Boolean, "theme did not switch to dark");
    assert.equal(await browser.evaluate<string>("location.pathname + location.hash"), routeBeforeTheme);
    assert.equal(browser.requests.get("/auth/me") || 0, 0, "theme switching must not recreate the session query");
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
