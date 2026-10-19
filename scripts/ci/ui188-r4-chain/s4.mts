import assert from 'node:assert/strict';
import {existsSync,readFileSync,writeFileSync} from 'node:fs';
import {resolve} from 'node:path';
import {RealBrowser} from './real_browser.mts';
import {OwnedCDP} from './owned_cdp.mts';
const root=resolve(process.env.UI188_EVIDENCE_DIRECTORY!),caseID=process.env.UI188_PROBE_ATTEMPT!,attempt=process.env.UI188_BROWSER_ATTEMPT||'1';
const live=JSON.parse(readFileSync(resolve(root,'acceptance-live-'+caseID+'.json'),'utf8')),credentials=JSON.parse(readFileSync(process.env.UI188_TEST_CREDENTIALS!,'utf8'));
const browser=await RealBrowser.launch(),base='http://127.0.0.1:18841',sid=live.stream_id,result:Record<string,unknown>={};
let loss:Awaited<ReturnType<OwnedCDP['loseOneStopResponse']>>|undefined;
const save=(suffix:string)=>writeFileSync(resolve(root,'s4-'+suffix+'-'+caseID+'-'+attempt+'.json'),JSON.stringify({...result,network:browser.network},null,2)+'\n');
const get=(path:string)=>browser.evaluate<any>(`(async()=>{const r=await fetch(${JSON.stringify(path)});if(r.status!==200)throw Error('real GET '+r.status);return r.json()})()`);
try{
 await browser.navigate(base+'/login');await browser.waitFor('!!document.querySelector("input[autocomplete=username]")',v=>v===true,'login');
 await browser.fillSelector('input[autocomplete=username]','ui188-r4');await browser.fillSelector('input[type=password]',credentials.admin_password);await browser.clickSelector('button[type=submit]');await browser.waitFor<string>('location.pathname',v=>!v.startsWith('/login'),'authenticated',20000);
 await browser.navigate(base+'/admin/streams/');const before=await get('/streams/'+sid);assert.equal(before.status,'live');result.before=before;result.otherBefore=(await get('/streams')).filter((s:any)=>s.id!==sid);
 const trigger=`button[data-slot="stream-primary-trigger"][data-stream-id="${sid}"]`;await browser.waitFor(`!!document.querySelector(${JSON.stringify(trigger)})`,v=>v===true,'same-run detail');await browser.clickSelector(trigger);
 const stop='button[aria-label="配信を停止"]';await browser.waitFor(`!!document.querySelector(${JSON.stringify(stop)})&&!document.querySelector(${JSON.stringify(stop)}).disabled`,v=>v===true,'normal stop available');
 if(process.env.UI188_LOST_STOP_RESPONSE==='1'){loss=await browser.loseOneStopResponse(base+'/admin/streams/',sid)}
 await browser.clickSelector(stop);const dialog=await browser.waitFor<{text:string;inputs:number}>('(()=>{const d=[...document.querySelectorAll("[role=alertdialog]")].at(-1);return d?{text:d.innerText,inputs:d.querySelectorAll("input").length}:null})()',v=>!!v&&v.text.includes('停止'),'stop consequence');assert.equal(dialog.inputs,0);result.confirmation=dialog;
 await browser.evaluate(`(()=>{const d=[...document.querySelectorAll('[role=alertdialog]')].at(-1);const b=[...d.querySelectorAll('button')].find(b=>b.innerText==='実行');if(!b)throw Error('explicit execute button missing');b.setAttribute('data-ui188-stop-confirm','true')})()`);await browser.clickSelector('[data-ui188-stop-confirm=true]');
 const path='/streams/'+sid+'/stop-outcome?expected_updated_at='+encodeURIComponent(before.updated_at);
 const outcome=await browser.waitFor<any>(`(async()=>{const r=await fetch(${JSON.stringify(path)});return r.status===200?r.json():null})()`,v=>!!v&&v.stop_confirmed&&['ready','completed'].includes(v.stream.status),'durable normal stop receipt',65000);result.outcome=outcome;
 await browser.waitFor('!document.querySelector("[data-ui188-stop-confirm=true]")',v=>v===true,'confirmation outcome converges',15000);
 await browser.waitFor<string>('document.body.innerText',v=>!v.includes('結果不明'),'no permanently unknown stop outcome',15000);
 const detail=await get('/streams/'+sid);result.detail=detail;assert.ok(['ready','completed'].includes(detail.status));
 if(loss){await Promise.race([loss.completed,new Promise((_,reject)=>setTimeout(()=>reject(new Error('actual correlated stop response loss not observed')),5000))]);assert.equal(loss.observed.length,1);assert.equal(loss.observed[0].status,200);assert.deepEqual(loss.errors,[]);assert.equal(loss.loadingFailed.length,1);assert.equal(loss.loadingFailed[0].requestID,loss.observed[0].networkRequestID);assert.equal(loss.loadingFailed[0].errorText,'net::ERR_CONNECTION_CLOSED');result.actualLostResponse=loss.observed;result.correlatedLoadingFailed=loss.loadingFailed;result.stopLossEventOrder=loss.trace;await loss.close();loss=undefined}
 const posts=browser.network.filter(r=>r.method==='POST'&&r.path==='/streams/'+sid+'/stop');assert.equal(posts.length,1,'stop is never automatically retransmitted');result.stopPOSTs=posts.length;
 await browser.navigate(base+'/admin/streams/');const listed=(await get('/streams')).find((s:any)=>s.id===sid);assert.equal(listed.status,detail.status);result.listed=listed;result.listText=await browser.evaluate('document.body.innerText');writeFileSync(resolve(root,'s4-stopped-'+attempt+'.png'),await browser.captureScreenshot());
 save('stop-success');console.log('REAL_BROWSER_STOP_RECEIPT_CONVERGENCE_PASS');
 writeFileSync(resolve(root,'before-delete-'+caseID+'.json'),JSON.stringify({stream_id:sid})+'\n');
 const evidencePath=resolve(root,'before-delete-recording-'+caseID+'.json');const deadline=Date.now()+20000;
 while(!existsSync(evidencePath)){assert.ok(Date.now()<deadline,'real recording observer must finish before delete');await new Promise(r=>setTimeout(r,100))}
 result.recordingBeforeDelete=JSON.parse(readFileSync(evidencePath,'utf8'));assert.equal((result.recordingBeforeDelete as {stream_id:string}).stream_id,sid);

 const barrierProof=resolve(root,'r9-pending-proof-'+caseID+'.json');
 const barrierDeadline=Date.now()+60000;
 while(!existsSync(barrierProof)){assert.ok(Date.now()<barrierDeadline,'actual Encoder report must arrive');await new Promise(r=>setTimeout(r,50))}
 const pendingRemove=`button[aria-label="${before.name} を削除"]`;
 await browser.waitFor(`!!document.querySelector(${JSON.stringify(pendingRemove)})&&!document.querySelector(${JSON.stringify(pendingRemove)}).disabled`,v=>v===true,'pending run normal delete button');
 await browser.clickSelector(pendingRemove);
 await browser.waitFor('!!document.querySelector("[role=alertdialog]")',v=>v===true,'pending delete confirmation');
 await browser.evaluate(`(()=>{const d=[...document.querySelectorAll('[role=alertdialog]')].at(-1);const b=[...d.querySelectorAll('button')].find(b=>b.innerText==='実行');if(!b||d.querySelector('input'))throw Error('normal button-only pending confirmation');b.setAttribute('data-r9-pending-confirm','true')})()`);
 await browser.clickSelector('[data-r9-pending-confirm=true]');
 result.pendingDeleteReason=await browser.waitFor<string>('document.body.innerText',v=>v.includes('録画の登録処理中のため削除できません'),'actual protected DELETE reason',5000);
 assert.equal(browser.network.filter(r=>r.method==='DELETE'&&r.path==='/streams/'+sid).length,1);
 writeFileSync(resolve(root,'r9-pending-ui-observed-'+caseID),sid);
 const accepted=resolve(root,'r9-report-ready-'+caseID+'.json');const reportDeadline=Date.now()+15000;
 while(!existsSync(accepted)){assert.ok(Date.now()<reportDeadline,'real CP report 202 and catalog 3 required');await new Promise(r=>setTimeout(r,50))}
 result.actualArchiveReport=JSON.parse(readFileSync(accepted,'utf8'));assert.equal((result.actualArchiveReport as {artifact_count:number}).artifact_count,3);
 await browser.evaluate(`(()=>{const d=[...document.querySelectorAll('[role=alertdialog]')].at(-1);const b=[...d.querySelectorAll('button')].find(b=>b.innerText==='キャンセル');if(!b)throw Error('cancel pending confirmation');b.setAttribute('data-r9-pending-close','true')})()`);
 await browser.clickSelector('[data-r9-pending-close=true]');
 // An explicit fresh navigation/re-read precedes a second, user-confirmed delete.
 await browser.navigate(base+'/admin/streams/');
 const reported=await get('/streams/'+sid);assert.ok(reported.archive_reported_at);assert.equal(reported.archive_run_id,before.archive_run_id);
 const remove=`button[aria-label="${before.name} を削除"]`;await browser.waitFor(`!!document.querySelector(${JSON.stringify(remove)})&&!document.querySelector(${JSON.stringify(remove)}).disabled`,v=>v===true,'regular stopped-row delete');await browser.clickSelector(remove);
 const deletion=await browser.waitFor<{text:string;inputs:number}>('(()=>{const d=[...document.querySelectorAll("[role=alertdialog]")].at(-1);return d?{text:d.innerText,inputs:d.querySelectorAll("input").length}:null})()',v=>!!v&&v.text.includes('削除'),'delete consequence');assert.equal(deletion.inputs,0);result.deleteConfirmation=deletion;
 await browser.evaluate(`(()=>{const d=[...document.querySelectorAll('[role=alertdialog]')].at(-1);const b=[...d.querySelectorAll('button')].find(b=>b.innerText==='実行');if(!b)throw Error('explicit delete execute button missing');b.setAttribute('data-ui188-delete-confirm','true')})()`);await browser.clickSelector('[data-ui188-delete-confirm=true]');
 await browser.waitFor<boolean>(`(async()=>{const r=await fetch('/streams');if(r.status!==200)throw Error('list reread');return !(await r.json()).some(s=>s.id===${JSON.stringify(sid)})})()`,v=>v===true,'deleted row leaves active list',20000);
 const retained=await get('/streams/'+sid);assert.ok(retained.deleted_at);assert.equal(retained.archive_run_id,before.archive_run_id);result.retainedArchiveRow=retained;assert.equal(browser.network.filter(r=>r.method==='DELETE'&&r.path==='/streams/'+sid).length,2,'one rejected pending attempt plus one explicit post-report deletion');
 result.otherAfter=(await get('/streams')).filter((s:any)=>s.id!==sid);save('delete-success');writeFileSync(resolve(root,'browser-delete-done-'+caseID),sid);console.log('REAL_BROWSER_REGULAR_DELETE_PASS');
}catch(error){result.error=error instanceof Error?error.message:String(error);result.dom=await browser.evaluate('document.body.innerText').catch(()=>undefined);save('failure');throw error}
finally{if(loss)await loss.close();await browser.close()}
