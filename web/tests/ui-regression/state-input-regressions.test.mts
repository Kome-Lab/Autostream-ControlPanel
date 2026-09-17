import "./component-loader.mts";
import assert from "node:assert/strict";
import test from "node:test";
import { createElement } from "react";
import { createUIFixture } from "./route-fixture.mts";
import { conditions,inventory,type Condition } from "./matrix.mts";
import { statePaths,assertState,deniedPrimaryRequests,loadingPaths,hasFailureCopy,failureTextExpression,type RequestEvidence } from "./state-drivers.mts";
import { contentInput,canonicalFixturePath,longIdentifier,longText,assertContentReached } from "./fixture-inputs.mts";
import { observationExpression,assertObservation,type UIObservation } from "./observation.mts";
import { observerDOM,Element } from "./observer-dom.mts";
import { renderUI } from "./render-ui.mts";
import { createHarnessFixture } from "../helpers/browser-cdp-socket-fixture.mts";
import { createConditionRunner } from "./condition-lifecycle.mts";
import { navigateDocument } from "./navigation.mts";
import { assertCurrentObservation } from "./run-browser.mts";
import { refreshCurrentState } from "./run-browser.mts";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { WorkerNode } from "../../src/types/domain.ts";
import { actualCallback, actualFunction } from "./source-callback.mts";
import { readFileSync } from "node:fs";
import ts from "typescript";
import { layoutExpression,type LayoutObservation } from "./layout-observation.mts";
import type { BrowserHarness } from "../helpers/browser-harness.mts";
const {ResourceTable}=await import("../../src/features/resources/resource-table.tsx");
const {resourcePages}=await import("../../src/features/resources/resource-config.ts");
const {enrichResourceRow,visibleColumns}=await import("../../src/features/resources/resource-presentation.tsx");
const base={...conditions[0],locale:"en"};
test('UI-WORKER-DENIAL-019: actual scenario phases and waitForWorkerAction distinguish interim unknown, handler settlement and final denied',async()=>{
 const {waitForWorkerAction}=await import('../ui-browser-worker-helpers.mts');
 const source=readFileSync(new URL('../ui-browser-worker-restart-scenarios.mts',import.meta.url),'utf8'),file=ts.createSourceFile('worker.mts',source,ts.ScriptTarget.Latest,true,ts.ScriptKind.TS);
 const phases=new Map<string,ts.CallExpression>();const visit=(n:ts.Node)=>{if(ts.isVariableDeclaration(n)&&['unknown','denied','revoked'].includes(n.name.getText(file))&&n.initializer&&ts.isAwaitExpression(n.initializer)&&ts.isCallExpression(n.initializer.expression))phases.set(n.name.getText(file),n.initializer.expression);ts.forEachChild(n,visit);};visit(file);assert.equal(phases.size,3);
 const unknown='The restart permission could not be verified.',denied='You do not have permission to restart workers.';
 for(const [phase,call] of phases){
  assert.equal(call.expression.getText(file),'waitForWorkerAction');assert.equal(call.arguments[1].getText(file),'"Restart worker"');assert.equal(call.arguments[2].getText(file),'"Worker One"');
  const predicate=actualCallback('const accept='+call.arguments[3].getText(file),'accept',{}) as Parameters<typeof waitForWorkerAction>[3];
  const oracle=phase==='unknown'?/permission could not be verified/i:/do not have permission to restart workers/i;
  async function wait(states:({disabled:boolean;reason:string}|null)[],accept=predicate){let seen=0;const dom=observerDOM();Object.assign(dom.context,{HTMLButtonElement:Element});dom.main.children=[];const row=dom.main.add(new Element('TR','Worker One')),button=row.add(new Element('BUTTON'));button.setAttribute('aria-label','Restart worker');button.setAttribute('aria-describedby','reason');const reason=row.add(new Element('SPAN'));reason.id='reason';
   const browser={waitFor:async(expression:string,check:(v:Parameters<typeof predicate>[0])=>boolean,_description:string,timeout:number)=>{assert.equal(timeout,15_000);for(const state of states){seen++;row.ownText=state?'Worker One':'Other Worker';button.disabled=state?.disabled??true;reason.ownText=state?.reason??'';const value=dom.run<Parameters<typeof predicate>[0]>(expression);if(check(value))return value;}throw Error('same deadline: expected state never reached');}} as unknown as BrowserHarness;
   return {result:await waitForWorkerAction(browser,'Restart worker','Worker One',accept),seen};}
  const good={disabled:true,reason:phase==='unknown'?unknown:denied},interim={disabled:true,reason:phase==='unknown'?denied:unknown};
  const reached=await wait([interim,interim,good]);assert.equal(reached.seen,3);assert.match(reached.result.reason,oracle);
  for(const bad of [interim,{disabled:true,reason:'Unrelated error'},{disabled:true,reason:''},{disabled:false,reason:good.reason},null])await assert.rejects(wait([bad,bad]),/expected state never reached/);
  const broad=await wait([interim,good],value=>value.disabled&&value.reason.length>0);assert.equal(broad.seen,1);assert.throws(()=>assert.match(broad.result.reason,oracle),'original assertion rejects the broad-predicate mutant');
 }
 const revoked=phases.get('revoked')!;let statements:ts.Node=revoked;while(!ts.isBlock(statements)&&statements.parent)statements=statements.parent;assert.ok(ts.isBlock(statements));
 const index=statements.statements.findIndex(n=>n.getText(file).includes('const revokedAuthResponseCount'));
 const end=statements.statements.findIndex(n=>n.getText(file).includes('assert.match(revoked.reason'));assert.ok(index>=0&&end>index);
 const code='const run=async()=>{'+statements.statements.slice(index,end+1).map(n=>n.getText(file)).join('\n')+'}';
 let arrived=false,settled=false,rendered=false,polls=0;const events:string[]=[],fixture={authResponse:{}};
 const predicate=actualCallback('const accept='+revoked.arguments[3].getText(file),'accept',{}) as Parameters<typeof waitForWorkerAction>[3];
 const browser={responses:new Map([['/auth/me',2]]),waitForResponseCount:async(path:string,count:number,timeout:number)=>{assert.equal(path,'/auth/me');assert.equal(count,3);assert.equal(timeout,20_000);arrived=true;events.push('arrival');},waitForRequestHandlersIdle:async(filter:unknown)=>{assert.deepEqual(filter,{pathname:'/auth/me',method:'GET'});assert.equal(arrived,true);assert.equal(rendered,false);settled=true;events.push('settlement');},waitFor:async(_expression:string,accept:(v:Parameters<typeof predicate>[0])=>boolean,_message:string,timeout:number)=>{assert.equal(timeout,15_000);assert.ok(settled,'response count alone cannot settle the handler');const shape={disabled:true,reason:unknown} as Parameters<typeof predicate>[0];polls++;assert.equal(accept(shape),false,'idle alone cannot establish rendered denial');await Promise.resolve();rendered=true;polls++;const value={...shape,reason:denied};assert.ok(accept(value));events.push('rendered-denied');return value;}} as unknown as BrowserHarness;
 await actualCallback(code,'run',{browser,fixture,permissionUser:(permissions:string[])=>({permissions}),waitForWorkerAction,assert})();assert.equal(polls,2);assert.deepEqual(events,['arrival','settlement','rendered-denied']);
});
test('UI-WORKER-AUTH-015: actual QueryClient and submitRestart revalidate auth failure before and after the held Worker GET without replay',async t=>{
 const {createWorkerRestartController}=await import('../../src/features/workers/workers-action-controller.ts'),{mergeOperationalNodes}=await import('../../src/features/workers/workers-view.tsx');
 const normalizer=await import('../../src/features/workers/workers-wire-normalizer.ts'),descriptors=await import('../../src/features/workers/workers-action-descriptors.ts'),confirmation=await import('../../src/lib/foundation/actions/confirmation-revalidation.ts'),permissions=await import('../../src/lib/foundation/permissions/evaluator.ts'),errors=await import('../../src/lib/foundation/api-errors/adapter.ts');
 type Factory=typeof createWorkerRestartController;type Controller=ReturnType<Factory>;type Open=Extract<ReturnType<Controller['open']>,{kind:'allowed'}>;type Result=Awaited<ReturnType<Controller['submit']>>;
 type Dialog={opened:Open;state:{kind:string}}|null;type Mode='success'|'fetching'|'error-cached'|'error-empty'|'denied'|'removed'|'pending'|'initial-pending'|'malformed'|'missing-permissions'|'wildcard';type Phase='before'|'during';
 const controllerSource=readFileSync(new URL('../../src/features/workers/workers-action-controller.ts',import.meta.url),'utf8').replaceAll('\r\n','\n'),viewSource=readFileSync(new URL('../../src/features/workers/workers-view.tsx',import.meta.url),'utf8');
 const factorySource=readFileSync(new URL('../workers-pilot-fixture.mts',import.meta.url),'utf8');const wireFactory:(id:string)=>Record<string,unknown>=actualFunction(factorySource.replace('export function worker(','function worker('),'worker',{});
 const wire=wireFactory('auth-worker-015'),other=wireFactory('other-worker-015');assert.equal(Object.hasOwn(wire,'id'),false);
 const rows:unknown=Reflect.apply(mergeOperationalNodes,undefined,[[wire],[],[{...wire}]]);assert.ok(Array.isArray(rows));const row:unknown=rows[0];assert.ok(normalizer.copyCanonicalWorkerWireValue(row));assert.ok(row&&typeof row==='object');assert.equal(Object.hasOwn(row,'id'),false);
 const auth={permissions:['workers.read','service_health.read','workers.restart']},authKey=['auth','me'];
 const clientForCase=()=>{const client=new QueryClient({defaultOptions:{queries:{retry:false,staleTime:Infinity,gcTime:Infinity}}});client.setQueryData(authKey,auth);client.setQueryData(['workers'],[wire,other]);return client;};
 const open=(controller:Controller,input:unknown=row)=>Reflect.apply(controller.open,controller,[input]) as ReturnType<Controller['open']>;
 const evaluate=(controller:Controller)=>Reflect.apply(controller.evaluate,controller,[row]) as ReturnType<Controller['evaluate']>;
 const isPending=(controller:Controller)=>Reflect.apply(controller.isPending,controller,[row]) as boolean;
 async function changeAuth(client:QueryClient,mode:Mode):Promise<()=>Promise<void>>{
  if(mode==='error-cached'||mode==='error-empty'||mode==='fetching'){
   const held=Promise.withResolvers<typeof auth>(),failure=Error('AUTH_RECHECK_TEST_FAILURE');let authGets=0;
   const pending=client.fetchQuery({queryKey:authKey,queryFn:()=>{authGets++;return held.promise;},staleTime:0});void pending.catch(()=>undefined);
   assert.equal(client.getQueryState(authKey)?.fetchStatus,'fetching');assert.equal(client.getQueryState(authKey)?.status,'success');assert.equal(client.getQueryData(authKey),auth);
   if(mode==='fetching')return async()=>{held.resolve(auth);await pending;assert.equal(authGets,1);};
   if(mode==='error-empty')client.getQueryCache().find({queryKey:authKey,exact:true})!.setState({data:undefined});
   held.reject(failure);await assert.rejects(pending,error=>error===failure);assert.equal(authGets,1);
   assert.equal(client.getQueryState(authKey)?.status,'error');assert.equal(client.getQueryState(authKey)?.fetchStatus,'idle');assert.equal(client.getQueryData(authKey),mode==='error-cached'?auth:undefined);
  }else if(mode==='denied')client.setQueryData(authKey,{permissions:[]});
  else if(mode==='removed')client.removeQueries({queryKey:authKey,exact:true});
  else if(mode==='pending'||mode==='initial-pending')client.getQueryCache().find({queryKey:authKey,exact:true})!.setState({status:'pending',fetchStatus:'idle',...(mode==='initial-pending'?{data:undefined}:{})});
  else if(mode==='malformed')client.setQueryData(authKey,{permissions:['workers.restart',17]});
  else if(mode==='missing-permissions')client.setQueryData(authKey,{});
  else if(mode==='wildcard')client.setQueryData(authKey,{permissions:['*']});
  return async()=>{};
 }
 async function exercise(mode:Mode,phase:Phase,factory:Factory=createWorkerRestartController,recover=false){
  const client=clientForCase(),getsHeld:Array<ReturnType<typeof Promise.withResolvers<unknown>>>=[],events:string[]=[];let gets=0,posts=0,starts=0,invalidations=0,notice='';
  const unsubscribe=client.getQueryCache().subscribe(event=>{if(event.type==='updated'&&event.action.type==='invalidate'){assert.deepEqual(event.query.queryKey,['workers']);invalidations++;events.push('invalidate');}});
  const controller=factory({queryClient:client,fetchWorkers:async()=>{gets++;events.push('worker-get-held');const held=Promise.withResolvers<unknown>();getsHeld.push(held);return held.promise;},postRestart:async path=>{assert.equal(path,'/workers/auth-worker-015/restart');posts++;events.push('post');}});
  const opened=open(controller);assert.equal(opened.kind,'allowed');if(opened.kind!=='allowed')assert.fail('original successful authority must open');let dialog:Dialog={opened,state:{kind:'ready'}},submission:Promise<Result>|undefined;
  const observedController={...controller,submit:(selected:Open,onMutationStart?:()=>void)=>{submission=controller.submit(selected,()=>{starts++;events.push('mutation-start');onMutationStart?.();});return submission;}};
  const setRestartDialog=(update:Dialog|((current:Dialog)=>Dialog))=>{dialog=typeof update==='function'?update(dialog):update;events.push('dialog-'+(dialog?.state.kind||'closed'));};
  const callback:(selected:Open)=>void=actualCallback(viewSource,'submitRestart',{restartController:observedController,setRestartDialog,setRestartNotice:(value:string)=>{notice=value;},t:(key:string)=>key});
  let finishAuth=async()=>{};
  try{
   if(phase==='before')finishAuth=await changeAuth(client,mode);
   callback(opened);assert.ok(submission);const firstSubmission=submission;
   if(phase==='during'){
    assert.deepEqual([gets,posts,starts,invalidations],[1,0,0,0]);assert.equal(isPending(controller),true);assert.equal(open(controller).kind,'blocked');assert.equal(open(controller,other).kind,'allowed','another Worker has an independent duplicate key');
    assert.equal((await controller.submit(opened)).state.kind,'revalidation-unavailable');assert.equal(gets,1);assert.equal(isPending(controller),true);
    finishAuth=await changeAuth(client,mode);
   }
   const authStatus=client.getQueryState(authKey)?.status,authFetchStatus=client.getQueryState(authKey)?.fetchStatus,retained=client.getQueryData(authKey)===auth;
   for(const held of getsHeld)held.resolve([wire,other]);const result=await firstSubmission;await Promise.resolve();assert.equal(isPending(controller),false,'finally releases the same Worker lock');
   const finalDialog=dialog as Dialog;const observed={mode,phase,gets,posts,starts,invalidations,result:result.state.kind,outcome:result.outcome?.kind,notice,dialog:finalDialog?.state.kind??'closed',authStatus,authFetchStatus,retained,events:[...events],recovery:false};
   await finishAuth();assert.equal(posts,observed.posts,'auth settlement cannot resend a completed operation');
   if(recover&&result.state.kind==='revalidation-unavailable'){
    await client.fetchQuery({queryKey:authKey,queryFn:async()=>auth,staleTime:0});assert.equal(client.getQueryState(authKey)?.status,'success');assert.equal(posts,0);assert.equal(gets,observed.gets);
    const next=open(controller);assert.equal(next.kind,'allowed');if(next.kind!=='allowed')assert.fail('recovery permits a new explicit operation');dialog={opened:next,state:{kind:'ready'}};
    callback(next);assert.ok(submission);assert.equal(gets,observed.gets+1);assert.equal(posts,0);getsHeld.at(-1)!.resolve([wire,other]);const recovered=await submission;await Promise.resolve();
    assert.equal(recovered.outcome?.kind,'succeeded');assert.deepEqual([posts,starts,invalidations],[1,1,1]);assert.equal(notice,'workerRestartSucceeded');assert.equal(isPending(controller),false);observed.recovery=true;
   }
   return observed;
  }finally{for(const held of getsHeld)held.resolve([wire,other]);await finishAuth();unsubscribe();client.clear();}
 }
 const assertBlocked=(observed:Awaited<ReturnType<typeof exercise>>,expectedGets:number)=>{
  assert.deepEqual([observed.gets,observed.posts,observed.starts,observed.invalidations],[expectedGets,0,0,0],'fresh GET / POST / onMutationStart / invalidate');
  assert.equal(observed.result,'revalidation-unavailable');assert.equal(observed.outcome,undefined);assert.equal(observed.notice,'');assert.equal(observed.dialog,'revalidation-unavailable');
 };
 const decisive=await exercise('error-cached','during',createWorkerRestartController,true);
 t.diagnostic(JSON.stringify({finding:'B10-R10-001',phase:decisive.phase,authStatus:decisive.authStatus,authFetchStatus:decisive.authFetchStatus,dataRetained:decisive.retained,gets:decisive.gets,posts:decisive.posts,starts:decisive.starts,invalidations:decisive.invalidations,result:decisive.result}));
 assert.equal(decisive.retained,true);assertBlocked(decisive,1);assert.equal(decisive.recovery,true);
 for(const phase of ['before','during'] as const)for(const mode of ['fetching','error-cached','error-empty','denied','removed','pending','initial-pending','malformed','missing-permissions'] as const){
  const observed=await exercise(mode,phase,createWorkerRestartController,mode==='error-cached');assertBlocked(observed,phase==='before'?0:1);if(mode==='error-cached')assert.equal(observed.recovery,true);
 }
 for(const phase of ['before','during'] as const){const success=await exercise('success',phase);assert.deepEqual([success.gets,success.posts,success.starts,success.invalidations],[1,1,1,1]);assert.equal(success.outcome,'succeeded');assert.equal(success.notice,'workerRestartSucceeded');assert.equal(success.dialog,'closed');assert.ok(success.events.indexOf('mutation-start')<success.events.indexOf('post'));}
 const copyStringArray=actualFunction(controllerSource,'copyStringArray',{}),snapshot:(client:QueryClient)=>{kind:string;permissions?:readonly string[]}=actualFunction(controllerSource,'permissionSnapshot',{authQueryKey:authKey,copyStringArray});
 for(const mode of ['success','wildcard','denied','fetching','error-cached','error-empty','removed','pending','initial-pending','malformed','missing-permissions'] as const){
  const client=clientForCase();let gets=0,posts=0;const controller=createWorkerRestartController({queryClient:client,fetchWorkers:async()=>{gets++;return [wire];},postRestart:async()=>{posts++;}}),finish=await changeAuth(client,mode);
  try{const current=snapshot(client);assert.equal(current.kind,['success','wildcard','denied'].includes(mode)?'ready':mode==='fetching'?'refreshing':'unavailable');
   const evaluation=evaluate(controller),opened=open(controller);if(mode==='success'||mode==='wildcard'){assert.equal(evaluation.availability.kind,'allowed');assert.equal(opened.kind,'allowed');}
   else{assert.equal(evaluation.availability.kind,mode==='denied'?'denied':'unknown');assert.equal(opened.kind,'blocked');}assert.deepEqual([gets,posts],[0,0]);
   if(mode==='denied')assert.deepEqual(current.permissions,[]);
   if(mode==='success'){for(const value of [undefined,null,{},['workers.restart','workers.restart'],['workers.restart',17]]){client.setQueryData(authKey,{permissions:value});assert.equal(snapshot(client).kind,'unavailable');assert.equal(open(controller).kind,'blocked');}}
  }finally{await finish();client.clear();}
 }
 // Compile only original function bodies and original dependencies. Mutation
 // controls execute the same submitRestart caller and held-GET sequence above.
 function fromSource(source:string):Factory{
  const bindings:Record<string,unknown>={...normalizer,...descriptors,...confirmation,...permissions,...errors,apiGet:()=>assert.fail('test must inject GET'),apiPost:()=>assert.fail('test must inject POST')};
  for(const name of ['authQueryKey','workersQueryKey','readyState','staleBlockedState','revalidationUnavailableState','invalidWorkerTarget'])bindings[name]=actualCallback(source,name,bindings);
  for(const name of ['copyStringArray','permissionSnapshot','findWorker','cachedWorker','workerRemoteState','freshWorkerRemoteState','fingerprintEvidence','blockedOpen','refreshWorkersAfterConflict','isAmbiguousMutationError'])bindings[name]=actualFunction(source,name,bindings);
  return actualFunction(source.replace('export function createWorkerRestartController(','function createWorkerRestartController('),'createWorkerRestartController',bindings);
 }
 assertBlocked(await exercise('error-cached','during',fromSource(controllerSource)),1);assert.equal((await exercise('success','during',fromSource(controllerSource))).posts,1);
 const guard='  if (state?.status !== "success") return Object.freeze({ kind: "unavailable" });\n';assert.ok(controllerSource.includes(guard),'mutation reaches actual success prerequisite');
 const removed=controllerSource.replace(guard,''),cacheFirst=controllerSource.replace(guard,'  if (state?.status !== "success" && !(state?.status === "error" && queryClient.getQueryData(authQueryKey))) return Object.freeze({ kind: "unavailable" });\n');
 const ast=ts.createSourceFile('controller.ts',controllerSource,ts.ScriptTarget.Latest,true,ts.ScriptKind.TS);let afterGet='';
 const visit=(node:ts.Node)=>{if(ts.isVariableStatement(node)&&node.declarationList.declarations.some(d=>ts.isIdentifier(d.name)&&d.name.text==='currentEvaluation'))afterGet=node.getText(ast);ts.forEachChild(node,visit);};visit(ast);assert.ok(afterGet.includes('permissionSnapshot(queryClient)'));
 const noRecheck=controllerSource.replace(afterGet,'const currentEvaluation = authEvaluation;');
 for(const [name,source,phases] of [['removed-status',removed,['before','during']],['cached-error-ready',cacheFirst,['before','during']],['missing-after-get-check',noRecheck,['during']]] as const){
  assert.notEqual(source,controllerSource);for(const phase of phases){const unsafe=await exercise('error-cached',phase,fromSource(source));assert.equal(unsafe.posts,1,name+' reaches the real injected POST, not an unrelated failure');assert.throws(()=>assertBlocked(unsafe,phase==='before'?0:1),assert.AssertionError);}
 }
});
test('UI-WORKER-WIRE-014: service-id-only factory values survive actual merge and restart authority without nodes or synthetic id',async()=>{
 const {mergeOperationalNodes}=await import('../../src/features/workers/workers-view.tsx'),{copyCanonicalWorkerWireValue}=await import('../../src/features/workers/workers-wire-normalizer.ts'),{createWorkerRestartController}=await import('../../src/features/workers/workers-action-controller.ts');
 const factorySource=readFileSync(new URL('../workers-pilot-fixture.mts',import.meta.url),'utf8');
 const factory:(id:string,name?:string,type?:string)=>Record<string,unknown>=actualFunction(factorySource.replace('export function worker(', 'function worker('),'worker',{});
 const wire=factory('worker-014','Wire Worker');assert.equal(Object.hasOwn(wire,'id'),false);assert.ok(copyCanonicalWorkerWireValue(wire));
 const merge=(...sources:unknown[][]):unknown[]=>{const value:unknown=Reflect.apply(mergeOperationalNodes,undefined,sources);assert.ok(Array.isArray(value));return value;};
 for(const parts of [[[wire]],[[wire],[],[{...wire}]],[[{...wire,id:'worker-014'}],[wire]],[[wire],[{...wire,id:'worker-014'}]],[[{...wire,id:'worker-014'}],[{...wire,id:'worker-014'}]],[[wire],[{...wire,id:'worker-014'}],[wire]]]){
  const original=parts.map(list=>list.map(row=>Object.getOwnPropertyDescriptors(row))),rows=merge(...parts);assert.equal(rows.length,1);const row=rows[0];assert.ok(row&&typeof row==='object');
  assert.equal(Object.hasOwn(row,'id'),parts.some(list=>list.some(node=>Object.hasOwn(node,'id'))));assert.deepEqual(copyCanonicalWorkerWireValue(row),copyCanonicalWorkerWireValue(wire));
  for(const key of ['reported_version','reported_commit','reported_build_date'])assert.equal(Object.hasOwn(row,key),false);
  assert.deepEqual(parts.map(list=>list.map(node=>Object.getOwnPropertyDescriptors(node))),original,'merge must not mutate either wire');
  const client=new QueryClient({defaultOptions:{queries:{retry:false}}});client.setQueryData(['auth','me'],{permissions:['workers.read','service_health.read','workers.restart']});client.setQueryData(['workers'],[wire]);client.setQueryData(['service-health'],[wire]);
  assert.equal(client.getQueryData(['nodes']),undefined,'two real read owners without nodes');let gets=0,posts=0;const held=Promise.withResolvers<unknown>();
  const controller=createWorkerRestartController({queryClient:client,fetchWorkers:async()=>{gets++;return held.promise;},postRestart:async()=>{posts++;}});
  const open=(input:unknown)=>Reflect.apply(controller.open,controller,[input]) as ReturnType<typeof controller.open>;
  for(const input of [wire,row]){const evaluation=Reflect.apply(controller.evaluate,controller,[input]) as ReturnType<typeof controller.evaluate>;assert.equal(evaluation.availability.kind,'allowed');assert.equal(open(input).kind,'allowed');}
  assert.deepEqual([gets,posts],[0,0]);const opened=open(row);assert.equal(opened.kind,'allowed');if(opened.kind!=='allowed')assert.fail('actual normal wire must open');
  const submission=controller.submit(opened);assert.equal(open(row).kind,'blocked');await controller.submit(opened);assert.deepEqual([gets,posts],[1,0]);held.resolve([wire]);assert.equal((await submission).outcome?.kind,'succeeded');assert.equal(posts,1);
  client.setQueryData(['auth','me'],{permissions:[]});assert.equal(open(row).kind,'blocked');client.setQueryData(['auth','me'],{permissions:['workers.restart']});client.getQueryCache().find({queryKey:['workers'],exact:true})!.setState({fetchStatus:'fetching'});assert.equal(open(row).kind,'blocked');client.clear();
 }
 const invalidClient=new QueryClient();invalidClient.setQueryData(['auth','me'],{permissions:['workers.restart']});invalidClient.setQueryData(['workers'],[wire]);let invalidGets=0,invalidPosts=0;
 const invalidController=createWorkerRestartController({queryClient:invalidClient,fetchWorkers:async()=>{invalidGets++;return [wire];},postRestart:async()=>{invalidPosts++;}});
 const assertInvalid=(row:unknown)=>{assert.equal(copyCanonicalWorkerWireValue(row),undefined);const evaluation=Reflect.apply(invalidController.evaluate,invalidController,[row]) as ReturnType<typeof invalidController.evaluate>,opened=Reflect.apply(invalidController.open,invalidController,[row]) as ReturnType<typeof invalidController.open>;assert.notEqual(evaluation.availability.kind,'allowed');assert.equal(opened.kind,'blocked');};
 let getterCalls=0;const accessor=Object.defineProperty({...wire},'id',{enumerable:true,get(){getterCalls++;throw Error('getter must not run');}});
 for(const bad of [undefined,null,'',17,'other'])for(const position of [0,1]){
  const invalid={...wire,id:bad},parts:unknown[][]=[[wire],[wire]];parts[position]=[invalid];assertInvalid(invalid);assertInvalid(merge(...parts)[0]);
 }
 for(const parts of [[[accessor],[wire]],[[wire],[accessor]],[[accessor]]])assertInvalid(merge(...parts)[0]);assert.equal(getterCalls,0);assert.deepEqual([invalidGets,invalidPosts],[0,0]);invalidClient.clear();
 const different=factory('other-014','Other Worker'),encoder=factory('encoder-014','Encoder','encoder_recorder');assert.deepEqual(merge([wire,different,encoder],[{...wire,service_name:'Later'}]).map(row=>copyCanonicalWorkerWireValue(row)?.service_id),['encoder-014','other-014','worker-014']);
 const differentType={...wire,service_type:'encoder_recorder'};assert.equal(copyCanonicalWorkerWireValue(merge([wire],[differentType])[0])?.service_type,'worker');
 const metadata=merge([{...wire,reported_version:'1',reported_commit:'before'}],[{...wire,reported_version:'2',reported_build_date:'after'}])[0];assert.ok(metadata&&typeof metadata==='object'&&'reported_version' in metadata&&'reported_commit' in metadata&&'reported_build_date' in metadata);assert.deepEqual([metadata.reported_version,metadata.reported_commit,metadata.reported_build_date],['2','before','after']);
});
test('UI-WORKER-REFRESH-014: actual view and QueryClient keep permitted rows through auth refetch, preserve remote state and reject dangerous actions',async()=>{
 const {WorkersView,mergeOperationalNodes}=await import('../../src/features/workers/workers-view.tsx'),{createWorkerRestartController}=await import('../../src/features/workers/workers-action-controller.ts'),{hasPermission}=await import('../../src/lib/auth/permissions.ts');
 const reads=['workers.read','api_tokens.create','service_health.read'],keys=[['workers'],['nodes'],['service-health']],names=['Retained Worker','Retained Node','Retained Health'];
 const wires=names.map((service_name,index)=>({service_id:'retained-'+index,service_name,service_type:'worker',status:'online',health_status:'healthy',metrics:{active_jobs:3},...(index===1?{id:'retained-'+index}:{})}));
 const visible=(html:string)=>html.replace(/<[^>]*>/g,' '),stateHTML=(html:string)=>html.match(/<div[^>]*data-remote-consumer="workers"[\s\S]*?<\/div>/)?.[0]||'';
 for(const locale of ['ja','en'] as const)for(const mask of [1,0,2,3,4,5,6,7]){
  const client=new QueryClient({defaultOptions:{queries:{staleTime:Infinity,gcTime:Infinity,retry:false,retryOnMount:false}}});client.mount();const auth={permissions:[...reads.filter((_,i)=>mask&(1<<i)),'workers.restart']};client.setQueryData(['auth','me'],auth);keys.forEach((key,index)=>client.setQueryData(key,[wires[index]]));
  try {
  const render=()=>renderUI(createElement(QueryClientProvider,{client},createElement(WorkersView)),locale,'/admin/workers/');
  const assertRows=(html:string,allowed:number)=>{for(let index=0;index<3;index++)assert.equal(visible(html).includes(names[index]),!!(allowed&(1<<index)),'only this snapshot permits each real source');};
  const idle=render();assertRows(idle,mask);const original=client.getQueryData(['auth','me']);const held=Promise.withResolvers<typeof auth>();let authGets=0;
  const refreshing=client.fetchQuery({queryKey:['auth','me'],queryFn:()=>{authGets++;return held.promise;},staleTime:0});void refreshing.catch(()=>undefined);assert.equal(client.getQueryState(['auth','me'])?.fetchStatus,'fetching');assert.equal(client.getQueryState(['auth','me'])?.status,'success');assert.equal(client.getQueryData(['auth','me']),original);
  const during=render();assertRows(during,mask);if(mask){assert.match(during,locale==='ja'?/閲覧権限を再確認中/:/Rechecking Worker read permissions/);assert.doesNotMatch(during,/Worker情報は最新|worker data is current/);}else assert.match(during,/Permission denied|権限がありません/);
  let gets=0,posts=0;const controller=createWorkerRestartController({queryClient:client,fetchWorkers:async()=>{gets++;return [wires[0]];},postRestart:async()=>{posts++;}});
  const evaluate=()=>Reflect.apply(controller.evaluate,controller,[wires[0]]) as ReturnType<typeof controller.evaluate>,open=()=>Reflect.apply(controller.open,controller,[wires[0]]) as ReturnType<typeof controller.open>;
  assert.notEqual(evaluate().availability.kind,'allowed');assert.equal(open().kind,'blocked');assert.deepEqual([gets,posts],[0,0]);
  held.resolve(auth);await refreshing;assert.equal(authGets,1);assert.equal(client.getQueryState(['auth','me'])?.fetchStatus,'idle');const after=render();assertRows(after,mask);assert.doesNotMatch(after,/Rechecking Worker read permissions|閲覧権限を再確認中/);
  const opened=open();assert.equal(opened.kind,'allowed');if(opened.kind!=='allowed')assert.fail('idle controller authority must open');
  const recheck=Promise.withResolvers<typeof auth>(),again=client.fetchQuery({queryKey:['auth','me'],queryFn:()=>recheck.promise,staleTime:0});void again.catch(()=>undefined);assert.equal((await controller.submit(opened)).state.kind,'revalidation-unavailable');assert.deepEqual([gets,posts],[0,0]);
  const reduced=mask&(mask-1);recheck.resolve({permissions:reads.filter((_,i)=>reduced&(1<<i))});await again;assertRows(render(),reduced);assert.equal(open().kind,'blocked');
  client.setQueryData(['auth','me'],{permissions:[]});assertRows(render(),0);assert.equal(open().kind,'blocked');assert.deepEqual(client.getQueryData(['workers']),[wires[0]],'retained source exists but cannot grant read access');
  } finally {client.unmount();client.clear();}assert.equal(client.getQueryCache().getAll().length,0);
 }
 for(const locale of ['ja','en'] as const)for(const state of ['partial','stale','background-refresh']){
  const client=new QueryClient({defaultOptions:{queries:{staleTime:Infinity,retry:false,retryOnMount:false,gcTime:Infinity}}});const auth={permissions:[...reads,'workers.restart']};client.setQueryData(['auth','me'],auth);keys.forEach((key,index)=>client.setQueryData(key,[wires[index]]));
  const key=state==='partial'?keys[1]:keys[0];client.getQueryCache().find({queryKey:key,exact:true})!.setState(state==='background-refresh'?{fetchStatus:'fetching'}:{status:'error',error:Error('PRIVATE_RESOURCE_ERROR'),...(state==='partial'?{data:undefined}:{})});
  const render=()=>renderUI(createElement(QueryClientProvider,{client},createElement(WorkersView)),locale,'/admin/workers/');const before=render(),notice=stateHTML(before);assert.ok(notice,'real source state notice required');
  const held=Promise.withResolvers<typeof auth>(),pending=client.fetchQuery({queryKey:['auth','me'],queryFn:()=>held.promise,staleTime:0});const during=render();assert.equal(stateHTML(during),notice,'auth refresh must not suppress partial/stale/resource refresh');assert.match(during,/Retained Worker/);assert.doesNotMatch(during,/PRIVATE_RESOURCE_ERROR/);held.resolve(auth);await pending;client.clear();
 }
 for(const locale of ['ja','en'] as const)for(const mode of ['initial','error-empty','error-cached','success-missing','session-ended']){
  const html=renderUI(createElement(WorkersView),locale,'/admin/workers/',client=>{const auth=client.getQueryCache().find({queryKey:['auth','me'],exact:true});assert.ok(auth);if(mode==='session-ended')client.removeQueries({queryKey:['auth','me'],exact:true});else auth.setState({status:mode==='initial'?'pending':mode==='success-missing'?'success':'error',fetchStatus:mode==='initial'?'fetching':'idle',...(mode==='error-cached'?{}:{data:undefined}),error:mode.startsWith('error')?Error('PRIVATE_AUTH_ERROR'):null});});
  assert.doesNotMatch(html,/Worker One|PRIVATE_AUTH_ERROR|Permission denied: no Worker|権限がありません/);assert.equal((html.match(/>—</g)||[]).length,3);
 }
 for(const locale of ['ja','en'] as const){
  const client=new QueryClient({defaultOptions:{queries:{retry:false,staleTime:Infinity}}}),auth={permissions:reads};client.setQueryData(['auth','me'],auth);keys.forEach((key,index)=>client.setQueryData(key,[wires[index]]));
  const failure=Error('PRIVATE_AUTH_RECHECK'),held=Promise.withResolvers<typeof auth>(),pending=client.fetchQuery({queryKey:['auth','me'],queryFn:()=>held.promise,staleTime:0});held.reject(failure);await assert.rejects(pending,error=>error===failure);
  assert.equal(client.getQueryState(['auth','me'])?.status,'error');assert.deepEqual(client.getQueryData(['auth','me']),auth);const html=renderUI(createElement(QueryClientProvider,{client},createElement(WorkersView)),locale,'/admin/workers/');
  for(const name of names)assert.equal(visible(html).includes(name),false);assert.match(html,/role="alert"/);assert.doesNotMatch(html,/PRIVATE_AUTH_RECHECK|Permission denied|権限がありません/);assert.equal((html.match(/>—</g)||[]).length,3);client.clear();
 }
 // Mutate the actual view declarations and the existing private authority gate
 // in memory; the production source, queries and controllers stay unchanged.
 const source=readFileSync(new URL('../../src/features/workers/workers-view.tsx',import.meta.url),'utf8'),ast=ts.createSourceFile('workers-view.tsx',source,ts.ScriptTarget.Latest,true,ts.ScriptKind.TSX);
 const selected=new Set(['canReadWorkers','canReadRegisteredNodes','canReadServiceHealth','canReadAny','readStatus','readRefreshing','rows']),declarations:string[]=[];
 const view=ast.statements.find((node):node is ts.FunctionDeclaration=>ts.isFunctionDeclaration(node)&&node.name?.text==='WorkersView');assert.ok(view?.body);
 for(const statement of view.body.statements)if(ts.isVariableStatement(statement))for(const declaration of statement.declarationList.declarations)if(ts.isIdentifier(declaration.name)&&selected.has(declaration.name.text))declarations.push('const '+declaration.getText(ast)+';');assert.equal(declarations.length,selected.size);
 const projection='const project=()=>{'+declarations.join('\n')+'return rows;}';
 const client=new QueryClient({defaultOptions:{queries:{retry:false,staleTime:Infinity}}}),auth={permissions:[...reads,'workers.restart']};client.setQueryData(['auth','me'],auth);keys.forEach((key,index)=>client.setQueryData(key,[wires[index]]));
 const project=(text=projection):unknown[]=>{const state=client.getQueryState(['auth','me']);const callback:()=>unknown[]=actualCallback(text,'project',{hasPermission,mergeOperationalNodes,currentUser:{...state,data:client.getQueryData(['auth','me']),isFetching:state?.fetchStatus==='fetching'},workers:{data:client.getQueryData(keys[0])},registeredNodes:{data:client.getQueryData(keys[1])},serviceHealth:{data:client.getQueryData(keys[2])}});return callback();};
 const assertRows=(rows:unknown[],count:number)=>assert.equal(rows.length,count);assertRows(project(),3);
 const held=Promise.withResolvers<typeof auth>(),pending=client.fetchQuery({queryKey:['auth','me'],queryFn:()=>held.promise,staleTime:0});assertRows(project(),3);
 const clearsRows=projection.replace('readStatus === "ready" ? mergeOperationalNodes','readStatus === "ready" && !currentUser.isFetching ? mergeOperationalNodes');assert.notEqual(clearsRows,projection);assert.throws(()=>assertRows(project(clearsRows),3),assert.AssertionError);
 const controllerSource=readFileSync(new URL('../../src/features/workers/workers-action-controller.ts',import.meta.url),'utf8'),copyStringArray=actualFunction(controllerSource,'copyStringArray',{});
 const permission=(text:string)=>{const callback:(queryClient:QueryClient)=>{kind:string}=actualFunction(text,'permissionSnapshot',{copyStringArray,authQueryKey:['auth','me']});return callback(client);};
 assert.equal(permission(controllerSource).kind,'refreshing');const weakGate=controllerSource.replace('if (state?.fetchStatus === "fetching")','if (false)');assert.notEqual(weakGate,controllerSource);assert.throws(()=>assert.equal(permission(weakGate).kind,'refreshing'),assert.AssertionError);
 held.resolve(auth);await pending;client.setQueryData(['auth','me'],{permissions:[]});assertRows(project(),0);
 const bypass=projection.replace(/const rows = [^;]+;/,'const rows = mergeOperationalNodes(workers.data || [], registeredNodes.data || [], serviceHealth.data || []);');assert.notEqual(bypass,projection);assert.throws(()=>assertRows(project(bypass),0),assert.AssertionError);
 client.setQueryData(['auth','me'],auth);client.getQueryCache().find({queryKey:['auth','me'],exact:true})!.setState({status:'error',error:Error('PRIVATE_AUTH')});assertRows(project(),0);assert.throws(()=>assertRows(project(bypass),0),assert.AssertionError);client.clear();
});
test('UI-WORKER-MERGE-013: actual merge, canonical normalizer and restart controller keep omission, priority and fail-closed inputs',async()=>{
 const {mergeOperationalNodes}=await import('../../src/features/workers/workers-view.tsx'),{copyCanonicalWorkerWireValue}=await import('../../src/features/workers/workers-wire-normalizer.ts'),{createWorkerRestartController}=await import('../../src/features/workers/workers-action-controller.ts');
 const worker:WorkerNode={id:'worker-one',service_id:'worker-one',service_type:'worker',service_name:'Worker One',status:'healthy'};
 for(const [before,next,expected] of [[{}, {}, undefined],[{reported_version:'1'}, {},'1'],[{}, {reported_version:'2'},'2'],[{reported_version:'1'},{reported_version:'2'},'2'],[{reported_version:'1'},{reported_version:''},'1']] as const){
   const source={...worker,...before},result=mergeOperationalNodes([source],[{...worker,...next}])[0];assert.equal(result.reported_version,expected);
   for(const key of ['reported_commit','reported_build_date','health_status'])assert.equal(Object.hasOwn(result,key),false);assert.equal(Object.hasOwn(result,'reported_version'),expected!==undefined);
   assert.ok(copyCanonicalWorkerWireValue(result));const client=new QueryClient();client.setQueryData(['auth','me'],{permissions:['workers.restart']});client.setQueryData(['workers'],[result]);
   let posts=0,fetches=0,release!:()=>void;const held=new Promise<void>(resolve=>{release=resolve;});
   const controller=createWorkerRestartController({queryClient:client,fetchWorkers:async()=>{fetches++;await held;return [result];},postRestart:async()=>{posts++;}});
   assert.equal(controller.evaluate(result).availability.kind,'allowed');const open=controller.open(result);assert.equal(open.kind,'allowed');assert.equal(posts,0);assert.equal(fetches,0);
   if(open.kind==='allowed'){const submission=controller.submit(open);assert.equal(controller.isPending(result),true);assert.equal(controller.open(result).kind,'blocked');await controller.submit(open);assert.equal(fetches,1);release();assert.equal((await submission).outcome?.kind,'succeeded');assert.equal(posts,1);}
   client.setQueryData(['auth','me'],{permissions:[]});assert.equal(controller.open(result).kind,'blocked');client.setQueryData(['auth','me'],{permissions:['workers.restart']});
   client.getQueryCache().find({queryKey:['workers'],exact:true})!.setState({fetchStatus:'fetching'});assert.equal(controller.open(result).kind,'blocked');client.clear();
 }
 for(const key of ['reported_version','health_status'])for(const value of [undefined,null,17,{},['bad']])for(const position of [0,1]){
   const invalid={...worker,[key]:value} as unknown as WorkerNode,parts:WorkerNode[][]=[[worker],[worker]];parts[position]=[invalid];
   assert.equal(copyCanonicalWorkerWireValue(mergeOperationalNodes(...parts)[0]),undefined);
 }
 let getterCalls=0;const accessor=Object.defineProperty({...worker},'reported_version',{enumerable:true,get(){getterCalls++;return '1';}});
 for(const invalid of [accessor,{...worker,id:'different'}]){const result=mergeOperationalNodes([worker],[invalid])[0];assert.equal(copyCanonicalWorkerWireValue(result),undefined);}
 assert.equal(getterCalls,0);const meta=mergeOperationalNodes([{...worker,reported_commit:'before',reported_build_date:'before'}],[{...worker,reported_commit:'after'}])[0];assert.equal(meta.reported_commit,'after');assert.equal(meta.reported_build_date,'before');
 const rows=mergeOperationalNodes([worker,{...worker,id:'encoder',service_id:'encoder',service_type:'encoder_recorder'}],[{...worker,service_name:'Later'}]);assert.deepEqual(rows.map(row=>row.service_id),['encoder','worker-one']);assert.equal(rows[1].service_name,'Worker One');assert.equal(rows[1].service_type,'worker');
});
test('UI-WORKER-READ-013: real view distinguishes all read combinations, denied caches, auth loading/error and eight denied IDs',async()=>{
 const {WorkersView}=await import('../../src/features/workers/workers-view.tsx');
 const keys=['workers.read','api_tokens.create','service_health.read'];
 for(const locale of ['ja','en'] as const)for(let mask=0;mask<8;mask++){
   const html=renderUI(createElement(WorkersView),locale,'/admin/workers/',client=>client.setQueryData(['auth','me'],{permissions:keys.filter((_,i)=>mask&(1<<i))}));
   if(mask===0){assert.match(html,locale==='ja'?/権限がありません/:/Permission denied/);assert.doesNotMatch(html,/Worker One|Worker情報は最新|Worker information is up to date/);assert.equal((html.match(/>—</g)||[]).length,3);assert.match(html,/<button[^>]*disabled=""[^>]*>[\s\S]*?(更新|Refresh)/);}
   else {assert.match(html,/Worker One/);assert.doesNotMatch(html,/Permission denied: no Worker|Worker情報を閲覧する権限がありません/);}
 }
 for(const status of ['pending','error'] as const)for(const locale of ['ja','en'] as const){
   const html=renderUI(createElement(WorkersView),locale,'/admin/workers/',client=>{client.getQueryCache().find({queryKey:['auth','me'],exact:true})!.setState({data:undefined,status,fetchStatus:status==='pending'?'fetching':'idle',error:status==='error'?Error('PRIVATE_AUTH'):null});});
   assert.doesNotMatch(html,/権限がありません|Permission denied|PRIVATE_AUTH|Worker One/);assert.match(html,status==='error'?/role="alert"/:/Checking Worker|閲覧権限を確認中/);
 }
 const denied=conditions.filter(c=>c.family==='workers'&&c.state==='permission-denied');assert.equal(denied.length,8);
 for(const condition of denied){const current=fixtureFor('workers','permission-denied');const me=current.get('/auth/me').body;const html=renderUI(createElement(WorkersView),condition.locale as 'ja'|'en',condition.route,client=>client.setQueryData(['auth','me'],me));const dom=observerDOM();dom.main.ownText=html.replace(/<[^>]*>/g,' ');assertState(condition,dom.run(observationExpression),current.evidence,'/workers');assert.equal(current.evidence.requests.get('/workers')||0,0);}
});
test('UI-REFRESH-013: actual scoped driver selects the page action or registered CardHeader, never Updated sort',async()=>{
 const {NodeRegistrationView}=await import('../../src/features/nodes/node-registration-view.tsx');
 const source=readFileSync(new URL('../../src/features/nodes/node-registration-view.tsx',import.meta.url),'utf8');
 const callback=actualCallback('const action='+source.match(/onClick=\{(\(\) => registeredNodes\.refetch\(\))\}/)![1],'action',{registeredNodes:{refetch(){return 'actual-refetch';}}});assert.equal(callback(),'actual-refetch');
 for(const family of ['streams-list','nodes'])for(const locale of ['ja','en'] as const)for(const fault of ['none','hidden','disabled','duplicate','wrong-owner']){
   if(family==='nodes'){const html=renderUI(createElement(NodeRegistrationView,{mode:'registered'}),locale,'/admin/registered-nodes/');assert.match(html,locale==='ja'?/更新<\/button>/:/Refresh<\/button>/);}
   const current=fixtureFor(family,'stale'),dom=observerDOM();dom.main.children=[];current.get(current.surface.primary);current.fixture.refresh();
   const page=dom.main.add(new Element('DIV'));page.setAttribute('data-screen-family','registered-nodes');const owner=page.add(new Element('DIV'));owner.setAttribute('data-slot',fault==='wrong-owner'?'other':family==='streams-list'?'page-actions-secondary':'card-header');
   const button=owner.add(new Element('BUTTON',locale==='ja'?'更新':'Refresh'));button.hidden=fault==='hidden';button.disabled=fault==='disabled';
   page.add(new Element('BUTTON',locale==='ja'?'更新':'Updated'));page.add(new Element('BUTTON',locale==='ja'?'更新':'Updated'));if(fault==='duplicate')owner.add(new Element('BUTTON',button.ownText));dom.document.elementFromPoint=()=>button;
   let clicks=0;const browser={evaluate:async(e:string)=>dom.run(e),waitFor:async(e:string,p:(v:unknown)=>boolean)=>assert.ok(p(dom.run(e))),clickAt:async()=>{clicks++;current.get(current.surface.primary);}} as unknown as BrowserHarness;
   if(fault==='none'){await refreshCurrentState(browser,{...current.condition,locale});assert.equal(clicks,1);assert.deepEqual(current.evidence.responseStatuses.get(current.surface.primary),[200,503]);}else{await assert.rejects(refreshCurrentState(browser,{...current.condition,locale}));assert.equal(clicks,0);}assert.deepEqual(current.fixture.unexpected,[]);current.fixture.release();
 }
});
function fixtureFor(family:string,state:string,exercise?:string) {
 const surface=inventory.surfaces.find(s=>s.id===family)!;assert.ok(surface);
 const condition={...base,family,state,exercise} as Condition;
 const fixture=createUIFixture("http://ui.test");fixture.reset(condition,surface.primary);
 const evidence:RequestEvidence={requests:new Map(),responses:new Map(),responseStatuses:new Map()};
 function get(path:string) {const response=fixture.resolver({method:"GET",url:"http://ui.test"+path})!;assert.ok(response);
  evidence.requests.set(path,(evidence.requests.get(path)||0)+1);
  if(!response.waitUntil) {evidence.responses.set(path,(evidence.responses.get(path)||0)+1);evidence.responseStatuses.set(path,[...(evidence.responseStatuses.get(path)||[]),response.status??200]);}
  return response;
 }
 return {surface,condition,fixture,evidence,get};
}
test("UI-STATE-001: actual resolver keeps partial successes and rejects all-failed substitution",()=>{
 const current=fixtureFor("monitoring","partial");
 for(const path of [...statePaths(current.condition,current.surface.primary).success,...statePaths(current.condition,current.surface.primary).failure])current.get(path);
 const dom=observerDOM();dom.main.ownText="Worker One. Some sections are unavailable.";
 const observed=dom.run<UIObservation>(observationExpression);
 assertState(current.condition,observed,current.evidence,current.surface.primary);
 current.evidence.responseStatuses.set("/service-health",[503]);
 assert.throws(()=>assertState(current.condition,observed,current.evidence,current.surface.primary),/successful section/);
 current.evidence.responseStatuses.set("/service-health",[200]);dom.main.ownText="Some sections are unavailable.";
 assert.throws(()=>assertState(current.condition,dom.run(observationExpression),current.evidence,current.surface.primary),/remain visible/);
});
test("UI-STATE-002: stale and refresh come from an initial 200 followed by a real changed resolver phase",()=>{
 for(const state of ["stale","background-refresh"]) {
  const current=fixtureFor("streams-list",state);const first=current.get("/streams");assert.equal(first.status??200,200);
  current.fixture.refresh();const second=current.get("/streams");assert.equal(second.status??200,state==="stale"?503:200);
  const dom=observerDOM();dom.main.ownText="B9 Browser Stream. "+(state==="stale"?"Refresh failed":"Refreshing");
  assertState(current.condition,dom.run(observationExpression),current.evidence,"/streams");
  current.evidence.responseStatuses.set("/streams",[503]);current.evidence.responses.set("/streams",2);
  assert.throws(()=>assertState(current.condition,dom.run(observationExpression),current.evidence,"/streams"));current.fixture.release();
 }
});
test("UI-STATE-003: every applicable empty driver preserves the collection shape and unknown affects consumed fields",()=>{
 for(const surface of inventory.surfaces.filter(s=>s.states.empty.applicable)) {
  const current=fixtureFor(surface.id,"empty");assert.deepEqual(current.get(surface.primary).body,[],surface.id);
 }
 for(const family of ["streams-list","workers","metrics","audit-logs","system-updates"]) {
  const current=fixtureFor(family,"unknown");const body=current.get(current.surface.primary).body;
  assert.match(JSON.stringify(body),/future_state|future_result/);assert.notEqual(body,undefined);
 }
});
test("UI-STATE-004: denied fixture role and per-query request contract remain distinct",()=>{
 for(const surface of inventory.surfaces.filter(s=>s.states["permission-denied"].applicable)) {
  const current=fixtureFor(surface.id,"permission-denied");const me=current.get("/auth/me").body as {user:{roles:string[]};permissions:string[]};
  assert.deepEqual(me.user.roles,["viewer"]);assert.deepEqual(me.permissions,[]);
  const count=deniedPrimaryRequests[surface.id]||0;for(let i=0;i<count;i++)current.get(surface.primary);
  const dom=observerDOM();dom.main.ownText="Permission denied";
  assertState(current.condition,dom.run(observationExpression),current.evidence,surface.primary);
  current.evidence.requests.set(surface.primary,count===0?1:0);
  assert.throws(()=>assertState(current.condition,dom.run(observationExpression),current.evidence,surface.primary),/query contract/);
 }
});
test("UI-CONTENT-001: real fixture values reach actual resource cells; metric code and linked IDs stay coherent",()=>{
 const metric={name:"node.cpu.used_percent",service_id:"worker-one",value:24};
 const changed=contentInput(metric,"long-id","/observability/metrics") as typeof metric;
 assert.equal(changed.name,metric.name);assert.match(changed.service_id,/UI-LONG-ID/);
 assert.equal(canonicalFixturePath('/nodes/'+changed.service_id),'/nodes/worker-one');
 for(const family of ["diagnostics","remediation","notifications"] as const)for(const exercise of ["long-id","long-text"]) {
  const current=fixtureFor(family,"ready",exercise);const resource=resourcePages[family].resources[0];
  const rows=(current.get(resource.path).body as Record<string,unknown>[]).map(row=>enrichResourceRow(resource,row));
  const html=renderUI(createElement(ResourceTable,{resource,rows,columns:visibleColumns(rows,resource),canEdit:false,canDelete:false,canTest:false,currentUser:undefined}),"en");
  assertContentReached(current.condition,html,[]);
  assert.throws(()=>assertContentReached(current.condition,"unmodified fixture",[]),/did not reach/);
  assert.ok(html.includes(exercise==="long-id"?longIdentifier:longText));
 }
});

