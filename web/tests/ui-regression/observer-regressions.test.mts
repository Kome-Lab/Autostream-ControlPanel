import assert from "node:assert/strict";
import test from "node:test";
import { observerDOM, Element } from "./observer-dom.mts";
import { observationExpression, assertObservation, focusExpression, assertFocus, exerciseAccessibility, safeKeyboardTrace, type UIObservation } from "./observation.mts";
import { layoutExpression, assertLayout, type LayoutObservation } from "./layout-observation.mts";
import { visibleTriggerExpression, clickVisible, clickDisabledVisible, clickStreamPrimary, clickWorkerRestart } from "./visible-trigger.mts";
import { createUIFixture } from "./route-fixture.mts";
import { assertPageText, exerciseTableSort, pageCounterExpression } from "./table-browser.mts";
import { conditions } from "./matrix.mts";
import type { BrowserHarness } from "../helpers/browser-harness.mts";
const condition={...conditions[0],locale:"en" as const,mode:"light" as const,theme:"autostream" as const};
const check=(dom:ReturnType<typeof observerDOM>,exercise?:string)=>assertObservation(dom.run<UIObservation>(observationExpression),{...condition,exercise} as typeof condition);
const keyboardCondition=conditions.find(row=>row.family==="login"&&row.exercise==="keyboard")!;
test('UI-DRIVER-012: eligible Worker is selected by its actual display identity and confirmed ID, never mixed-row order',async()=>{
  const fixture=createUIFixture('http://ui.test');fixture.reset(conditions.find(c=>c.family==='workers'&&c.state==='ready')!,'/workers');
  const worker=fixture.workerRestartTarget();
  for(const fault of ['none','encoder-only','duplicate','disabled','wrong-id','missing-control']){
    const dom=observerDOM(),owner=dom.main.add(new Element('DIV'));owner.setAttribute('data-screen-family','workers');
    const table=owner.add(new Element('TABLE')),body=table.add(new Element('TBODY'));
    const addRow=(type:string)=>{const row=body.add(new Element('TR')),name=row.add(new Element('TD')),kind=row.add(new Element('TD'));
      name.setAttribute('headers','worker-table-service_name');name.add(new Element('DIV',worker.name)).className='font-medium';
      kind.setAttribute('headers','worker-table-service_type');kind.add(new Element('DIV',type));
      return row.add(new Element('BUTTON','Restart worker'));};
    addRow('Encoder');const target=fault==='encoder-only'?null:addRow('Worker');if(fault==='duplicate')addRow('Worker');
    if(target){target.disabled=fault==='disabled';target.hidden=fault==='missing-control';dom.document.elementFromPoint=()=>target;}
    let clicks=0;
    const browser={evaluate:async(e:string)=>dom.run(e),waitFor:async(e:string,p:(v:unknown)=>boolean)=>assert.ok(p(dom.run(e))),clickAt:async()=>{
      clicks++;const dialog=dom.body.add(new Element('DIV'));dialog.setAttribute('role','alertdialog');
      const section=dialog.add(new Element('SECTION'));section.setAttribute('data-confirmation-section','target');
      section.add(new Element('LI',fault==='wrong-id'?'encoder-one':worker.id));section.add(new Element('LI',worker.name));
    }} as unknown as BrowserHarness;
    if(fault==='none')await clickWorkerRestart(browser,worker);else await assert.rejects(clickWorkerRestart(browser,worker));
    assert.equal(clicks,['none','wrong-id'].includes(fault)?1:0,fault);
    assert.equal(dom.document.querySelector('[data-ui-worker-row]'),null);
    assert.equal(dom.document.querySelector('[data-ui-scenario-target]'),null);
  }
  fixture.release();
});
function loginKeyboardDOM() {
  const dom=observerDOM();dom.input.setAttribute("autocomplete","username");
  const password=dom.main.add(new Element("INPUT"));password.type="password";password.setAttribute("type","password");
  const form=dom.main.add(new Element("FORM"));
  dom.main.children=dom.main.children.filter(e=>![dom.input,password,dom.button].includes(e));
  form.add(dom.input);form.add(password);form.add(dom.button);dom.button.setAttribute("type","submit");
  return {dom,nodes:[dom.input,password,dom.button]};
}

