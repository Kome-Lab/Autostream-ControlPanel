import assert from "node:assert/strict";
import type { UADOM } from "./browser-ua-identity.mts";

type Send = (method: string, params?: Record<string, unknown>) => Promise<Record<string, unknown>>;
type Frame = { id: string; loaderId: string };
type Part = { id: number; empty: boolean; display: string; active: number; paused: number };
type Sample = { frame: Frame; host: number; roots: number[]; error: boolean; panel?: Part; spinner?: Part };
export type NativeMediaSettlement = Readonly<{
  status: "initial-absent" | "initial-hidden" | "initial-inactive" | "observed-natural-end";
  reads: number;
}>;
const selector = "main[data-screen-family=archive-share] video[controls]";
const panelPseudo = "-internal-media-controls-loading-panel";
const spinnerPseudo = "-internal-media-controls-loading-panel-spinner-frame";

// This precedes the first AX/identity baseline. It neither creates nor resets a
// NativeFocusObserver ledger and never changes an animation or media property.
async function readSample(send: Send, source: string, read: number): Promise<Sample> {
  const group = "ui-native-media-settlement-" + read;
  let primary: unknown;
  let failed = false;
  const value = async <T,>(objectId: string, functionDeclaration: string, args: Record<string, unknown>[] = []) => {
    const response = await send("Runtime.callFunctionOn", { objectId, functionDeclaration, arguments: args, returnByValue: true });
    assert.ok(!response.exceptionDetails, "native media state read failed");
    return (response.result as { value?: T })?.value;
  };
  const frame = async () => {
    const result = await send("Page.getFrameTree");
    const current = (result.frameTree as { frame?: Frame })?.frame;
    assert.ok(current?.id && current.loaderId, "native media frame unavailable");
    return { id: current.id, loaderId: current.loaderId };
  };
  try {
    const before = await frame();
    const selected = await send("Runtime.evaluate", { expression: `(() => {const nodes=[...document.querySelectorAll(${JSON.stringify(selector)})];return nodes.length===1?nodes[0]:null;})()`, objectGroup: group, returnByValue: false });
    assert.ok(!selected.exceptionDetails, "native media host read failed");
    const objectId = (selected.result as { objectId?: string })?.objectId;
    assert.ok(objectId, "one native public media host required");
    const mediaRead = `function(source){return {owned:this.isConnected&&this.ownerDocument===document&&document.querySelectorAll(${JSON.stringify(selector)}).length===1&&document.querySelector(${JSON.stringify(selector)})===this&&this.getAttribute('src')===source&&this.currentSrc===source,ready:this.readyState,network:this.networkState,error:this.error?.code||0,paused:this.paused,atStart:this.currentTime===0};}`;
    const media = await value<{ owned: boolean; ready: number; network: number; error: number; paused: boolean; atStart: boolean }>(objectId, mediaRead, [{ value: source }]);
    assert.ok(media?.owned === true && media.paused === true && media.atStart === true, "native media fixture owner/source changed");
    for (const [key, max] of [["ready", 4], ["network", 3], ["error", 4]] as const) assert.ok(Number.isInteger(media[key]) && media[key] >= 0 && media[key] <= max, "native media state unavailable");
    assert.equal(media.ready, 0, "synthetic unavailable media must not become playable");
    const described = await send("DOM.describeNode", { objectId, depth: -1, pierce: true });
    const host = described.node as UADOM;
    assert.ok(host?.nodeName === "VIDEO" && Number.isSafeInteger(host.backendNodeId) && host.backendNodeId > 0, "native media host identity unavailable");
    const owned = new Map<number, UADOM>(), parents = new Map<number, number>();
    const visit = (node: UADOM, parent: number) => {
      assert.ok(owned.size < 4096 && Number.isSafeInteger(node.backendNodeId) && node.backendNodeId > 0 && !owned.has(node.backendNodeId), "native media DOM identity/bound");
      assert.ok(node.nodeType !== undefined, "native media DOM type unavailable");
      if ([1, 9, 11].includes(node.nodeType)) assert.ok(Number.isSafeInteger(node.childNodeCount) && node.childNodeCount! >= 0 && (node.children?.length || 0) === node.childNodeCount, "native media DOM incomplete");
      owned.set(node.backendNodeId, node); parents.set(node.backendNodeId, parent);
      for (const child of node.children || []) visit(child, node.backendNodeId);
      for (const shadow of node.shadowRoots || []) { assert.ok(shadow.shadowRootType === "user-agent", "native media shadow owner changed"); visit(shadow, node.backendNodeId); }
    };
    for (const shadow of host.shadowRoots || []) { assert.ok(shadow.shadowRootType === "user-agent", "native media host shadow owner changed"); visit(shadow, host.backendNodeId); }
    assert.ok(owned.size > 0, "native media complete UA owner required");
    const matches = (pseudo: string) => [...owned.values()].filter(node => {
      const attributes = node.attributes || [], values: string[] = [];
      assert.equal(attributes.length % 2, 0, "native media attributes incomplete");
      for (let index = 0; index < attributes.length; index += 2) if (attributes[index] === "pseudo") values.push(attributes[index + 1]);
      assert.ok(values.length <= 1, "native media pseudo ambiguous");
      return values[0] === pseudo;
    });
    const panels = matches(panelPseudo), spinners = matches(spinnerPseudo);
    assert.ok(panels.length <= 1 && spinners.length <= 1 && (panels.length === 1 || spinners.length === 0), "native media loading part ambiguous");
    if (spinners.length) {
      let current = spinners[0].backendNodeId, found = false;
      for (let index = 0; index < owned.size; index++) { const parent = parents.get(current); if (parent === panels[0].backendNodeId) { found = true; break; } if (parent === undefined) break; current = parent; }
      assert.ok(found, "native media spinner belongs to another loading part");
    }
    const part = async (node: UADOM): Promise<Part> => {
      const resolved = await send("DOM.resolveNode", { backendNodeId: node.backendNodeId, objectGroup: group });
      const target = (resolved.object as { objectId?: string })?.objectId;
      assert.ok(target, "native media loading part unavailable");
      const state = await value<{ owned: boolean; display: string; running: number; pending: number; paused: number; total: number; unknown: number }>(target,
        `function(host){let root=this.getRootNode(),owned=false;for(let i=0;i<32&&root instanceof ShadowRoot;i++){if(root.host===host){owned=true;break;}root=root.host.getRootNode();}const a=this.getAnimations({subtree:true});return {owned:owned&&this.isConnected&&this.ownerDocument===document&&host.ownerDocument===document,display:getComputedStyle(this).display,running:a.filter(v=>v.playState==='running').length,pending:a.filter(v=>v.pending).length,paused:a.filter(v=>v.playState==='paused').length,total:a.length,unknown:a.filter(v=>!['running','paused','finished','idle'].includes(v.playState)||typeof v.pending!=='boolean').length};}`, [{ objectId }]);
      assert.ok(state?.owned === true && ["none", "block", "inline", "inline-block", "flex", "inline-flex", "grid"].includes(state.display), "native media loading ownership/style unavailable");
      for (const count of [state.running, state.pending, state.paused, state.total, state.unknown]) assert.ok(Number.isSafeInteger(count) && count >= 0 && count <= 32, "native media animation bound");
      assert.equal(state.unknown, 0, "native media animation state unavailable");
      assert.ok(state.running + state.paused <= state.total && state.pending <= state.total, "native media animation counts inconsistent");
      const shadows = node.shadowRoots || [];
      return { id: node.backendNodeId, empty: shadows.length === 1 && shadows[0].childNodeCount === 0 && (shadows[0].children?.length || 0) === 0, display: state.display, active: state.running + state.pending, paused: state.paused };
    };
    const panel = panels.length ? await part(panels[0]) : undefined, spinner = spinners.length ? await part(spinners[0]) : undefined;
    const last = await frame();
    assert.ok(last.id === before.id && last.loaderId === before.loaderId, "native media document changed inside read");
    const finalMedia = await value<NonNullable<typeof media>>(objectId, mediaRead, [{ value: source }]);
    assert.ok(finalMedia && (["owned", "ready", "network", "error", "paused", "atStart"] as const).every(key => finalMedia[key] === media[key]), "native media host/source/state changed inside read");
    return { frame: before, host: host.backendNodeId, roots: (host.shadowRoots || []).map(root => root.backendNodeId), error: media.network === 3 && media.error === 4, panel, spinner };
  } catch (error) { failed = true; primary = error; throw error; }
  finally {
    try { await send("Runtime.releaseObjectGroup", { objectGroup: group }); }
    catch (error) { throw failed ? new AggregateError([primary, error], "Native media state and cleanup failed", { cause: primary }) : error; }
  }
}

