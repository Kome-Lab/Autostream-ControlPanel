import assert from "node:assert/strict";
import type { BrowserHarness } from "../helpers/browser-harness.mts";
import type { NativeFocusObservation } from "../helpers/browser-ua-focus.mts";
import type { Condition } from "./matrix.mts";

// Independent expectations for the unchanged synthetic fixture. An observed error
// cannot choose its own expected state. Playback acceptance is a separate gate.
export const publicFixtureMedia = "data:video/mp4;base64,AAAAHGZ0eXBtcDQyAAAAAG1wNDJpc29t";
export type MediaExpectation = "available" | "preview-403-empty" | "public-fixture-error";
export function mediaExpectation(condition: Pick<Condition,"family"|"state">): MediaExpectation {
  if(condition.state==="ready"&&condition.family==="stream-detail")return "preview-403-empty";
  if(condition.state==="ready"&&condition.family==="public-archive-share")return "public-fixture-error";
  return "available";
}
export function assertMediaIdentity(ua:NativeFocusObservation,expected:MediaExpectation){
  assert.equal(ua.kind,"media");assert.equal(ua.complete,true,"complete media AX set required");
  assert.ok(ua.visible&&ua.stable&&ua.focusable,"media identity must be focused, visible and stable");
  assert.ok(ua.media,"media facts required");
  assert.ok(ua.focusables.length>0&&ua.focusables.length<=32,"bounded complete focusable set required");
  assert.equal(new Set(ua.focusables.map(n=>n.node)).size,ua.focusables.length,"ambiguous media focusable identity");
  const hosts=ua.focusables.filter(n=>n.relation==="host"),leaf=ua.focusables.find(n=>n.node===ua.node);
  assert.equal(hosts.length,1,"one actual media host required");assert.ok(leaf,"focused leaf missing from complete set");
  assert.equal(leaf.relation,ua.relation);assert.equal(leaf.disabled,ua.disabled);
  assert.equal(ua.uaFocusable,ua.focusables.filter(n=>n.relation==="ua-descendant"&&!n.disabled).length);
  assert.ok(ua.media.paused&&ua.media.atStart,"media must remain unplayed");
  if(expected==="available"){
    assert.equal(ua.mediaState,"loaded","available media must be loaded");assert.equal(ua.disabled,false,"available control disabled");
    assert.ok(ua.uaFocusable>0,"loaded media requires operable internal controls");return "available" as const;
  }
  assert.equal(ua.relation,"host","unavailable media requires actual host entry");assert.equal(ua.disabled,true,"expected unavailable media host must suppress operations");
  assert.deepEqual(ua.focusables,[hosts[0]],"unavailable media may not conceal focusable UA descendants");assert.equal(hosts[0].node,ua.node);
  assert.equal(ua.uaFocusable,0,"expected no operable unavailable controls");
  assert.deepEqual(ua.media,expected==="preview-403-empty"
    ?{ready:0,network:0,error:0,source:false,currentSource:false,paused:true,atStart:true}
    :{ready:0,network:3,error:4,source:true,currentSource:true,paused:true,atStart:true},"media state must match the independent fixture contract");
  assert.equal(ua.mediaState,expected==="preview-403-empty"?"empty":"error");return "unavailable" as const;
}
export async function assertMediaFixture(browser:BrowserHarness,condition:Condition){
  const expected=mediaExpectation(condition);
  if(expected==="available")return;
  if(expected==="preview-403-empty"){
    const path="/streams/ui-stream-1/preview-links";
    assert.equal(browser.requests.get(path),1,"negative Preview requires its actual single issue");
    assert.deepEqual(browser.responseStatuses.get(path),[403],"negative Preview requires actual settled403");
  }
  const selector=expected==="preview-403-empty"?'[role=dialog][data-screen-family=stream-detail] #stream-preview video[controls]':'main[data-screen-family=archive-share] video[controls]';
  const expectedSource=expected==="preview-403-empty"?null:publicFixtureMedia;
  assert.equal(await browser.evaluate(`(() => {const nodes=[...document.querySelectorAll(${JSON.stringify(selector)})],host=document.activeElement;return nodes.length===1&&nodes[0]===host&&host.isConnected&&host.getAttribute('src')===${JSON.stringify(expectedSource)}&&globalThis.__uiKeyboardPlan.medias.some(item=>item.element===host);})()`),true,"media host/source must belong to this exact fixture and keyboard plan");
}
export async function exerciseUnavailableMedia(browser:BrowserHarness,condition:Condition,ua:NativeFocusObservation){
  assert.equal(assertMediaIdentity(ua,mediaExpectation(condition)),"unavailable");
  await assertMediaFixture(browser,condition);
  const marker="__uiUnavailableMedia";
  await browser.evaluate(`(() => {if(globalThis.${marker})throw Error('media negative owner collision');const host=document.activeElement,v={host,keys:0,clicks:0,events:0,invalid:false,owners:[],pending:new Set(),last:null,stable:0};v.key=e=>{if(e.target===host&&['Enter','Space'].includes(e.code)){v.keys++;v.invalid||=!e.isTrusted||e.repeat;v.last=null;v.stable=0;}};v.click=()=>v.clicks++;v.play=()=>v.events++;for(let e=host.parentElement;e;e=e.parentElement){if(v.owners.length>=32)throw Error('media scroll owner bound');v.owners.push(e);}v.scroll=e=>{if(v.owners.includes(e.target)){v.pending.add(e.target);v.stable=0;}};v.end=e=>{if(v.owners.includes(e.target))v.pending.delete(e.target);};for(const e of v.owners){e.addEventListener('scroll',v.scroll);e.addEventListener('scrollend',v.end);}host.addEventListener('keydown',v.key);host.addEventListener('click',v.click);for(const type of ['play','playing','seeking','ratechange'])host.addEventListener(type,v.play);globalThis.${marker}=v;return true;})()`);
  let primary:unknown;let restorationTabs=0;
  try{
    for(const [index,key]of(["Enter","Space"]as const).entries()){
      await browser.pressNativeKey(key);
      // Space on a disabled media host may legitimately scroll its containing
      // block. Observe that exact native default action to completion before a
      // following Tab; do not race it with focus scrolling or force a position.
      await browser.waitFor(mediaInputSettled,Boolean,"same media owner native scroll settlement");
      if(key==="Space"&&mediaExpectation(condition)==="public-fixture-error"){
        // Native Space has page-scroll semantics on this disabled host. Return
        // through the predeclared adjacent real link using one Tab/Shift+Tab,
        // not DOM focus/scroll, before comparing the final visible snapshot.
        await browser.evaluate(`(() => {const v=globalThis.${marker},matches=[...document.querySelectorAll('main[data-screen-family=archive-share] a[href="/archive-shares/ui-synthetic-share/download"]')];if(document.activeElement!==v.host||!v.host.isConnected||matches.length!==1)throw Error('negative media restoration owner missing');v.returnPeer=matches[0];return true;})()`);
        await browser.pressTab("forward");restorationTabs++;
        assert.equal(await browser.evaluate(mediaReturnPeer),true,"native Tab must reach the exact visible existing media download peer");
        await browser.pressTab("backward");restorationTabs++;
        assert.equal(await browser.evaluate(`document.activeElement===globalThis.${marker}.host&&globalThis.${marker}.host.isConnected`),true,"native reverse Tab must restore the original media host");
      }
      const next=await browser.observeNativeFocus();assert.deepEqual(next,ua,"negative native input must not change the media snapshot");
      await assertMediaFixture(browser,condition);
      const actual=await browser.evaluate(`(() => {const v=globalThis.${marker};return {keys:v.keys,clicks:v.clicks,events:v.events,invalid:v.invalid,same:v.host===document.activeElement&&v.host.isConnected};})()`);
      assert.deepEqual(actual,{keys:index+1,clicks:0,events:0,invalid:false,same:true},"unavailable media must receive trusted keys without playback or a control action");
    }
  }catch(error){primary=error;throw error;}finally{
    try{await browser.evaluate(`(() => {const v=globalThis.${marker};v.host.removeEventListener('keydown',v.key);v.host.removeEventListener('click',v.click);for(const type of ['play','playing','seeking','ratechange'])v.host.removeEventListener(type,v.play);for(const e of v.owners){e.removeEventListener('scroll',v.scroll);e.removeEventListener('scrollend',v.end);}delete globalThis.${marker};return true;})()`);}
    catch(error){throw primary?new AggregateError([primary,error],"media negative and cleanup failed",{cause:primary}):error;}
  }
  return restorationTabs;
}

