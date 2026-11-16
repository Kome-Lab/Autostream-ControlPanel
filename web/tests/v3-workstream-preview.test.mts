import "./ui-regression/component-loader.mts";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { createServer } from "node:http";
import test from "node:test";
import { actualCallback, actualEffect, actualFunction } from "./ui-regression/source-callback.mts";
const { createStreamActionController } = await import("../src/features/streams/stream-action-controller.ts");
const { APIError } = await import("../src/lib/api/client.ts");
import { STREAM_PREVIEW_PLAYBACK_DEADLINE_MS, isStreamPreviewPlaybackReady, selectStreamPreviewPlaybackEngine, streamPreviewPlaybackDiagnosticMessage, resolveStreamPreviewURL } from "../src/lib/stream-preview.ts";
import { lifecycleOutcomePresentation } from "../src/features/streams/lifecycle-outcome-presentation.ts";

const previewSource=readFileSync(new URL("../src/features/streams/stream-preview.tsx",import.meta.url),"utf8");
const hookSource=readFileSync(new URL("../src/features/streams/use-stream-preview-playback.ts",import.meta.url),"utf8");
const playerSource=readFileSync(new URL("../src/features/streams/stream-preview-player-view.tsx",import.meta.url),"utf8");
const stream={id:"preview-fixture",name:"Synthetic preview",status:"live",updated_at:"2026-11-15T00:00:00Z"};

function playbackFixture(supported=true,native=true,onLoad?:(url:string)=>void){
  const listeners=new Map<string,()=>void>(),states:string[]=[],errors:string[]=[],details:string[]=[],timers=new Map<number,()=>void>();
  let timer=0,rejectPlay:((reason?:unknown)=>void)|undefined;
  const video={currentTime:0,muted:true,paused:true,src:"",error:null,loads:0,
    play:()=>{video.paused=false;return new Promise<void>((_,reject)=>{rejectPlay=reject;});},
    pause:()=>{video.paused=true;},canPlayType:()=>native?"probably":"",
    addEventListener:(event:string,callback:()=>void)=>listeners.set(event,callback),removeEventListener:(event:string)=>listeners.delete(event),
    removeAttribute:(name:string)=>{if(name==="src")video.src="";},load:()=>{video.loads++;},
  };
  const instances:FixtureHls[]=[];
  class FixtureHls {
    static Events={MEDIA_ATTACHED:"attached",MANIFEST_PARSED:"manifest",FRAG_BUFFERED:"buffered",ERROR:"error"};
    static isSupported(){return supported;}
    handlers=new Map<string,(...args:unknown[])=>void>();sources:string[]=[];destroyed=false;recoveries=0;
    constructor(){instances.push(this);}
    attachMedia(){}
    on(name:string,callback:(...args:unknown[])=>void){this.handlers.set(name,callback);}
    loadSource(url:string){this.sources.push(url);onLoad?.(url);}
    recoverMediaError(){this.recoveries++;}
    stopLoad(){}
    destroy(){this.destroyed=true;}
  }
  const diagnostic={current:null as unknown};
  const originalWindow=Object.getOwnPropertyDescriptor(globalThis,"window");
  Object.defineProperty(globalThis,"window",{configurable:true,value:{setTimeout:(callback:()=>void)=>{const id=++timer;timers.set(id,callback);return id;},clearTimeout:(id:number)=>timers.delete(id)}});
  const run=()=>actualEffect(hookSource,"const video",{
    videoRef:{current:video},playbackURL:"http://127.0.0.1/stream-previews/synthetic/index.m3u8",playbackDiagnosticRef:diagnostic,
    setPlaybackState:(value:string)=>states.push(value),setPlaybackError:(value:string)=>errors.push(value),setPlaybackDetail:(value:string)=>details.push(value),
    Hls:FixtureHls,ErrorTypes:{NETWORK_ERROR:"network",MEDIA_ERROR:"media"},STREAM_PREVIEW_PLAYBACK_DEADLINE_MS,isStreamPreviewPlaybackReady,selectStreamPreviewPlaybackEngine,streamPreviewPlaybackDiagnosticMessage,
  })();
  const restore=()=>{if(originalWindow)Object.defineProperty(globalThis,"window",originalWindow);else Reflect.deleteProperty(globalThis,"window");};
  return {video,listeners,states,errors,details,timers,diagnostic,instances,run,restore,rejectPlay:()=>rejectPlay?.(Error("synthetic play rejection"))};
}

