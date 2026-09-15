import assert from "node:assert/strict";
import test from "node:test";
import { readFileSync, writeFileSync, mkdirSync, mkdtempSync, rmSync, readdirSync } from "node:fs";
import { execFileSync } from "node:child_process";
import { tmpdir } from "node:os";
import { resolve, basename } from "node:path";
import { fileURLToPath } from "node:url";
import ts from "typescript";
import { actualCallback } from "./source-callback.mts";
import { createHarnessFixture } from "../helpers/browser-cdp-socket-fixture.mts";
import { createConditionRunner, ConditionFailure } from "./condition-lifecycle.mts";
import { DraftRestorationFailure, ConditionOutputFailure, conditionFailureStatus, writeConditionFailure, ownedOutput } from "./run-browser.mts";
import { assertStreamsStartReadinessHandlerGuard, mutateStreamsStartReadinessHandlerGuard, type StreamsStartReadinessGuardSources } from "../helpers/streams-start-readiness-handler-guard.mts";
import type { BrowserHarness } from "../helpers/browser-harness.mts";
import { conditions, inventory, selectedConditions, assertExecution } from "./matrix.mts";
import { createUIFixture } from "./route-fixture.mts";
import { navigateDocument } from "./navigation.mts";
import { observationExpression } from "./observation.mts";
import { requiredScenarioNames, EXPECTED_UI_FOUNDATION_BROWSER_TESTS } from "../helpers/run-ui-foundation-browser.mts";
import { Element, observerDOM } from "./observer-dom.mts";
import { closeCreateAndAssertFocusReturn, mobileFocusDiagnosticExpression } from "../ui-browser-query-auth-helpers.mts";
import { accountAppearanceDiagnostics, accountAppearanceDiagnosticExpression } from "../ui-browser-account-scenarios.mts";
import { setStoredDisplay } from "../ui-browser-navigation-helpers.mts";
import { clickVisible } from "./visible-trigger.mts";

