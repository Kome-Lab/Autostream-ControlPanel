import assert from "node:assert/strict";
import type {BrowserHarness} from "../helpers/browser-harness.mts";
import type { Condition } from "./matrix.mts";
import { renderedDOM } from "./render-state.mts";
import { assertLayout, layoutExpression } from "./layout-observation.mts";
import { exerciseNativeLink, publicDownloadPath } from "./native-link-activation.mts";
// Operate only existing non-mutating disclosure/section controls. Missing paths
// remain pending; ordinary/high-risk action policy is never replaced by a driver.
export type ActivationTarget = { selector: string; sectionId?: string };
export const prepareActivation = (kind: "section" | "disclosure" | "auto", owner?: ActivationTarget) => `(() => {
 ${renderedDOM}
 const visible=e=>uiAX(e)&&!e.disabled;
 const root=uiRoot();
 const contract=${JSON.stringify(owner || null)},kind=${JSON.stringify(kind)};
 if(kind==='section'&&(!contract?.selector||!contract.sectionId))throw Error('predeclared section owner required');
 const sections=kind==='section'?[...root.querySelectorAll(contract.selector)].filter(visible):[];
 const disclosures=[...root.querySelectorAll('[data-slot=column-visibility] > summary')].filter(visible);
 const targets=kind==='section'?sections:disclosures;
 if(targets.length>1)throw Error('ambiguous keyboard activation target');if(!targets.length)return null;
 const target=targets[0],section=sections.length?document.getElementById(target.getAttribute('aria-controls')):null;
 if(kind==='section'&&(!section||target.getAttribute('aria-controls')!==contract.sectionId||!root.contains(section)||!uiAX(section)))throw Error('keyboard section reference missing or wrong owner');
 const value={target,section,clicks:0,initialOpen:!!target.parentElement.open};
 value.listener=()=>value.clicks++;target.addEventListener('click',value.listener);
 globalThis.__uiKeyboardActivation=value;return section?'section':'disclosure';
})()`;
export const activationObservation = `(() => {const v=globalThis.__uiKeyboardActivation;return {clicks:v.clicks,focused:!!v.section&&document.activeElement===v.section,open:!!v.target.parentElement.open,initialOpen:v.initialOpen};})()`;
export function assertActivation(value:{clicks:number;focused:boolean;open:boolean;initialOpen:boolean},kind:string,step:number) {
 assert.equal(value.clicks,step,"Enter/Space must activate exactly once per key");
 if(kind==="section")assert.equal(value.focused,true,"actual section callback must move focus to its referenced section");
 else assert.equal(value.open,step===1?!value.initialOpen:value.initialOpen,"actual disclosure must toggle and restore");
}
export async function exerciseActivation(browser:BrowserHarness, requiredKind: "section"|"disclosure"|"none"|"auto" = "auto", owner?: ActivationTarget) {
 if(requiredKind==="none")return ["ENTER_SPACE_ACTIVATION_EVIDENCE_PENDING: no predeclared non-mutating disclosure/section in this surface"];
 const kind=await browser.evaluate<string|null>(prepareActivation(requiredKind, owner));
 if(!kind){assert.equal(requiredKind,"auto","predeclared activation owner must be present");return ["ENTER_SPACE_ACTIVATION_EVIDENCE_PENDING: no registered non-mutating disclosure/section control in this surface"];}
 if(requiredKind!=="auto")assert.equal(kind,requiredKind,"predeclared activation owner must be present");
 let inputFailed=false;
 try {
  for(const [index,key] of (["Enter","Space"] as const).entries()) {
   let reached=false;
   for(let step=0;step<128;step++) {
    if(await browser.evaluate("document.activeElement===globalThis.__uiKeyboardActivation.target")){reached=true;break;}
    await browser.pressTab("forward");
   }
   assert.equal(reached,true,"real Tab must reach the activation target");
   await browser.pressNativeKey(key);
   await browser.waitFor<number>("globalThis.__uiKeyboardActivation.clicks",value=>value>=index+1,"native activation click observed");
   assertActivation(await browser.evaluate(activationObservation),kind,index+1);
   if(kind==="disclosure"&&index===0) {
    const columns=await browser.evaluate<{count:number;expected:number;allVisibleAndLabelled:boolean}>(`(() => {${renderedDOM}const details=globalThis.__uiKeyboardActivation.target.parentElement,inputs=[...details.querySelectorAll('input[type=checkbox]')];return {count:inputs.length,expected:details.closest('[data-slot=data-table]').querySelectorAll('thead th').length,allVisibleAndLabelled:inputs.every(e=>uiAX(e)&&[...(e.labels||[])].some(label=>label.textContent.trim()))};})()`);
    assert.ok(columns.count>0,"opened disclosure requires column controls");assert.equal(columns.count,columns.expected,"every displayed column keeps its actual visibility control");assert.equal(columns.allVisibleAndLabelled,true,"opened column controls must be visible and labelled");
    assertLayout(await browser.evaluate(layoutExpression));
   }
  }
 } catch(error) {inputFailed=true;throw error;} finally {
  try {await browser.evaluate("const v=globalThis.__uiKeyboardActivation;v.target.removeEventListener('click',v.listener);delete globalThis.__uiKeyboardActivation;true");}
  catch(error){if(!inputFailed)throw error;}
 }
 return [];
}

