import assert from "node:assert/strict";
import test from "node:test";
import { createHarnessFixture } from "../helpers/browser-cdp-socket-fixture.mts";
import { nativeKeyTypeDiagnostics, formatDiagnostic } from "../helpers/browser-native-input-oracle.mts";
import { observerDOM, Element } from './observer-dom.mts';
import { prepareActivation, exerciseActivation } from './keyboard-activation.mts';
import { keyboardContract, exerciseAccessibility, prepareKeyboardExpression } from './observation.mts';
import { conditions } from './matrix.mts';
import { actualJSXCallback } from './source-callback.mts';
import { readFileSync } from 'node:fs';
import type { BrowserHarness } from '../helpers/browser-harness.mts';
import './component-loader.mts';
import {createElement} from 'react';
import {renderUI} from './render-ui.mts';

test('UI-DATETIME-013: actual exercise and harness Tab socket distinguish host repeats, both exits and pending internal identity',async()=>{
 const condition=conditions.find(c=>c.family==='stream-create-edit'&&c.exercise==='keyboard')!;
 for(const fault of ['none','forward-trap','backward-trap','ordinary-text','ordinary-button','value','replacement','hidden','owner','escape','order','indicator']){
   const dom=observerDOM(),dialog=dom.body.add(new Element('DIV'));dialog.setAttribute('role','dialog');
   const query=dialog.querySelectorAll.bind(dialog);dialog.querySelectorAll=(selector)=>query(selector).sort((a,b)=>dialog.all().indexOf(a)-dialog.all().indexOf(b));
   const form=dialog.add(new Element('DIV'));form.id='create-stream';const content=form.add(new Element('DIV'));content.setAttribute('data-slot','card-content');const nav=content.add(new Element('NAV'));nav.setAttribute('data-slot','section-navigation');
   const first=nav.add(new Element('BUTTON','Basic')),date=dialog.add(new Element(fault==='ordinary-button'?'BUTTON':'INPUT')),last=dialog.add(new Element('BUTTON','Close'));first.setAttribute('aria-controls','create-stream-basic');const section=dialog.add(new Element('SECTION'));section.id='create-stream-basic';
   date.type=fault==='ordinary-text'?'text':'datetime-local';date.value='2026-09-01T12:34';date.setAttribute('aria-label','Schedule');const nodes=[first,date,last];
   const owner=createHarnessFixture(),nativeTab=owner.harness.pressTab.bind(owner.harness),trace:unknown[]=[];let index=-1,remaining=0,activation=0,repeatForward=0,repeatBackward=0;
   owner.harness.evaluate=async<T,>(expression:string)=>{if(expression.includes('const targets=kind'))activation++;return dom.run<T>(expression);};
   owner.harness.pressTab=async(direction)=>{
     await nativeTab(direction);if(dom.document.activeElement===section)index=-1;const wasDate=dom.document.activeElement===date;
     if(wasDate){if(direction==='forward')repeatForward++;else repeatBackward++;}
     const trapped=wasDate&&(fault===direction+'-trap'||fault==='ordinary-text'||fault==='ordinary-button');
     if(wasDate&&(remaining>0||trapped))remaining--;else{index=(index+(direction==='forward'?1:nodes.length-1))%nodes.length;if(fault==='order'&&index===1)index=2;dom.document.activeElement=nodes[index];remaining=index===1?6:0;}
     if(wasDate&&fault==='value')date.value='another value';
     if(wasDate&&fault==='replacement'){date.parentElement=null;const other=new Element('INPUT');other.type='datetime-local';other.value=date.value;other.parentElement=dialog;dialog.children[1]=other;dom.document.activeElement=other;}
     if(wasDate&&fault==='hidden')date.hidden=true;if(wasDate&&fault==='owner')dialog.parentElement=null;
     if(wasDate&&fault==='escape')dom.document.activeElement=dom.body;if(wasDate&&fault==='indicator')date.style.outlineStyle='none';
   };
   const activate=actualJSXCallback(readFileSync(new URL('../../src/components/layout/detail-section.tsx',import.meta.url),'utf8'),'button','onClick',{document:dom.document,item:{id:'create-stream-basic'}}),nativeKey=owner.harness.pressNativeKey.bind(owner.harness);
   owner.harness.pressNativeKey=async key=>{await nativeKey(key);first.dispatchEvent(new Event('click'));activate();};owner.harness.waitFor=async<T,>(expression:string,predicate:(value:T)=>boolean)=>{const value=dom.run<T>(expression);assert.ok(predicate(value));return value;};
   try{
     if(fault==='none'){const result=await exerciseAccessibility(owner.harness,condition,value=>trace.push(value));assert.ok(result.pending.includes('UA_DATETIME_SEGMENT_IDENTITY_PENDING'));assert.equal(result.pending.some(p=>p.startsWith('ENTER_SPACE')),false);assert.ok(activation>0);assert.equal(date.value,'2026-09-01T12:34');assert.ok(repeatForward>=7&&repeatBackward>=7);}
     else await assert.rejects(exerciseAccessibility(owner.harness,condition,value=>trace.push(value)),/datetime|Tab must move|escaped|hidden|indicator|reordered/);
     const commands=owner.socket.commandsFor('Input.dispatchKeyEvent').filter(c=>c.params.key==='Tab');assert.ok(commands.length<=256);assert.equal(commands.length%2,0);
     for(let i=0;i<commands.length;i+=2){assert.equal(commands[i].params.type,'keyDown');assert.equal(commands[i+1].params.type,'keyUp');assert.equal(commands[i].params.key,'Tab');assert.ok([0,8].includes(Number(commands[i].params.modifiers)));}
     if(fault==='forward-trap')assert.equal(repeatForward,16);if(fault==='backward-trap')assert.equal(repeatBackward,16);
     assert.equal(trace.length,1);assert.ok(Buffer.byteLength(JSON.stringify(trace[0]))<=4096);assert.doesNotMatch(JSON.stringify(trace),/2026-09|another value|Schedule/);
     assert.equal('__uiKeyboardPlan' in dom.context,false);
   }finally{await owner.harness.close();}
 }
});
test('UI-KEYBOARD-REFRESH-013: real required contract excludes both Updated sort buttons and rejects wrong or ambiguous owner',()=>{
 const condition=conditions.find(c=>c.family==='streams-list'&&c.exercise==='keyboard')!,contract=keyboardContract(condition);
 assert.equal(contract.required[1].selector,'main [data-slot=page-actions-secondary] button');
 for(const fault of ['none','hidden','disabled','wrong-owner','duplicate']){
   const dom=observerDOM();dom.main.children=[];const primary=dom.main.add(new Element('DIV'));primary.setAttribute('data-slot','page-actions-primary');primary.add(new Element('BUTTON','Create'));
   const secondary=dom.main.add(new Element('DIV'));secondary.setAttribute('data-slot',fault==='wrong-owner'?'other':'page-actions-secondary');const refresh=secondary.add(new Element('BUTTON','更新'));refresh.disabled=fault==='disabled';refresh.hidden=fault==='hidden';
   dom.main.add(new Element('BUTTON','更新'));dom.main.add(new Element('BUTTON','更新'));if(fault==='duplicate')secondary.add(new Element('BUTTON','更新'));
   const columns=dom.main.add(new Element('DETAILS'));columns.setAttribute('data-slot','column-visibility');columns.add(new Element('SUMMARY','Columns'));
   const stream=dom.main.add(new Element('BUTTON','Stream'));stream.setAttribute('data-slot','stream-primary-trigger');stream.setAttribute('data-stream-id','stream-control-platform');
   if(fault==='none')assert.equal(dom.run<{required:number}>(prepareKeyboardExpression(contract)).required,4);else assert.throws(()=>dom.run(prepareKeyboardExpression(contract)),/missing, disabled or ambiguous/);
 }
});

