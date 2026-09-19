import assert from "node:assert/strict";
import type { BrowserHarness } from "../helpers/browser-harness.mts";
import { renderedDOM } from "./render-state.mts";

export const publicDownloadPath="/archive-shares/ui-synthetic-share/download";
export type LinkActivationFacts={keys:number;clicks:number;navigations:number;invalid:boolean;sameDocument:boolean;sameTarget:boolean};
export function assertLinkActivation(facts:LinkActivationFacts,key:"Space"|"Enter"){
  assert.deepEqual(facts,{keys:key==="Space"?1:2,clicks:key==="Space"?0:1,navigations:key==="Space"?0:1,invalid:false,sameDocument:true,sameTarget:true},"native link: Space must not activate; Enter must invoke the exact default navigation once");
}
export async function exerciseNativeLink(browser:BrowserHarness){
  assert.equal(browser.requests.get(publicDownloadPath)||0,0,"link destination must not be requested before activation");
  await browser.evaluate(`(() => {${renderedDOM}
    if(globalThis.__uiNativeLink)throw Error('link owner collision');
    const matches=[...document.querySelectorAll('main[data-screen-family=archive-share] a')].filter(e=>uiAX(e)&&e.getAttribute('href')===${JSON.stringify(publicDownloadPath)});
    if(matches.length!==1||!globalThis.navigation)throw Error('native fixture link or navigation observer unavailable');
    const target=matches[0];if(target.target||target.hasAttribute('download')||target.getAttribute('aria-disabled')==='true')throw Error('unexpected fixture link semantics');
    const v={target,document,href:target.href,url:location.href,keys:0,clicks:0,navigations:0,invalid:false};
    v.key=e=>{if(e.target===v.target&&['Space','Enter'].includes(e.code)){v.keys++;v.invalid||=!e.isTrusted||e.repeat;}};
    v.click=e=>{v.clicks++;v.invalid||=!e.isTrusted||!(e.target===v.target||v.target.contains(e.target))||e.defaultPrevented;};
    v.navigate=e=>{v.navigations++;v.invalid||=!e.isTrusted||e.destination.url!==v.href||e.destination.sameDocument||e.defaultPrevented;};
    target.addEventListener('keydown',v.key);target.addEventListener('click',v.click);navigation.addEventListener('navigate',v.navigate);globalThis.__uiNativeLink=v;return true;
  })()`);
  let primary:unknown;
  const target=`(() => {${renderedDOM}const v=globalThis.__uiNativeLink,e=v.target;if(!e.isConnected||e.ownerDocument!==v.document||e.href!==v.href||location.href!==v.url||!uiAX(e))throw Error('link owner changed');if(document.activeElement!==e)return false;const r=e.getBoundingClientRect(),hit=document.elementFromPoint((r.left+r.right)/2,(r.top+r.bottom)/2);if(r.width<=0||r.height<=0||r.left<0||r.right>innerWidth||r.top<0||r.bottom>innerHeight||!hit||!(hit===e||e.contains(hit)))throw Error('link clipped or covered');return true;})()`;
  const observe="(() => {const v=globalThis.__uiNativeLink;return {keys:v.keys,clicks:v.clicks,navigations:v.navigations,invalid:v.invalid,sameDocument:document===v.document&&location.href===v.url,sameTarget:v.target.isConnected&&v.target.href===v.href&&document.activeElement===v.target};})()";
  try{
    for(const key of ["Space","Enter"]as const){
      let reached=false;for(let step=0;step<128;step++){if(await browser.evaluate<boolean>(target)){reached=true;break;}await browser.pressTab("forward");}
      assert.equal(reached,true,"native Tab must reach the predeclared download anchor");
      await browser.pressNativeKey(key);
      if(key==="Enter"){
        await browser.waitForRequestCount(publicDownloadPath,1);await browser.waitForResponseCount(publicDownloadPath,1);
      }
      await browser.waitForRequestHandlersIdle();
      assertLinkActivation(await browser.evaluate<LinkActivationFacts>(observe),key);
      assert.equal(browser.requests.get(publicDownloadPath)||0,key==="Enter"?1:0);
      assert.deepEqual(browser.responseStatuses.get(publicDownloadPath)||[],key==="Enter"?[204]:[]);
    }
    assert.equal(await browser.evaluate(target),true,"204 leaves the original link/document/focus available");
  }catch(error){primary=error;throw error;}finally{
    try{await browser.evaluate("(() => {const v=globalThis.__uiNativeLink;v.target.removeEventListener('keydown',v.key);v.target.removeEventListener('click',v.click);navigation.removeEventListener('navigate',v.navigate);delete globalThis.__uiNativeLink;return true;})()");}
    catch(error){throw primary?new AggregateError([primary,error],"link activation and cleanup failed",{cause:primary}):error;}
  }
  return [];
}
