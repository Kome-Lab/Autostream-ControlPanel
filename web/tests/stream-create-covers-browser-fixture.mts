import assert from "node:assert/strict";
import { mkdirSync, writeFileSync } from "node:fs";
import { resolve } from "node:path";
import { deflateSync } from "node:zlib";
import { BrowserHarness, type StubRequest, type StubResponse } from "./helpers/browser-harness.mts";
import { controlPlatformStream } from "./ui-browser-fixture.mts";
import { waitForAnimationFrames } from "./ui-browser-navigation-helpers.mts";
import { clickVisible } from "./ui-regression/visible-trigger.mts";

export const fileNames = [
  "huge_2023-02-26_03.28.55.png",
  "a_very_long_ASCII_cover_image_filename_with_no_missing_buttons_and_every_candidate_visible_20261027.png",
  "日本語のとても長い蓋画像ファイル名でも全文をキーボードとタッチで確認できます_20261027.png",
  "fourth-upload.png", "legacy-fifth-upload.png", "legacy-sixth-upload.png",
];
export const widths = [320, 360, 390, 768, 1280, 1440];
type Candidate = { name: string; source: "upload" | "preset"; asset_id: string; variant_id: string; preset_id?: string; preset_revision?: number };
type Visual = Record<string, unknown> & { revision: number; cover_candidates: Candidate[]; cover_variant_id: string };
export const evidenceDirectory = process.env.AUTOSTREAM_STREAM_CREATE_EVIDENCE_DIR;
const layoutObservations:unknown[]=[];

// Real 16:9 PNGs with distinct colors make every rendered thumbnail inspectable.
function coverPNG(index: number) {
  const width=640,height=360, raw=Buffer.alloc(height*(1+width*3));
  const colors=[[44,89,139],[103,66,133],[37,115,101],[166,94,48],[126,73,92],[70,101,64]];
  const color=colors[index%colors.length];
  for(let y=0;y<height;y++) for(let x=0;x<width;x++) {
    const offset=y*(1+width*3)+1+x*3, light=x>55&&x<585&&y>75&&y<285;
    for(let c=0;c<3;c++) raw[offset+c]=light?Math.min(255,color[c]+50):color[c];
  }
  const chunk=(type:string,data:Buffer)=>{
    const tag=Buffer.from(type),body=Buffer.concat([tag,data]);let crc=0xffffffff;
    for(const byte of body){crc^=byte;for(let bit=0;bit<8;bit++)crc=(crc>>>1)^((crc&1)?0xedb88320:0);}
    const result=Buffer.alloc(data.length+12);result.writeUInt32BE(data.length);tag.copy(result,4);data.copy(result,8);result.writeUInt32BE((crc^0xffffffff)>>>0,result.length-4);return result;
  };
  const header=Buffer.alloc(13);header.writeUInt32BE(width);header.writeUInt32BE(height,4);header[8]=8;header[9]=2;
  return Buffer.concat([Buffer.from([137,80,78,71,13,10,26,10]),chunk("IHDR",header),chunk("IDAT",deflateSync(raw)),chunk("IEND",Buffer.alloc(0))]).toString("base64");
}
const coverPNGs = fileNames.map((_,index)=>coverPNG(index));

export function visual(candidates: Candidate[]): Visual {
  return { stream_id: controlPlatformStream.id, background_mode: "default", header_title_mode: "default", discord_target_mode: "inherit", cover_source: candidates.length ? "upload" : "none", cover_asset_id: candidates[0]?.asset_id || "", cover_variant_id: candidates[0]?.variant_id || "", cover_candidates: candidates, cover_start_active: false, revision: 1 };
}

export function candidate(index: number): Candidate {
  return { name: fileNames[index], source: "upload", asset_id: `asset-${index + 1}`, variant_id: `variant-${index + 1}` };
}