test("UI-DRIVER-001: actual runner page assertion rejects one-based confusion",()=>{
  assertPageText("2 / 4",1); assertPageText("Page 2 of 4",1);
  assert.throws(()=>assertPageText("1 / 4",1),/zero-based URL/);
});

test('UI-DRIVER-011: actual live counter excludes page-size text and rejects hidden, duplicate and stale counters',()=>{
  const dom=observerDOM(),pagination=dom.main.add(new Element('DIV','Rows per page82050100'));pagination.setAttribute('data-slot','table-pagination');
  const counter=pagination.add(new Element('SPAN','Page 2 of 4'));counter.setAttribute('aria-live','polite');
  assertPageText(dom.run(pageCounterExpression),1);
  assert.throws(()=>assertPageText(pagination.textContent,1));
  counter.ownText='2 / 4 ページ';assertPageText(dom.run(pageCounterExpression),1);
  counter.ownText='Page 1 of 4';assert.throws(()=>assertPageText(dom.run(pageCounterExpression),1),/zero-based/);
  counter.hidden=true;assert.throws(()=>dom.run(pageCounterExpression),/one visible/);counter.hidden=false;
  const duplicate=pagination.add(new Element('SPAN','Page 2 of 4'));duplicate.setAttribute('aria-live','polite');assert.throws(()=>dom.run(pageCounterExpression),/one visible/);
});

