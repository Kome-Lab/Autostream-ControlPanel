import type { TestContext } from "node:test";
import assert from "node:assert/strict";
import { BrowserHarness } from "./helpers/browser-harness.mts";
import { waitForMinimumMapCount } from "./helpers/browser-response-count.mts";
import { assertAuthMeExpiryOutcome, assertLoginNavigationOutcome, assertLogoutOutcome, assertNoBrowserConsoleErrors, authMeExpiryScenarioName } from "./helpers/ui-foundation-assertions.mts";
import { type BrowserRouteFixture, currentUser, healthyRows, availableVersion, currentVersion, versionResponse, csrfStorageKey, expectedAuthMeExpiryReturnURL, loginReturnParameterName } from "./ui-browser-fixture.mts";
import { statusSnapshotExpression, type StatusSnapshot, assertStatusSnapshot, waitForStatus, triggerReconnect, waitForShell, type LogoutBrowserSnapshot, logoutSnapshotExpression, type SessionExpirySnapshot, sessionExpirySnapshotExpression, type AuthMeExpiryBrowserSnapshot, authMeExpirySnapshotExpression } from "./ui-browser-query-auth-helpers.mts";
import { deferred, setStoredDisplay, waitForAnimationFrames } from "./ui-browser-navigation-helpers.mts";

// Bootstrap receipt counts may include several completed reads. Operation
// periods keep their separate exact one-shot assertions below.
async function waitForInitialGET(browser: BrowserHarness, pathname: string, previousResponses: number, status: number) {
  await waitForMinimumMapCount(browser.responses, pathname, previousResponses + 1, `initial GET ${pathname}`, 10_000, () => browser.assertNoFatalError());
  await browser.waitForRequestHandlersIdle({ pathname, method: "GET", timeoutMs: 10_000 });
  const statuses = (browser.responseStatuses.get(pathname) || []).slice(previousResponses);
  assert.ok(statuses.length > 0, `${pathname}: a new bootstrap response completed`);
  assert.equal(statuses.length, (browser.responses.get(pathname) || 0) - previousResponses, `${pathname}: bootstrap status receipts match completed responses`);
  assert.deepEqual(statuses, statuses.map(() => status), `${pathname}: every new bootstrap response has the expected status`);
}


