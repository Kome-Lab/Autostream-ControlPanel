import assert from "node:assert/strict";
import { register } from "node:module";
import test from "node:test";
import { readFileSync } from "node:fs";
import { registerSourceResolution } from "./helpers/moved-source.mts";
import { actualCallback } from "./ui-regression/source-callback.mts";
import { buildStreamCreatePayload, buildStreamSettingsPayload, validateStreamCreateValues } from "../src/lib/stream-create.ts";
import type { Stream } from "../src/types/domain.ts";
import type { StreamActionIntent } from "../src/features/streams/stream-action-descriptors.ts";

const resolver=[
  "let root;",
  "export function initialize(data){root=data.root;}",
  "export function resolve(specifier,context,next){",
  " if(specifier.startsWith('@/')){const target=new URL('src/'+specifier.slice(2),root);if(!/\\.[cm]?[jt]sx?$/.test(target.pathname))target.pathname+='.ts';return next(target.href,context);}",
  " return next(specifier,context);",
  "}",
].join("\n");
register(`data:text/javascript,${encodeURIComponent(resolver)}`,{parentURL:import.meta.url,data:{root:new URL("../",import.meta.url).href}});
registerSourceResolution();
const { translate } = await import("../src/lib/i18n.ts");
const { definitiveStreamCreateRejection, streamCreateErrorRegistry, streamCreateErrorIssues }=await import("../src/features/streams/stream-create-errors.ts");
const { adaptAPIError }=await import("../src/lib/foundation/api-errors/adapter.ts");
const { defaultStreamVisualDraft, validateStreamVisualDraft }=await import("../src/features/streams/stream-visual-draft.ts");
const { buildStreamActionDescriptor, streamActionRequest, streamActionApplicable, streamSettingsPresentationLabel }=await import("../src/features/streams/stream-action-descriptors.ts");

test("create rejection is route-specific status and code proof, never a status guess",()=>{
  for(const [status,code] of [[400,"name_required"],[400,"invalid_visual_settings"],[409,"asset_claim_failed"],[409,"service_assignment_conflict"],[400,"encoder_service_not_found"]] as const){
    assert.equal(definitiveStreamCreateRejection({name:"APIError",status,code}),true);
  }
  for(const error of [
    {name:"APIError",status:400,code:"unregistered_validation"},
    {name:"APIError",status:409,code:"unknown_conflict"},
    {name:"APIError",status:500,code:"invalid_visual_settings"},
    {name:"APIError",status:500,code:"stream_visual_operation_failed"},
    {name:"APIError",status:201,code:"invalid_json_response"},
    new TypeError("lost response"),
    {name:"TimeoutError"},
    {get name(){throw new Error("untrusted getter");}},
  ])assert.equal(definitiveStreamCreateRejection(error),false);
});

test("registered create reasons identify safe fields without displaying arbitrary server details",async()=>{
  const error=adaptAPIError({name:"APIError",status:400,code:"encoder_service_required",message:"private SQL/path"}, {registry:streamCreateErrorRegistry});
  assert.equal(error.messageKey,"streamCreateEncoderRequired");
  assert.equal(error.diagnosticCode,"encoder_service_required");
  assert.deepEqual(streamCreateErrorIssues(error),[{field:"encoder",messageKey:"streamCreateEncoderRequired"}]);
  const unknown=adaptAPIError({name:"APIError",status:400,code:"private/path/token"}, {registry:streamCreateErrorRegistry});
  assert.equal(unknown.diagnosticCode,undefined);
  assert.deepEqual(streamCreateErrorIssues(unknown),[]);
  assert.equal(JSON.stringify(error).includes("private SQL"),false);
  const form = readFileSync(new URL("../src/features/streams/stream-slot-form.tsx", import.meta.url), "utf8");
  for (const locale of ["ja", "en"] as const) {
    const messages: string[] = [];
    const result = actualCallback(form, "handleSaveResult", {
      setSaveOutcomeUnresolved() {}, setServerIssues() {}, streamCreateErrorIssues,
      setMessage: (value: string) => messages.push(value),
      t: (key: Parameters<typeof translate>[1], values?: Parameters<typeof translate>[2]) => translate(locale, key, values),
      onActionResult: async () => undefined,
    });
    await result({ kind: "failed", error }, { id: "STR-01" });
    assert.ok(messages[0].includes(locale === "ja" ? "エラー識別子: encoder_service_required" : "Error identifier: encoder_service_required"));
    assert.equal(messages[0].includes("{code}"), false);
    assert.equal(messages[0].includes("private SQL"), false);
    await result({ kind: "failed", error: unknown }, { id: "STR-01" });
    assert.equal(messages[1].includes("private/path/token"), false);
  }
});

