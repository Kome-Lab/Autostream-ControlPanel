import assert from "node:assert/strict";
import test from "node:test";
import { observerDOM, Element } from "./observer-dom.mts";
import { observationExpression, assertObservation, focusExpression, assertFocus, exerciseAccessibility, type UIObservation } from "./observation.mts";
import { layoutExpression, assertLayout, type LayoutObservation } from "./layout-observation.mts";
import { visibleTriggerExpression, clickVisible } from "./visible-trigger.mts";
import { assertPageText, exerciseTableSort } from "./table-browser.mts";
import { conditions } from "./matrix.mts";
import type { BrowserHarness } from "../helpers/browser-harness.mts";
const condition={...conditions[0],locale:"en" as const,mode:"light" as const,theme:"autostream" as const};
const check=(dom:ReturnType<typeof observerDOM>,exercise?:string)=>assertObservation(dom.run<UIObservation>(observationExpression),{...condition,exercise} as typeof condition);

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
  const dom=observerDOM();
  const observed=dom.run<Parameters<typeof assertFocus>[0]>(focusExpression);assertFocus(observed);
  let keys=0;
  const browser={pressTab:async()=>{keys++;},evaluate:async(expression:string)=>dom.run(expression)} as unknown as BrowserHarness;
  await assert.rejects(exerciseAccessibility(browser,{...condition,exercise:"keyboard"}),/Tab must move/);assert.equal(keys,2);
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
    const dom=observerDOM(), nodes=[dom.input,dom.button,dom.main.add(new Element("BUTTON","Second"))];
    const directions:string[]=[];let index=0,backward=0;
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
    const run=()=>Reflect.apply(exerciseAccessibility,undefined,[browser,{...condition,exercise:"keyboard"}]);
    if(failure==="none") {
      const result=await run();assert.equal(dom.document.activeElement,dom.button);
      assert.deepEqual(directions,[...Array(13).fill("forward"),...Array(12).fill("backward")]);
      assert.equal(result.pending.some((entry:string)=>entry.includes("REVERSE_TAB")),false);
      assert.ok(result.pending.some((entry:string)=>entry.includes("ENTER_SPACE_ACTIVATION_EVIDENCE_PENDING")),"missing activation is not a pass");
    } else {
      await assert.rejects(run(),/Tab must move|reverse Tab|hidden, inert, body|escaped/);
      assert.ok(directions.includes("backward"),"negative must reach the actual reverse input");
    }
  }
});
