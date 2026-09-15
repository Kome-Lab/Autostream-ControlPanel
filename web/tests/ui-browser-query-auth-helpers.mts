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

export const mobileFocusDiagnosticExpression = `(() => {
  const target=globalThis.__uiReturnTrigger,active=document.activeElement,rect=target?.getBoundingClientRect();
  return {connected:target?.isConnected===true,disabled:!!target?.disabled||target?.getAttribute('aria-disabled')==='true',inert:!!target?.closest('[inert]'),
    active:active===target?'opening':active===document.body?'body':active?.closest('[role=dialog]')?'dialog':'other',
    dialogs:document.querySelectorAll('[role=dialog]').length,guards:document.querySelectorAll('[data-radix-focus-guard]').length,
    rect:rect?[rect.left,rect.top,rect.width,rect.height]:[]};
})()`;
type MobileFocusDiagnostic = { connected:boolean; disabled:boolean; inert:boolean; active:string; dialogs:number; guards:number; rect:number[] };
export async function closeCreateAndAssertFocusReturn(browser: BrowserHarness, triggerLabel: string, route: "same-route"|"cross-route" = "same-route", write: (line:string)=>void = console.log) {
  const observations: {phase:string;value:MobileFocusDiagnostic}[]=[],diagnosticErrors:unknown[]=[];
  let phase="before-close";
  const observe=async()=>{try{observations.push({phase,value:await browser.evaluate<MobileFocusDiagnostic>(mobileFocusDiagnosticExpression)});}catch(error){diagnosticErrors.push(error);}};
  try {
  await observe();
  assert.equal(await browser.evaluate("globalThis.__uiReturnTrigger?.isConnected===true"), true, "actual initiating Menu target must remain connected");
  await browser.pressKey("Escape");
  await browser.waitFor("document.querySelectorAll('[role=dialog]').length", (value: number) => value === 0, "Escape did not close create dialog");
  phase="dialog-closed";await observe();
  await browser.waitFor(
    `document.activeElement===globalThis.__uiReturnTrigger && document.activeElement?.getAttribute('aria-label')===${JSON.stringify(triggerLabel)} && document.activeElement.getClientRects().length>0`,
    Boolean,
    "focus did not return to the mobile navigation trigger",
  );
  phase="focus-restored";await observe();
  await browser.evaluate("delete globalThis.__uiReturnTrigger;true");
  if(diagnosticErrors.length)throw new AggregateError(diagnosticErrors,"Mobile diagnostic observation failed");
  } catch(original) {
    await observe();
    try {
      const count=(n:number)=>Number.isFinite(n)?Math.min(1024,Math.max(0,Math.floor(n))):null;
      const payload={schemaVersion:1,code:"D013-MOBILE",route:route==="cross-route"?"cross-route":"same-route",phase,
        observations:observations.slice(0,4).map(({phase:step,value:v})=>({phase:step,connected:v.connected===true,disabled:v.disabled===true,inert:v.inert===true,
          active:["opening","body","dialog"].includes(v.active)?v.active:"other",dialogs:count(v.dialogs),guards:count(v.guards),rect:v.rect.slice(0,4).map(n=>Number.isFinite(n)?Math.max(-100000,Math.min(100000,Math.round(n))):null)})),diagnosticFailed:diagnosticErrors.length>0};
      const json=JSON.stringify(payload);assert.ok(Buffer.byteLength(json)<=4096);write("UI_BROWSER_DIAGNOSTIC_013 "+json);
    }catch(error){diagnosticErrors.push(error);}
    if(diagnosticErrors.length)throw new AggregateError([original,...diagnosticErrors],"Mobile failure and diagnostic failure",{cause:original});
    throw original;
  }
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
