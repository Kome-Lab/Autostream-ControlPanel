import assert from "node:assert/strict";
import { mkdir, writeFile } from "node:fs/promises";
import { join, resolve } from "node:path";
import test from "node:test";
import { BrowserHarness, ensureWebServer, type RouteResolver } from "./helpers/browser-harness.mts";
import { createBrowserRouteFixture, localeStorageKey, requestedBaseUrl, webRoot } from "./ui-browser-fixture.mts";
import { waitForShell } from "./ui-browser-query-auth-helpers.mts";
import { scrollSelectorIntoView, waitForAnimationFrames } from "./ui-browser-navigation-helpers.mts";
import { paint } from "./ui-regression/navigation.mts";
import { clickVisible } from "./ui-regression/visible-trigger.mts";

const endpoint = "https://recording-layout.example.invalid:51378/" + "long-recording-source-".repeat(8) + "live";
const timestamp = "2026-10-06T02:07:00Z";
const node = {
  id: "layout-worker-174", service_id: "layout-worker-174", service_type: "worker",
  service_name: "長い名前の録画・配信ワーカーノード 174", public_url: endpoint,
  host: "recording-layout.example.invalid", port: 51378, ssl_enabled: true,
  status: "online", health_status: "healthy", heartbeat_age_sec: 1,
  last_heartbeat_at: timestamp, reported_version: "v2.0.0", reported_os: "linux", reported_arch: "amd64",
  metrics: { active_jobs: 0, cpu_percent: 12, memory_percent: 24 },
};
const userAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 Chrome/154.0.0.0 Safari/537.36 " + "layout-diagnostic-agent/174 ".repeat(5);
const auditTarget = "update_agent_long_recording_target_for_layout_verification";
const oauthName = "録画アーカイブと配信に使用する接続アカウント_174";
const videoCoverName = "配信準備中の蓋画像プリセット 174";
const widths = [1440, 390, 768] as const;
const nodeRow = "main .node-data-table tbody tr";
const endpointButton = nodeRow + ' td[headers$="-endpoint"] button';

const geometry = String.raw`
  const issues = [];
  const check = (ok, message) => { if (!ok) issues.push(message); };
  const visible = e => e instanceof HTMLElement && e.getClientRects().length > 0 && !e.closest('[hidden],[inert],[aria-hidden=true]') && getComputedStyle(e).visibility !== 'hidden';
  const inside = (a, b) => a.left >= b.left - 1 && a.right <= b.right + 1 && a.top >= b.top - 1 && a.bottom <= b.bottom + 1;
  const name = e => (e.getAttribute('aria-label') || e.textContent || '').trim();
  const overlap = (a, b) => Math.min(a.right, b.right) - Math.max(a.left, b.left) > 1 && Math.min(a.bottom, b.bottom) - Math.max(a.top, b.top) > 1;
  check(document.documentElement.scrollWidth <= document.documentElement.clientWidth + 1, 'document horizontal overflow');
`;

type TabSnapshot = { issues: string[]; tabs: Array<{ id: string; panel: string; name: string }> };

async function tabSnapshot(browser: BrowserHarness, activePanelOnly = false) {
  return browser.evaluate<TabSnapshot>(`(() => { ${geometry}
    const lists = [...document.querySelectorAll('main [role=tablist]')].filter(visible);
    if (lists.length !== 1) throw Error('one visible product tablist required');
    const list = lists[0], bounds = list.getBoundingClientRect(), tabs = [...list.querySelectorAll('[role=tab]')].filter(visible);
    check(tabs.length === 2, 'two product tabs required');
    const rects = tabs.map(tab => tab.getBoundingClientRect());
    tabs.forEach((tab, index) => {
      check(inside(rects[index], bounds), name(tab) + ': tab extends outside list');
      check(tab.scrollWidth <= tab.clientWidth + 1, name(tab) + ': clipped tab label');
      const walker = document.createTreeWalker(tab, NodeFilter.SHOW_TEXT);
      for (let text = walker.nextNode(); text; text = walker.nextNode()) {
        if (!text.textContent.trim()) continue;
        const range = document.createRange(); range.selectNodeContents(text);
        for (const rect of range.getClientRects()) check(inside(rect, rects[index]), name(tab) + ': text extends outside trigger');
      }
      for (let prior = 0; prior < index; prior++) check(!overlap(rects[prior], rects[index]), 'tab rectangles overlap');
      const panel = document.getElementById(tab.getAttribute('aria-controls'));
      if (!${activePanelOnly} || tab.getAttribute('aria-selected') === 'true') check(panel?.getAttribute('aria-labelledby') === tab.id, name(tab) + ': reciprocal panel reference missing');
    });
    return { issues, tabs: tabs.map(tab => ({ id: tab.id, panel: tab.getAttribute('aria-controls'), name: name(tab) })) };
  })()`);
}

