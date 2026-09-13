import type { TestContext } from "node:test";
import assert from "node:assert/strict";
import { BrowserHarness } from "./helpers/browser-harness.mts";
import { type BrowserRouteFixture, healthyRows, controlPlatformStream, controlPlatformCoverState, currentUser, controlPlatformCoverPath } from "./ui-browser-fixture.mts";
import { setStoredDisplay, scrollSelectorIntoView, waitForAnimationFrames, deferred } from "./ui-browser-navigation-helpers.mts";



export async function runVisualSettingsScenario(t: TestContext, browser: BrowserHarness, server: { baseUrl: string }, fixture: Pick<BrowserRouteFixture, "healthResponse" | "streamsResponse" | "controlPlatformVisualResponse" | "controlPlatformCoverResponse" | "controlPlatformCoverMethods" | "controlPlatformCoverBodies" | "authResponse" | "controlPlatformCoverWriteResponse">) {

  await t.test("Stream detail presents visual snapshots and cover actions preserve request-count and applied-state boundaries", async () => {
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
      layers: document.body.textContent?.includes('Base / Worker scene → Video Cover → Watermark → Encode → tee') === true,
      warning: document.body.textContent?.includes('保存済みsnapshotを継続します') === true,
      showDisabled: document.querySelector('button[aria-label="Video Coverを表示"]')?.hasAttribute('disabled') === true,
    }))()`);
    assert.deepEqual(visualSnapshot, { title: true, layers: true, warning: true, showDisabled: true });
    await scrollSelectorIntoView(browser, 'button[aria-label="Video Coverを表示"]');
    await browser.clickSelector('button[aria-label="Video Coverを表示"]');
    await waitForAnimationFrames(browser);
    assert.equal(fixture.controlPlatformCoverMethods.filter((method) => method === "PUT").length, 0, "permission denied UI must send zero cover requests");

    fixture.authResponse = { body: currentUser };
		fixture.controlPlatformCoverMethods = [];
		fixture.controlPlatformCoverBodies = [];
    fixture.controlPlatformCoverWriteResponse = { body: controlPlatformCoverState(true, 2, true, 2, "applied") };
		await browser.waitForRequestHandlersIdle({ pathname: "/auth/me", method: "GET" });
		browser.clearRequestCounts("/auth/me");
		await browser.reload();
		await browser.waitForResponseCount("/auth/me", 1, 20_000);
		await browser.waitForRequestHandlersIdle({ pathname: "/auth/me", method: "GET" });
    await browser.waitFor(`document.body.textContent?.includes(${JSON.stringify(controlPlatformStream.name)}) === true`, Boolean, "control-platform stream did not reload");
    await browser.clickSelector('button[aria-label="詳細"]');
    await browser.waitFor(`document.querySelector('button[aria-label="Video Coverを表示"]:not([disabled])') !== null`, Boolean, "show cover action did not become available");
    await browser.waitForRequestHandlersIdle({ pathname: "/auth/me", method: "GET" });
    browser.clearRequestCounts("/auth/me");
    const permissionRelease = deferred();
    fixture.authResponse = { body: currentUser, waitUntil: permissionRelease.promise };
    await browser.evaluate("window.dispatchEvent(new Event('focus')); true");
    await browser.waitForRequestCount("/auth/me", 1, 20_000);
    await browser.waitFor(`document.querySelector('button[aria-label="Video Coverを表示"]:disabled') !== null`, Boolean, "show cover action remained enabled while permission was refreshing");
    browser.clearRequestCounts(controlPlatformCoverPath);
    await browser.clickSelector('button[aria-label="Video Coverを表示"]');
    await waitForAnimationFrames(browser);
    assert.equal(fixture.controlPlatformCoverMethods.filter((method) => method === "PUT").length, 0, "refreshing permission must send zero cover requests");
    permissionRelease.resolve();
    fixture.authResponse = { body: currentUser };
    await browser.waitForResponseCount("/auth/me", 1, 20_000);
    await browser.waitForRequestHandlersIdle({ pathname: "/auth/me", method: "GET" });
    await browser.reload();
    await browser.waitFor(`document.body.textContent?.includes(${JSON.stringify(controlPlatformStream.name)}) === true`, Boolean, "control-platform stream did not reload after permission refresh");
    await browser.clickSelector('button[aria-label="詳細"]');
    await browser.waitFor(`document.querySelector('button[aria-label="Video Coverを表示"]:not([disabled])') !== null`, Boolean, "show cover action did not recover after permission refresh");
    await browser.waitForRequestHandlersIdle({ pathname: controlPlatformCoverPath, method: "GET" });
    await waitForAnimationFrames(browser);
    browser.clearRequestCounts(controlPlatformCoverPath);
		fixture.controlPlatformCoverMethods = [];
		fixture.controlPlatformCoverBodies = [];
    const showRelease = deferred();
    fixture.controlPlatformCoverWriteResponse = { body: controlPlatformCoverState(true, 2, true, 2, "applied"), waitUntil: showRelease.promise };
    await scrollSelectorIntoView(browser, 'button[aria-label="Video Coverを表示"]');
    await browser.clickSelector('button[aria-label="Video Coverを表示"]');
    await browser.waitForRequestCount(controlPlatformCoverPath, 1);
    await browser.clickSelector('button[aria-label="Video Coverを表示"]');
    await waitForAnimationFrames(browser);
    assert.equal(fixture.controlPlatformCoverMethods.filter((method) => method === "PUT").length, 1, "duplicate pending show must send zero additional requests");
    showRelease.resolve();
    await browser.waitFor(`document.querySelector('[data-video-cover="applied"]') !== null`, Boolean, "applied cover did not render after authoritative result");

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
    await browser.waitForRequestCount(controlPlatformCoverPath, 2);
    await browser.waitFor(`document.body.textContent?.includes('DesiredとAppliedは未一致です') === true`, Boolean, "ambiguous hide falsely appeared applied");
    assert.equal(fixture.controlPlatformCoverMethods.filter((method) => method === "PUT").length, 2);

    fixture.controlPlatformCoverResponse = { body: controlPlatformCoverState(false, 3, false, 3, "applied") };
    await browser.waitFor(
      `![...document.querySelectorAll('[role="alertdialog"]')].some((element) => element.getClientRects().length > 0)`,
      Boolean,
      "hide confirmation overlay did not finish closing before reconciliation",
    );
    await scrollSelectorIntoView(browser, 'button[aria-label="Coverの最新状態を確認"]');
    await browser.waitFor(`document.querySelector('button[aria-label="Coverの最新状態を確認"]:not([disabled])') !== null`, Boolean, "cover reconciliation remained disabled after the prior mutation settled");
    await browser.clickSelector('button[aria-label="Coverの最新状態を確認"]');
		await browser.waitForRequestCount(controlPlatformCoverPath, 3);
		assert.equal(fixture.controlPlatformCoverMethods.filter((method) => method === "PUT").length, 2, "reconciliation must refresh state without resending the mutation");
		assert.equal(fixture.controlPlatformCoverMethods.filter((method) => method === "GET").length, 1, "reconciliation must perform one read-only state request");

    fixture.controlPlatformCoverWriteResponse = { status: 403, body: { code: "permission_denied" } };
		await browser.waitFor(
			`document.querySelector('button[aria-label="Video Coverを表示"]:not([disabled])') !== null`,
			Boolean,
			"show cover action remained disabled after reconciliation settled",
		);
    await scrollSelectorIntoView(browser, 'button[aria-label="Video Coverを表示"]');
    await browser.clickSelector('button[aria-label="Video Coverを表示"]');
    await browser.waitForRequestCount(controlPlatformCoverPath, 4);
    await waitForAnimationFrames(browser);
    assert.equal(fixture.controlPlatformCoverMethods.filter((method) => method === "PUT").length, 3, "backend 403 must receive one request and no automatic resend");
  });
}
