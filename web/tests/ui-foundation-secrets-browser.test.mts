import assert from "node:assert/strict";
import test from "node:test";

import type { BrowserHarness } from "./helpers/browser-harness.mts";
import { withSecretBrowserFixture } from "./helpers/ui-foundation-secret-browser-harness.mts";

const primaryMarker = "B06-BROWSER-SECRET-64c5de";
const replacementMarker = "B06-BROWSER-REPLACEMENT-f5ae13";

test("one-time secret browser boundary preserves controlled reveal, focus, cleanup, and leakage invariants", { timeout: 180_000 }, async () => {
  await withSecretBrowserFixture(async ({ baseUrl, browser }) => {
    await browser.setViewport(1100, 850);
    browser.clearConsoleErrors();

    await browser.navigate(`${baseUrl}/?scenario=default&locale=en`);
    await waitForPhase(browser, "concealed");
    assert.equal(await text(browser, "[data-testid=unmount-count]"), "0", "Strict Mode effect probing must not dispose an active owner");
    assert.equal(await documentContains(browser, primaryMarker), false);
    assert.equal(await browser.evaluate("document.querySelector('[data-one-time-secret-copy]') === null"), true);
    await browser.clickSelector("[data-one-time-secret-reveal]");
    await browser.waitFor("document.querySelector('[data-secret-marker]')?.textContent || ''", (value: string) => value === primaryMarker, "Reveal exposes marker");
    assert.equal(await browser.evaluate("document.activeElement?.hasAttribute('data-one-time-secret-conceal')"), true);
    assert.deepEqual(await automaticSurfaceHits(browser), []);
    await browser.clickSelector("[data-one-time-secret-conceal]");
    await browser.waitFor("document.querySelector('[data-secret-marker]') === null", Boolean, "Conceal removes marker");
    assert.equal(await browser.evaluate("document.activeElement?.hasAttribute('data-one-time-secret-reveal')"), true);

    await browser.clickSelector("[data-one-time-secret-reveal]");
    await browser.clickSelector("[data-one-time-secret-copy]");
    await waitForPhase(browser, "copied");
    assert.equal(await text(browser, "[data-testid=copy-writer-count]"), "1");
    assert.equal(await browser.evaluate("document.activeElement?.hasAttribute('data-one-time-secret-copy')"), true);
    assert.match(await text(browser, "[data-one-time-secret-copy-status]"), /Copied/);
    assert.deepEqual(await automaticSurfaceHits(browser), []);

    await browser.navigate(`${baseUrl}/?scenario=copy-failure&locale=en`);
    await waitForPhase(browser, "concealed");
    await browser.clickSelector("[data-one-time-secret-reveal]");
    await browser.clickSelector("[data-one-time-secret-copy]");
    await browser.waitFor("document.querySelector('[data-one-time-secret-copy-status]')?.textContent || ''", (value: string) => /could not be copied/.test(value), "generic copy failure");
    assert.equal(await text(browser, "[data-testid=phase]"), "revealed");
    assert.equal(await text(browser, "[data-testid=copy-writer-count]"), "1");
    assert.equal(await browser.evaluate("document.activeElement?.hasAttribute('data-one-time-secret-copy')"), true);
    assert.equal((await text(browser, "[data-one-time-secret-copy-status]")).includes(primaryMarker), false);
    assert.deepEqual(await automaticSurfaceHits(browser), []);
    assert.equal(browser.consoleErrorCount, 0);

    await browser.navigate(`${baseUrl}/?scenario=default&locale=en`);
    await waitForPhase(browser, "concealed");
    await browser.clickSelector("[data-one-time-secret-reveal]");
    await browser.evaluate("document.querySelector('#fixture-focus-anchor')?.focus(); document.querySelector('#advance-warning')?.click(); true");
    await browser.waitFor("document.querySelector('[data-one-time-secret-warning]')?.textContent || ''", (value: string) => /cleared automatically soon/.test(value), "warning at T-60");
    assert.equal(await browser.evaluate("document.activeElement?.id"), "fixture-focus-anchor", "warning does not move focus");
    assert.equal(await documentContains(browser, primaryMarker), true);
    assert.deepEqual(await automaticSurfaceHits(browser), []);
    await browser.evaluate("document.querySelector('#advance-expiry')?.click(); true");
    await waitForPhase(browser, "cleared");
    assert.equal(await text(browser, "[data-testid=reason]"), "expired");
    assert.equal(await documentContains(browser, primaryMarker), false);
    assert.equal(await browser.evaluate("document.activeElement?.id"), "fixture-focus-anchor", "expiry does not move focus");
    assert.equal(await text(browser, "[data-testid=pending-count]"), "0");

    await browser.navigate(`${baseUrl}/?scenario=default&locale=en`);
    await waitForPhase(browser, "concealed");
    await browser.clickSelector("[data-one-time-secret-reveal]");
    await browser.clickSelector("[data-one-time-secret-acknowledge]");
    await waitForPhase(browser, "acknowledged");
    assert.equal(await text(browser, "[data-testid=acknowledge-count]"), "1");
    assert.equal(await documentContains(browser, primaryMarker), false);
    assert.equal(await browser.evaluate("document.querySelector('[data-one-time-secret-reveal]') === null"), true);
    assert.equal(await browser.evaluate("document.querySelector('[data-one-time-secret-copy]') === null"), true);

    await browser.navigate(`${baseUrl}/?scenario=default&locale=en`);
    await waitForPhase(browser, "concealed");
    await browser.clickSelector("[data-one-time-secret-reveal]");
    await browser.clickSelector("[data-one-time-secret-dismiss]");
    await waitForPhase(browser, "cleared");
    assert.equal(await text(browser, "[data-testid=reason]"), "dismissed");
    assert.equal(await text(browser, "[data-testid=dismiss-count]"), "1");
    assert.equal(await documentContains(browser, primaryMarker), false);

    for (const [control, reason] of [["navigation-clear", "navigation"], ["session-clear", "session-lost"]] as const) {
      await browser.navigate(`${baseUrl}/?scenario=default&locale=en`);
      await waitForPhase(browser, "concealed");
      await browser.clickSelector("[data-one-time-secret-reveal]");
      await browser.evaluate(`document.querySelector('#${control}')?.click(); true`);
      await waitForPhase(browser, "cleared");
      assert.equal(await text(browser, "[data-testid=reason]"), reason);
      assert.equal(await documentContains(browser, primaryMarker), false);
      assert.equal(await text(browser, "[data-testid=pending-count]"), "0");
    }

    await browser.navigate(`${baseUrl}/?scenario=default&locale=en`);
    await waitForPhase(browser, "concealed");
    await browser.clickSelector("[data-one-time-secret-reveal]");
    await browser.evaluate("document.querySelector('#replace-source')?.click(); true");
    await waitForPhase(browser, "concealed");
    assert.equal(await text(browser, "[data-testid=generation]"), "2");
    assert.equal(await documentContains(browser, primaryMarker), false);
    await browser.clickSelector("[data-one-time-secret-reveal]");
    await browser.waitFor("document.querySelector('[data-secret-marker]')?.textContent || ''", (value: string) => value === replacementMarker, "replacement source reveals");
    assert.equal(await documentContains(browser, primaryMarker), false);
    assert.deepEqual(await automaticSurfaceHits(browser), []);

    await browser.navigate(`${baseUrl}/?scenario=default&locale=en`);
    await waitForPhase(browser, "concealed");
    assert.equal(await text(browser, "[data-testid=unmount-count]"), "0");
    await browser.clickSelector("[data-one-time-secret-reveal]");
    await browser.evaluate("document.querySelector('#unmount-reveal')?.click(); true");
    await browser.waitFor("document.querySelector('[data-one-time-secret-root]') === null", Boolean, "component unmounted");
    await browser.waitFor("document.querySelector('[data-testid=unmount-count]')?.textContent || ''", (value: string) => value === "1", "unmount callback exactly once");
    assert.equal(await text(browser, "[data-testid=reason]"), "unmounted");
    assert.equal(await text(browser, "[data-testid=pending-count]"), "0");
    assert.equal(await documentContains(browser, primaryMarker), false);

    await browser.navigate(`${baseUrl}/?scenario=default&locale=ja`);
    await waitForPhase(browser, "concealed");
    assert.equal(await text(browser, "[data-one-time-secret-reveal]"), "表示");
    await browser.evaluate("document.querySelector('[data-one-time-secret-reveal]')?.focus(); true");
    assert.equal(await browser.evaluate("document.activeElement?.hasAttribute('data-one-time-secret-reveal')"), true);
    await browser.pressKey(" ", "Space");
    await browser.waitFor("document.querySelector('[data-secret-marker]')?.textContent || ''", (value: string) => value === primaryMarker, "keyboard Reveal");
    assert.equal(await browser.evaluate("document.activeElement?.hasAttribute('data-one-time-secret-conceal')"), true);
    assert.equal(await text(browser, "[data-one-time-secret-conceal]"), "隠す");
    assert.equal(await text(browser, "[data-one-time-secret-copy]"), "コピー");

    await browser.setMediaFeatures([
      { name: "prefers-reduced-motion", value: "reduce" },
      { name: "forced-colors", value: "active" },
    ]);
    assert.equal(await browser.evaluate("matchMedia('(prefers-reduced-motion: reduce)').matches"), true);
    assert.equal(await browser.evaluate("matchMedia('(forced-colors: active)').matches"), true);
    assert.equal(await browser.evaluate("document.querySelector('[data-one-time-secret-conceal]') instanceof HTMLButtonElement"), true);
    await browser.clickSelector("[data-one-time-secret-conceal]");
    assert.equal(await browser.evaluate("document.activeElement?.hasAttribute('data-one-time-secret-reveal')"), true);
    await browser.setMediaFeatures([]);

    assert.equal(await text(browser, "[data-testid=leak-hit-count]"), "0");
    assert.equal(await browser.evaluate(`!location.href.includes(${JSON.stringify(primaryMarker)}) && !location.href.includes(${JSON.stringify(replacementMarker)})`), true);
    assert.equal(browser.consoleErrorCount, 0);

    const safeObservation = { attributes: [] as string[], liveText: "Copied.", secretInConcealedDOM: false };
    assert.doesNotThrow(() => assertSafeBrowserObservation(safeObservation));
    assert.throws(() => assertSafeBrowserObservation({ ...safeObservation, attributes: [primaryMarker] }), /automatic surface/);
    assert.throws(() => assertSafeBrowserObservation({ ...safeObservation, liveText: primaryMarker }), /automatic surface/);
    assert.throws(() => assertSafeBrowserObservation({ ...safeObservation, secretInConcealedDOM: true }), /concealed DOM/);
  });
});

