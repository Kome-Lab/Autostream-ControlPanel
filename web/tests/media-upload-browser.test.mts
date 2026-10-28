import assert from "node:assert/strict";
import { mkdirSync, writeFileSync } from "node:fs";
import { resolve } from "node:path";
import test from "node:test";
import { BrowserHarness, ensureWebServer } from "./helpers/browser-harness.mts";
import { delay, withTimeout } from "./helpers/browser-process-timing.mts";
import { createP3MediaFixture, labels, cancelImageSelection, selectImage, p3ConsoleDiagnosticSource } from "./helpers/p3-media-browser-fixture.mts";
import { exerciseImageAttempt } from "./helpers/p3-media-browser-flow.mts";
import { createP3Proof, operationSelector, submitSelector } from "./helpers/p3-media-browser-proof.mts";
import { createBrowserRouteFixture, healthyRows, webRoot } from "./ui-browser-fixture.mts";
import { fileNames, select, waitCards } from "./stream-create-covers-browser-fixture.mts";
import { waitForAnimationFrames } from "./ui-browser-navigation-helpers.mts";
import { clickVisible } from "./ui-regression/visible-trigger.mts";

const evidence = process.env.AUTOSTREAM_MEDIA_UPLOAD_EVIDENCE_DIR;
const baseURL = process.env.AUTOSTREAM_BROWSER_BASE_URL || "http://127.0.0.1:3041";
assert.ok(["127.0.0.1", "localhost"].includes(new URL(baseURL).hostname), "this proof may use only a loopback Next fixture");
const conditions = [
  { id: "ja-light-1440", locale: "ja", theme: "light", width: 1440 },
  { id: "en-dark-390", locale: "en", theme: "dark", width: 390 },
] as const;

async function closeFixture(browser: BrowserHarness, fixture: ReturnType<typeof createP3MediaFixture>, primary: unknown, directory: string | undefined, locale: "ja" | "en") {
  fixture.releaseAll();
  const failures: unknown[] = [];
  try {
    await fixture.checkpoint(browser, "before-close", locale);
    if (directory) {
      mkdirSync(directory, { recursive: true });
      writeFileSync(resolve(directory, "initial-render-diagnostic.json"), JSON.stringify({ ...fixture.diagnostic, fetch_failure: browser.fetchFailureDiagnosticJSON ? JSON.parse(browser.fetchFailureDiagnosticJSON) : null, limits: ["Only final-resolver unmocked GET path classes or pathname-only SHA256; query, URL, headers and body are omitted.", "Console stores exact known static formats or unknown categories only; original calls and strict error counters are unchanged.", "A fatal harness may prevent the last in-page console read; unavailable checkpoints are explicit."] }, null, 2), { flag: "wx" });
    }
  } catch (failure) { failures.push(failure); }
  try { await browser.close(); } catch (cleanup) { failures.push(cleanup); }
  if (failures.length) throw new AggregateError(primary ? [primary, ...failures] : failures, "P3 browser owned diagnostics or cleanup failed");
}