test("UI-STATE-VIEW-001: actual Metrics retains old rows with failure/partial state and future metric status reaches unknown",async()=>{
  const {MetricsView}=await import("../../src/features/metrics/metrics-view.tsx");
  const {APIError}=await import("../../src/lib/api/client.ts");
  for(const state of ["stale","partial","unknown"]) {
    const current=fixtureFor("metrics",state);
    current.get(current.surface.primary);
    if(state==="stale") {current.fixture.refresh();current.get(current.surface.primary);}
    if(state==="partial")current.get("/service-health");
    const html=renderUI(createElement(MetricsView),"en","/admin/metrics/",client=>{
      const key=state==="partial"?["service-health"]:["observability","metrics",10800];
      const query=client.getQueryCache().find({queryKey:key,exact:true});assert.ok(query);
      if(state==="unknown")query.setData(current.get(current.surface.primary).body);
      else query.setState({status:"error",error:new APIError("UNSAFE_PROVIDER_DETAIL",503,"temporarily_unavailable"),fetchStatus:"idle",...(state==="partial"?{data:undefined}:{})});
    });
    const dom=observerDOM();dom.main.ownText=html.replace(/<[^>]*>/g," ");
    assertState(current.condition,dom.run(observationExpression),current.evidence,current.surface.primary);
    assert.doesNotMatch(html,/UNSAFE_PROVIDER_DETAIL/);
  }
});
test("UI-STATE-VIEW-002: actual security partial shows role error while keeping settings and safe labels",async()=>{
  const {ResourcePage}=await import("../../src/features/resources/resource-page.tsx");
  const {APIError}=await import("../../src/lib/api/client.ts");
  const html=renderUI(createElement(ResourcePage,{pageId:"security"}),"en","/admin/security/",client=>{
    const query=client.getQueryCache().find({queryKey:["resource","/roles"],exact:true});assert.ok(query);
    query.setState({data:undefined,status:"error",error:new APIError("UNSAFE_PROVIDER_DETAIL",503,"temporarily_unavailable"),fetchStatus:"idle"});
  });
  assert.match(html,/role="alert"/);assert.match(html,/The role list could not be loaded/);assert.match(html,/value="12"/);assert.match(html,/Minimum password length/);assert.doesNotMatch(html,/UNSAFE_PROVIDER_DETAIL/);
});

