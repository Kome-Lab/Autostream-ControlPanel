import assert from "node:assert/strict";
import { exerciseActivation } from "./keyboard-activation.mts";
import type { BrowserHarness } from "../helpers/browser-harness.mts";
import { bundle9SyntheticMFASecret } from "../helpers/bundle9-browser-fixtures.mts";
import type { Condition } from "./matrix.mts";

export type ControlObservation = { tag: string; name: string; labelled: boolean; disabled?: boolean; value?: string; left: number; right: number; width: number; height: number; clipped: boolean };
export type UIObservation = { h1: string[]; lang: string; mode: string; theme: string; overflow: number; hiddenDiagnostic: boolean; secretLeak: boolean; controls: ControlObservation[]; text: string; duplicateIDs: string[]; invalidReferences: string[]; media: { forcedColors: boolean; reducedMotion: boolean; activeMotion: string[]; charts: string[] }; notices: { kind: string | null; freshness: string | null; text: string }[] };
export const observationExpression = `(() => {
  const visible = e => e.getClientRects().length > 0 && getComputedStyle(e).visibility !== 'hidden';
  const roots = [...document.querySelectorAll('main,[role=dialog],[role=alertdialog]')];
  const references = (e, attr) => (e.getAttribute(attr)||'').trim().split(/\\s+/).filter(Boolean).map(id=>({id,element:document.getElementById(id)}));
  const nameFromLabels = e => [...(e.labels||[])].map(label=>label.textContent?.trim()||'').join(' ').trim();
  const named = e => e.getAttribute('aria-label')?.trim() || references(e,'aria-labelledby').map(ref=>ref.element?.textContent?.trim()||'').join(' ').trim() || nameFromLabels(e) || e.textContent?.trim() || e.getAttribute('title') || '';
  const controls = [...new Set(roots.flatMap(r => [...r.querySelectorAll('button,a,input,select,textarea,[role=combobox]')]))].filter(visible);
  const describe = e => { const r=e.getBoundingClientRect(); return { tag:e.tagName, name:named(e), id:e.id, disabled:!!e.disabled||e.getAttribute('aria-disabled')==='true', value:e.tagName==='SELECT'||e.type==='number'||e.tagName==='INPUT'&&[...(e.labels||[])].some(l=>/^(配信枠名|Stream name)\\s*\\*?$/.test(l.textContent.trim()))?e.value:undefined, left:r.left, right:r.right, width:r.width, height:r.height, text:e.textContent?.trim().slice(0,250), labelled:!!(nameFromLabels(e)||e.getAttribute('aria-label')?.trim()||references(e,'aria-labelledby').some(ref=>ref.element?.textContent?.trim())), clipped:r.left< -1 || r.right>innerWidth+1 }; };
  const ids=[...document.querySelectorAll('[id]')].map(e=>e.id).filter(Boolean);
  const invalidReferences=[];
  for(const e of [...new Set(roots.flatMap(root=>[...root.querySelectorAll('[aria-labelledby],[aria-describedby],label[for]')]))]) {
    for(const attr of ['aria-labelledby','aria-describedby']) for(const ref of references(e,attr)) if(!ref.element||!ref.element.textContent?.trim()) invalidReferences.push(attr+':'+ref.id);
    if(e.tagName==='LABEL'&&e.htmlFor&&!document.getElementById(e.htmlFor)) invalidReferences.push('for:'+e.htmlFor);
  }
  const activeMotion=[...new Set(roots.flatMap(root=>[...root.querySelectorAll('*')]))].filter(visible).filter(e=>{const s=getComputedStyle(e);return s.animationName!=='none'&&s.animationDuration.split(',').some(v=>parseFloat(v)>0.01)||s.transitionDuration.split(',').some(v=>parseFloat(v)>0.01);}).map(e=>e.tagName+':'+e.className);
  let stored = '';
  try { stored = Object.values(localStorage).join(' ') + Object.values(sessionStorage).join(' '); } catch { /* Storage may be unavailable. */ }
  const markup = document.documentElement.outerHTML;
  return {
    url:location.pathname+location.search+location.hash, lang:document.documentElement.lang,
    mode:document.documentElement.classList.contains('dark')?'dark':'light',
    theme:document.documentElement.getAttribute('data-theme'),
    h1:[...document.querySelectorAll('main h1')].filter(visible).map(e=>e.textContent?.trim()),
    overflow:Math.max(0,document.documentElement.scrollWidth-document.documentElement.clientWidth),
    controls:controls.map(describe), text:roots.map(e=>e.textContent).join(String.fromCharCode(10)),
    duplicateIDs:ids.filter((id,index)=>ids.indexOf(id)!==index), invalidReferences,
    media:{forcedColors:matchMedia('(forced-colors: active)').matches,reducedMotion:matchMedia('(prefers-reduced-motion: reduce)').matches,activeMotion,charts:[...document.querySelectorAll('[data-chart-motion]')].map(e=>e.dataset.chartMotion)},
    notices:[...document.querySelectorAll('[data-remote-state],[role=status],[role=alert]')].filter(visible).map(e=>({kind:e.getAttribute('data-remote-state'),freshness:e.getAttribute('data-remote-freshness')||e.getAttribute('data-freshness'),text:e.textContent || e.getAttribute('aria-label') || ''})),
    dialogs:document.querySelectorAll('[role=dialog],[role=alertdialog]').length,
    tables:[...document.querySelectorAll('[data-slot=data-table]')].map(e=>({rows:e.querySelectorAll('tbody tr').length,owners:e.querySelectorAll('tbody button[aria-label]').length})),
    focus:describe(document.activeElement), focusInsideDialog:!!document.activeElement?.closest('[role=dialog],[role=alertdialog]'),
    hiddenDiagnostic:markup.includes('UI-HIDDEN-DIAGNOSTIC'),
    secretLeak:[${JSON.stringify(bundle9SyntheticMFASecret)},'B9-SYNTHETIC-RECOVERY'].some(secret => (markup + stored).includes(secret))
  };
})()`;

