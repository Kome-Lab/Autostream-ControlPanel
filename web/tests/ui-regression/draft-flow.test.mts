import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";
import ts from "typescript";
import { createDraftExitController, createDraftNavigationGuard, type DraftExitController } from "../../src/lib/ui-v2/draft-exit-controller.ts";
import { notifyDraftSessionExit, subscribeDraftSessionExit } from "../../src/lib/ui-v2/draft-navigation-lifecycle.ts";
import { actualCallback, actualEffect } from "./source-callback.mts";

const source = (path: string) => readFileSync(new URL("../../src/" + path, import.meta.url), "utf8");
const forms = source("components/forms/draft-exit.tsx"), slot = source("features/streams/stream-slot-form.tsx"), visualSource = source("features/streams/stream-visual-settings-section.tsx"), streams = source("features/streams/streams-view.tsx");
function currentFunction(text: string, name: string, bindings: Record<string, unknown>) {
  const file = ts.createSourceFile("actual.tsx", text, ts.ScriptTarget.Latest, true, ts.ScriptKind.TSX);
  const declaration = file.statements.find((node): node is ts.FunctionDeclaration => ts.isFunctionDeclaration(node) && node.name?.text === name);
  assert.ok(declaration); return actualCallback("const selected=" + declaration.getText(file).replace(/^export /, ""), "selected", bindings);
}
function owner() {
  const state = { dirty: true, pending: false, discard: false, prompts: 0 };
  const controller = createDraftExitController({ enabled: () => true, pending: () => state.pending, confirmDiscard: () => { state.prompts++; return state.discard; } });
  return { state, controller };
}
function unload(surface: EventTarget) {
  const event = new Event("beforeunload", { cancelable: true }); Object.defineProperty(event, "returnValue", { value: "original", writable: true });
  surface.dispatchEvent(event); return event;
}
function mountedGuard(navigationAPI: boolean) {
  const state = { discard: false, prompts: 0 }, surface = Object.assign(new EventTarget(), { navigation: navigationAPI ? new EventTarget() : undefined, location: { origin: "https://panel.example" }, confirm: () => { state.prompts++; return state.discard; } });
  const document = new EventTarget(), cleanups: Array<() => void> = [];
  const effect = (callback: () => unknown) => { const cleanup = callback(); if (typeof cleanup === "function") cleanups.push(() => Reflect.apply(cleanup, undefined, [])); };
  const hook = currentFunction(forms, "useDraftExit", { useI18n: () => ({ locale: "en" }), useState: (initialize: () => unknown) => [initialize()], useLayoutEffect: effect, useEffect: effect, createDraftExitController, createDraftNavigationGuard, subscribeDraftSessionExit, window: surface, document });
  const controller: DraftExitController = hook({ enabled: true, pending: false });
  return { controller, surface, state, document, dispose: () => { for (const cleanup of cleanups.reverse()) cleanup(); } };
}
test("UI-DRAFT-FLOW-001: actual hook requests unload with no navigation, including unsupported Navigation API", () => {
  for (const api of [true, false]) {
    const mounted = mountedGuard(api); let dirty = true;
    const unregister = mounted.controller.register({ isDirty: () => dirty, saved() {} });
    try {
      assert.equal(unload(mounted.surface).defaultPrevented, true);
      const navigate = new Event("navigate", { cancelable: true }); Object.defineProperty(navigate, "destination", { value: { sameDocument: false, url: "https://other.example" } });
      mounted.surface.navigation?.dispatchEvent(navigate);
      assert.equal(navigate.defaultPrevented, false); assert.equal(unload(mounted.surface).defaultPrevented, true); assert.equal(mounted.state.prompts, 0, "cross-document must not add a custom prompt");
      dirty = false; assert.equal(unload(mounted.surface).defaultPrevented, false);
      dirty = true; unregister(); assert.equal(unload(mounted.surface).defaultPrevented, false);
    } finally { mounted.dispose(); }
    assert.equal(unload(mounted.surface).defaultPrevented, false, "unmount removes the actual listener");
  }
});
test("UI-DRAFT-FLOW-002: same-document stay, pending, ordinary login and one anchor approval retain dirty", async () => {
  const { controller, state } = owner(); controller.register({ isDirty: () => state.dirty, saved() {} });
  const guard = createDraftNavigationGuard(controller); let cancelled = 0, prevented = 0;
  const event = { destination: { url: "https://panel.example/login", sameDocument: true }, cancelable: true, defaultPrevented: false, preventDefault() { cancelled++; } };
  guard.navigate(event); assert.equal(cancelled, 1); assert.equal(state.prompts, 1);
  state.pending = true; guard.navigate(event); assert.equal(cancelled, 2); assert.equal(state.prompts, 1);
  state.pending = false; state.discard = true; guard.navigate({ ...event, source: "anchor" });
  const before = { returnValue: "original", preventDefault() { prevented++; } }; guard.beforeUnload(before);
  assert.equal(prevented, 0); assert.equal(state.prompts, 2); assert.equal(controller.dirty(), true);
  await Promise.resolve(); guard.beforeUnload(before); assert.equal(prevented, 1, "anchor approval must not persist into a later exit");
});
test("UI-DRAFT-FLOW-003: explicit ending signal expires, cancels, disposes and never reaches a new subscriber", t => {
  t.mock.timers.enable({ apis: ["setTimeout"] });
  const mounted = mountedGuard(false), second = mountedGuard(true); let reads = 0;
  for (const item of [mounted, second]) item.controller.register({ isDirty: () => { reads++; return true; }, saved() {} });
  try {
    notifyDraftSessionExit(); assert.equal(unload(mounted.surface).defaultPrevented, false); assert.equal(unload(second.surface).defaultPrevented, false); assert.equal(reads, 0);
    const fresh = mountedGuard(false); try { fresh.controller.register({ isDirty: () => true, saved() {} }); assert.equal(unload(fresh.surface).defaultPrevented, true); } finally { fresh.dispose(); }
    for (const name of ["pointerdown", "keydown", "click", "pageshow", "error", "unhandledrejection"]) { notifyDraftSessionExit(); mounted.surface.dispatchEvent(new Event(name)); assert.equal(unload(mounted.surface).defaultPrevented, true, name); }
    for (const name of ["navigateerror", "navigatesuccess"]) { notifyDraftSessionExit(); second.surface.navigation?.dispatchEvent(new Event(name)); assert.equal(unload(second.surface).defaultPrevented, true, name); }
    notifyDraftSessionExit(); t.mock.timers.tick(5_000); assert.equal(unload(mounted.surface).defaultPrevented, true);
    const mutant = createDraftNavigationGuard(mounted.controller, () => true); let prevented = 0; mutant.beforeUnload({ returnValue: "", preventDefault() { prevented++; } });
    assert.throws(() => assert.equal(prevented, 1), /0 !== 1/, "always-bypass mutant must fail ordinary unload");
  } finally { mounted.dispose(); second.dispose(); }
});
function hookReader(controller: DraftExitController) {
  const cells: unknown[] = []; let cursor = 0;
  const cell = <T,>(initialize: () => T): T => { const index = cursor++; if (!(index in cells)) cells[index] = initialize(); return cells[index] as T; };
  const hook = currentFunction(forms, "useNonSecretDraft", { DraftExitContext: null, useContext: () => controller, useRef: <T,>(value: T) => cell(() => ({ current: value })), useState: (initialize: () => unknown) => [cell(initialize)], useLayoutEffect: (callback: () => unknown) => callback(), useMemo: (callback: () => unknown) => callback() });
  return (values: string[]) => { cursor = 0; return hook(values); };
}
function jsxAt(text: string, tag: string, prop: string, index: number, bindings: Record<string, unknown>) {
  const file = ts.createSourceFile("actual.tsx", text, ts.ScriptTarget.Latest, true, ts.ScriptKind.TSX), expressions: string[] = [];
  const visit = (node: ts.Node) => {
    if ((ts.isJsxSelfClosingElement(node) || ts.isJsxOpeningElement(node)) && node.tagName.getText(file) === tag) for (const attribute of node.attributes.properties) if (ts.isJsxAttribute(attribute) && attribute.name.getText(file) === prop && attribute.initializer && ts.isJsxExpression(attribute.initializer) && attribute.initializer.expression) expressions.push(attribute.initializer.expression.getText(file));
    ts.forEachChild(node, visit);
  };
  visit(file); assert.ok(expressions[index]); return actualCallback("const selected=" + expressions[index], "selected", bindings);
}
function createFlow(editing = false, slotSource = slot) {
  const { controller, state } = owner(), renderBasic = hookReader(controller); renderBasic([""]); const basic = renderBasic(["Submitted name"]);
  let revision = 0, dirtySections: ReadonlySet<string> = new Set(), draft = { headerTitleValue: "" }, closes = 0, focus = 0;
  const currentCreateRevision = { current: 0 }, savedCreateRevision = { current: 0 };
  const setDirtySections = (next: ReadonlySet<string> | ((current: ReadonlySet<string>) => ReadonlySet<string>)) => { dirtySections = typeof next === "function" ? next(dirtySections) : next; };
  const update = actualCallback(visualSource, "update", { editing, currentCreateRevision, setCreateRevision: (next: number) => { revision = next; }, setDirtySections, setDraft: (next: (current: typeof draft) => typeof draft) => { draft = next(draft); } });
  controller.register({ isDirty: () => editing ? dirtySections.size > 0 : currentCreateRevision.current !== savedCreateRevision.current, saved() {} });
  const visualState = () => actualCallback(visualSource, "createState", { useMemo: (callback: () => unknown) => callback(), editing, draft, validation: { ready: true }, createRevision: revision, currentCreateRevision, savedCreateRevision, setDirtySections, buildStreamCreateVisualExtension: (value: typeof draft) => ({ visual_settings: { header_title_value: value.headerTitleValue } }) });
  const saveAcknowledgements = { current: new WeakMap<object, () => boolean>() };
  const bindings = { setMessage() {}, onActionResult() {}, liveEditing: false, editing, uiText: (key: string) => key, t: (key: string) => key, streamActionBlockedMessage: () => "blocked", isStreamValue: (value: unknown) => Boolean(value && typeof value === "object" && "id" in value), saveAcknowledgements, draft: basic };
  const focusCallback = jsxAt(streams, "SheetContent", "onCloseAutoFocus", editing ? 1 : 0, { createTrigger: { current: { focus() { focus++; } } }, editTrigger: { current: { focus() { focus++; } } } });
  const parent = jsxAt(streams, "StreamSlotForm", "onSaved", editing ? 1 : 0, { ...bindings, createDraftExit: controller, editDraftExit: controller, setCreatedStreams() {}, setActionNotice() {}, setCreateOpen() { closes++; focusCallback({ preventDefault() {} }); }, setEditingStream() { closes++; focusCallback({ preventDefault() {} }); } });
  const result = actualCallback(slotSource, "handleSaveResult", { ...bindings, onSaved: parent });
  const prepare = () => {
    const createVisualState = visualState(), actionIntent = { id: editing ? "STR-02" : "STR-01", payload: { name: "Submitted name", ...(!editing ? createVisualState.extension : {}) } };
    const effect = slotSource.replace("useLayoutEffect(() => {\n    let acknowledged", "useEffect(() => {\n    let acknowledged");
    actualEffect(effect, "saveAcknowledgements.current.set", { actionIntent, createVisualState, draft: basic, saveAcknowledgements })();
    return actionIntent;
  };
  return { controller, state, update, prepare, result, renderBasic, setDirtySections, get dirtySections() { return dirtySections; }, get closes() { return closes; }, get focus() { return focus; } };
}
test("UI-DRAFT-FLOW-004: actual form/visual/parent callbacks ack only successful submitted create once and return focus", () => {
  for (const withVisual of [false, true]) {
    const flow = createFlow(); if (withVisual) flow.update("title", { headerTitleValue: "Submitted title" }); const intent = flow.prepare();
    flow.result({ kind: "succeeded", value: { id: "created", name: "Submitted name" } }, intent);
    assert.equal(flow.controller.dirty(), false); assert.equal(flow.state.prompts, 0); assert.equal(flow.closes, 1); assert.equal(flow.focus, 1);
    flow.result({ kind: "succeeded", value: { id: "created", name: "Submitted name" } }, intent); assert.equal(flow.closes, 1);
  }
});
test("UI-DRAFT-FLOW-005: failure 409 unknown validation and mismatched intent cannot acknowledge or close", () => {
  for (const outcome of [{ kind: "failed", error: { kind: "conflict", status: 409, messageKey: "conflict" } }, { kind: "outcome_unknown" }, { kind: "blocked", reason: "invalid-intent" }, { kind: "succeeded", value: null }]) {
    const flow = createFlow(); flow.update("title", { headerTitleValue: "Unsent" }); flow.result(outcome, flow.prepare());
    assert.equal(flow.controller.dirty(), true); assert.equal(flow.closes, 0); assert.equal(flow.dirtySections.size, 1);
  }
  const flow = createFlow(); const intent = flow.prepare(); flow.result({ kind: "succeeded", value: { id: "other" } }, { ...intent }); assert.equal(flow.controller.dirty(), true); assert.equal(flow.closes, 0);
});
test("UI-DRAFT-FLOW-006: real action control delayed success keeps later basic and visual edits dirty", async () => {
  for (const changed of ["basic", "visual"]) {
    const flow = createFlow(); flow.update("title", { headerTitleValue: "Submitted" }); const intent = flow.prepare();
    let complete!: (value: unknown) => void; const pending = new Promise(resolve => { complete = resolve; });
    const submit = actualCallback(source("features/streams/stream-action-control.tsx"), "submit", { setDialogState() {}, setOpened() {}, controller: { submit: async (allowed: { intent: object }) => { assert.equal(allowed.intent, intent); return pending; } }, onResult: flow.result });
    const work = submit({ intent, descriptor: { retry: { kind: "never" } } });
    if (changed === "basic") flow.renderBasic(["New edit after request"]); else flow.update("title", { headerTitleValue: "New edit after request" });
    complete({ kind: "succeeded", value: { id: "created", name: "Submitted" } }); await work;
    assert.equal(flow.controller.dirty(), true); assert.equal(flow.state.prompts, 1); assert.equal(flow.closes, 0);
  }
});
test("UI-DRAFT-FLOW-007: basic edit leaves visual dirty; only existing independent visual success clears it", async () => {
  const flow = createFlow(true); flow.update("title", { headerTitleValue: "Independent" }); flow.result({ kind: "succeeded", value: { id: "edited", name: "Edited" } }, flow.prepare());
  assert.equal(flow.controller.dirty(), true); assert.equal(flow.closes, 0); assert.equal(flow.state.prompts, 1);
  const save = actualCallback(visualSource, "save", { visual: { data: {} }, validation: { ready: true }, saving: false, needsRefresh: false, draft: {}, dirtySections: flow.dirtySections, setSaving() {}, setMessage() {}, controller: { issue: async () => ({ kind: "succeeded", value: {} }) }, buildStreamVisualFields: () => ({}), queryClient: { setQueryData() {} }, queryKey: [], setDraft() {}, streamVisualDraftFromSettings: () => ({}), setDirtySections: flow.setDirtySections, setUploadedBackground() {}, setUploadedCover() {}, locale: "en" });
  await save(); assert.equal(flow.controller.dirty(), false);
});
class SessionError extends Error { status = 401; code = "unauthorized"; }
async function sessionCase(kind: string, removeNotice = false) {
  const mounted = mountedGuard(false), sequence: string[] = []; mounted.controller.register({ isDirty: () => true, saved() {} });
  let blocked = 0, requests = 0;
  const redirect = () => { sequence.push("redirect"); if (unload(mounted.surface).defaultPrevented) blocked++; };
  const effects: Array<() => unknown> = [], seen = { current: kind === "current401" }, error = new SessionError();
  const text = source("components/shell/use-shell-session-guard.ts").replaceAll(removeNotice ? "notifyDraftSessionExit();" : "NO_MUTATION", "void 0;");
  const hook = currentFunction(text, "useShellSessionGuard", { useRouter: () => ({ replace: redirect }), useRef: () => seen, useEffect: (callback: () => unknown) => effects.push(callback), APIError: SessionError, notifyDraftSessionExit: () => { sequence.push("notify"); notifyDraftSessionExit(); }, clearCSRFToken: () => sequence.push("clear"), loginPathForLocation: () => "/login", window: Object.assign(mounted.surface, { location: { replace: redirect } }), document: Object.assign(mounted.document, { visibilityState: "visible" }), apiPost: async () => { requests++; throw error; }, apiGet: async () => { requests++; if (kind === "setup-failure") throw Error("setup failure"); return { setup_required: kind === "setup-required" }; } });
  hook({ data: kind === "refresh401" ? {} : undefined, error, isError: kind !== "refresh401" });
  const cleanups = effects.map(callback => callback());
  if (kind === "refresh401") { mounted.surface.dispatchEvent(new Event("focus")); mounted.surface.dispatchEvent(new Event("focus")); }
  await new Promise<void>(resolve => setImmediate(resolve));
  for (const cleanup of cleanups) if (typeof cleanup === "function") Reflect.apply(cleanup, undefined, []);
  mounted.dispose(); return { sequence, blocked, requests };
}
test("UI-DRAFT-FLOW-008: actual 401/setup callers notify only after the existing decision, missing notifications fail", async () => {
  for (const kind of ["refresh401", "current401", "setup-required", "setup-login", "setup-failure"]) {
    const normal = await sessionCase(kind); assert.equal(normal.blocked, 0, kind); assert.equal(normal.sequence.at(-2), "notify"); assert.equal(normal.sequence.at(-1), "redirect"); assert.equal(normal.requests, kind === "current401" ? 0 : 1);
    const mutant = await sessionCase(kind, true); assert.equal(mutant.blocked, 1, kind + " missing notice must be caught");
  }
});
test("UI-DRAFT-FLOW-009: actual logout settled callback clears CSRF then emits document notice before redirect", () => {
  const mounted = mountedGuard(false); mounted.controller.register({ isDirty: () => true, saved() {} }); const sequence: string[] = [];
  try {
    const options = actualCallback(source("components/shell/app-shell.tsx"), "logout", { useMutation: (value: unknown) => value, apiPost() {}, clearCSRFToken() { sequence.push("clear"); }, globalThis: { dispatchEvent(event: Event) { sequence.push("notice"); return mounted.surface.dispatchEvent(event); } }, Event, router: { replace(path: string) { assert.equal(path, "/login"); sequence.push("redirect"); assert.equal(unload(mounted.surface).defaultPrevented, false); } } });
    options.onSettled(); assert.deepEqual(sequence, ["clear", "notice", "redirect"]); assert.equal(options.retry, false);
  } finally { mounted.dispose(); }
});

test("UI-DRAFT-FLOW-010: real missing-visual-ack and current-instead-of-submitted mutants are detected", () => {
  const missing = slot.replace("createVisualState.acknowledgeSubmitted?.();", "void 0;"); assert.notEqual(missing, slot);
  const flow = createFlow(false, missing); flow.update("title", { headerTitleValue: "Submitted" });
  flow.result({ kind: "succeeded", value: { id: "created", name: "Created" } }, flow.prepare());
  assert.equal(flow.controller.dirty(), true); assert.equal(flow.closes, 0);
  assert.throws(() => assert.equal(flow.closes, 1), /0 !== 1/, "the 003 missing visual acknowledgement must fail the success-close contract");
  const wrongSnapshot = slot.replace("draft.acknowledgeSubmitted();", "draft.saved();"); assert.notEqual(wrongSnapshot, slot);
  const delayed = createFlow(false, wrongSnapshot), intent = delayed.prepare(); delayed.renderBasic(["Later edit"]);
  delayed.result({ kind: "succeeded", value: { id: "created", name: "Created" } }, intent);
  assert.throws(() => assert.equal(delayed.controller.dirty(), true), /false !== true/, "current data cannot be marked saved by an older request");
});