function projectVisualWrite(current: Visual, fields: Record<string, unknown>): Visual {
  const { discord_target: target, expected_revision: _expectedRevision, upload_session_id: _uploadSessionID, ...rest }=fields;
  void _expectedRevision; void _uploadSessionID;
  const selection=target && typeof target==="object" ? target as Record<string,unknown> : undefined;
  const projected={ ...current,...rest,...(selection?{
    discord_target_mode:selection.mode,discord_target_preset_id:selection.preset_id,
    discord_target_preset_revision:selection.preset_revision,
    discord_guild_id:selection.guild_id || "guild-one",discord_text_channel_id:selection.text_channel_id || "text-one",discord_voice_channel_id:selection.voice_channel_id || "voice-one",
    discord_snapshot_revision:1,
  }:{}) } as Visual;
  if(projected.cover_source==="preset"){
    const snapshot=projected.cover_candidates.find(item=>item.variant_id===projected.cover_variant_id&&item.source==="preset");
    if(snapshot){projected.cover_asset_id=snapshot.asset_id;projected.cover_preset_revision=snapshot.preset_revision;}
  }
  return projected;
}

type FixtureSession = { id: string; owner_type: string; expires_at: string; claimed_stream_id?: string };
type FixtureAsset = { id: string; owner_type: string; owner_id: string; upload_session_id: string; client_operation_id: string; usage_type: string; media_type: string; width: number; height: number; opaque: boolean; sha256: string; byte_size: number; deleted_at?: string };
type FixtureVariant = { id: string; asset_id: string; status: string; width: number; height: number; opaque: boolean; target_width: number; target_height: number; crop_mode: string };

function uploadMetadata(postData?: string) {
  assert.ok(postData, "the browser must expose actual multipart metadata; do not invent a session or nonce");
  const boundary = postData.slice(0, postData.indexOf("\r\n"));
  assert.ok(/^--[^\r\n]{1,200}$/.test(boundary), "multipart boundary is required");
  const values = new Map<string, string>();
  for (const part of postData.split(boundary).slice(1)) {
    const separator = part.indexOf("\r\n\r\n");
    if (separator < 0) continue;
    const headers = part.slice(0, separator), name = headers.match(/content-disposition:[^\r\n]*; name="([^"]+)"/i)?.[1];
    if (name === "file") break;
    if (!name) continue;
    assert.ok(["session_id", "usage_type", "client_operation_id"].includes(name), "unexpected upload metadata");
    assert.equal(values.has(name), false, "duplicate multipart metadata is rejected");
    values.set(name, part.slice(separator + 4).replace(/\r\n$/, ""));
  }
  const sessionID = values.get("session_id"), usage = values.get("usage_type"), operationID = values.get("client_operation_id");
  assert.ok(sessionID && /^[A-Za-z0-9][A-Za-z0-9_-]{0,127}$/.test(sessionID), "actual session metadata is required");
  assert.ok(usage === "scene_background" || usage === "video_cover", "actual upload usage is required");
  assert.ok(operationID && /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/.test(operationID), "actual operation nonce is required");
  return { sessionID, usage, operationID };
}

