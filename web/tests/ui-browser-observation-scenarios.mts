import type { TestContext } from "node:test";
import assert from "node:assert/strict";
import { BrowserHarness } from "./helpers/browser-harness.mts";
import { assertAuthMeExpiryOutcome, assertBrowserSuiteExecution, assertLoginNavigationOutcome, assertLogoutOutcome, assertNavigationBoundaryOutcome, authMeExpiryScenarioName, type BrowserSuiteSummary } from "./helpers/ui-foundation-assertions.mts";
import { type BrowserRouteFixture, currentUser, controlPlatformStream, healthyRows, workerPilotRows, currentVersion, expectedAuthMeExpiryReturnURL } from "./ui-browser-fixture.mts";
import { setStoredDisplay, waitForAnimationFrames } from "./ui-browser-navigation-helpers.mts";
import { waitForShell, waitForStatus, type FocusSnapshot, focusSnapshotExpression } from "./ui-browser-query-auth-helpers.mts";



export async function runResponsiveSurfacesScenario(t: TestContext, browser: BrowserHarness, server: { baseUrl: string }, fixture: Pick<BrowserRouteFixture, "authResponse" | "streamsResponse" | "healthResponse" | "workersResponse" | "nodesResponse">) {

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
    fixture.authResponse = { body: currentUser };
    fixture.streamsResponse = { body: [controlPlatformStream] };
    fixture.healthResponse = { body: healthyRows };
    fixture.workersResponse = { body: workerPilotRows };
    fixture.nodesResponse = { body: workerPilotRows };
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
}


export async function runStatusFocusScenario(t: TestContext, browser: BrowserHarness, fixture: Pick<BrowserRouteFixture, "authResponse" | "healthResponse" | "versionFixture">) {

  await t.test("status focus remains visible in normal and forced-colors modes", async () => {
    fixture.authResponse = { body: currentUser };
    await browser.setViewport(1440, 900);
    await browser.setMediaFeatures([]);
    fixture.healthResponse = { body: healthyRows };
    fixture.versionFixture = { body: currentVersion };
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
}


export async function runFalsePositiveGuardsScenario(t: TestContext, server: { baseUrl: string }) {

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
}
