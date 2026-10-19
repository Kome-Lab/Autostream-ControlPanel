import assert from "node:assert/strict";
import { existsSync, readFileSync, writeFileSync } from "node:fs";
import { resolve } from "node:path";
import { RealBrowser } from "./real_browser.mts";
import {observeCover,observationExpression,type CoverObservation} from './cover_observation.mts';
import type { VideoCoverState } from "../../../web/src/features/streams/control-platform.ts";

type VideoSnapshot = {w:number;h:number;time:number;ready:number};
type Frame = {wall:number;mediaTime:number;protected:boolean;center:number[];watermark:number[]};
type Confirmation = {text:string;inputs:number};
type Narrow = {width:number;scroll:number;dialogWidth:number;missingImages:number;cssZoom:string;scale:number};

const evidence = resolve(process.env.UI188_EVIDENCE_DIRECTORY!);
const credentials = JSON.parse(readFileSync(process.env.UI188_TEST_CREDENTIALS!, "utf8"));
const live = JSON.parse(readFileSync(resolve(evidence, "acceptance-live-" + process.env.UI188_PROBE_ATTEMPT + ".json"), "utf8"));
const sid: string = live.stream_id;
const attempt = process.env.UI188_PROBE_ATTEMPT + "-" + (process.env.UI188_BROWSER_ATTEMPT || "1");
const coverSelector = 'select[aria-label="切替するフタ画像"]';
const showSelector = 'button[aria-label="Video Coverを表示"]';
const base = "http://127.0.0.1:18841";
const requests: Array<{at: string; method: string; path: string; body?: unknown}> = [];
const browser = await RealBrowser.launch();
const phases: unknown[] = [];
const playbackSamples: unknown[] = [];
const lateCheckpoints: unknown[] = [];
const offObservations:unknown[]=[];
let protectedEnd: number | undefined;
let offIntervalHLS: {start:number;end:number} | undefined;
const save = (name: string, value: unknown) => writeFileSync(resolve(evidence, name), JSON.stringify(value, null, 2) + "\n");
const stateExpression = `(async()=>{const r=await fetch('/streams/${sid}/video-cover-state');if(r.status!==200)throw Error('cover read '+r.status);return r.json()})()`;
try {
  browser.setRequestObserver(({ method, url, postData }) => {
    const path = new URL(url).pathname;
    if (path.startsWith(`/streams/${sid}/`) && (method !== "GET" || path.endsWith("video-cover-state"))) {
      requests.push({at: new Date().toISOString(), method, path, ...(method === "PUT" && path.endsWith("video-cover-state") ? {body: JSON.parse(postData || "{}")} : {})});
    }
  });
  await browser.setViewport(1440, 1000);
  await browser.navigate(base + "/login");
  await browser.waitFor<boolean>('[...document.querySelectorAll("input[autocomplete=username]")].some(e=>e.getClientRects().length>0)', v => v === true, "real hydrated login");
  await browser.fillSelector('input[autocomplete="username"]', "ui188-r4");
  await browser.fillSelector('input[type="password"]', credentials.admin_password);
  await browser.clickSelector('button[type="submit"]');
  await browser.waitFor<string>("location.pathname", v => !v.startsWith("/login"), "normal authenticated login", 20_000);
  await browser.navigate(base + "/admin/streams/");
  const trigger = `button[data-slot="stream-primary-trigger"][data-stream-id="${sid}"]`;
  await browser.waitFor(`!!document.querySelector(${JSON.stringify(trigger)})`, v => v === true, "same execution stream row", 20_000);
  await browser.clickSelector(trigger);
  await browser.waitFor(`!!document.querySelector(${JSON.stringify(coverSelector)})`, v => v === true, "three cover controls", 20_000);
  if(process.env.UI188_RESELECT_A==='1'){
    await browser.evaluate(`(()=>{const e=document.querySelector(${JSON.stringify(coverSelector)});e.value=${JSON.stringify(live.visual.candidates[0].variant_id)};e.dispatchEvent(new Event('change',{bubbles:true}))})()`);
    await browser.clickSelector(showSelector);
  }
  const initial = await browser.waitFor<VideoCoverState>(stateExpression, v => v.status === "applied" && !!v.applied_active && v.applied_asset_variant_id === live.visual.candidates[0].variant_id, "real start cover A", 30_000);
  const detail=await browser.evaluate<{assigned_encoder_id:string}>(`(async()=>{const r=await fetch('/streams/${sid}');if(r.status!==200)throw Error('actual stream detail');return r.json()})()`);
  assert.equal(initial.job_generation, 1);
  await browser.evaluate(`document.querySelector('#stream-preview')?.scrollIntoView({block:'start'})`);
  const video = await browser.waitFor<VideoSnapshot>(`(()=>{const v=document.querySelector('video');return v?{w:v.videoWidth,h:v.videoHeight,time:v.currentTime,ready:v.readyState}:null})()`, v => !!v && v.w === 854 && v.h === 480 && v.ready >= 2, "real HLS decode", 60_000);
  await browser.waitFor<number>("document.querySelector('video').currentTime", v => v > video.time + 1, "same HLS time advancement", 15_000);
  const protectedStart = await browser.evaluate<number>("document.querySelector('video').currentTime");
  await browser.evaluate(`(()=>{
    window.ui188Frames=[];window.ui188Protected=true;
    const v=document.querySelector('video'),c=document.createElement('canvas');c.width=v.videoWidth;c.height=v.videoHeight;const x=c.getContext('2d');
    const sample=(now,m)=>{x.drawImage(v,0,0);window.ui188Frames.push({wall:Date.now(),mediaTime:m.mediaTime,protected:window.ui188Protected,center:[...x.getImageData(420,240,1,1).data],watermark:[...x.getImageData(30,25,1,1).data]});if(window.ui188Frames.length<20000)v.requestVideoFrameCallback(sample)};
    v.requestVideoFrameCallback(sample);
  })()`);
  phases.push({phase: "A", at: new Date().toISOString(), state: initial, video});
  for (const [index, label] of [[1, "B"], [2, "C"]] as const) {
    const variant = live.visual.candidates[index].variant_id;
    await browser.evaluate(`(()=>{const e=document.querySelector('select[aria-label="切替するフタ画像"]');e.value=${JSON.stringify(variant)};e.dispatchEvent(new Event('change',{bubbles:true}))})()`);
    await browser.waitFor(`!document.querySelector(${JSON.stringify(showSelector)}).disabled`, v => v === true, "current permission/capability before cover PUT", 15_000);
    const before = requests.filter(r => r.method === "PUT").length;
    await browser.clickSelector('button[aria-label="Video Coverを表示"]');
    const state = await browser.waitFor<VideoCoverState>(stateExpression, v => v.status === "applied" && !!v.applied_active && v.applied_asset_variant_id === variant && v.applied_revision === v.desired_revision, "real Encoder applied " + label, 30_000);
    assert.equal(state.job_generation, initial.job_generation);
    assert.equal(requests.filter(r => r.method === "PUT").length, before + 1);
    const color = label === "B" ? [0, 0, 255] : [0, 128, 0];
    await browser.waitFor<number[]>(`window.ui188Frames?.at(-1)?.center`, v => !!v && color.every((c, i) => Math.abs(v[i] - c) < 40), "decoded HLS cover " + label, 60_000);
    phases.push({phase: label, at: new Date().toISOString(), state});
    writeFileSync(resolve(evidence, "browser-cover-" + attempt + "-" + label + ".png"), await browser.captureScreenshot());
  }
  // Keep this same job/media running through the original failing 250-400s
  // band. Use actual decoded HLS time, never synthetic PTS or a shorter clip.
  const longAudioSeconds = Number(process.env.UI188_LONG_AUDIO_SECONDS || "410");
  assert.ok(longAudioSeconds >= 410 && longAudioSeconds <= 900);
  const playbackDeadline=Date.now()+(longAudioSeconds+120)*1000;
  const completedLate = new Set<number>();
  while(true){
    assert.ok(!existsSync(resolve(evidence, "host-stop-requested.json")), "host safety stop requested");
    const expression="(()=>{const v=document.querySelector('video');return {at:new Date().toISOString(),time:v.currentTime,ready:v.readyState,paused:v.paused,error:v.error?.code||null,buffered:Array.from({length:v.buffered.length},(_,i)=>[v.buffered.start(i),v.buffered.end(i)]),visibility:document.visibilityState,frameCount:window.ui188Frames.length,mediaTime:window.ui188Frames.at(-1)?.mediaTime}})()";
    const sample=await browser.evaluate<{at:string;time:number;ready:number;paused:boolean;error:number|null;buffered:number[][];visibility:string;frameCount:number;mediaTime:number}>(expression);
    playbackSamples.push(sample);save('browser-playback-'+attempt+'.json',{sid,samples:playbackSamples});
    assert.equal(sample.error,null);assert.ok(Date.now()<playbackDeadline,'same-run long audio and cover interval');
    for (const threshold of [580, 900]) {
      if (longAudioSeconds >= 900 && sample.time >= threshold && !completedLate.has(threshold)) {
        await new Promise(resolve=>setTimeout(resolve,3000));
        const after=await browser.evaluate<typeof sample>(expression);
        assert.equal(after.error,null);assert.equal(after.paused,false);assert.ok(after.ready>=2);
        assert.ok(after.time>sample.time+1 && after.mediaTime>sample.mediaTime+1 && after.frameCount>sample.frameCount, "same HLS actually decodes after late checkpoint");
        lateCheckpoints.push({threshold,before:sample,after});completedLate.add(threshold);
        save('browser-late-checkpoints-'+attempt+'.json',{sid,lateCheckpoints});
        writeFileSync(resolve(evidence,'browser-late-'+attempt+'-'+threshold+'.png'),await browser.captureScreenshot());
      }
    }
    if(sample.time>=protectedStart+longAudioSeconds)break;
    await new Promise(resolve=>setTimeout(resolve,1000));
  }
  const offPUTs=()=>requests.filter(r=>r.method==='PUT'&&(r.body as {active?:boolean})?.active===false);
  const beforeOFF=await browser.evaluate<VideoCoverState>(stateExpression);
  let confirmation:Confirmation|undefined;
  for(let explicitConfirmation=1;explicitConfirmation<=2;explicitConfirmation++){
    assert.equal(offPUTs().length,0,'never retry an issued OFF request');
    const beforeRefresh=await observeCover(browser,sid,detail.assigned_encoder_id);
    offObservations.push({phase:'before normal UI refresh',explicitConfirmation,snapshot:beforeRefresh});
    await browser.evaluate(`(()=>{const s=document.querySelector('section[aria-label="配信ビジュアルとVideo Cover"]');const b=[...s.querySelectorAll('button')].find(b=>b.innerText==='更新');if(!b)throw Error('normal visual refresh missing');b.setAttribute('data-ui188-health-refresh','true')})()`);
    await browser.waitFor(`!document.querySelector('[data-ui188-health-refresh]').disabled`,v=>v===true,'normal refresh enabled',15000);
    const refreshAt=Date.now();await browser.clickSelector('[data-ui188-health-refresh]');
    const fresh=await browser.waitFor<CoverObservation>(observationExpression(sid,detail.assigned_encoder_id),v=>v.ready===true&&v.health!.dataUpdatedAt>=refreshAt,'actual fresh idle permission and Encoder capability',20000);
    assert.equal(fresh.cover?.job_generation,initial.job_generation);assert.equal(fresh.cover?.desired_revision,beforeOFF.desired_revision);
    offObservations.push({phase:'after normal UI refresh',explicitConfirmation,snapshot:fresh});
    await browser.clickSelector('button[aria-label="Video Coverを非表示"]');
    confirmation=await browser.waitFor<Confirmation>(`(()=>{const d=[...document.querySelectorAll('[role=alertdialog]')].at(-1);return d?{text:d.innerText,inputs:d.querySelectorAll('input').length}:null})()`,v=>!!v&&v.text.includes('背後の映像'),'button-only cover consequence');assert.equal(confirmation.inputs,0);
    offObservations.push({phase:'OFF dialog shown',explicitConfirmation,snapshot:await observeCover(browser,sid,detail.assigned_encoder_id)});
    await browser.evaluate(`(()=>{const d=[...document.querySelectorAll('[role=alertdialog]')].at(-1);const b=[...d.querySelectorAll('button')].find(b=>b.innerText==='Coverを非表示');if(!b)throw Error('confirmation action missing');b.setAttribute('data-ui188-confirm','true')})()`);
    offObservations.push({phase:'immediately before explicit confirm',explicitConfirmation,snapshot:await observeCover(browser,sid,detail.assigned_encoder_id)});
    const intendedEnd=await browser.evaluate<number>("document.querySelector('video').currentTime");
    await browser.clickSelector('button[data-ui188-confirm="true"]');
    const settleDeadline=Date.now()+5000;let rejection='';
    while(Date.now()<settleDeadline&&offPUTs().length===0){
      rejection=(await observeCover(browser,sid,detail.assigned_encoder_id)).notice||'';
      if(rejection.includes('capabilityを確認できるまで')||rejection.includes('権限'))break;
      await new Promise(r=>setTimeout(r,50));
    }
    offObservations.push({phase:'after explicit confirm',explicitConfirmation,OFF_PUTs:offPUTs().length,snapshot:await observeCover(browser,sid,detail.assigned_encoder_id)});
    save('browser-OFF-observation-'+attempt+'.json',{sid,offObservations,serviceHealthNetwork:browser.serviceHealthNetwork,requests});
    if(offPUTs().length===1){protectedEnd=intendedEnd;await browser.evaluate('window.ui188Protected=false');break}
    assert.equal(offPUTs().length,0);assert.ok(rejection.includes('capabilityを確認できるまで')||rejection.includes('権限'),'unsent OFF must have an explicit guard rejection');
    const unchanged=await browser.evaluate<VideoCoverState>(stateExpression);assert.equal(unchanged.desired_revision,beforeOFF.desired_revision);assert.equal(unchanged.applied_active,true);
    assert.ok(explicitConfirmation<2,'freshness unresolved after two distinct explicit confirmations');
  }
  assert.equal(offPUTs().length,1,'exactly one real OFF PUT');
  const off = await browser.waitFor<VideoCoverState>(stateExpression, v => v.status === "applied" && v.applied_active === false && v.applied_revision === v.desired_revision, "real Encoder explicit OFF", 30_000);
  phases.push({phase: "OFF", at: new Date().toISOString(), state: off, confirmation});
  const offFrame = await browser.waitFor<Frame>("window.ui188Frames?.at(-1)", f => !!f && ![[255,0,0],[0,0,255],[0,128,0]].some(color=>color.every((c,i)=>Math.abs(f.center[i]-c)<40)) && f.watermark.slice(0,3).every(v=>v>220), "same HLS explicit OFF decoded with Watermark", 30_000);
  await browser.waitFor<number>("document.querySelector('video').currentTime", t=>t>offFrame.mediaTime+3, "same output OFF interval advances", 15_000);
  offIntervalHLS = {start:offFrame.mediaTime,end:await browser.evaluate<number>("document.querySelector('video').currentTime")};
  const frames = await browser.evaluate<Frame[]>("window.ui188Frames");
  const colors = [[255,0,0],[0,0,255],[0,128,0]];
  const protectedFrames = frames.filter((f: {protected: boolean}) => f.protected);
  assert.ok(protectedFrames.length >= 30, "nonempty actual decoded frame denominator");
  for (const f of protectedFrames) {
    assert.ok(colors.some(color => color.every((c,i) => Math.abs(f.center[i]-c)<40)), "unintended base video exposure during cover switching");
    assert.ok(f.watermark.slice(0,3).every((v: number) => v > 220), "Watermark missing above cover");
  }
  await browser.setViewport(390, 844);
  const narrow = await browser.evaluate<Narrow>(`({width:innerWidth,scroll:document.documentElement.scrollWidth,dialogWidth:document.querySelector('[data-screen-family="stream-detail"]').getBoundingClientRect().width,missingImages:[...document.images].filter(i=>i.getClientRects().length&&!i.complete).length,cssZoom:getComputedStyle(document.documentElement).zoom,scale:visualViewport.scale})`);
  assert.ok(narrow.scroll <= narrow.width + 1);
  writeFileSync(resolve(evidence, "browser-cover-" + attempt + "-narrow.png"), await browser.captureScreenshot());
  if(longAudioSeconds>=900)assert.equal(completedLate.size,2,"580 and 900 second decode checkpoints required");
  save("browser-chain-cover-success-" + attempt + ".json", {sid, normalSandbox: true, viewport100: {width:1440,height:1000}, native200: "NOT_RUN", phases, requests, offObservations,serviceHealthNetwork:browser.serviceHealthNetwork,playbackSamples, lateCheckpoints, protectedIntervalHLS: {start:protectedStart,end:protectedEnd,selection:"playback interval before the issued explicit OFF confirmation, including any earlier rejected PUT0 attempt"}, offIntervalHLS, frameCount: frames.length, protectedFrameCount: protectedFrames.length, narrow, consoleErrors: browser.consoleErrorCount});
  save("browser-chain-frames-" + attempt + ".json", frames);
  console.log("REAL_BROWSER_COVER_HLS_PASS " + sid);
} catch (error) {
  const frames = await browser.evaluate<Frame[] | undefined>("window.ui188Frames").catch(() => undefined);
  if (frames) save("browser-chain-frames-failure-" + attempt + ".json", frames);
  const dom = await browser.evaluate("({path:location.pathname,text:document.body.innerText})").catch(() => undefined);
  const video = await browser.evaluate("(()=>{const v=document.querySelector('video');return v?{time:v.currentTime,ready:v.readyState,paused:v.paused,width:v.videoWidth,height:v.videoHeight,error:v.error?.code}:null})()").catch(()=>undefined);
  save("browser-chain-cover-failure-" + attempt + ".json", {sid, phases, requests, offObservations,serviceHealthNetwork:browser.serviceHealthNetwork,protectedEnd, offIntervalHLS, video, dom, network:browser.network,error: error instanceof Error ? error.message : String(error)});
  throw error;
} finally {
  await browser.close();
}
