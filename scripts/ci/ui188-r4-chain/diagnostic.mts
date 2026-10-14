import {readFileSync,writeFileSync} from "node:fs";
import {resolve} from "node:path";
import {RealBrowser} from "./real_browser.mts";
const evidence=resolve(process.env.UI188_EVIDENCE_DIRECTORY!);const caseID=process.env.UI188_PROBE_ATTEMPT!;
const live=JSON.parse(readFileSync(resolve(evidence,`acceptance-live-${caseID}.json`),"utf8"));const credentials=JSON.parse(readFileSync(process.env.UI188_TEST_CREDENTIALS!,"utf8"));
const browser=await RealBrowser.launch();const samples:unknown[]=[];
try{
 await browser.setViewport(1440,1000);await browser.navigate("http://127.0.0.1:18841/login");
 await browser.waitFor('!!document.querySelector("input[autocomplete=username]")',v=>v===true,"login");await browser.fillSelector('input[autocomplete="username"]',"ui188-r4");await browser.fillSelector('input[type="password"]',credentials.admin_password);await browser.clickSelector('button[type="submit"]');
 await browser.waitFor<string>("location.pathname",v=>!v.startsWith("/login"),"authenticated login",20000);await browser.navigate("http://127.0.0.1:18841/admin/streams/");
 const selector=`button[data-slot="stream-primary-trigger"][data-stream-id="${live.stream_id}"]`;await browser.waitFor(`!!document.querySelector(${JSON.stringify(selector)})`,v=>v===true,"stream row");await browser.clickSelector(selector);
 const end=Date.now()+230000;
 while(Date.now()<end){
  const state=await browser.evaluate(`(()=>{const v=document.querySelector('video');return {at:new Date().toISOString(),visibility:document.visibilityState,video:v?{time:v.currentTime,ready:v.readyState,paused:v.paused,width:v.videoWidth,height:v.videoHeight,error:v.error?.code,buffered:Array.from({length:v.buffered.length},(_,i)=>[v.buffered.start(i),v.buffered.end(i)])}:null}})()`);
  samples.push(state);writeFileSync(resolve(evidence,`diagnostic-browser-${caseID}.json`),JSON.stringify({scope:"DIAGNOSTIC_ONLY",sid:live.stream_id,samples,consoleErrors:browser.consoleErrorCount})+"\n");await new Promise(r=>setTimeout(r,1000));
 }
}finally{await browser.close()}