async function openCreate(browser: BrowserHarness, fixture: ReturnType<typeof createP3MediaFixture>, base: string, locale: "ja" | "en", name: string) {
  const before = new Map(fixture.setupPaths.map(path => [path, browser.responses.get(path) || 0]));
  fixture.mark("navigating");
  await browser.navigate(`${base}/admin/streams/`);
  await fixture.checkpoint(browser, "loaded", locale);
  await browser.waitFor(`!!document.querySelector('main button')&&!document.querySelector('[data-stream-create-view]')`, Boolean, "list create entry missing");
  await fixture.checkpoint(browser, "list-dom", locale);
  // Initial auth can settle more than once. Require a completed response from
  // this navigation without imposing an exact-count mutation oracle on GETs.
  const responseDeadline = Date.now() + 10_000;
  const initialGETsCompleted = () => fixture.setupPaths.every(path => (browser.responses.get(path) || 0) >= before.get(path)! + 1);
  while (!initialGETsCompleted() && Date.now() < responseDeadline) { browser.assertNoFatalError(); await delay(20); }
  browser.assertNoFatalError();
  assert.equal(initialGETsCompleted(), true, "mounted auth and streams GETs must each complete at least once after navigation");
  await browser.waitForRequestHandlersIdle({ method: "GET" });
  await fixture.checkpoint(browser, "responses-idle", locale);
  // The locale can change after SSR. Observe the actual committed button's
  // React handler without invoking it or writing React/DOM state.
  await browser.waitFor(`(() => {const buttons=[...document.querySelectorAll('main button')].filter(button=>button.textContent.trim()===${JSON.stringify(labels[locale].openCreate)}&&button.getBoundingClientRect().width>0);if(buttons.length!==1||document.documentElement.lang!==${JSON.stringify(locale)})return false;const props=Object.keys(buttons[0]).find(key=>key.startsWith('__reactProps$'));return !!props&&typeof buttons[0][props]?.onClick==='function';})()`, Boolean, "localized list create handler must commit before native input");
  await fixture.checkpoint(browser, "react-commit", locale);
  await waitForAnimationFrames(browser);
  await browser.waitForRequestHandlersIdle({ method: "GET" });
  await fixture.checkpoint(browser, "before-create-click", locale);
  await clickVisible(browser, "main button", new RegExp(`^${labels[locale].openCreate}$`));
  await fixture.checkpoint(browser, "after-create-click", locale);
  await browser.waitFor(`!!document.querySelector('[data-stream-create-view] #create-stream-basic input')`, Boolean, "full-page draft missing");
  await browser.fillSelector("#create-stream-basic input", name);
  const automatic = "#create-stream-start button[role=checkbox]";
  if (await browser.evaluate(`document.querySelector('${automatic}')?.getAttribute('data-state')==='checked'`)) await clickVisible(browser, automatic);
  await select(browser, labels[locale].worker, "Worker One");
  await select(browser, labels[locale].encoder, "Encoder One");
  await fixture.checkpoint(browser, "form-ready", locale);
  fixture.mark("scenario");
}

