import "./ui-regression/component-loader.mts";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { createServer } from "node:http";
import { join } from "node:path";
import test from "node:test";
import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
const { lifecycleOutcomePresentation, lifecycleContractsCommit, releaseRequirementPresentation } = await import("../src/features/streams/lifecycle-outcome-presentation.ts");
const { LifecycleOutcomeView } = await import("../src/features/streams/lifecycle-outcome-view.tsx");
const { readStreamLifecycleOutcome } = await import("../src/features/streams/stream-lifecycle-outcome.ts");
const { isVideoCoverActionResponse } = await import("../src/features/streams/control-platform.ts");
const { StreamControlPlatformPanel } = await import("../src/features/streams/stream-control-platform-panel.tsx");
const { I18nProvider } = await import("../src/components/admin/i18n-provider.tsx");

// Only the test process can select actual cross-repository export files. Product
// components have no environment fallback and ordinary startup stays unconnected.
const fixed=(name:string)=>JSON.parse(readFileSync(process.env.WS7_WS5_RESULT_DIR ? join(process.env.WS7_WS5_RESULT_DIR,name+".json") : new URL("./fixtures/v3-workstreams/ws5-"+name+".json",import.meta.url),"utf8"));
const coherentRelease={profile:"IU-WS6-01/RELEASE-RETURN/1",contracts_commit:lifecycleContractsCommit,status:"StructurallyCoherent",return_scenario:"RET-S1",contract_result:{Kind:"STRUCTURALLY_COHERENT",Issues:[]},reasons:[],authorizes_mutation:false,reusable_for_commit:false,production_qualified:false,common_restore_freshness_proven:false};
const mediaFixed=()=>JSON.parse(readFileSync(process.env.WS7_WS3_RESULT_FILE || new URL("./fixtures/v3-workstreams/ws3-media-boundaries.json",import.meta.url),"utf8"));