export async function runQueryStatesScenario(t: TestContext, browser: BrowserHarness, server: { baseUrl: string }, fixture: Pick<BrowserRouteFixture, "authResponse" | "healthResponse" | "versionFixture" | "observeBootstrapCancellation">) {

  await t.test("query states distinguish loading, empty, unhealthy, error, stale, recovery, and update variants", async () => {
    const previousObservation = fixture.observeBootstrapCancellation;
    const paths = ["/auth/me", "/settings/app", "/service-health", "/version"];
    const nextDocument = async () => {
      await browser.waitForRequestHandlersIdle({ method: "GET" });
      return new Map(paths.map(path => [path, browser.responses.get(path) || 0]));
    };
    const completedRead = async (path: string, minimum: number, status: number) => {
      await waitForMinimumMapCount(browser.responses, path, minimum, `completed query-state GET ${path}`, 15_000, () => browser.assertNoFatalError());
      await browser.waitForRequestHandlersIdle({ pathname: path, method: "GET" });
      assert.equal(browser.responseStatuses.get(path)?.at(-1), status, `${path}: intended query-state response completed`);
    };
    const completedState = async (baseline: ReadonlyMap<string, number>, healthStatus: number, versionStatus: number) => {
      for (const path of paths) await completedRead(path, baseline.get(path)! + 1,
        path === "/service-health" ? healthStatus : path === "/version" ? versionStatus : 200);
      await browser.waitForRequestHandlersIdle({ method: "GET" });
    };
    try {
      fixture.observeBootstrapCancellation = true;
      fixture.authResponse = { body: currentUser };
      await browser.setViewport(1440, 900);
      fixture.healthResponse = { body: healthyRows, delayMs: 1_200 };
      fixture.versionFixture = { body: availableVersion, delayMs: 1_200 };
      const initialReads = await nextDocument();
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
      await completedState(initialReads, 200, 200);

      fixture.healthResponse = { body: [] };
      fixture.versionFixture = { body: currentVersion };
      const emptyReads = await nextDocument();
      await browser.reload();
      const empty = await waitForStatus(browser, "登録済みサービスなし", "最新版です");
      assertStatusSnapshot(empty, { health: "登録済みサービスなし", update: "最新版です" });
      await completedState(emptyReads, 200, 200);

      fixture.healthResponse = { status: 503, body: { code: "service_health_unavailable" } };
      fixture.versionFixture = { status: 503, body: { code: "version_unavailable" } };
      await browser.waitForRequestHandlersIdle({ method: "GET" });
      browser.clearRequestCounts();
      const failedReads = await nextDocument();
      await browser.reload();
      await waitForMinimumMapCount(browser.responses, "/service-health", 1, "first 503 health response", 10_000, () => browser.assertNoFatalError());
      await waitForMinimumMapCount(browser.responses, "/version", 1, "first 503 version response", 10_000, () => browser.assertNoFatalError());
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
      await completedState(failedReads, 503, 503);

      fixture.healthResponse = { body: healthyRows };
      fixture.versionFixture = { body: availableVersion };
      const recoveredReads = await nextDocument();
      await browser.reload();
      await waitForStatus(browser, "1/2 サービス稼働", "更新 v1.3.0 を利用できます");
      await completedState(recoveredReads, 200, 200);
      fixture.healthResponse = { status: 503, body: { code: "service_health_unavailable" } };
      const staleResponse = browser.responses.get("/service-health") || 0;
      await triggerReconnect(browser);
      const stale = await browser.waitFor(
        statusSnapshotExpression,
        (value: StatusSnapshot) => value.health === "1/2 サービス稼働（更新失敗）",
        "cached health did not become stale after refresh failure",
        15_000,
      );
      assert.equal(stale.health, "1/2 サービス稼働（更新失敗）");
      await completedRead("/service-health", staleResponse + 1, 503);
      await browser.waitForRequestHandlersIdle({ method: "GET" });

      fixture.healthResponse = { body: healthyRows };
      const healthyResponse = browser.responses.get("/service-health") || 0;
      await triggerReconnect(browser);
      await browser.waitFor(
        statusSnapshotExpression,
        (value: StatusSnapshot) => value.health === "1/2 サービス稼働",
        "health status did not recover without a page reload",
        5_000,
      );
      await completedRead("/service-health", healthyResponse + 1, 200);
      await browser.waitForRequestHandlersIdle({ method: "GET" });

      fixture.versionFixture = { body: versionResponse({ source: "disabled" }) };
      const disabledReads = await nextDocument();
      await browser.reload();
      await waitForStatus(browser, "1/2 サービス稼働", "更新情報は未確認です");
      await completedState(disabledReads, 200, 200);

      fixture.healthResponse = { body: healthyRows };
      fixture.versionFixture = { body: currentVersion };
      await browser.waitForRequestHandlersIdle({ method: "GET" });
      browser.clearRequestCounts();
      const dashboardReads = await nextDocument();
      await browser.navigate(`${server.baseUrl}/admin/`);
      await waitForStatus(browser, "1/2 サービス稼働", "最新版です");
      await completedState(dashboardReads, 200, 200);
      assert.equal(browser.requests.get("/service-health"), 1, "Shell and Dashboard must share the service-health request");
    } finally {
      fixture.observeBootstrapCancellation = previousObservation;
      fixture.authResponse = { body: currentUser };
      fixture.healthResponse = { body: healthyRows };
      fixture.versionFixture = { body: currentVersion };
    }
  });
}


export async function runLogoutScenario(t: TestContext, browser: BrowserHarness, server: { baseUrl: string }, fixture: Pick<BrowserRouteFixture, "authResponse" | "logoutResponse">) {

  await t.test("logout clears protected UI and sends exactly one mutation", async () => {
    const logoutRelease = deferred();
    try {
      fixture.authResponse = { body: currentUser };
      fixture.logoutResponse = { body: { status: "ok" }, waitUntil: logoutRelease.promise };
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
      fixture.logoutResponse = { body: { status: "ok" } };
    }
  });
}


export async function runSessionExpiryScenario(t: TestContext, browser: BrowserHarness, server: { baseUrl: string }, fixture: Pick<BrowserRouteFixture, "authResponse" | "refreshResponse">) {

  await t.test("session expiry keeps a validated same-origin return URL without a redirect loop", async () => {
    fixture.authResponse = { body: currentUser };
    fixture.refreshResponse = { status: 401, body: { code: "unauthorized" } };
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
    fixture.refreshResponse = { body: { status: "ok" } };
  });
}


