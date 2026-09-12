import assert from "node:assert/strict";
import { mkdirSync, writeFileSync } from "node:fs";
import { resolve } from "node:path";
import { BrowserHarness } from "./browser-harness.mts";
import {
  BUNDLE9_BROWSER_CLOCK, apiObservationSummary, assertCaptureInventory,
  bundle9BrowserSurfaces, bundle9Viewports, sha256, type APIObservation,
} from "./bundle9-browser-contract.mts";
import {
  bundle9ReadinessPath, bundle9ReadinessSelector, bundle9Stream,
  bundle9SyntheticMFASecret, createBundle9Fixture,
} from "./bundle9-browser-fixtures.mts";

export type Bundle9Capture = Readonly<{ name: string; observation: unknown; pngSHA256: string }>;

export function assertBundle9CancelledMutations(trace: readonly APIObservation[]) {
  assert.deepEqual(trace.filter((call) => call.method !== "GET"), [
    { method: "POST", path: "/auth/session/refresh", body: null, status: 200 },
  ], "cancel permits exactly one unchanged session refresh and no business mutation");
}

const monitoringRetryExpression = `(() => {
  const sections = [...document.querySelectorAll('main section')].filter((section) => section.querySelector('h2')?.textContent.trim() === '現在の問題・Node稼働・診断を分けて確認');
  if (sections.length !== 1 || !sections[0].textContent.includes('一部の情報を取得できません')) return false;
  const buttons = [...sections[0].querySelectorAll('button')].filter((button) => button.getClientRects().length > 0 && getComputedStyle(button).visibility !== 'hidden' && button.textContent.trim() === '再試行');
  if (buttons.length !== 1 || buttons[0].disabled) return false;
  const button = buttons[0];
  if (globalThis.__bundle9MonitoringRetry && globalThis.__bundle9MonitoringRetry !== button) return false;
  globalThis.__bundle9MonitoringRetry = button;
  button.setAttribute('data-bundle9-monitoring-retry', 'target');
  button.scrollIntoView({ block: 'center' });
  return true;
})()`;

export async function retryBundle9Monitoring(
  browser: BrowserHarness,
  fixture: Pick<ReturnType<typeof createBundle9Fixture>, "state">,
  record: (name: string) => Promise<void>,
) {
  assert.equal(fixture.state.healthError, true, "observe the failed queries before normalizing the fixture");
  await browser.waitFor(monitoringRetryExpression, Boolean, "unique visible enabled Monitoring summary retry");
  await record("monitoring-error"); // Includes safe-detail, paint and real response settlement checks.
  assert.equal(await browser.evaluate(monitoringRetryExpression), true, "the same summary retry must remain enabled in the error state");
  const completed = ["/service-health", "/observability/incidents", "/observability/diagnostics", "/streams"].map((path) => {
    const count = browser.responses.get(path) || 0;
    assert.ok(count > 0, `initial Monitoring GET must complete: ${path}`);
    assert.equal(browser.responseStatuses.get(path)?.length, count, `completed Monitoring statuses: ${path}`);
    return { path, count };
  });
  assert.equal(browser.responseStatuses.get("/service-health")?.at(-1), 503, "observe the actual failed health response before retry");
  fixture.state.healthError = false;
  await browser.clickSelector('[data-bundle9-monitoring-retry="target"]');
  for (const { path, count } of completed) {
    await browser.waitForResponseCount(path, count + 1);
    assert.equal(browser.responseStatuses.get(path)?.[count], 200, `new successful Monitoring GET after the one retry click: ${path}`);
  }
  await browser.waitFor("document.querySelector('main')?.textContent || ''", (value: string) => value.includes("監視情報は正常に取得済み"), "monitoring retry recovery");
  await browser.evaluate("globalThis.__bundle9MonitoringRetry?.removeAttribute('data-bundle9-monitoring-retry'); delete globalThis.__bundle9MonitoringRetry; true");
  await record("monitoring-recovered");
}

