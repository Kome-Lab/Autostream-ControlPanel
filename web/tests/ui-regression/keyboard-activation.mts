import assert from "node:assert/strict";
import type {BrowserHarness} from "../helpers/browser-harness.mts";
import { renderedDOM } from "./render-state.mts";
import { assertLayout, layoutExpression } from "./layout-observation.mts";
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