export async function runAuthMeExpiryScenario(t: TestContext, browser: BrowserHarness, server: { baseUrl: string }, fixture: Pick<BrowserRouteFixture, "authResponse" | "refreshResponse">) {

  await t.test(authMeExpiryScenarioName, async () => {
    fixture.authResponse = { body: currentUser };
    fixture.refreshResponse = { body: { status: "ok" } };
    await browser.setViewport(1440, 900);
    await setStoredDisplay(browser, "en", "light");
    await browser.waitForRequestHandlersIdle({ method: "GET" });
    browser.clearRequestCounts();
    const initialAuthResponses = browser.responses.get("/auth/me") || 0;
    await browser.navigate(`${server.baseUrl}${expectedAuthMeExpiryReturnURL}`);
    await waitForShell(browser, "Account menu");
    await waitForInitialGET(browser, "/auth/me", initialAuthResponses, 200);
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
    await waitForAnimationFrames(browser);
    await browser.waitForRequestHandlersIdle({ method: "GET" });
    await browser.waitForRequestHandlersIdle({ pathname: "/auth/session/refresh", method: "POST" });

    fixture.authResponse = { status: 401, body: { code: "unauthorized" } };
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
    // LoginCard starts its own setup query after the expiry navigation. Settle it
    // before the next scenario leaves this document and cancels that request.
    await waitForInitialGET(browser, "/setup/status", 0, 200);
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

    fixture.authResponse = { body: currentUser };
    fixture.refreshResponse = { body: { status: "ok" } };
  });
}


export async function runRefreshGenerationScenario(t: TestContext, browser: BrowserHarness, server: { baseUrl: string }, fixture: Pick<BrowserRouteFixture, "authResponse" | "refreshResponse" | "loginResponse" | "logoutResponse">) {

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
      fixture.authResponse = { body: previousUser };
      fixture.refreshResponse = { body: { status: "ok" }, waitUntil: refreshRelease.promise };
      fixture.loginResponse = { body: { csrf_token: "newer-session-csrf" } };
      await browser.setViewport(1440, 900);
      await browser.navigate(`${server.baseUrl}/admin/streams/`);
      await browser.waitFor(
        "document.querySelector('button[aria-label=\"Account menu\"]')?.textContent || ''",
        (value: string) => value.includes("previous-operator"),
        "previous authenticated session did not render",
      );
      await browser.waitForRequestHandlersIdle({ method: "GET" });
      browser.clearRequestCounts();
      browser.clearConsoleErrors();
      const loginSetupResponses = browser.responses.get("/setup/status") || 0;

      fixture.authResponse = { body: newerUser };
      await browser.evaluate("window.dispatchEvent(new Event('focus')); true");
      await browser.waitForRequestCount("/auth/session/refresh", 1);

      fixture.logoutResponse = { body: { status: "ok" } };
      await browser.clickSelector('button[aria-label="Account menu"]');
      await browser.clickRoleWithText("menuitem", "Log out");
      await browser.waitFor("location.pathname", (value: string) => value === "/login/", "logout did not expose the new-session login flow");
      await browser.waitFor(
        `Boolean(document.querySelector('input[autocomplete="username"]') && document.querySelector('button[type="submit"]:not([disabled])'))`,
        Boolean,
        "new-session login form did not become interactive",
      );
      // Form readiness depends on app settings, so it does not prove that the
      // separate setup query has settled before submitting this new session.
      await waitForInitialGET(browser, "/setup/status", loginSetupResponses, 200);
      await browser.fillSelector('input[autocomplete="username"]', "newer-operator");
      await browser.fillSelector('input[autocomplete="current-password"]', "browser-fixture-password");
      await browser.clickSelector('form button[type="submit"]:not([disabled])');
      await browser.waitFor("location.pathname", (value: string) => value === "/admin/", "new session did not reach the authenticated shell");
      await browser.waitForRequestHandlersIdle({ method: "GET" });
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
      fixture.authResponse = { body: currentUser };
      fixture.refreshResponse = { body: { status: "ok" } };
      fixture.logoutResponse = { body: { status: "ok" } };
      fixture.loginResponse = { body: { csrf_token: "browser-fixture-csrf" } };
    }
  });
}