export async function navigateBundle9Document(
  browser: BrowserHarness,
  fixture: Pick<ReturnType<typeof createBundle9Fixture>, "resetTrace">,
  url: string,
  prepareNextPhase: () => void = () => {},
) {
  // The caller has observed the current page's required GETs and operations.
  // Viewport changes can still cause paint and Fetch work on that document.
  await paintBarrier(browser);
  browser.assertNoFatalError();
  await browser.navigate("about:blank");
  await browser.waitForRequestHandlersIdle();
  prepareNextPhase();
  fixture.resetTrace();
  browser.clearRequestCounts();
  browser.clearNavigationCount();
  browser.clearConsoleErrors();
  await browser.navigate(url);
}

export async function captureBundle9Source(baseURL: string, output: string) {
  mkdirSync(output, { recursive: false });
  const browser = await BrowserHarness.launch();
  const fixture = createBundle9Fixture(baseURL);
  browser.setRouteResolver(fixture.resolver);
  const captures: Bundle9Capture[] = [];
  const failures: { name: string; error: string }[] = [];
  try {
    await browser.configureDeterministicDocument({
      timezone: "Asia/Tokyo", locale: "ja-JP", source: initialDocumentScript,
    });
    const browserVersion = await browser.browserVersion();
    writeJSON(resolve(output, "browser-version.json"), browserVersion);
    const record = async (name: string) => {
      await paintBarrier(browser);
      await browser.waitForRequestHandlersIdle();
      assert.deepEqual(fixture.unexpected, [], `${name}: unfixtureed API or external request`);
      browser.assertNoFatalError();
      assert.equal(browser.consoleErrorCount, 0, `${name}: browser console errors`);
      const observation = await browser.evaluate<Record<string, unknown>>(observationExpression);
      assert.ok(typeof observation.text === "string" && observation.text.length > 20, `${name}: empty product page`);
      assert.equal(observation.hiddenDiagnostic, false, `${name}: diagnostic disclosure`);
      assert.equal(observation.secretLeak, false, `${name}: concealed/disposed secret must not leak`);
      const evidence = {
        ...observation,
        api: apiObservationSummary(fixture.trace),
        statuses: [...browser.responseStatuses].filter(([path]) => fixture.trace.some((request) => new URL(request.path, baseURL).pathname === path)).sort(([a], [b]) => a.localeCompare(b, "en")),
        navigations: browser.navigationCount,
        consoleErrors: browser.consoleErrorCount,
      };
      const png = await browser.captureScreenshot();
      writeFileSync(resolve(output, `${name}.png`), png, { flag: "wx" });
      writeJSON(resolve(output, `${name}.json`), evidence);
      captures.push({ name, observation: evidence, pngSHA256: sha256(png) });
    };
    const navigate = async (surfaceID: (typeof bundle9BrowserSurfaces)[number]["id"], text?: string, prepareNextPhase?: () => void) => {
      const surface = bundle9BrowserSurfaces.find((candidate) => candidate.id === surfaceID)!;
      await navigateBundle9Document(browser, fixture, `${baseURL}${surface.route}`, prepareNextPhase);
      await browser.waitFor("Boolean(document.querySelector('main') && document.querySelector('button[aria-label=\"アカウントメニュー\"]'))", Boolean, `${surfaceID}: protected shell`);
      for (const path of ["/auth/me", "/settings/app", "/version", ...surface.required]) {
        await browser.waitForResponseCount(path, 1);
      }
      await browser.waitFor("document.querySelector('main')?.textContent || ''", (value: string) => value.includes(text ?? surface.text), `${surfaceID}: product result did not render`);
      assert.equal(await browser.evaluate("location.pathname + location.search + location.hash"), surface.route, `${surfaceID}: URL/query/hash preservation`);
      await paintBarrier(browser);
      await browser.waitForRequestHandlersIdle();
    };
    const scenario = async (name: string, execute: () => Promise<void>) => {
      try { await execute(); }
      catch (error) {
        // Preserve the failure and continue independent observations; the
        // denominator and source result cannot become PASS after any failure.
        failures.push({ name, error: error instanceof Error ? error.message : String(error) });
        writeJSON(resolve(output, `${name}.failure.json`), failures.at(-1));
        browser.assertNoFatalError();
      }
    };

    for (const surface of bundle9BrowserSurfaces) {
      for (const viewport of bundle9Viewports) {
        await scenario(`${surface.id}-${viewport.width}`, async () => {
          await browser.setViewport(viewport.width, viewport.height);
          await navigate(surface.id, undefined, () => { fixture.state.permissions = ["*"]; });
          assert.equal(fixture.trace.filter((call) => call.method === "GET" && call.path === "/auth/me").length, 1, "fresh document shares auth query");
          for (const path of surface.required) assert.equal(fixture.trace.filter((call) => call.method === "GET" && call.path === path).length, 1, `${surface.id}: shared GET ${path}`);
          await record(`${surface.id}-${viewport.width}`);
        });
      }
    }
    await browser.setViewport(1440, 900);
    await scenario("streams-interactions", async () => {
      await navigate("streams", undefined, () => { fixture.state.permissions = ["*"]; });
      await rememberFocusTarget(browser, bundle9ReadinessSelector);
      await browser.clickSelector(bundle9ReadinessSelector);
      await waitForDialog(browser, true);
      assert.equal(fixture.trace.filter((call) => call.path === bundle9ReadinessPath).length, 0, "confirmation does not submit");
      await record("streams-confirmation");
      await browser.pressNativeKey("Escape");
      await waitForDialog(browser, false);
      await waitForExactFocus(browser);
      await record("streams-cancel-focus");
      await browser.clickSelector(bundle9ReadinessSelector);
      await waitForDialog(browser, true);
      await confirmOnce(browser);
      await browser.waitForResponseCount(bundle9ReadinessPath, 1);
      await browser.waitFor("document.querySelector('main')?.textContent || ''", (value: string) => value.includes("開始準備確認を受け付けました"), "readiness completion");
      await browser.waitForRequestHandlersIdle();
      assert.deepEqual(fixture.trace.filter((call) => call.path === bundle9ReadinessPath), [{ method: "POST", path: bundle9ReadinessPath, body: null, status: 200 }], "one readiness POST with unchanged body");
      await record("streams-one-mutation");
    });
    await scenario("streams-error", async () => {
      try {
        await navigate("streams", undefined, () => { fixture.state.readinessError = true; });
        await browser.clickSelector(bundle9ReadinessSelector);
        await waitForDialog(browser, true);
        await confirmOnce(browser);
        await browser.waitForResponseCount(bundle9ReadinessPath, 1);
        await browser.waitFor("document.body.textContent || ''", (value: string) => value.includes("再送せず"), "readiness safe error");
        await browser.waitForRequestHandlersIdle();
        assert.equal(fixture.trace.filter((call) => call.path === bundle9ReadinessPath).length, 1, "403 must not resend");
        await record("streams-error");
      } finally { await paintBarrier(browser); fixture.state.readinessError = false; }
    });
    await scenario("streams-permission-denied", async () => {
      try {
        await navigate("streams", undefined, () => { fixture.state.permissions = ["streams.read"]; });
        assert.equal(await browser.evaluate(`document.querySelector(${JSON.stringify(bundle9ReadinessSelector)})?.disabled`), true);
        await browser.clickSelector(bundle9ReadinessSelector);
        await paintBarrier(browser);
        assert.equal(fixture.trace.some((call) => call.path === bundle9ReadinessPath), false);
        await record("streams-permission-denied");
      } finally { await paintBarrier(browser); fixture.state.permissions = ["*"]; }
    });
    await scenario("streams-detail-preview", async () => {
      await navigate("streams");
      await rememberFocusTarget(browser, 'button[aria-label="詳細"]');
      await browser.clickSelector('button[aria-label="詳細"]');
      await waitForDialog(browser, true);
      await browser.waitForResponseCount(`/streams/${bundle9Stream.id}/visual-settings`, 1);
      await browser.waitForResponseCount(`/streams/${bundle9Stream.id}/video-cover-state`, 1);
      // A ready (not live) stream owns no media element or preview transport.
      assert.equal(await browser.evaluate("document.querySelectorAll('video,audio').length"), 0);
      assert.equal(fixture.trace.some((call) => call.path.includes("preview")), false);
      await record("streams-detail-preview");
      await browser.pressNativeKey("Escape");
      await waitForDialog(browser, false);
      assert.equal(await browser.evaluate("document.querySelectorAll('video,audio').length"), 0);
      await record("streams-detail-closed");
    });
    await scenario("resources-editor", async () => {
      await navigate("resources");
      const selector = 'button[aria-label="B9 Encoder Profile を編集"]';
      await rememberFocusTarget(browser, selector);
      await browser.clickSelector(selector);
      await waitForDialog(browser, true);
      await record("resources-editor");
      await browser.pressNativeKey("Escape");
      await waitForDialog(browser, false);
      await waitForExactFocus(browser);
      assertBundle9CancelledMutations(fixture.trace);
      await record("resources-cancel-focus");
    });
    await scenario("resources-permission-denied", async () => {
      try {
        await navigate("resources", undefined, () => { fixture.state.permissions = ["encoder_profiles.read"]; });
        assert.equal(await browser.evaluate("[...document.querySelectorAll('button')].filter((button) => button.textContent.trim() === '新規作成' && !button.disabled).length"), 0);
        await record("resources-permission-denied");
      } finally { await paintBarrier(browser); fixture.state.permissions = ["*"]; }
    });
    await scenario("updater-settings", async () => {
      await navigate("application");
      const selector = 'button[aria-label="B9 Host Agent の設定"]';
      await rememberFocusTarget(browser, selector);
      await browser.clickSelector(selector);
      await waitForDialog(browser, true);
      await browser.waitForResponseCount("/system-updates/updaters/host-agent-main/settings", 1);
      await browser.waitFor("document.body.textContent || ''", (value: string) => value.includes("更新実行権限の切替"), "pull ownership settings");
      await record("updater-settings");
      await browser.pressNativeKey("Escape");
      await waitForDialog(browser, false);
      await waitForExactFocus(browser);
      assertBundle9CancelledMutations(fixture.trace);
      await record("updater-cancel-focus");
    });
    await scenario("account-secret-owner", async () => {
      await navigate("account");
      await browser.clickRoleWithText("tab", "セキュリティ");
      await browser.waitFor("document.body.textContent || ''", (value: string) => value.includes("TOTP登録を開始"), "account security tab");
      await record("account-security");
      await clickButtonText(browser, "TOTP登録を開始");
      await waitForDialog(browser, true);
      await confirmOnce(browser);
      await browser.waitForResponseCount("/auth/mfa/enroll", 1);
      await browser.waitFor("Boolean(document.querySelector('[data-one-time-secret-reveal]'))", Boolean, "MFA result is concealed");
      assert.equal(await browser.evaluate("document.querySelectorAll('[data-one-time-secret-content]').length"), 0);
      await record("account-secret-concealed");
      await browser.clickRoleWithText("tab", "プロフィール");
      await browser.waitFor("document.querySelectorAll('[data-one-time-secret-root]').length", (value: number) => value === 0, "profile tab unmounts secret owner");
      await browser.clickRoleWithText("tab", "セキュリティ");
      await browser.waitFor("document.querySelectorAll('[data-one-time-secret-root]').length", (value: number) => value === 0, "unmounted secret owner disposed");
      assert.equal(fixture.trace.filter((call) => call.path === "/auth/mfa/enroll").length, 1);
      await record("account-secret-disposed");
    });
    await scenario("archive-local", async () => {
      await navigate("archive");
      await browser.clickRoleWithText("tab", "ローカル録画アーカイブ");
      await browser.waitFor("document.body.textContent || ''", (value: string) => value.includes("ローカル録画アーカイブはまだありません。"), "archive empty state");
      await record("archive-local");
    });
    await scenario("archive-permission-denied", async () => {
      try {
        // The default profile remains readable; archive queries must stay disabled.
        await navigateBundle9Document(browser, fixture, `${baseURL}${bundle9BrowserSurfaces.find((surface) => surface.id === "archive")!.route}`, () => { fixture.state.permissions = ["archive_profiles.read"]; });
        await browser.waitForResponseCount("/profiles/archive", 1);
        await browser.clickRoleWithText("tab", "ローカル録画アーカイブ");
        await browser.waitFor("document.body.textContent || ''", (value: string) => value.includes("録画成果物を確認する権限がありません"), "archive permission notice");
        await browser.waitForRequestHandlersIdle();
        assert.equal(fixture.trace.some((call) => call.path.startsWith("/archive/streams") || call.path.startsWith("/archive/processing-streams")), false);
        await record("archive-permission-denied");
      } finally { await paintBarrier(browser); fixture.state.permissions = ["*"]; }
    });
    await scenario("monitoring-error-and-recovery", async () => {
      try {
        await navigate("monitoring", "一部の情報を取得できません", () => { fixture.state.healthError = true; });
        await retryBundle9Monitoring(browser, fixture, record);
      } finally { await paintBarrier(browser); fixture.state.healthError = false; }
    });
    await scenario("mobile-navigation", async () => {
      await browser.setViewport(390, 844);
      await navigate("streams");
      const selector = 'button[aria-label="ナビゲーションを開く"]';
      await rememberFocusTarget(browser, selector);
      await browser.clickSelector(selector);
      await browser.waitFor("Boolean(document.querySelector('.mobile-navigation-sheet[data-state=\"open\"]'))", Boolean, "mobile navigation open");
      await record("mobile-navigation");
      await browser.pressNativeKey("Escape");
      await browser.waitFor("document.querySelectorAll('.mobile-navigation-sheet[data-state=\"open\"]').length", (value: number) => value === 0, "mobile navigation closed");
      await waitForExactFocus(browser);
      await record("mobile-navigation-closed");
    });

    assert.equal(failures.length, 0, `Bundle 9 browser scenarios failed: ${failures.map((failure) => failure.name).join(", ")}`);
    assertCaptureInventory(captures.map((capture) => capture.name));
    writeJSON(resolve(output, "captures.json"), captures);
    writeJSON(resolve(output, "execution.json"), { status: "pass", captures: captures.length, failed: 0, skipped: 0, cancelled: 0, browserVersion });
    return { captures, browserVersion };
  } catch (error) {
    writeJSON(resolve(output, "execution.json"), { status: "fail", captures: captures.length, failures, skipped: 0, cancelled: 0 });
    throw error;
  } finally { await browser.close(); }
}

