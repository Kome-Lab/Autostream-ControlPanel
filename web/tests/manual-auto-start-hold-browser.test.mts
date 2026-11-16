import assert from "node:assert/strict";
import { spawn } from "node:child_process";
import { resolve } from "node:path";
import test from "node:test";
import { BrowserHarness, captureNextGeneratedFiles, ensureWebServer, restoreNextGeneratedFiles } from "./helpers/browser-harness.mts";
import { terminateChildProcess } from "./helpers/browser-process-timing.mts";
import { withPreservedNextBuildDirectory } from "./helpers/run-ui-foundation-browser.mts";
import { createBrowserRouteFixture, currentUser, webRoot } from "./ui-browser-fixture.mts";
import { scrollSelectorIntoView, waitForAnimationFrames } from "./ui-browser-navigation-helpers.mts";
import type { AutoStartResumeRequest } from "../src/features/streams/stream-auto-start-hold.ts";

const stream = { id: "msr-browser-stream", name: "手動保留の合成配信", status: "ready", settings_revision: "settings-17", updated_at: "2026-11-16T01:00:00.000001Z", archive_run_id: "run-4", auto_start_trigger: "discord_voice_join" };
const held = { stream_id: stream.id, known: true, held: true, revision: 7, reason: "manual_stop", actor_user_id: "operator", original_attempt_id: "stop-original", pending_attempt_id: "", created_at: "2026-11-16T00:00:00Z", updated_at: "2026-11-16T01:00:00Z" };
const holdPath = `/streams/${stream.id}/auto-start-hold`, resumePath = `/streams/${stream.id}/auto-start/resume`;
const panel = "[data-manual-auto-start-hold]";
const resumeTrigger = `${panel} button[data-slot="alert-dialog-trigger"]`;
const confirmationAction = '[role="alertdialog"] button[data-confirm-action]';
const context = { OwnerID: "cp-owner", Scope: stream.id, FrameRef: "frame-9", SourceRevision: 9 };
const fact = <T,>(value: T) => ({ State: "OBSERVED", Value: value, Observation: context });
const boundary = (value: string, revision = context.SourceRevision, kind: "hold" | "start" = "hold") => ({ Address: { Kind: kind === "hold" ? "CP_LIFECYCLE" : "PROJECTION", OwnerID: "cp-owner", Resource: { Kind: "STREAM", OwnerID: "cp-owner", ResourceID: stream.id }, SubBoundary: kind === "hold" ? "RECEIPT_SAVE" : "CP_OUTCOME" }, Knowledge: "KNOWN", Presence: "PRESENT", Outcome: { State: "OBSERVED", Value: value, Observation: { ...context, SourceRevision: revision } } });

async function startServer() {
  const baseUrl = process.env.AUTOSTREAM_MSR_BROWSER_BASE_URL || "http://127.0.0.1:3019";
  if (process.env.AUTOSTREAM_MSR_BROWSER_WEBPACK !== "true") return ensureWebServer(webRoot, baseUrl);
  // Explicit local test selection for a dependency junction which Turbopack
  // cannot traverse. This never alters the production config or CI baseline.
  const url = new URL(baseUrl), generated = captureNextGeneratedFiles(webRoot);
  assert.equal(url.hostname, "127.0.0.1", "this fixture may bind loopback only");
  const child = spawn(process.execPath, [resolve(webRoot, "node_modules/next/dist/bin/next"), "dev", "--webpack", "--hostname", url.hostname, "--port", url.port], {
    cwd: webRoot, stdio: "pipe", windowsHide: true,
    env: { ...process.env, NEXT_PUBLIC_AUTOSTREAM_DEMO: "false", NEXT_TELEMETRY_DISABLED: "1" },
  });
  let output = "";
  child.stdout.on("data", chunk => { output = `${output}${chunk}`.slice(-4000); });
  child.stderr.on("data", chunk => { output = `${output}${chunk}`.slice(-4000); });
  const close = async () => { try { await terminateChildProcess(child); } finally { restoreNextGeneratedFiles(generated); } };
  try {
    const deadline = Date.now() + 60_000;
    while (Date.now() < deadline) {
      if (child.exitCode !== null || child.signalCode !== null) throw Error(`Owned Next Webpack server exited: ${output}`);
      try { if ((await fetch(baseUrl, { signal: AbortSignal.timeout(2000) })).status < 500) return { baseUrl, close }; } catch { /* bounded readiness observation */ }
      await new Promise(resolveWait => setTimeout(resolveWait, 100));
    }
    throw Error(`Owned Next Webpack server readiness deadline: ${output}`);
  } catch (error) { await close(); throw error; }
}

