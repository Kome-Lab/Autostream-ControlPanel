import type { TestContext } from "node:test";
import assert from "node:assert/strict";
import { BrowserHarness } from "./helpers/browser-harness.mts";
import { type BrowserRouteFixture, healthyRows, controlPlatformStream, controlPlatformCoverState, currentUser, controlPlatformCoverPath } from "./ui-browser-fixture.mts";
import { setStoredDisplay, scrollSelectorIntoView, waitForAnimationFrames, deferred } from "./ui-browser-navigation-helpers.mts";



export async function runVisualSettingsScenario(t: TestContext, browser: BrowserHarness, server: { baseUrl: string }, fixture: Pick<BrowserRouteFixture, "healthResponse" | "streamsResponse" | "controlPlatformVisualResponse" | "controlPlatformCoverResponse" | "controlPlatformCoverMethods" | "controlPlatformCoverBodies" | "authResponse" | "authNextResponse" | "controlPlatformCoverWriteResponse">) {

  await t.test("Stream detail presents visual snapshots and cover actions preserve request-count and applied-state boundaries", async () => {
    const coverMethodCount = (method: string) => fixture.controlPlatformCoverMethods.filter((value) => value === method).length;
    const waitForCoverMethod = async (method: string, expected: number) => {
      const deadline = Date.now() + 20_000;
      while (coverMethodCount(method) < expected && Date.now() < deadline) {
        browser.assertNoFatalError();
        await new Promise<void>((resolve) => setTimeout(resolve, 25));
      }
      assert.equal(coverMethodCount(method), expected, `cover ${method} count must be exactly ${expected}`);
    };
    const waitForAuthCount = async (counts: ReadonlyMap<string, number>, minimum: number, description: string) => {
      const deadline = Date.now() + 20_000;
      while ((counts.get("/auth/me") || 0) < minimum && Date.now() < deadline) {
        browser.assertNoFatalError();
        await new Promise<void>((resolve) => setTimeout(resolve, 25));
      }
      browser.assertNoFatalError();
      assert.ok((counts.get("/auth/me") || 0) >= minimum, description);
    };
    const limitedUser = { user: { id: "visual-reader", username: "visual-reader", roles: ["viewer"] }, permissions: ["streams.read"] };
    fixture.healthResponse = { body: healthyRows };
    fixture.streamsResponse = { body: [controlPlatformStream] };
    fixture.controlPlatformVisualResponse = { body: {
      stream_id: controlPlatformStream.id,
      background_mode: "image",
      header_title_mode: "custom",
      header_title_value: "配信ビジュアル見出し",
      discord_target_mode: "preset",
      discord_target_preset_revision: 3,
      discord_snapshot_revision: 5,
      discord_preset_deleted: true,
      cover_source: "upload",
      cover_variant_id: "variant-cover",
      cover_candidates: [{ name: "保存済みフタ", source: "upload", asset_id: "asset-cover", variant_id: "variant-cover" }],
      cover_start_active: false,
      revision: 2,
    } };
    fixture.controlPlatformCoverResponse = { body: controlPlatformCoverState(false, 1, false, 1, "idle") };
		fixture.controlPlatformCoverMethods = [];
		fixture.controlPlatformCoverBodies = [];
    fixture.authResponse = { body: limitedUser };
    await setStoredDisplay(browser, "ja", "light");
    await browser.navigate(`${server.baseUrl}/admin/streams/`);
    await browser.waitFor(`document.body.textContent?.includes(${JSON.stringify(controlPlatformStream.name)}) === true`, Boolean, "control-platform stream row missing");
    await browser.clickSelector('button[aria-label="詳細"]');
    await browser.waitFor(`document.querySelector('section[aria-label="配信ビジュアルとVideo Cover"]') !== null`, Boolean, "visual detail panel missing");
    await browser.waitFor(
      `document.body.textContent?.includes('配信ビジュアル見出し') === true && document.body.textContent?.includes('保存済みsnapshotを継続します') === true`,
      Boolean,
      "saved visual snapshot did not finish rendering",
    );
    const visualSnapshot = await browser.evaluate<{ title: boolean; layers: boolean; warning: boolean; showDisabled: boolean }>(`(() => ({
      title: document.body.textContent?.includes('配信ビジュアル見出し') === true,
      layers: document.body.textContent?.includes('背景・参加者 → フタ画像 → ウォーターマーク → 配信・録画・プレビュー') === true
        && document.querySelector('[data-watermark-layer="topmost"]') !== null,
      warning: document.body.textContent?.includes('保存済みsnapshotを継続します') === true,
      showDisabled: document.querySelector('button[aria-label="Video Coverを表示"]')?.hasAttribute('disabled') === true,
    }))()`);
    assert.deepEqual(visualSnapshot, { title: true, layers: true, warning: true, showDisabled: true });
    await browser.waitFor(`(() => {
      const image = document.querySelector('img[alt="選択したフタ画像の設定プレビュー"]');
      return image instanceof HTMLImageElement && image.complete && image.naturalWidth > 0
        && document.body.textContent?.includes('設定プレビュー（実映像は配信プレビューで確認）') === true
        && document.querySelector('img[alt="Encoderで反映確認済みのフタ画像"]') === null;
    })()`, Boolean, "saved settings image must decode without claiming Encoder application");
    await scrollSelectorIntoView(browser, 'button[aria-label="Video Coverを表示"]');
    await browser.clickSelector('button[aria-label="Video Coverを表示"]');
    await waitForAnimationFrames(browser);
    assert.equal(fixture.controlPlatformCoverMethods.filter((method) => method === "PUT").length, 0, "permission denied UI must send zero cover requests");

    fixture.authResponse = { body: currentUser };
		fixture.controlPlatformCoverMethods = [];
		fixture.controlPlatformCoverBodies = [];
    fixture.controlPlatformCoverWriteResponse = { body: controlPlatformCoverState(true, 2, true, 2, "applied") };
		await browser.waitForRequestHandlersIdle({ pathname: "/auth/me", method: "GET" });
    const initialAuthResponses = browser.responses.get("/auth/me") || 0;
		await browser.reload();
    await waitForAuthCount(browser.responses, initialAuthResponses + 1, "reloaded mounted auth must complete at least one new response");
		await browser.waitForRequestHandlersIdle({ pathname: "/auth/me", method: "GET" });
    await browser.waitFor(`document.body.textContent?.includes(${JSON.stringify(controlPlatformStream.name)}) === true`, Boolean, "control-platform stream did not reload");
    await browser.clickSelector('button[aria-label="詳細"]');
    await browser.waitFor(`document.querySelector('button[aria-label="Video Coverを表示"]:not([disabled])') !== null`, Boolean, "show cover action did not become available");
    await browser.waitForRequestHandlersIdle({ pathname: "/auth/me", method: "GET" });
    const observerAuthRequests = browser.requests.get("/auth/me") || 0, observerAuthResponses = browser.responses.get("/auth/me") || 0;
    const permissionRelease = deferred();
    const showRelease = deferred();
    fixture.authNextResponse = { body: currentUser, waitUntil: permissionRelease.promise };
    fixture.authResponse = { body: currentUser };
    fixture.controlPlatformCoverWriteResponse = { body: controlPlatformCoverState(true, 2, true, 2, "applied"), waitUntil: showRelease.promise };
    try {
      await browser.evaluate("window.dispatchEvent(new Event('visibilitychange')); true");
      await waitForAuthCount(browser.requests, observerAuthRequests + 1, "the controlled observer auth request must start");
      assert.equal(fixture.authNextResponse, undefined, "only one observer response may be held");
      await browser.waitFor(`document.querySelector('button[aria-label="Video Coverを表示"]:not([disabled])') !== null`, Boolean, "cached successful permission must remain usable during observer refresh");
      const freshPaths = ["/auth/me", `/streams/${controlPlatformStream.id}`, `/streams/${controlPlatformStream.id}/visual-settings`, "/service-health", controlPlatformCoverPath];
      const readsBefore = new Map(freshPaths.map((path) => [path, browser.requests.get(path) || 0]));
      const coverGETsBefore = coverMethodCount("GET");
      await scrollSelectorIntoView(browser, 'button[aria-label="Video Coverを表示"]');
      await browser.clickSelector('button[aria-label="Video Coverを表示"]');
      await waitForCoverMethod("PUT", 1);
      for (const path of freshPaths.filter(path => path !== controlPlatformCoverPath)) assert.ok((browser.requests.get(path) || 0) >= (readsBefore.get(path) || 0) + 1, "the action must freshly read each relevant authority endpoint");
      assert.equal(coverMethodCount("GET"), coverGETsBefore + 1, "the action must freshly GET the cover state independently of its PUT");
      await waitForAuthCount(browser.responses, observerAuthResponses + 1, "the fresh action auth read must complete while the observer is held");
      assert.ok((browser.requests.get("/auth/me") || 0) - observerAuthRequests > (browser.responses.get("/auth/me") || 0) - observerAuthResponses, "the fresh action must finish independently while the observer GET remains held");
      await browser.clickSelector('button[aria-label="Video Coverを表示"]');
      await waitForAnimationFrames(browser);
      assert.equal(coverMethodCount("PUT"), 1, "duplicate pending show must send zero additional requests");
      showRelease.resolve();
      await browser.waitFor(`(() => {
        const image = document.querySelector('img[alt="Encoderで反映確認済みのフタ画像"]');
        const state = [...document.querySelectorAll('dt')].find((element) => element.textContent === 'Encoderの反映確認')?.nextElementSibling;
        return image instanceof HTMLImageElement && image.complete && image.naturalWidth > 0 && state?.textContent === 'ON r2';
      })()`, Boolean, "confirmed cover image must decode with authoritative Encoder revision");
    } finally {
      permissionRelease.resolve(); showRelease.resolve(); fixture.authNextResponse = undefined; fixture.authResponse = { body: currentUser };
    }
    await browser.waitForRequestHandlersIdle({ pathname: "/auth/me", method: "GET" });
    const requestBody = fixture.controlPlatformCoverBodies[0];
    assert.ok(requestBody !== null && typeof requestBody === "object");
    const coverRequest = requestBody as Record<string, unknown>;
    assert.equal(coverRequest.candidate_variant_id, "variant-cover");
    assert.equal(coverRequest.expected_visual_revision, 2);

    const revokedObserverRelease = deferred();
    await browser.waitForRequestHandlersIdle({ pathname: "/auth/me", method: "GET" });
    const revokedAuthRequests = browser.requests.get("/auth/me") || 0, revokedAuthResponses = browser.responses.get("/auth/me") || 0;
    fixture.authNextResponse = { body: currentUser, waitUntil: revokedObserverRelease.promise };
    fixture.authResponse = { body: limitedUser };
    try {
      await browser.evaluate("window.dispatchEvent(new Event('visibilitychange')); true");
      await waitForAuthCount(browser.requests, revokedAuthRequests + 1, "the revocation-case observer auth request must start");
      await browser.waitFor(`document.querySelector('button[aria-label="Video Coverを表示"]:not([disabled])') !== null`, Boolean, "cached permission must remain usable before the fresh revocation check");
      await scrollSelectorIntoView(browser, 'button[aria-label="Video Coverを表示"]');
      assert.equal(await browser.evaluate(`(() => { const button=document.querySelector('button[aria-label="Video Coverを表示"]'); if(!(button instanceof HTMLButtonElement))return false; const rect=button.getBoundingClientRect(),x=rect.left+rect.width/2,y=rect.top+rect.height/2;return !button.disabled&&x>0&&x<innerWidth&&y>0&&y<innerHeight&&button.contains(document.elementFromPoint(x,y)); })()`), true, "fresh revocation must use a visible native-input target after the applied image changes layout");
      await browser.clickSelector('button[aria-label="Video Coverを表示"]');
      await waitForAuthCount(browser.requests, revokedAuthRequests + 2, "fresh revocation auth must be independent of the held observer");
      await waitForAuthCount(browser.responses, revokedAuthResponses + 1, "fresh revocation auth must complete before rejecting the action");
      await browser.waitFor(`document.querySelector('section[aria-label="配信ビジュアルとVideo Cover"]')?.textContent?.includes('streams.show_cover') === true`, Boolean, "the fresh missing permission must reject the action explicitly");
      await browser.waitForRequestHandlersIdle({ pathname: controlPlatformCoverPath, method: "GET" });
      await waitForAnimationFrames(browser);
      assert.equal(coverMethodCount("PUT"), 1, "fresh permission revocation must send zero additional cover PUTs");
    } finally {
      fixture.authResponse = { body: currentUser }; fixture.authNextResponse = undefined; revokedObserverRelease.resolve();
    }
    await browser.waitForRequestHandlersIdle({ pathname: "/auth/me", method: "GET" });
    await browser.waitFor(`document.querySelector('button[aria-label="Video Coverを非表示"]:not([disabled])') !== null`, Boolean, "known preflight rejection must permit the next separately confirmed intent");

    fixture.controlPlatformCoverWriteResponse = { body: controlPlatformCoverState(false, 3, true, 2, "confirming") };
    await scrollSelectorIntoView(browser, 'button[aria-label="Video Coverを非表示"]');
    await browser.clickSelector('button[aria-label="Video Coverを非表示"]');
    await waitForAnimationFrames(browser);
    assert.equal(fixture.controlPlatformCoverMethods.filter((method) => method === "PUT").length, 1, "hide trigger must not mutate before confirmation");
    const confirmed = await browser.evaluate<boolean>(`(() => {
      const dialog = document.querySelector('[role="alertdialog"]');
      const action = [...(dialog?.querySelectorAll('button') || [])].find((button) => button.textContent?.trim() === 'Coverを非表示');
      if (!(action instanceof HTMLButtonElement)) return false;
      action.click();
      return true;
    })()`);
    assert.equal(confirmed, true, "hide confirmation action missing");
    await waitForCoverMethod("PUT", 2);
    await browser.waitFor(`(() => {
      const image = document.querySelector('img[alt="Encoderで反映確認済みのフタ画像"]');
      const state = [...document.querySelectorAll('dt')].find((element) => element.textContent === 'Encoderの反映確認')?.nextElementSibling;
      return document.body.textContent?.includes('要求した状態の反映は未確認です') === true
        && image instanceof HTMLImageElement && image.complete && image.naturalWidth > 0 && state?.textContent === 'ON r2';
    })()`, Boolean, "ambiguous hide must retain the last confirmed image and revision");
    assert.equal(fixture.controlPlatformCoverMethods.filter((method) => method === "PUT").length, 2);

    fixture.controlPlatformCoverResponse = { body: controlPlatformCoverState(false, 3, false, 3, "applied") };
    const readsBeforeReconciliation = coverMethodCount("GET");
    await browser.waitFor(
      `![...document.querySelectorAll('[role="alertdialog"]')].some((element) => element.getClientRects().length > 0)`,
      Boolean,
      "hide confirmation overlay did not finish closing before reconciliation",
    );
    await scrollSelectorIntoView(browser, 'button[aria-label="Coverの最新状態を確認"]');
    await browser.waitFor(`document.querySelector('button[aria-label="Coverの最新状態を確認"]:not([disabled])') !== null`, Boolean, "cover reconciliation remained disabled after the prior mutation settled");
    await browser.clickSelector('button[aria-label="Coverの最新状態を確認"]');
		await waitForCoverMethod("GET", readsBeforeReconciliation + 1);
		assert.equal(fixture.controlPlatformCoverMethods.filter((method) => method === "PUT").length, 2, "reconciliation must refresh state without resending the mutation");
		assert.equal(coverMethodCount("GET"), readsBeforeReconciliation + 1, "reconciliation must perform one additional read-only state request");
    await browser.waitFor(`(() => {
      const state = [...document.querySelectorAll('dt')].find((element) => element.textContent === 'Encoderの反映確認')?.nextElementSibling;
      return document.querySelector('img[alt="Encoderで反映確認済みのフタ画像"]') === null && state?.textContent === 'OFF r3';
    })()`, Boolean, "confirmed hidden state must retire the applied image at the authoritative revision");

    fixture.controlPlatformCoverWriteResponse = { status: 403, body: { code: "permission_denied" } };
		await browser.waitFor(
			`document.querySelector('button[aria-label="Video Coverを表示"]:not([disabled])') !== null`,
			Boolean,
			"show cover action remained disabled after reconciliation settled",
		);
    await scrollSelectorIntoView(browser, 'button[aria-label="Video Coverを表示"]');
    await browser.clickSelector('button[aria-label="Video Coverを表示"]');
    await waitForCoverMethod("PUT", 3);
    await browser.waitForRequestHandlersIdle({ pathname: controlPlatformCoverPath, method: "PUT" });
    await waitForAnimationFrames(browser);
    assert.equal(fixture.controlPlatformCoverMethods.filter((method) => method === "PUT").length, 3, "backend 403 must receive one request and no automatic resend");
    await browser.waitFor(`document.querySelector('button[aria-label="Video Coverを表示"]:disabled') !== null`, Boolean, "an issued request with no accepted receipt must remain unresolved");
    const readsBeforeUnknownRefresh = coverMethodCount("GET");
    await scrollSelectorIntoView(browser, 'button[aria-label="Coverの最新状態を確認"]');
    await browser.clickSelector('button[aria-label="Coverの最新状態を確認"]');
    await waitForCoverMethod("GET", readsBeforeUnknownRefresh + 1);
    await browser.waitForRequestHandlersIdle({ pathname: controlPlatformCoverPath, method: "GET" });
    await waitForAnimationFrames(browser);
    assert.equal(await browser.evaluate(`document.querySelector('button[aria-label="Video Coverを表示"]')?.hasAttribute('disabled') === true`), true, "generic GET must not release the unknown-write latch");
    await scrollSelectorIntoView(browser, 'button[aria-label="Video Coverを表示"]');
    await browser.clickSelector('button[aria-label="Video Coverを表示"]');
    await waitForAnimationFrames(browser);
    assert.equal(coverMethodCount("PUT"), 3, "unknown refresh and repeated Show must not resend");
  });
}
