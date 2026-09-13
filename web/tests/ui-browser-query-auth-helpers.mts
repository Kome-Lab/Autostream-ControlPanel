import assert from "node:assert/strict";
import { BrowserHarness } from "./helpers/browser-harness.mts";
import { csrfStorageKey, startReadinessPath } from "./ui-browser-fixture.mts";



export type StatusSnapshot = { health: string; update: string };
export type CreateSnapshot = {
  dialogCount: number;
  navigationOpen: boolean;
  createOpen: boolean;
  focusInsideCreate: boolean;
  activeTag: string;
  url: string;
};
export type MotionSnapshot = { animationName: string; animationDuration: string; transitionDuration: string };
export type EnglishShellSnapshot = { url: string; text: string; closeLabel: string; navigationLabel: string; overflow: number; triggerLabel: string };
export type FocusSnapshot = { focused: boolean; outlineStyle: string; outlineWidth: string; outlineColor: string; boxShadow: string };
export type LogoutBrowserSnapshot = {
  pathname: string;
  protectedAdminLandmarkPresent: boolean;
  accountMenuPresent: boolean;
  csrfTokenPresent: boolean;
};
export type SessionExpirySnapshot = {
  href: string;
  pathname: string;
  reason: string | null;
  redirectAfter: string | null;
  csrfTokenPresent: boolean;
  protectedAdminLandmarkPresent: boolean;
};
export type AuthMeExpiryBrowserSnapshot = {
  accountMenuPresent: boolean;
  csrfTokenPresent: boolean;
  href: string;
  pathname: string;
  protectedAdminLandmarkPresent: boolean;
  reason: string | null;
  returnParameterValues: string[];
};

export const statusSnapshotExpression = `(() => {
  const health = [...document.querySelectorAll('a[href="/admin/service-health/"].semantic-status-focus')].find((element) => element.getClientRects().length > 0);
  const update = [...document.querySelectorAll('[role="status"]')].find((element) => element.getClientRects().length > 0);
  return { health: health?.getAttribute('aria-label') || '', update: update?.textContent?.trim() || '' };
})()`;

export const createSnapshotExpression = `(() => {
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

export const focusSnapshotExpression = `(() => {
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

export const desktopNavigationSnapshotExpression = navigationSnapshotExpression("aside nav");
export const mobileNavigationSnapshotExpression = navigationSnapshotExpression(".mobile-navigation-sheet nav");

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

export const logoutSnapshotExpression = `(() => ({
  pathname: location.pathname,
  protectedAdminLandmarkPresent: Boolean(document.querySelector('nav[aria-label="Admin navigation"]')),
  accountMenuPresent: Boolean(document.querySelector('button[aria-label="Account menu"]')),
  csrfTokenPresent: sessionStorage.getItem(${JSON.stringify(csrfStorageKey)}) !== null,
}))()`;

export const sessionExpirySnapshotExpression = `(() => {
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

export function authMeExpirySnapshotExpression(returnParameterName: string) {
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

export async function waitForStartReadinessHandlersIdle(browser: BrowserHarness) {
  await browser.waitForRequestHandlersIdle({ pathname: startReadinessPath, method: "POST" });
  await browser.waitForRequestHandlersIdle({ pathname: "/streams", method: "GET" });
  browser.assertNoFatalError();
}

export async function waitForStatus(browser: BrowserHarness, health: string, update: string) {
  return browser.waitFor(
    statusSnapshotExpression,
    (value: StatusSnapshot) => value.health === health && value.update === update,
    `status did not become ${health} / ${update}`,
    15_000,
  );
}

export function assertStatusSnapshot(actual: StatusSnapshot, expected: StatusSnapshot) {
  assert.equal(actual.health, expected.health, "health status projection");
  assert.equal(actual.update, expected.update, "update status projection");
}

export function assertCreateOutcome(snapshot: CreateSnapshot) {
  assert.equal(snapshot.dialogCount, 1, "create flow must leave exactly one dialog");
  assert.equal(snapshot.navigationOpen, false, "navigation Sheet must release its focus owner");
  assert.equal(snapshot.createOpen, true, "create dialog must be open");
  assert.equal(snapshot.focusInsideCreate, true, "focus must enter the create dialog");
  assert.match(snapshot.activeTag, /^(?:INPUT|BUTTON|TEXTAREA|SELECT)$/);
}

export async function closeCreateAndAssertFocusReturn(browser: BrowserHarness, triggerLabel: string) {
  await browser.pressKey("Escape");
  await browser.waitFor("document.querySelectorAll('[role=dialog]').length", (value: number) => value === 0, "Escape did not close create dialog");
  await browser.waitFor(
    "document.activeElement?.getAttribute('aria-label')",
    (value: string | null) => value === triggerLabel,
    "focus did not return to the mobile navigation trigger",
  );
}

export async function waitForShell(browser: BrowserHarness, accessibleName: string) {
  await browser.waitFor(
    `Boolean(document.querySelector('button[aria-label=${JSON.stringify(accessibleName)}]'))`,
    Boolean,
    `shell control ${accessibleName} did not render`,
  );
}

export async function triggerReconnect(browser: BrowserHarness) {
  await browser.evaluate("window.dispatchEvent(new Event('offline')); window.dispatchEvent(new Event('online')); true");
}