const initialDocumentScript = `(() => {
  if (location.protocol !== 'http:') return;
  const NativeDate = Date;
  const epoch = ${Date.parse(BUNDLE9_BROWSER_CLOCK)};
  class FixedDate extends NativeDate {
    constructor(...args) { if (args.length === 0) super(epoch); else super(...args); }
    static now() { return epoch; }
  }
  globalThis.Date = FixedDate;
  localStorage.setItem('autostream.controlPanel.locale', 'ja');
  localStorage.setItem('autostream.ui_preference', JSON.stringify({ theme_id: 'autostream', color_mode: 'light' }));
  sessionStorage.setItem('autostream.csrf_token', 'bundle9-synthetic-csrf');
})()`;

const observationExpression = `(() => {
  const visible = (element) => element instanceof HTMLElement && element.getClientRects().length > 0;
  const describe = (element) => element instanceof HTMLElement ? {
    tag: element.tagName.toLowerCase(), role: element.getAttribute('role') || '',
    label: element.getAttribute('aria-label') || '', text: element.textContent.trim(),
  } : null;
  const controls = [...document.querySelectorAll('a,button,input,textarea,select,[role="tab"]')].filter(visible).map((element) => ({
    ...describe(element), disabled: 'disabled' in element ? element.disabled : false,
    checked: element.getAttribute('aria-checked'), selected: element.getAttribute('aria-selected'),
    expanded: element.getAttribute('aria-expanded'), href: element.getAttribute('href'),
    value: element instanceof HTMLInputElement || element instanceof HTMLTextAreaElement || element instanceof HTMLSelectElement ? element.value : null,
  }));
  const markup = document.documentElement.outerHTML;
  const stores = JSON.stringify({ local: { ...localStorage }, session: { ...sessionStorage } });
  return {
    route: location.pathname + location.search + location.hash,
    title: document.title, text: document.body.innerText, controls,
    focus: describe(document.activeElement), focusReturnedToExactTrigger: globalThis.__bundle9FocusTarget ? document.activeElement === globalThis.__bundle9FocusTarget : null,
    dialogs: [...document.querySelectorAll('[role="dialog"],[role="alertdialog"]')].filter(visible).map(describe),
    permissionStates: [...document.querySelectorAll('[data-action-availability]')].map((element) => element.getAttribute('data-action-availability')),
    viewport: { width: innerWidth, height: innerHeight, dpr: devicePixelRatio },
    document: { width: document.documentElement.scrollWidth, height: document.documentElement.scrollHeight },
    locale: document.documentElement.lang, colorScheme: getComputedStyle(document.documentElement).colorScheme,
    font: getComputedStyle(document.body).fontFamily,
    media: [...document.querySelectorAll('video,audio')].map((element) => ({ tag: element.tagName, src: element.getAttribute('src'), paused: element.paused })),
    secret: { roots: document.querySelectorAll('[data-one-time-secret-root]').length, concealed: document.querySelectorAll('[data-one-time-secret-reveal]').length, content: document.querySelectorAll('[data-one-time-secret-content]').length },
    hiddenDiagnostic: markup.includes('B9-HIDDEN-DIAGNOSTIC'),
    secretLeak: (markup + stores).includes(${JSON.stringify(bundle9SyntheticMFASecret)}) || (markup + stores).includes('B9-SYNTHETIC-RECOVERY'),
  };
})()`;

