import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { stripTypeScriptTypes } from "node:module";
import test from "node:test";
import { runInNewContext } from "node:vm";
import { driveFormContent } from "./form-content-driver.mts";
import { conditions } from "./matrix.mts";
import { longIdentifier, longText } from "./fixture-inputs.mts";

class InputBoundary {
  value = ""; type = "text"; name = "stream-name"; id = "stream-name"; autocomplete = "off";
  disabled = false; hidden = false; inert = false; visibility = "visible"; display = "block"; opacity = "1";
  attributes = new Map<string,string>();
  label: string;
  constructor(label = "Stream name") { this.label = label; }
  get labels() { return [{ textContent: this.label }]; }
  getClientRects() { return this.hidden ? [] : [{ width: 120, height: 40 }]; }
  closest(selector: string) { return (selector.includes("inert") && this.inert) || (selector.includes("hidden") && this.hidden) ? this : null; }
  getAttribute(name: string) { return name === "autocomplete" ? this.autocomplete : this.attributes.get(name) ?? null; }
  hasAttribute(name: string) { return this.attributes.has(name); }
  setAttribute(name: string,value: string) { this.attributes.set(name,value); }
  removeAttribute(name: string) { this.attributes.delete(name); }
}
function boundary(inputs: InputBoundary[], failFirstFill = false) {
  const expressions: string[] = [], fills: string[] = [];
  const select = (selector: string) => selector === "[role=dialog] input" ? inputs : inputs.filter(input => input.hasAttribute("data-ui-content-field"));
  const context = { document: { querySelectorAll: select, querySelector: (selector: string) => select(selector)[0] || null }, getComputedStyle: (input: InputBoundary) => input };
  const browser = {
    async evaluate(expression: string) { expressions.push(expression); return runInNewContext(expression,context); },
    async fillSelector(selector: string,value: string) { const selected=select(selector);assert.equal(selected.length,1);selected[0].value=value;fills.push(value);if(failFirstFill&&fills.length===1)throw Error("partial fill failed"); },
    async waitFor(expression: string,predicate: (value: unknown)=>boolean) { assert.equal(predicate(await this.evaluate(expression)),true); },
  };
  return {browser,expressions,fills};
}
function condition(exercise: string,locale="en") { const value=conditions.find(row=>row.family==="stream-create-edit"&&row.exercise===exercise&&row.locale===locale);assert.ok(value);return value; }

test("UI-FORM-CONTENT-001: actual emitted expressions fill and restore both locales and exercises",async()=>{
  for(const locale of ["ja","en"])for(const required of [false,true])for(const exercise of ["long-id","long-text"]){
    const input=new InputBoundary((locale==="ja"?"配信枠名":"Stream name")+(required?" *":""));const b=boundary([input]);
    const restore=await Reflect.apply(driveFormContent,undefined,[b.browser,condition(exercise,locale)]);
    assert.equal(input.value,exercise==="long-id"?longIdentifier:longText);assert.ok(input.hasAttribute("data-ui-content-field"));
    await restore();assert.equal(input.value,"");assert.equal(input.hasAttribute("data-ui-content-field"),false);assert.equal(b.fills.length,2);
  }
});
test("UI-FORM-CONTENT-002: actual selector rejects hidden disabled duplicate credential and real input",async()=>{
  const cases: Array<(input:InputBoundary)=>void>=[input=>{input.hidden=true;},input=>{input.disabled=true;},input=>{input.visibility="hidden";},input=>{input.inert=true;},input=>{input.type="password";},input=>{input.name="token";},input=>{input.id="secret";},input=>{input.value="existing user input";}];
  for(const change of cases){const input=new InputBoundary();change(input);const original=input.value,b=boundary([input]);await assert.rejects(Reflect.apply(driveFormContent,undefined,[b.browser,condition("long-text")]),/unique|synthetic|secret/);assert.equal(b.fills.length,0);assert.equal(input.value,original);assert.equal(input.hasAttribute("data-ui-content-field"),false);}
  const a=new InputBoundary(),b=new InputBoundary();await assert.rejects(Reflect.apply(driveFormContent,undefined,[boundary([a,b]).browser,condition("long-id")]),/unique/);assert.equal(a.attributes.size+b.attributes.size,0);
});
test("UI-FORM-CONTENT-003: partial fill failure restores input and marker before propagating failure",async()=>{
  const input=new InputBoundary(),b=boundary([input],true);await assert.rejects(Reflect.apply(driveFormContent,undefined,[b.browser,condition("long-id")]),/partial fill failed/);assert.equal(input.value,"");assert.equal(input.hasAttribute("data-ui-content-field"),false);
});
test("UI-FORM-CONTENT-004: removing the real raw-template boundary reproduces the 003 syntax failure",async()=>{
  const source=readFileSync(new URL("./form-content-driver.mts",import.meta.url),"utf8");
  const mutant=stripTypeScriptTypes(source).replace(/^import .*;\r?\n/gm,"").replace("export async function","async function").replace("String.raw`","`");
  const drive=new Function("assert","longIdentifier","longText",mutant+";return driveFormContent;")(assert,longIdentifier,longText);
  for(const exercise of ["long-id","long-text"]){const input=new InputBoundary(),b=boundary([input]);await assert.rejects(Reflect.apply(drive,undefined,[b.browser,condition(exercise)]),/Invalid regular expression|Nothing to repeat/);assert.equal(b.fills.length,0);assert.equal(input.attributes.size,0);}
});
