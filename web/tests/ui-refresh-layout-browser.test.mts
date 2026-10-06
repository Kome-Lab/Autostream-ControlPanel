import assert from "node:assert/strict";
import { mkdir, writeFile } from "node:fs/promises";
import { join, resolve } from "node:path";
import test from "node:test";
import { BrowserHarness, ensureWebServer, type RouteResolver, type StubResponse } from "./helpers/browser-harness.mts";
import { createBrowserRouteFixture, localeStorageKey, permissionUser, requestedBaseUrl, webRoot } from "./ui-browser-fixture.mts";
import { waitForShell } from "./ui-browser-query-auth-helpers.mts";
import { waitForAnimationFrames } from "./ui-browser-navigation-helpers.mts";
import { clickVisible } from "./ui-regression/visible-trigger.mts";
import { paint } from "./ui-regression/navigation.mts";

const oauthPath = "/integrations/oauth-accounts";
const oauthAccount = {
  id: "poll-oauth-account", provider_type: "google", provider_id: "poll-provider",
  account_label: "Poll OAuth account", account_purpose: "drive_youtube", email: "before@example.test",
  scopes: ["https://www.googleapis.com/auth/drive.file", "https://www.googleapis.com/auth/youtube"],
};
const node = {
  id: "poll-worker-00", service_id: "poll-worker-00", service_type: "worker", service_name: "Poll Worker 00",
  host: "worker.example.invalid", port: 51377, status: "online", health_status: "healthy",
  reported_version: "v2.0.0", heartbeat_age_sec: 1, metrics: { cpu_percent: 11, memory_percent: 22 },
};
const editInput = '[role="dialog"] input[type="text"]';
const workerTable = '[data-screen-family="workers"] [data-slot="data-table"]';
const workerRows = '[data-screen-family="workers"] [data-slot="data-table"] tbody tr';
const pageText = `document.querySelector('[data-screen-family="workers"] [data-slot="table-pagination"] [aria-live="polite"]')?.textContent.trim()`;

