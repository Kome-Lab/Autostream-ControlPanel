import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";
import ts from "typescript";
import type { BrowserHarness } from "../helpers/browser-harness.mts";
import { createHarnessFixture } from "../helpers/browser-cdp-socket-fixture.mts";
import { observerDOM, Element } from "./observer-dom.mts";
import { conditions } from "./matrix.mts";
import { keyboardContract, exerciseKeyboardPath, prepareKeyboardExpression } from "./observation.mts";
import { actualCallback, actualJSXCallback } from "./source-callback.mts";
import { clickNamed } from "./visible-trigger.mts";
import { settleRender } from "./render-state.mts";

test("current creation page uses its Back action and exact new owner; a failed Back or legacy dialog cannot pass",async()=>{
  const text=readFileSync(new URL('./run-browser.mts',import.meta.url),'utf8'),file=ts.createSourceFile('runner.mts',text,ts.ScriptTarget.Latest,true,ts.ScriptKind.TS),blocks:ts.Block[]=[];
  const visit=(node:ts.Node)=>{if(ts.isIfStatement(node)&&node.expression.getText(file)==='surface.stage === "form"'&&ts.isBlock(node.thenStatement)&&node.thenStatement.getText(file).includes('deep-link stream creation page'))blocks.push(node.thenStatement);ts.forEachChild(node,visit);};visit(file);assert.equal(blocks.length,1);
  for(const fault of ['none','back-failed','back-did-not-close','legacy-dialog','duplicate-owner']){
    const dom=observerDOM();dom.main.children=[];
    const list=dom.main.add(new Element('DIV')),trigger=list.add(new Element('BUTTON','Create stream'));list.hidden=true;
    let owner:Element,back:Element,clicks=0;const original=Error('Back action failure');
    const open=()=>{owner=dom.main.add(new Element('SECTION'));owner.setAttribute('data-stream-create-view','');back=owner.add(new Element('BUTTON','Back / Cancel'));back.setAttribute('aria-label','Back to stream list');owner.add(new Element('DIV')).id='create-stream';dom.document.activeElement=back;list.hidden=true;};open();
    if(fault==='legacy-dialog'){owner!.removeAttribute('data-stream-create-view');owner!.setAttribute('role','dialog');}
    if(fault==='duplicate-owner')dom.main.add(new Element('SECTION')).setAttribute('data-stream-create-view','');
    dom.document.elementFromPoint=()=>Reflect.get(dom.context,'__uiScenarioTarget') as Element||trigger;
    const target={
      evaluate:async<T,>(expression:string)=>dom.run<T>(expression),
      async waitFor<T,>(expression:string,predicate:(value:T)=>boolean){for(let attempt=0;attempt<5;attempt++){const value=dom.run<T>(expression);if(predicate(value))return value;}throw Error('required page owner or measured trigger was not reached');},
      async clickAt(){clicks++;const selected=Reflect.get(dom.context,'__uiScenarioTarget');if(selected===back){if(fault==='back-failed')throw original;if(fault==='back-did-not-close')return;dom.main.children=dom.main.children.filter(e=>e!==owner);owner.parentElement=null;list.hidden=false;dom.document.activeElement=trigger;}else{assert.equal(selected,trigger);open();}},
      assertNoFatalError(){},
    } as unknown as BrowserHarness;
    const stage=actualCallback('const selected=async()=>'+blocks[0].getText(file),'selected',{target,condition:{locale:'en'},settleRender,clickNamed,reportRender:()=>{}});
    if(fault==='none'){await stage();assert.equal(clicks,2);assert.equal(dom.main.querySelectorAll('[data-stream-create-view]').length,1);assert.equal(Reflect.get(dom.context,'__uiReturnTrigger'),trigger);assert.equal(list.hidden,true);}
    else {await assert.rejects(stage(),error=>fault==='back-failed'?error===original:/page owner|page owner or measured trigger/.test((error as Error).message));assert.equal(clicks,fault.startsWith('back-')?1:0);}
  }
});