test('UI-KEYBOARD-FORM-010: actual form markup, main owner and real section callback drive Enter/Space once without submit or fragment changes',async()=>{
  const {StreamSlotForm}=await import('../../src/features/streams/stream-slot-form.tsx');
  const {createStreamActionController}=await import('../../src/features/streams/stream-action-controller.ts');
  const controller=createStreamActionController({getPermissions:()=>({kind:'ready',permissions:[]}),getState:()=>({kind:'ready',freshness:'fresh',fingerprint:'synthetic'}),mutate:async()=>{throw Error('navigation must not mutate');}});
  for(const locale of ['ja','en'] as const){
    const condition=conditions.find(c=>c.family==='stream-create-edit'&&c.exercise==='keyboard'&&c.locale===locale)!;
    const contract=keyboardContract(condition);assert.ok(contract.activationTarget);assert.equal(contract.activationTarget.sectionId,'create-stream-basic');
    const html=renderUI(createElement(StreamSlotForm,{actionController:controller,onActionResult(){},onSaved(){},canCreate:true,canUpdate:true,canAssignEncoder:true,canAssignWorker:true}),locale,'/admin/streams/#create-stream');
    for(const fault of ['none','missing','duplicate','wrong-reference','hidden','wrong-focus','double-activation']){
      const dom=observerDOM();dom.main.children=[];dom.context.location.hash='#create-stream';dom.context.location.search='?retained=1';
      const dialog=dom.main.add(new Element('DIV'));dialog.setAttribute('role','dialog');let parent=dialog;const stack=[dialog];
      // Parse the real rendered form; no replacement navigation is invented for this fixture.
      for(const token of html.matchAll(/<\/?([a-z][\w-]*)\b([^>]*)>|([^<]+)/gi)){
        if(token[3]){parent.ownText+=token[3];continue;}
        const tag=token[1].toUpperCase();if(token[0].startsWith('</')){if(stack.at(-1)?.tagName===tag){stack.pop();parent=stack.at(-1)!;}continue;}
        const e=parent.add(new Element(tag));for(const a of token[2].matchAll(/([\w-]+)="([^"]*)"/g))e.setAttribute(a[1],a[2]);
        if(!['INPUT','IMG','BR','HR','META','LINK'].includes(tag)){stack.push(e);parent=e;}
      }
      const button=dialog.querySelector(contract.activationTarget.selector),section=dom.document.getElementById('create-stream-basic');assert.ok(button);assert.ok(section);const nav=button.parentElement!;
      // An unrelated real-shaped nav cannot substitute for a missing required main owner.
      const other=dialog.add(new Element('NAV'));other.setAttribute('data-slot','section-navigation');other.add(new Element('BUTTON','Other')).setAttribute('aria-controls','create-stream-basic');
      if(fault==='missing')nav.hidden=true;if(fault==='hidden')section.hidden=true;if(fault==='wrong-reference')section.id='different';
      if(fault==='duplicate')nav.add(new Element('BUTTON','Duplicate')).setAttribute('aria-controls','create-stream-basic');
      let submits=0;dialog.addEventListener('submit',()=>submits++);const keys:string[]=[];let scrolls=0;
      section.scrollIntoView=()=>{scrolls++;};
      const navigate=actualJSXCallback(readFileSync(new URL('../../src/components/layout/detail-section.tsx',import.meta.url),'utf8'),'button','onClick',{document:dom.document,item:{id:button.getAttribute('aria-controls')}});
      const browser={evaluate:async(e:string)=>dom.run(e),pressTab:async()=>button.focus(),pressNativeKey:async(key:string)=>{keys.push(key);button.dispatchEvent(new Event('click'));if(fault==='double-activation')button.dispatchEvent(new Event('click'));if(fault!=='wrong-focus')navigate();},waitFor:async(e:string,p:(v:unknown)=>boolean)=>assert.ok(p(dom.run(e)))} as unknown as BrowserHarness;
      if(fault==='none'){assert.deepEqual(await exerciseActivation(browser,'section',contract.activationTarget),[]);assert.deepEqual(keys,['Enter','Space']);assert.equal(scrolls,2);assert.equal(dom.document.activeElement,section);}
      else await assert.rejects(exerciseActivation(browser,'section',contract.activationTarget));
      assert.equal(submits,0);assert.equal(dom.context.location.hash,'#create-stream');assert.equal(dom.context.location.search,'?retained=1');
    }
  }
});

