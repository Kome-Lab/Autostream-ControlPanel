import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import type { BrowserHarness, RouteResolver, StubRequest, StubResponse } from "./browser-harness.mts";
import { deferred } from "../ui-browser-navigation-helpers.mts";
import { fixtureRoutes } from "../stream-create-covers-browser-fixture.mts";

export type ImageKind = "cover" | "background";
export type ResponseMode = "normal" | "committed-unknown" | "unknown-delete" | "unobserved" | "failed-variant";
export const unsafeErrorProbe = "INTERNAL_SECRET_LEAK_PROBE";
export const labels = {
  ja: { openCreate: "配信枠を作成", create: "配信枠を作成", worker: "担当Worker Node", encoder: "担当Encoder Node", coverSource: "蓋画像のソース", upload: "アップロード", preset: "プリセット", coverPreset: "蓋画像プリセット", backgroundMode: "シーン背景モード", image: "画像（cover / center crop）", check: "画像の状態を確認", resume: "画像変換を再開", retry: "この画像を再試行", discard: "この画像を取り消す", ready: "利用可能（枠は未保存）" },
  en: { openCreate: "Create stream", create: "Create stream slot", worker: "Assigned worker node", encoder: "Assigned encoder node", coverSource: "Video cover source", upload: "Upload", preset: "Preset", coverPreset: "Video cover preset", backgroundMode: "Scene background mode", image: "Image (cover / center crop)", check: "Check image status", resume: "Resume image conversion", retry: "Retry this image", discard: "Discard this image", ready: "Ready (stream slot not saved)" },
};

// Exact development React format strings, never their interpolated arguments.
// This diagnostic classification does not exempt a console event from a guard.
const consoleFormats = {
  hydration_attributes: "A tree hydrated but some attributes of the server rendered HTML didn't match the client properties. This won't be patched up. This can happen if a SSR-ed Client Component used:\n\n- A server/client branch `if (typeof window !== 'undefined')`.\n- Variable input such as `Date.now()` or `Math.random()` which changes each time it's called.\n- Date formatting in a user's locale which doesn't match the server.\n- External changing data without sending a snapshot of it along with the HTML.\n- Invalid HTML tag nesting.\n\nIt can also happen if the client has a browser extension installed which messes with the HTML before React loaded.\n\n%s%s",
  missing_key: 'Each child in a list should have a unique "key" prop.%s%s See https://react.dev/link/warning-keys for more information.',
  duplicate_key: "Encountered two children with the same key, `%s`. Keys should be unique so that components maintain their identity across updates. Non-unique keys may cause children to be duplicated and/or omitted — the behavior is unsupported and could change in a future version.",
  uncontrolled_to_controlled: "A component is changing an uncontrolled input to be controlled. This is likely caused by the value changing from undefined to a defined value, which should not happen. Decide between using a controlled or uncontrolled input element for the lifetime of the component. More info: https://react.dev/link/controlled-components",
  controlled_to_uncontrolled: "A component is changing a controlled input to be uncontrolled. This is likely caused by the value changing from a defined to undefined, which should not happen. Decide between using a controlled or uncontrolled input element for the lifetime of the component. More info: https://react.dev/link/controlled-components",
  invalid_child: "In HTML, %s cannot be a child of <%s>.%s\nThis will cause a hydration error.%s",
  invalid_descendant: "In HTML, %s cannot be a descendant of <%s>.\nThis will cause a hydration error.%s",
} as const;
export const p3ConsoleDiagnosticSource = `(() => {
  const formats=${JSON.stringify(consoleFormats)},state={schema:'p3-safe-console-v1',rows:[],dropped:0};
  Object.defineProperty(globalThis,'__p3SafeConsole',{value:state,configurable:true});
  for(const method of ['error','warn','assert']) {
    const original=console[method];
    console[method]=function(...args) {
      try {return Reflect.apply(original,this,args);}
      finally {try {
        const first=args[0],match=typeof first==='string'?Object.entries(formats).find(([,format])=>first===format):null;
        const category=match?match[0]:first instanceof Error&&first.message.startsWith('Hydration failed because the server rendered')?'hydration_error_object':typeof first==='string'?'unlisted_string':first instanceof Error?'unlisted_error_object':'unlisted_nonstring';
        if(state.rows.length<64)state.rows.push({method,category,fixed_format:match?match[1]:null});else state.dropped++;
      }catch { /* Never replace or suppress the original console operation. */ }}
    };
  }
})()`;