async function checkTabs(browser: BrowserHarness, route: string, expected: readonly string[], width: number) {
  const snapshot = await tabSnapshot(browser);
  assert.deepEqual(snapshot.issues, [], `${route} at ${width}px`);
  assert.deepEqual(snapshot.tabs.map(tab => tab.name), expected);
  const [first, second] = snapshot.tabs;
  await clickVisible(browser, `main [role="tab"][id=${JSON.stringify(first.id)}]`);
  await browser.pressKey("ArrowRight");
  await browser.waitFor(`(() => {
    const tab = document.getElementById(${JSON.stringify(second.id)}), panel = document.getElementById(${JSON.stringify(second.panel)});
    return document.activeElement === tab && tab?.getAttribute('aria-selected') === 'true' && panel?.getClientRects().length > 0;
  })()`, Boolean, `${route}: native ArrowRight selects the second tab`, 5_000);
  await paint(browser);
  assert.deepEqual((await tabSnapshot(browser)).issues, [], `${route}: selected panel must not disturb tab geometry`);
  await browser.pressKey("ArrowLeft");
  await browser.waitFor(`document.activeElement?.id === ${JSON.stringify(first.id)} && document.activeElement?.getAttribute('aria-selected') === 'true'`, Boolean, `${route}: native ArrowLeft restores the first tab`, 5_000);
}

async function expandRow(browser: BrowserHarness, row: string) {
  const disclosure = row + " .ui-record-disclosure button";
  if (await browser.evaluate(`document.querySelector(${JSON.stringify(row)})?.getAttribute('data-record-expanded') === 'true'`)) {
    await clickVisible(browser, disclosure);
    await browser.waitFor(`document.querySelector(${JSON.stringify(row)})?.getAttribute('data-record-expanded') === 'false'`, Boolean, "record collapsed", 5_000);
  }
  await clickVisible(browser, disclosure);
  await browser.waitFor(`document.querySelector(${JSON.stringify(row)})?.getAttribute('data-record-expanded') === 'true'`, Boolean, "secondary fields expanded", 5_000);
  await paint(browser);
}

