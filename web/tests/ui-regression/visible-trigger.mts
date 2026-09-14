import assert from "node:assert/strict";
import type { BrowserHarness } from "../helpers/browser-harness.mts";

export function visibleTriggerExpression(selector: string, pattern?: RegExp) {
  return `(() => {
    for (const old of document.querySelectorAll('[data-ui-scenario-target]')) old.removeAttribute('data-ui-scenario-target');
    const pattern = ${pattern ? `new RegExp(${JSON.stringify(pattern.source)}, ${JSON.stringify(pattern.flags)})` : "null"};
    const matches = [...document.querySelectorAll(${JSON.stringify(selector)})].filter(e => {
      const r=e.getBoundingClientRect(), s=getComputedStyle(e);
      return e.getClientRects().length>0 && r.width>0 && r.height>0 && s.visibility!=='hidden' && s.display!=='none' && !e.closest('[inert]') && !e.disabled && e.getAttribute('aria-disabled')!=='true'
        && (!pattern || pattern.test((e.getAttribute('aria-label') || e.textContent || '').trim()));
    });
    if(matches.length>1) throw Error('Ambiguous visible trigger: '+matches.length);
    if(matches.length===0) return false;
    const target=matches[0]; target.scrollIntoView({block:'center',inline:'nearest',behavior:'instant'});
    target.setAttribute('data-ui-scenario-target',''); return true;
  })()`;
}
export async function clickVisible(browser: BrowserHarness, selector: string, pattern?: RegExp, remember = false) {
  await browser.waitFor(visibleTriggerExpression(selector, pattern), Boolean, "one visible enabled trigger");
  if (remember) await browser.evaluate("globalThis.__uiReturnTrigger=document.querySelector('[data-ui-scenario-target]');true");
  try { await browser.clickSelector("[data-ui-scenario-target]"); }
  finally { await browser.evaluate("document.querySelector('[data-ui-scenario-target]')?.removeAttribute('data-ui-scenario-target');true"); }
}
export async function clickNamed(browser: BrowserHarness, pattern: RegExp, scope = "main", remember = false) {
  return clickVisible(browser, scope + " button", pattern, remember);
}
export async function selectVisible(browser: BrowserHarness, selector: string, value: string) {
  assert.equal(await browser.evaluate(visibleTriggerExpression(selector)), true, "select target must be visible, unique and enabled");
  try {
    // Exercise the real native select with keyboard input, not a hidden element click.
    const index = await browser.evaluate<number>(`(() => {const e=document.querySelector('[data-ui-scenario-target]');e.focus();return [...e.options].findIndex(o=>o.value===${JSON.stringify(value)} && !o.disabled);})()`);
    assert.ok(index >= 0, "requested select option exists");
    await browser.pressKey("Home");
    for (let i = 0; i < index; i++) await browser.pressKey("ArrowDown");
    await browser.pressNativeKey("Enter");
    await browser.waitFor("document.querySelector('[data-ui-scenario-target]')?.value", current => current === value, "native select changed");
  } finally { await browser.evaluate("document.querySelector('[data-ui-scenario-target]')?.removeAttribute('data-ui-scenario-target');true"); }
}
