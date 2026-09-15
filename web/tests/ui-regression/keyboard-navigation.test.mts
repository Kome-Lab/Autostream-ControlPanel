import assert from "node:assert/strict";
import test from "node:test";
import { createHarnessFixture } from "../helpers/browser-cdp-socket-fixture.mts";
import { nativeKeyTypeDiagnostics, formatDiagnostic } from "../helpers/browser-native-input-oracle.mts";
import { observerDOM, Element } from './observer-dom.mts';
import { prepareActivation, exerciseActivation } from './keyboard-activation.mts';
import { keyboardContract } from './observation.mts';
import { conditions } from './matrix.mts';
import { actualJSXCallback } from './source-callback.mts';
import { readFileSync } from 'node:fs';
import type { BrowserHarness } from '../helpers/browser-harness.mts';
import './component-loader.mts';
import {createElement} from 'react';
import {renderUI} from './render-ui.mts';

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