test("CP live polling preserves OAuth drafts, node confirmation and Workers page state", { timeout: 120_000 }, async t => {
  const server = await ensureWebServer(webRoot, requestedBaseUrl);
  t.after(() => server.close());
  const browser = await BrowserHarness.launch();
  t.after(() => browser.close());
  let oauthResponse: StubResponse = { body: [oauthAccount], delayMs: 150 };
  const requestMethods: Array<{ path: string; method: string }> = [];
  const capture = async (name: string) => {
    const evidence = process.env.AUTOSTREAM_UI174_EVIDENCE_DIR;
    if (!evidence) return;
    const directory = resolve(evidence);
    await mkdir(directory, { recursive: true });
    await writeFile(join(directory, name), await browser.captureScreenshot());
  };

  // Compose only the public route registration. The original fixture, live query
  // timers, browser process, Fetch lifecycle and failure guards are unchanged.
  const routeRegistration = new Proxy(browser, {
    get(target, property, receiver) {
      if (property !== "setRouteResolver") return Reflect.get(target, property, receiver);
      return (fallback: RouteResolver) => target.setRouteResolver(request => {
        const path = new URL(request.url).pathname.replace(/\/$/, "");
        requestMethods.push({ path, method: request.method });
        if (request.method !== "GET" && path !== "/auth/session/refresh") {
          return { status: 405, body: { code: "unexpected_ui_mutation" } };
        }
        if (request.method === "GET" && path === oauthPath) return { ...oauthResponse, requiredResponse: false };
        if (request.method === "GET" && path === "/integrations/oauth-providers") return { body: [{ id: "poll-provider", provider_type: "google", name: "Polling provider", enabled: true }], requiredResponse: false };
        if (request.method === "GET" && path === "/roles") return { body: [], requiredResponse: false };
        return fallback(request);
      });
    },
  });
  const fixture = createBrowserRouteFixture(routeRegistration);
  fixture.healthResponse = { body: [] };
  fixture.workersResponse = { body: [] };
  fixture.nodesResponse = { body: [] };
  await browser.configureDeterministicDocument({
    source: `localStorage.setItem(${JSON.stringify(localeStorageKey)}, "en");`,
    timezone: "Asia/Tokyo", locale: "en-US",
  });
  await browser.setViewport(1440, 900);

  // The UI edits a synthetic label only; neither this case nor the other cases
  // confirms a write. Wait for real scheduled GETs, without manual refetch/events.
  await browser.navigate(`${server.baseUrl}/admin/integrations/`);
  await waitForShell(browser, "Account menu");
  await clickVisible(browser, 'main [role="tab"]', /^YouTube and Drive connections$/);
  await clickVisible(browser, 'main button', /^Edit Poll OAuth account$/);
  await clickVisible(browser, editInput);
  await browser.fillSelector(editInput, "Unsaved polling draft");
  await browser.waitFor(`document.querySelector(${JSON.stringify(editInput)})?.value`, value => value === "Unsaved polling draft", "OAuth draft entered");
  assert.equal(await browser.evaluate(`(() => {
    const input = document.querySelector(${JSON.stringify(editInput)}), dialog = input?.closest('[role="dialog"]');
    if (!input || !dialog || document.activeElement !== input) return false;
    globalThis.__refreshOAuth = { input, dialog }; return true;
  })()`), true, "store the actual focused input and Dialog owners");
  await browser.waitForRequestHandlersIdle({ pathname: oauthPath, method: "GET" });
  const oauthBefore = browser.responses.get(oauthPath) || 0;
  oauthResponse = { body: [{ ...oauthAccount, email: "after-poll@example.test" }], delayMs: 150 };
  await browser.waitForResponseCount(oauthPath, oauthBefore + 1, 15_000);
  await browser.waitFor(`document.querySelector('[role="dialog"]')?.textContent.includes('after-poll@example.test')`, Boolean, "OAuth polling response reached the existing editor");
  await waitForAnimationFrames(browser);
  assert.deepEqual(await browser.evaluate(`(() => {
    const old = globalThis.__refreshOAuth, input = document.querySelector(${JSON.stringify(editInput)});
    return { sameInput: input === old.input && input.isConnected, sameDialog: input?.closest('[role="dialog"]') === old.dialog && old.dialog.isConnected,
      value: input?.value, focused: document.activeElement === old.input };
  })()`), { sameInput: true, sameDialog: true, value: "Unsaved polling draft", focused: true });
  t.diagnostic(`OAuth scheduled GET responses: ${oauthBefore} -> ${browser.responses.get(oauthPath)}; draft, DOM owner and focus preserved`);
  await capture("oauth-unsaved-after-poll-1440.png");
  await browser.fillSelector(editInput, oauthAccount.account_label);
  await browser.pressNativeKey("Escape");
  await browser.waitFor(`document.querySelector('[role="dialog"]') === null`, Boolean, "OAuth editor closed without saving");
  await browser.evaluate("delete globalThis.__refreshOAuth; true");

  fixture.nodesResponse = { body: [node], delayMs: 150 };
  await browser.navigate(`${server.baseUrl}/admin/registered-nodes/`);
  await waitForShell(browser, "Account menu");
  await clickVisible(browser, 'main button', /^Regenerate runtime token$/);
  await browser.waitFor(`document.querySelector('[role="alertdialog"]')?.textContent.includes('Poll Worker 00')`, Boolean, "confirmation targets the registered node");
  assert.equal(await browser.evaluate(`(() => {
    const dialog = document.querySelector('[role="alertdialog"]');
    if (!dialog?.contains(document.activeElement)) return false;
    globalThis.__refreshNode = { dialog, focused: document.activeElement }; return true;
  })()`), true, "confirmation opened with focus inside");
  await browser.waitForRequestHandlersIdle({ pathname: "/nodes", method: "GET" });
  const nodesBefore = browser.responses.get("/nodes") || 0;
  fixture.nodesResponse = { body: [{ ...node, heartbeat_age_sec: 2 }], delayMs: 150 };
  await browser.waitForResponseCount("/nodes", nodesBefore + 1, 15_000);
  await browser.waitForRequestHandlersIdle({ pathname: "/nodes", method: "GET" });
  await waitForAnimationFrames(browser);
  assert.deepEqual(await browser.evaluate(`(() => {
    const old = globalThis.__refreshNode, dialog = document.querySelector('[role="alertdialog"]');
    return { sameDialog: dialog === old.dialog && old.dialog.isConnected, sameFocus: document.activeElement === old.focused && old.focused.isConnected,
      targetRetained: dialog?.textContent.includes('Poll Worker 00') === true };
  })()`), { sameDialog: true, sameFocus: true, targetRetained: true });
  t.diagnostic(`Node scheduled GET responses: ${nodesBefore} -> ${browser.responses.get("/nodes")}; unsent confirmation preserved`);
  await capture("node-confirmation-after-poll-1440.png");
  await browser.pressNativeKey("Escape");
  await browser.waitFor(`document.querySelector('[role="alertdialog"]') === null`, Boolean, "confirmation cancelled without mutation");
  await browser.evaluate("delete globalThis.__refreshNode; true");

  const rows = Array.from({ length: 17 }, (_, index) => ({ ...node,
    id: `poll-worker-${String(index).padStart(2, "0")}`, service_id: `poll-worker-${String(index).padStart(2, "0")}`,
    service_name: `Poll Worker ${String(index).padStart(2, "0")}`,
  }));
  fixture.nodesResponse = { body: [] };
  fixture.workersResponse = { body: rows, delayMs: 800 };
  await browser.navigate(`${server.baseUrl}/admin/workers/`);
  await waitForShell(browser, "Account menu");
  await browser.waitFor(pageText, value => value === "Page 1 of 3", "17 Workers produce three pages");
  await clickVisible(browser, '[data-screen-family="workers"] [data-slot="table-pagination"] button', /^Next page$/);
  await browser.waitFor(pageText, value => value === "Page 2 of 3", "second page selected");
  assert.equal(await browser.evaluate(`(() => {
    const row = document.querySelector(${JSON.stringify(workerRows)});
    if (!row?.querySelector('td[headers$="-service_name"]')?.textContent.includes('Poll Worker 08')) return false;
    globalThis.__refreshWorker = row; return true;
  })()`), true, "remember page two's first real record");
  await browser.waitForRequestHandlersIdle({ pathname: "/workers", method: "GET" });
  await browser.waitFor(`document.querySelector('[data-screen-family="workers"] button[aria-busy="true"]') === null`, Boolean, "initial Worker requests settled");
  await waitForAnimationFrames(browser);
  const workerTableY = await browser.evaluate<number>(`document.querySelector(${JSON.stringify(workerTable)}).getBoundingClientRect().top + window.scrollY`);
  assert.ok(Number.isFinite(workerTableY), "record table position before the scheduled poll");
  const workersBefore = browser.responses.get("/workers") || 0;
  const workerRequestsBefore = browser.requests.get("/workers") || 0;
  fixture.workersResponse = { body: rows.map(row => ({ ...row, metrics: { cpu_percent: 81, memory_percent: 22 } })), delayMs: 800 };
  await browser.waitForRequestCount("/workers", workerRequestsBefore + 1, 15_000);
  await browser.waitFor(`document.querySelector('[data-screen-family="workers"] button[aria-busy="true"]') !== null`, Boolean, "background Worker GET is visibly fetching");
  await waitForAnimationFrames(browser);
  assert.equal(browser.responses.get("/workers") || 0, workersBefore, "measure a real fetching frame before the delayed response");
  assert.equal(await browser.evaluate(pageText), "Page 2 of 3", "page is retained during the request");
  const fetchingTableY = await browser.evaluate<number>(`document.querySelector(${JSON.stringify(workerTable)}).getBoundingClientRect().top + window.scrollY`);
  assert.ok(Math.abs(fetchingTableY - workerTableY) <= 0.5, `table must not jump during polling: ${workerTableY} -> ${fetchingTableY}`);
  await browser.waitForResponseCount("/workers", workersBefore + 1, 15_000);
  await browser.waitFor(`document.querySelector(${JSON.stringify(workerRows)})?.querySelector('td[headers$="-load"]')?.textContent.includes('CPU 81%')`, Boolean, "new metric is displayed after the scheduled GET");
  await waitForAnimationFrames(browser);
  assert.equal(await browser.evaluate(pageText), "Page 2 of 3");
  assert.deepEqual(await browser.evaluate(`(() => {
    const row = document.querySelector(${JSON.stringify(workerRows)}), load = row?.querySelector('td[headers$="-load"]');
    return { sameRow: row === globalThis.__refreshWorker && row.isConnected,
      firstRecord: row?.querySelector('td[headers$="-service_name"]')?.textContent.includes('Poll Worker 08') === true,
      freshMetric: load?.textContent.includes('CPU 81%') === true && load.getClientRects().length > 0 };
  })()`), { sameRow: true, firstRecord: true, freshMetric: true });
  const refreshedTableY = await browser.evaluate<number>(`document.querySelector(${JSON.stringify(workerTable)}).getBoundingClientRect().top + window.scrollY`);
  assert.ok(Math.abs(refreshedTableY - workerTableY) <= 0.5, `table must remain in place after polling: ${workerTableY} -> ${refreshedTableY}`);
  t.diagnostic(`Workers table Y: ${workerTableY} -> fetching ${fetchingTableY} -> refreshed ${refreshedTableY}`);
  t.diagnostic(`Workers scheduled GET responses: ${workersBefore} -> ${browser.responses.get("/workers")}; page, record DOM and fresh metric verified`);
  await capture("workers-page-two-after-poll-1440.png");
  await browser.evaluate("delete globalThis.__refreshWorker; true");

  for (const path of [oauthPath, "/nodes", "/workers"]) {
    assert.ok(requestMethods.filter(request => request.path === path && request.method === "GET").length >= 2, `${path}: real repeated GETs observed`);
    assert.ok(browser.responseStatuses.get(path)?.every(status => status === 200), `${path}: successful responses required`);
  }
  assert.deepEqual(requestMethods.filter(request => request.method !== "GET" && request.path !== "/auth/session/refresh"), [], "no application or token mutation may be sent");
  assert.equal(browser.consoleErrorCount, 0, "no application console errors");
  browser.assertNoFatalError();
});