test("P3 actual image upload UI exposes bounded work and preserves ready candidates", { timeout: 360_000 }, async t => {
  const server = await ensureWebServer(webRoot, baseURL);
  t.after(() => server.close());
  const warmup = await fetch(`${server.baseUrl}/admin/streams/`, { signal: AbortSignal.timeout(60_000) });
  assert.equal(warmup.status, 200);
  for (const condition of conditions) await t.test(condition.id, async () => {
    const browser = await BrowserHarness.launch(), fixture = createP3MediaFixture();
    const routes = createBrowserRouteFixture(fixture.observeBrowserRoutes(browser), fixture.route);
    routes.healthResponse = { body: healthyRows };
    routes.uiPreferenceResponse = { body: { theme_id: "autostream", color_mode: condition.theme, revision: 4 } };
    const directory = evidence ? resolve(evidence, condition.id) : undefined;
    const proof = createP3Proof(browser, directory, condition.id, condition.locale);
    const context = { browser, fixture, proof, locale: condition.locale, width: condition.width };
    let primary: unknown;
    try {
      await browser.configureDeterministicDocument({ source: `${p3ConsoleDiagnosticSource};localStorage.setItem('autostream.controlPanel.locale',${JSON.stringify(condition.locale)});localStorage.setItem('autostream.ui_preference',${JSON.stringify(JSON.stringify({ theme_id: "autostream", color_mode: condition.theme }))});sessionStorage.setItem('autostream.csrf_token','browser-fixture-csrf');`, timezone: "Asia/Tokyo", locale: condition.locale === "ja" ? "ja-JP" : "en-US" });
      await browser.setViewport(condition.width, 1000);
      await openCreate(browser, fixture, server.baseUrl, condition.locale, `P3 ${condition.id} retained input`);
      await browser.waitFor(`document.documentElement.classList.contains('dark')===${condition.theme === "dark"}`, Boolean, "the requested DB-backed theme did not apply");
      await select(browser, labels[condition.locale].coverSource, labels[condition.locale].upload);
      const beforeCancel = fixture.app.state.uploads;
      await cancelImageSelection(browser, "cover"); await waitForAnimationFrames(browser);
      assert.equal(fixture.app.state.uploads, beforeCancel); assert.equal((await proof.observe()).operation, null);

      const readyAssets: string[] = [];
      for (let index = 0; index < 4; index++) {
        if (index === 1) {
          const failed = await exerciseImageAttempt(context, "cover", fileNames[index], index, index, "failed");
          if (condition.locale === "ja") await proof.layouts("cover-known-failed-actions", condition.width);
          const sent = fixture.app.state.uploads, deleted = fixture.app.state.deletes.length;
          await proof.action(condition.locale, "discard");
          await browser.waitFor(`!document.querySelector('${operationSelector}')`, Boolean, "explicit failed-image dismissal missing");
          assert.equal(fixture.app.state.deletes.length, deleted, "confirmed cleanup is not repeated on dismissal");
          const retried = await exerciseImageAttempt(context, "cover", fileNames[index], index, index);
          assert.notEqual(retried.operationID, failed.operationID, "same-file reselection is a new explicit operation");
          assert.equal(fixture.app.state.uploads, sent + 1); readyAssets.push(retried.assetID);
        } else {
          const result = await exerciseImageAttempt(context, "cover", fileNames[index], index, index, index === 2 ? "unknown" : "normal", { layouts: index === 3 });
          readyAssets.push(result.assetID);
        }
        await waitCards(browser, index + 1);
        assert.equal(await browser.evaluate(`([...document.querySelectorAll('[data-cover-candidate-actions] button')].filter(el=>['開始時の画像','Start image'].includes(el.textContent.trim())).length)`), 1, "only one ready cover is selected for the start");
      }
      assert.equal([...fixture.app.state.assets.values()].filter(asset => asset.usage_type === "video_cover" && !asset.deleted_at).length, 4);
      await assert.rejects(selectImage(browser, "cover", "forbidden-fifth.png"), /image upload disabled/);

      // Shared presets remain available at the upload-original limit and must
      // survive every following failure, cleanup and background transition.
      await select(browser, labels[condition.locale].coverSource, labels[condition.locale].preset);
      await select(browser, labels[condition.locale].coverPreset, "Shared cover (r7)"); await waitCards(browser, 5);
      await select(browser, labels[condition.locale].backgroundMode, labels[condition.locale].image);
      const backgroundName = `background_${condition.locale}_long_filename_for_state_visibility_and_explicit_retry.png`;
      const failedBackground = await exerciseImageAttempt(context, "background", backgroundName, 5, 4, "failed", { unknownCleanup: true });
      if (condition.locale === "en") await proof.layouts("background-cleanup-confirmed-failed-actions", condition.width);
      const background = await exerciseImageAttempt(context, "background", backgroundName, 5, 4, "unknown", { trigger: () => proof.action(condition.locale, "retry"), layouts: true });
      assert.notEqual(background.operationID, failedBackground.operationID);
      assert.equal(fixture.app.state.deletes.length, 2, "only the failed cover and failed background are deleted");
      assert.equal(fixture.count("DELETE", /^\/media-assets\/[^/]+$/), 2, "unexpected or shared-asset DELETE paths cannot escape the request oracle");
      assert.equal(fixture.app.state.deletes.some(path => path.includes("shared")), false);
      for (const assetID of readyAssets) assert.equal(fixture.app.state.assets.get(assetID)?.deleted_at, undefined, "a ready candidate must not be deleted with a failed neighbor");
      assert.equal((await proof.observe()).candidates, 5);
      assert.equal(await browser.evaluate(`document.querySelector('#create-stream-visual [role=img]')?.getAttribute('style')?.includes('blob:')`), true, "ready background uses the selected file preview");
      await proof.capture("ready-four-plus-shared-preset-and-background");
      await browser.waitFor(`document.querySelector('${submitSelector}')?.disabled===false`, Boolean, "only ready images may release the create save");
      await clickVisible(browser, submitSelector, new RegExp(`^${labels[condition.locale].create}$`));
      await browser.waitFor(`!document.querySelector('[data-stream-create-view]')`, Boolean, "successful ready-only save must return to the list");
      assert.equal(fixture.app.state.creates.length, 1);
      const submitted = fixture.app.state.creates[0].visual_settings as { cover_candidates: { variant_id: string }[]; background_asset_id: string; background_variant_id: string };
      assert.equal(submitted.cover_candidates.length, 5); assert.equal(submitted.background_asset_id, background.assetID);
      assert.equal(submitted.background_variant_id, background.assetID.replace("asset-", "variant-"));
      const savedIDs = new Set(submitted.cover_candidates.map(item => item.variant_id));
      for (const assetID of readyAssets) assert.equal(savedIDs.has(assetID.replace("asset-", "variant-")), true);
      assert.equal(savedIDs.has(failedBackground.assetID.replace("asset-", "variant-")), false);
      await browser.reload(); await browser.waitFor(`!!document.querySelector('button[aria-label="詳細"],button[aria-label="Details"]')`, Boolean, "saved stream detail action missing");
      await clickVisible(browser, 'button[aria-label="詳細"],button[aria-label="Details"]'); await waitCards(browser, 5);
      assert.equal(browser.consoleErrorCount, 0); browser.assertNoFatalError();
      if (directory) {
        mkdirSync(directory, { recursive: true });
        writeFileSync(resolve(directory, "receipt.json"), JSON.stringify({ status: "PASS_ACTUAL_UI_SYNTHETIC_API_FIXTURE", condition, browser: await browser.browserVersion(), observations: proof.rows, requests: fixture.requests, uploads: fixture.app.state.uploads, scoped_deletes: fixture.app.state.deletes.map((path, index) => ({ path, session: fixture.app.state.deleteSessions[index] })), create_count: 1,
          limits: ["Actual Next and BrowserHarness; controlled synthetic API metadata, not SQL/Discord or production.", "Committed-unknown HTTP503 fixture is not a physical network disconnect.", "320/390 are CSS viewports, not physical mobile devices.", "200% uses explicit CSS magnification, not native browser zoom."] }, null, 2), { flag: "wx" });
      }
    } catch (error) { primary = error; await fixture.checkpoint(browser, "failure", condition.locale); await proof.capture("failure", true).catch(() => undefined); throw error; }
    finally { await closeFixture(browser, fixture, primary, directory, condition.locale); }
  });

  await t.test("late conversion after a real session-expiry unmount cannot enter a new draft", async () => {
    const browser = await BrowserHarness.launch(), fixture = createP3MediaFixture();
    const routes = createBrowserRouteFixture(fixture.observeBrowserRoutes(browser), fixture.route); routes.healthResponse = { body: healthyRows };
    const directory = evidence ? resolve(evidence, "late-generation") : undefined;
    const proof = createP3Proof(browser, directory, "late-generation");
    const originalAuth = routes.authResponse, conversion = fixture.gate("POST", /^\/media-assets\/asset-\d+\/variants$/);
    let primary: unknown;
    try {
      await browser.configureDeterministicDocument({ source: `${p3ConsoleDiagnosticSource};localStorage.setItem('autostream.controlPanel.locale','ja');sessionStorage.setItem('autostream.csrf_token','browser-fixture-csrf');`, timezone: "Asia/Tokyo", locale: "ja-JP" });
      await browser.setViewport(390, 1000); await openCreate(browser, fixture, server.baseUrl, "ja", "old draft before session expiry");
      await select(browser, labels.ja.coverSource, labels.ja.upload); await selectImage(browser, "cover", "late-completion.png");
      await withTimeout(conversion.entered.promise, 10_000, "late conversion did not enter");
      await proof.phase("cover", "converting", 0, "late-completion.png"); await proof.capture("old-draft-converting");
      await browser.waitForRequestHandlersIdle({ pathname: "/auth/me", method: "GET" }); browser.clearRequestCounts("/auth/me");
      routes.authResponse = { status: 401, body: { code: "unauthorized" } };
      await browser.evaluate("document.dispatchEvent(new Event('visibilitychange',{bubbles:true}));true");
      await browser.waitFor("location.pathname==='/login/'&&new URLSearchParams(location.search).get('reason')==='session_expired'", Boolean, "the real session guard must unmount the old draft");
      routes.authResponse = originalAuth;
      await openCreate(browser, fixture, server.baseUrl, "ja", "new generation input retained");
      conversion.release.resolve();
      await browser.waitForRequestHandlersIdle({ pathname: "/media-assets/asset-1/variants", method: "POST" }); await waitForAnimationFrames(browser);
      const state = await proof.capture("new-generation-after-old-response");
      assert.equal(state.title, "new generation input retained"); assert.equal(state.candidates, 0); assert.equal(state.operation, null);
      assert.equal(fixture.app.state.uploads, 1); assert.equal(fixture.app.state.deletes.length, 0, "abort alone cannot authorize deleting a possibly-processing original");
      assert.equal(fixture.app.state.assets.get("asset-1")?.owner_type, "upload_draft", "the abandoned server draft remains owned by normal retention");
      assert.equal(browser.consoleErrorCount, 0); browser.assertNoFatalError();
    } catch (error) { primary = error; await fixture.checkpoint(browser, "failure", "ja"); await proof.capture("failure", true).catch(() => undefined); throw error; }
    finally { routes.authResponse = originalAuth; conversion.release.resolve(); await closeFixture(browser, fixture, primary, directory, "ja"); }
  });
});
