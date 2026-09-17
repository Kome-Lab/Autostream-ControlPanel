import type { TestContext } from "node:test";
import assert from "node:assert/strict";
import { BrowserHarness } from "./helpers/browser-harness.mts";
import { type BrowserRouteFixture, currentUser } from "./ui-browser-fixture.mts";
import { setStoredDisplay } from "./ui-browser-navigation-helpers.mts";
import { clickVisible } from "./ui-regression/visible-trigger.mts";



export async function runAccountAppearanceScenario(t: TestContext, rawBrowser: BrowserHarness, server: { baseUrl: string }, fixture: Pick<BrowserRouteFixture, "uiPreferenceMethods" | "authResponse" | "uiPreferenceBodies" | "uiPreferenceResponse" | "uiPreferenceWriteResponse">) {

  const diagnostic=accountAppearanceDiagnostics(rawBrowser,()=>fixture.uiPreferenceMethods);
  const browser=diagnostic.browser;
  try {
    await t.test("Account appearance persists 12 themes and 3 modes with DB fallback and save rollback", async () => {
		const preferenceRequestCount = (method: "GET" | "PUT") => fixture.uiPreferenceMethods.filter((value) => value === method).length;
		const waitForPreferenceSettlement = async (method: "GET" | "PUT", minimumRequests: number) => {
			assert.ok(minimumRequests > 0, "settlement needs an observed request phase");
			await browser.waitFor("true", () => preferenceRequestCount(method) >= minimumRequests, "UI preference request did not arrive");
			await browser.waitForRequestHandlersIdle({ pathname: "/account/preferences/ui", method });
		};
		for (const method of ["GET", "PUT"] as const) {
			const previousRequests = preferenceRequestCount(method);
			if (previousRequests > 0) await waitForPreferenceSettlement(method, previousRequests);
		}
		fixture.authResponse = { status: 401, body: { code: "unauthorized" } };
		fixture.uiPreferenceMethods = [];
		fixture.uiPreferenceBodies = [];
		await browser.navigate(`${server.baseUrl}/login/`);
		await browser.waitFor(`location.pathname === '/login/'`, Boolean, "login route was not retained");
		assert.equal(fixture.uiPreferenceMethods.filter((method) => method === "GET").length, 0, "public login must not query authenticated UI preferences");

		fixture.authResponse = { body: currentUser };
		fixture.uiPreferenceResponse = { body: { theme_id: "autostream", color_mode: "system", revision: 0 } };
		fixture.uiPreferenceWriteResponse = { body: { theme_id: "autostream", color_mode: "dark", revision: 1 } };
		await browser.evaluate(`localStorage.removeItem('autostream.ui_preference'); localStorage.setItem('autostream.theme', 'dark'); true`);
		await browser.navigate(`${server.baseUrl}/admin/account/`);
		await browser.waitFor(
			`document.documentElement.dataset.theme + '/' + document.documentElement.dataset.colorMode`,
			(value: string) => value === "autostream/system",
			"DB preference must win over the retained legacy storage value",
		);
		await browser.waitFor(
			`(localStorage.getItem('autostream.ui_preference') || '').includes('"color_mode":"system"')`,
			Boolean,
			"DB-backed bootstrap mirror did not reflect the current preference",
		);
		assert.equal(fixture.uiPreferenceMethods.filter((method) => method === "PUT").length, 0, "retired storage must not cause a migration write");
		assert.equal(await browser.evaluate(`localStorage.getItem('autostream.theme')`), "dark", "retained migration data must not be deleted");
		await waitForPreferenceSettlement("GET", 1);

		fixture.uiPreferenceResponse = { body: { theme_id: "ocean", color_mode: "dark", revision: 4 }, delayMs: 1_200 };
    fixture.uiPreferenceWriteResponse = { body: { theme_id: "violet", color_mode: "light", revision: 5 } };
    fixture.uiPreferenceMethods = [];
		fixture.uiPreferenceBodies = [];
    await browser.setViewport(1440, 1000);
    await setStoredDisplay(browser, "ja", "light");
		await browser.evaluate(`localStorage.setItem('autostream.ui_preference', JSON.stringify({ theme_id: 'cyan', color_mode: 'light' })); true`);
		await diagnostic.bootstrap("mirror-written");
		await diagnostic.bootstrap("before-navigate");
		await browser.navigate(`${server.baseUrl}/admin/account/`);
		try {
		assert.equal(
			await diagnostic.bootstrap("after-navigate"),
			"cyan/light",
			"external pre-hydration bootstrap did not apply the validated local mirror before the DB response",
		);
		} catch (error) { await diagnostic.failed(error); }
		diagnostic.bootstrapComplete();
		await browser.waitFor(
      `document.documentElement.dataset.theme + '/' + document.documentElement.dataset.colorMode`,
      (value: string) => value === "ocean/dark",
			"DB appearance did not override the pre-hydration mirror",
		);
		await waitForPreferenceSettlement("GET", 1);
		fixture.uiPreferenceResponse = { body: { theme_id: "ocean", color_mode: "dark", revision: 4 } };
    await clickVisible(browser, 'main [role="tablist"] [role="tab"]', /^外観$/);
    const matrix = await browser.waitFor(
      `(() => ({ themes: document.querySelectorAll('[role="radiogroup"][aria-label="配色テーマ"] [role="radio"]').length, modes: document.querySelectorAll('[role="radiogroup"][aria-label="表示モード"] [role="radio"]').length }))()`,
      (value: { themes: number; modes: number }) => value.themes === 12 && value.modes === 3,
      "appearance matrix was not rendered",
    );
    assert.deepEqual(matrix, { themes: 12, modes: 3 });
    await browser.clickSelector('[aria-label="Violetテーマ"]');
    await browser.clickSelector('[aria-label="ライトモード"]');
    await browser.waitFor(
      `document.documentElement.dataset.theme + '/' + document.documentElement.dataset.colorMode + '/' + document.documentElement.classList.contains('dark')`,
      (value: string) => value === "violet/light/false",
      "appearance preview was not immediate",
    );
		assert.match(await browser.evaluate<string>(`localStorage.getItem('autostream.ui_preference') || ''`), /"theme_id":"ocean"/, "unsaved appearance preview replaced the DB bootstrap mirror");
    await browser.clickSelector('[aria-label="表示設定を保存"]');
    await browser.waitFor(
      `document.body.textContent?.includes('表示設定を保存しました。') === true`,
      Boolean,
      "appearance save did not complete",
    );
    assert.equal(fixture.uiPreferenceMethods.filter((method) => method === "PUT").length, 1, "appearance save must send exactly one PUT");
		assert.match(await browser.evaluate<string>(`localStorage.getItem('autostream.ui_preference') || ''`), /"theme_id":"violet"/, "saved DB appearance did not refresh the bootstrap mirror");
		await waitForPreferenceSettlement("PUT", 1);

    fixture.uiPreferenceWriteResponse = { status: 409, body: { code: "revision_conflict" } };
    await browser.clickSelector('[aria-label="Oceanテーマ"]');
    await browser.clickSelector('[aria-label="ダークモード"]');
    await browser.clickSelector('[aria-label="表示設定を保存"]');
    await browser.waitFor(
      `document.documentElement.dataset.theme + '/' + document.documentElement.dataset.colorMode`,
      (value: string) => value === "violet/light",
      "failed save did not roll back the displayed preference",
    );
    assert.equal(fixture.uiPreferenceMethods.filter((method) => method === "PUT").length, 2, "failed save must not retry automatically");
		await waitForPreferenceSettlement("PUT", 2);
		await waitForPreferenceSettlement("GET", preferenceRequestCount("GET"));

    fixture.uiPreferenceResponse = { body: { theme_id: "violet", color_mode: "light", revision: 5 } };
		const savedGet = preferenceRequestCount("GET") + 1;
    await browser.reload();
    await browser.waitFor(
      `document.documentElement.dataset.theme + '/' + document.documentElement.dataset.colorMode`,
      (value: string) => value === "violet/light",
      "saved DB appearance did not persist across reload",
    );
		await waitForPreferenceSettlement("GET", savedGet);
    const putsBeforeFallback = fixture.uiPreferenceMethods.filter((method) => method === "PUT").length;
    fixture.uiPreferenceResponse = { body: { theme_id: "future-theme", color_mode: "infrared", revision: 6, fallback: true } };
		const fallbackGet = preferenceRequestCount("GET") + 1;
    await browser.reload();
    await browser.waitFor(
      `document.documentElement.dataset.theme + '/' + document.documentElement.dataset.colorMode`,
      (value: string) => value === "autostream/system",
      "unknown stored appearance did not render the safe fallback",
    );
    assert.equal(fixture.uiPreferenceMethods.filter((method) => method === "PUT").length, putsBeforeFallback, "safe fallback must not overwrite DB automatically");
		await waitForPreferenceSettlement("GET", fallbackGet);

		fixture.uiPreferenceResponse = { body: { theme_id: "violet", color_mode: "light", revision: 7 } };
		await setStoredDisplay(browser, "en", "light");
		const translatedGet = preferenceRequestCount("GET") + 1;
		diagnostic.settled("GET", preferenceRequestCount("GET"));diagnostic.settled("PUT", 2);
		await diagnostic.observe("before-reload");
		await browser.reload();
		await diagnostic.observe("after-reload");
		await clickVisible(browser, 'html[lang="en"] main [role="tablist"] [role="tab"]', /^Appearance$/);
		await diagnostic.observe("after-tab");
		await browser.waitFor(`document.querySelector('[aria-label="Violet theme"]') !== null`, Boolean, "translated theme accessible name missing");
		await browser.waitFor(
			`document.documentElement.dataset.theme + '/' + document.documentElement.dataset.colorMode`,
			(value: string) => value === "violet/light",
			"translated account did not consume its DB preference",
		);
		await waitForPreferenceSettlement("GET", translatedGet);
		diagnostic.settled("GET", translatedGet);await diagnostic.observe("preference-settled");
		await browser.evaluate(`document.querySelector('[aria-label="Ocean theme"]')?.focus(); true`);
		await browser.pressKey("ArrowRight");
		await browser.waitFor(
			`document.activeElement?.getAttribute('aria-label') + '/' + document.querySelector('[aria-label="Cyan theme"]')?.getAttribute('aria-checked')`,
			(value: string) => value === "Cyan theme/true",
			"theme radiogroup did not implement roving Arrow-key selection",
		);
		await browser.evaluate(`document.querySelector('[aria-label="System mode"]')?.focus(); true`);
		await browser.pressKey("End");
		await browser.waitFor(
			`document.activeElement?.getAttribute('aria-label') + '/' + document.querySelector('[aria-label="Dark mode"]')?.getAttribute('aria-checked')`,
			(value: string) => value === "Dark mode/true",
			"mode radiogroup did not implement roving Home/End selection",
		);
		await waitForPreferenceSettlement("GET", translatedGet);
		diagnostic.settled("GET", translatedGet);await diagnostic.observe("preference-settled");
		await waitForPreferenceSettlement("PUT", 2);
		diagnostic.settled("PUT", 2);await diagnostic.finish();
    });
  } catch (error) { await diagnostic.failed(error); } finally { await diagnostic.dispose(); }
}