test("Monitoring and Dashboard retain geometry during real scheduled GETs at desktop and mobile widths", { timeout: 120_000 }, async t => {
  const server = await ensureWebServer(webRoot, requestedBaseUrl);
  t.after(() => server.close());
  const browser = await BrowserHarness.launch();
  t.after(() => browser.close());
  const writes: string[] = [];
  const registration = new Proxy(browser, { get(target, property, receiver) {
    if (property !== "setRouteResolver") return Reflect.get(target, property, receiver);
    return (fallback: RouteResolver) => target.setRouteResolver(request => {
      const path = new URL(request.url).pathname.replace(/\/$/, "");
      if (request.method !== "GET" && path !== "/auth/session/refresh") {
        writes.push(request.method + " " + path);
        return { status: 405, body: { code: "unexpected_polling_mutation" } };
      }
      if (request.method === "GET" && ["/observability/incidents", "/observability/diagnostics"].includes(path)) return { body: [], requiredResponse: false };
      return fallback(request);
    });
  } });
  const fixture = createBrowserRouteFixture(registration);
  fixture.healthResponse = { body: [node], delayMs: 1_200 };
  fixture.streamsResponse = { body: [] };
  await browser.configureDeterministicDocument({ source: `localStorage.setItem(${JSON.stringify(localeStorageKey)}, "ja");`, timezone: "Asia/Tokyo", locale: "ja-JP" });
  for (const [route, family, anchors] of [
    ["/admin/monitoring/", "monitoring", ['[data-slot="detail-section"]', '[data-slot="detail-section"]:last-child']],
    ["/admin/", "dashboard", ["#dashboard-waiting", "#dashboard-recent"]],
  ] as const) for (const width of [1440, 390]) {
    await browser.setViewport(width, width === 1440 ? 900 : 1024);
    await browser.navigate(server.baseUrl + route);
    await waitForShell(browser, "アカウントメニュー");
    const root = `main [data-screen-family="${family}"]`;
    await browser.waitFor(`document.querySelector(${JSON.stringify(root + ' [data-freshness="fresh"]')}) !== null`, Boolean, "successful initial operational data", 10_000);
    await browser.waitForRequestHandlersIdle({ pathname: "/service-health", method: "GET" });
    await paint(browser);
    const snapshot = () => browser.evaluate<{ y: number[]; scrollY: number; text: string; overflow: boolean }>(`(() => {
      const root = document.querySelector(${JSON.stringify(root)});
      const nodes = ${JSON.stringify(anchors)}.map(selector => root.querySelector(selector));
      if (nodes.some(node => !node)) throw Error('operational layout anchors missing');
      const walker = document.createTreeWalker(root, NodeFilter.SHOW_TEXT), text = [];
      for (let part = walker.nextNode(); part; part = walker.nextNode()) if (!part.parentElement.closest('.sr-only,[hidden]') && part.parentElement.getClientRects().length) text.push(part.textContent.trim());
      return { y: nodes.map(node => node.getBoundingClientRect().top + window.scrollY), scrollY: window.scrollY, text: text.filter(Boolean).join(' '), overflow: document.documentElement.scrollWidth > document.documentElement.clientWidth + 1 };
    })()`);
    const before = await snapshot();
    assert.equal(before.overflow, false, `${family}/${width}: no document overflow`);
    const responses = browser.responses.get("/service-health") || 0;
    const requests = browser.requests.get("/service-health") || 0;
    await browser.waitForRequestCount("/service-health", requests + 1, 15_000);
    await browser.waitFor(`document.querySelector(${JSON.stringify(root + ' [data-freshness="refreshing"]')}) !== null`, Boolean, "actual operational GET is fetching", 5_000);
    await waitForAnimationFrames(browser);
    const fetching = await snapshot();
    assert.equal(browser.responses.get("/service-health") || 0, responses, "fetching geometry is measured before the delayed response");
    await browser.waitForResponseCount("/service-health", responses + 1, 5_000);
    await browser.waitFor(`document.querySelector(${JSON.stringify(root + ' [data-freshness="fresh"]')}) !== null`, Boolean, "scheduled GET settled successfully", 5_000);
    await paint(browser);
    const after = await snapshot();
    for (const state of [fetching, after]) {
      assert.equal(state.text, before.text, `${family}/${width}: loaded wording remains stable`);
      assert.equal(state.scrollY, before.scrollY, `${family}/${width}: viewport scroll remains stable`);
      assert.equal(state.overflow, false);
      state.y.forEach((y, index) => assert.ok(Math.abs(y - before.y[index]) <= 0.5, `${family}/${width}: anchor ${index} must not jump: ${before.y[index]} -> ${y}`));
    }
    t.diagnostic(`${family}/${width}: actual GET ${responses} -> ${browser.responses.get("/service-health")}; Y ${before.y} -> fetching ${fetching.y} -> refreshed ${after.y}`);
    const evidence = process.env.AUTOSTREAM_UI174_EVIDENCE_DIR;
    if (evidence) {
      const directory = resolve(evidence);
      await mkdir(directory, { recursive: true });
      await writeFile(join(directory, `${family}-${width}.png`), await browser.captureScreenshot());
    }
  }
  assert.deepEqual(writes, []);
  assert.equal(browser.consoleErrorCount, 0);
  browser.assertNoFatalError();
});

