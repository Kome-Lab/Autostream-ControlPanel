import "./component-loader.mts";
import assert from "node:assert/strict";
import ts from "typescript";
import { actualJSXCallback } from "./source-callback.mts";
import { readFileSync } from "node:fs";
import test from "node:test";
import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { actualCallback, actualFunction } from "./source-callback.mts";
const { I18nProvider } = await import("../../src/components/admin/i18n-provider.tsx");
const { StreamDetailOperations, readinessResult } = await import("../../src/features/streams/stream-detail-operations.tsx");
const { createStreamActionController } = await import("../../src/features/streams/stream-action-controller.ts");
const stream = { id: "stream-a", name: "日本語の利用者名", status: "ready", updated_at: "2026-09-01T00:00:00Z" };

test("UI-STREAM-IDENTITY-001: actual primary cell and dialog close callbacks retain the exact original trigger", () => {
  const owner = readFileSync(new URL("../../src/features/streams/streams-view.tsx", import.meta.url), "utf8");
  const source = readFileSync(new URL("../../src/features/streams/stream-table-cells.tsx", import.meta.url), "utf8");
  const parsed = ts.createSourceFile("cells.tsx", source, ts.ScriptTarget.Latest, true, ts.ScriptKind.TSX);
  const primary = parsed.statements.find(node => ts.isFunctionDeclaration(node) && node.name?.text === "StreamNameCell"); assert.ok(primary);
  const selected: unknown[] = [], detailTrigger = { current: null as { focus(): void } | null };
  let focused = 0; const original = { focus() { focused++; } };
  const onDetails = actualCallback(owner, "onDetails", { detailTrigger, setSelectedStream: (value: unknown) => selected.push(value) });
  const click = actualJSXCallback(primary.getText(parsed), "button", "onClick", { onDetails, row: { original: stream } });
  click({ currentTarget: original }); assert.equal(selected[0], stream); assert.equal(detailTrigger.current, original);
  const returnFocus = actualJSXCallback(owner, "StreamDetailsDialog", "returnFocus", { detailTrigger });
  const dialog = readFileSync(new URL("../../src/features/streams/stream-details-dialog.tsx", import.meta.url), "utf8");
  const close = actualJSXCallback(dialog, "DialogContent", "onCloseAutoFocus", { returnFocus });
  let prevented = 0; close({ preventDefault() { prevented++; } }); assert.equal(focused, 1); assert.equal(prevented, 1);
  const fresh = { ...stream, name: "Current input" }; onDetails(fresh, original); assert.equal(selected.at(-1), fresh); assert.equal(detailTrigger.current, original);
  assert.match(owner, /getRowId=\{streamRowID\}/); assert.match(owner, /<StreamTableContext.Provider value=\{tablePresentation\}/);
});