test("UI-STATE-005: every initial aggregate holds all real loading owners, not a partial settled substitute", async () => {
  for (const family of ["dashboard","workers","service-health","archive","monitoring","metrics","system-updates","account"]) {
    const current=fixtureFor(family,"initial-loading"), paths=loadingPaths(current.condition,current.surface.primary);
    const held=paths.map(path=>current.get(path));assert.ok(held.every(response=>response.waitUntil));
    const dom=observerDOM();dom.main.ownText="Data has not been received.";
    assertState(current.condition,dom.run(observationExpression),current.evidence,current.surface.primary);
    current.evidence.responses.set(paths.at(-1)!,1);
    assert.throws(()=>assertState(current.condition,dom.run(observationExpression),current.evidence,current.surface.primary),/settled response/);
    current.evidence.responses.clear();current.evidence.requests.delete(paths.at(-1)!);
    assert.throws(()=>assertState(current.condition,dom.run(observationExpression),current.evidence,current.surface.primary),/real GET/);
    current.fixture.release();await Promise.all(held.map(response=>response.waitUntil));
  }
  const {MetricsView}=await import("../../src/features/metrics/metrics-view.tsx");
  const current=fixtureFor("metrics","initial-loading");
  loadingPaths(current.condition,current.surface.primary).forEach(current.get);
  const html=renderUI(createElement(MetricsView),"en","/admin/metrics/",client=>{
    for(const key of [["observability","metrics",10800],["service-health"]]){
      const query=client.getQueryCache().find({queryKey:key,exact:true});assert.ok(query);
      query.setState({data:undefined,status:"pending",fetchStatus:"fetching",error:null});
    }
  });
  const dom=observerDOM();dom.main.ownText=html.replace(/<[^>]*>/g," ");
  const status=html.match(/<div role="status" aria-label="([^"]+)"/);assert.ok(status,"actual loading status must be rendered");
  const notice=dom.main.add(new Element("DIV"));
  notice.setAttribute("role","status");notice.setAttribute("aria-label",status[1]);
  assertState(current.condition,dom.run(observationExpression),current.evidence,current.surface.primary);current.fixture.release();
});
test('UI-STATE-VIEW-009: Service Health renders the real same-cache initial, stale, partial, refresh, empty and unknown states in both locales',async()=>{
  const {ResourcePage}=await import('../../src/features/resources/resource-page.tsx');
  const {APIError}=await import('../../src/lib/api/client.ts');
  for(const locale of ['ja','en'] as const)for(const state of ['ready','initial-loading','blocking-error','stale','partial','background-refresh','empty','unknown']){
    const current=fixtureFor('service-health',state);current.condition.locale=locale;
    const paths=['/service-health','/nodes'],first=new Map(paths.map(path=>[path,current.get(path)]));
    if(['stale','background-refresh'].includes(state))current.fixture.refresh();
    const latest=['stale','background-refresh'].includes(state)?new Map(paths.map(path=>[path,current.get(path)])):first;
    try {
      const html=renderUI(createElement(ResourcePage,{pageId:'service-health'}),locale,'/admin/service-health/',client=>{
        for(const path of paths){const response=latest.get(path)!;const query=client.getQueryCache().find({queryKey:[path.slice(1)],exact:true});assert.ok(query);
          const failed=(response.status??200)===503,held=!!response.waitUntil;
          query.setState({data:state==='initial-loading'||failed&&state!=='stale'?undefined:state==='empty'?[]:failed||held?first.get(path)!.body:response.body,
            status:state==='initial-loading'?'pending':failed?'error':'success',fetchStatus:held?'fetching':'idle',error:failed?new APIError('HIDDEN_PROVIDER_DETAIL',503,'temporarily_unavailable'):null});
        }
      });
      const dom=observerDOM();dom.main.ownText=html.replace(/<[^>]*>/g,' ');
      assertState(current.condition,dom.run(observationExpression),current.evidence,current.surface.primary);
      assert.doesNotMatch(html,/HIDDEN_PROVIDER_DETAIL/);
      if(['ready','stale','partial','background-refresh'].includes(state))assert.match(html,/Worker One/);
      if(['stale','partial'].includes(state)){assert.match(html,/role="alert"/);assert.match(html,/data-remote-freshness="stale"/);assert.match(html,/data-slot="data-table"/);}
      if(state==='initial-loading'){assert.match(html,/role="status"/);assert.match(html,locale==='ja'?/サービスの状態を読み込み中/:/Loading service state/);assert.doesNotMatch(html,/data-slot="data-table"/);}
      if(state==='blocking-error'){assert.match(html,/role="alert"/);assert.doesNotMatch(html,/data-slot="data-table"/);}
      if(state==='background-refresh')assert.match(html,/data-remote-freshness="refreshing"/);
      if(state==='unknown'){for(const column of ['status','health_status']){const cell=html.match(new RegExp('<td[^>]*headers="[^"]+-'+column+'"[^>]*>([\\s\\S]*?)</td>'))?.[1];assert.ok(cell);assert.match(cell,/data-status-known="false"/);assert.match(cell,locale==='ja'?/不明/:/Unknown/);}assert.doesNotMatch(html,/>future_state</);}
      if(state==='stale'){current.evidence.responseStatuses.set('/service-health',[200,200]);assert.throws(()=>assertState(current.condition,dom.run(observationExpression),current.evidence,current.surface.primary));}
    } finally {current.fixture.release();}
  }
});

