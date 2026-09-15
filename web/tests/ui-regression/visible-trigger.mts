import assert from "node:assert/strict";
import type { BrowserHarness } from "../helpers/browser-harness.mts";
import { renderedDOM } from "./render-state.mts";

export function visibleTriggerExpression(selector: string, pattern?: RegExp, disabled = false) {
  return `(() => {
    ${renderedDOM}
    for (const old of document.querySelectorAll('[data-ui-scenario-target]')) {if(old!==globalThis.__uiScenarioTarget)throw Error('scenario target marker belongs to another owner');old.removeAttribute('data-ui-scenario-target');}
    delete globalThis.__uiScenarioTarget;
    const pattern = ${pattern ? `new RegExp(${JSON.stringify(pattern.source)}, ${JSON.stringify(pattern.flags)})` : "null"};
    const matches = [...document.querySelectorAll(${JSON.stringify(selector)})].filter(e => {
      const r=e.getBoundingClientRect(), s=getComputedStyle(e);
      return uiAX(e) && !uiProxy(e) && r.width>0 && r.height>0 && s.visibility!=='hidden' && s.display!=='none'
        && (!pattern || pattern.test((e.getAttribute('aria-label') || e.textContent || '').trim()));
    });
    if(matches.length>1) throw Error('Ambiguous visible trigger: '+matches.length);
    if(matches.length===0) return false;
    const target=matches[0];if(!!(target.disabled||target.getAttribute('aria-disabled')==='true')!==${disabled})return false;
    target.scrollIntoView({block:'center',inline:'nearest',behavior:'instant'});
    target.setAttribute('data-ui-scenario-target',''); globalThis.__uiScenarioTarget=target; return true;
  })()`;
}
export const visibleTriggerPoint = (disabled = false) => `(() => {${renderedDOM}
  const e=globalThis.__uiScenarioTarget,marked=[...document.querySelectorAll('[data-ui-scenario-target]')];
  if(!e?.isConnected||marked.length!==1||marked[0]!==e)throw Error('marked trigger disconnected or replaced before click');
  if(!uiAX(e)||uiProxy(e)||!!(e.disabled||e.getAttribute('aria-disabled')==='true')!==${disabled})throw Error('marked trigger visibility or availability changed');
  const r=e.getBoundingClientRect(),x=r.left+r.width/2,y=r.top+r.height/2;
  if(![r.left,r.top,r.width,r.height,x,y].every(Number.isFinite)||r.width<=0||r.height<=0||x<0||y<0||x>=innerWidth||y>=innerHeight)throw Error('marked trigger center is outside the viewport');
  const hit=document.elementFromPoint(x,y);
  const blockedSurface=${disabled}&&hit?.contains(e)&&!hit.matches(uiControlSelector);
  if(!hit||!(e===hit||e.contains(hit)||blockedSurface))throw Error('marked trigger center is obstructed or clipped');
  return {x,y};
})()`;
async function clickTarget(browser: BrowserHarness, selector: string, pattern: RegExp | undefined, remember: boolean, disabled: boolean) {
  await browser.waitFor(visibleTriggerExpression(selector, pattern, disabled), Boolean, disabled ? "one visible disabled negative target" : "one visible enabled trigger");
  try {
    const point = await browser.evaluate<{ x: number; y: number }>(visibleTriggerPoint(disabled));
    assert.ok(point && Number.isFinite(point.x) && Number.isFinite(point.y), "native input needs the measured target point");
    if (remember) await browser.evaluate("globalThis.__uiReturnTrigger=globalThis.__uiScenarioTarget;true");
    await browser.clickAt(point.x, point.y);
  }
  finally { await browser.evaluate("globalThis.__uiScenarioTarget?.removeAttribute('data-ui-scenario-target');delete globalThis.__uiScenarioTarget;true"); }
}
export async function clickVisible(browser: BrowserHarness, selector: string, pattern?: RegExp, remember = false) {
  return clickTarget(browser, selector, pattern, remember, false);
}
// Keep the old permission/pending negative pointer attempt without enabling the
// disabled control or accepting it as a positive actionable target.
export async function clickDisabledVisible(browser: BrowserHarness, selector: string) {
  return clickTarget(browser, selector, undefined, false, true);
}
export async function clickStreamPrimary(browser: BrowserHarness, stream: { id: string; name: string }) {
  const selector = 'main [data-slot="stream-primary-trigger"][data-stream-id=' + JSON.stringify(stream.id) + ']';
  const exactName = new RegExp('^' + stream.name.replace(/[.*+?^${}()|[\]\\]/g, '\\$&') + '$');
  await clickVisible(browser, selector, exactName, true);
  assert.equal(await browser.evaluate("globalThis.__uiReturnTrigger?.isConnected===true"), true, "opening detail must preserve its actual trigger DOM identity");
}
export async function clickNamed(browser: BrowserHarness, pattern: RegExp, scope = "main", remember = false) {
  return clickVisible(browser, scope + " button", pattern, remember);
}
export async function clickWorkerRestart(browser: BrowserHarness, worker: { id: string; type: string; name: string }) {
  assert.equal(worker.id, "worker-one"); assert.equal(worker.type, "worker");
  await browser.waitFor(`(() => {${renderedDOM}
    if(document.querySelector('[data-ui-worker-row]'))throw Error('worker marker already owned');
    const rows=[...document.querySelectorAll('main [data-screen-family=workers] tbody tr')].filter(row=>
      row.querySelector('td[headers$="-service_name"] .font-medium')?.textContent.trim()===${JSON.stringify(worker.name)}&&
      row.querySelector('td[headers$="-service_type"] > div')?.textContent.trim()==='Worker');
    if(rows.length>1)throw Error('ambiguous eligible worker display identity');if(rows.length!==1||!uiAX(rows[0]))return false;
    rows[0].setAttribute('data-ui-worker-row','');globalThis.__uiWorkerRow=rows[0];return true;
  })()`, Boolean, "exact eligible Worker display row");
  try {
    await clickVisible(browser, '[data-ui-worker-row] button', /^(Worker を再起動|Restart worker)$/, true);
    await browser.waitFor(`(() => {const values=[...document.querySelectorAll('[role=alertdialog] [data-confirmation-section=target] li')].map(e=>e.textContent.trim());return values.length===2&&values.includes(${JSON.stringify(worker.id)})&&values.includes(${JSON.stringify(worker.name)});})()`, Boolean, "confirmation must name the same actual Worker ID and label");
  } finally { await browser.evaluate("globalThis.__uiWorkerRow?.removeAttribute('data-ui-worker-row');delete globalThis.__uiWorkerRow;true"); }
}
export async function selectVisible(browser: BrowserHarness, selector: string, value: string) {
  assert.equal(await browser.evaluate(visibleTriggerExpression(selector)), true, "select target must be visible, unique and enabled");
  try {
    await browser.evaluate(visibleTriggerPoint());
    // Exercise the real native select with keyboard input, not a hidden element click.
    const index = await browser.evaluate<number>(`(() => {const e=document.querySelector('[data-ui-scenario-target]');e.focus();return [...e.options].findIndex(o=>o.value===${JSON.stringify(value)} && !o.disabled);})()`);
    assert.ok(index >= 0, "requested select option exists");
    await browser.pressKey("Home");
    for (let i = 0; i < index; i++) await browser.pressKey("ArrowDown");
    await browser.pressNativeKey("Enter");
    await browser.waitFor("document.querySelector('[data-ui-scenario-target]')?.value", current => current === value, "native select changed");
  } finally { await browser.evaluate("document.querySelector('[data-ui-scenario-target]')?.removeAttribute('data-ui-scenario-target');true"); }
}