async function checkNode(browser: BrowserHarness, registered: boolean, width: number) {
  await browser.waitFor(`document.querySelector(${JSON.stringify(nodeRow)})?.textContent.includes(${JSON.stringify(node.service_name)})`, Boolean, "synthetic node row rendered", 5_000);
  await expandRow(browser, nodeRow);
  const snapshot = await browser.evaluate<{ issues: string[]; actions: string[]; controls: number }>(`(() => { ${geometry}
    const rows = [...document.querySelectorAll(${JSON.stringify(nodeRow)})];
    if (rows.length !== 1) throw Error('one synthetic node record required');
    const row = rows[0], rowRect = row.getBoundingClientRect(), table = row.closest('[data-slot=data-table]'), scroller = row.closest('[data-slot=table-container]');
    check(getComputedStyle(row).display === 'grid', 'node record did not reflow by content width');
    const columns = getComputedStyle(row).gridTemplateColumns.split(' ').length;
    check(columns === (table.getBoundingClientRect().width <= 608 ? 1 : 2), 'node card column count does not match content width');
    check(scroller.scrollWidth <= scroller.clientWidth + 1, 'node table still has horizontal overflow');
    for (const cell of row.querySelectorAll('td')) if (visible(cell)) {
      check(inside(cell.getBoundingClientRect(), rowRect), 'node cell extends outside record');
      check(cell.scrollWidth <= cell.clientWidth + 1, 'node cell content overflows');
    }
    const cell = row.querySelector('td[headers$="-endpoint"]'), copies = [...cell.querySelectorAll('button')].filter(visible);
    check(copies.length === 1, 'endpoint copy owner is missing or duplicated');
    check(copies[0]?.getBoundingClientRect().width >= 24, 'endpoint copy control collapsed');
    if (${registered}) {
      const values = [...cell.querySelectorAll('span')].filter(e => e.textContent === ${JSON.stringify(endpoint)});
      check(values.length === 1, 'full applied endpoint must be visible exactly once');
      if (values[0]) {
        const valueRect = values[0].getBoundingClientRect();
        check(valueRect.width >= 80, 'endpoint value is squeezed into a few characters');
        check(inside(valueRect, cell.getBoundingClientRect()), 'endpoint value escapes its cell');
      }
    } else {
      check(cell.textContent.includes('設定済み'), 'operational endpoint configured state is missing');
    }
    const actionCells = row.querySelectorAll('td[headers$="-actions"]');
    check(actionCells.length === 1, 'one actions cell per node required');
    const actions = [...actionCells[0].querySelectorAll('button')].filter(e => visible(e) && !e.disabled && e.getAttribute('aria-disabled') !== 'true').map(name);
    check(actions.length > 0 && actions.every(Boolean) && new Set(actions).size === actions.length, 'action owners must be named and unique');
    return { issues, actions, controls: [...row.querySelectorAll('button')].filter(e => visible(e) && !e.disabled && e.getAttribute('aria-disabled') !== 'true').length };
  })()`);
  assert.deepEqual(snapshot.issues, [], `${registered ? "registered" : "operational"} node at ${width}px`);

  // The disclosure was reached by a native click. Shift+Tab traverses the real
  // row action owners without activating configuration or mutation operations.
  const reached = new Set<string>();
  let reachedCopy = false;
  for (let index = 0; index < Math.min(snapshot.controls + 2, 18); index++) {
    await browser.pressTab("backward");
    const focus = await browser.evaluate<{ copy: boolean; action: string }>(`(() => {
      const active = document.activeElement, row = document.querySelector(${JSON.stringify(nodeRow)});
      return { copy: active === row?.querySelector('td[headers$="-endpoint"] button'),
        action: active?.closest('td[headers$="-actions"]') && row?.contains(active) ? (active.getAttribute('aria-label') || active.textContent || '').trim() : '' };
    })()`);
    if (focus.action) reached.add(focus.action);
    if (focus.copy) { reachedCopy = true; break; }
  }
  assert.deepEqual([...reached].sort(), [...snapshot.actions].sort(), "every enabled action has one keyboard-reachable owner");
  assert.equal(reachedCopy, true, "endpoint copy is keyboard-reachable");
  await scrollSelectorIntoView(browser, endpointButton);
  assert.equal(await browser.evaluate(`(() => {
    const button = document.querySelector(${JSON.stringify(endpointButton)}), r = button.getBoundingClientRect(), hit = document.elementFromPoint(r.left + r.width / 2, r.top + r.height / 2);
    return document.activeElement === button && (hit === button || button.contains(hit));
  })()`), true, "focused endpoint copy is visible and unobstructed after scrolling");
}

