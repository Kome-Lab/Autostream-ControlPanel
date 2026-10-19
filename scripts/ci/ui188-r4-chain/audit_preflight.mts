import assert from "node:assert/strict";
import {readFileSync,writeFileSync} from "node:fs";
import {resolve} from "node:path";
import {RealBrowser} from "./real_browser.mts";
import {configureNativeZoom} from "./native_zoom.mts";
import {verifyAuditPending} from "./audit_pending.mts";
const root=resolve(process.env.UI188_EVIDENCE_DIRECTORY!),caseID=process.env.UI188_PROBE_ATTEMPT!;
const credentials=JSON.parse(readFileSync(process.env.UI188_TEST_CREDENTIALS!,"utf8"));
const live=JSON.parse(readFileSync(resolve(root,"acceptance-live-"+caseID+".json"),"utf8"));
const browser=await RealBrowser.launch();
try {
 await configureNativeZoom(browser,2);await browser.navigate("http://127.0.0.1:18841/login");
 await browser.waitFor('!!document.querySelector("input[autocomplete=username]")',v=>v===true,"real login");
 await browser.fillSelector('input[autocomplete=username]',"ui188-r4");await browser.fillSelector('input[type=password]',credentials.admin_password);await browser.clickSelector('button[type=submit]');
 await browser.waitFor<string>('location.pathname',v=>!v.startsWith('/login'),"real authenticated audit preflight");
 await browser.waitFor<boolean>(`(async()=>{const r=await fetch('/audit-logs?q=streams.discord_youtube_notify');if(r.status!==200)return false;const rows=await r.json();return ['pending','success'].every(result=>rows.some(row=>row.resource_id===${JSON.stringify(live.stream_id)}&&row.result===result))})()`,v=>v===true,"normal same-run notification result rows",20000);
 const proof=await verifyAuditPending(browser,root,caseID,live.stream_id);assert.equal(browser.consoleErrorCount,0);
 writeFileSync(resolve(root,"r9-audit-preflight-"+caseID+".json"),JSON.stringify({scope:"narrow audit preflight only; full S5 remains required",proof,consoleErrors:0},null,2)+"\n");
 console.log("NORMAL_NATIVE200_JA_EN_AUDIT_PREFLIGHT_PASS");
} finally {await browser.close()}