export const accountAppearancePointerStart = (x: number, y: number) => `(() => {
  const target=globalThis.__uiScenarioTarget,record={target,planned:[${JSON.stringify(Number.isFinite(x)?x:null)},${JSON.stringify(Number.isFinite(y)?y:null)}],events:[]};
  const finite=n=>Number.isFinite(n)?Math.max(-100000,Math.min(100000,Math.round(n))):null;
  const rect=target?.getBoundingClientRect();record.rect=rect?[rect.left,rect.top,rect.width,rect.height].map(finite):null;
  record.marked=!!target&&document.querySelector('[data-ui-scenario-target]')===target;
  record.connected=!!target?.isConnected;record.role=target?.getAttribute('role')==='tab';
  const hit=document.elementFromPoint(record.planned[0],record.planned[1]);record.plannedHit=!!target&&!!hit&&(hit===target||target.contains(hit));
  record.listener=event=>{if(record.events.length>=6)return;
    const hit=document.elementFromPoint(event.clientX,event.clientY);
    record.events.push({kind:['mousedown','mouseup','click'].includes(event.type)?event.type:'other',trusted:event.isTrusted===true,
      point:[finite(event.clientX),finite(event.clientY)],sameTarget:globalThis.__uiScenarioTarget===target,connected:!!target?.isConnected,
      eventInside:!!target&&!!event.target&&(event.target===target||target.contains(event.target)),hitInside:!!target&&!!hit&&(hit===target||target.contains(hit))});
  };
  globalThis.__uiAccountPointer017=record;
  for(const type of ['mousedown','mouseup','click'])document.addEventListener(type,record.listener,true);
  return true;
})()`;
export const accountAppearancePointerStop = `(() => {const r=globalThis.__uiAccountPointer017;if(r)for(const type of ['mousedown','mouseup','click'])document.removeEventListener(type,r.listener,true);return true;})()`;
export const accountAppearancePointerDispose = `(() => {const r=globalThis.__uiAccountPointer017;if(r)for(const type of ['mousedown','mouseup','click'])document.removeEventListener(type,r.listener,true);delete globalThis.__uiAccountPointer017;return true;})()`;

