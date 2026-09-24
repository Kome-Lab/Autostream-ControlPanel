import assert from "node:assert/strict";
import test from "node:test";
import { createHarnessFixture } from "../helpers/browser-cdp-socket-fixture.mts";
import { nativeKeyTypeDiagnostics, formatDiagnostic } from "../helpers/browser-native-input-oracle.mts";
import { observerDOM, Element } from './observer-dom.mts';
import { prepareActivation, exerciseActivation } from './keyboard-activation.mts';
import { keyboardContract, exerciseKeyboardPath as exerciseAccessibility, prepareKeyboardExpression, focusExpression, assertFocus, type FocusObservation } from './observation.mts';
import { conditions } from './matrix.mts';
import { actualJSXCallback } from './source-callback.mts';
import { readFileSync } from 'node:fs';
import type { BrowserHarness } from '../helpers/browser-harness.mts';
import './component-loader.mts';
import {createElement} from 'react';
import {renderUI} from './render-ui.mts';

test('UI-PANEL-TAB-019: unchanged observer and native Tab socket retain both directions below sticky header and reject insufficient or invalid panels',async t=>{
 const source=(name:string)=>readFileSync(new URL('../../src/features/'+name,import.meta.url),'utf8');
 // Preserve the Account security assertion, and bind the Security route to its
 // actual ResourcePage owner instead of substituting the Account panel.
 assert.match(source('account/account-view.tsx'),/<TabsContent\s+value="security"\s+className="scroll-mt-20"/);
 for(const [family,path,panelTag] of [['security-settings','resources/resource-page.tsx','resource'],['audit-logs','audit/audit-logs-view.tsx','view']] as const){
  const opening=source(path).match(panelTag==='resource'?/<TabsContent\s+key=\{resource.path\}[^>]*>/:/<TabsContent\s+value=\{view\}[^>]*>/)?.[0];assert.ok(opening);
  assert.match(opening,panelTag==='resource'?/className=\{pageId === "security" \? "scroll-mt-20" : undefined\}/:/className="scroll-mt-20"/);
  for(const locale of ['ja','en'] as const)for(const width of [390,1440])for(const scale of [1,2])for(const height of [300,915,1450])for(const forced of [false,true]){
   const faults=locale==='en'&&width===390&&scale===1&&height===915&&!forced?['none','zero','insufficient','covered','ordinary','inactive','duplicate','wrong-reference','wrong-owner','unselected','hidden','inert','clip','indicator']:['none'];
   for(const fault of faults){
    const condition=conditions.find(c=>c.family===family&&c.locale===locale&&c.exercise===(forced?'forced-colors':'keyboard'))!,dom=observerDOM();dom.main.children=[];
    Object.assign(dom.context,{innerWidth:width,innerHeight:900,matchMedia:()=>({matches:forced})});
    const refresh=dom.main.add(new Element('BUTTON',locale==='ja'?'更新':'Refresh')),root=dom.main.add(new Element('DIV'));root.setAttribute('data-slot','tabs');
    const tab=root.add(new Element('BUTTON',locale==='ja'?'セキュリティ':'Security'));tab.setAttribute('role','tab');tab.setAttribute('aria-selected',fault==='unselected'?'false':'true');tab.setAttribute('aria-controls','panel');tab.id='tab';
    const panel=root.add(new Element(fault==='ordinary'?'BUTTON':'DIV'));panel.id='panel';panel.setAttribute('role',fault==='ordinary'?'button':'tabpanel');panel.setAttribute('data-slot','tabs-content');panel.setAttribute('data-state',fault==='inactive'?'inactive':'active');panel.setAttribute('tabindex','0');panel.setAttribute('aria-labelledby',fault==='wrong-reference'?'other':'tab');
    const next=panel.add(new Element(family==='audit-logs'?'A':'INPUT',family==='audit-logs'?'CSV':''));if(family==='audit-logs')next.setAttribute('href','/synthetic.csv');else{next.type='number';next.setAttribute('type','number');next.setAttribute('min','8');}
    if(fault==='duplicate'){const duplicate=root.add(new Element('DIV'));duplicate.id='panel';}if(fault==='wrong-owner')root.setAttribute('data-slot','other');if(fault==='hidden')panel.hidden=true;if(fault==='inert')panel.setAttribute('inert','');if(fault==='indicator')panel.style.outlineStyle='none';
    const margin=(fault==='zero'?0:fault==='insufficient'?8:80)*scale,header=72*scale;
    Object.assign(panel.style,{outlineWidth:2*scale+'px',outlineOffset:2*scale+'px',outlineColor:forced?'Highlight':'rgb(0,0,255)'});panel.rect={left:8*scale,right:width-8*scale,top:margin,bottom:margin+height*scale,width:width-16*scale,height:height*scale};
    if(fault==='clip'){root.style.overflowY='hidden';root.rect={left:0,right:width,top:margin+1,bottom:900,width,height:900-margin-1};}
    const topBar=dom.body.add(new Element('HEADER'));dom.document.elementFromPoint=(_x,y)=>fault==='covered'||y<header?topBar:panel;
    const owner=createHarnessFixture(),nativeTab=owner.harness.pressTab.bind(owner.harness),nodes=family==='audit-logs'?[refresh,tab,panel,next]:[refresh,panel,next];let index=-1;const steps:string[]=[];
    owner.harness.evaluate=async<T,>(e:string)=>dom.run<T>(e);owner.harness.pressTab=async direction=>{await nativeTab(direction);index+=direction==='forward'?1:-1;assert.ok(index>=0&&index<nodes.length);dom.document.activeElement=nodes[index];steps.push(direction+':'+index);};
    try{
     if(fault==='none'){await exerciseAccessibility(owner.harness,condition);const expected=nodes.map((_,i)=>'forward:'+i).concat(nodes.slice(0,-1).map((_,i)=>'backward:'+(nodes.length-2-i)));assert.deepEqual(steps,expected);assert.equal(owner.socket.commandsFor('Input.dispatchKeyEvent').length,expected.length*2);}
     else {await assert.rejects(exerciseAccessibility(owner.harness,condition));for(const direction of ['forward','backward'] as const){index=nodes.indexOf(panel)+(direction==='forward'?-1:1);await owner.harness.pressTab(direction);assert.throws(()=>assertFocus(dom.run<FocusObservation>(focusExpression)),/hidden|offscreen|indicator/);}}
     assert.equal('__uiKeyboardPlan' in dom.context,false);
    }finally{await owner.harness.close();}
   }
  }
 }
 t.diagnostic('Controlled geometry with actual unchanged exercise/observer/Tab socket; locale, width, forced colors and CSS scale purposes preserved. No real browser or official 12-condition PASS claimed.');
});