export function fixtureRoutes() {
  const state = {
    visual: visual([]), streams: [] as Record<string, unknown>[], uploads: 0,
    creates: [] as Record<string, unknown>[], saves: [] as Record<string, unknown>[], deletes: [] as string[],
    previewFailures: new Set<string>(), previewRequests: [] as string[],
    createWait: undefined as Promise<void> | undefined, createUnknown:false,
    createEntered:undefined as (()=>void)|undefined,
    visualWait: undefined as Promise<void> | undefined, visualUnknown:false,
    claimedAssets:new Set<string>(),deleteSessions:[] as string[],
    sessionSerial:0,currentSession:"",draftSessions:new Map<string,string>(),
    sessions:new Map<string,FixtureSession>(), assets:new Map<string,FixtureAsset>(),
    operations:new Map<string,string>(), variants:new Map<string,FixtureVariant[]>(),
  };
  const claimSession = (sessionID: unknown) => {
    if (typeof sessionID !== "string") return;
    const session = state.sessions.get(sessionID);
    if (!session) return;
    session.owner_type = "stream"; session.claimed_stream_id = controlPlatformStream.id;
    for (const asset of state.assets.values()) if (asset.upload_session_id === sessionID && !asset.deleted_at) {
      asset.owner_type = "stream"; asset.owner_id = controlPlatformStream.id; state.claimedAssets.add(asset.id);
    }
  };
  const route = ({ method, url, postData }: StubRequest): StubResponse | null => {
    const path = new URL(url).pathname.replace(/\/+$/, "");
    const response = (body: unknown): StubResponse => ({ body, requiredResponse: false });
    if (path === "/streams" && method === "GET") return response(state.streams);
    if (path === "/streams" && method === "POST") {
      const body = JSON.parse(postData || "{}") as Record<string, unknown>;
      state.creates.push(body);
      state.visual = projectVisualWrite(visual([]), body.visual_settings as Record<string,unknown>);
      for(const item of state.visual.cover_candidates)state.claimedAssets.add(item.asset_id);
      claimSession(body.upload_session_id);
      const stream = { ...controlPlatformStream, name: body.name, discord_config_id: body.discord_config_id, auto_start_trigger: body.auto_start_trigger };
      state.streams = [stream];
      state.createEntered?.();
      return { ...response(state.createUnknown?{code:"temporary_unavailable"}:stream), status: state.createUnknown?503:201, waitUntil:state.createWait };
    }
    if (path === `/streams/${controlPlatformStream.id}/visual-settings`) {
      if (method === "GET") return response(state.visual);
      if (method === "PUT") {
        const body = JSON.parse(postData || "{}") as Record<string, unknown>;
        state.saves.push(body);
        state.visual = { ...projectVisualWrite(state.visual,body), revision: state.visual.revision + 1 };
        for(const item of state.visual.cover_candidates)state.claimedAssets.add(item.asset_id);
        claimSession(body.upload_session_id);
        return {...response(state.visualUnknown?{code:"temporary_unavailable"}:state.visual),status:state.visualUnknown?503:200,waitUntil:state.visualWait};
      }
    }
    if (path === "/discord/target-presets") return response([{ id: "discord-target-one", name: "Dev_01", revision: 1, guild_id: "guild-one", text_channel_id: "text-one", voice_channel_id: "voice-one", enabled: true }]);
    if (path === "/video-cover-presets") return response([{ id: "cover-preset-one", name: "Shared cover", revision: 7, asset_id: "shared-asset", asset_variant_id: "shared-variant", enabled: true }]);
    if (path === "/discord/configs") return response([{ id: "discord-config-one", name: "Selected BOT", service_id: "bot-one" }]);
    if (path === "/media-assets/upload-sessions" && method === "POST") {
      state.sessionSerial++;state.currentSession=state.sessionSerial===1?"upload-session-one":`upload-session-${state.sessionSerial}`;
      const session = { id: state.currentSession, owner_type: "upload_draft", expires_at: "2099-01-01T00:00:00Z" };
      state.sessions.set(session.id, session);
      return response({ ...session });
    }
    const operation = path.match(/^\/media-assets\/upload-sessions\/([A-Za-z0-9_-]+)\/assets$/);
    if (operation && method === "GET") {
      const parameters = new URL(url).searchParams, operationID = parameters.get("operation_id") || "";
      if (parameters.getAll("operation_id").length !== 1 || !/^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/.test(operationID)) return { status: 400, body: { code: "bad_request" }, requiredResponse: false };
      const session = state.sessions.get(operation[1]);
      if (!session) return { status: 404, body: { code: "not_found" }, requiredResponse: false };
      const assetID = state.operations.get(`${session.id}/${operationID}`), asset = assetID ? state.assets.get(assetID) : undefined;
      return response({ session: { ...session }, operation_id: operationID, assets: asset ? [{ asset: { ...asset }, variants: (state.variants.get(asset.id) || []).map(variant => ({ ...variant })) }] : [] });
    }
    if (path === "/media-assets" && method === "POST") {
      const { sessionID, usage, operationID } = uploadMetadata(postData), session = state.sessions.get(sessionID);
      if (!session) return { status: 404, body: { code: "not_found" }, requiredResponse: false };
      if (session.owner_type !== "upload_draft" || session.claimed_stream_id) return { status: 409, body: { code: "upload_session_claimed" }, requiredResponse: false };
      const key = `${sessionID}/${operationID}`, existingID = state.operations.get(key), existing = existingID ? state.assets.get(existingID) : undefined;
      if (existing) {
        if (existing.deleted_at) return { status: 404, body: { code: "not_found" }, requiredResponse: false };
        if (existing.usage_type !== usage) return { status: 403, body: { code: "forbidden" }, requiredResponse: false };
        return response({ ...existing });
      }
      state.uploads += 1;
      const assetID = `asset-${state.uploads}`;
      const asset: FixtureAsset = { id: assetID, usage_type: usage, owner_type: "upload_draft", owner_id: sessionID, upload_session_id: sessionID, client_operation_id: operationID,
        media_type: "image/png", width: 640, height: 360, opaque: true, byte_size: 128, sha256: "a".repeat(64) };
      state.claimedAssets.delete(assetID);state.draftSessions.set(assetID,sessionID);state.assets.set(assetID,asset);state.operations.set(key,assetID);
      return response({ ...asset });
    }
    if (/^\/media-assets\/asset-\d+$/.test(path) && method === "DELETE") {
      state.deletes.push(path);state.deleteSessions.push(new URL(url).searchParams.get("expected_upload_session_id") || "");
      const assetID = path.split("/").at(-1)!, asset = state.assets.get(assetID);
      if(state.claimedAssets.has(assetID) || asset?.owner_type === "stream")return {status:409,body:{code:"upload_session_claimed"},requiredResponse:false};
      if(!asset)return {status:404,body:{code:"not_found"},requiredResponse:false};
      if(state.deleteSessions.at(-1)!==state.draftSessions.get(assetID))return {status:403,body:{code:"forbidden"},requiredResponse:false};
      asset.deleted_at = "2026-10-28T00:00:00Z";
      return { status: 204, requiredResponse: false };
    }
    const variant = path.match(/^\/media-assets\/(asset-\d+)\/variants$/);
    if (variant && method === "POST") {
      const asset = state.assets.get(variant[1]);
      if (!asset || asset.deleted_at) return { status: 404, body: { code: "not_found" }, requiredResponse: false };
      const spec = JSON.parse(postData || "{}");
      assert.deepEqual(spec, { width: 1920, height: 1080, opaque: true }, "the variant must use the actual requested specification");
      const ready = { id: variant[1].replace("asset-", "variant-"), asset_id: variant[1], status: "ready", width: 1920, height: 1080, opaque: true, target_width: 1920, target_height: 1080, crop_mode: "center_crop_opaque" };
      state.variants.set(asset.id, [ready]);
      return response(ready);
    }
    if (path.includes("/media-assets/") && method === "GET") {
      state.previewRequests.push(path);
      if(path===`/streams/${controlPlatformStream.id}/media-assets/shared-variant` && !state.visual.cover_candidates.some(item=>item.variant_id==="shared-variant")) return {status:403,body:{code:"permission_denied"},requiredResponse:false};
      if (state.previewFailures.has(path.split("/").at(-1)!)) return { status: 404, body: { code: "not_found" }, requiredResponse: false };
      const imageIndex=Number(path.match(/variant-(\d+)$/)?.[1] || 1)-1;
      return { contentType: "image/png", bodyBase64: coverPNGs[imageIndex % coverPNGs.length], requiredResponse: false };
    }
    if (["/youtube/outputs", "/profiles/encoder", "/profiles/caption", "/profiles/overlay", "/profiles/archive"].includes(path)) return response([]);
    return null;
  };
  return { state, route };
}