test("UI-STREAM-DETAIL-001: actual operation surface consumes the same controller for readiness/start/stop in ja/en", () => {
  for (const locale of ["ja", "en"]) {
    const evaluated: string[] = [];
    const base = createStreamActionController({ getPermissions: () => ({ kind: "ready", permissions: ["streams.start", "streams.stop"] }),
      getState: () => ({ kind: "ready", freshness: "fresh", fingerprint: "current" }), mutate: async () => { throw new Error("render must never mutate"); } });
    const controller = { ...base, evaluate(intent: Parameters<typeof base.evaluate>[0]) { evaluated.push(intent.id); return base.evaluate(intent); } };
    Object.defineProperty(globalThis, "window", { configurable: true, value: { localStorage: { getItem: () => locale } } });
    try {
      const html = renderToStaticMarkup(createElement(I18nProvider, null, createElement(StreamDetailOperations, { stream, controller, onResult() {}, notice: null })));
      assert.ok(evaluated.includes("STR-04") && evaluated.includes("STR-05") && evaluated.includes("STR-08"));
      assert.match(html, locale === "ja" ? /開始準備を再確認/ : /Check Readiness/);
      assert.match(html, /data-readiness="unknown"/);
      assert.match(html, locale === "ja" ? /配信を開始/ : /Start stream/);
    } finally { Reflect.deleteProperty(globalThis, "window"); }
  }
});
test("UI-STREAM-DETAIL-002: response projection never echoes issue messages and rejects inconsistent or wrong-target readiness", () => {
  const value = { stream_id: stream.id, ready: false, missing_service_types: ["worker"], issues: [{ message: "HOSTILE_SECRET" }] };
  assert.deepEqual(readinessResult(value, stream), { ready: false, missing: 1, issues: 1, version: stream.updated_at });
  assert.equal(readinessResult({ ...value, ready: true }, stream).ready, null);
  assert.equal(readinessResult({ ...value, stream_id: "other" }, stream).ready, null);
  assert.doesNotMatch(JSON.stringify(readinessResult(value, stream)), /HOSTILE_SECRET/);
});
test("UI-STREAM-DETAIL-003: real controller retains denied/stale/unknown/pending and duplicate rules for detail intents", async () => {
  let permissions = ["streams.start", "streams.stop"];
  let freshness: "fresh" | "stale" = "fresh";
  let release!: () => void;
  let calls = 0;
  const controller = createStreamActionController({ getPermissions: () => ({ kind: "ready", permissions }),
    getState: () => ({ kind: "ready", freshness, fingerprint: "same-owner" }), mutate: () => { calls++; return new Promise<void>((resolve) => { release = resolve; }); } });
  const intent = { id: "STR-08" as const, stream };
  permissions = []; assert.notEqual(controller.evaluate(intent).availability.kind, "allowed");
  permissions = ["streams.start"]; freshness = "stale"; assert.notEqual(controller.evaluate(intent).availability.kind, "allowed");
  freshness = "fresh";
  const opened = await controller.open(intent); assert.equal(opened.kind, "allowed");
  if (opened.kind !== "allowed") throw new Error("unreached");
  const first = controller.submit(opened, { confirmed: true });
  const duplicate = await controller.submit(opened, { confirmed: true });
  assert.equal(duplicate.kind, "blocked"); assert.equal(calls, 1);
  assert.equal(controller.evaluate(intent).availability.kind, "pending"); release(); await first;
});
test("UI-STREAM-DETAIL-004: actual parent callback owns result, refresh, and detail feedback", () => {
  const source = readFileSync(new URL("../../src/features/streams/streams-view.tsx", import.meta.url), "utf8");
  const notices: unknown[] = []; let refreshes = 0;
  const callback = actualCallback(source, "handleStreamActionResult", { ja: false, readinessResult,
    setActionNotice: (value: unknown) => notices.push(value), setCreatedStreams() {}, streamActionLabel: () => "Action",
    queryClient: { invalidateQueries() { refreshes++; } }, streamActionBlockedMessage: () => "Blocked", t: (key: string) => key });
  callback({ kind: "succeeded", value: { stream_id: stream.id, ready: true, missing_service_types: [], issues: [] } }, { id: "STR-08", stream });
  assert.equal(refreshes, 1); assert.equal((notices[0] as { streamID: string }).streamID, stream.id);
  assert.match(source, /<StreamDetailsDialog[^\n]*actionController=\{actionController\}[^\n]*onActionResult=\{handleStreamActionResult\}/);
  const detail = readFileSync(new URL("../../src/features/streams/stream-details-dialog.tsx", import.meta.url), "utf8");
  assert.match(detail, /<StreamDetailOperations stream=\{stream\} controller=\{actionController\} onResult=\{onActionResult\}/);
  assert.match(detail, /isPreviewableStreamStatus\(stream.status\)[^\n]*<StreamPreview stream=\{stream\} controller=\{actionController\}/);
});

