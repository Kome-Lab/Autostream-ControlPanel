import assert from "node:assert/strict";
import test from "node:test";
import type { BrowserHarness } from "../helpers/browser-harness.mts";
import { Element, observerDOM } from "./observer-dom.mts";
import { beginRenderState, renderStateExpression, assertSameRenderState, closeOverlay, settleRender, renderedDOM } from "./render-state.mts";
import { observationExpression, assertObservation, type UIObservation } from "./observation.mts";
import { layoutExpression, assertLayout } from "./layout-observation.mts";
import { conditions } from "./matrix.mts";

const condition = { ...conditions[0], locale: "en", mode: "light", theme: "autostream" };
function overlay() {
  const dom = observerDOM(), dialog = dom.body.add(new Element("DIV"));
  dialog.setAttribute("role", "dialog"); dialog.setAttribute("data-state", "open");
  dialog.add(new Element("BUTTON", "Close"));
  return { ...dom, dialog };
}
function finiteAnimation() {
  let finish!: () => void, cancel!: () => void;
  const animation = { playState: "running", currentTime: 0, effect: { getComputedTiming: () => ({ endTime: 150 }) },
    finished: new Promise<void>((resolve, reject) => { finish = resolve; cancel = () => reject(new Error("cancelled")); }) };
  return { animation, finish, cancel };
}
const probe = (dom: ReturnType<typeof observerDOM>) => dom.run<{ settled: boolean }>(renderStateExpression).settled;
const tick = async () => { await Promise.resolve(); await Promise.resolve(); };

test("UI-RENDER-001: emitted observation distinguishes closed details, visible peers and full-document secrets", () => {
  const dom = observerDOM(), details = dom.main.add(new Element("DETAILS"));
  details.add(new Element("SUMMARY", "Columns"));
  const hiddenColumn = details.add(new Element("INPUT")); hiddenColumn.type = "checkbox";
  const observe = () => dom.run<UIObservation>(observationExpression);
  assertObservation(observe(), condition);
  details.open = true;
  assert.throws(() => assertObservation(observe(), condition), /input label/);
  hiddenColumn.setAttribute("aria-label", "Name column"); assertObservation(observe(), condition);
  const field = dom.main.add(new Element("DIV")), peer = field.add(new Element("BUTTON", "Page size")); peer.setAttribute("role", "combobox");
  const proxy = field.add(new Element("SELECT")); proxy.setAttribute("aria-hidden", "true"); proxy.setAttribute("tabindex", "-1");
  peer.setAttribute("aria-label", "Page size");
  Object.assign(proxy.style, { width: "1px", height: "1px", position: "absolute", overflow: "hidden", clip: "rect(0px, 0px, 0px, 0px)" });
  Object.assign(proxy.rect, { width: 1, height: 1 });
  assertObservation(observe(), condition); assert.equal(observe().controls.filter(c => c.tag === "SELECT").length, 0);
  peer.hidden = true; assert.throws(() => assertObservation(observe(), condition), /references/);
  peer.hidden = false; proxy.style.position = "static"; assert.throws(() => assertObservation(observe(), condition), /references/);
  proxy.hidden = true; const secret = details.add(new Element("SPAN", "B9-SYNTHETIC-RECOVERY")); secret.hidden = true; details.open = false;
  assert.throws(() => assertObservation(observe(), condition), /secret must not leak/);
});

test("UI-RENDER-002: active dialog controls are measured while hidden backgrounds cannot conceal diagnostics", () => {
  const dom = overlay(); dom.button.ownText = "";
  const observe = () => dom.run<UIObservation>(observationExpression);
  assert.equal(observe().controls.length, 1); assertObservation(observe(), condition);
  dom.dialog.children[0].ownText = ""; assert.throws(() => assertObservation(observe(), condition), /unnamed/);
  dom.dialog.children[0].ownText = "Close";
  dom.main.add(new Element("DIV", "UI-HIDDEN-DIAGNOSTIC")).hidden = true;
  assert.throws(() => assertObservation(observe(), condition), /diagnostic/);
});

