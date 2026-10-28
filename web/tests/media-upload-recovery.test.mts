import assert from "node:assert/strict";
import { register } from "node:module";
import test from "node:test";
import { registerSourceResolution } from "./helpers/moved-source.mts";
import type { DraftMediaVariant, MediaUploadProgress } from "../src/features/streams/media-upload-protocol.ts";

register(`data:text/javascript,${encodeURIComponent(`
let root;
export function initialize(data) { root = data.root; }
export async function resolve(specifier, context, nextResolve) {
  if (specifier.startsWith('@/')) {
    const target = new URL('src/' + specifier.slice(2), root);
    if (!/\\.[cm]?[jt]sx?$/.test(target.pathname)) target.pathname += '.ts';
    return nextResolve(target.href, context);
  }
  return nextResolve(specifier, context);
}`)}`, { parentURL: import.meta.url, data: { root: new URL("../", import.meta.url).href } });
registerSourceResolution();
const { uploadDraftMediaAsset, deleteDraftMediaAsset } = await import("../src/features/streams/media-asset-client.ts");
const { checkDraftMediaUpload } = await import("../src/features/streams/media-upload-reconcile.ts");
const { requireMediaID, requireDraftMediaAsset } = await import("../src/features/streams/media-upload-protocol.ts");
const operationID = "b6cf8101-80f4-4d57-9610-f1a3d41aa245";
const session = { id: "draft-owned", owner_type: "upload_draft", expires_at: "2099-01-01T00:00:00Z" };
const asset = { id: "asset-owned", owner_type: "upload_draft", owner_id: session.id, upload_session_id: session.id, client_operation_id: operationID,
  usage_type: "video_cover" as const, media_type: "image/png", width: 640, height: 360, sha256: "a".repeat(64) };
const ready: DraftMediaVariant = { id: "variant-owned", asset_id: asset.id, status: "ready", width: 1920, height: 1080, opaque: true };
const nonready = (status: "queued" | "processing" | "failed"): DraftMediaVariant => ({ ...ready, status, width: 0, height: 0, opaque: false, target_width: 1920, target_height: 1080, crop_mode: "center_crop_opaque", last_error_code: "image_processing_failed" });
const file = () => new File(["fixture image"], "cover.png", { type: "image/png" });
async function fixture(handler: typeof fetch, body: () => Promise<void>) {
  const original = globalThis.fetch, demo = process.env.NEXT_PUBLIC_AUTOSTREAM_DEMO;
  globalThis.fetch = handler; process.env.NEXT_PUBLIC_AUTOSTREAM_DEMO = "false";
  try { await body(); } finally { globalThis.fetch = original; if (demo === undefined) delete process.env.NEXT_PUBLIC_AUTOSTREAM_DEMO; else process.env.NEXT_PUBLIC_AUTOSTREAM_DEMO = demo; }
}
function receipt(variant: DraftMediaVariant = ready) { return Response.json({ session, operation_id: operationID, assets: [{ asset, variants: [variant] }] }); }

test("metadata precedes bytes, the fixed nonce and AbortSignal reach the real fetch, and observer errors cannot delete success", async () => {
  const abort = new AbortController(), requests: string[] = [];
  await fixture(async (input, init) => {
    const path = String(input); requests.push(`${init?.method} ${path}`);
    assert.equal(init?.signal, abort.signal);
    if (path === "/media-assets/upload-sessions") return Response.json(session);
    if (path === "/media-assets") {
      assert.ok(init?.body instanceof FormData);
      assert.deepEqual([...init.body.keys()], ["session_id", "usage_type", "client_operation_id", "file"]);
      assert.equal(init.body.get("client_operation_id"), operationID); return Response.json(asset);
    }
    if (path === `/media-assets/${asset.id}/variants`) return Response.json(ready);
    throw new Error("unexpected request");
  }, async () => {
    const result = await uploadDraftMediaAsset(file(), "video_cover", undefined, { operationID, signal: abort.signal, onProgress() { throw new Error("presentation failure"); } });
    assert.equal(result.variant.id, ready.id); assert.equal(requests.some(value => value.startsWith("DELETE")), false);
  });
});