test("WS7-PREV-01: actual issue controller retains URL aliases, rejection and same-attempt uncertainty",async t=>{
  const originalWindow=Object.getOwnPropertyDescriptor(globalThis,"window");
  Object.defineProperty(globalThis,"window",{configurable:true,value:{location:{origin:"http://127.0.0.1"}}});
  t.after(()=>{if(originalWindow)Object.defineProperty(globalThis,"window",originalWindow);else Reflect.deleteProperty(globalThis,"window");});
  const isPreviewLink=actualFunction(previewSource,"isPreviewLink",{});
  for(const fields of [{url:"/stream-previews/synthetic/index.m3u8"},{url:"/legacy.m3u8",playback_url:"/preferred.m3u8",player_url:"/player/synthetic"}]){
    await t.test("URL compatibility "+Object.keys(fields).join(","),async()=>{
      let sends=0,saved:Record<string,unknown>|null=null;
      const controller=createStreamActionController({getPermissions:()=>({kind:"ready",permissions:["streams.read"]}),getState:()=>({kind:"ready",freshness:"fresh",fingerprint:"preview-original"}),mutate:async()=>{sends++;return {stream_id:stream.id,expires_at:"2026-11-16T00:00:00Z",...fields};}});
      const issue=actualCallback(previewSource,"issuePreviewLink",{issuePending:false,streamRef:{current:stream},controller,setIssuePending(){},setPreviewLink:(value:Record<string,unknown>|null)=>{saved=value;},setPreviewLinkError(){},setPlaybackError(){},setPlaybackState(){},setCopied(){},isPreviewLink,resolveStreamPreviewURL,t:(key:string)=>key});
      await issue();assert.equal(sends,1);assert.ok(saved);assert.equal(Reflect.get(saved,"playback_url"),"http://127.0.0.1"+(fields.playback_url||fields.url));assert.equal(Reflect.get(saved,"player_url"),"http://127.0.0.1"+(fields.player_url||fields.url));
    });
  }
  await t.test("exact HTTP403 and lost response never become a successful link or blind replay",async()=>{
    let sends=0;let status=403;
    const server=createServer((request,response)=>{sends++;assert.equal(request.method,"POST");if(status===0){response.destroy();return;}response.statusCode=status;response.setHeader("content-type","application/json");response.end('{"code":"forbidden"}');});
    await new Promise<void>(resolve=>server.listen(0,"127.0.0.1",resolve));const address=server.address();assert.ok(address&&typeof address==="object");
    try{
      const create=()=>createStreamActionController({getPermissions:()=>({kind:"ready",permissions:["streams.read"]}),getState:()=>({kind:"ready",freshness:"fresh",fingerprint:"preview-original"}),mutate:async()=>{const r=await fetch(`http://127.0.0.1:${address.port}/preview-links`,{method:"POST"});if(!r.ok)throw new APIError("Synthetic refusal",r.status,"forbidden");return r.json();}});
      const controller=create();const opened=await controller.open({id:"STR-11",stream});if(opened.kind!=="allowed")throw Error("not opened");assert.equal((await controller.submit(opened,{confirmed:true})).kind,"failed");assert.equal(sends,1);
      status=0;const uncertain=create();const next=await uncertain.open({id:"STR-11",stream});if(next.kind!=="allowed")throw Error("not opened");assert.equal((await uncertain.submit(next,{confirmed:true})).kind,"outcome_unknown");assert.equal((await uncertain.submit(next,{confirmed:true})).kind,"blocked");assert.equal(sends,2);
    }finally{await new Promise<void>((resolve,reject)=>server.close(error=>error?reject(error):resolve()));}
  });
});

