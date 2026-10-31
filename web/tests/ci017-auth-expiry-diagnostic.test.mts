import test from "node:test";
import { mkdirSync, writeFileSync } from "node:fs";
import { resolve } from "node:path";
import { BrowserHarness, ensureWebServer } from "./helpers/browser-harness.mts";
import { webRoot, requestedBaseUrl, createBrowserRouteFixture } from "./ui-browser-fixture.mts";
import { runQueryStatesScenario, runLogoutScenario, runSessionExpiryScenario, runAuthMeExpiryScenario } from "./ui-browser-query-auth-scenarios.mts";

test("CI017 auth/me expiry setup bootstrap diagnostic", { timeout: 420_000 }, async (t) => {
  if (process.env.CI !== "true" || process.platform !== "linux") throw new Error("CI017 diagnostic requires the blocking Linux CI runtime");
  if (process.env.AUTOSTREAM_CI017_DIAGNOSTIC !== "1") throw new Error("CI017 diagnostic mode must be explicitly enabled");
  const server = await ensureWebServer(webRoot, requestedBaseUrl);
  const browserPromise = BrowserHarness.launch();
  t.after(async () => { const browser = await browserPromise.catch(() => undefined); await browser?.close(); await server.close(); });
  const browser = await browserPromise;
  await browser.installCI017FetchProbe();
  const fixture = createBrowserRouteFixture(browser);
  await runQueryStatesScenario(t, browser, server, fixture);
  await runLogoutScenario(t, browser, server, fixture);
  await runSessionExpiryScenario(t, browser, server, fixture);
  try { await runAuthMeExpiryScenario(t, browser, server, fixture); }
  catch (error) {
    let page: Record<string, unknown> = { observation_error: true };
    let fetchTrace: unknown[] = [];
    try {
      page = await browser.evaluate<Record<string, unknown>>(`(() => ({
        pathname: location.pathname, reason: new URL(location.href).searchParams.get("reason"),
        login_fallback_present: [...document.querySelectorAll("main p")].some(node => node.textContent?.includes("ログイン情報を読み込んでいます。")),
        username_input_present: !!document.querySelector('input[autocomplete="username"]'),
        password_input_present: !!document.querySelector('input[autocomplete="current-password"]'),
        login_form_present: !!document.querySelector("form"),
        protected_navigation_present: !!document.querySelector('nav[aria-label="Admin navigation"]'),
        account_menu_present: !!document.querySelector('button[aria-label="Account menu"]'),
        document_ready_state: document.readyState
      }))()`);
      fetchTrace = await browser.evaluate<unknown[]>("globalThis.__ci017SetupFetchTrace || []");
    } catch {}
    const diagnostic = { schema_version: 1, code: "CI017_AUTH_EXPIRY_SETUP",
      failure_name: error instanceof Error ? error.name : "unknown",
      failure_message: error instanceof Error ? error.message.slice(0, 240) : "unknown",
      page, fetch_trace: fetchTrace.slice(0, 40), harness: browser.getCI017DiagnosticSnapshot() };
    const output = process.env.AUTOSTREAM_CI017_OUTPUT;
    if (output) { mkdirSync(output, { recursive: true }); writeFileSync(resolve(output, "ci017-auth-expiry-diagnostic.json"), JSON.stringify(diagnostic, null, 2) + "\n", { flag: "wx" }); }
    console.error("CI017_AUTH_EXPIRY_DIAGNOSTIC " + JSON.stringify(diagnostic));
    throw error;
  }
});