test('UI-STATE-VIEW-010: Security initial status and System Updates background status follow their real queries',async()=>{
  const {ResourcePage}=await import('../../src/features/resources/resource-page.tsx');
  const {ApplicationInfoView}=await import('../../src/features/application/application-info-view.tsx');
  for(const locale of ['ja','en'] as const){
    const security=renderUI(createElement(ResourcePage,{pageId:'security'}),locale,'/admin/security/',client=>{
      const query=client.getQueryCache().find({queryKey:['resource','/security/settings'],exact:true});assert.ok(query);query.setState({data:undefined,status:'pending',fetchStatus:'fetching'});
    });
    assert.match(security,locale==='ja'?/セキュリティ設定を読み込み中/:/Loading security settings/);assert.match(security,/role="status"/);
    for(const key of ['system-updates','nodes']){
      const html=renderUI(createElement(ApplicationInfoView),locale,'/admin/application/',client=>{
        const query=client.getQueryCache().find({queryKey:[key],exact:true});assert.ok(query);query.setState({fetchStatus:'fetching'});
      });
      assert.match(html,/role="status"/);assert.match(html,locale==='ja'?/更新中/:/Refreshing/);assert.match(html,/B9 Host Agent/);
    }
    const settled=renderUI(createElement(ApplicationInfoView),locale,'/admin/application/');
    assert.doesNotMatch(settled,/>Refreshing (?:service information|while displaying)/);
  }
});