export async function select(browser: BrowserHarness, label: string, option: string) {
  const selector = await browser.evaluate<string>(`(() => {
    const label = [...document.querySelectorAll('label')].find(el => el.textContent?.trim().startsWith(${JSON.stringify(label)}) && el.querySelector('[role="combobox"]'));
    const button = label?.querySelector('[role="combobox"]') || [...document.querySelectorAll('[role="combobox"]')].find(el => document.getElementById(el.getAttribute('aria-labelledby') || '')?.textContent === ${JSON.stringify(label)});
    if (!(button instanceof HTMLElement)) throw new Error('select missing: ' + ${JSON.stringify(label)});
    button.setAttribute('data-browser-select', 'active');
    return '[data-browser-select="active"]';
  })()`);
  await clickVisible(browser,selector);
  await browser.clickRoleWithText("option", option);
  await browser.waitFor(`!document.querySelector('[role="option"]')`, Boolean, "select popup did not close");
  await browser.evaluate(`document.querySelector('[data-browser-select]')?.removeAttribute('data-browser-select'); true`);
}

export async function upload(browser: BrowserHarness, name: string) {
  const image=coverPNGs[Math.max(0,fileNames.indexOf(name))];
  await browser.evaluate(`(() => {
    const label = [...document.querySelectorAll('label')].find(el => el.textContent?.includes('蓋画像をアップロード'));
    const input = label?.querySelector('input[type=file]');
    if (!(input instanceof HTMLInputElement) || input.disabled) throw new Error('cover upload disabled');
    const data = new DataTransfer();
    data.items.add(new File([Uint8Array.from(atob(${JSON.stringify(image)}), c => c.charCodeAt(0))], ${JSON.stringify(name)}, {type:'image/png'}));
    input.files = data.files; input.dispatchEvent(new Event('change', {bubbles:true}));
    return true;
  })()`);
}

