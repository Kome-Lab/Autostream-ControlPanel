import { clickVisible } from "./ui-regression/visible-trigger.mts";
import type { TestContext } from "node:test";
import assert from "node:assert/strict";
import { BrowserHarness } from "./helpers/browser-harness.mts";
import { assertNavigationBoundaryOutcome, type NavigationSnapshot } from "./helpers/ui-foundation-assertions.mts";
import { type BrowserRouteFixture, currentUser, healthyRows, currentVersion, escapeRegExp } from "./ui-browser-fixture.mts";
import { setStoredDisplay, waitForAnimationFrames, expandNavigationSections, sheetMotion, seconds, waitForSheetSettled } from "./ui-browser-navigation-helpers.mts";
import { waitForShell, desktopNavigationSnapshotExpression, mobileNavigationSnapshotExpression, createSnapshotExpression, type CreateSnapshot, assertCreateOutcome, closeCreateAndAssertFocusReturn, type EnglishShellSnapshot } from "./ui-browser-query-auth-helpers.mts";



export async function runNavigationParityScenario(t: TestContext, browser: BrowserHarness, server: { baseUrl: string }, fixture: Pick<BrowserRouteFixture, "authResponse" | "healthResponse" | "versionFixture">) {

  await t.test("desktop/mobile navigation parity, active route, and permission visibility are runtime-enforced", async () => {
    fixture.authResponse = { body: currentUser };
    fixture.healthResponse = { body: healthyRows };
    fixture.versionFixture = { body: currentVersion };
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
      await clickVisible(browser, 'button[aria-label="ナビゲーションを開く"]');
      await browser.waitFor("Boolean(document.querySelector('.mobile-navigation-sheet'))", Boolean, `mobile navigation did not open for ${requestedPath}`);
      const mobile = await browser.evaluate<NavigationSnapshot>(mobileNavigationSnapshotExpression);
      assertNavigationBoundaryOutcome({ requestedPath, expectedActive, desktop, mobile });
      await browser.pressKey("Escape");
      await browser.waitFor("!document.querySelector('.mobile-navigation-sheet')", Boolean, `mobile navigation did not close for ${requestedPath}`);
    }

    fixture.authResponse = {
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
    await clickVisible(browser, 'button[aria-label="ナビゲーションを開く"]');
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
    fixture.authResponse = { body: currentUser };
  });
}


export async function runNavigationCreateFocusScenario(t: TestContext, browser: BrowserHarness, server: { baseUrl: string }, fixture: Pick<BrowserRouteFixture, "authResponse" | "healthResponse" | "versionFixture">) {

  await t.test("same-route and cross-route mobile create release the navigation focus owner", async () => {
    fixture.authResponse = { body: currentUser };
    await browser.setMediaFeatures([]);
    await browser.setViewport(390, 844);
    fixture.healthResponse = { body: healthyRows };
    fixture.versionFixture = { body: currentVersion };
    await setStoredDisplay(browser, "ja", "light");
    await browser.navigate(`${server.baseUrl}/admin/streams/`);
    await waitForShell(browser, "ナビゲーションを開く");
    await clickVisible(browser, 'button[aria-label="ナビゲーションを開く"]', undefined, true);
    await browser.waitFor("Boolean(document.querySelector('.mobile-navigation-sheet'))", Boolean, "mobile navigation did not open");
    const normalMotion = await sheetMotion(browser);
    assert.notEqual(normalMotion.content.animationName, "none");
    assert.ok(seconds(normalMotion.content.animationDuration) > 0);
    await waitForSheetSettled(browser);

    await clickVisible(browser, '.mobile-navigation-sheet a[href="/admin/streams/#create-stream"]');
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
    await closeCreateAndAssertFocusReturn(browser, "ナビゲーションを開く", "same-route");

    await browser.navigate(`${server.baseUrl}/admin/`);
    await waitForShell(browser, "ナビゲーションを開く");
    await clickVisible(browser, 'button[aria-label="ナビゲーションを開く"]', undefined, true);
    await browser.waitFor("Boolean(document.querySelector('.mobile-navigation-sheet'))", Boolean, "cross-route navigation did not open");
    await waitForSheetSettled(browser);
    await clickVisible(browser, '.mobile-navigation-sheet a[href="/admin/streams/#create-stream"]');
    const crossRoute = await browser.waitFor(
      createSnapshotExpression,
      (value: CreateSnapshot) => value.dialogCount === 1 && value.createOpen && !value.navigationOpen && value.focusInsideCreate && value.url.includes("/admin/streams/#create-stream"),
      "cross-route create navigation did not settle on one dialog",
      10_000,
    );
    assertCreateOutcome(crossRoute);
    await closeCreateAndAssertFocusReturn(browser, "ナビゲーションを開く", "cross-route");

    await browser.setViewport(1440, 900);
    await browser.navigate(`${server.baseUrl}/admin/streams/`);
    await waitForShell(browser, "アカウントメニュー");
    await clickVisible(browser, 'header a[href="/admin/streams/#create-stream"]');
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
}


export async function runReducedMotionScenario(t: TestContext, browser: BrowserHarness, server: { baseUrl: string }, fixture: Pick<BrowserRouteFixture, "authResponse">) {

  await t.test("reduced motion removes Sheet animation while preserving close and focus", async () => {
    fixture.authResponse = { body: currentUser };
    await browser.setViewport(390, 844);
    await browser.setMediaFeatures([{ name: "prefers-reduced-motion", value: "reduce" }]);
    await browser.navigate(`${server.baseUrl}/admin/streams/`);
    await waitForShell(browser, "ナビゲーションを開く");
    await clickVisible(browser, 'button[aria-label="ナビゲーションを開く"]');
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
}


export async function runDisplayControlsScenario(t: TestContext, browser: BrowserHarness, server: { baseUrl: string }, fixture: Pick<BrowserRouteFixture, "authResponse">) {

  await t.test("locale and theme controls preserve route/session and expose translated accessible names", async () => {
    fixture.authResponse = { body: currentUser };
    await browser.setViewport(390, 844);
    await setStoredDisplay(browser, "ja", "light");
    await browser.navigate(`${server.baseUrl}/admin/streams/`);
    await waitForShell(browser, "ナビゲーションを開く");
    await clickVisible(browser, 'button[aria-label="ナビゲーションを開く"]');
    await browser.waitFor("Boolean(document.querySelector('.mobile-navigation-sheet'))", Boolean, "mobile navigation did not open for locale test");
    await waitForSheetSettled(browser);
    browser.clearRequestCounts();
    await clickVisible(browser, '[role="combobox"][aria-label="言語"]');
    await clickVisible(browser, '[role=option]', /^English$/);
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
    await clickVisible(browser, '.mobile-navigation-sheet button[aria-label="Close navigation"]');
    await browser.waitFor("!document.querySelector('.mobile-navigation-sheet')", Boolean, "translated close action did not close navigation");
    await clickVisible(browser, 'button[aria-label="Account menu"]');
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
    await clickVisible(browser, 'button[aria-label="Theme"]');
    await browser.waitFor("document.documentElement.classList.contains('dark')", Boolean, "theme did not switch to dark");
    assert.equal(await browser.evaluate<string>("location.pathname + location.hash"), routeBeforeTheme);
    assert.equal(browser.requests.get("/auth/me") || 0, 0, "theme switching must not recreate the session query");
		assert.equal(await browser.evaluate<string>(`localStorage.getItem('autostream.ui_preference') || ''`), mirrorBeforeTheme, "authenticated unsaved theme preview must not replace the DB bootstrap mirror");
  });
}