test('UI-RENDER-009: one CSS pixel proxy keeps its labelled visible peer at 1x/2x and rejects broad hidden-control exemptions',()=>{
  for(const scale of [1,2]){
    const dom=observerDOM(),field=dom.main.add(new Element('DIV')),peer=field.add(new Element('BUTTON','Choice')),proxy=field.add(new Element('SELECT'));
    peer.setAttribute('role','combobox');peer.setAttribute('aria-label','Choice');proxy.setAttribute('aria-hidden','true');proxy.setAttribute('tabindex','-1');
    Object.assign(proxy.style,{width:'1px',height:'1px',position:'absolute',overflow:'hidden',clip:'rect(0px, 0px, 0px, 0px)'});Object.assign(proxy.rect,{width:scale,height:scale});
    const classify=()=>dom.run<boolean>(`(()=>{${renderedDOM}return uiProxy(document.querySelector('select'));})()`);
    assert.equal(classify(),true);assertObservation(dom.run(observationExpression),condition);
    for(const fault of ['peer-absent','peer-unlabelled','peer-duplicate','tabbable','real-size','no-clip']){
      const old={...proxy.style};let extra:Element|undefined;
      if(fault==='peer-absent')peer.hidden=true;
      if(fault==='peer-unlabelled')peer.removeAttribute('aria-label');
      if(fault==='peer-duplicate'){extra=field.add(new Element('BUTTON','Other'));extra.setAttribute('role','combobox');extra.setAttribute('aria-label','Other');}
      if(fault==='tabbable')proxy.setAttribute('tabindex','0');
      if(fault==='real-size')Object.assign(proxy.style,{width:'100px',height:'40px'});
      if(fault==='no-clip')proxy.style.clip='auto';
      assert.equal(classify(),false,fault);assert.throws(()=>assertObservation(dom.run(observationExpression),condition));
      peer.hidden=false;peer.setAttribute('aria-label','Choice');if(extra)field.children=field.children.filter(e=>e!==extra);
      proxy.setAttribute('tabindex','-1');Object.assign(proxy.style,old);
    }
  }
});

test("UI-RENDER-003: finite completion and stable geometry are both required; infinite child motion is not awaited", async () => {
  const dom = overlay(), finite = finiteAnimation(); dom.dialog.animations = [finite.animation];
  const skeleton = dom.dialog.add(new Element("SPAN")); skeleton.animations = [{ ...finite.animation, effect: { getComputedTiming: () => ({ endTime: Infinity }) } }];
  dom.run(beginRenderState("open")); assert.equal(probe(dom), false);
  finite.animation.playState = "finished"; finite.animation.currentTime = 150;
  assert.equal(probe(dom), false, "playState alone is not the finished promise");
  finite.finish(); await tick(); assert.equal(probe(dom), false);
  dom.dialog.rect.left++; assert.equal(probe(dom), false);
  assert.equal(probe(dom), false); assert.equal(probe(dom), true);
  const before = { x: dom.context.scrollX, y: dom.context.scrollY, left: dom.dialog.scrollLeft, top: dom.dialog.scrollTop };
  assertLayout(dom.run(layoutExpression));
  assert.deepEqual({ x: dom.context.scrollX, y: dom.context.scrollY, left: dom.dialog.scrollLeft, top: dom.dialog.scrollTop }, before);
  assert.equal(dom.run(assertSameRenderState), true);
  dom.dialog.rect.left++; assert.throws(() => dom.run(assertSameRenderState), /geometry changed/);
});

test("UI-RENDER-004: cancelled, early-finished, replaced and zero-geometry owners fail the actual expression", async () => {
  for (const fault of ["cancel", "early", "replace", "zero"] as const) {
    const dom = overlay(), finite = finiteAnimation(); dom.dialog.animations = [finite.animation];
    dom.run(beginRenderState("open")); probe(dom);
    if (fault === "cancel") finite.cancel();
    if (fault === "early") { finite.animation.currentTime = 20; finite.finish(); }
    if (fault === "replace") { dom.dialog.parentElement = null; dom.body.children.pop(); const replacement = dom.body.add(new Element("DIV")); replacement.setAttribute("role", "dialog"); }
    if (fault === "zero") dom.html.rect.width = 0;
    await tick();
    if (fault === "zero") { dom.dialog.rect.width = 0; assert.throws(() => probe(dom), /replaced|geometry/); }
    else assert.throws(() => probe(dom), /cancelled|replaced/);
  }
});