export const accountAppearanceDiagnosticExpression = `(() => {
  const visible=e=>!!e?.getClientRects().length&&getComputedStyle(e).visibility!=='hidden'&&getComputedStyle(e).display!=='none';
  const tabs=[...document.querySelectorAll('main [role="tablist"] [role="tab"]')].filter(e=>visible(e)&&e.textContent?.trim()==='Appearance'),tab=tabs.length===1?tabs[0]:null;
  const target=tab?.getAttribute('aria-controls'),panels=target?[...document.querySelectorAll('[role="tabpanel"]')].filter(e=>e.id===target):[];
  const themes=[...document.querySelectorAll('[role="radiogroup"][aria-label="Color theme"] [role="radio"]')];
  const pointer=globalThis.__uiAccountPointer017;
  return {pointer:pointer?{marked:pointer.marked,connected:pointer.connected,role:pointer.role,planned:pointer.planned,rect:pointer.rect,plannedHit:pointer.plannedHit,events:pointer.events,sameTarget:tab===pointer.target,targetConnected:!!pointer.target?.isConnected}:null,lang:['ja','en'].includes(document.documentElement.lang)?document.documentElement.lang:'other',tabs:tabs.length,enabled:!!tab&&!tab.disabled&&tab.getAttribute('aria-disabled')!=='true',
    selected:tab?.getAttribute('aria-selected')==='true',panels:panels.length,visiblePanels:panels.filter(visible).length,themes:themes.length,
    violet:document.querySelectorAll('[role="radio"][aria-label="Violet theme"]').length};
})()`;
type AccountPointer = {marked:boolean;connected:boolean;role:boolean;planned:number[];rect:number[]|null;plannedHit:boolean;sameTarget:boolean;targetConnected:boolean;events:{kind:string;trusted:boolean;point:number[];sameTarget:boolean;connected:boolean;eventInside:boolean;hitInside:boolean}[]};
type AccountDiagnostic = { pointer?:AccountPointer|null; lang:string; tabs:number; enabled:boolean; selected:boolean; panels:number; visiblePanels:number; themes:number; violet:number };