test('UI-DATETIME-013: actual exercise and harness Tab socket distinguish host repeats, both exits and pending internal identity',async()=>{
 const condition=conditions.find(c=>c.family==='stream-create-edit'&&c.exercise==='keyboard')!;
 for(const fault of ['none','forward-trap','backward-trap','ordinary-text','ordinary-button','value','replacement','hidden','owner','escape','order','indicator','ua-positive','ua-stationary','ua-reverse','ua-owner','ua-hidden']){
   const dom=observerDOM(),dialog=dom.body.add(new Element('DIV'));dialog.setAttribute('role','dialog');
   const query=dialog.querySelectorAll.bind(dialog);dialog.querySelectorAll=(selector)=>query(selector).sort((a,b)=>dialog.all().indexOf(a)-dialog.all().indexOf(b));
   const form=dialog.add(new Element('DIV'));form.id='create-stream';const content=form.add(new Element('DIV'));content.setAttribute('data-slot','card-content');const nav=content.add(new Element('NAV'));nav.setAttribute('data-slot','section-navigation');
   const first=nav.add(new Element('BUTTON','Basic')),date=dialog.add(new Element(fault==='ordinary-button'?'BUTTON':'INPUT')),last=dialog.add(new Element('BUTTON','Close'));first.setAttribute('aria-controls','create-stream-basic');const section=dialog.add(new Element('SECTION'));section.id='create-stream-basic';
   date.type=fault==='ordinary-text'?'text':'datetime-local';date.value='2026-09-01T12:34';date.setAttribute('aria-label','Schedule');const nodes=[first,date,last];
   const owner=createHarnessFixture(),nativeTab=owner.harness.pressTab.bind(owner.harness),trace:unknown[]=[];let index=-1,remaining=0,activation=0,repeatForward=0,repeatBackward=0;
   let lastDirection:"forward"|"backward"="forward",uaCalls=0;
   Object.defineProperty(owner.harness,'observeNativeFocus',{value:undefined,writable:true});
   if(fault.startsWith('ua-'))Object.defineProperty(owner.harness,'observeNativeFocus',{value:async()=>{
    uaCalls++;return {document:1,host:fault==='ua-owner'&&uaCalls>1?99:2,node:fault==='ua-stationary'?3:lastDirection==='forward'||fault==='ua-reverse'?9-remaining:3+remaining,kind:'datetime',relation:'ua-descendant',role:'spinbutton',stable:true,focusedAncestors:1,indicator:true,visible:fault!=='ua-hidden'};
   }});
   owner.harness.evaluate=async<T,>(expression:string)=>{if(expression.includes('const targets=kind'))activation++;return dom.run<T>(expression);};
   owner.harness.pressTab=async(direction)=>{
     lastDirection=direction;await nativeTab(direction);if(dom.document.activeElement===section)index=-1;const wasDate=dom.document.activeElement===date;
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
     if(fault==='none'||fault==='ua-positive'){const result=await exerciseAccessibility(owner.harness,condition,value=>trace.push(value));assert.equal(result.pending.includes('UA_DATETIME_SEGMENT_IDENTITY_PENDING'),fault==='none');if(fault==='ua-positive')assert.ok(uaCalls>=14);assert.equal(result.pending.some(p=>p.startsWith('ENTER_SPACE')),false);assert.ok(activation>0);assert.equal(date.value,'2026-09-01T12:34');assert.ok(repeatForward>=7&&repeatBackward>=7);}
     else await assert.rejects(exerciseAccessibility(owner.harness,condition,value=>trace.push(value)),/datetime|Tab must move|escaped|hidden|indicator|reordered|UA/);
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


test('UI-ACTIVATION-024: current wrapper clears only same-condition activation after native keys, real state change and exact restoration',async()=>{
 const {exerciseAccessibility:current}=await import('./observation.mts');
 const condition=conditions.find(c=>c.family==='login'&&c.exercise==='keyboard')!;
 for(const fault of ['none','missing','hidden','disabled','duplicate','covered','no-change','duplicate-key','duplicate-click','restore-failure','owner-replaced','untrusted']){
  const dom=observerDOM();dom.main.children=[];const form=dom.main.add(new Element('FORM'));
  const username=form.add(new Element('INPUT'));username.setAttribute('autocomplete','username');
  const password=form.add(new Element('INPUT'));password.type='password';password.setAttribute('type','password');
  const submit=form.add(new Element('BUTTON','Login'));submit.setAttribute('type','submit');
  const theme=dom.main.add(new Element('BUTTON'));theme.setAttribute('aria-label','Theme');
  if(fault==='missing')theme.removeAttribute('aria-label');if(fault==='hidden')theme.hidden=true;if(fault==='disabled')theme.disabled=true;
  if(fault==='duplicate'){const other=dom.main.add(new Element('BUTTON'));other.setAttribute('aria-label','Theme');}
  Object.assign(dom.context.localStorage,{getItem:()=>'{"color_mode":"light"}'});Object.assign(dom.context.location,{href:'http://ui.test/login/'});
  const nodes=[username,password,submit,theme],owner=createHarnessFixture();let index=-1,acts=0;const keys:string[]=[],tabs:string[]=[];
  const nativeTab=owner.harness.pressTab.bind(owner.harness),nativeKey=owner.harness.pressNativeKey.bind(owner.harness);
  owner.harness.evaluate=async<T,>(e:string)=>dom.run<T>(e);dom.document.elementFromPoint=()=>fault==='covered'?submit:theme;
  owner.harness.pressTab=async direction=>{await nativeTab(direction);tabs.push(direction);index=(index+(direction==='forward'?1:nodes.length-1))%nodes.length;dom.document.activeElement=nodes[index];};
  const emit=(code:string)=>{const event=new Event('keydown');Object.defineProperties(event,{code:{value:code},repeat:{value:false},isTrusted:{value:fault!=='untrusted'}});theme.dispatchEvent(event);};
  owner.harness.pressNativeKey=async key=>{await nativeKey(key);keys.push(key);assert.equal(dom.document.activeElement,theme);acts++;emit(key);if(fault==='duplicate-key')emit(key);
   theme.dispatchEvent(new Event('click'));if(fault==='duplicate-click')theme.dispatchEvent(new Event('click'));if(fault==='owner-replaced'&&acts===2)theme.parentElement=null;
   if(fault!=='no-change'&&!(fault==='restore-failure'&&acts===2))dom.html.className=dom.html.className==='dark'?'':'dark';};
  owner.harness.waitFor=async<T,>(e:string,predicate:(v:T)=>boolean)=>{const value=dom.run<T>(e);assert.ok(predicate(value),'actual activation state did not change or restore');return value;};
  try{
   if(fault==='none'){
    const result=await current(owner.harness,condition);assert.deepEqual(result.pending,[]);assert.deepEqual(keys,['Enter','Space']);assert.equal(dom.html.className,'');
    assert.deepEqual(tabs.slice(0,5),['forward','forward','forward','backward','backward'],'original positive reverse order remains unchanged before activation');
    assert.equal(owner.socket.commandsFor('Input.dispatchKeyEvent').filter(c=>c.params.key==='Enter'||c.params.key===' ').length,4);
   }else await assert.rejects(current(owner.harness,condition),/missing|ambiguous|clipped|covered|activation|key|callback/);
   assert.equal('__uiKeyboardPlan'in dom.context,false);assert.equal('__uiSurfaceActivation'in dom.context,false);
   assert.equal(owner.harness.requests.size,0,'controlled keys issue no business request');
  }finally{await owner.harness.close();}
 }
});

test('UI-UA-IDENTITY-024: typed read-only harness binds focused AX leaf to exact document/frame/UA host and releases objects',async()=>{
 for(const fault of ['none','missing-leaf','wrong-host','wrong-frame','root-mismatch','two-leaves','ignored','missing-backend','disabled-leaf','changed-leaf','changed-loader','changed-host','ax-bound','read-failure','release-failure','both-failures','final-disabled','final-role','final-frame','final-root','final-ancestry','final-owner','final-duplicate']){
  const owner=createHarnessFixture();let frameReads=0,hostReads=0,axReads=0,hostDescriptions=0;const sent:{method:string;params:Record<string,unknown>}[]=[];
  owner.socket.send=(raw:string)=>{const cmd=JSON.parse(raw);sent.push(cmd);let result:Record<string,unknown>={};let error:{message:string}|undefined;
   if(cmd.method==='Page.getFrameTree')result={frameTree:{frame:{id:'frame-one',loaderId:fault==='changed-loader'&&++frameReads>1?'replaced':'loader-one'}}};
   if(cmd.method==='Runtime.evaluate')result={result:{objectId:cmd.params.expression==='document'?'document':'host'}};
   if(cmd.method==='DOM.describeNode')result={node:cmd.params.objectId==='document'?{nodeName:'#document',backendNodeId:1}:{nodeName:'INPUT',backendNodeId:10,shadowRoots:[{nodeName:'#document-fragment',backendNodeId:20,shadowRootType:'user-agent',children:[{nodeName:'SPAN',backendNodeId:11},{nodeName:'SPAN',backendNodeId:12}]}]}};
   if(cmd.method==='DOM.describeNode'&&cmd.params.objectId==='host'&&++hostDescriptions>1&&fault==='final-owner'){const node=result.node as {shadowRoots:{children:{backendNodeId:number}[]}[]};node.shadowRoots[0].children=node.shadowRoots[0].children.filter(n=>n.backendNodeId!==11);}
   if(cmd.method==='Runtime.callFunctionOn')result={result:{value:cmd.params.objectId==='leaf'?{indicator:true,visible:true}:String(cmd.params.functionDeclaration).includes('matches(')?'datetime':!(fault==='changed-host'&&++hostReads>0)}};
   if(cmd.method==='DOM.resolveNode')result={object:{objectId:'leaf'}};
   if(cmd.method==='Accessibility.getFullAXTree'){axReads++;
    const focused=[{name:'focused',value:{value:true}}];
    const nodes: {nodeId:string;backendDOMNodeId?:number;role:{value:string};frameId?:string;properties?:typeof focused;childIds?:string[];ignored?:boolean;name?:{value:string};value?:{value:string}}[]=[{nodeId:'root',backendDOMNodeId:fault==='root-mismatch'?999:1,role:{value:'RootWebArea'},frameId:fault==='wrong-frame'?'other':'frame-one',properties:focused,childIds:['host']},{nodeId:'host',backendDOMNodeId:10,role:{value:'DateTime'},childIds:['leaf','second']},{nodeId:'leaf',backendDOMNodeId:fault==='missing-backend'?undefined:fault==='wrong-host'?999:fault==='changed-leaf'&&axReads>1?12:11,ignored:fault==='ignored',role:{value:'spinbutton'},name:{value:'DO-NOT-LOG-NAME'},value:{value:'DO-NOT-LOG-VALUE'},properties:fault==='missing-leaf'?[]:fault==='disabled-leaf'?[...focused,{name:'disabled',value:{value:true}}]:focused}];
    if(axReads>1){
     if(fault==='final-disabled')nodes[2].properties=[...focused,{name:'disabled',value:{value:true}}];
     if(fault==='final-role')nodes[2].role={value:'slider'};
     if(fault==='final-frame')nodes[0]={...nodes[0],frameId:'other'};
     if(fault==='final-root')nodes[0].backendDOMNodeId=999;
     if(fault==='final-ancestry')nodes[0].childIds=[];
     if(fault==='final-duplicate')nodes.push({...nodes[1]});
    }
    if(fault==='two-leaves')nodes.push({nodeId:'second',backendDOMNodeId:12,role:{value:'spinbutton'},properties:focused,ignored:false}as typeof nodes[number]);
    if(fault==='ax-bound')while(nodes.length<=8192)nodes.push({...nodes[1],nodeId:'overflow-'+nodes.length});
    result={nodes};if(['read-failure','both-failures'].includes(fault))error={message:'bounded-read-failure'};
   }
   if(cmd.method==='Runtime.releaseObjectGroup'&&['release-failure','both-failures'].includes(fault))error={message:'bounded-release-failure'};
   queueMicrotask(()=>owner.socket.respond(cmd,error?{error}:{result}));
  };
  try{
   if(fault==='none'){
    const first=await owner.harness.observeNativeFocus(),second=await owner.harness.observeNativeFocus();assert.deepEqual(first,second);
    assert.equal(first.kind,'datetime');assert.equal(first.relation,'ua-descendant');assert.equal(first.role,'spinbutton');assert.equal(first.focusedAncestors,1);assert.ok(first.indicator&&first.visible);
    assert.ok(Buffer.byteLength(JSON.stringify(first))<1024);assert.doesNotMatch(JSON.stringify(first),/DO-NOT-LOG|frame-one|loader-one|nodeId|backendDOM/);
    assert.equal(sent.filter(c=>c.method==='Accessibility.enable').length,1);
   }else{
    await assert.rejects(owner.harness.observeNativeFocus(),error=>{assert.ok(error instanceof Error);if(fault==='both-failures'){assert.ok(error instanceof AggregateError);assert.equal(error.errors.length,2);assert.equal(error.cause,error.errors[0]);assert.equal(error.errors[0].message,'bounded-read-failure');assert.equal(error.errors[1].message,'bounded-release-failure');}return true;});
   }
   assert.equal(sent.filter(c=>c.method==='Runtime.releaseObjectGroup').length,fault==='none'?2:1);
   assert.equal(sent.some(c=>/Input\.|DOM\.focus|scroll|Fetch\./.test(c.method)),false,'observer never acts, changes interception or exposes raw send');
   assert.equal(owner.harness.requests.size,0);assert.equal(owner.harness.responses.size,0);
  }finally{await owner.harness.close();}
 }
});

test('UI-MEDIA-025: actual readonly observer separates complete stable media identity from independently expected operability',async()=>{
 const {assertMediaIdentity}=await import('./media-keyboard-contract.mts');
 for(const state of ['empty','error','loaded']as const)for(const fault of ['none','state-change','set-change','missing-host','ambiguous-host','wrong-leaf','read-failure','release-failure','unknown-error']){
  const owner=createHarnessFixture();let states=0,axes=0;const methods:string[]=[];
  owner.socket.send=(raw:string)=>{const cmd=JSON.parse(raw);methods.push(cmd.method);let result:Record<string,unknown>={};let error:{message:string}|undefined;
   if(cmd.method==='Page.getFrameTree')result={frameTree:{frame:{id:'owned-frame',loaderId:'owned-loader'}}};
   if(cmd.method==='Runtime.evaluate')result={result:{objectId:cmd.params.expression==='document'?'document':'host'}};
   if(cmd.method==='DOM.describeNode')result={node:cmd.params.objectId==='document'?{nodeName:'#document',backendNodeId:1}:{nodeName:'VIDEO',backendNodeId:10,shadowRoots:[{nodeName:'#document-fragment',backendNodeId:20,shadowRootType:'user-agent',children:[{nodeName:'BUTTON',backendNodeId:11}]}]}};
   if(cmd.method==='Runtime.callFunctionOn'){
    const expression=String(cmd.params.functionDeclaration);
    const facts={ready:state==='loaded'?4:0,network:state==='empty'?0:state==='loaded'?1:3,error:state==='error'?4:0,source:state!=='empty',currentSource:state!=='empty',paused:true,atStart:true};
    if(expression.includes('readyState')){states++;if(fault==='state-change'&&states>1)facts.ready=1;if(fault==='unknown-error')facts.error=9;}
    result={result:{value:cmd.params.objectId==='leaf'?{indicator:true,visible:true}:expression.includes('matches(')?'media':expression.includes('readyState')?facts:true}};
   }
   if(cmd.method==='DOM.resolveNode')result={object:{objectId:'leaf'}};
   if(cmd.method==='Accessibility.getFullAXTree'){
    axes++;const enabled=state==='loaded',property=(name:string,value:boolean)=>({name,value:{value}});
    const nodes=[{nodeId:'root',backendDOMNodeId:1,ignored:false,frameId:'owned-frame',role:{value:'RootWebArea'},childIds:['host'],properties:[property('focused',true)]},
     {nodeId:'host',backendDOMNodeId:10,ignored:false,frameId:'owned-frame',role:{value:'Video'},childIds:['control'],properties:[property('focused',!enabled),property('focusable',fault!=='missing-host'),property('disabled',!enabled)]},
     {nodeId:'control',backendDOMNodeId:fault==='wrong-leaf'?99:11,ignored:false,frameId:'owned-frame',role:{value:'button'},childIds:[],properties:[property('focused',enabled),property('focusable',enabled||fault==='set-change'&&axes>1),property('disabled',!enabled)]}];
    if(fault==='ambiguous-host')nodes.push({...nodes[1],nodeId:'duplicate-host'});
    result={nodes};if(fault==='read-failure')error={message:'closed-read-failure'};
   }
   if(cmd.method==='Runtime.releaseObjectGroup'&&fault==='release-failure')error={message:'closed-cleanup-failure'};
   queueMicrotask(()=>owner.socket.respond(cmd,error?{error}:{result}));
  };
  try{
   if(fault==='none'||fault==='wrong-leaf'&&state!=='loaded'){
    const actual=await owner.harness.observeNativeFocus();assert.equal(actual.complete,true);assert.equal(actual.disabled,state!=='loaded');assert.equal(actual.focusable,true);
    assert.equal(assertMediaIdentity(actual,state==='empty'?'preview-403-empty':state==='error'?'public-fixture-error':'available'),state==='loaded'?'available':'unavailable');
    assert.equal(actual.focusables.length,state==='loaded'?2:1);assert.equal(actual.uaFocusable,state==='loaded'?1:0);
    assert.doesNotMatch(JSON.stringify(actual),/owned-frame|owned-loader|backendDOM|nodeId/);
    const mutations:((v:typeof actual)=>unknown)[]=[v=>({...v,complete:false}),v=>({...v,visible:false}),v=>({...v,focusable:false}),v=>({...v,media:null}),v=>({...v,focusables:[]}),v=>({...v,mediaState:'loading'}),v=>({...v,media:{...v.media!,paused:false}})];
    for(const mutate of mutations)assert.throws(()=>assertMediaIdentity(mutate(actual)as typeof actual,state==='empty'?'preview-403-empty':state==='error'?'public-fixture-error':'available'));
    if(state!=='loaded')assert.throws(()=>assertMediaIdentity(actual,'available'),/loaded/);
   }else if(fault==='set-change'&&state==='loaded')await owner.harness.observeNativeFocus();
   else await assert.rejects(owner.harness.observeNativeFocus());
   assert.equal(methods.filter(m=>m==='Runtime.releaseObjectGroup').length,1);
   assert.equal(methods.some(m=>/Input\.|Fetch\.|DOM\.focus|Target\./.test(m)),false);
  }finally{await owner.harness.close();}
 }
});

test('UI-READONLY-025: four exact additions compose with original Tab protection and reject API, owner, fatal, closed, cleanup and old-body changes',async()=>{
 const {execFileSync}=await import('node:child_process');const {assertApprovedSourceDelta}=await import('./approved-source-delta.mts');
 const root=new URL('../../..',import.meta.url),path='web/tests/helpers/browser-harness.mts';
 const protectedFixture=JSON.parse(readFileSync(new URL('../fixtures/ui-regression/protected.json',import.meta.url),'utf8'));
 const manifest=JSON.parse(readFileSync(new URL('../fixtures/ui-regression/approved-source-deltas.json',import.meta.url),'utf8'));
 const before=execFileSync('git',['show',protectedFixture.base_commit+':'+path],{cwd:root}),after=readFileSync(new URL('../helpers/browser-harness.mts',import.meta.url),'utf8');
 assertApprovedSourceDelta(path,before,Buffer.from(after),manifest);
 for(const [from,to]of[
  ['return this.nativeFocusObserver.observe();','await this.send("Fetch.enable"); return this.nativeFocusObserver.observe();'],
  ['(method, params) => this.send(method, params)','(method, params) => this.sendBrowser(method, params)'],
  ['async observeNativeFocus() {\n    this.assertNoFatalError();','async observeNativeFocus() {'],
  ['if (this.closed) throw new Error("Browser harness closed");\n    return this.nativeFocusObserver.observe();','return this.nativeFocusObserver.observe();'],
  ['    this.nativeFocusObserver.clear();\n',''],
  ['async observeNativeFocus() {','async observeNativeFocus(method: string) {'],
  ['modifiers: direction === "backward" ? 8 : 0','modifiers: 0'],
 ]){
  assert.ok(after.includes(from));
  const changed=after.replace(from,to);assert.notEqual(changed,after);assert.throws(()=>assertApprovedSourceDelta(path,before,Buffer.from(changed),manifest));
 }
 const fixture=createHarnessFixture();await fixture.harness.close();await assert.rejects(fixture.harness.observeNativeFocus(),/closed/);
});

test('UI-MEDIA-INPUT-025: real negative-key caller observes finite native scroll and exact keyboard restoration, rejects state/action/owner faults and removes listeners',async()=>{
 const {exerciseUnavailableMedia,mediaInputSettled,mediaReturnPeer}=await import('./media-keyboard-contract.mts');
 const {runInNewContext}=await import('node:vm');
 for(const family of ['stream-detail','public-archive-share'])for(const fault of ['none','state','played','untrusted','wrong-peer','wrong-host','covered-peer','input-error','cleanup-error']){
  const condition=conditions.find(c=>c.family===family&&c.state==='ready'&&['keyboard','Detail'].includes(c.exercise||''))!;
  const listeners=new Map<object,Map<string,Set<(e:unknown)=>void>>>();
  const node=()=>{const n={isConnected:true,parentElement:null as unknown,scrollLeft:0,scrollTop:0,getAttribute:(name:string)=>name==='src'?(family==='stream-detail'?null:'data:video/mp4;base64,AAAAHGZ0eXBtcDQyAAAAAG1wNDJpc29t'):null,closest:()=>null,contains:(e:unknown)=>e===n,getBoundingClientRect:()=>({x:0,y:0,left:0,top:0,right:100,bottom:50,width:100,height:50}),addEventListener:(type:string,f:(e:unknown)=>void)=>{let m=listeners.get(n);if(!m){m=new Map();listeners.set(n,m);}if(!m.has(type))m.set(type,new Set());m.get(type)!.add(f);},removeEventListener:(type:string,f:(e:unknown)=>void)=>{listeners.get(n)?.get(type)?.delete(f);}};return n;};
  const root=node(),host=node(),peer=node();host.parentElement=root;peer.parentElement=root;
  const document={activeElement:host as unknown,querySelectorAll:(selector:string)=>selector.includes('a[href')?[peer]:[host],elementFromPoint:()=>fault==='covered-peer'?root:peer};
  const context={document,innerWidth:390,innerHeight:844,requestAnimationFrame:(f:()=>void)=>f(),getComputedStyle:()=>({visibility:'visible',display:'block',opacity:'1',outlineStyle:'solid',outlineWidth:'2'}),__uiKeyboardPlan:{medias:[{element:host}]}};
  const ua={document:1,host:2,node:3,kind:'media',relation:'host',role:'media',stable:true,focusedAncestors:1,uaFocusable:0,mediaState:family==='stream-detail'?'empty':'error',indicator:true,visible:true,disabled:true,focusable:true,complete:true,media:{ready:0,network:family==='stream-detail'?0:3,error:family==='stream-detail'?0:4,source:family!=='stream-detail',currentSource:family!=='stream-detail',paused:true,atStart:true},focusables:[{node:3,relation:'host',role:'media',disabled:true}]} as const;
  const inputs:string[]=[],cause=new Error('actual-input-cause');let cleanupCalls=0;
  const fixture={requests:new Map([['/streams/ui-stream-1/preview-links',1]]),responseStatuses:new Map([['/streams/ui-stream-1/preview-links',[403]]]),
   evaluate:async(e:string)=>{if(e.includes("v.host.removeEventListener")){cleanupCalls++;if(fault==='cleanup-error')throw cause;}return JSON.parse(JSON.stringify(await runInNewContext(e,context)));},
   pressNativeKey:async(key:string)=>{inputs.push(key);if(fault==='input-error')throw cause;for(const f of listeners.get(host)?.get('keydown')||[])f({target:host,code:key,isTrusted:fault!=='untrusted',repeat:false});if(fault==='played')for(const f of listeners.get(host)?.get('play')||[])f({});},
   observeNativeFocus:async()=>fault==='state'?{...ua,media:{...ua.media,ready:1}}:ua,
   pressTab:async(direction:string)=>{inputs.push(direction);document.activeElement=direction==='forward'?(fault==='wrong-peer'?root:peer):(fault==='wrong-host'?root:host);},
   waitFor:async(e:string,p:(v:unknown)=>boolean)=>{let value:unknown;for(let n=0;n<5;n++){value=await runInNewContext(e,context);if(p(value))return value;}throw Error('existing deadline');}
  } as unknown as BrowserHarness;
  const relevant=family==='public-archive-share'||!['wrong-peer','wrong-host','covered-peer'].includes(fault);
  if(fault==='none'||!relevant){assert.equal(await exerciseUnavailableMedia(fixture,condition,ua),family==='public-archive-share'?2:0);assert.deepEqual(inputs,family==='public-archive-share'?['Enter','Space','forward','backward']:['Enter','Space']);}
  else await assert.rejects(exerciseUnavailableMedia(fixture,condition,ua),error=>{if(['input-error','cleanup-error'].includes(fault))assert.equal(error,cause);return true;});
  assert.equal(cleanupCalls,1);
  if(fault!=='cleanup-error'){assert.equal('__uiUnavailableMedia'in context,false);assert.equal([...listeners.values()].flatMap(m=>[...m.values()].map(s=>s.size)).reduce((a,b)=>a+b,0),0);}
 }
 const parent={scrollLeft:0,scrollTop:0,parentElement:null},host={parentElement:parent,isConnected:true,getBoundingClientRect:()=>({x:0,y:0,width:100,height:100})};
 const state={host,owners:[parent],pending:new Set<object>(),last:null,stable:0},context={__uiUnavailableMedia:state,document:{activeElement:host},requestAnimationFrame:(f:()=>void)=>f()};
 const sample=()=>runInNewContext(mediaInputSettled,context) as Promise<boolean>;
 assert.equal(await sample(),false);parent.scrollTop=10;assert.equal(await sample(),false);state.pending.add(parent);for(let i=0;i<4;i++)assert.equal(await sample(),false,'unsettled native scroll cannot pass');state.pending.clear();assert.equal(await sample(),false);assert.equal(await sample(),true);
 host.isConnected=false;await assert.rejects(sample(),/owner replaced/);host.isConnected=true;state.owners=[];await assert.rejects(sample(),/scroll owner replaced/);
 assert.ok(mediaReturnPeer.includes('elementFromPoint'),'restoration observes the real hit target');
});

test('UI-LINK-025: native link expectations require default navigation and exact settled fixture GET while Space stays inactive',async()=>{
 const {assertLinkActivation,publicDownloadPath}=await import('./native-link-activation.mts');const {createUIFixture}=await import('./route-fixture.mts');
 const condition=conditions.find(c=>c.family==='public-archive-share'&&c.exercise==='keyboard')!;
 const good={keys:1,clicks:0,navigations:0,invalid:false,sameDocument:true,sameTarget:true};
 assertLinkActivation(good,'Space');assertLinkActivation({...good,keys:2,clicks:1,navigations:1},'Enter');
 for(const key of ['Space','Enter']as const){const expected=key==='Space'?good:{...good,keys:2,clicks:1,navigations:1};for(const change of [{keys:0},{clicks:2},{navigations:key==='Space'?1:0},{invalid:true},{sameDocument:false},{sameTarget:false}])assert.throws(()=>assertLinkActivation({...expected,...change},key));}
 for(const fault of ['none','post','query','other-path','external','wrong-condition']){
  const fixture=createUIFixture('http://127.0.0.1:4100');fixture.reset(fault==='wrong-condition'?{...condition,family:'archive'}:condition,'/archive-shares/ui-synthetic-share');
  const response=await fixture.resolver({method:fault==='post'?'POST':'GET',url:(fault==='external'?'https://unexpected.example':'http://127.0.0.1:4100')+publicDownloadPath+(fault==='query'?'?extra=1':fault==='other-path'?'/extra':'')});
  if(fault==='none'){assert.equal(response?.status,204);assert.equal(response?.body,'');assert.equal(response?.requiredResponse,true);assert.equal(fixture.trace.length,1);assert.deepEqual(fixture.unexpected,[]);}else assert.notEqual(response?.status,204);
 }
 const fixture=createUIFixture('http://127.0.0.1:4100');fixture.reset(condition,'/archive-shares/ui-synthetic-share');
 const response=await fixture.resolver({method:'GET',url:'http://127.0.0.1:4100/archive-shares/ui-synthetic-share'});
 assert.equal((response?.body as {playback_url:string}).playback_url,(await import('./media-keyboard-contract.mts')).publicFixtureMedia,'unhealthy original fixture media is unchanged');
});


test('UI-ACTIVATION-MUTATION-024: actual current callback keeps unknown API and business mutations fatal after harmless input',async()=>{
 const source=readFileSync(new URL('./run-browser.mts',import.meta.url),'utf8');
 const start=source.indexOf('        assert.deepEqual(fixture.unexpected'),end=source.indexOf('        if (condition.state === "ready")',start);
 assert.ok(start>0&&end>start);const body=source.slice(start,end);
 const {default:ts}=await import('typescript');
 const js=ts.transpileModule('async function current(fixture,condition,surface){'+body+'}',{compilerOptions:{target:ts.ScriptTarget.ES2022,module:ts.ModuleKind.CommonJS}}).outputText;
 const current=new Function('assert','previewExpectation','assertPreviewIssue',js+';return current;')(assert,()=>{throw Error('not Preview');},()=>{throw Error('not Preview');}) as (fixture:unknown,condition:unknown,surface:unknown)=>Promise<void>;
 const condition=conditions.find(c=>c.family==='login'&&c.exercise==='keyboard')!;
 for(const trace of [[],[{method:'POST',path:'/auth/session/refresh'}]])await current({unexpected:[],trace},condition,{stage:'page'});
 for(const method of ['POST','PUT','PATCH','DELETE'])await assert.rejects(current({unexpected:[],trace:[{method,path:'/nodes'}]},condition,{stage:'page'}),/display\/cancel must not mutate/);
 await assert.rejects(current({unexpected:['unknown'],trace:[]},condition,{stage:'page'}),/unknown API\/external request/);
});

test('UI-UA-STABILITY-030: real observer retains subtree rejection and original cause while recording closed complete delta classes',async()=>{
 const {readUADiagnostic}=await import('../helpers/browser-ua-diagnostic.mts');
 for(const fault of ['stable','decoration-replaced','control-added','owner-replaced','read-and-identity','cleanup-and-identity']){
  const owner=createHarnessFixture();let hostReads=0,axReads=0,releases=0;const sent:string[]=[];
  owner.socket.send=(raw:string)=>{const cmd=JSON.parse(raw);sent.push(cmd.method);let result:Record<string,unknown>={},error:{message:string}|undefined;
   const changed=hostReads>=3,backend=changed&&fault!=='stable'?31:30;
   if(cmd.method==='Page.getFrameTree')result={frameTree:{frame:{id:'DO-NOT-LOG-frame',loaderId:'DO-NOT-LOG-loader'}}};
   if(cmd.method==='Runtime.evaluate')result={result:{objectId:cmd.params.expression==='document'?'document':'host'}};
   if(cmd.method==='DOM.describeNode'){
    if(cmd.params.objectId==='document')result={node:{nodeName:'#document',backendNodeId:1}};
    else{hostReads++;const id=hostReads>=3&&fault!=='stable'?31:30;result={node:{nodeName:'VIDEO',backendNodeId:10,shadowRoots:[{nodeName:'#document-fragment',backendNodeId:hostReads>=3&&fault==='owner-replaced'?21:20,shadowRootType:'user-agent',children:[{nodeName:'SPAN',backendNodeId:id}]}]}};}
   }
   if(cmd.method==='DOM.resolveNode')result={object:{objectId:'leaf'}};
   if(cmd.method==='Runtime.callFunctionOn'){const expression=String(cmd.params.functionDeclaration);result={result:{value:cmd.params.objectId==='leaf'?{indicator:true,visible:true}:expression.includes('matches(')?'media':expression.includes('readyState')?{ready:0,network:3,error:4,source:true,currentSource:true,paused:true,atStart:true}:true}};}
   if(cmd.method==='Accessibility.getFullAXTree'){
    axReads++;const property=(name:string,value:boolean)=>({name,value:{value}});
    result={nodes:[{nodeId:'secret-root',backendDOMNodeId:1,ignored:false,frameId:'DO-NOT-LOG-frame',role:{value:'RootWebArea'},childIds:['secret-host'],properties:[property('focused',true)]},{nodeId:'secret-host',backendDOMNodeId:10,ignored:false,role:{value:'Video'},childIds:['secret-decoration'],properties:[property('focused',true),property('focusable',true),property('disabled',true)]},{nodeId:'secret-decoration',backendDOMNodeId:backend,ignored:false,role:{value:changed&&fault==='control-added'?'button':'StaticText'},name:{value:'DO-NOT-LOG-name'},properties:[property('focusable',changed&&fault==='control-added')]}]};
    if(changed&&fault==='read-and-identity')error={message:'fixed diagnostic read failure'};
   }
   if(cmd.method==='Runtime.releaseObjectGroup'){releases++;if(releases===2&&fault==='cleanup-and-identity')error={message:'fixed diagnostic cleanup failure'};}
   queueMicrotask(()=>owner.socket.respond(cmd,error?{error}:{result}));
  };
  try{
   const first=await owner.harness.observeNativeFocus();assert.ok(readUADiagnostic(first));
   if(fault==='stable'){assert.deepEqual(await owner.harness.observeNativeFocus(),first);assert.equal(readUADiagnostic(first)!.baseline,false);}
   else await assert.rejects(owner.harness.observeNativeFocus(),error=>{
    assert.ok(error instanceof Error);const original=error instanceof AggregateError?error.cause:error;assert.ok(original instanceof Error);assert.equal(original.message,'UA subtree identity replaced during traversal');
    if(fault==='read-and-identity'||fault==='cleanup-and-identity'){assert.ok(error instanceof AggregateError);assert.equal(error.errors[0],original);assert.equal(error.errors.length,2);assert.match(String(error.errors[1]),fault==='read-and-identity'?/read failure/:/cleanup failure/);}
    if(fault!=='read-and-identity'){
     const diagnostic=readUADiagnostic(error)!;assert.ok(diagnostic);assert.ok(diagnostic.delta[0]>0&&diagnostic.delta[1]>0);assert.equal(diagnostic.overflow,false);assert.deepEqual(diagnostic.same!.slice(0,4),[true,true,true,true]);
     assert.equal(diagnostic.focusable,fault==='control-added'?2:1);assert.doesNotMatch(JSON.stringify(diagnostic),/DO-NOT-LOG|secret-|backendNode|nodeId|objectId/);
    }
    return true;
   });
   assert.ok(axReads>=3,'current delta obtains an actual subsequent AX snapshot, not only a DOM-ID mismatch');assert.equal(releases,2);
   assert.equal(sent.some(m=>/Input\.|Fetch\.|DOM\.focus|Target\./.test(m)),false);
  }finally{await owner.harness.close();}
 }
});

test('UI-UA-WRITER-030: actual condition writer bounds closed output, preserves cause identity, and stops after output failure',async()=>{
 const {UAConditionDiagnostic,bindUADiagnostic,UAOutputFailure}=await import('../helpers/browser-ua-diagnostic.mts');
 const {createConditionRunner,ConditionFailure}=await import('./condition-lifecycle.mts');
 const {inventory}=await import('./matrix.mts');
 const record={boundary:'between',baseline:true,total:4,delta:[1,1,0],classes:[[{classification:'SPAN|StaticText|1|0|0|0|0',count:1}],[{classification:'SPAN|StaticText|1|0|0|0|0',count:1}],[]],same:[true,true,true,true,true,true,true],mediaStates:["error","error"],focusable:1,focused:2,unknown:1,overflow:false} as const;
 for(const fault of ['none','primary','writer','primary-writer','overflow']){
  const diagnostic=new UAConditionDiagnostic(),primary=new Error('DO-NOT-LOG secret-value'),output=new Error('fixed writer failure');const saved:unknown[]=[];
  const owner=createHarnessFixture();let launches=0,attempts=0;const condition=conditions.find(c=>c.family==='public-archive-share'&&c.exercise==='keyboard')!,surface=inventory.surfaces.find(s=>s.id===condition.family)!;
  const write=(name:string,value:unknown)=>{if(name.endsWith('.ua-stability.json')){attempts++;if(fault.includes('writer'))throw output;saved.push(value);}};
  const run=createConditionRunner('http://ui.test',write,async()=>{launches++;return owner.harness;});
  const operation=async()=>{
   let cause:unknown;try{
    const value={};bindUADiagnostic(fault.startsWith('primary')?primary:value,record as unknown as Parameters<typeof bindUADiagnostic>[1]);
    await diagnostic.observe('negative-enter',async()=>{if(fault.startsWith('primary'))throw primary;return value;});
    if(fault==='overflow')for(let i=0;i<128;i++)await diagnostic.observe('negative-space-return',async()=>value);
   }catch(error){cause=error;throw error;}finally{
    try{diagnostic.write(condition.id,value=>write(condition.id+'.ua-stability.json',value));}
    catch(error){throw cause?new AggregateError([cause,error],'primary and diagnostic writer',{cause}):error;}
   }
  };
  if(fault==='none')await run(condition,surface,operation);
  else await assert.rejects(run(condition,surface,operation),error=>{
   assert.ok(error instanceof ConditionFailure);const cause=error.cause;
   if(fault==='primary')assert.equal(cause,primary);
   if(fault==='writer'){assert.ok(cause instanceof UAOutputFailure);assert.equal(cause.cause,output);}
   if(fault==='primary-writer'){assert.ok(cause instanceof AggregateError);assert.equal(cause.cause,primary);assert.equal(cause.errors[0],primary);assert.equal(cause.errors[1].cause,output);}
   if(fault.includes('writer')||fault==='overflow')assert.equal(error.stop,true);
   return true;
  });
  assert.equal(attempts,1);assert.equal(owner.socket.commandsFor('Browser.close').length,1);
  if(fault.includes('writer')||fault==='overflow'){await assert.rejects(run({...condition,id:condition.id+'-next'},surface,async()=>{}),/stopped/);assert.equal(launches,1);}
  for(const value of saved){assert.ok(Buffer.byteLength(JSON.stringify(value,null,2)+'\n')<=4096);assert.doesNotMatch(JSON.stringify(value),/DO-NOT-LOG|secret-value|fixed writer/);}
  if(fault==='overflow')assert.equal((saved[0]as {overflow:boolean}).overflow,true);
 }
 let writes=0;const normal=new UAConditionDiagnostic();await normal.observe('tab-forward',async()=>({}));normal.write('fixed-condition',()=>writes++);assert.equal(writes,0,'stable success does not flood the writer');const invalid=new UAConditionDiagnostic(),value={};bindUADiagnostic(value,record as unknown as Parameters<typeof bindUADiagnostic>[1]);await invalid.observe('negative-enter',async()=>value);assert.throws(()=>invalid.write('/DO-NOT-LOG/raw',()=>{throw Error('must not write invalid identity');}),/outside closed ID/);
 const {safeKeyboardTrace}=await import('./observation.mts');
 const condition=conditions.find(c=>c.family==='public-archive-share'&&c.exercise==='keyboard')!;
 const size=(value:unknown)=>Buffer.byteLength(JSON.stringify(value,null,2)+'\n');
 const overhead=size(safeKeyboardTrace({...condition,id:'x'},[]))-1;
 const exact={...condition,id:'x'.repeat(4096-overhead)};
 assert.equal(size(safeKeyboardTrace(exact,[])),4096,'actual serialized boundary includes pretty spacing and newline');
 assert.throws(()=>safeKeyboardTrace({...exact,id:exact.id+'x'},[]),/serialized keyboard trace exceeds output bound/);
 assert.throws(()=>safeKeyboardTrace({...condition,id:'../DO-NOT-LOG'},[]));
});

test('UI-UA-CALLER-030: real current writer callback and keyboard exercise preserve observer, output and marker failures without another condition',async t=>{
 const {bindUADiagnostic}=await import('../helpers/browser-ua-diagnostic.mts');
 const {createConditionRunner,ConditionFailure}=await import('./condition-lifecycle.mts');
 const {exerciseAccessibility:actualExercise}=await import('./observation.mts');
 const {inventory}=await import('./matrix.mts');
 const source=readFileSync(new URL('./run-browser.mts',import.meta.url),'utf8');
 const expression=source.match(/const accessibility = (await exerciseAccessibility\([^\n]+);/)?.[1];assert.ok(expression);
 const caller=new Function('exerciseAccessibility','target','condition','write','return (async()=>'+expression+')();') as (exercise:typeof actualExercise,browser:BrowserHarness,condition:typeof conditions[number],write:(name:string,value:unknown)=>void)=>Promise<unknown>;
 const condition=conditions.find(c=>c.family==='public-archive-share'&&c.exercise==='keyboard')!,surface=inventory.surfaces.find(s=>s.id===condition.family)!;
 for(const fault of ['primary','primary-writer','primary-marker','primary-writer-marker','caller-type']){
  const primary=new Error('UA subtree identity replaced during traversal'),output=new Error('closed writer cause'),marker=new Error('closed marker cause');
  bindUADiagnostic(primary,{boundary:'between',baseline:true,total:3,delta:[1,1,0],classes:[[],[],[]],same:[true,true,true,true,true,true,true],mediaStates:["error","error"],focusable:1,focused:2,unknown:1,overflow:false});
  const dom=observerDOM();dom.main.children=[];const video=dom.main.add(new Element('VIDEO'));video.setAttribute('controls','');const link=dom.main.add(new Element('A','Open directly'));link.setAttribute('href','/archive-shares/ui-synthetic-share/download');dom.document.activeElement=dom.body;
  const owner=createHarnessFixture(),tab=owner.harness.pressTab.bind(owner.harness);let observations=0,clears=0,launches=0;const writes:{name:string;value:unknown}[]=[];
  owner.harness.pressTab=async direction=>{await tab(direction);dom.document.activeElement=video;};
  owner.harness.observeNativeFocus=async()=>{observations++;if(fault==='caller-type'){const value={document:1,host:2,node:3,kind:'datetime',stable:true,visible:true,indicator:true} as Awaited<ReturnType<BrowserHarness['observeNativeFocus']>>;bindUADiagnostic(value,{boundary:'between',baseline:true,total:3,delta:[0,0,0],classes:[[],[],[]],same:[true,true,true,true,true,true,true],mediaStates:["error","error"],focusable:1,focused:2,unknown:1,overflow:false});return value;}throw primary;};
  owner.harness.evaluate=async<T,>(expression:string)=>{if(expression==='delete globalThis.__uiKeyboardPlan;true'){clears++;if(fault.includes('marker'))throw marker;}return dom.run<T>(expression);};
  const write=(name:string,value:unknown)=>{writes.push({name,value});if(name.endsWith('.ua-stability.json')&&fault.includes('writer'))throw output;};
  const run=createConditionRunner('http://ui.test',write,async()=>{launches++;return owner.harness;});
  await assert.rejects(run(condition,surface,browser=>caller(actualExercise,browser,condition,write)),error=>{
   assert.ok(error instanceof ConditionFailure);assert.equal(error.stage,'exercise');assert.equal(error.stop,fault.includes('writer'));
   if(fault==='caller-type'){assert.ok(error.cause instanceof Error);assert.match(error.cause.message,/UA host type mismatch/);}else if(fault==='primary')assert.equal(error.cause,primary);else{
    assert.ok(error.cause instanceof AggregateError);assert.equal(error.cause.cause,primary);assert.equal(error.cause.errors[0],primary);
    if(fault.includes('writer'))assert.equal(error.cause.errors[1].cause,output);
    if(fault.includes('marker'))assert.equal(error.cause.errors.at(-1),marker);
   }return true;
  });
  assert.equal(observations,1);assert.equal(clears,1);assert.equal(owner.socket.commandsFor('Input.dispatchKeyEvent').length,2);assert.equal(owner.socket.commandsFor('Browser.close').length,1);
  assert.equal('__uiKeyboardPlan'in dom.context,fault.includes('marker'));
  assert.equal(writes.filter(r=>r.name.endsWith('.keyboard-trace.json')).length,1);const evidence=writes.filter(r=>r.name.endsWith('.ua-stability.json'));assert.equal(evidence.length,1);
  assert.ok(Buffer.byteLength(JSON.stringify(evidence[0].value,null,2)+'\n')<=4096);assert.doesNotMatch(JSON.stringify(evidence[0].value),/subtree identity|writer cause|marker cause|Open directly/);
  if(fault.includes('writer')){await assert.rejects(run({...condition,id:condition.id+'-next'},surface,async()=>{}),/stopped/);assert.equal(launches,1);}
 }
 // 031: real run-browser callback -> exercise -> real condition owner/harness.
 // Controlled I/O boundaries are not counted as native conditions or matrix IDs.
 const {UAOutputFailure,isUAOutputFailure}=await import('../helpers/browser-ua-diagnostic.mts');
 const form=conditions.find(c=>c.family==='stream-create-edit'&&c.exercise==='keyboard')!;assert.ok(form);
 for(const fault of ['normal','trace','serialize','generation','bound','marker','trace-marker','body','body-trace','body-ua','body-both','body-marker','body-trace-marker','body-ua-marker','body-both-marker'])await t.test('031-'+fault,async sub=>{
  const bodyFailure=fault.startsWith('body'),traceFailure=fault.includes('trace')||fault.includes('both'),uaFailure=fault.includes('ua')||fault.includes('both'),markerFailure=fault.includes('marker');
  const primary=new Error('DO-NOT-LOG primary'),traceError=new Error('DO-NOT-LOG trace'),uaError=new Error('DO-NOT-LOG ua'),markerError=new Error('DO-NOT-LOG marker'),generationError=new Error('DO-NOT-LOG generation');
  const ownedCondition=fault==='bound'?{...form,id:'x'.repeat(4096)}:bodyFailure?condition:form;
  const ownedSurface=inventory.surfaces.find(s=>s.id===ownedCondition.family)!;
  const dom=observerDOM();dom.main.children=[];dom.document.activeElement=dom.body;
  const video=dom.main.add(new Element('VIDEO'));video.setAttribute('controls','');
  const link=dom.main.add(new Element('A','Open directly'));link.setAttribute('href','/archive-shares/ui-synthetic-share/download');
  const dialog=dom.body.add(new Element('DIV'));dialog.setAttribute('role','dialog');
  const card=dialog.add(new Element('DIV'));card.id='create-stream';
  const content=card.add(new Element('DIV'));content.setAttribute('data-slot','card-content');
  const nav=content.add(new Element('NAV'));nav.setAttribute('data-slot','section-navigation');
  const sectionButton=nav.add(new Element('BUTTON','Basic'));sectionButton.setAttribute('aria-controls','create-stream-basic');
  const section=content.add(new Element('SECTION'));section.id='create-stream-basic';
  const close=dialog.add(new Element('BUTTON','Close'));dialog.hidden=bodyFailure;
  if(!bodyFailure)dom.main.children=[];
  const navigate=actualJSXCallback(readFileSync(new URL('../../src/components/layout/detail-section.tsx',import.meta.url),'utf8'),'button','onClick',{item:{id:section.id},document:dom.document});
  const owner=createHarnessFixture(),nativeTab=owner.harness.pressTab.bind(owner.harness),nativeKey=owner.harness.pressNativeKey.bind(owner.harness);
  let clears=0,launches=0,nextExercises=0,writerReached=0;const events:string[]=[],writes:{name:string;value:unknown}[]=[];
  owner.harness.pressTab=async direction=>{await nativeTab(direction);if(bodyFailure){dom.document.activeElement=video;return;}const nodes=[sectionButton,close],i=nodes.indexOf(dom.document.activeElement);dom.document.activeElement=nodes[(i+(direction==='forward'?1:-1)+nodes.length)%nodes.length];};
  owner.harness.pressNativeKey=async key=>{await nativeKey(key);sectionButton.dispatchEvent(new Event('click'));navigate();};
  owner.harness.observeNativeFocus=async()=>{throw primary;};
  owner.harness.evaluate=async<T,>(expression:string)=>{
   if(expression==='delete globalThis.__uiKeyboardPlan;true'){clears++;events.push('marker');if(markerFailure)throw markerError;}
   const value=dom.run<T>(expression);
   if(expression===focusExpression&&fault==='generation')Object.defineProperty(value,'rect',{get(){throw generationError;}});
   return value;
  };
  const closeBrowser=owner.harness.close.bind(owner.harness);owner.harness.close=async()=>{events.push('close');await closeBrowser();};
  const write=(name:string,value:unknown)=>{
   writes.push({name,value});if(name.endsWith('.keyboard-trace.json')){
    writerReached++;events.push('trace');
    if(fault==='serialize')JSON.stringify(value,()=>{throw traceError;});
    if(traceFailure)throw traceError;
   }
   if(name.endsWith('.ua-stability.json')){events.push('ua');if(uaFailure)throw uaError;}
  };
  const nextOwner=createHarnessFixture();
  sub.after(()=>nextOwner.harness.close());sub.after(()=>closeBrowser());
  const run=createConditionRunner('http://ui.test',write,async()=>{launches++;return launches===1?owner.harness:nextOwner.harness;});
  let failure:unknown;
  try{const result=await run(ownedCondition,ownedSurface,browser=>caller(actualExercise,browser,ownedCondition,write));assert.equal(fault,'normal');assert.deepEqual(result,{pending:[]});}catch(error){failure=error;}
  const outputFailure=traceFailure||uaFailure||['serialize','generation','bound'].includes(fault);
  if(fault!=='normal'){
   assert.ok(failure instanceof ConditionFailure,fault);assert.equal(failure.stage,'exercise');assert.equal(failure.stop,outputFailure,fault);assert.equal(isUAOutputFailure(failure.cause),outputFailure);
   const causes=failure.cause instanceof AggregateError?failure.cause.errors:[failure.cause];
   if(bodyFailure){assert.equal(causes[0],primary);if(failure.cause instanceof AggregateError)assert.equal(failure.cause.cause,primary);}
   const outputs=causes.filter((e:unknown)=>e instanceof UAOutputFailure);
   assert.equal(outputs.length,(traceFailure||['serialize','generation','bound'].includes(fault)?1:0)+(uaFailure?1:0));
   if(traceFailure||fault==='serialize')assert.equal(outputs[0].cause,traceError);
   if(fault==='generation')assert.equal(outputs[0].cause,generationError);
   if(fault==='bound'){assert.ok(outputs[0].cause instanceof assert.AssertionError);assert.match(outputs[0].cause.message,/serialized keyboard trace exceeds output bound/);}
   if(uaFailure)assert.equal(outputs.at(-1)!.cause,uaError);
   if(markerFailure)assert.equal(causes.at(-1),markerError);
  }else assert.equal(failure,undefined);
  assert.equal(clears,1,fault);assert.equal(owner.socket.commandsFor('Browser.close').length,1);assert.equal('__uiKeyboardPlan'in dom.context,markerFailure);
  assert.ok(events.indexOf('marker')<events.indexOf('close'));assert.equal(writerReached,['generation','bound'].includes(fault)?0:1);
  assert.equal(writes.filter(r=>r.name.endsWith('.ua-stability.json')).length,bodyFailure?1:0,'stable success has no UA diagnostics');
  const commands=owner.socket.commandsFor('Input.dispatchKeyEvent');assert.equal(commands.length,bodyFailure?2:20,'same native down/up order, no key retry');
  assert.deepEqual(commands.filter((_,i)=>i%2===0).map(c=>c.params!.code),bodyFailure?['Tab']:['Tab','Tab','Tab','Tab','Tab','Tab','Tab','Enter','Tab','Space']);
  for(const row of writes.filter(r=>/\.(keyboard-trace|ua-stability)\.json$/.test(r.name))){assert.ok(Buffer.byteLength(JSON.stringify(row.value,null,2)+'\n')<=4096);assert.doesNotMatch(JSON.stringify(row.value),/DO-NOT-LOG|Open directly|Basic/);}
  const next={...form,id:form.id+'-next'};
  if(outputFailure){await assert.rejects(run(next,ownedSurface,async()=>{nextExercises++;}),/stopped/);assert.equal(launches,1);assert.equal(nextExercises,0);assert.equal(nextOwner.socket.commandsFor('Browser.close').length,0);await nextOwner.harness.close();}
  else{await run(next,ownedSurface,async()=>{nextExercises++;});assert.equal(launches,2);assert.equal(nextExercises,1);assert.equal(nextOwner.socket.commandsFor('Browser.close').length,1);}
 });
});

test('UI-UA-BOUNDARY-030: successful and late frame/media paths keep the actual signed029 readonly command order',async()=>{
 const {execFileSync}=await import('node:child_process');const {fileURLToPath}=await import('node:url');
 const {default:ts}=await import('typescript');const {NativeFocusObserver:Current}=await import('../helpers/browser-ua-focus.mts');
 const original=execFileSync('git',['show','b77ea391731b3288f4c994c25b75bba2caac7a08:web/tests/helpers/browser-ua-focus.mts'],{cwd:fileURLToPath(new URL('../../..',import.meta.url)),encoding:'utf8'});
 const compiled=ts.transpileModule(original,{compilerOptions:{module:ts.ModuleKind.CommonJS,target:ts.ScriptTarget.ES2022,esModuleInterop:true}}).outputText;
 const exports:{NativeFocusObserver?:typeof Current}={};new Function('require','exports',compiled)((name:string)=>{assert.equal(name,'node:assert/strict');return assert;},exports);assert.ok(exports.NativeFocusObserver);
 for(const kind of ['media','datetime'])for(const fault of ['none','final-frame',...(kind==='media'?['final-media']:[])]){
  const runs: {commands:unknown[];value?:unknown;error?:unknown}[]=[];
  for(const Observer of [exports.NativeFocusObserver,Current]){
   const commands:unknown[]=[];let frames=0,media=0;const property=(name:string,value:boolean)=>({name,value:{value}});
   const observer=new Observer(async(method,params)=>{
    commands.push({method,params});
    if(method==='Page.getFrameTree'){frames++;return {frameTree:{frame:{id:'fixed-frame',loaderId:fault==='final-frame'&&frames===3?'other-loader':'fixed-loader'}}};}
    if(method==='Runtime.evaluate')return {result:{objectId:params?.expression==='document'?'document':'host'}};
    if(method==='DOM.describeNode')return {node:params?.objectId==='document'?{nodeName:'#document',backendNodeId:1}:{nodeName:kind==='media'?'VIDEO':'INPUT',backendNodeId:10,shadowRoots:[{nodeName:'#document-fragment',backendNodeId:20,shadowRootType:'user-agent',children:[{nodeName:kind==='media'?'SPAN':'INPUT',backendNodeId:30}]}]}};
    if(method==='Runtime.callFunctionOn'){
     const expression=String(params?.functionDeclaration);if(expression.includes('readyState')){media++;return {result:{value:{ready:0,network:3,error:fault==='final-media'&&media===2?3:4,source:true,currentSource:true,paused:true,atStart:true}}};}
     return {result:{value:params?.objectId==='leaf'?{indicator:true,visible:true}:expression.includes('matches(')?kind:true}};
    }
    if(method==='DOM.resolveNode')return {object:{objectId:'leaf'}};
    if(method==='Accessibility.getFullAXTree')return {nodes:[{nodeId:'root',backendDOMNodeId:1,frameId:'fixed-frame',ignored:false,role:{value:'RootWebArea'},childIds:['host'],properties:[property('focused',true)]},{nodeId:'host',backendDOMNodeId:10,ignored:false,role:{value:kind==='media'?'Video':'generic'},childIds:['leaf'],properties:kind==='media'?[property('focused',true),property('focusable',true),property('disabled',true)]:[]},{nodeId:'leaf',backendDOMNodeId:30,ignored:false,role:{value:kind==='media'?'StaticText':'spinbutton'},properties:kind==='datetime'?[property('focused',true),property('focusable',true)]:[]}]};
    return {};
   });
   try{runs.push({commands,value:await observer.observe()});}catch(error){runs.push({commands,error});}finally{observer.clear();}
  }
  assert.deepEqual(runs[1].commands,runs[0].commands,'normal and original late-failure boundaries must not move or gain sends');
  if(fault==='none'){assert.equal(runs[0].error,undefined);assert.deepEqual(runs[1].value,runs[0].value);}else{
   for(const run of runs){assert.ok(run.error instanceof Error);assert.match(run.error.message,fault==='final-frame'?/document changed after paint/:/media state changed during identity/);}
  }
 }
});
