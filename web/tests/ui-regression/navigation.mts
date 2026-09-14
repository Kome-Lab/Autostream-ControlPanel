import type { BrowserHarness } from "../helpers/browser-harness.mts";

// Initial product navigation belongs to one configured condition. Same-condition
// history/recovery continues through this harness; no inter-condition blank reset.
export async function navigateDocument(browser: BrowserHarness, url: string, initialize: () => void = () => {}) {
  browser.assertNoFatalError();
  browser.setFetchDiagnosticContext?.({ phase: "phase-reset" });
  initialize();
  browser.assertNoFatalError();
  browser.setFetchDiagnosticContext?.({ phase: "to-product" });
  await browser.navigate(url);
}
export async function paint(browser: BrowserHarness) {
  await browser.evaluate("(async () => { await document.fonts.ready; await Promise.all([...document.images].map(i => i.decode().catch(() => { if (i.currentSrc) throw new Error('Product image did not decode'); }))); await new Promise(r => requestAnimationFrame(() => requestAnimationFrame(r))); return true; })()");
  browser.assertNoFatalError();
}