test("a lost upload response stays unknown and one read-only lookup adopts the committed receipt without another body", async () => {
  const events: MediaUploadProgress[] = [], requests: string[] = [];
  await fixture(async (input, init) => {
    const path = String(input); requests.push(`${init?.method} ${path}`);
    if (path === "/media-assets/upload-sessions") return Response.json(session);
    if (path === "/media-assets") throw new TypeError("network response lost after commit");
    if (init?.method === "GET") return receipt();
    throw new Error("a lost receipt must not write again");
  }, async () => {
    await assert.rejects(uploadDraftMediaAsset(file(), "video_cover", undefined, { operationID, onProgress: value => events.push(value) }));
    assert.equal(events.at(-1)?.phase, "outcome_unknown"); assert.equal(events.at(-1)?.cleanupConfirmed, false);
    const result = await checkDraftMediaUpload(session.id, operationID, "video_cover"); assert.equal(result.kind, "ready");
    assert.equal(requests.filter(value => value === "POST /media-assets").length, 1); assert.equal(requests.filter(value => value.startsWith("DELETE")).length, 0);
  });
});

test("an existing operation lookup failure cannot prove an earlier upload absent or cleaned", async () => {
  const events: MediaUploadProgress[] = [], methods: string[] = [];
  await fixture(async (_input, init) => { methods.push(init?.method || ""); return Response.json({ code: "media_asset_operation_failed" }, { status: 500 }); }, async () => {
    await assert.rejects(uploadDraftMediaAsset(file(), "video_cover", { id: session.id }, { operationID, onProgress: value => events.push(value) }));
    assert.deepEqual(methods, ["GET"]); assert.equal(events.at(-1)?.phase, "outcome_unknown"); assert.equal(events.at(-1)?.cleanupConfirmed, false);
  });
});

test("processing uses immutable target spec despite zero result opacity and is never deleted or resubmitted", async () => {
  const events: MediaUploadProgress[] = [], methods: string[] = [];
  await fixture(async (input, init) => {
    methods.push(init?.method || "");
    if (String(input) === "/media-assets/upload-sessions") return Response.json(session);
    if (String(input) === "/media-assets") return Response.json(asset);
    if (init?.method === "GET") return receipt(nonready("processing"));
    return Response.json(nonready("processing"));
  }, async () => {
    await assert.rejects(uploadDraftMediaAsset(file(), "video_cover", undefined, { operationID, onProgress: value => events.push(value) }), /media_variant_not_ready/);
    assert.equal(events.at(-1)?.issue, "processing");
    assert.equal((await checkDraftMediaUpload(session.id, operationID, "video_cover")).kind, "processing");
    assert.deepEqual(methods, ["POST", "POST", "POST", "GET"]);
  });
});

test("a failed variant releases only its exact draft allowance; an uncertain delete stays blocked", async () => {
  for (const deleteStatus of [204, 500]) {
    const events: MediaUploadProgress[] = [], deletions: { path: string; signal?: AbortSignal | null }[] = [];
    const uploadAbort = new AbortController();
    await fixture(async (input, init) => {
      if (String(input) === "/media-assets/upload-sessions") return Response.json(session);
      if (String(input) === "/media-assets") return Response.json(asset);
      if (init?.method === "DELETE") {
        deletions.push({ path: String(input), signal: init.signal });
        return deleteStatus === 204 ? new Response(null, { status: 204 }) : Response.json({ code: "media_asset_operation_failed" }, { status: 500 });
      }
      return Response.json(nonready("failed"));
    }, async () => {
      await assert.rejects(uploadDraftMediaAsset(file(), "video_cover", undefined, { operationID, signal: uploadAbort.signal, onProgress: value => events.push(value) }), /image_processing_failed/);
      assert.equal(deletions.length, 1); assert.equal(deletions[0].path, `/media-assets/${asset.id}?expected_upload_session_id=${session.id}`);
      assert.notEqual(deletions[0].signal, uploadAbort.signal);
      assert.equal(events.at(-1)?.phase, deleteStatus === 204 ? "failed" : "outcome_unknown");
      assert.equal(events.at(-1)?.cleanupConfirmed, deleteStatus === 204 ? true : undefined);
    });
  }
});

