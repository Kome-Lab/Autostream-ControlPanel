import "./component-loader.mts";
import assert from "node:assert/strict";
import test from "node:test";
import { createElement, type ComponentType } from "react";
import { renderUI } from "./render-ui.mts";
import { readFileSync } from "node:fs";
import { renderedSource } from "./source-render.mts";
const { createUICopy } = await import("../../src/lib/i18n/ui-v2/copy.ts");
const { fixedPresentationText, oauthAccountName, serviceAssignmentPresentation } = await import("../../src/lib/i18n/ui-v2/presentation-copy.ts");
const { formatResourceCell } = await import("../../src/features/resources/resource-presentation.tsx");
const { resourcePages } = await import("../../src/features/resources/resource-config.ts");
const { recordingDescriptor } = await import("../../src/lib/stream-presentation.ts");
const { StreamSummary } = await import("../../src/features/streams/stream-summary.tsx");
const { ResourcePage } = await import("../../src/features/resources/resource-page.tsx");
const { StreamDetailOperations } = await import("../../src/features/streams/stream-detail-operations.tsx");
const { createStreamActionController } = await import("../../src/features/streams/stream-action-controller.ts");

test("UI-MONITORING-COPY-034: actual legacy badges translate labels and detail without changing classification or unknown safety", async () => {
  const { StatusBadge, statusDescriptor } = await import("../../src/components/admin/status-badge.tsx");
  const statuses = ["live", "starting", "scheduled", "ready", "created", "draft", "stopped", "completed", "failed", "error", "recording", "recording_started", "recording_completed", "online", "assigned", "pass", "ok", "healthy", "resolved", "closed", "acknowledged", "open", "offline", "unconfigured", "stopping", "degraded", "warning", "pending", "success", "executed", "retrying", "pending_approval", "failure", "UNRECOGNIZED-秘密値"];
  for (const status of statuses) {
    const before = structuredClone(statusDescriptor(status));
    const ja = renderUI(createElement(StatusBadge, { status, showDetail: true }), "ja");
    const en = renderUI(createElement(StatusBadge, { status, showDetail: true }), "en");
    const text = en.replace(/<[^>]*>/g, "");
    assert.doesNotMatch(text, /[\u3040-\u30ff\u3400-\u9fff]/, status);
    assert.ok(ja.includes(before.label) && ja.includes(before.detail));
    assert.deepEqual(statusDescriptor(status), before);
    assert.ok(en.includes(before.className), "status color meaning remains unchanged");
    if (status.startsWith("UNRECOGNIZED")) { assert.match(en, /Unknown state/); assert.doesNotMatch(en, /秘密値|UNRECOGNIZED/); }
  }
  const url = new URL("../../src/components/admin/status-badge.tsx", import.meta.url), source = readFileSync(url, "utf8");
  const mutant = await renderedSource(source.replace('uiText = useUICopy()', 'uiText = (key: string) => key'), url);
  assert.match(renderUI(createElement(mutant.StatusBadge, { status: "healthy", showDetail: true }), "en"), /Nodeの監視は正常/);
});