test('UI-NODE-FOCUS-013: actual runner waits for cleanup on its exact remembered trigger, rejecting disappearance or substitutes',async()=>{
 const text=readFileSync(new URL('./run-browser.mts',import.meta.url),'utf8'),file=ts.createSourceFile('runner.mts',text,ts.ScriptTarget.Latest,true,ts.ScriptKind.TS);let call:ts.CallExpression|undefined;
 const visit=(n:ts.Node)=>{if(ts.isCallExpression(n)&&n.arguments.some(a=>ts.isStringLiteral(a)&&a.text==='focus returns to the exact opening trigger after focus cleanup'))call=n;ts.forEachChild(n,visit);};visit(file);assert.ok(call);assert.equal(call.arguments.length,3,'existing default deadline');
 for(const fault of ['none','lost','replacement','hidden','disabled','inert','wrong-focus']){
   const dom=observerDOM();Object.assign(dom.context,{__uiReturnTrigger:dom.button});let observations=0;
   const target={waitFor:async(expression:string,predicate:(v:unknown)=>boolean)=>{observations++;assert.equal(predicate(dom.run(expression)),false,'closed dialog alone is not restored focus');await Promise.resolve();
     if(fault==='lost')dom.button.parentElement=null;if(fault==='hidden')dom.button.hidden=true;if(fault==='disabled')dom.button.disabled=true;if(fault==='inert')dom.button.setAttribute('inert','');
     if(fault==='replacement'){dom.button.parentElement=null;dom.main.children=dom.main.children.filter(e=>e!==dom.button);dom.document.activeElement=dom.main.add(new Element('BUTTON','Open'));}else if(fault!=='wrong-focus')dom.document.activeElement=dom.button;
     observations++;assert.ok(predicate(dom.run(expression)),'focus cleanup did not reach the original target');}};
   const wait=actualCallback('const wait=async()=>{await '+call.getText(file)+';}','wait',{target,Boolean});if(fault==='none')await wait();else await assert.rejects(wait(),/lost|focus cleanup/);assert.equal(observations,2);
 }
});
function diagnosticOutput(lines:string[],code:string){assert.equal(lines.length,1);assert.ok(lines[0].startsWith('UI_BROWSER_DIAGNOSTIC_013 '));const json=lines[0].slice('UI_BROWSER_DIAGNOSTIC_013 '.length);assert.ok(Buffer.byteLength(json)<=4096);assert.doesNotMatch(json,/PRIVATE|https:|password|stack|query=|inputValue/);const result=JSON.parse(json);assert.equal(result.code,code);return result;}
test('UI-D013-MOBILE: actual close helper records phases only on failure and preserves original and diagnostic causes',async()=>{
 for(const fault of ['none','focus','observation','output'])for(const route of ['same-route','cross-route'] as const){
   const dom=observerDOM(),dialog=dom.body.add(new Element('DIV'));dialog.setAttribute('role','dialog');dom.document.activeElement=dialog;dom.button.setAttribute('aria-label','Menu');Object.assign(dom.context,{__uiReturnTrigger:dom.button});
   const original=new Error('PRIVATE primary',{cause:Error('PRIVATE cause')}),diagnostic=Error('PRIVATE diagnostic'),lines:string[]=[];let escape=0;
   const browser={evaluate:async(e:string)=>{if(e===mobileFocusDiagnosticExpression&&fault==='observation')throw diagnostic;return dom.run(e);},pressKey:async(key:string)=>{assert.equal(key,'Escape');escape++;dom.body.children=dom.body.children.filter(e=>e!==dialog);dialog.parentElement=null;},waitFor:async(e:string,p:(v:unknown)=>boolean)=>{if(e.includes('document.activeElement===')){if(fault!=='none')throw original;await Promise.resolve();dom.document.activeElement=dom.button;}assert.ok(p(dom.run(e)));}} as unknown as BrowserHarness;
   let caught:unknown;try{await closeCreateAndAssertFocusReturn(browser,'Menu',route,line=>{if(fault==='output')throw diagnostic;lines.push(line);});}catch(error){caught=error;}
   assert.equal(escape,1);if(fault==='none'){assert.equal(caught,undefined);assert.equal(lines.length,0);}else if(fault==='focus'){assert.equal(caught,original);assert.equal((caught as Error).cause,original.cause);const value=diagnosticOutput(lines,'D013-MOBILE');assert.equal(value.route,route);assert.equal(value.phase,'dialog-closed');assert.deepEqual(value.observations.map((v:{dialogs:number})=>v.dialogs),[1,0,0]);}else{assert.ok(caught instanceof AggregateError);assert.equal(caught.cause,original);assert.ok(caught.errors.includes(diagnostic));if(fault==='observation')assert.equal(diagnosticOutput(lines,'D013-MOBILE').diagnosticFailed,true);}
 }
 const navigation=readFileSync(new URL('../ui-browser-navigation-scenarios.mts',import.meta.url),'utf8');for(const route of ['same-route','cross-route'])assert.equal(navigation.split('"'+route+'"').length-1,1,'one actual phase for each unchanged scenario section');
});
test('UI-D013-ACCOUNT: actual English scenario uses one real tab operation and bounded DOM/request-settlement diagnostics',async()=>{
 const source=readFileSync(new URL('../ui-browser-account-scenarios.mts',import.meta.url),'utf8'),file=ts.createSourceFile('account.mts',source,ts.ScriptTarget.Latest,true,ts.ScriptKind.TS),matches:ts.CallExpression[]=[];
 const visit=(node:ts.Node)=>{if(ts.isCallExpression(node)&&ts.isPropertyAccessExpression(node.expression)&&node.expression.expression.getText(file)==='t'&&node.expression.name.text==='test'&&node.arguments.some(argument=>ts.isStringLiteral(argument)&&argument.text==='Account appearance persists 12 themes and 3 modes with DB fallback and save rollback'))matches.push(node);ts.forEachChild(node,visit);};visit(file);
 assert.equal(matches.length,1,'one actual registered Account scenario');const callback=matches[0].arguments.find(ts.isArrowFunction);assert.ok(callback&&ts.isBlock(callback.body));const body=callback.body;
 const start=body.statements.findIndex(statement=>statement.getText(file).includes('revision: 7'));assert.ok(start>=0);
 const actualEnglish='const run=async()=>{'+body.statements.slice(start).map(statement=>statement.getText(file)).join('\n')+'}';
 for(const fault of ['none','theme','observation','output']){
   const dom=observerDOM();dom.main.children=[];const list=dom.main.add(new Element('DIV'));list.setAttribute('role','tablist');const tab=list.add(new Element('BUTTON','Appearance'));tab.setAttribute('role','tab');tab.setAttribute('aria-controls','panel');tab.setAttribute('aria-selected','false');
   const panel=dom.main.add(new Element('DIV'));panel.id='panel';panel.setAttribute('role','tabpanel');panel.hidden=true;const themes=panel.add(new Element('DIV'));themes.setAttribute('role','radiogroup');themes.setAttribute('aria-label','Color theme');
   const radios=['Violet theme','Ocean theme','Cyan theme','System mode','Dark mode'].map(name=>{const e=themes.add(new Element('BUTTON'));e.setAttribute('role','radio');e.setAttribute('aria-label',name);return e;});
   Object.assign(dom.context.localStorage,{setItem(){},removeItem(){}});Object.assign(dom.html.dataset,{theme:'violet',colorMode:'light'});dom.document.elementFromPoint=()=>tab;
   const original=new Error('PRIVATE theme',{cause:Error('PRIVATE original cause')}),diagnostic=Error('PRIVATE diagnostic'),lines:string[]=[];let clicks=0,reloads=0;
   const fixture={uiPreferenceMethods:['GET','PUT','PUT'],uiPreferenceResponse:{body:{theme_id:'violet',color_mode:'light',revision:7}}};const settled={GET:1,PUT:2};
   const browser={evaluate:async(e:string)=>{if(e===accountAppearanceDiagnosticExpression&&fault==='observation')throw diagnostic;return dom.run(e);},reload:async()=>{reloads++;fixture.uiPreferenceMethods.push('GET');},clickAt:async()=>{clicks++;tab.setAttribute('aria-selected','true');panel.hidden=false;},pressKey:async(key:string)=>{const e=key==='ArrowRight'?radios[2]:radios[4];e.focus();e.setAttribute('aria-checked','true');},waitFor:async(e:string,p:(v:unknown)=>boolean)=>{if(e.includes('Violet theme')&&fault!=='none')throw original;assert.ok(p(dom.run(e)));}} as unknown as BrowserHarness;
   const settle=async(method:'GET'|'PUT',minimum:number)=>{assert.ok(fixture.uiPreferenceMethods.filter(v=>v===method).length>=minimum);settled[method]=minimum;};
   const observed=accountAppearanceDiagnostics(browser,()=>fixture.uiPreferenceMethods,line=>{if(fault==='output')throw diagnostic;lines.push(line);});
   const run=actualCallback(actualEnglish,'run',{browser:observed.browser,fixture,diagnostic:observed,preferenceRequestCount:(method:string)=>fixture.uiPreferenceMethods.filter(v=>v===method).length,waitForPreferenceSettlement:settle,clickVisible,setStoredDisplay});
   let caught:unknown;try{await run();}catch(error){caught=error;}
   assert.equal(clicks,1);assert.equal(reloads,1);assert.equal(fixture.uiPreferenceMethods.filter(v=>v==='PUT').length,2);
   if(fault==='none'){assert.equal(caught,undefined);assert.equal(lines.length,0);assert.equal(settled.GET,2);}else if(fault==='theme'){assert.equal(caught,original);const value=diagnosticOutput(lines,'D013-ACCOUNT');assert.equal(value.phase,'after-tab');const last=value.observations.at(-1);assert.equal(last.lang,'en');assert.equal(last.tabs,1);assert.equal(last.selected,true);assert.equal(last.panels,1);assert.equal(last.get,2);assert.equal(last.getSettled,1);}else{assert.ok(caught instanceof AggregateError);assert.equal(caught.cause,original);assert.ok(caught.errors.includes(diagnostic));if(fault==='observation')assert.equal(diagnosticOutput(lines,'D013-ACCOUNT').diagnosticFailed,true);}
 }
});

