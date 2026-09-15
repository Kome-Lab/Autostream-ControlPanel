import "./component-loader.mts";
import assert from "node:assert/strict";
import { createElement, useId, type ComponentType } from "react";
import { readFileSync } from 'node:fs';
import { createRequire } from 'node:module';
import { fileURLToPath } from 'node:url';
import { createHash } from 'node:crypto';
import postcss from 'postcss';
import tailwind from '@tailwindcss/postcss';
import ts from 'typescript';
import { renderUI } from './render-ui.mts';
import { renderToStaticMarkup } from "react-dom/server";
import test from "node:test";
import { Element, observerDOM } from './observer-dom.mts';
import { observationExpression, assertObservation, type UIObservation } from './observation.mts';
import { conditions } from './matrix.mts';
import { actualJSXCallback, actualCallback } from './source-callback.mts';
import { createDraftExitController } from '../../src/lib/ui-v2/draft-exit-controller.ts';

const { Field } = await import("../../src/components/forms/field.tsx");
const { DetailSection, SectionNavigation } = await import("../../src/components/layout/detail-section.tsx");
const { Input }=await import('../../src/components/ui/input.tsx');
const { createUICopy }=await import('../../src/lib/i18n/ui-v2/copy.ts');

function actualMarkupDOM(html:string){
 const dom=observerDOM();dom.main.children=dom.main.children.filter(e=>e.tagName==='H1');const stack=[dom.main];
 for(const token of html.matchAll(/<\/?([a-z][\w-]*)\b([^>]*)>|([^<]+)/gi)){
   if(token[3]){stack.at(-1)!.ownText+=token[3];continue;}const tag=token[1].toUpperCase();
   if(token[0].startsWith('</')){if(stack.at(-1)?.tagName===tag)stack.pop();continue;}
   const e=stack.at(-1)!.add(new Element(tag));for(const a of token[2].matchAll(/([\w-]+)="([^"]*)"/g)){
     e.setAttribute(a[1],a[2]);if(a[1]==='type')e.type=a[2];if(a[1]==='hidden')e.hidden=true;
     if(a[1]==='style')for(const entry of a[2].split(';')){const [key,...parts]=entry.split(':');if(key)Object.assign(e.style,{[key.replace(/-([a-z])/g,(_,letter:string)=>letter.toUpperCase())]:parts.join(':')});}
   }
   if(e.style.position==='absolute'&&e.getAttribute('aria-hidden')==='true'){e.rect.width=1;e.rect.height=1;}
   // CSSOM expands unitless zero lengths in the actual inline clip declaration.
   if(/^rect\(0,?\s*0,?\s*0,?\s*0\)$/.test(e.style.clip))e.style.clip='rect(0px, 0px, 0px, 0px)';
   if(!['INPUT','SELECT','IMG','BR','HR','META','LINK'].includes(tag))stack.push(e);else if(tag==='SELECT')stack.push(e);
 }
 for(const label of dom.document.querySelectorAll('label[for]')){const control=dom.document.getElementById(label.htmlFor);if(control)control.labels.push(label);}
 return dom;
}
test('UI-VISUAL-LABEL-013: all six actual ModeSelect calls link unique localized visible triggers and native proxies',async()=>{
 const text=readFileSync(new URL('../../src/features/streams/stream-visual-settings-section.tsx',import.meta.url),'utf8'),file=ts.createSourceFile('visual.tsx',text,ts.ScriptTarget.Latest,true,ts.ScriptKind.TSX);
 const calls:string[]=[];const visit=(node:ts.Node)=>{if(ts.isJsxSelfClosingElement(node)&&node.tagName.getText(file)==='ModeSelect')calls.push(node.getText(file));ts.forEachChild(node,visit);};visit(file);assert.equal(calls.length,6);
 const helper=file.statements.find(n=>ts.isFunctionDeclaration(n)&&n.name?.text==='ModeSelect');assert.ok(helper);
 const code=ts.transpileModule(helper.getText(file)+'\nfunction Fields(){return <>'+calls.join('\n')+'</>};exports.Fields=Fields;',{compilerOptions:{jsx:ts.JsxEmit.ReactJSX,module:ts.ModuleKind.CommonJS,target:ts.ScriptTarget.ES2022}}).outputText;
 const selects=await import('../../src/components/ui/select.tsx'),{defaultStreamVisualDraft}=await import('../../src/features/streams/stream-visual-draft.ts');
 for(const locale of ['ja','en'] as const){
   const output:{Fields?:ComponentType}={},values={...selects,useId,locale,draft:defaultStreamVisualDraft(),controlsDisabled:false,uploadedBackground:false,uploadedCover:false,discordRows:[],coverRows:[],rowString:()=>'',update(){assert.fail('label rendering must not update');}};
   new Function('exports','require',...Object.keys(values),code)(output,createRequire(import.meta.url),...Object.values(values));assert.ok(output.Fields);
   const html=renderToStaticMarkup(createElement('div',null,createElement(output.Fields),createElement(output.Fields))),dom=actualMarkupDOM(html);
   const peers=dom.document.querySelectorAll('[role=combobox]'),labels=dom.document.querySelectorAll('label');assert.equal(peers.length,12);assert.equal(labels.length,12);assert.equal(new Set(labels.map(l=>l.textContent)).size,6);
   for(const peer of peers){const labelsFor=labels.filter(label=>label.htmlFor===peer.id);assert.equal(labelsFor.length,1);assert.equal(peer.getAttribute('aria-labelledby'),labelsFor[0].id);assert.ok(labelsFor[0].textContent.trim());assert.doesNotMatch(labelsFor[0].getAttribute('class')||'',/hidden|sr-only/);}
   assert.match(html,locale==='ja'?/シーン背景モード/:/Scene background mode/);assert.match(html,locale==='ja'?/Video Coverプリセット/:/Video Cover preset/);
   const condition={...conditions[0],locale:'en',mode:'light',theme:'autostream'};assertObservation(dom.run<UIObservation>(observationExpression),condition);
   for(const fault of ['missing','wrong','empty','duplicate']){const bad=actualMarkupDOM(html),peer=bad.document.querySelector('[role=combobox]')!,label=bad.document.getElementById(peer.getAttribute('aria-labelledby')!)!;
     if(fault==='missing')label.id='missing';if(fault==='wrong')peer.setAttribute('aria-labelledby','wrong');if(fault==='empty')label.ownText='';if(fault==='duplicate')bad.main.add(new Element('LABEL','Duplicate')).id=label.id;
     assert.throws(()=>assertObservation(bad.run(observationExpression),condition),/reference|duplicate|unnamed/);
   }
 }
});
test('UI-NODE-DIALOG-013: actual controlled root and trigger retain the draft decision and one cleanup focus target',async()=>{
 const text=readFileSync(new URL('../../src/features/nodes/node-registration-view.tsx',import.meta.url),'utf8'),file=ts.createSourceFile('nodes.tsx',text,ts.ScriptTarget.Latest,true,ts.ScriptKind.TSX);
 let roots=0,triggers=0;const visit=(n:ts.Node)=>{if(ts.isJsxElement(n)&&n.openingElement.tagName.getText(file)==='Dialog'){roots++;assert.match(n.getText(file),/<DialogTrigger asChild>/);}if(ts.isJsxElement(n)&&n.openingElement.tagName.getText(file)==='DialogTrigger'){triggers++;assert.match(n.getText(file),/Nodeを新規作成/);assert.doesNotMatch(n.getText(file),/onClick=/);}ts.forEachChild(n,visit);};visit(file);assert.equal(roots,1);assert.equal(triggers,1);
 const {NodeRegistrationView}=await import('../../src/features/nodes/node-registration-view.tsx');for(const locale of ['ja','en'] as const){const html=renderUI(createElement(NodeRegistrationView),locale,'/admin/nodes/');assert.match(html,/<button[^>]*data-slot="dialog-trigger"[^>]*>/);assert.match(html,locale==='ja'?/Nodeを新規作成/:/Create node/);}
 const radix=readFileSync(createRequire(import.meta.url).resolve('@radix-ui/react-dialog'),'utf8'),parsed=ts.createSourceFile('dialog.js',radix,ts.ScriptTarget.Latest,true,ts.ScriptKind.JS);let restore:ts.ArrowFunction|undefined;
 const find=(n:ts.Node)=>{if(ts.isArrowFunction(n)&&n.getText(parsed).includes('context.triggerRef.current?.focus()')&&!n.getText(parsed).includes('return')&&!n.getText(parsed).includes('hasInteractedOutsideRef'))restore=n;ts.forEachChild(n,find);};find(parsed);assert.ok(restore,'actual Radix modal focus cleanup callback');
 const dom=observerDOM(),target=dom.button;let focuses=0;target.focus=()=>{focuses++;dom.document.activeElement=target;};
 const cleanup=actualCallback('const cleanup='+restore.getText(parsed),'cleanup',{context:{triggerRef:{current:target}}});
 const state={open:false,dirty:false,pending:false,discard:false};const guard=createDraftExitController({enabled:()=>state.open,pending:()=>state.pending,confirmDiscard:()=>state.discard});guard.register({isDirty:()=>state.dirty,saved:()=>{state.dirty=false;}});
 const callback=actualJSXCallback(text,'Dialog','onOpenChange',{createDraftExit:guard,setCreateOpen:(open:boolean)=>{state.open=open;}});
 for(const decision of ['clean','stay','pending','discard','later-edit','clean']){callback(true);dom.document.activeElement=dom.input;state.dirty=['stay','discard','later-edit'].includes(decision);state.pending=decision==='pending';state.discard=decision==='discard';const before=focuses;callback(false);
   if(['stay','pending','later-edit'].includes(decision)){assert.equal(state.open,true);assert.equal(focuses,before);state.dirty=state.pending=false;callback(false);}
   assert.equal(state.open,false);assert.equal(dom.document.activeElement,dom.input);await Promise.resolve();let prevented=0;cleanup({preventDefault(){prevented++;}});assert.equal(prevented,1);assert.equal(focuses,before+1);assert.equal(dom.document.activeElement,target);
 }
});

