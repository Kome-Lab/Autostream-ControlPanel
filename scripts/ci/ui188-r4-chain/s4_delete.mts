import assert from 'node:assert/strict';
import {readFileSync,writeFileSync} from 'node:fs';
import {resolve} from 'node:path';
import {RealBrowser} from './real_browser.mts';
const root=resolve(process.env.UI188_EVIDENCE_DIRECTORY!),caseID=process.env.UI188_PROBE_ATTEMPT!,attempt=process.env.UI188_BROWSER_ATTEMPT||'1';
const live=JSON.parse(readFileSync(resolve(root,'acceptance-live-'+caseID+'.json'),'utf8')),credentials=JSON.parse(readFileSync(process.env.UI188_TEST_CREDENTIALS!,'utf8'));
const browser=await RealBrowser.launch(),base='http://127.0.0.1:18841',sid=live.stream_id,result:Record<string,unknown>={};
const get=(path:string)=>browser.evaluate<any>(`(async()=>{const r=await fetch(${JSON.stringify(path)});if(r.status!==200)throw Error('real GET '+r.status);return r.json()})()`);
try{
 await browser.navigate(base+'/login');await browser.waitFor('!!document.querySelector("input[autocomplete=username]")',v=>v===true,'login');await browser.fillSelector('input[autocomplete=username]','ui188-r4');await browser.fillSelector('input[type=password]',credentials.admin_password);await browser.clickSelector('button[type=submit]');await browser.waitFor<string>('location.pathname',v=>!v.startsWith('/login'),'authenticated',20000);
 await browser.navigate(base+'/admin/streams/');const before=await get('/streams/'+sid);assert.equal(before.status,'ready');assert.ok(!before.deleted_at);result.before=before;
 const recording=JSON.parse(readFileSync(resolve(root,'before-delete-recording-'+caseID+'.json'),'utf8'));assert.equal(recording.stream_id,sid);result.recordingBefore=recording;
 const remove=`button[aria-label="${before.name} を削除"]`;await browser.waitFor(`!!document.querySelector(${JSON.stringify(remove)})&&!document.querySelector(${JSON.stringify(remove)}).disabled`,v=>v===true,'regular stopped-row delete');await browser.clickSelector(remove);
 const dialog=await browser.waitFor<{text:string;inputs:number}>('(()=>{const d=[...document.querySelectorAll("[role=alertdialog]")].at(-1);return d?{text:d.innerText,inputs:d.querySelectorAll("input").length}:null})()',v=>!!v&&v.text.includes('配信枠を削除')&&v.text.includes('録画データと録画用の履歴は保持'),'specific delete title and preservation consequence');assert.equal(dialog.inputs,0);result.confirmation=dialog;writeFileSync(resolve(root,'s4-delete-confirm-'+attempt+'.png'),await browser.captureScreenshot());
 await browser.evaluate(`(()=>{const d=[...document.querySelectorAll('[role=alertdialog]')].at(-1);const b=[...d.querySelectorAll('button')].find(b=>b.innerText==='実行');if(!b)throw Error('explicit execute button missing');b.setAttribute('data-ui188-delete-confirm','true')})()`);await browser.clickSelector('[data-ui188-delete-confirm=true]');
 await browser.waitFor<boolean>(`(async()=>{const r=await fetch('/streams');if(r.status!==200)throw Error('list reread');return !(await r.json()).some(s=>s.id===${JSON.stringify(sid)})})()`,v=>v===true,'deleted row leaves active list',20000);
 result.retained=await get('/streams/'+sid);assert.ok((result.retained as any).deleted_at);assert.equal((result.retained as any).archive_run_id,before.archive_run_id);assert.equal(browser.network.filter(r=>r.method==='DELETE'&&r.path==='/streams/'+sid).length,1);
 writeFileSync(resolve(root,'s4-delete-success-'+caseID+'-'+attempt+'.json'),JSON.stringify({...result,network:browser.network},null,2)+'\n');writeFileSync(resolve(root,'browser-delete-done-'+caseID),sid);console.log('REAL_FINAL_SOURCE_BROWSER_REGULAR_DELETE_PASS');
}catch(error){writeFileSync(resolve(root,'s4-delete-failure-'+caseID+'-'+attempt+'.json'),JSON.stringify({...result,error:error instanceof Error?error.message:String(error),dom:await browser.evaluate('document.body.innerText').catch(()=>undefined)},null,2)+'\n');throw error}finally{await browser.close()}