test("UI-OBSERVABILITY-COPY-034: real resource actions translate only the trigger and preserve fixed authority plans", async () => {
  const { ResourcePage } = await import("../../src/features/resources/resource-page.tsx");
  const { observabilityActionPlans } = await import("../../src/features/observability/action-policy.ts");
  const plans = observabilityActionPlans("/observability/incidents", { id: "incident", title: "固有名", status: "open" });
  const before = structuredClone(plans);
  const en = renderUI(createElement(ResourcePage, { pageId: "incidents" }), "en");
  assert.match(en, />Acknowledge</); assert.match(en, />Resolve</);
  assert.doesNotMatch(en, />確認済みにする<|>解決済みにする</);
  const ja = renderUI(createElement(ResourcePage, { pageId: "incidents" }), "ja");
  assert.match(ja, />確認済みにする</); assert.match(ja, />解決済みにする</);
  assert.deepEqual(plans, before, "no locale re-creation of action authority");
  const { ObservabilityActionControl } = await import("../../src/features/observability/observability-action-control.tsx");
  const allPlans = [...plans,
    ...observabilityActionPlans("/observability/diagnostics", { id: "diagnostic", incident_id: "incident" }),
    ...observabilityActionPlans("/observability/remediation-actions", { id: "remediation", action: "restart", mode: "manual", status: "pending_approval" }),
    ...observabilityActionPlans("/observability/remediation-actions", { id: "remediation", action: "restart_worker", mode: "manual", status: "approved" }),
  ];
  assert.deepEqual(allPlans.map(plan => plan.id), ["OBS-01", "OBS-02", "OBS-03", "OBS-04", "OBS-05"]);
  for (const plan of allPlans) {
    const original = structuredClone(plan);
    for (const allowed of [true, false]) {
      const control = createElement(ObservabilityActionControl, { plan, allowed, controller: {
        execute: async () => { throw Error("render must not mutate"); }, reconcile() { throw Error("render must not reconcile"); },
      }, onResult() { throw Error("render must not report a mutation"); } });
      assert.doesNotMatch(renderUI(control, "en").replace(/<[^>]*>/g, ""), /[\u3040-\u30ff\u3400-\u9fff]/);
      assert.ok(renderUI(control, "ja").includes(plan.label));
      assert.deepEqual(plan, original);
    }
  }
});
const controller=createStreamActionController({getPermissions:()=>({kind:"ready",permissions:["*"]}),getState:()=>({kind:"ready",freshness:"fresh",fingerprint:"current"}),mutate:async()=>{throw Error("render cannot mutate");}});
const callbacks={actionController:controller,onActionResult(){},onSaved(){},canCreate:true,canUpdate:true,canAssignEncoder:true,canAssignWorker:true};
const specs=[
 ["dashboard","dashboard/dashboard-view.tsx","DashboardView",{}],
 ["streams-list","streams/streams-view.tsx","StreamsView",{}],
 ["stream-create-edit","streams/stream-slot-form.tsx","StreamSlotForm",callbacks],
 ["workers","workers/workers-view.tsx","WorkersView",{}],
 ["archive","archive/archive-view.tsx","ArchiveView",{}],
 ["monitoring","monitoring/monitoring-view.tsx","MonitoringView",{}],
 ["metrics","metrics/metrics-view.tsx","MetricsView",{}],
 ["audit-logs","audit/audit-logs-view.tsx","AuditLogsView",{}],
 ["nodes","nodes/node-registration-view.tsx","NodeRegistrationView",{}],
 ["system-updates","application/application-info-view.tsx","ApplicationInfoView",{}],
 ["account","account/account-view.tsx","AccountView",{}],
 ["public-archive-share","archive/archive-share-player-view.tsx","ArchiveSharePlayerView",{token:"ui-synthetic-share"}],
] as const;
const resourceFamilies: Record<string,keyof typeof resourcePages>={"service-health":"service-health","encoder-profiles":"encoder","discord":"discord","youtube":"youtube","captions":"caption","watermark":"overlay","incidents":"incidents","diagnostics":"diagnostics","remediation":"remediation","notifications":"notifications","integrations":"integrations","users":"users","roles":"roles","security-settings":"security"};
for (const [family,module,symbol,props] of specs) {
  const imported=await import("../../src/features/"+module);
  test("UI-COPY-"+family+": actual body renders in both locales",()=>{
    const ja=renderUI(createElement(imported[symbol] as ComponentType<typeof props>,props),"ja");
    const en=renderUI(createElement(imported[symbol] as ComponentType<typeof props>,props),"en");
    assert.ok(ja.length>300&&en.length>300);
    assert.notEqual(ja,en,"changing locale must reach actual components");
    if(family === "public-archive-share") { assert.match(en, /<video[^>]*controls/); assert.match(en, /Open directly/); assert.match(en, />Kind</); }
    else assert.ok((en.match(/<label\b|aria-label=|role="status"|<th\b/g)||[]).length>0,"real controls, table or state copy is present");
    // Stable UI copy only; Japanese user names and arbitrary IDs remain legitimate.
    for(const fixed of ["設定を変更","配信枠名","録画状態","通知する重要度","最小パスワード長","アイドルタイムアウト (分)","プライバシー","MFAを要求するロール"]) {
      if(ja.includes(">"+fixed+"<"))assert.ok(!en.includes(">"+fixed+"<"),"untranslated owned UI copy: "+fixed);
    }
  });
}
for(const [family,pageId] of Object.entries(resourceFamilies))test("UI-COPY-"+family+": actual resource body and columns use locale",()=>{
  const ja=renderUI(createElement(ResourcePage,{pageId}),"ja"),en=renderUI(createElement(ResourcePage,{pageId}),"en");
  assert.match(en,/aria-label="Breadcrumbs"/);assert.match(ja,/aria-label="パンくず"/);
  assert.notEqual(ja,en);assert.ok(en.includes("Refresh"),"body action must use English");
  assert.ok(!en.includes(">名前<"),"resource table header must use current copy");
  assert.ok(!en.includes(">設定を変更<"),"settings body must use current copy");
});
test("UI-COPY-stream-detail: actual Readiness surface, state and accessible name use locale",()=>{
  for(const locale of ["ja","en"] as const) {
    const html=renderUI(createElement(StreamDetailOperations,{stream:{id:"s",name:"日本語の利用者名",status:"ready"},controller,onResult(){},notice:null}),locale);
    assert.match(html,locale==="ja"?/配信の主要操作/:/Stream actions/);
    assert.match(html,locale==="ja"?/Readinessは未確認/:/Readiness has not been checked/);
  }
});
test("UI-COPY-login: actual login form labels and help use locale",async()=>{
  const { LoginCard }=await import("../../src/components/auth/auth-card.tsx");
  const ja=renderUI(createElement(LoginCard),"ja","/login/"),en=renderUI(createElement(LoginCard),"en","/login/");
  assert.notEqual(ja,en);assert.match(en,/Password|password/);assert.match(en,/Username|username/);
});
test("UI-COPY-DATA: locale does not translate user names, resource IDs or action payload labels",()=>{
  const resource=resourcePages.users.resources[0];
  for(const name of ["active","名前","配信を開始","日本語の利用者名"])for(const locale of ["ja","en"] as const) {
    assert.equal(formatResourceCell(resource,name, "name", undefined,createUICopy(locale)),name);
    assert.equal(formatResourceCell(resource,name, "id", undefined,createUICopy(locale)),name);
    assert.equal(oauthAccountName({account_label:name,id:"account",provider_type:"google"},createUICopy(locale)),name);
  }
});
test("UI-COPY-POLICY: recording count and busy assignment remain locale-independent",()=>{
  const stream={id:"s",name:"名前",status:"live",archive_profile_id:"recording"};
  for(const locale of ["ja","en"] as const) {
    const html=renderUI(createElement(StreamSummary,{rows:[stream]}),locale);
    assert.match(html,locale==="ja"?/>録画中<.*?>1</:/>Recording<.*?>1</);
    const original=recordingDescriptor(stream);
    assert.equal(original.label,"録画中");
    const option=serviceAssignmentPresentation({value:"worker",label:"名前",currentStreamID:"other"},"s",createUICopy(locale));
    assert.equal(option.disabled,true);assert.equal(option.value,"worker");assert.ok(option.label.includes("名前"));
    assert.equal(fixedPresentationText("user-unrecognized-value",createUICopy(locale)),"user-unrecognized-value");
  }
});

test("UI-COPY-NOTIFICATION: actual safe formatter retains result policy and never feeds raw errors or targets to copy",async()=>{
  const {notificationFeedback}=await import("../../src/lib/i18n/ui-v2/presentation-copy.ts");
  const response=[{status:"failed",target:"RAW_TARGET_SENTINEL",error:"RAW_ERROR_SENTINEL"}];
  const keys:string[]=[];const uiText=createUICopy("en");
  const output=notificationFeedback(response,(key,...values)=>{keys.push(key,...values.map(String));return uiText(key,...values);});
  assert.equal(output.ok,false);assert.doesNotMatch(output.message,/RAW_TARGET_SENTINEL|RAW_ERROR_SENTINEL/);
  assert.doesNotMatch(keys.join(" "),/RAW_TARGET_SENTINEL|RAW_ERROR_SENTINEL/);
  assert.match(output.message,/Test notification|Test delivery|Test send/);
});