test("UI-READINESS-009: actual row and detail declarations bind one existing controller and retain every guard negative", () => {
  const source = (name:string) => readFileSync(new URL("../../src/features/streams/"+name,import.meta.url),"utf8");
  const sources:StreamsStartReadinessGuardSources={view:source("streams-view.tsx"),cells:source("stream-table-cells.tsx"),detailDialog:source("stream-details-dialog.tsx"),details:source("stream-detail-operations.tsx"),controller:source("stream-action-controller.ts"),descriptors:source("stream-action-descriptors.ts")};
  assertStreamsStartReadinessHandlerGuard(sources);
  const negatives:[keyof StreamsStartReadinessGuardSources,string,string,RegExp][]=[
    ["cells","function StreamActionsCell(","function UnconnectedActionsCell(",/StreamActionsCell/],
    ["view",'from "./stream-table-cells"','from "./unrelated-owner"',/actual imported owner/],
    ["cells","cell: StreamActionsCell","cell: StreamNameCell",/action cell must be registered/],
    ["view","value={tablePresentation}","value={otherPresentation}",/table provider/],
    ["view","canUpdate, actionController, handleStreamActionResult","canUpdate, actionController: otherController, handleStreamActionResult",/table presentation/],
    ["cells",'controller={actionController} intent={{ id: "STR-08"','controller={otherController} intent={{ id: "STR-08"',/row STR-08 must use the shared controller/],
    ["detailDialog",'<StreamDetailOperations stream={stream} controller={actionController}', '<StreamDetailOperations stream={stream} controller={otherController}',/detail operations must receive the same controller/],
    ["details",'{ id: "STR-08", label:', '{ id: "STR-09", label:',/detail must declare exactly one/],
    ["details","controls.map(","unrelatedControls.map(",/detail action mapping/],
  ];
  for(const [key,before,after,reason] of negatives) {
    const changed=sources[key].replace(before,after);assert.notEqual(changed,sources[key]);
    assert.throws(()=>assertStreamsStartReadinessHandlerGuard({...sources,[key]:changed}),reason);
  }
  for(const mutation of ["remove-current-permission-snapshot","use-streams-update-authority","remove-pre-submit-evaluation","move-mutation-before-guard","add-alternate-unguarded-mutation"] as const) assert.throws(()=>assertStreamsStartReadinessHandlerGuard(mutateStreamsStartReadinessHandlerGuard(sources,mutation)));
});