export async function settleNativeMediaLoading(send: Send, source: string, now: () => number = () => performance.now()): Promise<NativeMediaSettlement> {
  assert.ok(source.length > 0, "independent public media source required");
  await send("DOM.enable");
  const start = now();
  let first: Sample | undefined, active: Sample | undefined;
  for (let reads = 1; reads <= 128; reads++) {
    assert.ok(now() - start < 10_000, "native media loading settlement timed out");
    const current = await readSample(send, source, reads);
    assert.ok(now() - start < 10_000, "native media loading settlement timed out");
    if (first) {
      assert.ok(current.frame.id === first.frame.id && current.frame.loaderId === first.frame.loaderId, "native media settlement document changed");
      assert.ok(current.host === first.host, "native media settlement host changed");
      assert.ok(current.roots.length === first.roots.length && current.roots.every((id, index) => id === first!.roots[index]), "native media settlement UA owner replaced");
    } else first = current;
    assert.ok(!active || current.error, "native media error fixture changed while settling");
    if (current.error) {
      const panel = current.panel, spinner = current.spinner;
      if (!active) {
        if (!panel) return { status: "initial-absent", reads };
        if (panel.display === "none") return { status: "initial-hidden", reads };
        assert.ok(spinner || panel.empty, "native media visible loading structure incomplete");
        if (panel.active + (spinner?.active || 0) === 0) {
          assert.equal(panel.paused + (spinner?.paused || 0), 0, "native media loading animation is paused");
          return { status: "initial-inactive", reads };
        }
        assert.ok(spinner, "active native media loading requires its unique spinner");
        active = current;
      } else {
        assert.ok(panel && panel.id === active.panel?.id, "native media loading panel replaced");
        if (panel.display === "none" && panel.empty && !spinner && panel.active === 0 && panel.paused === 0) return { status: "observed-natural-end", reads };
        assert.ok(spinner && spinner.id === active.spinner?.id, "active native media spinner replaced or removed before cleanup");
      }
    }
    // A real rendering boundary, not a duration sleep or an animation action.
    const rendered = await send("Runtime.evaluate", { expression: "(async()=>{await new Promise(requestAnimationFrame);return true;})()", awaitPromise: true, returnByValue: true });
    assert.ok(!rendered.exceptionDetails && (rendered.result as { value?: unknown })?.value === true, "native media rendering boundary unavailable");
  }
  throw new Error("native media loading settlement exceeded its read bound");
}

// Product hydration/error preparation precedes every strict identity sample.
// This is one atomic read, not a retry of a rejected native observation.
export function nativePublicMediaReadyExpression(source: string): string {
  return `(() => {const nodes=[...document.querySelectorAll(${JSON.stringify(selector)})],host=nodes[0];return nodes.length===1&&host.isConnected===true&&host.ownerDocument===document&&host.getAttribute('src')===${JSON.stringify(source)}&&host.currentSrc===${JSON.stringify(source)}&&host.readyState===0&&host.networkState===3&&host.error?.code===4&&host.paused===true&&host.currentTime===0;})()`;
}