async function checkDates(browser: BrowserHarness, kind: "audit" | "oauth", width: number) {
  const row = `main [data-screen-family="${kind === "audit" ? "audit" : "integrations"}"] [data-slot="data-table"] tbody tr`;
  const identity = kind === "audit" ? "layout-auditor-174" : oauthName;
  await browser.waitFor(`document.querySelector(${JSON.stringify(row)})?.textContent.includes(${JSON.stringify(identity)})`, Boolean, `${kind} fixture row rendered`, 5_000);
  if (width <= 768) await expandRow(browser, row);
  await paint(browser);
  const result = await browser.evaluate<{ issues: string[]; dates: number }>(`(() => { ${geometry}
    const row = document.querySelector(${JSON.stringify(row)});
    const dates = [...row.querySelectorAll('time')].filter(visible);
    check(dates.length === ${kind === "audit" ? 1 : 4}, 'expected complete date fields are missing');
    for (const time of dates) {
      check(time.textContent === '2026/10/06 11:07', 'date text or timezone changed');
      const spans = [...time.children];
      check(spans.length === 2, 'date and time must be separately readable');
      for (const span of spans) {
        const range = document.createRange(); range.selectNodeContents(span);
        check(range.getClientRects().length === 1, 'a date/time segment wrapped internally');
        check(inside(span.getBoundingClientRect(), time.closest('td').getBoundingClientRect()), 'date segment escaped its cell');
      }
    }
    if (${kind === "audit"}) {
      const target = row.querySelector('td[headers$="-resource"]'), agent = row.querySelector('td[headers$="-user_agent"]');
      check(target.textContent.includes(${JSON.stringify(auditTarget.replaceAll("_", " "))}), 'long audit target was shortened');
      check(target.querySelectorAll('button').length === 1, 'audit target copy owner duplicated');
      check(agent.textContent.includes(${JSON.stringify(userAgent)}), 'user agent was shortened');
      check(agent.scrollWidth <= agent.clientWidth + 1, 'user agent content overflows');
    } else {
      check(row.textContent.includes(${JSON.stringify(oauthName)}), 'OAuth account identity was shortened');
      check(row.querySelectorAll('button[aria-label="IDをコピー"]').length === 1, 'OAuth ID copy owner duplicated');
      const status = row.querySelector('td[headers$="-oauth_refresh_status"]');
      check(status.textContent.includes('認可のやり直しが必要です'), 'OAuth failure reason is missing');
      for (const label of status.querySelectorAll('span.text-muted-foreground')) {
        const range = document.createRange(); range.selectNodeContents(label);
        check(range.getClientRects().length === 1, 'OAuth refresh label wrapped internally');
      }
    }
    return { issues, dates: dates.length };
  })()`);
  assert.deepEqual(result.issues, [], `${kind} table at ${width}px`);
}

