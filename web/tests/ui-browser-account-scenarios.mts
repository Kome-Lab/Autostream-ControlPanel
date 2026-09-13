import type { TestContext } from "node:test";
import assert from "node:assert/strict";
import { BrowserHarness } from "./helpers/browser-harness.mts";
import { type BrowserRouteFixture, currentUser } from "./ui-browser-fixture.mts";
import { setStoredDisplay } from "./ui-browser-navigation-helpers.mts";



export async function runAccountAppearanceScenario(t: TestContext, browser: BrowserHarness, server: { baseUrl: string }, fixture: Pick<BrowserRouteFixture, "uiPreferenceMethods" | "authResponse" | "uiPreferenceBodies" | "uiPreferenceResponse" | "uiPreferenceWriteResponse">) {

  await t.test("Account appearance persists 12 themes and 3 modes with DB fallback and save rollback", async () => {
		const preferenceRequestCount = (method: "GET" | "PUT") => fixture.uiPreferenceMethods.filter((value) => value === method).length;
		const waitForPreferenceSettlement = async (method: "GET" | "PUT", minimumRequests: number) => {
			assert.ok(minimumRequests > 0, "settlement needs an observed request phase");
			await browser.waitFor("true", () => preferenceRequestCount(method) >= minimumRequests, "UI preference request did not arrive");
			await browser.waitForRequestHandlersIdle({ pathname: "/account/preferences/ui", method });
		};
		for (const method of ["GET", "PUT"] as const) {
			const previousRequests = preferenceRequestCount(method);
			if (previousRequests > 0) await waitForPreferenceSettlement(method, previousRequests);
		}
		fixture.authResponse = { status: 401, body: { code: "unauthorized" } };
		fixture.uiPreferenceMethods = [];
		fixture.uiPreferenceBodies = [];
		await browser.navigate(`${server.baseUrl}/login/`);
		await browser.waitFor(`location.pathname === '/login/'`, Boolean, "login route was not retained");
		assert.equal(fixture.uiPreferenceMethods.filter((method) => method === "GET").length, 0, "public login must not query authenticated UI preferences");

		fixture.authResponse = { body: currentUser };
		fixture.uiPreferenceResponse = { body: { theme_id: "autostream", color_mode: "system", revision: 0 } };
		fixture.uiPreferenceWriteResponse = { body: { theme_id: "autostream", color_mode: "dark", revision: 1 } };
		await browser.evaluate(`localStorage.removeItem('autostream.ui_preference'); localStorage.setItem('autostream.theme', 'dark'); true`);
		await browser.navigate(`${server.baseUrl}/admin/account/`);
		await browser.waitFor(
			`document.documentElement.dataset.theme + '/' + document.documentElement.dataset.colorMode`,
			(value: string) => value === "autostream/system",
			"DB preference must win over the retained legacy storage value",
		);
		await browser.waitFor(
			`(localStorage.getItem('autostream.ui_preference') || '').includes('"color_mode":"system"')`,
			Boolean,
			"DB-backed bootstrap mirror did not reflect the current preference",
		);
		assert.equal(fixture.uiPreferenceMethods.filter((method) => method === "PUT").length, 0, "retired storage must not cause a migration write");
		assert.equal(await browser.evaluate(`localStorage.getItem('autostream.theme')`), "dark", "retained migration data must not be deleted");
		await waitForPreferenceSettlement("GET", 1);

		fixture.uiPreferenceResponse = { body: { theme_id: "ocean", color_mode: "dark", revision: 4 }, delayMs: 1_200 };
    fixture.uiPreferenceWriteResponse = { body: { theme_id: "violet", color_mode: "light", revision: 5 } };
    fixture.uiPreferenceMethods = [];
		fixture.uiPreferenceBodies = [];
    await browser.setViewport(1440, 1000);
    await setStoredDisplay(browser, "ja", "light");
		await browser.evaluate(`localStorage.setItem('autostream.ui_preference', JSON.stringify({ theme_id: 'cyan', color_mode: 'light' })); true`);
		await browser.navigate(`${server.baseUrl}/admin/account/`);
		assert.equal(
			await browser.evaluate<string>(`document.documentElement.dataset.theme + '/' + document.documentElement.dataset.colorMode`),
			"cyan/light",
			"external pre-hydration bootstrap did not apply the validated local mirror before the DB response",
		);
		await browser.waitFor(
      `document.documentElement.dataset.theme + '/' + document.documentElement.dataset.colorMode`,
      (value: string) => value === "ocean/dark",
			"DB appearance did not override the pre-hydration mirror",
		);
		await waitForPreferenceSettlement("GET", 1);
		fixture.uiPreferenceResponse = { body: { theme_id: "ocean", color_mode: "dark", revision: 4 } };
    await browser.clickRoleWithText("tab", "外観");
    const matrix = await browser.waitFor(
      `(() => ({ themes: document.querySelectorAll('[role="radiogroup"][aria-label="配色テーマ"] [role="radio"]').length, modes: document.querySelectorAll('[role="radiogroup"][aria-label="表示モード"] [role="radio"]').length }))()`,
      (value: { themes: number; modes: number }) => value.themes === 12 && value.modes === 3,
      "appearance matrix was not rendered",
    );
    assert.deepEqual(matrix, { themes: 12, modes: 3 });
    await browser.clickSelector('[aria-label="Violetテーマ"]');
    await browser.clickSelector('[aria-label="ライトモード"]');
    await browser.waitFor(
      `document.documentElement.dataset.theme + '/' + document.documentElement.dataset.colorMode + '/' + document.documentElement.classList.contains('dark')`,
      (value: string) => value === "violet/light/false",
      "appearance preview was not immediate",
    );
		assert.match(await browser.evaluate<string>(`localStorage.getItem('autostream.ui_preference') || ''`), /"theme_id":"ocean"/, "unsaved appearance preview replaced the DB bootstrap mirror");
    await browser.clickSelector('[aria-label="表示設定を保存"]');
    await browser.waitFor(
      `document.body.textContent?.includes('表示設定を保存しました。') === true`,
      Boolean,
      "appearance save did not complete",
    );
    assert.equal(fixture.uiPreferenceMethods.filter((method) => method === "PUT").length, 1, "appearance save must send exactly one PUT");
		assert.match(await browser.evaluate<string>(`localStorage.getItem('autostream.ui_preference') || ''`), /"theme_id":"violet"/, "saved DB appearance did not refresh the bootstrap mirror");
		await waitForPreferenceSettlement("PUT", 1);

    fixture.uiPreferenceWriteResponse = { status: 409, body: { code: "revision_conflict" } };
    await browser.clickSelector('[aria-label="Oceanテーマ"]');
    await browser.clickSelector('[aria-label="ダークモード"]');
    await browser.clickSelector('[aria-label="表示設定を保存"]');
    await browser.waitFor(
      `document.documentElement.dataset.theme + '/' + document.documentElement.dataset.colorMode`,
      (value: string) => value === "violet/light",
      "failed save did not roll back the displayed preference",
    );
    assert.equal(fixture.uiPreferenceMethods.filter((method) => method === "PUT").length, 2, "failed save must not retry automatically");
		await waitForPreferenceSettlement("PUT", 2);
		await waitForPreferenceSettlement("GET", preferenceRequestCount("GET"));

    fixture.uiPreferenceResponse = { body: { theme_id: "violet", color_mode: "light", revision: 5 } };
		const savedGet = preferenceRequestCount("GET") + 1;
    await browser.reload();
    await browser.waitFor(
      `document.documentElement.dataset.theme + '/' + document.documentElement.dataset.colorMode`,
      (value: string) => value === "violet/light",
      "saved DB appearance did not persist across reload",
    );
		await waitForPreferenceSettlement("GET", savedGet);
    const putsBeforeFallback = fixture.uiPreferenceMethods.filter((method) => method === "PUT").length;
    fixture.uiPreferenceResponse = { body: { theme_id: "future-theme", color_mode: "infrared", revision: 6, fallback: true } };
		const fallbackGet = preferenceRequestCount("GET") + 1;
    await browser.reload();
    await browser.waitFor(
      `document.documentElement.dataset.theme + '/' + document.documentElement.dataset.colorMode`,
      (value: string) => value === "autostream/system",
      "unknown stored appearance did not render the safe fallback",
    );
    assert.equal(fixture.uiPreferenceMethods.filter((method) => method === "PUT").length, putsBeforeFallback, "safe fallback must not overwrite DB automatically");
		await waitForPreferenceSettlement("GET", fallbackGet);

		fixture.uiPreferenceResponse = { body: { theme_id: "violet", color_mode: "light", revision: 7 } };
		await setStoredDisplay(browser, "en", "light");
		const translatedGet = preferenceRequestCount("GET") + 1;
		await browser.reload();
		await browser.clickRoleWithText("tab", "外観");
		await browser.waitFor(`document.querySelector('[aria-label="Violet theme"]') !== null`, Boolean, "translated theme accessible name missing");
		await browser.waitFor(
			`document.documentElement.dataset.theme + '/' + document.documentElement.dataset.colorMode`,
			(value: string) => value === "violet/light",
			"translated account did not consume its DB preference",
		);
		await waitForPreferenceSettlement("GET", translatedGet);
		await browser.evaluate(`document.querySelector('[aria-label="Ocean theme"]')?.focus(); true`);
		await browser.pressKey("ArrowRight");
		await browser.waitFor(
			`document.activeElement?.getAttribute('aria-label') + '/' + document.querySelector('[aria-label="Cyan theme"]')?.getAttribute('aria-checked')`,
			(value: string) => value === "Cyan theme/true",
			"theme radiogroup did not implement roving Arrow-key selection",
		);
		await browser.evaluate(`document.querySelector('[aria-label="System mode"]')?.focus(); true`);
		await browser.pressKey("End");
		await browser.waitFor(
			`document.activeElement?.getAttribute('aria-label') + '/' + document.querySelector('[aria-label="Dark mode"]')?.getAttribute('aria-checked')`,
			(value: string) => value === "Dark mode/true",
			"mode radiogroup did not implement roving Home/End selection",
		);
		await waitForPreferenceSettlement("GET", translatedGet);
		await waitForPreferenceSettlement("PUT", 2);
  });
}
