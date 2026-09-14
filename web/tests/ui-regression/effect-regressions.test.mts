import assert from "node:assert/strict";
import {readFileSync} from "node:fs";
import test from "node:test";
import {actualEffect,actualJSXCallback} from "./source-callback.mts";
import {observerDOM,Element} from "./observer-dom.mts";
import {activationObservation,assertActivation,exerciseActivation} from "./keyboard-activation.mts";
import type {BrowserHarness} from "../helpers/browser-harness.mts";
test("UI-MOTION-001: actual chart effect suppresses animation, responds to mode changes and removes its listener",()=>{
 const source=readFileSync(new URL("../../src/components/charts/echarts-panel.tsx",import.meta.url),"utf8");
 const option={animation:true,animationDuration:1200,animationDurationUpdate:600};
 let listener:(()=>void)|undefined;const media={matches:true,addEventListener(_name:string,next:()=>void){listener=next;},removeEventListener(_name:string,next:()=>void){assert.equal(next,listener);listener=undefined;}};
 const applied:typeof option[]=[];const ref={current:{dataset:{chartMotion:""}}};
 const bindings={option,window:{matchMedia:()=>media},chartRef:{current:{setOption(value:typeof option){applied.push(value);}}},ref};
 const cleanup=actualEffect(source,"matchMedia",bindings)();assert.equal(applied.at(-1)?.animation,false);assert.equal(applied.at(-1)?.animationDuration,0);assert.equal(ref.current.dataset.chartMotion,"reduced");
 media.matches=false;listener?.();assert.deepEqual(applied.at(-1),option);cleanup();assert.equal(listener,undefined);
 const mutant=source.replace('animation: reduced.matches ? false : motion.animation ?? true','animation: true');
 media.matches=true;actualEffect(mutant,"matchMedia",bindings)();assert.throws(()=>assert.equal(applied.at(-1)?.animation,false),/true !== false/);
});
test("UI-KEYBOARD-001: actual section callback and observation reject disconnected focus and double activation",()=>{
 const source=readFileSync(new URL("../../src/components/layout/detail-section.tsx",import.meta.url),"utf8");
 const dom=observerDOM();const section=dom.main.add(new Element("SECTION","Overview"));section.id="overview";
 Object.assign(section,{focus(){dom.document.activeElement=section;}});
 const click=actualJSXCallback(source,"button","onClick",{document:dom.document,item:{id:"overview"}});
 const state={target:dom.button,section,clicks:0,initialOpen:false};Object.assign(dom.context,{__uiKeyboardActivation:state});
 for(let step=1;step<=2;step++){click();state.clicks++;assertActivation(dom.run(activationObservation),"section",step);}
 dom.document.activeElement=dom.button;assert.throws(()=>assertActivation(dom.run(activationObservation),"section",2),/move focus/);
 state.clicks=3;assert.throws(()=>assertActivation(dom.run(activationObservation),"section",2),/exactly once/);
});
test("UI-KEYBOARD-002: actual native activation runner observes both keys and rejects a no-op operation",async()=>{
 const dom=observerDOM();const section=dom.main.add(new Element("SECTION","Overview"));
 const state={target:dom.button,section,clicks:0,initialOpen:false};Object.assign(dom.context,{__uiKeyboardActivation:state});
 const keys:string[]=[];
 const browser={evaluate:async(expression:string)=>expression===activationObservation?dom.run(expression):expression.includes("ambiguous")?"section":true,pressNativeKey:async(key:string)=>{keys.push(key);state.clicks++;},waitFor:async(_expression:string,predicate:(n:number)=>boolean)=>{assert.ok(predicate(state.clicks));}} as unknown as BrowserHarness;
 await assert.rejects(exerciseActivation(browser),/move focus/);assert.deepEqual(keys,["Enter"]);
 keys.length=0;state.clicks=0;dom.document.activeElement=section;await exerciseActivation(browser);assert.deepEqual(keys,["Enter","Space"]);
});