test("invalid path identities, wrong ownership, wrong operation and claimed assets cannot form cleanup requests", async () => {
  let calls = 0;
  await fixture(async () => { calls++; throw new Error("no request expected"); }, async () => {
    for (const id of [".", "..", "asset/foreign", "asset?query", "asset#fragment", "https://foreign/"]) assert.throws(() => requireMediaID(id));
    for (const changed of [{ owner_type: "stream" }, { owner_id: "other" }, { upload_session_id: "other" }, { id: ".." }]) await assert.rejects(deleteDraftMediaAsset({ ...asset, ...changed }, session.id));
    assert.throws(() => requireDraftMediaAsset({ ...asset, client_operation_id: "00000000-0000-4000-8000-000000000000" }, session.id, "video_cover", operationID));
    assert.equal(calls, 0);
  });
});

test("claimed/expired sessions and missing or tombstoned results have distinct read-only outcomes", async () => {
  for (const kind of ["claimed", "expired", "missing", "deleted"] as const) {
    await fixture(async (_input, init) => {
      assert.equal(init?.method, "GET");
      return Response.json({ session: kind === "claimed" ? { ...session, owner_type: "stream", claimed_stream_id: "saved-stream" }
        : kind === "expired" ? { ...session, expires_at: "2000-01-01T00:00:00Z" } : session,
        operation_id: operationID, assets: kind === "missing" ? [] : [{ asset: kind === "deleted" ? { ...asset, deleted_at: "2026-10-28T00:00:00Z" } : asset, variants: [] }] });
    }, async () => { assert.equal((await checkDraftMediaUpload(session.id, operationID, "video_cover")).kind, kind); });
  }
});

test("same-target malformed readiness, ambiguous variants and foreign variant identities fail closed", async () => {
  const sameTarget = { target_width: 1920, target_height: 1080, crop_mode: "center_crop_opaque" };
  for (const variants of [
    [{ ...ready, ...sameTarget, width: 960 }],
    [ready, nonready("processing")],
    [{ ...ready, ...sameTarget, asset_id: "foreign-asset" }],
    [{ ...ready, ...sameTarget, status: "unrecognized" }],
  ]) {
    let requests = 0;
    await fixture(async (_input, init) => {
      requests++; assert.equal(init?.method, "GET");
      return Response.json({ session, operation_id: operationID, assets: [{ asset, variants }] });
    }, async () => {
      await assert.rejects(checkDraftMediaUpload(session.id, operationID, "video_cover"), /media_upload_integrity/);
      assert.equal(requests, 1);
    });
  }
});

test("an exact operation claimed by a preset or stream is displayed as claimed without any mutation", async () => {
  for (const owner_type of ["preset", "stream"] as const) {
    const claimed = { ...asset, owner_type, owner_id: `${owner_type}-one`, upload_session_id: owner_type === "preset" ? undefined : session.id };
    for (const malformed of [undefined, { client_operation_id: "00000000-0000-4000-8000-000000000001" }, { id: ".." }, { owner_id: session.id }, { upload_session_id: "foreign-session" }, { upload_session_id: 0 }, { upload_session_id: null }, { usage_type: "scene_background" }]) {
      const methods: string[] = [];
      await fixture(async (_input, init) => {
        methods.push(init?.method || "");
        return Response.json({ session, operation_id: operationID, assets: [{ asset: { ...claimed, ...malformed }, variants: [ready] }] });
      }, async () => {
        if (malformed) await assert.rejects(checkDraftMediaUpload(session.id, operationID, "video_cover"), /media_upload_integrity/);
        else assert.equal((await checkDraftMediaUpload(session.id, operationID, "video_cover")).kind, "claimed");
        assert.deepEqual(methods, ["GET"], "claimed observation must never resend, convert or delete an original");
      });
    }
  }
});