test('UI-LAYOUT-009: actual remaining reachability failures keep bounded value-free geometry and restoration',()=>{
  const dom=observerDOM();dom.button.ownText='private@example.test SECRET_TOKEN';dom.button.rect.left=-200;dom.button.rect.right=-60;
  dom.main.style.overflowX='hidden';const before={x:dom.context.scrollX,y:dom.context.scrollY};
  const value=dom.run<LayoutObservation>(layoutExpression);assert.throws(()=>assertLayout(value),/unreachable/);
  assert.ok(value.diagnostics?.failures.length);assert.equal(value.diagnostics.restored,true);
  const diagnostic=JSON.stringify(value.diagnostics);assert.ok(Buffer.byteLength(diagnostic)<4096);assert.doesNotMatch(diagnostic,/private|SECRET|TOKEN|example/);
  assert.deepEqual({x:dom.context.scrollX,y:dom.context.scrollY},before);
});
test("UI-DRIVER-002: real selector rejects hidden, disabled and ambiguous triggers",async()=>{
  const dom=observerDOM();assert.equal(dom.run(visibleTriggerExpression("button",/^Open$/)),true);
  dom.button.hidden=true;assert.equal(dom.run(visibleTriggerExpression("button",/^Open$/)),false);
  dom.button.hidden=false;dom.button.disabled=true;assert.equal(dom.run(visibleTriggerExpression("button",/^Open$/)),false);
  dom.button.disabled=false;dom.main.add(new Element("BUTTON","Open"));
  let clicks=0;
  const browser={waitFor:async(expression:string,predicate:(value:unknown)=>boolean)=>{assert.ok(predicate(dom.run(expression)));},clickSelector:async()=>{clicks++;},evaluate:async(expression:string)=>dom.run(expression)} as unknown as BrowserHarness;
  await assert.rejects(clickVisible(browser,"button",/^Open$/),/Ambiguous visible trigger/);assert.equal(clicks,0);
});
test("UI-DRIVER-003: actual desktop and mobile sort runner rejects unchanged row order",async()=>{
  for(const desktop of [true,false]) {
    const direction="ascending";const visited:string[]=[];
    const browser={
      evaluate:async(expression:string)=>{visited.push(expression);if(expression.includes("return {x,y}"))return {x:80,y:22};if(expression.includes("row")&&expression.includes("tbody"))return ["Z","A"];
        if(expression.includes("tbody tr td"))return ["Z","A"];
        if(expression.includes("findIndex"))return 0;if(expression.includes("streams.sort"))return "name";
        if(expression.includes("const e=document.querySelector"))return desktop;return true;},
      waitFor:async(expression:string,predicate:(value:unknown)=>boolean)=>{visited.push(expression);assert.ok(predicate(expression.includes("aria-sort")?direction:expression.includes("?.value")?"name":true));},
      clickAt:async()=>{},clickSelector:async()=>{},pressKey:async()=>{},pressNativeKey:async()=>{},
    } as unknown as BrowserHarness;
    await assert.rejects(exerciseTableSort(browser),/actual displayed row order/);
    assert.ok(visited.some(expression=>expression.includes(desktop?"th:first-child button":"option[value")));
  }
});
test("UI-OBSERVER-001: collected DOM references, labels and duplicate IDs are required",()=>{
  const dom=observerDOM();check(dom);
  dom.input.setAttribute("aria-describedby","missing-description");
  assert.throws(()=>check(dom),/references must resolve/);
  dom.input.removeAttribute("aria-describedby");dom.label.id="stream-name";
  assert.throws(()=>check(dom),/duplicate DOM IDs/);
  dom.label.id="field-label";dom.input.labels=[];dom.input.setAttribute("title","Name");
  assert.throws(()=>check(dom),/input label/);
});
test("UI-OBSERVER-002: actual keyboard loop detects stationary, body, hidden, offscreen and escaped focus",async()=>{
  const {dom}=loginKeyboardDOM();
  const observed=dom.run<Parameters<typeof assertFocus>[0]>(focusExpression);assertFocus(observed);
  let keys=0;
  const browser={pressTab:async()=>{keys++;},evaluate:async(expression:string)=>dom.run(expression)} as unknown as BrowserHarness;
  await assert.rejects(exerciseAccessibility(browser,keyboardCondition),/Tab must move/);assert.equal(keys,2);
  dom.document.activeElement=dom.body;assert.throws(()=>assertFocus(dom.run(focusExpression)),/hidden, inert, body/);
  dom.document.activeElement=dom.input;dom.input.hidden=true;assert.throws(()=>assertFocus(dom.run(focusExpression)),/hidden, inert, body/);
  dom.input.hidden=false;dom.input.rect.left=-40;assert.throws(()=>assertFocus(dom.run(focusExpression)),/offscreen/);dom.input.rect.left=10;
  const dialog=dom.main.add(new Element("DIV"));dialog.setAttribute("role","dialog");
  assert.throws(()=>assertFocus(dom.run(focusExpression)),/escaped the active dialog/);
});
test("UI-OBSERVER-003: modes require actual media and animation observations",()=>{
  const dom=observerDOM();
  assert.throws(()=>check(dom,"forced-colors"),/observed via matchMedia/);
  assert.throws(()=>check(dom,"reduced-motion"),/observed via matchMedia/);
  dom.context.matchMedia=()=>({matches:true});check(dom,"forced-colors");check(dom,"reduced-motion");
  dom.button.style.transitionDuration="1s";assert.throws(()=>check(dom,"reduced-motion"),/suppress actual animation/);
  dom.button.style.transitionDuration="0s";dom.button.setAttribute("data-chart-motion","normal");dom.button.dataset.chartMotion="normal";
  assert.throws(()=>check(dom,"reduced-motion"),/chart options must disable motion/);
});
test("UI-LAYOUT-001: actual measurement restores every nested scroll even when measurement throws",()=>{
  const dom=observerDOM();dom.main.scrollLeft=13;dom.main.scrollTop=71;dom.body.scrollTop=23;
  const before=[dom.main.scrollLeft,dom.main.scrollTop,dom.body.scrollTop,dom.context.scrollX,dom.context.scrollY];
  assertLayout(dom.run<LayoutObservation>(layoutExpression));
  assert.deepEqual([dom.main.scrollLeft,dom.main.scrollTop,dom.body.scrollTop,dom.context.scrollX,dom.context.scrollY],before);
  dom.button.getBoundingClientRect=()=>{throw Error("measurement interrupted");};
  assert.throws(()=>dom.run(layoutExpression),/measurement interrupted/);
  assert.deepEqual([dom.main.scrollLeft,dom.main.scrollTop,dom.body.scrollTop,dom.context.scrollX,dom.context.scrollY],before);
  assert.equal(dom.document.activeElement,dom.input);
});

