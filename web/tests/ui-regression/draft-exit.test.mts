import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";
import { createDraftExitController, createDraftNavigationGuard } from "../../src/lib/ui-v2/draft-exit-controller.ts";
import { actualCallback, actualJSXCallback } from "./source-callback.mts";

function setup() {
  const state = { dirty: false, pending: false, enabled: true, discard: false, prompts: 0, closes: 0 };
  const controller = createDraftExitController({ enabled: () => state.enabled, pending: () => state.pending,
    confirmDiscard: () => { state.prompts++; return state.discard; } });
  controller.register({ isDirty: () => state.dirty, saved: () => { state.dirty = false; } });
  return { state, controller, leave: () => { state.closes++; } };
}

test("UI-DRAFT-001: clean, stay, discard and reentrant close use the same owner once", () => {
  const { state, controller, leave } = setup();
  assert.equal(controller.request(leave), true);
  state.dirty = true;
  assert.equal(controller.request(leave), false);
  assert.equal(state.closes, 1);
  assert.equal(state.dirty, true);
  state.discard = true;
  controller.request(() => { leave(); assert.equal(controller.request(leave), false); });
  assert.equal(state.closes, 2);
  assert.equal(state.prompts, 2);
});
test("UI-DRAFT-002: pending and failed or ambiguous saves retain dirty until confirmed success", () => {
  const { state, controller, leave } = setup();
  state.dirty = state.pending = state.discard = true;
  assert.equal(controller.request(leave), false);
  assert.equal(state.prompts, 0);
  state.pending = false;
  assert.equal(controller.dirty(), true);
  controller.saved();
  assert.equal(controller.dirty(), false);
  controller.request(leave);
  assert.equal(state.prompts, 0);
});
test("UI-DRAFT-008: actual Settings result callback clears only a confirmed APP-01 save", () => {
  const { state, controller } = setup(); state.dirty = true;
  const bindings = { setDispatching() {}, draft: controller, setMessage() {}, setPending() {},
    uiText: (key: string) => key, queryClient: { invalidateQueries() {} }, t: (key: string) => key, appSettingsActionResultMessage: () => "Safe failure" };
  const current = source("features/settings/settings-view.tsx");
  const callback = actualCallback(current, "handleActionResult", bindings);
  for (const result of [{ kind: "blocked", reason: "permission-denied" }, { kind: "failed", error: { kind: "conflict" } },
    { kind: "failed", error: { kind: "forbidden" } }, { kind: "outcome_unknown" }]) {
    callback(result, { id: "APP-01" }); assert.equal(controller.dirty(), true);
  }
  callback({ kind: "succeeded" }, { id: "APP-02" }); assert.equal(controller.dirty(), true);
  callback({ kind: "succeeded" }, { id: "APP-01" }); assert.equal(controller.dirty(), false);
  const mutant = current.replace("draft.saved();", "void 0;");
  state.dirty = true;
  actualCallback(mutant, "handleActionResult", bindings)({ kind: "succeeded" }, { id: "APP-01" });
  assert.throws(() => assert.equal(controller.dirty(), false), /true !== false/);
});
test("UI-DRAFT-003: forced logout and permission loss bypass dirty and pending without reading draft", () => {
  const { state, controller, leave } = setup();
  state.dirty = state.pending = true;
  controller.register({ isDirty: () => { throw new Error("must not collect a draft on forced exit"); }, saved() {} });
  assert.equal(controller.request(leave, true), true);
  state.enabled = false;
  assert.equal(controller.request(leave), true);
  assert.equal(state.prompts, 0);
});
test("UI-DRAFT-010: callback updates preserve registered draft and use current pending and permission", () => {
  const { state, controller, leave } = setup(); state.dirty = true;
  controller.updateOptions({ enabled: () => true, pending: () => true, confirmDiscard: () => true });
  assert.equal(controller.dirty(), true);
  assert.equal(controller.request(leave), false);
  controller.updateOptions({ enabled: () => true, pending: () => false, confirmDiscard: () => false });
  assert.equal(controller.request(leave), false);
  assert.equal(state.closes, 0);
  controller.updateOptions({ enabled: () => false, pending: () => true, confirmDiscard: () => { throw new Error("forced exit must bypass confirm"); } });
  assert.equal(controller.request(leave), true);
  assert.equal(state.closes, 1);
});
test("UI-DRAFT-004: navigation/back/reload guard distinguishes native forced login and preserves cancellation", () => {
  const { state, controller } = setup(); state.dirty = true;
  let forced = false;
  const navigation = createDraftNavigationGuard(controller, () => forced);
  let cancelled = 0;
  const event = { destination: { url: "https://panel.example/admin/nodes/", sameDocument: true }, cancelable: true, defaultPrevented: false, preventDefault() { cancelled++; } };
  navigation.navigate(event);
  assert.equal(cancelled, 1);
  navigation.navigate({ ...event, cancelable: false });
  const unload = { returnValue: "original", preventDefault() { cancelled++; } };
  navigation.beforeUnload(unload);
  assert.equal(cancelled, 2);
  forced = true;
  navigation.navigate({ ...event, destination: { url: "https://panel.example/login?session_expired=1", sameDocument: true } });
  navigation.beforeUnload(unload);
  assert.equal(cancelled, 2);
  assert.equal(state.prompts, 1);
});
test("UI-DRAFT-005: unmount removes only that form; independently saved visual draft remains dirty", () => {
  const { controller } = setup(); let visual = true;
  const unregister = controller.register({ isDirty: () => visual, saved() {}, pending: () => false });
  controller.saved(); assert.equal(controller.dirty(), true);
  visual = false; assert.equal(controller.dirty(), false);
  visual = true; unregister(); assert.equal(controller.dirty(), false);
});
const source = (path: string) => readFileSync(new URL("../../src/" + path, import.meta.url), "utf8");
function assertCaller(source: string, expression: RegExp) { assert.match(source, expression, "actual form exit must reach its guard"); }
test("UI-DRAFT-006: actual form close callers stay connected; removing each guard is detected", () => {
  for (const [path, expression] of [
    ["features/streams/streams-view.tsx", /createDraftExit\.request\(/g],
    ["features/streams/streams-view.tsx", /editDraftExit\.request\(/g],
    ["features/resources/create-resource-form.tsx", /draftExit\.request\(/g],
    ["features/resources/edit-resource-button.tsx", /draftExit\.request\(/g],
    ["features/application/updater-settings-panel.tsx", /draftExit\.request\(/g],
    ["features/nodes/node-registration-view.tsx", /editDraftExit\.request\(/g],
    ["features/nodes/node-registration-view.tsx", /createDraftExit\.request\(/g],
  ] as const) {
    const current = source(path); assertCaller(current, expression);
    const mutant = current.replaceAll(expression, "unprotectedClose(");
    assert.notEqual(mutant, current); assert.throws(() => assertCaller(mutant, expression), /actual form exit/);
  }
});
test("UI-DRAFT-007: explicit non-secret comparison fields exclude credentials and raw form collection", () => {
  for (const path of ["components/forms/draft-exit.tsx", "features/streams/stream-slot-form.tsx", "features/settings/settings-view.tsx",
    "features/resources/resource-stream-forms.tsx", "features/resources/resource-caption-form.tsx", "features/resources/resource-access-forms.tsx", "features/resources/resource-oauth-forms.tsx", "features/resources/resource-notification-form.tsx"]) {
    const current = source(path);
    for (const match of current.matchAll(/useNonSecretDraft\(\[([\s\S]*?)\]/g)) assert.doesNotMatch(match[1], /smtpPassword|turnstileSecret|temporaryPassword|botToken|streamKey|apiKey|clientSecret|webhookURL/);
    assert.doesNotMatch(current, /new FormData\(/);
  }
  assert.doesNotMatch(source("components/forms/draft-exit.tsx"), /localStorage|sessionStorage|console\.|\.value\b/);
  assert.match(source("features/streams/stream-slot-form.tsx"), /result\.kind === "succeeded" && isStreamValue\(result\.value\)/);
  assert.match(source("features/settings/settings-view.tsx"), /intent\.id === "APP-01"\) \{\s*draft\.saved\(\)/);
});

test("UI-DRAFT-009: real Security result and resource-tab callbacks keep one draft and do not clear failures",()=>{
  let saved="before",tab="/security/settings",pending:unknown={},invalidations=0;
  const current=source("features/resources/resource-security-editor.tsx");
  const bindings={setMessage(){},resourceActionResultMessage:()=>"safe",t:(key:string)=>key,uiText:(key:string)=>key,
    setSavedSnapshot:(value:string)=>{saved=value;},draftSnapshot:"edited",setPending:(value:unknown)=>{pending=value;},resource:{path:"/security/settings"},queryClient:{invalidateQueries(){invalidations++;}}};
  const result=actualJSXCallback(current,"ResourceActionConfirmationHost","onResult",bindings);
  for(const kind of ["blocked","failed","outcome_unknown"]) {result({kind});assert.equal(saved,"before");}
  result({kind:"succeeded"});assert.equal(saved,"edited");assert.equal(pending,null);assert.equal(invalidations,2);
  saved="before";
  const mutant=current.replace("setSavedSnapshot(draftSnapshot);","void 0;");
  actualJSXCallback(mutant,"ResourceActionConfirmationHost","onResult",bindings)({kind:"succeeded"});
  assert.throws(()=>assert.equal(saved,"edited"),/before/);
  const {state,controller}=setup();state.dirty=true;
  const change=actualJSXCallback(source("features/resources/resource-page.tsx"),"Tabs","onValueChange",{draftExit:controller,setTab:(value:string)=>{tab=value;}});
  change("/secrets/status");assert.equal(tab,"/security/settings");
  state.discard=true;change("/secrets/status");assert.equal(tab,"/secrets/status");
  assert.match(current,/dirtyRef\.current\) return;/);
});
