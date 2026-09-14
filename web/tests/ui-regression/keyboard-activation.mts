import assert from "node:assert/strict";
import type {BrowserHarness} from "../helpers/browser-harness.mts";
// Operate only existing non-mutating disclosure/section controls. Missing paths
// remain pending; ordinary/high-risk action policy is never replaced by a driver.
export const prepareActivation = `(() => {
 const visible=e=>e.getClientRects().length>0&&!e.closest('[inert]')&&!e.disabled;
 const dialogs=[...document.querySelectorAll('[role=dialog],[role=alertdialog]')].filter(visible);
 if(dialogs.length>1)throw Error('ambiguous active keyboard dialog');
 const root=dialogs[0]||document.querySelector('main');
 const sections=[...root.querySelectorAll('[data-slot=section-navigation] button[aria-controls]:first-child')].filter(visible);
 const disclosures=[...root.querySelectorAll('[data-slot=column-visibility] > summary')].filter(visible);
 const targets=sections.length?sections:disclosures;
 if(targets.length>1)throw Error('ambiguous keyboard activation target');if(!targets.length)return null;
 const target=targets[0],section=sections.length?document.getElementById(target.getAttribute('aria-controls')):null;
 if(sections.length&&!section)throw Error('keyboard section reference missing');
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
export async function exerciseActivation(browser:BrowserHarness) {
 const kind=await browser.evaluate<string|null>(prepareActivation);
 if(!kind)return ["ENTER_SPACE_ACTIVATION_EVIDENCE_PENDING: no registered non-mutating disclosure/section control in this surface"];
 try {
  for(const [index,key] of (["Enter","Space"] as const).entries()) {
   await browser.evaluate("globalThis.__uiKeyboardActivation.target.scrollIntoView({block:'center',inline:'nearest'});globalThis.__uiKeyboardActivation.target.focus();true");
   await browser.pressNativeKey(key);
   await browser.waitFor<number>("globalThis.__uiKeyboardActivation.clicks",value=>value>=index+1,"native activation click observed");
   assertActivation(await browser.evaluate(activationObservation),kind,index+1);
  }
 } finally {await browser.evaluate("const v=globalThis.__uiKeyboardActivation;v.target.removeEventListener('click',v.listener);delete globalThis.__uiKeyboardActivation;true");}
 return [];
}
