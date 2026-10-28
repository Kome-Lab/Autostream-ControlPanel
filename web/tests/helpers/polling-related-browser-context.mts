import assert from "node:assert/strict";
import { mkdirSync, writeFileSync } from "node:fs";
import { resolve } from "node:path";
import { BrowserHarness, type StubRequest, type StubResponse } from "./browser-harness.mts";
import { createBrowserRouteFixture, currentUser, healthyRows } from "../ui-browser-fixture.mts";
import { clickVisible } from "../ui-regression/visible-trigger.mts";
import { waitForAnimationFrames } from "../ui-browser-navigation-helpers.mts";

export type RelatedRequest = Readonly<{ method: string; path: string; body?: Record<string, unknown> }>;
export type RelatedCaseResult = { name: string; status: "PASS" | "FAIL" | "NOT_RUN"; error_class?: string;
  request_counts?: Record<string, number>; response_counts?: Record<string, number>; captures?: string[];
  body_started?: boolean; diagnostic_errors?: { phase: string; error_class: string }[] };
type Handler = (request: StubRequest) => StubResponse;
type Gate = { entered: Promise<void>; release: () => void };

export type RelatedBrowserContext = ReturnType<typeof createRelatedBrowserContext>;

export function createRelatedBrowserContext(browser: BrowserHarness, baseURL: string, evidenceDirectory: string) {
  const routes = new Map<string, Handler>();
  const requests: RelatedRequest[] = [];
  const gates = new Map<string, { enter: () => void; wait: Promise<void>; release: () => void }>();
  const gateReleases = new Set<() => void>();
  const cases: RelatedCaseResult[] = [];
  const captures: string[] = [];
  const completedCycles: { name: string; paths: readonly string[]; cycles: number; trigger: string }[] = [];
  const mobileOperations: { name: string; width: number }[] = [];
  const failures: Error[] = [];
  const attemptedNames = new Set<string>();
  let browserReady = true;
  let isolationFailure: Error | undefined;
  let currentCase = "not-started";
  const installRoutes = (): ReturnType<typeof createBrowserRouteFixture> => createBrowserRouteFixture(browser, (request): StubResponse | null => {
    const path = new URL(request.url).pathname.replace(/\/$/, "") || "/";
    if (!/^\/(auth|setup|settings|account|workers|nodes|service-health|system-updates|version|profiles|discord|security|secrets|roles|streams)/.test(path)) return null;
    const key = `${request.method} ${path}`;
    let body: Record<string, unknown> | undefined;
    if (request.postData) { try { body = JSON.parse(request.postData); } catch { /* Non-JSON requests are never copied into evidence. */ } }
    requests.push({ method: request.method, path, ...(body ? { body } : {}) });
    const handler = routes.get(key);
    const gate = gates.get(key);
    if (!handler && !gate) return null;
    const response: StubResponse = handler ? handler(request) : path === "/auth/me" ? fixture.authResponse : { body: [] };
    if (!gate) return response;
    gates.delete(key);
    gate.enter();
    return { ...response, waitUntil: response.waitUntil ? Promise.all([response.waitUntil, gate.wait]).then(() => undefined) : gate.wait };
  });
  let fixture: ReturnType<typeof createBrowserRouteFixture> = installRoutes();
  const control = {
    requests,
    set(method: string, path: string, response: StubResponse) { routes.set(`${method} ${path}`, () => response); },
    handle(method: string, path: string, handler: Handler) { routes.set(`${method} ${path}`, handler); },
    count(method: string, path: string) { return requests.filter((request) => request.method === method && request.path === path).length; },
    gateNext(method: string, path: string): Gate {
      const key = `${method} ${path}`;
      assert.equal(gates.has(key), false, `one owned gate per ${key}`);
      let enter!: () => void, release!: () => void;
      const entered = new Promise<void>((done) => { enter = done; });
      const wait = new Promise<void>((done) => { release = done; });
      gateReleases.add(release);
      gates.set(key, { enter, wait, release });
      return { entered, release };
    },
    reset() {
      for (const release of gateReleases) release();
      gateReleases.clear();
      gates.clear(); routes.clear(); requests.length = 0;
      routes.set("GET /roles", () => ({ body: [] }));
      routes.set("GET /secrets/status", () => ({ body: [] }));
      fixture.authResponse = { body: currentUser };
      fixture.workersResponse = { body: healthyRows };
      fixture.nodesResponse = { body: healthyRows };
      fixture.healthResponse = { body: healthyRows };
      browser.clearRequestCounts();
    },
    releaseAll() { for (const release of gateReleases) release(); gateReleases.clear(); gates.clear(); },
  };
  async function navigate(path: string, readySelector = "main") {
    await browser.navigate(new URL(path, baseURL).href);
    await browser.waitFor(`!!document.querySelector(${JSON.stringify(readySelector)})`, Boolean, `actual ${path} UI did not mount`, 30_000);
    await browser.waitForRequestHandlersIdle({ pathname: "/auth/me", method: "GET" });
  }
  async function click(text: string, scope = "main") {
    const escaped = text.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
    await clickVisible(browser, `${scope} button`, new RegExp(`^${escaped}$`));
  }
  async function fill(selector: string, value: string) {
    await browser.evaluate(`document.querySelector(${JSON.stringify(selector)})?.scrollIntoView({block:'center',inline:'nearest'});true`);
    await browser.fillSelector(selector, value);
    await browser.waitFor(`document.querySelector(${JSON.stringify(selector)})?.value`, (actual) => actual === value, "controlled input did not retain the explicit draft");
  }
  async function readValue(selector: string) {
    return browser.evaluate<string | null>(`document.querySelector(${JSON.stringify(selector)})?.value ?? null`);
  }
  async function settleQueryConsumers() {
    // Fetch.fulfillRequest acknowledgement precedes the page's response.json(),
    // query success notification and React commit. A subsequent focus event can
    // otherwise reuse that query's still-active promise (cancelRefetch:false).
    await waitForAnimationFrames(browser);
    await browser.waitFor(`(() => {
      const workers=document.querySelector('[data-screen-family="workers"]');
      if(!workers)return true;
      const refresh=[...workers.querySelectorAll('button[aria-busy]')].filter(button=>button.textContent.trim().startsWith('Refresh'));
      return refresh.length===1 && refresh[0].getAttribute('aria-busy')==='false';
    })()`, Boolean, "the real Worker query consumer must commit its completed refresh before the next focus event");
    await waitForAnimationFrames(browser);
  }
  async function focusCycle(paths: readonly string[]) {
    assert.ok(paths.length > 0);
    await Promise.all(paths.map((path) => browser.waitForRequestHandlersIdle({ pathname: path, method: "GET" })));
    await settleQueryConsumers();
    const before = paths.map((path) => browser.responses.get(path) || 0);
    // This delivers the browser focus-manager event to the real mounted query
    // owners. It does not call fetch, a queryClient, or a controller in the page.
    await browser.evaluate("document.dispatchEvent(new Event('visibilitychange',{bubbles:true}));true");
    await Promise.all(paths.map((path, index) => browser.waitForResponseCount(path, before[index] + 1, 20_000)));
    await Promise.all(paths.map((path) => browser.waitForRequestHandlersIdle({ pathname: path, method: "GET" })));
    await settleQueryConsumers();
  }
  async function completeCycles(paths: readonly string[], cycles: number) {
    assert.equal(cycles, 3, "targeted polling acceptance requires three completed cycles");
    for (let cycle = 0; cycle < cycles; cycle++) await focusCycle(paths);
    completedCycles.push({ name: currentCase, paths: [...paths], cycles, trigger: "visibilitychange focus-manager; actual query GET response, handler-idle, animation-frame and rendered refresh-completion barriers" });
  }
  async function capture(label: string, focusSelector?: string) {
    assert.match(label, /^[a-z0-9-]+$/);
    mkdirSync(evidenceDirectory, { recursive: true });
    for (const width of [1440, 390, 320]) {
      await browser.setViewport(width, 1000);
      if (focusSelector) await browser.evaluate(`document.querySelector(${JSON.stringify(focusSelector)})?.scrollIntoView({block:'center',inline:'nearest'});true`);
      const snapshot = structuredClone(await browser.evaluate(`({width:innerWidth,viewport:document.documentElement.clientWidth,overflow:Math.max(0,document.documentElement.scrollWidth-document.documentElement.clientWidth),dialogs:document.querySelectorAll('[role=dialog],[role=alertdialog]').length,fields:[...document.querySelectorAll('main input,[role=dialog] input,[role=alertdialog] input')].filter(e=>e.getClientRects().length).map(e=>({type:e.type,id:e.id,disabled:e.disabled,length:e.value.length})),active:{tag:document.activeElement?.tagName||'',id:document.activeElement?.id||''}})`));
      const image = await browser.captureViewportEvidence();
      const stem = `${label}-${width}`;
      writeFileSync(resolve(evidenceDirectory, `${stem}.png`), image.png, { flag: "wx" });
      writeFileSync(resolve(evidenceDirectory, `${stem}.json`), JSON.stringify(snapshot, null, 2), { flag: "wx" });
      captures.push(stem);
    }
    await browser.setViewport(1440, 1000);
  }
  async function operateAt(width: 1440 | 390 | 320, body: () => Promise<void>) {
    await browser.setViewport(width, 1000);
    mobileOperations.push({ name: currentCase, width });
    try { await body(); } finally { await browser.setViewport(1440, 1000); }
  }
  const asError = (error: unknown) => error instanceof Error ? error : new Error("Related browser fixture threw a non-Error value", { cause: error });
  function retainCaseFailure(result: RelatedCaseResult, phase: string, error: unknown) {
    const retained = asError(error);
    failures.push(retained);
    result.status = "FAIL";
    result.error_class ||= retained.name;
    (result.diagnostic_errors ??= []).push({ phase, error_class: retained.name });
    return retained;
  }
  async function closeBrowser() {
    browserReady = false;
    try { await browser.close(); }
    catch (error) { isolationFailure ||= asError(error); throw error; }
  }
  async function captureCaseFailure(result: RelatedCaseResult, label: string) {
    if (!browserReady) return;
    try { await capture(label); }
    catch (error) { retainCaseFailure(result, "failure-capture", error); }
  }
  async function check(name: string, body: () => Promise<void>) {
    const result: RelatedCaseResult = cases.find((item) => item.name === name) ?? { name, status: "NOT_RUN" };
    if (!cases.includes(result)) cases.push(result);
    assert.equal(result.status, "NOT_RUN", "named acceptance case executes once");
    assert.equal(attemptedNames.has(name), false, "each named case receives one isolated execution attempt");
    attemptedNames.add(name);
    result.body_started = false;
    if (isolationFailure) {
      result.error_class = isolationFailure.name;
      result.diagnostic_errors = [{ phase: "not-run-isolation-unavailable", error_class: isolationFailure.name }];
      return; // Keep NOT_RUN; an unverified old process forbids another launch.
    }
    const captureStart = captures.length;
    const failureLabel = `case-${String(cases.indexOf(result) + 1).padStart(2, "0")}-failure`;
    let caseRoutesReady = false;
    currentCase = name;
    try {
      // A previous failed/conflicted draft can own a beforeunload guard. Close
      // its owned process instead of weakening that guard or accepting dialogs.
      if (attemptedNames.size > 1) {
        control.releaseAll();
        await closeBrowser();
        try { browser = await BrowserHarness.launch(); }
        catch (error) { isolationFailure = asError(error); throw error; }
        browserReady = true;
        fixture = installRoutes();
      }
      control.reset();
      caseRoutesReady = true;
      await browser.configureDeterministicDocument({
        source: "localStorage.setItem('autostream.controlPanel.locale','en');localStorage.setItem('autostream.ui_preference',JSON.stringify({theme_id:'autostream',color_mode:'light'}));sessionStorage.setItem('autostream.csrf_token','related-fixture-csrf');",
        timezone: "Asia/Tokyo", locale: "en-US",
      });
      await browser.setViewport(1440, 1000);
      result.body_started = true;
      await body();
      result.status = "PASS";
    }
    catch (error) {
      retainCaseFailure(result, result.body_started ? "case-body" : "case-setup-or-close", error);
      await captureCaseFailure(result, failureLabel);
    }
    finally {
      try { control.releaseAll(); }
      catch (error) {
        retainCaseFailure(result, "case-gate-cleanup", error);
        await captureCaseFailure(result, `${failureLabel}-cleanup`);
      }
      result.request_counts = {};
      for (const request of caseRoutesReady ? requests : []) {
        const key = `${request.method} ${request.path}`;
        result.request_counts[key] = (result.request_counts[key] || 0) + 1;
      }
      result.response_counts = caseRoutesReady ? Object.fromEntries(browser.responses) : {};
      result.captures = captures.slice(captureStart);
    }
  }
  return { get browser() { return browser; }, get failures() { return [...failures]; }, baseURL, evidenceDirectory, width: 1440, fixture: control, cases, captures, completedCycles, mobileOperations,
    navigate, click, fill, readValue, focusCycle, completeCycles, capture, operateAt, check,
    closeBrowser,
    plan(names: readonly string[]) { for (const name of names) { assert.equal(cases.some((item) => item.name === name), false); cases.push({ name, status: "NOT_RUN" }); } },
  };
}