test("v2 preset screens enforce permissions, distinguish failed and empty reads, and retain drafts on revision conflict", { timeout: 90_000 }, async t => {
  const server = await ensureWebServer(webRoot, requestedBaseUrl);
  t.after(() => server.close());
  const browser = await BrowserHarness.launch();
  t.after(() => browser.close());
  const preset = { id: "browser-cover-174", name: "Browser cover preset", asset_id: "saved-cover-asset", asset_variant_id: "saved-cover-variant", enabled: true, revision: 4 };
  let coverResponse: StubResponse = { body: [preset] };
  const writes: Array<{ method: string; path: string; body: unknown }> = [];
  const registration = new Proxy(browser, { get(target, property, receiver) {
    if (property !== "setRouteResolver") return Reflect.get(target, property, receiver);
    return (fallback: RouteResolver) => target.setRouteResolver(request => {
      const path = new URL(request.url).pathname.replace(/\/$/, "");
      if (request.method === "PUT" && path === `/video-cover-presets/${preset.id}`) {
        writes.push({ method: request.method, path, body: JSON.parse(request.postData || "null") });
        // A concurrent update occurs after the final authority read, at dispatch.
        return { status: 409, body: { code: "revision_conflict" } };
      }
      if (request.method !== "GET" && path !== "/auth/session/refresh") {
        writes.push({ method: request.method, path, body: null });
        return { status: 405, body: { code: "unexpected_acceptance_mutation" } };
      }
      if (request.method === "GET" && path === "/video-cover-presets") return { ...coverResponse, requiredResponse: false };
      if (request.method === "GET" && ["/discord/configs", "/discord/target-presets", "/youtube/outputs", "/profiles/encoder", "/profiles/caption", "/profiles/overlay", "/profiles/archive", "/recording-sessions"].includes(path)) return { body: [], requiredResponse: false };
      return fallback(request);
    });
  } });
  const fixture = createBrowserRouteFixture(registration);
  fixture.healthResponse = { body: [] };
  fixture.streamsResponse = { body: [] };
  fixture.workersResponse = { body: [] };
  fixture.nodesResponse = { body: [] };
  await browser.configureDeterministicDocument({ source: `localStorage.setItem(${JSON.stringify(localeStorageKey)}, "en");`, timezone: "Asia/Tokyo", locale: "en-US" });
  let currentPath = "";
  const navigate = async (path: string) => {
    // Navigating to an identical fragment URL is same-document navigation and
    // does not emit a new Page.loadEventFired. Reload for a new fixture condition.
    if (path === currentPath) await browser.reload();
    else await browser.navigate(server.baseUrl + path);
    currentPath = path;
    await waitForShell(browser, "Account menu");
  };
  const capture = async (name: string) => {
    const evidence = process.env.AUTOSTREAM_UI174_EVIDENCE_DIR;
    if (!evidence) return;
    const directory = resolve(evidence);
    await mkdir(directory, { recursive: true });
    await writeFile(join(directory, name), await browser.captureScreenshot());
  };

  fixture.authResponse = { body: permissionUser(["streams.read", "streams.create", "video_cover_presets.read", "discord_target_presets.read"]) };
  await browser.setViewport(390, 1024);
  await navigate("/admin/streams/#create-stream");
  await browser.waitFor("document.querySelectorAll('#create-stream-visual [role=combobox]').length === 4", Boolean, "new stream exposes four v2 setting controls", 10_000);
  assert.equal(await browser.evaluate("[...document.querySelectorAll('#create-stream-visual [role=combobox]')].every(control => !control.disabled && control.getAttribute('aria-disabled') !== 'true')"), true, "streams.create alone can use the v2 settings");
  assert.deepEqual(await browser.evaluate(`(() => {
    const links = [...document.querySelectorAll('#create-stream-visual a[target="_blank"]')];
    return links.map(link => ({ href: link.getAttribute('href'), rel: link.getAttribute('rel') })).sort((a, b) => a.href.localeCompare(b.href));
  })()`), [
    { href: "/admin/discord/#target-presets", rel: "noopener noreferrer" },
    { href: "/admin/overlay/#video-cover-presets", rel: "noopener noreferrer" },
  ], "both preset management links preserve the current creation form");
  await paint(browser);
  await capture("stream-create-only-390.png");
  t.diagnostic("390px stream creation: all four v2 settings enabled for create-only permission; both separate-tab preset links available");

  fixture.authResponse = { body: permissionUser(["video_cover_presets.read"]) };
  await browser.setViewport(1440, 900);
  await navigate("/admin/overlay/#video-cover-presets");
  const coverScope = 'main [data-screen-family="overlay"] [role="tabpanel"][data-state="active"]';
  await browser.waitFor(`document.querySelector(${JSON.stringify(coverScope)})?.textContent.includes(${JSON.stringify(preset.name)})`, Boolean, "read-only preset list loaded", 10_000);
  assert.equal(await browser.evaluate(`document.querySelector('main [role="tab"][aria-selected="true"]')?.textContent.trim()`), "Video cover presets");
  assert.deepEqual(await browser.evaluate(`(() => {
    const root = document.querySelector(${JSON.stringify(coverScope)});
    const controls = [...root.querySelectorAll('button')].filter(button => button.textContent.trim() === 'Create' || button.getAttribute('aria-label') === 'Edit Browser cover preset');
    return controls.map(control => ({ name: control.getAttribute('aria-label') || control.textContent.trim(), disabled: control.disabled }));
  })()`), [{ name: "Create", disabled: true }, { name: "Edit Browser cover preset", disabled: true }], "read-only users cannot create or edit presets");
  await capture("video-cover-read-only-1440.png");
  for (const status of [503, 403, 200]) {
    const before = (browser.responseStatuses.get("/video-cover-presets") || []).length;
    coverResponse = status === 200 ? { body: [] } : { status, body: { code: status === 503 ? "unavailable" : "forbidden" } };
    await navigate("/admin/overlay/#video-cover-presets");
    await browser.waitFor(`document.querySelector(${JSON.stringify(coverScope + ' [role="alert"]')}) ${status === 200 ? "===" : "!=="} null && document.querySelector(${JSON.stringify(coverScope)})?.textContent.includes('No data available.') === ${status === 200}`, Boolean, `HTTP ${status}: ${status === 200 ? "successful empty result" : "failed read has an alert and no false empty result"}`, 10_000);
    await browser.waitForRequestHandlersIdle({ pathname: "/video-cover-presets", method: "GET" });
    const statuses = (browser.responseStatuses.get("/video-cover-presets") || []).slice(before);
    assert.ok(statuses.length > 0 && statuses.every(value => value === status), `actual HTTP ${status} observed`);
    t.diagnostic(`Preset initial read HTTP ${status}: expected error/empty presentation passed`);
  }
  t.diagnostic("Read-only preset deep link works; HTTP 503/403 remain errors and HTTP 200 [] remains a successful empty list");

  coverResponse = { body: [preset] };
  fixture.authResponse = { body: permissionUser(["video_cover_presets.read", "video_cover_presets.update"]) };
  await navigate("/admin/overlay/#video-cover-presets");
  await clickVisible(browser, 'main button', /^Edit Browser cover preset$/);
  await browser.waitFor(`document.querySelector('[role="dialog"] input[type="text"]')?.value === 'Browser cover preset'`, Boolean, "preset editor opens with its saved baseline", 5_000);
  assert.equal(await browser.evaluate(`document.querySelector('[role="dialog"] input[type="file"]')?.disabled`), true, "preset-only update permission cannot upload stream media");
  assert.equal(await browser.evaluate(`document.querySelector('[role="dialog"]')?.textContent.includes('The saved image is selected.')`), true, "saved image remains usable without upload permission");
  await clickVisible(browser, editInput);
  await browser.fillSelector(editInput, "Unsaved conflict draft");
  await clickVisible(browser, '[role="dialog"] button[type="submit"]', /^Updated$/);
  await browser.waitFor(`document.querySelector('[role="alertdialog"]') !== null`, Boolean, "update requires confirmation", 5_000);
  await clickVisible(browser, '[role="alertdialog"] button[data-confirm-action]', /^Run$/);
  await browser.waitForResponseCount(`/video-cover-presets/${preset.id}`, 1, 5_000);
  await browser.waitFor(`document.querySelector('[role="dialog"]')?.textContent.includes('The resource has changed.')`, Boolean, "revision conflict is disclosed in the retained editor", 5_000);
  assert.equal(await browser.evaluate(`document.querySelector(${JSON.stringify(editInput)})?.value`), "Unsaved conflict draft", "conflict must not reset the input");
  assert.deepEqual(writes, [{ method: "PUT", path: `/video-cover-presets/${preset.id}`, body: { name: "Unsaved conflict draft", asset_id: preset.asset_id, asset_variant_id: preset.asset_variant_id, enabled: true, expected_revision: 4 } }], "one fixture-only mutation, using the editor's original revision and saved image; no automatic retry");
  await capture("video-cover-revision-conflict-1440.png");
  t.diagnostic("Fixture PUT expected_revision=4 received 409; saved image and unsaved name retained; no automatic retry or unrelated mutation");
  browser.assertNoFatalError();
});
