import assert from "node:assert/strict";
import type { BrowserHarness } from "./helpers/browser-harness.mts";

export type BootstrapWitness = {
  input: string | null;
  status: "waiting" | "loaded" | "error";
  script?: string;
  theme?: string;
  mode?: string;
  dark?: boolean;
  mirror?: string | null;
};

// Seed only storage in the exact next document. Capture the external script's
// load event before its target onload handlers can release Next's
// beforeInteractive queue and begin hydration. Later Provider/DB writes cannot
// replace this snapshot. No DOM theme state or production function is patched.
export function bootstrapInputObserver(url: string, mirror: string) {
  return `(() => {
    if (location.href !== ${JSON.stringify(url)}) return;
    localStorage.setItem('autostream.ui_preference', ${JSON.stringify(mirror)});
    const record = { input: localStorage.getItem('autostream.ui_preference'), status: 'waiting' };
    globalThis.__accountBootstrapWitness = record;
    const observe = event => {
      const script = event.target;
      if (!(script instanceof HTMLScriptElement) || script.src !== new URL('/theme-bootstrap.js', location.href).href) return;
      document.removeEventListener('load', observe, true);
      document.removeEventListener('error', observe, true);
      Object.assign(record, { status: event.type === 'load' ? 'loaded' : 'error', script: script.src,
        theme: document.documentElement.dataset.theme, mode: document.documentElement.dataset.colorMode,
        dark: document.documentElement.classList.contains('dark'), mirror: localStorage.getItem('autostream.ui_preference') });
    };
    document.addEventListener('load', observe, true);
    document.addEventListener('error', observe, true);
  })()`;
}

export function assertBootstrapWitness(witness: BootstrapWitness | undefined, url: string, mirror: string, theme: string, mode: string) {
  assert.ok(witness, "new-document bootstrap input was not installed");
  assert.equal(witness.input, mirror, "bootstrap input was overwritten before the new document");
  assert.equal(witness.status, "loaded", "actual external bootstrap did not execute successfully");
  assert.equal(witness.script, new URL("/theme-bootstrap.js", url).href);
  assert.equal(witness.theme, theme, "external bootstrap theme before hydration");
  assert.equal(witness.mode, mode, "external bootstrap mode before hydration");
  assert.equal(witness.mirror, mirror, "bootstrap must not overwrite its input mirror");
  if (mode !== "system") assert.equal(witness.dark, mode === "dark");
}

export async function installBootstrapInput(browser: BrowserHarness, url: string, mirror: string) {
  return browser.installNewDocumentScript(bootstrapInputObserver(url, mirror));
}

export async function readBootstrapWitness(browser: BrowserHarness) {
  return browser.waitFor<BootstrapWitness>("globalThis.__accountBootstrapWitness", value => value?.status === "loaded" || value?.status === "error", "external bootstrap load/error was not observed");
}

export async function withBootstrapInput<T>(browser: BrowserHarness, url: string, mirror: string, callback: () => Promise<T>): Promise<T> {
  const remove = await installBootstrapInput(browser, url, mirror);
  let failed = false, primary: unknown;
  try { return await callback(); }
  catch (error) { failed = true; primary = error; throw error; }
  finally {
    try { await remove(); }
    catch (error) {
      if (failed) throw new AggregateError([primary, error], "Bootstrap observation and cleanup failed", { cause: primary });
      throw error;
    }
  }
}
