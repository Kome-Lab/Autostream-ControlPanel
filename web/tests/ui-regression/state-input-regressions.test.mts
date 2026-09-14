import "./component-loader.mts";
import assert from "node:assert/strict";
import test from "node:test";
import { createElement } from "react";
import { createUIFixture } from "./route-fixture.mts";
import { conditions,inventory,type Condition } from "./matrix.mts";
import { statePaths,assertState,deniedPrimaryRequests,type RequestEvidence } from "./state-drivers.mts";
import { contentInput,canonicalFixturePath,longIdentifier,longText,assertContentReached } from "./fixture-inputs.mts";
import { observationExpression,type UIObservation } from "./observation.mts";
import { observerDOM } from "./observer-dom.mts";
import { renderUI } from "./render-ui.mts";
const {ResourceTable}=await import("../../src/features/resources/resource-table.tsx");
const {resourcePages}=await import("../../src/features/resources/resource-config.ts");
const {enrichResourceRow,visibleColumns}=await import("../../src/features/resources/resource-presentation.tsx");
const base={...conditions[0],locale:"en"};
function fixtureFor(family:string,state:string,exercise?:string) {
 const surface=inventory.surfaces.find(s=>s.id===family)!;assert.ok(surface);
 const condition={...base,family,state,exercise} as Condition;
 const fixture=createUIFixture("http://ui.test");fixture.reset(condition,surface.primary);
 const evidence:RequestEvidence={requests:new Map(),responses:new Map(),responseStatuses:new Map()};
 function get(path:string) {const response=fixture.resolver({method:"GET",url:"http://ui.test"+path})!;assert.ok(response);
  evidence.requests.set(path,(evidence.requests.get(path)||0)+1);
  if(!response.waitUntil) {evidence.responses.set(path,(evidence.responses.get(path)||0)+1);evidence.responseStatuses.set(path,[...(evidence.responseStatuses.get(path)||[]),response.status??200]);}
  return response;
 }
 return {surface,condition,fixture,evidence,get};
}
test("UI-STATE-001: actual resolver keeps partial successes and rejects all-failed substitution",()=>{
 const current=fixtureFor("monitoring","partial");
 for(const path of [...statePaths(current.condition,current.surface.primary).success,...statePaths(current.condition,current.surface.primary).failure])current.get(path);
 const dom=observerDOM();dom.main.ownText="Worker One. Some sections are unavailable.";
 const observed=dom.run<UIObservation>(observationExpression);
 assertState(current.condition,observed,current.evidence,current.surface.primary);
 current.evidence.responseStatuses.set("/service-health",[503]);
 assert.throws(()=>assertState(current.condition,observed,current.evidence,current.surface.primary),/successful section/);
 current.evidence.responseStatuses.set("/service-health",[200]);dom.main.ownText="Some sections are unavailable.";
 assert.throws(()=>assertState(current.condition,dom.run(observationExpression),current.evidence,current.surface.primary),/remain visible/);
});
test("UI-STATE-002: stale and refresh come from an initial 200 followed by a real changed resolver phase",()=>{
 for(const state of ["stale","background-refresh"]) {
  const current=fixtureFor("streams-list",state);const first=current.get("/streams");assert.equal(first.status??200,200);
  current.fixture.refresh();const second=current.get("/streams");assert.equal(second.status??200,state==="stale"?503:200);
  const dom=observerDOM();dom.main.ownText="B9 Browser Stream. "+(state==="stale"?"Refresh failed":"Refreshing");
  assertState(current.condition,dom.run(observationExpression),current.evidence,"/streams");
  current.evidence.responseStatuses.set("/streams",[503]);current.evidence.responses.set("/streams",2);
  assert.throws(()=>assertState(current.condition,dom.run(observationExpression),current.evidence,"/streams"));current.fixture.release();
 }
});
test("UI-STATE-003: every applicable empty driver preserves the collection shape and unknown affects consumed fields",()=>{
 for(const surface of inventory.surfaces.filter(s=>s.states.empty.applicable)) {
  const current=fixtureFor(surface.id,"empty");assert.deepEqual(current.get(surface.primary).body,[],surface.id);
 }
 for(const family of ["streams-list","workers","metrics","audit-logs","system-updates"]) {
  const current=fixtureFor(family,"unknown");const body=current.get(current.surface.primary).body;
  assert.match(JSON.stringify(body),/future_state|future_result/);assert.notEqual(body,undefined);
 }
});
test("UI-STATE-004: denied fixture role and per-query request contract remain distinct",()=>{
 for(const surface of inventory.surfaces.filter(s=>s.states["permission-denied"].applicable)) {
  const current=fixtureFor(surface.id,"permission-denied");const me=current.get("/auth/me").body as {user:{roles:string[]};permissions:string[]};
  assert.deepEqual(me.user.roles,["viewer"]);assert.deepEqual(me.permissions,[]);
  const count=deniedPrimaryRequests[surface.id]||0;for(let i=0;i<count;i++)current.get(surface.primary);
  const dom=observerDOM();dom.main.ownText="Permission denied";
  assertState(current.condition,dom.run(observationExpression),current.evidence,surface.primary);
  current.evidence.requests.set(surface.primary,count===0?1:0);
  assert.throws(()=>assertState(current.condition,dom.run(observationExpression),current.evidence,surface.primary),/query contract/);
 }
});
test("UI-CONTENT-001: real fixture values reach actual resource cells; metric code and linked IDs stay coherent",()=>{
 const metric={name:"node.cpu.used_percent",service_id:"worker-one",value:24};
 const changed=contentInput(metric,"long-id","/observability/metrics") as typeof metric;
 assert.equal(changed.name,metric.name);assert.match(changed.service_id,/UI-LONG-ID/);
 assert.equal(canonicalFixturePath('/nodes/'+changed.service_id),'/nodes/worker-one');
 for(const family of ["diagnostics","remediation","notifications"] as const)for(const exercise of ["long-id","long-text"]) {
  const current=fixtureFor(family,"ready",exercise);const resource=resourcePages[family].resources[0];
  const rows=(current.get(resource.path).body as Record<string,unknown>[]).map(row=>enrichResourceRow(resource,row));
  const html=renderUI(createElement(ResourceTable,{resource,rows,columns:visibleColumns(rows,resource),canEdit:false,canDelete:false,canTest:false,currentUser:undefined}),"en");
  assertContentReached(current.condition,html,[]);
  assert.throws(()=>assertContentReached(current.condition,"unmodified fixture",[]),/did not reach/);
  assert.ok(html.includes(exercise==="long-id"?longIdentifier:longText));
 }
});