// These existing controls are registered by surface and purpose before execution.
// Opening a popup and cancelling it does not select an option or submit a form.
export type SurfaceActivation = { kind:"popup"|"cancel"|"mode"|"link"; selector:string; name?:string; role?:"menu"|"listbox"|"dialog"; purpose:string };
export function surfaceActivation(condition:Pick<Condition,"family"|"state"|"exercise">):SurfaceActivation|undefined {
 if(condition.state!=="ready")return;
 if(condition.family==="workers"&&condition.exercise==="Confirmation")return {kind:"cancel",selector:'[role=alertdialog] button',name:'^(Cancel|キャンセル)$',purpose:"Cancel the existing restart confirmation and restore it through its exact opener"};
 if(!["keyboard","forced-colors"].includes(condition.exercise||""))return;
 if(condition.family==="public-archive-share")return {kind:"link",selector:`main[data-screen-family=archive-share] a[href="${publicDownloadPath}"]`,purpose:"Native Enter requests the existing synthetic download destination; Space does not activate; 204 preserves the exact document without saving content"};
 if(["dashboard","monitoring","account"].includes(condition.family))return {kind:"popup",selector:'header button[data-slot=dropdown-menu-trigger]',name:'^(Account menu|アカウントメニュー)$',role:"menu",purpose:"Open and dismiss the existing account navigation menu on this page"};
 if(condition.family==="archive")return {kind:"popup",selector:'main [role=combobox]',role:"listbox",purpose:"Inspect archive filter options without changing the filter"};
 if(condition.family==="metrics")return {kind:"popup",selector:'#metrics-range',role:"listbox",purpose:"Inspect time range options without changing the query"};
 if(condition.family==="audit-logs")return {kind:"popup",selector:'main [data-slot=tabs-content][data-state=active] [role=combobox]',role:"listbox",purpose:"Inspect operation result filter options without changing the query"};
 if(condition.family==="security-settings")return {kind:"popup",selector:'main [role=combobox]',role:"listbox",purpose:"Inspect MFA policy choices without changing the draft or saving"};
 if(condition.family==="nodes")return {kind:"popup",selector:'main button[data-slot=dialog-trigger]',name:'^(Create node|Nodeを新規作成)$',role:"dialog",purpose:"Open and cancel the pristine existing Node registration dialog"};
 if(condition.family==="login")return {kind:"mode",selector:'main button[aria-label]',name:'^(Theme|表示)$',purpose:"Toggle and restore the unauthenticated local theme preview without credential input"};
}
export const prepareSurfaceActivation=(contract:SurfaceActivation)=>`(() => {${renderedDOM}
 const contract=${JSON.stringify(contract)},name=e=>e.getAttribute('aria-label')||e.textContent?.trim()||'';
 if(globalThis.__uiSurfaceActivation)throw Error('activation owner already exists');
 const matches=[...document.querySelectorAll(contract.selector)].filter(e=>uiAX(e)&&!e.disabled&&e.getAttribute('aria-disabled')!=='true'&&(!contract.name||new RegExp(contract.name).test(name(e))));
 if(matches.length!==1)throw Error('predeclared activation owner missing or ambiguous');
 const target=matches[0],dialog=contract.kind==='cancel'?target.closest('[role=alertdialog]'):null,opener=dialog?globalThis.__uiReturnTrigger:null;
 if(dialog&&(!opener?.isConnected||opener.disabled))throw Error('exact confirmation opener missing');
 if(contract.kind==='popup'&&target.getAttribute('aria-expanded')!=='false')throw Error('activation popup must start closed');
 const value={contract,target,dialog,opener,keys:0,clicks:0,invalidKey:false,initialText:target.textContent,initialMode:document.documentElement.classList.contains('dark'),initialTheme:document.documentElement.getAttribute('data-theme'),initialURL:location.href,initialMirror:localStorage.getItem('autostream.ui_preference')};
 value.keyListener=e=>{if(e.target===value.target&&(e.code==='Enter'||e.code==='Space')){if(!e.isTrusted||e.repeat)value.invalidKey=true;value.keys++;}};
 value.clickListener=()=>value.clicks++;
 target.addEventListener('keydown',value.keyListener);target.addEventListener('click',value.clickListener);globalThis.__uiSurfaceActivation=value;return true;
})()`;
export const surfaceActivationTarget=`(() => {${renderedDOM}const v=globalThis.__uiSurfaceActivation,e=v.target;
 if(!e.isConnected||!uiAX(e)||e.disabled||e.getAttribute('aria-disabled')==='true'||location.href!==v.initialURL)throw Error('activation owner changed');
 if(document.activeElement!==e)return false;
 const name=n=>n.getAttribute('aria-label')||n.textContent?.trim()||'';
 const owners=[...document.querySelectorAll(v.contract.selector)].filter(n=>uiAX(n)&&!n.disabled&&(!v.contract.name||new RegExp(v.contract.name).test(name(n))));
 if(owners.length!==1||owners[0]!==e)throw Error('activation owner changed or ambiguous');
 const r=e.getBoundingClientRect(),hit=document.elementFromPoint((r.left+r.right)/2,(r.top+r.bottom)/2);
 if(r.width<=0||r.height<=0||r.left<0||r.right>innerWidth||r.top<0||r.bottom>innerHeight||!hit||!(hit===e||e.contains(hit)))throw Error('activation target is clipped or covered');return true;})()`;