for (const direction of ["forward", "backward"] as const) {
  test(`UI-KEYBOARD-001-${direction}: real harness socket awaits ordered Tab down/up with exact modifiers`, async t => {
    const { harness, socket } = createHarnessFixture(); t.after(() => harness.close());
    socket.hold("Input.dispatchKeyEvent");
    const input = harness.pressTab(direction);
    const down = await socket.waitForCommand("Input.dispatchKeyEvent");
    assert.equal(socket.commandsFor("Input.dispatchKeyEvent").length, 1, "keyUp must wait for down acknowledgement");
    socket.respond(down, { result: {} }); await new Promise<void>(resolve => setImmediate(resolve));
    const commands = socket.commandsFor("Input.dispatchKeyEvent"); assert.equal(commands.length, 2);
    const base = { key: "Tab", code: "Tab", windowsVirtualKeyCode: 9, nativeVirtualKeyCode: 9, modifiers: direction === "backward" ? 8 : 0 };
    assert.deepEqual(commands.map(command => command.params), [{ ...base, type: "keyDown" }, { ...base, type: "keyUp" }]);
    assert.deepEqual(commands.map(command => command.sessionId), ["test-session", "test-session"]);
    socket.respond(commands[1], { result: {} }); await input;
    assert.equal(socket.commandsFor("Input.dispatchKeyEvent").length, 2);
  });
}
test("UI-KEYBOARD-002: unsupported direction is rejected at runtime and type boundary", async t => {
  const { harness, socket } = createHarnessFixture(); t.after(() => harness.close());
  for (const direction of ["Tab", "Shift+Tab", "", null, undefined, 8, {}]) await assert.rejects(Reflect.apply(harness.pressTab, harness, [direction]), /Unsupported Tab direction/);
  assert.equal(socket.commandsFor("Input.dispatchKeyEvent").length, 0);
  const diagnostics = nativeKeyTypeDiagnostics(`import { BrowserHarness } from "./browser-harness.mts"; declare const browser: BrowserHarness; browser.pressTab("forward"); browser.pressTab("backward"); browser.pressTab("Shift+Tab"); browser.pressNativeKey("Tab");`);
  assert.equal(diagnostics.length, 2, diagnostics.map(formatDiagnostic).join("\n")); assert.ok(diagnostics.every(diagnostic => diagnostic.code === 2345));
});
test("UI-KEYBOARD-003: closed and fatal harness reject without new sends", async () => {
  const closed = createHarnessFixture(); await closed.harness.close();
  for (const direction of ["forward", "backward"] as const) await assert.rejects(closed.harness.pressTab(direction), /Browser harness closed/);
  assert.equal(closed.socket.commandsFor("Input.dispatchKeyEvent").length, 0);
  const fatal = createHarnessFixture();
  try {
    fatal.socket.hold("Input.dispatchKeyEvent"); const input = fatal.harness.pressTab("backward");
    await fatal.socket.waitForCommand("Input.dispatchKeyEvent"); fatal.socket.close();
    await assert.rejects(input, /Browser CDP connection closed/);
    await assert.rejects(fatal.harness.pressTab("forward"), /Browser CDP connection closed/);
    assert.equal(fatal.socket.commandsFor("Input.dispatchKeyEvent").length, 1);
  } finally { await fatal.harness.close(); }
});
for (const failedType of ["keyDown", "keyUp"]) test(`UI-KEYBOARD-004-${failedType}: socket failure propagates without retry or extra events`, async t => {
  const { harness, socket } = createHarnessFixture(); t.after(() => harness.close()); socket.hold("Input.dispatchKeyEvent");
  const input = harness.pressTab("backward"), rejected = assert.rejects(input, /fixed Tab failure/);
  const down = await socket.waitForCommand("Input.dispatchKeyEvent");
  if (failedType === "keyDown") socket.respond(down, { error: { message: "fixed Tab failure" } });
  else { socket.respond(down, { result: {} }); await new Promise<void>(resolve => setImmediate(resolve)); socket.respond(socket.commandsFor("Input.dispatchKeyEvent")[1], { error: { message: "fixed Tab failure" } }); }
  await rejected; assert.equal(socket.commandsFor("Input.dispatchKeyEvent").length, failedType === "keyDown" ? 1 : 2);
});

