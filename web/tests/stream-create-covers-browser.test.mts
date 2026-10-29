import test from "node:test";
import assert from "node:assert/strict";
import { mkdirSync, writeFileSync } from "node:fs";
import { resolve } from "node:path";
import { BrowserHarness, ensureWebServer } from "./helpers/browser-harness.mts";
import { withTimeout } from "./helpers/browser-process-timing.mts";
import { createBrowserRouteFixture, controlPlatformStream, healthyRows, webRoot } from "./ui-browser-fixture.mts";
import { deferred, scrollSelectorIntoView, waitForAnimationFrames } from "./ui-browser-navigation-helpers.mts";
import { clickDisabledVisible, clickVisible } from "./ui-regression/visible-trigger.mts";
import { assertCardLayout, candidate, capture, evidenceDirectory, fileNames, fixtureRoutes, select, upload, visual, waitCards, widths } from "./stream-create-covers-browser-fixture.mts";

test("Stream create and all cover candidates operate in real desktop and mobile browsers", { timeout: 300_000 }, async (t) => {
  const server = await ensureWebServer(webRoot, process.env.AUTOSTREAM_BROWSER_BASE_URL || "http://127.0.0.1:3013");
  const browserPromise=(async()=>{
    // Compile the exact route before its bounded real-browser navigation starts.
    const warmup=await fetch(`${server.baseUrl}/admin/streams/`,{signal:AbortSignal.timeout(60_000)});
    assert.equal(warmup.status,200,"the target dev route must be ready before browser navigation");
    return BrowserHarness.launch();
  })();
  t.after(async()=>{const owned=await browserPromise.catch(()=>undefined);await owned?.close();await server.close();});
  const browser=await browserPromise;
  if (evidenceDirectory) {
    mkdirSync(evidenceDirectory, { recursive: true });
    writeFileSync(resolve(evidenceDirectory, "browser-version.json"), JSON.stringify(await browser.browserVersion(), null, 2));
  }
  const app = fixtureRoutes();
  const visualPath = `/streams/${controlPlatformStream.id}/visual-settings`, coverPath = `/streams/${controlPlatformStream.id}/video-cover-state`;
  const visualMethods: string[] = [];
  const visualMethodCount = (method: string) => visualMethods.filter(value => value === method).length;
  let visualPUTEntered: (() => void) | undefined;
  const fixture = createBrowserRouteFixture(browser, request => {
    const response = app.route(request);
    if (new URL(request.url).pathname === visualPath) {
      visualMethods.push(request.method);
      if (request.method === "PUT") visualPUTEntered?.();
    }
    return response;
  });
  fixture.healthResponse = { body: [...healthyRows, {
    id: "bot-one", service_id: "bot-one", service_type: "discord_bot", service_name: "Selected BOT", status: "online", health_status: "healthy",
  }] };
  await browser.configureDeterministicDocument({ source: `localStorage.setItem('autostream.controlPanel.locale','ja'); sessionStorage.setItem('autostream.csrf_token','browser-fixture-csrf');`, timezone: "Asia/Tokyo", locale: "ja-JP" });
  // Create has no visual-settings GET: that query is enabled only for editing.
  // These are its actual mounted auth/list, option, preset and service readers.
  const createGETs = ["/auth/me", "/streams", "/service-health", "/discord/configs", "/youtube/outputs", "/profiles/encoder", "/profiles/caption", "/profiles/overlay", "/profiles/archive", "/discord/target-presets", "/video-cover-presets"];
  const snapshotGETs = (paths: readonly string[]) => new Map(paths.map(path => [path, browser.responses.get(path) || 0]));
  const waitGETIdle = (paths: readonly string[]) => Promise.all(paths.map(pathname => browser.waitForRequestHandlersIdle({ pathname, method: "GET" })));
  const waitGETResponses = async (paths: readonly string[], baseline: ReadonlyMap<string, number>) => {
    const completed = () => paths.every(path => (browser.responses.get(path) || 0) >= (baseline.get(path) || 0) + 1);
    const deadline = Date.now() + 20_000;
    while (!completed() && Date.now() < deadline) { browser.assertNoFatalError(); await new Promise<void>(resolve => setTimeout(resolve, 25)); }
    browser.assertNoFatalError();
    assert.equal(completed(), true, "every required mounted GET must complete after this navigation or controlled query event");
    await waitGETIdle(paths);
  };
  const navigationGETs = [...createGETs, visualPath, coverPath];
  let navigationBaseline = snapshotGETs(navigationGETs);
  const settleGETBoundary = async () => {
    await browser.waitForRequestHandlersIdle({ method: "GET" });
    await waitForAnimationFrames(browser);
    await browser.waitForRequestHandlersIdle({ method: "GET" });
  };
  const waitClickCommit = (selector: string) => browser.waitFor(`(() => {const buttons=[...document.querySelectorAll(${JSON.stringify(selector)})].filter(button=>button.getBoundingClientRect().width>0);if(buttons.length!==1)return false;const props=Object.keys(buttons[0]).find(key=>key.startsWith('__reactProps$'));return typeof buttons[0][props]?.onClick==='function';})()`, Boolean, "the actual localized navigation trigger must commit");
  const navigateList = async (mode: "navigate" | "reload") => {
    await settleGETBoundary();
    navigationBaseline = snapshotGETs(navigationGETs);
    const previousBootstrapObservation = fixture.observeBootstrapCancellation;
    fixture.observeBootstrapCancellation = true;
    try {
      if (mode === "reload") await browser.reload();
      else await browser.navigate(`${server.baseUrl}/admin/streams/`);
      await waitGETResponses(["/auth/me", "/streams"], navigationBaseline);
      await browser.waitFor(`document.documentElement.lang==='ja'&&!!document.querySelector('main')`, Boolean, "the localized stream list must commit after this navigation");
      await settleGETBoundary();
    } finally {
      fixture.observeBootstrapCancellation = previousBootstrapObservation;
    }
  };
  const openEdit = async () => {
    const selector = 'button[aria-label$=" を編集"]';
    await waitClickCommit(selector); await clickVisible(browser, selector);
    await browser.waitFor(`!!document.querySelector('[role="dialog"] form')`, Boolean, "existing edit Sheet missing");
    await waitGETResponses([...createGETs, visualPath], navigationBaseline);
    await browser.waitFor(`(() => {const form=document.querySelector('[role="dialog"] form');const props=form&&Object.keys(form).find(key=>key.startsWith('__reactProps$'));return !!form&&typeof form[props]?.onSubmit==='function';})()`, Boolean, "the real edit form must commit after visual settings load");
    await settleGETBoundary();
  };
  const openDetail = async () => {
    const selector = 'button[aria-label="詳細"]';
    await waitClickCommit(selector); await clickVisible(browser, selector);
    // The editor may already have loaded this visual snapshot in the same
    // document. Require this navigation's response, not an invented extra GET.
    await waitGETResponses(["/auth/me", "/streams", "/service-health", visualPath, coverPath], navigationBaseline);
    await waitClickCommit('button[aria-label="Video Coverを表示"]');
    await settleGETBoundary();
  };
  const closeSheet = async (message: string) => {
    await settleGETBoundary();
    await browser.pressKey("Escape");
    await browser.waitFor(`!document.querySelector('[role="dialog"]')`, Boolean, message);
    await settleGETBoundary();
  };
  const settleCreate = async () => {
    await browser.waitFor(`(() => {const view=document.querySelector('[data-stream-create-view]'),form=view?.querySelector('#create-stream form'),back=view?.querySelector('button[aria-label="配信枠の一覧へ戻る"]');const props=element=>element&&Object.keys(element).find(key=>key.startsWith('__reactProps$'));return document.documentElement.lang==='ja'&&!!form&&!!back&&typeof form[props(form)]?.onSubmit==='function'&&typeof back[props(back)]?.onClick==='function';})()`, Boolean, "localized create form and Back handlers must commit before navigation assertions");
    await waitGETIdle(createGETs);
    await waitForAnimationFrames(browser);
    await waitGETIdle(createGETs);
  };
  const openCreate = async () => {
    const location=await browser!.evaluate<string>(`location.origin+location.pathname`);
    await navigateList(location===`${server.baseUrl}/admin/streams/` ? "reload" : "navigate");
    await browser!.waitFor(`(() => {const buttons=[...document.querySelectorAll('main button')].filter(button=>button.textContent?.trim()==='配信枠を作成'&&button.getBoundingClientRect().width>0);if(buttons.length!==1||buttons[0].disabled||document.querySelector('[data-stream-create-view]'))return false;const props=Object.keys(buttons[0]).find(key=>key.startsWith('__reactProps$'));return typeof buttons[0][props]?.onClick==='function';})()`,Boolean,"localized stream list create handler missing");
    await clickVisible(browser!,'main button',/^配信枠を作成$/);
    await browser!.waitFor(`!!document.querySelector('[data-stream-create-view] #create-stream')`, Boolean, "dedicated creation view missing");
    await waitGETResponses(createGETs, navigationBaseline);
    await settleCreate();
  };
  const scenario = async (name: string, body: () => Promise<void>) => {
    let failure: unknown;
    await t.test(name, async () => { try { await body(); } catch (error) { failure=error; try { await capture(browser!,"failure"); } catch { /* Preserve the primary observable failure. */ } throw error; } });
    if (failure) throw failure; // A failed scenario cannot contaminate later navigation proof.
  };
  const assertDraftMutationsBlocked=async(variant:string)=>{
    const deletes=app.state.deletes.length,uploads=app.state.uploads;
    await clickDisabledVisible(browser!,`[data-cover-candidate="${variant}"] button:last-child`);
    await assert.rejects(()=>upload(browser!,"must-not-upload.png"),/cover upload disabled/);
    await waitForAnimationFrames(browser!);
    assert.equal(app.state.deletes.length,deletes,"pending/unknown save must not delete an asset");
    assert.equal(app.state.uploads,uploads,"pending/unknown save must not upload an asset");
  };

  await scenario("full page creation, required nodes, dirty cancellation, and focus", async () => {
    for (const width of widths) {
      await browser!.setViewport(width, 900);
      await openCreate();
      const view = await browser!.evaluate<{ dialogs: number; width: number; available: number; focus: boolean; fields: boolean; overflow: number }>(`(() => {
        const view=document.querySelector('[data-stream-create-view]'),main=view.closest('main');
        return { dialogs:document.querySelectorAll('[role=dialog]').length,width:view.getBoundingClientRect().width,available:main.getBoundingClientRect().width,focus:view.contains(document.activeElement),
          fields:['担当Worker Node','担当Encoder Node','Discord BOT設定','YouTube出力','録画プロファイル','ビジュアル設定','Encoderライブ調整'].every(value=>view.textContent.includes(value)),overflow:document.documentElement.scrollWidth-document.documentElement.clientWidth };
      })()`);
      assert.equal(view.dialogs, 0); assert.equal(view.focus, true); assert.equal(view.fields, true); assert.equal(view.overflow, 0);
      assert.ok(view.width >= view.available * 0.85, "create must occupy the available main content width");
      assert.equal(await browser!.evaluate(`location.pathname`), "/admin/streams/");
      assert.match(await browser!.evaluate<string>(`document.title`), /AutoStream/i);
      assert.equal(await browser!.evaluate(`document.body.textContent?.includes('Build Error') || document.body.textContent?.includes('Runtime Error')`), false, "framework error overlay must be absent");
      await browser!.pressTab("forward");
      assert.equal(await browser!.evaluate(`(() => {const el=document.activeElement,r=el?.getBoundingClientRect();return !!document.querySelector('[data-stream-create-view]')?.contains(el)&&!!r&&r.width>0&&r.height>0&&!el.hasAttribute('disabled');})()`), true, "Tab must reach a visible enabled create control");
      if (width === 390 || width === 1440) await capture(browser!, `create-empty-${width}`);
      await settleCreate();
      await clickVisible(browser!, 'button[aria-label="配信枠の一覧へ戻る"]');
      await browser!.waitFor(`!document.querySelector('[data-stream-create-view]')`, Boolean, "clean Back failed");
      await browser!.waitFor(`document.activeElement?.matches('main button') && document.activeElement?.textContent?.trim()==='配信枠を作成'`,Boolean,"Back must restore focus to the recreated list create trigger");
      await waitGETIdle(createGETs);
    }
    await browser!.setViewport(390, 844);
    await openCreate();
    await browser!.fillSelector('#create-stream-basic input', "保存前の入力");
    // Supply only the native decision boundary; the production dirty guard must invoke it.
    await browser!.evaluate(`globalThis.__coverOriginalConfirm=window.confirm; globalThis.__coverConfirmCalls=[]; window.confirm=(message)=>{globalThis.__coverConfirmCalls.push(message);return false;}; true`);
    try {
      await clickVisible(browser!, 'button[aria-label="配信枠の一覧へ戻る"]');
      assert.equal(await browser!.evaluate(`globalThis.__coverConfirmCalls.length`), 1, "dirty Back must request one discard decision");
      assert.match(await browser!.evaluate<string>(`globalThis.__coverConfirmCalls[0]`), /保存していない変更/);
    } finally {
      await browser!.evaluate(`window.confirm=globalThis.__coverOriginalConfirm; delete globalThis.__coverOriginalConfirm; delete globalThis.__coverConfirmCalls; true`);
    }
    assert.equal(await browser!.evaluate(`document.querySelector('#create-stream-basic input')?.value`), "保存前の入力");
    assert.equal(app.state.creates.length, 0);
    const authorized=fixture.authResponse;
    await browser!.evaluate(`globalThis.__coverOriginalConfirm=window.confirm;globalThis.__coverPermissionConfirmCalls=[];window.confirm=(message)=>{globalThis.__coverPermissionConfirmCalls.push(message);return false;};true`);
    try {
    await browser!.waitForRequestHandlersIdle({pathname:"/auth/me",method:"GET"});const beforeRevocation=snapshotGETs(["/auth/me"]);
    fixture.authResponse={body:{user:{id:"limited-user",username:"limited-user",roles:[]},permissions:["streams.read"]}};
    await browser!.evaluate(`document.dispatchEvent(new Event('visibilitychange',{bubbles:true}));true`);
    await waitGETResponses(["/auth/me"],beforeRevocation);
    assert.equal(await browser!.evaluate(`globalThis.__coverPermissionConfirmCalls.length`),0,"permission loss alone must not request draft discard");
    await browser!.waitFor(`document.querySelector('[data-stream-create-view]')?.textContent?.includes('配信枠を作成する権限がありません。') === true`,Boolean,"permission loss must keep the draft owner visible");
    assert.equal(await browser!.evaluate(`document.querySelector('#create-stream-basic input')?.value`),"保存前の入力");
    assert.equal(await browser!.evaluate(`document.querySelector('#create-stream form button[type="submit"]')?.disabled`),true);
    await browser!.waitForRequestHandlersIdle({pathname:"/auth/me",method:"GET"});const beforeRestoration=snapshotGETs(["/auth/me"]);fixture.authResponse=authorized;
    await browser!.evaluate(`document.dispatchEvent(new Event('visibilitychange',{bubbles:true}));true`);
    await waitGETResponses(["/auth/me"],beforeRestoration);
    assert.equal(await browser!.evaluate(`globalThis.__coverPermissionConfirmCalls.length`),0,"permission restoration alone must not request draft discard");
    await browser!.waitFor(`!document.querySelector('[data-stream-create-view]')?.textContent?.includes('配信枠を作成する権限がありません。')`,Boolean,"restored permission must recover the same draft");
    assert.equal(await browser!.evaluate(`document.querySelector('#create-stream-basic input')?.value`),"保存前の入力");
    } finally {await browser!.evaluate(`window.confirm=globalThis.__coverOriginalConfirm;delete globalThis.__coverOriginalConfirm;delete globalThis.__coverPermissionConfirmCalls;true`);}
    await browser!.fillSelector('#create-stream-basic input', "");
    await settleCreate();
    await clickVisible(browser!, 'button[aria-label="配信枠の一覧へ戻る"]');
    await browser!.waitFor(`!document.querySelector('[data-stream-create-view]')`, Boolean, "restored baseline Back failed");
  });

  await scenario("upload four, switch one start image, save, reload, and retain every candidate", async () => {
    await browser!.setViewport(1440, 1000);
    await openCreate();
    await browser!.fillSelector('#create-stream-basic input', "蓋画像ブラウザ検証");
    await select(browser!, "Discord BOT設定", "Selected BOT");
    await select(browser!, "担当Worker Node", "Worker One");
    await select(browser!, "担当Encoder Node", "Encoder One");
    await select(browser!, "Discord配信先モード", "プリセット");
    await select(browser!, "Discord配信先プリセット", "Dev_01");
    await select(browser!, "蓋画像のソース", "アップロード");
    for (let index=0; index<4; index++) {
      await upload(browser!, fileNames[index]);
      await waitCards(browser!, index+1);
      assert.equal(app.state.uploads, index+1);
    }
    assert.equal(await browser!.evaluate(`(() => {const label=[...document.querySelectorAll('label')].find(el=>el.textContent?.includes('蓋画像をアップロード'));return label.querySelector('input').disabled;})()`), true);
    assert.match(await browser!.evaluate<string>(`document.querySelector('[data-cover-upload-count]')?.textContent || ''`), /4\/4/);
    await assertCardLayout(browser!, 4, 1440);
    const choose = '[data-cover-candidate="variant-1"] button';
    await scrollSelectorIntoView(browser!, choose); await browser!.clickSelector(choose);
    assert.equal(await browser!.evaluate(`document.querySelectorAll('[data-cover-candidate] button:disabled').length`), 2, "only the start candidate's selection/removal controls are disabled");
    await scrollSelectorIntoView(browser!, '[data-cover-candidate="variant-3"] button:last-child');
    await browser!.clickSelector('[data-cover-candidate="variant-3"] button:last-child');
    await waitCards(browser!, 3);
    assert.deepEqual(app.state.deletes, ["/media-assets/asset-3"], "removing an owned draft must release its upload slot");
    assert.deepEqual(app.state.deleteSessions,["upload-session-one"],"draft cleanup must carry the expected ownership session");
    await upload(browser!, "replacement-fourth-image.png");
    await waitCards(browser!, 4);
    await scrollSelectorIntoView(browser!,choose);await browser!.clickSelector(choose);
    await capture(browser!, "create-four-desktop");
    await scrollSelectorIntoView(browser!, '#create-stream form button[type="submit"]');
    await browser!.clickSelector('#create-stream form button[type="submit"]');
    await browser!.waitFor(`!document.querySelector('[data-stream-create-view]')`, Boolean, "successful creation must return to list");
    assert.equal(app.state.creates.length, 1);
    const submitted = app.state.creates[0].visual_settings as Record<string, unknown>;
    assert.equal(submitted.cover_variant_id, "variant-1");
    assert.equal((submitted.cover_candidates as unknown[]).length, 4);
    assert.deepEqual(submitted.discord_target, {mode:"preset",preset_id:"discord-target-one",preset_revision:1}, "Discord revision must survive the plain name label");
    await navigateList("reload");
    await browser!.waitFor(`!!document.querySelector('button[aria-label="詳細"]')`, Boolean, "saved stream row missing");
    await openDetail();
    await waitCards(browser!, 4);
    for (const width of widths) {
      await browser!.setViewport(width, 1000); await waitForAnimationFrames(browser!);
      await assertCardLayout(browser!, 4, width);
      if (width === 390 || width === 1440) await capture(browser!, `saved-detail-four-${width}`);
    }
  });

  await scenario("zero, one, two, four, and legacy six remain visible in edit and detail", async () => {
    for (const count of [0,1,2,4,6]) {
      app.state.visual = visual(Array.from({length:count}, (_,index)=>candidate(index)));
      app.state.streams = [{...controlPlatformStream}];
      for (const width of [320,390,768,1440]) {
        await browser!.setViewport(width, 1000);
        await navigateList("navigate");
        await browser!.waitFor(`!!document.querySelector('button[aria-label$=" を編集"]')`, Boolean, "edit action missing");
        await openEdit();
        await browser!.waitFor(`!!document.querySelector('[role="dialog"]')`, Boolean, "existing edit Sheet missing");
        await assertCardLayout(browser!, count, width);
        for(let index=0;index<Math.min(3,count);index++) {
          const nameSelector=`[data-cover-candidate="variant-${index+1}"] [data-cover-candidate-name]`;
          await clickVisible(browser!,`${nameSelector} summary`);
          await browser!.waitFor(`document.querySelector(${JSON.stringify(nameSelector+'[open] p')})?.textContent === ${JSON.stringify(fileNames[index])}`, Boolean, "touch/click must reveal the full original filename");
          await browser!.pressNativeKey("Space");
          await browser!.waitFor(`!document.querySelector(${JSON.stringify(nameSelector+'[open]')})`, Boolean, "keyboard full filename disclosure missing");
        }
        if (count===6 && (width===390||width===1440)) await capture(browser!, `legacy-six-edit-${width}`);
        if (count===6 && width===1440) {
          await browser!.evaluate(`(() => {const label=[...document.querySelectorAll('[role="dialog"] label')].find(el=>el.textContent?.includes('配信開始時からCoverを表示'));label.querySelector('[role="checkbox"]').setAttribute('data-legacy-cover-visibility','');return true;})()`);
          await scrollSelectorIntoView(browser!, '[data-legacy-cover-visibility]');
          await browser!.clickSelector('[data-legacy-cover-visibility]');
          await clickVisible(browser!, '[role="dialog"] button', /^ビジュアル設定を保存$/);
          await browser!.waitFor(`document.body.textContent?.includes('ビジュアル設定を保存しました。') === true`, Boolean, "legacy non-collection save must remain available");
          assert.equal(app.state.saves.at(-1)?.cover_start_active, true);
          assert.equal(Object.hasOwn(app.state.saves.at(-1)!, "cover_candidates"), false, "legacy visibility save must retain the original six images");
          assert.equal(app.state.visual.cover_candidates.length, 6);
        }
        await closeSheet("edit close failed");
        await openDetail();
        await assertCardLayout(browser!, count, width);
        if (count===6 && (width===390||width===1440)) await capture(browser!, `legacy-six-detail-${width}`);
        await closeSheet("detail close failed");
      }
    }
  });
  await scenario("four uploads still permit a new preset through its authorized preview route",async()=>{
    app.state.visual=visual(Array.from({length:4},(_,index)=>candidate(index)));app.state.streams=[{...controlPlatformStream}];app.state.previewRequests=[];
    await browser!.setViewport(390,1000);
    await navigateList("navigate");
    await browser!.waitFor(`!!document.querySelector('button[aria-label$=" を編集"]')`,Boolean,"edit action missing for preset preview");
    await openEdit();await waitCards(browser!,4);
    await select(browser!,"蓋画像のソース","プリセット");
    await select(browser!,"蓋画像プリセット","Shared cover (r7)");
    await waitCards(browser!,5);
    assert.match(await browser!.evaluate<string>(`document.querySelector('[data-cover-upload-count]')?.textContent || ''`),/4\/4/);
    assert.ok(app.state.previewRequests.includes("/video-cover-presets/cover-preset-one/media-assets/shared-variant"),"new preset must use the preset authorization route");
    assert.equal(app.state.previewRequests.includes(`/streams/${controlPlatformStream.id}/media-assets/shared-variant`),false,"an unsaved preset is not yet stream-owned");
    await capture(browser!,"four-uploads-plus-preset-mobile");
    await clickVisible(browser!,'[role="dialog"] button',/^ビジュアル設定を保存$/);
    await browser!.waitFor(`document.body.textContent?.includes('ビジュアル設定を保存しました。') === true`,Boolean,"preset candidate save failed");
    assert.equal(app.state.visual.cover_candidates.length,5);
    assert.equal(app.state.visual.cover_candidates.filter(item=>item.source==="upload").length,4);
    await closeSheet("saved preset edit did not close");
    await navigateList("reload");await browser!.waitFor(`!!document.querySelector('button[aria-label="詳細"]')`,Boolean,"saved preset row missing");
    await openDetail();await waitCards(browser!,5);
    assert.ok(app.state.previewRequests.includes(`/streams/${controlPlatformStream.id}/media-assets/shared-variant`),"saved snapshot uses the stream authorization route");
    await closeSheet("saved preset detail did not close");
  });
  await scenario("committed but unknown edit blocks draft cleanup until read-only reconciliation",async()=>{
    app.state.visual=visual([candidate(0),candidate(1)]);app.state.streams=[{...controlPlatformStream}];
    await browser!.setViewport(390,1000);await navigateList("navigate");
    await browser!.waitFor(`!!document.querySelector('button[aria-label$=" を編集"]')`,Boolean,"unknown edit trigger missing");
    await openEdit();await waitCards(browser!,2);
    const firstDraftVariant=`variant-${app.state.uploads+1}`;
    await upload(browser!,"unknown-edit-first.png");await waitCards(browser!,3);
    await upload(browser!,"unknown-edit-second.png");await waitCards(browser!,4);
    const committedTitle="再取得しても保持するヘッダータイトル";
    await select(browser!,"ヘッダータイトルモード","カスタム");
    await browser!.fillSelector('input[aria-label="カスタムヘッダータイトル"]',committedTitle);
    const savesBeforeUnknown=app.state.saves.length;
    const gate=deferred(),entered=deferred();
    const writesBeforeUnknown=visualMethodCount("PUT"),readsBeforeUnknown=visualMethodCount("GET");
    app.state.visualWait=gate.promise;app.state.visualUnknown=true;
    visualPUTEntered=entered.resolve;
    await browser!.waitForRequestHandlersIdle({pathname:visualPath,method:"GET"});
    try {
      await clickVisible(browser!,'[role="dialog"] button',/^ビジュアル設定を保存$/);
      await withTimeout(entered.promise,10_000,"the actual visual PUT must enter the commit fixture independently of its fresh GET");
      await browser!.waitForRequestHandlersIdle({pathname:visualPath,method:"GET"});
      assert.equal(visualMethodCount("PUT"),writesBeforeUnknown+1,"the held write must be exactly one real PUT");
      assert.equal(app.state.saves.length,savesBeforeUnknown+1,"the held gate must follow the actual server-side PUT record");
      assert.equal(visualMethodCount("GET"),readsBeforeUnknown+1,"the save must freshly GET its authority before the distinct PUT");
      const responsesBeforeRelease=browser!.responses.get(visualPath)||0,readsBeforeRelease=visualMethodCount("GET");
      await browser!.waitFor(`document.querySelectorAll('[data-cover-candidate] button').length===8 && [...document.querySelectorAll('[data-cover-candidate] button')].every(button=>button.disabled)`,Boolean,"in-flight visual save must block candidate mutations");
      await assertDraftMutationsBlocked(firstDraftVariant);
      gate.resolve();await browser!.waitForRequestHandlersIdle({pathname:visualPath,method:"PUT"});await waitForAnimationFrames(browser!);
      assert.equal(visualMethodCount("GET"),readsBeforeRelease,"no separate GET may stand in for the held PUT response");
      assert.ok((browser!.responses.get(visualPath)||0)>=responsesBeforeRelease+1,"the real PUT response must complete after its gate is released");
      await browser!.waitFor(`document.querySelector('[role="dialog"]')?.textContent?.includes('操作結果')===true`,Boolean,"the released unknown PUT must show its unknown-outcome notice");
      await assertDraftMutationsBlocked(firstDraftVariant);
      assert.ok(app.state.claimedAssets.has(firstDraftVariant.replace("variant-","asset-")),"server commit precedes the unknown response");
      assert.equal(await browser!.evaluate(`document.querySelectorAll('[data-cover-candidate]').length`),4);
      await capture(browser!,"unknown-edit-retains-four-mobile");
    } finally {gate.resolve();app.state.visualWait=undefined;app.state.visualUnknown=false;visualPUTEntered=undefined;}
    assert.equal(app.state.saves.length,savesBeforeUnknown+1,"the unknown edit must send its PUT exactly once");
    const savedSnapshot=structuredClone(app.state.visual),deletesBeforeRefresh=app.state.deletes.length,uploadsBeforeRefresh=app.state.uploads;
    const draftSnapshot=`(() => {
      const dialog=document.querySelector('[role="dialog"]');
      if(!dialog)throw new Error('unknown edit dialog missing');
      return {url:location.href,inputs:[...dialog.querySelectorAll('input:not([type="file"])')].map(input=>[input.getAttribute('aria-label'),input.value,input.checked]),
        selections:[...dialog.querySelectorAll('[role="combobox"]')].map(select=>select.textContent),checks:[...dialog.querySelectorAll('[role="checkbox"]')].map(checkbox=>checkbox.getAttribute('aria-checked')),
        covers:[...dialog.querySelectorAll('[data-cover-candidate]')].map(card=>({variant:card.getAttribute('data-cover-candidate'),preview:card.querySelector('img')?.getAttribute('src'),start:card.querySelector('button')?.textContent?.trim()}))};
    })()`;
    const retained=await browser!.evaluate<{covers:{variant:string;preview:string;start:string}[]}>(draftSnapshot);
    assert.ok(retained.covers.some(card=>card.preview.startsWith('blob:')),"unsaved draft previews must exist before reconciliation");
    await browser!.evaluate(`globalThis.__coverOriginalConfirm=window.confirm;globalThis.__coverConfirmCalls=[];window.confirm=(message)=>{globalThis.__coverConfirmCalls.push(message);return false;};true`);
    try {
      const readsBeforeRefresh=visualMethodCount("GET");
      await clickVisible(browser!,'[role="dialog"] button',/^入力を保持して最新状態を確認$/);
      await browser!.waitFor(`document.body.textContent?.includes('サーバーの最新状態を確認しました。入力内容は保持しています。') === true`,Boolean,"unknown edit must reconcile without replacing its draft");
      await browser!.waitForRequestHandlersIdle({pathname:visualPath,method:"GET"});await waitCards(browser!,4);
      assert.equal(visualMethodCount("GET"),readsBeforeRefresh+1,"read-only reconciliation must perform exactly one additional visual GET");
      assert.equal(visualMethodCount("PUT"),writesBeforeUnknown+1,"read-only reconciliation must not resend the PUT");
      assert.deepEqual(await browser!.evaluate(draftSnapshot),retained,"read-only reconciliation must preserve every input, selection, candidate and preview URL");
      assert.equal(await browser!.evaluate(`globalThis.__coverConfirmCalls.length`),0,"read-only reconciliation must not request discard");
      assert.equal(app.state.saves.length,savesBeforeUnknown+1,"read-only reconciliation must not resend the PUT");
      assert.equal(app.state.deletes.length,deletesBeforeRefresh,"read-only reconciliation must not clean up draft assets");
      assert.equal(app.state.uploads,uploadsBeforeRefresh,"read-only reconciliation must not upload again");
      await capture(browser!,"unknown-edit-read-only-retains-four-mobile");
      await browser!.fillSelector('input[aria-label="カスタムヘッダータイトル"]',"明示的な破棄でのみ元に戻す入力");
      const beforeDiscard=await browser!.evaluate(draftSnapshot);
      await clickVisible(browser!,'[role="dialog"] button',/^入力を破棄して最新設定を再読込$/);
      assert.equal(await browser!.evaluate(`globalThis.__coverConfirmCalls.length`),1,"explicit discard must request one dirty decision");
      assert.match(await browser!.evaluate<string>(`globalThis.__coverConfirmCalls[0]`),/保存していない変更/);
      assert.deepEqual(await browser!.evaluate(draftSnapshot),beforeDiscard,"declining discard must preserve the current draft and URLs");
      await browser!.evaluate(`window.confirm=(message)=>{globalThis.__coverConfirmCalls.push(message);return true;};true`);
      await clickVisible(browser!,'[role="dialog"] button',/^入力を破棄して最新設定を再読込$/);
      await browser!.waitFor(`document.body.textContent?.includes('サーバーの最新設定を読み込みました。') === true`,Boolean,"accepted discard must reload the saved snapshot");await waitCards(browser!,4);
      assert.equal(await browser!.evaluate(`globalThis.__coverConfirmCalls.length`),2,"accepted discard must request exactly one further decision");
      assert.equal(await browser!.evaluate(`document.querySelector('input[aria-label="カスタムヘッダータイトル"]')?.value`),committedTitle,"only accepted discard restores the saved input");
      const reloaded=await browser!.evaluate<{covers:{variant:string;preview:string;start:string}[]}>(draftSnapshot);
      assert.deepEqual(reloaded.covers.map(({variant,start})=>({variant,start})),retained.covers.map(({variant,start})=>({variant,start})),"discard must retain all four saved candidates and the single start image");
      const savedPreviewPath=(variant:string)=>`/streams/${encodeURIComponent(controlPlatformStream.id)}/media-assets/${encodeURIComponent(variant)}`;
      const isSavedPreview=(preview:string,variant:string)=>{
        try {
          const actual=new URL(preview,server.baseUrl),expected=new URL(savedPreviewPath(variant),server.baseUrl);
          return actual.origin===expected.origin&&actual.protocol===expected.protocol&&actual.pathname===expected.pathname
            &&actual.username===""&&actual.password===""&&actual.search===""&&actual.hash===""&&actual.href===expected.href;
        } catch {return false;}
      };
      const referenceVariant=reloaded.covers[0].variant,referencePath=savedPreviewPath(referenceVariant),referenceURL=new URL(referencePath,server.baseUrl);
      assert.equal(isSavedPreview(referencePath,referenceVariant),true,"the authorized relative route must be accepted");
      assert.equal(isSavedPreview(referenceURL.href,referenceVariant),true,"the same-origin absolute DOM form must be equivalent");
      const credentialURL=new URL(referenceURL);credentialURL.username="fixture";
      for(const [kind,preview] of [
        ["blob",`blob:${server.baseUrl}/fixture`],["data","data:image/png;base64,AA=="],
        ["external",`https://outside.invalid${referencePath}`],["wrong-stream",`/streams/other-stream/media-assets/${referenceVariant}`],
        ["wrong-variant",savedPreviewPath("other-variant")],["query",referenceURL.href+"?copy=1"],
        ["hash",referenceURL.href+"#copy"],["credentials",credentialURL.href],
      ])assert.equal(isSavedPreview(preview,referenceVariant),false,`${kind} must not qualify as an authorized saved snapshot`);
      assert.ok(reloaded.covers.every(card=>isSavedPreview(card.preview,card.variant)),"accepted discard replaces local previews with exact same-origin authorized saved-snapshot URLs");
      assert.deepEqual(app.state.visual,savedSnapshot,"reconciliation and discard must not mutate the saved four-candidate snapshot");
      assert.equal(app.state.saves.length,savesBeforeUnknown+1,"discard must not resend the unknown PUT");
      assert.equal(app.state.deletes.length,deletesBeforeRefresh,"discard must not delete already claimed assets");
      assert.equal(app.state.uploads,uploadsBeforeRefresh,"discard must not upload again");
      await capture(browser!,"unknown-edit-discard-loaded-four-mobile");
      await closeSheet("reconciled edit failed to close");
      assert.equal(await browser!.evaluate(`globalThis.__coverConfirmCalls.length`),2,"a discarded and reloaded edit must close cleanly");
    } finally {await browser!.evaluate(`window.confirm=globalThis.__coverOriginalConfirm;delete globalThis.__coverOriginalConfirm;delete globalThis.__coverConfirmCalls;true`);}
  });
  await scenario("one unauthorized preview does not hide the other candidates", async () => {
    app.state.visual = visual(Array.from({length:4}, (_,index)=>candidate(index)));
    app.state.streams = [{...controlPlatformStream}];
    app.state.previewFailures.add("variant-2");
    await browser!.setViewport(390,1000);
    await navigateList("navigate");
    await browser!.waitFor(`!!document.querySelector('button[aria-label="詳細"]')`, Boolean, "detail trigger missing for preview failure");
    await openDetail();
    await browser!.waitFor(`(() => {
      const cards=[...document.querySelectorAll('[data-cover-candidate]')];
      return cards.length===4 && cards.filter(card=>{const img=card.querySelector('img');return img instanceof HTMLImageElement&&img.complete&&img.naturalWidth>0;}).length===3
        && document.querySelector('[data-cover-candidate="variant-2"]')?.textContent?.includes('プレビューを読み込めません');
    })()`, Boolean, "only the unavailable preview must fall back while every candidate remains");
    await capture(browser!, "one-preview-unavailable-mobile");
    app.state.previewFailures.clear();
  });
  await scenario("pending then committed but unknown creation retains the draft and sends no cleanup",async()=>{
    await browser!.setViewport(390,1000);await openCreate();
    await browser!.fillSelector('#create-stream-basic input',"結果不明でも保持する配信枠");
    await select(browser!,"Discord BOT設定","Selected BOT");await select(browser!,"担当Worker Node","Worker One");await select(browser!,"担当Encoder Node","Encoder One");
    await select(browser!,"Discord配信先モード","プリセット");await select(browser!,"Discord配信先プリセット","Dev_01");await select(browser!,"蓋画像のソース","アップロード");
    const firstDraftVariant=`variant-${app.state.uploads+1}`;
    await upload(browser!,"unknown-create-first.png");await waitCards(browser!,1);await upload(browser!,"unknown-create-second.png");await waitCards(browser!,2);
    const gate=deferred(),entered=deferred(),createCount=app.state.creates.length;app.state.createWait=gate.promise;app.state.createUnknown=true;app.state.createEntered=entered.resolve;
    try {
      await scrollSelectorIntoView(browser!,'#create-stream form button[type="submit"]');await browser!.clickSelector('#create-stream form button[type="submit"]');
      await withTimeout(entered.promise,10_000,"real create request did not reach the commit fixture");
      await browser!.waitFor(`document.querySelectorAll('[data-cover-candidate] button').length===4 && [...document.querySelectorAll('[data-cover-candidate] button')].every(button=>button.disabled)`,Boolean,"in-flight creation must block candidate mutations");
      await assertDraftMutationsBlocked(firstDraftVariant);
      gate.resolve();await browser!.waitForRequestHandlersIdle({pathname:"/streams",method:"POST"});
      await browser!.waitFor(`document.querySelector('[data-stream-create-view]')?.textContent?.includes('操作結果') === true`,Boolean,"unknown creation must show a result notice");
      await assertDraftMutationsBlocked(firstDraftVariant);
      assert.equal(app.state.creates.length,createCount+1,"unknown creation must not resend");
      assert.ok(app.state.claimedAssets.has(firstDraftVariant.replace("variant-","asset-")),"the assets already belong to the committed stream");
      assert.equal(await browser!.evaluate(`document.querySelector('#create-stream-basic input')?.value`),"結果不明でも保持する配信枠");await waitCards(browser!,2);
      await capture(browser!,"unknown-create-retains-draft-mobile");
    } finally {gate.resolve();app.state.createWait=undefined;app.state.createUnknown=false;app.state.createEntered=undefined;}
  });
  assert.equal(browser.consoleErrorCount, 0, "rendered target flow must not produce console/runtime errors");
  browser.assertNoFatalError();
});