export const surfaceActivationOpened=`(() => {${renderedDOM}const v=globalThis.__uiSurfaceActivation,e=v.target;
 if(!e.isConnected||e.disabled||location.href!==v.initialURL)throw Error('activation target replaced');
 const id=e.getAttribute('aria-controls'),owners=[...document.querySelectorAll('[id]')].filter(n=>n.id===id),popup=owners[0];
 if(e.getAttribute('aria-expanded')!=='true'||owners.length!==1||popup.getAttribute('role')!==v.contract.role||!uiAX(popup))return false;
 const r=popup.getBoundingClientRect();if(r.width<=0||r.height<=0||r.left<0||r.right>innerWidth||r.top<0||r.bottom>innerHeight)return false;
 if(!popup.contains(document.activeElement))return false;v.popup=popup;return true;})()`;
export const surfaceActivationRestored=`(() => {${renderedDOM}const v=globalThis.__uiSurfaceActivation,e=v.target;
 if(!e.isConnected||e.disabled||location.href!==v.initialURL||e.textContent!==v.initialText)throw Error('activation original target/value changed');
 return e.getAttribute('aria-expanded')==='false'&&(!v.popup?.isConnected||!uiAX(v.popup))&&document.activeElement===e;})()`;
export function assertSurfaceKeyCounts(value:{keys:number;clicks:number;invalidKey?:boolean},step:number,kind:SurfaceActivation["kind"]){
 assert.notEqual(value.invalidKey,true,"activation requires trusted nonrepeated keys");
 assert.equal(value.keys,step,"one native key event per activation");
 if(kind!=="popup")assert.equal(value.clicks,step,"one actual callback per native activation");
}
async function seekSurfaceActivation(browser:BrowserHarness){
 for(let step=0;step<128;step++){if(await browser.evaluate<boolean>(surfaceActivationTarget))return;await browser.pressTab("forward");}
 assert.fail("native Tab did not reach the registered activation owner");
}
export async function exerciseSurfaceActivation(browser:BrowserHarness,condition:Condition,fallback:()=>Promise<string[]>){
 const contract=surfaceActivation(condition);if(!contract)return fallback();
 if(contract.kind==="link")return exerciseNativeLink(browser);
 await browser.evaluate(prepareSurfaceActivation(contract));let primary:unknown;
 try{
  for(const [index,key]of(["Enter","Space"]as const).entries()){
   await seekSurfaceActivation(browser);await browser.pressNativeKey(key);
   if(contract.kind==="popup"){
    await browser.waitFor(surfaceActivationOpened,Boolean,"same owner popup opens after native activation");
    assertSurfaceKeyCounts(await browser.evaluate<{keys:number;clicks:number}>("({keys:globalThis.__uiSurfaceActivation.keys,clicks:globalThis.__uiSurfaceActivation.clicks,invalidKey:globalThis.__uiSurfaceActivation.invalidKey})"),index+1,contract.kind);
    await browser.pressNativeKey("Escape");
    await browser.waitFor(surfaceActivationRestored,Boolean,"popup closes and restores exact original trigger and selection");
   }else if(contract.kind==="mode"){
    await browser.waitFor("document.documentElement.classList.contains('dark')",value=>typeof value==="boolean", "theme observed");
    await browser.waitFor(`(() => {const v=globalThis.__uiSurfaceActivation;return document.documentElement.classList.contains('dark')===${index===0?'!v.initialMode':'v.initialMode'}&&document.documentElement.getAttribute('data-theme')===v.initialTheme;})()`,Boolean,"local theme changes and restores");
    assertSurfaceKeyCounts(await browser.evaluate<{keys:number;clicks:number}>("({keys:globalThis.__uiSurfaceActivation.keys,clicks:globalThis.__uiSurfaceActivation.clicks,invalidKey:globalThis.__uiSurfaceActivation.invalidKey})"),index+1,contract.kind);
    assert.equal(await browser.evaluate(surfaceActivationTarget),true,"activation retains the exact local mode owner");
   }else{
    await browser.waitFor(`(() => {const v=globalThis.__uiSurfaceActivation;return !v.dialog.isConnected&&document.querySelectorAll('[role=alertdialog]').length===0&&document.activeElement===v.opener;})()`,Boolean,"Cancel closes the original confirmation and restores its exact opener");
    assertSurfaceKeyCounts(await browser.evaluate<{keys:number;clicks:number}>("({keys:globalThis.__uiSurfaceActivation.keys,clicks:globalThis.__uiSurfaceActivation.clicks,invalidKey:globalThis.__uiSurfaceActivation.invalidKey})"),index+1,contract.kind);
    await browser.pressNativeKey(key);
    await browser.waitFor(`(() => {${renderedDOM}const v=globalThis.__uiSurfaceActivation,dialogs=[...document.querySelectorAll('[role=alertdialog]')].filter(uiAX);if(dialogs.length!==1)return false;const matches=[...dialogs[0].querySelectorAll('button')].filter(e=>uiAX(e)&&!e.disabled&&/^(Cancel|キャンセル)$/.test(e.textContent.trim()));if(matches.length!==1)return false;v.target.removeEventListener('keydown',v.keyListener);v.target.removeEventListener('click',v.clickListener);v.target=matches[0];v.dialog=dialogs[0];v.target.addEventListener('keydown',v.keyListener);v.target.addEventListener('click',v.clickListener);return true;})()`,Boolean,"restore the same confirmation through its original opener");
   }
  }
  if(contract.kind==="mode")await browser.waitFor("localStorage.getItem('autostream.ui_preference')===globalThis.__uiSurfaceActivation.initialMirror",Boolean,"restore existing local mirror");
  assert.equal(await browser.evaluate("location.href===globalThis.__uiSurfaceActivation.initialURL"),true,"activation stays on its condition route");
 }catch(error){primary=error;throw error;}finally{
  try{await browser.evaluate("const v=globalThis.__uiSurfaceActivation;v.target.removeEventListener('keydown',v.keyListener);v.target.removeEventListener('click',v.clickListener);delete globalThis.__uiSurfaceActivation;true");}
  catch(error){throw primary?new AggregateError([primary,error],"activation and cleanup failed",{cause:primary}):error;}
 }
 return [];
}