test("UI-STREAM-DETAIL-005: real Readiness surface renders ready/not-ready/stale and denied/running reasons",()=>{
  const controller=createStreamActionController({getPermissions:()=>({kind:"ready",permissions:["*"]}),getState:()=>({kind:"ready",freshness:"fresh",fingerprint:"same"}),mutate:async()=>{throw Error("no render mutation");}});
  for(const [ready,version,state] of [[true,stream.updated_at,"ready"],[false,stream.updated_at,"not-ready"],[true,"older","stale"]] as const) {
    const html=renderToStaticMarkup(createElement(I18nProvider,null,createElement(StreamDetailOperations,{stream,controller,onResult(){},notice:{tone:"success",message:"safe",streamID:stream.id,readiness:{ready,missing:ready?0:1,issues:0,version}}})));
    assert.ok(html.includes('data-readiness="'+state+'"'));if(!ready)assert.match(html,/未割当 1件/);
  }
  const denied=createStreamActionController({getPermissions:()=>({kind:"ready",permissions:[]}),getState:()=>({kind:"ready",freshness:"fresh",fingerprint:"same"}),mutate:async()=>{throw Error("denied");}});
  const html=renderToStaticMarkup(createElement(I18nProvider,null,createElement(StreamDetailOperations,{stream:{...stream,status:"live"},controller:denied,onResult(){},notice:null})));
  assert.match(html,/権限/);assert.equal(denied.evaluate({id:"STR-04",stream:{...stream,status:"live"}}).visibility.kind,"hidden");
  const buttons=html.match(/<button[^>]*>/g)||[];assert.equal(buttons.length,2);assert.ok(buttons.every(button=>button.includes('disabled=""')));
  assert.notEqual(controller.evaluate({id:"STR-04",stream:{...stream,status:"live"}}).availability.kind,"allowed");
  assert.equal(controller.evaluate({id:"STR-05",stream:{...stream,status:"live"}}).availability.kind,"allowed");
});
test("UI-STREAM-DETAIL-006: actual control submit reports 409, unavailable and ambiguous outcomes without retries",async()=>{
  const {APIError}=await import("../../src/lib/api/client.ts");
  const source=readFileSync(new URL("../../src/features/streams/stream-action-control.tsx",import.meta.url),"utf8");
  const dialogStateForResult=actualFunction(source,"dialogStateForResult",{});
  for(const [error,expected] of [[new APIError("hidden",409,"conflict"),"failed"],[new TypeError("hidden-network"),"outcome_unknown"],[new APIError("hidden",503,"temporarily_unavailable"),"outcome_unknown"]] as const) {
    let mutations=0;const outcomes:unknown[]=[];const states:{kind:string}[]=[];
    const controller=createStreamActionController({getPermissions:()=>({kind:"ready",permissions:["*"]}),getState:()=>({kind:"ready",freshness:"fresh",fingerprint:"same"}),mutate:async()=>{mutations++;throw error;}});
    const opened=await controller.open({id:"STR-08",stream});assert.equal(opened.kind,"allowed");
    if(opened.kind!=="allowed")throw Error("unreached");
    const submit=actualCallback(source,"submit",{controller,onResult:(value:unknown)=>{outcomes.push(value);return false;},setDialogState:(value:{kind:string})=>states.push(value),setOpened(){},dialogStateForResult});
    await submit(opened);assert.equal(mutations,1);assert.equal((outcomes[0] as {kind:string}).kind,expected);
    assert.equal(states.at(-1)?.kind,expected==="failed"?"conflict":"outcome-unknown");
    if(expected==="outcome_unknown") {const retry=await controller.open({id:"STR-08",stream});assert.equal(retry.kind,"blocked");assert.equal(mutations,1);}
  }
});