test("UI-STATE-VIEW-001: actual Metrics retains old rows with failure/partial state and future metric status reaches unknown",async()=>{
  const {MetricsView}=await import("../../src/features/metrics/metrics-view.tsx");
  const {APIError}=await import("../../src/lib/api/client.ts");
  for(const state of ["stale","partial","unknown"]) {
    const current=fixtureFor("metrics",state);
    current.get(current.surface.primary);
    if(state==="stale") {current.fixture.refresh();current.get(current.surface.primary);}
    if(state==="partial")current.get("/service-health");
    const html=renderUI(createElement(MetricsView),"en","/admin/metrics/",client=>{
      const key=state==="partial"?["service-health"]:["observability","metrics",10800];
      const query=client.getQueryCache().find({queryKey:key,exact:true});assert.ok(query);
      if(state==="unknown")query.setData(current.get(current.surface.primary).body);
      else query.setState({status:"error",error:new APIError("UNSAFE_PROVIDER_DETAIL",503,"temporarily_unavailable"),fetchStatus:"idle",...(state==="partial"?{data:undefined}:{})});
    });
    const dom=observerDOM();dom.main.ownText=html.replace(/<[^>]*>/g," ");
    assertState(current.condition,dom.run(observationExpression),current.evidence,current.surface.primary);
    assert.doesNotMatch(html,/UNSAFE_PROVIDER_DETAIL/);
  }
});
test("UI-STATE-VIEW-002: actual security partial shows role error while keeping settings and safe labels",async()=>{
  const {ResourcePage}=await import("../../src/features/resources/resource-page.tsx");
  const {APIError}=await import("../../src/lib/api/client.ts");
  const html=renderUI(createElement(ResourcePage,{pageId:"security"}),"en","/admin/security/",client=>{
    const query=client.getQueryCache().find({queryKey:["resource","/roles"],exact:true});assert.ok(query);
    query.setState({data:undefined,status:"error",error:new APIError("UNSAFE_PROVIDER_DETAIL",503,"temporarily_unavailable"),fetchStatus:"idle"});
  });
  assert.match(html,/role="alert"/);assert.match(html,/The role list could not be loaded/);assert.match(html,/value="12"/);assert.match(html,/Minimum password length/);assert.doesNotMatch(html,/UNSAFE_PROVIDER_DETAIL/);
});
