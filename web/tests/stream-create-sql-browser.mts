import assert from "node:assert/strict";
import { mkdirSync, writeFileSync } from "node:fs";
import { resolve } from "node:path";
import { BrowserHarness, ensureWebServer } from "./helpers/browser-harness.mts";
import { withTimeout } from "./helpers/browser-process-timing.mts";
import { createSQLUIProxy } from "./helpers/sql-ui-proxy.mts";
import { clickVisible } from "./ui-regression/visible-trigger.mts";
import { fileNames, select, upload, waitCards } from "./stream-create-covers-browser-fixture.mts";
import { webRoot } from "./ui-browser-fixture.mts";
import { verifyActualCreateAuthority } from "./helpers/sql-ui-authority.mts";

const apiOrigin = process.env.AUTOSTREAM_SQL_UI_API_ORIGIN;
assert.ok(apiOrigin && new URL(apiOrigin).hostname === "127.0.0.1", "only the isolated loopback Go fixture is permitted");
const evidence = process.env.AUTOSTREAM_SQL_UI_EVIDENCE_DIR;
const bootstrap = await fetch(`${apiOrigin}/__polling-fixture`).then((response) => response.json()) as { csrf_token: string; encoder: string; worker: string; with_images: boolean; store_kind: "SQL" | "MEMORY" };
const authority = await verifyActualCreateAuthority(apiOrigin, bootstrap.encoder, bootstrap.worker);
if (evidence) { mkdirSync(evidence, { recursive: true }); writeFileSync(resolve(evidence, "actual-authority.json"), JSON.stringify(authority, null, 2)); }
const web = await ensureWebServer(webRoot, process.env.AUTOSTREAM_BROWSER_BASE_URL || "http://127.0.0.1:3037");
const proxy = await createSQLUIProxy(web.baseUrl, apiOrigin);
let browser: BrowserHarness | undefined;
const phases: Record<string, unknown>[] = [];

async function capture(label: string) {
  if (!evidence || !browser) return;
  mkdirSync(evidence, { recursive: true });
  await browser.evaluate(`(document.querySelector('[data-media-upload]') || document.querySelector('[data-cover-candidate]') || document.querySelector('#create-stream-basic'))?.scrollIntoView({block:'center'});true`);
  const observation = structuredClone(await browser.evaluate(`({width:innerWidth,overflow:document.documentElement.scrollWidth-document.documentElement.clientWidth,operations:[...document.querySelectorAll('[data-media-upload]')].map(el=>({phase:el.getAttribute('data-media-upload-phase'),kind:el.getAttribute('data-media-upload-kind'),text:el.textContent})),candidates:document.querySelectorAll('[data-cover-candidate]').length,submitDisabled:document.querySelector('#create-stream form button[type=submit]')?.disabled,messages:[...document.querySelectorAll('#create-stream [role=alert],#create-stream [role=status]')].map(el=>el.textContent),formText:document.querySelector('#create-stream form')?.textContent?.slice(-8000)})`));
  const image = await browser.captureViewportEvidence();
  writeFileSync(resolve(evidence, `${label}.png`), image.png);
  writeFileSync(resolve(evidence, `${label}.json`), JSON.stringify(observation, null, 2));
  phases.push({ label, observation });
}

async function phase(value: string) {
  await browser!.waitFor(`!!document.querySelector('[data-media-upload-phase="${value}"]')`, Boolean, `visible ${value} upload state missing`);
}