test("UI-STREAM-PREVIEW-001: actual runner waits for the real required issue response and retains exact POST403",async()=>{
  const {createHarnessFixture,settlePromptly}=await import("../helpers/browser-cdp-socket-fixture.mts");
  const {createUIFixture}=await import("./route-fixture.mts");
  const {conditions,inventory}=await import("./matrix.mts");
  const {waitForLivePreview,assertPreviewIssue}=await import("./run-browser.mts");
  const condition=conditions.find(row=>row.family==="stream-detail"&&row.exercise==="Detail")!;
  const surface=inventory.surfaces.find(row=>row.id===condition.family)!;
  const fixture=createUIFixture("http://ui.test");fixture.reset(condition,surface.primary);
  const selected=fixture.detailStream(),path="/streams/"+encodeURIComponent(selected.id)+"/preview-links";
  const owner=createHarnessFixture();owner.harness.setRouteResolver(fixture.resolver);
  owner.socket.hold("Fetch.fulfillRequest");
  try {
    const waiting=waitForLivePreview(owner.harness,selected,condition);
    owner.socket.emitEvent("Fetch.requestPaused",{requestId:"preview",resourceType:"XHR",frameId:"main",request:{method:"POST",url:"http://ui.test"+path,postData:"{}"}});
    const command=await owner.socket.waitForCommand("Fetch.fulfillRequest");
    assert.equal(command.params.responseCode,403);assert.equal(owner.harness.requests.get(path),1);
    assert.equal(owner.harness.responses.get(path)||0,0);assert.equal(await settlePromptly(waiting),"pending");
    owner.socket.respond(command,{result:{}});await waiting;assert.equal(owner.harness.responses.get(path),1);
    assertPreviewIssue(fixture.trace,selected);
    for(const mutations of [[],[...fixture.trace,...fixture.trace],fixture.trace.map(row=>({...row,status:200})),fixture.trace.map(row=>({...row,method:"GET"})),fixture.trace.map(row=>({...row,path:"/streams/other/preview-links"}))]) {
      assert.throws(()=>assertPreviewIssue(mutations,selected));
    }
  } finally {fixture.release();await owner.harness.close();}
});

test('UI-STREAM-PREVIEW-009: actual public-label guard accepts 1/128 and rejects 129/long fixtures before any mutation',async()=>{
  const {conditions}=await import('./matrix.mts');
  const {createUIFixture}=await import('./route-fixture.mts');
  const {previewExpectation,waitForLivePreview,previewUnavailableCopy}=await import('./run-browser.mts');
  const {translate}=await import('../../src/lib/i18n.ts');
  const preview=readFileSync(new URL('../../src/features/streams/stream-preview.tsx',import.meta.url),'utf8');
  let mutations=0;
  const controller=createStreamActionController({getPermissions:()=>({kind:'ready',permissions:['*']}),getState:()=>({kind:'ready',freshness:'fresh',fingerprint:'same'}),mutate:async()=>{mutations++;}});
  for(const length of [1,128,129,432]){
    const result=await controller.open({id:'STR-11',stream:{...stream,status:'live',name:'a'.repeat(length)}});
    assert.equal(result.kind,length<=128?'allowed':'blocked');
  }
  const planned=conditions.filter(row=>row.family==='stream-detail'&&['long-id','long-text'].includes(row.exercise||''));assert.equal(planned.length,16);
  for(const condition of planned){
    const locale=condition.locale;assert.ok(locale==='ja'||locale==='en');
    const fixture=createUIFixture('http://ui.test');fixture.reset(condition,'/streams');const selected=fixture.detailStream();
    try{
      assert.equal(previewExpectation(condition,selected),'guard-rejected');
      let unavailable='',state='';
      const issue=actualCallback(preview,'issuePreviewLink',{issuePending:false,streamRef:{current:selected},controller,setIssuePending(){},setPreviewLink(){},setPreviewLinkError:(value:string)=>{unavailable=value;},setPlaybackState:(value:string)=>{state=value;},t:(key:Parameters<typeof translate>[1])=>translate(locale,key)});
      await issue();assert.equal(unavailable,previewUnavailableCopy[locale]);assert.equal(state,'error');assert.equal(mutations,0);
      const requests=new Map<string,number>(),browser={requests,waitFor:async(_expression:string,predicate:(value:string)=>boolean)=>assert.ok(predicate(unavailable))};
      await Reflect.apply(waitForLivePreview,undefined,[browser,selected,condition]);
      requests.set('/streams/'+encodeURIComponent(selected.id)+'/preview-links',1);
      await assert.rejects(Reflect.apply(waitForLivePreview,undefined,[browser,selected,condition]),/must not issue/);
      requests.clear();unavailable='unrelated HTTP failure';await assert.rejects(Reflect.apply(waitForLivePreview,undefined,[browser,selected,condition]));
    } finally {fixture.release();}
  }
});
