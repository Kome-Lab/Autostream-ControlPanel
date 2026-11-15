import assert from "node:assert/strict";
import { register } from "node:module";
import { createServer, type RequestListener } from "node:http";
import test from "node:test";

register(`data:text/javascript,${encodeURIComponent([
  "let root; export function initialize(data) { root=data.root; }",
  "export async function resolve(specifier,context,next) {",
  "if(specifier.startsWith('@/')) return next(new URL('src/'+specifier.slice(2)+'.ts',root).href,context);",
  "if(specifier.startsWith('.') && context.parentURL?.startsWith(root+'src/') && !/\\.[cm]?[jt]sx?$/.test(specifier)) return next(new URL(specifier+'.ts',context.parentURL).href,context);",
  "return next(specifier,context); }",
].join("\n"))}`, { parentURL: import.meta.url, data: { root: new URL("../", import.meta.url).href } });

const { createStreamActionController } = await import("../src/features/streams/stream-action-controller.ts");
const { createStreamVisualActionController } = await import("../src/features/streams/stream-visual-action-controller.ts");
const { createVideoCoverActionController, buildVideoCoverAction } = await import("../src/features/streams/control-platform.ts");
const { isConfirmedStreamStopOutcome } = await import("../src/features/streams/stream-action-response.ts");

const stream = { id: "fixture-stream", name: "Synthetic stream", status: "live", updated_at: "2026-11-15T00:00:00Z" };
const cover = { stream_id: stream.id, job_generation: 7, desired_active: true, desired_revision: 11, applied_active: true, applied_revision: 11, status: "applied" as const, pipeline_order: [], cover_watermark_independent: true };
const visual = { stream_id: stream.id, revision: 12, background_mode: "default" as const, header_title_mode: "default" as const, discord_snapshot_revision: 0, cover_source: "none" as const, cover_start_active: false };

function barrier() {
  let release!: () => void;
  const wait = new Promise<void>((resolve) => { release=resolve; });
  return { wait, release };
}

async function fixture(t: { after(fn: () => unknown): void }, respond: RequestListener) {
  const server = createServer(respond);
  await new Promise<void>((resolve) => server.listen(0,"127.0.0.1",resolve));
  t.after(() => new Promise<void>((resolve,reject) => server.close((error) => error ? reject(error) : resolve())));
  const address=server.address();
  assert.ok(address && typeof address === "object");
  return { origin:`http://127.0.0.1:${address.port}`, server };
}

test("WS7-COV-01: permissions, fixed request and fresh confirmation prevent stale dispatch", async (t) => {
  for (const permissions of [[],["streams.show_cover"],["streams.hide_cover"],["*"]]) {
    await t.test(`permission ${permissions.join(",") || "none"}`, async () => {
      let sends=0;
      const controller=createVideoCoverActionController(() => ({status:"ready",permissions}),async () => { sends++; return cover; });
      assert.equal((await controller.issue(true,cover,"fixture-show")).sent,permissions.includes("*")||permissions.includes("streams.show_cover"));
      assert.equal((await controller.issue(false,cover,"fixture-hide",true)).sent,permissions.includes("*")||permissions.includes("streams.hide_cover"));
      assert.equal(sends,Number(permissions.includes("*")||permissions.includes("streams.show_cover"))+Number(permissions.includes("*")||permissions.includes("streams.hide_cover")));
    });
  }
  await t.test("the exact confirmation intent cannot borrow caller mutations", async () => {
    let sentBody: unknown;
    const mutable={...stream};
    const controller=createStreamActionController({getPermissions:()=>({kind:"ready",permissions:["streams.stop"]}),getState:()=>({kind:"ready",freshness:"fresh",fingerprint:"original"}),mutate:async request=>{sentBody=request.body;return {stream:{...stream,status:"completed"},dispatch:[{accepted:true}]};}});
    const opened=await controller.open({id:"STR-05",stream:mutable});
    assert.equal(opened.kind,"allowed");if(opened.kind!=="allowed")throw Error("not opened");
    mutable.updated_at="2026-11-16T00:00:00Z";
    await controller.submit(opened,{confirmed:true});
    assert.deepEqual(sentBody,{expected_updated_at:stream.updated_at});
  });
  await t.test("a changed cached target after the fresh proof sends zero mutations", async () => {
    let fingerprint="original",sends=0;
    const controller=createStreamActionController({getPermissions:()=>({kind:"ready",permissions:["streams.stop"]}),getState:()=>({kind:"ready",freshness:"fresh",fingerprint}),revalidate:async()=>{fingerprint="another-generation";return {permissions:{kind:"ready",permissions:["streams.stop"]},state:{kind:"ready",freshness:"fresh",fingerprint:"original"}};},mutate:async()=>{sends++;return undefined;}});
    const opened=await controller.open({id:"STR-05",stream});assert.equal(opened.kind,"allowed");if(opened.kind!=="allowed")throw Error("not opened");
    const result=await controller.submit(opened,{confirmed:true});assert.equal(result.kind,"blocked");assert.equal(sends,0);
  });
});