const bootstrapThemes = ["autostream","slate","ocean","cyan","indigo","violet","magenta","rose","crimson","amber","emerald","monochrome"];
const bootstrapModes = ["system","light","dark"];
const bootstrapPhases = ["mirror-written","before-navigate","after-navigate"] as const;
type BootstrapPhase = typeof bootstrapPhases[number];
export const accountBootstrapExpression = `(() => {
  const themes=${JSON.stringify(bootstrapThemes)},modes=${JSON.stringify(bootstrapModes)};
  const theme=themes.includes(document.documentElement.dataset.theme)?document.documentElement.dataset.theme:'other';
  const mode=modes.includes(document.documentElement.dataset.colorMode)?document.documentElement.dataset.colorMode:'other';
  let mirror='missing';try {const raw=localStorage.getItem('autostream.ui_preference');if(raw!==null){mirror='invalid';if(raw.length<=2048){try{const parsed=JSON.parse(raw);if(parsed&&typeof parsed==='object'&&!Array.isArray(parsed)&&themes.includes(parsed.theme_id)&&modes.includes(parsed.color_mode))mirror=parsed.theme_id==='cyan'&&parsed.color_mode==='light'?'valid-cyan-light':'valid-other';}catch{mirror='invalid';}}}}catch{mirror='unavailable';}
  const fixed=value=>{if(typeof value!=='string'||value.length>2048)return false;try{const url=new URL(value,location.href);return url.origin===location.origin&&url.pathname==='/theme-bootstrap.js'&&!url.search&&!url.hash;}catch{return false;}};
  const scripts=[...document.querySelectorAll('script[src]')],links=[...document.querySelectorAll('link[rel=preload][href]')];
  const queue=globalThis.__next_s,resources=typeof performance?.getEntriesByType==='function'?performance.getEntriesByType('resource'):null;
  return {theme,mode,mirror,readyState:['loading','interactive','complete'].includes(document.readyState)?document.readyState:'other',
    timeOrigin:Number.isFinite(performance?.timeOrigin)?performance.timeOrigin:null,
    scripts:scripts.slice(0,128).filter(e=>fixed(e.getAttribute('src'))).length,preloads:links.slice(0,128).filter(e=>fixed(e.getAttribute('href'))).length,
    queue:Array.isArray(queue)?queue.slice(0,128).filter(e=>Array.isArray(e)&&fixed(e[0])).length:null,
    resources:Array.isArray(resources)?resources.slice(0,128).filter(e=>fixed(e.name)).length:null,
    scanLimited:scripts.length>128||links.length>128||Array.isArray(queue)&&queue.length>128||Array.isArray(resources)&&resources.length>128};
})()`;
type BootstrapObservation = {theme:string;mode:string;mirror:string;readyState:string;timeOrigin:number|null;scripts:number;preloads:number;queue:number|null;resources:number|null;scanLimited:boolean};