async function paintBarrier(browser: BrowserHarness) {
  await browser.waitForRequestHandlersIdle();
  await browser.evaluate(`(async () => {
    await document.fonts.ready;
    await Promise.all([...document.images].map((image) => image.decode().catch(() => { if (image.currentSrc) throw new Error('Product image did not decode'); })));
    await new Promise((done) => requestAnimationFrame(() => requestAnimationFrame(done)));
    const finite = document.getAnimations().filter((animation) => Number.isFinite(animation.effect?.getComputedTiming().endTime));
    await Promise.all(finite.map((animation) => animation.finished.catch(() => undefined)));
    await new Promise((done) => requestAnimationFrame(() => requestAnimationFrame(done)));
    return true;
  })()`);
  await browser.waitForRequestHandlersIdle();
}

async function rememberFocusTarget(browser: BrowserHarness, selector: string) {
  await browser.evaluate(`(() => { const element = [...document.querySelectorAll(${JSON.stringify(selector)})].find((item) => item instanceof HTMLElement && item.getClientRects().length > 0); if (!element) throw new Error('Missing focus trigger'); element.scrollIntoView({ block: 'center' }); element.focus(); globalThis.__bundle9FocusTarget = element; return true; })()`);
}
async function waitForExactFocus(browser: BrowserHarness) {
  await browser.waitFor("Boolean(globalThis.__bundle9FocusTarget && document.activeElement === globalThis.__bundle9FocusTarget)", Boolean, "focus did not return to the exact triggering element");
}
async function waitForDialog(browser: BrowserHarness, open: boolean) {
  await browser.waitFor("[...document.querySelectorAll('[role=\"dialog\"],[role=\"alertdialog\"]')].some((element) => element.getClientRects().length > 0)", (value: boolean) => value === open, `dialog ${open ? "open" : "closed"}`);
}
async function clickButtonText(browser: BrowserHarness, text: string) {
  const selector = await browser.evaluate<string>(`(() => { const element = [...document.querySelectorAll('button')].find((button) => button.getClientRects().length > 0 && button.textContent.trim() === ${JSON.stringify(text)} && !button.disabled); if (!element) throw new Error('Missing named button'); element.setAttribute('data-bundle9-button', 'target'); element.scrollIntoView({ block: 'center' }); return '[data-bundle9-button="target"]'; })()`);
  await browser.clickSelector(selector);
  await browser.evaluate("document.querySelector('[data-bundle9-button]')?.removeAttribute('data-bundle9-button'); true");
}
async function confirmOnce(browser: BrowserHarness) {
  const typed = await browser.evaluate("Boolean(document.querySelector('[data-confirmation-token-input]'))");
  if (typed) await browser.fillSelector("[data-confirmation-token-input]", "ui-foundation-admin");
  await browser.waitFor("Boolean(document.querySelector('[data-confirm-action]:not([disabled])'))", Boolean, "confirmation must become available");
  // Synchronous native DOM clicks exercise the existing single-flight guard.
  await browser.evaluate("(() => { const button = document.querySelector('[data-confirm-action]'); button.click(); button.click(); return true; })()");
}
function writeJSON(path: string, value: unknown) { writeFileSync(path, `${JSON.stringify(value, null, 2)}\n`, { flag: "wx" }); }