test("UI-STATE-006: actual English failure rendering and runner use the same failure predicate",async()=>{
  const {MetricsView}=await import("../../src/features/metrics/metrics-view.tsx");
  const {APIError}=await import("../../src/lib/api/client.ts");
  const current=fixtureFor("metrics","blocking-error");current.get(current.surface.primary);
  const html=renderUI(createElement(MetricsView),"en","/admin/metrics/",client=>{
    for(const key of [["observability","metrics",10800],["service-health"]]){
      const query=client.getQueryCache().find({queryKey:key,exact:true});assert.ok(query);
      query.setState({data:undefined,status:"error",fetchStatus:"idle",error:new APIError("HIDDEN_ERROR",503,"temporarily_unavailable")});
    }
  });
  const dom=observerDOM();dom.main.ownText=html.replace(/<[^>]*>/g," ");
  assert.equal(hasFailureCopy(dom.run(failureTextExpression)),true);
  assertState(current.condition,dom.run(observationExpression),current.evidence,current.surface.primary);
  assert.equal(hasFailureCopy("Ready. No error has occurred."),false);assert.equal(hasFailureCopy(null),false);
  assert.doesNotMatch(html,/HIDDEN_ERROR/);
});
test("UI-FLOW-001: Archive share-list GET follows both real IDs and rejects wrong ID/method/origin",()=>{
  for(const exercise of [undefined,"long-id"]) {
    const current=fixtureFor("archive","ready",exercise);
    const stream=(current.get("/archive/streams").body as {id:string}[])[0];
    const artifact=(current.get("/streams/"+encodeURIComponent(stream.id)+"/artifacts").body as {id:string}[])[0];
    const path="/streams/"+encodeURIComponent(stream.id)+"/artifacts/"+encodeURIComponent(artifact.id)+"/shares";
    assert.deepEqual(current.get(path).body,[]);assert.deepEqual(current.fixture.unexpected,[]);
    for(const request of [{method:"POST",url:"http://ui.test"+path},{method:"GET",url:"http://ui.test"+path.replace("ui-artifact","unknown")},{method:"GET",url:"https://external.invalid"+path}]) {
      const response=current.fixture.resolver(request);assert.ok(response);assert.ok((response.status??200)>=400);
    }
    assert.equal(current.fixture.unexpected.length,3);current.fixture.release();
  }
});
test("UI-FLOW-002: Nodes remote conditions use the existing registered list, creation stays on its existing route",()=>{
  const nodes=conditions.filter(row=>row.family==="nodes");assert.ok(nodes.length>100);
  assert.ok(nodes.filter(row=>row.kind==="state").every(row=>row.route==="/admin/registered-nodes/"));
  assert.ok(nodes.filter(row=>row.kind!=="state").every(row=>row.route==="/admin/nodes/"));
  assert.equal(conditions.length,4144);
});
test("UI-AUTH-LAYOUT-001: real Login preserves full synthetic labels with wrap/width/height classes and existing credentials",async()=>{
  const {LoginCard}=await import("../../src/components/auth/auth-card.tsx");
  for(const locale of ["ja","en"] as const)for(const exercise of ["long-id","long-text"]) {
    const current=fixtureFor("login","ready",exercise);
    const providers=current.get("/auth/oauth/providers").body as {id:string;name:string}[];
    const html=renderUI(createElement(LoginCard),locale,"/login/",client=>client.setQueryData(["auth","oauth","providers","login"],providers));
    assert.ok(html.includes(providers[0].name),"full provider name must reach actual markup");
    const buttons=html.match(/<button\b[^>]*>[\s\S]*?<\/button>/g)||[];
    for(const button of buttons.filter(button=>button.includes(providers[0].name)||/Passkey|passkey/.test(button)||/type="submit"/.test(button))) {
      assert.match(button,/whitespace-normal/);assert.match(button,/h-auto/);assert.match(button,/min-w-0/);assert.match(button,/max-w-full/);
      assert.doesNotMatch(button,/whitespace-nowrap|overflow-hidden|truncate/);
    }
    assert.ok(buttons.some(button=>button.includes(providers[0].name)));
    assert.match(html,/type="password"/);assert.match(html,/autoComplete="current-password"/i);
    assert.match(html,/value=""/);assert.match(html,/overflow-wrap:anywhere/);
  }
});