try {
  const warmup = await fetch(`${web.baseUrl}/admin/streams/`, { signal: AbortSignal.timeout(60_000) }); assert.equal(warmup.status, 200);
  browser = await BrowserHarness.launch();
  await browser.configureDeterministicDocument({ source: `localStorage.setItem('autostream.controlPanel.locale','ja');sessionStorage.setItem('autostream.csrf_token',${JSON.stringify(bootstrap.csrf_token)});`, timezone: "Asia/Tokyo", locale: "ja-JP" });
  browser.setRouteResolver(() => null);
  await browser.setViewport(1440, 1000);
  await browser.navigate(`${proxy.baseUrl}/admin/streams/`);
  await browser.waitFor(`!!document.querySelector('main button')`, Boolean, "real stream page did not load");
  await clickVisible(browser, "main button", /^配信枠を作成$/);
  await browser.waitFor(`!!document.querySelector('#create-stream-basic input')`, Boolean, "real create form missing");
  await browser.fillSelector("#create-stream-basic input", "SQL browser actual payload");
  const automatic = "#create-stream-start button[role=checkbox]";
  if (await browser.evaluate(`document.querySelector('${automatic}')?.getAttribute('data-state')==='checked'`)) await clickVisible(browser, automatic);
  await select(browser, "担当Encoder Node", bootstrap.encoder);
  await select(browser, "担当Worker Node", bootstrap.worker);
  if (bootstrap.with_images) await select(browser, "蓋画像のソース", "アップロード");
  for (let cycle = 0; cycle < 3; cycle++) {
    const before = proxy.finished.get("GET /auth/me") || 0;
    await browser.evaluate(`document.querySelector('#create-stream-basic input').focus();document.dispatchEvent(new Event('visibilitychange',{bubbles:true}));true`);
    await withTimeout((async () => { while ((proxy.finished.get("GET /auth/me") || 0) <= before) await new Promise((done) => setTimeout(done, 15)); })(), 10_000, "actual auth refresh did not finish");
    assert.equal(await browser.evaluate(`document.querySelector('#create-stream-basic input').value`), "SQL browser actual payload");
    assert.equal(await browser.evaluate(`document.activeElement===document.querySelector('#create-stream-basic input')`), true);
  }
  if (bootstrap.with_images) {
  const first = proxy.gateNext("POST", "/media-assets/upload-sessions");
  await upload(browser, fileNames[0]); await withTimeout(first.entered.promise, 10_000, "session preparation did not enter");
  await phase("preparing"); await capture("first-preparing-pc");
  await browser.setViewport(390, 900); await capture("first-preparing-mobile");
  first.release.resolve(); await waitCards(browser, 1);
  await browser.setViewport(1440, 1000);
  proxy.loseUploadResponse();
  await upload(browser, fileNames[1]); await phase("outcome_unknown");
  await capture("second-response-unknown-pc");
  await clickVisible(browser, "[data-media-upload] button", /画像の状態を確認/);
  await browser.waitFor(`document.querySelector('[data-media-upload]')?.textContent.includes('画像変換を再開')`, Boolean, "committed upload could not be inspected without resending");
  await clickVisible(browser, "[data-media-upload] button", /画像変換を再開/);
  await waitCards(browser, 2);
  await upload(browser, fileNames[2]); await waitCards(browser, 3);
  const fourth = proxy.gateNext("POST", "/media-assets");
  await upload(browser, fileNames[3]); await withTimeout(fourth.entered.promise, 10_000, "fourth upload did not enter");
  await phase("uploading"); await capture("fourth-uploading-pc");
  await browser.setViewport(320, 900); await capture("fourth-uploading-mobile-320");
  fourth.release.resolve(); await waitCards(browser, 4);
  await browser.setViewport(390, 900); await capture("four-ready-mobile");
  await browser.setViewport(1440, 1000); await capture("four-ready-pc");
  assert.equal(proxy.started.get("POST /media-assets"), 4, "lost response recovery resent image bytes");
  assert.equal(proxy.lostUploads, 1);
  } else {
    await capture("actual-handler-create-pc");
    await browser.setViewport(390, 900); await capture("actual-handler-create-mobile");
    await browser.setViewport(1440, 1000);
  }
  const auth = proxy.gateNext("GET", "/auth/me");
  await browser.evaluate(`document.dispatchEvent(new Event('visibilitychange',{bubbles:true}));true`);
  await withTimeout(auth.entered.promise, 10_000, "background auth read did not enter before save");
  try {
    await clickVisible(browser, '#create-stream form button[type="submit"]', /^配信枠を作成$/);
    const deadline = Date.now() + 10_000;
    let saved = false;
    while (Date.now() < deadline) {
      saved = await browser.evaluate<boolean>(`!document.querySelector('[data-stream-create-view]')`);
      if (saved) break;
      await new Promise((done) => setTimeout(done, 50));
    }
    assert.equal(saved, true, "actual UI create did not succeed through the real handler");
  } finally { auth.release.resolve(); }
  assert.equal(proxy.creates.length, 1, "one save intent must issue exactly one create POST");
  assert.equal((proxy.creates[0].visual_settings as { cover_candidates: unknown[] }).cover_candidates.length, bootstrap.with_images ? 4 : 0);
  const receipt = { status: `PASS_REAL_UI_HTTP_${bootstrap.store_kind}_FIXTURE`, backend: bootstrap.store_kind, browser: await browser.browserVersion(), create_requests: proxy.creates, upload_requests: proxy.started.get("POST /media-assets") || 0, lost_upload_responses: proxy.lostUploads, refresh_cycles: 3, phases, request_counts: Object.fromEntries(proxy.started), completed_counts: Object.fromEntries(proxy.finished), aborted_requests: proxy.aborted, console_errors: browser.consoleErrorCount, limits: [`Actual Next UI and Go handler/${bootstrap.store_kind}; synthetic authenticated fixture transport; no production or Discord.`, "Mobile is a responsive CSS viewport, not a physical device."] };
  if (evidence) writeFileSync(resolve(evidence, "receipt.json"), JSON.stringify(receipt, null, 2));
  process.stdout.write(JSON.stringify({ status: receipt.status, create_requests: proxy.creates.length, upload_requests: receipt.upload_requests, refresh_cycles: 3 }) + "\n");
} catch (error) {
  await capture("failure").catch(() => undefined);
  if (evidence) writeFileSync(resolve(evidence, "failure-diagnostics.json"), JSON.stringify({ creates: proxy.creates, request_counts: Object.fromEntries(proxy.started), completed_counts: Object.fromEntries(proxy.finished), outcomes: proxy.outcomes, aborted: proxy.aborted }, null, 2));
  throw error;
}
finally {
  const cleanup = await Promise.allSettled([browser?.close(), proxy.close()]);
  await web.close();
  const failures = cleanup.filter((result): result is PromiseRejectedResult => result.status === "rejected");
  if (failures.length) throw new AggregateError(failures.map((result) => result.reason), "Browser fixture cleanup failed");
}
