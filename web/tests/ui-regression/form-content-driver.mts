import assert from "node:assert/strict";
import type { BrowserHarness } from "../helpers/browser-harness.mts";
import type { Condition } from "./matrix.mts";
import { longIdentifier, longText } from "./fixture-inputs.mts";
export async function driveFormContent(browser: BrowserHarness, condition: Condition) {
  if(condition.family!=="stream-create-edit"||!["long-id","long-text"].includes(condition.exercise||""))return async()=>{};
  assert.equal(await browser.evaluate(String.raw`(() => {
    if(document.querySelector('[data-ui-content-field]'))throw Error('unique synthetic marker');
    const inputs=[...document.querySelectorAll('[role=dialog] input')].filter(e=>{
      const style=getComputedStyle(e);
      return e.getClientRects().length&&!e.disabled&&!e.closest('[hidden],[inert]')&&style.display!=='none'&&style.visibility!=='hidden'&&style.visibility!=='collapse'&&style.opacity!=='0'&&[...(e.labels||[])].some(l=>/^(配信枠名|Stream name)\s*\*?$/.test((l.textContent||'').trim()));
    });
    if(inputs.length!==1)throw Error('unique non-secret stream name');
    const e=inputs[0];
    if(e.type!=='text'||e.value!==''||/password|token|secret|credential/i.test([e.name,e.id,e.getAttribute('autocomplete')].join(' ')))throw Error('only empty non-secret synthetic create draft');
    e.setAttribute('data-ui-content-field','');return true;
  })()`),true);
  const restore=async()=>{
    try {
      await browser.fillSelector('[data-ui-content-field]',"");
      await browser.waitFor("document.querySelector('[data-ui-content-field]')?.value",value=>value==="","synthetic draft restored before close");
    } finally {
      await browser.evaluate("document.querySelector('[data-ui-content-field]')?.removeAttribute('data-ui-content-field');true");
    }
  };
  try { await browser.fillSelector('[data-ui-content-field]',condition.exercise==="long-id"?longIdentifier:longText); }
  catch(error) { await restore(); throw error; }
  return restore;
}