const statusPairs = [["metrics", "initial-loading"], ["monitoring", "initial-loading"],
  ["public-archive-share", "initial-loading"], ["public-archive-share", "blocking-error"], ["public-archive-share", "permission-denied"]];
const statusConditions = conditions.filter(condition => condition.kind === "state" && statusPairs.some(([family, state]) => condition.family === family && condition.state === state));

// Transcribe actual SSR branch markup into the existing controlled DOM boundary.
// Geometry is deliberately controlled; this does not claim browser layout proof.
function statusBranchDOM(markup: string, condition: Condition) {
  const dom = observerDOM(); for (const child of dom.body.children) child.parentElement = null; dom.body.children = [];
  const root = /<main\b/.test(markup) ? dom.body : dom.body.add(new Element("MAIN"));
  const stack = [root], decode = (text: string) => text.replace(/&#x([a-f0-9]+);|&#([0-9]+);|&(amp|lt|gt|quot|apos);/gi, (_, hex, decimal, name: string) => hex ? String.fromCodePoint(parseInt(hex, 16)) : decimal ? String.fromCodePoint(Number(decimal)) : ({ amp: "&", lt: "<", gt: ">", quot: '"', apos: "'" }[name.toLowerCase()]!));
  for (const token of markup.match(/<[^>]*>|[^<]+/g) || []) {
    if (token.startsWith("</")) { if (stack.length > 1) stack.pop(); continue; }
    if (token.startsWith("<")) {
      const match = /^<([\w-]+)([\s\S]*?)\/?\s*>$/.exec(token); if (!match) continue;
      const element = stack.at(-1)!.add(new Element(match[1].toUpperCase()));
      for (const attr of match[2].matchAll(/([^\s=/>]+)(?:="([^"]*)"|'([^']*)'|=([^\s>]+))?/g)) {
        const name = attr[1].toLowerCase(), value = decode(attr[2] || attr[3] || attr[4] || ""); element.setAttribute(name, value);
        if (name === "class") element.className = value;
        if (name === "hidden") element.hidden = true;
        if (name === "type") element.type = value;
      }
      if (!/^(AREA|BASE|BR|COL|EMBED|HR|IMG|INPUT|LINK|META|PARAM|SOURCE|TRACK|WBR)$/.test(element.tagName) && !token.endsWith("/>")) stack.push(element);
    } else stack.at(-1)!.ownText += decode(token);
  }
  const main = dom.document.querySelector("main"); assert.ok(main);
  dom.html.attributes.lang = condition.locale; dom.html.className = condition.mode; dom.html.attributes["data-theme"] = condition.theme;
  dom.html.clientWidth = condition.width; dom.html.scrollWidth = condition.width; dom.context.innerWidth = condition.width;
  const url = new URL(condition.route, "http://ui.test"); Object.assign(dom.context.location, { pathname: url.pathname, search: url.search, hash: url.hash });
  dom.document.activeElement = dom.body;
  return { ...dom, main };
}

async function renderStatusBranch(condition: Condition) {
  const { MetricsView } = await import("../../src/features/metrics/metrics-view.tsx");
  const { MonitoringView } = await import("../../src/features/monitoring/monitoring-view.tsx");
  const { ArchiveSharePlayerView } = await import("../../src/features/archive/archive-share-player-view.tsx");
  const { APIError } = await import("../../src/lib/api/client.ts");
  const element = condition.family === "metrics" ? createElement(MetricsView) : condition.family === "monitoring" ? createElement(MonitoringView) : createElement(ArchiveSharePlayerView, { token: "ui-synthetic-share" });
  const keys = condition.family === "metrics" ? [["observability", "metrics", 10800], ["service-health"]] : condition.family === "monitoring" ? [["streams"], ["service-health"], ["resource", "/observability/incidents"], ["resource", "/observability/diagnostics"]] : [["archive-share", "ui-synthetic-share"]];
  const html = renderUI(element, condition.locale as "ja" | "en", condition.route, client => {
    for (const key of keys) {
      const query = client.getQueryCache().find({ queryKey: key, exact: true }); assert.ok(query, "actual query owner must exist");
      query.setState(condition.state === "initial-loading" ? { data: undefined, status: "pending", fetchStatus: "fetching", error: null } :
        { data: undefined, status: "error", fetchStatus: "idle", error: new APIError("NEVER_RENDER_RAW_STATUS_ERROR", condition.state === "permission-denied" ? 403 : 503, "temporarily_unavailable") });
    }
  });
  assert.ok(!html.includes("NEVER_RENDER_RAW_STATUS_ERROR")); return html;
}

async function withStatusEvidence(condition: Condition, check: (browser: BrowserHarness, primary: string) => Promise<void> | void) {
  const owner = createHarnessFixture(), surface = inventory.surfaces.find(row => row.id === condition.family)!;
  const writes = new Map<string, unknown>();
  const run = createConditionRunner("http://ui.test", (name, value) => { assert.equal(writes.has(name), false); writes.set(name, value); }, async () => owner.harness);
  await run(condition, surface, async browser => {
    await navigateDocument(browser, "http://ui.test" + condition.route);
    const paths = condition.state === "initial-loading" ? loadingPaths(condition, surface.primary) : [surface.primary];
    for (const [index, path] of paths.entries()) {
      owner.socket.emitEvent("Fetch.requestPaused", { requestId: "status-" + index, resourceType: "XHR", frameId: "main", request: { method: "GET", url: "http://ui.test" + path } });
      await browser.waitForRequestCount(path, 1);
      if (condition.state !== "initial-loading") await browser.waitForResponseCount(path, 1);
    }
    await check(browser, surface.primary);
  });
  assert.equal(owner.socket.commandsFor("Browser.close").length, 1);
  assert.ok(writes.has(condition.id + ".lifecycle.json"));
}

test("UI-STATUS-ONLY-001: exactly five policies and 40 existing IDs use real SSR branches, the emitted observer, actual caller and real harness GET/status counters", async t => {
  const policies = inventory.surfaces.flatMap(surface => Object.entries(surface.states).filter(([, state]) => state.observation).map(([state]) => [surface.id, state]));
  assert.deepEqual(policies.sort(), [...statusPairs].sort()); assert.equal(statusConditions.length, 40);
  for (const pair of statusPairs) assert.equal(statusConditions.filter(c => c.family === pair[0] && c.state === pair[1]).length, 8);
  for (const condition of statusConditions) await t.test(condition.id, async () => {
    const dom = statusBranchDOM(await renderStatusBranch(condition), condition);
    await withStatusEvidence(condition, (browser, primary) => {
      const observed = dom.run<UIObservation>(observationExpression), layout = dom.run<LayoutObservation>(layoutExpression);
      assert.equal(observed.controls.length, 0); assert.ok(layout.examined > 0);
      assertCurrentObservation(observed, layout, condition, browser, primary);
      assert.throws(() => assertObservation(observed, condition), /requires actual status-only evidence/);
    });
  });
});

test("UI-STATUS-ONLY-002: every authorized branch rejects missing or wrong HTTP evidence at the real observation/state caller", async () => {
  for (const [family, state] of statusPairs) {
    const condition = statusConditions.find(c => c.family === family && c.state === state && c.locale === "en")!;
    const dom = statusBranchDOM(await renderStatusBranch(condition), condition);
    await withStatusEvidence(condition, (browser, primary) => {
      const observation = dom.run<UIObservation>(observationExpression), layout = dom.run<LayoutObservation>(layoutExpression);
      for (const fault of ["missing-get", "wrong-status", "wrong-responses"] as const) {
        const evidence: RequestEvidence = { requests: new Map(browser.requests), responses: new Map(browser.responses), responseStatuses: new Map([...browser.responseStatuses].map(([path, statuses]) => [path, [...statuses]])) };
        const path = state === "initial-loading" ? loadingPaths(condition, primary).at(-1)! : primary;
        if (fault === "missing-get") evidence.requests.delete(path);
        if (fault === "wrong-status") evidence.responseStatuses.set(path, [200]);
        if (fault === "wrong-responses") evidence.responses.set(path, state === "initial-loading" ? 1 : 0);
        assert.throws(() => assertCurrentObservation(observation, layout, condition, evidence, primary), /GET|response|HTTP|403|503|equal|existing per-surface denied query contract/);
      }
      assert.throws(() => assertCurrentObservation({ ...observation, statusPage: undefined }, layout, condition, browser, primary), /missing actual/);
      assert.throws(() => assertCurrentObservation(observation, layout, { ...condition, id: "unregistered" }, browser, primary), /exact registered/);
    });
  }
});

test("UI-STATUS-ONLY-003: zero-control admission preserves visible branch, geometry, copy, secret, ID/label and overflow failures", async () => {
  const condition = statusConditions.find(c => c.family === "metrics" && c.locale === "en")!;
  const html = await renderStatusBranch(condition);
  await withStatusEvidence(condition, (browser, primary) => {
    for (const fault of ["heading", "status", "hidden-status", "wrong-role", "wrong-copy", "route", "geometry", "hidden-control", "secret", "storage", "diagnostic", "duplicate", "reference", "overflow", "language", "mode", "theme", "whole-hidden"] as const) {
      const dom = statusBranchDOM(html, condition), status = dom.main.querySelector('[role=status]')!, heading = dom.main.querySelector('h1')!;
      if (fault === "heading") heading.ownText = "Wrong family";
      if (fault === "status") { status.removeAttribute("role"); status.removeAttribute("aria-label"); }
      if (fault === "hidden-status") status.hidden = true;
      if (fault === "wrong-role") status.setAttribute("role", "alert");
      if (fault === "wrong-copy") status.setAttribute("aria-label", "Ready");
      if (fault === "route") dom.context.location.pathname = "/admin/streams/";
      if (fault === "geometry") status.rect.width = 0;
      if (fault === "hidden-control") dom.main.add(new Element("BUTTON", "Refresh")).hidden = true;
      if (fault === "secret") dom.main.add(new Element("SPAN", "B9-SYNTHETIC-RECOVERY")).hidden = true;
      if (fault === "storage") Object.assign(dom.context.localStorage, { secret: "B9-SYNTHETIC-RECOVERY" });
      if (fault === "diagnostic") dom.main.add(new Element("SPAN", "UI-HIDDEN-DIAGNOSTIC")).hidden = true;
      if (fault === "duplicate") { status.id = "same"; heading.id = "same"; }
      if (fault === "reference") status.setAttribute("aria-describedby", "missing");
      if (fault === "overflow") dom.html.scrollWidth++;
      if (fault === "language") dom.html.attributes.lang = "wrong";
      if (fault === "mode") dom.html.className = "dark";
      if (fault === "theme") dom.html.attributes["data-theme"] = "wrong";
      if (fault === "whole-hidden") dom.main.style.display = "none";
      assert.throws(() => assertCurrentObservation(dom.run(observationExpression), dom.run(layoutExpression), condition, browser, primary), fault);
    }
    for (const state of ["ready", "unknown", "partial", "stale"]) {
      const other = conditions.find(c => c.family === "metrics" && c.state === state && c.locale === "en")!;
      const dom = statusBranchDOM(html, other);
      assert.throws(() => assertCurrentObservation(dom.run(observationExpression), dom.run(layoutExpression), other, browser, primary), /outside the five/);
    }
    const dom = statusBranchDOM(html, condition), button = dom.main.add(new Element("BUTTON"));
    assert.throws(() => assertCurrentObservation(dom.run(observationExpression), dom.run(layoutExpression), condition, browser, primary), /unnamed/);
    button.ownText = "Visible action"; button.rect.left = 2000; button.rect.right = 2140;
    assert.throws(() => assertCurrentObservation(dom.run(observationExpression), dom.run(layoutExpression), condition, browser, primary), /unreachable/);
  });
});

test('UI-WORKER-READ-ACTION-017: read-loss hides the actual view while WKR-01 independently revalidates action-only cached authority',async()=>{
 const {WorkersView}=await import('../../src/features/workers/workers-view.tsx'),{createWorkerRestartController}=await import('../../src/features/workers/workers-action-controller.ts');
 const factorySource=readFileSync(new URL('../workers-pilot-fixture.mts',import.meta.url),'utf8');const factory:(id:string,name:string)=>WorkerNode=actualFunction(factorySource.replace('export function worker(','function worker('),'worker',{});
 const row=factory('worker-read-action-017','Read Authority Worker');
 for(const locale of ['ja','en'] as const){
  const client=new QueryClient({defaultOptions:{queries:{retry:false,staleTime:Infinity,gcTime:Infinity,retryOnMount:false}}});client.setQueryData(['workers'],[row]);client.setQueryData(['auth','me'],{permissions:['workers.read','workers.restart']});
  const render=()=>renderUI(createElement(QueryClientProvider,{client},createElement(WorkersView)),locale,'/admin/workers/');let gets=0,posts=0;const held=Promise.withResolvers<unknown>();
  const controller=createWorkerRestartController({queryClient:client,fetchWorkers:async()=>{gets++;return held.promise;},postRestart:async()=>{posts++;}});
  try{assert.match(render(),/Read Authority Worker/);client.setQueryData(['auth','me'],{permissions:['workers.restart']});
    const denied=render();assert.doesNotMatch(denied,/Read Authority Worker|aria-label="(?:Restart worker|Workerを再起動)"/);assert.match(denied,/Permission denied|権限がありません/);assert.deepEqual([gets,posts],[0,0]);assert.deepEqual(client.getQueryData(['workers']),[row]);
    const opened=controller.open(row);assert.equal(opened.kind,'allowed');if(opened.kind!=='allowed')assert.fail('WKR-01 does not require read or Configuration permissions');
    const pending=controller.submit(opened);assert.deepEqual([gets,posts],[1,0]);assert.equal(controller.isPending(row),true);assert.equal((await controller.submit(opened)).state.kind,'revalidation-unavailable');
    held.resolve([row]);assert.equal((await pending).outcome?.kind,'succeeded');assert.deepEqual([gets,posts],[1,1]);assert.equal(controller.isPending(row),false);
    client.setQueryData(['auth','me'],{permissions:['workers.read']});assert.match(render(),/Read Authority Worker/);assert.equal(controller.open(row).kind,'blocked');
    client.setQueryData(['auth','me'],{permissions:['workers.read','workers.restart']});assert.equal(controller.open(row).kind,'allowed');
    client.getQueryCache().find({queryKey:['auth','me'],exact:true})!.setState({fetchStatus:'fetching'});assert.match(render(),/Read Authority Worker/);assert.equal(controller.open(row).kind,'blocked');assert.deepEqual([gets,posts],[1,1]);
  }finally{held.resolve([row]);client.clear();}
 }
});

test('UI-WORKER-SCENARIO-017: actual browser read-loss predicate accepts the real empty-table row only with denied evidence and no data or action',async()=>{
 const source=readFileSync(new URL('../ui-browser-worker-restart-scenarios.mts',import.meta.url),'utf8'),file=ts.createSourceFile('worker.mts',source,ts.ScriptTarget.Latest,true,ts.ScriptKind.TS);let expression='';
 const visit=(n:ts.Node)=>{if(ts.isCallExpression(n)&&n.arguments.some(a=>ts.isStringLiteral(a)&&a.text==='read permission loss must remove Worker rows and restart triggers')){const first=n.arguments[0];assert.ok(ts.isNoSubstitutionTemplateLiteral(first));expression=first.text;}ts.forEachChild(n,visit);};visit(file);assert.ok(expression);
 const {WorkersView}=await import('../../src/features/workers/workers-view.tsx');const html=renderUI(createElement(WorkersView),'en','/admin/workers/',client=>client.setQueryData(['auth','me'],{permissions:['workers.restart']}));assert.match(html,/No results/);assert.match(html,/<td[^>]*colSpan/i);
 const dom=observerDOM();dom.main.children=[];const stack=[dom.main];
 for(const token of html.matchAll(/<\/?([a-z][\w-]*)\b([^>]*)>|([^<]+)/gi)){if(token[3]){stack.at(-1)!.ownText+=token[3];continue;}const tag=token[1].toUpperCase();if(token[0].startsWith('</')){if(stack.at(-1)?.tagName===tag)stack.pop();continue;}const e=stack.at(-1)!.add(new Element(tag));for(const a of token[2].matchAll(/([\w-]+)="([^"]*)"/g))e.setAttribute(a[1],a[2]);if(!['INPUT','IMG','BR','HR','META','LINK'].includes(tag))stack.push(e);}
 assert.equal(dom.run(expression),true);const root=dom.document.querySelector('[data-screen-family=workers]')!,body=root.querySelector('tbody')!;
 const cell=body.add(new Element('TD','Private cached Worker'));cell.setAttribute('headers','workers-service_name');assert.equal(dom.run(expression),false);body.children=body.children.filter(e=>e!==cell);
 const trigger=root.add(new Element('BUTTON'));trigger.setAttribute('aria-label','Restart worker');assert.equal(dom.run(expression),false);root.children=root.children.filter(e=>e!==trigger);
 const notice=root.querySelectorAll('[role=status]').find(e=>e.textContent.includes('Permission denied:'))!;notice.hidden=true;assert.equal(dom.run(expression),false);notice.hidden=false;notice.ownText='Checking Worker read permissions.';assert.equal(dom.run(expression),false);
});
