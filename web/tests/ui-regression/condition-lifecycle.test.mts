import assert from "node:assert/strict";
import test from "node:test";
import { existsSync } from "node:fs";
import { createHarnessFixture } from "../helpers/browser-cdp-socket-fixture.mts";
import { createConditionRunner, ConditionFailure } from "./condition-lifecycle.mts";
import { createUIFixture } from "./route-fixture.mts";
import { conditions, inventory } from "./matrix.mts";
import { navigateDocument } from "./navigation.mts";

const baseURL="http://ui.test",surface=inventory.surfaces[0];
const condition={...conditions[0],state:"initial-loading"};
function writer() {
  const files=new Map<string,unknown>();
  return {files,write:(name:string,value:unknown)=>{assert.equal(files.has(name),false,"artifact overwrite");files.set(name,value);}};
}
const paused=(id:string,path:string)=>({requestId:id,resourceType:"XHR",frameId:"main",request:{method:"GET",url:baseURL+path}});

test("UI-CONDITION-001: real wrapper configures once before product GET, releases held responses, closes and isolates the next condition",async()=>{
  const records=writer(),owned:ReturnType<typeof createHarnessFixture>[]=[];
  const run=createConditionRunner(baseURL,records.write,async()=>{const fixture=createHarnessFixture();owned.push(fixture);return fixture.harness;});
  for(const current of [condition,{...condition,id:conditions[1].id}]) {
    await run(current,surface,async(browser,fixture)=>{
      const owner=owned.at(-1)!;
      assert.equal(owner.socket.commandsFor("Page.navigate").length,0);
      const scripts=owner.socket.commandsFor("Page.addScriptToEvaluateOnNewDocument");
      assert.equal(scripts.length,1);assert.match(String(scripts[0].params.source),/UIClock/);assert.match(String(scripts[0].params.source),/autostream.controlPanel.locale/);
      assert.equal(owner.socket.commandsFor("Emulation.setDeviceMetricsOverride").length,1);
      assert.equal(fixture.primary,surface.primary);assert.equal(browser.requests.size,0);
      await navigateDocument(browser,baseURL+current.route);
      owner.socket.emitEvent("Fetch.requestPaused",paused("held",surface.primary));
      await browser.waitForRequestCount(surface.primary,1);
      assert.equal(browser.responses.size,0);
      // Same-condition history/recovery uses this exact object and socket.
      await browser.pressTab("forward");await browser.pressTab("backward");
      assert.equal(owner.socket.commandsFor("Input.dispatchKeyEvent").length,4);
    });
    const owner=owned.at(-1)!;
    assert.equal(owner.harness.responses.get(surface.primary),1);
    assert.equal(owner.socket.commandsFor("Browser.close").length,1);
    assert.equal(existsSync(owner.profile),false);
    assert.deepEqual(owner.socket.commandsFor("Page.navigate").map(c=>c.params.url),[baseURL+current.route]);
    owner.socket.emitEvent("Fetch.requestPaused",paused("late-notification","/late-old-condition"));
    await new Promise<void>(resolve=>setImmediate(resolve));
  }
  assert.equal(owned.length,2);assert.notEqual(owned[0].harness,owned[1].harness);
  assert.equal(owned[1].harness.requests.has("/late-old-condition"),true,"late notification remains attached only to its own already closed socket");
  assert.equal(records.files.size,4);
});