test("UI-OBSERVER-004: actual current driver requires reverse order and restores its observed focus anchor",async()=>{
  for (const failure of ["none","reverse-stationary","wrong-order","hidden","inert","body","escaped"]) {
    const {dom,nodes}=loginKeyboardDOM();
    const directions:string[]=[];let index=-1,backward=0;
    const browser={
      async pressTab(direction:"forward"|"backward") {
        directions.push(direction); if(direction==="backward")backward++;
        if(failure!=="reverse-stationary"||direction!=="backward")index=(index+(direction==="forward"?1:2))%nodes.length;
        if(backward===1&&failure==="wrong-order")index=(index+2)%nodes.length;
        dom.document.activeElement=nodes[index];
        if(backward===1&&failure==="hidden")nodes[index].style.visibility="hidden";
        if(backward===1&&failure==="inert")nodes[index].setAttribute("inert","");
        if(backward===1&&failure==="body")dom.document.activeElement=dom.body;
        if(backward===1&&failure==="escaped")dom.main.add(new Element("DIV")).setAttribute("role","dialog");
      },
      async evaluate(expression:string) { return expression.includes("const targets=kind")?null:dom.run(expression); },
    };
    const run=()=>Reflect.apply(exerciseAccessibility,undefined,[browser,keyboardCondition]);
    if(failure==="none") {
      const result=await run();assert.equal(dom.document.activeElement,dom.input);
      assert.deepEqual(directions,[...Array(3).fill("forward"),...Array(2).fill("backward")]);
      assert.equal(result.pending.some((entry:string)=>entry.includes("REVERSE_TAB")),false);
      assert.ok(result.pending.some((entry:string)=>entry.includes("ENTER_SPACE_ACTIVATION_EVIDENCE_PENDING")),"missing activation is not a pass");
    } else {
      await assert.rejects(run(),/Tab must move|reverse Tab|hidden, inert, body|escaped/);
      assert.ok(directions.includes("backward"),"negative must reach the actual reverse input");
    }
  }
});

test("UI-DRIVER-004: actual Stream primary selection excludes six suffix actions and rejects replacement of the marked trigger",async()=>{
  for(const replace of [false,true]) {
    const dom=observerDOM(),name="UI-LONG-TEXT- (safe) [name] + stream";
    dom.button.ownText=name;dom.button.setAttribute("data-slot","stream-primary-trigger");dom.button.setAttribute("data-stream-id","ui-stream-1");
    for(const prefix of ["Start ","Stop ","Edit ","Delete ","Readiness ","Worker "])dom.main.add(new Element("BUTTON",prefix+name));
    let clicks=0;
    const browser={evaluate:async(expression:string)=>dom.run(expression),
      waitFor:async(expression:string,accept:(value:unknown)=>boolean)=>{assert.equal(accept(dom.run(expression)),true);},
      clickAt:async()=>{clicks++;if(replace){dom.button.parentElement=null;dom.main.children=dom.main.children.filter(e=>e!==dom.button);const replacement=dom.main.add(new Element("BUTTON",name));replacement.setAttribute("data-slot","stream-primary-trigger");replacement.setAttribute("data-stream-id","ui-stream-1");}}
    } as unknown as BrowserHarness;
    if(replace)await assert.rejects(clickStreamPrimary(browser,{id:"ui-stream-1",name}),/actual trigger DOM identity/);
    else await clickStreamPrimary(browser,{id:"ui-stream-1",name});
    assert.equal(clicks,1);
  }
});

