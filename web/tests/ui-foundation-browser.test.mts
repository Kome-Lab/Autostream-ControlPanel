import assert from "node:assert/strict";
import { fileURLToPath } from "node:url";
import test from "node:test";

import { BrowserHarness, ensureWebServer, type StubResponse } from "./helpers/browser-harness.mts";

const webRoot = fileURLToPath(new URL("..", import.meta.url));
const requestedBaseUrl = process.env.AUTOSTREAM_BROWSER_BASE_URL || "http://127.0.0.1:3002";
const localeStorageKey = "autostream.controlPanel.locale";
const themeStorageKey = "autostream.theme";

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

test("UI Foundation runtime behavior", { timeout: 180_000 }, async (t) => {
  const server = await ensureWebServer(webRoot, requestedBaseUrl);
  const browser = await BrowserHarness.launch();
  t.after(async () => {
    await browser.close();
    await server.close();
  });

  let authResponse: StubResponse = { body: currentUser };
  let healthResponse: StubResponse = { body: healthyRows };
  let versionFixture: StubResponse = { body: currentVersion };
  browser.setRouteResolver(({ method, url }) => {
    const pathname = normalizePath(new URL(url).pathname);
    if (pathname === "/auth/me") return authResponse;
    if (pathname === "/setup/status") return { body: { setup_enabled: true, setup_required: false } };
    if (pathname === "/settings/app") return { body: { app_name: "AutoStream", timezone: "Asia/Tokyo" } };
    if (pathname === "/streams") return { body: [] };
    if (pathname === "/service-health") return healthResponse;
    if (pathname === "/version") return versionFixture;
    if (pathname === "/auth/session/refresh" && method === "POST") return { body: { status: "ok" } };
    return null;
  });

  await t.test("query states distinguish loading, empty, unhealthy, error, stale, recovery, and update variants", async () => {
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
    await browser.reload();
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
  });

  await t.test("desktop/mobile navigation parity, active route, and permission visibility are runtime-enforced", async () => {
    authResponse = { body: currentUser };
    healthResponse = { body: healthyRows };
    versionFixture = { body: currentVersion };
    await setStoredDisplay(browser, "ja", "light");
    await browser.setViewport(1440, 900);
    await browser.navigate(`${server.baseUrl}/admin/streams/`);
    await waitForShell(browser, "アカウントメニュー");
    await expandNavigationSections(browser);
    const desktop = await browser.evaluate<NavigationSnapshot>(navigationSnapshotExpression);
    assert.equal(desktop.active, "/admin/streams/");
    assert.ok(desktop.hrefs.length > 20);

    await browser.setViewport(390, 844);
    await browser.clickSelector('button[aria-label="ナビゲーションを開く"]');
    await browser.waitFor("Boolean(document.querySelector('.mobile-navigation-sheet'))", Boolean, "mobile navigation did not open for parity check");
    const mobile = await browser.evaluate<NavigationSnapshot>(navigationSnapshotExpression);
    assert.deepEqual(mobile.hrefs, desktop.hrefs);
    assert.equal(mobile.active, desktop.active);
    await browser.pressKey("Escape");
    await browser.waitFor("!document.querySelector('.mobile-navigation-sheet')", Boolean, "parity navigation did not close");

    authResponse = {
      body: {
        user: { id: "limited-user", username: "limited-operator", roles: [] },
        permissions: ["streams.read"],
      },
    };
    await browser.setViewport(1440, 900);
    await browser.reload();
    await waitForShell(browser, "アカウントメニュー");
    const limited = await browser.evaluate<NavigationSnapshot>(navigationSnapshotExpression);
    assert.deepEqual(limited.hrefs, ["/admin/", "/admin/streams/"]);
    assert.equal(limited.active, "/admin/streams/");
    assert.equal(await browser.evaluate("Boolean(document.querySelector('a[href=\"/admin/streams/#create-stream\"]'))"), false);
    assert.equal(await browser.evaluate("Boolean(document.querySelector('a[href=\"/admin/users/\"]'))"), false);
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
type NavigationSnapshot = { hrefs: string[]; active: string };

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

const navigationSnapshotExpression = `(() => {
  const navigation = [...document.querySelectorAll('nav')].find((element) => element.getClientRects().length > 0 && element.querySelector('a[href^="/admin/"]'));
  if (!navigation) throw new Error('visible admin navigation is missing');
  return {
    hrefs: [...navigation.querySelectorAll('a[href^="/admin/"]')].map((link) => link.getAttribute('href')),
    active: navigation.querySelector('a[aria-current="page"]')?.getAttribute('href') || '',
  };
})()`;

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

function escapeRegExp(value: string) {
  return value.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
}