type SetupStage = "configured" | "navigating" | "loaded" | "list-dom" | "responses-idle" | "react-commit" | "before-create-click" | "after-create-click" | "form-ready" | "scenario" | "failure" | "before-close";
// These GET consumers mount on the actual Admin streams route. Login/setup
// queries are not a universal requirement for opening this screen.
const setupPaths = ["/auth/me", "/streams"] as const;
function safeUnmockedPath(pathname: string) {
  const literal = ["/admin/streams", "/login", "/favicon.ico", "/_next/webpack-hmr", "/__nextjs_original-stack-frames", "/__nextjs_source-map", "/__nextjs_error_feedback", "/__nextjs_launch-editor"].find(path => path === pathname);
  return { path: literal ?? null, path_class: literal ? "fixed_path" : pathname.startsWith("/_next/static/") ? "next_static" : pathname.startsWith("/__nextjs") ? "next_dev_rpc" : "other", pathname_sha256: createHash("sha256").update(pathname).digest("hex") };
}

export function createP3MediaFixture() {
  const app = fixtureRoutes();
  const diagnostic = { schema: "p3-initial-render-diagnostic-v1", stage: "configured" as SetupStage, order: 0, unmocked_gets: [] as { order: number; stage: SetupStage; path: string | null; path_class: string; pathname_sha256: string }[], dropped_gets: 0, checkpoints: [] as Record<string, unknown>[] };
  // Observe the final resolver's null result, after both P3 and shared fixtures.
  // Only registration is proxied; the real harness and every response are unchanged.
  const observeBrowserRoutes = (browser: BrowserHarness) => new Proxy(browser, { get(target, property, receiver) {
    if (property !== "setRouteResolver") return Reflect.get(target, property, receiver);
    return (resolver: RouteResolver) => target.setRouteResolver(request => {
      const response = resolver(request);
      if (request.method === "GET" && response === null) {
        const pathname = new URL(request.url).pathname.replace(/\/$/, "") || "/";
        const row = { order: ++diagnostic.order, stage: diagnostic.stage, ...safeUnmockedPath(pathname) };
        if (diagnostic.unmocked_gets.length < 512) diagnostic.unmocked_gets.push(row); else diagnostic.dropped_gets++;
      }
      return response;
    });
  } });
  const mark = (stage: SetupStage) => { diagnostic.stage = stage; };
  const checkpoint = async (browser: BrowserHarness, stage: SetupStage, locale: "ja" | "en") => {
    mark(stage);
    const row: Record<string, unknown> = { order: ++diagnostic.order, stage, console_error_count: browser.consoleErrorCount, setup: setupPaths.map(path => ({ path, requested: browser.requests.get(path) || 0, settled: browser.responses.get(path) || 0 })) };
    try {
      row.document = await browser.evaluate(`(() => {
        const matches=[...document.querySelectorAll('main button')].filter(button=>button.textContent.trim()===${JSON.stringify(labels[locale].openCreate)}&&button.getBoundingClientRect().width>0),button=matches.length===1?matches[0]:null;
        const props=button&&Object.keys(button).find(key=>key.startsWith('__reactProps$'));
        return {ready_state:document.readyState,locale:['ja','en'].includes(document.documentElement.lang)?document.documentElement.lang:'other',create_trigger_count:matches.length,react_click_handler:!!props&&typeof button[props]?.onClick==='function',create_view:!!document.querySelector('[data-stream-create-view]'),safe_console:globalThis.__p3SafeConsole||null};
      })()`);
    } catch { row.document = { unavailable: true }; }
    diagnostic.checkpoints.push(row);
  };
  const requests: { method: string; pathname: string; mode: ResponseMode }[] = [];
  const gates: { method: string; path: RegExp; mode: ResponseMode; used: boolean; entered: ReturnType<typeof deferred>; release: ReturnType<typeof deferred> }[] = [];
  const gate = (method: string, path: RegExp, mode: ResponseMode = "normal") => {
    const value = { method, path, mode, used: false, entered: deferred(), release: deferred() };
    gates.push(value); return value;
  };
  const route = (request: StubRequest): StubResponse | null => {
    const pathname = new URL(request.url).pathname.replace(/\/$/, "");
    const held = gates.find(item => !item.used && item.method === request.method && item.path.test(pathname));
    const mode = held?.mode || "normal";
    if (pathname.startsWith("/media-assets") || pathname === "/streams" || pathname.startsWith("/streams/")) requests.push({ method: request.method, pathname, mode });
    // The maintained fixture parses actual multipart metadata and performs its
    // state transition before an optional response gate. No session/nonce is invented.
    let response = app.route(request);
    if (!response) return null;
    if (mode === "committed-unknown" || mode === "unknown-delete") {
      if (mode === "committed-unknown") { assert.equal(pathname, "/media-assets"); assert.equal(request.method, "POST"); }
      else { assert.match(pathname, /^\/media-assets\/asset-\d+$/); assert.equal(request.method, "DELETE"); }
      response = { status: 503, body: { code: "media_asset_operation_failed" }, requiredResponse: false };
    } else if (mode === "unobserved") {
      assert.equal(request.method, "GET"); assert.match(pathname, /\/upload-sessions\//);
      assert.ok(response.body && typeof response.body === "object");
      response = { ...response, body: { ...response.body, assets: [] } };
    } else if (mode === "failed-variant") {
      const assetID = pathname.match(/^\/media-assets\/(asset-\d+)\/variants$/)?.[1]; assert.ok(assetID);
      const original = app.state.variants.get(assetID)?.[0]; assert.ok(original);
      const failed = { ...original, status: "failed", width: 0, height: 0, opaque: false, last_error_code: unsafeErrorProbe };
      app.state.variants.set(assetID, [failed]); response = { body: failed, requiredResponse: false };
    }
    if (!held) return response;
    held.used = true; held.entered.resolve();
    return { ...response, waitUntil: held.release.promise, requiredResponse: false };
  };
  return { app, route, requests, gate, diagnostic, observeBrowserRoutes, mark, checkpoint, setupPaths,
    count: (method: string, path: RegExp) => requests.filter(item => item.method === method && path.test(item.pathname)).length,
    releaseAll: () => { for (const value of gates) value.release.resolve(); },
  };
}

export async function selectImage(browser: BrowserHarness, kind: ImageKind, name: string, index = 0) {
  await browser.evaluate(`(() => {
    const names = ${JSON.stringify(kind === "cover" ? ["蓋画像をアップロード", "Upload cover image"] : ["背景画像をアップロード", "Upload background image"])};
    const label = [...document.querySelectorAll('#create-stream-visual label')].find(el => names.some(name => el.textContent?.includes(name)));
    const input = label?.querySelector('input[type=file]');
    if (!(input instanceof HTMLInputElement) || input.disabled) throw Error('image upload disabled');
    const canvas=document.createElement('canvas');canvas.width=640;canvas.height=360;
    const context=canvas.getContext('2d');if(!context)throw Error('PNG fixture context unavailable');
    context.fillStyle=${JSON.stringify(["#315f8f", "#70458b", "#287b69", "#aa663c"][index % 4])};context.fillRect(0,0,640,360);
    const bytes=Uint8Array.from(atob(canvas.toDataURL('image/png').split(',')[1]),c=>c.charCodeAt(0));
    const files=new DataTransfer();files.items.add(new File([bytes],${JSON.stringify(name)},{type:'image/png'}));
    input.files=files.files;input.dispatchEvent(new Event('change',{bubbles:true}));
    if(input.value!=='')throw Error('production file input must permit same-file reselection');
    return true;
  })()`);
}

export async function cancelImageSelection(browser: BrowserHarness, kind: ImageKind) {
  await browser.evaluate(`(() => {
    const name=${JSON.stringify(kind === "cover" ? "cover" : "background")};
    const input=[...document.querySelectorAll('#create-stream-visual input[type=file]')].find(el=>name==='cover'?/蓋画像|cover image/i.test(el.closest('label')?.textContent||''):/背景画像|background image/i.test(el.closest('label')?.textContent||''));
    if(!(input instanceof HTMLInputElement)||input.disabled)throw Error('cancel fixture input unavailable');
    input.files=new DataTransfer().files;input.dispatchEvent(new Event('change',{bubbles:true}));return true;
  })()`);
}
