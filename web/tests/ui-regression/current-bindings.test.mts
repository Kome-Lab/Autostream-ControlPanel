import "./component-loader.mts";
import assert from "node:assert/strict";
import {readFileSync} from "node:fs";
import {createElement} from "react";
import test from "node:test";
import {assertCopyBinding,copyBindings} from "./current-binding.mts";
import {renderedSource} from "./source-render.mts";
import {renderUI} from "./render-ui.mts";
const bindings=JSON.parse(readFileSync(new URL("../fixtures/ui-regression/current-copy-bindings.json",import.meta.url),"utf8")) as {files:{path:string;binding:ReturnType<typeof copyBindings>}[]};
test("UI-CURRENT-BINDING-001: actual current imports, hooks, helpers and displayed keys remain connected",()=>{
 assert.ok(bindings.files.length>70);
 for(const row of bindings.files)assertCopyBinding(readFileSync(new URL("../../../"+row.path,import.meta.url),"utf8"),row.binding);
});
test("UI-CURRENT-BINDING-002: missing import, disconnected owner and missing/duplicate key reach distinct current failures",()=>{
 const row=bindings.files.find(row=>row.path.endsWith("metrics-view.tsx"))!;
 const source=readFileSync(new URL("../../../"+row.path,import.meta.url),"utf8");
 assertCopyBinding(source,row.binding);
 assert.throws(()=>assertCopyBinding(source.replace('/i18n/ui-v2/use-ui-copy','/wrong-owner'),row.binding),/import disconnected/);
 assert.throws(()=>assertCopyBinding(source.replace('uiText = useUICopy()','uiText = disconnected()'),row.binding),/owner disconnected/);
 const first=source.match(/uiText\("([^"\\]+)"\)/)!;assert.ok(first);
 assert.throws(()=>assertCopyBinding(source.replace(first[0],'"removed"'),row.binding),/key missing or duplicated/);
 assert.throws(()=>assertCopyBinding(source+'\n'+first[0]+';',row.binding),/key missing or duplicated/);
});
test("UI-CURRENT-COPY-NEGATIVE: actual Metrics body with a disconnected copy owner is rejected after successful render",async()=>{
 const url=new URL("../../src/features/metrics/metrics-view.tsx",import.meta.url);const source=readFileSync(url,"utf8");
 const current=await import(url.href);const html=renderUI(createElement(current.MetricsView),"en");
 function english(body:string) {assert.ok(body.includes("CPU utilization"),"actual Metrics CPU label must use English");assert.ok(!body.includes(">CPU使用率<"),"untranslated actual Metrics CPU label");}
 english(html);
 const mutant=await renderedSource(source.replace('uiText = useUICopy()','uiText = (key: string) => key'),url);
 const changed=renderUI(createElement(mutant.MetricsView),"en");assert.ok(changed.length>300);
 assert.throws(()=>english(changed),/actual Metrics CPU label/);
});
