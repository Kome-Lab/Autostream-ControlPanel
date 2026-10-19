import assert from 'node:assert/strict';
import {readFileSync,writeFileSync} from 'node:fs';
import {resolve} from 'node:path';
import {RealBrowser} from './real_browser.mts';
import {observationExpression,type CoverObservation} from './cover_observation.mts';
const root=process.env.UI188_EVIDENCE_DIRECTORY!,caseID=process.env.UI188_PROBE_ATTEMPT!;
const live=JSON.parse(readFileSync(resolve(root,'acceptance-live-'+caseID+'.json'),'utf8'));
const credentials=JSON.parse(readFileSync(process.env.UI188_TEST_CREDENTIALS!,'utf8'));
const browser=await RealBrowser.launch();
try{
 await browser.navigate('http://127.0.0.1:18841/login');
 await browser.waitFor('!!document.querySelector("input[autocomplete=username]")',v=>v===true,'normal login');
 await browser.fillSelector('input[autocomplete=username]','ui188-r4');await browser.fillSelector('input[type=password]',credentials.admin_password);await browser.clickSelector('button[type=submit]');
 await browser.waitFor<string>('location.pathname',v=>!v.startsWith('/login'),'actual CP login',20000);
 await browser.navigate('http://127.0.0.1:18841/admin/streams/');
 const selector=`button[data-slot="stream-primary-trigger"][data-stream-id="${live.stream_id}"]`;
 await browser.waitFor(`!!document.querySelector(${JSON.stringify(selector)})`,v=>v===true,'S1 actual row');await browser.clickSelector(selector);
 const stream=await browser.evaluate<{assigned_encoder_id:string}>(`fetch('/streams/${live.stream_id}').then(r=>{if(r.status!==200)throw Error('actual stream');return r.json()})`);
 const observed=await browser.waitFor<CoverObservation>(observationExpression(live.stream_id,stream.assigned_encoder_id),v=>v.ready===true,'actual product query cache and ready capability read-only',25000);
 assert.equal(observed.cover?.job_generation,1);
 writeFileSync(resolve(root,'cover-cache-probe-'+caseID+'.json'),JSON.stringify({purpose:'Short real CP browser/cache preflight before the two-stream long hold; no Cover PUT',observed,serviceHealthNetwork:browser.serviceHealthNetwork},null,2)+'\n');
 console.log('ACTUAL_CP_QUERY_CACHE_OBSERVATION_PREFLIGHT_PASS');
}finally{await browser.close()}
