import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";
import ts from "typescript";
import { createDraftExitController, createDraftNavigationGuard, type DraftExitController } from "../../src/lib/ui-v2/draft-exit-controller.ts";
import { notifyDraftSessionExit, subscribeDraftSessionExit } from "../../src/lib/ui-v2/draft-navigation-lifecycle.ts";
import { actualCallback, actualEffect } from "./source-callback.mts";
import { observerDOM, Element } from './observer-dom.mts';
import type { StreamCreateFocus } from '../../src/lib/ui-v2/stream-create-focus-handoff.ts';

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
function createFlow(editing = false, slotSource = slot, handoff: StreamCreateFocus | null = null, parentOwner?: {controller:DraftExitController;state:ReturnType<typeof owner>["state"];onSaved:(value:unknown)=>void}) {
  const { controller, state } = parentOwner || owner(), renderBasic = hookReader(controller); renderBasic([""]); const basic = renderBasic(["Submitted name"]);
  let revision = 0, dirtySections: ReadonlySet<string> = new Set(), draft = { headerTitleValue: "" }, closes = 0, focus = 0;
  const currentCreateRevision = { current: 0 }, savedCreateRevision = { current: 0 };
  const setDirtySections = (next: ReadonlySet<string> | ((current: ReadonlySet<string>) => ReadonlySet<string>)) => { dirtySections = typeof next === "function" ? next(dirtySections) : next; };
  const update = actualCallback(visualSource, "update", { editing, currentCreateRevision, setCreateRevision: (next: number) => { revision = next; }, setDirtySections, setDraft: (next: (current: typeof draft) => typeof draft) => { draft = next(draft); } });
  controller.register({ isDirty: () => editing ? dirtySections.size > 0 : currentCreateRevision.current !== savedCreateRevision.current, saved() {} });
  const visualState = () => actualCallback(visualSource, "createState", { useMemo: (callback: () => unknown) => callback(), editing, draft, validation: { ready: true }, createRevision: revision, currentCreateRevision, savedCreateRevision, setDirtySections, buildStreamCreateVisualExtension: (value: typeof draft) => ({ visual_settings: { header_title_value: value.headerTitleValue } }) });
  const saveAcknowledgements = { current: new WeakMap<object, () => boolean>() };
  const bindings = { setMessage() {}, onActionResult() {}, liveEditing: false, editing, uiText: (key: string) => key, t: (key: string) => key, streamActionBlockedMessage: () => "blocked", isStreamValue: (value: unknown) => Boolean(value && typeof value === "object" && "id" in value), saveAcknowledgements, draft: basic };
  const focusCallback = jsxAt(streams, "SheetContent", "onCloseAutoFocus", editing ? 1 : 0, { createFocus: { current: handoff }, createTrigger: { current: { focus() { focus++; } } }, editTrigger: { current: { focus() { focus++; } } } });
  const setCreateOpen=()=>{closes++;focusCallback({preventDefault(){}});};
  const requestCreateClose=actualCallback(streams,"requestCreateClose",{createDraftExit:controller,setCreateOpen,createGeneration:{current:0},createCloseIntent:{current:null},window:{location:{hash:""}}});
  const parent = parentOwner?.onSaved || jsxAt(streams, "StreamSlotForm", "onSaved", editing ? 1 : 0, { ...bindings, requestCreateClose, createDraftExit: controller, editDraftExit: controller, setCreatedStreams() {}, setActionNotice() {}, setCreateOpen, setEditingStream() { closes++; focusCallback({ preventDefault() {} }); } });
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

async function settleEvents(){await new Promise<void>(resolve=>setImmediate(resolve));}
function actualLayout(text:string,needle:string,bindings:Record<string,unknown>){
  const file=ts.createSourceFile('actual.tsx',text,ts.ScriptTarget.Latest,true,ts.ScriptKind.TSX),matches:ts.ArrowFunction[]=[];
  const visit=(node:ts.Node)=>{if(ts.isCallExpression(node)&&node.expression.getText(file)==='useLayoutEffect'&&ts.isArrowFunction(node.arguments[0])&&node.arguments[0].getText(file).includes(needle))matches.push(node.arguments[0]);ts.forEachChild(node,visit);};visit(file);
  assert.equal(matches.length,1);return actualCallback('const selected='+matches[0].getText(file),'selected',bindings);
}
function mobileFocusCase(sameRoute: boolean, sourceText=streams, api=true, hookText=forms, eagerSetter=false) {
  const dom=observerDOM(),target=dom.button,document=new EventTarget();
  let url=new URL(sameRoute?'/admin/streams/?view=retained':'/admin/workers/?view=retained','https://panel.example');
  const stats={assignments:0,hashchanges:0,replaces:0,mounts:0,listeners:0,opens:0,created:0,notices:0,cancelled:0,guardEvents:0,guardListeners:0};
  const historyState={framework:'retained'},events:string[]=[],state={dirty:true,pending:false,discard:false,prompts:0};
  const navigation=api?new EventTarget():undefined;
  const listeners=new Map<EventListenerOrEventListenerObject,EventListener>();
  if(navigation){
    const add=navigation.addEventListener.bind(navigation),remove=navigation.removeEventListener.bind(navigation);
    navigation.addEventListener=(name,listener,options)=>{
      if(name==='navigate'&&typeof listener==='function'&&listener.name==='onNavigate'){
        const tracked:EventListener=event=>{stats.guardEvents++;listener(event);};listeners.set(listener,tracked);stats.guardListeners++;add(name,tracked,options);
      }else add(name,listener,options);
    };
    navigation.removeEventListener=(name,listener,options)=>{
      const tracked=listener&&listeners.get(listener);
      if(tracked&&listener){listeners.delete(listener);stats.guardListeners--;remove(name,tracked,options);}else remove(name,listener,options);
    };
  }
  let historyMode:'normal'|'cancel'|'throw'='normal';
  const historyError=Error('controlled history failure');
  const surface=Object.assign(new EventTarget(),{
    navigation,
    location:{get hash(){return url.hash;},set hash(value:string){stats.assignments++;const next=new URL(url);next.hash=value;if(next.hash===url.hash)return;url=next;queueMicrotask(()=>{stats.hashchanges++;surface.dispatchEvent(new Event('hashchange'));});},get pathname(){return url.pathname;},get search(){return url.search;},get origin(){return url.origin;}},
    history:{state:historyState,replaceState(value:typeof historyState,_title:string,path:string){
      assert.equal(value,historyState);stats.replaces++;if(historyMode==='throw')throw historyError;
      const next=new URL(path,url);
      if(navigation){const event=new Event('navigate',{cancelable:true});Object.defineProperties(event,{destination:{value:{sameDocument:true,url:next.href}},navigationType:{value:'replace'}});
        if(historyMode==='cancel')event.preventDefault();navigation.dispatchEvent(event);
        if(event.defaultPrevented){stats.cancelled++;return;}}
      url=next;
    }},
    confirm(){state.prompts++;return state.discard;},
  });
  const add=surface.addEventListener.bind(surface),remove=surface.removeEventListener.bind(surface);
  surface.addEventListener=(...args:Parameters<typeof add>)=>{if(args[0]==='hashchange')stats.listeners++;add(...args);};
  surface.removeEventListener=(...args:Parameters<typeof remove>)=>{if(args[0]==='hashchange')stats.listeners--;remove(...args);};
  const exported:Record<string,unknown>={};
  const compiled=ts.transpileModule(source('lib/ui-v2/stream-create-focus-handoff.ts'),{compilerOptions:{target:ts.ScriptTarget.ES2022,module:ts.ModuleKind.CommonJS}}).outputText;
  new Function('exports','require','window','getComputedStyle',compiled)(exported,(name:string)=>{assert.equal(name,'./draft-navigation-lifecycle');return {subscribeDraftSessionExit};},surface,(element:Element)=>element.style);
  const focus=exported as typeof import('../../src/lib/ui-v2/stream-create-focus-handoff.ts');
  const mobile=source('components/shell/mobile-navigation.tsx'),action=source('components/shell/stream-create-action.tsx');
  const pendingNavigationRef={current:null as (()=>void)|null},createFocusRef={current:null as StreamCreateFocus|null},createFocus={current:null as StreamCreateFocus|null},createTrigger={current:null as Element|null};
  const createGeneration={current:0},createCloseIntent:{current:unknown}={current:null},createCloseSession={current:null as ReturnType<typeof subscribeDraftSessionExit>|null};
  let focusCalls=0,scopeActive=false,open=false,scheduled:boolean|undefined,guard:DraftExitController|undefined,canCreate=true,mounted=false;
  let hookCleanup:(()=>void)|undefined,sessionCleanup:(()=>void)|undefined,previousEnabled:boolean|undefined;
  const layouts:Array<()=>void>=[],passives:Array<()=>void>=[];
  const hook=currentFunction(hookText,'useDraftExit',{
    useI18n:()=>({locale:'en'}),useState:(initialize:()=>DraftExitController)=>[guard??=initialize()],
    useLayoutEffect:(callback:()=>void)=>layouts.push(callback),
    useEffect(callback:()=>void|(()=>void),deps:unknown[]){const enabled=Boolean(deps[0]);if(enabled!==previousEnabled){previousEnabled=enabled;passives.push(()=>{hookCleanup?.();hookCleanup=callback()||undefined;});}},
    createDraftExitController,createDraftNavigationGuard,subscribeDraftSessionExit,window:surface,document,
  });
  const renderGuard=()=>hook({enabled:open&&canCreate,pending:()=>state.pending}) as DraftExitController;
  const createDraftExit=renderGuard();layouts.length=0;passives.length=0;previousEnabled=undefined;
  target.focus=()=>{assert.equal(scopeActive,false,'target focus must follow the current focus scope cleanup');focusCalls++;dom.document.activeElement=target;};
  const closeFocus=jsxAt(sourceText,'SheetContent','onCloseAutoFocus',0,{createFocus,createTrigger});
  // Setters only reserve state. Actual hook layout/options and the view layout
  // run in source order after the event stack, before passive listener cleanup.
  const commit=()=>{
    if(!mounted)return;
    const wasOpen=open;if(scheduled!==undefined){open=scheduled;scheduled=undefined;}
    if(open&&!wasOpen)stats.opens++;
    assert.equal(renderGuard(),createDraftExit);for(const layout of layouts.splice(0))layout();
    actualLayout(sourceText,'const intent = createCloseIntent.current',{createCloseIntent,createGeneration,createCloseSession,createOpen:open,canCreate,document,window:surface})();
    if(wasOpen&&!open){scopeActive=true;closeFocus({preventDefault(){}});scopeActive=false;}
    for(const passive of passives.splice(0))passive();
    if(!canCreate)actualEffect(sourceText,'if (!canCreate)',{canCreate,createFocus,createTrigger})();
  };
  const setCreateOpen=(value:boolean)=>{scheduled=value;if(eagerSetter)commit();else queueMicrotask(()=>{if(scheduled!==undefined)commit();});};
  const refs={createGeneration,createCloseIntent,createCloseSession};
  const requestCreateOpen=actualCallback(sourceText,'requestCreateOpen',{...refs,setCreateOpen});
  const createBindings={...refs,document,window:surface,canCreate,createFocus,createTrigger,takeStreamCreateFocus:focus.takeStreamCreateFocus,setCreateOpen,requestCreateOpen,createDraftExit};
  let hashCleanup:undefined|(()=>void);
  const mount=()=>{assert.equal(stats.mounts,0,'same route cannot rerun sync effect');stats.mounts++;mounted=true;
    sessionCleanup=actualLayout(sourceText,'const session = subscribeDraftSessionExit', {...refs,window:surface,subscribeDraftSessionExit})();
    commit();hashCleanup=actualEffect(sourceText,'const syncFromHash',createBindings)();
  };
  if(sameRoute)mount();
  const navigate=()=>actualCallback(action,'navigate',{sameRoute:url.pathname==='/admin/streams/',window:surface,streamCreateHref:'/admin/streams/#create-stream',router:{push(path:string){assert.notEqual(url.pathname,'/admin/streams/');url=new URL(path,url);mount();}}})();
  const navigateAfterClose=actualCallback(mobile,'navigateAfterClose',{createFocusRef,offerStreamCreateFocus:focus.offerStreamCreateFocus,triggerRef:{current:target},pendingNavigationRef,setOpen(){events.push('menu-close');}});
  const linkClick=jsxAt(action,'Link','onClick',0,{mobile:true,onNavigateAfterClose:navigateAfterClose,navigate:()=>{events.push('navigate');navigate();},sameRoute});
  const menuClose=jsxAt(mobile,'SheetContent','onCloseAutoFocus',0,{pendingNavigationRef,createFocusRef});
  const menuOpen=jsxAt(mobile,'Sheet','onOpenChange',0,{pendingNavigationRef,createFocusRef,setOpen(){}});
  const requestCreateClose=actualCallback(sourceText,'requestCreateClose',{...refs,document,createDraftExit,setCreateOpen,window:surface});
  const requestClose=jsxAt(sourceText,'Sheet','onOpenChange',0,{setCreateOpen,requestCreateOpen,requestCreateClose});
  const onSaved=jsxAt(sourceText,'StreamSlotForm','onSaved',0,{requestCreateClose,createDraftExit,setCreateOpen,setCreatedStreams(update:(rows:unknown[])=>unknown[]){stats.created++;update([]);},setActionNotice(){stats.notices++;},uiText:(s:string)=>s});
  const pageOpen=jsxAt(sourceText,'Button','onClick',0,{cancelStreamCreateFocus:focus.cancelStreamCreateFocus,createFocus,createTrigger,setCreateOpen,requestCreateOpen});
  const menuCleanup=actualEffect(mobile,'() => { pendingNavigationRef.current = null;', {pendingNavigationRef,createFocusRef})();
  const cancelFocus=actualEffect(sourceText,'() => { createFocus.current?.cancel(); }',{createFocus})();
  const createCleanup=()=>{mounted=false;scheduled=undefined;sessionCleanup?.();sessionCleanup=undefined;hookCleanup?.();hookCleanup=undefined;cancelFocus();};
  return {dom,target,surface,focus,events,createFocus,createFocusRef,pendingNavigationRef,state,createDraftExit,stats,onSaved,historyState,commit,createCloseIntent,createGeneration,historyError,
    begin:()=>linkClick({preventDefault(){}}),menuClose:()=>menuClose({preventDefault(){}}),menuOpen:()=>menuOpen(true),close:()=>requestClose(false),pageOpen:(target:Element)=>pageOpen({currentTarget:target}),
    losePermission:()=>{canCreate=false;commit();},allowPermission:()=>{canCreate=true;commit();},
    setHistoryMode:(mode:typeof historyMode)=>{historyMode=mode;},
    move:(path:string)=>{url=new URL(path,url);},menuCleanup,createCleanup,get open(){return open;},get focusCalls(){return focusCalls;},
    dispose(){hashCleanup?.();menuCleanup();createCleanup();focus.cancelStreamCreateFocus();assert.equal(stats.listeners,0);assert.equal(stats.guardListeners,0);}};
}

test('UI-CREATE-FOCUS-009: actual Menu, navigation, hash and Sheet close transfer the original target once after cleanup',async()=>{
  for(const sameRoute of [true,false]){
    const flow=mobileFocusCase(sameRoute);
    try {
      flow.begin();assert.deepEqual(flow.events,['menu-close']);flow.menuClose();flow.menuClose();assert.equal(flow.open,false);
      await settleEvents();assert.deepEqual(flow.events,['menu-close','navigate']);assert.equal(flow.open,true);assert.ok(flow.createFocus.current);assert.equal(flow.focus.takeStreamCreateFocus(),null);
      flow.close();assert.equal(flow.focusCalls,0);await settleEvents();assert.equal(flow.focusCalls,1);assert.equal(flow.dom.document.activeElement,flow.target);
      flow.begin();flow.menuClose();await settleEvents();flow.close();await settleEvents();assert.equal(flow.focusCalls,2,'reentry obtains one fresh handoff');
    } finally {flow.dispose();}
  }
});
test('UI-CREATE-FOCUS-010: actual dirty Stay/Discard and submitted save outcomes keep the same lease until real close',async()=>{
  const flow=mobileFocusCase(false);
  try {
    flow.begin();flow.menuClose();await settleEvents();
    flow.createDraftExit.register({isDirty:()=>true,saved(){}});flow.close();await settleEvents();assert.equal(flow.open,true);assert.equal(flow.focusCalls,0);assert.equal(flow.state.prompts,1);
    flow.state.discard=true;flow.close();await settleEvents();assert.equal(flow.open,false);assert.equal(flow.focusCalls,1);
  } finally {flow.dispose();}
  for(const outcome of ['succeeded','failed','outcome_unknown','later-edit']){
    const mobile=mobileFocusCase(true);
    try {
      mobile.begin();mobile.menuClose();await settleEvents();
      const form=createFlow(false,slot,mobile.createFocus.current,{controller:mobile.createDraftExit,state:mobile.state,onSaved:mobile.onSaved});form.update('title',{headerTitleValue:'Submitted'});const intent=form.prepare();
      if(outcome==='later-edit')form.renderBasic(['Later edit']);
      form.result(outcome==='succeeded'||outcome==='later-edit'?{kind:'succeeded',value:{id:'created',name:'Submitted'}}:{kind:outcome,error:{kind:'conflict',status:409,messageKey:'conflict'}},intent);
      await settleEvents();assert.equal(mobile.focusCalls,outcome==='succeeded'?1:0);assert.equal(mobile.open,outcome!=='succeeded');assert.equal(form.controller.dirty(),outcome!=='succeeded');
    } finally {mobile.dispose();}
  }
});
test('UI-CREATE-FOCUS-011: cancellation, permission, session, unmount and target replacement never focus a substitute or replay navigation',async()=>{
  for(const fault of ['menu-cancel','source-unmount','session-before-navigation','permission','session-after-open','create-unmount','target-replacement','disabled','hidden','superseded']){
    const flow=mobileFocusCase(false);
    try {
      flow.begin();flow.menuClose();
      if(fault==='menu-cancel')flow.menuOpen();if(fault==='source-unmount')flow.menuCleanup();if(fault==='session-before-navigation')notifyDraftSessionExit();
      await settleEvents();
      if(['menu-cancel','source-unmount','session-before-navigation'].includes(fault)){assert.equal(flow.open,false);assert.deepEqual(flow.events,['menu-close']);continue;}
      const lease=flow.createFocus.current!;
      if(fault==='permission')flow.losePermission();if(fault==='session-after-open')notifyDraftSessionExit();if(fault==='create-unmount')flow.createCleanup();
      if(fault==='target-replacement'){flow.target.parentElement=null;flow.dom.main.children=flow.dom.main.children.filter(e=>e!==flow.target);flow.dom.main.add(new Element('BUTTON','Open'));}
      if(fault==='disabled')flow.target.disabled=true;if(fault==='hidden')flow.target.hidden=true;
      if(fault==='superseded')flow.focus.offerStreamCreateFocus(flow.dom.input as unknown as HTMLButtonElement);
      flow.close();lease.restoreAfterClose();await settleEvents();lease.restoreAfterClose();await settleEvents();assert.equal(flow.focusCalls,0,fault);
    } finally {flow.dispose();}
  }
});

test('UI-CREATE-REENTRY-010: real successful form ack closes through its mounted guard, clears only create hash, and permits the next same-route Menu action',async()=>{
  for(const sameRoute of [true,false]){
    const flow=mobileFocusCase(sameRoute);
    try {
      flow.begin();flow.menuClose();await settleEvents();assert.equal(flow.open,true);
      const pathname=flow.surface.location.pathname,search=flow.surface.location.search;
      const form=createFlow(false,slot,flow.createFocus.current,{controller:flow.createDraftExit,state:flow.state,onSaved:flow.onSaved});form.update('title',{headerTitleValue:'Submitted'});
      form.result({kind:'succeeded',value:{id:'created',name:'Submitted'}},form.prepare());await settleEvents();
      assert.equal(flow.open,false);assert.equal(flow.surface.location.hash,'');assert.equal(flow.focusCalls,1);assert.equal(flow.surface.history.state,flow.historyState);
      assert.equal(flow.surface.location.pathname,pathname);assert.equal(flow.surface.location.search,search);assert.equal(flow.stats.replaces,1);assert.equal(flow.stats.created,1);assert.equal(flow.stats.notices,1);
      flow.begin();flow.menuClose();await settleEvents();assert.equal(flow.open,true);assert.equal(flow.stats.opens,2);assert.equal(flow.stats.mounts,1);assert.equal(flow.stats.listeners,1);
      flow.close();await settleEvents();assert.equal(flow.open,false);assert.equal(flow.surface.location.hash,'');assert.equal(flow.focusCalls,2);
      assert.equal(flow.stats.hashchanges,sameRoute?2:1,'only changed same-document hash setters dispatch events; a real cross-route mount is distinct');
    } finally {flow.dispose();}
  }
});
test('UI-CREATE-REENTRY-011: missing accepted hash cleanup reproduces the original same-mount success-close defect',async()=>{
  const mutant=streams.replace('window.history.replaceState(window.history.state, "", window.location.pathname + window.location.search);','void 0;');assert.notEqual(mutant,streams);
  const flow=mobileFocusCase(true,mutant);
  try {
    flow.begin();flow.menuClose();await settleEvents();flow.onSaved({id:'created',name:'Created'});await settleEvents();assert.equal(flow.open,false);assert.equal(flow.focusCalls,1);
    flow.begin();flow.menuClose();await settleEvents();assert.equal(flow.surface.location.hash,'#create-stream');assert.equal(flow.stats.assignments,2);assert.equal(flow.stats.hashchanges,1);assert.equal(flow.stats.mounts,1);
    assert.throws(()=>assert.equal(flow.open,true),/false !== true/,'the original defect is detected without remounting the effect');
  } finally {flow.dispose();}
});
test('UI-CREATE-REENTRY-012: Stay, pending, failed/unknown and later edits retain fragment and the exact lease until accepted close',async()=>{
  for(const outcome of ['stay','pending','failed','outcome_unknown','later-basic','later-visual']){
    const flow=mobileFocusCase(true);
    try {
      flow.begin();flow.menuClose();await settleEvents();const lease=flow.createFocus.current;
      const form=createFlow(false,slot,lease,{controller:flow.createDraftExit,state:flow.state,onSaved:flow.onSaved});form.update('title',{headerTitleValue:'Submitted'});const intent=form.prepare();
      if(outcome==='later-basic')form.renderBasic(['Later edit']);if(outcome==='later-visual')form.update('title',{headerTitleValue:'Later visual'});
      if(outcome==='pending')flow.state.pending=true;
      if(outcome==='stay'||outcome==='pending')flow.close();else form.result(outcome==='failed'||outcome==='outcome_unknown'?{kind:outcome,error:{kind:'conflict',status:409,messageKey:'conflict'}}:{kind:'succeeded',value:{id:'created',name:'Submitted'}},intent);
      await settleEvents();assert.equal(flow.open,true);assert.equal(flow.surface.location.hash,'#create-stream');assert.equal(flow.createFocus.current,lease);assert.equal(flow.focusCalls,0);assert.equal(flow.stats.replaces,0);assert.equal(form.controller.dirty(),true);
      flow.state.pending=false;flow.state.discard=true;flow.close();await settleEvents();assert.equal(flow.surface.location.hash,'');assert.equal(flow.focusCalls,1);
    } finally {flow.dispose();}
  }
});
test('UI-CREATE-REENTRY-013: normal Page close preserves a foreign fragment and framework state and returns to its actual Page target',async()=>{
  const flow=mobileFocusCase(true),page=flow.dom.main.add(new Element('BUTTON','Create stream'));let pageFocus=0;page.focus=()=>{pageFocus++;flow.dom.document.activeElement=page;};
  try {
    flow.surface.history.replaceState(flow.historyState,'','/admin/streams/?view=retained#other-section');
    flow.pageOpen(page);await settleEvents();assert.equal(flow.open,true);flow.close();await settleEvents();assert.equal(flow.open,false);assert.equal(flow.surface.location.hash,'#other-section');assert.equal(flow.surface.location.search,'?view=retained');assert.equal(flow.stats.replaces,1);assert.equal(flow.surface.history.state,flow.historyState);assert.equal(pageFocus,1);assert.equal(flow.focusCalls,0);assert.equal(flow.dom.document.activeElement,page);
  } finally {flow.dispose();}
});

test('UI-CREATE-NAVIGATION-011: actual listeners see synchronous replace only after committed disabled options, for both close callers and APIs',async()=>{
  assert.ok(streams.indexOf('useDraftExit({ enabled: createOpen')<streams.indexOf('const intent = createCloseIntent.current'));
  for(const api of [false,true])for(const mode of ['clean','discard','stay','pending'])for(const saved of [false,true]){
    const flow=mobileFocusCase(true,streams,api);
    try {
      flow.begin();flow.menuClose();await settleEvents();
      flow.createDraftExit.register({isDirty:()=>mode==='discard'||mode==='stay',pending:()=>mode==='pending',saved(){throw Error('close must not acknowledge readers');}});
      const accepted=mode==='clean'||mode==='discard';flow.state.discard=mode==='discard';
      if(saved)flow.onSaved({id:'created',name:'Created'});else flow.close();
      assert.equal(flow.open,true,'state setter cannot commit inside the decision callback');assert.equal(flow.stats.replaces,0);assert.equal(flow.surface.location.hash,'#create-stream');
      await settleEvents();assert.equal(flow.open,!accepted);assert.equal(flow.stats.replaces,accepted?1:0);assert.equal(flow.stats.cancelled,0);
      assert.equal(flow.state.prompts,mode==='discard'||mode==='stay'?1:0);assert.equal(flow.focusCalls,accepted?1:0);
      assert.equal(flow.stats.guardEvents,api&&accepted?1:0,'real guard listener is still attached until passive cleanup');assert.equal(flow.createCloseIntent.current,null);
      if(accepted){flow.begin();flow.menuClose();await settleEvents();assert.equal(flow.open,true);flow.close();await settleEvents();assert.equal(flow.focusCalls,2);assert.equal(flow.stats.replaces,2);assert.equal(flow.stats.mounts,1);}
    } finally {flow.dispose();}
  }
});
test('UI-CREATE-NAVIGATION-012: reopens, route/hash/search changes, permission, session and unmount consume stale intents without changing another generation',async()=>{
  for(const fault of ['reopen','pathname','search','hash','permission','session','unmount','generation']){
    const flow=mobileFocusCase(true);
    try {
      flow.begin();flow.menuClose();await settleEvents();flow.close();assert.ok(flow.createCloseIntent.current);
      if(fault==='reopen')flow.pageOpen(flow.target);
      if(fault==='pathname')flow.move('/admin/workers/?view=retained#create-stream');
      if(fault==='search')flow.move('/admin/streams/?view=other#create-stream');
      if(fault==='hash')flow.move('/admin/streams/?view=retained#another');
      if(fault==='permission')flow.losePermission();if(fault==='session')notifyDraftSessionExit();if(fault==='unmount')flow.createCleanup();
      if(fault==='generation')flow.createGeneration.current++;
      await settleEvents();assert.equal(flow.createCloseIntent.current,null,fault);assert.equal(flow.stats.replaces,0,fault);
      flow.move('/admin/streams/?view=retained#create-stream');flow.allowPermission();flow.commit();assert.equal(flow.stats.replaces,0,'old intent cannot revive when a later commit looks identical');
      if(fault==='reopen')assert.equal(flow.open,true);
    } finally {flow.dispose();}
  }
});
test('UI-CREATE-NAVIGATION-013: cancellation and exceptions are consumed once, another real draft owner can still refuse replacement',async()=>{
  for(const fault of ['cancel','throw','other-owner'] as const){
    const flow=mobileFocusCase(true),cleanups:Array<()=>void>=[];
    try {
      flow.begin();flow.menuClose();await settleEvents();
      if(fault==='other-owner'){
        const effect=(callback:()=>void|(()=>void))=>{const cleanup=callback();if(cleanup)cleanups.push(cleanup);};
        const hook=currentFunction(forms,'useDraftExit',{useI18n:()=>({locale:'en'}),useState:(init:()=>unknown)=>[init()],useLayoutEffect:effect,useEffect:effect,createDraftExitController,createDraftNavigationGuard,subscribeDraftSessionExit,window:flow.surface,document:new EventTarget()});
        const other:DraftExitController=hook({enabled:true,pending:false});other.register({isDirty:()=>true,saved(){throw Error('foreign owner must remain dirty');}});
      }else flow.setHistoryMode(fault);
      flow.close();if(fault==='throw')assert.throws(()=>flow.commit(),error=>error===flow.historyError);else flow.commit();
      await settleEvents();assert.equal(flow.surface.location.hash,'#create-stream');assert.equal(flow.stats.replaces,1);assert.equal(flow.createCloseIntent.current,null);
      flow.setHistoryMode('normal');flow.commit();assert.equal(flow.stats.replaces,1,'cancelled or throwing History cannot be retried');
      if(fault!=='throw')assert.equal(flow.stats.cancelled,1);
    } finally {for(const cleanup of cleanups.reverse())cleanup();flow.dispose();}
  }
});
test('UI-CREATE-NAVIGATION-014: accepted-callback History, missing listener and eager-setter boundary mutants fail their actual contracts',async()=>{
  const history='window.history.replaceState(window.history.state, "", window.location.pathname + window.location.search);';
  const moved=streams.replace(history,'void 0;').replace('setCreateOpen(false);','setCreateOpen(false); '+history);assert.notEqual(moved,streams);
  const missing=forms.replace('navigation?.addEventListener("navigate", onNavigate);','void 0;');assert.notEqual(missing,forms);
  for(const mutation of ['callback-history','missing-listener','eager-setter']){
    const flow=mobileFocusCase(true,mutation==='callback-history'?moved:streams,true,mutation==='missing-listener'?missing:forms,mutation==='eager-setter');
    try {
      flow.begin();flow.menuClose();await settleEvents();flow.close();
      if(mutation==='eager-setter')assert.throws(()=>assert.equal(flow.open,true),/false !== true/);
      await settleEvents();
      if(mutation==='callback-history'){assert.equal(flow.stats.cancelled,1);assert.throws(()=>assert.equal(flow.surface.location.hash,''));}
      if(mutation==='missing-listener')assert.throws(()=>assert.equal(flow.stats.guardEvents,1),/0 !== 1/);
    } finally {flow.dispose();}
  }
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
