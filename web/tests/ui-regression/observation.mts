import assert from "node:assert/strict";
import { longIdentifier, longText } from "./fixture-inputs.mts";
import { exerciseActivation, type ActivationTarget } from "./keyboard-activation.mts";
import type { BrowserHarness } from "../helpers/browser-harness.mts";
import { bundle9SyntheticMFASecret } from "../helpers/bundle9-browser-fixtures.mts";
import type { Condition } from "./matrix.mts";
import { readFileSync } from "node:fs";
import { renderedDOM } from "./render-state.mts";
import { assertStatusOnly, type RequestEvidence } from "./state-drivers.mts";

export type ControlObservation = { tag: string; role?: string | null; name: string; labelled: boolean; disabled?: boolean; value?: string; left: number; right: number; width: number; height: number; clipped: boolean };
export type StatusPageObservation = { url: string; mainCount: number; painted: boolean; rect: number[]; controlCandidates: number; overlays: number; headings: { text: string; rect: number[] }[]; messages: { role: string; text: string; rect: number[] }[] };
export type UIObservation = { h1: string[]; lang: string; mode: string; theme: string; overflow: number; hiddenDiagnostic: boolean; secretLeak: boolean; controls: ControlObservation[]; text: string; duplicateIDs: string[]; invalidReferences: string[]; media: { forcedColors: boolean; reducedMotion: boolean; activeMotion: string[]; charts: string[] }; notices: { kind: string | null; freshness: string | null; text: string }[]; statusPage?: StatusPageObservation };
export const observationExpression = `(() => {
  ${renderedDOM}
  const visible = uiPainted;
  const roots = [...document.querySelectorAll('main,[role=dialog],[role=alertdialog]')];
  const references = (e, attr) => (e.getAttribute(attr)||'').trim().split(/\\s+/).filter(Boolean).map(id=>({id,element:document.getElementById(id)}));
  const nameFromLabels = e => [...(e.labels||[])].map(label=>label.textContent?.trim()||'').join(' ').trim();
  const named = e => e.getAttribute('aria-label')?.trim() || references(e,'aria-labelledby').map(ref=>ref.element?.textContent?.trim()||'').join(' ').trim() || nameFromLabels(e) || e.textContent?.trim() || e.getAttribute('title') || '';
  const controls = uiRoot()?uiControls(uiRoot()):[];
  const describe = e => { const r=e.getBoundingClientRect(); return { tag:e.tagName, role:e.getAttribute("role"), name:named(e), id:e.id, disabled:!!e.disabled||e.getAttribute('aria-disabled')==='true', value:(e===globalThis.__uiContentDraft&&${JSON.stringify([longIdentifier,longText])}.includes(e.value))||e.tagName==='SELECT'||e.type==='number'||e.tagName==='INPUT'&&[...(e.labels||[])].some(l=>/^(配信枠名|Stream name)\\s*\\*?$/.test(l.textContent.trim()))?e.value:undefined, left:r.left, right:r.right, width:r.width, height:r.height, text:e.textContent?.trim().slice(0,250), labelled:!!(nameFromLabels(e)||e.getAttribute('aria-label')?.trim()||references(e,'aria-labelledby').some(ref=>ref.element?.textContent?.trim())), clipped:r.left< -1 || r.right>innerWidth+1 }; };
  const ids=[...document.querySelectorAll('[id]')].map(e=>e.id).filter(Boolean);
  const invalidReferences=[];
  for(const e of document.querySelectorAll('input,select')) if(uiProxyShape(e)&&!uiProxyPeer(e))invalidReferences.push('orphan-native-proxy:'+uiIdentity(e));
  for(const e of controls)if(!uiAX(e))invalidReferences.push('hidden-accessible-control:'+uiIdentity(e));
  for(const e of [...new Set(roots.flatMap(root=>[...root.querySelectorAll('[aria-labelledby],[aria-describedby],label[for]')]))]) {
    for(const attr of ['aria-labelledby','aria-describedby']) for(const ref of references(e,attr)) if(!ref.element||!ref.element.textContent?.trim()) invalidReferences.push(attr+':'+ref.id);
    if(e.tagName==='LABEL'&&e.htmlFor&&!document.getElementById(e.htmlFor)) invalidReferences.push('for:'+e.htmlFor);
  }
  const activeMotion=[...new Set(roots.flatMap(root=>[...root.querySelectorAll('*')]))].filter(visible).filter(e=>{const s=getComputedStyle(e);return s.animationName!=='none'&&s.animationDuration.split(',').some(v=>parseFloat(v)>0.01)||s.transitionDuration.split(',').some(v=>parseFloat(v)>0.01);}).map(e=>e.tagName+':'+e.className);
  let stored = '';
  try { stored = Object.values(localStorage).join(' ') + Object.values(sessionStorage).join(' '); } catch { /* Storage may be unavailable. */ }
  const markup = document.documentElement.outerHTML;
  const main=document.querySelector('main'),rect=e=>{const r=e?.getBoundingClientRect();return r?[r.left,r.top,r.width,r.height]:[];};
  const statusPage={url:location.pathname+location.search+location.hash,mainCount:document.querySelectorAll('main').length,painted:uiAX(main),rect:rect(main),
    controlCandidates:main?[...main.querySelectorAll(uiControlSelector)].filter(e=>!uiProxy(e)).length:0,overlays:uiOpeningDialogs().length+uiDialogs().length,
    headings:main?[...main.querySelectorAll('h1')].filter(uiAX).map(e=>({text:e.textContent.trim(),rect:rect(e)})):[],
    messages:main?[...main.querySelectorAll('[role=status],[role=alert]')].filter(uiAX).map(e=>({role:e.getAttribute('role'),text:(e.textContent?.trim()||e.getAttribute('aria-label')||'').trim(),rect:rect(e)})):[]};
  return {
    url:location.pathname+location.search+location.hash, lang:document.documentElement.lang,
    mode:document.documentElement.classList.contains('dark')?'dark':'light',
    theme:document.documentElement.getAttribute('data-theme'),
    h1:[...document.querySelectorAll('main h1')].filter(visible).map(e=>e.textContent?.trim()),
    overflow:Math.max(0,document.documentElement.scrollWidth-document.documentElement.clientWidth),
    controls:controls.map(describe), text:roots.map(e=>e.textContent).join(String.fromCharCode(10)), statusPage,
    duplicateIDs:ids.filter((id,index)=>ids.indexOf(id)!==index), invalidReferences,
    media:{forcedColors:matchMedia('(forced-colors: active)').matches,reducedMotion:matchMedia('(prefers-reduced-motion: reduce)').matches,activeMotion,charts:[...document.querySelectorAll('[data-chart-motion]')].map(e=>e.dataset.chartMotion)},
    notices:[...document.querySelectorAll('[data-remote-state],[role=status],[role=alert]')].filter(visible).map(e=>({kind:e.getAttribute('data-remote-state'),freshness:e.getAttribute('data-remote-freshness')||e.getAttribute('data-freshness'),text:e.textContent || e.getAttribute('aria-label') || ''})),
    dialogs:document.querySelectorAll('[role=dialog],[role=alertdialog]').length,
    tables:[...document.querySelectorAll('[data-slot=data-table]')].map(e=>({rows:e.querySelectorAll('tbody tr').length,owners:e.querySelectorAll('tbody button[aria-label]').length})),
    focus:{identity:uiIdentity(document.activeElement),tag:document.activeElement?.tagName||'NONE'}, focusInsideDialog:!!document.activeElement?.closest('[role=dialog],[role=alertdialog]'),
    hiddenDiagnostic:markup.includes('UI-HIDDEN-DIAGNOSTIC'),
    secretLeak:[${JSON.stringify(bundle9SyntheticMFASecret)},'B9-SYNTHETIC-RECOVERY'].some(secret => (markup + stored).includes(secret))
  };
})()`;

