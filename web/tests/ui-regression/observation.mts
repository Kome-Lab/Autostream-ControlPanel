import assert from "node:assert/strict";
import { longIdentifier, longText } from "./fixture-inputs.mts";
import { exerciseActivation, exerciseSurfaceActivation, surfaceActivation, type ActivationTarget } from "./keyboard-activation.mts";
import type { BrowserHarness } from "../helpers/browser-harness.mts";
import { bundle9SyntheticMFASecret } from "../helpers/bundle9-browser-fixtures.mts";
import type { Condition } from "./matrix.mts";
import { readFileSync } from "node:fs";
import { renderedDOM } from "./render-state.mts";
import { assertStatusOnly, type RequestEvidence } from "./state-drivers.mts";
import { assertMediaIdentity, assertMediaFixture, exerciseUnavailableMedia, mediaExpectation } from "./media-keyboard-contract.mts";
import type { NativeFocusObservation } from "../helpers/browser-ua-focus.mts";

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
  const datetime=e.tagName==='INPUT'&&e.type==='datetime-local',host=plan?.datetimes.findIndex(entry=>entry.element===e)??-1;
  const mediaUnchanged=!plan||plan.medias.every(entry=>entry.element.isConnected&&uiAX(entry.element)&&entry.element.paused===entry.paused&&entry.element.currentTime===entry.time&&entry.element.volume===entry.volume&&entry.element.muted===entry.muted&&uiRoot()===plan.root);
  const datetimeUnchanged=!plan||plan.datetimes.every(entry=>entry.element.isConnected&&entry.element.type==='datetime-local'&&uiAX(entry.element)&&entry.element.value===entry.value&&uiRoot()===plan.root);
  const tallPanelVisible=()=>{
    if(e.getAttribute('role')!=='tabpanel'||e.getAttribute('data-slot')!=='tabs-content'||e.getAttribute('data-state')!=='active'||e.tabIndex<0||!uiAX(e)||!indicator)return false;
    const owner=e.closest('[data-slot=tabs]'),label=e.getAttribute('aria-labelledby');
    const unique=id=>id&&[...document.querySelectorAll('[id]')].filter(n=>n.id===id).length===1;
    if(!owner||!unique(e.id)||!unique(label))return false;
    const tabs=[...owner.querySelectorAll('[role=tab]')].filter(t=>t.closest('[data-slot=tabs]')===owner&&t.getAttribute('aria-selected')==='true');
    if(tabs.length!==1)return false;const tab=tabs[0];
    if(tab.id!==label||tab.getAttribute('aria-controls')!==e.id||!uiAX(tab)||tab.disabled||tab.getAttribute('aria-disabled')==='true')return false;
    const edge=Math.max(1,parseFloat(s.outlineWidth)||0)+Math.max(0,parseFloat(s.outlineOffset)||0);
    let left=0,right=innerWidth,top=0,bottom=innerHeight;
    for(let p=e.parentElement;p;p=p.parentElement){const ps=getComputedStyle(p),pr=p.getBoundingClientRect();
      if(/hidden|clip|scroll|auto/.test(ps.overflowX)){left=Math.max(left,pr.left);right=Math.min(right,pr.right);}
      if(/hidden|clip|scroll|auto/.test(ps.overflowY)){top=Math.max(top,pr.top);bottom=Math.min(bottom,pr.bottom);}
    }
    const shell=document.querySelector('main')?.parentElement;
    for(const header of [...(shell?.children||[])].filter(n=>n.tagName==='HEADER'&&uiAX(n))){
      const hs=getComputedStyle(header),hr=header.getBoundingClientRect();
      if(['sticky','fixed'].includes(hs.position)&&hr.top<=top&&hr.bottom>top&&hr.left<r.right&&hr.right>r.left)top=hr.bottom;
    }
    if(r.height+2*edge<=bottom-top)return false;
    if(![r.left,r.right,r.top,r.bottom,r.width,r.height].every(Number.isFinite)||r.left-edge<left||r.right+edge>right||r.top-edge<top||Math.min(r.bottom,bottom)-r.top<48)return false;
    const y=Math.min(r.bottom,bottom)-edge-1;
    return [[r.left+1,r.top+1],[r.right-1,r.top+1],[(r.left+r.right)/2,r.top+1],[r.left+1,y],[r.right-1,y]].every(([x,y])=>{const hit=document.elementFromPoint(x,y);return hit&&(hit===e||e.contains(hit));});
  };
  return {id:uiIdentity(e),visible:uiAX(e)&&e!==document.body&&(r.left>=-1&&r.right<=innerWidth+1&&r.top>=-1&&r.bottom<=innerHeight+1||tallPanelVisible()),
    inside:!dialog||dialog.contains(e),indicator,boundary:e===document.body||e===document.documentElement,native:e.tagName==='VIDEO'||e.tagName==='AUDIO',
    datetime,datetimeHost:host,datetimeUnchanged,mediaUnchanged,rect:[r.left,r.top,r.width,r.height],required:plan?plan.targets.indexOf(e):-1,modal:!!dialog};
})()`;
export type FocusObservation = { id: string; visible: boolean; inside: boolean; indicator: boolean; boundary: boolean; native: boolean; rect: number[]; required: number; modal: boolean; datetime?: boolean; datetimeHost?: number; datetimeUnchanged?: boolean; mediaUnchanged?: boolean; mediaNegativeProven?:boolean; ua?: NativeFocusReading };
export function assertFocus(value: FocusObservation, previous?: string) {
  assert.ok(value.visible, "keyboard focus is hidden, inert, body or offscreen");
  assert.ok(value.inside, "keyboard focus escaped the active dialog");
  assert.notEqual(value.id, previous, "Tab must move focus to a different element");
  assert.equal(value.indicator, true, "keyboard focus indicator absent");
}
export type NativeFocusReading = {document:number;host:number;node:number;kind:"datetime"|"media";relation:"host"|"ua-descendant";role:string;stable:true;focusedAncestors:number;indicator:boolean;visible:boolean;uaFocusable?:number;mediaState?:string;disabled?:boolean} & Partial<Pick<NativeFocusObservation,"complete"|"focusable"|"focusables"|"media">>;
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
    const medias=[...root.querySelectorAll('video[controls],audio[controls]')].filter(uiAX).map(element=>({element,paused:element.paused,time:element.currentTime,volume:element.volume,muted:element.muted}));
    const ordinaryOrder=modal?uiControls(modal).filter(e=>!e.disabled&&e.tabIndex>=0):[];
    const order=modal&&medias.length?[...ordinaryOrder,...medias.map(v=>v.element)].sort((a,b)=>a.compareDocumentPosition(b)&2?1:-1):ordinaryOrder;
    if(modal&&(!order.length||targets.some(e=>!modal.contains(e))))throw Error('modal keyboard inventory missing');
    const datetimes=uiControls(root).filter(e=>e.tagName==='INPUT'&&e.type==='datetime-local'&&!e.disabled&&e.tabIndex>=0).map(element=>({element,value:element.value}));
    globalThis.__uiKeyboardPlan={targets,order,modal,root,datetimes,medias};return {required:targets.length,modal:!!modal,order:order.map(uiIdentity),native:!!(contract.native&&document.querySelector(contract.native))};
  })()`;
}
type KeyTrace = { direction: "forward" | "backward"; stage: "seek" | "path" | "reverse" | "boundary" | "restore"; focus: FocusObservation };
export function safeKeyboardTrace(condition: Condition, trace: KeyTrace[]) {
  assert.match(condition.id, /^[A-Za-z0-9-]+$/);
  const bounded=(value:unknown,max:number)=>typeof value==="number"&&Number.isSafeInteger(value)&&value>=0&&value<=max?value:null;
  const entries = trace.map(({direction,stage,focus}) => ({ direction, stage,
    id: /^(?:BODY|HTML|(?:\/[A-Z]+:[0-9]+){1,16})$/.test(focus.id) ? focus.id.slice(0,160) : "OTHER",
    visible:!!focus.visible, inside:!!focus.inside, type:focus.datetime?"datetime-local":"other", valueUnchanged:focus.datetimeUnchanged===true, ua:focus.ua?{document:bounded(focus.ua.document,128),host:bounded(focus.ua.host,128),node:bounded(focus.ua.node,128),kind:["datetime","media"].includes(focus.ua.kind)?focus.ua.kind:"other",relation:["host","ua-descendant"].includes(focus.ua.relation)?focus.ua.relation:"other",stable:focus.ua.stable===true,uaFocusable:bounded(focus.ua.uaFocusable,8192),mediaState:["not-media","empty","loading","loaded","error"].includes(focus.ua.mediaState||"")?focus.ua.mediaState:"other"}:undefined, rect:focus.rect.slice(0,4).map(v=>Number.isFinite(v)?Math.round(v):null) }));
  // Keep the complete bounded UA order even when the detailed tail is trimmed.
  const uaPaths: {document:number;host:number;kind:string;forward:number[];backward:number[];restored:number[];descendant:boolean;visible:boolean;stable:boolean;indicator:boolean;unchanged:boolean;overflow:boolean;media?:{state:string;complete:boolean;disabled:boolean;negativeKeys:boolean;focusables:{node:number|null;relation:string;disabled:boolean}[]}}[]=[];
  let uaPathOverflow=false;
  for(const {direction,stage,focus}of trace){const ua=focus.ua;if(!ua||![ua.document,ua.host,ua.node].every(n=>bounded(n,128)!==null&&n>0)||!["datetime","media"].includes(ua.kind))continue;
    let path=uaPaths.find(p=>p.document===ua.document&&p.host===ua.host);
    if(!path){if(uaPaths.length===8){uaPathOverflow=true;continue;}path={document:ua.document,host:ua.host,kind:ua.kind,forward:[],backward:[],restored:[],descendant:false,visible:true,stable:true,indicator:true,unchanged:true,overflow:false};uaPaths.push(path);}
    const nodes=stage==="restore"?path.restored:path[direction];if(nodes.length<16)nodes.push(ua.node);else path.overflow=true;
    path.descendant ||=ua.relation==="ua-descendant";path.visible&&=ua.visible&&focus.visible;path.stable&&=ua.stable===true;path.indicator&&=focus.indicator;path.unchanged&&=ua.kind==="datetime"?focus.datetimeUnchanged===true:focus.mediaUnchanged===true;
    if(ua.kind==="media")path.media={state:["empty","loaded","error","loading"].includes(ua.mediaState||"")?ua.mediaState!:"other",complete:ua.complete===true,disabled:ua.disabled===true,negativeKeys:!!(path.media?.negativeKeys||focus.mediaNegativeProven),focusables:(ua.focusables||[]).slice(0,32).map(n=>({node:bounded(n.node,128),relation:["host","ua-descendant"].includes(n.relation)?n.relation:"other",disabled:n.disabled===true}))};
  }
  const value = { condition: condition.id, totalSteps: trace.length, uaPaths, uaPathOverflow, entries };
  const outputBytes=()=>Buffer.byteLength(JSON.stringify(value,null,2)+"\n");
  while(outputBytes()>4096&&entries.length)entries.shift();
  assert.ok(outputBytes()<=4096,"actual serialized keyboard trace exceeds output bound");return value;
}
export async function exerciseKeyboardPath(browser: BrowserHarness, condition: Condition, saveTrace: (value: unknown) => void = () => {}) {
  const pending: string[] = [];
  if (["keyboard", "forced-colors"].includes(condition.exercise || "") || ["Confirmation", "Form", "Detail"].includes(condition.exercise || "")) {
    const trace: KeyTrace[] = [], contract = keyboardContract(condition);
    let previous: string | undefined; let inputFailed = false, mediaPending = false, mediaRestorationTabs=0;
    let last: FocusObservation | undefined;
    const datetimeHosts=new Set<number>(),exits=new Set<string>(),hostSteps=new Map<string,number>();
    const observer=(browser as unknown as {observeNativeFocus?:()=>Promise<NativeFocusReading>}).observeNativeFocus;
    const uaPaths=new Map<string,{host:number;document:number;kind:string;descendant:boolean;forward:number[];backward:number[];negative?:boolean;mediaSnapshot?:string;mediaNodes?:number[]}>();
    const mediaExits=new Set<string>();let observedDocument:number|undefined;
    const observeUA=async(focus:FocusObservation,direction:"forward"|"backward",restoring=false)=>{
      if(!observer||!focus.datetime&&!focus.native)return;
      const ua=await observer.call(browser);focus.ua=ua;
      if(focus.datetime)assert.notEqual(ua.disabled,true,"datetime operation target disabled");
      assert.equal(ua.stable,true,"unstable UA focus evidence");
      assert.equal(ua.visible,true,"UA focused leaf is hidden or clipped");
      assert.equal(ua.kind,focus.datetime?"datetime":"media","UA host type mismatch");
      assert.ok([ua.document,ua.host,ua.node].every(value=>Number.isSafeInteger(value)&&value>0&&value<=128),"bounded UA identities required");
      if(observedDocument!==undefined)assert.equal(ua.document,observedDocument,"UA document changed");observedDocument=ua.document;
      const path:NonNullable<ReturnType<typeof uaPaths.get>>=uaPaths.get(focus.id)||{host:ua.host,document:ua.document,kind:ua.kind,descendant:false,forward:[],backward:[]};
      assert.equal(ua.host,path.host,"UA host replaced");assert.equal(ua.document,path.document,"UA host document replaced");
      assert.ok(["host","ua-descendant"].includes(ua.relation),"unbound UA leaf");
      path.descendant ||=ua.relation==="ua-descendant";
      if(restoring)assert.equal(ua.node,path.forward[0],"restore the original UA entry segment");
      else{assert.notEqual(path[direction].at(-1),ua.node,"native Tab must leave the current UA segment");path[direction].push(ua.node);}uaPaths.set(focus.id,path);
      if(focus.native){
        assert.equal(focus.mediaUnchanged,true,"media was replaced, played or edited");assert.ok(focus.visible&&ua.visible,"native focus target hidden, clipped or outside viewport");focus.indicator=focus.indicator||ua.indicator;
        const status=assertMediaIdentity(ua as NativeFocusObservation,mediaExpectation(condition));await assertMediaFixture(browser,condition);
        const snapshot=JSON.stringify({media:ua.media,focusables:ua.focusables});
        if(path.mediaSnapshot!==undefined)assert.equal(snapshot,path.mediaSnapshot,"media state or complete set changed during traversal");else path.mediaSnapshot=snapshot;
        path.mediaNodes=ua.focusables!.filter(n=>status==="unavailable"||!n.disabled).map(n=>n.node);
        if(status==="unavailable"&&path.negative===undefined){assert.ok(trace.length+mediaRestorationTabs+2<=128,"media negative restoration retains existing keyboard step budget");mediaRestorationTabs+=await exerciseUnavailableMedia(browser,condition,ua as NativeFocusObservation);path.negative=true;focus.mediaNegativeProven=true;}
      }
    };
    const press = async (direction: "forward"|"backward", stage: KeyTrace["stage"]) => {
      for (;;) {
        assert.ok(trace.length+mediaRestorationTabs<128,"keyboard traversal exceeded the existing 128 step bound");
        if(last?.native&&observer)assert.ok((hostSteps.get(last.id+":"+direction)||0)<16,"media host did not exit within 16 native Tab steps");
        if(last?.datetime)assert.ok((hostSteps.get(last.datetimeHost+":"+direction)||0)<16,"datetime host did not exit within 16 native Tab steps");
        await browser.pressTab(direction); const focus=await browser.evaluate<FocusObservation>(focusExpression);trace.push({direction,stage,focus});
        await observeUA(focus,direction,stage==="restore");
        assert.notEqual(focus.datetimeUnchanged,false,"datetime host was replaced, hidden, moved or edited");
        if(focus.datetime){assertFocus(focus);assert.ok(Number.isInteger(focus.datetimeHost)&&focus.datetimeHost!>=0,"unobserved datetime host");datetimeHosts.add(focus.datetimeHost!);}
        if(last?.datetime){
          const key=last.datetimeHost+":"+direction,count=(hostSteps.get(key)||0)+1;hostSteps.set(key,count);
          assert.ok(count<=16,"datetime host did not exit within 16 native Tab steps");
          if(focus.id===last.id){assert.equal(focus.datetime,true,"datetime host type changed");assert.equal(focus.datetimeHost,last.datetimeHost,"datetime host identity changed");last=focus;continue;}
          exits.add(key);
        }
        if(last?.native&&observer){
          const key=last.id+":"+direction,count=(hostSteps.get(key)||0)+1;hostSteps.set(key,count);assert.ok(count<=16,"media host did not exit within 16 native Tab steps");
          if(focus.id===last.id){assert.equal(focus.native,true,"native media host type changed");assertFocus(focus);last=focus;continue;}mediaExits.add(key);
        }
        last=focus;return focus;
      }
    };
    try {
    const plan = await browser.evaluate<{required:number;modal:boolean;order:string[];native:boolean}>(prepareKeyboardExpression(contract));
      const order: string[] = [], seenRequired = new Set<number>(); let required=0, cycle=false;
      // A fixed maximum bounds broken navigation; it is not an expected count.
      for(let step=0;step<128;step++) {
        const focus=await press("forward",required?"path":"seek");
        if(focus.native&&!observer){mediaPending=true;pending.push("UA_KEYBOARD_IDENTITY_PENDING: native media internals cannot be observed");break;}
        if(focus.boundary&&!plan.modal){assert.equal(required,plan.required,"document ended before required keyboard path");break;}
        assertFocus(focus,previous);previous=focus.id;
        if(plan.modal){
          assert.ok(plan.order.includes(focus.id),"modal focus left its pre-observed control inventory");
          if(order.length){assert.equal(focus.id,plan.order[(plan.order.indexOf(order[0])+order.length)%plan.order.length],"modal Tab skipped or reordered a control");if(focus.id===order[0]){cycle=true;break;}}
          if(focus.required>=0)seenRequired.add(focus.required);required=seenRequired.size;
        } else if(focus.required>=0&&focus.required===required)required++;
        else if(focus.required>=required)assert.fail("required keyboard trigger was skipped");
        if(required||plan.modal||order.length||focus.datetime||focus.native)order.push(focus.id);
        if(!plan.modal&&required===plan.required&&!focus.datetime&&!focus.native)break;
      }
      if(!mediaPending) {
        assert.equal(required,plan.required,"required keyboard path not reached within bound");
        if(plan.modal){assert.equal(cycle,true,"modal first/last focus wrap missing");assert.deepEqual(new Set(order),new Set(plan.order),"modal skipped a tabbable control");}
        assert.ok(order.length,"zero keyboard path observations");
        const reverse=plan.modal?[...order].reverse():order.slice(0,-1).reverse();
        for(const id of reverse){const focus=await press("backward","reverse");assertFocus(focus,previous);assert.equal(focus.id,id,"reverse Tab must revisit actual forward focus order");previous=focus.id;}
        if(plan.modal){const focus=await press("backward","boundary");assertFocus(focus,previous);assert.equal(focus.id,order.at(-1),"Shift+Tab must wrap to modal last control");}
        // A one-control page still proves reverse movement through its preceding
        // document control/boundary, without asserting a nonexistent 13th item.
        if(!plan.modal&&order.length===1){const focus=await press("backward","boundary");if(!focus.boundary)assertFocus(focus,previous);const restored=await press("forward","boundary");assertFocus(restored);assert.equal(restored.id,order[0],"reverse boundary must restore the actual page target");}
        if(!plan.modal&&observer&&(last?.native||last?.datetime)){
          const anchor=last.id,escaped=await press("backward","boundary");if(!escaped.boundary)assertFocus(escaped);
          const restored=await press("forward","restore");assertFocus(restored);assert.equal(restored.id,anchor,"UA reverse boundary restores exact entry host");
        }
        for(const host of datetimeHosts)for(const direction of ["forward","backward"])assert.ok(exits.has(host+":"+direction),"datetime host must exit in both directions");
        pending.push(...await exerciseActivation(browser,contract.activation,contract.activationTarget));
      }
      let datetimeProven=!!observer,mediaProven=!!observer;let mediaHosts=0;
      for(const [id,path]of uaPaths){
        assert.deepEqual(path.backward,[...path.forward].reverse(),"UA reverse Tab must revisit actual forward segment order");
        assert.ok(path.forward.length>0&&path.forward.length<=16,"bounded nonzero UA path required");
        if(path.kind==="media"){mediaHosts++;for(const direction of ["forward","backward"])assert.ok(mediaExits.has(id+":"+direction),"media host must exit both directions");assert.deepEqual(new Set(path.forward),new Set(path.mediaNodes),"native path must visit the complete expected media focusable set");mediaProven&&=path.negative===true||path.descendant;}else datetimeProven&&=path.descendant;
      }
      if(datetimeHosts.size&&(!datetimeProven||[...uaPaths.values()].filter(p=>p.kind==="datetime").length!==datetimeHosts.size))pending.push("UA_DATETIME_SEGMENT_IDENTITY_PENDING");
      if(plan.native&&(!mediaProven||mediaHosts===0)&&!pending.some(value=>value.startsWith("UA_KEYBOARD_")))pending.push("UA_KEYBOARD_IDENTITY_PENDING: native media keyboard behavior remains required");
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

// The original path evidence remains independently testable; only a successful
// same-condition native activation can discharge its specific missing evidence.
export async function exerciseAccessibility(browser:BrowserHarness,condition:Condition,saveTrace:(value:unknown)=>void=()=>{}){
 const result=await exerciseKeyboardPath(browser,condition,saveTrace);
 if(result.pending.some(reason=>reason.startsWith("ENTER_SPACE_ACTIVATION_EVIDENCE_PENDING"))&&surfaceActivation(condition)){
  const remaining=await exerciseSurfaceActivation(browser,condition,async()=>result.pending);
  assert.deepEqual(remaining,[],"registered activation must complete before clearing its pending reason");
  return {pending:result.pending.filter(reason=>!reason.startsWith("ENTER_SPACE_ACTIVATION_EVIDENCE_PENDING"))};
 }
 return result;
}