async function waitForPhase(browser: BrowserHarness, phase: string) {
  await browser.waitFor(
    "document.querySelector('[data-testid=phase]')?.textContent || ''",
    (value: string) => value === phase,
    `phase ${phase}`,
  );
}

async function text(browser: BrowserHarness, selector: string) {
  return browser.evaluate<string>(`document.querySelector(${JSON.stringify(selector)})?.textContent || ''`);
}

async function documentContains(browser: BrowserHarness, value: string) {
  return browser.evaluate<boolean>(`document.documentElement.textContent?.includes(${JSON.stringify(value)}) || false`);
}

async function automaticSurfaceHits(browser: BrowserHarness) {
  return browser.evaluate<string[]>(`(() => {
    const markers = [${JSON.stringify(primaryMarker)}, ${JSON.stringify(replacementMarker)}];
    const hits = [];
    const includesMarker = (value) => markers.some((marker) => String(value || '').includes(marker));
    for (const element of document.querySelectorAll('*')) {
      for (const attribute of element.attributes) {
        if ((attribute.name.startsWith('aria-') || attribute.name.startsWith('data-') || ['id', 'name', 'title'].includes(attribute.name))
          && includesMarker(attribute.value)) hits.push('attribute:' + attribute.name);
      }
      for (const attributeName of ['aria-labelledby', 'aria-describedby']) {
        const ids = (element.getAttribute(attributeName) || '').split(/\\s+/).filter(Boolean);
        if (ids.some((id) => includesMarker(document.getElementById(id)?.textContent))) hits.push('referenced:' + attributeName);
      }
    }
    for (const region of document.querySelectorAll('[role=status], [aria-live]')) {
      if (includesMarker(region.textContent)) hits.push('live');
    }
    return hits;
  })()`);
}

function assertSafeBrowserObservation(observation: Readonly<{
  attributes: readonly string[];
  liveText: string;
  secretInConcealedDOM: boolean;
}>) {
  assert.equal(observation.attributes.some((value) => value.includes(primaryMarker)), false, "secret in automatic surface");
  assert.equal(observation.liveText.includes(primaryMarker), false, "secret in automatic surface");
  assert.equal(observation.secretInConcealedDOM, false, "secret in concealed DOM");
}
