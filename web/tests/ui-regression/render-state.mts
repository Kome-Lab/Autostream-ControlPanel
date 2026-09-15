import assert from "node:assert/strict";
import type { BrowserHarness } from "../helpers/browser-harness.mts";

// Shared emitted DOM classifiers. Paint, accessibility and native form proxies
// are distinct; secret/duplicate-ID checks deliberately keep the full document.
export const renderedDOM = String.raw`
  const uiPainted = e => {
    if (!e || e.hidden || !e.getClientRects().length) return false;
    for (let p=e;p;p=p.parentElement) {
      const s=getComputedStyle(p);
      if(s.display==='none'||s.visibility==='hidden'||s.visibility==='collapse'||s.opacity==='0')return false;
      if(p.tagName==='DETAILS'&&!p.open) {
        const summary=[...p.children].find(c=>c.tagName==='SUMMARY');
        if(!summary||!summary.contains(e))return false;
      }
    }
    const r=e.getBoundingClientRect();return r.width>0&&r.height>0;
  };
  const uiAX = e => uiPainted(e)&&!e.closest('[aria-hidden=true],[inert]');
  const uiDialogs = () => [...document.querySelectorAll('[role=dialog],[role=alertdialog]')].filter(uiAX);
  const uiOpeningDialogs = () => [...document.querySelectorAll('[role=dialog],[role=alertdialog]')].filter(e =>
    e.isConnected&&!e.hidden&&!e.closest('[aria-hidden=true],[inert]')&&
    (e.getAttribute('data-state')==='open'||e.getAttribute('data-state')!=='closed'&&uiAX(e)));
  const uiRoot = () => {const dialogs=uiDialogs();return dialogs.at(-1)||document.querySelector('main');};
  const uiProxyShape = e => {
    if(!['INPUT','SELECT'].includes(e.tagName)||e.getAttribute('aria-hidden')!=='true'||e.tabIndex!==-1)return false;
    const s=getComputedStyle(e),r=e.getBoundingClientRect();
    const w=parseFloat(s.width),h=parseFloat(s.height),zeroClip=/^rect\(0px,?\s*0px,?\s*0px,?\s*0px\)$/.test(s.clip);
    const cssPixelClip=w>0&&w<=1&&h>0&&h<=1&&zeroClip&&/(hidden|clip)/.test(s.overflow||s.overflowX)&&[r.width/w,r.height/h].every(v=>Number.isFinite(v)&&v>0);
    return s.position==='absolute'&&(cssPixelClip||s.opacity==='0'&&s.pointerEvents==='none');
  };
  const uiProxyPeer = e => {
    const selector=e.tagName==='SELECT'?'[role=combobox]':e.type==='checkbox'?'[role=checkbox],[role=switch]':e.type==='radio'?'[role=radio]':null;
    if(!selector)return null;
    const labelled=p=>!!(p.getAttribute('aria-label')?.trim()||[...(p.labels||[])].some(l=>l.textContent?.trim())||p.closest('label')?.textContent?.trim()||
      (p.getAttribute('aria-labelledby')||'').split(/\s+/).filter(Boolean).some(id=>document.getElementById(id)?.textContent?.trim()));
    const peers=[...(e.parentElement?.querySelectorAll(selector)||[])].filter(uiAX);
    if(peers.length===1&&!labelled(peers[0]))return null;
    return peers.length===1?peers[0]:null;
  };
  const uiProxy = e => uiProxyShape(e)&&!!uiProxyPeer(e);
  const uiControlSelector = 'button,a[href],input:not([type=hidden]),select,textarea,summary,[role=combobox],[role=checkbox],[role=switch],[role=radio],[role=tab]';
  const uiControls = root => [...root.querySelectorAll(uiControlSelector)].filter(e=>uiPainted(e)&&!e.closest('[inert]')&&!uiProxy(e));
  const uiIdentity = e => {
    if(!e||e===document.body)return 'BODY';
    if(e===document.documentElement)return 'HTML';
    let p=e,id='';for(let depth=0;p&&p!==document.documentElement&&depth<16;depth++,p=p.parentElement)id='/'+p.tagName+':'+[...(p.parentElement?.children||[])].indexOf(p)+id;
    return id;
  };
`;