test('UI-KEYBOARD-009: required main section owner survives other navigations and actual Enter/Space reaches its referenced section once',async()=>{
  const condition=conditions.find(c=>c.family==='stream-detail'&&c.exercise==='keyboard'&&c.locale==='en')!;
  const contract=keyboardContract(condition);assert.ok(contract.activationTarget);
  for(const fault of ['none','missing','duplicate','wrong-reference','wrong-focus']){
    const dom=observerDOM(),dialog=dom.body.add(new Element('DIV'));dialog.setAttribute('role','dialog');
    const nav=dialog.add(new Element('NAV'));nav.setAttribute('data-slot','section-navigation');nav.setAttribute('aria-label','Stream detail sections');
    const button=nav.add(new Element('BUTTON','Overview'));button.setAttribute('aria-controls','stream-overview');
    const section=dialog.add(new Element('SECTION'));section.id='stream-overview';
    const other=dialog.add(new Element('NAV'));other.setAttribute('data-slot','section-navigation');other.setAttribute('aria-label','Updater settings sections');
    other.add(new Element('BUTTON','Overview')).setAttribute('aria-controls','different-section');
    if(fault==='missing')nav.hidden=true;
    if(fault==='duplicate'){const extra=nav.add(new Element('BUTTON','Overview'));extra.setAttribute('aria-controls','stream-overview');}
    if(fault==='wrong-reference')section.id='wrong';
    if(['missing','duplicate','wrong-reference'].includes(fault)){
      assert.throws(()=>{assert.equal(dom.run(prepareActivation('section',contract.activationTarget)),'section');});continue;
    }
    const navigate=actualJSXCallback(readFileSync(new URL('../../src/components/layout/detail-section.tsx',import.meta.url),'utf8'),'button','onClick',{item:{id:'stream-overview'},document:dom.document});
    const keys:string[]=[];
    const browser={evaluate:async(e:string)=>dom.run(e),pressTab:async()=>button.focus(),pressNativeKey:async(key:string)=>{keys.push(key);button.dispatchEvent(new Event('click'));if(fault!=='wrong-focus')navigate();},waitFor:async(e:string,predicate:(v:unknown)=>boolean)=>assert.ok(predicate(dom.run(e)))} as unknown as BrowserHarness;
    if(fault==='wrong-focus')await assert.rejects(exerciseActivation(browser,'section',contract.activationTarget),/referenced section/);
    else {assert.deepEqual(await exerciseActivation(browser,'section',contract.activationTarget),[]);assert.deepEqual(keys,['Enter','Space']);assert.equal(dom.document.activeElement,section);}
  }
});