test("WS7-PREV-02: existing signed media failures retry playback without issuing an operation",async t=>{
  for(const status of [206,403,404,409,410])await t.test(`Range/segment fixture HTTP${status}`,async()=>{
    let gets=0,posts=0;const transfers:Promise<void>[]=[];
    const server=createServer((request,response)=>{if(request.method!=="GET")posts++;else gets++;assert.equal(request.headers.range,"bytes=0-3");response.statusCode=status;response.setHeader("content-type","video/mp2t");if(status===206)response.setHeader("Content-Range","bytes 0-3/4");response.end("test");});
    await new Promise<void>(resolve=>server.listen(0,"127.0.0.1",resolve));const address=server.address();assert.ok(address&&typeof address==="object");
    const original=`http://127.0.0.1:${address.port}/stream-previews/synthetic/index.m3u8`;
    const owner=playbackFixture(true,true,()=>{transfers.push(fetch(original,{headers:{Range:"bytes=0-3"}}).then(async response=>{assert.equal(response.status,status);if(status===206)assert.equal(response.headers.get("content-range"),"bytes 0-3/4");await response.arrayBuffer();}));});
    try{
      const cleanup=owner.run();const hls=owner.instances[0];assert.ok(hls);hls.handlers.get("attached")?.();await Promise.all(transfers);
      hls.handlers.get("error")?.("error",{fatal:true,type:"network",details:"https://fixture.invalid/private-url"});
      assert.equal(owner.states.at(-1),"retrying");assert.equal(posts,0);assert.equal(gets,1);
      const retry=[...owner.timers.values()].at(-1);assert.ok(retry);retry();await Promise.all(transfers);assert.equal(posts,0);assert.equal(gets,2);
      assert.deepEqual(hls.sources,["http://127.0.0.1/stream-previews/synthetic/index.m3u8","http://127.0.0.1/stream-previews/synthetic/index.m3u8"]);
      cleanup();assert.equal(hls.destroyed,true);assert.equal(owner.timers.size,0);assert.equal(owner.listeners.size,0);
      assert.doesNotMatch(owner.details.join(" "),/fixture\.invalid|private-url/);
    }finally{owner.restore();await new Promise<void>((resolve,reject)=>server.close(error=>error?reject(error):resolve()));}
  });
  t.diagnostic("Real playback hook with fake HLS transport and loopback HTTP. No native codec, real media segment qualification or remote playback claimed.");
});

test("WS7-PREV-03: actual controls, native fallback and retry cleanup reject stale callbacks",async t=>{
  await t.test("native stale play rejection and error cannot poison the next session",async()=>{
    const owner=playbackFixture(false,true);
    try{const cleanup=owner.run();assert.equal(owner.instances.length,0);const staleError=owner.listeners.get("error");assert.ok(staleError);cleanup();const states=owner.states.length;owner.rejectPlay();staleError();await Promise.resolve();await Promise.resolve();assert.equal(owner.states.length,states);assert.equal(owner.diagnostic.current,null);assert.equal(owner.timers.size,0);assert.equal(owner.video.src,"");assert.equal(owner.video.loads,1);}finally{owner.restore();}
  });
  await t.test("local ready requires advancing media time and never becomes qualified remote proof",()=>{
    const owner=playbackFixture(true,true);
    try{const cleanup=owner.run();owner.listeners.get("canplay")?.();owner.listeners.get("playing")?.();assert.notEqual(owner.states.at(-1),"ready");owner.video.currentTime=1;owner.listeners.get("timeupdate")?.();assert.equal(owner.states.at(-1),"ready");assert.equal(lifecycleOutcomePresentation(undefined).remoteProof,"not-qualified");const hls=owner.instances[0],stale=hls.handlers.get("manifest");cleanup();const states=owner.states.length;stale?.();hls.handlers.get("error")?.("error",{fatal:true,type:"media"});assert.equal(owner.states.length,states);assert.equal(hls.recoveries,0);}finally{owner.restore();}
  });
  await t.test("existing play/pause mute and +-10 seek stay in their own playback controls",()=>{
    let refreshes=0,muted=true;const video={paused:true,currentTime:30,muted:true,play(){this.paused=false;return Promise.resolve();},pause(){this.paused=true;}};
    const bindings={videoRef:{current:video},seekWindow:{start:10,end:40,position:30},refreshSeekWindow(){refreshes++;},setIsMuted:(value:boolean)=>{muted=value;}};
    const toggle=actualCallback(playerSource,"togglePlayback",bindings),seek=actualCallback(playerSource,"seekBy",bindings),mute=actualCallback(playerSource,"toggleMuted",bindings);
    toggle();assert.equal(video.paused,false);toggle();assert.equal(video.paused,true);seek(10);assert.equal(video.currentTime,40);seek(-10);assert.equal(video.currentTime,30);seek(-100);assert.equal(video.currentTime,10);assert.equal(refreshes,3);assert.equal(video.muted,true);mute();assert.equal(muted,false);
    assert.match(playerSource,/<video[^>]*muted=/);assert.match(previewSource,/<video[^>]*\bmuted\b/);
    assert.match(previewSource,/onClick=\{\(\) => setPlaybackRetry\(value => value \+ 1\)\}/);
  });
});
