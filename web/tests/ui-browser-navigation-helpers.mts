import assert from "node:assert/strict";
import { BrowserHarness } from "./helpers/browser-harness.mts";
import { localeStorageKey, themeStorageKey } from "./ui-browser-fixture.mts";
import { type MotionSnapshot } from "./ui-browser-query-auth-helpers.mts";


export async function expandNavigationSections(browser: BrowserHarness) {
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

export async function setStoredDisplay(browser: BrowserHarness, locale: "ja" | "en", theme: "light" | "dark") {
  await browser.evaluate(`localStorage.setItem(${JSON.stringify(localeStorageKey)}, ${JSON.stringify(locale)}); localStorage.setItem(${JSON.stringify(themeStorageKey)}, ${JSON.stringify(JSON.stringify({ theme_id: "autostream", color_mode: theme }))}); true`);
}

export async function sheetMotion(browser: BrowserHarness) {
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

export async function waitForSheetSettled(browser: BrowserHarness) {
  await browser.waitFor(
    `(() => { const sheet = document.querySelector('.mobile-navigation-sheet'); if (!(sheet instanceof HTMLElement)) return false; const rect = sheet.getBoundingClientRect(); return rect.left >= -1 && rect.right > rect.left; })()`,
    Boolean,
    "mobile navigation did not finish entering",
  );
}

export async function waitForAnimationFrames(browser: BrowserHarness) {
  await browser.evaluate("new Promise((resolve) => requestAnimationFrame(() => requestAnimationFrame(() => resolve(true))))");
}

export async function scrollSelectorIntoView(browser: BrowserHarness, selector: string) {
  const found = await browser.evaluate<boolean>(`(() => {
    const element = document.querySelector(${JSON.stringify(selector)});
    if (!(element instanceof HTMLElement)) return false;
    element.scrollIntoView({ block: 'center', inline: 'nearest' });
    return true;
  })()`);
  assert.equal(found, true, `could not scroll action into view: ${selector}`);
  await waitForAnimationFrames(browser);
}

export function deferred() {
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

export function seconds(value: string) {
  return Math.max(...value.split(",").map((part) => {
    const normalized = part.trim();
    return normalized.endsWith("ms") ? Number.parseFloat(normalized) / 1_000 : Number.parseFloat(normalized) || 0;
  }));
}