export const mediaInputSettled = `(async() => {await new Promise(requestAnimationFrame);const v=globalThis.__uiUnavailableMedia;if(!v||document.activeElement!==v.host||!v.host.isConnected)throw Error('media input owner replaced');const owners=[];for(let e=v.host.parentElement;e;e=e.parentElement)owners.push(e);if(owners.length!==v.owners.length||owners.some((e,i)=>e!==v.owners[i]))throw Error('media scroll owner replaced');const r=v.host.getBoundingClientRect(),current=[r.x,r.y,r.width,r.height,...owners.flatMap(e=>[e.scrollLeft,e.scrollTop])];if(current.some(n=>!Number.isFinite(n)))throw Error('media input geometry unavailable');if(v.last&&current.every((n,i)=>Math.abs(n-v.last[i])<0.25)&&v.pending.size===0)v.stable++;else v.stable=0;v.last=current;return v.stable>=2;})()`;
export const mediaReturnPeer = `(() => {const v=globalThis.__uiUnavailableMedia,e=v.returnPeer;if(!e?.isConnected||document.activeElement!==e||e.closest('[inert],[aria-hidden=true]')||e.getAttribute('aria-disabled')==='true')return false;const r=e.getBoundingClientRect(),s=getComputedStyle(e),hit=document.elementFromPoint(r.x+r.width/2,r.y+r.height/2);return r.width>0&&r.height>0&&r.left>=0&&r.right<=innerWidth&&r.top>=0&&r.bottom<=innerHeight&&s.visibility==='visible'&&s.display!=='none'&&Number(s.opacity)>0&&s.outlineStyle!=='none'&&parseFloat(s.outlineWidth)>=1&&(hit===e||!!hit&&e.contains(hit));})()`;
