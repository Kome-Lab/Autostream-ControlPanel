import assert from "node:assert/strict";
import type { BrowserHarness } from "../helpers/browser-harness.mts";
import type { Condition } from "./matrix.mts";
import { longIdentifier, longText } from "./fixture-inputs.mts";
// Independent synthetic contract, checked against the real worker useState defaults.
export const nodeDraftDefaults = Object.freeze({name:"東京本社 Worker 01",description:"番組配信と録画を担当する東京本社のNode Agent",type:"worker"});
export type FreshNodeDraft = Readonly<{conditionID:string}>;
const attemptedNodeDocuments = new WeakSet<BrowserHarness>();
const nodePermits = new WeakMap<BrowserHarness,FreshNodeDraft>();
export async function prepareFreshNodeDraft(browser:BrowserHarness,condition:Condition):Promise<FreshNodeDraft> {
  assert.equal(condition.family,'nodes');assert.equal(condition.state,'ready');
  assert.ok(['long-id','long-text'].includes(condition.exercise||''));
  assert.equal(attemptedNodeDocuments.has(browser),false,'one first registration open in a fresh condition instance');
  attemptedNodeDocuments.add(browser);
  assert.equal(await browser.evaluate(`(() => {const roots=[...document.querySelectorAll('main [data-screen-family=nodes]')];if(location.pathname!=='/admin/nodes/'||roots.length!==1||document.querySelector('[role=dialog]')||globalThis.__uiFreshNodeDraft)throw Error('fresh registration document before first dialog required');globalThis.__uiFreshNodeDraft={document,root:roots[0]};return true;})()`),true);
  const permit=Object.freeze({conditionID:condition.id});nodePermits.set(browser,permit);return permit;
}
export async function driveFormContent(browser: BrowserHarness, condition: Condition, fresh?:FreshNodeDraft) {
  if(!["stream-create-edit","nodes"].includes(condition.family)||!["long-id","long-text"].includes(condition.exercise||""))return async()=>{};
  const node=condition.family==="nodes",description=condition.exercise==="long-text",expected=description?longText:longIdentifier;
  if(node){assert.ok(fresh&&nodePermits.get(browser)===fresh&&fresh.conditionID===condition.id,'fresh first Node registration permit required');nodePermits.delete(browser);}
  // Native value setter + input/change is the existing harness input boundary.
  // Textarea uses its own native prototype; BrowserHarness.fillSelector accepts only Input.
  const writeValue=(restore:boolean)=>browser.evaluate(String.raw`(() => {
    const s=globalThis.__uiContentState,e=globalThis.__uiContentDraft,marked=[...document.querySelectorAll('[data-ui-content-field]')];
    if(!s||s.document!==document||e!==s.element||!e?.isConnected||!s.owner.isConnected||!s.owner.contains(e)||marked.length!==1||marked[0]!==e||e.value!==s.ownedValue)throw Error('synthetic draft owner replaced or independently edited');
    if(e.id!==s.id||e.name!==s.name||e.type!==s.type||e.labels?.[0]!==s.label)throw Error('synthetic draft identity replaced or independently edited');
    if(e.disabled||!e.getClientRects().length||e.closest('[hidden],[inert]'))throw Error('synthetic draft became unavailable');
    const prototype=e.tagName==='TEXTAREA'?HTMLTextAreaElement.prototype:HTMLInputElement.prototype;
    if(!(e instanceof (e.tagName==='TEXTAREA'?HTMLTextAreaElement:HTMLInputElement)))throw Error('actual native input required');
    const setter=Object.getOwnPropertyDescriptor(prototype,'value')?.set;
    if(!setter)throw Error('native input setter missing');
    const value=${restore?'s.original':JSON.stringify(expected)};
    try {setter.call(e,value);} finally {if(e.value===value)s.ownedValue=value;}
    e.dispatchEvent(new Event('input',{bubbles:true}));e.dispatchEvent(new Event('change',{bubbles:true}));
    if(e.value!==value)throw Error('synthetic input changed during delivery');
    return true;
  })()`);
  const clear=()=>browser.evaluate("globalThis.__uiContentState?.element.removeAttribute('data-ui-content-field');delete globalThis.__uiContentDraft;delete globalThis.__uiContentState;delete globalThis.__uiFreshNodeDraft;true");
  let owned=false,restored=false;
  const restore=async()=>{
    if(restored)return;
    let failure:unknown;
    try {
      assert.equal(await writeValue(true),true);
      await browser.waitFor("globalThis.__uiContentDraft?.value",value=>value===(node?(description?nodeDraftDefaults.description:nodeDraftDefaults.name):""),"exact original synthetic draft restored before close");
    } catch(error){failure=error;throw error;}
    finally {restored=true;try{await clear();}catch(cleanup){if(failure)throw new AggregateError([failure,cleanup],'synthetic restoration and marker cleanup failed',{cause:failure});throw cleanup;}}
  };
  try {
    assert.equal(await browser.evaluate(String.raw`(() => {
      if(document.querySelector('[data-ui-content-field]')||globalThis.__uiContentDraft||globalThis.__uiContentState)throw Error('unique synthetic marker');
      const node=${node},description=${description},defaults=${JSON.stringify(nodeDraftDefaults)};
      const dialogs=[...document.querySelectorAll('[role=dialog]')],owner=dialogs[0];
      if(dialogs.length!==1)throw Error('unique registration dialog owner required');
      if(node&&(location.pathname!=='/admin/nodes/'||globalThis.__uiFreshNodeDraft?.document!==document||!globalThis.__uiFreshNodeDraft.root.isConnected||!/^(ノード登録|Node registration)$/i.test(owner.querySelector('[data-slot=dialog-title]')?.textContent.trim()||'')))throw Error('actual fresh registration dialog owner required');
      const visible=e=>{if(!e?.isConnected||!e.getClientRects().length||e.disabled||e.closest('[hidden],[inert]'))return false;for(let p=e;p;p=p.parentElement){const s=getComputedStyle(p);if(s.display==='none'||s.visibility==='hidden'||s.visibility==='collapse'||s.opacity==='0')return false;}return true;};
      const labelled=(e,name)=>{const labels=[...(e.labels||[])];return visible(e)&&labels.length===1&&visible(labels[0])&&name.test(labels[0].textContent.trim())&&(!node||!!e.id&&labels[0].htmlFor===e.id&&[...document.querySelectorAll('[id]')].filter(x=>x.id===e.id).length===1);};
      const select=(selector,name)=>{const inputs=[...owner.querySelectorAll(selector)].filter(e=>labelled(e,name));if(inputs.length!==1)throw Error('unique non-secret form field');return inputs[0];};
      const nameField=select('input',node?/^(名称|Name)$/:/^(配信枠名|Stream name)\s*\*?$/);
      const e=node&&description?select('textarea',/^(説明|Description)$/):nameField;
      if(!(e.type==='text'||node&&description&&e.tagName==='TEXTAREA')||/password|token|secret|credential/i.test([e.name,e.id,e.getAttribute('autocomplete')].join(' ')))throw Error('only non-secret synthetic create draft');
      if(node){
        const descriptionField=description?e:select('textarea',/^(説明|Description)$/);
        const typeField=select('button[role=combobox]',/^(Node種別|Node type)$/);
        if(typeField.textContent.trim()!=='Worker Node Agent'||nameField.value!==defaults.name||descriptionField.value!==defaults.description)throw Error('exact fresh worker source defaults required');
      }else if(e.value!=='')throw Error('only empty non-secret synthetic create draft');
      e.setAttribute('data-ui-content-field','');globalThis.__uiContentDraft=e;
      globalThis.__uiContentState={document,owner,element:e,id:e.id,name:e.name,type:e.type,label:e.labels[0],original:e.value,ownedValue:e.value};return true;
    })()`),true);
    owned=true;
    assert.equal(await writeValue(false),true);
    await browser.waitFor("globalThis.__uiContentDraft?.value",value=>value===expected,"full synthetic content reached actual input");
    return restore;
  }catch(error){
    try {if(owned)await restore();else if(node)await browser.evaluate("delete globalThis.__uiFreshNodeDraft;true");}
    catch(cleanup){throw new AggregateError([error,cleanup],"synthetic input failed and restoration failed",{cause:error});}
    throw error;
  }
}