test("UI-MATRIX-001: specification denominators and nonready contracts are explicit", () => {
  assert.equal(inventory.surfaces.length, 28);
  assert.equal(conditions.filter(row => row.kind === "ready").length, 784);
  assert.equal(conditions.filter(row => row.kind === "shared").length, 480);
  assert.equal(new Set(conditions.map(row => row.id)).size, conditions.length);
  for (const surface of inventory.surfaces) {
    assert.equal(Object.keys(surface.states).length, 8);
    for (const state of Object.values(surface.states)) {
      assert.equal(typeof state.applicable, "boolean");
      assert.ok(state.contract.length > 30 && state.source.startsWith("web/src/"));
    }
    assert.ok(selectedConditions(surface.id).length > 28);
  }
});
test("UI-MATRIX-002: zero, missing, duplicate, failed, skipped and unreached executions fail closed", () => {
  const expected = selectedConditions("dashboard");
  const passing = expected.map(row => ({ id: row.id, status: "PASS" }));
  assertExecution(expected, passing);
  for (const invalid of [[], passing.slice(1), [...passing, passing[0]], ...["FAIL", "SKIP", "CANCELLED", "NOT_REACHED"].map(status => [{ ...passing[0], status }, ...passing.slice(1)])]) {
    assert.throws(() => assertExecution(expected, invalid));
  }
  assert.throws(() => assertExecution([], []));
  assert.throws(() => selectedConditions("unregistered"));
});
test("UI-PARITY-035: original browser IDs and exact denominator stay separate", () => {
  assert.equal(EXPECTED_UI_FOUNDATION_BROWSER_TESTS, 35);
  assert.equal(requiredScenarioNames.length, 35);
  assert.equal(new Set(requiredScenarioNames).size, 35);
  assert.ok(requiredScenarioNames.includes("false-positive guards reject invalid observable outcomes"));
});
test("UI-FIXTURE-001: current display fixture rejects unknown APIs and external requests", () => {
  const fixture = createUIFixture("http://127.0.0.1:3002");
  fixture.reset(conditions[0], "/streams");
  assert.equal(fixture.resolver({ method: "POST", url: "http://127.0.0.1:3002/not-allowed" })?.status, 404);
  assert.equal(fixture.resolver({ method: "GET", url: "https://external.invalid/" })?.status, 502);
  assert.equal(fixture.unexpected.length, 2);
  fixture.release();
});
test("UI-FIXTURE-002: pending GET is held by the real resolver until released", async () => {
  const fixture = createUIFixture("http://127.0.0.1:3002");
  fixture.reset({ ...conditions[0], state: "initial-loading" }, "/streams");
  const response = fixture.resolver({ method: "GET", url: "http://127.0.0.1:3002/streams" });
  let settled = false;
  response?.waitUntil?.then(() => { settled = true; });
  await Promise.resolve();
  assert.equal(settled, false);
  fixture.release();
  await response?.waitUntil;
  assert.equal(settled, true);
});
test("UI-LIFECYCLE-001: current caller retains condition ownership and request evidence across its navigation", async () => {
  const calls: string[] = [];
  const browser = {
    setFetchDiagnosticContext: (context: { phase: string }) => calls.push(context.phase),
    evaluate: async () => calls.push("paint"),
    assertNoFatalError: () => calls.push("healthy"),
    navigate: async (url: string) => calls.push(url),
    waitForRequestHandlersIdle: async () => calls.push("drain"),
    clearRequestCounts: () => calls.push("clear"), clearNavigationCount: () => calls.push("clear-navigation"), clearConsoleErrors: () => calls.push("clear-console"),
  };
  await navigateDocument(browser as unknown as BrowserHarness, "http://127.0.0.1/product", () => calls.push("reset"));
  assert.equal(calls.includes("about:blank"), false);
  assert.equal(calls.some(call => call.startsWith("clear")), false);
  assert.ok(calls.indexOf("healthy") < calls.indexOf("reset"));
  assert.ok(calls.indexOf("reset") < calls.indexOf("http://127.0.0.1/product"));
});