export function assertObservation(value: UIObservation, condition: Condition) {
  assert.equal(value.h1.length, 1, condition.id + ": exactly one visible page h1");
  assert.equal(value.lang, condition.locale);
  assert.equal(value.mode, condition.mode);
  assert.equal(value.theme, condition.theme);
  assert.equal(value.overflow, 0, condition.id + ": document overflow");
  assert.equal(value.hiddenDiagnostic, false, "raw diagnostic disclosure");
  assert.ok(value.h1[0].trim(), "empty page heading");
  assert.equal(value.secretLeak, false, "secret must not leak into markup or storage");
  assert.deepEqual(value.duplicateIDs, [], "duplicate DOM IDs make label/focus ownership ambiguous");
  assert.deepEqual(value.invalidReferences, [], "accessible references must resolve to real nonempty content");
  const unnamed = value.controls.filter((c: ControlObservation) => c.tag === "BUTTON" && !c.name);
  assert.deepEqual(unnamed, [], "unnamed visible button");
  const unlabelled = value.controls.filter((c: ControlObservation) => ["INPUT", "SELECT", "TEXTAREA"].includes(c.tag) && !c.labelled);
  assert.deepEqual(unlabelled, [], "placeholder is not an input label");
  if (condition.exercise === "forced-colors") {
    assert.equal(value.media.forcedColors, true, "forced colors must be observed via matchMedia");
    assert.ok(value.notices.every(notice => notice.text.trim()), "state cannot rely only on color");
  }
  if (condition.exercise === "reduced-motion") {
    assert.equal(value.media.reducedMotion, true, "reduced motion must be observed via matchMedia");
    assert.ok(value.media.charts.every(mode => mode === "reduced"), "actual chart options must disable motion");
    assert.deepEqual(value.media.activeMotion, [], "reduced motion must suppress actual animation and transition duration");
  }
}
export const focusExpression = `(() => {const e=document.activeElement,r=e.getBoundingClientRect(),s=getComputedStyle(e);const dialogs=[...document.querySelectorAll('[role=dialog],[role=alertdialog]')].filter(d=>d.getClientRects().length);let p=e,id='';while(p&&p!==document.documentElement){id='/'+p.tagName+':'+[...p.parentElement.children].indexOf(p)+id;p=p.parentElement;}return {id,visible:s.visibility!=='hidden'&&s.visibility!=='collapse'&&s.display!=='none'&&s.opacity!=='0'&&e!==document.body&&!e.closest('[inert],[aria-hidden=true]')&&e.getClientRects().length>0&&r.width>0&&r.height>0&&r.left>=-1&&r.right<=innerWidth+1&&r.top>=-1&&r.bottom<=innerHeight+1,inside:!dialogs.length||dialogs.at(-1).contains(e),outline:s.outlineStyle,shadow:s.boxShadow};})()`;
export function assertFocus(value: { id: string; visible: boolean; inside: boolean; outline: string; shadow: string }, previous?: string) {
  assert.ok(value.visible, "keyboard focus is hidden, inert, body or offscreen");
  assert.ok(value.inside, "keyboard focus escaped the active dialog");
  assert.notEqual(value.id, previous, "Tab must move focus to a different element");
  assert.ok(value.outline !== "none" || value.shadow !== "none", "keyboard focus indicator absent");
}
export async function exerciseAccessibility(browser: BrowserHarness, condition: Condition) {
  const pending: string[] = [];
  if (["keyboard", "forced-colors"].includes(condition.exercise || "") || ["Confirmation", "Form", "Detail"].includes(condition.exercise || "")) {
    await browser.pressTab("forward");
    const anchor = await browser.evaluate<Parameters<typeof assertFocus>[0]>(focusExpression);
    assertFocus(anchor);
    const order = [anchor.id];
    let previous = anchor.id;
    for (let step = 0; step < 12; step++) {
      await browser.pressTab("forward");
      const evidence = await browser.evaluate<Parameters<typeof assertFocus>[0]>(focusExpression);
      assertFocus(evidence, previous); previous = evidence.id; order.push(evidence.id);
    }
    for (let step = order.length - 2; step >= 0; step--) {
      await browser.pressTab("backward");
      const evidence = await browser.evaluate<Parameters<typeof assertFocus>[0]>(focusExpression);
      assertFocus(evidence, previous);
      assert.equal(evidence.id, order[step], "reverse Tab must revisit actual forward focus order");
      previous = evidence.id;
    }
    assert.equal(previous, anchor.id, "reverse Tab must restore the observed anchor");
    pending.push(...await exerciseActivation(browser));
  }
  if (condition.exercise === "system-mode") {
    const before = await browser.evaluate<string>("location.href");
    for (const mode of ["dark", "light", condition.mode]) {
      await browser.setMediaFeatures([{ name: "prefers-color-scheme", value: mode }]);
      await browser.waitFor("document.documentElement.classList.contains('dark')", value => value === (mode === "dark"), "system-mode did not follow media");
    }
    assert.equal(await browser.evaluate("location.href"), before);
  }
  return { pending };
}