test("UI-RENDER-005: close driver retains the settled owner before Escape and observes its exit", async () => {
  const dom = overlay(), finite = finiteAnimation();
  dom.run(beginRenderState("open")); probe(dom); probe(dom); assert.equal(probe(dom), true);
  let keys = 0, polls = 0;
  const browser = { evaluate: async (expression: string) => dom.run(expression), assertNoFatalError() {},
    pressKey: async (key: string) => { assert.equal(key, "Escape"); keys++; assert.equal((dom.context as unknown as { __uiRenderState: { owner: Element } }).__uiRenderState.owner, dom.dialog); dom.dialog.animations = [finite.animation]; dom.dialog.setAttribute("data-state", "closed"); },
    waitFor: async (expression: string, accept: (value: unknown) => boolean) => {
      for (let i = 0; i < 8; i++) { polls++; const value = dom.run(expression); if (accept(value)) return value;
        if (i === 0) { finite.animation.currentTime = 150; finite.animation.playState = "finished"; finite.finish(); dom.dialog.hidden = true; await tick(); }
      } throw Error("exit not settled");
    } } as unknown as BrowserHarness;
  await closeOverlay(browser); assert.equal(keys, 1); assert.ok(polls >= 3); assert.equal(dom.run(assertSameRenderState), true);
  await assert.rejects(closeOverlay(browser), /previously observed overlay/);
});

test("UI-RENDER-006: failing driver reports bounded stage/geometry while preserving the original observer error",async()=>{
  const original=new Error("finite overlay animation cancelled"),records:unknown[]=[];
  const browser={evaluate:async(expression:string)=>{if(expression.includes("const s=globalThis.__uiRenderState"))return {observed:true,connected:true,stable:0,rect:[1,2,3,4]};throw original;}} as unknown as BrowserHarness;
  await assert.rejects(settleRender(browser,"open",value=>records.push(value)),error=>error===original);
  assert.deepEqual(records,[{stage:"finite-overlay-geometry",expected:"open",geometry:{observed:true,connected:true,stable:0,rect:[1,2,3,4]}}]);
  await assert.rejects(settleRender(browser,"open",()=>{throw Error("writer failed");}),error=>error===original);
});

test("UI-RENDER-007: actual clipping, offscreen controls, missing restoration and all-hidden observations still fail",()=>{
  for(const fault of ["clip","offscreen","restore","all-hidden","unnamed-widget"]) {
    const dom=observerDOM();
    if(fault==="clip"){dom.button.style.overflowX="hidden";dom.button.scrollWidth=400;}
    if(fault==="offscreen"){dom.main.style.overflowX="hidden";dom.button.rect.left=2000;dom.button.rect.right=2140;}
    if(fault==="restore"){let value=71;Object.defineProperty(dom.main,"scrollTop",{get:()=>value,set:(next:number)=>{if(next!==71)value=next;}});}
    if(fault==="all-hidden")dom.main.style.display="none";
    if(fault==="unnamed-widget"){const widget=dom.main.add(new Element("DIV"));widget.setAttribute("role","combobox");assert.throws(()=>assertObservation(dom.run(observationExpression),condition),/unnamed/);continue;}
    assert.throws(()=>assertLayout(dom.run(layoutExpression)),/clipped|unreachable|restored|zero layout/);
  }
});

