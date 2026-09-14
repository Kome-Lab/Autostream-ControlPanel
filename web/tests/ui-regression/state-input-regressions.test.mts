import "./component-loader.mts";
import assert from "node:assert/strict";
import test from "node:test";
import { createElement } from "react";
import { createUIFixture } from "./route-fixture.mts";
import { conditions,inventory,type Condition } from "./matrix.mts";
import { statePaths,assertState,deniedPrimaryRequests,loadingPaths,hasFailureCopy,failureTextExpression,type RequestEvidence } from "./state-drivers.mts";
import { contentInput,canonicalFixturePath,longIdentifier,longText,assertContentReached } from "./fixture-inputs.mts";
import { observationExpression,assertObservation,type UIObservation } from "./observation.mts";
import { observerDOM,Element } from "./observer-dom.mts";
import { renderUI } from "./render-ui.mts";
import { createHarnessFixture } from "../helpers/browser-cdp-socket-fixture.mts";
import { createConditionRunner } from "./condition-lifecycle.mts";
import { navigateDocument } from "./navigation.mts";
import { assertCurrentObservation } from "./run-browser.mts";
import { layoutExpression,type LayoutObservation } from "./layout-observation.mts";
import type { BrowserHarness } from "../helpers/browser-harness.mts";
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

test("UI-STATE-005: every initial aggregate holds all real loading owners, not a partial settled substitute", async () => {
  for (const family of ["dashboard","workers","archive","monitoring","metrics","system-updates","account"]) {
    const current=fixtureFor(family,"initial-loading"), paths=loadingPaths(current.condition,current.surface.primary);
    const held=paths.map(path=>current.get(path));assert.ok(held.every(response=>response.waitUntil));
    const dom=observerDOM();dom.main.ownText="Data has not been received.";
    assertState(current.condition,dom.run(observationExpression),current.evidence,current.surface.primary);
    current.evidence.responses.set(paths.at(-1)!,1);
    assert.throws(()=>assertState(current.condition,dom.run(observationExpression),current.evidence,current.surface.primary),/settled response/);
    current.evidence.responses.clear();current.evidence.requests.delete(paths.at(-1)!);
    assert.throws(()=>assertState(current.condition,dom.run(observationExpression),current.evidence,current.surface.primary),/real GET/);
    current.fixture.release();await Promise.all(held.map(response=>response.waitUntil));
  }
  const {MetricsView}=await import("../../src/features/metrics/metrics-view.tsx");
  const current=fixtureFor("metrics","initial-loading");
  loadingPaths(current.condition,current.surface.primary).forEach(current.get);
  const html=renderUI(createElement(MetricsView),"en","/admin/metrics/",client=>{
    for(const key of [["observability","metrics",10800],["service-health"]]){
      const query=client.getQueryCache().find({queryKey:key,exact:true});assert.ok(query);
      query.setState({data:undefined,status:"pending",fetchStatus:"fetching",error:null});
    }
  });
  const dom=observerDOM();dom.main.ownText=html.replace(/<[^>]*>/g," ");
  const status=html.match(/<div role="status" aria-label="([^"]+)"/);assert.ok(status,"actual loading status must be rendered");
  const notice=dom.main.add(new Element("DIV"));
  notice.setAttribute("role","status");notice.setAttribute("aria-label",status[1]);
  assertState(current.condition,dom.run(observationExpression),current.evidence,current.surface.primary);current.fixture.release();
});
test("UI-STATE-006: actual English failure rendering and runner use the same failure predicate",async()=>{
  const {MetricsView}=await import("../../src/features/metrics/metrics-view.tsx");
  const {APIError}=await import("../../src/lib/api/client.ts");
  const current=fixtureFor("metrics","blocking-error");current.get(current.surface.primary);
  const html=renderUI(createElement(MetricsView),"en","/admin/metrics/",client=>{
    for(const key of [["observability","metrics",10800],["service-health"]]){
      const query=client.getQueryCache().find({queryKey:key,exact:true});assert.ok(query);
      query.setState({data:undefined,status:"error",fetchStatus:"idle",error:new APIError("HIDDEN_ERROR",503,"temporarily_unavailable")});
    }
  });
  const dom=observerDOM();dom.main.ownText=html.replace(/<[^>]*>/g," ");
  assert.equal(hasFailureCopy(dom.run(failureTextExpression)),true);
  assertState(current.condition,dom.run(observationExpression),current.evidence,current.surface.primary);
  assert.equal(hasFailureCopy("Ready. No error has occurred."),false);assert.equal(hasFailureCopy(null),false);
  assert.doesNotMatch(html,/HIDDEN_ERROR/);
});
test("UI-FLOW-001: Archive share-list GET follows both real IDs and rejects wrong ID/method/origin",()=>{
  for(const exercise of [undefined,"long-id"]) {
    const current=fixtureFor("archive","ready",exercise);
    const stream=(current.get("/archive/streams").body as {id:string}[])[0];
    const artifact=(current.get("/streams/"+encodeURIComponent(stream.id)+"/artifacts").body as {id:string}[])[0];
    const path="/streams/"+encodeURIComponent(stream.id)+"/artifacts/"+encodeURIComponent(artifact.id)+"/shares";
    assert.deepEqual(current.get(path).body,[]);assert.deepEqual(current.fixture.unexpected,[]);
    for(const request of [{method:"POST",url:"http://ui.test"+path},{method:"GET",url:"http://ui.test"+path.replace("ui-artifact","unknown")},{method:"GET",url:"https://external.invalid"+path}]) {
      const response=current.fixture.resolver(request);assert.ok(response);assert.ok((response.status??200)>=400);
    }
    assert.equal(current.fixture.unexpected.length,3);current.fixture.release();
  }
});
test("UI-FLOW-002: Nodes remote conditions use the existing registered list, creation stays on its existing route",()=>{
  const nodes=conditions.filter(row=>row.family==="nodes");assert.ok(nodes.length>100);
  assert.ok(nodes.filter(row=>row.kind==="state").every(row=>row.route==="/admin/registered-nodes/"));
  assert.ok(nodes.filter(row=>row.kind!=="state").every(row=>row.route==="/admin/nodes/"));
  assert.equal(conditions.length,4144);
});
test("UI-AUTH-LAYOUT-001: real Login preserves full synthetic labels with wrap/width/height classes and existing credentials",async()=>{
  const {LoginCard}=await import("../../src/components/auth/auth-card.tsx");
  for(const locale of ["ja","en"] as const)for(const exercise of ["long-id","long-text"]) {
    const current=fixtureFor("login","ready",exercise);
    const providers=current.get("/auth/oauth/providers").body as {id:string;name:string}[];
    const html=renderUI(createElement(LoginCard),locale,"/login/",client=>client.setQueryData(["auth","oauth","providers","login"],providers));
    assert.ok(html.includes(providers[0].name),"full provider name must reach actual markup");
    const buttons=html.match(/<button\b[^>]*>[\s\S]*?<\/button>/g)||[];
    for(const button of buttons.filter(button=>button.includes(providers[0].name)||/Passkey|passkey/.test(button)||/type="submit"/.test(button))) {
      assert.match(button,/whitespace-normal/);assert.match(button,/h-auto/);assert.match(button,/min-w-0/);assert.match(button,/max-w-full/);
      assert.doesNotMatch(button,/whitespace-nowrap|overflow-hidden|truncate/);
    }
    assert.ok(buttons.some(button=>button.includes(providers[0].name)));
    assert.match(html,/type="password"/);assert.match(html,/autoComplete="current-password"/i);
    assert.match(html,/value=""/);assert.match(html,/overflow-wrap:anywhere/);
  }
});