export async function waitCards(browser: BrowserHarness, count: number) {
  await browser.waitFor(`(() => {
    const cards = [...document.querySelectorAll('[data-cover-candidate]')];
    return cards.length === ${count} && cards.every(card => { const image = card.querySelector('img'); return image instanceof HTMLImageElement && image.complete && image.naturalWidth > 0; });
  })()`, Boolean, `${count} candidate previews must decode`);
  await browser.waitFor(`document.querySelector('[role="dialog"]')?.getAnimations().every(animation=>animation.playState!=='running') ?? true`,Boolean,"edit/detail Sheet must settle before candidate interactions");
}

export async function capture(browser: BrowserHarness, label: string) {
  if (!evidenceDirectory) return;
  mkdirSync(evidenceDirectory, { recursive: true });
  await browser.evaluate(`document.querySelector('[data-cover-candidate-grid]')?.scrollIntoView({block:'start',behavior:'instant'});true`);
  await waitForAnimationFrames(browser);
  // Detach observations before the awaited screenshot so later state cannot rewrite proof.
  const observation = structuredClone(await browser.evaluate(`(() => ({ url: location.href, title: document.title, width: innerWidth, overflow: document.documentElement.scrollWidth-document.documentElement.clientWidth, candidates:[...document.querySelectorAll('[data-cover-candidate]')].map(card => ({variant:card.getAttribute('data-cover-candidate'),text:card.textContent,image:card.querySelector('img')?.getAttribute('src')})) }))()`));
  const screenshot = await browser.captureScreenshot();
  writeFileSync(resolve(evidenceDirectory, `${label}.png`), screenshot);
  writeFileSync(resolve(evidenceDirectory, `${label}.json`), JSON.stringify(observation, null, 2));
  const viewport=await browser.captureViewportEvidence();
  writeFileSync(resolve(evidenceDirectory,`${label}.viewport.png`),viewport.png);
  writeFileSync(resolve(evidenceDirectory,`${label}.viewport.json`),JSON.stringify({observation,viewport:viewport.viewport},null,2));
}

export async function assertCardLayout(browser: BrowserHarness, expected: number, width: number) {
  await waitCards(browser, expected);
  const layout = await browser.evaluate<{ cssWidth:number; overflow: number; cards: { width: number; nameWidth: number; separate: boolean; buttons: boolean }[] }>(`(() => ({
    cssWidth:innerWidth,
    overflow: Math.max(0, document.documentElement.scrollWidth-document.documentElement.clientWidth),
    cards:[...document.querySelectorAll('[data-cover-candidate]')].map(card => {
      const name=card.querySelector('[data-cover-candidate-name]'),actions=card.querySelector('[data-cover-candidate-actions]');
      return { width:card.getBoundingClientRect().width, nameWidth:name?.getBoundingClientRect().width || 0,
        separate:!actions || !!name && actions.getBoundingClientRect().top >= name.getBoundingClientRect().bottom,
        buttons:[...card.querySelectorAll('button')].every(button=>button.getBoundingClientRect().width>=80 && button.getBoundingClientRect().height>=30) };
    })
  }))()`);
  assert.equal(layout.cssWidth,width,"the measured CSS viewport must match the requested width");
  assert.equal(layout.overflow, 0, `${width}px must not horizontally overflow`);
  assert.equal(layout.cards.length, expected);
  for (const card of layout.cards) {
    assert.ok(card.width >= 160 && card.nameWidth >= 130, `${width}px filename must retain a readable width: ${JSON.stringify(card)}`);
    assert.equal(card.separate, true, "filename and controls must occupy separate rows");
    assert.equal(card.buttons, true, "candidate controls remain usable");
  }
  layoutObservations.push(structuredClone({requestedWidth:width,expectedCandidates:expected,...layout}));
  if(evidenceDirectory){mkdirSync(evidenceDirectory,{recursive:true});writeFileSync(resolve(evidenceDirectory,"layout-observations.json"),JSON.stringify(layoutObservations,null,2));}
}
