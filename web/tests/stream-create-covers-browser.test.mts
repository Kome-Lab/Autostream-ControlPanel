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
  const fixture = createBrowserRouteFixture(browser, app.route);
  fixture.healthResponse = { body: healthyRows };
  await browser.configureDeterministicDocument({ source: `localStorage.setItem('autostream.controlPanel.locale','ja'); sessionStorage.setItem('autostream.csrf_token','browser-fixture-csrf');`, timezone: "Asia/Tokyo", locale: "ja-JP" });
  const openCreate = async () => {
    const location=await browser!.evaluate<string>(`location.origin+location.pathname`);
    if(location===`${server.baseUrl}/admin/streams/`)await browser!.reload();
    else await browser!.navigate(`${server.baseUrl}/admin/streams/`);
    await browser!.waitFor(`!!document.querySelector('main button') && !document.querySelector('[data-stream-create-view]')`,Boolean,"stream list entry point missing");
    await clickVisible(browser!,'main button',/^配信枠を作成$/);
    await browser!.waitFor(`!!document.querySelector('[data-stream-create-view] #create-stream')`, Boolean, "dedicated creation view missing");
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
      await clickVisible(browser!, 'button[aria-label="配信枠の一覧へ戻る"]');
      await browser!.waitFor(`!document.querySelector('[data-stream-create-view]')`, Boolean, "clean Back failed");
      await browser!.waitFor(`document.activeElement?.matches('main button') && document.activeElement?.textContent?.trim()==='配信枠を作成'`,Boolean,"Back must restore focus to the recreated list create trigger");
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
    await browser!.waitForRequestHandlersIdle({pathname:"/auth/me",method:"GET"});browser!.clearRequestCounts("/auth/me");
    fixture.authResponse={body:{user:{id:"limited-user",username:"limited-user",roles:[]},permissions:["streams.read"]}};
    await browser!.evaluate(`document.dispatchEvent(new Event('visibilitychange',{bubbles:true}));true`);
    await browser!.waitForResponseCount("/auth/me",1);
    assert.equal(await browser!.evaluate(`globalThis.__coverPermissionConfirmCalls.length`),0,"permission loss alone must not request draft discard");
    await browser!.waitFor(`document.querySelector('[data-stream-create-view]')?.textContent?.includes('配信枠を作成する権限がありません。') === true`,Boolean,"permission loss must keep the draft owner visible");
    assert.equal(await browser!.evaluate(`document.querySelector('#create-stream-basic input')?.value`),"保存前の入力");
    assert.equal(await browser!.evaluate(`document.querySelector('#create-stream form button[type="submit"]')?.disabled`),true);
    await browser!.waitForRequestHandlersIdle({pathname:"/auth/me",method:"GET"});browser!.clearRequestCounts("/auth/me");fixture.authResponse=authorized;
    await browser!.evaluate(`document.dispatchEvent(new Event('visibilitychange',{bubbles:true}));true`);
    await browser!.waitForResponseCount("/auth/me",1);
    assert.equal(await browser!.evaluate(`globalThis.__coverPermissionConfirmCalls.length`),0,"permission restoration alone must not request draft discard");
    await browser!.waitFor(`!document.querySelector('[data-stream-create-view]')?.textContent?.includes('配信枠を作成する権限がありません。')`,Boolean,"restored permission must recover the same draft");
    assert.equal(await browser!.evaluate(`document.querySelector('#create-stream-basic input')?.value`),"保存前の入力");
    } finally {await browser!.evaluate(`window.confirm=globalThis.__coverOriginalConfirm;delete globalThis.__coverOriginalConfirm;delete globalThis.__coverPermissionConfirmCalls;true`);}
    await browser!.fillSelector('#create-stream-basic input', "");
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
    await browser!.reload();
    await browser!.waitFor(`!!document.querySelector('button[aria-label="詳細"]')`, Boolean, "saved stream row missing");
    await clickVisible(browser!, 'button[aria-label="詳細"]');
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
        await browser!.navigate(`${server.baseUrl}/admin/streams/`);
        await browser!.waitFor(`!!document.querySelector('button[aria-label$=" を編集"]')`, Boolean, "edit action missing");
        await clickVisible(browser!, 'button[aria-label$=" を編集"]');
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
        await browser!.pressKey("Escape");
        await browser!.waitFor(`!document.querySelector('[role="dialog"]')`, Boolean, "edit close failed");
        await clickVisible(browser!, 'button[aria-label="詳細"]');
        await assertCardLayout(browser!, count, width);
        if (count===6 && (width===390||width===1440)) await capture(browser!, `legacy-six-detail-${width}`);
        await browser!.pressKey("Escape");
        await browser!.waitFor(`!document.querySelector('[role="dialog"]')`, Boolean, "detail close failed");
      }
    }
  });
  await scenario("four uploads still permit a new preset through its authorized preview route",async()=>{
    app.state.visual=visual(Array.from({length:4},(_,index)=>candidate(index)));app.state.streams=[{...controlPlatformStream}];app.state.previewRequests=[];
    await browser!.setViewport(390,1000);
    await browser!.navigate(`${server.baseUrl}/admin/streams/`);
    await browser!.waitFor(`!!document.querySelector('button[aria-label$=" を編集"]')`,Boolean,"edit action missing for preset preview");
    await clickVisible(browser!,'button[aria-label$=" を編集"]');await waitCards(browser!,4);
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
    await browser!.pressKey("Escape");await browser!.waitFor(`!document.querySelector('[role="dialog"]')`,Boolean,"saved preset edit did not close");
    await browser!.reload();await browser!.waitFor(`!!document.querySelector('button[aria-label="詳細"]')`,Boolean,"saved preset row missing");
    await clickVisible(browser!,'button[aria-label="詳細"]');await waitCards(browser!,5);
    assert.ok(app.state.previewRequests.includes(`/streams/${controlPlatformStream.id}/media-assets/shared-variant`),"saved snapshot uses the stream authorization route");
    await browser!.pressKey("Escape");
  });
  await scenario("committed but unknown edit blocks draft cleanup until read-only reconciliation",async()=>{
    app.state.visual=visual([candidate(0),candidate(1)]);app.state.streams=[{...controlPlatformStream}];
    await browser!.setViewport(390,1000);await browser!.navigate(`${server.baseUrl}/admin/streams/`);
    await browser!.waitFor(`!!document.querySelector('button[aria-label$=" を編集"]')`,Boolean,"unknown edit trigger missing");
    await clickVisible(browser!,'button[aria-label$=" を編集"]');await waitCards(browser!,2);
    const firstDraftVariant=`variant-${app.state.uploads+1}`;
    await upload(browser!,"unknown-edit-first.png");await waitCards(browser!,3);
    await upload(browser!,"unknown-edit-second.png");await waitCards(browser!,4);
    const gate=deferred(),visualPath=`/streams/${controlPlatformStream.id}/visual-settings`;
    app.state.visualWait=gate.promise;app.state.visualUnknown=true;
    await browser!.waitForRequestHandlersIdle({pathname:visualPath,method:"GET"});browser!.clearRequestCounts(visualPath);
    try {
      await clickVisible(browser!,'[role="dialog"] button',/^ビジュアル設定を保存$/);
      await browser!.waitForRequestCount(visualPath,1);
      await browser!.waitFor(`document.querySelectorAll('[data-cover-candidate] button').length===8 && [...document.querySelectorAll('[data-cover-candidate] button')].every(button=>button.disabled)`,Boolean,"in-flight visual save must block candidate mutations");
      await assertDraftMutationsBlocked(firstDraftVariant);
      gate.resolve();await browser!.waitForResponseCount(visualPath,1);await browser!.waitForRequestHandlersIdle({pathname:visualPath,method:"PUT"});await waitForAnimationFrames(browser!);
      await assertDraftMutationsBlocked(firstDraftVariant);
      assert.ok(app.state.claimedAssets.has(firstDraftVariant.replace("variant-","asset-")),"server commit precedes the unknown response");
      assert.equal(await browser!.evaluate(`document.querySelectorAll('[data-cover-candidate]').length`),4);
      await capture(browser!,"unknown-edit-retains-four-mobile");
    } finally {gate.resolve();app.state.visualWait=undefined;app.state.visualUnknown=false;}
    await browser!.evaluate(`globalThis.__coverOriginalConfirm=window.confirm;window.confirm=()=>true;true`);
    try {await clickVisible(browser!,'[role="dialog"] button',/^最新設定を再読込$/);}
    finally {await browser!.evaluate(`window.confirm=globalThis.__coverOriginalConfirm;delete globalThis.__coverOriginalConfirm;true`);}
    await browser!.waitFor(`document.body.textContent?.includes('サーバーの最新設定を読み込みました。') === true`,Boolean,"unknown edit must reconcile with a read-only refresh");await waitCards(browser!,4);
    await browser!.pressKey("Escape");await browser!.waitFor(`!document.querySelector('[role="dialog"]')`,Boolean,"reconciled edit failed to close");
  });
  await scenario("one unauthorized preview does not hide the other candidates", async () => {
    app.state.visual = visual(Array.from({length:4}, (_,index)=>candidate(index)));
    app.state.streams = [{...controlPlatformStream}];
    app.state.previewFailures.add("variant-2");
    await browser!.setViewport(390,1000);
    await browser!.navigate(`${server.baseUrl}/admin/streams/`);
    await browser!.waitFor(`!!document.querySelector('button[aria-label="詳細"]')`, Boolean, "detail trigger missing for preview failure");
    await clickVisible(browser!, 'button[aria-label="詳細"]');
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