export function accountAppearanceDiagnostics(browser: BrowserHarness, methods:()=>string[], write:(line:string)=>void=console.log) {
  const observations:{phase:string;value:AccountDiagnostic;get:number;put:number;getSettled:number;putSettled:number}[]=[],diagnosticErrors:unknown[]=[];
  const settledRequests={GET:0,PUT:0};let phase:string|undefined,reported=false,reportedFailure:unknown;
  const lastSettlement:Partial<Record<"GET"|"PUT",{count:number;batch:string[]}>>={};
  const settle=(method:"GET"|"PUT",count:number)=>{settledRequests[method]=count;lastSettlement[method]={count,batch:methods()};};
  const count=(n:number|null)=>n!==null&&Number.isFinite(n)?Math.min(1024,Math.max(0,Math.floor(n))):null;
  const safePhase=(value:string|undefined)=>[...bootstrapPhases,"before-reload","after-reload","after-tab","preference-settled"].includes(value||"")?value:"other";
  const bootstrapRows:{phase:BootstrapPhase;value:BootstrapObservation;documentChanged:boolean|null;get:number;put:number;lastGetSettlement:{count:number|null;sameBatch:boolean}|null;lastPutSettlement:{count:number|null;sameBatch:boolean}|null}[]=[];
  let documentOrigin:number|null=null;
  const bootstrap=async(step:BootstrapPhase):Promise<string|undefined>=>{phase=step;
    try {const value=await browser.evaluate<BootstrapObservation>(accountBootstrapExpression),batch=methods();
      const settlement=(method:"GET"|"PUT")=>lastSettlement[method]?{count:count(lastSettlement[method]!.count),sameBatch:lastSettlement[method]!.batch===batch}:null;
      const changed=documentOrigin===null||value.timeOrigin===null?null:documentOrigin!==value.timeOrigin;
      if(step!=="after-navigate")documentOrigin=value.timeOrigin;
      bootstrapRows.push({phase:step,value,documentChanged:changed,get:batch.filter(v=>v==="GET").length,put:batch.filter(v=>v==="PUT").length,lastGetSettlement:settlement("GET"),lastPutSettlement:settlement("PUT")});
      return value.theme+"/"+value.mode;
    }catch(error){if(step==="after-navigate")return failed(error);diagnosticErrors.push(error);}
  };
  const observe=async(step:string)=>{phase=step;try{const value=await browser.evaluate<AccountDiagnostic>(accountAppearanceDiagnosticExpression);
    observations.push({phase,value,get:methods().filter(v=>v==="GET").length,put:methods().filter(v=>v==="PUT").length,getSettled:settledRequests.GET,putSettled:settledRequests.PUT});
  }catch(error){diagnosticErrors.push(error);}};
  const failed=async(original:unknown):Promise<never>=>{
    if(reported)throw reportedFailure;
    if(!phase)throw original;
    reported=true;
    const bootstrapFailure=bootstrapPhases.some(step=>step===phase);
    if(!bootstrapFailure)await observe(phase);
    try {
      const point=(value:number[]|null|undefined)=>value?.slice(0,4).map(n=>Number.isFinite(n)?Math.max(-100000,Math.min(100000,Math.round(n))):null)||null;
      const pointer=observations.at(-1)?.value.pointer;
      const payload={schemaVersion:3,code:"D013-ACCOUNT",detailCode:bootstrapFailure?"D019-ACCOUNT-BOOTSTRAP":"D017-ACCOUNT",phase:safePhase(phase),diagnosticFailed:diagnosticErrors.length>0,
        bootstrap:bootstrapRows.slice(-3).map(row=>({phase:row.phase,theme:bootstrapThemes.includes(row.value.theme)?row.value.theme:"other",mode:bootstrapModes.includes(row.value.mode)?row.value.mode:"other",
          mirror:["valid-cyan-light","valid-other","missing","invalid","unavailable"].includes(row.value.mirror)?row.value.mirror:"invalid",readyState:["loading","interactive","complete"].includes(row.value.readyState)?row.value.readyState:"other",
          documentChanged:row.documentChanged,scripts:count(row.value.scripts),preloads:count(row.value.preloads),queue:count(row.value.queue),resources:count(row.value.resources),scanLimited:row.value.scanLimited===true,execution:"UNOBSERVED",
          get:count(row.get),put:count(row.put),lastGetSettlement:row.lastGetSettlement,lastPutSettlement:row.lastPutSettlement})),
        pointer:pointer?{marked:pointer.marked===true,connected:pointer.connected===true,role:pointer.role===true,planned:point(pointer.planned),rect:point(pointer.rect),plannedHit:pointer.plannedHit===true,sameTarget:pointer.sameTarget===true,targetConnected:pointer.targetConnected===true,
          events:pointer.events.slice(0,6).map(e=>({kind:["mousedown","mouseup","click"].includes(e.kind)?e.kind:"other",trusted:e.trusted===true,point:point(e.point),sameTarget:e.sameTarget===true,connected:e.connected===true,eventInside:e.eventInside===true,hitInside:e.hitInside===true}))}:null,
        observations:observations.slice(-6).map(row=>({phase:safePhase(row.phase),lang:["ja","en"].includes(row.value.lang)?row.value.lang:"other",
          tabs:count(row.value.tabs),enabled:row.value.enabled===true,selected:row.value.selected===true,panels:count(row.value.panels),
          visiblePanels:count(row.value.visiblePanels),themes:count(row.value.themes),violet:count(row.value.violet),
          get:count(row.get),put:count(row.put),getSettled:count(row.getSettled),putSettled:count(row.putSettled)}))};
      const json=JSON.stringify(payload);assert.ok(Buffer.byteLength(json)<=4096);write("UI_BROWSER_DIAGNOSTIC_013 "+json);
    }catch(error){diagnosticErrors.push(error);}
    reportedFailure=diagnosticErrors.length?new AggregateError([original,...diagnosticErrors],"Account failure and diagnostic failure",{cause:original}):original;
    throw reportedFailure;
  };
  // A local diagnostic view of the same harness. Calls, arguments, this-owner,
  // deadlines and native input are forwarded unchanged; no method is replaced.
  const observed=new Proxy(browser,{get(target,property){
    const value:unknown=Reflect.get(target,property,target);
    if(typeof value!=="function")return value;
    if(!["waitFor","waitForRequestHandlersIdle","reload","evaluate","clickAt","pressKey"].includes(String(property)))return value.bind(target);
    return (...args:unknown[])=>{
      const filter=args[0];
      if(property==="waitForRequestHandlersIdle"&&filter&&typeof filter==="object"&&"pathname" in filter&&filter.pathname==="/account/preferences/ui"&&"method" in filter&&(filter.method==="GET"||filter.method==="PUT")){
        const method=filter.method;return Promise.resolve(Reflect.apply(value,target,args)).then(result=>{settle(method,methods().filter(v=>v===method).length);return result;}).catch(error=>{if(phase)return failed(error);throw error;});
      }
      if(!phase)return Reflect.apply(value,target,args);
      if(property==="clickAt"&&phase==="after-reload")return (async()=>{
        try{await browser.evaluate(accountAppearancePointerStart(Number(args[0]),Number(args[1])));}catch(error){diagnosticErrors.push(error);}
        let result:unknown,primary:unknown,failedInput=false;
        try{result=await Reflect.apply(value,target,args);}catch(error){primary=error;failedInput=true;}
        finally{try{await browser.evaluate(accountAppearancePointerStop);}catch(error){diagnosticErrors.push(error);}}
        if(failedInput)return failed(primary);return result;
      })();
      try{return Promise.resolve(Reflect.apply(value,target,args)).catch(failed);}catch(error){return failed(error);}
    };
  }});
  return {browser:observed,observe,bootstrap,bootstrapComplete:()=>{phase=undefined;},failed,dispose:async()=>{try{await browser.evaluate(accountAppearancePointerDispose);}catch(error){if(reported)throw new AggregateError([reportedFailure,error],"Account diagnostic cleanup failed",{cause:reportedFailure});throw error;}},settled:settle,
    finish:async()=>{if(diagnosticErrors.length)await failed(new AggregateError(diagnosticErrors,"Account diagnostic observation failed"));}};
}