test('UI-DATETIME-CSS-017: actual datetime Input and generated scoped focus-within outline keep both normal and forced indicators',async t=>{
 const {default:postcss}=await import('postcss'),{default:tailwind}=await import('@tailwindcss/postcss'),{fileURLToPath}=await import('node:url');
 const {Input}=await import('../../src/components/ui/input.tsx');
 const html=renderUI(createElement(Input,{type:'datetime-local',value:'2026-09-01T12:34',readOnly:true}),'en','/admin/streams/');
 assert.match(html,/data-slot="input"/);assert.match(html,/type="datetime-local"/);assert.match(html,/value="2026-09-01T12:34"/);
 const from=fileURLToPath(new URL('../../src/app/globals.css',import.meta.url));
 const result=await postcss([tailwind({base:fileURLToPath(new URL('../../',import.meta.url)),optimize:false})]).process(readFileSync(from,'utf8'),{from});
 const rules:import('postcss').Rule[]=[];result.root.walkRules(r=>{if(r.selector==='input[data-slot="input"][type="datetime-local"]:focus-within')rules.push(r);});assert.equal(rules.length,2);
 for(const rule of rules){const forced=rule.parent?.type==='atrule';if(forced)assert.equal((rule.parent as import('postcss').AtRule).params,'(forced-colors: active)');else assert.equal(rule.parent?.type,'root');
  assert.ok(rule.nodes.some(n=>n.type==='decl'&&n.prop==='outline'&&n.value===(forced?'2px solid Highlight':'2px solid var(--ring)')));assert.ok(rule.nodes.some(n=>n.type==='decl'&&n.prop==='outline-offset'&&n.value==='2px'));
 }
 t.diagnostic('Generated CSS and actual Input only; native datetime segment identity remains pending, bounds 16/128 retained.');
});

test('UI-TABPANEL-KEYBOARD-017: actual current exercise and harness Tab/ShiftTab traverse the tall active Account panel in order',async()=>{
 const condition=conditions.find(c=>c.family==='account'&&c.exercise==='keyboard')!,dom=observerDOM();dom.main.children=[];
 const refresh=dom.main.add(new Element('BUTTON','Refresh')),root=dom.main.add(new Element('DIV'));root.setAttribute('data-slot','tabs');
 const tab=root.add(new Element('BUTTON','Profile'));tab.setAttribute('role','tab');tab.setAttribute('aria-selected','true');tab.setAttribute('aria-controls','profile');tab.id='profile-tab';
 const panel=root.add(new Element('DIV'));panel.id='profile';panel.setAttribute('role','tabpanel');panel.setAttribute('data-slot','tabs-content');panel.setAttribute('data-state','active');panel.setAttribute('tabindex','0');panel.setAttribute('aria-labelledby',tab.id);panel.rect={left:10,right:950,top:100,bottom:1550,width:940,height:1450};
 const choose=panel.add(new Element('BUTTON','Choose image')),nodes=[refresh,tab,panel,choose];dom.document.elementFromPoint=()=>panel;
 const owner=createHarnessFixture(),tabNative=owner.harness.pressTab.bind(owner.harness);let index=-1;const steps:string[]=[];
 owner.harness.evaluate=async<T,>(expression:string)=>dom.run<T>(expression);
 owner.harness.pressTab=async direction=>{await tabNative(direction);index+=direction==='forward'?1:-1;assert.ok(index>=0&&index<nodes.length);dom.document.activeElement=nodes[index];steps.push(direction+':'+index);};
 try{await exerciseAccessibility(owner.harness,condition);assert.deepEqual(steps,['forward:0','forward:1','forward:2','forward:3','backward:2','backward:1','backward:0']);assert.equal(owner.socket.commandsFor('Input.dispatchKeyEvent').length,14);}
 finally{await owner.harness.close();}
});