test("UI-DRIVER-009: native pointer uses the scrolled unique target and rejects changed identity, geometry, availability and hit", async () => {
  for (const failure of ["none", "replacement", "hidden", "disabled", "offscreen", "obstructed", "nonfinite", "duplicate"] as const) {
    const dom=observerDOM(),calls:string[]=[],points:number[][]=[];
    const scroll=dom.button.scrollIntoView.bind(dom.button);dom.button.scrollIntoView=()=>{calls.push("scroll");scroll();};
    const browser={waitFor:async(expression:string,accept:(value:unknown)=>boolean)=>{
      assert.equal(accept(dom.run(expression)),true);
      if(failure==="replacement")dom.button.parentElement=null;
      if(failure==="hidden")dom.button.hidden=true;
      if(failure==="disabled")dom.button.disabled=true;
      if(failure==="offscreen")dom.button.rect.top=1000;
      if(failure==="nonfinite")dom.button.rect.width=NaN;
      if(failure==="obstructed")dom.document.elementFromPoint=()=>dom.input;
      if(failure==="duplicate")dom.main.add(new Element("BUTTON","Other")).setAttribute("data-ui-scenario-target","");
    },evaluate:async(expression:string)=>dom.run(expression),clickAt:async(x:number,y:number)=>{calls.push("native");points.push([x,y]);}} as unknown as BrowserHarness;
    if(failure==="none") {await clickVisible(browser,"button",/^Open$/);assert.deepEqual(points,[[80,22]]);assert.deepEqual(calls,["scroll","native"]);}
    else {await assert.rejects(clickVisible(browser,"button",/^Open$/),/marked trigger/);assert.equal(points.length,0);}
    assert.equal(dom.button.getAttribute("data-ui-scenario-target"),null,"owned marker restored on success and failure");
  }
});
test("UI-DRIVER-010: disabled permission and pending negatives keep a native attempt without enabling the control", async () => {
  const dom=observerDOM();dom.button.disabled=true;let clicks=0;
  const browser={waitFor:async(expression:string,accept:(value:unknown)=>boolean)=>assert.equal(accept(dom.run(expression)),true),evaluate:async(expression:string)=>dom.run(expression),clickAt:async()=>{clicks++;assert.equal(dom.button.disabled,true);}} as unknown as BrowserHarness;
  await clickDisabledVisible(browser,"button");assert.equal(clicks,1);assert.equal(dom.button.disabled,true);
  dom.document.elementFromPoint=()=>dom.input;
  await assert.rejects(clickDisabledVisible(browser,"button"),/obstructed/);assert.equal(clicks,1);
});
test("UI-OBSERVER-005: modal paths require every observed control, bidirectional wrap and no document escape",async()=>{
  const modalCondition=conditions.find(row=>row.exercise==="Confirmation")!;
  for(const fault of ["none","skip","escape","missing"] as const) {
    const dom=observerDOM(),dialog=dom.body.add(new Element("DIV"));dialog.setAttribute("role","alertdialog");
    const nodes=["Cancel","Secondary","Submit"].map(name=>dialog.add(new Element("BUTTON",name)));
    if(fault==="missing")nodes[0].disabled=true;
    let index=-1,steps=0;const traces:unknown[]=[];
    const browser={evaluate:async(expression:string)=>dom.run(expression),pressTab:async(direction:"forward"|"backward")=>{
      steps++;index=(index+(direction==="forward"?(fault==="skip"&&steps===2?2:1):nodes.length-1))%nodes.length;
      dom.document.activeElement=fault==="escape"&&steps===2?dom.body:nodes[index];
    }} as unknown as BrowserHarness;
    if(fault==="none") {
      const result=await exerciseAccessibility(browser,modalCondition,value=>traces.push(value));
      assert.equal(steps,8);assert.equal(dom.document.activeElement,nodes[2]);assert.ok(result.pending.length>0);
      assert.equal(traces.length,1);
    } else await assert.rejects(exerciseAccessibility(browser,modalCondition),/skipped or reordered|hidden, inert, body|required keyboard target/);
  }
});
test("UI-OBSERVER-006: single-control document reverse boundary and unobservable media remain distinct from modal escape",async()=>{
  for(const native of [false,true]) {
    const dom=observerDOM();dom.main.children=dom.main.children.filter(e=>e.tagName==="H1");
    const target=dom.main.add(new Element(native?"A":"BUTTON",native?"Open directly":"Refresh"));
    if(native){target.setAttribute("href","/archive-shares/ui-synthetic-share/download");dom.main.add(new Element("VIDEO"));}
    const current=conditions.find(row=>row.family===(native?"public-archive-share":"monitoring")&&row.exercise==="keyboard")!;
    let steps=0;
    const browser={evaluate:async(expression:string)=>dom.run(expression),pressTab:async(direction:string)=>{steps++;dom.document.activeElement=direction==="backward"?dom.body:target;}} as unknown as BrowserHarness;
    const result=await exerciseAccessibility(browser,current);
    assert.equal(steps,3);assert.equal(dom.document.activeElement,target);
    assert.equal(result.pending.some(value=>value.startsWith("UA_KEYBOARD")),native);
    assert.ok(result.pending.length,"missing native/activation proof is not PASS");
  }
});
test("UI-OBSERVER-007: decorative shadows are not focus indicators; forced colors needs an observed outline",()=>{
  const dom=observerDOM();dom.input.style.outlineStyle="none";dom.input.style.boxShadow="0 3px 12px rgb(0,0,0)";
  assert.throws(()=>assertFocus(dom.run(focusExpression)),/indicator absent/);
  dom.input.style.getPropertyValue=()=>"0 0 0 3px rgb(0,0,255)";
  assertFocus(dom.run(focusExpression));
  dom.context.matchMedia=()=>({matches:true});assert.throws(()=>assertFocus(dom.run(focusExpression)),/indicator absent/);
  dom.input.style.outlineStyle="solid";dom.input.style.outlineWidth="2px";assertFocus(dom.run(focusExpression));
});
test("UI-OBSERVER-008: keyboard trace is condition-labelled, bounded and excludes uncontrolled focus text",()=>{
  const current=conditions.find(row=>row.family==="login"&&row.exercise==="keyboard")!;
  const focus={id:"SECRET_REQUEST_VALUE",visible:true,inside:true,indicator:true,boundary:false,native:false,rect:[1,2,3,4],required:0,modal:false};
  const result=safeKeyboardTrace(current,Array.from({length:128},()=>({direction:"forward" as const,stage:"path" as const,focus})));
  assert.equal(result.condition,current.id);assert.equal(result.totalSteps,128);
  assert.ok(Buffer.byteLength(JSON.stringify(result))<=4096);assert.doesNotMatch(JSON.stringify(result),/SECRET_REQUEST_VALUE/);
  assert.ok(result.entries.every(entry=>entry.id==="OTHER"));
});

