import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { stripTypeScriptTypes } from "node:module";
import test from "node:test";
import { runInNewContext } from "node:vm";
import ts from "typescript";
import { driveFormContent, prepareFreshNodeDraft, nodeDraftDefaults } from "./form-content-driver.mts";
import { conditions } from "./matrix.mts";
import { longIdentifier, longText } from "./fixture-inputs.mts";
import { observerDOM, Element } from "./observer-dom.mts";
import type { BrowserHarness } from "../helpers/browser-harness.mts";
import { createHarnessFixture } from "../helpers/browser-cdp-socket-fixture.mts";
import { ConditionFailure, createConditionRunner } from "./condition-lifecycle.mts";
import { DraftRestorationFailure, writeConditionFailure } from "./run-browser.mts";
import { actualCallback } from "./source-callback.mts";
import { inventory } from "./matrix.mts";

class InputBoundary extends Element {
  currentValue="";name="stream-name";autocomplete="off";label:string;onSet:(value:string)=>void=()=>{};
  constructor(label="Stream name",textarea=false){super(textarea?"TEXTAREA":"INPUT");this.label=label;this.id="stream-name";this.type=textarea?"textarea":"text";Reflect.deleteProperty(this,"value");}
  getAttribute(name:string){return name==="autocomplete"?this.autocomplete:super.getAttribute(name);}
  hasAttribute(name:string){return this.getAttribute(name)!==null;}
}
// The actual emitted prototype setter must run, including the Textarea branch.
Object.defineProperty(InputBoundary.prototype,"value",{get(this:InputBoundary){return this.currentValue;},set(this:InputBoundary,value:string){this.currentValue=value;this.onSet(value);},configurable:true});
function boundary(inputs:InputBoundary[],failFirstFill=false,node=false){
  const dom=observerDOM();dom.main.children=[];dom.context.location.pathname=node?"/admin/nodes/":"/admin/streams/";
  const root=dom.main.add(new Element("DIV"));root.setAttribute("data-screen-family","nodes");
  const dialog=new Element("DIV");dialog.setAttribute("role","dialog");const title=dialog.add(new Element("H2","Node Registration"));title.setAttribute("data-slot","dialog-title");
  const expressions:string[]=[],fills:string[]=[];let failed=false;
  const attach=(input:InputBoundary)=>{
    const row=dialog.add(new Element("DIV")),label=row.add(new Element("LABEL",input.label));label.htmlFor=input.id;input.labels=[label];row.add(input);
    input.onSet=value=>{fills.push(value);};
    const dispatch=input.dispatchEvent.bind(input);
    input.dispatchEvent=event=>{if(failFirstFill&&!failed&&event.type==="input"){failed=true;throw Error("partial fill failed");}return dispatch(event);};
  };
  inputs.forEach(attach);
  const type=dialog.add(new Element("BUTTON","Worker Node Agent"));type.id="node-type";type.setAttribute("role","combobox");
  const typeLabel=dialog.add(new Element("LABEL","Node type"));typeLabel.htmlFor=type.id;type.labels=[typeLabel];
  const open=()=>dom.body.add(dialog);if(!node)open();
  const context={...dom.context,HTMLInputElement:InputBoundary,HTMLTextAreaElement:InputBoundary,Event};
  const evaluate=async(expression:string)=>{expressions.push(expression);return runInNewContext(expression,context);};
  const browser={
    evaluate,
    async fillSelector(){throw Error("Textarea must use its actual prototype through existing evaluate; Input-only harness helper is not a textarea driver");},
    async waitFor(expression:string,predicate:(value:unknown)=>boolean){assert.equal(predicate(await evaluate(expression)),true);},
  } as unknown as BrowserHarness;
  return {browser,expressions,fills,context,dom,root,dialog,type,open,attach};
}
function condition(exercise:string,locale="en"){const value=conditions.find(row=>row.family==="stream-create-edit"&&row.exercise===exercise&&row.locale===locale);assert.ok(value);return value;}
const nodeSource=readFileSync(new URL("../../src/features/nodes/node-registration-view.tsx",import.meta.url),"utf8");
function sourceDefaults(){
  const file=ts.createSourceFile("nodes.tsx",nodeSource,ts.ScriptTarget.Latest,true,ts.ScriptKind.TSX),values:Record<string,string>={};
  const walk=(node:ts.Node)=>{if(ts.isVariableDeclaration(node)&&ts.isArrayBindingPattern(node.name)&&["name","description","nodeType"].includes(node.name.elements[0]?.getText(file))){const call=node.initializer;assert.ok(call&&ts.isCallExpression(call)&&call.expression.getText(file)==="useState"&&ts.isStringLiteral(call.arguments[0]));values[node.name.elements[0].getText(file)]=(call.arguments[0] as ts.StringLiteral).text;}ts.forEachChild(node,walk);};walk(file);
  assert.deepEqual(values,{name:nodeDraftDefaults.name,description:nodeDraftDefaults.description,nodeType:nodeDraftDefaults.type});return values;
}
function nodeBoundary(locale="en",failFirstFill=false){
  const defaults=sourceDefaults(),name=new InputBoundary(locale==="ja"?"名称":"Name"),description=new InputBoundary(locale==="ja"?"説明":"Description",true);
  name.id="node-name";name.value=defaults.name;description.id="node-description";description.value=defaults.description;
  const b=boundary([name,description],failFirstFill,true);return {...b,name,description,defaults};
}