export function assertObservation(value: UIObservation, condition: Condition, evidence?: RequestEvidence, primary?: string) {
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
  if (value.controls.length === 0) {
    assert.ok(evidence && primary, "zero active controls requires actual status-only evidence");
    assertStatusOnly(condition, value, evidence, primary);
  }
  const unnamed = value.controls.filter((c: ControlObservation) => (["BUTTON", "A", "SUMMARY"].includes(c.tag) || ["combobox", "checkbox", "switch", "radio", "tab"].includes(c.role || "")) && !c.name);
  assert.deepEqual(unnamed, [], "unnamed visible control");
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
export const focusExpression = `(() => {${renderedDOM}
  const e=document.activeElement,r=e.getBoundingClientRect(),s=getComputedStyle(e),dialog=uiDialogs().at(-1);
  const transparent=c=>!c||c==='transparent'||/rgba\\([^)]*,\\s*0\\)/.test(c);
  const outline=s.outlineStyle!=='none'&&parseFloat(s.outlineWidth)>=1&&!transparent(s.outlineColor);
  const ring=s.getPropertyValue('--tw-ring-shadow').trim();
  const indicator=outline||!matchMedia('(forced-colors: active)').matches&&e.matches(':focus-visible')&&ring!==''&&ring!=='none'&&ring!=='0 0 #0000'&&/[1-9][0-9]*(?:\\.[0-9]+)?px/.test(ring);
  const plan=globalThis.__uiKeyboardPlan;
  return {id:uiIdentity(e),visible:uiAX(e)&&e!==document.body&&r.left>=-1&&r.right<=innerWidth+1&&r.top>=-1&&r.bottom<=innerHeight+1,
    inside:!dialog||dialog.contains(e),indicator,boundary:e===document.body||e===document.documentElement,native:e.tagName==='VIDEO'||e.tagName==='AUDIO',
    rect:[r.left,r.top,r.width,r.height],required:plan?plan.targets.indexOf(e):-1,modal:!!dialog};
})()`;
export type FocusObservation = { id: string; visible: boolean; inside: boolean; indicator: boolean; boundary: boolean; native: boolean; rect: number[]; required: number; modal: boolean };
export function assertFocus(value: FocusObservation, previous?: string) {
  assert.ok(value.visible, "keyboard focus is hidden, inert, body or offscreen");
  assert.ok(value.inside, "keyboard focus escaped the active dialog");
  assert.notEqual(value.id, previous, "Tab must move focus to a different element");
  assert.equal(value.indicator, true, "keyboard focus indicator absent");
}
type KeyboardTarget = { selector: string; name?: string };
type KeyboardContract = { required: KeyboardTarget[]; activation: "section" | "disclosure" | "none"; activationTarget?: ActivationTarget; native?: string };
const keyboardInventory: { surfaces: { id: string; keyboard: KeyboardContract }[] } = JSON.parse(readFileSync(new URL('../fixtures/ui-regression/surfaces.json', import.meta.url), 'utf8'));
export function keyboardContract(condition: Condition): KeyboardContract {
  if (condition.exercise === "Confirmation") return { required: [{ selector: '[role=alertdialog] button', name: '^(Cancel|キャンセル)$' }], activation: "none" };
  const contract = keyboardInventory.surfaces.find(surface => surface.id === condition.family)?.keyboard;
  assert.ok(contract?.required.length, "predeclared family keyboard path required"); return contract;
}
export function prepareKeyboardExpression(contract: KeyboardContract) {
  return `(() => {${renderedDOM}
    const contract=${JSON.stringify(contract)},root=uiRoot();if(!root)throw Error('keyboard root missing');
    const name=e=>e.getAttribute('aria-label')||e.textContent?.trim()||'';
    const targets=contract.required.map(spec=>{const candidates=[...document.querySelectorAll(spec.selector)].filter(e=>uiAX(e)&&!e.disabled&&(!spec.name||new RegExp(spec.name).test(name(e))));if(candidates.length!==1)throw Error('required keyboard target missing, disabled or ambiguous');return candidates[0];});
    const modal=uiDialogs().at(-1)||null;
    const order=modal?uiControls(modal).filter(e=>!e.disabled&&e.tabIndex>=0):[];
    if(modal&&(!order.length||targets.some(e=>!modal.contains(e))))throw Error('modal keyboard inventory missing');
    globalThis.__uiKeyboardPlan={targets,order,modal};return {required:targets.length,modal:!!modal,order:order.map(uiIdentity),native:!!(contract.native&&document.querySelector(contract.native))};
  })()`;
}
type KeyTrace = { direction: "forward" | "backward"; stage: "seek" | "path" | "reverse" | "boundary"; focus: FocusObservation };
export function safeKeyboardTrace(condition: Condition, trace: KeyTrace[]) {
  assert.match(condition.id, /^[A-Za-z0-9-]+$/);
  const entries = trace.map(({direction,stage,focus}) => ({ direction, stage,
    id: /^(?:BODY|HTML|(?:\/[A-Z]+:[0-9]+){1,16})$/.test(focus.id) ? focus.id.slice(0,160) : "OTHER",
    visible:!!focus.visible, inside:!!focus.inside, rect:focus.rect.slice(0,4).map(v=>Number.isFinite(v)?Math.round(v):null) }));
  const value = { condition: condition.id, totalSteps: trace.length, entries };
  while(Buffer.byteLength(JSON.stringify(value))>4096&&entries.length)entries.shift();
  assert.ok(Buffer.byteLength(JSON.stringify(value))<=4096);return value;
}
export async function exerciseAccessibility(browser: BrowserHarness, condition: Condition, saveTrace: (value: unknown) => void = () => {}) {
  const pending: string[] = [];
  if (["keyboard", "forced-colors"].includes(condition.exercise || "") || ["Confirmation", "Form", "Detail"].includes(condition.exercise || "")) {
    const trace: KeyTrace[] = [], contract = keyboardContract(condition);
    let previous: string | undefined; let inputFailed = false;
    const press = async (direction: "forward"|"backward", stage: KeyTrace["stage"]) => {
      await browser.pressTab(direction); const focus=await browser.evaluate<FocusObservation>(focusExpression);trace.push({direction,stage,focus});return focus;
    };
    try {
    const plan = await browser.evaluate<{required:number;modal:boolean;order:string[];native:boolean}>(prepareKeyboardExpression(contract));
      const order: string[] = [], seenRequired = new Set<number>(); let required=0, cycle=false;
      // A fixed maximum bounds broken navigation; it is not an expected count.
      for(let step=0;step<128;step++) {
        const focus=await press("forward",required?"path":"seek");
        if(focus.native){pending.push("UA_KEYBOARD_IDENTITY_PENDING: native media internals cannot be observed");break;}
        if(focus.boundary&&!plan.modal){assert.equal(required,plan.required,"document ended before required keyboard path");break;}
        assertFocus(focus,previous);previous=focus.id;
        if(plan.modal){
          assert.ok(plan.order.includes(focus.id),"modal focus left its pre-observed control inventory");
          if(order.length){assert.equal(focus.id,plan.order[(plan.order.indexOf(order[0])+order.length)%plan.order.length],"modal Tab skipped or reordered a control");if(focus.id===order[0]){cycle=true;break;}}
          if(focus.required>=0)seenRequired.add(focus.required);required=seenRequired.size;
        } else if(focus.required>=0&&focus.required===required)required++;
        else if(focus.required>=required)assert.fail("required keyboard trigger was skipped");
        if(required||plan.modal)order.push(focus.id);
        if(!plan.modal&&required===plan.required)break;
      }
      if(!pending.length) {
        assert.equal(required,plan.required,"required keyboard path not reached within bound");
        if(plan.modal){assert.equal(cycle,true,"modal first/last focus wrap missing");assert.deepEqual(new Set(order),new Set(plan.order),"modal skipped a tabbable control");}
        assert.ok(order.length,"zero keyboard path observations");
        const reverse=plan.modal?[...order].reverse():order.slice(0,-1).reverse();
        for(const id of reverse){const focus=await press("backward","reverse");assertFocus(focus,previous);assert.equal(focus.id,id,"reverse Tab must revisit actual forward focus order");previous=focus.id;}
        if(plan.modal){const focus=await press("backward","boundary");assertFocus(focus,previous);assert.equal(focus.id,order.at(-1),"Shift+Tab must wrap to modal last control");}
        // A one-control page still proves reverse movement through its preceding
        // document control/boundary, without asserting a nonexistent 13th item.
        if(!plan.modal&&order.length===1){const focus=await press("backward","boundary");if(!focus.boundary)assertFocus(focus,previous);const restored=await press("forward","boundary");assertFocus(restored);assert.equal(restored.id,order[0],"reverse boundary must restore the actual page target");}
        pending.push(...await exerciseActivation(browser,contract.activation,contract.activationTarget));
      }
      if(plan.native&&!pending.some(value=>value.startsWith("UA_")))pending.push("UA_KEYBOARD_IDENTITY_PENDING: native media keyboard behavior remains required");
    } catch (error) { inputFailed = true; throw error; } finally {
      const cleanup: unknown[] = [];
      try { saveTrace(safeKeyboardTrace(condition,trace)); } catch (error) { cleanup.push(error); }
      try { await browser.evaluate("delete globalThis.__uiKeyboardPlan;true"); } catch (error) { cleanup.push(error); }
      if (!inputFailed && cleanup.length) throw new AggregateError(cleanup, "keyboard evidence/cleanup failure");
    }
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