const statusPairs = [["metrics", "initial-loading"], ["monitoring", "initial-loading"],
  ["public-archive-share", "initial-loading"], ["public-archive-share", "blocking-error"], ["public-archive-share", "permission-denied"]];
const statusConditions = conditions.filter(condition => condition.kind === "state" && statusPairs.some(([family, state]) => condition.family === family && condition.state === state));

// Transcribe actual SSR branch markup into the existing controlled DOM boundary.
// Geometry is deliberately controlled; this does not claim browser layout proof.
function statusBranchDOM(markup: string, condition: Condition) {
  const dom = observerDOM(); for (const child of dom.body.children) child.parentElement = null; dom.body.children = [];
  const root = /<main\b/.test(markup) ? dom.body : dom.body.add(new Element("MAIN"));
  const stack = [root], decode = (text: string) => text.replace(/&#x([a-f0-9]+);|&#([0-9]+);|&(amp|lt|gt|quot|apos);/gi, (_, hex, decimal, name: string) => hex ? String.fromCodePoint(parseInt(hex, 16)) : decimal ? String.fromCodePoint(Number(decimal)) : ({ amp: "&", lt: "<", gt: ">", quot: '"', apos: "'" }[name.toLowerCase()]!));
  for (const token of markup.match(/<[^>]*>|[^<]+/g) || []) {
    if (token.startsWith("</")) { if (stack.length > 1) stack.pop(); continue; }
    if (token.startsWith("<")) {
      const match = /^<([\w-]+)([\s\S]*?)\/?\s*>$/.exec(token); if (!match) continue;
      const element = stack.at(-1)!.add(new Element(match[1].toUpperCase()));
      for (const attr of match[2].matchAll(/([^\s=/>]+)(?:="([^"]*)"|'([^']*)'|=([^\s>]+))?/g)) {
        const name = attr[1].toLowerCase(), value = decode(attr[2] || attr[3] || attr[4] || ""); element.setAttribute(name, value);
        if (name === "class") element.className = value;
        if (name === "hidden") element.hidden = true;
        if (name === "type") element.type = value;
      }
      if (!/^(AREA|BASE|BR|COL|EMBED|HR|IMG|INPUT|LINK|META|PARAM|SOURCE|TRACK|WBR)$/.test(element.tagName) && !token.endsWith("/>")) stack.push(element);
    } else stack.at(-1)!.ownText += decode(token);
  }
  const main = dom.document.querySelector("main"); assert.ok(main);
  dom.html.attributes.lang = condition.locale; dom.html.className = condition.mode; dom.html.attributes["data-theme"] = condition.theme;
  dom.html.clientWidth = condition.width; dom.html.scrollWidth = condition.width; dom.context.innerWidth = condition.width;
  const url = new URL(condition.route, "http://ui.test"); Object.assign(dom.context.location, { pathname: url.pathname, search: url.search, hash: url.hash });
  dom.document.activeElement = dom.body;
  return { ...dom, main };
}

async function renderStatusBranch(condition: Condition) {
  const { MetricsView } = await import("../../src/features/metrics/metrics-view.tsx");
  const { MonitoringView } = await import("../../src/features/monitoring/monitoring-view.tsx");
  const { ArchiveSharePlayerView } = await import("../../src/features/archive/archive-share-player-view.tsx");
  const { APIError } = await import("../../src/lib/api/client.ts");
  const element = condition.family === "metrics" ? createElement(MetricsView) : condition.family === "monitoring" ? createElement(MonitoringView) : createElement(ArchiveSharePlayerView, { token: "ui-synthetic-share" });
  const keys = condition.family === "metrics" ? [["observability", "metrics", 10800], ["service-health"]] : condition.family === "monitoring" ? [["streams"], ["service-health"], ["resource", "/observability/incidents"], ["resource", "/observability/diagnostics"]] : [["archive-share", "ui-synthetic-share"]];
  const html = renderUI(element, condition.locale as "ja" | "en", condition.route, client => {
    for (const key of keys) {
      const query = client.getQueryCache().find({ queryKey: key, exact: true }); assert.ok(query, "actual query owner must exist");
      query.setState(condition.state === "initial-loading" ? { data: undefined, status: "pending", fetchStatus: "fetching", error: null } :
        { data: undefined, status: "error", fetchStatus: "idle", error: new APIError("NEVER_RENDER_RAW_STATUS_ERROR", condition.state === "permission-denied" ? 403 : 503, "temporarily_unavailable") });
    }
  });
  assert.ok(!html.includes("NEVER_RENDER_RAW_STATUS_ERROR")); return html;
}

async function withStatusEvidence(condition: Condition, check: (browser: BrowserHarness, primary: string) => Promise<void> | void) {
  const owner = createHarnessFixture(), surface = inventory.surfaces.find(row => row.id === condition.family)!;
  const writes = new Map<string, unknown>();
  const run = createConditionRunner("http://ui.test", (name, value) => { assert.equal(writes.has(name), false); writes.set(name, value); }, async () => owner.harness);
  await run(condition, surface, async browser => {
    await navigateDocument(browser, "http://ui.test" + condition.route);
    const paths = condition.state === "initial-loading" ? loadingPaths(condition, surface.primary) : [surface.primary];
    for (const [index, path] of paths.entries()) {
      owner.socket.emitEvent("Fetch.requestPaused", { requestId: "status-" + index, resourceType: "XHR", frameId: "main", request: { method: "GET", url: "http://ui.test" + path } });
      await browser.waitForRequestCount(path, 1);
      if (condition.state !== "initial-loading") await browser.waitForResponseCount(path, 1);
    }
    await check(browser, surface.primary);
  });
  assert.equal(owner.socket.commandsFor("Browser.close").length, 1);
  assert.ok(writes.has(condition.id + ".lifecycle.json"));
}

test("UI-STATUS-ONLY-001: exactly five policies and 40 existing IDs use real SSR branches, the emitted observer, actual caller and real harness GET/status counters", async t => {
  const policies = inventory.surfaces.flatMap(surface => Object.entries(surface.states).filter(([, state]) => state.observation).map(([state]) => [surface.id, state]));
  assert.deepEqual(policies.sort(), [...statusPairs].sort()); assert.equal(statusConditions.length, 40);
  for (const pair of statusPairs) assert.equal(statusConditions.filter(c => c.family === pair[0] && c.state === pair[1]).length, 8);
  for (const condition of statusConditions) await t.test(condition.id, async () => {
    const dom = statusBranchDOM(await renderStatusBranch(condition), condition);
    await withStatusEvidence(condition, (browser, primary) => {
      const observed = dom.run<UIObservation>(observationExpression), layout = dom.run<LayoutObservation>(layoutExpression);
      assert.equal(observed.controls.length, 0); assert.ok(layout.examined > 0);
      assertCurrentObservation(observed, layout, condition, browser, primary);
      assert.throws(() => assertObservation(observed, condition), /requires actual status-only evidence/);
    });
  });
});

test("UI-STATUS-ONLY-002: every authorized branch rejects missing or wrong HTTP evidence at the real observation/state caller", async () => {
  for (const [family, state] of statusPairs) {
    const condition = statusConditions.find(c => c.family === family && c.state === state && c.locale === "en")!;
    const dom = statusBranchDOM(await renderStatusBranch(condition), condition);
    await withStatusEvidence(condition, (browser, primary) => {
      const observation = dom.run<UIObservation>(observationExpression), layout = dom.run<LayoutObservation>(layoutExpression);
      for (const fault of ["missing-get", "wrong-status", "wrong-responses"] as const) {
        const evidence: RequestEvidence = { requests: new Map(browser.requests), responses: new Map(browser.responses), responseStatuses: new Map([...browser.responseStatuses].map(([path, statuses]) => [path, [...statuses]])) };
        const path = state === "initial-loading" ? loadingPaths(condition, primary).at(-1)! : primary;
        if (fault === "missing-get") evidence.requests.delete(path);
        if (fault === "wrong-status") evidence.responseStatuses.set(path, [200]);
        if (fault === "wrong-responses") evidence.responses.set(path, state === "initial-loading" ? 1 : 0);
        assert.throws(() => assertCurrentObservation(observation, layout, condition, evidence, primary), /GET|response|HTTP|403|503|equal|existing per-surface denied query contract/);
      }
      assert.throws(() => assertCurrentObservation({ ...observation, statusPage: undefined }, layout, condition, browser, primary), /missing actual/);
      assert.throws(() => assertCurrentObservation(observation, layout, { ...condition, id: "unregistered" }, browser, primary), /exact registered/);
    });
  }
});

test("UI-STATUS-ONLY-003: zero-control admission preserves visible branch, geometry, copy, secret, ID/label and overflow failures", async () => {
  const condition = statusConditions.find(c => c.family === "metrics" && c.locale === "en")!;
  const html = await renderStatusBranch(condition);
  await withStatusEvidence(condition, (browser, primary) => {
    for (const fault of ["heading", "status", "hidden-status", "wrong-role", "wrong-copy", "route", "geometry", "hidden-control", "secret", "storage", "diagnostic", "duplicate", "reference", "overflow", "language", "mode", "theme", "whole-hidden"] as const) {
      const dom = statusBranchDOM(html, condition), status = dom.main.querySelector('[role=status]')!, heading = dom.main.querySelector('h1')!;
      if (fault === "heading") heading.ownText = "Wrong family";
      if (fault === "status") { status.removeAttribute("role"); status.removeAttribute("aria-label"); }
      if (fault === "hidden-status") status.hidden = true;
      if (fault === "wrong-role") status.setAttribute("role", "alert");
      if (fault === "wrong-copy") status.setAttribute("aria-label", "Ready");
      if (fault === "route") dom.context.location.pathname = "/admin/streams/";
      if (fault === "geometry") status.rect.width = 0;
      if (fault === "hidden-control") dom.main.add(new Element("BUTTON", "Refresh")).hidden = true;
      if (fault === "secret") dom.main.add(new Element("SPAN", "B9-SYNTHETIC-RECOVERY")).hidden = true;
      if (fault === "storage") Object.assign(dom.context.localStorage, { secret: "B9-SYNTHETIC-RECOVERY" });
      if (fault === "diagnostic") dom.main.add(new Element("SPAN", "UI-HIDDEN-DIAGNOSTIC")).hidden = true;
      if (fault === "duplicate") { status.id = "same"; heading.id = "same"; }
      if (fault === "reference") status.setAttribute("aria-describedby", "missing");
      if (fault === "overflow") dom.html.scrollWidth++;
      if (fault === "language") dom.html.attributes.lang = "wrong";
      if (fault === "mode") dom.html.className = "dark";
      if (fault === "theme") dom.html.attributes["data-theme"] = "wrong";
      if (fault === "whole-hidden") dom.main.style.display = "none";
      assert.throws(() => assertCurrentObservation(dom.run(observationExpression), dom.run(layoutExpression), condition, browser, primary), fault);
    }
    for (const state of ["ready", "unknown", "partial", "stale"]) {
      const other = conditions.find(c => c.family === "metrics" && c.state === state && c.locale === "en")!;
      const dom = statusBranchDOM(html, other);
      assert.throws(() => assertCurrentObservation(dom.run(observationExpression), dom.run(layoutExpression), other, browser, primary), /outside the five/);
    }
    const dom = statusBranchDOM(html, condition), button = dom.main.add(new Element("BUTTON"));
    assert.throws(() => assertCurrentObservation(dom.run(observationExpression), dom.run(layoutExpression), condition, browser, primary), /unnamed/);
    button.ownText = "Visible action"; button.rect.left = 2000; button.rect.right = 2140;
    assert.throws(() => assertCurrentObservation(dom.run(observationExpression), dom.run(layoutExpression), condition, browser, primary), /unreachable/);
  });
});