test("UI-OBSERVATION-001: the browser observation expression is executable JavaScript", () => {
  assert.doesNotThrow(() => new Function("return " + observationExpression));
});

test("UI-FIXTURE-003: new Dashboard GET is explicit and denied-read permissions omit incidents.read", () => {
  const condition = conditions.find(row => row.exercise === "denied-read")!;
  assert.ok(condition);
  const fixture = createUIFixture("http://127.0.0.1:3002");
  fixture.reset(condition, "/streams");
  const me = fixture.resolver({ method: "GET", url: "http://127.0.0.1:3002/auth/me" });
  assert.ok(me);
  assert.doesNotMatch(JSON.stringify(me.body), /incidents.read/);
  fixture.release();
});

test("UI-FIXTURE-009: cover presets use the exact existing GET items contract; other methods, paths and origins still fail", () => {
  const fixture=createUIFixture('http://ui.test');fixture.reset(conditions[0],'/streams');
  try {
    assert.deepEqual(fixture.resolver({method:'GET',url:'http://ui.test/video-cover-presets'})?.body,{items:[]});
    for(const [method,url] of [['POST','http://ui.test/video-cover-presets'],['GET','http://ui.test/video-cover-presets/wrong'],['GET','http://ui.test/video-cover-presets/'],['GET','http://ui.test/video-cover-presets?unknown=1'],['GET','https://elsewhere.invalid/video-cover-presets']])assert.ok((fixture.resolver({method,url})?.status||0)>=400);
    assert.equal(fixture.unexpected.length,5);
    const handler=readFileSync(new URL('../../../internal/httpapi/control_platform_visual_cover.go',import.meta.url),'utf8');
    assert.match(handler,/func \(s \*Server\) listVideoCoverPresets[\s\S]*?writeJSON\(w, http.StatusOK, map\[string\]any\{"items": presets\}\)/);
    assert.deepEqual(fixture.workerRestartTarget(),{id:'worker-one',type:'worker',name:'Worker One'});
  } finally { fixture.release(); }
});

