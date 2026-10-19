import assert from "node:assert/strict";
import { writeFileSync } from "node:fs";
import { resolve } from "node:path";
import type { RealBrowser } from "./real_browser.mts";
type Row={id:string;timestamp:string;action:string;result:string;resource_id:string;actor_username:string};
type DOMRow={text:string;timestamp:string|null;actor:string;action:string;result:string;known:string|null;tone:string|null;icon:string|null};
export async function verifyAuditPending(browser:RealBrowser,root:string,caseID:string,sid:string) {
 const path="/audit-logs?q="+encodeURIComponent("streams.discord_youtube_notify");
 const expression=`(async()=>{const r=await fetch(${JSON.stringify(path)});if(r.status!==200)throw Error('real audit '+r.status);return (await r.json()).map(({id,timestamp,action,result,resource_id,actor_username})=>({id,timestamp,action,result,resource_id,actor_username}))})()`;
 const raw=await browser.evaluate<Row[]>(expression);
 assert.ok(raw.every(row=>row.action==="streams.discord_youtube_notify"&&["pending","success"].includes(row.result)));
 assert.equal(new Set(raw.map(row=>JSON.stringify([row.timestamp,row.actor_username,row.action,row.result]))).size,raw.length,"audit row correlation must be unambiguous");
 const pending=raw.filter(r=>r.resource_id===sid&&r.action==="streams.discord_youtube_notify"&&r.result==="pending");
 const success=raw.filter(r=>r.resource_id===sid&&r.action==="streams.discord_youtube_notify"&&r.result==="success");
 assert.equal(pending.length,1);assert.equal(success.length,1);assert.notEqual(pending[0].id,success[0].id);assert.ok(pending[0].timestamp<=success[0].timestamp);
 await browser.navigate("http://127.0.0.1:18841/admin/audit-logs/?q="+encodeURIComponent("streams.discord_youtube_notify"));
 const rowsExpression=`[...document.querySelectorAll('[data-screen-family="audit"] tbody tr[data-record-expanded]')].map(r=>{const b=r.querySelector('td[headers$="-result"] [data-status-known]');return {text:r.innerText,timestamp:r.querySelector('time')?.getAttribute('datetime')||null,actor:r.querySelector('td[headers$="-actor_username"] > div')?.innerText||'',action:r.querySelector('td[headers$="-action"] > div')?.innerText||'',result:b?.innerText||'',known:b?.getAttribute('data-status-known')||null,tone:b?.getAttribute('data-status-tone')||null,icon:b?.querySelector('[data-status-icon]')?.getAttribute('data-status-icon')||null}})`;
 const observed:Record<string,DOMRow[]>={};
 const verify=async(locale:"ja"|"en")=>{
  const rows=await browser.waitFor<DOMRow[]>(rowsExpression,v=>v.length===raw.length&&v.every(r=>r.known==="true"),"actual bilingual notification audit rows",20000);
  for(let index=0;index<raw.length;index++){
   assert.equal(rows[index].timestamp,raw[index].timestamp,"actual full timestamp correlation");assert.equal(rows[index].actor,raw[index].actor_username);
   assert.equal(rows[index].action,locale==="ja"?"DiscordへYouTube配信を通知":"Streams discord youtube notify");
   assert.equal(rows[index].result,raw[index].result==="pending"?(locale==="ja"?"要求済み・反映未確認":"Requested; result unconfirmed"):(locale==="ja"?"成功":"Success"));
   assert.equal(rows[index].tone,raw[index].result==="pending"?"warning":"success");
   assert.equal(rows[index].icon,raw[index].result==="pending"?"clock-3":"circle-check");
   assert.ok(!rows[index].action.includes("streams.discord_youtube_notify"));
  }
  observed[locale]=rows;
  writeFileSync(resolve(root,"r9-audit-"+locale+"-"+caseID+".png"),await browser.captureScreenshot());
 };
 const locale=async(from:string,to:string,label:string)=>{
  const trigger=`[role="combobox"][aria-label="${from}"]`;
  await browser.clickSelector(trigger);
  await browser.evaluate(`(()=>{const e=[...document.querySelectorAll('[role=option]')].find(e=>e.getClientRects().length&&e.innerText===${JSON.stringify(label)});if(!e)throw Error('real locale option missing');e.setAttribute('data-r9-locale','true')})()`);
  await browser.clickSelector('[data-r9-locale=true]');await browser.waitFor<string>('document.documentElement.lang',v=>v===to,'real locale change');
 };
 await verify("ja");await locale("言語","en","English");await verify("en");
 assert.deepEqual(await browser.evaluate<Row[]>(expression),raw,"later success/locale must not rewrite immutable pending audit row");
 await locale("Language","ja","日本語");
 writeFileSync(resolve(root,"r9-audit-pending-same-run-"+caseID+".json"),JSON.stringify({raw,observed,pending_id:pending[0].id,success_id:success[0].id,immutable:true},null,2)+"\n");
 return {pendingID:pending[0].id,successID:success[0].id,locales:["ja","en"],rawPendingPreserved:true};
}