test("current creation keyboard maps the semantic exit and traverses every page control forward and backward without modal wrapping",async()=>{
  for(const locale of ['ja','en'])for(const fault of ['none','missing-back','duplicate-owner','foreign-dialog','skip-control','independent-date-edit']){
    const condition=conditions.find(c=>c.family==='stream-create-edit'&&c.exercise==='keyboard'&&c.locale===locale)!;
    const contract=keyboardContract(condition);assert.equal(contract.required[1].selector,'[role=dialog] button');
    const dom=observerDOM();dom.main.children=[];
    const outside=dom.body.add(new Element('BUTTON','Shell control')),page=dom.main.add(new Element('SECTION'));page.setAttribute('data-stream-create-view','');
    const query=page.querySelectorAll.bind(page);page.querySelectorAll=selector=>query(selector).sort((a,b)=>page.all().indexOf(a)-page.all().indexOf(b));
    const heading=page.add(new Element('H1','Create stream')),back=page.add(new Element('BUTTON','Back / Cancel'));back.setAttribute('aria-label',fault==='missing-back'?'Other action':locale==='ja'?'配信枠の一覧へ戻る':'Back to stream list');
    const form=page.add(new Element('DIV'));form.id='create-stream';const content=form.add(new Element('DIV'));content.setAttribute('data-slot','card-content');
    const nav=content.add(new Element('NAV'));nav.setAttribute('data-slot','section-navigation');const basic=nav.add(new Element('BUTTON','Basic'));basic.setAttribute('aria-controls','create-stream-basic');
    const section=content.add(new Element('SECTION'));section.id='create-stream-basic';const name=section.add(new Element('INPUT')),date=content.add(new Element('INPUT'));date.type='datetime-local';date.value='2026-09-01T12:34';
    const last=content.add(new Element('BUTTON','Watermark'));last.setAttribute('role','checkbox');
    if(fault==='duplicate-owner')dom.main.add(new Element('SECTION')).setAttribute('data-stream-create-view','');
    if(fault==='foreign-dialog')dom.body.add(new Element('DIV')).setAttribute('role','dialog');
    dom.document.activeElement=heading;
    const owner=createHarnessFixture(),nativeTab=owner.harness.pressTab.bind(owner.harness),nativeKey=owner.harness.pressNativeKey.bind(owner.harness),visits:Element[]=[],nodes=[outside,back,basic,name,date,last];
    Object.defineProperty(owner.harness,'observeNativeFocus',{value:undefined,writable:true});
    owner.harness.evaluate=async<T,>(expression:string)=>dom.run<T>(expression);
    owner.harness.waitFor=async<T,>(expression:string,predicate:(value:T)=>boolean)=>{const value=dom.run<T>(expression);assert.ok(predicate(value));return value;};
    owner.harness.pressTab=async direction=>{await nativeTab(direction);const current=nodes.indexOf(dom.document.activeElement),next=current<0?1:(current+(direction==='forward'?1:-1)+nodes.length)%nodes.length;dom.document.activeElement=nodes[fault==='skip-control'&&direction==='forward'&&next===3?4:next];visits.push(dom.document.activeElement);if(fault==='independent-date-edit'&&dom.document.activeElement===last)date.value='independent edit';};
    const activate=actualJSXCallback(readFileSync(new URL('../../src/components/layout/detail-section.tsx',import.meta.url),'utf8'),'button','onClick',{document:dom.document,item:{id:section.id}});
    owner.harness.pressNativeKey=async key=>{await nativeKey(key);basic.dispatchEvent(new Event('click'));activate();};
    try {
      if(fault==='none'){
        const plan=await owner.harness.evaluate<{completePage:boolean;modal:boolean;order:string[]}>(prepareKeyboardExpression(contract));assert.equal(plan.completePage,true);assert.equal(plan.modal,false);assert.equal(plan.order.length,5);
        const result=await exerciseKeyboardPath(owner.harness,condition);assert.deepEqual(result.pending,['UA_DATETIME_SEGMENT_IDENTITY_PENDING']);
        assert.deepEqual(visits.slice(0,11),[back,basic,name,date,last,date,name,basic,back,outside,back]);assert.equal(date.value,'2026-09-01T12:34');
        assert.equal(owner.socket.commandsFor('Input.dispatchKeyEvent').filter(command=>command.params.key==='Tab').length>=22,true);
      }else await assert.rejects(exerciseKeyboardPath(owner.harness,condition),/keyboard|creation page|Tab skipped|datetime|owner/);
    }finally{await owner.harness.close();}
  }
});
