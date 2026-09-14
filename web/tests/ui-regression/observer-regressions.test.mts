import assert from "node:assert/strict";
import test from "node:test";
import { observerDOM, Element } from "./observer-dom.mts";
import { observationExpression, assertObservation, focusExpression, assertFocus, exerciseAccessibility, safeKeyboardTrace, type UIObservation } from "./observation.mts";
import { layoutExpression, assertLayout, type LayoutObservation } from "./layout-observation.mts";
import { visibleTriggerExpression, clickVisible, clickStreamPrimary } from "./visible-trigger.mts";
import { assertPageText, exerciseTableSort } from "./table-browser.mts";
import { conditions } from "./matrix.mts";
import type { BrowserHarness } from "../helpers/browser-harness.mts";
const condition={...conditions[0],locale:"en" as const,mode:"light" as const,theme:"autostream" as const};
const check=(dom:ReturnType<typeof observerDOM>,exercise?:string)=>assertObservation(dom.run<UIObservation>(observationExpression),{...condition,exercise} as typeof condition);
const keyboardCondition=conditions.find(row=>row.family==="login"&&row.exercise==="keyboard")!;
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
      evaluate:async(expression:string)=>{visited.push(expression);if(expression.includes("row")&&expression.includes("tbody"))return ["Z","A"];
        if(expression.includes("tbody tr td"))return ["Z","A"];
        if(expression.includes("findIndex"))return 0;if(expression.includes("streams.sort"))return "name";
        if(expression.includes("const e=document.querySelector"))return desktop;return true;},
      waitFor:async(expression:string,predicate:(value:unknown)=>boolean)=>{visited.push(expression);assert.ok(predicate(expression.includes("aria-sort")?direction:expression.includes("?.value")?"name":true));},
      clickSelector:async()=>{},pressKey:async()=>{},pressNativeKey:async()=>{},
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
      async evaluate(expression:string) { return expression.includes("const targets=sections")?null:dom.run(expression); },
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
      clickSelector:async()=>{clicks++;if(replace){dom.button.parentElement=null;dom.main.children=dom.main.children.filter(e=>e!==dom.button);const replacement=dom.main.add(new Element("BUTTON",name));replacement.setAttribute("data-slot","stream-primary-trigger");replacement.setAttribute("data-stream-id","ui-stream-1");}}
    } as unknown as BrowserHarness;
    if(replace)await assert.rejects(clickStreamPrimary(browser,{id:"ui-stream-1",name}),/actual trigger DOM identity/);
    else await clickStreamPrimary(browser,{id:"ui-stream-1",name});
    assert.equal(clicks,1);
  }
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