test("CP174 release layouts retain readable records and keyboard-operable Japanese tabs", { timeout: 120_000 }, async t => {
  const server = await ensureWebServer(webRoot, requestedBaseUrl);
  const browserPromise = BrowserHarness.launch();
  t.after(async () => {
    try { await (await browserPromise.catch(() => undefined))?.close(); }
    finally { await server.close(); }
  });
  const browser = await browserPromise;
  const unexpectedWrites: string[] = [];
  const resources: Readonly<Record<string, unknown>> = {
    "/discord/configs": [], "/discord/target-presets": [], "/roles": [], "/permissions": [],
    // Match the API enum; an unsupported select value can itself create a draft.
    "/security/settings": { password_min_length: 12, mfa_mode: "totp" }, "/secrets/status": [],
    "/profiles/overlay": [],
    "/video-cover-presets": [{ id: "layout-video-cover-174", name: videoCoverName, asset_id: "layout-cover-asset-174", asset_variant_id: "layout-cover-variant-174", enabled: true, revision: 1 }],
    "/integrations/oauth-providers": [{ id: "layout-provider-174", name: "Layout Google provider", provider_type: "google", enabled: true }],
    "/integrations/oauth-accounts": [{ id: "layout-oauth-174", account_label: oauthName, provider_type: "google", account_purpose: "drive_youtube", refresh_token_configured: true,
      access_token_refreshed_at: timestamp, access_token_refresh_attempted_at: timestamp, access_token_refresh_failed_at: timestamp,
      access_token_refresh_failure_code: "reauthorization_required", access_token_refresh_relink_required: true, refresh_token_updated_at: timestamp }],
    "/audit-logs": [{ id: "layout-audit-174", timestamp, actor_username: "layout-auditor-174", action: "system_updates.pull_ownership.activate", result: "success",
      resource_type: auditTarget, resource_id: "layout-target-copy-174", actor_ip: "2602:fd6f:100:30::a", user_agent: userAgent }],
  };
  const registration = new Proxy(browser, { get(target, property, receiver) {
    if (property !== "setRouteResolver") return Reflect.get(target, property, receiver);
    return (fallback: RouteResolver) => target.setRouteResolver(request => {
      const path = new URL(request.url).pathname.replace(/\/$/, "");
      if (request.method !== "GET" && path !== "/auth/session/refresh") {
        unexpectedWrites.push(request.method + " " + path);
        return { status: 405, body: { code: "unexpected_layout_mutation" } };
      }
      if (request.method === "GET" && Object.hasOwn(resources, path)) return { body: resources[path], requiredResponse: false };
      return fallback(request);
    });
  } });
  const fixture = createBrowserRouteFixture(registration);
  fixture.nodesResponse = { body: [node] };
  fixture.workersResponse = { body: [node] };
  fixture.healthResponse = { body: [] };
  await browser.configureDeterministicDocument({ source: `localStorage.setItem(${JSON.stringify(localeStorageKey)}, "ja");`, timezone: "Asia/Tokyo", locale: "ja-JP" });
  const evidenceDirectory = process.env.AUTOSTREAM_UI174_EVIDENCE_DIR;
  const screenshots: Array<{ name: string; png: Buffer }> = [];
  const capture = async (name: string) => { if (evidenceDirectory) screenshots.push({ name, png: await browser.captureScreenshot() }); };
  const navigate = async (path: string) => {
    t.signal.throwIfAborted();
    await browser.setViewport(1440, 900);
    await browser.navigate(server.baseUrl + path);
    await waitForShell(browser, "アカウントメニュー");
    await paint(browser);
  };
  const resize = async (width: number) => {
    t.signal.throwIfAborted();
    await browser.setViewport(width, width === 1440 ? 900 : 1024);
    await waitForAnimationFrames(browser);
  };

  for (const [route, labels] of [
    ["/admin/discord/", ["Discord BOT設定", "Discord配信先プリセット"]],
    ["/admin/integrations/", ["OAuthログインプロバイダ", "YouTube・Drive接続"]],
    ["/admin/roles/", ["ロール", "権限一覧"]],
    ["/admin/security/", ["セキュリティ設定", "シークレット登録状況"]],
  ] as const) {
    t.diagnostic(`${route}: start tab layout checks`);
    await navigate(route);
    await browser.waitFor("document.querySelectorAll('main [role=tab]').length", value => value === 2, `${route}: product tabs loaded`, 5_000);
    for (const width of widths) {
      await resize(width);
      await checkTabs(browser, route, labels, width);
      if (route === "/admin/discord/" && width === 1440) await capture("discord-1440.png");
    }
    if (route === "/admin/integrations/") {
      await clickVisible(browser, 'main [role="tab"]', /^YouTube・Drive接続$/);
      for (const width of widths) {
        await resize(width);
        await checkDates(browser, "oauth", width);
        if (width === 390) await capture("oauth-390.png");
      }
    }
    t.diagnostic(`${route}: 1440/390/768 tab bounds, label bounds and native arrow-key selection passed`);
  }
  for (const [route, registered] of [["/admin/registered-nodes/", true], ["/admin/workers/", false]] as const) {
    await navigate(route);
    for (const width of widths) {
      await resize(width);
      await checkNode(browser, registered, width);
      if (registered && width === 390) await capture("registered-nodes-390.png");
    }
    t.diagnostic(`${route}: 1440/390/768 expanded records, endpoint owner, local bounds and keyboard action reach passed`);
  }
  await navigate("/admin/audit-logs/");
  for (const width of widths) {
    await resize(width);
    await checkDates(browser, "audit", width);
    assert.deepEqual((await tabSnapshot(browser, true)).issues, [], "audit line-variant tab geometry and active panel reference");
    if (width === 1440) await capture("audit-1440.png");
  }

  await navigate("/admin/overlay/#video-cover-presets");
  const coverScope = 'main [data-screen-family="overlay"]';
  const coverRow = coverScope + ' [role="tabpanel"][data-state="active"] [data-slot="data-table"] tbody tr';
  await browser.waitFor(`document.querySelector(${JSON.stringify(coverScope + ' [role="tab"][aria-selected="true"]')})?.textContent.trim() === '蓋画像プリセット'`, Boolean, "video cover deep link selects its tab", 5_000);
  await browser.waitFor(`document.querySelector(${JSON.stringify(coverRow)})?.textContent.includes(${JSON.stringify(videoCoverName)})`, Boolean, "synthetic video cover preset appears in the list", 5_000);
  assert.deepEqual((await tabSnapshot(browser)).issues, [], "video cover list tab geometry and panel references");
  assert.equal(await browser.evaluate(`(() => { ${geometry}
    const rows = [...document.querySelectorAll(${JSON.stringify(coverRow)})].filter(visible);
    return rows.length === 1 && visible(rows[0].querySelector('td[headers$="-name"]')) && rows[0].textContent.includes(${JSON.stringify(videoCoverName)});
  })()`), true, "exactly one visible video cover fixture row is available");
  await clickVisible(browser, coverScope + " button", /^新規作成$/);
  await browser.waitFor(`document.querySelector('[role="dialog"] input[type="file"]') !== null`, Boolean, "video cover creation form opens", 5_000);
  for (const width of [1440, 390]) {
    await resize(width);
    await paint(browser);
    const result = await browser.evaluate<{ issues: string[] }>(`(() => { ${geometry}
      const dialogs = [...document.querySelectorAll('[role=dialog]')].filter(visible);
      if (dialogs.length !== 1) throw Error('one visible video cover creation dialog required');
      const dialog = dialogs[0], form = dialog.querySelector('form'), bounds = dialog.getBoundingClientRect();
      check(document.getElementById(dialog.getAttribute('aria-labelledby'))?.textContent === '蓋画像プリセットを作成', 'video cover creation dialog title is missing');
      check(bounds.left >= -1 && bounds.right <= window.innerWidth + 1, 'video cover dialog exceeds viewport width');
      check(dialog.scrollWidth <= dialog.clientWidth + 1, 'video cover dialog content overflows horizontally');
      const presetName = form?.querySelector('input[type=text]'), image = form?.querySelector('input[type=file]'), submit = form?.querySelector('button[type=submit]');
      check(presetName && visible(presetName) && !presetName.disabled && presetName.required && presetName.value === '' && [...presetName.labels].some(label => label.textContent.includes('プリセット名')), 'new preset name field is not available and labelled');
      const chooser = image?.closest('label');
      check(image && !image.disabled && image.accept === 'image/png,image/jpeg,image/webp' && chooser && visible(chooser) && chooser.textContent.includes('画像を選択'), 'image chooser is not available and labelled');
      check(image && document.getElementById(image.getAttribute('aria-describedby'))?.textContent.includes('16:9'), 'image requirements are not associated with the chooser');
      check(form?.querySelector('[role=status]')?.textContent === '画像が選択されていません。', 'new preset must start without an uploaded image');
      check(form?.querySelector('[role=switch]')?.getAttribute('aria-checked') === 'true', 'new preset availability switch is missing');
      check(submit && visible(submit) && submit.disabled, 'save must remain disabled before image selection');
      for (const control of [presetName, chooser, submit]) if (control) {
        const rect = control.getBoundingClientRect();
        check(rect.left >= bounds.left - 1 && rect.right <= bounds.right + 1, 'video cover form control exceeds dialog width');
      }
      return { issues };
    })()`);
    assert.deepEqual(result.issues, [], `video cover creation form at ${width}px`);
    if (width === 1440) await capture("video-cover-create-1440.png");
  }
  await browser.pressKey("Escape");
  await browser.waitFor(`document.querySelector('[role="dialog"]') === null`, Boolean, "unchanged video cover draft closes without saving", 5_000);
  t.diagnostic("/admin/overlay/#video-cover-presets: fixture list, labelled creation form at 1440/390, and close without upload or save passed");

  assert.deepEqual(unexpectedWrites, [], "layout checks cannot issue application mutations");
  assert.equal(browser.consoleErrorCount, 0, "no application console errors");
  browser.assertNoFatalError();
  if (evidenceDirectory) {
    const directory = resolve(evidenceDirectory);
    await mkdir(directory, { recursive: true });
    for (const screenshot of screenshots) await writeFile(join(directory, screenshot.name), screenshot.png);
    t.diagnostic(`Saved ${screenshots.length} successful layout screenshots to ${directory}`);
  }
});