test("WS7-COV-03: confirming and ACK loss have different same-attempt read obligations", async (t) => {
  await t.test("accepted confirming requires exact stream, generation and revision", () => {
    const controller=createVideoCoverActionController(()=>({status:"ready",permissions:["*"]}),async()=>cover);
    const accepted={...cover,desired_active:false,desired_revision:12,status:"confirming" as const};controller.holdForReconciliation(accepted);
    for(const wrong of [{...accepted,stream_id:"other"},{...accepted,job_generation:8},{...accepted,desired_revision:13}])assert.equal(controller.reconcile({...wrong,status:"applied",applied_active:false,applied_revision:wrong.desired_revision}),false);
    assert.equal(controller.reconcile({...accepted,status:"applied",applied_active:false,applied_revision:12}),true);
  });
  await t.test("provider accepted response loss never becomes a new PUT", async () => {
    let sends=0;
    const controller=createVideoCoverActionController(()=>({status:"ready",permissions:["*"]}),async()=>{sends++;throw new TypeError("synthetic response loss");});
    await assert.rejects(controller.issue(false,cover,"same-effect",true));
    assert.equal(controller.reconcile({...cover,desired_active:false,desired_revision:12,applied_active:false,applied_revision:12}),false);
    assert.equal((await controller.issue(false,cover,"different-attempt",true)).sent,false);assert.equal(sends,1);
  });
  await t.test("request fields are fixed before a held revalidation", async () => {
    const entered=barrier(),resume=barrier();let sent:unknown;
    const controller=createVideoCoverActionController(()=>({status:"ready",permissions:["*"]}),async request=>{sent=request;return cover;},()=>({status:"ready",supported:true}),async()=>{entered.release();await resume.wait;return {permission:{status:"ready",permissions:["*"]},capability:{status:"ready",supported:true},valid:true};});
    const request=buildVideoCoverAction(false,cover,"fixed-key",true);const pending=controller.issueRequest(request);await entered.wait;request.active=true;resume.release();await pending;
    assert.equal((sent as {active:boolean}).active,false);
  });
});