export async function runSetupUnmountScenario(t: TestContext, browser: BrowserHarness, server: { baseUrl: string }, fixture: Pick<BrowserRouteFixture, "authResponse" | "setupResponse">) {

  await t.test("session guard ignores setup completion after unmount", async () => {
    const setupRelease = deferred();
    try {
      fixture.authResponse = { status: 401, body: { code: "unauthorized" } };
      fixture.setupResponse = { body: { setup_enabled: true, setup_required: true }, waitUntil: setupRelease.promise };
      await browser.waitForRequestHandlersIdle({ pathname: "/setup/status", method: "GET" });
      browser.clearRequestCounts("/setup/status");
      browser.clearConsoleErrors();
      await browser.waitForRequestHandlersIdle({ pathname: "/auth/me", method: "GET" });
      const initialAuthResponses = browser.responses.get("/auth/me") || 0;
      await browser.navigate("about:blank");
      await browser.navigate(`${server.baseUrl}/admin/streams/?guard-unmount=1`);
      await waitForInitialGET(browser, "/auth/me", initialAuthResponses, 401);
      await browser.waitFor(
        `Boolean(document.querySelector('a[href="/login/"]'))`,
        Boolean,
        "authentication-pending login action did not render",
      );
      await waitForMinimumMapCount(browser.requests, "/setup/status", 1, "held setup GET arrived before unmount", 10_000, () => browser.assertNoFatalError());
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
      fixture.authResponse = { body: currentUser };
      fixture.setupResponse = { body: { setup_enabled: true, setup_required: false } };
    }
  });
}


export async function runLoginReturnScenario(t: TestContext, browser: BrowserHarness, server: { baseUrl: string }, fixture: Pick<BrowserRouteFixture, "observeBootstrapCancellation" | "authResponse" | "loginResponse" | "setupResponse" | "versionFixture">) {

  await t.test("login rejects external return URL variants", async () => {
    const previousAuthResponse = fixture.authResponse;
    const previousBootstrapObservation = fixture.observeBootstrapCancellation;
    const previousVersionFixture = fixture.versionFixture;
    try {
    fixture.observeBootstrapCancellation = true;
    fixture.versionFixture = { body: currentVersion };
    fixture.authResponse = { body: currentUser, observeBootstrapCancellation: true };
    fixture.loginResponse = { body: { csrf_token: "login-fixture-csrf" } };
    fixture.setupResponse = { body: { setup_enabled: true, setup_required: false } };
    const unsafeReturnURLs = [
      "https://attacker.example/",
      "//attacker.example/",
      "javascript:alert(1)",
      "https%3A%2F%2Fattacker.example%2Fadmin%2F",
      "/admin\\@attacker.example/",
    ];

    for (const unsafeReturnURL of unsafeReturnURLs) {
      const query = new URLSearchParams({ redirect_after: unsafeReturnURL });
      await browser.waitForRequestHandlersIdle({ method: "GET" });
      const loginSetupResponses = browser.responses.get("/setup/status") || 0;
      await browser.navigate(`${server.baseUrl}/login/?${query}`);
      await browser.waitFor(
        `Boolean(document.querySelector('form input[autocomplete="username"]') && document.querySelector('form button[type="submit"]:not([disabled])'))`,
        Boolean,
        "login form did not become interactive",
      );
      await waitForInitialGET(browser, "/setup/status", loginSetupResponses, 200);
      await browser.fillSelector('input[autocomplete="username"]', "browser-operator");
      await browser.fillSelector('input[autocomplete="current-password"]', "browser-fixture-password");
      browser.clearRequestCounts();
      browser.clearConsoleErrors();
      const dashboardReads = new Map(["/auth/me", "/streams", "/version"].map(path => [path, browser.responses.get(path) || 0]));
      const authPhase = browser.declareLoginReturnBootstrapPhase(server.baseUrl);
      try {
      await browser.clickSelector('form button[type="submit"]:not([disabled])');
      const href = await browser.waitFor(
        "location.href",
        (value: string) => new URL(value).pathname === "/admin/",
        `unsafe return URL was not normalized: ${unsafeReturnURL}`,
        15_000,
      );
      assertLoginNavigationOutcome({ href, expectedOrigin: server.baseUrl, expectedPathname: "/admin/" });
      await waitForShell(browser, "Account menu");
      for (const path of ["/auth/me", "/streams", "/version"]) await waitForInitialGET(browser, path, dashboardReads.get(path)!, 200);
      await waitForAnimationFrames(browser);
      await browser.waitForRequestHandlersIdle({ method: "GET" });
      assert.equal(browser.requests.get("/auth/login"), 1);
      assertNoBrowserConsoleErrors(browser.consoleErrorCount);
      } finally { authPhase.dispose(); }
    }
    } finally { fixture.authResponse = previousAuthResponse; fixture.observeBootstrapCancellation = previousBootstrapObservation; fixture.versionFixture = previousVersionFixture; }
  });
}
