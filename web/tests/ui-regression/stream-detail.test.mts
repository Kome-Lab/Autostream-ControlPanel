import "./component-loader.mts";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";
import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { actualCallback, actualFunction } from "./source-callback.mts";
const { I18nProvider } = await import("../../src/components/admin/i18n-provider.tsx");
const { StreamDetailOperations, readinessResult } = await import("../../src/features/streams/stream-detail-operations.tsx");
const { createStreamActionController } = await import("../../src/features/streams/stream-action-controller.ts");
const stream = { id: "stream-a", name: "日本語の利用者名", status: "ready", updated_at: "2026-09-01T00:00:00Z" };

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