test('UI-FORM-SECTIONS-010: actual create/edit/live forms link visible real regions with unique instance IDs in both locales',async()=>{
  const {StreamSlotForm}=await import('../../src/features/streams/stream-slot-form.tsx');
  const {createStreamActionController}=await import('../../src/features/streams/stream-action-controller.ts');
  let mutations=0;
  const actionController=createStreamActionController({getPermissions:()=>({kind:'ready',permissions:[]}),getState:()=>({kind:'ready',freshness:'fresh',fingerprint:'synthetic'}),mutate:async()=>{mutations++;}});
  const props={actionController,onActionResult(){},onSaved(){},canCreate:true,canUpdate:true,canAssignEncoder:true,canAssignWorker:true};
  for(const locale of ['ja','en'] as const){
    const html=renderUI(createElement('div',null,createElement(StreamSlotForm,props),...['ready','live'].map((status,index)=>createElement(StreamSlotForm,{...props,key:status,stream:{id:'synthetic-'+index,name:'Existing',status}}))),locale,'/admin/streams/');
    const navs=[...html.matchAll(/<nav\b([^>]*data-slot="section-navigation"[^>]*)>([\s\S]*?)<\/nav>/g)];
    assert.equal(navs.length,3);assert.deepEqual(navs.map(n=>[...n[2].matchAll(/aria-controls="([^"]+)"/g)].length),[6,6,1]);
    const targets=navs.flatMap(n=>[...n[2].matchAll(/aria-controls="([^"]+)"/g)].map(m=>m[1]));assert.equal(new Set(targets).size,13);
    assert.deepEqual(targets.slice(0,6),['basic','schedule','start','output','visual','encoder'].map(key=>'create-stream-'+key));
    assert.equal((html.match(/id="create-stream"/g)||[]).length,1);
    for(const nav of navs){assert.ok(nav[1].includes('aria-label="'+(locale==='ja'?'配信枠のセクション':'Stream slot sections')+'"'));assert.doesNotMatch(nav[1],/hidden|sr-only/);assert.equal((nav[2].match(/type="button"/g)||[]).length,(nav[2].match(/aria-controls=/g)||[]).length);}
    for(const id of targets){
      const sections=[...html.matchAll(/<section\b([^>]*)>([\s\S]*?)<\/section>/g)].filter(m=>m[1].includes('id="'+id+'"'));
      assert.equal(sections.length,1);assert.match(sections[0][1],/tabindex="-1"/);assert.match(sections[0][1],/aria-label="[^"]+"/);assert.doesNotMatch(sections[0][1],/hidden|sr-only/);
      if(!id.endsWith('-visual')){assert.match(sections[0][2],/<fieldset/);assert.match(sections[0][2],/<legend/);assert.match(sections[0][2],/<input|role="combobox"/);}else assert.match(sections[0][2],/Video Cover/);
    }
    assert.equal(mutations,0);
  }
});

test('UI-NODE-LABEL-010: all seven real registration label/control branches have localized unique visible targets',async()=>{
  const text=readFileSync(new URL('../../src/features/nodes/node-registration-view.tsx',import.meta.url),'utf8');
  const file=ts.createSourceFile('nodes.tsx',text,ts.ScriptTarget.Latest,true,ts.ScriptKind.TSX),pairs:string[]=[];
  const walk=(node:ts.Node)=>{if(ts.isJsxElement(node)&&node.openingElement.tagName.getText(file)==='div'){
    const label=node.children.find((child):child is ts.JsxElement=>ts.isJsxElement(child)&&child.openingElement.tagName.getText(file)==='label');
    const control=node.children.find(child=>ts.isJsxSelfClosingElement(child)&&['Input','Textarea'].includes(child.tagName.getText(file))||ts.isJsxElement(child)&&child.openingElement.tagName.getText(file)==='Select');
    if(label&&control)pairs.push(label.getText(file)+control.getText(file));
  }ts.forEachChild(node,walk);};walk(file);assert.equal(pairs.length,7);
  const {Textarea}=await import('../../src/components/ui/textarea.tsx');
  const selects=await import('../../src/components/ui/select.tsx');
  const {nodeTypes}=await import('../../src/features/nodes/node-registration-model.tsx');
  const {translate}=await import('../../src/lib/i18n.ts');
  assert.match(readFileSync(new URL('../../src/features/nodes/node-action-descriptors.ts',import.meta.url),'utf8'),/NODE_FOUNDATION_SOURCE_ENABLED = false as const/);
  const compiled=ts.transpileModule('function Fields(){const inputID=useId();return <>'+pairs.join('\n')+'</>};exports.Fields=Fields;',{compilerOptions:{jsx:ts.JsxEmit.ReactJSX,module:ts.ModuleKind.CommonJS,target:ts.ScriptTarget.ES2022}}).outputText;
  for(const locale of ['ja','en'] as const)for(const type of nodeTypes){
    const output:{Fields?:ComponentType}={};
    const values={Input,Textarea,...selects,nodeTypes,useId,t:(key:Parameters<typeof translate>[1])=>translate(locale,key),uiText:createUICopy(locale),nodeType:type.value,nodeID:'synthetic',name:'Synthetic',executionHostID:'synthetic-host',host:'example.test',port:'8100',description:'Synthetic description',handleTypeChange(){},setNodeID(){},setName(){},setExecutionHostID(){},setHost(){},setPort(){},setDescription(){}};
    new Function('exports','require',...Object.keys(values),compiled)(output,createRequire(import.meta.url),...Object.values(values));assert.ok(output.Fields);
    const html=renderToStaticMarkup(createElement('div',null,createElement(output.Fields),createElement(output.Fields)));
    const validate=(markup:string)=>{
      const controls=[...markup.matchAll(/<(input|textarea|button)\b([^>]*)>/g)].filter(m=>m[1]!=='button'||/role="combobox"/.test(m[2]));
      assert.equal(controls.length,14);const ids=controls.map(m=>m[2].match(/\bid="([^"]+)"/)?.[1]);assert.ok(ids.every(Boolean));assert.equal(new Set(ids).size,14);
      const labels=[...markup.matchAll(/<label\b([^>]*)>([\s\S]*?)<\/label>/g)];
      for(const id of ids){const linked=labels.filter(m=>m[1].includes('for="'+id+'"'));assert.equal(linked.length,1);assert.ok(linked[0][2].trim());assert.doesNotMatch(linked[0][1],/hidden|sr-only/);}return ids;
    };
    const ids=validate(html);assert.match(html,locale==='ja'?/>名称<\//:/>Name<\//);
    for(const bad of [html.replace('for="'+ids[0]+'"','for="wrong"'),html.replace('id="'+ids[1]+'"','id="'+ids[0]+'"'),html.replace(/(<label[^>]*>)[\s\S]*?(<\/label>)/,'$1$2'),html.replace('<label ','<label hidden ')])assert.throws(()=>validate(bad));
  }
});

function assertExplicitInputLabels(html:string,expected:number){
  const inputs=[...html.matchAll(/<input\b([^>]*)>/g)],ids=inputs.map(match=>match[1].match(/\bid="([^"]+)"/)?.[1]);
  assert.equal(inputs.length,expected);assert.ok(ids.every(Boolean));assert.equal(new Set(ids).size,expected,'input IDs are unique across mounted owners');
  const labels=[...html.matchAll(/<label\b([^>]*)>([\s\S]*?)<\/label>/g)];
  for(const id of ids){const matched=labels.filter(label=>label[1].match(/\bfor="([^"]+)"/)?.[1]===id);assert.equal(matched.length,1,'explicit label points to its one input');assert.ok(matched[0][2].replace(/<[^>]*>/g,'').trim(),'nonempty label');assert.doesNotMatch(matched[0][1],/\bhidden\b|\bsr-only\b/);}
  return ids as string[];
}
test('UI-ACCOUNT-LABEL-009: real MFA and passkey fields have localized unique explicit labels, including the gated enrollment verification field',async()=>{
  const paths=['features/account/account-mfa-panel.tsx','features/account/account-passkey-panel.tsx'];
  const nodes:string[]=[];
  for(const path of paths){
    const text=readFileSync(new URL('../../src/'+path,import.meta.url),'utf8'),file=ts.createSourceFile(path,text,ts.ScriptTarget.Latest,true,ts.ScriptKind.TSX);
    const walk=(node:ts.Node)=>{if(ts.isJsxElement(node)&&node.openingElement.tagName.getText(file)==='label')nodes.push(node.getText(file));
      if(ts.isJsxSelfClosingElement(node)&&node.tagName.getText(file)==='Input'&&node.attributes.properties.some(p=>ts.isJsxAttribute(p)&&p.name.getText(file)==='id'))nodes.push(node.getText(file));ts.forEachChild(node,walk);};walk(file);
  }
  assert.equal(nodes.length,10,'five actual label/control pairs, without changing the secret reveal owner');
  const compiled=ts.transpileModule('function Fields(){const inputID=useId(),nameID=useId();return <>'+nodes.join('\n')+'</>};exports.Fields=Fields;',{compilerOptions:{jsx:ts.JsxEmit.ReactJSX,module:ts.ModuleKind.CommonJS,target:ts.ScriptTarget.ES2022}}).outputText;
  for(const locale of ['ja','en'] as const){
    const output:{Fields?:ComponentType}={};
    const values={Input,useId,uiText:createUICopy(locale),currentCode:'',verifyCode:'',recoveryCode:'',disableCode:'',name:'',setCurrentCode(){},setVerifyCode(){},setRecoveryCode(){},setDisableCode(){},setName(){}};
    new Function('exports','require',...Object.keys(values),compiled)(output,createRequire(import.meta.url),...Object.values(values));assert.ok(output.Fields);
    const html=renderToStaticMarkup(createElement('div',null,createElement(output.Fields),createElement(output.Fields)));
    const ids=assertExplicitInputLabels(html,10);assert.match(html,locale==='ja'?/2\. アプリに表示された6桁コードで有効化/:/six-digit code to enable MFA/);
    for(const bad of [html.replace('for="'+ids[0]+'"','for="wrong-target"'),html.replace('id="'+ids[1]+'"','id="'+ids[0]+'"'),html.replace(/(<label\b[^>]*>)[\s\S]*?(<\/label>)/,'$1$2'),html.replace('<label ','<label hidden ')])assert.throws(()=>assertExplicitInputLabels(bad,10));
  }
  const {MFAPanel}=await import('../../src/features/account/account-mfa-panel.tsx'),{PasskeyPanel}=await import('../../src/features/account/account-passkey-panel.tsx');
  const {createAccountActionController}=await import('../../src/features/account/account-action-policy.ts');
  const authority={session:'authenticated',freshness:'fresh',revision:'ui-synthetic'} as const;
  const shared={username:'ui-user',loading:false,setNotice(){},refresh(){},authority,actionController:createAccountActionController({readAuthority:()=>authority}),refreshAuthority:async()=>authority,accountResourceID:'ui-user'};
  for(const locale of ['ja','en'] as const){const html=renderUI(createElement('div',null,createElement(MFAPanel,{...shared,status:{enabled:true,available:true,required:false,pending_enrollment:false},sessionAvailable:true}),createElement(PasskeyPanel,{...shared,passkeys:[]})),locale,'/admin/account/');assertExplicitInputLabels(html,4);}
});

test('UI-FOCUS-CSS-009: actual generated focus outlines outrank layered outline-none in normal and forced colors',async t=>{
  const from=fileURLToPath(new URL('../../src/app/globals.css',import.meta.url));
  const generated=await postcss([tailwind({base:fileURLToPath(new URL('../../',import.meta.url)),optimize:false})]).process(readFileSync(from,'utf8'),{from});
  const root=generated.root,shared:postcss.Rule[]=[];let layeredNone=0;
  root.walkRules(rule=>{
    let layered=false;for(let p:postcss.Container|postcss.Document|undefined=rule.parent;p;p=p.parent)if(p.type==='atrule'&&(p as postcss.AtRule).name==='layer')layered=true;
    if(rule.selector.startsWith(':where(button,')&&rule.selector.endsWith(':focus-visible')){assert.equal(layered,false);shared.push(rule);}
    rule.walkDecls('outline-style',decl=>{if(decl.value==='none'&&layered)layeredNone++;});
  });
  assert.equal(shared.length,2);assert.ok(layeredNone>0,'the generated conflicting utility must actually exist');
  const outline=(rule:postcss.Rule)=>rule.nodes.filter((n):n is postcss.Declaration=>n.type==='decl').find(n=>n.prop==='outline')?.value;
  const normal=shared.find(rule=>rule.parent?.type==='root'),forced=shared.find(rule=>rule.parent?.type==='atrule'&&(rule.parent as postcss.AtRule).params==='(forced-colors: active)');
  assert.ok(normal);assert.ok(forced);assert.equal(outline(normal),'2px solid var(--ring)');assert.equal(outline(forced),'2px solid Highlight');
  for(const rule of shared)assert.ok(rule.nodes.some(n=>n.type==='decl'&&n.prop==='outline-offset'&&n.value==='2px'));
  t.diagnostic(JSON.stringify({generatedCSSSHA256:createHash('sha256').update(generated.css).digest('hex'),bytes:Buffer.byteLength(generated.css),unlayeredOutlines:shared.length,layeredNone,proof:'generated cascade only; actual browser computed focus remains unproven'}));
});

test("UI-FORM-001: actual field keeps its control id and links description, validation and disabled reason", () => {
  const fieldProps = {
    label: "Name", description: "Display name", error: "Required", disabledReason: "Saving",
    children: createElement((props: {id: string; disabled: boolean; "aria-describedby": string}) => createElement("input", props), { id: "name", disabled: true, "aria-describedby": "existing" }),
  };
  const html = renderToStaticMarkup(createElement(Field, fieldProps));
  assert.match(html, /for="name"/);
  assert.match(html, /aria-describedby="existing name-description name-error name-disabled"/);
  assert.match(html, /aria-invalid="true"/);
  for (const id of ["name-description", "name-error", "name-disabled"]) assert.match(html, new RegExp(`id="${id}"`));
  assert.equal((html.match(/<input\b/g) || []).length, 1);
});

test("UI-DETAIL-001: section navigation does not overwrite an existing create/detail URL fragment", () => {
  const sectionProps = { id: "overview", title: "Overview", children: createElement("button", null, "Action") };
  const html = renderToStaticMarkup(createElement("div", null,
    createElement(SectionNavigation, { label: "Sections", items: [{ id: "overview", label: "Overview" }] }),
    createElement(DetailSection, sectionProps),
  ));
  assert.doesNotMatch(html, /href=/);
  assert.match(html, /aria-label="Sections"/);
  assert.match(html, /<h2 id=/);
  assert.equal((html.match(/>Action<\/button>/g) || []).length, 1);
});