test("WS7-OPS-008: fixed shared results reach the actual display without granting delivery or recovery",async t=>{
  await t.test("known provider acceptance survives local UNKNOWN",()=>{
    const result=lifecycleOutcomePresentation(fixed("provider-accepted-local-unknown"),"notification-domain");
    assert.equal(result.connection,"observed");assert.equal(result.remoteProof,"not-qualified");assert.equal(result.gate,"NOT_QUALIFIED");
    assert.deepEqual(result.boundaries.map(row=>[row.subBoundary,row.knowledge,row.outcome]).sort(),[
      ["INTENT","KNOWN","SUCCESS_AT_BOUNDARY"],["DISPATCH_POSSIBLE","KNOWN","SUCCESS_AT_BOUNDARY"],["REMOTE_ACCEPT","KNOWN","SUCCESS_AT_BOUNDARY"],["LOCAL_RESULT_RECORD","UNKNOWN","UNKNOWN"],
    ].sort());
    assert.equal(result.remaining,2);
  });
  await t.test("ACK loss remains a Provider UNKNOWN, not an absent dispatch",()=>{
    const result=lifecycleOutcomePresentation(fixed("provider-ack-unknown"));
    assert.equal(result.connection,"observed");assert.equal(result.boundaries.find(row=>row.subBoundary==="REMOTE_ACCEPT")?.knowledge,"UNKNOWN");assert.equal(result.remoteProof,"not-qualified");
  });
  await t.test("foreign correlation, unknown enum/profile and hostile detail never become confirmation",()=>{
    const original=fixed("provider-accepted-local-known");
    const wrong=structuredClone(original);wrong.Inspection.Target.Correlation.AttemptID="different-attempt";
    const enumWrong=structuredClone(original);enumWrong.Inspection.Observations.Observations[0].Address.SubBoundary="DELIVERED";
    const profileWrong=structuredClone(original);profileWrong.Header.ProfileRef.ProfileID="invented-profile";
    for(const value of [wrong,enumWrong,profileWrong])assert.equal(lifecycleOutcomePresentation(value).connection,"unknown");
    for(const kind of ["toString","__proto__",{toString:null}]){const malformed=structuredClone(original);malformed.Inspection.Observations.Observations[0].Address.Kind=kind;assert.equal(lifecycleOutcomePresentation(malformed).connection,"unknown");}
    for(const kind of ["UNREGISTERED","https://fixture.invalid/private-resource-kind",{toString:null}]){const malformed=structuredClone(original);malformed.Inspection.Observations.Observations[0].Address.Resource.Kind=kind;assert.equal(lifecycleOutcomePresentation(malformed).connection,"unknown");assert.doesNotMatch(renderToStaticMarkup(createElement(LifecycleOutcomeView,{notification:malformed})),/fixture\.invalid|private-resource-kind/);}
    const malformedProfile=structuredClone(original);malformedProfile.Header.ProfileRef.ProfileID={toString:null};assert.equal(lifecycleOutcomePresentation(malformedProfile).connection,"unknown");
    assert.equal(lifecycleOutcomePresentation(original,"another-target").connection,"unknown");
    const held=lifecycleOutcomePresentation({...original,Inspection:{...original.Inspection,Observations:{...original.Inspection.Observations,Gate:"HELD"}}});
    assert.match(renderToStaticMarkup(createElement(LifecycleOutcomeView,{outcome:held})),/HELD/);assert.equal(held.remoteProof,"not-qualified");
    original.internal_detail="https://fixture.invalid/private-payload";
    const html=renderToStaticMarkup(createElement(LifecycleOutcomeView,{notification:original}));
    assert.doesNotMatch(html,/fixture\.invalid|private-payload/);assert.match(html,/未資格/);
  });
  await t.test("WS6 statuses are structural or missing evidence/decision and always nonauthority",()=>{
    for(const [status,kind] of [["StructurallyCoherent","STRUCTURALLY_COHERENT"],["NeedsEvidence","NEEDS_EVIDENCE"],["NeedsDecision","NEEDS_DECISION"],["Contradiction","CONFLICT"]] as const){
      const value={profile:"IU-WS6-01/RELEASE-RETURN/1",contracts_commit:lifecycleContractsCommit,status,return_scenario:"RET-S3",contract_result:{Kind:kind,Issues:[]},reasons:["synthetic-ref"],authorizes_mutation:false,reusable_for_commit:false,production_qualified:false,common_restore_freshness_proven:false};
      assert.deepEqual(releaseRequirementPresentation(value),{connection:"observed",status,returnScenario:"RET-S3",authorizesMutation:false});
      for(const field of ["authorizes_mutation","reusable_for_commit","production_qualified","common_restore_freshness_proven"])assert.equal(releaseRequirementPresentation({...value,[field]:true}).connection,"unknown");
      const html=renderToStaticMarkup(createElement(LifecycleOutcomeView,{release:value,notification:fixed("provider-accepted-local-unknown"),locale:"en"}));
      assert.match(html,/No execution authority/);assert.match(html,/UNKNOWN/);assert.doesNotMatch(html,/recovery available|delivery complete|synthetic-ref/i);
    }
  });
  await t.test("normal display makes missing Ops backend connections explicit in both locales",()=>{
    for(const locale of ["ja","en"] as const){const html=renderToStaticMarkup(createElement(LifecycleOutcomeView,{locale}));assert.match(html,locale==="ja"?/未接続・未資格/:/Unconnected \/ unqualified/);assert.match(html,locale==="ja"?/実行権限なし/:/No execution authority/);}
  });
  await t.test("Media enqueue reports a known pending terminal boundary with a separate unknown physical duty",()=>{
    const input=mediaFixed(),result=lifecycleOutcomePresentation(input);
    assert.equal(result.connection,"observed");assert.equal(result.gate,"HELD");assert.equal(result.remoteProof,"not-qualified");
    assert.deepEqual(result.boundaries.map(row=>[row.subBoundary,row.knowledge,row.outcome]).sort(),[
      ["RAW_WRITE","KNOWN","SUCCESS_AT_BOUNDARY"],["DURABILITY","KNOWN","SUCCESS_AT_BOUNDARY"],["QUEUE_ADMISSION","KNOWN","SUCCESS_AT_BOUNDARY"],["QUEUE_TERMINAL","KNOWN","PENDING"],
    ].sort());
    assert.equal(result.boundaries.find(row=>row.subBoundary==="RAW_WRITE")?.resourceKind,"RUN");
    assert.equal(result.boundaries.find(row=>row.subBoundary==="QUEUE_ADMISSION")?.resourceKind,"PARTITION");
    const html=renderToStaticMarkup(createElement(LifecycleOutcomeView,{media:input,locale:"en"}));
    assert.equal(result.remaining,1,"the unknown queue overlaps the RemainingObligations reference and the known closed FD is not counted");
    assert.match(html,/Media storage \/ queue boundaries/);assert.match(html,/HELD/);assert.match(html,/QUEUE_TERMINAL: KNOWN \/ PENDING/);assert.match(html,/Unqualified; HTTP and playback do not prove remote state/);assert.doesNotMatch(html,/remote proof verified|delivery complete/i);
  });
  await t.test("remaining duties validate physical fact unions and count each unresolved ID only once",()=>{
    const input=mediaFixed(),set=input.Inspection.Observations;
    const fd=set.PhysicalObligations.find((value:{Kind:string})=>value.Kind==="FD"),queue=set.PhysicalObligations.find((value:{Kind:string})=>value.Kind==="QUEUE");
    assert.ok(fd);assert.ok(queue);assert.equal(fd.Knowledge,"KNOWN");assert.equal(fd.Terminal.State,"OBSERVED");assert.equal(queue.Knowledge,"UNKNOWN");assert.equal(queue.Terminal.State,"UNAVAILABLE");assert.ok(set.RemainingObligations.includes(queue.ObligationID));
    assert.equal(lifecycleOutcomePresentation(input).remaining,1);
    const extra=structuredClone(input);extra.Inspection.Observations.RemainingObligations.push("extra-unresolved-duty");assert.equal(lifecycleOutcomePresentation(extra).remaining,2);
    const closedOnly=structuredClone(input);closedOnly.Inspection.Observations.PhysicalObligations=[fd];closedOnly.Inspection.Observations.RemainingObligations=[];assert.equal(lifecycleOutcomePresentation(closedOnly).remaining,0);
    const conflict=structuredClone(input);conflict.Inspection.Observations.PhysicalObligations.find((value:{Kind:string})=>value.Kind==="QUEUE").Knowledge="CONFLICT";assert.equal(lifecycleOutcomePresentation(conflict).remaining,1);
    const mutations=[
      (value:typeof input)=>{value.Inspection.Observations.PhysicalObligations.find((item:{Kind:string})=>item.Kind==="QUEUE").Knowledge="KNOWN";},
      (value:typeof input)=>{value.Inspection.Observations.PhysicalObligations.find((item:{Kind:string})=>item.Kind==="FD").Terminal.Unavailable={Code:"MIXED_UNION"};},
      (value:typeof input)=>{value.Inspection.Observations.PhysicalObligations.find((item:{Kind:string})=>item.Kind==="QUEUE").Terminal.Value="MIXED_UNION";},
      (value:typeof input)=>{value.Inspection.Observations.PhysicalObligations[0].Kind="UNREGISTERED";},
      (value:typeof input)=>{value.Inspection.Observations.PhysicalObligations.find((item:{Kind:string})=>item.Kind==="FD").Terminal.Observation.OwnerID="foreign-owner";},
      (value:typeof input)=>{value.Inspection.Observations.PhysicalObligations.push(structuredClone(queue));},
      (value:typeof input)=>{value.Inspection.Observations.RemainingObligations.push("");},
    ];
    for(const mutate of mutations){const malformed=structuredClone(input);mutate(malformed);assert.equal(lifecycleOutcomePresentation(malformed).connection,"unknown");}
  });
  await t.test("distinct same-kind sinks keep separate safe resource labels without raw owner or resource IDs",()=>{
    const input=mediaFixed(),first=input.Inspection.Observations.Observations.find((row:{Address:{SubBoundary:string}})=>row.Address.SubBoundary==="RAW_WRITE");
    const second=structuredClone(first);second.Address.Resource.ResourceID="private-second-sink-reference";input.Inspection.Observations.Observations.push(second);
    const result=lifecycleOutcomePresentation(input),raw=result.boundaries.filter(row=>row.subBoundary==="RAW_WRITE");
    assert.equal(result.connection,"observed");assert.equal(raw.length,2);assert.equal(raw[0].resourceKind,raw[1].resourceKind);assert.notEqual(raw[0].resourceOrdinal,raw[1].resourceOrdinal);assert.equal(raw[0].ownerOrdinal,raw[1].ownerOrdinal);
    const html=renderToStaticMarkup(createElement(LifecycleOutcomeView,{media:input,locale:"en"}));
    assert.match(html,new RegExp(`RUN ${raw[0].resourceOrdinal} / Observed owner ${raw[0].ownerOrdinal}`));assert.match(html,new RegExp(`RUN ${raw[1].resourceOrdinal} / Observed owner ${raw[1].ownerOrdinal}`));
    assert.doesNotMatch(html,/private-second-sink-reference/);assert.ok(!html.includes(first.Address.OwnerID));assert.ok(!html.includes(first.Address.Resource.ResourceID));
  });
  await t.test("actual stream panel consumes fixture inputs and optional real cross-repository exports",()=>{
    const notification=fixed("provider-accepted-local-unknown");
    const release=process.env.WS7_WS6_RESULT_FILE ? JSON.parse(readFileSync(process.env.WS7_WS6_RESULT_FILE,"utf8")) : coherentRelease;
    assert.equal(release.status,"StructurallyCoherent");assert.equal(release.return_scenario,"RET-S1");assert.equal(release.common_restore_freshness_proven,false);
    assert.equal(releaseRequirementPresentation(release).connection,"observed");
    const stream={id:"panel-fixture",name:"Synthetic panel",status:"live",updated_at:"2026-11-15T00:00:00Z"};
    const client=new QueryClient({defaultOptions:{queries:{retry:false,staleTime:Infinity,gcTime:Infinity}}});
    client.setQueryData(["auth","me"],{permissions:["*"]});client.setQueryData(["streams"],[stream]);client.setQueryData(["service-health"],[]);
    client.setQueryData(["streams",stream.id,"visual-settings"],{stream_id:stream.id,revision:1,background_mode:"default",header_title_mode:"default",discord_snapshot_revision:0,cover_source:"none",cover_start_active:false});
    client.setQueryData(["streams",stream.id,"video-cover-state"],{stream_id:stream.id,job_generation:1,desired_active:false,desired_revision:1,applied_active:false,applied_revision:1,status:"applied",pipeline_order:[],cover_watermark_independent:true});
    try{
      const render=(operationsResults?:{notification:unknown;media?:unknown;release:unknown})=>renderToStaticMarkup(createElement(QueryClientProvider,{client},createElement(I18nProvider,{initialLocale:"en"},createElement(StreamControlPlatformPanel,{stream,operationsResults}))));
      const html=render({notification,media:mediaFixed(),release});assert.match(html,/Boundary observations only/);assert.match(html,/LOCAL_RESULT_RECORD: UNKNOWN \/ UNKNOWN/);assert.match(html,/RAW_WRITE: KNOWN \/ SUCCESS_AT_BOUNDARY/);assert.match(html,/DURABILITY: KNOWN \/ SUCCESS_AT_BOUNDARY/);assert.match(html,/QUEUE_ADMISSION: KNOWN \/ SUCCESS_AT_BOUNDARY/);assert.match(html,/QUEUE_TERMINAL: KNOWN \/ PENDING/);assert.match(html,/HELD/);assert.match(html,/Structurally coherent only/);assert.match(html,/No execution authority/);assert.match(html,/common restore do not prove freshness/);assert.doesNotMatch(html,/delivery complete|recovery available/i);
      assert.match(render(),/Unconnected \/ unqualified/);
    }finally{client.clear();}
  });
  await t.test("WS2 real header reader is an exact GET with no implicit mock fallback",async()=>{
    const vector=fixed("provider-accepted-local-unknown");let calls=0;
    const server=createServer((request,response)=>{calls++;assert.equal(request.method,"GET");assert.match(request.url||"",/lifecycle_outcome=1/);response.setHeader("content-type","application/json");response.setHeader("X-AutoStream-Lifecycle-Outcome",Buffer.from(JSON.stringify(vector)).toString("base64url"));response.setHeader("X-AutoStream-Lifecycle-Result","UNKNOWN");response.end(JSON.stringify({state:"synthetic"}));});
    await new Promise<void>(resolve=>server.listen(0,"127.0.0.1",resolve));const address=server.address();assert.ok(address&&typeof address==="object");
    try{const value=await readStreamLifecycleOutcome<{state:string}>(`http://127.0.0.1:${address.port}/outcome`,"notification-domain");assert.equal(value.value.state,"synthetic");assert.equal(value.presentation.connection,"observed");assert.equal(value.presentation.boundaries.find(row=>row.subBoundary==="LOCAL_RESULT_RECORD")?.knowledge,"UNKNOWN");assert.equal(calls,1);}finally{await new Promise<void>((resolve,reject)=>server.close(error=>error?reject(error):resolve()));}
  });
  await t.test("Cover HTTP bodies must match the confirmed generation and exact next revision",()=>{
    const request={active:true,expected_job_generation:7,expected_revision:11,idempotency_key:"fixture-key",candidate_variant_id:"variant-fixture"};
    const value={stream_id:"stream-fixture",job_generation:7,desired_revision:12,desired_active:true,asset_variant_id:"variant-fixture",status:"confirming",applied_active:null,applied_revision:null,pipeline_order:[],cover_watermark_independent:true};
    assert.equal(isVideoCoverActionResponse(value,request,"stream-fixture"),true);
    for(const bad of [{...value,stream_id:"other"},{...value,job_generation:8},{...value,desired_revision:13},{...value,desired_active:false},{...value,asset_variant_id:"other"},{...value,status:"applied"}])assert.equal(isVideoCoverActionResponse(bad,request,"stream-fixture"),false);
    assert.equal(isVideoCoverActionResponse({...value,status:"applied",applied_revision:12,applied_active:true,applied_asset_variant_id:"variant-fixture"},request,"stream-fixture"),true);
  });
});