test("UI-CONDITION-002: first fatal is preserved, no retry starts, and owned cleanup still runs",async()=>{
  const owner=createHarnessFixture(),records=writer();let launches=0;
  const run=createConditionRunner(baseURL,records.write,async()=>{launches++;return owner.harness;});
  await assert.rejects(run(condition,surface,async browser=>{owner.socket.close();browser.assertNoFatalError();}),error=>{
    assert.ok(error instanceof ConditionFailure);assert.match(String(error.cause),/Browser CDP connection closed/);assert.equal(error.stage,"exercise");return true;
  });
  assert.equal(launches,1);assert.equal(existsSync(owner.profile),false);
  await assert.rejects(run(condition,surface,async()=>{}),/stopped|duplicate/);assert.equal(launches,1);
});
for(const fault of ["release","close"] as const)test("UI-CONDITION-003-"+fault+": cleanup failure preserves the original exercise cause and stops the next independent condition",async()=>{
  const owner=createHarnessFixture(),records=writer(),cause=new Error("original exercise failure"),cleanup=new Error("owned "+fault+" failure");
  if(fault==="close"){const close=owner.harness.close.bind(owner.harness);owner.harness.close=async()=>{await close();throw cleanup;};}
  const run=createConditionRunner(baseURL,records.write,async()=>owner.harness,url=>{
    const fixture=createUIFixture(url);if(fault==="release"){const release=fixture.release;fixture.release=()=>{release();throw cleanup;};}return fixture;
  });
  await assert.rejects(run(condition,surface,async()=>{throw cause;}),error=>{assert.ok(error instanceof ConditionFailure);assert.equal(error.cause,cause);assert.ok(error.cleanup.includes(cleanup));assert.equal(error.stop,true);return true;});
  assert.equal(existsSync(owner.profile),false);
  await assert.rejects(run({...condition,id:conditions[1].id},surface,async()=>{}),/stopped/);
});
test("UI-CONDITION-004: duplicate IDs and reused browser/fixture objects never silently re-execute",async()=>{
  const records=writer(),owner=createHarnessFixture();let executed=0,launches=0;
  const run=createConditionRunner(baseURL,records.write,async()=>{launches++;return owner.harness;});
  await run(condition,surface,async()=>{executed++;});
  await assert.rejects(run(condition,surface,async()=>{executed++;}),/duplicate/);
  assert.equal(launches,1);
  await assert.rejects(run({...condition,id:conditions[1].id},surface,async()=>{executed++;}),error=>error instanceof ConditionFailure&&/old browser/.test(String(error.cause)));
  assert.equal(executed,1);
  const fixture=createUIFixture(baseURL),next=createConditionRunner(baseURL,writer().write,async()=>createHarnessFixture().harness,()=>fixture);
  await next(condition,surface,async()=>{});
  await assert.rejects(next({...condition,id:conditions[1].id},surface,async()=>{executed++;}),error=>error instanceof ConditionFailure&&/old fixture/.test(String(error.cause)));
  assert.equal(executed,1);
});
test("UI-CONDITION-005: request before setup and unconfigured fixture are rejected at the real boundary",async()=>{
  const owner=createHarnessFixture(),records=writer();owner.harness.requests.set("/streams",1);
  const run=createConditionRunner(baseURL,records.write,async()=>owner.harness);
  await assert.rejects(run(condition,surface,async()=>assert.fail("must not exercise")),error=>error instanceof ConditionFailure&&/before fixture setup/.test(String(error.cause)));
  assert.equal(owner.socket.commandsFor("Page.navigate").length,0);
  const fixture=createUIFixture(baseURL);
  assert.throws(()=>fixture.resolver({method:"GET",url:baseURL+"/streams"}),/before its first request/);
});
test("UI-CONDITION-006: artifact collision stops with original bytes intact and no rerun",async()=>{
  const records=writer(),owner=createHarnessFixture();records.files.set(condition.id+".browser-version.json","original");
  const run=createConditionRunner(baseURL,records.write,async()=>owner.harness);
  await assert.rejects(run(condition,surface,async()=>assert.fail("must not exercise")),error=>error instanceof ConditionFailure&&/artifact overwrite/.test(String(error.cause)));
  assert.equal(records.files.get(condition.id+".browser-version.json"),"original");assert.equal(existsSync(owner.profile),false);
  await assert.rejects(run({...condition,id:conditions[1].id},surface,async()=>{}),/stopped/);
});