export const beginRenderState = (requested?: "open" | "closed" | "page") => `(() => {${renderedDOM}
  const requested=${JSON.stringify(requested || "auto")},current=uiOpeningDialogs();
  const expected=requested==='auto'?(current.length?'open':'page'):requested;
  const owner=expected==='closed'?globalThis.__uiSettledOverlay:current.at(-1)||null;
  if(expected==='open'&&!owner)throw Error('expected overlay not reached');
  if(expected==='page'&&(current.length||uiDialogs().length))throw Error('unexpected overlay on page');
  if(expected==='closed'&&!owner)throw Error('close has no previously observed overlay owner');
  globalThis.__uiRenderState={owner,expected,last:null,stable:0,animations:new Set(),finished:new Set(),cancelled:new Set()};return true;
})()`;
export const renderStateExpression = `(() => {${renderedDOM}
  const state=globalThis.__uiRenderState;if(!state)throw Error('render settlement was not initialized');
  const {owner,expected}=state,active=uiDialogs().at(-1)||null,opening=uiOpeningDialogs().at(-1)||null;
  if(expected==='open'&&(!owner.isConnected||opening!==owner||active&&active!==owner||owner.getAttribute('data-state')==='closed'))throw Error('overlay owner replaced or cancelled');
  if(expected==='page'&&(active||opening))throw Error('unexpected overlay during page settlement');
  if(expected==='closed'&&(active&&active!==owner||opening&&opening!==owner))throw Error('different overlay replaced closing owner');
  for(const animation of owner?.getAnimations({subtree:false})||[]) {
    const timing=animation.effect?.getComputedTiming();
    if(!timing||!Number.isFinite(timing.endTime)||timing.endTime<0)throw Error('non-finite overlay animation');
    if(timing&&Number.isFinite(timing.endTime)&&timing.endTime>0&&!state.animations.has(animation)) {
      state.animations.add(animation);
      animation.finished.then(()=>{if(typeof animation.currentTime==='number'&&animation.currentTime>=timing.endTime)state.finished.add(animation);else state.cancelled.add(animation);},()=>state.cancelled.add(animation));
    }
  }
  let pending=expected==='open'&&!uiAX(owner);
  for(const animation of state.animations) {
    if(state.cancelled.has(animation)||animation.playState==='idle'&&!state.finished.has(animation))throw Error('finite overlay animation cancelled');
    if(!state.finished.has(animation))pending=true;
  }
  const closed=!owner||!owner.isConnected||owner.getAttribute('data-state')==='closed'&&!uiPainted(owner);
  if(expected==='closed'&&!closed)pending=true;
  const r=owner&&!closed?owner.getBoundingClientRect():document.documentElement.getBoundingClientRect();
  const rect=[r.left,r.top,r.width,r.height];
  if(rect.some(v=>!Number.isFinite(v))||r.width<=0||r.height<=0)throw Error('invalid settlement geometry');
  if(!pending&&state.last&&rect.every((v,i)=>Math.abs(v-state.last[i])<0.25))state.stable++;else state.stable=0;
  state.last=rect;
  if(state.stable>=2){globalThis.__uiSettledOverlay=expected==='open'?owner:null;return {settled:true,expected,rect};}
  return {settled:false,expected,rect};
})()`;
type RenderReporter = (value: unknown) => void;
const failureGeometry = `(() => {const s=globalThis.__uiRenderState,owner=s?.owner,r=owner?.getBoundingClientRect();
  return {observed:!!s,connected:!!owner?.isConnected,stable:typeof s?.stable==='number'?s.stable:0,rect:r?[r.left,r.top,r.width,r.height].map(v=>Number.isFinite(v)?Math.round(v):null):null};})()`;
async function reportFailure(browser: BrowserHarness, expected: string, report: RenderReporter) {
  let geometry: unknown = { observed: false };
  try { geometry = await browser.evaluate(failureGeometry); } catch { /* A fatal observer must not replace the original failure. */ }
  try { report({ stage: "finite-overlay-geometry", expected, geometry }); } catch { /* Original failure remains authoritative. */ }
}
export async function settleRender(browser: BrowserHarness, expected?: "open" | "closed" | "page", report: RenderReporter = () => {}) {
  try {
  await browser.evaluate(beginRenderState(expected));
  await browser.waitFor<{ settled: boolean }>(renderStateExpression, value => value.settled === true, "finite overlay animation and geometry settlement");
  browser.assertNoFatalError();
  } catch (error) { await reportFailure(browser, expected || "active-scope", report); throw error; }
}
export const assertSameRenderState = `(() => {${renderedDOM}
  const state=globalThis.__uiRenderState;if(!state||state.stable<2)throw Error('capture requires settled geometry');
  const active=uiDialogs().at(-1)||null,opening=uiOpeningDialogs().at(-1)||null;
  if(state.expected==='open'&&(active!==state.owner||opening!==state.owner||!state.owner.isConnected))throw Error('capture overlay owner changed');
  if(state.expected!=='open'&&(active||opening))throw Error('capture has an unexpected overlay owner');
  const r=active?active.getBoundingClientRect():document.documentElement.getBoundingClientRect();
  if([r.left,r.top,r.width,r.height].some((v,i)=>Math.abs(v-state.last[i])>=0.25))throw Error('capture geometry changed after layout restoration');
  return true;
})()`;
export async function closeOverlay(browser: BrowserHarness, report: RenderReporter = () => {}) {
  try {
  // Retain the actual settled owner before Escape starts its exit animation.
  await browser.evaluate(beginRenderState("closed"));
  await browser.evaluate(renderStateExpression);
  await browser.pressKey("Escape");
  await browser.waitFor<{ settled: boolean }>(renderStateExpression, value => value.settled === true, "closing owner animation and geometry settlement");
  browser.assertNoFatalError();
  } catch (error) { await reportFailure(browser, "closed", report); throw error; }
}
export async function assertStableCapture(browser: BrowserHarness) {
  assert.equal(await browser.evaluate(assertSameRenderState), true);
  browser.assertNoFatalError();
}