test("WS7-VIS-01: immutable requested fields and exact response correlation survive held reads", async (t) => {
  await t.test("fields cannot change during revalidation", async () => {
    const entered=barrier(),resume=barrier();let sent:Record<string,unknown>|undefined;
    const fields={header_title_mode:"custom",header_title_value:"Original"};
    const controller=createStreamVisualActionController({getPermission:()=>({kind:"ready",permissions:["streams.update"]}),getState:()=>({kind:"ready",freshness:"fresh",revision:12,fingerprint:"draft",streamID:stream.id}),revalidate:async()=>{entered.release();await resume.wait;return {permission:{kind:"ready",permissions:["streams.update"]},state:{kind:"ready",freshness:"fresh",revision:12,fingerprint:"draft",streamID:stream.id}};},mutate:async request=>{sent=request;return {...visual,revision:13,header_title_mode:"custom",header_title_value:"Original"};}});
    const pending=controller.issue(fields,12);await entered.wait;fields.header_title_value="Changed while waiting";resume.release();assert.equal((await pending).kind,"succeeded");assert.equal(sent?.header_title_value,"Original");
  });
  for(const invalid of [{...visual,revision:13,stream_id:"other"},{...visual,revision:12},{...visual,revision:13,header_title_mode:"custom",header_title_value:"Other writer"}]){
    await t.test(`wrong response ${invalid.stream_id}/${invalid.revision}/${invalid.header_title_value||"default"}`,async()=>{
      let puts=0;const controller=createStreamVisualActionController({getPermission:()=>({kind:"ready",permissions:["streams.update"]}),getState:()=>({kind:"ready",freshness:"fresh",revision:12,fingerprint:"draft",streamID:stream.id}),mutate:async()=>{puts++;return invalid;}});
      assert.equal((await controller.issue({header_title_mode:"custom",header_title_value:"Requested"},12)).kind,"outcome_unknown");assert.equal(controller.unresolved,true);assert.equal((await controller.issue({},12)).kind,"blocked");assert.equal(puts,1);
    });
  }
});

test("WS7-STOP-01: same-source HTTP lookup never substitutes a status or sends another Stop", async (t) => {
  let posts=0,gets=0;let receipt:unknown={stream:{...stream,status:"completed"},stop_confirmed:false,source_updated_at:stream.updated_at};
  const {origin}=await fixture(t,(request,response)=>{if(request.method==="POST"){posts++;response.destroy();return;}gets++;response.setHeader("Content-Type","application/json");response.end(JSON.stringify(receipt));});
  const intent={id:"STR-05" as const,stream};
  const controller=createStreamActionController({getPermissions:()=>({kind:"ready",permissions:["streams.stop"]}),getState:()=>({kind:"ready",freshness:"fresh",fingerprint:"live"}),mutate:async()=>{const response=await fetch(origin+"/stop",{method:"POST"});return response.json();},readStopOutcome:async original=>{const response=await fetch(origin+"/stop-outcome?expected_updated_at="+encodeURIComponent(original.stream!.updated_at!));return response.json();}});
  const opened=await controller.open(intent);assert.equal(opened.kind,"allowed");if(opened.kind!=="allowed")throw Error("not opened");
  assert.equal((await controller.submit(opened,{confirmed:true})).kind,"outcome_unknown");assert.equal(posts,1);assert.equal(gets,1);
  assert.equal((await controller.reconcileStop({...intent,stream:{...stream,updated_at:"2026-11-16T00:00:00Z"}})).kind,"blocked");assert.equal(gets,1,"a new source version cannot replace the unresolved original Stop");
  for(const invalid of [{stream:{...stream,status:"stopping"}},{stream:{...stream,id:"other",status:"completed"},stop_confirmed:true,source_updated_at:stream.updated_at},{stream:{...stream,status:"completed"},stop_confirmed:true,source_updated_at:"2026-11-16T00:00:00Z"}])assert.equal(isConfirmedStreamStopOutcome(invalid,intent),false);
  receipt={stream:{...stream,status:"completed"},stop_confirmed:true,source_updated_at:stream.updated_at};assert.equal((await controller.reconcileStop(intent)).kind,"succeeded");assert.equal(posts,1);assert.equal(gets,2);
  await t.test("completed alone is not the normal Stop response envelope",async()=>{
    const bare=createStreamActionController({getPermissions:()=>({kind:"ready",permissions:["streams.stop"]}),getState:()=>({kind:"ready",freshness:"fresh",fingerprint:"live"}),mutate:async()=>({...stream,status:"completed"})});
    const opened=await bare.open(intent);if(opened.kind!=="allowed")throw Error("not opened");assert.equal((await bare.submit(opened,{confirmed:true})).kind,"outcome_unknown");
  });
});