test("stream and cover names preserve full Unicode through the 255-character contract",()=>{
  assert.deepEqual(validateStreamCreateValues({name:"配信".repeat(127)+"枠"}),[]);
  assert.deepEqual(validateStreamCreateValues({name:"📷".repeat(255)}),[]);
  assert.deepEqual(validateStreamCreateValues({name:"あ".repeat(256)}),[{field:"name",code:"name_too_long"}]);
  for(const name of ["bad\u0000name","bad\u0085name","bad\u202ename"]){
    assert.deepEqual(validateStreamCreateValues({name}),[{field:"name",code:"name_invalid"}],"unsafe labels must identify the name field before confirmation");
  }
  const draft=defaultStreamVisualDraft();
  draft.coverCandidates=[{name:"📷".repeat(251)+".png",source:"upload",asset_id:"asset",variant_id:"variant"}];
  assert.equal(validateStreamVisualDraft(draft).ready,true);
  draft.coverCandidates[0].name="あ".repeat(252)+".png";
  assert.equal(validateStreamVisualDraft(draft).ready,false);
  draft.coverCandidates=[];
  draft.headerTitleMode="custom";
  draft.headerTitleValue="line\u0085break";
  assert.equal(validateStreamVisualDraft(draft).ready,false,"C1 controls must agree with the server's Unicode control check");
});

test("settings CAS stays out of create and remains present on edits",()=>{
  const values={name:"配信",discordConfigID:"",autoStartFromDiscord:false,youtubeOutputID:"",archiveProfileID:"",encoderProfileID:"",captionProfileID:"",watermarkEnabled:false,overlayProfileID:"",encoderAudioGainDB:0,encoderServiceID:"encoder",workerServiceID:"worker",expectedSettingsRevision:"2030-01-01T00:00:00.000001Z"};
  assert.equal(Object.hasOwn(buildStreamCreatePayload(values),"expected_settings_revision"),false);
  assert.equal(buildStreamSettingsPayload(values).expected_settings_revision,values.expectedSettingsRevision);
  assert.equal(validateStreamCreateValues({...values,scheduledStartAt:"invalid"}).at(0)?.code,"schedule_time_invalid");
});

test("create and settings retain full names while only their public presentation is bounded",()=>{
  for(const name of ["a".repeat(129),"a".repeat(255),"配信".repeat(127)+"枠","📷".repeat(255),"https://example.invalid","user@example.invalid"]){
    const stream={id:"stream-one",name,status:"created",created_at:"2030-01-01T00:00:00Z"} as Stream;
    for(const id of ["STR-01","STR-02","STR-03"] as const){
      const payload=id==="STR-03"?{encoder_audio_gain_db:3.5,overlay_profile_id:""}:{name};
      const intent:StreamActionIntent={id,...(id==="STR-01"?{}:{stream:{...stream,status:id==="STR-03"?"live":"created"}}),payload};
      const descriptor=buildStreamActionDescriptor(intent);
      assert.ok(descriptor,`${id} must accept a valid full name`);
      assert.equal(streamActionApplicable(intent),true);
      assert.ok(descriptor.target.publicLabel, "a valid stream action must have a nonempty public presentation");
      assert.ok(descriptor.target.publicLabel.length<=128);
      assert.equal(Buffer.from(descriptor.target.publicLabel,"utf8").toString("utf8"),descriptor.target.publicLabel,"presentation must not split a surrogate pair");
      assert.equal(streamActionRequest(intent)?.body,payload,"request body must retain the original object and full persisted name");
      assert.equal(stream.name,name);
    }
    assert.equal(buildStreamActionDescriptor({id:"STR-10",stream}),undefined,"high-risk public-label guard must remain unchanged");
  }
  for(const name of ["a".repeat(256),"bad\u0000name","bad\u202ename"]){
    assert.equal(streamSettingsPresentationLabel(name),"");
    assert.equal(buildStreamActionDescriptor({id:"STR-01",payload:{name}}),undefined);
  }
  assert.equal(streamSettingsPresentationLabel("https://example.invalid"),"Stream");
  assert.equal(streamSettingsPresentationLabel("user@example.invalid"),"Stream");
});
