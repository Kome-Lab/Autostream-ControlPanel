import assert from 'node:assert/strict';
import {createHash} from 'node:crypto';
import {readFileSync} from 'node:fs';

type Identity = {schema_version:number;scope:string;run_uuid:string;case:string;schema:string;services:Record<string,string>;source_lock_sha256:string;github_run_id:number;github_run_attempt:number;job:string;started_ns:string;source_shas:Record<string,string>};
const canonical=(v:any):any=>Array.isArray(v)?v.map(canonical):v&&typeof v==='object'?Object.fromEntries(Object.entries(v).sort(([a],[b])=>a<b?-1:a>b?1:0).map(([k,x])=>[k,canonical(x)])):v;

export function readIdentity():Identity {
  const raw=readFileSync(process.env.UI188_RUN_IDENTITY!,'utf8');
  assert.equal(createHash('sha256').update(raw).digest('hex'),process.env.UI188_IDENTITY_SHA256);
  const id:Identity=JSON.parse(raw);
  // Duplicate/unknown keys cannot survive canonical equality plus the closed key set.
  assert.equal(raw,JSON.stringify(canonical(id))+'\n');
  assert.deepEqual(Object.keys(id).sort(),['schema_version','scope','run_uuid','case','schema','services','source_lock_sha256','github_run_id','github_run_attempt','job','started_ns','source_shas'].sort());
  assert.match(id.run_uuid,/^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/);
  const hex=id.run_uuid.replaceAll('-','');
  assert.equal(id.case,'gha-'+hex.slice(0,16));assert.equal(id.schema,'ui188_gha_'+hex);
  assert.equal(id.schema_version,2);assert.equal(id.scope,'UI188_ACTIONS_RUN');assert.equal(id.github_run_attempt,1);
  assert.ok(Number.isSafeInteger(id.github_run_id)&&id.github_run_id>0);
  assert.ok(id.job==='full-e2e'||id.job==='contract-smoke');
  assert.match(id.started_ns,/^[1-9][0-9]{18}$/);assert.ok(BigInt(id.started_ns)<=BigInt(Date.now())*1000000n+999999n);
  assert.match(id.source_lock_sha256,/^[0-9a-f]{64}$/);
  assert.deepEqual(Object.keys(id.source_shas).sort(),['bot','cp','encoder','worker']);
  for(const value of Object.values(id.source_shas))assert.match(value,/^[0-9a-f]{40}$/);
  for(const [k,v] of Object.entries({UI188_RUN_UUID:id.run_uuid,UI188_WAIT_CALLER_RUN_ID:id.run_uuid,UI188_PROBE_ATTEMPT:id.case,UI188_DB_SCHEMA:id.schema,UI188_GITHUB_RUN_ID:String(id.github_run_id),UI188_GITHUB_RUN_ATTEMPT:'1',UI188_SOURCE_LOCK_SHA256:id.source_lock_sha256,UI188_IDENTITY_JOB:id.job}))assert.equal(process.env[k],v);
  assert.deepEqual(id.services,Object.fromEntries(['bot-a','bot-b','encoder-a','encoder-b','worker-a','worker-b'].map(k=>[k,k])));
  return id;
}

export function coverProof(identity:Identity,slot:string,live:any,observed:any,frames:any[]) {
  assert.ok(slot==='S1'||slot==='S2');assert.equal(observed.sid,live.stream_id);
  assert.match(live.stream_id,/^[0-9a-f-]{36}$/);assert.equal(live.status,'live');
  assert.equal(observed.normalSandbox,true);assert.equal(observed.consoleErrors,0);assert.equal(observed.narrow.width,390);
  assert.deepEqual(observed.phases.map((p:any)=>p.phase),['A','B','C','OFF']);
  let previous=-1;let generation:number|undefined;
  const revisions=observed.phases.map((p:any,index:number)=>{
    const s=p.state;assert.equal(s.status,'applied');assert.equal(s.applied_revision,s.desired_revision);
    assert.ok(Number.isInteger(s.job_generation)&&s.job_generation>0);generation??=s.job_generation;assert.equal(s.job_generation,generation);
    assert.ok(Number.isInteger(s.applied_revision)&&s.applied_revision>previous);previous=s.applied_revision;
    assert.equal(s.applied_active,index<3);
    if(index<3)assert.equal(s.applied_asset_variant_id,live.visual.candidates[index].variant_id);
    return {phase:p.phase,desired_revision:s.desired_revision,applied_revision:s.applied_revision,active:s.applied_active,asset_variant_id:s.applied_asset_variant_id??null};
  });
  assert.deepEqual(observed.lateCheckpoints.map((p:any)=>p.threshold).sort((a:number,b:number)=>a-b),[580,900]);
  const hls=observed.lateCheckpoints.map((p:any)=>{
    assert.ok(p.before.time>=p.threshold&&p.after.time>p.before.time+1&&p.after.mediaTime>p.before.mediaTime+1&&p.after.frameCount>p.before.frameCount);
    assert.equal(p.after.error,null);assert.equal(p.after.paused,false);assert.ok(p.after.ready>=2);
    return {threshold:p.threshold,before:p.before.time,after:p.after.time,frame_delta:p.after.frameCount-p.before.frameCount,media_delta:p.after.mediaTime-p.before.mediaTime};
  });
  const protectedFrames=frames.filter(f=>f.protected),off=frames.filter(f=>!f.protected);
  assert.equal(observed.frameCount,frames.length);assert.equal(observed.protectedFrameCount,protectedFrames.length);
  assert.ok(protectedFrames.length>=30&&off.length>=30);
  const covered=(f:any)=>[[255,0,0],[0,0,255],[0,128,0]].some(color=>color.every((v,i)=>Math.abs(f.center[i]-v)<40));
  assert.ok(frames.every(f=>f.watermark.slice(0,3).every((v:number)=>v>220)));
  assert.ok(protectedFrames.every(covered)&&off.every(f=>!covered(f)));
  return {schema_version:1,identity,producer:'browser-'+slot,stream_id:live.stream_id,job_generation:generation,revisions,hls,
    frames:frames.length,protected_frames:protectedFrames.length,off_frames:off.length,watermark_checked:true,normal_sandbox:true,console_errors:0,narrow_width:390};
}
