import type { BrowserHarness } from "../helpers/browser-harness.mts";

// Reuse the real lifecycle owner. Never change interception error tolerances.
export async function navigateDocument(browser: BrowserHarness, url: string, reset: () => void) {
  browser.setFetchDiagnosticContext?.({ phase: "paint-before-leave" });
  await browser.waitForRequestHandlersIdle();
  await paint(browser);
  await browser.waitForRequestHandlersIdle();
  browser.assertNoFatalError();
  browser.setFetchDiagnosticContext?.({ phase: "to-blank" });
  await browser.navigate("about:blank");
  browser.setFetchDiagnosticContext?.({ phase: "old-handlers-drain" });
  await browser.waitForRequestHandlersIdle();
  browser.setFetchDiagnosticContext?.({ phase: "phase-reset" });
  reset();
  browser.clearRequestCounts();
  browser.clearNavigationCount();
  browser.clearConsoleErrors();
  browser.setFetchDiagnosticContext?.({ phase: "to-product" });
  await browser.navigate(url);
}
export async function paint(browser: BrowserHarness) {
  await browser.evaluate("(async () => { await document.fonts.ready; await Promise.all([...document.images].map(i => i.decode().catch(() => { if (i.currentSrc) throw new Error('Product image did not decode'); }))); await new Promise(r => requestAnimationFrame(() => requestAnimationFrame(r))); return true; })()");
  browser.assertNoFatalError();
}