test("UI-FORM-CONTENT-001: actual emitted expressions fill and restore both locales and exercises",async()=>{
  for(const locale of ["ja","en"])for(const required of [false,true])for(const exercise of ["long-id","long-text"]){
    const input=new InputBoundary((locale==="ja"?"配信枠名":"Stream name")+(required?" *":"")),b=boundary([input]);
    const restore=await driveFormContent(b.browser,condition(exercise,locale));
    assert.equal(input.value,exercise==="long-id"?longIdentifier:longText);assert.ok(input.hasAttribute("data-ui-content-field"));
    await restore();assert.equal(input.value,"");assert.equal(input.hasAttribute("data-ui-content-field"),false);assert.equal(b.fills.length,2);
  }
});
test("UI-FORM-CONTENT-002: actual selector rejects hidden disabled duplicate credential and real input",async()=>{
  const cases:Array<(input:InputBoundary)=>void>=[input=>{input.hidden=true;},input=>{input.disabled=true;},input=>{input.style.visibility="hidden";},input=>{input.setAttribute("inert","");},input=>{input.type="password";},input=>{input.name="token";},input=>{input.id="secret";},input=>{input.value="existing user input";}];
  for(const change of cases){const input=new InputBoundary();change(input);const original=input.value,b=boundary([input]);await assert.rejects(driveFormContent(b.browser,condition("long-text")),/unique|synthetic|secret/);assert.equal(b.fills.length,0);assert.equal(input.value,original);assert.equal(input.hasAttribute("data-ui-content-field"),false);}
  const a=new InputBoundary(),b=new InputBoundary();await assert.rejects(driveFormContent(boundary([a,b]).browser,condition("long-id")),/unique/);assert.ok(!a.hasAttribute("data-ui-content-field")&&!b.hasAttribute("data-ui-content-field"));
});
test("UI-FORM-CONTENT-003: partial fill failure restores input and marker before propagating failure",async()=>{
  const input=new InputBoundary(),b=boundary([input],true);await assert.rejects(driveFormContent(b.browser,condition("long-id")),/partial fill failed/);assert.equal(input.value,"");assert.equal(input.hasAttribute("data-ui-content-field"),false);
});
test("UI-FORM-CONTENT-004: removing the real raw-template boundary reproduces the 003 syntax failure",async()=>{
  const source=readFileSync(new URL("./form-content-driver.mts",import.meta.url),"utf8");
  const mutant=stripTypeScriptTypes(source).replace(/^import .*;\r?\n/gm,"").replaceAll("export ","").replaceAll("String.raw`","`");
  const drive=new Function("assert","longIdentifier","longText",mutant+";return driveFormContent;")(assert,longIdentifier,longText) as typeof driveFormContent;
  for(const exercise of ["long-id","long-text"]){const input=new InputBoundary(),b=boundary([input]);await assert.rejects(drive(b.browser,condition(exercise)),/Invalid regular expression|Nothing to repeat/);assert.equal(b.fills.length,0);assert.equal(input.hasAttribute("data-ui-content-field"),false);}
});
test("UI-FORM-CONTENT-009: Nodes long values reach the actual registration field and restore only its owned draft",async()=>{
  const planned=conditions.filter(row=>row.family==="nodes"&&["long-id","long-text"].includes(row.exercise||""));assert.equal(planned.length,16);
  for(const c of planned){
    const b=nodeBoundary(c.locale),fresh=await prepareFreshNodeDraft(b.browser,c);b.open();
    const restore=await driveFormContent(b.browser,c,fresh),input=c.exercise==="long-id"?b.name:b.description;
    assert.equal(input.value,c.exercise==="long-id"?longIdentifier:longText);await restore();await restore();
    assert.equal(b.name.value,b.defaults.name);assert.equal(b.description.value,b.defaults.description);assert.equal(b.fills.length,2);assert.equal(input.hasAttribute("data-ui-content-field"),false);
    assert.doesNotMatch(b.expressions.join('\n'),/fetch\(|XMLHttpRequest|\.submit\(|requestSubmit\(/,'input delivery must not add a request or submit path');
    await assert.rejects(prepareFreshNodeDraft(b.browser,c),/first registration/);
  }
});
test("UI-FORM-CONTENT-010: later edits and detached or replaced fields are never erased on cleanup",async()=>{
  for(const fault of ["new-edit","prefix-edit","detached","replacement","owner-replaced","marker-lost"]){
    const input=new InputBoundary(),b=boundary([input]),restore=await driveFormContent(b.browser,condition("long-text"));
    if(fault==="new-edit")input.value="separate user edit";if(fault==="prefix-edit")input.value=longText.slice(0,5);
    if(fault==="detached")input.parentElement=null;
    if(fault==="replacement"){input.parentElement!.children=input.parentElement!.children.filter(e=>e!==input);input.parentElement=null;const replacement=new InputBoundary();replacement.value="different owner";replacement.setAttribute("data-ui-content-field","");b.attach(replacement);}
    if(fault==="owner-replaced")b.dialog.parentElement=null;if(fault==="marker-lost")input.removeAttribute("data-ui-content-field");
    const original=input.value,count=b.fills.length;await assert.rejects(restore(),/replaced or independently edited/);assert.equal(input.value,original);assert.equal(b.fills.length,count);assert.equal(input.hasAttribute("data-ui-content-field"),false);
  }
});
test("UI-FORM-CONTENT-011: fresh worker permit rejects reused/wrong documents and missing, ambiguous, hidden, secret or edited fields before mutation",async()=>{
  const c=conditions.find(row=>row.family==="nodes"&&row.exercise==="long-id"&&row.locale==="en")!;
  for(const fault of ["unknown-name","unknown-description","wrong-type","empty","hidden","disabled","duplicate","wrong-label","hidden-label","secret","wrong-route","wrong-document","owner-detached","no-permit","forged-permit"]){
    const b=nodeBoundary(),fresh=await prepareFreshNodeDraft(b.browser,c);b.open();
    if(fault==="unknown-name")b.name.value="Other editor";if(fault==="unknown-description")b.description.value="Other editor";if(fault==="empty")b.name.value="";
    if(fault==="wrong-type")b.type.ownText="Encoder / Recorder Node Agent";
    if(fault==="hidden")b.name.hidden=true;if(fault==="disabled")b.name.disabled=true;if(fault==="duplicate")b.attach(new InputBoundary("Name"));
    if(fault==="wrong-label")b.name.labels[0].htmlFor="other";if(fault==="hidden-label")b.name.labels[0].hidden=true;if(fault==="secret")b.name.name="credential";
    if(fault==="wrong-route")b.context.location.pathname="/admin/registered-nodes/";
    if(fault==="wrong-document")b.context.document={...b.context.document};if(fault==="owner-detached")b.root.parentElement=null;
    const before=[b.name.value,b.description.value],count=b.fills.length;
    await assert.rejects(driveFormContent(b.browser,c,fault==="no-permit"?undefined:fault==="forged-permit"?{...fresh}:fresh),/defaults|unique|secret|owner|permit/);
    assert.deepEqual([b.name.value,b.description.value],before);assert.equal(b.fills.length,count);assert.equal(b.name.hasAttribute("data-ui-content-field"),false);
  }
  const reused=nodeBoundary();reused.open();await assert.rejects(prepareFreshNodeDraft(reused.browser,c),/before first dialog/);
});
test("UI-FORM-CONTENT-012: real native setter failures restore exact defaults and preserve original errors and independent later edits",async()=>{
  const c=conditions.find(row=>row.family==="nodes"&&row.exercise==="long-text"&&row.locale==="en")!;
  const b=nodeBoundary("en",true),fresh=await prepareFreshNodeDraft(b.browser,c);b.open();
  await assert.rejects(driveFormContent(b.browser,c,fresh),/partial fill failed/);assert.equal(b.description.value,b.defaults.description);assert.equal(b.fills.length,2);assert.equal(b.description.hasAttribute("data-ui-content-field"),false);
  for(const fault of ["external-edit","prefix-edit","cleanup-failure"]){
    const x=nodeBoundary(),permit=await prepareFreshNodeDraft(x.browser,c);x.open();const original=Error("original input dispatch failure");
    x.description.dispatchEvent=()=>{if(fault==="cleanup-failure")throw original;x.description.currentValue=fault==="prefix-edit"?longText.slice(0,8):"Independent edit";throw original;};
    await assert.rejects(driveFormContent(x.browser,c,permit),(error:unknown)=>{assert.ok(error instanceof AggregateError);assert.equal(error.cause,original);assert.equal(error.errors[0],original);return true;});
    assert.equal(x.description.hasAttribute("data-ui-content-field"),false);
    assert.equal(x.description.value,fault==="cleanup-failure"?x.defaults.description:fault==="prefix-edit"?longText.slice(0,8):"Independent edit");
  }
});
async function runnerRestoration(bodyFails:boolean,restorationFault:string,mutant=false){
  let text=readFileSync(new URL('./run-browser.mts',import.meta.url),'utf8');
  if(mutant){const changed=text.replace('throw bodyFailed ? failure : restoration;','if (bodyFailed) throw bodyFailure; throw restoration;');assert.notEqual(changed,text);text=changed;}
  const file=ts.createSourceFile('runner.mts',text,ts.ScriptTarget.Latest,true,ts.ScriptKind.TS),arrows:ts.ArrowFunction[]=[];
  const visit=(node:ts.Node)=>{if(ts.isCallExpression(node)&&node.expression.getText(file)==='runCondition'&&ts.isArrowFunction(node.arguments[2]))arrows.push(node.arguments[2]);ts.forEachChild(node,visit);};visit(file);assert.equal(arrows.length,1);
  const c=condition('long-text'),surface=inventory.surfaces.find(item=>item.id===c.family);assert.ok(surface);
  const input=new InputBoundary(),b=boundary([input]),owner=createHarnessFixture(),primary=Error('controlled primary failure'),markerError=Error('controlled marker cleanup failure');
  let restoreError:unknown,restoreCalls=0,markerCalls=0;
  const evaluate=b.browser.evaluate.bind(b.browser);
  owner.harness.evaluate=async<T,>(expression:string):Promise<T>=>{
    if(expression==='test-layout')return {} as T;
    if(expression.includes('document.activeElement ==='))return true as T;
    if(expression==='delete globalThis.__uiReturnTrigger;true')return true as T;
    const value=await evaluate<T>(expression);
    if(expression.includes("removeAttribute('data-ui-content-field');delete")){markerCalls++;if(restorationFault==='marker-composite')throw markerError;}
    return value;
  };
  owner.harness.waitFor=async<T,>(expression:string,predicate:(value:T)=>boolean)=>{
    if(expression.includes('__uiContentDraft')){const value=await owner.harness.evaluate<T>(expression);assert.equal(predicate(value),true);return value;}
    return undefined as T;
  };
  owner.harness.waitForResponseCount=async()=>{};
  const records=new Map<string,unknown>(),write=(name:string,value:unknown)=>{assert.equal(records.has(name),false);records.set(name,value);};
  const draftRestoration:{failure?:DraftRestorationFailure}={};
  const exercise=actualCallback('const selected='+arrows[0].getText(file),'selected',{
    condition:c,surface,assert,write,server:{baseURL:'http://ui.test'},draftRestoration,DraftRestorationFailure,
    navigateDocument:async()=>{},settleRender:async()=>{},paint:async()=>{},clickNamed:async()=>{},closeOverlay:async()=>{},prepareFreshNodeDraft,
    driveFormContent:async(...args:Parameters<typeof driveFormContent>)=>{const restore=await driveFormContent(...args);return async()=>{restoreCalls++;try{await restore();}catch(error){restoreError=error;throw error;}};},
    exerciseAccessibility:async()=>{if(restorationFault==='independent'||restorationFault==='marker-composite')input.value='independent edit';if(restorationFault==='owner')b.dialog.parentElement=null;if(bodyFails)throw primary;return {pending:[]};},
    layoutExpression:'test-layout',assertStableCapture:async()=>{},captureObservation:async()=>({observation:{text:'',controls:[]}}),assertCurrentObservation(){},assertContentReached(){},
  });
  const run=createConditionRunner('http://ui.test',write,async()=>owner.harness);
  let caught:unknown,result:unknown;
  try {result=await run(c,surface,exercise);}catch(error){caught=error;}
  assert.equal(owner.socket.commandsFor('Browser.close').length,1);
  if(caught){assert.ok(caught instanceof ConditionFailure);assert.deepEqual(caught.cleanup,[]);assert.equal(caught.stop,false);writeConditionFailure(c,caught,write,draftRestoration.failure);}
  return {caught,result,primary,restoreError,markerError,restoreCalls,markerCalls,input,b,records,diagnostic:records.get(c.id+'.draft-restoration.json'),draftRestoration};
}
test('UI-DRAFT-RESTORATION-011: actual exercise, condition lifecycle, driver and outer writer retain single and dual error identities and restore once',async()=>{
  for(const bodyFails of [false,true])for(const fault of ['none','independent','owner','marker-composite']){
    const value=await runnerRestoration(bodyFails,fault);assert.equal(value.restoreCalls,1);assert.equal(value.markerCalls,1);assert.equal(value.input.hasAttribute('data-ui-content-field'),false);
    if(fault==='none'){
      assert.equal(value.input.value,'');assert.equal(value.diagnostic,undefined);
      if(bodyFails){assert.ok(value.caught instanceof ConditionFailure);assert.equal(value.caught.cause,value.primary);}else assert.deepEqual(value.result,{pending:[]});
    }else{
      assert.equal(value.input.value,fault==='owner'?longText:'independent edit');assert.ok(value.caught instanceof ConditionFailure);assert.ok(value.restoreError);
      if(bodyFails){assert.ok(value.caught.cause instanceof DraftRestorationFailure);assert.equal(value.caught.cause.primary,value.primary);assert.equal(value.caught.cause.cause,value.primary);assert.equal(value.caught.cause.restoration,value.restoreError);}
      else assert.equal(value.caught.cause,value.restoreError,'restore-only keeps the actual thrown object');
      assert.deepEqual(value.diagnostic,{schemaVersion:1,conditionID:condition('long-text').id,primaryFailed:bodyFails,restorationFailed:true,phase:'draft-restoration',code:fault==='marker-composite'?'RESTORE_COMPOSITE':'RESTORE_UNKNOWN'});
      if(fault==='marker-composite'){assert.ok(value.restoreError instanceof AggregateError);assert.equal(value.restoreError.errors[1],value.markerError);assert.equal(value.draftRestoration.failure?.restoration,value.restoreError);}
    }
  }
});
test('UI-DRAFT-RESTORATION-012: suppressing the real dual failure loses restoration identity and is rejected',async()=>{
  const value=await runnerRestoration(true,'independent',true);assert.ok(value.caught instanceof ConditionFailure);assert.equal(value.caught.cause,value.primary);
  assert.throws(()=>assert.ok(value.caught instanceof ConditionFailure&&value.caught.cause instanceof DraftRestorationFailure));
});

test("UI-FORM-CONTENT-013: actual runner arms exactly the first Node dialog after document navigation and passes that same permit",()=>{
  const text=readFileSync(new URL("./run-browser.mts",import.meta.url),"utf8");
  assert.ok(text.indexOf("await navigateDocument(target,")<text.indexOf("freshNodeDraft = await prepareFreshNodeDraft(target, condition)"));
  assert.ok(text.indexOf("freshNodeDraft = await prepareFreshNodeDraft(target, condition)")<text.indexOf("await clickNamed(target, /^(Nodeを新規作成|Create node)$/"));
  assert.match(text,/restoreForm = await driveFormContent\(target, condition, freshNodeDraft\)/);
  assert.equal((text.match(/prepareFreshNodeDraft\(target, condition\)/g)||[]).length,1);
  assert.match(text,/else assert.deepEqual\(mutations, \[\], "display\/cancel must not mutate"\)/);
  assert.match(readFileSync(new URL("../helpers/browser-harness.mts",import.meta.url),"utf8"),/if \(!\(element instanceof HTMLInputElement\)\) return false/);
});