test('UI-TABPANEL-017: emitted focus observer accepts only a mutually linked active tall panel with visible focus edges',()=>{
 for(const fault of ['none','button','div','body','inactive','hidden','inert','wrong-role','wrong-slot','wrong-owner','wrong-reference','duplicate-panel','duplicate-tab','disabled-tab','unselected','offscreen','covered','clipped','tiny-band','indicator']){
  const dom=observerDOM(),root=dom.main.add(new Element('DIV'));root.setAttribute('data-slot','tabs');
  const tab=root.add(new Element('BUTTON','Profile'));tab.id='profile-tab';tab.setAttribute('role','tab');tab.setAttribute('aria-selected','true');tab.setAttribute('aria-controls','profile-panel');
  const panel=root.add(new Element(fault==='button'?'BUTTON':'DIV'));panel.id='profile-panel';panel.setAttribute('role','tabpanel');panel.setAttribute('data-slot','tabs-content');panel.setAttribute('data-state','active');panel.setAttribute('aria-labelledby',tab.id);panel.setAttribute('tabindex','0');
  panel.rect={left:10,right:950,top:100,bottom:1550,width:940,height:1450};dom.document.activeElement=panel;dom.document.elementFromPoint=()=>panel;
  if(fault==='button'||fault==='div')panel.removeAttribute('role');if(fault==='body')dom.document.activeElement=dom.body;
  if(fault==='inactive')panel.setAttribute('data-state','inactive');if(fault==='hidden')panel.hidden=true;if(fault==='inert')root.setAttribute('inert','');
  if(fault==='wrong-role')panel.setAttribute('role','region');if(fault==='wrong-slot')panel.setAttribute('data-slot','other');
  if(fault==='wrong-owner'){root.children=root.children.filter(e=>e!==tab);dom.main.add(tab);}
  if(fault==='wrong-reference')tab.setAttribute('aria-controls','other');
  if(fault==='duplicate-panel')dom.main.add(new Element('DIV')).id=panel.id;if(fault==='duplicate-tab')dom.main.add(new Element('BUTTON')).id=tab.id;
  if(fault==='disabled-tab')tab.disabled=true;if(fault==='unselected')tab.setAttribute('aria-selected','false');
  if(fault==='offscreen'){panel.rect.left=-10;panel.rect.right=930;}if(fault==='covered')dom.document.elementFromPoint=()=>dom.button;
  if(fault==='clipped'){root.style.overflowX='hidden';root.rect.right=500;}if(fault==='tiny-band'){panel.rect.top=899;panel.rect.bottom=2349;}
  if(fault==='indicator')panel.style.outlineStyle='none';
  const value=dom.run<Parameters<typeof assertFocus>[0]>(focusExpression);
  if(fault==='none'){assertFocus(value);assert.equal(value.rect[3],1450);}else assert.throws(()=>assertFocus(value),/hidden|offscreen|indicator/,fault);
 }
});