test("MSR-UI-BROWSER: normal Stream detail preserves confirmation and same-request result boundaries", { timeout: 180_000 }, async t => {
  await withPreservedNextBuildDirectory(webRoot, async () => {
    const server = await startServer();
    let browser: BrowserHarness | undefined;
    let primaryFailure: unknown;
    try {
      browser = await BrowserHarness.launch();
      const activeBrowser = browser;
      await activeBrowser.setViewport(1440, 1000);
      await activeBrowser.installNewDocumentScript(`try { localStorage.setItem('autostream.controlPanel.locale','ja'); sessionStorage.setItem('autostream.csrf_token','msr-browser-fixture'); } catch {}`);
      let currentStream = { ...stream }, currentHold = { ...held }, mode = "rejected";
      const posts: AutoStartResumeRequest[] = [], inspected: string[] = [], genericInspected: string[] = [];
      const fixture = createBrowserRouteFixture(activeBrowser, ({ method, url, postData }) => {
        const path = new URL(url).pathname.replace(/\/+$/, "");
        if (method === "GET" && ["visual-settings", "video-cover-state"].some(kind => path === `/streams/${stream.id}/${kind}`)) return { status: 404, body: { code: "not_configured" }, requiredResponse: false };
        if (path === holdPath && method === "GET") return { body: { stream: currentStream, hold: currentHold } };
        if (path === resumePath && method === "POST") {
          const request = JSON.parse(postData || "null") as AutoStartResumeRequest;
          posts.push(request);
          assert.equal(request.expected_hold_revision, held.revision);
          assert.equal(request.expected_settings_revision, currentStream.settings_revision);
          assert.equal(request.expected_stream_revision, currentStream.updated_at);
          assert.equal(request.expected_run_id, stream.archive_run_id);
          assert.ok(request.request_id && request.request_id !== held.original_attempt_id);
          assert.equal(mode, "ack-unavailable", "stale or revoked confirmations must never dispatch");
          currentHold = { ...held, held: false, revision: 9, original_attempt_id: request.request_id };
          // The product browser receives no usable operation receipt. A
          // separate real HTTP test destroys the POST socket after dispatch.
          return { status: 503, body: { code: "receipt_unavailable" } };
        }
        const match = path.match(new RegExp(`^/streams/${stream.id}/auto-start/resume-attempts/([^/]+)$`));
        if (match && method === "GET") {
          inspected.push(decodeURIComponent(match[1]));
          assert.equal(inspected.at(-1), posts[0]?.request_id);
          return { body: { stream: currentStream, hold: currentHold, attempt: {
            request: { ...posts[0], stream_id: stream.id, operation: "explicit_resume" }, hold: currentHold,
            condition: fact(false), hold_commit: boundary("SUCCESS_AT_BOUNDARY"), start_result: boundary("NO_EFFECT_PROVEN", context.SourceRevision, "start"), gate: "NOT_QUALIFIED",
          } } };
        }
        const generic = path.match(new RegExp(`^/streams/${stream.id}/auto-start/hold-attempts/([^/]+)$`));
        if (generic && method === "GET") {
          genericInspected.push(decodeURIComponent(generic[1]));
          assert.equal(genericInspected.at(-1), currentHold.pending_attempt_id);
          return { body: { stream: currentStream, hold: currentHold, attempt: { request: { request_id: currentHold.pending_attempt_id, stream_id: stream.id, operation: "manual_force_stop" }, hold: currentHold, hold_commit: boundary("SUCCESS_AT_BOUNDARY", currentHold.revision), gate: "HELD" } } };
        }
        return null;
      });
      fixture.streamsResponse = { body: [currentStream] };
      fixture.authResponse = { body: currentUser };
      fixture.observeBootstrapCancellation = true;
      fixture.observeInFlightReadCancellation = true;
      const clickButtonText = async (value: string) => {
        await activeBrowser.evaluate(`(() => { const button=[...document.querySelectorAll('button')].find(element=>element.textContent?.trim()===${JSON.stringify(value)}&&element.getClientRects().length>0); if(!(button instanceof HTMLElement))throw Error('fixture button missing');button.scrollIntoView({block:'center',inline:'nearest'});return true; })()`);
        await waitForAnimationFrames(activeBrowser);
        const point = await activeBrowser.evaluate<{ x: number; y: number }>(`(() => { const button=[...document.querySelectorAll('button')].find(element=>element.textContent?.trim()===${JSON.stringify(value)}&&element.getClientRects().length>0); if(!(button instanceof HTMLButtonElement)||button.disabled)throw Error('fixture button unavailable');const rect=button.getBoundingClientRect(),x=rect.left+rect.width/2,y=rect.top+rect.height/2;if(x<0||x>innerWidth||y<0||y>innerHeight||!button.contains(document.elementFromPoint(x,y)))throw Error('fixture button is not a native input target');return{x,y}; })()`);
        await activeBrowser.clickAt(point.x, point.y);
      };
      const openDetail = async () => {
        await activeBrowser.navigate(`${server.baseUrl}/admin/streams/`);
        try { await activeBrowser.waitFor(`document.body.textContent?.includes(${JSON.stringify(stream.name)}) === true`, Boolean, "normal Stream row"); }
        catch (error) {
          console.error("MSR_BROWSER_FIXTURE_OBSERVATION", JSON.stringify({ phase: "normal-stream-row", authGETs: activeBrowser.requests.get("/auth/me") || 0, streamGETs: activeBrowser.requests.get("/streams") || 0, consoleErrors: activeBrowser.consoleErrorCount, view: await activeBrowser.evaluate<string>(`location.pathname + ':' + document.body.textContent?.slice(0,1500)`) }));
          throw error;
        }
        await scrollSelectorIntoView(activeBrowser, 'button[aria-label="詳細"]');
        await activeBrowser.clickSelector('button[aria-label="詳細"]');
        try { await activeBrowser.waitFor(`document.querySelector('${panel} [data-hold-state="held"]') !== null`, Boolean, "durable Hold display"); }
        catch (error) {
          console.error("MSR_BROWSER_FIXTURE_OBSERVATION", JSON.stringify({ phase: "normal-detail-hold", holdGETs: activeBrowser.requests.get(holdPath) || 0, holdResponses: activeBrowser.responses.get(holdPath) || 0, statuses: activeBrowser.responseStatuses.get(holdPath), panel: await activeBrowser.evaluate<string>(`document.querySelector('${panel}')?.textContent?.slice(0,1500) || 'panel_missing'`) }));
          throw error;
        }
      };
      const openConfirmation = async () => {
        await activeBrowser.waitFor(`document.querySelector(${JSON.stringify(resumeTrigger + ":not([disabled])")}) !== null`, Boolean, "current Resume trigger");
        await scrollSelectorIntoView(activeBrowser, resumeTrigger);
        await activeBrowser.clickSelector(resumeTrigger);
        await activeBrowser.waitFor(`document.querySelector('[role="alertdialog"]')?.textContent?.includes('開始条件が成立している場合、配信が開始される') === true`, Boolean, "explicit Resume warning");
        assert.equal(posts.length, 0, "opening confirmation does not release Hold");
      };
      await openDetail();

      await t.test("a changed configuration rejects the old browser confirmation", async () => {
        await openConfirmation();
        currentStream = { ...currentStream, settings_revision: "settings-18" };
        fixture.streamsResponse = { body: [currentStream] };
        await activeBrowser.clickSelector(confirmationAction);
        await activeBrowser.waitFor(`document.querySelector('${panel}')?.textContent?.includes('対象・Run・設定またはHold Revisionが変わりました') === true`, Boolean, "stale confirmation rejection");
        assert.equal(posts.length, 0);
        assert.equal(currentHold.held, true);
      });

      await t.test("fresh permission revocation sends no browser mutation", async () => {
        await openConfirmation();
        fixture.authResponse = { body: { ...currentUser, permissions: ["streams.read"] } };
        await activeBrowser.clickSelector(confirmationAction);
        await activeBrowser.waitFor(`document.querySelector('${panel}')?.textContent?.includes('現在の権限またはSessionを確認できません') === true`, Boolean, "fresh permission rejection");
        assert.equal(posts.length, 0);
        assert.equal(currentHold.held, true);
        await clickButtonText("キャンセル");
        fixture.authResponse = { body: currentUser };
      });

      await t.test("ambiguous ACK survives reload and only inspects the original request", async () => {
        mode = "ack-unavailable";
        await openDetail();
        await openConfirmation();
        await activeBrowser.clickSelector(confirmationAction);
        await activeBrowser.waitFor(`document.querySelector('${panel}')?.textContent?.includes('結果はUNKNOWNまたは未完了です') === true`, Boolean, "ambiguous boundary display");
        assert.equal(posts.length, 1);
        await waitForAnimationFrames(activeBrowser);
        assert.equal(posts.length, 1, "no automatic Resume replay");
        await activeBrowser.reload();
        await activeBrowser.waitFor(`document.body.textContent?.includes(${JSON.stringify(stream.name)}) === true`, Boolean, "normal row after reload");
        await scrollSelectorIntoView(activeBrowser, 'button[aria-label="詳細"]');
        await activeBrowser.clickSelector('button[aria-label="詳細"]');
        await activeBrowser.waitFor(`document.querySelector('${panel}')?.textContent?.includes('同じ要求の結果を照会') === true`, Boolean, "original request recovery");
        await clickButtonText("同じ要求の結果を照会");
        await activeBrowser.waitFor(`document.querySelector('${panel} [data-resume-boundaries]')?.textContent?.includes('開始していません') === true`, Boolean, "same-request condition false result");
        const text = await activeBrowser.evaluate<string>(`document.querySelector('${panel}')?.textContent || ''`);
        assert.ok(text.includes("確定を確認") && text.includes("不成立") && text.includes("開始していません"));
        assert.equal(posts.length, 1);
        assert.deepEqual(inspected, [posts[0].request_id]);
        assert.equal(currentHold.held, false);
      });
      await t.test("a reader inspects a server-owned force Hold without claiming physical Stop", async () => {
        currentHold = { ...currentHold, held: true, revision: 10, reason: "manual_force_stop", original_attempt_id: "server-force-original", pending_attempt_id: "server-force-original" };
        currentStream = { ...currentStream, status: "stopping", updated_at: "2026-11-16T01:05:00Z" };
        fixture.streamsResponse = { body: [currentStream] };
        fixture.authResponse = { body: { ...currentUser, permissions: ["streams.read"] } };
        await openDetail();
        await activeBrowser.waitFor(`document.querySelector('${panel} button[data-manual-hold-attempt-inspect]:not([disabled])') !== null`, Boolean, "read-scoped original Hold inspector");
        await scrollSelectorIntoView(activeBrowser, `${panel} button[data-manual-hold-attempt-inspect]`);
        await activeBrowser.clickSelector(`${panel} button[data-manual-hold-attempt-inspect]`);
        await activeBrowser.waitFor(`document.querySelector('${panel} [data-manual-hold-attempt-result]')?.textContent?.includes('実際の停止結果はUNKNOWN') === true`, Boolean, "Hold proof must not become physical Stop proof");
        const text = await activeBrowser.evaluate<string>(`document.querySelector('${panel} [data-manual-hold-attempt-result]')?.textContent || ''`);
        assert.ok(text.includes("確定を確認") && text.includes("HELD") && text.includes("UNKNOWN"));
        assert.deepEqual(genericInspected, ["server-force-original"]);
        assert.equal(posts.length, 1); assert.deepEqual(inspected, [posts[0].request_id]);
      });
      await activeBrowser.waitForRequestHandlersIdle({ pathname: holdPath, method: "GET" });
      await activeBrowser.waitForRequestHandlersIdle({ pathname: "/streams", method: "GET" });
      activeBrowser.assertNoFatalError();
      t.diagnostic("Native Chrome, actual Next Stream detail and confirmation. Synthetic API fixtures only; no provider, media or environment qualification.");
    } catch (error) {
      primaryFailure = error;
      throw error;
    } finally {
      const cleanup = await Promise.allSettled([browser?.close(), server.close()]);
      const failed = cleanup.filter((value): value is PromiseRejectedResult => value.status === "rejected").map(value => value.reason);
      if (failed.length) throw new AggregateError(primaryFailure ? [primaryFailure, ...failed] : failed, "Native Resume browser operation or owned cleanup failed");
    }
  });
});