async function failureOutputCase(primaryFails:boolean,restoreFails:boolean,outputFault:string,unknownRestore=false){
  const root=mkdtempSync(resolve(tmpdir(),'autostream-draft-evidence-')),output=resolve(root,'owned-output');
  const web=fileURLToPath(new URL('../..',import.meta.url)),repository=resolve(web,'..');
  const sha=execFileSync('git',['rev-parse','HEAD'],{cwd:repository,encoding:'utf8'}).trim();
  const planned=conditions.filter(c=>c.family==='stream-create-edit'&&c.exercise==='long-text').slice(0,2);assert.equal(planned.length,2);
  const primary=Error('primary identity'),privateText='private-input-URL-query-label-stack-never-serialize';
  const restoration=unknownRestore?{message:privateText,stack:privateText,toJSON(){throw Error('must not serialize restoration object');}}:new AggregateError([Error(privateText),Error(privateText)],privateText);
  const outputError=Error('controlled evidence output failure'),executionError=Error('controlled execution output failure');
  const stats={conditions:0,restores:0,serverCloses:0},owners:ReturnType<typeof createHarnessFixture>[]=[];
  const text=readFileSync(new URL('./run-browser.mts',import.meta.url),'utf8'),file=ts.createSourceFile('runner.mts',text,ts.ScriptTarget.Latest,true,ts.ScriptKind.TS);
  const declaration=file.statements.find((node):node is ts.FunctionDeclaration=>ts.isFunctionDeclaration(node)&&node.name?.text==='main');assert.ok(declaration);
  const main=actualCallback('const selected='+declaration.getText(file).replace(/^export /,''),'selected',{
    assert,repository,web,resolve,execFileSync,ownedOutput,mkdirSync,
    process:{env:{CI:'true',GITHUB_SHA:sha,RUNNER_TEMP:root,AUTOSTREAM_UI_REGRESSION_OUTPUT:output},platform:'linux',argv:['node','controlled-main','stream-create-edit']},
    selectedConditions:()=>planned,inventory,conditions,nativeZoomEvidence:'NOT_PROVEN',assertExecution,
    readFileSync:()=>JSON.stringify({commit:sha}),
    writeFileSync:(path:string,data:string,options:{flag:string})=>{
      assert.equal(options.flag,'wx');const name=basename(path);
      if(name===planned[0].id+'.'+outputFault+'.json'||['both','server'].includes(outputFault)&&name===planned[0].id+'.draft-restoration.json')throw outputError;
      if(outputFault==='both'&&name==='execution.json')throw executionError;
      if(outputFault==='collision'&&name===planned[0].id+'.draft-restoration.json')writeFileSync(path,JSON.stringify({prior:true}),{flag:'wx'});
      return writeFileSync(path,data,options);
    },
    staticExportServer:async()=>({baseURL:'http://ui.test',close:async()=>{stats.serverCloses++;if(outputFault==='server')throw executionError;}}),
    createConditionRunner:(baseURL:string,write:(name:string,value:unknown)=>void)=>{
      const run=createConditionRunner(baseURL,write,async()=>{const owner=createHarnessFixture();owners.push(owner);stats.conditions++;
        owner.harness.waitFor=async<T,>()=>undefined as T;owner.harness.waitForResponseCount=async()=>{};owner.harness.evaluate=async<T,>(expression:string)=>(expression==='test-layout'?{}:true) as T;
        return owner.harness;});return run;
    },
    DraftRestorationFailure,ConditionOutputFailure,conditionFailureStatus,writeConditionFailure,
    navigateDocument:async()=>{},settleRender:async()=>{},paint:async()=>{},clickNamed:async()=>{},closeOverlay:async()=>{},
    driveFormContent:async()=>async()=>{stats.restores++;if(restoreFails)throw restoration;},
    exerciseAccessibility:async()=>{if(primaryFails)throw primary;return {pending:[]};},
    layoutExpression:'test-layout',assertStableCapture:async()=>{},captureObservation:async()=>({observation:{text:'',controls:[]}}),assertCurrentObservation(){},assertContentReached(){},
  });
  let caught:unknown;
  try {
    try {await main();}catch(error){caught=error;}
    const records=new Map(readdirSync(output).map(name=>[name,JSON.parse(readFileSync(resolve(output,name),'utf8')) as unknown]));
    assert.equal(stats.serverCloses,1);assert.ok(owners.every(owner=>owner.socket.commandsFor('Browser.close').length===1));
    return {caught,records,stats,planned,primary,restoration,outputError,executionError,privateText};
  } finally {
    assert.equal(resolve(root,'..'),resolve(tmpdir()));assert.ok(basename(root).startsWith('autostream-draft-evidence-'));
    rmSync(root,{recursive:true});
  }
}
test('UI-DRAFT-OUTPUT-011: real main exercise and outer wx writer keep primary status, separate bounded diagnostics and unchanged lifecycle counts',async()=>{
  for(const [primaryFails,restoreFails,unknown] of [[false,false,false],[true,false,false],[false,true,false],[true,true,false],[true,true,true]]){
    const value=await failureOutputCase(primaryFails,restoreFails,'none',unknown);assert.equal(value.stats.conditions,2);assert.equal(value.stats.restores,2);
    for(const c of value.planned){
      const diagnostic=value.records.get(c.id+'.draft-restoration.json');
      if(restoreFails){assert.deepEqual(diagnostic,{schemaVersion:1,conditionID:c.id,primaryFailed:primaryFails,restorationFailed:true,phase:'draft-restoration',code:unknown?'RESTORE_UNKNOWN':'RESTORE_COMPOSITE'});const json=JSON.stringify(diagnostic);assert.ok(Buffer.byteLength(json)<=4096);assert.equal(json.includes(value.privateText),false);}
      else assert.equal(diagnostic,undefined);
      const lifecycle=value.records.get(c.id+'.lifecycle.json');assert.ok(lifecycle&&typeof lifecycle==='object'&&'cleanupFailures' in lifecycle);assert.equal(lifecycle.cleanupFailures,0);
      if(primaryFails){const status=value.records.get(c.id+'.failure.json');assert.ok(status&&typeof status==='object'&&'error' in status);assert.equal(status.error,value.primary.message);}
    }
    if(primaryFails||restoreFails)assert.ok(value.caught instanceof assert.AssertionError,'failed conditions remain FAIL at assertExecution');else assert.equal(value.caught,undefined);
  }
});
test('UI-DRAFT-OUTPUT-012: either evidence write failure stops the next condition, retains original causes and still writes execution and closes',async()=>{
  for(const fault of ['failure','draft-restoration','both','server','collision']){
    const value=await failureOutputCase(true,true,fault);assert.equal(value.stats.conditions,1);assert.equal(value.stats.restores,1);
    let caught=value.caught;
    if(fault==='server'){assert.ok(caught instanceof AggregateError);assert.equal(caught.errors[1],value.executionError);caught=caught.cause;}
    assert.ok(caught instanceof ConditionOutputFailure);
    let failure=caught;
    if(fault==='both'){assert.equal(failure.output,value.executionError);assert.ok(failure.original instanceof ConditionOutputFailure);failure=failure.original;}
    if(fault==='collision'){assert.ok(failure.output instanceof Error&&'code' in failure.output);assert.equal(failure.output.code,'EEXIST');assert.deepEqual(value.records.get(value.planned[0].id+'.draft-restoration.json'),{prior:true});}
    else assert.equal(failure.output,value.outputError);
    assert.equal(failure.errors[1],failure.output);assert.ok(failure.original instanceof ConditionFailure);
    assert.ok(failure.original.cause instanceof DraftRestorationFailure);assert.equal(failure.original.cause.primary,value.primary);assert.equal(failure.original.cause.restoration,value.restoration);assert.equal(failure.original.cause.cause,value.primary);assert.deepEqual(failure.original.cleanup,[]);
    if(fault!=='both'){const execution=value.records.get('execution.json');assert.ok(execution&&typeof execution==='object'&&'results' in execution&&Array.isArray(execution.results));assert.deepEqual(execution.results.map(row=>row.status),['FAIL','NOT_REACHED']);}
  }
});