test("UI-RENDER-008: explicit and automatic opening retain a transparent connected owner until actual paint, finite completion and geometry", async () => {
  for (const expected of ["open", undefined] as const) {
    const dom = overlay(), finite = finiteAnimation(); dom.dialog.style.opacity = "0"; dom.dialog.animations = [finite.animation];
    let polls = 0, finished = false;
    const browser = { evaluate: async (expression: string) => dom.run(expression), assertNoFatalError() {},
      waitFor: async (expression: string, accept: (value: unknown) => boolean, description: string, ...timeouts: unknown[]) => {
        assert.equal(description, "finite overlay animation and geometry settlement"); assert.deepEqual(timeouts, [], "keep the original harness deadline");
        assert.equal(dom.run(`(() => {${renderedDOM}return uiDialogs().length;})()`), 0, "transparent controls remain non-interactive");
        assert.equal((dom.context as unknown as { __uiRenderState: { owner: Element } }).__uiRenderState.owner, dom.dialog);
        for (let index = 0; index < 8; index++) {
          polls++; const result = dom.run(expression);
          if (accept(result)) { assert.equal(finished, true); assert.equal(dom.dialog.style.opacity, "1"); return result; }
          if (index === 0) dom.dialog.style.opacity = "1";
          if (index === 1) { finite.animation.currentTime = 150; finite.animation.playState = "finished"; finite.finish(); finished = true; await tick(); }
        }
        throw Error("original deadline");
      } } as unknown as BrowserHarness;
    await settleRender(browser, expected); assert.ok(polls >= 4);
    assert.equal(dom.run(assertSameRenderState), true);
    assert.equal((dom.context as unknown as { __uiSettledOverlay: Element }).__uiSettledOverlay, dom.dialog);
  }
});

test("UI-RENDER-009: both real opening paths reject permanent transparency, closed/replaced/missing owners, cancellation, nonfinite waits and cleanup failure", async () => {
  for (const expected of ["open", undefined] as const) for (const fault of ["transparent", "closed", "replace", "missing", "cancel", "infinite", "timeout", "cleanup"] as const) {
    const dom = overlay(), finite = finiteAnimation(); dom.dialog.style.opacity = "0"; dom.dialog.animations = [finite.animation];
    const deadline = new Error("original deadline"), cleanup = new Error("wait cleanup failed"); let polls = 0;
    const browser = { evaluate: async (expression: string) => dom.run(expression), assertNoFatalError() {},
      waitFor: async (expression: string, accept: (value: unknown) => boolean) => {
        for (let index = 0; index < 8; index++) {
          polls++; const result = dom.run(expression);
          if (accept(result)) { if (fault === "cleanup") throw cleanup; return result; }
          if (index === 0) {
            if (fault !== "transparent") dom.dialog.style.opacity = "1";
            if (fault === "closed") dom.dialog.setAttribute("data-state", "closed");
            if (fault === "replace" || fault === "missing") {
              dom.body.children = dom.body.children.filter(e => e !== dom.dialog); dom.dialog.parentElement = null;
              if (fault === "replace") { const next = dom.body.add(new Element("DIV")); next.setAttribute("role", "dialog"); next.setAttribute("data-state", "open"); }
            }
            if (fault === "infinite") finite.animation.effect.getComputedTiming = () => ({ endTime: Infinity });
            if (fault === "cancel") finite.cancel();
            else if (fault !== "timeout") { finite.animation.currentTime = 150; finite.animation.playState = "finished"; finite.finish(); }
            await tick();
          }
        }
        throw deadline;
      } } as unknown as BrowserHarness;
    await assert.rejects(settleRender(browser, expected), error => fault === "transparent" || fault === "timeout" ? error === deadline : fault === "cleanup" ? error === cleanup : /cancelled|replaced|non-finite/.test(String(error)));
    assert.ok(polls > 0, "negative reaches the actual opening wait");
    if (fault === "transparent" || fault === "timeout") assert.equal(polls, 8);
  }
});

test("UI-RENDER-010: auto page ignores unrelated closed hidden elements and capture rejects a newly opening owner", async () => {
  const dom = observerDOM(), hidden = dom.body.add(new Element("DIV")); hidden.setAttribute("role", "dialog"); hidden.setAttribute("data-state", "closed"); hidden.hidden = true;
  const browser = { evaluate: async (expression: string) => dom.run(expression), assertNoFatalError() {},
    waitFor: async (expression: string, accept: (value: unknown) => boolean) => { for (let i = 0; i < 5; i++) { const result = dom.run(expression); if (accept(result)) return result; } throw Error("original deadline"); } } as unknown as BrowserHarness;
  await settleRender(browser); assert.equal(dom.run(assertSameRenderState), true);
  hidden.hidden = false; hidden.style.opacity = "0"; hidden.setAttribute("data-state", "open");
  assert.throws(() => dom.run(assertSameRenderState), /unexpected overlay/);
  hidden.setAttribute("data-state", "closed"); await assert.rejects(settleRender(browser, "open"), /expected overlay not reached/);
});
